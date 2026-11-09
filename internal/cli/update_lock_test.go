package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// update_lock_test.go is the installation half of the workdir lock: two
// `fugaro update` runs on one installation. internal/infra's own
// workdirlock_test.go holds the lock itself (the refusal's sentence, the
// kernel's release when the holder is killed); what is here is the pair of
// converges the real failure was made of — the log of 2026-11-09 has
// 20261109114021 and 20261109114035 fourteen seconds apart, and 114021 died
// in terraform plan with "Failed to write plan file: open fugaro.tfplan: no
// such file or directory" because 114035's PrepareWorkdir had removed
// <workdir>/gcp under it.
//
// The two runs are a goroutine and a second process of this binary: the lock
// is the kernel's between processes, so the exclusion between two of them is
// what the test has to be made of. TestMain dispatches the second one
// (main_test.go), and the first one is held inside terraform by the rig's own
// wrapper.

// planMarker is what faketerraform puts in every plan file it writes with -out=
// (its own PlanMarker, which is the fake's main package's and so is not
// importable from here).
const planMarker = "fake-terraform-plan\n"

// convergeChildEnv marks the re-executed test binary as the second fugaro
// update process, and convergeChildHome carries the XDG homes it must work in
// past TestMain's own.
const (
	convergeChildEnv  = "FUGARO_TEST_CONVERGE_CHILD"
	convergeChildHome = "FUGARO_TEST_CONVERGE_CHILD_XDG_"
)

// updateInProcess is one `fugaro update --yes`: the command the test's own
// process runs, and the one the child process runs. It takes no *testing.T,
// because the first converge is still inside terraform while the second one is
// refused and neither may call t.Fatal from there.
func updateInProcess() (string, string, error) {
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(NormalizeArgs([]string{"update", "--yes", "--json"}))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// runConvergeChild is the second process, whose exit code is the command's. It
// is main_test.go's TestMain that reaches it, for the reason stated there.
func runConvergeChild() int {
	// TestMain gave the child TestMain's own three directories; the rig's are
	// the ones the first update is working in, and the two have to be the same
	// installation for the test to mean anything.
	for _, k := range []string{"XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		if v := os.Getenv(convergeChildHome + k); v != "" {
			os.Setenv(k, v)
		}
	}
	out, errOut, err := updateInProcess()
	fmt.Fprint(os.Stdout, out)
	fmt.Fprint(os.Stderr, errOut)
	if err != nil {
		// The line cmd/fugaro/main.go prints for every failure: without it the
		// second process would say nothing at all, and the test would be
		// checking a refusal nobody ever sees.
		fmt.Fprintln(os.Stderr, "fugaro:", ErrorText(err))
	}
	return ExitCode(err)
}

// secondUpdate runs the second fugaro update as its own process, in the rig's
// installation and with the output captured, and returns what the command
// printed and what it exited with. A second process is what the kernel's lock
// is between, and it is also what the failure of 2026-11-09 was made of: two
// invocations of the binary, not two runs of one.
func secondUpdate(t *testing.T) (out, errOut string, code int) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0])
	cmd.Dir = wd
	cmd.Env = append(os.Environ(), convergeChildEnv+"=1")
	for _, k := range []string{"XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		cmd.Env = append(cmd.Env, convergeChildHome+k+"="+os.Getenv(k))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running the second update: %v", err)
		}
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// blockTheFirstPlan rewrites the rig's terraform wrapper so that the first
// `plan` call stops until release is called, and returns the file that appears
// while it is stopped. The first plan and not every one: the update that must
// be refused reaches a plan too when nothing holds it back, and a wait it
// shared would stop the test instead of failing it.
//
// The wait is in the wrapper because the wrapper is this rig's own file, while
// FAKE_TERRAFORM_SCRIPT's steps are shared data a pause key would put in every
// other test's way. It is a real process waiting on a real file, which is the
// only way to hold one converge inside terraform while another one starts.
func blockTheFirstPlan(t *testing.T, r *initRig) (started string, release func()) {
	t.Helper()
	started = filepath.Join(r.dir, "plan-started")
	goFile := filepath.Join(r.dir, "plan-go")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = plan ] && [ ! -e '" + started + "' ]; then\n" +
		"  : > '" + started + "'\n" +
		"  while [ ! -e '" + goFile + "' ]; do sleep 0.01; done\n" +
		"fi\n" +
		"FAKE_TERRAFORM_LOG='" + r.log + "' FAKE_TERRAFORM_SCRIPT=\"$(cat '" + r.scriptPath + "')\" exec '" + fakeTerraform(t) + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(r.dir, "bin", "terraform"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return started, func() { _ = os.WriteFile(goFile, nil, 0o600) }
}

// waitForPath polls until the file appears. The converge that makes it runs in
// another goroutine, so a sleep and a hope is the only other way to wait for
// it — and this is a file the wrapper itself writes, so it is a signal and not
// a timing assumption.
func waitForPath(t *testing.T, path, what string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waited 60s for %s (%s)", what, path)
}

// changedObjects is how many storage requests write or delete: a refused run
// may read — it loads the project config to know which project's workdir it is
// asking about — and it may not change anything.
func changedObjects(reqs []gcpfake.Request) int {
	n := 0
	for _, r := range reqs {
		if r.Method != "GET" {
			n++
		}
	}
	return n
}

// TestTwoUpdatesOnOneInstallationDoNotClobberEachOther is the failure the lock
// exists for, at the level it happened: the first converge is inside terraform
// plan in the workdir it prepared, the second one starts, and — before the
// lock — its PrepareWorkdir removed <workdir>/gcp and wrote it again, so the
// first one's plan wrote into a directory that was no longer there.
//
// The second run is refused before the loop exists, which is also why preflight
// cannot be blamed for it: a refusal raised where a workdir is prepared — or by
// cancelling the context to stop the run at once — is recorded as the failure
// of whatever stage the loop had reached, preflight first, and a reader then
// sees the stage's name where the cause was a second converge on this machine.
func TestTwoUpdatesOnOneInstallationDoNotClobberEachOther(t *testing.T) {
	r := updateRig(t)
	started, release := blockTheFirstPlan(t, r)

	type ended struct {
		out, errOut string
		err         error
	}
	first := make(chan ended, 1)
	go func() {
		out, errOut, err := updateInProcess()
		first <- ended{out, errOut, err}
	}()
	waitForPath(t, started, "the first update to be inside terraform plan")

	before := struct {
		terraform, changed int
	}{len(r.calls(t)), changedObjects(r.gcs.Requests())}

	out, errOut, code := secondUpdate(t)

	// The lock is the first update's, so the pid the refusal names is this
	// process's — and the holder really is another process, which is the whole
	// point of running the second one in one.
	refusal := fmt.Sprintf("another fugaro update is running on this machine for project %s (pid %d), wait for it to finish, then rerun", initProject, os.Getpid())
	if code == 0 {
		t.Fatalf("the second update ran while the first held the workdirs:\n%s%s", out, errOut)
	}
	if code != ExitUserError {
		t.Errorf("the second update exited %d, want %d:\n%s%s", code, ExitUserError, out, errOut)
	}
	// The one line, whole, as cmd/fugaro/main.go prints a failure, and the last
	// thing the command printed: no stage, no "[todo]", nothing to look up.
	// (The rig's own warnings about its temporary XDG directories and the
	// project header go to the same stream before it, as they do for every
	// other command in a test.)
	if got, want := errOut, "fugaro: "+refusal+"\n"; !strings.HasSuffix(got, want) {
		t.Errorf("the command does not end with the refusal:\n%s\nwant it to end with\n%s", got, want)
	}
	if n := strings.Count(errOut, "another fugaro update is running"); n != 1 {
		t.Errorf("the refusal is printed %d times:\n%s", n, errOut)
	}
	// And the refusal a machine reads: one object carrying the reason, with no
	// stage list and no failed stage — nothing ran, so nothing failed, and
	// preflight least of all.
	var res struct {
		Error  string            `json:"error"`
		Stages []json.RawMessage `json:"stages"`
		Failed json.RawMessage   `json:"failed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("the refusal is not one --json object (%v):\n%s", err, out)
	}
	if res.Error != refusal {
		t.Errorf("--json carries the refusal as %q, want %q", res.Error, refusal)
	}
	if len(res.Stages) != 0 {
		t.Errorf("the refused run reports stages: %s", out)
	}
	if len(res.Failed) != 0 {
		t.Errorf("the refused run reports a failed stage (%s): %s", res.Failed, out)
	}
	if got := out + errOut; strings.Contains(got, "preflight") || strings.Contains(got, "cancel") {
		t.Errorf("the refusal reads as a stage's failure:\n%s%s", out, errOut)
	}
	// The second run changed nothing: no terraform (so no PrepareWorkdir, no
	// plan, no apply), and no storage write.
	if got := len(r.calls(t)); got != before.terraform {
		t.Errorf("the refused run ran terraform: %q", r.calls(t)[before.terraform:])
	}
	if got := changedObjects(r.gcs.Requests()); got != before.changed {
		t.Errorf("the refused run changed %d objects in Cloud Storage", got-before.changed)
	}

	// The first run is let go: it finishes in the workdir it prepared, which
	// the second one did not touch — the plan it wrote there is still there,
	// and its variables still parse.
	release()
	got := <-first
	if got.err != nil {
		t.Fatalf("the first update failed: %v\n%s%s", got.err, got.out, got.errOut)
	}
	// The plan file the fake writes for -out=, which is how a run whose plan
	// succeeded tells the two apart from one whose workdir moved: the same
	// file the failure of 2026-11-09 could not be written into.
	plan, err := os.ReadFile(filepath.Join(r.root(), infra.PlanFile))
	if err != nil || string(plan) != planMarker {
		t.Errorf("the first run's plan file is %q, %v, want %q", plan, err, planMarker)
	}
	r.tfvars(t)

	// And the lock went with it: the next converge on this installation — the
	// one the refusal tells the user to rerun — has it.
	if out, _, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("the update the refusal asked the user to rerun: %v\n%s", err, out)
	}
}

// TestTheWorkdirLockIsOnlyTheConverges: the lock is the converge's own. `run`,
// `doctor` and `runs ls` read an installation and start work in it, and none of
// them rebuilds a workdir, so none of them takes the lock — and a converge in
// flight on this machine is no reason for any of them to stop. The `run` arm is
// the one that matters most: a run in flight is a cloud execution, which is a
// different thing from this lock, and the refusal's sentence sends the reader
// away from confusing the two.
func TestTheWorkdirLockIsOnlyTheConverges(t *testing.T) {
	newCloudFixture(t)
	baseBranchCheckout(t, "acme/app", baseBranchYAML("git: { provider: github, base_branch: develop }\n"), "")
	// doctor answers what it finds on the machine, so its own answer before the
	// lock is taken is what the answer after it is compared with.
	dir := t.TempDir()
	_, _, withoutLock := execute(t, "doctor", "--json", "--dir", dir)

	lock, err := infra.LockWorkdirs(os.Getenv, initProject, "fugaro update")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()

	if out, errOut, err := execute(t, "run", "--run-id", "20261109-100000-abcd", "A task"); err != nil {
		t.Fatalf("fugaro run was refused by a converge's lock: %v\n%s%s", err, out, errOut)
	}
	if out, _, err := execute(t, "runs", "ls", "--since", "0", "--json"); err != nil {
		t.Fatalf("fugaro runs ls was refused by a converge's lock: %v\n%s", err, out)
	}
	out, errOut, err := execute(t, "doctor", "--json", "--dir", dir)
	if got := ExitCode(err); got != ExitCode(withoutLock) {
		t.Errorf("fugaro doctor exited %d with a converge's lock held, %d without it", got, ExitCode(withoutLock))
	}
	if text := out + errOut; strings.Contains(text, "another fugaro update is running") {
		t.Errorf("fugaro doctor read the lock file:\n%s", text)
	}
}
