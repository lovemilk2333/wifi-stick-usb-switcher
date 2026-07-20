package usb

import (
	"debug/elf"
	"fmt"
	"log"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
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

// func (this *UsbGadgetController) rndisCheckIfname(function UsbGadgetFunction) error {
// 	function_type := function.getType()
// 	if function_type != RNDIS_USE_GADGET_FUNCTION {
// 		return fmt.Errorf("invalid UsbGadgetFunction: expect `rndis`, got `%s`", function_type)
// 	}

// 	function_path := function.getPath()
// 	if strings.ContainsRune(function_path, filepath.Separator) {
// 		return fmt.Errorf("invalid UsbGadgetFunction path  `%s`: %c is not valid filename character", function_path, filepath.Separator)
// 	}

// 	subpath := filepath.Join("functions/", function_path, "ifname")

// 	ifname_binary, err := this.gadget.ReadSubpath(base.Subpath(subpath), true)
// 	if err != nil {
// 		return err
// 	}

// 	ifname := strings.TrimSpace(string(ifname_binary))
// 	if ifname == "" {
// 		return fmt.Errorf("cannot get UsbGadgetFunction rndis ifname: file is empty: `%s`", subpath)
// 	}

// 	_, err = net.InterfaceByName(ifname)
// 	if err != nil {
// 		return fmt.Errorf("cannot access UsbGadgetFunction rndis ifname %s: %w", ifname, err)
// 	}

// 	return nil
// }

func (this *UsbGadgetController) rndisCheckIfname(function UsbGadgetFunction) error {
	function_type := function.getType()
	if function_type != RNDIS_USE_GADGET_FUNCTION {
		return fmt.Errorf("invalid UsbGadgetFunction: expect `rndis`, got `%s`", function_type)
	}

	rndis, ok := function.(*UsbGadgetRndis)
	if !ok {
		return fmt.Errorf("cannot convert UsbGadgetFunction to UsbGadgetRndis")
	}

	ifname := rndis.ifname
	if ifname == "" {
		return fmt.Errorf("got an empty UsbGadgetFunction rndis ifname")
	}

	_, err := net.InterfaceByName(ifname)
	if err != nil {
		return fmt.Errorf("cannot access UsbGadgetFunction rndis ifname %s: %w", ifname, err)
	}

	return nil
}

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
	line = strings.Replace(line, ":", "", 1)

	idx := strings.LastIndexFunc(line, unicode.IsSpace)
	if idx == -1 {
		return "", ""
	}

	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx:])

	return key, value
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
		log.Printf("DEBUG: usb gadget not created\n")
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
	language, language_fields, state, functions, errors := this.parseGcList(this.gadget.id)
	if len(errors) > 0 {
		return errors
	}

	if language != "" {
		this.gadget.setLanguage(language)
		for key, value := range language_fields {
			this.gadget.setLanguageStrings(UsbGadgetSubpathStrings(key), value)
		}
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
		this.target_functions = functions
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
	_, err := this.gc("-c")
	if err != nil {
		function_errors["clear_all"] = err
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

	index := -1
	for path, function := range functions {
		index++
		if function.getEffected() {
			function_add_status[index] = true
			continue
		}

		if function_add_status[index] && function.getType() == RNDIS_USE_GADGET_FUNCTION {
			err := this.rndisCheckIfname(function)
			if err != nil {
				function_errors[path] = err
				function_add_status[index] = false
				continue
			}
		}

		safeAdd(index, function)
	}

	// this.current_functions = functions // will update below
	errors := this.updateGadget() // update instance of functions
	if len(errors) > 0 {
		log.Printf("WARN: call `updateGadget` error (maybe parts of it): %v\n", errors)
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

func (this *UsbGadgetController) enableGadget() error {
	_, err := this.gc("-e")
	if err != nil {
		return err
	}

	return nil
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

	return nil
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
		err := this.rndisCheckIfname(function)
		if err != nil {
			return err
		}
	}

	this.target_functions[target_key] = function

	return nil
}

/*
add function to `this.target_functions` or overwrite the function which have the same path
*/
func (this *UsbGadgetController) AddFunction(function UsbGadgetFunction) error {
	if function.getType() == RNDIS_USE_GADGET_FUNCTION {
		err := this.rndisCheckIfname(function)
		if err != nil {
			return err
		}
	}

	if function.getInstance() == "" {
		function.setInstance("tmp::" + time.Now().UTC().Format(time.RFC3339))
	}

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
