package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/infra"
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
// It is called by writeLocalConfig (init.go), on the config about to be
// written and BEFORE that write: the acceptance asks for the report before any
// write, and the local config's own write is the first one this migration
// makes. It is the update door's alone — `fugaro setup` creates an
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
//
// # What it does not do
//
// It publishes nothing. The published node is RT4's (its block carries
// `model_routing`, its publisher is the one both doors take), and this release
// does not have it: the write here is the local config's, and the published
// node is RT4's own task. Until it lands the migration is a local one — the
// pointer is recorded in /work/state/followup.md and in the run's
// discovered.json.
//
// It also leaves every entry MG1 kept on the release-1 bridge exactly where it
// is (D3196): the bridge serves those calls the credential their own
// configuration names today, and the migration names each of them.

// migrateRouting is MG2's step: it asks MG1 for the migration of this
// installation and writes the answer into the config about to be written,
// printing first what changes.
func (r *initRun) migrateRouting(next *localcfg.Config) {
	if next == nil || r.o.planOnly || r.o.doorName() != updateDoor.name || r.migratedRouting {
		return
	}
	r.migratedRouting = true
	// The marker is the table itself, and PL3 reads the same one off the
	// published node: an installation whose config carries a table is migrated,
	// and a second update writes nothing. It is also what keeps a converge that
	// writes the local config more than once from migrating twice.
	if len(next.ModelRouting) > 0 {
		return
	}
	cfgs, res, unseen := projectCheckoutResolved(next)
	if len(unseen) > 0 {
		// A table written without a repository's own file would route that
		// repository's calls nowhere: the entries are built from the models the
		// files name, and a model no entry matches is a refused call. Nothing
		// is written, and the next update on a machine that holds the checkout
		// migrates.
		r.warn(fmt.Sprintf("the routing table was not written: %s has no readable fugaro.yaml here, and a table without it would route its calls nowhere",
			strings.Join(unseen, ", ")))
		return
	}
	mig, pools, creds := routingMigration(next, cfgs, res)
	if len(mig.Table) == 0 && len(mig.Pools) == 0 {
		return
	}
	// The config the migration would write is judged before it is written and
	// before anything is printed: a file this release refuses would turn
	// `fugaro update` into a failure on the next stage that wrote it, and a
	// report of a migration that did not happen would be worse than none.
	oldTable, oldPools, oldCreds := next.ModelRouting, next.Pools, next.Credentials
	next.ModelRouting, next.Pools, next.Credentials = mig.Table, pools, creds
	data, err := next.Marshal()
	if err == nil {
		_, err = localcfg.Parse(data)
	}
	if err != nil {
		next.ModelRouting, next.Pools, next.Credentials = oldTable, oldPools, oldCreds
		r.warn(fmt.Sprintf("the routing table was not written: the config it writes does not validate (%s)", safetext.FieldErr(err)))
		return
	}
	r.printRoutingMigration(next, cfgs, mig)
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
	repo := routing.Repo{Name: name, Auth: auth, Models: routingModels(lc, cfg, in.Grades)}
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

// routingModels is every model the repository's file names, with the provider
// entry that claims it: `agent.model`, `agent.models.*`, a craft's model and a
// grade's (MG1's Repo.Models).
func routingModels(lc *localcfg.Config, cfg *config.Config, grades config.ModelGrades) []routing.Model {
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
// The credentials are the second half of D3176: a pool of one is written for
// every account, and a credential and a pool of one name may not both exist
// (config.ValidatePools refuses the pair, because an access names a name and
// not a kind), so the claude-oauth credential an account's pool now stands for
// is dropped — the pool serves the same token, and `crafts.<craft>.access`
// naming the account names it still. A member nothing declares and no
// credential may carry is left to the write's own validation, which refuses the
// file and leaves the config as it was.
func routingConfig(lc *localcfg.Config, specs []routing.PoolSpec) ([]config.Pool, []config.Credential) {
	carried := map[string]config.Pool{}
	for _, p := range lc.Pools {
		carried[p.Name] = p
	}
	pools := make([]config.Pool, 0, len(specs))
	members := map[string]bool{}
	for _, spec := range specs {
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
	// Every name the block declares stays declared, whether or not a pool took
	// its name: the member is a credential of the project's and needs none
	// written for it.
	declared := map[string]bool{config.CredentialVertex: true}
	for _, c := range lc.Credentials {
		declared[c.Name] = true
	}
	creds := make([]config.Credential, 0, len(lc.Credentials)+len(members))
	for _, c := range lc.Credentials {
		if members[c.Name] && c.Type == config.CredentialTypeClaudeOAuth {
			continue
		}
		creds = append(creds, c)
	}
	names := slices.Sorted(maps.Keys(members))
	for _, name := range names {
		if declared[name] {
			continue
		}
		creds = append(creds, config.Credential{Name: name, Type: config.CredentialTypeClaudeOAuth})
		declared[name] = true
	}
	return pools, creds
}

// printRoutingMigration is the report, printed before any write: what each
// repository's runs go out on and that nothing of its own moves, then the whole
// of what does.
func (r *initRun) printRoutingMigration(lc *localcfg.Config, cfgs map[string]*config.Config, mig routing.Migration) {
	w := r.stageDetail()
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
		fmt.Fprintln(w, "routing: fallback is auto, so a run pays on the project's key as soon as its subscriptions are exhausted, rather than waiting for the old threshold")
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
