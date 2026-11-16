package gateway

import "net/http"

// The marker a workspace's credentials service puts on the refusals it answers
// on the workspace's own account, and the three refusals it names (docs/
// workspaces/spec.md §5 and §9, D2022's O19).
//
// It is the second header of the wire workspace_headroom.go opens, between the
// same two binaries and in the same direction: the workspace's credentials
// service writes it and a member's gateway reads it. It is not a node of any
// database — the service holds no write on a member at all, and a marker
// carried on the refusal the service is already sending needs no write to
// carry.
//
// What it is for is the run's own record. A member's run reaches the
// workspace's service for the credentials the workspace pays for, and a
// refusal there is the same `state_unavailable` a member's own service answers
// when it cannot read its state, so the member's gateway records the same
// `budget_unavailable`/`run` halt for both and the run's record does not say
// which credentials service refused it (the reading of 2026-11-13,
// docs/stateless-orchestrator/reconciliation.md U12). A launcher reading that
// record therefore cannot tell a workspace's refusal from an ordinary outage,
// counts it as one of an item's attempts and trips its breaker only after
// three of them.
//
// Nothing here decides anything. The service's own refusal is what it was —
// the marker names which of the three it already made, and no reader of this
// package refuses a call, raises a cap or reads a database because of it. The
// cap, the stop and the fence stay exactly where they are: the workspace's
// meter (internal/credsvc/workspace_cap.go) takes the room, the workspace's
// own kill switch (budget.PathKillGlobal in the workspace's database) is what
// the service reads, and this package only carries the fact.
const (
	// WorkspaceRefusalStop is the workspace's stop switch: with it on, the
	// workspace's service issues no credential and refuses every call it
	// would pay for (spec §9, D1302).
	WorkspaceRefusalStop = "stop"
	// WorkspaceRefusalCap is the workspace's own spend ceiling: the room the
	// call needs is more than the workspace's month has left, so the
	// service's meter refuses the call before it is made (spec §5).
	WorkspaceRefusalCap = "cap"
	// WorkspaceRefusalSettle is a call the service made and could not record
	// the cost of: a call whose usage cannot be written back is refused
	// rather than served, because a call whose cost the service cannot record
	// spends against a cap it cannot see (spec §5, the fail-closed rule).
	WorkspaceRefusalSettle = "settle"
)

// WorkspaceRefusalHeader is the header a workspace's credentials service marks
// its own refusals with. It is a claim the service makes about itself and
// about no one else: it names no member, no project and no credential, and it
// is written on a refusal the service had already decided to answer.
//
// It is written by the service beside the headroom the same answer carries
// (credsvc.SetWorkspaceRefusal), and it is dropped from any answer that
// reaches the agent, as the headroom is: the marker is between the two
// binaries.
const WorkspaceRefusalHeader = "X-Fugaro-Workspace-Refusal"

// WorkspaceRefusals is the whole vocabulary, in source order. It is the one
// spelling of the set: result.schema.json's halt.workspace_refusal enum is
// held to it, so a value the runner writes and a value a reader accepts cannot
// drift apart.
func WorkspaceRefusals() []string {
	return []string{WorkspaceRefusalStop, WorkspaceRefusalCap, WorkspaceRefusalSettle}
}

// ValidWorkspaceRefusal reports whether v is one of the three refusals this
// wire carries.
func ValidWorkspaceRefusal(v string) bool {
	for _, r := range WorkspaceRefusals() {
		if v == r {
			return true
		}
	}
	return false
}

// WorkspaceRefusal reads the marker off a refusal answer: which of the
// workspace's own refusals the service answered, and whether the answer
// carried the marker at all.
//
// An answer with no marker is a refusal that is not the workspace's — a
// member's own credentials service answering, a project's policy refusing a
// call, an installation that deploys no workspace at all — and it is also what
// a workspace service from a release before this one answers. The two are the
// same thing to a reader that only marks a refusal the workspace claimed, and
// neither is a workspace's refusal.
//
// A marker that is not one of the three is not a refusal this contract could
// have written, and it is read as no marker rather than as a refusal: a value
// this release does not know must not be able to make a run's record say the
// workspace refused it.
func WorkspaceRefusal(h http.Header) (string, bool) {
	v := h.Get(WorkspaceRefusalHeader)
	if !ValidWorkspaceRefusal(v) {
		return "", false
	}
	return v, true
}

// ValidWorkspaceRefusalHalt reports whether reason, scope and detail are the
// halt a run refused by the workspace is written with, for the marker its
// record carries: the shape serviceAnswer's haltServiceMarked builds, which is
// the halt every credentials-service refusal forces — budget_unavailable, scope
// run — with the sentence that path writes for the refusal the workspace's own
// service answered.
//
// All three of the workspace's refusals reach a member's gateway as the same
// typed refusal, the service unable to read or write the state it must
// (state_unavailable): its stop is the kill switch it reads, its cap is the
// room its meter takes in the workspace's own database, and a settle it could
// not write back is that write failing. So the three markers share one halt
// shape here, and a release that answers one of them with another wire type
// extends this table rather than the reader that asks it.
//
// It is here, and not beside the reader, because this package is what writes
// the halt: the reader is the daemon's, which corroborates a marker a run wrote
// into its own record against the halt beside it before it pauses its queue on
// it (ee/orchestrator.WorkspaceRefused, O19). The corroboration is a bound and
// not an authentication — the marker and the halt are both the run's own write,
// and a run can produce this shape — which is why the pause the daemon raises
// lifts itself after orchestrator.WorkspacePauseWindow.
//
// A marker outside the three is not a refusal this contract could have written
// (ValidWorkspaceRefusal) and is answered false rather than compared: a value
// this release does not know must not be able to make a run's record pause a
// queue.
func ValidWorkspaceRefusalHalt(marker, reason, scope, detail string) bool {
	switch marker {
	case WorkspaceRefusalStop, WorkspaceRefusalCap, WorkspaceRefusalSettle:
		wantReason, wantScope, wantDetail := WorkspaceRefusalHalt()
		return reason == wantReason && scope == wantScope && detail == wantDetail
	}
	return false
}

// WorkspaceRefusalHalt is that halt as the three fields a reader compares: the
// one spelling of the shape, so the predicate above and a caller that states
// the shape — ee/orchestrator's tests, which are the other half of the
// corroboration — cannot drift from the halt haltServiceMarked writes.
func WorkspaceRefusalHalt() (reason, scope, detail string) {
	return ReasonBudgetUnavailable, "run", refusalDetail(ErrorStateUnavailable)
}
