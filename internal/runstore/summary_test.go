package runstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

const summaryTestID = "20261119-130000-abcd"

// summaryFixture is a record and a task with every field the summary's
// projection carries set to something no zero value would give it, so a
// member the projection drops reads as a difference rather than as two zeroes
// agreeing. The values are nonsense on purpose: what is under test is what
// travels, not what the fields mean.
func summaryFixture() (*Record, *task.Spec) {
	built := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	deadline := built.Add(2 * time.Hour)
	statusAt := built.Add(30 * time.Minute)
	rec := &Record{
		Version:       1,
		RunID:         summaryTestID,
		Repo:          "acme/app",
		Workflow:      "web",
		Execution:     "projects/p/locations/r/jobs/j/executions/e-1",
		Status:        StatusSucceeded,
		Stage:         "writeback",
		Outcome:       OutcomeReady,
		Reason:        "the run's own reason",
		FailureClass:  failure.Class("forbidden"),
		Halt:          &Halt{Reason: HaltOwnerDecision, Scope: "run", At: built, Detail: "the owner said so"},
		Branch:        "fugaro/20261119-130000-abcd",
		BaseBranch:    "main",
		HeadSHA:       strings.Repeat("a", 40),
		PushedHead:    strings.Repeat("b", 40),
		PR:            &PRRef{Number: 42, URL: "https://example.test/acme/app/pull/42", Desc: "the pull request's own description", StatusAt: &statusAt},
		DraftFallback: true,
		StartedAt:     built,
		Deadline:      &deadline,
		FollowUp:      &FollowUp{PR: 41, PreviousRun: "20261119-120000-9999", Comments: 2, Session: "fresh"},
		OAuthAccount:  "someone@example.com",
		Recipe:        &RecipeRecord{Name: "ship", Source: "repo", SHA256: strings.Repeat("c", 64)},
		ProjectLayer:  &ProjectLayerRecord{SHA256: strings.Repeat("d", 64), Generation: 7, Applied: true},
		ConfigSHA256:  strings.Repeat("e", 64),
		Image:         &ImageInfo{BuiltAt: &built, BakedCommit: strings.Repeat("f", 40)},
		ChangedFiles:  &ChangedFiles{Paths: []string{"a.go", "b.go", "c.go"}, Total: 9, Truncated: true},
		Rigor:         &RigorRecord{Mode: rigor.Pro, Level: rigor.Pro, From: rigor.FromRepo, At: "2026-11-19T13:00:00Z"},
		OwnerDecisions: &OwnerDecisions{
			Decisions: []OwnerDecision{{File: "a.go", Summary: "only the owner can settle this"}},
			Total:     1,
		},
		Artifacts: []ArtifactFile{{Name: "plan.md", Bytes: 120, SHA256: strings.Repeat("1", 64), ContentType: "text/markdown"}},
		CostUSD:   1.25,
		Cost: &Cost{
			ModelUSD: 1.0, ComputeUSD: 0.25, ComputeEstimated: true, TotalUSD: 1.25,
			ModelBasis: BasisAPIList, CacheReadTokens: 12, CacheWriteTokens: 34,
		},
		// What a row never shows and the projection must leave behind: the
		// stage timings, the review rounds, the verify records.
		Reviews: []ReviewSummary{{Round: 1, Verdict: "approve", Findings: 2}},
		Stages:  []StageTiming{{Name: "implement", StartedAt: built, DurationS: 1}},
	}
	spec := &task.Spec{
		Version:     1,
		RunID:       summaryTestID,
		Repo:        "acme/app",
		Ref:         "main",
		Workflow:    "web",
		Task:        "the run's prompt, which is most of what task.json costs and no row shows",
		Title:       "the run's title",
		OAuthToken:  "work",
		Recipe:      &task.Recipe{Name: "ship", Source: "repo", SHA256: strings.Repeat("c", 64), YAML: "steps:\n  - the recipe's own YAML, which no row shows\n"},
		Branch:      "fugaro/20261119-130000-abcd",
		PR:          42,
		PreviousRun: "20261119-120000-9999",
		FollowUpOf:  &task.FollowUpOf{Run: "20261119-110000-8888", Keys: []string{"k1"}, Importance: "high"},
		ReviewPRs:   []int{7},
		Debt:        true,
		Batch:       "nightly",
		RequestedBy: "someone@example.com",
	}
	return rec, spec
}

// summaryStore writes the fixture's summary through the store, as the runner
// does, and hands back the store, the two objects it projects and the bytes a
// reader gets: the document under test is the one this tree writes, never a
// hand-built one.
func summaryStore(t *testing.T) (*Store, *Record, *task.Spec, []byte) {
	t.Helper()
	ctx := context.Background()
	rec, spec := summaryFixture()
	s := Open(memblob.OpenBucket(nil), "acme-app", summaryTestID)
	at := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	if err := WriteSummary(ctx, s, NewSummary(rec, spec, at)); err != nil {
		t.Fatal(err)
	}
	raw, err := s.ReadFile(ctx, SummaryName)
	if err != nil {
		t.Fatal(err)
	}
	return s, rec, spec, raw
}

// TestSummaryIsTheRecordsProjection: the summary is the record's own
// projection, and reading it back gives the two objects it stands in for.
// Every member the summary carries is compared against the record's own JSON
// value for that member, so a block copied from the wrong place, a value of
// the wrong shape or a member dropped on the way back (Summary.AsRecord) is a
// difference here and a wrong cell in `runs ls` there.
//
// The one reduction is the recipe: the record carries the block and a row
// shows its name, so the summary carries the name, which assertRecipe keeps.
//
// Mutation caught: a projection that renames, retypes or forgets a member the
// row is built from. What it cannot see is a member the row reads and this
// fixture never sets: that is TestSummaryEqualsRecords in internal/cli, where
// one run is read both ways and the two rows are compared field for field.
func TestSummaryIsTheRecordsProjection(t *testing.T) {
	ctx := context.Background()
	s, rec, spec, raw := summaryStore(t)
	sum, err := s.ReadSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Version != SummaryVersion || sum.RunID != rec.RunID || sum.Slug != s.Slug() {
		t.Errorf("the summary read back names %d %s/%s, want version %d and the store's own run", sum.Version, sum.Slug, sum.RunID, SummaryVersion)
	}

	recDoc := jsonMembers(t, rec)
	for name, got := range jsonMembers(t, sum.Record) {
		want, ok := recDoc[name]
		if !ok {
			t.Errorf("the summary carries record.%s, which the record does not write: a row would read a field no run ever wrote", name)
			continue
		}
		if name == "recipe" {
			continue // the name, never the record's block: assertRecipe keeps it
		}
		if !sameJSON(got, want) {
			t.Errorf("record.%s = %s in the summary, %s in the record", name, got, want)
		}
		if back := jsonMembers(t, sum.AsRecord())[name]; !sameJSON(got, back) {
			t.Errorf("record.%s = %s after AsRecord, %s in the summary: reading it back does not give the object it stands in for", name, back, got)
		}
	}
	// The one reduction: the record carries the recipe's block and the summary
	// the name a row shows, under the record's own member name.
	if r := sum.AsRecord().Recipe; r == nil || r.Name != rec.Recipe.Name {
		t.Errorf("the record's recipe = %+v, want the name %q a row shows", r, rec.Recipe.Name)
	}

	for name, got := range jsonMembers(t, sum.Task) {
		if name == "recipe" {
			continue // the name, never a block: the check below keeps it
		}
		if back := jsonMembers(t, sum.Spec())[name]; !sameJSON(got, back) {
			t.Errorf("task.%s = %s after Spec(), %s in the summary", name, back, got)
		}
	}
	if r := sum.Spec().Recipe; r == nil || r.Name != spec.Recipe.Name {
		t.Errorf("the task's recipe = %+v, want the name %q a row shows", r, spec.Recipe.Name)
	}
	back := sum.Spec()
	if back.TitleLine() != spec.TitleLine() || back.IsFollowUp() != spec.IsFollowUp() ||
		!reflect.DeepEqual(back.ReviewPRs, spec.ReviewPRs) || !reflect.DeepEqual(back.FollowUpOf, spec.FollowUpOf) ||
		back.Debt != spec.Debt || back.Batch != spec.Batch || back.RequestedBy != spec.RequestedBy ||
		back.OAuthToken != spec.OAuthToken || back.Repo != spec.Repo || back.Workflow != spec.Workflow ||
		back.Branch != spec.Branch || back.PR != spec.PR || back.PreviousRun != spec.PreviousRun {
		t.Errorf("Spec() = %+v, want the fields of the task it stands in for", back)
	}

	// What the summary must NOT carry: the objects it stands in for are the
	// expensive ones, and a summary quoting the prompt or the recipe's YAML
	// would be as big as task.json and cost a listing the same.
	for _, absent := range []string{spec.Task, spec.Recipe.YAML, "reviews", "stages", "verify"} {
		if bytes.Contains(raw, []byte(absent)) {
			t.Errorf("the summary quotes %q, which is what reading the run's own objects costs and no row shows", absent)
		}
	}
}

// TestSummaryDecodeRefusesWhatItCannotVouchFor: the object is written by the
// run's own job account, which is not trusted, so a reader takes nothing in it
// on faith. Every one of these is what a caller falls back from -- the run's
// three objects -- and never a row: a summary is a shortcut, and a shortcut
// may not be the thing a listing breaks on.
func TestSummaryDecodeRefusesWhatItCannotVouchFor(t *testing.T) {
	_, rec, _, raw := summaryStore(t)
	good := string(raw)
	if _, err := DecodeSummary(raw, "acme-app", rec.RunID); err != nil {
		t.Fatalf("the summary this tree writes is refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		slug string
		id   string
		raw  string
	}{
		{"another version", "acme-app", rec.RunID, strings.Replace(good, `"version": 1`, `"version": 2`, 1)},
		{"another run asked for", "acme-app", "20261119-140000-beef", good},
		{"the summary of another run", "acme-app", rec.RunID, strings.Replace(good, rec.RunID, "20261119-140000-beef", 1)},
		{"another slug asked for", "other-repo", rec.RunID, good},
		{"the slug of another repository", "acme-app", rec.RunID, strings.Replace(good, `"slug": "acme-app"`, `"slug": "other-repo"`, 1)},
		{"no record", "acme-app", rec.RunID, `{"version":1,"run_id":"` + rec.RunID + `","slug":"acme-app","at":"2026-11-19T13:00:00Z"}`},
		{"no time", "acme-app", rec.RunID, strings.Replace(good, `"at": "2026-11-19T13:00:00Z",`, "", 1)},
		{"a member the reader does not know", "acme-app", rec.RunID, strings.Replace(good, `"version": 1,`, `"version": 1, "forged": true,`, 1)},
		{"two JSON values", "acme-app", rec.RunID, good + `{"version":1}`},
		{"a member of the wrong type", "acme-app", rec.RunID, strings.Replace(good, `"cost_usd": 1.25`, `"cost_usd": "1.25"`, 1)},
		{"no JSON at all", "acme-app", rec.RunID, ""},
	} {
		if _, err := DecodeSummary([]byte(tc.raw), tc.slug, tc.id); err == nil {
			t.Errorf("%s: decoded, want a refusal", tc.name)
		}
	}
}

// TestWriteSummaryStampsIdentity: the summary's own identity is this tree's
// and never the caller's. What the writer is handed cannot make a summary that
// speaks for another run -- the version, the run and the slug are stamped from
// the store -- and one that would stand in for nothing (no record, no time) is
// refused before the bucket is touched.
func TestWriteSummaryStampsIdentity(t *testing.T) {
	ctx := context.Background()
	s := Open(memblob.OpenBucket(nil), "acme-app", summaryTestID)
	rec, spec := summaryFixture()
	at := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)

	sum := NewSummary(rec, spec, at)
	sum.Version, sum.RunID, sum.Slug = 99, "20261119-140000-beef", "other-repo"
	if err := WriteSummary(ctx, s, sum); err != nil {
		t.Fatal(err)
	}
	back, err := s.ReadSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if back.Version != SummaryVersion || back.RunID != s.RunID() || back.Slug != s.Slug() {
		t.Errorf("the summary read back names %d %s/%s, want the store's own run", back.Version, back.Slug, back.RunID)
	}

	if err := WriteSummary(ctx, s, NewSummary(rec, spec, time.Time{})); err == nil {
		t.Error("a summary that names no time was written")
	}
	noRec := NewSummary(rec, spec, at)
	noRec.Record = nil
	if err := WriteSummary(ctx, s, noRec); err == nil {
		t.Error("a summary with no record was written: it stands in for nothing")
	}
	if err := WriteSummary(ctx, s, nil); err == nil {
		t.Error("a nil summary was written")
	}

	// The run's own summary is rewritten, not create-only like the
	// pull-request index: the row a listing shows is the run as it is now.
	later := at.Add(time.Hour)
	rec.Status, rec.Stage = StatusFailed, "finalize"
	if err := WriteSummary(ctx, s, NewSummary(rec, spec, later)); err != nil {
		t.Fatal(err)
	}
	back, err = s.ReadSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if back.Record.Status != StatusFailed || !back.At.Equal(later) {
		t.Errorf("the second write left %+v, want the run as it is now", back)
	}
}

// TestReadSummaryAbsent: a run written before summaries existed has none, and
// that is ErrNotFound -- the caller's cue to read the run's three objects --
// and not an error a listing reports.
func TestReadSummaryAbsent(t *testing.T) {
	s := Open(memblob.OpenBucket(nil), "acme-app", summaryTestID)
	sum, err := s.ReadSummary(context.Background())
	if !errors.Is(err, ErrNotFound) || sum != nil {
		t.Fatalf("ReadSummary = %v, %v, want ErrNotFound: every run before this object reads as absent", sum, err)
	}
}

// jsonMembers is a value's JSON members and their values, as the wire carries
// them: what the schema, the summary and the record all speak in.
func jsonMembers(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// sameJSON compares two JSON values by what they mean rather than by their
// spacing: both are decoded into the value model and compared.
func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// TestAChainRaiseLeavesTheSummaryProjectionUnchanged is the budget chain's
// raise (internal/cli/budget_chain.go, runChainRaise): it appends a ChainRaise
// to the root run's record and rewrites result.json, and writes no summary
// beside it -- that command is a launcher's, and a summary is the run's own
// job's object. What makes a stale summary harmless there is that a chain
// raise is not a projected member (SummaryRecord): the summary the run left
// still carries the record's own value for every field a row is built from, so
// a listing reads the raised run's row the same from the summary as from the
// rewritten record. The day the projection carries ChainRaises, a raised run's
// row would fall back, and this is the test that would fail.
func TestAChainRaiseLeavesTheSummaryProjectionUnchanged(t *testing.T) {
	rec, spec := summaryFixture()
	at := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	before := NewSummary(rec, spec, at)

	rec.ChainRaises = append(rec.ChainRaises, ChainRaise{
		By: "someone@example.com", At: at, Reason: "the chain spent its limit",
		Limits: []ChainRaiseLimit{{Dimension: USD, From: 5_000_000, To: 10_000_000}},
	})
	after := NewSummary(rec, spec, at)

	// The fixture's record does carry the raise, so the equality below is not
	// two projections agreeing on nothing.
	if members := jsonMembers(t, rec); members["chain_raises"] == nil {
		t.Fatalf("the record carries no chain_raises member, so this test would pass over a raise that moved nothing")
	}
	if !reflect.DeepEqual(before.Record, after.Record) {
		t.Errorf("a chain raise moved a projected member:\n  before %+v\n  after  %+v\nwant no difference: a row built from the summary must be the record's own",
			before.Record, after.Record)
	}
}
