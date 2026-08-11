package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func addJobCmd() *cobra.Command {
	var cron string

	cmd := &cobra.Command{
		Use:   "job <module> <name>",
		Short: "Add a scheduled job (a recurring handler, not an event)",
		Long: `Creates a job stub in <module> and schedules it on --cron. The job runs on
whichever worker picks it up; the schedule fires replica-safely (only one
worker per tick). Cron accepts 5-field specs ("0 3 * * *") and asynq
descriptors ("@every 30m", "@daily").`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, name := args[0], args[1]
			if generator.ToPascal(name) == "" {
				return fmt.Errorf("job name %q is empty after normalization", name)
			}
			cron = strings.TrimSpace(cron)
			if cron == "" {
				return fmt.Errorf("--cron is required (e.g. \"0 3 * * *\" or \"@every 30m\")")
			}
			if strings.ContainsAny(cron, `"\`) {
				return fmt.Errorf(`--cron must not contain quotes or backslashes (got %q)`, cron)
			}

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			if _, exists := proj.Manifest.Modules[module]; !exists {
				return fmt.Errorf("module %q does not exist (create it with `new module %s`)", module, module)
			}
			if !workerWired(proj.Root) {
				return fmt.Errorf("this project has no worker yet (run `add worker` first)")
			}
			jobsGo, err := moduleWorkerRegistry(proj.Root, module, "jobs")
			if err != nil {
				return err
			}

			data := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				Module:   module,
				Job:      generator.ToCamel(name),
				Cron:     cron,
				DB:       proj.Manifest.DBName(),
				Kit:      kitRef(""),
				Errs:     errsRef(""),
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "job", data, proj.Root)
			if err != nil {
				return err
			}
			if err := eng.Apply(files); err != nil {
				return err
			}
			if err := insertSnippet(eng, jobsGo, "gpsystem:jobs", "job-register", data); err != nil {
				return err
			}
			if err := eng.FormatGoFile(jobsGo); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Job %q added to module %q (%q). Implement the body in
internal/modules/%s/jobs/%s.go.
`, data.Job, module, cron, module, generator.ToSnake(data.Job))
			return nil
		},
	}

	cmd.Flags().StringVar(&cron, "cron", "", `cron spec or asynq descriptor, e.g. "0 3 * * *" or "@every 30m"`)
	return cmd
}
