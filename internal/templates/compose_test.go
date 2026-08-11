package templates

import (
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gp-system/framework/internal/generator"
)

func projectData(kitReplace string) generator.Data {
	data := generator.Data{
		Project:  "shop",
		GoModule: "example.com/shop",
		DB:       "pgx",
		Kit:      generator.ModuleRef{Module: generator.Kit, Replace: kitReplace},
		Errs:     generator.ModuleRef{Module: generator.Errs.Pin("v0.0.0")},
	}
	// new project derives the errs checkout as a sibling of the kit checkout.
	if kitReplace != "" {
		data.Errs.Replace = path.Join(path.Dir(kitReplace), "errs")
	}
	return data
}

func renderProjectFile(t *testing.T, data generator.Data, suffix string) string {
	t.Helper()
	eng := &generator.Engine{}
	files, err := eng.RenderTree(FS, "project", data, "/out")
	if err != nil {
		t.Fatalf("RenderTree project: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(filepath.ToSlash(f.Path), suffix) {
			return string(f.Content)
		}
	}
	t.Fatalf("no rendered file with suffix %q", suffix)
	return ""
}

// TestGoModRequiresAndReplaces pins the generated go.mod: the project
// requires both the kit and the standalone errs module, and a --kit-replace
// project carries a replace directive for each (errs is developed in a
// sibling checkout of the kit).
func TestGoModRequiresAndReplaces(t *testing.T) {
	t.Run("with kit-replace", func(t *testing.T) {
		out := renderProjectFile(t, projectData("../framework"), "go.mod")
		for _, want := range []string{
			"github.com/gp-system/errs v0.0.0",
			"github.com/gp-system/framework v0.0.0",
			"tool (",
			"github.com/gp-system/framework/cmd/gpsystem",
			"replace github.com/gp-system/framework => ../framework",
			"replace github.com/gp-system/errs => ../errs",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("go.mod (kit-replace) missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("without kit-replace", func(t *testing.T) {
		out := renderProjectFile(t, projectData(""), "go.mod")
		if !strings.Contains(out, "github.com/gp-system/errs v0.0.0") {
			t.Errorf("go.mod missing the errs require:\n%s", out)
		}
		if strings.Contains(out, "replace") {
			t.Errorf("go.mod (no kit-replace) unexpectedly has a replace directive:\n%s", out)
		}
	})
}

// TestComposeHasNoPublishedPorts pins the "zero published ports, everything
// through Traefik on proxynet" design: a "ports:" block anywhere in
// compose.yml would mean a backing service leaked onto the host network.
func TestComposeHasNoPublishedPorts(t *testing.T) {
	for _, kitReplace := range []string{"", "../framework"} {
		out := renderProjectFile(t, projectData(kitReplace), "compose.yml")
		if strings.Contains(out, "ports:") {
			t.Errorf("compose.yml (kitReplace=%q) publishes a port:\n%s", kitReplace, out)
		}
	}
}

func TestComposeBackingServices(t *testing.T) {
	out := renderProjectFile(t, projectData(""), "compose.yml")
	for _, want := range []string{
		"POSTGRES_USER: ${DB_USER}",
		"POSTGRES_PASSWORD: ${DB_PASSWORD}",
		"POSTGRES_DB: ${DB_NAME}",
		"pg_isready -U ${DB_USER} -d ${DB_NAME}",
		"# gpsystem:services",
		"proxynet:",
		"external: true",
		"rustfsdata:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compose.yml missing %q:\n%s", want, out)
		}
	}
}

// TestComposeAppServicesUseDevelopmentTarget pins that migrate/worker build
// from the project's own Dockerfile at the development stage (bind-mounted
// source, no rebuild on restart) rather than a prebuilt/production image.
func TestComposeAppServicesUseDevelopmentTarget(t *testing.T) {
	out := renderProjectFile(t, projectData(""), "compose.yml")
	for _, want := range []string{
		"target: development",
		".:/src",
		"gomodcache:/gomodcache",
		"gocache:/gocache",
		`command: ["sh", "-c", "exec go run ./cmd/migrate up"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compose.yml missing %q:\n%s", want, out)
		}
	}
}

// TestComposeKitReplaceWiring pins the two kit-replace mechanisms: a runtime
// bind-mount for the development target, additional_contexts for build. Both
// must be entirely absent for a non-replace (published-kit) project.
func TestComposeKitReplaceWiring(t *testing.T) {
	t.Run("with kit-replace", func(t *testing.T) {
		out := renderProjectFile(t, projectData("../framework"), "compose.yml")
		for _, want := range []string{
			"additional_contexts:",
			"kit: ../framework",
			"../framework:/framework:ro",
			"errs: ../errs",
			"../errs:/errs:ro",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("compose.yml (kit-replace) missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("without kit-replace", func(t *testing.T) {
		out := renderProjectFile(t, projectData(""), "compose.yml")
		for _, unwanted := range []string{"additional_contexts:", "gp-system/framework:", "errs:"} {
			if strings.Contains(out, unwanted) {
				t.Errorf("compose.yml (no kit-replace) unexpectedly has %q:\n%s", unwanted, out)
			}
		}
	})
}

// TestComposeTraefikRouters pins the mailpit/rustfs Traefik wiring: dual
// (web/websecure) routers with the letsEncrypt resolver, external proxynet.
func TestComposeTraefikRouters(t *testing.T) {
	out := renderProjectFile(t, projectData(""), "compose.yml")
	for _, want := range []string{
		"traefik.http.routers.shopMailpit.rule=Host(`${MAILPIT_HOST}`)",
		"traefik.http.routers.shopMailpitSsl.entrypoints=websecure",
		"traefik.http.routers.shopMailpitSsl.tls.certresolver=letsEncrypt",
		"traefik.http.routers.shopRustfs.rule=Host(`${RUSTFS_S3_HOST}`)",
		"traefik.http.routers.shopRustfsConsole.rule=Host(`${RUSTFS_CONSOLE_HOST}`)",
		"traefik.http.routers.shopRustfsConsoleSsl.tls.certresolver=letsEncrypt",
		"- proxynet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compose.yml missing %q:\n%s", want, out)
		}
	}
}

// TestDockerfileStages pins the four-stage Dockerfile: development runs as a
// host-matched user via go run (no rebuild needed on restart), build embeds
// the kit only when kit-replace is set, production is a minimal runtime.
func TestDockerfileStages(t *testing.T) {
	t.Run("with kit-replace", func(t *testing.T) {
		out := renderProjectFile(t, projectData("../framework"), "Dockerfile")
		for _, want := range []string{
			"FROM golang:1.25-alpine AS base",
			"FROM base AS development",
			"ARG USER_ID=1000",
			"ARG GROUP_ID=1000",
			"USER app",
			`CMD ["sh", "-c", "exec go run ./cmd/${CMD}"]`,
			"FROM base AS build",
			"COPY --from=kit / /framework",
			"COPY --from=errs / /errs",
			"ARG CMD",
			"./cmd/${CMD}",
			"FROM alpine:3.22 AS production",
			"USER nobody",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("Dockerfile (kit-replace) missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("without kit-replace", func(t *testing.T) {
		out := renderProjectFile(t, projectData(""), "Dockerfile")
		for _, unwanted := range []string{"COPY --from=kit", "COPY --from=errs"} {
			if strings.Contains(out, unwanted) {
				t.Errorf("Dockerfile (no kit-replace) unexpectedly has %q:\n%s", unwanted, out)
			}
		}
	})
}

// TestEnvExampleStackVariables pins the compose-service-name defaults and the
// new host/stack variables .env.example must ship for the stack to boot.
func TestEnvExampleStackVariables(t *testing.T) {
	out := renderProjectFile(t, projectData(""), ".env.example")
	for _, want := range []string{
		"DB_HOST=postgres",
		"VALKEY_ADDR=valkey:6379",
		"APP_HOST=shop.localhost",
		"MAILPIT_HOST=mailpit.shop.localhost",
		"RUSTFS_S3_HOST=rustfs.shop.localhost",
		"RUSTFS_CONSOLE_HOST=rustfs-console.shop.localhost",
		"MAIL_HOST=mailpit",
		"S3_ENDPOINT=http://rustfs:9000",
		"USER_ID=1000",
		"GROUP_ID=1000",
		"# gpsystem:env",
	} {
		if !strings.Contains(out, want) {
			t.Errorf(".env.example missing %q:\n%s", want, out)
		}
	}
}
