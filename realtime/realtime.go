// Package realtime is the kit's realtime gateway chassis: a dedicated
// server that holds client streaming connections (WebSocket, with an
// HTTP-streaming/SSE fallback) and delivers messages published by any
// process through notify/broadcast.Publisher.
//
// It is a separate binary (cmd/realtime, scaffolded by `gpsystem add
// realtime`), not a route mounted on an API module's server: see the
// package's architecture doc for why. Instances are stateless and scale
// freely: each only holds the channels its own connections need, publishes
// reach every instance through the shared Valkey broker, and there is no
// sticky-session requirement.
//
//	func main() {
//		cfg := envconf.MustLoad[config.Config]()
//		ctx := context.Background()
//
//		err := realtime.Run(ctx, cfg.Realtime, func(topics *realtime.TopicAuth) error {
//			topics.Allow("announcements", func(id *rbac.Identity) bool { return true })
//			return nil
//		})
//		if err != nil {
//			log.Fatal(err)
//		}
//	}
package realtime

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/gp-system/errs"
	"github.com/gp-system/framework/app"
)

// RegisterFunc registers the project's shared-topic authorization. It runs
// once at startup, before the gateway starts accepting connections.
type RegisterFunc func(topics *TopicAuth) error

// RegisterRPCFunc registers the project's client-callable RPC methods. It
// runs once at startup, before the gateway starts accepting connections,
// alongside RegisterFunc.
type RegisterRPCFunc func(rpc *RPCRegistry) error

// Option customizes Run.
type Option func(*options)

type closer struct {
	name string
	fn   func(context.Context) error
}

type options struct {
	closers          []closer
	middleware       []func(http.Handler) http.Handler
	withoutTelemetry bool
	mutateServer     []func(*http.Server)
	registerRPC      RegisterRPCFunc
}

// WithCloser registers a cleanup function run during graceful shutdown,
// after every held connection has been disconnected. Closers run LIFO;
// telemetry flushes last.
func WithCloser(name string, fn func(context.Context) error) Option {
	return func(o *options) { o.closers = append(o.closers, closer{name: name, fn: fn}) }
}

// WithoutTelemetry skips telemetry.Setup and the otel middleware. For
// tests, or when the process configures telemetry itself.
func WithoutTelemetry() Option {
	return func(o *options) { o.withoutTelemetry = true }
}

// WithMiddleware appends net/http middleware after the kit defaults (CORS,
// request id, otel, Sentry hub, panic recovery) and before the transport
// endpoints, the same contract as server.WithMiddleware.
func WithMiddleware(mw ...func(http.Handler) http.Handler) Option {
	return func(o *options) { o.middleware = append(o.middleware, mw...) }
}

// WithHTTPServer mutates the http.Server before it starts listening: an
// escape hatch for settings the kit does not expose.
func WithHTTPServer(mutate func(*http.Server)) Option {
	return func(o *options) { o.mutateServer = append(o.mutateServer, mutate) }
}

// WithRPC registers the project's client-callable RPC methods (see
// RPCRegistry.Handle), the gateway's client-to-server direction. Without
// this option the gateway stays server-to-client only, as before: an
// unregistered RPC call is rejected with centrifuge.ErrorMethodNotFound.
func WithRPC(register RegisterRPCFunc) Option {
	return func(o *options) { o.registerRPC = register }
}

func buildOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Run starts the gateway and blocks until it exits: telemetry bootstrap,
// topic-authorization registration, the centrifuge node and its HTTP
// transports, then graceful shutdown on SIGINT/SIGTERM.
//
// Shutdown order: stop accepting new connections (health and connect
// endpoints start returning 503), node.Shutdown disconnects every held
// connection with a reconnect-eligible code (clients back off and land on
// another instance, then backfill any missed notification from the database
// channel's REST endpoint), the HTTP server stops, closers run LIFO,
// telemetry flushes last.
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

	topics := newTopicAuth()
	if err := register(topics); err != nil {
		return bail(errs.Wrap(err, "realtime: register"))
	}

	rpc := newRPCRegistry()
	if o.registerRPC != nil {
		if err := o.registerRPC(rpc); err != nil {
			return bail(errs.Wrap(err, "realtime: register rpc"))
		}
	}

	node, err := newNode(cfg, topics, rpc)
	if err != nil {
		return bail(err)
	}

	var draining atomic.Bool
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           newMux(node, &draining, cfg, &o),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		// Streaming connections are held open indefinitely; WriteTimeout/
		// IdleTimeout must stay unbounded, unlike server.Config's API posture.
	}
	for _, mutate := range o.mutateServer {
		mutate(srv)
	}

	closers := make([]app.Closer, 0, len(o.closers))
	for _, c := range o.closers {
		closers = append(closers, app.Closer{Name: c.name, Fn: c.fn})
	}

	return app.Run(ctx, app.Params{
		ListenAddr:      cfg.ListenAddr,
		ShutdownTimeout: cfg.DrainTimeout,
		Serve: func() error {
			if err := node.Run(); err != nil {
				return errs.Wrap(err, "realtime: run node")
			}
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				return errs.Wrap(err, "realtime: listen")
			}
			return nil
		},
		Shutdown: func(shutdownCtx context.Context) error {
			draining.Store(true)
			if err := node.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("realtime: node shutdown: %w", err)
			}
			return srv.Shutdown(shutdownCtx)
		},
		Closers: closers,
		Flush:   telemetryShutdown,
	})
}
