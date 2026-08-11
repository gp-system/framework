// Package worker is the kit's background-worker chassis: it runs an asynq
// server that processes event, listener, scheduled-event and job tasks, plus
// (optionally) the outbox relay and the recurring-schedule runner, all under
// the same signal-driven graceful shutdown as the HTTP engines (app.Run).
//
// A worker binary reduces to:
//
//	func main() {
//		cfg := envconf.MustLoad[config.Config]()
//		ctx := context.Background()
//		pool := pg.MustNewPool(ctx, cfg.DB)
//		relay := outbox.MustNewRelay(ctx, cfg.Outbox, pool, cfg.Worker.Valkey)
//
//		err := worker.Run(ctx, cfg.Worker, func(reg *events.Registry, sched *scheduler.Schedule) error {
//			events.Listen(reg, "send-welcome-email", listeners.SendWelcomeEmail(deps))
//			sched.Daily(reports.GenerateDaily{})
//			return nil
//		}, worker.WithOutboxRelay(relay), worker.WithCloser("pgxpool", func(context.Context) error {
//			pool.Close()
//			return nil
//		}))
//		if err != nil {
//			log.Fatal(err)
//		}
//	}
package worker

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
	"golang.org/x/sync/errgroup"

	"github.com/gp-system/errs"
	"github.com/gp-system/events"
	"github.com/gp-system/events/outbox"
	"github.com/gp-system/events/scheduler"
	"github.com/gp-system/framework/app"
	"github.com/gp-system/queue"
)

// RegisterFunc registers the application's listeners and schedule. It runs once
// at startup; jobs added to sched are both scheduled (leader-only) and handled
// (on any worker).
type RegisterFunc func(reg *events.Registry, sched *scheduler.Schedule) error

// Option customizes Run.
type Option func(*options)

type closer struct {
	name string
	fn   func(context.Context) error
}

type options struct {
	closers          []closer
	withoutTelemetry bool
	relay            *outbox.Relay
	mutateAsynq      []func(*asynq.Config)
	mux              func(*asynq.ServeMux)
	taskMiddleware   []asynq.MiddlewareFunc
}

// WithCloser registers a cleanup function run during graceful shutdown, after
// tasks have drained. Closers run LIFO; telemetry flushes last.
func WithCloser(name string, fn func(context.Context) error) Option {
	return func(o *options) { o.closers = append(o.closers, closer{name: name, fn: fn}) }
}

// WithoutTelemetry skips telemetry.Setup. For tests, or when the process
// configures telemetry itself.
func WithoutTelemetry() Option {
	return func(o *options) { o.withoutTelemetry = true }
}

// WithOutboxRelay runs the transactional-outbox relay inside this worker. Safe
// on N replicas (the relay claims rows with FOR UPDATE SKIP LOCKED).
func WithOutboxRelay(r *outbox.Relay) Option {
	return func(o *options) { o.relay = r }
}

// WithAsynqConfig mutates the asynq.Config before the server is built: an
// escape hatch for settings the kit does not expose.
func WithAsynqConfig(mutate func(*asynq.Config)) Option {
	return func(o *options) { o.mutateAsynq = append(o.mutateAsynq, mutate) }
}

// WithMux registers extra raw asynq handlers: an escape hatch for task types
// the kit does not model (event/listener/schedule/job).
func WithMux(fn func(mux *asynq.ServeMux)) Option {
	return func(o *options) { o.mux = fn }
}

// WithTaskMiddleware appends asynq middleware after the kit trio
// (HubMiddleware, RecoverMiddleware, TracingMiddleware) and before the task
// handlers: the worker-side counterpart of server.WithMiddleware.
func WithTaskMiddleware(mw ...asynq.MiddlewareFunc) Option {
	return func(o *options) { o.taskMiddleware = append(o.taskMiddleware, mw...) }
}

func buildOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Run starts the worker and blocks until it exits: telemetry bootstrap,
// listener/schedule registration, the asynq server (plus optional relay and
// schedule runner), then graceful shutdown on SIGINT/SIGTERM. Shutdown order:
// stop the schedule and relay loops, drain in-flight tasks (ShutdownTimeout),
// run closers LIFO, flush telemetry. Returns nil on clean shutdown.
func Run(ctx context.Context, cfg Config, register RegisterFunc, opts ...Option) error {
	o := buildOptions(opts)

	telemetryShutdown := func(context.Context) error { return nil }
	if !o.withoutTelemetry {
		var err error
		telemetryShutdown, err = app.Telemetry(ctx, cfg.Telemetry, cfg.Log)
		if err != nil {
			return err
		}
	}
	bail := func(err error) error {
		_ = telemetryShutdown(context.WithoutCancel(ctx))
		return err
	}

	registry := events.NewRegistry()
	sched := scheduler.New()
	if err := register(registry, sched); err != nil {
		return bail(fmt.Errorf("worker: register: %w", err))
	}

	client, err := queue.NewClient(ctx, cfg.Valkey)
	if err != nil {
		return bail(err)
	}

	var runner *scheduler.Runner
	if sched.Len() > 0 {
		runner, err = scheduler.NewRunner(ctx, cfg.Scheduler, cfg.Valkey, sched)
		if err != nil {
			_ = client.Close()
			return bail(err)
		}
	}

	asynqCfg := asynq.Config{
		Concurrency:     cfg.Concurrency,
		Queues:          cfg.Queues,
		StrictPriority:  cfg.StrictPriority,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          slogLogger{},
	}
	for _, mutate := range o.mutateAsynq {
		mutate(&asynqCfg)
	}
	srv := asynq.NewServer(cfg.Valkey.ValkeyConnOpt(), asynqCfg)
	mux := newHandlers(registry, client, sched).mux(o.mux, o.taskMiddleware)

	// Closers run LIFO after the drain: tear down the schedule runner and relay
	// clients first, then the queue client, then the user's closers (pool).
	closers := make([]app.Closer, 0, len(o.closers)+3)
	for _, c := range o.closers {
		closers = append(closers, app.Closer{Name: c.name, Fn: c.fn})
	}
	closers = append(closers, app.Closer{Name: "queue-client", Fn: func(context.Context) error { return client.Close() }})
	if o.relay != nil {
		closers = append(closers, app.Closer{Name: "outbox-relay", Fn: o.relay.Shutdown})
	}
	if runner != nil {
		closers = append(closers, app.Closer{Name: "scheduler", Fn: runner.Shutdown})
	}

	runCtx, cancelRun := context.WithCancel(context.WithoutCancel(ctx))
	var g errgroup.Group

	return app.Run(ctx, app.Params{
		ListenAddr:      fmt.Sprintf("asynq(%s)", cfg.Valkey.Addr),
		ShutdownTimeout: cfg.ShutdownTimeout,
		Serve: func() error {
			if err := srv.Start(mux); err != nil {
				cancelRun()
				return errs.Wrap(err, "worker: start")
			}
			if o.relay != nil {
				g.Go(func() error { return o.relay.Run(runCtx) })
			}
			if runner != nil {
				g.Go(func() error { return runner.Run(runCtx) })
			}
			<-runCtx.Done()
			return g.Wait()
		},
		Shutdown: func(context.Context) error {
			cancelRun()    // stop the schedule and relay loops
			srv.Shutdown() // drain in-flight tasks (bounded by asynq's own timeout)
			return g.Wait()
		},
		Closers: closers,
		Flush:   telemetryShutdown,
	})
}
