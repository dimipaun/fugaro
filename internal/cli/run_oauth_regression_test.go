package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// TestRunTokenRepeatSameRunIDReportsAlreadyLaunched is review finding #1
// (named oauth tokens Task 6 follow-up round): checkExecution used to
// compare a named run's execution against the PLAIN job
// (gcp.CheckRunExecution(..., spec.Workflow)), but a named run starts
// JobName(slug, WorkflowVariant(workflow, token)) — so a repeat of the same
// --run-id, which calls existingLaunch -> checkExecution, refused its own
// execution with "belongs to job ..., not the run's job ...". Fixed:
// checkExecution now compares against gcp.WorkflowVariant(spec.Workflow,
// spec.OAuthToken). A repeat must report "already-launched", never an error.
func TestRunTokenRepeatSameRunIDReportsAlreadyLaunched(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	variant := gcp.WorkflowVariant("svc", "acme-personal")
	f.run.AddJob(gcp.JobName(otherSlug, variant), "2", "4Gi")
	args := []string{"run", "--run-id", "20261012-100000-b1b1", "--token", "acme-personal", "--json", "A task"}

	out1, _, err := execute(t, args...)
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	var res1 launchResult
	if err := json.Unmarshal([]byte(out1), &res1); err != nil || res1.Status != "launched" {
		t.Fatalf("first launch result = %s, %v", out1, err)
	}

	out2, _, err := execute(t, args...)
	if err != nil {
		t.Fatalf("repeated --run-id: %v", err)
	}
	var res2 launchResult
	if err := json.Unmarshal([]byte(out2), &res2); err != nil || res2.Status != "already-launched" {
		t.Fatalf("repeat result = %s, %v, want already-launched", out2, err)
	}
	if len(f.run.Executions()) != 1 {
		t.Fatalf("%d executions, want exactly 1 (no second launch)", len(f.run.Executions()))
	}
}

// TestLsNamedRunIsPendingNotError is review finding #1's second named
// regression: before the fix, ls (which calls checkExecution through
// followExecution) reported every named run as StatusError ("not following
// its execution"), since it compared against the plain job. A named run
// with only a launch.json (no result.json yet) must show StatusPending,
// exactly as an unnamed run would at the same point.
func TestLsNamedRunIsPendingNotError(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	variant := gcp.WorkflowVariant("svc", "acme-personal")
	f.run.AddJob(gcp.JobName(otherSlug, variant), "2", "4Gi")
	// The id carries today's date, not a fixed one: ls lists the last 7 days by
	// default, so an id dated exactly seven days ago falls out of the window as
	// soon as the clock passes the hour in it — from then on, permanently. This
	// test read 0 runs that way from 2026-10-19T10:00Z on, and the full-suite
	// run of PR #642's security round is what met it. runs_test.go's own ids
	// carry today's date for the same reason (TestLsFollowsTheRecordsExecution).
	id := time.Now().UTC().Format("20060102") + "-100000-b2b2"
	if _, _, err := execute(t, "run", "--run-id", id, "--token", "acme-personal", "A task"); err != nil {
		t.Fatal(err)
	}
	got, errOut := lsJSON(t, "--repo", "acme/other")
	if len(got.Runs) != 1 {
		t.Fatalf("ls --repo acme/other: %d runs, want 1 (%s)", len(got.Runs), errOut)
	}
	if r := got.Runs[0]; r.Status != runview.StatusPending {
		t.Fatalf("row = %+v, want status %q (stderr %q)", r, runview.StatusPending, errOut)
	}
	if strings.Contains(errOut, "warning") || strings.Contains(errOut, "not following") {
		t.Fatalf("ls printed a warning for a healthy named run: %q", errOut)
	}
}

// writeOtherRecord stores rec as run id's result.json on otherSlug
// (acme/other): no runner runs in these tests, so a real launch's finalize
// record is written by hand, the same way followup_test.go's writeRecord
// does for appSlug.
func writeOtherRecord(t *testing.T, f *cloudFixture, id string, rec *runstore.Record) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	if err := runstore.Open(b, otherSlug, id).WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// TestFollowUpAfterRealNamedLaunch is review finding #1's third named
// regression: resolvePR refuses a follow-up outright when any run on the PR
// has StatusError ("run ... can't be read ..., so the PR's state is
// unknown"), which followExecution set for every named run before the fix.
// The earlier named-tokens follow-up tests (TestFollowUpInheritsOAuthToken
// and friends) seeded their root run unlaunched (no launch.json or
// execution), so they never exercised checkExecution/followExecution at all
// and missed this. Here the root run is a REAL launch (launch.json and a
// live execution, like TestRunPRLaunchesFollowUp), so resolvePR must
// actually follow its execution to see it pushed successfully.
func TestFollowUpAfterRealNamedLaunch(t *testing.T) {
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	variant := gcp.WorkflowVariant("svc", "acme-personal")
	f.run.AddJob(gcp.JobName(otherSlug, variant), "2", "4Gi")

	rootID := "20261010-090000-c1c1"
	out, _, err := execute(t, "run", "--run-id", rootID, "--token", "acme-personal", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("root launch: %v", err)
	}
	var root launchResult
	if err := json.Unmarshal([]byte(out), &root); err != nil || root.Status != "launched" {
		t.Fatalf("root launch result = %s, %v", out, err)
	}
	// resolvePR needs a run on the PR's branch that has pushed: give the root
	// run's record a pushed_head and the PR, as a real finalize would.
	c := runstore.NewCost(1, 0.5, runstore.BasisAPIList)
	rec := &runstore.Record{Version: 1, RunID: rootID, Repo: "acme/other", Workflow: "svc", Execution: root.Execution,
		Status: runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Branch: root.Branch, PushedHead: "1111111111111111111111111111111111111111", CostUSD: 1, Cost: &c,
		PR: &runstore.PRRef{Number: 7, URL: "https://github.com/acme/other/pull/7"}}
	writeOtherRecord(t, f, rootID, rec)

	fuID := "20261010-120000-c2c2"
	out, _, err = execute(t, "run", "--repo", "acme/other", "--pr", "7", "--run-id", fuID, "--json", "more work")
	if err != nil {
		t.Fatalf("follow-up: %v", err)
	}
	var fu launchResult
	if err := json.Unmarshal([]byte(out), &fu); err != nil || fu.Status != "launched" || fu.PR != 7 || fu.PreviousRun != rootID {
		t.Fatalf("follow-up result = %s, %v", out, err)
	}
	if got := otherSpec(t, f, fuID).OAuthToken; got != "acme-personal" {
		t.Fatalf("follow-up OAuthToken = %q, want acme-personal (inherited)", got)
	}
}
