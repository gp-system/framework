package httpmw

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Otel wraps the handler in otelhttp instrumentation under the given
// operation name (the server chassis uses "http.server", the realtime
// gateway "realtime").
func Otel(name string, opts ...otelhttp.Option) Middleware {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, name, opts...)
	}
}

// FilterPath is an otelhttp option dropping spans for the given exact
// request paths; use it to keep load-balancer health checks (e.g.
// "/healthz") out of the trace backend.
func FilterPath(paths ...string) otelhttp.Option {
	return otelhttp.WithFilter(func(r *http.Request) bool {
		for _, p := range paths {
			if r.URL.Path == p {
				return false
			}
		}
		return true
	})
}
