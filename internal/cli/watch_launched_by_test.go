package cli

// fugaro watch and fugaro runs ls read the same launch.json, so they must
// agree about who started a run: the same full identity in both --json
// documents, the same short form in both text frames, and no key at all when
// the record has none. Several people start runs on one project at once, and a
// run that reads differently in the two listings would be worse than either
// reading alone.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// watchLauncherRow is one run of watch --json, read for its launcher only.
type watchLauncherRow struct {
	Run        string `json:"run"`
	LaunchedBy string `json:"launched_by"`
}

// watchLauncherJSON runs watch --once --json and returns its runs and finished
// rows by id, plus the document itself.
func watchLauncherJSON(t *testing.T) (map[string]string, string) {
	t.Helper()
	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs     []watchLauncherRow `json:"runs"`
		Finished []watchLauncherRow `json:"finished"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	by := map[string]string{}
	for _, r := range append(append([]watchLauncherRow{}, doc.Runs...), doc.Finished...) {
		by[r.Run] = r.LaunchedBy
	}
	return by, out
}

// TestWatchAndLsAgreeOnTheLauncher: one launch.json, two listings, one answer —
// a queued run (no registry entry yet), a finished run, and a run whose
// launch.json recorded no launcher. The JSON carries the whole identity in
// both commands and the key is absent from both when there is none; the text
// frames show the same short form in both.
func TestWatchAndLsAgreeOnTheLauncher(t *testing.T) {
	// The cloud fixture's RTDB: runs ls reads the registry and lists the
	// backend's executions, watch reads the registry and the runs bucket, and
	// both must place the same launcher on the same run.
	f := newLaunchBudget(t, "observe")
	// A live run: the registry entry watch draws its row from, which carries no
	// launcher (the runner never learns one), beside the launch.json that does.
	live := runID(time.Minute, "dddd")
	seedSpecBy(t, f.cloudFixture, firstRunSpec(live), "frank@example.com")
	ms := time.Now().UnixMilli()
	f.db.Set(budget.PathAgent(appSlug, live), map[string]any{
		"repo": "acme/app", "title": "live", "stage": "code", "auth": "api_key", "spent": usd1,
		"startedAt": ms - 60_000, "updatedAt": ms})
	// A run launched and not started yet: watch shows it queued, ls launching.
	// The IDs are minted minutes ago, not hours: queued detection looks back
	// over a run's mint time (queuedLookback, plus a margin).
	queued := runID(2*time.Minute, "aaaa")
	seedSpecBy(t, f.cloudFixture, firstRunSpec(queued), "dana@example.com")
	// A finished run, launched by an automated identity.
	done := runID(4*time.Minute, "bbbb")
	seedSpecBy(t, f.cloudFixture, firstRunSpec(done), "ci-runner@proj.iam.gserviceaccount.com")
	finishedAt := time.Now().Add(-20 * time.Minute)
	writeRecord(t, f.cloudFixture, done, &runstore.Record{Version: 1, RunID: done, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeNone,
		StartedAt: time.Now().Add(-25 * time.Minute), FinishedAt: &finishedAt})
	// A launch that recorded no launcher (a backfill, a local run, or the
	// synthetic launch `fugaro logs` builds).
	old := runID(6*time.Minute, "cccc")
	seedSpecBy(t, f.cloudFixture, firstRunSpec(old), "")
	writeRecord(t, f.cloudFixture, old, &runstore.Record{Version: 1, RunID: old, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeNone,
		StartedAt: time.Now().Add(-30 * time.Minute), FinishedAt: &finishedAt})

	watchBy, watchOut := watchLauncherJSON(t)
	ls, _ := lsJSON(t)
	lsBy := map[string]string{}
	for _, r := range ls.Runs {
		lsBy[r.RunID] = r.LaunchedBy
	}
	want := map[string]string{
		live:   "frank@example.com",
		queued: "dana@example.com",
		done:   "ci-runner@proj.iam.gserviceaccount.com",
		old:    "",
	}
	for run, w := range want {
		if watchBy[run] != w {
			t.Errorf("watch: %s's launched_by = %q, want %q\n%s", run, watchBy[run], w, watchOut)
		}
		if lsBy[run] != w {
			t.Errorf("runs ls: %s's launched_by = %q, want %q", run, lsBy[run], w)
		}
		if watchBy[run] != lsBy[run] {
			t.Errorf("%s reads %q in watch and %q in runs ls", run, watchBy[run], lsBy[run])
		}
	}
	// Absent means absent: three runs have a launcher, the fourth has no key at
	// all, in both documents. A key that were always there would make "not
	// recorded" indistinguishable from an empty identity.
	for _, doc := range []string{watchOut, lsRawJSON(t)} {
		if n := strings.Count(doc, `"launched_by"`); n != 3 {
			t.Errorf("%d launched_by keys in a document with three launchers:\n%s", n, doc)
		}
	}

	// The text frames agree too: the short form of an address in both, the
	// whole value for an identity with no @ to cut at. --all, because the two
	// finished runs here finished half an hour ago and the default window a
	// finished run stays listed for is three minutes (owner spec 2026-10-22,
	// item 3); runs ls lists them either way.
	watchText, _, err := execute(t, "watch", "--once", "--plain", "--all")
	if err != nil {
		t.Fatal(err)
	}
	lsText, _, err := execute(t, "runs", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []struct{ what, text string }{{"watch", watchText}, {"runs ls", lsText}} {
		for _, w := range []string{"dana", "ci-runner"} {
			if !strings.Contains(got.text, w) {
				t.Errorf("%s does not show the launcher %q:\n%s", got.what, w, got.text)
			}
		}
		if strings.Contains(got.text, "dana@example.com") || strings.Contains(got.text, "ci-runner@") {
			t.Errorf("%s shows the whole address, not the short form:\n%s", got.what, got.text)
		}
	}
}

// TestWatchFollowUpShowsItsOwnLauncher: a follow-up records its own launcher
// at its own launch, so watch shows the follow-up's identity on the follow-up's
// row and the first run's on the first run's — never the first run's launcher
// carried forward — and runs ls --pr agrees. Mutation caught: carrying the
// root run's launcher onto a follow-up's row.
func TestWatchFollowUpShowsItsOwnLauncher(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	root, fu := runID(10*time.Minute, "aaaa"), runID(2*time.Minute, "bbbb")
	seedSpecBy(t, f.cloudFixture, firstRunSpec(root), "dana@example.com")
	finishedAt := time.Now().Add(-8 * time.Minute)
	writeRecord(t, f.cloudFixture, root, &runstore.Record{Version: 1, RunID: root, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady, PR: &runstore.PRRef{Number: 7, URL: "https://github.com/acme/app/pull/7"},
		StartedAt: time.Now().Add(-12 * time.Minute), FinishedAt: &finishedAt})
	// The follow-up is launched by someone else, and is running: its registry
	// entry (which carries no launcher) beside its own launch.json.
	seedSpecBy(t, f.cloudFixture, followUpSpec(fu, root, root, 7), "erin@example.com")
	ms := time.Now().UnixMilli()
	f.db.Set(budget.PathAgent(appSlug, fu), map[string]any{
		"repo": "acme/app", "title": "the follow-up", "stage": "code", "auth": "api_key", "spent": usd1,
		"startedAt": ms - 60_000, "updatedAt": ms})

	watchBy, watchOut := watchLauncherJSON(t)
	if watchBy[root] != "dana@example.com" || watchBy[fu] != "erin@example.com" {
		t.Fatalf("watch's launchers = %v, want each run's own (root dana, follow-up erin):\n%s", watchBy, watchOut)
	}
	ls, _ := lsJSON(t, "--pr", "7", "--repo", "acme/app")
	lsBy := map[string]string{}
	for _, r := range ls.Runs {
		lsBy[r.RunID] = r.LaunchedBy
	}
	if lsBy[root] != "dana@example.com" || lsBy[fu] != "erin@example.com" {
		t.Fatalf("runs ls --pr 7's launchers = %v, want each run's own", lsBy)
	}
}

// lsRawJSON is the raw runs ls --json document.
func lsRawJSON(t *testing.T) string {
	t.Helper()
	out, _, err := execute(t, "runs", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWatchLauncherIsSanitised: the launcher is written by whoever launched the
// run, so watch must not print it as it is — not in the JSON, which the console
// reads, and not in the plain frame, which a terminal draws. Mutation caught:
// passing launch.json's value through unsanitised.
func TestWatchLauncherIsSanitised(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(2*time.Minute, "d010")
	f.writeTask(t, appSlug, run, "")
	f.writeLaunchBy(t, appSlug, run, "root\x1b]0;PWNED\x07\x9b2J\u202eevil@example.com", time.Now().Add(-2*time.Minute))

	by, out := watchLauncherJSON(t)
	if strings.Contains(by[run], "PWNED") || strings.ContainsAny(by[run], "\x1b\x07\u009b\u202e") {
		t.Fatalf("an escape sequence leaked into watch --json: %q\n%s", by[run], out)
	}
	if !strings.Contains(by[run], "root") {
		t.Fatalf("the launcher was erased (%q) rather than sanitised", by[run])
	}
	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "PWNED") || strings.ContainsAny(plain, "\x1b\x07\u009b\u202e") {
		t.Fatalf("plain: %q", plain)
	}
	// A finished run's launcher clears the same bar: it arrives by a different
	// path (the result.json scan, watch.FinishedRun), so it is sanitised there
	// too, not only on the queued path.
	done := runID(6*time.Minute, "d012")
	f.writeTask(t, appSlug, done, "")
	f.writeLaunchBy(t, appSlug, done, "\x1b[2Jroot@example.com", time.Now().Add(-6*time.Minute))
	f.writeFinishedResult(t, appSlug, done, "succeeded", "none", 0, "", 5*time.Minute)
	by, out = watchLauncherJSON(t)
	if strings.Contains(by[done], "\x1b") || !strings.Contains(by[done], "root") {
		t.Fatalf("a finished run's launcher = %q\n%s", by[done], out)
	}
	// A launcher far past any cap is capped, not printed whole: launch.json is
	// bucket text, and a value nobody bounds is a value that can hide the rest
	// of the frame.
	huge := runID(2*time.Minute, "d011")
	f.writeTask(t, appSlug, huge, "")
	f.writeLaunchBy(t, appSlug, huge, strings.Repeat("x", 10_000)+"@example.com", time.Now().Add(-2*time.Minute))
	by, _ = watchLauncherJSON(t)
	if n := len([]rune(by[huge])); n == 0 || n > 513 {
		t.Fatalf("a 10 KB launcher came through as %d runes", n)
	}
}
