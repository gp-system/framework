package cli

import (
	"testing"
)

func TestResolveReplacesReplaceDevIncludesMinioStorageDriver(t *testing.T) {
	replaces, err := resolveReplaces(nil, "/work", "/work/shop")
	if err != nil {
		t.Fatal(err)
	}
	want := "../minio-storage-driver"
	if got := replaces["minio-storage-driver"]; got != want {
		t.Errorf("minio-storage-driver replace = %q, want %q (sibling checkout from --replace-dev)", got, want)
	}
}

func TestResolveReplacesExplicitMinioStorageDriverWins(t *testing.T) {
	replaces, err := resolveReplaces([]string{"minio-storage-driver=/work/s3-fork"}, "/work", "/work/shop")
	if err != nil {
		t.Fatal(err)
	}
	want := "../s3-fork"
	if got := replaces["minio-storage-driver"]; got != want {
		t.Errorf("minio-storage-driver replace = %q, want %q (explicit --replace must override --replace-dev's sibling)", got, want)
	}
}

func TestResolveReplacesFrameworkAloneLeavesRegistryUnreplaced(t *testing.T) {
	replaces, err := resolveReplaces([]string{"framework=/work/framework"}, "", "/work/shop")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := replaces["minio-storage-driver"]; ok {
		t.Errorf("minio-storage-driver replace = %q, want unset when only --replace framework=... is given", got)
	}
	if got, ok := replaces["storage"]; ok {
		t.Errorf("storage replace = %q, want unset when only --replace framework=... is given", got)
	}
}
