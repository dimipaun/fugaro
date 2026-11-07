package cli

import (
	"strings"
	"testing"
)

// Check 111 — the billing picker's failure modes (setup-wizard plan W6.2, its
// section in the register) — is owner-run, NOT RUN, and it ships as ONE script:
// scripts/live-checks/check111.sh, with its own test
// scripts/live-checks/check111_test.sh driven by fake `gcloud` and `fugaro` on
// PATH and no network. No agent runs any step of it: an agent's session is one
// of the modes the check observes, so its guard refuses to start in one.
//
// The register's section is where the check's own text lives — the guard block,
// the seven runs, the assertion block and the Restore — and the script is that
// text made runnable. So the pin is threefold, the same shape Check 130's is:
// the section names the one command and still says the check has not been run,
// the register's row agrees and names the script too, and the script keeps the
// properties the section promises about it — the `belong` refusal, the block and
// its verdict line, the trap that undoes what three runs change even after a
// failed assertion, `--dry-run` calling nothing, and the single manual step.
//
// The script itself is executed by scripts/live_checks_test.go's
// TestLiveCheckScripts against those fakes; what is read here is its text, which
// is what a reader of the register is promised.
const (
	// check111Heading is docs_setup_wizard_checks_test.go's, which bounds the
	// same section for the pins on its blocks.
	check111Script   = "scripts/live-checks/check111.sh"
	check111Test     = "scripts/live-checks/check111_test.sh"
	check111RunLine  = "**Run it:** `bash scripts/live-checks/check111.sh --project <P>"
	check111Row      = "111"
	check111RowRun   = "`bash scripts/live-checks/check111.sh`"
	check111RowState = "**NOT RUN.**"
)

// TestDocsCheck111IsOneScriptTheOwnerRuns: the section names the one command,
// says no agent runs it and that it has not been run, and carries the block the
// owner pastes into the register — the row and the section moving together, or
// not at all.
func TestDocsCheck111IsOneScriptTheOwnerRuns(t *testing.T) {
	doc := readDoc(t, liveChecksDoc)
	section := registerSection(t, doc, check111Heading)
	// The section's own status, in its heading and in its first line: the
	// register's row carries the same word, and a check whose script exists but
	// has not been run is the one state both of them describe.
	heading, _, _ := strings.Cut(section, "\n")
	if !strings.Contains(heading, "NOT RUN") {
		t.Errorf("%s: the Check 111 heading does not carry NOT RUN: %q", liveChecksDoc, heading)
	}
	for _, want := range []string{
		"**NOT RUN.**",
		check111RunLine,
		"no agent runs it",
		check111Test,
		"CHECK 111 RESULT: PASS|FAIL",
		"`FACT` block",
		// The one step no script may run, and the login two of the modes cannot
		// share with the rest of the run.
		"One step no script may run for you",
		"`--second-adc`",
		// --dry-run is the first thing the owner runs, because the real run
		// disables an API in two throwaway projects and repoints their ADC.
		"`--dry-run` prints every command",
		"never `belong`",
		// What the owner does with the block, and what the script undoes.
		"**Paste back**",
		"**Restore.**",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s: the Check 111 section lacks %q", liveChecksDoc, want)
		}
	}

	row := registerRow(t, doc, check111Row)
	// The row's own account of the check names the script and its test, so a
	// reader of the register finds the one command without opening the section —
	// and the state it carries is the one the section's banner carries.
	for _, want := range []string{check111RowRun, check111Test, check111RowState, "never `belong`"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s: the | 111 | row lacks %q", liveChecksDoc, want)
		}
	}
}

// TestDocsCheck111ScriptKeepsItsPromises: the section's claims about the script
// are claims about this tree, so each one is read out of the script rather than
// trusted to the prose.
func TestDocsCheck111ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, check111Script)
	for _, want := range []string{
		// The refusal the standing rule requires, on all three projects the
		// check is pointed at: the belong installation and the two names it is
		// reached through.
		"belong | belong-* | fugaro-dev | edge-devel-dimi | fugaro-belong)",
		// The block, the verdict and the trap that undoes a run on every path,
		// Ctrl-C included.
		"BLOCK_KEYS=(",
		"CHECK 111 RESULT: %s",
		"trap finish EXIT",
		"trap on_signal INT TERM HUP",
		"remove_scratch",
		// The three writes no assertion may leave behind: the API two modes
		// disable, the ADC's quota project, and the login mode 2's runs use —
		// the last of it from the exit trap, whatever ended the run.
		`enable_api "$P"`,
		`enable_api "$Q"`,
		`quota_set "$ADC_QUOTA_ORIGINAL"`,
		"adc_swap_in",
		"adc_swap_out",
		// The copies of the two logins go with the scratch directory, so no copy
		// of a credential outlives the run — kept directory or not.
		`rm -f "${ADC_OWN_BACKUP:-}" "${SECOND_ADC_BACKUP:-}"`,
		// The one manual step, and the run's refusal to read a mode as a pass
		// while its premise is not there.
		"fact manual INFO",
		"not run: this mode's criteria were never read",
		// --dry-run, the trace refusal and the shell it is refused in.
		"--dry-run",
		"XTRACE_AT_START",
		"bash -x",
		// The guards a run may not pass with: they are what makes the check
		// answerable rather than dangerous.
		"GOOGLE_APPLICATION_CREDENTIALS",
		"XDG_CONFIG_HOME",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s lacks %q, which the register's section promises about it", check111Script, want)
		}
	}

	// Every mode's own row is in the block, so a pasted block answers for all
	// seven runs (and their two `--yes` reruns) rather than for the ones that
	// happened to pass. An unrun mode reads FAIL, which the block would not do
	// if its key were missing.
	keys := blockKeys(t, script)
	if len(keys) == 0 {
		t.Fatalf("%s: BLOCK_KEYS parsed empty", check111Script)
	}
	have := make(map[string]bool, len(keys))
	for _, k := range keys {
		have[k] = true
	}
	for _, want := range []string{"run-1a", "run-1b", "run-2a", "run-2b", "run-3", "run-4", "run-5", "second-adc", "restore", "scratch-removed", "manual"} {
		if !have[want] {
			t.Errorf("%s: BLOCK_KEYS has no %q row, and the pasted block has to answer for it", check111Script, want)
		}
	}

	// The three environment knobs are the script's own and are documented as
	// such: a fourth, added later, is how a knob that points the check at
	// another project or another installation gets in. The rest of the run comes
	// from the command line, and the flags are read by parse_args and nothing
	// else.
	var code []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	runnable := strings.Join(code, "\n")
	known := map[string]bool{"CHECK111_DRY_RUN": true, "CHECK111_RUN_SECONDS": true, "CHECK111_KEEP_SCRATCH": true}
	for _, name := range envNames(runnable) {
		if strings.HasPrefix(name, "CHECK111_") && !known[name] {
			t.Errorf("%s reads $%s: the only knobs this check carries are --dry-run, the per-run cap and keeping the scratch directory, and none of them can point it at another project or installation", check111Script, name)
		}
	}

	// The test the register names is the one the Go test runs: a script whose
	// test is not wired in is a script nothing checks.
	if got := readRepoFile(t, "scripts/live_checks_test.go"); !strings.Contains(got, `{"check111.sh", "check111_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not run %s", check111Test)
	}
}

// envNames returns every shell variable name the code expands, in order. Only
// the check's own `CHECK111_` family is read out of it, so a false positive —
// a name inside a string — is answered by naming the knob in the message.
func envNames(code string) []string {
	var out []string
	for i := 0; i < len(code); i++ {
		if code[i] != '$' {
			continue
		}
		j := i + 1
		if j < len(code) && code[j] == '{' {
			j++
		}
		start := j
		for j < len(code) && (code[j] == '_' || code[j] >= 'A' && code[j] <= 'Z' || code[j] >= '0' && code[j] <= '9') {
			j++
		}
		if j > start {
			out = append(out, code[start:j])
		}
		i = j - 1
	}
	return out
}
