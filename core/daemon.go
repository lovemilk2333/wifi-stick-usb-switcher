package core

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
	UsbConfigFs          string           `arg:"-c,--config-fs" default:"/sys/kernel/config/usb_gadget/g1" help:"usb config-fs path, such as /sys/kernel/config/usb_gadget/g1"`
	GcPath               string           `arg:"-g,--gc-path" default:"gc" help:"gadget controller (https://github.com/HandsomeMod/gc) path or ELF name which can be found in $PATH"`
	RndisDeviceMac       net.HardwareAddr `arg:"--rndis-device-mac" default:"02:12:34:56:78:9a" help:"the mac address of current device rndis network interface"`
	RndisHostMac         net.HardwareAddr `arg:"--rndis-host-mac" default:"02:98:76:54:32:10" help:"the network interface mac address of the device which connected to rndis can see"`
	RndisIP              string           `arg:"-a,--rndis-ip" default:"10.22.33.1/24" help:"the IP address of rndis network interface, you need provide a valid IP address and a prefix of network like 10.0.0.100/24"`
	RndisUsbIfname       string           `arg:"-i,--rndis-ifname" default:"usb0" help:"usb ifname name to config RNDIS, you can use \"ip link\" to find the ifname name, such as usb0"`
	RndisSerialNumber    string           `arg:"--rndis-serial-number" default:"wifi-stick-miruku" help:"the serial number string of the rndis usb gadget device"`
	RndisManufacturer    string           `arg:"--rndis-manufacturer" default:"wifi-stick" help:"the manufacturer string of the rndis usb gadget device"`
	RndisProduct         string           `arg:"--rndis-product" default:"RNDIS Ethernet" help:"the product string of the rndis usb gadget device"`
	AdbSerialNumber      string           `arg:"--adb-serial-number" default:"wifi-stick-miruku" help:"the serial number string of the adb usb gadget device"`
	AdbManufacturer      string           `arg:"--adb-manufacturer" default:"Google" help:"the manufacturer string of the adb usb gadget device"`
	AdbProduct           string           `arg:"--adb-product" default:"ADB Gadget" help:"the product string of the adb usb gadget device"`
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
	turn_off_leds    bool
	tick_rate        time.Duration
	daemonipc        *daemonipc.IPCFramework
	daemonipc_config *ipc.ServerConfig
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
	// TODO
	// ipc_server, err := ipc.StartServer(base.PROJECT_IDENT, this.daemonipc_config)
	// if err != nil {
	// 	return err
	// }

	// err = this.daemonipc.Start(ipc_server)
	// if err != nil {
	// 	return err
	// }

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

	LED_MODE_BLINK = led.NewLedMode().OnDuration(cmd.LedBlinkDuration).Wait(cmd.LedBlinkInterval).Done()

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
		usb.NewUsbGadgetRndis(rndisIP, base.PROJECT_IDENT+"_", cmd.RndisDeviceMac.String(), cmd.RndisHostMac.String(), cmd.RndisUsbIfname, "", cmd.DnsmasqArgs, cmd.RndisSerialNumber, cmd.RndisManufacturer, cmd.RndisProduct),
		usb.NewUsbGadgetAdb("/dev/usb-ffs/adb", cmd.AdbSerialNumber, cmd.AdbManufacturer, cmd.AdbProduct),
	}

	// ---- initialise LEDs --------------------------------------------------

	this.interpreters = loadLedInterpreters(cmd.Leds)

	for _, interpreter := range this.interpreters {
		interpreter.SetMode(led.MODE_PRESET_ON)
		interpreter.Tick()
		time.Sleep(time.Millisecond * 500)
		interpreter.SetMode(led.MODE_PRESET_OFF)
	}

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

	this.interpreters[this.current_mode].SetMode(LED_MODE_BLINK)

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

	if !this.turn_off_leds {
		this.interpreters[this.current_mode].SetMode(led.MODE_PRESET_ON)
	} else {
		this.interpreters[this.current_mode].SetMode(led.MODE_PRESET_OFF)
	}

	this.mode_changing = false
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
