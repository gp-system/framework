package realtime

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// newOriginAllower builds the gateway's single origin policy, shared by the
// WebSocket transport's CheckOrigin, the SSE/HTTP-stream guard, and the CORS
// middleware, so the three can never drift into inconsistent rules.
//
// A request is allowed when any of the following holds:
//   - it carries no Origin header at all (non-browser clients: server-to-
//     server calls, the health check, curl); centrifuge's own default
//     same-host check makes the same allowance, so this preserves it
//   - the Origin's host matches the request Host (same-origin, the
//     documented production setup: the gateway sits behind the app's own
//     domain); this holds regardless of origins, so narrowing CORS_ORIGINS
//     can never lock the app out of its own gateway
//   - the Origin matches an entry in origins, using the same semantics as
//     go-chi/cors: "*" allows everything, entries are matched
//     case-insensitively, and a "*" inside an entry matches any substring
//     (e.g. "https://*.example.com")
func newOriginAllower(origins []string) func(*http.Request) bool {
	allowAll := false
	var exact []string
	var wildcards []wildcardOrigin
	for _, o := range origins {
		o = strings.ToLower(o)
		if o == "*" {
			allowAll = true
			break
		}
		if i := strings.IndexByte(o, '*'); i >= 0 {
			wildcards = append(wildcards, wildcardOrigin{prefix: o[:i], suffix: o[i+1:]})
			continue
		}
		exact = append(exact, o)
	}

	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		if sameHost(r.Host, origin) {
			return true
		}
		if allowAll {
			return true
		}
		lower := strings.ToLower(origin)
		for _, o := range exact {
			if o == lower {
				return true
			}
		}
		for _, w := range wildcards {
			if w.match(lower) {
				return true
			}
		}
		slog.Warn("realtime: origin rejected",
			slog.String("origin", origin), slog.String("host", r.Host))
		return false
	}
}

// sameHost reports whether origin's host matches host (the request's Host,
// e.g. r.Host). An unparsable origin is treated as a mismatch, not a panic.
func sameHost(host, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(host, u.Host)
}

// wildcardOrigin matches an origin pattern containing exactly one "*",
// mirroring go-chi/cors's wildcard type.
type wildcardOrigin struct {
	prefix string
	suffix string
}

func (w wildcardOrigin) match(origin string) bool {
	return len(origin) >= len(w.prefix)+len(w.suffix) &&
		strings.HasPrefix(origin, w.prefix) &&
		strings.HasSuffix(origin, w.suffix)
}

// checkOrigin wraps a transport handler (WebSocket, HTTP-stream, or SSE) with
// the shared origin policy, rejecting disallowed cross-origin handshakes with
// 403 before they reach centrifuge. WebSocketConfig.CheckOrigin covers the
// websocket transport too (belt and suspenders: centrifuge's upgrader runs
// its own check before this middleware would even matter for that path), but
// HTTPStreamConfig and SSEConfig have no CheckOrigin field, so this is the
// only origin guard those two transports get.
func checkOrigin(allow func(*http.Request) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allow(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
