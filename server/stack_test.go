package server_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gp-system/framework/server"
	"github.com/gp-system/httperr/validate"
)

// captureNames returns the stack names New builds for the given config and
// options, observed through a WithStack mutator appended after them.
func captureNames(cfg server.Config, opts ...server.Option) []string {
	var names []string
	opts = append(opts, server.WithStack(func(s *server.Stack) { names = s.Names() }))
	server.New(cfg, opts...)
	return names
}

func TestDefaultStackOrderFull(t *testing.T) {
	cfg := server.Config{
		TrustProxy:     true,
		AccessLog:      true,
		RequestTimeout: time.Second,
	}
	got := captureNames(cfg, server.WithValidator(validate.New()))
	want := []string{
		server.MWRequestID, server.MWRealIP, server.MWOtel, server.MWSentryHub,
		server.MWRecover, server.MWValidator, server.MWAccessLog, server.MWCORS,
		server.MWBodyLimit, server.MWTimeout,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stack = %v, want %v", got, want)
	}
}

func TestDefaultStackOrderMinimal(t *testing.T) {
	got := captureNames(server.Config{}, server.WithoutTelemetry())
	want := []string{
		server.MWRequestID, server.MWSentryHub, server.MWRecover,
		server.MWCORS, server.MWBodyLimit,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stack = %v, want %v", got, want)
	}
}

func TestWithMiddlewareAppendsAfterDefaults(t *testing.T) {
	noop := func(next http.Handler) http.Handler { return next }
	got := captureNames(server.Config{}, server.WithoutTelemetry(), server.WithMiddleware(noop, noop))
	want := []string{
		server.MWRequestID, server.MWSentryHub, server.MWRecover,
		server.MWCORS, server.MWBodyLimit, "anonymous-1", "anonymous-2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stack = %v, want %v", got, want)
	}
}

func TestStackMutations(t *testing.T) {
	noop := func(next http.Handler) http.Handler { return next }
	var names []string
	server.New(server.Config{}, server.WithoutTelemetry(), server.WithStack(func(s *server.Stack) {
		s.InsertBefore(server.MWCORS, "tenant", noop)
		s.InsertAfter(server.MWRequestID, "early", noop)
		s.Replace(server.MWSentryHub, noop)
		s.Remove(server.MWBodyLimit)
		s.Remove("not-present") // idempotent no-op
		names = s.Names()
	}))
	want := []string{
		server.MWRequestID, "early", server.MWSentryHub, server.MWRecover,
		"tenant", server.MWCORS,
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("stack = %v, want %v", names, want)
	}
}

func TestStackPanics(t *testing.T) {
	noop := func(next http.Handler) http.Handler { return next }
	tests := []struct {
		name   string
		mutate func(*server.Stack)
	}{
		{"duplicate name", func(s *server.Stack) { s.Use(server.MWCORS, noop) }},
		{"unknown insert target", func(s *server.Stack) { s.InsertBefore("nope", "x", noop) }},
		{"unknown replace target", func(s *server.Stack) { s.Replace("nope", noop) }},
		{"empty name", func(s *server.Stack) { s.Use("", noop) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			server.New(server.Config{}, server.WithoutTelemetry(), server.WithStack(tt.mutate))
		})
	}
}

// TestStackExecutionOrder sends a request through New and asserts the
// observed execution order of probes placed via WithStack.
func TestStackExecutionOrder(t *testing.T) {
	var order []string
	probe := func(name string) server.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	r := server.New(server.Config{}, server.WithoutTelemetry(), server.WithStack(func(s *server.Stack) {
		s.InsertBefore(server.MWRequestID, "outermost", probe("outermost"))
		s.InsertAfter(server.MWRecover, "mid", probe("mid"))
		s.Use("innermost", probe("innermost"))
	}))
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"outermost", "mid", "innermost"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("execution order = %v, want %v", order, want)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

// TestCORSDefaultsOnZeroConfig pins a regression: server.New with a zero
// Config (as tests and special cases commonly build it, bypassing envconf's
// CORS_METHODS/CORS_HEADERS defaults) must still allow the kit's default
// preflight methods and headers, not fall back to go-chi/cors' own bare
// defaults.
func TestCORSDefaultsOnZeroConfig(t *testing.T) {
	r := server.New(server.Config{}, server.WithoutTelemetry())
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPut)
	req.Header.Set("Access-Control-Request-Headers", "Authorization")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPut) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want it to contain PUT", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "authorization") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want it to contain Authorization", got)
	}
}

// TestRequestIDOnResponses pins the new default: every response carries an
// X-Request-Id header.
func TestRequestIDOnResponses(t *testing.T) {
	r := server.New(server.Config{}, server.WithoutTelemetry())
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("response missing X-Request-Id")
	}
}
