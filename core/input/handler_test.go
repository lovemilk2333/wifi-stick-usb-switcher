package input

import (
	"container/list"
	"testing"
	"time"
)

func newTestDevice() *InputDevice {
	return &InputDevice{
		Config:      &InputDeviceConfig{MultipleTapThreshold: -1, LongTapThreshold: 300 * time.Millisecond},
		event_queue: list.New(),
	}
}

func TestInjectEvent(t *testing.T) {
	dev := newTestDevice()

	dev.InjectEvent(&InputEvent{Type: INPUT_TAP, Status: DEVICE_STATUS_NORMAL})
	events := dev.Tick()
	if len(events) != 1 || events[0].Type != INPUT_TAP {
		t.Fatalf("unexpected events %+v", events)
	}

	dev.InjectEvent(&InputEvent{Type: INPUT_LONG_TAP, Status: DEVICE_STATUS_NORMAL})
	events = dev.Tick()
	if len(events) != 1 || events[0].Type != INPUT_LONG_TAP {
		t.Fatalf("unexpected events %+v", events)
	}
}

func TestInjectPress(t *testing.T) {
	dev := newTestDevice()

	dev.InjectPress(5 * time.Second)
	st := dev.State()
	if st == nil {
		t.Fatalf("expected non-nil state")
	}
	if st.Duration < 5*time.Second {
		t.Fatalf("unexpected duration %v", st.Duration)
	}
	if st.Type != INPUT_LONG_TAP {
		t.Fatalf("expected LONG_TAP state, got %v", st.Type)
	}
}
