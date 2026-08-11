package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

func addMailCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mail <module> <name>",
		Short: "Add a mail notifier a module owns and can send",
		Long: `Creates a notifier (payload + Notifier, in internal/modules/<module>/mail/)
and its html/text templates. On the first mail notifier in the project, wires
a kit mail.Mailer into every entry point (SMTP when MAIL_HOST is set, a no-op
otherwise) and adds it to the module's Dependencies.

Mail content (payload, templates, subject) is module business content and
lives with the module; the SMTP transport is centralized in platform config
and the kit. Call the notifier from a service or a listener, whichever fits:
some mail is sent synchronously (e.g. a password reset), some asynchronously
via an event (see 'add event' / 'add listener').`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, name := args[0], args[1]
			if generator.ToPascal(name) == "" {
				return fmt.Errorf("mail notifier name %q is empty after normalization", name)
			}
			mailName := generator.ToCamel(name)

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			entry, exists := proj.Manifest.Modules[module]
			if !exists {
				return fmt.Errorf("module %q does not exist (create it with `new module %s`)", module, module)
			}
			for _, m := range entry.Mail {
				if m == mailName {
					return fmt.Errorf("module %q already has mail notifier %q", module, mailName)
				}
			}

			data := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				Module:   module,
				Mail:     mailName,
				DB:       proj.Manifest.DBName(),
				Kit:      kitRef(""),
				Errs:     errsRef(""),
			}

			eng := newEngine(cmd)
			files, err := eng.RenderTree(templates.FS, "mail/go", data, proj.Root)
			if err != nil {
				return err
			}
			assets, err := mailAssetFiles(data, proj.Root)
			if err != nil {
				return err
			}
			files = append(files, assets...)
			if err := eng.Apply(files); err != nil {
				return err
			}

			if err := wireMailer(eng, proj, data); err != nil {
				return err
			}

			if !dryRunFlag(cmd) {
				if err := proj.Manifest.AddMail(module, mailName); err != nil {
					return err
				}
				if err := proj.Manifest.Save(proj.Root); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Mail notifier %q added to module %q. Next steps:

  # add payload fields and write the subject/templates in:
  #   internal/modules/%s/mail/%s.go
  #   internal/modules/%s/mail/templates/%s.html.tmpl
  #   internal/modules/%s/mail/templates/%s.txt.tmpl
  # send it from a service or listener:
  #   %smail.New%sNotifier(deps.Mailer).Notify(ctx, to, %smail.%s{...})
  # set MAIL_HOST (and MAIL_FROM_ADDRESS) to send over SMTP; leave MAIL_HOST
  # empty to keep the no-op mailer in dev.
`, mailName, module,
				module, generator.ToSnake(mailName),
				module, generator.ToSnake(mailName),
				module, generator.ToSnake(mailName),
				module, generator.ToPascal(mailName), module, generator.ToPascal(mailName))
			return nil
		},
	}
}

// mailAssetFiles builds the html/text template files for a mail notifier.
// They are copied verbatim (never rendered by the gpsystem engine): they are
// html/template and text/template sources consumed at runtime by
// mail.Template, and would fail to render against generator.Data since their
// {{...}} actions reference the notifier's own payload fields instead.
func mailAssetFiles(data generator.Data, root string) ([]generator.File, error) {
	base := generator.ToSnake(data.Mail)
	dir := filepath.Join(root, "internal", "modules", data.Module, "mail", "templates")

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

// wireMailer adds a kit mail.Mailer to a module's Dependencies and constructs
// it (SMTP if MAIL_HOST is set, a no-op otherwise) in the HTTP entry point
// and the worker entry point when present. Idempotent: a second mail notifier
// added to any module skips these.
func wireMailer(eng *generator.Engine, proj *generator.Project, data generator.Data) error {
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
	if err := insertSnippet(eng, registerGo, "gpsystem:imports", "deps-mailer-imports", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:dependencies", "deps-mailer", data); err != nil {
		return err
	}
	if err := eng.FormatGoFile(registerGo); err != nil {
		return err
	}

	mainGo := filepath.Join(proj.Root, "cmd", data.Module, "main.go")
	if err := wireMailerInit(eng, mainGo, "gpsystem:dependencies-init", data); err != nil {
		return err
	}

	if workerWired(proj.Root) {
		workerGo := filepath.Join(proj.Root, "cmd", "worker", "main.go")
		if err := wireMailerInit(eng, workerGo, "gpsystem:deps-init("+data.Module+")", data); err != nil {
			return err
		}
	}
	return nil
}

// wireMailerInit inserts the mailer construction and its import at the given
// deps-init anchor in an entry point, plus the shared "mailer :=" line at the
// entry point's gpsystem:pools anchor.
func wireMailerInit(eng *generator.Engine, path, anchor string, data generator.Data) error {
	if err := insertSnippet(eng, path, "gpsystem:imports", "import-smtp", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, path, "gpsystem:pools", "mailer-pool-init", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, path, anchor, "mailer-deps-init", data); err != nil {
		return err
	}
	return eng.FormatGoFile(path)
}
