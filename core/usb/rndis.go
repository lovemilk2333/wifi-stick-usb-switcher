package usb

import (
	"fmt"
	"net/netip"
	"os/exec"
	"strings"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
)

// TODO fix RNDIS DHCP not work
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

// add uses gc -a rndis, inherited from UsbGadgetFunctionBase, to create the
// RNDIS function.  gc handles the gadget directory, config, OS descriptors
// and the function symlink; effect() only writes the subpath overrides.

func (this *UsbGadgetRndis) effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	basepath := ctx.GetBasepath()

	instance := this.getInstance()
	if instance == "" {
		return fmt.Errorf("rndis instance not set after gc -a")
	}

	// Override device IDs and class codes — gc defaults (from cmake config)
	// differ from the RNDIS-mode values we need.
	if err := ctx.setAttr(USB_GADGET_SUBPATH_VENDOR, "0x1d6b"); err != nil {
		return fmt.Errorf("write idVendor: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_PRODUCT, "0x0104"); err != nil {
		return fmt.Errorf("write idProduct: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_BCD_USB, "0x0200"); err != nil {
		return fmt.Errorf("write bcdUSB: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_CLASS, "0xEF"); err != nil {
		return fmt.Errorf("write bDeviceClass: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_SUBCLASS, "0x02"); err != nil {
		return fmt.Errorf("write bDeviceSubClass: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_PROTOCOL, "0x01"); err != nil {
		return fmt.Errorf("write bDeviceProtocol: %w", err)
	}

	// Override strings (gc defaults are generic HandsomeMod strings).
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_SERIALNUMBER, "wifi-stick-miruku"); err != nil {
		return fmt.Errorf("write serialnumber: %w", err)
	}
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_MANUFACTURER, "wifi-stick"); err != nil {
		return fmt.Errorf("write manufacturer: %w", err)
	}
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_PRODUCT, "RNDIS Ethernet"); err != nil {
		return fmt.Errorf("write product: %w", err)
	}

	// Override function subpath fields — dev_addr / host_addr.
	devAddrPath := functionSubpath(this._type, instance, "dev_addr")
	hostAddrPath := functionSubpath(this._type, instance, "host_addr")

	if err := ctx.WriteSubpath(base.Subpath(devAddrPath), true, []byte(this.dev_addr+"\n")); err != nil {
		return fmt.Errorf("write rndis dev_addr: %w", err)
	}
	if err := ctx.WriteSubpath(base.Subpath(hostAddrPath), true, []byte(this.host_addr+"\n")); err != nil {
		return fmt.Errorf("write rndis host_addr: %w", err)
	}

	// Assign UDC — needed before gc -e in enableGadget().
	udcScript := fmt.Sprintf(
		"udc=$(ls /sys/class/udc | head -n 1); [ -n \"$udc\" ] && echo \"$udc\" > '%s/UDC'",
		basepath,
	)
	if out, err := exec.Command("sh", "-c", udcScript).CombinedOutput(); err != nil {
		return fmt.Errorf("UDC assign failed: %w, output: %s", err, string(out))
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
