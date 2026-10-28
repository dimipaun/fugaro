package cli

// fugaro runs digest --submit-draft: a redacted learning draft a person can
// file as a "Skill learning" issue at Fugaro. It is the terminal half of
// D327/D328, and the one place a report leaves its own repository.
//
// Two properties shape it, and each has a test:
//
//   - The draft leaves the repository. A person pastes it into a public issue,
//     so it carries no repository name and no run ID: the runs are renumbered
//     run-1, run-2, ... and nothing else in it names this repository or one of
//     its runs. The figures are internal/digest's own closed vocabulary
//     (D323's set), which carries no person and no task text (G1).
//   - It sends nothing. There is no send seam to call: the command prints the
//     draft, and the person opens the issue form and sends it themselves
//     (D327). A learning is never filed by the command, by a run or by an
//     agent.
//
// The draft is built from the same read as the report -- the period's records
// and the committed reports under --dir, which the pattern rules read -- so
// the patterns it shows are the ones the report would print.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// digestSubmitNotice is the draft's first line: a machine-readable marker
// saying what wrote it and, for a reader who finds it later, that nothing was
// sent.
const digestSubmitNotice = "<!-- fugaro runs digest --submit-draft: a draft, nothing has been sent -->"

// digestSubmitLesson is the placeholder the person replaces with the lesson.
// It is one line, and it says what the lesson is for: the command can show the
// figures and never the lesson, which is the person's own reading of them.
const digestSubmitLesson = "<write the lesson here: what these runs show, and what you would change>"

// digestSubmitCommand is the one line that opens the issue form. It is the
// command the draft carries, and it is never run here: --web opens the form in
// the person's browser, so the person holds the submit button (D327).
//
// -R names Fugaro's own repository, because `gh issue create --web` resolves
// the repository from the working directory -- which is the checkout this
// command just read, the user's own -- and the "Skill learning" form lives only
// in Fugaro's repository. Without it a person who follows the draft lands on
// their own repository's new-issue form, which carries no such form, and files
// the learning in the wrong place.
//
// --template is deliberately absent: `gh issue create --web` ignores it and
// opens the repository's own chooser instead, so the draft names the form to
// pick there (digestSubmitForm) rather than passing a flag the browser drops.
const digestSubmitCommand = "gh issue create --web -R " + updatecheck.Repo

// digestSubmitForm is the form the person picks in the browser, named by the
// path it lives at in Fugaro's repository. --web opens the chooser rather than
// one form, so the draft says which form to pick and where it is.
const digestSubmitForm = ".github/ISSUE_TEMPLATE/skill_learning.yml"

// digestSubmitDraft is the learning draft: the patterns the fixed rules found,
// the period's runs renumbered, the figures, the line for the lesson, and the
// one command that sends it.
//
// It renders what the report put in s and runs and nothing else, so the draft
// carries no repository name, no run ID and no free text of any record (G1),
// and the same digest gives byte-identical bytes (G3).
func digestSubmitDraft(s digest.Summary, runs []digest.Run, patterns []digest.Pattern) []byte {
	var b strings.Builder
	b.WriteString(digestSubmitNotice + "\n\n")
	b.WriteString("# Skill learning draft\n\n")
	b.WriteString("A draft for a \"Skill learning\" issue. It leaves this repository, so it carries no\n" +
		"repository name, no run ID and no person. Read it, write the lesson, and send it\n" +
		"yourself.\n\n")
	b.Write(digest.PatternsMarkdown(patterns))
	b.WriteString("\n")
	if len(runs) > 0 {
		b.WriteString(digestSubmitRuns(runs))
	}
	b.WriteString(digestSubmitFigures(s))
	fmt.Fprintf(&b, "## The lesson\n\n%s\n\n", digestSubmitLesson)
	b.WriteString("## Send it\n\n")
	b.WriteString("Nothing has been sent. Review the draft, then open the issue form in your browser\n" +
		"with:\n\n```sh\n" + digestSubmitCommand + "\n```\n\n" +
		"`--web` opens the form chooser rather than one form, so pick \"Skill learning\"\n" +
		"(" + digestSubmitForm + "), paste the draft and send it.\n")
	return []byte(b.String())
}

// digestSubmitRuns is the period's runs as the draft shows them: the report's
// own table, with the run IDs renumbered, because a run ID names this
// repository and one of its runs and the draft leaves the repository. The
// numbering follows the order the report lists the rows in -- flagged first,
// then the dearest of the rest -- which is fixed, so the same records give the
// same numbers (G3).
func digestSubmitRuns(runs []digest.Run) string {
	renumbered := make([]digest.Run, len(runs))
	copy(renumbered, runs)
	for i := range renumbered {
		renumbered[i].ID = fmt.Sprintf("run-%d", i+1)
	}
	var b strings.Builder
	b.WriteString("## Runs\n\n")
	b.WriteString("Renumbered: a run ID names this repository and one of its runs, and this draft\n" +
		"leaves the repository.\n\n")
	b.WriteString(digest.RunsTable(renumbered))
	return b.String()
}

// digestSubmitFigures is the period's figures as the draft lists them: the
// summary block's own numbers and closed-vocabulary names (D323's set), one
// per line. A figure that is absent is left out rather than printed as a zero
// (G5).
func digestSubmitFigures(s digest.Summary) string {
	var b strings.Builder
	b.WriteString("## Figures\n\n")
	fmt.Fprintf(&b, "- period: %s (%s to %s)\n", s.Period, s.Start, s.End)
	fmt.Fprintf(&b, "- runs: %d\n", s.Runs)
	fmt.Fprintf(&b, "- shown: %d\n", s.Shown)
	fmt.Fprintf(&b, "- flagged: %d\n", s.Flagged)
	fmt.Fprintf(&b, "- halts: %d\n", s.Halts)
	fmt.Fprintf(&b, "- billed: $%s\n", s.Billed)
	fmt.Fprintf(&b, "- notional: $%s\n", s.Notional)
	fmt.Fprintf(&b, "- review: $%s\n", s.Review)
	if s.Dropped > 0 {
		fmt.Fprintf(&b, "- rows not shown: %d\n", s.Dropped)
	}
	if s.Months > 0 {
		fmt.Fprintf(&b, "- months rolled up: %d\n", s.Months)
	}
	if counts := submitCounts(s.Flags); counts != "" {
		fmt.Fprintf(&b, "- flags: %s\n", counts)
	}
	if recipes := submitRecipes(s.Recipes); recipes != "" {
		fmt.Fprintf(&b, "- recipes: %s\n", recipes)
	}
	b.WriteString("\n")
	return b.String()
}

// submitCounts is a flag count map as one line, in name order so the same
// figures give the same line (G3).
func submitCounts(flags map[string]int) string {
	names := slices.Sorted(maps.Keys(flags))
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, fmt.Sprintf("%s %d", name, flags[name]))
	}
	return strings.Join(out, ", ")
}

// submitRecipes is a recipe median map as one line, in name order (G3). A
// recipe name is the report's own vocabulary -- a catalog name or "other" --
// so it is no repository's word (G1).
func submitRecipes(recipes map[string]string) string {
	names := slices.Sorted(maps.Keys(recipes))
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, fmt.Sprintf("%s $%s", name, recipes[name]))
	}
	return strings.Join(out, ", ")
}
