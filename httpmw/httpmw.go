// Package httpmw holds the kit's net/http middleware building blocks, shared
// by the HTTP chassis (server) and the realtime gateway (realtime). Each
// constructor returns a plain func(http.Handler) http.Handler and is
// individually testable; composition, ordering and configuration stay with
// the consumers (server's named Stack, realtime's fixed chain).
//
// The package deliberately depends on neither chi nor httperr: anything
// router-specific (route patterns, error rendering) is injected by the
// caller. The worker's asynq middleware chain is NOT unified here on
// purpose: net/http and asynq middleware differ in shape and error model
// (a response writer versus a returned error driving retries), so only the
// context-level helpers (sentryx.WithRequestHub) are shared across that
// boundary.
package httpmw

import "net/http"

// Middleware is the standard net/http middleware shape every constructor in
// this package returns.
type Middleware = func(http.Handler) http.Handler
