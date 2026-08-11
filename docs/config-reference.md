# Configuration reference

Compose the framework's config structs in the project's `config.Config` and load with
`envconf.MustLoad[config.Config]()`. A `.env` file is read in development
(never overriding real environment variables).

This page covers only the three chassis config structs that live in this
repo: `server.Config`, `worker.Config` and `realtime.Config`. Each nests
`telemetry.Config` (standard `OTEL_*`/`SENTRY_*` names) and `logx.Config`
(standard `LOG_*` names, see below); `worker.Config` and `realtime.Config`
also nest other modules' configs
(`queue.Config`, `events/scheduler.Config`, `auth.Config`, ...). For those
other modules' own env vars (`DB_*`, `JWT_*`, `VALKEY_*`, `MAIL_*`,
`OTEL_*`/`LOG_*`/`SENTRY_*`, `OUTBOX_*`, `SCHEDULER_*`, `S3_*`, ...), see each
module's own README or the public docs site's
[reference/configuration](https://gpsystem.hu/en/reference/configuration)
page; this page does not duplicate that cross-module table.

## server.Config (no prefix): the HTTP entry points

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:3000` | HTTP listen address |
| `SHUTDOWN_TIMEOUT` | `10s` | drain budget for in-flight requests on SIGTERM |
| `READ_HEADER_TIMEOUT` | `5s` | Slowloris defense: bounds how long a client may take to send request headers |
| `READ_TIMEOUT` | `30s` | bounds reading the whole request (headers + body); also required for graceful shutdown to close idle keep-alive connections instead of blocking until `SHUTDOWN_TIMEOUT` |
| `WRITE_TIMEOUT` | `0s` (disabled) | bounds writing the response; left disabled by default since it also caps slow/streaming responses (large downloads, SSE) |
| `IDLE_TIMEOUT` | `120s` | how long an idle keep-alive connection is kept open |
| `CORS_ORIGINS` | `*` | comma-separated allowed origins |
| `BODY_LIMIT` | `4194304` | max request body bytes; a value at or below 0 is treated as this default, never as "unlimited" |
| `HTTP_EXPOSE_INTERNAL_ERRORS` | `false` | include the underlying error message, stack and wrap chain in 5xx responses; enable only in development, decoupled from `OTEL_DEV_MODE` so telemetry-dev logging can never accidentally leak internals to clients |

## worker.Config (nested, no prefix): the `cmd/worker` binary

| Variable | Default | Meaning |
|---|---|---|
| `WORKER_CONCURRENCY` | `10` | number of tasks processed simultaneously |
| `WORKER_QUEUES` | `default:1` | queue to weight map, e.g. `critical:6,default:3,low:1` |
| `WORKER_STRICT_PRIORITY` | `false` | serve higher-weight queues to exhaustion first |
| `WORKER_SHUTDOWN_TIMEOUT` | `30s` | graceful drain budget; unfinished tasks are requeued |
| `WORKER_DEFAULT_MAX_RETRY` | `25` | retry budget for listener/job tasks without an override |

`worker.Config` also embeds `github.com/gp-system/queue`'s `Config`
(`VALKEY_*`), `github.com/gp-system/events/scheduler`'s `Config`
(`SCHEDULER_*`), `telemetry.Config` (standard `OTEL_*`/`SENTRY_*`) and
`logx.Config` (standard `LOG_*`).

## realtime.Config (mixed prefix): the `cmd/realtime` binary

| Variable | Default | Meaning |
|---|---|---|
| `REALTIME_LISTEN_ADDR` | `:3000` | container-internal listen address |
| `REALTIME_DRAIN_TIMEOUT` | `20s` | graceful-shutdown budget; keep under the deployment's stop-grace-period |
| `REALTIME_READ_HEADER_TIMEOUT` | `5s` | Slowloris defense on the connect handshake |
| `REALTIME_CHANNEL_PREFIX` | `rt` | Valkey channel namespace, shared with every `notify/broadcast.Publisher` |
| `REALTIME_MAX_CONNECTIONS` | `0` (unlimited) | per-instance stream cap; over it, connect attempts get 503 |
| `CORS_ORIGINS` | `*` | reuses the same variable name as `server.Config` |

`realtime.Config` also embeds `github.com/gp-system/queue`'s `Config`
(`VALKEY_*`, the same broker the queue/worker use), `github.com/gp-system/auth`'s
`Config` (`JWT_*`, the same token issuer every module trusts),
`telemetry.Config` (standard `OTEL_*`/`SENTRY_*`) and `logx.Config`
(standard `LOG_*`).

## telemetry.Config and logx.Config (nested in server.Config / worker.Config / realtime.Config, no prefix)

`telemetry.Config{Otel, Sentry}` and `logx.Config` are what all three chassis
configs above hold, as two separate fields (`Telemetry` and `Log`).
`app.Telemetry` validates `logx.Config`, builds a console
`slog.Handler` with it when dev mode or `LOG_STDOUT` calls for one, and passes
it into `telemetry.Setup` via `telemetry.WithLogHandler`/`WithLogLevel`, which
composes OTel bootstrap, that handler and the optional Sentry integration
into one default logger. The `OTEL_*`/`SENTRY_*` variables belong to the
standalone `github.com/gp-system/telemetry` module (and its `telemetry/otelx`,
`telemetry/sentryx` subpackages); `LOG_*` belongs to the standalone
`github.com/gp-system/logx` module, which `telemetry` has no dependency on.
See each module's README or the docs site reference page for the full
variable list.
