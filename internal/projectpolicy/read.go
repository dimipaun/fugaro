package projectpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Read reads the project's policy through db, applying the two rules that
// decide whether this installation has a policy at all (design §5(3), plan
// task 1.3 step 1):
//
//   - The rules marker, config/limits/policyRules, records which generation
//     of the database's security rules is deployed. Absent, or below
//     RulesVersion, this installation's rules predate the policy nodes: no
//     policy can have been published here (publishing refuses without the
//     marker), so the answer is "not found" and a permission-denied or
//     missing read of config/policy/shared is not an error. This is what
//     keeps a release from halting an installation whose rules deploy is the
//     owner's own step.
//   - With the marker deployed, the node decides: absent (or null) is
//     "installed, not adopted", today's behaviour; anything else that cannot
//     be read or decoded is an error the caller must treat as a halt, never
//     as an empty ceiling. A denial here is genuine: the marker is written by
//     the same deploy that grants the read, so "denied" means the rules were
//     changed out from under a published policy.
//
// It returns (p, found, err), exactly as Decode documents, with err
// non-nil whenever the marker or the node could not be read.
func Read(ctx context.Context, db *rtdb.Client) (*Policy, bool, error) {
	gen, err := rulesGeneration(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if gen < RulesVersion {
		return nil, false, nil
	}
	var raw json.RawMessage
	found, err := db.Get(ctx, PathShared(), &raw)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", PathShared(), err)
	}
	if !found {
		return nil, false, nil
	}
	return Decode(raw)
}

// MarkerError is the refusal of a config/limits/policyRules node whose value
// is not an integer generation. It is a user error — the node is a person's,
// and a hand-edited one must be named rather than guessed at — and it is
// never read as "no marker", which would silently make a project-centric
// installation a legacy one.
type MarkerError struct{ Value string }

func (e *MarkerError) Error() string {
	return fmt.Sprintf("%s holds %s: it must be the integer generation of the deployed rules, written by fugaro init --firebase", PathRulesMarker(), e.Value)
}

// rulesGeneration reads the deployed security rules' generation. An absent
// marker is generation 0: the pre-policy rules, under which the policy nodes
// do not exist (the owner's rules deploy has not happened yet, and nothing
// may halt on that).
func rulesGeneration(ctx context.Context, db *rtdb.Client) (int, error) {
	var raw json.RawMessage
	found, err := db.Get(ctx, PathRulesMarker(), &raw)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", PathRulesMarker(), err)
	}
	if !found {
		return 0, nil
	}
	var gen int
	if err := json.Unmarshal(raw, &gen); err != nil {
		return 0, &MarkerError{Value: nodeText(raw)}
	}
	return gen, nil
}

// nodeText renders a node for an error message: one line, bounded, so a
// hand-edited node cannot paste a document into a log.
func nodeText(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}
