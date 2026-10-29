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
