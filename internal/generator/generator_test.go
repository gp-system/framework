package generator

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNaming(t *testing.T) {
	cases := []struct{ in, pascal, camel, snake, kebab string }{
		{"news", "News", "news", "news", "news"},
		{"server-stats", "ServerStats", "serverStats", "server_stats", "server-stats"},
		{"getNews", "GetNews", "getNews", "get_news", "get-news"},
		{"markAllNotificationsRead", "MarkAllNotificationsRead", "markAllNotificationsRead", "mark_all_notifications_read", "mark-all-notifications-read"},
		{"listContacts", "ListContacts", "listContacts", "list_contacts", "list-contacts"},
		{"HTTPServer", "HTTPServer", "hTTPServer", "http_server", "http-server"},
	}
	for _, c := range cases {
		if got := ToPascal(c.in); got != c.pascal {
			t.Errorf("ToPascal(%q) = %q, want %q", c.in, got, c.pascal)
		}
		if got := ToCamel(c.in); got != c.camel {
			t.Errorf("ToCamel(%q) = %q, want %q", c.in, got, c.camel)
		}
		if got := ToSnake(c.in); got != c.snake {
			t.Errorf("ToSnake(%q) = %q, want %q", c.in, got, c.snake)
		}
		if got := ToKebab(c.in); got != c.kebab {
			t.Errorf("ToKebab(%q) = %q, want %q", c.in, got, c.kebab)
		}
	}

	if err := ValidateName("module", "news"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"News", "my-module", "9lives", "a_b", ""} {
		if err := ValidateName("module", bad); err == nil {
			t.Errorf("ValidateName(%q) accepted invalid name", bad)
		}
	}
}

func TestValidateSnakeName(t *testing.T) {
	for _, ok := range []string{"add_blog", "init", "add_user_role", "step2"} {
		if err := ValidateSnakeName("migration", ok); err != nil {
			t.Errorf("ValidateSnakeName(%q) rejected valid name: %v", ok, err)
		}
	}
	// Path-traversal and other separators must be rejected (H5).
	for _, bad := range []string{"../../etc/evil", "a/b", "a.b", "9lives", "_x", "a__b", "", "Add"} {
		if err := ValidateSnakeName("migration", bad); err == nil {
			t.Errorf("ValidateSnakeName(%q) accepted traversal/invalid name", bad)
		}
	}
}

func TestValidateIdentifier(t *testing.T) {
	for _, ok := range []string{"getNews", "ListThings", "x1"} {
		if err := ValidateIdentifier("operation", ok); err != nil {
			t.Errorf("ValidateIdentifier(%q) rejected valid identifier: %v", ok, err)
		}
	}
	// Separators, symbols and Go keywords must be rejected (M12).
	for _, bad := range []string{"get/news", "get.news", "get-news", "range", "func", "type", "1x", ""} {
		if err := ValidateIdentifier("operation", bad); err == nil {
			t.Errorf("ValidateIdentifier(%q) accepted invalid identifier", bad)
		}
	}
}

func TestNamespaceAndSpecFile(t *testing.T) {
	api := Data{Project: "shop", Module: "news", Surface: "api"}
	if api.Namespace() != "Shop.News" || api.SpecFile() != "Shop.News.yaml" {
		t.Errorf("api: %q %q", api.Namespace(), api.SpecFile())
	}
	admin := Data{Project: "shop", Module: "news", Surface: "admin"}
	if admin.Namespace() != "Shop.NewsAdmin" {
		t.Errorf("admin namespace = %q (must be a SIBLING namespace, never nested under the api service)", admin.Namespace())
	}
	web := Data{Project: "shop", Module: "news", Surface: "web"}
	if web.Namespace() != "Shop.NewsWeb" || web.SpecFile() != "Shop.NewsWeb.yaml" {
		t.Errorf("web: %q %q (must be a SIBLING namespace, never nested under the api service)", web.Namespace(), web.SpecFile())
	}
	backoffice := Data{Project: "shop", Module: "news", Surface: "backoffice"}
	if backoffice.Namespace() != "Shop.NewsBackoffice" || backoffice.SpecFile() != "Shop.NewsBackoffice.yaml" {
		t.Errorf("backoffice: %q %q", backoffice.Namespace(), backoffice.SpecFile())
	}
	if api.BasePath() != "/news" || admin.BasePath() != "/admin/news" || backoffice.BasePath() != "/backoffice/news" {
		t.Errorf("base paths: %q %q %q", api.BasePath(), admin.BasePath(), backoffice.BasePath())
	}
}

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := NewManifest("shop", "github.com/acme/shop", "kit@v0.1.0")
	if err := m.AddSurface("news", "api"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSurface("news", "api"); err == nil {
		t.Error("duplicate surface accepted")
	}
	if err := m.AddSurface("news", "backoffice"); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(dir, "internal", "modules")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := FindRoot(nested)
	if err != nil || root != dir {
		t.Fatalf("FindRoot from nested dir = %q, %v", root, err)
	}

	loaded, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "shop" || loaded.Modules["news"] == nil {
		t.Errorf("manifest lost data: %+v", loaded)
	}
}

func TestAddEventAndEventName(t *testing.T) {
	m := NewManifest("shop", "github.com/acme/shop", "kit@v0.1.0")
	if err := m.AddEvent("news", "articlePublished"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddEvent("news", "articlePublished"); err == nil {
		t.Error("duplicate event accepted")
	}
	if got := m.Modules["news"].Events; len(got) != 1 || got[0] != "articlePublished" {
		t.Errorf("events = %v", got)
	}

	// EventName defaults the producer to Module, or uses EventModule when set.
	own := Data{Module: "news", Event: "articlePublished"}
	if own.EventName() != "news.articlePublished" {
		t.Errorf("EventName (own) = %q", own.EventName())
	}
	cross := Data{Module: "billing", EventModule: "news", Event: "articlePublished"}
	if cross.EventName() != "news.articlePublished" {
		t.Errorf("EventName (cross) = %q", cross.EventName())
	}

	pv := cross.PathVars()
	if pv["event"] != "article_published" {
		t.Errorf("PathVars event = %q", pv["event"])
	}
}

func TestManifestAddSeed(t *testing.T) {
	m := NewManifest("shop", "github.com/acme/shop", "kit@v0.1.0")
	if err := m.AddSeed("adminUser"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSeed("adminUser"); err == nil {
		t.Error("duplicate seeder accepted")
	}
	if got := m.Seeds; len(got) != 1 || got[0] != "adminUser" {
		t.Errorf("seeds = %v", got)
	}

	pv := Data{Seed: "adminUser"}.PathVars()
	if pv["seed"] != "admin_user" {
		t.Errorf("PathVars seed = %q", pv["seed"])
	}
}

// TestManifestAddDB pins the named-connection manifest bookkeeping added via
// `add db`: it records name+connector, rejects a duplicate name, round-trips
// through Save/Load, and FindDB reports misses for unknown names/modules.
func TestManifestAddDB(t *testing.T) {
	dir := t.TempDir()
	m := NewManifest("shop", "github.com/acme/shop", "kit@v0.1.0")

	if err := m.AddDB("news", "analytics", "bun"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddDB("news", "analytics", "pgx"); err == nil {
		t.Error("duplicate connection name accepted")
	}
	if err := m.AddDB("news", "reporting", "pgx"); err != nil {
		t.Fatal(err)
	}

	if conn, ok := m.FindDB("news", "analytics"); !ok || conn.Connector != "bun" {
		t.Errorf("FindDB(news, analytics) = %+v, %v", conn, ok)
	}
	if _, ok := m.FindDB("news", "missing"); ok {
		t.Error("FindDB found a connection that was never added")
	}
	if _, ok := m.FindDB("billing", "analytics"); ok {
		t.Error("FindDB found a connection under the wrong module")
	}

	m.SetSurfaceDB("news", "reports", "analytics")

	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if conn, ok := loaded.FindDB("news", "analytics"); !ok || conn.Connector != "bun" {
		t.Errorf("round-tripped FindDB(news, analytics) = %+v, %v", conn, ok)
	}
	if got := loaded.Modules["news"].SurfaceDBs["reports"]; got != "analytics" {
		t.Errorf("round-tripped SurfaceDBs[reports] = %q, want analytics", got)
	}
}

func TestRenderTreeFormatsGoAndSubstitutesPaths(t *testing.T) {
	tfs := fstest.MapFS{
		"tree/cmd/__module__/main.go.tmpl": {Data: []byte(
			"package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"{{.Module}}\")}\n")},
		"tree/static.txt": {Data: []byte("verbatim {{not rendered}}")},
	}
	eng := &Engine{}
	files, err := eng.RenderTree(tfs, "tree", Data{Module: "news"}, "/target")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = string(f.Content)
	}

	mainGo, ok := byPath[filepath.FromSlash("/target/cmd/news/main.go")]
	if !ok {
		t.Fatalf("path placeholder not substituted: %v", keys(byPath))
	}
	if !strings.Contains(mainGo, "fmt.Println(\"news\")") || !strings.Contains(mainGo, "import \"fmt\"") {
		t.Errorf("rendered main.go wrong:\n%s", mainGo)
	}
	if byPath[filepath.FromSlash("/target/static.txt")] != "verbatim {{not rendered}}" {
		t.Error("non-.tmpl file was rendered")
	}
}

func TestRenderTreeRejectsInvalidGo(t *testing.T) {
	tfs := fstest.MapFS{
		"tree/broken.go.tmpl": {Data: []byte("package main\nfunc {")},
	}
	if _, err := (&Engine{}).RenderTree(tfs, "tree", Data{}, "/t"); err == nil {
		t.Fatal("invalid rendered Go accepted")
	}
}

func TestApplyRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &Engine{Out: os.Stderr}
	err := eng.Apply([]File{{Path: target, Content: []byte("new")}})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("want overwrite refusal, got %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "old" {
		t.Error("file was clobbered despite refusal")
	}

	eng.Force = true
	if err := eng.Apply([]File{{Path: target, Content: []byte("new")}}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Error("force overwrite did not write")
	}
}

func TestApplyForceListsOverwrites(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.txt")
	fresh := filepath.Join(dir, "fresh.txt")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	eng := &Engine{Force: true, Out: &buf}
	err := eng.Apply([]File{
		{Path: existing, Content: []byte("new")},
		{Path: fresh, Content: []byte("brand new")},
	})
	if err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if strings.Count(out, "overwriting "+existing) != 1 {
		t.Errorf("want exactly one overwrite warning for %s:\n%s", existing, out)
	}
	if strings.Contains(out, "overwriting "+fresh) {
		t.Errorf("fresh file must not be reported as overwrite:\n%s", out)
	}
	// warning must come before the write line for the same file
	if i, j := strings.Index(out, "overwriting "+existing), strings.Index(out, "wrote "+existing); i < 0 || j < 0 || i > j {
		t.Errorf("overwrite warning must precede the write:\n%s", out)
	}
	if data, _ := os.ReadFile(existing); string(data) != "new" {
		t.Error("force overwrite did not replace content")
	}
}

func TestApplyForceDryRun(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	eng := &Engine{Force: true, DryRun: true, Out: &buf}
	if err := eng.Apply([]File{{Path: existing, Content: []byte("new")}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "would overwrite "+existing) {
		t.Errorf("dry-run force want 'would overwrite':\n%s", buf.String())
	}
	if data, _ := os.ReadFile(existing); string(data) != "old" {
		t.Error("force dry-run modified the file")
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	eng := &Engine{DryRun: true, Out: os.Stderr}
	if err := eng.Apply([]File{{Path: filepath.Join(dir, "a.txt"), Content: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
		t.Error("dry-run wrote a file")
	}
}

func TestInsertAtAnchor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "register.go")
	content := "func Register() {\n\t// gpsystem:surfaces\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &Engine{Out: os.Stderr}
	inserted, err := eng.InsertAtAnchor(path, "gpsystem:surfaces", "wireApi()\nwireMore()")
	if err != nil || !inserted {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "func Register() {\n\twireApi()\n\twireMore()\n\t// gpsystem:surfaces\n}\n"
	if string(got) != want {
		t.Errorf("insertion wrong:\n%q\nwant\n%q", got, want)
	}

	// Idempotent: same snippet again is a no-op.
	inserted, err = eng.InsertAtAnchor(path, "gpsystem:surfaces", "wireApi()\nwireMore()")
	if err != nil || inserted {
		t.Errorf("re-insert: inserted=%v err=%v, want skip", inserted, err)
	}

	// Missing anchor errors.
	if _, err := eng.InsertAtAnchor(path, "gpsystem:nope", "x"); err == nil {
		t.Error("missing anchor accepted")
	}
}

func TestInsertIntoContent(t *testing.T) {
	content := "services:\n  # gpsystem:services\n"
	updated, inserted, err := InsertIntoContent(content, "gpsystem:services", "news:\n  image: news")
	if err != nil || !inserted {
		t.Fatal(err)
	}
	want := "services:\n  news:\n    image: news\n  # gpsystem:services\n"
	if updated != want {
		t.Errorf("insertion wrong:\n%q\nwant\n%q", updated, want)
	}

	// Idempotent: same snippet again is a no-op, content unchanged.
	again, inserted, err := InsertIntoContent(updated, "gpsystem:services", "news:\n  image: news")
	if err != nil || inserted {
		t.Errorf("re-insert: inserted=%v err=%v, want skip", inserted, err)
	}
	if again != updated {
		t.Errorf("content changed on idempotent re-insert:\n%q\nwant\n%q", again, updated)
	}

	// Missing anchor errors.
	if _, _, err := InsertIntoContent(content, "gpsystem:nope", "x"); err == nil {
		t.Error("missing anchor accepted")
	}
}

// TestInsertAtAnchorFileLevel pins the column-0 anchor geometry the module
// register.go uses: a multi-line function snippet inserted at a file-level
// anchor must keep its own internal indentation (no re-indent at indent "").
func TestInsertAtAnchorFileLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "register.go")
	content := "package news\n\n// gpsystem:surfaces\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &Engine{Out: os.Stderr}
	snippet := "func RegisterWeb(router fiber.Router, deps Dependencies) {\n\twire()\n}"
	inserted, err := eng.InsertAtAnchor(path, "gpsystem:surfaces", snippet)
	if err != nil || !inserted {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "package news\n\nfunc RegisterWeb(router fiber.Router, deps Dependencies) {\n\twire()\n}\n// gpsystem:surfaces\n"
	if string(got) != want {
		t.Errorf("insertion wrong:\n%q\nwant\n%q", got, want)
	}

	// A second surface lands before the anchor, after the first one.
	second := "func RegisterAdmin(router fiber.Router, deps Dependencies) {\n\twire()\n}"
	if _, err := eng.InsertAtAnchor(path, "gpsystem:surfaces", second); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if webIdx, adminIdx := strings.Index(string(got), "RegisterWeb"), strings.Index(string(got), "RegisterAdmin"); webIdx == -1 || adminIdx == -1 || adminIdx < webIdx {
		t.Errorf("surfaces out of add-order:\n%s", got)
	}
}

// TestInsertAtAnchorMissingPrintsFixHint pins the anchors.go:pools bug report:
// when the marker comment was removed (or never existed) from the target
// file, InsertAtAnchor must not just fail silently: it should print the
// rendered snippet and the marker line to restore, so the user can paste it
// in by hand.
func TestInsertAtAnchorMissingPrintsFixHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	eng := &Engine{Out: &out}
	snippet := "newsPool, err := pg.NewPool(ctx, cfg.NewsDB)"
	_, err := eng.InsertAtAnchor(path, "gpsystem:pools", snippet)
	if err == nil {
		t.Fatal("expected error for missing anchor")
	}
	if !errors.Is(err, ErrAnchorNotFound) {
		t.Errorf("err = %v, want wrapping ErrAnchorNotFound", err)
	}

	hint := out.String()
	if !strings.Contains(hint, snippet) {
		t.Errorf("hint missing rendered snippet:\n%s", hint)
	}
	if !strings.Contains(hint, "// gpsystem:pools") {
		t.Errorf("hint missing marker line to restore:\n%s", hint)
	}
}

// TestInsertAtAnchorIdempotentAfterGofmtRealign reproduces the shared-config
// bug: a struct field inserted at an anchor is later re-aligned by gofmt
// (extra spaces between name and type). A re-run must still recognize it as
// present and not duplicate it.
func TestInsertAtAnchorIdempotentAfterGofmtRealign(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.go")
	// gofmt-aligned struct: the existing field uses column alignment, while
	// the snippet uses a single space.
	content := "type Config struct {\n" +
		"\tServer server.Config\n" +
		"\tJWT    auth.Config `envPrefix:\"JWT_\"`\n" +
		"\t// gpsystem:config\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &Engine{Out: os.Stderr}
	inserted, err := eng.InsertAtAnchor(path, "gpsystem:config", "JWT auth.Config `envPrefix:\"JWT_\"`")
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Error("snippet re-inserted despite being present (gofmt whitespace re-alignment defeated idempotency)")
	}
	if got, _ := os.ReadFile(path); strings.Count(string(got), "auth.Config") != 1 {
		t.Errorf("field duplicated:\n%s", got)
	}
}

// TestInsertAtAnchor_IdenticalSnippetAtSiblingAnchors reproduces a bug where
// two modules wired into the shared cmd/worker/main.go each render an
// identical snippet line (e.g. "Mailer: mailer," or a dispatcher
// construction) at their own gpsystem:deps-init(<module>) anchor. A
// whole-file idempotency check would see the first module's line already
// present and silently skip inserting it for the second module.
func TestInsertAtAnchor_IdenticalSnippetAtSiblingAnchors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := "newsDeps := news.Dependencies{\n" +
		"\t// gpsystem:deps-init(news)\n" +
		"}\n" +
		"contactDeps := contact.Dependencies{\n" +
		"\t// gpsystem:deps-init(contact)\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &Engine{Out: os.Stderr}
	if _, err := eng.InsertAtAnchor(path, "gpsystem:deps-init(news)", "Mailer: mailer,"); err != nil {
		t.Fatal(err)
	}
	inserted, err := eng.InsertAtAnchor(path, "gpsystem:deps-init(contact)", "Mailer: mailer,")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("contact's identical snippet was skipped because news's already exists in the file")
	}
	if got, _ := os.ReadFile(path); strings.Count(string(got), "Mailer: mailer,") != 2 {
		t.Errorf("want one \"Mailer: mailer,\" per module, got:\n%s", got)
	}
}

// TestInsertAtAnchor_NonAnchorLineContainingAnchorTextIsNotAnAnchorLine
// guards against a bug class where a rendered file contains the bare
// substring "gpsystem:" outside of an anchor comment (e.g. as part of a
// host:container path in a Docker volume mount line). Scoping the
// idempotency check by that line would exclude content above it (e.g. a
// service block already present higher up in compose.yml) from the search,
// causing a duplicate insertion.
func TestInsertAtAnchor_NonAnchorLineContainingAnchorTextIsNotAnAnchorLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yml")
	content := "services:\n" +
		"  mailpit:\n" +
		"    image: axllent/mailpit:latest\n" +
		"  app:\n" +
		"    volumes:\n" +
		"      - ./gpsystem:/gpsystem:ro\n" +
		"  # gpsystem:services\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &Engine{Out: os.Stderr}
	inserted, err := eng.InsertAtAnchor(path, "gpsystem:services", "mailpit:\n  image: axllent/mailpit:latest")
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Error("mailpit re-inserted despite already being present above a volume-mount line containing \"gpsystem:\"")
	}
	got, _ := os.ReadFile(path)
	if n := strings.Count(string(got), "\n  mailpit:\n"); n != 1 {
		t.Errorf("mailpit duplicated (%d service keys):\n%s", n, got)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
