package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"

	"github.com/alexflint/go-arg"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemon"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/ipc"
	"golang.org/x/sys/unix"
)

type VersionCmd struct{}

var args struct {
	Daemon  *daemon.DaemonCmd `arg:"subcommand:daemon"`
	Version *VersionCmd       `arg:"subcommand:version"`
	IPC     *ipc.IPCCmd       `arg:"subcommand:ipc"`
}

// https://github.com/xpzouying/golang-notes/issues/24

var (
	CommitHash string
	BuildTime  string
)

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
		if args.IPC.List {
			fmt.Print(ipc.ListCommandsString())
			return
		}

		fw, err := ipc.InitIPCClient(args.IPC.ConnectTimeout, args.IPC.DialRetry)
		if err != nil {
			log.Printf("cannot start IPC client: %v", err)
			os.Exit(65)
		}

		msg, err := ipc.CallIPC(fw, args.IPC, args.IPC.Command, args.IPC.Args)
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

		// SIGTERM/SIGINT → 优雅退出(Mainloop defer 清理运行时副作用)
		sig_chan := make(chan os.Signal, 1)
		signal.Notify(sig_chan, unix.SIGTERM, unix.SIGINT)
		go func() {
			<-sig_chan
			daemon.Stop()
		}()

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
