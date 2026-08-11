package httpmw

import (
	"net/http"

	"github.com/gp-system/errs"
)

// Recoverer turns handler panics into an errs error whose stack points at
// the panic site and hands it to onError for rendering (the server chassis
// passes its Problem writer). http.ErrAbortHandler is re-raised untouched:
// it is net/http's flow-control signal, not a failure.
func Recoverer(onError func(http.ResponseWriter, *http.Request, error)) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison per net/http docs
						panic(rec)
					}
					onError(w, req, errs.NewPanic(rec))
				}
			}()
			next.ServeHTTP(w, req)
		})
	}
}
