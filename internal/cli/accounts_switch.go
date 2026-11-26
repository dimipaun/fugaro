package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro accounts switch FROM TO`: the one command that moves
// every key of this machine's config that NAMES one account as a choice to
// another. The owner's own switch took nine hand edits —
// crafts.<craft>.access (four), default_claude_account,
// stages.<stage>.claude_account (two) and repos.<repo>.claude_accounts (two) —
// followed by `fugaro update`, and an update after an edit is the second step
// the HARD RULE forbids. The rewrite and the converge are one command here, as
// they are in `accounts alias set`, whose guards, write path and converge this
// reuses.
//
// It is not `accounts alias set` and neither command replaces the other: an
// alias IS the indirection, so repointing one moves the block's single entry and
// leaves every key naming the alias as it was, while a switch moves the keys
// themselves, one by one, and there may be no alias in the file at all.

// accountsSwitchOptions are the flags `accounts switch` takes. There is no
// --json and no flag that carries a value: the whole output is the diff, the
// keys and the block a person reads, as `accounts alias set`'s is.
type accountsSwitchOptions struct {
	yes    bool
	dryRun bool
}

func newAccountsSwitchCmd() *cobra.Command {
	var o accountsSwitchOptions
	cmd := &cobra.Command{
		Use:   "switch FROM TO",
		Short: "Move every key of this machine's config that names FROM as an account choice to TO",
		Long: `Move every key of this machine's local project config that names FROM as an
account choice to TO, and converge this installation on the config it writes.

The keys are the whole of what may name an account as a choice:

  default_claude_account
  stages.<stage>.claude_account
  crafts.<craft>.access
  repos.<repo>.claude_accounts
  profiles.<profile>.claude_accounts

Nothing else in the file is touched: the claude_accounts block keeps both names
and the credentials block keeps both credentials, so both accounts stay declared
and a switch takes no token away.

TO must already be declared here — an account of the claude_accounts block,
never an alias and never a pool — and its allow_data_to must cover every
repository the keys it takes over serve: each repository whose own
claude_accounts list names it, and every allow_repos entry of a profile that
lists it. The refusal names the one command that widens the list.

FROM may be a declared account, the default account (the unsuffixed
claude-oauth-token secret) or a craft's own credential. It may not be an alias:
the keys naming one are meant to keep naming it, and fugaro accounts alias set
moves all of them in one edit. It may not be a pool or one of a pool's members:
a pool is walked inside a run, at the stage boundary, and a switch may not
re-resolve what a run spends mid-stage.

Then it converges, in the same command and after the write, exactly as
fugaro accounts alias set does: the policy node, the job set and this checkout's
repository root, through the stages fugaro update runs.

A run already in flight keeps the account its launch resolved: nothing here
rewrites a record, a ledger or a counter (docs/design/update-in-flight.md), and
a stage already going spends the credential it started with.

--dry-run prints the keys the switch would rewrite, and the diff of the file it
would write, and writes nothing.

Run this at your own terminal: a switch decides which credential this project's
stages spend, so a coding agent's session is refused, --yes included.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsSwitch(cmd, args[0], args[1], o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.yes, "yes", false, "confirm the switch without a prompt")
	f.BoolVar(&o.dryRun, "dry-run", false, "print the keys the switch would rewrite and write nothing")
	return cmd
}

// accountsSwitchSession refuses this command in a coding agent's session, before
// anything is read (D2097, as accountsAliasSession does for the alias write): a
// switch decides which credential this project's stages spend, which is the
// owner's own step at their own terminal, and --yes does not answer it. It
// stands at the top of the command and not only in the write path, so that a
// switch with nothing to rewrite is refused here rather than answered by that
// path's "no change" line.
func accountsSwitchSession(cmd *cobra.Command, command string) error {
	marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	if marker == "" {
		return nil
	}
	return userErr("refusing to switch a model account in a coding agent's session (%s is set): a switch decides which credential this installation's stages spend, which is your own step with your own token at your terminal; nothing was written; run this yourself at your own terminal: %s",
		marker, command)
}

func runAccountsSwitch(cmd *cobra.Command, from, to string, o accountsSwitchOptions) error {
	command := accountsSwitchCommand(from, to)
	if err := accountsSwitchSession(cmd, command); err != nil {
		return err
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	// A switch decides which account this project's stages spend, which is the
	// account's own project-wide allow_data_to's business — so it is declared by
	// a project admin and by nobody else, exactly as `accounts add` declares an
	// account.
	if err := accessGateProject(cmd.Context(), lc, nil, "nothing was written", "switching a model account"); err != nil {
		return err
	}
	if err := accountsSwitchRefusals(lc, from, to); err != nil {
		return err
	}
	keys := accountsSwitchPlan(lc, from)
	if len(keys) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "no change: no key of this config names %s as an account choice\n", pluginwire.Printable(from))
		return nil
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next, err := accountsSwitchEdit(old, keys, from, to)
	if err != nil {
		return err
	}
	after, err := localcfg.Parse(next)
	if err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	// The subset rule is D3's, asked of the document this run would WRITE: what
	// the file holds today was already held to it when the keys named FROM, and
	// the whole question here is whether TO may stand where FROM stood.
	if err := accountsSwitchCoverage(after, to, keys); err != nil {
		return err
	}
	accountsSwitchLines(cmd, keys, from, to)
	if o.dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n%s\ndry run: nothing was written; run `%s` to write %s and converge\n",
			sel.Path, settingsEditDiff(old, next), command, sel.Path)
		return nil
	}
	// The jobs this switch stops declaring, asked of the installation before a
	// byte is written: a job a run is in flight on cannot be retired, and the
	// write may not land before that is known (issue #3686's own failure, and
	// update_retire_jobs.go's accountsSwitchJobRefusal).
	if err := accountsSwitchJobRefusal(cmd.Context(), lc, keys, from, command); err != nil {
		return err
	}
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    accountsSwitchWhat(keys, from, to),
		Kind:    settingsDocLocal,
		Command: command,
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	return accountsSwitchConverge(cmd.Context(), cmd, after, next, sel.Path, from, to, len(keys))
}

// accountsSwitchKey is one key the switch rewrites: the path the editor is given,
// the spelling a person reads, and what the key is about besides the rewrite.
//
// items is the list a list key holds — nil for the three scalar keys — which is
// what settingsSetList compares the written list against. repo and profile name
// the repository a repos.<repo> entry serves and the profile a
// profiles.<profile> list belongs to: they are the two keys whose rewrite the
// subset rule has to judge, and they are carried here rather than read back off
// the key's own spelling.
type accountsSwitchKey struct {
	key     string
	path    []string
	items   []string
	repo    string
	profile string
}

// accountsSwitchPlan is every key of the config that names from as an account
// choice, in the order the file writes its blocks: the default, the stages, the
// crafts, the repositories and the profiles. The two list keys are rewritten as
// lists and the three scalar keys as scalars, and the order is the plan's own
// rather than the file's, because a key the file writes under another block
// than it is named in would make the lines a person reads harder to follow
// while changing nothing.
func accountsSwitchPlan(lc *localcfg.Config, from string) []accountsSwitchKey {
	var out []accountsSwitchKey
	if lc.DefaultClaudeAccount == from {
		out = append(out, accountsSwitchKey{key: "default_claude_account", path: []string{"default_claude_account"}})
	}
	for _, stage := range slices.Sorted(maps.Keys(lc.ClaudeAccountStages)) {
		if lc.ClaudeAccountStages[stage].ClaudeAccount != from {
			continue
		}
		out = append(out, accountsSwitchKey{key: "stages." + stage + ".claude_account", path: []string{"stages", stage, "claude_account"}})
	}
	for _, craft := range config.CraftNames {
		if lc.Crafts.Of(craft).Access != from {
			continue
		}
		out = append(out, accountsSwitchKey{key: "crafts." + craft + ".access", path: []string{"crafts", craft, "access"}})
	}
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		items := lc.Repos[repo].ClaudeAccounts
		if !slices.Contains(items, from) {
			continue
		}
		out = append(out, accountsSwitchKey{
			key:   "repos." + repo + ".claude_accounts",
			path:  []string{"repos", repo, "claude_accounts"},
			items: slices.Clone(items), repo: repo,
		})
	}
	for _, profile := range slices.Sorted(maps.Keys(lc.Profiles)) {
		items := lc.Profiles[profile].ClaudeAccounts
		if !slices.Contains(items, from) {
			continue
		}
		out = append(out, accountsSwitchKey{
			key:   "profiles." + profile + ".claude_accounts",
			path:  []string{"profiles", profile, "claude_accounts"},
			items: slices.Clone(items), profile: profile,
		})
	}
	return out
}

// accountsSwitchEdit is the file this run would write: each key taken over, one
// at a time.
//
// The three scalar keys are written through yamledit.SetScalar, which is what
// every scalar writer here uses. The two list keys go through settingsSetList —
// FROM taken out and TO appended, every other entry keeping its place — because
// the editor has no in-place item replacement, and none is needed: an account
// list is a SET everywhere it is read (config.AllowedClaudeAccounts sorts it,
// projectpolicy.DeriveAccounts sorts it, config.ProjectJobAccounts sorts it),
// so where TO lands among the entries is not a fact about the config.
//
// settingsSetList rewrites a list written as a flow sequence (["team-a"]) by
// taking the whole key away and writing its items back in block style, which is
// the one re-formatting every list writer here does. What is left is the entry
// itself written as a one-line flow mapping —
// repos.acme/app: { provider: github, claude_accounts: [team-a] } — where the
// member's own value is a collection and no edit goes inside it: yamledit
// refuses every flow shape it cannot splice into rather than re-formatting the
// line, and this command refuses with it rather than writing a file with the
// entry's other members rearranged. The sentence names the style the fix is in,
// because yamledit's own reason class is not something a person can act on.
func accountsSwitchEdit(old []byte, keys []accountsSwitchKey, from, to string) ([]byte, error) {
	next := old
	for _, k := range keys {
		var err error
		if k.items == nil {
			next, err = yamledit.SetScalar(next, k.path, to)
		} else {
			want := slices.Clone(k.items)
			for i, item := range want {
				if item == from {
					want[i] = to
				}
			}
			next, err = settingsSetList(next, k.path, k.items, want)
		}
		if err != nil {
			if k.items != nil && yamledit.ReasonOf(err) == yamledit.ReasonFlowCollection {
				return nil, userErr("%s is written as a one-line flow mapping, and a list inside one is a shape no edit goes into: this command rewrites a list by taking its items out and adding them back, and it will not re-format an entry of yours to do it — write %s in block style, as docs/project-claude-accounts.md writes it, then run this again; nothing was written",
					pluginwire.Printable(strings.TrimSuffix(k.key, ".claude_accounts")),
					pluginwire.Printable(strings.TrimSuffix(k.key, ".claude_accounts")))
			}
			return nil, userErr("%s: %v", pluginwire.Printable(k.key), err)
		}
	}
	return next, nil
}

// accountsSwitchRefusals is the rule each of the two names breaks, asked before
// anything is read off the file, so that the refusals a person meets name the
// command that settles them.
//
// The three refusals on FROM are one rule: this command moves the keys that
// NAME an account, and each of the three names something else. An alias is the
// indirection the keys are meant to keep naming, so `accounts alias set` repoints
// all of them in one edit — which is what the alias is FOR. A pool and a pool's
// member are walked inside a run, at the stage boundary: the members are chosen
// there, and a switch of a craft that names a pool would re-resolve what a run
// spends while the run is going, which is the one thing D2094's in-flight rule
// says nothing here may do.
func accountsSwitchRefusals(lc *localcfg.Config, from, to string) error {
	if _, isAlias := lc.ClaudeAccountAliases[from]; isAlias {
		return userErr("claude_account_aliases.%s is an alias, and every key naming it is meant to keep naming it: an alias IS the indirection, so what moves is the block's one entry, and one command moves it for every key — run `%s` to make them all choose %s; nothing was written",
			pluginwire.Printable(from), accountsAliasSetCommand(from, to), pluginwire.Printable(to))
	}
	if slices.ContainsFunc(lc.Pools, func(p config.Pool) bool { return p.Name == from }) {
		return userErr("pools.%s is a pool, and a pool is a list of credentials whose members are chosen inside a run, at the stage boundary: pointing the craft that names it at one account would change which credential this project spends without changing the pool's own answer, and a run already going resolves its member from the node — point the craft at the account itself, or change the pool with `fugaro accounts pool add` and `fugaro accounts pool rm`; nothing was written",
			pluginwire.Printable(from))
	}
	if slices.ContainsFunc(lc.Pools, func(p config.Pool) bool { return slices.Contains(p.Members, from) }) {
		return userErr("pools.%s.members names %s, and a switch moves the account-choice keys alone: the pool would still spend %s after the switch, whatever the keys say, because a pool's members are the pool's own — declare the pool again without it (`%s`), then run this switch again; nothing was written",
			accountsSwitchPoolNaming(lc, from), pluginwire.Printable(from), pluginwire.Printable(from),
			accountsPoolAddCommand(accountsSwitchPoolNaming(lc, from), accountsSwitchMembersWithout(lc, from), accountsPoolWriteOptions{}))
	}
	// TO is a declaration this file already holds, never "default": the default
	// account is the unsuffixed secret, which no claude_accounts entry declares
	// and which therefore has no allow_data_to for the rule below to read. The
	// alias's own target rule (accountsAliasProblem) is the shape of this one.
	if target, isAlias := lc.ClaudeAccountAliases[to]; isAlias {
		return userErr("claude_account_aliases.%s is an alias, and a switch names a declared account and never a name standing for one: the account here is %s, so run this switch with that name; nothing was written",
			pluginwire.Printable(to), pluginwire.Printable(target))
	}
	if _, declared := lc.ClaudeAccounts[to]; !declared {
		return userErr("%s is not an account this config declares, and a switch only moves the keys onto an account that is already here: declare it with `fugaro accounts add %s`, whose --repos names the repositories its token may be used by, then run this switch again; nothing was written",
			pluginwire.Printable(to), pluginwire.Printable(to))
	}
	return nil
}

// accountsSwitchPoolNaming is the pool whose members name the account, in the
// file's own order, and the first such pool is the one both sentences name: a
// switch moves no pool's list, so one named pool is enough to tell the person
// what stands between them and the switch.
func accountsSwitchPoolNaming(lc *localcfg.Config, from string) string {
	for _, p := range lc.Pools {
		if slices.Contains(p.Members, from) {
			return p.Name
		}
	}
	return ""
}

// accountsSwitchMembersWithout is that pool's members with the account taken out:
// what `accounts pool add` has to be given to declare the pool without it.
func accountsSwitchMembersWithout(lc *localcfg.Config, from string) []string {
	name := accountsSwitchPoolNaming(lc, from)
	for _, p := range lc.Pools {
		if p.Name != name {
			continue
		}
		out := make([]string, 0, len(p.Members))
		for _, m := range p.Members {
			if m != from {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// accountsSwitchCoverage is D3 asked of TO: an account a repository's own list
// names, or that a profile lists, covers every repository that key serves —
// and a switch may not write a key whose account does not, because the
// repository's runs would then be admitted to spend a token that may not reach
// them.
//
// It is asked of the document this run would write and not of the file as it is,
// and the remedies are the two commands that settle each case: `accounts allow`
// widens the account's own list, and a profile's allow_repos is the profile's to
// narrow. The profile half is accountsDenySubsetRule's rule and its sentences,
// because the two commands ask the same question about the same list — which is
// why the remedy is spelled by accountsAllowCommand in both.
func accountsSwitchCoverage(after *localcfg.Config, to string, keys []accountsSwitchKey) error {
	acct := after.ClaudeAccounts[to]
	for _, k := range keys {
		switch {
		case k.repo != "":
			if acct.AllowsData(k.repo) {
				continue
			}
			return userErr("repos.%s.claude_accounts names %s and claude_accounts.%s.allow_data_to does not cover %s: a repository's own list is what its runs may spend, and moving the entry onto an account whose token may not reach it is the one thing this command may not write — widen the account's list with `%s`, then run this switch again; nothing was written",
				pluginwire.Printable(k.repo), pluginwire.Printable(to), pluginwire.Printable(to), pluginwire.Printable(k.repo),
				accountsAllowCommand(to, []string{k.repo}, true))
		case k.profile != "":
			if err := accountsSwitchProfileCoverage(after, to, k.profile); err != nil {
				return err
			}
		}
	}
	return nil
}

// accountsSwitchProfileCoverage is the profile half of that rule, in
// accountsDenySubsetRule's own words: the preconditions a profile listing
// accounts meets — its own exact allow_repos list — and then the coverage
// question, repository by repository.
func accountsSwitchProfileCoverage(after *localcfg.Config, to, profile string) error {
	p := after.Profiles[profile]
	if len(p.AllowRepos) == 0 {
		return userErr("profiles.%s.claude_accounts would list %s and profiles.%s writes no allow_repos: a profile that lists accounts must write its own list, and without one which repositories it admits is undecidable — add allow_repos to profiles.%s, naming the repositories it admits, then run this again; nothing was written",
			pluginwire.Printable(profile), pluginwire.Printable(to), pluginwire.Printable(profile), pluginwire.Printable(profile))
	}
	for _, pattern := range p.AllowRepos {
		if strings.Contains(pattern, "*") {
			return userErr("profiles.%s.allow_repos carries the wildcard %s, and a profile that lists accounts must name exact repositories: whether the account covers it is decided repository by repository, and a wildcard makes that undecidable — write the repositories themselves, then run this again; nothing was written",
				pluginwire.Printable(profile), pluginwire.Printable(pattern))
		}
	}
	var missing []string
	for _, r := range p.AllowRepos {
		if !accountsAddCovers(after.ClaudeAccounts[to].AllowDataTo, p.Provider, r) {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	// The remedy writes the account's own list, whose spelling is owner/name,
	// while a profile's allow_repos is the canonical spelling the profile's
	// provider qualifies (accountsDenySubsetRule's own reason for the trimming).
	keep := make([]string, 0, len(missing))
	for _, r := range missing {
		keep = append(keep, strings.TrimPrefix(r, p.Provider+"/"))
	}
	return userErr("profiles.%s.allow_repos lists %s and claude_accounts.%s.allow_data_to does not cover %s: an account a profile lists covers every repository that profile admits, and the profile's repositories are the intersection of the two and never their union — widen the account's list with `%s`, or take the repositories out of profiles.%s.allow_repos, then run this again; nothing was written",
		pluginwire.Printable(profile), strings.Join(missing, ", "), pluginwire.Printable(to), strings.Join(missing, ", "),
		accountsAllowCommand(to, keep, true), pluginwire.Printable(profile))
}

// accountsSwitchLines is the one line per key the command prints before it
// converges: the key as the file spells it and the move it takes. It is what the
// owner's nine hand edits are replaced by, and --dry-run prints it and stops.
func accountsSwitchLines(cmd *cobra.Command, keys []accountsSwitchKey, from, to string) {
	w := cmd.OutOrStdout()
	for _, k := range keys {
		fmt.Fprintf(w, "%s: %s -> %s\n", k.key, pluginwire.Printable(from), pluginwire.Printable(to))
	}
}

// accountsSwitchCommand is the command a refusal prints for the person to run at
// their own terminal: the same edit, spelled whole, because nothing they type
// inside a session is remembered.
func accountsSwitchCommand(from, to string) string {
	return "fugaro accounts switch " + quoteWord(from) + " " + quoteWord(to)
}

// accountsSwitchWhat is the sentence this run prints before the write: which
// keys move and what a launch after it resolves, so whoever reads the run knows
// what changes and what does not.
func accountsSwitchWhat(keys []accountsSwitchKey, from, to string) string {
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, k.key)
	}
	return fmt.Sprintf("switch %d %s of this machine's config from %s to %s (%s); the claude_accounts block and the credentials block are left alone, so both accounts stay declared; publish the policy node and deploy the job set from the config this writes (new launches only: a run in flight keeps the account its launch resolved)",
		len(keys), accountsSwitchKeyWord(len(keys)),
		pluginwire.Printable(from), pluginwire.Printable(to), strings.Join(quoteKeys(names), ", "))
}

// accountsSwitchKeyWord keeps that sentence's grammar right for the one-key and
// the several-key case alike.
func accountsSwitchKeyWord(n int) string {
	if n == 1 {
		return "key"
	}
	return "keys"
}

// accountsSwitchDoor is the verb this command's own converge runs under: the
// door accountsAddRunStages names on every line it prints, and one of the two
// facts initOptions.ownerHand reads to tell a run that IS the owner's hand at
// their terminal from one that is not (D2097).
const accountsSwitchDoor = "accounts switch"

// accountsSwitchConverge is the publish and the deploy, through `accounts add`'s
// own runner over the registered stages (accountsAddRunStages), exactly as
// accountsAliasConverge is: the node has to carry the account the keys now name,
// and the job set has to mount that account's token, whichever command moved
// them.
func accountsSwitchConverge(ctx context.Context, cmd *cobra.Command, after *localcfg.Config, wrote []byte, path, from, to string, keys int) error {
	run := stepRunFor(cmd, "accounts-switch", cmd.ErrOrStderr())
	defer run.close()
	stages, err := accountsAddRunStages(ctx, cmd, run, after, path, wrote, accountsSwitchDoor)
	if err != nil {
		return err
	}
	// The block the node carries is read back off the node, as the alias write's
	// is: this command's last line says there is nothing left to run, and a line
	// claiming a member the node does not carry would be the one sentence it may
	// not print.
	members, why := accountsAliasNode(ctx, after)
	accountsSwitchReport(cmd, after, stages, from, to, keys, members, why)
	return nil
}

// accountsSwitchReport is the command's answer: the move and its size, the
// members the node now carries, what the installation did, and the one line that
// says what is left — which is nothing.
func accountsSwitchReport(cmd *cobra.Command, lc *localcfg.Config, stages map[string]initflow.StageResult, from, to string, keys int, members map[string]string, why string) {
	w := cmd.OutOrStdout()
	row := func(label, value, rest string) {
		fmt.Fprintf(w, "%-10s%-18s%s\n", label, pluginwire.Printable(value), rest)
	}
	row("switch", from, fmt.Sprintf("-> %s in %d %s", pluginwire.Printable(to), keys, accountsSwitchKeyWord(keys)))
	// The jobs are TO's, whichever keys moved: the job set is per
	// (profile, provider, account), so this is the same job `accounts add` names
	// for that account, and the type is the config's own reading of it.
	profile := accountsAddProfileOf(lc, to)
	row("deploy", "jobs", accountsAddDeployLine(lc, accountsAddCredentialType(lc, to), profile, to, stages[initflow.Installation]))
	row("publish", "policy node", accountsAliasPublishLine(stages[initflow.Policy], members, why))
	fmt.Fprintln(w, accountsSwitchLastLine(from, to))
}

// accountsSwitchLastLine is the command's last line: the thing the owner does
// next, which is nothing. What it says about a run already going is the half of
// D2094 the owner needs — a switch moves NEW launches, and a run in flight keeps
// the account its launch resolved — and it is the same rule the help states.
func accountsSwitchLastLine(from, to string) string {
	return fmt.Sprintf("Nothing else to run: every key that named %s now names %s, so new launches choose it; a run already going keeps the account its launch resolved.\n",
		pluginwire.Printable(from), pluginwire.Printable(to))
}
