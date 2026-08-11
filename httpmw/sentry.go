package httpmw

import (
	"net/http"

	"github.com/gp-system/telemetry/sentryx"
)

// RequestHub gives each request its own Sentry hub so breadcrumbs (the
// request's log trail) attach only to that request's events. No-op when
// Sentry is off.
func RequestHub() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(sentryx.WithRequestHub(r.Context())))
		})
	}
}
