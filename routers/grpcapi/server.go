package grpcapi

import (
	"context"
	"errors"
	"math"
	"time"

	pb "nano_ctl/pkg/proto/godmc/v1"
	"nano_ctl/service/hardware"
	"nano_ctl/service/store"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	pb.UnimplementedMotionServiceServer
	hw *hardware.Controller
	db *store.Store
}

func New(hw *hardware.Controller, db *store.Store) *Server { return &Server{hw: hw, db: db} }

func rpcError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, hardware.ErrCardClosed):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, hardware.ErrTransition):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, hardware.ErrCardUnknown):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, hardware.ErrBusy):
		return status.Error(codes.ResourceExhausted, err.Error())
	case store.IsNotFound(err):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		var sdkErr *hardware.SDKError
		if errors.As(err, &sdkErr) {
			return status.Errorf(codes.Internal, "%s (LTDMC code %d)", sdkErr.Operation, sdkErr.Code)
		}
		return status.Error(codes.InvalidArgument, err.Error())
	}
}

func narrow(v uint32, name string) (uint16, error) {
	if v > math.MaxUint16 {
		return 0, status.Errorf(codes.InvalidArgument, "%s exceeds 16-bit SDK range", name)
	}
	return uint16(v), nil
}
func requiredAxis(ref *pb.AxisRef) (uint16, uint16, error) {
	if ref == nil {
		return 0, 0, status.Error(codes.InvalidArgument, "axis ref is required")
	}
	card, err := narrow(ref.GetCardNo(), "card_no")
	if err != nil {
		return 0, 0, err
	}
	axis, err := narrow(ref.GetAxis(), "axis")
	return card, axis, err
}
func axisMessage(v hardware.AxisSnapshot) *pb.AxisSnapshot {
	return &pb.AxisSnapshot{
		Ref:         &pb.AxisRef{CardNo: uint32(v.CardNo), Axis: uint32(v.Axis)},
		Equivalence: v.Equivalence, EncoderPosition: v.EncoderPosition,
		TargetPosition: v.TargetPosition, CurrentSpeed: v.CurrentSpeed,
		Done: v.Done, IoStatus: v.IOStatus, StopReason: v.StopReason,
		HomingStatus: uint32(v.HomingStatus), StateMachine: uint32(v.StateMachine),
		ErrorCode: uint32(v.ErrorCode), Torque: v.Torque, OffsetPosition: v.OffsetPosition,
		FieldsRead: v.FieldsRead,
	}
}

func (s *Server) ReadAxis(ctx context.Context, in *pb.AxisRef) (*pb.AxisSnapshot, error) {
	card, axis, err := requiredAxis(in)
	if err != nil {
		return nil, err
	}
	v, err := s.hw.ReadAxis(ctx, card, axis, hardware.AxisReadMask(in.GetFieldsMask()), hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	return axisMessage(v), nil
}

func (s *Server) ReadAxes(ctx context.Context, in *pb.AxesRequest) (*pb.AxisBatch, error) {
	if in == nil || len(in.GetAxes()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one axis is required")
	}
	card, err := narrow(in.GetCardNo(), "card_no")
	if err != nil {
		return nil, err
	}
	axes := make([]uint16, len(in.GetAxes()))
	if len(axes) > 64 {
		return nil, status.Error(codes.InvalidArgument, "at most 64 axes can be read in one batch")
	}
	for i, raw := range in.GetAxes() {
		axes[i], err = narrow(raw, "axis")
		if err != nil {
			return nil, err
		}
	}
	values, err := s.hw.ReadAxes(ctx, card, axes, hardware.AxisReadMask(in.GetFieldsMask()), hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.AxisBatch{SampledAtUnixNano: time.Now().UnixNano(), Axes: make([]*pb.AxisSnapshot, len(values))}
	for i, v := range values {
		out.Axes[i] = axisMessage(v)
	}
	return out, nil
}

func (s *Server) WatchAxes(in *pb.AxesRequest, stream pb.MotionService_WatchAxesServer) error {
	if in == nil || len(in.GetAxes()) == 0 {
		return status.Error(codes.InvalidArgument, "at least one axis is required")
	}
	interval := time.Duration(in.GetIntervalMicros()) * time.Microsecond
	if interval == 0 {
		interval = 10 * time.Millisecond
	}
	if interval < time.Millisecond || interval > time.Second {
		return status.Error(codes.InvalidArgument, "interval_micros must be between 1000 and 1000000")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		batch, err := s.ReadAxes(stream.Context(), in)
		if err != nil {
			return err
		}
		if err := stream.Send(batch); err != nil {
			return err
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) AxisCommand(ctx context.Context, in *pb.AxisCommandRequest) (*pb.CommandResult, error) {
	card, axis, err := requiredAxis(in.GetRef())
	if err != nil {
		return nil, err
	}
	action := in.GetAction()
	if action < pb.AxisAction_AXIS_ACTION_ENABLE || action > pb.AxisAction_AXIS_ACTION_SET_OFFSET {
		return nil, status.Error(codes.InvalidArgument, "axis action is invalid")
	}
	if in.GetUnit() != pb.MotionUnit_MOTION_UNIT_POSITION && in.GetUnit() != pb.MotionUnit_MOTION_UNIT_ANGLE {
		return nil, status.Error(codes.InvalidArgument, "motion unit is invalid")
	}
	direction, err := narrow(in.GetDirection(), "direction")
	if err != nil {
		return nil, err
	}
	positionMode, err := narrow(in.GetPositionMode(), "position_mode")
	if err != nil {
		return nil, err
	}
	stopMode, err := narrow(in.GetStopMode(), "stop_mode")
	if err != nil {
		return nil, err
	}
	homeMethod, err := narrow(in.GetHomeMethod(), "home_method")
	if err != nil {
		return nil, err
	}
	probeFunction, err := narrow(in.GetProbeFunction(), "probe_function")
	if err != nil {
		return nil, err
	}
	cmd := hardware.AxisCommand{CardNo: card, Axis: axis, Action: hardware.AxisAction(action), Position: in.GetPosition(), Speed: in.GetSpeed(), Acceleration: in.GetAcceleration(), Deceleration: in.GetDeceleration(), Direction: uint16(in.GetDirection()), PositionMode: uint16(in.GetPositionMode()), StopMode: uint16(in.GetStopMode()), HomeMethod: uint16(in.GetHomeMethod()), SecondarySpeed: in.GetSecondarySpeed(), Offset: in.GetOffset(), ProbeFunction: uint16(in.GetProbeFunction()), Equivalence: in.GetEquivalence()}
	cmd.Direction, cmd.PositionMode, cmd.StopMode, cmd.HomeMethod, cmd.ProbeFunction = direction, positionMode, stopMode, homeMethod, probeFunction
	if in.GetUnit() == pb.MotionUnit_MOTION_UNIT_ANGLE {
		cfg, err := s.db.GetAxisConfig(card, axis)
		if err != nil {
			return nil, rpcError(err)
		}
		degreePerPos := 360.0 / (float64(cfg.ReductionRatio) * math.Pow(2, float64(cfg.Resolution)))
		if degreePerPos <= 0 || math.IsNaN(degreePerPos) || math.IsInf(degreePerPos, 0) {
			return nil, status.Error(codes.FailedPrecondition, "invalid axis angle configuration")
		}
		switch action {
		case pb.AxisAction_AXIS_ACTION_JOG, pb.AxisAction_AXIS_ACTION_MOVE, pb.AxisAction_AXIS_ACTION_HOME, pb.AxisAction_AXIS_ACTION_CHANGE_SPEED:
			cmd.Speed /= degreePerPos
			cmd.Acceleration /= degreePerPos
			cmd.Deceleration /= degreePerPos
			cmd.SecondarySpeed /= degreePerPos
		}
		switch action {
		case pb.AxisAction_AXIS_ACTION_MOVE, pb.AxisAction_AXIS_ACTION_CHANGE_TARGET:
			cmd.Position = math.RoundToEven(cmd.Position / degreePerPos)
		case pb.AxisAction_AXIS_ACTION_HOME:
			cmd.Offset = math.RoundToEven(cmd.Offset / degreePerPos)
		}
	}
	if err := s.hw.AxisCommand(ctx, cmd, hardware.PriorityRealtime); err != nil {
		return nil, rpcError(err)
	}
	return &pb.CommandResult{SdkCode: 0}, nil
}

func ioKind(v pb.IOKind) (hardware.IOKind, error) {
	switch v {
	case pb.IOKind_IO_KIND_INPUT_BIT:
		return hardware.IOInputBit, nil
	case pb.IOKind_IO_KIND_OUTPUT_BIT:
		return hardware.IOOutputBit, nil
	case pb.IOKind_IO_KIND_INPUT_PORT:
		return hardware.IOInputPort, nil
	case pb.IOKind_IO_KIND_OUTPUT_PORT:
		return hardware.IOOutputPort, nil
	case pb.IOKind_IO_KIND_ANALOG_INPUT:
		return hardware.IOAnalogInput, nil
	case pb.IOKind_IO_KIND_ANALOG_OUTPUT:
		return hardware.IOAnalogOutput, nil
	default:
		return 0, status.Error(codes.InvalidArgument, "io kind is required")
	}
}
func (s *Server) ReadIO(ctx context.Context, in *pb.IOReadRequest) (*pb.IOBatch, error) {
	if in == nil || len(in.GetAddresses()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one address is required")
	}
	card, err := narrow(in.GetCardNo(), "card_no")
	if err != nil {
		return nil, err
	}
	kind, err := ioKind(in.GetKind())
	if err != nil {
		return nil, err
	}
	values, err := s.hw.ReadIO(ctx, card, kind, in.GetAddresses(), hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.IOBatch{Values: make([]*pb.IOValue, len(values))}
	for i, v := range values {
		out.Values[i] = &pb.IOValue{Address: v.Address, RawValue: v.RawValue, AnalogValue: v.AnalogValue, On: v.On}
	}
	return out, nil
}

func (s *Server) ReadIOArray(ctx context.Context, in *pb.IOArrayReadRequest) (*pb.IOBatch, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	card, err := narrow(in.GetCardNo(), "card_no")
	if err != nil {
		return nil, err
	}
	port, err := narrow(in.GetPortNum(), "port_num")
	if err != nil {
		return nil, err
	}
	if port == 0 {
		port = 2
	}
	values, err := s.hw.ReadIOArray(ctx, card, port, in.GetLength(), in.GetOutputs(), hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.IOBatch{Values: make([]*pb.IOValue, len(values))}
	for i, value := range values {
		out.Values[i] = &pb.IOValue{Address: uint32(i), RawValue: value, On: value != 0}
	}
	return out, nil
}

func (s *Server) WatchIO(in *pb.IOReadRequest, stream pb.MotionService_WatchIOServer) error {
	if in == nil || len(in.GetAddresses()) == 0 {
		return status.Error(codes.InvalidArgument, "at least one address is required")
	}
	interval := time.Duration(in.GetIntervalMicros()) * time.Microsecond
	if interval == 0 {
		interval = 10 * time.Millisecond
	}
	if interval < time.Millisecond || interval > time.Second {
		return status.Error(codes.InvalidArgument, "interval_micros must be between 1000 and 1000000")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		batch, err := s.ReadIO(stream.Context(), in)
		if err != nil {
			return err
		}
		if err := stream.Send(batch); err != nil {
			return err
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}
func (s *Server) WriteIO(ctx context.Context, in *pb.IOWriteRequest) (*pb.CommandResult, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	card, err := narrow(in.GetCardNo(), "card_no")
	if err != nil {
		return nil, err
	}
	kind, err := ioKind(in.GetKind())
	if err != nil {
		return nil, err
	}
	if err := s.hw.WriteIO(ctx, card, kind, in.GetAddress(), in.GetRawValue(), in.GetAnalogValue(), in.GetOn(), in.GetReverseTimeMillis(), hardware.PriorityRealtime); err != nil {
		return nil, rpcError(err)
	}
	return &pb.CommandResult{SdkCode: 0}, nil
}

func (s *Server) ReadBus(ctx context.Context, in *pb.BusRef) (*pb.BusSnapshot, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "bus ref is required")
	}
	card, err := narrow(in.GetCardNo(), "card_no")
	if err != nil {
		return nil, err
	}
	port, err := narrow(in.GetPortNum(), "port_num")
	if err != nil {
		return nil, err
	}
	if port == 0 {
		port = 2
	}
	v, err := s.hw.ReadBus(ctx, card, port, hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.BusSnapshot{Ref: &pb.BusRef{CardNo: uint32(card), PortNum: uint32(port)}, AxisCount: v.AxisCount, SlaveCount: uint32(v.SlaveCount), InputCount: uint32(v.InputCount), OutputCount: uint32(v.OutputCount), ErrorCode: uint32(v.ErrorCode), CycleTimeMicros: uint32(max(v.CycleTimeMicros, 0)), AverageCycleMicros: v.AverageCycleMicros, MaxCycleMicros: v.MaxCycleMicros, TotalCycles: v.TotalCycles, CycleTimeSdkCode: int32(v.CycleTimeSDKCode), ConsumeSdkCode: int32(v.ConsumeSDKCode)}, nil
}
func (s *Server) BusCommand(ctx context.Context, in *pb.BusCommandRequest) (*pb.CommandResult, error) {
	if in == nil || in.GetRef() == nil {
		return nil, status.Error(codes.InvalidArgument, "bus ref is required")
	}
	card, err := narrow(in.GetRef().GetCardNo(), "card_no")
	if err != nil {
		return nil, err
	}
	port, err := narrow(in.GetRef().GetPortNum(), "port_num")
	if err != nil {
		return nil, err
	}
	if port == 0 {
		port = 2
	}
	var resultValue uint32
	switch in.GetAction() {
	case pb.BusAction_BUS_ACTION_STOP:
		state, err := s.hw.StopBus(ctx, card, hardware.PriorityRealtime)
		if err != nil {
			return nil, rpcError(err)
		}
		resultValue = uint32(state)
	case pb.BusAction_BUS_ACTION_CLEAR_ERROR:
		if err := s.hw.ClearBusError(ctx, card, port, hardware.PriorityRealtime); err != nil {
			return nil, rpcError(err)
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "bus action is required")
	}
	return &pb.CommandResult{SdkCode: 0, Value: resultValue}, nil
}

func (s *Server) ReadObjectDictionary(ctx context.Context, in *pb.ODRequest) (*pb.IntValue, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	card, e := narrow(in.GetCardNo(), "card_no")
	if e != nil {
		return nil, e
	}
	port, e := narrow(in.GetPortNum(), "port_num")
	if e != nil {
		return nil, e
	}
	if port == 0 {
		port = 2
	}
	node, e := narrow(in.GetNode(), "node")
	if e != nil {
		return nil, e
	}
	index, e := narrow(in.GetIndex(), "index")
	if e != nil {
		return nil, e
	}
	sub, e := narrow(in.GetSubIndex(), "sub_index")
	if e != nil {
		return nil, e
	}
	length, e := narrow(in.GetValueLength(), "value_length")
	if e != nil {
		return nil, e
	}
	v, err := s.hw.ReadOD(ctx, card, port, node, index, sub, length, hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.IntValue{Value: v}, nil
}
func (s *Server) WriteObjectDictionary(ctx context.Context, in *pb.ODWriteRequest) (*pb.CommandResult, error) {
	if in == nil || in.GetRef() == nil {
		return nil, status.Error(codes.InvalidArgument, "request ref is required")
	}
	r := in.GetRef()
	card, e := narrow(r.GetCardNo(), "card_no")
	if e != nil {
		return nil, e
	}
	port, e := narrow(r.GetPortNum(), "port_num")
	if e != nil {
		return nil, e
	}
	if port == 0 {
		port = 2
	}
	node, e := narrow(r.GetNode(), "node")
	if e != nil {
		return nil, e
	}
	index, e := narrow(r.GetIndex(), "index")
	if e != nil {
		return nil, e
	}
	sub, e := narrow(r.GetSubIndex(), "sub_index")
	if e != nil {
		return nil, e
	}
	length, e := narrow(r.GetValueLength(), "value_length")
	if e != nil {
		return nil, e
	}
	if err := s.hw.WriteOD(ctx, card, port, node, index, sub, length, in.GetValue(), hardware.PriorityRealtime); err != nil {
		return nil, rpcError(err)
	}
	return &pb.CommandResult{SdkCode: 0}, nil
}
func (s *Server) ReadRxPDO(ctx context.Context, in *pb.PDORequest) (*pb.IntValue, error) {
	return s.readPDO(ctx, in, true)
}
func (s *Server) ReadTxPDO(ctx context.Context, in *pb.PDORequest) (*pb.IntValue, error) {
	return s.readPDO(ctx, in, false)
}
func (s *Server) readPDO(ctx context.Context, in *pb.PDORequest, rx bool) (*pb.IntValue, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	card, e := narrow(in.GetCardNo(), "card_no")
	if e != nil {
		return nil, e
	}
	port, e := narrow(in.GetPortNum(), "port_num")
	if e != nil {
		return nil, e
	}
	if port == 0 {
		port = 2
	}
	addr, e := narrow(in.GetAddress(), "address")
	if e != nil {
		return nil, e
	}
	length, e := narrow(in.GetDataLength(), "data_length")
	if e != nil {
		return nil, e
	}
	v, err := s.hw.ReadPDO(ctx, card, port, addr, length, rx, hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.IntValue{Value: v}, nil
}
func (s *Server) WriteRxPDO(ctx context.Context, in *pb.PDOWriteRequest) (*pb.CommandResult, error) {
	if in == nil || in.GetRef() == nil {
		return nil, status.Error(codes.InvalidArgument, "request ref is required")
	}
	r := in.GetRef()
	card, e := narrow(r.GetCardNo(), "card_no")
	if e != nil {
		return nil, e
	}
	port, e := narrow(r.GetPortNum(), "port_num")
	if e != nil {
		return nil, e
	}
	if port == 0 {
		port = 2
	}
	addr, e := narrow(r.GetAddress(), "address")
	if e != nil {
		return nil, e
	}
	length, e := narrow(r.GetDataLength(), "data_length")
	if e != nil {
		return nil, e
	}
	if err := s.hw.WritePDO(ctx, card, port, addr, length, in.GetValue(), hardware.PriorityRealtime); err != nil {
		return nil, rpcError(err)
	}
	return &pb.CommandResult{SdkCode: 0}, nil
}

func hcmpMessage(v hardware.HcmpSnapshot) *pb.HcmpSnapshot {
	return &pb.HcmpSnapshot{Ref: &pb.HcmpRef{CardNo: uint32(v.CardNo), Channel: uint32(v.Channel)}, Mode: uint32(v.Mode), Axis: uint32(v.Axis), Source: uint32(v.Source), Logic: uint32(v.Logic), Time: v.Time, Increment: v.Increment, Count: v.Count, RemainingPoints: v.RemainingPoints, CurrentPoint: v.CurrentPoint, RunPoints: v.RunPoints}
}
func (s *Server) ReadHcmp(ctx context.Context, in *pb.HcmpRef) (*pb.HcmpSnapshot, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "ref is required")
	}
	card, e := narrow(in.GetCardNo(), "card_no")
	if e != nil {
		return nil, e
	}
	channel, e := narrow(in.GetChannel(), "channel")
	if e != nil {
		return nil, e
	}
	v, err := s.hw.Hcmp(ctx, hardware.HcmpCommand{CardNo: card, Channel: channel}, hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	return hcmpMessage(v), nil
}
func (s *Server) HcmpCommand(ctx context.Context, in *pb.HcmpCommandRequest) (*pb.CommandResult, error) {
	if in == nil || in.GetRef() == nil {
		return nil, status.Error(codes.InvalidArgument, "ref is required")
	}
	r := in.GetRef()
	card, e := narrow(r.GetCardNo(), "card_no")
	if e != nil {
		return nil, e
	}
	channel, e := narrow(r.GetChannel(), "channel")
	if e != nil {
		return nil, e
	}
	mode, e := narrow(in.GetMode(), "mode")
	if e != nil {
		return nil, e
	}
	axis, e := narrow(in.GetAxis(), "axis")
	if e != nil {
		return nil, e
	}
	source, e := narrow(in.GetSource(), "source")
	if e != nil {
		return nil, e
	}
	logic, e := narrow(in.GetLogic(), "logic")
	if e != nil {
		return nil, e
	}
	if in.GetAction() < pb.HcmpAction_HCMP_ACTION_SET_CONFIG || in.GetAction() > pb.HcmpAction_HCMP_ACTION_CLEAR_POINTS {
		return nil, status.Error(codes.InvalidArgument, "hcmp action is invalid")
	}
	_, err := s.hw.Hcmp(ctx, hardware.HcmpCommand{CardNo: card, Channel: channel, Action: uint8(in.GetAction()), Mode: mode, Axis: axis, Source: source, Logic: logic, Time: in.GetTime(), Increment: in.GetIncrement(), Count: in.GetCount(), Position: in.GetPosition()}, hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.CommandResult{SdkCode: 0}, nil
}
func extraMessage(v hardware.ExtraEncoderSnapshot) *pb.ExtraEncoderSnapshot {
	return &pb.ExtraEncoderSnapshot{Ref: &pb.ExtraEncoderRef{CardNo: uint32(v.CardNo), Channel: uint32(v.Channel)}, InputMode: uint32(v.InputMode), Multiplier: uint32(v.Multiplier), Position: v.Position}
}
func (s *Server) ReadExtraEncoder(ctx context.Context, in *pb.ExtraEncoderRef) (*pb.ExtraEncoderSnapshot, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "ref is required")
	}
	card, e := narrow(in.GetCardNo(), "card_no")
	if e != nil {
		return nil, e
	}
	channel, e := narrow(in.GetChannel(), "channel")
	if e != nil {
		return nil, e
	}
	v, err := s.hw.ExtraEncoder(ctx, hardware.ExtraEncoderCommand{CardNo: card, Channel: channel}, hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	return extraMessage(v), nil
}
func (s *Server) ExtraEncoderCommand(ctx context.Context, in *pb.ExtraEncoderCommandRequest) (*pb.CommandResult, error) {
	if in == nil || in.GetRef() == nil {
		return nil, status.Error(codes.InvalidArgument, "ref is required")
	}
	r := in.GetRef()
	card, e := narrow(r.GetCardNo(), "card_no")
	if e != nil {
		return nil, e
	}
	channel, e := narrow(r.GetChannel(), "channel")
	if e != nil {
		return nil, e
	}
	inputMode, e := narrow(in.GetInputMode(), "input_mode")
	if e != nil {
		return nil, e
	}
	multiplier, e := narrow(in.GetMultiplier(), "multiplier")
	if e != nil {
		return nil, e
	}
	if in.GetAction() < pb.ExtraEncoderAction_EXTRA_ENCODER_ACTION_SET_MODE || in.GetAction() > pb.ExtraEncoderAction_EXTRA_ENCODER_ACTION_SET_POSITION {
		return nil, status.Error(codes.InvalidArgument, "extra encoder action is invalid")
	}
	_, err := s.hw.ExtraEncoder(ctx, hardware.ExtraEncoderCommand{CardNo: card, Channel: channel, Action: uint8(in.GetAction()), InputMode: inputMode, Multiplier: multiplier, Position: in.GetPosition()}, hardware.PriorityRealtime)
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.CommandResult{SdkCode: 0}, nil
}
