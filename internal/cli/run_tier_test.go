package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
)

// releaseTier is a job image new enough to read a task's tier
// (governanceSince): the release this work ships in.
const releaseTier = "ghcr.io/dimipaun/fugaro-base:0.13.0"

// TestRunTierIsStoredInTheTask: the expectation tier a launcher dispatches a
// run at travels to the runner in the task (docs/task-governance spec §3, plan
// T8.1), which is what records it and, in T8.2, what a run's expected cost is
// read from. A launch that asks for none carries no such field.
func TestRunTierIsStoredInTheTask(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseTier)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261025-140000-abcd",
		"--tier", "small", "Fix the flaky test."); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261025-140000-abcd")
	if spec.Tier != tier.Small {
		t.Fatalf("tier = %q, want the flag's", spec.Tier)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(taskJSON(t, f, "20261025-140000-abcd")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["tier"] != "small" {
		t.Fatalf("task.json tier = %v", doc["tier"])
	}

	f2 := newCloudFixture(t)
	writeBuildRecord(t, f2, appSlug, "web", releaseTier)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261025-140100-abcd", "Fix the flaky test."); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f2, "20261025-140100-abcd"); spec.Tier != "" {
		t.Fatalf("a launch that asked for no tier carries %q", spec.Tier)
	}
	if wire := taskJSON(t, f2, "20261025-140100-abcd"); strings.Contains(wire, `"tier"`) {
		t.Fatalf("a launch that asked for no tier writes the field: %s", wire)
	}
}

// TestRunRefusesATierOutsideTheVocabulary: the vocabulary is closed (internal/
// tier), so a near miss is a launcher's mistake refused before anything is
// written — never a value the runner reads as an unknown tier.
func TestRunRefusesATierOutsideTheVocabulary(t *testing.T) {
	newCloudFixture(t)
	for _, tt := range []string{"Trivial", "SMALL", "small ", "huge", "none"} {
		_, _, err := execute(t, "run", "--repo", "acme/app", "--tier", tt, "Fix the flaky test.")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "tier") {
			t.Errorf("--tier %q: err = %v, want exit 1 naming the tier", tt, err)
		}
	}
}

// TestFollowUpKeepsTheTier: a follow-up carries the launcher's --tier, and
// keeps the tier the previous run recorded when it asks for none — the same
// keeping rigor's mode gets (followUpRigor, T5.2). Without it a follow-up
// would silently drop an envelope the PR's runs were dispatched under.
func TestFollowUpKeepsTheTier(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	rec := prRecord(rootID, "", 7, 1)
	rec.Tier = &runstore.TierRecord{Tier: tier.Medium, ExpectedUSD: 3, ExpectedMinutes: 90}
	writeRecord(t, f, rootID, rec)
	writeBuildRecord(t, f, appSlug, "web", releaseTier)

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x"); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f, fuID); spec.Tier != tier.Medium {
		t.Fatalf("the follow-up carries tier %q, want the one its run recorded", spec.Tier)
	}

	f2 := newCloudFixture(t)
	seedRoot(t, f2, rootID, time.Now().Add(-72*time.Hour))
	rec2 := prRecord(rootID, "", 7, 1)
	rec2.Tier = &runstore.TierRecord{Tier: tier.Medium, ExpectedUSD: 3, ExpectedMinutes: 90}
	writeRecord(t, f2, rootID, rec2)
	writeBuildRecord(t, f2, appSlug, "web", releaseTier)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--tier", "large", "also rename x"); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f2, fuID); spec.Tier != tier.Large {
		t.Fatalf("the follow-up carries tier %q, want the flag's", spec.Tier)
	}
}

// TestReviewLaunchCarriesTheTier: a mode: review recipe builds its spec in
// reviewSpec, a third builder. The tier is the launcher's on every launch
// shape the flag is accepted on, so a review is no exception.
func TestReviewLaunchCarriesTheTier(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseTier)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261025-140200-abcd",
		"--recipe", "review-only", "--pr", "7", "--tier", "small"); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f, "20261025-140200-abcd"); spec.Tier != tier.Small {
		t.Fatalf("review task carries tier %q, want the flag's", spec.Tier)
	}
}

// TestRunAttemptsCarryTheTierOnEveryAttempt: a best-of-N batch is one task
// launched N times, and the tier is part of that task — every attempt is
// dispatched at the same expectation, so an attempt that dropped it would run
// with no envelope at all while its siblings had one.
//
// Mutation (drop Tier from buildAttemptSpec): no attempt stores a tier, and
// each of their records carries no tier block.
func TestRunAttemptsCarryTheTierOnEveryAttempt(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseTier)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--tier", "small", "--json",
		"Read the plan first.\n\nThen fix it.")
	if err != nil {
		t.Fatal(err)
	}
	var batch batchResult
	if err := json.Unmarshal([]byte(out), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Runs) != 2 {
		t.Fatalf("launched %d attempts, want 2 (%s)", len(batch.Runs), out)
	}
	for _, r := range batch.Runs {
		if got := readSpec(t, f, r.RunID).Tier; got != tier.Small {
			t.Fatalf("attempt %s: tier = %q, want the flag's", r.RunID, got)
		}
	}
}

// A --batch launch (one run under a batch label) carries its tier the same
// way: the label groups runs, and the tier still describes this one.
func TestRunBatchCarriesTheTier(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseTier)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261025-140300-abcd",
		"--batch", "nightly", "--tier", "small", "Fix the flaky test."); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261025-140300-abcd")
	if spec.Tier != tier.Small || spec.Batch != "nightly" {
		t.Fatalf("tier = %q, batch = %q, want the flag's tier under the batch label", spec.Tier, spec.Batch)
	}
}

// TestRunTierRefusedWithRetry: --retry launches the stored task as it is, so it
// takes no task flags at all — a tier given there would be accepted and then
// silently dropped, which is worse than a refusal.
func TestRunTierRefusedWithRetry(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "it takes no --tier", "run", "--retry", "20261018-120000-abcd", "--tier", "small")
}

// TestStoredFollowUpRefusesADifferentTier: a repeated --run-id names a stored
// task, and the tier is part of that task — the stored spec is what the job
// reads, so a tier this launch asked for and the comparison did not check would
// be dropped while the launch reported the run as already launched.
func TestStoredFollowUpRefusesADifferentTier(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	writeBuildRecord(t, f, appSlug, "web", releaseTier)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID,
		"--tier", "small", "also rename x"); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID,
		"--tier", "large", "also rename x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("err = %v, want the different task refusal", err)
	}
	if spec := readSpec(t, f, fuID); spec.Tier != tier.Small {
		t.Fatalf("the stored task carries tier %q, want the first launch's", spec.Tier)
	}
}

// TestRunTierIsGatedOnTheJobImage: the tier is a new task field, and task.Parse
// decodes with DisallowUnknownFields, so a runner older than governanceSince
// refuses the whole spec at bootstrap — bytes in the bucket under a run that
// never starts. The launcher therefore refuses the launch, and a run that
// carries no tier is never gated.
func TestRunTierIsGatedOnTheJobImage(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release085)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--tier", "small", "Fix the flaky test.")
	if ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "the run's own tier request (--tier) needs a job image whose runner knows the tier (fugaro "+governanceSince+" or later)") {
		t.Fatalf("err = %v, want the launch refused on an image that predates the field", err)
	}
	writeBuildRecord(t, f, appSlug, "web", releaseTier)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "Fix the flaky test."); err != nil {
		t.Fatalf("a launch carrying no tier was gated: %v", err)
	}
}

// TestRunTierIsGatedOnACloneWorkflow: a checkout: clone workflow has no build
// record to judge, so the same gate is judged against base_images.base itself —
// the other half of the check.
func TestRunTierIsGatedOnACloneWorkflow(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "base_images: {base: ghcr.io/dimipaun/fugaro-base:0.7.5}\n")
	testutil.IsolateGit(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n" +
		"  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "--tier", "small", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "the run's own tier request (--tier) needs a job image whose runner knows the tier (fugaro "+governanceSince+" or later)") ||
		!strings.Contains(err.Error(), "ghcr.io/dimipaun/fugaro-base:0.7.5, release 0.7.5") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// seedRootTier writes a first run's task and the record for it that carries a
// tier, which is what a --pr follow-up inherits (runstore.TierRecord).
func seedRootTier(t *testing.T, f *cloudFixture, id string, tt tier.Tier) {
	t.Helper()
	seedSpec(t, f, firstRunSpec(id), false)
	rec := prRecord(id, "", 7, 1)
	if tt != "" {
		rec.Tier = &runstore.TierRecord{Tier: tt, ExpectedUSD: 3, ExpectedMinutes: 90}
	}
	writeRecord(t, f, id, rec)
}

// TestFollowUpKeptTierIsGatedOnTheJobImage: a follow-up that passed no --tier
// still carries the tier it keeps from its PR, so it is gated exactly as a
// --tier launch is — task.Parse decodes with DisallowUnknownFields, and a
// runner older than governanceSince refuses the whole task at bootstrap, bytes
// in the bucket under a run that never starts. The refusal names the tier the
// follow-up keeps rather than a flag nobody gave.
func TestFollowUpKeptTierIsGatedOnTheJobImage(t *testing.T) {
	f := newCloudFixture(t)
	seedRootTier(t, f, rootID, tier.Medium)
	writeBuildRecord(t, f, appSlug, "web", release085)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "the tier a follow-up keeps from its PR (tier: medium)") {
		t.Fatalf("err = %v, want exit 1 on an image that predates the field, naming the tier the follow-up keeps", err)
	}
	if !noTaskStored(t, f, fuID) {
		t.Errorf("the refused launch wrote a task.json")
	}
}

// TestRetryOfAFirstRunIsGatedUnderItsTierFlag: the follow-up branch of the
// gate's subject is for a launch that passed no --tier and keeps one from a PR.
// A --retry of a first run that was launched with --tier follows no PR and
// keeps nothing from one, so the gate describes it by the flag, which is where
// its request came from; saying it keeps a tier from a PR would name a rule
// that never ran.
func TestRetryOfAFirstRunIsGatedUnderItsTierFlag(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release085)
	spec := firstRunSpec(rootID)
	spec.Tier = tier.Large
	seedSpec(t, f, spec, false)
	_, _, err := execute(t, "run", "--retry", rootID)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "the run's own tier request (--tier) needs a job image whose runner knows the tier (fugaro "+governanceSince+" or later)") {
		t.Fatalf("err = %v, want exit 1 naming the flag the stored request came from", err)
	}
	if strings.Contains(err.Error(), "a follow-up keeps") {
		t.Errorf("the refusal claims the tier comes from a PR for a first run: %v", err)
	}
}
