package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
)

func addDBCmd() *cobra.Command {
	var connector string
	cmd := &cobra.Command{
		Use:   "db <module> <name>",
		Short: "Add a named database connection to a module",
		Long: `Adds a named database connection alongside the project's default: its own
config field (DB_<NAME>_*), a pgx pool (optionally wrapped with dbx/bunx), a
closer, and Dependencies fields on the module.

By default a module's repository stays under a flat repository/ directory
bound to the project's default connection. Bind a module's repository to this connection
with:

  gpsystem add surface <module> <surface> --db <name>
`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, name := args[0], args[1]
			if err := generator.ValidateName("connection", name); err != nil {
				return err
			}
			if name == "default" {
				return fmt.Errorf(`connection name "default" is reserved for the project's unnamed default connection`)
			}

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			if _, exists := proj.Manifest.Modules[module]; !exists {
				return fmt.Errorf("module %q does not exist (create it with `new module %s`)", module, module)
			}
			if _, exists := proj.Manifest.FindDB(module, name); exists {
				return fmt.Errorf("module %q already has a %q database connection", module, name)
			}

			if connector == "" {
				connector = proj.Manifest.DBName()
			}
			if err := validateDB(connector); err != nil {
				return err
			}

			mainGo := filepath.Join(proj.Root, "cmd", module, "main.go")
			if err := preflightDBAnchors(mainGo); err != nil {
				return err
			}

			data := generator.Data{
				Project:     projName(proj),
				GoModule:    proj.Manifest.Module,
				Module:      module,
				DB:          proj.Manifest.DBName(),
				DBConn:      name,
				DBConnector: connector,
				Kit:         kitRef(""),
				Errs:        errsRef(""),
			}

			eng := newEngine(cmd)
			out := cmd.OutOrStdout()

			configGo := filepath.Join(proj.Root, "internal", "platform", "config", "config.go")
			if err := insertSnippet(eng, configGo, "gpsystem:config", "db-config", data); err != nil {
				return err
			}
			if err := eng.FormatGoFile(configGo); err != nil {
				return err
			}

			optionalInsert(eng, out, filepath.Join(proj.Root, ".env.example"), "gpsystem:env", "db-env", data)

			if err := wireDBIntoMain(eng, mainGo, data); err != nil {
				return err
			}

			registerGo, err := moduleRegisterPath(proj.Root, module)
			if err != nil {
				return err
			}
			if err := insertSnippet(eng, registerGo, "gpsystem:imports", "db-deps-imports", data); err != nil {
				return err
			}
			if err := insertSnippet(eng, registerGo, "gpsystem:dependencies", "db-deps", data); err != nil {
				return err
			}
			if err := eng.FormatGoFile(registerGo); err != nil {
				return err
			}

			if workerWired(proj.Root) {
				workerGo := filepath.Join(proj.Root, "cmd", "worker", "main.go")
				if err := wireDBIntoWorker(eng, workerGo, module, data); err != nil {
					return err
				}
			}

			if !dryRunFlag(cmd) {
				if err := proj.Manifest.AddDB(module, name, connector); err != nil {
					return err
				}
				if err := proj.Manifest.Save(proj.Root); err != nil {
					return err
				}
			}

			upper := strings.ToUpper(name)
			fmt.Fprintf(out, `
Connection %q added to module %q (%s). Next steps:

  Set DB_%s_USER, DB_%s_PASSWORD and DB_%s_NAME (required; HOST/PORT/SSLMODE
  default to localhost/5432/disable).
  Bind a repository to it: gpsystem add surface %s <surface> --db %s
  Note: cmd/migrate only targets the project's default connection.
`, name, module, connector, upper, upper, upper, module, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&connector, "connector", "", `connection stack: "pgx" or "bun" (default: the project's db choice)`)
	return cmd
}
