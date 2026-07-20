package usb

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

func (this *UsbGadgetAdb) add(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	// We handle gc -a ffs inside effect() so we can control the order:
	// configfs base attrs first, then gc -a, then config/c.1 symlink.
	return nil
}

func (this *UsbGadgetAdb) effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	// Match the working script exactly, step by step.
	basepath := ctx.GetBasepath()

	// Step 1: Clean up old gadget state
	exec.Command("sh", "-c", fmt.Sprintf(
		"echo '' > '%s/UDC' 2>/dev/null; rm -rf '%s'/* 2>/dev/null; mkdir -p '%s'",
		basepath, basepath, basepath,
	)).Run()

	// Step 2: Device IDs
	ctx.setAttr(USB_GADGET_SUBPATH_VENDOR, "0x18d1")
	ctx.setAttr(USB_GADGET_SUBPATH_PRODUCT, "0x4ee7")
	ctx.setAttr(USB_GADGET_SUBPATH_BCD_USB, "0x0200")
	ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_CLASS, "0x00")
	ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_SUBCLASS, "0x00")
	ctx.setAttr(USB_GADGET_SUBPATH_DEVICE_PROTOCOL, "0x00")

	// Step 3: Strings — must be written BEFORE gc -a ffs (matching the script order).
	log.Printf("DEBUG adb: writing strings before gc -a, language=%q\n", ctx.getLanguage())
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_SERIALNUMBER, "wifi-stick-miruku"); err != nil {
		log.Printf("ERROR adb: setLanguageStrings serialnumber failed: %s\n", err)
	}
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_MANUFACTURER, "Google"); err != nil {
		log.Printf("ERROR adb: setLanguageStrings manufacturer failed: %s\n", err)
	}
	if err := ctx.setLanguageStrings(USB_GADGET_SUBPATH_STRINGS_PRODUCT, "ADB Gadget"); err != nil {
		log.Printf("ERROR adb: setLanguageStrings product failed: %s\n", err)
	}

	// Step 4: Create FFS function
	log.Printf("DEBUG adb: running gc -a ffs\n")
	if _, err := gc("-a", "ffs"); err != nil {
		return fmt.Errorf("gc -a ffs failed: %w", err)
	}

	// Verify strings after gc -a ffs
	log.Printf("DEBUG adb: verifying strings after gc -a...\n")
	for _, f := range []string{"serialnumber", "manufacturer", "product"} {
		data, err := os.ReadFile(filepath.Join(basepath, "strings/"+ctx.getLanguage()+"/"+f))
		if err != nil {
			log.Printf("DEBUG adb: verify read %s FAILED: %s\n", f, err)
		} else {
			log.Printf("DEBUG adb: verify read %s = %q\n", f, strings.TrimSpace(string(data)))
		}
	}

	// Step 5: Create config/c.1 with symlink (scripts uses c.1, not c1.1 from gc)
	os.MkdirAll(filepath.Join(basepath, "configs/c.1/strings/0x409"), 0755)
	os.WriteFile(filepath.Join(basepath, "configs/c.1/strings/0x409/configuration"), []byte("adb\n"), 0644)
	os.Symlink("../../functions/ffs.adb", filepath.Join(basepath, "configs/c.1/ffs.adb"))

	// Step 6: Mount functionfs
	exec.Command("umount", this.ffs_path).Run()
	os.MkdirAll(this.ffs_path, 0755)
	if out, err := exec.Command("mount", "-t", "functionfs", "adb", this.ffs_path).CombinedOutput(); err != nil {
		return fmt.Errorf("cannot mount functionfs: %w, output: %s", err, string(out))
	}

	// Step 7: Start adbd (kill old first, same as script)
	exec.Command("killall", "adbd").Run()
	time.Sleep(200 * time.Millisecond)

	homedir, _ := os.UserHomeDir()
	if homedir == "" {
		homedir = "/root"
	}
	adbd_process = exec.Command("adbd", "-D")
	adbd_process.Dir = homedir
	if err := adbd_process.Start(); err != nil {
		return fmt.Errorf("cannot start adbd: %w", err)
	}

	// Wait for adbd to write ep0 descriptors; UDC won't bind without them.
	time.Sleep(2 * time.Second)

	// Step 8: Write UDC, then gc -e handles the rest in enableGadget()
	udcScript := fmt.Sprintf("udc=$(ls /sys/class/udc | head -n 1); [ -n \"$udc\" ] && echo \"$udc\" > '%s/UDC'", basepath)
	if out, err := exec.Command("sh", "-c", udcScript).CombinedOutput(); err != nil {
		return fmt.Errorf("UDC assign failed: %w, output: %s", err, string(out))
	}

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
