package runner

import (
	"encoding/json"
	"regexp"

	"github.com/dimipaun/fugaro/internal/agent"
)

// VerdictSchema is passed to claude --json-schema for review stages.
const VerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["ship","changes"]},"findings":{"type":"array","items":{"type":"object","properties":{"severity":{"type":"string"},"file":{"type":"string"},"summary":{"type":"string"}},"required":["summary"]}}},"required":["verdict","findings"]}`

// Finding is one issue a review asks to fix.
type Finding struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Summary  string `json:"summary"`
}

// Verdict is a review stage's structured result.
type Verdict struct {
	Verdict  string    `json:"verdict"`
	Findings []Finding `json:"findings"`
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
