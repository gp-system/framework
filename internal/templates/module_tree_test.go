package templates

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gp-system/framework/internal/generator"
)

// TestModuleMainHasDBAnchors pins the two anchors `add db` depends on to wire
// a named connection's pool and closer into a module's HTTP entry point.
// Their absence would make `add db` fail its preflight check against a
// freshly generated project.
func TestModuleMainHasDBAnchors(t *testing.T) {
	eng := &generator.Engine{}
	data := newsData()
	files, err := eng.RenderTree(FS, "module", data, "/out")
	if err != nil {
		t.Fatalf("RenderTree module: %v", err)
	}
	var mainGo string
	for _, f := range files {
		if strings.HasSuffix(filepath.ToSlash(f.Path), "cmd/news/main.go") {
			mainGo = string(f.Content)
		}
	}
	if mainGo == "" {
		t.Fatal("cmd/news/main.go not rendered")
	}
	for _, anchor := range []string{"gpsystem:pools", "gpsystem:closers"} {
		if !strings.Contains(mainGo, anchor) {
			t.Errorf("cmd/news/main.go missing anchor %q", anchor)
		}
	}
}

// TestModuleMainHasMiddlewareAnchors pins the two seams the middleware
// feature depends on: gpsystem:api-middleware (where `add auth` wires the
// identity middleware) and gpsystem:middleware (where server.WithStack /
// WithMiddleware options are inserted).
func TestModuleMainHasMiddlewareAnchors(t *testing.T) {
	mainGo := renderModuleMain(t, newsData())
	for _, anchor := range []string{"gpsystem:api-middleware", "gpsystem:middleware"} {
		if !strings.Contains(mainGo, anchor) {
			t.Errorf("cmd/news/main.go missing anchor %q", anchor)
		}
	}
	if strings.Contains(mainGo, "IdentityMiddleware") {
		t.Error("main.go renders the identity middleware without Data.Auth")
	}
}

// TestModuleMainRendersIdentityMiddlewareWithAuth pins that a module
// generated after `add auth` (Data.Auth true) authenticates its api router
// out of the box.
func TestModuleMainRendersIdentityMiddlewareWithAuth(t *testing.T) {
	data := newsData()
	data.Auth = true
	mainGo := renderModuleMain(t, data)
	if !strings.Contains(mainGo, "api.Use(server.IdentityMiddleware(cfg.JWT))") {
		t.Fatalf("main.go with Data.Auth lacks the identity middleware:\n%s", mainGo)
	}
}

// TestModuleTreeWiring pins the module-root domain layout: the wiring lives
// in surfaces/register.go (package surfaces, with the three anchors the add
// commands insert at), and the entry point reaches it through the
// <module>surfaces alias, keeping the module root free for the domain
// package.
func TestModuleTreeWiring(t *testing.T) {
	eng := &generator.Engine{}
	files, err := eng.RenderTree(FS, "module", newsData(), "/out")
	if err != nil {
		t.Fatalf("RenderTree module: %v", err)
	}
	var registerGo, mainGo string
	for _, f := range files {
		switch {
		case strings.HasSuffix(filepath.ToSlash(f.Path), "internal/modules/news/surfaces/register.go"):
			registerGo = string(f.Content)
		case strings.HasSuffix(filepath.ToSlash(f.Path), "cmd/news/main.go"):
			mainGo = string(f.Content)
		}
	}
	if registerGo == "" {
		t.Fatal("internal/modules/news/surfaces/register.go not rendered")
	}
	if !strings.Contains(registerGo, "package surfaces") {
		t.Errorf("surfaces/register.go has wrong package clause:\n%s", registerGo)
	}
	for _, anchor := range []string{"gpsystem:imports", "gpsystem:dependencies", "gpsystem:surfaces"} {
		if !strings.Contains(registerGo, anchor) {
			t.Errorf("surfaces/register.go missing anchor %q", anchor)
		}
	}
	if mainGo == "" {
		t.Fatal("cmd/news/main.go not rendered")
	}
	if !strings.Contains(mainGo, `newssurfaces "example.com/shop/internal/modules/news/surfaces"`) {
		t.Errorf("main.go lacks the aliased surfaces import:\n%s", mainGo)
	}
	if !strings.Contains(mainGo, "deps := newssurfaces.Dependencies{") {
		t.Errorf("main.go does not construct newssurfaces.Dependencies:\n%s", mainGo)
	}
}

func renderModuleMain(t *testing.T, data generator.Data) string {
	t.Helper()
	eng := &generator.Engine{}
	files, err := eng.RenderTree(FS, "module", data, "/out")
	if err != nil {
		t.Fatalf("RenderTree module: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(filepath.ToSlash(f.Path), "cmd/news/main.go") {
			return string(f.Content)
		}
	}
	t.Fatal("cmd/news/main.go not rendered")
	return ""
}
