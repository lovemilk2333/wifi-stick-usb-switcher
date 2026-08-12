package usb

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// type Usb
// TODO

var adbd_process *exec.Cmd = nil

// 本 daemon 启动的 adbd 的 pid 文件 — killAdbd 在进程句柄丢失(daemon
// 重启)时用它精确停掉自己启动的 adbd
const adbdPidFile = "/tmp/wifi-stick-usb-switcher-adbd.pid"

type UsbGadgetAdb struct {
	dev_name string
	ffs_path string

	UsbGadgetFunctionBase
}

// add uses gc -a ffs, inherited from UsbGadgetFunctionBase, to create the
// FFS function.  gc handles the gadget directory, config, and function
// symlink; effect() writes the subpath overrides and performs the FFS-
// specific setup (mount, adbd).

// add 手工创建 ffs 函数并 link 进 config —— 同 rndis,不用 gc -a
// (gc -a 创建即绑定,后续属性全部锁定)。FFS 函数的 ep0 描述符由
// adbd 提供,这里只需要目录 + link;UDC 绑定在 enableGadget。
func (this *UsbGadgetAdb) add(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	this.setInstance("adb")
	instance := this.getInstance()

	funcSub := "functions/ffs.adb"
	funcDir := filepath.Join(ctx.Basepath, funcSub)
	if err := os.MkdirAll(funcDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", funcSub, err)
	}

	linkPath := filepath.Join(ctx.Basepath, "configs/c1.1", instance)
	if err := os.Symlink(funcDir, linkPath); err != nil {
		return fmt.Errorf("link %s -> %s: %w", linkPath, funcSub, err)
	}
	return nil
}

// effect 在 add 之后、绑定之前执行。写入 ID/class/strings 并做 FFS-
// 特定设置(mount functionfs、启动 adbd)。

func (this *UsbGadgetAdb) effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	instance := this.getInstance()
	if instance == "" {
		return fmt.Errorf("adb instance not set after add")
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

	// Stop the adbd started by a previous ADB switch (ours only — see
	// killAdbd), then start a fresh one.  Also stop the RNDIS dnsmasq:
	// ADB 模式下 usb0 已随 gadget 消失,DHCP 不再需要。
	killAdbd()
	stopDnsmasqAll()
	time.Sleep(200 * time.Millisecond)

	homedir, _ := os.UserHomeDir()
	if homedir == "" {
		homedir = "/root"
	}
	adbd_process = exec.Command("adbd", "-D")
	adbd_process.Dir = homedir
	if err := adbd_process.Start(); err != nil {
		adbd_process = nil
		return fmt.Errorf("start adbd: %w", err)
	}

	// Remember the pid for killAdbd's fallback when the handle is lost to
	// a daemon restart.
	if err := os.WriteFile(adbdPidFile, []byte(strconv.Itoa(adbd_process.Process.Pid)+"\n"), 0644); err != nil {
		log.Printf("WARN: cannot write %s: %v\n", adbdPidFile, err)
	}

	// Wait for adbd to write its ep0 descriptors; UDC won't bind without
	// them.  Like /sbin/mobian-usb-gadget, the UDC itself is bound later by
	// gc -e in enableGadget() — binding here too would make gc -e fail with
	// EBUSY and re-enumerate the host port twice.
	time.Sleep(100 * time.Millisecond)

	return nil
}

// killAdbd stops the adbd started by THIS daemon — precisely, never a
// killall sweep: the stored process handle is used first; the pid file is
// the fallback when the handle was lost to a daemon restart.  Before
// signaling a pid-file pid, /proc/<pid>/comm is checked so a recycled pid
// can't take down an unrelated process.  An adbd this daemon didn't start
// (e.g. from /sbin/mobian-usb-gadget) is left alone.
func killAdbd() {
	var proc *os.Process

	if adbd_process != nil && adbd_process.Process != nil {
		proc = adbd_process.Process
		adbd_process = nil
	} else if data, err := os.ReadFile(adbdPidFile); err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 {
			if comm := readProcComm(pid); comm != "adbd" {
				log.Printf("WARN: pid %d from %s is not adbd (comm=%q), skip kill\n", pid, adbdPidFile, comm)
			} else if p, err := os.FindProcess(pid); err == nil {
				proc = p
			}
		}
	} else {
		log.Printf("WARN: no adbd handle nor pid file %s, skip kill\n", adbdPidFile)
	}

	if proc != nil {
		stopProcess(proc)
	}
	_ = os.Remove(adbdPidFile)
}

// readProcComm returns the comm name of pid, or "" if it doesn't exist.
func readProcComm(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// stopProcess sends SIGTERM, waits up to 2s for exit, then SIGKILL.
func stopProcess(proc *os.Process) {
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		log.Printf("WARN: cannot stop process %d: %v\n", proc.Pid, err)
		return
	}

	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = proc.Kill()
		<-done
	}
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
