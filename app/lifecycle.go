// Package app is the kit's engine-agnostic process infrastructure: the
// signal-driven graceful shutdown lifecycle (Run) and the logx/telemetry
// bootstrap (Telemetry) shared by every chassis. Neither depends on HTTP or
// any other transport: the HTTP engine (server) and the worker
// chassis (worker) all delegate to it. External engines can too.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Closer is a named cleanup function executed during graceful shutdown.
type Closer struct {
	Name string
	Fn   func(context.Context) error
}

// Params describes one process run. Run blocks until the listener stops;
// Shutdown stops accepting requests and drains in-flight ones.
type Params struct {
	ListenAddr      string
	ShutdownTimeout time.Duration
	Serve           func() error
	Shutdown        func(context.Context) error
	Closers         []Closer
	Flush           func(context.Context) error
}

// Run runs the lifecycle and blocks until exit: Serve in a goroutine, then
// graceful shutdown on SIGINT/SIGTERM (a second signal force-kills the
// process). Shutdown order: drain requests (ShutdownTimeout), run closers
// LIFO, flush telemetry. Returns nil on clean shutdown.
func Run(ctx context.Context, p Params) error {
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- p.Serve()
	}()
	slog.Info("server started", slog.String("addr", p.ListenAddr))

	var runErr error
	select {
	case err := <-serveErr:
		runErr = fmt.Errorf("app: listen: %w", err)
	case <-signalCtx.Done():
		stop() // restore default signal handling: a second signal kills the process
		slog.Info("shutting down", slog.Duration("timeout", p.ShutdownTimeout))

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.ShutdownTimeout)
		defer cancel()
		if err := p.Shutdown(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("app: shutdown: %w", err))
		}
	}

	// Closers and telemetry flush get their own grace period even when the
	// HTTP drain consumed the shutdown budget.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.ShutdownTimeout)
	defer cancel()

	for i := len(p.Closers) - 1; i >= 0; i-- {
		c := p.Closers[i]
		if err := c.Fn(cleanupCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("app: closer %q: %w", c.Name, err))
		}
	}
	if p.Flush != nil {
		if err := p.Flush(cleanupCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("app: telemetry shutdown: %w", err))
		}
	}

	if runErr == nil {
		slog.Info("server stopped cleanly")
	}
	return runErr
}
