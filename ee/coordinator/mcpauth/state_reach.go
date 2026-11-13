// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"errors"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The state group's reach as the front door serves it (O16b): one project's
// orchestrator node, read over internal/rtdb as the front door's OWN account
// (D2020 (a), answered 2026-11-10).
//
// **What the account holds is the whole of what this file may reach.** The
// front door runs as `fugaro-mcp` (modules/installation/coordinator.tf), which
// holds the runs bucket's read, the store its own token records live in, and —
// since O16b — a custom role carrying `firebasedatabase.instances.get` and
// nothing else. Realtime Database IAM has no path-level scope, so that role
// reaches the whole database; what keeps this reach to the state group is the
// two paths below and the interface it satisfies: StateReach has no write
// method, and the Reader this reach is built over has no write to call.
//
// **The id is the project's own orchestrator, and the role is the reader's.**
// `orchestrator.ReadState` is handed RoleOrchestrator because that is the loop
// the project's node is kept by (the same one the CLI reads); a document
// another loop wrote is refused rather than answered as this project's state.
// The id itself was already checked against orchestrator.PathState by the tool
// that read it (stateID), so a value the database would refuse never reaches
// this type.
//
// Nothing here holds a clock: the log's `since` cut is resolved inside
// orchestrator.ReadAudit, on the database server's own time (D1230), so a
// duration a caller sends is never measured against the machine the front door
// happens to run on.
//
// The caller's identity is not read here either. Every role M7 admits to the
// tool set reads every orchestrator id of the project (the spec's read-all), so
// there is nothing for an identity to decide: what this reach may read is what
// its account's IAM admits, and the door's own audit line is what records who
// called (D920).
type ProjectState struct {
	db orchestrator.Reader
}

// The reach is what the tool set is registered over, and the interface is
// checked here rather than at the point a handler would notice.
var _ StateReach = (*ProjectState)(nil)

// NewProjectState builds the reach over the database the front door's account
// opens. The client is what the account's identity is already in (the process
// runs as that account, and the metadata server is where its token comes from),
// so this holds no credential of its own and nothing about the account moves
// with this call.
func NewProjectState(db orchestrator.Reader) (*ProjectState, error) {
	if db == nil {
		return nil, errors.New("mcpauth: the state reach needs the database it reads")
	}
	return &ProjectState{db: db}, nil
}

// Load answers one orchestrator's state as the node holds it, by the same read
// `fugaro orchestrator state` makes: the absent node is this orchestrator's
// empty state at this release's schema version rather than an error, and a
// document another id or loop wrote is refused.
func (r *ProjectState) Load(ctx context.Context, id string) (orchestrator.State, error) {
	return orchestrator.ReadState(ctx, r.db, id, orchestrator.RoleOrchestrator)
}

// Events answers the log under that id, in the order the server stamped it and
// cut at since when it is positive — the same read `fugaro orchestrator events`
// makes, so the CLI and the door answer one log the same way. The cut is not
// returned: this type is the interface's, and what the CLI prints of it
// (`since`) is the surface's own business (eventsDoc says why the tool does not
// carry it).
func (r *ProjectState) Events(ctx context.Context, id string, since time.Duration) ([]orchestrator.Entry, error) {
	read, err := orchestrator.ReadAudit(ctx, r.db, id, since)
	if err != nil {
		return nil, err
	}
	return read.Entries, nil
}
