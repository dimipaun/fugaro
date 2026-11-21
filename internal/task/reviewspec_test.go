package task

import (
	"encoding/json"
	"strings"
	"testing"
)

// reviewTask is a launch whose shape admits a review_spec: a mode: review
// task names its pull requests, so it needs no task text of its own
// (Spec.Validate's "task is required" rule).
func reviewTask() *Spec {
	return &Spec{Version: 1, RunID: "20261119-120000-a1b2", Repo: "acme/app", Ref: "main", ReviewPRs: []int{7}}
}

// TestReviewSpecValidate: the review_spec block's own rules (R2), and the two
// fields beside it (from_findings, own_text). Every row below is refused at
// both readers — Spec.Validate here and the schema corpus in testdata/task —
// so a block the launcher accepts is never one the review job refuses at
// bootstrap.
//
// Mutation (drop the ReviewSpecFile case from ReviewSpecProblem): the
// file-with-a-run-id row is accepted, and a task whose spec claims to be a
// person's own file while naming a run the reviewer would read as its source
// reaches the prompt.
func TestReviewSpecValidate(t *testing.T) {
	const rootRun = "20261118-090000-abcd"
	for _, tc := range []struct {
		name string
		rs   *ReviewSpec
	}{
		{"no block at all", nil},
		{"a run source", &ReviewSpec{Source: ReviewSpecRun, Run: rootRun, Text: "Add a health endpoint."}},
		{"a run source with a follow-up and a plan task", &ReviewSpec{
			Source:   ReviewSpecRun,
			Run:      rootRun,
			Text:     "Add a health endpoint.",
			Followup: "Also rename the flag.",
			PlanTask: "setup-wizard/W1.3",
		}},
		{"a file source", &ReviewSpec{Source: ReviewSpecFile, Text: "Trim the retry window to 30s."}},
		{"a none source with a reason", &ReviewSpec{Source: ReviewSpecNone, Reason: "--code-only"}},
		{"a text exactly at the cap", &ReviewSpec{Source: ReviewSpecRun, Run: rootRun, Text: strings.Repeat("x", MaxReviewSpecBytes)}},
		{"a followup exactly at the cap", &ReviewSpec{Source: ReviewSpecRun, Run: rootRun, Followup: strings.Repeat("x", MaxReviewSpecBytes)}},
	} {
		t.Run("valid: "+tc.name, func(t *testing.T) {
			s := reviewTask()
			s.ReviewSpec = tc.rs
			if err := s.Validate(); err != nil {
				t.Errorf("review_spec %+v: %v", tc.rs, err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		rs   *ReviewSpec
	}{
		{"no source", &ReviewSpec{}},
		{"an unknown source", &ReviewSpec{Source: "diff"}},
		{"a run source with no run id", &ReviewSpec{Source: ReviewSpecRun, Text: "x"}},
		{"a run source whose run id is not one", &ReviewSpec{Source: ReviewSpecRun, Run: "main", Text: "x"}},
		{"a file source carrying a run id", &ReviewSpec{Source: ReviewSpecFile, Run: rootRun, Text: "x"}},
		{"a none source with no reason", &ReviewSpec{Source: ReviewSpecNone}},
		{"a none source with a blank reason", &ReviewSpec{Source: ReviewSpecNone, Reason: "  "}},
		{"a none source with text", &ReviewSpec{Source: ReviewSpecNone, Reason: "--code-only", Text: "x"}},
		{"a text over the cap", &ReviewSpec{Source: ReviewSpecRun, Run: rootRun, Text: strings.Repeat("x", MaxReviewSpecBytes+1)}},
		{"a followup over the cap", &ReviewSpec{Source: ReviewSpecRun, Run: rootRun, Followup: strings.Repeat("x", MaxReviewSpecBytes+1)}},
		{"a plan task PlanTaskProblem refuses", &ReviewSpec{Source: ReviewSpecRun, Run: rootRun, Text: "x", PlanTask: "../etc/passwd"}},
		{"a plan task that names no task in a plan", &ReviewSpec{Source: ReviewSpecRun, Run: rootRun, Text: "x", PlanTask: "setup-wizard"}},
	} {
		t.Run("refused: "+tc.name, func(t *testing.T) {
			s := reviewTask()
			s.ReviewSpec = tc.rs
			if err := s.Validate(); err == nil {
				t.Errorf("review_spec %+v: accepted, want refused", tc.rs)
			}
		})
	}
	// own_text is the person's own words beside a review's findings: it is
	// quoted only when the launch marked the task text as carrying findings
	// too, and it is bounded like the spec's own two texts (R2).
	for _, tc := range []struct {
		name         string
		fromFindings bool
		ownText      string
		ok           bool
	}{
		{"own_text with from_findings", true, "Also rename the flag.", true},
		{"own_text without from_findings", false, "Also rename the flag.", false},
		{"own_text exactly at the cap", true, strings.Repeat("x", MaxReviewSpecBytes), true},
		{"own_text over the cap", true, strings.Repeat("x", MaxReviewSpecBytes+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reviewTask()
			s.FromFindings = tc.fromFindings
			s.OwnText = tc.ownText
			err := s.Validate()
			if tc.ok && err != nil {
				t.Errorf("from_findings %v, own_text of %d bytes: %v", tc.fromFindings, len(tc.ownText), err)
			}
			if !tc.ok && err == nil {
				t.Errorf("from_findings %v, own_text of %d bytes: accepted, want refused", tc.fromFindings, len(tc.ownText))
			}
		})
	}
}

// TestSpecWithoutReviewSpecIsByteIdentical: a launch that is not a review
// carries none of the three fields, so its spec is byte-identical to what the
// release before them wrote — the rule every optional task field in this
// package follows, and what keeps an older runner (whose decoder disallows
// unknown fields) reading a task this release wrote for an ordinary run.
func TestSpecWithoutReviewSpecIsByteIdentical(t *testing.T) {
	plain, err := json.Marshal(&Spec{Version: 1, RunID: "20261119-120000-a1b2", Repo: "acme/app", Ref: "main", Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"review_spec", "from_findings", "own_text"} {
		if strings.Contains(string(plain), key) {
			t.Errorf("a spec with no review fields carries %q: %s", key, plain)
		}
	}
}
