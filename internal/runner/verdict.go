package runner

import (
	"encoding/json"
	"errors"
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
// last fenced JSON block. Anything unparseable counts as a blocking finding,
// and the finding says what could not be read: the decoder's own refusal
// names the field it refused, which is the one thing a reader of a failed
// round needs — a score written as a word ("likelihood": "high") is the case
// this is for, and a finding that says only "no parseable verdict" leaves
// that reader searching the transcript by hand.
//
// The run's own review rounds do not read a verdict this way: a round with
// nothing to read is retried once and then let through, so no reviewer that
// answers nothing can fail a run (seniorLoop, TryVerdict). What is left here
// is the caller that must have an answer — a pick run choosing the pull
// request out of the list it was given, and diagnose's account of a round.
func ParseVerdict(res agent.Result) Verdict {
	v, why := readVerdict(res)
	if why == "" {
		return v
	}
	return Verdict{Verdict: "changes", Findings: []Finding{{Severity: "blocker", Summary: "review produced no parseable verdict: " + why}}}
}

// TryVerdict is ParseVerdict that says whether there was a verdict to read.
func TryVerdict(res agent.Result) (Verdict, bool) {
	v, why := readVerdict(res)
	return v, why == ""
}

// readVerdict decodes a verdict from the structured output or, failing that,
// from the last fenced JSON block in the text. It returns "" as the reason
// when there was a verdict, and otherwise why the read failed — the refusal
// of the structured output first, since that is the one a reviewer wrote.
func readVerdict(res agent.Result) (Verdict, string) {
	why := ""
	note := func(err error) {
		if why == "" {
			why = err.Error()
		}
	}
	if len(res.Structured) > 0 {
		v, err := decodeVerdict(res.Structured)
		if err == nil {
			return v, ""
		}
		note(err)
	}
	if m := fencedJSONRE.FindAllStringSubmatch(res.Text, -1); len(m) > 0 {
		v, err := decodeVerdict([]byte(m[len(m)-1][1]))
		if err == nil {
			return v, ""
		}
		note(err)
	}
	if why == "" {
		why = "nothing in the result was a verdict"
	}
	return Verdict{}, why
}

// decodeVerdict reads one verdict. A verdict that is not one of the two words
// the readiness rule reads is refused rather than passed on: every reader of
// a verdict compares it against those two, so a third word would be a run
// that shipped nothing and said so.
func decodeVerdict(b []byte) (Verdict, error) {
	var v Verdict
	if len(b) == 0 {
		return v, errors.New("the output was empty")
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	if v.Verdict != "ship" && v.Verdict != "changes" {
		return v, errors.New("verdict is neither ship nor changes")
	}
	return v, nil
}
