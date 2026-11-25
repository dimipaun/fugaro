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

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
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

// publishedMode is the mode a local config's spelling asks for (AB8a): the word
// the mode's vocabulary dropped (D3009) is no mode of its own, and the answer
// the vocabulary gives for it is the mode it means. Every reader of a file's
// mode on the way to the published node asks it here, so neither the drift
// comparison nor the node a publish builds can put the word back into the node
// the migration has just taken it out of — which is what the update would
// otherwise do to the very node it converged, on every run of it. A live mode,
// and the empty spelling a config that states none carries, come back
// unchanged; the file itself loses the word in AB8b, not here.
func publishedMode(mode string) string {
	if policy.Mode(mode).Removed() {
		return policy.Mode(mode).Effective()
	}
	return mode
}

// policyConfigDrift is the members of the published node the local config is
// the source of and that the node does not carry, in the order a diff reads.
//
// Two members are not the config's: the mobile block and the app's
// crash-report switch beside it, whose source is the publisher's own default
// for a node that carries neither (projectpolicy.MobileForPublish and
// MobileCrashReportsForPublish, O9), and the rows are there for the same reason
// the config's own are — a publish would write a member the node does not
// carry, and a stage that could not see it would leave the node at the release
// behind for ever. They are the only members of the list a config cannot state.
//
// The comparison is the JSON both sides spell, so a member the node omits and a
// config that states nothing for it are one value (the carrier's omitempty is
// what drops an empty list on the way in). Only the members a config edit has to
// reach a run through are compared: allow_repos is the node's own (no command of
// this release writes it from the local config), and `providers` reaches a run
// through its own job's copy, not through this node (F9), so a config whose only
// difference is its providers: block is no reason to rewrite what every run of
// the project is admitted by. The credentials block is not in that list any
// more: this release's local config carries it (credential_cascade), so it is
// compared below through the publisher's own merge (D2607).
//
// The file's mode is compared as the mode it asks for (publishedMode), which is
// AB8a's rule and the reason the comparison is not the two spellings.
func policyConfigDrift(lc *localcfg.Config, p *projectpolicy.Policy, identities map[string]string) []policyDrift {
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
	// The file's own mode is asked through the vocabulary (AB8a): the word the
	// mode's vocabulary dropped (D3009) is no mode of its own — a file that
	// spells it asks for the mode it means, which is the mode the migration has
	// just written into the published node — so a row comparing the two
	// spellings would report this stage's own rewrite as a difference, and the
	// publish that followed would put the word back into the node the step
	// exists to take it out of. The node's side is its own bytes: a node that
	// carries no mode at all is a member this file states and the node does not,
	// which is what the settings read marks unpublished. The file itself loses
	// the word in AB8b, not here.
	add("budget.mode", publishedMode(lc.Budget.Mode), p.Budget.Mode, true)
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
		cat := lc.ImageCatalogEffective(nil)
		add("image_catalog", cat, p.ImageCatalog, false)
		// The services the entries declare are derived from that same catalog
		// (D1904) and travel as a key of their own (F20), so a config that adds
		// or removes one is a difference this comparison has to see: comparing
		// the catalog alone would leave an entry's services on the node exactly
		// as they were, and the run would start what the operator just removed.
		add("image_services", projectpolicy.ImageServicesFromCatalog(cat), p.ImageServices, false)
	}
	// The repo-to-image map is compared the same way and for the same reason
	// (D1906): it is where a run's own repository is looked up, so a config that
	// maps a repository and a node that carries no map is exactly the gap this
	// stage exists to close — every run of that repository would take the
	// catalog's default image instead of the one the owner mapped it to. A
	// config silent about the map keeps the node's, and a written empty one
	// differs from a node that still maps something, which is the removal an
	// operator has to see.
	if lc.ImageMap != nil {
		// Both sides are the block's own type, so what is compared is the JSON
		// the node is written with — the escaped keys — and not the config
		// spelling of a name against the node's (ImageMap).
		add("image_map", projectpolicy.ImageMap(lc.ImageMapEffective(nil)), p.ImageMap, false)
	}
	// The bake switch reaches a run off this node too (D1901), so a config that
	// states it where the node states the other answer is a difference a publish
	// closes. It is compared by the publisher's own keep rule, which is the
	// orchestrator line's reason: the node's value is handed to the rule, so a
	// config that writes nothing is not reported as a difference — nil would
	// read as a difference against a node that states either answer — while a
	// written false against a node that says nothing (or true) is exactly the
	// edit this stage exists to carry.
	if lc.BakeRepos != nil {
		add("bake_repos", lc.BakeReposEffective(p.BakeRepos), p.BakeRepos, false)
	}
	// The clone URLs reach a run off this node and nowhere else — a job's
	// environment carries none of them — and the block's source is this file
	// since SD5 (D842), so a config that states one where the node carries
	// another is exactly the gap this stage exists to close: without the
	// comparison, `fugaro project clone-url set` names `fugaro update` and the
	// update publishes nothing. It is compared by the publisher's own keep rule
	// (CloneURLsEffective), so a config that writes no block is not reported —
	// nil would read as a difference against every node that carries one, and
	// the block is then deliberately the node's own (D844) — while a written
	// empty block against a node that carries entries is the removal an
	// operator has to see.
	if lc.CloneURLs != nil {
		add("clone_urls", projectpolicy.CloneURLs(lc.CloneURLsEffective(nil)), p.CloneURLs, false)
	}
	// The binaries block reaches a run off this node and nowhere else (D981,
	// D983) — no job's environment carries it — and the local config is its
	// carrier from this release on (AR1, D987), so a config that states one
	// where the node carries another is exactly the gap this stage exists to
	// close: without the comparison, the operator's own `binaries:` edit would
	// reach no run until they ran the explicit publish, which is the second
	// command the HARD RULE forbids. It is compared by the publisher's own keep
	// rule (BinariesEffective), the clone_urls line's reason: a config that
	// writes no block is not reported — nil would read as a difference against
	// every node that carries a block, and the block is then deliberately the
	// node's own (D987) — while a written block, the empty one that clears the
	// list included, is the operator's edit and is reported like any other.
	if lc.Binaries != nil {
		add("binaries", lc.BinariesEffective(p.Binaries), p.Binaries, false)
	}
	// The designated repository reaches a run off this node and nowhere else
	// (D2750, D2751; periodic-reviews R14), and the local config is its carrier,
	// the binaries block's line word for word: without the comparison the
	// operator's own `reports_commit:` edit would reach no run until they ran the
	// explicit publish, which is the second command the HARD RULE forbids. It is
	// compared by the publisher's own keep rule (ReportsCommitEffective), so a
	// config that writes no block is not reported — nil would read as a
	// difference against every node that carries a designation, and the block is
	// then deliberately the node's own (D987) — while a written block, the empty
	// one that takes the designation away included, is the operator's edit and is
	// reported like any other.
	if lc.ReportsCommit != nil {
		add("reports_commit", lc.ReportsCommitEffective(p.ReportsCommit), p.ReportsCommit, false)
	}
	// The crafts, their credentials and their pools reach a run off this node
	// and nowhere else (docs/model-crafts §1–3), and the local config is their
	// carrier from this release on (MC18, D1667), so a config that states one
	// where the node carries another is exactly the gap this stage exists to
	// close: without the comparison the operator's own `credentials:` or
	// `crafts:` edit would reach no run until they ran the explicit publish,
	// which is the second command the HARD RULE forbids.
	//
	// Each row is compared through the publisher's own merge — the one
	// buildProjectPolicy writes the node with — and not by a rule of its own
	// here, the clone_urls line's reason: a config that states nothing is then
	// not reported as a difference (the merge hands the node's own member back,
	// so the two sides are one document and the row is silent), while a written
	// block is the operator's edit and is reported like any other. A pool the
	// config states and the node cannot carry is not a difference either: the
	// publish leaves it out and names it (D2608), so reporting it would send the
	// operator to a command that cannot close the gap it fired on — the rule the
	// accounts entry above follows.
	craftCreds := publishedCraftCredentials(lc, p)
	add("craft_credentials", craftCreds, p.CraftCredentials, false)
	craftPools, _ := publishedCraftPools(lc, p, craftCreds)
	add("craft_pools", craftPools, p.CraftPools, false)
	add("crafts", publishedCrafts(lc, p), p.Crafts, false)
	// The cascade and the model grades are the same case one block over
	// (D2607, F5, F6): the local config is their carrier from this release on,
	// so a config that states a member the node does not carry is exactly the
	// gap this stage exists to close — without the comparison the operator's
	// own `credential_cascade:` or `model_grades:` edit would reach no run
	// until they ran the explicit publish, which is the second command the HARD
	// RULE forbids. Each is compared through the publisher's own merge, the
	// craft rows' reason: a config that states nothing is not reported as a
	// difference (the merge hands the node's own member back, so the two sides
	// are one document and the row is silent), while a written block is the
	// operator's edit and is reported like any other.
	if lc.CredentialCascade != nil {
		add("credentials", publishedCredentials(lc, p.Credentials), p.Credentials, false)
	}
	if lc.ModelGrades != nil {
		add("model_grades", publishedModelGrades(lc, p.ModelGrades), p.ModelGrades, false)
	}
	// The triage switch reaches a run off this node too (D1854), so a config
	// that states it where the node states the other answer is a difference a
	// publish closes, and it is compared the bake switch's way: the node's value
	// is handed to the publisher's keep rule, so a config that writes nothing is
	// not reported as a difference — nil would read as one against a node that
	// states either answer — while a written false against a node that says
	// nothing (or true) is exactly the edit this stage exists to carry.
	if lc.Triage != nil {
		add("triage", lc.TriageEffective(p.Triage), p.Triage, false)
	}
	// The review-against-task switch reaches a run off this node too (R7 of
	// docs/review-against-task/), so a config that states it where the node
	// states the other answer is a difference a publish closes, and it is
	// compared the triage switch's way: the node's value is handed to the
	// publisher's keep rule, so a config that writes nothing is not reported as
	// a difference — nil would read as one against a node that states either
	// answer — while a written false, the project-wide default (D2935), against
	// a node that says nothing (or true) is exactly the edit this stage exists
	// to carry.
	if lc.ReviewAgainstTask != nil {
		add("review_against_task", lc.ReviewAgainstTaskEffective(p.ReviewAgainstTask), p.ReviewAgainstTask, false)
	}
	// The member's switch for the personal half of a workspace's credentials
	// reaches the workspace's service off this node and nowhere else (R9 of
	// docs/workspace-credentials/, WC6, D2972) — the service reads the member's
	// PUBLISHED policy, never anything on the member's disk — so a config that
	// states it where the node states the other answer is a difference a publish
	// closes, and it is compared the triage switch's way: the node's value is
	// handed to the publisher's keep rule, so a config that writes nothing is
	// not reported as a difference — nil would read as one against a node that
	// states either answer — while a written true against a node that says
	// nothing (or false) is exactly the edit this stage exists to carry. Without
	// the row the member's admin's own act would reach no run: `settings set`
	// writes the local config, and `fugaro update` — the one command that brings
	// an installation forward — is where the node learns it.
	if lc.PersonalWorkspaceTokens != nil {
		add("personal_workspace_tokens", lc.PersonalWorkspaceTokensEffective(p.PersonalWorkspaceTokens), p.PersonalWorkspaceTokens, false)
	}
	// The pools' subscription switch reaches a run off this node too (F22,
	// D1636), so a config that states it where the node states the other answer
	// is a difference a publish closes, and it is compared the triage switch's
	// way: the node's value is handed to the publisher's keep rule, so a config
	// that writes nothing is not reported as a difference — nil would read as
	// one against a node that states either answer — while a written true
	// against a node that says nothing (or false) is exactly the edit this
	// stage exists to carry. Without the row the owner's own act would reach no
	// run: `settings set subscription_spread true` writes the local config, and
	// `fugaro update` — the one command that brings an installation forward —
	// is where the node learns it.
	if lc.SubscriptionSpread != nil {
		add("subscription_spread", lc.SubscriptionSpreadEffective(p.SubscriptionSpread), p.SubscriptionSpread, false)
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
	accounts, err := projectpolicy.DeriveAccounts(profiles, lc.ClaudeAccounts, lc.ProjectSecrets, lc.PolicyRepoAccounts())
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
		np, ok := p.Profiles[name]
		if !ok {
			// The NAME is a difference a publish closes, and the only one in
			// the block this comparison reports: a config that defines a
			// profile the node does not carry is a profile nothing resolves —
			// a workflow that names it, and the default of a provider (R8) —
			// and `fugaro update` writing the default profile of a provider a
			// project uses is exactly that config (S5, R1, D2310): the node of
			// an installation that never carried one learns it here, in the
			// same run, or the shared job the installation stage deploys is a
			// job no policy names. The contents are still not compared, for
			// the reason above them: a publish that fired on those would
			// rewrite the node on every run without closing the gap it fired
			// on.
			out = append(out, policyDrift{Member: "profiles." + name, File: jsonText(profiles[name])})
			continue
		}
		add("profiles."+name+".claude_accounts", profiles[name].ClaudeAccounts, np.ClaudeAccounts, false)
	}
	// The account-choice keys are compared too — the project's
	// default_claude_account and each stage's own claude_account — because
	// this file is their only store and the publish is what carries them
	// (docs/mixed-credentials/spec.md §11): the runner reads them off the
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
	//
	// A stage the release behind cannot read is not one of these members on the
	// file's side (config.AccountStageHeldBack, the publisher's own rule): the entry
	// stays in the local config and the publisher writes nothing for it, so
	// reporting it here would be a difference no publish closes — the operator
	// would be sent to a command that cannot close the gap it fired on. The
	// node's side is compared as it is: a node that carries such an entry is
	// still a difference, and the publish takes it off.
	fileAccounts, nodeAccounts := configAccountValues(lc), policyAccountValues(p)
	for _, member := range accountChoiceNames(fileAccounts, nodeAccounts) {
		add(member, fileAccounts[member], nodeAccounts[member], false)
	}
	// The Bitbucket token's own block is derived from the local config as the
	// typed family below it is, and it is the block the credentials service's
	// proxy is admitted by (G3): whether a repository's git credential is
	// brokered instead of mounted is the published node's answer alone, so a
	// config that forwards while the node carries no block — or one that carries
	// a block the config does not state — is a project whose runs and whose
	// operator disagree about where the credential comes from.
	//
	// The row is what makes `fugaro update` turn the proxy on for a project at
	// the previous version (S6, D2306). The switch is written in the local config
	// by that same step (update_proxy_switch.go), and the release that writes it
	// does NOT move the node's version: a node already at this version has no
	// migration step that would carry the block, so without this comparison the
	// update would leave the file stating a switch the node does not carry — and
	// resolveBitbucketProxied reads exactly that pair, refusing the run of every
	// repository of the project until an operator repaired it by hand.
	//
	// It is compared by the derivation rather than by the entry as written, as
	// the typed family is and for the same reason: the node spells allow_repos
	// provider-qualified and the file's allow_data_to does not, so comparing the
	// file's own form would report a difference on every run. The two states
	// DeriveBitbucketToken answers nil for — no entry at all, and one whose list
	// is empty — are the two that publish no block (D493: "shared with nobody"),
	// and a node that carries one is then a difference this stage closes:
	// removing the block is how an operator takes the credential away, and a node
	// left proxying for a repository the config no longer names is a run that
	// fetches its git credential from a service holding nothing for it (D143).
	// A derivation the config cannot state is NOT reported here, and that is the
	// one place this row parts company with the typed family's: DeriveBitbucketToken
	// is reached through this comparison for every project, entry or no entry,
	// while the migration path of this stage discards its refusal for a node
	// already at this version (a config only a publish would refuse is not the
	// migration's to fail over: TestUpdateCurrentNodeWithUnderivableBitbucketDoesNotFail).
	// A member reported here would send that config to a publish that refuses it,
	// so a run that used to converge would stop needing a second command — and the
	// operator's entry is named by the publish that does refuse it, which is where
	// they meet it (S6's flip warns and holds itself back rather than publishing
	// one; update_proxy_switch.go's bitbucketProxyBlock).
	//
	// The row is reported only where this machine's config STATES the entry, and
	// that gate is load-bearing (S6's fix, 2026-11-18): an absent entry is no
	// statement about the switch at all, while the derivation answers nil for it
	// exactly as it does for an entry whose list is empty — so a machine that
	// holds no entry (a second operator's config, a config whose secrets live
	// elsewhere, one whose entry an operator removed) would publish the node
	// WITHOUT bitbucket_token and turn the whole project's proxy off, stranding
	// the proxied runs in flight over a file that never mentioned the switch.
	// An entry that is there and whose list is empty is the takedown above and
	// still reports: that is the operator's own act, and the node has to follow
	// it.
	if _, stated := lc.ProjectSecrets[config.BitbucketSecret]; stated {
		if token, err := bitbucketProxyBlock(lc); err == nil {
			add(bitbucketTokenKey, token, p.BitbucketToken, false)
		}
	}
	// The typed Bitbucket tokens are DERIVED from the local config, like the
	// accounts block and for a stronger reason: the node is the only place the
	// credentials service learns what each token reaches (R5), and this key is
	// new with a release that does not move the node's version — so an
	// installation whose node is already at this version has NO migration step
	// that would carry it, and this comparison is the whole of what makes
	// `fugaro update` publish it. Without the row the operator's own records
	// reach no run, and the one command the upgrade is would need a second one.
	//
	// It is compared by the derivation and not by the records as written, which
	// is the accounts rule above: the node's spelling of a coverage entry is
	// provider-qualified and the file's is not, so comparing the file's own
	// form would report a difference on every run — a publish on every update,
	// forever. A derivation the config cannot state is reported as the member
	// alone (the accounts case), and the publisher's own refusal names the
	// entry to fix.
	//
	// A config that names no record derives none, and a node that carries the
	// key is then a difference this stage closes: removing the block is how an
	// operator takes the tokens away, and it is a removal they have to see.
	tokens, err := projectpolicy.DeriveBitbucketTokens(lc.BitbucketTokens, identities)
	if err != nil {
		out = append(out, policyDrift{Member: projectpolicy.BitbucketTokensKey})
		return out
	}
	add(projectpolicy.BitbucketTokensKey, tokens, p.BitbucketTokens, false)
	// The mobile block is one of the two members of this comparison whose source
	// is neither this machine's config nor the node: a publish writes the node's
	// own block when it carries one and O9's defaults when it does not
	// (projectpolicy.MobileForPublish), so the row is silent for a node that
	// carries a block — the switches are the project's own answers and this
	// machine has no opinion about them — and fires for a node published before
	// the key existed, which is the node an installation at the release behind
	// holds.
	//
	// It is the typed Bitbucket tokens' argument and the whole of what makes
	// O9's "on in policy, answering only after `update` publishes it" true: the
	// key is new with a release that does not move the node's version, so an
	// installation whose node is already at this version has NO migration step
	// that would carry it, and without this row the stage would find nothing to
	// do about a node that is current in every other member — the pairing route
	// would answer `mobile_disabled` for every installation that ran the update,
	// and the block would reach a node only where an operator had written one by
	// hand. It is compared through the publisher's own function rather than by a
	// rule of its own here, so the node this stage decides on and the node the
	// publish writes cannot disagree (F40, the plan's global constraint).
	add("mobile", projectpolicy.MobileForPublish(p.Mobile), p.Mobile, false)
	// The app's crash-report switch is the row above's twin and it is a key of
	// its own, beside the block and never inside it (policy.go's
	// Policy.MobileCrashReports): a member added inside the block would be
	// refused by every reader of the release behind — the block decodes strictly
	// — which is exactly what update promises never to do while runs are in
	// flight. So the switch travels as a tolerated top-level key, and this row
	// is what carries it: the node's own answer where it carries one, and the
	// publish's default otherwise (MobileCrashReportsForPublish), so a node
	// published before the key existed is a difference this stage reports and
	// publishes once, and the run after finds nothing.
	add(projectpolicy.MobileCrashReportsKey, projectpolicy.MobileCrashReportsForPublish(p.MobileCrashReports), p.MobileCrashReports, false)
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
// sentence show an operator. A stage the release behind cannot read is left out
// as well, because the publish states nothing for it either
// (config.AccountStageHeldBack): the member this function reports is the one a
// publish would write, and a stage held back is not.
//
// A key may hold an alias (D2091), and the value reported is the account it
// resolves to, so that the diff, the widening sentence and the node the publish
// writes all speak the account rather than a name no job image knows (F4,
// D2090). This is the same resolution project_cmd.go copies the members with: a
// name that is no alias is reported unchanged.
func configAccountValues(lc *localcfg.Config) map[string]string {
	out := map[string]string{}
	if lc.DefaultClaudeAccount != "" {
		account, _ := lc.ResolveAccount(lc.DefaultClaudeAccount)
		out["default_claude_account"] = account
	}
	for stage, choice := range lc.ClaudeAccountStages {
		if choice.ClaudeAccount == "" || config.AccountStageHeldBack(stage) {
			continue
		}
		account, _ := lc.ResolveAccount(choice.ClaudeAccount)
		out[stageAccountMember(stage)] = account
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

// splitAccountChoices separates a re-seed's unwritten members into the
// account-choice keys and everything else, in the order they came. The two take
// different remedies in the refusal (applyPolicyReseed), so the split happens
// where the sentence is built rather than in the members' own shape: a member
// that is an account choice is recognised by name and prefix, which is the same
// spelling stageAccountMember and the published node use.
func splitAccountChoices(hand []string) (accounts, rest []string) {
	for _, member := range hand {
		if member == "default_claude_account" || strings.HasPrefix(member, "claude_account_stages.") {
			accounts = append(accounts, member)
			continue
		}
		rest = append(rest, member)
	}
	return accounts, rest
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
// because the path the run selected is not handed down to the publish — the
// engine that reaches it holds the run and its flags, and the function below
// reaches the publish without them (policyMigrationFor). $FUGARO_CONFIG wins
// when it holds this project, the
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
// The gate is true from 0.26.0, the release that first writes and reads the
// member, so the branch below is the record of what the release behind met
// rather than something an operator meets today: a 0.25.0 installation's
// `fugaro update` refuses to publish this node, and this release's publishes
// it (the pair is one fact with internal/infra's imageContextShipped, and the
// release's notes say which one carries it).
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

// policyImageTokensRefusal refuses the node about to be published when it
// prices a model's images (model_prices' image_tokens, V2, D2001) and the
// installation's base image predates visionSince. The member is not optional
// the way the price overrides around it are: every runner of the project
// decodes the published node strictly, so a node carrying a member they do not
// know is refused whole and every run of the project fails until someone
// removes it — the same failure policyContextRefusal keeps out of a node, and
// the reason this is a refusal rather than the warning warnOldImages prints
// about a workflow whose build record is older than layeredSince.
//
// The node is judged and not the local config, for policyContextRefusal's
// reason: the node is what the older runner reads, and a config silent about
// model_prices keeps the published node's, so the member can reach these bytes
// from either side. Both doors build the node through policyNodeForPublish,
// which is why the check is here.
//
// ref is the installation's base image (localcfg.Config.BaseImage) and is
// passed rather than read so a test can ask both halves of the gate, the way
// the skew gates' callers do. Its states are read as the skew gates read them,
// with one difference: a base image that cannot be dated (a dev build, a
// hand-pushed one, an --image-source mirror) passes in silence rather than
// warning, because there is no writer here to warn on — the publish door is
// inside the engine's own stage as often as it is a command's.
//
// The judge is the base image and not each workflow's build record, which is
// what warnOldImages can walk: the policy node is read by every job of the
// project, and the base image is the one image this door can date without a
// read per workflow. What it dates is the installation that publishes, which is
// not every reader of the node: a repository of the project whose job image was
// built from an older base has no launch gate on this member either — the
// vision slot's gates fire on agent.models.vision being written, and a
// repository can price images without ever naming a slot — so that launch runs
// and its runner refuses the node whole at bootstrap, and fugaro image refresh
// in that checkout is what closes it.
func policyImageTokensRefusal(p *projectpolicy.Policy, ref string) error {
	if p == nil {
		return nil
	}
	var priced []string
	for _, id := range slices.Sorted(maps.Keys(p.Prices)) {
		if pr := p.Prices[id]; pr.ImageTokens != nil {
			priced = append(priced, id)
		}
	}
	if len(priced) == 0 {
		return nil
	}
	rel, ok := releaseOf(ref)
	if !ok || !imagePredates(rel, visionSince) {
		return nil
	}
	return userErr("the node about to be published prices model %s's images (model_prices' image_tokens), and this installation's base image predates the release whose runners read that member: fugaro %s or later is what decodes it, and %s is release %s. Every runner of the project decodes the node strictly, so a node carrying the member is refused whole and every run of the project fails on it until someone removes it. Run fugaro update in your own terminal window to move the installation's base image to a current release, then publish the node again",
		strings.Join(priced, ", "), visionSince, pluginwire.Printable(ref), rel)
}

// policyNodeForPublish is the one build both doors take: the node the operator's
// config states, on top of the node it replaces, with the allow-list seeded or
// kept as the writer's rule has it. `fugaro project policy publish` and the
// policy stage call it, so the bytes they write from one local config cannot
// differ; TestUpdateStageAndTheExplicitDoorBuildTheSameNode is the guard.
func policyNodeForPublish(ctx context.Context, lc *localcfg.Config, old *projectpolicy.Policy, base []byte, exists, seed, removeRouting bool, identities map[string]string) ([]byte, *projectpolicy.Policy, map[string]string, error) {
	allow, sources, err := publishAllowRepos(ctx, lc, old, seed)
	if err != nil {
		return nil, nil, nil, err
	}
	pol, keepRouting, err := buildProjectPolicy(lc, allow, old, removeRouting, identities)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := policyContextRefusal(pol, imageContextShipped); err != nil {
		return nil, nil, nil, err
	}
	if err := policyImageTokensRefusal(pol, lc.BaseImage(config.BaseKind)); err != nil {
		return nil, nil, nil, err
	}
	body, err := policyNodeBytes(pol, base, exists, keepRouting, lc.CloneURLs == nil, lc.Binaries == nil)
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

// orchestratorEffectiveValues is the orchestrator block as this machine reads it
// for the project, spelled as the settings keys above so that the one comparison
// (settingsLoosens) measures it too.
//
// It is the daemon's own reading without the node
// (orchestrator_run.go's orchestratorBlock): the file's block where it states
// one, Fugaro's guardrails filling what it leaves unset, and the person's own
// max_parallel filling the running cap they did not take. The node is left out
// because the write that reads this is a command editing a file offline, which
// is the residual settingsEffectiveCeiling documents for the node's other
// members: where the file states nothing and a node states less than the
// guardrail, this is the higher of the two, and a write landing between them
// reads as a tightening though the daemon would be raised by it. Reading the
// guardrail is still what closes the hole that matters — a file stating its
// first rate is measured against the ceiling Fugaro ships and not against
// nothing — and the node itself the read refuses to dial for a file edit is not
// something a wider base could answer.
//
// It is a second spelling of a block policyCeilingValues already spells because
// the two ask the same question of different layers: that one compares a node
// about to replace another, and this one what a run gets from the file. The
// block is the one family whose layers do not combine by the tighter-wins rule —
// localcfg.OrchestratorEffective REPLACES the published block wholesale when the
// file states any key of it — so a file that starts stating a rate raises what a
// run gets whatever the node is pinned at, and a write into the file is measured
// against the file's own block rather than against a clamp the node applies.
func orchestratorEffectiveValues(lc *localcfg.Config) map[string]string {
	blk := policy.Orchestrator{}
	maxParallel := int64(0)
	if lc != nil {
		if eff := lc.OrchestratorEffective(nil); eff != nil {
			blk = *eff
		}
		maxParallel = int64(lc.MaxParallel)
	}
	filled := blk.WithDefaults(maxParallel)
	return policyCeilingValues(&projectpolicy.Policy{Orchestrator: &filled})
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
// (docs/mixed-credentials/spec.md §11), so a node that starts naming one hands
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

// policyCraftWidening is the grants this publish would ADD or REWRITE where the
// node it replaces carries them not at all or carries another value: a
// credential the node does not name, a credential whose type changes, a pool the
// node does not name or one whose members, strategy, owner or users change, a
// craft's access, a member of the cascade (its fallback, its threshold, its
// backoff and D142's two controls), and a model grade's access.
//
// They are the node's spending and egress grant (F12/D1628, D2617): a credential
// is a token a run's calls go out on, a pool is the set of them a craft may be
// served by, a craft's access is which of the two that craft's calls use, the
// cascade is where a run's calls fall over when one is exhausted, and a grade's
// access is what the ladder's rung spends. So a publish that adds one hands
// every run of the project a credential it did not hold, and one that repoints
// an access moves a craft's calls onto another credential. That is the
// account-choice members' own class rather than a ceiling settingsLoosens reads
// (a credential and a pool are no member of policyCeilingMembers), so their
// comparison is asked beside it, and this release's publish — which writes the
// blocks from the installation's config (MC18, N4) — is what makes them a change
// this node can meet at all.
//
// A member this publish REMOVES is not a widening, exactly as the account
// members' removal is not: it takes a grant away from the node. Nor is a craft's
// MODEL, which decides what a stage runs and not whose credential it spends, so
// only the access half of the craft block is measured.
//
// Nor is the member MC4's conversion derives where the node holds none
// (craftGrantDerived): the credential an account of the installation's own keys
// is written down as, and the craft key naming it. The account is the
// installation's already — its token, its allow_data_to, and the stage keys that
// reach it — and the craft key is the account that craft's stages already run
// on, which is F17's whole contract ("a run before and after resolves the same
// model and credential"). Counting it here would make the migration a step the
// owner has to answer for by name, and the first `fugaro update` after this
// release the one that stops for it: the HARD RULE's own loss, on an
// installation whose config predates these keys and which has nothing to do but
// run the command.
func policyCraftWidening(old, new *projectpolicy.Policy, lc *localcfg.Config) []policyCeiling {
	was, now := policyCraftGrantValues(old), policyCraftGrantValues(new)
	derived := craftDerivedAccesses(lc)
	names := map[string]bool{}
	for _, side := range []map[string]string{was, now} {
		for name := range side {
			names[name] = true
		}
	}
	var out []policyCeiling
	for _, member := range slices.Sorted(maps.Keys(names)) {
		after := now[member]
		// A member the node states nothing for is the grant this publish
		// REMOVES, skipped above; one the two state alike is no change at all.
		if after == "" || after == was[member] {
			continue
		}
		// Only a member the node does NOT hold can be the migration's: a value
		// it does hold and this publish rewrites is the operator's own, whatever
		// it names.
		if was[member] == "" && craftGrantDerived(lc, derived, member, after) {
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

// craftGrantDerived is whether a craft member is the one MC4's conversion
// derives for an installation whose config predates the craft keys
// (init_craft_migrate.go): a credential of type claude-oauth under the name of an
// account the installation's own keys declare — the account written down as the
// typed credential this release reads (spec §1) — and a craft access that is
// exactly the one the conversion derives for that craft (craftDerivedAccesses).
//
// The names are read off the config's own account keys (lc.ClaudeAccounts) and
// never off the node's accounts block: that block is derived from the profiles
// and the repositories that name an account (projectpolicy.DeriveAccounts), so
// an installation whose stages name an account nothing else does holds none of
// it. Every name the conversion writes is one of the config's own, the alias a
// stage key may carry resolved to the account it stands for first (D2092).
//
// The access half is asked per craft and not by the account it names: a craft
// moved onto ANOTHER account the installation declares is an access the
// conversion does not derive — that craft's stages run on the account they name
// today, and the derived key is that account — so it is the widening this
// comparison exists to find. It is the same change the account keys make, which
// policyAccountWidening already catches, and the runner reads the node's crafts
// for the layers a craft's tooling reaches (runner/poolchoice.go): a craft
// repointed through the craft keys alone must not be the one spelling of it that
// no gate reads.
func craftGrantDerived(lc *localcfg.Config, derived map[string]string, member, value string) bool {
	if lc == nil || value == "" {
		return false
	}
	if name, ok := strings.CutPrefix(member, "craft_credentials "); ok {
		return value == config.CredentialTypeClaudeOAuth && craftAccountNamed(lc, name)
	}
	if craft, ok := strings.CutPrefix(member, "crafts "); ok {
		craft, ok = strings.CutSuffix(craft, ".access")
		return ok && derived[craft] == value
	}
	return false
}

// craftDerivedAccesses is the access MC4's conversion derives for each craft of
// this config, by craft name (deriveCraftAccesses).
//
// It is asked of a clone whose crafts block is emptied rather than of the config
// itself: the conversion leaves a craft the owner has already written an access
// for exactly as it is (its own guard), so a config that carries one would hand
// back the operator's own value as the conversion's and exempt it. The
// credentials are cloned because the derivation appends the entries it derives
// to the slice it is handed (deriveCraftCredentials); the crafts block is the
// clone's own copy and the derivation's only other write.
func craftDerivedAccesses(lc *localcfg.Config) map[string]string {
	if lc == nil {
		return nil
	}
	clone := *lc
	clone.Crafts = config.Crafts{}
	clone.Credentials = slices.Clone(lc.Credentials)
	deriveCraftAccesses(&clone, deriveCraftCredentials(&clone))
	out := map[string]string{}
	for _, craft := range config.CraftNames {
		if access := clone.Crafts.Of(craft).Access; access != "" {
			out[craft] = access
		}
	}
	return out
}

// craftAccountNamed is whether the installation's own keys declare an account
// under a name.
func craftAccountNamed(lc *localcfg.Config, name string) bool {
	_, ok := lc.ClaudeAccounts[name]
	return ok
}

// policyCraftGrantValues is a node's grants keyed and spelled the way the
// publish diff spells them — the very lines whose change the operator reads
// there — so a sentence naming one names the text the diff above it shows. The
// blocks are the diff's own: a credential by its type, a pool by the members and
// the answers that decide who it is served to, a craft by its access alone, and
// the two the reconcile carries beside them — the cascade, member by member
// (credentialsGrantLines), and each grade's access half (gradeGrantLines).
func policyCraftGrantValues(p *projectpolicy.Policy) map[string]string {
	out := map[string]string{}
	if p == nil {
		return out
	}
	for _, lines := range []map[string]string{
		craftCredentialLines(p), craftPoolLines(p), craftAccessLines(p),
		credentialsGrantLines(p), gradeGrantLines(p),
	} {
		maps.Copy(out, lines)
	}
	return out
}

// credentialsGrantLines is the cascade's members as grants, keyed the way the
// diff spells them (credentialsText) and one key per member so a sentence names
// the member that moved rather than the block.
//
// The cascade is a grant because it decides how a run's calls go out when a
// credential is exhausted (D152, D153): `fallback` is whether a run may spend
// the project's money at all — `auto` is openrouter when the run holds no
// Anthropic key (D1b) — and D142's two controls decide how much of it a window
// admits. A member this publish removes is left out here and is not a widening
// (policyCraftWidening's own rule): it takes the statement away.
func credentialsGrantLines(p *projectpolicy.Policy) map[string]string {
	out := map[string]string{}
	if p == nil || p.Credentials == nil {
		return out
	}
	c := p.Credentials
	if c.Fallback != "" {
		out["credentials fallback"] = c.Fallback
	}
	if c.WaitThreshold != "" {
		out["credentials wait_threshold"] = c.WaitThreshold
	}
	if c.Backoff != nil {
		if c.Backoff.Initial != "" {
			out["credentials backoff.initial"] = c.Backoff.Initial
		}
		if c.Backoff.Cap != "" {
			out["credentials backoff.cap"] = c.Backoff.Cap
		}
	}
	if c.StopAfterExhaustionPerWindow != nil {
		out["credentials stop_after_exhaustion_per_window"] = strconv.FormatBool(*c.StopAfterExhaustionPerWindow)
	}
	if c.ConcurrencyPerToken != nil {
		out["credentials concurrency_per_token"] = strconv.Itoa(*c.ConcurrencyPerToken)
	}
	return out
}

// gradeGrantLines is each model grade's ACCESS half as a grant, and never its
// model: a grade's access is the credential or pool its rung's calls go out on,
// which is the craft access's own grant (F12, D1628), while the model beside it
// decides what the rung runs and not whose credential it spends — the same
// split policyCraftWidening makes for a craft.
func gradeGrantLines(p *projectpolicy.Policy) map[string]string {
	out := map[string]string{}
	if p == nil || p.ModelGrades == nil {
		return out
	}
	for _, name := range config.GradeNames {
		if c := p.ModelGrades.At(name); c != nil && c.Access != "" {
			out["model_grades "+name+".access"] = c.Access
		}
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
// ceiling the operator's config states. The account-choice members and the
// craft grants are asked beside the ceilings (policyAccountWidening,
// policyCraftWidening) rather than through them, since neither a node's account
// nor its credentials, pools and accesses is a member settingsLoosens reads.
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
// It is what makes a first publish the owner's own step: the person's own
// answer rather than one a run carries, and a coding agent's session refused by
// the gate's own D1026 rule
// rather than admitted to the boundary every later run of the project is
// admitted by.
func (m *policyMigration) loosening() []policyCeiling {
	if m.pub == nil {
		return nil
	}
	if m.firstPublish {
		return []policyCeiling{{Member: projectpolicy.PathShared()}}
	}
	// The account keys and the grants are not ceilings, so settingsLoosens has
	// nothing to say about them and their own comparisons are asked beside it:
	// setting one is the same class of decision — who is admitted, and on whose
	// credential — that the gate refuses here, and a credential, a pool, a
	// craft's access, a cascade member or a grade's access this publish adds is
	// the grant itself (policyCraftWidening, D2617).
	out := policyLoosening(m.node, m.pub.pol)
	out = append(out, policyAccountWidening(m.node, m.pub.pol)...)
	return append(out, policyCraftWidening(m.node, m.pub.pol, m.lc)...)
}

// grantOnly is whether the whole of this publish's loosening is grants — the
// members D2617 names, which hand a run a credential, a pool or a model it did
// not hold — and nothing else: no ceiling raised, none removed, the model list
// not widened, and no account-choice member moved.
//
// It is the condition the typed name is waived on for a grant (D1542, D1552,
// F24). Adding the grants to loosening() is what puts them in front of the
// gate, which refuses them in a coding agent's session and on a managed
// project's --yes; what it must not do is put them in front of every
// installation's first `fugaro update` as a typed name, because the update door
// asks nothing for a grant: the diff above the line is what the operator reads,
// and a grant is a widening the owner's own command publishes. The account
// choice beside it is NOT this and keeps the confirmation: it has its own fence
// (D2097's door, and policyAccountWidening's typed name at a real terminal), and
// the alias command's waiver is the narrower accountChoiceOnly. So is a ceiling
// raised in the same publish, which is the separate decision D846 reserves for
// the project's own hand.
//
// A project's FIRST node is never this, whatever it carries, for
// accountChoiceOnly's reason: the node it writes is the whole of the project's
// policy, which is what the first publish's own sentence says and what its Typed
// class is about. So is a run with no node to compare against, which is the same
// case read from the other side.
func (m *policyMigration) grantOnly(loosening []policyCeiling) bool {
	if m.firstPublish || m.pub == nil || len(loosening) == 0 {
		return false
	}
	return len(policyLoosening(m.node, m.pub.pol)) == 0 && len(policyAccountWidening(m.node, m.pub.pol)) == 0
}

// refuseGrantsInAnAgentSession is D2617's fence on this tail: a publish that
// carries a grant is refused in a coding agent's session whatever the gate said.
//
// It is D1552's fence and not a second one — the session is what refuses a
// widening, and not the step's class — extended from the ceilings to the grants
// this release's reconcile publishes (D2617, F24). The gate refuses this case
// itself wherever it stands in for the session, so what reaches here is a step
// the gate admitted; the fence is kept anyway, because a grant a coding agent
// queues in the local config is published by the owner's own next `fugaro
// update` and never by the session that wrote the file. It is asked before the
// diff is printed, so a session that is refused is told why and reads nothing
// it might take for the write.
func (m *policyMigration) refuseGrantsInAnAgentSession(c initflow.Conditions) error {
	if c.Agent == "" || !m.writesNode() || len(m.loosening()) == 0 {
		return nil
	}
	return &initflow.AgentError{Marker: c.Agent}
}

// accountChoiceOnly is whether the whole of this publish's loosening is the
// account-choice members — which account the project's stages go out on — and
// nothing else: no ceiling is raised, none is removed and the model list is not
// widened. It is asked where the typed name is about to be, and its one caller
// is the door whose own command the owner typed at their own terminal (D2097):
// for that door the account choice is the decision it was run to make, while a
// ceiling raised in the same publish is the separate one D846 reserves for the
// project's own hand and keeps its typed name.
//
// A craft grant carried in the same publish is not this either: a credential,
// a pool or an access the publish adds is the spending and egress grant itself
// (policyCraftWidening), and the door's own decision — which account the
// project's stages go out on — does not cover it, so that publish keeps the
// typed name.
//
// A project's FIRST node is never this, whatever it carries: the node it writes
// is the whole of the project's policy, which is what the first publish's own
// sentence says and what its Typed class is about. So is a run with no node to
// compare against, which is the same case read from the other side.
func (m *policyMigration) accountChoiceOnly(loosening []policyCeiling) bool {
	if m.firstPublish || m.pub == nil || len(loosening) == 0 {
		return false
	}
	return len(policyLoosening(m.node, m.pub.pol)) == 0 && len(policyCraftWidening(m.node, m.pub.pol, m.lc)) == 0
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
		return fmt.Sprintf("the project has no published policy node at %s, and this run writes the one every run of the project is admitted by — its allow-list, its ceilings and its catalog are chosen here for the first time. The project's own name is what confirms it, and no flag stands in; run this yourself at your own terminal: %s",
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
	fmt.Fprintf(&b, ". The project's own name is what confirms it, and no flag stands in; run this yourself at your own terminal: %s", selfCommand()+" update")
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
	return policyGenerationRecordOf(w, path, lc, gen, nil)
}

// policyGenerationRecordOf is policyGenerationRecord for a run that has just
// written a policy node, published being the node it wrote: the two edits ride
// on one write of the file, so the backup beside it is one and the file is
// replaced once (clearReconciledRemovals).
func policyGenerationRecordOf(w io.Writer, path string, lc *localcfg.Config, gen string, published *projectpolicy.Policy) error {
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
	out, cleared, err := clearReconciledRemovals(out, lc, published)
	if err != nil {
		return userErr("clearing the removals the published node no longer holds from %s: %v", path, err)
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
	for _, name := range cleared {
		fmt.Fprintf(w, "  node_removals %s cleared: the published node no longer holds it\n", name)
	}
	lc.PolicyGeneration = gen
	return nil
}

// clearReconciledRemovals is this machine's explicit-unset record with the
// names the node it has just published no longer holds taken out of it, in the
// file's own text and in the config this run holds (D2607, F22): a removal is a
// statement about the node and it is spent once the write has landed, so the
// record would otherwise make every publish after this one delete a name the
// node does not have. A record that names nothing left is the key's absence
// (NodeRemovals.IsZero), so the key goes with the last name.
//
// A name the published node STILL holds stays in the record: the write that was
// to delete it did not, and the record is what makes the next publish try
// again. published is nil for a run that published no node — a bare migration
// writes the node it migrated and reconciles nothing — and then nothing is
// cleared, because this run has no answer about any of the names.
func clearReconciledRemovals(src []byte, lc *localcfg.Config, published *projectpolicy.Policy) ([]byte, []string, error) {
	r := removedNames(lc)
	if r.IsZero() || published == nil {
		return src, nil, nil
	}
	creds := map[string]bool{}
	for _, c := range published.CraftCredentials {
		creds[c.Name] = true
	}
	pools := map[string]bool{}
	for _, p := range published.CraftPools {
		pools[p.Name] = true
	}
	crafts := map[string]bool{}
	for _, name := range config.CraftNames {
		if published.Crafts != nil && published.Crafts.Of(name) != (config.Craft{}) {
			crafts[name] = true
		}
	}
	remaining := localcfg.NodeRemovals{}
	var cleared []string
	for _, l := range []struct {
		key, block string
		names      []string
		held       map[string]bool
		keep       *[]string
	}{
		{"craft_credentials", "craft_credentials", r.CraftCredentials, creds, &remaining.CraftCredentials},
		{"pools", "pools", r.Pools, pools, &remaining.Pools},
		{"crafts", "crafts", r.Crafts, crafts, &remaining.Crafts},
	} {
		for _, name := range l.names {
			if l.held[name] {
				*l.keep = append(*l.keep, name)
				continue
			}
			cleared = append(cleared, l.block+" "+name)
		}
	}
	if len(cleared) == 0 {
		return src, nil, nil
	}
	if remaining.IsZero() {
		out, err := yamledit.RemoveKey(src, []string{"node_removals"})
		if err != nil {
			return nil, nil, err
		}
		lc.NodeRemovals = nil
		return out, cleared, nil
	}
	var err error
	out := src
	for _, l := range []struct {
		key   string
		names []string
		held  map[string]bool
	}{
		{"craft_credentials", r.CraftCredentials, creds},
		{"pools", r.Pools, pools},
		{"crafts", r.Crafts, crafts},
	} {
		for _, name := range l.names {
			if l.held[name] {
				continue
			}
			if out, err = yamledit.RemoveListItem(out, []string{"node_removals", l.key}, name); err != nil {
				return nil, nil, err
			}
		}
	}
	lc.NodeRemovals = &remaining
	return out, cleared, nil
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
func recordPublishedGeneration(ctx context.Context, w io.Writer, db *rtdb.Client, lc *localcfg.Config, path string, payload []byte, published *projectpolicy.Policy) error {
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
	return policyGenerationRecordOf(w, path, lc, etag, published)
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
// allow-list admitting nothing takes before its first write: the warning below
// is what a person reads, and what answers for the write is the run's own
// answer to it (askConfirm) — the person at the terminal where --ask is not
// passed, the question where it is. Nothing is typed under it (T7,
// docs/no-prompt-policy). What confirms it is a person and nothing else:
// initflow.PersonAtTheTerminal — a real terminal, no coding agent's marker, and
// none of --ask, --non-interactive and --json — or, where the owner asked for
// the question back, --ask's own answer below. --yes is deliberately not a
// route (no door offers one for this step): the flag says "the ordinary steps",
// and writing a node that stops every run of the project is not one. So a
// session that can neither read the consequence nor be asked — a script's, a
// cron entry's, a coding agent's, or --non-interactive or --json at a real
// terminal — is refused, and an empty allow-list is never written on the
// operator's behalf by something that cannot have read it (#1674). The same
// wording is both the note and the refusal, so the two cannot drift.
func confirmEmptyAllowList(cmd *cobra.Command, lc *localcfg.Config, c initflow.Conditions) error {
	why := policyEmptyAllowListWarning()
	// A coding agent's session is refused before anything else, and
	// --non-interactive and --json with it: a terminal is not the question, the
	// person reading the consequence is, and a flag that says nobody is reading
	// (or an agent that can give itself a pty) must not stand in for one. Where
	// the command has --ask, that puts the question back, and askConfirm below
	// owns its refusal for a session that cannot answer it. Without this, a
	// script or a cron entry publishing a config that lists no repository would
	// write the node unasked, and every run of the project would stop from that
	// moment (#1674).
	if !c.Ask && !initflow.PersonAtTheTerminal(c) {
		return userErr("%s: %s", initflow.NeedsTerminal("the confirmation of this step"), why)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "⚠ CONFIRM (project %s): %s\n", safetext.Field(lc.Name), why)
	ok, err := askConfirm(cmd, "write an allow-list that admits no repository?")
	if err != nil {
		return err
	}
	if !ok {
		return userErr("%s", why)
	}
	return nil
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
		case "mobile", projectpolicy.MobileCrashReportsKey:
			// The block and the crash-report switch beside it are the node's own
			// and no config key carries either: the publish writes the node's
			// block back (MobileForPublish) and the switch's value back
			// (MobileCrashReportsForPublish), so there is nothing in this file
			// for a re-seed to bring in line, and sending the operator to a hand
			// edit for it would ask for a key the config has no place for.
		default:
			// The account-choice members — default_claude_account and each
			// claude_account_stages.<stage> — land here, and the refusal above
			// names them: this re-seed splices the members it has a shape for,
			// and these are the operator's own settings, which is why the local
			// config is their only store (the locLocal rows `fugaro settings set`
			// writes). An operator whose node carries an account this file does
			// not writes it back with that command and publishes it with
			// `fugaro project policy publish`, not by running `fugaro update`
			// again: the update refuses this file until a publish has landed
			// (publishPlanFor's generation check), so its message names the
			// publish rather than the run that would repeat the refusal.
			hand = append(hand, member)
		}
	}
	return out, written, hand
}

// applyPolicyReseed is the accepted offer: the file is backed up and written
// with what the re-seed could splice, and a member it could not write is
// refused rather than skipped — a re-seed that recorded a generation while
// leaving a member behind would let the next publish overwrite the newer node
// with the older file, which is the whole of what D854 refuses. The spliced
// members are written even when the offer is refused, so the file is the node's
// own on everything but the members the refusal names; that is what makes the
// publish it recommends safe, since a publish builds the node from this file.
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
		// The members this re-seed could splice are written even though the
		// offer is refused, because the remedy the sentence below names for an
		// account key — `fugaro project policy publish` — builds the node from
		// this file: a spliced member dropped here is this file's older value
		// written back over the newer node the moment the operator follows that
		// advice, which is the overwrite D854 refuses, made by the sentence that
		// recommends the publish. What is left unwritten is named.
		//
		// The generation stays unrecorded — the caller returns on the error
		// below — because the file still differs from the node on the members
		// named: a record here would claim a baseline the file does not have,
		// and the next run's refusal is what carries the rest over.
		if err := writeFileAtomicGuarded(cmd, path, old, out); err != nil {
			return nil, err
		}
		// The remedy is per member, because the two kinds need different ones
		// and naming the wrong one leaves the operator where they started: a
		// value the node states and this file does not goes back by a hand edit
		// the update then re-seeds around, while an account-choice key has no
		// published value for a hand edit to put there — the local file is its
		// only store — and the command that writes it to the node is
		// `fugaro project policy publish` at the operator's own terminal. The
		// update is named only where it is the way out: run again over an
		// account key it refuses this same file (publishPlanFor), which is the
		// dead end this sentence exists not to send anyone into.
		//
		// The two are ordered where both are named, because that publish writes
		// every member this file states over the node's: run before the hand
		// edits, it puts this file's older values for them there, which is the
		// overwrite D854 refuses one round later — and it is why the members
		// written above are named in the refusal: they are what the publish no
		// longer carries back.
		accounts, rest := splitAccountChoices(hand)
		var remedies []string
		if len(rest) > 0 {
			remedies = append(remedies, fmt.Sprintf("put the published values of %s there by hand — a flow block rewritten as a block mapping is one an edit can reach — and run %s again",
				strings.Join(rest, ", "), selfCommand()+" update"))
		}
		if len(accounts) > 0 {
			publishing := "then publish it with `fugaro project policy publish` at your own terminal, which builds the node from this file and never consults the recorded generation"
			if len(rest) > 0 {
				publishing = "then, once this file states the node's own values for the members above, publish it with `fugaro project policy publish` at your own terminal — that command builds the node from this file, so run before those edits it would write this file's older values for them over the node's"
			}
			remedies = append(remedies, fmt.Sprintf("%s: an account choice is your own, and the local config is its only store — write the choice with `fugaro settings set default_claude_account <name>` or `fugaro settings set stages.<stage>.claude_account <name>`, %s (%s is the upgrade, and its policy stage refuses this file until that publish has landed)",
				strings.Join(accounts, ", "), publishing, selfCommand()+" update"))
		}
		left := fmt.Sprintf("the published policy node's %s cannot be re-seeded into %s by one edit", strings.Join(hand, ", "), path)
		if len(written) > 0 {
			left += fmt.Sprintf(", so the node's own values for %s were written there and no generation was recorded", strings.Join(written, ", "))
		} else {
			left += ", so the file was left as it is"
		}
		return written, userErr("%s: %s", left, strings.Join(remedies, "; and "))
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

// cloneURLsToImport is the published node's clone_urls block when this machine's
// file states none of its own and there is a file to write it into, and nil
// otherwise (D842, D843, D844).
//
// The two nil halves are the two cases the design words: a file that states a
// block of its own is the operator's answer already, and a node that carries
// none is an installation that never had the block — no default is seeded into
// a file nobody wrote (D843). A run with no local config file at all (a shared
// config, a --config elsewhere) has nothing to write the block into, and the
// block stays in the node for a machine whose file can hold it.
func cloneURLsToImport(lc *localcfg.Config, node *projectpolicy.Policy, path string) map[string]string {
	if path == "" || lc == nil || lc.CloneURLs != nil || node == nil || len(node.CloneURLs) == 0 {
		return nil
	}
	return node.CloneURLs
}

// importPolicyNodeCloneURLs is D842/D844's migration: the clone_urls block moved
// out of the node and into the local config, and the first `fugaro update`
// after that release copies the node's entries into the file, so the operator
// has one place to change them and this command publishes it from then on.
//
// It writes the file with the triple every writer of it uses — the printed
// entries, a backup, an atomic write — and it asks nothing of its own: it is one
// step of a run the operator confirmed whole (initflow's own confirmation, which
// is what `fugaro update` shows the plan under), so a run that declines writes
// neither the file nor the node. A coding agent's session never reaches here at
// all, which the stage refuses on the way in (SD9).
//
// The entries are printed before the write and not after it: this is the
// migration that moves a block an operator could see only in the console into a
// file of their own, and a write they read afterwards is a write they cannot
// check against what it replaced.
func (m *policyMigration) importPolicyNodeCloneURLs(w io.Writer) error {
	if len(m.importCloneURLs) == 0 {
		return nil
	}
	old, err := os.ReadFile(m.path)
	if err != nil {
		return userErr("importing the published policy node's clone_urls block into %s: %v", m.path, err)
	}
	next := old
	for _, canonical := range slices.Sorted(maps.Keys(m.importCloneURLs)) {
		out, err := yamledit.SetScalar(next, []string{"clone_urls", canonical}, m.importCloneURLs[canonical])
		if err != nil {
			return userErr("%s cannot take the published policy node's clone_urls block in one edit: write the block in by hand, or set each entry with 'fugaro project clone-url set', then run %s again",
				m.path, selfCommand()+" update")
		}
		next = out
	}
	if !yamledit.Changed(old, next) {
		// The file already holds every entry — an earlier run of this update
		// wrote them and a later stage re-planned — so there is nothing to back
		// up, nothing to write and nothing to say.
		m.lc.CloneURLs = maps.Clone(m.importCloneURLs)
		return nil
	}
	fmt.Fprintf(w, "the published policy node carries the clone_urls block this machine's config file does not; importing it into %s:\n", m.path)
	for _, canonical := range slices.Sorted(maps.Keys(m.importCloneURLs)) {
		fmt.Fprintf(w, "  clone_urls %s: %s\n", canonical, m.importCloneURLs[canonical])
	}
	backup, err := backupFile(m.path, old, time.Now())
	if err != nil {
		return userErr("backing up %s before importing the published policy node's clone_urls block: %v", m.path, err)
	}
	if err := writeFileAtomic(m.path, next); err != nil {
		return userErr("importing the published policy node's clone_urls block into %s: %v", m.path, err)
	}
	m.lc.CloneURLs = maps.Clone(m.importCloneURLs)
	fmt.Fprintf(w, "imported into %s (backup at %s): the file is the block's source from now on, and `%s update` publishes it\n", m.path, backup, selfCommand())
	return nil
}

// formatFloat is a policy number as the yaml file spells it: no exponent, and
// no trailing zeroes ("5", never "5.000000").
func formatFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
}
