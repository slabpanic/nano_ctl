package hardware

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nano_ctl/pkg/godmc"
)

type SDKError struct {
	Operation string `json:"operation"`
	Code      int16  `json:"code"`
}

func (e *SDKError) Error() string {
	return fmt.Sprintf("%s failed with LTDMC code %d", e.Operation, e.Code)
}

func sdkCheck(operation string, code int16) error {
	if code != 0 {
		return &SDKError{Operation: operation, Code: code}
	}
	return nil
}

type cardSet struct {
	ids   []uint16
	types []uint32
}

type Controller struct {
	exec        *executor
	open        atomic.Bool
	transition  atomic.Bool
	cards       atomic.Pointer[cardSet]
	session     atomic.Pointer[godmc.Session]
	lifecycleMu sync.Mutex
	resetDelay  time.Duration
}

func NewController() *Controller {
	workers := min(max(runtime.GOMAXPROCS(0), 2), 8)
	return &Controller{exec: newExecutor(workers), resetDelay: 16 * time.Second}
}

func (c *Controller) CloseExecutor()        { c.exec.close() }
func (c *Controller) IsOpen() bool          { return c.open.Load() }
func (c *Controller) IsTransitioning() bool { return c.transition.Load() }

func (c *Controller) Status() (bool, bool, []uint16) {
	set := c.cards.Load()
	if set == nil {
		return c.open.Load(), c.transition.Load(), nil
	}
	return c.open.Load(), c.transition.Load(), append([]uint16(nil), set.ids...)
}

func (c *Controller) hasCard(cardNo uint16) bool {
	set := c.cards.Load()
	if set == nil {
		return false
	}
	for _, id := range set.ids {
		if id == cardNo {
			return true
		}
	}
	return false
}

func (c *Controller) ensureReady(cardNo uint16) error {
	if c.transition.Load() {
		return ErrTransition
	}
	if !c.open.Load() {
		return ErrCardClosed
	}
	if !c.hasCard(cardNo) {
		return ErrCardUnknown
	}
	return nil
}

func (c *Controller) call(ctx context.Context, p Priority, cardNo uint16, fn func() error) error {
	if err := c.ensureReady(cardNo); err != nil {
		return err
	}
	return c.exec.do(ctx, p, func() error {
		if err := c.ensureReady(cardNo); err != nil {
			return err
		}
		return fn()
	})
}

func (c *Controller) readCall(ctx context.Context, p Priority, cardNo uint16, fn func() error) error {
	if err := c.ensureReady(cardNo); err != nil {
		return err
	}
	return c.exec.doRead(ctx, p, func() error {
		if err := c.ensureReady(cardNo); err != nil {
			return err
		}
		return fn()
	})
}

func (c *Controller) Open(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return c.openLocked(ctx)
}

func (c *Controller) openLocked(ctx context.Context) error {
	if c.open.Load() {
		return nil
	}
	c.transition.Store(true)
	defer c.transition.Store(false)
	var opened *godmc.Session
	var found cardSet
	err := c.exec.do(ctx, PriorityControl, func() error {
		var err error
		opened, err = godmc.Open()
		if err != nil {
			return err
		}
		for _, card := range opened.Cards() {
			found.ids = append(found.ids, card.ID)
			found.types = append(found.types, card.Type)
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.cards.Store(&found)
	c.session.Store(opened)
	c.open.Store(true)
	return nil
}

func (c *Controller) Close(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return c.closeLocked(ctx)
}

func (c *Controller) closeLocked(ctx context.Context) error {
	if !c.open.Load() {
		return nil
	}
	c.transition.Store(true)
	defer c.transition.Store(false)
	err := c.exec.do(ctx, PriorityControl, func() error {
		if session := c.session.Load(); session != nil {
			return session.Close()
		}
		return sdkCheck("board close", godmc.BoardClose())
	})
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	c.open.Store(false)
	c.cards.Store(nil)
	c.session.Store(nil)
	return err
}

func (c *Controller) Reset(ctx context.Context, cardNo uint16) error {
	return c.reset(ctx, cardNo, false, 0)
}

func (c *Controller) SoftReset(ctx context.Context, cardNo uint16, timeout time.Duration) error {
	return c.reset(ctx, cardNo, true, timeout)
}

func (c *Controller) reset(ctx context.Context, cardNo uint16, soft bool, timeout time.Duration) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if err := c.ensureReady(cardNo); err != nil {
		return err
	}
	c.transition.Store(true)
	defer c.transition.Store(false)
	if soft {
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		if err := c.exec.do(ctx, PriorityControl, func() error { return sdkCheck("soft reset", godmc.SoftReset(cardNo)) }); err != nil {
			return err
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			var busErr uint16
			err := c.exec.do(context.Background(), PriorityControl, func() error {
				return sdkCheck("read bus error during soft reset", godmc.NmcGetErrCode(cardNo, 2, &busErr))
			})
			if err == nil && busErr == 0 {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
		}
		return fmt.Errorf("soft reset timed out after %s", timeout)
	}
	err := c.exec.do(ctx, PriorityControl, func() error {
		if err := sdkCheck("cool reset", godmc.CoolReset(cardNo)); err != nil {
			return err
		}
		if session := c.session.Load(); session != nil {
			return session.Close()
		}
		return sdkCheck("board close", godmc.BoardClose())
	})
	if err != nil {
		c.closeNativeAfterFailure()
		return err
	}
	c.open.Store(false)
	c.cards.Store(nil)
	c.session.Store(nil)
	// Keep the request path responsive and reject gRPC work during the device
	// reset window. The worker remains free for unrelated process work.
	timer := time.NewTimer(c.resetDelay)
	defer timer.Stop()
	<-timer.C
	var reopened *godmc.Session
	var reopenedCards cardSet
	err = c.exec.do(context.Background(), PriorityControl, func() error {
		var err error
		reopened, err = godmc.Open()
		if err != nil {
			return err
		}
		for _, card := range reopened.Cards() {
			reopenedCards.ids = append(reopenedCards.ids, card.ID)
			reopenedCards.types = append(reopenedCards.types, card.Type)
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.cards.Store(&reopenedCards)
	c.session.Store(reopened)
	c.open.Store(true)
	return nil
}

// closeNativeAfterFailure leaves the software state closed after an operation
// that may have partially reset or reconfigured the controller.
func (c *Controller) closeNativeAfterFailure() {
	_ = c.exec.do(context.Background(), PriorityControl, func() error {
		if session := c.session.Load(); session != nil {
			return session.Close()
		}
		return sdkCheck("board close", godmc.BoardClose())
	})
	c.open.Store(false)
	c.cards.Store(nil)
	c.session.Store(nil)
}

type CardInfo struct {
	CardNo           uint16    `json:"card_no"`
	Type             uint32    `json:"type"`
	HardwareVersion  uint32    `json:"hardware_version"`
	FirmwareID       uint32    `json:"firmware_id"`
	SubFirmwareID    uint32    `json:"sub_firmware_id"`
	ReleaseVersion   string    `json:"release_version"`
	LibraryVersion   uint32    `json:"library_version"`
	LocalAxisCount   uint32    `json:"local_axis_count"`
	BusAxisCount     uint32    `json:"bus_axis_count"`
	LocalInputCount  uint16    `json:"local_input_count"`
	LocalOutputCount uint16    `json:"local_output_count"`
	BusInputCount    uint16    `json:"bus_input_count"`
	BusOutputCount   uint16    `json:"bus_output_count"`
	SlaveCount       uint16    `json:"slave_count"`
	BusErrorCode     uint16    `json:"bus_error_code"`
	CycleTimeMicros  int32     `json:"cycle_time_micros"`
	CycleTimeSDKCode int16     `json:"cycle_time_sdk_code"`
	AxisEquivalences []float64 `json:"axis_equivalences,omitempty"`
}

func (c *Controller) CardInfo(ctx context.Context, cardNo, portNum uint16) (CardInfo, error) {
	var out CardInfo
	err := c.readCall(ctx, PriorityControl, cardNo, func() error {
		set := c.cards.Load()
		for i, id := range set.ids {
			if id == cardNo {
				out.Type = set.types[i]
				break
			}
		}
		out.CardNo = cardNo
		if code := godmc.GetCardVersion(cardNo, &out.HardwareVersion); code != 0 {
			return &SDKError{"get card version", code}
		}
		if code := godmc.GetCardSoftVersion(cardNo, &out.FirmwareID, &out.SubFirmwareID); code != 0 {
			return &SDKError{"get card firmware version", code}
		}
		if code := godmc.GetCardLibVersion(&out.LibraryVersion); code != 0 {
			return &SDKError{"get library version", code}
		}
		version := make([]byte, 128)
		if code := godmc.GetReleaseVersion(cardNo, version); code != 0 {
			return &SDKError{"get release version", code}
		}
		out.ReleaseVersion = strings.TrimRight(string(version), "\x00")
		if code := godmc.GetTotalAxes(cardNo, &out.LocalAxisCount); code != 0 {
			return &SDKError{"get local axis count", code}
		}
		if code := godmc.NmcGetTotalAxes(cardNo, &out.BusAxisCount); code != 0 {
			return &SDKError{"get bus axis count", code}
		}
		if code := godmc.GetTotalIONum(cardNo, &out.LocalInputCount, &out.LocalOutputCount); code != 0 {
			return &SDKError{"get local io counts", code}
		}
		if code := godmc.NmcGetTotalIONum(cardNo, &out.BusInputCount, &out.BusOutputCount); code != 0 {
			return &SDKError{"get bus io counts", code}
		}
		if code := godmc.NmcGetTotalSlaves(cardNo, portNum, &out.SlaveCount); code != 0 {
			return &SDKError{"get slave count", code}
		}
		if code := godmc.NmcGetErrCode(cardNo, portNum, &out.BusErrorCode); code != 0 {
			return &SDKError{"get bus error code", code}
		}
		if code := godmc.NmcGetCycleTime(cardNo, portNum, &out.CycleTimeMicros); code != 0 {
			out.CycleTimeSDKCode = code
		}
		if out.BusAxisCount <= 64 {
			out.AxisEquivalences = make([]float64, out.BusAxisCount)
			for axis := uint32(0); axis < out.BusAxisCount; axis++ {
				if code := godmc.GetEquiv(cardNo, uint16(axis), &out.AxisEquivalences[axis]); code != 0 {
					return &SDKError{"get axis equivalence", code}
				}
			}
		}
		return nil
	})
	return out, err
}

type AxisSnapshot struct {
	CardNo          uint16  `json:"card_no"`
	Axis            uint16  `json:"axis"`
	Equivalence     float64 `json:"equivalence"`
	EncoderPosition float64 `json:"encoder_position"`
	TargetPosition  int32   `json:"target_position"`
	CurrentSpeed    float64 `json:"current_speed"`
	Done            bool    `json:"done"`
	IOStatus        uint32  `json:"io_status"`
	StopReason      int32   `json:"stop_reason"`
	HomingStatus    uint16  `json:"homing_status"`
	StateMachine    uint16  `json:"state_machine"`
	ErrorCode       uint16  `json:"error_code"`
	Torque          int32   `json:"torque"`
	OffsetPosition  float64 `json:"offset_position"`
	FieldsRead      uint32  `json:"fields_read"`
}

type AxisReadMask uint32

const (
	AxisReadEncoder AxisReadMask = 1 << iota
	AxisReadTarget
	AxisReadSpeed
	AxisReadDone
	AxisReadIOStatus
	AxisReadStateMachine
	AxisReadEquivalence
	AxisReadStopReason
	AxisReadHomingStatus
	AxisReadErrorCode
	AxisReadTorque
	AxisReadOffset
	AxisReadCommon = AxisReadEncoder | AxisReadTarget | AxisReadSpeed | AxisReadDone | AxisReadIOStatus | AxisReadStateMachine
	AxisReadAll    = AxisReadCommon | AxisReadEquivalence | AxisReadStopReason | AxisReadHomingStatus | AxisReadErrorCode | AxisReadTorque | AxisReadOffset
)

func normalizedReadMask(mask AxisReadMask) AxisReadMask {
	if mask == 0 {
		return AxisReadCommon
	}
	return mask & AxisReadAll
}

func (c *Controller) ReadAxis(ctx context.Context, cardNo, axis uint16, mask AxisReadMask, p Priority) (AxisSnapshot, error) {
	var out AxisSnapshot
	err := c.readCall(ctx, p, cardNo, func() error {
		var err error
		out, err = readAxisUnsafe(cardNo, axis, mask)
		return err
	})
	return out, err
}

func readAxisUnsafe(cardNo, axis uint16, mask AxisReadMask) (AxisSnapshot, error) {
	var out AxisSnapshot
	mask = normalizedReadMask(mask)
	out.CardNo, out.Axis = cardNo, axis
	out.FieldsRead = uint32(mask)
	if mask&AxisReadEquivalence != 0 {
		if code := godmc.GetEquiv(cardNo, axis, &out.Equivalence); code != 0 {
			return out, &SDKError{"get axis equivalence", code}
		}
	}
	if mask&AxisReadEncoder != 0 {
		if code := godmc.GetEncoderUnit(cardNo, axis, &out.EncoderPosition); code != 0 {
			return out, &SDKError{"read encoder position", code}
		}
	}
	if mask&AxisReadTarget != 0 {
		out.TargetPosition = godmc.GetTargetPosition(cardNo, axis)
	}
	if mask&AxisReadSpeed != 0 {
		if code := godmc.ReadCurrentSpeedUnit(cardNo, axis, &out.CurrentSpeed); code != 0 {
			return out, &SDKError{"read current speed", code}
		}
	}
	if mask&AxisReadDone != 0 {
		out.Done = godmc.CheckDone(cardNo, axis) == 1
	}
	if mask&AxisReadIOStatus != 0 {
		out.IOStatus = godmc.AxisIOStatus(cardNo, axis)
	}
	if mask&AxisReadStopReason != 0 {
		if code := godmc.GetStopReason(cardNo, axis, &out.StopReason); code != 0 {
			return out, &SDKError{"get stop reason", code}
		}
	}
	if mask&AxisReadHomingStatus != 0 {
		if code := godmc.GetHomeResult(cardNo, axis, &out.HomingStatus); code != 0 {
			return out, &SDKError{"get homing status", code}
		}
	}
	if mask&AxisReadStateMachine != 0 {
		if code := godmc.NmcGetAxisStateMachine(cardNo, axis, &out.StateMachine); code != 0 {
			return out, &SDKError{"get axis state machine", code}
		}
	}
	if mask&AxisReadErrorCode != 0 {
		if code := godmc.NmcGetAxisErrCode(cardNo, axis, &out.ErrorCode); code != 0 {
			return out, &SDKError{"get axis error code", code}
		}
	}
	if mask&AxisReadTorque != 0 {
		if code := godmc.NmcGetTorque(cardNo, axis, &out.Torque); code != 0 {
			return out, &SDKError{"get axis torque", code}
		}
	}
	if mask&AxisReadOffset != 0 {
		if code := godmc.NmcGetOffsetPos(cardNo, axis, &out.OffsetPosition); code != 0 {
			return out, &SDKError{"get axis offset", code}
		}
	}
	return out, nil
}

func (c *Controller) ReadAxes(ctx context.Context, cardNo uint16, axes []uint16, mask AxisReadMask, p Priority) ([]AxisSnapshot, error) {
	if len(axes) == 0 || len(axes) > 64 {
		return nil, errors.New("axes must contain between 1 and 64 entries")
	}
	out := make([]AxisSnapshot, len(axes))
	err := parallel(ctx, c.exec.workers, len(axes), func(ctx context.Context, i int) error {
		return c.readCall(ctx, p, cardNo, func() error {
			v, err := readAxisUnsafe(cardNo, axes[i], mask)
			if err == nil {
				out[i] = v
			}
			return err
		})
	})
	return out, err
}

func (c *Controller) ReadAxisEquivalence(ctx context.Context, cardNo, axis uint16) (float64, error) {
	var value float64
	err := c.readCall(ctx, PriorityControl, cardNo, func() error { return sdkCheck("get axis equivalence", godmc.GetEquiv(cardNo, axis, &value)) })
	return value, err
}

func (c *Controller) SetAxisEquivalence(ctx context.Context, cardNo, axis uint16, value float64) error {
	if value <= 0 || !finite(value) {
		return errors.New("equivalence must be finite and greater than zero")
	}
	return c.call(ctx, PriorityControl, cardNo, func() error { return sdkCheck("set axis equivalence", godmc.SetEquiv(cardNo, axis, value)) })
}

type AxisAction uint8

const (
	ActionEnable AxisAction = iota + 1
	ActionDisable
	ActionStop
	ActionEmergencyStop
	ActionClearFault
	ActionClearStopReason
	ActionZeroPosition
	ActionJog
	ActionMove
	ActionHome
	ActionHomeStop
	ActionChangeSpeed
	ActionChangeTarget
	ActionSetEquivalence
	ActionSetOffset
)

type AxisCommand struct {
	CardNo         uint16
	Axis           uint16
	Action         AxisAction
	Position       float64
	Speed          float64
	Acceleration   float64
	Deceleration   float64
	Direction      uint16
	PositionMode   uint16
	StopMode       uint16
	HomeMethod     uint16
	SecondarySpeed float64
	Offset         float64
	ProbeFunction  uint16
	Equivalence    float64
}

// enableAxis follows the HardwareOrchestrator enable sequence while yielding
// between polls so other real-time reads and commands can use the SDK worker.
func (c *Controller) enableAxis(ctx context.Context, cardNo, axis uint16, p Priority) error {
	var busErr, state uint16
	err := c.readCall(ctx, p, cardNo, func() error {
		if code := godmc.NmcGetErrCode(cardNo, 2, &busErr); code != 0 {
			return &SDKError{"get bus error code", code}
		}
		if busErr != 0 {
			return &SDKError{fmt.Sprintf("bus error %d prevents axis enable", busErr), -1000}
		}
		return sdkCheck("get axis state machine", godmc.NmcGetAxisStateMachine(cardNo, axis, &state))
	})
	if err != nil || state == 4 {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = c.call(ctx, p, cardNo, func() error {
			if code := godmc.NmcSetAxisEnable(cardNo, axis); code != 0 {
				return &SDKError{"enable axis", code}
			}
			return sdkCheck("get axis state machine", godmc.NmcGetAxisStateMachine(cardNo, axis, &state))
		})
		if err != nil || state == 4 {
			break
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return c.call(ctx, p, cardNo, func() error {
		return sdkCheck("enable axis", godmc.NmcSetAxisEnable(cardNo, axis))
	})
}

func (c *Controller) AxisCommand(ctx context.Context, cmd AxisCommand, p Priority) error {
	if cmd.Action == ActionEnable {
		return c.enableAxis(ctx, cmd.CardNo, cmd.Axis, p)
	}
	return c.call(ctx, p, cmd.CardNo, func() error {
		card, axis := cmd.CardNo, cmd.Axis
		check := func(name string, code int16) error { return sdkCheck(name, code) }
		switch cmd.Action {
		case ActionDisable:
			return check("disable axis", godmc.NmcSetAxisDisable(card, axis))
		case ActionStop, ActionEmergencyStop, ActionHomeStop:
			mode := cmd.StopMode
			if cmd.Action == ActionEmergencyStop {
				mode = 1
			}
			if mode > 1 {
				return errors.New("stop mode must be 0 or 1")
			}
			if err := check("set deceleration stop time", godmc.SetDecStopTime(card, axis, 0.5)); err != nil {
				return err
			}
			return check("stop axis", godmc.Stop(card, axis, mode))
		case ActionClearFault:
			return check("clear axis error", godmc.NmcClearAxisErrCode(card, axis))
		case ActionClearStopReason:
			return check("clear stop reason", godmc.ClearStopReason(card, axis))
		case ActionZeroPosition:
			return check("zero axis position", godmc.SetPositionUnit(card, axis, 0))
		case ActionJog, ActionMove:
			if cmd.Speed <= 0 || cmd.Acceleration <= 0 || cmd.Deceleration <= 0 || !finite(cmd.Speed) || !finite(cmd.Acceleration) || !finite(cmd.Deceleration) || !finite(cmd.Position) {
				return errors.New("speed, acceleration and deceleration must be finite and greater than zero")
			}
			tacc, tdec := math.Abs(cmd.Speed/cmd.Acceleration), math.Abs(cmd.Speed/cmd.Deceleration)
			if err := check("set motion profile", godmc.SetProfileUnit(card, axis, 0, cmd.Speed, tacc, tdec, 0)); err != nil {
				return err
			}
			if err := check("set S profile", godmc.SetSProfile(card, axis, 0, 0.1)); err != nil {
				return err
			}
			if cmd.Action == ActionJog {
				if cmd.Direction > 1 {
					return errors.New("direction must be 0 or 1")
				}
				return check("jog axis", godmc.Vmove(card, axis, cmd.Direction))
			}
			if cmd.PositionMode > 1 {
				return errors.New("position mode must be 0 (relative) or 1 (absolute)")
			}
			return check("move axis", godmc.PmoveUnit(card, axis, cmd.Position, cmd.PositionMode))
		case ActionHome:
			if cmd.Speed <= 0 || cmd.Acceleration <= 0 || !finite(cmd.Speed) || !finite(cmd.SecondarySpeed) || !finite(cmd.Acceleration) || !finite(cmd.Offset) {
				return errors.New("homing speed and acceleration must be greater than zero")
			}
			t := math.Abs(cmd.Speed / cmd.Acceleration)
			if err := check("set homing profile", godmc.NmcSetHomeProfile(card, axis, cmd.HomeMethod, cmd.Speed, cmd.SecondarySpeed, t, t, cmd.Offset)); err != nil {
				return err
			}
			return check("start homing", godmc.NmcHomeMove(card, axis))
		case ActionChangeSpeed:
			if cmd.Acceleration <= 0 || !finite(cmd.Speed) || !finite(cmd.Acceleration) {
				return errors.New("acceleration must be greater than zero")
			}
			return check("change speed", godmc.ChangeSpeedUnit(card, axis, cmd.Speed, math.Abs(cmd.Speed/cmd.Acceleration)))
		case ActionChangeTarget:
			if !finite(cmd.Position) {
				return errors.New("target position must be finite")
			}
			return check("change target position", godmc.ResetTargetPositionUnit(card, axis, cmd.Position))
		case ActionSetEquivalence:
			if cmd.Equivalence <= 0 || math.IsNaN(cmd.Equivalence) || math.IsInf(cmd.Equivalence, 0) {
				return errors.New("equivalence must be finite and greater than zero")
			}
			return check("set axis equivalence", godmc.SetEquiv(card, axis, cmd.Equivalence))
		case ActionSetOffset:
			if !finite(cmd.Offset) {
				return errors.New("axis offset must be finite")
			}
			return check("set axis offset", godmc.NmcSetOffsetPos(card, axis, cmd.Offset))
		default:
			return fmt.Errorf("unsupported axis action %d", cmd.Action)
		}
	})
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

type IOKind uint8

const (
	IOInputBit IOKind = iota + 1
	IOOutputBit
	IOInputPort
	IOOutputPort
	IOAnalogInput
	IOAnalogOutput
)

type IOValue struct {
	Address     uint32  `json:"address"`
	RawValue    uint32  `json:"raw_value"`
	AnalogValue float64 `json:"analog_value"`
	On          bool    `json:"on"`
}

func (c *Controller) ReadIO(ctx context.Context, cardNo uint16, kind IOKind, addresses []uint32, p Priority) ([]IOValue, error) {
	if len(addresses) == 0 || len(addresses) > 4096 {
		return nil, errors.New("addresses must contain between 1 and 4096 entries")
	}
	if kind < IOInputBit || kind > IOAnalogOutput {
		return nil, fmt.Errorf("unsupported io kind %d", kind)
	}
	for _, addr := range addresses {
		if addr > math.MaxUint16 {
			return nil, fmt.Errorf("io address %d exceeds SDK range", addr)
		}
	}
	out := make([]IOValue, len(addresses))
	err := parallel(ctx, c.exec.workers, len(addresses), func(ctx context.Context, i int) error {
		return c.readCall(ctx, p, cardNo, func() error {
			v, err := readIOValueUnsafe(cardNo, kind, addresses[i])
			if err == nil {
				out[i] = v
			}
			return err
		})
	})
	return out, err
}

func readIOValueUnsafe(cardNo uint16, kind IOKind, address uint32) (IOValue, error) {
	v := IOValue{Address: address}
	switch kind {
	case IOInputBit:
		bit := godmc.ReadInBit(cardNo, uint16(address))
		if bit < 0 {
			return v, &SDKError{"read input bit", bit}
		}
		v.RawValue = uint32(bit)
	case IOOutputBit:
		bit := godmc.ReadOutBit(cardNo, uint16(address))
		if bit < 0 {
			return v, &SDKError{"read output bit", bit}
		}
		v.RawValue = uint32(bit)
	case IOInputPort:
		v.RawValue = godmc.ReadInPort(cardNo, uint16(address))
	case IOOutputPort:
		v.RawValue = godmc.ReadOutPort(cardNo, uint16(address))
	case IOAnalogInput:
		if code := godmc.GetADInput(cardNo, uint16(address), &v.AnalogValue); code != 0 {
			return v, &SDKError{"read analog input", code}
		}
	case IOAnalogOutput:
		if code := godmc.GetDAOutput(cardNo, uint16(address), &v.AnalogValue); code != 0 {
			return v, &SDKError{"read analog output", code}
		}
	}
	v.On = v.RawValue != 0
	return v, nil
}

func (c *Controller) ReadIOArray(ctx context.Context, cardNo, port uint16, length uint32, outputs bool, p Priority) ([]uint32, error) {
	if length == 0 || length > 4096 {
		return nil, errors.New("IO array length must be between 1 and 4096")
	}
	values := make([]uint32, length)
	err := c.readCall(ctx, p, cardNo, func() error {
		var code int16
		if outputs {
			code = godmc.ReadOutPortArray(cardNo, port, values)
		} else {
			code = godmc.ReadInPortArray(cardNo, port, values)
		}
		if code != 0 {
			return &SDKError{"read IO array", code}
		}
		return nil
	})
	return values, err
}

func (c *Controller) WriteIO(ctx context.Context, cardNo uint16, kind IOKind, address, raw uint32, analog float64, on bool, reverseMillis uint32, p Priority) error {
	if address > math.MaxUint16 {
		return fmt.Errorf("io address %d exceeds SDK range", address)
	}
	return c.call(ctx, p, cardNo, func() error {
		bit := uint16(0)
		if on {
			bit = 1
		}
		switch kind {
		case IOOutputBit:
			if reverseMillis > 0 {
				return sdkCheck("reverse output bit", godmc.ReverseOutBit(cardNo, uint16(address), float64(reverseMillis)))
			}
			return sdkCheck("write output bit", godmc.WriteOutBit(cardNo, uint16(address), bit))
		case IOOutputPort:
			return sdkCheck("write output port", godmc.WriteOutPort(cardNo, uint16(address), raw))
		case IOAnalogOutput:
			return fmt.Errorf("analog output must be written through mapped RxPDO")
		case IOInputBit, IOInputPort, IOAnalogInput:
			return fmt.Errorf("io kind %d is read-only", kind)
		default:
			if reverseMillis > 0 {
				return sdkCheck("reverse output bit", godmc.ReverseOutBit(cardNo, uint16(address), float64(reverseMillis)))
			}
			return fmt.Errorf("unsupported io kind %d", kind)
		}
	})
}

type BusSnapshot struct {
	CardNo             uint16 `json:"card_no"`
	PortNum            uint16 `json:"port_num"`
	AxisCount          uint32 `json:"axis_count"`
	SlaveCount         uint16 `json:"slave_count"`
	InputCount         uint16 `json:"input_count"`
	OutputCount        uint16 `json:"output_count"`
	ErrorCode          uint16 `json:"error_code"`
	CycleTimeMicros    int32  `json:"cycle_time_micros"`
	CycleTimeSDKCode   int16  `json:"cycle_time_sdk_code"`
	AverageCycleMicros uint32 `json:"average_cycle_micros"`
	MaxCycleMicros     uint32 `json:"max_cycle_micros"`
	TotalCycles        uint64 `json:"total_cycles"`
	ConsumeSDKCode     int16  `json:"consume_sdk_code"`
}

func (c *Controller) ReadBus(ctx context.Context, cardNo, port uint16, p Priority) (BusSnapshot, error) {
	var out BusSnapshot
	err := c.readCall(ctx, p, cardNo, func() error {
		out.CardNo, out.PortNum = cardNo, port
		if code := godmc.NmcGetTotalAxes(cardNo, &out.AxisCount); code != 0 {
			return &SDKError{"get bus axis count", code}
		}
		if code := godmc.NmcGetTotalSlaves(cardNo, port, &out.SlaveCount); code != 0 {
			return &SDKError{"get slave count", code}
		}
		if code := godmc.NmcGetTotalIONum(cardNo, &out.InputCount, &out.OutputCount); code != 0 {
			return &SDKError{"get bus io counts", code}
		}
		if code := godmc.NmcGetErrCode(cardNo, port, &out.ErrorCode); code != 0 {
			return &SDKError{"get bus error code", code}
		}
		out.CycleTimeSDKCode = godmc.NmcGetCycleTime(cardNo, port, &out.CycleTimeMicros)
		out.ConsumeSDKCode = godmc.NmcGetConsumeTimeFieldbus(cardNo, port, &out.AverageCycleMicros, &out.MaxCycleMicros, &out.TotalCycles)
		return nil
	})
	return out, err
}

func (c *Controller) SetCycleTime(ctx context.Context, cardNo, port uint16, cycleMicros uint32) error {
	if port != 2 {
		return errors.New("EtherCAT port number must be 2")
	}
	if cycleMicros != 250 && cycleMicros != 500 && cycleMicros != 1000 && cycleMicros != 2000 {
		return errors.New("cycle time must be one of 250, 500, 1000 or 2000 microseconds")
	}
	return c.call(ctx, PriorityControl, cardNo, func() error { return sdkCheck("set cycle time", godmc.NmcSetCycleTime(cardNo, port, cycleMicros)) })
}

func (c *Controller) ReadOD(ctx context.Context, cardNo, port, node, index, sub, length uint16, p Priority) (int32, error) {
	var value int32
	err := c.readCall(ctx, p, cardNo, func() error {
		return sdkCheck("read object dictionary", godmc.NmcGetNodeOD(cardNo, port, node, index, sub, length, &value))
	})
	return value, err
}
func (c *Controller) WriteOD(ctx context.Context, cardNo, port, node, index, sub, length uint16, value int32, p Priority) error {
	return c.call(ctx, p, cardNo, func() error {
		return sdkCheck("write object dictionary", godmc.NmcSetNodeOD(cardNo, port, node, index, sub, length, value))
	})
}
func (c *Controller) ReadPDO(ctx context.Context, cardNo, port, address, length uint16, rx bool, p Priority) (int32, error) {
	var value int32
	err := c.readCall(ctx, p, cardNo, func() error {
		if rx {
			return sdkCheck("read RxPDO", godmc.NmcReadRxPDOExtra(cardNo, port, address, length, &value))
		}
		return sdkCheck("read TxPDO", godmc.NmcReadTxPDOExtra(cardNo, port, address, length, &value))
	})
	return value, err
}
func (c *Controller) WritePDO(ctx context.Context, cardNo, port, address, length uint16, value int32, p Priority) error {
	return c.call(ctx, p, cardNo, func() error {
		return sdkCheck("write RxPDO", godmc.NmcWriteRxPDOExtra(cardNo, port, address, length, value))
	})
}

type HcmpCommand struct {
	CardNo, Channel                  uint16
	Action                           uint8
	Mode, Axis, Source, Logic        uint16
	Time, Increment, Count, Position int32
}
type HcmpSnapshot struct {
	CardNo, Channel, Mode, Axis, Source, Logic                       uint16
	Time, Increment, Count, RemainingPoints, CurrentPoint, RunPoints int32
}

func (c *Controller) Hcmp(ctx context.Context, cmd HcmpCommand, p Priority) (HcmpSnapshot, error) {
	var out HcmpSnapshot
	call := func() error {
		out.CardNo, out.Channel = cmd.CardNo, cmd.Channel
		switch cmd.Action {
		case 1:
			if code := godmc.HcmpSetMode(cmd.CardNo, cmd.Channel, cmd.Mode); code != 0 {
				return &SDKError{"set hcmp mode", code}
			}
			if code := godmc.HcmpSetConfig(cmd.CardNo, cmd.Channel, cmd.Axis, cmd.Source, cmd.Logic, cmd.Time); code != 0 {
				return &SDKError{"set hcmp config", code}
			}
		case 2:
			if code := godmc.HcmpSetLiner(cmd.CardNo, cmd.Channel, cmd.Increment, cmd.Count); code != 0 {
				return &SDKError{"set hcmp linear config", code}
			}
		case 3:
			if code := godmc.HcmpAddPoint(cmd.CardNo, cmd.Channel, cmd.Position); code != 0 {
				return &SDKError{"add hcmp point", code}
			}
		case 4:
			if code := godmc.HcmpClearPoints(cmd.CardNo, cmd.Channel); code != 0 {
				return &SDKError{"clear hcmp points", code}
			}
		case 0:
		default:
			return fmt.Errorf("unsupported hcmp action %d", cmd.Action)
		}
		if cmd.Action != 0 {
			return nil
		}
		if code := godmc.HcmpGetMode(cmd.CardNo, cmd.Channel, &out.Mode); code != 0 {
			return &SDKError{"get hcmp mode", code}
		}
		if code := godmc.HcmpGetConfig(cmd.CardNo, cmd.Channel, &out.Axis, &out.Source, &out.Logic, &out.Time); code != 0 {
			return &SDKError{"get hcmp config", code}
		}
		if code := godmc.HcmpGetLiner(cmd.CardNo, cmd.Channel, &out.Increment, &out.Count); code != 0 {
			return &SDKError{"get hcmp linear config", code}
		}
		if code := godmc.HcmpGetCurrentState(cmd.CardNo, cmd.Channel, &out.RemainingPoints, &out.CurrentPoint, &out.RunPoints); code != 0 {
			return &SDKError{"get hcmp state", code}
		}
		return nil
	}
	var err error
	if cmd.Action == 0 {
		err = c.readCall(ctx, p, cmd.CardNo, call)
	} else {
		err = c.call(ctx, p, cmd.CardNo, call)
	}
	return out, err
}

type ExtraEncoderCommand struct {
	CardNo, Channel       uint16
	Action                uint8
	InputMode, Multiplier uint16
	Position              int32
}
type ExtraEncoderSnapshot struct {
	CardNo, Channel, InputMode, Multiplier uint16
	Position                               int32
}

func (c *Controller) ExtraEncoder(ctx context.Context, cmd ExtraEncoderCommand, p Priority) (ExtraEncoderSnapshot, error) {
	var out ExtraEncoderSnapshot
	call := func() error {
		out.CardNo, out.Channel = cmd.CardNo, cmd.Channel
		switch cmd.Action {
		case 1:
			if code := godmc.SetExtraEncoderMode(cmd.CardNo, cmd.Channel, cmd.InputMode, cmd.Multiplier); code != 0 {
				return &SDKError{"set extra encoder mode", code}
			}
		case 2:
			if code := godmc.SetExtraEncoder(cmd.CardNo, cmd.Channel, cmd.Position); code != 0 {
				return &SDKError{"set extra encoder position", code}
			}
		case 0:
		default:
			return fmt.Errorf("unsupported extra encoder action %d", cmd.Action)
		}
		if cmd.Action != 0 {
			return nil
		}
		if code := godmc.GetExtraEncoderMode(cmd.CardNo, cmd.Channel, &out.InputMode, &out.Multiplier); code != 0 {
			return &SDKError{"get extra encoder mode", code}
		}
		if code := godmc.GetExtraEncoder(cmd.CardNo, cmd.Channel, &out.Position); code != 0 {
			return &SDKError{"get extra encoder position", code}
		}
		return nil
	}
	var err error
	if cmd.Action == 0 {
		err = c.readCall(ctx, p, cmd.CardNo, call)
	} else {
		err = c.call(ctx, p, cmd.CardNo, call)
	}
	return out, err
}

func (c *Controller) StopBus(ctx context.Context, cardNo uint16, p Priority) (uint16, error) {
	var state uint16
	err := c.call(ctx, p, cardNo, func() error { return sdkCheck("stop EtherCAT", godmc.NmcStopEtc(cardNo, &state)) })
	return state, err
}

func (c *Controller) ClearBusError(ctx context.Context, cardNo, port uint16, p Priority) error {
	return c.call(ctx, p, cardNo, func() error { return sdkCheck("clear EtherCAT error", godmc.NmcClearErrCode(cardNo, port)) })
}

// ApplyBusConfig downloads INI and ENI from memory to the controller and
// restarts the master. While the
// controller is transitioning, real-time RPCs fail fast instead of waiting
// behind the reset interval or observing a half-applied bus configuration.
func (c *Controller) ApplyBusConfig(ctx context.Context, cardNo uint16, eni, ini []byte, cycleMicros uint32) error {
	if len(eni) == 0 || len(ini) == 0 {
		return errors.New("both ENI and INI files are required")
	}
	if len(eni) > 16<<20 || len(ini) > 4<<20 {
		return errors.New("configuration file exceeds size limit")
	}
	if cycleMicros != 250 && cycleMicros != 500 && cycleMicros != 1000 && cycleMicros != 2000 {
		return errors.New("unsupported EtherCAT cycle time")
	}
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if err := c.ensureReady(cardNo); err != nil {
		return err
	}
	c.transition.Store(true)
	defer c.transition.Store(false)
	if err := c.exec.do(ctx, PriorityControl, func() error {
		if err := sdkCheck("set cycle time", godmc.NmcSetCycleTime(cardNo, 2, cycleMicros)); err != nil {
			return err
		}
		if err := sdkCheck("download INI", godmc.DownloadMemFile(cardNo, ini, []byte{0}, 201)); err != nil {
			return err
		}
		return sdkCheck("download ENI", godmc.DownloadMemFile(cardNo, eni, []byte{0}, 200))
	}); err != nil {
		c.closeNativeAfterFailure()
		return err
	}
	if err := c.exec.do(context.Background(), PriorityControl, func() error {
		if err := sdkCheck("cool reset", godmc.CoolReset(cardNo)); err != nil {
			return err
		}
		if session := c.session.Load(); session != nil {
			return session.Close()
		}
		return sdkCheck("board close", godmc.BoardClose())
	}); err != nil {
		c.closeNativeAfterFailure()
		return err
	}
	c.open.Store(false)
	c.cards.Store(nil)
	c.session.Store(nil)
	timer := time.NewTimer(c.resetDelay)
	defer timer.Stop()
	<-timer.C
	var reopened *godmc.Session
	var reopenedCards cardSet
	err := c.exec.do(context.Background(), PriorityControl, func() error {
		var err error
		reopened, err = godmc.Open()
		if err != nil {
			return err
		}
		for _, card := range reopened.Cards() {
			reopenedCards.ids = append(reopenedCards.ids, card.ID)
			reopenedCards.types = append(reopenedCards.types, card.Type)
		}
		var busErr uint16
		if code := godmc.NmcGetErrCode(cardNo, 2, &busErr); code != 0 {
			_ = reopened.Close()
			return &SDKError{"get bus error after config download", code}
		}
		if busErr != 0 {
			_ = reopened.Close()
			return fmt.Errorf("EtherCAT reported error 0x%04x after config download", busErr)
		}
		return nil
	})
	if err != nil {
		c.closeNativeAfterFailure()
		return err
	}
	c.cards.Store(&reopenedCards)
	c.session.Store(reopened)
	c.open.Store(true)
	return nil
}
