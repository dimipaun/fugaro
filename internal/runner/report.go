package runner

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// LogTail is the end of the output that explains a draft PR (design §4.5).
type LogTail struct {
	Source string   // what the lines are from, such as "stage implement-1, stderr"
	Lines  []string // already redacted
}

// FollowUpSection is what a follow-up's report says about the pull
// request it continued: whose comments drove it, and what the agent says
// it did.
type FollowUpSection struct {
	PreviousRun          string
	Session, SessionNote string
	// Authors are the display names whose comments reached the agent,
	// with their counts; UntrustedAuthors (at most 20) and
	// UntrustedAuthorCount (all of them) name those who were dropped,
	// with UntrustedComments comments between them.
	Authors              map[string]int
	UntrustedAuthors     []string
	UntrustedAuthorCount int
	UntrustedComments    int
	// MarkersFromAnyone is set when the provider couldn't tell Fugaro's
	// own comments apart, so markers from every author were honoured.
	MarkersFromAnyone bool
	// AuthorUnknown is set when the provider didn't name the pull
	// request's author: no comment could then be told apart from Fugaro's
	// own, so none was trusted.
	AuthorUnknown bool
	NoNewCommits  bool
	// MovedToDraft is set when the pull request was ready before the run
	// and ends as a draft.
	MovedToDraft bool
	// Answer is the agent's followup.md as followup.QuoteAnswer returns it.
	Answer string
}

// Report renders the run report posted to the PR and stored as report.md.
// tail, when non-nil, is shown in a code block.
func Report(rec *runstore.Record, location string, tail *LogTail) string {
	return FollowUpReport(rec, location, tail, nil)
}

// FollowUpReport is Report with a follow-up's section, when fu is non-nil.
func FollowUpReport(rec *runstore.Record, location string, tail *LogTail, fu *FollowUpSection) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Fugaro run `%s`\n\n", rec.RunID)
	if rec.Status == runstore.StatusHalted && rec.Halt != nil {
		b.WriteString(haltLines(rec))
	}
	switch rec.Outcome {
	case runstore.OutcomeReady:
		b.WriteString("**Outcome:** ready for review\n\n")
	case runstore.OutcomeNone:
		fmt.Fprintf(&b, "**Outcome:** stopped — %s\n\n", rec.Reason)
	default:
		fmt.Fprintf(&b, "**Outcome:** draft — %s\n\n", rec.Reason)
	}
	if len(rec.Stages) > 0 {
		b.WriteString("| Stage | Duration |\n|---|---|\n")
		for _, s := range rec.Stages {
			d := time.Duration(s.DurationS * float64(time.Second)).Round(time.Second)
			fmt.Fprintf(&b, "| %s | %s |\n", s.Name, d)
		}
		b.WriteString("\n")
	}
	if len(rec.Reviews) > 0 {
		parts := make([]string, 0, len(rec.Reviews))
		for _, r := range rec.Reviews {
			p := fmt.Sprintf("round %d: %s", r.Round, r.Verdict)
			if r.Verdict != "ship" {
				p += " (" + Plural(r.Findings, "finding") + ")"
			}
			parts = append(parts, p)
		}
		fmt.Fprintf(&b, "**Reviews:** %s\n\n", strings.Join(parts, "; "))
	}
	b.WriteString(testsLine(rec))
	if rec.Cost != nil {
		b.WriteString(CostLine(*rec.Cost) + "\n\n")
	} else {
		fmt.Fprintf(&b, "**Cost:** $%.2f\n\n", rec.CostUSD)
	}
	b.WriteString(policyLine(rec.Policy))
	if fu != nil {
		b.WriteString(followUpSection(fu, rec.Reason))
	}
	if tail != nil && len(tail.Lines) > 0 {
		text := strings.Join(tail.Lines, "\n")
		f := fence(text)
		fmt.Fprintf(&b, "**Log tail** (%s):\n\n%stext\n%s\n%s\n\n", tail.Source, f, text, f)
	}
	fmt.Fprintf(&b, "Transcripts and verify records: `%s` in the runs bucket.\n", location)
	b.WriteString("\n" + gitprov.ReportMarker(rec.RunID) + "\n")
	return b.String()
}

func testsLine(rec *runstore.Record) string {
	last := latestVerifiedTest(rec.Verify, rec.HeadSHA)
	if last == nil {
		return "**Tests:** no recorded test run on the final commit\n\n"
	}
	status := "passed"
	if !last.Passed {
		status = "failed"
	}
	sha := last.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	s := fmt.Sprintf("**Tests:** %s on %s (%s, %s", status, sha, Plural(last.Tests, "test"), Plural(last.Failures, "failure"))
	if len(last.Flaky) > 0 {
		s += ", flaky: " + strings.Join(last.Flaky, ", ")
	}
	return s + ")\n\n"
}

// fence returns a backtick fence longer than any backtick run in s, so s
// cannot end the code block early.
func fence(s string) string {
	longest, run := 0, 0
	for _, c := range s {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// followUpSection renders fu; reason is why the run ended a draft.
func followUpSection(fu *FollowUpSection, reason string) string {
	var b strings.Builder
	b.WriteString("#### Follow-up\n\n")
	fmt.Fprintf(&b, "- **Previous run:** `%s`\n", fu.PreviousRun)
	if fu.Session != "" {
		fmt.Fprintf(&b, "- **Session:** %s", fu.Session)
		if fu.SessionNote != "" {
			fmt.Fprintf(&b, " (%s)", fu.SessionNote)
		}
		b.WriteString("\n")
	}
	if len(fu.Authors) == 0 {
		b.WriteString("- **Comments:** no trusted comments; acting on the launcher's instructions only\n")
	} else {
		total, names := 0, make([]string, 0, len(fu.Authors))
		for _, name := range slices.Sorted(maps.Keys(fu.Authors)) {
			total += fu.Authors[name]
			names = append(names, fmt.Sprintf("%s (%d)", followup.MarkdownName(name), fu.Authors[name]))
		}
		fmt.Fprintf(&b, "- **Comments used:** %d, by %s\n", total, strings.Join(names, ", "))
	}
	switch n := max(fu.UntrustedAuthorCount, len(fu.UntrustedAuthors)); {
	case fu.AuthorUnknown:
		fmt.Fprintf(&b, "- **Not used:** %s; the provider did not name the pull request's author, so no comment could be trusted\n", Plural(fu.UntrustedComments, "comment"))
	case n > 0:
		names := make([]string, len(fu.UntrustedAuthors))
		for i, name := range fu.UntrustedAuthors {
			names[i] = followup.MarkdownName(name)
		}
		fmt.Fprintf(&b, "- **Not used:** %s by %s outside the trusted list", Plural(fu.UntrustedComments, "comment"), Plural(n, "untrusted author"))
		if len(names) > 0 {
			fmt.Fprintf(&b, " (%s", strings.Join(names, ", "))
			if n > len(names) {
				fmt.Fprintf(&b, ", and %d more", n-len(names))
			}
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if fu.MarkersFromAnyone {
		b.WriteString("- **Note:** the provider could not tell Fugaro's own comments apart, so Fugaro's markers were honoured from every author\n")
	}
	if fu.NoNewCommits {
		b.WriteString("- **Commits:** no new commits; the branch is where this run started\n")
	}
	if fu.MovedToDraft {
		fmt.Fprintf(&b, "- **Draft:** was ready; moved back to draft because %s\n", reason)
	}
	b.WriteString("\n")
	if answer := strings.TrimSpace(fu.Answer); answer != "" {
		b.WriteString("**What the agent says it did** (`followup.md`):\n\n")
		for _, l := range strings.Split(answer, "\n") {
			b.WriteString(strings.TrimRight("> "+l, " ") + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("The agent wrote no `followup.md`.\n\n")
	}
	return b.String()
}

// policyLine is the report's note that fugaro.yaml values looser than the
// limits in force were dropped, one entry per key; empty when none were.
func policyLine(p *runstore.PolicyRecord) string {
	if p == nil || len(p.Ignored) == 0 {
		return ""
	}
	type entry struct{ first, last runstore.PolicyIgnored }
	seen, order, fromDefault := map[string]*entry{}, []string{}, false
	for _, ig := range p.Ignored {
		fromDefault = fromDefault || ig.From == policy.SourceDefaultBranch
		if e := seen[ig.Key]; e != nil {
			e.last = ig
			continue
		}
		seen[ig.Key] = &entry{ig, ig}
		order = append(order, ig.Key)
	}
	parts := make([]string, len(order))
	for i, k := range order {
		e := seen[k]
		parts[i] = fmt.Sprintf("%s %s -> %s", PolicyKeyPath(k), policyValue(k, e.first.Value), policyValue(k, e.last.Effective))
	}
	n := len(order)
	where, limits := " on this branch", "the project's limits"
	if fromDefault {
		where, limits = "", "the limits set above them"
	}
	were := "were"
	if n == 1 {
		were = "was"
	}
	return fmt.Sprintf("**Policy:** %s from fugaro.yaml%s %s looser than %s and %s ignored (%s)\n\n",
		Plural(n, "value"), where, were, limits, were, strings.Join(parts, "; "))
}

// policyValue is a limit's value for the report. An allow-list's models come
// from a branch, so they are quoted as code; the numbers and modes are ours.
func policyValue(key, v string) string {
	if v == "" {
		return "none"
	}
	if key == policy.KeyAllowedModels {
		return config.CodeSpan(v)
	}
	return v
}
