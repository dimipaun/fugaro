package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// cmdNamed is a bare cobra command with the given name, a "json" and
// "plain" flag (as real commands that have them declare), and a buffer
// for stderr: enough for the update-check decision functions, which only
// ever look at Name(), Flags() and ErrOrStderr().
func cmdNamed(name string) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: name}
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("plain", false, "")
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	return cmd, &errBuf
}

// TestShouldStartUpdateCheckExclusions pins every reason the background
// check must never fire, each checked in isolation.
//
// Mutation: dropping any one of shouldStartUpdateCheck's conditions (for
// example `&& !backend.OnCloudRun(...)`) makes exactly that case in this
// table wrongly true.
func TestShouldStartUpdateCheckExclusions(t *testing.T) {
	withVersion(t, "0.6.1")
	cases := []struct {
		name     string
		cmdName  string
		version  string
		cloudRun string
		disabled bool
		want     bool
	}{
		{"ordinary command", "doctor", "0.6.1", "", false, true},
		{"exec (the runner)", "exec", "0.6.1", "", false, false},
		{"completion", "completion", "0.6.1", "", false, false},
		{"help", "help", "0.6.1", "", false, false},
		{"version", "version", "0.6.1", "", false, false},
		{"dev build", "doctor", "dev", "", false, false},
		{"unparseable build", "doctor", "0.6.1-rc1+local", "", false, false},
		{"inside Cloud Run", "doctor", "0.6.1", "exec-1", false, false},
		{"disabled", "doctor", "0.6.1", "", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			Version = c.version
			t.Setenv("CLOUD_RUN_EXECUTION", c.cloudRun)
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
	oldTTY := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = oldTTY })

	cases := []struct {
		name     string
		cmdName  string
		tty      bool
		json     bool
		plain    bool
		cloudRun bool
		disabled bool
		want     bool
	}{
		{"ordinary, a terminal", "doctor", true, false, false, false, false, true},
		{"not a terminal", "doctor", false, false, false, false, false, false},
		{"--json", "doctor", true, true, false, false, false, false},
		{"--plain", "ls", true, false, true, false, false, false},
		{"excluded command", "version", true, false, false, false, false, false},
		{"inside Cloud Run", "doctor", true, false, false, true, false, false},
		{"disabled", "doctor", true, false, false, false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stderrIsTerminal = func(io.Writer) bool { return c.tty }
			if c.cloudRun {
				t.Setenv("CLOUD_RUN_EXECUTION", "exec-1")
			} else {
				t.Setenv("CLOUD_RUN_EXECUTION", "")
			}
			cmd, _ := cmdNamed(c.cmdName)
			if c.json {
				_ = cmd.Flags().Set("json", "true")
			}
			if c.plain {
				_ = cmd.Flags().Set("plain", "true")
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
