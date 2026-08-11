package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

// socialProviders are the goth-backed providers `add auth --social` knows
// how to pre-wire. Any other provider is a manual addition on top of the
// always-generated social package; see providers.go's own doc comment.
var socialProviders = []string{"discord", "facebook", "apple", "google"}

// parseSocialFlag validates and normalizes a comma-separated --social value.
func parseSocialFlag(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !slices.Contains(socialProviders, p) {
			return nil, fmt.Errorf("unsupported --social provider %q (one of %v)", p, socialProviders)
		}
		if slices.Contains(out, p) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// authMigrations are the auth schema's goose migrations, in FK-dependency
// order: each table is its own file (not one lumped migration), so a project
// can inspect, reorder concerns, or later split/replace a single table's
// migration without touching the rest.
var authMigrations = []struct {
	name     string // goose file suffix, e.g. "create_users"
	template string // source path under the embedded auth/migration tree
	sentinel string // substring that marks the table as already migrated
}{
	{"create_users", "authmigration/01_create_users.sql.tmpl", "CREATE TABLE users"},
	{"create_auth_credentials", "authmigration/02_create_auth_credentials.sql.tmpl", "CREATE TABLE auth_credentials"},
	{"create_roles", "authmigration/03_create_roles.sql.tmpl", "CREATE TABLE roles"},
	{"create_permissions", "authmigration/04_create_permissions.sql.tmpl", "CREATE TABLE permissions"},
	{"create_role_permissions", "authmigration/05_create_role_permissions.sql.tmpl", "CREATE TABLE role_permissions"},
	{"create_user_roles", "authmigration/06_create_user_roles.sql.tmpl", "CREATE TABLE user_roles"},
	{"create_refresh_tokens", "authmigration/07_create_refresh_tokens.sql.tmpl", "CREATE TABLE refresh_tokens"},
	{"create_email_verification_tokens", "authmigration/08_create_email_verification_tokens.sql.tmpl", "CREATE TABLE email_verification_tokens"},
	{"create_password_reset_tokens", "authmigration/09_create_password_reset_tokens.sql.tmpl", "CREATE TABLE password_reset_tokens"},
}

func addAuthCmd() *cobra.Command {
	var module string
	var social string

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Scaffold a full auth module: DB-backed RBAC schema + register/verify-email/login/refresh/logout/forgot-reset-password",
		Long: `Generates a complete auth module (its own cmd/<module> entry point):
a TypeSpec contract, http/service layers over the module-root domain package
and repository, and one goose migration
per table for a DB-backed RBAC schema (users, roles, permissions,
role_permissions, user_roles) plus refresh/email-verification/password-reset
tokens (each stored only as a SHA-256 hash).

The kit's auth/rbac packages are deliberately stateless (JWT sign/parse +
middleware + guards only); this command generates the persistence and
endpoints every project otherwise hand-writes: Register (sends an email
verification link), VerifyEmail, Login (requires a verified email), Refresh,
Logout, ForgotPassword and ResetPassword (both over email).

The repository always runs on the kit's pgx executor, regardless of the
project's DB choice for its business modules: a hand-written pgx repository
cannot see a *bun.DB's transaction, so its own AuthDB/AuthTransactor stay
pgx-backed unconditionally (mirroring the outbox/notification database
channel's own pg.NewDB(pool) wiring).

The module also always gets a provider-agnostic social login layer (a
markbates/goth-backed registry + /social/{provider} begin/callback
endpoints, calling into the same Login/token machinery as email+password).
--social pre-wires any of discord, facebook, apple, google; any other goth
provider (or a hand-written goth.Provider) is a one-line manual addition on
top; see internal/modules/<module>/social/providers.go's doc comment.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := generator.ValidateName("module", module); err != nil {
				return err
			}
			socialList, err := parseSocialFlag(social)
			if err != nil {
				return err
			}

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			if _, exists := proj.Manifest.Modules[module]; exists {
				return fmt.Errorf("module %q already exists", module)
			}

			replaces := projectReplaces(proj)
			data := generator.Data{
				Project:  projName(proj),
				GoModule: proj.Manifest.Module,
				Module:   module,
				Surface:  "api",
				DB:       proj.Manifest.DBName(),
				Social:   socialList,
				Auth:     true, // the auth module's own main.go renders the identity middleware directly
				Kit:      kitRef(replaces[generator.Kit.Path]),
				Errs:     errsRef(replaces[generator.Errs.Path]),
				Mods:     registryModsByPath(replaces),
			}

			eng := newEngine(cmd)

			// The generic module skeleton: cmd/<module>/main.go +
			// surfaces/register.go with Dependencies and the gpsystem:*
			// anchors, exactly as `new module` renders it.
			files, err := eng.RenderTree(templates.FS, "module", data, proj.Root)
			if err != nil {
				return err
			}
			if err := eng.Apply(files); err != nil {
				return err
			}

			// The auth-specific content: TypeSpec, oapi-codegen config, the
			// module-root domain/repository/http/service/policy/mail Go
			// layers, one tree, like generateSurface's "surface" tree.
			authFiles, err := eng.RenderTree(templates.FS, "auth", data, proj.Root)
			if err != nil {
				return err
			}
			assetFiles, err := authAssetFiles(data, proj.Root)
			if err != nil {
				return err
			}
			all := append(authFiles, assetFiles...)
			if err := eng.Apply(all); err != nil {
				return err
			}

			if err := generateModuleWorker(eng, proj, data); err != nil {
				return err
			}

			if err := wireAuthConfig(eng, proj, data); err != nil {
				return err
			}
			if err := wireAuthModule(eng, proj, data); err != nil {
				return err
			}
			if err := wireAuthMiddleware(eng, cmd, proj, data); err != nil {
				return err
			}
			// Mailer plumbing (Dependencies field, SMTP-or-discard
			// construction at every entry point) is identical to `add mail`'s
			// first-notifier wiring; reuse it rather than duplicating it.
			if err := wireMailer(eng, proj, data); err != nil {
				return err
			}

			mainGo := filepath.Join(proj.Root, "cmd", data.Module, "main.go")
			if err := insertSnippet(eng, mainGo, "gpsystem:dependencies-init", "auth-deps-init", data); err != nil {
				return err
			}
			if err := eng.FormatGoFile(mainGo); err != nil {
				return err
			}

			if err := writeAuthMigrations(eng, proj); err != nil {
				return err
			}

			if err := wireCompose(eng, cmd.OutOrStdout(), proj, data); err != nil {
				return err
			}

			if !dryRunFlag(cmd) {
				if err := proj.Manifest.AddSurface(module, "api"); err != nil {
					return err
				}
				proj.Manifest.Auth = module
				if err := proj.Manifest.Save(proj.Root); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Auth module %q generated. Next steps:

  go mod tidy
  mise run generate        # tsp -> OpenAPI -> strict server code -> build
  go run ./cmd/migrate up  # apply the 9 auth schema migrations
  go run ./cmd/%s

Set JWT_SECRET, APP_BASE_URL (and MAIL_HOST to send real email) in .env, then:

  POST /api/v1/%s/register        {name, email, password}
  POST /api/v1/%s/verify-email    {token}
  POST /api/v1/%s/login           {email, password}
  POST /api/v1/%s/refresh         {refreshToken}
  POST /api/v1/%s/logout          {refreshToken}
  POST /api/v1/%s/password/forgot {email}
  POST /api/v1/%s/password/reset  {token, password}
  GET  /api/v1/%s/social/{provider}          # redirects to the provider's consent screen
  GET  /api/v1/%s/social/{provider}/callback # most providers (query-string callback)
`, module, module, module, module, module, module, module, module, module, module, module)
			if len(socialList) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "\nPre-wired social providers: %s. Set their OAUTH_<PROVIDER>_* env vars in .env (each provider's app console gives you the client id/secret).\n", strings.Join(socialList, ", "))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "\nNo social providers pre-wired; add one with `add auth --social discord,facebook,apple,google` on a fresh module, or wire it by hand, see internal/modules/"+module+"/social/providers.go.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&module, "module", "auth", "name of the generated module")
	cmd.Flags().StringVar(&social, "social", "", "comma-separated social login providers to pre-wire: discord,facebook,apple,google")
	return cmd
}

// authAssetFiles builds the html/text template files for the two auth mail
// notifiers. Copied verbatim (never rendered by the gpsystem engine): they
// are html/template and text/template sources consumed at runtime by
// mail.Template, mirroring mailAssetFiles/notificationAssetFiles.
func authAssetFiles(data generator.Data, root string) ([]generator.File, error) {
	dir := filepath.Join(root, "internal", "modules", data.Module, "mail", "templates")

	var files []generator.File
	for _, name := range []string{"verify_email", "reset_password"} {
		for _, kind := range []string{"html", "txt"} {
			raw, err := templates.FS.ReadFile(fmt.Sprintf("authassets/%s.%s.tmpl", name, kind))
			if err != nil {
				return nil, err
			}
			files = append(files, generator.File{
				Path:    filepath.Join(dir, fmt.Sprintf("%s.%s.tmpl", name, kind)),
				Content: raw,
			})
		}
	}
	return files, nil
}

// wireAuthConfig adds the JWT + AUTH_ settings to platform config.go: the
// kit's auth.Config under JWT_, plus the app base URL and token TTLs the
// service needs to build email links.
func wireAuthConfig(eng *generator.Engine, proj *generator.Project, data generator.Data) error {
	configGo := filepath.Join(proj.Root, "internal", "platform", "config", "config.go")
	if err := insertSnippet(eng, configGo, "gpsystem:imports", "import-auth", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, configGo, "gpsystem:config", "auth-config", data); err != nil {
		return err
	}
	return eng.FormatGoFile(configGo)
}

// wireAuthModule wires the auth module's own surfaces/register.go
// (Dependencies + RegisterApi) and main.tsp/cmd/<module>/main.go
// registrations. Mirrors
// generateSurface's anchor sequence but with auth-specific snippets, since
// the generic register-surface snippet constructs a parameterless
// service.New().
func wireAuthModule(eng *generator.Engine, proj *generator.Project, data generator.Data) error {
	mainTsp := filepath.Join(proj.Root, "spec", "typespec", "main.tsp")
	if err := insertSnippet(eng, mainTsp, "gpsystem:modules", "main-tsp-imports", data); err != nil {
		return err
	}

	mainGo := filepath.Join(proj.Root, "cmd", data.Module, "main.go")
	if err := insertSnippet(eng, mainGo, "gpsystem:registrations", "main-register-call", data); err != nil {
		return err
	}
	if err := eng.FormatGoFile(mainGo); err != nil {
		return err
	}

	registerSnippet := "auth-register-surface"
	registerGo, err := moduleRegisterPath(proj.Root, data.Module)
	if err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:imports", "auth-register-imports", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:imports", "deps-auth-imports", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:dependencies", "deps-auth", data); err != nil {
		return err
	}
	if err := insertSnippet(eng, registerGo, "gpsystem:surfaces", registerSnippet, data); err != nil {
		return err
	}
	return eng.FormatGoFile(registerGo)
}

// wireAuthMiddleware inserts the identity middleware (optional
// authentication: anonymous without a token, 401 on a bad one, an
// rbac.Identity in the context on a good one) into every OTHER existing
// module's api router, so @permission/@policy tags enforce project-wide out
// of the box. The auth module's own main.go rendered it directly via
// Data.Auth, and modules generated later get it the same way (new_module
// reads Manifest.Auth). cfg.JWT and the server import are already present
// in every module entry point, so no Dependencies plumbing is needed.
//
// A module main.go without the gpsystem:api-middleware anchor (generated
// before the middleware feature) is skipped with the engine's manual-fix
// hint instead of failing the whole command.
func wireAuthMiddleware(eng *generator.Engine, cmd *cobra.Command, proj *generator.Project, data generator.Data) error {
	for name := range proj.Manifest.Modules {
		if name == data.Module {
			continue
		}
		mainGo := filepath.Join(proj.Root, "cmd", name, "main.go")
		if _, err := os.Stat(mainGo); err != nil {
			continue // module without its own entry point
		}
		err := insertSnippet(eng, mainGo, "gpsystem:api-middleware", "api-auth-middleware", data)
		if errors.Is(err, generator.ErrAnchorNotFound) {
			// The engine already printed the AnchorFixHint block.
			fmt.Fprintf(cmd.OutOrStdout(), "skipping cmd/%s/main.go: add the line above by hand\n", name)
			continue
		}
		if err != nil {
			return err
		}
		if err := eng.FormatGoFile(mainGo); err != nil {
			return err
		}
	}
	return nil
}

// writeAuthMigrations writes the auth schema's goose migrations, one file per
// table, at consecutive timestamp prefixes (so FK-dependency order survives
// goose's lexical ordering), unless a migration for that table already
// exists.
func writeAuthMigrations(eng *generator.Engine, proj *generator.Project) error {
	migrationsDir := filepath.Join(proj.Root, "migrations")

	existing := map[string]bool{}
	if entries, err := os.ReadDir(migrationsDir); err == nil {
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
			if err != nil {
				continue
			}
			for _, m := range authMigrations {
				if strings.Contains(string(data), m.sentinel) {
					existing[m.name] = true
				}
			}
		}
	}

	prefix, err := nextMigrationPrefix(migrationsDir)
	if err != nil {
		return err
	}
	base, perr := time.Parse(migrationTimestampFormat, prefix)
	if perr != nil {
		return perr
	}

	var files []generator.File
	for i, m := range authMigrations {
		if existing[m.name] {
			continue
		}
		raw, err := templates.FS.ReadFile(m.template)
		if err != nil {
			return err
		}
		ts := base.Add(time.Duration(i) * time.Second).Format(migrationTimestampFormat)
		path := filepath.Join(migrationsDir, fmt.Sprintf("%s_%s.sql", ts, m.name))
		files = append(files, generator.File{Path: path, Content: raw})
	}
	if len(files) == 0 {
		return nil
	}
	return eng.Apply(files)
}
