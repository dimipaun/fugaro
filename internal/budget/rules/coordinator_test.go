package rules

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/coordinator"
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
// The emulator suite proves the same three against a real rules engine; these
// run in the job that has no emulator, which is where a rules change is read.
//
// Hand mutation each fails on: dropping the /coordinator node (the state has
// no grant at all), widening the read to TOKEN or to the root's own grant (a
// run reads the project's state), adding a .write anywhere under it (a client
// can write the coordinator's record), and moving the read from the root to a
// child (the subtree a reader is not told about is not granted).

func TestCoordinatorReadGrantIsTheSignedInReaderAlone(t *testing.T) {
	root, ok := generatedPaths(t)[coordinator.PathRoot]
	if !ok {
		t.Fatalf("the generated rules carry no /%s node: the coordinator's state is granted to nobody", coordinator.PathRoot)
	}
	got, _ := root[".read"].(string)
	if got != userClaim {
		t.Errorf("/%s .read = %q, want the signed-in reader's claim alone (%q)", coordinator.PathRoot, root[".read"], userClaim)
	}
}

func TestCoordinatorIsWrittenByNoClient(t *testing.T) {
	walk(rulesTree(t), nil, func(p []string, attrs map[string]any) {
		if len(p) == 0 || p[0] != coordinator.PathRoot {
			return
		}
		if w, has := attrs[".write"]; has && w != false {
			t.Errorf("/%s: .write = %v under /%s, where the coordinator is the only writer and writes over IAM", strings.Join(p, "/"), w, coordinator.PathRoot)
		}
	})
}

func TestRunTokenCannotReadTheCoordinator(t *testing.T) {
	for p, attrs := range generatedPaths(t) {
		if p != coordinator.PathRoot && !strings.HasPrefix(p, coordinator.PathRoot+"/") {
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
