package ipc

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/typeinject"
)

var ipc_mapping = map[string]daemonipc.IPCPackageType{
	"led":    daemonipc.PACKAGE_TOGGLE_LED,
	"tap":    daemonipc.PACKAGE_SIMULATE_BUTTON,
	"gadget": daemonipc.PACKAGE_GADGET,
	"status": daemonipc.PACKAGE_STATUS,
}

type IPCCmd struct {
	Command        string        `arg:"positional" help:"operation: led, tap, gadget, or status"`
	Timeout        time.Duration `arg:"-t,--timeout" default:"10s" help:"maximum time to wait for a daemon response"`
	ConnectTimeout time.Duration `arg:"--connect-timeout" default:"5s" help:"maximum time to connect to the daemon"`
	DialRetry      time.Duration `arg:"--dial-retry" default:"1s" help:"retry interval while connecting to the daemon"`
	List           bool          `arg:"--list" help:"show available operations, argument types, and examples"`
	Args           []string      `arg:"positional" help:"operation arguments; see --list for types and examples"`
}

// IPCClient provides one request/response operation over the selected transport.
type IPCClient interface {
	Call(package_type daemonipc.IPCPackageType, payload []any, timeout time.Duration) (string, error)
	Close() error
}

// CallIPC resolves command to a package type, builds the typed payload, sends it,
// and returns the daemon's textual response.
func CallIPC(ipc_client IPCClient, ipc_args *IPCCmd, command string, args []string) (string, error) {
	if ipc_client == nil {
		return "", fmt.Errorf("IPC client is not connected")
	}
	ipc_command := strings.TrimSpace(command)
	package_type, ok := ipc_mapping[ipc_command]
	if !ok {
		return "", fmt.Errorf("no such IPC command `%s`", ipc_command)
	}

	payload, err := build_payload(package_type, args)
	if err != nil {
		return "", err
	}

	return ipc_client.Call(package_type, payload, ipc_args.Timeout)
}

// ipc_builders maps each package type to a builder function. The builder's typed
// parameters are converted from the CLI string args by typeinject (via JSON), and
// its ([]any, error) return value becomes the IPC payload.
var ipc_builders = map[daemonipc.IPCPackageType]any{}

// RegisterHandler registers a builder function for an IPC package type. Adding a
// new command is just a RegisterHandler call together with an ipc_mapping entry.
func RegisterHandler(package_type daemonipc.IPCPackageType, builder any) {
	ipc_builders[package_type] = builder
}

func init() {
	RegisterHandler(daemonipc.PACKAGE_TOGGLE_LED, build_toggle_led)
	RegisterHandler(daemonipc.PACKAGE_SIMULATE_BUTTON, build_tap)
	RegisterHandler(daemonipc.PACKAGE_GADGET, build_gadget)
	RegisterHandler(daemonipc.PACKAGE_STATUS, build_status)
}

// build_status maps `ipc status` to the status package payload (no args).
func build_status(_ string) ([]any, error) {
	return []any{""}, nil
}

// build_gadget maps `ipc gadget [spec]` to the gadget package payload:
// an empty spec queries the current gadget, `name[.submode]` switches to it.
func build_gadget(spec string) ([]any, error) {
	return []any{strings.TrimSpace(spec)}, nil
}

// build_tap maps a CLI subcommand to a SimulateButtonTarget (+ count for multi).
func build_tap(action string, count string) ([]any, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "", "-", "tap":
		return []any{daemonipc.SIMULATE_BUTTON_TAP, 0}, nil
	case "long":
		return []any{daemonipc.SIMULATE_BUTTON_LONG, 0}, nil
	case "shutdown":
		return []any{daemonipc.SIMULATE_BUTTON_SHUTDOWN, 0}, nil
	case "multi":
		if strings.TrimSpace(count) == "" {
			return nil, fmt.Errorf("multi requires a count: ipc tap multi <n>")
		}
		n, err := strconv.Atoi(strings.TrimSpace(count))
		if err != nil {
			return nil, fmt.Errorf("invalid multi count %q (must be an integer)", count)
		}
		if n < 2 {
			return nil, fmt.Errorf("multi-tap count must be >= 2")
		}
		return []any{daemonipc.SIMULATE_BUTTON_MULTI, n}, nil
	default:
		return nil, fmt.Errorf("invalid tap action %q (use tap/long/shutdown/multi)", action)
	}
}

// build_toggle_led maps a CLI state string to a ToggleLEDTarget.
func build_toggle_led(state string) ([]any, error) {
	state = strings.ToLower(strings.TrimSpace(state))
	var target daemonipc.ToggleLEDTarget
	switch state {
	case "", "get", "state", "query":
		target = daemonipc.TOGGLE_LED_NONE
	case "on", "true", "1":
		target = daemonipc.TOGGLE_LED_ON
	case "off", "false", "0":
		target = daemonipc.TOGGLE_LED_OFF
	default:
		return nil, fmt.Errorf("invalid led state %q (use on/off/true/false)", state)
	}
	return []any{target}, nil
}

// build_payload converts CLI string args into the typed payload for package_type
// by dispatching to the registered builder through typeinject.
func build_payload(package_type daemonipc.IPCPackageType, args []string) ([]any, error) {
	builder, ok := ipc_builders[package_type]
	if !ok {
		// unknown package type: pass raw string args through
		out := make([]any, len(args))
		for i, a := range args {
			out[i] = a
		}
		return out, nil
	}

	// pad args to the builder's arity so typeinject gets the expected count
	fn_type := reflect.TypeOf(builder)
	arity := fn_type.NumIn()
	padded := make([]string, arity)
	for i := 0; i < arity; i++ {
		if i < len(args) {
			padded[i] = args[i]
		}
	}
	data, err := json.Marshal(padded)
	if err != nil {
		return nil, err
	}

	results, err := typeinject.CallFunctionJSON(builder, data)
	if err != nil {
		return nil, err
	}

	// last return value may be an error
	if n := len(results); n > 0 {
		if e, ok := results[n-1].Interface().(error); ok && e != nil {
			return nil, e
		}
	}

	payload, ok := results[0].Interface().([]any)
	if !ok {
		return nil, fmt.Errorf("builder for %v returned unexpected type %T", package_type, results[0].Interface())
	}
	return payload, nil
}

// IPCCommandArg describes one argv slot of a command's builder.
type IPCCommandArg struct {
	Type string
}

// IPCCommandDesc describes an IPC command: its name, package type and argv.
type IPCCommandDesc struct {
	Name        string
	PackageType daemonipc.IPCPackageType
	Args        []IPCCommandArg
}

// describe_type renders a builder parameter type, expanding struct fields so the
// argv "struct" is visible in --list output.
func describe_type(t reflect.Type) string {
	if t == nil {
		return "any"
	}
	elem := t
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() == reflect.Struct {
		var fields []string
		for i := 0; i < elem.NumField(); i++ {
			f := elem.Field(i)
			fields = append(fields, fmt.Sprintf("%s %s", f.Name, f.Type.String()))
		}
		return "struct{" + strings.Join(fields, "; ") + "}"
	}
	return t.String()
}

// ListCommands returns all registered IPC commands and their argv, derived from
// each builder's signature via typeinject.
func ListCommands() []IPCCommandDesc {
	descs := make([]IPCCommandDesc, 0, len(ipc_mapping))
	for name, pkg_type := range ipc_mapping {
		desc := IPCCommandDesc{Name: name, PackageType: pkg_type}
		if builder, ok := ipc_builders[pkg_type]; ok {
			fn_type := reflect.TypeOf(builder)
			metas, err := typeinject.GetStructByFunctionType(fn_type)
			if err == nil {
				for _, meta := range metas {
					if meta == nil {
						continue
					}
					desc.Args = append(desc.Args, IPCCommandArg{Type: describe_type(meta.Type)})
				}
			}
		}
		if len(desc.Args) == 0 {
			desc.Args = []IPCCommandArg{{Type: "<raw string args>"}}
		}
		descs = append(descs, desc)
	}

	sort.Slice(descs, func(i, j int) bool {
		return descs[i].Name < descs[j].Name
	})
	return descs
}

// ListCommandsString renders ListCommands as a human-readable listing.
func ListCommandsString() string {
	descs := ListCommands()
	var b strings.Builder
	for _, d := range descs {
		types := make([]string, len(d.Args))
		for i, a := range d.Args {
			types[i] = a.Type
		}
		example := map[string]string{
			"led":    "ipc led [on|off]",
			"tap":    "ipc tap [tap|long|shutdown|multi <count>]",
			"gadget": "ipc gadget [name[.submode]]",
			"status": "ipc status",
		}[d.Name]
		fmt.Fprintf(&b, "%-7s args: [%s]\n        %s\n", d.Name, strings.Join(types, ", "), example)
	}
	b.WriteString("\nExamples:\n  ipc led off\n  ipc tap multi 3\n  ipc gadget rndis.1\n  ipc status\n")
	return b.String()
}
