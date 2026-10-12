package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestRunTokenImageGateAppliesOnRetry is review finding #3: nothing pinned
// the namedTokensSince gate on fugaro run --retry, so a mutation scoping the
// gate's call site to "&& o.retry == \"\"" (skipping it on every retry)
// would have stayed green. --retry replays a task stored but never
// launched; if its OAuthToken is set and its job's build record predates
// namedTokensSince, the retry must still be refused.
func TestRunTokenImageGateAppliesOnRetry(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n")
	writeBuildRecord(t, f, appSlug, "web", release060)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: "20261012-100000-b5b5", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		RequestedBy: "someone@example.com", OAuthToken: "acme-personal"}
	if err := runstore.Open(b, appSlug, spec.RunID).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "run", "--retry", spec.RunID)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.5 or later") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunTokenImageGateAppliesOnReusedStoredFollowUp is review finding #3's
// second named gap: a repeated --run-id of a follow-up stored but never
// launched (storedFollowUp's reuse path, distinct from TestRunPRRepeat
// RunIDIsIdempotent's "already-launched" case, which never reaches the
// image gate at all since prior != nil there). The gate must still fire
// here: prior == nil (nothing launched yet) even though the task itself is
// reused as stored, not freshly resolved.
func TestRunTokenImageGateAppliesOnReusedStoredFollowUp(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n")
	writeBuildRecord(t, f, appSlug, "web", release060)
	root := firstRunSpec(rootID)
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)
	// The follow-up's task is already stored (an earlier attempt that never
	// launched), named: storedFollowUp reuses it as it is.
	fu := followUpSpec(fuID, rootID, rootID, 7)
	fu.OAuthToken = "acme-personal"
	seedSpec(t, f, fu, false)

	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.5 or later") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRetryRefusesDisallowedStoredAccount is review finding A (round 2):
// retrySpec read a stored task.json and returned its OAuthToken as is,
// never calling claudeAccountAllowed — bucket data nobody vouches for. A
// stored task naming an account the local config doesn't (or no longer)
// allow for this repository must still be refused on --retry, even though
// the variant job is deployed and would otherwise launch successfully.
func TestRunRetryRefusesDisallowedStoredAccount(t *testing.T) {
	f := newCloudFixture(t)
	// No claude_accounts configured at all: acme-personal is not allowed
	// for acme/app. The variant job is deployed anyway, so the bug this
	// pins would have let the retry actually launch.
	variant := gcp.WorkflowVariant("web", "acme-personal")
	f.run.AddJob(gcp.JobName(appSlug, variant), "4", "8Gi")
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: "20261013-100000-d1d1", Repo: "acme/app", Ref: "main", Workflow: "web",
		Task: "x", RequestedBy: "someone@example.com", OAuthToken: "acme-personal"}
	if err := runstore.Open(b, appSlug, spec.RunID).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "run", "--retry", spec.RunID)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-personal") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestFollowUpRepeatRunIDRefusesDisallowedStoredAccount is review finding
// A's second path: storedFollowUp (a repeated --run-id of a follow-up
// stored but never launched) returned its stored OAuthToken unchecked too.
// Must refuse before resolvePR or any build-record read.
func TestFollowUpRepeatRunIDRefusesDisallowedStoredAccount(t *testing.T) {
	f := newCloudFixture(t)
	// No claude_accounts configured at all: acme-personal is not allowed.
	variant := gcp.WorkflowVariant("web", "acme-personal")
	f.run.AddJob(gcp.JobName(appSlug, variant), "4", "8Gi")
	root := firstRunSpec(rootID)
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)
	fu := followUpSpec(fuID, rootID, rootID, 7)
	fu.OAuthToken = "acme-personal"
	seedSpec(t, f, fu, false)

	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-personal") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}
