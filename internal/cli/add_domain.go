package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func addDomainCmd() *cobra.Command {
	var dbConn string
	cmd := &cobra.Command{
		Use:   "domain <module>",
		Short: "Retrofit a module with the module-root domain files and repository/ skeleton",
		Long: `Renders the module-root domain files (entities, rules, sentinel errors
shared by every surface; the package is named after the module) and the
repository/ skeleton into an existing module. New modules get both alongside
their first surface; this command retrofits modules whose domain files were
deleted.

No anchors are touched: the command is purely additive. An existing domain
file surfaces as a normal conflict.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			module := args[0]

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			entry, exists := proj.Manifest.Modules[module]
			if !exists {
				return fmt.Errorf("module %q does not exist (create it with `new module %s`)", module, module)
			}

			data := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				Module:   module,
				DB:       proj.Manifest.DBName(),
				Kit:      kitRef(""),
				Errs:     errsRef(""),
			}

			if dbConn != "" {
				conn, ok := proj.Manifest.FindDB(module, dbConn)
				if !ok {
					return fmt.Errorf("module %q has no %q database connection (available: %s; add one with `add db %s %s`)",
						module, dbConn, availableDBs(entry), module, dbConn)
				}
				data.DBConn = conn.Name
				data.DBConnector = conn.Connector
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "moduledomain", data, proj.Root)
			if err != nil {
				return err
			}
			if err := eng.Apply(files); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nDomain skeleton added to module %q: internal/modules/%s/{%s.go,errors.go,repository}.\n", module, module, module)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbConn, "db", "", "render the repository skeleton for a named database connection added via `add db` (default: the project's default connection, flat repository/)")
	return cmd
}
