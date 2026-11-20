//go:build firebase

package rules

// The credential route against a real rules engine (workspace-service task
// WS22, decisions D2980-D2982, resolutions R3 and R4).
//
// The route record is the member's own answer to "which service serves this
// repository's model, GitHub and Bitbucket credentials" (C2, the wire contract:
// internal/creds/route.go). The member's `fugaro update` publishes it at
// config/policy/route/<budget.Key(slug)>, and a run of that repository reads it
// once at bootstrap and mints an identity token for whatever address it names
// (WS11). The rules are the half that decides who may read it — a run's own
// token, and no one else — and they are the only place that decision is written
// down: the node sits under config/policy, where a grant left out is a refusal
// no reader of the template can see. rules_test.go's
// TestPolicyNodesAreReadPerChild reads the generated document's shape (every
// config path carries no write, and this one is no exception) and
// TestRoutePathIsGrantedInTheDeployedRules (internal/creds) holds the path this
// package's template spells to creds.RoutePath; this file proves the three
// claims on the emulator, each beside the positive control that makes its
// refusal a refusal of the rule and not of an empty tree:
//
//   - a run token reads its own repository's record and is refused its
//     sibling's — `TOKEN && auth.token.fs == $slug`, which is the whole reason
//     the record is per repository rather than one document: a read granted on
//     the parent would cascade to every repository's record, and the parent is
//     the path the member's own update walks to remove the records of
//     repositories it no longer publishes;
//   - a run writes nothing here: not its own record, not its sibling's, not a
//     child of one, and not a record that is not there yet — a client that could
//     write one would point a later run at a service of its own;
//   - no other identity the rules evaluate writes it either: not the device's
//     token, not one the launcher's `signJwt` minter made (which is how the
//     launcher, an orchestrator and a coordinator present themselves), not a
//     signed-in person's with no device node, not the mailbox consumer's, not a
//     pairing session's, not the reports job's. The one writer is the member's
//     own admin credential over IAM, which bypasses these rules
//     (deploy/terraform/gcp/modules/firebase/iam.tf) and is what WS9's
//     publisher writes the record with.

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// routePath is one repository's record, keyed as the rule and the token agree.
// The subtree above it is routeSubtree, declared beside isNewRoutePath in
// rules_test.go so that the path this file drives and the path the shape test
// names are one spelling.
func routePath(slug string) string { return routeSubtree + "/" + budget.Key(slug) }

// aRoute is a record of C2's shape: the service's address and the holder of the
// kinds the member publishes. The rules never read it — they grant the leaf and
// nothing else, and its members are validated by RouteRecord when it is
// published, not here — so what it is for is that the node holds something: a
// granted read of an absent node answers null, which would make a refusal below
// indistinguishable from a record that was never written.
var aRoute = map[string]any{
	"version": 1,
	"address": "https://fugaro-workspace-credentials-abc123-uc.a.run.app",
	"kinds":   map[string]any{"model": map[string]any{"holder": "workspace"}},
}

func TestARunReadsTheRoute(t *testing.T) {
	h := newHarness(t)
	h.seed(leased(2 * usd))

	mine, sibling := routePath("aurora"), routePath("borealis")
	// The member's own `fugaro update` publishes the records over IAM, which is
	// the only writer: the nodes are really there, so the refusals below are the
	// rule's and not an absent node's.
	h.set(mine, aRoute)
	h.set(sibling, aRoute)

	// A run token reads its own repository's record. This is the red step of
	// WS22 read forwards: under the document behind, this read is refused, and
	// the run's bootstrap has no route to take.
	run := h.run("aurora", "r1")
	h.mustRead(run, budget.PathCapsGlobal)
	h.mustRead(run, mine)
	h.wantValue(mine+"/address", `"https://fugaro-workspace-credentials-abc123-uc.a.run.app"`)

	// Its sibling's is its sibling's: the grant is the token's own slug, so one
	// member's run never learns which service another member's runs call.
	h.mustReadDenied(run, sibling)
	h.mustRead(h.run("borealis", "r2"), sibling)
	// A repository the member publishes no record for is refused the same way:
	// the grant is the claim and not the node's existence, so a run cannot use
	// the read to probe which repositories its workspace holds.
	h.mustReadDenied(run, routePath("carina"))

	// The two parents are refused, and they are the paths a read WOULD cascade
	// from: granting either would hand every repository's record — its address,
	// and the holder of each kind — to every run of the installation.
	h.mustReadDenied(run, routeSubtree)
	h.mustReadDenied(run, "config/policy")
}

func TestNoRunWritesTheRoute(t *testing.T) {
	h := newHarness(t)
	w := leased(2 * usd)
	h.seed(w)
	mine, sibling := routePath("aurora"), routePath("borealis")
	h.set(mine, aRoute)
	h.set(sibling, aRoute)

	run := w.client(h)
	h.mustRead(run, mine)
	// The control: this token holds a write the deployed rules DO grant it, so
	// every refusal below is this node's rule and not a read-only identity. It
	// is also the write that makes the route's address worth a run's while: a
	// run that could move its own record's address would send its model calls,
	// and its git credential fetches, to a service of its own (D2981, R3).
	h.mustAllow(run, "the run's own lease, a write the deployed rules grant it", leaseUpdates(w, 2*usd))

	h.mustDeny(run, "a run writing its own repository's route record", put(mine, aRoute))
	h.mustDeny(run, "a run rewriting the address of its own route record", put(mine+"/address", "https://elsewhere.invalid"))
	h.mustDeny(run, "a run rewriting one kind of its own route record", put(mine+"/kinds/model", map[string]any{"holder": "project"}))
	h.mustDeny(run, "a run deleting its own route record", map[string]any{mine: nil})
	h.mustDeny(run, "a run writing a sibling repository's route record", put(sibling, aRoute))
	h.mustDeny(run, "a run publishing a record for a repository it has no route for", put(routePath("carina"), aRoute))
	h.mustDeny(run, "a run writing the subtree the records sit under", put(routeSubtree, map[string]any{budget.Key("aurora"): aRoute}))

	// And the denials changed nothing: the run still reads what it read.
	h.mustRead(run, mine)
	h.wantValue(mine+"/address", `"https://fugaro-workspace-credentials-abc123-uc.a.run.app"`)
}

func TestNoOtherTokenWritesTheRoute(t *testing.T) {
	h := newHarness(t)
	h.seed(leased(2 * usd))
	mine := routePath("aurora")
	h.set(mine, aRoute)

	// The identities below hold a read or a write the rules DO grant them
	// elsewhere in the same database, so their refusals here are this node's
	// rule and not claims the rules ignore wherever they appear.
	device := h.phone(mbAlice)
	h.mustRead(device, budget.PathCapsGlobal)
	minted := h.minter("svc-minter-1")
	h.mustRead(minted, budget.PathCapsGlobal)
	// The strongest case of the minted ones: a launcher can mint a token that
	// carries a run's own claims for this repository, and it READS the record as
	// that run does — which is why the refusal the table below asserts is the
	// leaf's `.write` and not a shape check that happens to reject the claims.
	h.mustRead(h.run("aurora", "r9"), mine)
	h.mustAllow(h.consumer(mbAlice), "alice's heartbeat", put(beat(mbAlice), aHeartbeat()))
	h.mustAllow(h.pairing(pairUid), "its own pairing request", put(request(pairUid), aRequest()))

	for _, c := range []struct {
		who    string
		client *rtdb.Client
		write  func() map[string]any
	}{
		{"the device's token (the paired phone)", device, func() map[string]any { return put(mine, aRoute) }},
		{"the device's token (the paired phone)", device, func() map[string]any { return put(mine+"/address", "https://elsewhere.invalid") }},
		// A launcher, an orchestrator and a coordinator present themselves with a
		// token the `signJwt` minter made (iam.tf's `minter` grant; docs/
		// workspace-service/reconciliation.md, "What the rules do and do not
		// bind"), so they are the same case: a minted token, whatever claims it
		// carries, is refused the write.
		{"a launcher's minted token", minted, func() map[string]any { return put(mine, aRoute) }},
		{"a launcher's minted token shaped as a run's", h.run("aurora", "r9"), func() map[string]any { return put(mine, aRoute) }},
		{"a token of an account with no device node (the launcher's own session)", h.unpaired(mbBob), func() map[string]any { return put(mine, aRoute) }},
		{"a mailbox consumer's token", h.consumer(mbAlice), func() map[string]any { return put(mine, aRoute) }},
		{"a pairing session's token", h.pairing(pairUid), func() map[string]any { return put(mine, aRoute) }},
		{"the reports job's token", h.reportsJob(projectID), func() map[string]any { return put(mine, aRoute) }},
	} {
		h.mustDeny(c.client, c.who+" writing the route record", c.write())
	}

	// A record that is not there yet is refused them too: the write that creates
	// one is the member's, and a client that could create one would be a client
	// that could give a repository a route the member never chose.
	h.mustDeny(device, "the device publishing a record for a repository the member never routed", put(routePath("carina"), aRoute))

	// The member's own credential writes both, over IAM: the rules do not bind
	// that path and this file claims no guarantee about it. It is here as the
	// positive control, so that the refusals above are refusals of a node that
	// holds data and is written by someone.
	h.set(routePath("carina"), aRoute)
	h.wantValue(routePath("carina")+"/address", `"https://fugaro-workspace-credentials-abc123-uc.a.run.app"`)
}
