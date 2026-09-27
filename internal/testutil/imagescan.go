package testutil

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os/exec"
	"testing"
)

// TarContains reports whether needle appears in any regular file's content
// within the tar stream r, decompressing gzip- or zstd-compressed entries
// first (an entry's own content, not the outer tar, is what `docker save`
// stores compressed, whichever image store produced it). It underlies
// ImageContains, and is exercised directly by tests that build a synthetic
// tar stream and so need no Docker daemon.
//
// zstd decompression shells out to the `zstd` binary (`zstd -dc`) rather than
// vendoring a Go zstd decoder: the module takes no new dependencies (see
// global-constraints.md), and every environment that runs the `docker`-tagged
// tests that call ImageContains already needs a real Docker daemon, so
// requiring `zstd` on PATH too is a small ask; docker/cli and moby/moby's own
// test suites do the same.
func TarContains(t *testing.T, r io.Reader, needle string) bool {
	t.Helper()
	found := false
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading tar stream: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		br := bufio.NewReaderSize(tr, 1<<16)
		var er io.Reader = br
		var wait func() error
		switch magic, _ := br.Peek(4); {
		case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
			gz, err := gzip.NewReader(br)
			if err != nil {
				t.Fatalf("%s: %v", hdr.Name, err)
			}
			er = gz
		case len(magic) == 4 && magic[0] == 0x28 && magic[1] == 0xb5 && magic[2] == 0x2f && magic[3] == 0xfd:
			er, wait = decompressZstd(t, hdr.Name, br)
		}
		ok, scanErr := streamContains(er, []byte(needle))
		// Reap the zstd -dc child (if any) before acting on scanErr, so a
		// scan failure never leaves it behind.
		if wait != nil {
			if werr := wait(); werr != nil && scanErr == nil {
				scanErr = werr
			}
		}
		if scanErr != nil {
			t.Fatalf("scanning %s: %v", hdr.Name, scanErr)
		}
		found = found || ok
	}
	return found
}

// decompressZstd streams r through `zstd -dc`, returning its stdout and a
// wait function the caller must call, once it has read the returned reader to
// EOF (or hit an error), to reap the child and surface its failure.
func decompressZstd(t *testing.T, name string, r io.Reader) (io.Reader, func() error) {
	t.Helper()
	cmd := exec.Command("zstd", "-dc", "-q")
	cmd.Stdin = r
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("running zstd -dc for %s: %v (install zstd to scan zstd-compressed layers)", name, err)
	}
	return stdout, func() error {
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("%v: %s", err, stderr.String())
		}
		return nil
	}
}

// streamContains reports whether needle occurs in r, reading it in chunks.
func streamContains(r io.Reader, needle []byte) (bool, error) {
	buf := make([]byte, 1<<20)
	var tail []byte
	for {
		n, err := r.Read(buf)
		window := append(tail, buf[:n]...)
		if bytes.Contains(window, needle) {
			return true, nil
		}
		if keep := len(needle) - 1; len(window) > keep {
			tail = append([]byte(nil), window[len(window)-keep:]...)
		} else {
			tail = window
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}

// HasZstd reports whether the zstd binary is on PATH, for tests that need to
// build a zstd-compressed fixture themselves.
func HasZstd() bool {
	_, err := exec.LookPath("zstd")
	return err == nil
}
