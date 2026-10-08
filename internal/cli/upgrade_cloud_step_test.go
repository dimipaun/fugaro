package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// upgradeRig is an onboarded checkout of acme/app (two workflows, kinds go
// and web-node) whose local config records no base image yet, at version
// 0.5.1, with no claude; it returns the rig and the checkout's top as
// fugaro upgrade sees it.
func upgradeRig(t *testing.T) (*anchorModeRig, string) {
	t.Helper()
	fakeTerraform(t) // builds with go, which upgradeEnv takes off PATH
	upgradeEnv(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	loc, ok := pluginwire.Locate(".")
	if !ok {
		t.Fatal("the rig is not a checkout")
	}
	return r, loc.Root
}

func TestUpgradeInAnAgentSessionNeverTouchesTheCloud(t *testing.T) {
	r, root := upgradeRig(t)
	t.Setenv("CLAUDECODE", "1")
	noRecordReads(t)
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"pin: done", "cloud: skipped: a coding agent's session (CLAUDECODE is set) never applies cloud changes",
		"in your own terminal window, not through the agent, run fugaro upgrade --yes " + root, "or fugaro upgrade " + root + " to be asked at each step"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(*ran) != 0 || len(r.calls(t)) != 0 || len(r.ar.Requests()) != 0 || strings.Contains(out, "fugaro image refresh of") {
		t.Fatalf("the cloud was touched: steps %q\n%s", *ran, out)
	}
}

func TestUpgradeCheckSeesTheCloudWithoutTouchingIt(t *testing.T) {
	r, root := upgradeRig(t)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "none.json"))
	noRecordReads(t)
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--check")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "cloud: stale: base images to move: go: none recorded, this release's is "+managedRef("go", "0.5.1")) {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	if len(*ran) != 0 || len(r.calls(t)) != 0 {
		t.Fatalf("the cloud was touched: %q", *ran)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("--check wrote the settings")
	}
}

func TestUpgradeLocalNamesTheCloudCommand(t *testing.T) {
	_, root := upgradeRig(t)
	noRecordReads(t)
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--local")
	if err != nil || len(*ran) != 0 || !strings.Contains(out, "cloud: skipped: --local; for the cloud step, run in your own terminal window: fugaro upgrade "+root) {
		t.Fatalf("%v, steps %q\n%s", err, *ran, out)
	}
}

// TestUpgradePassesYesToTheRefresh: the cloud step runs after pin (the
// plugin step is still the notImplementedStep placeholder, Task 6 has not
// landed yet), and --yes reaches the refresh unchanged.
func TestUpgradePassesYesToTheRefresh(t *testing.T) {
	for _, yes := range []bool{true, false} {
		t.Run(map[bool]string{true: "yes", false: "interactive"}[yes], func(t *testing.T) {
			_, root := upgradeRig(t)
			var seen *initOptions
			old := newRefreshSteps
			newRefreshSteps = func(r *initRun, e *initEngine, p *refreshPlan) refreshSteps {
				seen = r.o
				return refreshSteps{
					base:     func(context.Context) error { return nil },
					reload:   func(context.Context) (*refreshTarget, error) { return &refreshTarget{lc: p.lc, cfg: p.cfg}, nil },
					checkJob: func(context.Context, *refreshTarget) error { return nil },
					builds:   func(context.Context, *refreshTarget) error { return nil },
				}
			}
			t.Cleanup(func() { newRefreshSteps = old })
			args := []string{"upgrade"}
			if yes {
				args = append(args, "--yes")
			} else {
				fakeTerminal(t)
			}
			out, _, err := executeStdin(t, "", args...)
			if err != nil || seen == nil || seen.yes != yes {
				t.Fatalf("%v, options %+v\n%s", err, seen, out)
			}
			pin, plugin, base, cloud := strings.Index(out, "pin: done"), strings.Index(out, "plugin: not implemented"), strings.Index(out, "step 2, base:"), strings.Index(out, "cloud: done")
			if pin < 0 || plugin < pin || base < plugin || cloud < base || !strings.Contains(out, "  "+root+": pin done, plugin not implemented, cloud done") {
				t.Fatalf("order or summary wrong:\n%s", out)
			}
		})
	}
}

func TestUpgradeCloudFailureNamesTheUpgradeRerun(t *testing.T) {
	_, root := upgradeRig(t)
	fakeSteps(t, "check job", &ExitError{Code: ExitRemoteError, Err: errors.New("Cloud Run refused the update")})
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	for _, want := range []string{
		"cloud: failed: Cloud Run refused the update; fugaro image refresh stopped at step 3 (check job), steps finished: preflight, base; once that is fixed, rerun fugaro upgrade --yes " + root + ": finished steps say No changes",
		"once that is fixed, rerun: fugaro upgrade --yes " + root,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "in this checkout") {
		t.Errorf("the stop names refresh's own rerun:\n%s", out)
	}
}

func TestUpgradeOneCheckoutsCloudFailureDoesNotHideAnother(t *testing.T) {
	_, root := upgradeRig(t)
	other, otherSettings := skillCheckout(t, wiredAt("v0.5.0"))
	fakeSteps(t, "base", userErr("the base copy was declined"))
	out, _, err := executeStdin(t, "", "upgrade", "--yes", root, other)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped in 1 of 2 checkout(s)") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if r := pluginwire.Status(otherSettings, "0.5.1", ""); r.Pin != pluginwire.OK || !strings.Contains(out, "  "+other+": pin done, plugin not implemented, cloud skipped") {
		t.Fatalf("the other checkout: %+v\n%s", r, out)
	}
}

func TestUpgradeTeammateSkipsTheCloud(t *testing.T) {
	_, _ = upgradeRig(t)
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes", "--config", filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil || len(*ran) != 0 || !strings.Contains(out, "cloud: skipped: no local project config selects this checkout") {
		t.Fatalf("%v, steps %q\n%s", err, *ran, out)
	}
}

// TestUpgradeRerunWhenCurrentIsNothingToDo: pin and cloud both current (the
// plugin step is still the notImplementedStep placeholder, so the summary
// cannot reach "nothing to do" as a whole until Task 6 lands): no settings
// write, and a cloud plan with no base to copy and no build.
func TestUpgradeRerunWhenCurrentIsNothingToDo(t *testing.T) {
	r, root := upgradeRig(t)
	r.appendConfig(t, "base_images: {web-node: "+managedRef("web-node", "0.5.1")+", go: "+managedRef("go", "0.5.1")+"}\n")
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	putWorkflowRecordFrom("api", "0.5.1", managedRef("go", "0.5.1"))
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.1")), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil || upgradeRead(t, settings) != wiredAt("v0.5.1") ||
		!strings.Contains(out, "pin: current: pinned to v0.5.1") ||
		!strings.Contains(out, "cloud: current: no base image to copy and no image to rebuild") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(r.calls(t)) != 0 || len(r.ar.Requests()) != 0 {
		t.Fatalf("the cloud touched terraform or the registry:\n%s", out)
	}
}

// TestUpgradeCloudCheckFailureIsAFailedStep: a real local-file error
// (cloudFailed) stops the checkout in every mode, --check included, never a
// skip (binding note from past reviews of this feature); the refresh itself
// is never attempted when cloudCheck already found the checkout broken.
func TestUpgradeCloudCheckFailureIsAFailedStep(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		exit int
	}{
		{"check", []string{"--check"}, ExitUserError},
		{"yes", []string{"--yes"}, ExitUserError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := upgradeRig(t)
			testutil.Git(t, root, "remote", "remove", "origin")
			ran := fakeSteps(t, "", nil)
			out, _, err := executeStdin(t, "", append([]string{"upgrade"}, tc.args...)...)
			if ExitCode(err) != tc.exit || !strings.Contains(out, "cloud: failed:") || !strings.Contains(out, "origin") ||
				strings.Contains(out, "stopped at step 1") {
				t.Fatalf("exit %d, want %d\n%s", ExitCode(err), tc.exit, out)
			}
			if len(*ran) != 0 {
				t.Fatalf("the refresh ran after cloudCheck already found a real error: %q", *ran)
			}
		})
	}
}

// TestUpgradeCloudFailedIsSkippedBeforeReadingUnderLocalAndAgent: per U7 the
// cloud step is skipped before anything is read under --local and in an
// agent's session, so a broken checkout (no origin) does not fail it: exit 0,
// skipped, the command to run named. --check and a real run still fail it
// (TestUpgradeCloudCheckFailureIsAFailedStep).
func TestUpgradeCloudFailedIsSkippedBeforeReadingUnderLocalAndAgent(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"local", "cloud: skipped: --local; for the cloud step, run in your own terminal window: fugaro upgrade "},
		{"agent", "cloud: skipped: a coding agent's session (CLAUDECODE is set) never applies cloud changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := upgradeRig(t)
			testutil.Git(t, root, "remote", "remove", "origin")
			args := []string{"upgrade"}
			if tc.name == "local" {
				args = append(args, "--local")
			} else {
				t.Setenv("CLAUDECODE", "1")
			}
			ran := fakeSteps(t, "", nil)
			out, _, err := executeStdin(t, "", args...)
			if err != nil || len(*ran) != 0 || !strings.Contains(out, tc.want) || strings.Contains(out, "cloud: failed") || !strings.Contains(out, "fugaro upgrade") {
				t.Fatalf("%v, steps %q\n%s", err, *ran, out)
			}
		})
	}
}

// TestUpgradeYesRefreshesTheNamedCheckoutNotTheWorkingDirectory: run from
// another directory, fugaro upgrade --yes <root> refreshes root.
// Mutation (run, restore): in cloudStep, ro.dir = "" instead of u.loc.Root.
func TestUpgradeYesRefreshesTheNamedCheckoutNotTheWorkingDirectory(t *testing.T) {
	_, root := upgradeRig(t)
	t.Chdir(t.TempDir())
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes", root)
	if err != nil || len(*ran) == 0 || !strings.Contains(out, "cloud: done") || !strings.Contains(out, "fugaro image refresh of acme/app is done") {
		t.Fatalf("%v, steps %q\n%s", err, *ran, out)
	}
}

// TestUpgradePassesTheCloudFlagsToTheRefresh: --config, --project, --region,
// --workflow, --image-source and --expect-digest reach the refresh.
// Mutation (run, restore): in cloudStep, ro := refreshOptions{} instead of
// u.o.refresh.
func TestUpgradePassesTheCloudFlagsToTheRefresh(t *testing.T) {
	r, _ := upgradeRig(t)
	var seen *initOptions
	var plan *refreshPlan
	old := newRefreshSteps
	newRefreshSteps = func(run *initRun, e *initEngine, p *refreshPlan) refreshSteps {
		seen, plan = run.o, p
		return refreshSteps{
			base:     func(context.Context) error { return nil },
			reload:   func(context.Context) (*refreshTarget, error) { return &refreshTarget{lc: p.lc, cfg: p.cfg}, nil },
			checkJob: func(context.Context, *refreshTarget) error { return nil },
			builds:   func(context.Context, *refreshTarget) error { return nil },
		}
	}
	t.Cleanup(func() { newRefreshSteps = old })
	digest := "go=sha256:" + strings.Repeat("a", 64)
	out, _, err := executeStdin(t, "", "upgrade", "--yes", "--config", r.cfg, "--project", "aurora", "--region", "us-east5",
		"--workflow", "app", "--image-source", "ghcr.io/acme", "--expect-digest", digest)
	if err != nil || seen == nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c := seen.cloud
	if c.config != r.cfg || c.project != "aurora" || c.region != "us-east5" || seen.imageSource != "ghcr.io/acme" ||
		len(seen.expectDigests) != 1 || seen.expectDigests[0] != digest || len(plan.workflows) != 1 || plan.workflows[0] != "app" {
		t.Fatalf("flags not passed: cloud %+v, image source %q, digests %q, workflows %q", c, seen.imageSource, seen.expectDigests, plan.workflows)
	}
}

// TestUpgradeCheckBlockedIsAFailureWithTheRerunLine: a custom base blocks the
// refresh, so --check reports it stale (exit 1, never skipped), and the
// reason carries the command the user ran, --check included.
// Mutations (run, restore): M15 return stepSkipped for cloudBlocked in the
// --check switch; M17 pass "" as the rerun in cloudStep's cloudCheck call.
func TestUpgradeCheckBlockedIsAFailureWithTheRerunLine(t *testing.T) {
	r, root := upgradeRig(t)
	r.appendConfig(t, "base_images: {go: "+refreshHost+"/fugaro-base/fugaro-go:dev-abc}\n")
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--check")
	if ExitCode(err) != ExitUserError || len(*ran) != 0 || !strings.Contains(out, "cloud: stale:") || strings.Contains(out, "cloud: skipped") {
		t.Fatalf("exit %d, steps %q\n%s", ExitCode(err), *ran, out)
	}
	if again := "fugaro upgrade --check " + root; !strings.Contains(out, again) {
		t.Fatalf("the reason lacks the rerun line %q:\n%s", again, out)
	}
}

// TestUpgradeCheckCurrentCloudIsCurrent: every kind on this release's base
// is "current" under --check, not stale or skipped.
// Mutation (run, restore): return stepStale for cloudCurrent in the --check
// switch.
func TestUpgradeCheckCurrentCloudIsCurrent(t *testing.T) {
	r, _ := upgradeRig(t)
	r.appendConfig(t, "base_images: {web-node: "+managedRef("web-node", "0.5.1")+", go: "+managedRef("go", "0.5.1")+"}\n")
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--check")
	if !strings.Contains(out, "cloud: current:") {
		t.Fatalf("%v\n%s", err, out)
	}
}
