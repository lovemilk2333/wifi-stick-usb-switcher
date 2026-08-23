package main

import (
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/alexflint/go-arg"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemon"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
	"golang.org/x/sys/unix"
)

type VersionCmd struct{}

var ipc_mapping map[string]daemonipc.IPCPackageType = map[string]daemonipc.IPCPackageType{
	"toggle-led": daemonipc.PACKAGE_TOGGLE_LED,
}

type IPCCmd struct {
	Command string   `arg:"positional,required" help:"IPC command name (e.g. toggle-led)"`
	Args    []string `arg:"positional" help:"arguments passed to the IPC command"`
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

// func call_ipc(ipc *IPCCmd) (string, error) {
// 	ipc_command := strings.TrimSpace(ipc.Command)
// 	pkg_type, ok := ipc_mapping[ipc_command]
// 	if !ok {
// 		return "", fmt.Errorf("no such ipc command `%s`\n", ipc_command)
// 	}

// }

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
		// msg, err := call_ipc(args.IPC)
		// if err != nil {
		// 	log.Fatalf("FATAL: %s\n", err)
		// } else {
		// 	fmt.Println(msg)
		// }
	case args.Daemon != nil:
		core_count := runtime.NumCPU()
		last_core := core_count - 1

		err := lock2core(last_core)
		if err != nil {
			log.Fatalf("cannot lock to CPU core: %s\n", err)
			return
		}

		daemon, err := daemon.NewDaemon(args.Daemon)
		if err != nil {
			log.Fatalf("cannot init daemon: %s\n", err)
			return
		}

		err = daemon.Mainloop()
		if err != nil {
			log.Fatalf("cannot start daemon: %v", err)
			return
		}
	default:
		parser.WriteHelp(os.Stdout)
	}
}
