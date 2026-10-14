package runner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// cloneYAML is the fixture workflow with checkout: clone (design
// generic-tool.md §2, G3): no image block, so a clone job may run it.
const cloneYAML = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
  review_rounds: 2
workflows:
  app:
    checkout: clone
    commands:
      build: sh build.sh
      test: sh test.sh
      rerun_failed:
        command: sh test.sh
        each: "{id}"
      reports: ["build/test-results/*.xml"]
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// gitToken is what the harness's provider hands the run, and what the fake
// mise echoes: a published reason must never carry it.
const gitToken = "ghs_fixture_token"

// cloneJob makes the run's job a checkout: clone one: no checkout in
// WorkDir (the harness never creates it) and FUGARO_CHECKOUT=clone in the
// job's environment, with a provider token for mise's rate limit.
func (h *harness) cloneJob(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(h.deps.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("the harness's checkout already exists: %v", err)
	}
	h.deps.Env = append(h.deps.Env, runner.CheckoutEnv+"=clone")
	h.provider.Auth = func(time.Duration) gitprov.GitAuth {
		return gitprov.GitAuth{Username: "x-access-token", Token: gitToken}
	}
}

// fakeMise puts a mise on the run's PATH that appends "<cwd>|<argv>" to a
// log, one line per call, echoes the git token it was given, and exits with
// exit. It returns the log's path.
func (h *harness) fakeMise(t *testing.T, exit int) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "mise.log")
	script := "#!/bin/sh\n" +
		"printf '%s|%s\\n' \"$PWD\" \"$*\" >> \"$MISE_LOG\"\n" +
		"echo \"token $GITHUB_TOKEN\"\n" +
		"echo 'mise: no such tool node' >&2\n" +
		"exit " + string(rune('0'+exit)) + "\n"
	testutil.WriteFiles(t, dir, map[string]string{"mise": script})
	h.deps.Env = append(filterEnv(h.deps.Env, "PATH"), "PATH="+dir+":"+os.Getenv("PATH"), "MISE_LOG="+log)
	return log
}

type miseCall struct {
	Dir  string
	Args []string
}

// miseCalls reads the fake mise's log: each call's directory and arguments.
func miseCalls(t *testing.T, log string) []miseCall {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var out []miseCall
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		dir, args, _ := strings.Cut(line, "|")
		out = append(out, miseCall{Dir: dir, Args: strings.Fields(args)})
	}
	return out
}

// provisionTiming is the record's provision stage timing, if it ran.
func provisionTiming(rec *runstore.Record) *runstore.StageTiming {
	for i, s := range rec.Stages {
		if s.Name == "provision" {
			return &rec.Stages[i]
		}
	}
	return nil
}

// TestCloneProvisionRunsMiseInstall (plan Task 17, design generic-tool.md
// §2, G4): a run whose job has no derived image clones its repository
// blobless and installs the checkout's root mise config itself, before its
// first stage, recording the time as the provision stage.
func TestCloneProvisionRunsMiseInstall(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	log := h.fakeMise(t, 0)
	h.cloneJob(t)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	got := miseCalls(t, log)
	if len(got) != 1 || len(got[0].Args) == 0 || got[0].Args[0] != "install" || got[0].Dir != h.deps.WorkDir {
		t.Fatalf("mise calls = %+v, want one `mise install` in %s", got, h.deps.WorkDir)
	}
	if provisionTiming(rec) == nil {
		t.Fatalf("no provision timing in %+v", rec.Stages)
	}
	if v := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.partialclonefilter"); v != "blob:none" {
		t.Fatalf("the checkout's filter = %q, want blob:none", v)
	}
}

// TestCloneProvisionNoMiseConfig: a checkout with no root mise config has
// nothing to install: no mise call and no provision stage.
func TestCloneProvisionNoMiseConfig(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, nil)
	log := h.fakeMise(t, 0)
	h.cloneJob(t)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := miseCalls(t, log); len(got) != 0 {
		t.Fatalf("mise calls = %+v, want none", got)
	}
	if provisionTiming(rec) != nil {
		t.Fatalf("a provision timing with nothing to install: %+v", rec.Stages)
	}
}

// TestBakedRunProvisionsNothing: a baked job's image installed the tools at
// build time, so it runs no mise and records no provision stage.
func TestBakedRunProvisionsNothing(t *testing.T) {
	h := newHarnessFiles(t, "", nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	log := h.fakeMise(t, 0)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := miseCalls(t, log); len(got) != 0 {
		t.Fatalf("mise calls = %+v, want none", got)
	}
	if provisionTiming(rec) != nil {
		t.Fatalf("a baked run recorded a provision stage: %+v", rec.Stages)
	}
}

// TestCloneProvisionFailureIsInfraError: mise refusing to install the
// repository's tools ends the run as infra_error with mise's last line
// (redacted) as the reason.
func TestCloneProvisionFailureIsInfraError(t *testing.T) {
	h := newHarnessFiles(t, cloneYAML, nil, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	h.fakeMise(t, 1)
	h.cloneJob(t)
	rec, err := h.run(t, implement("feature"))
	if err == nil {
		t.Fatal("a failed mise install did not fail the run")
	}
	if rec.Status != runstore.StatusInfraError || rec.Reason == "" {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "mise install failed") || !strings.Contains(rec.Reason, "no such tool node") {
		t.Fatalf("reason = %q, want mise's last line", rec.Reason)
	}
	if strings.Contains(rec.Reason, gitToken) {
		t.Fatalf("reason leaks the git token: %q", rec.Reason)
	}
	if provisionTiming(rec) == nil {
		t.Fatalf("a failed provision was not timed: %+v", rec.Stages)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("the agent ran anyway: %+v", h.agent.calls)
	}
}
