package realtime

import (
	"time"

	"github.com/gp-system/auth"
	"github.com/gp-system/logx"
	"github.com/gp-system/notify/broadcast"
	"github.com/gp-system/queue"
	"github.com/gp-system/telemetry"
)

// Config holds everything Run needs. Embed it (without prefix) in the
// realtime binary's application config struct, like worker.Config: it is
// deliberately its own struct, not server.Config: the gateway holds
// long-lived streaming connections (WriteTimeout effectively unbounded, no
// request body handling), a posture that would be wrong for the API's
// server.Config if shared.
//
//	type Config struct {
//		Realtime realtime.Config
//	}
type Config struct {
	// ListenAddr is the gateway's container-internal listen address; Traefik
	// (or another reverse proxy) targets it.
	ListenAddr string `env:"REALTIME_LISTEN_ADDR" envDefault:":3000"`
	// DrainTimeout bounds graceful shutdown: node.Shutdown disconnects every
	// held connection with a reconnect-eligible code, then closers run. Keep
	// this below the deployment's stop-grace-period.
	DrainTimeout time.Duration `env:"REALTIME_DRAIN_TIMEOUT" envDefault:"20s"`
	// ReadHeaderTimeout is the Slowloris defense on the connect handshake.
	ReadHeaderTimeout time.Duration `env:"REALTIME_READ_HEADER_TIMEOUT" envDefault:"5s"`
	// MaxConnections caps concurrent streams held by this instance; 0 means
	// unlimited. Once exceeded, new connect attempts get 503 so a load
	// balancer retries a less-loaded instance.
	MaxConnections int `env:"REALTIME_MAX_CONNECTIONS" envDefault:"0"`
	// CORSOrigins reuses the shared variable name so the gateway matches the
	// API modules' configuration.
	CORSOrigins []string `env:"CORS_ORIGINS" envDefault:"*"`

	// Broadcast configures the channel-naming prefix, shared with every
	// notify/broadcast.Publisher in the project.
	Broadcast broadcast.Config `envPrefix:"REALTIME_"`
	// Valkey is the connection the centrifuge node's broker uses: the same
	// VALKEY_* variables the queue and worker share.
	Valkey queue.Config `envPrefix:"VALKEY_"`
	// JWT authenticates the connect handshake, reusing the project's token
	// issuer configuration.
	JWT auth.Config `envPrefix:"JWT_"`
	// Telemetry configures telemetry.Setup (standard OTEL_*, SENTRY_* names,
	// no prefix).
	Telemetry telemetry.Config
	// Log configures the default slog logger's console format and minimum
	// level (LOG_LEVEL, LOG_STDOUT, LOG_FORMAT).
	Log logx.Config
}

// BroadcastValkey narrows Valkey (the kit's queue.Config-shaped Valkey
// source) down to the broadcast.ValkeyConfig shape notify/broadcast's
// constructors accept: broadcast does not depend on queue, so it cannot
// take a queue.Config directly even though the two share one Valkey
// instance.
func (c Config) BroadcastValkey() broadcast.ValkeyConfig {
	return broadcast.ValkeyConfig{
		Addr:     c.Valkey.Addr,
		Password: c.Valkey.Password,
		DB:       c.Valkey.DB,
	}
}
