package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gp-system/framework/internal/generator"
)

// preflightDBAnchors checks that an entry point has the two anchors a named
// connection wires into. Projects generated before this feature lack them;
// failing here, before any file is touched, avoids leaving a half-wired
// connection behind on templates whose closer call cannot be safely
// auto-migrated.
func preflightDBAnchors(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("preflight %s: %w", path, err)
	}
	content := string(data)
	var missing []string
	for _, anchor := range []string{"gpsystem:pools", "gpsystem:closers"} {
		if !strings.Contains(content, anchor) {
			missing = append(missing, anchor)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(`%s predates named database connections and is missing: %s

Add the missing comment(s) by hand (see a freshly generated project for
placement) and rerun this command`, path, strings.Join(missing, ", "))
}

// wireDBIntoMain inserts a named connection's pool/bun handle, its
// Dependencies field initializers and its closers into an HTTP entry point
// (cmd/<module>/main.go).
func wireDBIntoMain(eng *generator.Engine, mainGo string, data generator.Data) error {
	if data.DBConnector == "bun" {
		if err := insertSnippet(eng, mainGo, "gpsystem:imports", "db-main-imports", data); err != nil {
			return err
		}
	}
	if err := insertSnippet(eng, mainGo, "gpsystem:pools", "db-pool", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, mainGo, "gpsystem:dependencies-init", "db-deps-init", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, mainGo, "gpsystem:closers", "db-closer", data); err != nil {
		return err
	}
	return eng.FormatGoFile(mainGo)
}

// wireDBIntoWorker inserts a named connection's pool/bun handle, the
// module-scoped deps-init and the closers into cmd/worker/main.go. Called
// both by `add db` (for a worker that already exists) and by `add worker`
// (retrofitting connections added before the worker binary existed).
func wireDBIntoWorker(eng *generator.Engine, workerGo, module string, data generator.Data) error {
	if data.DBConnector == "bun" {
		if err := insertSnippet(eng, workerGo, "gpsystem:imports", "db-main-imports", data); err != nil {
			return err
		}
	}
	if err := insertSnippet(eng, workerGo, "gpsystem:pools", "db-pool", data); err != nil {
		return err
	}
	depsInitAnchor := "gpsystem:deps-init(" + module + ")"
	if err := insertSnippet(eng, workerGo, depsInitAnchor, "db-deps-init", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, workerGo, "gpsystem:closers", "db-closer-worker", data); err != nil {
		return err
	}
	return eng.FormatGoFile(workerGo)
}

// wireModuleDBs runs wireDBIntoWorker for every named connection a module
// already has, per the manifest. Used by `add worker` to retrofit
// connections that were added before the worker binary existed.
func wireModuleDBs(eng *generator.Engine, proj *generator.Project, module string, base generator.Data) error {
	entry := proj.Manifest.Modules[module]
	if entry == nil || len(entry.DBs) == 0 {
		return nil
	}
	workerGo := filepath.Join(proj.Root, "cmd", "worker", "main.go")
	for _, conn := range entry.DBs {
		data := base
		data.Module = module
		data.DBConn = conn.Name
		data.DBConnector = conn.Connector
		if err := wireDBIntoWorker(eng, workerGo, module, data); err != nil {
			return err
		}
	}
	return nil
}
