package budget

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// LedgerReader reads one run's ledger by name. It is the whole seam a chain
// read needs of the budget database — one named get per run, never a listing —
// so a caller's tests stand a fake in for it.
type LedgerReader interface {
	ReadLedger(ctx context.Context, slug, run string) (RunLedger, bool, error)
}

// LedgerDB is the LedgerReader a launcher uses. The launcher is the identity
// that reads a chain: it holds roles/firebasedatabase.viewer, which reads every
// path, while a run's own token reads /runs/<slug>/<run> for the run it was
// minted for and no sibling's (U4). A runner therefore reads its own ledger
// through this and takes the chain's other figures from the launch.
type LedgerDB struct{ DB *rtdb.Client }

// ReadLedger reads /runs/<slug>/<run>. found is false for a node that is not
// there; err is the read's own failure. The two are kept apart because a chain
// prints which of them it hit, even though D1049 charges both the same — the
// run's full per-run cap, never zero — so a caller that collapsed them could
// not say which read it was reporting.
//
// An empty slug or run id panics in the path builder, which is the guard
// against a segment that would collapse the path onto another run's ledger.
func (l LedgerDB) ReadLedger(ctx context.Context, slug, run string) (RunLedger, bool, error) {
	path := PathRun(slug, run)
	var out RunLedger
	found, err := l.DB.Get(ctx, path, &out)
	if err != nil {
		return RunLedger{}, false, fmt.Errorf("reading /%s: %w", path, err)
	}
	return out, found, nil
}
