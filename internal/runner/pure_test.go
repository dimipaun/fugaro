package runner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
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
		Verify:  []verify.Record{{Kind: verify.KindTest, HeadSHA: "abcdef1234567", Tests: 3, Failures: 1, Flaky: []string{"pkg.A.b"}}},
	}
	got := Report(rec, "runs/acme-app/20260926-221530-abcd/")
	for _, want := range []string{"draft — tests failing on the final commit", "| implement | 1m1s |", "round 1: changes (1 finding)", "round 2: ship", "failed on abcdef1 (3 tests, 1 failure, flaky: pkg.A.b)", "$4.13", "runs/acme-app/20260926-221530-abcd/"} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}
