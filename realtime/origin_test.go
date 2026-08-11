package realtime

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/centrifugal/centrifuge"
)

func TestNewOriginAllower(t *testing.T) {
	cases := []struct {
		name    string
		origins []string
		host    string
		origin  string
		want    bool
	}{
		{"no origin header allowed", nil, "app.example", "", true},
		{"same host allowed regardless of list", nil, "app.example", "https://app.example", true},
		{"same host allowed even with empty list", []string{}, "app.example", "http://app.example", true},
		{"wildcard entry allows everything", []string{"*"}, "app.example", "http://evil.example", true},
		{"exact match allowed", []string{"http://foo.example"}, "app.example", "http://foo.example", true},
		{"exact match case-insensitive", []string{"http://Foo.example"}, "app.example", "http://foo.EXAMPLE", true},
		{"non-matching origin denied", []string{"http://foo.example"}, "app.example", "http://bar.example", false},
		{"empty list denies cross-origin", []string{}, "app.example", "http://foo.example", false},
		{"wildcard subdomain match", []string{"https://*.example.com"}, "app.example", "https://foo.example.com", true},
		{"wildcard subdomain non-match", []string{"https://*.example.com"}, "app.example", "https://example.com", false},
		{"unparsable origin falls through to list", []string{"http://foo.example"}, "app.example", "://bad", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allow := newOriginAllower(tc.origins)
			r := httptest.NewRequest(http.MethodGet, "http://app.example/realtime/connection/websocket", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := allow(r); got != tc.want {
				t.Fatalf("allow() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckOrigin_RejectsDisallowedOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := checkOrigin(newOriginAllower([]string{"http://allowed.example"}), next)

	r := httptest.NewRequest(http.MethodGet, "http://app.example/realtime/connection/sse", nil)
	r.Host = "app.example"
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCheckOrigin_AllowsMatchingOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := checkOrigin(newOriginAllower([]string{"http://allowed.example"}), next)

	r := httptest.NewRequest(http.MethodGet, "http://app.example/realtime/connection/http_stream", nil)
	r.Host = "app.example"
	r.Header.Set("Origin", "http://allowed.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHealthHandler_IgnoresOrigin(t *testing.T) {
	node, err := centrifuge.New(centrifuge.Config{})
	if err != nil {
		t.Fatalf("centrifuge.New: %v", err)
	}
	var draining atomic.Bool
	handler := healthHandler(node, &draining)

	r := httptest.NewRequest(http.MethodGet, "http://app.example/realtime/healthz", nil)
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	handler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
