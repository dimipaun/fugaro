package cli

import (
	"slices"
	"strconv"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// The four words for the state of a pull request, exactly as the owner
// defined them on 2026-11-12:
//
//   - live: a run is going on the pull request.
//   - ready: no run is going and the pull request waits only on a merge (a
//     non-draft, or a draft whose last run succeeded with outcome ready).
//   - stalled: the pull request had a run, no run is going, and it is not
//     ready.
//   - orphan: no run ever produced or follows the pull request.
//
// A listing of runs can answer the first three and never the fourth: orphan
// is a pull request with no run on it at all, so the runs bucket holds no row
// of it and no listing can see it. Naming one would take a call to the forge
// (every open pull request of the repository, matched against the runs a
// listing holds), which `runs ls` does not make: it reads the bucket and the
// compute backend, and the read of a repository's pull requests is `fugaro
// runs ls --pr`'s caller's, a PR number at a time.
const (
	prStateLive    = "live"
	prStateReady   = "ready"
	prStateStalled = "stalled"
	// prStateOrphan is named here and answered by no listing: it is the one
	// word --state refuses with a sentence of its own rather than with the
	// list of the words it takes, so a reader who asks for it is told why the
	// flag has no answer instead of being shown a list that omits it.
	prStateOrphan = "orphan"
)

// lsPRStates are the pull request states a run listing answers, in the order
// the help text and the refusal name them. It is the closed set --state
// matches a word against (rowInState), the way lsStates is the closed set it
// matched before: a word outside both lists is refused rather than read as a
// run status nothing carries.
var lsPRStates = []string{prStateLive, prStateReady, prStateStalled}

// lsStateWords is every word --state takes: the run's own states, then the
// three states of the pull request a run carries. The two are one list
// because the flag asks one question — which rows are in this state — and no
// word is in both vocabularies: no run status is spelled live, ready or
// stalled.
func lsStateWords() []string {
	return append(slices.Clone(lsStates), lsPRStates...)
}

// rowInState reports whether a row is in the state --state named: one of the
// three states of the pull request it carries, when the word is one of them,
// else the run's own status — the vocabulary --state has always matched
// against, which is the one --json's `status` carries and never the decorated
// cell (`halted (run_cap)` is --state halted).
func rowInState(r runview.Row, state string) bool {
	if slices.Contains(lsPRStates, state) {
		return r.PRState == state
	}
	return string(r.Status) == state
}

// prGroup is the runs of one pull request a listing read, as markPRStates
// folds them: whether a run of it is going, and the newest run whose record
// says what the pull request is — prVerdict's rows.
type prGroup struct {
	verdict runview.Row // the newest run of the pull request whose outcome is the record's word for it
	said    bool        // a row is in verdict
	live    bool        // a run of it is going
}

// state is the group's pull request in the vocabulary above, decided in that
// order: a run going means live whatever the runs that ended left behind — a
// run that finished and a run still going are both on a live pull request —
// and otherwise the newest run that said what the pull request is decides.
//
// It is ready when that run's outcome is `ready`, which is the record's own
// word for a pull request left ready for review rather than a draft: the
// owner's definition names both halves of "waits only on a merge" ("a
// non-draft, or a draft whose last run succeeded with outcome ready"), and
// they are one test, because the outcome is the only word a run record has for
// either — a run that ends ready leaves the pull request a non-draft, and the
// draft marker a host may still show is the one a mark-ready call left behind,
// which the record cannot see. An outcome of `draft` is the other word, and it
// stalls the pull request.
//
// Everything else is stalled: the pull request had a run (it has this row), no
// run is going, and it is not ready — a run whose outcome left a draft, a run
// that failed or halted, an error row whose objects could not be read, a run
// that never launched, and a pull request whose runs here all ended without
// saying what they left behind (prVerdict) among them.
func (g prGroup) state() string {
	switch {
	case g.live:
		return prStateLive
	case g.said && g.verdict.Outcome == string(runstore.OutcomeReady):
		return prStateReady
	default:
		return prStateStalled
	}
}

// prVerdict reports whether a run's record says what its pull request is: its
// outcome, `ready` or `draft`, which is the only word a record has for the
// pull request's own state — the runner writes `ready` for a run that left the
// pull request ready for review and `draft` for one that left it, or could
// have left it, a draft.
//
// Everything else says nothing about the pull request and is not a verdict: a
// `none` outcome is a run that never had one to leave behind — a follow-up
// that only put the branch back on its base (internal/runner's rebase, which
// pushes for nobody's review), an artifact run, a run that failed before it
// reached any pull request — and `reviewed` is a review run, which never
// pushes and never edits a pull request of its own. None of them may decide
// how the pull request they were launched on reads: a rebase that succeeded on
// a pull request waiting only on a merge leaves it exactly that, and a review
// run leaves it as it found it, so the fold looks past both for the newest run
// that did say (prGroup.verdict).
func prVerdict(r runview.Row) bool {
	switch r.Outcome {
	case string(runstore.OutcomeReady), string(runstore.OutcomeDraft):
		return true
	}
	return false
}

// markPRStates sets PRState on every row that is on a pull request, from the
// runs of that pull request the listing read: the rows are grouped by the pull
// request they are on, the newest run of each group that said what the pull
// request is is taken (prVerdict), and the vocabulary above is applied to the
// group.
//
// reviewing is the pull request each of those rows reviews, by run id, for the
// review runs whose row cannot say it yet (prGroupKey's third case).
//
// It is the listing's read and not the row's, which is why it is done here and
// not in runview.Join: whether a run is going on a pull request is a fact about
// the other runs of that pull request, and one row says nothing about them. The
// grouping is over the rows the listing read — the window, as --pr and --debt
// narrowed it — and it runs before --mine, --batch and --state pick the rows
// that are printed, so every row of a page carries the state of its pull
// request and not the state of the page.
func markPRStates(rows []runview.Row, reviewing map[string]int) {
	groups := map[string]*prGroup{}
	keys := make([]string, len(rows))
	for i, r := range rows {
		key, ok := prGroupKey(r, reviewing[r.RunID])
		if !ok {
			continue
		}
		keys[i] = key
		g := groups[key]
		if g == nil {
			g = &prGroup{}
			groups[key] = g
		}
		if prGoing(r.Status) {
			g.live = true
		}
		if prVerdict(r) && (!g.said || newerRow(r, g.verdict)) {
			g.verdict, g.said = r, true
		}
	}
	for i, key := range keys {
		if key != "" {
			rows[i].PRState = groups[key].state()
		}
	}
}

// prGroupKey is the pull request a row is on, as the identity a listing groups
// its runs by: the repository's storage slug (two repositories number their
// pull requests from 1, so a number alone is not a pull request) and the number
// the run's record or its task names — or, for a record that names a pull
// request whose number it does not carry, the URL, which is the only thing left
// of it to group by. reviewing is the third case, the pull request a review run
// was launched to review: its record carries no number at all until the
// artifact stage writes one, which is what ends the review
// (internal/runner/reviewartifact.go), so while the review is going its task is
// the only thing that says which pull request it is on.
//
// ok is false for a row that is on no pull request at all: a run that opened
// none, and an artifact run among them. A review run is not one of those, in
// either half of its life: a finished review of PR N records N, so its row is
// grouped with that pull request's runs and reads their state, while its own
// outcome says nothing about it (prVerdict), and a review still going is
// grouped by the number its task names (reviewing), which is the same key the
// record gives it once the review ends.
func prGroupKey(r runview.Row, reviewing int) (string, bool) {
	slug := rowRef(r).Slug
	switch {
	case r.PR > 0:
		return slug + "#" + strconv.Itoa(r.PR), true
	case r.PRURL != "":
		return slug + " " + r.PRURL, true
	case reviewing > 0:
		return slug + "#" + strconv.Itoa(reviewing), true
	}
	return "", false
}

// newerRow reports whether a is newer than b in the listing's own order:
// created descending, then the run id descending (loadRows's sort). A pull
// request's newest run is the one the listing prints first among its runs.
func newerRow(a, b runview.Row) bool {
	if c := a.Created.Compare(b.Created); c != 0 {
		return c > 0
	}
	return a.RunID > b.RunID
}

// prGoing reports whether a run is going on its pull request: it is running,
// or its launch is in flight — the backend has taken it (pending) or a fresh
// claim says it is being launched. An unlaunched run is not going, which is
// what runview's own Settled says of it (nobody is launching it), and a
// terminal one is not either, whatever it ended as.
func prGoing(status string) bool {
	switch status {
	case string(runstore.StatusRunning), runview.StatusPending, runview.StatusLaunching:
		return true
	}
	return false
}
