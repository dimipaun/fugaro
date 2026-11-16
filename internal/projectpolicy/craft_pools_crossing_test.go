package projectpolicy

// The crossing at the node's own read (plan MC20, reconciliation D1668,
// D1673): a published node that carries a pool naming members of both halves of
// a run is refused by Decode, which runs config.ValidatePublishedPools. That is
// the read the runner's Read fails closed on, the credentials service's
// per-call re-read, `fugaro doctor` and `fugaro update` — the service must
// never be handed a pool whose listed order it cannot honour — so a node that
// already carries one is refused wherever it is read, and the step that splits
// it into two pools is MC21's and ships in this release with the refusal.

import (
	"fmt"
	"strings"
	"testing"
)

// TestTheNodeRefusesACrossPathPool is the node's half of the refusal: a node at
// THIS binary's version whose pool lists a member of each half is refused, and
// the sentence is the config's own — one sentence, so an operator who meets it
// at `fugaro accounts pool add` and on a node read by `fugaro update` reads the
// same thing. The version is read from Version rather than spelled, because the
// refusal is a rule of the reader and not a step of the chain: what MC21 adds is
// the step that converges such a node, in the release that ships this.
func TestTheNodeRefusesACrossPathPool(t *testing.T) {
	const want = "members anthropic-y and openrouter-x are served by different halves of a run (anthropic-y's type is served by the credentials service, openrouter-x's by the run's own gateway), and a pool's order can therefore mean nothing: name two pools, one per half, and let each craft's access name the one its stages need"
	const creds = `"craft_credentials":[{"name":"openrouter-x","type":"openrouter"},{"name":"anthropic-y","type":"anthropic"}],`
	raw := fmt.Sprintf(`{"version":%d,`+creds+`"craft_pools":[{"name":"mixed","owner":"owner@example.com","members":["openrouter-x","anthropic-y"]}]}`, Version)

	p, _, err := Decode([]byte(raw))
	if err == nil || p != nil {
		t.Fatalf("a node carrying a cross-path pool was read: %+v (%v)", p, err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal\n%v\ndoes not carry the sentence\n%s", err, want)
	}
	if !strings.Contains(err.Error(), "pools.mixed.members[1]") {
		t.Errorf("the refusal %q does not name the member the pool's order crosses at", err)
	}

	// The control: the same node with its members split into two pools of one
	// path each — which is what the fix asks for, and what MC21 writes for a
	// node that already carries a crossing.
	split := fmt.Sprintf(`{"version":%d,`+creds+`"craft_pools":[{"name":"mixed-claude","owner":"owner@example.com","members":["anthropic-y"]},{"name":"mixed-provider","owner":"owner@example.com","members":["openrouter-x"]}]}`, Version)
	read, found, err := Decode([]byte(split))
	if err != nil || !found || read == nil || len(read.CraftPools) != 2 {
		t.Fatalf("two pools of one path each do not read: (%+v, %v, %v)", read, found, err)
	}
}
