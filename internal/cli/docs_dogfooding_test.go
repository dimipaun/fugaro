package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// The practice the modes formalise, and the guard that owns it (plan T7.2).
//
// `docs/dogfooding.md` is where this repository's own operating practice is
// written down, and F30 of docs/rigor-modes/reconciliation.md reads seven of its
// sentences back as "the manual version of the modes": habits the owner wrote
// out by hand, which a rigor mode now decides. T7.2 is the task that put the
// mode automating each habit beside its sentence on the page, and this file is
// the pin that task owes — the mapping is prose, and prose is what a later edit
// drops. Without it the page can keep the habit and lose the mode, and the
// design's own claim that "the modes are a formalisation of the owner's written
// practice" (docs/rigor-modes/README.md) is left with nothing to check it
// against.
//
// The page had no pin of its own before this one. Its neighbours read it —
// docs_base_test.go holds the flags it names, docs_runner_timeout_test.go the CI
// bound it states, docs_project_wide_git_test.go the secret scope it explains,
// docs_in_job_review_test.go its citation — and docs_dogfooding_reports_test.go
// owns its reports section, so this file holds F30's list and nothing those
// guards already own.
//
// Each entry is the sentence as the page carries it, and the mode beside it. The
// mode is a value of `internal/rigor`'s vocabulary and never a string retyped
// here, so the guard asks `rigor.Known` and a mode the code loses fails here
// rather than reading as prose. The sentence is held too, because it is the half
// that drifts: an edit that rewrites a habit would otherwise leave the mode
// standing beside words the modes no longer formalise.
//
// The red step is this file against the page as it stood: no one of the seven
// sentences named a mode, so every case below failed on the mode half.

// dogfoodingDoc is the page F30 reads, and the only document this guard owns.
const dogfoodingDoc = "docs/dogfooding.md"

// dogfoodingPractices is F30's list, in the order the reconciliation quotes it:
// the habit as `docs/dogfooding.md` carries it, and the mode that automates it.
//
// The sentences are the page's own, so a case fails on a sentence the page
// rewrote rather than on a fixture that misquotes it — and each one is the
// smallest span that identifies its habit, because a longer quote would make
// this guard a second, stricter owner of prose the page's neighbours already
// pin.
var dogfoodingPractices = []struct {
	sentence string
	mode     rigor.Mode
}{
	// The batch practice: a base lands before its dependents launch, and what
	// says a base is done is the readiness answer — Pro's, the mode every
	// repository that configures nothing resolves (D19), and this repository's
	// own until the owner pins one (T7.3).
	{"Merge in dependency order", rigor.Pro},
	// The salvage: a run that ended a draft over parts it could not run may
	// still be fine. Startup is the mode whose stop row is that reading —
	// "merge as-is, follow up the rest" — a run that opens its pull request
	// ready with the findings its rounds left named in its status section
	// rather than fixed, and with the runner and never a person marking that
	// pull request ready (D11).
	{"check the PR's CI, mark it ready and merge", rigor.Startup},
	// The gate: the merge is not what proves the change, the CI on `main` is.
	// Hotfix is the mode built on that, its post-merge suite being this
	// repository's existing CI (F27).
	{"CI is the release gate, not the merge gate", rigor.Hotfix},
	// The smoke check: what a run can prove in the container is not the gate.
	// POC is the mode that reads its own check that way — the first-line review
	// alone, one round, nothing but a catastrophic finding blocking.
	{"The in-run verify is a smoke check, not the gate", rigor.POC},
	// The file no run may touch: no mode changes it, Hotfix included, whose
	// post-merge suite needs no workflow of its own.
	{"a task that touches `.github/` is done by a person or a local session, never by a run", rigor.Hotfix},
	// The loop judging itself: Critical is the mode that automates the most of
	// the review — five senior rounds, the adversarial pass, every finding
	// blocking — and its `ship` is still not independent review.
	{"A run's own `ship` never replaces it.", rigor.Critical},
	// The tier a run does not take: money and security stay a person's, and the
	// mode for that work is Critical (F31).
	{"anything that handles money or security", rigor.Critical},
}

// TestDocsDogfoodingNamesTheModeBesideEachPractice: every practice F30 quotes
// carries, on its own line, the mode that automates it — and that mode is one
// the code has.
//
// The line is the unit on purpose: this page writes one paragraph per line and
// one bullet per line, so "the same line" is "beside its sentence" in the file
// as it is written, and the mode has to follow the sentence it belongs to
// rather than sit anywhere on the page. A page that keeps all seven modes and
// loses the sentences beside them is the drift this guard exists to catch, and
// so is the reverse.
func TestDocsDogfoodingNamesTheModeBesideEachPractice(t *testing.T) {
	doc := readDoc(t, dogfoodingDoc)
	lines := strings.Split(doc, "\n")

	for _, p := range dogfoodingPractices {
		if !rigor.Known(p.mode) {
			t.Errorf("%s: this guard reads %q as the mode automating %q, and internal/rigor.Modes does not hold it: the vocabulary moved, and the page and this list move with it", dogfoodingDoc, p.mode, p.sentence)
			continue
		}
		line, at, ok := dogfoodingPracticeLine(lines, p.sentence)
		if !ok {
			t.Errorf("%s no longer carries F30's practice %q: the modes formalise the sentences the owner wrote, and a habit the page dropped is a mapping with nothing to point at", dogfoodingDoc, p.sentence)
			continue
		}
		if !strings.Contains(line[at+len(p.sentence):], "`"+string(p.mode)+"`") {
			t.Errorf("%s carries %q without naming %s after it: the practice and the mode that automates it read together, and a page that keeps the habit and loses the mode is the drift this guard is for", dogfoodingDoc, p.sentence, p.mode)
		}
	}
}

// dogfoodingPracticeLine is the first line carrying the sentence, with the
// offset it was found at, so the caller can ask what the page puts after it. A
// sentence the page wrapped across two lines is not found, which is the failure
// the caller reports: the page writes one paragraph per line, and a habit split
// in two is one the mode cannot stand beside.
func dogfoodingPracticeLine(lines []string, sentence string) (string, int, bool) {
	for _, line := range lines {
		if at := strings.Index(line, sentence); at >= 0 {
			return line, at, true
		}
	}
	return "", 0, false
}
