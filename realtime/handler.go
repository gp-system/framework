package realtime

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/centrifugal/centrifuge"

	"github.com/gp-system/framework/httpmw"
)

// newMux builds the gateway's HTTP surface: the centrifuge transport
// endpoints (WebSocket, with an HTTP-streaming/SSE fallback for clients or
// proxies that don't like WebSocket) and a health endpoint reporting
// draining state, so a load balancer's health check evicts an instance the
// moment it starts shutting down.
//
// The kit middleware chain is aligned with server's, from the same httpmw
// building blocks: CORS (streaming preset) outermost, then request id, otel
// span, Sentry hub, panic recovery, then WithMiddleware extras. There is no
// access log: the connection endpoints hold long-lived streams, so a
// per-request record would be misleading.
func newMux(node *centrifuge.Node, draining *atomic.Bool, cfg Config, o *options) http.Handler {
	mux := http.NewServeMux()

	allow := newOriginAllower(cfg.CORSOrigins)

	wsHandler := centrifuge.NewWebsocketHandler(node, centrifuge.WebsocketConfig{CheckOrigin: allow})
	streamHandler := centrifuge.NewHTTPStreamHandler(node, centrifuge.HTTPStreamConfig{})
	sseHandler := centrifuge.NewSSEHandler(node, centrifuge.SSEConfig{})

	mux.Handle("/realtime/connection/websocket", checkDraining(draining, checkOrigin(allow, wsHandler)))
	mux.Handle("/realtime/connection/http_stream", checkDraining(draining, checkOrigin(allow, streamHandler)))
	mux.Handle("/realtime/connection/sse", checkDraining(draining, checkOrigin(allow, sseHandler)))
	mux.HandleFunc("/realtime/healthz", healthHandler(node, draining))

	handler := http.Handler(mux)
	for i := len(o.middleware) - 1; i >= 0; i-- {
		handler = o.middleware[i](handler)
	}
	handler = httpmw.Recoverer(writePanic)(handler)
	handler = httpmw.RequestHub()(handler)
	if !o.withoutTelemetry {
		handler = httpmw.Otel("realtime", httpmw.FilterPath("/realtime/healthz"))(handler)
	}
	handler = httpmw.RequestID()(handler)
	handler = httpmw.CORSStreaming(allow)(handler)
	return handler
}

// writePanic renders a recovered panic as a plain 500 and logs the
// stack-carrying error: the gateway has no Problem writer, and streaming
// clients reconnect on any 5xx anyway.
func writePanic(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "realtime: panic", slog.Any("error", err))
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// checkDraining returns 503 for new connect attempts once the gateway has
// started shutting down, so a load balancer routes them to another
// instance instead of a replica about to disconnect everyone.
func checkDraining(draining *atomic.Bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if draining.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type healthStatus struct {
	Status      string `json:"status"`
	Connections int    `json:"connections"`
}

func healthHandler(node *centrifuge.Node, draining *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := healthStatus{Status: "ok", Connections: node.Hub().NumClients()}
		code := http.StatusOK
		if draining.Load() {
			status.Status = "draining"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(status)
	}
}
