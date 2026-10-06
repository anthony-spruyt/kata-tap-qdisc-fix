package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Ready struct{ ready atomic.Bool }

func (r *Ready) MarkReady()    { r.ready.Store(true) }
func (r *Ready) IsReady() bool { return r.ready.Load() }

// StartHealth serves /healthz and /readyz on port and returns a shutdown func.
func StartHealth(port int, ready *Ready, logger *slog.Logger) func() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.IsReady() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	return serve(port, mux, logger, "health")
}

// StartMetrics serves /metrics on port and returns a shutdown func.
func StartMetrics(port int, reg *prometheus.Registry, logger *slog.Logger) func() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return serve(port, mux, logger, "metrics")
}

func serve(port int, mux *http.ServeMux, logger *slog.Logger, kind string) func() {
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		logger.Error("listen failed", "server", kind, "addr", srv.Addr, "error", err.Error())
		return func() {
			// No-op: the listener never opened, so there is no server to shut down.
		}
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve error", "server", kind, "error", err.Error())
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
