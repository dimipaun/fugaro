package watch

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestWatchShowsAPausedRun pins the view of a run that suspended itself
// (contract-pause, R15; model-credentials-routing PR7): it is listed as every
// finished run is, its own record's word for it is PAUSED, and its pause's two
// facts — the instant the resume is due at and the run that resumed it — are on
// the row in the text frame and in --json, where the two new members are
// `finished[]`'s and nothing is retyped.
//
// The two facts come from two places and neither is the paused run's record
// (PausesOf): the instant is the credentials service's node for the pause, and
// the resumer is another run's task naming this run under `resumes`. So the
// test builds the view the way the command does — the row from the runs bucket
// scan, then the facts the scan read, then the pause — rather than by setting
// the row's fields by hand, which would pass even if nothing joined the two.
func TestWatchShowsAPausedRun(t *testing.T) {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	resumeAt := time.Date(2026, 11, 24, 9, 25, 0, 0, time.UTC)
	const (
		paused  = "20261124-081500-4c1a"
		resumer = "20261124-092000-7d3e"
		live    = "20261124-085900-11ff"
	)

	v := View{Now: now, Day: "2026-11-24", Mode: "enforce", Conn: Connection{Kind: ConnLive},
		Repos: []RepoBlock{{Slug: "acme-app", Name: "acme/app", Runs: []RunRow{
			{Run: live, Slug: "acme-app", Title: "a run still going", Stage: "implement", Round: "1",
				Verify: "-", Models: "sonnet", Auth: "api", HasAge: true, Age: time.Minute},
		}}}}
	v = MergeFinished(v, Config{}, []FinishedRun{{
		Run: paused, Slug: "acme-app", Title: "the run that ran out of chain",
		Status: string(runstore.StatusPaused), FinishedAt: now.Add(-10 * time.Minute),
	}}, now)
	facts := Facts{RunKeyOf("acme-app", resumer): {Resumes: paused}}
	v = MergeFacts(v, facts)
	v = MergePaused(v, PausesOf(map[RunKey]time.Time{RunKeyOf("acme-app", paused): resumeAt}, facts))

	var row RunRow
	for _, b := range v.Repos {
		for _, f := range b.Finished {
			if f.Run == paused {
				row = f
			}
		}
	}
	if row.Run == "" {
		t.Fatal("a paused run is not listed among the finished runs")
	}
	if !row.Paused {
		t.Errorf("the row is not marked paused: %+v", row)
	}
	if row.ResumeAt != resumeAt.UnixMilli() {
		t.Errorf("ResumeAt = %d, want %d", row.ResumeAt, resumeAt.UnixMilli())
	}
	if row.ResumedBy != resumer {
		t.Errorf("ResumedBy = %q, want %q", row.ResumedBy, resumer)
	}

	// The text frame: the interactive screen's own words, in the block that
	// draws it.
	if f := frame(v, 110, 24); !strings.Contains(f, "PAUSED") ||
		!strings.Contains(f, "expected resume 2026-11-24 09:25Z") || !strings.Contains(f, "resumed by 7d3e") {
		t.Errorf("the frame does not say the pause, its resume and its resumer:\n%s", f)
	}
	plain := new(strings.Builder)
	if err := RenderPlain(plain, "acme", v, ReadyRowsOf(v), PlainOptions{}); err != nil {
		t.Fatalf("RenderPlain: %v", err)
	}
	if !strings.Contains(plain.String(), "PAUSED") ||
		!strings.Contains(plain.String(), "2026-11-24 09:25Z by 7d3e") {
		t.Errorf("the plain frame does not say the pause, its resume and its resumer:\n%s", plain)
	}

	// --json: the two new members, on finished[] alone, with the instant as
	// this document spells every instant and the resumer as the run's own id.
	doc := BuildJSON("acme", v)
	if len(doc.Finished) != 1 {
		t.Fatalf("finished[] has %d rows, want 1", len(doc.Finished))
	}
	got := doc.Finished[0]
	if got.Status != string(runstore.StatusPaused) {
		t.Errorf("finished[0].status = %q, want %q", got.Status, runstore.StatusPaused)
	}
	if got.ResumeAt != "2026-11-24T09:25:00Z" {
		t.Errorf("finished[0].resume_at = %q, want 2026-11-24T09:25:00Z", got.ResumeAt)
	}
	if got.ResumedBy != resumer {
		t.Errorf("finished[0].resumed_by = %q, want %q", got.ResumedBy, resumer)
	}
	// The live row beside it is a run that is going, and a run that is going is
	// not paused: the two members are `JSONFinished`'s alone, so nothing about a
	// live run changes shape for this (the asymmetry `credential` and
	// `waiting_until` carry on the other side).
	liveKeys := jsonKeys(t, doc.Runs)
	for _, key := range []string{"resume_at", "resumed_by"} {
		if liveKeys[key] {
			t.Errorf("runs[] carries %s, which is finished[]'s", key)
		}
	}
	finishedKeys := jsonKeys(t, doc.Finished)
	for _, key := range []string{"resume_at", "resumed_by"} {
		if !finishedKeys[key] {
			t.Errorf("finished[] does not carry %s", key)
		}
	}
}

// jsonKeys is the set of keys one row marshals to, so a test can say which
// members a shape carries without reading the struct.
func jsonKeys(t *testing.T, rows any) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := map[string]bool{}
	for _, item := range items {
		for key := range item {
			out[key] = true
		}
	}
	return out
}

// TestPausedRunWithoutANodeOrAResumer pins the degradation: a paused row the
// reader has no pause for still reads PAUSED — that is the run's own record,
// the one thing it always has — and says nothing it does not know. The instant
// and the resumer are absent from --json rather than empty, so a reader can
// tell "not recorded" from a value.
func TestPausedRunWithoutANodeOrAResumer(t *testing.T) {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	const paused = "20261124-081500-4c1a"
	v := View{Now: now, Day: "2026-11-24", Mode: "enforce", Conn: Connection{Kind: ConnLive}}
	v = MergeFinished(v, Config{}, []FinishedRun{{
		Run: paused, Slug: "acme-app", Title: "the run that ran out of chain",
		Status: string(runstore.StatusPaused), FinishedAt: now.Add(-10 * time.Minute),
	}}, now)
	v = MergePaused(v, PausesOf(nil, nil))

	doc := BuildJSON("acme", v)
	if len(doc.Finished) != 1 || doc.Finished[0].Status != string(runstore.StatusPaused) {
		t.Fatalf("finished[] = %+v, want one paused row", doc.Finished)
	}
	if doc.Finished[0].ResumeAt != "" || doc.Finished[0].ResumedBy != "" {
		t.Errorf("a pause nothing recorded invented a fact: %+v", doc.Finished[0])
	}
	if keys := jsonKeys(t, doc.Finished); keys["resume_at"] || keys["resumed_by"] {
		t.Errorf("an absent fact is on the wire: %v", keys)
	}
	if f := frame(v, 110, 24); !strings.Contains(f, "PAUSED") || strings.Contains(f, "expected resume") {
		t.Errorf("the frame does not read as the run's own word alone:\n%s", f)
	}
}

// TestPausesOfNamesTheResumerOfAPause pins the join the contract's own rule
// needs: the resumer is not on the pause's node but in the chain — the run
// whose task names the paused run under `resumes` — and only a run the reader
// has a pause for is given one, so a resumer naming a run no node covers does
// not conjure a pause.
func TestPausesOfNamesTheResumerOfAPause(t *testing.T) {
	const (
		paused   = "20261124-081500-4c1a"
		resumer  = "20261124-092000-7d3e"
		stranger = "20261124-100000-9999"
	)
	at := time.Date(2026, 11, 24, 9, 25, 0, 0, time.UTC)
	key := RunKeyOf("acme-app", paused)
	facts := Facts{
		RunKeyOf("acme-app", resumer):  {Resumes: paused},
		RunKeyOf("acme-app", stranger): {Resumes: "20260101-000000-0000"},
	}
	p := PausesOf(map[RunKey]time.Time{key: at}, facts)
	if len(p) != 1 {
		t.Fatalf("PausesOf made %d pauses, want 1: %v", len(p), p)
	}
	if p[key].ResumedBy != resumer || !p[key].ResumeAt.Equal(at) {
		t.Errorf("PausesOf[%v] = %+v, want %s by %s", key, p[key], at, resumer)
	}
}
