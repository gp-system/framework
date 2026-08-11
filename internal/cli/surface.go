package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

// DBs lists the database stacks the generators know how to scaffold: the
// pgx-native dbx/pg executor, or the dbx/bunx adapter for query building
// with bun.
var DBs = []string{"pgx", "bun"}

func validateDB(db string) error {
	if !slices.Contains(DBs, db) {
		return fmt.Errorf("unknown db %q (supported: %v)", db, DBs)
	}
	return nil
}

// insertSnippet renders a named snippet with data and inserts it at the
// anchor in path.
func insertSnippet(eng *generator.Engine, path, anchor, snippetName string, data generator.Data) error {
	raw, err := templates.Snippet(snippetName)
	if err != nil {
		return err
	}
	rendered, err := generator.RenderSnippet(raw, data)
	if err != nil {
		return fmt.Errorf("render snippet %s: %w", snippetName, err)
	}
	if _, err := eng.InsertAtAnchor(path, anchor, rendered); err != nil {
		return err
	}
	return nil
}

// parseEventRef splits an event reference into its producer module and event
// name. "news.articlePublished" targets another module's event;
// "articlePublished" defaults to the current module.
func parseEventRef(defaultModule, ref string) (module, event string) {
	if m, e, ok := strings.Cut(ref, "."); ok {
		return m, e
	}
	return defaultModule, ref
}

// workerWired reports whether the project has the shared worker binary (so
// `add event/listener/job` can wire into it). Projects created before the
// worker feature lack it until `add worker` runs.
func workerWired(root string) bool {
	_, err := os.Stat(filepath.Join(root, "cmd", "worker", "main.go"))
	return err == nil
}

// moduleRegisterPath returns the module's wiring file,
// internal/modules/<module>/surfaces/register.go (package surfaces: the
// Dependencies struct and the Register<Surface> functions with their
// gpsystem:* anchors). It errors when the file is missing.
func moduleRegisterPath(root, module string) (string, error) {
	path := filepath.Join(root, "internal", "modules", module, "surfaces", "register.go")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("module %q has no wiring file (expected internal/modules/%s/surfaces/register.go)", module, module)
	}
	return path, nil
}

// wireDispatcher adds the outbox event.Dispatcher to a module's Dependencies
// and constructs it in the HTTP entry point (and the worker entry point when
// present). Idempotent: the second event added to a module skips these.
func wireDispatcher(eng *generator.Engine, proj *generator.Project, data generator.Data) error {
	registerGo, err := moduleRegisterPath(proj.Root, data.Module)
	if err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:imports", "deps-dispatcher-imports", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:dependencies", "deps-dispatcher", data); err != nil {
		return err
	}
	if err := eng.FormatGoFile(registerGo); err != nil {
		return err
	}

	mainGo := filepath.Join(proj.Root, "cmd", data.Module, "main.go")
	if err := wireDispatcherInit(eng, mainGo, "gpsystem:dependencies-init", data); err != nil {
		return err
	}

	if workerWired(proj.Root) {
		workerGo := filepath.Join(proj.Root, "cmd", "worker", "main.go")
		if err := wireDispatcherInit(eng, workerGo, "gpsystem:deps-init("+data.Module+")", data); err != nil {
			return err
		}
	}
	return nil
}

// wireDispatcherInit inserts the outbox dispatcher construction and its imports
// at the given deps-init anchor in an entry point. Imports are inserted one line
// at a time so each stays idempotent after goimports reorders them (an entry
// point already importing outbox (the worker main) simply keeps its import).
func wireDispatcherInit(eng *generator.Engine, path, anchor string, data generator.Data) error {
	if err := insertSnippet(eng, path, "gpsystem:imports", "import-outbox", data); err != nil {
		return err
	}
	if data.DB == "bun" {
		if err := insertSnippet(eng, path, "gpsystem:imports", "import-outbox-bunx", data); err != nil {
			return err
		}
	}
	if err := insertSnippet(eng, path, anchor, "dispatcher-init", data); err != nil {
		return err
	}
	return eng.FormatGoFile(path)
}

// moduleWorkerRegistry returns the module's jobs/register.go or
// listeners/register.go (kind is "jobs" or "listeners"), erroring when the
// subpackage is missing.
func moduleWorkerRegistry(root, module, kind string) (string, error) {
	path := filepath.Join(root, "internal", "modules", module, kind, "register.go")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("module %q has no %s package (expected internal/modules/%s/%s/register.go)", module, kind, module, kind)
	}
	return path, nil
}

// generateModuleWorker renders a module's listeners/register.go and
// jobs/register.go stubs and, when the worker binary exists, wires the module
// into it.
func generateModuleWorker(eng *generator.Engine, proj *generator.Project, data generator.Data) error {
	files, err := eng.RenderTree(templates.FS, "moduleworker", data, proj.Root)
	if err != nil {
		return err
	}
	if err := eng.Apply(files); err != nil {
		return err
	}
	if !workerWired(proj.Root) {
		return nil
	}
	workerGo := filepath.Join(proj.Root, "cmd", "worker", "main.go")
	for _, imp := range []string{"worker-module-imports", "worker-module-jobs-import", "worker-module-listeners-import"} {
		if err := insertSnippet(eng, workerGo, "gpsystem:imports", imp, data); err != nil {
			return err
		}
	}
	if err := insertSnippet(eng, workerGo, "gpsystem:modules", "worker-module", data); err != nil {
		return err
	}
	return eng.FormatGoFile(workerGo)
}

// generateModuleDomain renders the module-root domain files (entities,
// rules, sentinel errors; package named after the module) and the
// repository/ skeleton, unless the module root already holds .go files: the
// first surface stamps them, later surfaces reuse them. `add domain` calls
// this directly to retrofit an existing module. The wiring
// (surfaces/register.go) is not a domain file and lives one level down, so
// it never trips this check.
func generateModuleDomain(eng *generator.Engine, proj *generator.Project, data generator.Data) error {
	moduleDir := filepath.Join(proj.Root, "internal", "modules", data.Module)
	entries, err := os.ReadDir(moduleDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
	}
	files, err := eng.RenderTree(templates.FS, "moduledomain", data, proj.Root)
	if err != nil {
		return err
	}
	return eng.Apply(files)
}

// generateSurface renders one surface (a parallel HTTP bundle) into the
// project and wires it up via anchor insertions: TypeSpec imports in
// main.tsp, a Register<Surface> call in cmd/<module>/main.go, and imports +
// a Register<Surface> function in the module's surfaces/register.go. The
// surface is a minimal skeleton: the contract (.tsp), the handler wired to
// the generated strict interface and calling a minimal service seam (whose
// errors it wraps with errs so they carry a stack), the policy registry stub
// enforcing @permission / @policy tags, and a kept-empty mapper directory
// the consumer fills in. The module-root domain files and the repository/
// skeleton are stamped alongside the module's first surface.
func generateSurface(eng *generator.Engine, proj *generator.Project, data generator.Data) error {
	files, err := eng.RenderTree(templates.FS, "surface", data, proj.Root)
	if err != nil {
		return err
	}
	if err := eng.Apply(files); err != nil {
		return err
	}
	if err := generateModuleDomain(eng, proj, data); err != nil {
		return err
	}

	// Resolve the wiring file up front: if it's missing, fail before any
	// anchor is touched, avoiding a half-wired module.
	registerGo, err := moduleRegisterPath(proj.Root, data.Module)
	if err != nil {
		return err
	}

	mainTsp := filepath.Join(proj.Root, "spec", "typespec", "main.tsp")
	if err := insertSnippet(eng, mainTsp, "gpsystem:modules", "main-tsp-imports", data); err != nil {
		return err
	}

	// The entry point registers each surface explicitly. Insert here first:
	// on a project scaffolded from pre-Register<Surface> templates the anchor
	// is missing, and failing before touching register.go avoids leaving a
	// half-wired module behind.
	mainGo := filepath.Join(proj.Root, "cmd", data.Module, "main.go")
	if err := insertSnippet(eng, mainGo, "gpsystem:registrations", "main-register-call", data); err != nil {
		return err
	}
	if err := eng.FormatGoFile(mainGo); err != nil {
		return err
	}

	registerSnippet := "register-surface"
	if err := insertSnippet(eng, registerGo, "gpsystem:imports", "register-imports", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:surfaces", registerSnippet, data); err != nil {
		return err
	}
	if err := eng.FormatGoFile(registerGo); err != nil {
		return err
	}

	return nil
}
