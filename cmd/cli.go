package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core"
)

type VersionCmd struct{}

var args struct {
	Daemon  *core.DaemonCmd `arg:"subcommand:daemon"`
	Version *VersionCmd     `arg:"subcommand:version"`
}

// https://github.com/xpzouying/golang-notes/issues/24

var (
	CommitHash string
	BuildTime  string
)

func main() {
	parser := arg.MustParse(&args)

	switch {
	case args.Version != nil:
		fmt.Printf("Version: %s\nBuilt: %s\n", CommitHash, BuildTime)
	case args.Daemon != nil:
		daemon, err := core.NewDaemon(*args.Daemon, time.Millisecond*10)
		if err != nil {
			log.Fatalf("FATAL: %s\n", err)
		}

		daemon.Mainloop()
	default:
		parser.WriteHelp(os.Stdout)
	}
}
