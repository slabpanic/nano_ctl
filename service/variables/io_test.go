package variables

import (
	"context"
	"errors"
	"testing"

	"nano_ctl/service/hardware"
	"nano_ctl/service/store"
)

type fakeHardware struct {
	calls         int
	on            bool
	address       uint32
	reverseMillis uint32
}

func (f *fakeHardware) ReadIO(_ context.Context, _ uint16, _ hardware.IOKind, addresses []uint32, _ hardware.Priority) ([]hardware.IOValue, error) {
	f.calls++
	f.address = addresses[0]
	return []hardware.IOValue{{Address: addresses[0], RawValue: 0, On: false}}, nil
}
func (f *fakeHardware) WriteIO(_ context.Context, _ uint16, _ hardware.IOKind, address, raw uint32, analog float64, on bool, reverseMillis uint32, _ hardware.Priority) error {
	f.calls++
	f.on = on
	f.address = address
	f.reverseMillis = reverseMillis
	return nil
}
func TestLogicalPolarityAndDisabledWrites(t *testing.T) {
	hw := &fakeHardware{}
	row := store.IOAlias{Kind: "output-bit", Address: 8, Reverse: true}
	value, err := Read(context.Background(), hw, row, hardware.PriorityControl)
	if err != nil || !value.LogicalOn || value.On || value.RawValue != 0 || hw.address != 8 {
		t.Fatalf("wrong polarity/address: %+v %v", value, err)
	}
	if err := Write(context.Background(), hw, row, 0, 0, true, 0, hardware.PriorityRealtime); err != nil || hw.on {
		t.Fatalf("logical ON should write physical OFF: %v", err)
	}
	if err := Write(context.Background(), hw, row, 0, 0, false, 250, hardware.PriorityRealtime); err != nil || !hw.on || hw.reverseMillis != 250 {
		t.Fatalf("wrong write/pulse: %v", err)
	}
	calls := hw.calls
	row.Disabled = true
	if err := Write(context.Background(), hw, row, 0, 0, true, 0, hardware.PriorityControl); !errors.Is(err, ErrDisabled) || hw.calls != calls {
		t.Fatal("disabled variable reached hardware")
	}
	if _, err := Read(context.Background(), hw, row, hardware.PriorityControl); !errors.Is(err, ErrDisabled) || hw.calls != calls {
		t.Fatal("disabled read reached hardware")
	}
	row.Disabled = false
	row.Kind = "input-bit"
	if err := Write(context.Background(), hw, row, 0, 0, true, 0, hardware.PriorityControl); !errors.Is(err, ErrReadOnly) || hw.calls != calls {
		t.Fatal("input write reached hardware")
	}
}
