package usb

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
)

// TODO
// type Usb

type UsbGadgetRndis struct {
	ip_addr           net.IPNet
	connection_prefix string

	dev_addr  string
	host_addr string
	ifname    string
	qmult     string

	UsbGadgetFunctionBase
}

func (this *UsbGadgetRndis) effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	writer := ctx.getSubpathWriter()
	basepath := this.getPath()

	set := func(field string, data string) {
		if len(data) > 0 {
			writer.WriteSubpath(base.Subpath(filepath.Join(basepath, field)), true, []byte(data))
		}
	}

	get := func(field string) string {
		data, err := writer.ReadSubpath(base.Subpath(filepath.Join(basepath, field)), true)
		if err != nil {
			return ""
		}

		return string(data)
	}

	set("dev_addr", this.dev_addr)
	set("host_addr", this.host_addr)
	set("ifname", this.ifname)
	set("qmult", this.qmult)

	// TODO handle errors
	ctx.setAttr(USB_GADGET_SUBPATH_VENDOR, "0x1d6b")
	ctx.setAttr(USB_GADGET_SUBPATH_PRODUCT, "0x0104")
	ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_CLASS, "0xEF")
	ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_SUBCLASS, "0x02")
	ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_PROTOCOL, "0x01")

	connection_name := this.connection_prefix + this.getInstance()

	need_create_connection := true
	process := exec.Command("nmcli", "connection", "show", connection_name)
	if err := process.Start(); err != nil {
		log.Printf("WARN: cannot run `nmcli connection show %s`: %s\n", connection_name, err)
	} else {
		if err := process.Wait(); err != nil {
			if exiterr, ok := err.(*exec.ExitError); ok {
				need_create_connection = exiterr.ExitCode() != 0
			} else {
				log.Printf("WARN: cannot run `nmcli connection show %s`: %s\n", connection_name, err)
			}
		}
	}

	ifname := get("ifname")

	if ifname == "" {
		return fmt.Errorf("cannot get ifname to create connection")
	}

	if need_create_connection {
		process = exec.Command("nmcli", "connection", "add", "con-name", connection_name, "ifname", ifname, "type", "ethernet", "ip4", this.ip_addr.String())
		if err := process.Start(); err != nil {
			return fmt.Errorf("cannot create connection: %w", err)
		} else if err := process.Wait(); err != nil {
			return fmt.Errorf("cannot create connection: %w", err)
		}
	}

	process = exec.Command("nmcli", "connection", "modify", connection_name, "ipv4.route-metric", "1500")
	if err := process.Start(); err != nil {
		return fmt.Errorf("cannot modify connection: %w", err)
	} else if err := process.Wait(); err != nil {
		return fmt.Errorf("cannot modify connection: %w", err)
	}

	process = exec.Command("nmcli", "connection", "modify", connection_name, "ipv4.dns-priority", "150")
	if err := process.Start(); err != nil {
		return fmt.Errorf("cannot modify connection: %w", err)
	} else if err := process.Wait(); err != nil {
		return fmt.Errorf("cannot modify connection: %w", err)
	}

	process = exec.Command("nmcli", "connection", "modify", connection_name, "ipv4.method", "shared")
	if err := process.Start(); err != nil {
		return fmt.Errorf("cannot modify connection: %w", err)
	} else if err := process.Wait(); err != nil {
		return fmt.Errorf("cannot modify connection: %w", err)
	}

	process = exec.Command("ip", "link", "set", ifname, "up")
	if err := process.Start(); err != nil {
		return fmt.Errorf("cannot up connection: %w", err)
	} else if err := process.Wait(); err != nil {
		return fmt.Errorf("cannot up connection: %w", err)
	}

	/*
		if ! nmcli connection show "<connection-name>" >/dev/null 2>&1; then
			# Create network connection
			nmcli connection add con-name <connection-name> \
								ifname <ifname> \
								type ethernet \
								ip4 <ip>

			# Set priorities so it doesn't take precedence over sWiFi/mobile connections
			nmcli connection modify <connection-name> ipv4.route-metric 1500
			nmcli connection modify <connection-name> ipv4.dns-priority 150

			# Auto connection so it can be used for tethering
			nmcli connection modify <connection-name> ipv4.method shared

			# this is optional, only for sim card
			# user can run this in their own scripts
			# nmcli con add con-name "modem" type "gsm" ifname "wwan0qmi0"

			ip link set <ifname> up
		fi
	*/

	return nil
}

func SnapshotUsbGadgetRndis(instance string) *UsbGadgetRndis {
	rndis := &UsbGadgetRndis{}
	instance = strings.TrimSpace(instance)

	rndis.instance = instance
	rndis._type = "rndis"
	rndis.code = USB_GADGET_FUNCTION_CODE_RNDIS
	return rndis
}

func NewUsbGadgetRndis(ip_addr net.IPNet, connection_prefix string, dev_addr string, host_addr string, ifname string, qmult string) *UsbGadgetRndis {
	rndis := &UsbGadgetRndis{}

	rndis.ip_addr = ip_addr
	rndis.connection_prefix = connection_prefix
	rndis.dev_addr = dev_addr
	rndis.host_addr = host_addr
	rndis.ifname = ifname
	rndis.qmult = qmult

	rndis._type = "rndis"
	rndis.code = USB_GADGET_FUNCTION_CODE_RNDIS
	return rndis
}
