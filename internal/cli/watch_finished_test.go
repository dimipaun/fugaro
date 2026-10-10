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
	run := runID(10*time.Minute, "f001")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 42, "https://github.com/acme/app/pull/42", 10*time.Minute)

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

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, run) || !strings.Contains(plain, "Ready for your review") || !strings.Contains(plain, "https://github.com/acme/app/pull/42") {
		t.Fatalf("plain output missing the finished run or the review section:\n%s", plain)
	}
}

// A successful run older than --keep's default (6h) is hidden from --plain,
// and shown again with --all (design generic-tool §10.3); --json never
// hides it. (The same rule for a failure's 24h FailedKeep is pinned at the
// unit level, TestFailedRunsStayUntilAcked: the queued scanner's own
// finishedLookback is also 24h, so a run old enough to need FailedKeep's
// hiding is never discovered by `fugaro watch` at all, successful or not.)
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
		t.Fatalf("a 10h-old success must be hidden by the default --keep (6h):\n%s", plain)
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

func TestWatchKeepFlagsValidate(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "watch", "--once", "--keep", "0"); err == nil || !strings.Contains(err.Error(), "--keep") {
		t.Fatalf("--keep 0: err = %v, want a usage error naming --keep", err)
	}
	if _, _, err := execute(t, "watch", "--once", "--keep-count", "0"); err == nil || !strings.Contains(err.Error(), "--keep-count") {
		t.Fatalf("--keep-count 0: err = %v, want a usage error naming --keep-count", err)
	}
}
