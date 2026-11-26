// Package apply holds what a management command owes after it has made a
// change, and where this machine records it (docs/composite-commands, D1561,
// D1562, D1570).
//
// Two things live here and nothing else. steps.go is the one table from a
// change — a setting key or a resource type — to the steps that make it live:
// the initflow stage names plus the one step that is not a stage, a catalog
// image's build. For a setting it holds the exceptions only — the steps no
// layer of the key's own row names, the catalog's build and the workspace's two
// stages — and every other key answers from its registry row
// (internal/settings/steps.go, AP5), so the declaration is one place per key
// and not a second list beside the registry. The order and the dedupe are
// initflow's (Names, Needs); this package keeps no second list of stages, so a
// stage renamed or reordered there moves here with no edit. pending.go is the
// local document beside the project config that records the steps still owed,
// bound to the project and the GCP project ID and holding no value of anything.
//
// Nothing here reads the cloud, the config files or the command line: a Change
// is a value, and the pending document is a file the caller names a project
// for. Running the steps is `fugaro apply`'s work.
package apply

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/settings"
)

// CatalogBuild is the one step of the table that is not an initflow stage: the
// Cloud Build of a catalog image (`fugaro image build <name>`), which is
// billable and so leased from the run's budget (F2, D1543). `fugaro update`
// never runs it (D1577): its images stage mirrors and pins the base images and
// builds no catalog image, which is why a catalog entry's steps can never be
// answered by "run fugaro update".
const CatalogBuild = "catalog-build"

// Kind is what a Change is keyed by.
type Kind string

const (
	// Setting is a key of the settings registry (internal/settings): the
	// change is what `fugaro settings set|unset` wrote.
	Setting Kind = "setting"
	// Resource is a resource type a management command manages.
	Resource Kind = "resource"
)

// The resource types the table declares. A resource type is the subject a
// command acts on and not the command.
const (
	// ResourceImage is the project's image catalog (internal/cli/
	// image_catalog.go) as `fugaro image add` leaves it: an entry written into
	// it, whose image has to be built before anything can pin or publish it.
	ResourceImage = "image"
	// ResourceImageRemoval is that same catalog with an entry taken out of it
	// (`fugaro image rm`), which is a different subject from an entry written
	// into it and not the same change read twice: a removal builds nothing.
	// The entry's image is built, pushed and left in the registry, the catalog
	// the removal leaves is the one the remaining entries make up, and the
	// build of a catalog image is billable and leased from the run's budget
	// (F2, D1543) — so a removal that owed it would rebuild every entry of the
	// catalog, and fail the whole removal on the first entry whose build fails,
	// over a change that never touched it (check 390).
	ResourceImageRemoval = "image-removal"
	// ResourceRepo is a repository of the project (internal/cli/
	// project_enroll.go): onboarding one is what the repository stage's own
	// plan is made from.
	ResourceRepo = "repo"
	// ResourceAccount is a model account of the project (internal/cli/
	// accounts_rm.go).
	ResourceAccount = "account"
	// ResourceModel is a model of the project (internal/cli/models_add.go,
	// models_rm.go).
	ResourceModel = "model"
	// ResourceWorkspace is the workspace this project is the owner of
	// (internal/cli/workspace.go, D1292).
	ResourceWorkspace = "workspace"
)

// Change is one change a management command made: what the table is keyed by.
// It carries the change's subject and never its value — a value belongs in the
// file the command wrote, and the pending document may not become a second
// copy of the config (D1570).
type Change struct {
	// Kind says what Key names: a setting key or a resource type.
	Kind Kind `json:"kind"`
	// Key is the setting key as `fugaro settings` spells it, or one of the
	// resource type constants above.
	Key string `json:"key"`
}

// catalogSteps is what a catalog entry written into the catalog owes (F2): the
// build that makes the image exist, the repository stage, whose redeploy pins
// the digest that build produced, and the policy stage, which publishes
// `image_catalog` into the node every run of the project is admitted by. Both
// the image resource type and every `image_catalog.*` key owe it, which is why
// it is written once.
var catalogSteps = []string{CatalogBuild, initflow.Repository, initflow.Policy}

// removalSteps is what an entry taken out of the catalog owes: the two stages
// that make the removal live and no build. The repository stage redeploys the
// checkout's own jobs without the entry (D1574) and the policy stage publishes
// the catalog the removal leaves, which is the whole of what a removal changes
// — see ResourceImageRemoval for why the build is not one of them.
var removalSteps = []string{initflow.Repository, initflow.Policy}

// resourceSteps is the resource-type half of the table.
var resourceSteps = map[string][]string{
	ResourceImage:        catalogSteps,
	ResourceImageRemoval: removalSteps,
	// ResourceRepo is a repository of the project (internal/cli/
	// project_enroll.go): onboarding one is what the repository stage's own
	// plan is made from, and the three stages are the ones the enrolment's own
	// converge runs (project_enroll_converge.go's reposEnrolmentStageList): the
	// policy stage publishes the node the enrolment rewrote (`repo_images`
	// beside the `allow_repos` the command wrote itself), the installation stage
	// deploys the jobs of the profiles this project's repositories use, and the
	// repository stage is the new repository's own Terraform root — its jobs and
	// its service accounts (F15, D1574).
	ResourceRepo: []string{initflow.Policy, initflow.Installation, initflow.Repository},
	// ResourceAccount is a model account of the project (internal/cli/
	// accounts_rm.go): removing one takes the declaration out of the local
	// config, and the two stages below are the project-wide halves of what that
	// change affects — the node stops naming the account (`accounts.NAME`, which
	// the policy stage derives from the profiles' lists), and the job the account
	// owned goes from the installation's job set (one job and one service account
	// per profile, provider and account).
	//
	// The repository stage is deliberately not one of them. The third thing
	// `accounts add` converges is "the running checkout's own repository root",
	// whose workflows mount the named secret (the accounts spec §2.5), which is a
	// fact about where the command is run and not about the account: the
	// command's converge runs it for the checkout it is run in, and a project
	// directory that is no checkout is the ordinary case for both commands. D865
	// answers for that case by naming the repository rather than leaving work
	// owed, and queuing the redeploy here would contradict it — every such
	// machine would hold a step whose subject it cannot name (`fugaro apply`
	// redeploys the checkout it is run in, D1574) while the removal's own report
	// says the installation is done.
	ResourceAccount: []string{initflow.Policy, initflow.Installation},
	// ResourceModel is a model of the project (internal/cli/models_add.go,
	// models_rm.go): the published node carries the `model_prices` and
	// `budget.allowed_models` halves of the change, which is what the policy
	// stage publishes. The route's own claims are in the copy of the providers
	// block each job holds, and that copy is refreshed by the repository
	// stage's own redeploy — which is per repository and per checkout (D1574),
	// so it is queued by a change made in a repository (ResourceRepo) and never
	// by a project-wide one: a models change that named it would leave every
	// machine with no checkout of that repository holding a step it cannot run.
	ResourceModel: []string{initflow.Policy},
	// A workspace is both halves of the owner's own declaration, built from the
	// `workspace_members` and `workspace_cap` this installation declares: the
	// workspace's own project — its service account and the one grant that
	// reaches its database (initflow.WorkspaceProject) — and the published
	// record that service reads (initflow.Workspace's own comment). The project
	// runs first; a record published over a project that was never applied
	// names a service that does not exist.
	ResourceWorkspace: []string{initflow.WorkspaceProject, initflow.Workspace},
}

// settingSteps is the setting-key half of the table. A key is matched against
// these rows in order and the first row that answers it is the answer: a prefix
// row answers every key that starts with it, and an exact row answers its own
// key and nothing else.
//
// A key that matches no row here is asked of the registry itself, whose rows
// declare what a write of their key owes through their own layers
// (settings.KeySteps, AP5): a key no row of either answers for is a gap and not
// an answer (Steps says so), because every setting a command writes has to
// declare what it affects (spec rule 2, D1561). A row with no steps is a
// declaration too — it says the change is live once the config is written — and
// it is a different answer from the gap, which is why the two are not spelled
// the same way.
var settingSteps = []struct {
	// match is the key this row answers for, or the prefix of the family it
	// answers for when exact is false.
	match string
	// exact says match is the whole key rather than a prefix of it.
	exact bool
	steps []string
}{
	// The catalog's keys are the local config's (internal/settings, the
	// `image_catalog.entries.*` rows), and they owe what the image resource
	// type owes: the build, the redeploy and the publish.
	{"image_catalog.", false, catalogSteps},
	// The project layer's keys are published by the layer stage, which is the
	// only writer of that file's cloud copy (D830).
	{"layer.", false, []string{initflow.Layer}},
	// The workspace OWNER's half of a membership: the member table, the cap and
	// the shared list are the whole of what this installation declares about its
	// workspace (D1292, and update_workspace.go's own comment: the record a run
	// publishes is the one its own config declares, `workspace_members`,
	// `workspace_cap` and `workspace_shared`). Both stages read that
	// declaration — the workspace's own project, whose stage is the one that
	// creates its service account and grant, and the record's own publish — so a
	// key written here owes both, in initflow's order. They are the keys of the
	// project the workspace is named after, and both stages do nothing on a
	// member's run, where they are absent.
	{"workspace_members.", false, []string{initflow.WorkspaceProject, initflow.Workspace}},
	{"workspace_cap.", false, []string{initflow.WorkspaceProject, initflow.Workspace}},
	{"workspace_shared.", false, []string{initflow.WorkspaceProject, initflow.Workspace}},
	// The providers block is where the copy of the installation's model keys
	// into the credentials service's bundle reads two of the three things an
	// entry carries: the provider's OWN address and auth shape (F45, D1418 —
	// the destination of a routed call is the entry's and never a caller's) and
	// the container the key is stored in, `providers.<name>.secret`. A write
	// here is therefore a bundle entry the copy would write differently, which
	// is what `fugaro update`'s credentials-copy stage is for
	// (internal/cli/update_keys.go, KS12/D3001).
	//
	// The policy stage is restated because a row of this table REPLACES what
	// the registry declares for the keys it answers and never adds to it: the
	// providers block's rows are the local config's (`settings.loc`, both
	// `local` and `shared`), so the publish below is their own answer and
	// dropping it here would save a providers write that never reaches the node
	// again. The family prefix is the only granularity this table has (a row
	// matches a key's literal text), so the keys of the block that move neither
	// fact — `route_fee_pct`, say — queue the copy as well: they cost it one
	// check that finds nothing to write, which is the cheap direction of a
	// declaration that cannot be made finer.
	{"providers.", false, []string{initflow.Policy, initflow.CredentialsCopy}},
	// The opt-in fact (R2, D3110): the one field of the `batch:` block that says
	// whether this installation's compute root is applied, and what makes the
	// write live is that root being applied — which `fugaro update` does, and
	// the stage is a step of the table for that reason. The operator's own
	// command writes and publishes the fact and names the update as the step
	// after it (BR18); what a machine's `fugaro apply` makes of the entry is the
	// same step, run where a person is at the terminal.
	//
	// The policy stage is restated beside it because a row of this table
	// REPLACES what the registry declares for the key it answers and never adds
	// to it: the fact's own row is the local config's (`loc`, both `local` and
	// `shared`), so the publish is that row's own answer, and dropping it here
	// would save an opt-in that never reaches the node again (R15).
	{"batch.opt_in", true, []string{initflow.Policy, initflow.Compute}},
	// The pin's ADDRESS is the one key of the member's half that owes a step:
	// it is what the member's own credential-route record is keyed on — the
	// address a run of its repositories takes the workspace's service from
	// (internal/cli/update_route.go, R3/WS9) — so writing it queues the route
	// stage, which republishes the record or removes it. The rest of the pin
	// owes no step: the workspace stage is the owner's and skips on a member,
	// the route reads no other pin key, and localcfg.Config.Shared nils the pin
	// rather than publishing it (W1). Those keys are the member's own record,
	// read by its own commands and verified against its own config, so the
	// change is live when the config is written — and a row mapping them to the
	// workspace stage would queue a step that can never run on the machine that
	// asked for it. The exact row must stand before the prefix one: the table
	// answers a key with the first row that matches it, and a prefix row placed
	// above this one would answer the address with the pin's own "owes
	// nothing".
	{"workspace_pin.service_address", true, []string{initflow.CredentialsRoute}},
	{"workspace_pin.", false, nil},
	// The bare membership key owes the route step: whether this project is in a
	// workspace at all is half of what the record says — a member publishes one
	// and a project in no workspace removes the one it published before
	// (update_route.go's own comment) — so a membership key written here is a
	// route decision the next run makes. It is matched whole rather than as a
	// prefix: it is a prefix of every other key of its family, so a prefix row
	// here would answer `workspace_members`, `workspace_cap` and any future
	// `workspaces.*` — keys the registry does not declare — with "declared,
	// owes nothing" instead of the gap the table owes them, and an owner-side
	// key added later would be saved with no workspace stage queued and nothing
	// said about it.
	{"workspace", true, []string{initflow.CredentialsRoute}},
}

// Steps answers the steps change owes, in the order initflow runs them, and
// whether the table declares anything for it at all. A change nobody declared
// answers false, which a caller must not read as "nothing owed": the table is
// the one place a change's steps are declared, so a key or a resource type with
// no row is a gap in it.
func Steps(change Change) ([]string, bool) {
	switch change.Kind {
	case Resource:
		steps, ok := resourceSteps[change.Key]
		if !ok {
			return nil, false
		}
		return Order(steps), true
	case Setting:
		for _, row := range settingSteps {
			if row.exact {
				if change.Key != row.match {
					continue
				}
			} else if !strings.HasPrefix(change.Key, row.match) {
				continue
			}
			return Order(row.steps), true
		}
		// The rows above are the exceptions this table adds to what the
		// registry declares: the catalog's build and the workspace's two
		// stages are steps no layer of the key's row names. Everything else
		// answers from the row itself (settings.KeySteps, AP5), so a key the
		// registry holds is declared by being there and this table does not
		// restate it.
		steps, ok := settings.KeySteps(change.Key)
		if !ok {
			return nil, false
		}
		return Order(steps), true
	}
	return nil, false
}

// WriteSteps is what a write of change landed in store owes: the steps of the
// change itself (Steps) plus the store's own stage, and nothing at all for a
// write into a checkout's `fugaro.yaml` (D1564).
//
// The store is the second half of the answer and neither half alone is it. A
// key's own declaration says what the key is — the layers it may be set in, and
// so the stages that serve it — while the store says where THIS write landed:
// `--layer project` owes the layer stage, and `--layer repo` on a key that has a
// project layer too owes it nothing, because a checkout's file is a file the
// next run reads and the change that makes it live is the merge of the branch
// holding it (D1564, docs/contracts/config-ownership.md §3). The node is the
// one store whose stage is its own publish, which a row's `policy` layer states
// already — the union dedupes.
//
// It is the one rule for every caller: the settings commands' own chain and the
// after-write hook both ask it, so the steps a command says it owes and the
// steps it runs cannot come to be two answers (D1561).
//
// A key neither the table nor the registry answers for owes nothing at all, and
// the store does not fill the gap in: a step run for a key the program cannot
// name is this rule deciding about a key it has never heard of, which is the
// fail-closed direction read the wrong way round — and it is the answer the
// after-write hook gave for such a key before this function was its rule.
func WriteSteps(change Change, store settings.Store) []string {
	// A checkout's file owes nothing, whatever the key's own row says: the
	// branch's merge is what makes the change live, and no step of this
	// machine's can run before it (D1564).
	if store == settings.StoreRepo {
		return nil
	}
	steps, ok := Steps(change)
	if !ok {
		return nil
	}
	switch store {
	case settings.StoreProject:
		steps = append(steps, initflow.Layer)
	case settings.StoreNode:
		steps = append(steps, initflow.Policy)
	}
	return Order(steps)
}

// Order returns names deduped and in the order initflow runs them: the stage
// names in initflow.Names order, which is the linear extension of
// initflow.Needs, so a stage is never asked to run before one it needs — and
// CatalogBuild ahead of every stage, because the digest it builds is what the
// repository stage's redeploy pins.
//
// A name that is neither a stage nor CatalogBuild is kept, last, rather than
// dropped: a caller that named something this build cannot run sees it in the
// list instead of silently running fewer steps than it asked for.
func Order(names []string) []string {
	left := make(map[string]bool, len(names))
	for _, n := range names {
		left[n] = true
	}
	out := make([]string, 0, len(left))
	if left[CatalogBuild] {
		out = append(out, CatalogBuild)
		delete(left, CatalogBuild)
	}
	for _, name := range initflow.Names() {
		if left[name] {
			out = append(out, name)
			delete(left, name)
		}
	}
	// Sorted rather than in the caller's order: what is left is a name this
	// build does not know, and one order for those is one answer.
	rest := make([]string, 0, len(left))
	for name := range left {
		rest = append(rest, name)
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// KnownStep reports whether name is a step this build can run: an initflow
// stage or CatalogBuild.
func KnownStep(name string) bool {
	return name == CatalogBuild || slices.Contains(initflow.Names(), name)
}
