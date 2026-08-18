package daemonipc

import (
	"reflect"
)

type daemonInterface interface {
	GetTurnOffLeds() bool
	SetTurnOffLeds(off bool)
}

func InitServer(daemon daemonInterface) *IPCFramework {
	fw := NewIPCFramework()

	fw.RegisterHandler(
		PACKAGE_TOGGLE_LED,
		reflect.TypeOf(ToggleLEDPayload{}),
		func(this *IPCFramework, payload any) (*IPCPackage, error) {
			data := payload.(ToggleLEDPayload)
			switch data.Target {
			case TOGGLE_LED_NONE: // resp current led state
				break
			case TOGGLE_LED_OFF:
				daemon.SetTurnOffLeds(true)
			case TOGGLE_LED_ON:
				daemon.SetTurnOffLeds(false)
			}

			return &IPCPackage{
				Type: PACKAGE_TOGGLE_LED_RESP,
				Payload: &ToggleLEDResp{
					Off: daemon.GetTurnOffLeds(),
				},
			}, nil
		},
	)

	return fw
}
