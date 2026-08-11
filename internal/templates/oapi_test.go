package templates

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oapi-codegen/oapi-codegen/v2/pkg/codegen"
	"github.com/oapi-codegen/oapi-codegen/v2/pkg/util"
)

// loadChiUserTemplates flattens the embedded oapi tree into the map shape
// oapi-codegen expects for user-templates.
func loadChiUserTemplates(t *testing.T) map[string]string {
	t.Helper()
	root, err := OAPI()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	err = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		out[path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// generateChi runs oapi-codegen the way chi projects do: built-in chi routing
// templates plus the kit's minimal strict overrides (Object suffix dropped so
// the business handler templates are router-agnostic).
func generateChi(t *testing.T) string {
	t.Helper()
	spec, err := util.LoadSwagger("testdata/spec.yaml")
	if err != nil {
		t.Fatal(err)
	}

	code, err := codegen.Generate(spec, codegen.Configuration{
		PackageName: "covgen",
		Generate: codegen.GenerateOptions{
			Models:    true,
			ChiServer: true,
			Strict:    true,
		},
		OutputOptions: codegen.OutputOptions{
			UserTemplates: loadChiUserTemplates(t),
		},
	})
	if err != nil {
		t.Fatalf("codegen failed: %v", err)
	}
	return code
}

// TestChiGeneratedCodeContract asserts the identifiers the register-surface
// snippet and server.StrictValidator depend on. This is the tripwire for
// oapi-codegen bumps: if upstream renames these, the snippet must follow.
func TestChiGeneratedCodeContract(t *testing.T) {
	code := generateChi(t)
	for _, ident := range []string{
		"func HandlerWithOptions(",
		"type ChiServerOptions struct",
		"func NewStrictHandlerWithOptions(",
		"type StrictHTTPServerOptions struct",
		"type StrictMiddlewareFunc",
		"type StrictHandlerFunc",
		"ErrorHandlerFunc func(w http.ResponseWriter, r *http.Request, err error)",
		"RequestErrorHandlerFunc",
		"ResponseErrorHandlerFunc",
		"BaseRouter",
		"chi.Router",
	} {
		if !strings.Contains(code, ident) {
			t.Errorf("generated chi code lacks %q: the register-surface snippet would not compile", ident)
		}
	}
	// StrictHandlerFunc must instantiate server.StrictValidator's type constraint.
	if !strings.Contains(code, "type StrictHandlerFunc func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error)") {
		t.Error("StrictHandlerFunc signature changed: server.StrictValidator's type parameter constraint must be updated")
	}
	if strings.Contains(code, "gofiber") {
		t.Error("chi output references fiber")
	}
	// The kit's strict overrides drop the Object suffix so the business
	// handler templates (gen.<Op>Request / gen.<Op>Response) fit both routers.
	if strings.Contains(code, "RequestObject") || strings.Contains(code, "ResponseObject") {
		t.Error("chi output still uses the Object suffix: the oapi strict overrides were not applied")
	}
}

// TestChiGeneratedCodeExposesAuthzMaps pins the authorization-enforcer
// contract: the x-permission / x-policy extensions must land in the
// generated PermissionByOperation/PolicyByOperation maps.
func TestChiGeneratedCodeExposesAuthzMaps(t *testing.T) {
	code := generateChi(t)
	for _, want := range []string{
		"var PermissionByOperation = map[string]string{",
		"var PolicyByOperation = map[string]string{",
		`"UpdateThing": "things.write"`,
		`"UpdateThing": "things.owner"`,
		`"DeleteThing": "things.owner"`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("generated chi code lacks %q: x-permission/x-policy extensions were not emitted", want)
		}
	}
	if strings.Contains(code, `"ListThings": `) {
		t.Error("untagged operation leaked into the authz maps")
	}
}

// TestChiGeneratedCodeCompiles materializes the generated package in a temp
// module and builds it against chi.
func TestChiGeneratedCodeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("compile test skipped in -short mode")
	}
	code := generateChi(t)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := `module covgen

go 1.25
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"mod", "tidy"},
		{"build", "./..."},
	} {
		cmd := exec.Command(goBin, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
