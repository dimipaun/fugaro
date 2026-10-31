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
//     PolicyRulesVersion, this installation's rules are older than the policy
//     nodes: no policy can have been published here (publishing refuses
//     without a current marker), so the answer is "not found" and a
//     permission-denied or missing read of config/policy/shared is not an
//     error. This is what keeps a release from halting an installation whose
//     rules deploy is the owner's own step.
//
//     The gate is PolicyRulesVersion and not RulesVersion, and that is the
//     whole of the difference between "this installation has no policy" and
//     "this installation runs a document older than mine". A marker between
//     the two — generation 1, the only such value today — is a deployment that
//     carries the policy nodes: the policy published under it is read here, and
//     what it lacks is the nodes a later generation added beside them (the
//     mailbox, RulesVersion). Gating on RulesVersion would answer "not found"
//     for a node that is there, which every caller of this function reads as an
//     empty ceiling (the review finding on P2.1).
//
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
	if gen < PolicyRulesVersion {
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
	return fmt.Sprintf("%s holds %s: it must be the integer generation of the deployed rules, written by fugaro update --firebase-project <gcp-project-id>", PathRulesMarker(), e.Value)
}

// rulesGeneration reads the deployed security rules' generation. An absent
// marker is generation 0: the pre-policy rules, under which the policy nodes
// do not exist (the owner's rules deploy has not happened yet, and nothing
// may halt on that).
//
// A *negative* generation is a MarkerError and not a small one. Read asks this
// function only whether the generation is at least PolicyRulesVersion, so a
// marker of -1 would answer "no": the whole node would read as "not found",
// which is the
// legacy path, and a hand-edited -1 would silently take a project-centric
// installation's policy out of every run's reach — the fail-open MarkerError's
// own doc forbids, on the one node that decides whether a published policy is
// read at all. No deploy writes a negative generation (RulesVersion only goes
// up), so a negative one is a hand edit, and it is named rather than
// guessed at. The same reading applies to a generation far above RulesVersion:
// that is a newer installation's marker, and it is at least this one's, so it
// reads as deployed.
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
	if MarkerHandEdited(gen) {
		return 0, &MarkerError{Value: nodeText(raw)}
	}
	return gen, nil
}

// MarkerHandEdited reports whether the generation a caller has read off
// config/limits/policyRules is one no deploy writes, which is the value Read
// refuses as a MarkerError (rulesGeneration's doc has the argument: a negative
// generation would otherwise answer "no policy here" and take a project-centric
// installation's published policy out of every run's reach).
//
// It is exported because the marker has three readers, and all three must agree
// about what a negative generation is: Read, which refuses it, `fugaro project
// policy publish` (internal/cli's requirePolicyRules), which refuses to publish
// under it, and doctor's policy-rules-skew check, which reports it. Both of the
// latter branched on "below RulesVersion" and printed rulesMarkerBelowText —
// whose sentence claims the installation's rules predate the policy nodes, and
// sends the operator to redeploy rules that are not what is wrong — while Read
// read the same node as the hand edit it is (the review finding on this
// change). One predicate, so a marker cannot be one state to the run and
// another to the two commands that talk about it.
func MarkerHandEdited(gen int) bool { return gen < 0 }

// nodeText renders a node for an error message: one line, bounded, so a
// hand-edited node cannot paste a document into a log.
func nodeText(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}
