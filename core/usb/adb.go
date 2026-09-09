package usb

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
)

var adbd_process *exec.Cmd = nil

// umount_ffs 循环卸载 functionfs 挂载点(可能叠加挂载,全部卸掉)。
func umount_ffs(ffs_path string) {
	for {
		if err := exec.Command("umount", ffs_path).Run(); err != nil {
			return // 没有更多挂载层
		}
	}
}

// CleanupRuntime daemon 退出时统一清理本 daemon 启动的运行时副作用:
// adbd 进程、dnsmasq、functionfs 挂载 —— 整体关闭不是跨模式耦合,
// 避免强杀残留(旧 adbd 占 ep0、叠加挂载)污染下次启动。
func CleanupRuntime() {
	kill_adbd()
	stop_dnsmasq_all()
	umount_ffs(adbd_ffs_path)
}

// 本 daemon 启动的 adbd 的 pid 文件 — kill_adbd 在进程句柄丢失(daemon
// 重启)时用它精确停掉自己启动的 adbd
const adbdPidFile = "/tmp/" + base.PROJECT_IDENT + "-adbd.pid"

// adbd_ffs_path 是 functionfs 挂载点(daemon 传入的 ffs 路径,清理用)
const adbd_ffs_path = "/dev/usb-ffs/adb"

type UsbGadgetAdb struct {
	dev_name string
	ffs_path string

	// USB 描述符字符串,由 --adb-serial-number 等参数传入
	serial_number string
	manufacturer  string
	product       string

	// envs 是传给 adbd 进程的额外环境变量(KEY=VALUE),由 --adb-env 传入,
	// 默认 ["TERM=xterm-256color"],保证 adb shell 有正确的终端类型。
	envs []string

	UsbGadgetFunctionBase
}

// add uses gc -a ffs, inherited from UsbGadgetFunctionBase, to create the
// FFS function.  gc handles the gadget directory, config, and function
// symlink; effect() writes the subpath overrides and performs the FFS-
// specific setup (mount, adbd).

// add 经 libusbgx 创建 ffs 函数并 link 进 config(instance "adb" →
// functions/ffs.adb)。FFS 函数的 ep0 描述符由 adbd 提供,这里只建
// 函数 + link;UDC 绑定在 Apply 的 Enable。
func (this *UsbGadgetAdb) add(ctx *UsbGadgetFunctionContext) error {
	this.set_instance("adb")

	if err := ctx.C.AddFfs("adb"); err != nil {
		return err
	}
	return nil
}

// effect 在 add 之后、绑定之前执行。写入 ID/class/strings 并做 FFS-
// 特定设置(mount functionfs、启动 adbd)。

func (this *UsbGadgetAdb) effect(ctx *UsbGadgetFunctionContext) error {
	instance := this.get_instance()
	if instance == "" {
		return fmt.Errorf("adb instance not set after add")
	}

	// Override device IDs and class codes — libusbgx 默认值(0x0000/0x0000)
	// 与 ADB 模式需要值不同(Google 0x18d1:0x4ee7)。
	// 设备级 class 0/0/0:与真实 Android 手机一致,分类在接口级。
	if err := ctx.C.SetGadgetAttrs(0x0200, 0x18d1, 0x4ee7, 0x0000, 0x00, 0x00, 0x00); err != nil {
		return err
	}
	if err := ctx.C.SetStrs(this.serial_number, this.manufacturer, this.product); err != nil {
		return err
	}

	// Mount functionfs for adbd.
	// functionfs 可能被叠加挂载(残留),循环卸干净再挂单层,
	// 否则旧 adbd 占用 ep0,新 adbd 起来即死、Enable 绑定失败。
	umount_ffs(this.ffs_path)
	if err := os.MkdirAll(this.ffs_path, 0755); err != nil {
		return fmt.Errorf("mkdir ffs path: %w", err)
	}
	if out, err := exec.Command("mount", "-t", "functionfs", "adb", this.ffs_path).CombinedOutput(); err != nil {
		return fmt.Errorf("mount functionfs failed: %w, output: %s", err, string(out))
	}

	// 停掉本模式上次启动的 adbd(ours only — 见 kill_adbd),再启新的。
	// 不碰 RNDIS 的 dnsmasq:它由 rndis effect/enable 自管
	// (effect 不做跨模式副作用,避免耦合)。
	kill_adbd()
	time.Sleep(200 * time.Millisecond)

	homedir, _ := os.UserHomeDir()
	if homedir == "" {
		homedir = "/root"
	}
	adbd_process = exec.Command("adbd", "-D")
	adbd_process.Dir = homedir
	adbd_process.Env = merge_envs(this.envs)
	if err := adbd_process.Start(); err != nil {
		adbd_process = nil
		return fmt.Errorf("start adbd: %w", err)
	}

	// Remember the pid for kill_adbd's fallback when the handle is lost to
	// a daemon restart.
	if err := os.WriteFile(adbdPidFile, []byte(strconv.Itoa(adbd_process.Process.Pid)+"\n"), 0644); err != nil {
		log.Printf("WARN: cannot write %s: %v\n", adbdPidFile, err)
	}

	// Wait for adbd to write its ep0 descriptors; UDC won't bind without
	// them.  Like /sbin/mobian-usb-gadget, the UDC itself is bound later by
	// gc -e in enable_gadget() — binding here too would make gc -e fail with
	// EBUSY and re-enumerate the host port twice.
	time.Sleep(100 * time.Millisecond)

	return nil
}

// merge_envs 返回继承自当前进程的环境,并把额外环境变量(KEY=VALUE)合并进去。
// 若父环境已有同名 KEY,用额外值替换(exec 按顺序取第一个匹配,直接 append
// 会被系统已有值覆盖)。
func merge_envs(extras []string) []string {
	env := append([]string{}, os.Environ()...)
	for _, extra := range extras {
		if extra == "" {
			continue
		}

		key := strings.SplitN(extra, "=", 2)[0] + "="
		replaced := false
		for i, kv := range env {
			if strings.HasPrefix(kv, key) {
				env[i] = extra
				replaced = true
			}
		}
		if !replaced {
			env = append(env, extra)
		}
	}
	return env
}

// kill_adbd stops the adbd started by THIS daemon — precisely, never a
// killall sweep: the stored process handle is used first; the pid file is
// the fallback when the handle was lost to a daemon restart.  Before
// signaling a pid-file pid, /proc/<pid>/comm is checked so a recycled pid
// can't take down an unrelated process.  An adbd this daemon didn't start
// (e.g. from /sbin/mobian-usb-gadget) is left alone.
func kill_adbd() {
	var proc *os.Process

	if adbd_process != nil && adbd_process.Process != nil {
		proc = adbd_process.Process
		adbd_process = nil
	} else if data, err := os.ReadFile(adbdPidFile); err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 {
			if comm := read_proc_comm(pid); comm != "adbd" {
				log.Printf("WARN: pid %d from %s is not adbd (comm=%q), skip kill\n", pid, adbdPidFile, comm)
			} else if p, err := os.FindProcess(pid); err == nil {
				proc = p
			}
		}
	} else {
		log.Printf("WARN: no adbd handle nor pid file %s, skip kill\n", adbdPidFile)
	}

	if proc != nil {
		stop_process(proc)
	}
	_ = os.Remove(adbdPidFile)
}

// read_proc_comm returns the comm name of pid, or "" if it doesn't exist.
func read_proc_comm(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// stop_process sends SIGTERM, waits up to 2s for exit, then SIGKILL.
func stop_process(proc *os.Process) {
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

func NewUsbGadgetAdb(ffs_path string, serial_number string, manufacturer string, product string, envs []string) *UsbGadgetAdb {
	adb := &UsbGadgetAdb{}

	adb.dev_name = "adb"
	adb.ffs_path = ffs_path
	adb.serial_number = serial_number
	adb.manufacturer = manufacturer
	adb.product = product
	adb.envs = envs

	adb._type = "ffs"
	adb.code = USB_GADGET_FUNCTION_CODE_ADB
	return adb
}
