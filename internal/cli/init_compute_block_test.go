package cli

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
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
// The block carries one fact the installation owns and this stage forms no
// answer for -- the opt-in fact, which is carried through -- and it no longer
// carries the facts the release behind wrote: `batch.secrets` and the key fact
// a Batch run's identity needed before the claims service issued it. Those
// leave the machine's file HERE, in the write that records the block, and the
// key is known to this stage only as the word the file spells (R10): the tests
// below hold both the removal and that limit, because BR23's deletion of the
// field, its type and its validation waits on exactly it.
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

// retiredBatchBlock writes the `batch:` block the release before this one left
// on a machine's file: the fields this stage fills, the opt-in fact an operator
// recorded, and the two facts this release retires -- `secrets`, the Secret
// Manager mounts a launch stopped carrying, and `seal_key`, the key fact
// `fugaro update` wrote out of the compute root's own output before a run's
// identity came from the claims service (R10). The caller chooses which of the
// two it carries, so each is pinned on its own mechanism: the mounts leave with
// the block's merge, the key with the write the file's own text forces.
//
// The key is written the way that release wrote it -- a KMS version path and
// the public key as a BLOCK SCALAR -- which is the shape a rewrite has to
// survive: a PEM is multi-line, and a removal that spliced lines out of the
// file would have to know where a block scalar ends.
//
// Every field this stage forms is in it, and the values are the ones it forms
// (the root's own outputs): the block the file holds is the block a converge
// would write, so `sameBatch` says the two are the same and nothing but a
// retired fact's NAME in the text can be what makes the write happen. A fixture
// missing one of them would leave the merge itself forcing the write, and the
// text scan every removal above rests on could be deleted with these tests
// still green.
func retiredBatchBlock(t *testing.T, r *initRig, secrets, key bool) {
	t.Helper()
	block := "batch:\n" +
		"  image: " + computeOwnPin + "\n" +
		"  boot_image: projects/" + initProject + "/global/images/family/" + infra.PreloadedImage(computeOwnPin) + "\n" +
		"  network: " + infra.DefaultNetwork(initProject) + "\n" +
		"  subnetwork: " + infra.DefaultSubnetwork(initProject, updateBatchRegion) + "\n" +
		"  service_account: fugaro-acme-web@proj-1234.iam.gserviceaccount.com\n" +
		"  opt_in: " + localcfg.BatchOptInOn + "\n"
	if secrets {
		block += "  secrets:\n    FUGARO_RUN_TOKEN: projects/" + initProject + "/secrets/run-token\n"
	}
	if key {
		version, publicKey := sealKeyFact(t)
		block += "  seal_key:\n    version: " + version + "\n    public_key: |\n" + indentLines(publicKey, "      ")
	}
	r.appendConfig(t, block)
}

// updateRigOnTheReleaseBehind is the update door's rig on an installation an
// earlier release provisioned: the block that release wrote, the operator whose
// update converges it, and a compute root with a state of its own.
//
// The caller gives the root's plan: a change, which is the converge this stage
// is registered for, or nothing at all, which is an installation whose root is
// already what the plan would make it -- the path with no apply on it, and the
// one a converged installation takes the block's write through.
func updateRigOnTheReleaseBehind(t *testing.T, secrets, key bool, changes ...planChange) *initRig {
	t.Helper()
	r := updateRig(t)
	computeReadyWith(t, r, computeOwnPin)
	updateBatchOperatorOn(t, r)
	computeProvisioned(t, r)
	if len(changes) > 0 {
		computePlans(t, r, changes...)
	}
	retiredBatchBlock(t, r, secrets, key)
	return r
}

// backupHolding is the backup the update's write left, as the local config's own
// writer names them (<file>.bak-<UTC>): the file that carried the retired fact,
// kept beside the new one. A converge writes the config more than once, so the
// caller asks for the backup that holds the fact it means.
func backupHolding(t *testing.T, r *initRig, want string) string {
	t.Helper()
	found := ""
	for _, f := range backupsOf(t, r) {
		if strings.Contains(readFileString(t, f), want) {
			found = f
		}
	}
	return found
}

// backupsOf is every backup the update's writes left beside the machine's file.
func backupsOf(t *testing.T, r *initRig) []string {
	t.Helper()
	backups, err := filepath.Glob(r.cfg + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	return backups
}

// publishedBucketReady points the two seams a shared-config read reaches -- the
// opener the publish writes through, and the fetch a machine with no local
// config reads through -- at a directory of this test's own, and writes the
// project marker every read checks before the object. A bucket of its own
// rather than the package's own memory bucket, so what a test publishes is what
// it reads back.
func publishedBucketReady(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	prevOpen, prevFetch := sharedBucketOpener, sharedFetch
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+dir)
	}
	sharedFetch = func(ctx context.Context, _ func(string) string, _ time.Time, name, gcp string) (*localcfg.Config, string, error) {
		c, _, _, err := readSharedFromBucket(ctx, SharedAnchor{Name: name, GCPProject: gcp, Bucket: "fugaro-runs-" + gcp})
		return c, "", err
	}
	t.Cleanup(func() { sharedBucketOpener, sharedFetch = prevOpen, prevFetch })
	ctx := context.Background()
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: initProjectName, GCPProject: initProject})
	if err != nil {
		t.Fatal(err)
	}
	b, err := blobx.Open(ctx, "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Put(ctx, infra.ProjectMarkerObject, marker, "application/json"); err != nil {
		t.Fatal(err)
	}
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

// TestComputeStageKeepsTheOptInFactAndDropsTheKeyFact: the opt-in fact is the
// installation's own -- an operator records it by selecting the backend (R2) --
// and the stage forms its block out of the root's outputs alone, so it has no
// answer for it and carries it through. A converge that recorded its own block
// without it would delete it from this machine's file and from the published
// copy, which is the one thing that field may not suffer.
//
// The key fact is the other way round: it belongs to a release behind, no
// reader wants it, and the write that records the block is what takes it out
// (R10). It is pinned on the FILE's own bytes -- the key's name, and the public
// key's own text below it -- because the stage knows the fact only as the word
// the config file spells.
func TestComputeStageKeepsTheOptInFactAndDropsTheKeyFact(t *testing.T) {
	r := computeBlockRig(t)
	version, publicKey := sealKeyFact(t)
	r.appendConfig(t, "batch:\n  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:9\n  opt_in: in\n"+
		"  seal_key:\n    version: "+version+"\n    public_key: |\n"+indentLines(publicKey, "      "))
	out, err := provisionBatch(t, r)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := readFileString(t, r.cfg)
	b := localBatchBlock(t, r)
	if b == nil {
		t.Fatalf("the provisioning dropped the owner's block:\n%s\n%s", got, out)
	}
	if b.OptIn != localcfg.BatchOptInOn {
		t.Errorf("batch.opt_in = %q, want %q: the converge dropped the operator's fact", b.OptIn, localcfg.BatchOptInOn)
	}
	if strings.Contains(got, "seal_key") || strings.Contains(got, "BEGIN PUBLIC KEY") {
		t.Errorf("the converge left the key fact in the file:\n%s", got)
	}
	// The fields a root output answers are still filled, the fields no output
	// answers are still left out rather than guessed, and the owner's own value
	// for the image is still the one that stays.
	if b.BootImage == "" || b.Network == "" || b.Subnetwork == "" || b.ServiceAccount == "" {
		t.Errorf("the fields the owner left empty were not filled: %+v", b)
	}
	if b.MachineType != "" || b.SocketGID != 0 {
		t.Errorf("the stage wrote a field no output answers: %+v", b)
	}
	if b.Image != "us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:9" {
		t.Errorf("batch.image = %q, want the image the owner recorded", b.Image)
	}
}

// TestSameBatchComparesTheOptInFactAndTheSecrets: two blocks that differ in
// either fact the file may still carry are not the same block, so the stage's
// own "already what this run would write" answer cannot be reached by ignoring
// them -- a comparison that did would let a converge report a block unchanged
// while the file it holds says something else about the installation. The
// retired mounts are the case that matters: they are gone from the merged block
// whatever the file holds, and this comparison is the whole of what makes the
// write happen for a machine whose block is otherwise current.
func TestSameBatchComparesTheOptInFactAndTheSecrets(t *testing.T) {
	base := localcfg.Batch{Image: "image", OptIn: localcfg.BatchOptInOn}
	same := base
	withdrawn := localcfg.Batch{Image: "image", OptIn: localcfg.BatchOptInWithdrawn}
	absent := localcfg.Batch{Image: "image"}
	mounted := localcfg.Batch{Image: "image", OptIn: localcfg.BatchOptInOn,
		Secrets: map[string]string{"FUGARO_RUN_TOKEN": "projects/" + initProject + "/secrets/run-token"}}
	if !sameBatch(&base, &same) {
		t.Error("two blocks carrying the same facts are not the same block")
	}
	if sameBatch(&base, &withdrawn) {
		t.Error("an opted-in block and a withdrawn one are the same block")
	}
	if sameBatch(&base, &absent) {
		t.Error("a block carrying the opt-in fact and one that carries none are the same block")
	}
	if sameBatch(&base, &mounted) {
		t.Error("a block still mounting a secret and one that mounts nothing are the same block: the mounts are retired and the write is what removes them (R10)")
	}
}

// sealKeyFact is a key fact for a test config: a throwaway 2048-bit RSA public
// key generated here -- public by construction and nobody's (Convention 2) --
// and the version line `fugaro update` writes beside it.
func sealKeyFact(t *testing.T) (version, publicKey string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	version = "projects/" + initProject + "/locations/us-central1/keyRings/fugaro-batch/cryptoKeys/seal-key/cryptoKeyVersions/1"
	return version, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// indentLines prefixes every non-blank line, so a PEM lands in the block scalar
// at the indentation the key fact's `public_key` field carries.
func indentLines(s, prefix string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString(line)
			continue
		}
		b.WriteString(prefix + line)
	}
	return b.String()
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

// TestUpdateRemovesTheKeyFactInPlace: `fugaro update` takes the key fact a
// release behind wrote out of this machine's file, where it stands, and the
// write that does it is the block's own (R10, the acceptance's own case). The
// fact is dropped BY NAME AS TEXT -- the writer names neither its field nor its
// type -- so the removal is pinned on the file's own bytes: the key's name and
// the public key's text below it are gone, the opt-in fact and the rest of the
// block stand, and the file the release behind wrote is kept beside it as the
// backup every local-config write makes.
func TestUpdateRemovesTheKeyFactInPlace(t *testing.T) {
	r := updateRigOnTheReleaseBehind(t, false, true, launcherGrantChange(updateBatchOperator))
	before := readFileString(t, r.cfg)
	if !strings.Contains(before, "seal_key") || !strings.Contains(before, "BEGIN PUBLIC KEY") {
		t.Fatalf("the fixture did not write the key fact:\n%s", before)
	}
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	after := readFileString(t, r.cfg)
	if strings.Contains(after, "seal_key") {
		t.Errorf("the update left the key fact in the file:\n%s", after)
	}
	// The whole fact and not just its name: the key version's own path, which no
	// other field of the file spells, and the public key below it.
	if strings.Contains(after, "BEGIN PUBLIC KEY") || strings.Contains(after, "keyRings") {
		t.Errorf("the update left the key fact's own values in the file:\n%s", after)
	}
	if !strings.Contains(after, "opt_in: "+localcfg.BatchOptInOn) {
		t.Errorf("the update dropped the operator's opt-in fact:\n%s", after)
	}
	b := localBatchBlock(t, r)
	if b == nil || b.OptIn != localcfg.BatchOptInOn || b.Image != computeOwnPin || b.ServiceAccount == "" {
		t.Errorf("the block the update left is not the installation's: %+v", b)
	}
	// In place, and not in place of the file: the removal is a write of the
	// local config, which backs the old one up before it replaces it.
	if got := backupHolding(t, r, "seal_key"); got == "" {
		t.Errorf("the update removed the key fact without keeping the file it was in: %v", backupsOf(t, r))
	}
}

// TestUpdateRemovesTheKeyFactWithNothingToApply: the block is written on the
// path with no plan and no apply on it too. An installation whose compute root
// is already what `terraform plan` would make it is the ordinary converged one,
// and it is where the block's write is the ONLY thing the step does -- the
// apply's own call would leave a machine that has converged before this release
// holding the key fact for good. The no-changes line is asserted with it, so the
// case cannot pass by having taken the apply's path by accident.
func TestUpdateRemovesTheKeyFactWithNothingToApply(t *testing.T) {
	r := updateRigOnTheReleaseBehind(t, false, true)
	before := readFileString(t, r.cfg)
	if !strings.Contains(before, "seal_key") || !strings.Contains(before, "BEGIN PUBLIC KEY") {
		t.Fatalf("the fixture did not write the key fact:\n%s", before)
	}
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if calls := subcommands(computeCalls(t, r)); slicesContains(calls, "apply") {
		t.Fatalf("an empty plan was applied: %q", calls)
	}
	// The stage's own word for nothing to do is the run's account, so it is the
	// log's and the screen is the step's line (init_compute_test.go holds the
	// same sentence on the provisioning's own path).
	if all := out + newestLog(t, "update"); !strings.Contains(all, "No changes: the compute backend's provisioning matches the plan.") {
		t.Fatalf("the update did not take the no-changes path this case is about:\n%s", all)
	}
	after := readFileString(t, r.cfg)
	if strings.Contains(after, "seal_key") || strings.Contains(after, "BEGIN PUBLIC KEY") {
		t.Errorf("the update left the key fact in the file with nothing to apply:\n%s", after)
	}
	b := localBatchBlock(t, r)
	if b == nil {
		t.Fatalf("the update dropped the block:\n%s", after)
	}
	if b.OptIn != localcfg.BatchOptInOn || b.Image != computeOwnPin ||
		b.BootImage == "" || b.Network == "" || b.Subnetwork == "" || b.ServiceAccount == "" {
		t.Errorf("the block the update left is not the installation's: %+v", b)
	}
	if got := backupHolding(t, r, "seal_key"); got == "" {
		t.Errorf("the update removed the key fact without keeping the file it was in: %v", backupsOf(t, r))
	}
}

// TestUpdateRemovesBatchSecretsInPlace: the Secret Manager mounts a launch
// stopped carrying leave the machine's file in the same update (R10). They go
// by the block's own merge -- which no longer copies them -- and not by the
// key's text-level rewrite, so this is the case that pins the second mechanism:
// the mapping, the variable name and the secret's own resource are gone, the
// block reads back, and the installation's own facts stand.
func TestUpdateRemovesBatchSecretsInPlace(t *testing.T) {
	r := updateRigOnTheReleaseBehind(t, true, false, launcherGrantChange(updateBatchOperator))
	before := readFileString(t, r.cfg)
	if !strings.Contains(before, "FUGARO_RUN_TOKEN") {
		t.Fatalf("the fixture did not write the retired mounts:\n%s", before)
	}
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	after := readFileString(t, r.cfg)
	for _, gone := range []string{"secrets:", "FUGARO_RUN_TOKEN", "/secrets/run-token"} {
		if strings.Contains(after, gone) {
			t.Errorf("the update left the retired mounts in the file (%q):\n%s", gone, after)
		}
	}
	b := localBatchBlock(t, r)
	if b == nil {
		t.Fatalf("the update dropped the block:\n%s", after)
	}
	if b.Secrets != nil {
		t.Errorf("the block still mounts a secret: %v", b.Secrets)
	}
	if b.OptIn != localcfg.BatchOptInOn || b.Image != computeOwnPin || b.ServiceAccount == "" || b.BootImage == "" {
		t.Errorf("the update left a block that is not the installation's: %+v", b)
	}
}

// TestNoKeyFactIsEverWritten: the block this stage writes names no key fact,
// and neither does the code that writes it. The second half is what makes
// BR23's deletion land -- it takes the field, its type, its validation and its
// doc out of the tree, and a writer that named any of them would be a file that
// deletion has to reach into -- so the pin is the writer's own source: the key
// is the text its config file spells, and nothing else of it.
func TestNoKeyFactIsEverWritten(t *testing.T) {
	r := computeBlockRig(t)
	if out, err := provisionBatch(t, r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, r.cfg); strings.Contains(got, "seal_key") {
		t.Errorf("the stage wrote a block naming a key fact:\n%s", got)
	}
	const src = "init_compute.go"
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	code := string(data)
	for _, name := range []string{"SealKey", "sealKey"} {
		if strings.Contains(code, name) {
			t.Errorf("%s names %s: this file knows the retired key only as the text its config file spells, or BR23's deletion has to reach into it", src, name)
		}
	}
	if n := strings.Count(code, `"seal_key"`); n != 1 {
		t.Errorf("%s names the retired key as text %d times, want the one list of retired facts", src, n)
	}
}

// TestALaunchReadsTheOptInFromThePublishedBlock: the update publishes the block
// it recorded, opt-in fact and all, and a machine with no local config reads
// the fact there -- which is the read a launch makes (R15, R16's launches
// clause). The published object is read back through the readers' own path
// (readSharedFromBucket), so a block the update published but no machine could
// read is a failure here and not a pass, and the machine's own file is the
// first case of the same fact.
func TestALaunchReadsTheOptInFromThePublishedBlock(t *testing.T) {
	r := updateRigOnTheReleaseBehind(t, true, true, launcherGrantChange(updateBatchOperator))
	publishedBucketReady(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	ctx := context.Background()
	anchor := SharedAnchor{Name: initProjectName, GCPProject: initProject, Bucket: "fugaro-runs-" + initProject}
	published, _, data, err := readSharedFromBucket(ctx, anchor)
	if err != nil {
		t.Fatalf("the update published no shared config: %v", err)
	}
	if published.Batch == nil || published.Batch.OptIn != localcfg.BatchOptInOn {
		t.Fatalf("the published block carries %+v, want the opt-in fact", published.Batch)
	}
	if strings.Contains(string(data), "seal_key") || strings.Contains(string(data), "FUGARO_RUN_TOKEN") {
		t.Errorf("the update published the retired facts:\n%s", data)
	}
	// The machine that holds the block: the fact is on its own file.
	if b := localBatchBlock(t, r); b == nil || b.OptIn != localcfg.BatchOptInOn {
		t.Errorf("the machine's own block carries no opt-in fact: %+v", b)
	}
	// And the machine that holds none: the launch reads the published copy, and
	// what it reads there passes the check the launch's own gate stands on.
	// isolateProjects empties this machine's own config directories, which is
	// what makes the published copy the one the selection has to reach for.
	isolateProjects(t, t.TempDir())
	sel, lc, err := selectProject(ctx, cloudOptions{project: initProjectName, gcpProject: initProject,
		stderr: func() io.Writer { return io.Discard }})
	if err != nil {
		t.Fatalf("a machine with no local config could not read the installation: %v", err)
	}
	if sel.From != "shared config" {
		t.Fatalf("the launch read %s, want the published copy", sel.From)
	}
	if lc.Batch == nil || lc.Batch.OptIn != localcfg.BatchOptInOn {
		t.Fatalf("the launch read a block with no opt-in fact: %+v", lc.Batch)
	}
	if check := doctorBatchOptIn(lc); !check.OK {
		t.Errorf("the launch's own check refuses the published fact: %s", check.Problem)
	}
}

// brUpdateReleaseNotes is the notes of the release this work ships in, from
// docs/release-streams.yaml (current 0.28.0, so 0.29.0 "Zander" is next). A
// feature writes its bullets there before the cut, and the after-deploy steps
// of docs/batch-run-identity's plan go under its `### For operators` (the
// plan's §9); when the release is cut this constant moves with the next one.
const brUpdateReleaseNotes = "../../docs/releases/v0.29.0.md"

// TestTheOperatorStepIsRunUpdate: the release notes carry the two halves this
// work owes them, and the boundary between them: the operators' section says to
// run `fugaro update` -- the one command the upgrade is, and the one that
// writes the block and takes the retired facts out of a machine's file -- while
// the feature notes say how Batch is chosen, because an operator's selection is
// an opt-in to a feature and not a step of the upgrade (the plan's §9). The
// selection is not named under `### For operators` at all: a step there is
// something an upgrade needs, and no installation needs this one.
func TestTheOperatorStepIsRunUpdate(t *testing.T) {
	data, err := os.ReadFile(brUpdateReleaseNotes)
	if err != nil {
		t.Fatalf("reading %s: %v (a feature writes its bullets into the notes of the release it ships in, before the cut)", brUpdateReleaseNotes, err)
	}
	notes := string(data)
	at := strings.Index(notes, "### For operators")
	if at < 0 {
		t.Fatalf("%s carries no `### For operators` section, which is where this work's after-deploy step goes:\n%s", brUpdateReleaseNotes, notes)
	}
	operators := notes[at:]
	if !strings.Contains(operators, "Run `fugaro update`") {
		t.Errorf("%s's `### For operators` does not say to run the one command the upgrade is:\n%s", brUpdateReleaseNotes, operators)
	}
	if strings.Contains(operators, "fugaro backends use") {
		t.Errorf("%s's `### For operators` carries the selection, which is a feature and not a step of the upgrade:\n%s", brUpdateReleaseNotes, operators)
	}
	if !strings.Contains(notes, "fugaro backends use "+backend.CloudBatch) {
		t.Errorf("%s does not say how Batch is chosen (`fugaro backends use %s`):\n%s", brUpdateReleaseNotes, backend.CloudBatch, notes)
	}
	// What the step is FOR, in the operators' own words: the block this release
	// writes, the fact it keeps, and the two it retires in place.
	for _, want := range []string{"batch:", "opt-in", "secrets"} {
		if !strings.Contains(operators, want) {
			t.Errorf("%s's `### For operators` does not say what the update does to an installation's block (want %q):\n%s", brUpdateReleaseNotes, want, operators)
		}
	}
}
