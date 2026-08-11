package cli

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func newProjectCmd() *cobra.Command {
	var (
		modulePath   string
		dir          string
		db           string
		replaceFlags []string
		replaceDev   string
	)

	cmd := &cobra.Command{
		Use:   "project <name>",
		Short: "Generate a new consumer project skeleton",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := generator.ValidateName("project", name); err != nil {
				return err
			}
			if err := validateDB(db); err != nil {
				return err
			}
			if dir == "" {
				dir = name
			}

			replaces, err := resolveReplaces(replaceFlags, replaceDev, dir)
			if err != nil {
				return err
			}

			data := generator.Data{
				Project:  name,
				GoModule: modulePath,
				DB:       db,
				Kit:      kitRef(replaces[generator.Kit.Name]),
				Errs:     errsRef(replaces[generator.Errs.Name]),
			}
			for _, m := range generator.Registry {
				data.Mods = append(data.Mods, generator.ModuleRef{Module: m, Replace: replaces[m.Name]})
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "project", data, dir)
			if err != nil {
				return err
			}

			workerFiles, err := eng.RenderTree(templates.FS, "worker", data, dir)
			if err != nil {
				return err
			}
			files = append(files, workerFiles...)

			oapiTree := "oapi"
			oapiTemplates, err := eng.CopyTree(templates.FS, oapiTree, filepath.Join(dir, "api", "oapi-codegen", "templates"))
			if err != nil {
				return err
			}
			files = append(files, oapiTemplates...)

			if err := eng.Apply(files); err != nil {
				return err
			}

			if !dryRunFlag(cmd) {
				manifest := generator.NewManifest(name, modulePath, KitModule+"@"+kitVersion())
				manifest.DB = db
				if err := manifest.Save(dir); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Project %q is ready. Next steps:

  cd %s
  go mod tidy
  mise run setup                      # TypeSpec toolchain (npm)
  go tool gpsystem new module <name>  # first module
  mise run generate                   # tsp -> OpenAPI -> Go
  cp .env.example .env                # set USER_ID/GROUP_ID to id -u/id -g
  docker network create proxynet      # one-time, shared across projects
  mise run dev                        # build + start the full dev stack
`, name, dir)
			return nil
		},
	}

	cmd.Flags().StringVar(&modulePath, "module-path", "", "Go module path of the new project (required)")
	cmd.Flags().StringVar(&dir, "dir", "", "target directory (default: ./<name>)")
	cmd.Flags().StringArrayVar(&replaceFlags, "replace", nil, `add a go.mod replace for one module, as name=path (repeatable); valid names are "framework", "errs" plus any module in the kit's registry`)
	cmd.Flags().StringVar(&replaceDev, "replace-dev", "", `convenience for a workspace checkout: assumes sibling directories DIR/<name> for the framework, errs and every registry module, equivalent to passing --replace for each of them individually`)
	cmd.Flags().StringVar(&db, "db", "pgx", "database stack of the generated backend (pgx|bun)")
	_ = cmd.MarkFlagRequired("module-path")
	return cmd
}

// resolveReplaces merges --replace and --replace-dev into a single
// module-name -> path map of go.mod replace targets, each resolved relative
// to dir (the new project's root) so the emitted go.mod/compose.yml/
// Dockerfile carry no machine-specific absolute path.
//
// Precedence when more than one flag targets the same module: an explicit
// --replace always wins; --replace-dev supplies a sibling-directory default
// for every module in Kit ∪ Errs ∪ Registry, overridden by --replace.
func resolveReplaces(replaceFlags []string, replaceDev, dir string) (map[string]string, error) {
	// names drives --replace-dev's sibling-directory loop below: every
	// module, the kit and errs included, lives in its own sibling checkout
	// named after its Module.Name.
	names := make([]string, 0, 2+len(generator.Registry))
	valid := map[string]bool{}
	for _, n := range append([]string{generator.Kit.Name, generator.Errs.Name}, registryNames()...) {
		names = append(names, n)
		valid[n] = true
	}

	explicit := map[string]string{}
	for _, kv := range replaceFlags {
		name, target, ok := strings.Cut(kv, "=")
		if !ok || name == "" || target == "" {
			return nil, fmt.Errorf("--replace must be name=path, got %q", kv)
		}
		if !valid[name] {
			return nil, fmt.Errorf("--replace %q: %q is not a known module (valid: %s)", kv, name, strings.Join(names, ", "))
		}
		explicit[name] = target
	}

	replaces := map[string]string{}
	if replaceDev != "" {
		for _, n := range names {
			replaces[n] = path.Join(replaceDev, n)
		}
	}
	for name, target := range explicit {
		replaces[name] = target
	}

	for name, target := range replaces {
		rel, err := relativeReplace(target, dir)
		if err != nil {
			return nil, err
		}
		replaces[name] = rel
	}
	return replaces, nil
}

func registryNames() []string {
	names := make([]string, len(generator.Registry))
	for i, m := range generator.Registry {
		names[i] = m.Name
	}
	return names
}

// relativeReplace resolves target (any path form accepted by --replace/
// --replace-dev) to a path relative to dir (the new project's root). Both
// inputs are made absolute first so the relative result is stable regardless
// of the caller's current directory.
func relativeReplace(target, dir string) (string, error) {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absDir, absTarget)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
