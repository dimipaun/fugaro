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
// folds them: whether a run of it is going, and the newest run's row.
type prGroup struct {
	newest runview.Row // the newest run of the pull request, in the listing's own order
	set    bool        // a row is in newest
	live   bool        // a run of it is going
}

// state is the group's pull request in the vocabulary above, decided in that
// order: a run going means live whatever the newest run ended as — a run that
// finished and a run still going are both on a live pull request — and
// otherwise the newest run decides.
//
// It is ready when that run succeeded with outcome `ready`, which is the
// record's own word for a pull request left ready for review rather than a
// draft: the owner's definition names both halves of "waits only on a merge"
// ("a non-draft, or a draft whose last run succeeded with outcome ready"), and
// they are one test, because the outcome is the only word a run record has for
// either — a run that ends ready leaves the pull request a non-draft, and the
// draft marker a host may still show is the one a mark-ready call left behind,
// which the record cannot see.
//
// Everything else is stalled: the pull request had a run (it has this row),
// no run is going, and it is not ready — a failed or halted run, a run whose
// outcome left a draft, an error row whose objects could not be read, and a
// run that never launched among them.
func (g prGroup) state() string {
	switch {
	case g.live:
		return prStateLive
	case g.newest.Status == string(runstore.StatusSucceeded) && g.newest.Outcome == string(runstore.OutcomeReady):
		return prStateReady
	default:
		return prStateStalled
	}
}

// markPRStates sets PRState on every row that carries a pull request, from the
// runs of that pull request the listing read: the rows are grouped by the pull
// request they carry, the newest run of each group is taken, and the
// vocabulary above is applied to the group.
//
// It is the listing's read and not the row's, which is why it is done here and
// not in runview.Join: whether a run is going on a pull request is a fact about
// the other runs of that pull request, and one row says nothing about them. The
// grouping is over the rows the listing read — the window, as --pr and --debt
// narrowed it — and it runs before --mine, --batch and --state pick the rows
// that are printed, so every row of a page carries the state of its pull
// request and not the state of the page.
func markPRStates(rows []runview.Row) {
	groups := map[string]*prGroup{}
	keys := make([]string, len(rows))
	for i, r := range rows {
		key, ok := prGroupKey(r)
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
		if !g.set || newerRow(r, g.newest) {
			g.newest, g.set = r, true
		}
	}
	for i, key := range keys {
		if key != "" {
			rows[i].PRState = groups[key].state()
		}
	}
}

// prGroupKey is the pull request a row carries, as the identity a listing
// groups its runs by: the repository's storage slug (two repositories number
// their pull requests from 1, so a number alone is not a pull request) and the
// number the run's record or its task names — or, for a record that names a
// pull request whose number it does not carry, the URL, which is the only
// thing left of it to group by. ok is false for a row that carries no pull
// request at all: a run that opened none, an artifact run and a review run
// among them.
func prGroupKey(r runview.Row) (string, bool) {
	slug := rowRef(r).Slug
	switch {
	case r.PR > 0:
		return slug + "#" + strconv.Itoa(r.PR), true
	case r.PRURL != "":
		return slug + " " + r.PRURL, true
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
