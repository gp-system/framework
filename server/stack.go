package server

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/gp-system/framework/httpmw"
)

// Middleware is the standard net/http middleware shape the Stack holds.
type Middleware = func(http.Handler) http.Handler

// Names of the kit's built-in middleware entries, the targets Stack
// mutations address. Using the constants keeps typos compile errors.
const (
	// MWRequestID assigns and echoes X-Request-Id (httpmw.RequestID).
	MWRequestID = "requestid"
	// MWRealIP rewrites RemoteAddr from proxy headers; present only when
	// Config.TrustProxy is set (httpmw.RealIP).
	MWRealIP = "realip"
	// MWOtel is the otelhttp span around the request; absent under
	// WithoutTelemetry (httpmw.Otel).
	MWOtel = "otel"
	// MWSentryHub gives each request its own Sentry hub (httpmw.RequestHub).
	MWSentryHub = "sentryhub"
	// MWRecover converts handler panics into 500 Problems (httpmw.Recoverer).
	MWRecover = "recover"
	// MWValidator injects the WithValidator validator into the request
	// context; present only when the option is used.
	MWValidator = "validator"
	// MWAccessLog is the structured slog access log; present when
	// Config.AccessLog is set or in telemetry dev mode (httpmw.AccessLog).
	MWAccessLog = "accesslog"
	// MWCORS is the API CORS preset built from Config (httpmw.CORSAPI).
	MWCORS = "cors"
	// MWBodyLimit caps the request body at Config.EffectiveBodyLimit.
	MWBodyLimit = "bodylimit"
	// MWTimeout applies Config.RequestTimeout as a context deadline; present
	// only when the timeout is nonzero.
	MWTimeout = "timeout"
	// MWAuth is the conventional name for the auth identity middleware when
	// a project mounts it on the whole stack. The kit reserves the name but
	// installs no entry under it; `gpsystem add auth` wires identity at the
	// api router instead.
	MWAuth = "auth"
)

// Entry is one named middleware in the server chain.
type Entry struct {
	Name string
	MW   Middleware
}

// Stack is the named, ordered middleware chain New and Run install on the
// router, pre-populated with the kit defaults (see the MW* constants for
// the names and defaultStack for the order). WithStack mutates it before
// the router is built.
//
// Mutations panic on an unknown target or a duplicate name: stack wiring
// errors are boot-time programmer errors and must fail loudly before the
// server starts, the same philosophy as chi's own router panics.
type Stack struct {
	entries []Entry
	anon    int
}

// Use appends the middleware at the end of the stack (innermost, closest to
// the handlers). Panics if the name is already registered.
func (s *Stack) Use(name string, mw Middleware) {
	s.insert(len(s.entries), name, mw)
}

// InsertBefore inserts the middleware immediately before (outside of) the
// named target. Panics if the target is unknown or the name is taken.
func (s *Stack) InsertBefore(target, name string, mw Middleware) {
	s.insert(s.index(target), name, mw)
}

// InsertAfter inserts the middleware immediately after (inside of) the
// named target. Panics if the target is unknown or the name is taken.
func (s *Stack) InsertAfter(target, name string, mw Middleware) {
	s.insert(s.index(target)+1, name, mw)
}

// Replace swaps the implementation under an existing name, keeping its
// position. Panics if the name is unknown.
func (s *Stack) Replace(name string, mw Middleware) {
	s.entries[s.index(name)].MW = mw
}

// Remove deletes the named entry. Removing an absent name is a no-op, so
// conditional defaults (realip, validator, accesslog, timeout) can be
// removed unconditionally.
func (s *Stack) Remove(name string) {
	for i, e := range s.entries {
		if e.Name == name {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

// Names returns the entry names in chain order (outermost first): a
// snapshot for tests and debugging.
func (s *Stack) Names() []string {
	names := make([]string, len(s.entries))
	for i, e := range s.entries {
		names[i] = e.Name
	}
	return names
}

func (s *Stack) insert(at int, name string, mw Middleware) {
	if name == "" {
		panic("server: stack: middleware name must not be empty")
	}
	for _, e := range s.entries {
		if e.Name == name {
			panic(fmt.Sprintf("server: stack: middleware %q already registered", name))
		}
	}
	s.entries = append(s.entries, Entry{})
	copy(s.entries[at+1:], s.entries[at:])
	s.entries[at] = Entry{Name: name, MW: mw}
}

func (s *Stack) index(name string) int {
	for i, e := range s.entries {
		if e.Name == name {
			return i
		}
	}
	panic(fmt.Sprintf("server: stack: unknown middleware %q", name))
}

// anonName names a WithMiddleware entry; the counter keeps names unique
// even across removals.
func (s *Stack) anonName() string {
	s.anon++
	return fmt.Sprintf("anonymous-%d", s.anon)
}

// defaultStack builds the kit's middleware chain from the config and
// options, outermost first:
//
//	requestid   everything below (logs, Sentry, error bodies) sees the id
//	realip      only with TrustProxy; rewrites RemoteAddr before logging
//	otel        the span wraps recovery and logging, so a recovered 500 is
//	            recorded on it and access-log records carry trace context
//	sentryhub   per-request hub inside the span
//	recover     panics anywhere below become 500 Problems
//	validator   only with WithValidator; pure context injection
//	accesslog   inside recover: panicked requests still log (as 500)
//	cors        after logging, so preflights are observable
//	bodylimit   innermost guard before the handlers
//	timeout     only with RequestTimeout; deadline nearest the handler
//
// WithStack and WithMiddleware mutations apply on top of this.
func defaultStack(cfg Config, o *options, writeError func(http.ResponseWriter, *http.Request, error)) *Stack {
	s := &Stack{}
	s.Use(MWRequestID, httpmw.RequestID())
	if cfg.TrustProxy {
		s.Use(MWRealIP, httpmw.RealIP(true))
	}
	if !o.withoutTelemetry {
		s.Use(MWOtel, httpmw.Otel("http.server"))
	}
	s.Use(MWSentryHub, httpmw.RequestHub())
	s.Use(MWRecover, httpmw.Recoverer(writeError))
	if o.validator != nil {
		s.Use(MWValidator, injectValidator(o.validator))
	}
	if cfg.AccessLog || cfg.Telemetry.Otel.Dev {
		s.Use(MWAccessLog, httpmw.AccessLog(nil, httpmw.AccessLogRoute(chiRoutePattern)))
	}
	s.Use(MWCORS, httpmw.CORSAPI(cfg.CORSOrigins, cfg.EffectiveCORSMethods(), cfg.EffectiveCORSHeaders(), cfg.CORSAllowCredentials))
	s.Use(MWBodyLimit, chimiddleware.RequestSize(int64(cfg.EffectiveBodyLimit())))
	if cfg.RequestTimeout > 0 {
		s.Use(MWTimeout, chimiddleware.Timeout(cfg.RequestTimeout))
	}
	return s
}

// chiRoutePattern is the AccessLog route hook: it reports the matched chi
// route pattern (low cardinality, e.g. /api/v1/things/{id}), available once
// the request was served.
func chiRoutePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		return rc.RoutePattern()
	}
	return ""
}
