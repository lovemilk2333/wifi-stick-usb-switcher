package usb

import (
	"fmt"
	"log"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
)

// TODO
// type Usb

type UsbGadgetRndis struct {
	ip_addr           netip.Prefix
	connection_prefix string

	dev_addr  string
	host_addr string
	ifname    string
	qmult     string

	UsbGadgetFunctionBase
}

func (this *UsbGadgetRndis) effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	writer := ctx.getSubpathWriter()
	// Functions live under <config_fs>/functions/<type>.<instance>/, not at the root.
	basepath := filepath.Join("functions", this.getPath())

	set := func(field string, data string) {
		if len(data) > 0 {
			writer.WriteSubpath(base.Subpath(filepath.Join(basepath, field)), true, []byte(data))
		}
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

	return nil
}

// postEnable configures the RNDIS network interface.
// This runs AFTER gc -e, so the network interface (e.g. usb0) exists.
func (this *UsbGadgetRndis) postEnable(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	writer := ctx.getSubpathWriter()
	basepath := filepath.Join("functions", this.getPath())

	get := func(field string) (string, error) {
		data, err := writer.ReadSubpath(base.Subpath(filepath.Join(basepath, field)), true)
		if err != nil {
			return "", err
		}
		return string(data), err
	}

	ifname, err := get("ifname")
	if err != nil {
		return fmt.Errorf("cannot get ifname: %w", err)
	}

	// Set IP address and bring interface up.
	// The script doesn't use nmcli for gadget config — use ip commands directly.
	if out, err := exec.Command("ip", "addr", "add", this.ip_addr.Masked().String(), "dev", ifname).CombinedOutput(); err != nil {
		// May already exist from a previous cycle; log but don't fail.
		log.Printf("WARN: ip addr add (may already exist): %s, output: %s\n", err, string(out))
	}

	if out, err := exec.Command("ip", "link", "set", ifname, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("cannot set link up: %w, output: %s", err, string(out))
	}

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

func NewUsbGadgetRndis(ip_addr netip.Prefix, connection_prefix string, dev_addr string, host_addr string, ifname string, qmult string) *UsbGadgetRndis {
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
