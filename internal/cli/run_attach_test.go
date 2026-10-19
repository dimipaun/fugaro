package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// attachShot writes a file of hold bytes named name under a directory of the
// test's own, and returns its path.
func attachShot(t *testing.T, name string, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// inputKey is where the launcher uploads an --attach file of run id: the
// run's own prefix, "inputs/", and the file's base name (design §4 step 3).
func inputKey(id, name string) string { return "runs/" + appSlug + "/" + id + "/inputs/" + name }

// taskKey is the run's stored task.
func taskKey(id string) string { return "runs/" + appSlug + "/" + id + "/task.json" }

// object reads one object of the fixture's bucket.
func object(t *testing.T, f *cloudFixture, key string) []byte {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	data, err := b.ReadAll(context.Background(), key)
	if err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return data
}

// hasObject reports whether the fixture's bucket holds key at all.
func hasObject(t *testing.T, f *cloudFixture, key string) bool {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	_, err = b.Attributes(context.Background(), key)
	return err == nil
}

// guardAttach counts the calls that reach the uploader, so a test can assert
// that a refusal happened before the first byte rather than after it.
func guardAttach(t *testing.T) *int {
	t.Helper()
	n := 0
	old := attachInputsFn
	attachInputsFn = func(ctx context.Context, b *blobx.Bucket, prefix string, paths []string, caps config.Inputs) ([]task.InputFile, error) {
		n++
		return old(ctx, b, prefix, paths, caps)
	}
	t.Cleanup(func() { attachInputsFn = old })
	return &n
}

// wantNoUpload asserts that a refused launch put nothing in the bucket: the
// uploader was never called, and no object lives under any run's inputs/.
// The keys are listed rather than guessed, so a launch that minted its own
// run id — the ones --attach refuses before it is even resolved — is covered
// too. Asserting only that the run never started would miss the failure this
// guards: an object uploaded under a run id that never gets a task.
func wantNoUpload(t *testing.T, f *cloudFixture, calls *int) {
	t.Helper()
	if *calls != 0 {
		t.Fatalf("the uploader ran %d time(s), want none", *calls)
	}
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	it := b.List(nil)
	for {
		obj, err := it.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(obj.Key, "/"+runstore.InputsDir+"/") {
			t.Fatalf("a refused launch left %s in the bucket", obj.Key)
		}
	}
}

// attachCheckout is a checkout of acme/app whose single workflow declares
// inputs caps (the literal "inputs: {…}" of the caller's own), so a test can
// trip one cap well under the defaults. It is the checkout --attach reads its
// caps from (attachCaps), as a launch made inside the repository does.
func attachCheckout(t *testing.T, inputs string) {
	t.Helper()
	appRepoCheckout(t, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
		"workflows:\n  web: { commands: { build: sh build.sh, render: sh render.sh, test: sh test.sh }, inputs: " + inputs + " }\n"})
}

// artifactAttachFixture is the common setup: the project's sizzle recipe and
// a job image new enough for it.
func artifactAttachFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
	writeBuildRecord(t, f, appSlug, "web", release080)
	return f
}

// TestAttachUploadsBeforeTheTaskSpecIsWritten is the order P7 asks for, seen
// from the launch itself: when the uploader runs, the run's task.json is not
// there yet, so the bytes are up before anything names them — and the task
// that is written afterwards carries the size and the sha256 of exactly what
// went up, in the order --attach named it.
//
// Mutation (move attachLaunch below createTask in finishLaunch): the uploader
// runs with the task already stored and this test fails on the first
// assertion.
func TestAttachUploadsBeforeTheTaskSpecIsWritten(t *testing.T) {
	f := artifactAttachFixture(t)
	id := "20261015-100000-abcd"
	shot := attachShot(t, "shot.png", "png-bytes")
	old := attachInputsFn
	sawTask := true
	attachInputsFn = func(ctx context.Context, b *blobx.Bucket, prefix string, paths []string, caps config.Inputs) ([]task.InputFile, error) {
		sawTask = hasObject(t, f, taskKey(id))
		return old(ctx, b, prefix, paths, caps)
	}
	t.Cleanup(func() { attachInputsFn = old })

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", id, "--attach", shot, "Render the nightly bundle"); err != nil {
		t.Fatal(err)
	}
	if sawTask {
		t.Fatal("the upload ran after the task was written, want before")
	}
	spec := readSpec(t, f, id)
	if len(spec.Inputs) != 1 {
		t.Fatalf("task inputs = %+v, want one", spec.Inputs)
	}
	sum := sha256.Sum256([]byte("png-bytes"))
	want := task.InputFile{Name: "shot.png", Bytes: int64(len("png-bytes")), SHA256: hex.EncodeToString(sum[:])}
	if spec.Inputs[0] != want {
		t.Fatalf("task input = %+v, want %+v", spec.Inputs[0], want)
	}
	if got := string(object(t, f, inputKey(id, "shot.png"))); got != "png-bytes" {
		t.Fatalf("uploaded bytes = %q", got)
	}
}

// TestAttachRefusesAnExistingRunIdBeforeUploading is Review Focus 11, the
// ordering bug this feature exists to avoid: an --attach launch whose run id
// already holds a task is refused BEFORE the caps are checked and before the
// first byte leaves the machine, and the stored task's bytes are untouched.
// Both shapes are asserted, because the difference between them is the whole
// reason --attach does not adopt: a DIFFERENT stored task (which a plain
// launch would refuse too, but only after an upload) and a BYTE-IDENTICAL one
// (which a plain launch adopts).
//
// Mutation (drop the attachRefusesStoredRun call from finishLaunch, or move
// it below attachLaunch): the launch uploads over the run's inputs/ before
// createTask looks at the task, and this test fails on the upload count, on
// the stored task's bytes and on the input object's.
func TestAttachRefusesAnExistingRunIdBeforeUploading(t *testing.T) {
	t.Run("a different stored task", func(t *testing.T) {
		f := artifactAttachFixture(t)
		id := "20261015-100000-abcd"
		stored := `{"version":1,"run_id":"` + id + `","repo":"acme/app","ref":"main","workflow":"web","task":"a different task"}`
		writeBucketFile(t, f, taskKey(id), stored)
		writeBucketFile(t, f, inputKey(id, "shot.png"), "old-bytes")
		n := guardAttach(t)
		shot := attachShot(t, "shot.png", "new-bytes")
		wantRefused(t, f, "already holds a task", "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", id, "--attach", shot, "Render the nightly bundle")
		if *n != 0 {
			t.Fatalf("%d uploads, want none", *n)
		}
		if got := string(object(t, f, taskKey(id))); got != stored {
			t.Fatalf("the stored task changed: %s", got)
		}
		if got := string(object(t, f, inputKey(id, "shot.png"))); got != "old-bytes" {
			t.Fatalf("the run's input was overwritten: %q", got)
		}
	})
	t.Run("a byte-identical stored task", func(t *testing.T) {
		f := artifactAttachFixture(t)
		id := "20261015-100000-abcd"
		args := []string{"run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", id, "Render the nightly bundle"}
		if _, _, err := execute(t, args...); err != nil {
			t.Fatal(err)
		}
		stored := object(t, f, taskKey(id))
		writeBucketFile(t, f, inputKey(id, "shot.png"), "old-bytes")
		n := guardAttach(t)
		shot := attachShot(t, "shot.png", "new-bytes")
		_, _, err := execute(t, append(args, "--attach", shot)...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already holds a task") {
			t.Fatalf("repeat with --attach: err = %v, want a refusal naming the stored task", err)
		}
		if *n != 0 {
			t.Fatalf("%d uploads, want none", *n)
		}
		if got := object(t, f, taskKey(id)); !bytes.Equal(got, stored) {
			t.Fatalf("the stored task changed:\n%s", got)
		}
		if got := string(object(t, f, inputKey(id, "shot.png"))); got != "old-bytes" {
			t.Fatalf("the run's input was overwritten: %q", got)
		}
	})
}

// TestARefusedLaunchLeavesNoInputsInTheBucket is the other half of the
// freshness check's promise (design §4 step 1, plan Review Focus 11): a
// refusal the launcher can make from what it already has "costs nothing and
// leaves nothing in the bucket", so the upload is the LAST thing an --attach
// launch does — after every gate that can still refuse it and before the
// task that names the files. Here the budget's global kill switch refuses the
// launch after the attach refusals and after the image gate have passed.
//
// Mutation (move the attachInputsFn call back above the gates, into
// attachLaunch): the upload runs, the run id never gets a task, and the file
// is left in the bucket under it — this test fails on the object.
func TestARefusedLaunchLeavesNoInputsInTheBucket(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	projectRecipesFixture(t, f.cloudFixture, map[string]string{"sizzle": projectSizzle})
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", release080)
	f.db.Set("config/kill/global", map[string]any{"on": true})
	id := "20261015-100000-abcd"
	shot := attachShot(t, "shot.png", "png")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", id, "--attach", shot, "Render the nightly bundle")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("err = %v, want the budget's own refusal", err)
	}
	if hasObject(t, f.cloudFixture, inputKey(id, "shot.png")) {
		t.Fatal("a refused launch left its attached file in the bucket under a run id that never got a task")
	}
	if hasObject(t, f.cloudFixture, taskKey(id)) {
		t.Fatal("the refused launch wrote a task")
	}
}

// TestAPlainLaunchStillAdoptsAByteIdenticalTask: the rule --attach does not
// change. A launch carrying no attachments repeats its run id exactly as it
// always has — a byte-identical stored task is adopted and the existing
// launch is reported, never refused.
//
// Mutation (make the run-id freshness check unconditional instead of gated on
// len(o.attach) > 0): the plain repeat is refused and this test fails.
func TestAPlainLaunchStillAdoptsAByteIdenticalTask(t *testing.T) {
	f := artifactAttachFixture(t)
	id := "20261015-100000-abcd"
	args := []string{"run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", id, "--json", "Render the nightly bundle"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	stored := object(t, f, taskKey(id))
	out, _, err := execute(t, args...)
	if err != nil || !strings.Contains(out, "already-launched") {
		t.Fatalf("plain repeat = %s, %v; want the launch reported", out, err)
	}
	if got := object(t, f, taskKey(id)); !bytes.Equal(got, stored) {
		t.Fatalf("the adopted task was rewritten:\n%s", got)
	}
}

// TestAttachRefusesMoreFilesThanTheCap: workflows.inputs.max_files counts
// PATHS, and a set over it is refused with the count it saw and the cap,
// before the first byte.
//
// Mutation (read the cap as per-file, or drop the count check): the launch
// uploads and this test fails on the missing refusal (and on the object).
func TestAttachRefusesMoreFilesThanTheCap(t *testing.T) {
	f := artifactAttachFixture(t)
	attachCheckout(t, "{ max_files: 2 }")
	args := []string{"run", "--repo", "acme/app", "--workflow", "web", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd"}
	for _, name := range []string{"one.png", "two.png", "three.png"} {
		args = append(args, "--attach", attachShot(t, name, "x"))
	}
	wantRefused(t, f, "max_files", append(args, "Render the nightly bundle")...)
}

// TestAttachRefusesAWhitespaceOnlyNameBeforeUploading: a name that is nothing
// but whitespace is a legal file name and a legal JSON string, so the
// launcher's own name rule (task.InputNameProblem, which asked only for
// non-empty) let it through — while the decoder that reads the task back
// (task.Spec.Validate's required-name check, which asks for
// strings.TrimSpace(name) != "") refused the same name. So --attach uploaded
// the file, createTask wrote the task.json naming it, and the runner's
// ReadTask refused the task at bootstrap: the bytes were in the bucket under a
// run that could never start, which is the one thing design §4 step 1's
// ordering exists to prevent. The two rules are one predicate now
// (task.InputNameBlank), and the refusal happens where every other name
// refusal happens: in attachPrepared, before the first byte.
//
// Mutation (drop the whitespace arm from InputNameProblem): the launch
// uploads, writes the task and starts, and this test fails on the missing
// refusal, on the upload count and on the object.
func TestAttachRefusesAWhitespaceOnlyNameBeforeUploading(t *testing.T) {
	f := artifactAttachFixture(t)
	n := guardAttach(t)
	shot := attachShot(t, "   ", "png")
	wantRefused(t, f, "is only whitespace", "run", "--repo", "acme/app", "--recipe", "sizzle",
		"--run-id", "20261015-100000-abcd", "--attach", shot, "Render the nightly bundle")
	wantNoUpload(t, f, n)
}

// TestAttachRefusesAnOversizedFile: workflows.inputs.max_bytes is PER FILE,
// and the refusal names that one file's own size beside it — a set of many
// small files is not what trips it, and the count is unaffected.
//
// Mutation (drop the per-file check from attachPrepared): the launch uploads
// and this test fails on the missing refusal. It is NOT pinned here by
// "check the set's total against max_bytes instead of each file": the running
// total crosses max_bytes at the big file, so that mutation refuses the same
// file with the same sentence — the other side of it is
// TestAttachAllowsASetWhoseTotalIsOverMaxBytes.
func TestAttachRefusesAnOversizedFile(t *testing.T) {
	f := artifactAttachFixture(t)
	attachCheckout(t, "{ max_files: 8, max_bytes: 4, max_total_bytes: 100 }")
	big := attachShot(t, "big.png", "12345")
	small := attachShot(t, "small.png", "xx")
	wantRefused(t, f, "max_bytes", "run", "--repo", "acme/app", "--workflow", "web", "--recipe", "sizzle",
		"--run-id", "20261015-100000-abcd", "--attach", small, "--attach", big, "Render the nightly bundle")
}

// TestAttachAllowsASetWhoseTotalIsOverMaxBytes: max_bytes is not a budget for
// the set — workflows.inputs.max_total_bytes is — so three files of 3 bytes
// each, 9 in all, pass a 4-byte per-file cap as long as the SET is under
// max_total_bytes. Reading max_bytes as a running total would pass every
// other test about it (each over-cap test has one file that crosses the cap
// alone) while refusing the ordinary case the config key promises: "the most
// ONE attached file may hold".
//
// Mutation (accumulate the total and compare that against max_bytes in
// attachPrepared): the launch is refused and this test fails.
func TestAttachAllowsASetWhoseTotalIsOverMaxBytes(t *testing.T) {
	f := artifactAttachFixture(t)
	attachCheckout(t, "{ max_files: 8, max_bytes: 4, max_total_bytes: 1024 }")
	id := "20261015-100000-abcd"
	args := []string{"run", "--repo", "acme/app", "--workflow", "web", "--recipe", "sizzle", "--run-id", id}
	for i, name := range []string{"a.png", "b.png", "c.png"} {
		args = append(args, "--attach", attachShot(t, name, strings.Repeat(string(rune('a'+i)), 3)))
	}
	if _, _, err := execute(t, append(args, "Render the nightly bundle")...); err != nil {
		t.Fatalf("three files each under max_bytes were refused: %v", err)
	}
	if got := readSpec(t, f, id); len(got.Inputs) != 3 {
		t.Fatalf("task inputs = %+v, want all three recorded", got.Inputs)
	}
}

// TestAttachRefusesASetOverTheTotal: the cap a set of files each comfortably
// under the per-file cap still trips. max_total_bytes bounds THE SET — the
// sum of the sizes about to be uploaded — and the refusal says so with the
// sum, not with one file's size and not with the count times the per-file
// cap.
//
// Mutation (check each file against max_total_bytes instead of the sum, or
// check bytes × max_files): three files of 6 bytes each pass, the launch
// uploads, and this test fails on the missing refusal.
func TestAttachRefusesASetOverTheTotal(t *testing.T) {
	f := artifactAttachFixture(t)
	attachCheckout(t, "{ max_files: 5, max_bytes: 10, max_total_bytes: 15 }")
	args := []string{"run", "--repo", "acme/app", "--workflow", "web", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd"}
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		args = append(args, "--attach", attachShot(t, name, "sixsix"))
	}
	wantRefused(t, f, "max_total_bytes", append(args, "Render the nightly bundle")...)
}

// TestAttachAllowsASetExactlyAtTheCaps is the other side of every cap: a
// launch exactly AT max_files, at max_bytes and at max_total_bytes is
// allowed. Without it, flipping one comparison to >= passes the whole suite
// while refusing the owner's ordinary set — sixteen files by default, the
// documented default max_files, is exactly the launch that would break.
//
// Mutation (change any of attachPrepared's three `>` comparisons to `>=`):
// this test fails at that boundary — the launch is refused, or the second
// file is missing from the bucket.
func TestAttachAllowsASetExactlyAtTheCaps(t *testing.T) {
	f := artifactAttachFixture(t)
	attachCheckout(t, "{ max_files: 2, max_bytes: 6, max_total_bytes: 12 }")
	id := "20261015-100000-abcd"
	one := attachShot(t, "one.png", "aaaaaa")
	two := attachShot(t, "two.png", "bbbbbb")
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--workflow", "web", "--recipe", "sizzle", "--run-id", id,
		"--attach", one, "--attach", two, "Render the nightly bundle"); err != nil {
		t.Fatalf("a set exactly at all three caps was refused: %v", err)
	}
	spec := readSpec(t, f, id)
	if len(spec.Inputs) != 2 {
		t.Fatalf("task inputs = %+v, want both files recorded", spec.Inputs)
	}
	for name, body := range map[string]string{"one.png": "aaaaaa", "two.png": "bbbbbb"} {
		if got := string(object(t, f, inputKey(id, name))); got != body {
			t.Fatalf("input %s = %q, want %q", name, got, body)
		}
	}
}

// TestAttachRefusesARecipeItCannotReadHere: a task that names a REPOSITORY
// recipe carries its name alone (taskRecipe), so the launcher resolves it from
// this checkout's copy of the file — and with no checkout there, or no such
// file in it, it has not read the recipe the run will use. The refusal is
// fail-closed and names the recipe, so the owner is not told "--attach needs
// an artifact recipe" as though they had named a non-artifact one when the
// truth is that the launcher could not tell.
//
// Mutation (drop the `case spec.Recipe != nil` arm): the generic refusal below
// fires and this test fails on the sentence that names the recipe.
func TestAttachRefusesARecipeItCannotReadHere(t *testing.T) {
	t.Chdir(t.TempDir())
	spec := &task.Spec{Version: 1, RunID: "20261015-100000-abcd", Repo: "acme/app", Ref: "main", Task: "Render the nightly bundle",
		Recipe: &task.Recipe{Name: "sizzle", Source: string(recipe.SourceRepo)}}
	err := checkAttachRecipe(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "recipe sizzle could not be read here") {
		t.Fatalf("checkAttachRecipe = %v, want a refusal naming the recipe it could not read", err)
	}
}

// TestAttachUploadsCreateOnly: every upload is a create-only write
// (blobx.PutFile's ifNotExist), and the arm that classifies the refusal is
// reachable even though the deterministic refusal — the run-id freshness
// check — comes first: a second launcher that won the run id in the meantime
// writes the object this one is about to, and this is what stops it
// overwriting the inputs of the run whose task is already there. The message
// says which run and which file, since the launcher lost a race it cannot see.
//
// Mutation (pass false for PutFile's ifNotExist, or drop the
// errors.Is(err, blobx.ErrExists) arm): the second upload overwrites the first
// and this test fails on the missing refusal and on the object's bytes.
func TestAttachUploadsCreateOnly(t *testing.T) {
	f := artifactAttachFixture(t)
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	id := "20261015-100000-abcd"
	writeBucketFile(t, f, inputKey(id, "shot.png"), "a second launcher's bytes")
	shot := attachShot(t, "shot.png", "these bytes")
	_, err = attachInputs(context.Background(), blobx.Wrap(b), "runs/"+appSlug+"/"+id+"/", []string{shot}, config.Inputs{})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "input shot.png is already in run runs/"+appSlug+"/"+id) {
		t.Fatalf("attachInputs = %v, want a refusal naming the file and the run", err)
	}
	if got := string(object(t, f, inputKey(id, "shot.png"))); got != "a second launcher's bytes" {
		t.Fatalf("the existing object was overwritten: %q", got)
	}
}

// TestAttachRefusesDuplicateBaseNames: two attachments with one base name
// would be one object and one entry, so the second is refused rather than
// overwriting the first — and the refusal names BOTH paths the user typed,
// because one base name and two directories is exactly the case where "shot.png
// is named twice" leaves the owner looking for the second one: the whole
// reason the two collide is that the paths differ.
//
// Mutation (drop the duplicate-name refusal): the second upload is a
// create-only write against the first and the launch fails at the bucket
// instead, with a message that names neither the file nor the reason; the
// test fails on the refusal. Mutation (print the base name twice instead of
// the two paths, the way it did): the test fails on the missing path.
func TestAttachRefusesDuplicateBaseNames(t *testing.T) {
	f := artifactAttachFixture(t)
	first := attachShot(t, "shot.png", "one")
	second := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(second, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd",
		"--attach", first, "--attach", second, "Render the nightly bundle")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	for _, p := range []string{first, second} {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("the refusal does not name %s:\n%v", p, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestAttachNeedsAResolvableRepo: --attach resolves its repository exactly as
// a plain launch does, and the origin of the checkout the launch is made from
// is enough (P7) — so `fugaro run --recipe sizzle --attach shot.png …` needs
// no --repo at all. A working directory with neither --repo nor an origin is
// the only refusal, and it comes before anything is uploaded.
//
// Mutation (resolve the repository from --repo alone): the first half fails
// with "no --repo"; mutation (drop the refusal and carry on with an empty
// repository): the second half launches instead of refusing.
func TestAttachNeedsAResolvableRepo(t *testing.T) {
	t.Run("the origin is enough", func(t *testing.T) {
		f := artifactAttachFixture(t)
		appRepoCheckout(t, nil)
		shot := attachShot(t, "shot.png", "png")
		if _, _, err := execute(t, "run", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd", "--attach", shot, "Render the nightly bundle"); err != nil {
			t.Fatal(err)
		}
		if got := readSpec(t, f, "20261015-100000-abcd").Repo; got != "acme/app" {
			t.Fatalf("task repo = %q, want acme/app from the origin", got)
		}
	})
	t.Run("neither", func(t *testing.T) {
		f := artifactAttachFixture(t)
		t.Chdir(t.TempDir())
		shot := attachShot(t, "shot.png", "png")
		wantRefused(t, f, "no origin remote here", "run", "--recipe", "sizzle", "--attach", shot, "Render the nightly bundle")
	})
}

// TestAttachGatesOnTheImageThatKnowsInputs: an attached file needs a job
// image whose runner knows about them (P9) — a spec carrying inputs is
// refused outright by an older runner, whose parser disallows unknown fields
// — and the refusal is the one that names --attach, not the mode: artifact
// one beside it, which would fire first if this gate ran after it. Nothing is
// uploaded either: the gate is before the upload, so the refusal leaves no
// object under the run id (the same assertion every other refusal test makes,
// which this one was missing).
//
// Mutation (drop the --attach image gate from attachLaunch): the launch is
// refused by the mode: artifact gate instead and this test fails on the
// sentence only --attach's own gate writes; mutation (move attachLaunch below
// the mode gate): the same.
func TestAttachGatesOnTheImageThatKnowsInputs(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
	writeBuildRecord(t, f, appSlug, "web", release075)
	shot := attachShot(t, "shot.png", "png")
	n := guardAttach(t)
	wantRefused(t, f, "--attach needs a job image whose runner knows attached files (fugaro "+artifactsSince+" or later)",
		"run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd", "--attach", shot, "Render the nightly bundle")
	wantNoUpload(t, f, n)
}

// TestAttachRefusesARunThatAlreadyLaunched: the freshness check covers the run
// id whose task.json is already stored, and this is the other half of the same
// run: launch.json (or result.json's execution) says the run started while no
// task.json is there to be read — a task deleted after the fact, a record
// backfilled without one. The whole --attach block sits inside the prior ==
// nil arm, so a launch that found a prior would write a task with no inputs
// and the files the owner named would never go anywhere: refused here, before
// the caps, before the gates and before the first byte.
//
// Mutation (drop the prior != nil refusal): the launch writes a task with no
// inputs, reports the existing launch, and this test fails on the missing
// refusal and on the task it left behind.
func TestAttachRefusesARunThatAlreadyLaunched(t *testing.T) {
	f := artifactAttachFixture(t)
	id := "20261015-100000-abcd"
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// The execution carries its run's FUGARO_RUN: next-0.8.5's
	// CheckRunExecution refuses a launch record naming an execution that does
	// not report this run (an execution of a shared job is told apart only by
	// it), so the seeded launch must be this run's own.
	exec := f.run.StartWithEnv(gcp.JobName(appSlug, "web"), map[string]string{"FUGARO_RUN": appSlug + "/" + id})
	if err := runstore.Open(b, appSlug, id).WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	shot := attachShot(t, "shot.png", "png")
	n := guardAttach(t)
	// wantRefused is not used here: it asserts the fixture has no execution
	// at all, and this test seeded the run's own (the launch it refuses).
	_, _, err = execute(t, "run", "--repo", "acme/app", "--recipe", "sizzle",
		"--run-id", id, "--attach", shot, "Render the nightly bundle")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "has already launched") {
		t.Fatalf("err = %v, want exit 1 naming the launch that already happened", err)
	}
	if got := len(f.run.Executions()); got != 1 {
		t.Fatalf("%d executions, want the seeded one alone", got)
	}
	wantNoUpload(t, f, n)
	if hasObject(t, f, taskKey(id)) {
		t.Fatal("the refused launch wrote a task for a run that had already launched")
	}
}

// attachFifo makes a FIFO at path whose other end this test holds open,
// read-write and never written to: a plain writer's open blocks until a
// reader's, and a FIFO no writer has ever opened reads as an empty file (an
// end of file, not a stream), so holding both ends is what gives a reader on
// it a read that never returns.
func attachFifo(t *testing.T, path string) {
	t.Helper()
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo %s: %v: %s", path, err, out)
	}
	w, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
}

// swapPathWhenTheUploadReads replaces path the moment attachInputs' upload is
// about to read the handle it judged, which is the window the single open
// exists to close: the check and the copy are as close together as the code
// can put them, and what the swap leaves at the path is what a second open
// would find. make runs exactly once.
func swapPathWhenTheUploadReads(t *testing.T, make func()) {
	t.Helper()
	old := attachStreamFn
	swapped := false
	attachStreamFn = func(b *blobx.Bucket, ctx context.Context, key string, f *os.File, st fs.FileInfo, ct string, ifNotExist bool) (int64, string, error) {
		if !swapped {
			swapped = true
			make()
		}
		return old(b, ctx, key, f, st, ct, ifNotExist)
	}
	t.Cleanup(func() { attachStreamFn = old })
}

// TestAttachUploadsTheFileItJudgedNotWhatThePathNamesNow: the caps and the
// mode are read a moment before the upload, and the path is not under this
// launcher's control — anything the same user is running can put another file
// there in between. What is judged and what is uploaded are therefore the same
// OPEN file: attachInputs opens each path once, judges that handle's own
// fstat, and streams that handle, so replacing the path with a FIFO that never
// ends cannot make this launch stream a byte of it. The object that goes up is
// the file --attach named, byte for byte, and the size recorded is that file's.
//
// Mutation (attachStreamFn streams the path again — blobx.PutFile by
// f.Name(), the pre-fix shape, verified): the upload opens the FIFO the swap
// left, blobx refuses it as a non-regular file, and this test fails with
// "uploading input shot.png: ... (it is not a regular file)".
func TestAttachUploadsTheFileItJudgedNotWhatThePathNamesNow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shot := attachShot(t, "shot.png", "png-bytes")
	swapPathWhenTheUploadReads(t, func() {
		// A different file at the same path, and one that never ends: the
		// inode the handle holds outlives the name.
		if err := os.Remove(shot); err != nil {
			t.Error(err)
		}
		attachFifo(t, shot)
	})

	b := blobx.Wrap(memblob.OpenBucket(nil))
	defer b.Close()
	inputs, err := attachInputs(ctx, b, "runs/"+appSlug+"/20261015-100000-abcd/", []string{shot}, config.Inputs{})
	if err != nil {
		t.Fatalf("attachInputs = %v, want the file the launcher judged uploaded", err)
	}
	sum := sha256.Sum256([]byte("png-bytes"))
	want := task.InputFile{Name: "shot.png", Bytes: int64(len("png-bytes")), SHA256: hex.EncodeToString(sum[:])}
	if len(inputs) != 1 || inputs[0] != want {
		t.Fatalf("inputs = %+v, want %+v", inputs, want)
	}
	data, _, err := b.Read(ctx, inputKey("20261015-100000-abcd", "shot.png"))
	if err != nil {
		t.Fatalf("the input was not uploaded: %v", err)
	}
	if string(data) != "png-bytes" {
		t.Fatalf("uploaded bytes = %q, want the file the launcher judged", data)
	}
}

// TestAttachUploadsTheSizeItJudgedNotThePathsSizeNow is the same window with
// only the SIZE changed, and it is the one no other layer catches: a file that
// fitted under workflows.inputs.max_bytes when the launcher looked can be
// replaced by one that does not, and blobx bounds what it streams by the size
// of the file it opens — the larger one. Uploading it would put bytes the cap
// refuses into the bucket and leave the runner to refuse them at bootstrap,
// with the run's input objects already up under a run id that never gets a
// task. What goes up is bounded by the judgement: the byte count and the
// sha256 in the task are the file the launcher checked.
//
// Mutation (attachStreamFn streams the path again — blobx.PutFile by
// f.Name(), verified): the replacement goes up whole, the recorded size is
// 4096 and its sha256 the replacement's, and this test fails on both.
func TestAttachUploadsTheSizeItJudgedNotThePathsSizeNow(t *testing.T) {
	ctx := context.Background()
	shot := attachShot(t, "shot.png", "png-bytes")
	big := filepath.Join(t.TempDir(), "big.png")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	caps := config.Inputs{MaxBytes: 16}
	if err := attachPrepared([]string{shot}, caps); err != nil {
		t.Fatalf("attachPrepared = %v, want the file that fits", err)
	}
	swapPathWhenTheUploadReads(t, func() {
		if err := os.Rename(big, shot); err != nil {
			t.Error(err)
		}
	})

	b := blobx.Wrap(memblob.OpenBucket(nil))
	defer b.Close()
	inputs, err := attachInputs(ctx, b, "runs/"+appSlug+"/20261015-100000-abcd/", []string{shot}, caps)
	if err != nil {
		t.Fatalf("attachInputs = %v, want the judged file uploaded", err)
	}
	sum := sha256.Sum256([]byte("png-bytes"))
	want := task.InputFile{Name: "shot.png", Bytes: int64(len("png-bytes")), SHA256: hex.EncodeToString(sum[:])}
	if len(inputs) != 1 || inputs[0] != want {
		t.Fatalf("inputs = %+v, want the judgement's size and hash %+v", inputs, want)
	}
}

// TestAttachRefusesASymlinkedPath: --attach uploads the file the owner named,
// and a link is not one: os.Stat would describe the target (its size, its
// mode) while the name stored and downloaded is the LINK's, so the run would
// hold a file whose recorded size is some other file's — and the bytes would
// come from wherever the link pointed, which is not a path this launch named.
// The check is an Lstat, so the refusal is made before the launch's gates and
// before anything is opened, and blobx.OpenSource's O_NOFOLLOW is the second
// line of defence for the upload itself (internal/blobx: the source a write
// streams is never the file behind a link).
//
// Mutation (os.Stat in attachPrepared, attachJudge's symlink arm removed): the
// link is described as a regular file of the target's size, the set is
// accepted, the launch reaches the upload and this test fails — on the unit
// assertion (the refusal is blobx's "too many levels of symbolic links", which
// is not the sentence the launcher writes) and on the uploader having run at
// all.
func TestAttachRefusesASymlinkedPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.png")
	if err := os.WriteFile(target, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "shot.png")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := attachPrepared([]string{link}, config.Inputs{})
	if err == nil || !strings.Contains(err.Error(), "is a symbolic link") {
		t.Fatalf("attachPrepared of a symlink = %v, want a refusal naming the link", err)
	}
	if !strings.Contains(err.Error(), link) {
		t.Fatalf("the refusal does not name the path the owner typed: %v", err)
	}
	f := artifactAttachFixture(t)
	n := guardAttach(t)
	wantRefused(t, f, "is a symbolic link", "run", "--repo", "acme/app", "--recipe", "sizzle",
		"--run-id", "20261015-100000-abcd", "--attach", link, "Render the nightly bundle")
	wantNoUpload(t, f, n)
}

// TestAttachRefusesNamesDifferingOnlyInCase is the launcher's half of Task
// 4's case rule (the runner's artifact names key on the FOLDED name): the run
// downloads its inputs into one directory, a local run writes them into the
// checkout on the operator's own filesystem, and a name that differs only in
// case is one name there — so the second file would replace the first while
// the task named both. Refused before the first byte, naming both paths.
//
// Mutation (key the duplicate rule on the exact name alone): A.png and a.png
// are two names, the set is accepted, both objects go up, and this test fails
// on the refusal.
func TestAttachRefusesNamesDifferingOnlyInCase(t *testing.T) {
	a := attachShot(t, "A.png", "upper")
	b := attachShot(t, "a.png", "lower")
	err := attachPrepared([]string{a, b}, config.Inputs{})
	if err == nil || !strings.Contains(err.Error(), "only in case") {
		t.Fatalf("attachPrepared of a case-fold pair = %v, want a refusal naming the pair", err)
	}
	for _, p := range []string{a, b} {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("the refusal does not name %s: %v", p, err)
		}
	}
	f := artifactAttachFixture(t)
	n := guardAttach(t)
	wantRefused(t, f, "only in case", "run", "--repo", "acme/app", "--recipe", "sizzle",
		"--run-id", "20261015-100000-abcd", "--attach", a, "--attach", b, "Render the nightly bundle")
	wantNoUpload(t, f, n)
}

// TestAttachRefusesNamesOneFullCaseFoldApart is the same rule asked with a
// pair a *simple* case fold keeps apart: ß folds to ss (and ﬁ to fi) under
// Unicode's full case folding, which is what a case-insensitive,
// normalization-insensitive filesystem — APFS, where a mac runs this — applies
// to the names in the one directory the inputs land in. A launcher keyed on
// strings.ToLower, or on the simple fold, accepts straße.png and strasse.png
// as two names and the second download then replaces the first while the task
// names both.
//
// Mutation (key the folded map on strings.ToLower or on the exact name): this
// pair is accepted and this test fails on the missing refusal.
func TestAttachRefusesNamesOneFullCaseFoldApart(t *testing.T) {
	a := attachShot(t, "straße.png", "sharp s")
	b := attachShot(t, "strasse.png", "two letters")
	err := attachPrepared([]string{a, b}, config.Inputs{})
	if err == nil || !strings.Contains(err.Error(), "only in case") {
		t.Fatalf("attachPrepared of a full-fold pair = %v, want a refusal naming the pair", err)
	}
	for _, p := range []string{a, b} {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("the refusal does not name %s: %v", p, err)
		}
	}
	f := artifactAttachFixture(t)
	n := guardAttach(t)
	wantRefused(t, f, "only in case", "run", "--repo", "acme/app", "--recipe", "sizzle",
		"--run-id", "20261015-100000-abcd", "--attach", a, "--attach", b, "Render the nightly bundle")
	wantNoUpload(t, f, n)
}

// TestAttachRefusesADevice: --attach uploads FILES, and a path that is not a
// regular file — a device, a FIFO, a socket — is refused for the same reason a
// directory is, one step further on: os.Stat describes it, and the size it
// reports is not the size of anything that could be uploaded (/dev/zero
// reports 0 and reads forever). The refusal is made from the same stat, before
// the caps, before the image gate and before the first read or write.
//
// Mutation (drop the !st.Mode().IsRegular() arm of attachPrepared): the
// launch streams /dev/zero into the run's bucket and this test fails on the
// missing refusal and on the object.
func TestAttachRefusesADevice(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("/dev/zero is not here")
	}
	// The refusal is attachPrepared's, made from the stat alone — no read of
	// the path happens, which is why the endless /dev/zero is safe to name
	// here at all (and why the red step for this arm can be run without
	// streaming a device into a bucket).
	if err := attachPrepared([]string{"/dev/zero"}, config.Inputs{}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("attachPrepared(/dev/zero) = %v, want a refusal naming its mode", err)
	}
	f := artifactAttachFixture(t)
	n := guardAttach(t)
	wantRefused(t, f, "not a regular file", "run", "--repo", "acme/app", "--recipe", "sizzle",
		"--run-id", "20261015-100000-abcd", "--attach", "/dev/zero", "Render the nightly bundle")
	if *n != 0 {
		t.Fatalf("%d uploads, want none", *n)
	}
	if hasObject(t, f, inputKey("20261015-100000-abcd", "zero")) {
		t.Fatal("a device was uploaded into the run")
	}
}

// TestAttachRefusesANonArtifactRecipe: an attachment is only meaningful to a
// run that produces files rather than a pull request, and the refusal names
// the recipe and its mode. It is the launcher's, and it comes before the caps
// and before the first byte, so an attachment never reaches the bucket on a
// launch that cannot use it — asserted on the bucket itself and on the
// uploader, not just on the run never starting: an object left under a run id
// that never gets a task is exactly what this ordering exists to prevent
// (design §4 step 1).
//
// Mutation (drop checkAttachRecipe from attachLaunch): the implement launch
// uploads the file into inputs/ and this test fails on the object.
func TestAttachRefusesANonArtifactRecipe(t *testing.T) {
	f := artifactAttachFixture(t)
	n := guardAttach(t)
	shot := attachShot(t, "shot.png", "png")
	wantRefused(t, f, "--attach needs an artifact recipe; default is mode: "+recipe.ModeImplement,
		"run", "--repo", "acme/app", "--recipe", "default", "--run-id", "20261015-100000-abcd", "--attach", shot, "Add a feature")
	wantNoUpload(t, f, n)
}

// TestAttachOnAFollowUpResolvesTheRecipeTheRunnerWill: a follow-up does not
// take agent.recipe from fugaro.yaml — the runner's resolveRecipe consults it
// only for a first run (`r.follow == nil && cfg.Agent.Recipe != ""`,
// internal/runner/recipe.go) and otherwise runs its default — and the
// launcher used to resolve that same key for a follow-up anyway. A
// fugaro.yaml naming an artifact recipe (here a REPOSITORY recipe, the one
// kind localRecipe can read) then made --attach look legal on a follow-up
// whose previous run carried no recipe at all (resolveFollowUp's
// `rcp == nil && c.Chosen == nil` arm, internal/cli/followup.go): the launcher
// uploaded the files, wrote the task naming them, and the run failed at
// bootstrap with "the task attached 1 file(s), but recipe default is mode:
// implement" — the bytes had left the machine for a run that could never read
// them, which is the whole thing the launcher's refusals exist to prevent.
//
// The previous run is seeded exactly that way — no recipe in its task, none in
// its record — and the job image is release080, so the image gate cannot
// refuse this launch first and stand in for the refusal under test.
//
// Mutation (drop the spec.IsFollowUp() arm of recipeForLaunch, so agent.recipe
// is resolved for a follow-up as for a first run): the recipe becomes sizzle
// — the fugaro.yaml artifact recipe — and the refusal moves to the arm above
// this one ("a follow-up inherits the recipe it resolves…"), which is
// mode-keyed rather than name-keyed: this test fails on the missing sentence
// (nothing is uploaded either way, because that arm is a refusal too, not a
// hole). The hole this test was written for was the arms not existing at all;
// the counting assertions below are here for the regressions that would put
// the upload back in front of them.
func TestAttachOnAFollowUpResolvesTheRecipeTheRunnerWill(t *testing.T) {
	f := newCloudFixture(t)
	appRepoCheckout(t, map[string]string{
		"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\nagent:\n  recipe: sizzle\n" +
			"workflows:\n  web: { commands: { build: sh build.sh, render: sh render.sh, test: sh test.sh } }\n",
		recipe.RepoPath("sizzle"): projectSizzle,
	})
	writeBuildRecord(t, f, appSlug, "web", release080)
	seedSpec(t, f, firstRunSpec(rootID), false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
	calls := guardAttach(t)
	shot := attachShot(t, "shot.png", "png-bytes")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--attach", shot, "Render the bundle")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "a follow-up never takes agent.recipe") ||
		!strings.Contains(err.Error(), "mode: implement") {
		t.Fatalf("err = %v, want a refusal naming the recipe the runner would run", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
	wantNoUpload(t, f, calls)
}

// TestAttachOnAFollowUpWithAnArtifactDefaultRecipeIsRefused: the other half
// of the follow-up hole. The refusal above keys on the recipe the runner will
// really run, and that recipe can itself be mode: artifact — for a follow-up
// that inherited nothing, the runner resolves the repository's own
// .fugaro/recipes/default.yaml at this task's base, and a repository may
// commit an artifact one there. The launcher's ALLOW arm (a resolved mode:
// artifact recipe is what --attach needs) then fired for a follow-up, and
// gateAttachRecipe's follow-up arm below it never ran: the files were
// uploaded, the task written, and the run refused at bootstrap — the runner
// refuses EVERY follow-up whose recipe is mode: artifact, whatever resolved
// it (runner.go, `r.follow != nil && rcp.Mode == recipe.ModeArtifact`),
// because a follow-up inherits its recipe rather than choosing one and an
// artifact run opens no pull request for one to continue.
//
// So an allow arm for mode: artifact is only ever right off a follow-up:
// a launch that continues a pull request can never be an artifact run.
// The sentence is asserted rather than the two words "follow-up" and "mode:
// artifact" the generic follow-up arm further down also prints, so that
// dropping THIS arm is not silently covered by that one.
//
// Mutation (drop the `spec.IsFollowUp() && rcp.Mode == recipe.ModeArtifact`
// arm): the launch reaches the allow arm — which is what the review's
// `!spec.IsFollowUp()` guard on it defends, the arm here being the one that
// fires first — and this test fails on the missing sentence, on the
// execution count and on the object that is already in the bucket.
func TestAttachOnAFollowUpWithAnArtifactDefaultRecipeIsRefused(t *testing.T) {
	f := newCloudFixture(t)
	appRepoCheckout(t, map[string]string{
		"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
			"workflows:\n  web: { commands: { build: sh build.sh, render: sh render.sh, test: sh test.sh } }\n",
		recipe.RepoPath(recipe.DefaultName): projectSizzle,
	})
	writeBuildRecord(t, f, appSlug, "web", release080)
	seedSpec(t, f, firstRunSpec(rootID), false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
	calls := guardAttach(t)
	shot := attachShot(t, "shot.png", "png-bytes")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--attach", shot, "Render the bundle")
	if ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "the runner refuses every follow-up whose recipe is mode: artifact") {
		t.Fatalf("err = %v, want a refusal saying a follow-up can never run an artifact recipe", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
	wantNoUpload(t, f, calls)
}

// TestAttachOnAFollowUpThatCarriesArtifactModeIsRefused: the allow arm keyed
// on the task's OWN mode is gated the same way, and for the same reason — no
// follow-up is ever an artifact run. The mode is only reachable here from a
// spec this CLI did not build (a stored task re-launched, or a forged one:
// resolveFollowUp writes no mode, and both ways a follow-up could pick one are
// refused by refuseFollowUpArtifactMode and refuseFollowUpRepoRecipe), which
// is exactly why the gate has to be on the check and not on the writer.
//
// Mutation (restore `spec.Mode == recipe.ModeArtifact` as the early return,
// without `&& !spec.IsFollowUp()`): this spec is allowed and the test fails.
func TestAttachOnAFollowUpThatCarriesArtifactModeIsRefused(t *testing.T) {
	t.Chdir(t.TempDir())
	spec := &task.Spec{Version: 1, RunID: "20261015-100000-abcd", Repo: "acme/app", Ref: "main",
		Branch: "fugaro/20261015-090000-aaaa", PR: 7, PreviousRun: "20261015-090000-aaaa",
		Task: "Render the nightly bundle", Mode: recipe.ModeArtifact}
	err := checkAttachRecipe(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "a follow-up is never an artifact run") {
		t.Fatalf("checkAttachRecipe = %v, want a refusal of an artifact mode on a follow-up", err)
	}
}

// TestAttachNeverReadsADefaultRecipeFromTheWorkingDirectory: the repository
// layer is read from a checkout OF THE TASK'S REPOSITORY, and with no such
// checkout there is no root to read it from — an empty root handed to
// recipe.ReadRepoFile is a RELATIVE path, which the process resolves against
// its own working directory. A directory that merely happens to hold
// .fugaro/recipes/default.yaml (here an artifact one) would then decide a
// follow-up's launch: --attach would be allowed, the files uploaded, and the
// runner — which reads the repository layer at the task's base ref, not from
// this directory — would run the catalog default and refuse the task at
// bootstrap, with the bytes already in the bucket.
//
// Mutation (read the repository layer with an empty root, i.e. drop the
// `root != ""` guard from localRecipeNamed): the artifact recipe in the
// working directory is resolved, the refusal disappears and this test fails.
func TestAttachNeverReadsADefaultRecipeFromTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".fugaro", "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, recipe.RepoPath(recipe.DefaultName)), []byte(projectSizzle), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	spec := &task.Spec{Version: 1, RunID: "20261015-100000-abcd", Repo: "acme/app", Ref: "main",
		Branch: "fugaro/20261015-090000-aaaa", PR: 7, PreviousRun: "20261015-090000-aaaa", Task: "Render the nightly bundle"}
	err := checkAttachRecipe(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "a follow-up never takes agent.recipe") || !strings.Contains(err.Error(), "mode: implement") {
		t.Fatalf("checkAttachRecipe = %v, want the catalog default resolved rather than a file from the working directory", err)
	}
}

// TestAttachRefusesARecipeTheRunnerChooses: the arm a launcher-side refusal
// can only take by declining to guess. With no recipe in the task, no
// follow-up and no checkout here, the runner picks the recipe itself
// (agent.recipe at the ref, else default), so this CLI cannot promise an
// artifact run — and being unable to prove it is not a reason to upload the
// files anyway. The refusal names the fix, and nothing reached the bucket.
//
// Mutation (drop the runnerChooses arm of checkAttachRecipe): the launch
// uploads the file and starts a run whose mode nobody checked; this test then
// fails on the missing refusal and on the object.
func TestAttachRefusesARecipeTheRunnerChooses(t *testing.T) {
	f := artifactAttachFixture(t)
	t.Chdir(t.TempDir())
	n := guardAttach(t)
	shot := attachShot(t, "shot.png", "png")
	wantRefused(t, f, "leaves the recipe to the runner",
		"run", "--repo", "acme/app", "--attach", shot, "Render the nightly bundle")
	wantNoUpload(t, f, n)
}

// TestAttachAllowsAnArtifactRecipeWithNoModeStamped: the recipe refusal's
// allow arm is keyed on the RESOLVED recipe being mode: artifact, not on the
// task carrying a mode. A task is allowed to carry none (design §8.2's empty
// Mode), and the CLI's own chooseRecipe stamps it for the launches it
// resolves — so keying the refusal on "a recipe resolved" alone would leave
// the allow arm resting on that stamp, and would answer a genuinely artifact
// recipe with "‑‑attach needs an artifact recipe; sizzle is mode: artifact".
//
// Mutation (add `&& spec.Mode == recipe.ModeArtifact` to the allow arm, i.e.
// let it rest on the stamp): the refusal below fires with exactly the message
// named above and this test fails. Dropping `&& rcp.Mode ==
// recipe.ModeArtifact` instead is NOT caught here — it widens the arm to
// `rcp != nil`, which still allows this spec; TestAttachRefusesANonArtifactRecipe
// is the test that dies under that one.
func TestAttachAllowsAnArtifactRecipeWithNoModeStamped(t *testing.T) {
	t.Chdir(t.TempDir())
	spec := &task.Spec{Version: 1, RunID: "20261015-100000-abcd", Repo: "acme/app", Ref: "main", Task: "Render the nightly bundle",
		Recipe: &task.Recipe{Name: "sizzle", Source: string(recipe.SourceCatalog), YAML: projectSizzle}}
	if err := checkAttachRecipe(context.Background(), spec); err != nil {
		t.Fatalf("checkAttachRecipe = %v, want an artifact recipe without a stamped mode allowed", err)
	}
}

// TestAttachNamesTheBackgroundModelTheRunWillActuallyUse is the other half of
// D9's rule: the third model the set judges is the one the run will really
// send, config.EffectiveBackground — the coder's own model whenever a
// provider claims the coder and no background model was pinned — never the
// raw Models.Background, which is empty there. Judging the raw field would
// print "Anthropic (direct)" for a run whose every call a provider serves,
// telling the owner their screenshot goes to Anthropic when it does not.
//
// Mutation (iterate ag.PinnedModels() instead of NeedsAnthropicKey's own
// three models): the empty background pin is read as a model no provider
// claims, the direct route is added, and this test fails on it.
func TestAttachNamesTheBackgroundModelTheRunWillActuallyUse(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"acme-gw": {Models: []string{"coder-*", "reviewer-*"}},
	}}
	// api-key is the auth the direct route belongs to (TestAttachNamesThe-
	// RouteTheAuthModeActuallyTakes covers the other two).
	ag := config.Agent{Auth: "api-key", Models: config.ModelRoles{Coder: "coder-1", Reviewer: "reviewer-1"}}
	if got := config.EffectiveBackground(ag, lc.Providers); got != "coder-1" {
		t.Fatalf("EffectiveBackground = %q, want the coder's model", got)
	}
	if config.NeedsAnthropicKey(ag, lc.Providers, "acme/app") {
		t.Fatal("this configuration makes no Anthropic call; the test's premise moved")
	}
	got := attachProviders(lc, ag, "acme/app")
	if strings.Join(got, ",") != "acme-gw" {
		t.Fatalf("attachProviders = %v, want the claiming provider alone", got)
	}
	var buf bytes.Buffer
	attachProviderWarning(&buf, got)
	if strings.Contains(buf.String(), attachDirectAnthropic) {
		t.Fatalf("the line names Anthropic for a run that makes no Anthropic call: %q", buf.String())
	}
	// A background model the owner pinned explicitly is judged as itself.
	ag.Models.Background = "bg-1"
	if got := attachProviders(lc, ag, "acme/app"); strings.Join(got, ",") != "acme-gw,"+attachDirectAnthropic {
		t.Fatalf("attachProviders with a pinned background = %v, want the direct route too", got)
	}
}

// TestAttachNamesTheProvidersThatMayReceiveTheFile is D9's set, computed: the
// destinations are every provider that claims one of the run's models AND may
// receive this repository's data, plus "Anthropic (direct)" whenever a model
// is unclaimed — and a provider this repository may NOT send code to is not
// one of them, so its model's route is the direct one.
//
// Mutation (read the set from lc.Providers alone): the second case lists a
// provider the repository is not allowed to use and loses the direct route,
// and this test fails on both.
func TestAttachNamesTheProvidersThatMayReceiveTheFile(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"acme-gw":  {Models: []string{"coder-*"}},
		"other-gw": {Models: []string{"reviewer-*"}},
	}}
	ag := config.Agent{Auth: "api-key", Models: config.ModelRoles{Coder: "coder-1", Reviewer: "reviewer-1", Background: "coder-2"}}
	got := attachProviders(lc, ag, "acme/app")
	if strings.Join(got, ",") != "acme-gw,other-gw" {
		t.Fatalf("attachProviders = %v, want both claiming providers and no direct route", got)
	}
	var buf bytes.Buffer
	attachProviderWarning(&buf, got)
	for _, want := range []string{"acme-gw", "other-gw"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the warning does not name %s: %q", want, buf.String())
		}
	}
	// A provider this repository may not send code to is not a destination:
	// its model goes direct, exactly as runner.ProvidersEnv leaves it.
	closed := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"acme-gw": {Models: []string{"coder-*"}, AllowDataTo: []string{"other/repo"}},
	}}
	if got := attachProviders(closed, ag, "acme/app"); strings.Join(got, ",") != attachDirectAnthropic {
		t.Fatalf("attachProviders with a closed provider = %v, want only the direct route", got)
	}
}

// TestAttachNamesTheRouteTheAuthModeActuallyTakes: the destination of a model
// no provider claims is the run's OWN auth route, and agent.auth decides which
// one it is (design §8 item 8): vertex — config.applyDefaults' default, so an
// unset Agent.Auth too — sends Claude calls to Google Vertex AI, api-key to
// Anthropic's own API, and oauth to Anthropic on a subscription's token. A
// default-configuration launch therefore must not be told its screenshot goes
// straight to Anthropic: the request, and the bytes in it, go to Google.
//
// Mutation (name the direct route regardless of auth, the way this file did
// before this test): the vertex and unset cases print "Anthropic (direct)"
// and fail.
func TestAttachNamesTheRouteTheAuthModeActuallyTakes(t *testing.T) {
	for _, tc := range []struct{ auth, want string }{
		{"vertex", attachRouteVertex},
		{"", attachRouteVertex}, // unset: applyDefaults writes vertex
		{"api-key", attachDirectAnthropic},
		{"oauth", attachRouteOAuth},
	} {
		ag := config.Agent{Auth: tc.auth}
		if got := attachProviders(nil, ag, "acme/app"); strings.Join(got, ",") != tc.want {
			t.Errorf("attachProviders(auth %q) = %v, want %q", tc.auth, got, tc.want)
		}
		var buf bytes.Buffer
		attachProviderWarning(&buf, attachProviders(nil, ag, "acme/app"))
		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("the destination line for auth %q does not name %q:\n%s", tc.auth, tc.want, buf.String())
		}
	}
	// The default route is not Anthropic, and saying so is the whole of this
	// finding: a vertex launch must not name a destination its bytes miss.
	if got := attachProviders(nil, config.Agent{Auth: "vertex"}, "acme/app"); slices.Contains(got, attachDirectAnthropic) {
		t.Fatalf("a vertex launch names %s: %v", attachDirectAnthropic, got)
	}
}

// TestAttachProviderWarningIsSilentWithoutAttachments: it is printed once per
// launch and only when --attach carried a file, so a plain artifact launch —
// the common case — says nothing about destinations.
//
// Mutation (print the line unconditionally): this test fails on the plain
// launch's own stderr.
func TestAttachProviderWarningIsSilentWithoutAttachments(t *testing.T) {
	f := artifactAttachFixture(t)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd", "Render the nightly bundle")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "--attach") {
		t.Fatalf("a launch with no attachments printed the destination line:\n%s", errOut)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the plain launch to have started", n)
	}
}

// TestAttachProviderWarningNamesTheDefaultAuthRoute is D9 end to end, and it
// names what a default configuration really does: a workflow with no
// providers configured at all and no agent.auth of its own — applyDefaults
// writes vertex — prints the Vertex route, never a line telling the owner
// their screenshot "routes nowhere" and never one sending it "direct" to
// Anthropic while the request goes to Google.
//
// Mutation (name the direct route whatever the auth says, or build the set
// from lc.Providers alone): the line names no destination, or names the
// wrong one, and this test fails on it.
func TestAttachProviderWarningNamesTheDefaultAuthRoute(t *testing.T) {
	f := artifactAttachFixture(t)
	shot := attachShot(t, "shot.png", "png")
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd", "--attach", shot, "Render the nightly bundle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, attachRouteVertex) {
		t.Fatalf("the destination line does not name %s:\n%s", attachRouteVertex, errOut)
	}
	if strings.Contains(errOut, attachDirectAnthropic) {
		t.Fatalf("a default (vertex) launch named %s:\n%s", attachDirectAnthropic, errOut)
	}
	if strings.Contains(errOut, "route nowhere") {
		t.Fatalf("a default Claude workflow was told its file goes nowhere:\n%s", errOut)
	}
	if got := readSpec(t, f, "20261015-100000-abcd"); len(got.Inputs) != 1 {
		t.Fatalf("task inputs = %+v, want the attachment recorded", got.Inputs)
	}
}

// TestAttachProviderWarningSaysLocalOnlyWhenNothingRoutes: the empty set is
// the only case the local wording is for — no claiming provider AND no
// Anthropic path — and it says the bytes stay on this machine rather than
// printing a blank destination.
//
// Mutation (print the sentence with an empty list of names): the line reads
// "and so reach ." and this test fails.
func TestAttachProviderWarningSaysLocalOnlyWhenNothingRoutes(t *testing.T) {
	for _, providers := range [][]string{nil, {}} {
		var buf bytes.Buffer
		attachProviderWarning(&buf, providers)
		if !strings.Contains(buf.String(), "route nowhere") || !strings.Contains(buf.String(), "stay on this machine") {
			t.Fatalf("the empty set prints %q, want the local wording", buf.String())
		}
		if strings.Contains(buf.String(), "and so reach") {
			t.Fatalf("the empty set prints a blank destination: %q", buf.String())
		}
	}
}

// TestAttachProviderWarningIsNotAGate: the line is printed and the launch
// proceeds. It is D9's whole mechanism, and D9 refuses nothing — the policy
// that already governs the model's own data governs this.
//
// Mutation (return the destination set as an error): the launch is refused
// and this test fails on the error and on the execution count.
func TestAttachProviderWarningIsNotAGate(t *testing.T) {
	f := artifactAttachFixture(t)
	shot := attachShot(t, "shot.png", "png")
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd", "--attach", shot, "Render the nightly bundle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "so reach") {
		t.Fatalf("the destination line was not printed:\n%s", errOut)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the launch to have started", n)
	}
}
