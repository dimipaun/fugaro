package claudeplugin

import (
	"bytes"
	"errors"
	"os/exec"
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
	if err := r.Change(t.Context(), updateMarket...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.Calls(t)
	if len(calls) != 1 || !slices.Equal(calls[0].Args, updateMarket) || !testutil.SamePath(calls[0].Dir, r.Dir) {
		t.Fatalf("calls %+v", calls)
	}
	for _, want := range []string{"running: " + f.Bin() + " plugin marketplace update fugaro", "claude: ok: plugin marketplace update fugaro"} {
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
