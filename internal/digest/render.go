package digest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/report"
)

// Markdown is d as the report's file: the tables a person reads and the
// fenced JSON block a later report's pattern rules read. It is deterministic
// (G3) -- the same digest gives byte-identical bytes -- and it renders what
// Build put in d and nothing else, so a report carries no person and no text
// a record wrote (G1).
func Markdown(d Digest) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Run digest: %s\n\n", d.Period.Key)
	fmt.Fprintf(&b, "Runs of this repository that finished in %s (%s to %s): %d in all, %d shown, %d flagged. Billed %s, notional %s.\n\n",
		d.Period.Key, d.Period.Start, d.Period.End, d.Total.Runs, len(d.Runs), d.Total.Flagged,
		report.USD(d.Total.Billed), report.USD(d.Total.Notional))

	if len(d.Runs) == 0 {
		b.WriteString("No run of this repository finished in this period.\n\n")
	} else {
		b.WriteString("| run | status | pr | base | reviews | billed | notional | flags |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
		for _, r := range d.Runs {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %d | %s | %s | %s |\n",
				r.ID, r.Status, pr(r.PR), yesNo(r.Base), r.Reviews,
				report.USD(r.Billed), report.USD(r.Notional), flagCell(r.Flags))
		}
		b.WriteString("\n")
	}
	if d.Dropped > 0 {
		fmt.Fprintf(&b, "Rows not shown: %d. The report is capped at %d bytes and the cheapest unflagged rows go first.\n\n",
			d.Dropped, MaxBytes)
	}

	if len(d.Stages) > 0 {
		b.WriteString("## Stages\n\n")
		b.WriteString("Model calls and sub-agents by stage, over every run of the period.\n\n")
		b.WriteString("| stage | calls | agents |\n")
		b.WriteString("| --- | --- | --- |\n")
		for _, s := range d.Stages {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", s.Name, figure(s.Calls, s.CallsKnown), figure(s.Agents, s.AgentsKnown))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Summary\n\n")
	b.WriteString("```json\n")
	// Marshal cannot fail on this type: it holds strings, ints and a map of
	// them, and every dollar figure is one of the strings.
	block, _ := json.Marshal(d.Summary())
	b.Write(block)
	b.WriteString("\n```\n")
	return []byte(b.String())
}

// Summary is d's machine-readable block: the same figures the tables show,
// for a later report's pattern rules. A dollar figure is a string of exact
// micro-dollars, never a float, so two readers of one file agree to the
// micro-dollar.
type Summary struct {
	Period   string         `json:"period"`
	Start    string         `json:"start"`
	End      string         `json:"end"`
	Runs     int            `json:"runs"`
	Shown    int            `json:"shown"`
	Flagged  int            `json:"flagged"`
	Dropped  int            `json:"dropped,omitempty"`
	Billed   string         `json:"billed"`
	Notional string         `json:"notional"`
	Review   string         `json:"review"`
	Halts    int            `json:"halts"`
	Flags    map[string]int `json:"flags,omitempty"`
}

// Summary is d's block, as the report writes it.
func (d Digest) Summary() Summary {
	s := Summary{
		Period: d.Period.Key, Start: d.Period.Start, End: d.Period.End,
		Runs: d.Total.Runs, Shown: len(d.Runs), Flagged: d.Total.Flagged, Dropped: d.Dropped,
		Billed: report.USD6(d.Total.Billed), Notional: report.USD6(d.Total.Notional),
		Review: report.USD6(d.Total.Review), Halts: d.Total.Halts,
	}
	for _, f := range d.Flags {
		if s.Flags == nil {
			s.Flags = map[string]int{}
		}
		s.Flags[f.Name] = f.Count
	}
	return s
}

// pr is a pull-request number as the table shows it: a number, or "-" for a
// run that has no pull request of its own.
func pr(n int) string {
	if n < 1 {
		return "-"
	}
	return "#" + strconv.Itoa(n)
}

// yesNo is the diff base as the report shows it (G1, reconciliation O6): the
// record's own base branch is free text a task could steer, so a reader is
// told only whether the record names one.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// flagCell is a run's flags as one cell.
func flagCell(flags []Flag) string {
	if len(flags) == 0 {
		return "-"
	}
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, f.String())
	}
	return strings.Join(out, "; ")
}

// figure is a counted total, or n/a when nobody counted it (G5).
func figure(n int, counted bool) string {
	if !counted {
		return "n/a"
	}
	return strconv.Itoa(n)
}
