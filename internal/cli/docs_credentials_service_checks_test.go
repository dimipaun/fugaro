package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// The two live checks of the credentials service (plan Task C0.3, D151) are
// owner-run: no agent runs a step of either. They were written into the
// register as step-by-step text; they are now ONE script each that the owner
// runs, printing a block to paste back, and the register's sections say so.
// The step text is kept as an appendix, because it is what explains a result
// that is not PASS.
//
// These tests are the pin: the register must tell the owner to run the script
// and must not carry the steps as instructions again, the appendix must still
// hold them, and the scripts themselves must keep the properties the register
// promises about them — the hard-coded sandbox project, the guards, the token
// handling, the block, and the Restore.
const (
	check90Script   = "scripts/live-checks/check90.sh"
	check91Script   = "scripts/live-checks/check91.sh"
	check90Steps    = "docs/credentials-service/check90-steps.md"
	check91Steps    = "docs/credentials-service/check91-steps.md"
	liveChecksDoc   = "docs/gcp-live-checklist.md"
	check90RunLine  = "**Run it:** `bash scripts/live-checks/check90.sh`"
	check91RunLine  = "**Run it:** `bash scripts/live-checks/check91.sh`"
	check90Heading  = "## Check 90: the OAuth header swap — one script you run"
	check91Heading  = "## Check 91: Cloud Run streaming — one script you run"
	check90Appendix = "[credentials-service/check90-steps.md](credentials-service/check90-steps.md)"
	check91Appendix = "[credentials-service/check91-steps.md](credentials-service/check91-steps.md)"
)

// section returns the text of a register section, from its heading to the next
// top-level heading.
func registerSection(t *testing.T, doc, heading string) string {
	t.Helper()
	i := strings.Index(doc, heading)
	if i < 0 {
		t.Fatalf("%s has no section starting %q", liveChecksDoc, heading)
	}
	rest := doc[i:]
	if j := strings.Index(rest[1:], "\n## "); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(), path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestLiveChecksAreOneScriptTheOwnerRuns: each section names the script to run,
// says what it costs and how long it takes, how to read the block, where to
// paste it, and that the step text now lives in the appendix rather than in the
// instructions.
func TestLiveChecksAreOneScriptTheOwnerRuns(t *testing.T) {
	doc := readDoc(t, liveChecksDoc)
	for _, c := range []struct {
		heading, run, block, appendix, steps, path string
	}{
		{check90Heading, check90RunLine, "CHECK 90 RESULT", check90Appendix, "Check 90 step 1", check90Steps},
		{check91Heading, check91RunLine, "CHECK 91 RESULT", check91Appendix, "Check 91 step 1", check91Steps},
	} {
		sec := registerSection(t, doc, c.heading)
		for _, want := range []string{
			"**NOT RUN.**",
			c.run,
			c.block,
			c.appendix,
			"**Cost and duration:**",
			"**Paste back:**",
			"**Restore:**",
			"no agent runs any step of it",
		} {
			if !strings.Contains(sec, want) {
				t.Errorf("%s: the section starting %q lacks %q", liveChecksDoc, c.heading, want)
			}
		}
		// The steps are no longer the instructions: they are the appendix.
		if strings.Contains(sec, c.steps) {
			t.Errorf("%s: the section starting %q still carries the step text as instructions", liveChecksDoc, c.heading)
		}
		if got := readDoc(t, c.path); !strings.Contains(got, c.steps) {
			t.Errorf("%s: the appendix does not carry %q", c.appendix, c.steps)
		}
	}

	// The rows and the 90-99 paragraph name the scripts too, so an owner who
	// reads only the table is told the same thing.
	for _, want := range []string{
		"`scripts/live-checks/check90.sh` and `check91.sh`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", liveChecksDoc, want)
		}
	}
	for _, row := range []struct{ number, script string }{
		{"90", check90Script}, {"91", check91Script},
	} {
		line := ""
		for _, l := range strings.Split(doc, "\n") {
			if strings.HasPrefix(l, "| "+row.number+" | ") {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("%s has no row %s", liveChecksDoc, row.number)
		}
		if !strings.Contains(line, "`bash "+row.script+"`") {
			t.Errorf("%s: row %s does not tell the owner to run %s", liveChecksDoc, row.number, row.script)
		}
	}
}

// TestCheck90ScriptKeepsItsPromises: the script the register points at is the
// one the section describes — the guards, the single hidden token prompt, the
// block, the cleanup, and the credentials-service facts the plan's Task C1.7
// is written against.
func TestCheck90ScriptKeepsItsPromises(t *testing.T) {
	text := readRepoFile(t, check90Script)
	for _, want := range []string{
		"set -euo pipefail",
		// The guards: routing variables and settings files, names only.
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK",
		"settings.json", "apiKeyHelper",
		// The token: asked for once, hidden, never echoed, never on argv.
		"read -rs", "CLAUDE_CODE_OAUTH_TOKEN", "unset token",
		// The calls and the facts C1.7's header list is read from.
		"observed-control", "swap-credential-header", "headers-required",
		"headers-optional", "usage-limit", "timeout-retry", "tier-a",
		"Global Constraint 4",
		// The block and the cleanup.
		"CHECK 90 RESULT", "CHECK 90 RESULT END", "trap finish EXIT",
		"refusing to remove", "chk90-mine",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", check90Script, want)
		}
	}
	// The token reaches the hidden read, the 0600 file and the one child that
	// needs it, and nothing else. What proves the value never survives a run
	// is the script's own test, which feeds it a sentinel and greps every file
	// and every line of output for it; these are the structural halves.
	for _, want := range []string{
		"read -rs",
		"umask 077",
		"chmod 600",
		"unset token",
		`rm -f "$TOKEN_FILE"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q, which is what keeps the token off every surface but the file and the one child", check90Script, want)
		}
	}
}

// TestCheck91ScriptKeepsItsPromises: Check 91's script is the sandbox
// installation's and nobody else's, and it creates only what it deletes.
func TestCheck91ScriptKeepsItsPromises(t *testing.T) {
	text := readRepoFile(t, check91Script)
	for _, want := range []string{
		"set -euo pipefail",
		// The installation, hard-coded, and the doctor stop that proves it.
		"P=fugaro-dev", "L=us-east5",
		"fugaro doctor --project fugaro",
		"(GCP fugaro-dev,",
		// The three things it creates, and the ownership records that say they
		// are its own: a repository or service that was already there stops it.
		"fugaro-chk91", "repo-existed", "repo-mine", "service-mine",
		"never adopts, writes into or deletes",
		// The deploy the register names, flag for flag, and the unauthenticated
		// refusal: the sequence is the deploy command's own, not the fact
		// string's summary of it.
		"--no-allow-unauthenticated --timeout=3600 --min-instances=0 --max-instances=1",
		"unauthenticated-request",
		// U5's four numbers, and the half this check does NOT settle.
		"cold-start", "stream-unbuffered", "stream-longest",
		"body-limit-http1", "body-limit-http2-front-end", "http2-end-to-end",
		"NOT SETTLED", "h2c",
		// The Restore: three counts, all zero, and the directory kept when they
		// are not.
		"restore-counts", "must read 0", "scratch directory is KEPT",
		// The block, the cleanup on every path, and the identity token that is
		// minted per request and never printed.
		"CHECK 91 RESULT", "CHECK 91 RESULT END", "trap finish EXIT",
		"refusing to remove", "chk91-mine", "print-identity-token",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", check91Script, want)
		}
	}
	// The project is never read from a gcloud config, and no other
	// installation is ever named: `belong` appears only where the script says
	// it is never used.
	if strings.Contains(text, "gcloud config get-value project") {
		t.Errorf("%s reads the project from a gcloud config; it must be hard-coded", check91Script)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "belong") && !strings.Contains(strings.ToLower(line), "never belong") && !strings.Contains(line, "never `belong`") {
			t.Errorf("%s names another installation: %q", check91Script, line)
		}
	}
}

// TestCheck91ScriptNeverDeletesWhatItDidNotCreate: the Restore's deletes are
// gated on the records this check writes, so a run that created nothing
// deletes nothing, and a repository that was already there is never named by a
// delete. This is the property the register's "never adopts" paragraph states.
func TestCheck91ScriptNeverDeletesWhatItDidNotCreate(t *testing.T) {
	text := readRepoFile(t, check91Script)
	restore := text[strings.Index(text, "restore_run() {"):]
	if j := strings.Index(restore, "\ncleanup() {"); j >= 0 {
		restore = restore[:j]
	}
	for _, want := range []string{
		`[ ! -f "$SERVICE_MINE" ] && [ ! -f "$REPO_MINE" ]`,
		`if [ -f "$SERVICE_MINE" ]; then`,
		`if [ -f "$REPO_MINE" ]; then`,
		`[ "$mine" != no ]`,
	} {
		if !strings.Contains(restore, want) {
			t.Errorf("%s: the Restore lacks %q", check91Script, want)
		}
	}
}
