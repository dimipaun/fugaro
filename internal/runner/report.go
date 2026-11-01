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
	return followUpReport(rec, location, tail, fu, nil)
}

// followUpReport is FollowUpReport with notes about the pull request itself
// (reviewers that could not be requested, a host without drafts), each
// shown as a line after the outcome.
func followUpReport(rec *runstore.Record, location string, tail *LogTail, fu *FollowUpSection, notes []string) string {
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
	case runstore.OutcomeArtifacts:
		// Without this arm an artifact run falls into the default below and
		// reads as a draft pull request with an empty reason, which is the
		// one thing it is not: it opens no pull request at all.
		b.WriteString("**Outcome:** artifacts — the deliverable is in this run's bucket, no pull request\n\n")
	default:
		fmt.Fprintf(&b, "**Outcome:** draft — %s\n\n", rec.Reason)
	}
	for _, n := range notes {
		fmt.Fprintf(&b, "**Note:** %s\n\n", n)
	}
	if len(rec.Stages) > 0 {
		b.WriteString("| Stage | Duration |\n|---|---|\n")
		for _, s := range rec.Stages {
			d := time.Duration(s.DurationS * float64(time.Second)).Round(time.Second)
			fmt.Fprintf(&b, "| %s | %s |\n", s.Name, d)
		}
		b.WriteString("\n")
	}
	if rec.Outcome == runstore.OutcomeArtifacts {
		b.WriteString(artifactsSection(rec))
	}
	if len(rec.Reviews) > 0 {
		parts := make([]string, 0, len(rec.Reviews))
		for _, r := range rec.Reviews {
			p := fmt.Sprintf("round %d: %s", r.Round, r.Verdict)
			if r.Tier != "" {
				p = r.Tier + " " + p
			}
			if r.Verdict != "ship" && r.Verdict != "none" {
				p += " (" + Plural(r.Findings, "finding") + ")"
			}
			parts = append(parts, p)
		}
		fmt.Fprintf(&b, "**Reviews:** %s\n\n", strings.Join(parts, "; "))
	}
	// The mode and the findings it let through, right under the reviews whose
	// rounds it decided: the line says what the run was held to, and the table
	// says what that left unfixed (docs/rigor-modes/implementation-plan.md
	// T4.2, D9, F19).
	b.WriteString(rigorLine(rec.Rigor))
	b.WriteString(toleratedSection(rec))
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

// artifactsSection is the report's Artifacts section (design §9) for a
// mode: artifact run: what the render step did, the files the run uploaded
// with the probe's verdict on each, and the compute estimate the run's wall
// clock priced. The per-file table is written from the record's own
// artifacts, so a record that carries none (a release without the upload
// step, or a run that ended before it) says so rather than printing a header
// over nothing.
func artifactsSection(rec *runstore.Record) string {
	var b strings.Builder
	b.WriteString("#### Artifacts\n\n")
	if r := rec.Render; r != nil {
		exit := fmt.Sprintf("exit %d", r.ExitCode)
		if r.TimedOut {
			exit += ", timed out"
		}
		fmt.Fprintf(&b, "- **Render:** `%s` in the %s tree, %s, %s\n", r.Command, r.Tree, exit, secsText(r.DurationS))
		fmt.Fprintf(&b, "- **Command sha256:** `%s`\n", r.CommandSHA256)
		if c := rec.Cost; c != nil && c.ComputeEstimated {
			// The figure is priced from the run's wall clock (updateCost), so
			// it is never attributed to the render's seconds: a short render
			// on a long run would otherwise read as its own price.
			fmt.Fprintf(&b, "- **Compute:** $%.4f estimated from the run's wall clock, of which the render is %s\n", c.ComputeUSD, secsText(r.DurationS))
		}
	} else {
		b.WriteString("- **Render:** none recorded: the run ended before its render step ran.\n")
	}
	if len(rec.Artifacts) == 0 {
		// Reported, not silent: a render that matched none of the recipe's
		// globs is not an error by itself (design §8.4), so the record
		// carries no artifacts and this line is where a reader learns that
		// the run uploaded nothing rather than that the section is missing.
		b.WriteString("\n**Files:** none recorded: the run uploaded no artifacts (the render matched none of the recipe's `outputs.globs`, or the run ended before its upload step).\n\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n%d files, uploaded to this run's bucket under `%s`:\n\n", len(rec.Artifacts), runstore.ArtifactPrefix)
	b.WriteString("| Artifact | Bytes | sha256 | Type | Probe |\n|---|---|---|---|---|\n")
	for _, a := range rec.Artifacts {
		fmt.Fprintf(&b, "| %s | %d | `%s` | %s | %s |\n", tableCode(a.Name), a.Bytes, shortSum(a.SHA256), tableText(a.ContentType), probeCell(a))
	}
	b.WriteString("\nEach row is what the run's own record says the object at that name holds: download it and hash it to check the bytes arrived as rendered. In the Probe column, `ok` is the recipe's own `commands.probe` having exited zero on the whole set, `none` is a recipe that asked for no probe, and `—` is a record written before the probe step existed, which carries no verdict to read.\n\n")
	return b.String()
}

// probeCell is the table's probe column: what the probe step said about this
// artifact. "none" is the recipe's own answer when it asked for no probe —
// the record carries {ran: false, exit_code: -1}, and the column says so
// rather than leaving a reader to infer it from an empty cell. A recorded
// non-zero exit code is printed as such rather than folded into "ok": the
// table must never claim a check passed when the record says it did not.
//
// A record with NO verdict is a third thing and gets a third answer. It is a
// record written before the probe step existed (the schema's own description
// of the absent key), and what the run knows about it is that nothing was
// recorded — not that the recipe asked for no probe. Rendering it "none"
// would put the recipe's intent into a record that cannot know it.
func probeCell(a runstore.ArtifactFile) string {
	switch {
	case a.Probe == nil:
		return "—"
	case !a.Probe.Ran:
		return "none"
	case a.Probe.ExitCode == 0:
		return "ok"
	default:
		return fmt.Sprintf("exit %d", a.Probe.ExitCode)
	}
}

// shortSum is a sha256 as a report row shows it: the first twelve hex
// characters, which is what a reader compares by eye. The full hash is in
// result.json, which is what a reader verifies with.
func shortSum(sum string) string {
	const n = 12
	if len(sum) > n {
		return sum[:n]
	}
	return sum
}

// secsText is a recorded duration in seconds for a report line.
func secsText(s float64) string {
	return time.Duration(s * float64(time.Second)).Round(time.Second).String()
}

// rigorLine is the mode the run resolved as the report prints it: the block's
// own words (runstore.RigorRecord.Line, which `fugaro diagnose` prints too)
// under the report's label, so the pull request and the diagnosis cannot say
// different things about one fact.
//
// "" for a record carrying no block, which is every run that resolved no mode
// and every record written before the field existed: an absent answer is not
// the built-in default, and a line claiming one would name a mode nobody
// resolved (D18's one-value rule).
func rigorLine(rg *runstore.RigorRecord) string {
	if rg == nil {
		return ""
	}
	return "**Rigor:** " + rg.Line() + "\n\n"
}

// toleratedSection is the report's list of the findings the run's resolved
// mode let through (T4.2, D9, F19): the block `fugaro diagnose --json` prints
// back from the record under `tolerated`, one row per finding — the score the
// mode's table read, the word a person reads for it, the mode that tolerated it
// and the reviewer's own words for the finding — so the pull request says what
// was left unfixed and not only how many findings the round had.
//
// "" for a record carrying no block, which is every run that tolerated
// nothing: an absent block is a different fact from a block that lists none,
// and a section over an empty list would claim a decision no mode made.
//
// "" too for a round the mode blocked. The record keeps the tolerated half of
// every round the split scored, a mixed round's included, while the body names
// what was tolerated only where the round was let through whole (prflow.go
// toleratedPart): a table over that half would claim a decision Decide never
// made, and it is the round's own count printed above it that a reader goes by.
func toleratedSection(rec *runstore.Record) string {
	tf := rec.Tolerated
	if tf == nil || len(tf.Findings) == 0 {
		return ""
	}
	// The split is a partition of the round (rigor.Split), so the findings the
	// record counts as tolerated are all of them — the only count a round the
	// mode let through whole can leave — and fewer of them is a round Decide
	// blocked on the rest. The comparison is against the record's own total
	// rather than the rows, which the cap can shorten.
	last := seniorReview(rec.Reviews)
	if last == nil || tf.Total != last.Findings {
		return ""
	}
	var b strings.Builder
	b.WriteString("#### Tolerated\n\n")
	b.WriteString("| Label | Likelihood | Impact | Fix cost | Class | Mode | Finding |\n|---|---|---|---|---|---|---|\n")
	for _, f := range tf.Findings {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %s | %s | %s |\n",
			tableText(string(f.Label)), f.Likelihood, f.Impact, f.FixCost,
			tableText(string(f.Class)), tableText(string(f.Mode)), toleratedCell(f.Title))
	}
	if tf.Total > len(tf.Findings) {
		// The table is not the whole answer, and it says so rather than
		// passing its last row off as the last finding the mode tolerated.
		// The gap is not always the record's cap (runstore.MaxTolerated):
		// NewTolerated also drops an entry it cannot read, so the rows are
		// "some of" the total and never "the first" of it.
		fmt.Fprintf(&b, "\n%s tolerated in all; the record lists %d of them.\n", Plural(tf.Total, "finding"), len(tf.Findings))
	}
	b.WriteString("\n")
	return b.String()
}

// toleratedCell renders a reviewer's title into the one table cell it can
// break. The title is the reviewer's own text: a newline in it would end the
// row, and the line after it would be read as Markdown of its own — a title
// opening "# " becomes a heading — so its whitespace is flattened first. What
// is left is a value the render's own text chose, like an artifact's name, so
// it goes through tableCode: the pipe that would slide the columns is escaped
// and the fence is longer than any backtick run in it.
func toleratedCell(title string) string {
	return tableCode(strings.Join(strings.Fields(title), " "))
}

// testsLine is the report's account of the suite on the final commit: the run
// writes nothing after the commit it tested (finalize), so the only way the
// final commit is not the tested one is the leftover work finalize committed,
// and that is a draft whose reason says so rather than a line here.
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
	s += ")"
	return s + "\n\n"
}

// tableText renders a value into a markdown table cell. A pipe ends the cell
// — every value after it slides a column, so a reader sees the render's text
// where the run's account should be — and GFM's own escape for it is a
// backslash, which is why the value is escaped rather than the row rebuilt.
// An artifact's name is the render's to choose, so this is reachable: the
// sanitiser refuses control characters and keeps punctuation.
func tableText(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// tableCode is tableText as a code span, which is how the report shows a name
// or a hash. A backtick inside the value would end the span, so the fence is
// one backtick longer than the longest run of them in the value, and a value
// that starts or ends with one gets GFM's own padding space, which the code
// span's rule strips again.
func tableCode(s string) string {
	fence := "`"
	if n := longestRun(s, '`'); n > 0 {
		fence = strings.Repeat("`", n+1)
	}
	body := tableText(s)
	if strings.HasPrefix(body, "`") || strings.HasSuffix(body, "`") {
		body = " " + body + " "
	}
	return fence + body + fence
}

// longestRun is the length of the longest run of c in s.
func longestRun(s string, c rune) int {
	longest, run := 0, 0
	for _, r := range s {
		if r == c {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// fence returns a backtick fence longer than any backtick run in s, so s
// cannot end the code block early.
func fence(s string) string {
	return strings.Repeat("`", max(3, longestRun(s, '`')+1))
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

// policyLine is the report's note that the run dropped values a layer wrote:
// the fugaro.yaml values looser than the limits in force, one entry per key,
// and the stage timeouts the run's own timeouts.stage bounds at least as
// tightly (T7.6), which are not looser than a limit but get a sentence of
// their own so the report does not call them one. Empty when nothing was.
//
// The stage-timeout sentence is for the entries Clamped marks, and only those.
// A stage timeout a tighter layer capped is a merge entry like any other — its
// key, source and from can read exactly like a clamp's — and it takes the
// "looser than the project's limits" sentence, as it should; a key that has
// both shapes gets a part in each sentence.
func policyLine(p *runstore.PolicyRecord) string {
	if p == nil || len(p.Ignored) == 0 {
		return ""
	}
	type entry struct{ first, last runstore.PolicyIgnored }
	seen, order, fromDefault := map[string]*entry{}, []string{}, false
	// The clamped entries, kept apart: the drop's why is the run's own bound
	// and never "looser than the project's limits" (the value can come from
	// the project layer itself), so they must not join the sentence below.
	var clampedKeys []string
	clamped := map[string]runstore.PolicyIgnored{}
	for _, ig := range p.Ignored {
		if ig.Clamped {
			if _, ok := clamped[ig.Key]; !ok {
				clampedKeys = append(clampedKeys, ig.Key)
			}
			clamped[ig.Key] = ig
			continue
		}
		fromDefault = fromDefault || ig.From == policy.SourceDefaultBranch
		if e := seen[ig.Key]; e != nil {
			e.last = ig
			continue
		}
		seen[ig.Key] = &entry{ig, ig}
		order = append(order, ig.Key)
	}
	var b strings.Builder
	if n := len(order); n > 0 {
		parts := make([]string, n)
		for i, k := range order {
			e := seen[k]
			parts[i] = fmt.Sprintf("%s %s -> %s", PolicyKeyPath(k), policyValue(k, e.first.Value), policyValue(k, e.last.Effective))
		}
		where, limits := " on this branch", "the project's limits"
		if fromDefault {
			where, limits = "", "the limits set above them"
		}
		were := "were"
		if n == 1 {
			were = "was"
		}
		fmt.Fprintf(&b, "**Policy:** %s from fugaro.yaml%s %s looser than %s and %s ignored (%s)\n\n",
			Plural(n, "value"), where, were, limits, were, strings.Join(parts, "; "))
	}
	if n := len(clampedKeys); n > 0 {
		parts := make([]string, n)
		for i, k := range clampedKeys {
			ig := clamped[k]
			parts[i] = fmt.Sprintf("%s %s from %s -> %s",
				PolicyKeyPath(k), policyValue(k, ig.Value), policyFrom(ig.From), policyValue(k, ig.Effective))
		}
		were := "were"
		if n == 1 {
			were = "was"
		}
		fmt.Fprintf(&b, "**Policy:** the run's own timeouts.stage bounds a stage at least as tightly as the stage timeout asked for, so %s %s ignored (%s)\n\n",
			Plural(n, "value"), were, strings.Join(parts, "; "))
	}
	return b.String()
}

// policyFrom names the layer that asked for a dropped stage timeout, as
// PolicyIgnored.From spells it (the ceiling there is the project layer's
// governance, which the run drops when its own timeouts.stage is tighter).
func policyFrom(from string) string {
	switch from {
	case policy.SourceCeiling:
		return "the project layer"
	case policy.SourceDefaultBranch:
		return "the default branch's fugaro.yaml"
	default:
		return "fugaro.yaml on this branch"
	}
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
