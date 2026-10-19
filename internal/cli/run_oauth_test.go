package cli

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// release065 is a build record's base_ref at exactly namedTokensSince: a
// runner built from it knows task.Spec.OAuthToken.
const release065 = "ghcr.io/dimipaun/fugaro-base:0.6.5"

// otherSlug is acme/other's storage slug, the repository oauthCheckout anchors.
var otherSlug = mustSlug("github", "acme/other")

// oauthCheckout makes the working directory a checkout of acme/other, whose
// fugaro.yaml sets agent.auth: oauth, with extra agent: lines (such as
// oauth_token:), one workflow "svc". It registers svc's plain job (the
// default account's); a test that launches a named variant adds that job
// itself.
func oauthCheckout(t *testing.T, f *cloudFixture, agentLines string) {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(otherSlug, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent:\n  auth: oauth\n" + agentLines +
		"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml})
	t.Chdir(dir)
}

// claudeAccountsConfig is a claude_accounts block allowing acme-personal and
// acme-overflow for acme/other, appended to the fixture's local config.
const claudeAccountsConfig = "claude_accounts:\n  acme-personal:\n    allow_data_to: [\"acme/other\"]\n  acme-overflow:\n    allow_data_to: [\"acme/other\"]\n"

func otherSpec(t *testing.T, f *cloudFixture, runID string) *task.Spec {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, otherSlug, runID).ReadTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// TestRunTokenDefaultsToConfiguredThenDefaultAccount is the plan's own test:
// --token wins over agent.oauth_token, which wins over the repository's
// default account ("", config.DefaultClaudeAccount).
func TestRunTokenDefaultsToConfiguredThenDefaultAccount(t *testing.T) {
	t.Run("flag wins", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig)
		oauthCheckout(t, f, "  oauth_token: acme-personal\n")
		writeBuildRecord(t, f, otherSlug, "svc", release065)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261012-100000-aaaa", "--token", "acme-overflow", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261012-100000-aaaa").OAuthToken; got != "acme-overflow" {
			t.Fatalf("OAuthToken = %q, want acme-overflow", got)
		}
	})
	t.Run("else agent.oauth_token", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig)
		oauthCheckout(t, f, "  oauth_token: acme-personal\n")
		writeBuildRecord(t, f, otherSlug, "svc", release065)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-personal")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261012-100000-bbbb", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261012-100000-bbbb").OAuthToken; got != "acme-personal" {
			t.Fatalf("OAuthToken = %q, want acme-personal", got)
		}
	})
	t.Run("else default", func(t *testing.T) {
		f := newCloudFixture(t)
		oauthCheckout(t, f, "")
		if _, _, err := execute(t, "run", "--run-id", "20261012-100000-cccc", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261012-100000-cccc").OAuthToken; got != "" {
			t.Fatalf("OAuthToken = %q, want empty (default)", got)
		}
	})
}

// TestRunTokenRefusesDisallowedName: --token names an account this repo
// isn't allowed, refused before Launch is called.
func TestRunTokenRefusesDisallowedName(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig) // allows acme-personal, acme-overflow; not evil-name
	oauthCheckout(t, f, "")
	_, _, err := execute(t, "run", "--run-id", "20261012-100000-dddd", "--token", "evil-name", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "evil-name") || !strings.Contains(err.Error(), "acme/other") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunLaunchesTheVariantJob: --token acme-personal launches the variant
// job, gcp.WorkflowVariant("svc", "acme-personal"), not the plain one.
func TestRunLaunchesTheVariantJob(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	variant := gcp.WorkflowVariant("svc", "acme-personal")
	f.run.AddJob(gcp.JobName(otherSlug, variant), "2", "4Gi")
	var got gcpfake.RunCall
	f.run.OnRun = func(c gcpfake.RunCall) { got = c }
	if _, _, err := execute(t, "run", "--run-id", "20261012-100000-eeee", "--token", "acme-personal", "A task"); err != nil {
		t.Fatal(err)
	}
	if want := gcp.JobName(otherSlug, variant); got.Job != want {
		t.Fatalf("launched job = %q, want %q", got.Job, want)
	}
}

// TestRunTokenVariantJobMissingNeverFallsBackToDefault: a repository may
// choose a named account whose job variant was never deployed (fugaro init
// --repo not yet re-run); the launch must fail outright, never silently
// reach the default account's job (carry-forward (b), Task 4/6 review).
func TestRunTokenVariantJobMissingNeverFallsBackToDefault(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	// No AddJob for the "svc_acme-personal" variant: it does not exist yet.
	_, _, err := execute(t, "run", "--run-id", "20261012-100000-ffff", "--token", "acme-personal", "A task")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "fugaro init --repo") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none (the default job must never run instead)", n)
	}
}

// appClaudeAccountsConfig allows acme-personal and acme-overflow for
// acme/app, the repository firstRunSpec/followUpSpec/prRecord (ls_test.go)
// hardcode.
const appClaudeAccountsConfig = "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n  acme-overflow:\n    allow_data_to: [acme/app]\n"

// seedAppFollowUpRoot seeds a first run of acme/app (PR 7) whose resolved
// account is account ("" for the repository's default), ready for a
// follow-up of fuID to continue.
func seedAppFollowUpRoot(t *testing.T, f *cloudFixture, account string) {
	t.Helper()
	root := firstRunSpec(rootID)
	root.OAuthToken = account
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)
}

// TestFollowUpNoTokenInheritsPreviousAccount: with no --token at all, a
// follow-up's task.Spec.OAuthToken is the previous run's, unchanged by the
// task 9b review's override rule below (no --token means no override).
func TestFollowUpNoTokenInheritsPreviousAccount(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, appClaudeAccountsConfig)
	f.run.AddJob(gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-personal")), "4", "8Gi")
	writeBuildRecord(t, f, appSlug, "web", release065)
	seedAppFollowUpRoot(t, f, "acme-personal")
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "more work")
	if err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).OAuthToken; got != "acme-personal" {
		t.Fatalf("follow-up OAuthToken = %q, want acme-personal (inherited)", got)
	}
	if strings.Contains(errOut, "ignored") || strings.Contains(errOut, "switches") {
		t.Fatalf("stderr %q should say nothing about a switch when --token was not given", errOut)
	}
}

// TestFollowUpExplicitTokenOverridesInheritedAccount is the owner's ruling
// (2026-10-12, task 9b review): an EXPLICIT --token on a follow-up now
// overrides the inherited account instead of being ignored. The root run
// used the default account; the follow-up switches to acme-personal and
// must launch THAT account's variant job (gcp.WorkflowVariant("web",
// "acme-personal")), never the default job the root run used.
func TestFollowUpExplicitTokenOverridesInheritedAccount(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, appClaudeAccountsConfig)
	variant := gcp.WorkflowVariant("web", "acme-personal")
	f.run.AddJob(gcp.JobName(appSlug, variant), "4", "8Gi")
	writeBuildRecord(t, f, appSlug, "web", release065)
	seedAppFollowUpRoot(t, f, "") // root used the default account
	var got gcpfake.RunCall
	f.run.OnRun = func(c gcpfake.RunCall) { got = c }
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-personal", "more work")
	if err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f, fuID); spec.OAuthToken != "acme-personal" {
		t.Fatalf("follow-up OAuthToken = %q, want acme-personal (the override)", spec.OAuthToken)
	}
	if want := gcp.JobName(appSlug, variant); got.Job != want {
		t.Fatalf("launched job = %q, want the new account's variant %q, not the default job", got.Job, want)
	}
	if !strings.Contains(errOut, "acme-personal") || !strings.Contains(errOut, "switches") {
		t.Fatalf("stderr %q should note the switch from default to acme-personal", errOut)
	}

	// Followable: a repeated --run-id reports already-launched (checkExecution
	// compares against the NEW variant, not the job the root run used), and
	// fugaro runs ls shows it pending, not an error.
	out2, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-personal", "--json", "more work")
	if err != nil {
		t.Fatalf("repeated --run-id: %v", err)
	}
	if !strings.Contains(out2, `"status": "already-launched"`) {
		t.Fatalf("repeat result = %s, want already-launched", out2)
	}
	if len(f.run.Executions()) != 1 {
		t.Fatalf("%d executions, want exactly 1 (no second launch)", len(f.run.Executions()))
	}
	lsGot, lsErrOut := lsJSON(t, "--repo", "acme/app")
	if len(lsGot.Runs) != 2 { // root (unlaunched here) is not an execution; fuID is
		t.Fatalf("ls --repo acme/app: %d runs (%s)", len(lsGot.Runs), lsErrOut)
	}
	var fuRow *runview.Row
	for i := range lsGot.Runs {
		if lsGot.Runs[i].RunID == fuID {
			fuRow = &lsGot.Runs[i]
		}
	}
	if fuRow == nil || fuRow.Status != "pending" {
		t.Fatalf("ls row for %s = %+v, want status pending (stderr %q)", fuID, fuRow, lsErrOut)
	}
}

// TestFollowUpTokenOverrideRefusedWhenNotAllowed: an override naming an
// account this repository is not configured to use is refused through the
// identical claudeAccountAllowed gate a first run's --token goes through,
// before any network call.
func TestFollowUpTokenOverrideRefusedWhenNotAllowed(t *testing.T) {
	f := newCloudFixture(t)
	// No claude_accounts configured at all: evil-name is disallowed.
	seedAppFollowUpRoot(t, f, "")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "evil-name", "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "evil-name") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestFollowUpTokenOverrideRefusedWhenVersionGated: an override to an
// allowed account whose job's build record predates namedTokensSince is
// refused by the same image gate a first run's --token triggers (run.go's
// checkImageSince, applied via the override's spec.OAuthToken).
func TestFollowUpTokenOverrideRefusedWhenVersionGated(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, appClaudeAccountsConfig)
	writeBuildRecord(t, f, appSlug, "web", release060)
	seedAppFollowUpRoot(t, f, "") // root used the default account; old build predates the gate too, but default never triggers it
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-personal", "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.5 or later") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestFollowUpTokenOverrideRefusedWhenAuthNotOAuth: the override goes
// through the same agent.auth: oauth rule a first run's --token does, when a
// checkout is present to read it from.
func TestFollowUpTokenOverrideRefusedWhenAuthNotOAuth(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, appClaudeAccountsConfig)
	appCheckout(t) // acme/app, no agent.auth: oauth
	seedAppFollowUpRoot(t, f, "")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-personal", "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-personal") || !strings.Contains(err.Error(), "agent.auth is") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestFollowUpTokenOverrideRefusedWhenVariantJobMissing: the override never
// falls back to the default job when the new account's variant was never
// deployed (fugaro init --repo not yet re-run) — the same carry-forward
// rule TestRunTokenVariantJobMissingNeverFallsBackToDefault pins for a
// first run.
func TestFollowUpTokenOverrideRefusedWhenVariantJobMissing(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, appClaudeAccountsConfig)
	writeBuildRecord(t, f, appSlug, "web", release065)
	// No AddJob for "web_acme-personal": it does not exist yet.
	seedAppFollowUpRoot(t, f, "")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-personal", "more work")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "fugaro init --repo") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none (the default job must never run instead)", n)
	}
}

// TestFollowUpDocumentedTokenOverrideCommandWorks runs the EXACT command
// reference/followup.md's usage-limit section documents (--token NAME added
// to a plain follow-up), substituting real values for its placeholders, so
// the skill's documented command and the CLI's actual behavior cannot drift
// apart again (review finding, task 9b round 2). A later edit to the
// skill's command shape, or a regression in the override itself, fails
// this test.
func TestFollowUpDocumentedTokenOverrideCommandWorks(t *testing.T) {
	args := documentedFollowUpOverrideCommand(t, "7", "acme/app", "acme-personal", fuID)
	f := newCloudFixture(t)
	f.appendConfig(t, appClaudeAccountsConfig)
	variant := gcp.WorkflowVariant("web", "acme-personal")
	f.run.AddJob(gcp.JobName(appSlug, variant), "4", "8Gi")
	writeBuildRecord(t, f, appSlug, "web", release065)
	seedAppFollowUpRoot(t, f, "")
	var got gcpfake.RunCall
	f.run.OnRun = func(c gcpfake.RunCall) { got = c }
	out, _, err := execute(t, append(args, "more work")...)
	if err != nil {
		t.Fatalf("documented command %v: %v", args, err)
	}
	if !strings.Contains(out, `"status": "launched"`) {
		t.Fatalf("documented command %v: result %s, want launched", args, out)
	}
	if want := gcp.JobName(appSlug, variant); got.Job != want {
		t.Fatalf("documented command %v launched job %q, want %q", args, got.Job, want)
	}
	if got := readSpec(t, f, fuID).OAuthToken; got != "acme-personal" {
		t.Fatalf("documented command %v: OAuthToken = %q, want acme-personal", args, got)
	}
}

// TestFollowUpRepeatRunIDRefusesDifferentToken is review finding (task 9b
// round 3): storedFollowUp's "different task" comparison checked PR, text,
// batch, overrides and recipe, but never --token, so a repeated --run-id
// naming a DIFFERENT account than the one already stored silently reported
// "already-launched" instead of refusing — the opposite of what the
// skill's own account-switch procedure needs (a repeated --run-id there
// means "confirm this launch went through," never "silently keep the
// account that was already launched").
func TestFollowUpRepeatRunIDRefusesDifferentToken(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, appClaudeAccountsConfig) // allows acme-personal and acme-overflow for acme/app
	variant := gcp.WorkflowVariant("web", "acme-personal")
	f.run.AddJob(gcp.JobName(appSlug, variant), "4", "8Gi")
	writeBuildRecord(t, f, appSlug, "web", release065)
	seedAppFollowUpRoot(t, f, "")
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-personal", "more work"); err != nil {
		t.Fatal(err)
	}
	// A different --token on the repeat is refused as a different task,
	// before any second launch: the stored task is acme-personal's.
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-overflow", "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "different task") {
		t.Fatalf("different --token: err = %v", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1 (no launch under acme-overflow)", n)
	}
	// The SAME --token the stored task already has still reports
	// already-launched, not a refusal.
	out, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-personal", "--json", "more work")
	if err != nil || !strings.Contains(out, `"already-launched"`) {
		t.Fatalf("same --token: %s, %v", out, err)
	}
	// No --token at all is the ordinary idempotent repeat and is unchanged.
	out, _, err = execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--json", "more work")
	if err != nil || !strings.Contains(out, `"already-launched"`) {
		t.Fatalf("no --token: %s, %v", out, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1 (still, after both idempotent repeats)", n)
	}
	if got := readSpec(t, f, fuID).OAuthToken; got != "acme-personal" {
		t.Fatalf("stored OAuthToken = %q, want acme-personal (unchanged)", got)
	}
}

// TestFollowUpRepeatRunIDUnlaunchedRefusesDifferentToken is the more
// dangerous half of the same bug: a stored-but-never-launched follow-up
// (the probe the review comment gave) repeated with a different --token
// must never actually launch — under the stored (old) account or any
// other — before being refused.
func TestFollowUpRepeatRunIDUnlaunchedRefusesDifferentToken(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, appClaudeAccountsConfig)
	writeBuildRecord(t, f, appSlug, "web", release065)
	seedAppFollowUpRoot(t, f, "")
	fu := followUpSpec(fuID, rootID, rootID, 7)
	fu.Task = "more work"
	fu.OAuthToken = "acme-personal"
	seedSpec(t, f, fu, false) // stored but never launched
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-overflow", "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "different task") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none: must never launch under the stored (old) account", n)
	}
}

// TestFollowUpRefusesDisallowedInheritedAccount: the previous run's
// task.json is bucket data a launcher can write directly; a follow-up must
// not launch under the account it names when this repository is not (or no
// longer) allowed to use it (carry-forward review note (d) of Tasks 1-7:
// "a hostile record must not make a follow-up launch under an account the
// repository is not allowed to use"). No claude_accounts entry at all here
// allows acme-overflow for acme/other, so the inherited name is disallowed
// regardless of whether it once was.
func TestFollowUpRefusesDisallowedInheritedAccount(t *testing.T) {
	f := newCloudFixture(t)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	root := wallRunIDAt(1, "090000", "a1a6")
	spec := &task.Spec{Version: 1, RunID: root, Repo: "acme/other", Ref: "develop", Workflow: "svc", Task: "x",
		RequestedBy: "someone@example.com", OAuthToken: "acme-overflow"}
	if err := runstore.Open(b, otherSlug, root).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{Version: 1, RunID: root, Repo: "acme/other", Workflow: "svc", Status: runstore.StatusFailed,
		Stage: "writeback", Outcome: runstore.OutcomeDraft, Branch: "fugaro/" + root,
		PushedHead: "1111111111111111111111111111111111111111", StartedAt: time.Now().Add(-time.Hour),
		PR: &runstore.PRRef{Number: 9, URL: "https://github.com/acme/other/pull/9"}}
	if err := runstore.Open(b, otherSlug, root).WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	fu := wallRunIDAt(0, "120000", "a1a7")
	_, _, err = execute(t, "run", "--repo", "acme/other", "--pr", "9", "--run-id", fu, "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-overflow") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunTokenImageGateRefusesOldFirstRun: a first run whose OAuthToken is
// set is refused when the job's build record predates namedTokensSince.
func TestRunTokenImageGateRefusesOldFirstRun(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release060)
	_, _, err := execute(t, "run", "--run-id", "20261012-100000-a1a1", "--token", "acme-personal", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.5 or later") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunTokenImageGateRefusesOldFollowUp: the gate is not skipped just
// because the launch is a follow-up.
func TestRunTokenImageGateRefusesOldFollowUp(t *testing.T) {
	f := newCloudFixture(t)
	// claudeAccountAllowed now checks the allow-list regardless of whether a
	// checkout is present (review finding #2): acme-personal must be allowed
	// for acme/app so this test reaches the image gate it means to exercise.
	f.appendConfig(t, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n")
	writeBuildRecord(t, f, appSlug, "web", release060)
	root := firstRunSpec(rootID)
	root.OAuthToken = "acme-personal"
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.5 or later") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunTokenImageGateAppliesRegardlessOfWorkflowFlag: an explicit
// --workflow does not skip the gate.
func TestRunTokenImageGateAppliesRegardlessOfWorkflowFlag(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release060)
	_, _, err := execute(t, "run", "--run-id", "20261012-100000-a1a2", "--workflow", "svc", "--token", "acme-personal", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro 0.6.5 or later") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunTokenImageGateAppliesWithBatch: --batch is a label, not a separate
// launch path; the gate still applies.
func TestRunTokenImageGateAppliesWithBatch(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release060)
	_, _, err := execute(t, "run", "--run-id", "20261012-100000-a1a3", "--batch", "nightly", "--token", "acme-personal", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro 0.6.5 or later") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunTokenImageGateWarnsOnNonReleaseBase: a dev build base warns, but
// does not refuse, mirroring checkImageSince's existing siblings.
func TestRunTokenImageGateWarnsOnNonReleaseBase(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", "ghcr.io/dimipaun/fugaro-base:dev")
	variant := gcp.WorkflowVariant("svc", "acme-personal")
	f.run.AddJob(gcp.JobName(otherSlug, variant), "2", "4Gi")
	_, errOut, err := execute(t, "run", "--run-id", "20261012-100000-a1a4", "--token", "acme-personal", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "warning:") || !strings.Contains(errOut, "named Claude accounts") {
		t.Fatalf("stderr %q should warn, not refuse", errOut)
	}
}

// TestRunTokenImageGateSkippedForDefaultAccount: no change to the image
// gate's behavior for an installation with no claude_accounts (design §5):
// no build record is needed at all when OAuthToken stays "".
func TestRunTokenImageGateSkippedForDefaultAccount(t *testing.T) {
	newCloudFixture(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261012-100000-a1a5", "A task"); err != nil {
		t.Fatal(err)
	}
}

// followUpOverrideCommandRE finds reference/followup.md's documented
// account-switch relaunch: a fenced inline span naming fugaro run --pr,
// --token and --run-id together, so a later rewording of the skill's own
// text is read fresh each time, not copied into the test.
var followUpOverrideCommandRE = regexp.MustCompile("`(fugaro run --pr N --repo <owner/name> --token NAME --run-id <id> --json)`")

// documentedFollowUpOverrideCommand reads reference/followup.md's own
// account-switch relaunch command and substitutes real values for its
// placeholders (N, <owner/name>, NAME, <id>), returning it as exec args
// (without the leading "fugaro"). Fails the test outright if the skill no
// longer documents that exact command shape, so a doc rewording and a
// behavior regression are caught by the same test, not two that can drift
// apart.
func documentedFollowUpOverrideCommand(t *testing.T, pr, repo, name, runID string) []string {
	t.Helper()
	data, err := os.ReadFile("../../plugin/skills/working/reference/followup.md")
	if err != nil {
		t.Fatal(err)
	}
	m := followUpOverrideCommandRE.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("reference/followup.md no longer documents the account-switch relaunch command (fugaro run --pr N --repo <owner/name> --token NAME --run-id <id> --json)")
	}
	fields := strings.Fields(m[1])
	if fields[0] != "fugaro" {
		t.Fatalf("documented command %q does not start with fugaro", m[1])
	}
	placeholders := map[string]string{"N": pr, "<owner/name>": repo, "NAME": name, "<id>": runID}
	for i, f := range fields {
		if v, ok := placeholders[f]; ok {
			fields[i] = v
		}
	}
	return fields[1:]
}
