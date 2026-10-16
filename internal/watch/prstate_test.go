package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeGH writes an executable gh into a temp dir, first on PATH, whose body is
// script (run after the argv log line). It returns the file every invocation
// appends its argv to, so a test can assert both what was run and that nothing
// was run at all.
func fakeGH(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + calls + "\n" + script
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
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
// failure, 5 a state word that is not one of the three, 6 slow.
const ghStates = `case "$3" in
  1) echo MERGED ;;
  2) echo CLOSED ;;
  3) echo OPEN ;;
  4) echo "gh: could not resolve to a PullRequest" >&2; exit 1 ;;
  5) echo DRAFT ;;
  6) sleep 1; echo OPEN ;;
  *) echo OPEN ;;
esac
`

// The point of the whole thing: a merged or closed pull request leaves the
// list, an open one stays, and nothing else about the row is touched.
func TestPRStateDropsMergedAndClosedKeepsOpen(t *testing.T) {
	calls := fakeGH(t, ghStates)
	p := NewPRState(PRStateOptions{})
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/1", 1),
		readyRow("https://github.com/acme/app/pull/2", 2),
		readyRow("https://github.com/acme/app/pull/3", 3),
	}
	p.Filter(rows)
	p.Settle(5 * time.Second)
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
	if want := "pr view 1 --repo acme/app --json state --jq .state"; !slices.Contains(ghCalls(t, calls), want) {
		t.Fatalf("calls = %v, want %q among them", ghCalls(t, calls), want)
	}
}

// Everything that is not a clean answer fails open: a failing gh, a state word
// gh never prints, and a gh that does not answer in time all leave the row
// listed. The note says so once, and never carries gh's output.
func TestPRStateKeepsRowsWhenGHDisagrees(t *testing.T) {
	calls := fakeGH(t, ghStates)
	p := NewPRState(PRStateOptions{Timeout: 150 * time.Millisecond})
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/4", 4),
		readyRow("https://github.com/acme/app/pull/5", 5),
		readyRow("https://github.com/acme/app/pull/6", 6),
	}
	p.Filter(rows)
	p.Settle(5 * time.Second)
	if got := p.Filter(rows); len(got) != 3 {
		t.Fatalf("kept %d rows, want all three", len(got))
	}
	if p.Note() != "PR state unavailable: "+errGHNoAnswer.Error() {
		t.Fatalf("note = %q", p.Note())
	}
	if strings.Contains(p.Note(), "resolve to a PullRequest") {
		t.Fatalf("the note must not carry gh's output: %q", p.Note())
	}
	if n := len(ghCalls(t, calls)); n != 3 {
		t.Fatalf("calls = %d, want one per row", n)
	}
}

// gh not installed: every row stays listed, and one note says why, once.
func TestPRStateMissingGHKeepsRowsAndNotesOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // no gh here, and not the developer's either
	p := NewPRState(PRStateOptions{})
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/1", 1),
		readyRow("https://github.com/acme/lib/pull/2", 2),
	}
	if got := p.Filter(rows); len(got) != 2 {
		t.Fatalf("kept %d rows, want both", len(got))
	}
	p.Settle(5 * time.Second)
	if got := p.Filter(rows); len(got) != 2 {
		t.Fatalf("kept %d rows after the failed lookup, want both", len(got))
	}
	if want := "PR state unavailable: gh not found"; p.Note() != want {
		t.Fatalf("note = %q, want %q", p.Note(), want)
	}
}

// A URL that is not a GitHub pull request URL is never handed to a subprocess:
// not a Bitbucket URL, and not a hostile one — an appended shell command, a
// space, a newline, a command substitution, a traversal.
func TestPRStateNeverExecutesAnythingForOtherURLs(t *testing.T) {
	calls := fakeGH(t, ghStates)
	p := NewPRState(PRStateOptions{})
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
	p.Settle(time.Second)
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
	p := NewPRState(PRStateOptions{Now: func() time.Time { return now }, OpenTTL: time.Minute})
	rows := []ReadyRow{
		readyRow("https://github.com/acme/app/pull/1", 1),
		readyRow("https://github.com/acme/app/pull/3", 3),
	}
	p.Filter(rows)
	p.Settle(5 * time.Second)
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
	p.Settle(5 * time.Second)
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
// spawned once per redraw.
func TestPRStateRetriesAFailureSlowly(t *testing.T) {
	calls := fakeGH(t, ghStates)
	now := time.Unix(1_700_000_000, 0)
	p := NewPRState(PRStateOptions{Now: func() time.Time { return now }, Retry: time.Minute})
	rows := []ReadyRow{readyRow("https://github.com/acme/app/pull/4", 4)}
	for i := 0; i < 5; i++ {
		p.Filter(rows)
		p.Settle(5 * time.Second)
	}
	if n := len(ghCalls(t, calls)); n != 1 {
		t.Fatalf("calls = %d, want one inside the retry window", n)
	}
	now = now.Add(2 * time.Minute)
	p.Filter(rows)
	p.Settle(5 * time.Second)
	if n := len(ghCalls(t, calls)); n != 2 {
		t.Fatalf("calls = %d, want a retry after the window", n)
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
	p.Settle(time.Second)
	if got := ghCalls(t, calls); got != nil {
		t.Fatalf("gh ran under --no-pr-state: %v", got)
	}
	if p.Note() != "" {
		t.Fatalf("note = %q, want none", p.Note())
	}
}

// Many rows resolve, and never more than Concurrency gh processes at a time.
func TestPRStateBoundsConcurrency(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
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
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + calls + "\n" +
		with("n=$(cat "+live+" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "+live+"\n"+
			"p=$(cat "+peak+" 2>/dev/null || echo 0); [ \"$n\" -gt \"$p\" ] && echo $n > "+peak) +
		"sleep 0.2\n" +
		with("n=$(cat "+live+" 2>/dev/null || echo 0); echo $((n-1)) > "+live) +
		"echo OPEN\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := NewPRState(PRStateOptions{Concurrency: 2})
	var rows []ReadyRow
	for i := 10; i < 18; i++ {
		rows = append(rows, readyRow("https://github.com/acme/app/pull/"+strconv.Itoa(i), i))
	}
	if got := p.Filter(rows); len(got) != len(rows) {
		t.Fatalf("kept %d rows, want all of them listed while unknown", len(got))
	}
	p.Settle(20 * time.Second)
	if got := p.Filter(rows); len(got) != len(rows) {
		t.Fatalf("kept %d rows, want every open one", len(got))
	}
	if n := len(ghCalls(t, calls)); n != len(rows) {
		t.Fatalf("calls = %d, want %d", n, len(rows))
	}
	b, err := os.ReadFile(peak)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "2" {
		t.Fatalf("peak concurrency = %s, want 2", got)
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
	x.m.o.PRState = NewPRState(PRStateOptions{})
	x.m.o.Queued = func() ([]QueuedRun, []FinishedRun, string) {
		return nil, []FinishedRun{
			{Run: "r-merged", Slug: "acme/app", Title: "Already merged", Status: "succeeded",
				Outcome: "ready", PRURL: "https://github.com/acme/app/pull/1", PRNumber: 1, FinishedAt: x.now.Add(-30 * time.Minute)},
			{Run: "r-open", Slug: "acme/app", Title: "Still open", Status: "succeeded",
				Outcome: "ready", PRURL: "https://github.com/acme/app/pull/2", PRNumber: 2, FinishedAt: x.now.Add(-20 * time.Minute)},
		}, ""
	}
	x.seed()
	if s := x.screen(); !strings.Contains(s, "Ready for your review (2)") {
		t.Fatalf("the first frame must draw the list as it is, before any answer:\n%s", s)
	}
	x.m.o.PRState.Settle(5 * time.Second)
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
	if !strings.Contains(s, "r-merged") {
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

// The environment gh inherits is exactly the list in ghEnvKeep: nothing of the
// run's, nothing of the operator's beyond PATH, HOME, a token and a proxy.
func TestGhEnvIsMinimal(t *testing.T) {
	env := map[string]string{
		"PATH": "/bin", "HOME": "/home/x", "GH_TOKEN": "t", "GITHUB_TOKEN": "g",
		"HTTPS_PROXY": "http://p", "no_proxy": "localhost",
		"ANTHROPIC_API_KEY": "secret", "FUGARO_GIT_TOKEN": "secret", "CLAUDECODE": "1",
		"PS1": "x", "LD_PRELOAD": "/tmp/evil.so", "GIT_CONFIG_COUNT": "2",
	}
	got := ghEnv(func(k string) string { return env[k] })
	want := []string{"PATH=/bin", "HOME=/home/x", "GH_TOKEN=t", "GITHUB_TOKEN=g", "HTTPS_PROXY=http://p", "no_proxy=localhost"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %v, want %v", got, want)
	}
	for _, e := range got {
		if strings.Contains(e, "secret") || strings.HasPrefix(e, "LD_PRELOAD=") {
			t.Fatalf("gh inherited %q", e)
		}
	}
}
