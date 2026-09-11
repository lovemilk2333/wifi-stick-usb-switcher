package usb

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"math/bits"
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
	client_ip         netip.Addr    // 从模式 IP 模板(--rndis-client-ip):0 字节取上游网段字节;主机字节用于无 DHCP 时的 ICS 静态探测(192.168.137.xx)
	client_timeout    time.Duration // 从模式总超时:等网卡出现 + DHCP 探测(--rndis-client-timeout)
	ics_timeout       time.Duration // ICS 静态网关探测超时(--rndis-ics-timeout)

	dev_addr     string
	host_addr    string
	ifname       string
	qmult        string
	dnsmasq_args []string // 用户通过 --dnsmasq-arg 传入的自定义 dnsmasq 参数

	// USB 描述符字符串,由 --rndis-serial-number 等参数传入
	serial_number string
	manufacturer  string
	product       string

	UsbGadgetFunctionBase
}

// dnsmasq_pid_file 返回本实例的 dnsmasq pid 文件路径 — 随接口名动态,
// 接口名由 --rndis-ifname 配置(默认 usb0)。
func (this *UsbGadgetRndis) dnsmasq_pid_file() string {
	return "/tmp/dnsmasq-" + this.ifname + ".pid"
}

// add 经 libusbgx 创建 rndis 函数并 link 进 config
// (gadget.Ctx.AddRndis:create_function → MAC/qmult(usbg_f_net_*)→
// ifname 直写(只读 attr)→ add_config_function)。顺序由 libusbgx 保证:
// 属性写入在绑定前,configfs 未锁定,不会 EBUSY。
func (this *UsbGadgetRndis) add(ctx *UsbGadgetFunctionContext) error {
	this.set_instance("rndis.1")
	instance := this.get_instance()

	var qmult uint
	if this.qmult != "" {
		parsed, err := strconv.ParseUint(this.qmult, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid qmult `%s`: %w", this.qmult, err)
		}
		qmult = uint(parsed)
	}

	if err := ctx.C.AddRndis(instance, this.dev_addr, this.host_addr, this.ifname, qmult); err != nil {
		return err
	}
	return nil
}

// resolve_ifname 读回 ifname 属性得到绑定后内核分配的真实接口名
// (绑定前属性回显的是 "usb%d" 模式,绑定后回显具体名字)。读不到时
// 返回空串,由调用方继续用配置名。configfs 只读,直读文件即可。
func (this *UsbGadgetRndis) resolve_ifname(ctx *UsbGadgetFunctionContext) string {
	instance := this.get_instance()
	if instance == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(ctx.ConfigFs, "functions", "rndis."+instance, "ifname"))
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	if strings.HasSuffix(name, "%d") { // 绑定前:仍是模式,不是真实名字
		return ""
	}
	return name
}

// effect 在 add 之后、绑定(Enable)之前写 gadget 属性/strings/os_desc
// (绑定前 configfs 未锁定);MAC 已在 add 里写入。
func (this *UsbGadgetRndis) effect(ctx *UsbGadgetFunctionContext) error {
	instance := this.get_instance()
	if instance == "" {
		return fmt.Errorf("rndis instance not set after add")
	}

	// 18d1:4ee4 + bcdDevice 0x0223 对齐真实 Android 手机;设备级
	// class 0/0/0(此前 EF/02/01 在 Win7 开 ICS 后 RNDIS 驱动蓝屏)。
	if err := ctx.C.SetGadgetAttrs(0x0200, 0x18d1, 0x4ee4, 0x0223, 0x00, 0x00, 0x00); err != nil {
		return err
	}
	if err := ctx.C.SetStrs(this.serial_number, this.manufacturer, this.product); err != nil {
		return err
	}

	// MaxPower/配置名对齐手机枚举(120 会被部分 host 视为低功耗设备)
	if err := ctx.C.SetConfigName("RNDIS"); err != nil {
		return err
	}
	if err := ctx.C.WriteConfigMaxPower(500); err != nil {
		return err
	}

	// MS OS Descriptor (Extended Compat ID):Windows 的 usb8023 驱动靠
	// compatible_id="RNDIS" 自动匹配;缺失时设备管理器显示"其他设备"(代码 28)。
	if err := ctx.C.SetOsDesc(0xcd, "MSFT100"); err != nil {
		return err
	}
	if err := ctx.C.SetFunctionOsDesc(instance, "rndis", "RNDIS", "5162001"); err != nil {
		return err
	}

	// 只停本模式自己的 dnsmasq(切走期间空转,回来时先杀再启);
	// adbd 归 ADB 模式自管,不在此碰(避免跨模式耦合)。
	stop_dnsmasq_all()

	// NOTE: 不要在这里绑定 UDC。绑定只有一次,在 Apply 的 Enable。
	return nil
}

// enable 在 gadget 绑定(enable_gadget)之后调用 — 此时内核才创建 usb0
// 接口。RNDIS 自己管理网络:让 NM 让开、接口 up,然后按 submode 分派:
// 0(网关模式)配 --rndis-ip 并起 dnsmasq;1(从模式)从上游 DHCP 拿
// 网段配客户端 IP 与默认路由。接口每次随 gadget 重建都是干净的,切换
// submode 无需清地址/路由。
func (this *UsbGadgetRndis) enable(ctx *UsbGadgetFunctionContext) error {
	ifname := this.ifname

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := net.InterfaceByName(ifname); err == nil {
			break
		}
		// ifname 属性写成的是 "usb%d" 模式,绑定后内核才分配具体名字
		// (usb0,若 usb0 被占用则为 usb1);从属性读回真实名字。
		if resolved := this.resolve_ifname(ctx); resolved != "" && resolved != ifname {
			ifname = resolved
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rndis interface `%s` not found after enable", ifname)
		}
		time.Sleep(50 * time.Millisecond)
	}
	this.ifname = ifname // 让 dnsmasq pid 文件等后续逻辑用真实名字

	this.unmanage_from_network_manager(ifname)
	// unmanage_from_network_manager 内部已等 NM 标记 unmanaged(并兜底),
	// 接口归属确定,直接配 IP

	if out, err := exec.Command("ip", "link", "set", ifname, "up").CombinedOutput(); err != nil {
		log.Printf("WARN: `ip link set %s up`: %v, output: %s\n", ifname, err, string(out))
	}

	switch submode := this.GetSubmode(); submode {
	case 0:
		return this.enable_gateway_mode(ifname)
	case 1:
		return this.enable_client_mode(ifname)
	default:
		return fmt.Errorf("%s have no such submode: %d", this.instance, submode)
	}
}

// enable_gateway_mode 主模式:usb0 配 --rndis-ip 作为网关,起 dnsmasq
// 供 USB host 获取地址。
func (this *UsbGadgetRndis) enable_gateway_mode(ifname string) error {
	// 离开从模式:还原 DNS 配置(没进过从模式则 tail 无备份,不动)
	restore_dns()

	// submode 直切(不重建 gadget)时接口还带着从模式的地址/路由,先清掉;
	// 完整重建后接口是干净的,flush 无操作
	if out, err := exec.Command("ip", "route", "flush", "dev", ifname).CombinedOutput(); err != nil {
		log.Printf("WARN: `ip route flush dev %s`: %v, output: %s\n", ifname, err, string(out))
	}
	if out, err := exec.Command("ip", "addr", "flush", "dev", ifname).CombinedOutput(); err != nil {
		log.Printf("WARN: `ip addr flush dev %s`: %v, output: %s\n", ifname, err, string(out))
	}
	if err := add_iface_addr(ifname, this.ip_addr.String()); err != nil {
		log.Printf("WARN: %v\n", err)
	}
	this.start_dnsmasq()
	return nil
}

// enable_client_mode 从模式:优先 udhcpc 从上游拿 DHCP(Windows ICS /
// 路由器);拿不到可用租约时静态接入 Windows ICS 网段并 ping 网关
// 验证(host 开了共享但 DHCP 分配器不可用时链路仍可达);仍不通才
// 回退主模式,保证 stick 始终可达。
func (this *UsbGadgetRndis) enable_client_mode(ifname string) error {
	// submode 直切(不重建 gadget)时主模式的 dnsmasq 还在接口上跑,
	// 从模式是 DHCP 客户端,先停掉本机 DHCP 服务器(幂等,完整重建
	// 路径已在 effect 里停过)
	stop_dnsmasq_all()

	// 探测前先配临时地址:实测 usb0 无 IPv4 地址时收不到上游 DHCP 应答
	// (手动 udhcpc 成功时 usb0 都带着地址)。失败回退主模式时这个地址
	// 正好就是 --rndis-ip,不用额外处理。
	if err := add_iface_addr(ifname, this.ip_addr.String()); err != nil {
		log.Printf("WARN: %v\n", err)
	}

	subnet, router, dns, err := this.probe_upstream_dhcp_with_retry(ifname)
	if err == nil {
		if err := this.apply_dhcp_lease(ifname, subnet, router, dns); err == nil {
			return nil
		} else {
			log.Printf("WARN: dhcp lease unusable, try ICS gateway probe: %v\n", err)
		}
	} else {
		log.Printf("WARN: dhcp probe failed, try ICS gateway probe: %v\n", err)
	}

	return this.enable_ics_static_mode(ifname)
}

// apply_dhcp_lease 应用 DHCP 探测到的租约:usb0 配计算出的客户端 IP
// (--rndis-client-ip 规则),默认路由 via 上游网关,DNS 用上游发的。
// 掩码非连续/prefix >= /30(分不出可用地址)时返回错误,由调用方
// 转 ICS 静态探测。
func (this *UsbGadgetRndis) apply_dhcp_lease(ifname string, subnet, router netip.Addr, dns []netip.Addr) error {
	prefix, ok := mask_to_prefix(subnet)
	if !ok {
		return fmt.Errorf("non-contiguous subnet mask %s", subnet)
	}
	if prefix >= 30 { // /30 只有 2 个可用地址,没有分配给客户端的余量
		return fmt.Errorf("upstream prefix /%d too small", prefix)
	}

	// 探测成功:清掉临时地址,换成计算出的地址
	if out, err := exec.Command("ip", "addr", "flush", "dev", ifname).CombinedOutput(); err != nil {
		log.Printf("WARN: `ip addr flush dev %s`: %v, output: %s\n", ifname, err, string(out))
	}

	network := netip.PrefixFrom(router, prefix).Masked().Addr()
	ip := calc_client_ip(network, prefix, this.client_ip)
	ipSpec := fmt.Sprintf("%s/%d", ip, prefix)
	if err := add_iface_addr(ifname, ipSpec); err != nil {
		return err
	}
	if out, err := exec.Command("ip", "route", "add", "default", "via", router.String(), "dev", ifname).CombinedOutput(); err != nil {
		log.Printf("WARN: `ip route add default via %s dev %s`: %v, output: %s\n", router, ifname, err, string(out))
	}
	// 从模式走 usb0 上网,DNS 用上游发的(没有则用上游网关兜底),
	// 经 resolvconf tail 生效,离开从模式时还原
	this.apply_dns(dns, router)
	log.Printf("INFO: rndis client mode: %s, gateway %s\n", ipSpec, router)
	return nil
}

// enable_ics_static_mode 无可用 DHCP 时静态接入 Windows ICS 网段:
// --rndis-client-ip 的主机字节配 192.168.137.xx/24(ICS 共享端固定
// 192.168.137.1/24),DNS 探测网关确认 ICS 真在(host 开了共享但 DHCP
// 分配器不可用时链路仍可达);探测失败说明 host 既无 DHCP 也没开
// ICS,回退主模式保可达。不用 ping 探测:Win7 防火墙默认丢入站 ICMP
// (实测主机能 ping 通 stick、stick ping 不通 .1,但 TCP/UDP 正常)。
func (this *UsbGadgetRndis) enable_ics_static_mode(ifname string) error {
	gateway := netip.AddrFrom4([4]byte{192, 168, 137, 1})

	host := this.client_ip.As4()[3] // 传入 IP 的主机字节(0.0.0.33 → 33)
	ip := netip.AddrFrom4([4]byte{192, 168, 137, host})
	ipSpec := fmt.Sprintf("%s/24", ip)

	// 清掉探测用的临时地址(--rndis-ip),换成 ICS 网段地址
	if out, err := exec.Command("ip", "addr", "flush", "dev", ifname).CombinedOutput(); err != nil {
		log.Printf("WARN: `ip addr flush dev %s`: %v, output: %s\n", ifname, err, string(out))
	}
	if err := add_iface_addr(ifname, ipSpec); err != nil {
		log.Printf("WARN: %v, fallback to gateway mode\n", err)
		return this.fallback_to_gateway_mode()
	}

	if !this.probe_ics_dns(gateway, this.ics_timeout) {
		log.Printf("WARN: ics gateway %s has no DNS proxy, fallback to gateway mode\n", gateway)
		return this.fallback_to_gateway_mode()
	}

	if out, err := exec.Command("ip", "route", "add", "default", "via", gateway.String(), "dev", ifname).CombinedOutput(); err != nil {
		log.Printf("WARN: `ip route add default via %s dev %s`: %v, output: %s\n", gateway, ifname, err, string(out))
	}
	// 无 DHCP 就没有上游 DNS,ICS 主机自带 DNS 代理,指向网关
	this.apply_dns(nil, gateway)
	log.Printf("INFO: rndis client mode (ics static): %s, gateway %s\n", ipSpec, gateway)
	return nil
}

// probe_ics_dns 向 gateway:53 发一个 DNS query,收到任何回复(UDP 包,
// 哪怕是 REFUSED)即认为 ICS DNS 代理活着。ICS 启用时 DNS 代理固定
// 监听共享网卡 UDP 53,且 ICS 自动在防火墙放行本网段到 53 的流量
// (它自己依赖)——比 ping 网关可靠,也比探测 DHCP 67 有意义:
// DNS 代理不通就没有 DNS,配了静态也上不了网。无服务时 UDP 会收到
// ICMP port unreachable(Read 报错)或超时,两种情况都返回 false。
func (this *UsbGadgetRndis) probe_ics_dns(gateway netip.Addr, timeout time.Duration) bool {
	conn, err := net.DialTimeout("udp4", net.JoinHostPort(gateway.String(), "53"), timeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return false
	}
	if _, err := conn.Write(dns_query("dns.msftncsi.com")); err != nil {
		return false
	}
	reply := make([]byte, 512)
	n, err := conn.Read(reply)
	return err == nil && n > 0
}

// dns_query 手工构造最小 DNS query(id 固定,IANA 保留校验和可乱写;
// 探测只关心有没有回复,不需要解析内容)。
func dns_query(name string) []byte {
	buf := []byte{0x12, 0x34, // id(任意)
		0x01, 0x00, // flags: RD
		0x00, 0x01, // QDCOUNT = 1
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // AN/NS/AR = 0
	for _, label := range strings.Split(name, ".") {
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	buf = append(buf, 0x00)                   // root
	buf = append(buf, 0x00, 0x01, 0x00, 0x01) // QTYPE A, QCLASS IN
	return buf
}

// fallback_to_gateway_mode 从模式回退:submode 直接跳回 0(回退后 LED 显示
// 与后续 effect 都回到主模式),再走主模式逻辑。
func (this *UsbGadgetRndis) fallback_to_gateway_mode() error {
	this.SetSubmode(0)
	return this.enable_gateway_mode(this.ifname)
}

// DNS 直接写 /etc/resolv.conf:resolvconf tail 方案在 stick 上无效
// (resolv.conf 不重新聚合),改为直接写文件。写前备份原状(是否
// symlink 及内容),离开从模式时还原。NetworkManager 重写 resolv.conf
// 会覆盖条目(实测环境不受影响,由用户确认),这是已知限制。
const (
	resolvConfPath   = "/etc/resolv.conf"
	resolvConfBackup = "/tmp/rndis-resolv.conf"
)

// apply_dns 把上游 DNS(探测拿到的,没有则用上游网关兜底)写入
// /etc/resolv.conf。
func (this *UsbGadgetRndis) apply_dns(dns []netip.Addr, fallback netip.Addr) {
	if len(dns) == 0 {
		dns = []netip.Addr{fallback}
	}
	backup_resolv_conf()

	var sb strings.Builder
	sb.WriteString("# generated by " + base.PROJECT_IDENT + " RNDIS client submode\n")
	for _, d := range dns {
		fmt.Fprintf(&sb, "nameserver %s\n", d)
	}
	_ = os.Remove(resolvConfPath) // 是 symlink 的话先删,WriteFile 不会跟随写坏
	if err := os.WriteFile(resolvConfPath, []byte(sb.String()), 0644); err != nil {
		log.Printf("WARN: write %s: %v\n", resolvConfPath, err)
	}
}

// backup_resolv_conf 把 resolv.conf 原状(是否 symlink 及内容)存到 /tmp,
// 首次进入从模式时备份,restore_dns 还原。
func backup_resolv_conf() {
	if _, err := os.Stat(resolvConfBackup); err == nil {
		return // 上次的备份还没恢复,仍在从模式,不重复备份
	}
	link, linkErr := os.Readlink(resolvConfPath)
	data, err := os.ReadFile(resolvConfPath)
	if err != nil {
		return // resolv.conf 不存在,restore 时按删除处理
	}
	if linkErr == nil {
		data = []byte("LINK:" + link + "\n" + string(data))
	}
	_ = os.WriteFile(resolvConfBackup, data, 0644)
}

// restore_dns 离开从模式(回退/切回主模式)时还原 resolv.conf 原状。
func restore_dns() {
	data, err := os.ReadFile(resolvConfBackup)
	if err != nil {
		return // 无备份(没进过从模式),不动
	}
	_ = os.Remove(resolvConfPath)
	if link, rest, ok := strings.Cut(string(data), "\n"); ok && strings.HasPrefix(link, "LINK:") {
		// 原状是 symlink(如 systemd-resolved stub):重建 symlink 再写内容
		if err := os.Symlink(strings.TrimPrefix(link, "LINK:"), resolvConfPath); err != nil {
			log.Printf("WARN: restore symlink %s: %v\n", resolvConfPath, err)
		} else if err := os.WriteFile(resolvConfPath, []byte(rest), 0644); err != nil {
			log.Printf("WARN: restore %s: %v\n", resolvConfPath, err)
		}
	} else if err := os.WriteFile(resolvConfPath, data, 0644); err != nil {
		log.Printf("WARN: restore %s: %v\n", resolvConfPath, err)
	}
	_ = os.Remove(resolvConfBackup)
}

// add_iface_addr 给接口配地址,3 次重试后放弃;地址已存在(重复 apply)
// 不算错误。
func add_iface_addr(ifname, ipSpec string) error {
	for attempt := 1; ; attempt++ {
		out, err := exec.Command("ip", "addr", "add", ipSpec, "dev", ifname).CombinedOutput()
		if err == nil || has_iface_addr(ifname, ipSpec) {
			return nil
		}
		if attempt >= 3 {
			return fmt.Errorf("`ip addr add %s dev %s`: %v, output: %s", ipSpec, ifname, err, string(out))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// probe_upstream_dhcp_with_retry 在 client_timeout 总预算内等 RNDIS 网卡
// 出现并探测。切换 submode 时 gadget 重建,usb0 随 RNDIS 重新枚举,
// 网卡未出现时第一轮探测必然失败(实测),所以先等 carrier up(carrier
// up 只代表数据通道建立,Windows 侧 ICS 可能仍在就绪);剩余预算内
// 失败重试,耗尽回退,由 enable_client_mode 处理。
func (this *UsbGadgetRndis) probe_upstream_dhcp_with_retry(ifname string) (netip.Addr, netip.Addr, []netip.Addr, error) {
	deadline := time.Now().Add(this.client_timeout)
	wait_rndis_carrier(ifname, time.Until(deadline))

	var lastErr error
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			log.Printf("INFO: rndis dhcp retry %d\n", attempt)
		}
		subnet, router, dns, err := this.probe_upstream_dhcp(ifname, time.Until(deadline))
		if err == nil {
			return subnet, router, dns, nil
		}
		lastErr = err
		// 剩余预算不够再跑一轮 udhcpc(约 6s)+ 1s 间隔,放弃
		if time.Until(deadline) <= 7*time.Second {
			break
		}
		time.Sleep(time.Second)
	}
	return netip.Addr{}, netip.Addr{}, nil, lastErr
}

// wait_rndis_carrier 等接口出现且 carrier up(RNDIS 数据通道建立)。
// carrier 文件不存在(接口未出现/刚重建)时按未就绪继续等。
func wait_rndis_carrier(ifname string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile("/sys/class/net/" + ifname + "/carrier")
		if err == nil && strings.TrimSpace(string(data)) == "1" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// probe_upstream_dhcp 用系统 udhcpc 做一次完整 DHCP 交换:udhcpc -q 拿到
// 租约即退出,-R 退出前发 DHCPRELEASE 终止租约 —— 只取网段,不持有
// 地址。掩码/网关由回调脚本打到 stdout,daemon 捕获解析。insomniacslk
// 库的 nclient4 收不到 Windows ICS 的 Offer(实测),udhcpc 手动成功过。
func (this *UsbGadgetRndis) probe_upstream_dhcp(ifname string, timeout time.Duration) (netip.Addr, netip.Addr, []netip.Addr, error) {
	script, err := this.udhcpc_script()
	if err != nil {
		return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("write udhcpc script: %w", err)
	}
	defer os.Remove(script)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{
		"-i", ifname,
		"-p", "/tmp/rndis-udhcpc-" + ifname + ".pid",
		"-q", "-n", "-R", // 拿到租约即退出;退出前发 DHCPRELEASE
		"-t", "3", "-T", "2", // 3 次尝试,间隔 2s(与手动调试命令一致)
		"-s", script,
	}
	out, err := exec.CommandContext(ctx, "udhcpc", args...).CombinedOutput()
	if err != nil {
		// 探测预算耗尽时 CommandContext 会 SIGKILL udhcpc(err 只有
		// "signal: killed"),补上真实原因(context deadline)方便排查
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = fmt.Errorf("udhcpc on %s: %w (probe budget %s exhausted), output: %s", ifname, ctxErr, timeout, strings.TrimSpace(string(out)))
		} else {
			err = fmt.Errorf("udhcpc on %s: %w, output: %s", ifname, err, strings.TrimSpace(string(out)))
		}
		return netip.Addr{}, netip.Addr{}, nil, err
	}

	var subnet, router net.IP
	var dns []netip.Addr
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "RNDIS_SUBNET="):
			subnet = net.ParseIP(strings.TrimPrefix(line, "RNDIS_SUBNET="))
		case strings.HasPrefix(line, "RNDIS_ROUTER="):
			router = net.ParseIP(strings.TrimPrefix(line, "RNDIS_ROUTER="))
		case strings.HasPrefix(line, "RNDIS_DNS="):
			// $dns 是空格分隔的服务器列表,如 "192.168.137.1 1.1.1.1"
			for _, s := range strings.Fields(strings.TrimPrefix(line, "RNDIS_DNS=")) {
				if ip := net.ParseIP(s); ip != nil {
					dns = append(dns, netip.AddrFrom4([4]byte(ip.To4())))
				}
			}
		}
	}
	if subnet == nil || router == nil {
		return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("udhcpc got no subnet/router, output: %s", string(out))
	}
	return netip.AddrFrom4([4]byte(subnet.To4())), netip.AddrFrom4([4]byte(router.To4())), dns, nil
}

// udhcpc_script 落盘 udhcpc 回调脚本:bound/renew 时把上游掩码/网关打到
// stdout(daemon 从 CombinedOutput 捕获解析)。
func (this *UsbGadgetRndis) udhcpc_script() (string, error) {
	path := "/tmp/rndis-udhcpc-" + this.ifname + ".sh"
	script := `#!/bin/sh
# udhcpc 回调:bound/renew 时输出网段信息,daemon 捕获解析
case "$1" in
	bound|renew)
		echo "RNDIS_SUBNET=$subnet"
		echo "RNDIS_ROUTER=$router"
		echo "RNDIS_DNS=$dns"
		;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		return "", err
	}
	return path, nil
}

// mask_to_prefix 把点分掩码(255.255.255.0)转成前缀长度;非连续掩码
// 返回 false。
func mask_to_prefix(mask netip.Addr) (int, bool) {
	a := mask.As4()
	m := binary.BigEndian.Uint32(a[:])
	prefix := bits.OnesCount32(m)
	if uint32(0xffffffff)<<(32-prefix) != m {
		return 0, false
	}
	return prefix, true
}

// calc_client_ip 计算从模式 usb0 的 IP:掩码对齐 8bit 时,取上游网络
// 前缀字节 + client_ip 的后缀字节(0 字节表示取上游字节),例如
// /24 + 0.0.22.33 → 192.168.137.33;/16 + 0.0.22.33 → 192.168.22.33;
// 不对齐 8bit(如 /29)时取广播地址 - 2。prefix >= 30 由调用方回退。
func calc_client_ip(network netip.Addr, prefix int, client_ip netip.Addr) netip.Addr {
	if prefix%8 != 0 {
		broadcast, _ := subnet_last(netip.PrefixFrom(network, prefix))
		return broadcast.Prev().Prev()
	}

	n := network.As4()
	c := client_ip.As4()
	for i := prefix / 8; i < 4; i++ {
		n[i] = c[i]
	}
	return netip.AddrFrom4(n)
}

// has_iface_addr 检查接口上是否已有指定地址(如 `10.22.33.1/24`)。
func has_iface_addr(ifname, ipSpec string) bool {
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

// unmanage_from_network_manager 让 NetworkManager 不接管 ifname。NM 接管
// 后会把 usb0 当 DHCP client(method=auto),永远拿不到地址,超时后清掉
// 我们配的 IP,再每 45s 无限重试 — 必须让开。
//
// 持久规则写 /etc/NetworkManager/conf.d/<PROJECT_IDENT>.conf:NM 在
// 启动时加载配置,而 usb0 是 daemon 绑定 UDC 之后才创建的 — 规则先于
// 接口存在,usb0 注册即 unmanaged,开机无竞态。本次运行中 NM 已启动,
// 内存配置里可能还没有这条规则(文件刚写入),所以写入后还要 SIGHUP
// 重载,并校验是否生效,不行用 nmcli 直接改运行时状态兜底(实测
// NM 1.30 的 SIGHUP 重载不会重新评估"已有"设备的 unmanaged 状态)。
// NM 未安装/未运行时,以上步骤均为无害 no-op。
func (this *UsbGadgetRndis) unmanage_from_network_manager(ifname string) {
	// 不主动创建 /etc/NetworkManager(没装 NM 就别留垃圾)
	if _, err := os.Stat("/etc/NetworkManager"); err != nil {
		return
	}
	confPath := filepath.Join("/etc/NetworkManager/conf.d", base.PROJECT_IDENT+".conf")
	if err := os.MkdirAll(filepath.Dir(confPath), 0755); err != nil {
		log.Printf("WARN: mkdir %s: %v\n", filepath.Dir(confPath), err)
		return
	}
	if err := os.WriteFile(confPath, []byte(unmanaged_conf(ifname)), 0644); err != nil {
		log.Printf("WARN: write %s: %v\n", confPath, err)
		return
	}

	reload_network_manager()

	// 3) 校验生效;SIGHUP 重载对已有设备可能无效(实测),超时后用
	//    nmcli 直接设运行时状态兜底。NM 未运行时立即放弃等。
	for range 6 {
		unmanaged, responsive := nm_device_is_unmanaged(ifname)
		if !responsive {
			return // NM 未运行,conf 已写入,等它下次启动时生效即可
		}
		if unmanaged {
			return
		}
		reload_network_manager()
		time.Sleep(500 * time.Millisecond)
	}
	if out, err := exec.Command("nmcli", "device", "set", ifname, "managed", "no").CombinedOutput(); err != nil {
		log.Printf("WARN: `nmcli device set %s managed no`: %v, output: %s\n", ifname, err, string(out))
	} else {
		log.Printf("INFO: `nmcli device set %s managed no` (SIGHUP 重载未生效,已兜底)\n", ifname)
	}
}

// unmanaged_conf 生成 NetworkManager conf.d 的 unmanaged 规则。
func unmanaged_conf(ifname string) string {
	return "[device]\nmatch-device=interface-name:" + ifname + "\nmanaged=0\n"
}

// reload_network_manager 向 NetworkManager 发 SIGHUP 重载配置(标准做法,
// 不依赖 nmcli)。NM 未运行时是无害 no-op。
func reload_network_manager() {
	if out, err := exec.Command("sh", "-c", "kill -HUP $(pgrep -x NetworkManager) 2>/dev/null").CombinedOutput(); err != nil {
		log.Printf("WARN: reload NetworkManager: %v, output: %s\n", err, string(out))
	}
}

// nm_device_is_unmanaged 用 nmcli 查询 ifname 是否已被 NM 标为 unmanaged
// (固定枚举值,不受 locale 影响)。返回 (是否 unmanaged, NM 是否可答);
// NM 未安装/未运行或设备未知时 responsive 为 false。
func nm_device_is_unmanaged(ifname string) (bool, bool) {
	out, err := exec.Command("nmcli", "-t", "-f", "GENERAL.STATE", "device", "show", ifname).CombinedOutput()
	if err != nil {
		return false, false
	}
	return strings.Contains(string(out), "unmanaged"), true
}

// start_dnsmasq 在接口上启动 dnsmasq DHCP 服务器,供 USB host 获取地址。
// 池从本机 IP 之后到子网最后一个地址;--port=0 关闭 DNS,避免与系统
// dnsmasq 冲突(系统实例已禁用,本实例独占 67 端口)。
func (this *UsbGadgetRndis) start_dnsmasq() {
	pidFile := this.dnsmasq_pid_file()
	if pidNum := read_dnsmasq_pid(pidFile); pidNum > 0 {
		return // 已在运行,pid 文件校验过 cmdline,不会误判
	}

	// 网关就是本机 usb0 的地址 —— 用 Masked() 会取到网络地址
	// (10.22.33.0),给 host 一个无人应答的假网关
	router := this.ip_addr.Addr()
	last, ok := subnet_last(this.ip_addr)
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

	// 参数顺序:服务默认值 → 用户自定义(--dnsmasq-arg)→ 控制参数。
	// dnsmasq 对标量选项(如 --port)取最后一次出现,所以用户参数放在
	// 默认值之后可以覆盖它们(--port=53 开启 DNS);--interface /
	// --bind-interfaces / --pid-file 是控制参数,必须最后,保证接口归属
	// 和 stop_dnsmasq_all 的 pid 文件追踪不被破坏。
	// 注意 --no-resolv / --no-hosts 无法被参数覆盖(--hosts 没有正向
	// 开关);想提供 hosts 请用 --addn-hosts=...(--no-hosts 只跳过
	// /etc/hosts,addn-hosts 文件仍然加载)。
	args := []string{
		// 不读系统配置文件:dnsmasq 默认会加载 /etc/dnsmasq.conf 及其 include
		// (OpenWrt: /tmp/dnsmasq.d/),把系统 DHCP 范围(实测:192.168.68.10
		// -.254,stick 出厂 RNDIS 网段)合并进本实例,为无关网段广播 DHCP
		"--conf-file=/dev/null",
		fmt.Sprintf("--dhcp-range=%s,%s,12h", start, end),
		"--dhcp-option=option:router," + router.String(),
		"--port=0", // 不提供 DNS
		"--no-resolv",
		"--no-hosts",
	}
	args = append(args, this.dnsmasq_args...)
	args = append(args,
		"--interface="+this.ifname,
		"--bind-interfaces",
		"--pid-file="+pidFile,
	)
	if out, err := exec.Command("dnsmasq", args...).CombinedOutput(); err != nil {
		log.Printf("WARN: cannot start dnsmasq: %v, output: %s\n", err, string(out))
	}
}

// stop_dnsmasq_all 停掉由本 daemon 启动的所有 dnsmasq 实例(pid 文件 +
// cmdline 校验,pid 复用也不会误杀无关进程)。本 daemon 只会为 RNDIS
// 接口启动 dnsmasq,遍历 /tmp/dnsmasq-*.pid 覆盖 ifname 动态化后的
// 全部路径。
func stop_dnsmasq_all() {
	matches, err := filepath.Glob("/tmp/dnsmasq-*.pid")
	if err != nil {
		return
	}
	for _, pidFile := range matches {
		pidNum := read_dnsmasq_pid(pidFile)
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

// read_dnsmasq_pid 返回 pid 文件中指向的、由我们启动的 dnsmasq 实例的
// pid;不存在或 pid 已被其他进程复用(pid 文件过期)时返回 0。校验方式:
// /proc/<pid>/cmdline 必须包含该 pid 文件对应的 --pid-file 参数 — 这样
// 绝不会误杀系统 dnsmasq 或无关进程。
func read_dnsmasq_pid(pidFile string) int {
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

// subnet_last 返回 prefix 子网的最后一个地址(广播地址)。
func subnet_last(prefix netip.Prefix) (netip.Addr, bool) {
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
	rndis.max_submode = 1 // submode 0(网关)/1(从模式),enable() 只认这两个
	return rndis
}

func NewUsbGadgetRndis(ip_addr netip.Prefix, connection_prefix string, dev_addr string, host_addr string, ifname string, qmult string, dnsmasq_args []string, client_ip netip.Addr, client_timeout time.Duration, ics_timeout time.Duration, serial_number string, manufacturer string, product string) *UsbGadgetRndis {
	rndis := &UsbGadgetRndis{}

	rndis.ip_addr = ip_addr
	rndis.connection_prefix = connection_prefix
	rndis.client_ip = client_ip
	rndis.client_timeout = client_timeout
	rndis.ics_timeout = ics_timeout
	rndis.dev_addr = dev_addr
	rndis.host_addr = host_addr
	rndis.ifname = ifname
	rndis.qmult = qmult
	rndis.dnsmasq_args = dnsmasq_args
	rndis.serial_number = serial_number
	rndis.manufacturer = manufacturer
	rndis.product = product

	rndis._type = "rndis"
	rndis.code = USB_GADGET_FUNCTION_CODE_RNDIS
	rndis.max_submode = 1 // submode 0(网关)/1(从模式),enable() 只认这两个
	return rndis
}
