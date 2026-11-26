package credsvc

// The launcher's pool (C2's lookup, D3176; plan task PL4).
//
// A table's `launcher-pool` source names no pool: which pool serves a run is
// decided per run, from the principal the run's SLIP carries and the pool its
// launch named with `--pool` (D3176, D3180) — the principal this service
// verified when it wrote the slip, and never anything the launch or the job
// said about itself (R7). This file is that decision, in place of the stub
// launcherpool_stub.go ships while the resolution is being built.
//
// Three documents meet here and each is answered by the part that owns it: the
// published policy (is this project migrated at all, and what pools does it
// hold), the principal's designation from internal/creds' own store, and the
// request's own `--pool` name. The entitlement is one rule for both the
// designated and the named case — poolServesPrincipal — so naming a pool can
// never widen what a run may be served from.

import (
	"context"
	"errors"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
)

// launcherPools is routing.LauncherPool as this deployment answers it: the
// lookup reads the Server the call is being served by, because the three
// documents above are that deployment's own.
type launcherPools struct{ s *Server }

// LauncherPool answers the pool one run's launcher is served from for one
// project.
//
// `found` false is "no pool designated" and it is the answer of every state
// that is not a pool this launcher may be served by: a principal that names
// nobody (the zero principal a resumed or orchestrator-launched call can
// carry), a project whose published policy carries no table (an installation
// `fugaro update` has not migrated, whose pools have no principal to resolve),
// no designation, a designation written for another project, a pool the node
// does not hold, a pool this launcher does not own, and a pool with no members
// (D3180). None of them is an error: the walk moves on to the chain's next
// source with the step flagged a fallback.
//
// An error is one thing only, and it is the opposite answer: a document that
// could not be read. A caller that read one as an absent designation would
// serve a fallback source where a pool exists.
func (l launcherPools) LauncherPool(ctx context.Context, principal routing.Principal, named string) (routing.PoolRef, bool, error) {
	none := routing.PoolRef{}
	if l.s == nil || !principal.Valid() {
		return none, false, nil
	}
	shared, ok, err := sharedPolicy(ctx, l.s)
	if err != nil {
		return none, false, err
	}
	if !ok || !migrated(shared) {
		return none, false, nil
	}
	project, ok := installationProject(l.s)
	if !ok {
		return none, false, nil
	}
	poolName := named
	if poolName == "" {
		d, found, err := readDesignation(ctx, l.s, principal)
		if err != nil {
			return none, false, err
		}
		// A designation answers for ONE project: one written for another
		// installation's is not this project's answer, whatever principal it
		// names (R26).
		if !found || d.Project != project {
			return none, false, nil
		}
		poolName = d.Pool
	}
	pool, ok := namedPool(poolName, shared)
	if !ok {
		return none, false, nil
	}
	if !poolServesPrincipal(pool, principal) {
		return none, false, nil
	}
	if len(pool.Members) == 0 {
		// An empty pool is "none" and never an error (D3180): it is a pool its
		// owner has named and not filled, and a run whose chain reaches one
		// falls back rather than halting.
		return none, false, nil
	}
	return poolRef(pool, project), true, nil
}

// sharedPolicy is the published policy of the deployment serving this call, as
// the call's own scope answers it: an installation's is the project's node, a
// workspace's is its member's, and a call that arrived with no scope at all —
// a unit test walking a chain — is read as the deployment's own.
func sharedPolicy(ctx context.Context, s *Server) (*projectpolicy.Policy, bool, error) {
	shared, found, err := scopeOfCall(ctx, s).Policy().Shared(ctx)
	if err != nil {
		return nil, false, err
	}
	if !found || shared == nil {
		return nil, false, nil
	}
	return shared, true, nil
}

// readDesignation reads one principal's designation through the deployment's own
// database. It is the read half of the store PL4's write route owns
// (internal/creds/designation.go), and it names the database's absence as an
// error rather than reading it as no designation: a service that cannot read
// the store cannot tell "no pool" from "I could not look", and the two answers
// decide whether a run falls back or waits.
func readDesignation(ctx context.Context, s *Server, principal routing.Principal) (routing.Designation, bool, error) {
	db, ok := s.cfg.DB.(creds.DB)
	if !ok {
		return routing.Designation{}, false, errNoDesignationStore
	}
	return creds.ReadDesignation(ctx, db, principal)
}

// poolServesPrincipal reports whether a pool is one this principal's runs may be
// served from (D3176, D3181): the pool's own principal is the launcher's, or it
// is the project's — the shared pool, which every account the migration wrapped
// became (D3183) and which takes contributions deliberately (D3194).
//
// A pool that names NO principal serves nobody here, and it is not a pool open
// to everyone: a pool an installation already holds carries no principal until
// the migration writes one (PL1, MG1), and the legacy `owner`/`allowed_users`
// pair keeps its meaning for the roster and the grant and decides nothing on
// this path (R21). Reading no principal as "anyone" would be a pool every
// launcher could name with --pool, which is the widening D3176 refuses. After
// the migration every published pool names a principal, so this is the whole of
// the rule for every pool a run can reach.
func poolServesPrincipal(pool config.Pool, principal routing.Principal) bool {
	if pool.Principal == nil || !principal.Valid() {
		return false
	}
	own := principalOfPool(pool)
	if own.Kind == routing.PrincipalProject {
		return true
	}
	return own == principal
}

// principalOfPool is a pool's principal as the wire's own type. The two
// vocabularies are one list (config.PrincipalKinds mirrors routing's), and a
// kind this release does not know is carried through rather than rewritten:
// PoolPrincipal.Valid refuses it at the publish, so a value here is one a
// hand-edit wrote, and comparing it to a verified principal can only fail.
func principalOfPool(pool config.Pool) routing.Principal {
	if pool.Principal == nil {
		return routing.Principal{}
	}
	return routing.Principal{Kind: routing.PrincipalKind(pool.Principal.Kind), ID: pool.Principal.ID}
}

// poolRef is one pool as the lookup answers it: the rule resolved (never "") and
// the threshold the pool carries, so the picker reads the pool's configuration
// no second time (PL3's contract). It carries names and never a value.
func poolRef(pool config.Pool, project string) routing.PoolRef {
	ref := routing.PoolRef{
		Project:   project,
		Pool:      pool.Name,
		Rule:      poolRule(pool),
		Principal: principalOfPool(pool),
	}
	if pool.Threshold != nil {
		t := routing.Threshold(*pool.Threshold)
		ref.Threshold = &t
	}
	return ref
}

// errNoDesignationStore is a deployment whose database cannot read a node: the
// service is built with one that patches only (Config.DB's narrow half), and
// this is a deployment that cannot answer "which pool serves this run" at all.
var errNoDesignationStore = errors.New("credsvc: the deployment's database cannot read a designation")
