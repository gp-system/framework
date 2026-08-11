package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
)

func TestRenderComposeContent(t *testing.T) {
	dir := t.TempDir()
	proj := &generator.Project{
		Root: dir,
		Manifest: &generator.Manifest{
			Version: 1,
			Name:    "shop",
			Module:  "example.com/shop",
			Modules: map[string]*generator.ModuleEntry{
				"news": {},
				"auth": {},
			},
		},
	}

	content, err := renderComposeContent(proj, stackData(proj))
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)

	if !strings.Contains(got, "postgres:") || !strings.Contains(got, "mailpit:") || !strings.Contains(got, "rustfs:") {
		t.Errorf("base stack missing from rendered compose.yml:\n%s", got)
	}
	if !strings.Contains(got, "# gpsystem:services") {
		t.Errorf("anchor not preserved:\n%s", got)
	}
	if !strings.Contains(got, "CMD: news") || !strings.Contains(got, "CMD: auth") {
		t.Errorf("module app services missing:\n%s", got)
	}
	// Modules are inserted in sorted order (auth before news).
	if authIdx, newsIdx := strings.Index(got, "CMD: auth"), strings.Index(got, "CMD: news"); authIdx == -1 || newsIdx == -1 || newsIdx < authIdx {
		t.Errorf("modules not inserted in sorted order:\n%s", got)
	}
	if strings.Contains(got, "KitReplace") {
		t.Errorf("unrendered template action leaked into output:\n%s", got)
	}
	if strings.Contains(got, "additional_contexts") {
		t.Errorf("kit-replace wiring rendered without a replace directive:\n%s", got)
	}
}

func TestRenderComposeContentWithKitReplace(t *testing.T) {
	dir := t.TempDir()
	goMod := "module example.com/shop\n\ngo 1.25\n\nreplace " + KitModule + " => /kit/local\nreplace " + ErrsModule + " => /errs/local\nreplace github.com/gp-system/storage => /storage/local\nreplace github.com/gp-system/minio-storage-driver => /minio-storage-driver/local\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &generator.Project{
		Root: dir,
		Manifest: &generator.Manifest{
			Version: 1,
			Name:    "shop",
			Module:  "example.com/shop",
			Modules: map[string]*generator.ModuleEntry{"news": {}},
		},
	}

	content, err := renderComposeContent(proj, stackData(proj))
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	if !strings.Contains(got, "additional_contexts") || !strings.Contains(got, "kit: /kit/local") {
		t.Errorf("kit-replace wiring missing:\n%s", got)
	}
	if !strings.Contains(got, "errs: /errs/local") {
		t.Errorf("errs-replace wiring missing:\n%s", got)
	}
	// Regression: stackData must also populate Data.Mods from the project's
	// go.mod replaces, or a replaced Registry module (storage here) silently
	// loses its build context/mount (the bug new_project.go never had).
	if !strings.Contains(got, "storage: /storage/local") {
		t.Errorf("registry module replace wiring missing from stackData:\n%s", got)
	}
	if !strings.Contains(got, "minio-storage-driver: /minio-storage-driver/local") {
		t.Errorf("minio-storage-driver replace wiring missing from stackData:\n%s", got)
	}
}

// newTestRootCmd builds a minimal root command with the --dry-run/--force
// persistent flags addComposeCmd/addDockerCmd rely on, without going through
// the full Execute() tree.
func newTestRootCmd(sub *cobra.Command) *cobra.Command {
	root := &cobra.Command{Use: "gpsystem"}
	root.PersistentFlags().Bool("dry-run", false, "")
	root.PersistentFlags().Bool("force", false, "")
	root.AddCommand(sub)
	return root
}

func setupTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "version: 1\nname: shop\nmodule: example.com/shop\nengine: chi\nmodules: {}\n"
	if err := os.WriteFile(filepath.Join(dir, generator.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAddComposeCommand(t *testing.T) {
	dir := setupTestProject(t)
	t.Chdir(dir)

	root := newTestRootCmd(addComposeCmd())
	root.SetArgs([]string{"compose"})
	var out strings.Builder
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("first run: %v (output: %s)", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yml")); err != nil {
		t.Fatalf("compose.yml not written: %v", err)
	}

	// Without --force, a second run refuses to overwrite.
	root2 := newTestRootCmd(addComposeCmd())
	root2.SetArgs([]string{"compose"})
	root2.SetOut(&strings.Builder{})
	if err := root2.Execute(); err == nil {
		t.Fatal("want error re-running without --force")
	}

	// With --force, it overwrites.
	root3 := newTestRootCmd(addComposeCmd())
	root3.SetArgs([]string{"compose", "--force"})
	root3.SetOut(&strings.Builder{})
	if err := root3.Execute(); err != nil {
		t.Fatalf("forced run: %v", err)
	}
}

func TestAddComposeCommandDryRunWritesNothing(t *testing.T) {
	dir := setupTestProject(t)
	t.Chdir(dir)

	root := newTestRootCmd(addComposeCmd())
	root.SetArgs([]string{"compose", "--dry-run"})
	root.SetOut(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yml")); err == nil {
		t.Error("dry-run wrote compose.yml")
	}
}

func TestAddDockerCommand(t *testing.T) {
	dir := setupTestProject(t)
	t.Chdir(dir)

	root := newTestRootCmd(addDockerCmd())
	root.SetArgs([]string{"docker"})
	root.SetOut(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err != nil {
		t.Errorf("Dockerfile not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".dockerignore")); err != nil {
		t.Errorf(".dockerignore not written: %v", err)
	}

	// Without --force, refuses to overwrite (atomic across both files).
	root2 := newTestRootCmd(addDockerCmd())
	root2.SetArgs([]string{"docker"})
	root2.SetOut(&strings.Builder{})
	if err := root2.Execute(); err == nil {
		t.Fatal("want error re-running without --force")
	}

	root3 := newTestRootCmd(addDockerCmd())
	root3.SetArgs([]string{"docker", "--force"})
	root3.SetOut(&strings.Builder{})
	if err := root3.Execute(); err != nil {
		t.Fatalf("forced run: %v", err)
	}
}
