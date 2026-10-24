package runner

import (
	"encoding/json"
	"regexp"

	"github.com/dimipaun/fugaro/internal/agent"
)

// VerdictSchema is passed to claude --json-schema for review stages. keep is
// read only for a pick run (reviewmode.go): the pull request number to
// keep, among the ones the prompt listed.
//
// A finding's score is three anchored 1–5 axes and a class (D1, D3). All four
// are optional, so a verdict written before them still validates and still
// parses, and severity stays the free-form label it is (D2).
const VerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["ship","changes"]},"findings":{"type":"array","items":{"type":"object","properties":{"severity":{"type":"string"},"likelihood":{"type":"integer","minimum":1,"maximum":5},"impact":{"type":"integer","minimum":1,"maximum":5},"fix_cost":{"type":"integer","minimum":1,"maximum":5},"class":{"type":"string","enum":["correctness","performance","security","data_loss","maintainability","cosmetic"]},"file":{"type":"string"},"summary":{"type":"string"}},"required":["summary"]}},"keep":{"type":"integer"}},"required":["verdict","findings"]}`

// Finding is one issue a review asks to fix.
type Finding struct {
	Severity string `json:"severity"`
	// The mode's threshold table reads these, never Severity (D2). An
	// unscored finding reads as zero on every axis and blocks in every
	// mode, so a reviewer that stops scoring cannot turn a mode down
	// silently (D4).
	Likelihood int    `json:"likelihood,omitempty"`
	Impact     int    `json:"impact,omitempty"`
	FixCost    int    `json:"fix_cost,omitempty"`
	Class      string `json:"class,omitempty"`
	File       string `json:"file"`
	Summary    string `json:"summary"`
}

// Verdict is a review stage's structured result.
type Verdict struct {
	Verdict  string    `json:"verdict"`
	Findings []Finding `json:"findings"`
	// Keep is a pick run's chosen pull request number (reviewmode.go); 0
	// for any other review stage.
	Keep int `json:"keep,omitempty"`
}

var fencedJSONRE = regexp.MustCompile("(?s)```json\\s*\\n(.*?)\\n```")

// ParseVerdict reads the verdict from structured output, falling back to the
// last fenced JSON block. Anything unparseable counts as a blocking finding.
func ParseVerdict(res agent.Result) Verdict {
	if v, ok := TryVerdict(res); ok {
		return v
	}
	return Verdict{Verdict: "changes", Findings: []Finding{{Severity: "blocker", Summary: "review produced no parseable verdict"}}}
}

// TryVerdict is ParseVerdict that says whether there was a verdict to read.
func TryVerdict(res agent.Result) (Verdict, bool) {
	if v, ok := decodeVerdict(res.Structured); ok {
		return v, true
	}
	if m := fencedJSONRE.FindAllStringSubmatch(res.Text, -1); len(m) > 0 {
		if v, ok := decodeVerdict([]byte(m[len(m)-1][1])); ok {
			return v, true
		}
	}
	return Verdict{}, false
}

func decodeVerdict(b []byte) (Verdict, bool) {
	var v Verdict
	if len(b) == 0 || json.Unmarshal(b, &v) != nil {
		return v, false
	}
	return v, v.Verdict == "ship" || v.Verdict == "changes"
}
