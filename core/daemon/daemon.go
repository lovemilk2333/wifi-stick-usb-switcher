package daemon

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
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
	RndisDeviceMac       net.HardwareAddr `arg:"--rndis-device-mac" default:"02:12:34:56:78:9a" help:"the mac address of current device rndis network interface"`
	RndisHostMac         net.HardwareAddr `arg:"--rndis-host-mac" default:"02:98:76:54:32:10" help:"the network interface mac address of the device which connected to rndis can see"`
	RndisIP              string           `arg:"-a,--rndis-ip" default:"10.22.33.1/24" help:"the IP address of rndis network interface, you need provide a valid IP address and a prefix of network like 10.0.0.100/24"`
	RndisClientIP        string           `arg:"--rndis-client-ip" default:"0.0.0.33" help:"the client IP of the stick in RNDIS client submode: template (x.x.x.x, zero bytes take the upstream subnet bytes, e.g. 0.0.22.33) for DHCP leases; its last byte is used for the static ICS probe (192.168.137.x)"`
	RndisClientTimeout   time.Duration    `arg:"--rndis-client-timeout" default:"5s" help:"the total timeout of the RNDIS client submode, including waiting for the network interface and DHCP probing, such as 5s, 30s"`
	RndisUsbIfname       string           `arg:"-i,--rndis-ifname" default:"usb0" help:"usb ifname name to create for RNDIS"`
	RndisQmult           uint             `arg:"--rndis-qmult" default:"8" help:"usb ifname qmult (queue length multiplier) config for RNDIS"`
	RndisSerialNumber    string           `arg:"--rndis-serial-number" default:"wifi-stick-miruku" help:"the serial number string of the rndis usb gadget device"`
	RndisManufacturer    string           `arg:"--rndis-manufacturer" default:"wifi-stick" help:"the manufacturer string of the rndis usb gadget device"`
	RndisProduct         string           `arg:"--rndis-product" default:"RNDIS Ethernet" help:"the product string of the rndis usb gadget device"`
	AdbSerialNumber      string           `arg:"--adb-serial-number" default:"wifi-stick-miruku" help:"the serial number string of the adb usb gadget device"`
	AdbManufacturer      string           `arg:"--adb-manufacturer" default:"Google" help:"the manufacturer string of the adb usb gadget device"`
	AdbProduct           string           `arg:"--adb-product" default:"ADB Gadget" help:"the product string of the adb usb gadget device"`
	AdbEnv               []string         `arg:"--adb-env,separate" help:"extra environment variables (KEY=VALUE) passed to the adbd process, repeatable, e.g. --adb-env=TERM=xterm-256color; default TERM=xterm-256color"`
	DnsmasqArgs          []string         `arg:"--dnsmasq-arg,separate" help:"extra dnsmasq argument for the RNDIS DHCP server, repeatable; use the = form, e.g. --dnsmasq-arg=--addn-hosts=/etc/wifi-stick/hosts (a space-separated value starting with -- would be parsed as a flag); can override scalar defaults like --port=53"`
	IPCAllowOtherUser    bool             `arg:"--ipc-share,--ipc-allow-other-user" default:"false" help:"allow other user to access IPC (UnmaskPermissions)"`
	TickInterval         time.Duration    `arg:"--tick-rate,--tick-interval" default:"50ms" help:"daemon event loop tick interval"`
	ShutdownThreshold    time.Duration    `arg:"--shutdown-threshold" default:"5s" help:"long-press shutdown threshold, must be greater than --long-tap-threshold; 0 disables"`
	ShutdownCommand      string           `arg:"--shutdown-command" default:"poweroff" help:"command run when long-press shutdown triggers"`
	ShutdownShell        string           `arg:"--shutdown-shell" default:"" help:"shell used to run --shutdown-command"`
	ShutdownTimeout      time.Duration    `arg:"--shutdown-timeout" default:"30s" help:"run --shutdown-command timeout"`
}

type Daemon struct {
	base.PathChecker

	input_device  *input.InputDevice
	controller    *usb.UsbGadgetController
	interpreters  []*led.LedInterpreter
	modes         []usb.UsbGadgetFunction
	current_mode  int
	mode_changed  bool
	mode_changing bool
	// submode 选择中的切换(submode_changed):函数不变,apply_function
	// 不重建 gadget,直接在当前接口上重配网络 —— 重建会断开对端 RNDIS
	// 网卡(Windows 侧重新枚举,ICS 需重新就绪,DHCP 探测必失败)。
	submode_changed  bool
	turn_off_leds    atomic.Bool // IPC goroutine 写、apply_function goroutine 读,需原子
	tick_interval    time.Duration
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

	// 长按关机状态(均由 mainloop 的 Tick 单 goroutine 读写,无需锁):
	// 按住时长 >= shutdown_threshold 后立即执行关机命令,不等松开 ——
	// 默认按键是 KEY_RESTART,松开事件会触发系统级重启,poweroff
	// 必须在按住期间调用,松开发生在关机过程中;
	// shutting_down = 已进入关机流程,不再处理任何事件。
	shutdown_threshold time.Duration
	shutdown_command   string
	shutdown_shell     string
	shutdown_timeout   time.Duration
	shutting_down      bool

	// stop_chan 由 Stop()(SIGTERM/SIGINT)关闭,Mainloop select 退出,
	// defer 统一清理运行时副作用 —— systemctl stop 不再强杀留残留
	stop_chan chan struct{}
}

// Stop 请求 daemon 优雅退出(幂等)。
func (this *Daemon) Stop() {
	select {
	case <-this.stop_chan:
	default:
		close(this.stop_chan)
	}
}

// LED_SUBMODE_MODES 是 submode 对应的 LED 显示模式,index = submode % 2:
// 0 = 常亮,1 = 慢闪(1Hz,500ms on / 500ms off,暂固定周期)。
var LED_SUBMODE_MODES = []*led.LedMode{
	led.MODE_PRESET_ON,
	led.NewLedMode().OnDuration(500 * time.Millisecond).Wait(500 * time.Millisecond).Done(),
}

func NewDaemon(cmd *DaemonCmd) (*Daemon, error) {
	daemon := &Daemon{
		stop_chan: make(chan struct{}),
	}

	if err := daemon.init(cmd); err != nil {
		return nil, err
	}
	return daemon, nil
}

func (this *Daemon) GetTurnOffLeds() bool {
	return this.turn_off_leds.Load()
}

// SimulateButton injects a synthetic button action, reusing the physical-button
// path: tap/long push an event into the input queue (processed by Tick on the
// mainloop, no shared-state race); shutdown simulates a held button long enough
// for the mainloop's shutdown check to fire do_shutdown; multi injects `count`
// taps that the input chain-merge turns into a single INPUT_MULTIPLE_TAP.
func (this *Daemon) SimulateButton(target daemonipc.SimulateButtonTarget, count int) error {
	now := time.Now()
	switch target {
	case daemonipc.SIMULATE_BUTTON_TAP:
		this.input_device.InjectEvent(&input.InputEvent{
			Type:     input.INPUT_TAP,
			Time:     now.Add(-(this.input_device.Config.MultipleTapThreshold + time.Millisecond)),
			TapCount: 1,
			Status:   input.DEVICE_STATUS_NORMAL,
		})
	case daemonipc.SIMULATE_BUTTON_LONG:
		this.input_device.InjectEvent(&input.InputEvent{
			Type:     input.INPUT_LONG_TAP,
			Time:     now.Add(-(this.input_device.Config.MultipleTapThreshold + time.Millisecond)),
			TapCount: 1,
			Status:   input.DEVICE_STATUS_NORMAL,
		})
	case daemonipc.SIMULATE_BUTTON_SHUTDOWN:
		d := this.shutdown_threshold
		if d <= 0 {
			d = time.Second // shutdown disabled: still simulate a long press
		}
		this.input_device.InjectPress(d + time.Second)
	case daemonipc.SIMULATE_BUTTON_MULTI:
		if count < 2 {
			return fmt.Errorf("multi-tap count must be >= 2")
		}
		max := int(this.input_device.Config.MultipleTapMaxCount)
		if max > 0 && count > max {
			return fmt.Errorf("multi-tap count %d exceeds maximum %d", count, max)
		}
		for i := 0; i < count; i++ {
			this.input_device.InjectEvent(&input.InputEvent{
				Type:     input.INPUT_TAP,
				Time:     now,
				TapCount: 1,
				Status:   input.DEVICE_STATUS_NORMAL,
			})
		}
	}
	return nil
}

func (this *Daemon) SetTurnOffLeds(off bool) {
	this.turn_off_leds.Store(off)
	this.apply_led_state() // IPC 线程改完立即生效,不等下一次模式切换
}

// apply_led_state 按 turn_off_leds 立即设置当前 LED:关 / 显示 submode 状态。
// 由 IPC handler(goroutine)调用,与 Tick/apply_function 的并发是既有模型。
func (this *Daemon) apply_led_state() {
	if interpreter := this.current_interpreter(); interpreter != nil {
		if this.turn_off_leds.Load() {
			interpreter.SetMode(led.MODE_PRESET_OFF)
			interpreter.Tick()
		} else {
			interpreter.SetMode(this.submode_led_mode())
			interpreter.Tick()
		}
	}
}

// Mainloop runs the daemon event loop at the configured tick rate.
func (this *Daemon) Mainloop() error {
	// 退出时清理本 daemon 启动的运行时副作用(adbd/dnsmasq/functionfs
	// 挂载),否则 systemctl stop 强杀会残留,下次启动撞上(旧 adbd 占
	// ep0、叠加挂载 → ADB 绑定失败)
	defer usb.CleanupRuntime()

	// IPC server 由独立 goroutine 托管(挂了自动重建),不阻塞主循环
	go this.run_ipc_server()

	log.Printf("INFO daemon LED init\n")
	for _, interpreter := range this.interpreters {
		if interpreter == nil {
			continue
		}
		interpreter.SetMode(led.MODE_PRESET_OFF)
	}
	this.update_interpreters()

	for _, interpreter := range this.interpreters {
		if interpreter == nil {
			continue // LED 初始化失败(如开机时序 sysfs 未就绪),跳过
		}
		interpreter.SetMode(led.MODE_PRESET_ON)
		this.update_interpreters()
		time.Sleep(time.Millisecond * 500)
		interpreter.SetMode(led.MODE_PRESET_OFF)
	}

	ticker := time.NewTicker(this.tick_interval)
	defer ticker.Stop()

	go this.apply_function()

	log.Printf("INFO daemon started\n")
	for {
		select {
		case <-this.stop_chan:
			log.Printf("INFO daemon stopped\n")
			return nil
		case <-ticker.C:
			this.Tick()
			if this.shutting_down {
				return nil // 关机流程(do_shutdown 同步执行)已完成,退出主循环
			}
		}
	}
}

// run_ipc_server 持续提供 IPC server。golang-ipc 的 server 是一次性的:
// 握手失败(旧 cli 二进制、cli 半途退出)会关闭 listener、框架 mainloop
// 退出 —— 死掉就重建(socket 文件由库 run() 先 RemoveAll,无残留)。
// daemon 主循环不依赖 IPC,IPC 故障不影响设备功能。
func (this *Daemon) run_ipc_server() {
	for {
		server, err := ipc.StartServer(base.PROJECT_IDENT, this.daemonipc_config)
		if err != nil {
			log.Printf("WARN: ipc server start: %v", err)
		} else {
			fw := daemonipc.InitServer(this) // 新框架,重新注册 handler
			if err := fw.Start(server); err != nil {
				log.Printf("WARN: ipc framework start: %v", err)
				server.Close()
			} else {
				fw.Wait() // 阻塞到 mainloop 退出(Read 报错 break)
				log.Printf("WARN: ipc server down, restarting")
			}
		}
		time.Sleep(time.Second) // 避免失败风暴
	}
}

var LED_MODE_BLINK *led.LedMode

// init validates cmd and stores all initialised handles on the Daemon struct.
func (this *Daemon) init(cmd *DaemonCmd) error {
	// ---- validate arguments ------------------------------------------------
	if cmd.TickInterval <= 0 {
		return fmt.Errorf("tick interval must > 0")
	}

	this.tick_interval = cmd.TickInterval

	if this.IsValidPath(cmd.Devnode, "/dev/input/", true, true) != base.PATH_STATUS_OK {
		return fmt.Errorf("`%s` is not a valid input device", cmd.Devnode)
	}

	if !this.is_valid_config_fs(cmd.UsbConfigFs) {
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

	// 长按关机阈值必须大于长按阈值,否则长按会先触发子模式选择再触发关机
	if cmd.ShutdownThreshold > 0 && cmd.ShutdownThreshold <= cmd.LongTapThreshold {
		return fmt.Errorf("`--shutdown-threshold` must be greater than `--long-tap-threshold`")
	}

	this.shutdown_threshold = cmd.ShutdownThreshold
	this.shutdown_command = cmd.ShutdownCommand
	this.shutdown_shell = cmd.ShutdownShell
	this.shutdown_timeout = cmd.ShutdownTimeout

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

	controller, err := usb.NewUsbGadgetController(cmd.UsbConfigFs)
	if err != nil {
		return fmt.Errorf("cannot init usb gadget: %w", err)
	}

	if errs := controller.ClearFunctions(); errs != nil {
		return fmt.Errorf("cannot clear usb gadget functions: %v", errs)
	}

	this.controller = controller

	// ---- prepare modes ----------------------------------------------------

	var rndis_qmult string
	if cmd.RndisQmult > 0 {
		rndis_qmult = strconv.FormatUint(uint64(cmd.RndisQmult), 10)
	} else {
		rndis_qmult = ""
	}
	this.modes = []usb.UsbGadgetFunction{
		usb.NewUsbGadgetRndis(rndisIP, base.PROJECT_IDENT+"_", cmd.RndisDeviceMac.String(), cmd.RndisHostMac.String(), cmd.RndisUsbIfname, rndis_qmult, cmd.DnsmasqArgs, rndisClientIP, cmd.RndisClientTimeout, cmd.RndisSerialNumber, cmd.RndisManufacturer, cmd.RndisProduct),
		usb.NewUsbGadgetAdb("/dev/usb-ffs/adb", cmd.AdbSerialNumber, cmd.AdbManufacturer, cmd.AdbProduct, cmd.AdbEnv),
	}

	// ---- initialise LEDs --------------------------------------------------

	this.interpreters = load_led_interpreters(cmd.Leds)

	// ---- init ipc ----
	// InitClient 注册响应包的 payload struct 到全局表,server 端
	// SendPackage 校验时需要;server handler 由 run_ipc_server 每次注册
	daemonipc.InitClient() // load client package definitions
	this.daemonipc_config = &ipc.ServerConfig{
		Encryption:        false,
		UnmaskPermissions: cmd.IPCAllowOtherUser,
	}

	return nil
}

func (this *Daemon) apply_function() {
	defer func() {
		if r := recover(); r != nil {
			// USB 操作链上的 panic 会杀整个进程,兜底;必须释放模式切换锁,
			// 否则 mode_changing 卡死,之后再也切不了模式
			log.Printf("WARN: apply_function panic: %v", r)
			this.mode_changing = false
		}
	}()

	this.mode_changing = true

	// submode 切换:函数不变,不重建 gadget(重建会断开对端 RNDIS 网卡,
	// ICS 需重新就绪,第一次探测必然失败)—— 直接在当前接口上重配网络
	if this.submode_changed {
		this.submode_changed = false
		if interpreter := this.current_interpreter(); interpreter != nil {
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}
		if err := this.controller.ReconfigureFunction(this.modes[this.current_mode]); err != nil {
			log.Printf("WARN: cannot reconfigure function: %v\n", err)
		}
		if !this.turn_off_leds.Load() {
			if interpreter := this.current_interpreter(); interpreter != nil {
				interpreter.SetMode(this.submode_led_mode())
			}
		} else {
			if interpreter := this.current_interpreter(); interpreter != nil {
				interpreter.SetMode(led.MODE_PRESET_OFF)
			}
		}
		log.Printf("INFO: submode switched: mode %d submode %d\n", this.current_mode, this.modes[this.current_mode].GetSubmode())
		this.mode_changing = false
		return
	}

	// effect 期间:模式切换用快闪,子模式切换(选择状态中短按)用关闭 LED
	if this.submode_selection {
		if interpreter := this.current_interpreter(); interpreter != nil {
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}
	} else {
		if interpreter := this.current_interpreter(); interpreter != nil {
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
	if !this.turn_off_leds.Load() {
		if interpreter := this.current_interpreter(); interpreter != nil {
			interpreter.SetMode(this.submode_led_mode())
		}
	} else {
		if interpreter := this.current_interpreter(); interpreter != nil {
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}
	}

	log.Printf("INFO: mode switched: mode %d submode %d\n", this.current_mode, this.modes[this.current_mode].GetSubmode())
	this.mode_changing = false
}

// submode_led_mode 返回当前主模式 submode 对应的 LED 模式。
// submode 超出 1 用 % 2 取 index(0 -> 常亮,1 -> 慢闪)。
func (this *Daemon) submode_led_mode() *led.LedMode {
	submode := this.modes[this.current_mode].GetSubmode()
	index := submode % 2
	if index < 0 { // SetSubmode 只递增,防御负数
		index += 2
	}
	return LED_SUBMODE_MODES[index]
}

// current_interpreter 返回当前主模式对应的 LED interpreter。
// 可能为 nil:LED 数量少于主模式数量,或该 LED 初始化失败。
func (this *Daemon) current_interpreter() *led.LedInterpreter {
	if this.current_mode < 0 || this.current_mode >= len(this.interpreters) {
		return nil
	}
	return this.interpreters[this.current_mode]
}

func (this *Daemon) Tick() {
	// 长按关机检测,优先级高于一切事件处理:按住时长 >= shutdown_threshold
	// 立即关机 —— 默认按键是 KEY_RESTART,松开事件会触发系统重启,
	// poweroff 必须在按住期间调用,不能等松开
	if this.shutting_down {
		return // 关机流程中,停止除 INPUT Grab 外的一切事件处理
	}

	if this.shutdown_threshold > 0 {
		if st := this.input_device.State(); st != nil && st.Duration >= this.shutdown_threshold {
			this.shutting_down = true
			log.Printf("WARN: long-press shutdown triggered\n")
			this.do_shutdown() // 同步执行:完成后 Mainloop 检测 shutting_down 退出进程
			return
		}
	}

	for _, event := range this.input_device.Tick() {
		log.Printf("%+v\n", event)

		if event.Status != input.DEVICE_STATUS_NORMAL {
			log.Printf("ERROR: %s\n", event.Error.Error())
			continue
		}

		switch event.Type {
		case input.INPUT_TAP:
			if this.submode_selection {
				// 子模式选择状态:短按循环切换 submode(LED 用 % 2 显示)
				mode := this.modes[this.current_mode]
				// submode 有界循环 0~max_submode(RNDIS 只有 0/1):无限 +1
				// 会让 enable() 遇到不认识的值报错("have no such submode")
				mode.SetSubmode((mode.GetSubmode() + 1) % (mode.MaxSubmode() + 1))
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
			if interpreter := this.current_interpreter(); interpreter != nil {
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
		if interpreter := this.current_interpreter(); interpreter != nil && interpreter.GetLoopCount() >= 1 {
			this.submode_entry_done = true
			interpreter.SetMode(this.submode_led_mode())
		}
	}

	if this.mode_changed && !this.mode_changing {
		this.mode_changed = false
		this.mode_changing = true

		for _, interpreter := range this.interpreters {
			if interpreter != nil {
				interpreter.SetMode(led.MODE_PRESET_OFF)
			}
		}

		go this.apply_function()
	}

	for _, interpreter := range this.interpreters {
		if interpreter != nil {
			interpreter.Tick()
		}
	}
}

func (this *Daemon) update_interpreters() {
	for _, interpreter := range this.interpreters {
		if interpreter == nil {
			continue
		}
		interpreter.Tick()
	}
}

func (this *Daemon) do_shutdown() {
	wait_shutdown := make(chan bool)

	shell := strings.TrimSpace(this.shutdown_shell)
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/bash"
	}

	shutdown := func() {
		log.Printf("INFO executing shutdown command: %s -c %s\n", shell, this.shutdown_command)
		out, err := exec.Command(shell, "-c", this.shutdown_command).CombinedOutput()
		if err != nil {
			log.Printf("ERROR: shutdown command failed: %v: %s\n", err, out)
		} else {
			log.Printf("INFO shutdown successfully")
		}

		wait_shutdown <- true
	}

	var last_interpreter *led.LedInterpreter

	for _, interpreter := range this.interpreters { // close all first
		if interpreter == nil {
			continue
		}
		interpreter.SetMode(led.MODE_PRESET_OFF)
	}
	this.update_interpreters()

	interpreter_length := len(this.interpreters)
	for i := interpreter_length - 1; i >= 0; i-- {
		interpreter := this.interpreters[i]
		if interpreter == nil {
			continue // LED 初始化失败(如开机时序 sysfs 未就绪),跳过
		}
		last_interpreter = interpreter
		interpreter.SetMode(led.MODE_PRESET_ON)
		this.update_interpreters()
		time.Sleep(time.Millisecond * 500)
		interpreter.SetMode(led.MODE_PRESET_OFF)
	}

	go shutdown()

	if last_interpreter != nil { // keep last Led on to show if system is powered off
		last_interpreter.SetMode(led.MODE_PRESET_ON)
		this.update_interpreters()
	}

	// waiting for shutdown
	select {
	case <-wait_shutdown:
	case <-time.After(this.shutdown_timeout):
		log.Printf("WARN: shutdown command timed out")
	}
}

// load_led_interpreters 初始化每个 LED 设备;失败的槽位留 nil(消费方
// 遍历时判空跳过),避免单颗 LED 故障(systemd 开机时序 sysfs 未就绪)
// 拖垮整个 daemon。
func load_led_interpreters(ledDevnodes []string) []*led.LedInterpreter {
	interpreters := make([]*led.LedInterpreter, len(ledDevnodes))

	for index, ledDevnode := range ledDevnodes {
		ledDevice, err := led.NewLed(ledDevnode)
		if err != nil {
			log.Printf("WARN: cannot create Led device for node `%s`: %s\n", ledDevnode, err)
			continue
		}
		interpreter := led.NewLedInterpreter(ledDevice)
		interpreter.SetMode(led.MODE_PRESET_OFF)
		err = interpreter.Tick()
		if err != nil {
			log.Printf("WARN: cannot init LedInterpreter for node `%s`: %s\n", ledDevnode, err)
			continue
		}
		interpreter.Tick() // Tick 无返回值,只刷新状态(SetMode 内部已 act 一次)

		interpreters[index] = interpreter
	}

	return interpreters
}

func (this *Daemon) is_valid_config_fs(path string) bool {
	status := this.IsValidPath(path, "/sys/kernel/config/usb_gadget/", false, true)
	if status == base.PATH_ERROR_NOT_EXISTS {
		return true // gc -a will create the gadget directory
	}
	return status == base.PATH_STATUS_OK
}
