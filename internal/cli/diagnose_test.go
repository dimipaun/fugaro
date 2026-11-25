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
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/runmsg"
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

// TestDiagnoseShowsThePauseAndItsResume: a run that suspended itself is
// diagnosed as paused with the instant its resume is due at and the run that
// resumed it — the two facts the console's Run screen draws on this document's
// row (contract-pause R15, D3195).
//
// Neither is in the paused run's own records, and this read holds one run and
// no listing: the instant is the credentials service's node under paused/
// (runstore.PauseNode), and the resumer is another run of the same repository
// whose own task names the paused run under `resumes` (task.Spec.Resumes, R17).
// The two degrade independently, which the last two cases are: a pause whose
// node this read cannot see has no instant and no resumer named, and a pause
// nothing has resumed keeps the instant alone — a row that had only the record
// would show the word and neither fact.
func TestDiagnoseShowsThePauseAndItsResume(t *testing.T) {
	f := newCloudFixture(t)
	// A resume instant that has passed: the contract refuses a resume asked for
	// before it (contract-pause, "Resuming"), so a resumer exists only behind a
	// due one.
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	resume := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	suspended := wallRunID(t, 2*time.Hour, "7d3e")
	resumer := wallRunID(t, 25*time.Minute, "9f10")
	pausedRun(t, f, suspended, "", 7, at)
	pausedNode(t, f, suspended, suspended, resume, false)
	seedResumer(t, f, resumer, suspended, time.Now().UTC().Add(-25*time.Minute))
	// A second pause with a node and no resumer, and a third with neither.
	standing := wallRunID(t, 4*time.Hour, "cafe")
	pausedRun(t, f, standing, "", 0, at.Add(-time.Hour))
	pausedNode(t, f, standing, standing, resume, false)
	unseen := wallRunID(t, 5*time.Hour, "beef")
	pausedRun(t, f, unseen, "", 0, at.Add(-2*time.Hour))

	diagnosed := func(t *testing.T, id string) Diagnosis {
		t.Helper()
		out, errOut, err := execute(t, "diagnose", "--json", id)
		if err != nil {
			t.Fatalf("diagnose %s: %v (%s)", id, err, errOut)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return d
	}

	t.Run("the row carries both facts", func(t *testing.T) {
		d := diagnosed(t, suspended)
		if d.Row.Status != string(runstore.StatusPaused) {
			t.Errorf("status = %q, want %q", d.Row.Status, runstore.StatusPaused)
		}
		if d.Row.ResumeAt == nil || !d.Row.ResumeAt.Equal(resume) {
			t.Errorf("resume_at = %v, want %s", d.Row.ResumeAt, resume.Format(time.RFC3339))
		}
		if d.Row.ResumedBy != resumer {
			t.Errorf("resumed_by = %q, want %q: the run whose task names this one under `resumes`", d.Row.ResumedBy, resumer)
		}
		// The members on the wire are the two the console's Run screen reads off
		// this document's row, as RFC 3339 UTC and a run id.
		out, _, err := execute(t, "diagnose", "--json", suspended)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Row map[string]json.RawMessage `json:"row"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if got := string(doc.Row["resume_at"]); got != `"`+resume.Format(time.RFC3339)+`"` {
			t.Errorf("resume_at on the wire = %s, want the instant as RFC 3339 UTC", got)
		}
		if got := string(doc.Row["resumed_by"]); got != `"`+resumer+`"` {
			t.Errorf("resumed_by on the wire = %s, want %q", got, resumer)
		}
	})

	t.Run("a run that is not paused carries neither", func(t *testing.T) {
		d := diagnosed(t, resumer)
		if d.Row.ResumeAt != nil || d.Row.ResumedBy != "" {
			t.Errorf("the resumer's own diagnosis carries resume_at %v and resumed_by %q, want neither", d.Row.ResumeAt, d.Row.ResumedBy)
		}
	})

	t.Run("a pause nothing has resumed keeps the instant alone", func(t *testing.T) {
		d := diagnosed(t, standing)
		if d.Row.ResumeAt == nil || !d.Row.ResumeAt.Equal(resume) {
			t.Errorf("resume_at = %v, want %s", d.Row.ResumeAt, resume.Format(time.RFC3339))
		}
		if d.Row.ResumedBy != "" {
			t.Errorf("resumed_by = %q, want none: no run of the repository names this pause", d.Row.ResumedBy)
		}
	})

	t.Run("a pause this read cannot see names neither fact", func(t *testing.T) {
		d := diagnosed(t, unseen)
		if d.Row.Status != string(runstore.StatusPaused) {
			t.Errorf("status = %q, want %q: the record alone is the word", d.Row.Status, runstore.StatusPaused)
		}
		if d.Row.ResumeAt != nil || d.Row.ResumedBy != "" {
			t.Errorf("a pause with no node carries resume_at %v and resumed_by %q, want neither", d.Row.ResumeAt, d.Row.ResumedBy)
		}
	})
}

// TestDiagnoseStopsTheResumerScanAtItsBound is the scan's own edge: a diagnose
// of a paused run asks a bounded number of the repository's runs — the ones
// nearest the pause's resume instant — and what it does at the bound is fixed
// here.
//
// The read holds one run and no listing, so the resumer comes from a scan it
// makes itself (resumersOf). That scan is bounded by pauseResumerScan because a
// paused run's node is never deleted and its instant is never bumped: a window
// that ran on to the repository's newest run would grow with every run minted
// after the pause, at one task read per run, on the read the console's Run
// screen makes when a person opens a paused run.
//
// The first case is the bound: pauseResumerScan runs minted inside the minute
// before the instant fill it, and a resumer one launch later — near enough that
// an unbounded scan would find it — is not named, with the one line that says
// the read stopped there. The second is what the bound must not cost: a resumer
// beside the instant is still named when the repository minted hundreds of runs
// after the pause, which are the runs an unbounded window would have read.
func TestDiagnoseStopsTheResumerScanAtItsBound(t *testing.T) {
	t.Run("the runs nearest the instant fill the bound", func(t *testing.T) {
		f := newCloudFixture(t)
		now := time.Now().UTC()
		resume := now.Add(-30 * time.Minute).Truncate(time.Second)
		suspended := wallRunID(t, 2*time.Hour, "7d3e")
		pausedRun(t, f, suspended, "", 0, now.Add(-2*time.Hour))
		pausedNode(t, f, suspended, suspended, resume, false)
		for i := 0; i < pauseResumerScan; i++ {
			seedRun(t, f, wallRunID(t, 30*time.Minute+time.Duration(i+1)*time.Second, fmt.Sprintf("%04x", i)), "", "", false)
		}
		// Five minutes after the instant, and behind every run above it.
		resumer := wallRunID(t, 25*time.Minute, "9f10")
		seedResumer(t, f, resumer, suspended, now.Add(-25*time.Minute))

		out, errOut, err := execute(t, "diagnose", "--json", suspended)
		if err != nil {
			t.Fatalf("diagnose: %v (%s)", err, errOut)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if d.Row.ResumedBy != "" {
			t.Errorf("resumed_by = %q, want none: the %d runs nearest the resume instant fill the read's bound and %s is past it",
				d.Row.ResumedBy, pauseResumerScan, resumer)
		}
		if want := fmt.Sprintf("the %d runs nearest its resume instant name no resume of it", pauseResumerScan); !strings.Contains(errOut, want) {
			t.Errorf("the read stopped at its bound and did not say so, want %q:\n%s", want, errOut)
		}
	})

	t.Run("runs minted after the pause cost the bound nothing", func(t *testing.T) {
		f := newCloudFixture(t)
		now := time.Now().UTC()
		resume := now.Add(-30 * time.Minute).Truncate(time.Second)
		suspended := wallRunID(t, 2*time.Hour, "7d3e")
		pausedRun(t, f, suspended, "", 0, now.Add(-2*time.Hour))
		pausedNode(t, f, suspended, suspended, resume, false)
		for i := 0; i < 10; i++ {
			seedRun(t, f, wallRunID(t, 30*time.Minute+time.Duration(i+1)*time.Second, fmt.Sprintf("%04x", i)), "", "", false)
		}
		// A minute after the instant, which is where a launch the service
		// granted lands.
		resumer := wallRunID(t, 29*time.Minute, "9f10")
		seedResumer(t, f, resumer, suspended, now.Add(-29*time.Minute))
		// Three bounds' worth minted in the minutes since, none of them a
		// resume of anything.
		for i := 0; i < 3*pauseResumerScan; i++ {
			seedRun(t, f, wallRunID(t, time.Duration(i)*time.Second, fmt.Sprintf("%04x", 0x100+i)), "", "", false)
		}

		out, errOut, err := execute(t, "diagnose", "--json", suspended)
		if err != nil {
			t.Fatalf("diagnose: %v (%s)", err, errOut)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if d.Row.ResumedBy != resumer {
			t.Errorf("resumed_by = %q, want %q: the resumer is beside the pause's own instant whatever the repository minted afterwards",
				d.Row.ResumedBy, resumer)
		}
		if strings.Contains(errOut, "name no resume of it") {
			t.Errorf("the scan stopped at its bound with the resumer inside it:\n%s", errOut)
		}
	})
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

// TestDiagnosePrintsTheSwitchLog: the run's switch log (C1.16, D140) and the
// source that served each stage (C1, CS8) are on the record and neither reached
// a JSON the console reads, so the console could not draw the fallback a step
// took or the money it cost (D3188, VS1). diagnose --json carries both, in the
// record's own order, with every string redacted like the rest of the document
// -- the service wrote from, to and reason, and the record is launcher-writable,
// so they are bytes a hand can edit. The log is bounded at diagnoseSwitches,
// which is the bound a hand-edited record cannot have had.
func TestDiagnosePrintsTheSwitchLog(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	f := newCloudFixture(t)
	const id = "20260927-100000-abd7"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	cost := runstore.NewCost(2.5, 0, runstore.BasisAPIList)
	cost.FallbackUSD = 1.4
	switches := []runstore.Switch{
		{From: "default " + secret, To: "alice", Reason: credsvc.ReasonExhausted, CacheMissUSD: 0.42},
		{To: "bob " + secret, Reason: credsvc.ReasonBroken, CacheMissUnmeasured: true},
	}
	for len(switches) < diagnoseSwitches+1 {
		switches = append(switches, runstore.Switch{To: "alice", Reason: credsvc.ReasonExhausted})
	}
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady,
		CostUSD: 2.5, Cost: &cost,
		Stages: []runstore.StageTiming{
			{Name: "implement", ModelUSD: 2.0, Fallback: true,
				Source: &routing.SourceName{Kind: routing.KindPool, Name: "team " + secret}},
			{Name: "review", ModelUSD: 0.5},
		},
		Switches: switches}
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
	if len(d.Switches) != diagnoseSwitches {
		t.Fatalf("%d switches printed, want the bound %d", len(d.Switches), diagnoseSwitches)
	}
	first := d.Switches[0]
	if first.From != "default [REDACTED]" || first.To != "alice" || first.Reason != credsvc.ReasonExhausted || first.CacheMissUSD != 0.42 {
		t.Fatalf("the first switch = %+v", first)
	}
	// An empty from is the service's own meaning for a call that started on the
	// credential that answered it, so it stays absent rather than becoming a
	// name -- and the unmeasured flag is what says the miss is unknown rather
	// than free, which the zero beside it must not be read without.
	second := d.Switches[1]
	if second.From != "" || second.To != "bob [REDACTED]" || !second.CacheMissUnmeasured || second.CacheMissUSD != 0 {
		t.Fatalf("the second switch = %+v", second)
	}
	src := d.Stages[0].Source
	if !d.Stages[0].Fallback || src == nil || src.Kind != routing.KindPool || src.Name != "team [REDACTED]" {
		t.Fatalf("implement's source = %+v (fallback %v)", src, d.Stages[0].Fallback)
	}
	if d.Stages[1].Source != nil || d.Stages[1].Fallback {
		t.Fatalf("review's source = %+v: a stage the service answered no call of has none", d.Stages[1])
	}
}

// TestDiagnoseOmitsTheSwitchLogOfARunThatNeverSwitched: the key is absent, not
// an empty list, for a run the credentials service never moved a call of --
// which is every run whose record predates the member, and every run on a chain
// that held. An empty list would read as "this run's log was read and is empty",
// a claim about a record that has no such member at all.
func TestDiagnoseOmitsTheSwitchLogOfARunThatNeverSwitched(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abd8"
	seedRun(t, f, id, "", "someone@example.com", true)
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"switches"`) {
		t.Fatalf("a run with no switches carries the key:\n%s", out)
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
				Title: secret, Mode: rigor.Rigor(secret), Label: rigor.Label(secret), Class: rigor.Class(secret),
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

// TestDiagnosePrintsTriageRounds: every triage round the record carries is in
// the document with what served it, whether its call failed, why it failed and
// how its findings were placed, counted by source -- and in the human
// diagnosis a failed round is flagged both above the status block and in the
// Triage block below it. A run a judge weighed looks judged from its findings
// alone, which is what a failed call makes untrue: the fail-safe blocks every
// finding the mode's table blocks (D1846), and the block is where a reader
// learns that this round's fix round was handed findings no judge saw.
func TestDiagnosePrintsTriageRounds(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	const answer = "Invalid API key · Fix external API key"
	f := newCloudFixture(t)
	const id = "20260927-100000-abd9"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady,
		Stages: []runstore.StageTiming{{Name: "triage"}},
		Triage: runstore.NewTriage([]runstore.TriageRound{
			// The round run 20261115-194116-0846 bought: a call the endpoint
			// refused the credential for, at the model and account the record
			// named, with the mode's table placing both of the round's
			// findings because no verdict was read (D1846).
			{Round: 1, Mode: rigor.Startup, Level: policy.LevelL2, Model: "claude-opus-5-5", Account: "bmb",
				Failed: true, Cause: answer + " " + secret,
				Entries: []runstore.TriageEntry{
					{Key: "k1", Verdict: runstore.VerdictBlock, Reason: "the table's own call", Source: runstore.TriageSourceTable},
					{Key: "k2", Verdict: runstore.VerdictBlock, Reason: "no verdict was read", Source: runstore.TriageSourceFailSafe},
				}},
			// The round that answered, with one placement of each of the
			// remaining two sources and no cause at all.
			{Round: 2, Mode: rigor.Startup, Level: policy.LevelL2, Model: "claude-opus-5-5",
				Entries: []runstore.TriageEntry{
					{Key: "k1", Verdict: runstore.VerdictDefer, Reason: "doc drift", Source: runstore.TriageSourceAgent},
					{Key: "k3", Verdict: runstore.VerdictDefer, Reason: "trailing cleanup", Source: runstore.TriageSourceCap},
				}},
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
	if len(d.Triage) != 2 {
		t.Fatalf("triage = %+v, want the record's two rounds", d.Triage)
	}
	failed := d.Triage[0]
	if failed.Round != 1 || failed.Mode != rigor.Startup || failed.Level != policy.LevelL2 ||
		failed.Model != "claude-opus-5-5" || failed.Account != "bmb" {
		t.Errorf("the failed round = %+v, want the round, mode, level, model and account the record named", failed)
	}
	if !failed.Failed || failed.Cause != answer+" [REDACTED]" {
		t.Errorf("the failed round = %+v, want it marked failed with the record's own cause, redacted", failed)
	}
	if failed.Sources != (TriageSources{Table: 1, FailSafe: 1}) || failed.Total != 2 {
		t.Errorf("the failed round's placements = %+v of %d, want one table, one fail_safe and both findings", failed.Sources, failed.Total)
	}
	answered := d.Triage[1]
	if answered.Failed || answered.Cause != "" {
		t.Errorf("the round that answered = %+v, want it unflagged and without a cause", answered)
	}
	if answered.Sources != (TriageSources{Agent: 1, Cap: 1}) || answered.Total != 2 {
		t.Errorf("the round that answered = %+v of %d, want one agent, one cap and both findings", answered.Sources, answered.Total)
	}
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// The flag above the status block: a reader who stops there must not
		// come away thinking the run's rounds were all judged.
		"Triage:   the judge did not answer for round 1 of level 2: " + answer + " [REDACTED]; the fail-safe blocked every finding the mode's table blocks",
		"Triage\n",
		"round 1 (startup, l2, claude-opus-5-5, account bmb): 2 findings — agent 0, table 1, fail_safe 1, cap 0; FAILED: " + answer,
		"round 2 (startup, l2, claude-opus-5-5): 2 findings — agent 1, table 0, fail_safe 0, cap 1",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("the human diagnosis lacks %q:\n%s", want, human)
		}
	}
	if strings.Contains(human, secret) {
		t.Fatalf("the human diagnosis carries the secret:\n%s", human)
	}
}

// TestDiagnosePrintsASkippedTriageRound: a round the run did not ask at all is
// a different fact from one whose call failed, and the document says so on a
// member of its own: `skipped` set, `failed` absent, and the run's own cause
// beside them. The human diagnosis flags it above the status block and in the
// Triage block below it -- a reader who saw "failed" over a round that never ran
// would go looking for a call that does not exist -- and the fix round that
// follows such a round was handed what the mode's table blocks, which no judge
// weighed (D1846).
//
// The clause above the block is runstore's own (TriageSkipClause, the same
// reader the report's status line uses), so the two places cannot word one
// record differently.
func TestDiagnosePrintsASkippedTriageRound(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abda"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady,
		Stages: []runstore.StageTiming{{Name: "triage", Ordinal: 1, Round: 1, Level: policy.LevelL2}},
		Triage: runstore.NewTriage([]runstore.TriageRound{
			{Round: 1, Ordinal: 1, Mode: rigor.Startup, Level: policy.LevelL2, Model: "claude-opus-5-5",
				Skipped: true, Cause: runstore.TriageCauseNoAccount,
				Entries: []runstore.TriageEntry{
					{Key: "k1", Verdict: runstore.VerdictBlock, Reason: "no verdict was read", Source: runstore.TriageSourceFailSafe},
				}},
			// The round that answered, so the two readings sit beside each
			// other and a reader can tell them apart.
			{Round: 2, Ordinal: 2, Mode: rigor.Startup, Level: policy.LevelL2, Model: "claude-opus-5-5",
				Entries: []runstore.TriageEntry{
					{Key: "k1", Verdict: runstore.VerdictDefer, Reason: "doc drift", Source: runstore.TriageSourceAgent},
				}},
		})}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(d.Triage) != 2 {
		t.Fatalf("triage = %+v, want the record's two rounds", d.Triage)
	}
	skipped := d.Triage[0]
	if !skipped.Skipped || skipped.Failed {
		t.Errorf("the round = %+v, want it skipped and not failed", skipped)
	}
	if skipped.Cause != runstore.TriageCauseNoAccount {
		t.Errorf("the skipped round's cause = %q, want the record's own", skipped.Cause)
	}
	if skipped.Sources != (TriageSources{FailSafe: 1}) || skipped.Total != 1 {
		t.Errorf("the skipped round's placements = %+v of %d, want the fail-safe's one", skipped.Sources, skipped.Total)
	}
	if answered := d.Triage[1]; answered.Skipped || answered.Failed || answered.Cause != "" {
		t.Errorf("the round that answered = %+v, want it unflagged and without a cause", answered)
	}
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Triage:   triage did not run for round 1 of level 2: " + runstore.TriageCauseNoAccount + "; the fail-safe blocked every finding the mode's table blocks",
		"round 1 (startup, l2, claude-opus-5-5): 1 finding — agent 0, table 0, fail_safe 1, cap 0; SKIPPED: " + runstore.TriageCauseNoAccount,
		"round 2 (startup, l2, claude-opus-5-5): 1 finding — agent 1, table 0, fail_safe 0, cap 0",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("the human diagnosis lacks %q:\n%s", want, human)
		}
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
		if d.Stages != nil || d.Tolerated != nil || d.Triage != nil {
			t.Fatalf("%s: stages %+v, tolerated %+v, triage %+v", id, d.Stages, d.Tolerated, d.Triage)
		}
		for _, key := range []string{`"stages"`, `"tolerated"`, `"triage"`} {
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

// TestDiagnoseReadsTheLastImplementAttempt: transcripts are written per
// attempt (transcripts/<stage>-<n>.jsonl, n the stage's own attempt number),
// so a run whose implement stage ran twice -- a retried implement after a
// cut stream (issue #1827), a recovered failure -- has two of them, and the
// agent's last word is the attempt the run went on with. diagnose reads the
// highest implement number rec.Stages holds, the way it already reads fix-<n>,
// rather than implement-1 alone, which is the attempt the run threw away.
func TestDiagnoseReadsTheLastImplementAttempt(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	const cut = "API Error: stream closed before completion"
	if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, Retries: 1,
		Stages: []runstore.StageTiming{{Name: "implement"}, {Name: "implement"}, {Name: "review"}}}); err != nil {
		t.Fatal(err)
	}
	_ = s.PutFile(ctx, "transcripts/implement-1.jsonl", []byte(`{"type":"result","subtype":"success","is_error":true,"result":"`+cut+`"}`+"\n"), "application/x-ndjson")
	_ = s.PutFile(ctx, "transcripts/implement-2.jsonl", []byte(`{"type":"result","subtype":"success","result":"added the feature"}`+"\n"), "application/x-ndjson")
	_ = s.PutFile(ctx, "transcripts/review-1.jsonl", []byte(`{"type":"result","subtype":"success","result":"looks right"}`+"\n"), "application/x-ndjson")

	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if d.AgentMessage != "added the feature" {
		t.Errorf("agent message = %q, want the second implement attempt's: the first is the attempt the retry threw away", d.AgentMessage)
	}
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "added the feature") || strings.Contains(human, cut) {
		t.Errorf("diagnose printed the abandoned attempt's words, not the run's own:\n%s", human)
	}
}

// TestDiagnoseReadsImplement1WithoutAStageTiming: the implement walk's floor
// is implement-1, because a record can hold no implement timing while the
// transcript is there. Every attempt uploads its transcript and only then
// writes its stage timing (internal/runner's stageOnce), and a run killed in
// between leaves exactly that: the transcript, no timing. A record written
// before the timings were (2026-10-03) has the same shape. Reading the count
// alone would print no agent message at all for the dead run diagnose exists
// for -- origin/main read implement-1 here whatever rec.Stages held.
func TestDiagnoseReadsImplement1WithoutAStageTiming(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusFailed, Stage: "implement", Reason: "the run was killed"}); err != nil {
		t.Fatal(err)
	}
	_ = s.PutFile(ctx, "transcripts/implement-1.jsonl", []byte(`{"type":"result","subtype":"success","result":"got halfway"}`+"\n"), "application/x-ndjson")

	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if d.AgentMessage != "got halfway" {
		t.Errorf("agent message = %q, want the one implement transcript the record has", d.AgentMessage)
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

// TestDiagnosePrintsTheClass: D1460 answered "both" — the field on --json's
// shape and one printed line, which is the answer's first sentence (design §8
// D9), so it stands directly under the status it describes. Both halves of the
// reading are here: a record carrying the field prints it, a record written
// before the field shipped prints the class its reason reads as, and a run that
// did not fail prints no line at all.
func TestDiagnosePrintsTheClass(t *testing.T) {
	f := newCloudFixture(t)
	wrote := func(id, reason string, class failure.Class) {
		exec := seedRun(t, f, id, "", "", true)
		writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
			Status: runstore.StatusFailed, Stage: "verify", Reason: reason, FailureClass: class})
	}
	const legacy, held = "20260927-100000-abcd", "20260927-110000-abcd"
	// The sentence the runner writes for a tests failure, with no class on the
	// record: the reader's table is what names it.
	wrote(legacy, "tests failing on the final commit", "")
	// A record that carries a class: the field is the class, and its reason —
	// a sentence the reader's table classes review_not_converged — is not read
	// back over it.
	wrote(held, "the mode was raised to pro at finalize, after the last review round", failure.NoCommits)

	for _, tc := range []struct {
		id, line string
		want     failure.Class
	}{
		{legacy, "Status:   failed (stage verify)\nClass:    tests_failed\n", failure.TestsFailed},
		{held, "Class:    no_commits\n", failure.NoCommits},
	} {
		human, errOut, err := execute(t, "diagnose", tc.id)
		if err != nil {
			t.Fatalf("diagnose %s: %v\n%s", tc.id, err, errOut)
		}
		if !strings.Contains(human, tc.line) {
			t.Errorf("diagnose %s lacks %q:\n%s", tc.id, tc.line, human)
		}
		if strings.Contains(human, "Class:    unclassified") {
			t.Errorf("diagnose %s classes a cause its table knows as unclassified:\n%s", tc.id, human)
		}
		out, _, err := execute(t, "diagnose", "--json", tc.id)
		var d Diagnosis
		if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.FailureClass != tc.want {
			t.Fatalf("diagnose --json %s: class %q (%v):\n%s", tc.id, d.FailureClass, err, out)
		}
	}

	done := "20260927-120000-cccc"
	seedFinishedRun(t, f, done)
	human, _, err := execute(t, "diagnose", done)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, "Class:") {
		t.Errorf("a succeeded run prints a class:\n%s", human)
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

// TestDiagnosePrintsCheckpoint: a governance halt leaves a checkpoint report on
// the record (T6.1, D329, G7) and diagnose is where a person reads it before
// deciding whether to continue the run. The block prints the runner's own three
// fields -- the trigger, its words for why the run is over, and the spend it had
// reached -- and the three the stopping agent answers, each only when the record
// carries it: the checkpoint stage that fills them is wired in T7.3, so before
// that every one of the three is empty, and an empty field prints as nothing
// rather than as a fabricated or zero value. The free text comes from the run,
// so it is redacted and printed as data, the way every other string of a record
// is here.
func TestDiagnosePrintsCheckpoint(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	f := newCloudFixture(t)
	const id = "20260927-100000-abce"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	f.run.SetState(exec, backend.StateSucceeded)
	at := time.Date(2026, 9, 27, 10, 5, 0, 0, time.UTC)
	rec := prRecord(id, exec, 7, 2)
	rec.Status, rec.Halt = runstore.StatusHalted, &runstore.Halt{
		Reason: runstore.HaltStageBudget, Scope: "run", At: at, Detail: "implement spent $2.00 of $1.50"}
	rec.Reason = "halted: stage_budget: implement spent $2.00 of $1.50"
	rec.Checkpoint = runstore.NewCheckpoint(runstore.Checkpoint{
		Trigger:          string(runstore.HaltStageBudget),
		WhyOver:          "a stage passed its own budget",
		EstimateUSD:      2,
		Done:             "the parser: " + secret,
		Next:             "wire the flag into the CLI\nand run the tests",
		HardInstructions: "the spec asks for \x1b[31mX\x1b[0m and forbids it",
	})
	writeRecord(t, f, id, rec)

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
	if !strings.Contains(out, `"checkpoint": {`) {
		t.Fatalf("JSON has no checkpoint block:\n%s", out)
	}
	if d.Checkpoint == nil {
		t.Fatalf("json checkpoint is absent:\n%s", out)
	}
	want := runstore.Checkpoint{
		Trigger:          "stage_budget",
		WhyOver:          "a stage passed its own budget",
		EstimateUSD:      2,
		Done:             "the parser: [REDACTED]",
		Next:             "wire the flag into the CLI\nand run the tests",
		HardInstructions: "the spec asks for \x1b[31mX\x1b[0m and forbids it",
	}
	if *d.Checkpoint != want {
		t.Fatalf("json checkpoint = %+v, want %+v", *d.Checkpoint, want)
	}

	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"\nCheckpoint\n",
		"  Trigger:  stage_budget\n",
		"  Why over: a stage passed its own budget\n",
		"  Estimate: $2.0000\n",
		"  Done:     the parser: [REDACTED]\n",
		"  Next:     wire the flag into the CLI\n            and run the tests\n",
		"  Hard:     the spec asks for X and forbids it\n",
	} {
		if !strings.Contains(human, line) {
			t.Fatalf("diagnose lacks %q:\n%s", line, human)
		}
	}
	if strings.ContainsAny(human, "\x1b") {
		t.Fatalf("the checkpoint carries a terminal escape:\n%q", human)
	}

	// The runner's fields alone, as a checkpoint written before the checkpoint
	// stage exists: the three the agent answers are absent from the record, so
	// they are absent from both outputs rather than printed empty.
	const bare = "20260927-110000-bbbf"
	e2 := seedRun(t, f, bare, "", "", true)
	f.run.SetState(e2, backend.StateSucceeded)
	rec2 := prRecord(bare, e2, 8, 1)
	rec2.Status, rec2.Halt = runstore.StatusHalted, &runstore.Halt{
		Reason: runstore.HaltFanout, Scope: "run", At: at, Detail: "implement ran 3 sub-agents of 1"}
	rec2.Checkpoint = runstore.NewCheckpoint(runstore.Checkpoint{
		Trigger: string(runstore.HaltFanout), WhyOver: "a stage used more sub-agents than the maximum", EstimateUSD: 1})
	writeRecord(t, f, bare, rec2)
	human, _, err = execute(t, "diagnose", bare)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"  Trigger:  fanout\n", "  Why over: a stage used more sub-agents than the maximum\n", "  Estimate: $1.0000\n"} {
		if !strings.Contains(human, line) {
			t.Fatalf("diagnose lacks %q:\n%s", line, human)
		}
	}
	for _, absent := range []string{"  Done:", "  Next:", "  Hard:"} {
		if strings.Contains(human, absent) {
			t.Fatalf("diagnose prints %q for a field the record does not carry:\n%s", absent, human)
		}
	}
	out, _, err = execute(t, "diagnose", "--json", bare)
	if err != nil || strings.Contains(out, `"done"`) || strings.Contains(out, `"next"`) || strings.Contains(out, `"hard_instructions"`) {
		t.Fatalf("json carries an empty checkpoint field (%v):\n%s", err, out)
	}

	// A budget halt carries no checkpoint (only a governance stop does), and a
	// run without one prints no block at all.
	const other = "20260927-120000-bbb0"
	e3 := seedRun(t, f, other, "", "", true)
	f.run.SetState(e3, backend.StateSucceeded)
	rec3 := prRecord(other, e3, 9, 1)
	rec3.Status, rec3.Halt = runstore.StatusHalted, &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run", At: at}
	writeRecord(t, f, other, rec3)
	for _, args := range [][]string{{"diagnose", other}, {"diagnose", "--json", other}} {
		out, _, err := execute(t, args...)
		if err != nil || strings.Contains(out, "Checkpoint") || strings.Contains(out, `"checkpoint"`) {
			t.Fatalf("%v prints a checkpoint for a run that has none (%v):\n%s", args, err, out)
		}
	}

	// The record is an object a hand can edit, so the empty block it can write
	// -- `{"trigger": ""}` -- is the absent one: a block with no report in it
	// prints nowhere, rather than as a bare header and a trigger of "".
	const blank = "20260927-130000-bbb1"
	e4 := seedRun(t, f, blank, "", "", true)
	f.run.SetState(e4, backend.StateSucceeded)
	rec4 := prRecord(blank, e4, 10, 1)
	rec4.Checkpoint = &runstore.Checkpoint{}
	writeRecord(t, f, blank, rec4)
	for _, args := range [][]string{{"diagnose", blank}, {"diagnose", "--json", blank}} {
		out, _, err := execute(t, args...)
		if err != nil || strings.Contains(out, "Checkpoint") || strings.Contains(out, `"checkpoint"`) {
			t.Fatalf("%v prints a checkpoint block the record does not carry (%v):\n%s", args, err, out)
		}
	}

	// The bound on read: a hand can write a text field longer than the one the
	// write path clips to (runstore.NewCheckpoint), and the document clips it
	// again -- like the stage and tolerated bounds above.
	const long = "20260927-140000-bbb2"
	e5 := seedRun(t, f, long, "", "", true)
	f.run.SetState(e5, backend.StateSucceeded)
	rec5 := prRecord(long, e5, 11, 1)
	rec5.Checkpoint = &runstore.Checkpoint{Trigger: "fanout", Done: strings.Repeat("x", runstore.MaxCheckpointText+50)}
	writeRecord(t, f, long, rec5)
	out, _, err = execute(t, "diagnose", "--json", long)
	var d5 Diagnosis
	if err != nil || json.Unmarshal([]byte(out), &d5) != nil || d5.Checkpoint == nil {
		t.Fatalf("diagnose --json: %v:\n%s", err, out)
	}
	// clipRunes keeps the bound's runes and marks the cut, the way every other
	// bound here does.
	if done := d5.Checkpoint.Done; len(done) != runstore.MaxCheckpointText+len("…") || !strings.HasSuffix(done, "…") || len([]rune(done)) != runstore.MaxCheckpointText+1 {
		t.Fatalf("a hand-edited checkpoint field is %d runes, %q, want the bound %d marked", len([]rune(done)), done[len(done)-3:], runstore.MaxCheckpointText)
	}

	// A value that ends in a newline: the block prints it without the line of
	// alignment padding a trailing newline would otherwise leave behind.
	rec5.Checkpoint = &runstore.Checkpoint{Trigger: "fanout", Done: "the parser\n\n"}
	writeRecord(t, f, long, rec5)
	human, _, err = execute(t, "diagnose", long)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(human, "\n") {
		if line != "" && strings.TrimSpace(line) == "" {
			t.Fatalf("a checkpoint field's trailing newline left a blank line:\n%q", human)
		}
	}
}

// TestDiagnoseReadsACheckpointUntagged pins G6 (D521, D529) in this core
// build: a record with a governance stop and the checkpoint report the stop
// left is read and shown by diagnose, in the text a person reads and in the
// JSON the console reads, with nothing under ee/ in the binary. diagnose reads
// the record's own fields -- the halt, its reason, and the checkpoint -- and
// the governance vocabulary they arrive in is the core's (D522: an untagged
// build decodes a governance block and never refuses it), so a halt a tagged
// runner wrote is explained here exactly as it is there. This file carries no
// build tag and registers no seam, so it is the untagged reader's own test.
func TestDiagnoseReadsACheckpointUntagged(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-090000-abcf"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	at := time.Date(2026, 9, 27, 9, 4, 0, 0, time.UTC)
	rec := prRecord(id, exec, 6, 1.75)
	rec.Status = runstore.StatusHalted
	rec.Reason = "halted: stage_budget: implement spent $1.20 of $0.90"
	rec.Halt = &runstore.Halt{Reason: runstore.HaltStageBudget, Scope: "run", At: at,
		Detail: "implement spent $1.20 of $0.90"}
	rec.Checkpoint = runstore.NewCheckpoint(runstore.Checkpoint{
		Trigger:     string(runstore.HaltStageBudget),
		WhyOver:     "the implement stage passed the budget the repository set for it",
		EstimateUSD: 1.2,
		Next:        "finish the parser tests",
	})
	writeRecord(t, f, id, rec)

	if !slices.Contains(runstore.GovernanceHaltReasons(), runstore.HaltStageBudget) {
		t.Fatalf("this build does not decode stage_budget as a governance stop: %v", runstore.GovernanceHaltReasons())
	}

	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if d.Halt == nil || d.Halt.Reason != runstore.HaltStageBudget {
		t.Fatalf("json halt = %+v:\n%s", d.Halt, out)
	}
	want := runstore.Checkpoint{
		Trigger:     "stage_budget",
		WhyOver:     "the implement stage passed the budget the repository set for it",
		EstimateUSD: 1.2,
		Next:        "finish the parser tests",
	}
	if d.Checkpoint == nil || *d.Checkpoint != want {
		t.Fatalf("json checkpoint = %+v:\n%s", d.Checkpoint, out)
	}
	if !strings.Contains(out, `"checkpoint": {`) {
		t.Fatalf("json has no checkpoint block:\n%s", out)
	}

	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"Status:   halted (stage writeback)\n",
		"Halted:   stage_budget (run) at 2026-09-27T09:04:00Z: implement spent $1.20 of $0.90\n",
		"\nCheckpoint\n",
		"  Trigger:  stage_budget\n",
		"  Why over: the implement stage passed the budget the repository set for it\n",
		"  Estimate: $1.2000\n",
		"  Next:     finish the parser tests\n",
	} {
		if !strings.Contains(human, line) {
			t.Fatalf("diagnose lacks %q:\n%s", line, human)
		}
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
	// The Reported line's parenthetical says the table price settles and
	// names the one exception, so it does not contradict the Tier line above
	// it on a run whose tier charge the report floored.
	for _, want := range []string{"Route:", "openrouter $0.3000", "Reported:", "$0.4000", "charged $0.3000", "the table price settles; an unpriced tier's charge is floored by the report", "built-in provider price catalog", "zdr", "more than 10% above", "may be too low"} {
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

// TestDiagnoseReportAgainstBand: a record that carries the table's band is
// read against the band and not against the charge (D2650, F16). The human
// line says what the run settled, how many of its calls the report covers and
// the band; the JSON carries the record's three fields; and a report more than
// reportedGapPct outside either end warns in band_warning -- the field the
// console does not render yet, which is why reported_warning is empty for such
// a record rather than left carrying a comparison it no longer makes.
func TestDiagnoseReportAgainstBand(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	for i, tc := range []struct {
		reported   float64
		lo, hi     float64
		unreported int
		warn       string // "" inside the band, else "above" or "below"
	}{
		{0.42, 0.30, 0.45, 2, ""},
		{0.55, 0.30, 0.45, 2, "above"},
		{0.20, 0.30, 0.45, 2, "below"},
		{0.45, 0.30, 0.45, 0, ""}, // the dearest end itself, and every call reported
	} {
		id := fmt.Sprintf("20260927-17000%d-aaaa", i)
		exec := seedRun(t, f, id, "", "someone@example.com", true)
		cost := runstore.NewCost(0.42, 0, runstore.BasisAPIList)
		cost.ModelBy = map[string]float64{"deepseek/deepseek-v4-flash": 0.42}
		cost.RouteBy = map[string]float64{"openrouter": 0.42}
		cost.ReportedUSD, cost.TableLoUSD, cost.TableHiUSD, cost.UnreportedCalls =
			tc.reported, tc.lo, tc.hi, tc.unreported
		calls := 5
		if err := runstore.Open(b, appSlug, id).WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
			Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, CostUSD: 0.42, Cost: &cost,
			Stages: []runstore.StageTiming{{Name: "implement", Calls: &calls}}}); err != nil {
			t.Fatal(err)
		}
		human, _, err := execute(t, "diagnose", id)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("Settled:  $0.4200 (reported on %d of %d calls); table band $%.4f-$%.4f",
			5-tc.unreported, 5, tc.lo, tc.hi)
		if !strings.Contains(human, want) {
			t.Fatalf("reported %v: diagnose lacks %q:\n%s", tc.reported, want, human)
		}
		if strings.Contains(human, "Reported:") {
			t.Fatalf("reported %v: a record with the band printed the charge comparison:\n%s", tc.reported, human)
		}
		js, _, err := execute(t, "diagnose", "--json", id)
		if err != nil {
			t.Fatal(err)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(js), &d); err != nil {
			t.Fatal(err)
		}
		if d.TableLoUSD != tc.lo || d.TableHiUSD != tc.hi || d.UnreportedCalls != tc.unreported {
			t.Fatalf("reported %v: band %v-%v unreported %d, want %v-%v %d",
				tc.reported, d.TableLoUSD, d.TableHiUSD, d.UnreportedCalls, tc.lo, tc.hi, tc.unreported)
		}
		if d.ReportedWarning != "" {
			t.Errorf("reported %v: reported_warning = %q, want empty for a record with a band", tc.reported, d.ReportedWarning)
		}
		if tc.warn == "" && d.BandWarning != "" {
			t.Errorf("reported %v: band_warning = %q, want none inside the band", tc.reported, d.BandWarning)
		}
		if tc.warn != "" {
			if d.BandWarning == "" {
				t.Fatalf("reported %v: no band_warning, want one %s the band", tc.reported, tc.warn)
			}
			if !strings.Contains(d.BandWarning, tc.warn) {
				t.Errorf("reported %v: band_warning %q does not say %q", tc.reported, d.BandWarning, tc.warn)
			}
			if !strings.Contains(human, "Warning:  "+oneLine(d.BandWarning)) {
				t.Errorf("reported %v: the warning is not in the human output:\n%s", tc.reported, human)
			}
		}
	}
}

// TestReportedCallsReadsTheRunsOwnCounts: the coverage the Settled line prints
// comes from the record's own figures -- the gateway's per-stage call counts
// and cost.unreported_calls -- and never from a count diagnose would have to
// guess. A record whose unreported count sits above the stages' sum is a run
// whose calls settled after their stage's report was taken, and the larger of
// the two is what it settled.
func TestReportedCallsReadsTheRunsOwnCounts(t *testing.T) {
	calls, none := 5, 0
	stages := []runstore.StageTiming{{Name: "implement", Calls: &calls}, {Name: "review"}, {Name: "fix", Calls: &none}}
	for _, tc := range []struct{ unreported, reported, settled int }{
		{0, 5, 5},
		{2, 3, 5},
		{5, 0, 5},
		{7, 0, 7},
	} {
		reported, settled := reportedCalls(stages, tc.unreported)
		if reported != tc.reported || settled != tc.settled {
			t.Errorf("unreported %d: reported on %d of %d, want %d of %d",
				tc.unreported, reported, settled, tc.reported, tc.settled)
		}
	}
}

// TestDiagnoseOldRecordUnchanged: a record written before the band existed
// keeps the comparison and the line it always had -- the report against the
// charge without the route fee, and no band in either output.
func TestDiagnoseOldRecordUnchanged(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-180000-aaaa"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	cost := runstore.NewCost(0.60, 0, runstore.BasisAPIList)
	cost.ModelBy = map[string]float64{"deepseek/deepseek-v4-flash": 0.30, "unattributed": 0.30}
	cost.RouteBy = map[string]float64{"openrouter": 0.30, "unattributed": 0.30}
	cost.ReportedUSD = 0.40
	if err := runstore.Open(b, appSlug, id).WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, CostUSD: 0.60, Cost: &cost}); err != nil {
		t.Fatal(err)
	}
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	want := "Reported: providers said $0.4000; charged $0.3000 (the table price settles; an unpriced tier's charge is floored by the report, clamped to the call's reservation)"
	if !strings.Contains(human, want) {
		t.Fatalf("diagnose lacks %q:\n%s", want, human)
	}
	if strings.Contains(human, "Settled:") || strings.Contains(human, "table band") {
		t.Fatalf("a record with no band printed one:\n%s", human)
	}
	js, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"table_lo_usd", "table_hi_usd", "unreported_calls", "band_warning"} {
		if _, ok := doc[key]; ok {
			t.Errorf("a record with no band carries %q:\n%s", key, js)
		}
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatal(err)
	}
	if d.Reported != 0.40 || d.Charge != 0.30 || d.ReportedWarning == "" {
		t.Fatalf("reported %v charge %v warning %q", d.Reported, d.Charge, d.ReportedWarning)
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

// TestDiagnoseReportedWarningNamesModelPrices: a reported cost above the
// charge or above the table's dearest end has one answer left, model_prices,
// because the catalog already charges its dearest end by default (D2644) —
// the switch the warning used to name is the default, so naming it would be
// advice to change nothing. A record that carries the band warns the same way
// in band_warning, whose two directions name the same answers -- the band's
// dearest end for a report above it, and a price too high for a report below
// the cheapest one (D2650).
func TestDiagnoseReportedWarningNamesModelPrices(t *testing.T) {
	cost := runstore.NewCost(0.42, 0, runstore.BasisAPIList)
	cost.RouteBy = map[string]float64{"openrouter": 0.30}
	cost.ReportedUSD = 0.40
	rec := &runstore.Record{Version: 1, RunID: "20261001-120000-abcd", Cost: &cost}
	var d Diagnosis
	routeOf(&d, rec, nil, func(s string) string { return s })
	for _, want := range []string{"model_prices", "reported"} {
		if !strings.Contains(d.ReportedWarning, want) {
			t.Errorf("the warning lacks %q: %q", want, d.ReportedWarning)
		}
	}
	if strings.Contains(d.ReportedWarning, "price_policy") {
		t.Errorf("the warning still names a price_policy, which is the default it already applies: %q", d.ReportedWarning)
	}
	// A report within the margin warns nothing.
	cost.ReportedUSD = 0.31
	d = Diagnosis{}
	routeOf(&d, rec, nil, func(s string) string { return s })
	if d.ReportedWarning != "" {
		t.Errorf("a report inside the margin warned: %q", d.ReportedWarning)
	}

	// The same record with the table's band: the charge is no longer the
	// comparison, so the warning is the band's and reported_warning is empty.
	cost.ReportedUSD, cost.TableLoUSD, cost.TableHiUSD = 0.40, 0.30, 0.35
	d = Diagnosis{}
	routeOf(&d, rec, nil, func(s string) string { return s })
	for _, want := range []string{"model_prices", "reported", "above"} {
		if !strings.Contains(d.BandWarning, want) {
			t.Errorf("the band warning lacks %q: %q", want, d.BandWarning)
		}
	}
	if strings.Contains(d.BandWarning, "price_policy") {
		t.Errorf("the band warning still names a price_policy, which is the default it already applies: %q", d.BandWarning)
	}
	if d.ReportedWarning != "" {
		t.Errorf("a record with a band kept the charge comparison: %q", d.ReportedWarning)
	}
	cost.ReportedUSD = 0.20
	d = Diagnosis{}
	routeOf(&d, rec, nil, func(s string) string { return s })
	for _, want := range []string{"model_prices", "below"} {
		if !strings.Contains(d.BandWarning, want) {
			t.Errorf("the below-band warning lacks %q: %q", want, d.BandWarning)
		}
	}
	// Inside the band -- between the cheapest end and the dearest, with the
	// 10% margin either side -- nothing warns at all.
	cost.ReportedUSD = 0.32
	d = Diagnosis{}
	routeOf(&d, rec, nil, func(s string) string { return s })
	if d.BandWarning != "" || d.ReportedWarning != "" {
		t.Errorf("a report inside the band warned: %q / %q", d.BandWarning, d.ReportedWarning)
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

// TestDiagnoseNamesChargedServiceTiers: a run whose calls came back at a
// service tier the price table has no multiplier for charged them
// conservatively (design §6.1), so diagnose names the tier and the multiple
// over the Cost line: without it a reader has a figure nothing explains --
// on a provider route a complete call settled on the provider's own reported
// cost, and only a call the stream cut short kept the multiple with the report
// as a floor under it, so the tier's name is what explains the figure. That
// second figure is named only for a run that recorded one: a Claude run has no
// route and no report. A run whose calls were all priced normally prints no
// such line at all.
func TestDiagnoseNamesChargedServiceTiers(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	for i, tc := range []struct {
		tiers    []string
		reported float64
		want     string
	}{
		{nil, 0, ""},
		{[]string{`usage.service_tier "priority"`}, 0, `Tier:     usage.service_tier "priority" charged at 2x the rates the call was priced at (no published price for the tier)`},
		{[]string{`usage.service_tier "priority"`}, 1.5, `Tier:     usage.service_tier "priority" charged at 2x the rates the call was priced at, or the provider's reported cost with the route fee: a complete call settled on that figure, floored at the row's cheapest end, and a call the stream cut short was charged the dearer of the multiple and that figure (no published price for the tier)`},
		{[]string{`usage.service_tier "priority"`, `usage.service_tier 12`}, 1.5, `Tier:     usage.service_tier "priority", usage.service_tier 12 charged at 2x the rates the call was priced at, or the provider's reported cost with the route fee: a complete call settled on that figure, floored at the row's cheapest end, and a call the stream cut short was charged the dearer of the multiple and that figure (no published price for the tier)`},
	} {
		id := fmt.Sprintf("20260927-19000%d-aaaa", i)
		exec := seedRun(t, f, id, "", "someone@example.com", true)
		s := runstore.Open(b, appSlug, id)
		cost := runstore.NewCost(1.23, 0, runstore.BasisAPIList)
		cost.ServiceTiers = tc.tiers
		// The reported cost is what decides the clause: a run with none --
		// every Claude run -- has no figure to name, and the line stops at
		// the multiple.
		if tc.reported > 0 {
			cost.ReportedUSD, cost.RouteBy = tc.reported, map[string]float64{"openrouter": 2}
		}
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
			if strings.HasPrefix(ln, "Tier:") {
				lines++
			}
		}
		if tc.want == "" {
			if lines != 0 {
				t.Fatalf("a run charged no tier premium printed a tier line:\n%s", human)
			}
		} else if lines != 1 || !strings.Contains(human, tc.want+"\n") {
			t.Fatalf("want exactly %q:\n%s", tc.want, human)
		}
	}
}

// TestDiagnosePrintsRetries: a run that retried a stage because its model
// stream was closed before the response finished (issue #1827) says so where
// a reader looks for why a run took longer or cost more than its stages
// suggest, and a run that retried nothing says nothing -- the count is
// additive (runstore.Record.Retries) and absent, not zero, when it is 0.
func TestDiagnosePrintsRetries(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	for i, tc := range []struct {
		retries int
		want    string
	}{
		{0, ""},
		{1, "Retries:  1 (the model's stream was closed before a stage finished, so that stage was run once more)"},
		{2, "Retries:  2 (the model's stream was closed before a stage finished, so that stage was run once more)"},
	} {
		id := fmt.Sprintf("20260927-21000%d-aaaa", i)
		exec := seedRun(t, f, id, "", "someone@example.com", true)
		s := runstore.Open(b, appSlug, id)
		if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
			Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, Retries: tc.retries}); err != nil {
			t.Fatal(err)
		}
		human, _, err := execute(t, "diagnose", id)
		if err != nil {
			t.Fatal(err)
		}
		lines := 0
		for _, ln := range strings.Split(human, "\n") {
			if strings.HasPrefix(ln, "Retries:") {
				lines++
			}
		}
		if tc.want == "" {
			if lines != 0 {
				t.Fatalf("a run that retried nothing printed a retries line:\n%s", human)
			}
		} else if lines != 1 || !strings.Contains(human, tc.want+"\n") {
			t.Fatalf("%d retries: want exactly %q:\n%s", tc.retries, tc.want, human)
		}
		js, _, err := execute(t, "diagnose", "--json", id)
		if err != nil {
			t.Fatal(err)
		}
		var d Diagnosis
		if err := json.Unmarshal([]byte(js), &d); err != nil {
			t.Fatal(err)
		}
		if d.Retries != tc.retries {
			t.Fatalf("%d retries: json retries = %d", tc.retries, d.Retries)
		}
		if tc.retries == 0 && strings.Contains(js, `"retries"`) {
			t.Fatalf("a run that retried nothing carries a retries key in --json: the field is additive and absent at 0:\n%s", js)
		}
	}
}

// TestDiagnoseRendersTheServiceTierCounter: a run that came back at more
// distinct unpriced tiers than the record names carries the gateway's own
// "+N more" entry as the last element of cost.service_tiers (it is
// ServiceTierSet.List that is bounded, not the count), and diagnose prints
// the list as it stands. Dropping the entry would read as a run that saw
// only the MaxServiceTiers names above it, which is the over-claim the bound
// exists to avoid.
func TestDiagnoseRendersTheServiceTierCounter(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	tiers := make([]string, 0, gateway.MaxServiceTiers+1)
	for i := range gateway.MaxServiceTiers {
		tiers = append(tiers, fmt.Sprintf(`usage.service_tier "tier-%02d"`, i))
	}
	tiers = append(tiers, "+3 more")
	const id = "20260927-190009-aaaa"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	s := runstore.Open(b, appSlug, id)
	cost := runstore.NewCost(1.23, 0, runstore.BasisAPIList)
	cost.ServiceTiers = tiers
	cost.ReportedUSD, cost.RouteBy = 1.5, map[string]float64{"openrouter": 2}
	if err := s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, CostUSD: 1.23, Cost: &cost}); err != nil {
		t.Fatal(err)
	}
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	want := "Tier:     " + strings.Join(tiers, ", ") + " charged at 2x the rates the call was priced at, or the provider's reported cost with the route fee: a complete call settled on that figure, floored at the row's cheapest end, and a call the stream cut short was charged the dearer of the multiple and that figure (no published price for the tier)"
	if !strings.Contains(human, want+"\n") {
		t.Fatalf("want %q:\n%s", want, human)
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

// TestDiagnosePrintsTheReviewersModelEffortAndHead: the Reviews block names what
// reviewed each round — the model, the reasoning effort and the commit it read
// (CLI 0.16.0) — and --json carries the same three on the entry, with the three
// keys absent from a round that recorded none. The omission is the record's own:
// a console reads the keys it has rather than an empty claim, and a record
// written before the fields existed reads exactly as it always did.
func TestDiagnosePrintsTheReviewersModelEffortAndHead(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000030-abcd"
	// A git head as the runner records one: 40 lowercase hex characters, the
	// SHA-1 a `git rev-parse` answers.
	head := strings.Repeat("a", 40)
	seedReviewersRun(t, f, id, []runstore.ReviewSummary{
		{Round: 1, Tier: runstore.TierFirst, Verdict: "ship", Head: head, Model: "deepseek/deepseek-v4-flash"},
		{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 2, Head: head, Model: "claude-opus-5-5", Effort: "high"},
		{Round: 3, Tier: runstore.TierAdversarial, Verdict: "none"},
	})
	human, errOut, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, errOut)
	}
	for _, want := range []string{
		"\nReviews\n",
		"  round 1 first: ship, 0 findings (recorded: model deepseek/deepseek-v4-flash, at " + head[:7] + ")\n",
		"  round 2 senior: changes, 2 findings (recorded: model claude-opus-5-5, effort high, at " + head[:7] + ")\n",
		"  round 3 adversarial: no verdict, 0 findings\n",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("diagnose does not print %q:\n%s", want, human)
		}
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	entries, _ := doc["reviews"].([]any)
	if len(entries) != 3 {
		t.Fatalf("reviews = %v, want three entries", doc["reviews"])
	}
	senior, _ := entries[1].(map[string]any)
	if got := keysOfDoc(senior); !slices.Equal(got, []string{"effort", "findings", "head", "model", "round", "tier", "verdict"}) {
		t.Errorf("a review entry's keys = %v, want the seven the round recorded", got)
	}
	for _, want := range []struct {
		key  string
		got  any
		want any
	}{
		{"model", senior["model"], "claude-opus-5-5"},
		{"effort", senior["effort"], "high"},
		{"head", senior["head"], head},
	} {
		if want.got != want.want {
			t.Errorf("reviews[1].%s = %v, want %v", want.key, want.got, want.want)
		}
	}
	// The round that recorded none of the three carries none of the three
	// keys, rather than three empty strings.
	plain, _ := entries[2].(map[string]any)
	if got := keysOfDoc(plain); !slices.Equal(got, []string{"findings", "round", "tier", "verdict"}) {
		t.Errorf("a review entry that recorded no model, effort or head has keys %v", got)
	}
}

// TestShortGitHead: the Reviews block shortens a head the way git abbreviates
// one — 7 characters of a SHA-1 object id, 12 of a SHA-256 — and answers
// "(invalid)" for anything that is not an object id, so a record a hand edited
// cannot put a slice of arbitrary text where a head goes.
func TestShortGitHead(t *testing.T) {
	sha1, sha256 := strings.Repeat("a1", 20), strings.Repeat("f0", 32)
	if got := shortGitHead(sha1); got != "a1a1a1a" {
		t.Errorf("shortGitHead(sha1) = %q, want its first 7 characters", got)
	}
	if got := shortGitHead(sha256); got != "f0f0f0f0f0f0" {
		t.Errorf("shortGitHead(sha256) = %q, want its first 12 characters", got)
	}
	for _, bad := range []string{
		"",
		strings.Repeat("a", 39),
		strings.Repeat("a", 41),
		strings.Repeat("A", 40),
		strings.Repeat("g", 40),
		"HEAD",
		strings.Repeat("é", 20),
	} {
		if got := shortGitHead(bad); got != "(invalid)" {
			t.Errorf("shortGitHead(%q) = %q, want (invalid)", bad, got)
		}
	}
}

// TestDiagnoseSaysWhenTheReviewListIsCut: a record with more rounds than the
// document prints says so in one line, and the line claims only what a cut
// list leaves knowable — that it is at most that many. The wording is pinned
// here because the console's json contract quotes it in the `reviews` row: the
// row said the line named how many rounds it left out, which no list can say
// after the cut.
func TestDiagnoseSaysWhenTheReviewListIsCut(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000031-abcd"
	reviews := make([]runstore.ReviewSummary, diagnoseReviews)
	for i := range reviews {
		reviews[i] = runstore.ReviewSummary{Round: i + 1, Tier: runstore.TierSenior, Verdict: "ship"}
	}
	seedReviewersRun(t, f, id, reviews)
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("  … at most %d review rounds are printed\n", diagnoseReviews)
	if !strings.Contains(human, want) {
		t.Errorf("a cut review list does not print %q:\n%s", want, human)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if entries, _ := doc["reviews"].([]any); len(entries) != diagnoseReviews {
		t.Errorf("reviews holds %d entries, want the diagnoseReviews = %d the block prints", len(entries), diagnoseReviews)
	}
}

// TestDiagnoseRedactsEveryStringOfAReviewRound: the tier and the verdict are
// enums on the write path, but they are strings on the wire and a hand-edited
// record can put anything in them, so every string of a review round goes
// through the document's own redaction — the tier and the verdict included, as
// a tolerated finding's own three enums do.
func TestDiagnoseRedactsEveryStringOfAReviewRound(t *testing.T) {
	const secret = "ghp_test0123456789abcdef"
	t.Setenv("FUGARO_BITBUCKET_TOKEN", secret)
	f := newCloudFixture(t)
	const id = "20260101-000032-abcd"
	head := strings.Repeat("a", 40)
	seedReviewersRun(t, f, id, []runstore.ReviewSummary{
		{Round: 1, Tier: runstore.TierAdversarial, Verdict: "ship " + secret,
			Findings: 1, Head: head, Model: "model " + secret, Effort: "effort " + secret},
		// A tier that is no longer one of the three once it is redacted: the
		// block's own label falls back to the outer reviewer, which is what it
		// does for any tier this build does not know.
		{Round: 2, Tier: runstore.TierSenior + " " + secret, Verdict: "changes " + secret, Findings: 2},
	})
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, secret) {
		t.Fatalf("the Reviews block prints the secret:\n%s", human)
	}
	for _, want := range []string{
		"  round 1 adversarial: ship [REDACTED], 1 finding (recorded: model model [REDACTED], effort effort [REDACTED], at " + head[:7] + ")\n",
		"  round 2 senior: changes [REDACTED], 2 findings\n",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("diagnose does not print %q:\n%s", want, human)
		}
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
	if len(d.Reviews) != 2 {
		t.Fatalf("reviews = %+v, want two entries", d.Reviews)
	}
	if got := d.Reviews[0]; got.Tier != runstore.TierAdversarial || got.Verdict != "ship [REDACTED]" ||
		got.Model != "model [REDACTED]" || got.Effort != "effort [REDACTED]" || got.Head != head {
		t.Errorf("the first round = %+v", got)
	}
	if got := d.Reviews[1]; got.Tier != "senior [REDACTED]" || got.Verdict != "changes [REDACTED]" {
		t.Errorf("the second round's enums read tier %q, verdict %q, want both redacted", got.Tier, got.Verdict)
	}
}

// TestDiagnosePrintsEachLevelsRoundsAndCap: the document carries, per review
// level, the rounds that level's own loop reached and the cap the run's plan
// resolved for it (V1.3) -- the figures the Run screen's iterations bar is drawn
// from, and the same figures the human output prints. Each level's loop numbers
// its rounds from 1, so the rounds a level spent are the highest number its own
// reviews carry and never a count of them.
func TestDiagnosePrintsEachLevelsRoundsAndCap(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000033-abcd"
	seedPlannedRun(t, f, id,
		[]runstore.ReviewSummary{
			{Round: 1, Tier: runstore.TierFirst, Verdict: "changes", Findings: 1},
			{Round: 2, Tier: runstore.TierFirst, Verdict: "ship"},
			{Round: 1, Tier: runstore.TierSenior, Verdict: "changes", Findings: 2},
			{Round: 2, Tier: runstore.TierSenior, Verdict: "ship"},
			{Round: 1, Tier: runstore.TierAdversarial, Verdict: "ship"},
		},
		&runstore.PlanRecord{Steps: []runstore.PlanStepRecord{
			{Kind: "first_line", Rounds: 3},
			{Kind: "review", Rounds: 2},
			{Kind: "adversarial", Rounds: 2},
		}})
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\nReview levels\n",
		"  level l1: 2 of 3 rounds\n",
		"  level l2: 2 of 2 rounds\n",
		"  level l3: 1 of 2 rounds\n",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("diagnose does not print %q:\n%s", want, human)
		}
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	levels, _ := doc["review_levels"].([]any)
	if len(levels) != 3 {
		t.Fatalf("review_levels = %v, want one entry per level", doc["review_levels"])
	}
	if got := keysOfDoc(levels[0].(map[string]any)); !slices.Equal(got, []string{"cap", "level", "rounds"}) {
		t.Errorf("a level's keys = %v, want level, rounds and cap", got)
	}
	for i, want := range []struct {
		level  string
		rounds float64
		cap    float64
	}{{"l1", 2, 3}, {"l2", 2, 2}, {"l3", 1, 2}} {
		entry := levels[i].(map[string]any)
		if entry["level"] != want.level || entry["rounds"] != want.rounds || entry["cap"] != want.cap {
			t.Errorf("review_levels[%d] = %v, want %s at %v of %v", i, entry, want.level, want.rounds, want.cap)
		}
	}
}

// TestDiagnosePrintsANullCapWithoutAPlan: a record with no plan block carries no
// cap for any level, and the document says so with a null rather than a number
// nothing set (V1.3: a cap it cannot resolve prints null, never a guess) -- and
// with the sentence the human output prints in its place. A record with no plan
// and no review carries no block at all, so a reader can tell it from one that
// carries levels and no caps.
func TestDiagnosePrintsANullCapWithoutAPlan(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260101-000034-abcd"
	seedReviewersRun(t, f, id, []runstore.ReviewSummary{
		{Round: 1, Tier: runstore.TierSenior, Verdict: "ship"},
		{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 1},
	})
	human, _, err := execute(t, "diagnose", id)
	if err != nil {
		t.Fatal(err)
	}
	if want := "  level l2: 2 rounds, no cap recorded\n"; !strings.Contains(human, want) {
		t.Errorf("diagnose does not print %q:\n%s", want, human)
	}
	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	levels, _ := doc["review_levels"].([]any)
	if len(levels) != 1 {
		t.Fatalf("review_levels = %v, want the one level the record's reviews reach", doc["review_levels"])
	}
	entry := levels[0].(map[string]any)
	if entry["level"] != "l2" || entry["rounds"] != float64(2) {
		t.Errorf("review_levels[0] = %v, want l2 at 2 rounds", entry)
	}
	if cap, ok := entry["cap"]; !ok || cap != nil {
		t.Errorf("cap = %v (present %v), want a null: a record with no plan block resolves no cap", cap, ok)
	}
	// A record that carries neither prints no block, so a reader can tell the
	// two apart.
	const empty = "20260101-000035-abcd"
	seedReviewersRun(t, f, empty, nil)
	out, _, err = execute(t, "diagnose", "--json", empty)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "review_levels") {
		t.Errorf("a record with no plan and no reviews printed the block:\n%s", out)
	}
}

// seedReviewersRun stores a finished run under id whose record carries the
// given review rounds and nothing else the diagnosis reads.
func seedReviewersRun(t *testing.T, f *cloudFixture, id string, reviews []runstore.ReviewSummary) {
	t.Helper()
	seedPlannedRun(t, f, id, reviews, nil)
}

// seedPlannedRun is seedReviewersRun with the record's plan block, which is what
// a run's review caps are read off (V1.3): nil for a record written before the
// block existed, which is what a reader without one has to fall back to.
func seedPlannedRun(t *testing.T, f *cloudFixture, id string, reviews []runstore.ReviewSummary, plan *runstore.PlanRecord) {
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
		Reviews: reviews, Plan: plan}); err != nil {
		t.Fatal(err)
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

// TestDiagnoseShowsTheChainForAHaltedRun: a halted run's first question is how
// much of its chain's limits is left (D1055), so diagnose prints the block
// `runs show` prints — the chain, each dimension's limit, used and remaining,
// the source of each limit and whether the chain is enforced — beside the halt
// that raises the question. A run that did not halt gets no block: nothing
// stopped it, so there is no halt for the block to answer.
func TestDiagnoseShowsTheChainForAHaltedRun(t *testing.T) {
	f := showRig(t)
	rec := showRecord(t, f, showTip)
	rec.Status, rec.Halt = runstore.StatusHalted, &showHalt
	writeRecord(t, f, showTip, rec)

	out, errOut, err := execute(t, "diagnose", showTip)
	if err != nil {
		t.Fatalf("diagnose: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "Halted:   run_cap") {
		t.Fatalf("diagnose does not name the halt:\n%s", out)
	}
	if !strings.Contains(out, showRoot) || !strings.Contains(out, "3 runs, ENFORCED") {
		t.Errorf("diagnose does not print the chain the halted run is in:\n%s", out)
	}
	if got, want := showRow(t, out, "usd"), "4 dollars 2.25 dollars 1.75 dollars chain"; got != want {
		t.Errorf("usd row = %q, want %q\n%s", got, want, out)
	}

	js, errOut, err := execute(t, "diagnose", "--json", showTip)
	if err != nil {
		t.Fatalf("diagnose --json: %v (%s)", err, errOut)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatal(err)
	}
	if d.ChainLimits == nil || d.ChainLimits.Chain != showRoot || d.ChainLimits.RunCount != 3 || !d.ChainLimits.Enforced || len(d.ChainLimits.Limits) != 4 {
		t.Fatalf("json chain = %+v, want the chain %s and its four limits", d.ChainLimits, showRoot)
	}
	if d.ChainLimits.Limits[0].Source != policy.SourceChain {
		t.Errorf("json limits[0].source = %q, want %q", d.ChainLimits.Limits[0].Source, policy.SourceChain)
	}

	plain, _, err := execute(t, "diagnose", showMid)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "Chain:") {
		t.Errorf("a run that did not halt prints a chain block:\n%s", plain)
	}
}

// The run every message test here diagnoses: one run of acme/app, whose record
// carries the block a test hands in and whose message objects are the ones a
// test writes by hand — the launcher's own write is what puts a message under
// tokens/, and the store is not asked, so a test can build the shapes a
// hand-written object has.
const diagnoseMsgRun = "20261119-054111-abcd"

// diagnoseMsgSent is when every message here was written.
var diagnoseMsgSent = time.Date(2026, 11, 19, 5, 41, 11, 0, time.UTC)

// diagnoseMsgRig seeds that run with msgs as its record's messages block and
// objects as the message objects under its prefix. The record's own pull
// request is 99 and deliberately not whatever a test's code host answers: the
// line a diagnosis prints must take its number from the host, never from
// anything under runs/ (F19, F20).
func diagnoseMsgRig(t *testing.T, msgs *runmsg.Record, objects ...runmsg.Message) string {
	t.Helper()
	f := newCloudFixture(t)
	exec := seedRun(t, f, diagnoseMsgRun, "", "dana@example.com", true)
	rec := prRecord(diagnoseMsgRun, exec, 99, 1.5)
	rec.Messages = msgs
	writeRecord(t, f, diagnoseMsgRun, rec)
	if len(objects) == 0 {
		return diagnoseMsgRun
	}
	ctx := context.Background()
	b, err := blobx.Open(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, m := range objects {
		m.Version = runmsg.Format
		if m.SentAt.IsZero() {
			m.SentAt = diagnoseMsgSent
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Create(ctx, runmsg.Path(appSlug, diagnoseMsgRun, m.Seq), raw, "application/json"); err != nil {
			t.Fatal(err)
		}
	}
	return diagnoseMsgRun
}

// diagnoseMsgBlock is the message block of a diagnosis of id, failing the test
// when the command carries none.
func diagnoseMsgBlock(t *testing.T, id string) *MessagesDoc {
	t.Helper()
	out, errOut, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatalf("diagnose --json: %v (%s)", err, errOut)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if d.Messages == nil {
		t.Fatalf("a diagnosis of a run with messages carries no block:\n%s", out)
	}
	return d.Messages
}

// diagnoseMsgFate is one message's entry of a decoded block.
func diagnoseMsgFate(t *testing.T, doc *MessagesDoc, seq int) MessageFate {
	t.Helper()
	for _, f := range doc.Messages {
		if f.Seq == seq {
			return f
		}
	}
	t.Fatalf("the block has no entry for message %d: %+v", seq, doc.Messages)
	return MessageFate{}
}

// undeliveredBlock is the record's block for one message the run never carried:
// the run ended before a coder stage could reach it (D2944, rule 5), so the
// record names the fate and the object under tokens/ still holds the words.
func undeliveredBlock() *runmsg.Record {
	return &runmsg.Record{Accepting: true, Format: runmsg.Format, Flags: []string{runmsg.FlagNow},
		Messages: []runmsg.Delivery{{Seq: 1, SentAt: &diagnoseMsgSent, SentBy: "dana@example.com",
			State: runmsg.StateUndelivered, Reason: "the run ended before a coder stage carried it"}}}
}

// diagnoseHostStub installs the one code-host read a diagnosis makes (F19) over
// one fake host holding prs: the provider kind is resolved from this machine's
// config for real, and the read is the fake's, which is the network no test
// has.
func diagnoseHostStub(t *testing.T, prs ...fake.PRState) *fake.Provider {
	t.Helper()
	p := &fake.Provider{Repo: "acme/app", State: fake.State{PRs: prs}}
	was := openDiagnoseProvider
	openDiagnoseProvider = func([]string, func(string)) (string, gitprov.Opener, error) {
		return "", gitprov.Static(p), nil
	}
	t.Cleanup(func() { openDiagnoseProvider = was })
	return p
}

// diagnoseNoHost installs that read as one this machine cannot make at all: no
// credential for the repository, which is the state the printed line has to
// survive (F19).
func diagnoseNoHost(t *testing.T) {
	t.Helper()
	was := openDiagnoseProvider
	openDiagnoseProvider = func([]string, func(string)) (string, gitprov.Opener, error) {
		return "", nil, errors.New("no credential on this machine for acme/app")
	}
	t.Cleanup(func() { openDiagnoseProvider = was })
}

// TestDiagnoseMessages: every fate the run's record holds is on the diagnosis,
// in number order, under the line that says whose account it is — the record is
// written by the run, whose agent can rewrite it, so it is read as what the run
// recorded and not as proof (F20, F13) — and in the text and in --json alike. A
// run with nothing to say about messages is unchanged: no section, and no
// member in the document.
func TestDiagnoseMessages(t *testing.T) {
	delivered := diagnoseMsgSent.Add(2 * time.Minute)
	diagnoseMsgRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format, Flags: []string{runmsg.FlagNow},
		Messages: []runmsg.Delivery{
			{Seq: 1, SentAt: &diagnoseMsgSent, SentBy: "dana@example.com", Flags: []string{runmsg.FlagNow},
				State: runmsg.StateDelivered, Stage: "implement", DeliveredAt: &delivered},
			{Seq: 2, SentAt: &diagnoseMsgSent, SentBy: "dana@example.com",
				State: runmsg.StateRefused, Reason: "the object is not a message this build reads"},
		}})

	out, errOut, err := execute(t, "diagnose", diagnoseMsgRun)
	if err != nil {
		t.Fatalf("diagnose: %v (%s)", err, errOut)
	}
	for _, want := range []string{
		"Messages (as the run recorded them)",
		"#1 delivered in implement by dana@example.com (now)",
		"#2 refused by dana@example.com: the object is not a message this build reads",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("diagnose lacks %q:\n%s", want, out)
		}
	}

	doc := diagnoseMsgBlock(t, diagnoseMsgRun)
	if !doc.Accepting || doc.Format != runmsg.Format || strings.Join(doc.Flags, ",") != runmsg.FlagNow {
		t.Errorf("block = %+v, want the run accepting format %d with the flags it honours", doc, runmsg.Format)
	}
	one := diagnoseMsgFate(t, doc, 1)
	if one.State != runmsg.StateDelivered || !one.Recorded || one.Stage != "implement" || one.SentBy != "dana@example.com" {
		t.Errorf("message 1 = %+v, want the fate the record states", one)
	}
	if one.DeliveredAt == nil || !one.DeliveredAt.Equal(delivered) {
		t.Errorf("message 1 = %+v, want it delivered at %s", one, delivered)
	}
	// The object is gone for both — neither was written under tokens/ — and the
	// block says so rather than leaving the member out: this document looked.
	if one.Object == nil || *one.Object {
		t.Errorf("message 1's object = %v, want the bucket's own answer that it is not there", one.Object)
	}
	two := diagnoseMsgFate(t, doc, 2)
	if two.State != runmsg.StateRefused || two.Reason == "" {
		t.Errorf("message 2 = %+v, want the refusal and the record's reason", two)
	}

	t.Run("a run with no messages is unchanged", func(t *testing.T) {
		f := newCloudFixture(t)
		seedRun(t, f, diagnoseMsgRun, "", "dana@example.com", true)
		writeRecord(t, f, diagnoseMsgRun, prRecord(diagnoseMsgRun, "", 99, 1.5))
		text, _, err := execute(t, "diagnose", diagnoseMsgRun)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "Messages") {
			t.Errorf("a run whose record carries no messages block prints one:\n%s", text)
		}
		js, _, err := execute(t, "diagnose", "--json", diagnoseMsgRun)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(js, `"messages"`) {
			t.Errorf("a run whose record carries no messages block carries one in its document:\n%s", js)
		}
	})

	t.Run("a run that reads no messages says so", func(t *testing.T) {
		diagnoseMsgRig(t, &runmsg.Record{Accepting: false, Format: runmsg.Format})
		text, _, err := execute(t, "diagnose", diagnoseMsgRun)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "this run reads no messages: accepting false in the run's record") {
			t.Errorf("a run that reads no messages does not say so:\n%s", text)
		}
		// The channel is closed, so nothing reaching the run is a state to
		// report: the closed channel is the whole of it.
		if strings.Contains(text, "no message reached this run") {
			t.Errorf("a run that reads no messages reads as one that was merely sent none:\n%s", text)
		}
	})

	t.Run("a record with more entries than a run reads", func(t *testing.T) {
		// No runner writes past the channel's cap, so this is a record someone
		// wrote by hand; the list stops at the cap and says it is short rather
		// than printing an unbounded one (F12).
		full := &runmsg.Record{Accepting: true, Format: runmsg.Format}
		for seq := 1; seq <= runmsg.MaxMessages+1; seq++ {
			full.Messages = append(full.Messages, runmsg.Delivery{Seq: seq, State: runmsg.StateDelivered})
		}
		diagnoseMsgRig(t, full)
		text, _, err := execute(t, "diagnose", diagnoseMsgRun)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "more messages are there than are listed") {
			t.Errorf("a list that stopped at the cap does not say it is short:\n%s", text)
		}
		doc := diagnoseMsgBlock(t, diagnoseMsgRun)
		if !doc.Truncated || len(doc.Messages) != runmsg.MaxMessages {
			t.Errorf("block holds %d messages (truncated %v), want the channel's %d and the note that it is short", len(doc.Messages), doc.Truncated, runmsg.MaxMessages)
		}
	})

	t.Run("a run that reads messages and was sent none", func(t *testing.T) {
		diagnoseMsgRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format})
		text, _, err := execute(t, "diagnose", diagnoseMsgRun)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "no message reached this run") {
			t.Errorf("a run that reads messages and was sent none does not say so:\n%s", text)
		}
	})
}

// TestDiagnoseListsUnreadMessages: the run's own record is not the whole story.
// A launcher can write a message object under the run's prefix at any moment —
// the runner's last poll is at finalize (F18) — so a number the record never
// mentions is one the run never read, and a number past the channel's cap is
// one no run reads however it was written (F12). The diagnosis lists both, from
// the prefix and from the objects themselves.
func TestDiagnoseListsUnreadMessages(t *testing.T) {
	diagnoseHostStub(t, followUpPR(12, "fugaro/"+diagnoseMsgRun, gitprov.PROpen))
	diagnoseMsgRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format, Messages: []runmsg.Delivery{
		{Seq: 1, SentAt: &diagnoseMsgSent, SentBy: "dana@example.com", State: runmsg.StateDelivered, Stage: "implement"},
	}}, runmsg.Message{Seq: 3, Text: "the flaky test is known"}, runmsg.Message{Seq: 21, Text: "past the cap"})

	out, errOut, err := execute(t, "diagnose", diagnoseMsgRun)
	if err != nil {
		t.Fatalf("diagnose: %v (%s)", err, errOut)
	}
	for _, want := range []string{
		"#3 never read: the run's record does not mention it",
		"#21 never read: over the cap",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("diagnose lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "fugaro run --pr 12 --repo acme/app -- 'the flaky test is known'") {
		t.Errorf("the message the run never read has no line to send it again:\n%s", out)
	}

	doc := diagnoseMsgBlock(t, diagnoseMsgRun)
	unread := diagnoseMsgFate(t, doc, 3)
	if unread.Recorded || unread.State != "" || unread.Object == nil || !*unread.Object || unread.OverCap {
		t.Errorf("message 3 = %+v, want an object the record never mentions", unread)
	}
	if unread.Text != "the flaky test is known" || !strings.Contains(unread.Line, "--pr 12") {
		t.Errorf("message 3 = %+v, want the object's own text and the host's number", unread)
	}
	// A number past the cap is never read, so its object is listed and its text
	// is not: the diagnosis says the run cannot read it rather than quoting
	// what it holds (F12).
	over := diagnoseMsgFate(t, doc, 21)
	if !over.OverCap || over.Recorded || over.Text != "" || over.Line != "" {
		t.Errorf("message 21 = %+v, want it listed as past the cap with nothing read from it", over)
	}
	if len(doc.Messages) != 3 {
		t.Errorf("the block holds %d messages, want the record's one and the two objects:\n%+v", len(doc.Messages), doc.Messages)
	}
	// Every entry is in number order, which is the order a listing sorts in and
	// the order the runner numbers in.
	for i, f := range doc.Messages {
		if i > 0 && doc.Messages[i-1].Seq >= f.Seq {
			t.Errorf("the block is not in number order: %+v", doc.Messages)
			break
		}
	}
}

// TestDiagnoseLineFromObjectsAndHost: a message that never reached the agent is
// the one a person wants back, and the line that sends it again is built from
// the three places a run cannot write (F19): the text out of the message object
// under tokens/, the repository this machine's config names, and the number of
// the open pull request the code host reports for the run's branch. The record
// here names pull request 99 and the host answers 12, so a line read off the
// record at all — the failure F19 exists to prevent — reads differently.
func TestDiagnoseLineFromObjectsAndHost(t *testing.T) {
	t.Run("an open pull request of the run's branch", func(t *testing.T) {
		diagnoseHostStub(t, followUpPR(12, "fugaro/"+diagnoseMsgRun, gitprov.PROpen))
		diagnoseMsgRig(t, undeliveredBlock(), runmsg.Message{Seq: 1, Text: "use the helper in parser.go"})

		out, errOut, err := execute(t, "diagnose", diagnoseMsgRun)
		if err != nil {
			t.Fatalf("diagnose: %v (%s)", err, errOut)
		}
		want := "fugaro run --pr 12 --repo acme/app -- 'use the helper in parser.go'"
		if !strings.Contains(out, want) {
			t.Errorf("diagnose lacks the line %q:\n%s", want, out)
		}
		if !strings.Contains(out, "text:\n      use the helper in parser.go") {
			t.Errorf("the message's own text is not printed under its fate:\n%s", out)
		}
		if strings.Contains(out, "--pr 99") {
			t.Errorf("the line names the record's pull request:\n%s", out)
		}

		doc := diagnoseMsgBlock(t, diagnoseMsgRun)
		f := diagnoseMsgFate(t, doc, 1)
		if f.State != runmsg.StateUndelivered || !f.Recorded {
			t.Fatalf("message 1 = %+v, want the record's undelivered message", f)
		}
		if f.Text != "use the helper in parser.go" || f.Line != want || f.LineNote != "" {
			t.Errorf("message 1 = %+v, want the object's text and the line %q", f, want)
		}
	})

	// A merged or closed pull request is one PullRequestByBranch answers too,
	// and a message sent again under it would follow up a pull request that is
	// over: the line is printed without a number and the note says why.
	t.Run("a merged pull request is not a number to name", func(t *testing.T) {
		diagnoseHostStub(t, followUpPR(12, "fugaro/"+diagnoseMsgRun, gitprov.PRMerged))
		diagnoseMsgRig(t, undeliveredBlock(), runmsg.Message{Seq: 1, Text: "use the helper in parser.go"})

		out, _, err := execute(t, "diagnose", diagnoseMsgRun)
		if err != nil {
			t.Fatal(err)
		}
		want := "fugaro run --repo acme/app -- 'use the helper in parser.go'"
		if !strings.Contains(out, want) || strings.Contains(out, "fugaro run --pr") {
			t.Errorf("a merged pull request is named as a number to follow up:\n%s", out)
		}
		if !strings.Contains(out, "no open pull request of acme/app is on branch fugaro/"+diagnoseMsgRun) {
			t.Errorf("the line says nothing about why it has no number:\n%s", out)
		}

		f := diagnoseMsgFate(t, diagnoseMsgBlock(t, diagnoseMsgRun), 1)
		if f.Line != want || f.LineNote == "" || f.Text != "use the helper in parser.go" {
			t.Errorf("message 1 = %+v, want the line without a number and the reason", f)
		}
	})
}

// TestDiagnoseNoHostNoNumber: when this machine cannot ask the code host at all
// — no credential for the repository — the diagnosis still prints the text and
// the line, without a number, and says why (F19). Nothing about the run fails
// for it: the line is one part of the diagnosis that could not be built.
func TestDiagnoseNoHostNoNumber(t *testing.T) {
	diagnoseNoHost(t)
	diagnoseMsgRig(t, undeliveredBlock(), runmsg.Message{Seq: 1, Text: "use the helper in parser.go"})

	out, errOut, err := execute(t, "diagnose", diagnoseMsgRun)
	if err != nil {
		t.Fatalf("diagnose: %v (%s)", err, errOut)
	}
	want := "fugaro run --repo acme/app -- 'use the helper in parser.go'"
	if !strings.Contains(out, want) {
		t.Errorf("diagnose lacks the line %q:\n%s", want, out)
	}
	if strings.Contains(out, "fugaro run --pr") {
		t.Errorf("a line with no host to ask carries a number:\n%s", out)
	}
	if !strings.Contains(out, "no --pr: the code host could not be asked") {
		t.Errorf("the line does not say why it has no number:\n%s", out)
	}

	f := diagnoseMsgFate(t, diagnoseMsgBlock(t, diagnoseMsgRun), 1)
	if f.Line != want || !strings.HasPrefix(f.LineNote, "the code host could not be asked") || f.Text != "use the helper in parser.go" {
		t.Errorf("message 1 = %+v, want the line without a number and the reason", f)
	}
}

// TestDiagnoseTextExpired: the bucket ages a message object out after its seven
// days (F11), and what is left of an undelivered message then is the record's
// fate. The diagnosis says the text expired rather than leaving the entry
// looking like one whose words are still there, and there is no line: the text
// a line would carry is exactly what is gone.
func TestDiagnoseTextExpired(t *testing.T) {
	diagnoseMsgRig(t, undeliveredBlock())

	out, errOut, err := execute(t, "diagnose", diagnoseMsgRun)
	if err != nil {
		t.Fatalf("diagnose: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "#1 undelivered by dana@example.com") {
		t.Errorf("the record's fate is not printed:\n%s", out)
	}
	if !strings.Contains(out, "text expired") {
		t.Errorf("a message whose object is gone does not say so:\n%s", out)
	}
	if strings.Contains(out, "fugaro run") {
		t.Errorf("a message whose text is gone still prints a line:\n%s", out)
	}

	f := diagnoseMsgFate(t, diagnoseMsgBlock(t, diagnoseMsgRun), 1)
	if !f.Expired || f.Text != "" || f.Line != "" {
		t.Errorf("message 1 = %+v, want it expired with nothing left to send", f)
	}
	if f.Object == nil || *f.Object {
		t.Errorf("message 1's object = %v, want the bucket's own answer that it is gone", f.Object)
	}
	// A message the run delivered has no text to lose, so the fate stands
	// without the word: expiry is about what is left to send again.
	diagnoseMsgRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format, Messages: []runmsg.Delivery{
		{Seq: 1, SentAt: &diagnoseMsgSent, SentBy: "dana@example.com", State: runmsg.StateDelivered, Stage: "implement"}}})
	delivered, _, err := execute(t, "diagnose", diagnoseMsgRun)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(delivered, "text expired") {
		t.Errorf("a delivered message whose object is gone reads as one whose text expired:\n%s", delivered)
	}
}

// keysOfDoc is a decoded document object's own keys, sorted, for the
// field-by-field comparisons above: the set is what a rename moves.
func keysOfDoc(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}
