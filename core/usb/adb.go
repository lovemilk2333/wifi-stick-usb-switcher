package usb

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
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
	output, err := gc("-a", "ffs")
	if err != nil {
		if len(output) == 0 {
			output = "(no output)"
		}

		log.Printf("WARN: cannot add UsbGadgetAdb (type: %s, err: %s): %s\n", "ffs", err, output)
		return err
	}

	return nil
}

func (this *UsbGadgetAdb) effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	// writer := ctx.getSubpathWriter()

	// TODO
	// mkdir -p <ffs_path>
	// mount -t functionfs adb <ffs_path>
	// set `pwd`` to `~` ($HOME), `/root` if $HOME is not set
	// run `adbd -D` background if adbd is not running
	// # (hack) wait adbd setup
	// sleep 1

	stat, err := os.Stat(this.ffs_path)
	if err != nil || !stat.IsDir() {
		err := os.MkdirAll(this.ffs_path, 0755)
		if err != nil {
			return fmt.Errorf("cannot create ffs_path `%s`: %w", this.ffs_path, err)
		}
	}

	if adbd_process != nil {
		if err := adbd_process.Process.Kill(); err != nil {
			return fmt.Errorf("cannot kill old adbd: %w", err)
		}

		_, err = adbd_process.Process.Wait()
		if err != nil {
			if !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("wait old adbd failed: %w", err)
			}
		}
	}

	process := exec.Command("mount", "-t", "functionfs", "adb", this.ffs_path)
	if output, err := process.CombinedOutput(); err != nil {
		return fmt.Errorf("cannot mount functionfs (adb): error: %w, output: %s", err, string(output))
	}

	homedir, err := os.UserHomeDir()
	if err != nil {
		homedir = "/root"
	}

	adbd_process = exec.Command("adbd", "-D")
	adbd_process.Dir = homedir
	err = adbd_process.Start()
	if err != nil {
		return fmt.Errorf("cannot start adbd: %w", err)
	}

	return nil
}

func SnapshotUsbGadgetAdb(instance string) *UsbGadgetAdb {
	adb := &UsbGadgetAdb{}
	instance = strings.TrimSpace(instance)

	adb.instance = instance
	adb._type = "adb"
	adb.code = USB_GADGET_FUNCTION_CODE_ADB
	return adb
}

func NewUsbGadgetAdb(ffs_path string) *UsbGadgetAdb {
	adb := &UsbGadgetAdb{}

	adb.dev_name = "adb"
	adb.ffs_path = ffs_path

	adb._type = "adb"
	adb.code = USB_GADGET_FUNCTION_CODE_ADB
	return adb
}
