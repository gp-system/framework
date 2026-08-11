package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
)

func TestParseSocialFlag(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "empty", raw: "", want: nil},
		{name: "single", raw: "discord", want: []string{"discord"}},
		{name: "multiple with spaces and case", raw: " Discord, Google ,apple", want: []string{"discord", "google", "apple"}},
		{name: "dedupes", raw: "discord,discord", want: []string{"discord"}},
		{name: "unknown provider rejected", raw: "discord,twitter", wantErr: true},
		{name: "blank segments ignored", raw: "discord,,google", want: []string{"discord", "google"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSocialFlag(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSocialFlag(%q): want error, got %v", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSocialFlag(%q): unexpected error: %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseSocialFlag(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestWireAuthMiddleware pins `add auth`'s project-wide identity wiring: a
// sibling module main.go with the gpsystem:api-middleware anchor receives
// the api.Use line, one without the anchor (generated before the middleware
// feature) is skipped with a hint instead of failing, and the auth module's
// own entry point (rendered with Data.Auth) is left alone.
func TestWireAuthMiddleware(t *testing.T) {
	root := t.TempDir()

	const withAnchor = `package main

import (
	"github.com/go-chi/chi/v5"

	"example.com/shop/internal/platform/config"
)

func run(cfg config.Config) {
	_ = func(r chi.Router) error {
		api := chi.NewRouter()
		// gpsystem:api-middleware
		r.Mount("/api/v1", api)
		return nil
	}
}
`
	const withoutAnchor = `package main

func main() {}
`
	writeMain := func(module, content string) {
		t.Helper()
		dir := filepath.Join(root, "cmd", module)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeMain("news", withAnchor)
	writeMain("legacy", withoutAnchor)

	proj := &generator.Project{
		Root: root,
		Manifest: &generator.Manifest{
			Version: 1,
			Modules: map[string]*generator.ModuleEntry{
				"news":   {Surfaces: []string{"api"}},
				"legacy": {Surfaces: []string{"api"}},
				"auth":   {Surfaces: []string{"api"}},
			},
		},
	}

	var out strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	eng := &generator.Engine{Out: &out}

	data := generator.Data{Module: "auth", Kit: generator.ModuleRef{Module: generator.Kit}}
	if err := wireAuthMiddleware(eng, cmd, proj, data); err != nil {
		t.Fatalf("wireAuthMiddleware: %v", err)
	}

	news, err := os.ReadFile(filepath.Join(root, "cmd", "news", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(news), "api.Use(server.IdentityMiddleware(cfg.JWT))") {
		t.Errorf("news main.go lacks the identity middleware:\n%s", news)
	}
	legacy, err := os.ReadFile(filepath.Join(root, "cmd", "legacy", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "IdentityMiddleware") {
		t.Errorf("legacy main.go modified despite the missing anchor:\n%s", legacy)
	}
	if !strings.Contains(out.String(), "gpsystem:api-middleware") {
		t.Errorf("missing-anchor hint not printed:\n%s", out.String())
	}
}
