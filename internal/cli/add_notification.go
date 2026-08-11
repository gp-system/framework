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

func addNotificationCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "notification <module> <name>",
		Short: "Add a Laravel-style, multi-channel notification a module owns",
		Long: `Creates a notification (payload + Via + per-channel content, in
internal/modules/<module>/notifications/) and its html/text mail templates.
Via defaults to the database and mail channels; add notify.ChannelBroadcast
once 'gpsystem add realtime' has wired the project's realtime gateway.

On the first notification in the project, wires a kit notify.Sender (a Hub
over the mail and database channels, plus the broadcast channel when the
realtime gateway is present) into every entry point, and adds the
notifications table migration.

Use this for content addressed to a recipient (a user, possibly on several
channels); for mail sent straight to an address with no recipient concept
(e.g. a contact-form confirmation), use 'add mail' instead: the two commands
are independent and compose: a notification's ToMail reuses the same
kitmail.Message/kitmail.Template builders an 'add mail' notifier does.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, name := args[0], args[1]
			if generator.ToPascal(name) == "" {
				return fmt.Errorf("notification name %q is empty after normalization", name)
			}
			notification := generator.ToCamel(name)

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			entry, exists := proj.Manifest.Modules[module]
			if !exists {
				return fmt.Errorf("module %q does not exist (create it with `new module %s`)", module, module)
			}
			for _, n := range entry.Notifications {
				if n == notification {
					return fmt.Errorf("module %q already has notification %q", module, notification)
				}
			}

			data := generator.Data{
				Project:      projName(proj),
				GoModule:     proj.Manifest.Module,
				Module:       module,
				Notification: notification,
				DB:           proj.Manifest.DBName(),
				Realtime:     realtimeWired(proj.Root),
				Kit:          kitRef(""),
				Errs:         errsRef(""),
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "notification/go", data, proj.Root)
			if err != nil {
				return err
			}
			assets, err := notificationAssetFiles(data, proj.Root)
			if err != nil {
				return err
			}
			files = append(files, assets...)
			if err := eng.Apply(files); err != nil {
				return err
			}

			if err := wireNotifier(eng, proj, data); err != nil {
				return err
			}
			if err := writeNotificationsMigration(eng, proj); err != nil {
				return err
			}

			if !dryRunFlag(cmd) {
				if err := proj.Manifest.AddNotification(module, notification); err != nil {
					return err
				}
				if err := proj.Manifest.Save(proj.Root); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Notification %q added to module %q. Next steps:

  go mod tidy
  go run ./cmd/migrate up   # apply the notifications table migration

  # add payload fields and write the subject/templates in:
  #   internal/modules/%s/notifications/%s.go
  #   internal/modules/%s/notifications/templates/%s.html.tmpl
  #   internal/modules/%s/notifications/templates/%s.txt.tmpl
  # send it from a service or listener:
  #   deps.Notifier.Send(ctx, notify.Recipient{ID: subject, Email: email}, %snotifications.%s{...})
`, notification, module,
				module, generator.ToSnake(notification),
				module, generator.ToSnake(notification),
				module, generator.ToSnake(notification),
				module, generator.ToPascal(notification))
			return nil
		},
	}
}

// realtimeWired reports whether `add realtime` has scaffolded the realtime
// gateway into this project, mirroring workerWired's filesystem check.
func realtimeWired(root string) bool {
	_, err := os.Stat(filepath.Join(root, "cmd", "realtime", "main.go"))
	return err == nil
}

// notificationAssetFiles builds the html/text template files for a
// notification. Copied verbatim from the mail generator's placeholder
// assets (never rendered by the gpsystem engine): they are html/template and
// text/template sources consumed at runtime by mail.Template, and would fail
// to render against generator.Data since their {{...}} actions reference the
// notification's own payload fields instead, see mailAssetFiles.
func notificationAssetFiles(data generator.Data, root string) ([]generator.File, error) {
	base := generator.ToSnake(data.Notification)
	dir := filepath.Join(root, "internal", "modules", data.Module, "notifications", "templates")

	var files []generator.File
	for _, kind := range []string{"html", "txt"} {
		raw, err := templates.FS.ReadFile(fmt.Sprintf("mail/assets/notification.%s.tmpl", kind))
		if err != nil {
			return nil, err
		}
		files = append(files, generator.File{
			Path:    filepath.Join(dir, fmt.Sprintf("%s.%s.tmpl", base, kind)),
			Content: raw,
		})
	}
	return files, nil
}

// wireNotifier adds a kit notify.Sender to a module's Dependencies and
// constructs it (mail + database channels, plus broadcast when the realtime
// gateway is present) in the HTTP entry point and the worker entry point
// when present. Idempotent: a second notification added to any module skips
// these.
func wireNotifier(eng *generator.Engine, proj *generator.Project, data generator.Data) error {
	configGo := filepath.Join(proj.Root, "internal", "platform", "config", "config.go")
	if err := insertSnippet(eng, configGo, "gpsystem:imports", "import-smtp", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, configGo, "gpsystem:config", "mail-config", data); err != nil {
		return err
	}
	if err := eng.FormatGoFile(configGo); err != nil {
		return err
	}

	registerGo, err := moduleRegisterPath(proj.Root, data.Module)
	if err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:imports", "import-notify", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:dependencies", "deps-notifier", data); err != nil {
		return err
	}
	if err := eng.FormatGoFile(registerGo); err != nil {
		return err
	}

	mainGo := filepath.Join(proj.Root, "cmd", data.Module, "main.go")
	if err := wireNotifierInit(eng, mainGo, "gpsystem:dependencies-init", "notifier-publisher-closer", data); err != nil {
		return err
	}

	if workerWired(proj.Root) {
		workerGo := filepath.Join(proj.Root, "cmd", "worker", "main.go")
		if err := wireNotifierInit(eng, workerGo, "gpsystem:deps-init("+data.Module+")", "notifier-publisher-closer-worker", data); err != nil {
			return err
		}
	}
	return nil
}

// wireNotifierInit inserts the notifier construction and its imports at the
// given deps-init anchor in an entry point, plus the shared "notifier :="
// line at the entry point's gpsystem:pools anchor and, when the realtime
// gateway is wired, the publisher's closer.
//
// The database channel always persists through the kit's pgx executor
// (pg.NewDB(pool)), regardless of the project's DB choice for its business
// modules: pool (a *pgxpool.Pool) is in scope at gpsystem:pools in every
// entry point, whereas a bun *bun.DB is only constructed later, inside the
// per-module Dependencies literal: notify/database/bunx remains available
// for a project that wants to wire it there by hand.
func wireNotifierInit(eng *generator.Engine, path, anchor, closerSnippet string, data generator.Data) error {
	if err := insertSnippet(eng, path, "gpsystem:imports", "import-smtp", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, path, "gpsystem:pools", "mailer-pool-init", data); err != nil {
		return err
	}

	// The notifier is constructed once per entry point, on the project's
	// first notification (mirroring mailer-pool-init/dispatcher-init). A
	// later notification's data may render different content here (e.g. it
	// arrives after `add realtime`, adding the broadcast channel), that
	// would not dedupe against an earlier module's block and would emit a
	// second "notifier :=", which does not compile. Skip everything only
	// notifier-pool-init needs once any notifier construction is present;
	// upgrading an existing one (e.g. to add broadcast) is the manual step
	// the `add realtime` next-steps note already calls out.
	wired, err := containsNotifierInit(path)
	if err != nil {
		return err
	}
	if !wired {
		if err := insertSnippet(eng, path, "gpsystem:imports", "import-notify", data); err != nil {
			return err
		}
		if err := insertSnippet(eng, path, "gpsystem:imports", "import-notify-database", data); err != nil {
			return err
		}
		if data.Realtime {
			if err := insertSnippet(eng, path, "gpsystem:imports", "import-notify-broadcast", data); err != nil {
				return err
			}
		}
		if err := insertSnippet(eng, path, "gpsystem:pools", "notifier-pool-init", data); err != nil {
			return err
		}
		if data.Realtime {
			if err := insertSnippet(eng, path, "gpsystem:closers", closerSnippet, data); err != nil {
				return err
			}
		}
	}
	if err := insertSnippet(eng, path, anchor, "notifier-deps-init", data); err != nil {
		return err
	}
	return eng.FormatGoFile(path)
}

// containsNotifierInit reports whether path already declares the shared
// notifier variable.
func containsNotifierInit(path string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return strings.Contains(string(content), "notifier := notify.NewHub("), nil
}

// writeNotificationsMigration writes the notify database channel's goose
// migration at the next timestamp prefix, unless a migration for the
// notifications table already exists, mirroring writeOutboxMigration in
// add_worker.go.
func writeNotificationsMigration(eng *generator.Engine, proj *generator.Project) error {
	migrationsDir := filepath.Join(proj.Root, "migrations")
	if entries, err := os.ReadDir(migrationsDir); err == nil {
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
			if err == nil && strings.Contains(string(data), "CREATE TABLE notifications") {
				return nil // already present
			}
		}
	}
	raw, err := templates.FS.ReadFile("notification/migration/notifications.sql.tmpl")
	if err != nil {
		return err
	}
	prefix, err := nextMigrationPrefix(migrationsDir)
	if err != nil {
		return err
	}
	path := filepath.Join(migrationsDir, fmt.Sprintf("%s_notifications.sql", prefix))
	return eng.Apply([]generator.File{{Path: path, Content: raw}})
}
