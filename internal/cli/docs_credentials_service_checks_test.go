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

	// The appendix describes the FACT the script records, and Check 90's
	// `token-file` fact records the token file's MODE and says out loud that the
	// value and its length are not printed: a token's length is a hint about the
	// value. The appendix told the owner to paste "the file's length and mode
	// (wc -c, ls -l)" in three places, which asks for something no run of this
	// check ever prints -- a promise to paste that cannot be kept, next to the
	// token file, in the one document the owner reads while holding it.
	if got := readDoc(t, check90Steps); strings.Contains(got, "wc -c") {
		t.Errorf("%s: the appendix asks for the token file's length (wc -c), which no run of check90.sh records", check90Steps)
	}
	if got := readRepoFile(t, check90Script); !strings.Contains(got, "the value and its length are not printed anywhere") {
		t.Errorf("%s: the token-file fact no longer says the length is not printed, which is what the appendix is corrected against", check90Script)
	}

	// A Restore that ends with a count that is not 0 is finished BY HAND: the
	// register must not tell the owner to run the check again, which reads
	// nothing -- a fresh run mints a scratch directory of its own and stops in
	// the guards while the service or the repository is still in the project --
	// and the appendix must not repeat the promise either. The appendix is
	// pinned by its INTENT rather than by three spellings of the retired
	// promise: a fourth paraphrase ("the directory is kept ... because it is what
	// a re-run needs to know that the service and the repository are this
	// check's") sat in the appendix's FACT paragraph while all three strings
	// were absent, so what the pin has to say is that the Restore section
	// promises no re-run at all -- of the check, of the Restore, of anything.
	// The reason it gives for keeping the directory is the records and the hand
	// work done from them, which is what "hand work" is asserted for.
	sec91 := registerSection(t, doc, check91Heading)
	for _, bad := range []string{"re-run the Restore", "a re-run needs the records", "re-reads the records"} {
		if strings.Contains(sec91, bad) {
			t.Errorf("%s: the section starting %q says %q, which no run does", liveChecksDoc, check91Heading, bad)
		}
	}
	app := readDoc(t, check91Steps)
	const restoreHead = "### Check 91 step 7: Restore"
	i := strings.Index(app, restoreHead)
	if i < 0 {
		t.Fatalf("%s: the appendix has no %q section", check91Steps, restoreHead)
	}
	restoreSec := app[i:]
	if j := strings.Index(restoreSec, "\n### "); j >= 0 {
		restoreSec = restoreSec[:j]
	}
	if !strings.Contains(restoreSec, "hand work") {
		t.Errorf("%s: the Restore section does not say that finishing a count which is not 0 is hand work", check91Steps)
	}
	if strings.Contains(restoreSec, "re-run") {
		t.Errorf("%s: the Restore section promises a re-run; a run that ended with a count which is not 0 is finished by hand, and no re-run of the check or of its Restore can do it", check91Steps)
	}
	if want := "finishing a count that is not `0` is hand work"; !strings.Contains(sec91, want) {
		t.Errorf("%s: the section starting %q lacks %q", liveChecksDoc, check91Heading, want)
	}
	for _, c := range []struct{ path, want string }{
		{check91Steps, "Finishing this is hand work"},
		{check91Script, "does NOT take it over"},
	} {
		if got := readRepoFile(t, c.path); !strings.Contains(got, c.want) {
			t.Errorf("%s lacks %q, the correction of what a re-run can do", c.path, c.want)
		}
	}
	if got := readDoc(t, check91Steps); strings.Contains(got, "re-run this step: it re-reads") {
		t.Errorf("%s: the appendix still promises that re-running the step re-reads the kept records", check91Steps)
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

	// Row 90's own cell was double-edited into a bold run with a second bold run
	// nested inside it: `**settles … met; **does NOT ask you to paste … itself**
	// — and reaches U3 … outcome**`. The cell is read as the row's summary of what
	// the check settles, so the nesting renders as emphasis on a phrase that was
	// meant to be a peer of the first, and a claim repeated in it reads as two
	// claims. The parenthetical is ONE bold run — the marker count below is what
	// fails on that garble — and each claim is stated in it once, so that a later
	// edit can neither drop one nor state it twice.
	//
	// Mutation (verified, run and restored): put the inner `**` pair and the
	// duplicated "— and reaches U3 …" back — red on the marker count.
	cell := ""
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "| 90 | ") {
			cell = strings.SplitN(strings.TrimPrefix(l, "| 90 | "), "|", 2)[0]
			break
		}
	}
	if cell == "" {
		t.Fatalf("%s: row 90 has no cell to read", liveChecksDoc)
	}
	if got := strings.Count(cell, "**"); got != 2 {
		t.Errorf("%s: row 90's cell carries %d `**` markers; its parenthetical is one bold run, and a nested pair renders as bold inside bold", liveChecksDoc, got)
	}
	for _, want := range []string{
		"settles U1",
		"does NOT ask you to paste",
		"reaches U3 only if a usage limit can be observed, recording either outcome",
	} {
		if got := strings.Count(cell, want); got != 1 {
			t.Errorf("%s: row 90's cell says %q %d times; each claim is stated once", liveChecksDoc, want, got)
		}
	}

	// The paragraph that reads the block names the one PASS a reader would
	// otherwise take for a contradiction: a swap the upstream answered with a
	// RATE LIMIT (429) is tier A viable — the credential was accepted, since a
	// rate limit is not reachable without being authenticated — while `swap`
	// itself is INFO because the call did not complete. check90.sh's own suite
	// pins the script's half of this (test_limit_is_recorded); this is the half
	// the owner reads.
	//
	// Mutation (verified, run and restored): take the rate-limit sentence back
	// out of the paragraph — red here.
	sec90 := registerSection(t, doc, check90Heading)
	for _, want := range []string{"rate limit", "429", "authenticated"} {
		if !strings.Contains(sec90, want) {
			t.Errorf("%s: the section starting %q explains the block without %q, so a `tier-a: PASS` on a rate-limited swap reads as a contradiction", liveChecksDoc, check90Heading, want)
		}
	}
}

// TestCheck90ScriptKeepsItsPromises: the script the register points at is the
// one the section describes — the guards, the single hidden token prompt, the
// block, the cleanup, and the credentials-service facts the plan's Task C1.7
// is written against.
func TestCheck90ScriptKeepsItsPromises(t *testing.T) {
	text := readRepoFile(t, check90Script)
	// Strict mode WITH errtrace: -E is what makes the ERR trap below fire for a
	// failure inside a function or a command substitution too, which is what
	// turns an internal error into a distinct ABORTED fact rather than a run
	// that ends looking as if it had measured something.
	if want := "set -Eeuo pipefail"; !strings.Contains(text, want) {
		t.Errorf("%s lacks %q", check90Script, want)
	}
	for _, want := range []string{
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
	// The upstream TLS trust store, which is what a python.org macOS build
	// (an EMPTY store until its Install Certificates.command is run) turned
	// into a bare "502 ... upstream error" with nothing to read. The bundle is
	// chosen; verification is never turned off. The script's own suite proves
	// the choice and the refusal, in test_the_proxy_uses_the_first_ca_bundle_
	// that_loads and test_a_machine_with_no_ca_bundle_stops_before_the_first_
	// call; these are the structural halves.
	for _, want := range []string{
		"ssl.create_default_context()",
		"context=CTX",
		"Install Certificates.command",
		"SSL_CERT_FILE",
		`fact("ca-bundle", "FAIL"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q, one of the ways the proxy picks the trust store it verifies the upstream with", check90Script, want)
		}
	}
	for _, bad := range []string{
		"_create_unverified_context", "CERT_NONE", "check_hostname = False",
	} {
		if strings.Contains(text, bad) {
			t.Errorf("%s contains %q: the throwaway proxy must never turn certificate verification off, which would answer every call and measure a different endpoint", check90Script, bad)
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
	// ... and the four ways a token could still get out, each pinned by a test of
	// the script's own:
	//
	//   - a SECOND prompt. The run asks for the setup-token at one hidden prompt
	//     and for no other input at all: a prompt beside the token file invites a
	//     paste of `claude setup-token`'s own output, which carries validity
	//     beside the token, and whatever is typed there lands in the block and in
	//     the shell's history. The validity this check records is derived from the
	//     live control call instead.
	//   - a TRACE. `bash -x` prints the value the hidden read assigned, so a trace
	//     is refused and xtrace is turned off before the read as well.
	//   - an INHERITED ENVIRONMENT. The child that needs the token is the only one
	//     that gets it; the others are run with it unset.
	//   - the owner's own DIRECTORY and settings. `claude` is run from the scratch
	//     directory with hooks off and every MCP server refused, so nothing it
	//     starts inherits the token.
	for _, want := range []string{
		"drain_input",
		"XTRACE_AT_START",
		"{ set +x; } 2>/dev/null",
		// The whole line, not its halves: every part of it appears in the comment
		// above it as well, and a pin the comment satisfies stops pinning
		// anything. `--tools ""` removes the built-in tools (a tool call is a
		// process the client starts on its own, with the token in its
		// environment) and `--no-session-persistence` keeps the call's session
		// file out of the owner's store; both are in the installed client's own
		// --help, which the script's test asserts by rejecting any flag help
		// does not list.
		`exec claude --settings "{\"disableAllHooks\":true}" --strict-mcp-config --tools "" --no-session-persistence "$@"`,
		`cd "$CHK90_CWD" || exit 1`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q, one of the ways a token could still leave the run", check90Script, want)
		}
	}
	// Exactly ONE hidden read of the terminal: a second prompt is how a token
	// gets typed twice, and what is typed into the second one lands in a surface
	// that keeps it. (The drain above it reads with a one-second timeout and
	// throws away what it reads.)
	if got := strings.Count(text, "read -rs"); got != 1 {
		t.Errorf("%s has %d hidden reads of the terminal; the token is asked for exactly once", check90Script, got)
	}
}

// TestCheck90RegisterNamesEveryBlockKey: the register's "Reading the `CHECK 90
// RESULT` block" paragraph is what the owner diffs a pasted block against, so
// it names every key the block prints, and what the block prints is the
// script's own BLOCK_KEYS rather than a paraphrase of it. A key the paragraph
// leaves out reads as a key the block was not meant to print: `restore` is the
// one that says the token file and the scratch directory are gone, and Check
// 91's paragraph calls out its equivalent, so Check 90's omission read as
// deliberate rather than abbreviated. The paragraph, and not the whole section,
// is the check: `restore` is named again in the section's own `**Restore:**`
// sentence, which is about the directory rather than about the block's keys.
//
// Mutation (verified, run and restored): take `restore` back out of the
// paragraph's list — red on that key.
func TestCheck90RegisterNamesEveryBlockKey(t *testing.T) {
	keys := blockKeys(t, readRepoFile(t, check90Script))
	// A parse that reads nothing (or reads the comment above the array) would
	// make this test vacuous.
	if len(keys) < 10 {
		t.Fatalf("%s: BLOCK_KEYS parsed as %d keys (%v), which cannot be the block", check90Script, len(keys), keys)
	}
	sec := registerSection(t, readDoc(t, liveChecksDoc), check90Heading)
	const heading = "**Reading the `CHECK 90 RESULT` block:**"
	i := strings.Index(sec, heading)
	if i < 0 {
		t.Fatalf("%s: the Check 90 section has no %q paragraph", liveChecksDoc, heading)
	}
	para := sec[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	for _, k := range keys {
		if !strings.Contains(para, "`"+k+"`") {
			t.Errorf("the register's Check 90 %q paragraph does not name the block key `%s`, which check90.sh's BLOCK_KEYS prints", heading, k)
		}
	}
}

// blockKeys reads one check script's BLOCK_KEYS array: the keys, in the order
// the block prints them. `#` fields are comments inside the array, if any.
func blockKeys(t *testing.T, script string) []string {
	t.Helper()
	i := strings.Index(script, "BLOCK_KEYS=(")
	if i < 0 {
		t.Fatalf("the script has no BLOCK_KEYS= array")
	}
	rest := script[i+len("BLOCK_KEYS=("):]
	j := strings.Index(rest, ")")
	if j < 0 {
		t.Fatalf("BLOCK_KEYS=( is never closed")
	}
	var keys []string
	for _, f := range strings.Fields(rest[:j]) {
		if strings.HasPrefix(f, "#") {
			continue
		}
		keys = append(keys, f)
	}
	return keys
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
		// minted per request, never printed, and never on a command line either:
		// it goes into an 0600 curl config file the request names with --config,
		// because `ps` shows a process's argv to every user of the machine for as
		// long as the process lives.
		"CHECK 91 RESULT", "CHECK 91 RESULT END", "trap finish EXIT",
		"refusing to remove", "chk91-mine", "print-identity-token",
		"umask 077", `--config "$AUTH_CONF"`,
		// The build's own litter: the staged source is given a prefix of this
		// run's own and the Restore deletes exactly that prefix.
		"--gcs-source-staging-dir", `gcloud storage rm -r "$STAGING_PREFIX"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", check91Script, want)
		}
	}
	// The ownership records are written BEFORE the create they record. A run that
	// dies in between -- Ctrl-C, a dropped connection, a kill -- has still made a
	// repository or a service, and the record is what tells the Restore it is
	// this check's to delete; written after the create, that window leaves a
	// billed resource with nothing claiming it. (The script's own test watches a
	// fake gcloud that hangs mid-create to check the same thing at run time.)
	for _, pair := range []struct{ record, create string }{
		{`: >"$REPO_MINE"`, "artifacts repositories create"},
		{`: >"$SERVICE_MINE"`, "run deploy"},
	} {
		ri, ci := strings.Index(text, pair.record), strings.Index(text, pair.create)
		if ri < 0 || ci < 0 {
			t.Errorf("%s lacks %q or %q", check91Script, pair.record, pair.create)
			continue
		}
		if ri > ci {
			t.Errorf("%s writes %s AFTER %s: a run killed in between leaves the resource behind with nothing recording it as this check's", check91Script, pair.record, pair.create)
		}
	}
	// The project is never read from a gcloud config, and no other
	// installation is ever named: `belong` appears only where the script says
	// it is never used. The config question is asked of the script's CODE: the
	// phrase it looks for occurs in a comment, wrapped across two lines, so
	// searching the whole text answered "not there" for a reason that had
	// nothing to do with what the script runs -- a comment could be re-wrapped
	// into a match, and a real call in a comment's shadow would read the same.
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "gcloud config") {
			t.Errorf("%s runs a gcloud config command: %q; the project is hard-coded", check91Script, line)
		}
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
	// A checked index: strings.Index answers -1 for a renamed function, and
	// text[-1:] panics, which reports a renamed restore_run as a crash rather
	// than as the missing function it is.
	at := strings.Index(text, "restore_run() {")
	if at < 0 {
		t.Fatalf("%s has no restore_run(): what it deletes cannot be read", check91Script)
	}
	restore := text[at:]
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
	// Every delete sits INSIDE the guard that decides it -- after the guard's
	// opening line and before that guard's own closing `fi` -- not merely
	// somewhere below the guard's text. A test that only asked "does the guard
	// appear above the delete" stays green for a delete moved below its `fi`,
	// and that is exactly the shape that deletes what this run did not create:
	// the record the guard reads is the only thing that makes a resource this
	// check's.
	for _, c := range []struct{ guard, del string }{
		{`if [ -f "$SERVICE_MINE" ]; then`, "gcloud run services delete"},
		{`if [ "$BUILD_ATTEMPTED" = yes ]; then`, "gcloud artifacts docker images delete"},
		{`if [ -f "$REPO_MINE" ]; then`, "gcloud artifacts repositories delete"},
	} {
		if !insideGuard(restore, c.guard, c.del) {
			t.Errorf("%s: %q is not inside a %q guard, so a run that did not create that resource would delete it", check91Script, c.del, c.guard)
		}
	}
}

// insideGuard reports whether del appears between one of guard's opening lines
// and that guard's own closing `fi` -- the next `fi` at the two-space indent
// the guards in restore_run use. Every occurrence of guard is tried, so a guard
// text that legitimately appears more than once (BUILD_ATTEMPTED gates both the
// image delete and the staged-source removal) is not a failure as long as one
// of them holds del.
func insideGuard(body, guard, del string) bool {
	for off := 0; off < len(body); {
		i := strings.Index(body[off:], guard)
		if i < 0 {
			return false
		}
		block := body[off+i:]
		if j := strings.Index(block, "\n  fi\n"); j >= 0 {
			block = block[:j]
		}
		if strings.Contains(block, del) {
			return true
		}
		off += i + len(guard)
	}
	return false
}
