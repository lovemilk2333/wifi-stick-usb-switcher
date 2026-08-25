package daemonipc

import (
	"log"
)

type IPCClientRespChannel chan string

func InitClient() (*IPCFramework, IPCClientRespChannel) {
	IPClient := NewIPCFramework()
	channel := make(IPCClientRespChannel, 16)

	IPClient.SetFallbackHandler(func(this *IPCFramework, status IPCHandlerStatus, err error, package_type IPCPackageType, data []byte) (*IPCPackage, error) {
		switch status {
		case HANDLER_STATUS_PAYLOAD_STRUCT_NOT_FOUND:
			log.Printf("no such payload struct for package `%d`, payload: %s\n", package_type, data)
		case HANDLER_STATUS_INVALID_HANDLER, HANDLER_STATUS_INVALID_HANDLER_RESULT:
			log.Printf("invalid handler (or its result) for package `%d`, payload: %s\n", package_type, data)
		case HANDLER_STATUS_INVALID_PAYLOAD:
			log.Print(err.Error())
		default:
			log.Printf("cannot handle error because of unexpected IPC handler status `%d`: %v\n", status, err)
		}

		return nil, nil
	})

	IPClient.RegisterHandler(
		PACKAGE_TOGGLE_LED_RESP,
		func(this *IPCFramework, turn_off_led bool) {
			if turn_off_led {
				channel <- "led: off"
			} else {
				channel <- "led: on"
			}
		},
	)

	return IPClient, channel
}
