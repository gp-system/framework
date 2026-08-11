package generator

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/mod/modfile"
)

// TestRegistryVersionsMatchKit is a drift guard for the rule documented on
// Registry: once the kit's own go.mod requires a gp-system module without a
// local replace (i.e. the module is actually published and resolved from the
// proxy, not a sibling checkout), the Registry entry for that module must
// carry the exact same version. Otherwise a generated project's go.mod
// requires a version the kit itself doesn't build against -- as happened
// with httperr, dbx and auth after they were migrated to real modules but
// the Registry was never updated to match.
//
// A module the kit replaces locally (still unpublished) is exempt: its
// Registry version is a placeholder that only resolves via --replace-dev,
// and isn't expected to match anything in the kit's own require block.
func TestRegistryVersionsMatchKit(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this file's path")
	}
	kitGoMod := filepath.Join(filepath.Dir(thisFile), "..", "..", "go.mod")

	data, err := os.ReadFile(kitGoMod)
	if err != nil {
		t.Fatalf("reading kit go.mod at %s: %v", kitGoMod, err)
	}

	mf, err := modfile.Parse(kitGoMod, data, nil)
	if err != nil {
		t.Fatalf("parsing kit go.mod: %v", err)
	}

	kitRequire := map[string]string{}
	for _, r := range mf.Require {
		kitRequire[r.Mod.Path] = r.Mod.Version
	}
	kitReplaced := map[string]bool{}
	for _, r := range mf.Replace {
		kitReplaced[r.Old.Path] = true
	}

	check := func(name, path, registryVersion string) {
		t.Helper()
		if kitReplaced[path] {
			// Unpublished: the kit resolves it from a local checkout, so
			// its Registry placeholder version isn't expected to match.
			return
		}
		kitVersion, ok := kitRequire[path]
		if !ok {
			// Not a dependency of the kit at all (e.g. paginate, storage):
			// nothing in the kit's own go.mod to compare against.
			return
		}
		if registryVersion != kitVersion {
			t.Errorf("Registry entry %q (%s) has version %q, but the kit's own go.mod requires %q at %s -- update modules.go to match",
				name, path, registryVersion, kitVersion, kitGoMod)
		}
	}

	check(Errs.Name, Errs.Path, Errs.Version)
	for _, m := range Registry {
		check(m.Name, m.Path, m.Version)
	}
}
