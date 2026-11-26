package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_routing_migrate.go is MG2: `fugaro update` writes into an installation
// at the previous version the table, the pools and the admissions `routing.
// Migrate` answers for its own configuration
// (docs/model-credentials-routing/implementation-plan.md).
//
// It reads the installation's local config and each of its repositories' own
// `fugaro.yaml` — the two documents the retired vocabulary is written in — and
// hands them to MG1's function as the plain values it takes. Nothing here
// decides what a call becomes: every rule of the migration is that function's
// (MG1 is kept, and a second copy of its answer here would be a second
// migration), and this file is the translation of the documents into its input
// and of its answer back into a config.
//
// # Where it runs, and why there
//
// Two steps, one answer, and the split is what keeps the update's own infra
// plan out of it. The table moves what a repository's root is applied with —
// `model_secrets_served`, the credentials service's chains, Vertex — so a local
// config that carried it while the run was still planning would change the plan
// of the very update that migrates (ChainPlanFor reads it). So nothing writes
// it while the run plans:
//
//   - the policy stage publishes it (routingForPublish), from a copy of the
//     config the run holds, through RT4's publisher: the node carries the table,
//     the pools and the admissions, and the copy never reaches the run's config.
//   - the converge's tail writes it into this machine's file
//     (initEngine.migrateRouting), after every stage that plans an apply has
//     run, so the plan the next update builds is the one that reads it.
//
// The report is printed when MG1's answer is first asked for, which is before
// either write. It is the update door's alone — `fugaro setup` creates an
// installation, and a project being created has no previous version to migrate
// from — and `--plan-only` changes nothing.
//
// # Why it cannot fail an update
//
// The HARD RULE is that `fugaro update` is the whole upgrade and works
// perfectly, so every state this migration cannot answer is the config as this
// run found it: a repository whose file is not on this machine, a configuration
// the migrated file would refuse, a name no credential may carry. Each is a
// warning naming what happened and the config keeps the routing it has today,
// which is the state an installation that never migrated is already in (X2
// refuses the retired keys in a later release; this release still reads them).
// The one state that is neither a write nor a warning is a migration with no
// entry to write, and routingMigrationFor says why.
//
// It also leaves every entry MG1 kept on the release-1 bridge exactly where it
// is (D3196): the bridge serves those calls the credential their own
// configuration names today, and the migration names each of them.
//
// D3176's per-account pool of one is not written, and routingConfig says why:
// the config refuses a pool and a credential of one name, and the account's own
// credential — which the craft migration derived — is that pool of one already.

// The migration's own lines, each a constant because a person reads them and
// their length is pinned (TestUpdateMessagesAreLaconic, CLAUDE.md's bound on
// what a command tells someone): one per state this migration can be in and
// cannot write, and the one D3192's change is announced with.
const (
	routingNoCheckoutWarn   = "the routing table was not written: no checkout of %s is here, so its calls would route nowhere"
	routingUnwrittenWarn    = "the routing table was not written: the config it writes does not validate (%s)"
	routingUnreadableWarn   = "the routing table was not written: %s could not be read (%s)"
	routingWriteFailedWarn  = "the routing table was not written: %s (%s)"
	routingUnpublishedWarn  = "the routing table was not published: the project's policy cannot carry the pool %s, so a table naming it is refused"
	routingFallbackAutoLine = "routing: fallback is auto, so a run pays on the key as soon as its subscriptions are exhausted, not after the old threshold"
)

// routingMigrationDone is MG1's answer for this installation, as the two writers
// use it: the members the config is written with, and the documents the report
// and the publish are made of.
type routingMigrationDone struct {
	table routing.Table
	pools []config.Pool
	creds []config.Credential
	cfgs  map[string]*config.Config
	mig   routing.Migration
}

// routingMigrationFor is MG1's answer for this installation, computed once per
// run and printed once: the report is what changes — and what does not — and it
// belongs before the writes, not to one of them. It answers nil when this run
// migrates nothing (not the update door, --plan-only, nothing to migrate, or one
// of the states the header calls the config as this run found it).
func (r *initRun) routingMigrationFor(lc *localcfg.Config) *routingMigrationDone {
	if r.routingAsked {
		return r.routingMig
	}
	r.routingAsked = true
	if lc == nil || r.o.planOnly || r.o.doorName() != updateDoor.name {
		return nil
	}
	// The marker is the table itself, and PL3 reads the same one off the
	// published node: an installation whose config carries a table is migrated,
	// and a second update writes nothing.
	if len(lc.ModelRouting) > 0 {
		return nil
	}
	// The answer is the one the config written from this one would produce, and
	// every write of the local config converts the craft keys first
	// (migrateCraftKeys, init_craft_migrate.go): the credentials an account's
	// keys derive are what says which of MG1's pools may be written beside them
	// (routingConfig's namespace rule), so the conversion is applied to a copy
	// here rather than assumed of the run's own config, which a converge that
	// never reached the write would not have.
	src := *lc
	migrateCraftKeys(&src)
	cfgs, res, unseen := projectCheckoutResolved(&src)
	mig, pools, creds := routingMigration(&src, cfgs, res)
	// An answer with nothing in it is not a migration, and the one state that
	// gives it is a machine that holds no checkout of any repository: MG1 is
	// answered no model at all (buildTable reads Repos alone), so there is no
	// entry, no call on the bridge, no move and no pool that starts rotating. The
	// update stays as quiet as it was before this release rather than carrying the
	// fail-safe below on every run of a machine that will never hold them. The
	// pools are not part of the question: the answer carries the shared pool
	// whenever the installation has an account.
	if len(mig.Table) == 0 && len(mig.Bridge) == 0 && len(mig.Moves) == 0 && len(mig.Inert) == 0 {
		return nil
	}
	if len(unseen) > 0 {
		// A table written without a repository's own file would route that
		// repository's calls nowhere: the entries are built from the models the
		// files name, and a model no entry matches is a refused call. Nothing
		// is written, and the next update on a machine that holds the checkout
		// migrates.
		r.warn(fmt.Sprintf(routingNoCheckoutWarn, strings.Join(unseen, ", ")))
		return nil
	}
	// The config the migration would write is judged before it is written and
	// before anything is printed: a file this release refuses would turn
	// `fugaro update` into a failure on the next step that wrote it, and a
	// report of a migration that did not happen would be worse than none.
	next := src
	next.ModelRouting, next.Pools, next.Credentials = mig.Table, pools, creds
	data, err := next.Marshal()
	if err == nil {
		_, err = localcfg.Parse(data)
	}
	if err != nil {
		r.warn(fmt.Sprintf(routingUnwrittenWarn, safetext.FieldErr(err)))
		return nil
	}
	r.routingMig = &routingMigrationDone{table: mig.Table, pools: pools, creds: creds, cfgs: cfgs, mig: mig}
	r.printRoutingMigration(&src, cfgs, mig)
	return r.routingMig
}

// routingForPublish is the same answer applied to a COPY of the config the
// publish is built from, so the node RT4's publisher writes carries the table,
// the pools and the admissions. The run's own config is left as it is: every
// stage that plans an apply reads it, and the table in it is what changes the
// plan (the header's whole argument). It answers the config unchanged when this
// run migrates nothing.
//
// A table whose source names a pool the publisher's own rule leaves out of the
// node is answered unchanged too, with one line saying which pool: the pool the
// migration writes for the installation's own accounts is nobody's (a project's
// shared pool is its principal and names no owner), and publishing a table that
// names it is refused by the publisher's read of the table rather than by its
// own rule for the block — a refusal `fugaro update` may not end on (the HARD
// RULE, and D2608's "an ownerless local pool is saved, is not published, and is
// named in one line at update"). The installation's config keeps the table
// either way, and the next update of a project whose pools the node can carry
// publishes it.
func (r *initRun) routingForPublish(lc *localcfg.Config) *localcfg.Config {
	d := r.routingMigrationFor(lc)
	if d == nil || lc == nil {
		return lc
	}
	next := *lc
	next.ModelRouting, next.Pools, next.Credentials = d.table, d.pools, d.creds
	if left := poolsTheNodeCannotCarry(&next); len(left) > 0 {
		r.warn(fmt.Sprintf(routingUnpublishedWarn, strings.Join(left, ", ")))
		return lc
	}
	return &next
}

// poolsTheNodeCannotCarry names the pools a source of the table names that the
// node this publish would write does not carry, in the order the table meets
// them. It asks the publisher's own rule for the block (publishedCraftPools)
// rather than a second one: the block is what a source of the table is resolved
// against, and the file's own pools are the whole of what the migration asks
// the publish to carry. It is asked with no node behind the write — the pools
// the node already holds are read only once the publish reads it, which is
// after the config the publish is built from — so a pool the node carries and
// this file does not is reported as one the node cannot carry, which is a table
// left for the update that reads the node.
func poolsTheNodeCannotCarry(lc *localcfg.Config) []string {
	carried := map[string]bool{}
	pools, _ := publishedCraftPools(lc, nil, publishedCraftCredentials(lc, nil))
	for _, p := range pools {
		carried[p.Name] = true
	}
	var out []string
	for _, e := range lc.ModelRouting {
		for _, s := range e.Chain {
			if s.Kind == routing.KindPool && s.Pool != "" && !carried[s.Pool] {
				out = appendName(out, s.Pool)
			}
		}
	}
	return out
}

// migrateRouting is the converge's tail: it writes MG1's answer into this
// machine's local config, where every stage that plans an apply is behind it.
// It is a warning and never an error — the HARD RULE — and it writes nothing
// when the run changed nothing (--plan-only, a failed update).
func (e *initEngine) migrateRouting(res *initflow.Result) {
	r := e.r
	if r == nil || e.lc == nil || e.path == "" || res == nil || res.PlanOnly || res.Failed != nil {
		return
	}
	d := r.routingMigrationFor(e.lc)
	if d == nil {
		return
	}
	// The document is built from the file as it is at this moment and not from
	// the engine's own copy of the config, for the reason writeRecordedLocations
	// is: the repository stage writes the file from the config IT read, and a
	// write from e.lc would put the whole of that back. This step writes three
	// members and leaves every other one as the file has it.
	old, err := os.ReadFile(e.path)
	if err != nil {
		r.warn(fmt.Sprintf(routingUnreadableWarn, e.path, safetext.FieldErr(err)))
		return
	}
	cur, err := localcfg.Parse(old)
	if err != nil {
		r.warn(fmt.Sprintf(routingUnreadableWarn, e.path, safetext.FieldErr(err)))
		return
	}
	next := *cur
	next.ModelRouting, next.Pools, next.Credentials = d.table, d.pools, d.creds
	data, err := next.Marshal()
	if err == nil {
		_, err = localcfg.Parse(data)
	}
	if err != nil {
		r.warn(fmt.Sprintf(routingUnwrittenWarn, safetext.FieldErr(err)))
		return
	}
	if err := r.writeLocalConfig(&next, e.path, old, true); err != nil {
		r.warn(fmt.Sprintf(routingWriteFailedWarn, e.path, safetext.FieldErr(err)))
	}
}

// routingMigration is MG1's input and its answer, translated: the installation
// and its repositories as the function reads them, and the pools and the
// credentials the config is written with.
func routingMigration(lc *localcfg.Config, cfgs map[string]*config.Config, res map[string]*config.Resolution) (routing.Migration, []config.Pool, []config.Credential) {
	in := routing.Installation{
		Project:            lc.Name,
		DefaultAccount:     defaultAccountOf(lc),
		Providers:          routingProviders(lc),
		Pools:              routingPools(lc),
		SubscriptionSpread: lc.SubscriptionSpread != nil && *lc.SubscriptionSpread,
		FallbackAuto:       lc.CredentialCascade.FallbackValue() == projectpolicy.FallbackAuto,
	}
	if lc.Routing != nil && lc.Routing.Claude != nil {
		in.Via = lc.Routing.Claude.ViaValue()
		in.ViaModels = lc.Routing.Claude.Models
	}
	for _, name := range slices.Sorted(maps.Keys(lc.ClaudeAccounts)) {
		in.Accounts = append(in.Accounts, routing.Account{Name: name, AllowDataTo: lc.ClaudeAccounts[name].AllowDataTo})
	}
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		if cfg := cfgs[name]; cfg != nil {
			in.Repos = append(in.Repos, routingRepo(lc, cfg, res[name], name))
		}
	}
	// The models the checked-in map spells for Vertex (D3175): the migration
	// reads this for membership alone, and a Vertex model it is not in cannot be
	// written as an entry RT3 accepts.
	spelled := map[string]bool{}
	for _, repo := range in.Repos {
		for _, model := range repo.Models {
			if spelled[model.ID] {
				continue
			}
			spelled[model.ID] = true
			if _, ok := pricing.VertexPublisher(model.ID); ok {
				in.VertexSpelled = append(in.VertexSpelled, model.ID)
			}
		}
	}
	mig := routing.Migrate(in)
	pools, creds := routingConfig(lc, mig.Pools)
	return mig, pools, creds
}

// routingPools is the pools the installation already carries, as MG1 reads
// them: PL1's fields, each member as the config writes it.
func routingPools(lc *localcfg.Config) []routing.Pool {
	out := make([]routing.Pool, 0, len(lc.Pools))
	for _, p := range lc.Pools {
		rp := routing.Pool{
			Name:         p.Name,
			Members:      slices.Clone(p.Members),
			Strategy:     p.Strategy,
			Owner:        p.Owner,
			AllowedUsers: slices.Clone(p.AllowedUsers),
			LegacyOpen:   p.LegacyOpen,
			Cascade:      p.Cascade,
		}
		if p.Principal != nil {
			rp.Principal = routing.Principal{Kind: routing.PrincipalKind(p.Principal.Kind), ID: p.Principal.ID}
		}
		out = append(out, rp)
	}
	return out
}

// routingProviders is the installation's provider keys, by name: the admission
// MG1 writes into a chain's provider sources is the provider's own
// `allow_data_to`, and a key written with none admits every repository
// (config.ModelProvider.AllowsData).
func routingProviders(lc *localcfg.Config) []routing.Provider {
	out := make([]routing.Provider, 0, len(lc.Providers))
	for _, name := range slices.Sorted(maps.Keys(lc.Providers)) {
		p := lc.Providers[name]
		out = append(out, routing.Provider{
			Name: name,
			// The key written with nothing under it and the key never written
			// decode to the same nil slice, and ModelProvider's own reader is
			// what tells them apart: only a written-but-empty list answers
			// false to both questions.
			AllowDataTo:        slices.Clone(p.AllowDataTo),
			AllowDataToWritten: p.AllowDataTo == nil && !p.OpenToEveryRepo(),
		})
	}
	return out
}

// routingRepo is one repository's own file, as MG1 reads it.
func routingRepo(lc *localcfg.Config, cfg *config.Config, res *config.Resolution, name string) routing.Repo {
	// An auth neither the file nor the project layer writes is the empty value
	// C1's rule is about (F49), and config.Resolve answers the layer's own
	// default (config.DefaultAgentAuth) for it: the source says which of the two
	// it is, and an empty auth must never be read as Vertex — that is the one
	// reading F49 retires, because it would send a repository to the platform's
	// own model and bill.
	auth := ""
	if res != nil && res.SourceOf("agent.auth") != config.SourceDefault {
		auth = cfg.Agent.Auth
	}
	in := legacyInput(lc, cfg, auth)
	repo := routing.Repo{Name: name, Auth: auth, Models: routingRepoModels(lc, cfg, in.Grades)}
	// The accounts the repository's stages go out on: the retired walk's answer
	// for this repository, and the accounts a craft's or a grade's access names.
	for _, stage := range config.RunStages {
		if access, ok := craft.LegacyAccess(in, stage, ""); ok && access.Kind == craft.AccessCredential {
			for _, member := range access.Members {
				if member.Type == config.CredentialTypeClaudeOAuth {
					repo.Accounts = appendName(repo.Accounts, access.Name)
				}
			}
		}
	}
	// What the file's own `access` keys name, and nothing more: the retired
	// walk's answer for a repository that writes none is the auth's credential,
	// which is not a craft key and would put every api-key repository on the
	// bridge (MG1's addCall: an access other than the auth is a craft of its
	// own).
	for _, name := range config.CraftNames {
		classifyAccess(lc, &repo, cfg.Agent.Crafts.Inherited(name).Access)
	}
	for _, name := range config.GradeNames {
		classifyAccess(lc, &repo, in.Grades.Of(name).Access)
	}
	return repo
}

// classifyAccess puts one written `access` name where MG1 reads it: an account
// is the shared pool's member (D3183), because one chain per model cannot hold
// a pool per craft (D3171), and every other name is a credential the table
// cannot keep and goes on the release-1 bridge (D3062, D3196).
func classifyAccess(lc *localcfg.Config, repo *routing.Repo, name string) {
	if name == "" {
		return
	}
	if isAccountName(lc, name) {
		repo.Accounts = appendName(repo.Accounts, name)
		return
	}
	repo.Accesses = appendName(repo.Accesses, name)
}

// isAccountName is whether a name an `access` key wrote is a subscription
// account. It is the type and not the name that says so (craft.LegacyInput's
// own rule): an account whose token exists is a claude-oauth credential whether
// the credentials block declares it or the retired account keys do.
func isAccountName(lc *localcfg.Config, name string) bool {
	if _, ok := lc.ClaudeAccounts[name]; ok {
		return true
	}
	for _, c := range lc.Credentials {
		if c.Name == name {
			return c.Type == config.CredentialTypeClaudeOAuth
		}
	}
	return false
}

// routingRepoModels is every model the repository's file names, with the provider
// entry that claims it: `agent.model`, `agent.models.*`, a craft's model and a
// grade's (MG1's Repo.Models).
func routingRepoModels(lc *localcfg.Config, cfg *config.Config, grades config.ModelGrades) []routing.Model {
	var ids []string
	for _, id := range []string{
		cfg.Agent.Model,
		cfg.Agent.Models.Coder,
		cfg.Agent.Models.Reviewer,
		cfg.Agent.Models.Background,
		cfg.Agent.Models.Vision,
	} {
		ids = appendName(ids, id)
	}
	for _, name := range config.CraftNames {
		ids = appendName(ids, cfg.Agent.Crafts.Of(name).Model)
	}
	for _, name := range config.GradeNames {
		ids = appendName(ids, grades.Of(name).Model)
	}
	out := make([]routing.Model, 0, len(ids))
	for _, id := range ids {
		provider, _, ok := config.ProviderFor(lc.Providers, id)
		if !ok {
			provider = ""
		}
		out = append(out, routing.Model{ID: id, Claude: config.IsClaudeModel(id), Provider: provider})
	}
	return out
}

// legacyInput is the retired vocabulary in force for one repository, as
// craft's own rule reads it.
func legacyInput(lc *localcfg.Config, cfg *config.Config, auth string) craft.LegacyInput {
	in := craft.LegacyInput{
		Auth:           auth,
		OAuthToken:     resolveAccount(lc, cfg.Agent.OAuthToken),
		StageAccounts:  stageAccountsOf(lc),
		DefaultAccount: defaultAccountOf(lc),
		Accesses:       cfg.Agent.Crafts,
		Credentials:    lc.Credentials,
		Pools:          lc.Pools,
		CascadePool:    cascadePoolOf(lc),
	}
	if cfg.ModelGrades != nil {
		in.Grades = *cfg.ModelGrades
	} else if lc.ModelGrades != nil {
		in.Grades = *lc.ModelGrades
	}
	return in
}

// resolveAccount is a name the config wrote, resolved past any alias.
func resolveAccount(lc *localcfg.Config, name string) string {
	account, _ := lc.ResolveAccount(name)
	return account
}

// defaultAccountOf is the project's default_claude_account, past any alias, and
// "" where it names none or names the unsuffixed-default sentinel: the
// sentinel is no account of its own (config.DefaultClaudeAccount), and MG1
// reads it as the walk's last rung rather than as a name.
func defaultAccountOf(lc *localcfg.Config) string {
	account, _ := lc.ResolveAccount(lc.DefaultClaudeAccount)
	if account == config.DefaultClaudeAccount {
		return ""
	}
	return account
}

// cascadePoolOf is the pool the project's cascade is served by: the one its
// config marks (config.Pool.Cascade), or the one the migration named for it.
func cascadePoolOf(lc *localcfg.Config) string {
	for _, p := range lc.Pools {
		if p.Cascade {
			return p.Name
		}
	}
	for _, p := range lc.Pools {
		if p.Name == config.PoolCascade {
			return p.Name
		}
	}
	return ""
}

// appendName adds a name to a list once, in the order it was met.
func appendName(list []string, name string) []string {
	if name == "" || slices.Contains(list, name) {
		return list
	}
	return append(list, name)
}

// routingConfig is MG1's pools as the config is written with them, and the
// credentials they need.
//
// A pool the installation already carried keeps what its own config states and
// gains the rule the migration resolved for it — written only where the pool's
// own `rule` and `strategy` do not already say it, so a pool the migration only
// resolved is not a pool the document moves (R21).
//
// D3176's pool of one per account is the one pool that is not written, and the
// credential the account's name already holds is why. A pool and a credential
// share one namespace — a craft's access names a name and does not say which
// (config.ValidatePools) — so a pool named after an account, which is what the
// migration answers, is refused beside the claude-oauth credential the craft
// migration derived for that same account: the file would refuse itself and
// `fugaro update` with it (HARD RULE). The credential IS that pool of one
// (config.craftAccessRefusal reads a single credential as one), its token is a
// member of the shared pool below, and no chain of the table names the pool of
// one — MG1 writes it for `--pool <account>` alone — so no call moves by its
// absence. A member nothing declares and no credential may carry is left to the
// write's own validation, which refuses the file and leaves the config as it was.
func routingConfig(lc *localcfg.Config, specs []routing.PoolSpec) ([]config.Pool, []config.Credential) {
	carried := map[string]config.Pool{}
	for _, p := range lc.Pools {
		carried[p.Name] = p
	}
	held := map[string]bool{config.CredentialVertex: true}
	for _, c := range lc.Credentials {
		held[c.Name] = true
	}
	pools := make([]config.Pool, 0, len(specs))
	members := map[string]bool{}
	for _, spec := range specs {
		if held[spec.Name] {
			continue
		}
		old := carried[spec.Name]
		p := config.Pool{
			Name:         spec.Name,
			Strategy:     old.Strategy,
			Members:      slices.Clone(spec.Members),
			Owner:        spec.Owner,
			AllowedUsers: slices.Clone(spec.AllowedUsers),
			LegacyOpen:   spec.LegacyOpen,
			Cascade:      old.Cascade,
		}
		// A carried pool keeps the rule its own document states — the migration
		// resolved exactly that one from its strategy (Rule.RuleOrDefault), and
		// writing it back as a `rule` would be a diff for an installation that
		// changed nothing. A new pool is written with the rule MG1 answered, and
		// the shared pool too when it is one an installation carried: that pool
		// is the cascade's own, and failover is the rule it already rotates by
		// (D3183).
		p.Rule = old.Rule
		if old.Name == "" || routing.Rule("").RuleOrDefault(old.Strategy) != spec.Rule {
			p.Rule = string(spec.Rule)
		}
		if spec.Principal != nil {
			p.Principal = &config.PoolPrincipal{Kind: string(spec.Principal.Kind), ID: spec.Principal.ID}
		}
		pools = append(pools, p)
		for _, member := range spec.Members {
			members[member] = true
		}
	}
	// Every name the file already declares stays declared, and a member of a
	// written pool that nothing declares gains the claude-oauth entry its type
	// is: a pool's member names a credential of the same document, or the
	// built-in vertex (config.ValidatePools). A name a written pool holds is
	// left alone — the namespace rule above refuses the pair — and the write's
	// own validation then answers for the file, which is the state the header
	// describes.
	written := map[string]bool{}
	for _, p := range pools {
		written[p.Name] = true
	}
	creds := slices.Clone(lc.Credentials)
	for _, name := range slices.Sorted(maps.Keys(members)) {
		if held[name] || written[name] {
			continue
		}
		creds = append(creds, config.Credential{Name: name, Type: config.CredentialTypeClaudeOAuth})
		held[name] = true
	}
	return pools, creds
}

// printRoutingMigration is the report, printed before any write: what each
// repository's runs go out on and that nothing of its own moves, then the whole
// of what does. It is the update's own announcement and not a stage's detail,
// so it takes the writer the two announcements beside it take — the terminal
// always, the log beside it (loggedOut) — and it is silent where stdout is a
// document (--json, --print-vars), which is what the steps that print before
// the confirmation above it do.
func (r *initRun) printRoutingMigration(lc *localcfg.Config, cfgs map[string]*config.Config, mig routing.Migration) {
	if r.o.asJSON || r.o.printVars {
		return
	}
	w := loggedOut(r.w, r.repLog)
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		fmt.Fprintf(w, "routing: %s's runs go out on %s, as they do today\n", name, repoSourceSentence(repoAuthSources(infra.RepoSpec{Name: name}, cfgs[name], lc)))
	}
	for _, move := range mig.Moves {
		fmt.Fprintf(w, "routing: %s's calls of %s move from %s to %s\n", move.Repo, move.Model, routingSourceSentence(move.From), routingSourceSentence(move.To))
	}
	for _, pool := range mig.Inert {
		fmt.Fprintf(w, "routing: the pool %s rotates among its tokens now that subscription_spread is gone\n", pool)
	}
	// D3192: the paid key is in a cascade's chain as soon as the subscriptions
	// are exhausted, where the retired threshold waited for them to be spent.
	if lc.CredentialCascade.FallbackValue() == projectpolicy.FallbackAuto {
		fmt.Fprintln(w, routingFallbackAutoLine)
	}
	for _, b := range mig.Bridge {
		fmt.Fprintf(w, "routing: %s's calls of %s stay on the release-1 bridge (%s)\n", b.Repo, b.Model, b.Reason)
	}
}

// repoSourceSentence is what a repository's own runs go out on today, as
// repoAuthSources answers it.
func repoSourceSentence(sources []routing.Source) string {
	if len(sources) == 0 {
		return "nothing"
	}
	parts := make([]string, 0, len(sources))
	for _, s := range sources {
		switch s.Kind {
		case routing.KindPool:
			parts = append(parts, "the account "+s.Pool)
		case routing.KindLauncherPool:
			parts = append(parts, "the launcher's own credentials")
		case routing.KindAnthropic:
			parts = append(parts, "the project's Anthropic key")
		case routing.KindVertex:
			parts = append(parts, "the built-in Vertex credential")
		default:
			parts = append(parts, s.Pool)
		}
	}
	return strings.Join(parts, ", then ")
}

// routingSourceSentence is one source a call is served by, as the migration
// names it.
func routingSourceSentence(s routing.SourceName) string {
	switch s.Kind {
	case routing.KindPool:
		return "the pool " + s.Name
	case routing.KindProvider:
		return "the provider key " + s.Name
	case routing.KindAnthropic:
		return "the project's Anthropic key"
	case routing.KindVertex:
		return "the built-in Vertex credential"
	case routing.KindLauncherPool:
		return "the launcher's own pool"
	}
	return "nothing"
}
