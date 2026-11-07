package cli

// family_report_test.go is CL7a: the accounts, secrets and creds commands that
// store, remove or clear a credential print one line per step through the
// reporter (D1354) with the closing line under them, keep the run's own output
// in a log filed under the command's own name (D1351, D1352), and leave every
// confirmation, diff and prompt exactly where it was (G1). What a person reads
// on the way to a credential is a step; what a person has to answer is not.

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/report"
)

// familyLogs points this test's state directory at one of its own, so the log
// a run keeps is the one this test reads and no other test's run is in the
// same directory: the log is filed under the state directory (D1351), and
// onlyLog insists on exactly one file per command name.
func familyLogs(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// familyStep is a run's account: ONE step line, the closing line under it, and
// whatever prose the run has to print anyway — a diff, a confirmation — kept
// where it was (G1). It answers the step line, which is the thing this file is
// about.
func familyStep(t *testing.T, account string) string {
	t.Helper()
	lines := reportLines(account)
	if len(lines) == 0 {
		t.Fatalf("the run printed nothing")
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Done in ") || !strings.HasSuffix(last, "step") && !strings.HasSuffix(last, "steps") {
		t.Errorf("the closing line is %q, want the reporter's summary:\n%s", last, account)
	}
	steps := make([]string, 0, 1)
	for _, l := range lines {
		if stepName(l) != "" {
			steps = append(steps, l)
		}
	}
	if len(steps) != 1 {
		t.Fatalf("the run printed %d step line(s), want one:\n%s", len(steps), account)
	}
	if !strings.HasPrefix(steps[0], markDone) {
		t.Fatalf("the run's step is not a tick, and the change it reports succeeded: %q", steps[0])
	}
	return steps[0]
}

// familyLog is G5's promise for every one of these runs: the log is mode 0600
// in a 0700 directory, so a run's own output is never readable by anyone else
// on the machine. It answers what the log holds.
func familyLog(t *testing.T, cmdName string) string {
	t.Helper()
	path, log := onlyLog(t, cmdName)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("%s is mode %v, want 0600", path, fi.Mode().Perm())
	}
	dir, err := os.Stat(logDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("%s is mode %v, want 0700", logDir(t), dir.Mode().Perm())
	}
	return log
}

// clearRig is a broken mark on a named account: the state `accounts clear`
// exists for, with stdin not a terminal so the run takes --yes and nothing
// else.
func clearRig(t *testing.T) *budgetFixture {
	t.Helper()
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "broken", "since": time.Now().UTC().Add(-time.Minute).Truncate(time.Second).Format(time.RFC3339Nano)})
	noTerminal(t)
	return f
}

func TestAccountsClearDefaultIsShort(t *testing.T) {
	familyLogs(t)
	clearRig(t)
	out, errOut, err := execute(t, "accounts", "clear", "acme-personal", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	step := familyStep(t, out)
	if !strings.Contains(step, "acme-personal") || !strings.Contains(step, "broken") {
		t.Errorf("the step does not name the account and the mark it cleared: %q\n%s", step, out)
	}
	// The state the clear left is the one fact of the run, so the step says it
	// rather than leaving it to the database.
	if !strings.Contains(step, "available") {
		t.Errorf("the step does not say what the mark is now: %q", step)
	}
}

func TestAccountsClearWritesALog(t *testing.T) {
	familyLogs(t)
	clearRig(t)
	if _, _, err := execute(t, "accounts", "clear", "acme-personal", "--yes"); err != nil {
		t.Fatal(err)
	}
	familyLog(t, "accounts-clear")
}

func TestSecretsSetDefaultIsShort(t *testing.T) {
	familyLogs(t)
	sm := secretsFixture(t)
	out, errOut, err := executeStdin(t, tokenValue+"\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	step := familyStep(t, out)
	if !strings.Contains(step, "claude-oauth-token") || !strings.Contains(step, "version 1") {
		t.Errorf("the step does not name the secret and the version the store wrote: %q\n%s", step, out)
	}
	// Nothing of the value, on either stream, and the store happened: the step
	// is a reading of the store's answer, not a replacement for it.
	if strings.Contains(out+errOut, tokenValue) {
		t.Errorf("a value leaked:\n%s%s", out, errOut)
	}
	if got := string(sm.Latest(claudeSecret)); got != tokenValue {
		t.Fatalf("stored %q", got)
	}
}

func TestSecretsSetWritesALog(t *testing.T) {
	familyLogs(t)
	secretsFixture(t)
	if _, _, err := executeStdin(t, tokenValue+"\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	}
	familyLog(t, "secrets-set")
}

func TestSecretsRmDefaultIsShort(t *testing.T) {
	familyLogs(t)
	sm, id, _ := secretsRmFixture(t)
	out, errOut, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	step := familyStep(t, out)
	if !strings.Contains(step, "project_secrets.bitbucket-token") || !strings.Contains(step, id) {
		t.Errorf("the step does not name the entry and the container the removal takes away: %q\n%s", step, out)
	}
	// G1: the diff and the confirmation are the run's own and are still printed
	// where they were — the change is confirmed before it is made, and the step
	// line is the outcome printed after it.
	confirm := "⚠ CONFIRM (project aurora): delete the Secret Manager container"
	lines := reportLines(out)
	if at, stepAt := lineAt(lines, confirm), lineAt(lines, step); at < 0 || at > stepAt {
		t.Errorf("the confirmation is not printed before the step it is confirmed for (%d, %d):\n%s", at, stepAt, out)
	}
	if !strings.Contains(out, "  - project_secrets:") {
		t.Errorf("the diff the person confirms is not printed:\n%s", out)
	}
	if got := sm.Deleted(); len(got) != 1 || got[0] != id {
		t.Fatalf("deleted %v, want %s", got, id)
	}
}

func TestSecretsRmWritesALog(t *testing.T) {
	familyLogs(t)
	secretsRmFixture(t)
	if _, _, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes"); err != nil {
		t.Fatal(err)
	}
	familyLog(t, "secrets-rm")
}

// TestSecretsRmFailureIsACrossAndALog: a removal whose delete fails is not a
// tick, whatever else the command printed (G3), and the run's account carries
// the cross, the error and the path of the log the rest of it is in (G8).
// This is the one path of the command that ends a step itself, so it is the
// one place the rules for a failure are this batch's to keep.
func TestSecretsRmFailureIsACrossAndALog(t *testing.T) {
	familyLogs(t)
	sm, id, path := secretsRmFixture(t)
	before := readFileString(t, path)
	sm.FailDeleteWith = http.StatusForbidden

	out, _, err := execute(t, "secrets", "rm", "bitbucket-token", "--yes")
	if err == nil {
		t.Fatalf("the delete failed, yet the removal succeeded:\n%s", out)
	}
	step := stepLineOf(reportLines(out), "Removing")
	if !strings.HasPrefix(step, markFailed+" Removing project_secrets.bitbucket-token") {
		t.Errorf("the failed removal is not a cross naming the entry:\n%s", out)
	}
	if !strings.Contains(step, "was put back") {
		t.Errorf("the step does not carry the error the command returns: %q", step)
	}
	log, _ := onlyLog(t, "secrets-rm")
	if !strings.Contains(out, "full output: "+log) {
		t.Errorf("the failure does not name the log the rest of it is in:\n%s", out)
	}
	if last := reportLines(out); !strings.HasPrefix(last[len(last)-1], "Failed in ") {
		t.Errorf("the run's account does not close on a failed outcome:\n%s", out)
	}
	// The failure is a failure and not a half-done removal: the value is where
	// it was and the config entry is back (secretsRmUndo).
	if got := sm.Latest(id); string(got) != tokenValue || readFileString(t, path) != before {
		t.Errorf("the failed removal changed something: value %q, config %s", got, readFileString(t, path))
	}
}

// lineAt is the first line of account that carries want, or -1.
func lineAt(account []string, want string) int {
	for i, l := range account {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

func TestCredsSetDefaultIsShort(t *testing.T) {
	familyLogs(t)
	f := credsFixtureFor(t)
	f.seed(t, nil)
	out, errOut, err := executeStdin(t, credsValue(t, "ours")+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	step := familyStep(t, out)
	if !strings.Contains(step, "version 1") || !strings.Contains(step, "first version") {
		t.Errorf("the step is not the write's own reading of the bundle: %q\n%s", step, out)
	}
	if strings.Contains(out+errOut, credsValue(t, "ours")) {
		t.Errorf("a value leaked:\n%s%s", out, errOut)
	}
}

func TestCredsSetWritesALog(t *testing.T) {
	familyLogs(t)
	f := credsFixtureFor(t)
	f.seed(t, nil)
	if _, _, err := executeStdin(t, credsValue(t, "ours")+"\n", "creds", "set", config.ClaudeOAuthToken); err != nil {
		t.Fatal(err)
	}
	familyLog(t, "creds-set")
}

// TestCredsSetFailureIsACrossAndALog: a write that may have landed is not
// abandoned. The run reached a result for its step — a version of the
// credential exists — so the account carries the cross, the error the command
// returns and the path of the log the rest of it is in (G3, G8). The lost
// update is the one that reads worst either way: the command set no step and
// printed no closing line, so a person whose write lost the race was told
// nothing until the error came back with no account of the run at all.
func TestCredsSetFailureIsACrossAndALog(t *testing.T) {
	familyLogs(t)
	f := credsFixtureFor(t)
	f.seed(t, nil)
	restore := credsWriteBack
	t.Cleanup(func() { credsWriteBack = restore })
	credsWriteBack = func(ctx context.Context, s *bundleStore) (bundleDoc, error) {
		f.AddVersion(f.id, []byte(config.AnthropicSecret+": "+credsValue(t, "other")+"\n"))
		return s.access(ctx, "latest")
	}

	out, _, err := executeStdin(t, credsValue(t, "ours")+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil {
		t.Fatalf("a lost concurrent update was not refused:\n%s", out)
	}
	step := stepLineOf(reportLines(out), "Writing")
	if !strings.HasPrefix(step, markFailed+" Writing ") {
		t.Errorf("the write that landed and lost the race is not a cross:\n%s", out)
	}
	if !strings.Contains(step, "not the document this command wrote") {
		t.Errorf("the step does not carry the error the command returns: %q", step)
	}
	log, _ := onlyLog(t, "creds-set")
	if !strings.Contains(out, "full output: "+log) {
		t.Errorf("the failure does not name the log the rest of it is in:\n%s", out)
	}
	if last := reportLines(out); !strings.HasPrefix(last[len(last)-1], "Failed in ") {
		t.Errorf("the run's account does not close on a failed outcome:\n%s", out)
	}
}

// noisyTerraform gives the rig's Terraform something to say: the progress
// output a converge used to show in full, and the reason a run of these
// commands keeps a log at all.
func noisyTerraform(t *testing.T, r *initRig) {
	t.Helper()
	r.script["plan"] = map[string]any{"exit": 2, "stdout": convergeNoise + "\n"}
	r.script["apply"] = map[string]any{"exit": 0, "stdout": convergeNoise + "\n"}
	r.save(t)
}

// familyStageLine is the stage's own line of the run's account, whatever
// marker it earned: a stage this rig skips is not a tick, and what these
// commands are about is that each stage has a line at all.
func familyStageLine(t *testing.T, account []string, stage string) string {
	t.Helper()
	if l := stepLineOf(account, stage); l != "" {
		return l
	}
	t.Fatalf("the run has no step line for the %s stage:\n%s", stage, strings.Join(account, "\n"))
	return ""
}

// TestAccountsAddDefaultIsShort: the command's own block is stdout and stays
// there — a person who pipes `fugaro accounts add` out gets the block — and the
// run's account of itself, one line per stage, is stderr, which is where the
// converge's own output already went. The stages used to be one block at the
// end behind a result banner; they print as each stage ends now.
func TestAccountsAddDefaultIsShort(t *testing.T) {
	familyLogs(t)
	r := newAddRig(t)
	noisyTerraform(t, r.initRig)
	out, errOut := r.add(t, "team-a", "--repos", "owner/repo-a")
	lines := reportLines(errOut)
	for _, stage := range []string{"policy", "installation", "repository"} {
		familyStageLine(t, lines, stage)
	}
	// Step 5's store is a step of the same account: the one line that names the
	// container the token went into, through the opener `fugaro secrets set`
	// uses, so a store that fails is a cross of this run rather than an error
	// with no account around it.
	store := stepLineOf(lines, "Storing")
	if want := config.ClaudeAccountSecret("team-a"); !strings.HasPrefix(store, markDone+" Storing ") || !strings.Contains(store, want) {
		t.Errorf("the store is not a tick naming the container it wrote to (%q):\nstderr:\n%s", store, errOut)
	}
	// The two stages that did something are ticks carrying what they did; the
	// third is skipped in this rig and says so rather than ticking.
	for _, stage := range []string{"policy", "installation"} {
		if l := stepLineOf(lines, stage); !strings.HasPrefix(l, markDone+" "+stage) {
			t.Errorf("the %s stage did not earn a tick: %q\nstderr:\n%s", stage, l, errOut)
		}
	}
	if l := stepLineOf(lines, "repository"); !strings.HasPrefix(l, markSkipped) {
		t.Errorf("a stage this rig skips is not marked as skipped: %q\nstderr:\n%s", l, errOut)
	}
	if strings.Contains(errOut, ": result") || strings.Contains(out, ": result") {
		t.Errorf("the run still prints the result banner the step lines replace:\nstderr:\n%s\nstdout:\n%s", errOut, out)
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Done in ") || !strings.HasSuffix(last, "steps") {
		t.Errorf("the run's account does not close on the reporter's summary: %q\nstderr:\n%s", last, errOut)
	}
	// The happenings the run still owes the person are not steps and are not
	// hidden by them (G1): the review screen, and the confirmation the run
	// answered with --yes.
	for _, want := range []string{"fugaro accounts add: project aurora", "[will do]", "CONFIRM"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the run's own reading of itself is gone with the account: %q is not on stderr:\n%s", want, errOut)
		}
	}
	// The block is still the whole of stdout: the answer and nothing a stage
	// said on the way there (F4).
	for _, l := range reportLines(out) {
		if stepName(l) != "" || strings.HasPrefix(l, "Done in ") {
			t.Errorf("a step line reached the command's stdout: %q\n%s", l, out)
		}
	}
	if !strings.Contains(out, "Nothing else to run") {
		t.Errorf("the command's stdout carries no answer:\n%s", out)
	}
}

func TestAccountsAddWritesALog(t *testing.T) {
	familyLogs(t)
	r := newAddRig(t)
	noisyTerraform(t, r.initRig)
	out, errOut := r.add(t, "team-a", "--repos", "owner/repo-a")
	log := familyLog(t, "accounts-add")
	if !strings.Contains(log, convergeNoise) {
		t.Errorf("the log does not hold the run's own output:\n%s", log)
	}
	if strings.Contains(out+errOut, convergeNoise) {
		t.Errorf("the default view streamed the run's own output:\n%s%s", out, errOut)
	}
}

// TestAccountsAddLogHoldsNoValueTheRunRead: the token is a value this run
// holds from the moment it reads it, so a child that echoes it back — the
// stages' own output is streamed into the log, which is what a redaction list
// is for — puts neither the value nor a line of it in the log, and the marker
// is there instead. The fixture's Terraform prints the token the person typed,
// which is the only place one of these runs can leak it: the default view
// prints no value of its own, and the log is the run's other reader.
func TestAccountsAddLogHoldsNoValueTheRunRead(t *testing.T) {
	familyLogs(t)
	r := newAddRig(t)
	// The echo a stage may make of what it was given, in the shape a reader of
	// the log would find it: on its own line, and inside one besides.
	echo := "credentials: " + addToken() + "\nand the line it came on\n"
	r.script["plan"] = map[string]any{"exit": 2, "stdout": echo + convergeNoise + "\n"}
	r.script["apply"] = map[string]any{"exit": 0, "stdout": echo + convergeNoise + "\n"}
	r.save(t)

	out, errOut := r.add(t, "team-a", "--repos", "owner/repo-a")
	log := familyLog(t, "accounts-add")
	if !strings.Contains(log, convergeNoise) {
		t.Fatalf("the run's own output is not in the log at all, so this test proves nothing:\n%s", log)
	}
	if strings.Contains(log, addToken()) {
		t.Errorf("the log holds the value the run read:\n%s", log)
	}
	if !strings.Contains(log, report.Redacted) {
		t.Errorf("the echo of the value is not in the log in any form:\n%s", log)
	}
	assertNoValue(t, out+errOut, addToken())
}

func TestAccountsRmDefaultIsShort(t *testing.T) {
	familyLogs(t)
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	out, errOut, err := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard", "--yes")
	if err != nil {
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	lines := reportLines(errOut)
	for _, stage := range []string{"policy", "installation", "repository"} {
		familyStageLine(t, lines, stage)
	}
	if strings.Contains(errOut, ": result") {
		t.Errorf("the run still prints the result banner the step lines replace:\nstderr:\n%s", errOut)
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Done in ") {
		t.Errorf("the run's account does not close on the reporter's summary: %q\nstderr:\n%s", last, errOut)
	}
	if !strings.Contains(out, "Nothing else to run") {
		t.Errorf("the command's stdout carries no answer:\n%s", out)
	}
}

func TestAccountsRmWritesALog(t *testing.T) {
	familyLogs(t)
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	noisyTerraform(t, r.initRig)
	if _, _, err := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard", "--yes"); err != nil {
		t.Fatal(err)
	}
	if log := familyLog(t, "accounts-rm"); !strings.Contains(log, convergeNoise) {
		t.Errorf("the log does not hold the run's own output:\n%s", log)
	}
}

// TestNoStepCommandPrintsAnsiOffATerminal is G4 for this family: a test's
// writers are buffers, so every one of these runs is off a terminal, and not
// one of them may print an escape sequence or a carriage return — neither the
// commands that print steps now nor the readers of the same files, which have
// no step to print and must not have gained a coloured one. A marker is a
// marker off a terminal, never a coloured one, and a spinner is not drawn
// where nobody is watching.
func TestNoStepCommandPrintsAnsiOffATerminal(t *testing.T) {
	// One rig per command: these fixtures select their local config through
	// FUGARO_CONFIG, so two of them cannot be alive in one test at once.
	runs := []struct {
		name string
		body func(t *testing.T) (string, string)
	}{
		{"accounts clear", func(t *testing.T) (string, string) {
			clearRig(t)
			out, errOut, err := execute(t, "accounts", "clear", "acme-personal", "--yes")
			if err != nil {
				t.Fatal(err)
			}
			return out, errOut
		}},
		{"accounts ls", func(t *testing.T) (string, string) {
			f := newBudgetFixture(t, "")
			f.appendConfig(t, claudeAccountsConfig)
			out, errOut, err := execute(t, "accounts", "ls", "--repo", "acme/app")
			if err != nil {
				t.Fatal(err)
			}
			return out, errOut
		}},
		{"secrets set", func(t *testing.T) (string, string) {
			secretsFixture(t)
			out, errOut, err := executeStdin(t, tokenValue+"\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app")
			if err != nil {
				t.Fatal(err)
			}
			return out, errOut
		}},
		{"secrets ls", func(t *testing.T) (string, string) {
			secretsFixture(t)
			out, errOut, err := execute(t, "secrets", "ls")
			if err != nil {
				t.Fatal(err)
			}
			return out, errOut
		}},
		{"creds set", func(t *testing.T) (string, string) {
			credsFixtureFor(t).seed(t, nil)
			out, errOut, err := executeStdin(t, credsValue(t, "ansi")+"\n", "creds", "set", config.ClaudeOAuthToken)
			if err != nil {
				t.Fatal(err)
			}
			return out, errOut
		}},
		{"creds ls", func(t *testing.T) (string, string) {
			f := credsFixtureFor(t)
			f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "listed")+"\n"))
			out, errOut, err := execute(t, "creds", "ls")
			if err != nil {
				t.Fatal(err)
			}
			return out, errOut
		}},
	}
	for _, tc := range runs {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut := tc.body(t)
			for _, stream := range []struct{ label, s string }{{"stdout", out}, {"stderr", errOut}} {
				if i := strings.IndexByte(stream.s, 0x1b); i >= 0 {
					t.Errorf("%s printed an escape sequence on %s at byte %d:\n%q", tc.name, stream.label, i, stream.s)
				}
				if strings.Contains(stream.s, "\r") {
					t.Errorf("%s printed a carriage return on %s:\n%q", tc.name, stream.label, stream.s)
				}
			}
		})
	}
}
