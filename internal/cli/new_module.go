package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func newModuleCmd() *cobra.Command {
	var surface string

	cmd := &cobra.Command{
		Use:   "module <name>",
		Short: "Generate a module (entry point + first surface) into the current project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := generator.ValidateName("module", name); err != nil {
				return err
			}
			if err := generator.ValidateName("surface", surface); err != nil {
				return err
			}

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			if _, exists := proj.Manifest.Modules[name]; exists {
				return fmt.Errorf("module %q already exists (add a surface with `add surface %s <surface>`)", name, name)
			}

			replaces := projectReplaces(proj)
			data := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				Module:   name,
				Surface:  surface,
				DB:       proj.Manifest.DBName(),
				Auth:     proj.Manifest.Auth != "",
				Kit:      kitRef(replaces[generator.Kit.Path]),
				Errs:     errsRef(replaces[generator.Errs.Path]),
				Mods:     registryModsByPath(replaces),
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "module", data, proj.Root)
			if err != nil {
				return err
			}
			if err := eng.Apply(files); err != nil {
				return err
			}

			if err := generateSurface(eng, proj, data); err != nil {
				return err
			}
			if err := generateModuleWorker(eng, proj, data); err != nil {
				return err
			}
			if err := wireCompose(eng, cmd.OutOrStdout(), proj, data); err != nil {
				return err
			}

			if !dryRunFlag(cmd) {
				if err := proj.Manifest.AddSurface(name, surface); err != nil {
					return err
				}
				if err := proj.Manifest.Save(proj.Root); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Module %q (%s surface) generated. Next steps:

  mise run generate        # tsp -> OpenAPI -> strict server code -> build
  go run ./cmd/%s
`, name, surface, name)
			return nil
		},
	}

	cmd.Flags().StringVar(&surface, "surface", "api", `first surface name; "api" mounts at the module root, any other name under /<surface>`)
	return cmd
}

// projName returns the project name recorded in the manifest, falling back
// to the Go module path's last segment for manifests predating the field.
func projName(proj *generator.Project) string {
	if proj.Manifest.Name != "" {
		return proj.Manifest.Name
	}
	mod := proj.Manifest.Module
	for i := len(mod) - 1; i >= 0; i-- {
		if mod[i] == '/' {
			return generator.ToSnake(mod[i+1:])
		}
	}
	return generator.ToSnake(mod)
}
