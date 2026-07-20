package main

// TODO use standard log system

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/led"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/usb"
)

type DaemonCmd struct {
	Devnode              string           `arg:"-d,required" help:"button devnode path"`
	LongTapImmediately   bool             `arg:"--long-tap-immediately" default:"true" help:"emit long-tap when pressing time >= LongTapThreshold, even button is still pressing"`
	LongTapThreshold     time.Duration    `arg:"--long-tap-threshold" default:"500ms" help:"the threshold of long-tap, such as 500ms, 1s"`
	MultipleTapThreshold time.Duration    `arg:"--multiple-tap-threshold" default:"500ms" help:"the threshold of multiple-tap, lower than zero means disable, such as 500ms, 1s"`
	AutoConfirmThreshold time.Duration    `arg:"--auto-confirm-threshold" default:"5s" help:"the threshold that auto confirm mode switch"`
	Leds                 []string         `arg:"-l,--led,separate" help:"led path, such as /sys/class/leds/blue:wifi"`
	UsbConfigFs          string           `arg:"-c,--config-fs" default:"/sys/kernel/config/usb_gadget/g1" help:"usb config-fs path, such as /sys/kernel/config/usb_gadget/g1"`
	GcPath               string           `arg:"-g,--gc-path" default:"gc" help:"gadget controller (https://github.com/HandsomeMod/gc) path or ELF name which can be found in $PATH"`
	RndisDeviceMac       net.HardwareAddr `arg:"--rndis-device-mac" default:"02:00:00:11:22:33" help:"the mac address of current device rndis network interface"`
	RndisHostMac         net.HardwareAddr `arg:"--rndis-host-mac" default:"02:00:00:44:55:66" help:"the network interface mac address of the device which connected to rndis can see"`
	RndisIP              string           `arg:"-a,--rndis-ip" default:"10.22.33.1/24" help:"the IP address of rndis network interface, you need provide a valid IP address and a prefix of network like 10.0.0.100/24"`
	RndisUsbIfname       string           `arg:"-i,--rndis-ifname" default:"usb0" help:"usb ifname name to config RNDIS, you can use \"ip link\" to find the ifname name, such as usb0"`
}

var args struct {
	Daemon *DaemonCmd `arg:"subcommand:daemon"`
}

func isValidPath(path string, base string, file_required bool) bool {
	if !strings.HasPrefix(path, base) {
		return false
	}

	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}

	is_dir := stat.IsDir()
	if (file_required && is_dir) || (!file_required && !is_dir) {
		return false
	}

	return true
}

func isValidCharDevice(path string) bool {
	return isValidPath(path, "/dev/input/", true)
}

func isValidLedDevice(path string) bool {
	return isValidPath(path, "/sys/class/leds/", false)
}

func isValidConfigFs(path string) bool {
	if !strings.HasPrefix(path, "/sys/kernel/config/usb_gadget/") {
		return false
	}

	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		// gc -a will create the gadget directory if it doesn't exist
		return true
	}
	if err != nil {
		return false
	}

	return stat.IsDir()
}

func loadLedInterpreters(led_devnodes []string) []*led.LedInterpreter {
	interpreters := make([]*led.LedInterpreter, len(led_devnodes))

	for index, led_devnode := range led_devnodes {
		led_device, err := led.NewLed(led_devnode)
		if err != nil {
			log.Printf("WARN: cannot create Led device for node `%s`: %s\n", led_devnode, err)
			continue
		}
		interpreter := led.NewLedInterpreter(led_device)
		err = interpreter.SetMode(led.MODE_PRESET_OFF)
		if err != nil {
			log.Printf("WARN: cannot init LedInterpreter for node `%s`: %s\n", led_devnode, err)
			continue
		}

		interpreters[index] = interpreter
	}

	return interpreters
}

func fatal(message string) {
	log.Fatal("FATAL: " + message + "\n")
	os.Exit(1)
}

// TODO handle SIGNALs
// TODO cover panic

func main() {
	parser := arg.MustParse(&args)

	switch {
	case args.Daemon != nil:
		// TODO 这里的逻辑丢到专门的 Daemon
		if !isValidCharDevice(args.Daemon.Devnode) {
			fatal(fmt.Sprintf("`%s` is not a valid input device", args.Daemon.Devnode))
		}

		if !isValidConfigFs(args.Daemon.UsbConfigFs) {
			fatal(fmt.Sprintf("`%s` is not valid config fs", args.Daemon.UsbConfigFs))
		}

		if args.Daemon.Leds != nil {
			for _, led_devnode := range args.Daemon.Leds {
				if !isValidLedDevice(led_devnode) {
					fatal(fmt.Sprintf("`%s` is not a valid led device\n", led_devnode))
				}
			}
		}

		rndis_ip, err := netip.ParsePrefix(args.Daemon.RndisIP)
		if err != nil {
			fatal(fmt.Sprintf("`%s` is not valid IP address", args.Daemon.RndisIP))
		}

		// if args.DaemonCmd.LongTapThreshold <= 0 {
		// 	log.Println("WARN: invalid LONG-TAP-THRESHOLD, use default")
		// 	args.DaemonCmd.LongTapThreshold = time.Millisecond * 500
		// }

		// if args.DaemonCmd.AutoConfirmThreshold <= 0 {
		// 	log.Println("WARN: invalid AUTO-CONFIRM-THRESHOLD, use default")
		// 	args.DaemonCmd.AutoConfirmThreshold = time.Second * 5
		// }

		log.Printf("daemon started\n")

		input_device, err := core.NewDevice(args.Daemon.Devnode, &core.InputDeviceConfig{
			LongTapThreshold:     args.Daemon.LongTapThreshold,
			MultipleTapThreshold: args.Daemon.MultipleTapThreshold,
			LongTapImmediately:   args.Daemon.LongTapImmediately,
		})

		interpreters := loadLedInterpreters(args.Daemon.Leds)

		for _, interpreter := range interpreters {
			interpreter.SetMode(led.MODE_PRESET_ON)
			interpreter.Tick()
			time.Sleep(time.Millisecond * 300)
			interpreter.SetMode(led.MODE_PRESET_OFF)
		}

		if err != nil {
			fatal(fmt.Sprintf("cannot create input device: %s", err))
		}

		status, err := input_device.Open()
		if status != core.DEVICE_STATUS_NORMAL {
			fatal(fmt.Sprintf("cannot open input device (%d): %s", status, err))
		}

		input_device.StartDaemon()

		controller, err := usb.NewUsbGadgetController(args.Daemon.UsbConfigFs, args.Daemon.GcPath)
		if err != nil {
			fatal(fmt.Sprintf("cannot init usb gadget: %s", err))
		}

		errors := controller.ClearFunctions()
		if errors != nil {
			fatal(fmt.Sprintf("cannot clear usb gadget functions: %v", errors))
		}

		mode := 0
		modes := []usb.UsbGadgetFunction{
			usb.NewUsbGadgetRndis(rndis_ip, "rndis_", args.Daemon.RndisDeviceMac.String(), args.Daemon.RndisHostMac.String(), args.Daemon.RndisUsbIfname, ""),
			usb.NewUsbGadgetAdb("/dev/usb-ffs/adb"),
		}
		modes_length := len(modes)
		mode_changed := false

		for {
			for _, event := range input_device.Tick() {
				log.Printf("%+v\n", event)

				if event.Status != core.DEVICE_STATUS_NORMAL {
					fatal(event.Error.Error())
				}

				switch event.Type {
				case core.INPUT_TAP:
					mode += 1
					mode %= modes_length
					mode_changed = true
				case core.INPUT_LONG_TAP:
					mode -= 1
					mode %= modes_length
					if mode < 0 {
						mode += modes_length
					}
					mode_changed = true
				case core.INPUT_MULTIPLE_TAP:
					// TODO
				case core.INPUT_ERROR:
					// TODO WARNING
				}
			}

			if mode_changed {
				if errors := controller.ClearFunctions(); errors != nil {
					log.Printf("WARN: cannot clear functions: %v\n", errors)
				}

				if err = controller.AddFunction(modes[mode]); err != nil {
					log.Printf("WARN: cannot add function: %v\n", err)
				}

				if errors := controller.Apply(); errors != nil {
					log.Printf("WARN: cannot apply functions: %v\n", errors)
				}

				if errors := controller.UpdateGadget(); errors != nil {
					log.Printf("WARN: cannot update gadget: %v\n", errors)
				}

				for _, interpreter := range interpreters {
					interpreter.SetMode(led.MODE_PRESET_OFF)
				}
				interpreters[mode].SetMode(led.MODE_PRESET_ON)

				mode_changed = false
			}

			for _, interpreter := range interpreters {
				interpreter.Tick()
			}

			time.Sleep(time.Millisecond * 10)
		}
	default:
		parser.WriteHelp(os.Stdout)
	}
}
