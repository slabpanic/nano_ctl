// Package variables applies saved IO variable settings to explicit debug reads
// and writes. Raw address APIs continue to expose physical levels.
package variables

import (
	"context"
	"errors"

	"nano_ctl/service/hardware"
	"nano_ctl/service/store"
)

var ErrDisabled = errors.New("IO variable is disabled")
var ErrReadOnly = errors.New("IO variable is read-only; analog outputs use mapped RxPDO")

type Hardware interface {
	ReadIO(context.Context, uint16, hardware.IOKind, []uint32, hardware.Priority) ([]hardware.IOValue, error)
	WriteIO(context.Context, uint16, hardware.IOKind, uint32, uint32, float64, bool, uint32, hardware.Priority) error
}

type Sample struct {
	hardware.IOValue
	LogicalOn bool `json:"logical_on"`
}

func Kind(kind string) hardware.IOKind {
	return map[string]hardware.IOKind{"input-bit": hardware.IOInputBit, "output-bit": hardware.IOOutputBit, "input-port": hardware.IOInputPort, "output-port": hardware.IOOutputPort, "analog-input": hardware.IOAnalogInput, "analog-output": hardware.IOAnalogOutput}[kind]
}

func Read(ctx context.Context, hw Hardware, alias store.IOAlias, priority hardware.Priority) (Sample, error) {
	if alias.Disabled {
		return Sample{}, ErrDisabled
	}
	values, err := hw.ReadIO(ctx, alias.CardNo, Kind(alias.Kind), []uint32{alias.Address}, priority)
	if err != nil {
		return Sample{}, err
	}
	value := values[0]
	return Sample{IOValue: value, LogicalOn: value.On != alias.Reverse}, nil
}

func Write(ctx context.Context, hw Hardware, alias store.IOAlias, raw uint32, analog float64, on bool, reverseMillis uint32, priority hardware.Priority) error {
	if alias.Disabled {
		return ErrDisabled
	}
	if alias.Kind != "output-bit" && alias.Kind != "output-port" {
		return ErrReadOnly
	}
	return hw.WriteIO(ctx, alias.CardNo, Kind(alias.Kind), alias.Address, raw, analog, on != alias.Reverse, reverseMillis, priority)
}
