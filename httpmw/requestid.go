package httpmw

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// RequestIDHeader is the header RequestID reads inbound ids from and echoes
// the effective id on.
const RequestIDHeader = "X-Request-Id"

// maxRequestIDLength caps accepted inbound ids; anything longer is replaced
// with a generated one so a client cannot bloat logs and traces.
const maxRequestIDLength = 128

type requestIDCtxKey struct{}

// RequestID assigns every request an id: an inbound X-Request-Id is kept
// when it is well formed (printable ASCII, at most 128 bytes), otherwise a
// UUID is generated. The id is echoed on the response header and stored in
// the context for RequestIDFromContext, so access logs, Problem responses
// and support tickets can reference the same request.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID(id) {
				id = uuid.NewString()
			}
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDCtxKey{}, id)))
		})
	}
}

// RequestIDFromContext returns the id assigned by RequestID, or "" when the
// middleware is not installed.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDCtxKey{}).(string)
	return id
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] <= ' ' || id[i] > '~' {
			return false
		}
	}
	return true
}
