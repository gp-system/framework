// Package cli implements the gpsystem command tree.
package cli

import (
	"path/filepath"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
)

// KitModule is the kit's module path, baked into generated go.mod files and
// import paths.
const KitModule = "github.com/gp-system/framework"

// ErrsModule is the standalone errs module path. Generated code imports it
// directly, so generated go.mod files require it alongside the kit.
const ErrsModule = "github.com/gp-system/errs"

// newEngine builds an Engine from the --dry-run/--force persistent flags
// inherited from the root command.
func newEngine(cmd *cobra.Command) *generator.Engine {
	return &generator.Engine{DryRun: dryRunFlag(cmd), Force: forceFlag(cmd)}
}

func dryRunFlag(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("dry-run")
	return v
}

func forceFlag(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("force")
	return v
}

// warnProject prints proj.Warning to cmd's error stream, if set.
func warnProject(cmd *cobra.Command, proj *generator.Project) {
	if proj.Warning != "" {
		cmd.PrintErrln(proj.Warning)
	}
}

// kitVersion resolves the kit's version from build info: when consumers run
// the CLI via `go tool gpsystem`, the kit is the main module with a real
// version. Falls back to v0.0.0 for development builds (pair with
// --replace/--replace-dev).
func kitVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Path == KitModule && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
		for _, dep := range bi.Deps {
			if dep.Path == KitModule {
				return dep.Version
			}
		}
	}
	return "v0.0.0"
}

// errsVersion resolves the errs module's version from build info: errs is a
// dependency of the kit, so it appears in Deps both under `go tool gpsystem`
// and in development builds. Falls back to v0.0.0 when unresolved (pair with
// --replace-dev, or --replace errs=...).
func errsVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range bi.Deps {
			if dep.Path == ErrsModule {
				return dep.Version
			}
		}
	}
	return "v0.0.0"
}

// kitRef builds the Data.Kit module reference shared across generators: the
// registry's Kit module pinned to the resolved build version, with an
// optional local replace path (relative to the project root, or "" to use
// the published version).
func kitRef(replace string) generator.ModuleRef {
	return generator.ModuleRef{Module: generator.Kit.Pin(kitVersion()), Replace: replace}
}

// errsRef is kitRef's counterpart for the standalone errs module.
func errsRef(replace string) generator.ModuleRef {
	return generator.ModuleRef{Module: generator.Errs.Pin(errsVersion()), Replace: replace}
}

// registryModsByPath builds the Data.Mods list shared across generators that
// read replaces out of an existing project's go.mod (keyed by module path, as
// ReadReplaces produces): every Registry module, paired with its replace
// target if the project's go.mod has one, "" (published version, no mount)
// otherwise.
func registryModsByPath(replaces map[string]string) []generator.ModuleRef {
	mods := make([]generator.ModuleRef, 0, len(generator.Registry))
	for _, m := range generator.Registry {
		mods = append(mods, generator.ModuleRef{Module: m, Replace: replaces[m.Path]})
	}
	return mods
}

// projectReplaces reads the replace directives out of proj's go.mod,
// tolerating a missing/unreadable file: ReadReplaces then returns a nil map,
// and looking up any module path in a nil map yields "" (no replace).
func projectReplaces(proj *generator.Project) map[string]string {
	replaces, _ := generator.ReadReplaces(filepath.Join(proj.Root, "go.mod"))
	return replaces
}

// Execute runs the gpsystem CLI, parsing os.Args and dispatching to the
// matched subcommand.
func Execute() error {
	root := &cobra.Command{
		Use:           "gpsystem",
		Short:         "Scaffolding CLI for gpsystem-based modular Go backends",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().Bool("dry-run", false, "print the plan without writing anything")
	root.PersistentFlags().Bool("force", false, "overwrite existing files")

	newCmd := &cobra.Command{Use: "new", Short: "Generate a new project, module or migration"}
	newCmd.AddCommand(newProjectCmd(), newModuleCmd(), newMigrationCmd())

	addCmd := &cobra.Command{Use: "add", Short: "Add a surface, handler, domain, event, listener, job, mail notifier, notification, realtime gateway, worker, database connection, seeder, compose stack or dockerization to a project"}
	addCmd.AddCommand(addSurfaceCmd(), addDomainCmd(), addHandlerCmd(), addEventCmd(), addListenerCmd(), addJobCmd(), addMailCmd(), addNotificationCmd(), addWorkerCmd(), addRealtimeCmd(), addDBCmd(), addSeederCmd(), addComposeCmd(), addDockerCmd(), addAuthCmd())

	root.AddCommand(newCmd, addCmd, versionCmd())
	return root.Execute()
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the kit version this CLI was built from",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(KitModule, kitVersion())
		},
	}
}
