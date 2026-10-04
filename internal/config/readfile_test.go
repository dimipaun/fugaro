package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if _, err := yarnGlobalCache(root, 4); err == nil {
		t.Error("yarnGlobalCache read a .yarnrc.yml symlink, or fell back silently")
	}
	if g, err := yarnGlobalCache(t.TempDir(), 4); err != nil || !g {
		t.Errorf("no .yarnrc.yml: %v, %v", g, err)
	}
}

// A FIFO swapped in after the Lstat must not block the open: openRegular
// returns at once, and the fstat check refuses it.
func TestOpenRegularDoesNotBlockOnAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		f, err := openRegular(fifo)
		if err == nil {
			fi, _ := f.Stat()
			if fi.Mode().IsRegular() {
				err = errors.New("a FIFO is regular")
			}
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openRegular blocked on a FIFO")
	}
	link := filepath.Join(t.TempDir(), "l")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openRegular(link); err == nil {
		f.Close()
		t.Error("openRegular followed a symlink")
	}
}
