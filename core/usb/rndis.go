package usb

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

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

// dnsmasqPidFile 返回本实例的 dnsmasq pid 文件路径 — 随接口名动态,
// 接口名由 --rndis-ifname 配置(默认 usb0)。
func (this *UsbGadgetRndis) dnsmasqPidFile() string {
	return "/tmp/dnsmasq-" + this.ifname + ".pid"
}

// add 手工创建 rndis 函数并 link 进 config —— 不用 gc -a:这台设备上的
// gc -a 创建函数后立即绑定 UDC,config link 建立即锁定全部函数属性,
// dev_addr/host_addr 写入永远 EBUSY,usb0 接口 MAC 只能是 gc 随机值。
// configfs 原生 mkdir 的顺序:函数目录 → 写 MAC(link 前可写)→ link;
// UDC 绑定由 enableGadget 的 echo 完成,绑定前的 configfs 完全可写。
// 实测验证:mkdir → 写 dev_addr/host_addr → ln → echo UDC,usb0 接口
// MAC 即为写入的 dev_addr。
func (this *UsbGadgetRndis) add(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	this.setInstance("rndis.1")
	instance := this.getInstance()

	funcSub := "functions/" + this._type + "." + instance
	funcDir := filepath.Join(ctx.Basepath, funcSub)
	if err := os.MkdirAll(funcDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", funcSub, err)
	}

	// dev_addr / host_addr 必须在 link 之前写入:link 建立后这两个
	// 属性被 configfs 锁定(EBUSY,实测)。
	if err := ctx.WriteSubpath(base.Subpath(funcSub+"/dev_addr"), true, []byte(this.dev_addr+"\n")); err != nil {
		return fmt.Errorf("write dev_addr: %w", err)
	}
	if err := ctx.WriteSubpath(base.Subpath(funcSub+"/host_addr"), true, []byte(this.host_addr+"\n")); err != nil {
		return fmt.Errorf("write host_addr: %w", err)
	}
	if this.ifname != "" {
		// 内核 (≥5.12, gether_set_ifname) 要求 ifname 属性写成接口
		// PATTERN —— 必须恰好含一个 `%d`("usb%d"),写具体名字("usb0")
		// 会返回 -EINVAL(实测 log 里的 `write ifname: invalid
		// argument`)。具体接口名由内核在绑定(enableGadget)时按空闲号
		// 分配,enable() 里读回属性解析真实名字。
		if err := ctx.WriteSubpath(base.Subpath(funcSub+"/ifname"), true, []byte(rndisIfnamePattern(this.ifname)+"\n")); err != nil {
			return fmt.Errorf("write ifname: %w", err)
		}
	}
	if this.qmult != "" {
		if err := ctx.WriteSubpath(base.Subpath(funcSub+"/qmult"), true, []byte(this.qmult+"\n")); err != nil {
			return fmt.Errorf("write qmult: %w", err)
		}
	}

	linkPath := filepath.Join(ctx.Basepath, "configs/c1.1", instance)
	if err := os.Symlink(funcDir, linkPath); err != nil {
		return fmt.Errorf("link %s -> %s: %w", linkPath, funcSub, err)
	}
	return nil
}

// rndisIfnamePattern 把具体接口名("usb0")转换成内核 ifname 属性要求的
// 模式("usb%d")—— 内核按该模式在绑定后分配下一个空闲接口名。
func rndisIfnamePattern(ifname string) string {
	return strings.TrimRight(ifname, "0123456789") + "%d"
}

// resolveIfname 读回 ifname 属性得到绑定后内核分配的真实接口名
// (绑定前属性回显的是 "usb%d" 模式,绑定后回显具体名字)。读不到时
// 返回空串,由调用方继续用配置名。
func (this *UsbGadgetRndis) resolveIfname(ctx UsbGadgetContext) string {
	instance := this.getInstance()
	if instance == "" {
		return ""
	}
	data, err := ctx.ReadSubpath(base.Subpath("functions/rndis."+instance+"/ifname"), false)
	if err != nil {
		return ""
	}
	name := string(data)
	if strings.HasSuffix(name, "%d") { // 绑定前:仍是模式,不是真实名字
		return ""
	}
	return name
}

// effect 在 add 之后、绑定(enableGadget)之前执行 —— configfs 未锁定,
// gadget 级属性可写;MAC 已在 add 里写入,这里只写 ID/class/strings
// 并清场。
func (this *UsbGadgetRndis) effect(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	instance := this.getInstance()
	if instance == "" {
		return fmt.Errorf("rndis instance not set after add")
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

	// 清场:离开 ADB 模式后 orphaned adbd 还在 poll /dev/usb-ffs/adb,
	// 停掉本 daemon 启动的它(句柄/pid 文件校验,不是无差别 killall);
	// 上一轮 RNDIS 的 dnsmasq(接口已随 gadget 消失)也一并停掉。
	killAdbd()
	stopDnsmasqAll()

	// NOTE: 不要在这里绑定 UDC。绑定只有一次,在 enableGadget 的
	// `echo <udc> > UDC` —— 绑定前的 configfs 是可写的。
	return nil
}

// enable 在 gadget 绑定(enableGadget)之后调用 — 此时内核才创建 usb0
// 接口。RNDIS 自己管理网络:让 NM 让开、配置 IP、启动 dnsmasq 供 USB
// host 获取地址。
func (this *UsbGadgetRndis) enable(ctx UsbGadgetContext, gc func(args ...string) (string, error)) error {
	ifname := this.ifname

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := net.InterfaceByName(ifname); err == nil {
			break
		}
		// ifname 属性写成的是 "usb%d" 模式,绑定后内核才分配具体名字
		// (usb0,若 usb0 被占用则为 usb1);从属性读回真实名字。
		if resolved := this.resolveIfname(ctx); resolved != "" && resolved != ifname {
			ifname = resolved
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rndis interface `%s` not found after enable", ifname)
		}
		time.Sleep(200 * time.Millisecond)
	}
	this.ifname = ifname // 让 dnsmasq pid 文件等后续逻辑用真实名字

	this.unmanageFromNetworkManager(ifname)

	// NM 释放接口是异步的,等它交还后再配 IP,否则地址会被 NM 收尾清掉
	time.Sleep(500 * time.Millisecond)

	if out, err := exec.Command("ip", "link", "set", ifname, "up").CombinedOutput(); err != nil {
		log.Printf("WARN: `ip link set %s up`: %v, output: %s\n", ifname, err, string(out))
	}

	ipSpec := this.ip_addr.String()
	for attempt := 1; ; attempt++ {
		out, err := exec.Command("ip", "addr", "add", ipSpec, "dev", ifname).CombinedOutput()
		if err == nil {
			break
		}
		// 地址已存在(重复 apply)不算错误
		if hasIfaceAddr(ifname, ipSpec) {
			break
		}
		if attempt >= 3 {
			log.Printf("WARN: `ip addr add %s dev %s`: %v, output: %s\n", ipSpec, ifname, err, string(out))
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	this.startDnsmasq()
	return nil
}

// hasIfaceAddr 检查接口上是否已有指定地址(如 `10.22.33.1/24`)。
func hasIfaceAddr(ifname, ipSpec string) bool {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if addr.String() == ipSpec {
			return true
		}
	}
	return false
}

// unmanageFromNetworkManager 让 NetworkManager 不接管 ifname:写
// unmanaged 配置(易失 /run)并向 NM 发 SIGHUP 重载 — 不依赖 nmcli
// 工具;NM 未安装/未运行时两步都是无害 no-op。NM 接管后会把 usb0
// 当 DHCP client(method=auto)用,永远拿不到地址,超时后清掉我们配
// 的 IP — 必须让开。
func (this *UsbGadgetRndis) unmanageFromNetworkManager(ifname string) {
	confPath := filepath.Join("/run/NetworkManager/conf.d", "wifi-stick-usb.conf")
	content := "[device]\nmatch-device=interface-name:" + ifname + "\nmanaged=0\n"
	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		log.Printf("WARN: write %s: %v\n", confPath, err)
		return
	}
	// SIGHUP 让 NM 立即重载配置(标准做法,不需要 nmcli)
	if out, err := exec.Command("sh", "-c", "kill -HUP $(pgrep -x NetworkManager) 2>/dev/null").CombinedOutput(); err != nil {
		log.Printf("WARN: reload NetworkManager: %v, output: %s\n", err, string(out))
	}
}

// startDnsmasq 在接口上启动 dnsmasq DHCP 服务器,供 USB host 获取地址。
// 池从本机 IP 之后到子网最后一个地址;--port=0 关闭 DNS,避免与系统
// dnsmasq 冲突(系统实例已禁用,本实例独占 67 端口)。
func (this *UsbGadgetRndis) startDnsmasq() {
	pidFile := this.dnsmasqPidFile()
	if pidNum := readDnsmasqPid(pidFile); pidNum > 0 {
		return // 已在运行,pid 文件校验过 cmdline,不会误判
	}

	router := this.ip_addr.Masked().Addr()
	last, ok := subnetLast(this.ip_addr)
	if !ok {
		log.Printf("WARN: cannot derive dhcp pool from `%s`, skip dnsmasq\n", this.ip_addr)
		return
	}
	start := router.Next()
	end := last.Prev()
	if !start.IsValid() || start.Compare(end) > 0 {
		log.Printf("WARN: invalid dhcp pool %s-%s for `%s`, skip dnsmasq\n", start, end, this.ip_addr)
		return
	}

	args := []string{
		"--interface=" + this.ifname,
		"--bind-interfaces",
		fmt.Sprintf("--dhcp-range=%s,%s,12h", start, end),
		"--dhcp-option=option:router," + router.String(),
		"--port=0", // 不提供 DNS
		"--no-resolv",
		"--no-hosts",
		"--pid-file=" + pidFile,
	}
	if out, err := exec.Command("dnsmasq", args...).CombinedOutput(); err != nil {
		log.Printf("WARN: cannot start dnsmasq: %v, output: %s\n", err, string(out))
	}
}

// stopDnsmasqAll 停掉由本 daemon 启动的所有 dnsmasq 实例(pid 文件 +
// cmdline 校验,pid 复用也不会误杀无关进程)。本 daemon 只会为 RNDIS
// 接口启动 dnsmasq,遍历 /tmp/dnsmasq-*.pid 覆盖 ifname 动态化后的
// 全部路径。
func stopDnsmasqAll() {
	matches, err := filepath.Glob("/tmp/dnsmasq-*.pid")
	if err != nil {
		return
	}
	for _, pidFile := range matches {
		pidNum := readDnsmasqPid(pidFile)
		if pidNum <= 0 {
			continue
		}
		if err := syscall.Kill(pidNum, syscall.SIGTERM); err != nil {
			log.Printf("WARN: cannot stop dnsmasq (pid %d): %v\n", pidNum, err)
			continue
		}
		_ = os.Remove(pidFile)
	}
}

// readDnsmasqPid 返回 pid 文件中指向的、由我们启动的 dnsmasq 实例的
// pid;不存在或 pid 已被其他进程复用(pid 文件过期)时返回 0。校验方式:
// /proc/<pid>/cmdline 必须包含该 pid 文件对应的 --pid-file 参数 — 这样
// 绝不会误杀系统 dnsmasq 或无关进程。
func readDnsmasqPid(pidFile string) int {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}

	pidNum, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pidNum <= 0 {
		return 0
	}

	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pidNum))
	if err != nil || !strings.Contains(string(cmdline), "--pid-file="+pidFile) {
		return 0
	}
	return pidNum
}

// subnetLast 返回 prefix 子网的最后一个地址(广播地址)。
func subnetLast(prefix netip.Prefix) (netip.Addr, bool) {
	if !prefix.IsValid() || prefix.Bits() >= 32 || !prefix.Addr().Is4() {
		return netip.Addr{}, false
	}

	a := prefix.Masked().Addr().As4()
	network := binary.BigEndian.Uint32(a[:])
	mask := uint32(0xffffffff) << (32 - prefix.Bits())
	broadcast := network | ^mask

	var last [4]byte
	binary.BigEndian.PutUint32(last[:], broadcast)
	return netip.AddrFrom4(last), true
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
