package godmc

import (
	"errors"
	"fmt"
)

var ErrSessionClosed = errors.New("godmc session is closed")

// SDKError preserves the original LTDMC return code and names the operation.
type SDKError struct {
	Operation string
	Code      int16
}

func (e *SDKError) Error() string { return fmt.Sprintf("%s: LTDMC code %d", e.Operation, e.Code) }

func checked(operation string, code int16) error {
	if code != 0 {
		return &SDKError{Operation: operation, Code: code}
	}
	return nil
}

// Session owns one dmc_board_init/dmc_board_close lifetime. The vendor SDK is
// process-global and does not document thread-safety; callers must synchronize
// direct Session/Card calls. nano_ctl bounds concurrent reads and excludes
// writes through its hardware controller.
type Session struct {
	cardIDs   []uint16
	cardTypes []uint32
	closed    bool
}

// Open initializes the LTDMC library and enumerates the discovered cards.
func Open() (*Session, error) {
	count := BoardInit()
	if count <= 0 || count > 8 {
		_ = BoardClose()
		return nil, fmt.Errorf("board init returned card count %d", count)
	}
	var total uint16
	var types [8]uint32
	var ids [8]uint16
	if err := checked("get card info list", GetCardInfoList(&total, &types, &ids)); err != nil {
		_ = BoardClose()
		return nil, err
	}
	if total == 0 || total > 8 {
		_ = BoardClose()
		return nil, fmt.Errorf("card info list returned invalid count %d", total)
	}
	return &Session{cardIDs: append([]uint16(nil), ids[:total]...), cardTypes: append([]uint32(nil), types[:total]...)}, nil
}

// Cards returns the enumerated card handles without exposing SDK buffers.
func (s *Session) Cards() []Card {
	if s == nil || s.closed {
		return nil
	}
	out := make([]Card, len(s.cardIDs))
	for i, id := range s.cardIDs {
		out[i] = Card{session: s, ID: id, Type: s.cardTypes[i]}
	}
	return out
}

// Close releases the process-global card connection.
func (s *Session) Close() error {
	if s == nil || s.closed {
		return nil
	}
	if err := checked("board close", BoardClose()); err != nil {
		return err
	}
	s.closed = true
	return nil
}

func (s *Session) checkOpen() error {
	if s == nil || s.closed {
		return ErrSessionClosed
	}
	return nil
}
func (s *Session) Reset(cardNo uint16) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	return checked("cool reset", CoolReset(cardNo))
}
func (s *Session) SoftReset(cardNo uint16) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	return checked("soft reset", SoftReset(cardNo))
}

// Card is a typed handle to a card discovered by its Session.
type Card struct {
	session *Session
	ID      uint16
	Type    uint32
}

func (c Card) ready() error {
	if c.session == nil || c.session.closed {
		return ErrSessionClosed
	}
	for _, id := range c.session.cardIDs {
		if id == c.ID {
			return nil
		}
	}
	return fmt.Errorf("card %d is not part of this session", c.ID)
}

func (c Card) Axis(index uint16) Axis               { return Axis{Card: c, Index: index} }
func (c Card) Bus(port uint16) Bus                  { return Bus{Card: c, Port: port} }
func (c Card) IO(address uint16) IO                 { return IO{Card: c, Address: address} }
func (c Card) Comparator(channel uint16) Comparator { return Comparator{Card: c, Channel: channel} }
func (c Card) ExtraEncoder(channel uint16) ExtraEncoder {
	return ExtraEncoder{Card: c, Channel: channel}
}

type CardSnapshot struct {
	CardNo          uint16
	Type            uint32
	HardwareVersion uint32
	FirmwareID      uint32
	SubFirmwareID   uint32
	ReleaseVersion  string
	LibraryVersion  uint32
	LocalAxisCount  uint32
	BusAxisCount    uint32
	LocalInputs     uint16
	LocalOutputs    uint16
	BusInputs       uint16
	BusOutputs      uint16
}

// Snapshot reads stable card identity and configured channel counts.
func (c Card) Snapshot() (CardSnapshot, error) {
	var out CardSnapshot
	if err := c.ready(); err != nil {
		return out, err
	}
	out.CardNo, out.Type = c.ID, c.Type
	if err := checked("get card version", GetCardVersion(c.ID, &out.HardwareVersion)); err != nil {
		return out, err
	}
	if err := checked("get firmware version", GetCardSoftVersion(c.ID, &out.FirmwareID, &out.SubFirmwareID)); err != nil {
		return out, err
	}
	if err := checked("get library version", GetCardLibVersion(&out.LibraryVersion)); err != nil {
		return out, err
	}
	version := make([]byte, 128)
	if err := checked("get release version", GetReleaseVersion(c.ID, version)); err != nil {
		return out, err
	}
	for i, b := range version {
		if b == 0 {
			version = version[:i]
			break
		}
	}
	out.ReleaseVersion = string(version)
	if err := checked("get local axis count", GetTotalAxes(c.ID, &out.LocalAxisCount)); err != nil {
		return out, err
	}
	if err := checked("get bus axis count", NmcGetTotalAxes(c.ID, &out.BusAxisCount)); err != nil {
		return out, err
	}
	if err := checked("get local IO counts", GetTotalIONum(c.ID, &out.LocalInputs, &out.LocalOutputs)); err != nil {
		return out, err
	}
	if err := checked("get bus IO counts", NmcGetTotalIONum(c.ID, &out.BusInputs, &out.BusOutputs)); err != nil {
		return out, err
	}
	return out, nil
}

type AxisSnapshot struct {
	Equivalence     float64
	EncoderPosition float64
	TargetPosition  int32
	CurrentSpeed    float64
	Done            bool
	IOStatus        uint32
	StopReason      int32
	HomingStatus    uint16
	StateMachine    uint16
	ErrorCode       uint16
	Torque          int32
	OffsetPosition  float64
}

type Axis struct {
	Card  Card
	Index uint16
}

// Snapshot reads the full axis state. For high-rate scans use the backend's
// read mask so the SDK only performs the requested native reads.
func (a Axis) Snapshot() (AxisSnapshot, error) {
	var out AxisSnapshot
	if err := a.Card.ready(); err != nil {
		return out, err
	}
	id, axis := a.Card.ID, a.Index
	if err := checked("get equivalence", GetEquiv(id, axis, &out.Equivalence)); err != nil {
		return out, err
	}
	if err := checked("read encoder position", GetEncoderUnit(id, axis, &out.EncoderPosition)); err != nil {
		return out, err
	}
	out.TargetPosition = GetTargetPosition(id, axis)
	if err := checked("read current speed", ReadCurrentSpeedUnit(id, axis, &out.CurrentSpeed)); err != nil {
		return out, err
	}
	out.Done = CheckDone(id, axis) == 1
	out.IOStatus = AxisIOStatus(id, axis)
	if err := checked("get stop reason", GetStopReason(id, axis, &out.StopReason)); err != nil {
		return out, err
	}
	if err := checked("get home result", GetHomeResult(id, axis, &out.HomingStatus)); err != nil {
		return out, err
	}
	if err := checked("get state machine", NmcGetAxisStateMachine(id, axis, &out.StateMachine)); err != nil {
		return out, err
	}
	if err := checked("get axis error", NmcGetAxisErrCode(id, axis, &out.ErrorCode)); err != nil {
		return out, err
	}
	if err := checked("get torque", NmcGetTorque(id, axis, &out.Torque)); err != nil {
		return out, err
	}
	if err := checked("get offset", NmcGetOffsetPos(id, axis, &out.OffsetPosition)); err != nil {
		return out, err
	}
	return out, nil
}

func (a Axis) Enable() error {
	if err := a.Card.ready(); err != nil {
		return err
	}
	return checked("enable axis", NmcSetAxisEnable(a.Card.ID, a.Index))
}
func (a Axis) Disable() error {
	if err := a.Card.ready(); err != nil {
		return err
	}
	return checked("disable axis", NmcSetAxisDisable(a.Card.ID, a.Index))
}
func (a Axis) ClearFault() error {
	if err := a.Card.ready(); err != nil {
		return err
	}
	return checked("clear axis error", NmcClearAxisErrCode(a.Card.ID, a.Index))
}
func (a Axis) ZeroPosition() error {
	if err := a.Card.ready(); err != nil {
		return err
	}
	return checked("zero axis position", SetPositionUnit(a.Card.ID, a.Index, 0))
}
func (a Axis) Stop(decelerationSeconds float64, emergency bool) error {
	if err := a.Card.ready(); err != nil {
		return err
	}
	if decelerationSeconds < 0 {
		return errors.New("deceleration time cannot be negative")
	}
	if err := checked("set deceleration stop time", SetDecStopTime(a.Card.ID, a.Index, decelerationSeconds)); err != nil {
		return err
	}
	mode := uint16(0)
	if emergency {
		mode = 1
	}
	return checked("stop axis", Stop(a.Card.ID, a.Index, mode))
}
func (a Axis) Jog(direction uint16, speed, acceleration, deceleration float64) error {
	if err := a.Card.ready(); err != nil {
		return err
	}
	if direction > 1 || speed <= 0 || acceleration <= 0 || deceleration <= 0 {
		return errors.New("invalid jog parameters")
	}
	if err := checked("set jog profile", SetProfileUnit(a.Card.ID, a.Index, 0, speed, speed/acceleration, speed/deceleration, 0)); err != nil {
		return err
	}
	if err := checked("set S profile", SetSProfile(a.Card.ID, a.Index, 0, 0.1)); err != nil {
		return err
	}
	return checked("jog axis", Vmove(a.Card.ID, a.Index, direction))
}
func (a Axis) Move(position, speed, acceleration, deceleration float64, relative bool) error {
	if err := a.Card.ready(); err != nil {
		return err
	}
	if speed <= 0 || acceleration <= 0 || deceleration <= 0 {
		return errors.New("invalid move parameters")
	}
	if err := checked("set move profile", SetProfileUnit(a.Card.ID, a.Index, 0, speed, speed/acceleration, speed/deceleration, 0)); err != nil {
		return err
	}
	if err := checked("set S profile", SetSProfile(a.Card.ID, a.Index, 0, 0.1)); err != nil {
		return err
	}
	mode := uint16(1)
	if relative {
		mode = 0
	}
	return checked("move axis", PmoveUnit(a.Card.ID, a.Index, position, mode))
}
func (a Axis) SetEquivalence(value float64) error {
	if err := a.Card.ready(); err != nil {
		return err
	}
	return checked("set equivalence", SetEquiv(a.Card.ID, a.Index, value))
}
func (a Axis) Equivalence() (float64, error) {
	var value float64
	if err := a.Card.ready(); err != nil {
		return 0, err
	}
	err := checked("get equivalence", GetEquiv(a.Card.ID, a.Index, &value))
	return value, err
}

type IO struct {
	Card    Card
	Address uint16
}

func (i IO) ReadInputBit() (bool, error) {
	if err := i.Card.ready(); err != nil {
		return false, err
	}
	v := ReadInBit(i.Card.ID, i.Address)
	if v < 0 {
		return false, &SDKError{"read input bit", v}
	}
	return v != 0, nil
}
func (i IO) ReadOutputBit() (bool, error) {
	if err := i.Card.ready(); err != nil {
		return false, err
	}
	v := ReadOutBit(i.Card.ID, i.Address)
	if v < 0 {
		return false, &SDKError{"read output bit", v}
	}
	return v != 0, nil
}
func (i IO) WriteOutputBit(on bool) error {
	if err := i.Card.ready(); err != nil {
		return err
	}
	v := uint16(0)
	if on {
		v = 1
	}
	return checked("write output bit", WriteOutBit(i.Card.ID, i.Address, v))
}
func (i IO) ReverseOutputBit(milliseconds uint32) error {
	if err := i.Card.ready(); err != nil {
		return err
	}
	return checked("reverse output bit", ReverseOutBit(i.Card.ID, i.Address, float64(milliseconds)))
}
func (c Card) ReadInputPort(port uint16) (uint32, error) {
	if err := c.ready(); err != nil {
		return 0, err
	}
	return ReadInPort(c.ID, port), nil
}
func (c Card) ReadOutputPort(port uint16) (uint32, error) {
	if err := c.ready(); err != nil {
		return 0, err
	}
	return ReadOutPort(c.ID, port), nil
}
func (c Card) WriteOutputPort(port uint16, value uint32) error {
	if err := c.ready(); err != nil {
		return err
	}
	return checked("write output port", WriteOutPort(c.ID, port, value))
}

type Bus struct {
	Card Card
	Port uint16
}
type BusSnapshot struct {
	AxisCount          uint32
	SlaveCount         uint16
	InputCount         uint16
	OutputCount        uint16
	ErrorCode          uint16
	CycleTimeMicros    int32
	AverageCycleMicros uint32
	MaxCycleMicros     uint32
	TotalCycles        uint64
}

func (b Bus) Snapshot() (BusSnapshot, error) {
	var out BusSnapshot
	if err := b.Card.ready(); err != nil {
		return out, err
	}
	id, port := b.Card.ID, b.Port
	if err := checked("get bus axis count", NmcGetTotalAxes(id, &out.AxisCount)); err != nil {
		return out, err
	}
	if err := checked("get slave count", NmcGetTotalSlaves(id, port, &out.SlaveCount)); err != nil {
		return out, err
	}
	if err := checked("get bus IO counts", NmcGetTotalIONum(id, &out.InputCount, &out.OutputCount)); err != nil {
		return out, err
	}
	if err := checked("get bus error", NmcGetErrCode(id, port, &out.ErrorCode)); err != nil {
		return out, err
	}
	if code := NmcGetCycleTime(id, port, &out.CycleTimeMicros); code != 0 {
		return out, &SDKError{"get bus cycle time", code}
	}
	if code := NmcGetConsumeTimeFieldbus(id, port, &out.AverageCycleMicros, &out.MaxCycleMicros, &out.TotalCycles); code != 0 {
		return out, &SDKError{"get bus consume time", code}
	}
	return out, nil
}
func (b Bus) SetCycleTime(microseconds uint32) error {
	if err := b.Card.ready(); err != nil {
		return err
	}
	return checked("set bus cycle time", NmcSetCycleTime(b.Card.ID, b.Port, microseconds))
}
func (b Bus) Stop() (uint16, error) {
	var state uint16
	if err := b.Card.ready(); err != nil {
		return 0, err
	}
	err := checked("stop EtherCAT", NmcStopEtc(b.Card.ID, &state))
	return state, err
}
func (b Bus) ReadOD(node, index, sub, length uint16) (int32, error) {
	var value int32
	if err := b.Card.ready(); err != nil {
		return 0, err
	}
	err := checked("read object dictionary", NmcGetNodeOD(b.Card.ID, b.Port, node, index, sub, length, &value))
	return value, err
}
func (b Bus) WriteOD(node, index, sub, length uint16, value int32) error {
	if err := b.Card.ready(); err != nil {
		return err
	}
	return checked("write object dictionary", NmcSetNodeOD(b.Card.ID, b.Port, node, index, sub, length, value))
}
func (b Bus) ReadRxPDO(address, length uint16) (int32, error) {
	var value int32
	if err := b.Card.ready(); err != nil {
		return 0, err
	}
	err := checked("read RxPDO", NmcReadRxPDOExtra(b.Card.ID, b.Port, address, length, &value))
	return value, err
}
func (b Bus) WriteRxPDO(address, length uint16, value int32) error {
	if err := b.Card.ready(); err != nil {
		return err
	}
	return checked("write RxPDO", NmcWriteRxPDOExtra(b.Card.ID, b.Port, address, length, value))
}
func (b Bus) ReadTxPDO(address, length uint16) (int32, error) {
	var value int32
	if err := b.Card.ready(); err != nil {
		return 0, err
	}
	err := checked("read TxPDO", NmcReadTxPDOExtra(b.Card.ID, b.Port, address, length, &value))
	return value, err
}

type Comparator struct {
	Card    Card
	Channel uint16
}
type ComparatorConfig struct {
	Mode, Axis, Source, Logic uint16
	Time                      int32
}
type ComparatorState struct{ RemainingPoints, CurrentPoint, RunPoints int32 }

func (h Comparator) SetConfig(v ComparatorConfig) error {
	if err := h.Card.ready(); err != nil {
		return err
	}
	if err := checked("set comparator mode", HcmpSetMode(h.Card.ID, h.Channel, v.Mode)); err != nil {
		return err
	}
	return checked("set comparator config", HcmpSetConfig(h.Card.ID, h.Channel, v.Axis, v.Source, v.Logic, v.Time))
}
func (h Comparator) Config() (ComparatorConfig, error) {
	var v ComparatorConfig
	if err := h.Card.ready(); err != nil {
		return v, err
	}
	if err := checked("get comparator mode", HcmpGetMode(h.Card.ID, h.Channel, &v.Mode)); err != nil {
		return v, err
	}
	return v, checked("get comparator config", HcmpGetConfig(h.Card.ID, h.Channel, &v.Axis, &v.Source, &v.Logic, &v.Time))
}
func (h Comparator) SetLinear(increment, count int32) error {
	if err := h.Card.ready(); err != nil {
		return err
	}
	return checked("set comparator linear config", HcmpSetLiner(h.Card.ID, h.Channel, increment, count))
}
func (h Comparator) Linear() (increment, count int32, err error) {
	if e := h.Card.ready(); e != nil {
		return 0, 0, e
	}
	err = checked("get comparator linear config", HcmpGetLiner(h.Card.ID, h.Channel, &increment, &count))
	return
}
func (h Comparator) AddPoint(position int32) error {
	if err := h.Card.ready(); err != nil {
		return err
	}
	return checked("add comparator point", HcmpAddPoint(h.Card.ID, h.Channel, position))
}
func (h Comparator) ClearPoints() error {
	if err := h.Card.ready(); err != nil {
		return err
	}
	return checked("clear comparator points", HcmpClearPoints(h.Card.ID, h.Channel))
}
func (h Comparator) State() (ComparatorState, error) {
	var v ComparatorState
	if err := h.Card.ready(); err != nil {
		return v, err
	}
	err := checked("read comparator state", HcmpGetCurrentState(h.Card.ID, h.Channel, &v.RemainingPoints, &v.CurrentPoint, &v.RunPoints))
	return v, err
}

type ExtraEncoder struct {
	Card    Card
	Channel uint16
}
type ExtraEncoderConfig struct {
	InputMode, Multiplier uint16
	Position              int32
}

func (e ExtraEncoder) Config() (ExtraEncoderConfig, error) {
	var v ExtraEncoderConfig
	if err := e.Card.ready(); err != nil {
		return v, err
	}
	if err := checked("get extra encoder mode", GetExtraEncoderMode(e.Card.ID, e.Channel, &v.InputMode, &v.Multiplier)); err != nil {
		return v, err
	}
	return v, checked("get extra encoder position", GetExtraEncoder(e.Card.ID, e.Channel, &v.Position))
}
func (e ExtraEncoder) SetMode(inputMode, multiplier uint16) error {
	if err := e.Card.ready(); err != nil {
		return err
	}
	return checked("set extra encoder mode", SetExtraEncoderMode(e.Card.ID, e.Channel, inputMode, multiplier))
}
func (e ExtraEncoder) SetPosition(position int32) error {
	if err := e.Card.ready(); err != nil {
		return err
	}
	return checked("set extra encoder position", SetExtraEncoder(e.Card.ID, e.Channel, position))
}
