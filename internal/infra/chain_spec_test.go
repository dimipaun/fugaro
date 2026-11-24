package infra

// CS11: the cloud side is planned from the table (docs/model-credentials-routing,
// implementation-plan.md). One test per clause of the task's acceptance:
//
//   - TestSpecFromTheTable — what a job mounts and whether it is told about the
//     credentials service come from the table and the pools and not from
//     agent.auth: no secret is mounted for a served source, the admission is
//     read per repository, and the release-1 bridge is what the same repository
//     plans without a table.
//   - TestTheServiceIsPlannedVertexWhereNamed — the installation plans Vertex
//     wherever a chain names it, which is the one fact the Vertex AI API and
//     the credentials service's own custom role are both gated on.
//   - TestNoJobVertexGrantUnderEnforce — no job account and no shared project
//     job's account is planned the Vertex permission on a project that
//     enforces, while an `observe` project keeps it until release 2 (X8), so an
//     `observe` run in flight is not broken.
//
// The Terraform halves are deploy/terraform's own (TestTheServiceVertexRoleIsItsOwn,
// TestCredentialsServiceGrantsAreTheOneBundle, TestCredentialsServiceResourcesAreCounted)
// and the installation root's `credentials_service_vertex` run in
// installation.tftest.hcl.

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// chainAPIKeyAgent is the agent block of the fixtures the mounts are told
// apart by: `api-key` is the one auth whose own arm mounts the Anthropic key
// and the repository's provider key beside it, so a job that mounts nothing at
// all is the table's arm and no other.
const chainAPIKeyAgent = `agent: { auth: api-key }`

// chainVertexSource is the table whose Claude models go out on Vertex where the
// repository asking is admitted to it: the one source kind that is a single
// credential of the project and therefore carries an admission (D3182, R20).
const chainVertexSource = `model_routing:
  - match: "claude-*"
    chain:
      - { kind: vertex, admit: [acme/webapp] }
`

// chainTableLC is the fixture of TestSpecFromTheTable: repoRootAPIKeyLC — an
// api-key repository that mounts the Anthropic key and its own provider key —
// with a table over it: Vertex where this repository is admitted, the
// launcher's own pool otherwise (a source that admits every repository and
// reaches no Vertex, the project naming no pool at all).
const chainTableLC = repoRootAPIKeyLC + chainVertexSource + `      - { kind: launcher-pool }
`

// chainElsewhereLC is the same table with its only source admitted to another
// repository: what the plan answers for acme/webapp then is that this project's
// table serves it nothing at all, so the release-1 bridge is still its answer.
const chainElsewhereLC = repoRootAPIKeyLC + `model_routing:
  - match: "claude-*"
    chain:
      - { kind: anthropic, admit: [acme/other] }
`

// chainVertexPoolLC names Vertex through a pool rather than through the vertex
// kind: a pool's member may be the built-in vertex credential, which no
// installation defines (config.CredentialVertex, D1624).
const chainVertexPoolLC = repoRootLC + `pools:
  - { name: subscriptions, owner: owner@example.com, members: [vertex] }
model_routing:
  - match: "claude-*"
    chain:
      - { kind: pool, pool: subscriptions }
`

// chainLauncherPoolLC is a table whose only source is the launcher's own pool:
// a source that names NO pool in the table (which pool serves is decided per
// run, D3176), so the plan has no member list to read and answers the walk's
// own direction — the pool a launcher may designate is one that may hold the
// built-in Vertex credential.
const chainLauncherPoolLC = repoRootLC + `model_routing:
  - match: "claude-*"
    chain:
      - { kind: launcher-pool }
`

// chainAnthropicLC is a table that names no Vertex anywhere: the project's own
// Anthropic key, admitted to this repository, and nothing else.
const chainAnthropicLC = repoRootLC + `model_routing:
  - match: "claude-*"
    chain:
      - { kind: anthropic, admit: [acme/webapp] }
`

// repoLineRE is the repository line of the fixtures below, read to be written
// with the Vertex record on it.
var repoLineRE = regexp.MustCompile(`(?m)^(  acme/webapp: .*github_app_id: "[0-9]+")( \})$`)

// vertexRecordOf is a fixture with the fact `fugaro update` records for a
// repository whose runs reach Vertex (localcfg.Repo.Vertex, written out of
// RepoSpec.UsesVertex): what the installation's own plan reads the Vertex AI
// API from when neither the repository's file nor the project's table names
// Vertex where this reader can see it.
func vertexRecordOf(lc string) string {
	out := repoLineRE.ReplaceAllString(lc, `$1, vertex: true$2`)
	if out == lc {
		panic("the fixture's repos block moved: vertexRecordOf no longer records the Vertex repository")
	}
	return out
}

// enforceBudgetLC is the budget block an enforcing project records. It is the
// installation's own copy of the mode: the repository root's plan reads the
// published node instead (Inputs.Policy), and an installation that has
// converged holds the same fact in both.
const enforceBudgetLC = "budget: { mode: enforce, per_run_usd: 20 }\n"

// observeBudgetLC is the same block with the mode release 1 leaves alone.
const observeBudgetLC = "budget: { mode: observe }\n"

// repoRootWithoutService plans the same repository fixture on the installation
// as it is when the switch was never turned on: no credentials service in the
// installation's outputs and no readiness record for the repository, which is
// the pair repoRootServed turns on.
func repoRootWithoutService(t *testing.T, agent, lc, mode string) RepoSpec {
	t.Helper()
	in := repoRootInputs(t, agent)
	in.LC = parseLC(t, lc)
	in.Policy = &projectpolicy.Policy{Budget: projectpolicy.Budget{Mode: mode}}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// providerMountOf reports whether a model-mount list holds a provider's key,
// which is the one model credential the release-1 bridge's `anthropic` arm
// deliberately leaves mounted (R6, D1634).
func providerMountOf(mounts []string) bool {
	return slices.ContainsFunc(mounts, func(m string) bool {
		env, _, _ := strings.Cut(m, "=")
		return config.IsProviderKeyEnv(env)
	})
}

// mountKindsOf is a model-mount list as the three names a reader can check it
// by: the two Claude credentials, the provider keys, and anything else.
func mountKindsOf(mounts []string) (claude, provider, other []string) {
	for _, m := range mounts {
		env, _, _ := strings.Cut(m, "=")
		switch {
		case slices.Contains(config.ModelSecretEnvs, env):
			claude = append(claude, m)
		case config.IsProviderKeyEnv(env):
			provider = append(provider, m)
		default:
			other = append(other, m)
		}
	}
	return claude, provider, other
}

// TestSpecFromTheTable is CS11's first clause: the infra spec derives what a job
// mounts and whether it is told about the service from the table and the pools,
// not from agent.auth.
//
// The facts it pins, in order: a repository the table serves mounts no model
// credential at all and is told about the service, whatever its agent.auth says
// (the fixture's is `api-key`, whose own arm mounts the Anthropic key and the
// provider's key); a repository the table's sources are not admitted to is
// served nothing, so its mounts are exactly the bridge's; and the control — the
// same repository with no table — is the release-1 bridge unchanged:
// `anthropic`, the two Claude mounts gone and the provider's key kept. The
// bridge's answer is the one the middle value keeps its meaning for (O7: the
// flip list is not retired here), so a spec that answered `all` for every
// repository would pass the first case and fail the last.
func TestSpecFromTheTable(t *testing.T) {
	served := repoRootServed(t, chainAPIKeyAgent, chainTableLC, config.BudgetObserve)
	if got := served.ModelSecretsServed; got != config.ModelSecretServedAll {
		t.Errorf("the table serves this repository and the spec plans model_secrets_served = %q, want %q: a served source is the service's, so none of the job's model credentials is the run's to mount", got, config.ModelSecretServedAll)
	}
	if served.Installation.CredentialsService == nil {
		t.Error("the spec does not tell the job about the service although a chain of the table has a source this repository is admitted to: the run then reaches no credential its chain names")
	}
	for _, name := range slices.Sorted(maps.Keys(served.Workflows)) {
		if mounts := modelMountsOf(served.Workflows[name]); len(mounts) > 0 {
			t.Errorf("the %s job still mounts %v: no secret is mounted for a served source", name, mounts)
		}
	}

	// A table whose sources are admitted to another repository serves this one
	// nothing: the plan is the bridge's, every mount the bridge keeps stays, and
	// the two repositories of one table plan differently — which is the
	// per-repository admission D3182 is about.
	off := repoRootServed(t, chainAPIKeyAgent, chainElsewhereLC, config.BudgetObserve)
	bridge := repoRootServed(t, chainAPIKeyAgent, repoRootAPIKeyLC, config.BudgetObserve)
	if got, want := off.ModelSecretsServed, bridge.ModelSecretsServed; got != want {
		t.Errorf("a table that admits this repository nowhere plans model_secrets_served = %q, and the same repository with no table at all plans %q: the bridge is what is left for a repository the table does not place", got, want)
	}
	if got, want := modelMountsOf(off.Workflows["web"]), modelMountsOf(bridge.Workflows["web"]); !slices.Equal(got, want) {
		t.Errorf("a table that admits this repository nowhere leaves it mounting %v, and the same repository with no table mounts %v: nothing about the job is planned from a chain that does not reach it", got, want)
	}

	// And the release-1 bridge itself, which the table replaces one repository
	// at a time and does not yet retire (O7): `anthropic` for an api-key
	// repository the switch admits, so the two Claude mounts go and the
	// provider's key stays for the route that forwards it (R6, D1634).
	if got := bridge.ModelSecretsServed; got != config.ModelSecretServedAnthropic {
		t.Errorf("a repository with no table plans model_secrets_served = %q, want %q: agent.auth is still the answer there, and retiring it is the owner's go (O7)", got, config.ModelSecretServedAnthropic)
	}
	claude, provider, other := mountKindsOf(modelMountsOf(bridge.Workflows["web"]))
	if len(claude) > 0 || len(provider) == 0 || len(other) > 0 {
		t.Errorf("the bridge's job mounts %v (claude %v, providers %v, other %v), want the provider keys alone: ModelSecretServedAnthropic is the two Claude credentials and no provider key", modelMountsOf(bridge.Workflows["web"]), claude, provider, other)
	}

	// A `vertex` repository is the case agent.auth cannot answer at all: the
	// service is not a credential Vertex is held in — the service calls Vertex
	// on its own identity (D3174) — so CredentialsServiceServes answers false
	// for that auth and, before the table, such a job was told about no service
	// and reached Vertex with its own account. A table that names Vertex makes
	// the call the service's, so the job is told even though its auth is the one
	// the bridge serves nothing for.
	vertex := repoRootServed(t, repoRootVertexAgent, chainTableLC, config.BudgetObserve)
	if vertex.Installation.CredentialsService == nil {
		t.Error("a table that names Vertex does not tell the job about the service: nothing else can reach Vertex for it, so the run has no way to make the call its chain names")
	}
	vertexBridge := repoRootServed(t, repoRootVertexAgent, repoRootLC, config.BudgetObserve)
	if vertexBridge.Installation.CredentialsService != nil {
		t.Error("a vertex repository with no table plans an address and an invoker grant for the service: no arm of the release-1 bridge serves that auth, and the job reaches Vertex with its own account as it always did")
	}

	// And the gate the table does not escape: a table is no service. On an
	// installation that never opted in — or one whose root has not recorded this
	// repository ready — nothing can hand the run a credential, so the table's
	// answer is not read at all and the job keeps every mount it would have had.
	// The drop and the address are one decision: taking the mounts while
	// CredentialsService stays nil is a run that reaches no model credential and
	// has no way to ask for one, which is the state the gate exists to prevent.
	unserved := repoRootWithoutService(t, chainAPIKeyAgent, chainTableLC, config.BudgetObserve)
	if got := unserved.ModelSecretsServed; got != config.ModelSecretServedNone {
		t.Errorf("a table-served repository on an installation with no credentials service plans model_secrets_served = %q, want %q: nothing serves it, so its own mounts must stay", got, config.ModelSecretServedNone)
	}
	if unserved.Installation.CredentialsService != nil {
		t.Error("the spec tells a job about a credentials service the installation does not have")
	}
	if mounts := modelMountsOf(unserved.Workflows["web"]); len(mounts) == 0 {
		t.Error("a table-served repository on an installation with no credentials service mounts nothing and is told about no service: the run then reaches no credential its chain names, which is what the installed-and-ready gate is for")
	}
}

// TestTheServiceIsPlannedVertexWhereNamed is CS11's second clause: the
// credentials service's own account is planned the Vertex permission wherever a
// chain names Vertex, which is what the installation's one Vertex fact is for.
//
// What the installation plans is InstallationSpec.EnableVertex — the root's
// var.enable_vertex — and two things hang off it: modules/installation/apis.tf
// enables aiplatform.googleapis.com from it, and credentials.tf's own local
// gates the service's custom Vertex role and its binding on it. So this test is
// that fact, read for the three shapes a chain names Vertex in and for one that
// names it nowhere; the role itself, its one permission and its being a custom
// role rather than a predefined one are deploy/terraform's half
// (TestTheServiceVertexRoleIsItsOwn, and the installation root's
// credentials_service_vertex run).
func TestTheServiceIsPlannedVertexWhereNamed(t *testing.T) {
	// named is what the TABLE says — ChainsNameVertex and this repository's own
	// plan, which are false for a repository whose file records Vertex and whose
	// project names it nowhere. enabled is what the installation plans, which is
	// the record and the table together: the record is what `fugaro update`
	// wrote out of a previous plan, so an installation that has not converged on
	// a table naming Vertex still gets the API from it.
	for _, tc := range []struct {
		name    string
		lc      string
		named   bool
		enabled bool
	}{
		{"the vertex kind", repoRootLC + chainVertexSource, true, true},
		{"a pool whose members hold the built-in vertex credential", chainVertexPoolLC, true, true},
		{"a launcher-pool source, which names no pool in the table", chainLauncherPoolLC, true, true},
		{"the recorded per-repository fact", vertexRecordOf(repoRootLC), false, true},
		{"the project's own Anthropic key and no Vertex anywhere", chainAnthropicLC, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := parseLC(t, tc.lc)
			if got := ChainsNameVertex(lc); got != tc.named {
				t.Errorf("ChainsNameVertex = %v, want %v: it reads the table's chains and no repository's own record", got, tc.named)
			}
			if got := ChainPlanFor(lc, "acme/webapp").Vertex; got != tc.named {
				t.Errorf("the repository's own plan says Vertex = %v, want %v", got, tc.named)
			}
			s, err := Installation(lc, InstallOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if s.EnableVertex != tc.enabled {
				t.Errorf("var.enable_vertex = %v, want %v: the Vertex AI API and the credentials service's own permission are both planned from this one fact, so a chain that names Vertex and leaves it false is a run whose call is denied", s.EnableVertex, tc.enabled)
			}
		})
	}
}

// TestNoJobVertexGrantUnderEnforce is CS11's third clause: on a project whose
// budget mode is `enforce` a job's account and the shared project job's account
// are planned no Vertex permission, and on an `observe` project they keep it
// until release 2 (X8).
//
// The reason the split is safe is the split's own: a Vertex run that
// authenticates to Vertex with its own job account is refused at bootstrap
// under `budget.mode: enforce` (runner.VertexBudgetRefusal, internal/runner's
// checkBudget) — the run an enforcing project starts anyway is one the
// credentials service serves, and its Vertex call is the service's own (CS5,
// D3174) — so no run in flight can hold the permission release 1 takes away,
// while an `observe` project's in-flight run really does authenticate to Vertex
// with its own job account, and a plan that took the permission away there
// would break it mid-flight.
//
// The SERVICE keeps Vertex either way, which is the half that is not a detail:
// the service is what calls Vertex for a run of a table-served repository
// (D3174), so the API has to stay enabled and its own permission planned
// exactly where the jobs' grant goes away — the two are read from the one
// installation fact, and the assertions below pin them together.
func TestNoJobVertexGrantUnderEnforce(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{config.BudgetObserve, true},
		{config.BudgetEnforce, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			budget := observeBudgetLC
			if tc.mode == config.BudgetEnforce {
				budget = enforceBudgetLC
			}
			rs := repoRootServed(t, repoRootVertexAgent, vertexRecordOf(repoRootLC)+budget, tc.mode)
			for _, name := range slices.Sorted(maps.Keys(rs.Workflows)) {
				if got := rs.Workflows[name].Vertex; got != tc.want {
					t.Errorf("the %s job's account is planned the Vertex permission = %v on a %s project, want %v: a Vertex run under enforce is refused at bootstrap, so no run in flight can hold it there, and the two Vertex names in the job's environment go with the grant", name, got, tc.mode, tc.want)
				}
			}
			if !rs.UsesVertex() {
				t.Errorf("the spec stops saying the repository reaches Vertex on a %s project: the installation would disable the API and plan the service no permission, and the calls the service is meant to make could not be made at all", tc.mode)
			}
			if tc.want {
				return
			}
			// The module's own address set is the other half of the grant: a
			// plan that took the role away and left google_project_iam_member
			// .vertex in the root's addresses is a root whose apply fails.
			if got := rs.Addresses(); slices.ContainsFunc(got, func(a string) bool { return strings.HasSuffix(a, "google_project_iam_member.vertex[0]") }) {
				t.Errorf("the root's addresses still hold google_project_iam_member.vertex[0]: %v", got)
			}
		})
	}

	// The installation's own plan, which is where the shared project jobs are:
	// the same mode, read from the config the installation holds rather than
	// from a published node. The shared job's account is planned the permission
	// on an observe project and none on an enforcing one, and the API stays
	// enabled either way.
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{config.BudgetObserve, true},
		{config.BudgetEnforce, false},
	} {
		t.Run("shared jobs/"+tc.mode, func(t *testing.T) {
			budget := observeBudgetLC
			if tc.mode == config.BudgetEnforce {
				budget = enforceBudgetLC
			}
			s, err := Installation(parseLC(t, chainProjectLC(budget)), InstallOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !s.EnableVertex {
				t.Fatal("the fixture plans var.enable_vertex false: the assertions below would be vacuous")
			}
			if len(s.ProjectJobs) == 0 {
				t.Fatal("the fixture plans no shared project job")
			}
			for _, name := range slices.Sorted(maps.Keys(s.ProjectJobs)) {
				if got := s.ProjectJobs[name].Vertex; got != tc.want {
					t.Errorf("the shared %s job's account is planned the Vertex permission = %v on a %s project, want %v: the grant is what modules/project/sa.tf's one aiplatform member is gated on", name, got, tc.mode, tc.want)
				}
			}
		})
	}
}

// chainProjectLC is the shared-job fixture: the project profile fixtures a
// shared job is built from (projectjobs_test.go's own), with the Vertex fact
// recorded on the repository and the budget block given — the two facts the
// installation's own plan reads its Vertex answer from.
func chainProjectLC(budget string) string {
	repo := vertexRecordOf(projectJobRepoLC)
	if repo == projectJobRepoLC {
		panic("projectJobRepoLC moved: chainProjectLC no longer records the Vertex repository")
	}
	return m4LocalConfig + repo + m5Additions + budget + credentialsServiceLCBlock + projectProfilesLCBlock
}

// TestChainAdmitsReadsTheAdmissionPerRepository is CS5's CLI-side fact
// (D3174): ChainAdmits answers whether a source of the table admits ONE
// repository, which is what the pre-flight gates read before a run exists.
//
// The cases are the shapes the migration writes and the one a plan cannot
// follow: a single-credential source's own admit list (empty meaning nobody),
// a named pool through its members' allow_data_to — with a member that is no
// account consenting to nobody — and the launcher's own pool, which names no
// pool here and so admits nobody before a launcher designates one (D3176).
func TestChainAdmitsReadsTheAdmissionPerRepository(t *testing.T) {
	pool := func(name string, members ...string) string {
		return "pools:\n  - { name: " + name + ", owner: owner@example.com, members: [" + strings.Join(members, ", ") + "] }\n"
	}
	// A pool lists credential names (config.ValidatePools), and a Claude account
	// the migration wraps has one of its own name (the craft migration's own
	// conversion): `bmb` is repoRootLC's account, admitted to acme/webapp alone.
	const bmbCredential = "credentials:\n  - { name: bmb, type: claude-oauth }\n"
	source := func(kind string) string {
		return "model_routing:\n  - match: \"claude-*\"\n    chain:\n      - { kind: " + kind + " }\n"
	}
	for _, tc := range []struct {
		name   string
		lc     string
		admits map[string]bool
	}{
		{
			name: "a single-credential source by its own admit list",
			lc:   repoRootLC + chainVertexSource,
			admits: map[string]bool{
				"acme/webapp": true,
				"acme/other":  false,
				"":            false,
			},
		},
		{
			name: "an empty admit list admits nobody",
			lc:   repoRootLC + source("anthropic"),
			admits: map[string]bool{
				"acme/webapp": false,
				"acme/other":  false,
			},
		},
		{
			name: "a named pool through its members' allow_data_to",
			lc:   repoRootLC + bmbCredential + pool("subscriptions", "bmb", "vertex") + "model_routing:\n  - match: \"claude-*\"\n    chain:\n      - { kind: pool, pool: subscriptions }\n",
			admits: map[string]bool{
				"acme/webapp": true,
				"acme/other":  false,
				"":            false,
			},
		},
		{
			name: "a member that is no account consents to nobody",
			lc:   repoRootLC + pool("subscriptions", "vertex") + "model_routing:\n  - match: \"claude-*\"\n    chain:\n      - { kind: pool, pool: subscriptions }\n",
			admits: map[string]bool{
				"acme/webapp": false,
				"acme/other":  false,
			},
		},
		{
			name: "the launcher's own pool admits nobody",
			lc:   repoRootLC + chainLauncherPoolLC[len(repoRootLC):],
			admits: map[string]bool{
				"acme/webapp": false,
				"acme/other":  false,
			},
		},
		{
			name: "a project with no table",
			lc:   repoRootLC,
			admits: map[string]bool{
				"acme/webapp": false,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := parseLC(t, tc.lc)
			for _, repo := range slices.Sorted(maps.Keys(tc.admits)) {
				if got := ChainAdmits(lc, repo); got != tc.admits[repo] {
					t.Errorf("ChainAdmits(%q) = %v, want %v", repo, got, tc.admits[repo])
				}
			}
		})
	}
	if ChainAdmits(nil, "acme/webapp") {
		t.Error("a project with no local config at all admits a repository")
	}
}
