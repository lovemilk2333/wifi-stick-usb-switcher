//go:build dbus

package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
)

const (
	DBUS_OBJECT_PATH = dbus.ObjectPath("/io/github/lovemilk2333/wifi_stick_usb_switcher/go/Control")
	DBUS_INTERFACE   = "io.github.lovemilk2333.wifi_stick_usb_switcher.go.Control"
	DBUS_BUS_NAME    = "io.github.lovemilk2333.wifi-stick-usb-switcher.go"
)

type dbusClient struct{ connection *dbus.Conn }

func (this *dbusClient) Call(package_type daemonipc.IPCPackageType, payload []any, timeout time.Duration) (string, error) {
	request, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode D-Bus request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var response_type uint32
	var response_json string
	err = this.connection.Object(DBUS_BUS_NAME, DBUS_OBJECT_PATH).CallWithContext(ctx, DBUS_INTERFACE+".Request", 0, uint32(package_type), string(request)).Store(&response_type, &response_json)
	if err != nil {
		return "", fmt.Errorf("D-Bus request failed: %w", err)
	}
	if response_type == 0 {
		return "", fmt.Errorf("daemon returned no response")
	}
	// Keep formatting identical to the stream transport's public CLI output.
	switch daemonipc.IPCPackageType(response_type) {
	case daemonipc.PACKAGE_TOGGLE_LED_RESP:
		var values []bool
		if err := json.Unmarshal([]byte(response_json), &values); err != nil {
			return "", fmt.Errorf("decode LED response: %w", err)
		}
		if len(values) == 0 {
			return "", fmt.Errorf("decode LED response: empty payload")
		}
		if values[0] {
			return "led: off", nil
		}
		return "led: on", nil
	case daemonipc.PACKAGE_STATUS_RESP, daemonipc.PACKAGE_GADGET_RESP, daemonipc.PACKAGE_SIMULATE_BUTTON_RESP:
		var values []string
		if err := json.Unmarshal([]byte(response_json), &values); err != nil {
			return "", fmt.Errorf("decode daemon response: %w", err)
		}
		if len(values) == 0 {
			return "", fmt.Errorf("decode daemon response: empty payload")
		}
		prefix := map[daemonipc.IPCPackageType]string{daemonipc.PACKAGE_STATUS_RESP: "status: ", daemonipc.PACKAGE_GADGET_RESP: "gadget: ", daemonipc.PACKAGE_SIMULATE_BUTTON_RESP: "button: "}[daemonipc.IPCPackageType(response_type)]
		return prefix + values[0], nil
	default:
		return "", fmt.Errorf("daemon returned unexpected response package %d", response_type)
	}
}

func (this *dbusClient) Close() error { return this.connection.Close() }

// InitIPCClient connects to the daemon on the system bus.
func InitIPCClient(_ time.Duration, _ time.Duration) (IPCClient, error) {
	connection, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to D-Bus system bus: %w", err)
	}
	return &dbusClient{connection: connection}, nil
}
