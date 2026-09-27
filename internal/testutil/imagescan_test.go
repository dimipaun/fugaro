package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// zstdCompress compresses plain with the zstd CLI, the same way a
// zstd-compressed `docker save` layer is produced, so the fixture below is
// real zstd data, not a hand-rolled approximation of it.
func zstdCompress(t *testing.T, plain string) []byte {
	t.Helper()
	cmd := exec.Command("zstd", "-q", "-c")
	cmd.Stdin = bytes.NewReader([]byte(plain))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("zstd -q -c: %v", err)
	}
	return out
}

// gzipCompress compresses plain with compress/gzip.
func gzipCompress(t *testing.T, plain string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(plain)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildSaveTar assembles a docker-save-shaped tar stream: one regular file
// entry per name → content, uncompressed at the tar level (as docker save
// itself is), with each entry's own content compressed however the caller
// chose (or left plain).
func buildSaveTar(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestTarContainsZstdLayer is a non-Docker regression test for the zstd
// branch of TarContains (which ImageContains, the Docker-tagged secret
// scan, is built on): without it, a regression there would make the scan
// silently pass over a zstd-compressed layer instead of failing loudly, the
// same way a vacuous pass would.
func TestTarContainsZstdLayer(t *testing.T) {
	if !HasZstd() {
		t.Skip("zstd is not on PATH")
	}
	const canary = "tarcontains-zstd-canary-9f3c1a"
	present := buildSaveTar(t, map[string][]byte{
		"layer.tar": zstdCompress(t, "prefix "+canary+" suffix"),
	})
	if !TarContains(t, bytes.NewReader(present), canary) {
		t.Error("TarContains missed the canary in a zstd-compressed layer")
	}

	absent := buildSaveTar(t, map[string][]byte{
		"layer.tar": zstdCompress(t, "prefix, no canary here, suffix"),
	})
	if TarContains(t, bytes.NewReader(absent), canary) {
		t.Error("TarContains found the canary in a zstd-compressed layer that doesn't have it")
	}
}

// TestTarContainsZstdEarlyMatch pins the early-match path: the needle sits
// ahead of far more than a pipe buffer's worth (64KB) of decompressed output,
// so zstd -dc is still blocked writing when the scan matches. TarContains must
// stop and reap it rather than deadlock in Wait.
func TestTarContainsZstdEarlyMatch(t *testing.T) {
	if !HasZstd() {
		t.Skip("zstd is not on PATH")
	}
	const canary = "tarcontains-zstd-early-canary-4e1b"
	tarball := buildSaveTar(t, map[string][]byte{
		"layer.tar": zstdCompress(t, canary+strings.Repeat("x", 8<<20)),
	})
	// tarContains, not TarContains: the scan runs off the test goroutine,
	// where t.Fatal must not be called, so its result comes back here.
	type result struct {
		found bool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		found, err := tarContains(bytes.NewReader(tarball), canary)
		done <- result{found, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !r.found {
			t.Error("TarContains missed the canary at the start of a zstd-compressed layer")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("TarContains hung after an early match in a zstd-compressed layer")
	}
}

// TestTarContainsGzipLayer is the same regression test for the gzip branch.
func TestTarContainsGzipLayer(t *testing.T) {
	const canary = "tarcontains-gzip-canary-4b7e2d"
	present := buildSaveTar(t, map[string][]byte{
		"layer.tar": gzipCompress(t, "prefix "+canary+" suffix"),
	})
	if !TarContains(t, bytes.NewReader(present), canary) {
		t.Error("TarContains missed the canary in a gzip-compressed layer")
	}

	absent := buildSaveTar(t, map[string][]byte{
		"layer.tar": gzipCompress(t, "prefix, no canary here, suffix"),
	})
	if TarContains(t, bytes.NewReader(absent), canary) {
		t.Error("TarContains found the canary in a gzip-compressed layer that doesn't have it")
	}
}

// TestTarContainsPlainLayer covers an uncompressed entry (what this
// environment's own containerd image store actually produces for `docker
// save`), so the three shapes TarContains handles are all exercised.
func TestTarContainsPlainLayer(t *testing.T) {
	const canary = "tarcontains-plain-canary-2a91ff"
	present := buildSaveTar(t, map[string][]byte{"layer.tar": []byte("prefix " + canary + " suffix")})
	if !TarContains(t, bytes.NewReader(present), canary) {
		t.Error("TarContains missed the canary in a plain layer")
	}
	absent := buildSaveTar(t, map[string][]byte{"layer.tar": []byte("prefix, no canary here, suffix")})
	if TarContains(t, bytes.NewReader(absent), canary) {
		t.Error("TarContains found the canary in a plain layer that doesn't have it")
	}
}
