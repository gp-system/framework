package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
)

func addSurfaceCmd() *cobra.Command {
	var dbConn string
	cmd := &cobra.Command{
		Use:   "surface <module> <surface>",
		Short: "Add another surface (parallel HTTP bundle) to an existing module",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, surface := args[0], args[1]
			if err := generator.ValidateName("surface", surface); err != nil {
				return err
			}

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			entry, exists := proj.Manifest.Modules[module]
			if !exists {
				return fmt.Errorf("module %q does not exist (create it with `new module %s`)", module, module)
			}
			if slices.Contains(entry.Surfaces, surface) {
				return fmt.Errorf("module %q already has the %s surface", module, surface)
			}

			data := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				Module:   module,
				Surface:  surface,
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
			if err := generateSurface(eng, proj, data); err != nil {
				return err
			}

			if !dryRunFlag(cmd) {
				if err := proj.Manifest.AddSurface(module, surface); err != nil {
					return err
				}
				if dbConn != "" {
					proj.Manifest.SetSurfaceDB(module, surface, dbConn)
				}
				if err := proj.Manifest.Save(proj.Root); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nSurface %s added to module %q. Run `mise run generate`.\n", surface, module)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbConn, "db", "", "bind the module's shared repository skeleton to a named database connection added via `add db` (default: the project's default connection, flat repository/)")
	return cmd
}

// availableDBs lists a module's named connections for an error message.
func availableDBs(entry *generator.ModuleEntry) string {
	if entry == nil || len(entry.DBs) == 0 {
		return "none"
	}
	names := make([]string, 0, len(entry.DBs))
	for _, c := range entry.DBs {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
