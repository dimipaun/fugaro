package cli

// troubleshoot_bundle_test.go drives the three functions of the cloud tier
// that touch a bucket: the bundle's upload, the answer's read-back and the
// delete. Nothing here starts a run, calls a model or reaches the network: the
// bucket is the file:// driver of the fixture (where an object is a file and
// an assertion can look at it) or the GCS fake, and what is under test is what
// went up, what came back and what was left.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/diagnoseagent"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/srctree"
)

const (
	// tbRunID is the cloud run a test uploads to. It is deliberately not the
	// evidence's own run ID: the manifest carries the LOCAL one, and a test
	// that had the two the same would not notice them being swapped.
	tbRunID = "20261117-060234-777a"
	// tbRunID2 is a second run of the same repository, for the case that needs
	// a bundle of its own.
	tbRunID2 = "20261117-060235-777b"
)

// tbEvidence writes the evidence folder of the shape the collector leaves —
// the four files, the changes file this command writes beside them, and the
// report it writes for a report-carrying outcome, which is NOT part of the
// bundle — and returns the Evidence that points at it.
func tbEvidence(t *testing.T) diagnoseagent.Evidence {
	t.Helper()
	ev := diagnoseagent.Evidence{
		RunID:       "update-20261107183012-48213",
		Command:     "update",
		Version:     "v0.30.0",
		EvidenceDir: t.TempDir(),
		SourceDir:   t.TempDir(),
	}
	body := map[string]string{
		"doctor.txt":            "check 410: the runs bucket answers\n",
		"version.txt":           "version: v0.30.0\ncommit: none: this build carries no commit\n",
		"config.yaml":           "# The effective configuration: this checkout's fugaro.yaml\n",
		troubleshootChangesName: "v0.31.0 fixed the layer check\n",
	}
	for _, name := range diagnoseagent.EvidenceNames(ev.RunID) {
		text, ok := body[name]
		if !ok {
			text = "the failed command's log, redacted\n"
		}
		if err := os.WriteFile(filepath.Join(ev.EvidenceDir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range body {
		if err := os.WriteFile(filepath.Join(ev.EvidenceDir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ev.EvidenceDir, troubleshootReportName), []byte("# a report for the person\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ev.SourceDir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ev
}

// tbFileStore opens the fixture's runs bucket with the driver production uses
// (blobx.Open: no attribute sidecars) and returns the store of one run in it.
func tbFileStore(t *testing.T, x *tsWorld, id string) *runstore.Store {
	t.Helper()
	b, err := blobx.Open(t.Context(), "file://"+x.f.runsDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return runstore.Open(b.Bucket, appSlug, id)
}

// tbInputDir is where a run's bundle lands on the file:// driver: one file per
// object of the run's own prefix.
func tbInputDir(x *tsWorld, id string) string {
	return filepath.Join(x.f.runsDir(), "runs", appSlug, id, "inputs", "diagnose")
}

// tbAnswer writes the two files a diagnosis job delivers — the document the
// command reads and the markdown a person opens, both from the same answer, as
// the runner writes them — the record that names them, and returns the record.
func tbAnswer(t *testing.T, s *runstore.Store, answer diagnoseagent.Answer) *runstore.Record {
	t.Helper()
	doc, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{Version: 1, RunID: s.RunID(), Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeArtifacts, CostUSD: 0.42}
	tbPutArtifact(t, s, rec, diagnoseagent.AnswerName, append(doc, '\n'))
	tbPutArtifact(t, s, rec, diagnoseagent.AnswerMarkdownName, []byte("# Diagnosis\n\n"+answer.Text))
	if err := s.WriteRecord(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// tbPutArtifact writes one delivered file and its row in the record, so that
// the two agree unless a test makes them disagree on purpose.
func tbPutArtifact(t *testing.T, s *runstore.Store, rec *runstore.Record, name string, data []byte) {
	t.Helper()
	if err := s.PutFile(t.Context(), runstore.ArtifactPrefix+name, data, "text/plain"); err != nil {
		t.Fatal(err)
	}
	rec.Artifacts = append(rec.Artifacts, runstore.ArtifactFile{Name: name, Bytes: int64(len(data)), SHA256: sha256Hex(data), ContentType: "text/plain"})
}

func TestTroubleshootUploadBundleWritesEveryPartAndItsHash(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	s := tbFileStore(t, x, tbRunID)
	rec := srctree.Record{Version: ev.Version, Commit: strings.Repeat("a", 40), Kind: "git"}
	if err := troubleshootUploadBundle(t.Context(), s, ev, rec); err != nil {
		t.Fatal(err)
	}
	b, err := troubleshootReadManifest(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	want := append(diagnoseagent.EvidenceNames(ev.RunID), troubleshootChangesName)
	if len(b.Parts) != len(want) {
		t.Fatalf("the manifest names %d parts (%+v), want %d", len(b.Parts), b.Parts, len(want))
	}
	for i, name := range want {
		p := b.Parts[i]
		if p.Name != name {
			t.Errorf("part %d is %q, want %q", i, p.Name, name)
		}
		body, err := os.ReadFile(filepath.Join(ev.EvidenceDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if p.Bytes != int64(len(body)) || p.SHA256 != sha256Hex(body) {
			t.Errorf("the manifest records %s as %d bytes with sha256 %s, and the file is %d bytes with sha256 %s",
				name, p.Bytes, p.SHA256, len(body), sha256Hex(body))
		}
		// The key is the one the runner downloads from — runstore.InputKey of
		// the run's own prefix, over the leaf diagnoseagent.PartObjectName
		// spells — and what is there is the file itself.
		key := runstore.InputKey(s.Prefix(), diagnoseagent.PartObjectName(name))
		at := filepath.Join(x.f.runsDir(), filepath.FromSlash(key))
		onDisk, err := os.ReadFile(at)
		if err != nil {
			t.Errorf("the part %s is not at %s: %v", name, key, err)
			continue
		}
		if !bytes.Equal(onDisk, body) {
			t.Errorf("the object at %s is not the file that was uploaded", key)
		}
	}
	if b.Version != diagnoseagent.BundleVersion || b.RunID != ev.RunID || b.Command != ev.Command || b.FugaroVersion != ev.Version {
		t.Errorf("the manifest describes %+v, want the run %s of %s", b, ev.RunID, ev.Version)
	}
	if b.OS != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("the manifest names the OS %q", b.OS)
	}
	if b.SourceVersion != rec.Version || b.SourceCommit != rec.Commit {
		t.Errorf("the manifest's source is %s at %s, want %+v", b.SourceVersion, b.SourceCommit, rec)
	}
	if !validSHA256(b.SourceTree) {
		t.Errorf("the manifest carries no digest of the source tree (%q), so a job cannot tell which tree it read", b.SourceTree)
	}
	if b.Redaction != diagnoseagent.BundleRedaction || b.Redaction == "" {
		t.Errorf("the manifest's own statement of the redaction is %q", b.Redaction)
	}
	// The bundle and nothing else: the evidence folder also holds the report
	// the command wrote for a person, and no report goes up.
	entries, err := os.ReadDir(tbInputDir(x, tbRunID))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want)+1 {
		t.Errorf("the run's bundle holds %d objects, want the %d parts and the manifest", len(entries), len(want))
	}
	for _, e := range entries {
		if e.Name() == troubleshootReportName {
			t.Error("the report went up with the bundle")
		}
	}
	// A changes file that was never written is not a part: the bundle is the
	// four evidence files, and the manifest says so rather than failing.
	if err := os.Remove(filepath.Join(ev.EvidenceDir, troubleshootChangesName)); err != nil {
		t.Fatal(err)
	}
	s2 := tbFileStore(t, x, tbRunID2)
	if err := troubleshootUploadBundle(t.Context(), s2, ev, rec); err != nil {
		t.Fatal(err)
	}
	b2, err := troubleshootReadManifest(t.Context(), s2)
	if err != nil {
		t.Fatal(err)
	}
	if len(b2.Parts) != 4 {
		t.Errorf("a bundle without a changes file names %d parts (%+v), want 4", len(b2.Parts), b2.Parts)
	}
}

// The name becomes an object key here and a path in the job's checkout there,
// so a name that walks out of either is refused before anything is written —
// and the refusal happens on the first part, so a bundle with a hostile name
// in it leaves nothing behind.
func TestTroubleshootUploadBundleRefusesATraversalName(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	ev.RunID = "../escape"
	s := tbFileStore(t, x, tbRunID)
	err := troubleshootUploadBundle(t.Context(), s, ev, srctree.Record{Version: ev.Version})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "path element") {
		t.Fatalf("uploading a bundle named ../escape = %v, want a user error about the name", err)
	}
	if _, err := os.Stat(filepath.Join(x.f.runsDir(), "runs", appSlug, tbRunID, "inputs")); !os.IsNotExist(err) {
		t.Errorf("the run's inputs hold something after a refused upload: %v", err)
	}
}

// A run's inputs are written once: a second launch under the same run id fails
// on the first part rather than leaving two bundles in one prefix, and the
// refusal names the run and the reason, because "the run already has a
// diagnosis" is something this command can still explain and a bucket that
// would not answer is not.
//
// Mutation (drop the blobx.ErrExists arm): the second upload is reported as a
// remote failure, and only this test notices.
func TestTroubleshootUploadBundleRefusesARunThatAlreadyHasOne(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	s := tbFileStore(t, x, tbRunID)
	rec := srctree.Record{Version: ev.Version}
	if err := troubleshootUploadBundle(t.Context(), s, ev, rec); err != nil {
		t.Fatal(err)
	}
	err := troubleshootUploadBundle(t.Context(), s, ev, rec)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already holds a diagnosis bundle") {
		t.Fatalf("uploading a second bundle to the same run = %v, want a user error saying the run already holds one", err)
	}
}

// An evidence file with nothing in it — the failed command's log is written as
// it is, so a command that logged nothing leaves <run-id>.log empty — is
// refused before the first object goes up. The parts are create-only, so a
// part the manifest would refuse found after the upload would leave the run's
// prefix holding parts and no manifest, and every later attempt would fail on
// the first part with ErrExists and report a bundle the run never got: the
// diagnosis would be unretryable.
func TestTroubleshootUploadBundleRefusesAnEmptyPartBeforeAnythingIsWritten(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	s := tbFileStore(t, x, tbRunID)
	log := filepath.Join(ev.EvidenceDir, diagnoseagent.EvidenceNames(ev.RunID)[0])
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := troubleshootUploadBundle(t.Context(), s, ev, srctree.Record{Version: ev.Version})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("uploading a bundle with an empty part = %v, want a user error saying the part is empty", err)
	}
	// Nothing was written: the run's prefix is as the refusal found it, which
	// is what makes the diagnosis attemptable again.
	if _, err := os.Stat(filepath.Join(x.f.runsDir(), "runs", appSlug, tbRunID, "inputs")); !os.IsNotExist(err) {
		t.Errorf("the run's inputs hold something after a refused upload: %v", err)
	}
	if err := os.WriteFile(log, []byte("the command said nothing, and then this\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := troubleshootUploadBundle(t.Context(), s, ev, srctree.Record{Version: ev.Version}); err != nil {
		t.Fatalf("the same diagnosis with the log filled in = %v, want it uploaded", err)
	}
}

// The bundle is judged against diagnoseagent.BundleMaxBytes here, before the
// first object is written, because that is the bound the runner refuses a
// bundle over before it downloads a part. The collector's own cap is larger —
// a log is kept to its last maxLogBytes — so this is a size the evidence can
// really reach, and uploading past it would leave the person's log in the
// run's bucket for an answer no run could give (D2404).
//
// Mutation (drop the total): the bundle goes up and the run fails on it.
func TestTroubleshootUploadBundleRefusesABundleOverTheBound(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	s := tbFileStore(t, x, tbRunID)
	log := filepath.Join(ev.EvidenceDir, diagnoseagent.EvidenceNames(ev.RunID)[0])
	// Sparse, so the test does not write the megabytes it is about: the size
	// is what the bound is judged on.
	if err := os.Truncate(log, diagnoseagent.BundleMaxBytes+1); err != nil {
		t.Fatal(err)
	}
	err := troubleshootUploadBundle(t.Context(), s, ev, srctree.Record{Version: ev.Version})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "a cloud diagnosis reads") {
		t.Fatalf("uploading a bundle over the bound = %v, want a user error naming the bound", err)
	}
	if _, err := os.Stat(filepath.Join(x.f.runsDir(), "runs", appSlug, tbRunID, "inputs")); !os.IsNotExist(err) {
		t.Errorf("the run's inputs hold something after a refused upload: %v", err)
	}
}

// The ErrExists refusal says what the run's prefix actually holds: a manifest
// means a diagnosis bundle is already there, and parts without one are the
// leftovers of an upload that stopped before it could describe them. Reporting
// the second as the first would send the person looking for a diagnosis no run
// ever made.
func TestTroubleshootUploadBundleNamesWhatThePrefixHolds(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	s := tbFileStore(t, x, tbRunID)
	// One part of an earlier attempt and no manifest beside it. The write is
	// create-only, so this upload meets it on the first part.
	if err := s.PutFile(t.Context(), troubleshootBundleName(diagnoseagent.EvidenceNames(ev.RunID)[0]), []byte("an earlier attempt\n"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	err := troubleshootUploadBundle(t.Context(), s, ev, srctree.Record{Version: ev.Version})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no manifest") {
		t.Fatalf("uploading over the parts of an earlier attempt = %v, want a user error saying the prefix holds parts and no manifest", err)
	}
}

// A changes file that is not a regular file — a link somebody left at a
// predictable name — is not a part of the bundle: the bundle is read from this
// machine and written into a job's checkout, and neither side has business
// following a name out of either place.
//
// Mutation (troubleshootIsFile returning true): the link is offered to the
// upload and the whole diagnosis fails on it.
func TestTroubleshootUploadBundleSkipsAChangesFileThatIsNotOne(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	changes := filepath.Join(ev.EvidenceDir, troubleshootChangesName)
	if err := os.Remove(changes); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ev.EvidenceDir, diagnoseagent.EvidenceNames(ev.RunID)[0]), changes); err != nil {
		t.Skipf("this machine cannot make a symlink: %v", err)
	}
	s := tbFileStore(t, x, tbRunID)
	if err := troubleshootUploadBundle(t.Context(), s, ev, srctree.Record{Version: ev.Version}); err != nil {
		t.Fatalf("uploading a bundle whose changes file is a link = %v, want the link left out", err)
	}
	b, err := troubleshootReadManifest(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range b.Parts {
		if p.Name == troubleshootChangesName {
			t.Errorf("the manifest names %s, which is a link and not a file", troubleshootChangesName)
		}
	}
	if len(b.Parts) != 4 {
		t.Errorf("the bundle names %d parts (%+v), want the four evidence files", len(b.Parts), b.Parts)
	}
}

func TestTroubleshootReadAnswerRejectsAMissingArtifact(t *testing.T) {
	tsSetup(t)
	b := gcpfake.NewGCS(t).Bucket(t, "runs")
	// A run that never finished: there is no record, and there is then nothing
	// the answer could be read against.
	s := runstore.Open(b.Bucket, appSlug, tbRunID)
	if _, err := troubleshootReadAnswer(t.Context(), s); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "result.json") {
		t.Fatalf("reading the answer of a run with no record = %v", err)
	}
	// A record that names the document and not the markdown: a job that
	// answered halfway is a refusal, not an outcome with nothing under it.
	doc := []byte(`{"outcome":"bug"}`)
	if err := s.PutFile(t.Context(), runstore.ArtifactPrefix+diagnoseagent.AnswerName, doc, "application/json"); err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{Version: 1, RunID: tbRunID, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeArtifacts,
		Artifacts: []runstore.ArtifactFile{{Name: diagnoseagent.AnswerName, Bytes: int64(len(doc)), SHA256: sha256Hex(doc), ContentType: "application/json"}}}
	if err := s.WriteRecord(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if _, err := troubleshootReadAnswer(t.Context(), s); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), diagnoseagent.AnswerMarkdownName) {
		t.Fatalf("reading an answer with no markdown = %v", err)
	}
	// The record names both and the bucket no longer holds one of them: the
	// run's artifacts expire with it, and that is said rather than reported as
	// a job that did not answer.
	tbPutArtifact(t, s, rec, diagnoseagent.AnswerMarkdownName, []byte("the markdown\n"))
	if err := s.WriteRecord(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFile(t.Context(), runstore.ArtifactPrefix+diagnoseagent.AnswerMarkdownName); err != nil {
		t.Fatal(err)
	}
	if _, err := troubleshootReadAnswer(t.Context(), s); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), diagnoseagent.AnswerMarkdownName) {
		t.Fatalf("reading an answer whose markdown expired = %v", err)
	}
	// The record's own account of the bytes is what is checked, so an object
	// that changed after the record was written is refused rather than read.
	tbPutArtifact(t, s, rec, diagnoseagent.AnswerMarkdownName, []byte("the markdown\n"))
	if err := s.PutFile(t.Context(), runstore.ArtifactPrefix+diagnoseagent.AnswerMarkdownName, []byte("something else\n"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := troubleshootReadAnswer(t.Context(), s); ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("reading an answer whose bytes disagree with the record = %v", err)
	}
}

func TestTroubleshootReadAnswerReadsTheJobsAnswer(t *testing.T) {
	tsSetup(t)
	b := gcpfake.NewGCS(t).Bucket(t, "runs")
	s := runstore.Open(b.Bucket, appSlug, tbRunID)
	// A credential no list of this machine's holds: only the pattern pass can
	// know it, and it is built from pieces (the repository's no-secret-shaped-
	// literal rule).
	secret := strings.Join([]string{"ghp_", strings.Repeat("A1b2C3d4", 5)}, "")
	rec := tbAnswer(t, s, diagnoseagent.Answer{
		Outcome:   diagnoseagent.OutcomeConfiguration,
		Signature: "update the layer check",
		Text:      "The layer check failed.\n\nA token like " + secret + " was in the log, and `fugaro update` fixes it.\n",
	})
	a, err := troubleshootReadAnswer(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if a.Outcome != diagnoseagent.OutcomeConfiguration || a.Signature != "update the layer check" {
		t.Errorf("the answer read back as %+v", a)
	}
	if !strings.Contains(a.Text, "The layer check failed.") {
		t.Errorf("the answer's text is %q", a.Text)
	}
	if a.CostUSD != rec.CostUSD {
		t.Errorf("the answer's cost is %v, want the record's %v", a.CostUSD, rec.CostUSD)
	}
	// The second look: the answer is model output, and the shapes nobody named
	// come out of it here or nowhere (F20).
	if strings.Contains(a.Text, secret) {
		t.Error("the job's answer reached the command with a credential shape in it")
	}
	if !strings.Contains(a.Text, "[REDACTED]") {
		t.Errorf("the answer's text is %q, want the value replaced by the redaction marker", a.Text)
	}
}

func TestTroubleshootDeleteBundleLeavesTheRecordAndRemovesTheInputs(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	s := tbFileStore(t, x, tbRunID)
	if err := troubleshootUploadBundle(t.Context(), s, ev, srctree.Record{Version: ev.Version}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteRecord(t.Context(), &runstore.Record{Version: 1, RunID: tbRunID, Repo: "acme/app",
		Workflow: "web", Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeArtifacts}); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	troubleshootDeleteBundle(t.Context(), s, &warn)
	if warn.Len() != 0 {
		t.Errorf("the delete warned about a bundle it removed: %s", warn.String())
	}
	// Every object of the bundle is gone, and nothing else under the run's
	// inputs was touched.
	if entries, err := os.ReadDir(tbInputDir(x, tbRunID)); err == nil && len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the bundle is still in the run's prefix: %v", names)
	}
	// The record is not the delete's to touch: the run stays in `fugaro runs`
	// and `fugaro artifacts` reads it as it did before.
	rec, err := s.ReadRecord(t.Context())
	if err != nil || rec.RunID != tbRunID || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("the record after the delete = %+v, %v", rec, err)
	}
}

// The delete is best effort and never the diagnosis's failure: a bucket that
// will not answer is one line on stderr, and the objects stay for the run's own
// expiry to collect (D2405).
func TestTroubleshootDeleteBundleIsBestEffort(t *testing.T) {
	x := tsSetup(t)
	ev := tbEvidence(t)
	s := tbFileStore(t, x, tbRunID)
	if err := troubleshootUploadBundle(t.Context(), s, ev, srctree.Record{Version: ev.Version}); err != nil {
		t.Fatal(err)
	}
	// A manifest nothing can read: the names to delete cannot be had from it,
	// so nothing is deleted and the failure is reported in one line.
	if err := s.PutFile(t.Context(), troubleshootBundleName(diagnoseagent.BundleManifestName), []byte("not a manifest\n"), "application/json"); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	troubleshootDeleteBundle(t.Context(), s, &warn)
	if n := strings.Count(warn.String(), "\n"); n != 1 {
		t.Errorf("the delete printed %d lines, want one: %q", n, warn.String())
	}
	if !strings.Contains(warn.String(), "not removed") {
		t.Errorf("the delete's line does not say what happened: %q", warn.String())
	}
	entries, err := os.ReadDir(tbInputDir(x, tbRunID))
	if err != nil || len(entries) == 0 {
		t.Errorf("the parts were removed without a manifest to name them: %v, %d entries", err, len(entries))
	}
}

// The names the delete prints are read back out of the run's bucket, and the
// manifest's own refusals leave a name free to hold an escape sequence
// (task.InputNameProblem refuses a NUL and a newline, not an ESC): a job that
// rewrote its own manifest must not be able to aim one at this terminal.
//
// Mutation (drop the oneLine on the line): the ESC reaches warn, and only this
// test notices.
func TestTroubleshootDeleteBundleKeepsAnEscapelessNameOutOfTheLine(t *testing.T) {
	x := tsSetup(t)
	s := tbFileStore(t, x, tbRunID)
	// A name the manifest's own rule accepts and a terminal would obey. The
	// part itself is not in the bucket — a job that rewrote its own manifest
	// can name anything — so the delete fails on it and the name is what the
	// failure line would print.
	const hostile = "\x1b[2Jevidence/log.txt"
	data, err := diagnoseagent.WriteBundle(diagnoseagent.Bundle{
		Version:       diagnoseagent.BundleVersion,
		RunID:         "update-20261107183012-48213",
		Command:       "update",
		FugaroVersion: "v0.30.0",
		OS:            runtime.GOOS + "/" + runtime.GOARCH,
		Parts:         []diagnoseagent.BundlePart{{Name: hostile, Bytes: 15, SHA256: strings.Repeat("ab", 32)}},
		SourceVersion: "v0.30.0",
		SourceCommit:  strings.Repeat("a", 40),
		SourceTree:    strings.Repeat("cd", 32),
		Redaction:     diagnoseagent.BundleRedaction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutFile(t.Context(), troubleshootBundleName(diagnoseagent.BundleManifestName), data, "application/json"); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	troubleshootDeleteBundle(t.Context(), s, &warn)
	line := warn.String()
	if line == "" {
		t.Fatal("the delete said nothing about a part it could not remove")
	}
	if strings.ContainsRune(line, 0x1b) {
		t.Errorf("the delete's line carries an escape sequence out of the bucket: %q", line)
	}
	// The name is still named, minus the sequence: dropping the whole line
	// would hide which part the run still holds.
	if !strings.Contains(line, "not removed in full") || !strings.Contains(line, "evidence/log.txt") {
		t.Errorf("the delete's line does not say what happened: %q", line)
	}
}
