package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// The fence's tests (T3). Two halves, and the seam between them is deliberate:
//
//   - what the fence decides for a command is TestEveryCommandIsFencedOrExempt
//     and the tests that drive the root hook (fenceHook) or the whole tree
//     (execute);
//   - what it does with the project it decided about is the rest, driven with a
//     stamp source that is a fake, so a test says what the node holds and reads
//     back which project the fence asked about — and no test needs a database,
//     a bucket or a real release.
//
// The one thing a fake source cannot show is that the CLI's own source reaches
// the right node, and that is internal/versionstamp's and T1's (source_test.go).

// fenceURL is the database the fence fixture's project names. Nothing connects
// to it — the source is a fake — so the string only has to be an address
// localcfg accepts.
const fenceURL = "https://fugaro-aurora-default-rtdb.firebaseio.com"

// fenceOtherURL is the same for the second project, so a test can tell which
// one the fence asked about.
const fenceOtherURL = "https://fugaro-borealis-default-rtdb.firebaseio.com"

// fenceVersion is the binary every fence test runs as: a released version, so
// the fence compares. A dev build skips it (spec, "Dev builds"), which
// TestDevBuildSkipsTheFence pins.
const fenceVersion = "0.29.0"

// fenceAbove is the stamp a test writes when it means "this project requires a
// newer CLI than the one running", and fenceBelow one it is above.
const (
	fenceAbove = "0.99.0"
	fenceBelow = "0.1.0"
)

// fenceRig is a fence test's world: the source the fence reads the stamp
// through is a fake, so the test decides what the node says and can read back
// which project the fence asked about.
type fenceRig struct {
	stamp *versionstamp.Stamp
	// readErr is a read that failed: what a Source answers with OriginNone and
	// its reason, which the fence fails open on (D3264).
	readErr error
	// sourceErr is an error the Source itself returns, which is the reader's
	// own for a stamp that is present and malformed (T1).
	sourceErr error
	queries   []versionstamp.Query
}

func newFenceRig(t *testing.T) *fenceRig {
	t.Helper()
	r := &fenceRig{}
	old := newStampSource
	newStampSource = func(versionstamp.Options) versionstamp.Source { return r }
	t.Cleanup(func() { newStampSource = old })
	return r
}

// above puts a stamp on the node requiring min, written by an update on a
// fixed day, and returns the rig.
func (r *fenceRig) above(min string) *fenceRig {
	r.stamp = &versionstamp.Stamp{Schema: versionstamp.SchemaVersion, Min: min,
		SetAt: time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC)}
	return r
}

// empty is a node that is not there: a project with no fence.
func (r *fenceRig) empty() *fenceRig {
	r.stamp = nil
	return r
}

// Required is the fake Source: it records the query and answers what the test
// said, as a completed read of the node, a failed read or the source's own
// error.
func (r *fenceRig) Required(_ context.Context, q versionstamp.Query) (versionstamp.Result, error) {
	r.queries = append(r.queries, q)
	switch {
	case r.sourceErr != nil:
		return versionstamp.Result{}, r.sourceErr
	case r.readErr != nil:
		return versionstamp.Result{Origin: versionstamp.OriginNone, Reason: r.readErr.Error()}, nil
	case r.stamp == nil:
		return versionstamp.Result{Origin: versionstamp.OriginDatabase}, nil
	}
	return versionstamp.Result{Found: true, Stamp: *r.stamp, Origin: versionstamp.OriginDatabase}, nil
}

// fenceCommandAt is the command path names, or nil where this build has no
// such command: cobra's Find stops at the closest command it knows and hands
// the rest back as arguments, so a path only resolves when the command it
// lands on says so — `fugaro coordinator serve` finds `coordinator` and the
// word `serve`.
func fenceCommandAt(root *cobra.Command, path string) *cobra.Command {
	c, _, err := root.Find(strings.Fields(strings.TrimPrefix(path, "fugaro ")))
	if err != nil || c == nil || c.CommandPath() != path {
		return nil
	}
	return c
}

// fenceEEOnly is the paths the two tables name that only a build with ee/
// registers. Importing ee/register here would run its init for this whole test
// binary, so this package can never see those commands (extend.go's stubs cover
// the paid *names*, not the ee command families) — and the tree walk below has
// to tell a path that is merely absent from this build from one that no build
// has, which is a typo in a table.
var fenceEEOnly = map[string]bool{
	"fugaro coordinator serve":   true,
	"fugaro coordinator status":  true,
	"fugaro coordinator show":    true,
	"fugaro coordinator intents": true,
	"fugaro orchestrator map":    true,
}

// fenceConfigFor writes project name's local config with a budget block naming
// url, and returns its path.
func fenceConfigFor(t *testing.T, name, gcp, url string) string {
	t.Helper()
	path := writeProject(t, name, gcp)
	setBudgetBlock(t, path, "budget: { rtdb_url: "+url+" }")
	return path
}

// fenceProjectConfig writes aurora's local config, names it in $FUGARO_CONFIG,
// and returns its path: a project with a database, which is what the fence
// reads a stamp from at all.
func fenceProjectConfig(t *testing.T, url string) string {
	t.Helper()
	isolateProjects(t, t.TempDir())
	t.Setenv("FUGARO_PROJECT", "")
	path := fenceConfigFor(t, "aurora", "proj-1234", url)
	t.Setenv("FUGARO_CONFIG", path)
	return path
}

// fenceHook runs the root's own pre-run for argv against the real tree and
// returns what it wrote to stderr. It is the hook without the command: every
// test about what the fence decides runs this rather than execute, so no test
// ever reaches a command that would converge an installation, launch a run or
// call a service — and the flags are parsed, so a table entry's predicate sees
// exactly what a person typed.
func fenceHook(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := cliRefRoot()
	var errOut bytes.Buffer
	root.SetOut(io.Discard)
	root.SetErr(&errOut)
	cmd, flags, err := root.Find(args)
	if err != nil {
		t.Fatalf("fugaro %s: %v", strings.Join(args, " "), err)
	}
	if err := cmd.ParseFlags(flags); err != nil {
		t.Fatalf("fugaro %s: %v", strings.Join(args, " "), err)
	}
	// ExecuteC's own line, and the only thing the hook needs of it: a command
	// reached outside Execute has no context, and the hook reads one.
	cmd.SetContext(context.Background())
	err = root.PersistentPreRunE(cmd, flags)
	// The hook runs before the buffer is read: errOut.String() in a return
	// list would be evaluated first, and the line the fence prints would be
	// read out of an empty buffer.
	return errOut.String(), err
}

// fenceRefusal asserts that err is the fence's refusal of a command, and
// returns the line main prints for it. The refusal is an error like every other
// (PrintFailure, exit code 1) and not output, so a command that refuses writes
// nothing to stdout — `--json` included.
func fenceRefusal(t *testing.T, out string, err error) string {
	t.Helper()
	var below *versionstamp.BelowError
	if !errors.As(err, &below) {
		t.Fatalf("err = %v, want the fence's *versionstamp.BelowError", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing: the refusal is an error, not output, and --json changes nothing", out)
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("exit code = %d, want %d", code, ExitUserError)
	}
	var errOut bytes.Buffer
	PrintFailure(&errOut, err)
	line := errOut.String()
	if !strings.HasPrefix(line, "fugaro: ") || !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Errorf("the printed refusal is %q, want one \"fugaro: ...\" line", line)
	}
	return line
}

// TestEveryCommandIsFencedOrExempt is the tree walk (R6): the fence hangs off
// the root's pre-run, no command replaces it, every exemption names a command
// this build has, and no console read is refused.
func TestEveryCommandIsFencedOrExempt(t *testing.T) {
	root := cliRefRoot()
	if root.PersistentPreRunE == nil {
		t.Fatal("the root defines no PersistentPreRunE, so no command is fenced")
	}
	if root.PersistentPreRun != nil {
		t.Error("the root defines a PersistentPreRun beside its PersistentPreRunE: cobra runs one of the two, and which one is not worth the doubt")
	}
	// cobra runs the closest ancestor's pre-run, so a command with one of its
	// own would run that instead of the root's — and every command below it
	// would be unfenced with nothing saying so.
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c != root && (c.PersistentPreRunE != nil || c.PersistentPreRun != nil) {
			t.Errorf("%s defines its own pre-run, which replaces the root's fence", c.CommandPath())
		}
		if v := fenceVerdictFor(c); v == fenceNone {
			t.Errorf("%s has no fence verdict: a command nobody classified must be fenced", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	// And a command added tomorrow is fenced: the table is the whole of the
	// exemption, so a path nobody wrote down gets the default rather than
	// being unfenced because nobody thought about it.
	stranger := &cobra.Command{Use: "brandnew"}
	parent := &cobra.Command{Use: "fugaro"}
	parent.AddCommand(stranger)
	if v := fenceVerdictFor(stranger); v != fenceFenced {
		t.Errorf("a command in no table is %v, want fenced: the fence is the default", v)
	}

	// The two tables name commands by path, and a path no command has is an
	// exemption that silently stopped applying — a renamed command would be
	// fenced again with nothing saying so. The exceptions are the paths only
	// ee/ registers (fenceEEOnly).
	for path := range fenceExemptions {
		if fenceCommandAt(root, path) == nil && !fenceEEOnly[path] {
			t.Errorf("fenceExemptions names %q, which this build has no command for", path)
		}
	}
	for path := range consoleReadPaths {
		c := fenceCommandAt(root, path)
		if c == nil {
			if !fenceEEOnly[path] {
				t.Errorf("consoleReadPaths names %q, which this build has no command for", path)
			}
			continue
		}
		if path == "fugaro apply" {
			// The one console read whose exemption is conditional: the read
			// arm passes and the bare word is fenced, which the check below
			// pins. Everything else here passes unconditionally.
			continue
		}
		// D3265: the console's reads are the one surface that may never be
		// refused, so no entry of the exemption table may fence one. The two
		// entries that answer for a console read (version, doctor) pass it as
		// their own exemptions.
		if v := fenceVerdictFor(c); v == fenceFenced {
			t.Errorf("%s is a console read and the fence refuses it: D3265 exempts every console read path", path)
		}
	}
	// R5: one exemption is conditional, and it is apply's read arm — and the
	// arm its predicate refuses is fenced, not handed the console-read
	// exemption by the table it also appears in.
	if fenceCommandAt(root, "fugaro apply") == nil {
		t.Fatal("this build has no `fugaro apply`: the one conditional exemption cannot be checked")
	}
	for _, tc := range []struct {
		flags []string
		want  fenceVerdict
	}{
		{nil, fenceFenced},
		{[]string{"--status"}, fenceConsoleRead},
	} {
		apply := fenceCommandAt(cliRefRoot(), "fugaro apply")
		if err := apply.ParseFlags(tc.flags); err != nil {
			t.Fatal(err)
		}
		if v := fenceVerdictFor(apply); v != tc.want {
			t.Errorf("`fugaro apply %s` is %v, want %v", strings.Join(tc.flags, " "), v, tc.want)
		}
	}
	for path, e := range fenceExemptions {
		if e.applies != nil && path != "fugaro apply" {
			t.Errorf("fenceExemptions[%q] carries a flag predicate; only apply's entry may (R5)", path)
		}
	}
}

// TestRunBudgetSetAndSettingsSetRefuseBelowTheStamp is the refusal end to end:
// a project requiring a newer CLI refuses the commands that act on it, with the
// spec's sentence, exit 1, nothing on stdout and `--json` included.
func TestRunBudgetSetAndSettingsSetRefuseBelowTheStamp(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)

	for _, args := range [][]string{
		{"run", "--json"},
		{"budget", "set"},
		{"settings", "set", "budget.mode", "off"},
		{"tell", "--json", "abcd1234", "hello"},
	} {
		rig.queries = nil
		out, _, err := execute(t, args...)
		line := fenceRefusal(t, out, err)
		if !strings.Contains(line, fenceAbove) || !strings.Contains(line, fenceVersion) {
			t.Errorf("fugaro %s: refusal = %q, want it to name %s and %s", strings.Join(args, " "), line, fenceAbove, fenceVersion)
		}
		if !strings.Contains(line, updatecheck.BrewCommand) {
			t.Errorf("fugaro %s: refusal = %q, want the fix %q", strings.Join(args, " "), line, updatecheck.BrewCommand)
		}
		if len(rig.queries) != 1 || rig.queries[0].URL != fenceURL {
			t.Errorf("fugaro %s: the fence asked %v, want one read of %s", strings.Join(args, " "), rig.queries, fenceURL)
		}
	}
}

// TestExemptCommandsPassBelowTheStamp drives the hook over every entry of the
// exemption table with the stamp above this binary: none is refused, and none
// of the silent ones even reads the node — an exemption is the fence not asking,
// not the fence asking and shrugging.
func TestExemptCommandsPassBelowTheStamp(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)

	for path, e := range fenceExemptions {
		if fenceCommandAt(cliRefRoot(), path) == nil {
			continue // a path only ee/ registers (fenceEEOnly)
		}
		args := strings.Fields(strings.TrimPrefix(path, "fugaro "))
		if e.applies != nil {
			// apply's read arm: the flag the entry's predicate reads.
			args = append(args, "--status")
		}
		rig.queries = nil
		errOut, err := fenceHook(t, args...)
		if err != nil {
			t.Errorf("fugaro %s below the stamp: %v, want it exempt", strings.Join(args, " "), err)
		}
		if e.line {
			// The console-read flavour: it passes with one line (D3265).
			if errOut == "" {
				t.Errorf("fugaro %s: no stderr line, want the one a console read prints", strings.Join(args, " "))
			}
			continue
		}
		if len(rig.queries) != 0 {
			t.Errorf("fugaro %s: the fence read the stamp %v, want an exempt command never asked", strings.Join(args, " "), rig.queries)
		}
		if errOut != "" {
			t.Errorf("fugaro %s: stderr = %q, want nothing", strings.Join(args, " "), errOut)
		}
	}
}

// TestBudgetKillPassesBelowTheStamp is D3270: stopping spend is the one write
// that must always work, so a member below the stamp can still stop a runaway
// run. The fence never asks about it, in every form it has — --workspace
// included, which writes another project's database.
func TestBudgetKillPassesBelowTheStamp(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)

	for _, args := range [][]string{
		{"budget", "kill", "--all"},
		{"budget", "kill", "--repo", "acme/app"},
		{"budget", "kill", "--workspace", "shop"},
	} {
		rig.queries = nil
		if _, err := fenceHook(t, args...); err != nil {
			t.Errorf("fugaro %s below the stamp: %v, want it exempt (D3270)", strings.Join(args, " "), err)
		}
		if len(rig.queries) != 0 {
			t.Errorf("fugaro %s: the fence read the stamp %v, want it never asked", strings.Join(args, " "), rig.queries)
		}
		// And the whole tree agrees: the command runs, and whatever it says
		// about the database, it is not the fence's refusal.
		out, _, err := execute(t, args...)
		var below *versionstamp.BelowError
		if errors.As(err, &below) {
			t.Errorf("fugaro %s: refused below the stamp, want D3270's exemption (stdout %q)", strings.Join(args, " "), out)
		}
	}
}

// TestConsoleReadsPrintOneStderrLineAndCleanStdout is D3265: a read the console
// spawns passes below the stamp, with one line on stderr and its answer
// untouched on stdout.
func TestConsoleReadsPrintOneStderrLineAndCleanStdout(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)

	// End to end, on a read that opens nothing and writes nothing: the JSON
	// document is still the whole of stdout, and the warning is beside it.
	out, errOut, err := execute(t, "config", "list", "--json")
	if err != nil {
		t.Fatalf("fugaro config list --json below the stamp: %v (stderr %q)", err, errOut)
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("stdout = %q, want the installations document: a console read's answer must survive the fence", out)
	}
	if len(rig.queries) != 1 {
		t.Errorf("the fence read the stamp %v, want one read", rig.queries)
	}
	if !isOneFenceNote(errOut) {
		t.Errorf("stderr = %q, want exactly one \"fugaro: note: ...\" line", errOut)
	}

	// And every console read path the tree has, through the hook: the line, and
	// no refusal.
	for path := range consoleReadPaths {
		if _, ok := fenceExemptions[path]; ok {
			continue // version and doctor answer for themselves
		}
		if fenceCommandAt(cliRefRoot(), path) == nil {
			continue // a path only ee/ registers (fenceEEOnly)
		}
		args := strings.Fields(strings.TrimPrefix(path, "fugaro "))
		if path == "fugaro apply" {
			args = append(args, "--status") // the read arm, R5
		}
		errOut, err := fenceHook(t, args...)
		if err != nil {
			t.Errorf("fugaro %s below the stamp: %v, want a console read to pass (D3265)", strings.Join(args, " "), err)
		}
		if !isOneFenceNote(errOut) {
			t.Errorf("fugaro %s: stderr = %q, want exactly one \"fugaro: note: ...\" line", strings.Join(args, " "), errOut)
		}
	}
}

// isOneFenceNote reports whether s is exactly one line, and a note.
func isOneFenceNote(s string) bool {
	return strings.HasPrefix(s, "fugaro: note: ") && strings.HasSuffix(s, "\n") && strings.Count(s, "\n") == 1
}

// TestBareApplyIsFencedButApplyStatusPasses is R5: apply's exemption is its
// read arm alone. `--status` changes nothing, so the console may spawn it; the
// bare word writes the steps a project owes, so a binary below the stamp may
// not — and `--status=false` is the bare word, which is why the entry is a
// predicate over the parsed flag and not a table of spellings.
func TestBareApplyIsFencedButApplyStatusPasses(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)

	for _, tc := range []struct {
		args   []string
		fenced bool
	}{
		{[]string{"apply", "--status"}, false},
		{[]string{"apply", "--status=true"}, false},
		{[]string{"apply", "--status=false"}, true},
		{[]string{"apply"}, true},
	} {
		rig.queries = nil
		errOut, err := fenceHook(t, tc.args...)
		if !tc.fenced {
			if err != nil {
				t.Errorf("fugaro %s: %v, want it to pass", strings.Join(tc.args, " "), err)
			}
			if !isOneFenceNote(errOut) {
				t.Errorf("fugaro %s: stderr = %q, want one line", strings.Join(tc.args, " "), errOut)
			}
			continue
		}
		var below *versionstamp.BelowError
		if !errors.As(err, &below) {
			t.Errorf("fugaro %s: err = %v, want the fence's refusal", strings.Join(tc.args, " "), err)
		}
	}
}

// TestFenceReadsTheCommandsOwnProjectFlag is R3: the fence resolves the project
// the command's own flags and environment select, not the ambient one — a
// person who aims a command at another project is fenced by that project's
// installation.
func TestFenceReadsTheCommandsOwnProjectFlag(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	isolateProjects(t, t.TempDir())
	t.Setenv("FUGARO_PROJECT", "")
	aurora := fenceConfigFor(t, "aurora", "proj-1234", fenceURL)
	borealis := fenceConfigFor(t, "borealis", "proj-5678", fenceOtherURL)
	t.Setenv("FUGARO_CONFIG", aurora)

	tell := func(extra ...string) []string {
		return append(append([]string{"tell"}, extra...), "abcd1234", "hello")
	}
	for _, tc := range []struct {
		name    string
		args    []string
		env     map[string]string
		wantURL string
	}{
		{name: "the environment", args: tell(), wantURL: fenceURL},
		{name: "--config", args: tell("--config", borealis), wantURL: fenceOtherURL},
		{name: "--project", args: tell("--project", "borealis"), wantURL: fenceOtherURL},
		{name: "FUGARO_PROJECT", args: tell(), env: map[string]string{"FUGARO_PROJECT": "borealis", "FUGARO_CONFIG": ""}, wantURL: fenceOtherURL},
		// --gcp-project names the GCP project, not the Fugaro project: it
		// changes no selection here, and it is read all the same, because the
		// command's own flags are what the fence resolves from.
		{name: "--gcp-project", args: tell("--gcp-project", "proj-1234"), wantURL: fenceURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			rig.queries = nil
			if _, err := fenceHook(t, tc.args...); err == nil {
				t.Fatal("the fence did not refuse: the stamp is above this binary")
			}
			if len(rig.queries) != 1 || rig.queries[0].URL != tc.wantURL {
				t.Fatalf("the fence asked %v, want one read of %s", rig.queries, tc.wantURL)
			}
		})
	}
}

// TestFenceReadsAProjectFromACheckout is R3's other half: a command inside a
// checkout is fenced by the project the checkout names, and the fence resolves
// that checkout without remembering it — the same rule the update check's own
// lookup follows (TestTheUpdateCheckLookupRemembersNothing).
func TestFenceReadsAProjectFromACheckout(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	dir := t.TempDir()
	isolateProjects(t, dir)
	fenceConfigFor(t, "aurora", "proj-1234", fenceURL)

	t.Chdir(gitCheckout(t, filepath.Join(dir, "app"), "version: 1\nproject: aurora\n"))
	state, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fenceHook(t, "tell", "abcd1234", "hello"); err == nil {
		t.Fatal("the fence did not refuse: the stamp is above this binary")
	}
	if len(rig.queries) != 1 || rig.queries[0].URL != fenceURL {
		t.Fatalf("the fence asked %v, want one read of %s", rig.queries, fenceURL)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the fence remembered the checkout it looked at (stat %s: %v)", state, err)
	}
}

// TestDevBuildSkipsTheFence is the spec's "Dev builds": a version that is not a
// release (the `dev` a build from source carries) has nothing to compare with,
// so it is not refused — and it does not even read the node, so a developer
// pays no cloud call and this machine writes no cache.
func TestDevBuildSkipsTheFence(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, "dev")
	fenceProjectConfig(t, fenceURL)

	for _, args := range [][]string{{"run"}, {"tell", "abcd1234", "hello"}, {"budget", "set"}} {
		rig.queries = nil
		errOut, err := fenceHook(t, args...)
		if err != nil {
			t.Errorf("fugaro %s on a dev build: %v, want the fence skipped", strings.Join(args, " "), err)
		}
		if len(rig.queries) != 0 {
			t.Errorf("fugaro %s on a dev build: the fence read the stamp %v, want nothing", strings.Join(args, " "), rig.queries)
		}
		if errOut != "" {
			t.Errorf("fugaro %s on a dev build: stderr = %q, want nothing", strings.Join(args, " "), errOut)
		}
	}
}

// TestNoUpdateCheckDoesNotDisableTheFence is A5: FUGARO_NO_UPDATE_CHECK=1
// silences the update check and nothing else. The fence is not a nag, and a
// binary below its project's required CLI refuses whether or not the person
// turned the nag off.
func TestNoUpdateCheckDoesNotDisableTheFence(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)
	t.Setenv(noUpdateCheckEnv, "1")

	out, _, err := execute(t, "tell", "--json", "abcd1234", "hello")
	fenceRefusal(t, out, err)
	if len(rig.queries) != 1 {
		t.Fatalf("the fence read the stamp %d times, want once", len(rig.queries))
	}
}

// TestOnPlatformPasses is R6's second gate: a process a backend claims is a job
// or a service acting for the installation, under an image the installation
// pins, so nothing a person's binary says about it applies. The stamp is above
// this binary and the command passes without a read.
func TestOnPlatformPasses(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)
	t.Setenv("CLOUD_RUN_EXECUTION", "fugaro-run-1-abc")

	errOut, err := fenceHook(t, "tell", "abcd1234", "hello")
	if err != nil {
		t.Fatalf("fugaro tell on a platform: %v, want it to pass", err)
	}
	if len(rig.queries) != 0 {
		t.Errorf("the fence read the stamp %v on a platform, want nothing", rig.queries)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
}

// TestTheFencePassesWithNoProjectOrNoDatabase: the two states a fence has
// nothing to say about. A machine with no project config has no installation to
// fence, and a project with no budget database has no node to read — neither is
// a refusal, and neither makes a cloud call.
func TestTheFencePassesWithNoProjectOrNoDatabase(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)

	t.Run("no project", func(t *testing.T) {
		isolateProjects(t, t.TempDir())
		t.Setenv("FUGARO_PROJECT", "")
		t.Setenv("FUGARO_CONFIG", "")
		rig.queries = nil
		if errOut, err := fenceHook(t, "tell", "abcd1234", "hello"); err != nil {
			t.Fatalf("err = %v, want a machine with no project to pass", err)
		} else if errOut != "" {
			t.Errorf("stderr = %q, want nothing", errOut)
		}
		if len(rig.queries) != 0 {
			t.Errorf("the fence read the stamp %v with no project selected, want nothing", rig.queries)
		}
	})

	t.Run("no database", func(t *testing.T) {
		isolateProjects(t, t.TempDir())
		t.Setenv("FUGARO_PROJECT", "")
		path := writeProject(t, "aurora", "proj-1234")
		t.Setenv("FUGARO_CONFIG", path)
		rig.queries = nil
		if errOut, err := fenceHook(t, "tell", "abcd1234", "hello"); err != nil {
			t.Fatalf("err = %v, want a project with no database to pass", err)
		} else if errOut != "" {
			t.Errorf("stderr = %q, want nothing", errOut)
		}
		if len(rig.queries) != 0 {
			t.Errorf("the fence read the stamp %v with no database to read, want nothing", rig.queries)
		}
	})
}

// TestTheFencePassesOnAnEmptyNodeAndOnAStampBelowTheBinary: the two reads that
// are answers rather than failures. A node that is not there is a project with
// no fence, and a stamp below the running binary is the ordinary state of an
// installation that has been updated by a newer fugaro.
func TestTheFencePassesOnAnEmptyNodeAndOnAStampBelowTheBinary(t *testing.T) {
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)

	for _, tc := range []struct {
		name string
		rig  func(*fenceRig) *fenceRig
	}{
		{"no node", func(r *fenceRig) *fenceRig { return r.empty() }},
		{"a stamp below this binary", func(r *fenceRig) *fenceRig { return r.above(fenceBelow) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newFenceRig(t)
			tc.rig(rig)
			errOut, err := fenceHook(t, "tell", "abcd1234", "hello")
			if err != nil {
				t.Fatalf("err = %v, want it to pass", err)
			}
			if errOut != "" {
				t.Errorf("stderr = %q, want nothing", errOut)
			}
			if len(rig.queries) != 1 {
				t.Errorf("the fence read the stamp %v, want one read", rig.queries)
			}
		})
	}
}

// TestTheFenceFailsOpenOnAReadFailure is D3264: the fence is not a security
// boundary, so an unreachable database, a timeout or a denied read lets the
// command go on — with the one stderr line that says the stamp could not be
// read, so the person is not left wondering.
func TestTheFenceFailsOpenOnAReadFailure(t *testing.T) {
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)

	rig := newFenceRig(t)
	rig.readErr = errors.New("dial tcp 127.0.0.1:1: connect: connection refused")

	errOut, err := fenceHook(t, "tell", "abcd1234", "hello")
	if err != nil {
		t.Fatalf("err = %v, want a read failure to fail open", err)
	}
	if !isOneFenceNote(errOut) || !strings.Contains(errOut, versionstamp.Path) {
		t.Errorf("stderr = %q, want one line naming %s", errOut, versionstamp.Path)
	}
}

// TestTheFenceRefusesOnAMalformedStamp is D3264's other half, and the one read
// failure that fails closed: a stamp that is present and unreadable was written
// by somebody, so a binary that cannot read it must not act on the project. The
// error is the reader's own, not the fence's sentence: it says what is wrong
// with the node and what repairs it.
func TestTheFenceRefusesOnAMalformedStamp(t *testing.T) {
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)

	rig := newFenceRig(t)
	rig.sourceErr = &versionstamp.MalformedError{Node: versionstamp.Path}

	out, _, err := execute(t, "tell", "--json", "abcd1234", "hello")
	var malformed *versionstamp.MalformedError
	if !errors.As(err, &malformed) {
		t.Fatalf("err = %v, want the reader's *versionstamp.MalformedError", err)
	}
	var below *versionstamp.BelowError
	if errors.As(err, &below) {
		t.Error("a malformed stamp was refused as a version below the stamp")
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
}
