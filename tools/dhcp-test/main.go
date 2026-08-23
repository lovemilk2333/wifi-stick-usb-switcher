// dhcp-test 是一个独立的 DHCP 调试工具,用于定位 daemon RNDIS 从模式
// 探测不到上游 DHCP Offer 的问题(Windows ICS 下 nclient4 超时,而手动
// udhcpc 成功)。两端都用 insomniacslk/dhcp 库,排除环境变量:
//
//	# 本机建 veth pair,一端起模拟 ICS 的 DHCP server,收发全在本地
//	sudo go run . server -v        # 自动建 dhcptest0/1,监听 dhcptest0
//	sudo go run . client -i dhcptest1 -v   # 复刻 daemon 探测(带临时地址)
//
//	# 真机环境:client 直接对上游(如 Windows ICS)
//	sudo go run . client -i usb0 -v
//
// server 也支持 -i 指定现有接口(不建 veth),例如 stick 上需要
// 在另一个网段模拟 ICS 时。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
)

const (
	vethA = "dhcptest0" // server 端
	vethB = "dhcptest1" // client 端
)

// ICS 模拟参数,与 Windows ICS 行为对齐:
// 固定网段 192.168.137.1/24,租约 7 天
const (
	serverIP = "192.168.137.1"
	poolBase = "192.168.137.100"
	leaseDur = 604800
)

var poolBaseIP = net.ParseIP(poolBase)

func run(args ...string) error {
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("`ip %v`: %v, output: %s", args, err, string(out))
	}
	return nil
}

// setupVeth 创建 veth pair 并给 server 端配 ICS 网段地址。
// 已存在则先删(上次残留)。
func setupVeth() error {
	_ = run("link", "del", vethA)
	if err := run("link", "add", vethA, "type", "veth", "peer", "name", vethB); err != nil {
		return err
	}
	if err := run("addr", "add", serverIP+"/24", "dev", vethA); err != nil {
		return err
	}
	if err := run("link", "set", vethA, "up"); err != nil {
		return err
	}
	return run("link", "set", vethB, "up")
}

// poolIP 按 chaddr 末字节在 .100 基础上偏移,重复测试拿同一地址
func poolIP(chaddr net.HardwareAddr) net.IP {
	ip := append(net.IP(nil), poolBaseIP...)
	if len(chaddr) >= 6 {
		ip[3] += chaddr[5] % 100
	}
	return ip
}

// handler 模拟 ICS:Discover -> Offer,Request -> ACK,打印收发的包
func handler(conn net.PacketConn, peer net.Addr, m *dhcpv4.DHCPv4) {
	log.Printf("recv from %v:\n%s\n", peer, m.Summary())

	var reply *dhcpv4.DHCPv4
	var err error
	switch m.MessageType() {
	case dhcpv4.MessageTypeDiscover:
		reply, err = dhcpv4.NewReplyFromRequest(m,
			dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer),
			dhcpv4.WithYourIP(poolIP(m.ClientHWAddr)),
			dhcpv4.WithServerIP(net.ParseIP(serverIP)),
			dhcpv4.WithOption(dhcpv4.OptSubnetMask(net.CIDRMask(24, 32))),
			dhcpv4.WithOption(dhcpv4.OptRouter(net.ParseIP(serverIP))),
			dhcpv4.WithOption(dhcpv4.OptDNS(net.ParseIP(serverIP))),
			dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(leaseDur)),
			dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP(serverIP))),
		)
	case dhcpv4.MessageTypeRequest:
		// 复刻 ICS:请求哪个地址给哪个
		yiaddr := poolIP(m.ClientHWAddr)
		if b := m.Options.Get(dhcpv4.OptionRequestedIPAddress); len(b) >= 4 {
			yiaddr = net.IP(b[:4])
		}
		reply, err = dhcpv4.NewReplyFromRequest(m,
			dhcpv4.WithMessageType(dhcpv4.MessageTypeAck),
			dhcpv4.WithYourIP(yiaddr),
			dhcpv4.WithServerIP(net.ParseIP(serverIP)),
			dhcpv4.WithOption(dhcpv4.OptSubnetMask(net.CIDRMask(24, 32))),
			dhcpv4.WithOption(dhcpv4.OptRouter(net.ParseIP(serverIP))),
			dhcpv4.WithOption(dhcpv4.OptDNS(net.ParseIP(serverIP))),
			dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(leaseDur)),
			dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP(serverIP))),
		)
	default:
		log.Printf("WARN: unhandled message type %s\n", m.MessageType())
		return
	}
	if err != nil {
		log.Printf("WARN: cannot build reply: %v\n", err)
		return
	}
	if _, err := conn.WriteTo(reply.ToBytes(), peer); err != nil {
		log.Printf("WARN: cannot send reply: %v\n", err)
		return
	}
	log.Printf("sent to %v:\n%s\n", peer, reply.Summary())
}

func cmdServer() error {
	var (
		ifname  = flag.String("i", "", "监听接口;留空则自动建 veth pair(dhcptest0/1)")
		verbose = flag.Bool("v", false, "verbose(打印包详情)")
	)
	flag.Parse()

	ownVeth := *ifname == ""
	if ownVeth {
		if err := setupVeth(); err != nil {
			return err
		}
		*ifname = vethA
		log.Printf("veth ready: %s (server) <-> %s (client)\n", vethA, vethB)
	}

	laddr := &net.UDPAddr{IP: net.IPv4zero, Port: 67}
	opts := []server4.ServerOpt{}
	if *verbose {
		opts = append(opts, server4.WithLogger(server4.DebugLogger{
			Printfer: log.New(os.Stderr, "dhcp-test server: ", log.LstdFlags),
		}))
	}
	server, err := server4.NewServer(*ifname, laddr, handler, opts...)
	if err != nil {
		return fmt.Errorf("create server on %s: %w", *ifname, err)
	}
	log.Printf("listening on %s\n", *ifname)
	if err := server.Serve(); err != nil {
		return err
	}
	return nil
}

func addIfaceAddr(ifname, spec string) error {
	// 探测前先配临时地址:实测 usb0 无 IPv4 地址时收不到上游 DHCP 应答
	// (手动 udhcpc 成功时 usb0 都带着地址),与 daemon 行为保持一致
	for i := 0; i < 3; i++ {
		if err := run("addr", "add", spec, "dev", ifname); err == nil {
			return nil
		} else if i == 2 {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func cmdClient() error {
	var (
		ifname  = flag.String("i", "", "client 监听接口(必填)")
		timeout = flag.Duration("t", 3*time.Second, "DHCP 探测超时")
		addr    = flag.String("a", "10.22.33.1/24", "probe 前加到接口的临时地址,复刻 daemon;空串不加")
		verbose = flag.Bool("v", false, "verbose(打印包详情)")
	)
	flag.Parse()
	if *ifname == "" {
		return fmt.Errorf("-i 必填")
	}

	if *addr != "" {
		if err := addIfaceAddr(*ifname, *addr); err != nil {
			return fmt.Errorf("add temp addr: %w", err)
		}
		defer run("addr", "flush", "dev", *ifname)
	}

	opts := []nclient4.ClientOpt{nclient4.WithTimeout(*timeout)}
	if *verbose {
		opts = append(opts, nclient4.WithDebugLogger())
	}
	client, err := nclient4.New(*ifname, opts...)
	if err != nil {
		return fmt.Errorf("create dhcp client on %s: %w", *ifname, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	lease, err := client.Request(ctx, dhcpv4.WithRequestedOptions(dhcpv4.OptionSubnetMask, dhcpv4.OptionRouter))
	if err != nil {
		return fmt.Errorf("dhcp exchange on %s: %w", *ifname, err)
	}

	log.Printf("OFFER:\n%s\n", lease.Offer.Summary())
	log.Printf("ACK:\n%s\n", lease.ACK.Summary())
	if b := lease.ACK.Options.Get(dhcpv4.OptionSubnetMask); len(b) >= 4 {
		log.Printf("subnet mask = %s\n", net.IP(b))
	}
	if b := lease.ACK.Options.Get(dhcpv4.OptionRouter); len(b) >= 4 {
		log.Printf("router = %s\n", net.IP(b))
	}
	return nil
}

func main() {
	log.SetFlags(log.LstdFlags)
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <server|client> [flags]\n", os.Args[0])
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "server":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		err = cmdServer()
	case "client":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		err = cmdClient()
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
}
