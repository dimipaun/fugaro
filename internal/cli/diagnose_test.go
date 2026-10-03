package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/verify"
)

func TestDiagnose(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	cost := runstore.NewCost(3.2, 0, runstore.BasisSubscription)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Reason: "tests failing on the final commit", HeadSHA: "abc1234", CostUSD: 3.2, Cost: &cost,
		PR:      &runstore.PRRef{Number: 7, URL: "https://bitbucket.org/acme/app/pull-requests/7"},
		Reviews: []runstore.ReviewSummary{{Round: 1, Verdict: "changes", Findings: 3}, {Round: 2, Verdict: "changes", Findings: 2}},
		Verify: []verify.Record{{N: 1, Kind: verify.KindTest, HeadSHA: "abc1234", CleanTree: true, Passed: false,
			Tests: 3, Failures: 1, Failed: []string{"pkg.Suite.beta"}, Flaky: []string{"pkg.Suite.alpha"}}},
		Stages: []runstore.StageTiming{{Name: "implement"}, {Name: "review"}, {Name: "fix"}, {Name: "review"}}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	review := `{"type":"result","subtype":"success","result":"see findings","structured_output":{"verdict":"changes","findings":[{"severity":"high","file":"a.go","summary":"nil deref"},{"severity":"low","file":"b.go","summary":"typo"}]}}` + "\n"
	fix := `{"type":"result","subtype":"success","result":"I fixed the parser but tests still fail"}` + "\n"
	_ = s.PutFile(ctx, "transcripts/review-2.jsonl", []byte(review), "application/x-ndjson")
	_ = s.PutFile(ctx, "transcripts/fix-1.jsonl", []byte(fix), "application/x-ndjson")
	f.logging.AddJSONLines(exec, []byte(`{"time":"`+logTime(time.Second)+`","severity":"INFO","message":"stage started","stage":"fix"}`+"\n"))

	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if d.Row.Status != "failed" || d.Row.PRURL != rec.PR.URL {
		t.Fatalf("row = %+v", d.Row)
	}
	if strings.Join(d.Failed, ",") != "pkg.Suite.beta" || strings.Join(d.Flaky, ",") != "pkg.Suite.alpha" {
		t.Fatalf("failed %v, flaky %v", d.Failed, d.Flaky)
	}
	if len(d.Findings) != 2 || d.Findings[0].Summary != "nil deref" {
		t.Fatalf("findings = %+v", d.Findings)
	}
	if !strings.Contains(d.AgentMessage, "fixed the parser") || len(d.LogTail) != 1 || !strings.Contains(d.LogTail[0], "stage started") {
		t.Fatalf("message %q, tail %v", d.AgentMessage, d.LogTail)
	}
	if d.ReportPath != "runs/"+appSlug+"/"+id+"/report.md" {
		t.Fatalf("report path = %q", d.ReportPath)
	}
	human, _, err := execute(t, "diagnose", id)
	for _, want := range []string{"Reason:", "Findings", "Cost:", rec.PR.URL, "notional"} {
		if err != nil || !strings.Contains(human, want) {
			t.Fatalf("human diagnose lacks %q (%v):\n%s", want, err, human)
		}
	}
}

func TestDiagnoseWithoutTranscripts(t *testing.T) {
	f := newCloudFixture(t)
	seedRun(t, f, "20260927-110000-bbbb", "", "", true)
	out, _, err := execute(t, "diagnose", "--json", "20260927-110000-bbbb")
	var d Diagnosis
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.Row.Status != "pending" || len(d.Findings) != 0 || d.AgentMessage != "" {
		t.Fatalf("diagnose of a run with nothing yet: %s, %v", out, err)
	}
}

// diagnose redacts what it prints from the run's objects too, and clips the
// agent's message.
func TestDiagnoseRedactsAndClips(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusFailed, Stage: "implement", Reason: "push failed: " + secret,
		Stages: []runstore.StageTiming{{Name: "implement"}}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	long := secret + " " + strings.Repeat("x", 10000)
	_ = s.PutFile(ctx, "transcripts/implement-1.jsonl", []byte(`{"type":"result","subtype":"success","result":"`+long+`"}`+"\n"), "application/x-ndjson")
	for _, args := range [][]string{{"diagnose"}, {"diagnose", "--json"}} {
		out, _, err := execute(t, append(args, id)...)
		if err != nil || strings.Contains(out, secret) || !strings.Contains(out, "[REDACTED]") {
			t.Fatalf("%v = %s, %v", args, out, err)
		}
	}
	out, _, _ := execute(t, "diagnose", "--json", id)
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil || len(strings.TrimSuffix(d.AgentMessage, " …")) > agentMessageBytes || !strings.HasSuffix(d.AgentMessage, " …") || !strings.HasPrefix(d.AgentMessage, "[REDACTED]") {
		t.Fatalf("agent message %d bytes: %.40q, %v", len(d.AgentMessage), d.AgentMessage, err)
	}
}

// A failed log read is the one GCP error diagnose tolerates: it warns on
// stderr, exits 0 and still prints everything the bucket holds.
func TestDiagnoseToleratesLogFailure(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusFailed, Stage: "implement", Reason: "agent gave up",
		Stages: []runstore.StageTiming{{Name: "implement"}}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	_ = s.PutFile(ctx, "transcripts/implement-1.jsonl", []byte(`{"type":"result","subtype":"success","result":"could not finish"}`+"\n"), "application/x-ndjson")

	// Point the local config's Logging endpoint at a server that refuses
	// every call.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"logging denied","status":"PERMISSION_DENIED"}}`))
	}))
	defer down.Close()
	path := os.Getenv("FUGARO_CONFIG")
	cfg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg = []byte(strings.Replace(string(cfg), "logging: "+f.logging.URL+"/", "logging: "+down.URL+"/", 1))
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := execute(t, "diagnose", id)
	if err != nil || ExitCode(err) != ExitOK {
		t.Fatalf("diagnose with Logging down: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "warning: reading the logs") {
		t.Fatalf("no warning on stderr: %q", errOut)
	}
	for _, want := range []string{"Status:   failed", "Reason:   agent gave up", "could not finish", "Report:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diagnosis lacks %q:\n%s", want, out)
		}
	}
	js, _, err := execute(t, "diagnose", "--json", id)
	var d Diagnosis
	if err != nil || json.Unmarshal([]byte(js), &d) != nil || d.Row.Status != "failed" || d.AgentMessage != "could not finish" || len(d.LogTail) != 0 {
		t.Fatalf("diagnose --json with Logging down: %s, %v", js, err)
	}
}

func TestDiagnoseUsesPriceOverride(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-120000-cccc"
	seedFinishedRun(t, f, id)
	compute := func() float64 {
		out, _, err := execute(t, "diagnose", "--json", id)
		if err != nil {
			t.Fatal(err)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return d.Row.Cost.ComputeUSD
	}
	list := compute()
	f.appendConfig(t, priceOverride)
	wantOverrideRatio(t, list, compute())
}

func TestDiagnoseFollowUp(t *testing.T) {
	f := newCloudFixture(t)
	root, id := runIDAt(1, "090000", "aaaa"), runIDAt(0, "000100", "bbbb")
	exec := seedSpec(t, f, followUpSpec(id, root, root, 7), true)
	f.run.SetState(exec, backend.StateSucceeded)
	rec := prRecord(id, exec, 7, 1)
	rec.Branch = "fugaro/" + root
	rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: root, Session: "resumed", SessionNote: "the branch moved by 1 commit",
		Comments: 3, Authors: map[string]int{"alice": 2, "bob": 1}, UntrustedAuthors: []string{"mallory"},
		Omitted: map[string]int{"untrusted_author": 1, "resolved": 2}}
	writeRecord(t, f, id, rec)

	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	fu := d.FollowUp
	if fu == nil || fu.PR != 7 || fu.PreviousRun != root || fu.Session != "resumed" || fu.Comments != 3 || fu.Authors["alice"] != 2 ||
		strings.Join(fu.UntrustedAuthors, ",") != "mallory" || fu.Omitted["resolved"] != 2 || !d.Row.FollowUp {
		t.Fatalf("follow_up = %+v", fu)
	}
	if d.CommentsPath != "runs/"+appSlug+"/"+id+"/comments.json" {
		t.Fatalf("comments_path = %q", d.CommentsPath)
	}
	human, _, err := execute(t, "diagnose", id)
	want := "follow-up of PR #7 after " + root + "; session resumed (the branch moved by 1 commit); 3 comments from alice (2), bob (1) (1 untrusted: mallory)"
	if err != nil || !strings.Contains(human, want+"\n") {
		t.Fatalf("human diagnose lacks %q (%v):\n%s", want, err, human)
	}

	// Before the runner has written its block, the task says what it follows.
	waiting := runIDAt(0, "000200", "cccc")
	seedSpec(t, f, followUpSpec(waiting, root, id, 7), false)
	out, _, err = execute(t, "diagnose", "--json", waiting)
	d = Diagnosis{}
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.FollowUp == nil || d.FollowUp.PR != 7 || d.FollowUp.PreviousRun != id ||
		d.CommentsPath != "runs/"+appSlug+"/"+waiting+"/comments.json" {
		t.Fatalf("task-only follow-up: %s, %v", out, err)
	}
	human, _, err = execute(t, "diagnose", waiting)
	if err != nil || !strings.Contains(human, "follow-up of PR #7 after "+id+"\n") {
		t.Fatalf("task-only human diagnose (%v):\n%s", err, human)
	}

	// A first run has neither.
	first := runIDAt(0, "000300", "dddd")
	seedSpec(t, f, firstRunSpec(first), false)
	out, _, err = execute(t, "diagnose", "--json", first)
	d = Diagnosis{}
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.FollowUp != nil || strings.Contains(out, "comments_path") {
		t.Fatalf("first run: %s, %v", out, err)
	}
	if human, _, _ := execute(t, "diagnose", first); strings.Contains(human, "follow-up") {
		t.Fatalf("first run human diagnose:\n%s", human)
	}
}

// diagnose redacts the follow-up block: its note and the author names come
// from the run.
func TestDiagnoseFollowUpRedacts(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	f := newCloudFixture(t)
	root, id := runIDAt(1, "090000", "aaaa"), runIDAt(0, "000100", "bbbb")
	seedSpec(t, f, followUpSpec(id, root, root, 7), false)
	rec := prRecord(id, "", 7, 1)
	rec.Branch = "fugaro/" + root
	rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: root, Session: "fresh", SessionNote: "no session: " + secret,
		Comments: 1, Authors: map[string]int{"alice " + secret: 1}, UntrustedAuthors: []string{secret}, Omitted: map[string]int{secret: 1}}
	writeRecord(t, f, id, rec)
	for _, args := range [][]string{{"diagnose"}, {"diagnose", "--json"}} {
		out, _, err := execute(t, append(args, id)...)
		if err != nil || strings.Contains(out, secret) || !strings.Contains(out, "[REDACTED]") {
			t.Fatalf("%v = %s, %v", args, out, err)
		}
	}
}

// followUpLine counts every untrusted author, beyond the 20 names the
// record keeps.
func TestFollowUpLineCountsAllUntrusted(t *testing.T) {
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("u%02d", i)
	}
	line := followUpLine(&runstore.FollowUp{PR: 7, PreviousRun: "20260101-000000-0000", Session: "fresh", UntrustedAuthors: names, UntrustedAuthorCount: 25})
	if !strings.Contains(line, "(25 untrusted: u00") || !strings.Contains(line, "and 5 more") {
		t.Fatalf("line = %q", line)
	}
	// A record from before the count was kept counts the names.
	line = followUpLine(&runstore.FollowUp{PR: 7, PreviousRun: "20260101-000000-0000", Session: "fresh", UntrustedAuthors: []string{"x"}})
	if !strings.Contains(line, "(1 untrusted: x)") {
		t.Fatalf("line = %q", line)
	}
}

func TestDiagnoseHaltBlock(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	f.run.SetState(exec, backend.StateSucceeded)
	at := time.Date(2026, 9, 27, 10, 5, 0, 0, time.UTC)
	rec := prRecord(id, exec, 7, 2)
	rec.Status = runstore.StatusHalted
	rec.Halt = &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run", At: at, Detail: "run spent $5.00 of $5.00"}
	rec.Reason = "halted: run_cap: run spent $5.00 of $5.00"
	writeRecord(t, f, id, rec)

	human, _, err := execute(t, "diagnose", id)
	want := "Halted:   run_cap (run) at 2026-09-27T10:05:00Z: run spent $5.00 of $5.00"
	if err != nil || !strings.Contains(human, want) || !strings.Contains(human, "Status:   halted") {
		t.Fatalf("diagnose lacks %q (%v):\n%s", want, err, human)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	var d Diagnosis
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.Halt == nil || *d.Halt != *rec.Halt {
		t.Fatalf("json halt = %+v (%v):\n%s", d.Halt, err, out)
	}
	if !strings.Contains(out, `"halt": {`) {
		t.Fatalf("JSON has no halt block:\n%s", out)
	}
	// A run that was not halted prints no Halted line.
	other := "20260927-110000-bbbb"
	e2 := seedRun(t, f, other, "", "", true)
	f.run.SetState(e2, backend.StateSucceeded)
	writeRecord(t, f, other, prRecord(other, e2, 8, 1))
	if human, _, err := execute(t, "diagnose", other); err != nil || strings.Contains(human, "Halted:") {
		t.Fatalf("a failed run prints a halt (%v):\n%s", err, human)
	}
}

func staleRecord(id, exec string, statusAt time.Time) *runstore.Record {
	rec := prRecord(id, exec, 7, 2)
	rec.Status, rec.Outcome, rec.Stage = runstore.StatusRunning, "", "review"
	rec.PR.StatusAt = &statusAt
	return rec
}

func TestDiagnoseStaleDraft(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	f.run.SetState(exec, backend.StateFailed) // the execution died; the record never finalized
	rec := staleRecord(id, exec, time.Now().Add(-3*time.Hour))
	rec.DraftFallback = true
	writeRecord(t, f, id, rec)

	human, _, err := execute(t, "diagnose", id)
	want := "Draft:    draft PR #7 last updated 3h ago; the run may have crashed"
	if err != nil || !strings.Contains(human, want) {
		t.Fatalf("diagnose lacks %q (%v):\n%s", want, err, human)
	}
	for _, w := range []string{"fugaro run --pr 7", "marked [DRAFT] in its title", "PR:       https://github.com/acme/app/pull/7 (draft, stale)"} {
		if !strings.Contains(human, w) {
			t.Errorf("diagnose lacks %q:\n%s", w, human)
		}
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	var d Diagnosis
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || !d.Row.StaleDraft || !d.Row.DraftFallback || d.Row.PRStatusAt == nil || !strings.Contains(d.DraftNote, "may have crashed") {
		t.Fatalf("json = %+v (%v):\n%s", d, err, out)
	}
	// A fresh status is no stale draft, and a halted run's draft is just a draft.
	live := "20260927-110000-bbbb"
	e2 := seedRun(t, f, live, "", "", true)
	f.run.SetState(e2, backend.StateRunning)
	writeRecord(t, f, live, staleRecord(live, e2, time.Now().Add(-time.Minute)))
	if human, _, err := execute(t, "diagnose", live); err != nil || strings.Contains(human, "Draft:") || strings.Contains(human, "stale") {
		t.Fatalf("a live run reads stale (%v):\n%s", err, human)
	}
}

func TestDiagnoseDraftTextIsSanitised(t *testing.T) {
	r := runview.Row{PR: 7, PRURL: "https://h/pr/7\x1b[2J", StaleDraft: true}
	var b strings.Builder
	d := &Diagnosis{Row: r, DraftNote: draftNote(r, time.Now())}
	d.Row.Run, d.Row.Status = "a/b", "infra_error"
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "\x1b") {
		t.Fatalf("control sequence printed: %q", b.String())
	}
}
