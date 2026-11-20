package usb

import (
	"errors"
	"testing"
)

func TestRndisNoSubmodeFallback(t *testing.T) {
	rndis := &UsbGadgetRndis{}
	rndis.SetSubmode(1)
	rndis.SetSubmodeNoFallback(true)

	probe_err := errors.New("ICS probe failed")
	if err := rndis.fallback_to_gateway_mode(probe_err); err != probe_err {
		t.Fatalf("fallback returned %v, want %v", err, probe_err)
	}
	if got := rndis.GetSubmode(); got != 1 {
		t.Fatalf("submode = %d, want 1", got)
	}
}
