package grpcapi

import (
	"context"
	"errors"

	pb "nano_ctl/pkg/proto/godmc/v1"
	"nano_ctl/service/hardware"
	"nano_ctl/service/store"
	"nano_ctl/service/variables"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ioKindNames = [...]string{"", "input-bit", "output-bit", "input-port", "output-port", "analog-input", "analog-output"}

func variableFromRef(ref *pb.IOVariableRef) (store.IOAlias, error) {
	if ref == nil {
		return store.IOAlias{}, status.Error(codes.InvalidArgument, "IO variable ref is required")
	}
	card, err := narrow(ref.GetCardNo(), "card_no")
	if err != nil {
		return store.IOAlias{}, err
	}
	if _, err := narrow(ref.GetAddress(), "address"); err != nil {
		return store.IOAlias{}, err
	}
	if _, err := ioKind(ref.GetKind()); err != nil {
		return store.IOAlias{}, err
	}
	return store.IOAlias{CardNo: card, Kind: ioKindNames[ref.GetKind()], Address: ref.GetAddress()}, nil
}

func variableMessage(row store.IOAlias) *pb.IOVariable {
	return &pb.IOVariable{Ref: &pb.IOVariableRef{CardNo: uint32(row.CardNo), Kind: pb.IOKind(variables.Kind(row.Kind)), Address: row.Address}, Alias: row.Alias, Remarks: row.Remarks, Reverse: row.Reverse, Disabled: row.Disabled, SourceId: row.SourceID}
}

func variableError(err error) error {
	switch {
	case err == nil:
		return nil
	case store.IsNotFound(err):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrInvalidIOAlias), errors.Is(err, variables.ErrReadOnly):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, variables.ErrDisabled):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return rpcError(err)
	}
}

func (s *Server) ListIOVariables(ctx context.Context, in *pb.IOVariablesRequest) (*pb.IOVariableList, error) {
	var card *uint16
	if in != nil && in.CardNo != nil {
		n, err := narrow(in.GetCardNo(), "card_no")
		if err != nil {
			return nil, err
		}
		card = &n
	}
	rows, err := s.db.ListIOAliases(card)
	if err != nil {
		return nil, variableError(err)
	}
	out := &pb.IOVariableList{Variables: make([]*pb.IOVariable, 0, len(rows))}
	for _, row := range rows {
		out.Variables = append(out.Variables, variableMessage(row))
	}
	return out, nil
}

func (s *Server) PutIOVariable(ctx context.Context, in *pb.IOVariable) (*pb.IOVariable, error) {
	row, err := variableFromRef(in.GetRef())
	if err != nil {
		return nil, err
	}
	row.Alias, row.Remarks, row.Reverse, row.Disabled = in.GetAlias(), in.GetRemarks(), in.GetReverse(), in.GetDisabled()
	if err := s.db.PutIOAlias(&row); err != nil {
		return nil, variableError(err)
	}
	row, err = s.db.GetIOAlias(row.CardNo, row.Kind, row.Address)
	if err != nil {
		return nil, variableError(err)
	}
	return variableMessage(row), nil
}

func (s *Server) DeleteIOVariable(ctx context.Context, in *pb.IOVariableRef) (*pb.Empty, error) {
	ref, err := variableFromRef(in)
	if err != nil {
		return nil, err
	}
	if err := s.db.DeleteIOAlias(ref.CardNo, ref.Kind, ref.Address); err != nil {
		return nil, variableError(err)
	}
	return &pb.Empty{}, nil
}

func (s *Server) loadIOVariable(ref *pb.IOVariableRef) (store.IOAlias, error) {
	row, err := variableFromRef(ref)
	if err != nil {
		return row, err
	}
	row, err = s.db.GetIOAlias(row.CardNo, row.Kind, row.Address)
	return row, variableError(err)
}

func (s *Server) ReadIOVariable(ctx context.Context, in *pb.IOVariableRef) (*pb.IOVariableValue, error) {
	row, err := s.loadIOVariable(in)
	if err != nil {
		return nil, err
	}
	value, err := variables.Read(ctx, s.hw, row, hardware.PriorityRealtime)
	if err != nil {
		return nil, variableError(err)
	}
	return &pb.IOVariableValue{Value: &pb.IOValue{Address: value.Address, RawValue: value.RawValue, AnalogValue: value.AnalogValue, On: value.On}, LogicalOn: value.LogicalOn}, nil
}

func (s *Server) WriteIOVariable(ctx context.Context, in *pb.IOVariableWriteRequest) (*pb.CommandResult, error) {
	row, err := s.loadIOVariable(in.GetRef())
	if err != nil {
		return nil, err
	}
	if err := variables.Write(ctx, s.hw, row, in.GetRawValue(), 0, in.GetOn(), in.GetReverseTimeMillis(), hardware.PriorityRealtime); err != nil {
		return nil, variableError(err)
	}
	return &pb.CommandResult{}, nil
}
