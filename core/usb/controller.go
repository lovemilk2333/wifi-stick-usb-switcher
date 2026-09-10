package usb

import (
	"fmt"
	"os"
	"time"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/usb/gadget"
)

// 流程(全部 configfs 操作由 cgo(libusbgx)完成,不再 exec 外部 gc):
// ClearFunctions → AddFunction(目标函数)→ Apply:
// CleanAll(拆旧 gadget)→ CreateGadget(骨架)→ 每个 add(建函数+link)
// → 每个 effect(写 gadget/函数属性)→ Enable(绑 UDC)→ 每个 enable(网络)。

type UsbGadgetFunctionCode uint32

const (
	USB_GADGET_FUNCTION_CODE_NONE  UsbGadgetFunctionCode = 0
	USB_GADGET_FUNCTION_CODE_RNDIS UsbGadgetFunctionCode = 0b1
	USB_GADGET_FUNCTION_CODE_ADB   UsbGadgetFunctionCode = 0b01
)

// UsbGadgetFunctionContext 是函数实现操作 gadget 的入口:configfs 写
// 全部走 cgo(gadget.Ctx),config_fs 仅供只读(如 enable 时读回 ifname
// 的真实接口名)。
type UsbGadgetFunctionContext struct {
	C        *gadget.Ctx
	ConfigFs string
}

type UsbGadgetFunction interface {
	set_effected(effected bool)
	get_effected() bool
	get_path() string
	get_instance() string
	set_instance(instance string)
	get_code() UsbGadgetFunctionCode
	get_type() string
	add(ctx *UsbGadgetFunctionContext) error
	remove(ctx *UsbGadgetFunctionContext) error
	effect(ctx *UsbGadgetFunctionContext) error
	enable(ctx *UsbGadgetFunctionContext) error

	// submode 是主模式内部的子模式,仅内存状态;真正生效需要重新
	// effect(apply_function 重建 gadget 时 effect() 消费它)。
	GetSubmode() int
	SetSubmode(mode int)
	// MaxSubmode 返回 submode 上界(支持 0~MaxSubmode),切换时取模,
	// 防止 +1 无限增长撞上 enable() 只认识有限值
	MaxSubmode() int
	// GetName 返回 gadget 名称(如 `rndis`/`adb`),用于 --gadget 指定
	// 与日志展示
	GetName() string
}

type UsbGadgetFunctionBase struct {
	instance string // like `rndis.1` or `adb`, you can access `<CONFIG_FS>/functions/<this.get_type()>.<instance>`
	_type    string
	code     UsbGadgetFunctionCode
	effected bool
	submode  int
	// submode 上界,构造时设定(RNDIS 1,ADB 默认 0 无 submode)
	max_submode int
}

func (this *UsbGadgetFunctionBase) GetSubmode() int {
	return this.submode
}

func (this *UsbGadgetFunctionBase) SetSubmode(mode int) {
	this.submode = mode
}

func (this *UsbGadgetFunctionBase) MaxSubmode() int {
	return this.max_submode
}

func (this *UsbGadgetFunctionBase) get_type() string {
	return this._type
}

func (this *UsbGadgetFunctionBase) GetName() string {
	return this._type
}

func (this *UsbGadgetFunctionBase) get_instance() string {
	return this.instance
}

func (this *UsbGadgetFunctionBase) set_instance(instance string) {
	this.instance = instance
}

func (this *UsbGadgetFunctionBase) get_code() UsbGadgetFunctionCode {
	return this.code
}

func (this *UsbGadgetFunctionBase) get_path() string {
	return this._type + "." + this.instance
}

func (this *UsbGadgetFunctionBase) get_effected() bool {
	return this.effected
}

func (this *UsbGadgetFunctionBase) set_effected(effected bool) {
	this.effected = effected
}

// add 默认由具体函数实现覆写(创建 configfs 函数 + link);这里保留
// 接口方法的最小实现以防基类被直接使用。
func (this *UsbGadgetFunctionBase) add(ctx *UsbGadgetFunctionContext) error {
	return fmt.Errorf("%s add not implemented", this.get_type())
}

// enable 在 gadget 绑定(Enable)之后调用 — 此时内核接口(如 usb0)才存在。
// 默认无操作;需要运行时管理的实现覆写它。
func (this *UsbGadgetFunctionBase) enable(ctx *UsbGadgetFunctionContext) error {
	return nil
}

// remove 不做实际拆除:失败回滚由下一轮 Apply 的 CleanAll 统一处理
// (整个 gadget 目录删除重建),单函数 remove 没有意义。
func (this *UsbGadgetFunctionBase) remove(ctx *UsbGadgetFunctionContext) error {
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
	ctx *gadget.Ctx

	config_fs string

	current_functions map[string]UsbGadgetFunction
	target_functions  map[string]UsbGadgetFunction
}

func (this *UsbGadgetController) reset_functions(targets bool) {
	this.current_functions = make(map[string]UsbGadgetFunction)
	if targets {
		this.target_functions = make(map[string]UsbGadgetFunction)
	}
}

// UpdateGadget 兼容入口:instance 固定(rndis.1/adb)后不再需要 gc -l
// 回读同步,无操作。
func (this *UsbGadgetController) UpdateGadget() []error {
	return nil
}

func (this *UsbGadgetController) apply_functions() map[string]error {
	// TODO check diff between target and current
	// if !this.target_functions_changed {
	// 	return nil
	// }

	function_errors := make(map[string]error)

	// 每个函数实现访问 gadget 的上下文(写走 cgo,config_fs 供只读)
	fctx := &UsbGadgetFunctionContext{C: this.ctx, ConfigFs: this.config_fs}

	// 拆掉旧 gadget 后重建。拆解(usbg_rm_gadget)在本平台(ChipIdea)是
	// 异步的:立刻重建可能撞上目录未完全删除;快速连按会连续触发 apply,
	// /sbin/mobian-usb-gadget 在 teardown 与 setup 之间插同样延时。
	if err := this.ctx.CleanAll(); err != nil {
		function_errors["clean_all"] = err
		return function_errors
	}
	time.Sleep(1 * time.Second)

	if err := this.ctx.CreateGadget(); err != nil {
		function_errors["create_gadget"] = err
		return function_errors
	}

	functions := this.target_functions
	function_add_status := make([]bool, len(functions))
	function_effect_status := make([]bool, len(functions))

	safeAdd := func(index int, function UsbGadgetFunction) {
		defer func() {
			if r := recover(); r != nil {
				function_errors["add_panic_"+function.get_path()] = fmt.Errorf("panic caused when adding UsbGadgetFunction %+v: %s", function, r)
			}
		}()

		if err := function.add(fctx); err != nil {
			function_errors["call_add_"+function.get_path()] = err
			return
		}

		function_add_status[index] = true
	}

	safeEffect := func(index int, function UsbGadgetFunction) {
		defer func() {
			if r := recover(); r != nil {
				function_errors["effect_panic_"+function.get_path()] = fmt.Errorf("panic caused when effecting UsbGadgetFunction %+v: %s", function, r)
			}
		}()

		if err := function.effect(fctx); err != nil {
			function_errors["call_effect_"+function.get_path()] = err
			return
		}

		function_effect_status[index] = true
	}

	safeRemove := func(_ int, function UsbGadgetFunction) {
		defer func() {
			if r := recover(); r != nil {
				function_errors["remove_panic_"+function.get_path()] = fmt.Errorf("panic caused when removing (restore effect because of error) UsbGadgetFunction %+v: %s", function, r)
			}
		}()

		// remove 是 no-op(下轮 Apply 的 CleanAll 统一拆除);仍保留错误面
		if err := function.remove(fctx); err != nil {
			function_errors["call_remove_"+function.get_path()] = err
		}
	}

	// NOTE: 不做 rndis ifname 检查 —— usb0 接口要到绑定(Enable)后
	// 才存在,add 阶段检查只会误报。
	index := -1
	for _, function := range functions {
		index++
		if function.get_effected() {
			function_add_status[index] = true
			continue
		}

		safeAdd(index, function)
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
			function.set_effected(true)
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

func (this *UsbGadgetController) enable_gadget() error {
	// 拆旧后给 USB 控制器/host 稳定时间(unbind = host 看到断开),立刻
	// 重绑可能让 host 端口卡死"无法识别";mobian-usb-gadget 同样延时。
	time.Sleep(1 * time.Second)

	udc, err := find_udc()
	if err != nil {
		return err
	}
	return this.ctx.Enable(udc)
}

// find_udc 返回系统唯一的 UDC 控制器名(如 ci_hdrc.0)。
func find_udc() (string, error) {
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
	errors := this.apply_functions()
	if errors != nil {
		return errors
	}

	err := this.enable_gadget()
	if err != nil {
		return map[string]error{
			"enable_gadget": err,
		}
	}

	// UDC 已绑定,内核接口(usb0)此时存在 — 让每个函数实现做自己的
	// 运行时管理(如 RNDIS 的 IP + dnsmasq)。
	fctx := &UsbGadgetFunctionContext{C: this.ctx, ConfigFs: this.config_fs}
	var enable_errors map[string]error
	for path, function := range this.target_functions {
		if err := function.enable(fctx); err != nil {
			if enable_errors == nil {
				enable_errors = make(map[string]error)
			}
			enable_errors["call_enable_"+path] = err
		}
	}
	return enable_errors
}

// ReconfigureFunction 不重建 gadget,直接在当前接口上重新执行函数的
// enable 配置(如 RNDIS submode 切换)。重建 gadget 会断开 USB 对端的
// RNDIS 网卡(Windows 侧重新枚举,ICS 需重新就绪,DHCP 探测才老失败),
// submode 切换时函数不变,网卡连接保持,只需重配网络。
func (this *UsbGadgetController) ReconfigureFunction(function UsbGadgetFunction) error {
	fctx := &UsbGadgetFunctionContext{C: this.ctx, ConfigFs: this.config_fs}
	return function.enable(fctx)
}

/*
add function to `this.target_functions` or overwrite the function which have the same path
*/
func (this *UsbGadgetController) AddFunction(function UsbGadgetFunction) error {
	// NOTE: don't check rndis ifname here — the network interface doesn't exist
	// until the gadget function is added and enabled.

	// Modes share one function object across switch cycles.  The instance is
	// (re)assigned by each concrete add() implementation (fixed name, e.g.
	// "rndis.1") on every apply, and effects are re-run — only flag the
	// function for a fresh add here.
	function.set_effected(false)

	this.target_functions[function.get_path()] = function

	return nil
}

func (this *UsbGadgetController) ClearFunctions() []error {
	// 不在此拆 gadget(CleanAll 由 Apply 统一处理),只清目标函数集合
	this.reset_functions(true)
	return nil
}

func NewUsbGadgetController(config_fs string) (*UsbGadgetController, error) {
	ctx, err := gadget.New(config_fs)
	if err != nil {
		return nil, err
	}

	controller := &UsbGadgetController{
		ctx:       ctx,
		config_fs: config_fs,
	}
	controller.reset_functions(true)

	return controller, nil
}
