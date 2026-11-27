package cli

// raw_output_test.go is CO3: no path prints the Terraform or docker stream on
// the terminal (D3438). What a person reads is the run's own account — one
// line per step, and the plan in plain words — and the whole of what the
// children printed is in the run's log, which is where a failure sends them.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoRawTerraformOnScreen: --verbose adds the run's own reasons and never
// Terraform's stream, and no state puts a `module.` address on the screen
// (D3438). The plan the person is confirming is there in plain words, and
// Terraform's own account of it — the addresses and the attribute paths — is
// in the log beside it, which is where --allow-delete's reader takes one from.
func TestNoRawTerraformOnScreen(t *testing.T) {
	applyingRig(t)
	out, errOut, err := executeStdin(t, "", "update", "-v")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	all := out + errOut
	for _, raw := range []string{convergeNoise, "module.installation.google_storage_bucket.runs"} {
		if strings.Contains(all, raw) {
			t.Errorf("--verbose put the Terraform stream on the screen: %q\n%s", raw, all)
		}
	}
	if !strings.Contains(all, "Plan: 1 to create.") {
		t.Errorf("the plan is not on the screen in plain words:\n%s", all)
	}
	_, log := onlyLog(t, "update")
	for _, raw := range []string{convergeNoise, "create module.installation.google_storage_bucket.runs"} {
		if !strings.Contains(log, raw) {
			t.Errorf("the log does not hold %q:\n%s", raw, log)
		}
	}
}

// fakeDocker puts a `docker` on PATH that answers the daemon check and the
// build itself: noise on both streams, and success. image.BuildLocal runs it
// through image.ExecRunner, so the only seam a hermetic test needs is the
// name on PATH.
func fakeDocker(t *testing.T, noise string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' '" + noise + "'\nprintf '%s\\n' '" + noise + "-ERR' >&2\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestLocalBuildToLog: `image build --local` hands docker's own output to the
// run's log and never to the terminal (D3438). The build's report — what it
// built, from what — is the command's answer and stays on stdout.
func TestLocalBuildToLog(t *testing.T) {
	const noise = "DOCKER-BUILD-NOISE"
	checkoutWith(t, npmFiles())
	isolateState(t)
	fakeDocker(t, noise)
	out, errOut, err := executeStdin(t, "", "image", "build", "--local", "--base", "fugaro-base:1.2.3", "--no-smoke")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if strings.Contains(out+errOut, noise) {
		t.Errorf("the docker stream reached the terminal:\n%s%s", out, errOut)
	}
	if !strings.Contains(out, "built ") {
		t.Errorf("the build's own report is not on stdout:\n%s", out)
	}
	_, log := onlyLog(t, "image-build")
	for _, want := range []string{noise, noise + "-ERR"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log does not hold docker's %q:\n%s", want, log)
		}
	}
}
