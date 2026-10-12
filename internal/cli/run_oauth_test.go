package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// release061 is a build record's base_ref at exactly namedTokensSince: a
// runner built from it knows task.Spec.OAuthToken.
const release061 = "ghcr.io/dimipaun/fugaro-web-node:0.6.1"

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
		"workflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"
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
		writeBuildRecord(t, f, otherSlug, "svc", release061)
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
		writeBuildRecord(t, f, otherSlug, "svc", release061)
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
	writeBuildRecord(t, f, otherSlug, "svc", release061)
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
	writeBuildRecord(t, f, otherSlug, "svc", release061)
	// No AddJob for the "svc_acme-personal" variant: it does not exist yet.
	_, _, err := execute(t, "run", "--run-id", "20261012-100000-ffff", "--token", "acme-personal", "A task")
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "fugaro init --repo") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none (the default job must never run instead)", n)
	}
}

// TestFollowUpInheritsOAuthToken: a follow-up's task.Spec.OAuthToken is the
// previous run's, never a --token the follow-up command passes; a warning
// is printed instead of an error.
func TestFollowUpInheritsOAuthToken(t *testing.T) {
	f := newCloudFixture(t)
	f.run.AddJob(gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-personal")), "4", "8Gi")
	writeBuildRecord(t, f, appSlug, "web", release061)
	root := firstRunSpec(rootID)
	root.OAuthToken = "acme-personal"
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--token", "acme-overflow", "more work")
	if err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).OAuthToken; got != "acme-personal" {
		t.Fatalf("follow-up OAuthToken = %q, want acme-personal (inherited)", got)
	}
	if !strings.Contains(errOut, "acme-overflow") || !strings.Contains(errOut, "ignored") || !strings.Contains(errOut, "acme-personal") {
		t.Fatalf("stderr %q should warn that --token is ignored on a follow-up", errOut)
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
	writeBuildRecord(t, f, otherSlug, "svc", release061)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	root := runIDAt(1, "090000", "a1a6")
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
	fu := runIDAt(0, "120000", "a1a7")
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
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.1 or later") {
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
	writeBuildRecord(t, f, appSlug, "web", release060)
	root := firstRunSpec(rootID)
	root.OAuthToken = "acme-personal"
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.1 or later") {
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
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro 0.6.1 or later") {
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
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro 0.6.1 or later") {
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
	writeBuildRecord(t, f, otherSlug, "svc", "ghcr.io/dimipaun/fugaro-web-node:dev")
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
