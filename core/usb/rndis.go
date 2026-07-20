package usb

import (
	"fmt"
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

	// Create a NetworkManager connection with shared mode so the host
	// gets an IP via DHCP (10.22.33.x) and the device gets NAT/sharing.
	connection_name := this.connection_prefix + this.getInstance()

	output, err := exec.Command("nmcli", "connection", "show", connection_name).CombinedOutput()
	exists := err == nil

	if !exists {
		output, err = exec.Command("nmcli", "connection", "add",
			"con-name", connection_name,
			"ifname", ifname,
			"type", "ethernet",
			"ip4", this.ip_addr.Masked().String(),
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("cannot add nmcli connection: %w, output: %s", err, string(output))
		}
	}

	// Switch to shared mode (DHCP + NAT on the device side)
	output, err = exec.Command("nmcli", "connection", "modify", connection_name,
		"ipv4.route-metric", "1500",
		"ipv4.dns-priority", "150",
		"ipv4.method", "shared",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot modify nmcli connection: %w, output: %s", err, string(output))
	}

	// Bring up the connection
	output, err = exec.Command("nmcli", "connection", "up", connection_name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot up nmcli connection: %w, output: %s", err, string(output))
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
