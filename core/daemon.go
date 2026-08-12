package core

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/input"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/led"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/usb"
)

// TODO
// https://github.com/james-barrow/golang-ipc

const PROJECT_IDENT = "miruku-wifi-stick-usb-switcher"

// modes 数组索引 — 见 init() 中的构建顺序
const (
	mode_index_rndis = 0
	mode_index_adb   = 1
)

// dnsmasq 实例的 pid 文件,ADB 模式切换时按它精确停掉,避免误杀系统 dnsmasq
const dnsmasqPidFile = "/tmp/dnsmasq-usb0.pid"

type DaemonCmd struct {
	Devnode              string           `arg:"-d,required" help:"button devnode path"`
	LongTapImmediately   bool             `arg:"--long-tap-immediately" default:"true" help:"emit long-tap when pressing time >= LongTapThreshold, even button is still pressing"`
	LongTapThreshold     time.Duration    `arg:"--long-tap-threshold" default:"500ms" help:"the threshold of long-tap, such as 500ms, 1s"`
	MultipleTapThreshold time.Duration    `arg:"--multiple-tap-threshold" default:"500ms" help:"the threshold of multiple-tap, lower than zero means disable, such as 500ms, 1s"`
	AutoConfirmThreshold time.Duration    `arg:"--auto-confirm-threshold" default:"5s" help:"the threshold that auto confirm mode switch"`
	Leds                 []string         `arg:"-l,--led,separate" help:"led path, such as /sys/class/leds/blue:wifi"`
	UsbConfigFs          string           `arg:"-c,--config-fs" default:"/sys/kernel/config/usb_gadget/g1" help:"usb config-fs path, such as /sys/kernel/config/usb_gadget/g1"`
	GcPath               string           `arg:"-g,--gc-path" default:"gc" help:"gadget controller (https://github.com/HandsomeMod/gc) path or ELF name which can be found in $PATH"`
	RndisDeviceMac       net.HardwareAddr `arg:"--rndis-device-mac" default:"02:12:34:56:78:9a" help:"the mac address of current device rndis network interface"`
	RndisHostMac         net.HardwareAddr `arg:"--rndis-host-mac" default:"02:98:76:54:32:10" help:"the network interface mac address of the device which connected to rndis can see"`
	RndisIP              string           `arg:"-a,--rndis-ip" default:"10.22.33.1/24" help:"the IP address of rndis network interface, you need provide a valid IP address and a prefix of network like 10.0.0.100/24"`
	RndisUsbIfname       string           `arg:"-i,--rndis-ifname" default:"usb0" help:"usb ifname name to config RNDIS, you can use \"ip link\" to find the ifname name, such as usb0"`
	// TickRate             time.Duration    `arg:"--tick-rate" default:"10ms" help:"daemon event loop tick rate"`
}

type Daemon struct {
	base.PathChecker

	input_device *input.InputDevice
	controller   *usb.UsbGadgetController
	interpreters []*led.LedInterpreter
	modes        []usb.UsbGadgetFunction
	current_mode int
	mode_changed bool
	tick_rate    time.Duration

	// RNDIS 网络配置 — 由 init() 从 DaemonCmd 拷贝
	rndis_ifname string
	rndis_ip     netip.Prefix
}

func NewDaemon(cmd DaemonCmd, tick_rate time.Duration) (*Daemon, error) {
	daemon := &Daemon{}
	daemon.tick_rate = tick_rate
	if err := daemon.init(cmd); err != nil {
		return nil, err
	}
	return daemon, nil
}

// Mainloop runs the daemon event loop at the configured tick rate.
func (this *Daemon) Mainloop() {
	log.Printf("daemon started\n")
	ticker := time.NewTicker(this.tick_rate)
	defer ticker.Stop()

	for range ticker.C {
		this.Tick()
	}
}

// init validates cmd and stores all initialised handles on the Daemon struct.
func (this *Daemon) init(cmd DaemonCmd) error {
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
		usb.NewUsbGadgetRndis(rndisIP, PROJECT_IDENT+"_", cmd.RndisDeviceMac.String(), cmd.RndisHostMac.String(), cmd.RndisUsbIfname, ""),
		usb.NewUsbGadgetAdb("/dev/usb-ffs/adb"),
	}

	this.rndis_ifname = cmd.RndisUsbIfname
	this.rndis_ip = rndisIP

	// ---- initialise LEDs --------------------------------------------------

	this.interpreters = loadLedInterpreters(cmd.Leds)

	for _, interpreter := range this.interpreters {
		interpreter.SetMode(led.MODE_PRESET_ON)
		interpreter.Tick()
		time.Sleep(time.Millisecond * 500)
		interpreter.SetMode(led.MODE_PRESET_OFF)
	}

	return nil
}

func (this *Daemon) Tick() {
	for _, event := range this.input_device.Tick() {
		log.Printf("%+v\n", event)

		if event.Status != input.DEVICE_STATUS_NORMAL {
			log.Fatalf("FATAL: %s\n", event.Error.Error())
		}

		switch event.Type {
		case input.INPUT_TAP:
			this.current_mode++
			this.current_mode %= len(this.modes)
			this.mode_changed = true
		case input.INPUT_LONG_TAP:
			this.current_mode--
			this.current_mode %= len(this.modes)
			if this.current_mode < 0 {
				this.current_mode += len(this.modes)
			}
			this.mode_changed = true
		case input.INPUT_MULTIPLE_TAP:
			// TODO
		case input.INPUT_ERROR:
			// TODO WARNING
		}
	}

	if this.mode_changed {
		if errs := this.controller.ClearFunctions(); errs != nil {
			log.Printf("WARN: cannot clear functions: %v\n", errs)
		}

		if err := this.controller.AddFunction(this.modes[this.current_mode]); err != nil {
			log.Printf("WARN: cannot add function: %v\n", err)
		}

		apply_ok := true
		if errs := this.controller.Apply(); errs != nil {
			apply_ok = false
			log.Printf("WARN: cannot apply functions: %v\n", errs)
		}

		if errs := this.controller.UpdateGadget(); errs != nil {
			apply_ok = false
			log.Printf("WARN: cannot update gadget: %v\n", errs)
		}

		if apply_ok {
			if this.current_mode == mode_index_rndis {
				this.setupRndisNetwork()
			} else {
				this.teardownRndisNetwork()
			}
		}

		for _, interpreter := range this.interpreters {
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}
		this.interpreters[this.current_mode].SetMode(led.MODE_PRESET_ON)

		this.mode_changed = false
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

// setupRndisNetwork 配置 usb0 并启动 dnsmasq,用户验证过的流程:
//
//	ip link set usb0 up
//	ip addr add 10.22.33.1/24 dev usb0      (已分配时忽略错误)
//	dnsmasq --interface=usb0 --bind-interfaces --dhcp-range=... ...
//
// usb0 由内核在 gc -e 绑定 gadget 后才创建,所以先轮询它出现。
func (this *Daemon) setupRndisNetwork() {
	ifname := this.rndis_ifname

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := net.InterfaceByName(ifname); err == nil {
			break
		}
		if time.Now().After(deadline) {
			log.Printf("WARN: rndis interface `%s` not found after apply, skip network setup\n", ifname)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	// usb0 由 daemon 静态配置 + dnsmasq 服务。NM 的 "有线连接 1" (method=auto)
	// 会把 usb0 当 DHCP client,永远拿不到地址(设备上没有 DHCP server 服务
	// 自身),超时后停用连接并删掉 IPv4 地址,破坏 RNDIS。让它别管理 usb0。
	// 仅运行时生效,重启后 NM 恢复 — 每次 setup 都会重新执行。
	_ = exec.Command("nmcli", "device", "set", ifname, "managed", "no").Run()

	if out, err := exec.Command("ip", "link", "set", ifname, "up").CombinedOutput(); err != nil {
		log.Printf("WARN: `ip link set %s up`: %v, output: %s\n", ifname, err, string(out))
	}

	ipSpec := this.rndis_ip.String()
	if out, err := exec.Command("ip", "addr", "add", ipSpec, "dev", ifname).CombinedOutput(); err != nil {
		// 重复 apply 时地址已存在 — 不是错误
		log.Printf("DEBUG: `ip addr add %s dev %s`: %v, output: %s\n", ipSpec, ifname, err, string(out))
	}

	this.startDnsmasq()
}

// startDnsmasq 在 usb0 上启动 dnsmasq DHCP 服务器,供 USB host 获取地址。
// 池从本机 IP 之后到子网最后一个地址;--port=0 关闭 DNS,避免与系统
// dnsmasq 冲突(系统实例已禁用,usb0 实例独占 67 端口)。
func (this *Daemon) startDnsmasq() {
	if pid, err := os.ReadFile(dnsmasqPidFile); err == nil {
		if pidNum, err := strconv.Atoi(strings.TrimSpace(string(pid))); err == nil && pidNum > 0 && processAlive(pidNum) {
			log.Printf("DEBUG: dnsmasq for usb0 already running (pid %d)\n", pidNum)
			return
		}
	}

	router := this.rndis_ip.Masked().Addr()
	last, ok := subnetLast(this.rndis_ip)
	if !ok {
		log.Printf("WARN: cannot derive dhcp pool from `%s`, skip dnsmasq\n", this.rndis_ip)
		return
	}
	start := router.Next()
	end := last.Prev()
	if !start.IsValid() || start.Compare(end) > 0 {
		log.Printf("WARN: invalid dhcp pool %s-%s for `%s`, skip dnsmasq\n", start, end, this.rndis_ip)
		return
	}

	args := []string{
		"--interface=" + this.rndis_ifname,
		"--bind-interfaces",
		fmt.Sprintf("--dhcp-range=%s,%s,12h", start, end),
		"--dhcp-option=option:router," + router.String(),
		"--port=0", // 不提供 DNS
		"--no-resolv",
		"--no-hosts",
		"--pid-file=" + dnsmasqPidFile,
	}
	if out, err := exec.Command("dnsmasq", args...).CombinedOutput(); err != nil {
		log.Printf("WARN: cannot start dnsmasq: %v, output: %s\n", err, string(out))
		return
	}
	log.Printf("DEBUG: dnsmasq started on %s (pool %s-%s, router %s)\n", this.rndis_ifname, start, end, router)
}

// teardownRndisNetwork 切到 ADB 模式时停掉 dnsmasq。usb0 接口随 gadget
// 一起消失,IP 地址自动清除,无需处理。
func (this *Daemon) teardownRndisNetwork() {
	pidData, err := os.ReadFile(dnsmasqPidFile)
	if err != nil {
		return // 没有运行
	}

	pidNum, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil || pidNum <= 0 || !processAlive(pidNum) {
		return
	}

	if err := syscall.Kill(pidNum, syscall.SIGTERM); err != nil {
		log.Printf("WARN: cannot stop dnsmasq (pid %d): %v\n", pidNum, err)
		return
	}
	_ = os.Remove(dnsmasqPidFile)
	log.Printf("DEBUG: stopped dnsmasq (pid %d)\n", pidNum)
}

// processAlive 检查 pid 对应的进程是否存在。
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// subnetLast 返回 prefix 子网的最后一个地址(广播地址)。
func subnetLast(prefix netip.Prefix) (netip.Addr, bool) {
	if !prefix.IsValid() || prefix.Bits() >= 32 || !prefix.Addr().Is4() {
		return netip.Addr{}, false
	}

	a := prefix.Masked().Addr().As4()
	network := binary.BigEndian.Uint32(a[:])
	mask := uint32(0xffffffff) << (32 - prefix.Bits())
	broadcast := network | ^mask

	var last [4]byte
	binary.BigEndian.PutUint32(last[:], broadcast)
	return netip.AddrFrom4(last), true
}

func (this *Daemon) isValidConfigFs(path string) bool {
	status := this.IsValidPath(path, "/sys/kernel/config/usb_gadget/", false, true)
	if status == base.PATH_ERROR_NOT_EXISTS {
		return true // gc -a will create the gadget directory
	}
	return status == base.PATH_STATUS_OK
}
