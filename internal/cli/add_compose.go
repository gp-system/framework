package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func addComposeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compose",
		Short: "(Re)generate compose.yml for the current project",
		Long: `Renders compose.yml from scratch: the base dev stack (postgres, valkey,
mailpit, rustfs, migrate, worker) plus one app service per module recorded in
the manifest. Refuses to overwrite an existing compose.yml unless --force is
given, since hand-added services below the # gpsystem:services anchor would
be lost.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)
			out := cmd.OutOrStdout()

			data := stackData(proj)
			content, err := renderComposeContent(proj, data)
			if err != nil {
				return err
			}

			eng := newEngine(cmd)
			composePath := filepath.Join(proj.Root, "compose.yml")
			if err := eng.Apply([]generator.File{{Path: composePath, Content: content}}); err != nil {
				return err
			}

			envPath := filepath.Join(proj.Root, ".env.example")
			optionalInsert(eng, out, envPath, "gpsystem:env", "env-stack", data)

			if len(proj.Manifest.Modules) == 0 {
				fmt.Fprintln(out, "note: no modules recorded yet; `new module` will add its app service to compose.yml")
			}
			if _, err := os.Stat(filepath.Join(proj.Root, "Dockerfile")); err != nil {
				fmt.Fprintln(out, "note: no Dockerfile found; run `gpsystem add docker`, the compose services build from it")
			}

			fmt.Fprintf(out, `
compose.yml generated. Next steps:

  docker network create proxynet   # once per machine
  cp .env.example .env
  mise run dev
`)
			return nil
		},
	}
}

func addDockerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "docker",
		Short: "(Re)generate Dockerfile and .dockerignore for the current project",
		Long: `Renders the multi-stage Dockerfile (development/build/production targets)
and .dockerignore used by compose.yml. Refuses to overwrite existing files
unless --force is given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			data := stackData(proj)
			files, err := renderProjectFiles(proj.Root, data, [][2]string{
				{"Dockerfile", "project/Dockerfile.tmpl"},
				{".dockerignore", "project/.dockerignore.tmpl"},
			})
			if err != nil {
				return err
			}

			eng := newEngine(cmd)
			return eng.Apply(files)
		},
	}
}

// stackData rebuilds the infra template context (compose.yml/Dockerfile) from
// a project's manifest and go.mod, for commands that regenerate those files
// standalone rather than as part of `new module`.
func stackData(proj *generator.Project) generator.Data {
	replaces := projectReplaces(proj)
	return generator.Data{
		Project:  projName(proj),
		GoModule: proj.Manifest.Module,
		DB:       proj.Manifest.DBName(),
		Kit:      kitRef(replaces[generator.Kit.Path]),
		Errs:     errsRef(replaces[generator.Errs.Path]),
		Mods:     registryModsByPath(replaces),
	}
}

// renderComposeContent renders the base project/compose.yml.tmpl and inserts
// one compose-module snippet per manifest module (sorted), at the
// gpsystem:services anchor.
func renderComposeContent(proj *generator.Project, base generator.Data) ([]byte, error) {
	raw, err := templates.FS.ReadFile("project/compose.yml.tmpl")
	if err != nil {
		return nil, err
	}
	content, err := generator.RenderSnippet(string(raw), base)
	if err != nil {
		return nil, fmt.Errorf("render project/compose.yml.tmpl: %w", err)
	}

	modules := make([]string, 0, len(proj.Manifest.Modules))
	for module := range proj.Manifest.Modules {
		modules = append(modules, module)
	}
	sort.Strings(modules)

	moduleSnippet, err := templates.Snippet("compose-module")
	if err != nil {
		return nil, err
	}
	for _, module := range modules {
		data := base
		data.Module = module
		rendered, err := generator.RenderSnippet(moduleSnippet, data)
		if err != nil {
			return nil, fmt.Errorf("render compose-module for %q: %w", module, err)
		}
		updated, _, err := generator.InsertIntoContent(content, "gpsystem:services", rendered)
		if err != nil {
			return nil, err
		}
		content = updated
	}

	return []byte(content), nil
}

// renderProjectFiles renders the given project-tree templates (relPath,
// tmplPath pairs) into an Apply-ready file plan.
func renderProjectFiles(root string, data generator.Data, pairs [][2]string) ([]generator.File, error) {
	files := make([]generator.File, 0, len(pairs))
	for _, pair := range pairs {
		relPath, tmplPath := pair[0], pair[1]
		raw, err := templates.FS.ReadFile(tmplPath)
		if err != nil {
			return nil, err
		}
		rendered, err := generator.RenderSnippet(string(raw), data)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", tmplPath, err)
		}
		files = append(files, generator.File{Path: filepath.Join(root, relPath), Content: []byte(rendered)})
	}
	return files, nil
}
