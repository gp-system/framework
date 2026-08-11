// Package server is the kit's chi-based HTTP chassis: a Config shared by the
// whole HTTP stack, and Run/New, which build a chi router with the kit's
// error handling and telemetry middleware wired in and run it with
// signal-driven graceful shutdown (app.Run). server does not depend on any
// other HTTP engine; it is the kit's only one.
//
// A module entry point reduces to:
//
//	func main() {
//		cfg := envconf.MustLoad[config.Config]()
//		ctx := context.Background()
//		pool := pg.MustNewPool(ctx, cfg.DB)
//
//		err := server.Run(ctx, cfg.Server, func(r chi.Router) error {
//			api := chi.NewRouter()
//			news.Register(api, news.Dependencies{ /* ... */ })
//			r.Mount("/api/v1", api)
//			return nil
//		}, server.WithCloser("pgxpool", func(context.Context) error {
//			pool.Close()
//			return nil
//		}))
//		if err != nil {
//			log.Fatal(err)
//		}
//	}
package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gp-system/framework/app"
	"github.com/gp-system/httperr"
	"github.com/gp-system/httperr/validate"
)

// RegisterFunc mounts the application's routes and modules on the router.
type RegisterFunc func(r chi.Router) error

// Option customizes New and Run.
type Option func(*options)

type options struct {
	closers          []app.Closer
	stackMutators    []func(*Stack)
	mutateServer     []func(*http.Server)
	withoutTelemetry bool
	validator        *validate.Validator
}

// WithCloser registers a cleanup function executed during graceful shutdown,
// after the HTTP server has stopped accepting requests. Closers run in
// reverse registration order (LIFO); telemetry providers are flushed last,
// so spans emitted by closers are still exported.
func WithCloser(name string, fn func(context.Context) error) Option {
	return func(o *options) { o.closers = append(o.closers, app.Closer{Name: name, Fn: fn}) }
}

// WithMiddleware appends router-level middleware after the kit defaults
// (see the MW* names) and before routes are registered. For anything beyond
// appending (inserting before a default, replacing or removing one) use
// WithStack.
func WithMiddleware(mw ...func(http.Handler) http.Handler) Option {
	return func(o *options) {
		o.stackMutators = append(o.stackMutators, func(s *Stack) {
			for _, m := range mw {
				s.Use(s.anonName(), m)
			}
		})
	}
}

// WithStack customizes the middleware chain beyond appending: insert
// before or after a named default, replace or remove one (see the MW*
// constants and Stack). Mutators run in registration order after the
// default stack is built from the Config, and panic on unknown or
// duplicate names; wiring bugs surface at boot, not at request time.
//
//	server.WithStack(func(s *server.Stack) {
//		s.InsertBefore(server.MWCORS, "tenant", tenantMiddleware)
//		s.Remove(server.MWSentryHub)
//	})
func WithStack(mutate func(*Stack)) Option {
	return func(o *options) { o.stackMutators = append(o.stackMutators, mutate) }
}

// WithHTTPServer mutates the http.Server before Listen: an escape hatch for
// settings the kit does not expose (timeouts, TLS, ...).
func WithHTTPServer(mutate func(*http.Server)) Option {
	return func(o *options) { o.mutateServer = append(o.mutateServer, mutate) }
}

// WithoutTelemetry skips telemetry.Setup and the otel middleware. For tests,
// or when the process configures telemetry itself.
func WithoutTelemetry() Option {
	return func(o *options) { o.withoutTelemetry = true }
}

// WithValidator sets the request validator that StrictValidator(nil)
// middlewares resolve at request time (e.g. one carrying custom validation
// tags registered via validate.WithRegister). A single option on New/Run
// applies to the whole application, so generated register.go code keeps
// passing nil and needs no edits. Without it, those middlewares fall back to
// validate.New().
func WithValidator(v *validate.Validator) Option {
	return func(o *options) { o.validator = v }
}

// validatorCtxKey keys the WithValidator validator in the request context.
type validatorCtxKey struct{}

// injectValidator stores v in the request context so StrictValidator(nil)
// middlewares (wired by oapi-codegen inside generated packages) resolve it
// at request time. This is how WithValidator reaches generated code without
// editing it.
func injectValidator(v *validate.Validator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), validatorCtxKey{}, v)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

// validatorFromContext returns the validator set by WithValidator, or nil.
func validatorFromContext(ctx context.Context) *validate.Validator {
	v, _ := ctx.Value(validatorCtxKey{}).(*validate.Validator)
	return v
}

// New builds a *chi.Mux with the kit's middleware stack (request id, OTel,
// Sentry hub, panic recovery, optional access log, CORS, body limit; see
// defaultStack) and RFC 9457 Problem responses for panics, 404 and 405.
// Most applications use Run instead; New exists for tests and special cases.
func New(cfg Config, opts ...Option) *chi.Mux {
	o := buildOptions(opts)
	return newRouter(cfg, &o)
}

func buildOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func newRouter(cfg Config, o *options) *chi.Mux {
	writeError := httperr.NewWriteError(httperr.WithExposeInternal(cfg.ExposeInternalErrors))

	stack := defaultStack(cfg, o, writeError)
	for _, mutate := range o.stackMutators {
		mutate(stack)
	}

	r := chi.NewRouter()
	for _, e := range stack.entries {
		r.Use(e.MW)
	}

	// chi's stock handlers write plain-text errors; keep every error response
	// application/problem+json. Mounted subrouters inherit these.
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, req, httperr.NotFound(""))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, req, httperr.New(http.StatusMethodNotAllowed, "", ""))
	})

	return r
}

// Run starts the service and blocks until it exits: telemetry bootstrap,
// router construction, route registration, ListenAndServe, then graceful
// shutdown on SIGINT/SIGTERM (a second signal force-kills the process).
// Shutdown order: stop accepting requests and drain in-flight ones
// (ShutdownTimeout), run closers LIFO, flush telemetry. Returns nil on clean
// shutdown.
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

	router := newRouter(cfg, &o)
	if err := register(router); err != nil {
		_ = telemetryShutdown(context.WithoutCancel(ctx))
		return fmt.Errorf("server: register: %w", err)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	for _, mutate := range o.mutateServer {
		mutate(srv)
	}

	return app.Run(ctx, app.Params{
		ListenAddr:      cfg.ListenAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Serve:           srv.ListenAndServe,
		Shutdown:        srv.Shutdown,
		Closers:         o.closers,
		Flush:           telemetryShutdown,
	})
}
