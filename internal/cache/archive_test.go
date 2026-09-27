package cache

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestWriteExtractRoundTrip(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, src, map[string]string{"a/b.txt": "hello", "c.bin": "x"})
	if err := os.Symlink("a/b.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "c.bin"), 0o4755); err != nil { // setuid must not survive
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Write(&buf, []string{src}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := Extract(&buf, []string{dst}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "a/b.txt")); string(data) != "hello" {
		t.Fatalf("content = %q", data)
	}
	if target, _ := os.Readlink(filepath.Join(dst, "link")); target != "a/b.txt" {
		t.Fatalf("link = %q", target)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "c.bin")); fi.Mode()&os.ModeSetuid != 0 || fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", fi.Mode())
	}
}

// hostile builds an archive with the manifest and then hdrs.
func hostile(t *testing.T, hdrs ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	manifest := []byte(`{"version":1,"roots":1}`)
	_ = tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(manifest)
	for _, h := range hdrs {
		if h.Typeflag == tar.TypeReg {
			h.Size = 1
		}
		_ = tw.WriteHeader(h)
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("x"))
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return &buf
}

func TestRestoreRejectsTraversal(t *testing.T) {
	for _, name := range []string{"0/../../escape", "/abs", "1/out-of-range-root", "0/a/../../x"} {
		dst := t.TempDir()
		err := Extract(hostile(t, &tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}), []string{filepath.Join(dst, "root")}, 1<<20)
		if err == nil {
			t.Errorf("%s: extracted", name)
		}
		if _, statErr := os.Stat(filepath.Join(dst, "escape")); statErr == nil {
			t.Errorf("%s: wrote outside the root", name)
		}
	}
}

func TestRestoreRejectsSymlinkEscape(t *testing.T) {
	for _, target := range []string{"../../etc", "/etc/passwd"} {
		dst := t.TempDir()
		err := Extract(hostile(t, &tar.Header{Name: "0/link", Linkname: target, Typeflag: tar.TypeSymlink}), []string{dst}, 1<<20)
		if err == nil || !strings.Contains(err.Error(), "outside") {
			t.Errorf("symlink to %s: %v", target, err)
		}
	}
	if err := Extract(hostile(t, &tar.Header{Name: "0/hard", Linkname: "0/x", Typeflag: tar.TypeLink}), []string{t.TempDir()}, 1<<20); err == nil {
		t.Error("hardlink extracted")
	}
}

func TestRestoreRejectsWriteThroughExistingSymlink(t *testing.T) {
	dst, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Already in the root (baked into the image, say): a file link and a
	// directory link, both pointing outside.
	if err := os.Symlink(victim, filepath.Join(dst, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dst, "dir")); err != nil {
		t.Fatal(err)
	}
	// A regular entry at the link itself replaces the link, not the victim.
	if err := Extract(hostile(t, &tar.Header{Name: "0/a", Mode: 0o644, Typeflag: tar.TypeReg}), []string{dst}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Fatalf("wrote through the symlink: victim = %q", data)
	}
	if fi, _ := os.Lstat(filepath.Join(dst, "a")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the link at the target survived")
	}
	// An entry under the directory link must be refused, not written outside.
	if err := Extract(hostile(t, &tar.Header{Name: "0/dir/planted", Mode: 0o644, Typeflag: tar.TypeReg}), []string{dst}, 1<<20); err == nil {
		t.Fatal("extracted through a directory symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("a file was planted outside the root")
	}
}

func TestExtractEnforcesMaxBytes(t *testing.T) {
	src := t.TempDir()
	write(t, src, map[string]string{"big": strings.Repeat("x", 4096)})
	var buf bytes.Buffer
	if _, err := Write(&buf, []string{src}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := Extract(&buf, []string{t.TempDir()}, 1024); err == nil {
		t.Fatal("an archive past maxBytes was extracted")
	}
}

// A chain of links planted by earlier entries can pass the lexical check
// ("d/e/up/.." looks local) and still resolve outside the root; os.Root
// must refuse the write through it.
func TestRestoreRejectsWriteThroughPlantedSymlinkChain(t *testing.T) {
	dst := t.TempDir()
	root := filepath.Join(dst, "root")
	err := Extract(hostile(t,
		&tar.Header{Name: "0/d/e/", Mode: 0o755, Typeflag: tar.TypeDir},
		&tar.Header{Name: "0/d/e/up", Linkname: "../..", Typeflag: tar.TypeSymlink},
		&tar.Header{Name: "0/p", Linkname: "d/e/up/..", Typeflag: tar.TypeSymlink},
		&tar.Header{Name: "0/p/planted", Mode: 0o644, Typeflag: tar.TypeReg},
	), []string{root}, 1<<20)
	if err == nil {
		t.Fatal("extracted through a planted symlink chain")
	}
	if _, err := os.Stat(filepath.Join(dst, "planted")); err == nil {
		t.Fatal("a file was planted outside the root")
	}
}

func TestRestoreRejectsManifestMismatch(t *testing.T) {
	// hostile's manifest says one root.
	if err := Extract(hostile(t), []string{t.TempDir(), t.TempDir()}, 1<<20); err == nil {
		t.Fatal("an archive for one root was restored into two")
	}
}
