package diagnoseagent

import (
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

// bundleManifest is one manifest as the launcher writes it: the strict shape
// ReadBundle parses, marshalled here so a field renamed in Go and a field
// renamed in the JSON cannot drift apart unnoticed. WriteBundle is the other
// half of this pair (TestBundleRoundTrip).
func bundleManifest(parts ...BundlePart) Bundle {
	return Bundle{
		Version:       BundleVersion,
		RunID:         "update-20261107183012-48213",
		Command:       "update",
		FugaroVersion: "0.9.5",
		OS:            "linux",
		Parts:         parts,
		SourceVersion: "v0.9.5",
		SourceCommit:  "0123456789abcdef0123456789abcdef01234567",
		SourceTree:    "tree",
		Redaction:     "both passes, as the local tier sends it",
	}
}

// part is one part of those manifests, with a digest of the right shape.
func part(name, sum string) BundlePart {
	return BundlePart{Name: name, Bytes: 15, SHA256: sum}
}

const partSHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// TestBundleRejectsTraversalName: a part's name is what a filesystem path and a
// bucket key are built from (the runner's dest, and BundlePrefix+name here), so
// a name that leaves the run's prefix, is absolute, is empty or carries a NUL
// or a newline is refused when the manifest is read — before the runner builds
// a key or opens a file at a path the manifest chose (D2404). The rule is the
// task's own input-name rule, applied to the whole object name.
//
// Mutation (drop the task.InputNameProblem check): every case here is accepted
// and the escape reaches the runner's filepath.Join unchecked.
func TestBundleRejectsTraversalName(t *testing.T) {
	for _, name := range []string{
		"../escape",
		"a/../../escape",
		"/etc/x",
		"",
		"x\ny",
		"a\x00b",
		"evidence/./log.txt",
		"evidence//log.txt",
	} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			data, err := json.Marshal(bundleManifest(part("evidence/log.txt", partSHA), part(name, partSHA)))
			if err != nil {
				t.Fatal(err)
			}
			b, err := ReadBundle(data)
			if err == nil {
				t.Fatalf("the bundle named the part %q and was read: %+v", name, b)
			}
			if !strings.Contains(err.Error(), "names a part") {
				t.Errorf("the refusal does not name the part: %v", err)
			}
		})
	}
	// The same rule the other way round: a name the rule admits is read, and the
	// object name it is stored under is one spelling.
	data, err := json.Marshal(bundleManifest(part("evidence/log.txt", partSHA)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadBundle(data)
	if err != nil {
		t.Fatalf("a bundle of ordinary names was refused: %v", err)
	}
	if got := PartObjectName(b.Parts[0].Name); got != "diagnose/evidence/log.txt" {
		t.Errorf("PartObjectName = %q, want the run's own leaf %q", got, "diagnose/evidence/log.txt")
	}
}

// TestBundleRejectsItsOwnManifestAsAPart: the manifest is the bundle's account
// of itself and not one of its parts, so the name is refused on both sides —
// the launcher asking WriteBundle, and the job reading one back out of a
// bucket through ReadBundle. A reader that downloaded a part under that name
// would write over the list it is reading.
//
// Mutation (drop the BundleManifestName check): the manifest is written and
// read with the manifest among its own parts, and only this test notices.
func TestBundleRejectsItsOwnManifestAsAPart(t *testing.T) {
	selfNamed := bundleManifest(part(BundleManifestName, partSHA))
	if err := selfNamed.Validate(); err == nil {
		t.Errorf("the manifest accepted %s as one of its own parts", BundleManifestName)
	}
	if _, err := WriteBundle(selfNamed); err == nil {
		t.Errorf("a manifest naming %s as one of its own parts was written", BundleManifestName)
	}
	data, err := json.Marshal(selfNamed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBundle(data); err == nil {
		t.Errorf("a manifest naming %s as one of its parts was read", BundleManifestName)
	}
	// The name is otherwise an ordinary one, so the refusal above is the
	// manifest rule and not the name rule happening to catch it: a part of the
	// same shape under any other name is accepted.
	if _, err := WriteBundle(bundleManifest(part("other.json", partSHA))); err != nil {
		t.Errorf("a manifest naming the part other.json = %v, want it accepted", err)
	}
}

// TestBundleRejectsUnknownField: the strict codec every wire shape in this
// program uses. A manifest from a release that added a field is refused rather
// than read with the field dropped, because the field it drops is one this
// build would have to act on.
func TestBundleRejectsUnknownField(t *testing.T) {
	const yaml = `{"version":1,"run_id":"r","command":"update","fugaro_version":"0.9.5","os":"linux",
		"parts":[{"name":"evidence/log.txt","bytes":15,"sha256":"` + partSHA + `"}],
		"source_version":"v0.9.5","source_commit":"c","source_tree":"tree","redaction":"both",
		"something_new":true}`
	if _, err := ReadBundle([]byte(yaml)); err == nil || !strings.Contains(err.Error(), "something_new") {
		t.Errorf("a manifest with an unknown field was read: %v", err)
	}
}

// TestBundleRejectsAManifestItCannotUse: everything else the manifest can get
// wrong is refused at the read, before a part is downloaded and before the
// model is called — a version this build does not read, no parts at all, the
// same part twice, a part with no bytes, and a digest that is not a SHA-256. A
// manifest a launcher of another release wrote, or one assembled by hand, is
// data this build will not act on a guess about.
func TestBundleRejectsAManifestItCannotUse(t *testing.T) {
	duplicate := bundleManifest(part("evidence/log.txt", partSHA), part("evidence/log.txt", partSHA))
	noParts := bundleManifest()
	zeroBytes := bundleManifest(BundlePart{Name: "evidence/log.txt", Bytes: 0, SHA256: partSHA})
	noDigest := bundleManifest(BundlePart{Name: "evidence/log.txt", Bytes: 15})
	shortDigest := bundleManifest(part("evidence/log.txt", "abc123"))
	upperDigest := bundleManifest(part("evidence/log.txt", strings.ToUpper(partSHA)))

	cases := []struct {
		name string
		man  Bundle
		want string
	}{
		{"no parts", noParts, "names no parts"},
		{"the same part twice", duplicate, "twice"},
		{"a part with no bytes", zeroBytes, "claims 0 bytes"},
		{"a part with no digest", noDigest, "no SHA-256"},
		{"a digest that is not one", shortDigest, "no SHA-256"},
		{"an uppercase digest", upperDigest, "no SHA-256"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.man)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadBundle(data); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("a manifest with %s was read: %v, want a refusal saying %q", tc.name, err, tc.want)
			}
			// The launcher refuses the same manifest before it is uploaded: a
			// bundle one end would hand to the other is a bundle both ends
			// agree about.
			if _, err := WriteBundle(tc.man); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("a manifest with %s was written: %v, want a refusal saying %q", tc.name, err, tc.want)
			}
		})
	}
	// A version this build does not read, which is the one refusal the runner's
	// own stage also pins (internal/runner): the fields are a wire shape both
	// sides hold, so a guessed-at version is a bundle read as something else.
	for _, version := range []int{0, BundleVersion + 1, 99} {
		man := bundleManifest(part("evidence/log.txt", partSHA))
		man.Version = version
		data, err := json.Marshal(man)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadBundle(data); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("a manifest of version %d was read: %v", version, err)
		}
		if _, err := WriteBundle(man); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("a manifest of version %d was written: %v", version, err)
		}
	}
	// Two documents in one object is not a manifest either: the second one is
	// read as no part of this bundle.
	data, err := json.Marshal(bundleManifest(part("evidence/log.txt", partSHA)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBundle(append(data, []byte("\n{}")...)); err == nil {
		t.Error("a manifest holding two JSON documents was read")
	}
	if _, err := ReadBundle([]byte("{not json")); err == nil {
		t.Error("a manifest that is not JSON was read")
	}
}

// TestBundleReadsTheManifestTheLauncherWrites: the accept path, field by field.
// The runner quotes the command, the version and the source's version and
// commit into the prompt and fetches the tree the last two name (D2404), so a
// field silently dropped by the codec would be a diagnosis about the wrong
// command or the wrong release.
func TestBundleReadsTheManifestTheLauncherWrites(t *testing.T) {
	man := bundleManifest(BundlePart{Name: "evidence/log.txt", Bytes: 15, SHA256: partSHA},
		BundlePart{Name: "changes.txt", Bytes: 512, SHA256: strings.Repeat("ab", 32)})
	data, err := json.Marshal(man)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadBundle(data)
	if err != nil {
		t.Fatalf("the launcher's own manifest was refused: %v", err)
	}
	if !reflect.DeepEqual(b, man) {
		t.Errorf("read back %+v, want %+v", b, man)
	}
}

// TestBundleRoundTrip: WriteBundle and ReadBundle are two ends of one format,
// held by two binaries at the same release, so what one writes the other reads
// back field for field — the parts included, since the runner checks each of
// them against what the launcher measured (D2404).
//
// Mutation (drop WriteBundle's Validate): a bundle the job refuses is uploaded
// and the failure moves from the command to the job's log.
func TestBundleRoundTrip(t *testing.T) {
	want := bundleManifest(part("evidence/log.txt", partSHA), part("changes.txt", strings.Repeat("ab", 32)))
	data, err := WriteBundle(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadBundle(data)
	if err != nil {
		t.Fatalf("reading back the manifest it wrote: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the manifest read back is\n%+v\nwant\n%+v", got, want)
	}
}

// TestBundleReadsTheAnswerDocument: the answer as the job wrote it beside the
// markdown (D2402, D2410). Every label Fugaro knows is read with its two other
// fields, and a word it does not know, a field this build does not know and a
// second document in the same object are refused: the outcome is the one word
// the tier acts on, so a word it cannot act on is an answer it must not print.
func TestBundleReadsTheAnswerDocument(t *testing.T) {
	for _, outcome := range Outcomes {
		a, err := ReadAnswerDocument([]byte(`{"outcome":"` + outcome + `","signature":"update the layer check","text":"the cause and the fix"}`))
		if err != nil {
			t.Errorf("reading the answer %q: %v", outcome, err)
			continue
		}
		if a.Outcome != outcome || a.Signature != "update the layer check" || a.Text != "the cause and the fix" {
			t.Errorf("answer %q read as %+v", outcome, a)
		}
	}
	for _, doc := range []string{
		`{"outcome":"a_word_fugaro_does_not_know"}`,
		`{}`,
		`{"outcome":"bug","something_new":true}`,
		`{"outcome":"bug"}
		{}`,
		`not json at all`,
	} {
		if _, err := ReadAnswerDocument([]byte(doc)); err == nil {
			t.Errorf("the answer %s was read", doc)
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
	if !sha256RE.MatchString(first) {
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

// The bundle's own statement of the redaction is what a reader quotes, so it
// has to be a sentence rather than an empty field.
func TestBundleRedactionStatementIsWritten(t *testing.T) {
	b := bundleManifest(part("evidence/log.txt", partSHA))
	b.Redaction = BundleRedaction
	data, err := WriteBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Redaction != BundleRedaction || BundleRedaction == "" {
		t.Errorf("the manifest's own statement of the redaction is %q", got.Redaction)
	}
}
