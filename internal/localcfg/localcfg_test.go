package localcfg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

const sample = `version: 1
project: my-project
region: us-central1
runs_bucket: my-runs
registry: us-central1-docker.pkg.dev/my-project/fugaro
repos:
  acme/web: { provider: github, base_branch: main, workflows: [web] }
`

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxParallel != 20 || c.Build.MachineType != "E2_HIGHCPU_8" || c.BucketURL() != "gs://my-runs" || c.BuildRegion() != "us-central1" {
		t.Fatalf("defaults = %+v", c)
	}
	if r := c.Repos["acme/web"]; r.Provider != "github" || r.BaseBranch != "main" || len(r.Workflows) != 1 {
		t.Fatalf("repo = %+v", r)
	}
	c.Override("other-project", "europe-west1")
	if c.Project != "other-project" || c.Region != "europe-west1" || c.BuildRegion() != "europe-west1" {
		t.Fatalf("override = %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, msg string }{
		"unknown field": {sample + "colour: blue\n", "colour"},
		"bad project":   {strings.Replace(sample, "my-project\n", "My_Project\n", 1), "project"},
		"no bucket":     {strings.Replace(sample, "runs_bucket: my-runs\n", "", 1), "runs_bucket"},
		"bad repo":      {sample + "  nope: { workflows: [web] }\n", "owner/name"},
		"bad workflow":  {sample + "  acme/api: { workflows: [Web] }\n", "workflow"},
		"bad parallel":  {sample + "max_parallel: -1\n", "max_parallel"},
		"bad provider":  {sample + "  acme/api: { provider: githbu, workflows: [web] }\n", "must be one of github, bitbucket"},
		"fake provider": {sample + "  acme/api: { provider: fake, workflows: [web] }\n", "must be one of github, bitbucket"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want ~%q", err, tc.msg)
			}
		})
	}
}

func TestPathAndLoad(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"HOME": dir}
	get := func(k string) string { return env[k] }
	p, err := Path(get)
	if err != nil || p != filepath.Join(dir, ".config", "fugaro", "config.yaml") {
		t.Fatalf("Path = %q, %v", p, err)
	}
	env["XDG_CONFIG_HOME"] = filepath.Join(dir, "xdg")
	if p, _ = Path(get); p != filepath.Join(dir, "xdg", "fugaro", "config.yaml") {
		t.Fatalf("XDG Path = %q", p)
	}
	env["FUGARO_CONFIG"] = filepath.Join(dir, "explicit.yaml")
	if p, _ = Path(get); p != env["FUGARO_CONFIG"] {
		t.Fatalf("FUGARO_CONFIG Path = %q", p)
	}
	if _, err := Load(p); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("missing file: %v", err)
	}
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Project != "my-project" {
		t.Fatalf("Load = %+v, %v", c, err)
	}
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Parse(data); err != nil || again.RunsBucket != "my-runs" {
		t.Fatalf("round trip = %+v, %v", again, err)
	}
}

func TestMe(t *testing.T) {
	testutil.IsolateGit(t)
	// IsolateGit points GIT_CONFIG_GLOBAL at os.DevNull, which git cannot
	// take a write lock on (it needs to create a sibling .lock file). Point
	// it at a writable temp file instead so this test's own
	// `git config --global` below can succeed; this keeps the developer's
	// real global config out of the test, same as IsolateGit intends.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	c, _ := Parse([]byte(sample + "user: someone@example.com\n"))
	if me, err := c.Me(context.Background()); err != nil || me != "someone@example.com" {
		t.Fatalf("Me = %q, %v", me, err)
	}
	c, _ = Parse([]byte(sample))
	testutil.Git(t, "", "config", "--global", "user.email", "git@example.com")
	if me, err := c.Me(context.Background()); err != nil || me != "git@example.com" {
		t.Fatalf("Me from git = %q, %v", me, err)
	}
}
