package runner_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/recipe"
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
func wantBootstrapError(t *testing.T, h *harness, steps []step, want ...string) *runstore.Record {
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
	return rec
}

// TestInputsAreDownloadedHashedAndExcludedFromGit is the runner's half of the
// contract (design §4 step 5): every input the task names comes down into
// <workdir>/inputs/<name> through blobx.GetFile, which verifies the sha256
// and the size the task claims and leaves nothing behind on a mismatch; the
// render that follows is handed FUGARO_INPUTS_DIR, the variable the render is
// told to read its files through (it names the absolute path, so a render does
// not have to know where the checkout is); the record echoes the task's own
// list, so a reader sees exactly what the run was given; and inputs/ is in the
// checkout's own git exclude, so the plan stage's `git add -A` cannot commit a
// screenshot into a branch.
//
// Mutation (skip downloadInputs in bootstrap): the render finds no inputs/ and
// the run fails. Mutation (drop the FUGARO_INPUTS_DIR assignment from the env
// block): the render's own guard exits non-zero and this test fails. Mutation
// (drop excludeInputsFromGit): the exclude file has no `inputs/` line and this
// test fails. Mutation (record the object's own size instead of the spec's
// list): rec.Inputs loses the second file.
func TestInputsAreDownloadedHashedAndExcludedFromGit(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	shot, shotBody := attachedInput("shot.png", "png-bytes")
	notes, notesBody := attachedInput("notes.txt", "two\n")
	spec.Inputs = []task.InputFile{shot, notes}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript("#!/bin/sh\n"+
		"mkdir -p out\n"+
		"[ -n \"$FUGARO_INPUTS_DIR\" ] || { echo 'FUGARO_INPUTS_DIR is unset' >&2; exit 4; }\n"+
		"cat \"$FUGARO_INPUTS_DIR/shot.png\" \"$FUGARO_INPUTS_DIR/notes.txt\" > out/seen.mp4\n"))
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

// TestInputsMissingFromTheBucketFailWithAMissingInput pins what the runner
// does with an object that is not there (design §4 step 5, P5): the run fails
// in bootstrap, the reason names the input and the key it looked for, nothing
// is written into the checkout, and the failure is NOT dressed up as an IAM
// problem the owner cannot act on. The last clause is the one an owner sees:
// a job whose service account was never granted the runs bucket reports a
// permission error, and the runner must not add to that confusion by calling
// a plain absence one.
//
// What this test does NOT pin, and cannot with this fake: that the runner
// passes strict=false to blobx.GetFile. memblob answers a missing key with
// NotFound, and blobx.GetFile turns NotFound into ErrNotExist whatever strict
// says — the two values differ only for an HTTP 403, which needs a driver
// that can answer one. That is pinned in blobx's own package, where the fake
// is a real gcsblob bucket over an HTTP server that answers 403
// (internal/blobx/blobx_test.go: forbiddenBucket, with
// TestReadTreatsForbiddenAsAbsent and TestStrictReadsReportAForbiddenObject).
// So the strict argument here is a convention this test cannot see through;
// the IAM-shaped-message assertion below is the part of it that is observable
// from the runner at all.
//
// Mutation (drop the errors.Is(err, blobx.ErrNotExist) arm of downloadInputs):
// the reason becomes "downloading attached input ...: <driver error>" and this
// test fails on the sentence the runner writes.
func TestInputsMissingFromTheBucketFailWithAMissingInput(t *testing.T) {
	spec := artifactSpec(sizzleRenderOnlyYAML)
	shot, _ := attachedInput("shot.png", "png-bytes")
	spec.Inputs = []task.InputFile{shot}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
	h.deps.Bucket = blobx.Wrap(h.bucket)
	rec := wantBootstrapError(t, h, nil, "not in this run's bucket", "shot.png", runInputKey(h, "shot.png"))
	for _, never := range []string{"forbidden", "permission", "denied", "IAM", "403"} {
		if strings.Contains(strings.ToLower(rec.Reason), strings.ToLower(never)) {
			t.Fatalf("a missing input is reported as %q: %s", never, rec.Reason)
		}
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, runstore.InputsDir, "shot.png")); err == nil {
		t.Fatal("an input that never came down was written into the checkout")
	}
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

// TestThePlanPromptNamesTheInputsDirectory is the prompt half of the same
// contract: a plan stage on a run that HAS attachments is told where they are
// and that FUGARO_INPUTS_DIR names the same directory, so it can read them
// instead of guessing — and, since the prompt is the only place a plan stage
// learns anything, telling it nothing is the failure mode this pins down.
// artifact_test.go's planStage asserts the other side: a launch with no
// attachments is told nothing about a directory that does not exist.
//
// Mutation (drop the `len(r.spec.Inputs) > 0` paragraph from
// artifactPlanPrompt): the prompt never names the directory and this test
// fails.
func TestThePlanPromptNamesTheInputsDirectory(t *testing.T) {
	spec := artifactSpec(sizzlePlanRenderYAML)
	shot, body := attachedInput("shot.png", "png-bytes")
	spec.Inputs = []task.InputFile{shot}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
	seedInputs(t, h, runInputKey(h, "shot.png"), body)

	var prompt string
	plan := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		prompt = req.Prompt
		if err := os.MkdirAll(filepath.Join(req.Dir, ".fugaro"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(req.Dir, ".fugaro", "render-plan.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 0.25}, nil
	}
	if rec, err := h.run(t, plan); err != nil || rec.Outcome != runstore.OutcomeArtifacts {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	dir := filepath.Join(h.deps.WorkDir, runstore.InputsDir)
	for _, want := range []string{"FUGARO_INPUTS_DIR", dir, "1 file(s)"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the plan prompt does not name %q:\n%s", want, prompt)
		}
	}
}

// TestInputsUnderAnImplementRecipeFailTheRun is the deferred cross-check arm
// (design §8.2, plan Task 6): a task that attached files under a resolved
// recipe of another mode fails the run before its first step. The launcher
// refuses this for every launch that resolves a recipe, so what reaches this
// arm is the launch the CLI could not decide — a recipe the runner chose, or
// a hand-written task.json — where the inputs would otherwise be downloaded
// into a run that commits a diff and opens a pull request, and nothing in its
// steps reads inputs/.
//
// The arm is keyed on the RESOLVED mode, not on spec.Mode: a spec is allowed
// to carry none (design §8.2's empty-Mode rule), and this spec carries none —
// it leaves the recipe to the runner, which resolves the catalog's default,
// mode: implement.
//
// Mutation (drop the `len(spec.Inputs) > 0 && !r.artifact` refusal from
// bootstrap): the implement run starts and this test fails on the missing
// bootstrap error; the inputs directory is then never created either, since
// nothing downloads into a run of this mode.
func TestInputsUnderAnImplementRecipeFailTheRun(t *testing.T) {
	shot, _ := attachedInput("shot.png", "png-bytes")
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature", Inputs: []task.InputFile{shot}}
	h := newHarnessFiles(t, renderCfg(t, "", ""), spec, renderScript(sizzleScript))
	h.deps.Bucket = blobx.Wrap(h.bucket)
	wantBootstrapError(t, h, nil, "only read by a mode: artifact run", "mode: "+recipe.ModeImplement, "1 file(s)")
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, runstore.InputsDir)); err == nil {
		t.Fatal("an implement run downloaded the attached files")
	}
}
