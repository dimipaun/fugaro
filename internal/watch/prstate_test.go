package watch

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// fakeGH puts a fake gh first on PATH whose body is script (run after the
// argv log line). It returns the file every invocation appends its argv to, so
// a test can assert both what was run and that nothing was run at all. It
// writes no executable of its own: see testutil.FakeCommand for why.
func fakeGH(t *testing.T, script string) string {
	t.Helper()
	return testutil.FakeCommand(t, "gh", script)
}

// ghCalls is the argv of every fake gh invocation, in order.
func ghCalls(t *testing.T, calls string) []string {
	t.Helper()
	b, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// readyRow is one ready row with just the fields the resolver reads.
func readyRow(url string, n int) ReadyRow {
	return ReadyRow{Repo: "acme/app", Run: RunRow{Run: "r", PRURL: url, PRNumber: n}}
}

func urls(rows []ReadyRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Run.PRURL)
	}
	return out
}

// ghStates answers by pull request number: 1 merged, 2 closed, 3 open, 4 a
// failure, 5 a state word that is not one of the three. Every one of them
// answers at once: a test that is about the ANSWER must never also be about
// how long it took.
const ghStates = `case "$3" in
  1) echo MERGED ;;
  2) echo CLOSED ;;
  3) echo OPEN ;;
  4) echo "gh: could not resolve to a PullRequest" >&2; exit 1 ;;
  5) echo DRAFT ;;
  *) echo OPEN ;;
esac
`

// prStateTestTimeout is how long the tests below let one lookup take. Every
// one of them asserts an ANSWER — which rows survive, what gh was asked, how
// many times — and a test about an answer must never also be a test of how
// long the machine took: a fork, an exec and a shell script can outlast a
// short budget on a loaded machine, and the lookup is then killed and read as
// "gh could not tell the pull request's state" instead of as the answer the
// test means to check. The timeout itself has its own tests
// (TestPRStateGHThatHangsLeavesTheRowListed and
// TestPRStateGHLeavingAChildBehindAnswers), which set their own short one.
const prStateTestTimeout = 30 * time.Second

// waitLookups waits for the lookups Filter started to finish, however long
// they take. Settle(d) is the production wait, and it is bounded because a
// screen has to draw; a test that asserts an answer waits for the lookup
// itself, with no deadline to race, so what it reads is the lookup's answer
// and never the clock's. A lookup that never finishes hangs the test, and a
// hung test is a failure, not a flake.
func waitLookups(p *PRState) {
	if p == nil || p.disabled {
		return
	}
	p.wg.Wait()
}

// The point of the whole thing: a merged or closed pull request leaves the
// list, an open one stays, and nothing else about the row is touched.
func TestPRStateDropsMergedAndClosedKeepsOpen(t *testing.T) {
	calls := fakeGH(t, ghStates)
	p := NewPRState(PRStateOptions{Timeout: prStateTestTimeout})
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/1", 1),
		readyRow("https://github.com/acme/app/pull/2", 2),
		readyRow("https://github.com/acme/app/pull/3", 3),
	}
	p.Filter(rows)
	waitLookups(p)
	got := p.Filter(rows)
	if want := []string{"https://github.com/acme/app/pull/3"}; !slices.Equal(urls(got), want) {
		t.Fatalf("kept %v, want %v", urls(got), want)
	}
	if p.Note() != "" {
		t.Fatalf("note = %q, want none", p.Note())
	}
	if got := ghCalls(t, calls); len(got) != 3 {
		t.Fatalf("calls = %v, want three lookups", got)
	}
	// The argv is exact: no shell, one element per argument.
	if want := "pr view 1 --repo github.com/acme/app --json state --jq .state"; !slices.Contains(ghCalls(t, calls), want) {
		t.Fatalf("calls = %v, want %q among them", ghCalls(t, calls), want)
	}
}

// TestPRStateAsksTheHostTheURLNames is the review finding on a GH_HOST-pinned
// environment handed a hostless --repo. gh resolves a bare `owner/repo`
// against its *default* host, and GH_HOST is what sets that default — ghEnv
// forwards it on purpose, so an operator whose gh is set up for an enterprise
// host keeps that setup. The lookup would then ask that other GitHub for a
// repository of the same name and believe whatever it answered there: a MERGED
// on the wrong host drops a row whose github.com pull request is still open,
// which is the one thing this whole file exists to get right.
//
// ghPRURL matches github.com URLs only, so the host gh is given is the host
// the URL named, and GH_HOST cannot move the question to another GitHub.
// Measured against gh 2.101.0: `--repo acme/app` with GH_HOST set fails with
// "error connecting to github.example.com", `--repo github.com/acme/app` with
// the same environment reaches api.github.com.
func TestPRStateAsksTheHostTheURLNames(t *testing.T) {
	calls := fakeGH(t, "echo OPEN\n")
	t.Setenv("GH_HOST", "github.example.com")
	rows := []ReadyRow{readyRow("https://github.com/acme/app/pull/3", 3)}
	p := NewPRState(PRStateOptions{Timeout: prStateTestTimeout})
	p.Filter(rows)
	waitLookups(p)
	if got := p.Filter(rows); len(got) != 1 {
		t.Fatalf("kept %d rows, want the open one", len(got))
	}
	got := ghCalls(t, calls)
	if len(got) != 1 {
		t.Fatalf("calls = %v, want one lookup", got)
	}
	// The repository gh is asked for is the one the row's URL names, host and
	// all: that is the property, not the literal below it.
	u, err := url.Parse(rows[0].Run.PRURL)
	if err != nil {
		t.Fatal(err)
	}
	want := u.Host + "/" + "acme/app"
	repo := ""
	argv := strings.Fields(got[0])
	for i, a := range argv {
		switch {
		case a == "--repo" && i+1 < len(argv):
			repo = argv[i+1]
		case strings.HasPrefix(a, "--repo="):
			repo = strings.TrimPrefix(a, "--repo=")
		}
	}
	if repo != want {
		t.Fatalf("gh was asked for %q, want %q: a bare owner/repo is resolved against gh's default host, and GH_HOST (github.example.com here) is what sets it", repo, want)
	}
}

// Everything that is not a clean answer fails open: a failing gh and a state
// word gh never prints both leave the row listed. The note says so once, and
// never carries gh's output.
//
// The timeout is prStateTestTimeout, not the production one: both rows here
// answer at once, and a test about the answer must not become a test of how
// fast a loaded machine can start a shell. The timing case has its own test
// below.
func TestPRStateKeepsRowsWhenGHDisagrees(t *testing.T) {
	calls := fakeGH(t, ghStates)
	p := NewPRState(PRStateOptions{Timeout: prStateTestTimeout})
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/4", 4),
		readyRow("https://github.com/acme/app/pull/5", 5),
	}
	p.Filter(rows)
	waitLookups(p)
	if got := p.Filter(rows); len(got) != 2 {
		t.Fatalf("kept %d rows, want both", len(got))
	}
	if p.Note() != "PR state unavailable: "+errGHNoAnswer.Error() {
		t.Fatalf("note = %q", p.Note())
	}
	if strings.Contains(p.Note(), "resolve to a PullRequest") {
		t.Fatalf("the note must not carry gh's output: %q", p.Note())
	}
	if n := len(ghCalls(t, calls)); n != 2 {
		t.Fatalf("calls = %d, want one per row", n)
	}
}

// A gh that does not answer in time fails open like every other bad answer,
// and the lookup comes back when the timeout says so, not when gh feels like
// it: the row stays listed, the note says the state could not be read, and the
// whole thing lasts about the timeout — this fake gh would take 30s.
func TestPRStateGHThatHangsLeavesTheRowListed(t *testing.T) {
	calls := fakeGH(t, "sleep 30; echo OPEN\n")
	timeout := time.Second
	p := NewPRState(PRStateOptions{Timeout: timeout})
	rows := []ReadyRow{readyRow("https://github.com/acme/app/pull/7", 7)}
	p.Filter(rows)
	start := time.Now()
	p.Settle(20 * time.Second)
	elapsed := time.Since(start)
	if got := p.Filter(rows); len(got) != 1 {
		t.Fatalf("kept %d rows, want the timed-out one listed", len(got))
	}
	if want := "PR state unavailable: " + errGHNoAnswer.Error(); p.Note() != want {
		t.Fatalf("note = %q, want %q", p.Note(), want)
	}
	if elapsed > timeout+2*time.Second {
		t.Fatalf("the lookup took %s, want it bounded by its %s timeout", elapsed, timeout)
	}
	if n := len(ghCalls(t, calls)); n != 1 {
		t.Fatalf("calls = %d, want one", n)
	}
}

// gh that leaves a child holding the pipe behind it must not hold the lookup:
// killing gh alone is not enough, since cmd.Output then waits for the pipe to
// close, and the child here keeps it open for 30s. The whole group goes, so the
// answer arrives at once and the state it printed is the state recorded.
func TestPRStateGHLeavingAChildBehindAnswers(t *testing.T) {
	calls := fakeGH(t, "sleep 30 &\necho OPEN\n")
	timeout := time.Second
	p := NewPRState(PRStateOptions{Timeout: timeout})
	rows := []ReadyRow{readyRow("https://github.com/acme/app/pull/8", 8)}
	p.Filter(rows)
	start := time.Now()
	p.Settle(25 * time.Second)
	elapsed := time.Since(start)
	if got := p.Filter(rows); len(got) != 1 {
		t.Fatalf("kept %d rows, want the open one listed", len(got))
	}
	// An OPEN answer landed, which is why there is nothing to say: a lookup
	// that had failed or timed out would carry a note.
	if p.Note() != "" {
		t.Fatalf("note = %q, want none: gh answered OPEN at once", p.Note())
	}
	if elapsed > timeout+2*time.Second {
		t.Fatalf("the lookup took %s: a child of gh held the pipe open past the timeout", elapsed)
	}
	if n := len(ghCalls(t, calls)); n != 1 {
		t.Fatalf("calls = %d, want one", n)
	}
}

// gh not installed: every row stays listed, and one note says why, once.
func TestPRStateMissingGHKeepsRowsAndNotesOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // no gh here, and not the developer's either
	p := NewPRState(PRStateOptions{Timeout: prStateTestTimeout})
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/1", 1),
		readyRow("https://github.com/acme/lib/pull/2", 2),
	}
	if got := p.Filter(rows); len(got) != 2 {
		t.Fatalf("kept %d rows, want both", len(got))
	}
	waitLookups(p)
	if got := p.Filter(rows); len(got) != 2 {
		t.Fatalf("kept %d rows after the failed lookup, want both", len(got))
	}
	if want := "PR state unavailable: gh not found"; p.Note() != want {
		t.Fatalf("note = %q, want %q", p.Note(), want)
	}
}

// A gh that goes away between two frames does not resurrect what it already
// answered, and does not leave a note behind when the rows it describes are
// gone: "gh is not installed" is a fact about the lookups, and nothing about
// it undoes an answer or outlives the list.
func TestPRStateKeepsTheAnswersItHasWhenGHGoesAway(t *testing.T) {
	fakeGH(t, ghStates)
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/1", 1), // merged
		readyRow("https://github.com/acme/app/pull/3", 3), // open
	}
	p := NewPRState(PRStateOptions{Timeout: prStateTestTimeout})
	p.Filter(rows)
	waitLookups(p)
	if got := p.Filter(rows); len(got) != 1 || urls(got)[0] != "https://github.com/acme/app/pull/3" {
		t.Fatalf("kept %v, want the open one only", urls(got))
	}
	// gh disappears — a PATH with none on it, the developer's own included —
	// and a row it has never answered appears with it.
	t.Setenv("PATH", t.TempDir())
	rows = append(rows, readyRow("https://github.com/acme/app/pull/5", 5))
	p.Filter(rows)
	waitLookups(p)
	got := p.Filter(rows)
	if len(got) != 2 {
		t.Fatalf("kept %v, want the open one and the unanswered one — and never the merged one", urls(got))
	}
	if want := "PR state unavailable: gh not found"; p.Note() != want {
		t.Fatalf("note = %q, want %q", p.Note(), want)
	}
	// The last frame holds only the merged row, which its answer takes off the
	// list: with nothing left that a lookup could be failing for, the note has
	// nothing left to describe either.
	if got := p.Filter(rows[:1]); len(got) != 0 {
		t.Fatalf("kept %v, want the merged one still off the list", urls(got))
	}
	if p.Note() != "" {
		t.Fatalf("the note outlived the rows it describes: %q", p.Note())
	}
}

// A URL that is not a GitHub pull request URL is never handed to a subprocess:
// not a Bitbucket URL, and not a hostile one — an appended shell command, a
// space, a newline, a command substitution, a traversal.
func TestPRStateNeverExecutesAnythingForOtherURLs(t *testing.T) {
	calls := fakeGH(t, ghStates)
	p := NewPRState(PRStateOptions{Timeout: prStateTestTimeout})
	rows := []ReadyRow{
		readyRow("https://bitbucket.org/acme/app/pull-requests/7", 7),
		readyRow("https://github.com/a/b/pull/1;rm -rf x", 1),
		readyRow("https://github.com/a/b/pull/1 --json state", 1),
		readyRow("https://github.com/a/b/pull/1\nhttps://github.com/c/d/pull/2", 1),
		readyRow("https://github.com/a/b/pull/$(rm -rf x)", 1),
		readyRow("https://github.com/a/../../b/pull/1", 1),
		readyRow("https://github.com/a/b/pull/01", 1),
		readyRow("http://github.com/a/b/pull/1", 1),
		readyRow("https://github.com/a/b/pulls/1", 1),
		readyRow("https://github.com.evil.example/a/b/pull/1", 1),
		readyRow("", 0),
	}
	if got := p.Filter(rows); len(got) != len(rows) {
		t.Fatalf("kept %d rows, want all %d", len(got), len(rows))
	}
	// Every URL here is one gh must never see, so no lookup was started and
	// this waits for nothing: what it proves is that nothing was ever in
	// flight, not that a second was long enough for something to have run.
	waitLookups(p)
	if got := ghCalls(t, calls); got != nil {
		t.Fatalf("gh was run for a URL it must never see: %v", got)
	}
	if p.Note() != "" {
		t.Fatalf("note = %q, want none: nothing was even attempted", p.Note())
	}
}

// An answer is cached per URL: an open one for its TTL, a merged one for the
// life of the process.
func TestPRStateCachesAnswers(t *testing.T) {
	calls := fakeGH(t, ghStates)
	now := time.Unix(1_700_000_000, 0)
	p := NewPRState(PRStateOptions{Now: func() time.Time { return now }, OpenTTL: time.Minute, Timeout: prStateTestTimeout})
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/1", 1),
		readyRow("https://github.com/acme/app/pull/3", 3),
	}
	p.Filter(rows)
	waitLookups(p)
	if n := len(ghCalls(t, calls)); n != 2 {
		t.Fatalf("calls = %d, want two", n)
	}
	// A second pass inside the TTL re-runs nothing.
	if got := p.Filter(rows); len(got) != 1 {
		t.Fatalf("kept %d rows, want the open one only", len(got))
	}
	if n := len(ghCalls(t, calls)); n != 2 {
		t.Fatalf("calls = %d after a cache hit, want still two", n)
	}
	// Past the open TTL the open one is asked about again; the merged one is
	// never asked about again, however far the clock moves.
	now = now.Add(2 * time.Minute)
	if got := p.Filter(rows); len(got) != 1 {
		t.Fatalf("kept %d rows, want the open one only", len(got))
	}
	waitLookups(p)
	if n := len(ghCalls(t, calls)); n != 3 {
		t.Fatalf("calls = %d, want the open URL re-asked exactly once", n)
	}
	askedMerged := 0
	for _, c := range ghCalls(t, calls) {
		if strings.Contains(c, " view 1 ") {
			askedMerged++
		}
	}
	if askedMerged != 1 {
		t.Fatalf("a merged PR was asked about %d times, want exactly one: %v", askedMerged, ghCalls(t, calls))
	}
}

// A failed answer is retried, but not on every frame: a broken gh must not be
// spawned once per redraw. Each further failure doubles the wait, up to the cap,
// so a gh that stays broken costs a handful of processes an hour per URL rather
// than one a minute, and one that starts working is still noticed within the cap.
func TestPRStateRetriesAFailureSlowly(t *testing.T) {
	calls := fakeGH(t, ghStates)
	now := time.Unix(1_700_000_000, 0)
	p := NewPRState(PRStateOptions{Now: func() time.Time { return now }, Retry: time.Minute, RetryMax: 4 * time.Minute, Timeout: prStateTestTimeout})
	rows := []ReadyRow{readyRow("https://github.com/acme/app/pull/4", 4)}
	want := 0
	// fail asks and checks that the number of gh processes so far is want.
	fail := func(what string) {
		t.Helper()
		want++
		p.Filter(rows)
		waitLookups(p)
		if n := len(ghCalls(t, calls)); n != want {
			t.Fatalf("calls = %d after %s, want %d", n, what, want)
		}
	}
	// quiet checks that nothing ran: the URL is inside its retry window.
	quiet := func(what string) {
		t.Helper()
		p.Filter(rows)
		waitLookups(p)
		if n := len(ghCalls(t, calls)); n != want {
			t.Fatalf("calls = %d after %s, want none more than %d", n, what, want)
		}
	}
	fail("the first frame")
	for i := 0; i < 4; i++ {
		quiet("a frame inside the first window")
	}
	now = now.Add(time.Minute + time.Second)
	fail("the first window has passed")
	// The wait doubles with each failure: a minute after the second failure is
	// inside its two-minute window, and one second past it is not.
	now = now.Add(time.Minute)
	quiet("half of the doubled window")
	now = now.Add(time.Minute + time.Second)
	fail("the doubled window has passed")
	// Then it doubles to the four-minute cap, and stays there: three minutes
	// in nothing runs, four minutes in it does, however long the screen stays
	// open.
	for i := 0; i < 3; i++ {
		now = now.Add(4*time.Minute - time.Second)
		quiet("just inside the capped window")
		now = now.Add(2 * time.Second)
		fail("the capped window has passed")
	}
}

// A gh that comes back clears the note: it describes the lookups failing now,
// not a failure that happened once and was fixed a minute later.
func TestPRStateNoteClearsWhenGHAgreesAgain(t *testing.T) {
	dir := t.TempDir()
	fakeGH(t, "if [ -f "+dir+"/broken ]; then echo 'gh: could not resolve' >&2; exit 1; fi\necho OPEN\n")
	if err := os.WriteFile(filepath.Join(dir, "broken"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_700_000_000, 0)
	p := NewPRState(PRStateOptions{Now: func() time.Time { return now }, Retry: time.Minute, Timeout: prStateTestTimeout})
	rows := []ReadyRow{readyRow("https://github.com/acme/app/pull/1", 1)}
	p.Filter(rows)
	waitLookups(p)
	if want := "PR state unavailable: " + errGHNoAnswer.Error(); p.Note() != want {
		t.Fatalf("note = %q, want %q", p.Note(), want)
	}
	if err := os.Remove(filepath.Join(dir, "broken")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	p.Filter(rows)
	waitLookups(p)
	if p.Note() != "" {
		t.Fatalf("note = %q, want none: gh answered on the retry", p.Note())
	}
}

// --no-pr-state: nothing is asked, nothing is hidden, no note.
func TestPRStateDisabled(t *testing.T) {
	calls := fakeGH(t, ghStates)
	p := NewPRState(PRStateOptions{Disabled: true})
	rows := []ReadyRow{readyRow("https://github.com/acme/app/pull/1", 1)}
	if got := p.Filter(rows); len(got) != 1 {
		t.Fatalf("kept %d rows, want the merged one listed with --no-pr-state", len(got))
	}
	waitLookups(p)
	if got := ghCalls(t, calls); got != nil {
		t.Fatalf("gh ran under --no-pr-state: %v", got)
	}
	if p.Note() != "" {
		t.Fatalf("note = %q, want none", p.Note())
	}
}

// Many rows resolve, never more than Concurrency gh processes run at once, and
// two of them really do run at once.
//
// The timeout is prStateTestTimeout: this test asserts an ANSWER (the peak), so
// it must not also be a test of how fast a loaded machine can fork and exec a
// shell. A gh killed at a short budget would answer nothing, and a killed gh
// never runs its own decrement, which would leave the counter reading a peak
// that is not there.
func TestPRStateBoundsConcurrency(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live")
	peak := filepath.Join(dir, "peak")
	// The script counts how many copies of itself are running at once, the
	// only way to see the bound from outside. The counter is updated under a
	// mkdir mutex: the reads and writes race otherwise, and a lost increment
	// would look like a bound that is not there.
	lock := filepath.Join(dir, "lock")
	with := func(body string) string {
		return "while ! mkdir " + lock + " 2>/dev/null; do sleep 0.01; done\n" + body + "\nrmdir " + lock + "\n"
	}
	calls := fakeGH(t,
		with("n=$(cat "+live+" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "+live+"\n"+
			"p=$(cat "+peak+" 2>/dev/null || echo 0); [ \"$n\" -gt \"$p\" ] && echo $n > "+peak)+
			"sleep 0.2\n"+
			with("n=$(cat "+live+" 2>/dev/null || echo 0); echo $((n-1)) > "+live)+
			"echo OPEN\n")

	p := NewPRState(PRStateOptions{Concurrency: 2, Timeout: prStateTestTimeout})
	var rows []ReadyRow
	for i := 10; i < 18; i++ {
		rows = append(rows, readyRow("https://github.com/acme/app/pull/"+strconv.Itoa(i), i))
	}
	if got := p.Filter(rows); len(got) != len(rows) {
		t.Fatalf("kept %d rows, want all of them listed while unknown", len(got))
	}
	waitLookups(p)
	if got := p.Filter(rows); len(got) != len(rows) {
		t.Fatalf("kept %d rows, want every open one", len(got))
	}
	if n := len(ghCalls(t, calls)); n != len(rows) {
		t.Fatalf("calls = %d, want %d", n, len(rows))
	}
	// The peak is read exactly. Two is the semaphore's bound, so a higher
	// reading is a lookup that ran outside it; and the bound has to be
	// *reached*: eight rows with a gh that holds its slot for 0.2s must put
	// two lookups in flight at once, and a peak of one would mean this test no
	// longer sees the overlap it exists for. A one-sided check would let that
	// pass as a quiet machine.
	b, err := os.ReadFile(peak)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("peak concurrency = %q, want a number", b)
	}
	if n != 2 {
		t.Fatalf("peak concurrency = %d, want exactly 2: the semaphore's bound, and the overlap it exists to allow", n)
	}
}

// An unknown URL never blocks: the row is listed from the first frame, before
// any answer exists.
func TestPRStateListsBeforeAnyAnswer(t *testing.T) {
	fakeGH(t, ghStates)
	p := NewPRState(PRStateOptions{})
	rows := []ReadyRow{readyRow("https://github.com/acme/app/pull/1", 1)}
	if got := p.Filter(rows); len(got) != 1 {
		t.Fatalf("the first frame must draw the row as today")
	}
}

// The interactive screen applies the same filter, and never waits for it: the
// first frame draws the ready list as today, and the answer lands in the next
// frame the tick produces.
func TestTUIPRStateDropsMergedReadyRow(t *testing.T) {
	fakeGH(t, "case \"$3\" in 1) echo MERGED ;; *) echo OPEN ;; esac\n")
	x := newTM(t)
	x.m.o.PRState = NewPRState(PRStateOptions{Timeout: prStateTestTimeout})
	x.m.o.Queued = func() ([]QueuedRun, []FinishedRun, string) {
		// Inside the default window a finished run stays listed for, so the
		// finished rows below are still rows of this screen (item 3): this test
		// is about the ready list's PR states, not about the window.
		return nil, []FinishedRun{
			{Run: "r-merged", Slug: "acme/app", Title: "Already merged", Status: "succeeded",
				Outcome: "ready", PRURL: "https://github.com/acme/app/pull/1", PRNumber: 1, FinishedAt: x.now.Add(-2 * time.Minute)},
			{Run: "r-open", Slug: "acme/app", Title: "Still open", Status: "succeeded",
				Outcome: "ready", PRURL: "https://github.com/acme/app/pull/2", PRNumber: 2, FinishedAt: x.now.Add(-time.Minute)},
		}, ""
	}
	x.seed()
	if s := x.screen(); !strings.Contains(s, "Ready for your review (2)") {
		t.Fatalf("the first frame must draw the list as it is, before any answer:\n%s", s)
	}
	waitLookups(x.m.o.PRState)
	x.m.Update(renderMsg{}) // the tick is what turns an answer into a frame
	s := x.screen()
	if !strings.Contains(s, "Ready for your review (1)") {
		t.Fatalf("the merged PR must leave the list:\n%s", s)
	}
	section := s[strings.Index(s, "Ready for your review"):]
	if strings.Contains(section, "pull/1") || !strings.Contains(section, "pull/2") {
		t.Fatalf("want only the open PR listed:\n%s", section)
	}
	// The row is gone from the ready list only: its finished row is untouched.
	// (The run table names a run by the last four characters of its id, item 1.)
	if !strings.Contains(s, shortRun("r-merged")) {
		t.Fatalf("the finished row itself must stay:\n%s", s)
	}
}

// The footer and the plain frame say once — never once per row — why the ready
// list's PR states are missing, and say nothing when they are not.
func TestNoteSaysWhyPRStatesAreMissing(t *testing.T) {
	v := fixture()
	v = MergeFinished(v, Config{}, []FinishedRun{
		{Run: "r-ready1", Slug: "acme__app", Title: "Fix the login bug", Status: "succeeded", Outcome: "ready",
			PRURL: "https://github.com/acme/app/pull/101", PRNumber: 101, FinishedAt: t0.Add(-30 * time.Minute)},
		{Run: "r-ready2", Slug: "acme__app", Title: "Speed up the parser", Status: "succeeded", Outcome: "ready",
			PRURL: "https://github.com/acme/app/pull/102", PRNumber: 102, FinishedAt: t0.Add(-29 * time.Minute)},
	}, t0)
	note := "PR state unavailable: gh not found"
	got := frame(v, 100, 0, func(o *RenderOptions) { o.Ready = ReadyRowsOf(v); o.PRNote = note })
	if n := strings.Count(got, note); n != 1 {
		t.Fatalf("want exactly one note line, got %d:\n%s", n, got)
	}
	quiet := frame(v, 100, 0, func(o *RenderOptions) { o.Ready = ReadyRowsOf(v) })
	if strings.Contains(quiet, "PR state unavailable") {
		t.Fatalf("no note belongs when there is nothing to say:\n%s", quiet)
	}

	var b strings.Builder
	if err := RenderPlain(&b, "aurora", v, ReadyRowsOf(v), PlainOptions{PRNote: note}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), note); n != 1 {
		t.Fatalf("want exactly one note line in the plain frame, got %d:\n%s", n, b.String())
	}
}

func TestParseGHPRURL(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://github.com/acme/app/pull/1", true},
		{"https://github.com/dimipaun/fugaro/pull/438", true},
		{"https://github.com/a/b.c_d-e/pull/42", true},
		{"https://github.com/a/b/pull/0", false},
		{"https://github.com/a/b/pull/007", false},
		{"https://github.com/a/b/pull/1/", false},
		{"https://github.com/a/b/pull/1\n", false},
		{"https://github.com/a/b/pull/-1", false},
		{"https://github.com/a/b/pull/1.5", false},
		{"https://github.com/-a/b/pull/1", false},
		{"https://github.com/a/b/pull/1", true},
		{"https://github.com/a/.hidden/pull/1", false},
		{"HTTPS://github.com/a/b/pull/1", false},
		{"https://github.com/a/b/pull/1#fragment", false},
	} {
		got, ok := parseGHPRURL(tc.url)
		if ok != tc.want {
			t.Errorf("parseGHPRURL(%q) ok = %v, want %v", tc.url, ok, tc.want)
			continue
		}
		if ok && (got.owner == "" || got.repo == "" || got.number <= 0 || got.url != tc.url) {
			t.Errorf("parseGHPRURL(%q) = %+v", tc.url, got)
		}
	}
}

// The environment gh inherits is exactly the list in ghEnvKeep: what it needs
// to find its configuration, its credentials and its network, and nothing of
// the run's, the agent's or the operator's beyond that.
func TestGhEnvIsMinimal(t *testing.T) {
	env := map[string]string{
		"PATH": "/bin", "HOME": "/home/x", "GH_TOKEN": "t", "GITHUB_TOKEN": "g",
		"GH_CONFIG_DIR": "/home/x/.config/gh", "XDG_CONFIG_HOME": "/home/x/.config",
		"GH_HOST": "github.example.com", "GH_ENTERPRISE_TOKEN": "e",
		"SSL_CERT_FILE": "/etc/ssl/certs/ca.pem", "TERM": "xterm-256color",
		"HTTPS_PROXY": "http://p", "no_proxy": "localhost",
		"ANTHROPIC_API_KEY": "secret", "FUGARO_GIT_TOKEN": "secret", "CLAUDECODE": "1",
		"PS1": "x", "LD_PRELOAD": "/tmp/evil.so", "GIT_CONFIG_COUNT": "2",
	}
	got := ghEnv(func(k string) string { return env[k] })
	want := []string{
		"PATH=/bin", "HOME=/home/x", "GH_TOKEN=t", "GITHUB_TOKEN=g",
		"GH_CONFIG_DIR=/home/x/.config/gh", "XDG_CONFIG_HOME=/home/x/.config",
		"GH_HOST=github.example.com", "GH_ENTERPRISE_TOKEN=e",
		"SSL_CERT_FILE=/etc/ssl/certs/ca.pem", "TERM=xterm-256color",
		"HTTPS_PROXY=http://p", "no_proxy=localhost",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %v, want %v", got, want)
	}
	for _, e := range got {
		if strings.Contains(e, "secret") || strings.HasPrefix(e, "LD_PRELOAD=") {
			t.Fatalf("gh inherited %q", e)
		}
	}
	// A gh of ours never inherits the whole environment, however little is set:
	// a nil Env means just that to both exec and procgroup.
	if got := ghEnv(func(string) string { return "" }); got == nil || len(got) != 0 {
		t.Fatalf("env = %#v, want an empty, non-nil one", got)
	}
}

// gh's own complaint is read only to name the reason, never to carry it: the
// note is one of our fixed sentences.
func TestClassifyNamesTheReasonWithoutQuotingGH(t *testing.T) {
	for _, tc := range []struct {
		complaint string
		want      error
	}{
		{"gh: HTTP 403: API rate limit exceeded for user ID 1", errGHRateLimited},
		{"gh: To get started with GitHub CLI, please run: gh auth login", errGHNotLoggedIn},
		{"gh: authentication required", errGHNotLoggedIn},
		{"gh: could not resolve to a PullRequest with the number 42", errGHNoAnswer},
		{"", errGHNoAnswer},
	} {
		if got := classify(tc.complaint); !errors.Is(got, tc.want) {
			t.Errorf("classify(%q) = %v, want %v", tc.complaint, got, tc.want)
		}
	}
}
