package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// init_checkout_upgrade_test.go is `fugaro update`'s checkout steps
// (init_checkout_upgrade.go): the three of `fugaro upgrade`'s steps that belong
// to the checkout rather than to the installation, run by update's own stage.
//
// The stage is driven directly, on the anchor rig's checkout (the working
// directory it resolves), the way the other stage tests drive theirs: the rig's
// fake terraform, fake cloud and fake build records are what the cloud step
// needs, and `upgradeEnv` is the hermetic machine.

// updateCheckoutRig is the update door's checkout-upgrade stage over a new
// anchor rig's checkout. It takes no initRig: every caller wants the checkout
// in the working directory, which is what the stage reads.
func updateCheckoutRig(t *testing.T, o *initOptions) (*checkoutUpgradeStage, *anchorModeRig) {
	t.Helper()
	fakeTerraform(t)
	if releaseVersion() == "" {
		upgradeEnv(t, "0.5.2")
	}
	r := newAnchorModeRig(t, refreshYAML, true)
	if o.door == "" {
		o.door = updateDoor.name
	}
	st := newCheckoutUpgradeStage(rigEngine(t, r.initRig, o))
	st.e.r.cmd.SetContext(context.Background()) // the cloud step runs the refresh on it
	return st, r
}

// stageOut is what the stage wrote to the run's account.
func stageOut(s *checkoutUpgradeStage) string { return s.e.r.w.(*syncBuf).String() }

// wiredSettingsAt leaves the rig's checkout pinned to ref, the state a checkout
// an older fugaro pinned is in.
func wiredSettingsAt(t *testing.T, r *anchorModeRig, ref string) string {
	t.Helper()
	settings := filepath.Join(r.root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt(ref)), 0o644); err != nil {
		t.Fatal(err)
	}
	return settings
}

// TestUpdateRunsPinPluginAndImages: a checkout whose plugin pin and job image
// predate this binary is brought up by `fugaro update`'s own stage — the pin
// moves, the checkout is left with the new ref uncommitted, and the cloud step
// runs rather than being skipped. Before this stage existed, update left both
// stale and doctor kept reporting plugin-pin and a stale base image.
func TestUpdateRunsPinPluginAndImages(t *testing.T) {
	fakeSteps(t, "", nil)
	stage, r := updateCheckoutRig(t, &initOptions{yes: true})
	settings := wiredSettingsAt(t, r, "v0.5.1")

	if _, err := stage.Apply(context.Background(), initflow.Env{Yes: true}); err != nil {
		t.Fatalf("%v\n%s", err, stageOut(stage))
	}
	if got := pluginwire.Status(settings, "0.5.2", ""); got.Pin != pluginwire.OK {
		t.Fatalf("the pin is %+v after the update\n%s", got, stageOut(stage))
	}
	for _, want := range []string{"pin: done: pinned to v0.5.2 in " + settings, "git diff", "cloud: done"} {
		if !strings.Contains(stageOut(stage), want) {
			t.Errorf("output lacks %q:\n%s", want, stageOut(stage))
		}
	}
}

// TestUpdateOutsideACheckoutSkipsCheckoutSteps: outside a Fugaro checkout there
// is no pin, no plugin and no image to bring up, and the stage says so in one
// line instead of failing.
func TestUpdateOutsideACheckoutSkipsCheckoutSteps(t *testing.T) {
	t.Chdir(t.TempDir()) // a directory that is no checkout of anything
	if releaseVersion() == "" {
		upgradeEnv(t, "0.5.2")
	}
	st := newCheckoutUpgradeStage(rigEngine(t, newInitRig(t), &initOptions{door: updateDoor.name}))
	status, err := st.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != initflow.Skipped || !strings.Contains(status.Detail, "not in a git checkout") {
		t.Fatalf("status %+v", status)
	}
	out, err := st.Apply(context.Background(), initflow.Env{Yes: true})
	if err != nil || !strings.Contains(out.Detail, "not in a git checkout") {
		t.Fatalf("%v %+v", err, out)
	}
}

// TestUpdateCloudWritesStillRefusedInAgentSession: the stage adds no way past
// the cloud step's own refusal — in a coding agent's session the cloud step is
// skipped, before anything is read, while the local steps still run. This is
// the refusal `fugaro upgrade` has had all along, not a new one.
func TestUpdateCloudWritesStillRefusedInAgentSession(t *testing.T) {
	fakeSteps(t, "", nil)
	stage, r := updateCheckoutRig(t, &initOptions{yes: true})
	wiredSettingsAt(t, r, "v0.5.1")
	t.Setenv(agentMarkers[0], "1")

	if _, err := stage.Apply(context.Background(), initflow.Env{Yes: true}); err != nil {
		t.Fatalf("%v\n%s", err, stageOut(stage))
	}
	got := stageOut(stage)
	if !strings.Contains(got, "cloud: skipped: a coding agent's session") {
		t.Errorf("the cloud step was not refused:\n%s", got)
	}
	if len(r.ar.Requests()) != 0 || len(r.calls(t)) != 0 {
		t.Errorf("the cloud was touched in a coding agent's session: %v %v", r.ar.Requests(), r.calls(t))
	}
	if st := pluginwire.Status(filepath.Join(r.root, ".claude", "settings.json"), "0.5.2", ""); st.Pin != pluginwire.OK {
		t.Errorf("the local pin step did not run in a coding agent's session: %+v", st)
	}
}

// TestUpdateImageBuildNeedsItsOwnConfirmation: the billable build is money, so
// the cloud step's own confirmation is what covers it — a run with no terminal
// and no --yes stops at the refresh's refusal instead of building, and the
// stage reports it as the failure it is rather than silently skipping the
// repository's images.
func TestUpdateImageBuildNeedsItsOwnConfirmation(t *testing.T) {
	stage, r := updateCheckoutRig(t, &initOptions{})
	wiredSettingsAt(t, r, "v0.5.1")

	_, err := stage.Apply(context.Background(), initflow.Env{Interactive: false})
	if err == nil {
		t.Fatalf("a run that cannot confirm anything built the images anyway:\n%s", stageOut(stage))
	}
	if !strings.Contains(err.Error(), "real terminal") {
		t.Errorf("the refusal does not name the terminal it needs: %v", err)
	}
	if len(r.ar.Requests()) != 0 {
		t.Errorf("the registry was touched without a confirmation: %v", r.ar.Requests())
	}
}

// TestUpdateRegistersTheCheckoutSteps: the stage is not merely implemented, it
// is the list `fugaro update` runs — which is the whole point of the owner's
// rule: one command, and the checkout's pin, plugin and images move with it.
func TestUpdateRegistersTheCheckoutSteps(t *testing.T) {
	fakeTerraform(t) // builds with go, which upgradeEnv takes off PATH
	upgradeEnv(t, "0.5.2")
	e := rigEngine(t, newInitRig(t), &initOptions{door: updateDoor.name})
	if _, ok := stageNamed(initStages(e), initflow.Checkout); !ok {
		t.Fatal("fugaro update does not run the checkout's upgrade steps: the pin, the plugin and the job images stay stale")
	}
}
