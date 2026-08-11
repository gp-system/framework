package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func addEventCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "event <module> <name>",
		Short: "Add an event a module owns and can dispatch",
		Long: `Creates an event struct in the module's events/ package and, on the first
event, wires an outbox event.Dispatcher into the module's Dependencies (HTTP
and worker entry points). Dispatch it from a service inside a transaction;
subscribe with 'add listener'.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, name := args[0], args[1]
			if generator.ToPascal(name) == "" {
				return fmt.Errorf("event name %q is empty after normalization", name)
			}
			event := generator.ToCamel(name)

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			entry, exists := proj.Manifest.Modules[module]
			if !exists {
				return fmt.Errorf("module %q does not exist (create it with `new module %s`)", module, module)
			}
			for _, e := range entry.Events {
				if e == event {
					return fmt.Errorf("module %q already has event %q", module, event)
				}
			}

			data := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				Module:   module,
				Event:    event,
				DB:       proj.Manifest.DBName(),
				Kit:      kitRef(""),
				Errs:     errsRef(""),
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "event", data, proj.Root)
			if err != nil {
				return err
			}
			if err := eng.Apply(files); err != nil {
				return err
			}
			if err := wireDispatcher(eng, proj, data); err != nil {
				return err
			}

			if !dryRunFlag(cmd) {
				if err := proj.Manifest.AddEvent(module, event); err != nil {
					return err
				}
				if err := proj.Manifest.Save(proj.Root); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Event %q added to module %q (name: %s). Next steps:

  # add payload fields to internal/modules/%s/events/%s.go
  # dispatch it from a service, inside deps.Transactor.WithinTransaction:
  #   deps.Dispatcher.Dispatch(ctx, %sevents.%s{...})
  # subscribe to it:
  go tool gpsystem add listener <module> %s <listenerName>
`, event, module, data.EventName(), module, generator.ToSnake(event), module, generator.ToPascal(event), event)
			return nil
		},
	}
}
