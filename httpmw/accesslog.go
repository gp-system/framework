package httpmw

import (
	"log/slog"
	"net/http"
	"time"
)

// AccessLogOption customizes AccessLog.
type AccessLogOption func(*accessLogOptions)

type accessLogOptions struct {
	route func(*http.Request) string
}

// AccessLogRoute supplies the low-cardinality route pattern logged as the
// "route" attribute (e.g. chi's RoutePattern). It is called after the
// request was served, when routing has happened. httpmw takes it as a hook
// to stay router-agnostic.
func AccessLogRoute(route func(*http.Request) string) AccessLogOption {
	return func(o *accessLogOptions) { o.route = route }
}

// AccessLog logs one structured record per request: method, path, status,
// bytes written, duration, request id (from RequestID) and remote address.
// A nil logger resolves slog.Default at request time, so it picks up the
// logger telemetry bootstrap installs. Logging goes through the request
// context: with the OTel slog bridge active the records are
// trace-correlated, which chi's stock Logger (plain log.Print) never was.
//
// The record is emitted from a defer, so a panicking handler still produces
// one (with the panic attribute and status 500 unless a status was already
// written) before the panic continues to the Recoverer above.
func AccessLog(logger *slog.Logger, opts ...AccessLogOption) Middleware {
	var o accessLogOptions
	for _, opt := range opts {
		opt(&o)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lg := logger
			if lg == nil {
				lg = slog.Default()
			}
			sw := &statusWriter{ResponseWriter: w}
			start := time.Now()
			finished := false
			defer func() {
				status := sw.status
				panicked := !finished
				if status == 0 {
					if panicked {
						status = http.StatusInternalServerError
					} else {
						status = http.StatusOK
					}
				}
				attrs := make([]slog.Attr, 0, 9)
				attrs = append(attrs,
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", status),
					slog.Int64("bytes", sw.bytes),
					slog.Duration("duration", time.Since(start)),
					slog.String("remote", r.RemoteAddr),
				)
				if id := RequestIDFromContext(r.Context()); id != "" {
					attrs = append(attrs, slog.String("request_id", id))
				}
				if o.route != nil {
					if route := o.route(r); route != "" {
						attrs = append(attrs, slog.String("route", route))
					}
				}
				if panicked {
					attrs = append(attrs, slog.Bool("panic", true))
				}
				level := slog.LevelInfo
				if status >= http.StatusInternalServerError {
					level = slog.LevelError
				}
				lg.LogAttrs(r.Context(), level, "http request", attrs...)
			}()
			next.ServeHTTP(sw, r)
			finished = true
		})
	}
}

// statusWriter records the status code and body size written through it.
// Unwrap exposes the underlying writer so http.NewResponseController (and
// through it flushing, deadlines and hijacking) keeps working.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush forwards to the underlying writer when it supports it, so streaming
// responses (SSE) keep flushing through the access log wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		f.Flush()
	}
}
