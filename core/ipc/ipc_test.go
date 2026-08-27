package ipc

import (
	"testing"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
)

func TestBuildPayloadToggleLED(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected daemonipc.ToggleLEDTarget
		wantErr  bool
	}{
		{"no arg -> get state", nil, daemonipc.TOGGLE_LED_NONE, false},
		{"empty -> get state", []string{""}, daemonipc.TOGGLE_LED_NONE, false},
		{"true -> on", []string{"true"}, daemonipc.TOGGLE_LED_ON, false},
		{"on -> on", []string{"on"}, daemonipc.TOGGLE_LED_ON, false},
		{"1 -> on", []string{"1"}, daemonipc.TOGGLE_LED_ON, false},
		{"off -> off", []string{"off"}, daemonipc.TOGGLE_LED_OFF, false},
		{"false -> off", []string{"false"}, daemonipc.TOGGLE_LED_OFF, false},
		{"bad -> error", []string{"bogus"}, 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload, err := build_payload(daemonipc.PACKAGE_TOGGLE_LED, c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(payload) != 1 {
				t.Fatalf("expected 1 arg, got %d", len(payload))
			}
			if payload[0].(daemonipc.ToggleLEDTarget) != c.expected {
				t.Fatalf("expected %v, got %v", c.expected, payload[0])
			}
		})
	}
}

func TestBuildPayloadUnknownFallsBackToRaw(t *testing.T) {
	payload, err := build_payload(daemonipc.IPCPackageType(9999), []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payload) != 2 || payload[0].(string) != "a" || payload[1].(string) != "b" {
		t.Fatalf("unexpected fallback payload %v", payload)
	}
}

func TestListCommands(t *testing.T) {
	descs := ListCommands()
	if len(descs) == 0 {
		t.Fatalf("expected at least one command")
	}

	var toggle *IPCCommandDesc
	for i := range descs {
		if descs[i].Name == "toggle-led" {
			toggle = &descs[i]
			break
		}
	}
	if toggle == nil {
		t.Fatalf("toggle-led not listed")
	}
	if toggle.PackageType != daemonipc.PACKAGE_TOGGLE_LED {
		t.Fatalf("unexpected package type %v", toggle.PackageType)
	}
	if len(toggle.Args) != 1 || toggle.Args[0].Type != "string" {
		t.Fatalf("unexpected argv %v", toggle.Args)
	}
}

func TestBuildTap(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected daemonipc.SimulateButtonTarget
		wantErr  bool
	}{
		{"no arg -> tap", nil, daemonipc.SIMULATE_BUTTON_TAP, false},
		{"empty -> tap", []string{""}, daemonipc.SIMULATE_BUTTON_TAP, false},
		{"- -> tap", []string{"-"}, daemonipc.SIMULATE_BUTTON_TAP, false},
		{"tap -> tap", []string{"tap"}, daemonipc.SIMULATE_BUTTON_TAP, false},
		{"long -> long", []string{"long"}, daemonipc.SIMULATE_BUTTON_LONG, false},
		{"shutdown -> shutdown", []string{"shutdown"}, daemonipc.SIMULATE_BUTTON_SHUTDOWN, false},
		{"bad -> error", []string{"bogus"}, 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload, err := build_payload(daemonipc.PACKAGE_SIMULATE_BUTTON, c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if payload[0].(daemonipc.SimulateButtonTarget) != c.expected {
				t.Fatalf("expected %v, got %v", c.expected, payload[0])
			}
		})
	}
}
