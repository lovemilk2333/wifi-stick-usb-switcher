package main

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core"
)

type VersionCmd struct{}

var args struct {
	Daemon  *core.DaemonCmd `arg:"subcommand:daemon"`
	Version *VersionCmd     `arg:"subcommand:version"`
}

type Commit struct {
	Hash string
	Time time.Time
}

func getCommit() *Commit {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		panic("cannot read build info")
	}

	commit := &Commit{}

	for _, settings := range info.Settings {
		if settings.Key == "vcs.revision" {
			commit.Hash = settings.Value
		} else if settings.Key == "vcs.time" {
			commit_time, err := time.Parse(time.RFC3339, settings.Value)
			if err != nil {
				panic(fmt.Errorf("cannot parse commit time (%s): %w", settings.Value, err))
			}

			commit.Time = commit_time
		}
	}

	return commit
}

func main() {
	parser := arg.MustParse(&args)

	switch {
	case args.Version != nil:
		commit := getCommit()
		fmt.Printf("Version: %s (%s)\n", commit.Hash, commit.Time)
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
