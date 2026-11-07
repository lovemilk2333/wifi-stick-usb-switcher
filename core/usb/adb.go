package usb

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
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

// adbdReadyTimeout bounds the time spent waiting for adbd to publish its
// FunctionFS descriptors. Without this bound a failed adbd/lock setup leaves
// the daemon stuck in the mode-switch LED indication forever.
const adbdReadyTimeout = 2 * time.Second

// adbd_ffs_path 是 functionfs 挂载点(daemon 传入的 ffs 路径,清理用)
const adbd_ffs_path = "/dev/usb-ffs/adb"

type UsbGadgetAdb struct {
	dev_name string
	ffs_path string

	// USB 描述符字符串,由 --adb-serial-number 等参数传入
	serial_number string
	manufacturer  string
	product       string
	run_as_user   string
	run_as_group  string

	// envs 是传给 adbd 进程的额外环境变量(KEY=VALUE),由 --adb-env 传入,
	// 默认 ["TERM=xterm-256color"],保证 adb shell 有正确的终端类型。
	envs []string

	UsbGadgetFunctionBase
}

// GetName 返回用户可见的 gadget 名(`adb`)。内核函数类型是 `ffs`
// (base._type,决定 configfs 目录 functions/ffs.adb),两者不同。
func (this *UsbGadgetAdb) GetName() string {
	return "adb"
}

// add 创建 ffs 函数并 link 进 config(instance "adb" → functions/ffs.adb):
// ep0 描述符由 adbd 提供,这里只建函数 + link;UDC 绑定在 Apply 的 Enable。
func (this *UsbGadgetAdb) add(ctx *UsbGadgetFunctionContext) error {
	this.set_instance("adb")

	if err := ctx.C.AddFfs("adb"); err != nil {
		return err
	}
	return nil
}

// effect 写 gadget 属性/strings,挂载 functionfs 并启动 adbd(须在
// add 之后、UDC 绑定之前)。
func (this *UsbGadgetAdb) effect(ctx *UsbGadgetFunctionContext) error {
	instance := this.get_instance()
	if instance == "" {
		return fmt.Errorf("adb instance not set after add")
	}

	// class 0/0/0 + Google 0x18d1:0x4ee7,对齐真实 Android 手机枚举
	if err := ctx.C.SetGadgetAttrs(0x0200, 0x18d1, 0x4ee7, 0x0000, 0x00, 0x00, 0x00); err != nil {
		return err
	}
	if err := ctx.C.SetStrs(this.serial_number, this.manufacturer, this.product); err != nil {
		return err
	}

	umount_ffs(this.ffs_path)
	if err := os.MkdirAll(this.ffs_path, 0755); err != nil {
		return fmt.Errorf("mkdir ffs path: %w", err)
	}
	if out, err := exec.Command("mount", "-t", "functionfs", "adb", this.ffs_path).CombinedOutput(); err != nil {
		return fmt.Errorf("mount functionfs failed: %w, output: %s", err, string(out))
	}

	kill_adbd()

	adbd_path, err := exec.LookPath("adbd")
	if err != nil {
		return fmt.Errorf("find adbd executable: %w", err)
	}

	adbd_process = exec.Command(adbd_path, "-D")
	adbd_process.Stdout = os.Stdout
	adbd_process.Stderr = os.Stderr

	env_extras := []string{}
	if this.run_as_user != "" || this.run_as_group != "" {
		identity, err := resolve_adbd_identity(this.run_as_user, this.run_as_group)
		if err != nil {
			adbd_process = nil
			return fmt.Errorf("resolve adbd identity: %w", err)
		}
		adbd_process.Dir = identity.home
		env_extras = identity.env()
		if err := os.Chmod(this.ffs_path, 0770); err != nil {
			adbd_process = nil
			return fmt.Errorf("set FunctionFS permissions for adbd: %w", err)
		}
		if err := os.Chown(this.ffs_path, int(identity.uid), int(identity.gid)); err != nil {
			adbd_process = nil
			return fmt.Errorf("set FunctionFS owner for adbd: %w", err)
		}
		adbd_process.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
			Uid:    identity.uid,
			Gid:    identity.gid,
			Groups: identity.groups,
		}}
		log.Printf("INFO: launching adbd path=%s dir=%s uid=%d gid=%d groups=%v (user=%s group=%s)\n",
			adbd_path, identity.home, identity.uid, identity.gid, identity.groups, this.run_as_user, this.run_as_group)
	} else {
		adbd_process.Dir = adbdHomeDir("")
		// Keep HOME/USER/LOGNAME consistent with the working directory. For
		// root this is /root, matching a normal root login.
		env_extras = []string{"HOME=" + adbd_process.Dir}
		if account, err := user.Current(); err == nil {
			env_extras = append(env_extras, "USER="+account.Username, "LOGNAME="+account.Username)
		}
		log.Printf("INFO: launching adbd path=%s dir=%s uid=%d gid=%d\n",
			adbd_path, adbd_process.Dir, os.Geteuid(), os.Getegid())
	}
	adbd_process.Env = merge_envs(append(env_extras, this.envs...))
	dir := adbd_process.Dir
	if err := adbd_process.Start(); err != nil {
		adbd_process = nil
		return fmt.Errorf("start adbd (path=%s dir=%s): %w", adbd_path, dir, err)
	}
	log.Printf("INFO: adbd started (pid %d), waiting up to %s for FunctionFS descriptors\n", adbd_process.Process.Pid, adbdReadyTimeout)

	// pid 文件供 kill_adbd 在句柄丢失(daemon 重启)时兜底
	if err := os.WriteFile(adbdPidFile, []byte(strconv.Itoa(adbd_process.Process.Pid)+"\n"), 0644); err != nil {
		log.Printf("WARN: cannot write %s: %v\n", adbdPidFile, err)
	}

	// adbd writes its descriptors to ep0; ep1 appears once the FunctionFS
	// endpoint set has been published. Poll instead of sleeping blindly so a
	// missing lock, permission failure, or early adbd exit is reported quickly.
	deadline := time.Now().Add(adbdReadyTimeout)
	ep1 := filepath.Join(this.ffs_path, "ep1")
	probe := time.NewTicker(20 * time.Millisecond)
	defer probe.Stop()
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ep1); err == nil {
			log.Printf("INFO: adbd FunctionFS descriptors ready (pid %d)\n", adbd_process.Process.Pid)
			return nil
		}
		if err := adbd_process.Process.Signal(syscall.Signal(0)); err != nil {
			kill_adbd()
			return fmt.Errorf("adbd exited before FunctionFS descriptors were ready: %w", err)
		}
		select {
		case <-probe.C:
		}
	}
	pid := adbd_process.Process.Pid
	log.Printf("WARN: adbd FunctionFS descriptors not ready after %s (pid %d); stopping adbd\n", adbdReadyTimeout, pid)
	kill_adbd()
	return fmt.Errorf("adbd FunctionFS descriptors timeout after %s", adbdReadyTimeout)
}

// adbdHomeDir returns the working directory for adbd. Root keeps /root so an
// interactive root shell starts where root expects; a --user run uses that
// user's home instead of inheriting /root from the daemon.
func adbdHomeDir(run_as_user string) string {
	if run_as_user != "" {
		if account, err := user.Lookup(run_as_user); err == nil && account.HomeDir != "" {
			return account.HomeDir
		}
	}
	if account, err := user.Current(); err == nil && account.HomeDir != "" {
		return account.HomeDir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if os.Geteuid() == 0 {
			return "/root"
		}
		return "/"
	}

	return home
}

// adbdIdentity is the complete runtime identity for adbd: UID, primary GID,
// supplementary groups, and the account used for HOME/USER/LOGNAME.
type adbdIdentity struct {
	uid    uint32
	gid    uint32
	groups []uint32
	user   string
	home   string
}

// env returns the identity-related environment for the adbd child. Explicit
// --adb-env entries are merged afterwards and take precedence.
func (this *adbdIdentity) env() []string {
	env := make([]string, 0, 3)
	if this.home != "" {
		env = append(env, "HOME="+this.home)
	}
	if this.user != "" {
		env = append(env, "USER="+this.user, "LOGNAME="+this.user)
	}
	return env
}

// resolve_adbd_identity resolves the target user/group and all supplementary
// groups, so adbd receives the same permissions as an interactive login of
// that user, not just uid/gid.
func resolve_adbd_identity(user_name, group_name string) (*adbdIdentity, error) {
	identity := &adbdIdentity{
		uid:  uint32(os.Geteuid()),
		gid:  uint32(os.Getegid()),
		home: adbdHomeDir(user_name),
	}

	var account *user.User
	var err error
	if user_name != "" {
		account, err = user.Lookup(user_name)
		if err != nil {
			return nil, fmt.Errorf("lookup user %q: %w", user_name, err)
		}
	} else {
		// --group without --user: keep the daemon UID/home but still resolve
		// the current account for supplementary groups.
		account, _ = user.Current()
	}

	if account != nil {
		if account.Uid != "" {
			parsed_uid, err := strconv.ParseUint(account.Uid, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid uid for user %q: %w", account.Username, err)
			}
			identity.uid = uint32(parsed_uid)
		}
		if account.Gid != "" {
			parsed_gid, err := strconv.ParseUint(account.Gid, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid primary gid for user %q: %w", account.Username, err)
			}
			identity.gid = uint32(parsed_gid)
		}
		identity.user = account.Username
		if account.HomeDir != "" {
			identity.home = account.HomeDir
		}

		group_ids, err := account.GroupIds()
		if err != nil {
			return nil, fmt.Errorf("lookup supplementary groups for user %q: %w", account.Username, err)
		}
		for _, group_id := range group_ids {
			parsed_gid, err := strconv.ParseUint(group_id, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid supplementary gid %q for user %q: %w", group_id, account.Username, err)
			}
			identity.groups = append_group(identity.groups, uint32(parsed_gid))
		}
	}

	if group_name != "" {
		group, err := user.LookupGroup(group_name)
		if err != nil {
			return nil, fmt.Errorf("lookup group %q: %w", group_name, err)
		}
		parsed_gid, err := strconv.ParseUint(group.Gid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid gid for group %q: %w", group_name, err)
		}
		identity.gid = uint32(parsed_gid)
	}

	// Always include the primary GID. A non-empty group list also makes exec
	// call setgroups(2), which clears supplementary groups inherited from the
	// root daemon instead of keeping root-only access.
	identity.groups = append_group(identity.groups, identity.gid)
	return identity, nil
}

func append_group(groups []uint32, gid uint32) []uint32 {
	for _, existing := range groups {
		if existing == gid {
			return groups
		}
	}
	return append(groups, gid)
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

// kill_adbd 只停本 daemon 启动的 adbd(句柄优先,pid 文件兜底;pid 复用
// 用 /proc/<pid>/comm 校验,不 killall 误伤他人启动的 adbd)。
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

// stop_process 发 SIGTERM,最多等 2s,超时 SIGKILL。
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

func NewUsbGadgetAdb(ffs_path string, serial_number string, manufacturer string, product string, envs []string, run_as_user string, run_as_group string) *UsbGadgetAdb {
	adb := &UsbGadgetAdb{}

	adb.dev_name = "adb"
	adb.ffs_path = ffs_path
	adb.serial_number = serial_number
	adb.manufacturer = manufacturer
	adb.product = product
	adb.envs = envs
	adb.run_as_user = run_as_user
	adb.run_as_group = run_as_group

	adb._type = "ffs"
	adb.code = USB_GADGET_FUNCTION_CODE_ADB
	return adb
}
