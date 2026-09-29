package tf

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeBin is the fake terraform built once for the package's tests.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fugaro-faketerraform-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeBin = filepath.Join(dir, "terraform")
	out, err := exec.Command("go", "build", "-o", fakeBin, "github.com/dimipaun/fugaro/internal/infra/tf/faketerraform").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "building the fake terraform: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeCall is one invocation the fake recorded.
type fakeCall struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Dir  string   `json:"dir"`
}

// fakeRig is a workdir, a log and the fake's script for one test.
type fakeRig struct {
	dir, cache, log string
	script          map[string]any
}

func newRig(t *testing.T) *fakeRig {
	t.Helper()
	root := t.TempDir()
	r := &fakeRig{
		dir:    filepath.Join(root, "work"),
		cache:  filepath.Join(root, "cache", "fugaro", "terraform-plugins"),
		log:    filepath.Join(root, "calls.jsonl"),
		script: map[string]any{},
	}
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return r
}

// env builds the allowlisted environment from parent and adds the fake's
// own two variables, which the allowlist rightly drops.
func (r *fakeRig) env(t *testing.T, parent []string) []string {
	t.Helper()
	env, err := Env(parent, r.dir, r.cache)
	if err != nil {
		t.Fatal(err)
	}
	script, err := json.Marshal(r.script)
	if err != nil {
		t.Fatal(err)
	}
	return append(env, "FAKE_TERRAFORM_LOG="+r.log, "FAKE_TERRAFORM_SCRIPT="+string(script))
}

func (r *fakeRig) tf(t *testing.T, parent []string) *TF {
	t.Helper()
	tf, err := New(fakeBin, r.dir, r.env(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	return tf
}

func (r *fakeRig) calls(t *testing.T) []fakeCall {
	t.Helper()
	f, err := os.Open(r.log)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var calls []fakeCall
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var c fakeCall
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return calls
}

// call returns the last recorded call of the subcommand.
func (r *fakeRig) call(t *testing.T, sub string) fakeCall {
	t.Helper()
	calls := r.calls(t)
	for i := len(calls) - 1; i >= 0; i-- {
		if len(calls[i].Args) > 0 && calls[i].Args[0] == sub {
			return calls[i]
		}
	}
	t.Fatalf("no %q call among %v", sub, calls)
	return fakeCall{}
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}
