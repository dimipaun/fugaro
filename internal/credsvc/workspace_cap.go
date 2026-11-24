package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The workspace service's meter: the workspace's own cap, taken where the money
// is spent (docs/workspaces/spec.md §5, D1301 as amended 2026-11-04). The
// service reserves the room for a call it pays for BEFORE it makes the call and
// settles on the response's usage, in the workspace's OWN database
// (internal/budget/workspace.go holds the transaction), and it answers each
// member with the advisory headroom the ceiling still leaves.
//
// THE METER IS WIRED TO THE CALL. internal/credsvc/forward.go reserves a paid
// call's worst case before the request goes out — the probe the cascade owes a
// backed-off key included, since a probe is a call the workspace pays for too
// (C1.16) — settles it on the usage the answer reported, refuses the CALL when
// that settle cannot be written, and writes the headroom on the answers it
// writes itself: a served call (cascade.go's answer) and a refusal the meter
// made (forward.go's refuseMeter). A refusal that never reached the meter —
// refuseSelection's wait and no-candidates answers, the call a credential has
// already been sent to — carries no headroom, and nothing depends on one there.
// Its tests are meter_test.go, one per promise, and its own task is WS8 of
// docs/workspace-service/implementation-plan.md.
//
// ONE CODE PATH, TWO DEPLOYMENTS, AND BOTH ARE BUILT HERE. The workspace's own
// scope is WS2's (scope_workspace.go, whose init assigns the real constructor
// over C1's stub), the workspace's own service is deployed by `fugaro update`
// from deploy/terraform/gcp/roots/workspace (WS5, WS6, WS7), and Scope.Meter is
// non-nil for it — so a service in workspace mode reserves every paid call and
// settles it. An installation's scope is the other deployment, and its Meter is
// nil, which is exactly what R1 keeps: the same forwarding path serves both and
// the meter in the scope is the whole of what differs — an installation
// reserves nothing, settles nothing and writes no header. meter_test.go drives
// that path through a scope with a meter over it.
//
// Three things about the shape are the design and not an accident:
//
//   - the figure is the WORKSPACE's, and no member's. The member's own ledger
//     stays authoritative for the member's own caps; the two are different
//     quantities, each governing its own cap, and what compares them is the
//     reconciliation alarm (internal/budget/workspace_reconcile.go) rather than
//     anything here.
//   - the ceiling is read from the workspace's own record (`workspaces/<name>`,
//     projectpolicy.PathWorkspace), which `fugaro update` publishes, on every
//     question rather than cached: an owner who narrows the cap has narrowed it
//     for the next call, and the meter is given no cap of its own that could
//     drift from the published one.
//   - the meter holds no write on any member. Its database is the workspace's,
//     and what a member is told travels on the answer the service is already
//     sending.
type WorkspaceMeterConfig struct {
	// Name is the workspace's name: the project name of the installation that
	// published it, which is also the last segment of its record's path.
	Name string
	// DB is the workspace's OWN database: the one the record is read from and
	// the one the figure is written to. A member's database is never one, which
	// is what lets the service hold no write on a member at all.
	DB budget.WorkspaceSpendDB
	// Now is the clock the month is taken from; nil is time.Now.
	Now func() time.Time
}

// WorkspaceMeter takes the room for a call and records what it cost. It is safe
// for concurrent use: every reservation and every settle is one conditional
// write of the month's node, so two calls at the ceiling's boundary cannot both
// take the last of the room.
type WorkspaceMeter struct {
	name string
	db   budget.WorkspaceSpendDB
	now  func() time.Time
}

// NewWorkspaceMeter builds the meter. A service that could serve a call it
// cannot meter is a service that could pass the workspace's cap, so a
// configuration without the workspace's own database is refused here rather
// than at the first call.
func NewWorkspaceMeter(cfg WorkspaceMeterConfig) (*WorkspaceMeter, error) {
	if cfg.Name == "" {
		return nil, errors.New("credsvc: a workspace meter names the workspace whose figure it keeps")
	}
	if cfg.DB == nil {
		return nil, errors.New("credsvc: a workspace meter needs the workspace's own database, which is the one it writes; a member's is not one")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &WorkspaceMeter{name: cfg.Name, db: cfg.DB, now: now}, nil
}

// Ceiling is the workspace's own ceiling, read from the workspace's own record.
// A record that states no cap is no ceiling — never a ceiling of zero, which
// would refuse every call the workspace's credentials pay for — and a record
// that cannot be read at all is an error, because a service that cannot know
// the ceiling must not spend against it.
//
// A record that is ABSENT is read the same way, as no ceiling, and it is not a
// hole through the cap: the workspace's member table lives in that record, so a
// workspace with none binds no member, resolves no caller and reaches this
// meter with no call at all (the front door, internal/credsvc's own). A record
// that is there and unreadable is the error above.
func (m *WorkspaceMeter) Ceiling(ctx context.Context) (budget.WorkspaceCeiling, error) {
	var raw json.RawMessage
	found, err := m.db.Get(ctx, projectpolicy.PathWorkspace(m.name), &raw)
	if err != nil {
		return budget.WorkspaceCeiling{}, err
	}
	if !found {
		return budget.WorkspaceCeiling{}, nil
	}
	w, _, err := projectpolicy.DecodeWorkspace(raw)
	if err != nil {
		return budget.WorkspaceCeiling{}, err
	}
	if w.Cap == nil {
		return budget.WorkspaceCeiling{}, nil
	}
	return budget.WorkspaceCeiling{Monthly: w.Cap.MonthlyMicros, Set: true}, nil
}

// Room answers the headroom question for this moment: the ceiling the
// workspace's record states, what the calls that have settled cost this month,
// and the room the calls in flight hold. It is the number a member is answered
// with, and it is read through the read-only half of the database.
func (m *WorkspaceMeter) Room(ctx context.Context) (budget.WorkspaceRoom, error) {
	ceiling, err := m.Ceiling(ctx)
	if err != nil {
		return budget.WorkspaceRoom{}, err
	}
	return budget.ReadWorkspaceRoom(ctx, m.db, m.name, ceiling, m.now())
}

// Reserve takes the room for one call out of the workspace's month, before the
// call is made. The call is identified by call, which must be unique to it: a
// second reservation for the same call is refused rather than taken twice.
//
// A call that would pass the ceiling comes back as budget.ErrWorkspaceCeilingReached
// with the room that refused it, so a caller can refuse the call and still
// answer the member with the headroom. Any other error means the room could not
// be taken at all, and the call must be refused too: a call served without a
// reservation is a call that can pass the cap.
func (m *WorkspaceMeter) Reserve(ctx context.Context, member, call string, amount pricing.Micros) (budget.WorkspaceHold, budget.WorkspaceRoom, error) {
	if member == "" {
		return budget.WorkspaceHold{}, budget.WorkspaceRoom{}, errors.New("credsvc: a reservation is for a member of the workspace, and the call resolved to none")
	}
	ceiling, err := m.Ceiling(ctx)
	if err != nil {
		return budget.WorkspaceHold{}, budget.WorkspaceRoom{}, err
	}
	return budget.ReserveWorkspaceSpend(ctx, m.db, budget.WorkspaceReserve{
		Name: m.name, Call: call, Member: member, Amount: amount, Ceiling: ceiling, Now: m.now(),
	})
}

// Settle records what the call cost, from the response's own usage, and ends
// the reservation the hold names.
//
// An error here is a call to refuse rather than to serve: a call whose cost the
// service cannot record spends against a cap it cannot see. The failure is not
// swallowed and the reservation is not released as zero, so the reservation is
// still there for the service's next attempt at the same call — and that retry
// is bounded by the hold's own age (budget.WorkspaceReservationTTL): a hold
// older than that is a call that cannot still be in flight, so it is released
// and a settle that arrives then finds no reservation and is refused like any
// other usage the meter cannot place.
func (m *WorkspaceMeter) Settle(ctx context.Context, h budget.WorkspaceHold, actual pricing.Micros) (budget.WorkspaceRoom, error) {
	ceiling, err := m.Ceiling(ctx)
	if err != nil {
		return budget.WorkspaceRoom{}, err
	}
	return budget.SettleWorkspaceSpend(ctx, m.db, h, budget.WorkspaceSettle{Actual: actual, Ceiling: ceiling, Now: m.now()})
}

// SetWorkspaceHeadroom writes the advisory headroom onto the answer to a member.
// A workspace that states no ceiling writes nothing: there is no number to
// deliver, and an absent header is what the member's gateway reads as "no
// ceiling" rather than as a ceiling of zero.
func SetWorkspaceHeadroom(h http.Header, room budget.WorkspaceRoom) {
	if !room.Ceiling.Set {
		return
	}
	h.Set(gateway.WorkspaceHeadroomHeader, strconv.FormatInt(int64(room.Headroom), 10))
}

// SetWorkspaceRefusal writes the workspace's own marker onto a refusal the
// workspace's service answers (gateway.WorkspaceRefusalHeader): which of the
// workspace's refusals it was, one of gateway.WorkspaceRefusals. It is the
// second half of the same wire SetWorkspaceHeadroom writes the first half of,
// and it exists for the run's record rather than for the service's decision:
// the refusal is the same refusal either way, and what the marker adds is that
// the member's run can say in its own record that the workspace refused it
// instead of recording the same halt a member's own service outage writes
// (docs/stateless-orchestrator/reconciliation.md U12).
//
// A reason outside the three is written as no marker rather than as a value a
// gateway this release would have to guess at: the vocabulary is closed
// (gateway.ValidWorkspaceRefusal), and a service that answered something else
// is answering a refusal that is not the workspace's own.
//
// TWO OF THE THREE ARE WRITTEN, and the third is not. The cap and the settle
// are the meter's own refusals, and the wiring above writes both: a call at the
// ceiling carries `cap` and a call whose cost could not be recorded carries
// `settle` (forward.go's refuseMeter). The stop is not written by anything this
// tree runs, and the missing thing is not a deployment: the workspace's own
// deployment exists and the service tells its two apart by
// creds.WorkspaceModeEnv (NewWorkspace's caller is the workspace mode in
// internal/cli), so what declines to write it is the plan, which decides that
// ending a run on the workspace's stop needs a halt reason the run record's
// closed vocabulary does not have — a wire change with its own readers — and
// leaves it the workspaces design's open check U4
// (docs/workspace-service/implementation-plan.md §10). So a member's run
// records the marker only against a service that writes it, and the tests
// beside this file stand in for the stop.
func SetWorkspaceRefusal(h http.Header, reason string) {
	if !gateway.ValidWorkspaceRefusal(reason) {
		return
	}
	h.Set(gateway.WorkspaceRefusalHeader, reason)
}
