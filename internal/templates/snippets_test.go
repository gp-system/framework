package templates

import (
	"strings"
	"testing"

	"github.com/gp-system/framework/internal/generator"
)

func renderSnippet(t *testing.T, name string, data generator.Data) string {
	t.Helper()
	raw, err := Snippet(name)
	if err != nil {
		t.Fatal(err)
	}
	out, err := generator.RenderSnippet(raw, data)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return out
}

func newsData() generator.Data {
	return generator.Data{
		Project:  "shop",
		GoModule: "example.com/shop",
		Module:   "news",
		Surface:  "api",
		Kit:      generator.ModuleRef{Module: generator.Kit},
	}
}

// TestTspOperationAuthzDecorators pins the @permission / @policy emission of
// `add handler --permission --policy` and the widened response union.
func TestTspOperationAuthzDecorators(t *testing.T) {
	data := newsData()
	data.Operation = "updateNews"
	data.Method = "PATCH"
	data.HTTPPath = "/items/{id}"
	data.PathParams = []string{"id"}

	t.Run("untagged", func(t *testing.T) {
		out := renderSnippet(t, "tsp-operation", data)
		if strings.Contains(out, "@permission") || strings.Contains(out, "@policy") {
			t.Errorf("untagged operation emits authz decorators:\n%s", out)
		}
		if strings.Contains(out, "Forbidden") {
			t.Errorf("untagged operation declares Forbidden response:\n%s", out)
		}
	})

	t.Run("tagged", func(t *testing.T) {
		tagged := data
		tagged.Permission = "news.write"
		tagged.Policy = "news.owner"
		out := renderSnippet(t, "tsp-operation", tagged)
		for _, want := range []string{
			`@permission("news.write")`,
			`@policy("news.owner")`,
			"| Unauthorized | Forbidden;",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("tagged operation lacks %q:\n%s", want, out)
			}
		}
	})
}

// TestRegisterSurfaceWiresPolicyEnforcer pins the strict-middleware wiring
// the chi register snippet emits.
func TestRegisterSurfaceWiresPolicyEnforcer(t *testing.T) {
	t.Run("chi", func(t *testing.T) {
		out := renderSnippet(t, "register-surface", newsData())
		for _, want := range []string{
			"func RegisterApi(router chi.Router, deps Dependencies) {",
			"apiPolicies := apipolicy.New()",
			"server.StrictValidator[newsapigen.StrictHandlerFunc](nil)",
			"kitpolicy.Enforcer[newsapigen.StrictHandlerFunc](apiPolicies, newsapigen.PermissionByOperation, newsapigen.PolicyByOperation)",
			"ResponseErrorHandlerFunc: server.WriteError",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("chi register snippet lacks %q:\n%s", want, out)
			}
		}
		// The enforcer must come after the validator: last = outermost, so
		// authorization runs before validation.
		if strings.Index(out, "StrictValidator") > strings.Index(out, "Enforcer") {
			t.Error("enforcer wired before validator: authorization must wrap outermost")
		}
	})

	t.Run("imports include router package for chi", func(t *testing.T) {
		out := renderSnippet(t, "register-imports", newsData())
		for _, want := range []string{
			`"github.com/go-chi/chi/v5"`,
			`apipolicy "example.com/shop/internal/modules/news/surfaces/api/policy"`,
			`kitpolicy "github.com/gp-system/auth/policy"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("chi import snippet lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "gofiber") {
			t.Errorf("chi import snippet pulls in fiber:\n%s", out)
		}
	})
}

// TestSnippetsHaveNoExplanatoryComments guards the "no comments in generated
// output" rule for the register snippet: the only comments it may emit are
// the chi middleware-ordering note (correctness-load-bearing) and
// gpsystem:* anchor markers (machinery, not explanation).
func TestSnippetsHaveNoExplanatoryComments(t *testing.T) {
	out := renderSnippet(t, "register-surface", newsData())
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "// Last = outermost") {
			continue // the one allowed correctness comment
		}
		if strings.HasPrefix(trimmed, "// gpsystem:") {
			continue // anchor markers are generator machinery
		}
		t.Errorf("register-surface emits an explanatory comment: %q", trimmed)
	}
}

// TestComposeModuleSnippet pins the module app-service backfill inserted by
// `new module` on an older project: dual Traefik routers, the PathRegexp
// module-routing rule and the development-target build.
func TestComposeModuleSnippet(t *testing.T) {
	out := renderSnippet(t, "compose-module", newsData())
	for _, want := range []string{
		"news:",
		"target: development",
		"traefik.http.routers.shopNews.rule=Host(`${APP_HOST}`) && PathRegexp(`^/api/v1/([a-z0-9-]+/)?news(/|$)`)",
		"traefik.http.routers.shopNewsSsl.entrypoints=websecure",
		"traefik.http.routers.shopNewsSsl.tls.certresolver=letsEncrypt",
		"traefik.http.services.shopNews.loadbalancer.server.port=3000",
		"- proxynet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compose-module missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "additional_contexts") {
		t.Errorf("compose-module (no kit-replace) unexpectedly has additional_contexts:\n%s", out)
	}

	data := newsData()
	data.Kit.Replace = "../framework"
	data.Errs.Replace = "../errs"
	withKit := renderSnippet(t, "compose-module", data)
	for _, want := range []string{
		"additional_contexts:",
		"kit: ../framework",
		"../framework:/framework:ro",
		"errs: ../errs",
		"../errs:/errs:ro",
	} {
		if !strings.Contains(withKit, want) {
			t.Errorf("compose-module (kit-replace) missing %q:\n%s", want, withKit)
		}
	}
}

// TestComposeRealtimeSnippet pins the `add realtime` compose backfill: the
// kit-replace mount must land at the in-container /framework path
// (generator.ModuleRef.Mount), not literally re-mount the host checkout path
// inside the container -- a real bug found only when actually docker
// compose up'ing a kit-replace project (go build failed with "replacement
// directory ../../framework does not exist" because .Kit.Replace was reused
// as both sides of the volume mount).
func TestComposeRealtimeSnippet(t *testing.T) {
	out := renderSnippet(t, "compose-realtime", newsData())
	for _, want := range []string{
		"realtime:",
		"target: development",
		"traefik.http.routers.shopRealtime.rule=Host(`${APP_HOST}`) && PathPrefix(`/realtime`)",
		"- proxynet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compose-realtime missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "additional_contexts") {
		t.Errorf("compose-realtime (no kit-replace) unexpectedly has additional_contexts:\n%s", out)
	}

	data := newsData()
	data.Kit.Replace = "../framework"
	data.Errs.Replace = "../errs"
	withKit := renderSnippet(t, "compose-realtime", data)
	for _, want := range []string{
		"additional_contexts:",
		"kit: ../framework",
		"../framework:/framework:ro",
		"errs: ../errs",
		"../errs:/errs:ro",
	} {
		if !strings.Contains(withKit, want) {
			t.Errorf("compose-realtime (kit-replace) missing %q:\n%s", want, withKit)
		}
	}
	if strings.Contains(withKit, "../framework:../framework:ro") {
		t.Errorf("compose-realtime (kit-replace) mounts the kit at its host path instead of /framework:\n%s", withKit)
	}
	if strings.Contains(withKit, "../errs:../errs:ro") {
		t.Errorf("compose-realtime (kit-replace) mounts errs at its host path instead of /errs:\n%s", withKit)
	}
}

// TestEnvStackSnippet pins the .env.example backfill's host variables.
func TestEnvStackSnippet(t *testing.T) {
	out := renderSnippet(t, "env-stack", newsData())
	for _, want := range []string{
		"APP_HOST=shop.localhost",
		"MAILPIT_HOST=mailpit.shop.localhost",
		"RUSTFS_S3_HOST=rustfs.shop.localhost",
		"RUSTFS_CONSOLE_HOST=rustfs-console.shop.localhost",
		"USER_ID=1000",
		"GROUP_ID=1000",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("env-stack missing %q:\n%s", want, out)
		}
	}
}

// TestComposeSnippetsMatchProjectCompose pins the intentional duplication
// between the backfill snippets and the base compose.yml.tmpl (the same
// pattern compose-valkey already follows): a normalized backfill snippet must
// appear verbatim inside the freshly generated compose.yml, so the two never
// silently drift apart.
func TestComposeSnippetsMatchProjectCompose(t *testing.T) {
	eng := &generator.Engine{}
	data := newsData()
	files, err := eng.RenderTree(FS, "project", generator.Data{
		Project:  "shop",
		GoModule: "example.com/shop",
		DB:       "pgx",
		Kit:      generator.ModuleRef{Module: generator.Kit},
	}, "/out")
	if err != nil {
		t.Fatalf("RenderTree project: %v", err)
	}
	var compose string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "compose.yml") {
			compose = string(f.Content)
		}
	}
	if compose == "" {
		t.Fatal("compose.yml not rendered")
	}

	for _, snippet := range []string{"compose-mailpit", "compose-rustfs", "compose-rustfs-init"} {
		out := renderSnippet(t, snippet, data)
		for _, line := range strings.Split(strings.Trim(out, "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if !strings.Contains(compose, strings.TrimSpace(line)) {
				t.Errorf("%s line %q not found in generated compose.yml", snippet, strings.TrimSpace(line))
			}
		}
	}
}

// TestMainRegisterCall pins the entry-point registration line inserted at the
// gpsystem:registrations anchor, including the Pascal-cased function name.
func TestMainRegisterCall(t *testing.T) {
	out := renderSnippet(t, "main-register-call", newsData())
	if want := "newssurfaces.RegisterApi(api, deps)"; strings.TrimSpace(out) != want {
		t.Errorf("main-register-call = %q, want %q", strings.TrimSpace(out), want)
	}

	data := newsData()
	data.Surface = "backoffice"
	out = renderSnippet(t, "main-register-call", data)
	if want := "newssurfaces.RegisterBackoffice(api, deps)"; strings.TrimSpace(out) != want {
		t.Errorf("main-register-call = %q, want %q", strings.TrimSpace(out), want)
	}
}

// TestApiAuthMiddlewareSnippet pins what `add auth` inserts at the
// gpsystem:api-middleware anchor of every module entry point.
func TestApiAuthMiddlewareSnippet(t *testing.T) {
	out := renderSnippet(t, "api-auth-middleware", newsData())
	if strings.TrimSpace(out) != "api.Use(server.IdentityMiddleware(cfg.JWT))" {
		t.Fatalf("unexpected snippet output:\n%s", out)
	}
}

// TestRegisterSurfaceHasMiddlewareAnchor pins the per-surface seam for
// route-group middleware (r.Use(server.RequireRole(...)) and friends)
// inside the Route closure, in both register snippet variants.
func TestRegisterSurfaceHasMiddlewareAnchor(t *testing.T) {
	for _, snippet := range []string{"register-surface", "auth-register-surface"} {
		out := renderSnippet(t, snippet, newsData())
		routeAt := strings.Index(out, "router.Route(")
		anchorAt := strings.Index(out, "// gpsystem:surface-middleware")
		if anchorAt < 0 {
			t.Errorf("%s lacks the gpsystem:surface-middleware anchor", snippet)
			continue
		}
		if routeAt < 0 || anchorAt < routeAt {
			t.Errorf("%s: anchor is not inside the Route closure", snippet)
		}
	}
}
