package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The block the compute stage records (design compute-backend.md §4.4,
// docs/backends.md "The batch block"). Until this stage wrote one, enabling the
// Batch backend was a provisioning, four lines copied out of `terraform output`
// and a hand edit -- the state the owner's HARD RULE (the documented command
// does the whole job) does not allow. What these tests hold is the first half
// of the two commands that replaced it: the block lands in the machine's own
// project config from the root's own outputs, a re-run changes nothing, a field
// the owner wrote is the field that stays, and a block whose image cannot be
// formed honestly is not written at all.
//
// The other half -- the selection -- is `fugaro backends use`, and the one line
// this stage ends with (backends_use_test.go).

// computeOwnPin is the base image of an installation that may run on Batch: an
// image of the installation's own Artifact Registry. The rig's own computePin is
// the release pin through the remote repository, which the block's readers
// refuse, so the cases that expect a block recorded need this one.
const computeOwnPin = "us-east5-docker.pkg.dev/" + initProject + "/" + infra.BaseRegistry + "/fugaro-base:1.2.3"

// computePinDigest is a release pin with the digest its shape requires, for the
// case that pins the refusal's own wording.
var computePinDigest = "us-east5-docker.pkg.dev/" + initProject + "/" + infra.GhcrRegistry +
	"/acme/fugaro-base:1.2.3@" + cdg([]byte("the release pin"))

// computeBlockRig is the compute rig with a base image the block may name.
func computeBlockRig(t *testing.T) *initRig {
	t.Helper()
	r := newInitRig(t)
	computeReadyWith(t, r, computeOwnPin)
	return r
}

// provisionBatch runs the converge the way the owner does: --provision-batch, a
// terminal, and the project's name on stdin (the typed-only confirmation of a
// step that applies billable infrastructure). Nothing stands in for the
// terminal: no flag confirms this stage (T11), so the fake one is the whole of
// the confirmation.
func provisionBatch(t *testing.T, r *initRig) (string, error) {
	t.Helper()
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProjectName+"\n", "setup", "--provision-batch")
	return out, err
}

// localBatchBlock is the block the machine's project config carries, nil when
// it carries none: read back through the config's own parser rather than
// searched for in the text, so a block that is there but unreadable is a
// failure here and not a pass.
func localBatchBlock(t *testing.T, r *initRig) *localcfg.Batch {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, r.cfg)))
	if err != nil {
		t.Fatalf("%s does not read back after the run: %v", r.cfg, err)
	}
	return lc.Batch
}

// TestComputeStageRecordsTheBatchBlock: the provisioning writes the block, out
// of the root's own outputs, with the fields a Batch run needs and no others.
// What it does NOT write is the selection: `backend:` is `fugaro backends use`'s
// and the stage only names it (asserted below the block, and in
// backends_use_test.go for the command itself).
func TestComputeStageRecordsTheBatchBlock(t *testing.T) {
	r := computeBlockRig(t)
	out, err := provisionBatch(t, r)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b := localBatchBlock(t, r)
	if b == nil {
		t.Fatalf("the provisioning recorded no batch block:\n%s\n%s", readFileString(t, r.cfg), out)
	}
	for _, f := range []struct {
		key, got, want string
	}{
		// The image is the config's own base, which is the one field no root
		// output answers.
		{"image", b.Image, computeOwnPin},
		// The family the preload publishes, as the resource name a run's
		// bootDisk.image takes: the root's own output, which the module
		// derives from the same pin.
		{"boot_image", b.BootImage, "projects/" + initProject + "/global/images/family/" + infra.PreloadedImage(computeOwnPin)},
		{"network", b.Network, infra.DefaultNetwork(initProject)},
		{"subnetwork", b.Subnetwork, infra.DefaultSubnetwork(initProject, "us-east5")},
		// The project's one run account: the block carries one account for
		// every workflow, so it is written only when there is exactly one.
		{"service_account", b.ServiceAccount, "fugaro-acme-web@proj-1234.iam.gserviceaccount.com"},
	} {
		if f.got != f.want {
			t.Errorf("batch.%s = %q, want %q", f.key, f.got, f.want)
		}
	}
	// The fields a root output cannot answer are left OUT rather than filled
	// with a guess: empty means the backend's own default, and the socket gid
	// is a fact about the boot image's build.
	if b.MachineType != "" || b.SocketGID != 0 || b.Secrets != nil || b.TaskTimeout.Set {
		t.Errorf("the stage wrote a field no output answers: %+v", b)
	}
	// The selection is the owner's next command and not this stage's.
	if got := readFileString(t, r.cfg); strings.Contains(got, "backend:") {
		t.Errorf("the stage selected a backend:\n%s", got)
	}
	if lc, err := localcfg.Parse([]byte(readFileString(t, r.cfg))); err != nil || lc.Backend() != backend.CloudRun {
		t.Errorf("the installation does not still run on %s (err %v)", backend.CloudRun, err)
	}
	// And it names the one command that selects it, once the block is there.
	if !strings.Contains(out, "still runs on "+backend.CloudRun+": select the backend with fugaro backends use "+backend.CloudBatch) {
		t.Errorf("the stage did not name the selection command:\n%s", out)
	}
}

// TestComputeStageLeavesAFieldTheOwnerWroteAlone: the merge is field by field
// over the block the machine already has, and the owner's value wins. An
// installation whose owner set a machine type and measured the socket gid --
// and pointed the block at an image of their own -- keeps all three, and the
// fields they left empty are the ones this run fills.
func TestComputeStageLeavesAFieldTheOwnerWroteAlone(t *testing.T) {
	r := computeBlockRig(t)
	r.appendConfig(t, `batch:
  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:9
  machine_type: e2-standard-4
  socket_gid: 989
`)
	out, err := provisionBatch(t, r)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b := localBatchBlock(t, r)
	if b == nil {
		t.Fatalf("the provisioning dropped the owner's block:\n%s\n%s", readFileString(t, r.cfg), out)
	}
	if b.Image != "us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:9" {
		t.Errorf("batch.image = %q, want the image the owner recorded", b.Image)
	}
	if b.MachineType != "e2-standard-4" || b.SocketGID != 989 {
		t.Errorf("the owner's machine_type/socket_gid were overwritten: %+v", b)
	}
	if b.BootImage == "" || b.Network == "" || b.Subnetwork == "" || b.ServiceAccount == "" {
		t.Errorf("the fields the owner left empty were not filled: %+v", b)
	}
}

// TestComputeStageRerunRecordsNothing: a second --provision-batch over an
// installation that already carries the block leaves the file byte-identical.
// The stage reads no outputs on that path (the plan is empty, so there is
// nothing to apply), so the family comes from the pin the config holds, and the
// two have to agree for the file to be left alone -- which is by construction,
// since infra.PreloadedImage is the module's own computation.
func TestComputeStageRerunRecordsNothing(t *testing.T) {
	r := computeBlockRig(t)
	if out, err := provisionBatch(t, r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	before := readFileString(t, r.cfg)
	// Nothing left to apply anywhere: the installation's own converge is done
	// and the compute root's provisioning matches its plan.
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	out, err := provisionBatch(t, r)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The stage's own word for nothing to do, read off the step's line: the
	// stage's longer sentence is the run's account and lands in the log
	// whenever the run keeps one (init_compute_test.go's no-changes case reads
	// it there), while the step's line is on the screen on both paths.
	if !strings.Contains(out, markDone+" compute No changes") {
		t.Errorf("the second run did not report the compute stage's no-changes path:\n%s", out)
	}
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("the second run rewrote the config:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if !strings.Contains(out, "select the backend with fugaro backends use "+backend.CloudBatch) {
		t.Errorf("the no-changes path no longer names the selection command:\n%s", out)
	}
}

// TestComputeStageRecordsNoBlockItCannotFillHonestly: the block is written or
// nothing is, and the line on the terminal says which field stopped it. A block
// naming an image the installation's own registry does not serve is one doctor
// passes and the first run's pull fails on (a pull-through path is a stranger's
// code), so the stage refuses rather than guesses -- and the case a fresh
// installation forgives, the selection line, is not printed at all: the
// selection would be refused for the block that is not there.
//
// The last case fails EARLIER than the block is formed -- a root with no base
// image to build its preload from is refused by the stage's own plan -- which
// is the same answer one step further back: the run stops with a line naming
// what is missing, and no block follows it either.
func TestComputeStageRecordsNoBlockItCannotFillHonestly(t *testing.T) {
	for _, c := range []struct {
		name, pin, extra, want string
		stops                  bool
	}{
		{
			name: "the tag under the remote repository",
			pin:  computePin,
			want: "without being a base this tool pinned for base",
		},
		{
			name:  "the release pin",
			pin:   computePinDigest,
			extra: "image_source: ghcr.io/acme\n",
			want:  "sh images/build-base.sh base",
		},
		{
			name: "a base outside this project's registry",
			pin:  "eu.gcr.io/other/fugaro-base:1.2.3",
			want: "is not an image of this installation's own Artifact Registry",
		},
		{
			name:  "no base image at all",
			pin:   "",
			want:  "needs the installation's pinned base image",
			stops: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newInitRig(t)
			computeReadyWith(t, r, c.pin)
			if c.extra != "" {
				r.appendConfig(t, c.extra)
			}
			out, err := provisionBatch(t, r)
			if !c.stops {
				if err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				if !strings.Contains(out, "the batch block was not recorded") {
					t.Errorf("the terminal does not say why no block was recorded:\n%s", out)
				}
			} else if err == nil {
				t.Fatalf("the run was not stopped before the block:\n%s", out)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("the terminal does not say what stopped the block (want %q):\n%s", c.want, out)
			}
			if b := localBatchBlock(t, r); b != nil {
				t.Fatalf("a block was recorded from %q: %+v", c.pin, b)
			}
			if strings.Contains(readFileString(t, r.cfg), "batch:") {
				t.Errorf("a batch block was written for %q:\n%s", c.pin, readFileString(t, r.cfg))
			}
			if strings.Contains(out, "select the backend with fugaro backends use") {
				t.Errorf("the stage named the selection command for an installation with no block:\n%s", out)
			}
		})
	}
}

// TestComputeStageRecordsNoBlockWhenTheApplyFails: the failing apply is the
// stage's error and the run stops there, so nothing of the config is written --
// the block describes what an apply created, and there is no apply. (The
// failure is scripted into the COMPUTE root alone: a run that never reached it
// would pass this for the wrong reason.)
func TestComputeStageRecordsNoBlockWhenTheApplyFails(t *testing.T) {
	r := computeBlockRig(t)
	r.script["apply@compute"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded\n"}
	r.save(t)
	out, err := provisionBatch(t, r)
	if err == nil {
		t.Fatalf("a failed apply was reported as success:\n%s", out)
	}
	if b := localBatchBlock(t, r); b != nil {
		t.Fatalf("a failed apply still recorded a block: %+v", b)
	}
	if strings.Contains(readFileString(t, r.cfg), "batch:") {
		t.Errorf("the failed run wrote a batch block into the config:\n%s", readFileString(t, r.cfg))
	}
	// The guard: the compute root's own apply is what failed, so the stage that
	// records the block is the one the run stopped in.
	if calls := subcommands(computeCalls(t, r)); !slicesContains(calls, "apply") {
		t.Fatalf("the compute root's apply never ran: %q", calls)
	}
}
