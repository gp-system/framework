package generator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// devSrcDir is the WORKDIR of the development/build Dockerfile stages
// (Dockerfile.tmpl) and the bind-mount target of the project source in
// compose.yml.tmpl (`.:/src`). ModuleRef.Mount resolves relative to it so a
// relative Replace lands at the same place on the host and in-container.
const devSrcDir = "/src"

// Project is an opened consumer project: its root directory and manifest.
type Project struct {
	Root     string
	Manifest *Manifest

	// Warning is a non-fatal discrepancy found while opening the project
	// (e.g. go.mod disagreeing with the manifest). Empty when there is none;
	// callers that want to surface it print it themselves.
	Warning string
}

// OpenProject locates the project containing start (via gpsystem.yaml) and
// loads its manifest. Project.Warning is set when go.mod disagrees with it.
func OpenProject(start string) (*Project, error) {
	root, err := FindRoot(start)
	if err != nil {
		return nil, err
	}
	m, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	proj := &Project{Root: root, Manifest: m}
	if mod := readGoModulePath(filepath.Join(root, "go.mod")); mod != "" && mod != m.Module {
		proj.Warning = fmt.Sprintf("warning: go.mod module %q differs from manifest module %q, using the manifest", mod, m.Module)
	}
	return proj, nil
}

var modLineRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)

func readGoModulePath(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := modLineRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

// ReadReplaces reads the go.mod at gomodPath and returns every
// `replace <module> => <path>` directive it contains, keyed by module path.
// Only the single-line form emitted by go.mod.tmpl is recognized. Returns a
// nil map and no error when gomodPath does not exist, so callers that
// haven't written a go.mod yet (a project being generated for the first
// time) can look up a key in the result unconditionally and get ""; any
// other read error (permissions, a directory in the way, ...) is returned.
func ReadReplaces(gomodPath string) (map[string]string, error) {
	data, err := os.ReadFile(gomodPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	replaces := map[string]string{}
	for _, m := range replaceLineRe.FindAllSubmatch(data, -1) {
		replaces[string(m[1])] = string(m[2])
	}
	return replaces, nil
}

var replaceLineRe = regexp.MustCompile(`(?m)^replace\s+(\S+)\s*=>\s*(\S+)`)

// Data is the template context shared by all generators. Unused fields stay
// empty for generators that do not need them.
type Data struct {
	Project     string   // project name (lowercase)
	GoModule    string   // consumer Go module path
	Module      string   // module name (lowercase), e.g. "news"
	Surface     string   // surface name (lowercase); "api" mounts at the module root
	DB          string   // "pgx" (pgx-native dbx/pg.DB) or "bun" (dbx/bunx adapter)
	DBConn      string   // named DB connection (lowercase), e.g. "analytics"; empty = the project's default connection
	DBConnector string   // named connection's stack: "pgx" or "bun"; only meaningful when DBConn is set
	Operation   string   // handler generator: operation id (camelCase input)
	Method      string   // handler generator: HTTP method (GET, POST, ...)
	HTTPPath    string   // handler generator: route path, e.g. "/things/{id}"
	PathParams  []string // handler generator: names extracted from HTTPPath
	Group       string   // handler generator: category the operation's file groups under ("" = the module's base file)
	Permission  string   // handler generator: RBAC permission tag (@permission → x-permission)
	Policy      string   // handler generator: policy name tag (@policy → x-policy)

	Event        string // event/listener generator: event name (camelCase input)
	EventModule  string // listener generator: module owning the event (defaults to Module)
	Listener     string // listener generator: listener name (camelCase input)
	Job          string // job generator: job name (camelCase input)
	Cron         string // job generator: cron spec, e.g. "0 3 * * *"
	Queue        string // listener generator: asynq queue name ("" = default)
	Mail         string // mail generator: notifier name (camelCase input)
	Notification string // notification generator: notification name (camelCase input)
	Realtime     bool   // notification generator: whether `add realtime` has run (wires the broadcast channel)
	Auth         bool   // module generator: whether `add auth` has run (renders the identity middleware on the api router)
	Seed         string // seeder generator: seeder name (camelCase input)

	Social []string // auth generator: social login providers to pre-wire (discord, facebook, apple, google)

	Kit  ModuleRef   // the kit module (always set)
	Errs ModuleRef   // the standalone errs module (always set)
	Mods []ModuleRef // additional extracted modules this project requires; empty until later phases populate Registry
}

// AllModuleRefs returns every module this project depends on: Kit, Errs,
// then any additional Mods, in that order. Templates range over this single
// slice instead of enumerating Kit/Errs/Mods separately, so appending to
// Mods as the registry grows needs no template changes.
func (d Data) AllModuleRefs() []ModuleRef {
	return append([]ModuleRef{d.Kit, d.Errs}, d.Mods...)
}

// PathVars returns d's fields as the string substitutions used to render
// template file paths (e.g. __surface__ in a template tree path).
func (d Data) PathVars() map[string]string {
	return map[string]string{
		"project":      d.Project,
		"module":       d.Module,
		"surface":      d.Surface,
		"dbconn":       d.DBConn,
		"operation":    ToSnake(d.Operation),
		"handlerbase":  d.HandlerBase(),
		"event":        ToSnake(d.Event),
		"listener":     ToSnake(d.Listener),
		"job":          ToSnake(d.Job),
		"mail":         ToSnake(d.Mail),
		"notification": ToSnake(d.Notification),
		"seed":         ToSnake(d.Seed),
	}
}

// HandlerBase names the file a handler method groups into: the module's base
// handler file when Group is empty, or a per-category file otherwise. Both
// forms are snake_case, e.g. "contact_handler" or "contact_notifications_handler".
func (d Data) HandlerBase() string {
	if d.Group == "" {
		return d.Module + "_handler"
	}
	return d.Module + "_" + ToSnake(d.Group) + "_handler"
}

// HasSocialProvider reports whether name was passed to `add auth --social`,
// gating the per-provider blocks in the social login templates (imports,
// config fields, registrations).
func (d Data) HasSocialProvider(name string) bool {
	return slices.Contains(d.Social, name)
}

// EventName is the stable, persisted name of an event: "<module>.<event>" in
// camelCase, where the module is the producer (EventModule, or Module when a
// listener subscribes to an event its own module owns).
func (d Data) EventName() string {
	m := d.EventModule
	if m == "" {
		m = d.Module
	}
	return m + "." + ToCamel(d.Event)
}

// BasePath is the route prefix the surface mounts under the project's
// /api/v1 group. The api surface is the module root ("/news"); every other
// surface is namespaced under its own prefix ("/<surface>/news") so sibling
// surfaces never collide on the same routes.
func (d Data) BasePath() string {
	if d.Surface == "api" {
		return "/" + d.Module
	}
	return "/" + d.Surface + "/" + d.Module
}

// Namespace is the surface's TypeSpec namespace. The openapi3 emitter names
// its output file after it ({service-name}.yaml), so it must stay
// deterministic: Project.Module for api, Project.Module<Surface> otherwise.
// Surfaces must be SIBLING namespaces: nesting one @service namespace
// inside another merges their routes and trips duplicate-operation errors.
func (d Data) Namespace() string {
	ns := ToPascal(d.Project) + "." + ToPascal(d.Module)
	if d.Surface != "api" {
		ns += ToPascal(d.Surface)
	}
	return ns
}

// SpecFile is the OpenAPI document emitted for the surface.
func (d Data) SpecFile() string { return d.Namespace() + ".yaml" }

// GenPackage names the generated Go package for the surface.
func (d Data) GenPackage() string { return d.Module + d.Surface + "gen" }

// HasBody reports whether the handler generator's operation carries a
// request body (POST/PUT/PATCH).
func (d Data) HasBody() bool {
	return d.Method == "POST" || d.Method == "PUT" || d.Method == "PATCH"
}
