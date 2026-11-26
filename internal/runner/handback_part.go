package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// The per-level hand-back in the account a run publishes
// (docs/rigor-modes/levels-implementation-plan.md LV4.3, levels-spec.md §2,
// levels-reconciliation.md LD5, LD7).
//
// The agent writes its hand-back sections into its own file — pr.md on a first
// run, followup.md on a follow-up, which is what LV4.2's parser reads and what
// the persona of the level the run resolved tells it to write (LV2.1). This
// file is the other half: what the runner does with them. The level's own row
// says which sections its account carries (rigor.HandBackOf, so the rule is one
// table and not a condition here), and the block below is that account, in the
// pull request's status section and in the report a follow-up posts, so a
// reader who never scrolls past the status section still sees what this level
// owed handed back.
//
// POC's is the one part the agent does not write: its persona says "one line of
// warning: this is a prototype, do not ship it" and the note on how to run it
// is the agent's own body above the section — the runner writes the warning, so
// the level whose whole account is that its work is not for production cannot
// lose that line to an agent that forgot it.
//
// Two rules bound the block, and both come from the contract LV4.2 settled:
//
//   - The text is the agent's own file and nothing else. A run whose file is
//     missing — the runner fell back to a body built from the task text
//     (rawPRText) — writes no hand-back block at any level, whatever headings
//     the task happens to carry: a brief that spells `## Revert plan` satisfies
//     nothing, which is the same injection the parser refuses at its own door.
//   - What is published is what parseHandBack returns, which is read from the
//     redacted text. Every line goes through inlineText like the section's
//     other free text, and every line is inside a bullet or indented under one,
//     so an entry cannot forge the section's markers or a line of the status
//     section's own.

// maxHandBackPartBytes bounds the hand-back block. It is small beside
// statusReserve because the block is one reader's aid in a section that also
// carries the outcome, the tests, the reviews and the cost, and a hand-back of
// ordinary size fits well inside it.
const maxHandBackPartBytes = 2 << 10

// handBack is the agent's hand-back as this run has it, and whether the agent
// wrote the file it comes from at all.
func (r *run) handBack() (HandBack, bool) {
	text, fromAgent := r.handBackText()
	if !fromAgent {
		return HandBack{}, false
	}
	hb, err := parseHandBack(text, true)
	if err != nil {
		// A file naming one section twice is the one shape the parser refuses.
		// The run is not failed over it — the account is the reader's aid and
		// the pull request is the run's work — but nothing of the file is shown
		// either: half of a hand-back is a list that reads as the whole one.
		r.d.Log.Warn("the hand-back file could not be read as a hand-back; the account carries none of it", "err", r.redact(err.Error()))
		return HandBack{}, false
	}
	return hb, true
}

// handBackText is the text of the file the agent wrote its hand-back in, and
// whether that file is there at all.
//
// A follow-up's is the text the report already quotes (answer): redacted,
// cleaned, its Fugaro markers stripped and its length bounded, which is what
// may be published, and the same text both readers of that file see so they
// cannot disagree. A first run's is read here rather than taken from
// rawPRText, which cannot say whether pr.md was there and whose fallback body
// is built from the task — the one text the hand-back may never be read from.
func (r *run) handBackText() (string, bool) {
	if r.follow != nil {
		a := r.answer()
		return a, strings.TrimSpace(a) != ""
	}
	f, err := openNoFollow(filepath.Join(r.d.StateDir, prFile))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.d.Log.Warn("reading pr.md for the hand-back failed", "err", r.redact(err.Error()))
		}
		return "", false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		r.d.Log.Warn("pr.md is not a regular file; not reading a hand-back from it")
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAnswerRead))
	if err != nil {
		r.d.Log.Warn("reading pr.md for the hand-back failed", "err", r.redact(err.Error()))
		return "", false
	}
	return agent.Redact(string(data), r.secretList()), true
}

// writeGateHandBack writes the hand-back a first run owes when the check gate's
// autofix takes implement's place: the gate commits the change itself, no coder
// stage runs, and readiness would otherwise refuse every such run for a file no
// agent could have written (the owner's ruling of 2026-11-26, LV5.1).
//
// It is the run's own account and not an agent's, and it goes to pr.md — the
// file a first run's hand-back is read from (handBackText) and the file the
// pull request's title and body come from (rawPRText) — so the pull request says
// what the gate changed and the readiness rule has the file it asks for. Every
// section is written, not only the ones the level carries right now: the level
// is resolved again at finalize from the paths the run changed (D22), so a run
// whose autofix reached an area of a higher level would otherwise owe a section
// this file left out. handBackBlock publishes the level's own row alone, so a
// section its level does not carry costs the reader nothing.
//
// The text it wrote and the commit it was written on are kept (gateHandBack,
// gateHandBackSHA): this account is written before the review loop, and the
// review loop's fix rounds commit on top of the autofix, which the account
// would still call the whole of the change and hand back a revert plan that
// leaves those commits on the branch. refreshGateHandBack rewrites it at
// finalize, where every commit is made.
//
// A failure to write is the caller's to report: an absent file is the draft
// every such run was before this, never a run failed over its own account.
func (r *run) writeGateHandBack(ctx context.Context) error {
	text, sha, err := r.gateHandBackText(ctx)
	if err != nil {
		return err
	}
	path := filepath.Join(r.d.StateDir, prFile)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|oNoFollow, 0o644)
	if err != nil {
		return fmt.Errorf("writing the check gate's hand-back: %w", err)
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return fmt.Errorf("writing the check gate's hand-back: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing the check gate's hand-back: %w", err)
	}
	// The redacted text is what a reader of the file gets back (handBackText),
	// which is what refreshGateHandBack compares against to tell this runner's
	// own file from an agent's.
	r.gateHandBack, r.gateHandBackSHA = agent.Redact(text, r.secretList()), sha
	return nil
}

// refreshGateHandBack rewrites the check gate's account when the branch has
// moved past the autofix commit it was written on (LV5.1): a review round's fix,
// or the leftover commit finalize makes, lands on top of the autofix, and the
// file would otherwise still call the autofix the whole of the change and hand
// the reader a revert plan that leaves the later commits on the branch.
// Readiness accepts the file either way — it is there and it carries the
// level's sections — so a stale account is published as it stands.
//
// The file is rewritten only while it is still the text this runner wrote: a
// fix round runs with the coder's own instructions, which name pr.md, so an
// agent may have written its own account on this path, and that one is the
// agent's to hand back.
func (r *run) refreshGateHandBack(ctx context.Context) error {
	if r.gateHandBackSHA == "" {
		return nil
	}
	if text, ok := r.handBackText(); !ok || text != r.gateHandBack {
		return nil
	}
	sha, err := r.repo.HeadSHA(ctx)
	if err != nil {
		return err
	}
	if sha == r.gateHandBackSHA {
		return nil
	}
	return r.writeGateHandBack(ctx)
}

// gateHandBackText is that account: the run's own title line, which is what the
// fallback body opens with (rawPRText), and one entry under each of the five
// sections an agent may write, each stating what the gate's autofix changed and
// nothing the runner does not know — the checks that passed on it are the ones
// the recipe named.
//
// It answers with the commit it describes as well: the head the run is on when
// it is written, which is what refreshes are judged against.
func (r *run) gateHandBackText(ctx context.Context) (string, string, error) {
	sha, err := r.repo.HeadSHA(ctx)
	if err != nil {
		return "", "", err
	}
	short := shortGitHead(sha)
	// autofix is the commit the gate's own autofix is, and later says whether
	// the branch has moved past it: this run wrote the file before the review
	// loop, so a head other than the one recorded is a fix round's (or the
	// leftover commit's) on top of the autofix — not an autofix that never
	// happened.
	autofix, later := short, ""
	if r.gateHandBackSHA != "" && sha != r.gateHandBackSHA {
		autofix = shortGitHead(r.gateHandBackSHA)
		later = fmt.Sprintf("; no implement stage ran, and the run committed more on top of it, up to `%s`", short)
	}
	subject := "the check gate's autofix"
	if later != "" {
		subject += " and the commits the run made after it"
	}
	checks := make([]string, 0, len(r.gateSteps))
	for _, s := range r.gateSteps {
		checks = append(checks, "`commands."+string(s.Command)+"`")
	}
	// The range of the run's own commits, from the commit it was cut from
	// (workBase) to the head it publishes: what reverting it undoes, whatever
	// the run committed after the autofix.
	from := r.workBase()
	if validGitHead(from) {
		from = shortGitHead(from)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", r.titleLine())
	fmt.Fprintf(&b, "Opened by Fugaro run `%s`.\n\n", task.ShortRun(r.rec.RunID))
	para := func(head string, entries ...string) {
		b.WriteString("## " + head + "\n\n")
		for _, e := range entries {
			b.WriteString("- " + e + "\n")
		}
		b.WriteString("\n")
	}
	para(handBackDeferred, "nothing: the whole of the change is "+subject)
	para(handBackOpenRisks, fmt.Sprintf("none known: the repository's own checks passed on the autofix commit `%s`", autofix))
	para(handBackVerification, fmt.Sprintf(
		"the check gate's autofix (`commands.fix`) changed the branch and %s passed on the autofix commit `%s`%s",
		strings.Join(checks, ", "), autofix, later))
	para(handBackRevertPlan, fmt.Sprintf("revert every commit the run made: `git revert %s..%s` (`%s` is where the run started)", from, short, from))
	para(handBackMorningNote, "what changed: "+subject+"; nothing else is known to need a fix")
	return b.String(), sha, nil
}

// handBackFile is the file this run's hand-back is written in, which the block
// names so a reader can go and read the whole of it.
func (r *run) handBackFile() string {
	if r.follow != nil {
		return followupFile
	}
	return prFile
}

// handBackPart is the section's hand-back block, or "" for a run whose agent
// wrote no file to hand back in.
func (r *run) handBackPart() string {
	hb, ok := r.handBack()
	if !ok {
		return ""
	}
	return handBackBlock(rigor.HandBackOf(r.reachedRigor()), hb, r.handBackFile())
}

// handBackBlock renders the level's hand-back: the sections want says the level
// carries, each with the entries the file held, and POC's own line.
//
// A section the level does not carry is not shown even where the file writes
// it — a POC run's `## Deferred` is not POC's to hand back, and showing it
// would be the runner reading a section the level's persona says its agent does
// not keep. A section the level carries and the file did not write is not shown
// either: this block is what was handed back, and a heading over nothing is the
// absence the readiness rule names (LV5.1) rather than a list.
//
// "" when that leaves nothing to say, which is every run whose level owes no
// line of the runner's and whose file wrote none of its sections.
func handBackBlock(want rigor.HandBack, hb HandBack, file string) string {
	head := "**Hand-back** (`" + file + "`)\n"
	out := head
	para := func(paras ...string) { out += "\n" + strings.Join(paras, "\n\n") + "\n" }
	if want.Prototype {
		para("This is a prototype; do not ship it.")
	}
	// room is what is left for the next section's entries. The head of every
	// section goes under it together with hiddenHandBackLineCap, so an entry
	// that is shown is shown whole and the count that closes a bounded list is
	// always written: a section whose head would not fit is left out entirely
	// rather than written over nothing, and the block stays inside its bound
	// however many entries the file carried.
	room := maxHandBackPartBytes - len(out) - hiddenHandBackLineCap
	for _, s := range []struct {
		name  string
		carry bool
		sec   HandBackSection
	}{
		{handBackDeferred, want.Deferred, hb.Deferred},
		{handBackOpenRisks, want.OpenRisks, hb.OpenRisks},
		{handBackVerification, want.Verification, hb.Verification},
		{handBackRevertPlan, want.RevertPlan, hb.RevertPlan},
		{handBackMorningNote, want.MorningNote, hb.MorningNote},
	} {
		if !s.carry || len(s.sec.Entries) == 0 {
			continue
		}
		title := fmt.Sprintf("**%s** (`## %s`)", s.name, s.name)
		use := len(title) + len("\n\n\n") + hiddenHandBackLineCap
		if use > room {
			break
		}
		room -= use
		body := make([]string, 0, len(s.sec.Entries))
		for _, e := range s.sec.Entries {
			entry := handBackEntry(e)
			if len(entry)+1 > room {
				break
			}
			body = append(body, entry)
			room -= len(entry) + 1
		}
		if hidden := len(s.sec.Entries) - len(body) + s.sec.Dropped; hidden > 0 {
			body = append(body, hiddenHandBackLine(hidden))
		}
		para(title + "\n\n" + strings.Join(body, "\n"))
	}
	if out == head {
		return ""
	}
	return out
}

// hiddenHandBackLineCap is the room kept for the line that closes a bounded
// section. The line names a count of at most a couple of hundred entries — the
// parser's own bound (maxHandBackEntries) and the renderer's cannot add up to
// more — so this is the whole of it and the bound above holds without measuring
// the line first.
const hiddenHandBackLineCap = 64

// hiddenHandBackLine is what the block says about the entries it does not show:
// the ones its own bound left out and the ones the parser's did
// (HandBackSection.Dropped), together, so a reader is never handed a list that
// reads as complete and is not.
func hiddenHandBackLine(hidden int) string {
	// Spelled here rather than through Plural, whose rule is the regular "+s"
	// and would print "entrys": this is the one noun the section counts.
	noun := "entries"
	if hidden == 1 {
		noun = "entry"
	}
	return fmt.Sprintf("- and %d %s, which this account does not show", hidden, noun)
}

// handBackEntry renders one entry as one bullet: its first line after the
// marker, the lines under it indented, each through inlineText so an entry
// carrying a heading, a marker or a line break of its own stays the text it is
// rather than structure of the section's.
//
// An entry runs over several lines by the parser's own rule — the lines under
// its bullet are the entry's — so the shape the agent wrote is kept rather than
// flattened: a deferred item whose reasons are a nested list is read and shown
// as one entry.
func handBackEntry(e string) string {
	lines := strings.Split(e, "\n")
	var b strings.Builder
	b.WriteString("- " + inlineText(lines[0]))
	for _, l := range lines[1:] {
		if t := inlineText(l); t != "" {
			b.WriteString("\n  " + t)
		}
	}
	return b.String()
}
