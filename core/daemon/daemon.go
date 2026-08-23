package daemon

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"time"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/input"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/led"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/usb"
)

// TODO
// https://github.com/james-barrow/golang-ipc

type DaemonCmd struct {
	Devnode              string           `arg:"-d,required" help:"button devnode path"`
	LongTapImmediately   bool             `arg:"--long-tap-immediately" default:"true" help:"emit long-tap when pressing time >= LongTapThreshold, even button is still pressing"`
	LongTapThreshold     time.Duration    `arg:"--long-tap-threshold" default:"500ms" help:"the threshold of long-tap, such as 500ms, 1s"`
	MultipleTapThreshold time.Duration    `arg:"--multiple-tap-threshold" default:"500ms" help:"the threshold of multiple-tap, lower than zero means disable, such as 500ms, 1s"`
	AutoConfirmThreshold time.Duration    `arg:"--auto-confirm-threshold" default:"5s" help:"the threshold that auto confirm mode switch"`
	Leds                 []string         `arg:"-l,--led,separate" help:"led path, such as /sys/class/leds/blue:wifi"`
	LedBlinkDuration     time.Duration    `arg:"--led-blink-duration" help:"led blink duration, the light duration of led when blinking" default:"100ms"`
	LedBlinkInterval     time.Duration    `arg:"--led-blink-interval" help:"led blink interval, the dark duration of led when blinking" default:"300ms"`
	SubmodeLedDuration   time.Duration    `arg:"--submode-led-duration" help:"led off duration when entering submode selection, before showing the submode state" default:"750ms"`
	UsbConfigFs          string           `arg:"-c,--config-fs" default:"/sys/kernel/config/usb_gadget/g1" help:"usb config-fs path, such as /sys/kernel/config/usb_gadget/g1"`
	GcPath               string           `arg:"-g,--gc-path" default:"gc" help:"gadget controller (https://github.com/HandsomeMod/gc) path or ELF name which can be found in $PATH"`
	RndisDeviceMac       net.HardwareAddr `arg:"--rndis-device-mac" default:"02:12:34:56:78:9a" help:"the mac address of current device rndis network interface"`
	RndisHostMac         net.HardwareAddr `arg:"--rndis-host-mac" default:"02:98:76:54:32:10" help:"the network interface mac address of the device which connected to rndis can see"`
	RndisIP              string           `arg:"-a,--rndis-ip" default:"10.22.33.1/24" help:"the IP address of rndis network interface, you need provide a valid IP address and a prefix of network like 10.0.0.100/24"`
	RndisClientIP        string           `arg:"--rndis-client-ip" default:"0.0.0.33" help:"the client IP template (x.x.x.x, zero bytes take the upstream subnet bytes) of the stick in RNDIS client submode, e.g. 0.0.22.33"`
	RndisClientTimeout   time.Duration    `arg:"--rndis-client-timeout" default:"5s" help:"the total timeout of the RNDIS client submode, including waiting for the network interface and DHCP probing, such as 5s, 30s"`
	RndisUsbIfname       string           `arg:"-i,--rndis-ifname" default:"usb0" help:"usb ifname name to config RNDIS, you can use \"ip link\" to find the ifname name, such as usb0"`
	RndisSerialNumber    string           `arg:"--rndis-serial-number" default:"wifi-stick-miruku" help:"the serial number string of the rndis usb gadget device"`
	RndisManufacturer    string           `arg:"--rndis-manufacturer" default:"wifi-stick" help:"the manufacturer string of the rndis usb gadget device"`
	RndisProduct         string           `arg:"--rndis-product" default:"RNDIS Ethernet" help:"the product string of the rndis usb gadget device"`
	AdbSerialNumber      string           `arg:"--adb-serial-number" default:"wifi-stick-miruku" help:"the serial number string of the adb usb gadget device"`
	AdbManufacturer      string           `arg:"--adb-manufacturer" default:"Google" help:"the manufacturer string of the adb usb gadget device"`
	AdbProduct           string           `arg:"--adb-product" default:"ADB Gadget" help:"the product string of the adb usb gadget device"`
	AdbEnv               []string         `arg:"--adb-env,separate" help:"extra environment variables (KEY=VALUE) passed to the adbd process, repeatable, e.g. --adb-env=TERM=xterm-256color; default TERM=xterm-256color"`
	DnsmasqArgs          []string         `arg:"--dnsmasq-arg,separate" help:"extra dnsmasq argument for the RNDIS DHCP server, repeatable; use the = form, e.g. --dnsmasq-arg=--addn-hosts=/etc/wifi-stick/hosts (a space-separated value starting with -- would be parsed as a flag); can override scalar defaults like --port=53"`
	IPCAllowOtherUser    bool             `arg:"--ipc-share, --ipc-allow-other-user" default:"false" help:"allow other user to access IPC (UnmaskPermissions)"`
	TickRate             time.Duration    `arg:"--tick-rate" default:"50ms" help:"daemon event loop tick rate"`
}

type Daemon struct {
	base.PathChecker

	input_device     *input.InputDevice
	controller       *usb.UsbGadgetController
	interpreters     []*led.LedInterpreter
	modes            []usb.UsbGadgetFunction
	current_mode     int
	mode_changed     bool
	mode_changing    bool
	// submode 选择中的切换(submode_changed):函数不变,applyFunction
	// 不重建 gadget,直接在当前接口上重配网络 —— 重建会断开对端 RNDIS
	// 网卡(Windows 侧重新枚举,ICS 需重新就绪,DHCP 探测必失败)。
	submode_changed bool
	turn_off_leds    bool
	tick_rate        time.Duration
	daemonipc        *daemonipc.IPCFramework
	daemonipc_config *ipc.ServerConfig

	// submode selection state:长按进入选择模式后,短按切换 submode,
	// 再长按退出。进入/退出选择时 LED 先关闭 submode_led_duration
	// (submode_entry_mode)作为提示,loop_count 判断关闭期结束后显示
	// submode 状态;effect 期间:模式切换快闪 LED_MODE_BLINK,
	// 子模式切换关闭 LED。
	// submode_entry_done 初始为 true:启动时(未进入/退出选择)不触发
	// 关闭期检查,否则慢闪模式的 loop_count 增长会被误判为"关闭期
	// 结束"而额外 SetMode 一次(闪烁相位跳变)。
	submode_selection  bool
	submode_entry_done bool
	submode_entry_mode *led.LedMode
}

// LED_SUBMODE_MODES 是 submode 对应的 LED 显示模式,index = submode % 2:
// 0 = 常亮,1 = 慢闪(1Hz,500ms on / 500ms off,暂固定周期)。
var LED_SUBMODE_MODES = []*led.LedMode{
	led.MODE_PRESET_ON,
	led.NewLedMode().OnDuration(500 * time.Millisecond).Wait(500 * time.Millisecond).Done(),
}

func NewDaemon(cmd *DaemonCmd) (*Daemon, error) {
	daemon := &Daemon{}
	daemon.tick_rate = cmd.TickRate
	if err := daemon.init(cmd); err != nil {
		return nil, err
	}
	return daemon, nil
}

func (this *Daemon) GetTurnOffLeds() bool {
	return this.turn_off_leds
}

func (this *Daemon) SetTurnOffLeds(off bool) {
	this.turn_off_leds = off
}

// Mainloop runs the daemon event loop at the configured tick rate.
func (this *Daemon) Mainloop() error {
	// TODO impl IPC

	// ipc_server, err := ipc.StartServer(base.PROJECT_IDENT, this.daemonipc_config)
	// if err != nil {
	// 	return err
	// }

	// err = this.daemonipc.Start(ipc_server)
	// if err != nil {
	// 	return err
	// }

	log.Printf("INFO daemon LED init\n")
	for _, interpreter := range this.interpreters {
		interpreter.SetMode(led.MODE_PRESET_ON)
		interpreter.Tick()
		time.Sleep(time.Millisecond * 500)
		interpreter.SetMode(led.MODE_PRESET_OFF)
	}

	ticker := time.NewTicker(this.tick_rate)
	defer ticker.Stop()

	go this.applyFunction()

	log.Printf("INFO daemon started\n")
	for range ticker.C {
		this.Tick()
	}

	return nil
}

var LED_MODE_BLINK *led.LedMode

// init validates cmd and stores all initialised handles on the Daemon struct.
func (this *Daemon) init(cmd *DaemonCmd) error {
	// ---- validate arguments ------------------------------------------------

	if this.IsValidPath(cmd.Devnode, "/dev/input/", true, true) != base.PATH_STATUS_OK {
		return fmt.Errorf("`%s` is not a valid input device", cmd.Devnode)
	}

	if !this.isValidConfigFs(cmd.UsbConfigFs) {
		return fmt.Errorf("`%s` is not a valid config fs", cmd.UsbConfigFs)
	}

	if cmd.Leds != nil {
		for _, ledDevnode := range cmd.Leds {
			if this.IsValidPath(ledDevnode, "/sys/class/leds/", false, true) != base.PATH_STATUS_OK {
				return fmt.Errorf("`%s` is not a valid led device", ledDevnode)
			}
		}
	}

	rndisIP, err := netip.ParsePrefix(cmd.RndisIP)
	if err != nil {
		return fmt.Errorf("`%s` is not a valid IP address", cmd.RndisIP)
	}

	rndisClientIP, err := netip.ParseAddr(cmd.RndisClientIP)
	if err != nil || !rndisClientIP.Is4() {
		return fmt.Errorf("`%s` is not a valid IPv4 address", cmd.RndisClientIP)
	}

	if cmd.RndisClientTimeout <= 0 {
		return fmt.Errorf("`--rndis-client-timeout` must be positive")
	}

	// 初始 true:见 struct 注释 —— 启动时不要误触发关闭期检查
	this.submode_entry_done = true

	// go-arg 的 slice 默认值分隔行为不可靠,默认环境变量在这里补:
	// adb shell 需要正确的终端类型
	if len(cmd.AdbEnv) == 0 {
		cmd.AdbEnv = []string{"TERM=xterm-256color"}
	}

	LED_MODE_BLINK = led.NewLedMode().OnDuration(cmd.LedBlinkDuration).Wait(cmd.LedBlinkInterval).Done()

	// 进入/退出子模式选择时的过渡模式:LED 关闭 submode_led_duration 时间
	// 作为提示,之后(loop_count >= 1,即 Off 已执行第二次)daemon 切换到
	// submode 状态 LED。不用 OffDuration():其尾缀 On 会在关闭期结束后
	// 先亮一个 tick、下一轮 Off 又灭,daemon 切 submode LED 时再亮 ——
	// 表现为"闪两下"。Off().Wait(d) 让关闭期结束后直接切 submode 状态。
	this.submode_entry_mode = led.NewLedMode().Off().Wait(cmd.SubmodeLedDuration).Done()

	// ---- initialise input device ------------------------------------------

	inputDevice, err := input.NewDevice(cmd.Devnode, &input.InputDeviceConfig{
		LongTapThreshold:     cmd.LongTapThreshold,
		MultipleTapThreshold: cmd.MultipleTapThreshold,
		LongTapImmediately:   cmd.LongTapImmediately,
	})
	if err != nil {
		return fmt.Errorf("cannot create input device: %w", err)
	}

	status, err := inputDevice.Open()
	if status != input.DEVICE_STATUS_NORMAL {
		return fmt.Errorf("cannot open input device (%d): %w", status, err)
	}

	inputDevice.StartDaemon()
	this.input_device = inputDevice

	// ---- initialise USB gadget controller ---------------------------------

	controller, err := usb.NewUsbGadgetController(cmd.UsbConfigFs, cmd.GcPath)
	if err != nil {
		return fmt.Errorf("cannot init usb gadget: %w", err)
	}

	if errs := controller.ClearFunctions(); errs != nil {
		return fmt.Errorf("cannot clear usb gadget functions: %v", errs)
	}

	this.controller = controller

	// ---- prepare modes ----------------------------------------------------

	this.modes = []usb.UsbGadgetFunction{
		usb.NewUsbGadgetRndis(rndisIP, base.PROJECT_IDENT+"_", cmd.RndisDeviceMac.String(), cmd.RndisHostMac.String(), cmd.RndisUsbIfname, "", cmd.DnsmasqArgs, rndisClientIP, cmd.RndisClientTimeout, cmd.RndisSerialNumber, cmd.RndisManufacturer, cmd.RndisProduct),
		usb.NewUsbGadgetAdb("/dev/usb-ffs/adb", cmd.AdbSerialNumber, cmd.AdbManufacturer, cmd.AdbProduct, cmd.AdbEnv),
	}

	// ---- initialise LEDs --------------------------------------------------

	this.interpreters = loadLedInterpreters(cmd.Leds)

	// ---- init ipc
	// TODO
	// this.daemonipc = daemonipc.InitServer(this)
	// this.daemonipc_config = &ipc.ServerConfig{
	// 	UnmaskPermissions: cmd.IPCAllowOtherUser,
	// }

	return nil
}

func (this *Daemon) applyFunction() {
	this.mode_changing = true

	// submode 切换:函数不变,不重建 gadget(重建会断开对端 RNDIS 网卡,
	// ICS 需重新就绪,第一次探测必然失败)—— 直接在当前接口上重配网络
	if this.submode_changed {
		this.submode_changed = false
		if interpreter := this.currentInterpreter(); interpreter != nil {
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}
		if err := this.controller.ReconfigureFunction(this.modes[this.current_mode]); err != nil {
			log.Printf("WARN: cannot reconfigure function: %v\n", err)
		}
		if !this.turn_off_leds {
			if interpreter := this.currentInterpreter(); interpreter != nil {
				interpreter.SetMode(this.submodeLedMode())
			}
		} else {
			if interpreter := this.currentInterpreter(); interpreter != nil {
				interpreter.SetMode(led.MODE_PRESET_OFF)
			}
		}
		this.mode_changing = false
		return
	}

	// effect 期间:模式切换用快闪,子模式切换(选择状态中短按)用关闭 LED
	if this.submode_selection {
		if interpreter := this.currentInterpreter(); interpreter != nil {
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}
	} else {
		if interpreter := this.currentInterpreter(); interpreter != nil {
			interpreter.SetMode(LED_MODE_BLINK)
		}
	}

	if errs := this.controller.ClearFunctions(); errs != nil {
		log.Printf("WARN: cannot clear functions: %v\n", errs)
	}

	if err := this.controller.AddFunction(this.modes[this.current_mode]); err != nil {
		log.Printf("WARN: cannot add function: %v\n", err)
	}

	if errs := this.controller.Apply(); errs != nil {
		log.Printf("WARN: cannot apply functions: %v\n", errs)
	}

	if errs := this.controller.UpdateGadget(); errs != nil {
		log.Printf("WARN: cannot update gadget: %v\n", errs)
	}

	// 完成后按 submode 显示 LED:0 常亮 / 1 慢闪(submode % 2 取 index)
	if !this.turn_off_leds {
		if interpreter := this.currentInterpreter(); interpreter != nil {
			interpreter.SetMode(this.submodeLedMode())
		}
	} else {
		if interpreter := this.currentInterpreter(); interpreter != nil {
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}
	}

	this.mode_changing = false
}

// submodeLedMode 返回当前主模式 submode 对应的 LED 模式。
// submode 超出 1 用 % 2 取 index(0 -> 常亮,1 -> 慢闪)。
func (this *Daemon) submodeLedMode() *led.LedMode {
	submode := this.modes[this.current_mode].GetSubmode()
	index := submode % 2
	if index < 0 { // SetSubmode 只递增,防御负数
		index += 2
	}
	return LED_SUBMODE_MODES[index]
}

// currentInterpreter 返回当前主模式对应的 LED interpreter。
// 可能为 nil:LED 数量少于主模式数量,或该 LED 初始化失败。
func (this *Daemon) currentInterpreter() *led.LedInterpreter {
	if this.current_mode < 0 || this.current_mode >= len(this.interpreters) {
		return nil
	}
	return this.interpreters[this.current_mode]
}

func (this *Daemon) Tick() {
	for _, event := range this.input_device.Tick() {
		log.Printf("%+v\n", event)

		if event.Status != input.DEVICE_STATUS_NORMAL {
			log.Fatalf("FATAL: %s\n", event.Error.Error())
			continue
		}

		switch event.Type {
		case input.INPUT_TAP:
			if this.submode_selection {
				// 子模式选择状态:短按切换 submode(0→1→2...,LED 用 % 2 显示)
				mode := this.modes[this.current_mode]
				mode.SetSubmode(mode.GetSubmode() + 1)
				this.mode_changed = true
				this.submode_changed = true
			} else {
				this.current_mode++
				this.current_mode %= len(this.modes)
				this.mode_changed = true
				this.submode_changed = false
			}
		case input.INPUT_LONG_TAP:
			// 进入/退出子模式选择:LED 先关闭 submode_led_duration 作为提示,
			// 关闭期结束后(loop_count >= 1)显示 submode 状态
			this.submode_selection = !this.submode_selection
			this.submode_entry_done = false
			if interpreter := this.currentInterpreter(); interpreter != nil {
				interpreter.SetMode(this.submode_entry_mode)
			}
		case input.INPUT_MULTIPLE_TAP:
			// TODO
		case input.INPUT_ERROR:
			// TODO WARNING
		}
	}

	// 进入/退出子模式选择的 LED 关闭期结束后显示 submode 状态。
	// loop_count >= 1 表示 submode_entry_mode(Off→Wait(duration)→On)
	// 已完成一轮,即关闭了 submode_led_duration 时间。
	if !this.submode_entry_done {
		if interpreter := this.currentInterpreter(); interpreter != nil && interpreter.GetLoopCount() >= 1 {
			this.submode_entry_done = true
			interpreter.SetMode(this.submodeLedMode())
		}
	}

	if this.mode_changed && !this.mode_changing {
		this.mode_changed = false
		this.mode_changing = true

		for _, interpreter := range this.interpreters {
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}

		go this.applyFunction()
	}

	for _, interpreter := range this.interpreters {
		interpreter.Tick()
	}
}

func loadLedInterpreters(ledDevnodes []string) []*led.LedInterpreter {
	interpreters := make([]*led.LedInterpreter, len(ledDevnodes))

	for index, ledDevnode := range ledDevnodes {
		ledDevice, err := led.NewLed(ledDevnode)
		if err != nil {
			log.Printf("WARN: cannot create Led device for node `%s`: %s\n", ledDevnode, err)
			continue
		}
		interpreter := led.NewLedInterpreter(ledDevice)
		err = interpreter.SetMode(led.MODE_PRESET_OFF)
		if err != nil {
			log.Printf("WARN: cannot init LedInterpreter for node `%s`: %s\n", ledDevnode, err)
			continue
		}

		interpreters[index] = interpreter
	}

	return interpreters
}

func (this *Daemon) isValidConfigFs(path string) bool {
	status := this.IsValidPath(path, "/sys/kernel/config/usb_gadget/", false, true)
	if status == base.PATH_ERROR_NOT_EXISTS {
		return true // gc -a will create the gadget directory
	}
	return status == base.PATH_STATUS_OK
}
