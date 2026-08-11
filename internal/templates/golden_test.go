package templates

import (
	"strings"
	"testing"
)

// These fixtures pin the exact go.mod/Dockerfile/compose.yml byte output for
// a kit+errs, --kit-replace project (Mods empty) -- the only configuration
// that exists before any package is extracted from the kit. They were
// captured by rendering with the pre-registry Data.KitModule/KitReplace/...
// fields (see the "refactor(generator): introduce a module registry" commit
// history) and must stay byte-identical now that Data.Kit/Data.Errs/Data.Mods
// generalize them: this is the safety net proving the registry refactor does
// not change what a generated project looks like today.
const (
	goldenGoMod = `module example.com/shop

go 1.25

require (
	github.com/gp-system/errs v0.0.0
	github.com/gp-system/framework v0.0.0
)

tool (
	github.com/gp-system/framework/cmd/gpsystem
	github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
)

replace github.com/gp-system/framework => ../framework
replace github.com/gp-system/errs => ../errs
`

	goldenDockerfile = `# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS base
WORKDIR /src

# Dev: the source (and kit-replace checkout) is bind-mounted in by compose;
# after a code change ` + "`docker compose restart <service>`" + ` is enough: go run
# recompiles from the mounted source. No source copy, no module download at
# build time; the caches live on named volumes.
FROM base AS development
ARG USER_ID=1000
ARG GROUP_ID=1000
RUN addgroup -g ${GROUP_ID} -S app && adduser -u ${USER_ID} -S -G app app \
 && mkdir -p /gomodcache /gocache && chown app:app /gomodcache /gocache
ENV GOMODCACHE=/gomodcache GOCACHE=/gocache
USER app
ARG CMD
ENV CMD=${CMD}
CMD ["sh", "-c", "exec go run ./cmd/${CMD}"]

FROM base AS build
COPY --from=kit / /framework
COPY --from=errs / /errs
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG CMD
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd/${CMD}

FROM alpine:3.22 AS production
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/app /usr/local/bin/app
USER nobody
ENTRYPOINT ["/usr/local/bin/app"]
`

	goldenComposeMigrateWorker = `  migrate:
    build:
      context: .
      dockerfile: Dockerfile
      target: development
      args:
        CMD: migrate
        USER_ID: "${USER_ID}"
        GROUP_ID: "${GROUP_ID}"
      additional_contexts:
        kit: ../framework
        errs: ../errs
    command: ["sh", "-c", "exec go run ./cmd/migrate up"]
    env_file: .env
    environment:
      OTEL_SERVICE_NAME: shop-migrate
    volumes:
      - .:/src
      - ../framework:/framework:ro
      - ../errs:/errs:ro
      - gomodcache:/gomodcache
      - gocache:/gocache
    depends_on:
      postgres:
        condition: service_healthy
    restart: "no"

  worker:
    build:
      context: .
      dockerfile: Dockerfile
      target: development
      args:
        CMD: worker
        USER_ID: "${USER_ID}"
        GROUP_ID: "${GROUP_ID}"
      additional_contexts:
        kit: ../framework
        errs: ../errs
    env_file: .env
    environment:
      OTEL_SERVICE_NAME: shop-worker
    volumes:
      - .:/src
      - ../framework:/framework:ro
      - ../errs:/errs:ro
      - gomodcache:/gomodcache
      - gocache:/gocache
    depends_on:
      postgres:
        condition: service_healthy
      valkey:
        condition: service_started
      migrate:
        condition: service_completed_successfully
    restart: unless-stopped
`
)

// TestGoModByteIdentical is the golden-equivalence test for the Data.Kit/
// Data.Errs/Data.Mods registry refactor: with today's only real inputs (kit
// and errs, both --kit-replace'd, Mods empty), go.mod must render exactly as
// it did with the old Data.KitModule/KitVersion/KitReplace/ErrsModule/...
// fields.
func TestGoModByteIdentical(t *testing.T) {
	got := renderProjectFile(t, projectData("../framework"), "go.mod")
	if got != goldenGoMod {
		t.Errorf("go.mod changed by the module-registry refactor:\ngot:\n%s\nwant:\n%s", got, goldenGoMod)
	}
}

// TestDockerfileByteIdentical is Dockerfile's counterpart to
// TestGoModByteIdentical.
func TestDockerfileByteIdentical(t *testing.T) {
	got := renderProjectFile(t, projectData("../framework"), "Dockerfile")
	if got != goldenDockerfile {
		t.Errorf("Dockerfile changed by the module-registry refactor:\ngot:\n%s\nwant:\n%s", got, goldenDockerfile)
	}
}

// TestComposeYmlByteIdentical is compose.yml's counterpart to
// TestGoModByteIdentical. compose.yml also carries the project name/env
// values baked in by projectData/renderProjectFile, so this pins only the
// migrate/worker services (the ones with kit-replace wiring) rather than the
// whole file.
func TestComposeYmlByteIdentical(t *testing.T) {
	got := renderProjectFile(t, projectData("../framework"), "compose.yml")
	if !strings.Contains(got, goldenComposeMigrateWorker) {
		t.Errorf("compose.yml migrate/worker services changed by the module-registry refactor:\ngot:\n%s\nwant (substring):\n%s", got, goldenComposeMigrateWorker)
	}
}
