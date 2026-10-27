package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runner"
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

// TestDiagnosePrintsStagesAndTolerated: the run's per-stage record (the model
// calls, sub-agent calls and model spend of each stage, governance T1.1-T1.3)
// and the findings the run's rigor mode let through (T3.4) are both on the
// record and neither reached a JSON the console reads. diagnose --json carries
// both, in the record's own order, with every string redacted like the rest of
// the document.
func TestDiagnosePrintsStagesAndTolerated(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	f := newCloudFixture(t)
	const id = "20260927-100000-abcf"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	calls, agents, none := 12, 3, 0
	cost := runstore.NewCost(2.5, 0, runstore.BasisAPIList)
	cost.ModelBy = map[string]float64{"unattributed": 0.25, "model-a": 2.25}
	score := rigor.Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Cosmetic}
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady,
		CostUSD: 2.5, Cost: &cost,
		Stages: []runstore.StageTiming{
			{Name: "implement " + secret, Calls: &calls, Agents: &agents, ModelUSD: 2.0, Model: "model-a " + secret, ModelNotionalUSD: 0.5},
			{Name: "review", Agents: &none, ModelUSD: 0.25},
		},
		Tolerated: runstore.NewTolerated([]runstore.ToleratedFinding{
			{Title: "a comment left behind: " + secret, Mode: rigor.Pro,
				Likelihood: score.Likelihood, Impact: score.Impact, FixCost: score.FixCost, Class: score.Class},
		})}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("the document carries the secret:\n%s", out)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(d.Stages) != 2 || d.Stages[0].Name != "implement [REDACTED]" || d.Stages[1].Name != "review" {
		t.Fatalf("stages = %+v", d.Stages)
	}
	if d.Stages[0].Model != "model-a [REDACTED]" || d.Stages[0].ModelUSD != 2.0 || d.Stages[0].ModelNotionalUSD != 0.5 {
		t.Fatalf("implement's spend = %+v", d.Stages[0])
	}
	// A stage nobody counted and a stage counted at zero are different facts,
	// so the counts stay pointers: review has no call count at all (no
	// gateway) and an agent count of zero.
	if d.Stages[0].Calls == nil || *d.Stages[0].Calls != calls || d.Stages[0].Agents == nil || *d.Stages[0].Agents != agents {
		t.Fatalf("implement's counts = %+v", d.Stages[0])
	}
	if d.Stages[1].Calls != nil || d.Stages[1].Agents == nil || *d.Stages[1].Agents != 0 {
		t.Fatalf("review's counts = %+v: calls %v, agents %v", d.Stages[1], d.Stages[1].Calls, d.Stages[1].Agents)
	}
	// The invariant the record's own comment states, read off the printed
	// document: the stages are the whole model spend but for what settled
	// after its stage's wait ran out.
	var stages float64
	for _, st := range d.Stages {
		stages += st.ModelUSD
	}
	if got := stages + d.ModelBy["unattributed"]; got != d.Row.Cost.ModelUSD {
		t.Fatalf("sum(stages[].model_usd) %v + model_by[unattributed] %v = %v, want row.cost.model_usd %v", stages, d.ModelBy["unattributed"], got, d.Row.Cost.ModelUSD)
	}
	if d.Tolerated == nil || d.Tolerated.Total != 1 || len(d.Tolerated.Findings) != 1 {
		t.Fatalf("tolerated = %+v", d.Tolerated)
	}
	tf := d.Tolerated.Findings[0]
	if tf.Title != "a comment left behind: [REDACTED]" || tf.Mode != rigor.Pro || tf.Label != score.Label() {
		t.Fatalf("tolerated finding = %+v", tf)
	}
}

// TestDiagnoseRedactsEveryStringOfAToleratedFinding: the block's mode, label
// and class are enums on the write path, but they are strings on the wire and
// a hand-edited record can put anything in them, so every string of the block
// goes through the document's own redaction and not only the title.
func TestDiagnoseRedactsEveryStringOfAToleratedFinding(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	f := newCloudFixture(t)
	const id = "20260927-100000-abd3"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback",
		Tolerated: &runstore.ToleratedFindings{
			Findings: []runstore.ToleratedFinding{{
				Title: secret, Mode: rigor.Mode(secret), Label: rigor.Label(secret), Class: rigor.Class(secret),
				Likelihood: 1, Impact: 1, FixCost: 1}},
			Total: 1}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("the document carries the secret:\n%s", out)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if d.Tolerated == nil || len(d.Tolerated.Findings) != 1 {
		t.Fatalf("tolerated = %+v", d.Tolerated)
	}
	tf := d.Tolerated.Findings[0]
	if tf.Title != "[REDACTED]" || tf.Mode != "[REDACTED]" || tf.Label != "[REDACTED]" || tf.Class != "[REDACTED]" {
		t.Fatalf("tolerated finding = %+v", tf)
	}
}

// TestDiagnoseOmitsStagesAndToleratedWhenEmpty: both keys are omitempty, so a
// run whose record carries neither -- a run that never recorded a stage, and
// one with no record at all -- prints a document without them rather than an
// empty list a console would draw as "no stages".
func TestDiagnoseOmitsStagesAndToleratedWhenEmpty(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	const bare = "20260927-100000-abd0"
	exec := seedRun(t, f, bare, "", "someone@example.com", true)
	s := runstore.Open(b, appSlug, bare)
	if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: bare, Repo: "acme/app", Workflow: "web",
		Execution: exec, Status: runstore.StatusSucceeded, Stage: "writeback"}); err != nil {
		t.Fatal(err)
	}
	seedRun(t, f, "20260927-100000-abd1", "", "someone@example.com", true) // no record at all
	for _, id := range []string{bare, "20260927-100000-abd1"} {
		out, _, err := execute(t, "diagnose", "--json", id)
		if err != nil {
			t.Fatal(err)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatal(err)
		}
		if d.Stages != nil || d.Tolerated != nil {
			t.Fatalf("%s: stages %+v, tolerated %+v", id, d.Stages, d.Tolerated)
		}
		for _, key := range []string{`"stages"`, `"tolerated"`} {
			if strings.Contains(out, key) {
				t.Fatalf("%s prints %s for a run that recorded none:\n%s", id, key, out)
			}
		}
	}
}

// TestDiagnoseBoundsStagesAndTolerated: the record is an object a hand can
// edit, and diagnose prints one document, so both lists are bounded again on
// read: the stages at diagnoseStages and the tolerated findings at
// runstore.MaxTolerated, with the block's total never below the entries kept.
func TestDiagnoseBoundsStagesAndTolerated(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abd2"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	stages := make([]runstore.StageTiming, 0, diagnoseStages+50)
	for i := range diagnoseStages + 50 {
		stages = append(stages, runstore.StageTiming{Name: fmt.Sprintf("stage-%d", i)})
	}
	findings := make([]runstore.ToleratedFinding, 0, runstore.MaxTolerated+50)
	for i := range runstore.MaxTolerated + 50 {
		findings = append(findings, runstore.ToleratedFinding{Title: fmt.Sprintf("finding-%d", i), Mode: rigor.Pro,
			Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Cosmetic})
	}
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Stages: stages,
		Tolerated: &runstore.ToleratedFindings{Findings: findings, Total: 0}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Stages) != diagnoseStages || d.Stages[diagnoseStages-1].Name != fmt.Sprintf("stage-%d", diagnoseStages-1) {
		t.Fatalf("%d stages printed, want the first %d", len(d.Stages), diagnoseStages)
	}
	if d.Tolerated == nil || len(d.Tolerated.Findings) != runstore.MaxTolerated {
		t.Fatalf("tolerated = %+v", d.Tolerated)
	}
	if d.Tolerated.Total < len(d.Tolerated.Findings) {
		t.Fatalf("total %d is below the %d entries printed", d.Tolerated.Total, len(d.Tolerated.Findings))
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

func TestDiagnosePrintsAccountOnlyWhenNamed(t *testing.T) {
	r := runview.Row{Run: "a/b", Status: "succeeded"}
	var b strings.Builder
	d := &Diagnosis{Row: r}
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Account:") {
		t.Fatalf("default account printed a line:\n%s", b.String())
	}

	r.OAuthAccount = "default"
	b.Reset()
	d = &Diagnosis{Row: r}
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Account:") {
		t.Fatalf("explicit default account printed a line:\n%s", b.String())
	}

	r.OAuthAccount = "acme-personal"
	b.Reset()
	d = &Diagnosis{Row: r}
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Account:  acme-personal\n") {
		t.Fatalf("named account missing its line:\n%s", b.String())
	}
}

// TestDiagnosePrintsAccountSanitizesHostileNames: like
// TestLsAccountColumnSanitizesHostileNames, the account name is untrusted
// text from a run's own result.json. printDiagnosis is not a tabwriter
// table (one oneLine-formatted field per line via strings.Builder), so a
// tab is ordinary here, same as in every other field (Reason, PR URL);
// what must not happen is an embedded newline forging a second report
// field, a raw ESC/bidi/line-separator rune reaching the terminal, or a
// 10 KB name blowing up the report.
func TestDiagnosePrintsAccountSanitizesHostileNames(t *testing.T) {
	huge := strings.Repeat("x", 10_000)
	hostile := []string{
		"a\nFake: injected\nb", "esc\x1b[2Jc",
		"bidi" + string(rune(0x202e)) + "evil",
		"line" + string(rune(0x2028)) + "sep",
		"para" + string(rune(0x2029)) + "graph",
		huge,
	}
	for _, name := range hostile {
		r := runview.Row{Run: "a/b", Status: "succeeded", OAuthAccount: name}
		var b strings.Builder
		d := &Diagnosis{Row: r}
		if err := printDiagnosis(&b, d, false); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if n := utf8.RuneCountInString(out); n > 700 {
			t.Fatalf("name %q: diagnosis is %d runes, not bounded:\n%.80q...", name, n, out)
		}
		for _, r := range out {
			if r == '\x1b' || (r >= 0x202a && r <= 0x202e) || r == 0x2028 || r == 0x2029 {
				t.Fatalf("name %q: hostile rune %U survived in:\n%q", name, r, out)
			}
		}
		accountLines := 0
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "Account:") {
				accountLines++
			}
			if l == "Fake: injected" {
				t.Fatalf("name %q: an embedded newline forged its own report line:\n%q", name, out)
			}
		}
		if accountLines != 1 {
			t.Fatalf("name %q: %d lines start with Account:, want 1:\n%q", name, accountLines, out)
		}
	}
}

func TestDiagnoseShowsRouteReportedAndPinWarnings(t *testing.T) {
	const key = "sk-or-v1-testkeyshouldneverprint"
	t.Setenv("OPENROUTER_API_KEY", key)
	f := newCloudFixture(t)
	// The owner's route, so the run's model is the one the built-in catalog
	// prices: the pin warning is the catalog's note (the embedded placeholder
	// row is gone, P6).
	f.appendConfig(t, validateProviderBlock)
	const id = "20260927-130000-dddd"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	cost := runstore.NewCost(0.42, 0, runstore.BasisAPIList)
	cost.ModelBy = map[string]float64{"deepseek/deepseek-v4-flash": 0.30, "claude-sonnet-5-5": 0.12}
	cost.RouteBy = map[string]float64{"openrouter": 0.30}
	cost.ReportedUSD = 0.40
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, CostUSD: 0.42, Cost: &cost}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Route:", "openrouter $0.3000", "Reported:", "$0.4000", "charged $0.3000", "built-in provider price catalog", "zdr", "more than 10% above", "may be too low"} {
		if !strings.Contains(human, want) {
			t.Fatalf("diagnose lacks %q:\n%s", want, human)
		}
	}
	js, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatal(err)
	}
	if d.Route["openrouter"] != 0.30 || d.Reported != 0.40 || d.Charge != 0.30 || len(d.PinWarnings) != 1 || d.ReportedWarning == "" {
		t.Fatalf("route %v reported %v charge %v warnings %v", d.Route, d.Reported, d.Charge, d.PinWarnings)
	}
	if strings.Contains(human+js, key) {
		t.Fatal("the provider key is in the output")
	}

	// A Claude-only run shows none of it.
	const id2 = "20260927-130001-eeee"
	exec2 := seedRun(t, f, id2, "", "someone@example.com", true)
	s2 := runstore.Open(b, appSlug, id2)
	c2 := runstore.NewCost(1, 0, runstore.BasisAPIList)
	c2.ModelBy = map[string]float64{"claude-sonnet-5-5": 1}
	if err := s2.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id2, Repo: "acme/app", Workflow: "web", Execution: exec2,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, CostUSD: 1, Cost: &c2}); err != nil {
		t.Fatal(err)
	}
	human, _, err = execute(t, "diagnose", id2)
	if err != nil || strings.Contains(human, "Route:") || strings.Contains(human, "Reported:") || strings.Contains(human, "Warning:") {
		t.Fatalf("err %v; a Claude run shows provider lines:\n%s", err, human)
	}
}

// TestDiagnoseShowsAStaleCatalog: a run whose models the catalog priced while
// it was past its own 30 days charged the dearest end of their tier's range,
// and diagnose says so and says how to fix it (design §7, P7). The line is
// driven by the record's own flag — result.json's cost.catalog_stale, written
// by the run that charged — so diagnose reports what the run did rather than
// re-measuring the catalog's age today; a run that charged no such row has no
// line.
func TestDiagnoseShowsAStaleCatalog(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	for i, tc := range []struct {
		stale bool
	}{{true}, {false}} {
		id := fmt.Sprintf("20260927-15000%d-aaaa", i)
		exec := seedRun(t, f, id, "", "someone@example.com", true)
		s := runstore.Open(b, appSlug, id)
		cost := runstore.NewCost(0.42, 0, runstore.BasisAPIList)
		cost.ModelBy = map[string]float64{"deepseek/deepseek-v4-flash": 0.42}
		cost.CatalogStale = tc.stale
		if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
			Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, CostUSD: 0.42, Cost: &cost}); err != nil {
			t.Fatal(err)
		}
		human, _, err := execute(t, "diagnose", id)
		if err != nil {
			t.Fatal(err)
		}
		if !tc.stale {
			if strings.Contains(human, "Catalog:") {
				t.Fatalf("a run with no stale row shows the catalog line:\n%s", human)
			}
			continue
		}
		for _, want := range []string{"Catalog:", "stale", "dearest end", "scripts/gen-price-catalog.sh"} {
			if !strings.Contains(human, want) {
				t.Errorf("diagnose lacks %q:\n%s", want, human)
			}
		}
	}
}

// A run with a provider coder and a Claude senior: the charge compared with
// the provider's report is the provider route's spend, not the whole run's,
// and an "unattributed" remainder (maybe Claude's) is not in it.
func TestDiagnoseReportedComparesTheProviderRouteOnly(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	for i, tc := range []struct {
		reported float64
		warn     bool
	}{{0.31, false}, {0.10, false}, {0.32, false}, {0.40, true}} {
		id := fmt.Sprintf("20260927-14000%d-aaaa", i)
		exec := seedRun(t, f, id, "", "someone@example.com", true)
		cost := runstore.NewCost(0.60, 0, runstore.BasisAPIList)
		cost.ModelBy = map[string]float64{"deepseek/deepseek-v4-flash": 0.30, "claude-sonnet-5-5": 0.25, "unattributed": 0.05}
		cost.RouteBy = map[string]float64{"openrouter": 0.30, "unattributed": 0.30}
		cost.ReportedUSD = tc.reported
		if err := runstore.Open(b, appSlug, id).WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
			Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, CostUSD: 0.60, Cost: &cost}); err != nil {
			t.Fatal(err)
		}
		js, _, err := execute(t, "diagnose", "--json", id)
		if err != nil {
			t.Fatal(err)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(js), &d); err != nil {
			t.Fatal(err)
		}
		if d.Charge != 0.30 || d.Reported != tc.reported || (d.ReportedWarning != "") != tc.warn {
			t.Fatalf("reported %v: charge %v reported %v warning %q (want warning %v)", tc.reported, d.Charge, d.Reported, d.ReportedWarning, tc.warn)
		}
	}
}

// With first-line entries in Reviews, the findings come from the transcript
// of the last senior round, not review-<len(Reviews)>.
func TestDiagnoseFindingsFromSeniorRound(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abce"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Reviews: []runstore.ReviewSummary{
			{Round: 1, Tier: runstore.TierFirst, Verdict: "changes", Findings: 1},
			{Round: 2, Tier: runstore.TierFirst, Verdict: "ok", Findings: 0},
			{Round: 1, Tier: runstore.TierSenior, Verdict: "changes", Findings: 1}}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	line := func(sum string) []byte {
		return []byte(`{"type":"result","subtype":"success","result":"x","structured_output":{"verdict":"changes","findings":[{"severity":"high","file":"a.go","summary":"` + sum + `"}]}}` + "\n")
	}
	_ = s.PutFile(ctx, "transcripts/review-1.jsonl", line("senior finding"), "application/x-ndjson")
	_ = s.PutFile(ctx, "transcripts/review-3.jsonl", line("wrong transcript"), "application/x-ndjson")
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Findings) != 1 || d.Findings[0].Summary != "senior finding" {
		t.Fatalf("findings = %+v", d.Findings)
	}
}

// The provider reports the model's price, so the comparison leaves the route
// fee out of the charge: a report equal to the bare price is not a gap, one
// 10% above it is, and a report below the charge never warns.
func TestRouteOfComparesWithoutTheRouteFee(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{"openrouter": {RouteFeePct: 50}}}
	for _, tc := range []struct {
		reported float64
		warn     bool
	}{{0.30, false}, {0.329, false}, {0.34, true}, {0.05, false}} {
		cost := runstore.NewCost(0.45, 0, runstore.BasisAPIList)
		cost.RouteBy = map[string]float64{"openrouter": 0.45} // 0.30 bare plus the 50% fee
		cost.ModelBy = map[string]float64{"deepseek/deepseek-v4-flash": 0.45}
		cost.ReportedUSD = tc.reported
		var d Diagnosis
		routeOf(&d, &runstore.Record{Cost: &cost}, lc, func(s string) string { return s })
		if (d.ReportedWarning != "") != tc.warn {
			t.Errorf("reported %v: warning %q, want warning %v", tc.reported, d.ReportedWarning, tc.warn)
		}
		if tc.warn && !strings.Contains(d.ReportedWarning, "may") {
			t.Errorf("warning %q isn't worded as a possibility", d.ReportedWarning)
		}
	}
}

// TestDiagnoseReportedWarningNamesPricePolicy: on a catalog-priced route a
// reported cost above the charge is as likely to be OpenRouter serving a
// dearer endpoint as a wrong model_prices, so the warning names both ways
// out (design §7, D12, Task 5 step 4).
func TestDiagnoseReportedWarningNamesPricePolicy(t *testing.T) {
	cost := runstore.NewCost(0.42, 0, runstore.BasisAPIList)
	cost.RouteBy = map[string]float64{"openrouter": 0.30}
	cost.ReportedUSD = 0.40
	rec := &runstore.Record{Version: 1, RunID: "20261001-120000-abcd", Cost: &cost}
	var d Diagnosis
	routeOf(&d, rec, nil, func(s string) string { return s })
	for _, want := range []string{"model_prices", "price_policy: highest", "reported"} {
		if !strings.Contains(d.ReportedWarning, want) {
			t.Errorf("the warning lacks %q: %q", want, d.ReportedWarning)
		}
	}
	// A report within the margin warns nothing.
	cost.ReportedUSD = 0.31
	d = Diagnosis{}
	routeOf(&d, rec, nil, func(s string) string { return s })
	if d.ReportedWarning != "" {
		t.Errorf("a report inside the margin warned: %q", d.ReportedWarning)
	}
}

// A compute estimate priced from a table that names itself renders that
// source line under the cost, on a line of its own: diagnose is where an
// operator reads which table the figure came from and what it leaves out
// (design compute-backend.md §5.2, C17). It is not folded into the cost line
// by oneLine, which would turn the break into a "?".
func TestDiagnosePutsTheComputeSourceOnItsOwnLine(t *testing.T) {
	source := "Compute Engine VM list price, e2-standard family, us-central1 (2026-10-15, UNVERIFIED); boot disk and Cloud NAT are not in this figure (design C17)"
	cost := runstore.NewCost(4.12, 0.38, runstore.BasisAPIList)
	cost.ComputeSource = source
	var b strings.Builder
	if err := printDiagnosis(&b, &Diagnosis{Row: runview.Row{Cost: cost}}, false); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if want := "\nCompute source: " + source + "\n"; !strings.Contains(got, want) {
		t.Errorf("diagnose lacks %q:\n%s", want, got)
	}
	if strings.Contains(got, "?") {
		t.Errorf("the source line is folded into the cost line:\n%s", got)
	}
	// A Cloud Run cost carries no source line, so nothing is added.
	cost.ComputeSource = ""
	b.Reset()
	if err := printDiagnosis(&b, &Diagnosis{Row: runview.Row{Cost: cost}}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Compute source") {
		t.Errorf("a cost with no source line rendered one:\n%s", b.String())
	}
}

// TestDiagnoseShowsCacheLineOnlyWhenNonZero (P12): the run's prompt-cache
// read and write token totals get one line of their own, and a run that
// recorded neither -- no gateway, or a gateway run whose calls hit no cache
// -- prints exactly the block it always did.
func TestDiagnoseShowsCacheLineOnlyWhenNonZero(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	for i, tc := range []struct {
		read, write int64
		want        string
	}{
		{0, 0, ""},
		{400, 0, "Cache:    400 read, 0 write (tokens)"},
		{0, 50, "Cache:    0 read, 50 write (tokens)"},
		{400, 50, "Cache:    400 read, 50 write (tokens)"},
	} {
		id := fmt.Sprintf("20260927-17000%d-aaaa", i)
		exec := seedRun(t, f, id, "", "someone@example.com", true)
		s := runstore.Open(b, appSlug, id)
		cost := runstore.NewCost(1.23, 0, runstore.BasisAPIList)
		cost.CacheReadTokens, cost.CacheWriteTokens = tc.read, tc.write
		if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
			Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, CostUSD: 1.23, Cost: &cost}); err != nil {
			t.Fatal(err)
		}
		human, _, err := execute(t, "diagnose", id)
		if err != nil {
			t.Fatal(err)
		}
		lines := 0
		for _, ln := range strings.Split(human, "\n") {
			if strings.HasPrefix(ln, "Cache:") {
				lines++
			}
		}
		if tc.want == "" {
			if lines != 0 {
				t.Fatalf("%d read/%d write: a cache line was printed for a run that recorded none:\n%s", tc.read, tc.write, human)
			}
		} else if lines != 1 || !strings.Contains(human, tc.want+"\n") {
			t.Fatalf("%d read/%d write: want exactly %q:\n%s", tc.read, tc.write, tc.want, human)
		}
		js, _, err := execute(t, "diagnose", "--json", id)
		if err != nil {
			t.Fatal(err)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(js), &d); err != nil {
			t.Fatal(err)
		}
		if d.Row.Cost.CacheReadTokens != tc.read || d.Row.Cost.CacheWriteTokens != tc.write {
			t.Fatalf("%d read/%d write: json cost = %+v", tc.read, tc.write, d.Row.Cost)
		}
	}
}

// updateDiagnoseGolden rewrites testdata/diagnose_tolerated.golden. It is a
// flag of its own: the package's other goldens already own -update,
// -update-review, -update-retire, -update-cloudrun and -update-coordinator,
// and a second flag.Bool of the same name panics.
var updateDiagnoseGolden = flag.Bool("update-diagnose", false, "rewrite testdata/diagnose_tolerated.golden")

// toleratedTwo is the block T4.1's fixtures store: two findings a Pro run let
// through, in the reviewer's order, each carrying the score the mode's table
// read. It is a record a run can really have written — the two entries are two
// of scoredReview's own findings, with their score and their title, and Pro
// tolerates both: the nit is under its floor of 4, and the low one is the
// "extremely rare, high-effort" escape its own row names (likelihood 1 with a
// fix cost of 4). assertFixtureIsStorable reads it back as such, so a later
// edit to a score, a label or a mode cannot leave a fixture no run can store.
func toleratedTwo() *runstore.ToleratedFindings {
	return &runstore.ToleratedFindings{Total: 2, Findings: []runstore.ToleratedFinding{
		{Title: "the retry ceiling differs by one when a job is requeued twice", Label: rigor.Low, Mode: rigor.Pro,
			Likelihood: 1, Impact: 4, FixCost: 4, Class: rigor.Correctness},
		{Title: "the retry table test does not cover a requeue after a drain", Label: rigor.Nit, Mode: rigor.Pro,
			Likelihood: 2, Impact: 1, FixCost: 1, Class: rigor.Cosmetic},
	}}
}

// scoredReview is review-2's transcript of the tolerated fixtures: its first
// two findings are the tolerated ones, scored with rigor Slice 2's four
// optional properties, and its third is the shape every finding had before
// them — unscored, so it blocks in every mode (D4) and is in no tolerated
// block. One diagnosis therefore shows both shapes and both outcomes.
const scoredReview = `{"type":"result","subtype":"success","result":"x","structured_output":{"verdict":"changes","findings":[` +
	`{"severity":"minor","likelihood":1,"impact":4,"fix_cost":4,"class":"correctness",` +
	`"file":"internal/queue/retry.go","summary":"the retry ceiling differs by one when a job is requeued twice"},` +
	`{"severity":"nit","likelihood":2,"impact":1,"fix_cost":1,"class":"cosmetic",` +
	`"file":"internal/queue/retry_test.go","summary":"the retry table test does not cover a requeue after a drain"},` +
	`{"severity":"high","file":"docs/queue.md","summary":"the retry section still describes the legacy table"}]}}`

// assertFixtureIsStorable reads tolerated back the way the runner writes it:
// runstore.NewTolerated keeps an entry only when its score is readable and
// derives its label from that score (D2), and the runner files an entry only
// for a finding of the last review the mode tolerates, with the finding's own
// score and title (T3.4). An entry whose label its score does not derive, or
// whose mode blocks it, or whose score and title no finding of review carries,
// is a record no run can have written — and a reviewer reading the fixture
// should not have to work that out.
func assertFixtureIsStorable(t *testing.T, tolerated *runstore.ToleratedFindings, review string) {
	t.Helper()
	res, found, err := agent.ParseStream(strings.NewReader(review+"\n"), nil)
	if err != nil || !found {
		t.Fatalf("the fixture's transcript is not one diagnose can read: %v, found %v", err, found)
	}
	findings := runner.ParseVerdict(res).Findings
	for i, e := range tolerated.Findings {
		s := rigor.Score{Likelihood: e.Likelihood, Impact: e.Impact, FixCost: e.FixCost, Class: e.Class}
		if !s.Scored() {
			t.Errorf("tolerated.findings[%d] carries no readable score", i)
			continue
		}
		if got := s.Label(); e.Label != got {
			t.Errorf("tolerated.findings[%d] is labelled %s, which its score derives as %s", i, e.Label, got)
		}
		if rigor.Blocks(e.Mode, s) {
			t.Errorf("tolerated.findings[%d] is filed under %s, which blocks that score", i, e.Mode)
		}
		if !slices.ContainsFunc(findings, func(f runner.Finding) bool {
			return f.Likelihood == e.Likelihood && f.Impact == e.Impact && f.FixCost == e.FixCost &&
				rigor.Class(f.Class) == e.Class && f.Summary == e.Title
		}) {
			t.Errorf("tolerated.findings[%d] is not one of the review's findings, score and title alike: %+v", i, e)
		}
	}
}

// unscoredReview is the same round as a transcript written before the score
// existed: neither finding carries one.
const unscoredReview = `{"type":"result","subtype":"success","result":"x","structured_output":{"verdict":"changes","findings":[` +
	`{"severity":"high","file":"internal/queue/retry.go","summary":"the backoff ceiling differs by one when a job is requeued twice"}]}}`

// seedToleratedRun stores a finished run under id: a record with the given
// tolerated block (nil for a run that tolerated nothing) and review-2's
// transcript, review. The run never launched, so the diagnosis is the bucket's
// own documents and nothing that depends on when the test runs.
func seedToleratedRun(t *testing.T, f *cloudFixture, id string, tolerated *runstore.ToleratedFindings, review string) {
	t.Helper()
	seedSpec(t, f, firstRunSpec(id), false)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	cost := runstore.NewCost(1.25, 0, runstore.BasisAPIList)
	if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Reason: "tests failing on the final commit", CostUSD: 1.25, Cost: &cost,
		Reviews:   []runstore.ReviewSummary{{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 2}},
		Tolerated: tolerated}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFile(ctx, "transcripts/review-2.jsonl", []byte(review+"\n"), "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}
}

// TestDiagnoseToleratedGolden: the whole human output of a run whose mode let
// findings through, byte for byte — the score columns the last review's
// findings gained, the tolerated block with the mode, the label and the score
// that decided each entry, and the truncation line's absence when nothing was
// truncated.
func TestDiagnoseToleratedGolden(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000000-abcd"
	tol := toleratedTwo()
	assertFixtureIsStorable(t, tol, scoredReview)
	seedToleratedRun(t, f, id, tol, scoredReview)
	human, errOut, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, errOut)
	}
	path := filepath.Join("testdata", "diagnose_tolerated.golden")
	if *updateDiagnoseGolden {
		if err := os.WriteFile(path, []byte(human), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("no golden file: run go test ./internal/cli -run TestDiagnoseToleratedGolden -update-diagnose")
		}
		t.Fatal(err)
	}
	if string(want) != human {
		t.Errorf("diagnose's output changed (go test -update-diagnose to accept):\n%s\nwant:\n%s", human, want)
	}
}

// TestDiagnoseToleratedJSONFields is the console's half, asserted field by
// field rather than by unmarshalling into Diagnosis: the console reads these
// documents in another language, so a key this adds, renames or drops is a
// silent break there and has to fail here.
func TestDiagnoseToleratedJSONFields(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000010-abcd"
	seedToleratedRun(t, f, id, toleratedTwo(), scoredReview)
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	tol, _ := doc["tolerated"].(map[string]any)
	if tol == nil {
		t.Fatalf("the document carries no tolerated block:\n%s", out)
	}
	if got := keysOfDoc(tol); !slices.Equal(got, []string{"findings", "total"}) {
		t.Errorf("tolerated keys = %v, want [findings total]", got)
	}
	if total, _ := tol["total"].(float64); total != 2 {
		t.Errorf("tolerated.total = %v, want 2", tol["total"])
	}
	entries, _ := tol["findings"].([]any)
	if len(entries) != 2 {
		t.Fatalf("tolerated.findings = %v, want two entries", tol["findings"])
	}
	first, _ := entries[0].(map[string]any)
	if got := keysOfDoc(first); !slices.Equal(got, []string{"class", "fix_cost", "impact", "label", "likelihood", "mode", "title"}) {
		t.Errorf("a tolerated entry's keys = %v, want the seven the record keeps", got)
	}
	for _, want := range []struct {
		key  string
		got  any
		want any
	}{
		{"title", first["title"], "the retry ceiling differs by one when a job is requeued twice"},
		{"label", first["label"], "low"},
		{"mode", first["mode"], "pro"},
		{"likelihood", first["likelihood"], float64(1)},
		{"impact", first["impact"], float64(4)},
		{"fix_cost", first["fix_cost"], float64(4)},
		{"class", first["class"], "correctness"},
	} {
		if want.got != want.want {
			t.Errorf("tolerated.findings[0].%s = %v, want %v", want.key, want.got, want.want)
		}
	}
	// The last review's own findings: the two the mode tolerated carry the
	// four properties beside the three every finding has, and the third — the
	// one the reviewer left unscored — only those three.
	findings, _ := doc["findings"].([]any)
	if len(findings) != 3 {
		t.Fatalf("findings = %v, want three", doc["findings"])
	}
	for i, want := range []map[string]any{
		{"likelihood": float64(1), "impact": float64(4), "fix_cost": float64(4), "class": "correctness"},
		{"likelihood": float64(2), "impact": float64(1), "fix_cost": float64(1), "class": "cosmetic"},
	} {
		scored, _ := findings[i].(map[string]any)
		if got := keysOfDoc(scored); !slices.Equal(got, []string{"class", "file", "fix_cost", "impact", "likelihood", "severity", "summary"}) {
			t.Errorf("scored findings[%d]'s keys = %v", i, got)
		}
		for k, v := range want {
			if scored[k] != v {
				t.Errorf("findings[%d].%s = %v, want %v", i, k, scored[k], v)
			}
		}
	}
	plain, _ := findings[2].(map[string]any)
	if got := keysOfDoc(plain); !slices.Equal(got, []string{"file", "severity", "summary"}) {
		t.Errorf("an unscored finding's keys = %v, want the three it always had", got)
	}
}

// TestDiagnoseUnscoredFindingIsUnchanged: a transcript from before the score
// existed renders exactly as it did — the same row, and no score line and no
// score key anywhere — so an old run stays readable.
func TestDiagnoseUnscoredFindingIsUnchanged(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000020-abcd"
	seedToleratedRun(t, f, id, nil, unscoredReview)
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	want := "\nFindings (last review)\n  [high] internal/queue/retry.go: the backoff ceiling differs by one when a job is requeued twice\n"
	if !strings.Contains(human, want) {
		t.Errorf("an unscored finding's row changed:\n%s\nwant:\n%s", human, want)
	}
	if strings.Contains(human, "score") || strings.Contains(human, "Tolerated") {
		t.Errorf("an unscored finding grew a score line, or a run with no tolerated block grew one:\n%s", human)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"likelihood", "impact", "fix_cost", "class", "tolerated"} {
		if strings.Contains(out, `"`+key+`"`) {
			t.Errorf("an unscored finding's document carries %q:\n%s", key, out)
		}
	}
}

// TestDiagnoseToleratedTruncatedSaysSo: the record's total is what the mode
// really tolerated and its list is what was kept — bounded when it was written
// and again on read, because the record is an object a hand can edit — so a
// total above the entries says how many the list does not show instead of
// reading as the whole of it (F19). The line says nothing about where the rest
// are: a hand-edited record's entries were never bounded at all, so the claim
// that they sit in the record would be false for exactly the record this
// guards against.
func TestDiagnoseToleratedTruncatedSaysSo(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000030-abcd"
	tol := toleratedTwo()
	tol.Total = 137
	seedToleratedRun(t, f, id, tol, scoredReview)
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	if want := "  … and 135 more tolerated findings\n"; !strings.Contains(human, want) {
		t.Errorf("a truncated tolerated list does not say so (%q missing):\n%s", want, human)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil || !strings.Contains(out, `"total": 137`) {
		t.Errorf("the document's total is not the record's: %s, %v", out, err)
	}
}

// TestDiagnoseToleratedModesAreKeptApart: a run resolves one mode (D9), so a
// real record's entries name one; the block is grouped by the mode each entry
// carries rather than printed as one list, so a hand-edited record that names
// two reads as two rather than as whichever was listed first. Each entry stays
// under its own mode, in the order the modes appear, and the tail's line
// closes the last block — the entries are in the reviewer's order and the cut
// is at the end of that order, so that is the only block it belongs to.
func TestDiagnoseToleratedModesAreKeptApart(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000050-abcd"
	tol := toleratedTwo()
	tol.Findings[1].Mode = rigor.Startup
	tol.Total = 5
	seedToleratedRun(t, f, id, tol, scoredReview)
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	want := "\nTolerated by pro\n" +
		"  [low] (likelihood 1, impact 4, fix cost 4, class correctness) the retry ceiling differs by one when a job is requeued twice\n" +
		"\nTolerated by startup\n" +
		"  [nit] (likelihood 2, impact 1, fix cost 1, class cosmetic) the retry table test does not cover a requeue after a drain\n" +
		"  … and 3 more tolerated findings\n"
	if !strings.Contains(human, want) {
		t.Errorf("two modes' entries are not one block per mode, each entry under its own:\n%s\nwant:\n%s", human, want)
	}
}

// TestDiagnoseToleratedTitleIsUntrusted: a tolerated title is the reviewer's
// own sentence, and the record is job-writable, so it goes through the
// redaction and oneLine paths the findings already do: a credential in it is
// redacted and a line break in it cannot forge a second report field.
func TestDiagnoseToleratedTitleIsUntrusted(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	f := newCloudFixture(t)
	const id = "20260101-000040-abcd"
	tol := toleratedTwo()
	tol.Findings[0].Title = "the mirror at https://person-1:" + secret + "@example.com/repo-a is stale\nReason:   the run succeeded"
	seedToleratedRun(t, f, id, tol, scoredReview)
	for _, args := range [][]string{{"diagnose"}, {"diagnose", "--json"}} {
		out, _, err := execute(t, append(args, id)...)
		if err != nil || strings.Contains(out, secret) || !strings.Contains(out, "[REDACTED]") {
			t.Fatalf("%v = %s, %v", args, out, err)
		}
	}
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, "\nReason:   the run succeeded") {
		t.Errorf("a title's line break forged a second report field:\n%s", human)
	}
	if !strings.Contains(human, "is stale?Reason:   the run succeeded\n") {
		t.Errorf("the title is not one-lined onto its own row:\n%s", human)
	}
}

// keysOfDoc is a decoded document object's own keys, sorted, for the
// field-by-field comparisons above: the set is what a rename moves.
func keysOfDoc(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}
