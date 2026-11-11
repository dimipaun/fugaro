//go:build firebase

package rules

// The coordinator's read grant against a real rules engine (coordinator plan
// part P3; Q15 A and D195): coordinator_test.go reads the generated document's
// shape, and this file proves the same three claims on the emulator — the
// signed-in reader reads the subtree, no client writes it, and no run token
// reads it — each beside the positive control that makes its refusal a
// refusal of the rule and not of an empty tree.

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/coordpath"
)

func TestCoordinatorGrantOnTheEmulator(t *testing.T) {
	h := newHarness(t)
	w := leased(2 * usd)
	h.seed(w)
	project := coordpath.PathProjectDocument(coordpath.DocumentID("project", "view"))
	repo := coordpath.PathRepoDocument("aurora", coordpath.DocumentID("intent", "who", "what"))
	// The coordinator's writer bypasses the rules, as the harness's admin does.
	h.set(project, map[string]any{"state": "open"})
	h.set(repo, map[string]any{"state": "open"})

	// The signed-in reader (a paired account) reads the whole subtree, from
	// the one grant on its root.
	alice := h.phone(mbAlice)
	for _, p := range []string{coordpath.PathRoot, coordpath.PathProject, project, coordpath.PathRepos, repo} {
		h.mustRead(alice, p)
	}

	// An account with no device node is not a signed-in reader (D166).
	h.mustReadDenied(h.unpaired(mbBob), coordpath.PathRoot)

	// No run identity reads it (finding #25), and neither does the mailbox
	// consumer or a pairing session.
	run := h.run("aurora", "r1")
	h.mustRead(run, "config/caps/global")
	for _, p := range []string{coordpath.PathRoot, repo} {
		h.mustReadDenied(run, p)
	}
	h.mustReadDenied(h.consumer(mbAlice), coordpath.PathRoot)
	h.mustReadDenied(h.pairing(pairUid), coordpath.PathRoot)

	// No client writes it: the reader it is granted to, and a run.
	h.mustDeny(alice, "the reader writing a coordinator document", put(project, map[string]any{"state": "closed"}))
	h.mustDeny(alice, "the reader creating a coordinator document", put(coordpath.PathProjectDocument("new"), map[string]any{"state": "open"}))
	h.mustDeny(run, "a run writing a coordinator document", put(repo, map[string]any{"state": "closed"}))
	h.mustRead(alice, project)
}

// TestNoClientWritesARoadmapNode is P9b's fence on the emulator (D2118 as
// finally answered, D2124): the roadmap node and each of its four document
// families are refusals for all three of the client identities the rules know
// — the signed-in reader the phone and the console hold, a project token, and a
// run's — and each identity is beside a positive control, so the refusal is of
// these paths and not of a token the rules would refuse anywhere.
//
// The one writer is the coordinator's account over IAM through the apply job
// (D2118), which is why every write here has to be denied: the front door
// queues a roadmap write and never makes one (D2124).
func TestNoClientWritesARoadmapNode(t *testing.T) {
	h := newHarness(t)
	w := leased(2 * usd)
	h.seed(w)
	roadmap := coordpath.PathProjectDocument("roadmap")

	reader := h.phone(mbAlice)
	// A project token: the project's id in fp and no run claims at all, which
	// is what the front door's session holds.
	projectToken := h.as(claims{rb: "-"})
	run := h.run("aurora", "r1")

	// The positive controls: each identity is live and admitted where the rules
	// grant it, so what follows is a refusal of the roadmap and not of the
	// token.
	h.mustRead(reader, coordpath.PathRoot)
	h.mustRead(projectToken, "config/caps/global")
	h.mustRead(run, "config/caps/global")
	h.mustAllow(reader, "the reader writing its own inbox", put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))

	for _, node := range []string{
		roadmap,
		roadmap + "/plans", roadmap + "/releases", roadmap + "/edges", roadmap + "/assign",
	} {
		doc := map[string]any{"schema": 1}
		h.mustDeny(reader, "the signed-in reader writing /"+node, put(node+"/a-document", doc))
		h.mustDeny(projectToken, "a project token writing /"+node, put(node+"/a-document", doc))
		h.mustDeny(run, "a run's token writing /"+node, put(node+"/a-document", doc))
	}
	// The reader still reads it: the refusal above is a write's, and the whole
	// subtree stays readable to the identity D195 granted it to.
	h.mustRead(reader, roadmap)
}
