package runner

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
)

// The score D3 adds to a finding: three axes and a class, all optional, so
// every verdict written before them still reads exactly as it did.

func TestVerdictScoredFindingRoundTrips(t *testing.T) {
	res := agent.Result{Structured: json.RawMessage(`{"verdict":"changes","findings":[{"severity":"high","file":"ledger.go","summary":"a partial write is not rolled back","likelihood":2,"impact":5,"fix_cost":3,"class":"data_loss"}]}`)}
	v := ParseVerdict(res)
	if v.Verdict != "changes" || len(v.Findings) != 1 {
		t.Fatalf("scored verdict = %+v", v)
	}
	f := v.Findings[0]
	if f.Likelihood != 2 || f.Impact != 5 || f.FixCost != 3 || f.Class != "data_loss" {
		t.Errorf("the score did not survive ParseVerdict: %+v", f)
	}
	if f.Severity != "high" || f.File != "ledger.go" || f.Summary == "" {
		t.Errorf("the fields the score was added beside did not: %+v", f)
	}
	// A round trip, because a scored finding is written to a transcript and
	// read back: a field that survives unmarshalling but not marshalling is
	// a score that disappears on the way into the record.
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshalling a finding: %v", err)
	}
	var back Finding
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("reading %s back: %v", b, err)
	}
	if back != f {
		t.Errorf("round trip changed the finding: %s gave %+v, want %+v", b, back, f)
	}
}

func TestVerdictTodaysShapeStillParses(t *testing.T) {
	res := agent.Result{Structured: json.RawMessage(`{"verdict":"changes","findings":[{"severity":"high","file":"a.go","summary":"nil deref"}]}`)}
	f := ParseVerdict(res).Findings[0]
	if f.Severity != "high" || f.File != "a.go" || f.Summary != "nil deref" {
		t.Fatalf("a verdict in today's shape = %+v", f)
	}
	// Zero on every axis, which is what "unscored" is: the valid scale
	// starts at 1, so nothing a reviewer scores can read as absent, and
	// D4's fail-closed rule has an unambiguous input.
	if f.Likelihood != 0 || f.Impact != 0 || f.FixCost != 0 || f.Class != "" {
		t.Errorf("a verdict with no score did not read as unscored: %+v", f)
	}
	// And it is written back without them: an old transcript must not gain
	// keys on a read-modify-write.
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshalling an unscored finding: %v", err)
	}
	if got := string(b); got != `{"severity":"high","file":"a.go","summary":"nil deref"}` {
		t.Errorf("an unscored finding marshalled to %s, want today's three fields alone", got)
	}
}

func TestVerdictSecurityClassParses(t *testing.T) {
	for _, class := range []string{"security", "data_loss"} {
		res := agent.Result{Structured: json.RawMessage(`{"verdict":"changes","findings":[{"summary":"x","likelihood":1,"impact":5,"fix_cost":1,"class":"` + class + `"}]}`)}
		if f := ParseVerdict(res).Findings[0]; f.Class != class {
			t.Errorf("class %q read as %q", class, f.Class)
		}
	}
}

// The schema is what constrains the model; the parser reads whatever a
// transcript holds. A score outside 1–5 is therefore read as given rather
// than refused or clamped — the transcript a run already wrote stays
// readable, and D4's rule is what keeps a bad score from being lenient.
func TestVerdictOutOfRangeScoreIsReadNotRefused(t *testing.T) {
	for _, score := range []string{"0", "-1", "9"} {
		res := agent.Result{Structured: json.RawMessage(`{"verdict":"changes","findings":[{"summary":"x","likelihood":` + score + `,"impact":` + score + `,"fix_cost":` + score + `}]}`)}
		v := ParseVerdict(res)
		if v.Verdict != "changes" || len(v.Findings) != 1 {
			t.Fatalf("a finding scored %s did not parse: %+v", score, v)
		}
		if got := strconv.Itoa(v.Findings[0].Likelihood); got != score {
			t.Errorf("likelihood %s read as %s, want it read as given", score, got)
		}
	}
}

// The schema is the one place the score is declared to the model, so its
// shape is asserted rather than left to the model to guess at: three
// integers bounded to the 1–5 scale D1 answered, and a closed class.
func TestVerdictSchemaCarriesTheScore(t *testing.T) {
	type prop struct {
		Type    string   `json:"type"`
		Minimum *int     `json:"minimum"`
		Maximum *int     `json:"maximum"`
		Enum    []string `json:"enum"`
	}
	var schema struct {
		Properties struct {
			Findings struct {
				Items struct {
					Properties map[string]prop `json:"properties"`
					Required   []string        `json:"required"`
				} `json:"items"`
			} `json:"findings"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(VerdictSchema), &schema); err != nil {
		t.Fatalf("VerdictSchema is not JSON: %v", err)
	}
	item := schema.Properties.Findings.Items
	for _, axis := range []string{"likelihood", "impact", "fix_cost"} {
		p, ok := item.Properties[axis]
		if !ok {
			t.Errorf("VerdictSchema declares no %s: the model cannot score an axis it is not given", axis)
			continue
		}
		if p.Type != "integer" || p.Minimum == nil || *p.Minimum != 1 || p.Maximum == nil || *p.Maximum != 5 {
			t.Errorf("%s = %+v, want an integer from 1 to 5", axis, p)
		}
	}
	if p := item.Properties["class"]; p.Type != "string" || len(p.Enum) == 0 {
		t.Errorf("class = %+v, want a closed enum", p)
	} else {
		for _, want := range []string{"security", "data_loss"} {
			if !slices.Contains(p.Enum, want) {
				t.Errorf("class omits %q: the two classes the threshold table overrides on must be in it: %v", want, p.Enum)
			}
		}
	}
	if p, ok := item.Properties["severity"]; !ok || p.Type != "string" {
		t.Errorf("severity = %+v, want the free-form string it is today: the label is not the score", p)
	}
	// Additive, which is the whole reason a stored transcript still reads:
	// a summary is the only thing a finding must carry.
	if len(item.Required) != 1 || item.Required[0] != "summary" {
		t.Errorf("the finding item requires %v, want summary alone", item.Required)
	}
}
