package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/task"
	"github.com/spf13/cobra"
)

// TestRunAttemptsLaunchesNWithOneBatch is generic-tool plan Task 13's test:
// --attempts N launches N runs of the same task under one batch label, and
// refuses the flag combinations a best-of-N batch makes no sense with.
func TestRunAttemptsLaunchesNWithOneBatch(t *testing.T) {
	f := newCloudFixture(t)
	setMaxParallel(t, 5)
	// review-only needs a job image with a build record (needsRecipeImage);
	// without one, --recipe review-only would be refused there instead of
	// by the mode: review check this test means to exercise.
	writeBuildRecord(t, f, appSlug, "web", release070)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "3", "--json", "fix the flaky test")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Batch string `json:"batch"`
		Runs  []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil || len(doc.Runs) != 3 || !strings.HasPrefix(doc.Batch, "bestof-") {
		t.Fatalf("out = %s", out)
	}
	seen := map[string]bool{}
	for _, r := range doc.Runs {
		if r.RunID == "" || seen[r.RunID] {
			t.Fatalf("run ids: %v", doc.Runs)
		}
		seen[r.RunID] = true
	}
	if !strings.HasSuffix(doc.Batch, doc.Runs[0].RunID) {
		t.Fatalf("batch %q does not name the first run %q", doc.Batch, doc.Runs[0].RunID)
	}
	if n := len(f.run.Executions()); n != 3 {
		t.Fatalf("executions = %d, want 3", n)
	}
	// Each bad case asserts the message it is actually meant to pin, not
	// only ExitUserError: a review of this test (design generic-tool
	// review, 2026-10-13, item 3) found that checking only the exit code
	// let several of these go green for the wrong reason — see
	// TestRunAttemptsRangeIsTwoToFour and TestRunAttemptsRunIDIsRefused for
	// the two cases this loop can't safely assert a message for (the
	// fixture's capacity is already spent by the batch above, so --attempts
	// 5 here is refused by max_parallel, not necessarily by the range
	// check; a fresh fixture is needed to tell them apart).
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--attempts", "2", "--pr", "4"}, "takes no --pr"},
		{[]string{"--attempts", "2", "--recipe", "review-only"}, "mode: review"},
	} {
		args := append([]string{"run", "--repo", "acme/app"}, tc.args...)
		args = append(args, "x")
		if _, _, err := execute(t, args...); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want it to contain %q", tc.args, err, tc.want)
		}
	}
}

// TestRunAttemptsRangeIsTwoToFour pins --attempts's own range check in
// isolation (design generic-tool review, 2026-10-13, item 3): a fresh
// fixture with plenty of max_parallel headroom, so a boundary value is
// always refused by the range check's own message, never by an unrelated
// cap the same fixture happens to also be short on.
func TestRunAttemptsRangeIsTwoToFour(t *testing.T) {
	newCloudFixture(t)
	setMaxParallel(t, 10)
	for _, n := range []string{"-1", "0", "1", "5"} {
		_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", n, "x")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "use 2 to 4") {
			t.Errorf("--attempts %s: err = %v", n, err)
		}
	}
}

// TestRunAttemptsRunIDIsRefused pins --run-id's exclusion on its own: a
// review of this test (design generic-tool review, 2026-10-13, item 3)
// found no case had ever named --run-id, so removing it from the
// exclusion list (internal/cli/run.go) stayed green — without the
// refusal, every attempt would reuse the one given id and, once
// buildAttemptSpec stopped generating a fresh one per attempt, collide and
// dedupe silently (item 1).
func TestRunAttemptsRunIDIsRefused(t *testing.T) {
	newCloudFixture(t)
	setMaxParallel(t, 10)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--run-id", "20260927-100000-aaaa", "x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "takes no --run-id") {
		t.Fatalf("err = %v", err)
	}
}

// TestRunAttemptsNeedsRoomForAll pins the preflight cap check (design
// generic-tool G13, plan Task 13 step 1): with max_parallel: 2 (the
// fixture's default), --attempts 3 is refused before any launch, counting
// the whole batch against the running total, not one run at a time. A
// batch must never launch partially because of the cap.
func TestRunAttemptsNeedsRoomForAll(t *testing.T) {
	f := newCloudFixture(t) // max_parallel: 2
	_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "3", "task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "max_parallel") || !strings.Contains(err.Error(), "--attempts 3 needs 3") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("executions = %d, want 0: a refused batch must not launch any of it", n)
	}
}

// TestRunAttemptsRetry pins that --attempts and --retry are refused
// together: --retry replays a stored task as it is, which a fresh N-run
// batch contradicts.
func TestRunAttemptsRetry(t *testing.T) {
	newCloudFixture(t)
	setMaxParallel(t, 5)
	// A review of this test (design generic-tool review, 2026-10-13, item
	// 3) found that checking only the exit code did not actually pin
	// --attempts's own exclusion of --retry: if --retry were removed from
	// that exclusion list, the run id below (never stored) would still be
	// refused, with ExitUserError, once retrySpec looked it up — a
	// different refusal the old assertion could not tell apart from the
	// right one. Asserting the message does.
	_, _, err := execute(t, "run", "--attempts", "2", "--retry", "20260927-100000-abcd")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "takes no --retry") {
		t.Fatalf("err = %v", err)
	}
}

// TestRunAttemptsKeepsExplicitBatch pins that --batch, when given, names
// the batch instead of the default bestof-<first run id>.
func TestRunAttemptsKeepsExplicitBatch(t *testing.T) {
	f := newCloudFixture(t)
	setMaxParallel(t, 5)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--batch", "nightly", "--json", "task")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Batch string `json:"batch"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil || doc.Batch != "nightly" {
		t.Fatalf("out = %s", out)
	}
	if n := len(f.run.Executions()); n != 2 {
		t.Fatalf("executions = %d, want 2", n)
	}
}

// TestRunAttemptsRefusesReviewModeBeforeMaxParallel pins the ordering a
// review raised (design generic-tool review, 2026-10-13): a batch naming a
// mode: review recipe is refused by resolving its plan, before
// checkMaxParallelFor's backend listing call or the budget pre-check's
// database read ever run, so the message is always about the recipe, never
// about an unrelated cap the same request happens to also miss. The
// fixture's max_parallel is shrunk to 1 (below --attempts 2) so a
// wrongly-ordered cap check would refuse this too, and could be mistaken
// for the right refusal if both just returned ExitUserError.
func TestRunAttemptsRefusesReviewModeBeforeMaxParallel(t *testing.T) {
	newCloudFixture(t)
	setMaxParallel(t, 1)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--recipe", "review-only", "task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "mode: review") || strings.Contains(err.Error(), "max_parallel") {
		t.Fatalf("err = %v", err)
	}
	// An attempts-specific refusal (G13 review item 6), not the plain
	// launch's "launch it with --recipe X --pr N" advice: --attempts
	// itself refuses --pr, so that advice would describe something the
	// user's very next attempt at the suggested fix would also be refused
	// for.
	if strings.Contains(err.Error(), "launch it with --recipe") {
		t.Fatalf("err = %v, used the plain-launch refusal message", err)
	}
}

// TestReportBatchFailurePrintsWhatLaunched pins plan Task 13 step 3's
// "a failure after k launches stops the loop, prints the k launched runs and
// returns the error": in JSON mode (where nothing was printed per attempt,
// unlike text mode, which already streamed each run as it launched) the
// partial batch is printed before the error returns; in text mode nothing
// more is printed, since each launched run already was.
func TestReportBatchFailurePrintsWhatLaunched(t *testing.T) {
	results := []launchResult{{RunID: "20260927-100000-aaaa"}}
	boom := errors.New("boom")

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	err := reportBatchFailure(cmd, "bestof-20260927-100000-aaaa", 2, true, results, 1, boom)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap boom", err)
	}
	if !strings.Contains(err.Error(), "attempt 2 of 2") || !strings.Contains(err.Error(), "launched 1") || !strings.Contains(err.Error(), "20260927-100000-aaaa") {
		t.Fatalf("err = %v", err)
	}
	var doc batchResult
	if json.Unmarshal(out.Bytes(), &doc) != nil || doc.Batch != "bestof-20260927-100000-aaaa" || doc.Attempts != 2 || !doc.Incomplete || len(doc.Runs) != 1 || doc.Runs[0].RunID != "20260927-100000-aaaa" {
		t.Fatalf("json mode out = %s", out.String())
	}

	out.Reset()
	err = reportBatchFailure(cmd, "bestof-20260927-100000-aaaa", 2, false, results, 1, boom)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap boom", err)
	}
	if out.Len() != 0 {
		t.Fatalf("text mode printed %q, want nothing", out.String())
	}
}

// TestRunAttemptsResolveRecipeOnce pins that a batch resolves its plan
// (including the recipe) once, before any attempt, and shares it with every
// attempt (design generic-tool review, 2026-10-13): a project recipe
// published mid-batch must not give a batch's later attempts a different
// recipe from its first. A content-mutation test (publish a changed project
// recipe between attempts, read back each attempt's embedded recipe sha)
// cannot actually discriminate resolve-once from resolve-per-attempt here:
// fetchProjectRecipe's own local freshness cache (localcfg.SharedFreshFor,
// 24h) would serve the first attempt's reading back to a second resolution
// milliseconds later regardless, masking a resolve-per-attempt bug just as
// well as fixing it would. So this counts resolveAttemptPlan's calls
// directly, through the resolveBatchPlan seam, instead.
func TestRunAttemptsResolveRecipeOnce(t *testing.T) {
	newCloudFixture(t)
	setMaxParallel(t, 5)

	orig := resolveBatchPlan
	calls := 0
	resolveBatchPlan = func(ctx context.Context, env *cloudEnv, o *runOptions, total time.Duration, warn io.Writer, forAttempts bool) (*attemptPlan, error) {
		calls++
		return orig(ctx, env, o, total, warn, forAttempts)
	}
	defer func() { resolveBatchPlan = orig }()

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "3", "task"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("resolveAttemptPlan called %d times for a 3-attempt batch, want 1", calls)
	}
}

// TestRunAttemptsPartialFailureReportsWhatLaunched is runAttempts's own
// end-to-end version of TestReportBatchFailurePrintsWhatLaunched: it forces
// the 2nd of 3 attempts to fail (through the launchAttempt seam, with no
// live fault needed in the backend fake) and checks that fugaro run itself
// (not just reportBatchFailure in isolation) reports exactly the one run
// that did launch, names which attempt failed, and never calls the backend
// for the 2nd or 3rd attempt once the 2nd has failed.
func TestRunAttemptsPartialFailureReportsWhatLaunched(t *testing.T) {
	f := newCloudFixture(t)
	setMaxParallel(t, 5)
	boom := errors.New("boom")

	orig := launchAttempt
	calls := 0
	launchAttempt = func(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec, reused bool, warn io.Writer, skipBudget bool) (launchResult, error) {
		calls++
		if calls == 2 {
			return launchResult{}, boom
		}
		return orig(ctx, env, o, slug, spec, reused, warn, skipBudget)
	}
	defer func() { launchAttempt = orig }()

	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "3", "--json", "task")
	if err == nil || !strings.Contains(err.Error(), "attempt 2 of 3") || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "launched 1") {
		t.Fatalf("err = %v", err)
	}
	var doc batchResult
	if json.Unmarshal([]byte(out), &doc) != nil || !doc.Incomplete || doc.Attempts != 3 || len(doc.Runs) != 1 {
		t.Fatalf("out = %s", out)
	}
	if calls != 2 {
		t.Fatalf("launchAttempt called %d times, want 2 (the loop must stop at the failed attempt, not try a 3rd)", calls)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("executions = %d, want 1: the failed 2nd attempt must not have actually started", n)
	}
}

// TestRunAttemptsRetriesOnRunIDCollision pins buildUniqueAttemptSpec (design
// generic-tool review, 2026-10-13): task.NewRunID's own resolution (a
// timestamp plus two random bytes) makes two attempts launched within the
// same second collide about 1 time in 65536; a batch must regenerate rather
// than silently report two attempts under the same run id (which
// finishLaunch would then treat as one repeated launch, reporting N results
// for N-1 real runs). The runIDGenerator seam forces the collision this
// rare in production.
func TestRunAttemptsRetriesOnRunIDCollision(t *testing.T) {
	newCloudFixture(t)
	setMaxParallel(t, 5)

	orig := runIDGenerator
	calls := 0
	const dup = "20260927-100000-dad0"
	runIDGenerator = func() (string, error) {
		calls++
		if calls <= 2 {
			return dup, nil
		}
		return orig()
	}
	defer func() { runIDGenerator = orig }()

	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--json", "task")
	if err != nil {
		t.Fatal(err)
	}
	var doc batchResult
	if json.Unmarshal([]byte(out), &doc) != nil || len(doc.Runs) != 2 {
		t.Fatalf("out = %s", out)
	}
	if doc.Runs[0].RunID == doc.Runs[1].RunID {
		t.Fatalf("both attempts got run id %s: the collision retry did not work", doc.Runs[0].RunID)
	}
	if calls < 3 {
		t.Fatalf("runIDGenerator called %d times, want at least 3 (the 2nd attempt's first try collided and had to retry)", calls)
	}
}

// TestRunAttemptsRefusesAlreadyLaunchedInsideBatch pins the defense-in-depth
// half of item 1 (design generic-tool review, 2026-10-13): even past
// buildUniqueAttemptSpec's own retry, finishLaunch reporting
// "already-launched" for an attempt (which here can only mean some other
// run reused the id) must stop the batch and count as a failure, never as
// one more successful attempt — a batch must never report N results for
// fewer than N runs it actually started.
func TestRunAttemptsRefusesAlreadyLaunchedInsideBatch(t *testing.T) {
	newCloudFixture(t)
	setMaxParallel(t, 5)

	orig := launchAttempt
	calls := 0
	launchAttempt = func(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec, reused bool, warn io.Writer, skipBudget bool) (launchResult, error) {
		calls++
		res, err := orig(ctx, env, o, slug, spec, reused, warn, skipBudget)
		if calls == 2 && err == nil {
			res.Status = "already-launched"
		}
		return res, err
	}
	defer func() { launchAttempt = orig }()

	_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "task")
	if err == nil || !strings.Contains(err.Error(), "already launched") {
		t.Fatalf("err = %v", err)
	}
}
