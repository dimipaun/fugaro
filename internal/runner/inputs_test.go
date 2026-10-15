package runner_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// inputsCfg is the fixture's fugaro.yaml with commands.render and the
// workflow's own inputs caps, so a test trips one of the three well under the
// defaults the zero config.Inputs answers with.
func inputsCfg(t *testing.T, caps string) string {
	t.Helper()
	cfg := renderCfg(t, "", "")
	const anchor = "      reports: [\"build/test-results/*.xml\"]\n"
	if !strings.Contains(cfg, anchor) {
		t.Fatal("the fixture's reports line moved")
	}
	return strings.Replace(cfg, anchor, anchor+"    inputs: "+caps+"\n", 1)
}

// attachedInput is one file an --attach launch would have recorded, and the
// bytes it uploaded: the name, that body, and the sha256 and size of it (the
// launcher hashes as it uploads, blobx.PutFile, so the record and the object
// are of the same bytes).
func attachedInput(name, body string) (task.InputFile, []byte) {
	sum := sha256.Sum256([]byte(body))
	return task.InputFile{Name: name, Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}, []byte(body)
}

// seedInputs puts body under the run's own inputs/<name>, where the launcher
// uploaded it, and gives the harness the runs bucket to read it back from:
// newHarnessFiles leaves Deps.Bucket nil, as a local `fugaro exec` run has it,
// and a run that was given files needs one.
func seedInputs(t *testing.T, h *harness, key string, body []byte) {
	t.Helper()
	h.deps.Bucket = blobx.Wrap(h.bucket)
	if err := h.bucket.WriteAll(context.Background(), key, body, nil); err != nil {
		t.Fatal(err)
	}
}

// runInputKey is where the launcher uploads an attached input: the run's
// prefix, "inputs/", the name (design §4 step 3).
func runInputKey(h *harness, name string) string {
	return runstore.InputKey(h.store.Prefix(), name)
}

// wantBootstrapError runs the harness and requires the run to have failed in
// bootstrap with a reason containing every want.
func wantBootstrapError(t *testing.T, h *harness, steps []step, want ...string) {
	t.Helper()
	rec, err := h.run(t, steps...)
	if err == nil {
		t.Fatal("the run succeeded, want a refusal")
	}
	if rec.Status != runstore.StatusInfraError || !strings.HasPrefix(rec.Reason, "bootstrap: ") {
		t.Fatalf("record = %+v, want a bootstrap infra_error", rec)
	}
	for _, w := range want {
		if !strings.Contains(rec.Reason, w) {
			t.Errorf("reason = %q, want it to contain %q", rec.Reason, w)
		}
	}
}

// TestInputsAreDownloadedHashedAndExcludedFromGit is the runner's half of the
// contract (design §4 step 5): every input the task names comes down into
// <workdir>/inputs/<name> through blobx.GetFile, which verifies the sha256
// and the size the task claims and leaves nothing behind on a mismatch; the
// render that follows is handed FUGARO_INPUTS_DIR; the record echoes the task's
// own list, so a reader sees exactly what the run was given; and inputs/ is in
// the checkout's own git exclude, so the plan stage's `git add -A` cannot
// commit a screenshot into a branch.
//
// Mutation (skip downloadInputs in bootstrap): the render finds no inputs/ and
// the run fails. Mutation (drop excludeInputsFromGit): the exclude file has no
// `inputs/` line and this test fails. Mutation (record the object's own size
// instead of the spec's list): rec.Inputs loses the second file.
func TestInputsAreDownloadedHashedAndExcludedFromGit(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	shot, shotBody := attachedInput("shot.png", "png-bytes")
	notes, notesBody := attachedInput("notes.txt", "two\n")
	spec.Inputs = []task.InputFile{shot, notes}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript("#!/bin/sh\nmkdir -p out && cat inputs/shot.png inputs/notes.txt > out/seen.mp4\n"))
	seedInputs(t, h, runInputKey(h, "shot.png"), shotBody)
	seedInputs(t, h, runInputKey(h, "notes.txt"), notesBody)

	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Inputs) != 2 || rec.Inputs[0] != shot || rec.Inputs[1] != notes {
		t.Fatalf("record inputs = %+v, want the task's own list", rec.Inputs)
	}
	got, err := os.ReadFile(filepath.Join(h.deps.WorkDir, "out", "seen.mp4"))
	if err != nil {
		t.Fatalf("the render did not read the inputs: %v", err)
	}
	if string(got) != "png-bytes"+"two\n" {
		t.Fatalf("the render read %q, want both files' bytes", got)
	}
	for name, want := range map[string]string{"shot.png": "png-bytes", "notes.txt": "two\n"} {
		data, err := os.ReadFile(filepath.Join(h.deps.WorkDir, runstore.InputsDir, name))
		if err != nil {
			t.Fatalf("input %s was not downloaded: %v", name, err)
		}
		if string(data) != want {
			t.Errorf("input %s holds %q, want %q", name, data, want)
		}
	}
	exclude, err := os.ReadFile(filepath.Join(h.deps.WorkDir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("reading the exclude file: %v", err)
	}
	if !strings.Contains(string(exclude), runstore.InputsDir+"/") {
		t.Fatalf("the exclude file does not hold %s/:\n%s", runstore.InputsDir, exclude)
	}
}

// TestInputsMissingFromTheBucketFailWithAMissingInput is the runner-account
// semantics (design §4 step 5, P5): the job's own service account cannot read
// a runs bucket it was not given, so a missing object must be reported as a
// missing object — never folded into an IAM problem the owner cannot act on.
// blobx.GetFile is called with strict=false, which is what turns a 403 into
// ErrNotExist; with strict=true the same read is a permission error.
//
// Mutation (pass strict=true): the reason reads as a permission failure
// ("forbidden"/"permission") and this test fails.
func TestInputsMissingFromTheBucketFailWithAMissingInput(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	shot, _ := attachedInput("shot.png", "png-bytes")
	spec.Inputs = []task.InputFile{shot}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
	h.deps.Bucket = blobx.Wrap(h.bucket)
	wantBootstrapError(t, h, nil, "not in this run's bucket", "shot.png")
}

// TestAnInputWhoseHashDoesNotMatchFailsTheRun is what the expected-hash
// argument is for: the bytes that came down are not the bytes the task
// recorded, so the input never reaches the checkout — blobx.GetFile writes to
// a temp file and renames it into place only after the hash matches — and the
// run fails rather than rendering from a file nobody vouched for.
//
// Mutation (pass "" as the expected hash): the object is renamed into place,
// the render reads the wrong bytes, and this test fails.
func TestAnInputWhoseHashDoesNotMatchFailsTheRun(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	shot, _ := attachedInput("shot.png", "png-bytes")
	spec.Inputs = []task.InputFile{shot}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
	// Same length, different bytes: only the hash tells them apart.
	seedInputs(t, h, runInputKey(h, "shot.png"), []byte("PNG-BYTES"))
	wantBootstrapError(t, h, nil, "expected sha256")
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, runstore.InputsDir, "shot.png")); err == nil {
		t.Fatal("the mismatched object was written into the checkout")
	}
}

// TestInputsOnlyTheClaimedFilesAreDownloaded: the task's own list is the
// worklist, and the bucket's contents are not. A run downloads what the task
// names, whatever else has accumulated under the run's prefix — the runner
// never lists the bucket, so an object nobody attached cannot become an input.
//
// Mutation (list the bucket and download what is there): the second file
// appears in inputs/ and this test fails.
func TestInputsOnlyTheClaimedFilesAreDownloaded(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	shot, shotBody := attachedInput("shot.png", "png-bytes")
	spec.Inputs = []task.InputFile{shot}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
	seedInputs(t, h, runInputKey(h, "shot.png"), shotBody)
	if err := h.bucket.WriteAll(context.Background(), runInputKey(h, "other.png"), []byte("not attached"), nil); err != nil {
		t.Fatal(err)
	}
	if rec, err := h.run(t); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, runstore.InputsDir, "other.png")); err == nil {
		t.Fatal("an object the task never named was downloaded as an input")
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, runstore.InputsDir, "shot.png")); err != nil {
		t.Fatalf("the named input was not downloaded: %v", err)
	}
}

// TestInputsOverTheCapFailTheRun: workflows.inputs.max_files bounds the
// NUMBER of files, judged from the task's own list and before anything is
// downloaded.
//
// Mutation (drop the count arm of checkInputCaps): the run downloads both and
// this test fails.
func TestInputsOverTheCapFailTheRun(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	one, oneBody := attachedInput("one.png", "x")
	two, twoBody := attachedInput("two.png", "y")
	spec.Inputs = []task.InputFile{one, two}
	h := newHarnessFiles(t, inputsCfg(t, "{ max_files: 1 }"), spec, renderScript(sizzleScript))
	seedInputs(t, h, runInputKey(h, "one.png"), oneBody)
	seedInputs(t, h, runInputKey(h, "two.png"), twoBody)
	wantBootstrapError(t, h, nil, "max_files", "allows 1")
}

// TestInputsOverTheFileCapFailTheRun: max_bytes bounds ONE file's own claimed
// size, and it is judged per entry — a set of small files is not what trips
// it.
//
// Mutation (compare the set's total against max_bytes instead of each
// entry): three small files pass and this test fails.
func TestInputsOverTheFileCapFailTheRun(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	big, bigBody := attachedInput("big.png", "12345")
	small, smallBody := attachedInput("small.png", "x")
	spec.Inputs = []task.InputFile{small, big}
	h := newHarnessFiles(t, inputsCfg(t, "{ max_files: 8, max_bytes: 4, max_total_bytes: 1024 }"), spec, renderScript(sizzleScript))
	seedInputs(t, h, runInputKey(h, "big.png"), bigBody)
	seedInputs(t, h, runInputKey(h, "small.png"), smallBody)
	wantBootstrapError(t, h, nil, "max_bytes", "big.png", "per file")
}

// TestInputsOverTheTotalCapFailTheRun: max_total_bytes bounds THE SET — the
// sum of the claims — so a run whose every file is under the per-file cap is
// still refused, and the refusal says the sum rather than one file's size or
// the count times the per-file cap.
//
// Mutation (check each entry against max_total_bytes instead of the sum):
// both files pass and this test fails.
func TestInputsOverTheTotalCapFailTheRun(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	one, oneBody := attachedInput("one.png", "aaaaaa")
	two, twoBody := attachedInput("two.png", "bbbbbb")
	spec.Inputs = []task.InputFile{one, two}
	h := newHarnessFiles(t, inputsCfg(t, "{ max_files: 8, max_bytes: 10, max_total_bytes: 10 }"), spec, renderScript(sizzleScript))
	seedInputs(t, h, runInputKey(h, "one.png"), oneBody)
	seedInputs(t, h, runInputKey(h, "two.png"), twoBody)
	wantBootstrapError(t, h, nil, "max_total_bytes", "for the set")
}

// TestAnInputNameThatEscapesInputsIsRefused is the runner's own check, on the
// spec it was handed and not on whatever the launcher checked (design §8.2):
// the name is a path under inputs/, so a name that is absolute, that holds a
// ".." element, that is empty, or that is not a path at all is refused before
// anything is written. This is the arm a task file written by hand, or by a
// launcher from a later version, reaches.
//
// Mutation (drop the InputNameProblem loop from downloadInputs): the name is
// joined to the inputs directory, the download escapes it, and this test
// fails — with ../escape.png written outside inputs/.
func TestAnInputNameThatEscapesInputsIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"../escape.png", `has a ".." path element`},
		{"a/../../escape.png", `has a ".." path element`},
		{"/etc/passwd", "is an absolute path"},
		{"a\x00b", "NUL or a newline"},
		{"a//b", "empty path element"},
		// An empty name is not here: task.Parse's Validate owns it ("name
		// is required"), and ReadTask runs before this and refuses it. The
		// shape check below is for the names a decoder accepts — which is
		// every one of the paths above.
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := artifactSpec(sizzleRenderOnlyYAML)
			bad, body := attachedInput(tc.name, "x")
			spec.Inputs = []task.InputFile{bad}
			h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
			h.deps.Bucket = blobx.Wrap(h.bucket)
			_ = body
			wantBootstrapError(t, h, nil, tc.want)
			if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "escape.png")); err == nil {
				t.Fatal("the refusal still wrote a file outside inputs/")
			}
		})
	}
}

// TestAnInputNameIsNeverCleanedIntoSomethingPlausible is the difference
// between refusing a name and repairing it: "a/./b.png" and "./shot.png"
// clean to something that looks like an input, and both are refused anyway.
// A name is either the path it says it is or it is refused — never silently
// rewritten into a file the task did not name.
//
// Mutation (clean the name with filepath.Clean/path.Clean before checking
// it): both names pass, the file is downloaded under a different name than
// the task recorded, and this test fails.
func TestAnInputNameIsNeverCleanedIntoSomethingPlausible(t *testing.T) {
	for _, name := range []string{"./shot.png", "a/./b.png", "a/b/../b.png"} {
		t.Run(name, func(t *testing.T) {
			spec := artifactSpec(sizzleRenderOnlyYAML)
			in, body := attachedInput(name, "x")
			spec.Inputs = []task.InputFile{in}
			h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
			h.deps.Bucket = blobx.Wrap(h.bucket)
			_ = body
			wantBootstrapError(t, h, nil, "attached input")
			entries, err := os.ReadDir(filepath.Join(h.deps.WorkDir, runstore.InputsDir))
			if err == nil && len(entries) > 0 {
				t.Fatalf("a refused name still produced %d file(s) under inputs/", len(entries))
			}
		})
	}
}
