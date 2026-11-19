package runner

// S-OUT's own tests: an issue's scope and the artifact's record of what the
// review was checked against (D2930, D2935, R6, R8, R16). The two are one
// change because they are one shape's new optional fields, read by the same
// reader -- the artifact's answer decoder, the stored object's reader and the
// in-run verdict's parser.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestReviewArtifactScopeValidated: an issue's scope is a closed vocabulary
// the artifact's reader closes, and it is additive -- an issue that carries
// none reads exactly as it did, and one that carries a word this build does
// not know is refused rather than read as the nearest word it does.
//
// Mutation (accept any scope): the sideways row passes and this test fails.
func TestReviewArtifactScopeValidated(t *testing.T) {
	art := reviewArtifactFixture(t)
	art.Verdict = "fix first"
	issue := art.Issues[0]

	for _, scope := range []string{scopeUnmet, scopeBeyond, scopeDiffers} {
		art.Issues[0] = issue
		art.Issues[0].Scope = scope
		data, err := json.Marshal(art)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeReviewArtifact(data)
		if err != nil {
			t.Fatalf("the reader refused scope %q, one of the three words: %v", scope, err)
		}
		if back.Issues[0].Scope != scope {
			t.Errorf("scope %q read back as %q", scope, back.Issues[0].Scope)
		}
	}

	art.Issues[0] = issue
	art.Issues[0].Scope = "sideways"
	data, err := json.Marshal(art)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReviewArtifact(data); err == nil {
		t.Error("the reader accepted a scope nobody defined")
	} else if !strings.Contains(err.Error(), "scope") {
		t.Errorf("the refusal does not name the field it refused: %v", err)
	}

	// Absent is the shape every issue written before the field had: it reads
	// as the empty word rather than as a refusal.
	art.Issues[0] = issue
	data, err = json.Marshal(art)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := DecodeReviewArtifact(data); err != nil {
		t.Errorf("an issue with no scope is refused: %v", err)
	} else if back.Issues[0].Scope != "" {
		t.Errorf("an issue with no scope read as %q", back.Issues[0].Scope)
	}

	// The artifact's other new optional field is held to its own vocabulary by
	// the same reader (R8): an against nobody defined is refused, and each of
	// the three words reads back as it was written.
	for _, tc := range []struct {
		name    string
		against *runstore.Against
		ok      bool
	}{
		{"a root run", &runstore.Against{Source: runstore.AgainstSourceRun, Run: "20261102-093000-abcd", SHA256: strings.Repeat("a", 64)}, true},
		{"a spec file", &runstore.Against{Source: runstore.AgainstSourceFile, SHA256: strings.Repeat("a", 64)}, true},
		{"nothing found", &runstore.Against{Source: runstore.AgainstSourceNone, Reason: "--code-only"}, true},
		{"a source nobody defined", &runstore.Against{Source: "sideways"}, false},
		{"a run that is not a run id", &runstore.Against{Source: runstore.AgainstSourceRun, Run: "../../etc/passwd"}, false},
		{"a file carrying a run id", &runstore.Against{Source: runstore.AgainstSourceFile, Run: "20261102-093000-abcd"}, false},
		{"a none with no reason", &runstore.Against{Source: runstore.AgainstSourceNone}, false},
		{"a run carrying a reason", &runstore.Against{Source: runstore.AgainstSourceRun, Run: "20261102-093000-abcd", Reason: "--code-only"}, false},
		{"a none that names a run", &runstore.Against{Source: runstore.AgainstSourceNone, Reason: "no run found", Run: "20261102-093000-abcd"}, false},
		{"a digest that is not a digest", &runstore.Against{Source: runstore.AgainstSourceFile, SHA256: "not-a-digest"}, false},
	} {
		art.Against = tc.against
		data, err := json.Marshal(art)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeReviewArtifact(data)
		if tc.ok && err != nil {
			t.Errorf("%s: the reader refused %+v: %v", tc.name, tc.against, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: the reader accepted %+v", tc.name, tc.against)
		}
		if tc.ok && err == nil && *back.Against != *tc.against {
			t.Errorf("%s: read back as %+v, want %+v", tc.name, *back.Against, *tc.against)
		}
	}

	// Absent, which is what a review written before the field carries: the
	// reader neither refuses it nor invents one.
	art.Against = nil
	data, err = json.Marshal(art)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := DecodeReviewArtifact(data); err != nil {
		t.Errorf("an artifact with no against is refused: %v", err)
	} else if back.Against != nil {
		t.Errorf("the reader made up an against: %+v", back.Against)
	}
	if strings.Contains(string(data), `"against"`) {
		t.Errorf("an artifact whose against is absent wrote one anyway: %s", data)
	}
}

// TestReviewArtifactUnmetBelowMajorRefused: a change that does not do what its
// task asks is never a small matter, so an `unmet` or `differs` issue below
// `major` is refused outright rather than scored -- no mode's threshold table
// gets to wave it through (R6). A `beyond` issue is scored by its own risk and
// may be minor.
func TestReviewArtifactUnmetBelowMajorRefused(t *testing.T) {
	for _, tc := range []struct {
		scope    string
		severity string
		ok       bool
	}{
		{scopeUnmet, "minor", false},
		{scopeDiffers, "minor", false},
		{scopeUnmet, "major", true},
		{scopeUnmet, "blocker", true},
		{scopeDiffers, "major", true},
		{scopeBeyond, "minor", true},
	} {
		art := reviewArtifactFixture(t)
		art.Verdict = "fix first"
		art.Issues[0].Scope = tc.scope
		art.Issues[0].Severity = tc.severity
		data, err := json.Marshal(art)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeReviewArtifact(data)
		if tc.ok && err != nil {
			t.Errorf("an %s issue at %s was refused: %v", tc.scope, tc.severity, err)
			continue
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("the reader accepted an %s issue at %s", tc.scope, tc.severity)
			}
			continue
		}
		if back.Issues[0].Scope != tc.scope || back.Issues[0].Severity != tc.severity {
			t.Errorf("the issue read back as %+v, want %s at %s", back.Issues[0], tc.scope, tc.severity)
		}
	}

	// The same rule holds on the reviewer's own answer, which is the path the
	// review job takes: an answer refused there ends the run as an invalid
	// verdict rather than being signed and stored (finishReviewJob).
	answer := reviewAnswerOf("fix first", "one requirement is missing",
		ReviewIssue{Severity: "minor", Class: "correctness", Scope: scopeUnmet,
			File: "a.go", Line: 1, Summary: "the brief asks for a flag the change does not add",
			Fix: "add the flag", Evidence: "the brief's sentence about the flag"})
	data, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeReviewAnswer(data); err == nil {
		t.Error("the reviewer's own answer carried an unmet issue at minor and was accepted")
	}
}

// TestReviewArtifactShipWithUnmetRefused: `ship` is the word a coordinator
// reads as "nothing must change before this merges", so an artifact that ships
// a change whose own issue says the task is not done is refused -- on the
// stored object and on the reviewer's answer alike (R6). A `beyond` issue
// beside a ship is not refused: doing more than the brief asked is a thing to
// report.
func TestReviewArtifactShipWithUnmetRefused(t *testing.T) {
	for _, tc := range []struct {
		verdict string
		scope   string
		ok      bool
	}{
		{"ship", scopeUnmet, false},
		{"ship", scopeDiffers, false},
		{"ship", scopeBeyond, true},
		{"ship", "", true},
		{"fix first", scopeUnmet, true},
		{"reject", scopeDiffers, true},
	} {
		art := reviewArtifactFixture(t)
		art.Verdict = tc.verdict
		art.Issues[0].Scope = tc.scope
		data, err := json.Marshal(art)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeReviewArtifact(data); tc.ok && err != nil {
			t.Errorf("a %q verdict with a %q issue was refused: %v", tc.verdict, tc.scope, err)
		} else if !tc.ok && err == nil {
			t.Errorf("the reader accepted a %q verdict carrying a %q issue", tc.verdict, tc.scope)
		}
	}

	// The reviewer's own answer: a ship beside an unmet issue never reaches
	// the signer, and the run ends the way an unreadable verdict ends it.
	r, _ := reviewJobRun(t, nil)
	res := reviewResult(t, reviewAnswerOf("ship", "everything the brief asked for is here",
		ReviewIssue{Severity: "major", Class: "correctness", Scope: scopeUnmet,
			File: "a.go", Line: 1, Summary: "the brief asks for a reader the change does not add",
			Fix: "add the reader", Evidence: "the brief's own sentence"}))
	r.finishReviewJob(context.Background(), res)
	if r.rec.Status != runstore.StatusFailed || r.rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("a ship beside an unmet issue ended %s/%s, want failed/none", r.rec.Status, r.rec.Outcome)
	}
	if !strings.Contains(r.rec.Reason, "unmet") {
		t.Errorf("the run's reason does not name the scope it refused: %q", r.rec.Reason)
	}
	if len(r.rec.Artifacts) != 0 || len(r.rec.Reviews) != 0 {
		t.Errorf("a review that shipped beside an unmet issue stored an artifact or a review entry: %+v", r.rec)
	}
}

// TestVerdictScopeDecodes: the in-run verdict carries a finding's scope too --
// the same three words on the reviewer's answer for a coding run's own rounds,
// where the rigor table reads it (RA6a) -- and, like the class and the axes, it
// is additive: a verdict written before it parses and is written back with its
// own fields alone.
func TestVerdictScopeDecodes(t *testing.T) {
	v := ParseVerdict(agent.Result{Structured: json.RawMessage(
		`{"verdict":"changes","findings":[{"severity":"high","file":"a.go","summary":"the brief asks for this","likelihood":2,"impact":5,"fix_cost":1,"class":"correctness","scope":"unmet"}]}`)})
	if len(v.Findings) != 1 || v.Findings[0].Scope != scopeUnmet {
		t.Fatalf("a scoped finding did not parse: %+v", v.Findings)
	}
	// A round trip, for the reason the scored finding's round trip is here: a
	// scope that survives unmarshalling but not marshalling disappears on the
	// way into the record, which is where the fix brief and the digest read it.
	b, err := json.Marshal(v.Findings[0])
	if err != nil {
		t.Fatal(err)
	}
	var back Finding
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != v.Findings[0] {
		t.Errorf("round trip changed the finding: %s gave %+v, want %+v", b, back, v.Findings[0])
	}

	// Additive: today's finding marshals to today's fields alone, and the
	// schema the model is held to is where the three words are declared.
	old := Finding{Severity: "high", File: "a.go", Summary: "nil deref"}
	b, err = json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"severity":"high","file":"a.go","summary":"nil deref"}` {
		t.Errorf("an unscoped finding marshalled to %s, want today's fields alone", got)
	}
	var schema struct {
		Properties struct {
			Findings struct {
				Items struct {
					Properties map[string]struct {
						Type string   `json:"type"`
						Enum []string `json:"enum"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"findings"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(VerdictSchema), &schema); err != nil {
		t.Fatalf("VerdictSchema is not JSON: %v", err)
	}
	p, ok := schema.Properties.Findings.Items.Properties["scope"]
	if !ok {
		t.Fatal("VerdictSchema declares no scope: the model cannot answer the one axis that is about the task")
	}
	if p.Type != "string" || len(p.Enum) == 0 {
		t.Fatalf("scope = %+v, want a closed enum", p)
	}
	for _, want := range issueScopes {
		if !slices.Contains(p.Enum, want) {
			t.Errorf("scope omits %q: %v", want, p.Enum)
		}
	}
}
