package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// requireDistinctNames skips a test that needs two names its filesystem keeps
// apart: a case-insensitive, normalization-insensitive one (APFS, where a mac
// developer runs this suite) stores A.png and a.png as one file, so a fixture
// that writes both into the file-backed runs bucket has one object where the
// test means two. The skip says so instead of failing for the machine.
func requireDistinctNames(t *testing.T, names ...string) {
	t.Helper()
	probe := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(probe, n), nil, 0o600); err != nil {
			t.Skipf("this filesystem cannot hold %q and its folded twin: %v", names[0], err)
		}
	}
	if ents, err := os.ReadDir(probe); err != nil || len(ents) != len(names) {
		t.Skipf("this filesystem stores %q and %q as one file (%d entries, %v)", names[0], names[1], len(ents), err)
	}
}

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
	// The execution reports its run (FUGARO_RUN): next-0.8.5's
	// CheckRunExecution refuses a record naming an execution that does not.
	exec := f.run.StartWithEnv(gcp.JobName(appSlug, "web"), map[string]string{"FUGARO_RUN": appSlug + "/" + id})
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

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if err == nil {
		t.Fatal("an object larger than the recorded size was accepted")
	}
	// The refusal is the announced-size one, made against the RECORD's own
	// count and not against the format's ceiling: the message says the cap
	// that fired was 4. With the ceiling passed as the cap instead, the same
	// object is read to the end and refused for its size afterwards — a
	// different error, and the one this pins.
	if !strings.Contains(err.Error(), "the cap is 4") {
		t.Errorf("the refusal does not name the recorded size as the cap that fired: %v", err)
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

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if err == nil {
		t.Fatal("a record claiming a size over the format's ceiling was accepted")
	}
	// It is refused FOR the ceiling, before the bucket is asked anything: a
	// record this size would otherwise be refused later, for the size the
	// object actually has, which is a different answer to a different
	// question — and it is the answer a caller with the check removed gives.
	if !strings.Contains(err.Error(), "ceiling") {
		t.Errorf("the refusal does not name the format's ceiling: %v", err)
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

// TestArtifactNameIsTheUploadersRule: the name rule is ONE rule with two
// callers — runner.ArtifactName, which the upload sanitiser records with and
// which this command's artifactNameProblem calls — so the two lists cannot
// drift (design §9: "the upload path cannot produce what the retrieval path
// refuses").
//
// The class this pins is the one a copy got wrong: the zero-width characters,
// bidi marks and variation selectors internal/safetext escapes are names the
// uploader accepts (a variation selector in an emoji's filename is one), so a
// retriever that refused them could never hand back an artifact a run
// legitimately delivered. Mutation caught: an artifactNameProblem of its own
// that compares against oneLine/cellOneLine, which refuses those.
func TestArtifactNameIsTheUploadersRule(t *testing.T) {
	// Names the uploader accepts, exactly as given: the retrieval rule must
	// accept them too, because the record carries them unchanged.
	accepted := []string{
		"sizzle.mp4", "clips/a.png", "clips/.hidden.png", "clips/.hidden/a.png",
		"a|b`c.png", "café.png",
		"a​b.png",    // zero-width space
		"a‍b.png",    // zero-width joiner
		"smoke️.png", // variation selector, as an emoji filename carries
		"a­b.png",    // soft hyphen
		"a b.png",    // line separator
		"a‎b.png",    // LRM
		"a\xffb.png", // invalid UTF-8: a byte sequence the uploader does not refuse
	}
	for _, name := range accepted {
		clean, problem := runner.ArtifactName(name)
		if problem != "" || clean != name {
			t.Errorf("runner.ArtifactName(%q) = %q, %q; want it accepted unchanged", name, clean, problem)
			continue
		}
		if p := artifactNameProblem(name); p != "" {
			t.Errorf("artifactNameProblem(%q) = %q, but the uploader records that name: an artifact a run delivered could never be fetched", name, p)
		}
	}
	// Names the uploader refuses: the retriever refuses them too.
	refused := []string{"", "../x.png", "/etc/passwd", "..", "a/..", ".git/hooks/pre-commit",
		".envrc", "a\x00b.png", "a\tb.png", "a\nb.png", "a\x1b[31mb.png", "a‮b.png",
		// A backslash separates path elements on Windows: refused by the one
		// shared rule, so both sides refuse it.
		"a\\b.png", "frames\\..\\..\\x.png",
		strings.Repeat("a", 201)}
	for _, name := range refused {
		if _, problem := runner.ArtifactName(name); problem == "" {
			t.Errorf("runner.ArtifactName(%q) accepts a name, so this test's premise is wrong", name)
			continue
		}
		if p := artifactNameProblem(name); p == "" {
			t.Errorf("artifactNameProblem(%q) accepts a name the uploader refuses", name)
		}
	}
	// The one deliberate difference, and the only one: a name the uploader
	// CLEANS before recording (sanitiseName returns path.Clean) is a name the
	// record cannot carry, so the retriever refuses it rather than reading a
	// record no runner could have written. Nothing the uploader can record is
	// in this list.
	for _, name := range []string{"clips/./a.png", "a//b.png", "clips/../a.png"} {
		if _, problem := runner.ArtifactName(name); problem != "" {
			t.Errorf("runner.ArtifactName(%q) refuses a name it cleans, so this test's premise is wrong", name)
			continue
		}
		if p := artifactNameProblem(name); p == "" {
			t.Errorf("artifactNameProblem(%q) accepts an unclean name: the uploader records only the clean form", name)
		}
	}
}

// TestArtifactsGetFetchesTheNamesTheUploaderRecords is the same agreement, end
// to end and over the wire: every one of those names is downloaded, bytes and
// all, so a retriever that refused one of them fails here rather than in a
// user's hands.
func TestArtifactsGetFetchesTheNamesTheUploaderRecords(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	// Names a run can legitimately deliver and a first draft of this command
	// refused: the spoofing class safetext escapes (an invalid UTF-8 byte is
	// in the rule test above and not here, because the file:// driver the
	// fixture uses does not round-trip such an object key — a limit of the
	// test bucket, not of the rule).
	names := []string{"a​b.png", "smoke️.png", "a­b.png", "café.png", "clips/.hidden.png"}
	body := []byte("rendered")
	files := make([]runstore.ArtifactFile, 0, len(names))
	bodies := map[string][]byte{}
	for i, name := range names {
		// Each name's own bytes, so a mixed-up download is visible.
		b := append([]byte{}, body...)
		b = append(b, byte('0'+i))
		files = append(files, runstore.ArtifactFile{Name: name, Bytes: int64(len(b)), SHA256: sha256Hex(b), ContentType: "image/png"})
		bodies[name] = b
	}
	seedArtifactRun(t, f, id, files, bodies)

	out, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("get of names the uploader records: %v (%s)", err, out)
	}
	for _, name := range names {
		got, err := os.ReadFile(filepath.Join(dir, artifactRunDir(id), filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("the artifact %q was not written: %v", name, err)
			continue
		}
		if string(got) != string(bodies[name]) {
			t.Errorf("the artifact %q = %q, want %q", name, got, bodies[name])
		}
	}
}

// TestArtifactsGetRefusesADirectoryThatLeavesTheRoot: a symlinked directory
// planted under the destination root is the same escape as a name with a ".."
// in it, and it is refused BEFORE anything is created: the guarantee is that
// the command writes only under the fresh root it made, which has to hold for
// the directories it creates on the way too. Mutation caught: the MkdirAll
// before the check, which leaves the outside directory behind.
func TestArtifactsGetRefusesADirectoryThatLeavesTheRoot(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "frames/sub/a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "image/png"},
	}, map[string][]byte{"frames/sub/a.png": body})
	root := filepath.Join(dir, artifactRunDir(id))
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(outside)
	if err != nil || !fi.IsDir() {
		if err := os.Mkdir(outside, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A directory the user (or an earlier run) left under the root: the name
	// walks through it, and it points out of the root.
	if err := os.Symlink(outside, filepath.Join(root, "frames")); err != nil {
		t.Fatal(err)
	}

	_, _, err = execute(t, "artifacts", "get", appSlug+"/"+id, "frames/sub/a.png")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("get through a symlinked directory = %v, want a user error", err)
	}
	if entries := dirEntries(t, outside); len(entries) != 0 {
		t.Errorf("the refusal created %q outside the destination root", entries)
	}
	if _, err := os.Stat(filepath.Join(outside, "sub")); err == nil {
		t.Error("get created a directory outside the destination root")
	}
	// The direct check, since it is the one that has to run first: a parent
	// that does not exist yet under a real root is inside it; the same path
	// under a link is not.
	if err := parentUnderRoot(root, filepath.Join(root, "new", "deep")); err != nil {
		t.Errorf("parentUnderRoot on a missing directory under the root = %v, want it accepted", err)
	}
	if err := parentUnderRoot(root, filepath.Join(root, "frames", "sub")); err == nil {
		t.Error("parentUnderRoot accepted a directory that resolves outside the root")
	}
	// And what it checks is the resolved path, not the text: the root itself
	// reached through a link is the root's business, not the name's.
	if err := underArtifactRoot(root, root); err != nil {
		t.Errorf("underArtifactRoot(root, root) = %v, want it accepted", err)
	}
}

// TestArtifactsResolvesRepoAndRunLikeRunsLs: --repo is what a bare run ID
// needs (that is the only reason the flag exists), a RUN that already names a
// repository has to agree with it, and a bare ID that is not one is refused
// rather than turned into an object name.
func TestArtifactsResolvesRepoAndRunLikeRunsLs(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})

	// A bare run ID with --repo: both subcommands.
	if out, _, err := execute(t, "artifacts", "ls", "--repo", "acme/app", id); err != nil || !strings.Contains(out, "sizzle.mp4") {
		t.Errorf("ls --repo acme/app %s = %v (%s)", id, err, out)
	}
	if _, _, err := execute(t, "artifacts", "get", "--repo", "acme/app", id, "sizzle.mp4"); err != nil {
		t.Errorf("get --repo acme/app %s: %v", id, err)
	}
	// A RUN that carries a slug, with the matching --repo: the same run.
	if _, _, err := execute(t, "artifacts", "ls", "--repo", "acme/app", appSlug+"/"+id); err != nil {
		t.Errorf("ls --repo acme/app %s/%s: %v", appSlug, id, err)
	}
	// A RUN that carries a different repository than --repo names is refused,
	// naming both: the slug is part of the object key, so a mismatch would
	// read another repository's run.
	other := mustSlug("gitlab", "acme/app")
	_, _, err := execute(t, "artifacts", "ls", "--repo", "acme/app", other+"/"+id)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), "acme-app") {
		t.Errorf("ls of %s with --repo acme/app = %v, want a user error naming both repositories", other, err)
	}
	// A bare ID that is not a run ID never becomes part of an object name.
	_, _, err = execute(t, "artifacts", "get", "--repo", "acme/app", "not-a-run-id")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "run ID") {
		t.Errorf("get of a malformed run ID = %v, want a user error", err)
	}
}

// TestArtifactsGetRefusesARecordedSizeTheObjectDoesNotHave: the record's own
// byte count is checked as well as used as the cap — an object smaller than
// the record says, with the hash the record carries for those smaller bytes,
// is a record whose two fields disagree with each other's premise, and it is
// refused with the file removed again rather than written. Mutation caught:
// passing the recorded size to blobx.GetFile as the cap only (an upper bound,
// which a smaller object satisfies).
func TestArtifactsGetRefusesARecordedSizeTheObjectDoesNotHave(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		// 100 bytes recorded, 5 in the bucket, and the hash of those 5: the
		// hash matches and the size does not.
		{Name: "sizzle.mp4", Bytes: 100, SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("get of a record whose size and hash disagree = %v, want a user error", err)
	}
	for _, want := range []string{"100", "5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
	if names := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(names) != 0 {
		t.Errorf("the refusal left %q behind, want nothing", names)
	}
}

// TestArtifactsGetFetchesAnEmptyArtifact: bytes == 0 is a size a run records
// (result.schema.json: "bytes": {"minimum": 0}), and a render that writes an
// empty file matched by a glob records exactly that — {bytes: 0, sha256: the
// digest of ""} — so refusing it would make a file the run delivered
// impossible to fetch. ls lists it like any other (not flagged, 0 B) and get
// writes a 0-byte file, verified against the record's digest of nothing.
// Mutation caught: refusing bytes <= 0 instead of bytes < 0.
func TestArtifactsGetFetchesAnEmptyArtifact(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("rendered")
	empty := []byte{}
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "frames/empty.png", Bytes: 0, SHA256: sha256Hex(empty), ContentType: "image/png"},
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"frames/empty.png": empty, "sizzle.mp4": body})

	// ls prints the record as it is: an empty artifact is an artifact, not an
	// entry this command cannot retrieve.
	out, _, err := execute(t, "artifacts", "ls", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(out, "frames/empty.png") || strings.Contains(out, "cannot be retrieved") {
		t.Errorf("ls does not list an empty artifact as an ordinary one:\n%s", out)
	}
	if !strings.Contains(out, "2 artifacts recorded") {
		t.Errorf("ls does not count the empty artifact:\n%s", out)
	}
	// And get hands it back, empty, beside the one with bytes in it.
	getOut, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("get: %v (%s)", err, getOut)
	}
	got, err := os.ReadFile(filepath.Join(dir, artifactRunDir(id), "frames", "empty.png"))
	if err != nil {
		t.Fatalf("the empty artifact was not written: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the empty artifact = %q, want no bytes", got)
	}
	if fi, err := os.Lstat(filepath.Join(dir, artifactRunDir(id), "frames", "empty.png")); err != nil || fi.Size() != 0 {
		t.Errorf("the empty artifact's size = %v, %v; want a 0-byte file", fi, err)
	}
	if !strings.Contains(getOut, "0 B") {
		t.Errorf("get does not print the empty artifact's size:\n%s", getOut)
	}
	// The empty artifact alone, by name, is retrievable too — into a
	// destination of its own, since the two files above are already there.
	alone := t.TempDir()
	if _, _, err := execute(t, "artifacts", "get", "--out", alone, appSlug+"/"+id, "frames/empty.png"); err != nil {
		t.Errorf("get of an empty artifact by name: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(alone, artifactRunDir(id), "frames", "empty.png")); err != nil || len(got) != 0 {
		t.Errorf("the empty artifact fetched by name = %q, %v; want no bytes", got, err)
	}
	if _, err := os.Stat(filepath.Join(alone, artifactRunDir(id), "sizzle.mp4")); err == nil {
		t.Error("get NAME downloaded an artifact the user did not name")
	}
}

// TestArtifactsGetNeverRemovesWhatItDidNotWrite: a refusal after the bytes
// arrived must not touch the operator's own file at the destination. The
// destination is replaced by a rename, so a size check that runs AFTER the
// rename has to delete the file it just put there — and with --force it is
// deleting whatever was at that path before, which this command never wrote
// and has no business removing. Mutation caught: comparing the size (or the
// hash) outside blobx, after the temp file was renamed over the destination.
func TestArtifactsGetNeverRemovesWhatItDidNotWrite(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	mine := []byte("mine, and mine to keep")
	// The record says 100 bytes with the hash of the 5-byte object: the hash
	// matches and the size does not, so the bytes are verified and refused.
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: 100, SHA256: sha256Hex(body), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})
	dest := filepath.Join(dir, artifactRunDir(id), "sizzle.mp4")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, mine, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, force := range []bool{false, true} {
		args := []string{"artifacts", "get", appSlug + "/" + id}
		if force {
			args = []string{"artifacts", "get", "--force", appSlug + "/" + id}
		}
		if _, _, err := execute(t, args...); err == nil {
			t.Errorf("get (--force=%v) accepted a record whose size disagrees with the object", force)
		}
		got, err := os.ReadFile(dest)
		if err != nil || string(got) != string(mine) {
			t.Errorf("get (--force=%v) changed the operator's file at %s: %q, %v", force, dest, got, err)
		}
	}
	// A hash mismatch is the same rule, and it is checked on its own: the
	// operator's file is put back first, so a regression that only touches
	// the hash path is not hidden by the size phase above.
	if err := os.WriteFile(dest, mine, 0o600); err != nil {
		t.Fatal(err)
	}
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex([]byte("other bytes")), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": body})
	if _, _, err := execute(t, "artifacts", "get", "--force", appSlug+"/"+id); err == nil {
		t.Error("get --force accepted a mismatching sha256")
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != string(mine) {
		t.Errorf("a hash mismatch changed the operator's file at %s: %q, %v", dest, got, err)
	}
}

// TestArtifactsGetRefusesDuplicateNames: a record that names one artifact
// twice, or that names two artifacts differing only by case, is refused
// before any download. This is the retriever's half of the uploader's "no
// duplicate" refusal (design §8.2), asked with the same key (NFC plus full
// case folding): a second download of
// the same name writes one path twice, and the case-folded pair does it on a
// filesystem that ignores case (APFS, NTFS) — where checkArtifactDest has
// already proved both destinations before the first byte is read, so the
// second write lands on the first file's path with --force never consulted.
// Mutations caught: dropping the duplicate check (two success lines for one
// artifact), and checking exact names only.
func TestArtifactsGetRefusesDuplicateNames(t *testing.T) {
	body := []byte("one")
	for _, tc := range []struct {
		name  string
		files []runstore.ArtifactFile
		quote []string // every name the refusal must quote
	}{
		{
			name: "the same name twice",
			files: []runstore.ArtifactFile{
				{Name: "frames/a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
				{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
				{Name: "frames/a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
			},
			quote: []string{`"frames/a.png"`},
		},
		{
			name: "names that differ only by case",
			files: []runstore.ArtifactFile{
				{Name: "A.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
				{Name: "a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
			},
			quote: []string{`"A.png"`, `"a.png"`},
		},
		{
			// The pairs cases.Fold is not idempotent on: U+13A0 and U+AB70 are
			// the same Cherokee letter, so a key that folds them to each other
			// gives this record two keys and the retriever writes one file
			// twice while reporting two arrivals.
			name: "Cherokee case pairs",
			files: []runstore.ArtifactFile{
				{Name: "Ꭰ.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
				{Name: "ꭰ.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
			},
			quote: []string{`"Ꭰ.png"`, `"ꭰ.png"`},
		},
		{
			// The mark-order half: a precomposed letter plus a mark, and the
			// same name spelled with the marks the other way round and the
			// letter in the other case. One file on APFS, and a key that
			// normalizes only before the fold gave them two.
			name: "names that differ only in the order of their marks",
			files: []runstore.ArtifactFile{
				{Name: "ǰ̣.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
				{Name: "J̣̌.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
			},
			quote: []string{"ǰ̣.png", "J̣̌.png"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := t.TempDir()
			t.Chdir(dir)
			const id = "20261016-090000-abcd"
			bodies := map[string][]byte{}
			for _, a := range tc.files {
				bodies[a.Name] = body
			}
			seedArtifactRun(t, f, id, tc.files, bodies)
			for _, force := range []bool{false, true} {
				args := []string{"artifacts", "get", appSlug + "/" + id}
				if force {
					args = []string{"artifacts", "get", "--force", appSlug + "/" + id}
				}
				_, _, err := execute(t, args...)
				if err == nil {
					t.Fatalf("get (--force=%v) accepted a record that names one artifact twice", force)
				}
				if code := ExitCode(err); code != ExitUserError {
					t.Errorf("get (--force=%v) exit code = %d, want %d (a bad record is the user's to read)", force, code, ExitUserError)
				}
				for _, q := range tc.quote {
					if !strings.Contains(err.Error(), q) {
						t.Errorf("get (--force=%v) refusal does not quote %s: %v", force, q, err)
					}
				}
			}
			if got := dirEntries(t, artifactRunDir(id)); len(got) != 0 {
				t.Errorf("a duplicate-name record downloaded something: %v", got)
			}
		})
	}
}

// TestArtifactsGetStillFetchesADuplicatedNameByName: the wholesale download
// refuses a case-folded pair — the two would be one file on APFS — but the
// user can still name one of them and get exactly that file, because the
// refusal is about writing two artifacts to one path and not about the
// record being unreadable.
func TestArtifactsGetStillFetchesADuplicatedNameByName(t *testing.T) {
	// Two names that differ only in case are one name on a case-insensitive
	// filesystem, and this test writes them into a file-backed bucket, which
	// APFS merges into one object: the record would then carry two entries
	// with one object's bytes and the assertion would be about the machine.
	// The point is the named fetch not being routed through the wholesale
	// collision check, so the test needs a filesystem that keeps them apart.
	requireDistinctNames(t, "A.png", "a.png")
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	upper, lower := []byte("UPPER"), []byte("lower")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "A.png", Bytes: int64(len(upper)), SHA256: sha256Hex(upper)},
		{Name: "a.png", Bytes: int64(len(lower)), SHA256: sha256Hex(lower)},
	}, map[string][]byte{"A.png": upper, "a.png": lower})
	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, "A.png"); err != nil {
		t.Fatalf("get of a named artifact whose record also carries a case-folded twin: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, artifactRunDir(id), "A.png"))
	if err != nil || string(got) != string(upper) {
		t.Errorf("the named artifact = %q, %v; want %q", got, err, upper)
	}
}

// TestArtifactsGetRefusesANamedFetchOfADoublyRecordedName: naming one of two
// entries that share a name is refused for what it is — the record names that
// artifact twice — and not reported as a name the run never recorded, which is
// what a single len(chosen) != 1 check says. The message points at the record,
// not at `artifacts ls` (which prints the name, twice), and nothing is
// downloaded. Mutation caught: folding "no such name" and "recorded twice"
// into one check.
func TestArtifactsGetRefusesANamedFetchOfADoublyRecordedName(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("one")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
		{Name: "a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{"a.png": body})

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, "a.png")
	if err == nil {
		t.Fatal("naming an artifact the record lists twice was accepted")
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("exit code = %d, want %d (a bad record is the user's to read)", code, ExitUserError)
	}
	msg := err.Error()
	if strings.Contains(msg, "recorded no artifact named") {
		t.Errorf("a doubly-recorded name is reported as a name the run never recorded: %v", err)
	}
	if !strings.Contains(msg, `"a.png"`) || !strings.Contains(msg, "twice") {
		t.Errorf("the refusal does not quote the name and say it is recorded twice: %v", err)
	}
	if names := dirEntries(t, artifactRunDir(id)); len(names) != 0 {
		t.Errorf("a named fetch of a doubly-recorded name wrote %q, want nothing", names)
	}
	// A name the record really does not carry still gets the missing-name
	// message, pointing at the listing.
	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, "b.png"); err == nil || !strings.Contains(err.Error(), "recorded no artifact named") {
		t.Errorf("get of a name the record does not carry = %v, want the missing-name refusal", err)
	}
}

// TestArtifactsLsEscapesAProbeWarningsTab: the probe's warning is text out of
// the record, and it is interpolated into a tabwriter row — where a literal tab
// opens a column of its own, which is why every other record-derived cell in
// that table is table-escaped. Mutation caught: probeCell's result written into
// the row without cellOneLine.
func TestArtifactsLsEscapesAProbeWarningsTab(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "video/mp4",
			Probe: &runstore.ProbeResult{Ran: true, ExitCode: 0, Warning: "the tab\there"}},
	}, map[string][]byte{"sizzle.mp4": body})

	out, _, err := execute(t, "artifacts", "ls", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if strings.Contains(out, "\t") {
		t.Errorf("ls printed a raw tab in a table row, opening a column of its own:\n%q", out)
	}
	if !strings.Contains(out, "exit 0 (the tab?here)") {
		t.Errorf("the probe's warning is not printed table-escaped:\n%q", out)
	}
}

// TestArtifactsGetRefusesEveryOneFileFoldCollision: the collision rule is
// "these two names are one file", and it has to be order-independent: the
// entries are the record's, and a record that lists the names in the order that
// hides a pair defeats a check that keeps one name per key and overwrites it.
// The pairs below are the ones a lower-cased key gets wrong or misses —
// U+0130 (İ), whose lower case is 'i' but which EqualFold does NOT equate with
// it, so an entry like it evicts the entry it collided with and the real pair
// goes unnoticed; U+212A (the Kelvin sign) and U+017F (long s), which fold to
// k/K and s/S and which a lower-cased key misses on one side; and the NFC/NFD
// pair macOS stores as one file, which no case folding equates at all.
//
// Each pair is listed in BOTH orders, and the İ case needs the third name that
// makes the eviction visible: without it the record is refused for the
// (İ, i) pair's key collision only by accident.
func TestArtifactsGetRefusesEveryOneFileFoldCollision(t *testing.T) {
	body := []byte("one")
	for _, tc := range []struct {
		name  string
		names []string
	}{
		{"U+0130 between a pair that folds", []string{"I.png", "\u0130.png", "i.png"}},
		{"the same, listed the other way", []string{"i.png", "\u0130.png", "I.png"}},
		{"the Kelvin sign", []string{"K.png", "\u212a.png"}},
		{"the Kelvin sign, listed the other way", []string{"\u212a.png", "K.png"}},
		{"long s", []string{"s.png", "\u017f.png"}},
		{"long s, listed the other way", []string{"\u017f.png", "s.png"}},
		{"NFC and NFD, as macOS stores them", []string{"caf\u00e9.png", "cafe\u0301.png"}},
		{"NFC and NFD, listed the other way", []string{"cafe\u0301.png", "caf\u00e9.png"}},
		// FULL case folding, which is what a filesystem that folds at all
		// does: one rune, two letters. Simple folding (strings.EqualFold)
		// keeps each of these pairs apart, so a key built from it accepts
		// the record and the download then writes the second artifact over
		// the first while reporting both.
		{"sharp s", []string{"stra\u00dfe.png", "strasse.png"}},
		{"sharp s, listed the other way", []string{"strasse.png", "stra\u00dfe.png"}},
		{"capital sharp s", []string{"STRA\u1e9eE.png", "STRASSE.png"}},
		{"fi ligature", []string{"\ufb01le.png", "file.png"}},
		{"fl ligature", []string{"\ufb02ag.png", "flag.png"}},
		{"final sigma", []string{"\u03c2.png", "\u03c3.png"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := t.TempDir()
			t.Chdir(dir)
			const id = "20261016-090000-abcd"
			files := make([]runstore.ArtifactFile, len(tc.names))
			bodies := map[string][]byte{}
			for i, n := range tc.names {
				files[i] = runstore.ArtifactFile{Name: n, Bytes: int64(len(body)), SHA256: sha256Hex(body)}
				bodies[n] = body
			}
			seedArtifactRun(t, f, id, files, bodies)

			_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
			if err == nil {
				t.Fatalf("get accepted a record whose names are one file on a case-insensitive, normalizing filesystem: %v", tc.names)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit code = %d, want %d (a bad record is the user's to read)", code, ExitUserError)
			}
			if names := dirEntries(t, artifactRunDir(id)); len(names) != 0 {
				t.Errorf("a one-file record downloaded %q, want nothing", names)
			}
		})
	}
	// The pairs the rule must NOT refuse: a case-insensitive filesystem keeps
	// these apart on every platform Go folds for, so refusing them would
	// refuse a record no run could have avoided.
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	kept := []string{"\u0130.png", "i.png"}
	files := make([]runstore.ArtifactFile, 0, len(kept))
	bodies := map[string][]byte{}
	for _, n := range kept {
		files = append(files, runstore.ArtifactFile{Name: n, Bytes: int64(len(body)), SHA256: sha256Hex(body)})
		bodies[n] = body
	}
	seedArtifactRun(t, f, id, files, bodies)
	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id); err != nil {
		t.Errorf("get refused \u0130.png and i.png, which EqualFold keeps apart: %v", err)
	}
}

// TestArtifactsGetRefusesANameThatIsADirectoryOfAnother: "a" and "a/b.png"
// cannot both be written — the second needs root/a to be a directory and the
// first needs it to be a file — and the record is refused before any directory
// is created, as a usage error. The alternative is what the check phase used
// to do: create root/a while checking a/b.png, then fail to rename a file onto
// the directory it had just made, and exit 2 with a bare rename error.
// Mutation caught: creating parents during the check phase, or leaving the
// prefix relation to the rename.
func TestArtifactsGetRefusesANameThatIsADirectoryOfAnother(t *testing.T) {
	body := []byte("one")
	for _, tc := range []struct {
		name  string
		names []string
	}{
		{"the file first", []string{"a", "a/b.png"}},
		{"the file inside first", []string{"a/b.png", "a"}},
		{"deeper", []string{"frames/a.png", "frames"}},
		{"deeper, listed the other way", []string{"frames", "frames/a.png"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := t.TempDir()
			t.Chdir(dir)
			const id = "20261016-090000-abcd"
			files := make([]runstore.ArtifactFile, len(tc.names))
			for i, n := range tc.names {
				files[i] = runstore.ArtifactFile{Name: n, Bytes: int64(len(body)), SHA256: sha256Hex(body)}
			}
			// No objects at all: the record is the hostile half (a sibling run
			// of the same repository can rewrite result.json), and the refusal
			// has to come before the bucket is asked for anything — which is
			// also what lets this test seed a pair of names no object store
			// could hold at once.
			seedArtifactRun(t, f, id, files, nil)

			_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
			if err == nil {
				t.Fatalf("get accepted a record naming both a file and a file inside it: %v", tc.names)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit code = %d, want %d (the record is the user's to read, not a remote failure)", code, ExitUserError)
			}
			for _, n := range tc.names {
				if !strings.Contains(err.Error(), strconv.Quote(n)) {
					t.Errorf("the refusal does not name %q: %v", n, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(dir, artifactRunDir(id))); !os.IsNotExist(err) {
				t.Errorf("a prefix-conflicting record created the destination root: %v", err)
			}
		})
	}
	// A name that merely shares a prefix is not a conflict: "a" and "ab.png"
	// are two files, and "a/b.png" and "ab.png" are two more.
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "a", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
		{Name: "ab.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{"a": body, "ab.png": body})
	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id); err != nil {
		t.Errorf("get refused two names that share a prefix but are not one inside the other: %v", err)
	}
}

// TestArtifactsGetCreatesNoDirectoriesWhileChecking: every destination is
// proved before the first byte is read, and proving one must not change the
// filesystem — a check phase that creates directories leaves a half-made tree
// behind when a LATER entry is refused, and it is what makes the prefix
// conflict above reachable in the first place. Mutation caught: MkdirAll
// during the check phase.
func TestArtifactsGetCreatesNoDirectoriesWhileChecking(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("one")
	// The second entry's destination is a symlink, which the check refuses;
	// the first entry's parent (root/frames) must not have been created by the
	// time that refusal is reported.
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "frames/a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
		{Name: "b.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{"frames/a.png": body, "b.png": body})
	root := filepath.Join(dir, artifactRunDir(id))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), filepath.Join(root, "b.png")); err != nil {
		t.Fatal(err)
	}

	_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if code := ExitCode(err); code != ExitUserError {
		t.Fatalf("get with a symlinked destination = %v, want a user error", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "frames")); !os.IsNotExist(err) {
		t.Errorf("the check phase created %s: a refused record left a directory behind", filepath.Join(root, "frames"))
	}
	if entries := dirEntries(t, root); len(entries) != 1 || entries[0] != "b.png" {
		t.Errorf("the destination root holds %v, want just the symlink that was already there", entries)
	}
}

// TestArtifactsGetRefusesADeepDirectoryComponentThatIsNotADirectory: the same
// refusal as the one-component case, one level down. A name like
// frames/sub/a.png whose root/frames is a regular file fails on the very first
// Lstat of parentUnderRoot's walk, and ENOTDIR is not os.IsNotExist — so the
// walk's own "not a remote failure" arm does not catch it either, and the
// whole set of downloads exits 2 for a destination the operator's own
// filesystem cannot hold. Mutation caught: that arm calling remote(err) for
// every error that is not ENOENT.
func TestArtifactsGetRefusesADeepDirectoryComponentThatIsNotADirectory(t *testing.T) {
	body := []byte("hello")
	for _, tc := range []struct {
		name        string
		artifact    string
		entryInWay  string
		wantsInText string
	}{
		{"a file where the first directory goes", "frames/sub/a.png", "frames", "frames"},
		{"a file inside the first directory", "frames/sub/a.png", filepath.Join("frames", "sub"), "sub"},
		{"a file three components deep", "a/b/c/d.png", filepath.Join("a", "b"), "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := t.TempDir()
			t.Chdir(dir)
			const id = "20261016-090000-abcd"
			seedArtifactRun(t, f, id, []runstore.ArtifactFile{
				{Name: tc.artifact, Bytes: int64(len(body)), SHA256: sha256Hex(body)},
			}, map[string][]byte{tc.artifact: body})
			root := filepath.Join(dir, artifactRunDir(id))
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			inWay := filepath.Join(root, tc.entryInWay)
			if err := os.MkdirAll(filepath.Dir(inWay), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(inWay, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}

			_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
			if err == nil {
				t.Fatalf("get wrote through the file at %s", tc.entryInWay)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit code = %d, want %d: a destination this filesystem cannot hold is the operator's error, not a remote failure (%v)", code, ExitUserError, err)
			}
			if !strings.Contains(err.Error(), tc.wantsInText) {
				t.Errorf("the refusal does not name the entry in the way (%s): %v", tc.wantsInText, err)
			}
			if strings.Contains(err.Error(), "lstat") && strings.Contains(err.Error(), "not a directory") {
				t.Errorf("the refusal is the raw filesystem error rather than a sentence about the path: %v", err)
			}
		})
	}
}

// TestArtifactPathErrClassifiesLocalConditions pins the whole mapping, not
// only the errno a Linux test can reach: the two errnos the suite cannot
// produce reliably — a directory it may not read (as root, chmod 000 still
// opens) and a path over the filesystem's limit (the limit is the filesystem's
// and the platform's) — are the ones a regression would silently turn back
// into exit 2, and this is where they are checked. An error the mapping does
// not recognise stays remote: guessing "the operator's path" for an error
// whose cause is unknown is the opposite mistake.
func TestArtifactPathErrClassifiesLocalConditions(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"a component that is a regular file", syscall.ENOTDIR, ExitUserError},
		{"a path the operator cannot read", fs.ErrPermission, ExitUserError},
		{"a path over the filesystem's limit", syscall.ENAMETOOLONG, ExitUserError},
		{"something else", errors.New("the disk went away"), ExitRemoteError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := artifactPathErr("out/the-run-artifacts", tc.err)
			if code := ExitCode(err); code != tc.want {
				t.Fatalf("exit code = %d, want %d (%v)", code, tc.want, err)
			}
			if tc.want == ExitUserError && !strings.Contains(err.Error(), "out/the-run-artifacts") {
				t.Errorf("the message does not name the path: %v", err)
			}
			// The unrecognised error is passed through as it came: this
			// function adds nothing to an error whose cause it does not know.
			if tc.want == ExitRemoteError && !errors.Is(err, tc.err) {
				t.Errorf("the error was not passed through: %v", err)
			}
		})
	}
}

// TestArtifactsGetRefusesAnOutThatIsNotADirectory: --out names the parent the
// run's directory is made in, and an ordinary file there — or any non-directory
// component of the --out path — is a local condition: the Lstat that asks
// whether the destination already exists answers ENOTDIR, which is not
// os.IsNotExist, so the arm written for "some other error" treats it as a
// remote failure and the command exits 2 where the very next check (an entry AT
// the root that is not a directory, exit 1) says the operator's path is wrong.
// The message names the path, and nothing is created.
// Mutation caught: prepareArtifactRoot's Lstat arm calling remote(err) for
// anything that is not ENOENT.
func TestArtifactsGetRefusesAnOutThatIsNotADirectory(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{"sizzle.mp4": body})
	file := filepath.Join(dir, "an-out-file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, out := range []string{file, filepath.Join(file, "under-it"), filepath.Join(file, "a", "b")} {
		t.Run(out, func(t *testing.T) {
			_, _, err := execute(t, "artifacts", "get", "--out", out, appSlug+"/"+id)
			if err == nil {
				t.Fatalf("get with --out %s was accepted", out)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit code = %d, want %d: a --out this filesystem cannot hold is the operator's error, not a remote failure (%v)", code, ExitUserError, err)
			}
			if !strings.Contains(err.Error(), "an-out-file") {
				t.Errorf("the refusal does not name the path in the way: %v", err)
			}
		})
	}
	// The file is still exactly what it was: a refusal creates nothing, and
	// replaces nothing.
	if got, err := os.ReadFile(file); err != nil || string(got) != "not a directory" {
		t.Errorf("the file used as --out = %q, %v, want it untouched", got, err)
	}
	if _, err := os.Stat(filepath.Join(file, "under-it")); err == nil {
		t.Errorf("--out created something under a regular file")
	}
	// A --out that is a real directory still works, so the refusal is about
	// what is there and not about --out being checked at all.
	good := filepath.Join(dir, "an-out-dir")
	if _, _, err := execute(t, "artifacts", "get", "--out", good, appSlug+"/"+id); err != nil {
		t.Fatalf("get with --out naming a directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(good, artifactRunDir(id), "sizzle.mp4")); err != nil {
		t.Errorf("the download did not land under the directory --out named: %v", err)
	}
}

// TestArtifactsGetRefusesADirectoryComponentThatIsNotADirectory: a name like
// frames/a.png needs root/frames to be a directory, and the operator's own
// file — or a dangling link, or a link to a file — sitting there means the
// download cannot happen at all. That is a user error naming the path, exit 1,
// like every other destination refusal in checkArtifactDest; left to the write
// it surfaces from os.Lstat or MkdirAll as a bare "not a directory" and
// reaches the user through remote(err) as exit 2, which tells a script to
// retry and blames the infrastructure for a local condition. Mutation caught:
// parentUnderRoot accepting any existing entry as its anchor (root/frames is
// Lstat-ed as existing, EvalSymlinks resolves it inside root, the prefix check
// passes, and the failure only appears later, unclassified).
func TestArtifactsGetRefusesADirectoryComponentThatIsNotADirectory(t *testing.T) {
	body := []byte("hello")
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, dir, root string)
	}{
		{"a regular file", func(t *testing.T, dir, root string) {
			if err := os.WriteFile(filepath.Join(root, "frames"), []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlink to nothing", func(t *testing.T, dir, root string) {
			if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(root, "frames")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlink to a file", func(t *testing.T, dir, root string) {
			target := filepath.Join(dir, "a-file")
			if err := os.WriteFile(target, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(root, "frames")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := t.TempDir()
			t.Chdir(dir)
			const id = "20261016-090000-abcd"
			seedArtifactRun(t, f, id, []runstore.ArtifactFile{
				{Name: "frames/a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
			}, map[string][]byte{"frames/a.png": body})
			root := filepath.Join(dir, artifactRunDir(id))
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			tc.plant(t, dir, root)

			_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
			if err == nil {
				t.Fatalf("get wrote through %s at a directory the name needs", tc.name)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit code = %d, want %d: a destination this filesystem cannot hold is the operator's error, not a remote failure (%v)", code, ExitUserError, err)
			}
			if !strings.Contains(err.Error(), "frames") {
				t.Errorf("the refusal does not name the entry in the way: %v", err)
			}
			if entries := dirEntries(t, root); len(entries) != 1 || entries[0] != "frames" {
				t.Errorf("the destination root holds %v, want just the entry that was already there", entries)
			}
		})
	}
	// A real directory at that path is exactly what the name needs, so the
	// download goes through it: the refusal must be about what is there, not
	// about the name having a directory component at all.
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "frames/a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{"frames/a.png": body})
	root := filepath.Join(dir, artifactRunDir(id))
	if err := os.MkdirAll(filepath.Join(root, "frames"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id); err != nil {
		t.Errorf("get refused an existing directory at a directory of the name: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "frames", "a.png")); err != nil || string(got) != string(body) {
		t.Errorf("the artifact was not written through the existing directory: %q, %v", got, err)
	}
}

// TestArtifactsWithRepoStillLooksTheRunUp: --repo answers "which repository
// does this bare run ID belong to", not "does this run exist", so a mistyped
// id with --repo is a missing run and never the "has no result.json: it never
// finished" that only a run that really launched can be charged with — that
// message sends an operator looking for a container that died. It is also the
// cheap check: one object under the named repository rather than the search
// across all of them that a bare ID without --repo needs. Mutation caught:
// returning a store for anything shaped like a run ID when --repo is given.
func TestArtifactsWithRepoStillLooksTheRunUp(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{"sizzle.mp4": body})

	// The run that is there still resolves, in both shapes of RUN, so this is
	// not a blanket refusal of --repo.
	for _, args := range [][]string{
		{"artifacts", "ls", "--repo", "acme/app", id},
		{"artifacts", "ls", "--repo", "acme/app", appSlug + "/" + id},
	} {
		if _, _, err := execute(t, args...); err != nil {
			t.Errorf("%v = %v", args, err)
		}
	}
	// A run ID nothing ever launched.
	const gone = "20261016-090000-beef"
	for _, args := range [][]string{
		{"artifacts", "ls", "--repo", "acme/app", gone},
		{"artifacts", "get", "--repo", "acme/app", gone},
		{"artifacts", "ls", "--repo", "acme/app", appSlug + "/" + gone},
	} {
		_, _, err := execute(t, args...)
		if code := ExitCode(err); code != ExitUserError {
			t.Errorf("%v = %v, want a user error", args, err)
		}
		if err == nil || !strings.Contains(err.Error(), "no run") {
			t.Errorf("%v does not report a missing run: %v", args, err)
		}
		if err != nil && strings.Contains(err.Error(), "never finished") {
			t.Errorf("%v charges a run that never launched with never finishing: %v", args, err)
		}
	}
	// A run that did launch and never wrote a record is still that other
	// thing: the lookup is what tells the two apart, not the message alone.
	const id2 = "20261016-090000-cafe"
	seedRun(t, f, id2, "", "someone@example.com", false)
	_, _, err := execute(t, "artifacts", "ls", "--repo", "acme/app", id2)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "never finished") {
		t.Errorf("ls of a launched run with no record = %v, want the never-finished refusal", err)
	}
}

// TestArtifactsGetEscapesANameInItsRefusals: every name in a refusal is
// untrusted text — the record's (a hostile sibling run of the same repository
// can rewrite result.json) and the NAME the user typed alike — and the
// top-level error line keeps newlines, because some errors list several
// problems. A name carrying a newline would otherwise print a line of its
// own, and a line beginning "fugaro:" is indistinguishable from the tool's
// own output on stderr. Mutation caught: interpolating a name with %s.
func TestArtifactsGetEscapesANameInItsRefusals(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	hostile := "a.png\nfugaro: all good"
	absent := "b.png\nfugaro: all good"
	body := []byte("hello")
	// A newline is refused by the uploader's own rule, so the record's entry
	// is exactly the "cannot be retrieved" refusal — the message a hostile
	// record aims at.
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: hostile, Bytes: int64(len(body)), SHA256: sha256Hex(body)},
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{"sizzle.mp4": body})

	for _, tc := range []struct {
		what string
		args []string
	}{
		{"the whole record", []string{"artifacts", "get", appSlug + "/" + id}},
		{"a named entry of the record", []string{"artifacts", "get", appSlug + "/" + id, hostile}},
		{"a NAME the record does not carry", []string{"artifacts", "get", appSlug + "/" + id, absent}},
	} {
		_, _, err := execute(t, tc.args...)
		if err == nil {
			t.Fatalf("get of %s was accepted, want a refusal", tc.what)
		}
		msg := ErrorText(err)
		if strings.ContainsAny(msg, "\n\r") {
			t.Errorf("the refusal about %s printed a line of its own:\n%q", tc.what, msg)
		}
		if !strings.Contains(msg, `\n`) {
			t.Errorf("the refusal about %s does not escape the name's newline: %q", tc.what, msg)
		}
	}
}

// TestArtifactsGetEscapesTheDestinationItPrints: the destination is built from
// a name the record carries, and the uploader's rule accepts the characters
// safetext escapes as "spoofing" (a zero-width space, a bidi override) —
// a variation selector in a filename is a legal name a run delivers, so
// ArtifactName does not refuse the class. What must not happen is the operator
// reading a line a bidi override draws for them: the bytes on disk are the name
// as recorded, and the line printed is the escaped one, so the path shown is
// the path that is really there.
// Mutation caught: printing dest raw.
func TestArtifactsGetEscapesTheDestinationItPrints(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	// A zero-width space and a right-to-left override: nothing a terminal
	// should be trusted with, and both inside the uploader's own rule.
	name := "a​b.png"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: name, Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "image/png"},
	}, map[string][]byte{name: body})

	out, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, name)
	if err != nil {
		t.Fatalf("get of a name the uploader accepts: %v", err)
	}
	if strings.ContainsRune(out, '​') {
		t.Errorf("get printed the name's zero-width/bidi characters raw:\n%q", out)
	}
	if !strings.Contains(out, "a?b.png") {
		t.Errorf("get does not print the escaped destination:\n%q", out)
	}
	// The file is where the record's name says it is: escaping is for the
	// line, not for the path.
	if _, err := os.Stat(filepath.Join(dir, artifactRunDir(id), name)); err != nil {
		t.Errorf("the artifact is not at the name the record carries: %v", err)
	}
	// The bidi override is the same class of character and a stronger
	// refusal: it never becomes a path at all.
	bidi := "a‮b.png"
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: bidi, Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{bidi: body})
	if _, _, err := execute(t, "artifacts", "get", appSlug+"/"+id, bidi); err == nil {
		t.Error("get accepted a name carrying a bidi override")
	}
}

// TestArtifactsGetRefusesASymlinkedOrNonDirectoryRoot: the destination root —
// <run-id>-artifacts under --out, which is the PARENT the user names and never
// the destination — is only usable when it is a real directory. A symlink
// there is a path out of the directory the user asked for, and anything else
// is not somewhere to write files into. Mutation caught: checking the root
// with Stat (which follows the link), or not checking it at all.
func TestArtifactsGetRefusesASymlinkedOrNonDirectoryRoot(t *testing.T) {
	body := []byte("hello")
	for _, tc := range []struct {
		name string
		// plant puts something at the root path before the run.
		plant func(t *testing.T, root string) string
	}{
		{"a symlink to another directory", func(t *testing.T, root string) string {
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, root); err != nil {
				t.Fatal(err)
			}
			return outside
		}},
		{"a regular file", func(t *testing.T, root string) string {
			if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			return root
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := t.TempDir()
			t.Chdir(dir)
			const id = "20261016-090000-abcd"
			seedArtifactRun(t, f, id, []runstore.ArtifactFile{
				{Name: "a.png", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
			}, map[string][]byte{"a.png": body})
			root := filepath.Join(dir, artifactRunDir(id))
			planted := tc.plant(t, root)

			_, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("get with %s at the destination root = %v, want a user error", tc.name, err)
			}
			if fi, err := os.Lstat(planted); err != nil || fi.Mode()&os.ModeSymlink != 0 {
				t.Errorf("the existing entry at the root was replaced: %v, %v", fi, err)
			}
			if fi, err := os.Lstat(planted); err != nil || !fi.IsDir() {
				// A regular file: the entry is still the file it was.
				if got, err := os.ReadFile(planted); err != nil || string(got) != "not a directory" {
					t.Errorf("the file at the root was changed: %q, %v", got, err)
				}
			} else if entries := dirEntries(t, planted); len(entries) != 0 {
				t.Errorf("get wrote through the root: %v", entries)
			}
			// ... and the same with --force: the flag is about the file at
			// the destination, not about what the root path means.
			if _, _, err := execute(t, "artifacts", "get", "--force", appSlug+"/"+id); ExitCode(err) != ExitUserError {
				t.Errorf("get --force with %s at the root = %v, want a user error", tc.name, err)
			}
		})
	}
}

// TestArtifactsGetAsksForTheStrictClassification: a 403 on the artifact's read
// is an access problem and must be reported as one — this account may not read
// the run's artifacts — and not folded into "there is no such artifact", which
// would send the operator looking for a run whose objects expired when the
// truth is a missing objectViewer binding (design §3). The classification is
// blobx's strict flag, and it is the only thing that tells the two apart on
// GCS, where gocloud reports a refused read as NotFound. Mutation caught:
// passing strict false (the message becomes the raw driver error, with no
// mention of what to fix).
//
// The fake GCS is where a 403 can be produced at all — a file-backed bucket
// has no 403 to give — so this calls getArtifact directly, as the drill-down
// commands' tests call their run functions.
func TestArtifactsGetAsksForTheStrictClassification(t *testing.T) {
	f := newCloudFixture(t)
	g := gcpfake.NewGCS(t)
	env := envOn(t, f, g.Bucket(t, "runs"))
	defer env.Close()
	ctx := context.Background()
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	a := runstore.ArtifactFile{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body)}
	s := runstore.Open(env.bucket.Bucket, appSlug, id)
	key := s.Prefix() + runstore.ArtifactPrefix + a.Name
	// The object is in the fake bucket (a file-backed bucket has no 403 to
	// give), and every read of it answers 403, as a bucket without
	// objectViewer does for this account.
	g.Put("runs", key, body)
	g.FailObjectGetsAfter("runs", key, 0)
	dir := t.TempDir()
	var out strings.Builder
	err := getArtifact(ctx, env, s, a, dir, filepath.Join(dir, a.Name), false, &out)
	if err == nil {
		t.Fatal("get of an artifact the bucket refuses to read was accepted")
	}
	if code := ExitCode(err); code != ExitRemoteError {
		t.Errorf("exit code = %d, want %d (a refused read is not the record's fault)", code, ExitRemoteError)
	}
	if !strings.Contains(err.Error(), "may not read the run's artifacts") {
		t.Errorf("a 403 is not reported as an access problem: %v", err)
	}
	if strings.Contains(err.Error(), "no such artifact in the runs bucket") {
		t.Errorf("a refused read is reported as a missing artifact: %v", err)
	}
	if entries := dirEntries(t, dir); len(entries) != 0 {
		t.Errorf("a refused read wrote %q, want nothing", entries)
	}
}

// appearDriver serves one object and creates a file at a path on the first
// read of its body: the destination arriving in the window between the
// command's pre-check and the placement. No file-backed bucket can open that
// window — nothing the command does between the check and the first byte of
// the read is a point a test can hang a write on — so this is what a race with
// another process looks like from inside `artifacts get`.
type appearDriver struct {
	driver.Bucket
	data   []byte
	appear string // the path a file appears at
	at     string // what that file holds
	done   bool
}

func (d *appearDriver) ErrorCode(error) gcerrors.ErrorCode { return gcerrors.Unknown }
func (d *appearDriver) As(any) bool                        { return false }
func (d *appearDriver) ErrorAs(error, any) bool            { return false }
func (d *appearDriver) Close() error                       { return nil }

func (d *appearDriver) NewRangeReader(context.Context, string, int64, int64, *driver.ReaderOptions) (driver.Reader, error) {
	return &appearReader{Reader: bytes.NewReader(d.data), d: d}, nil
}

type appearReader struct {
	*bytes.Reader
	d *appearDriver
}

func (r *appearReader) Read(p []byte) (int, error) {
	if !r.d.done {
		r.d.done = true
		if err := os.WriteFile(r.d.appear, []byte(r.d.at), 0o600); err != nil {
			return 0, err
		}
	}
	return r.Reader.Read(p)
}

func (r *appearReader) Close() error { return nil }
func (r *appearReader) Attributes() *driver.ReaderAttributes {
	return &driver.ReaderAttributes{Size: int64(len(r.d.data))}
}
func (r *appearReader) As(any) bool { return false }

// TestArtifactsGetRefusesAFileThatAppearedAfterThePreCheck: `artifacts get`
// keeps its no-replace promise twice over — the pre-check of every destination
// before the first byte is read, and the placement itself — and every other
// test reaches one of the two and not both: a file that is already there is
// refused by the pre-check, so deleting --force's wiring into the placement
// (the replace argument of GetFileExactly, through getArtifact) failed
// nothing. The window between them is the one this test opens, with the file
// arriving during the read of the object, and both --force answers are
// asserted against it.
//
// The three steps are runArtifactsGet's own, in its order — prepareArtifactRoot,
// checkArtifactDest for the name, getArtifact — because the bucket has to be
// the in-process one the driver is behind, which the command's own
// openCloud-from-config path cannot be given.
//
// Mutation caught: getArtifact passing replace=true regardless of force (the
// no-force case downloads and overwrites the file that appeared), or replace
// false regardless of force (the --force case is refused).
func TestArtifactsGetRefusesAFileThatAppearedAfterThePreCheck(t *testing.T) {
	ctx := context.Background()
	const id = "20261016-090000-abcd"
	body := []byte("the run's file")
	a := runstore.ArtifactFile{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body)}
	const theirs = "the operator's own file"

	for name, force := range map[string]bool{"without --force": false, "with --force": true} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "out")
			dest := filepath.Join(root, a.Name)
			env := &cloudEnv{bucket: blobx.Wrap(blob.NewBucket(&appearDriver{data: body, appear: dest, at: theirs}))}
			defer env.Close()
			s := runstore.Open(env.bucket.Bucket, appSlug, id)

			if err := prepareArtifactRoot(root); err != nil {
				t.Fatal(err)
			}
			got, err := checkArtifactDest(root, a.Name, force)
			if err != nil || got != dest {
				t.Fatalf("checkArtifactDest(%q, force=%v) = %q, %v: the file is not there yet, so the pre-check has nothing to refuse", a.Name, force, got, err)
			}
			var out strings.Builder
			err = getArtifact(ctx, env, s, a, root, got, force, &out)

			// Whatever the outcome, the tree holds one file and it is the
			// destination: no leftover temporary beside it.
			if got := dirEntries(t, root); len(got) != 1 || got[0] != a.Name {
				t.Errorf("the destination directory holds %v, want just %s", got, a.Name)
			}
			back, rerr := os.ReadFile(dest)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if !force {
				if code := ExitCode(err); code != ExitUserError {
					t.Errorf("get of a file that appeared mid-read exit code = %d, want %d (the destination is the operator's to change)", code, ExitUserError)
				}
				if err == nil || !strings.Contains(err.Error(), "already exists") {
					t.Errorf("get of a file that appeared mid-read = %v, want a refusal naming the destination", err)
				}
				if string(back) != theirs {
					t.Errorf("the file that appeared mid-read was replaced by %q, and the command reported %v", back, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("get --force of a file that appeared mid-read = %v, want the download (the flag is what permits replacing it)", err)
			}
			if !bytes.Equal(back, body) {
				t.Errorf("get --force left %q at the destination, want the object's bytes", back)
			}
		})
	}
}

// TestArtifactsGetRefusesAHashThatIsNotASha256: the record's hash is what
// makes the download checkable, and the check is that it is 64 lower-case hex
// characters — not that it is non-empty. An upper-case digest is not what the
// uploader records (it writes hex.EncodeToString, lower case) and is refused
// by the same rule as a truncated or non-hex one, each with a reason that names
// the field. Mutation caught: validSHA256 reduced to "not empty".
func TestArtifactsGetRefusesAHashThatIsNotASha256(t *testing.T) {
	body := []byte("hello")
	for _, tc := range []struct {
		name string
		sum  string
	}{
		{"upper case hex", strings.ToUpper(sha256Hex(body))},
		{"sixty-four characters that are not hex", strings.Repeat("g", 64)},
		{"sixty-three characters", sha256Hex(body)[:63]},
		{"sixty-five characters", sha256Hex(body) + "0"},
		{"a sha1", strings.Repeat("a", 40)},
		{"hex with a space", sha256Hex(body)[:63] + " "},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := t.TempDir()
			t.Chdir(dir)
			const id = "20261016-090000-abcd"
			seedArtifactRun(t, f, id, []runstore.ArtifactFile{
				{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: tc.sum},
			}, map[string][]byte{"sizzle.mp4": body})
			for _, args := range [][]string{
				{"artifacts", "get", appSlug + "/" + id},
				{"artifacts", "get", appSlug + "/" + id, "sizzle.mp4"},
			} {
				_, _, err := execute(t, args...)
				if ExitCode(err) != ExitUserError {
					t.Fatalf("%v with %s = %v, want a user error", args, tc.name, err)
				}
				if !strings.Contains(err.Error(), "sha256") {
					t.Errorf("%v does not name the field it refused: %v", args, err)
				}
			}
			if entries := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(entries) != 0 {
				t.Errorf("a record with %s wrote %q, want nothing", tc.name, entries)
			}
		})
	}
}

// TestArtifactsGetPrintsNoContentTypeRatherThanAGap: content_type is optional
// in a record, and the line that says what was written must not answer the
// question with a gap — an empty field interpolated into the parenthetical
// leaves ",  recorded)". Mutation caught: interpolating the recorded content
// type unconditionally.
func TestArtifactsGetPrintsNoContentTypeRatherThanAGap(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	body := []byte("hello")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(body)), SHA256: sha256Hex(body)},
	}, map[string][]byte{"sizzle.mp4": body})

	out, _, err := execute(t, "artifacts", "get", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(out, ",  recorded)") {
		t.Errorf("an empty content type printed a gap:\n%q", out)
	}
	if !strings.Contains(out, "no content type recorded)") {
		t.Errorf("the line does not say the record carried no content type:\n%q", out)
	}
	// A record that does carry one prints it, unchanged.
	ct := []byte("video/mp4")
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: int64(len(ct)), SHA256: sha256Hex(ct), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": ct})
	out, _, err = execute(t, "artifacts", "get", "--force", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("get --force: %v", err)
	}
	if !strings.Contains(out, "video/mp4 recorded)") {
		t.Errorf("the recorded content type is not printed:\n%q", out)
	}
}

// TestArtifactsReadIsBounded: a read that stops producing bytes has to end the
// command, and the bound is derived from the recorded size and clamped, so a
// record's own byte count — or a value a caller passes without one — cannot
// make the deadline meaningless (a zero, a negative or an overflowed
// duration is no deadline at all). Mutation caught: dropping the timeout, and
// deriving it without a ceiling.
func TestArtifactsReadIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
	}{
		{"an empty artifact", 0},
		{"a negative size", -1},
		{"a small artifact", 4 << 20},
		{"the format's ceiling", recipe.MaxOutputBytes},
		{"a size nothing in the format reaches", math.MaxInt64},
	} {
		ctx, cancel := artifactReadContext(context.Background(), tc.size)
		dl, ok := ctx.Deadline()
		cancel()
		if !ok {
			t.Fatalf("%s: no deadline on the read at all — a stalled object hangs the command", tc.name)
		}
		left := time.Until(dl)
		if left <= 0 {
			t.Errorf("%s: the read's deadline is already past (%s)", tc.name, left)
		}
		if ceiling := artifactReadBaseTimeout + artifactReadMaxTimeout; left > ceiling {
			t.Errorf("%s: bound = %s, over the %s ceiling the derived bonus is clamped to", tc.name, left, ceiling)
		}
	}
	// A larger artifact gets a longer bound: the point of deriving it from the
	// size is that the ceiling-sized artifact is not refused on a slow link.
	small, cancelSmall := artifactReadContext(context.Background(), 1<<20)
	smallDL, _ := small.Deadline()
	cancelSmall()
	big, cancelBig := artifactReadContext(context.Background(), recipe.MaxOutputBytes)
	bigDL, _ := big.Deadline()
	cancelBig()
	if !bigDL.After(smallDL) {
		t.Errorf("a %s artifact is bounded by %s and a 1 MiB one by %s: the bound does not grow with the size", recipe.SizeText(recipe.MaxOutputBytes), time.Until(bigDL), time.Until(smallDL))
	}
	// The caller's own context still ends the read sooner, which is the
	// property a derived timeout must not break (a signal, a --timeout).
	parent, cancelParent := context.WithTimeout(context.Background(), time.Second)
	defer cancelParent()
	child, cancelChild := artifactReadContext(parent, recipe.MaxOutputBytes)
	childDL, _ := child.Deadline()
	cancelChild()
	pDL, _ := parent.Deadline()
	if !childDL.Equal(pDL) {
		t.Errorf("the derived deadline %s replaced the caller's %s", time.Until(childDL), time.Until(pDL))
	}
}

// TestArtifactsGetStillRefusesANegativeRecordedSize: 0 is a size, a negative
// one is not — there is no cap to bound a download by, and the refusal says so
// rather than reporting the count as an over-ceiling one (a negative number is
// below every ceiling, so a check ordered the other way would let it through).
// Mutation caught: dropping the size check entirely, and ordering the ceiling
// check before it.
func TestArtifactsGetStillRefusesANegativeRecordedSize(t *testing.T) {
	f := newCloudFixture(t)
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "20261016-090000-abcd"
	empty := []byte{}
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: "sizzle.mp4", Bytes: -1, SHA256: sha256Hex(empty), ContentType: "video/mp4"},
	}, map[string][]byte{"sizzle.mp4": empty})

	for _, args := range [][]string{
		{"artifacts", "get", appSlug + "/" + id},
		{"artifacts", "get", appSlug + "/" + id, "sizzle.mp4"},
	} {
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("%v of a record with a negative size = %v, want a user error", args, err)
		}
		if !strings.Contains(err.Error(), "negative") {
			t.Errorf("%v does not say the count is not a size: %v", args, err)
		}
		if strings.Contains(err.Error(), "ceiling") {
			t.Errorf("%v reports a negative count as over the ceiling: %v", args, err)
		}
	}
	if names := dirEntries(t, filepath.Join(dir, artifactRunDir(id))); len(names) != 0 {
		t.Errorf("a negative recorded size wrote %q, want nothing", names)
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

// TestDiagnoseEscapesAnArtifactName: the record is untrusted text (§8.3), and
// the artifact block prints the record's names. A name carrying a control
// character is printed escaped — an ANSI escape in a name would otherwise
// reach the operator's terminal and paint or move what follows it, which is
// how a record that a hostile run wrote turns into terminal output the
// operator reads as the tool's own. Mutation caught: printing the path through
// oneLine's own callers without it (fmt.Fprintf(w, "  %s\n", p)).
func TestDiagnoseEscapesAnArtifactName(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20261016-090000-abcd"
	name := "a\x1b[31mred\x1b[0m.mp4"
	seedArtifactRun(t, f, id, []runstore.ArtifactFile{
		{Name: name, Bytes: 1, SHA256: sha256Hex([]byte("x")), ContentType: "video/mp4"},
	}, nil)

	text, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if strings.ContainsRune(text, 0x1b) {
		t.Errorf("the text diagnosis printed a raw escape from an artifact name:\n%q", text)
	}
	if !strings.Contains(text, "Artifacts") {
		t.Fatalf("the artifacts block is missing:\n%s", text)
	}
	// The name is still readable: escaped, not dropped.
	if !strings.Contains(text, "red.mp4") {
		t.Errorf("the artifact name was not printed:\n%s", text)
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
