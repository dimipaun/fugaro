package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// limitsYAML is the fugaro.yaml the limits tests check out: the caps a launch
// may only tighten against (a dollar cap and a token cap, both of them keys a
// repository's own file can carry), and a workflow whose clock --max-time is
// compared with.
const limitsRepo = "acme/app"

const limitsYAML = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
	"budget: { per_run_usd: 5 }\n" +
	"agent: { max_run_tokens: 900000 }\n" +
	"workflows:\n  web:\n    commands: { build: sh b.sh, test: sh t.sh }\n" +
	"    timeouts: { total: 2h, finalize_reserve: 10m }\n"

// limitsCheckout makes the working directory a checkout of acme/app on main
// whose file carries limitsYAML above. The default branch's own copy is the
// same file (baseBranchCheckout points origin/main at HEAD), so the layer the
// refusals name is the repository's default branch.
func limitsCheckout(t *testing.T) {
	t.Helper()
	baseBranchCheckout(t, limitsRepo, limitsYAML, "main")
}

// limitsImage gives the fixture's web job a build record naming the release
// that reads the four limits. Every launch that carries one is gated on the
// image (E1), so a test about anything else than the gate needs a record at or
// above launchLimitsSince, as a released installation that passes a --max-*
// has.
func limitsImage(t *testing.T, f *cloudFixture) {
	t.Helper()
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+launchLimitsSince)
}

// TestRunRefusesALooseningLimit: a launch may only tighten a chain limit, and
// a caller who typed a number that did not take effect is told both numbers
// and the layer that set the lower one (D1043, F4). The two dimensions whose
// caps a file can set today are the dollars (budget.per_run_usd) and the
// tokens (agent.max_run_tokens); both refusals name the checkout's file, which
// is the layer that beat the flag.
func TestRunRefusesALooseningLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"dollars", []string{"--max-usd", "9"},
			[]string{"--max-usd 9", "chain dollar limit", "the repository's default branch sets 5"}},
		{"tokens", []string{"--max-tokens", "1000000"},
			[]string{"--max-tokens 1000000", "chain token limit", "the repository's default branch sets 900000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			limitsCheckout(t)
			args := append([]string{"run"}, append(tc.args, "A task")...)
			_, _, err := execute(t, args...)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("%v: err = %v, want a user error", tc.args, err)
			}
			for _, want := range append(tc.want, "never loosen") {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("%v: err = %v, want it to name %q", tc.args, err, want)
				}
			}
			if n := len(f.run.RunRequests()); n != 0 {
				t.Fatalf("%v: %d launches, want none", tc.args, n)
			}
		})
	}
}

// TestRunRefusesALooseningMaxTimeAgainstTotalTimeout: --max-time tightens the
// clock the run itself has, and giving both composes as the smaller of the two
// (F18, spec §2). A --max-time above the clock is refused rather than written
// and silently ineffective, and the refusal names the figure and where it came
// from: the caller's own --total-timeout, or the workflow's timeouts.total
// when this launcher resolved its clock from the checkout.
//
// The last row is the composition's own floor: the figure the gate writes as
// the run's total_timeout is the run's clock, so it is held to the rule the
// --total-timeout flag already answers to — a clock that does not clear the
// workflow's finalize reserve leaves the run no time to finish in.
func TestRunRefusesALooseningMaxTimeAgainstTotalTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"the workflow's clock", []string{"--max-time", "3h"},
			[]string{"--max-time 3h0m0s", "above 2h0m0s", "workflows.web.timeouts.total", "compose as the smaller"}},
		{"the caller's own clock", []string{"--total-timeout", "30m", "--max-time", "45m"},
			[]string{"--max-time 45m0s", "above 30m0s", "--total-timeout", "compose as the smaller"}},
		{"a clock the reserve does not fit inside", []string{"--max-time", "5m"},
			[]string{"--max-time 5m0s", "5m0s clock", "timeouts.finalize_reserve 10m0s", "workflows.web.timeouts.total"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			limitsCheckout(t)
			args := append([]string{"run"}, append(tc.args, "A task")...)
			_, _, err := execute(t, args...)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("%v: err = %v, want a user error", tc.args, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("%v: err = %v, want it to name %q", tc.args, err, want)
				}
			}
			if n := len(f.run.RunRequests()); n != 0 {
				t.Fatalf("%v: %d launches, want none", tc.args, n)
			}
		})
	}
}

// TestRunAcceptsATighteningLimit: every dimension the launch names is written
// into the task's overrides as the merged effective value, which for a limit
// that tightened is the caller's own number. Nothing loosens here, so nothing
// is refused, and the task the run is started from carries all four.
func TestRunAcceptsATighteningLimit(t *testing.T) {
	f := newCloudFixture(t)
	limitsImage(t, f)
	limitsCheckout(t)
	if _, _, err := execute(t, "run", "--run-id", "20261110-100000-abcd",
		"--max-usd", "2.5", "--max-tokens", "1000", "--max-time", "45m", "--max-review-rounds", "2", "A task"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261110-100000-abcd")
	o := spec.Overrides
	if o.MaxUSD == nil || *o.MaxUSD != 2.5 {
		t.Fatalf("max_usd = %v, want 2.5", o.MaxUSD)
	}
	if o.MaxTokens == nil || *o.MaxTokens != 1000 {
		t.Fatalf("max_tokens = %v, want 1000", o.MaxTokens)
	}
	if o.MaxTime != "45m0s" {
		t.Fatalf("max_time = %q, want 45m0s", o.MaxTime)
	}
	if o.MaxReviewRounds == nil || *o.MaxReviewRounds != 2 {
		t.Fatalf("max_review_rounds = %v, want 2", o.MaxReviewRounds)
	}
	if o.TotalTimeout != "45m0s" {
		t.Fatalf("total_timeout = %q: the chain's wall time tightens the run's 2h clock, so the clock the task carries is the smaller of the two", o.TotalTimeout)
	}
	if n := len(f.run.RunRequests()); n != 1 {
		t.Fatalf("%d launches, want 1", n)
	}
}

// TestRunGatesTheLimitsOnTheImage: task.Parse decodes the overrides with
// DisallowUnknownFields, so a launch that carries one of the four on a job
// image whose runner predates launchLimitsSince fails in the container, after
// it was paid for (E1). The gate refuses it before anything is written, naming
// the release and fugaro image refresh — and it judges the task's fields and
// not the flags, so the same launch with no limit is started on the same image
// (the common case must stay byte-identical to what the previous release
// wrote).
func TestRunGatesTheLimitsOnTheImage(t *testing.T) {
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, launchLimitsSince, -1)
	writeBuildRecord(t, f, appSlug, "web", older)
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--max-usd", "3", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused on the image", err)
	}
	for _, want := range []string{"a chain limit (--max-usd, --max-tokens, --max-time, --max-review-rounds)",
		"fugaro " + launchLimitsSince + " or later", "fugaro image refresh"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--run-id", "20261110-110000-abcd", "A task"); err != nil {
		t.Fatalf("a launch carrying no limit must not be gated: %v", err)
	}
}

// TestRunWritesNoLimitWhenNoneIsGiven: a launch that names none of the four
// carries no chain at all — the task is byte-identical to what the previous
// release wrote, which is what makes the feature opt-in and keeps an
// installation that never passes a --max-* on its old job image.
func TestRunWritesNoLimitWhenNoneIsGiven(t *testing.T) {
	f := newCloudFixture(t)
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--run-id", "20261110-120000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261110-120000-abcd")
	o := spec.Overrides
	if o.MaxUSD != nil || o.MaxTokens != nil || o.MaxTime != "" || o.MaxReviewRounds != nil {
		t.Fatalf("overrides = %+v, want none of the four chain limits", o)
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	raw, err := runstore.Open(b, appSlug, "20261110-120000-abcd").ReadFile(context.Background(), "task.json")
	if err != nil {
		t.Fatal(err)
	}
	// The stored text, not only the decoded struct: a field written as null
	// would decode to a nil pointer and still be a byte the previous release
	// never wrote.
	for _, key := range []string{"max_usd", "max_tokens", "max_time", "max_review_rounds"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("task.json names %q with no limit given: %s", key, raw)
		}
	}
}

// TestRunTotalTimeoutAndMaxTimeComposeAsMin: the two flags are different
// knobs — --total-timeout is the run's clock, raised or lowered as the caller
// likes, and --max-time is the chain limit that may only tighten it (F18). A
// launch that gives both writes the smaller of the two as the task's own
// total_timeout, which is the field the backend's override reads, and keeps
// the chain's number in max_time beside it for the runner's own merge (D1044).
func TestRunTotalTimeoutAndMaxTimeComposeAsMin(t *testing.T) {
	// The two launches of each case differ in nothing but the chain limit, so
	// the backend timeout's drop is the clock the limit took off the run and
	// not a constant the fixture applies (the launch adds the workflow's
	// finalize reserve to whatever clock it resolved, and a launch that
	// resolved no clock of its own asks the backend for none).
	for _, tc := range []struct {
		name             string
		clock, limit     []string
		wantTotal, drops time.Duration
	}{
		{"the chain's limit is the smaller", []string{"--total-timeout", "6h"},
			[]string{"--total-timeout", "6h", "--max-time", "90m"}, 90 * time.Minute, 6*time.Hour - 90*time.Minute},
		{"the workflow's clock is", []string{"--total-timeout", "2h"}, []string{"--max-time", "45m"},
			45 * time.Minute, 2*time.Hour - 45*time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			limitsImage(t, f)
			limitsCheckout(t)
			base := limitsLaunch(t, f, "20261110-130000-abcd", tc.clock...)
			withLimit := limitsLaunch(t, f, "20261110-130001-abcd", tc.limit...)
			if base-withLimit != tc.drops {
				t.Fatalf("the run's clock dropped by %s with the chain limit, want %s", base-withLimit, tc.drops)
			}
			spec := readSpec(t, f, "20261110-130001-abcd")
			if spec.Overrides.TotalTimeout != tc.wantTotal.String() {
				t.Fatalf("total_timeout = %q, want %q", spec.Overrides.TotalTimeout, tc.wantTotal)
			}
			if spec.Overrides.MaxTime != tc.wantTotal.String() {
				t.Fatalf("max_time = %q, want the chain's own figure %q kept beside it", spec.Overrides.MaxTime, tc.wantTotal)
			}
		})
	}
}

// TestRunPRRepeatCarriesTheChainLimits: repeating a --run-id is the documented
// way to find out whether a launch went through, and a --pr follow-up carrying
// a chain limit is no exception (followup.go's storedFollowUp). The stored task
// holds the limits the launch wrote, so the repeat has to be compared against
// those and not against the flags alone — and the clock makes the point twice
// over, since it is the one field two flags write: a repeat of
// `--total-timeout 6h --max-time 90m` finds the composed 1h30m0s stored, not
// the 6h the caller typed.
func TestRunPRRepeatCarriesTheChainLimits(t *testing.T) {
	f := newCloudFixture(t)
	limitsImage(t, f)
	limitsCheckout(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	args := []string{"run", "--repo", limitsRepo, "--pr", "7", "--run-id", fuID,
		"--total-timeout", "6h", "--max-usd", "3", "--max-time", "90m"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	o := readSpec(t, f, fuID).Overrides
	if o.MaxUSD == nil || *o.MaxUSD != 3 || o.MaxTime != "1h30m0s" || o.TotalTimeout != "1h30m0s" {
		t.Fatalf("overrides = %+v, want the chain's 3 dollars and the composed 1h30m0s clock", o)
	}
	if out, _, err := execute(t, args...); err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("the repeat of a follow-up carrying limits: %v, output:\n%s", err, out)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	// The guard the comparison exists for is kept: a repeat whose figure is not
	// the stored one is still a different task, and this one is no loosening
	// either (4 is under the repository's 5), so the refusal is that check's.
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", fuID,
		"--total-timeout", "6h", "--max-usd", "4", "--max-time", "90m"); err == nil || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("err = %v, want a repeat naming another limit refused as a different task", err)
	}
}

// TestRunPRRepeatIsIdempotentWhereNoClockResolves: a repeat is compared against
// the stored task and not against what this call can resolve here, and the
// clock is the field that proves it. A launch made outside the repository's
// checkout resolves no clock of its own — --max-time is written alone, and the
// composition is the runner's at bootstrap — so a repeat of it from the same
// place finds a stored task whose clock this call cannot re-derive, and a
// repeat of it made later from inside the checkout resolves one the launch
// never had. Both are the same task, and neither may write a different
// composition into a run that has started.
func TestRunPRRepeatIsIdempotentWhereNoClockResolves(t *testing.T) {
	f := newCloudFixture(t)
	limitsImage(t, f)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	limitsCheckout(t)
	inCheckout, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // --repo names the repository; no checkout resolves a clock
	args := []string{"run", "--repo", limitsRepo, "--pr", "7", "--run-id", fuID, "--max-time", "90m"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	if o := readSpec(t, f, fuID).Overrides; o.MaxTime != "1h30m0s" || o.TotalTimeout != "" {
		t.Fatalf("overrides = %+v, want the chain's 1h30m0s alone (no clock here to compose it with)", o)
	}
	if out, _, err := execute(t, args...); err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("the repeat with no clock of its own: %v, output:\n%s", err, out)
	}
	// The same repeat with the checkout in hand, where workflows.web.timeouts.total
	// (2h) would resolve: the stored task is the run's truth, so the repeat is
	// still that task and not one carrying a clock this call invented.
	t.Chdir(inCheckout)
	if out, _, err := execute(t, args...); err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("the repeat from inside the checkout: %v, output:\n%s", err, out)
	}
	if o := readSpec(t, f, fuID).Overrides; o.MaxTime != "1h30m0s" || o.TotalTimeout != "" {
		t.Fatalf("overrides = %+v after the repeats, want them untouched", o)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// limitsLaunch launches args under runID and answers the clock the backend was
// asked for it.
func limitsLaunch(t *testing.T, f *cloudFixture, runID string, args ...string) time.Duration {
	t.Helper()
	argv := append([]string{"run", "--run-id", runID}, append(args, "A task")...)
	if _, _, err := execute(t, argv...); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.run.RunRequests() {
		if r.Env["FUGARO_RUN"] != appSlug+"/"+runID {
			continue
		}
		d, err := time.ParseDuration(r.Timeout)
		if err != nil {
			t.Fatalf("the launch of %s asked the backend for %q: %v", runID, r.Timeout, err)
		}
		return d
	}
	t.Fatalf("no request for %s in %+v", runID, f.run.RunRequests())
	return 0
}

// TestRunBatchLimitsEveryAttempt: a --max-* on a batch applies to every
// attempt, and the batch is N chains rather than one (D1053): each attempt
// starts from its own task, and each of those tasks carries the limit its
// launcher wrote — a limit on the first attempt alone would leave the other
// two unbounded.
func TestRunBatchLimitsEveryAttempt(t *testing.T) {
	f := newCloudFixture(t)
	setMaxParallel(t, 5)
	limitsImage(t, f)
	out, _, err := execute(t, "run", "--repo", limitsRepo, "--attempts", "3", "--max-usd", "3", "--json", "A task")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil || len(doc.Runs) != 3 {
		t.Fatalf("out = %s", out)
	}
	for _, r := range doc.Runs {
		spec := readSpec(t, f, r.RunID)
		if spec.Overrides.MaxUSD == nil || *spec.Overrides.MaxUSD != 3 {
			t.Fatalf("%s: max_usd = %v, want 3 on every attempt", r.RunID, spec.Overrides.MaxUSD)
		}
	}
}

// TestRunLimitNeverAboveTheModeCeiling: the chain's review-round limit is the
// chain's own number and never the mode's (D1057). A mode's row is a default
// and not a cap — internal/rigor's DefaultRounds says so itself, and
// chainRoundCeiling is the figure a run of that mode starts from — so a chain
// limit above it is a launch the engine would run, and the launcher accepts it
// rather than refusing one the engine would not: a chain spans runs, and a
// later run of a stricter mode is not the launcher's to guess.
func TestRunLimitNeverAboveTheModeCeiling(t *testing.T) {
	f := newCloudFixture(t)
	limitsImage(t, f)
	limitsCheckout(t)
	above := chainRoundCeiling(rigor.Pro) + 7
	args := []string{"run", "--run-id", "20261110-140000-abcd", "--max-review-rounds", fmt.Sprint(above), "A task"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatalf("--max-review-rounds %d: %v", above, err)
	}
	spec := readSpec(t, f, "20261110-140000-abcd")
	if spec.Overrides.MaxReviewRounds == nil || *spec.Overrides.MaxReviewRounds != above {
		t.Fatalf("max_review_rounds = %v, want %d", spec.Overrides.MaxReviewRounds, above)
	}
}

// TestRunClampedRoundsRefusalNamesTheFigure: the one review-round value the
// launcher refuses is a chain limit below one round, which bounds a chain that
// could never spend it. The figure it quotes is chainRoundCeiling's output —
// the function the whole tree reads, never a
// literal transcribed from the mode table (F15) — and the recipe's bound it is
// held inside.
func TestRunClampedRoundsRefusalNamesTheFigure(t *testing.T) {
	f := newCloudFixture(t)
	limitsCheckout(t)
	_, _, err := execute(t, "run", "--max-review-rounds", "0", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	want := fmt.Sprintf("A %s run's own review loop starts from %d rounds per run (the mode's default, held inside the recipe's %d)",
		rigor.Pro, chainRoundCeiling(rigor.Pro), recipe.MaxReviewRounds)
	for _, w := range []string{"--max-review-rounds 0", "must be at least 1", want} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("err = %v, want it to name %q", err, w)
		}
	}
	if n := len(f.run.RunRequests()); n != 0 {
		t.Fatalf("%d launches, want none", n)
	}
}

// TestRunRefusesAMalformedLimit: the syntax of the four flags is refused
// before openCloud, so a mistyped number costs no cloud call and no read of
// the policy — a different refusal from the loosening one, which needs the
// merge. Zero is malformed for three of the four and the fourth
// (--max-review-rounds) parses and is refused by the semantic check above,
// where the figure it names can be said.
func TestRunRefusesAMalformedLimit(t *testing.T) {
	f := newCloudFixture(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--max-usd", "abc"}, "must be a number of dollars"},
		{[]string{"--max-usd", "0"}, "must be greater than 0"},
		{[]string{"--max-usd", "-1"}, "must be greater than 0"},
		{[]string{"--max-tokens", "half"}, "must be a whole number of tokens"},
		{[]string{"--max-tokens", "0"}, "must be greater than 0"},
		{[]string{"--max-time", "later"}, "must be a duration"},
		{[]string{"--max-time", "0"}, "must be greater than 0"},
		{[]string{"--max-review-rounds", "two"}, "must be a whole number of rounds"},
	} {
		args := append([]string{"run", "--repo", limitsRepo}, append(tc.args, "A task")...)
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want it to name %q", tc.args, err, tc.want)
		}
	}
	if n := len(f.run.RunRequests()); n != 0 {
		t.Fatalf("%d launches after malformed limits, want none", n)
	}
}

// TestLaunchLimitsSinceIsTheReleaseTheChecklistNames holds the constant to the
// reader that states its number: docs/release.md's Before-you-tag bullet, the
// job every other *Since constant's bullet does. Nothing else in the tree ties
// the release the gate accepts to the release the four overrides members
// actually ship in, and the gate is the whole of what protects a run from a
// runner that would refuse its task (E1: there is no fugaro update
// convergence here, the four are per-launch).
func TestLaunchLimitsSinceIsTheReleaseTheChecklistNames(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `launchLimitsSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no launchLimitsSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+launchLimitsSince+"\"`") {
		t.Errorf("docs/release.md's launchLimitsSince bullet does not carry the constant's release (%q)", launchLimitsSince)
	}
	// The four keys, so a bullet that lost one is not a bullet about this
	// field: they are what the strict decoder refuses, one by one.
	for _, key := range []string{"max_usd", "max_tokens", "max_time", "max_review_rounds"} {
		if !strings.Contains(bullet, key) {
			t.Errorf("docs/release.md's launchLimitsSince bullet does not name %q", key)
		}
	}
}

// TestLaunchLimitsSinceIsTheNextRelease: the constant is written out rather
// than read from anything, because nothing in the tree knows which release
// carries this work. The four members ship in 0.22.0, the release
// docs/release-streams.yaml names as the next one after the current 0.21.0
// (docs/releases/v0.22.0.md is the notes main is assembling), which is the
// release the five sibling constants merged in the same window name too. A
// renumbering moves this and the bullet together.
func TestLaunchLimitsSinceIsTheNextRelease(t *testing.T) {
	if launchLimitsSince != "0.22.0" {
		t.Errorf("launchLimitsSince = %q: the four chain limits ship in 0.22.0, the release docs/release-streams.yaml names as the next CLI release", launchLimitsSince)
	}
}
