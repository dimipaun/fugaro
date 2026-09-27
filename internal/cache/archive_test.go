package cache

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

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

func sym(name, target string) *tar.Header {
	return &tar.Header{Name: name, Linkname: target, Typeflag: tar.TypeSymlink}
}

// Links that pass the lexical check when created but resolve outside the
// root once later entries are in: each must be refused and removed.
func TestRestoreRejectsEscapingLinkChains(t *testing.T) {
	cases := map[string][]*tar.Header{
		"ordering":    {sym("0/p", "q/.."), sym("0/q", ".")},
		"parent link": {sym("0/q", "."), sym("0/q/p", "..")},
		"replacement": {
			{Name: "0/a/b/", Mode: 0o755, Typeflag: tar.TypeDir},
			sym("0/q", "a/b"), sym("0/p", "q/.."), sym("0/q", "."),
		},
		"implementer chain": {
			{Name: "0/d/e/", Mode: 0o755, Typeflag: tar.TypeDir},
			sym("0/d/e/up", "../.."), sym("0/p", "d/e/up/.."),
		},
		"grandparent": {sym("0/q", "."), sym("0/r", "q/.."), sym("0/p", "r/..")},
	}
	for name, hdrs := range cases {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "root")
			err := Extract(hostile(t, hdrs...), []string{root}, 1<<20)
			if err == nil || !strings.Contains(err.Error(), "outside") || !errors.Is(err, ErrBadArchive) {
				t.Fatalf("Extract = %v, want an ErrBadArchive naming outside", err)
			}
			if _, err := os.Lstat(filepath.Join(root, "p")); err == nil {
				if target, err := filepath.EvalSymlinks(filepath.Join(root, "p")); err == nil {
					t.Fatalf("root/p survives and resolves to %s", target)
				}
				t.Fatal("root/p survives")
			}
		})
	}
}

// A link created before a later entry is refused is still checked.
func TestRestoreRemovesEscapingLinkOnPartialRestore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	err := Extract(hostile(t, sym("0/q", "."), sym("0/p", "q/.."),
		&tar.Header{Name: "0/fifo", Typeflag: tar.TypeFifo}), []string{root}, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("Extract = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "p")); err == nil {
		t.Fatal("the escaping link survived a partial restore")
	}
	if target, _ := os.Readlink(filepath.Join(root, "q")); target != "." {
		t.Fatalf("a link inside the root was removed: q = %q", target)
	}
}

func TestExtractChargesHeaders(t *testing.T) {
	var hdrs []*tar.Header
	for i := range 3000 {
		hdrs = append(hdrs, &tar.Header{Name: fmt.Sprintf("0/d%d/", i), Mode: 0o755, Typeflag: tar.TypeDir})
	}
	err := Extract(hostile(t, hdrs...), []string{t.TempDir()}, 1<<20)
	if !errors.Is(err, ErrTooLarge) || !errors.Is(err, ErrBadArchive) {
		t.Fatalf("a headers-only archive past the cap: %v", err)
	}
}

func TestWriteChargesHeaders(t *testing.T) {
	src := t.TempDir()
	for i := range 10 {
		if err := os.Mkdir(filepath.Join(src, fmt.Sprint(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Write(io.Discard, []string{src}, 1024); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Write = %v, want ErrTooLarge", err)
	}
}

func TestWriteSkipsPhysicallyEscapingLinks(t *testing.T) {
	src := t.TempDir()
	write(t, src, map[string]string{"f": "x"})
	for link, target := range map[string]string{"q": ".", "p": "q/..", "abs": "/etc", "ok": "f"} {
		if err := os.Symlink(target, filepath.Join(src, link)); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	st, err := writeArchive(&buf, []string{src}, 1<<20)
	if err != nil || st.skippedLinks != 2 {
		t.Fatalf("writeArchive = %+v, %v; want 2 skipped links", st, err)
	}
	dst := t.TempDir()
	if err := Extract(&buf, []string{dst}, 1<<20); err != nil {
		t.Fatalf("the archive Write produced does not restore: %v", err)
	}
	for _, gone := range []string{"p", "abs"} {
		if _, err := os.Lstat(filepath.Join(dst, gone)); err == nil {
			t.Errorf("%s was archived", gone)
		}
	}
	if target, _ := os.Readlink(filepath.Join(dst, "ok")); target != "f" {
		t.Errorf("ok = %q", target)
	}
}

func TestExtractClassifiesErrors(t *testing.T) {
	if err := Extract(strings.NewReader("not zstd"), []string{t.TempDir()}, 1<<20); !errors.Is(err, ErrBadArchive) {
		t.Errorf("corrupt archive: %v, want ErrBadArchive", err)
	}
	src := t.TempDir()
	write(t, src, map[string]string{"big": strings.Repeat("x", 64<<10)})
	var buf bytes.Buffer
	if _, err := Write(&buf, []string{src}, 1<<20); err != nil {
		t.Fatal(err)
	}
	broken := io.MultiReader(bytes.NewReader(buf.Bytes()[:buf.Len()/2]), iotest.ErrReader(errors.New("connection reset")))
	err := Extract(broken, []string{t.TempDir()}, 1<<20)
	if err == nil || errors.Is(err, ErrBadArchive) {
		t.Errorf("I/O error: %v, want an error that is not ErrBadArchive", err)
	}
}
