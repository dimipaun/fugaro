package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

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
