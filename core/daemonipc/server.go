package daemonipc

import (
	"fmt"
)

type daemonInterface interface {
	GetTurnOffLeds() bool
	SetTurnOffLeds(off bool)
	SimulateButton(target SimulateButtonTarget, count int) error
}

func InitServer(daemon daemonInterface) *IPCFramework {
	IPCServer := NewIPCFramework()

	IPCServer.SetFallbackHandler(func(this *IPCFramework, status IPCHandlerStatus, err error, package_type IPCPackageType, data []byte) (*IPCPackage, error) {
		switch status {
		case HANDLER_STATUS_PAYLOAD_STRUCT_NOT_FOUND:
			return &IPCPackage{
				Type: PACKAGE_INTERNAL_SERVER_ERROR,
				Payload: []any{
					fmt.Sprintf("no such payload struct for package `%d`, payload: %s", package_type, data),
				},
			}, nil
		case HANDLER_STATUS_INVALID_HANDLER, HANDLER_STATUS_INVALID_HANDLER_RESULT:
			return &IPCPackage{
				Type: PACKAGE_INTERNAL_SERVER_ERROR,
				Payload: []any{
					fmt.Sprintf("invalid handler (or its result) for package `%d`, payload: %s", package_type, data),
				},
			}, nil
		case HANDLER_STATUS_INVALID_PAYLOAD:
			// TODO resp the data struct package
			return &IPCPackage{
				Type: PACKAGE_INVALID_PAYLOAD,
				Payload: []any{
					err.Error(),
				},
			}, nil
		default:
			return &IPCPackage{
				Type: PACKAGE_INTERNAL_SERVER_ERROR,
				Payload: []any{
					fmt.Sprintf("cannot handle error because of unexpected IPC handler status `%d`: %v", status, err),
				},
			}, nil
		}
	})

	IPCServer.RegisterHandler(
		PACKAGE_TOGGLE_LED,
		func(this *IPCFramework, target ToggleLEDTarget) (*IPCPackage, error) {
			switch target {
			case TOGGLE_LED_NONE: // resp current led state
				break
			case TOGGLE_LED_OFF:
				daemon.SetTurnOffLeds(true)
			case TOGGLE_LED_ON:
				daemon.SetTurnOffLeds(false)
			}

			return &IPCPackage{
				Type: PACKAGE_TOGGLE_LED_RESP,
				Payload: []any{ // Off or not
					daemon.GetTurnOffLeds(),
				},
			}, nil
		},
	)

	IPCServer.RegisterHandler(
		PACKAGE_SIMULATE_BUTTON,
		func(this *IPCFramework, target SimulateButtonTarget, count int) (*IPCPackage, error) {
			err := daemon.SimulateButton(target, count)
			if err != nil {
				// respond with the error message instead of a Go error so the
				// client still receives a package and does not time out
				return &IPCPackage{
					Type:    PACKAGE_SIMULATE_BUTTON_RESP,
					Payload: []any{"error: " + err.Error()},
				}, nil
			}
			return &IPCPackage{
				Type: PACKAGE_SIMULATE_BUTTON_RESP,
				Payload: []any{
					target.SimulateButtonActionName(),
				},
			}, nil
		},
	)

	return IPCServer
}
