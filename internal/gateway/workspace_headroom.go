package gateway

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// WorkspaceHeadroomHeader is the header a workspace's service delivers a
// member's advisory headroom under, on the answer to the calls it already
// proxies (docs/workspaces/spec.md §5, reconciliation.md §8's O1).
//
// It is the wire between two binaries: the workspace's credentials service
// writes it (internal/credsvc imports this package and uses this constant) and
// a member's gateway reads it here. It is never a node of a member's database —
// the service holds no write on a member at all, and a number delivered on the
// answer it is already sending needs no write to deliver.
//
// The value is micro-dollars, decimal: what the workspace's own ceiling still
// leaves, this month, across all of its members — budget.WorkspaceRoom, read
// through the service's read-only half. It is the workspace's figure and not
// the member's own spend, which is why it is written on the answer to the
// calling member's job service account alone (spec §5) and dropped from the
// answer the agent is handed (dropResponseHeaders).
//
// It is ADVISORY. The workspace service refuses the call at the ceiling
// whatever a member's gateway believes: a member that never read a header is
// still cut off there.
const WorkspaceHeadroomHeader = "X-Fugaro-Workspace-Headroom"

// WorkspaceHeadroom reads the advisory headroom off an answer: what the
// workspace's ceiling still leaves, and whether the answer carried the header at
// all.
//
// An answer with no header is a workspace that states no ceiling, or a call the
// workspace's service did not pay for. The two are the same thing to a reader
// that only compares a call against the room, and neither is a ceiling of zero.
// A header that is not a non-negative integer is not a number the gateway acts
// on: it reports no headroom rather than guessing at one.
func WorkspaceHeadroom(h http.Header) (pricing.Micros, bool) {
	v := h.Get(WorkspaceHeadroomHeader)
	if v == "" {
		return 0, false
	}
	m, err := strconv.ParseInt(v, 10, 64)
	if err != nil || m < 0 {
		return 0, false
	}
	return pricing.Micros(m), true
}

// noteWorkspaceHeadroom records the room the workspace's service delivered on an
// answer, if the answer carried one. It is called where every other service
// answer header is read (Server.serviceAnswer), so the number is taken from the
// answer that delivered it and from nowhere else.
//
// The number is the room the service had left AFTER it settled the call the
// answer belongs to, so it is the room the NEXT call starts from. An answer that
// carries none leaves the last one where it was: a call the workspace's service
// did not pay for (a provider route, a token count) says nothing about the
// workspace's room, and reading its silence as "no room" would stop a run the
// workspace had room for.
func (s *Server) noteWorkspaceHeadroom(h http.Header) {
	m, ok := WorkspaceHeadroom(h)
	if !ok {
		return
	}
	s.mu.Lock()
	s.wsHeadroom, s.wsHeadroomSet = m, true
	s.mu.Unlock()
}

// DeliveredHeadroom reports the room the workspace's service last delivered,
// and whether it ever delivered one. It is named apart from the wire's reader
// so that a reader of this package can tell the number a header carried from
// the number this gateway is holding.
func (s *Server) DeliveredHeadroom() (pricing.Micros, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wsHeadroom, s.wsHeadroomSet
}

// WorkspaceStop is the advisory early stop's decision: whether a call whose
// worst case is w passes the room the workspace's service last delivered, and
// the sentence such a call is reported with when it does.
//
// It decides and nothing acts on it yet. The run-side stop is W8's ("the stop",
// docs/workspaces/implementation-plan.md) and it is not simply a call to this
// method: ending a run needs a halt reason and a scope that internal/runstore's
// closed vocabulary and internal/runner's halt classes do not have, and W7's
// file list carries no wiring file. So no caller in this tree passes this
// answer on, and the reading of it into a refusal belongs with that vocabulary
// rather than here. What is built is what W7's interface asks the gateway for:
// the number, and the comparison — never the cap.
//
// It is advisory in both directions, which is what "the gateway never decides
// the cap" means:
//
//   - the ONLY input is the number the service delivered. A gateway that has
//     been told nothing stops nothing, however much the run has spent: it holds
//     no workspace ceiling of its own to compare against, and the run's own cap
//     (Options.Cap) is a different quantity that keeps its own rule.
//   - a stop here is not the enforcement. The service refuses the call itself
//     when the ceiling is reached, whatever this gateway believes, so a member
//     whose headroom is stale or absent is stopped by the service and never
//     spends past the cap on this gateway's account.
//
// A call that needs exactly the room that is left fits; only a call that needs
// more is a stop.
func (s *Server) WorkspaceStop(w pricing.Micros) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.wsHeadroomSet || w <= s.wsHeadroom {
		return "", false
	}
	return fmt.Sprintf("fugaro: the workspace's own cap leaves %s and this call needs up to %s", dollars(s.wsHeadroom), dollars(w)), true
}
