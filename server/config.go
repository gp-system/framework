package server

import (
	"time"

	"github.com/gp-system/logx"
	"github.com/gp-system/telemetry"
)

// Config holds everything Run needs. Embed it (without prefix) in the
// application config struct and load with envconf.MustLoad.
type Config struct {
	// ListenAddr is the address the engine listens on, e.g. ":3000".
	ListenAddr string `env:"LISTEN_ADDR" envDefault:":3000"`
	// ShutdownTimeout bounds graceful shutdown: in-flight requests get this
	// long to finish after SIGINT/SIGTERM before connections are dropped.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
	// ReadHeaderTimeout bounds how long a client may take to send the request
	// headers, per net/http's http.Server semantics. A nonzero value is the
	// primary Slowloris defense.
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"5s"`
	// ReadTimeout bounds reading the whole request (headers + body). A nonzero
	// value is also required for net/http's http.Server to close idle
	// keep-alive connections on graceful shutdown instead of blocking until
	// ShutdownTimeout.
	ReadTimeout time.Duration `env:"READ_TIMEOUT" envDefault:"30s"`
	// WriteTimeout bounds writing the response. Left at 0 (disabled) by default
	// because it also caps slow/streaming responses (large downloads, SSE);
	// set it when your endpoints are all short-lived.
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT" envDefault:"0s"`
	// IdleTimeout bounds how long an idle keep-alive connection is kept open.
	IdleTimeout time.Duration `env:"IDLE_TIMEOUT" envDefault:"120s"`
	// CORSOrigins lists allowed origins (comma-separated in the env var).
	CORSOrigins []string `env:"CORS_ORIGINS" envDefault:"*"`
	// CORSMethods lists the methods CORS preflights allow.
	CORSMethods []string `env:"CORS_METHODS" envDefault:"GET,POST,PUT,PATCH,DELETE,OPTIONS"`
	// CORSHeaders lists the request headers CORS preflights allow.
	CORSHeaders []string `env:"CORS_HEADERS" envDefault:"Content-Type,Authorization"`
	// CORSAllowCredentials permits cookies/credentials on cross-origin
	// requests. Default false: bearer-token APIs need no cookies, and
	// credentials together with wildcard origins is an invalid CORS setup.
	CORSAllowCredentials bool `env:"CORS_ALLOW_CREDENTIALS" envDefault:"false"`
	// TrustProxy enables the realip middleware: RemoteAddr is rewritten from
	// X-Real-Ip/X-Forwarded-For. Enable ONLY behind a proxy that overwrites
	// those headers; they are client-controlled otherwise.
	TrustProxy bool `env:"HTTP_TRUST_PROXY" envDefault:"false"`
	// AccessLog enables the structured slog access log (one record per
	// request, trace-correlated). Telemetry dev mode always logs regardless.
	// Default false so upgrading does not change production log volume.
	AccessLog bool `env:"HTTP_ACCESS_LOG" envDefault:"false"`
	// RequestTimeout, when nonzero, installs the timeout middleware: each
	// request context is canceled after this duration. Off by default
	// because it also caps streaming responses (SSE, large downloads).
	RequestTimeout time.Duration `env:"HTTP_REQUEST_TIMEOUT" envDefault:"0s"`
	// BodyLimit caps request body size in bytes (default 4 MiB). A value ≤ 0 is
	// treated as the 4 MiB default rather than "unlimited", so a misconfigured
	// or zero-valued limit can never disable the cap.
	BodyLimit int `env:"BODY_LIMIT" envDefault:"4194304"`
	// ExposeInternalErrors includes the underlying error message, stack and
	// wrap chain in 5xx responses. Default false: enable ONLY in development.
	// Decoupled from OTEL_DEV_MODE so enabling telemetry-dev logging cannot
	// accidentally start leaking internals to clients.
	ExposeInternalErrors bool `env:"HTTP_EXPOSE_INTERNAL_ERRORS" envDefault:"false"`
	// Telemetry configures telemetry.Setup: OTel bootstrap and the optional
	// Sentry integration (OTEL_SERVICE_NAME, OTEL_DEV_MODE, SENTRY_DSN, ...;
	// no prefix, all standard names).
	Telemetry telemetry.Config
	// Log configures the default slog logger's console format and minimum
	// level (LOG_LEVEL, LOG_STDOUT, LOG_FORMAT).
	Log logx.Config
}

// DefaultBodyLimit is the request-body cap applied when BodyLimit is ≤ 0.
const DefaultBodyLimit = 4 << 20 // 4 MiB

// EffectiveBodyLimit returns the body cap to apply, coercing a ≤ 0 configured
// value to DefaultBodyLimit so the limit can never be silently disabled.
func (c Config) EffectiveBodyLimit() int {
	if c.BodyLimit <= 0 {
		return DefaultBodyLimit
	}
	return c.BodyLimit
}

// DefaultCORSMethods and DefaultCORSHeaders mirror the CORS_METHODS and
// CORS_HEADERS envDefault values. EffectiveCORSMethods/EffectiveCORSHeaders
// fall back to them when the corresponding Config field is empty, so a
// Config built directly (tests, special cases) gets the same CORS behavior
// as one loaded through envconf defaults.
var (
	DefaultCORSMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	DefaultCORSHeaders = []string{"Content-Type", "Authorization"}
)

// EffectiveCORSMethods returns the methods to allow in CORS preflights,
// coercing an empty CORSMethods to DefaultCORSMethods.
func (c Config) EffectiveCORSMethods() []string {
	if len(c.CORSMethods) == 0 {
		return DefaultCORSMethods
	}
	return c.CORSMethods
}

// EffectiveCORSHeaders returns the request headers to allow in CORS
// preflights, coercing an empty CORSHeaders to DefaultCORSHeaders.
func (c Config) EffectiveCORSHeaders() []string {
	if len(c.CORSHeaders) == 0 {
		return DefaultCORSHeaders
	}
	return c.CORSHeaders
}
