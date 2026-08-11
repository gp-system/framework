package generator

import (
	"path"
	"path/filepath"
)

// Module identifies a Go module the kit or a generated project can depend
// on: the kit itself, the standalone errs module, or (once later phases
// start extracting packages) any other module pulled out of the kit.
type Module struct {
	Path    string // full module/import path, e.g. "github.com/gp-system/queue"
	Name    string // short key: docker context name, mount dir name, --replace flag key, e.g. "queue"
	Version string // default require version for newly generated projects
}

// Pin returns m with Version overridden, leaving the package-level Module
// value it was copied from untouched. Callers use it to substitute a
// build-info-resolved version (kitVersion(), errsVersion()) for the
// registry default.
func (m Module) Pin(version string) Module {
	m.Version = version
	return m
}

// Kit is the gpsystem module itself. It is special-cased outside Registry
// because generated go.mod files also give it a `tool` directive, and its
// version is normally resolved from runtime/debug.BuildInfo rather than this
// default (see the CLI's kitVersion).
var Kit = Module{Path: "github.com/gp-system/framework", Name: "framework", Version: "v0.0.0"}

// Errs is the first package extracted out of the kit into a standalone
// module. Its version mirrors the kit's own go.mod require line for
// github.com/gp-system/errs; see the Registry doc comment below for the
// general rule.
var Errs = Module{Path: "github.com/gp-system/errs", Name: "errs", Version: "v0.1.0"}

// Registry lists every module extracted from the kit beyond Errs. This
// Registry is also the only place a project's go.mod require (and, for a
// still-unpublished module, replace) directive gets wired up, at `new
// project` time via --replace/--replace-dev; the same Name doubles as the
// docker-compose build-context key and mount dir name, and as the --replace
// flag key. None of the later `add ...` commands touch go.mod.
//
// Most entries are listed unconditionally (their Name always gets a
// require+replace slot) even when nothing in the unconditional project/
// worker template trees imports them yet, e.g. mail, paginate, notify,
// storage and minio-storage-driver: a generated project's first `go mod
// tidy` drops the unused require but keeps any replace directive in place,
// so a later `add mail`/`add notification`/`add auth` or hand-written import
// picks the dependency back up with no go.mod surgery. Others (envconf,
// httperr, queue, telemetry, dbx, events, auth, logx) are unconditional
// because the chassis or the base project/module/worker templates import
// them from the start.
//
// Every entry's Version follows one rule: once a module's repo is public,
// the framework pins it to a specific commit on that module's main branch (a
// tagged release if one exists at that commit, otherwise a main
// pseudo-version) in the framework's own go.mod, and the Registry entry here
// carries the identical value, so a generated project resolves the exact
// same commit the framework itself builds against, with no --replace-dev
// needed. modules_test.go's TestRegistryVersionsMatchKit enforces this
// against the framework's own go.mod for every entry it requires without a
// local replace, so this comment documents intent, not a value callers must
// keep in sync by hand. paginate is the one exception: its repo is not
// published yet, so its version here is an unresolvable placeholder that
// only works via --replace-dev (or an explicit --replace) pointing at a
// local checkout; it becomes a real pin once the repo is published.
var Registry = []Module{
	{Path: "github.com/gp-system/envconf", Name: "envconf", Version: "v0.0.0-20260801093219-491b4df8ac7e"},
	{Path: "github.com/gp-system/httperr", Name: "httperr", Version: "v0.0.0-20260804144916-3b20622210df"},
	{Path: "github.com/gp-system/queue", Name: "queue", Version: "v0.0.0-20260806071718-00a10cf06b4a"},
	{Path: "github.com/gp-system/mail", Name: "mail", Version: "v0.0.0-20260803084058-c2e9f618d2fc"},
	{Path: "github.com/gp-system/telemetry", Name: "telemetry", Version: "v0.0.0-20260805082731-54141fe347c3"},
	{Path: "github.com/gp-system/dbx", Name: "dbx", Version: "v0.0.0-20260804144853-f3a27f628dd7"},
	// Unpublished; only resolvable via --replace/--replace-dev. Refresh once published.
	{Path: "github.com/gp-system/paginate", Name: "paginate", Version: "v0.0.0-00010101000000-000000000000"},
	{Path: "github.com/gp-system/auth", Name: "auth", Version: "v0.0.0-20260730104100-88c4f9d120f8"},
	{Path: "github.com/gp-system/events", Name: "events", Version: "v0.0.0-20260806074211-6f2a8c3ab48c"},
	{Path: "github.com/gp-system/notify", Name: "notify", Version: "v0.0.0-20260806075941-0b4d34564dce"},
	{Path: "github.com/gp-system/storage", Name: "storage", Version: "v0.0.0-20260806150810-7fe5d5d9f194"},
	{Path: "github.com/gp-system/minio-storage-driver", Name: "minio-storage-driver", Version: "v0.0.0-20260806184341-f6f223d0b1ff"},
	{Path: "github.com/gp-system/logx", Name: "logx", Version: "v0.0.0-20260804111936-575c6fdbeedb"},
}

// ModuleRef pairs a Module with an optional local replace path for
// unpublished/in-development use. Replace == "" means "use the published
// Version" (no go.mod replace directive, no local bind mount).
type ModuleRef struct {
	Module
	Replace string // optional go.mod replace target, relative to the project root
}

// Mount is the in-container path a locally-replaced module is
// bind-mounted/copied to during local development (compose.yml volume
// target, Dockerfile COPY destination). Resolving Replace against
// devSrcDir mirrors, inside the container, the same relative hop the go.mod
// replace directive takes on the host from the project root, so a relative
// Replace resolves identically in both places. Returns "" when Replace is
// unset.
func (m ModuleRef) Mount() string {
	if m.Replace == "" {
		return ""
	}
	return path.Clean(path.Join(devSrcDir, filepath.ToSlash(m.Replace)))
}
