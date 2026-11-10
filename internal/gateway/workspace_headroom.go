package gateway

import (
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
// leaves. It is ADVISORY. The gateway stops a run early on it and never decides
// the cap, which is the workspace service's alone: a member that never read the
// header is still refused by the service when the ceiling is reached.
const WorkspaceHeadroomHeader = "X-Fugaro-Workspace-Headroom"

// WorkspaceHeadroom reads the advisory headroom off an answer: what the
// workspace's ceiling still leaves, and whether the answer carried the header at
// all.
//
// An answer with no header is a workspace that states no ceiling, or a call the
// workspace's service did not pay for. The two are the same thing to a reader
// that only stops a run early, and neither is a ceiling of zero. A header that
// is not a non-negative integer is not a number the gateway acts on: it reports
// no headroom rather than guessing at one.
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
