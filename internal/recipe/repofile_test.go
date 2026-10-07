package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRepoFile(t *testing.T) {
	root := t.TempDir()
	if _, found, err := ReadRepoFile(root, "x"); found || err != nil {
		t.Fatalf("absent: found %v, err %v", found, err)
	}
	dir := filepath.Join(root, ".fugaro", "recipes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, found, err := ReadRepoFile(root, "x"); !found || err != nil || string(data) != "version: 1\n" {
		t.Fatalf("regular: %q, %v, %v", data, found, err)
	}
	if err := os.Symlink(filepath.Join(dir, "x.yaml"), filepath.Join(dir, "y.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRepoFile(root, "y"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink: %v", err)
	}
	other := t.TempDir()
	if err := os.Symlink(filepath.Join(root, ".fugaro"), filepath.Join(other, ".fugaro")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRepoFile(other, "x"); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("linked directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.yaml"), []byte(strings.Repeat("#", MaxBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRepoFile(root, "big"); err == nil || !strings.Contains(err.Error(), "16 KiB") {
		t.Fatalf("big: %v", err)
	}
}
