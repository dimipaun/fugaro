package cli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// The five words for the state of a pull request: the four the owner defined
// on 2026-11-12, and the fifth D3195 added on 2026-11-21.
//
//   - live: a run is going on the pull request.
//   - paused: a run of it suspended itself and the orchestrator will resume it
//     at a named time (D3195, R15). The chain is still in flight, so nothing
//     may be launched on it while the pause stands (pauseStands).
//   - ready: no run is going and the pull request waits only on a merge (a
//     non-draft, or a draft whose last run succeeded with outcome ready).
//   - stalled: the pull request had a run, no run is going, and it is not
//     ready.
//   - orphan: no run ever produced or follows the pull request.
//
// A listing of runs can answer the first four and never the last: orphan
// is a pull request with no run on it at all, so the runs bucket holds no row
// of it and no listing can see it. Naming one would take a call to the forge
// (every open pull request of the repository, matched against the runs a
// listing holds), which `runs ls` does not make: it reads the bucket and the
// compute backend, and the read of a repository's pull requests is `fugaro
// runs ls --pr`'s caller's, a PR number at a time.
//
// None of the five changes meaning, and paused is a value of the same `pr_state`
// member the other four are, not a new member: a reader of a release behind
// draws a value it does not know rather than a field it cannot place
// (CLAUDE.md, "A field never changes meaning").
const (
	prStateLive    = "live"
	prStatePaused  = "paused"
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
//
// paused is deliberately not one of them, although it is one of the pull
// request's states. It is already a word --state takes — the run's own status,
// the one a suspended run's record carries (runstore.StatusPaused, R15) — and
// rowInState matches the pull request's words *before* the run's, so adding it
// here would take `--state paused` away from the vocabulary it has and answer
// it with the pull request's word instead: a listing would stop answering "the
// runs that suspended themselves" and start answering "every run of a pull
// request one of them is on", which is a different question and a second
// meaning for one flag. A reader who wants the pull request's own state reads
// the state column, or asks --pr.
var lsPRStates = []string{prStateLive, prStateReady, prStateStalled}

// lsStateWords is every word --state takes: the run's own states, then the
// three states of the pull request a run carries that are not also a run's
// state. The two are one list because the flag asks one question — which rows
// are in this state — and no word is in both vocabularies: no run status is
// spelled live, ready or stalled, and `paused` is a run status and so is in
// the first list alone (lsPRStates).
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
// folds them: whether a run of it is going, whether one of them suspended
// itself into a pause that still stands, and the newest run whose record says
// what the pull request is — prVerdict's rows.
type prGroup struct {
	verdict runview.Row // the newest run of the pull request whose outcome is the record's word for it
	said    bool        // a row is in verdict
	live    bool        // a run of it is going
	paused  bool        // a run of it ended paused and the pause still stands (pauseStands)
}

// state is the group's pull request in the vocabulary above, decided in that
// order: a run going means live whatever the runs that ended left behind — a
// run that finished and a run still going are both on a live pull request —
// then a suspended run whose pause still stands means paused, and otherwise the
// newest run that said what the pull request is decides.
//
// paused comes before ready on purpose, and the two are not the same fact: a
// pull request whose chain is paused is not one waiting only on a merge, even
// when the run behind it left it ready for review — the work is not finished
// and the chain will resume on its own, which is what a person reading the
// listing has to see. The pause is the newer, larger fact about it (D3195).
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
	case g.paused:
		return prStatePaused
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
// pauses is the databases read a paused row is asked about, and nil is the
// caller that made none: a row whose status is paused is one whose pause may
// have been cancelled since (D3197), and the cancellation is written on the
// service's node and nowhere the runs bucket holds, so the word cannot be read
// off the row. A nil lookup reads every paused row as a standing pause, which
// is the safe direction — the run's own record says it ended paused and the
// chain is in flight until the service says otherwise (pauseStands).
//
// It is the listing's read and not the row's, which is why it is done here and
// not in runview.Join: whether a run is going on a pull request is a fact about
// the other runs of that pull request, and one row says nothing about them. The
// grouping is over the rows the listing read — the window, as --pr and --debt
// narrowed it — and it runs before --mine, --batch and --state pick the rows
// that are printed, so every row of a page carries the state of its pull
// request and not the state of the page.
func markPRStates(rows []runview.Row, reviewing map[string]int, pauses pauseLookup) {
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
		if r.Status == string(runstore.StatusPaused) && pauseStands(pauses, rowRef(r).Slug, r.RunID) {
			g.paused = true
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

// pauseFact is what one read of a paused node says about the pause it records:
// whether the service marked it cancelled, and the instant its resume is due at
// when the node could be read.
type pauseFact struct {
	cancelled bool
	resumeAt  time.Time
}

// pauseLookup answers what a run's paused node says, by the repository's slug
// and the run id. ok is false for a node this reader could not read at all — no
// database in reach, a read that failed, a node absent, or one this release does
// not know (runstore.ParsePauseNode) — and every caller reads that the same way
// it reads a standing pause: the run's own record already says it ended paused,
// and a guard that turned a pause it could not see into a finished run would
// admit the second launch the pause is there to stop.
//
// It is a function and not a struct so that the node is read once per row that
// needs it and never for a listing that holds no paused run: markPRStates asks
// it only for a row whose status is paused, and a repo whose chain has never
// paused one opens no database at all.
type pauseLookup func(slug, run string) (pauseFact, bool)

// pauseFactsFor is the lookup a read of rows makes of the pauses database: the
// project's own client (budgetDBFor), opened on the first paused row it is
// asked about, and one node read per paused run.
//
// One read per run, not per question: a listing asks a paused row's node twice
// — once for the state of its pull request (markPRStates) and once for the
// resume instant its own row shows (markPauses) — and the two are the same
// node, so the answer is kept by row key and the second question is answered
// from it. A read that failed is not kept: the row is asked again by the next
// question, which is one more attempt at a node that may have been written
// since, and the line about it is said once either way (warnOnce).
//
// Every failure answers the way a standing pause does and prints one line —
// once, so a listing of a repository whose project has no pauses database is
// not one line per paused row. It is not an error the caller returns: `runs ls`
// still lists the rows it read, exactly as it does when a run's own objects
// cannot be read, and the state of one pull request is worth less than the
// listing.
func pauseFactsFor(ctx context.Context, env *cloudEnv, warn func(string, ...any)) pauseLookup {
	warn = warnOnce(warn)
	var (
		db     *rtdb.Client
		opened bool
		fail   error
	)
	read := map[string]pauseFact{}
	return func(slug, run string) (pauseFact, bool) {
		if !opened {
			opened = true
			if env == nil || env.lc == nil {
				fail = errors.New("no project config is loaded")
			} else if _, db, fail = budgetDBFor(ctx, env.lc); fail != nil {
				db = nil
			}
		}
		if fail != nil {
			warn("warning: run %s/%s ended paused and the pause could not be read (%s); it is counted as paused until the database says otherwise\n",
				safetext.Field(slug), task.ShortRun(oneLine(run)), safetext.OneLine(fail.Error()))
			return pauseFact{}, false
		}
		key := pauseRowKey(slug, run)
		if f, ok := read[key]; ok {
			return f, true
		}
		f, ok := pauseNode(ctx, db, slug, run, warn)
		if ok {
			read[key] = f
		}
		return f, ok
	}
}

// markPauses stamps each paused row with the pause it ended in (contract-pause
// R15, D3195): the instant the orchestrator will resume its chain at, from the
// service's own node, and the run that resumed it, from the resumer's own task.
// It is the listing's counterpart of `internal/watch`'s MergePaused, and the
// two read the same two facts: the node the credentials service writes and the
// `resumes` member the resumer's task carries.
//
// resumers is the run that resumed each paused run, by the paused run's key
// (pauseRowKey): the resumer's task names the paused run under `resumes`
// (task.Spec.Resumes), and nothing on the paused run's own records does — a
// chain is walked backwards, so a paused run's chain holds its ancestors and
// never the run the orchestrator launched to pick it up.
//
// Only a row whose own status is `paused` is touched: a run that is not paused
// is not one a pause is about, whatever a stale node under its id might say.
// The two facts degrade independently and both degrade to absence: a node the
// lookup could not read leaves ResumeAt unset, and a chain no run of the
// listing resumed leaves ResumedBy empty, so the row reads `paused` and claims
// nothing this read did not see.
func markPauses(rows []runview.Row, resumers map[string]string, pauses pauseLookup) {
	for i := range rows {
		if rows[i].Status != string(runstore.StatusPaused) {
			continue
		}
		slug := rowRef(rows[i]).Slug
		if by := resumers[pauseRowKey(slug, rows[i].RunID)]; by != "" {
			rows[i].ResumedBy = by
		}
		if pauses == nil {
			continue
		}
		if f, ok := pauses(slug, rows[i].RunID); ok && !f.resumeAt.IsZero() {
			at := f.resumeAt
			rows[i].ResumeAt = &at
		}
	}
}

// pauseRowKey is the key a paused run is looked up by in the resumer map
// (markPauses) and the key one node read is kept under (pauseFactsFor): the
// repository's storage slug and the run's id, the pair rowRef splits out of
// Row.Run. Neither half can hold the separator — a slug is a bucket key and a
// run id is digits, dashes and hex — so the join names exactly one pair.
func pauseRowKey(slug, run string) string { return slug + "\x00" + run }

// pauseLookupWith is pauseFactsFor over a client the caller already holds: the
// daemon's own, which is the same database the state node lives in
// (orchestrator_cmd.openBudget), so the orchestrator's view reads a pause
// through the connection it already opened this cycle rather than making a
// second one.
func pauseLookupWith(ctx context.Context, db *rtdb.Client, warn func(string, ...any)) pauseLookup {
	if db == nil {
		return nil
	}
	warn = warnOnce(warn)
	return func(slug, run string) (pauseFact, bool) { return pauseNode(ctx, db, slug, run, warn) }
}

// pauseNode reads one paused node and holds it to the pause contract's own
// reader (runstore.ParsePauseNode): a node this release does not know is a pause
// this reader cannot answer for, and it answers with the standing pause every
// failure answers with.
func pauseNode(ctx context.Context, db *rtdb.Client, slug, run string, warn func(string, ...any)) (pauseFact, bool) {
	var raw json.RawMessage
	found, err := db.Get(ctx, runstore.PauseNodePath(slug, run), &raw)
	if err != nil {
		warn("warning: run %s/%s ended paused and the pause could not be read (%s); it is counted as paused until the database says otherwise\n",
			safetext.Field(slug), task.ShortRun(oneLine(run)), safetext.OneLine(err.Error()))
		return pauseFact{}, false
	}
	if !found {
		// The service writes the node before the run ends paused (C3, R19), so
		// a row saying paused with no node under it is a pause nothing will
		// resume. The line is the whole of what this reader can do about it,
		// and the pause is counted as standing so no second run is launched on
		// the chain meanwhile.
		warn("warning: run %s/%s ended paused and no pause of it is recorded; nothing will resume it\n",
			safetext.Field(slug), task.ShortRun(oneLine(run)))
		return pauseFact{}, false
	}
	node, err := runstore.ParsePauseNode(raw)
	if err != nil {
		warn("warning: run %s/%s ended paused and its pause is not one this release can read (%s); nothing will resume it\n",
			safetext.Field(slug), task.ShortRun(oneLine(run)), safetext.OneLine(err.Error()))
		return pauseFact{}, false
	}
	return pauseFact{cancelled: node.Cancelled, resumeAt: time.UnixMilli(node.ResumeAt).UTC()}, true
}

// warnOnce wraps a warning function so a reader that meets the same trouble on
// every row of a listing says it once: the listing is the thing the caller
// reads, and one line about a database this machine may not have beats one per
// paused row of it.
func warnOnce(warn func(string, ...any)) func(string, ...any) {
	if warn == nil {
		return func(string, ...any) {}
	}
	var said bool
	return func(format string, a ...any) {
		if said {
			return
		}
		said = true
		warn(format, a...)
	}
}

// pauseStands reports whether the pause a run's `paused` status names is one
// that still holds the run in flight: the node says it stands, or this reader
// could not read it at all — which answers as a standing pause does, because
// the run's own record says it ended paused and a guard that read a pause it
// could not see as a finished run would admit the second launch the pause is
// there to stop. Only the service's own cancellation mark (D3197) takes the run
// out of flight.
//
// It is one function because three places ask the same question — the fold
// above, the duplicate guard and the follow-up guard — and the answer has to be
// the same one in all three: a pull request that reads paused while a launch on
// it is admitted is the incoherence F64 named.
func pauseStands(pauses pauseLookup, slug, run string) bool {
	if pauses == nil {
		// The caller made no lookup at all (a unit test folding rows by hand):
		// the pause stands, as it does when a read fails.
		return true
	}
	f, ok := pauses(slug, run)
	return !(ok && f.cancelled)
}

// pauseInFlight reports whether a row is a run a pause still holds in flight,
// and answers the pause's own node beside it: true for a row that ended paused
// and whose pause the service has not cancelled, false for every other row —
// including one that ended paused and was cancelled, which is a chain that is
// over and a run the guards read as the finished run it is (D3197).
//
// It is pauseStands over a row, with the node handed back: a guard that refuses
// on a standing pause names the instant the chain comes back, and asking twice
// would read the node twice.
func pauseInFlight(r runview.Row, pauses pauseLookup) (pauseFact, bool) {
	if r.Status != string(runstore.StatusPaused) {
		return pauseFact{}, false
	}
	if pauses == nil {
		return pauseFact{}, true
	}
	f, ok := pauses(rowRef(r).Slug, r.RunID)
	if ok && f.cancelled {
		return pauseFact{}, false
	}
	return f, true
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
