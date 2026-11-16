package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/settings"
)

// This file is one half of the contract the writer families are built against:
// the hook a semantic command calls once its write has landed
// (docs/contracts/config-ownership.md, D2602). The other half is the writer
// registry in internal/settings/writer.go.
//
// The write and the publish are one act for a person — a pool that no run can
// be served by is not the pool they asked for — so a command that writes a key
// a node or a layer serves from runs the stages `fugaro update` runs, in
// process, before it returns (item 9, composite D1561/D1566 amended by D2602).
// What the command states is the write; which stages that write owes is the
// hook's own reading of the store it landed in (D2615), so no caller encodes a
// stage name and a stage renamed in initflow moves with no caller edited.

// afterWriteChange is what a semantic command hands runAfterWrite: the
// settings key it wrote, as `fugaro settings` spells it, and the store the
// value went into (settings.Store).
//
// The two fields are the whole of the interface on purpose. The store is the
// input of the stage choice — the policy stage for a key the node is served
// from, the layer stage for a project-layer key, both for a change that
// touches both (D2615) — and the key is what a stage's own refusal and the
// not-yet-live sentence name. A value never travels here: it is in the file
// the command wrote, and a second copy would be a second source.
type afterWriteChange struct {
	Key   string
	Store settings.Store
}

// afterWriteFunc runs the stages a write owes before the command returns. It
// is called with the command's own context and command, and it answers the
// error the command exits with: on a stage failure the local change stays and
// one sentence says it is saved and not yet live (composite D1563, from N3's
// one helper — D2614 — so that the day `fugaro apply` lands, the wording moves
// in one place).
type afterWriteFunc func(ctx context.Context, cmd *cobra.Command, change afterWriteChange) error

// runAfterWrite is the hook every semantic command calls after its write. It
// is N3's function (write_publish.go) once that task lands; until then
// after_write_stub.go holds the no-op that keeps every family built against
// this contract inert, and I1 deletes that file (see the contract document).
var runAfterWrite afterWriteFunc
