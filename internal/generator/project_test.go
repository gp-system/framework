package generator

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHasSocialProvider pins the lookup the social login templates gate
// their per-provider blocks on: present, absent, and an empty selection.
func TestHasSocialProvider(t *testing.T) {
	d := Data{Social: []string{"discord", "google"}}
	for _, name := range []string{"discord", "google"} {
		if !d.HasSocialProvider(name) {
			t.Errorf("HasSocialProvider(%q) = false, want true", name)
		}
	}
	if d.HasSocialProvider("apple") {
		t.Error(`HasSocialProvider("apple") = true, want false`)
	}
	if (Data{}).HasSocialProvider("discord") {
		t.Error("HasSocialProvider on a zero-value Data should be false")
	}
}

// TestReadReplaces pins the cases callers rely on to recover replace
// directives from an existing project's go.mod: present (single and
// multiple), absent, and a missing file.
func TestReadReplaces(t *testing.T) {
	const kitModule = "github.com/gp-system/framework"

	write := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "go.mod")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("hit", func(t *testing.T) {
		path := write(t, "module example.com/shop\n\ngo 1.25\n\nreplace "+kitModule+" => ../framework\n")
		replaces, err := ReadReplaces(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := replaces[kitModule]; got != "../framework" {
			t.Errorf("ReadReplaces[kit] = %q, want ../framework", got)
		}
	})

	t.Run("miss", func(t *testing.T) {
		path := write(t, "module example.com/shop\n\ngo 1.25\n")
		replaces, err := ReadReplaces(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := replaces[kitModule]; got != "" {
			t.Errorf("ReadReplaces[kit] = %q, want empty string", got)
		}
	})

	t.Run("replace for something else", func(t *testing.T) {
		path := write(t, "module example.com/shop\n\ngo 1.25\n\nreplace github.com/other/pkg => ../pkg\n")
		replaces, err := ReadReplaces(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := replaces[kitModule]; got != "" {
			t.Errorf("ReadReplaces[kit] = %q, want empty string (non-kit replace)", got)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		replaces, err := ReadReplaces(filepath.Join(t.TempDir(), "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		if got := replaces[kitModule]; got != "" {
			t.Errorf("ReadReplaces[kit] = %q, want empty string", got)
		}
	})

	t.Run("kit and errs replaces side by side", func(t *testing.T) {
		const errsModule = "github.com/gp-system/errs"
		path := write(t, "module example.com/shop\n\ngo 1.25\n\nreplace "+kitModule+" => ../framework\nreplace "+errsModule+" => ../errs\n")
		replaces, err := ReadReplaces(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := replaces[kitModule]; got != "../framework" {
			t.Errorf("ReadReplaces[kit] = %q, want ../framework", got)
		}
		if got := replaces[errsModule]; got != "../errs" {
			t.Errorf("ReadReplaces[errs] = %q, want ../errs", got)
		}
	})
}

// TestMounts pins the host-path -> in-container-path resolution both dev
// mounts rely on: relative replace targets resolve against /src, absolute
// ones stay put, unset replaces yield no mount.
func TestMounts(t *testing.T) {
	d := Data{
		Kit:  ModuleRef{Replace: "../framework"},
		Errs: ModuleRef{Replace: "../errs"},
	}
	if got := d.Kit.Mount(); got != "/framework" {
		t.Errorf("Kit.Mount() = %q, want /framework", got)
	}
	if got := d.Errs.Mount(); got != "/errs" {
		t.Errorf("Errs.Mount() = %q, want /errs", got)
	}
	deep := ModuleRef{Replace: "../../errs"}
	if got := deep.Mount(); got != "/errs" {
		t.Errorf("Mount(../../errs) = %q, want /errs", got)
	}
	if got := (ModuleRef{}).Mount(); got != "" {
		t.Errorf("Mount on zero-value ModuleRef = %q, want empty string", got)
	}
}
