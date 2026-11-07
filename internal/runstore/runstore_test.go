package runstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/blob/memblob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

var ctx = context.Background()

const runID = "20260926-221530-abcd"

func newStore(t *testing.T) *Store {
	t.Helper()
	b := memblob.OpenBucket(nil)
	t.Cleanup(func() { b.Close() })
	return Open(b, "acme-app", runID)
}

func TestTaskRoundTrip(t *testing.T) {
	s := newStore(t)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "do it"}
	if err := s.WriteTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadTask(ctx)
	if err != nil || got.Task != "do it" || got.Repo != "acme/app" {
		t.Fatalf("ReadTask = %+v, %v", got, err)
	}
}

func TestRecordRoundTripAndNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.ReadRecord(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadRecord on an empty store err = %v, want ErrNotFound", err)
	}
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady,
		Verify: []verify.Record{{N: 1, Kind: verify.KindTest, Passed: true}}, StartedAt: time.Now().UTC()}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || got.Status != StatusSucceeded || len(got.Verify) != 1 {
		t.Fatalf("ReadRecord = %+v, %v", got, err)
	}
}

func TestRecordRoundTripsOAuthAccount(t *testing.T) {
	s := newStore(t)
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady,
		StartedAt: time.Now().UTC(), OAuthAccount: "acme-personal"}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || got.OAuthAccount != "acme-personal" {
		t.Fatalf("ReadRecord oauth_account = %+v, %v", got, err)
	}

	// Empty is omitted, like Workflow's own omitempty convention.
	data, err := json.Marshal(&Record{Version: 1, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "oauth_account") {
		t.Fatalf("empty OAuthAccount encoded: %s", data)
	}

	// A record written before this field existed still parses, without one.
	old := `{"version":1,"run_id":"` + runID + `","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1,"started_at":"2026-09-26T22:15:30Z"}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadRecord(ctx)
	if err != nil || got.OAuthAccount != "" {
		t.Fatalf("old record = %+v, %v", got, err)
	}
}

// TestStageAccountsIsAdditive is D876's own compatibility claim (spec §11.5 of
// docs/accounts-add): the record gains a second, new field beside the scalar it
// already has, so a record written before it decodes unchanged, the format's
// version does not move, an empty map is omitted rather than written as an empty
// object, and oauth_account keeps the meaning it had — the account the run used
// last, never a stage's.
func TestStageAccountsIsAdditive(t *testing.T) {
	s := newStore(t)
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady,
		StartedAt: time.Now().UTC(),
		// The run used team-a last (the review stage's own override, and the
		// account the run ended on), while implement ran on the project
		// default: the two fields answer two different questions and both are
		// kept.
		OAuthAccount:  "team-a",
		StageAccounts: map[string]string{"implement": "team-b", "review": "team-a"},
	}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.StageAccounts, rec.StageAccounts) {
		t.Fatalf("ReadRecord stage_accounts = %+v, want %+v", got.StageAccounts, rec.StageAccounts)
	}
	if got.OAuthAccount != "team-a" {
		t.Errorf("oauth_account = %q, want the account the run used last (team-a): the per-stage answer is stage_accounts' and not this field's", got.OAuthAccount)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1: the field is additive", got.Version)
	}

	// Empty is omitted, like oauth_account's own convention: a run that
	// recorded no per-stage account writes the record it always wrote.
	data, err := json.Marshal(&Record{Version: 1, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "stage_accounts") {
		t.Fatalf("an empty StageAccounts encoded the field: %s", data)
	}

	// A record written before this field existed still parses, version and
	// all, and carries no map.
	old := `{"version":1,"run_id":"` + runID + `","status":"succeeded","stage":"writeback","outcome":"ready","oauth_account":"team-a","cost_usd":1,"started_at":"2026-09-26T22:15:30Z"}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadRecord(ctx)
	if err != nil {
		t.Fatalf("old record: %v", err)
	}
	if got.StageAccounts != nil {
		t.Errorf("old record stage_accounts = %+v, want none", got.StageAccounts)
	}
	if got.OAuthAccount != "team-a" || got.Version != 1 {
		t.Errorf("old record = %+v, want its own oauth_account and version 1", got)
	}
}

// TestRecordRoundTripsToleratedFindings: what a mode let through survives the
// round trip — the findings themselves, with the count, and not only the
// number reviews carries (docs/rigor-modes T3.4, D9, F19) — and a record
// written before the field existed still decodes, which is the compatibility
// claim the unchanged version rests on.
func TestRecordRoundTripsToleratedFindings(t *testing.T) {
	s := newStore(t)
	tolerated := NewTolerated([]ToleratedFinding{
		{Title: "the retry loop has no backoff", Mode: rigor.Startup, Likelihood: 1, Impact: 3, FixCost: 2, Class: rigor.Correctness},
		{Title: "a long comment restates the function", Mode: rigor.Startup, Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Cosmetic},
	})
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady,
		StartedAt: time.Now().UTC(), Tolerated: tolerated}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Tolerated, tolerated) {
		t.Fatalf("ReadRecord tolerated = %+v, want %+v", got.Tolerated, tolerated)
	}
	// The label is the score's, and the entry is bounded: the word a reader
	// sees is the one the axes derive, whatever a caller put beside them.
	if got.Tolerated.Findings[0].Label != rigor.Nit || got.Tolerated.Findings[1].Label != rigor.Low {
		t.Errorf("labels = %q, %q, want nit and low", got.Tolerated.Findings[0].Label, got.Tolerated.Findings[1].Label)
	}
	if got.Tolerated.Total != 2 || len(got.Tolerated.Findings) != 2 {
		t.Errorf("tolerated = %+v, want 2 findings and a total of 2", got.Tolerated)
	}

	// Empty is omitted, so a run that tolerated nothing writes the record it
	// always wrote rather than an empty block.
	data, err := json.Marshal(&Record{Version: 1, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tolerated") {
		t.Fatalf("a run that tolerated nothing encoded the field: %s", data)
	}

	// A record written before this field existed still parses, without one.
	old := `{"version":1,"run_id":"` + runID + `","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1,"reviews":[{"round":2,"verdict":"changes","findings":2}],"started_at":"2026-09-26T22:15:30Z"}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadRecord(ctx)
	if err != nil || got.Tolerated != nil {
		t.Fatalf("old record = %+v, %v, want no tolerated block", got, err)
	}
	if len(got.Reviews) != 1 || got.Reviews[0].Findings != 2 {
		t.Fatalf("old record reviews = %+v, want the round it always carried", got.Reviews)
	}
}

// TestNewToleratedBoundsWhatItStores: the record is a small object every
// listing reads, so the block is bounded — the list at MaxTolerated, each
// title at MaxToleratedTitle — and Total says how many the mode really
// tolerated, so a capped list is never presented as the whole one. An entry
// the record cannot read is not stored at all.
func TestNewToleratedBoundsWhatItStores(t *testing.T) {
	var many []ToleratedFinding
	for i := 0; i < MaxTolerated+5; i++ {
		many = append(many, ToleratedFinding{Title: "a finding", Mode: rigor.Pro, Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness})
	}
	tf := NewTolerated(many)
	if len(tf.Findings) != MaxTolerated || tf.Total != MaxTolerated+5 {
		t.Fatalf("NewTolerated = %d findings, total %d, want %d and %d", len(tf.Findings), tf.Total, MaxTolerated, MaxTolerated+5)
	}

	long := strings.Repeat("x", MaxToleratedTitle+50)
	tf = NewTolerated([]ToleratedFinding{{Title: long, Mode: rigor.Pro, Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness}})
	if got := len([]rune(tf.Findings[0].Title)); got != MaxToleratedTitle {
		t.Errorf("title length = %d, want %d", got, MaxToleratedTitle)
	}

	for _, tc := range []struct {
		name string
		f    ToleratedFinding
	}{
		{"no title", ToleratedFinding{Mode: rigor.Pro, Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness}},
		{"no mode", ToleratedFinding{Title: "t", Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness}},
		{"a mode outside the vocabulary", ToleratedFinding{Title: "t", Mode: rigor.Mode("paranoid"), Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness}},
		{"an unscored finding", ToleratedFinding{Title: "t", Mode: rigor.Pro, Class: rigor.Correctness}},
		{"a class outside the vocabulary", ToleratedFinding{Title: "t", Mode: rigor.Pro, Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Class("style")}},
	} {
		if tf := NewTolerated([]ToleratedFinding{tc.f}); tf != nil {
			t.Errorf("%s: NewTolerated = %+v, want nil", tc.name, tf)
		}
	}
	if tf := NewTolerated(nil); tf != nil {
		t.Errorf("NewTolerated(nil) = %+v, want nil", tf)
	}
}

// TestNewReviewScopeBoundsWhatItStores: the comparison's Outside is the
// review range's paths less the implement set's, and the block is bounded —
// each list at MaxReviewScopeFiles, the two lists together at
// MaxReviewScopeBytes, each path at MaxReviewScopePath — with
// Truncated saying when anything was cut, so a stored set is never presented
// as the whole one. A set the runner did not read stores no list at all,
// which is a different fact from a set that is empty.
//
// The blocks that record an Outside carry a WhyNot, the shape a run writes:
// Applicable is true only where the two sets were read from one revision, so
// an applicable comparison has nothing outside (ReviewScope's doc). The
// schema states that shape but does not enforce it — it carries no
// constraint tying applicable to outside — so this test pins the blocks the
// runner builds, not the schema.
func TestNewReviewScopeBoundsWhatItStores(t *testing.T) {
	sc := NewReviewScope(false, ReviewScopeNoNarrowerRange, []string{"a.txt", "b.txt"}, []string{"a.txt", "b.txt", "c.txt"})
	if sc.Applicable || sc.WhyNot != ReviewScopeNoNarrowerRange || !slices.Equal(sc.Outside, []string{"c.txt"}) {
		t.Errorf("NewReviewScope = %+v, want c.txt outside a range kept wide", sc)
	}
	if sc.Truncated {
		t.Error("a comparison of three paths is recorded as truncated")
	}
	sc = NewReviewScope(false, ReviewScopeFollowUp, nil, nil)
	if sc == nil || sc.Applicable || sc.WhyNot != ReviewScopeFollowUp {
		t.Fatalf("NewReviewScope on no sets = %+v, want the reason alone", sc)
	}
	if sc.ImplementFiles != nil || sc.ReviewFiles != nil || sc.Outside != nil {
		t.Errorf("NewReviewScope on no sets stores %v/%v/%v, want no lists", sc.ImplementFiles, sc.ReviewFiles, sc.Outside)
	}
	data, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"implement_files"`)) || bytes.Contains(data, []byte(`"review_files"`)) || bytes.Contains(data, []byte(`"outside"`)) {
		t.Errorf("a comparison with no sets marshals lists: %s", data)
	}

	var implement, review []string
	for i := 0; i < MaxReviewScopeFiles+5; i++ {
		p := fmt.Sprintf("p%03d.txt", i)
		implement = append(implement, p)
		review = append(review, p)
	}
	sc = NewReviewScope(true, "", implement, review)
	if len(sc.ImplementFiles) != MaxReviewScopeFiles || len(sc.ReviewFiles) != MaxReviewScopeFiles || !sc.Truncated {
		t.Errorf("NewReviewScope = %d/%d paths, truncated %v, want %d and true",
			len(sc.ImplementFiles), len(sc.ReviewFiles), sc.Truncated, MaxReviewScopeFiles)
	}
	if len(sc.Outside) != 0 {
		t.Errorf("outside = %v, want nothing outside an equal pair", sc.Outside)
	}

	// The two lists share one byte budget, so a directory rename's worth of
	// paths stops there rather than putting the block past MaxRecordBytes.
	wide := strings.Repeat("d", 200)
	var many []string
	for i := range MaxReviewScopeFiles {
		many = append(many, fmt.Sprintf("%03d/%s", i, wide))
	}
	sc = NewReviewScope(true, "", many, many)
	if !sc.Truncated {
		t.Error("a set past the byte budget is not recorded as truncated")
	}
	var stored int
	for _, p := range sc.ImplementFiles {
		stored += len(p)
	}
	for _, p := range sc.ReviewFiles {
		stored += len(p)
	}
	if stored > MaxReviewScopeBytes {
		t.Errorf("stored %d bytes of path, over the bound of %d", stored, MaxReviewScopeBytes)
	}
	if len(sc.ImplementFiles) == len(many) {
		t.Errorf("stored all %d paths, want the byte bound to cut the list", len(many))
	}

	long := strings.Repeat("x", MaxReviewScopePath+50)
	sc = NewReviewScope(false, ReviewScopeNoNarrowerRange, nil, []string{long})
	if got := len([]rune(sc.Outside[0])); got != MaxReviewScopePath {
		t.Errorf("outside path length = %d, want %d", got, MaxReviewScopePath)
	}
	if !sc.Truncated {
		t.Error("a clipped path is not recorded as truncated")
	}

	// An empty path is not a path, and a set that came to nothing stores no
	// list rather than an empty one.
	if sc := NewReviewScope(true, "", []string{""}, []string{""}); sc.ImplementFiles != nil || sc.ReviewFiles != nil {
		t.Errorf("NewReviewScope on an empty path stores %v/%v, want no lists", sc.ImplementFiles, sc.ReviewFiles)
	}
}

// TestNewCheckpointBoundsWhatItStores: the three fields a model answers the
// checkpoint prompt with are a model's output in a record every listing
// reads, so each is clipped to MaxCheckpointText; the trigger and the money
// figure are the runner's own and pass through. A checkpoint a governance
// halt writes before the checkpoint stage exists carries the trigger alone,
// and that marshals as the trigger alone — an absent field, not an empty
// string, which is how a reader tells "the agent said nothing" from "nothing
// asked it".
func TestNewCheckpointBoundsWhatItStores(t *testing.T) {
	long := strings.Repeat("w", MaxCheckpointText+50)
	cp := NewCheckpoint(Checkpoint{
		Trigger:          string(HaltStageBudget),
		Done:             long,
		WhyOver:          long,
		Next:             long,
		HardInstructions: long,
		EstimateUSD:      1.25,
	})
	for name, got := range map[string]string{
		"done":              cp.Done,
		"why_over":          cp.WhyOver,
		"next":              cp.Next,
		"hard_instructions": cp.HardInstructions,
	} {
		if n := len([]rune(got)); n != MaxCheckpointText {
			t.Errorf("%s = %d runes, want %d", name, n, MaxCheckpointText)
		}
	}
	if cp.Trigger != string(HaltStageBudget) || cp.EstimateUSD != 1.25 {
		t.Errorf("NewCheckpoint = %+v, want the trigger and the figure as written", cp)
	}

	data, err := json.Marshal(NewCheckpoint(Checkpoint{Trigger: string(HaltFanout)}))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"trigger":"fanout"}` {
		t.Errorf("a checkpoint of the trigger alone marshals %s", data)
	}
}

func TestCancelMarker(t *testing.T) {
	s := newStore(t)
	if ok, err := s.CancelRequested(ctx); err != nil || ok {
		t.Fatalf("CancelRequested before = %v, %v", ok, err)
	}
	if err := s.RequestCancel(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CancelRequested(ctx); err != nil || !ok {
		t.Fatalf("CancelRequested after = %v, %v", ok, err)
	}
}

func TestPutFileUnderPrefix(t *testing.T) {
	b := memblob.OpenBucket(nil)
	defer b.Close()
	s := Open(b, "acme-app", runID)
	if err := s.PutFile(ctx, "transcripts/implement-1.jsonl", []byte("{}\n"), "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}
	key := "runs/acme-app/" + runID + "/transcripts/implement-1.jsonl"
	if ok, _ := b.Exists(ctx, key); !ok || s.Prefix() != "runs/acme-app/"+runID+"/" {
		t.Fatalf("object %s missing or prefix %q wrong", key, s.Prefix())
	}
}

// ctxAwareBucket is an in-memory bucket driver whose Delete refuses to work on
// a context that is already done, the way a real backend's does — the request
// carries the context, and GCS does not delete an object for a cancelled one.
// gocloud's memblob and fileblob both ignore the context in Delete, which is
// why the store's other tests cannot tell a detached cleanup from one that
// runs on the caller's cancelled context and fails; this one can.
type ctxAwareBucket struct {
	driver.Bucket // nil: only the methods below are reached
	mu            sync.Mutex
	data          map[string][]byte
}

func (d *ctxAwareBucket) ErrorCode(error) gcerrors.ErrorCode { return gcerrors.Unknown }
func (d *ctxAwareBucket) As(any) bool                        { return false }
func (d *ctxAwareBucket) ErrorAs(error, any) bool            { return false }
func (d *ctxAwareBucket) Close() error                       { return nil }

func (d *ctxAwareBucket) NewTypedWriter(ctx context.Context, key, _ string, _ *driver.WriterOptions) (driver.Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &ctxAwareWriter{d: d, key: key}, nil
}

func (d *ctxAwareBucket) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.data[key]; !ok {
		return fmt.Errorf("no object %s", key)
	}
	delete(d.data, key)
	return nil
}

// has reads the driver's own map, so an assertion about what is left does not
// depend on a reader this driver does not implement.
func (d *ctxAwareBucket) has(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.data[key]
	return ok
}

type ctxAwareWriter struct {
	d   *ctxAwareBucket
	key string
	buf bytes.Buffer
}

func (w *ctxAwareWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *ctxAwareWriter) Close() error {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	w.d.data[w.key] = w.buf.Bytes()
	return nil
}

// TestDeleteFileDeletesWhenItsContextIsAlreadyCancelled: the upload step cleans
// up after a failed attempt with this delete, on the run's own context — the
// one that has expired, or was cancelled, is usually the reason there is
// anything to clean up at all. The delete is therefore detached from its
// caller's context (deleteDetached), and this is the test that says so: the
// context is cancelled before the call, and both objects still have to be
// gone, with no error to report.
//
// Mutation (deleteDetached drops the detach and deletes on ctx): the driver
// refuses, DeleteFile returns context.Canceled, and both objects are still at
// their keys, so this test fails on the error and on the objects.
func TestDeleteFileDeletesWhenItsContextIsAlreadyCancelled(t *testing.T) {
	d := &ctxAwareBucket{data: map[string][]byte{}}
	s := Open(blob.NewBucket(d), "acme-app", runID)
	names := []string{"artifacts/a.png", "artifacts/b.png"}
	for _, name := range names {
		if err := s.PutFile(ctx, name, []byte("sizzle"), "image/png"); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, name := range names {
		if err := s.DeleteFile(cancelled, name); err != nil {
			t.Fatalf("DeleteFile(%s) under a cancelled context = %v", name, err)
		}
		if key := s.prefix + name; d.has(key) {
			t.Fatalf("%s is still in the bucket: the cleanup delete did not survive the context", key)
		}
	}
}

func TestParseRef(t *testing.T) {
	if slug, id, err := ParseRef("acme-app/" + runID); err != nil || slug != "acme-app" || id != runID {
		t.Fatalf("ParseRef = %q %q %v", slug, id, err)
	}
	// Every slug task.Slug produces for a valid repo must parse back.
	for _, repo := range []string{"acme/my_service", "Acme/Server", "acme/app.v2", "org/sub/repo-x"} {
		spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: "main", Task: "t"}
		if err := spec.Validate(); err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		want, err := task.Slug("github", repo)
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		if slug, id, err := ParseRef(want + "/" + runID); err != nil || slug != want || id != runID {
			t.Errorf("ParseRef(Slug(%q)) = %q %q %v", repo, slug, id, err)
		}
	}
	for _, bad := range []string{"", "acme-app", "a/b/c", "acme-app/not-a-run", "./" + runID, "../" + runID, "acme.app/" + runID} {
		if _, _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) succeeded", bad)
		}
	}
}

// TestFinalizeReserve: the record carries the run's finalize reserve, so
// cancel can floor its grace without a checkout; an older record has none.
func TestFinalizeReserve(t *testing.T) {
	if d, ok := (&Record{FinalizeReserveS: 90}).FinalizeReserve(); !ok || d != 90*time.Second {
		t.Fatalf("FinalizeReserve = %v, %v", d, ok)
	}
	if d, ok := (&Record{}).FinalizeReserve(); ok || d != 0 {
		t.Fatalf("FinalizeReserve of a record without one = %v, %v", d, ok)
	}
}

func TestRecordFollowUpRoundTrip(t *testing.T) {
	s := newStore(t)
	fu := &FollowUp{
		PR: 12, PreviousRun: "20260925-000000-0a1b", StartSHA: "0123456789abcdef0123456789abcdef01234567",
		Session: "fresh", SessionNote: "no saved session", Comments: 3,
		Authors: map[string]int{"Ada": 2, "Linus": 1}, UntrustedAuthors: []string{"mallory"},
		Omitted: map[string]int{"self": 1, "untrusted_author": 1},
	}
	rec := &Record{Version: 1, RunID: runID, Status: StatusRunning, Outcome: OutcomeNone, StartedAt: time.Now().UTC(), FollowUp: fu}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || !reflect.DeepEqual(got.FollowUp, fu) {
		t.Fatalf("ReadRecord follow_up = %+v, %v", got.FollowUp, err)
	}
	data, _ := json.Marshal(got)
	if !strings.Contains(string(data), `"follow_up":{"pr":12,"previous_run":"20260925-000000-0a1b"`) {
		t.Fatalf("encoded record = %s", data)
	}

	// A record written before follow-ups existed still parses, without one.
	old := `{"version":1,"run_id":"` + runID + `","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1,"started_at":"2026-09-26T22:15:30Z","pr":{"number":3,"url":"u"}}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadRecord(ctx)
	if err != nil || got.FollowUp != nil || got.PushedHead != "" || got.PR.Number != 3 {
		t.Fatalf("old record = %+v, %v", got, err)
	}
	// And a first run's record encodes neither field.
	data, _ = json.Marshal(&Record{Version: 1, RunID: runID})
	if strings.Contains(string(data), "follow_up") || strings.Contains(string(data), "pushed_head") {
		t.Fatalf("empty fields encoded: %s", data)
	}
}

// TestStageTimingCallsRoundTrip pins the record's own contract for the stage
// counters (docs/task-governance constraint G5): absent is not zero. A stage
// nobody counted -- an oauth run's calls, absent because no gateway reported
// them and no result event supplies a figure, and every record written before
// repeats existed -- keeps the field absent, while a stage counted at zero
// carries 0.
func TestStageTimingCallsRoundTrip(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	calls, agents, repeats, zero := 12, 2, 4, 0
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady, StartedAt: at,
		Stages: []StageTiming{
			{Name: "implement", StartedAt: at, DurationS: 61, Calls: &calls, Agents: &agents, Repeats: &repeats},
			{Name: "review", StartedAt: at, DurationS: 30},
			{Name: "fix", StartedAt: at, DurationS: 9, Calls: &zero, Agents: &zero, Repeats: &zero},
		}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Stages) != 3 {
		t.Fatalf("ReadRecord stages = %+v", got.Stages)
	}
	if got.Stages[0].Calls == nil || *got.Stages[0].Calls != 12 || got.Stages[0].Agents == nil || *got.Stages[0].Agents != 2 ||
		got.Stages[0].Repeats == nil || *got.Stages[0].Repeats != 4 {
		t.Fatalf("stage with counts = %+v", got.Stages[0])
	}
	if got.Stages[1].Calls != nil || got.Stages[1].Agents != nil || got.Stages[1].Repeats != nil {
		t.Fatalf("stage with no count gained one: %+v", got.Stages[1])
	}
	if got.Stages[2].Calls == nil || *got.Stages[2].Calls != 0 || got.Stages[2].Agents == nil || *got.Stages[2].Agents != 0 ||
		got.Stages[2].Repeats == nil || *got.Stages[2].Repeats != 0 {
		t.Fatalf("stage with zero counts = %+v", got.Stages[2])
	}

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name":"implement","started_at":"2026-10-26T10:00:00Z","duration_s":61,"calls":12,"agents":2,"repeats":4`) {
		t.Fatalf("counted stage encoded = %s", data)
	}
	if !strings.Contains(string(data), `"name":"review","started_at":"2026-10-26T10:00:00Z","duration_s":30}`) {
		t.Fatalf("uncounted stage encoded = %s", data)
	}
	if !strings.Contains(string(data), `"name":"fix","started_at":"2026-10-26T10:00:00Z","duration_s":9,"calls":0,"agents":0,"repeats":0}`) {
		t.Fatalf("zero-count stage encoded = %s", data)
	}
}

func TestRecordPushedHead(t *testing.T) {
	s := newStore(t)
	head := "0123456789abcdef0123456789abcdef01234567"
	if err := s.WriteRecord(ctx, &Record{Version: 1, RunID: runID, Status: StatusRunning, PushedHead: head}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || got.PushedHead != head {
		t.Fatalf("PushedHead = %q, %v", got.PushedHead, err)
	}
}

// TestReviewSummaryRecordsModelEffortAndHead: a review entry carries what
// reviewed it — the model the stage was pinned to and the reasoning effort it
// ran at — beside the commit it read, so a reader of the record can tell which
// model reviewed which head (CLI 0.16.0, docs/c39-review-identity). A round
// that recorded neither stays without them, so both are omitted rather than
// written empty, and a record written before the two fields existed still
// decodes.
func TestReviewSummaryRecordsModelEffortAndHead(t *testing.T) {
	s := newStore(t)
	head := "0123456789abcdef0123456789abcdef01234567"
	if err := s.WriteRecord(ctx, &Record{Version: 1, RunID: runID, Status: StatusSucceeded, StartedAt: time.Now().UTC(),
		Reviews: []ReviewSummary{
			{Round: 1, Tier: TierSenior, Verdict: "ship", Model: "claude-opus-5-5", Effort: "high", Head: head},
			{Round: 2, Tier: TierAdversarial, Verdict: "changes", Findings: 1, Model: "claude-sonnet-5"},
			{Round: 3, Tier: TierFirst, Verdict: "ship"},
		}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Reviews) != 3 {
		t.Fatalf("ReadRecord reviews = %+v, want 3", got.Reviews)
	}
	first := got.Reviews[0]
	if first.Model != "claude-opus-5-5" || first.Effort != "high" || first.Head != head {
		t.Fatalf("senior review = %+v, want model %q, effort %q and head %q", first, "claude-opus-5-5", "high", head)
	}
	if second := got.Reviews[1]; second.Model != "claude-sonnet-5" || second.Effort != "" {
		t.Fatalf("adversarial review = %+v, want model %q and no effort: the level named none", second, "claude-sonnet-5")
	}
	if third := got.Reviews[2]; third.Model != "" || third.Effort != "" || third.Head != "" {
		t.Fatalf("first-line review = %+v, want no model, effort or head", third)
	}

	// Both are omitted when absent, so a round that recorded neither is
	// byte-identical to one written before the fields existed.
	data, err := json.Marshal(&Record{Version: 1, RunID: runID,
		Reviews: []ReviewSummary{{Round: 1, Tier: TierSenior, Verdict: "ship"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"model":`) || strings.Contains(string(data), `"effort":`) {
		t.Fatalf("an empty model or effort encoded: %s", data)
	}

	// A record written before the two fields existed still parses, with its
	// reviews intact.
	old := `{"version":1,"run_id":"` + runID + `","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1,"started_at":"2026-09-26T22:15:30Z","reviews":[{"round":1,"tier":"senior","verdict":"ship","findings":0,"head":"` + head + `"}]}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadRecord(ctx)
	if err != nil || len(got.Reviews) != 1 {
		t.Fatalf("old record reviews = %+v, %v", got.Reviews, err)
	}
	if r := got.Reviews[0]; r.Model != "" || r.Effort != "" || r.Head != head {
		t.Fatalf("old record review = %+v, want no model and no effort, and the head it carried", r)
	}
}

// TestReviewSummaryHeadRoundTrip: the commit a review read survives the trip
// beside its verdict — the levels of one round can approve different heads,
// which is what the PR body reports and what the record has to carry
// (docs/review-levels-ab-jobmax L4.1, D727) — while a review that recorded
// none stays without one, so the field is omitted rather than written empty,
// and a record written before it existed still decodes.
func TestReviewSummaryHeadRoundTrip(t *testing.T) {
	s := newStore(t)
	head := "0123456789abcdef0123456789abcdef01234567"
	if err := s.WriteRecord(ctx, &Record{Version: 1, RunID: runID, Status: StatusSucceeded, StartedAt: time.Now().UTC(),
		Reviews: []ReviewSummary{
			{Round: 2, Tier: TierSenior, Verdict: "ship", Head: head},
			{Round: 3, Tier: TierAdversarial, Verdict: "changes", Findings: 1},
		}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Reviews) != 2 {
		t.Fatalf("ReadRecord reviews = %+v, want 2", got.Reviews)
	}
	if got.Reviews[0].Tier != TierSenior || got.Reviews[0].Head != head {
		t.Fatalf("senior review = %+v, want tier %q and head %q", got.Reviews[0], TierSenior, head)
	}
	if got.Reviews[1].Tier != TierAdversarial || got.Reviews[1].Head != "" {
		t.Fatalf("adversarial review = %+v, want tier %q and no head", got.Reviews[1], TierAdversarial)
	}

	// Empty is omitted, so a review that read no commit is byte-identical to
	// one written before the field existed.
	data, err := json.Marshal(&Record{Version: 1, RunID: runID,
		Reviews: []ReviewSummary{{Round: 1, Tier: TierSenior, Verdict: "ship"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"head":`) {
		t.Fatalf("empty Head encoded: %s", data)
	}

	// A record written before this field existed still parses, with its
	// reviews intact.
	old := `{"version":1,"run_id":"` + runID + `","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1,"started_at":"2026-09-26T22:15:30Z","reviews":[{"round":1,"tier":"senior","verdict":"ship","findings":0}]}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadRecord(ctx)
	if err != nil || len(got.Reviews) != 1 || got.Reviews[0].Head != "" || got.Reviews[0].Tier != TierSenior {
		t.Fatalf("old record reviews = %+v, %v", got.Reviews, err)
	}
}

func TestSibling(t *testing.T) {
	b := memblob.OpenBucket(nil)
	defer b.Close()
	s := Open(b, "acme-app", runID)
	const other = "20260925-000000-0a1b"
	sib := s.Sibling(other)
	if sib.RunID() != other || sib.Slug() != "acme-app" || sib.Prefix() != "runs/acme-app/"+other+"/" {
		t.Fatalf("Sibling = %q %q %q", sib.RunID(), sib.Slug(), sib.Prefix())
	}
	// It shares the bucket: what one writes, the other's opener reads.
	if err := sib.WriteRecord(ctx, &Record{Version: 1, RunID: other, Status: StatusSucceeded}); err != nil {
		t.Fatal(err)
	}
	if got, err := Open(b, "acme-app", other).ReadRecord(ctx); err != nil || got.RunID != other {
		t.Fatalf("sibling's record = %+v, %v", got, err)
	}
	if s.RunID() != runID {
		t.Fatalf("Sibling changed its receiver: %q", s.RunID())
	}
}

func TestRecordHaltRoundTrip(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	rec := &Record{Version: 1, RunID: runID, Status: StatusHalted, Outcome: OutcomeDraft, StartedAt: at,
		Halt: &Halt{Reason: HaltRunCap, Scope: "run", At: at, Detail: "run spent $5.00 of $5.00"}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || got.Status != StatusHalted || got.Halt == nil || *got.Halt != *rec.Halt {
		t.Fatalf("ReadRecord = %+v, %v", got, err)
	}
	// A record from before halts existed still parses, with no halt.
	old := `{"version":1,"run_id":"` + runID + `","status":"failed","stage":"fix","outcome":"draft","cost_usd":1,"started_at":"2026-09-30T10:00:00Z"}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	if got, err = s.ReadRecord(ctx); err != nil || got.Halt != nil || got.Status != StatusFailed {
		t.Fatalf("old record = %+v, %v", got, err)
	}
	data, _ := json.Marshal(&Record{Version: 1})
	if strings.Contains(string(data), `"halt"`) {
		t.Fatalf("an unset halt is written: %s", data)
	}
}

// TestRecordABRoundTrip: the pair block survives the record's round trip
// unchanged, and the patch object's own two facts with it (A2.1 uploads one
// for both arms, capped, with the truncation recorded rather than hidden).
func TestRecordABRoundTrip(t *testing.T) {
	s := newStore(t)
	// The one spelling of the patch's leaf, which the runner that uploads it
	// and the reader that scores the pair both use.
	patchKey := PatchKey(s.Prefix())
	if want := "runs/acme-app/" + runID + "/ab/patch.diff"; patchKey != want {
		t.Fatalf("PatchKey = %q, want %q", patchKey, want)
	}
	ab := &task.AB{PairID: runID, Arm: task.ABArmChallenger, Publish: task.ABPublishNone,
		Challenger: "invented-challenger-1", ChallengerSource: task.ABChallengerOwner}
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeNone,
		StartedAt: time.Now().UTC(), AB: ab, PatchObject: patchKey, PatchTruncated: true}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.AB == nil || *got.AB != *ab {
		t.Fatalf("ReadRecord ab = %+v, want %+v", got.AB, ab)
	}
	if got.PatchObject != rec.PatchObject || !got.PatchTruncated {
		t.Fatalf("ReadRecord patch = %q, truncated %v, want %q, true", got.PatchObject, got.PatchTruncated, rec.PatchObject)
	}
}

// TestRecordABOmittedWhenEmpty: a run that was not sampled carries no pair
// block and no patch object, so its result.json is byte-for-byte what the
// release before these fields wrote — and a record written before them still
// decodes, which is the compatibility claim the unchanged version rests on.
func TestRecordABOmittedWhenEmpty(t *testing.T) {
	data, err := json.Marshal(&Record{Version: 1, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ab", "patch_object", "patch_truncated"} {
		if strings.Contains(string(data), `"`+key+`"`) {
			t.Fatalf("an unset %s is written: %s", key, data)
		}
	}

	s := newStore(t)
	old := `{"version":1,"run_id":"` + runID + `","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1,"started_at":"2026-09-26T22:15:30Z"}`
	if err := s.PutFile(ctx, "result.json", []byte(old), "application/json"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil || got.AB != nil || got.PatchObject != "" || got.PatchTruncated {
		t.Fatalf("old record = %+v, %v", got, err)
	}
}

// TestStageNamesTheCredentialThatServedIt is the record's own contract for
// StageTiming.ServedBy (plan AK3 of docs/api-key-review, F18, D1415): the new
// field names the credential a stage ran on, the run's own included, which is
// the case Credential excludes. Both shapes round-trip -- a stage the run's
// own api-key credential served carries served_by and no credential, and a
// stage an account served carries served_by beside credential -- and both
// survive the record's own encoding.
func TestStageNamesTheCredentialThatServedIt(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	rec := &Record{Version: 1, RunID: runID, Status: StatusSucceeded, Outcome: OutcomeReady, StartedAt: at,
		Stages: []StageTiming{
			{Name: "implement", StartedAt: at, DurationS: 61, Model: "claude-sonnet-5-5", ModelUSD: 0.04,
				ServedBy: CredentialAPIKey},
			{Name: "review", StartedAt: at, DurationS: 42, Model: "claude-opus-5", ModelUSD: 0.42,
				ModelNotionalUSD: 0.42, Credential: CredentialOAuth, ServedBy: CredentialOAuth},
		}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Stages) != 2 {
		t.Fatalf("ReadRecord stages = %+v", got.Stages)
	}
	if own := got.Stages[0]; own.ServedBy != CredentialAPIKey || own.Credential != "" {
		t.Errorf("the run's own credential's stage = %+v, want served_by %q and no credential", own, CredentialAPIKey)
	}
	if account := got.Stages[1]; account.ServedBy != CredentialOAuth || account.Credential != CredentialOAuth {
		t.Errorf("the account's stage = %+v, want served_by beside credential, both %q", account, CredentialOAuth)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"served_by":"api-key"`) {
		t.Errorf("the stage the run's own credential served encodes no served_by: %s", data)
	}
	if !strings.Contains(string(data), `"credential":"oauth","served_by":"oauth"`) {
		t.Errorf("the account's stage does not carry served_by beside credential: %s", data)
	}
}

// TestStageCredentialKeepsItsMeaning is AK3's other direction, and the one
// D1415's recommendation exists for: the new field is additive, so credential
// keeps its stated meaning -- the credential that served the stage when it was
// NOT the run's own -- and its single value. A stage the run's own key served
// carries served_by and no credential at all (which is the mutation this test
// catches: writing "api-key" into credential instead), a record written before
// the field existed still parses, and the published schema still closes
// credential's set at ["oauth"] with served_by beside it.
func TestStageCredentialKeepsItsMeaning(t *testing.T) {
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	data, err := json.Marshal(StageTiming{Name: "implement", StartedAt: at, DurationS: 61, ModelUSD: 0.04,
		ServedBy: CredentialAPIKey})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"credential"`) {
		t.Errorf("a stage the run's own credential served carries credential: %s", data)
	}
	if !strings.Contains(string(data), `"served_by":"api-key"`) {
		t.Errorf("a stage the run's own credential served does not name it: %s", data)
	}
	// Additive: a stage written before the field existed parses, and its
	// ServedBy is the absent empty value rather than a wrong one.
	var old StageTiming
	if err := json.Unmarshal([]byte(`{"name":"review","started_at":"2026-11-05T10:00:00Z","duration_s":42,`+
		`"credential":"oauth","model_usd":0.42,"model_notional_usd":0.42}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.ServedBy != "" || old.Credential != CredentialOAuth {
		t.Errorf("a record written before the field = %+v, want credential %q alone", old, CredentialOAuth)
	}

	// The published contract, in the schema's own words: credential is still
	// the one value, and the new field sits beside it, optional.
	props, required := resultStageProperties(t)
	cred, ok := props["credential"].(map[string]any)
	if !ok {
		t.Fatalf("result.schema.json's credential property = %v", props["credential"])
	}
	if got := schemaEnum(cred["enum"]); !slices.Equal(got, []string{CredentialOAuth}) {
		t.Errorf("result.schema.json's credential enum = %v, want %v alone: the new field is additive, not a widening",
			got, CredentialOAuth)
	}
	if desc, _ := cred["description"].(string); !strings.Contains(desc, "when it was not the run's own") {
		t.Errorf("result.schema.json's credential description no longer states the field's meaning: %q", desc)
	}
	served, ok := props["served_by"].(map[string]any)
	if !ok {
		t.Fatalf("result.schema.json has no served_by beside credential: a stage carrying one is schema-invalid (%v)",
			schemaNames(props))
	}
	if got := schemaEnum(served["enum"]); !slices.Equal(got, []string{CredentialOAuth, CredentialAPIKey}) {
		t.Errorf("result.schema.json's served_by enum = %v, want [%s %s]", got, CredentialOAuth, CredentialAPIKey)
	}
	if desc, _ := served["description"].(string); !strings.Contains(desc, "the run's own") {
		t.Errorf("result.schema.json's served_by description does not say it includes the run's own credential: %q", desc)
	}
	for _, name := range required {
		if name == "served_by" || name == "credential" {
			t.Errorf("result.schema.json requires %v: both are optional, and absent is what every record written before them carries", name)
		}
	}
}

// resultStageProperties is schemas/result.schema.json's stages item: its
// properties and what it requires of a stage. The record's contract is that
// file, and a test that pins a field's meaning reads it here rather than
// restating it.
func resultStageProperties(t *testing.T) (map[string]any, []any) {
	t.Helper()
	data, err := os.ReadFile("../../schemas/result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			Stages struct {
				Items struct {
					Properties map[string]any `json:"properties"`
					Required   []any          `json:"required"`
				} `json:"items"`
			} `json:"stages"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	props := doc.Properties.Stages.Items.Properties
	if len(props) == 0 {
		t.Fatal("result.schema.json's stages item has no properties: this test's anchor moved")
	}
	return props, doc.Properties.Stages.Items.Required
}

// schemaEnum is a schema property's enum as strings, so a comparison reads as
// the values rather than as the JSON.
func schemaEnum(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

// schemaNames is a schema object's property names, sorted, for a failure that
// names what the contract does carry.
func schemaNames(props map[string]any) []string {
	out := make([]string, 0, len(props))
	for name := range props {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// TestCredentialForAuth pins the whole mapping the runner's stage-recording
// path writes ServedBy from (AK3, D1415) -- all five auths the config allows
// (config.AgentAuths), not only the two the record's field is ever named
// for. A case that fell through to "api-key" would put a metered key on a
// vertex run's service-account stages, a cascade run's service-answered ones
// and a review class run's, and an "oauth" case returning "" would leave
// every stage of a subscription run unnamed while the record still read as
// complete: both are quiet in every run that only ever exercises one auth.
func TestCredentialForAuth(t *testing.T) {
	for _, tt := range []struct {
		auth string
		want string
	}{
		{"oauth", CredentialOAuth},
		{"api-key", CredentialAPIKey},
		// The auths whose credential is not one class: a vertex run's service
		// account, and a cascade run's calls, which the credentials service
		// answers one at a time. A review class run takes the cascade path
		// too (runner.modelAuth), so it is this case and not a third one.
		{"vertex", ""},
		{"cascade", ""},
		// Not an auth any config allows, and the answer is still not a class:
		// the mapping names a credential only where the auth names one.
		{"", ""},
	} {
		if got := CredentialForAuth(tt.auth); got != tt.want {
			t.Errorf("CredentialForAuth(%q) = %q, want %q", tt.auth, got, tt.want)
		}
	}
}
