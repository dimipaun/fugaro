package cli

import (
	"context"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runindex"
)

// runindex_wire.go is I-INDEX (D3205): the one place the run index's real
// Reader is built, so `runs ls`, `plan status`, `runs stats` and `fugaro graph`
// read the documents the history job's fold writes (RI3) instead of C-INDEX's
// stub.
//
// The reader costs two clients and no new credential. The Firestore one is the
// same client the spend history and doctor's line about the index are read
// with (newReportFirestore), and the Realtime Database one is the budget
// database the installation already has, where the overlay's live set and its
// recent outcomes live (D3201, D3202). Both are opened per invocation and
// closed by the process's own exit, as every other read of them is.

// runIndexReader is the index every read of a project's history goes through:
// `runs ls` and its `--pr` reads, `plan status`, `runs stats`, and the launch
// gate a pull request's runs are decided from. It is a var because which reader
// a read gets is one answer for the whole CLI, and the tests replace it with a
// fake.
var runIndexReader = func(ctx context.Context, env *cloudEnv) (runindex.Reader, error) {
	return openRunIndex(ctx, env)
}

// RunIndex is the project's run index, for a caller outside the core: ee's
// `graph` reads the coordinator scope's runs half from it (IR3). It is the
// same reader the core's own commands read, so the two cannot answer one
// question two ways.
func (c *Cloud) RunIndex(ctx context.Context) (runindex.Reader, error) {
	return openRunIndex(ctx, c.env)
}

// openRunIndex is the index of the installation env names: the Firestore
// documents the history job's fold writes, paged with the Realtime Database's
// overlay merged in.
//
// An installation that names no database for an index to live in
// (noIndexDatabase: no budget.firebase_project, or a no-auth config that names
// no endpoints.firestore either) has no index to read, and the read gets
// emptyIndexReader rather than an error. Every reader already has a path for
// "the index has nothing to answer with", so such an installation behaves
// exactly as one whose index the fold has not reached yet, which is what it is
// -- rather than a fourth shape each command would have to learn. A client that
// cannot be opened at all -- no credentials, a URL the config refuses -- is
// still that error, and the command that asked decides: `runs ls` says it could
// not read the index and lists the window, which is what every listing did
// before the index existed.
func openRunIndex(ctx context.Context, env *cloudEnv) (runindex.Reader, error) {
	if noIndexDatabase(env.lc) {
		return emptyIndexReader{}, nil
	}
	fs, err := newReportFirestore(ctx, env.lc)
	if err != nil {
		return nil, err
	}
	_, db, err := budgetDBFor(ctx, env.lc)
	if err != nil {
		return nil, err
	}
	return runindex.NewFirestore(fs, db), nil
}

// noIndexDatabase is whether the installation lc names no database the run
// index could be read from or written to: the index lives in the installation's
// Firestore, named by budget.firebase_project and reached with the person's own
// credentials or, in a no-auth config, at endpoints.firestore, so a config that
// names neither names no index at all. It is the one answer to that question,
// asked by the read (openRunIndex) and by `fugaro update`'s reindex step
// (update_runindex.go), which must not try to rebuild an index the
// installation has no room for.
func noIndexDatabase(lc *localcfg.Config) bool {
	return lc.Budget == nil || lc.Budget.FirebaseProject == "" ||
		(lc.Endpoints.NoAuth && lc.Endpoints.Firestore == "")
}

// emptyIndexReader is what a read gets from an installation with no index to
// read at all: an empty page, and a summary that has never finished a walk.
//
// It is not "an empty index" and it is deliberately not nil. Every reader of
// runindex.Reader already answers this shape the way it must -- `runs ls`,
// `--pr` and `runs stats` read the window, and `plan status` and `graph` say
// the index is still building rather than printing a short list as the truth
// -- so an installation without a Firestore database behaves exactly as one
// whose index the fold has not reached yet, which is what it is.
type emptyIndexReader struct{}

func (emptyIndexReader) List(context.Context, runindex.Filter) (runindex.Page, error) {
	return runindex.Page{Entries: []runindex.Entry{}}, nil
}

func (emptyIndexReader) Summary(context.Context) (runindex.Summary, error) {
	return runindex.Summary{Version: runindex.Version, States: map[string]int64{}}, nil
}
