package digest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/task"
)

// Markdown is d as the report's file: the tables a person reads and the
// fenced JSON block a later report's pattern rules read. It is deterministic
// (G3) -- the same digest gives byte-identical bytes -- and it renders what
// Build put in d and nothing else, so a report carries no person and no text
// a record wrote (G1).
//
// The one thing not of d's own fields is the tail a registered extra-section
// hook returns (section 5.4): a build that registered none, which every core
// build is, prints the report above and nothing more. The tail is appended
// after the core report, so the size cap is measured over markdownCore alone
// (capTo): the hook's bytes never count toward the cap, and the hook is called
// once per measurement rather than once per dropped row.
func Markdown(d Digest) []byte {
	return append(markdownCore(d), sectionsMarkdown(d)...)
}

// markdownCore is the report without the hook's tail: what Build put in d, as
// the report prints it. capTo measures the cap over this and not over Markdown,
// so a registered section hook's output is outside the cap by construction: it
// is appended after a report already trimmed to fit, and it is not rendered
// again and again while rows are dropped.
func markdownCore(d Digest) []byte {
	var b strings.Builder
	// The IDs this report prints, each shortened by the one collision rule
	// over exactly the rows it prints (spec.md §3, D1706): the run table's
	// rows and the failures table's example runs. The shortening is here and
	// not in RunsTable because digestSubmitRuns renders that table too, with
	// its rows renumbered -- a draft leaves the repository and carries no run
	// ID at all.
	ends := task.DisplayRuns(printed(d))
	fmt.Fprintf(&b, "# Run digest: %s\n\n", d.Period.Key)
	fmt.Fprintf(&b, "Runs of this repository that finished in %s (%s to %s): %d in all, %d shown, %d flagged. Billed %s, notional %s.\n\n",
		d.Period.Key, d.Period.Start, d.Period.End, d.Total.Runs, len(d.Runs), d.Total.Flagged,
		USD(d.Total.Billed), USD(d.Total.Notional))

	if len(d.Runs) == 0 {
		b.WriteString("No run of this repository finished in this period.\n\n")
	} else {
		rows := make([]Run, len(d.Runs))
		for i, r := range d.Runs {
			r.ID = ends[task.Ref{RunID: r.ID}]
			rows[i] = r
		}
		b.WriteString(RunsTable(rows))
	}
	if d.Dropped > 0 {
		fmt.Fprintf(&b, "Rows not shown: %d. The report is capped at %d bytes before extra sections, and the cheapest unflagged rows go first.\n\n",
			d.Dropped, MaxBytes)
	}

	if len(d.Failures) > 0 {
		b.WriteString("## Failures\n\n")
		b.WriteString("Why the period's runs did not succeed, over every run of the period, not only the rows above.\n\n")
		rows := make([]ClassCount, len(d.Failures))
		for i, f := range d.Failures {
			f.Example = ends[task.Ref{RunID: f.Example}]
			rows[i] = f
		}
		b.WriteString(FailuresTable(rows))
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

// printed is every run ID the report's tables print: the run table's rows and
// the failures table's example runs. The collision rule is computed over
// exactly this set, because a collision is a property of what is shown
// together and not of the bucket (F15).
func printed(d Digest) []task.Ref {
	refs := make([]task.Ref, 0, len(d.Runs)+len(d.Failures))
	for _, r := range d.Runs {
		refs = append(refs, task.Ref{RunID: r.ID})
	}
	for _, f := range d.Failures {
		refs = append(refs, task.Ref{RunID: f.Example})
	}
	return refs
}

// RunsTable is the report's run table: its header, one row per run, and the
// blank line after it. Every cell is a figure or a closed vocabulary, and the
// first one is the ID the caller put in the row -- the run's short ID in the
// report (markdownCore), a renumbered one in a draft that leaves the
// repository -- so the two renderings of one set of figures cannot drift
// apart.
func RunsTable(runs []Run) string {
	var b strings.Builder
	b.WriteString("| run | status | pr | base | reviews | billed | notional | flags |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range runs {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %d | %s | %s | %s |\n",
			r.ID, r.Status, pr(r.PR), yesNo(r.Base), r.Reviews,
			USD(r.Billed), USD(r.Notional), flagCell(r.Flags))
	}
	b.WriteString("\n")
	return b.String()
}

// FailuresTable is the report's failure table: one row per class, the class
// itself and its figures. Every cell is a figure or a value of the closed
// vocabulary, and the run ID is the example the digest picked out of its own
// records, so a row can hold no word a record chose (G1).
func FailuresTable(failures []ClassCount) string {
	var b strings.Builder
	b.WriteString("| class | runs | billed | example |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, f := range failures {
		fmt.Fprintf(&b, "| `%s` | %d | %s | `%s` |\n", f.Class, f.Runs, USD(f.Billed), f.Example)
	}
	b.WriteString("\n")
	return b.String()
}

// Summary is d's machine-readable block: the same figures the tables show,
// for a later report's pattern rules. A dollar figure is a string of exact
// micro-dollars, never a float, so two readers of one file agree to the
// micro-dollar.
type Summary struct {
	Period   string `json:"period"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Runs     int    `json:"runs"`
	Shown    int    `json:"shown"`
	Flagged  int    `json:"flagged"`
	Dropped  int    `json:"dropped,omitempty"`
	Billed   string `json:"billed"`
	Notional string `json:"notional"`
	Review   string `json:"review"`
	Halts    int    `json:"halts"`
	// Months is how many monthly reports a year's block added up (F24): a
	// year is its months, and a year of fewer than twelve says so rather than
	// let a reader take a partial year's figures for a whole one (G5). No
	// other period writes it.
	Months int            `json:"months,omitempty"`
	Flags  map[string]int `json:"flags,omitempty"`
	// Failures is how many of the period's runs each class holds, by the
	// class's own name (design §6.2): the two failure-class rules read counts,
	// and money per class stays out of a block whose discipline is flat maps
	// and exact strings.
	//
	// A block written before the field shipped carries no key at all, which is
	// not the same fact as a period of no failures: the two rules read the
	// key's presence, so a period whose runs all succeeded carries none either
	// (G5).
	Failures map[string]int `json:"failures,omitempty"`
	// Recipes is each recipe's median cost over the period's runs, by the
	// name the report writes: a catalog recipe's own, "other" for the runs no
	// catalog recipe names. The pattern rules read it month over month, and a
	// table has no room for it.
	Recipes map[string]string `json:"recipes,omitempty"`
}

// Summary is d's block, as the report writes it.
func (d Digest) Summary() Summary {
	s := Summary{
		Period: d.Period.Key, Start: d.Period.Start, End: d.Period.End,
		Runs: d.Total.Runs, Shown: len(d.Runs), Flagged: d.Total.Flagged, Dropped: d.Dropped,
		Billed: USD6(d.Total.Billed), Notional: USD6(d.Total.Notional),
		Review: USD6(d.Total.Review), Halts: d.Total.Halts,
	}
	for _, f := range d.Flags {
		if s.Flags == nil {
			s.Flags = map[string]int{}
		}
		s.Flags[f.Name] = f.Count
	}
	for _, f := range d.Failures {
		if s.Failures == nil {
			s.Failures = map[string]int{}
		}
		s.Failures[string(f.Class)] = f.Runs
	}
	for name, med := range d.Recipes {
		if s.Recipes == nil {
			s.Recipes = map[string]string{}
		}
		s.Recipes[name] = USD6(med)
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
