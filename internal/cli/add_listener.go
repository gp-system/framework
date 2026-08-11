package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func addListenerCmd() *cobra.Command {
	var queue string

	cmd := &cobra.Command{
		Use:   "listener <module> <event> <name>",
		Short: "Add a listener that reacts to an event",
		Long: `Creates a listener stub in <module> and registers it for <event>. The event
is "name" for an event the module owns, or "producer.name" to subscribe to an
event another module owns. Each listener runs as an independent asynq task
with its own retry, so it must be idempotent.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, eventRef, name := args[0], args[1], args[2]
			if generator.ToPascal(name) == "" {
				return fmt.Errorf("listener name %q is empty after normalization", name)
			}
			eventModule, eventName := parseEventRef(module, eventRef)
			eventName = generator.ToCamel(eventName)

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			if _, exists := proj.Manifest.Modules[module]; !exists {
				return fmt.Errorf("module %q does not exist (create it with `new module %s`)", module, module)
			}
			producer, exists := proj.Manifest.Modules[eventModule]
			if !exists {
				return fmt.Errorf("event module %q does not exist", eventModule)
			}
			hasEvent := false
			for _, e := range producer.Events {
				if e == eventName {
					hasEvent = true
					break
				}
			}
			if !hasEvent {
				return fmt.Errorf("module %q has no event %q (run `add event %s %s` first)", eventModule, eventName, eventModule, eventName)
			}
			if !workerWired(proj.Root) {
				return fmt.Errorf("this project has no worker yet (run `add worker` first)")
			}
			listenersGo, err := moduleWorkerRegistry(proj.Root, module, "listeners")
			if err != nil {
				return err
			}

			data := generator.Data{
				Project:     projName(proj),
				GoModule:    proj.Manifest.Module,
				Module:      module,
				EventModule: eventModule,
				Event:       eventName,
				Listener:    generator.ToCamel(name),
				Queue:       queue,
				DB:          proj.Manifest.DBName(),
				Kit:         kitRef(""),
				Errs:        errsRef(""),
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "listener", data, proj.Root)
			if err != nil {
				return err
			}
			if err := eng.Apply(files); err != nil {
				return err
			}
			if queue != "" {
				if err := insertSnippet(eng, listenersGo, "gpsystem:worker-imports", "listener-queue-import", data); err != nil {
					return err
				}
			}
			if err := insertSnippet(eng, listenersGo, "gpsystem:listeners", "listener-register", data); err != nil {
				return err
			}
			if err := eng.FormatGoFile(listenersGo); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Listener %q added to module %q for event %s. Implement the body in
internal/modules/%s/listeners/%s.go, then run the worker with `+"`mise run worker`"+`.
`, data.Listener, module, data.EventName(), module, generator.ToSnake(data.Listener))
			return nil
		},
	}

	cmd.Flags().StringVar(&queue, "queue", "", "asynq queue this listener runs on (default: the default queue)")
	return cmd
}
