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
//
// A SERVICE ACCOUNT's pool is D3181's, and it is the one place this file reads a
// pool's own principal to resolve one (PL6): the published pool whose principal
// that account is — the project pool an admin fills — is the launcher's where
// the account has designated none, which is the state it is normally in (a
// designation is a launcher's own session's act, and a service account's runs
// are launched by a pipeline). It never draws on a person's pool (D2976), and
// the two cases the plan names come out of the same reading: a run that resumes
// a person's paused run carries THAT person in its slip's launcher (R8, D3191)
// and is resolved as a person's, and a run the orchestrator reissues carries the
// launcher of the original slip and the pool that launch named — the
// orchestrator's own class never decides either.

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
// does not hold and this principal has not declared as one of its own (PL9), a
// pool this launcher does not own, and a pool with no members (D3180). None of
// them is an error: the walk moves on to the chain's next source with the step
// flagged a fallback.
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
		// names (R26) — so it is not this run's pool name, and what answers
		// below is what answers a principal that designated nothing.
		if found && d.Project == project {
			poolName = d.Pool
		}
	}
	pool, ok := poolForLauncher(poolName, shared, principal)
	if ok {
		if !poolServesPrincipal(pool, principal) {
			return none, false, nil
		}
		if len(pool.Members) == 0 {
			// An empty pool is "none" and never an error (D3180): it is a pool
			// its owner has named and not filled, and a run whose chain reaches
			// one falls back rather than halting.
			return none, false, nil
		}
		return poolRef(pool, project), true, nil
	}
	// PL9: a person's own pool is not a pool this project PUBLISHES, so a name
	// the node holds NONE of may still be one this launcher owns. The store is
	// keyed on the subject this service verified, so the read below can reach a
	// pool of the caller's own alone (R9, R26).
	//
	// The two reasons poolForLauncher answers false are told apart here, and it
	// is what keeps a personal pool from shadowing a name the node owns: a pool
	// the node HOLDS and this launcher is not served by is a refusal (D3176),
	// and falling through to the store for it would answer a pool the chain then
	// re-reads out of a published block by NAME — serving that block's members
	// and not the person's own (chain.go's planSource).
	if poolHeldByNode(poolName, shared) {
		return none, false, nil
	}
	return personalPoolRef(ctx, l.s, poolName, principal, project)
}

// poolHeldByNode reports whether the published node holds a pool of this name in
// EITHER of its two blocks (PL9, R26).
//
// The two blocks are not one list, and this is the whole of why both are asked:
// this lookup resolves a launcher's pool against `model_routing.pools` (C2's
// shape, launcherPoolsOf), and the CHAIN resolves a named pool against
// `craft_pools` (chain.go's planSource, namedPool) — so a name the key does not
// hold may still be one the chain would resolve, and a person declaring a pool of
// that name would then be served the published pool's members instead of their
// own container. A name either block holds is the node's, and a personal pool of
// it is refused rather than read.
func poolHeldByNode(name string, shared *projectpolicy.Policy) bool {
	if name == "" || shared == nil {
		return false
	}
	if _, held := namedLauncherPool(name, shared); held {
		return true
	}
	_, held := namedPool(name, shared)
	return held
}

// personalPoolRef resolves one PERSON's own pool out of the service's store
// (PL9, R26): the record the service kept for the subject it verified, as the
// reference the walk is answered with.
//
// The reference names the pool and carries no member list — that is what a
// PoolRef is — so the chain that serves the run reads the pool's members out of
// the same record (chain.go's planSource). It is what makes "the runs you launch
// may be served from it" true on the serving side: the pool is designated like
// any other, and the source that answers it is read where the published pools
// are not (a block this pool was never in).
func personalPoolRef(ctx context.Context, s *Server, name string, principal routing.Principal, project string) (routing.PoolRef, bool, error) {
	rec, found, err := personalPoolFor(ctx, s, principal, name, project)
	if err != nil || !found {
		return routing.PoolRef{}, false, err
	}
	if len(rec.Members) == 0 {
		// A pool its owner has declared and not filled is "none" and never an
		// error, exactly as a published pool with no members is (D3180).
		return routing.PoolRef{}, false, nil
	}
	return routing.PoolRef{
		Project:   project,
		Pool:      rec.Pool,
		Rule:      rec.Rule.RuleOrDefault(""),
		Principal: principal,
	}, true, nil
}

// personalPoolFor reads one person's own pool out of the service's store, and is
// the one read both halves of PL9 make — the lookup above and the route that
// designates a pool (designation_route.go).
//
// found is false for every state that is not this principal's own pool in this
// project: a name that is empty, a principal that is not a person (a service
// account's runs draw on the project's pools, D2976, D3181), a pool this
// principal has not declared, and a record that answers for another project (a
// personal pool's members are one project's per-user containers).
func personalPoolFor(ctx context.Context, s *Server, principal routing.Principal, name, project string) (PersonalPoolRecord, bool, error) {
	if s == nil || name == "" || project == "" || principal.Kind != routing.PrincipalPerson {
		return PersonalPoolRecord{}, false, nil
	}
	db, ok := s.cfg.DB.(creds.DB)
	if !ok {
		return PersonalPoolRecord{}, false, errNoDesignationStore
	}
	rec, found, err := readPersonalPool(ctx, db, principal, name)
	if err != nil || !found {
		return PersonalPoolRecord{}, false, err
	}
	if rec.Project != project {
		return PersonalPoolRecord{}, false, nil
	}
	return rec, true, nil
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

// launcherPoolsOf is the pools this lookup resolves against: the migrated
// node's `model_routing` block, in C2's shape (ModelRouting.Pools).
//
// It is NOT `craft_pools`, and that is the whole of why this function exists.
// The two blocks carry the same pools, and `craft_pools` is the one whose
// readers are a release behind — every publish strips a pool's rule, principal
// and threshold out of it (internal/cli's publishedCraftPools: a job image of
// the release behind decodes that block strictly and would refuse the whole
// node for a key it does not know) and writes them under the key instead (RT4).
// A principal read from `craft_pools` is therefore a member that is never there
// on an installation anyone published: the pool would answer nobody and every
// launcher's run would fall back to a paid source. The node's own type says the
// same thing from the other side — these are "the project's pools in C2's
// shape, and they are what the service resolves a source of kind pool against".
//
// nil is a node that carries no key at all, which `migrated` has already
// refused one step above: a pool of an installation `fugaro update` has not
// migrated has no principal to resolve, so there is nothing here to read.
func launcherPoolsOf(shared *projectpolicy.Policy) []config.Pool {
	if shared == nil || shared.ModelRouting == nil {
		return nil
	}
	return shared.ModelRouting.Pools
}

// poolForLauncher is the pool one launcher's run is served from: the pool its
// launch named with `--pool`, or, where the launch named none, the principal's
// designation — and, where a service account has designated none, the published
// project pool whose principal it is (D3181).
//
// An empty name reaches the second two cases and never the pool a policy holds
// under an empty name: a pool's name is a member a validator holds to the
// credential-name grammar (PL1), so `namedLauncherPool` answers no pool for one,
// and the service-account search below is what a run that named nothing is
// resolved by.
func poolForLauncher(poolName string, shared *projectpolicy.Policy, principal routing.Principal) (config.Pool, bool) {
	if poolName != "" {
		pool, ok := namedLauncherPool(poolName, shared)
		if !ok || !poolServesPrincipal(pool, principal) {
			return config.Pool{}, false
		}
		return pool, true
	}
	pool, ok := poolOfServiceAccount(shared, principal)
	if !ok || !poolServesPrincipal(pool, principal) {
		return config.Pool{}, false
	}
	return pool, true
}

// namedLauncherPool is the pool of that name as this lookup reads one, over the
// same block poolForLauncher's other case searches (launcherPoolsOf).
func namedLauncherPool(name string, shared *projectpolicy.Policy) (config.Pool, bool) {
	for _, pool := range launcherPoolsOf(shared) {
		if pool.Name == name {
			return pool, true
		}
	}
	return config.Pool{}, false
}

// poolOfServiceAccount is the project pool a service account's runs are served
// from where the account has designated none (D3181, PL6): the published pool
// whose principal that account is — the one an admin fills, because a run of a
// pipeline holds no session to fill its own.
//
// The FIRST such pool is the answer where a project publishes several, in the
// order the installation wrote them: each is that account's own by its own
// statement, so none of them is a pool the account's runs may not draw on, and
// the order is the one an operator can read back.
//
// A person is answered no pool here: a person's run is resolved from that
// person's designation and never from a pool that merely names them, which is
// PL4's rule and stays PL4's (D3180).
func poolOfServiceAccount(shared *projectpolicy.Policy, principal routing.Principal) (config.Pool, bool) {
	if principal.Kind != routing.PrincipalServiceAccount {
		return config.Pool{}, false
	}
	for _, pool := range launcherPoolsOf(shared) {
		own := principalOfPool(pool)
		if own.Kind == routing.PrincipalServiceAccount && sameAccount(own.ID, principal.ID) {
			return pool, true
		}
	}
	return config.Pool{}, false
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
	if own.Kind == routing.PrincipalServiceAccount && principal.Kind == routing.PrincipalServiceAccount {
		// The two addresses are compared folded (sameAccount), because that is
		// how the roster admits the account's own token in the first place: a
		// pool written with the address spelled otherwise is the same account's,
		// and failing to see that would fall a run back to a paid source over
		// the case of a letter.
		return sameAccount(own.ID, principal.ID)
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
