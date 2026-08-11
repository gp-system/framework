package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

// wireCompose ensures a project has the containerized dev stack: the
// Dockerfile/.dockerignore (created if missing) and, in compose.yml /
// .env.example, the mailpit/rustfs backing services and this module's
// app-service. `new project` already ships all of this via compose.yml.tmpl;
// this is mainly the backfill path for `new module` on an older project. To
// (re)generate compose.yml or the Dockerfile/.dockerignore standalone, use
// `gpsystem add compose` / `gpsystem add docker` (add_compose.go) instead.
func wireCompose(eng *generator.Engine, out io.Writer, proj *generator.Project, data generator.Data) error {
	if err := writeIfMissing(eng, proj.Root, "Dockerfile", "project/Dockerfile.tmpl", data); err != nil {
		return err
	}
	if err := writeIfMissing(eng, proj.Root, ".dockerignore", "project/.dockerignore.tmpl", data); err != nil {
		return err
	}

	composePath := filepath.Join(proj.Root, "compose.yml")
	if _, err := os.Stat(composePath); err != nil {
		// A missing compose.yml on an older project is not a failure: print a
		// note and skip wiring, the same fallback optionalInsert uses below.
		fmt.Fprintf(out, "note: %s not found; run `gpsystem add compose` to generate it\n", composePath)
		return nil //nolint:nilerr
	}
	compose, err := os.ReadFile(composePath)
	if err != nil {
		return err
	}
	content := string(compose)

	optionalInsert(eng, out, composePath, "gpsystem:services", "compose-mailpit", data)
	optionalInsert(eng, out, composePath, "gpsystem:services", "compose-rustfs", data)
	optionalInsert(eng, out, composePath, "gpsystem:services", "compose-rustfs-init", data)

	if strings.Contains(content, "pg_isready") {
		optionalInsert(eng, out, composePath, "gpsystem:services", "compose-module", data)
	} else {
		printSnippet(out, "compose-module", data, fmt.Sprintf(
			"%s has no postgres healthcheck (pg_isready): a service_healthy dependency on it "+
				"would break `docker compose up`; add this module service manually once postgres has one:",
			filepath.Base(composePath)))
	}

	if !strings.Contains(content, "proxynet") || !strings.Contains(content, "rustfsdata") {
		fmt.Fprintf(out, `
note: %s is missing the proxynet network and/or the rustfsdata/gomodcache/
gocache volumes, add this at the top level manually:

networks:
  proxynet:
    external: true

volumes:
  rustfsdata:
  gomodcache:
  gocache:
`, filepath.Base(composePath))
	}

	envPath := filepath.Join(proj.Root, ".env.example")
	if envContent, err := os.ReadFile(envPath); err == nil && strings.Contains(string(envContent), "DB_HOST=localhost") {
		fmt.Fprintf(out, "\nnote: %s still has DB_HOST=localhost: the app now runs inside compose; change it to DB_HOST=postgres\n", filepath.Base(envPath))
	}
	optionalInsert(eng, out, envPath, "gpsystem:env", "env-stack", data)

	return nil
}

// writeIfMissing creates path from a rendered project-tree template unless it
// already exists (an older project may have hand-edited or removed it).
func writeIfMissing(eng *generator.Engine, root, relPath, tmplPath string, data generator.Data) error {
	target := filepath.Join(root, relPath)
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	raw, err := templates.FS.ReadFile(tmplPath)
	if err != nil {
		return err
	}
	rendered, err := generator.RenderSnippet(string(raw), data)
	if err != nil {
		return fmt.Errorf("render %s: %w", tmplPath, err)
	}
	return eng.Apply([]generator.File{{Path: target, Content: []byte(rendered)}})
}

// printSnippet renders a snippet and prints it for manual insertion, along
// with the caller-supplied reason it could not be inserted automatically.
func printSnippet(out io.Writer, name string, data generator.Data, why string) {
	raw, err := templates.Snippet(name)
	if err != nil {
		return
	}
	rendered, err := generator.RenderSnippet(raw, data)
	if err != nil {
		return
	}
	fmt.Fprintf(out, "\nnote: %s\n\n%s\n", why, rendered)
}
