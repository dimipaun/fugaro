package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// The publish half of the policy stage of `fugaro update`: the node every run
// of the project is admitted by, built from the local config and written by the
// one command the user runs, instead of a second command (`fugaro project
// policy publish`) after every config edit. The migration half stays where it
// was (init_policy_migrate.go); this file is the diff, the confirmation, the
// ETag write and the generation record. Both halves share one read, one
// ownership gate and one write path (policyMigrationFor), and both doors build
// the document through policyNodeForPublish, so the stage and
// `fugaro project policy publish` cannot drift into two builds of one node.

// policyPublish is the node the local config states, when the published one
// does not carry it.
type policyPublish struct {
	body    []byte
	pol     *projectpolicy.Policy
	drift   []policyDrift
	sources map[string]string // where each seeded allow_repos entry came from, for the printed list
	adopted bool              // no generation was recorded for this file, so this run adopts the published one (D854)
}

// policyDrift is one member the local config states and the published node does
// not carry: what the three-way state prints, and what a re-seed writes back.
type policyDrift struct {
	Member string
	File   string // the local config's value, when it is a scalar worth printing
	Node   string // the published node's value, the same way
}

// policyConfigDrift is the members of the published node the local config is
// the source of and that the node does not carry, in the order a diff reads.
//
// The comparison is the JSON both sides spell, so a member the node omits and a
// config that states nothing for it are one value (the carrier's omitempty is
// what drops an empty list on the way in). Only the members a config edit has to
// reach a run through are compared: the keys no command of this release writes
// into the node (clone_urls, credentials, allow_repos) are the node's own, and
// `providers` reaches a run through its own job's copy, not through this node
// (F9), so a config whose only difference is its providers: block is no reason
// to rewrite what every run of the project is admitted by.
func policyConfigDrift(lc *localcfg.Config, p *projectpolicy.Policy) []policyDrift {
	if lc.Budget == nil || p == nil {
		// A config with no budget block names no budget backend, so the stage
		// reads no node for it at all (policyNodeUnreachable); and a node the
		// database holds as empty is nothing to differ from.
		return nil
	}
	var out []policyDrift
	add := func(member string, file, node any, show bool) {
		if jsonText(file) == jsonText(node) {
			return
		}
		d := policyDrift{Member: member}
		if show {
			d.File, d.Node = jsonText(file), jsonText(node)
		}
		out = append(out, d)
	}
	add("budget.mode", lc.Budget.Mode, p.Budget.Mode, true)
	add("budget.per_run_usd", lc.Budget.PerRunUSD, p.Budget.PerRunUSD, true)
	add("budget.max_run_tokens", lc.Budget.MaxRunTokens, p.Budget.MaxRunTokens, true)
	add("budget.allowed_models", lc.Budget.AllowedModels, p.Budget.AllowedModels, false)
	add("model_prices", policyPrices(lc), p.Prices, false)
	add("base_branch", lc.BaseBranch, p.BaseBranch, true)
	if lc.Routing != nil && lc.Routing.Claude != nil {
		add("routing", lc.Routing, p.Routing, false)
	}
	if lc.ReviewFloor != nil {
		if floor, err := lc.ReviewFloorPolicy(); err == nil {
			add("review_floor", floor, p.ReviewFloor, false)
		}
	}
	// The verifier list is compared the same way and for the same reason: the
	// node is where the credentials service's list comes from, so a config edit
	// that names a verifier and a node that carries none is exactly the gap
	// this stage exists to close. A config silent about the list names nothing
	// — it keeps the node's (localcfg's rule) — and a written empty one differs
	// from a node that still carries a list, which is the removal an operator
	// has to see.
	if lc.VerifierAccounts != nil {
		add("verifier_accounts", *lc.VerifierAccounts, p.VerifierAccounts, false)
	}
	// The same rule for the image catalog (D1140): a config silent about it
	// keeps the node's, and a config that states one, an empty one included,
	// differs from a node that carries another, so a catalog-only edit reaches
	// the node through `fugaro update` with no second command.
	if lc.ImageCatalog != nil {
		add("image_catalog", lc.ImageCatalogEffective(nil), p.ImageCatalog, false)
	}
	// The orchestrator block is compared by the publisher's own keep rule
	// (D1239, D1234): a config that states a block that sets a key differs from
	// a node carrying another — or none — so a guardrail-only edit reaches the
	// node through `fugaro update` with no second command. The node's own block
	// is handed to the rule here, where the catalog's line passes nil: a written
	// block that sets no key is not a statement but the absent one
	// (OrchestratorEffective, SetsNothing), so comparing against nil would
	// report a difference a publish does not close.
	if lc.Orchestrator != nil {
		add("orchestrator", lc.OrchestratorEffective(p.Orchestrator), p.Orchestrator, false)
	}
	// The members a publish DERIVES from the local config are compared too, by
	// their own derivation: the accounts block, which is the operator's
	// claude_accounts.<name>.allow_data_to qualified with the provider of the
	// profile that names the account (D3), and each profile's list of the
	// accounts it serves. Without them a config edit that only declares an
	// account is a config the node never learns: the account keys are looked up
	// per named account here and the naming profile beside them, so a profile
	// that gains a name moves this member and nothing else.
	//
	// The comparison is the derivation and never the profiles block as a whole,
	// because the rest of a profile is the node's own — `fugaro project repos
	// add` writes its allow_repos — and a publish that fired on those would
	// rewrite the node on every run of the project without ever closing the gap
	// it fired on.
	profiles := policyProfiles(lc)
	accounts, err := projectpolicy.DeriveAccounts(profiles, lc.ClaudeAccounts, lc.ProjectSecrets)
	if err != nil {
		// The config names an account that cannot be published at all (one no
		// field defines, an empty allow_data_to, a pattern where an exact
		// repository is required). There is no pair of documents to compare
		// then, and the entry stands for the member: it sends the run to the
		// publisher, whose own refusal names the file and the entry to fix.
		out = append(out, policyDrift{Member: "accounts"})
		return out
	}
	add("accounts", accounts, p.Accounts, false)
	for _, name := range slices.Sorted(maps.Keys(profiles)) {
		var served []string
		if np, ok := p.Profiles[name]; ok {
			served = np.ClaudeAccounts
		}
		add("profiles."+name+".claude_accounts", profiles[name].ClaudeAccounts, served, false)
	}
	// The account-choice keys are compared too — the project's
	// default_claude_account and each stage's own claude_account — because
	// this file is their only store and the publish is what carries them
	// (docs/design/mixed-credentials.md §11): the runner reads them off the
	// published node alone (resolveMixedStages, mixedDefaultAccount), so a
	// config that names an account while the node does not carry it is a
	// launch that admits the mix and a job that mounts the token beside a
	// runner that never sees the keys (B7).
	//
	// They are compared whether or not this file states them, which is the
	// derived members' rule above and not the keep rule review_floor, the
	// verifier list and the catalog get: the publisher COPIES these members
	// from the file and keeps none of them, so a file silent about the key
	// states no account — the node it builds carries no member — and a node
	// that names one is a difference only a publish closes. The stage names
	// are the union of the two documents' for the same reason: an entry the
	// node carries and the file does not is a stage the publish takes off the
	// account, and comparing the file's own stages alone would leave the
	// runner serving it there.
	fileAccounts, nodeAccounts := configAccountValues(lc), policyAccountValues(p)
	for _, member := range accountChoiceNames(fileAccounts, nodeAccounts) {
		add(member, fileAccounts[member], nodeAccounts[member], false)
	}
	return out
}

// stageAccountMember is the member a stage's account is published under: the
// node spells the stage accounts as one member keyed by stage, so this is the
// key inside it that a diff and a widening sentence name.
func stageAccountMember(stage string) string { return "claude_account_stages." + stage }

// configAccountValues is the account-choice members this file states, keyed the
// way the published node spells them, with the keys it states nothing for left
// out. The two are the keys `settings set` writes (internal/settings, the
// claude_account entries), and the key names are what the diff and the widening
// sentence show an operator.
func configAccountValues(lc *localcfg.Config) map[string]string {
	out := map[string]string{}
	if lc.DefaultClaudeAccount != "" {
		out["default_claude_account"] = lc.DefaultClaudeAccount
	}
	for stage, choice := range lc.ClaudeAccountStages {
		if choice.ClaudeAccount != "" {
			out[stageAccountMember(stage)] = choice.ClaudeAccount
		}
	}
	return out
}

// accountChoiceNames is the account-choice members either side names, in the
// order both diffs read: the project's default, then the stages by name.
func accountChoiceNames(sides ...map[string]string) []string {
	names := map[string]bool{}
	for _, side := range sides {
		for name := range side {
			names[name] = true
		}
	}
	var out []string
	if names["default_claude_account"] {
		out = append(out, "default_claude_account")
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		if name != "default_claude_account" {
			out = append(out, name)
		}
	}
	return out
}

// jsonText is a value as the two documents spell it, with the empty forms
// folded onto the absent one: a node that omits a member and a config that
// states an empty list for it mean the same thing, and comparing their bytes
// would report a difference no publish can close.
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "?"
	}
	switch string(b) {
	case "null", "[]", "{}":
		return ""
	}
	return string(b)
}

// policyConfigPath is the local config file this installation's policy keys live
// in: the file a publish records the published generation in (D854), and the
// file a re-seed writes.
//
// It is worked out from the project and the environment rather than handed down,
// because only one of the two doors that reach the publish holds the path the
// run selected — `fugaro update`'s engine does, `fugaro upgrade`'s step discards
// it — and both doors reach the publish through one function
// (policyMigrationFor). $FUGARO_CONFIG wins when it holds this project, the
// project's own path under the config directory (localcfg.ProjectPath)
// otherwise: the two places a project config is. A file that does not load as
// this project is never taken for it, and a run whose config is somewhere else
// entirely (`--config`) gets no path at all — the publish half is then left out
// of the plan rather than recording a baseline in a file it was not built from,
// because a baseline that belongs to another file suppresses exactly the refusal
// D854 adds.
func policyConfigPath(lc *localcfg.Config) string {
	var candidates []string
	if p := os.Getenv("FUGARO_CONFIG"); p != "" {
		candidates = append(candidates, p)
	}
	if p, err := localcfg.ProjectPath(os.Getenv, lc.Name); err == nil {
		candidates = append(candidates, p)
	}
	for _, p := range candidates {
		if held, err := localcfg.Load(p); err == nil && held.Name == lc.Name {
			return p
		}
	}
	return ""
}

// policyContextRefusal refuses the node about to be published when it carries a
// catalog entry whose source names a build context, while no released runner
// reads that member (imageContextShipped). The member is not optional the way
// the catalog key itself is: an older runner decodes the node strictly and
// refuses the whole image_catalog block an unknown member sits in
// (projectpolicy's catalogSourceMembers), so the node would fail every run of
// the project rather than the one job the entry builds — the failure D1403
// keeps out of a published node.
//
// The node is judged and not the local config, because the node is what the
// older runner reads: a config silent about the catalog keeps the published
// one, so the member can reach these bytes from either side. Both doors build
// the node through policyNodeForPublish, which is why the check is here.
//
// shipped is passed rather than read so a test can ask both halves of the
// gate, the way the skew gates' callers do.
func policyContextRefusal(p *projectpolicy.Policy, shipped bool) error {
	if shipped || p == nil || p.ImageCatalog == nil {
		return nil
	}
	for _, e := range p.ImageCatalog.Entries {
		if e.Source.Context == "" {
			continue
		}
		return userErr("the image catalog's entry %s names a build context, and no fugaro release reads that key yet: the policy node is decoded strictly by every runner, so a published node carrying it is refused whole and every run of the project fails until someone removes it. Leave context: off the entry until the release that reads it ships, then fugaro update publishes it",
			e.Name)
	}
	return nil
}

// policyNodeForPublish is the one build both doors take: the node the operator's
// config states, on top of the node it replaces, with the allow-list seeded or
// kept as the writer's rule has it. `fugaro project policy publish` and the
// policy stage call it, so the bytes they write from one local config cannot
// differ; TestUpdateStageAndTheExplicitDoorBuildTheSameNode is the guard.
func policyNodeForPublish(ctx context.Context, lc *localcfg.Config, old *projectpolicy.Policy, base []byte, exists, seed, removeRouting bool) ([]byte, *projectpolicy.Policy, map[string]string, error) {
	allow, sources, err := publishAllowRepos(ctx, lc, old, seed)
	if err != nil {
		return nil, nil, nil, err
	}
	pol, keepRouting, err := buildProjectPolicy(lc, allow, old, removeRouting)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := policyContextRefusal(pol, imageContextShipped); err != nil {
		return nil, nil, nil, err
	}
	body, err := policyNodeBytes(pol, base, exists, keepRouting)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := checkPolicyBytes(body); err != nil {
		return nil, nil, nil, err
	}
	return body, pol, sources, nil
}

// policyCeiling is one member a publish loosens: the node's key, and the two
// values as a settings write spells them.
type policyCeiling struct {
	Member string
	Was    string
	Now    string
}

// policyCeilingMembers are the published node's ceiling members, in key order:
// the keys `settings set` compares through settingsLoosens, and the
// orchestrator's guardrails (D1239, O4), which are caps of the same rule — what
// bounds how much work a project's orchestrator sets going at once, which §4.1
// rule 6 refuses in the words of its own body: "a command that would raise a cap
// is refused, whichever command it is". A node's other members — base_branch,
// routing, model_prices, the profiles, review_floor — decide where a run goes
// and what it may do, and none of them is a ceiling a publish raises, so none of
// them is what the typed name guards here.
var policyCeilingMembers = func() []string {
	out := []string{
		"budget.mode", "budget.per_run_usd", "budget.max_run_tokens", "budget.allowed_models",
		"orchestrator.max_running", "orchestrator.batch_size", "orchestrator.spawn_per_minute",
		"orchestrator.spawn_per_hour", "orchestrator.spawn_per_day",
	}
	// The block's model member is one member per class: a class's model is
	// compared with its own class's and never with the other's, so a class
	// added to the vocabulary later is a member this list carries as soon as
	// DecisionClasses names it.
	for _, class := range policy.DecisionClasses {
		out = append(out, policyDecisionModelMember(class))
	}
	return out
}()

// policyDecisionModelPrefix is the member name a decision class's model is
// compared under. It is one prefix because both readers of the member have to
// recognize it: the list above spells it through policyDecisionModelMember, and
// settingsLoosens is where its two values are compared.
const policyDecisionModelPrefix = "orchestrator.decision_model_per_class."

// policyDecisionModelMember is the ceiling member one class's model is compared
// as. The name is the node's own path to the entry, so a refusal naming it says
// which class moved.
func policyDecisionModelMember(class policy.DecisionClass) string {
	return policyDecisionModelPrefix + string(class)
}

// policyCeilingValues is a node's ceiling members spelled the way the settings
// commands spell them, so the one comparison (settingsLoosens) decides both
// doors and a widening this stage would ask nobody about is a widening
// `settings set` refuses. The spellings are the ones settings_set.go reads: the
// mode's own name, a list joined with ",", a number as the yaml file writes it —
// where 0 is the value that sets no cap at all, which settingsLoosens already
// reads as the loosest a ceiling can be written — and a decision model by its
// name, compared by its tier rather than parsed as a number.
//
// The orchestrator block is spelled with its unset keys filled from the
// guardrails Fugaro ships (policy.WithDefaults), because that is the ceiling a
// node that states none is held to: a project with no spawn rate written is a
// project held to 2 a minute, so a file that starts stating 60 raises that
// ceiling exactly as one that raises a stated 4 does, and a file that stops
// stating a rate it did state gives the shipped guardrail back. The running cap
// is the one key WithDefaults leaves unset with no person to take it from: it is
// spelled as the node states it, and the empty spelling says the person's own
// max_parallel is what bounds a run — a value no file of this project's can
// raise, and so not one this comparison answers for.
func policyCeilingValues(p *projectpolicy.Policy) map[string]string {
	out := make(map[string]string, len(policyCeilingMembers))
	if p == nil {
		return out
	}
	out["budget.mode"] = p.Budget.Mode
	out["budget.per_run_usd"] = formatFloat(p.Budget.PerRunUSD)
	out["budget.max_run_tokens"] = strconv.FormatInt(p.Budget.MaxRunTokens, 10)
	out["budget.allowed_models"] = strings.Join(p.Budget.AllowedModels, ", ")
	var o policy.Orchestrator
	if p.Orchestrator != nil {
		o = *p.Orchestrator
	}
	o = o.WithDefaults(0)
	if o.MaxRunning != 0 {
		out["orchestrator.max_running"] = strconv.FormatInt(o.MaxRunning, 10)
	}
	out["orchestrator.batch_size"] = strconv.FormatInt(o.BatchSize, 10)
	out["orchestrator.spawn_per_minute"] = strconv.FormatInt(o.SpawnPerMinute, 10)
	out["orchestrator.spawn_per_hour"] = strconv.FormatInt(o.SpawnPerHour, 10)
	out["orchestrator.spawn_per_day"] = strconv.FormatInt(o.SpawnPerDay, 10)
	for class, name := range o.DecisionModelPerClass {
		out[policyDecisionModelMember(class)] = name
	}
	return out
}

// policyLoosening is the ceiling members the node about to be published would
// loosen, measured against the node it replaces, in key order.
//
// The comparison is settingsLoosens, the same function `settings set` decides
// with, because the two are one question: what a run of this project is
// admitted by. A member the new node states nothing for is the ceiling REMOVED
// — the value text cannot say it, since an emptied model list reads as a subset
// of every list and a mode that is gone reads as nothing to compare — so it is
// named here, the way `settings unset` is typed before its own comparison is
// asked rather than by it.
func policyLoosening(old, new *projectpolicy.Policy) []policyCeiling {
	was, now := policyCeilingValues(old), policyCeilingValues(new)
	var out []policyCeiling
	for _, member := range policyCeilingMembers {
		before, after := was[member], now[member]
		if before == after {
			continue
		}
		switch {
		case after == "":
			out = append(out, policyCeiling{Member: member, Was: before, Now: "(removed)"})
		case settingsLoosens(member, before, after):
			out = append(out, policyCeiling{Member: member, Was: before, Now: after})
		}
	}
	return out
}

// policyAccountWidening is the account-choice members this publish would SET
// where the node it replaces names none of them or names another account: the
// project's default_claude_account, and each stage's own claude_account.
//
// It is a widening in the sense the typed name is asked for, and it is measured
// member by member against the node it replaces for that reason. The account is
// the credential a mixed run's review goes out on
// (docs/design/mixed-credentials.md §11), so a node that starts naming one hands
// a stage a credential it did not hold — and, to an api-key run, the mix the
// ruling admits, which the launch resolver reads off this node and nothing else,
// while the credential itself is mounted into the job's environment from this
// machine's config. So the publish is the moment the operator's choice becomes
// the project's, and that is the difference from the local write of the same key
// `settings set` makes: both are confirmed, and only this one hands the account
// to every run.
//
// A member this publish REMOVES — the node names one and this file names none —
// is not a widening: it takes a credential away from a stage, and the
// comparison settingsLoosens makes for a ceiling has the same shape (a member
// the new node states nothing for is carried by the removal branch above). A
// member the publish CHANGES to another account is one: the project's runs stop
// going out on the account the node named, which is a widening of who holds the
// review credential wherever the new name is pointed at.
func policyAccountWidening(old, new *projectpolicy.Policy) []policyCeiling {
	was, now := policyAccountValues(old), policyAccountValues(new)
	var out []policyCeiling
	for _, member := range accountChoiceNames(was, now) {
		after := now[member]
		// A member the node states nothing for is the account this publish
		// REMOVES, which is the tightening direction and is skipped; a member
		// the two name alike is no change at all.
		if after == "" || after == was[member] {
			continue
		}
		before := was[member]
		if before == "" {
			before = "(not set)"
		}
		out = append(out, policyCeiling{Member: member, Was: before, Now: after})
	}
	return out
}

// policyAccountValues is a node's account-choice members, keyed the way the
// node's own document spells them, with the members it states nothing for left
// out. It is the node side of configAccountValues, and the two are one
// comparison: what this file states and what the publish would carry.
func policyAccountValues(p *projectpolicy.Policy) map[string]string {
	out := map[string]string{}
	if p == nil {
		return out
	}
	if p.DefaultClaudeAccount != "" {
		out["default_claude_account"] = p.DefaultClaudeAccount
	}
	for stage, account := range p.ClaudeAccountStages {
		if account != "" {
			out[stageAccountMember(stage)] = account
		}
	}
	return out
}

// loosening is this run's publish measured against the node it replaces, and it
// is empty for every run that publishes nothing: Migrate moves a version and
// derives the members the config states, and a member it derives is not a
// ceiling the operator's config states. The account-choice members are asked
// beside the ceilings (policyAccountWidening) rather than through them, since a
// node's account is not a member settingsLoosens reads.
//
// A FIRST publish is the one publish with no node to measure against, and it is
// a widening by its own name rather than an empty comparison: the document it
// writes is what every run of the project is admitted by, chosen where none was,
// so its allow-list admits repositories nothing admitted before — M3's own
// reading of a first node (`policyReposRefusal`), which is what makes the first
// node the owner's to write. The comparison below cannot see that:
// settingsLoosens reads a member the node did not carry as no loosening, which
// is right when an operator edits a ceiling that is there and wrong when the
// ceiling is created. The entry is named here for that reason, and its Member is
// the node itself — looseningWhy words the first publish as the widening of the
// whole node, so it never prints the two empty values.
//
// It is what gives a first publish the Typed class: the typed name rather than
// --yes, and a coding agent's session refused by the gate's own D1026 rule
// rather than admitted to the boundary every later run of the project is
// admitted by.
func (m *policyMigration) loosening() []policyCeiling {
	if m.pub == nil {
		return nil
	}
	if m.firstPublish {
		return []policyCeiling{{Member: projectpolicy.PathShared()}}
	}
	// The account keys are not ceilings, so settingsLoosens has nothing to say
	// about them and their own comparison is asked beside it: setting one is the
	// same class of decision — who is admitted, and on whose credential — that
	// the typed name guards here.
	return append(policyLoosening(m.node, m.pub.pol), policyAccountWidening(m.node, m.pub.pol)...)
}

// looseningWhy is the sentence the typed confirmation shows and the refusal
// gives: which member loosens, from what to what, and the command that reaches
// the same publish at the operator's own terminal. --yes is named as the thing
// that does not stand in rather than passed over, because the operator reading
// this from a script has just passed it and would otherwise take it for the
// answer.
//
// The first publish has no member to compare and no value to name on either
// side, so it says what it widens instead: a project with nothing published is
// a project every run reaches through its job's environment, and the node
// written now is the one every run of the project is admitted by from here on.
func (m *policyMigration) looseningWhy(loosening []policyCeiling) string {
	if m.firstPublish {
		return fmt.Sprintf("the project has no published policy node at %s, and this run writes the one every run of the project is admitted by — its allow-list, its ceilings and its catalog are chosen here for the first time. The project's own name is what confirms it and --yes does not stand in; run this yourself at your own terminal: %s",
			projectpolicy.PathShared(), selfCommand()+" update")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "this publish loosens %s, which every run of the project is admitted by: ", projectpolicy.PathShared())
	for i, c := range loosening {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s %s -> %s", c.Member, c.Was, c.Now)
	}
	fmt.Fprintf(&b, ". The project's own name is what confirms it and --yes does not stand in; run this yourself at your own terminal: %s", selfCommand()+" update")
	return b.String()
}

// printPolicyPublishDiff is what the operator confirms: which members this
// publish changes, and — where the migration half is not already printing them
// — the allow-list entries and where they came from.
func printPolicyPublishDiff(w io.Writer, drift []policyDrift, sources map[string]string) {
	if len(drift) == 0 {
		return
	}
	fmt.Fprintf(w, "%s: the local config states what the node does not carry\n", projectpolicy.PathShared())
	printPolicyDrift(w, drift)
	if len(sources) > 0 {
		fmt.Fprintf(w, "  allow_repos is seeded from this machine's repositories: %s\n", entryListText(sources))
	}
}

// printFirstPublishDiff is printPolicyPublishDiff for a project whose node does
// not exist yet (#1656), and it is a renderer of its own because there is no
// published side to render: the drift is measured against an empty document, so
// every member's right-hand value is that document's zero rather than anything
// the project ever published, and the arrow would point at it — "per_run_usd: 5
// (this machine) -> 0 (published)" names a node nobody published, in the one
// confirmation the operator cannot check against a node they do not have. What
// is shown instead is what the node will carry, member by member; the members
// the config is silent about carry nothing in this node, so naming them would
// describe a write that does not happen.
func printFirstPublishDiff(w io.Writer, drift []policyDrift, sources map[string]string) {
	if len(drift) == 0 {
		return
	}
	fmt.Fprintf(w, "%s: this publish creates the node, carrying what the local config states\n", projectpolicy.PathShared())
	for _, d := range drift {
		if d.File == "" {
			fmt.Fprintf(w, "  %s differs\n", d.Member)
			continue
		}
		fmt.Fprintf(w, "  %s: %s\n", d.Member, d.File)
	}
	if len(sources) > 0 {
		fmt.Fprintf(w, "  allow_repos is seeded from this machine's repositories: %s\n", entryListText(sources))
	}
}

// printPolicyDrift is the three-way state of a publish: this machine's file, and
// the node as it stands now, member by member.
func printPolicyDrift(w io.Writer, drift []policyDrift) {
	for _, d := range drift {
		switch {
		case d.File == "" && d.Node == "":
			fmt.Fprintf(w, "  %s differs\n", d.Member)
		case d.File == "":
			fmt.Fprintf(w, "  %s: (not set in the config) -> %s\n", d.Member, d.Node)
		default:
			fmt.Fprintf(w, "  %s: %s (this machine) -> %s (published)\n", d.Member, d.File, d.Node)
		}
	}
}

// printReseedDrift is printPolicyDrift for D854's re-seed, where the arrow points
// the other way: a publish writes this machine's values INTO the node, and a
// re-seed writes the node's values INTO this machine's file. The publish's
// wording on a re-seed would tell the operator their file is about to be
// published over the node — which is the one thing the re-seed exists to stop —
// when what the run does is the reverse.
func printReseedDrift(w io.Writer, drift []policyDrift) {
	for _, d := range drift {
		switch {
		case d.File == "" && d.Node == "":
			fmt.Fprintf(w, "  %s differs\n", d.Member)
		case d.File == "":
			fmt.Fprintf(w, "  %s: (not set in the config) re-seeded from %s (published)\n", d.Member, d.Node)
		case d.Node == "":
			fmt.Fprintf(w, "  %s: %s (this machine), which the published node does not carry\n", d.Member, d.File)
		default:
			fmt.Fprintf(w, "  %s: %s (this machine) re-seeded from %s (published)\n", d.Member, d.File, d.Node)
		}
	}
}

// entryListText is a sorted "a, b, c" of a source map's keys.
func entryListText(sources map[string]string) string {
	return strings.Join(slices.Sorted(maps.Keys(sources)), ", ")
}

// policyGenerationState is D854's third value: the generation this machine's
// config was written from, against the one the node carries now.
type policyGenerationState struct {
	recorded  string
	published string
}

func (s policyGenerationState) text() string {
	return fmt.Sprintf("%s recorded for this machine's config, %s published at %s", generationText(s.recorded), generationText(s.published), projectpolicy.PathShared())
}

// generationText is a generation as a sentence reads it. The value is the
// node's ETag — its own version marker, and the same string the write is
// conditional on (rtdb.PutIfMatch) — and an absent one is the state of every
// installation before its first publish of this release.
func generationText(g string) string {
	if g == "" {
		return "none"
	}
	return g
}

// policyGenerationRecord writes the generation the local config's policy keys
// are now written from, leaving every other byte of the file as it is
// (yamledit: this file is one a person edits by hand, and marshalling the whole
// struct would drop the comments and the order they chose). The file it
// replaces is backed up first and the write is atomic, the same triple the
// local config's own writer takes.
//
// It runs after the write landed, never before: the generation recorded is the
// one the node carries now — the one this machine's file was written from — and
// recording the one read before the write would leave the record pointing at an
// object that no longer exists, which the next run would read as another
// machine's publish.
func policyGenerationRecord(w io.Writer, path string, lc *localcfg.Config, gen string) error {
	if path == "" {
		return userErr("the published policy node's generation cannot be recorded: this installation has no local config file (a shared config has none)")
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return userErr("recording the published policy node's generation in %s: %v", path, err)
	}
	out, err := yamledit.SetScalar(old, []string{"policy_generation"}, gen)
	if err != nil {
		return userErr("recording the published policy node's generation in %s: %v", path, err)
	}
	if !yamledit.Changed(old, out) {
		lc.PolicyGeneration = gen
		return nil
	}
	backup, err := backupFile(path, old, time.Now())
	if err != nil {
		return userErr("backing up %s before recording the published policy node's generation: %v", path, err)
	}
	if err := writeFileAtomic(path, out); err != nil {
		return userErr("recording the published policy node's generation in %s: %v", path, err)
	}
	fmt.Fprintf(w, "recorded the published policy node's generation in %s (backup at %s)\n", path, backup)
	lc.PolicyGeneration = gen
	return nil
}

// recordPublishedGeneration records the generation of the node a run has just
// written, in the local config file the run built that node from. Both doors
// take it — the policy stage after its write and `fugaro project policy publish`
// after its own — so the record follows every write and not only the stage's.
//
// The ETag is read back and checked against the document the run wrote before it
// is recorded: the write is conditional on the ETag the run read, and a writer
// that landed between it and this read holds the node now. Recording THAT
// generation would leave this machine's file claiming a node it never wrote,
// which is the one state D854 then cannot catch — the next edit of the file would
// look like this machine's own publish and would overwrite the newer node. So
// nothing is recorded, and the refusal says what happened and where the node is
// read.
//
// Every failure here happens AFTER the write landed, so every one of them says
// so: the caller reports it as the run's outcome, and a sentence that read as a
// publish that failed would hide the node this run moved — the operator would
// look for a write that is already there.
func recordPublishedGeneration(ctx context.Context, w io.Writer, db *rtdb.Client, lc *localcfg.Config, path string, payload []byte) error {
	if path == "" {
		// A run that holds no local config file — the shared config has none, and
		// a config elsewhere was not built from this file — has nothing to record
		// the generation in, and the node it wrote is not refused for it.
		return nil
	}
	etag, found, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return fmt.Errorf("the policy node at %s was written, but the generation to record in %s could not be read back, so that config still names the generation from before this write — its next edit is refused as one made against a node that has moved, read what the node holds now with 'fugaro project policy show': %w",
			projectpolicy.PathShared(), path, err)
	}
	if !found {
		return userErr("the policy node at %s was written by this run and is gone now, so no generation was recorded in %s: another writer deleted the node after this publish landed. Nothing has to be put back in step — a project with no published policy reads nothing — and the next publish of one ('fugaro project policy publish') records its own generation",
			projectpolicy.PathShared(), path)
	}
	if !sameNode(raw, payload) {
		// The node holds a document other than the one this run wrote, so the
		// generation to record is not this run's to claim. What the next run
		// does with the record then depends on what the node carries against
		// what the config states — it records the generation when the two
		// agree, and offers the re-seed when they do not — and the sentence
		// says both rather than promising the refusal: the state this one
		// leaves is not always the state that refuses, and the operator who
		// went looking for a re-seed that never came is who it cost.
		return userErr("the policy node at %s was written and then changed again before the generation could be recorded in %s: another writer holds a different document there now, so nothing was recorded and the config still records the generation from before this write. The next %s compares this machine's config with the node it finds and records that node's generation when the node carries what the config states; when the node carries something else it refuses and offers to re-seed the file from the node instead. Read what the node holds now with 'fugaro project policy show'",
			projectpolicy.PathShared(), path, selfCommand()+" update")
	}
	return policyGenerationRecord(w, path, lc, etag)
}

// sameNode is whether two documents are the same node: the values they hold
// rather than the bytes they are spelled with, because the database is free to
// hand an object back with its keys in another order, a number spelled another
// way or a member it stores no value for gone entirely. A member the database
// stores no value for is not a member, and it stores none for three shapes: a
// null one, an empty object, and an empty list — an RTDB list is an object with
// numeric keys, so an empty list is an empty object and is pruned the same way.
// This machine's policy carries all three: budget.allowed_models is written
// null when the file names no models, and a publish from a config that lists no
// repository writes allow_repos as an empty list the database keeps no member
// for. Comparing the bytes would report every such publish as one another
// writer had moved, and the generation would never be recorded (#1658). Numbers
// are read into float64 on both sides (json.Unmarshal's default), those members
// are dropped, and objects are marshalled with sorted keys, so the two sides
// are compared as the values they are.
func sameNode(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, err := json.Marshal(dropAbsentMembers(x))
	if err != nil {
		return false
	}
	jb, err := json.Marshal(dropAbsentMembers(y))
	return err == nil && string(ja) == string(jb)
}

// dropAbsentMembers is a document with every object member the database stores
// no value for removed, recursively: a null one, an empty object, and an empty
// list (an RTDB list is an object with numeric keys, so an empty list is an
// empty object). A null inside a list is a value and is left alone: what the
// database drops is a member, not a position.
func dropAbsentMembers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, m := range t {
			folded := dropAbsentMembers(m)
			if storesNoMember(folded) {
				continue
			}
			out[k] = folded
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, m := range t {
			out[i] = dropAbsentMembers(m)
		}
		return out
	default:
		return v
	}
}

// storesNoMember is whether the database keeps no value for a member holding v:
// null, an empty object, or an empty list.
func storesNoMember(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

// policyEmptyAllowListWarning is what a publish says before it writes a policy
// whose allow-list admits no repository, in doctor's own words (the
// allow-list-empty check): once that node is published, every run of the
// project stops before it starts. It is one function so the policy stage of
// `fugaro update` can say the same sentence when it makes the first publish
// (#1656, #1674), and it names the command that enrolls a repository, because a
// refusal that only said what was wrong would leave the operator to find the
// fix.
func policyEmptyAllowListWarning() string {
	return "the published policy admits no repository, so every run stops before it starts; run `fugaro project repos add provider/owner/name` to enroll one"
}

// confirmEmptyAllowList is the confirmation every publish that would write an
// allow-list admitting nothing takes before its first write: a person types the
// project's name at a terminal, no flag stands in for it, and a session with no
// terminal — a script's, or a coding agent's — is refused, so an empty
// allow-list is never written on the operator's behalf by something that cannot
// have read the consequence (#1674). The same wording is both the confirmation
// and the refusal, so the two cannot drift.
func confirmEmptyAllowList(cmd *cobra.Command, lc *localcfg.Config, agent string) error {
	why := policyEmptyAllowListWarning()
	// A coding agent's session is refused before any prompt: a terminal it can
	// give itself (a pty wrapper) is not a person reading the consequence.
	if agent != "" {
		return userErr("%s", why)
	}
	return confirmTypedStrict(cmd, lc, why, why)
}

// reseedPolicyKeys writes the published node's policy values back into the local
// config, one yamledit operation per member, and reports the members it could
// not write (hand) beside the ones it did (written).
//
// It is D854's remedy for a machine whose file is behind: the operator accepts
// the published node as what this machine's file is written from. What it can
// write is what one splice can express — a scalar, or a list taken item by item
// — and a member it cannot is named for the operator rather than skipped: a
// re-seed that recorded the generation while leaving a member behind would let
// the next publish overwrite the newer node with the older file, which is the
// whole of what D854 refuses.
//
// It edits the bytes its caller read (old), rather than reading the file itself:
// the write the caller ends with is guarded on exactly those bytes
// (writeFileAtomicGuarded), and a second read of the same file would be a splice
// made against contents the backup beside it does not hold.
func reseedPolicyKeys(old []byte, lc *localcfg.Config, p *projectpolicy.Policy, members []string) (out []byte, written, hand []string) {
	out = old
	// apply is what the caller's own config learns from a splice that landed:
	// the members this re-seed writes are the published node's now, and a config
	// left stating the values the file no longer holds is one a stage re-planned
	// inside the same run — the rules stage's forget and re-plan is exactly that
	// — would read as drift and publish over the newer node the re-seed exists
	// to protect.
	set := func(member string, keyPath []string, value string, apply func()) {
		if value == "" {
			// A member the node leaves out is a key the file must drop, which
			// one splice cannot state without the value it would have to know.
			hand = append(hand, member)
			return
		}
		next, err := yamledit.SetScalar(out, keyPath, value)
		if err != nil {
			hand = append(hand, member)
			return
		}
		out = next
		apply()
		written = append(written, member)
	}
	setList := func(member string, keyPath []string, want, have []string, apply func()) {
		for _, item := range have {
			if slices.Contains(want, item) {
				continue
			}
			next, err := yamledit.RemoveListItem(out, keyPath, item)
			if err != nil {
				hand = append(hand, member)
				return
			}
			out = next
		}
		for _, item := range want {
			if slices.Contains(have, item) {
				continue
			}
			next, err := yamledit.AddListItem(out, keyPath, item)
			if err != nil {
				hand = append(hand, member)
				return
			}
			out = next
		}
		apply()
		written = append(written, member)
	}
	for _, member := range members {
		switch member {
		case "budget.mode":
			set(member, []string{"budget", "mode"}, p.Budget.Mode, func() { lc.Budget.Mode = p.Budget.Mode })
		case "budget.per_run_usd":
			set(member, []string{"budget", "per_run_usd"}, formatFloat(p.Budget.PerRunUSD), func() { lc.Budget.PerRunUSD = p.Budget.PerRunUSD })
		case "budget.max_run_tokens":
			set(member, []string{"budget", "max_run_tokens"}, fmt.Sprintf("%d", p.Budget.MaxRunTokens), func() { lc.Budget.MaxRunTokens = p.Budget.MaxRunTokens })
		case "budget.allowed_models":
			setList(member, []string{"budget", "allowed_models"}, p.Budget.AllowedModels, lc.Budget.AllowedModels, func() {
				lc.Budget.AllowedModels = slices.Clone(p.Budget.AllowedModels)
			})
		case "base_branch":
			set(member, []string{"base_branch"}, p.BaseBranch, func() { lc.BaseBranch = p.BaseBranch })
		default:
			// The account-choice members — default_claude_account and each
			// claude_account_stages.<stage> — land here, and the refusal above
			// names them: this re-seed splices the members it has a shape for,
			// and these are the operator's own settings, which is why the local
			// config is their only store (the locLocal rows `fugaro settings set`
			// writes). An operator whose node carries an account this file does
			// not puts it back with that command — `fugaro settings set
			// default_claude_account <name>`, `fugaro settings set
			// stages.<stage>.claude_account <name>` — and runs `fugaro update`
			// again, which is the path the refusal's message leaves open rather
			// than a splice this re-seed could have made.
			hand = append(hand, member)
		}
	}
	return out, written, hand
}

// applyPolicyReseed is the accepted offer: the file is backed up and written
// with what the re-seed could splice. A member it could not write is refused
// rather than skipped — a re-seed that recorded a generation while leaving a
// member behind would let the next publish overwrite the newer node with the
// older file, which is the whole of what D854 refuses.
//
// The generation this file is now written from is the caller's to record and
// not this function's: the caller is the one that knows whether the node is
// still going to be written afterwards in the same run.
func applyPolicyReseed(cmd *cobra.Command, path string, lc *localcfg.Config, p *projectpolicy.Policy, drift []policyDrift) ([]string, error) {
	members := make([]string, 0, len(drift))
	for _, d := range drift {
		members = append(members, d.Member)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, userErr("re-seeding %s from the published policy node: %v", path, err)
	}
	out, written, hand := reseedPolicyKeys(old, lc, p, members)
	if len(hand) > 0 {
		return nil, userErr("the published policy node's %s cannot be re-seeded into %s by one edit, so the file was left as it is: put the published values there by hand, then run %s again",
			strings.Join(hand, ", "), path, selfCommand()+" update")
	}
	if err := writeFileAtomicGuarded(cmd, path, old, out); err != nil {
		return nil, err
	}
	return written, nil
}

// writeFileAtomicGuarded writes out over path with a backup of old beside it,
// and writes nothing at all when the re-seed changed nothing.
func writeFileAtomicGuarded(cmd *cobra.Command, path string, old, out []byte) error {
	if !yamledit.Changed(old, out) {
		return nil
	}
	backup, err := backupFile(path, old, time.Now())
	if err != nil {
		return userErr("backing up %s before re-seeding it: %v", path, err)
	}
	if err := writeFileAtomic(path, out); err != nil {
		return userErr("re-seeding %s from the published policy node: %v", path, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s re-seeded from the published policy node (backup at %s)\n", path, backup)
	return nil
}

// formatFloat is a policy number as the yaml file spells it: no exponent, and
// no trailing zeroes ("5", never "5.000000").
func formatFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
}
