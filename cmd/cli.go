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
	Command string        `arg:"positional,required" help:"IPC command name (e.g. toggle-led)"`
	Timeout time.Duration `arg:"-t,--timeout" default:"10s" help:"wait IPC response timeout"`
	Args    []string      `arg:"positional" help:"arguments passed to the IPC command"`
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

func init_ipc_client() (*daemonipc.IPCFramework, error) {
	daemonipc.InitServer(nil) // load server package types

	ipc_client, channel := daemonipc.InitClient()
	ipc_client_chan = channel

	ipc_impl, err := ipc.StartClient(base.PROJECT_IDENT, nil)
	if err != nil {
		return nil, err
	}

	return ipc_client, ipc_client.Start(ipc_impl)
}

func call_ipc(ipc_client *daemonipc.IPCFramework, timeout time.Duration, command string, args []string) (string, error) {
	var err error

	if ipc_client_chan == nil {
		ipc_client, err = init_ipc_client()
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
	case <-time.After(timeout):
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
		fw, err := init_ipc_client()
		if err != nil {
			log.Printf("cannot start IPC client: %v", err)
			os.Exit(65)
		}

		msg, err := call_ipc(fw, args.IPC.Timeout, args.IPC.Command, args.IPC.Args)
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
