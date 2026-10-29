package cli

import (
	"context"
	"slices"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// This file is the one exported surface the core offers the commands the owner
// moves out of it (design ee-boundary-moves.md §10). A moved command lives in
// another package, so it cannot reach cloudEnv, cloudOptions or the helpers
// beside them; it opens a cloud through OpenCloud and reads what it needs from
// the opaque *Cloud handle. The list is closed: a command that needs something
// outside it adds the helper HERE, where it is readable, rather than reaching
// into the core from across the boundary. Every function below is a thin
// wrapper over the core's own implementation, so the two cannot drift.

// CloudFlags are the flags every command that talks to the cloud shares,
// opaque so that a moved command cannot read the project selection or the
// header behind them.
type CloudFlags struct{ o cloudOptions }

// AddCloudFlags registers --config, --project, --gcp-project and --region on
// cmd.
func AddCloudFlags(cmd *cobra.Command, f *CloudFlags) {
	addCloudFlags(cmd, &f.o)
}

// Cloud is an opened cloud environment: the resolved project config, the runs
// bucket and the compute backend, opaque so that a moved command cannot reach
// the endpoints or the opener behind them.
type Cloud struct{ env *cloudEnv }

// OpenCloud selects the project config and connects to the backend and the
// runs bucket.
func OpenCloud(ctx context.Context, f *CloudFlags) (*Cloud, error) {
	env, err := openCloud(ctx, f.o)
	if err != nil {
		return nil, err
	}
	return &Cloud{env: env}, nil
}

// OpenFromConfig connects to the backend and the runs bucket of an already
// selected project config.
func OpenFromConfig(ctx context.Context, lc *localcfg.Config) (*Cloud, error) {
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	return &Cloud{env: env}, nil
}

// Config is the project config this cloud was opened with.
func (c *Cloud) Config() *localcfg.Config { return c.env.lc }

// NoBudgetCheck is --no-budget-check: the launch's own switch for skipping the
// budget pre-check.
func (c *Cloud) NoBudgetCheck() bool { return c.env.noBudgetCheck }

// RepoSlug is repo's slug, as run and ls take it.
func (c *Cloud) RepoSlug(ctx context.Context, repo string) (string, error) {
	return repoSlugOf(ctx, c.env.lc, repo)
}

// BudgetDB is the project's budget database, checked to be the database of the
// project the cloud was opened with: a config whose budget.rtdb_url points at
// another project's database is refused, exactly as it is on the paths that
// open one in the core.
func (c *Cloud) BudgetDB(ctx context.Context) (*rtdb.Client, error) {
	lc, db, err := budgetDBFor(ctx, c.env.lc)
	if err != nil {
		return nil, err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, err
	}
	return db, nil
}

// DBErr classifies an error of a database call: a refusal is a user error
// (exit 1), everything else, an outage included, a remote one (exit 2).
func (c *Cloud) DBErr(err error, write bool) error { return dbErr(c.env.lc, err, write) }

// Firestore is the client of the project's spend history. A project with no
// Firebase backend is refused here rather than dereferenced: the core's own
// reader checks the same two fields before it reaches for one, and a project
// that never ran `fugaro update --firebase-project` has no history to read.
func (c *Cloud) Firestore(ctx context.Context) (*firestore.Client, error) {
	lc := c.env.lc
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return nil, userErr("project %s has no Firebase budget backend (budget.firebase_project is not set in its project config), so it has no spend history: an operator runs fugaro update --firebase-project %s --name %s", lc.Name, lc.GCPProject, lc.Name)
	}
	return newReportFirestore(ctx, lc)
}

// FirestoreErr classifies a Firestore read failure: a refusal names the role.
func (c *Cloud) FirestoreErr(err error) error { return firestoreErr(c.env.lc, err) }

// UserErr is a user error: ExitCode maps it to exit 1.
func UserErr(format string, args ...any) error { return userErr(format, args...) }

// RemoteErr marks err as a remote failure: ExitCode maps it to exit 2.
func RemoteErr(err error) error { return remote(err) }

// ClipRunes is s cut to n runes.
func ClipRunes(s string, n int) string { return clipRunes(s, n) }

// PaidCommandNames are the command names whose code the owner moves out of the
// core. A reader, not a seam: the commands a build holds are its own business,
// so there is no setter and a caller cannot change the list the stubs are built
// from (design docs/design/ee-boundary-moves.md §9, D536).
//
// The core's skill checks read it to tell a name the released binary answers to
// — the core command today, a registration after the move, the stub in a build
// carrying none of that code — from a name no build has, and to hand a paid
// name's flags to the tagged tree, where the command that carries them is
// (ee/plugincheck). It answers a copy, so a caller that appends to it changes
// nothing.
func PaidCommandNames() []string { return slices.Clone(paidCommandNames) }
