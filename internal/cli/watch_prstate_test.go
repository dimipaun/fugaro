package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/watch"
)

// fakeWatchGH puts a fake gh first on PATH whose body is script; it returns
// the file every invocation appends its argv to, so a test can see both what
// ran and that nothing ran at all. It writes no executable of its own: see
// testutil.FakeCommand for why.
func fakeWatchGH(t *testing.T, script string) string {
	t.Helper()
	return testutil.FakeCommand(t, "gh", script)
}

// prStateTestBudget is how long the tests below let one gh lookup take. Every
// one of them asserts an ANSWER — which row the frame drops, what gh was asked,
// how many times — and a test about an answer must never also be a test of how
// long the machine took: a fork, an exec and a shell script can outlast the
// production 3s, and a lookup killed at its budget records no answer at all,
// which the test then reads as "gh could not tell the pull request's state"
// (fail open: every row kept) instead of the answer it means to check. The
// budget itself has its own tests, in the package that owns it
// (internal/watch: TestPRStateGHThatHangsLeavesTheRowListed and
// TestPRStateGHLeavingAChildBehindAnswers), which set their own short one.
const prStateTestBudget = 30 * time.Second

// longPRStateBudget gives the ready list's gh lookups room a loaded machine can
// meet, for the rest of the test. It covers both halves of the wait: the
// lookup's own budget, and how long one --once snapshot waits for it, which
// must be longer — a lookup killed at its budget answers "no answer", so a wait
// that expired first would leave the test reading the clock rather than the
// answer.
func longPRStateBudget(t *testing.T) {
	t.Helper()
	oldTimeout, oldWait := prStateTimeout, prStateOnceWait
	prStateTimeout, prStateOnceWait = prStateTestBudget, 2*prStateTestBudget
	t.Cleanup(func() { prStateTimeout, prStateOnceWait = oldTimeout, oldWait })
}

// waitPRStateAnswers waits for the n lookups p was asked to start to land.
// Settle(d) is not usable for that: it is a deadline of its own, started when
// the caller asks, before the lookup goroutine has set its own — and a wait
// equal to the budget is then a race against the clock the lookup is killed by.
// Version counts answers landed (a failure counts: it is an answer), so this
// waits for the work itself. The deadline is twice the budget, so it can only
// ever fire when the answer is genuinely not coming, and a test that gets there
// fails rather than flakes.
func waitPRStateAnswers(t *testing.T, p *watch.PRState, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * prStateTestBudget)
	for p.Version() < uint64(n) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d PR-state lookups answered", p.Version(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func ghWatchCalls(t *testing.T, calls string) []string {
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

// readySection is the "Ready for your review" part of a plain frame, "" when
// the frame has none.
func readySection(frame string) string {
	i := strings.Index(frame, "Ready for your review")
	if i < 0 {
		return ""
	}
	return frame[i:]
}

// The owner's request (2026-10-16): a merged or closed pull request leaves the
// ready list, an open one stays, and the header's count is the filtered one.
func TestWatchPlainDropsMergedAndClosedPRs(t *testing.T) {
	longPRStateBudget(t)
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	merged, closed, open := runID(10*time.Minute, "aa01"), runID(9*time.Minute, "aa02"), runID(8*time.Minute, "aa03")
	for _, r := range []struct {
		run string
		n   int
	}{{merged, 42}, {closed, 44}, {open, 43}} {
		f.writeTask(t, appSlug, r.run, "")
		f.writeFinishedResult(t, appSlug, r.run, "succeeded", "ready", r.n,
			"https://github.com/acme/app/pull/"+itoa(r.n), 10*time.Minute)
	}
	calls := fakeWatchGH(t, "case \"$3\" in 42) echo MERGED ;; 44) echo CLOSED ;; *) echo OPEN ;; esac\n")

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "Ready for your review (1)") {
		t.Fatalf("want the count to reflect the filtered list:\n%s", plain)
	}
	section := readySection(plain)
	if !strings.Contains(section, "https://github.com/acme/app/pull/43") {
		t.Fatalf("the open PR must stay listed:\n%s", section)
	}
	for _, gone := range []string{"pull/42", "pull/44"} {
		if strings.Contains(section, gone) {
			t.Fatalf("%s is merged or closed and must not be listed:\n%s", gone, section)
		}
	}
	if got := ghWatchCalls(t, calls); len(got) != 3 {
		t.Fatalf("calls = %v, want one per ready PR", got)
	}
	// The repository gh is asked for carries its host: a bare owner/repo would
	// be resolved against gh's default host, which GH_HOST sets.
	if want := "pr view 42 --repo github.com/acme/app --json state --jq .state"; !containsStr(ghWatchCalls(t, calls), want) {
		t.Fatalf("calls = %v, want %q among them", ghWatchCalls(t, calls), want)
	}
	if strings.Contains(plain, "PR state unavailable") {
		t.Fatalf("no note belongs here:\n%s", plain)
	}
}

// gh not installed: every ready row stays listed (today's behaviour), with one
// note saying so — not one per row, and not one per frame.
func TestWatchPlainWithoutGHKeepsRowsAndNotesOnce(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	for _, r := range []struct {
		run string
		n   int
	}{{runID(10*time.Minute, "bb01"), 42}, {runID(9*time.Minute, "bb02"), 43}} {
		f.writeTask(t, appSlug, r.run, "")
		f.writeFinishedResult(t, appSlug, r.run, "succeeded", "ready", r.n,
			"https://github.com/acme/app/pull/"+itoa(r.n), 10*time.Minute)
	}
	t.Setenv("PATH", t.TempDir()) // no gh at all, the developer's included

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "Ready for your review (2)") {
		t.Fatalf("both PRs must stay listed without gh:\n%s", plain)
	}
	if n := strings.Count(plain, "PR state unavailable: gh not found"); n != 1 {
		t.Fatalf("want exactly one note, got %d:\n%s", n, plain)
	}
}

// A gh that fails leaves the row listed, with one note that names the reason
// in words of ours, and never leaks what gh printed.
func TestWatchPlainWithFailingGHKeepsRows(t *testing.T) {
	longPRStateBudget(t)
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(10*time.Minute, "cc01")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 42, "https://github.com/acme/app/pull/42", 10*time.Minute)
	fakeWatchGH(t, "echo 'gh: HTTP 403: API rate limit exceeded for user ID 12345' >&2; exit 1\n")

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "Ready for your review (1)") {
		t.Fatalf("a failing gh must fail open:\n%s", plain)
	}
	if want := "PR state unavailable: gh is rate limited"; !strings.Contains(plain, want) {
		t.Fatalf("want the note %q:\n%s", want, plain)
	}
	// The reason is a sentence of ours; not a word gh printed reaches the frame.
	for _, leaked := range []string{"HTTP 403", "12345"} {
		if strings.Contains(plain, leaked) {
			t.Fatalf("gh's own output (%q) must never be shown:\n%s", leaked, plain)
		}
	}
	if n := strings.Count(plain, "PR state unavailable"); n != 1 {
		t.Fatalf("want exactly one note, got %d:\n%s", n, plain)
	}
}

// --no-pr-state runs nothing at all, and lists every ready PR: it is what an
// offline operator (and every test that is not about PR state) uses.
func TestWatchNoPRStateRunsNothing(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(10*time.Minute, "dd01")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 42, "https://github.com/acme/app/pull/42", 10*time.Minute)
	calls := fakeWatchGH(t, "echo MERGED\n")

	plain, _, err := execute(t, "watch", "--once", "--plain", "--no-pr-state")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "Ready for your review (1)") {
		t.Fatalf("--no-pr-state must list every ready PR:\n%s", plain)
	}
	if got := ghWatchCalls(t, calls); got != nil {
		t.Fatalf("--no-pr-state ran gh: %v", got)
	}
}

// Only a URL of exactly the GitHub pull request form is ever handed to gh. A
// Bitbucket URL and a hostile one are listed and never executed; the fake gh
// proves nothing ran.
func TestWatchNeverExecutesGHForOtherURLs(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	for i, url := range []string{
		"https://bitbucket.org/acme/app/pull-requests/7",
		"https://github.com/a/b/pull/1;rm -rf x",
		"https://github.com/a/b/pull/$(id)",
		"https://github.com/a/../../b/pull/1",
	} {
		run := runID(time.Duration(10+i)*time.Minute, "ee0"+itoa(i))
		f.writeTask(t, appSlug, run, "")
		f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 7, url, 10*time.Minute)
	}
	calls := fakeWatchGH(t, "echo MERGED\n")

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "Ready for your review (4)") {
		t.Fatalf("every row must be listed:\n%s", plain)
	}
	if got := ghWatchCalls(t, calls); got != nil {
		t.Fatalf("gh ran for a URL it must never see: %v", got)
	}
}

// The live plain path — fugaro watch --plain, and every non-TTY fugaro watch —
// applies the filter frame by frame, and not only in --once: the first frame
// draws the list as it is, and the frame after the answer drops the merged PR.
// A live frame has no wait of its own, so what it drops comes from the filter's
// return value; discarding that value is this test's whole subject.
func TestEmitLiveFramesApplyPRState(t *testing.T) {
	fakeWatchGH(t, "case \"$3\" in 42) echo MERGED ;; *) echo OPEN ;; esac\n")
	now := time.Now().UTC()
	v := watch.View{Repos: []watch.RepoBlock{{Slug: "a", Name: "acme/app"}}}
	v = watch.MergeFinished(v, watch.Config{}, []watch.FinishedRun{
		{Run: "r-merged", Slug: "a", Title: "Already merged", Status: "succeeded", Outcome: "ready",
			PRURL: "https://github.com/acme/app/pull/42", PRNumber: 42, FinishedAt: now.Add(-10 * time.Minute)},
		{Run: "r-open", Slug: "a", Title: "Still open", Status: "succeeded", Outcome: "ready",
			PRURL: "https://github.com/acme/app/pull/43", PRNumber: 43, FinishedAt: now.Add(-9 * time.Minute)},
	}, now)
	var buf bytes.Buffer
	// The lookup's own timeout is generous on purpose: this test asserts the
	// ANSWER (which row the frame drops), so it must not also be a test of how
	// fast a loaded machine can start a shell — a lookup killed at a short
	// timeout would answer "unknown", leave the row listed, and fail here as
	// if the filter were broken.
	d := &WatchDeps{Out: &buf, LC: &localcfg.Config{Name: "aurora"},
		Filter:  watch.Filter{Keep: 6 * time.Hour, KeepCount: 15, FailedKeep: 24 * time.Hour},
		Acks:    watch.LoadAcks(filepath.Join(t.TempDir(), "watch-acks.json")),
		PRState: watch.NewPRState(watch.PRStateOptions{Timeout: prStateTestBudget})}
	o := &watchOptions{plain: true}

	if err := d.emit(o, v, false); err != nil {
		t.Fatal(err)
	}
	if first := buf.String(); !strings.Contains(first, "Ready for your review (2)") {
		t.Fatalf("the first live frame must draw the list as it is:\n%s", first)
	}
	// Both rows' lookups: the wait is for those answers, not a deadline of its
	// own racing them (waitPRStateAnswers says why).
	waitPRStateAnswers(t, d.PRState, 2)
	buf.Reset()
	if err := d.emit(o, v, false); err != nil {
		t.Fatal(err)
	}
	second := buf.String()
	if !strings.Contains(second, "Ready for your review (1)") {
		t.Fatalf("the next live frame must drop the merged PR:\n%s", second)
	}
	section := readySection(second)
	if strings.Contains(section, "pull/42") || !strings.Contains(section, "pull/43") {
		t.Fatalf("want only the open PR listed:\n%s", section)
	}
}

// The live plain stream redraws when a PR-state answer lands. A plain stream
// has no other reason to print again — nothing changed in the database — so
// watchStream's version comparison is the whole mechanism that turns an answer
// into a frame there; without it the merged PR would sit in the frame for as
// long as the stream ran. The screen's equivalent is a test of another package:
// watch.TestTUIPRStateDropsMergedReadyRow (internal/watch/prstate_test.go), which
// proves the drawn frame loses the row for the same reason.
func TestWatchStreamRedrawsWhenAPRStateAnswerLands(t *testing.T) {
	longPRStateBudget(t)
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	for _, r := range []struct {
		run string
		n   int
	}{{runID(10*time.Minute, "ab01"), 42}, {runID(9*time.Minute, "ab02"), 43}} {
		f.writeTask(t, appSlug, r.run, "")
		f.writeFinishedResult(t, appSlug, r.run, "succeeded", "ready", r.n,
			"https://github.com/acme/app/pull/"+itoa(r.n), 10*time.Minute)
	}
	fakeWatchGH(t, "case \"$3\" in 42) echo MERGED ;; *) echo OPEN ;; esac\n")
	// The finished-run source polls the bucket on its own clock, and a test
	// that wants the ready list must not wait a minute for its second scan
	// (TestWatchStreamShowsQueuedRun does the same).
	oldPoll := queuedNextPoll
	queuedNextPoll = func() time.Duration { return 20 * time.Millisecond }
	t.Cleanup(func() { queuedNextPoll = oldPoll })

	cmd := NewRootCmd()
	out := &lockedBuf{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	// A short interval: this is about the trigger, not about the 10s cadence.
	cmd.SetArgs([]string{"watch", "--plain", "--interval", "200ms"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(out.String(), "Ready for your review (1)") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Ready for your review (2)") {
		t.Fatalf("the first frame must draw the list as it is:\n%s", got)
	}
	// The frame the answer produced — the last one — has the merged PR gone.
	frames := strings.Split(got, "\n--- ")
	section := readySection(frames[len(frames)-1])
	if strings.Contains(section, "pull/42") || !strings.Contains(section, "pull/43") {
		t.Fatalf("want only the open PR listed in the last frame:\n%s", section)
	}
}

// The --json feed is a contract the desktop console reads (the JSON contract
// doc, §2): the PR-state filter is a human-view filter and never touches it. The
// document is identical with the filter on and off — a merged PR is still in
// ready[] — and its key sets are pinned against a golden, so neither this
// change nor a later one can add or drop a field by accident.
func TestWatchJSONUnaffectedByPRState(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(10*time.Minute, "ff01")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 42, "https://github.com/acme/app/pull/42", 10*time.Minute)
	// The run records who launched it, so the shape golden covers the
	// launcher's key too (a launch that recorded no launcher has no
	// key, which the same field's own tests pin).
	f.writeLaunchBy(t, appSlug, run, "dana@example.com", time.Now().Add(-11*time.Minute))
	calls := fakeWatchGH(t, "echo MERGED\n")

	with, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	without, _, err := execute(t, "watch", "--once", "--json", "--no-pr-state")
	if err != nil {
		t.Fatal(err)
	}
	// --json is the machine view and consults nothing: the feed is the runs
	// bucket's own record, so not one gh ran.
	if got := ghWatchCalls(t, calls); got != nil {
		t.Fatalf("--json ran gh: %v", got)
	}

	var a, b map[string]any
	if err := json.Unmarshal([]byte(with), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(without), &b); err != nil {
		t.Fatal(err)
	}
	// The values move between two runs (ages, the clock), so the comparison is
	// of the shape: every key, at every level, in both documents.
	if sa, sb := shapeOf(a), shapeOf(b); sa != sb {
		t.Fatalf("--json's shape differs with the PR-state filter on:\n%s\n\n%s\n\n%s", sa, sb, with)
	}
	ready, _ := a["ready"].([]any)
	if len(ready) != 1 {
		t.Fatalf("ready = %v, want the merged PR still in the feed", a["ready"])
	}
	first, _ := ready[0].(map[string]any)
	if first["pr_url"] != "https://github.com/acme/app/pull/42" {
		t.Fatalf("ready[0] = %v", first)
	}

	if got := keysOf(t, a); !reflect.DeepEqual(got, goldenKeys(t, "watch_json_keys.golden")) {
		t.Fatalf("watch --json top-level keys = %v", got)
	}
	if got := keysOf(t, first); !reflect.DeepEqual(got, goldenKeys(t, "watch_json_ready_keys.golden")) {
		t.Fatalf("watch --json ready[] keys = %v", got)
	}
}

// shapeOf is a decoded document's structure without its values: every key at
// every level, with the kind of value under it. Two documents with the same
// shape carry the same fields, which is the thing the console's contract is
// about.
func shapeOf(v any) string {
	switch t := v.(type) {
	case map[string]any:
		var parts []string
		for k, sub := range t {
			parts = append(parts, k+":"+shapeOf(sub))
		}
		slices.Sort(parts)
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		return "[" + shapeOf(t[0]) + "]"
	case nil:
		return "null"
	default:
		return strings.TrimPrefix(reflect.TypeOf(t).String(), "json.")
	}
}

// keysOf is m's keys, sorted: the shape of a document, without its values.
func keysOf(t *testing.T, m map[string]any) []string {
	t.Helper()
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// goldenKeys reads testdata/<name>, one key per line.
func goldenKeys(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("%v (create it: one JSON key per line, sorted)", err)
	}
	return strings.Fields(string(b))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func containsStr(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
