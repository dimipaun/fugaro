package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The second half of enabling the Batch backend: `fugaro backends use
// cloud-batch`, the one command that writes `backend:` (the design's §4.4 and
// Task 8 step 3; backends.go). The provisioning writes the block and DOES NOT
// select -- every launch of the installation moves onto VMs -- so the selection
// is here, behind two fences that are the whole point of the command: the block
// has to be there, and doctor's Batch checks have to pass.
//
// The rig is the doctor rig: the same fakes, the same config, and the same
// batchProvisioned / batchProvisionedWithout worlds the checks' own tests are
// built from, so a fence tested here is the fence doctor reads and not a second
// derivation of it.

// useBackend runs the command the way the owner does: aimed at the rig's own
// config file, so the project the write lands in is the one the fakes describe.
func useBackend(t *testing.T, r *doctorRig, name string) (string, error) {
	t.Helper()
	out, _, err := execute(t, "backends", "use", name, "--config", r.cfgPath)
	return out, err
}

// selectedBackend is the backend the rig's config selects, through the config's
// own parser: an unreadable file is a failure here rather than a value.
func selectedBackend(t *testing.T, r *doctorRig) string {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, r.cfgPath)))
	if err != nil {
		t.Fatalf("%s does not read back: %v", r.cfgPath, err)
	}
	return lc.Backend()
}

// TestBackendsUseRefusesAnInstallationWithNoBlock: the first fence. A config
// with no batch block is one nothing on the machine knows a run's VM shape
// from, so the selection is refused whatever doctor says -- doctor is silent
// about a block that is not there, and that silence is not a pass.
func TestBackendsUseRefusesAnInstallationWithNoBlock(t *testing.T) {
	r := newDoctorRig(t)
	before := readFileString(t, r.cfgPath)
	out, err := useBackend(t, r, backend.CloudBatch)
	if err == nil {
		t.Fatalf("an installation with no batch block selected the backend:\n%s", out)
	}
	if !strings.Contains(err.Error(), "carries no batch block") || !strings.Contains(err.Error(), "fugaro setup --provision-batch") {
		t.Errorf("the refusal does not name the provisioning that writes the block: %v", err)
	}
	if after := readFileString(t, r.cfgPath); after != before {
		t.Errorf("the refused selection wrote the file:\n%s", after)
	}
}

// TestBackendsUseRefusesAnInstallationDoctorRefuses: the second fence. The
// block is there and one of the checks it needs is not -- the job image's
// repository -- so the selection is refused, in doctor's own words with
// doctor's own fix, and nothing is written. (The check's own test is what says
// it is reported; here it is the GATE that is asserted, and the case is one
// check rather than a dozen so the assertion is about the gate and not about
// every check's text.)
func TestBackendsUseRefusesAnInstallationDoctorRefuses(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisionedWithout(t, r, f, batchStepRepo)
	r.withBatch(t, f, batchLists)
	before := readFileString(t, r.cfgPath)
	out, err := useBackend(t, r, backend.CloudBatch)
	if err == nil {
		t.Fatalf("an installation that fails a Batch check selected the backend:\n%s", out)
	}
	if !strings.Contains(err.Error(), "batch-job-image") || !strings.Contains(err.Error(), "fugaro doctor") {
		t.Errorf("the refusal does not name the failing check and the command that reports them all: %v", err)
	}
	if after := readFileString(t, r.cfgPath); after != before {
		t.Errorf("the refused selection wrote the file:\n%s", after)
	}
}

// TestBackendsUseSelectsAProvisionedBackend: the whole point -- a provisioned
// installation, every check passing, and the one key is written. The widening
// is printed (this moves every launch onto VMs), the config's own comments and
// every other byte survive the write (yamledit, not a re-marshal), and the
// installation really runs on the backend afterwards.
func TestBackendsUseSelectsAProvisionedBackend(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	// A comment in the owner's own file: the write is one key, so it is still
	// there afterwards -- which is what a hand edit of the same file would not
	// promise.
	r.withBatch(t, f, batchLists)
	data := readFileString(t, r.cfgPath) + "# the VMs the reaper sweeps\n"
	if err := os.WriteFile(r.cfgPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := useBackend(t, r, backend.CloudBatch)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := selectedBackend(t, r); got != backend.CloudBatch {
		t.Fatalf("the config selects %q, want %q:\n%s", got, backend.CloudBatch, readFileString(t, r.cfgPath))
	}
	after := readFileString(t, r.cfgPath)
	if !strings.Contains(after, "# the VMs the reaper sweeps") {
		t.Errorf("the write dropped a comment from the config:\n%s", after)
	}
	if !strings.Contains(after, "batch:\n") {
		t.Errorf("the write dropped the batch block:\n%s", after)
	}
	for _, want := range []string{
		// The widening, in the terms the refusal carries for a coding agent.
		"Docker daemon",
		"one VM per run",
		// And where every run goes from now on, with the direction back.
		"launched on Cloud Batch from now on",
		"fugaro backends use " + backend.CloudRun,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the write does not say %q:\n%s", want, out)
		}
	}
}

// TestBackendsUseIsRefusedInACodingAgentsSession: the selection is a widening
// -- a run's job gets a VM with a Docker daemon on it -- so no coding agent's
// session writes it, whatever the checks said. The block and the checks are the
// passing ones, so what this asserts is the fence and not the gate.
func TestBackendsUseIsRefusedInACodingAgentsSession(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	batchProvisioned(t, r, f)
	r.withBatch(t, f, batchLists)
	before := readFileString(t, r.cfgPath)
	agentSession(t)
	out, err := useBackend(t, r, backend.CloudBatch)
	if err == nil {
		t.Fatalf("a coding agent's session selected the backend:\n%s", out)
	}
	if !strings.Contains(err.Error(), "coding agent's session") || !strings.Contains(err.Error(), "fugaro backends use "+backend.CloudBatch) {
		t.Errorf("the refusal does not name the fence and the command the owner runs: %v", err)
	}
	if after := readFileString(t, r.cfgPath); after != before {
		t.Errorf("the refused selection wrote the file:\n%s", after)
	}
}

// TestBackendsUseCloudRunNeedsNothing: the direction back is never fenced --
// Cloud Run is the default and needs no block and no check -- so an
// installation mid-provisioning can always return to it. The name a config that
// already selects it carries is not written again either.
func TestBackendsUseCloudRunNeedsNothing(t *testing.T) {
	r, f := newDoctorRig(t), newBatchFakes(t)
	// A config that selects cloud-batch with no provisioning behind it at all:
	// the state the fence above refuses to CREATE, and the one this command
	// has to be able to leave.
	r.withBatchSelected(t, f, batchBlock, batchLists)
	out, err := useBackend(t, r, backend.CloudRun)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := selectedBackend(t, r); got != backend.CloudRun {
		t.Fatalf("the config selects %q, want %q:\n%s", got, backend.CloudRun, readFileString(t, r.cfgPath))
	}
	if !strings.Contains(out, "moves it back to Cloud Batch") {
		t.Errorf("the direction back is not named:\n%s", out)
	}
	// The same selection again: nothing is written, and the file is untouched.
	again := readFileString(t, r.cfgPath)
	out, err = useBackend(t, r, backend.CloudRun)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already runs on "+backend.CloudRun) {
		t.Errorf("a selection the config already makes is not reported as unchanged:\n%s", out)
	}
	if after := readFileString(t, r.cfgPath); after != again {
		t.Errorf("the redundant selection rewrote the file:\n%s", after)
	}
}

// TestBackendsUseRefusesANameThatIsNotABackend: the argument is held to the two
// names the seam defines, so a typo is refused here rather than written into the
// config for the next launch to refuse.
func TestBackendsUseRefusesANameThatIsNotABackend(t *testing.T) {
	r := newDoctorRig(t)
	before := readFileString(t, r.cfgPath)
	out, err := useBackend(t, r, "cloud-batch ")
	if err == nil {
		t.Fatalf("a name that is not a backend was accepted:\n%s", out)
	}
	if !strings.Contains(err.Error(), "fugaro backends ls") {
		t.Errorf("the refusal does not name the command that lists them: %v", err)
	}
	if after := readFileString(t, r.cfgPath); after != before {
		t.Errorf("the refused name wrote the file:\n%s", after)
	}
}
