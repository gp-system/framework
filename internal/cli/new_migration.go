package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

// migrationTimestampFormat is goose's timestamp versioning layout: a 14-digit
// UTC "YYYYMMDDHHMMSS" prefix, e.g. "20260720143012_add_blog.sql".
const migrationTimestampFormat = "20060102150405"

// migrationPrefixRe matches the 14-digit timestamp prefix of a goose
// migration file, e.g. "20260720143012_add_blog.sql".
var migrationPrefixRe = regexp.MustCompile(`^(\d{14})_`)

func newMigrationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migration <name>",
		Short: "Create a new goose SQL migration in the project's migrations/ directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := generator.ToSnake(args[0])
			if name == "" {
				return fmt.Errorf("migration name %q is empty after normalization", args[0])
			}
			if err := generator.ValidateSnakeName("migration", name); err != nil {
				return err
			}

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			migrationsDir := filepath.Join(proj.Root, "migrations")
			prefix, err := nextMigrationPrefix(migrationsDir)
			if err != nil {
				return err
			}

			body, err := templates.Snippet("migration-sql")
			if err != nil {
				return err
			}

			path := filepath.Join(migrationsDir, fmt.Sprintf("%s_%s.sql", prefix, name))
			// Defense in depth: the validated name cannot traverse, but assert
			// the resolved path stays within migrations/ before writing.
			if rel, err := filepath.Rel(migrationsDir, path); err != nil || rel != filepath.Base(path) {
				return fmt.Errorf("migration path %q escapes the migrations directory", path)
			}
			eng := newEngine(cmd)
			if err := eng.Apply([]generator.File{{Path: path, Content: []byte(body)}}); err != nil {
				return err
			}

			if !dryRunFlag(cmd) {
				fmt.Fprintf(cmd.OutOrStdout(), `
Migration created: %s

Edit the SQL between the goose markers, then apply it with:

  go run ./cmd/migrate up
`, path)
			}
			return nil
		},
	}
	return cmd
}

// nextMigrationPrefix returns the next goose timestamp prefix for a new
// migration in dir, derived from the current UTC time.
func nextMigrationPrefix(dir string) (string, error) {
	return nextMigrationPrefixAt(dir, time.Now().UTC())
}

// nextMigrationPrefixAt returns the goose timestamp prefix to use for a
// migration created at now, given the existing files in dir. It starts from
// now's own "YYYYMMDDHHMMSS" prefix and steps forward one second at a time
// until it finds a prefix not already used by a file in dir (guarding
// against two migrations generated within the same second). Returns now's
// prefix unchanged when dir is empty or absent.
func nextMigrationPrefixAt(dir string, now time.Time) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	taken := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if m := migrationPrefixRe.FindStringSubmatch(e.Name()); m != nil {
			taken[m[1]] = true
		}
	}
	for t := now; ; t = t.Add(time.Second) {
		prefix := t.Format(migrationTimestampFormat)
		if !taken[prefix] {
			return prefix, nil
		}
	}
}
