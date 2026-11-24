package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
)

// batchBlockForLaunch is the `batch:` block an installation that selected the
// backend carries (init_compute writes it on a provisioning apply), enough of
// it for the backend to open: every field CheckOptions requires, pointed at no
// cloud at all. The launches below refuse before any Batch call is made, so no
// endpoint of the block is ever asked anything -- what the block is here for is
// that the launch opens the backend the config selects and reaches the gates
// with one, which is where the refusal lives.
const batchBlockForLaunch = "backend: cloud-batch\n" +
	"batch:\n" +
	"  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1\n" +
	"  machine_type: e2-standard-4\n" +
	"  boot_image: projects/proj-1234/global/images/family/fugaro-base\n" +
	"  network: projects/proj-1234/global/networks/fugaro\n" +
	"  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro\n" +
	"  service_account: fugaro-runs@proj-1234.iam.gserviceaccount.com\n"

// TestAnUnservedRepositoryIsRefusedOnBatchWithItsFix is BR11's launcher half
// (D3104): a Batch job mounts nothing, so a launch on the Batch backend whose
// repository the credentials service does not serve is refused before anything
// of it is created, with the fix that changes that. The run such a launch would
// start holds no model credential at all -- not the repository's own, which
// nothing mounts, and not the service's, which this installation cannot reach
// for it -- and it would hold none silently, failing at its first model call
// with a Task already created, a claim already taken and a token already
// minted.
//
// The rows are the switch's conditions taken one at a time, because the
// refusal is one sentence for all of them and a gate that judged any one of
// them wrongly would be a launch refused that the service serves, or admitted
// that it does not. What makes the refusal the gate's and not a launch's own is
// the Cloud Run row: the same installation, the same repository and the same
// unserved pair are a launch Cloud Run admits, because a Cloud Run run's job
// carries its own mounts where the service does not serve it (G17). The gate is
// called directly, the way the Batch half of the proxy gate is (G14) and for
// the same reason: what refuses is a fact about the backend and the switch, and
// a launch that reaches it needs a whole fixture (a build record, an image, a
// job) that has nothing to do with it.
//
// Mutation (drop the gate's second half, or its backend comparison): the first
// two rows launch, or every Cloud Run launch of an unserved repository is
// refused with it.
func TestAnUnservedRepositoryIsRefusedOnBatchWithItsFix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		block    string
		switchOn bool
		be       backend.Codec
		refused  bool
	}{
		{"an installation that carries no credentials service, on Batch", "", true, backend.BatchCodec(), true},
		{"a repository whose record says its root was not applied with the switch on, on Batch", proxiedBlock, false, backend.BatchCodec(), true},
		{"a repository the service serves, on Batch", proxiedBlock, true, backend.BatchCodec(), false},
		{"the same unserved repository, on Cloud Run", "", true, backend.CloudRunCodec(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			addProxiedRepo(t, f, releasePolicy, tc.block, tc.switchOn)
			publishPolicy(t, f, proxiedPolicy)
			_, spec := proxiedLaunchSpec()
			env := fileEnv(t, f.cloudFixture)
			env.be = backendNamed{codec: tc.be}
			err := batchLaunchRefusal(context.Background(), env, spec)
			switch {
			case tc.refused && err == nil:
				t.Fatal("a Batch launch of a repository the credentials service does not serve was admitted: it would start a run holding no model credential at all (D3104)")
			case tc.refused:
				if ExitCode(err) != ExitUserError {
					t.Errorf("the refusal is not a user error: %v", err)
				}
				for _, want := range []string{backend.CloudBatch, "credentials service", spec.Repo, selfCommand() + " update", "cloud-run"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q: %v", want, err)
					}
				}
			case err != nil:
				t.Fatalf("a launch the service serves, or one on another backend, was refused: %v", err)
			}
		})
	}
}

// TestTheRefusalComesBeforeTheMint is the other half of BR11's launcher
// acceptance: the refusal is a gate, so it comes before the claim and before
// the budget token is minted -- a launch refused for holding no credential must
// not have spent one. It runs the command a person runs, on an installation
// whose config selects the Batch backend, and holds the two things the order is
// made of: nothing was minted (the IAM signer was never asked, and the bucket
// holds no token object) and nothing was launched (no execution, no stored
// task). A gate moved below the mint would fail here and nowhere else.
//
// Mutation (drop the call, or move it out of freshLaunchGates and into the
// launch after the mint): the launch goes on, and the assertions below are
// what fails -- the signer was asked and the bucket holds a token object.
func TestTheRefusalComesBeforeTheMint(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.appendConfig(t, batchBlockForLaunch)
	_, _, err := runBudget(t)
	if ExitCode(err) != ExitUserError || err == nil {
		t.Fatalf("a Batch launch of a repository the credentials service does not serve: err = %v, want a refusal", err)
	}
	for _, want := range []string{backend.CloudBatch, "credentials service", selfCommand() + " update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Errorf("the refusal minted a budget token: %d signing call(s) reached the IAM fake", n)
	}
	if tok := readToken(t, f.cloudFixture, budgetRun); tok != "" {
		t.Error("a refused launch wrote a token object into the bucket")
	}
	if launchedAny(f) {
		t.Error("the refused launch started an execution")
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored a task")
	}
}
