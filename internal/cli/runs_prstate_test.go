package cli

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// prStateRoot is the run the fixture's follow-ups continue: a follow-up names
// its branch, its pull request and the run before it together, and the run it
// names need not be in the bucket for the task to be readable.
const prStateRoot = "20261101-090000-0000"

// prStateTaskFor is the task of a follow-up on PR pr of acme/app, for the
// fixtures below: the pull request a run's task names is the one a listing
// groups it by when the record cannot say.
func prStateTaskFor(id string, pr int) *task.Spec {
	return followUpSpec(id, prStateRoot, prStateRoot, pr)
}

// prStateRuns is the fixture of the tests below: one pull request per state of
// the owner's 2026-11-12 ruling, plus a run that carries none.
type prStateRuns struct {
	live       string // PR 11: a run of it is going
	readyOld   string // PR 12: a follow-up that failed
	ready      string // PR 12: the newest run, succeeded with outcome ready
	stalled    string // PR 13: the newest run ended a draft
	unlaunched string // PR 14: a run that never launched on the pull request
	broken     string // PR 15: a run whose result.json cannot be read
	rebased    string // PR 16: the run that left the pull request ready
	rebase     string // PR 16: the newer run, a rebase that said nothing about it
	reviewing  string // PR 17: a review run in flight, whose record names no pull request yet
	noPR       string // a run that opened no pull request
}

// seedPRStateRuns seeds the fixture above in one repository, acme/app. Every
// record it writes is a run record as the runner writes one; nothing here is
// read from a forge.
func seedPRStateRuns(t *testing.T, f *cloudFixture) prStateRuns {
	t.Helper()
	r := prStateRuns{
		live:       runIDAt(0, "000100", "a001"),
		readyOld:   runIDAt(1, "000200", "b001"),
		ready:      runIDAt(0, "000200", "b002"),
		stalled:    runIDAt(0, "000300", "c001"),
		unlaunched: runIDAt(0, "000400", "d001"),
		broken:     runIDAt(0, "000500", "e001"),
		rebased:    runIDAt(0, "000700", "1a01"),
		rebase:     runIDAt(0, "000800", "1b01"),
		reviewing:  runIDAt(0, "000900", "2a01"),
		noPR:       runIDAt(0, "000600", "f001"),
	}
	// PR 11: the newest run of the pull request is still going, so the pull
	// request is live whatever its other runs ended as.
	exec := seedSpec(t, f, prStateTaskFor(r.live, 11), true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f, r.live, &runstore.Record{Version: 1, RunID: r.live, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusRunning, Stage: "implement", Execution: exec,
		PR: &runstore.PRRef{Number: 11, URL: "https://github.com/acme/app/pull/11"}})

	// PR 12: the newest run succeeded with outcome ready, which is the
	// record's own word for a pull request left ready for review; the older
	// one failed, and the newest decides.
	seedSpec(t, f, prStateTaskFor(r.readyOld, 12), false)
	writeRecord(t, f, r.readyOld, prRecord(r.readyOld, "", 12, 1))
	seedSpec(t, f, prStateTaskFor(r.ready, 12), false)
	writeRecord(t, f, r.ready, &runstore.Record{Version: 1, RunID: r.ready, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady, Stage: "finalize",
		PR: &runstore.PRRef{Number: 12, URL: "https://github.com/acme/app/pull/12"}})

	// PR 13: the run that had the pull request ended a draft and no run is
	// going, so the pull request is stalled.
	seedSpec(t, f, prStateTaskFor(r.stalled, 13), false)
	writeRecord(t, f, r.stalled, prRecord(r.stalled, "", 13, 1))

	// PR 14: a run that never launched is not going (nobody is launching
	// it), so a pull request whose only run is that one is stalled.
	seedSpec(t, f, prStateTaskFor(r.unlaunched, 14), false)

	// PR 15: a run whose result.json cannot be read is an error row, and an
	// error row is not going: the pull request is stalled.
	seedSpec(t, f, prStateTaskFor(r.broken, 15), false)
	putBuildObject(t, f, "runs/"+appSlug+"/"+r.broken+"/result.json", []byte("{not json"))

	// PR 16: the newest run is a follow-up that only put the branch back on
	// its base (internal/runner's rebase, which ends succeeded with outcome
	// none and pushes for nobody's review), so the pull request is still the
	// ready one the run before it left behind: a run that says nothing about
	// the pull request does not take it off ready.
	seedSpec(t, f, prStateTaskFor(r.rebased, 16), false)
	writeRecord(t, f, r.rebased, &runstore.Record{Version: 1, RunID: r.rebased, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady, Stage: "finalize",
		PR: &runstore.PRRef{Number: 16, URL: "https://github.com/acme/app/pull/16"}})
	seedSpec(t, f, prStateTaskFor(r.rebase, 16), false)
	writeRecord(t, f, r.rebase, &runstore.Record{Version: 1, RunID: r.rebase, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeNone, Stage: "rebase",
		Branch: "fugaro/" + r.rebased, FollowUp: &runstore.FollowUp{PR: 16, PreviousRun: r.rebased},
		PR: &runstore.PRRef{Number: 16, URL: "https://github.com/acme/app/pull/16"}})

	// PR 17: a review run that is going. Its own record names no pull request
	// -- a review writes one only at the artifact stage, which is what ends
	// the review -- so the pull request it is on is the one its task names as
	// the subject of the review, and a run going on it is what makes it live.
	exec = seedSpec(t, f, reviewTaskFor(r.reviewing, 17), true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f, r.reviewing, &runstore.Record{Version: 1, RunID: r.reviewing, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusRunning, Stage: "review", Execution: exec})

	// A run that opened no pull request carries no state at all.
	seedSpec(t, f, firstRunSpec(r.noPR), false)
	return r
}

// reviewTaskFor is the task of a review run of PR pr of acme/app: a review
// names the pull request it reviews in Spec.ReviewPRs, which is the only thing
// that says which one it is on until its record names it.
func reviewTaskFor(id string, pr int) *task.Spec {
	s := firstRunSpec(id)
	s.Mode = recipe.ModeReview
	s.ReviewPRs = []int{pr}
	return s
}

// prStatesOf is each row of a listing by its run ID, as the state of the pull
// request it carries.
func prStatesOf(rows []runview.Row) map[string]string {
	byID := make(map[string]string, len(rows))
	for _, r := range rows {
		byID[r.RunID] = r.PRState
	}
	return byID
}

// TestRunsLsPRStateVocabulary: the state of a pull request is read from the
// runs on it and from nothing else -- no forge is called -- and each of the
// three states a listing answers is read the way the owner defined it on
// 2026-11-12: live when a run is going, ready when the newest run succeeded
// with outcome ready, stalled when the pull request had a run, no run is going
// and it is not ready. Every row of a pull request carries the state of that
// pull request, not the state of its own run.
func TestRunsLsPRStateVocabulary(t *testing.T) {
	f := newCloudFixture(t)
	r := seedPRStateRuns(t, f)

	got, _ := lsJSON(t, "--since", "0")
	states := prStatesOf(got.Runs)
	want := map[string]string{
		r.live:       prStateLive,
		r.readyOld:   prStateReady, // the newest run of PR 12 decides, not this one
		r.ready:      prStateReady,
		r.stalled:    prStateStalled,
		r.unlaunched: prStateStalled,
		r.broken:     prStateStalled,
		r.rebased:    prStateReady, // the rebase below that run said nothing about the pull request
		r.rebase:     prStateReady,
		r.reviewing:  prStateLive, // a review going is a run going on the pull request its task names
		r.noPR:       "",          // no pull request, no state
	}
	if len(got.Runs) != len(want) {
		t.Fatalf("the fixture lists %d runs, want its %d: %v", len(got.Runs), len(want), runIDs(got.Runs))
	}
	for id, state := range want {
		if states[id] != state {
			t.Errorf("run %s carries pr_state %q, want %q", id, states[id], state)
		}
	}
}

// TestRunsLsPRStateFilter: --state takes the three states of a pull request
// beside the run's own states. The state is the pull request's, so every row
// of a live pull request is listed by --state live -- a row that ended is on a
// pull request a run is going on -- and a row that carries no pull request is
// in none of the three.
func TestRunsLsPRStateFilter(t *testing.T) {
	f := newCloudFixture(t)
	r := seedPRStateRuns(t, f)

	for state, want := range map[string][]string{
		prStateLive:    {r.reviewing, r.live}, // newest first: the review was minted after the running run
		prStateReady:   {r.rebase, r.rebased, r.ready, r.readyOld},
		prStateStalled: {r.broken, r.unlaunched, r.stalled}, // newest first, the listing's own order
	} {
		got, _ := lsJSON(t, "--since", "0", "--state", state)
		if gotIDs := runIDs(got.Runs); !slices.Equal(gotIDs, want) {
			t.Errorf("runs ls --state %s listed %v, want the runs of the pull requests in that state, %v", state, gotIDs, want)
		}
		if got.Totals.Runs != len(want) {
			t.Errorf("runs ls --state %s totals %d runs, want %d: the totals are the filtered window's", state, got.Totals.Runs, len(want))
		}
	}
	// The run's own vocabulary is untouched by the three new words: a run
	// status is still matched against the status --json carries.
	if got, _ := lsJSON(t, "--since", "0", "--state", "failed"); len(got.Runs) != 2 {
		t.Errorf("runs ls --state failed listed %v, want the two runs whose own status is failed", runIDs(got.Runs))
	}
	// orphan is the one word the flag refuses with a sentence of its own: no
	// run produced such a pull request, so no listing of runs holds a row of
	// it, and the refusal says that rather than offering a list without it.
	_, _, err := execute(t, "runs", "ls", "--state", "orphan")
	if err == nil || !strings.Contains(err.Error(), "no run produced or follows") {
		t.Errorf("runs ls --state orphan: err = %v, want a refusal saying why a listing of runs cannot name one", err)
	}
}

// TestRunsLsPRStateJSONField: `pr_state` is on every row that carries a pull
// request and on none of the rows that carry none -- absence is how a reader
// tells "no pull request" from a state, and the key is the one the console
// reads.
func TestRunsLsPRStateJSONField(t *testing.T) {
	f := newCloudFixture(t)
	r := seedPRStateRuns(t, f)

	rows := lsRawRuns(t, "--since", "0")
	if len(rows) == 0 {
		t.Fatal("the fixture lists no row")
	}
	words := []string{prStateLive, prStateReady, prStateStalled}
	for _, row := range rows {
		id := strings.Trim(string(row["run_id"]), `"`)
		raw, ok := row["pr_state"]
		switch {
		case id == r.noPR:
			if ok {
				t.Errorf("the row of the run that opened no pull request carries pr_state %s, want no key at all", raw)
			}
		case !ok:
			t.Errorf("the row of %s carries no pr_state key: it is on a pull request", id)
		default:
			if state := strings.Trim(string(raw), `"`); !slices.Contains(words, state) {
				t.Errorf("the row of %s carries pr_state %s, want one of %v", id, raw, words)
			}
		}
	}
}

// TestRunsLsPRStateHelpNamesTheFourthWord: --help names the three states the
// flag answers and says in one sentence why it answers no fourth: orphan is a
// pull request no run ever produced or follows, which no listing of runs can
// see.
func TestRunsLsPRStateHelpNamesTheFourthWord(t *testing.T) {
	out, _, err := execute(t, "runs", "ls", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{prStateLive, prStateReady, prStateStalled, prStateOrphan, "no run ever produced or follows"} {
		if !strings.Contains(out, want) {
			t.Errorf("runs ls --help does not name %q:\n%s", want, out)
		}
	}
	// The flag's own line lists the words it takes, and orphan is not one of
	// them: the sentence above is where a reader learns why.
	if !strings.Contains(out, strings.Join(lsStateWords(), ", ")) {
		t.Errorf("runs ls --help does not list the words --state takes (%s)", strings.Join(lsStateWords(), ", "))
	}
}

// TestDiagnoseDoesNotGuessAPRState: diagnose reads one run, so it cannot answer
// what that run's pull request is doing -- whether a run is going on a pull
// request is a fact about the other runs of it -- and its row carries no
// pr_state at all rather than a state read from the one run it holds. The pull
// request the run is on is still named.
func TestDiagnoseDoesNotGuessAPRState(t *testing.T) {
	f := newCloudFixture(t)
	r := seedPRStateRuns(t, f)

	out, errOut, err := execute(t, "diagnose", "--json", r.ready)
	if err != nil {
		t.Fatalf("diagnose: %v (%s)", err, errOut)
	}
	var doc struct {
		Row map[string]json.RawMessage `json:"row"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if raw, ok := doc.Row["pr_state"]; ok {
		t.Errorf("diagnose --json carries pr_state %s on its row, want no key at all: one run cannot say what its pull request is doing", raw)
	}
	if _, ok := doc.Row["pr"]; !ok {
		t.Errorf("diagnose --json's row carries no pr, want the pull request the run is on: %v", doc.Row)
	}
}

// prStateRow is a row as a listing's own read of a record leaves it: the run's
// identity, the moment it was created and the fields the state of its pull
// request is read from, and nothing else.
func prStateRow(slug, id string, created time.Time, status runstore.Status, outcome runstore.Outcome, pr int) runview.Row {
	row := runview.Row{Run: slug + "/" + id, RunID: id, Created: created, Status: string(status), Outcome: string(outcome)}
	if pr > 0 {
		row.PR = pr
		row.PRURL = "https://github.com/acme/app/pull/" + strconv.Itoa(pr)
	}
	return row
}

// TestPRStateFromTheListingsOwnRows: the state is folded from the rows a
// listing read, in the listing's own order, so the fold holds without a bucket
// or a backend: a run of the pull request that is going makes it live whatever
// the newest run ended as, otherwise the newest run decides, and two pull
// requests never share an answer.
func TestPRStateFromTheListingsOwnRows(t *testing.T) {
	const slug = "acme-app-3f9c1d2e4b5a6078" // a storage slug, one token, as a row's run reference carries it
	at := func(daysAgo int) time.Time { return lsNow().AddDate(0, 0, -daysAgo) }
	rows := []runview.Row{
		// PR 1: a run is going on it, and a newer run of it ended ready --
		// the pull request is live, because a run is going on it.
		prStateRow(slug, "20261110-090000-aaaa", at(2), runstore.StatusRunning, runstore.OutcomeNone, 1),
		prStateRow(slug, "20261112-090000-bbbb", at(0), runstore.StatusSucceeded, runstore.OutcomeReady, 1),
		// PR 2: the newest run failed, so the older ready run does not decide.
		prStateRow(slug, "20261111-090000-cccc", at(1), runstore.StatusSucceeded, runstore.OutcomeReady, 2),
		prStateRow(slug, "20261112-090000-dddd", at(0), runstore.StatusFailed, runstore.OutcomeDraft, 2),
		// PR 3: a follow-up that never launched is not going.
		prStateRow(slug, "20261112-090000-eeee", at(0), runview.StatusUnlaunched, runstore.OutcomeNone, 3),
		// PR 4: the run that left it ready is older than a rebase that only
		// put the branch back on its base and a review run that pushed
		// nothing -- neither says what the pull request is, so neither takes
		// the pull request off ready.
		prStateRow(slug, "20261110-090000-aa11", at(2), runstore.StatusSucceeded, runstore.OutcomeReady, 4),
		prStateRow(slug, "20261112-090000-bb11", at(0), runstore.StatusSucceeded, runstore.OutcomeNone, 4),
		prStateRow(slug, "20261112-090000-cc11", at(0), runstore.StatusSucceeded, runstore.OutcomeReviewed, 4),
		// PR 5: the same rebase after a run that left a draft leaves it a
		// draft: looking past the runs that said nothing is not looking past
		// the newest one that did.
		prStateRow(slug, "20261111-090000-dd11", at(1), runstore.StatusFailed, runstore.OutcomeDraft, 5),
		prStateRow(slug, "20261112-090000-ee11", at(0), runstore.StatusSucceeded, runstore.OutcomeNone, 5),
		// A run that opened no pull request.
		prStateRow(slug, "20261112-090000-ffff", at(0), runstore.StatusFailed, runstore.OutcomeNone, 0),
	}
	markPRStates(rows, nil)
	want := []string{prStateLive, prStateLive, prStateStalled, prStateStalled, prStateStalled,
		prStateReady, prStateReady, prStateReady, prStateStalled, prStateStalled, ""}
	for i, row := range rows {
		if row.PRState != want[i] {
			t.Errorf("row %s carries pr_state %q, want %q", row.RunID, row.PRState, want[i])
		}
	}

	// A run whose launch is in flight is going too, in the two shapes the read
	// side gives it, and a run nobody is launching is not.
	for status, going := range map[string]bool{
		runview.StatusPending: true, runview.StatusLaunching: true, string(runstore.StatusRunning): true,
		runview.StatusUnlaunched: false, string(runstore.StatusCancelled): false, runview.StatusError: false,
	} {
		if got := prGoing(status); got != going {
			t.Errorf("prGoing(%q) = %v, want %v", status, got, going)
		}
	}
}

// TestPRStateGroupsOneRepositoryAtATime: two repositories number their pull
// requests from 1, so a pull request is a repository and a number, never a
// number alone: a live PR 1 in one repository does not make PR 1 of another
// live.
func TestPRStateGroupsOneRepositoryAtATime(t *testing.T) {
	at := lsNow()
	rows := []runview.Row{
		{Run: "acme-app-3f9c1d2e4b5a6078/20261112-090000-aaaa", RunID: "20261112-090000-aaaa", Created: at,
			Status: string(runstore.StatusRunning), PR: 1},
		{Run: "acme-other-8b1e5a9d3c7f2046/20261112-090000-bbbb", RunID: "20261112-090000-bbbb", Created: at,
			Status: string(runstore.StatusFailed), Outcome: string(runstore.OutcomeDraft), PR: 1},
	}
	markPRStates(rows, nil)
	if rows[0].PRState != prStateLive || rows[1].PRState != prStateStalled {
		t.Fatalf("PR 1 of acme/app is %q and PR 1 of acme/other is %q, want live and stalled: a pull request number means one repository",
			rows[0].PRState, rows[1].PRState)
	}
}

// TestPRStateReadsTheTaskWhenTheRunHasNoRecord: a run that has not written a
// record still names the pull request its task carries, and a listing groups
// it by that: a run going on the pull request is what makes its state live,
// exactly as it is for a run whose record is readable.
func TestPRStateReadsTheTaskWhenTheRunHasNoRecord(t *testing.T) {
	f := newCloudFixture(t)
	id := runIDAt(0, "000700", "a701")
	exec := seedSpec(t, f, prStateTaskFor(id, 21), true)
	f.run.SetState(exec, backend.StateRunning)

	got, _ := lsJSON(t, "--since", "0")
	if len(got.Runs) != 1 || got.Runs[0].PRState != prStateLive {
		t.Fatalf("a running run whose record has not arrived carries %+v, want the live state of the pull request its task names", got.Runs)
	}
}

// TestPRStateReadsAReviewRunsTask: a review run is a run going on the pull
// request it reviews, and its own record says which that is only at the
// artifact stage, which is what ends the review -- so the fold takes the number
// from the task (markPRStates' reviewing), and while the review is going the
// pull request reads live and the review's own row carries that state. A review
// of several is on no single pull request (chain_continues.go's
// continuesPRChain) and the listing places it on none.
func TestPRStateReadsAReviewRunsTask(t *testing.T) {
	const slug = "acme-app-3f9c1d2e4b5a6078"
	at := lsNow()
	rows := []runview.Row{
		// PR 21: the run that left it ready, then a review of it that is going.
		prStateRow(slug, "20261112-090000-aa21", at, runstore.StatusSucceeded, runstore.OutcomeReady, 21),
		{Run: slug + "/20261112-090000-bb21", RunID: "20261112-090000-bb21", Created: at,
			Status: string(runstore.StatusRunning)},
	}
	markPRStates(rows, map[string]int{"20261112-090000-bb21": 21})
	for i, want := range []string{prStateLive, prStateLive} {
		if rows[i].PRState != want {
			t.Errorf("row %s carries pr_state %q, want %q", rows[i].RunID, rows[i].PRState, want)
		}
	}

	// The same review run of a launch that names several: no number is a
	// number the run's task gives it, so the pull request it reviews stays
	// whatever its own runs said and the review's row carries no state.
	rows = []runview.Row{
		prStateRow(slug, "20261112-090000-cc21", at, runstore.StatusSucceeded, runstore.OutcomeReady, 22),
		{Run: slug + "/20261112-090000-dd21", RunID: "20261112-090000-dd21", Created: at,
			Status: string(runstore.StatusRunning)},
	}
	markPRStates(rows, nil)
	for i, want := range []string{prStateReady, ""} {
		if rows[i].PRState != want {
			t.Errorf("row %s carries pr_state %q, want %q", rows[i].RunID, rows[i].PRState, want)
		}
	}
}
