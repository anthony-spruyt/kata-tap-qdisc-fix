// Command proc-scanner runs one kata-tap-qdisc-fix sweep and prints the
// result. It is a diagnostic for a debug pod with hostPID, not part of the image.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/config"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/netns"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/procscan"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/qdisc"
)

func main() { os.Exit(run()) }

func run() int {
	dryRun := flag.Bool("dry-run", false, "inspect qdiscs but do not replace them")
	procRoot := flag.String("proc", "/proc", "proc filesystem root (use /host/proc inside a hostPID=true container)")
	logLevel := flag.String("log-level", "info", "log level: debug|info|warn|error")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: config.ParseLevel(strings.ToLower(*logLevel))}))
	logger.Info("proc-scanner starting", "dry_run", *dryRun, "proc", *procRoot)

	hostInode, err := procscan.HostNetnsInode(*procRoot, 1)
	if err != nil {
		logger.Error("read host netns inode", "error", err.Error())
		return 3
	}
	logger.Info("host netns inode", "inode", hostInode)

	if err := netns.InitHost(); err != nil {
		logger.Error("init host netns", "error", err.Error())
		return 3
	}

	scanner := procscan.New(exitOnRestoreFailure{netns.NewOpener()}, qdisc.NewManager, *dryRun, logger, hostInode, *procRoot)
	result, err := scanner.Sweep(context.Background())
	if err != nil {
		logger.Error("sweep failed", "error", err.Error())
		return 1
	}

	fmt.Println()
	fmt.Println("=== proc sweep result ===")
	fmt.Printf("elapsed:       %v\n", result.Elapsed)
	fmt.Printf("total_inodes:  %d\n", result.TotalInodes)
	fmt.Printf("host_skipped:  %d\n", result.HostSkipped)
	fmt.Printf("unique_netns:  %d\n", result.UniqueNetns)
	fmt.Printf("taps_found:    %d\n", result.TapsFound)
	fmt.Printf("replaced:      %d\n", result.Replaced)
	fmt.Printf("would_replace: %d\n", result.WouldReplace)
	fmt.Println("=========================")
	return 0
}

// exitOnRestoreFailure stops the process rather than sweep on from a thread stuck in a pod netns.
type exitOnRestoreFailure struct{ netns.Opener }

func (o exitOnRestoreFailure) DoInNetns(path string, fn func() error) error {
	err := o.Opener.DoInNetns(path, fn)
	if errors.Is(err, netns.ErrRestoreFailed) {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(2)
	}
	return err
}
