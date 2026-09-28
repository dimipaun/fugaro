package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
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
