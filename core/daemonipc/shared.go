package daemonipc

const (
	PACKAGE_INTERNAL_SERVER_ERROR IPCPackageType = 0
	// PACKAGE_QUERY_PAYLOAD          IPCPackageType = 1
	// PACKAGE_PAYLOAD_STRUCT         IPCPackageType = 2
	PACKAGE_INVALID_PAYLOAD IPCPackageType = 3

	PACKAGE_TOGGLE_LED      IPCPackageType = 1024
	PACKAGE_TOGGLE_LED_RESP IPCPackageType = 1025
)

type ToggleLEDTarget uint8

const (
	TOGGLE_LED_NONE ToggleLEDTarget = iota
	TOGGLE_LED_OFF
	TOGGLE_LED_ON
)
