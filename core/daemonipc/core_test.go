package daemonipc

import (
	"io"
	"testing"

	ipc "github.com/james-barrow/golang-ipc"
)

type mockIPC struct {
	types  []int
	writes [][]byte
}

func (m *mockIPC) Close()                      {}
func (m *mockIPC) Read() (*ipc.Message, error) { return nil, io.EOF }
func (m *mockIPC) Status() string              { return "ok" }
func (m *mockIPC) StatusCode() ipc.Status      { return ipc.Status(0) }
func (m *mockIPC) Write(msgType int, message []byte) error {
	m.types = append(m.types, msgType)
	m.writes = append(m.writes, message)
	return nil
}

type fakeDaemon struct{ off bool }

func (d *fakeDaemon) GetTurnOffLeds() bool    { return d.off }
func (d *fakeDaemon) SetTurnOffLeds(off bool) { d.off = off }
func (d *fakeDaemon) SimulateButton(target SimulateButtonTarget) {}

func TestServerParseDataUsesTypeinject(t *testing.T) {
	server := InitServer(&fakeDaemon{})

	// refactor: parse_data delegates to typeinject using stored metadata
	payload, err := server.parse_data(server.GetPayloadStruct(PACKAGE_TOGGLE_LED), []byte("[1]"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payload) != 1 || payload[0].Interface().(ToggleLEDTarget) != TOGGLE_LED_OFF {
		t.Fatalf("unexpected payload %v", payload)
	}

	// wrong number of args must fail validation
	if _, err := server.parse_data(server.GetPayloadStruct(PACKAGE_TOGGLE_LED), []byte("[1, 3]")); err == nil {
		t.Fatal("expected length mismatch error")
	}
}

func TestClientHandleDataToggleLEDResp(t *testing.T) {
	client, channel := InitClient()
	client.ipc_impl = &mockIPC{}

	// payload [true] => turn_off_led true => "led: off"
	if err := client.handle_data(PACKAGE_TOGGLE_LED_RESP, []byte("[true]")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	select {
	case msg := <-channel:
		if msg != "led: off" {
			t.Fatalf("expected 'led: off', got %q", msg)
		}
	default:
		t.Fatal("expected channel message for true")
	}

	// payload [false] => "led: on"
	if err := client.handle_data(PACKAGE_TOGGLE_LED_RESP, []byte("[false]")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	select {
	case msg := <-channel:
		if msg != "led: on" {
			t.Fatalf("expected 'led: on', got %q", msg)
		}
	default:
		t.Fatal("expected channel message for false")
	}

	// wrong type (number where bool expected) must not panic and must not deliver
	if err := client.handle_data(PACKAGE_TOGGLE_LED_RESP, []byte("[123]")); err != nil {
		t.Fatalf("handle_data should recover, got err: %v", err)
	}
	select {
	case msg := <-channel:
		t.Fatalf("did not expect channel message, got %q", msg)
	default:
		// expected: nothing delivered
	}
}
