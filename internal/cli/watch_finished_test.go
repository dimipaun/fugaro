package cli

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeFinishedResult writes a terminal result.json with a PR and an
// outcome, finished ago ago.
func (f *budgetFixture) writeFinishedResult(t *testing.T, slug, run, status, outcome string, prNumber int, prURL string, ago time.Duration) {
	t.Helper()
	at := time.Now().Add(-ago).UTC().Format(time.RFC3339)
	f.writeRunObject(t, slug, run, "result.json",
		`{"version":1,"run_id":"`+run+`","repo":"acme/app","status":"`+status+`","outcome":"`+outcome+`",`+
			`"pr":{"number":`+strconv.Itoa(prNumber)+`,"url":"`+prURL+`"},"started_at":"`+at+`","finished_at":"`+at+`"}`)
}

// --json carries a "finished" run and, when it is ready, the same row again
// under "ready" (design generic-tool §10.1 and §10.3); --plain shows both
// the finished table and the "Ready for your review" section.
func TestWatchJSONAndPlainShowFinishedAndReady(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	// Two minutes, not ten: the default window a finished run stays listed for
	// is three minutes (owner spec 2026-10-22, item 3), and this test is about
	// what --plain and --json carry, not about the window.
	run := runID(2*time.Minute, "f001")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 42, "https://github.com/acme/app/pull/42", 2*time.Minute)

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Finished []struct {
			Run     string `json:"run"`
			Outcome string `json:"outcome"`
		} `json:"finished"`
		Ready []struct {
			Run   string `json:"run"`
			PRURL string `json:"pr_url"`
		} `json:"ready"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range doc.Finished {
		if r.Run == run && r.Outcome == "ready" {
			found = true
		}
	}
	if !found {
		t.Fatalf("finished run missing from the json finished array:\n%s", out)
	}
	if len(doc.Ready) != 1 || doc.Ready[0].Run != run || doc.Ready[0].PRURL != "https://github.com/acme/app/pull/42" {
		t.Fatalf("ready = %+v", doc.Ready)
	}

	// --no-pr-state: this test is about the ready list's own rule, and the
	// default would ask the developer's own gh about acme/app (a real
	// command over a real network); the PR-state filter is tested on its own
	// in watch_prstate_test.go.
	plain, _, err := execute(t, "watch", "--once", "--plain", "--no-pr-state")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, run) || !strings.Contains(plain, "Ready for your review") || !strings.Contains(plain, "https://github.com/acme/app/pull/42") {
		t.Fatalf("plain output missing the finished run or the review section:\n%s", plain)
	}
}

// A successful run older than --keep's default (three minutes since the
// owner spec of 2026-10-22) is hidden from --plain, and shown again with
// --all (design generic-tool §10.3); --json never hides it. A failure ages
// out on the same window: FailedKeep is FinishedKeep now, so the rule this
// test pins is no longer a success-only one.
func TestWatchAllFlagShowsOldSuccess(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(10*time.Hour, "01d0")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "none", 0, "", 10*time.Hour)

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, run) {
		t.Fatalf("a 10h-old success must be hidden by the default --keep (3m):\n%s", plain)
	}

	all, _, err := execute(t, "watch", "--once", "--plain", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, run) {
		t.Fatalf("--all must show the old success:\n%s", all)
	}

	kept, _, err := execute(t, "watch", "--once", "--plain", "--keep", "24h")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kept, run) {
		t.Fatalf("--keep 24h must show a 10h-old run:\n%s", kept)
	}

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, run) {
		t.Fatalf("--json must never hide a finished run:\n%s", out)
	}
}

// The default window is three minutes, not six hours (owner spec
// 2026-10-22, item 3): a run that finished four minutes ago has left the
// list by itself, one that finished a minute ago is still there, and --all
// (or a wider --keep) is how history comes back.
func TestWatchDefaultWindowIsThreeMinutes(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	fresh, stale := runID(time.Minute, "f1a2"), runID(4*time.Minute, "f3b4")
	f.writeTask(t, appSlug, fresh, "")
	f.writeFinishedResult(t, appSlug, fresh, "succeeded", "none", 0, "", time.Minute)
	f.writeTask(t, appSlug, stale, "")
	f.writeFinishedResult(t, appSlug, stale, "succeeded", "none", 0, "", 4*time.Minute)

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, fresh) {
		t.Fatalf("a run finished a minute ago must be listed:\n%s", plain)
	}
	if strings.Contains(plain, stale) {
		t.Fatalf("a run finished four minutes ago must have left the list:\n%s", plain)
	}
}

// A ready PR stays in --plain's "Ready for your review" section even once
// its own row has aged out of the default --keep (three minutes): the recency filter
// bounds how much finished-run history the frame shows, not which PRs still
// need a look (design generic-tool §10.1). This is the --plain counterpart
// of TestReadyForReviewSurvivesTheFilter (the interactive screen).
func TestWatchPlainReadySurvivesKeepFilter(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(10*time.Hour, "f2ad")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 7, "https://github.com/acme/app/pull/7", 10*time.Hour)

	// --no-pr-state: without it the default would ask the developer's own gh
	// about acme/app — a real command over a real network — and this test is
	// about --keep, not about the PR-state filter (watch_prstate_test.go).
	plain, _, err := execute(t, "watch", "--once", "--plain", "--no-pr-state")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "FINISHED") && strings.Contains(plain[:strings.Index(plain, "Ready for your review")], run) {
		t.Fatalf("the 10h-old row should have aged out of the regular finished table:\n%s", plain)
	}
	if !strings.Contains(plain, "Ready for your review (1)") || !strings.Contains(plain, "https://github.com/acme/app/pull/7") {
		t.Fatalf("the ready PR must still be listed even though --keep (3m) hid its row:\n%s", plain)
	}
}

func TestWatchKeepFlagsValidate(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "watch", "--once", "--keep", "0"); err == nil || !strings.Contains(err.Error(), "--keep") {
		t.Fatalf("--keep 0: err = %v, want a usage error naming --keep", err)
	}
	if _, _, err := execute(t, "watch", "--once", "--keep-count", "0"); err == nil || !strings.Contains(err.Error(), "--keep-count") {
		t.Fatalf("--keep-count 0: err = %v, want a usage error naming --keep-count", err)
	}
}
