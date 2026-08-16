package daemonipc

const (
	PACKAGE_QUERY_PARAMS IPCPackageType = 1

	PACKAGE_TOGGLE_LED      IPCPackageType = 1024
	PACKAGE_TOGGLE_LED_RESP IPCPackageType = 1025
)

type ToggleLEDTarget uint8

const (
	TOGGLE_LED_NONE ToggleLEDTarget = iota
	TOGGLE_LED_OFF
	TOGGLE_LED_ON
)

type ToggleLEDPayload struct {
	// `validate` is need to update when `TOGGLE_LED_*` add or change
	Target ToggleLEDTarget `json:"target" validate:"oneof=0 1 2"`
}

type ToggleLEDResp struct {
	Off bool `json:"off"`
}
