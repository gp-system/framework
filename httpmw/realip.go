package httpmw

import (
	"net/http"
	"strings"
)

// RealIP rewrites r.RemoteAddr from the X-Real-Ip or X-Forwarded-For header
// (first hop), so logs, Sentry events and rate limiters see the client
// address instead of the proxy's. With trustProxy false it is a no-op:
// forwarding headers are client-controlled, so they must only be honored
// behind a proxy that overwrites them (HTTP_TRUST_PROXY).
func RealIP(trustProxy bool) Middleware {
	if !trustProxy {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip := realIP(r); ip != "" {
				r.RemoteAddr = ip
			}
			next.ServeHTTP(w, r)
		})
	}
}

func realIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(ip)
	}
	return ""
}
