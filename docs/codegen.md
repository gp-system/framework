# Codegen pipeline

```
spec/typespec/**.tsp ──tsp compile──▶ api/openapi/<Namespace>.yaml
        │                                     │
        │ (source of truth, hand-edited       │ oapi-codegen v2.8.x
        │  + gpsystem generators)             │ + chi-server + strict overrides
        ▼                                     ▼
  gpsystem CLI                 internal/modules/<m>/<d>/http/gen/api.gen.go
```

`mise run generate` runs the whole chain: `tsp compile` → `go generate ./...`
→ `go mod tidy` → `go build ./...`.

## TypeSpec conventions

- Each surface is its own `@service` with `@server("/api/v1<base>")`; the
  emitter names the output file after the namespace, so namespaces are
  deterministic: `Project.Module` (api), `Project.Module<Surface>` otherwise.
  **Never nest one service namespace inside another**: routes merge and
  `duplicate-operation` errors follow.
- `shared/errors.tsp` mirrors `httperr.Problem`; keep them in sync.
- OpenAPI version is pinned to 3.0 (oapi-codegen does not read 3.1).

## The oapi-codegen templates

Projects use oapi-codegen's **built-in** `chi-server` routing templates
plus a minimal override set from `internal/templates/oapi/`: the two
strict templates (`strict/strict-interface.tmpl`, `strict/strict-http.tmpl`)
with a single delta: the `Object` suffix is dropped from the generated
request/response type names (`GetNewsRequest`, not `GetNewsRequestObject`),
so the business handler templates stay simple to write against.
`internal/templates/oapi_test.go` pins the contract (identifiers the
register snippet uses, the `StrictHandlerFunc` shape
`server.StrictValidator` instantiates, no `Object` suffix) and compiles the
output against a golden file (refresh with `UPDATE_GOLDEN=1`). Bumping
oapi-codegen breaks this test first, not consumer projects.

## Authorization extensions (x-permission / x-policy)

The strict templates (`oapi/strict/strict-interface.tmpl`,
`oapi/strict/strict-http.tmpl`) read the `x-permission` / `x-policy`
operation extensions off `.Spec.Extensions` and emit them as
`PermissionByOperation` / `PolicyByOperation` maps keyed by the normalized
`OperationId`, the same value the strict middleware chain receives, which
is what lets the kit's `policy` enforcer match tags to requests. The
extensions originate from the `@permission` / `@policy` decorators the
project scaffold ships in `spec/typespec/lib/` (a tiny JS lib using
`setExtension` from `@typespec/openapi`). Aliased (`$ref`ed) paths are
skipped: tags are honored on the canonical operation only. Coverage: tagged
operations in `testdata/spec.yaml` and map assertions in the template test.

## Event / listener / job generators

Beyond the TypeSpec pipeline, four generators scaffold the worker side. They are
plain tree renders plus anchor insertions (no TypeSpec involved):

- `add event <module> <name>`: renders `internal/modules/<module>/events/<name>.go`
  and, on the first event, wires an outbox `events.Dispatcher` into the module's
  `Dependencies` (`surfaces/register.go`) and constructs it in both entry points
  (`cmd/<module>/main.go` and `cmd/worker/main.go`). Idempotent: a second event
  reuses the existing dispatcher. Records the event in `gpsystem.yaml`
  (`ModuleEntry.Events`) so `add listener` can validate it.
- `add listener <module> <event> <name>`: renders a listener stub into the
  module's `listeners/` subpackage and registers it at `gpsystem:listeners` in
  `listeners/register.go`. `<event>` is `name` for the module's own event or
  `producer.name` to subscribe cross-module. `--queue` routes the listener to a
  named asynq queue.
- `add job <module> <name> --cron "..."`: renders a job stub into the module's
  `jobs/` subpackage and registers it at `gpsystem:jobs` in `jobs/register.go`.
- `add worker`: retrofits the whole worker layer (the `cmd/worker` binary,
  per-module `listeners/` and `jobs/` subpackages, the outbox migration,
  Valkey/worker config) into a project generated before the worker feature
  existed. New projects and `new module` already include it. For `compose.yml` /
  `.env.example` that predate the `gpsystem:services` / `gpsystem:env` anchors,
  it prints the block to paste instead of failing (those files are not compiled).

Any command that requires an anchor and can't find it (e.g. `// gpsystem:pools`
was deleted from `cmd/worker/main.go`) still fails the command, but
`InsertAtAnchor` (`internal/generator/anchors.go`) prints an `AnchorFixHint`
first: the rendered snippet plus the marker line to restore just below it, so
a future run can find the anchor again. `optionalInsert`
(`internal/cli/add_worker.go`) reuses the same helper for its non-fatal path.

The `listeners`/`jobs` subpackages are their own Go packages (like a surface's
`http`/`service`): they import the module root package for its `Dependencies`
type, never the other way around, and the worker main wires them via aliased
imports (`<mod>listeners.Register(reg, <mod>Deps)` / `<mod>jobs.Register(sched,
<mod>Deps)`). `add listener`/`add job` fail a preflight check if the
subpackage's `register.go` is missing.

Import anchors are inserted one line at a time so they stay idempotent after
goimports reorders them; the worker main's per-module deps use a
`gpsystem:deps-init(<module>)` anchor delimited by parentheses to avoid prefix
collisions between module names.

## Mail generator

`add mail <module> <name>` renders a notifier tree
(`internal/templates/mail/go/`) into `internal/modules/<module>/mail/<name>.go`
: a payload struct + `<Name>Notifier` wrapping an injected `mail.Mailer`, plus
its `templates/<name>.{html,txt}.tmpl` (copied verbatim from
`internal/templates/mail/assets/`, **never** rendered by the engine: they are
html/template and text/template sources consumed at runtime by
`mail.Template`, and would fail to render against `generator.Data` since their
`{{...}}` actions reference the notifier's own payload fields instead, see
`CopyTree`'s doc comment for the same rule applied to the oapi-codegen
templates). A generated test uses `mail.NewMemory()`.

On the project's first mail notifier, `wireMailer` (`internal/cli/add_mail.go`)
wires the shared transport: `Mail smtp.Config` at `gpsystem:config` in platform
config, `Mailer mail.Mailer` at `gpsystem:dependencies` in the module's
`surfaces/register.go`, and `mailer := smtp.MustNewIfConfigured(ctx, cfg.Mail)` +
`Mailer: mailer,` at `gpsystem:pools`/`gpsystem:dependencies-init` (or
`gpsystem:deps-init(<module>)` in the worker) in every entry point, the same
anchor set `add db` uses for a named connection's pool/config/deps. Records the
notifier in `gpsystem.yaml` (`ModuleEntry.Mail`).

This generator surfaced a pre-existing anchor-idempotency bug: `InsertAtAnchor`
used to check the whole file for a snippet's presence, so two modules rendering
the *same* snippet text at their own distinct anchor occurrence (e.g. `Mailer:
mailer,` at each module's own `gpsystem:deps-init(<module>)` in the shared
`cmd/worker/main.go`) would see the first module's line and silently skip the
second's; this affected `add event`'s dispatcher wiring too, just never
surfaced since no test exercised two modules with events. `scopedContent`
(`internal/generator/anchors.go`) now bounds the check to the section between
the nearest earlier anchor comment and the current one, so sibling anchors
don't false-positive on identical content. It matches on `// gpsystem:` / `#
gpsystem:` specifically, not the bare substring `"gpsystem:"`, since a
rendered file could legitimately contain that text elsewhere too, e.g. as
part of a host:container path in a `--replace-dev` Docker volume mount.

## Notification generator

`add notification <module> <name>` renders a notification tree
(`internal/templates/notification/go/`) into
`internal/modules/<module>/notifications/<name>.go`: an empty payload struct
implementing `notify.Notification` (+ `notify.Mailable`/`notify.Databasable`),
`Via` defaulting to `[database, mail]`, plus its
`templates/<name>.{html,txt}.tmpl` (copied verbatim from `mail/assets/`,
reusing the same placeholder pair `add mail` uses, for the same reason: never
engine-rendered). A generated test exercises it over a memory mail channel
and a memory database store.

On the project's first notification, `wireNotifier`
(`internal/cli/add_notification.go`) wires a shared `notify.Sender`: reuses
`add mail`'s `import-smtp`/`mail-config` snippets in platform config (no new
env needed: the notify mail channel shares the same `smtp.Config`), adds
`Notifier notify.Sender` at `gpsystem:dependencies` in `surfaces/register.go`, and
constructs the `Hub` at `gpsystem:pools` in every entry point:
`notify.NewHub(notify.NewMailChannel(mailer),
notifydatabase.NewChannel(notifydatabase.NewStore(pg.NewDB(pool))))`. The
database channel always persists through the kit's pgx executor regardless of
the project's `--db` choice: `pool` (a `*pgxpool.Pool`) is in scope at
`gpsystem:pools` in every entry point, whereas a `bun.DB` is only constructed
later, inside the per-module `Dependencies` literal, so threading it through
would require a deeper template change for no real benefit (the notify table
is kit-owned infrastructure, not a business-domain repository).

If `add realtime` has already run (detected the same way `workerWired` detects
the worker: a filesystem check for `cmd/realtime/main.go`), the same
`gpsystem:pools` insertion also constructs a `notify/broadcast.Publisher` and
adds `notify.NewBroadcastChannel(...)` to the `Hub`, plus its closer at
`gpsystem:closers`. The notifier is constructed **once per entry point**,
mirroring `add mail`'s `mailer` and `add db`'s named pool, so a second
notification's `wireNotifierInit` call first checks whether
`"notifier := notify.NewHub("` already appears in the target file and, if so,
skips the whole `notifier-pool-init`/import/closer insertion entirely,
inserting only the `Notifier: notifier,` deps-init line. This guard exists
because, unlike `mailer-pool-init` or `dispatcher-init`, the *content* of
`notifier-pool-init` legitimately differs across calls (whether `Realtime` is
true), the anchor-scoped idempotency check in `anchors.go` correctly does
NOT dedupe two different renderings, so without this guard a second module
added after `add realtime` would emit a second, conflicting `notifier :=` in
the shared `cmd/worker/main.go` and fail to compile. Verified against both a
pgx and a bun scaffold, adding a notification before and after
`add realtime`, across two modules.

Records the notification in `gpsystem.yaml` (`ModuleEntry.Notifications`) and
writes the `notifications` table migration (`writeNotificationsMigration`,
mirroring `writeOutboxMigration`: scans existing migrations for `CREATE TABLE
notifications` first, so it is a no-op on a project that already has it).
Both share `nextMigrationPrefix`, which allocates a goose timestamp prefix
(`YYYYMMDDHHMMSS`, UTC) rather than a sequential number, so retrofitting the
migration into an older project cannot collide with one a teammate generated
on another branch.

## Realtime gateway

`add realtime` renders `internal/templates/realtime/` into `cmd/realtime/main.go`
: a `realtime.Run` call with a `// gpsystem:topics` anchor for registering
`TopicAuth` callbacks, and wires `Realtime realtime.Config` into platform
config, plus a `compose.yml` service (path-routed under `/realtime` on the
project's `APP_HOST`, no `container_name` so `--scale` works, a Traefik
health-check label pointed at `/realtime/healthz`) and the matching
`.env.example` lines. Like `add worker`, it falls back to printing the block
to paste when the anchors predate the file.

The gateway itself (`realtime` package) deliberately does not reuse
`server.Config`/`server.Run`: it holds long-lived streaming connections
(WriteTimeout effectively unbounded, no request-body handling), a posture
that would silently retune the API's own `server.Config` if shared, since
every compose service reads the same `.env`. It is built directly on
`app.Run`, mirroring `worker.Run`'s shape (`Config`, `RegisterFunc`, LIFO
closers) rather than the HTTP chassis. Transport is
`centrifugal/centrifuge` (the library behind Centrifugo and Grafana Live),
chosen after a survey of the Go realtime ecosystem for being the only option
that is simultaneously battle-tested, horizontally scalable with no
sticky-session requirement (its built-in Valkey broker), framework-free (a
plain `net/http` handler, needing no chi router or kit chassis), and backed
by a production-grade JS client
(`centrifuge-js`: reconnect backoff, token refresh, WS/HTTP-stream/SSE
fallback); see the docs site's Realtime page for the full comparison.
Because centrifuge is pre-1.0, nothing outside `notify/broadcast` and
`realtime` imports it directly; `notify.Broadcaster` is the API-stability
firewall a future driver could sit behind instead.
