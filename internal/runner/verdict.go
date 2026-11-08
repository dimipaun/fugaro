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
// It is NOT the independent review job's schema: that job's result is the
// artifact (reviewartifact.go, review.schema.json), whose own three-word
// verdict this reader refuses and whose reader would refuse this pair. A
// review stage's request carries one of the two and never both
// (reviewStageSchema, D890/F23), and this one is every review stage the
// coding run runs -- its own in-loop rounds and `fugaro run --pr N --recipe
// review-only` alike.
//
// A finding's score is three anchored 1–5 axes and a class (D1, D3). All four
// are optional, so a verdict written before them still validates and still
// parses, and severity stays the free-form label it is (D2).
//
// A summary is the one thing a finding must carry, and it is a sentence: the
// key is required and it may not be empty. The model is constrained by this
// schema as it decodes, so minLength is what keeps a finding the owner must
// settle (needs_owner) from arriving with nothing for them to read — the
// reader does not enforce the schema (decodeVerdict), so the runner stores a
// decision with a sentence of its own when a verdict reaches it without one
// (ownerDecision).
const VerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["ship","changes"]},"findings":{"type":"array","items":{"type":"object","properties":{"severity":{"type":"string"},"likelihood":{"type":"integer","minimum":1,"maximum":5},"impact":{"type":"integer","minimum":1,"maximum":5},"fix_cost":{"type":"integer","minimum":1,"maximum":5},"class":{"type":"string","enum":["correctness","performance","security","data_loss","maintainability","cosmetic"]},"needs_owner":{"type":"boolean"},"file":{"type":"string"},"summary":{"type":"string","minLength":1}},"required":["summary"]}},"keep":{"type":"integer"}},"required":["verdict","findings"]}`

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
	// NeedsOwner marks a finding whose ONLY remedies are a decision that is
	// not this run's to make (OD1): a licence or legal clearance, a product or
	// policy choice, an owner-run step, or a spec that says the pull request
	// must not merge until the owner answers. The reviewer sets it and the
	// code never does, so it is a claim about who can settle the finding and
	// never a second score: the axes beside it are still read by the mode's
	// table, and a needs_owner finding at fix cost 1 is still the owner's.
	//
	// It is additive and false in every verdict written before it existed. A
	// run that leaves one stops its review loop rather than buying fix rounds
	// no code change can settle, and ends waiting for the owner (ownerStop,
	// outcome.go).
	NeedsOwner bool   `json:"needs_owner,omitempty"`
	File       string `json:"file"`
	Summary    string `json:"summary"`
}

// Owner reports whether this finding is one only the owner can settle.
func (f Finding) Owner() bool { return f.NeedsOwner }

// ownerFindings is the findings of a round that only the owner can settle, in
// the reviewer's order.
func ownerFindings(findings []Finding) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.NeedsOwner {
			out = append(out, f)
		}
	}
	return out
}

// fixableFindings is the findings of a round the run's own fix round may
// address: every finding a reviewer did not mark as the owner's. It is what
// a fix round's brief is built from (FixPrompt), so a finding no code change
// can settle never spends a round on itself.
func fixableFindings(findings []Finding) []Finding {
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if !f.NeedsOwner {
			out = append(out, f)
		}
	}
	return out
}

// allOwner reports whether a round asked for changes with every finding the
// owner's to settle. It is false for a round with no findings: a `changes`
// verdict that names nothing is a review which failed to say what it wanted,
// and it is not an owner decision (D4's rule is about a round that names what
// it wants).
func allOwner(findings []Finding) bool {
	return len(findings) > 0 && len(ownerFindings(findings)) == len(findings)
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
