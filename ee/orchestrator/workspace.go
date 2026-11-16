// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator

import (
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// WorkspaceRefused reports whether a run the daemon launched came back refused
// by the workspace its credentials came from, which is the one refusal the
// daemon pauses its queue on at once (O19, D2022 (a): "when a run the daemon
// launched ends refused by the workspace (its stop or its cap), the daemon
// trips its breaker at once"). It is the implementation ee/register installs in
// the core's workspace seam (internal/orchestrator.SetWorkspace), and it reads
// the run's report and nothing else: no database, no network, no clock, and no
// other run's record.
//
// A report is a workspace's refusal when its record carries one of the
// workspace's own markers (gateway.ValidWorkspaceRefusal) AND the halt beside
// that marker is the halt a workspace's refusal is written with
// (gateway.ValidWorkspaceRefusalHalt). Both halves are needed, and the second
// is why:
//
// The marker is the run's own write. It reaches the record from the header the
// workspace's credentials service put on the refusal it answered
// (gateway.WorkspaceRefusalHeader, read by the member's gateway and copied by
// the runner), and the halt is the run's own write too — result.json is the
// run's job account's to write, and a hostile run reaches that account's token
// from the metadata server (SECURITY.md, "Runs of one repository can see each
// other"). So a run can claim a refusal no workspace's service answered, and
// this function is a bound on the claim and not an authentication of it:
//
//   - a marker on a record whose halt is not the shape that path writes — a
//     halt with another reason, another scope, another detail, or no halt at
//     all — is not a refusal this tree acts on, and the run counts as the
//     ordinary refusal O6's rule counts. What that costs a forger is the shape
//     of a refusal, which is a constant in this repository and therefore cheap;
//     what it does catch is a record that says "the workspace refused me" and
//     nothing else, which is the accident the field exists to prevent.
//   - the pause the claim does earn is bounded in time:
//     internal/orchestrator's WorkspacePauseWindow lifts it after an hour, so a
//     forged marker costs the queue one window and a person's answer ends it
//     sooner. The bound is not here because it is a fact about the pause and
//     not about the marker — the daemon's own breaker reads it — and it is the
//     half of the pair that does not rest on the forger's own honesty.
//
// Neither half is a decision this package can make on its own: what a
// workspace's refusal looks like on the wire is the gateway's, and what a pause
// costs is the core's.
func WorkspaceRefused(r orchestrator.RunReport) bool {
	return gateway.ValidWorkspaceRefusalHalt(r.WorkspaceRefusal, r.Halt.Reason, r.Halt.Scope, r.Halt.Detail)
}
