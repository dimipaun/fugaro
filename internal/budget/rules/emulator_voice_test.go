//go:build firebase

package rules

// The voice policy node against a real rules engine (mobile plan task P5.3.1,
// approved by the owner on 2026-11-14; the voice-evaluation switch design
// §4.3). rules_test.go's
// TestVoicePolicyNodesAreReadPerChild reads the generated document's shape;
// this file proves the same three claims on the emulator, each beside the
// positive control that makes its refusal a refusal of the rule and not of an
// empty tree:
//
//   - a run token reads its own project's node and the shared one, and is
//     refused its sibling's — `TOKEN && auth.token.fs == $slug`, which is the
//     whole reason the node is per project rather than one document;
//   - a signed-in user reads nothing anywhere under the subtree, device node
//     or not: `USER` is in neither grant, which is why the switch's "blocked by
//     project policy" state arrives as the hop's project-status answer and not
//     as a read;
//   - nothing writes the subtree at all. The one writer is the operator's IAM
//     credential, which bypasses these rules (deploy/terraform/gcp/modules/
//     firebase/iam.tf), so a `.write` left open here would be an allow-list
//     every run and every phone of the installation could widen.

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

func TestVoicePolicyGrantOnTheEmulator(t *testing.T) {
	h := newHarness(t)
	h.seed(leased(2 * usd))

	shared := projectpolicy.PathVoiceShared()
	mine := projectpolicy.PathVoiceRepo("aurora")
	sibling := projectpolicy.PathVoiceRepo("borealis")
	// The operator publishes the policy over IAM, which is the only writer and
	// bypasses the rules: the nodes are really there, so every refusal below is
	// the rule's and not an absent node's.
	h.set(shared, map[string]any{"providers": []any{"a-provider"}})
	h.set(mine, map[string]any{
		"providers": []any{"a-provider"}, "direction": map[string]any{"stt": true, "tts": false}, "force_local": false,
	})
	h.set(sibling, map[string]any{"providers": []any{"another-provider"}})

	// A run token reads the shared node and its own project's, and is refused
	// its sibling's — which is the sibling's own token's to read.
	run := h.run("aurora", "r1")
	h.mustRead(run, "config/caps/global")
	h.mustRead(run, shared)
	h.mustRead(run, mine)
	h.mustReadDenied(run, sibling)
	h.mustRead(h.run("borealis", "r2"), sibling)
	// The two parents are refused: the grant is per child, and a read on either
	// parent would hand every project's allow-list to every run.
	h.mustReadDenied(run, PathVoice)
	h.mustReadDenied(run, projectpolicy.PathVoiceRepos())
	h.mustReadDenied(run, "config/policy")

	// No signed-in user reads any of it, and the phone is live: the control
	// beside each refusal is a node USER is granted.
	phone := h.phone(mbAlice)
	h.mustRead(phone, "config/caps/global")
	for _, p := range []string{PathVoice, shared, projectpolicy.PathVoiceRepos(), mine, sibling} {
		h.mustReadDenied(phone, p)
	}
	h.mustReadDenied(h.unpaired(mbBob), shared)
	h.mustReadDenied(h.consumer(mbAlice), shared)
	h.mustReadDenied(h.pairing(pairUid), shared)

	// Nothing writes it: not the run the node is granted to, not the phone, and
	// not with a whole-document replace or a single child.
	doc := map[string]any{"providers": []any{"a-provider"}}
	h.mustDeny(run, "a run writing its own project's voice policy", put(mine, doc))
	h.mustDeny(run, "a run writing the shared voice policy", put(shared, doc))
	h.mustDeny(run, "a run writing a sibling project's voice policy", put(sibling, doc))
	h.mustDeny(phone, "the phone writing its project's voice policy", put(mine, doc))
	h.mustDeny(phone, "the phone writing the shared voice policy", put(shared, doc))
	h.mustDeny(phone, "the phone widening its own project's allow-list", put(mine+"/providers", []any{"a-provider", "another-provider"}))

	// And the denials changed nothing: the run still reads what it read.
	h.mustRead(run, mine)
	h.wantValue(mine+"/force_local", "false")
}
