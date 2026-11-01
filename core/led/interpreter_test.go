package led

import (
	"testing"
	"time"
)

func TestLedInterpreterWaitsForModeAction(t *testing.T) {
	interp := NewLedInterpreter(nil)
	interp.SetMode(NewLedMode().Wait(20 * time.Millisecond).Wait(time.Hour).Done())
	interp.Start()
	defer interp.Stop()

	deadline := time.After(300 * time.Millisecond)
	for {
		interp.lock.Lock()
		index := interp.mode_action_index
		remaining := time.Until(interp.next_action_time)
		interp.lock.Unlock()
		if index == 0 && remaining > 30*time.Minute {
			return
		}
		select {
		case <-deadline:
			t.Fatal("WAIT duration from LedMode did not advance to the next action")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestLedInterpreterStartStopIdempotent(t *testing.T) {
	// Use a wait-only mode so apply_action never touches the nil Led.
	interp := NewLedInterpreter(nil)
	interp.SetMode(NewLedMode().Wait(time.Hour).Done())

	interp.Start()
	interp.Start() // idempotent
	time.Sleep(15 * time.Millisecond)
	interp.Stop()
	interp.Stop() // idempotent
}

func TestLedInterpreterSignalWake(t *testing.T) {
	interp := NewLedInterpreter(nil)
	interp.SetMode(NewLedMode().Wait(time.Hour).Wait(time.Hour).Done())
	interp.Start()
	defer interp.Stop()

	// SetMode cancels the old wait and starts the replacement mode immediately.
	done := make(chan struct{})
	go func() {
		interp.SetMode(NewLedMode().Wait(20 * time.Millisecond).Wait(time.Hour).Done())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SetMode blocked (wake not delivered)")
	}

	deadline := time.After(300 * time.Millisecond)
	for {
		interp.lock.Lock()
		index := interp.mode_action_index
		remaining := time.Until(interp.next_action_time)
		interp.lock.Unlock()
		if index == 0 && remaining > 30*time.Minute {
			return
		}
		select {
		case <-deadline:
			t.Fatal("replacement mode did not run after canceling the old wait")
		case <-time.After(time.Millisecond):
		}
	}
}
