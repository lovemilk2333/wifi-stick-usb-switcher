package daemonipc

const (
	// 不能用 0:golang-ipc 库保留 msgType 0(收发两侧都跳过),错误
	// 响应会被静默丢弃
	PACKAGE_INTERNAL_SERVER_ERROR IPCPackageType = 1
	// PACKAGE_QUERY_PAYLOAD          IPCPackageType = 2
	PACKAGE_INVALID_PAYLOAD IPCPackageType = 3

	PACKAGE_TOGGLE_LED         IPCPackageType = 1024
	PACKAGE_TOGGLE_LED_RESP    IPCPackageType = 1025
	PACKAGE_SIMULATE_BUTTON    IPCPackageType = 1026
	PACKAGE_SIMULATE_BUTTON_RESP IPCPackageType = 1027
)

type ToggleLEDTarget uint8

const (
	TOGGLE_LED_NONE ToggleLEDTarget = iota
	TOGGLE_LED_OFF
	TOGGLE_LED_ON
)

// SimulateButtonTarget selects which synthetic button action an IPC "tap"
// command triggers.
type SimulateButtonTarget uint8

const (
	SIMULATE_BUTTON_TAP      SimulateButtonTarget = iota // short click
	SIMULATE_BUTTON_LONG                                 // long press (enter/exit submode)
	SIMULATE_BUTTON_SHUTDOWN                             // long-press shutdown
	SIMULATE_BUTTON_MULTI                                // multi-tap (n clicks)
)

// SimulateButtonActionName returns the human-readable name of a target.
func (this SimulateButtonTarget) SimulateButtonActionName() string {
	switch this {
	case SIMULATE_BUTTON_TAP:
		return "tap"
	case SIMULATE_BUTTON_LONG:
		return "long"
	case SIMULATE_BUTTON_SHUTDOWN:
		return "shutdown"
	case SIMULATE_BUTTON_MULTI:
		return "multi"
	default:
		return "unknown"
	}
}
