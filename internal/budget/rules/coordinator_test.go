package rules

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/coordpath"
)

// The coordinator's read grant (docs/coordinator/implementation-plan.md, part
// P3; reconciliation Q15, answered (A) on 2026-10-19, and D195, answered (A) on
// 2026-10-21). Three claims are pinned here, and each is a different way for
// the same subtree to be reached by somebody it was not granted to:
//
//   - the signed-in reader the CLI and the phone already authenticate as reads
//     the whole subtree, from one grant on the root, because D195 chose the
//     layout so that the read is one prefix rather than a list of paths;
//   - no client writes it: the coordinator is its only writer, over IAM, which
//     bypasses the rules (Q15 A);
//   - no run token reads it, which is finding #25's fence — no run identity
//     reaches a node it has no business in.
//
// emulator_coordinator_test.go (build tag firebase) proves the same three
// against a real rules engine; these run in the job that has no emulator,
// which is where a rules change is read.
//
// Hand mutation each fails on: dropping the /coordinator node (the state has
// no grant at all), widening the read to TOKEN or to the root's own grant (a
// run reads the project's state), adding a .write anywhere under it (a client
// can write the coordinator's record), and moving the read from the root to a
// child (the subtree a reader is not told about is not granted).

func TestCoordinatorReadGrantIsTheSignedInReaderAlone(t *testing.T) {
	root, ok := generatedPaths(t)[coordpath.PathRoot]
	if !ok {
		t.Fatalf("the generated rules carry no /%s node: the coordinator's state is granted to nobody", coordpath.PathRoot)
	}
	got, _ := root[".read"].(string)
	if got != userClaim {
		t.Errorf("/%s .read = %q, want the signed-in reader's claim alone (%q)", coordpath.PathRoot, root[".read"], userClaim)
	}
}

func TestCoordinatorIsWrittenByNoClient(t *testing.T) {
	walk(rulesTree(t), nil, func(p []string, attrs map[string]any) {
		if len(p) == 0 || p[0] != coordpath.PathRoot {
			return
		}
		if w, has := attrs[".write"]; has && w != false {
			t.Errorf("/%s: .write = %v under /%s, where the coordinator is the only writer and writes over IAM", strings.Join(p, "/"), w, coordpath.PathRoot)
		}
	})
}

// TestRoadmapNodesAreWrittenByNoClient is P9b's half of the fence (D2118 as
// finally answered, D2124): the roadmap's node and its four document families —
// plans, releases, edges, assignments — are governed by the /coordinator node
// alone, whose .write is the boolean false, and every generated location nearer
// than it that governs one of them denies a write too.
//
// A `.write: "USER"` on a roadmap node is the mistake this asserts against, and
// it is the one a future change is most likely to make: the front door queues
// every roadmap write and the apply job applies it, so no client's token —
// signed in, project-wide or a run's — may write any of these paths. It is the
// same claim as coordinator_test.go's node walk, made at the paths P9b writes:
// a grant added below /coordinator would leave that walk green and this one
// red.
func TestRoadmapNodesAreWrittenByNoClient(t *testing.T) {
	roadmap := coordpath.PathProjectDocument("roadmap")
	for _, node := range []string{
		roadmap,
		roadmap + "/plans", roadmap + "/releases", roadmap + "/edges", roadmap + "/assigns",
	} {
		governing := governingPaths(t, node)
		if len(governing) == 0 {
			t.Errorf("/%s is governed by no generated location: the rules say nothing about it", node)
		}
		for p, attrs := range governing {
			if w, has := attrs[".write"]; has && w != false {
				t.Errorf("/%s: .write = %v governs /%s; the roadmap is written by the apply job over IAM and by no client", p, w, node)
			}
			if r, has := attrs[".read"]; has && r != userClaim {
				t.Errorf("/%s: .read = %q governs /%s, want the signed-in reader's claim alone (%q)", p, r, node, userClaim)
			}
		}
	}
}

func TestRunTokenCannotReadTheCoordinator(t *testing.T) {
	for p, attrs := range generatedPaths(t) {
		if p != coordpath.PathRoot && !strings.HasPrefix(p, coordpath.PathRoot+"/") {
			continue
		}
		r, _ := attrs[".read"].(string)
		for _, claim := range []string{"auth.token.fp", "auth.token.fs", "auth.token.fr", "auth.token.fx"} {
			if strings.Contains(r, claim) {
				t.Errorf("/%s .read = %q admits a run's token (%s): no run identity reaches the coordinator's state", p, r, claim)
			}
		}
	}
}
