package runner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

func TestParseVerdict(t *testing.T) {
	structured := agent.Result{Structured: json.RawMessage(`{"verdict":"changes","findings":[{"summary":"x"}]}`)}
	if v := ParseVerdict(structured); v.Verdict != "changes" || len(v.Findings) != 1 {
		t.Errorf("structured = %+v", v)
	}
	fenced := agent.Result{Text: "Looks good.\n```json\n{\"verdict\":\"ship\",\"findings\":[]}\n```\n"}
	if v := ParseVerdict(fenced); v.Verdict != "ship" {
		t.Errorf("fenced = %+v", v)
	}
	for _, bad := range []agent.Result{{Text: "no verdict here"}, {Structured: json.RawMessage(`{"verdict":"maybe"}`)}} {
		v := ParseVerdict(bad)
		if v.Verdict != "changes" || len(v.Findings) != 1 || !strings.Contains(v.Findings[0].Summary, "no parseable verdict") {
			t.Errorf("unparseable %+v gave %+v", bad, v)
		}
	}
}

func TestDecide(t *testing.T) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	fail := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true}
	dirty := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", Passed: true}
	old := verify.Record{Kind: verify.KindTest, HeadSHA: "old", CleanTree: true, Passed: true}
	build := verify.Record{Kind: verify.KindBuild, HeadSHA: "abc", CleanTree: true, Passed: true}
	ship := &runstore.ReviewSummary{Round: 1, Verdict: "ship"}
	changes := &runstore.ReviewSummary{Round: 2, Verdict: "changes", Findings: 3}
	cases := []struct {
		name    string
		records []verify.Record
		last    *runstore.ReviewSummary
		ready   bool
		reason  string
	}{
		{"ready", []verify.Record{pass}, ship, true, ""},
		{"latest matching run wins", []verify.Record{pass, fail}, ship, false, "tests failing on the final commit"},
		{"fixed after failing", []verify.Record{fail, pass}, ship, true, ""},
		{"dirty tree does not count", []verify.Record{dirty}, ship, false, "no verified test run on the final commit"},
		{"old commit does not count", []verify.Record{old}, ship, false, "no verified test run on the final commit"},
		{"build is not test", []verify.Record{build}, ship, false, "no verified test run on the final commit"},
		{"review findings", []verify.Record{pass}, changes, false, "review round 2 still has 3 findings"},
		{"no review", []verify.Record{pass}, nil, false, "no review verdict"},
	}
	for _, tc := range cases {
		ready, reason := Decide(tc.records, "abc", tc.last)
		if ready != tc.ready || reason != tc.reason {
			t.Errorf("%s: Decide = %v %q, want %v %q", tc.name, ready, reason, tc.ready, tc.reason)
		}
	}
}

func TestPrompts(t *testing.T) {
	sys := SystemPrompt(PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/work/state"}, "Use tabs.")
	for _, want := range []string{"fugaro/x", "main", "/work/state/pr.md", "fugaro verify test", "--rerun-failed", "Use tabs."} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	if got := ReviewPrompt("/review", "", "main"); !strings.HasPrefix(got, "/review ") || !strings.Contains(got, "origin/main") {
		t.Errorf("skill review prompt = %q", got)
	}
	if got := ReviewPrompt("", "Check the SQL.", "develop"); !strings.HasPrefix(got, "Check the SQL.") || !strings.Contains(got, "origin/develop") {
		t.Errorf("file review prompt = %q", got)
	}
	if got := ReviewPrompt("", "", "main"); !strings.Contains(got, "correctness bugs") {
		t.Errorf("default review prompt = %q", got)
	}
	fix := FixPrompt(Verdict{Verdict: "changes", Findings: []Finding{{Severity: "major", File: "a.go", Summary: "nil deref"}, {Summary: "add a test"}}})
	if !strings.Contains(fix, "- [major] a.go: nil deref\n- add a test\n") {
		t.Errorf("fix prompt = %q", fix)
	}
}

func TestReport(t *testing.T) {
	rec := &runstore.Record{
		RunID: "20260926-221530-abcd", Outcome: runstore.OutcomeDraft, Reason: "tests failing on the final commit",
		HeadSHA: "abcdef1234567", CostUSD: 4.126,
		Stages:  []runstore.StageTiming{{Name: "implement", DurationS: 61}},
		Reviews: []runstore.ReviewSummary{{Round: 1, Verdict: "changes", Findings: 1}, {Round: 2, Verdict: "ship"}},
		Verify:  []verify.Record{{Kind: verify.KindTest, HeadSHA: "abcdef1234567", CleanTree: true, Tests: 3, Failures: 1, Flaky: []string{"pkg.A.b"}}},
	}
	got := Report(rec, "runs/acme-app/20260926-221530-abcd/", nil)
	for _, want := range []string{"draft — tests failing on the final commit", "| implement | 1m1s |", "round 1: changes (1 finding)", "round 2: ship", "failed on abcdef1 (3 tests, 1 failure, flaky: pkg.A.b)", "$4.13", "runs/acme-app/20260926-221530-abcd/"} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}

func TestReportConsistentWithDecide(t *testing.T) {
	// Test that report and outcome agree: clean-failed followed by dirty-passed
	// on the same SHA. Report should say "failed", Decide should say "tests failing".
	cleanFailed := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: false, Tests: 2, Failures: 1}
	dirtyPassed := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: false, Passed: true, Tests: 2}
	ship := &runstore.ReviewSummary{Round: 1, Verdict: "ship"}

	rec := &runstore.Record{
		RunID:   "20260927-000000-xxxx",
		Outcome: runstore.OutcomeDraft,
		Reason:  "tests failing on the final commit",
		HeadSHA: "abc",
		Verify:  []verify.Record{cleanFailed, dirtyPassed},
	}

	// Decide should report tests failing (uses clean record)
	ready, reason := Decide(rec.Verify, rec.HeadSHA, ship)
	if ready || reason != "tests failing on the final commit" {
		t.Errorf("Decide = %v %q, want false %q", ready, reason, "tests failing on the final commit")
	}

	// Report should also say failed (uses clean record via latestVerifiedTest)
	got := Report(rec, "location/", nil)
	if !strings.Contains(got, "failed on abc (2 tests, 1 failure)") {
		t.Errorf("report should say failed, got:\n%s", got)
	}
}

func TestReportLogTail(t *testing.T) {
	rec := &runstore.Record{RunID: "20260927-000000-abcd", Outcome: runstore.OutcomeDraft, Reason: "stage implement failed: boom"}
	got := Report(rec, "loc/", &LogTail{Source: "stage implement-1, stderr", Lines: []string{"a ``` b", "boom"}})
	// The fence is longer than any backtick run in the tail, so the tail
	// cannot close the code block early.
	want := "**Log tail** (stage implement-1, stderr):\n\n````text\na ``` b\nboom\n````\n\n"
	if !strings.Contains(got, want) {
		t.Fatalf("report lacks %q:\n%s", want, got)
	}
	if strings.Contains(Report(rec, "loc/", nil), "Log tail") {
		t.Fatal("a nil tail rendered a section")
	}
}

func TestCostLine(t *testing.T) {
	cases := map[string]runstore.Cost{
		"**Cost:** ≈ $4.50 (model $4.12 + compute $0.38, estimate)":                                           runstore.NewCost(4.12, 0.38, runstore.BasisAPIList),
		"**Cost:** ≈ $0.38 compute (estimate); model $4.12 notional, counted against the Claude subscription": runstore.NewCost(4.12, 0.38, runstore.BasisSubscription),
		"**Cost:** model $4.12 (compute not estimated)":                                                       runstore.ModelOnlyCost(4.12, runstore.BasisAPIList),
		"**Cost:** model $4.12 notional, counted against the Claude subscription (compute not estimated)":     runstore.ModelOnlyCost(4.12, runstore.BasisSubscription),
		// An estimated compute figure of zero is shown as an estimate, not as "not estimated".
		"**Cost:** ≈ $4.12 (model $4.12 + compute $0.00, estimate)": runstore.NewCost(4.12, 0, runstore.BasisAPIList),
	}
	for want, c := range cases {
		if got := CostLine(c); got != want {
			t.Errorf("CostLine(%+v) = %q, want %q", c, got, want)
		}
	}
}

// TestReportCarriesMarker: the report ends with the run's marker, which
// names the run that posted it.
func TestReportCarriesMarker(t *testing.T) {
	rec := &runstore.Record{RunID: "20260926-221530-abcd", Outcome: runstore.OutcomeReady}
	got := Report(rec, "loc/", nil)
	if !strings.HasSuffix(got, "\n"+gitprov.ReportMarker(rec.RunID)+"\n") {
		t.Fatalf("report does not end with the marker:\n%s", got)
	}
	if id, ok := gitprov.FugaroRun(got); !ok || id != rec.RunID {
		t.Fatalf("FugaroRun(report) = %q, %v", id, ok)
	}
}

func TestPromptWorkflowRule(t *testing.T) {
	d := PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/s"}
	if got := SystemPrompt(d, ""); strings.Contains(got, ".github/workflows") {
		t.Errorf("a prompt without NoWorkflows names workflow files: %s", got)
	}
	d.NoWorkflows = true
	got := SystemPrompt(d, "")
	if !strings.Contains(got, ".github/workflows/") || strings.Count(got, ".github/workflows") != 1 {
		t.Errorf("the rule is missing or repeated: %s", got)
	}
}
