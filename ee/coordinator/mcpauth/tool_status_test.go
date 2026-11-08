// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The read tools read one project's own runs, so their tests need a runs
// bucket with two projects in it: this one's repository, and another
// repository the same installation's bucket holds. The fixture is the reach
// (a bucket and one project's slugs), the registry the four tools are
// registered on, and the seeds.
const (
	// testSlug is a repository of the project the read tools are registered
	// for, spelled the way the runs tree keys it (task.Slug's own shape:
	// a readable name and a hash).
	testSlug = "aurora-app-9f8e7d6c5b4a3210"
	// testOtherSlug is another project's repository in the same bucket. The
	// installation's runs bucket holds a run of every repository somebody
	// launched from its project, and this is the one the fence exists for.
	testOtherSlug = "globex-app-0123456789abcdef"

	// testRunID is the project's own run, and testOtherRunID is the other
	// project's — minted later, so a list that leaked it would name it first
	// rather than last. testDeadRunID is a second run of the project's own,
	// for the case whose record names no stage.
	testRunID      = "20300101-120000-a1b2"
	testOtherRunID = "20300102-120000-c3d4"
	testDeadRunID  = "20300101-130000-e5f6"

	// testRunsBucket is the runs bucket of this installation, derived the way
	// production derives it: fugaro-runs-<gcp-project>.
	testRunsBucket = runsBucketPrefix + testProject
)

// runsFixture is one project's own runs as the read tools read them.
type runsFixture struct {
	t     *testing.T
	reg   *Registry
	reach *ProjectRuns
}

func newRunsFixture(t *testing.T) *runsFixture {
	t.Helper()
	g := gcpfake.NewGCS(t)
	g.AddBucket(testRunsBucket, 1234, nil)
	reach, err := NewProjectRuns(g.Bucket(t, testRunsBucket).Bucket, []string{testSlug})
	if err != nil {
		t.Fatalf("NewProjectRuns: %v", err)
	}
	// The clock is the fixture's, so the list's default window is a fact
	// about the fixture and not about the day the suite runs.
	reach.now = func() time.Time { return testNow }
	reg := NewRegistry()
	if err := RegisterReadTools(reg, reach); err != nil {
		t.Fatalf("RegisterReadTools: %v", err)
	}
	return &runsFixture{t: t, reg: reg, reach: reach}
}

// seedRun writes one run's objects into the bucket: the task.json that makes
// it a run at all (the marker runstore.Locate reads a run by and this
// package's own locate follows), its record when it has one, and its other
// objects by name.
func (f *runsFixture) seedRun(slug, runID string, rec *runstore.Record, files map[string][]byte) {
	f.t.Helper()
	ctx := context.Background()
	st := runstore.Open(f.reach.bucket, slug, runID)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/aurora", Ref: "main"}
	if err := st.WriteTask(ctx, spec); err != nil {
		f.t.Fatalf("writing task.json of %s/%s: %v", slug, runID, err)
	}
	if rec != nil {
		rec.Version, rec.RunID, rec.Repo = 1, runID, "acme/aurora"
		if err := st.WriteRecord(ctx, rec); err != nil {
			f.t.Fatalf("writing result.json of %s/%s: %v", slug, runID, err)
		}
	}
	for name, data := range files {
		if err := st.PutFile(ctx, name, data, "application/octet-stream"); err != nil {
			f.t.Fatalf("writing %s of %s/%s: %v", name, slug, runID, err)
		}
	}
}

// call runs one read tool through the registry, which is the one path the
// door reaches a handler by (resource.go's callTool), and answers its text.
func (f *runsFixture) call(tool, args string) (string, error) {
	f.t.Helper()
	out, err := f.reg.Call(context.Background(), Call{
		Tool:      tool,
		Arguments: json.RawMessage(args),
		Scopes:    []string{ScopeTasksRead},
	})
	if err != nil {
		return "", err
	}
	text, ok := out.(string)
	if !ok {
		f.t.Fatalf("%s answered %T: a read tool's answer is JSON text, which is what lets it be redacted before the door writes it", tool, out)
	}
	return text, nil
}

// answer runs one read tool that must answer, and decodes its JSON into v.
func (f *runsFixture) answer(tool, args string, v any) string {
	f.t.Helper()
	text, err := f.call(tool, args)
	if err != nil {
		f.t.Fatalf("%s(%s): %v", tool, args, err)
	}
	if err := json.Unmarshal([]byte(text), v); err != nil {
		f.t.Fatalf("%s(%s) answered text that is not the JSON it should be: %v", tool, args, err)
	}
	return text
}

// refusal runs one read tool that must refuse.
func (f *runsFixture) refusal(tool, args string) error {
	f.t.Helper()
	text, err := f.call(tool, args)
	if err == nil {
		f.t.Fatalf("%s(%s) answered %q; want a refusal", tool, args, text)
	}
	return err
}

// TestListTasksNamesOnlyTheProjectsRuns is M8's red step: list_tasks answers
// the runs of the repositories this project's own list holds, and a run
// belonging to another project — in the same bucket, minted later, so a list
// that leaked it would put it first — is absent from the answer and
// unreachable by the identifier it is named by.
func TestListTasksNamesOnlyTheProjectsRuns(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady, StartedAt: testNow}, nil)
	f.seedRun(testOtherSlug, testOtherRunID, &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady}, nil)

	var rows []taskStatus
	text := f.answer("list_tasks", `{}`, &rows)
	if len(rows) != 1 || rows[0].RunID != testRunID {
		t.Fatalf("list_tasks named %d run(s) (%v); want only this project's %s", len(rows), rows, testRunID)
	}
	if rows[0].Repo != testSlug || rows[0].Status != string(runstore.StatusSucceeded) {
		t.Errorf("list_tasks answered %+v; want the project's own run of %s, succeeded", rows[0], testSlug)
	}
	if strings.Contains(text, testOtherSlug) || strings.Contains(text, testOtherRunID) {
		t.Errorf("the answer names another project's runs: %s", text)
	}

	// The other project's run is unreachable by both of the names it has: its
	// own <repo-slug>/<run-id>, and the bare run ID, which exists in the
	// bucket and is not looked for outside the project's own repositories.
	for _, args := range []string{
		`{"run_id":"` + testOtherSlug + `/` + testOtherRunID + `"}`,
		`{"run_id":"` + testOtherRunID + `"}`,
	} {
		err := f.refusal("get_task_status", args)
		if !errors.Is(err, runstore.ErrNotFound) {
			t.Errorf("another project's run, asked for as %s, answered %v; want runstore.ErrNotFound", args, err)
		}
	}
}

// TestStatusOfAnUnknownRunIsRefused is the other half of the same rule: a run
// this project does not own is refused, and so is anything that is not a run
// of it — an identifier of the wrong shape, a bare run ID nobody minted, and a
// call that names no run at all. The known run is answered first, so the
// refusals below are not vacuous.
func TestStatusOfAnUnknownRunIsRefused(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{Status: runstore.StatusRunning, Stage: "implement", StartedAt: testNow}, nil)

	var status taskStatus
	f.answer("get_task_status", `{"run_id":"`+testRunID+`"}`, &status)
	if status.RunID != testRunID || status.Repo != testSlug || status.Status != string(runstore.StatusRunning) || status.Stage != "implement" {
		t.Fatalf("get_task_status answered %+v; want this project's run %s, running, at implement", status, testRunID)
	}
	f.answer("get_task_status", `{"run_id":"`+testSlug+`/`+testRunID+`"}`, &status)

	for _, args := range []string{
		`{}`,
		`{"run_id":""}`,
		`{"run_id":"20300101-120000-ffff"}`,
		`{"run_id":"../../etc/passwd"}`,
		`{"run_id":"` + testSlug + `/not-a-run-id"}`,
		`{"run_id":"` + testOtherSlug + `/` + testOtherRunID + `"}`,
	} {
		if err := f.refusal("get_task_status", args); err == nil || err.Error() == "" {
			t.Errorf("get_task_status(%s) was refused with no reason", args)
		}
	}
}

// TestResultNeverQuotesAToken is M-g's rule for the outcome tool: the run's
// own session file can hold a credential the agent printed
// (internal/runner/session.go), so get_result serves the run's record and its
// own diff and never that file — the answer carries the diff it should and
// nothing of the session beside it.
func TestResultNeverQuotesAToken(t *testing.T) {
	f := newRunsFixture(t)
	const uuid = "3f1c1b2a-8d4e-4f60-9a71-2c3d4e5f6a7b"
	const sessionLine = `{"type":"assistant","text":"a line the run's session recorded: ` + testSeededToken + `"}`
	f.seedRun(testSlug, testRunID, &runstore.Record{
		Status:       runstore.StatusSucceeded,
		Outcome:      runstore.OutcomeReady,
		PR:           &runstore.PRRef{Number: 7, URL: "https://github.com/acme/aurora/pull/7"},
		PatchObject:  runstore.PatchName,
		ChangedFiles: &runstore.ChangedFiles{Paths: []string{"x.go"}, Total: 1},
	}, map[string][]byte{
		runstore.PatchName:           []byte("--- a/x.go\n+++ b/x.go\n+the run's own line\n"),
		"session/session.json":       []byte(`{"version":1,"id":"` + uuid + `","head_sha":"deadbeef","pushed":true,"workdir":"/work","bytes":123}`),
		"session/" + uuid + ".jsonl": []byte(sessionLine),
	})

	var res runResult
	text := f.answer("get_result", `{"run_id":"`+testRunID+`"}`, &res)
	if res.PR == nil || res.PR.Number != 7 {
		t.Errorf("get_result answered %+v; want the run's pull request link", res)
	}
	if !strings.Contains(res.Patch, "the run's own line") {
		t.Fatalf("get_result answered no diff (%+v); the run's own patch.diff is what the tool is for", res)
	}
	if strings.Contains(text, testSeededToken) {
		t.Errorf("the answer quotes a token the run's own session recorded: %s", text)
	}
	if slices.Contains(res.ChangedFiles, testSeededToken) {
		t.Errorf("the answer carries the session's own text as a changed path: %v", res.ChangedFiles)
	}
}
