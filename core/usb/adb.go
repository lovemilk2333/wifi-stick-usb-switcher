package usb

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// type Usb
// TODO

var adbd_process *exec.Cmd = nil

type UsbGadgetAdb struct {
	dev_name string
	ffs_path string

	UsbGadgetFunctionBase
}

// add uses gc -a ffs, inherited from UsbGadgetFunctionBase, to create the
// FFS function.  gc handles the gadget directory, config, and function
// symlink; effect() writes the subpath overrides and performs the FFS-
// specific setup (mount, adbd).

func (this *UsbGadgetAdb) effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	instance := this.getInstance()
	if instance == "" {
		return fmt.Errorf("adb instance not set after gc -a")
	}

	// Override device IDs and class codes — gc defaults differ from the
	// ADB-mode values (Google 0x18d1:0x4ee7).
	if err := ctx.setAttr(USB_GADGET_SUBPATH_VENDOR, "0x18d1"); err != nil {
		return fmt.Errorf("write idVendor: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_PRODUCT, "0x4ee7"); err != nil {
		return fmt.Errorf("write idProduct: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_BCD_USB, "0x0200"); err != nil {
		return fmt.Errorf("write bcdUSB: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_CLASS, "0x00"); err != nil {
		return fmt.Errorf("write bDeviceClass: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_SUBCLASS, "0x00"); err != nil {
		return fmt.Errorf("write bDeviceSubClass: %w", err)
	}
	if err := ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_PROTOCOL, "0x00"); err != nil {
		return fmt.Errorf("write bDeviceProtocol: %w", err)
	}

	// TODO move following lines to Base
	// Override strings.
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_SERIALNUMBER, "wifi-stick-miruku"); err != nil {
		return fmt.Errorf("write serialnumber: %w", err)
	}
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_MANUFACTURER, "Google"); err != nil {
		return fmt.Errorf("write manufacturer: %w", err)
	}
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_PRODUCT, "ADB Gadget"); err != nil {
		return fmt.Errorf("write product: %w", err)
	}

	// Mount functionfs for adbd.
	umountCmd := exec.Command("umount", this.ffs_path)
	_ = umountCmd.Run() // ignore error — not mounted yet
	if err := os.MkdirAll(this.ffs_path, 0755); err != nil {
		return fmt.Errorf("mkdir ffs path: %w", err)
	}
	if out, err := exec.Command("mount", "-t", "functionfs", "adb", this.ffs_path).CombinedOutput(); err != nil {
		return fmt.Errorf("mount functionfs failed: %w, output: %s", err, string(out))
	}

	// Kill any previous adbd, then start a fresh one.
	_ = exec.Command("killall", "adbd").Run()
	time.Sleep(200 * time.Millisecond)

	homedir, _ := os.UserHomeDir()
	if homedir == "" {
		homedir = "/root"
	}
	adbd_process = exec.Command("adbd", "-D")
	adbd_process.Dir = homedir
	if err := adbd_process.Start(); err != nil {
		return fmt.Errorf("start adbd: %w", err)
	}

	// Wait for adbd to write its ep0 descriptors; UDC won't bind without
	// them.  Like /sbin/mobian-usb-gadget, the UDC itself is bound later by
	// gc -e in enableGadget() — binding here too would make gc -e fail with
	// EBUSY and re-enumerate the host port twice.
	time.Sleep(100 * time.Millisecond)

	return nil
}

func SnapshotUsbGadgetAdb(instance string) *UsbGadgetAdb {
	adb := &UsbGadgetAdb{}
	instance = strings.TrimSpace(instance)

	adb.instance = instance
	adb._type = "ffs"
	adb.code = USB_GADGET_FUNCTION_CODE_ADB
	return adb
}

func NewUsbGadgetAdb(ffs_path string) *UsbGadgetAdb {
	adb := &UsbGadgetAdb{}

	adb.dev_name = "adb"
	adb.ffs_path = ffs_path

	adb._type = "ffs"
	adb.code = USB_GADGET_FUNCTION_CODE_ADB
	return adb
}
