package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/alexflint/go-arg"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemon"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
	"golang.org/x/sys/unix"
)

type VersionCmd struct{}

var ipc_mapping map[string]daemonipc.IPCPackageType = map[string]daemonipc.IPCPackageType{
	"toggle-led": daemonipc.PACKAGE_TOGGLE_LED,
}

type IPCCmd struct {
	Command        string        `arg:"positional,required" help:"IPC command name (e.g. toggle-led)"`
	Timeout        time.Duration `arg:"-t,--timeout" default:"10s" help:"wait IPC response timeout"`
	ConnectTimeout time.Duration `arg:"--connect-timeout" default:"5s" help:"IPC dial and handshake timeout"`
	DialRetry      time.Duration `arg:"--dial-retry" default:"1s" help:"IPC dial retry interval"`
	Args           []string      `arg:"positional" help:"arguments passed to the IPC command"`
}

var args struct {
	Daemon  *daemon.DaemonCmd `arg:"subcommand:daemon"`
	Version *VersionCmd       `arg:"subcommand:version"`
	IPC     *IPCCmd           `arg:"subcommand:ipc"`
}

// https://github.com/xpzouying/golang-notes/issues/24

var (
	CommitHash string
	BuildTime  string
)

var ipc_client_chan daemonipc.IPCClientRespChannel

func init_ipc_client(connect_timeout, dial_retry time.Duration) (*daemonipc.IPCFramework, error) {
	daemonipc.InitServer(nil) // load server package definitions

	ipc_client, channel := daemonipc.InitClient()
	ipc_client_chan = channel

	ipc_impl, err := ipc.StartClient(base.PROJECT_IDENT, &ipc.ClientConfig{
		Encryption: false, // daemon server 不加密(ServerConfig 零值),必须对齐否则握手失败
		Timeout:    connect_timeout.Seconds(),
		RetryTimer: time.Duration(dial_retry.Seconds()), // 库的字段语义是"秒"这个数值
	})
	if err != nil {
		return nil, err
	}

	// 先启动 mainloop 消费库消息(startClient 的第一条状态消息阻塞在
	// 无缓冲通道上,dial 要等它被读走才继续),再等握手完成才发请求,
	// 否则 Write 报 "Connecting"、cli 退出,daemon 侧握手失败
	err = ipc_client.Start(ipc_impl)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(connect_timeout)
	for ipc_impl.StatusCode() != ipc.Connected {
		if time.Now().After(deadline) {
			ipc_impl.Close()
			return nil, fmt.Errorf("IPC not connected: %s", ipc_impl.Status())
		}
		time.Sleep(20 * time.Millisecond)
	}

	return ipc_client, nil
}

func call_ipc(ipc_client *daemonipc.IPCFramework, ipc_args *IPCCmd, command string, args []string) (string, error) {
	var err error

	if ipc_client_chan == nil {
		ipc_client, err = init_ipc_client(ipc_args.ConnectTimeout, ipc_args.DialRetry)
		if err != nil {
			return "", err
		}
	}

	ipc_command := strings.TrimSpace(command)
	package_type, ok := ipc_mapping[ipc_command]
	if !ok {
		return "", fmt.Errorf("no such IPC command `%s`\n", ipc_command)
	}

	err = ipc_client.SendRaw(package_type, args)
	if err != nil {
		return "", err
	}

	select {
	case resp := <-ipc_client_chan:
		return resp, nil
	case <-time.After(ipc_args.Timeout):
		return "", fmt.Errorf("IPC timed out")
	}
}

func lock2core(coreID int) error {
	runtime.LockOSThread() // lock goroutine

	var set unix.CPUSet
	set.Zero()
	set.Set(coreID)

	err := unix.SchedSetaffinity(0, &set) // lock current thread
	if err != nil {
		return fmt.Errorf("failed to set cpu affinity: %w", err)
	}

	return nil
}

func main() {
	parser := arg.MustParse(&args)

	switch {
	case args.Version != nil:
		fmt.Printf("Version: %s\nBuilt: %s\n", CommitHash, BuildTime)
	case args.IPC != nil:
		fw, err := init_ipc_client(args.IPC.ConnectTimeout, args.IPC.DialRetry)
		if err != nil {
			log.Printf("cannot start IPC client: %v", err)
			os.Exit(65)
		}

		msg, err := call_ipc(fw, args.IPC, args.IPC.Command, args.IPC.Args)
		if err != nil {
			log.Printf("cannot call IPC: %v", err)
			os.Exit(66)
		} else {
			fmt.Println(msg)
		}
	case args.Daemon != nil:
		core_count := runtime.NumCPU()
		last_core := core_count - 1

		err := lock2core(last_core)
		if err != nil {
			log.Printf("cannot lock to CPU core: %v", err)
			os.Exit(1)
		}

		daemon, err := daemon.NewDaemon(args.Daemon)
		if err != nil {
			log.Printf("cannot init daemon: %v", err)
			os.Exit(2)
		}

		err = daemon.Mainloop()
		if err != nil {
			log.Printf("cannot start daemon: %v", err)
			os.Exit(3)
		}
	default:
		parser.WriteHelp(os.Stdout)
		os.Exit(127)
	}
}
