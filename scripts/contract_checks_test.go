package scripts_test

// The hermetic half of Checks 480 and 481, the two owner-run live checks of
// docs/contract-first-decoupling/implementation-plan.md's CD5. They live under
// that directory rather than scripts/live-checks/ because the plan's Files line
// puts them there, so they are not in liveCheckScripts and nothing else in this
// repository runs them: without the two tests below, a script that can never
// pass reads green everywhere.
//
// What each one drives is the script against a fake `fugaro` on PATH, in a
// repository built in a temp directory: no cloud, no batch, no run and no
// account is touched, and no step of either check's live half happens here.
// Both tests pin a failure the scripts shipped with and no other test could
// see:
//
//   - Check 480 read the run id it launched out of the launch JSON's `run`,
//     which is the qualified `slug/id`, and then matched the runs listing on
//     `run_id`, which is the bare id. Nothing matched, so the one follow-up the
//     check spends was polled for the whole timeout and the three rows after it
//     were never measured: the check could not pass.
//   - Check 481 watched the decoupled batch and then the sequential one, so a
//     sequential batch that settled while the loop waited on the decoupled one
//     read as "already over" and was left unmeasured — the faster shape, which
//     is exactly the reading the comparison row exists for.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// contractCheckTimeout bounds one script run. Both scripts poll at a second, so
// a passing run is over in seconds; the bound is what keeps a script that waits
// for a row it will never match (the 480 bug) from hanging the package instead
// of failing this test by name.
const contractCheckTimeout = 2 * time.Minute

// TestCheck480MeasuresItsOwnFollowUp drives the whole check: the plan read, the
// dependent read out of the listing, the amendment, the one follow-up, and the
// three rows measured on the branch afterwards. The fake's launch answer spells
// both `run` and `run_id` the way the CLI does — the qualified one and the bare
// one — because matching the wrong one is the bug this test exists for.
func TestCheck480MeasuresItsOwnFollowUp(t *testing.T) {
	for _, tool := range []string{"bash", "git", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}

	work := t.TempDir()
	// The origin has to name a github.com repository: the check refuses to
	// measure one it cannot place, and a bare temp path names none.
	origin := filepath.Join(work, "gh", "github.com", "acme", "sandbox.git")
	if err := os.MkdirAll(filepath.Dir(origin), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "init", "--quiet", "--bare", origin)

	sim := filepath.Join(work, "sim")
	if err := os.MkdirAll(filepath.Join(sim, "internal", "record"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sim, "internal", "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sim, "internal", "record", "record.go"), "package record\n\ntype R struct{ Name string }\n")
	writeFile(t, filepath.Join(sim, "internal", "cli", "read.go"), "package cli\n\nfunc Read() string { return \"\" }\n")
	gitRun(t, sim, "init", "--quiet", "-b", "main")
	gitRun(t, sim, "add", "-A")
	gitRun(t, sim, "commit", "--quiet", "-m", "the plan's work")
	gitRun(t, sim, "remote", "add", "origin", origin)
	gitRun(t, sim, "push", "--quiet", "origin", "main")

	// The dependent's branch, as a launched run leaves it: a commit of its own
	// on the read, and the contract file as the plan had it.
	gitRun(t, sim, "checkout", "--quiet", "-b", "fugaro/r1")
	writeFile(t, filepath.Join(sim, "internal", "cli", "read.go"), "package cli\n\nfunc Read() string { return \"dep\" }\n")
	gitRun(t, sim, "commit", "--quiet", "-am", "the dependent's own work")
	gitRun(t, sim, "push", "--quiet", "-u", "origin", "fugaro/r1")
	depHead := gitRun(t, sim, "rev-parse", "HEAD")

	// The amendment the owner made on the base: without it the check stops
	// before it spends a run, which is a different test.
	gitRun(t, sim, "checkout", "--quiet", "main")
	writeFile(t, filepath.Join(sim, "internal", "record", "record.go"), "package record\n\ntype R struct {\n\tName string\n\tKind string\n}\n")
	gitRun(t, sim, "commit", "--quiet", "-am", "amend the contract")
	gitRun(t, sim, "push", "--quiet", "origin", "main")

	graph := filepath.Join(work, "graph.json")
	writeFile(t, graph, contractCheck480Graph)
	newHead := filepath.Join(work, "new-head")
	bin := filepath.Join(work, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(bin, "fugaro"), fakeFugaro480(sim, origin, graph, depHead, newHead,
		filepath.Join(work, "launched"), filepath.Join(work, "follow-up-id")))

	out, code := runContractCheck(t, sim, bin, filepath.Join(testutil.ModuleRoot(), "docs", "contract-first-decoupling", "check-480.sh"),
		"--pr", "7", "--poll", "1", "--run-seconds", "30")

	// The row the bug killed first: without it the block ends with the polling
	// timeout and the three rows below it say "not reached".
	for _, want := range []string{
		"FACT run: PASS",
		"FACT base-merged: PASS",
		"FACT conformed: PASS",
		"FACT own-code: PASS",
		"CHECK 480 RESULT: PASS (exit 0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("check-480 did not report %q:\n%s", want, out)
		}
	}
	if code != 0 {
		t.Errorf("check-480 exited %d, want 0:\n%s", code, out)
	}
}

// TestCheck481MeasuresBothBatchesInOnePoll drives the two batches through one
// fake `fugaro`: the sequential batch settles at its second poll and the
// decoupled one at its third, so a loop that watched them one after the other
// would find the sequential batch already over and leave its wall clock
// unmeasured (exit 3). Both rows pass only if both are polled as they run, and
// the clocks differ, so neither figure can be the other's.
func TestCheck481MeasuresBothBatchesInOnePoll(t *testing.T) {
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}

	work := t.TempDir()
	graph := filepath.Join(work, "graph.json")
	writeFile(t, graph, contractCheck481Graph)
	bin := filepath.Join(work, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(bin, "fugaro"), fakeFugaro481(graph, work))

	out, code := runContractCheck(t, work, bin, filepath.Join(testutil.ModuleRoot(), "docs", "contract-first-decoupling", "check-481.sh"),
		graph, "--poll", "1", "--wait", "30")

	for _, want := range []string{
		"FACT decoupled-batch: PASS",
		"FACT sequential-batch: PASS",
		"CHECK 481 RESULT: PASS (exit 0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("check-481 did not report %q:\n%s", want, out)
		}
	}
	// Each span has to be its own: the sequential batch ended seconds before the
	// decoupled one, and a row that took the other's figure would read equal here.
	decoupled, sequential := batchWallClock(t, out, "decoupled-batch"), batchWallClock(t, out, "sequential-batch")
	if decoupled <= sequential {
		t.Errorf("check-481 took the decoupled batch's wall clock as %ds against the sequential batch's %ds, and the fake ends the sequential batch first:\n%s", decoupled, sequential, out)
	}
	if code != 0 {
		t.Errorf("check-481 exited %d, want 0:\n%s", code, out)
	}
}

// batchWallClock reads one batch row's observed span out of the block.
func batchWallClock(t *testing.T, out, key string) int {
	t.Helper()
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "FACT "+key+":") {
			line = l
		}
	}
	m := regexp.MustCompile(`wall clock 0h(\d+)m(\d+)s`).FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("the %s row carries no wall clock:\n%s", key, out)
	}
	minutes, _ := strconv.Atoi(m[1])
	seconds, _ := strconv.Atoi(m[2])
	return minutes*60 + seconds
}

// runContractCheck runs one script with the fake tools first on PATH and
// returns its merged output and exit status.
func runContractCheck(t *testing.T, dir, bin, script string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), contractCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish within %s:\n%s", filepath.Base(script), contractCheckTimeout, out)
	}
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s: %v\n%s", filepath.Base(script), err, out)
		}
		code = exit.ExitCode()
	}
	return string(out), code
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// gitRun runs one git command in dir with an identity of its own: the checkout
// is a temp directory, and a commit there must not read the machine's config.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=check", "GIT_AUTHOR_EMAIL=check@example.invalid",
		"GIT_COMMITTER_NAME=check", "GIT_COMMITTER_EMAIL=check@example.invalid",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// fakeFugaro480 is a `fugaro` that answers the four calls Check 480 makes. The
// launch is the run's own work done for real against the temp repository: the
// check reads the branch back from origin, so a fake that only claimed a push
// would measure nothing.
func fakeFugaro480(sim, origin, graph, depHead, newHead, launched, followUpID string) string {
	return strings.NewReplacer(
		"@SIM@", sim, "@ORIGIN@", origin, "@GRAPH@", graph, "@FOLLOW_UP_ID@", followUpID,
		"@DEP_HEAD@", depHead, "@NEW_HEAD@", newHead, "@LAUNCHED@", launched,
	).Replace(`#!/usr/bin/env bash
set -euo pipefail
SIM=@SIM@
case "${1:-} ${2:-}" in
"plan graph")
  cat @GRAPH@
  ;;
"runs ls")
  if [ -f @LAUNCHED@ ]; then
    printf '{"runs":[{"run_id":"%s","status":"succeeded","terminal":true,"branch":"fugaro/r1","pr":7,"pushed_head":"%s"},{"run_id":"dep-run","status":"succeeded","terminal":true,"branch":"fugaro/r1","pr":7,"pushed_head":"@DEP_HEAD@","base_branch":"main"}],"totals":{"total_usd":1.5}}\n' "$(cat @FOLLOW_UP_ID@)" "$(cat @NEW_HEAD@)"
  else
    printf '{"runs":[{"run_id":"dep-run","status":"succeeded","terminal":true,"branch":"fugaro/r1","pr":7,"pushed_head":"@DEP_HEAD@","base_branch":"main"}],"totals":{"total_usd":0.5}}\n'
  fi
  ;;
"run "*)
  run_id=""
  args=("$@")
  for i in "${!args[@]}"; do
    [ "${args[$i]}" = "--run-id" ] && run_id=${args[$((i+1))]}
  done
  git -C "$SIM" fetch --quiet origin main
  git -C "$SIM" checkout --quiet fugaro/r1
  git -C "$SIM" merge --quiet --no-edit origin/main >/dev/null
  printf 'package cli\n\nfunc Read() string { return "conformed" }\n' >"$SIM/internal/cli/read.go"
  git -C "$SIM" commit --quiet -am "conform to the amended contract"
  git -C "$SIM" push --quiet origin fugaro/r1
  git -C "$SIM" rev-parse HEAD >@NEW_HEAD@
  printf '%s\n' "$run_id" >@FOLLOW_UP_ID@
  touch @LAUNCHED@
  printf '{"run":"acme/sandbox/%s","run_id":"%s","branch":"fugaro/r1","pr":7,"previous_run":"dep-run","status":"launched"}\n' "$run_id" "$run_id"
  ;;
*)
  printf 'fake fugaro: unexpected: %s\n' "$*" >&2
  exit 1
  ;;
esac
`)
}

// fakeFugaro481 is a `fugaro` whose two batches run at the same time and settle
// on the clock, the way the batches a real operator launched do: the sequential
// batch ends two seconds after the first poll of either, the decoupled one five.
// Settling on the clock and not on a per-batch poll count is the whole point —
// a fake that counted its own polls would let Check 481 watch the two batches
// one after the other and still read the second as a live one, which is the
// failure this test exists for. The epoch is the first poll of either batch, so
// the first poll always sees both running whatever the script did before it.
//
// The listing carries the fraction of a second a real `started_at` has, so the
// check's own parse of it is exercised here too.
func fakeFugaro481(graph, work string) string {
	return strings.NewReplacer("@GRAPH@", graph, "@WORK@", work).Replace(`#!/usr/bin/env bash
set -euo pipefail
case "${1:-} ${2:-}" in
"plan graph")
  cat @GRAPH@
  ;;
"runs ls")
  name=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --batch) name=$2; shift 2 ;;
    *) shift ;;
    esac
  done
  epoch=@WORK@/epoch
  [ -f "$epoch" ] || date +%s >"$epoch"
  elapsed=$(( $(date +%s) - $(cat "$epoch") ))
  settle=5
  [ "$name" = check481-sequential ] && settle=2
  started=$(date -u +%Y-%m-%dT%H:%M:%S.123456789Z)
  if [ "$elapsed" -lt "$settle" ]; then
    printf '{"runs":[{"run_id":"r1","status":"running","terminal":false,"started_at":"%s"}],"totals":{"total_usd":0}}\n' "$started"
  else
    printf '{"runs":[{"run_id":"r1","status":"succeeded","terminal":true,"started_at":"%s"}],"totals":{"total_usd":3.5}}\n' "$started"
  fi
  ;;
*)
  printf 'fake fugaro: unexpected: %s\n' "$*" >&2
  exit 1
  ;;
esac
`)
}

// contractCheck480Graph is one plan with one broken edge: the contract waits for
// nothing, so the break is the one the pass writes, and the dependent node names
// the run whose pull request the follow-up continues.
const contractCheck480Graph = `{
  "schema_version": 3,
  "plan": "docs/plans/foo.md",
  "levels": 4,
  "nodes": [
    {"id": "T1", "title": "the parser", "size": 3, "footprint": {"prediction": true, "paths": ["internal/record/**"]}},
    {"id": "T2", "title": "the read", "run": "dep-run", "size": 2, "footprint": {"prediction": true, "paths": ["internal/cli/read.go"]}},
    {"id": "C1", "title": "the record's shape", "kind": "contract", "stubs": ["internal/record/record_stub.go"], "size": 0.5,
     "footprint": {"prediction": true, "paths": ["internal/record/record.go", "internal/record/record_stub.go"]}},
    {"id": "I1", "title": "wire the record in", "kind": "integration", "size": 0.5,
     "footprint": {"prediction": true, "paths": ["internal/record/record_stub.go"]}}
  ],
  "edges": [
    {"task": "T2", "needs": "C1", "reason": "the read is built against the record's shape"},
    {"task": "I1", "needs": "T1", "reason": "the integration runs both sides"},
    {"task": "I1", "needs": "T2", "reason": "the integration runs both sides"}
  ],
  "decoupling": [
    {"contract": "C1", "was": {"task": "T2", "needs": "T1"}, "integration": "I1",
     "reason": "T2 needed the parser's interface only, and not its behaviour"}
  ]
}
`

// contractCheck481Graph is the answer `fugaro plan graph` gives Check 481: the
// gain the read computed, with the weighted path after below the one before.
const contractCheck481Graph = `{
  "schema_version": 3,
  "plan": "docs/plans/x.md",
  "levels": 3,
  "nodes": [
    {"id": "T1", "footprint": {"prediction": true, "paths": ["a/**"]}},
    {"id": "C1", "kind": "contract", "stubs": ["a/stub.go"], "stubs_present": ["a/stub.go"],
     "footprint": {"prediction": true, "paths": ["a/stub.go", "a/iface.go"]}}
  ],
  "edges": [],
  "gain": {"broken": 1, "kept": 0, "added_tasks": 2, "before": {"tasks": 2, "weight": 9}, "after": {"tasks": 3, "weight": 5}, "improves": true}
}
`
