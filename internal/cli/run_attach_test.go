package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
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

// TestAttachRefusesAnOversizedFile: workflows.inputs.max_bytes is PER FILE,
// and the refusal names that one file's own size beside it — a set of many
// small files is not what trips it, and the count is unaffected.
//
// Mutation (check the set's total against max_bytes instead of each file, or
// drop the per-file check): the launch uploads and this test fails on the
// missing refusal.
func TestAttachRefusesAnOversizedFile(t *testing.T) {
	f := artifactAttachFixture(t)
	attachCheckout(t, "{ max_files: 8, max_bytes: 4, max_total_bytes: 100 }")
	big := attachShot(t, "big.png", "12345")
	small := attachShot(t, "small.png", "xx")
	wantRefused(t, f, "max_bytes", "run", "--repo", "acme/app", "--workflow", "web", "--recipe", "sizzle",
		"--run-id", "20261015-100000-abcd", "--attach", small, "--attach", big, "Render the nightly bundle")
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

// TestAttachRefusesDuplicateBaseNames: two attachments with one base name
// would be one object and one entry, so the second is refused rather than
// overwriting the first.
//
// Mutation (drop the duplicate-name refusal): the second upload is a
// create-only write against the first and the launch fails at the bucket
// instead, with a message that names neither the file nor the reason; the
// test fails on wantRefused's own wording.
func TestAttachRefusesDuplicateBaseNames(t *testing.T) {
	f := artifactAttachFixture(t)
	first := attachShot(t, "shot.png", "one")
	second := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(second, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, f, "named twice", "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd",
		"--attach", first, "--attach", second, "Render the nightly bundle")
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
// one beside it, which would fire first if this gate ran after it.
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
	wantRefused(t, f, "--attach needs a job image whose runner knows attached files (fugaro "+artifactsSince+" or later)",
		"run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd", "--attach", shot, "Render the nightly bundle")
}

// TestAttachRefusesANonArtifactRecipe: an attachment is only meaningful to a
// run that produces files rather than a pull request, and the refusal names
// the recipe and its mode. It is the launcher's, and it comes before the caps
// and before the first byte, so an attachment never reaches the bucket on a
// launch that cannot use it.
//
// Mutation (drop checkAttachRecipe from attachLaunch): the implement launch
// uploads and starts a job.
func TestAttachRefusesANonArtifactRecipe(t *testing.T) {
	f := artifactAttachFixture(t)
	shot := attachShot(t, "shot.png", "png")
	wantRefused(t, f, "--attach needs an artifact recipe; default is mode: "+recipe.ModeImplement,
		"run", "--repo", "acme/app", "--recipe", "default", "--run-id", "20261015-100000-abcd", "--attach", shot, "Add a feature")
}

// TestAttachRefusesARecipeTheRunnerChooses: the arm a launcher-side refusal
// can only take by declining to guess. With no recipe in the task, no
// follow-up and no checkout here, the runner picks the recipe itself
// (agent.recipe at the ref, else default), so this CLI cannot promise an
// artifact run — and being unable to prove it is not a reason to upload the
// files anyway. The refusal names the fix.
//
// Mutation (drop the runnerChooses arm of checkAttachRecipe): the launch
// uploads the file and starts a run whose mode nobody checked.
func TestAttachRefusesARecipeTheRunnerChooses(t *testing.T) {
	f := artifactAttachFixture(t)
	t.Chdir(t.TempDir())
	shot := attachShot(t, "shot.png", "png")
	wantRefused(t, f, "leaves the recipe to the runner",
		"run", "--repo", "acme/app", "--attach", shot, "Render the nightly bundle")
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
	ag := config.Agent{Models: config.ModelRoles{Coder: "coder-1", Reviewer: "reviewer-1", Background: "coder-2"}}
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

// TestAttachProviderWarningNamesAnthropicDirect is D9's round-3 correction,
// end to end: a default Claude workflow with no providers configured at all
// prints "Anthropic (direct)" — never a line telling the owner their
// screenshot "routes nowhere" while it is on its way to Anthropic.
//
// Mutation (build the set from lc.Providers alone): the line names no
// destination and this test fails on the missing provider.
func TestAttachProviderWarningNamesAnthropicDirect(t *testing.T) {
	f := artifactAttachFixture(t)
	shot := attachShot(t, "shot.png", "png")
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--recipe", "sizzle", "--run-id", "20261015-100000-abcd", "--attach", shot, "Render the nightly bundle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, attachDirectAnthropic) {
		t.Fatalf("the destination line does not name %s:\n%s", attachDirectAnthropic, errOut)
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
