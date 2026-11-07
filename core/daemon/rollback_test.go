package daemon

import (
	"testing"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/led"
)

func TestShouldRollback(t *testing.T) {
	cases := []struct {
		name      string
		modes     int
		failed    applyTarget
		last_good applyTarget
		want      bool
	}{
		{
			name:      "single mode never rolls back",
			modes:     1,
			failed:    applyTarget{mode: 0, submode: 1},
			last_good: applyTarget{mode: 0, submode: 0},
			want:      false,
		},
		{
			name:      "different mode rolls back",
			modes:     2,
			failed:    applyTarget{mode: 1, submode: 0},
			last_good: applyTarget{mode: 0, submode: 0},
			want:      true,
		},
		{
			name:      "same mode different submode rolls back",
			modes:     2,
			failed:    applyTarget{mode: 0, submode: 1},
			last_good: applyTarget{mode: 0, submode: 0},
			want:      true,
		},
		{
			name:      "same target has nothing to restore",
			modes:     2,
			failed:    applyTarget{mode: 0, submode: 0},
			last_good: applyTarget{mode: 0, submode: 0},
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := should_rollback(tc.modes, tc.failed, tc.last_good); got != tc.want {
				t.Fatalf("should_rollback() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCommitMode(t *testing.T) {
	d := &Daemon{}
	d.apply_error.Store(true)

	d.commit_mode(applyTarget{mode: 1, submode: 1})

	if d.last_good_mode != 1 || d.last_good_submode != 1 {
		t.Fatalf("commit_mode did not record target: mode=%d submode=%d", d.last_good_mode, d.last_good_submode)
	}
	if d.apply_error.Load() {
		t.Fatal("commit_mode must clear the apply error state")
	}
}

func TestHandleApplyFailureTurnsOffAllLeds(t *testing.T) {
	interp := led.NewLedInterpreter(nil)
	interp.SetMode(led.MODE_PRESET_ON)

	d := &Daemon{interpreters: []*led.LedInterpreter{interp}}
	d.handle_apply_failure(applyTarget{mode: 0, submode: 0}, false)

	if !d.apply_error.Load() {
		t.Fatal("apply_error should be set when there is no rollback target")
	}
	if interp.GetMode() != led.MODE_PRESET_OFF {
		t.Fatal("all LEDs should be off after an unrecoverable apply failure")
	}
	if d.mode_changing {
		t.Fatal("mode_changing should be released after failure handling")
	}
	if !d.apply_done.Load() {
		t.Fatal("apply_done should be set after failure handling")
	}
}

func TestHandleApplyFailureSameTargetDoesNotRollback(t *testing.T) {
	interp := led.NewLedInterpreter(nil)
	interp.SetMode(led.MODE_PRESET_ON)

	d := &Daemon{
		interpreters: []*led.LedInterpreter{interp},
	}
	d.last_good_mode = 0
	d.last_good_submode = 0

	d.handle_apply_failure(applyTarget{mode: 0, submode: 0}, false)

	if !d.apply_error.Load() {
		t.Fatal("apply_error should be set when target equals last good mode")
	}
	if interp.GetMode() != led.MODE_PRESET_OFF {
		t.Fatal("all LEDs should be off when there is nothing to restore")
	}
}
