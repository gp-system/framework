package httpmw

import (
	"net/http"

	"github.com/go-chi/cors"
)

// CORSAPI is the API chassis' CORS preset: explicit origins, methods and
// headers from configuration. Credentials default to off; JWT bearer APIs
// need no cookies, so keeping credentials out preserves the option of
// wildcard origins.
func CORSAPI(origins, methods, headers []string, allowCredentials bool) Middleware {
	return cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   methods,
		AllowedHeaders:   headers,
		AllowCredentials: allowCredentials,
	})
}

// CORSStreaming is the realtime gateway's CORS preset. It differs from
// CORSAPI on purpose: streaming transports (WebSocket fallback via
// HTTP-streaming/SSE) authenticate with cookies in some setups, so
// credentials are allowed, which in turn forbids wildcard origins; the
// allow func makes the per-origin decision, and headers stay open because
// centrifuge clients vary.
func CORSStreaming(allow func(*http.Request) bool) Middleware {
	return cors.Handler(cors.Options{
		AllowOriginFunc:  func(r *http.Request, _ string) bool { return allow(r) },
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
	})
}
