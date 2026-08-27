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

func TestInjectMultiTap(t *testing.T) {
	dev := &InputDevice{
		Config: &InputDeviceConfig{
			MultipleTapThreshold: 300 * time.Millisecond,
			MultipleTapMaxCount:  5,
			LongTapThreshold:     300 * time.Millisecond,
		},
		event_queue: list.New(),
	}

	// timestamps in the past so the chain flushes on the first Tick
	now := time.Now().Add(-400 * time.Millisecond)
	for i := 0; i < 3; i++ {
		dev.InjectEvent(&InputEvent{Type: INPUT_TAP, Time: now, TapCount: 1, Status: DEVICE_STATUS_NORMAL})
	}

	events := dev.Tick()
	if len(events) != 1 {
		t.Fatalf("expected 1 merged event, got %d: %+v", len(events), events)
	}
	if events[0].Type != INPUT_MULTIPLE_TAP {
		t.Fatalf("expected MULTIPLE_TAP, got %v", events[0].Type)
	}
	if events[0].TapCount != 3 {
		t.Fatalf("expected TapCount 3, got %d", events[0].TapCount)
	}
}
