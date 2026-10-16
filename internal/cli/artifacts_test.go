package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// sha256Hex is the hash of b, the shape a run record carries (lower-case hex).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// seedArtifactRun writes a finished artifact run: task.json (so the run can be
// located), a record whose artifacts are files, and one object per body. A
// file in files with no body has no object in the bucket at all — the record
// lists it and the bucket does not, which is exactly the shape `ls` must print
// and `get` must report as missing.
func seedArtifactRun(t *testing.T, f *cloudFixture, id string, files []runstore.ArtifactFile, bodies map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	// Ignored as seedRun ignores it: a subtest re-seeds the same run, and
	// task.json is here only so the run can be located at all.
	_ = s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"})
	// The run launched and then ended, which is the shape a finished artifact
	// run has: task.json, launch.json, and a record that says what it
	// delivered.
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	// Ignored for the same reason as task.json above: a subtest re-seeds the
	// run, and what matters is that its record names the newest execution.
	_ = s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now()})
	f.run.SetState(exec, backend.StateSucceeded)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeArtifacts, Artifacts: files}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	for name, body := range bodies {
		putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/artifacts/"+name, body)
	}
}

// artifactRunDir is where get's default destination lands for a run.
func artifactRunDir(id string) string { return id + "-artifacts" }

// dirEntries lists the names under dir, "" when it does not exist.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(ents))
	for i, e := range ents {
		names[i] = e.Name()
	}
	return names
}

// TestArtifactsLsPrintsTheRecordNotTheBucket: ls reads result.json, so an
// artifact whose object is gone is still listed, and it is `get` that says the
// bytes are missing (design artifact-runs.md §9). Mutation caught: an ls that
// heads the bucket instead of the record would drop the missing row.
func TestArtifactsLsPrintsTheRecordNotTheBucket(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	const id = "20261016-090000-abcd"
	present := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.png", Bytes: int64(len(present)), SHA256: sha256Hex(present), ContentType: "image/png"},
		{Name: "frames/b.png", Bytes: 4, SHA256: sha256Hex([]byte("gone")), ContentType: "image/png"},
	}, map[string][]byte{"sizzle.png": present})

	out, _, err := execute(t, "artifacts", "ls", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(out, "sizzle.png") || !strings.Contains(out, "frames/b.png") {
		t.Errorf("ls does not list both recorded artifacts:\n%s", out)
	}
	if !strings.Contains(out, "2") {
		t.Errorf("ls does not say how many the record has:\n%s", out)
	}
	// The record is printed verbatim as JSON.
	jsonOut, _, err := execute(t, "artifacts", "ls", "--json", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	var got []runstore.ArtifactFile
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("ls --json is not the record's list (%v): %s", err, jsonOut)
	}
	if len(got) != 2 || got[0].Name != "sizzle.png" || got[1].SHA256 == "" {
		t.Errorf("ls --json = %+v", got)
	}
	// A bare run ID resolves too, as every other run command's does.
	if _, _, err := execute(t, "artifacts", "ls", id); err != nil {
		t.Errorf("ls by bare run ID: %v", err)
	}
	// The object that is not there is get's to report, not ls's.
	_, _, err = execute(t, "artifacts", "get", appSlug+"/"+id, "frames/b.png")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no such artifact") {
		t.Errorf("get of a recorded-but-absent artifact = %v, want a user error saying there is no such artifact", err)
	}
}

// TestArtifactsGetVerifiesTheHash: the bytes come back, the hash is checked
// against the record, and the file lands under the fresh default directory.
func TestArtifactsGetVerifiesTheHash(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("the rendered bytes")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})

	out, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	dest := filepath.Join(dir, artifactRunDir(id), "sizzle.mp4")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("the artifact was not written to %s (%s): %v", dest, out, err)
	}
	if string(got) != string(body) {
		t.Errorf("the artifact = %q, want %q", got, body)
	}
	// What was downloaded is never opened, executed or unpacked: the recorded
	// type is printed and nothing more.
	if !strings.Contains(out, "video/mp4") || !strings.Contains(out, sha256Hex(body)[:12]) {
		t.Errorf("get does not print the recorded type and the hash:\n%s", out)
	}
	// The bytes are the object's, and the directory the record's.
	if names := dirEntries(t, dir); len(names) != 1 || names[0] != artifactRunDir(id) {
		t.Errorf("the checkout holds %q, want only %s", names, artifactRunDir(id))
	}
}

// TestArtifactsGetWritesNothingOnAMismatch (Review Focus 9): a record whose
// sha256 is not the object's is a refusal, and the destination directory is
// left with nothing in it — not even the temporary file the download streamed
// into. Mutation caught: checking the hash after the download, or renaming
// before checking it.
func TestArtifactsGetWritesNothingOnAMismatch(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	wrong := sha256Hex([]byte("something else"))
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: wrong, ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if err == nil {
		t.Fatal("a mismatching sha256 was accepted")
	}
	for _, want := range []string{wrong, sha256Hex(body)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
	root := filepath.Join(dir, artifactRunDir(id))
	if names := dirEntries(t, root); len(names) != 0 {
		t.Errorf("a mismatch left %q behind, want nothing", names)
	}
}

// TestArtifactsGetRefusesAnUnverifiableRecord: a name is only downloaded when
// the record fixes its hash and its size, because an empty or malformed sha256
// would otherwise turn the check off entirely and write bytes nothing attests
// to. Mutation caught: treating a blank sum as "no check" and a zero size as
// "no cap".
func TestArtifactsGetRefusesAnUnverifiableRecord(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "no-hash.mp4", Bytes: int64(len(body)), ContentType: "video/mp4"},
		{Name: "bad-hash.mp4", Bytes: int64(len(body)), SHA256: "not a sha256", ContentType: "video/mp4"},
		{Name: "no-size.mp4", Bytes: 0, SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"no-hash.mp4": body, "bad-hash.mp4": body, "no-size.mp4": body})

	for _, name := range []string{"no-hash.mp4", "bad-hash.mp4", "no-size.mp4"} {
		_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, name)
		if err == nil {
			t.Errorf("get %s was accepted, want a refusal naming the record's own gap", name)
		}
	}
	if names := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(names) != 0 {
		t.Errorf("an unverifiable record wrote %q, want nothing", names)
	}
}

// TestArtifactsGetRefusesAnEscapingNameFromTheRecord: the record is untrusted
// text (§8.3), so a name that is absolute or walks out of the destination root
// is a refusal — an error the user sees, never a flattened name (design §8.2,
// §9). Mutation caught: joining the stored name onto the root without checking
// it, which is how a record writes outside the directory the user asked for.
func TestArtifactsGetRefusesAnEscapingNameFromTheRecord(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	good := []byte("hello")
	for _, name := range []string{"../outside.mp4", "../../etc/passwd", "/etc/passwd", "a/../../b.mp4", "..", "frames/../../escape.mp4"} {
		t.Run(name, func(t *testing.T) {
			seedArtifactRun(t, f, id, []runstore.ArtifactFile{
				{Name: name, Bytes: 5, SHA256: sha256Hex(good), ContentType: "video/mp4"},
			}, nil)
			_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, name)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("get %q = %v, want a user error", name, err)
			}
			// Nothing was created outside (or inside) the destination root.
			if _, err := os.Stat(filepath.Join(dir, "outside.mp4")); err == nil {
				t.Errorf("get %q wrote outside the destination root", name)
			}
			if names := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(names) != 0 {
				t.Errorf("get %q wrote %q, want nothing", name, names)
			}
		})
	}
}

// TestArtifactsGetRefusesADotFilePathFromTheRecord: the three shapes an
// untrusted record would use to plant a file the user's *next* command runs or
// loads — .git/hooks/pre-commit, .claude/settings.json, .envrc — are refused,
// and so is any other name beginning with a dot (design §9). Mutation caught:
// accepting a leading dot, which the upload sanitiser refuses for the same
// reason.
func TestArtifactsGetRefusesADotFilePathFromTheRecord(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	for _, name := range []string{".git/hooks/pre-commit", ".claude/settings.json", ".envrc", ".hidden.mp4", "./.git/config"} {
		t.Run(name, func(t *testing.T) {
			seedArtifactRun(t, f, id, []runstore.ArtifactFile{
				{Name: name, Bytes: 1, SHA256: sha256Hex([]byte("x")), ContentType: "text/plain"},
			}, nil)
			_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, name)
			if err == nil {
				t.Fatalf("get %q was accepted", name)
			}
			if !strings.Contains(err.Error(), "dot") {
				t.Errorf("the refusal %q does not say why", err)
			}
			// The refusal is a refusal: never a flattened "pre-commit".
			if _, err := os.Stat(filepath.Join(dir, "pre-commit")); err == nil {
				t.Error("a refused name was flattened into a file")
			}
			if names := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(names) != 0 {
				t.Errorf("get %q wrote %q, want nothing", name, names)
			}
		})
	}
}

// TestArtifactsGetWithoutANameRefusesWholesaleOnOneBadEntry: an "all
// artifacts" download that skipped one unretrievable entry would tell the
// owner everything came back when it did not, so one bad name refuses the
// whole download and nothing is written (design §9). Mutation caught: a
// per-file skip that reports success for the rest.
func TestArtifactsGetWithoutANameRefusesWholesaleOnOneBadEntry(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	good := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(good)), SHA256: sha256Hex(good), ContentType: "video/mp4"},
		{Name: ".git/hooks/pre-commit", Bytes: 1, SHA256: sha256Hex([]byte("x")), ContentType: "text/plain"},
	}, map[string][]byte{"sizzle.mp4": good})

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("get with no NAME = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), ".git/hooks/pre-commit") {
		t.Errorf("the refusal %q does not name the offending entry", err)
	}
	if names := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(names) != 0 {
		t.Errorf("a wholesale refusal wrote %q, want nothing (the good entry included)", names)
	}
}

// TestArtifactsLsFlagsANameItCannotRetrieve: ls shows the record as it is —
// the entry is listed with the reason it cannot be fetched, never filtered out
// (design §9). Mutation caught: dropping unretrievable entries from the
// listing, which hides a record that no longer matches what get will do.
func TestArtifactsLsFlagsANameItCannotRetrieve(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	const id = "20261016-090000-abcd"
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: ".git/hooks/pre-commit", Bytes: 1, SHA256: sha256Hex([]byte("x")), ContentType: "text/plain"},
	}, nil)

	out, _, err := execute(t, "artifacts", "ls", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(out, ".git/hooks/pre-commit") {
		t.Errorf("ls filtered out a name it cannot retrieve:\n%s", out)
	}
	if !strings.Contains(out, "dot") {
		t.Errorf("ls does not say why the name cannot be retrieved:\n%s", out)
	}
}

// TestArtifactsLsEscapesANamesControlCharacters: a name out of the record is
// untrusted text, and printed raw an escape sequence in it would rewrite the
// operator's screen (the same rule every other view follows). Mutation caught:
// printing the name without oneLine/cellOneLine.
func TestArtifactsLsEscapesANamesControlCharacters(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	const id = "20261016-090000-abcd"
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "bad\x1b[2Jname.mp4", Bytes: 1, SHA256: sha256Hex([]byte("x")), ContentType: "video/mp4"},
	}, nil)

	out, _, err := execute(t, "artifacts", "ls", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("ls printed a raw escape sequence:\n%q", out)
	}
	if !strings.Contains(out, "name.mp4") {
		t.Errorf("ls does not print the name at all:\n%q", out)
	}
}

// TestArtifactsGetCreatesAFreshDirectoryRatherThanWritingDots: the default
// destination is ./<run>-artifacts/, so a name from the record can never land
// beside the checkout the user is standing in (design §9). Mutation caught: a
// default of "." with only the name checked.
func TestArtifactsGetCreatesAFreshDirectoryRatherThanWritingDots(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "frames/a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "image/png"},
	}, map[string][]byte{"frames/a.png": body})

	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "frames", "a.png")); err == nil {
		t.Error("the artifact was written into the checkout, not into the fresh directory")
	}
	if got, err := os.ReadFile(filepath.Join(dir, artifactRunDir(id), "frames", "a.png")); err != nil || string(got) != string(body) {
		t.Errorf("the nested artifact under the fresh directory = %q, %v", got, err)
	}
	// --out names the fresh directory's PARENT, never the destination itself.
	out := t.TempDir()
	if _, _, err := execute(t, "artifacts", "get", "--out", out, appSlug+"/"+id); err != nil {
		t.Fatalf("get --out: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "frames", "a.png")); err == nil {
		t.Error("--out was used as the destination itself")
	}
	if _, err := os.Stat(filepath.Join(out, artifactRunDir(id), "frames", "a.png")); err != nil {
		t.Errorf("--out is not the parent of the fresh directory: %v", err)
	}
}

// TestArtifactsGetNeedsForceToOverwrite: an existing file is refused without
// --force and is left exactly as it was; --force replaces it. Mutation caught:
// a rename that overwrites whatever was at the destination path.
func TestArtifactsGetNeedsForceToOverwrite(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})
	dest := filepath.Join(dir, artifactRunDir(id), "sizzle.mp4")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("get over an existing file = %v, want a user error naming --force", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "mine" {
		t.Errorf("the refused download changed the existing file: %q", got)
	}
	if _, _, err := execute(t, "artifacts", "get", "--force", appSlug+"/"+id); err != nil {
		t.Fatalf("get --force: %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(body) {
		t.Errorf("get --force left %q, want the artifact's bytes", got)
	}
}

// TestArtifactsGetRefusesASymlinkOrNonRegularDestination: the destination is
// written by a rename, and a symlink there is a path out of the directory the
// user named (a directory at the path is not a file at all). Both are
// refusals, --force or not. Mutation caught: an Lstat-free existence check
// (os.Stat follows a symlink and would call a dangling link absent).
func TestArtifactsGetRefusesASymlinkOrNonRegularDestination(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})
	root := filepath.Join(dir, artifactRunDir(id))
	target := filepath.Join(dir, "target.mp4")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "sizzle.mp4")); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		args := []string{"artifacts", "get", appSlug + "/" + id}
		if force {
			args = []string{"artifacts", "get", "--force", appSlug + "/" + id}
		}
		if _, _, err := execute(t, args...); err == nil {
			t.Errorf("get (--force=%v) wrote through a symlinked destination", force)
		}
		if got, _ := os.ReadFile(target); string(got) != "someone else's" {
			t.Errorf("the symlink's target was written: %q", got)
		}
	}
	if err := os.Remove(filepath.Join(root, "sizzle.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sizzle.mp4"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "artifacts", "get", "--force", appSlug+"/"+id); err == nil {
		t.Error("get wrote over a directory at the destination path")
	}
}

// TestArtifactsGetNeverExecutesWhatItWrites: what comes back is the output of
// a model-driven job, so the command writes it and stops — it is not run, not
// unpacked, and not made executable (design §8.1). Mutation caught: anything
// that opens the file it just wrote.
func TestArtifactsGetNeverExecutesWhatItWrites(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("#!/bin/sh\ntouch ran-the-artifact\n")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.sh", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "text/x-shellscript"},
	}, map[string][]byte{"sizzle.sh": body})

	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id); err != nil {
		t.Fatalf("get: %v", err)
	}
	dest := filepath.Join(dir, artifactRunDir(id), "sizzle.sh")
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != string(body) {
		t.Fatalf("the artifact = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, artifactRunDir(id), "ran-the-artifact")); err == nil {
		t.Error("the artifact was executed")
	}
	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("the downloaded artifact is executable: %v", fi.Mode())
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("the downloaded artifact is group/world readable: %v", fi.Mode())
	}
}

// TestArtifactsGetRefusesOverTheCapBeforeReadingTheBody: the object's size is
// checked against the record's own byte count before the body is read, and an
// over-cap object writes nothing at all — the cap refuses, it never truncates
// (blobx.GetFile, design §8.4). Mutation caught: a limit of 0 ("no cap") or a
// LimitReader that keeps the first limit bytes.
func TestArtifactsGetRefusesOverTheCapBeforeReadingTheBody(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("0123456789")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		// The record says 4 bytes; the object holds 10.
		{Name: "sizzle.mp4", Bytes: 4, SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})

	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id); err == nil {
		t.Fatal("an object larger than the recorded size was accepted")
	}
	root := filepath.Join(dir, artifactRunDir(id))
	if names := dirEntries(t, root); len(names) != 0 {
		t.Errorf("an over-cap download wrote %q, want nothing", names)
	}
}

// TestArtifactsGetRefusesASizeOverTheFormatsCeiling: the record's own byte
// count is the cap, and it is itself bounded — an artifact larger than the
// recipe format's per-artifact ceiling is not one any runner could have
// uploaded, so the record is refused rather than believed (design §6, P6).
func TestArtifactsGetRefusesASizeOverTheFormatsCeiling(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: recipe.MaxOutputBytes + 1, SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})

	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id); err == nil {
		t.Fatal("a record claiming a size over the format's ceiling was accepted")
	}
	if names := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(names) != 0 {
		t.Errorf("an over-ceiling record wrote %q, want nothing", names)
	}
}

// TestArtifactsGetRefusesANameTheRecordDoesNotCarry: only a name the record
// lists can be fetched, because only the record fixes that name's hash and
// size — an unnamed object's bytes would be unverifiable. Mutation caught:
// downloading whatever key the user typed.
func TestArtifactsGetRefusesANameTheRecordDoesNotCarry(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body, "other.mp4": body})

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, "other.mp4")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other.mp4") {
		t.Fatalf("get of a name the record does not carry = %v, want a user error naming it", err)
	}
	if names := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(names) != 0 {
		t.Errorf("an unrecorded name wrote %q, want nothing", names)
	}
}

// TestArtifactsGetRefusesARecordlessRun: a run whose result.json is not there
// has nothing to verify a download against, so get refuses it by name.
func TestArtifactsGetRefusesARecordlessRun(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	const id = "20261016-090000-abcd"
	seedRun(t, f, id, "", "someone@example.com", false)

	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id); err == nil {
		t.Fatal("get on a run with no result.json was accepted")
	}
	if _, _, err := execute(t, "artifacts", "ls", appSlug+"/"+id); err == nil {
		t.Fatal("ls on a run with no result.json was accepted")
	}
}

// TestArtifactsObjectKeyStaysUnderTheRunsPrefix: the key a download reads is
// built from the run's own prefix, artifacts/ and the name, and a name that
// would resolve out of that prefix is refused rather than read — the last
// refusal after artifactNameProblem's, and the one that holds even if a later
// caller skips them. Mutation caught: building the key from the raw name
// without checking where path.Clean leaves it.
func TestArtifactsObjectKeyStaysUnderTheRunsPrefix(t *testing.T) {
	const prefix = "runs/acme-app-dc4d4e59e884fc44/20261016-090000-abcd/"
	// An absolute name is artifactNameProblem's to refuse (a key is a
	// concatenation, so "/etc/passwd" stays inside the prefix here); what
	// this refusal catches is every name path.Clean resolves out of it.
	for _, name := range []string{"../other-run/result.json", "a/../../b.mp4", "..", "../../result.json"} {
		if key, err := artifactObjectKey(prefix, name); err == nil {
			t.Errorf("artifactObjectKey(%q) = %q, want a refusal", name, key)
		}
	}
	key, err := artifactObjectKey(prefix, "frames/a.png")
	if err != nil || key != prefix+"artifacts/frames/a.png" {
		t.Errorf("artifactObjectKey = %q, %v; want the run's own under-artifacts key", key, err)
	}
}

// TestDiagnoseNamesTheArtifacts: an artifact run has no pull request, so its
// deliverable is a file in the runs bucket that no host shows; diagnose names
// each one's path beside Report: — in the record's own order, and the record's
// list only, never a bucket listing (design §9). Mutation caught: printing a
// path built from a bucket listing, which cannot say what the record promised.
func TestDiagnoseNamesTheArtifacts(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "video/mp4"},
		{Name: "frames/a.png", Bytes: 1, SHA256: sha256Hex([]byte("x")), ContentType: "image/png"},
	}, map[string][]byte{"sizzle.mp4": body})

	want := []string{
		"runs/" + appSlug + "/" + id + "/artifacts/sizzle.mp4",
		"runs/" + appSlug + "/" + id + "/artifacts/frames/a.png",
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(d.ArtifactPaths) != len(want) {
		t.Fatalf("diagnose names %q, want %q", d.ArtifactPaths, want)
	}
	for i, p := range want {
		if d.ArtifactPaths[i] != p {
			t.Errorf("artifact path %d = %q, want %q", i, d.ArtifactPaths[i], p)
		}
	}
	text, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	for _, p := range want {
		if !strings.Contains(text, p) {
			t.Errorf("the text diagnosis does not print %s:\n%s", p, text)
		}
	}
	// A run that opens a pull request has no artifact paths at all, and the
	// block is not printed for it.
	f2 := newCloudFixture(t)
	const plain = "20261016-091000-abcd"
	seedRun(t, f2, plain, "", "someone@example.com", true)
	text, _, err = execute(t, "diagnose", plain)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if strings.Contains(text, "Artifacts") {
		t.Errorf("a run with no artifacts printed an artifacts block:\n%s", text)
	}
}

// TestRunsLsShowsArtifactCountOnlyWhenThereAreArtifacts: the count and the
// total bytes are in the row's JSON, and the table grows an column only for a
// listing that has one — so the common case's columns do not change (design
// §9, the rule runview already follows for optional fields).
func TestRunsLsShowsArtifactCountOnlyWhenThereAreArtifacts(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	with := "20261016-090000-abcd"
	without := "20261016-091000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, with, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)) + 1024, SHA256: sha256Hex(body), ContentType: "video/mp4"},
		{Name: "frames/a.png", Bytes: 1024, SHA256: sha256Hex(body), ContentType: "image/png"},
	}, nil)
	// A run whose record is the shape written before this task: no artifacts
	// field at all, which is every run that opens a pull request (Review
	// Focus 8 — the new fields are omitempty, so an old reader sees the row
	// it always saw).
	seedRun(t, f, without, "", "someone@example.com", false)
	writeRecord(t, f, without, &runstore.Record{Version: 1, RunID: without, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady})

	raw, _, err := execute(t, "runs", "ls", "--json", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(raw, `"artifact_count"`); n != 1 {
		t.Errorf("--json carries artifact_count on %d rows, want only the one that has artifacts:\n%s", n, raw)
	}
	got, _ := lsJSON(t, "--since", "0")
	rows := map[string]runview.Row{}
	for _, r := range got.Runs {
		rows[r.RunID] = r
	}
	if r := rows[with]; r.ArtifactCount != 2 || r.ArtifactBytes != int64(len(body))+2048 {
		t.Errorf("the run with artifacts reports %d files / %d bytes, want 2 and %d", r.ArtifactCount, r.ArtifactBytes, int64(len(body))+2048)
	}
	if r := rows[without]; r.ArtifactCount != 0 || r.ArtifactBytes != 0 {
		t.Errorf("the run without artifacts reports %d files / %d bytes, want nothing", r.ArtifactCount, r.ArtifactBytes)
	}
	human, _, err := execute(t, "runs", "ls", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "ARTIFACTS") || !strings.Contains(human, "2 files") {
		t.Errorf("the table does not show the artifact count:\n%s", human)
	}
	// A listing with no artifacts at all keeps its old columns: another
	// project whose runs delivered none.
	plain := newCloudFixture(t)
	seedRun(t, plain, "20261016-091000-abcd", "", "someone@example.com", false)
	empty, _, err := execute(t, "runs", "ls", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(empty, "ARTIFACTS") {
		t.Errorf("an empty listing still prints the artifact column:\n%s", empty)
	}
}
