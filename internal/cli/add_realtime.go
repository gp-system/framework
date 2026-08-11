package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func addRealtimeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "realtime",
		Short: "Scaffold the realtime notification gateway into an existing project",
		Long: `Adds the cmd/realtime binary, a dedicated, horizontally scalable server
that holds client streaming connections (WebSocket, with an HTTP-streaming/SSE
fallback) and delivers messages published through notify/broadcast from any
process. Instances are stateless: each holds only the channels its own
connections need, publishes reach every instance through the shared Valkey
broker, and there is no sticky-session requirement: scale with
'docker compose up -d --scale realtime=N'.

Run 'add notification' first (or after) to give notifications a broadcast
channel; this command only wires the gateway itself.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			out := cmd.OutOrStdout()

			replaces := projectReplaces(proj)
			data := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				DB:       proj.Manifest.DBName(),
				Kit:      kitRef(replaces[generator.Kit.Path]),
				Errs:     errsRef(replaces[generator.Errs.Path]),
				Mods:     registryModsByPath(replaces),
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "realtime", data, proj.Root)
			if err != nil {
				return err
			}
			if err := eng.Apply(files); err != nil {
				return err
			}

			configGo := filepath.Join(proj.Root, "internal", "platform", "config", "config.go")
			if err := insertSnippet(eng, configGo, "gpsystem:imports", "config-realtime-imports", data); err != nil {
				return err
			}
			if err := insertSnippet(eng, configGo, "gpsystem:config", "config-realtime", data); err != nil {
				return err
			}
			if err := eng.FormatGoFile(configGo); err != nil {
				return err
			}

			// compose.yml / .env may predate the anchors; fall back to a printed
			// block rather than failing (they are not compiled).
			composePath := filepath.Join(proj.Root, "compose.yml")
			if data2, err := os.ReadFile(composePath); err != nil || !strings.Contains(string(data2), "realtime:") {
				optionalInsert(eng, out, composePath, "gpsystem:services", "compose-realtime", data)
			}
			envPath := filepath.Join(proj.Root, ".env.example")
			if data2, err := os.ReadFile(envPath); err != nil || !strings.Contains(string(data2), "REALTIME_LISTEN_ADDR=") {
				optionalInsert(eng, out, envPath, "gpsystem:env", "env-realtime", data)
			}

			fmt.Fprintf(out, `
Realtime gateway scaffolded. Next steps:

  go mod tidy
  mise run realtime        # run the gateway

  # authorize any shared topic channels in cmd/realtime/main.go:
  #   topics.Allow("announcements", func(id *rbac.Identity) bool { return true })
  # add notify.ChannelBroadcast to a notification's Via to deliver it here
  # (existing notifications need this added by hand; 'add notification' run
  # after this command wires it automatically)
  # scale with: docker compose up -d --scale realtime=N
`)
			return nil
		},
	}
}
