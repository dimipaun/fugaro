package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

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
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.1 or later") {
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
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "named Claude accounts") || !strings.Contains(err.Error(), "fugaro 0.6.1 or later") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}
