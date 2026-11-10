package scripts_test

// The hermetic half of Checks 480 and 481, the two owner-run live checks of
// docs/contract-first-decoupling/implementation-plan.md's CD5. They live under
// that directory rather than scripts/live-checks/ because the plan's Files line
// puts them there, so they are not in liveCheckScripts and nothing else in this
// repository runs them: without the tests below, a script that can never pass
// reads green everywhere.
//
// What each one drives is the script against a fake `fugaro` on PATH, in a
// repository built in a temp directory: no cloud, no batch, no run and no
// account is touched, and no step of either check's live half happens here.
//
// Two of the tests pin failures the scripts shipped with and no other test
// could see:
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
//
// The rest pin the refusals: Check 480 spends a run and pushes to a branch, so
// the guards that stop it before either — a traced shell, a checkout that is
// not the repository the plan describes, the owner's own installation, and
// contract text that reached the instructions — are as much a part of what it
// does as the rows it measures. The live-check family keeps the same cases in
// its own suites (scripts/live-checks/check510_test.sh's
// test_belong_is_refused_by_name and test_xtrace_refused), and a later edit
// that drops one of them has to fail here.

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

// check480Script and check481Script are the two checks under test.
var (
	check480Script = filepath.Join(testutil.ModuleRoot(), "docs", "contract-first-decoupling", "check-480.sh")
	check481Script = filepath.Join(testutil.ModuleRoot(), "docs", "contract-first-decoupling", "check-481.sh")
)

// contractPath is the contract file the fixture's graph names — the document
// the pass settles a shape in, which is the file a follow-up is told to conform
// to. The three bodies below are that file as the plan has it, as the amendment
// leaves it on the base, and with a sentence the check's own instructions also
// carry, which is what the instructions guard refuses: instructions are trusted
// text, so a contract's words in them are plan text the runner acts on as its
// own (D1812).
const (
	contractPath    = "docs/contracts/record.md"
	contractBefore  = "# The record\n\nThe record carries a name.\n"
	contractAfter   = "# The record\n\nThe record carries a name and a kind.\n"
	contractLeaking = "# The record\n\nthe code that uses the contract matches the contract as it is there\n"
)

// TestCheck480MeasuresItsOwnFollowUp drives the whole check: the plan read, the
// dependent read out of the listing, the amendment, the one follow-up, and the
// three rows measured on the branch afterwards. The fake's launch answer spells
// both `run` and `run_id` the way the CLI does — the qualified one and the bare
// one — because matching the wrong one is the bug this test exists for.
func TestCheck480MeasuresItsOwnFollowUp(t *testing.T) {
	requireTools(t, "bash", "git", "jq")

	f := buildCheck480Fixture(t, "gh/github.com/acme/sandbox.git", contractAfter, contractBefore)
	out, code := runContractCheck(t, f.sim, f.bin, check480Script, "--pr", "7", "--poll", "1", "--run-seconds", "30")

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
	if calls := f.calls(t); !strings.Contains(calls, "run --pr 7") {
		t.Errorf("check-480 never launched its follow-up; the calls it made were:\n%s", calls)
	}
}

// TestCheck480RefusesTheOwnersOwnInstallations: the check sends a real run at
// whatever repository it is pointed at, so the owner's own installation is
// refused by name rather than measured. The three arms of that case are the
// owner and the two repositories.
func TestCheck480RefusesTheOwnersOwnInstallations(t *testing.T) {
	requireTools(t, "bash", "git", "jq")

	for _, origin := range []string{
		"gh/github.com/belong/sandbox.git",
		"gh/github.com/acme/edgeweb.git",
		"gh/github.com/acme/edgeserver.git",
	} {
		t.Run(origin, func(t *testing.T) {
			f := buildCheck480Fixture(t, origin, contractAfter, contractBefore)
			out, code := runContractCheck(t, f.sim, f.bin, check480Script, "--pr", "7", "--poll", "1", "--run-seconds", "30")
			assertRefused(t, f, out, code, "names the owner's own installation")
		})
	}
}

// TestCheck480RefusesATracedShell: a trace prints the rows and the branch this
// check reads into the log the owner would then paste, so a shell started with
// -x is refused before anything is read.
func TestCheck480RefusesATracedShell(t *testing.T) {
	requireTools(t, "bash", "git", "jq")

	f := buildCheck480Fixture(t, "gh/github.com/acme/sandbox.git", contractAfter, contractBefore)
	out, code := runContractCheckTracing(t, f.sim, f.bin, check480Script, "--pr", "7", "--poll", "1", "--run-seconds", "30")
	assertRefused(t, f, out, code, "this shell is tracing (bash -x)")
	if !strings.Contains(out, "FACT xtrace: FAIL") {
		t.Errorf("check-480 did not record the xtrace row as FAIL:\n%s", out)
	}
}

// TestCheck480RefusesAForeignOrigin: the repository comes from the checkout's
// own origin, and one that names no github.com or bitbucket.org repository is
// refused rather than matched by guesswork.
func TestCheck480RefusesAForeignOrigin(t *testing.T) {
	requireTools(t, "bash", "git", "jq")

	f := buildCheck480Fixture(t, "not-a-forge/sandbox.git", contractAfter, contractBefore)
	out, code := runContractCheck(t, f.sim, f.bin, check480Script, "--pr", "7", "--poll", "1", "--run-seconds", "30")
	assertRefused(t, f, out, code, "names no github.com or bitbucket.org repository")
}

// TestCheck480RefusesContractTextInTheInstructions: the instructions name the
// contract file and never paste its text. The fixture's contract carries a line
// the instructions also use, so the guard has something to catch — and it has to
// catch it before the launch, which is the run the check spends.
func TestCheck480RefusesContractTextInTheInstructions(t *testing.T) {
	requireTools(t, "bash", "git", "jq")

	f := buildCheck480Fixture(t, "gh/github.com/acme/sandbox.git", contractLeaking, contractBefore)
	out, code := runContractCheck(t, f.sim, f.bin, check480Script, "--pr", "7", "--poll", "1", "--run-seconds", "30")

	if code != 2 {
		t.Errorf("check-480 exited %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "FACT instructions: FAIL") {
		t.Errorf("check-480 did not record the instructions row as FAIL:\n%s", out)
	}
	if !strings.Contains(out, "a line of the contract's own body is in the instructions this check would send") {
		t.Errorf("check-480 stopped for some other reason:\n%s", out)
	}
	// The stop is the whole point: the launch comes after this guard, so a run
	// that got this far would have been spent on instructions carrying the
	// contract's own text.
	if calls := f.calls(t); strings.Contains(calls, "run --pr 7") {
		t.Errorf("check-480 launched the follow-up with the contract's own text in the instructions:\n%s", calls)
	}
}

// TestCheck481MeasuresBothBatchesInOnePoll drives the two batches through one
// fake `fugaro`: they run at the same time and settle on the clock, so a loop
// that watched them one after the other would find the sequential batch already
// over and leave its wall clock unmeasured (exit 3). Both rows pass only if both
// are polled as they run, and the clocks differ, so neither figure can be the
// other's.
func TestCheck481MeasuresBothBatchesInOnePoll(t *testing.T) {
	requireTools(t, "bash", "jq")

	work := t.TempDir()
	graph := filepath.Join(work, "graph.json")
	writeFile(t, graph, contractCheck481Graph)
	bin := filepath.Join(work, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(bin, "fugaro"), fakeFugaro481(graph, work))

	out, code := runContractCheck(t, work, bin, check481Script, graph, "--poll", "1", "--wait", "30")

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

// assertRefused holds a refusal to its shape: exit 2, the reason in the block,
// and not one call to `fugaro` — a guard that stopped the check after reading
// the plan would still have been reached too late to be a guard.
func assertRefused(t *testing.T, f *check480Fixture, out string, code int, want string) {
	t.Helper()
	if code != 2 {
		t.Errorf("check-480 exited %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("check-480 did not refuse with %q:\n%s", want, out)
	}
	if calls := f.calls(t); calls != "" {
		t.Errorf("check-480 called fugaro before its refusal:\n%s", calls)
	}
}

// check480Fixture is one temp repository for Check 480: its origin, the checkout
// the check runs in, the graph the fake `fugaro` answers with, and the files the
// fake writes as it works.
type check480Fixture struct {
	work       string
	origin     string
	sim        string
	graph      string
	bin        string
	depHead    string
	newHead    string
	launched   string
	followUpID string
	callsFile  string
}

// buildCheck480Fixture builds the repository Check 480 measures. origin is the
// bare repository's path under the temp directory, which is what decides whether
// the check places it at all: one that does not name a forge, or that names the
// owner's own installation, is refused. baseContract is the contract file as the
// amendment leaves it on the base branch, branchContract as the dependent's
// branch has it.
func buildCheck480Fixture(t *testing.T, origin, baseContract, branchContract string) *check480Fixture {
	t.Helper()
	// The check and the fake `fugaro` it runs inherit this test's environment,
	// so the identity and the isolation have to be here rather than on this
	// test's own git commands: the fake commits into the temp repository too,
	// and without this it reads whatever identity the machine happens to have —
	// none, on a CI runner, where its commit then fails and the check measures a
	// follow-up that never pushed.
	testutil.IsolateGit(t)
	work := t.TempDir()
	f := &check480Fixture{
		work:       work,
		origin:     filepath.Join(work, filepath.FromSlash(origin)),
		sim:        filepath.Join(work, "sim"),
		graph:      filepath.Join(work, "graph.json"),
		bin:        filepath.Join(work, "bin"),
		newHead:    filepath.Join(work, "new-head"),
		launched:   filepath.Join(work, "launched"),
		followUpID: filepath.Join(work, "follow-up-id"),
		callsFile:  filepath.Join(work, "calls"),
	}
	if err := os.MkdirAll(filepath.Dir(f.origin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "init", "--quiet", "--bare", f.origin)

	// The dependent's branch, as a launched run leaves it: the contract as the
	// plan had it, a commit of the read, and its own pull request.
	writeFile(t, filepath.Join(f.sim, filepath.FromSlash(contractPath)), branchContract)
	writeFile(t, filepath.Join(f.sim, "internal", "cli", "read.go"), "package cli\n\nfunc Read() string { return \"\" }\n")
	gitRun(t, f.sim, "init", "--quiet", "-b", "main")
	gitRun(t, f.sim, "add", "-A")
	gitRun(t, f.sim, "commit", "--quiet", "-m", "the plan's work")
	gitRun(t, f.sim, "remote", "add", "origin", f.origin)
	gitRun(t, f.sim, "push", "--quiet", "origin", "main")
	gitRun(t, f.sim, "checkout", "--quiet", "-b", "fugaro/r1")
	writeFile(t, filepath.Join(f.sim, "internal", "cli", "read.go"), "package cli\n\nfunc Read() string { return \"dep\" }\n")
	gitRun(t, f.sim, "commit", "--quiet", "-am", "the dependent's own work")
	gitRun(t, f.sim, "push", "--quiet", "-u", "origin", "fugaro/r1")
	f.depHead = gitRun(t, f.sim, "rev-parse", "HEAD")

	// The amendment the owner made on the base: without it the check stops
	// before it spends a run, which is a different test.
	gitRun(t, f.sim, "checkout", "--quiet", "main")
	writeFile(t, filepath.Join(f.sim, filepath.FromSlash(contractPath)), baseContract)
	gitRun(t, f.sim, "commit", "--quiet", "-am", "amend the contract")
	gitRun(t, f.sim, "push", "--quiet", "origin", "main")

	writeFile(t, f.graph, contractCheck480Graph)
	writeExec(t, filepath.Join(f.bin, "fugaro"), fakeFugaro480(f))
	return f
}

// calls is every `fugaro` call the check made, one per line; empty when it made
// none.
func (f *check480Fixture) calls(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(f.callsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(body)
}

// fakeFugaro480 is a `fugaro` that answers the four calls Check 480 makes, and
// logs every call it takes so a refusal can be shown to have happened before
// any of them. The launch is the run's own work done for real against the temp
// repository: the check reads the branch back from origin, so a fake that only
// claimed a push would measure nothing.
func fakeFugaro480(f *check480Fixture) string {
	return strings.NewReplacer(
		"@SIM@", f.sim, "@GRAPH@", f.graph, "@FOLLOW_UP_ID@", f.followUpID,
		"@DEP_HEAD@", f.depHead, "@NEW_HEAD@", f.newHead, "@LAUNCHED@", f.launched,
		"@CALLS@", f.callsFile,
	).Replace(`#!/usr/bin/env bash
set -euo pipefail
SIM=@SIM@
printf '%s\n' "$*" >>@CALLS@
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
  # The CLI's own rule (task.ValidRunID), and it is checked before anything
  # else: a check that minted an id of another shape would get no run at all,
  # so a fake that took any id would let that ship as a green test.
  case "$run_id" in
  [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]-[0-9a-f][0-9a-f][0-9a-f][0-9a-f]) : ;;
  *)
    printf 'fugaro run: --run-id "%s" must look like 20260926-221530-a1b2\n' "$run_id" >&2
    exit 1
    ;;
  esac
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

// runContractCheck runs one script with the fake tools first on PATH and
// returns its merged output and exit status.
func runContractCheck(t *testing.T, dir, bin, script string, args ...string) (string, int) {
	t.Helper()
	return runBash(t, dir, bin, append([]string{script}, args...))
}

// runContractCheckTracing runs one script under `bash -x`, which is the traced
// shell the check's own stop message says a run must not be started from.
func runContractCheckTracing(t *testing.T, dir, bin, script string, args ...string) (string, int) {
	t.Helper()
	return runBash(t, dir, bin, append([]string{"-x", script}, args...))
}

func runBash(t *testing.T, dir, bin string, argv []string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), contractCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish within %s:\n%s", filepath.Base(argv[len(argv)-1]), contractCheckTimeout, out)
	}
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s: %v\n%s", filepath.Base(argv[len(argv)-1]), err, out)
		}
		code = exit.ExitCode()
	}
	return string(out), code
}

func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
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

// gitRun runs one git command in dir. The checkout is a temp directory, so the
// identity and the isolation come from the fixture's testutil.IsolateGit and
// not from here: the script and the fake it runs inherit the same environment,
// and a second identity set here would leave those two reading the machine's.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
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
     "footprint": {"prediction": true, "paths": ["docs/contracts/record.md", "internal/record/record_stub.go"]}},
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
