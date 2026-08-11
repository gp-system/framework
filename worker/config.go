package worker

import (
	"time"

	"github.com/gp-system/events/scheduler"
	"github.com/gp-system/logx"
	"github.com/gp-system/queue"
	"github.com/gp-system/telemetry"
)

// Config holds everything Run needs. Embed it (without prefix) in the worker
// binary's application config struct, like server.Config:
//
//	type Config struct {
//		Worker worker.Config
//		DB     dbx.Config    `envPrefix:"DB_"`
//		Outbox outbox.Config `envPrefix:"OUTBOX_"`
//	}
type Config struct {
	// Concurrency is the number of tasks processed simultaneously.
	Concurrency int `env:"WORKER_CONCURRENCY" envDefault:"10"`
	// Queues maps queue name to priority weight; higher weights are served more
	// often. Set as "critical:6,default:3,low:1" in the env var.
	Queues map[string]int `env:"WORKER_QUEUES" envDefault:"default:1"`
	// StrictPriority serves higher-weight queues to exhaustion before lower ones.
	StrictPriority bool `env:"WORKER_STRICT_PRIORITY" envDefault:"false"`
	// ShutdownTimeout bounds graceful drain; unfinished tasks are requeued by
	// asynq and redelivered (at-least-once).
	ShutdownTimeout time.Duration `env:"WORKER_SHUTDOWN_TIMEOUT" envDefault:"30s"`
	// DefaultMaxRetry is the retry budget for listener and job tasks that do not
	// set their own via queue.MaxRetry.
	DefaultMaxRetry int `env:"WORKER_DEFAULT_MAX_RETRY" envDefault:"25"`

	// Valkey is the connection shared by the worker, the fan-out enqueue client
	// and (when enabled) the relay and scheduler.
	Valkey queue.Config `envPrefix:"VALKEY_"`
	// Scheduler configures the recurring-schedule runner (WithSchedule).
	Scheduler scheduler.Config `envPrefix:"SCHEDULER_"`
	// Telemetry configures telemetry.Setup (standard OTEL_*, SENTRY_* names,
	// no prefix).
	Telemetry telemetry.Config
	// Log configures the default slog logger's console format and minimum
	// level (LOG_LEVEL, LOG_STDOUT, LOG_FORMAT).
	Log logx.Config
}
