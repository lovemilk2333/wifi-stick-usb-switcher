package usb

import (
	"debug/elf"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

/*
1. check `config_fs` if is exists, `gc -c` to remove
2. run `gc -a <type>` to create different type of usb gadget function
	a. for `rndis`: run ```sh
	// OR
	// if [ ! -e /etc/NetworkManager/system-connections/<connection-name>.nmconnection ]; then
	if ! nmcli connection show "<connection-name>" >/dev/null 2>&1; then
		# Create network connection
		nmcli connection add con-name <connection-name> \
							ifname <ifname> \
							type ethernet \
							ip4 <ip>

		# Set priorities so it doesn't take precedence over WiFi/mobile connections
		nmcli connection modify <connection-name> ipv4.route-metric 1500
		nmcli connection modify <connection-name> ipv4.dns-priority 150

		# Auto connection so it can be used for tethering
		nmcli connection modify <connection-name> ipv4.method shared

		# this is optional, only for sim card
		# user can run this in their own scripts
		# nmcli con add con-name "modem" type "gsm" ifname "wwan0qmi0"

		ip link set <ifname> up
		# <ip> like `10.22.33.1/24`, and this is the ip of itself
		ip addr add <ip-range> dev <ifname>
	fi
	```

	b. for 'ffs' (adb): run ```sh
		mkdir -p /dev/usb-ffs/adb
		# in offical version of gc name will be ffs.x
    	mount -t functionfs adb /dev/usb-ffs/adb
		# Fire Up Adbd
    	adbd -D # NOTE: run in new thread
	```

	(to create mixed usb gadget, we need store function options in binary like `O_ADB | O_RNDIS => 0b11`)
3. run `gc -d` to disable that not update gadget
4. run `gc -l` to check and update current gadget state
5. run `gc -e` to enable all gadgets (apply changes)
*/

type UsbGadgetFunctionCode uint32

const (
	USB_GADGET_FUNCTION_CODE_NONE  UsbGadgetFunctionCode = 0
	USB_GADGET_FUNCTION_CODE_RNDIS UsbGadgetFunctionCode = 0b1
	USB_GADGET_FUNCTION_CODE_ADB   UsbGadgetFunctionCode = 0b01
)

type UsbGadgetContext = *UsbGadget

type UsbGadgetFunction interface {
	setEffected(effected bool)
	getEffected() bool
	getPath() string
	getInstance() string
	setInstance(instance string)
	getCode() UsbGadgetFunctionCode
	getType() string
	add(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error
	remove(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error
	effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error
	enable(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error
}

type UsbGadgetFunctionBase struct {
	instance string // like `rndis.1` or `adb`, you can access `<CONFIG_FS>/functions/<this.getType()>.<instance>`
	_type    string
	code     UsbGadgetFunctionCode
	effected bool
}

func (this *UsbGadgetFunctionBase) getType() string {
	return this._type
}

func (this *UsbGadgetFunctionBase) getInstance() string {
	return this.instance
}

func (this *UsbGadgetFunctionBase) setInstance(instance string) {
	this.instance = instance
}

func (this *UsbGadgetFunctionBase) getCode() UsbGadgetFunctionCode {
	return this.code
}

func (this *UsbGadgetFunctionBase) getPath() string {
	return this._type + "." + this.instance
}

func (this *UsbGadgetFunctionBase) getEffected() bool {
	return this.effected
}

func (this *UsbGadgetFunctionBase) setEffected(effected bool) {
	this.effected = effected
}

func (this *UsbGadgetFunctionBase) defaultAdd(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	return this.add(ctx, gc)
}

func (this *UsbGadgetFunctionBase) add(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	_type := this.getType()
	output, err := gc("-a", _type)
	if err != nil {
		if len(output) == 0 {
			output = "(no output)"
		}

		log.Printf("WARN: cannot add UsbGadgetFunction (type: %s, err: %s): %s\n", _type, err, output)
		return err
	}

	return nil
}

// enable 在 gadget 绑定(enableGadget 的 gc -e)之后调用 — 此时内核接口
// (如 usb0)才存在。默认无操作;需要运行时管理的实现覆写它。
func (this *UsbGadgetFunctionBase) enable(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	return nil
}

func (this *UsbGadgetFunctionBase) defaultRemove(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	return this.remove(ctx, gc)
}

func (this *UsbGadgetFunctionBase) remove(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	instance := this.getInstance()
	if len(instance) == 0 {
		// already removed
		return nil
	}
	output, err := gc("-r", instance)
	if err != nil {
		if len(output) == 0 {
			output = "(no output)"
		}

		log.Printf("WARN: cannot remove UsbGadgetFunction (type: %s, err: %s): %s\n", this.getType(), err, output)
		return err
	}

	return nil
}

// type UsbGadgetControllerConfig struct {
// 	Vendor         string // vendor code in hex like `0x0001`
// 	Product        string // product code in hex like `0x0001`
// 	DeviceClass    string
// 	DeviceSubClass string
// 	DeviceProtocol string

// 	Serialnumber string
// 	Manufacturer string
// 	ProductName  string
// }

type UsbGadgetController struct {
	gadget  *UsbGadget
	gc_path string

	current_functions map[string]UsbGadgetFunction
	target_functions  map[string]UsbGadgetFunction

	// Config *UsbGadgetControllerConfig
}

func (this *UsbGadgetController) check() error {
	err := this.checkGc()
	if err != nil {
		return err
	}

	// if !this.gadget.init {
	// 	// init gc
	// }

	return nil
}

func (this *UsbGadgetController) checkGc() error {
	var gc_path string
	gc_path_normalized := strings.TrimSpace(this.gc_path)

	if !filepath.IsAbs(gc_path_normalized) {
		gc_path_abs, err := exec.LookPath(this.gc_path)
		if err != nil {
			return fmt.Errorf("cannot find gc ELF: %w", err)
		}

		gc_path = gc_path_abs
	} else {
		gc_path = gc_path_normalized
	}

	_, err := elf.Open(gc_path)
	if err != nil {
		return fmt.Errorf("invalid gc ELF: %w", err)
	}

	this.gc_path = gc_path

	return nil
}

const RNDIS_USE_GADGET_FUNCTION = "rndis"

func (this *UsbGadgetController) resetFunctions(targets bool) {
	this.current_functions = make(map[string]UsbGadgetFunction)
	if targets {
		this.target_functions = make(map[string]UsbGadgetFunction)
	}
}

func (this *UsbGadgetController) gc(args ...string) (string, error) {
	cmd := exec.Command(this.gc_path, args...)

	output_binary, err := cmd.CombinedOutput()
	output := string(output_binary)

	return output, err
}

func (this *UsbGadgetController) UpdateGadget() []error {
	return this.updateGadget()
}

/*
╰─# gc -l
ID 18d1:d001 'g1'
  UDC			ci_hdrc.0
  bcdUSB		2.00
  bDeviceClass		0x00
  bDeviceSubClass	0x00
  bDeviceProtocol	0x00
  bMaxPacketSize0	64
  idVendor		0x18d1
  idProduct		0xd001
  bcdDevice		0.01
  Language: 	0x409
    Manufacturer	HandsomeTech
    Product		HandsomeMod Device
    Serial Number	0123456789
  Function, type: ffs instance: adb
    dev_name		adb
  Function, type: rndis instance: rndis.1
    dev_addr		3a:e9:58:ba:20:3b
    host_addr		8e:c3:64:bd:88:de
    ifname		usb0
    qmult		5
  Configuration: 'c1' ID: 1
    MaxPower		2
    bmAttributes	0x80
    Language: 	0x409
      configuration	c1
    adb -> ffs adb
    rndis.1 -> rndis rndis.1
*/

type gcListState uint8

const (
	GC_LIST_STATE_IGNORE gcListState = iota
	GC_LIST_STATE_IGNORE_ACTION
	GC_LIST_STATE_GLOBAL_FIELD
	GC_LIST_STATE_LANGUAGE
	GC_LIST_STATE_LANGUAGE_FIELD
	GC_LIST_STATE_FUNCTION
	GC_LIST_STATE_FUNCTION_FIELD
	GC_LIST_STATE_CONFIG
	GC_LIST_STATE_CONFIG_FIELD
	GC_LIST_STATE_CONFIG_LANGUAGE
)

const (
	GC_LIST_MATCHER_ID                = "ID "
	GC_LIST_MATCHER_LANGUAGE          = "Language: "
	GC_LIST_MATCHER_FUNCTION          = "Function, type: "
	GC_LIST_MATCHER_FUNCTION_INSTANCE = "instance: "
	GC_LIST_MATCHER_CONFIG            = "Configuration: "
)

/*
returns: id like `g1`
*/
func (this *UsbGadgetController) parseGcId(line string) string {
	start := strings.IndexByte(line, '\'')
	if start == -1 {
		return ""
	}
	end := strings.IndexByte(line[start+1:], '\'')
	if end == -1 {
		return ""
	}

	return line[start+1 : start+1+end]
}

/*
returns: (key, value) like (`bDeviceClass`, `0x00`)
*/
func (this *UsbGadgetController) parseGcGlobalField(line string) (string, string) {
	// gc -l separates key and value with tabs (one or more); keys never
	// contain tabs but values may contain spaces ("HandsomeMod Device"),
	// and the key "Serial Number" contains a space too — so only a tab is
	// a reliable separator.  Splitting at the last whitespace instead
	// (the old fallback) broke for two-word keys with an empty value:
	// "Serial Number" became key="Serial" value="Number", which got
	// written back to configfs as strings/0x409/serial (EACCES).
	if idx := strings.LastIndexByte(line, '\t'); idx != -1 {
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		key = strings.TrimSuffix(key, ":") // "Language: " -> "Language"
		return key, value
	}

	// No tab: only a "Key: value" line (e.g. "Language: 0x409") is valid.
	if idx := strings.IndexByte(line, ':'); idx != -1 {
		return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:])
	}

	// No separator at all — key with an empty value (e.g. "UDC" when the
	// gadget is unbound, "Manufacturer" when the string is unset).
	return "", ""
}

/*
returns: (key, value) like (`ifname`, `usb0`)
*/
func (this *UsbGadgetController) parseGcFunctionField(line string) (string, string) {
	return this.parseGcGlobalField(line)
}

/*
returns: (type, instance) like (`rndis`, `rndis.1`)
*/
func (this *UsbGadgetController) parseGcFunction(line string) (string, string) {
	if len(line) < len(GC_LIST_MATCHER_FUNCTION) {
		return "", ""
	}

	line = strings.TrimSpace(line[len(GC_LIST_MATCHER_FUNCTION):])

	instIdx := strings.Index(line, GC_LIST_MATCHER_FUNCTION_INSTANCE)
	if instIdx == -1 {
		return "", ""
	}

	_type := strings.TrimSpace(line[:instIdx])
	instance := strings.TrimSpace(line[instIdx+len(GC_LIST_MATCHER_FUNCTION_INSTANCE):])
	return _type, instance
}

/*
returns: (language, language state, gadget state, functions, error)
*/
func (this *UsbGadgetController) parseGcList(gadget_id string) (string, map[string]string, UsbGadgetState, map[string]UsbGadgetFunction, []error) {
	errors := make([]error, 0)

	output, err := this.gc("-l")
	if err != nil {
		errors = append(errors, fmt.Errorf("cannot get `gc -l` output: %w", err))
		return "", nil, nil, nil, errors
	}

	output = strings.TrimSpace(output)

	if output == "" {
		return "", nil, nil, nil, nil
	}

	global_fields := UsbGadgetState{}
	language_strings := make(map[string]map[string]string)
	current_language := "__unknown__"
	result_language := ""
	function_fields := make(map[string]string)
	functions := make(map[string]UsbGadgetFunction)

	state := GC_LIST_STATE_IGNORE
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, GC_LIST_MATCHER_ID) {
			id := this.parseGcId(line)
			if id == gadget_id {
				state = GC_LIST_STATE_GLOBAL_FIELD
				continue
			} else {
				state = GC_LIST_STATE_IGNORE
			}
		}

		if state == GC_LIST_STATE_IGNORE {
			continue
		}

		switch {
		case state == GC_LIST_STATE_GLOBAL_FIELD && strings.HasPrefix(line, GC_LIST_MATCHER_LANGUAGE):
			state = GC_LIST_STATE_LANGUAGE
		case strings.HasPrefix(line, GC_LIST_MATCHER_FUNCTION):
			state = GC_LIST_STATE_FUNCTION
		case strings.HasPrefix(line, GC_LIST_MATCHER_CONFIG):
			state = GC_LIST_STATE_CONFIG
		case state == GC_LIST_STATE_CONFIG_FIELD && strings.HasPrefix(line, GC_LIST_MATCHER_LANGUAGE):
			state = GC_LIST_STATE_CONFIG_LANGUAGE
		}

		if state == GC_LIST_STATE_IGNORE_ACTION {
			continue
		}

		switch state {
		case GC_LIST_STATE_GLOBAL_FIELD:
			key, value := this.parseGcGlobalField(line)
			if key != "" && value != "" {
				global_fields[key] = value
			} else {
				log.Printf("WARN: cannot parse global field for `gc -l`, gadget `%s`: key and/or value is empty for line `%s`\n", gadget_id, line)
			}
		case GC_LIST_STATE_LANGUAGE:
			_, language := this.parseGcGlobalField(line)
			if language != "" {
				state = GC_LIST_STATE_LANGUAGE_FIELD
				current_language = language
				language_strings[language] = make(map[string]string)
			} else {
				log.Printf("WARN: cannot parse global `Language` field for `gc -l`, gadget `%s`: language is empty for line `%s`\n", gadget_id, line)
				state = GC_LIST_STATE_IGNORE_ACTION
			}
		case GC_LIST_STATE_LANGUAGE_FIELD:
			key, value := this.parseGcGlobalField(line)
			if key != "" && value != "" {
				// see struct `UsbGadgetSubpathStrings`
				// Manufacturer -> manufacturer
				// Serial Number -> serialnumber
				language_strings[current_language][strings.ToLower(strings.ReplaceAll(key, " ", ""))] = value
			} else {
				log.Printf("WARN: cannot parse language field for `gc -l`, gadget `%s`: key and/or value is empty for line `%s`\n", gadget_id, line)
			}
		case GC_LIST_STATE_FUNCTION:
			if len(function_fields) > 0 {
				function, err := AdaptUsbGadgetFunction(function_fields["type"], function_fields["instance"], function_fields)
				if err != nil {
					errors = append(errors, err)
					break // break switch
				}
				functions[function.getPath()] = function
				clear(function_fields)
			}

			_type, instance := this.parseGcFunction(line)
			if _type != "" && instance != "" {
				state = GC_LIST_STATE_FUNCTION_FIELD
				function_fields["type"] = _type
				function_fields["instance"] = instance
			} else {
				state = GC_LIST_STATE_IGNORE_ACTION
				log.Printf("WARN: cannot parse UsbGadgetFunction for `gc -l`, gadget `%s`: type and/or instance is empty for line `%s`\n", gadget_id, line)
			}
		case GC_LIST_STATE_FUNCTION_FIELD:
			key, value := this.parseGcFunctionField(line)
			if key != "" && value != "" {
				function_fields[key] = value
			} else {
				log.Printf("WARN: cannot parse UsbGadgetFunction (%s, %s) field for `gc -l`, gadget `%s`: key and/or value is empty for line `%s`\n", function_fields["type"], function_fields["instance"], gadget_id, line)
			}
		case GC_LIST_STATE_CONFIG:
			if len(function_fields) > 0 {
				function, err := AdaptUsbGadgetFunction(function_fields["type"], function_fields["instance"], function_fields)
				if err != nil {
					errors = append(errors, err)
					break // break switch
				}
				functions[function.getPath()] = function
				clear(function_fields)
			}

			state = GC_LIST_STATE_CONFIG_FIELD
		case GC_LIST_STATE_CONFIG_FIELD:
			// DO NOTHING
		case GC_LIST_STATE_CONFIG_LANGUAGE:
			_, language := this.parseGcGlobalField(line)
			if language != "" {
				state = GC_LIST_STATE_LANGUAGE_FIELD
				result_language = language
			} else {
				log.Printf("WARN: cannot parse config `Language` field for `gc -l`, gadget `%s`: language is empty for line `%s`\n", gadget_id, line)
				state = GC_LIST_STATE_IGNORE_ACTION
			}

			// finish all
			goto finish
		}
	}

finish:
	if result_language != "" {
		return result_language, language_strings[result_language], global_fields, functions, errors
	} else {
		return "", nil, global_fields, functions, errors
	}
}

func (this *UsbGadgetController) updateGadget() []error {
	language, _, state, functions, errors := this.parseGcList(this.gadget.id)
	if len(errors) > 0 {
		return errors
	}

	if language != "" {
		// NOTE: 只记录 language,不要写回 —— gc -l 是 configfs 的只读快照,
		// setLanguageStrings 写回既多余(值本来就来自 configfs),又会在
		// strings 为空时把解析容错得到的垃圾 key 写进不存在的路径
		// (如 strings/0x409/serial → EACCES)。字符串由各函数的 effect()
		// 显式写入。
		this.gadget.setLanguage(language)
	}

	if len(state) == 0 {
		this.gadget.resetState()
	} else {
		this.gadget.overwriteState(state)
	}

	if len(functions) == 0 {
		this.resetFunctions(false)
	} else {
		this.current_functions = functions
		// NOTE: 不要替换 target_functions —— 它是用户目标(daemon 的
		// mode 函数对象,含 ip_addr/ifname 等配置);gc -l 解析出来的是
		// configfs 快照(只有 type/instance),替换后 Apply() 的 enable()
		// 会拿到零值对象(ifname 为空 → 轮询超时,IP/dnsmasq 全部不配)。
		// current_functions 只用于状态展示和 instance 同步。
	}

	return nil
}

func (this *UsbGadgetController) applyGadget() map[string]error {
	return nil

	// errors := make(map[string]error)

	// set := func(subpath UsbGadgetSubpath, value string) {
	// 	err := this.gadget.setAttr(subpath, value)
	// 	if err != nil {
	// 		errors["set_attr_"+string(subpath)] = err
	// 	}
	// }

	// set(USB_GADGET_SUBPATH_VENDOR, this.Config.Vendor)
	// set(USB_GADGET_SUBPATH_PRODUCT, this.Config.Product)
	// set(USB_GADGET_SUBPATH_DEVICE_CLASS, this.Config.DeviceClass)
	// set(USB_GADGET_SUBPATH_DEVICE_SUBCLASS, this.Config.DeviceSubClass)
	// set(USB_GADGET_SUBPATH_DEVICE_PROTOCOL, this.Config.DeviceProtocol)

	// setStrings := func(subpath UsbGadgetSubpathStrings, value string) {
	// 	err := this.gadget.setLanguageStrings(subpath, value)
	// 	if err != nil {
	// 		errors["set_strings_"+string(subpath)] = err
	// 	}
	// }

	// setStrings(USB_GADGET_SUBPATH_STRINGS_MANUFACTURER, this.Config.Manufacturer)
	// setStrings(USB_GADGET_SUBPATH_STRINGS_PRODUCT, this.Config.ProductName)
	// setStrings(USB_GADGET_SUBPATH_STRINGS_SERIALNUMBER, this.Config.Serialnumber)

	// if len(errors) == 0 {
	// 	return nil
	// }

	// return errors
}

func (this *UsbGadgetController) applyFunctions() map[string]error {
	// TODO check diff between target and current
	// if !this.target_functions_changed {
	// 	return nil
	// }

	function_errors := make(map[string]error)

	// clear all first
	var err error
	if this.gadget.init {
		_, err = this.gc("-c")
		if err != nil {
			function_errors["clear_all"] = err
			return function_errors
		}

		// gc -c tears the gadget down asynchronously on this platform
		// (ChipIdea): a create started immediately after can return before
		// the gadget directory is fully removed.  Pressing the button in
		// rapid succession runs applies back-to-back, hitting this every
		// time.  /sbin/mobian-usb-gadget inserts the same settle delay
		// between teardown and setup.
		time.Sleep(1 * time.Second)
	}

	// 手工创建 gadget 骨架(gadget 目录 + strings + configs)。不用 gc -a:
	// 这台设备上的 gc -a 创建函数后立即绑定 UDC,config link 建立即锁定
	// 全部函数属性(dev_addr 写入 EBUSY),MAC 永远写不进去;绑定前的
	// configfs 才是可写的,绑定由 enableGadget 的 echo UDC 完成。
	if err := this.createGadget(); err != nil {
		function_errors["create_gadget"] = err
		return function_errors
	}

	functions := this.target_functions
	function_add_status := make([]bool, len(functions))
	function_effect_status := make([]bool, len(functions))

	safeAdd := func(index int, function UsbGadgetFunction) {
		defer func() {
			if r := recover(); r != nil {
				function_errors["add_panic_"+function.getPath()] = fmt.Errorf("panic caused when adding UsbGadgetFunction %+v: %s", function, r)
			}
		}()

		err := function.add(this.gadget, this.gc)
		if err != nil {
			function_errors["call_add_"+function.getPath()] = err
			return
		}

		function_add_status[index] = true

		// gc -a may have created the gadget directory; mark it as initialized.
		if !this.gadget.init {
			this.gadget.init = true
		}
	}

	safeEffect := func(index int, function UsbGadgetFunction) {
		defer func() {
			if r := recover(); r != nil {
				function_errors["effect_panic_"+function.getPath()] = fmt.Errorf("panic caused when effecting UsbGadgetFunction %+v: %s", function, r)
			}
		}()

		err = function.effect(this.gadget, this.gc)
		if err != nil {
			function_errors["call_effect_"+function.getPath()] = err
			return
		}

		function_effect_status[index] = true
	}

	safeRemove := func(_ int, function UsbGadgetFunction) {
		defer func() {
			if r := recover(); r != nil {
				function_errors["remove_panic_"+function.getPath()] = fmt.Errorf("panic caused when removing (restore effect because of error) UsbGadgetFunction %+v: %s", function, r)
			}
		}()

		err = function.remove(this.gadget, this.gc)
		if err != nil {
			function_errors["call_remove_"+function.getPath()] = err
		}
	}

	// NOTE: 不做 rndis ifname 检查 —— usb0 接口要到 enableGadget 绑定后
	// 才存在,add 阶段检查只会误报。
	index := -1
	for _, function := range functions {
		index++
		if function.getEffected() {
			function_add_status[index] = true
			continue
		}

		safeAdd(index, function)
	}

	// this.current_functions = functions // will update below
	errors := this.updateGadget() // update instance of functions
	if len(errors) > 0 {
		log.Printf("WARN: call `updateGadget` error (maybe parts of it): %v\n", errors)
	}

	// Sync real instances from gc -l back to our function objects.
	// gc -a <type> auto-assigns an instance (e.g. "rndis.1"), but our
	// functions still have the placeholder "tmp::..." instance. Without
	// this sync, effect() writes to the wrong configfs path.
	for _, ourFunc := range functions {
		for _, realFunc := range this.current_functions {
			if realFunc.getType() == ourFunc.getType() {
				ourFunc.setInstance(realFunc.getInstance())
				break
			}
		}
	}

	index = -1
	for _, function := range functions {
		index++
		if !function_add_status[index] {
			safeRemove(index, function)
			continue
		}

		safeEffect(index, function)

		if function_effect_status[index] {
			function.setEffected(true)
		} else {
			safeRemove(index, function)
		}
	}

	// this.target_functions_changed = len(function_errors) > 0

	if len(function_errors) == 0 {
		return nil
	} else {
		return function_errors
	}
}

// createGadget 手工创建 gadget 骨架(configfs 原生 mkdir,不用 gc -a):
// gadget 目录 + strings/<lang> + configs/<config> + config strings,
// 属性值由各函数的 effect() 写入,UDC 绑定由 enableGadget 完成。
// 这台设备上的 gc -a 创建函数后立即绑定 UDC,config link 建立即锁定
// 全部函数属性(dev_addr/host_addr 写入 EBUSY),只有绑定前的 configfs
// 可写 —— 所以创建顺序必须是:mkdir 骨架 → add(写函数属性 + link)→
// effect(写 gadget 属性)→ enableGadget(绑定)。
func (this *UsbGadgetController) createGadget() error {
	basepath := this.gadget.Basepath

	if err := os.MkdirAll(filepath.Join(basepath, "strings", "0x409"), 0755); err != nil {
		return fmt.Errorf("mkdir gadget strings: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(basepath, "configs", "c1.1", "strings", "0x409"), 0755); err != nil {
		return fmt.Errorf("mkdir gadget config strings: %w", err)
	}

	this.gadget.init = true
	return nil
}

// enableGadget 绑定 UDC —— 手工 configfs 创建的 gadget 未绑定,直接
// `echo <udc> > UDC` 完成绑定(不用 gc -e:这台设备上的 gc -a 已经
// 绑过,gc -e 会因重复绑定报 EBUSY;手工流程里 UDC 绑定只有这里
// 一次)。绑定后内核才创建 usb0 接口,host 侧重枚举一次。
func (this *UsbGadgetController) enableGadget() error {
	// Give the USB controller/host time to settle after gc -c tore down the
	// previous gadget (unbind = host sees a disconnect).  Rebinding within
	// milliseconds can leave the host port stuck and the device "not
	// recognized" — the reference script /sbin/mobian-usb-gadget inserts the
	// same sleep 1 between teardown and rebind.
	time.Sleep(1 * time.Second)

	udc, err := findUdc()
	if err != nil {
		return err
	}
	udcPath := filepath.Join(this.gadget.Basepath, "UDC")
	out, err := exec.Command("sh", "-c", "echo '"+udc+"' > '"+udcPath+"'").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bind udc %s: %w, output: %s", udc, err, string(out))
	}
	return nil
}

// findUdc 返回系统唯一的 UDC 控制器名(如 ci_hdrc.0)。
func findUdc() (string, error) {
	entries, err := os.ReadDir("/sys/class/udc")
	if err != nil {
		return "", fmt.Errorf("list /sys/class/udc: %w", err)
	}
	for _, entry := range entries {
		return entry.Name(), nil
	}
	return "", fmt.Errorf("no udc found in /sys/class/udc")
}

func (this *UsbGadgetController) Apply() map[string]error {
	errors := this.applyFunctions()
	if errors != nil {
		return errors
	}

	errors = this.applyGadget()
	if errors != nil {
		return errors
	}

	err := this.enableGadget()
	if err != nil {
		return map[string]error{
			"enable_gadget": err,
		}
	}

	// UDC 已绑定,内核接口(usb0)此时存在 — 让每个函数实现做自己的
	// 运行时管理(如 RNDIS 的 IP + dnsmasq)。
	var enable_errors map[string]error
	for path, function := range this.target_functions {
		if err := function.enable(this.gadget, this.gc); err != nil {
			if enable_errors == nil {
				enable_errors = make(map[string]error)
			}
			enable_errors["call_enable_"+path] = err
		}
	}
	return enable_errors
}

func (this *UsbGadgetController) GetFunctions() map[string]UsbGadgetFunction {
	return this.current_functions
}

/*
replace function which `getType()` matched in `this.target_functions`, the action as same as `AddFunction()` if not found and `replace_only` is not true

also, whenever `getInstance()` is empty, the action as same as `AddFunction()` if `replace_only` is not true
*/
func (this *UsbGadgetController) ReplaceFunction(function UsbGadgetFunction, replace_only bool) error {
	instance := function.getInstance()
	if instance == "" {
		if replace_only {
			return fmt.Errorf("[replace_only] instance is empty")
		} else {
			return this.AddFunction(function)
		}
	}

	target_key := ""
	function_type := function.getType()
	for key, value := range this.target_functions {
		if value.getType() == function_type {
			target_key = key
			break
		}
	}

	if target_key == "" {
		if replace_only {
			return fmt.Errorf("[replace_only] no such type `%s`", function_type)
		} else {
			return this.AddFunction(function)
		}
	}

	if function.getType() == RNDIS_USE_GADGET_FUNCTION {
		// NOTE: don't check rndis ifname here — the network interface doesn't exist
		// until the gadget is enabled. Validation happens at effect time.
	}

	this.target_functions[target_key] = function

	return nil
}

/*
add function to `this.target_functions` or overwrite the function which have the same path
*/
func (this *UsbGadgetController) AddFunction(function UsbGadgetFunction) error {
	// NOTE: don't check rndis ifname here — the network interface doesn't exist
	// until the gadget function is added and enabled via gc -e.

	// Modes share one function object across switch cycles.  The instance is
	// (re)assigned by each concrete add() implementation (fixed name, e.g.
	// "rndis.1") on every apply, and effects are re-run — only flag the
	// function for a fresh add here.
	function.setEffected(false)

	this.target_functions[function.getPath()] = function

	return nil
}

/*
remove function which `getType()` matched in `this.target_functions`
*/
func (this *UsbGadgetController) RemoveFunction(function UsbGadgetFunction) error {
	if function.getInstance() == "" {
		return fmt.Errorf("instance is empty")
	}

	function_type := function.getType()
	if this.target_functions[function_type] != nil {
		delete(this.target_functions, function_type)
	}

	return nil
}

func (this *UsbGadgetController) ClearFunctions() []error {
	// Don't call gc -c here — that would delete the gadget directory.
	// applyFunctions handles gc -c + gc -a together.
	this.resetFunctions(true)
	return nil
}

func NewUsbGadgetController(
	config_fs string,
	gc_path string,
	// config *UsbGadgetControllerConfig,
) (*UsbGadgetController, error) {
	gadget, err := NewUsbGadget(config_fs)
	if err != nil {
		return nil, err
	}

	controller := &UsbGadgetController{gadget: gadget, gc_path: gc_path}
	err = controller.check()
	if err != nil {
		return nil, err
	}
	// if config == nil {
	// 	config = &UsbGadgetControllerConfig{}
	// }
	// controller.Config = config
	controller.resetFunctions(true)

	return controller, nil
}
