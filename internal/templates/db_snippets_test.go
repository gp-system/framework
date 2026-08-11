package templates

import (
	"strings"
	"testing"

	"github.com/gp-system/framework/internal/generator"
)

// dbConnData builds the template context `add db`/`add domain --db` use for
// a named connection "analytics" on module "news".
func dbConnData(connector string) generator.Data {
	d := newsData()
	d.DBConn = "analytics"
	d.DBConnector = connector
	return d
}

// TestDBConfigSnippet pins the config field a named connection adds: its Go
// identifier and its DB_<NAME>_ env prefix.
func TestDBConfigSnippet(t *testing.T) {
	out := renderSnippet(t, "db-config", dbConnData("pgx"))
	want := "DBAnalytics dbx.Config `envPrefix:\"DB_ANALYTICS_\"`"
	if !strings.Contains(out, want) {
		t.Errorf("db-config missing %q:\n%s", want, out)
	}
}

// TestDBEnvSnippet pins the .env.example block a named connection adds.
func TestDBEnvSnippet(t *testing.T) {
	out := renderSnippet(t, "db-env", dbConnData("pgx"))
	for _, want := range []string{
		"DB_ANALYTICS_HOST=localhost",
		"DB_ANALYTICS_PORT=5432",
		"DB_ANALYTICS_USER=shop",
		"DB_ANALYTICS_PASSWORD=shop",
		"DB_ANALYTICS_NAME=shop",
		"DB_ANALYTICS_SSLMODE=disable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("db-env missing %q:\n%s", want, out)
		}
	}
}

// TestDBPoolSnippet pins the pool (and, for bun, the bun.DB) construction
// for a named connection, per connector.
func TestDBPoolSnippet(t *testing.T) {
	t.Run("pgx", func(t *testing.T) {
		out := renderSnippet(t, "db-pool", dbConnData("pgx"))
		if !strings.Contains(out, "analyticsPool := pg.MustNewPool(ctx, cfg.DBAnalytics)") {
			t.Errorf("db-pool (pgx):\n%s", out)
		}
		if strings.Contains(out, "bunx.Open") {
			t.Errorf("db-pool (pgx) should not open a bun handle:\n%s", out)
		}
	})
	t.Run("bun", func(t *testing.T) {
		out := renderSnippet(t, "db-pool", dbConnData("bun"))
		for _, want := range []string{
			"analyticsPool := pg.MustNewPool(ctx, cfg.DBAnalytics)",
			"analyticsBun := bunx.Open(analyticsPool)",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("db-pool (bun) missing %q:\n%s", want, out)
			}
		}
	})
}

// TestDBDepsInitSnippet pins the Dependencies field initializers for a named
// connection, per connector. The same snippet targets both the module main
// entry point (gpsystem:dependencies-init) and the worker's per-module
// deps-init anchor.
func TestDBDepsInitSnippet(t *testing.T) {
	t.Run("pgx", func(t *testing.T) {
		out := renderSnippet(t, "db-deps-init", dbConnData("pgx"))
		for _, want := range []string{
			"AnalyticsDB: pg.NewDB(analyticsPool),",
			"AnalyticsTransactor: pg.NewTransactor(analyticsPool),",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("db-deps-init (pgx) missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("bun", func(t *testing.T) {
		out := renderSnippet(t, "db-deps-init", dbConnData("bun"))
		for _, want := range []string{
			"AnalyticsDB: analyticsBun,",
			"AnalyticsTransactor: bunx.NewTransactor(analyticsBun),",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("db-deps-init (bun) missing %q:\n%s", want, out)
			}
		}
	})
}

// TestDBCloserSnippet pins the closer registration for a named connection,
// per connector, and the LIFO-correct ordering: the pool closer is
// registered before the bun closer, so bun closes before its own pool.
func TestDBCloserSnippet(t *testing.T) {
	for _, connector := range []string{"pgx", "bun"} {
		t.Run(connector, func(t *testing.T) {
			out := renderSnippet(t, "db-closer", dbConnData(connector))
			poolCloser := `server.WithCloser("analytics-pgxpool"`
			if !strings.Contains(out, poolCloser) {
				t.Errorf("db-closer missing %q:\n%s", poolCloser, out)
			}
			if connector == "bun" {
				bunCloser := `server.WithCloser("analytics-bun"`
				if !strings.Contains(out, bunCloser) {
					t.Errorf("db-closer missing %q:\n%s", bunCloser, out)
				}
				if strings.Index(out, poolCloser) > strings.Index(out, bunCloser) {
					t.Errorf("pool closer must be registered before the bun closer (LIFO closes bun first):\n%s", out)
				}
			} else if strings.Contains(out, "-bun\"") {
				t.Errorf("db-closer (pgx) should not register a bun closer:\n%s", out)
			}
		})
	}
}

// TestDBCloserWorkerSnippet mirrors TestDBCloserSnippet for the worker's
// closer wiring (worker.WithCloser, no engine branch).
func TestDBCloserWorkerSnippet(t *testing.T) {
	t.Run("pgx", func(t *testing.T) {
		out := renderSnippet(t, "db-closer-worker", dbConnData("pgx"))
		if !strings.Contains(out, `worker.WithCloser("analytics-pgxpool"`) {
			t.Errorf("db-closer-worker (pgx):\n%s", out)
		}
		if strings.Contains(out, "-bun\"") {
			t.Errorf("db-closer-worker (pgx) should not register a bun closer:\n%s", out)
		}
	})
	t.Run("bun", func(t *testing.T) {
		out := renderSnippet(t, "db-closer-worker", dbConnData("bun"))
		poolCloser := `worker.WithCloser("analytics-pgxpool"`
		bunCloser := `worker.WithCloser("analytics-bun"`
		if !strings.Contains(out, poolCloser) || !strings.Contains(out, bunCloser) {
			t.Errorf("db-closer-worker (bun) missing a closer:\n%s", out)
		}
		if strings.Index(out, poolCloser) > strings.Index(out, bunCloser) {
			t.Errorf("pool closer must be registered before the bun closer (LIFO closes bun first):\n%s", out)
		}
	})
}

// TestDBDepsSnippet pins the register.go Dependencies fields for a named
// connection, per connector.
func TestDBDepsSnippet(t *testing.T) {
	t.Run("pgx", func(t *testing.T) {
		out := renderSnippet(t, "db-deps", dbConnData("pgx"))
		for _, want := range []string{"AnalyticsDB *pg.DB", "AnalyticsTransactor dbx.Transactor"} {
			if !strings.Contains(out, want) {
				t.Errorf("db-deps (pgx) missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("bun", func(t *testing.T) {
		out := renderSnippet(t, "db-deps", dbConnData("bun"))
		if !strings.Contains(out, "AnalyticsDB *bun.DB") {
			t.Errorf("db-deps (bun):\n%s", out)
		}
	})
}

// TestDBDepsImportsAndMainImportsSnippets pin the per-connector import lines
// inserted alongside db-deps (register.go) and db-pool (main.go/worker main).
func TestDBDepsImportsAndMainImportsSnippets(t *testing.T) {
	if out := renderSnippet(t, "db-deps-imports", dbConnData("pgx")); !strings.Contains(out, `"github.com/gp-system/dbx/pg"`) {
		t.Errorf("db-deps-imports (pgx):\n%s", out)
	}
	if out := renderSnippet(t, "db-deps-imports", dbConnData("bun")); !strings.Contains(out, `"github.com/uptrace/bun"`) {
		t.Errorf("db-deps-imports (bun):\n%s", out)
	}
	if out := renderSnippet(t, "db-main-imports", dbConnData("bun")); !strings.Contains(out, `"github.com/gp-system/dbx/bunx"`) {
		t.Errorf("db-main-imports:\n%s", out)
	}
}

// TestDBSnippetsNoExplanatoryComments pins the kit's no-explanatory-comment
// rule for the db-* snippets across both connectors.
func TestDBSnippetsNoExplanatoryComments(t *testing.T) {
	names := []string{
		"db-config", "db-env", "db-main-imports", "db-pool", "db-deps-init",
		"db-closer", "db-closer-worker", "db-deps", "db-deps-imports",
	}
	for _, connector := range []string{"pgx", "bun"} {
		for _, name := range names {
			out := renderSnippet(t, name, dbConnData(connector))
			for _, line := range strings.Split(out, "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, "//") {
					continue
				}
				if !strings.HasPrefix(trimmed, "//go:") && !strings.Contains(trimmed, "gpsystem:") {
					t.Errorf("%s (%s) carries an explanatory comment: %q", name, connector, trimmed)
				}
			}
		}
	}
}
