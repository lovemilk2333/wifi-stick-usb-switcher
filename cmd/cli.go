package main

import (
	"log"
	"os"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core"
)

var args struct {
	Daemon *core.DaemonCmd `arg:"subcommand:daemon"`
}

func main() {
	parser := arg.MustParse(&args)

	switch {
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
