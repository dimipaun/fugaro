package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// bumpMaxParallel rewrites the fixture's local config to raise max_parallel
// above the fixture's default of 2 (cloud_test.go's newCloudFixture), for a
// batch that needs more headroom than any single-run test ever did.
func bumpMaxParallel(t *testing.T, n int) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "max_parallel: 2\n", fmt.Sprintf("max_parallel: %d\n", n), 1)
	if updated == string(data) {
		t.Fatal("max_parallel: 2 not found in the fixture's config")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRunAttemptsLaunchesNWithOneBatch is generic-tool plan Task 13's test:
// --attempts N launches N runs of the same task under one batch label, and
// refuses the flag combinations a best-of-N batch makes no sense with.
func TestRunAttemptsLaunchesNWithOneBatch(t *testing.T) {
	f := newCloudFixture(t)
	bumpMaxParallel(t, 5)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "3", "--json", "fix the flaky test")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Batch string `json:"batch"`
		Runs  []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil || len(doc.Runs) != 3 || !strings.HasPrefix(doc.Batch, "bestof-") {
		t.Fatalf("out = %s", out)
	}
	seen := map[string]bool{}
	for _, r := range doc.Runs {
		if r.RunID == "" || seen[r.RunID] {
			t.Fatalf("run ids: %v", doc.Runs)
		}
		seen[r.RunID] = true
	}
	if !strings.HasSuffix(doc.Batch, doc.Runs[0].RunID) {
		t.Fatalf("batch %q does not name the first run %q", doc.Batch, doc.Runs[0].RunID)
	}
	if n := len(f.run.Executions()); n != 3 {
		t.Fatalf("executions = %d, want 3", n)
	}
	for _, bad := range [][]string{
		{"--attempts", "1"},
		{"--attempts", "5"},
		{"--attempts", "2", "--pr", "4"},
		{"--attempts", "2", "--recipe", "review-only"},
	} {
		args := append([]string{"run", "--repo", "acme/app"}, bad...)
		args = append(args, "x")
		if _, _, err := execute(t, args...); ExitCode(err) != ExitUserError {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

// TestRunAttemptsNeedsRoomForAll pins the preflight cap check (design
// generic-tool G13, plan Task 13 step 1): with max_parallel: 2 (the
// fixture's default), --attempts 3 is refused before any launch, counting
// the whole batch against the running total, not one run at a time. A
// batch must never launch partially because of the cap.
func TestRunAttemptsNeedsRoomForAll(t *testing.T) {
	f := newCloudFixture(t) // max_parallel: 2
	_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "3", "task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "max_parallel") || !strings.Contains(err.Error(), "--attempts 3 needs 3") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("executions = %d, want 0: a refused batch must not launch any of it", n)
	}
}

// TestRunAttemptsRetry pins that --attempts and --retry are refused
// together: --retry replays a stored task as it is, which a fresh N-run
// batch contradicts.
func TestRunAttemptsRetry(t *testing.T) {
	newCloudFixture(t)
	bumpMaxParallel(t, 5)
	_, _, err := execute(t, "run", "--attempts", "2", "--retry", "20260927-100000-abcd")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
}

// TestRunAttemptsKeepsExplicitBatch pins that --batch, when given, names
// the batch instead of the default bestof-<first run id>.
func TestRunAttemptsKeepsExplicitBatch(t *testing.T) {
	f := newCloudFixture(t)
	bumpMaxParallel(t, 5)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "--batch", "nightly", "--json", "task")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Batch string `json:"batch"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil || doc.Batch != "nightly" {
		t.Fatalf("out = %s", out)
	}
	if n := len(f.run.Executions()); n != 2 {
		t.Fatalf("executions = %d, want 2", n)
	}
}
