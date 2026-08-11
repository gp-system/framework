package httpmw_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gp-system/framework/httpmw"
)

func TestRequestIDGeneratesAndEchoes(t *testing.T) {
	var ctxID string
	h := httpmw.RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = httpmw.RequestIDFromContext(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	headerID := rec.Header().Get(httpmw.RequestIDHeader)
	if headerID == "" {
		t.Fatal("no X-Request-Id response header")
	}
	if ctxID != headerID {
		t.Fatalf("context id %q != header id %q", ctxID, headerID)
	}
}

func TestRequestIDKeepsValidInbound(t *testing.T) {
	h := httpmw.RequestID()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(httpmw.RequestIDHeader, "client-id-42")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(httpmw.RequestIDHeader); got != "client-id-42" {
		t.Fatalf("inbound id not kept, got %q", got)
	}
}

func TestRequestIDRejectsMalformedInbound(t *testing.T) {
	for name, id := range map[string]string{
		"control chars": "bad\nid",
		"too long":      strings.Repeat("x", 500),
		"non ascii":     "id-\xc3\xa9",
	} {
		t.Run(name, func(t *testing.T) {
			h := httpmw.RequestID()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(httpmw.RequestIDHeader, id)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(httpmw.RequestIDHeader)
			if got == id || got == "" {
				t.Fatalf("malformed id %q not replaced, got %q", id, got)
			}
		})
	}
}

func TestRequestIDFromContextWithoutMiddleware(t *testing.T) {
	if got := httpmw.RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("expected empty id, got %q", got)
	}
}

func TestRealIP(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		headers    map[string]string
		want       string // "" = original RemoteAddr untouched
	}{
		{"untrusted ignores XFF", false, map[string]string{"X-Forwarded-For": "1.2.3.4"}, ""},
		{"trusted XFF first hop", true, map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8"}, "1.2.3.4"},
		{"trusted X-Real-Ip wins", true, map[string]string{"X-Real-Ip": "9.9.9.9", "X-Forwarded-For": "1.2.3.4"}, "9.9.9.9"},
		{"trusted no headers", true, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := httpmw.RealIP(tt.trustProxy)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			orig := req.RemoteAddr
			h.ServeHTTP(httptest.NewRecorder(), req)

			want := tt.want
			if want == "" {
				want = orig
			}
			if got != want {
				t.Fatalf("RemoteAddr = %q, want %q", got, want)
			}
		})
	}
}

func TestRecovererConvertsPanic(t *testing.T) {
	var seen error
	h := httpmw.Recoverer(func(w http.ResponseWriter, _ *http.Request, err error) {
		seen = err
		w.WriteHeader(http.StatusInternalServerError)
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if seen == nil || !strings.Contains(seen.Error(), "boom") {
		t.Fatalf("onError got %v, want panic error containing boom", seen)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRecovererRepanicsAbortHandler(t *testing.T) {
	h := httpmw.Recoverer(func(http.ResponseWriter, *http.Request, error) {
		t.Fatal("onError must not run for ErrAbortHandler")
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if rec := recover(); !errors.Is(rec.(error), http.ErrAbortHandler) {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", rec)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("expected re-panic")
}

// capturingHandler collects slog records for assertions.
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) attrs(t *testing.T) map[string]slog.Value {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.records) != 1 {
		t.Fatalf("got %d records, want 1", len(h.records))
	}
	m := map[string]slog.Value{}
	h.records[0].Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value
		return true
	})
	return m
}

func TestAccessLogFields(t *testing.T) {
	capture := &capturingHandler{}
	chain := httpmw.RequestID()(httpmw.AccessLog(
		slog.New(capture),
		httpmw.AccessLogRoute(func(*http.Request) string { return "/things/{id}" }),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	})))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/things/7", nil))

	attrs := capture.attrs(t)
	if got := attrs["method"].String(); got != http.MethodPost {
		t.Errorf("method = %q", got)
	}
	if got := attrs["path"].String(); got != "/things/7" {
		t.Errorf("path = %q", got)
	}
	if got := attrs["status"].Int64(); got != http.StatusTeapot {
		t.Errorf("status = %d", got)
	}
	if got := attrs["bytes"].Int64(); got != int64(len("hello")) {
		t.Errorf("bytes = %d", got)
	}
	if got := attrs["route"].String(); got != "/things/{id}" {
		t.Errorf("route = %q", got)
	}
	if attrs["request_id"].String() == "" {
		t.Error("request_id missing")
	}
	if _, ok := attrs["duration"]; !ok {
		t.Error("duration missing")
	}
}

func TestAccessLogImplicit200(t *testing.T) {
	capture := &capturingHandler{}
	h := httpmw.AccessLog(slog.New(capture))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := capture.attrs(t)["status"].Int64(); got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

func TestAccessLogLogsPanicAsError(t *testing.T) {
	capture := &capturingHandler{}
	h := httpmw.AccessLog(slog.New(capture))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic must propagate past AccessLog")
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	attrs := capture.attrs(t)
	if got := attrs["status"].Int64(); got != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", got)
	}
	if !attrs["panic"].Bool() {
		t.Error("panic attribute missing")
	}
	capture.mu.Lock()
	level := capture.records[0].Level
	capture.mu.Unlock()
	if level != slog.LevelError {
		t.Errorf("level = %v, want error", level)
	}
}

func TestOtelFilterPath(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	h := httpmw.Otel("test.server",
		otelhttp.WithTracerProvider(tp),
		httpmw.FilterPath("/healthz"),
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if n := len(exporter.GetSpans()); n != 0 {
		t.Fatalf("healthz produced %d spans, want 0", n)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api", nil))
	if n := len(exporter.GetSpans()); n != 1 {
		t.Fatalf("api produced %d spans, want 1", n)
	}
}

func TestCORSAPIHonorsConfig(t *testing.T) {
	h := httpmw.CORSAPI(
		[]string{"https://app.example.com"},
		[]string{http.MethodGet, http.MethodDelete},
		[]string{"Content-Type", "X-Custom"},
		false,
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodDelete)
	req.Header.Set("Access-Control-Request-Headers", "X-Custom")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("allow-origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != http.MethodDelete {
		t.Errorf("allow-methods = %q", got)
	}
}

func TestCORSStreamingAllowsCredentials(t *testing.T) {
	h := httpmw.CORSStreaming(func(r *http.Request) bool {
		return r.Header.Get("Origin") == "https://app.example.com"
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("allow-origin = %q", got)
	}
}
