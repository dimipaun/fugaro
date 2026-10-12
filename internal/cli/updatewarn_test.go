package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// fakeHTTPClient adapts a function to updatecheck.Client, for tests that
// need to control exactly when a "request" returns.
type fakeHTTPClient func(*http.Request) (*http.Response, error)

func (f fakeHTTPClient) Do(req *http.Request) (*http.Response, error) { return f(req) }

// cmdNamed is a bare cobra command with the given name, a "json" and
// "plain" flag (as real commands that have them declare), and a buffer
// for stderr: enough for the update-check decision functions, which only
// ever look at Name(), Flags() and ErrOrStderr().
func cmdNamed(name string) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: name}
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("plain", false, "")
	cmd.Flags().Bool("help", false, "")
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	return cmd, &errBuf
}

// TestShouldStartUpdateCheckExclusions pins every reason the background
// check must never fire, each checked in isolation. The baseline ("ordinary
// command") uses "ls", not "doctor": doctor is itself one of the
// exclusions (it runs its own synchronous Refresh as part of cli-version),
// pinned separately below.
//
// Mutation: dropping any one of shouldStartUpdateCheck's conditions (for
// example `&& !backend.OnCloudRun(...)`) makes exactly that case in this
// table wrongly true.
func TestShouldStartUpdateCheckExclusions(t *testing.T) {
	withVersion(t, "0.6.1")
	oldTTY := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = oldTTY })
	cases := []struct {
		name     string
		cmdName  string
		version  string
		cloudRun string
		ci       string
		tty      bool
		disabled bool
		want     bool
	}{
		{"ordinary command", "ls", "0.6.1", "", "", true, false, true},
		{"exec (the runner)", "exec", "0.6.1", "", "", true, false, false},
		{"completion", "completion", "0.6.1", "", "", true, false, false},
		{"help", "help", "0.6.1", "", "", true, false, false},
		{"version", "version", "0.6.1", "", "", true, false, false},
		{"doctor (its own synchronous Refresh)", "doctor", "0.6.1", "", "", true, false, false},
		{"dev build", "ls", "dev", "", "", true, false, false},
		{"unparseable build", "ls", "0.6.1-rc1+local", "", "", true, false, false},
		{"inside Cloud Run", "ls", "0.6.1", "exec-1", "", true, false, false},
		{"CI", "ls", "0.6.1", "", "true", true, false, false},
		{"not a terminal", "ls", "0.6.1", "", "", false, false, false},
		{"disabled", "ls", "0.6.1", "", "", true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			Version = c.version
			t.Setenv("CLOUD_RUN_EXECUTION", c.cloudRun)
			t.Setenv(ciEnv, c.ci)
			stderrIsTerminal = func(io.Writer) bool { return c.tty }
			cmd, _ := cmdNamed(c.cmdName)
			if got := shouldStartUpdateCheck(cmd, c.disabled); got != c.want {
				t.Errorf("shouldStartUpdateCheck = %v, want %v", got, c.want)
			}
		})
	}
}

// TestShouldWarnUpdateAvailableExclusions pins every reason the nag must
// stay silent, independent of whether there is actually a newer release
// cached (that part is updatecheck.Warn's own job, tested in its package).
//
// Mutation: dropping the TTY check (`&& stderrIsTerminal(...)`) makes the
// "not a terminal" case wrongly true; dropping the json/plain check makes
// the "--json" and "--plain" cases wrongly true.
func TestShouldWarnUpdateAvailableExclusions(t *testing.T) {
	t.Setenv("CLOUD_RUN_EXECUTION", "")
	t.Setenv(noUpdateCheckEnv, "")
	oldTTY := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = oldTTY })

	cases := []struct {
		name     string
		cmdName  string
		tty      bool
		json     bool
		plain    bool
		help     bool
		cloudRun bool
		env      string
		disabled bool
		want     bool
	}{
		{"ordinary, a terminal", "doctor", true, false, false, false, false, "", false, true},
		{"not a terminal", "doctor", false, false, false, false, false, "", false, false},
		{"--json", "doctor", true, true, false, false, false, "", false, false},
		{"--plain", "ls", true, false, true, false, false, "", false, false},
		{"--help", "doctor", true, false, false, true, false, "", false, false},
		{"excluded command", "version", true, false, false, false, false, "", false, false},
		{"inside Cloud Run", "doctor", true, false, false, false, true, "", false, false},
		{"disabled", "doctor", true, false, false, false, false, "", true, false},
		// disabled is explicitly false here: only the direct os.Getenv
		// re-check inside shouldWarnUpdateAvailable itself can be making
		// this silent, independent of whatever the caller resolved.
		{"env set, caller says not disabled", "doctor", true, false, false, false, false, "1", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stderrIsTerminal = func(io.Writer) bool { return c.tty }
			if c.cloudRun {
				t.Setenv("CLOUD_RUN_EXECUTION", "exec-1")
			} else {
				t.Setenv("CLOUD_RUN_EXECUTION", "")
			}
			t.Setenv(noUpdateCheckEnv, c.env)
			cmd, _ := cmdNamed(c.cmdName)
			if c.json {
				_ = cmd.Flags().Set("json", "true")
			}
			if c.plain {
				_ = cmd.Flags().Set("plain", "true")
			}
			if c.help {
				_ = cmd.Flags().Set("help", "true")
			}
			if got := shouldWarnUpdateAvailable(cmd, c.disabled); got != c.want {
				t.Errorf("shouldWarnUpdateAvailable = %v, want %v", got, c.want)
			}
		})
	}
}

// TestWarnUpdateAvailablePrintsTheLine is the end-to-end wiring: a cached
// newer release, a terminal stderr, prints exactly updatecheck.Warn's line
// and nothing else.
func TestWarnUpdateAvailablePrintsTheLine(t *testing.T) {
	withVersion(t, "0.6.1")
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("CLOUD_RUN_EXECUTION", "")
	oldTTY := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })
	if err := updatecheck.Save(os.Getenv, updatecheck.Entry{Latest: "0.7.0"}); err != nil {
		t.Fatal(err)
	}
	cmd, errBuf := cmdNamed("doctor")
	warnUpdateAvailable(cmd, false)
	want := "fugaro 0.7.0 is available (you have 0.6.1): " + updatecheck.BrewCommand + "\n"
	if got := errBuf.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

// TestUpdateCheckDisabledByEnv pins FUGARO_NO_UPDATE_CHECK=1.
func TestUpdateCheckDisabledByEnv(t *testing.T) {
	t.Setenv(noUpdateCheckEnv, "1")
	cmd, _ := cmdNamed("doctor")
	cmd.SetContext(context.Background())
	if !updateCheckDisabled(cmd) {
		t.Fatal("updateCheckDisabled with FUGARO_NO_UPDATE_CHECK=1 = false, want true")
	}
}

// TestUpdateCheckNotDisabledByEnvValue pins the exact value FUGARO_NO_UPDATE_CHECK
// must be: "1", not any other truthy-looking string.
func TestUpdateCheckNotDisabledByEnvValue(t *testing.T) {
	for _, v := range []string{"", "0", "true", "yes"} {
		t.Setenv(noUpdateCheckEnv, v)
		cmd, _ := cmdNamed("doctor")
		cmd.SetContext(context.Background())
		if got := updateCheckDisabled(cmd); got {
			t.Errorf("updateCheckDisabled with %s=%q = true, want false", noUpdateCheckEnv, v)
		}
	}
}

// TestUpdateCheckDisabledByLocalConfig pins update_check: false in the
// selected project's local config (no checkout: "only project config"
// selects it), independent of the env switch.
func TestUpdateCheckDisabledByLocalConfig(t *testing.T) {
	t.Setenv(noUpdateCheckEnv, "")
	dir := t.TempDir()
	path := isolateProjects(t, dir)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"user: someone@example.com\nupdate_check: false\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, _ := cmdNamed("doctor")
	cmd.SetContext(context.Background())
	if !updateCheckDisabled(cmd) {
		t.Fatal("updateCheckDisabled with update_check: false = false, want true")
	}
}

// TestUpdateCheckNotDisabledWithoutTheKey: the same selection, no
// update_check key at all, leaves the check on.
func TestUpdateCheckNotDisabledWithoutTheKey(t *testing.T) {
	t.Setenv(noUpdateCheckEnv, "")
	dir := t.TempDir()
	path := isolateProjects(t, dir)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"user: someone@example.com\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, _ := cmdNamed("doctor")
	cmd.SetContext(context.Background())
	if updateCheckDisabled(cmd) {
		t.Fatal("updateCheckDisabled with no update_check key = true, want false")
	}
}

// TestCLIVersionCheck exercises doctor's cli-version check directly:
// ok when current, ok when newer than the release, a non-red warning when
// behind (with the brew command as the fix), and an info line (never red)
// when the lookup can't produce an answer at all.
//
// Mutation: changing cliVersionCheck's warning branch to leave Severity
// "" makes the "behind" case an ordinary (red) failing check instead of a
// warning; this test's explicit Severity assertion catches that.
func TestCLIVersionCheck(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	withHTTPFake := func(t *testing.T, body string, status int) {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		oldClient, oldEndpoint := updateCheckHTTPClient, updateCheckEndpoint
		updateCheckHTTPClient, updateCheckEndpoint = srv.Client(), srv.URL
		t.Cleanup(func() { updateCheckHTTPClient, updateCheckEndpoint = oldClient, oldEndpoint })
	}

	t.Run("dev build is silently ok", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", dir)
		c := cliVersionCheck(context.Background(), "dev", now)
		if c.ID != "cli-version" || !c.OK || c.Severity != "" {
			t.Fatalf("dev build check = %+v, want a plain ok", c)
		}
	})

	t.Run("current release is ok", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", dir)
		withHTTPFake(t, `{"tag_name":"v0.6.1"}`, http.StatusOK)
		c := cliVersionCheck(context.Background(), "0.6.1", now)
		if !c.OK || c.Severity != "" {
			t.Fatalf("current release check = %+v, want a plain ok", c)
		}
	})

	t.Run("behind is a non-red warning naming the brew command", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", dir)
		withHTTPFake(t, `{"tag_name":"v0.7.0"}`, http.StatusOK)
		c := cliVersionCheck(context.Background(), "0.6.1", now)
		if c.OK {
			t.Fatal("behind check reports OK = true")
		}
		if c.Severity != "warning" {
			t.Fatalf("behind check severity = %q, want \"warning\" (never red)", c.Severity)
		}
		if c.Fix != updatecheck.BrewCommand {
			t.Fatalf("behind check fix = %q, want %q", c.Fix, updatecheck.BrewCommand)
		}
	})

	t.Run("an impossible lookup is informational, never red", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", dir)
		oldClient, oldEndpoint := updateCheckHTTPClient, updateCheckEndpoint
		updateCheckHTTPClient, updateCheckEndpoint = http.DefaultClient, "http://127.0.0.1:1/unreachable"
		t.Cleanup(func() { updateCheckHTTPClient, updateCheckEndpoint = oldClient, oldEndpoint })
		c := cliVersionCheck(context.Background(), "0.6.1", now)
		if c.OK {
			t.Fatal("unreachable lookup reports OK = true")
		}
		if c.Severity != "info" {
			t.Fatalf("unreachable lookup severity = %q, want \"info\" (never red)", c.Severity)
		}
	})
}

// TestDoctorReportsCLIVersionCheck is the full doctor command: the
// cli-version check is present, and never fails doctor (the lookup
// predictably fails: HTTPS_PROXY in TestMain sends it nowhere real).
func TestDoctorReportsCLIVersionCheck(t *testing.T) {
	r := newDoctorRig(t)
	out, _, err := execute(t, "doctor", "--json", "--dir", filepath.Dir(r.cfgPath))
	if err != nil {
		t.Fatalf("doctor --json failed: %v\n%s", err, out)
	}
	if !bytes.Contains([]byte(out), []byte(`"cli-version"`)) {
		t.Fatalf("doctor --json has no cli-version check:\n%s", out)
	}
}

// TestRootHasNoPersistentPostRun pins that the nag is never wired through
// a PersistentPostRun(E) field on the root command: that hook is skipped
// by cobra's Command.execute whenever the matched command's RunE returns
// an error (the vendored spf13/cobra v1.10.2 behavior
// TestWarnUpdateAvailableFiresWhenExecuteErrors exercises below), which is
// most command failures, so relying on it would silently drop the nag on
// them. cmd/fugaro/main.go calls WarnUpdateAvailable itself instead.
func TestRootHasNoPersistentPostRun(t *testing.T) {
	root := NewRootCmd()
	if root.PersistentPostRun != nil || root.PersistentPostRunE != nil {
		t.Fatal("root command has a PersistentPostRun(E): it is skipped whenever a command's RunE errors, so it must not be how the update-available nag is printed; call WarnUpdateAvailable from cmd/fugaro/main.go instead")
	}
}

// TestWarnUpdateAvailableNilCommand: cmd/fugaro/main.go calls
// WarnUpdateAvailable unconditionally on whatever ExecuteContextC returns,
// so a nil cmd (should ExecuteC's contract ever allow one) must never
// panic.
func TestWarnUpdateAvailableNilCommand(t *testing.T) {
	WarnUpdateAvailable(nil)
}

// TestWarnUpdateAvailableFiresWhenExecuteErrors drives a full cobra
// ExecuteC() that errors (doctor with no installation configured, which
// returns a real RunE error, not just non-zero output), then calls
// WarnUpdateAvailable the way cmd/fugaro/main.go does: unconditionally,
// after Execute returns, regardless of its error. This is the case
// cobra's own PersistentPostRun cannot cover at all — Command.execute
// returns as soon as RunE errors, skipping PostRun and PersistentPostRun
// — which is exactly why root.go no longer has one and main.go calls
// WarnUpdateAvailable itself instead.
//
// Mutation: making WarnUpdateAvailable return early whenever cmd carries
// a non-nil error (threading err into it, as a PersistentPostRun-based
// design would effectively require) makes this test fail: the whole
// point is that it must still print on this exact path.
func TestWarnUpdateAvailableFiresWhenExecuteErrors(t *testing.T) {
	withVersion(t, "0.6.1")
	dir := t.TempDir()
	isolateProjects(t, dir)
	if err := updatecheck.Save(os.Getenv, updatecheck.Entry{Latest: "0.7.0"}); err != nil {
		t.Fatal(err)
	}
	oldTTY := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })

	root := NewRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"doctor", "--dir", t.TempDir()})
	cmd, err := root.ExecuteC()
	if err == nil {
		t.Fatal("doctor with no installation configured = nil error, want one (TestDoctorWithNoInstallationSaysRunInit pins the same case)")
	}

	// What cmd/fugaro/main.go does right after ExecuteContextC returns,
	// whatever err is.
	WaitForUpdateCheck()
	WarnUpdateAvailable(cmd)

	want := "fugaro 0.7.0 is available (you have 0.6.1): " + updatecheck.BrewCommand
	if got := errOut.String(); !strings.Contains(got, want) {
		t.Fatalf("stderr after an erroring Execute = %q, want it to contain %q", got, want)
	}
}

// TestWarnUpdateAvailableFailsClosedWhenPersistentPreRunNeverRan reproduces
// PR #296's review finding exactly: cobra's Command.execute returns out of
// several paths (a flag parse error, a failing Args validator, Find/Traverse
// never matching a command, --help) before PersistentPreRun ever runs, so
// the command's context never carries updateCheckDisabled's resolution —
// including whether FUGARO_NO_UPDATE_CHECK=1 or update_check: false apply.
// Before the fix, WarnUpdateAvailable treated "no value in the context" as
// "not disabled" and printed the nag anyway in every one of these cases,
// even with the env switch set; it must now fail closed and print nothing.
//
// Mutation: reverting updateCheckDisabledFromContext to its one-return-value
// form (treating a missing context value as disabled=false, ok implied
// true) makes every case here print the line again.
func TestWarnUpdateAvailableFailsClosedWhenPersistentPreRunNeverRan(t *testing.T) {
	withVersion(t, "0.6.1")
	oldTTY := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })

	cases := []struct {
		name string
		args []string
	}{
		{"flag parse error", []string{"ls", "--bogus"}},
		{"Args validator fails", []string{"doctor", "x", "y", "z"}},
		{"unknown command", []string{"nosuchcmd"}},
		{"--help on a real command", []string{"ls", "--help"}},
		{"bare root (help)", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", dir)
			t.Setenv(noUpdateCheckEnv, "1")
			if err := updatecheck.Save(os.Getenv, updatecheck.Entry{Latest: "0.7.0"}); err != nil {
				t.Fatal(err)
			}
			root := NewRootCmd()
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(c.args)
			cmd, _ := root.ExecuteC()
			WarnUpdateAvailable(cmd)
			if errOut.Len() != 0 {
				t.Fatalf("stderr = %q, want nothing: PersistentPreRun never ran for this invocation (FUGARO_NO_UPDATE_CHECK=1 was set), so the nag must fail closed", errOut.String())
			}
		})
	}
}

// TestWarnUpdateAvailableFailsClosedWithLocalConfigDisable is the same
// fail-closed property, but for the local config's update_check: false
// rather than the env switch: PersistentPreRun never running means that
// was never resolved either.
func TestWarnUpdateAvailableFailsClosedWithLocalConfigDisable(t *testing.T) {
	withVersion(t, "0.6.1")
	oldTTY := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })
	t.Setenv(noUpdateCheckEnv, "")
	dir := t.TempDir()
	path := isolateProjects(t, dir)
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"user: someone@example.com\nupdate_check: false\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := updatecheck.Save(os.Getenv, updatecheck.Entry{Latest: "0.7.0"}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"nosuchcmd"})
	cmd, _ := root.ExecuteC()
	WarnUpdateAvailable(cmd)
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing: update_check: false is set, and the broken invocation never resolved it, so the nag must fail closed", errOut.String())
	}
}

// TestWarnUpdateAvailablePrintsOnANormalSuccessfulExecuteC is the
// counterpart to the fail-closed tests above: a well-formed command that
// actually reaches PersistentPreRun and RunE must still get the nag,
// driven through the same full ExecuteC() path (not a direct
// warnUpdateAvailable call), so the fail-closed fix hasn't swallowed the
// ordinary case along with the broken ones. `config example` needs no
// installation or checkout and always succeeds.
func TestWarnUpdateAvailablePrintsOnANormalSuccessfulExecuteC(t *testing.T) {
	withVersion(t, "0.6.1")
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv(noUpdateCheckEnv, "")
	oldTTY := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })
	if err := updatecheck.Save(os.Getenv, updatecheck.Entry{Latest: "0.7.0"}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"config", "example"})
	cmd, err := root.ExecuteC()
	if err != nil {
		t.Fatalf("config example failed: %v", err)
	}
	WarnUpdateAvailable(cmd)
	want := "fugaro 0.7.0 is available (you have 0.6.1): " + updatecheck.BrewCommand
	if got := errOut.String(); !strings.Contains(got, want) {
		t.Fatalf("stderr = %q, want it to contain %q: a normal, successful command must still get the nag", got, want)
	}
}

// TestWaitForUpdateCheckAbandonsASlowCheck pins that WaitForUpdateCheck
// gives up on a background check that is still running past
// updateCheckJoinBound, rather than blocking on it indefinitely (or for
// its own ~1s FetchTimeout): "never slows a command" means a blackholed
// network must not visibly hold up an otherwise-fast command.
//
// Mutation: making startUpdateCheck launch the goroutine bare (`go
// updatecheck.Refresh(...)`, never setting updateCheckDone at all) makes
// WaitForUpdateCheck's nil check return at once regardless of the
// still-blocked fake client; this test's lower bound on the elapsed time
// catches that.
func TestWaitForUpdateCheckAbandonsASlowCheck(t *testing.T) {
	withVersion(t, "0.6.1")
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("CLOUD_RUN_EXECUTION", "")
	t.Setenv(ciEnv, "")
	oldBound := updateCheckJoinBound
	updateCheckJoinBound = 100 * time.Millisecond
	t.Cleanup(func() { updateCheckJoinBound = oldBound })
	oldTTY := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })

	// updateCheckHTTPClient/updateCheckEndpoint are package-level vars the
	// still-running background goroutine reads (as arguments to
	// updatecheck.Refresh) whenever the Go scheduler gets around to
	// starting it — not necessarily before WaitForUpdateCheck's bound
	// elapses. Restoring them before that goroutine is known to be done
	// would race with its read (`go test -race` caught exactly this with
	// the first version of this test); waiting on updateCheckDone directly
	// in t.Cleanup, registered before the restore (cleanups run LIFO, so
	// it runs after), makes sure the goroutine has actually finished
	// before anything reassigns them.
	oldClient, oldEndpoint := updateCheckHTTPClient, updateCheckEndpoint
	t.Cleanup(func() { updateCheckHTTPClient, updateCheckEndpoint = oldClient, oldEndpoint })
	block := make(chan struct{})
	updateCheckHTTPClient = fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		select {
		case <-block:
		case <-req.Context().Done():
		}
		return nil, errors.New("unreachable")
	})
	updateCheckEndpoint = "http://127.0.0.1:1/unused"

	cmd, _ := cmdNamed("ls")
	startUpdateCheck(cmd, false)
	done := updateCheckDone
	t.Cleanup(func() {
		close(block)
		if done != nil {
			<-done
		}
	})

	start := time.Now()
	WaitForUpdateCheck()
	elapsed := time.Since(start)
	if elapsed < updateCheckJoinBound {
		t.Fatalf("WaitForUpdateCheck returned after %v, want at least the join bound %v: the background check never finished, so it must have been abandoned at the bound, not short-circuited", elapsed, updateCheckJoinBound)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("WaitForUpdateCheck took %v, want it bounded near %v: a blackholed network must never visibly slow the command", elapsed, updateCheckJoinBound)
	}
}

// TestWaitForUpdateCheckReturnsEarlyWhenTheCheckFinishes: with a generous
// join bound, a background check that fails (or succeeds) immediately
// must not make WaitForUpdateCheck wait out the bound anyway.
func TestWaitForUpdateCheckReturnsEarlyWhenTheCheckFinishes(t *testing.T) {
	withVersion(t, "0.6.1")
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("CLOUD_RUN_EXECUTION", "")
	t.Setenv(ciEnv, "")
	oldBound := updateCheckJoinBound
	updateCheckJoinBound = time.Second
	t.Cleanup(func() { updateCheckJoinBound = oldBound })
	oldTTY := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })

	oldClient, oldEndpoint := updateCheckHTTPClient, updateCheckEndpoint
	updateCheckHTTPClient = fakeHTTPClient(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fails immediately")
	})
	updateCheckEndpoint = "http://127.0.0.1:1/unused"
	t.Cleanup(func() { updateCheckHTTPClient, updateCheckEndpoint = oldClient, oldEndpoint })

	cmd, _ := cmdNamed("ls")
	startUpdateCheck(cmd, false)

	start := time.Now()
	WaitForUpdateCheck()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("WaitForUpdateCheck took %v for a check that finished immediately, want well under the %v join bound", elapsed, updateCheckJoinBound)
	}
}

// TestUpdateCheckHTTPClientNeverFollowsARedirect pins that the production
// HTTP client (updateCheckHTTPClient) never follows a redirect: two real
// servers, the first redirecting to the second, which would answer with a
// valid-looking release and could observe the Referer header naming the
// first if a redirect were ever followed. Reproduced before the fix: a
// plain &http.Client{} followed the 302 and the second server received a
// request (with Referer); after the fix, the second server sees nothing
// at all, and Fetch (StatusFound != StatusOK) reports ok = false.
//
// Mutation: removing CheckRedirect from updateCheckHTTPClient's
// construction makes the second server receive the request again.
func TestUpdateCheckHTTPClientNeverFollowsARedirect(t *testing.T) {
	var secondServerHit bool
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondServerHit = true
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL, http.StatusFound)
	}))
	defer first.Close()

	version, ok := updatecheck.Fetch(context.Background(), updateCheckHTTPClient, first.URL)
	if ok {
		t.Fatalf("Fetch followed the redirect and reported ok = true, version = %q", version)
	}
	if secondServerHit {
		t.Fatal("the second (redirect target) server received a request: the client followed the redirect instead of refusing it")
	}
}

// TestCompletionTreeExcludedThroughRealCommandTree reproduces PR #296's
// review finding exactly: cmd.Name() for `completion zsh`/`completion
// bash` is "zsh"/"bash" (cobra dispatches to a real generated subcommand),
// never "completion", and a shell's completion script invokes the hidden
// `__complete` command directly. A fake command literally named
// "completion" (as the table-driven tests above use) can't catch a
// regression here at all, since it never exercises cmd.Name() returning
// anything else — this drives the real tree through root.ExecuteC().
//
// Mutation: reverting excludedCommand to plain `updateCheckExcluded[cmd.Name()]`
// (dropping inCompletionTree) makes every case here nag and start the
// background check again.
func TestCompletionTreeExcludedThroughRealCommandTree(t *testing.T) {
	withVersion(t, "0.6.1")
	oldTTY := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stderrIsTerminal = oldTTY })

	for _, args := range [][]string{
		{"completion", "zsh"},
		{"completion", "bash"},
		{"completion", "fish"},
		{"__complete", "l"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", dir)
			t.Setenv("CLOUD_RUN_EXECUTION", "")
			t.Setenv(ciEnv, "")
			if err := updatecheck.Save(os.Getenv, updatecheck.Entry{Latest: "0.7.0"}); err != nil {
				t.Fatal(err)
			}
			root := NewRootCmd()
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(args)
			cmd, err := root.ExecuteC()
			if err != nil {
				t.Fatalf("%v failed: %v", args, err)
			}
			if shouldStartUpdateCheck(cmd, false) {
				t.Errorf("shouldStartUpdateCheck(%q) = true, want false: the background check must never run for completion machinery", cmd.Name())
			}
			WaitForUpdateCheck()
			WarnUpdateAvailable(cmd)
			if errOut.Len() > 0 && strings.Contains(errOut.String(), "is available") {
				t.Fatalf("stderr = %q, want no update nag: completion commands must stay silent", errOut.String())
			}
		})
	}
}

// TestStderrIsTerminalRejectsDevNull pins the real terminal check:
// /dev/null is a character device, so isTTY's cruder ModeCharDevice check
// would wrongly call it a terminal, which is exactly what cobra's
// generated completion scripts redirect stderr to (`2>/dev/null`).
//
// Mutation: reverting stderrIsTerminal to isTTY makes this true.
func TestStderrIsTerminalRejectsDevNull(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if realTerminal(f) {
		t.Fatal("realTerminal(/dev/null) = true, want false")
	}
	if !isTTY(f) {
		t.Skip("isTTY(/dev/null) is already false on this platform (not a character device here); nothing to contrast")
	}
	// Reaching here means isTTY(/dev/null) = true on this platform (/dev/null
	// is a character device, which is all isTTY checks): exactly the bug
	// realTerminal fixes, confirmed above to no longer be true of it.
}

// TestStderrRedirectedToDevNullNeverChecksOrPrints drives the real command
// tree with stderr actually redirected to /dev/null (not a bytes.Buffer,
// which isTTY/realTerminal both already treat as non-terminal — this
// specifically exercises the character-device case the review flagged):
// a cached newer release must not be reported as "warned" (the real
// terminal would never see the line that day), and the background check
// must never start either.
//
// Mutation: reverting stderrIsTerminal to isTTY makes this test fail, both
// on the "no request" assertion (shouldStartUpdateCheck would say true)
// and the "cache untouched" one (Warn would mark it warned).
func TestStderrRedirectedToDevNullNeverChecksOrPrints(t *testing.T) {
	withVersion(t, "0.6.1")
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("CLOUD_RUN_EXECUTION", "")
	t.Setenv(ciEnv, "")
	if err := updatecheck.Save(os.Getenv, updatecheck.Entry{Latest: "0.7.0"}); err != nil {
		t.Fatal(err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(devNull)
	root.SetArgs([]string{"config", "example"})
	cmd, err := root.ExecuteC()
	if err != nil {
		t.Fatalf("config example failed: %v", err)
	}
	if shouldStartUpdateCheck(cmd, false) {
		t.Fatal("shouldStartUpdateCheck with stderr = /dev/null returned true, want false")
	}
	WaitForUpdateCheck()
	WarnUpdateAvailable(cmd)

	e := updatecheck.Load(os.Getenv)
	if e.Warned != "" {
		t.Fatalf("cache Warned = %q after a /dev/null run, want \"\": the real terminal never saw the line today, so it must not be spent", e.Warned)
	}
}
