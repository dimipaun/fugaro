// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// The next three are the ways a record is there and unusable, each one the
	// run's own object and each one writable by the run's own agent:
	// testCorruptRunID wrote bytes that are not JSON at all, testBadTimeRunID
	// one that decodes into the record's own shape but not into its fields (a
	// timestamp that is not a time, which is the decoder's business and the
	// time package's rather than a JSON type), and testTooBigRecordRunID one
	// over runstore.MaxRecordBytes.
	testCorruptRunID      = "20300101-190000-7a8b"
	testBadTimeRunID      = "20300101-193000-4e5f"
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
// The three records below are the ways that goes: bytes that are not JSON, one
// that decodes into the record's own shape but not into its fields (a
// timestamp that is not a time — a failure the JSON decoder reports as the
// time package's, not as one of encoding/json's own types, which is why the
// test is what holds the sentinel in place), and one longer than a read will
// take.
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
	// None of the three below can be seeded through WriteRecord, which writes
	// the record it is given.
	f.seedRun(testSlug, testCorruptRunID, nil, map[string][]byte{"result.json": []byte("{not json")})
	f.seedRun(testSlug, testBadTimeRunID, nil, map[string][]byte{"result.json": []byte(`{"started_at":"not a time"}`)})
	f.seedRun(testSlug, testTooBigRecordRunID, nil, map[string][]byte{
		"result.json": bytes.Repeat([]byte(" "), runstore.MaxRecordBytes+1),
	})
	unreadable := []string{testCorruptRunID, testBadTimeRunID, testTooBigRecordRunID}

	var rows []taskStatus
	text := f.answer("list_tasks", `{}`, &rows)
	if len(rows) != len(unreadable)+1 {
		t.Fatalf("list_tasks named %d row(s) %s; want all %d of the project's runs, %d of them unreadable", len(rows), text, len(unreadable)+1, len(unreadable))
	}
	byID := map[string]taskStatus{}
	for _, row := range rows {
		byID[row.RunID] = row
	}
	if got := byID[testRunID]; got.Status != string(runstore.StatusSucceeded) || got.Unreadable {
		t.Errorf("list_tasks answered %+v for the run whose record reads; want its status", got)
	}
	for _, id := range unreadable {
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
	for _, id := range unreadable {
		err := f.refusal("get_task_status", `{"run_id":"`+id+`"}`)
		if errors.Is(err, runstore.ErrNotFound) {
			t.Errorf("get_task_status of %s, whose record cannot be used, answered %v; want the read failure, not a run that is not there", id, err)
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

// testSpoofing is a run's own text carrying the two things the redaction is
// for and that a JSON encoder does not write away: an escape sequence, which
// would repaint the terminal a client is read in, and U+202E, RIGHT-TO-LEFT
// OVERRIDE, which makes the text after it read backwards (internal/safetext).
// Both are characters a run's own output or its record can carry, so both are
// redactions a test has to reach.
const testSpoofing = "\x1b[31m a reversal‮"

// TestARecordsOwnFieldsAreRedactedInEveryAnswer: the strings a run wrote reach
// no answer as they were written — not the record's own fields (its reason, its
// branch, its pull request URL), not the paths it changed, not a line of the
// diff it uploaded (spec.md §5, M-g).
//
// The answer is read as text rather than only decoded, because decoding undoes
// what the JSON encoder did: a control the encoder escaped as \u001b comes back
// as the byte itself, so a struct that looks clean can still be a document a
// terminal obeys. The plain words are asserted beside the characters, so a
// redaction that answered nothing, or answered "?" for everything, fails here
// too.
func TestARecordsOwnFieldsAreRedactedInEveryAnswer(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{
		Status:       runstore.StatusFailed,
		Outcome:      runstore.OutcomeNone,
		Reason:       "a reason the run wrote:" + testSpoofing,
		Branch:       "fugaro/the-run" + testSpoofing,
		PR:           &runstore.PRRef{Number: 9, URL: "https://github.com/acme/aurora/pull/9" + testSpoofing},
		ChangedFiles: &runstore.ChangedFiles{Paths: []string{"internal/x" + testSpoofing + ".go"}, Total: 1},
		PatchObject:  runstore.PatchName,
	}, map[string][]byte{
		runstore.PatchName: []byte("+a diff line the run wrote" + testSpoofing + "\n"),
	})

	var status taskStatus
	statusText := f.answer("get_task_status", `{"run_id":"`+testRunID+`"}`, &status)
	var res runResult
	resultText := f.answer("get_result", `{"run_id":"`+testRunID+`"}`, &res)
	var rows []taskStatus
	listText := f.answer("list_tasks", `{}`, &rows)

	for tool, text := range map[string]string{
		"get_task_status": statusText,
		"get_result":      resultText,
		"list_tasks":      listText,
	} {
		if strings.ContainsAny(text, "\x1b‮") {
			t.Errorf("%s answered a string the run wrote as it was written: %q", tool, text)
		}
	}

	if len(res.ChangedFiles) != 1 || len(rows) != 1 {
		t.Fatalf("get_result answered %v changed file(s) and list_tasks %d row(s); want the one run's own", res.ChangedFiles, len(rows))
	}
	for what, got := range map[string]string{
		"the record's reason":      status.Reason,
		"the record's branch":      status.Branch,
		"the record's PR URL":      status.PR.URL,
		"the run's changed path":   res.ChangedFiles[0],
		"a line of the run's diff": res.Patch,
		"the listing's own reason": rows[0].Reason,
		"the listing's own branch": rows[0].Branch,
		"the listing's own PR URL": rows[0].PR.URL,
	} {
		if strings.ContainsAny(got, "\x1b‮") {
			t.Errorf("%s reached an answer unredacted: %q", what, got)
		}
	}

	if !strings.Contains(status.Reason, "a reason the run wrote:") || !strings.Contains(res.Patch, "a diff line the run wrote") ||
		!strings.Contains(res.ChangedFiles[0], "internal/x") || !strings.Contains(rows[0].Branch, "fugaro/the-run") {
		t.Errorf("the answers lost the run's own text with the characters: %+v / %+v / %v", status, res, rows)
	}
}

// TestAnswerJSONRedactsTheWholeDocument: the redaction is on the document and
// not only on the fields this package happens to write through safetext today,
// which is the one thing answerJSON can promise whoever adds the next field
// (M-g). The value below is held by a field no tool redacts on its own, so a
// document pass that stopped happening is visible here and nowhere else.
func TestAnswerJSONRedactsTheWholeDocument(t *testing.T) {
	got, err := answerJSON(struct {
		Field string `json:"field"`
	}{Field: "a value a later field holds:" + testSpoofing})
	if err != nil {
		t.Fatalf("answerJSON: %v", err)
	}
	text, ok := got.(string)
	if !ok {
		t.Fatalf("answerJSON answered %T; the door writes a string verbatim and marshals anything else, so an answer that is not one is redacted by nothing", got)
	}
	if strings.ContainsAny(text, "\x1b‮") {
		t.Errorf("answerJSON answered %q; want the whole document made safe whatever its fields are", text)
	}
	if !strings.Contains(text, "a value a later field holds:") {
		t.Errorf("answerJSON answered %q; want the value it was given, with the characters redacted and not the text dropped", text)
	}
}

// TestTheReadReachRefusesAProjectItCannotFence: NewProjectRuns' own guards,
// which are the reach's and not a call's — a reach built over a project the
// tools cannot answer for is a wiring mistake, and every tool over it would
// answer as if the project had never run anything.
func TestTheReadReachRefusesAProjectItCannotFence(t *testing.T) {
	g := gcpfake.NewGCS(t)
	g.AddBucket(testRunsBucket, 1234, nil)
	bucket := g.Bucket(t, testRunsBucket).Bucket

	if _, err := NewProjectRuns(nil, []string{testSlug}); err == nil {
		t.Error("NewProjectRuns was built over no bucket: every read over it would fail at the first call rather than at the wiring")
	}
	for _, slugs := range [][]string{nil, {}} {
		if _, err := NewProjectRuns(bucket, slugs); err == nil {
			t.Errorf("NewProjectRuns admitted a project whose list names no repository (%v): every tool over it answers as if the project had never run anything, which reads as a working door with nothing behind it", slugs)
		}
	}

	// A name the runs tree is not keyed by is refused before any call: a slug
	// is a key into the bucket, and one that is not a slug is a key that names
	// something else.
	for _, slug := range []string{"", "Aurora-App", "aurora/app", "../aurora-app", "aurora_app", "aurora app", "."} {
		if _, err := NewProjectRuns(bucket, []string{testSlug, slug}); err == nil {
			t.Errorf("NewProjectRuns admitted %q, which is not a repository slug: the slug list is the fence, and a name that is not one is a wiring mistake rather than a key to read by", slug)
		}
	}

	// And what it does admit is deduplicated and sorted, so two projects with
	// the same repositories answer the same order.
	reach, err := NewProjectRuns(bucket, []string{testSiblingSlug, testSlug, testSlug})
	if err != nil {
		t.Fatalf("NewProjectRuns: %v", err)
	}
	if !slices.Equal(reach.slugs, []string{testSlug, testSiblingSlug}) {
		t.Errorf("NewProjectRuns kept the repositories %v; want them sorted and without repeats", reach.slugs)
	}
}

// TestReadToolArgumentsAreReadStrictly: an argument a tool does not take, and
// an argument of a type it cannot be, are refused rather than ignored. A client
// that misspells a filter and is answered with everything has been answered by
// a tool that did not read what it sent (the rule every other format in this
// repository is decoded by).
func TestReadToolArgumentsAreReadStrictly(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{Status: runstore.StatusRunning}, nil)

	var status taskStatus
	f.answer("get_task_status", `{"run_id":"`+testRunID+`"}`, &status)
	var rows []taskStatus
	f.answer("list_tasks", `{}`, &rows)

	for _, c := range []struct{ tool, args string }{
		// Another tool's own argument, and a name that is nobody's.
		{"get_task_status", `{"run_id":"` + testRunID + `","repo":"` + testSlug + `"}`},
		{"get_task_status", `{"run_id":"` + testRunID + `","limit":5}`},
		{"list_tasks", `{"repos":"` + testSlug + `"}`},
		{"list_tasks", `{"repo":"` + testSlug + `","count":5}`},
		// The right names, holding values of the wrong type.
		{"get_task_status", `{"run_id":12}`},
		{"list_tasks", `{"limit":"20"}`},
		{"list_tasks", `{"since":20300101}`},
		// And a call whose arguments are not an object at all.
		{"get_task_status", `"` + testRunID + `"`},
		{"list_tasks", `[1]`},
	} {
		if err := f.refusal(c.tool, c.args); err == nil || err.Error() == "" {
			t.Errorf("%s(%s) was refused with no reason", c.tool, c.args)
		}
	}
}

// TestListTasksAnswersTheCountItWasGiven: the count is the caller's to narrow
// and bounded at both ends. The fixture is deliberately larger than the default
// and than any single answer below, so the default, the narrowing and the
// ceiling are each a fact about the answer rather than about the fixture.
func TestListTasksAnswersTheCountItWasGiven(t *testing.T) {
	f := newRunsFixture(t)
	const seeded = 25
	ids := make([]string, 0, seeded)
	for i := range seeded {
		id := fmt.Sprintf("20300101-1200%02d-a%03d", i, i)
		ids = append(ids, id)
		f.seedRun(testSlug, id, &runstore.Record{Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady}, nil)
	}

	for _, c := range []struct {
		args string
		want int
	}{
		{`{}`, defaultListLimit},
		{`{"limit":1}`, 1},
		{`{"limit":5}`, 5},
		{`{"limit":` + fmt.Sprint(maxListLimit) + `}`, seeded},
		{`{"limit":` + fmt.Sprint(seeded) + `}`, seeded},
	} {
		var rows []taskStatus
		f.answer("list_tasks", c.args, &rows)
		if len(rows) != c.want {
			t.Errorf("list_tasks(%s) answered %d run(s); want %d", c.args, len(rows), c.want)
		}
		// Newest first, and the newest is the fixture's last minted run.
		if len(rows) > 0 && rows[0].RunID != ids[seeded-1] {
			t.Errorf("list_tasks(%s) answered %s first; want the newest run of the project", c.args, rows[0].RunID)
		}
	}

	for _, args := range []string{
		`{"limit":0}`,
		`{"limit":-1}`,
		fmt.Sprintf(`{"limit":%d}`, maxListLimit+1),
	} {
		if err := f.refusal("list_tasks", args); err == nil || err.Error() == "" {
			t.Errorf("list_tasks(%s) was refused with no reason", args)
		}
	}
}

// TestARunWithNoRecordYetIsRunning: a run that has written its task.json and
// no result.json is one that started and has not recorded anything yet, and
// that is what every tool answers about it — the question, the listing's own
// row and the outcome alike.
//
// The direction is the deliberate one: a missing record reports work rather
// than hiding it (the rule runview applies to the same case), and the only
// other answer available is a status this package would have to invent. The
// fields the record would carry are asserted empty beside it, so an answer
// that stopped at "running" and filled nothing is told from one that invented
// an outcome.
func TestARunWithNoRecordYetIsRunning(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, nil, nil)

	var status taskStatus
	f.answer("get_task_status", `{"run_id":"`+testRunID+`"}`, &status)
	if status.RunID != testRunID || status.Repo != testSlug || status.Status != string(runstore.StatusRunning) {
		t.Errorf("get_task_status answered %+v for a run whose result.json is not there yet; want it running", status)
	}
	if status.Outcome != "" || status.Reason != "" || status.FinishedAt != "" || status.Unreadable {
		t.Errorf("get_task_status answered %+v for a run with no record; want nothing the record would have carried", status)
	}

	var rows []taskStatus
	f.answer("list_tasks", `{}`, &rows)
	if len(rows) != 1 || rows[0].RunID != testRunID || rows[0].Status != string(runstore.StatusRunning) || rows[0].Unreadable {
		t.Errorf("list_tasks named %v for a project whose one run has no record yet; want its row, running", rows)
	}

	var res runResult
	f.answer("get_result", `{"run_id":"`+testRunID+`"}`, &res)
	if res.Status != string(runstore.StatusRunning) || res.Patch != "" || res.PatchTruncated {
		t.Errorf("get_result answered %+v for a run with neither record nor diff; want it running with nothing else", res)
	}
}

// TestResultOfARunThatUploadedNoDiffIsItsRecord: the diff is the run's own
// object under its own prefix, and a run killed before writeback never
// uploaded one — which is the case get_result is reached for. An object that
// is not there is read as a run with no diff rather than as a failure, so what
// a caller gets is the record's own account of it: what became of the run, the
// pull request it is on and the files it changed.
func TestResultOfARunThatUploadedNoDiffIsItsRecord(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{
		Status:       runstore.StatusFailed,
		Outcome:      runstore.OutcomeNone,
		Reason:       "the run stopped before it wrote a diff",
		PR:           &runstore.PRRef{Number: 12, URL: "https://github.com/acme/aurora/pull/12"},
		ChangedFiles: &runstore.ChangedFiles{Paths: []string{"internal/x.go"}, Total: 1},
	}, nil)

	var res runResult
	text := f.answer("get_result", `{"run_id":"`+testRunID+`"}`, &res)
	if res.Status != string(runstore.StatusFailed) || res.Reason != "the run stopped before it wrote a diff" ||
		res.PR == nil || res.PR.Number != 12 || !slices.Equal(res.ChangedFiles, []string{"internal/x.go"}) {
		t.Errorf("get_result answered %s for a run with no ab/patch.diff; want its own record", text)
	}
	if res.Patch != "" || res.PatchTruncated {
		t.Errorf("get_result answered a %d-byte diff (truncated=%v) for a run that uploaded none: an absent object is a run with no diff, not a failure and not an empty one", len(res.Patch), res.PatchTruncated)
	}
}
