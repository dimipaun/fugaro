package selfupgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// good and old are the two releases every test compares: a job image on
// old, a task stamped with good.
const (
	good = "0.5.0"
	old  = "0.4.0"
)

// payload is what the fake release's binary holds: the tests read it back
// from the installed file, so it is bytes rather than something runnable.
var payload = []byte("#!/bin/sh\necho fugaro\n")

// entry is one file of a built archive.
type entry struct {
	h    tar.Header
	body []byte
}

func regular(name string, body []byte) entry {
	return entry{tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}, body}
}

func link(name, target string, typ byte) entry {
	return entry{tar.Header{Name: name, Linkname: target, Mode: 0o777, Typeflag: typ}, nil}
}

// archiveOf builds a tar.gz holding entries, in order.
func archiveOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		if err := tw.WriteHeader(&e.h); err != nil {
			t.Fatalf("writing %s: %v", e.h.Name, err)
		}
		if len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatalf("writing %s: %v", e.h.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// release is a fake GitHub release: checksums.txt and the archive at the
// name this package fetches, with a request log and a knob to break the
// archive.
type release struct {
	*httptest.Server
	version string
	archive []byte
	sum     string

	mu            sync.Mutex
	requests      []string
	archiveStatus int
}

func newRelease(t *testing.T, version string, archive []byte) *release {
	t.Helper()
	sum := sha256.Sum256(archive)
	r := &release{version: version, archive: archive, sum: hex.EncodeToString(sum[:])}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests = append(r.requests, req.URL.Path)
		sum, status := r.sum, r.archiveStatus
		r.mu.Unlock()
		dir := "/v" + version + "/"
		switch req.URL.Path {
		case dir + "checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", sum, archiveName(version))
		case dir + archiveName(version):
			if status != 0 {
				w.WriteHeader(status)
				return
			}
			w.Write(r.archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(r.Server.Close)
	pointAt(t, r.Server)
	return r
}

// mismatch makes checksums.txt state a digest the archive does not have.
func (r *release) mismatch() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sum = strings.Repeat("0", sha256.Size*2)
}

// refuse makes every archive request answer code.
func (r *release) refuse(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archiveStatus = code
}

func (r *release) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

// pointAt makes upgrade fetch from s, restoring the shipped base when the
// test ends.
func pointAt(t *testing.T, s *httptest.Server) {
	t.Helper()
	old := releaseBase
	releaseBase = s.URL + "/"
	t.Cleanup(func() { releaseBase = old })
}

// countingServer is an httptest server answering 404 to everything, for the
// tests that assert nothing was fetched at all.
func countingServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(s.Close)
	return s, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

func TestUpgradeChecksSHA256(t *testing.T) {
	r := newRelease(t, good, archiveOf(t, regular("fugaro", payload)))
	exe, err := upgrade(context.Background(), good, old)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("reading the installed binary: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("installed binary = %q, want %q", got, payload)
	}
	want := []string{"/v" + good + "/checksums.txt", "/v" + good + "/" + archiveName(good)}
	if got := r.paths(); !slices.Equal(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
}

func TestUpgradeRefusesMismatch(t *testing.T) {
	r := newRelease(t, good, archiveOf(t, regular("fugaro", payload)))
	r.mismatch()
	exe, err := upgrade(context.Background(), good, old)
	if err == nil {
		t.Fatalf("upgrade accepted a mismatched archive, installing %q", exe)
	}
	if exe != "" {
		t.Fatalf("exe = %q, want none", exe)
	}
	if !strings.Contains(err.Error(), good) {
		t.Fatalf("error %q does not name the version", err)
	}
}

func TestUpgradeFixedRepository(t *testing.T) {
	if got, want := releaseBaseURL, "https://github.com/"+updatecheck.Repo+"/releases/download/"; got != want {
		t.Fatalf("release base = %q, want %q", got, want)
	}
	s, requests := countingServer(t)
	pointAt(t, s)
	// Nothing a task carries reaches the URL: a version that is not a plain
	// release is refused before a request is made.
	for _, want := range []string{"0.5.0/../evil", "../../evil", "0.5.0?x=1", "0.5.0\n", "", "dev", "1.0", "1.0.0-rc1"} {
		exe, err := upgrade(context.Background(), want, old)
		if err != nil || exe != "" {
			t.Fatalf("upgrade(%q) = %q, %v; want nothing", want, exe, err)
		}
	}
	if n := requests(); n != 0 {
		t.Fatalf("%d requests for versions that are not plain releases", n)
	}
}

func TestUpgradeEEArchive(t *testing.T) {
	r := newRelease(t, good, archiveOf(t, regular("fugaro", payload)))
	if _, err := upgrade(context.Background(), good, old); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	got := r.paths()
	for _, p := range got {
		if strings.Contains(p, "fugaro-core") {
			t.Fatalf("fetched the pure-MIT archive: %s", p)
		}
	}
	want := "/v" + good + "/fugaro_" + good + "_linux_" + runtime.GOARCH + ".tar.gz"
	if !slices.Contains(got, want) {
		t.Fatalf("requests = %v, which do not name %s", got, want)
	}
}

func TestExtractRefusesLinksAndTraversal(t *testing.T) {
	for _, c := range []struct {
		name    string
		entries []entry
	}{
		// Each case carries a good `fugaro` beside the hostile entry, so
		// the refusal is what fails it: without one it would fail on "no
		// binary" whatever the extractor did with the hostile entry.
		{"symlink", []entry{
			regular("fugaro", payload),
			link("LICENSE", "/etc/passwd", tar.TypeSymlink),
		}},
		{"hardlink", []entry{
			regular("fugaro", payload),
			link("LICENSE", "../../etc/passwd", tar.TypeLink),
		}},
		{"absolute", []entry{
			regular("fugaro", payload),
			regular("/tmp/x", payload),
		}},
		{"traversal", []entry{
			regular("fugaro", payload),
			regular("../x", payload),
		}},
		{"no binary", []entry{regular("README.md", payload)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			newRelease(t, good, archiveOf(t, c.entries...))
			exe, err := upgrade(context.Background(), good, old)
			if err == nil {
				t.Fatalf("upgrade extracted %q from an archive with %s", exe, c.name)
			}
			if exe != "" {
				t.Fatalf("exe = %q, want none", exe)
			}
			if !strings.Contains(err.Error(), good) {
				t.Fatalf("error %q does not name the version", err)
			}
		})
	}
}

func TestDownloadCapped(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 4096))
	}))
	t.Cleanup(big.Close)
	if _, err := download(context.Background(), big.URL, 1024); err == nil {
		t.Fatal("a body over the cap was read")
	}
	if _, err := download(context.Background(), big.URL, 8192); err != nil {
		t.Fatalf("a body under the cap was refused: %v", err)
	}
}

func TestUpgradeNeverDowngrades(t *testing.T) {
	r := newRelease(t, good, archiveOf(t, regular("fugaro", payload)))
	for _, want := range []string{old, good} {
		exe, err := upgrade(context.Background(), want, good)
		if err != nil || exe != "" {
			t.Fatalf("upgrade(%q, %q) = %q, %v; want nothing", want, good, exe, err)
		}
	}
	if got := r.paths(); len(got) != 0 {
		t.Fatalf("fetched %v", got)
	}
}

func TestDevNeverUpgrades(t *testing.T) {
	r := newRelease(t, good, archiveOf(t, regular("fugaro", payload)))
	for _, c := range [][2]string{{"dev", good}, {good, "dev"}, {"dev", "dev"}, {"", ""}, {good, ""}, {"", good}} {
		exe, err := upgrade(context.Background(), c[0], c[1])
		if err != nil || exe != "" {
			t.Fatalf("upgrade(%q, %q) = %q, %v; want nothing", c[0], c[1], exe, err)
		}
	}
	if got := r.paths(); len(got) != 0 {
		t.Fatalf("fetched %v", got)
	}
}

func TestUpgradeFailureOneLine(t *testing.T) {
	r := newRelease(t, good, archiveOf(t, regular("fugaro", payload)))
	r.refuse(http.StatusInternalServerError)
	exe, err := upgrade(context.Background(), good, old)
	if err == nil {
		t.Fatalf("upgrade succeeded against a failing release, installing %q", exe)
	}
	if exe != "" {
		t.Fatalf("exe = %q, want none", exe)
	}
	msg := err.Error()
	if strings.Contains(msg, "\n") {
		t.Fatalf("error is not one line: %q", msg)
	}
	if len(msg) > 140 {
		t.Fatalf("error is %d characters: %q", len(msg), msg)
	}
	if !strings.Contains(msg, good) {
		t.Fatalf("error %q does not name the version", msg)
	}
}

func TestInstallReadOnly(t *testing.T) {
	newRelease(t, good, archiveOf(t, regular("fugaro", payload)))
	exe, err := upgrade(context.Background(), good, old)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	fi, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o222 != 0 {
		t.Fatalf("binary mode %v is writable", fi.Mode())
	}
	di, err := os.Stat(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm()&0o222 != 0 {
		t.Fatalf("directory mode %v is writable", di.Mode())
	}
}
