package usb

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
)

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

	// Leaving ADB mode: the gadget UDC is unbound by gc -c, but the orphaned
	// adbd keeps polling /dev/usb-ffs/adb — kill it so it can't interfere
	// with the next ADB switch (best-effort: no adbd running is fine).
	_ = exec.Command("killall", "adbd").Run()

	// Override function subpath fields — dev_addr / host_addr.
	// Best-effort: on failure the gadget still enumerates with gc's random
	// MACs, so a failed write must not abort the switch.
	if err := this.overrideMacs(ctx); err != nil {
		log.Printf("WARN: override rndis MACs: %v — using gc default MACs\n", err)
	}

	// NOTE: do NOT bind the UDC here.  gc -e (usbg_enable_gadget) is the
	// only place the UDC gets bound — like /sbin/mobian-usb-gadget, which
	// binds it exactly once, after a settle delay, so the host re-enumerates
	// the port instead of seeing a connect/disconnect/connect burst.
	return nil
}

// overrideMacs writes dev_addr / host_addr.  While the function is linked
// into a config (gc -a does that automatically), configfs locks the
// attributes and the write fails with EBUSY — so the link is removed, the
// MACs written, then the link re-created, like libusbgx's
// usbg_function_set_attrs.  The UDC is not bound here (gc -e runs later in
// enableGadget), so modifying the config is safe.  Verified sequence on
// this platform (HandsomeMod gc): rm config link → write → ln with an
// ABSOLUTE target — relative targets containing ".." make configfs fail
// with ENOENT even though gc's own (kernel-normalized) links look relative.
func (this *UsbGadgetRndis) overrideMacs(ctx UsbGadgetContext) error {
	instance := this.getInstance()

	devSubpath := base.Subpath(functionSubpath(this._type, instance, "dev_addr"))
	hostSubpath := base.Subpath(functionSubpath(this._type, instance, "host_addr"))
	funcDir := filepath.Dir(string(devSubpath)) // `functions/rndis.rndis.1`
	funcDirAbs := filepath.Join(ctx.Basepath, funcDir)

	// Find the config link pointing at our function dir, e.g.
	// `configs/c1.1/rndis.1 -> .../functions/rndis.rndis.1`.
	linkPath := ""
	configDirs, _ := filepath.Glob(filepath.Join(ctx.Basepath, "configs", "*"))
	for _, configDir := range configDirs {
		entries, err := os.ReadDir(configDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Readlink(filepath.Join(configDir, entry.Name()))
			if err != nil {
				continue
			}
			if filepath.Base(target) == filepath.Base(funcDirAbs) {
				linkPath = filepath.Join(configDir, entry.Name())
				break
			}
		}
		if linkPath != "" {
			break
		}
	}

	writeMacs := func() error {
		var errs []error
		if err := ctx.WriteSubpath(devSubpath, true, []byte(this.dev_addr+"\n")); err != nil {
			errs = append(errs, fmt.Errorf("write dev_addr: %w", err))
		}
		if err := ctx.WriteSubpath(hostSubpath, true, []byte(this.host_addr+"\n")); err != nil {
			errs = append(errs, fmt.Errorf("write host_addr: %w", err))
		}
		return errors.Join(errs...)
	}

	if linkPath == "" {
		// Not attached to any config — attributes unlocked, write directly.
		return writeMacs()
	}

	// Detach, write, then always re-attach so the gadget stays enumerable
	// even if a MAC write failed.
	if err := os.Remove(linkPath); err != nil {
		return fmt.Errorf("detach %s: %w", linkPath, err)
	}

	macErr := writeMacs()

	if err := os.Symlink(funcDirAbs, linkPath); err != nil {
		if macErr != nil {
			return fmt.Errorf("write MACs (%v) and re-attach %s: %w", macErr, linkPath, err)
		}
		return fmt.Errorf("re-attach %s: %w", linkPath, err)
	}

	return macErr
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
