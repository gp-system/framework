package templates

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gp-system/framework/internal/generator"
)

func workerData(db string) generator.Data {
	return generator.Data{
		Project:     "shop",
		GoModule:    "example.com/shop",
		Module:      "news",
		EventModule: "news",
		Event:       "articlePublished",
		Listener:    "sendNewsletter",
		Job:         "cleanupStale",
		Cron:        "0 3 * * *",
		DB:          db,
		Kit:         generator.ModuleRef{Module: generator.Kit},
	}
}

// TestWorkerTreesRenderWithoutComments renders every worker-related tree for
// both DB stacks. RenderTree runs goimports, so invalid Go fails the render;
// the comment check pins the kit's no-explanatory-comment rule for generated
// code (only //go: directives and gpsystem anchors survive).
func TestWorkerTreesRenderWithoutComments(t *testing.T) {
	trees := []string{"worker", "moduleworker", "event", "listener", "job"}
	for _, db := range []string{"pgx", "bun"} {
		t.Run(db, func(t *testing.T) {
			for _, tree := range trees {
				eng := &generator.Engine{}
				files, err := eng.RenderTree(FS, tree, workerData(db), "/out")
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

// TestWorkerMainHasDBAnchors pins the two anchors `add db`/`add worker`
// depend on to wire a named connection's pool and closer into the worker
// entry point. Their absence would make `add db` fail its preflight check
// against a freshly generated project.
func TestWorkerMainHasDBAnchors(t *testing.T) {
	for _, db := range []string{"pgx", "bun"} {
		eng := &generator.Engine{}
		files, err := eng.RenderTree(FS, "worker", workerData(db), "/out")
		if err != nil {
			t.Fatalf("RenderTree worker (%s): %v", db, err)
		}
		var mainGo string
		for _, f := range files {
			if strings.HasSuffix(filepath.ToSlash(f.Path), "cmd/worker/main.go") {
				mainGo = string(f.Content)
			}
		}
		if mainGo == "" {
			t.Fatalf("cmd/worker/main.go not rendered (%s)", db)
		}
		for _, anchor := range []string{"gpsystem:pools", "gpsystem:closers"} {
			if !strings.Contains(mainGo, anchor) {
				t.Errorf("cmd/worker/main.go (%s) missing anchor %q", db, anchor)
			}
		}
	}
}

// TestWorkerModuleSnippet pins the per-module worker wiring for both stacks:
// the deps struct, the module-scoped deps-init anchor and the register calls.
func TestWorkerModuleSnippet(t *testing.T) {
	t.Run("pgx", func(t *testing.T) {
		out := renderSnippet(t, "worker-module", workerData("pgx"))
		for _, want := range []string{
			"newsDeps := newssurfaces.Dependencies{",
			"DB:         pg.NewDB(pool),",
			"Transactor: pg.NewTransactor(pool),",
			"// gpsystem:deps-init(news)",
			"newslisteners.Register(reg, newsDeps)",
			"newsjobs.Register(sched, newsDeps)",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("worker-module (pgx) missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("bun", func(t *testing.T) {
		out := renderSnippet(t, "worker-module", workerData("bun"))
		for _, want := range []string{
			"DB:         bunDB,",
			"Transactor: bunx.NewTransactor(bunDB),",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("worker-module (bun) missing %q:\n%s", want, out)
			}
		}
	})
}

// TestWorkerModuleImportSnippets pin the aliased subpackage imports the
// worker entry point needs for the per-module Register calls.
func TestWorkerModuleImportSnippets(t *testing.T) {
	data := workerData("pgx")
	jobs := renderSnippet(t, "worker-module-jobs-import", data)
	if !strings.Contains(jobs, `newsjobs "example.com/shop/internal/modules/news/jobs"`) {
		t.Errorf("worker-module-jobs-import:\n%s", jobs)
	}
	listeners := renderSnippet(t, "worker-module-listeners-import", data)
	if !strings.Contains(listeners, `newslisteners "example.com/shop/internal/modules/news/listeners"`) {
		t.Errorf("worker-module-listeners-import:\n%s", listeners)
	}
	deps := renderSnippet(t, "worker-module-imports", data)
	if !strings.Contains(deps, `newssurfaces "example.com/shop/internal/modules/news/surfaces"`) {
		t.Errorf("worker-module-imports:\n%s", deps)
	}
}

// TestDispatcherInitSnippet pins the outbox dispatcher construction per stack.
func TestDispatcherInitSnippet(t *testing.T) {
	pgx := renderSnippet(t, "dispatcher-init", workerData("pgx"))
	if !strings.Contains(pgx, "outbox.NewDispatcher(outbox.NewStore(pg.NewDB(pool)))") {
		t.Errorf("dispatcher-init (pgx):\n%s", pgx)
	}
	bun := renderSnippet(t, "dispatcher-init", workerData("bun"))
	if !strings.Contains(bun, "outbox.NewDispatcher(outboxbunx.NewStore(bunDB))") {
		t.Errorf("dispatcher-init (bun):\n%s", bun)
	}
}

// TestListenerAndJobRegisterSnippets pin the registration lines.
func TestListenerAndJobRegisterSnippets(t *testing.T) {
	data := workerData("pgx")
	data.Queue = "newsletters"
	ln := renderSnippet(t, "listener-register", data)
	if !strings.Contains(ln, `events.Listen(reg, "sendNewsletter", sendNewsletter(deps), queue.OnQueue("newsletters"))`) {
		t.Errorf("listener-register with queue:\n%s", ln)
	}
	noQ := workerData("pgx")
	lnNoQ := renderSnippet(t, "listener-register", noQ)
	if strings.Contains(lnNoQ, "queue.OnQueue") {
		t.Errorf("listener-register without queue should not reference queue.OnQueue:\n%s", lnNoQ)
	}
	job := renderSnippet(t, "job-register", data)
	if !strings.Contains(job, `sched.Job("news.cleanupStale", "0 3 * * *", cleanupStale(deps))`) {
		t.Errorf("job-register:\n%s", job)
	}
}
