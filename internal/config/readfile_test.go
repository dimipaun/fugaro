package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadRegularRefusesWhatIsNotARegularFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(good, []byte("FROM x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadRegular(good, 100); err != nil || string(b) != "FROM x\n" {
		t.Fatalf("%q, %v", b, err)
	}
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("TOPSECRETVALUE"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"symlink": link, "fifo": fifo, "device": "/dev/null", "directory": dir} {
		if b, err := ReadRegular(path, 100); err == nil || len(b) != 0 || strings.Contains(err.Error(), "TOPSECRETVALUE") {
			t.Errorf("%s: %q, %v", name, b, err)
		}
	}
	if _, err := ReadRegular(good, 3); err == nil {
		t.Error("a file over the cap was read")
	}
	if _, err := ReadRegular(good, 7); err != nil {
		t.Errorf("a file at the cap: %v", err)
	}
	if _, err := ReadRegular(filepath.Join(dir, "none"), 100); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
}

// The reads of a checkout's own files (package.json, .yarnrc.yml, the
// workflow's Dockerfile) never follow a link out of it.
func TestCheckoutFileReadsRefuseSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte(`{"packageManager":"pnpm@9.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectNodePM(root); err == nil {
		t.Error("DetectNodePM followed a package.json symlink")
	}
	if err := os.WriteFile(outside, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	if ps := checkDockerfile("workflows.w", root, Workflow{Dockerfile: "Dockerfile", Base: "go"}); len(ps) != 1 || !strings.Contains(ps[0].Message, "cannot be read") {
		t.Errorf("problems %+v", ps)
	}
	if err := os.WriteFile(outside, []byte("enableGlobalCache: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".yarnrc.yml")); err != nil {
		t.Fatal(err)
	}
	if !yarnGlobalCache(root, 4) {
		t.Error("yarnGlobalCache read a .yarnrc.yml symlink")
	}
}
