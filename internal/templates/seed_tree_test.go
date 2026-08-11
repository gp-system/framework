package templates

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gp-system/framework/internal/generator"
)

func seedData(db string) generator.Data {
	return generator.Data{
		Project:  "shop",
		GoModule: "example.com/shop",
		DB:       db,
		Seed:     "adminUser",
		Kit:      generator.ModuleRef{Module: generator.Kit},
	}
}

// TestSeedTreesRenderWithoutComments renders the project (for seeds/register.go
// and cmd/migrate/main.go) and seedfile trees for both DB stacks. RenderTree
// runs goimports, so invalid Go fails the render; the comment check pins the
// kit's no-explanatory-comment rule for generated code.
func TestSeedTreesRenderWithoutComments(t *testing.T) {
	trees := []string{"project", "seedfile"}
	for _, db := range []string{"pgx", "bun"} {
		t.Run(db, func(t *testing.T) {
			for _, tree := range trees {
				eng := &generator.Engine{}
				files, err := eng.RenderTree(FS, tree, seedData(db), "/out")
				if err != nil {
					t.Fatalf("RenderTree %s (%s): %v", tree, db, err)
				}
				for _, f := range files {
					if !strings.HasSuffix(f.Path, ".go") {
						continue
					}
					for _, line := range strings.Split(string(f.Content), "\n") {
						trimmed := strings.TrimSpace(line)
						if !strings.HasPrefix(trimmed, "//") {
							continue
						}
						switch {
						case strings.HasPrefix(trimmed, "//go:"):
						case strings.Contains(trimmed, "gpsystem:"):
						default:
							t.Errorf("%s (%s) carries an explanatory comment: %q", filepath.Base(f.Path), tree, trimmed)
						}
					}
				}
			}
		})
	}
}

// TestSeedsRegisterHasAnchors pins the two anchors `add seeder` depends on to
// wire an import and a registration call into the project's seeds/register.go.
func TestSeedsRegisterHasAnchors(t *testing.T) {
	for _, db := range []string{"pgx", "bun"} {
		eng := &generator.Engine{}
		files, err := eng.RenderTree(FS, "project", seedData(db), "/out")
		if err != nil {
			t.Fatalf("RenderTree project (%s): %v", db, err)
		}
		var registerGo, migrateGo string
		for _, f := range files {
			switch filepath.ToSlash(f.Path) {
			case "/out/seeds/register.go":
				registerGo = string(f.Content)
			case "/out/cmd/migrate/main.go":
				migrateGo = string(f.Content)
			}
		}
		if registerGo == "" {
			t.Fatalf("seeds/register.go not rendered (%s)", db)
		}
		for _, anchor := range []string{"gpsystem:seed-imports", "gpsystem:seeds"} {
			if !strings.Contains(registerGo, anchor) {
				t.Errorf("seeds/register.go (%s) missing anchor %q", db, anchor)
			}
		}
		if migrateGo == "" {
			t.Fatalf("cmd/migrate/main.go not rendered (%s)", db)
		}
		for _, want := range []string{`command == "seed"`, "--seed", "seeds.Register(reg, deps)", "reg.Run(ctx)"} {
			if !strings.Contains(migrateGo, want) {
				t.Errorf("cmd/migrate/main.go (%s) missing %q", db, want)
			}
		}
	}
}

// TestSeedRegisterSnippet pins the registration call `add seeder` inserts at
// the gpsystem:seeds anchor.
func TestSeedRegisterSnippet(t *testing.T) {
	out := renderSnippet(t, "seed-register", seedData("pgx"))
	if !strings.Contains(out, "reg.Add(adminUser(deps))") {
		t.Errorf("seed-register:\n%s", out)
	}
}

// TestSeedFileNamesFunctionAndName pins the seeder stub's function name and
// its Seeder.Name. The stub is DB-agnostic (it does not reference bunx or
// pg.DB directly), so it renders identically for both stacks.
func TestSeedFileNamesFunctionAndName(t *testing.T) {
	for _, db := range []string{"pgx", "bun"} {
		eng := &generator.Engine{}
		files, err := eng.RenderTree(FS, "seedfile", seedData(db), "/out")
		if err != nil {
			t.Fatalf("RenderTree seedfile (%s): %v", db, err)
		}
		var got string
		for _, f := range files {
			if filepath.ToSlash(f.Path) == "/out/seeds/admin_user.go" {
				got = string(f.Content)
			}
		}
		if got == "" {
			t.Fatalf("seeds/admin_user.go not rendered (%s)", db)
		}
		if !strings.Contains(got, "func adminUser(deps Deps) seed.Seeder {") {
			t.Errorf("seedfile (%s) missing function signature:\n%s", db, got)
		}
		if !strings.Contains(got, `Name: "admin_user",`) {
			t.Errorf("seedfile (%s) missing seeder name:\n%s", db, got)
		}
	}
}
