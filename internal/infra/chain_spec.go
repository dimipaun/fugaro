package infra

// This file is where the project's routing table answers the cloud side's
// questions: whether a repository's runs are the credentials service's at all,
// whether a job's account is planned the Vertex permission, and whether the
// installation has to plan Vertex for the service's own identity.
//
// It reads the table, the pools and nothing of agent.auth, and that is the
// point of the migration (docs/model-credentials-routing): the switch is the
// release-1 bridge, and a table replaces it one repository at a time. The two
// answers are separate functions rather than one branch of the switch because
// they come apart — a repository with a table is the table's answer whatever
// its file still names, and a repository without one is still the file's — and
// because only the first is the owner's to change (O7: retiring the flip list
// is not this release's).

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/routing"
)

// ChainPlan is what the table decides for ONE repository, which is what a
// repository root's plan reads: whether the project publishes a table at all,
// whether a chain of it may serve this repository, and whether a chain that may
// serve it reaches Vertex.
//
// Serves is the fact the run's whole model access turns on: a repository the
// table serves mounts no model credential and its job is told about the service
// (R1, R2 — the run's gateway holds no key and no table). It is answered here,
// at plan time, and not left to the run: what a job mounts and what the launch
// tells it are one plan, and a job that mounted a credential it was told to ask
// the service for is a job Cloud Run refuses to create (the account's accessor
// and the mount go together).
type ChainPlan struct {
	// Migrated is whether the project publishes a table. It is the same marker
	// PL3 reads out of the published node (`model_routing` present), read here
	// from the local config the plan is built from — the document `fugaro
	// update` publishes from, so the two cannot say different things about one
	// installation.
	Migrated bool
	// Serves is whether a chain of the table may serve this repository: some
	// source of some entry admits it. False for a project with no table and for
	// one whose every source is admitted to another repository — the second is
	// the state MG1's migration leaves a repository it cannot place, which runs
	// on the release-1 bridge until it is placed.
	Serves bool
	// Vertex is whether a chain that may serve this repository reaches Vertex.
	// It is per repository and not per project: it is this repository's runs
	// that land on the project's Vertex, and X8 reads it to take the
	// permission away repository by repository.
	Vertex bool
}

// ChainPlanFor is the plan for one repository: the table and the pools, read
// for the repository's own name.
//
// A source is read for the admission its kind carries. The three kinds that
// name a single credential of the project — anthropic, provider and vertex —
// carry their own admit list, and an empty one admits nobody (D3182: the
// fail-closed direction, the same rule a Claude account's own allow_data_to
// already has). A pool and the launcher's own pool carry none: a pool is
// admitted by its members' own allow_data_to, and the launcher's pool by the
// designation the launcher makes per run (D3176, D3180), so both are the walk's
// answer at call time and neither is a fact this plan can read. Saying they
// serve is therefore the only plan-time answer, and the fail-closed one it
// gives up is given up by the contract, not here: a source with no usable
// member is skipped at the call, where the run has nothing to read and the call
// is refused.
func ChainPlanFor(lc *localcfg.Config, repo string) ChainPlan {
	plan := ChainPlan{Migrated: len(lc.ModelRouting) > 0}
	if !plan.Migrated {
		return plan
	}
	for _, entry := range lc.ModelRouting {
		for _, s := range entry.Chain {
			if !chainSourceServes(s, repo) {
				continue
			}
			plan.Serves = true
			if chainSourceReachesVertex(s, lc.Pools, lc.Credentials) {
				plan.Vertex = true
			}
		}
	}
	return plan
}

// ChainsNameVertex reports whether any chain of any entry names Vertex,
// whatever repository it admits: the installation-wide question, and the one
// the Vertex AI API and the credentials service's own permission are planned
// from.
//
// It is deliberately not per repository, and deliberately not the recorded
// per-repository fact (localcfg.Repo.Vertex, which `fugaro update` writes out
// of RepoSpec.UsesVertex): the service is the installation's, its account is
// the installation's, and a chain that names Vertex is a call the service may
// be asked to make for a repository whose own file says nothing about Vertex —
// a run admitted to a project's Vertex source is exactly that. So the
// permission is planned where the table names Vertex, and the two per-repo
// facts say whether a JOB needs the grant (the recorded fact, read at its
// face; the plan's own Vertex, read under enforce).
func ChainsNameVertex(lc *localcfg.Config) bool {
	for _, entry := range lc.ModelRouting {
		for _, s := range entry.Chain {
			if chainSourceReachesVertex(s, lc.Pools, lc.Credentials) {
				return true
			}
		}
	}
	return false
}

// CredentialsServiceServesChains reports whether the table is what puts a
// repository's runs on the credentials service, the table's arm of the switch
// beside CredentialsServiceServes' own.
//
// The installed-and-ready pair is the same one the switch reads, and for the
// same reason: a table is no service (an installation that never opted in has
// nothing to hand a run an address for, and a repository the update has not
// recorded a bundle for is one whose runs C1.19's slip has no admission to), so
// a table on such an installation plans no job to reach anything.
//
// What it does NOT read is agent.auth, which is the whole of this arm: a table
// serves a repository the switch never would — a `vertex` repository above
// all, which has no credential to hand over and is told about the service only
// because its chain names Vertex, the service calling Vertex on its own
// identity (D3174).
func CredentialsServiceServesChains(installed, ready bool, plan ChainPlan) bool {
	return installed && ready && plan.Serves
}

// VertexGrantForJob reports whether a job's account is planned the Vertex
// permission on a project whose budget mode is mode.
//
// Under `enforce` it is not, and that is CS11's third rule: no run in flight
// can hold the permission there, because a `vertex` run that would authenticate
// to Vertex with its own job account is refused at bootstrap
// (runner.VertexBudgetRefusal, internal/runner's checkBudget) — the one
// `vertex` run an enforcing project still starts is a run the credentials
// service serves, whose Vertex call is the service's own (CS5, D3174) — so the
// grant release 1 takes away is one nothing can be using, and the run that
// would have used it is one the gateway stops first. Release 2
// (X8) takes it away in every mode, which is the change an `observe` project
// waits for: there a run in flight really does authenticate to Vertex with its
// own job account, and a plan that took the grant away would break it
// mid-flight.
//
// The empty mode — a caller that read no node (fugaro image, doctor,
// --print-vars) — keeps the grant. It is not "not enforcing" by defaulting but
// by the direction the whole file fails in: this answer drives the removal of a
// permission a run in flight may be using, and a plan built without knowing the
// project's mode must not be the plan that breaks one.
//
// One function because the two plan paths hold different documents of the same
// fact — a repository root reads the published node (Inputs.Policy), the
// installation reads its own record (localcfg.Budget.Mode) — and a second
// spelling of this rule would be a second answer to it.
func VertexGrantForJob(mode string) bool {
	return mode != config.BudgetEnforce
}

// reachesVertex reports whether this repository's runs reach Vertex at all:
// its file's own agent block, or a chain of the table that may serve it naming
// Vertex. It is RepoSpec.ReachesVertex and RepoSpec.UsesVertex, and it is read
// where the INSTALLATION is planned — the API and the credentials service's own
// permission — never where a job's grant is (that is VertexGrantForJob's
// question, and the two answers come apart under `enforce`).
func (c *repoCtx) reachesVertex() bool {
	return c.in.Cfg.Agent.Auth == "vertex" || c.chain.Vertex
}

// chainSourceServes reports whether a source may serve repo: its own admit list
// for the kinds that carry one, and true for the two pool kinds, whose
// admissions are walked at the call (ChainPlanFor).
func chainSourceServes(s routing.Source, repo string) bool {
	switch s.Kind {
	case routing.KindLauncherPool, routing.KindPool:
		return true
	default:
		return slices.Contains(s.Admit, repo)
	}
}

// chainSourceReachesVertex reports whether a source of the chain is Vertex: the
// vertex kind itself, or a pool one of whose members is the built-in Vertex
// credential (config.CredentialVertex, D1624 — the one name a pool may list
// without the installation defining it, which config.PoolMembers reads as
// Vertex for exactly that reason).
//
// The provider kind is never it. A provider's key is an API key of that
// provider's own service; the models it serves are its own, whatever Claude
// spelling they carry, and no call of it is made on the project's GCP identity.
//
// The launcher-pool kind is true, and it is the one arm that differs from its
// sibling in chainSourceServes in the other direction: a launcher-pool source
// names NO pool in the table (routing.Source.Pool is empty for it — which pool
// serves is decided per run, D3176, and the plan cannot follow it), so there is
// no member list here to walk and no name to look up. The answer is the walk's
// own direction rather than a read: a pool a launcher may designate is one that
// may hold the built-in Vertex credential, so this file says the chain may reach
// Vertex and the API is enabled and the service's role planned for it. Inert
// until PL4 replaces the launcher lookup (the shipped internal/credsvc stub
// answers found=false for every principal, so nothing designates a pool today),
// and it is written down here so the next reader does not take it for an
// oversight: the alternative — false — would leave a run the launcher sends to a
// Vertex-holding pool with no API and no grant.
func chainSourceReachesVertex(s routing.Source, pools []config.Pool, creds []config.Credential) bool {
	switch s.Kind {
	case routing.KindVertex:
		return true
	case routing.KindPool:
		p, ok := chainPool(pools, s.Pool)
		return ok && config.PoolHasVertexMember(p, creds) != ""
	case routing.KindLauncherPool:
		return true
	}
	return false
}

// chainPool is the pool the project names by that name. A name no pool of the
// config carries is not the caller's refusal: the table is validated where it
// is written and published (localcfg.validateModelRouting refuses a pool source
// with no name, and RT3's checks judge the table as a whole), and this reader
// answers the fail-closed direction for a chain it cannot follow — a source
// that reaches no pool reaches no Vertex.
func chainPool(pools []config.Pool, name string) (config.Pool, bool) {
	for _, p := range pools {
		if p.Name == name {
			return p, true
		}
	}
	return config.Pool{}, false
}
