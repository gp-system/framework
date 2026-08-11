package realtime

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/centrifugal/centrifuge"
)

func testNode(t *testing.T) *centrifuge.Node {
	t.Helper()
	node, err := centrifuge.New(centrifuge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestWithMiddlewareOrdering(t *testing.T) {
	var order []string
	probe := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	o := options{withoutTelemetry: true, middleware: []func(http.Handler) http.Handler{probe("first"), probe("second")}}
	var draining atomic.Bool
	h := newMux(testNode(t), &draining, Config{}, &o)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/realtime/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d", rec.Code)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("middleware order = %v, want %v", order, want)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("response missing X-Request-Id")
	}
}

func TestGatewayRecoversPanics(t *testing.T) {
	panicking := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	}
	o := options{withoutTelemetry: true, middleware: []func(http.Handler) http.Handler{panicking}}
	var draining atomic.Bool
	h := newMux(testNode(t), &draining, Config{}, &o)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/realtime/healthz", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
