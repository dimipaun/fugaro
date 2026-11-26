package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// update_reconcile_converge_test.go is N2 of docs/config-ownership/implementation-plan.md:
// the converge test the HARD RULE requires for the reconcile N4 writes into the
// published policy node, and the whole of what that reconcile owes it — no
// migration code at all.
//
// **What the tests pin is the sentence the HARD RULE is about**: an installation
// at the previous version reaches this one with `fugaro update` alone, and the
// run that carries the node forward is also the run that reconciles what the
// operator's local config states into it. The fixture is that installation: the
// published node the release behind left (testdata/node_reconcile/node.json,
// version 7, carrying the craft family that release wrote) and the local config
// of the same machine (testdata/node_reconcile/config.yaml), which answers the
// craft family differently in the three ways the acceptance names.
//
// The three answers are one assertion each, and each one is a rule of the
// reconcile's:
// the pool the node carries and the file does not state survives the run with
// its owner intact (a silence is not a removal, so the first reconcile deletes
// nothing), the pool both carry keeps the node's legacy-open marker (a written
// value wins field by field, and a file is never the marker's source — D2608),
// and the ownerless pool the file states reaches no node and is named in one
// line rather than failing the run (ValidatePublishedPools never sees it).
//
// The two documents are fixture files and not strings in Go, the reason every
// converge fixture in this package is one: a config assembled from this tree's
// own helpers would converge whatever this tree happens to write today.
//
// **The red step is the pool merge itself, and the naming line is what carries
// it**: with the node build handing the node's own `craft_pools` back instead of
// merging the file's (the shape F5 names), the run finds no `craft_pools` drift
// to publish, so it writes no node, names `acme-loose` in no line, and the first
// test fails there. The merge itself landed with MC18 (#2881, docs/model-crafts)
// rather than with N4, whose own additions to this half are the `node_removals`
// unset this fixture does not state: against a tree that already carries it,
// this file is the converge coverage the HARD RULE requires rather than a test
// that was red the day it was written.
const (
	nodeReconcileNodeFixture   = "testdata/node_reconcile/node.json"
	nodeReconcileConfigFixture = "testdata/node_reconcile/config.yaml"
)

// The fixture's three pools, by the part each one plays in the acceptance.
const (
	// nodeReconcileNodePool is the pool the node carries and the local config
	// does not state: silence keeps it, owner and all.
	nodeReconcileNodePool = "node-pool"
	// nodeReconcileNodeOwner is the owner the node's copy of it names.
	nodeReconcileNodeOwner = "owner@example.com"
	// nodeReconcileLoosePool is the pool both carry: legacy_open in the node,
	// and the file states neither that nor an owner.
	nodeReconcileLoosePool = "loose-pool"
	// nodeReconcileOwnerless is the pool the file states with no owner and the
	// node has no copy of: left out, and named.
	nodeReconcileOwnerless = "acme-loose"
)

// nodeReconcileStale is what makes the fixture an installation of the release
// behind rather than a current one: the deployed security rules carry the
// previous generation's marker and the checkout carries that release's plugin
// pin. The published node is version 7, one below this binary's, so the same
// run's policy stage carries it forward — and that is the write the reconcile
// rides on.
var nodeReconcileStale = []string{"policy-rules-skew", "repo-plugin-pin"}

// nodeReconcileRig is the installation the release behind left: the converge rig
// over the fixture's node, with the fixture's local config appended to the
// config the door reads.
//
// The config goes in before registerInstallation takes the copy the door reads:
// the rig clears FUGARO_CONFIG, so the local config `fugaro update` reads and
// writes is the one this rig left in the XDG projects directory.
func nodeReconcileRig(t *testing.T) *convergeRig {
	t.Helper()
	// Both documents are read before the rig is built, and carried rather than
	// re-read: the rig chdirs into the checkout it made (t.Chdir), so a testdata
	// path spelled relatively stops naming this package's directory the moment
	// the rig exists.
	cfg := string(readFile(t, nodeReconcileConfigFixture))
	var node map[string]any
	if err := json.Unmarshal(readFile(t, nodeReconcileNodeFixture), &node); err != nil {
		t.Fatalf("%s is not the published node it stands for: %v", nodeReconcileNodeFixture, err)
	}
	r := newConvergeRig(t, node)
	r.appendConfig(t, cfg)
	registerInstallation(t, r.initRig)
	return r
}

// nodeReconcilePools is the published node's craft_pools block by name: the
// shape the acceptance's three answers are read in.
func nodeReconcilePools(t *testing.T, r *convergeRig) map[string]config.Pool {
	t.Helper()
	out := make(map[string]config.Pool)
	for _, p := range convergeNode(t, r).CraftPools {
		out[p.Name] = p
	}
	return out
}

// nodeReconcileRaw is the node as the database holds it, byte for byte: what a
// second run of the door may not move.
func nodeReconcileRaw(t *testing.T, r *convergeRig) string {
	t.Helper()
	b, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUpdateReconcilesFromThePreviousVersion: the installation of the release
// behind ends after one `fugaro update` with the node's own pool kept, the
// pool the node marks legacy_open still marked, the ownerless pool of the local
// config in no node and named in one line, and nothing deleted — the door the
// HARD RULE allows is one command, and it may not stop over a pool it cannot
// publish.
func TestUpdateReconcilesFromThePreviousVersion(t *testing.T) {
	r := nodeReconcileRig(t)
	out := convergeOn(t, r, nodeReconcileStale)

	if v, want := storedNodeVersion(t, r), projectpolicy.Version; v != want {
		t.Errorf("after one fugaro update the published policy is version %d, want %d", v, want)
	}
	pools := nodeReconcilePools(t, r)
	// The pool the file does not state is the node's, whole: the file's silence
	// is not a removal, and the first reconcile deletes no member it was not
	// asked about (the merge's own rule, D2607/F22, and the reason a config that
	// predates the key keeps a project whose runs resolve their crafts as they
	// did).
	held, ok := pools[nodeReconcileNodePool]
	if !ok {
		t.Fatalf("the run deleted %s, the pool the node carried and the local config states nothing about: the node's pools are %+v", nodeReconcileNodePool, pools)
	}
	if held.Owner != nodeReconcileNodeOwner || !slices.Equal(held.Members, []string{"team-a"}) {
		t.Errorf("the node's %s reads %+v after the run, want the copy it carried: owner %s, members [team-a]", nodeReconcileNodePool, held, nodeReconcileNodeOwner)
	}
	// The pool both carry: the file states its name and its members and neither
	// of the two members that answer for it, so the node's marker is the one the
	// run writes back — a file is never the marker's source (D2608).
	loose, ok := pools[nodeReconcileLoosePool]
	if !ok {
		t.Fatalf("the run deleted %s, the pool the node carried and the file states: the node's pools are %+v", nodeReconcileLoosePool, pools)
	}
	if !loose.LegacyOpen || loose.Owner != "" {
		t.Errorf("%s reads %+v after the run, want the node's own copy: legacy_open true and no owner, which is what the file's silence about both keeps", nodeReconcileLoosePool, loose)
	}
	// The ownerless pool of the file reaches no node — an ownerless published
	// pool is one this Fugaro refuses to read back, so it is left out rather
	// than written — and the run names it instead of failing on it.
	if got, ok := pools[nodeReconcileOwnerless]; ok {
		t.Errorf("the ownerless pool of the local config reached the published node: %+v, want no node to carry it (config.ValidatePublishedPools refuses one)", got)
	}
	if want := "craft_pools " + nodeReconcileOwnerless + " not published: owner is required"; !strings.Contains(out, want) {
		t.Errorf("the run left the ownerless pool out in silence, want the line %q:\n%s", want, out)
	}
	// Nothing is deleted, in this block or the two beside it: the acceptance's
	// sentence is about the run as a whole, and a reconcile that published the
	// file's pools by replacing the block would take the node's credentials and
	// crafts with it.
	if len(pools) != 2 {
		t.Errorf("the published node holds %d pools after the run, want the two it carried: the first reconcile deletes nothing", len(pools))
	}
	p := convergeNode(t, r)
	if len(p.CraftCredentials) != 2 {
		t.Errorf("the published node's craft_credentials are %+v after the run, want the two it carried", p.CraftCredentials)
	}
	if p.Crafts == nil || p.Crafts.Coder.Access != nodeReconcileNodePool {
		t.Errorf("the published node's crafts read %+v after the run, want the coder craft on %s, the pool it named", p.Crafts, nodeReconcileNodePool)
	}
}

// TestUpdateReconcileIsIdempotent: the node `fugaro update` leaves behind is one
// the reconcile has nothing left to do to, so a second run of the door over what
// the first left publishes nothing and moves not one byte of the node.
//
// The door IS run twice here, unlike the two idempotence tests this package
// already has (TestUpdateIsIdempotent, TestUpdateIsIdempotentForTheModelHalf):
// those read the file the run wrote, because this rig's second run stops at the
// repository stage over the tree the first one left — its own, correct refusal.
// What the person does with that line is what this test does between the two
// runs: commit what the door wrote, so the checkout is the default branch's
// again. The second run then reaches the policy stage, and that stage's own
// check is the assertion: the node carries what the local config states, so it
// reports the published policy at this version and publishes nothing.
func TestUpdateReconcileIsIdempotent(t *testing.T) {
	r := nodeReconcileRig(t)
	convergeOn(t, r, nodeReconcileStale)
	before := nodeReconcileRaw(t, r)

	testutil.Git(t, r.root, "add", "-A")
	testutil.Git(t, r.root, "commit", "-q", "-m", "what the first fugaro update wrote")
	fetched(t, r.root)

	out, errOut, err := executeStdin(t, "aurora\n", "update")
	if err != nil {
		t.Fatalf("the second fugaro update of an installation the first had just converged stopped at %s (exit %d):\n%s\n%s", stoppedStage(out), ExitCode(err), out, errOut)
	}
	if after := nodeReconcileRaw(t, r); after != before {
		t.Errorf("the second run rewrote the published node:\n%s\nwant the node the first run left:\n%s", after, before)
	}
	// The stage's own line, which is what the person read: the node is at this
	// version and carries what the file states, so there is nothing to publish.
	if want := fmt.Sprintf("the published policy is version %d", projectpolicy.Version); !strings.Contains(out, want) {
		t.Errorf("the second run's policy stage does not report the node current (%q), so this run proves nothing about a reconcile with nothing left to do:\n%s", want, out)
	}
	for _, line := range []string{
		"craft_pools " + nodeReconcileOwnerless + " not published",
		"the local config states what the node does not carry",
	} {
		if strings.Contains(out+errOut, line) {
			t.Errorf("the second run published something (%q), want a run that writes no node:\n%s", line, out)
		}
	}
}

// TestUpdateReconcileLeavesARunInFlightRunning is the plan's own pin for this
// reconcile: the requirement of 2026-11-09, over an installation whose local
// config states pools of its own. The reconcile rewrites the node every run of
// the project reads, so this is the update that had the most to reach a run in
// flight — and what the test asserts is that it reaches none of it: the run is
// named once and asked nothing, and every write it makes afterwards still lands.
func TestUpdateReconcileLeavesARunInFlightRunning(t *testing.T) {
	r := nodeReconcileRig(t)
	run := startRunInFlight(t, r)

	out := convergeOn(t, r, nodeReconcileStale)

	if line := inFlightLine(t, out); !strings.Contains(line, "1 run in flight") {
		t.Errorf("the update's line does not say how many runs are running over a project whose local config states pools of its own: %q", line)
	}
	// The run in flight is not a reason to hold the reconcile back: the node is
	// this release's, and the pool the file does not state is still the node's.
	if v, want := storedNodeVersion(t, r), projectpolicy.Version; v != want {
		t.Errorf("the published policy is version %d after the update, want %d: a run in flight held the node where it was", v, want)
	}
	if _, ok := nodeReconcilePools(t, r)[nodeReconcileNodePool]; !ok {
		t.Errorf("the run in flight cost the node its own pool: %+v", nodeReconcilePools(t, r))
	}
	run.laterWrites(t, r)
}
