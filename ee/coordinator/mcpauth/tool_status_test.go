// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
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
	// testSiblingSlug is a second repository of the project the read tools
	// answer for: the fence is per repository as well as per project, so a
	// listing narrowed to one of them answers that one's runs and not its
	// sibling's.
	testSiblingSlug = "aurora-docs-0a1b2c3d4e5f6071"
	// testOtherSlug is another project's repository in the same bucket. The
	// installation's runs bucket holds a run of every repository somebody
	// launched from its project, and this is the one the fence exists for.
	testOtherSlug = "globex-app-0123456789abcdef"

	// testRunID is the project's own run, and testOtherRunID is the other
	// project's — minted later, so a list that leaked it would name it first
	// rather than last. testSiblingRunID is the sibling repository's, minted
	// after testRunID for the same reason. testDeadRunID is a second run of
	// the project's own, for the case whose record names no stage.
	testRunID        = "20300101-120000-a1b2"
	testSiblingRunID = "20300101-140000-7e8f"
	testOtherRunID   = "20300102-120000-c3d4"
	testDeadRunID    = "20300101-130000-e5f6"

	// The rest are one run per bound a read has: testCappedRunID wrote more
	// transcripts than one call reads, testExactRunID more lines than one call
	// answers (an exact multiple of the line cap, which is the count a check
	// made after the read cannot see), testBigTranscriptRunID one transcript
	// over runstore.MaxTranscriptBytes, and testBigPatchRunID a diff over
	// runstore.MaxRecordBytes.
	testCappedRunID        = "20300101-150000-9c0d"
	testExactRunID         = "20300101-160000-1f2e"
	testBigTranscriptRunID = "20300101-170000-3d4c"
	testBigPatchRunID      = "20300101-180000-5b6a"

	// testCorruptRunID wrote a result.json that does not decode and
	// testTooBigRecordRunID one over runstore.MaxRecordBytes: both are the
	// run's own object, written by the run's own agent, and each is one of the
	// two ways a record is there and unusable.
	testCorruptRunID      = "20300101-190000-7a8b"
	testTooBigRecordRunID = "20300101-200000-9c0d"

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
	reach, err := NewProjectRuns(g.Bucket(t, testRunsBucket).Bucket, []string{testSlug, testSiblingSlug})
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
// unreachable by the identifier it is named by. The same list is what a `repo`
// argument narrows to, and the only argument of these tools that names a
// repository: a slug outside it is refused before the bucket is read.
func TestListTasksNamesOnlyTheProjectsRuns(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady, StartedAt: testNow}, nil)
	f.seedRun(testSiblingSlug, testSiblingRunID, &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady}, nil)
	f.seedRun(testOtherSlug, testOtherRunID, &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady}, nil)

	var rows []taskStatus
	text := f.answer("list_tasks", `{}`, &rows)
	named := map[string]string{}
	for _, row := range rows {
		named[row.Repo+"/"+row.RunID] = row.Status
	}
	if len(rows) != 2 || named[testSlug+"/"+testRunID] != string(runstore.StatusSucceeded) ||
		named[testSiblingSlug+"/"+testSiblingRunID] != string(runstore.StatusSucceeded) {
		t.Fatalf("list_tasks named %v; want this project's own two runs, %s and %s", rows, testRunID, testSiblingRunID)
	}
	if strings.Contains(text, testOtherSlug) || strings.Contains(text, testOtherRunID) {
		t.Errorf("the answer names another project's runs: %s", text)
	}

	// A repository of the project's own narrows the listing to that one, and
	// the narrowing is the project's own list rather than a filter over the
	// bucket: the sibling's run is newer and would be named first if it leaked.
	rows = nil
	f.answer("list_tasks", `{"repo":"`+testSlug+`"}`, &rows)
	if len(rows) != 1 || rows[0].RunID != testRunID || rows[0].Repo != testSlug {
		t.Errorf("list_tasks of %s named %v; want only that repository's own run", testSlug, rows)
	}

	// And a repository the project does not hold is refused rather than read:
	// another project's slug, and a name that is nobody's, alike.
	for _, args := range []string{`{"repo":"` + testOtherSlug + `"}`, `{"repo":"no-such-repository"}`} {
		f.refusal("list_tasks", args)
	}

	// `since` narrows the window and cannot widen it. The two runs are an hour
	// and three hours before the fixture's clock, so a `since` between them
	// answers the newer one alone and one after both answers nothing — the same
	// build whether the bucket honoured the offset or filtered the whole list.
	rows = nil
	f.answer("list_tasks", `{"since":"2030-01-01T13:00:00Z"}`, &rows)
	if len(rows) != 1 || rows[0].RunID != testSiblingRunID {
		t.Errorf("list_tasks since 13:00 named %v; want only the run minted after it", rows)
	}
	rows = nil
	f.answer("list_tasks", `{"since":"2030-01-01T15:00:00Z"}`, &rows)
	if len(rows) != 0 {
		t.Errorf("list_tasks since 15:00 named %v; want nothing it is newer than", rows)
	}

	// A window wider than the one the tool answers over is refused rather than
	// clamped onto it: the answer carries no window of its own, so a caller
	// whose `since` had been silently narrowed could not tell the rows it asked
	// for from the rows it got. The refusal is also the bound's only statement,
	// since the schema says 30 days and the answer says nothing.
	for _, args := range []string{
		`{"since":"0001-01-01T00:00:00Z"}`,
		`{"since":"not-a-time"}`,
	} {
		if err := f.refusal("list_tasks", args); err == nil || err.Error() == "" {
			t.Errorf("list_tasks(%s) was refused with no reason", args)
		}
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

// TestListTasksKeepsARowWhoseRecordIsUnusable: a result.json that does not
// decode, or one past runstore.MaxRecordBytes, costs its own row its status
// and nothing else.
//
// The record is the run's own object and the run's own agent writes it, so one
// bad object is the run's problem rather than the listing's — the rule
// internal/cli/runs.go applies to the same failure (corruptObject: "that is
// the run's problem, not the listing's"). Refusing the whole answer for it
// would make every other run of the project unreachable by the one tool that
// names them, and the run that wrote the bad object is exactly the one a
// caller is asking about.
func TestListTasksKeepsARowWhoseRecordIsUnusable(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady}, nil)
	// Neither of the two below can be seeded through WriteRecord, which writes
	// the record it is given: one run's object is bytes that are not JSON at
	// all, and the other is longer than a read will take.
	f.seedRun(testSlug, testCorruptRunID, nil, map[string][]byte{"result.json": []byte("{not json")})
	f.seedRun(testSlug, testTooBigRecordRunID, nil, map[string][]byte{
		"result.json": bytes.Repeat([]byte(" "), runstore.MaxRecordBytes+1),
	})

	var rows []taskStatus
	text := f.answer("list_tasks", `{}`, &rows)
	if len(rows) != 3 {
		t.Fatalf("list_tasks named %d row(s) %s; want all three of the project's runs, two of them unreadable", len(rows), text)
	}
	byID := map[string]taskStatus{}
	for _, row := range rows {
		byID[row.RunID] = row
	}
	if got := byID[testRunID]; got.Status != string(runstore.StatusSucceeded) || got.Unreadable {
		t.Errorf("list_tasks answered %+v for the run whose record reads; want its status", got)
	}
	for _, id := range []string{testCorruptRunID, testTooBigRecordRunID} {
		got, ok := byID[id]
		if !ok {
			t.Fatalf("list_tasks left out the row of %s; want its row kept and marked, because one run's own unusable record is not the project's listing", id)
		}
		if !got.Unreadable || got.Repo != testSlug || got.RunID != id || got.Status != "" || got.PR != nil {
			t.Errorf("list_tasks answered %+v for %s; want the row kept, its repository and ID named, marked unreadable, and no status invented for it", got, id)
		}
	}

	// The same run asked about on its own is refused instead: a listing has
	// rows to keep, and a question about one run has nothing to answer with —
	// the run's state is not this package's to invent.
	err := f.refusal("get_task_status", `{"run_id":"`+testCorruptRunID+`"}`)
	if errors.Is(err, runstore.ErrNotFound) {
		t.Errorf("get_task_status of a run whose record does not decode answered %v; want the read failure, not a run that is not there", err)
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

	// A diff in the band between the store's record cap and the runner's own
	// patch cap is still answered: reading it through Store.ReadFile refuses
	// anything over runstore.MaxRecordBytes, and a refusal there is the whole
	// answer — the status, the pull request and the changed files with it.
	var big strings.Builder
	for big.Len() <= runstore.MaxRecordBytes {
		big.WriteString("+a line of the run's own diff\n")
	}
	f.seedRun(testSlug, testBigPatchRunID, &runstore.Record{
		Status:       runstore.StatusSucceeded,
		Outcome:      runstore.OutcomeReady,
		PR:           &runstore.PRRef{Number: 8, URL: "https://github.com/acme/aurora/pull/8"},
		PatchObject:  runstore.PatchName,
		ChangedFiles: &runstore.ChangedFiles{Paths: []string{"x.go"}, Total: 1},
	}, map[string][]byte{runstore.PatchName: []byte(big.String())})

	res = runResult{}
	f.answer("get_result", `{"run_id":"`+testBigPatchRunID+`"}`, &res)
	if res.PR == nil || res.PR.Number != 8 || res.Status != string(runstore.StatusSucceeded) {
		t.Errorf("get_result answered %+v for a run whose diff is over the store's record cap; want its record", res)
	}
	if len(res.Patch) != maxPatchBytes || !res.PatchTruncated {
		t.Errorf("get_result answered a %d-byte diff (truncated=%v); want the head cut at %d and marked as cut", len(res.Patch), res.PatchTruncated, maxPatchBytes)
	}
	if !strings.Contains(res.Patch, "the run's own diff") {
		t.Errorf("get_result answered a diff that is not the run's own: %q", res.Patch[:min(len(res.Patch), 200)])
	}
}
