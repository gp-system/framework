# Architecture

## Module map

This repo (`github.com/gp-system/framework`) is thin on purpose: the CLI
(`cmd/gpsystem`, `internal/`) and four chassis packages (`app`, `server`,
`worker`, `realtime`). Everything else is a standalone, independently
`go get`-able module under `github.com/gp-system/`, pinned by this repo's own
`go.mod` (see `internal/generator/modules.go`), with no dependency back on
this repo:

| Module | Import path | Layer |
|---|---|---|
| errs | `github.com/gp-system/errs` | Foundation: structured errors, everything else's error currency |
| envconf | `github.com/gp-system/envconf` | Foundation: typed env config loading |
| httperr (+`validate`) | `github.com/gp-system/httperr` | HTTP: RFC 9457 Problem rendering + request validation |
| paginate | `github.com/gp-system/paginate` | HTTP: page/perPage normalization |
| auth (+`rbac`, `policy`) | `github.com/gp-system/auth` | Security: JWT, RBAC, resource policies |
| dbx (+`pg`, `bunx`, `seed`) | `github.com/gp-system/dbx` | Database: connection + transaction contract |
| storage (+`memory`, `local`, `storagetest`) | `github.com/gp-system/storage` | Storage: object storage abstraction |
| minio-storage-driver | `github.com/gp-system/minio-storage-driver` | Storage: S3-compatible driver (package name `s3`) |
| queue | `github.com/gp-system/queue` | Async: Valkey/asynq foundation |
| events (+`outbox`, `scheduler`) | `github.com/gp-system/events` | Async: event dispatch, transactional outbox, scheduling |
| mail (+`smtp`, `mjml`) | `github.com/gp-system/mail` | Mail: mailer interface, SMTP transport, MJML rendering |
| notify (+`broadcast`, `database`) | `github.com/gp-system/notify` | Mail/async: multi-channel notifications |
| telemetry (+`otelx`, `sentryx`) | `github.com/gp-system/telemetry` | Observability: OTel, Sentry, default logger composition |
| logx (+`monolog`) | `github.com/gp-system/logx` | Observability: default slog logger console formatting |

`errs` sits at the bottom (every other module's error currency); the kit's
own chassis packages compose the rest on top, but each module works standalone
against plain `net/http` or any other Go program. The kit directly imports
`auth`, `envconf`, `errs`, `events`, `httperr`, `logx`, `notify`, `queue` and
`telemetry`; `dbx` and `mail` come in transitively through `events`/`notify`.
`telemetry` has no dependency on `logx`: the chassis configs hold their own
`Log logx.Config` field and wire it into `telemetry.Setup` as a plain
`slog.Handler` (see the Observability section below). `paginate` and
`storage` are not used by the kit's own code but are available to any
project through the CLI's generators.

## The server package and the process lifecycle

`server` is the single HTTP chassis package: `server.Config`, `server.New`
(builds the chi router: kit error handling, `server.StrictValidator`, panic
recovery that writes a Problem, 404/405 handlers, body-limit middleware) and
`server.Run` (owns the process lifecycle end to end). The signal-driven
graceful-shutdown lifecycle (`app.Run`, `app.Params`, `app.Closer`) lives in
the separate `app` package, shared by `server.Run` and the worker chassis: a
queue-consumer binary has no business depending on a package named `server`.
Neither `server` nor `app` has any HTTP-framework dependency beyond chi
itself, which is a thin `net/http` router, not a framework.

There is no engine choice: the framework generates chi (net/http) backends
only, and the CLI has no `--engine` flag. `gpsystem.yaml` carries no engine
field at all. The generated entry points (`cmd/<module>/main.go`,
`register.go`, the generated `gen/` package) are chi-shaped throughout.

## Observability: otelx and sentryx composed by telemetry, logx standalone

Three packages each own one observability capability, and a fourth composes
two of them for the chassis. `logx` (`github.com/gp-system/logx`) is a fully
standalone module: `telemetry` has no dependency on it at all, or on any
other logging library.

- `otelx`: pure OpenTelemetry SDK bootstrap (traces, metrics, an OTLP log
  bridge). No opinion on console formatting or optional integrations.
- `logx`: the default slog logger's composition (fanout, minimum-level
  filter, console handler). `logx/monolog` is a console format driver
  (PHP/Monolog-style lines); other formats are siblings.
- `sentryx`: the optional Sentry integration (span exporter + slog capture
  handler + per-request hub), gated on `SENTRY_DSN`, framework-free.
- `telemetry`: the facade `server.Config.Telemetry` / `worker.Config.Telemetry`
  actually holds (`telemetry.Config{Otel, Sentry}`, no `Log` field).
  `telemetry.Setup` composes `otelx` and `sentryx`: it sets up Sentry first,
  wires its span exporter into `otelx.Setup` via `otelx.WithSpanExporter`,
  then fans the OTLP bridge, the Sentry slog handler and any handler passed
  via `telemetry.WithLogHandler` into one `slog.Default()`, gated by the
  minimum level from `telemetry.WithLogLevel`. Non-chassis consumers of
  `otelx` alone (a CLI, a migration tool) never link sentry-go.

The chassis wires the two together: `server.Config`/`worker.Config`/
`realtime.Config` each hold their own `Log logx.Config` field alongside
`Telemetry telemetry.Config`, and `app.Telemetry` validates it, builds
`logx.ConsoleHandler` when dev mode or `LOG_STDOUT` calls for one, and passes
it to `telemetry.Setup` via `WithLogHandler`/`WithLogLevel`. `app.Telemetry`
has no dependency on `app.Run`, so a standalone consumer of `telemetry` (no
chassis at all) can call it directly instead of hand-rolling the same
`slog.Handler` wiring.

## The chassis (`server.Run`)

`server.Run(ctx, cfg, register, opts...)` owns the process lifecycle:

1. `app.Telemetry` (validates `cfg.Log`, builds the console
   handler when dev mode or `LOG_STDOUT` calls for one) delegates to
   `telemetry.Setup`: Sentry, then OTel (resource, OTLP exporters via
   autoexport honoring standard `OTEL_*` env vars, W3C propagators), then the
   default slog logger (OTLP bridge + optional console + optional Sentry
   handler, fanned out and level-gated). `OTEL_DEV_MODE=true` skips OTel
   exporters and always adds the console handler.
2. App factory: `server.New` builds the chi router from the named
   middleware stack (`server.Stack`, built by `defaultStack` from the
   `httpmw` package): request id, real-ip (with `HTTP_TRUST_PROXY`), OTel
   span, Sentry hub, Problem-writing panic recovery (a panic becomes an
   `errs` error carrying a panic-site stack), validator injection (with
   `WithValidator`), structured slog access log (with `HTTP_ACCESS_LOG` or
   dev mode), CORS, body limit, timeout (with `HTTP_REQUEST_TIMEOUT`);
   plus 404/405 Problem handlers. `WithStack` inserts/replaces/removes
   entries by name (the `server.MW*` constants); `WithMiddleware` appends.
   Request validation is separate, a strict middleware
   (`server.StrictValidator`) inside the generated per-surface chain.
3. `register(router)`: the application mounts its modules.
4. `ListenAndServe` in a goroutine; `signal.NotifyContext(SIGINT, SIGTERM)`.
5. On signal (or parent ctx cancel): graceful shutdown (`Shutdown` with
   `ShutdownTimeout`) drains in-flight requests → `WithCloser` closers run
   LIFO → telemetry flushes last (so closers' spans still export). A second
   signal force-kills.

Options: `WithCloser`, `WithMiddleware`, `WithStack`, `WithValidator`,
`WithoutTelemetry`; `WithHTTPServer` is the escape hatch for tuning the
underlying `*http.Server` directly.

The `httpmw` package holds the exported net/http middleware building blocks
(RequestID, RealIP, Recoverer, RequestHub, AccessLog, Otel + FilterPath,
CORSAPI/CORSStreaming), shared by `server` and `realtime`; it stays a kit
package rather than a `gp-system/*` module because its only consumers are
in-kit (the promotion policy below). The worker's asynq chain is exported
separately (`worker.HubMiddleware`/`RecoverMiddleware`/`TracingMiddleware`
plus `WithTaskMiddleware`); the asynq middleware shape and error model are
incompatible with net/http, so only the context helpers
(`sentryx.WithRequestHub`) are shared across that boundary. `realtime` takes
`WithMiddleware` too, applied inside its fixed chain.

## Error model

Everything renders as RFC 9457 `application/problem+json`:

- `*httperr.Problem` (returned by handlers/services): as-is
- validation failures (`server.StrictValidator` → `validate.Validator`):
  422 with per-field `errors[]`
- `*http.MaxBytesError` (body over the limit): 413
- anything else: 500; the cause goes to slog (with trace context), the
  client gets no internals unless dev mode

An `errs` error anywhere in the chain maps to 500 with its public message as
`detail` and its code as the `code` member. Its chain and stack are logged on
every 5xx; they also appear in the response, as the `stack` and `chain`
extension members, but only in dev mode (`WithExposeInternal`), never in
production. These two members are deliberately absent from `errors.tsp`.

`github.com/gp-system/auth` (and its `rbac`/`policy` subpackages) is
independent of `errs`/`httperr`: it returns plain sentinel errors
(`rbac.ErrUnauthenticated`/`ErrForbidden`,
`auth.ErrMissingToken`/`ErrInvalidToken`/`ErrInvalidClaims`,
`*policy.Denial`) and never writes a response. `server.WriteError` maps
those onto Problems before delegating everything else to
`httperr.WriteError`, so generated register snippets wire
`ResponseErrorHandlerFunc: server.WriteError` rather than
`httperr.WriteError` directly.

The mapping runs through `server.WriteError` / `httperr.WriteBadRequest`,
wired into the generated server's error hooks (`ErrorHandlerFunc`,
`RequestErrorHandlerFunc`, `ResponseErrorHandlerFunc`) by the register
snippet, and into the router's panic/404/405 handlers by `server.New`.

The generated strict-server templates propagate handler errors **unchanged**
so this mapping is the single source of truth.

## Cross-repository transactions (`github.com/gp-system/dbx`)

Repositories hold a `pg.DBTX` (sqlc-compatible interface, from `dbx/pg`).
`pg.DB` resolves every call against the transaction carried by `ctx` when
present, else the pool. `dbx.Transactor.WithinTransaction` (implemented by
`dbx/pg` or `dbx/bunx`) begins a tx, injects it into ctx, commits/rolls back.
Two independent repositories called inside one closure therefore share one
transaction: no `WithTx` plumbing in either of them. Nested
`WithinTransaction` joins the outer tx (flat, no savepoints).

**Named connections.** A module's default connection is `cfg.DB`/`pool`, one
pgxpool per binary. `add db <module> <name> --connector pgx|bun` adds another,
independent pgxpool (its own `DB_<NAME>_*` config, pool, closer and
`Dependencies` fields: `<Name>DB`/`<Name>Transactor`) alongside it; the
connector is chosen per connection, not per project, because a pgx-native
`dbx/pg` executor and a `dbx/bunx` one cannot share a single transaction:
`WithinTransaction` on one connection never spans the other. `add surface
<module> <surface> --db <name>` binds the module's repository skeleton to a named
connection, splitting its `repository/` directory into `repository/<name>/`
(the default, unbound case stays a flat `repository/`). `cmd/migrate` only
ever targets the project's default connection.

## Events, outbox and the worker (`github.com/gp-system/events` + `worker`)

Background work runs in a **separate `cmd/worker` binary** built on the same
`app.Run` lifecycle as the HTTP servers, so it scales horizontally and
independently of the API. It runs three things under one graceful shutdown: an
asynq server (task processing), the outbox relay, and the schedule runner.

**Events fan out.** Application code dispatches an event (`events.Event`, a
struct with a stable `EventName()`); listeners subscribe by type with the
generic `events.Listen[T]`. Dispatch enqueues one `event:<name>` task; the
worker's fan-out handler expands it into one `listener:<event>:<name>` task per
registered listener, each with its own retry budget. A failing listener retries
without re-running the others. Listener tasks carry a deterministic asynq
`TaskID` (envelope id + listener) so re-running the fan-out is idempotent.

**Guaranteed delivery via a transactional outbox.** The generated dispatcher is
`outbox.NewDispatcher`: it writes the event into the `outbox_events` table
through `pg.DBTX`, so a `Dispatch` inside `WithinTransaction` commits or rolls
back atomically with the business write: no event is emitted for a rolled-back
change, and none is lost after a commit. The relay polls committed rows with
`FOR UPDATE SKIP LOCKED` (safe across N worker replicas) and forwards them to
Valkey, where asynq's retry/recovery takes over. The deterministic `TaskID`
(= the outbox `event_id`) makes the crash-after-enqueue-before-mark window a
no-op. Delivery is **at-least-once end to end, so listeners must be
idempotent**: use `events.MetaFromContext(ctx).ID` as an idempotency key.

**Scheduling.** `scheduler.Schedule` registers cron/interval events and jobs
(a single handler, no fan-out). The runner uses asynq's `Scheduler` guarded by a
Valkey lease leader election: only the lease holder fires each tick, so N worker
replicas do not duplicate. Scheduled events go through a `schedule:<name>`
trigger whose per-fire asynq task id becomes the envelope id, giving each fire a
fresh identity while keeping retries of the same fire idempotent.

Shutdown order in the worker: stop the schedule and relay loops, drain in-flight
tasks (`WORKER_SHUTDOWN_TIMEOUT`; unfinished tasks are requeued), run closers
LIFO, flush telemetry.

## Module layout (generated)

```
cmd/<module>/main.go                  # ~20 lines, server.Run
cmd/worker/main.go                    # shared background worker: worker.Run
internal/modules/<module>/
├── <module>.go, errors.go            # the module root domain package (package <module>): shared business
│                                     #   logic: entities/views, rules, sentinels (ErrNotFound)
├── repository/                       # the module's single persistence layer (row structs + interface + impl);
│                                     #   repository/<name>/ when bound to a named connection
├── events/<event>.go                 # events this module owns (add event)
├── listeners/                        # package listeners: event listeners
│   ├── register.go                   #   Register(reg, deps) wiring
│   └── <name>.go                     #   one event listener (add listener)
├── jobs/                             # package jobs: scheduled jobs
│   ├── register.go                   #   Register(sched, deps) wiring
│   └── <name>.go                     #   one scheduled job (add job)
├── mail/                             # package mail: notifiers this module owns
│   ├── <name>.go                     #   payload + Notifier (add mail)
│   └── templates/<name>.{html,txt}.tmpl
├── notifications/                    # package notifications: multi-channel notifications
│   ├── <name>.go                     #   payload + Via + ToMail/ToDatabase (add notification)
│   └── templates/<name>.{html,txt}.tmpl
└── surfaces/                         # package surfaces: wiring + one folder per surface
    ├── register.go                   # Dependencies + one Register<Surface>(router, deps) per surface
    └── <surface>/                    # any name; "api" mounts at the module root
        ├── http/handler.go           # implements gen.StrictServerInterface; wraps service errors with errs
        ├── http/mapper/              # DTO mapping (kept empty, .gitkeep)
        ├── http/gen/                 # oapi-codegen output (never edit)
        ├── policy/policy.go          # per-user authz registry (@policy names)
        └── service/service.go        # thin audience-specific seam delegating to the module root
spec/typespec/modules/<module>/       # contract source of truth
api/openapi/<Ns>.yaml                 # emitted OpenAPI (committed)
api/oapi-codegen/<module>-<surface>.yaml
```

Dependency rule:
`http → service → <module> (module root) → repository(interface)`; the
repository package implements the interface (pgx or bun); the module root
never imports a surface package; handlers never import repository (sentinels
are re-exported through the module root/service); http never touches the DB.
The wiring (`Dependencies` + `Register<Surface>`) lives in the `surfaces`
package, not the module root, so the root stays importable from every
surface service without a cycle.

A module folder holds four distinct kinds of thing: **surfaces**
(`surfaces/<surface>/`, vertical HTTP bundles per audience), **shared logic**
(the module root package + `repository/`, one per module), **trigger units**
(`jobs/`, `listeners/`, alternative entry points wired into `cmd/worker`),
and **contracts & content** (`events/`, `mail/`, `notifications/`: what the
module produces: event payloads, message content/templates). Channel/driver
code (SMTP, DB, the notify channels) lives in the kit or in
`internal/platform`; content lives in the module; triggers live in
`jobs`/`listeners`. A surface is a per-audience HTTP bundle (the pre-rename
docs called it a "domain"); the module root package (named after the module,
so callers read `gallery.Service`, `gallery.ErrNotFound`) holds the business
rules shared by the surfaces (plain structs and methods, not a DDD domain
layer), and each surface's `service/` is a thin audience-specific seam (DDD
application-service role). See the docs site's Project structure and Design
patterns pages for the full reasoning.

Authorization is declarative: `@permission` / `@policy` decorators in the
surface's `.tsp` emit `x-permission` / `x-policy` OpenAPI extensions,
the strict templates emit them as `PermissionByOperation` /
`PolicyByOperation` maps in the gen package, and `register.go` chains the
kit's `policy.Enforcer` strict middleware, which resolves `@policy` names
against the surface's `policy/` registry
(fail-closed for unregistered names).

## Anchors

Generators modify existing files only at marker comments:

| Anchor | File | Inserted by |
|---|---|---|
| `gpsystem:modules` | `spec/typespec/main.tsp` | new module / add surface |
| `gpsystem:imports`, `gpsystem:surfaces` (file level) | `surfaces/register.go` | new module / add surface |
| `gpsystem:registrations` | `cmd/<module>/main.go` | new module / add surface |
| `gpsystem:operations` | `<surface>.tsp` | add handler |
| `gpsystem:imports`, `gpsystem:modules`, `gpsystem:deps-init(<module>)` | `cmd/worker/main.go` | new module / add worker / add event |
| `gpsystem:listeners`, `gpsystem:worker-imports` | `listeners/register.go` | add listener |
| `gpsystem:jobs`, `gpsystem:job-imports` | `jobs/register.go` | add job |
| `gpsystem:services` | `compose.yml` | add worker (Valkey service, older projects), add realtime |
| `gpsystem:env` | `.env.example` | add worker / add db (older projects), add realtime |
| `gpsystem:pools` | `cmd/<module>/main.go`, `cmd/worker/main.go` | add db (named connection's pool), add mail (shared mailer construction), add notification (shared notifier + publisher construction) |
| `gpsystem:closers` | `cmd/<module>/main.go`, `cmd/worker/main.go` | add db (named connection's closer), add notification (publisher closer, once `add realtime` has run) |
| `gpsystem:config` | `internal/platform/config/config.go` | add db (named connection's config field), add mail (`Mail smtp.Config`), add realtime (`Realtime realtime.Config`) |
| `gpsystem:dependencies` | `surfaces/register.go` | add event (dispatcher field), add mail (`Mailer mail.Mailer` field), add notification (`Notifier notify.Sender` field) |
| `gpsystem:dependencies-init`, `gpsystem:deps-init(<module>)` | `cmd/<module>/main.go`, `cmd/worker/main.go` | add event (dispatcher construction), add mail (`Mailer: mailer,`), add notification (`Notifier: notifier,`) |
| `gpsystem:topics` | `cmd/realtime/main.go` | add realtime (shared topic-channel authorization) |
| `gpsystem:api-middleware` | `cmd/<module>/main.go` (after `api := chi.NewRouter()`) | add auth (`api.Use(server.IdentityMiddleware(cfg.JWT))` into every module entry point; modules generated after `add auth` render it directly) |
| `gpsystem:middleware` | `cmd/<module>/main.go` (options list) | nobody yet; the documented seam for `server.WithStack`/`WithMiddleware` insertions |
| `gpsystem:surface-middleware` | `surfaces/register.go`, inside each surface's `router.Route` closure | nobody yet; the documented seam for route-group middleware (`r.Use(server.RequireRole(...))`) |

Insertions are idempotent and re-formatted with goimports. Removing an
anchor makes the corresponding generator fail loudly instead of guessing.

Each surface is registered by its own exported `Register<Surface>(router,
deps)` function (e.g. `RegisterWeb`, `RegisterAdmin`), appended at the
file-level `gpsystem:surfaces` anchor in `surfaces/register.go`; `add
surface` also inserts the matching `<module>surfaces.Register<Surface>(api,
deps)` call at `gpsystem:registrations` in the entry point (the entry points
import the surfaces package under the `<module>surfaces` alias). Per-surface middleware (e.g.
`server.RequireRole` on an admin group) goes at the
`gpsystem:surface-middleware` anchor inside that surface's `router.Route`
closure. Authentication itself needs no per-surface wiring: `add auth`
installs `server.IdentityMiddleware` on the api router of every module
entry point (the `gpsystem:api-middleware` anchor), so `@permission` /
`@policy` tags enforce out of the box while public operations stay
reachable anonymously.

## Promoting shared code into a module or the kit

Maintainer policy for the top rung of the docs site's "shared code" decision
ladder (project-agnostic code promoted into a standalone `gp-system/*`
module, or, for chassis-specific wiring, into this kit): promotion needs a
second real consumer. Promoting on first use freezes an app-specific
decision into a public API before it's actually general; wait for a genuine
second project that needs the code before extracting it.
