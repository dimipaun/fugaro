package cli

import (
	"strings"
	"testing"
	"time"
)

// TestRunTokenCheckedBeforeRecipeResolution is review finding E (round 2):
// newSpec used to call chooseRecipe (which can resolve a project recipe
// through fetchProjectRecipe, a real runs-bucket read) before claudeAccountFor,
// so a disallowed --token triggered that bucket read before ever being
// refused. projectRecipesFixture makes "fugaro-runs-proj-1234" this
// installation's runs bucket (the convention fetchProjectRecipe requires to
// even attempt the read, docs: projectRecipesNote), so --recipe nope here
// really would reach a bucket read if chooseRecipe ran at all. No
// claude_accounts configured: evil-name is disallowed. If the account check
// runs first (the fix), the launch fails with the disallowed-account error
// before chooseRecipe/fetchProjectRecipe ever runs; if the recipe lookup
// runs first (the bug), it fails instead with "recipe nope is not in
// .fugaro/recipes, the project's, or the catalog" — a different, specific
// error this test can tell apart from the account one.
//
// A literal "fails on any bucket read" fake was not built for this test:
// the GCS fake's only read-fault knob (FailObjectGets) affects metadata-only
// GETs, not the content read fetchProjectRecipe actually makes, and nothing
// else in the test tree offers a general-purpose failing blob.Bucket driver.
// Distinguishing the two error messages above still proves the same thing a
// failing-read fake would: evil-name is refused without chooseRecipe (and
// so without fetchProjectRecipe's bucket read) ever running, which the
// mutation below confirms by making this exact test fail.
func TestRunTokenCheckedBeforeRecipeResolution(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil) // no claude_accounts: evil-name is disallowed
	_, _, err := execute(t, "run", "--repo", "acme/app", "--token", "evil-name", "--recipe", "nope", "x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "evil-name") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v, want the disallowed-account refusal, checked before recipe resolution", err)
	}
	if strings.Contains(err.Error(), "recipe nope") {
		t.Fatalf("err = %v: chooseRecipe ran (and so fetchProjectRecipe's bucket read was attempted) before the account check", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestFollowUpTokenCheckedBeforeRecipeResolution mirrors the fix above for
// newFollowUpSpec, which had the identical ordering bug: resolveRecipe/
// carryChosenRecipe ran before claudeAccountAllowed for the inherited
// account. The root run's OAuthToken ("acme-personal") is disallowed (no
// claude_accounts configured); --recipe nope would otherwise reach
// fetchProjectRecipe's bucket read (projectRecipesFixture makes this
// installation's runs bucket the one that check requires).
func TestFollowUpTokenCheckedBeforeRecipeResolution(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil) // no claude_accounts: acme-personal is disallowed
	root := firstRunSpec(rootID)
	root.OAuthToken = "acme-personal"
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)

	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--recipe", "nope", "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-personal") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v, want the disallowed-account refusal, checked before recipe resolution", err)
	}
	if strings.Contains(err.Error(), "recipe nope") {
		t.Fatalf("err = %v: the recipe lookup ran before the account check", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunReviewModeTokenCheckedBeforeRecipeResolution: --pr with an
// explicit --recipe has its own dispatch path in runRun (deciding whether
// --recipe names a mode: review recipe at all, so --pr can mean "any open
// pull request" rather than a follow-up), added after the two fixes above
// and with the identical bug: it called resolveRecipe (the same project/
// catalog bucket read) before any account check. projectRecipesFixture
// makes "fugaro-runs-proj-1234" this installation's runs bucket, so
// --recipe nope would reach fetchProjectRecipe's bucket read if dispatch's
// own resolveRecipe call ran at all; no claude_accounts configured, so
// evil-name is disallowed regardless of what --recipe resolves to.
//
// Mutation (move the `claudeAccountFor` call added to runRun's --pr/--recipe
// dispatch to after the `resolveRecipe` call instead of before): this test
// fails, finding "recipe nope" in the error instead of the account refusal.
func TestRunReviewModeTokenCheckedBeforeRecipeResolution(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil) // no claude_accounts: evil-name is disallowed
	_, _, err := execute(t, "run", "--repo", "acme/app", "--token", "evil-name", "--pr", "7", "--recipe", "nope")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "evil-name") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v, want the disallowed-account refusal, checked before recipe resolution", err)
	}
	if strings.Contains(err.Error(), "recipe nope") {
		t.Fatalf("err = %v: resolveRecipe ran (and so fetchProjectRecipe's bucket read was attempted) before the account check", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}
