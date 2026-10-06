package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/config"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/metrics"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/netns"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/procscan"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/qdisc"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/server"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() { os.Exit(run()) }

func run() int {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid config", "error", err.Error())
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: config.ParseLevel(cfg.LogLevel)}))
	logger.Info("kata-tap-qdisc-fix starting",
		"version", version,
		"commit", commit,
		"dry_run", cfg.DryRun,
		"sweep_interval", cfg.SweepInterval.String())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := netns.InitHost(); err != nil {
		logger.Error("init host netns", "error", err.Error())
		return 3
	}

	hostInode, err := procscan.HostNetnsInode("/proc", 0)
	if err != nil {
		logger.Error("read host netns inode", "error", err.Error())
		return 3
	}

	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	ready := &server.Ready{}

	stopHealth := server.StartHealth(cfg.HealthPort, ready, logger)
	defer stopHealth()
	stopMetrics := server.StartMetrics(cfg.MetricsPort, reg, logger)
	defer stopMetrics()

	scanner := procscan.New(netns.NewOpener(), qdisc.NewManager, cfg.DryRun, logger, hostInode, "/proc")

	ready.MarkReady()

	ticker := time.NewTicker(cfg.SweepInterval)
	defer ticker.Stop()

	runSweep := func() {
		res, err := scanner.Sweep(ctx)
		if err != nil {
			m.ReplaceFailuresTotal.Inc()
			logger.Error("sweep failed", "error", err.Error())
			return
		}
		m.SweepsTotal.Inc()
		if res.Replaced > 0 {
			m.ReplacementsTotal.Add(float64(res.Replaced))
		}
		logger.Debug("sweep ok",
			"elapsed", res.Elapsed,
			"total_inodes", res.TotalInodes,
			"unique_netns", res.UniqueNetns,
			"replaced", res.Replaced,
			"taps_found", res.TapsFound)
	}

	runSweep()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutdown signal received")
			logger.Info("kata-tap-qdisc-fix stopped")
			return 0
		case <-ticker.C:
			runSweep()
		}
	}
}
