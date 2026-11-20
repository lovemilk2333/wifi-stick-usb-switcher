package daemon

import (
	"net/netip"
	"testing"
	"time"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/led"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/usb"
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
	d.update_status()
	if got := d.RequestStatus(); got != "running" {
		t.Fatalf("status after success = %q, want running", got)
	}
}

func TestHandleApplyFailureTurnsOffAllLeds(t *testing.T) {
	interp := led.NewLedInterpreter(nil)
	interp.SetMode(led.MODE_PRESET_ON)

	d := &Daemon{interpreters: []*led.LedInterpreter{interp}}
	d.handle_apply_failure(applyTarget{mode: 0, submode: 0}, false, false)

	if !d.apply_error.Load() {
		t.Fatal("apply_error should be set when there is no rollback target")
	}
	if interp.GetMode() != led.MODE_PRESET_OFF {
		t.Fatal("all LEDs should be off after an unrecoverable apply failure")
	}
	if d.mode_changing.Load() {
		t.Fatal("mode_changing should be released after failure handling")
	}
	if !d.apply_done.Load() {
		t.Fatal("apply_done should be set after failure handling")
	}
	d.update_status()
	if got := d.RequestStatus(); got != "failed::gadget" {
		t.Fatalf("status after failure = %q, want failed::gadget", got)
	}
	d.mode_changing.Store(true)
	d.effect_kind = "submode"
	d.update_status()
	if got := d.RequestStatus(); got != "effecting::gadget::submode" {
		t.Fatalf("status during retry = %q, want effecting::gadget::submode", got)
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

	d.handle_apply_failure(applyTarget{mode: 0, submode: 0}, false, false)

	if !d.apply_error.Load() {
		t.Fatal("apply_error should be set when target equals last good mode")
	}
	if interp.GetMode() != led.MODE_PRESET_OFF {
		t.Fatal("all LEDs should be off when there is nothing to restore")
	}
}

func TestHandleApplyFailureKeepsRequestedSubmode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		separate     bool
		submode_only bool
	}{
		{name: "in-place switch", submode_only: true},
		{name: "separate gadget entries", separate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := &usb.UsbGadgetRndis{}
			requested.SetSubmode(1)
			d := &Daemon{
				modes:               []usb.UsbGadgetFunction{requested},
				submode_no_fallback: true,
			}
			if tc.separate {
				d.modes = []usb.UsbGadgetFunction{&usb.UsbGadgetRndis{}, requested}
				d.current_mode = 1
			}
			failed := applyTarget{mode: d.current_mode, submode: 1}
			d.mode_changing.Store(true)

			d.handle_apply_failure(failed, tc.submode_only, tc.submode_only)

			if d.current_mode != failed.mode || requested.GetSubmode() != failed.submode {
				t.Fatalf("requested target changed to mode %d, submode %d", d.current_mode, requested.GetSubmode())
			}
			if !d.apply_error.Load() || !d.apply_done.Load() || d.mode_changing.Load() {
				t.Fatal("failed apply should finish and release the mode change lock")
			}
		})
	}
}

func TestRetainedSubmodeRetry(t *testing.T) {
	for _, tc := range []struct {
		name         string
		interval     time.Duration
		no_fallback  bool
		in_place     bool
		user_request bool
	}{
		{name: "retry in place after configuration failure", interval: time.Second, no_fallback: true, in_place: true},
		{name: "rebuild after bind failure", interval: time.Second, no_fallback: true},
		{name: "user request cancels retry", interval: time.Second, no_fallback: true, user_request: true},
		{name: "zero disables retries", no_fallback: true},
		{name: "fallback enabled", interval: time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rndis := usb.NewUsbGadgetRndis(netip.Prefix{}, "", "", "", "", "", nil, netip.Addr{}, 0, 0, "", "", "")
			rndis.SetSubmode(1)
			d := &Daemon{
				modes:                  []usb.UsbGadgetFunction{rndis},
				last_good_submode:      1,
				submode_no_fallback:    tc.no_fallback,
				submode_retry_interval: tc.interval,
			}
			failed := applyTarget{submode: 1}
			earliest := time.Now().Add(tc.interval)
			d.handle_apply_failure(failed, false, tc.in_place)
			retry := d.submode_retry.Load()
			if !tc.no_fallback || tc.interval == 0 {
				if retry != nil {
					t.Fatal("retry scheduled while disabled")
				}
				return
			}
			if retry == nil || retry.target != failed || retry.deadline.Before(earliest) ||
				retry.deadline.After(time.Now().Add(tc.interval)) {
				t.Fatalf("unexpected retry: %+v", retry)
			}
			d.queue_due_submode_retry(retry.deadline.Add(-time.Nanosecond))
			if d.mode_changed {
				t.Fatal("retry queued before its deadline")
			}

			d.mode_changed = tc.user_request
			d.queue_due_submode_retry(retry.deadline)
			if d.submode_retry.Load() != nil || !d.mode_changed || d.submode_changed != tc.in_place {
				t.Fatal("retry did not preserve the expected apply path")
			}
		})
	}
}
