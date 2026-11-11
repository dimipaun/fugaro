package rules

// CG6 of the console dependency graph plan (docs/cli-output-console-graph/
// implementation-plan-graph.md; finding T2, ruled 2026-11-06): the graph reads
// the `orchestrator/` subtree through the person's own IAM credential, exactly
// as `coordinator status --from-store` reads `coordinator/`, so the subtree is
// granted nothing new — this file is the *pin* that keeps the document that way
// (H7, T2(a)), not a grant.
//
// O2 of the stateless orchestrator plan wrote the subtree and its own tests
// (emulator_orchestrator_test.go); what is pinned here is the denial the graph's
// read rests on: no identity the rules evaluate as a run's or as a client
// token's is admitted to the subtree, on the read or on the write. The run's is
// the one that matters — its holder is a launched job, and a read of this
// subtree hands it every task title and note of every orchestrator of the
// project — and it is joined by the two other identities a launcher's minter
// can put on a token: the mailbox consumer's `mc` claim and a pairing session's
// `pu` claim.
//
// The `mu` claim is deliberately not in that list. It is the person — the
// account D166's device allowlist narrows a signed-in reader to — and D1241's
// answer has the person read this node, which O2's own test pins. A read this
// file refused would be a read the design grants.
//
// emulator_orchestrator_test.go (build tag firebase) proves the same claims
// against a real rules engine; these run in the job that has no emulator, which
// is where a rules change is read.
//
// Hand mutation each fails on: widening the subtree's `.read` from USER to
// TOKEN (the equality below, and then the claim scan), putting a `.read` for a
// run or a consumer identity on a `$child` under it (the scan, which is the
// shape a widening hides in), and adding a `.write` anywhere under the subtree
// (a client can write the state the daemon's `PutIfMatch` is supposed to own).

import (
	"strings"
	"testing"
)

// refusedOrchestratorClaims are the claims of every identity the rules evaluate
// and refuse under `/orchestrator/`: a run-scoped job token carries FP, FS, FR
// and FX (the TOKEN and RUN macros), the mailbox consumer's carries MC
// (CONSUMER), and a pairing session's carries PU (PAIRING). MU is absent on
// purpose: it is the person, the reader D1241's answer grants.
var refusedOrchestratorClaims = []string{
	"auth.token.fp", "auth.token.fs", "auth.token.fr", "auth.token.fx",
	"auth.token.mc", "auth.token.pu",
}

func TestOrchestratorNodeIsDeniedToRunAndTokenIdentities(t *testing.T) {
	node, ok := generatedPaths(t)[PathOrchestrator]
	if !ok {
		t.Fatalf("the generated rules carry no /%s node: the graph's read of it is the person's credential over IAM, and a subtree the document never names is one no reader of this file can see", PathOrchestrator)
	}
	// The subtree's own read is the signed-in person's claim alone. This is
	// stronger than "no run claim appears in it": it also refuses a read granted
	// to an identity nobody has named yet.
	got, _ := node[".read"].(string)
	if got != userClaim {
		t.Errorf("/%s .read = %q, want the signed-in person's claim alone (%q): the graph and `coordinator status --from-store` read this subtree over IAM, and a read granted to a token identity hands every orchestrator's task titles and notes to whoever holds that token", PathOrchestrator, got, userClaim)
	}
	// And nothing beneath it re-grants what the node refuses: a read on a child
	// is the shape a widening hides in, because the node's own rule reads
	// unchanged while a $child under it opens.
	for p, attrs := range generatedPaths(t) {
		if p != PathOrchestrator && !strings.HasPrefix(p, PathOrchestrator+"/") {
			continue
		}
		r, _ := attrs[".read"].(string)
		for _, claim := range refusedOrchestratorClaims {
			if strings.Contains(r, claim) {
				t.Errorf("/%s .read = %q admits a token identity (%s): no run, consumer or pairing token reaches the orchestrator's state", p, r, claim)
			}
		}
	}
}

func TestNoClientWriteUnderOrchestrator(t *testing.T) {
	walk(rulesTree(t), nil, func(p []string, attrs map[string]any) {
		if len(p) == 0 || p[0] != PathOrchestrator {
			return
		}
		if w, has := attrs[".write"]; has && w != false {
			t.Errorf("/%s: .write = %v under /%s, where the person's OAuth Bearer is the only writer and IAM bypasses the rules: a client-writable node here is a job token overwriting the daemon's own record (D1241)", strings.Join(p, "/"), w, PathOrchestrator)
		}
	})
}
