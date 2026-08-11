# gpsystem

A Go framework for modular backends: a scaffolding CLI, a service chassis
and generators, built on top of standalone, independently usable modules.
`server` is the HTTP chassis, built on [chi](https://github.com/go-chi/chi) /
`net/http`; the signal-driven graceful-shutdown lifecycle it shares with the
worker chassis lives in `app`.

One consumer pattern: every module gets its own entry point (`cmd/<module>/main.go`),
all foundations come from the framework: no copy-pasted bootstrap code. See
`examples/minimal/main.go` for a runnable, single-file version of the same
pattern:

```go
func main() {
	cfg := envconf.MustLoad[config.Config]()
	ctx := context.Background()
	pool := pg.MustNewPool(ctx, cfg.DB)

	err := server.Run(ctx, cfg.Server, func(r chi.Router) error {
		api := chi.NewRouter()
		deps := news.Dependencies{
			DB:         pg.NewDB(pool),
			Transactor: pg.NewTransactor(pool),
		}
		news.RegisterApi(api, deps)
		r.Mount("/api/v1", api)
		return nil
	}, server.WithCloser("pgxpool", func(context.Context) error {
		pool.Close()
		return nil
	}))
	if err != nil {
		log.Fatal(err)
	}
}
```

That ~20-line `main` gets: OTel traces/metrics/logs, slog with trace
correlation, RFC 9457 `application/problem+json` errors, request validation,
CORS, panic recovery and signal-driven graceful shutdown (drain in-flight
requests → closers LIFO → flush telemetry).

## Standalone modules

Everything that isn't the CLI or the HTTP/worker/realtime chassis lives in
its own publicly `go get`-able module under `github.com/gp-system/`, pinned
by this repo's own `go.mod` (see `internal/generator/modules.go`). Each is
independently usable (no dependency on this repo); see `docs/architecture.md`
for how the framework composes them.

| Module | Import path | What it gives you |
|---|---|---|
| errs | `github.com/gp-system/errs` | Structured errors: stack + code + public message, `New`/`Wrap`/`Define` |
| envconf | `github.com/gp-system/envconf` | Typed env config loading (`Load`/`MustLoad`, struct tags, `.env` support) |
| httperr (+ `httperr/validate`) | `github.com/gp-system/httperr` | RFC 9457 `Problem` type + helpers + error handler; `validate` wires go-playground/validator to 422 responses |
| paginate | `github.com/gp-system/paginate` | Page/perPage normalization + `Page[T]` |
| auth (+ `auth/rbac`, `auth/policy`) | `github.com/gp-system/auth` | JWT (HS256) issue/parse + Bearer middleware → `rbac.Identity`, `RequireRole`/`RequirePermission`; resource-level `policy` enforcers |
| dbx (+ `dbx/pg`, `dbx/bunx`, `dbx/seed`) | `github.com/gp-system/dbx` | Database contract: `Config` + `Transactor`, implemented by `dbx/pg` (pgxpool) or `dbx/bunx` (bun); two repositories share one tx via `Transactor.WithinTransaction`; `dbx/seed` for seeders |
| queue | `github.com/gp-system/queue` | Valkey/asynq foundation: connection config, PING-checked client, enqueue options, the event `Envelope` (OTel trace propagation) |
| events (+ `events/outbox`, `events/scheduler`) | `github.com/gp-system/events` | `Dispatcher`, generic `Listen[T]`, fan-out to independently-retried listener tasks; `outbox` for a transactional outbox relay; `scheduler` for cron/interval jobs, replica-safe via a Valkey lease |
| notify (+ `notify/broadcast`, `notify/database`) | `github.com/gp-system/notify` | Multi-channel notifications (`Hub`, `Mailable`/`Databasable`/`Broadcastable`) over mail, a database store and a realtime broadcast channel |
| mail (+ `mail/smtp`, `mail/mjml`) | `github.com/gp-system/mail` | Mailer interface + SMTP transport + MJML template rendering |
| telemetry (+ `telemetry/otelx`, `telemetry/sentryx`) | `github.com/gp-system/telemetry` | Facade composing OTel bootstrap and the optional Sentry integration; `Setup` takes the default logger's extra sinks as `slog.Handler` options |
| logx (+ `logx/monolog`) | `github.com/gp-system/logx` | Default slog logger composition (fanout, minimum-level filter, console handler); no dependency on `telemetry` |
| storage (+ `storage/memory`, `storage/local`, `storage/storagetest`) | `github.com/gp-system/storage` | Object storage abstraction (`Driver`/`Disk`/`Manager`) with in-memory and local-disk drivers |
| minio-storage-driver | `github.com/gp-system/minio-storage-driver` | S3-compatible `storage.Driver` built on minio-go (declared package name: `s3`); standalone so only projects that need S3 pull the SDK |

The framework itself directly imports `auth`, `envconf`, `errs`, `events`,
`httperr`, `logx`, `notify`, `queue` and `telemetry`, with `dbx` and `mail`
pulled in transitively through `events`/`notify`; `paginate`, `storage` and
`minio-storage-driver` are not used by the framework's own code but are available
to any project (generated or not).

## Chassis packages

What actually remains in this repo beyond the CLI: four thin packages that
compose the modules above into runnable processes.

| Package | What it gives you |
|---|---|
| `app` | The engine-agnostic process lifecycle: signal-driven graceful shutdown (`Run`, `Params`, `Closer`) shared by every chassis below |
| `server` | The HTTP chassis, built on chi / net/http: `Config`, `New()` (router with Problem-writing recover/404/405, `StrictValidator`), `Run()` (app factory + lifecycle); `WithCloser`, `WithMiddleware`, `WithHTTPServer`, `WithoutTelemetry` |
| `worker` | `Run()`: background-worker chassis: asynq server + outbox relay + schedule runner under the same graceful shutdown as the HTTP chassis |
| `realtime` | `Run()`: the realtime gateway chassis (`centrifugal/centrifuge`); long-lived streaming connections under its own lifecycle, deliberately separate from `server.Config` |

## Scaffolding CLI

Install it directly:

```sh
go install github.com/gp-system/framework/cmd/gpsystem@latest
```

Scaffold a new project with the installed binary, then the generated project
pins the CLI with the Go 1.24+ `tool` directive so every later command runs
as `go tool gpsystem`, reproducibly, without a separate install step:

```sh
gpsystem new project shop --module-path github.com/acme/shop
go tool gpsystem new module news             # cmd/news + api surface + .tsp
go tool gpsystem add surface news backoffice # extra surface under /backoffice/news
go tool gpsystem add handler news api getNews --method GET --path "/items/{id}"
go tool gpsystem add event news articlePublished          # event + outbox dispatcher wiring
go tool gpsystem add listener news articlePublished sendNewsletter --queue newsletters
go tool gpsystem add job news cleanupDrafts --cron "0 3 * * *"
go tool gpsystem add worker                  # retrofit the worker into a pre-worker project
```

Contributors working against a local checkout of the framework or a module
can use `new project`'s `--replace`/`--replace-dev` flags to write a
`replace` directive into the generated `go.mod` instead of resolving the
published version.

New projects include a `cmd/worker` binary (asynq server + outbox relay +
scheduler) alongside the HTTP entry points; scale it independently. `add event`
wires an outbox `Dispatcher` into the module's `Dependencies`, `add listener`
and `add job` register handlers the worker runs.

Contract pipeline (spec-first): **TypeSpec → OpenAPI 3.0 → oapi-codegen strict
server**, built on oapi-codegen's built-in `chi-server` routing templates
plus a minimal strict-template override the framework ships
(`internal/templates/oapi/`, copied into each project) with a golden +
compile test pinning the pair to a known-good oapi-codegen version.

All generators are safe by default: full file plan up front, never overwrite
(`--force` to override), `--dry-run` everywhere, existing files only modified
at `// gpsystem:*` anchor comments.

## Development

```sh
mise run build              # go build ./...
mise run test               # unit tests
```

See `docs/` for the architecture and codegen pipeline details.

### Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `docs:`, `refactor:`, ...), enforced by
[commitlint](https://commitlint.js.org/) with the standard
`@commitlint/config-conventional` rules; see `commitlint.config.mjs`.
CI lints every pushed/PR commit; to get instant feedback locally, install the
`commit-msg` hook once:

```sh
mise run hooks:install      # npm ci + git config core.hooksPath .githooks
```
