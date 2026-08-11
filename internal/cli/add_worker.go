package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func addWorkerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Scaffold the background worker into an existing project",
		Long: `Adds the shared cmd/worker binary, per-module listeners/ and jobs/
subpackages, the outbox migration and the Valkey/worker config. Use it on
projects created before the worker feature; new projects already include it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			out := cmd.OutOrStdout()

			base := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				DB:       proj.Manifest.DBName(),
				Kit:      kitRef(""),
				Errs:     errsRef(""),
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "worker", base, proj.Root)
			if err != nil {
				return err
			}
			if err := eng.Apply(files); err != nil {
				return err
			}

			// Wire every existing module into the worker, in a stable order.
			modules := make([]string, 0, len(proj.Manifest.Modules))
			for module := range proj.Manifest.Modules {
				modules = append(modules, module)
			}
			sort.Strings(modules)
			for _, module := range modules {
				data := base
				data.Module = module
				if err := generateModuleWorker(eng, proj, data); err != nil {
					return err
				}
				if len(proj.Manifest.Modules[module].Events) > 0 {
					if err := wireDispatcher(eng, proj, data); err != nil {
						return err
					}
				}
				if err := wireModuleDBs(eng, proj, module, base); err != nil {
					return err
				}
			}

			// Config is always present with the anchors.
			configGo := filepath.Join(proj.Root, "internal", "platform", "config", "config.go")
			if err := insertSnippet(eng, configGo, "gpsystem:imports", "config-worker-imports", base); err != nil {
				return err
			}
			if err := insertSnippet(eng, configGo, "gpsystem:config", "config-worker", base); err != nil {
				return err
			}
			if err := eng.FormatGoFile(configGo); err != nil {
				return err
			}

			// compose.yml / .env may predate the anchors; fall back to a printed
			// block rather than failing (they are not compiled). Projects
			// scaffolded from the base compose-stack template already have a
			// valkey service and VALKEY_ADDR, in a different shape than these
			// snippets, inserting again would duplicate them, since the
			// idempotency check does not recognize the newer form.
			composePath := filepath.Join(proj.Root, "compose.yml")
			if data, err := os.ReadFile(composePath); err != nil || !strings.Contains(string(data), "valkey:") {
				optionalInsert(eng, out, composePath, "gpsystem:services", "compose-valkey", base)
			}
			envPath := filepath.Join(proj.Root, ".env.example")
			if data, err := os.ReadFile(envPath); err != nil || !strings.Contains(string(data), "VALKEY_ADDR=") {
				optionalInsert(eng, out, envPath, "gpsystem:env", "env-worker", base)
			}

			if err := writeOutboxMigration(eng, proj); err != nil {
				return err
			}

			fmt.Fprintf(out, `
Worker scaffolded. Next steps:

  go mod tidy
  go run ./cmd/migrate up   # apply the outbox migration
  mise run worker           # run the worker
`)
			return nil
		},
	}
}

// optionalInsert inserts a snippet at an anchor, but when the anchor is missing
// (an older non-Go file) it prints the rendered block for the user to paste,
// instead of failing.
func optionalInsert(eng *generator.Engine, out io.Writer, path, anchor, snippetName string, data generator.Data) {
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(out, "note: %s not found; skipping %s wiring\n", path, snippetName)
		return
	}
	if !strings.Contains(string(content), anchor) {
		raw, _ := templates.Snippet(snippetName)
		rendered, _ := generator.RenderSnippet(raw, data)
		fmt.Fprint(out, generator.AnchorFixHint(path, anchor, rendered))
		return
	}
	if err := insertSnippet(eng, path, anchor, snippetName, data); err != nil {
		fmt.Fprintf(out, "note: could not wire %s into %s: %v\n", snippetName, filepath.Base(path), err)
	}
}

// writeOutboxMigration writes the outbox goose migration at the next version,
// unless a migration for outbox_events already exists.
func writeOutboxMigration(eng *generator.Engine, proj *generator.Project) error {
	migrationsDir := filepath.Join(proj.Root, "migrations")
	if entries, err := os.ReadDir(migrationsDir); err == nil {
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
			if err == nil && strings.Contains(string(data), "outbox_events") {
				return nil // already present
			}
		}
	}
	raw, err := templates.FS.ReadFile("project/migrations/20200101000100_outbox.sql.tmpl")
	if err != nil {
		return err
	}
	prefix, err := nextMigrationPrefix(migrationsDir)
	if err != nil {
		return err
	}
	path := filepath.Join(migrationsDir, fmt.Sprintf("%s_outbox.sql", prefix))
	return eng.Apply([]generator.File{{Path: path, Content: raw}})
}
