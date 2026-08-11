package templates

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gp-system/framework/internal/generator"
)

// TestSurfaceTreeRendersErrsSeamWithoutComments renders the surface skeleton
// and pins two guarantees: the handler adopts errs at the service boundary,
// and no generated file carries an explanatory comment (only functional
// directives and anchors survive). RenderTree runs goimports, so a rendered
// file that is not valid Go fails the render outright.
func TestSurfaceTreeRendersErrsSeamWithoutComments(t *testing.T) {
	eng := &generator.Engine{}
	files, err := eng.RenderTree(FS, "surface", newsData(), "/out")
	if err != nil {
		t.Fatalf("RenderTree: %v", err)
	}

	byName := map[string]string{}
	for _, f := range files {
		byName[filepath.Base(f.Path)] = string(f.Content)
		rel := filepath.ToSlash(f.Path)
		if strings.Contains(rel, "/repository/") {
			t.Errorf("surface tree rendered a repository file (repositories are module-level, from moduledomain): %s", f.Path)
		}
		if !strings.Contains(rel, "/out/") {
			continue
		}
		if strings.Contains(rel, "/internal/modules/news/") && !strings.Contains(rel, "/internal/modules/news/surfaces/api/") {
			t.Errorf("surface file rendered outside surfaces/api/: %s", f.Path)
		}
	}

	if byName["handler.go"] == "" {
		t.Fatal("handler.go not rendered")
	}
	handler := byName["news_handler.go"]
	if handler == "" {
		t.Fatal("news_handler.go not rendered")
	}
	if !strings.Contains(handler, "ErrListNews.Wrap(err,") {
		t.Errorf("news_handler.go does not wrap the service error with the surface's errs.Definition:\n%s", handler)
	}
	if byName["service.go"] == "" {
		t.Error("service.go seam not rendered")
	}

	errorsGo := byName["errors.go"]
	if errorsGo == "" {
		t.Fatal("errors.go seam not rendered")
	}
	if !strings.Contains(errorsGo, `"github.com/gp-system/errs"`) {
		t.Errorf("errors.go does not import errs:\n%s", errorsGo)
	}
	if !strings.Contains(errorsGo, "errs.Define(") {
		t.Errorf("errors.go does not declare an errs.Definition:\n%s", errorsGo)
	}

	// No .go file may carry an explanatory comment: only //go:*
	// directives and gpsystem anchors are allowed.
	for name, content := range byName {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "//") {
				continue
			}
			switch {
			case strings.HasPrefix(trimmed, "//go:"):
			case strings.Contains(trimmed, "gpsystem:"):
			default:
				t.Errorf("%s carries an explanatory comment: %q", name, trimmed)
			}
		}
	}
}

// TestModuleDomainTree pins the moduledomain skeleton: the module-root
// domain files (the package named after the module, with the ErrNotFound
// sentinel) plus the module-level repository package, which splits under a
// connection-named subfolder when DBConn is set (`add surface --db` /
// `add domain --db`) and stays flat otherwise.
func TestModuleDomainTree(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		eng := &generator.Engine{}
		files, err := eng.RenderTree(FS, "moduledomain", newsData(), "/out")
		if err != nil {
			t.Fatalf("RenderTree: %v", err)
		}

		byRel := map[string]string{}
		for _, f := range files {
			byRel[filepath.ToSlash(f.Path)] = string(f.Content)
		}
		domainGo := byRel["/out/internal/modules/news/news.go"]
		if domainGo == "" {
			t.Fatalf("news.go not rendered, got: %v", keys(byRel))
		}
		if !strings.Contains(domainGo, "package news") {
			t.Errorf("news.go has wrong package clause:\n%s", domainGo)
		}
		errorsGo := byRel["/out/internal/modules/news/errors.go"]
		if errorsGo == "" {
			t.Fatalf("errors.go not rendered, got: %v", keys(byRel))
		}
		if !strings.Contains(errorsGo, "package news") {
			t.Errorf("errors.go has wrong package clause:\n%s", errorsGo)
		}
		if !strings.Contains(errorsGo, `errs.Define("news_not_found"`) {
			t.Errorf("errors.go lacks the ErrNotFound sentinel:\n%s", errorsGo)
		}
		repoDoc := byRel["/out/internal/modules/news/repository/doc.go"]
		if repoDoc == "" {
			t.Fatalf("repository/doc.go not rendered, got: %v", keys(byRel))
		}
		if !strings.Contains(repoDoc, "package repository") {
			t.Errorf("flat repository doc.go has wrong package clause:\n%s", repoDoc)
		}
	})

	t.Run("named connection", func(t *testing.T) {
		data := newsData()
		data.DBConn = "analytics"
		eng := &generator.Engine{}
		files, err := eng.RenderTree(FS, "moduledomain", data, "/out")
		if err != nil {
			t.Fatalf("RenderTree: %v", err)
		}
		var hasNamed, hasFlat bool
		for _, f := range files {
			rel := filepath.ToSlash(f.Path)
			switch {
			case strings.HasSuffix(rel, "/repository/analytics/doc.go"):
				hasNamed = true
				if !strings.Contains(string(f.Content), "package analytics") {
					t.Errorf("named repository doc.go has wrong package clause:\n%s", f.Content)
				}
			case strings.HasSuffix(rel, "/repository/doc.go"):
				hasFlat = true
			}
		}
		if !hasNamed {
			t.Error("expected repository/analytics/doc.go, none rendered")
		}
		if hasFlat {
			t.Error("flat repository/doc.go rendered alongside the named connection")
		}
	})
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
