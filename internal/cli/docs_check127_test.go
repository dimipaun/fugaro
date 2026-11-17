package cli

import (
	"strings"
	"testing"
)

// Check 127 — the rigor modes on a real repository, the eleven live steps of
// docs/rigor-modes/implementation-plan.md §9, written by its task T8.1 and run
// by its T8.2 — is owner-run, NOT RUN, and it ships as ONE script:
// scripts/live-checks/check127.sh, with its own test
// scripts/live-checks/check127_test.sh driven by a fake `fugaro`, a fake `git`
// and a fake `gh` on PATH and by no network. No agent runs any step of it.
//
// The number is the owner's: the plan first took 119, the console-on-Windows
// work's claim on the same number is dated and the owner's, and the ruling of
// 2026-11-17 gives the rigor live check 127 — which is why this folder's pin
// reads 127 out of the register and out of the plan rather than out of the
// number the plan was written under.
//
// The register's section is where the check's text lives — the eleven steps and
// what makes each row pass — and the script is that text made runnable. So the
// pin is threefold: the section names the one script and still says the check
// has not been run, the register's row agrees, and the script keeps the
// properties the section promises about it: that it launches nothing, that the
// only write it makes is against a COPY of the backlog folder in its own
// scratch directory, the `belong` refusal, the block and its keys, and the exit
// trap that removes the scratch directory even after a failed assertion.
//
// The script itself is executed by scripts/live_checks_test.go's
// TestLiveCheckScripts against those three fakes; what is read here is its
// text, which is what a reader of the register is promised.
const (
	check127Script   = "scripts/live-checks/check127.sh"
	check127Test     = "scripts/live-checks/check127_test.sh"
	check127Heading  = "## Check 127: the rigor modes on a real repository"
	check127RunLine  = "bash scripts/live-checks/check127.sh --help"
	check127Row      = "127"
	check127RowRun   = "`bash scripts/live-checks/check127.sh --sandbox <checkout> --run <slug>/<run-id>`"
	check127RowState = "**OWNER-RUN, NOT RUN.**"
)

// TestDocsCheck127IsOneScriptTheOwnerRuns: the section tells the owner to run
// the script, and it says the check has not been run — the row's status and the
// section's own banner, which move together or not at all.
func TestDocsCheck127IsOneScriptTheOwnerRuns(t *testing.T) {
	doc := readDoc(t, liveChecksDoc)
	section := registerSection(t, doc, check127Heading)
	for _, want := range []string{
		"**NOT RUN.**",
		check127RunLine,
		// The standing rule for every live check, in the sentence the section
		// states it in: the steps are the owner's alone.
		"**No agent runs any step of it.**",
		check127Test,
		"CHECK 127 RESULT",
		"BLOCK_KEYS",
		"**What it costs and what it restores.**",
		"CHECK127_KEEP_SCRATCH=1",
		// The claim that makes this check readable in a container's tree: it
		// reads what exists and launches nothing, so a missing run is an INFO
		// row naming the command that would produce it rather than a FAIL.
		"**launches nothing**",
		"a run that was never asked for is not a finding about the",
		// Step 3's premise, stated rather than assumed: the floors it reads
		// about are T1.5's, and until that task lands a path no rule matches is
		// a FAIL about this tree and not about the modes.
		"`internal/rigor/floors.go`",
		"naming T1.5 as the work that would make",
		// Step 11's one write, and where it lands.
		"`fugaro backlog index --dir <the copy>` regenerates the index **there**",
		// The list's own refusal, in the section's words.
		"refuses every value that names `belong`",
		// What the check does not claim: U1 and U2 stay the plan's.
		"**U1**",
		"**U2**",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s: the Check 127 section lacks %q", liveChecksDoc, want)
		}
	}

	row := registerRow(t, doc, check127Row)
	for _, want := range []string{
		check127RowRun,
		check127Test,
		check127RowState,
		"never `belong`",
		// The plan and its two tasks, and the ruling that moved the number:
		// the row is where a reader finds out whose check this is.
		"[`docs/rigor-modes/implementation-plan.md`](rigor-modes/implementation-plan.md)",
		"**T8.1**",
		"**T8.2**",
		"2026-11-17",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("%s: the | 127 | row lacks %q", liveChecksDoc, want)
		}
	}
}

// TestDocsCheck127ScriptKeepsItsPromises: the section's claims about the script
// are claims about this tree, so each one is read out of the script rather than
// trusted to the prose.
func TestDocsCheck127ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, check127Script)
	for _, want := range []string{
		// The repository is read from the checkout's own origin, and the
		// second checkout's from its own: step 6's whole claim is that the two
		// repositories are read rather than named.
		`git -C "$SANDBOX" remote get-url origin`,
		`git -C "$CHECKOUT2" remote get-url origin`,
		// The refusal the standing rule requires, before any tool is called.
		"*belong*",
		// The reads the eleven steps are: each of them a document the CLI or
		// the provider already holds, and never a run of the check's own.
		"fugaro runs ls --json --since 0 --repo",
		`fugaro diagnose "$1" --json`,
		"fugaro config show --json",
		`fugaro security paths --check "$FLOOR_PATH" --json`,
		"fugaro budget preview --recipe",
		"fugaro watch --once",
		"fugaro backlog list --json",
		// Step 11's index is regenerated on a copy and nowhere else.
		"backlog index --dir",
		"gh pr view",
		"gh run list --branch",
		"gh api",
		// The block and its verdict, and the trap that cleans up on every path.
		"CHECK 127 RESULT",
		"CHECK 127 RESULT END (exit %s)",
		"trap finish EXIT",
		// The exit trap is reached for a Ctrl-C too.
		"trap on_signal INT TERM HUP",
		"remove_scratch",
		// --dry-run reaches nothing at all.
		"--dry-run",
		// The guards a run may not pass with: a traced shell, and an agent's
		// own session.
		"bash -x",
		"XTRACE_AT_START",
		"CLAUDECODE",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s lacks %q, which the register's section promises about it", check127Script, want)
		}
	}

	// The keys the block prints are the section's list: a reader diffing a
	// pasted block against the register must find the same rows.
	keys := blockKeys(t, script)
	if len(keys) == 0 {
		t.Fatalf("%s: BLOCK_KEYS parsed empty", check127Script)
	}
	for _, key := range []string{
		"installation", "sandbox", "belong", "agent-session", "xtrace",
		"poc-run", "diagnose", "critical-run", "mode-diff", "hotfix-run",
		"floor", "preview", "post-merge", "second-repo", "project-default",
		"ref-revision", "ref-branch", "areas", "areas-round", "watch",
		"item-pr", "item-branches", "item-branches-index", "restore",
	} {
		if !contains(keys, key) {
			t.Errorf("%s: BLOCK_KEYS does not print the %q row the section's steps rest on", check127Script, key)
		}
	}

	// The repository comes from the checkout and from nowhere else, and no
	// knob can point this check at another project, another branch or another
	// host. The two variables it does read are bounded: a timeout, and the
	// switch that keeps the scratch directory.
	var code []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	runnable := strings.Join(code, "\n")
	for _, banned := range []string{"gcloud", "gsutil", "--project"} {
		if strings.Contains(runnable, banned) {
			t.Errorf("%s runs a line naming %q: this check touches no cloud project, no bucket and no installation but the owner's own", check127Script, banned)
		}
	}
	for _, knob := range []string{"CHECK127_TIMEOUT", "CHECK127_KEEP_SCRATCH"} {
		if !strings.Contains(runnable, knob) {
			t.Errorf("%s does not read %s, which the section names as the only knob it has", check127Script, knob)
		}
	}
	// `belong` is not in this list: the lines of code that name it are the
	// refusal, asserted above.
	if strings.Contains(runnable, "CHECK127_REPO") || strings.Contains(runnable, "CHECK127_BRANCH") ||
		strings.Contains(runnable, "CHECK127_HOST") || strings.Contains(runnable, "CHECK127_PROJECT") {
		t.Errorf("%s carries a knob that can point the check at another repository, branch, host or project; the section says the checkout's own origin is the only source", check127Script)
	}

	// The test the register names is the one the Go test runs: a script whose
	// test is not wired in is a script nothing checks.
	if got := readRepoFile(t, "scripts/live_checks_test.go"); !strings.Contains(got, `{"check127.sh", "check127_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not run %s", check127Test)
	}
}
