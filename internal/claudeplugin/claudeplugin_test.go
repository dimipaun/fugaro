package claudeplugin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestAllowed(t *testing.T) {
	for _, ok := range [][]string{
		{"plugin", "marketplace", "list", "--json"},
		{"plugin", "list", "--json"},
		{"plugin", "marketplace", "update", "fugaro"},
		{"plugin", "marketplace", "add", "--scope", "user", "dimipaun/fugaro"},
		{"plugin", "marketplace", "add", "--scope", "user", "some-one/fugaro.fork_2"},
		{"plugin", "install", "fugaro@fugaro", "--scope", "user"},
		{"plugin", "update", "fugaro@fugaro", "--scope", "user"},
		{"plugin", "update", "fugaro@fugaro", "--scope", "project"},
		{"plugin", "update", "fugaro@fugaro", "--scope", "local"},
	} {
		if err := Allowed(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range [][]string{
		nil,
		{"plugin", "install", "fugaro@fugaro", "--scope", "user", "-y"},
		{"plugin", "install", "fugaro@fugaro", "--scope", "project"},
		{"plugin", "update", "fugaro@fugaro", "--scope", "managed"},
		{"plugin", "update", "fugaro@fugaro"},
		{"plugin", "install", "other@fugaro", "--scope", "user"},
		{"plugin", "marketplace", "update"},
		{"plugin", "marketplace", "add", "--scope", "user", "-x/fugaro"},
		{"plugin", "marketplace", "add", "--scope", "user", "owner/-rf"},
		{"plugin", "marketplace", "add", "--scope", "user", "owner/.."},
		{"plugin", "marketplace", "add", "--scope", "user", "owner/name;rm"},
		{"plugin", "marketplace", "add", "--scope", "user", "https://evil.example/x"},
		{"plugin", "marketplace", "add", "--scope", "project", "dimipaun/fugaro"},
		{"plugin", "marketplace", "add", "dimipaun/fugaro"},
		{"--dangerously-skip-permissions", "plugin", "list", "--json"},
		{"-p", "hello"},
		{"plugin", "uninstall", "fugaro@fugaro"},
	} {
		if err := Allowed(bad); err == nil {
			t.Errorf("%q allowed", bad)
		}
	}
}

func TestFind(t *testing.T) {
	look := func(p string, err error) func(string) (string, error) {
		return func(name string) (string, error) {
			if name != "claude" {
				t.Fatalf("looked up %q", name)
			}
			return p, err
		}
	}
	if p, err := Find(look("/opt/bin/claude", nil)); err != nil || p != "/opt/bin/claude" {
		t.Fatalf("absolute: %q %v", p, err)
	}
	if _, err := Find(look("", exec.ErrNotFound)); !errors.Is(err, ErrNoClaude) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := Find(look("bin/claude", exec.ErrDot)); err == nil || errors.Is(err, ErrNoClaude) || !strings.Contains(err.Error(), "relative PATH entry") {
		t.Fatalf("dot: %v", err)
	}
	if _, err := Find(look("bin/claude", nil)); err == nil || !strings.Contains(err.Error(), "not an absolute path") {
		t.Fatalf("relative: %v", err)
	}
}

func runner(t *testing.T, f *testutil.ClaudePluginFake) (*Runner, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &Runner{Bin: f.Bin(), Dir: t.TempDir(), Out: &out}, &out
}

func TestChangeRunsTheExactArgvInDir(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	r, out := runner(t, f)
	if err := r.Change(t.Context(), updatePlugin("project")...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.Calls(t)
	if len(calls) != 1 || !slices.Equal(calls[0].Args, updatePlugin("project")) || !testutil.SamePath(calls[0].Dir, r.Dir) {
		t.Fatalf("calls %+v", calls)
	}
	for _, want := range []string{"running: " + f.Bin() + " plugin update fugaro@fugaro --scope project", "claude: ok: plugin update fugaro@fugaro --scope project"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestChangeRefusedCallRunsNothing(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	r, out := runner(t, f)
	err := r.Change(t.Context(), "plugin", "install", "fugaro@fugaro", "--scope", "user", "--dangerously-skip-permissions")
	if err == nil || len(f.Calls(t)) != 0 || out.Len() != 0 {
		t.Fatalf("err %v, calls %+v, output %q", err, f.Calls(t), out)
	}
}

func TestChangeTimesOut(t *testing.T) {
	old := ChangeTimeout
	ChangeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { ChangeTimeout = old })
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "sleep", "", installUser...)
	r, _ := runner(t, f)
	start := time.Now()
	err := r.Change(t.Context(), installUser...)
	if err == nil || !strings.Contains(err.Error(), "did not finish within 300ms") || time.Since(start) > 10*time.Second {
		t.Fatalf("err %v after %s", err, time.Since(start))
	}
}

func TestChangeOutputIsMadeSafeAndCapped(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "print", "plain\n\x1b]52;c;ZXZpbA==\x07evil\n"+strings.Repeat("line\n", 60), updateMarket...)
	r, out := runner(t, f)
	if err := r.Change(t.Context(), updateMarket...); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.ContainsRune(s, 0x1b) || strings.ContainsRune(s, 0x07) {
		t.Fatalf("a control character reached the output: %q", s)
	}
	// 63 lines: plain, the escape line, 60 x line, ok; 40 are printed.
	for _, want := range []string{`claude: \u001b]52;c;ZXZpbA==\u0007evil`, "claude: ... (23 more lines)"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

func TestChangeFailureIsAnError(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "fail", "network down\n", installUser...)
	r, out := runner(t, f)
	err := r.Change(t.Context(), installUser...)
	if err == nil || !strings.Contains(err.Error(), "claude plugin install fugaro@fugaro --scope user failed: exit status 1") || !strings.Contains(out.String(), "claude: network down") {
		t.Fatalf("err %v\n%s", err, out)
	}
}

func TestListsParse(t *testing.T) {
	f := testutil.NewClaudePluginFake(t,
		`[{"name":"fugaro","source":"github","repo":"dimipaun/fugaro","installLocation":"/x"}]`,
		`[{"id":"fugaro@fugaro","version":"0.5.1","scope":"project","enabled":true,"projectPath":"/p"}]`)
	r, _ := runner(t, f)
	ms, err := r.Markets(t.Context())
	if err != nil || len(ms) != 1 || ms[0] != (Market{Name: "fugaro", Source: "github", Repo: "dimipaun/fugaro"}) {
		t.Fatalf("markets %+v %v", ms, err)
	}
	is, err := r.Installed(t.Context())
	if err != nil || len(is) != 1 || is[0] != (Install{ID: "fugaro@fugaro", Version: "0.5.1", Scope: "project", ProjectPath: "/p"}) {
		t.Fatalf("installed %+v %v", is, err)
	}
	f.Write(t, "plugins.json", "not json")
	if _, err := r.Installed(t.Context()); err == nil || !strings.Contains(err.Error(), "not the expected JSON list") {
		t.Fatalf("bad JSON: %v", err)
	}
}

func shorten(t *testing.T, v *time.Duration, d time.Duration) {
	old := *v
	*v = d
	t.Cleanup(func() { *v = old })
}

func TestChildEnvIsAnAllowlist(t *testing.T) {
	t.Setenv("FAKE_SECRET", "hunter2")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("GIT_SSH_COMMAND", "evil")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "k")
	for k, v := range map[string]string{"HOME": "/h", "LC_ALL": "C", "XDG_CONFIG_HOME": "/x", "CLAUDE_CONFIG_DIR": "/c", "HTTPS_PROXY": "p", "https_proxy": "p", "SSH_AUTH_SOCK": "/s", "LANG": "C"} {
		t.Setenv(k, v)
	}
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.Write(t, "dumpenv", "")
	r, out := runner(t, f)
	if err := r.Change(t.Context(), updateMarket...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(f.Dir, "env.out"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(data)
	for _, bad := range []string{"FAKE_SECRET", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "GIT_SSH_COMMAND", "AWS_SECRET_ACCESS_KEY", "hunter2"} {
		if strings.Contains(env, bad) {
			t.Errorf("child environment has %s:\n%s", bad, env)
		}
	}
	for _, want := range []string{"HOME=/h", "LC_ALL=C", "XDG_CONFIG_HOME=/x", "CLAUDE_CONFIG_DIR=/c", "HTTPS_PROXY=p", "https_proxy=p", "SSH_AUTH_SOCK=/s", "LANG=C", "TERM=dumb", "PATH="} {
		if !strings.Contains(env, want+"") && !strings.Contains(env, "\n"+want) && !strings.HasPrefix(env, want) {
			t.Errorf("child environment lacks %s:\n%s", want, env)
		}
	}
}

func TestChildEnvPWDIsDir(t *testing.T) {
	got := childEnv([]string{"PWD=/elsewhere", "HOME=/h"}, "/d")
	if !slices.Contains(got, "PWD=/d") || slices.Contains(got, "PWD=/elsewhere") {
		t.Fatalf("%v", got)
	}
}

// stopOnceStarted runs install and cancels it as soon as the fake has started
// its background job (a fresh script can take long to start on a loaded machine, so
// no fixed timeout is reliable). It returns the error and how long the call
// took to return after the cancel.
func stopOnceStarted(t *testing.T, r *Runner, f *testutil.ClaudePluginFake) (error, time.Duration) {
	t.Helper()
	shorten(t, &ChangeTimeout, time.Minute)
	shorten(t, &KillWait, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Change(ctx, installUser...) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(f.Dir, "spawned")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	cancel()
	err := <-done
	return err, time.Since(start)
}

func TestStopKillsGrandchildren(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.Write(t, "grandchild", "")
	r, _ := runner(t, f)
	if err, _ := stopOnceStarted(t, r, f); err == nil {
		t.Fatal("no error")
	}
	time.Sleep(3 * time.Second) // the job would write 2 s after the fake started
	if _, err := os.Stat(filepath.Join(f.Dir, "marker")); err == nil {
		t.Fatal("a background job outlived the call and wrote its marker")
	}
}

func TestStopReturnsWhileAStrayHoldsStdout(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.Write(t, "holdout", "")
	r, _ := runner(t, f)
	if err, took := stopOnceStarted(t, r, f); err == nil || took > 3*time.Second {
		t.Fatalf("err %v after %s", err, took)
	}
}

func TestStdinIsTheNullDevice(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Close()
	defer pr.Close()
	old := os.Stdin
	os.Stdin = pr // open and silent: inheriting it would block the fake
	t.Cleanup(func() { os.Stdin = old })
	shorten(t, &ChangeTimeout, 5*time.Second)
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.Write(t, "readstdin", "")
	r, out := runner(t, f)
	if err := r.Change(t.Context(), updateMarket...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, _ := os.ReadFile(filepath.Join(f.Dir, "stdin.out"))
	if string(got) != "stdin-eof\n" {
		t.Fatalf("stdin gave %q", got)
	}
}

func TestOutputIsCapped(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.Write(t, "bigout", "")
	r, _ := runner(t, f)
	stdout, _, err := r.call(t.Context(), 10*time.Second, listMarkets)
	if err != nil || len(stdout) != maxOutput {
		t.Fatalf("kept %d bytes, err %v; want %d", len(stdout), err, maxOutput)
	}
}

func TestListFailurePrintsStderr(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.Write(t, "listfail", "not logged in\n")
	r, out := runner(t, f)
	if _, err := r.Markets(t.Context()); err == nil || !strings.Contains(out.String(), "claude: not logged in") {
		t.Fatalf("err %v\n%s", err, out)
	}
}

func TestCallRefusesWhatIsNotAllowed(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	r, _ := runner(t, f)
	if _, _, err := r.call(t.Context(), time.Second, []string{"-p", "hello"}); err == nil || len(f.Calls(t)) != 0 {
		t.Fatalf("err %v, calls %+v", err, f.Calls(t))
	}
}

func TestCallRefusesRelativeBinAndDir(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	for name, r := range map[string]*Runner{
		"relative bin": {Bin: "claude", Dir: t.TempDir()},
		"empty bin":    {Dir: t.TempDir()},
		"relative dir": {Bin: f.Bin(), Dir: "."},
		"empty dir":    {Bin: f.Bin()},
	} {
		if _, _, err := r.call(t.Context(), time.Second, listMarkets); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(f.Calls(t)) != 0 {
		t.Fatalf("calls %+v", f.Calls(t))
	}
}

func TestOnlyProjectAndLocalUpdatesRunInTheCheckout(t *testing.T) {
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	r, out := runner(t, f)
	if err := os.WriteFile(filepath.Join(r.Dir, "dimipaun"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{addMarket("dimipaun/fugaro"), updateMarket, installUser, updatePlugin("user"), updatePlugin("project"), updatePlugin("local")} {
		if err := r.Change(t.Context(), args...); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	if _, err := r.Markets(t.Context()); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls(t)
	data, _ := os.ReadFile(filepath.Join(f.Dir, "cwdfiles.log"))
	files := strings.Fields(string(data))
	if len(calls) != 7 || len(files) != 7 {
		t.Fatalf("calls %+v files %v", calls, files)
	}
	for i, c := range calls {
		inCheckout := testutil.SamePath(c.Dir, r.Dir)
		if want := i == 4 || i == 5; inCheckout != want {
			t.Errorf("%q ran in %s (checkout %s)", c.Args, c.Dir, r.Dir)
		}
		if !inCheckout && (files[i] != "0" || !strings.HasPrefix(c.Dir, filepath.Clean(os.TempDir())) && !strings.HasPrefix(c.Dir, "/private")) {
			t.Errorf("%q ran in %s with %s files, not an empty temp directory", c.Args, c.Dir, files[i])
		}
	}
	if _, err := os.Stat(calls[0].Dir); err == nil {
		t.Errorf("temp directory %s was not removed", calls[0].Dir)
	}
}
