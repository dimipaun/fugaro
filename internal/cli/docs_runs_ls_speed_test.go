package cli

// RL5 of docs/runs-ls-speed: the documents, the plugin skills and the release
// line that describe the fast `runs ls` RL1, RL2W, RL2B and RL3 built.
//
// The task's own red step is these pins: the sentences are a claim about
// behaviour a test can measure (RL1's live set, RL3's index), and a doc or a
// skill that does not carry them leaves a reader — an agent in a session, most
// of all — reading the slow listing as the only one there is, or reading the
// one line the fallback prints as a failure. So the three sentences are pinned
// where they live: the residual of D2964 in the design of record, the row the
// backfill added to the in-flight audit now that its reader exists, the two
// skills' own words, and the release the whole thing ships in.

import (
	"os"
	"strings"
	"testing"
)

const (
	// The design of record the bucket's grants and their limits live in
	// (docs/bucket-iam/spec.md §10, the threat model).
	runsLsSpeedBucketIAM = "docs/bucket-iam/spec.md"
	// The step-by-step audit of `fugaro update` while runs are in flight:
	// RL2B wrote the index row, and RL3 is the release that makes runs read it.
	runsLsSpeedUpdateInFlight = "docs/design/update-in-flight.md"
	// The working skill, and the reference that owns the listing's own words:
	// the SKILL.md is at its byte budget (plugin/skills_lint_test.go's
	// maxSkillBytes), so its line names the two fast queries and the detail
	// sits in the page a reader of "what is running" is sent to.
	runsLsSpeedWorkingSkill = "plugin/skills/working/SKILL.md"
	runsLsSpeedWorkingRef   = "plugin/skills/working/reference/status.md"
	// The landing skill: the sweep a land starts from reads `--pr N`.
	runsLsSpeedLanding = "plugin/skills/landing/SKILL.md"
	// The release in progress, which is the one that carries RL3: the CLI
	// stream is at 0.26.1 and its next minor is 0.27.0
	// (docs/release-streams.yaml), and a file above the stream's `current` is
	// the next release's notes (docs/releases/README.md).
	runsLsSpeedNotes = "docs/releases/v0.27.0.md"
	// The line `runs ls --pr N` prints on stderr when the pull request has no
	// index entry: runs_pr.go's own wording, read off it here so the skills and
	// the CLI cannot spell it differently.
	runsLsSpeedFallbackNote = "note: no pull-request index for <repo>#<N>: reading every run of the window, as it did before the index existed"
)

// TestDocsBucketIAMNamesThePRIndexResidual: the index is a hint a hostile run
// can forge and erase (F4, D2963, D2964), and the design of record states the
// residual where the shared job's own reach is stated. Two sentences: the
// erasure hides a run older than the 24-hour window the listing unions the
// index with, and a forged entry adds no row, because every run the index names
// is read and checked against its own records.
func TestDocsBucketIAMNamesThePRIndexResidual(t *testing.T) {
	doc := flattenDoc(readDoc(t, runsLsSpeedBucketIAM))
	for _, want := range []struct{ phrase, why string }{
		{"It can also write and delete the pull-request index entries under `runs/<slug>/pr-index/`",
			"the threat model does not name the index among the objects a compromised agent writes, and the job account is what writes it (D2962)"},
		{"hides a run older than the 24-hour window the listing unions the index with",
			"the erasure's residual is not stated: what it can hide is bounded by the recent-window union, and a reader has to know which bound (D2964, F5)"},
		{"a forged entry adds a read and never a row, because every run the index names is read and checked against its own records",
			"the other half is not stated: the index is a hint and never a source of rows, which is what keeps a forged entry from inventing one (D2963)"},
		{"the fallback that scans the window as the listing did before the index existed is the empty index's alone, so one entry — forged, or an ordinary run's own — turns it off for the whole pull request",
			"the forged entry is stated as costing a read and no row, and nothing more; runs_pr.go falls back to the window scan only when the index is empty, so one entry turns that fallback off for the whole pull request, and its runs older than the 24-hour union are hidden the way an erased entry's are"},
		{"a run of it older than the 24-hour union the listing reads beside the index drops out unless the index names it",
			"the residual the fallback's suppression hides is not stated: which runs an index that holds anything leaves out (F9, D2964)"},
	} {
		if !strings.Contains(doc, want.phrase) {
			t.Errorf("%s does not say %q: %s", runsLsSpeedBucketIAM, want.phrase, want.why)
		}
	}
	// The sentence the row's own reasoning replaced: before RL3 the index was
	// written and unread, and the audit said so. A page that still says no run
	// reads it describes a tree that no longer exists.
	if strings.Contains(doc, "no run reads the index") {
		t.Errorf("%s still says no run reads the index, and `runs ls --pr N` does since 0.27.0 (D2964)", runsLsSpeedBucketIAM)
	}
}

// TestDocsUpdateInFlightIndexRowIsCurrent: RL2B added the backfill's row to the
// audit, and its exposure sentence said no run read the index yet. The release
// that carries RL3 is the one that makes a run read it, so the row is rewritten
// in the same sweep (the plan's RL5: "docs/design/update-in-flight.md is
// current after RL2B"). What the row must keep saying is the half that matters:
// the index is a hint a reader falls back from, so the backfill's timing is
// never something a run waits for.
func TestDocsUpdateInFlightIndexRowIsCurrent(t *testing.T) {
	doc := flattenDoc(readDoc(t, runsLsSpeedUpdateInFlight))
	for _, want := range []struct{ phrase, why string }{
		{"the pull-request index (D2965)", "the backfill's row is gone from the step table"},
		{"the index is a hint a reader falls back from and never waits for",
			"the row does not say what the index is to a reader, which is the reason the step can be safe for a run in flight at all (D2964)"},
	} {
		if !strings.Contains(doc, want.phrase) {
			t.Errorf("%s does not say %q: %s", runsLsSpeedUpdateInFlight, want.phrase, want.why)
		}
	}
}

// TestDocsUpdateInFlightSummaryRowIsCurrent: RL4's backfill runs inside the
// same `fugaro update` the index backfill does, from the same place
// (init_stages.go's outcome), so the audit's table carries its row beside that
// one and states the half a run in flight turns on: a run whose record still
// says running is skipped and never waited for, and a reader falls back to the
// run's own three objects wherever the summary is absent or unusable.
//
// The row's symbols are checked too: a row naming a writer that has been
// renamed is a page that sends its reader nowhere.
func TestDocsUpdateInFlightSummaryRowIsCurrent(t *testing.T) {
	doc := flattenDoc(readDoc(t, runsLsSpeedUpdateInFlight))
	for _, want := range []struct{ phrase, why string }{
		{"the run summaries (D2966)", "the summary backfill's row is gone from the step table"},
		{"a hint a reader falls back from and never waits for",
			"the row does not say what the summary is to a reader, which is the reason the step can be safe for a run in flight at all"},
		{"its record still says `running`",
			"the skip is not stated, so a reader cannot tell that the walk neither writes under a run that is going nor waits for it"},
		{"backfillRunSummary", "the row does not name the writer the code search lands on"},
	} {
		if !strings.Contains(doc, want.phrase) {
			t.Errorf("%s does not say %q: %s", runsLsSpeedUpdateInFlight, want.phrase, want.why)
		}
	}
	step, err := os.ReadFile("update_summary.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(step), "func (e *initEngine) backfillRunSummary(") || !strings.Contains(string(step), "func backfillRunSummariesIn(") {
		t.Errorf("%s's row names backfillRunSummary and backfillRunSummariesIn, which internal/cli/update_summary.go no longer holds: the row sends its reader to a function this tree does not have", runsLsSpeedUpdateInFlight)
	}
	if w, err := os.ReadFile("../runstore/summary.go"); err != nil || !strings.Contains(string(w), "func CreateSummary(") {
		t.Errorf("%s's row names runstore.CreateSummary, the create-if-absent write the step uses; internal/runstore/summary.go does not hold it (%v)", runsLsSpeedUpdateInFlight, err)
	}
}

// TestDocsWorkingSkillNamesTheFastQueries: an agent in a session reports run
// state constantly, and two of the listings it reaches for are answered without
// walking the window — `--state running` from the live set (RL1), `--pr N` from
// the pull request's own index (RL3) — while every other one reads the window.
// The skill has to say which is which, and what the one fallback line means: it
// is a slower read and not a different one, so an agent that saw it must not
// report missing rows or a failure.
func TestDocsWorkingSkillNamesTheFastQueries(t *testing.T) {
	skill := flattenDoc(readDoc(t, runsLsSpeedWorkingSkill))
	for _, want := range []struct{ phrase, why string }{
		{"`--state running` and `--pr N` are the fast queries", "the skill's listing line does not say which listings are cheap, so an agent reads the slow sweep as the only one there is"},
		{"reference/status.md", "the listing line does not send the reader to the page that carries the detail"},
	} {
		if !strings.Contains(skill, want.phrase) {
			t.Errorf("%s does not say %q: %s", runsLsSpeedWorkingSkill, want.phrase, want.why)
		}
	}

	ref := flattenDoc(readDoc(t, runsLsSpeedWorkingRef))
	for _, want := range []struct{ phrase, why string }{
		{"`--state <state>` for one state", "the reference no longer has the bullet the state words are read out of (plugin/routing_skill_test.go's pin)"},
		{"those two are answered from the live set", "the reference still says a state filter reads every run of the window first, which the two live states no longer do (RL1, D2960)"},
		{"it is the other fast query: it reads the index the runs keep for that pull request, plus the runs of the last day", "the `--pr N` bullet does not say what the listing reads, so a reader cannot tell why it is fast or what the fallback costs (RL3, D2964)"},
		{"the listing prints one line on stderr and scans the window instead", "the fallback is not stated where the flag is documented (F9)"},
		{runsLsSpeedFallbackNote, "the reference does not name the line the CLI prints, so an agent that saw it cannot recognise it"},
		{"The rows are the same and only the read is slower", "a reader who saw the note is not told it changed nothing about the answer, which is the whole point of a fallback (D2964)"},
	} {
		if !strings.Contains(ref, want.phrase) {
			t.Errorf("%s does not say %q: %s", runsLsSpeedWorkingRef, want.phrase, want.why)
		}
	}
	// The sentence the live set made false, and the one that would contradict
	// the two above if it survived: a reader of the `--state` bullet must not be
	// told that every state filter walks the window.
	if strings.Contains(ref, "It reads every run in the window first, because only a finished run's own record says what its state is.") {
		t.Errorf("%s still says a state filter reads every run of the window first, and `running` and `launching` read the live set (RL1, D2960)", runsLsSpeedWorkingRef)
	}
}

// TestDocsLandingSkillNamesTheFastQuery (plugin skills are a security path: the
// landing skill is what decides which pull requests a person is shown): a land
// reads a pull request's runs through `--pr N`, and the skill has to say that
// this listing is the cheap one and what the fallback line means — a stderr
// note about how the listing was read, never a warning about the pull request
// and never a reason to tell the person something is wrong with it.
func TestDocsLandingSkillNamesTheFastQuery(t *testing.T) {
	skill := flattenDoc(readDoc(t, runsLsSpeedLanding))
	for _, want := range []struct{ phrase, why string }{
		{"`--pr N` and `--state running` are the two listings that do not walk the window",
			"the landing skill does not say which listings are cheap, and its sweep is the one a person waits on"},
		{"the first reads the pull request's own index of runs, the second the runs that are going",
			"the skill does not say what makes the two fast, so a reader cannot judge the fallback below"},
		{"it says so on stderr", "the fallback is not stated"},
		{runsLsSpeedFallbackNote, "the skill does not name the line the CLI prints, so an agent that saw it cannot recognise it"},
		{"the rows are the same, the wait longer", "a reader who saw the note is not told it changed nothing about the answer"},
	} {
		if !strings.Contains(skill, want.phrase) {
			t.Errorf("%s does not say %q: %s", runsLsSpeedLanding, want.phrase, want.why)
		}
	}
}

// TestDocsRunsLsSpeedReleaseNotesCarryIt: the release notes in progress are the
// page a person reads before upgrading, and the CLI stream's next minor is the
// release that carries RL1, RL2W, RL2B and RL3. The Highlights line says what a
// user gets; the `### For operators` line is the one command plus the backfill,
// which is the whole of the operator's work (CLAUDE.md's HARD RULE).
func TestDocsRunsLsSpeedReleaseNotesCarryIt(t *testing.T) {
	doc := flattenDoc(readDoc(t, runsLsSpeedNotes))
	for _, want := range []struct{ phrase, why string }{
		{"`--pr N` and `--state running`", "the notes never name the queries that got faster"},
		{"read a small index of a pull request's runs and the executions the cloud is still running",
			"the notes do not say what the two queries read now"},
		{"23 s for one row and 20 s for none", "the notes do not carry the numbers the change is measured by"},
		{"one line on stderr saying so", "the notes do not say what happens to a pull request with no index yet"},
		{"the rows and `--json` are unchanged", "the notes do not say the change is speed alone, which is what a reader of `--json` needs"},
	} {
		if !strings.Contains(doc, want.phrase) {
			t.Errorf("%s does not say %q: %s", runsLsSpeedNotes, want.phrase, want.why)
		}
	}
	operators := flattenDoc(docSection(t, runsLsSpeedNotes, readDoc(t, runsLsSpeedNotes), "### For operators"))
	for _, want := range []struct{ phrase, why string }{
		{"run `fugaro update`", "the operator section does not name the one documented command (HARD RULE)"},
		{"`fugaro update` writes the pull-request index for the last 90 days of runs",
			"the operator section does not say what the update does that is new (D2965, RL2B)"},
		{"created only where one is missing", "the step's create-if-absent half is not stated, so a reader cannot tell it is idempotent"},
		{"It waits for no run", "the in-flight half is not stated, and it is the one an operator about to update needs (CLAUDE.md's HARD RULE, update-in-flight.md)"},
	} {
		if !strings.Contains(operators, want.phrase) {
			t.Errorf("%s's `### For operators` section does not say %q: %s", runsLsSpeedNotes, want.phrase, want.why)
		}
	}
}
