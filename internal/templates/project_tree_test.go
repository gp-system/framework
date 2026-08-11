package templates

import (
	"path/filepath"
	"testing"

	"github.com/gp-system/framework/internal/generator"
)

// TestProjectTreeRootFiles pins the top-level files `new project` must stamp.
// A sablonfa dotfile-jai (.env.example, .gitignore, .dockerignore) különösen
// sérülékenyek: a `.tmpl` suffix vagy a fájlnév elrontása, illetve egy
// git-ignorált sablonfájl (friss klónból hiányzik) itt bukik el egyetlen,
// egyértelmű hibaüzenettel.
func TestProjectTreeRootFiles(t *testing.T) {
	eng := &generator.Engine{}
	files, err := eng.RenderTree(FS, "project", projectData(""), "/out")
	if err != nil {
		t.Fatalf("RenderTree project: %v", err)
	}
	got := make(map[string]bool, len(files))
	for _, f := range files {
		rel, err := filepath.Rel("/out", f.Path)
		if err != nil {
			t.Fatalf("Rel %s: %v", f.Path, err)
		}
		got[filepath.ToSlash(rel)] = true
	}
	for _, want := range []string{
		".dockerignore",
		".env.example",
		".gitignore",
		".golangci.yml",
		"Dockerfile",
		"README.md",
		"compose.yml",
		"go.mod",
		"mise.toml",
		"package.json",
		"tspconfig.yaml",
	} {
		if !got[want] {
			t.Errorf("project tree missing %q", want)
		}
	}
}
