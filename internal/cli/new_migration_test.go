package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

var fullMigrationPrefixRe = regexp.MustCompile(`^\d{14}$`)

func TestNextMigrationPrefixAt_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 14, 30, 12, 0, time.UTC)

	got, err := nextMigrationPrefixAt(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	want := "20260720143012"
	if got != want {
		t.Errorf("nextMigrationPrefixAt(empty dir) = %q, want %q", got, want)
	}
}

func TestNextMigrationPrefixAt_MissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	now := time.Date(2026, 7, 20, 14, 30, 12, 0, time.UTC)

	got, err := nextMigrationPrefixAt(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if !fullMigrationPrefixRe.MatchString(got) {
		t.Errorf("nextMigrationPrefixAt(missing dir) = %q, want 14 digits", got)
	}
}

func TestNextMigrationPrefixAt_CollisionBumpsOneSecond(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 14, 30, 12, 0, time.UTC)

	if err := os.WriteFile(filepath.Join(dir, now.Format(migrationTimestampFormat)+"_x.sql"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := nextMigrationPrefixAt(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(time.Second).Format(migrationTimestampFormat)
	if got != want {
		t.Errorf("nextMigrationPrefixAt(collision) = %q, want %q", got, want)
	}
}

func TestNextMigrationPrefixAt_IgnoresNonTimestampFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 14, 30, 12, 0, time.UTC)

	// A 5-digit sequential filename (not a 14-digit timestamp prefix) must
	// not be mistaken for a timestamp prefix or otherwise block allocation.
	if err := os.WriteFile(filepath.Join(dir, "00001_init.sql"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "00002_outbox.sql"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := nextMigrationPrefixAt(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	want := now.Format(migrationTimestampFormat)
	if got != want {
		t.Errorf("nextMigrationPrefixAt(non-timestamp files) = %q, want %q", got, want)
	}
}
