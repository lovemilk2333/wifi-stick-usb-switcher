package daemonipc

const (
	// 不能用 0:golang-ipc 库保留 msgType 0(收发两侧都跳过),错误
	// 响应会被静默丢弃
	PACKAGE_INTERNAL_SERVER_ERROR IPCPackageType = 1
	// PACKAGE_QUERY_PAYLOAD          IPCPackageType = 2
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
