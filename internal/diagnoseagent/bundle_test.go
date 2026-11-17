package diagnoseagent

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cmdlog"
)

// testBundle is a manifest of the shape the CLI writes: two parts, a source
// reference and the redaction statement.
func testBundle() Bundle {
	return Bundle{
		Version: BundleVersion,
		RunID:   "update-20261107183012-48213",
		Command: "update",
		Fugaro:  "0.9.5",
		OS:      "darwin/arm64",
		Parts: []BundlePart{
			{Name: "update-20261107183012-48213.log", Bytes: 12, SHA256: sha256Hex([]byte("the log text"))},
			{Name: "doctor.txt", Bytes: 0, SHA256: sha256Hex(nil)},
		},
		Source:    BundleSource{Version: "0.9.5", Commit: strings.Repeat("a", 40), Digest: strings.Repeat("b", 64)},
		Redaction: BundleRedaction,
	}
}

func TestBundleRoundTrip(t *testing.T) {
	want := testBundle()
	var buf bytes.Buffer
	if err := WriteBundle(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBundle(&buf)
	if err != nil {
		t.Fatalf("reading back the manifest it wrote: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the manifest read back is\n%+v\nwant\n%+v", got, want)
	}
}

// The two ends are different binaries: the runner in the job image is whatever
// release built it, so a field it does not know is a manifest it must refuse
// rather than read as though the field were not there.
func TestBundleRejectsUnknownField(t *testing.T) {
	var m map[string]any
	data, err := json.Marshal(testBundle())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["surprise"] = "a field this reader does not know"
	data, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBundle(bytes.NewReader(data)); err == nil {
		t.Fatal("a manifest with an unknown field was read")
	}
}

// The name is the one thing in a manifest that becomes an object key and then
// a path in the job's checkout, so a name that walks out of either is refused
// before anything is built from it.
func TestBundleRejectsTraversalName(t *testing.T) {
	for _, name := range []string{
		"../outside.txt",
		"a/../../outside.txt",
		"/etc/passwd",
		"",
		"  ",
		"nul\x00.txt",
		"new\nline.txt",
		"./log.txt",
		"dir//log.txt",
	} {
		b := testBundle()
		b.Parts = []BundlePart{{Name: name, Bytes: 1, SHA256: sha256Hex([]byte("x"))}}
		if err := b.Validate(); err == nil {
			t.Errorf("the manifest accepted the part name %q", name)
		}
		// The same refusal on the way in from a bucket, not only from a caller
		// that asked Validate itself: this is the path a job reads.
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadBundle(bytes.NewReader(data)); err == nil {
			t.Errorf("a manifest naming the part %q was read", name)
		}
	}
}

// A manifest without a digest is a bundle a job cannot check, and a manifest
// with no parts is a diagnosis with nothing to read: both are refusals rather
// than a reader doing the best it can.
func TestBundleRejectsAPartWithoutADigest(t *testing.T) {
	b := testBundle()
	b.Parts[0].SHA256 = ""
	if err := b.Validate(); err == nil {
		t.Error("a part with no SHA-256 was accepted")
	}
	b = testBundle()
	b.Parts[0].SHA256 = strings.Repeat("A", 64)
	if err := b.Validate(); err == nil {
		t.Error("an upper-case SHA-256 was accepted")
	}
	b = testBundle()
	b.Parts = nil
	if err := b.Validate(); err == nil {
		t.Error("a manifest naming no parts was accepted")
	}
	b = testBundle()
	b.Parts[0].Bytes = -1
	if err := b.Validate(); err == nil {
		t.Error("a part with a negative size was accepted")
	}
	b = testBundle()
	b.Parts = append(b.Parts, b.Parts[0])
	if err := b.Validate(); err == nil {
		t.Error("a part named twice was accepted")
	}
}

func TestBundleRejectsAnUnknownVersion(t *testing.T) {
	b := testBundle()
	b.Version = BundleVersion + 1
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBundle(bytes.NewReader(data)); err == nil {
		t.Fatal("a manifest of an unknown version was read")
	}
	if err := WriteBundle(&bytes.Buffer{}, b); err == nil {
		t.Error("a manifest of an unknown version was written")
	}
}

// The answer is read leniently the other way round — a newer job may add a
// field — but the outcome is the one word the command acts on, so a word it
// does not know is a refusal rather than a guess.
func TestBundleAnswerOutcomes(t *testing.T) {
	for _, outcome := range Outcomes {
		a, err := ReadAnswer(strings.NewReader(`{"outcome":"` + outcome + `","signature":"update the layer check","extra":"ignored"}`))
		if err != nil {
			t.Errorf("reading the answer %q: %v", outcome, err)
			continue
		}
		if a.Outcome != outcome || a.Signature != "update the layer check" {
			t.Errorf("answer %q read as %+v", outcome, a)
		}
	}
	for _, s := range []string{`{"outcome":"a_word_fugaro_does_not_know"}`, `{}`, `not json at all`} {
		if _, err := ReadAnswer(strings.NewReader(s)); err == nil {
			t.Errorf("the answer %s was read", s)
		}
	}
}

// EvidenceNames is what the cloud tier uploads, so it has to be the
// collector's own list and not a second copy of it: a file the collector
// renamed would otherwise go up under a name nothing wrote. The test runs the
// collector rather than reading the constants, because the constants are what
// a copy would be made of.
func TestBundleEvidenceNamesAreTheCollectors(t *testing.T) {
	id := evidenceID()
	ev, err := Collect(t.Context(), id, Options{
		Version: "0.9.5",
		Getenv:  evidenceEnv(t),
		Open: func(cmdlog.ID) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("the log said this\n")), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wrote := evidenceRead(t, ev.EvidenceDir)
	names := EvidenceNames(id.String())
	if len(names) != 4 {
		t.Fatalf("EvidenceNames = %v, want the four evidence files", names)
	}
	for _, name := range names {
		if _, ok := wrote[name]; !ok {
			t.Errorf("EvidenceNames names %q, which the collector did not write", name)
		}
		delete(wrote, name)
	}
	if len(wrote) != 0 {
		t.Errorf("the collector wrote %v, which EvidenceNames does not name", slices.Sorted(maps.Keys(wrote)))
	}
}

// TreeDigest is the tree's content: the same tree digests the same, a changed
// file changes it, and the repository's own directory is not part of it —
// which is what lets the person's checkout and the job's unpacked archive be
// compared at all.
func TestBundleTreeDigestIsTheTreesContent(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n")
	write("docs/guide.md", "# guide\n")
	// A git checkout has one; an unpacked tag archive does not. Two trees of
	// the same source have to digest the same, so it is skipped whole.
	write(".git/HEAD", "ref: refs/heads/main\n")
	write(".git/objects/ab/cdef", "a packfile's worth of nothing\n")
	first, err := TreeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bundleSHA256RE.MatchString(first) {
		t.Fatalf("TreeDigest = %q, want a lower-case hex sha256", first)
	}
	// A second tree, written in the other order, is the same tree.
	other := t.TempDir()
	for _, f := range []struct{ rel, text string }{{"docs/guide.md", "# guide\n"}, {"main.go", "package main\n"}} {
		path := filepath.Join(other, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if again, err := TreeDigest(other); err != nil || again != first {
		t.Errorf("the same tree digested as %q (%v), want %q", again, err, first)
	}
	// A changed file is a different tree, and so is a file that moved.
	write("main.go", "package main // changed\n")
	if changed, err := TreeDigest(dir); err != nil || changed == first {
		t.Errorf("a changed file left the digest at %q (%v)", changed, err)
	}
	// The repository's own directory is skipped, so adding to it changes
	// nothing.
	write("main.go", "package main\n")
	write(".git/index", "a new index\n")
	if after, err := TreeDigest(dir); err != nil || after != first {
		t.Errorf("the digest changed with .git alone: %q (%v), want %q", after, err, first)
	}
}
