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
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro accounts alias`: the one command that moves which
// account the keys naming an alias resolve to (docs/account-aliases/spec.md §6,
// D2096). It is one command and not an edit per key because the alias IS the
// indirection — `default_claude_account` and every `stages.<stage>.claude_account`
// keep naming the alias, and what moves is the block's one entry.
//
// The write is the settings commands' own path (applySettingsEdit, D847) and the
// converge is `accounts add`'s runner over the registered stages, for add's
// reason: the node and the job set have to carry the account the keys now
// resolve to, and a command that left the owner to run `fugaro update` after it
// would be the second step the HARD RULE forbids.

// accountsAliasSetOptions are the flags `accounts alias set` takes. There is no
// --json and no flag that carries a value: the whole output is the diff and the
// block a person reads, as `accounts add`'s and `accounts rm`'s are.
type accountsAliasSetOptions struct {
	yes bool
}

func newAccountsAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Point a name at one named Claude account, so the keys naming it choose that account",
		Long: `Manage this machine's claude_account_aliases block: the names the account-choice
keys may hold instead of an account.

A key that names an alias — default_claude_account, and each
stages.<stage>.claude_account — keeps the alias as written, and whoever reads it
resolves the alias before it asks anything else, the account's own
allow_data_to included. So moving every stage that names one is one edit here,
and an alias never reaches the policy node, a task, a run record or a job
image under its own name.

The block is the owner's own and is never published (D2090): it is stated in
this machine's config, and the read of it is local.

Run these at your own terminal: a repoint decides which credential this
project's stages spend, so a coding agent's session is refused, --yes
included.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown accounts alias subcommand; want set or rm (see fugaro accounts alias --help)")
		},
	}
	cmd.AddCommand(newAccountsAliasSetCmd(), newAccountsAliasRmCmd())
	return cmd
}

func newAccountsAliasSetCmd() *cobra.Command {
	var o accountsAliasSetOptions
	cmd := &cobra.Command{
		Use:   "set ALIAS ACCOUNT",
		Short: "Point ALIAS at ACCOUNT, so every key naming the alias chooses it",
		Long: `Point an alias at one named Claude account, and converge this installation on
the repoint.

The alias stands for exactly one account of this config's claude_accounts
block, or for "default" (the unsuffixed claude-oauth-token secret). It never
stands for another alias, a pool or a credential: a chain would need a cycle
check and a reader that prints the path a name came through, and a pool is
walked inside a run — its members are chosen there, at the stage boundary —
which is the re-resolution a repoint must not cause.

Then it converges, in the same command and after the write: the policy node
with the account the keys now resolve to, the job set and this checkout's
repository root, through the stages fugaro update runs. The new target's job is
added; the old target's job is never deleted (D2095), because the account may
still be served by another profile or named by another alias.

A run already in flight keeps the account its launch resolved: nothing here
rewrites a record, a ledger or a counter (docs/design/update-in-flight.md), and
a stage already going spends the credential it started with.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsAliasSet(cmd, args[0], args[1], o)
		},
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the repoint without a prompt")
	return cmd
}

func newAccountsAliasRmCmd() *cobra.Command {
	var o accountsAliasSetOptions
	cmd := &cobra.Command{
		Use:   "rm ALIAS",
		Short: "Drop an alias no account-choice key names any more",
		Long: `Drop one alias from this machine's claude_account_aliases block.

An alias a choice key still names is refused, naming every key: the key would
resolve to a name this config no longer defines, which is the file the loader
refuses. Point those keys at the account they should choose instead
(fugaro accounts alias set ALIAS ACCOUNT), or change them, and remove the alias
once nothing names it.

Then it converges, as fugaro accounts alias set does: the block is local and
never published, so the node, the job set and this checkout's repository root
are converged on the config this wrote and nothing is deleted.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsAliasRm(cmd, args[0], o)
		},
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the removal without a prompt")
	return cmd
}

// accountsAliasSession refuses this command in a coding agent's session, before
// anything is read (D2097): a repoint decides which credential this project's
// stages spend, which is the owner's own step at their own terminal — the same
// step `accounts add`'s and `accounts rm`'s refusals are about, and `--yes` does
// not answer it. It stands at the top of both subcommands and not only in the one
// write path, because a repoint onto the account an alias already names has
// nothing to write and would otherwise be answered by that path's "no change"
// line rather than by a refusal.
func accountsAliasSession(cmd *cobra.Command, command string) error {
	marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	if marker == "" {
		return nil
	}
	return userErr("refusing to move an account alias in a coding agent's session (%s is set): an alias decides which credential this installation's stages spend, which is your own step with your own token at your terminal; nothing was written; run this yourself at your own terminal: %s",
		marker, command)
}

func runAccountsAliasSet(cmd *cobra.Command, alias, account string, o accountsAliasSetOptions) error {
	if err := accountsAliasSession(cmd, accountsAliasSetCommand(alias, account)); err != nil {
		return err
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	// An alias is the project's — what it may be mounted for is the account's own
	// project-wide allow_data_to — so it is declared by a project admin and by
	// nobody else, exactly as `accounts add` declares an account.
	if err := accessGateProject(cmd.Context(), lc, nil, "nothing was written", "repointing a model account"); err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	after, next, err := accountsAliasResolve(sel.Path, lc, alias, account, old)
	if err != nil {
		return err
	}
	what := accountsAliasWhat(lc, alias, account)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    settingsDocLocal,
		Command: accountsAliasSetCommand(alias, account),
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	return accountsAliasConverge(cmd.Context(), cmd, after, next, sel.Path, alias, account, false)
}

func runAccountsAliasRm(cmd *cobra.Command, alias string, o accountsAliasSetOptions) error {
	if err := accountsAliasSession(cmd, accountsAliasRmCommand(alias)); err != nil {
		return err
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if err := accessGateProject(cmd.Context(), lc, nil, "nothing was written", "removing an account alias"); err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	// The account the alias stood for is read before the removal: after it the
	// block no longer holds it, and the report's job row and closing line are
	// about the account whose jobs are left standing, not about a name that went
	// with the entry.
	target := lc.ClaudeAccountAliases[alias]
	after, next, err := accountsAliasRmResolve(sel.Path, lc, alias, old)
	if err != nil {
		return err
	}
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("remove claude_account_aliases.%s (%s) of this machine's config; it is never published (D2090)", pluginwire.Printable(alias), pluginwire.Printable(target)),
		Kind:    settingsDocLocal,
		Command: accountsAliasRmCommand(alias),
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	return accountsAliasConverge(cmd.Context(), cmd, after, next, sel.Path, alias, target, true)
}

// accountsAliasResolve is the edit, or the refusal: the block this file would
// carry, read back strictly, and the config it decodes to.
//
// The rules are config.ValidateAccountAliases' and not a second copy of them
// (D2092, D2093): the alias's own name rule, the one namespace it shares with
// the accounts, the credentials and the pools, and the one account it may stand
// for. They are asked of the document this run would WRITE, which is the only
// document whose answers matter here — the file as it is was already held to
// them when it was loaded.
func accountsAliasResolve(path string, lc *localcfg.Config, alias, account string, old []byte) (*localcfg.Config, []byte, error) {
	next := old
	// A target that is the alias's own name is the shortest chain there is, and
	// writing it first would leave the block naming itself for the length of the
	// parse below: the rule below answers it with the chain refusal, which is
	// the sentence the operator needs.
	merged := maps.Clone(lc.ClaudeAccountAliases)
	if merged == nil {
		merged = map[string]string{}
	}
	merged[alias] = account
	if p, ok := accountsAliasProblem(alias, merged, lc); ok {
		return nil, nil, userErr("%s: %s; nothing was written", p.Path, p.Message)
	}
	next, err := yamledit.SetScalar(next, []string{"claude_account_aliases", alias}, account)
	if err != nil {
		return nil, nil, userErr("claude_account_aliases.%s: %v", pluginwire.Printable(alias), err)
	}
	after, err := localcfg.Parse(next)
	if err != nil {
		return nil, nil, userErr("the local config this would write does not read back: %v", err)
	}
	return after, next, nil
}

// accountsAliasProblem is the rule this alias breaks in the block the run would
// write. It answers the alias's own entry and not the whole block's problems:
// the file as it is loaded clean, so the only entry an edit can break is the one
// it writes — and reading the other names here would attribute a problem to a
// run that did not cause it.
func accountsAliasProblem(alias string, merged map[string]string, lc *localcfg.Config) (config.Problem, bool) {
	problems := config.ValidateAccountAliases(merged, lc.ClaudeAccounts, lc.Credentials, lc.Pools)
	at := "claude_account_aliases." + pluginwire.Printable(alias)
	for _, p := range problems {
		if p.Path == at {
			return p, true
		}
	}
	return config.Problem{}, false
}

// accountsAliasRmResolve is the removal, or the refusal: an alias a choice key
// still names is not removed, because the key would resolve to a name this file
// no longer defines — the state the loader refuses, and one this command may not
// write (spec §6).
func accountsAliasRmResolve(path string, lc *localcfg.Config, alias string, old []byte) (*localcfg.Config, []byte, error) {
	if _, ok := lc.ClaudeAccountAliases[alias]; !ok {
		return nil, nil, userErr("claude_account_aliases.%s is not defined in %s, so there is nothing to remove; `fugaro accounts ls` lists this machine's aliases", pluginwire.Printable(alias), path)
	}
	if keys := accountsAliasKeyNames(lc, alias); len(keys) > 0 {
		return nil, nil, userErr("claude_account_aliases.%s is named by %s, so removing it would leave %s resolving to a name this config does not define; point %s at the account %s should choose instead (`fugaro accounts alias set %s ACCOUNT`), or change %s, and remove the alias once nothing names it; nothing was written",
			pluginwire.Printable(alias), strings.Join(quoteKeys(keys), ", "), accountsAliasThem(keys),
			accountsAliasThem(keys), accountsAliasThey(keys), pluginwire.Printable(alias), accountsAliasThem(keys))
	}
	next, err := yamledit.RemoveKeyWithOptions(old, []string{"claude_account_aliases", alias}, yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		return nil, nil, userErr("claude_account_aliases.%s: %v", pluginwire.Printable(alias), err)
	}
	after, err := localcfg.Parse(next)
	if err != nil {
		return nil, nil, userErr("the local config this would write does not read back: %v", err)
	}
	return after, next, nil
}

// accountsAliasKeyNames is every account-choice key that names alias, spelled
// the way the file spells it: `default_claude_account`, and one
// `stages.<stage>.claude_account` per stage. The two are the whole of what may
// hold an alias (D2091, localcfg's choiceRefProblem) — a profile's
// claude_accounts list and agent.oauth_token may not.
func accountsAliasKeyNames(lc *localcfg.Config, alias string) []string {
	var out []string
	if lc.DefaultClaudeAccount == alias {
		out = append(out, "default_claude_account")
	}
	for _, stage := range slices.Sorted(maps.Keys(lc.ClaudeAccountStages)) {
		if lc.ClaudeAccountStages[stage].ClaudeAccount == alias {
			out = append(out, "stages."+stage+".claude_account")
		}
	}
	return out
}

// accountsAliasTargeting is every alias that stands for account, in name order:
// the aliases another command's edit of that account has to answer for (D2101,
// `accounts rm`) — an alias left standing for a name nothing defines is the file
// the loader refuses.
func accountsAliasTargeting(lc *localcfg.Config, account string) []string {
	var out []string
	for _, alias := range slices.Sorted(maps.Keys(lc.ClaudeAccountAliases)) {
		if lc.ClaudeAccountAliases[alias] == account {
			out = append(out, alias)
		}
	}
	return out
}

// quoteKeys backticks each key of a refusal, so the sentence a person reads
// separates the keys it is about from the prose around them.
func quoteKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, "`"+k+"`")
	}
	return out
}

// accountsAliasThem names the keys of a refusal in the plural, keeping the
// sentence right for the one-key and the several-key case alike.
func accountsAliasThem(keys []string) string {
	if len(keys) == 1 {
		return "it"
	}
	return "them"
}

// accountsAliasThey is the keys as the subject of their own clause. It is not
// accountsAliasThem: that one is the objective form the same refusals need for
// "leave them resolving", and a sentence that used it as a subject would read
// "the account them should choose".
func accountsAliasThey(keys []string) string {
	if len(keys) == 1 {
		return "it"
	}
	return "they"
}

// accountsAliasWhat is the sentence this run prints before the write: the one
// entry that moves, and the keys it moves for, so whoever reads the run knows
// which launches change and which do not.
func accountsAliasWhat(lc *localcfg.Config, alias, account string) string {
	was, defined := lc.ClaudeAccountAliases[alias]
	what := fmt.Sprintf("claude_account_aliases.%s = %s", pluginwire.Printable(alias), pluginwire.Printable(account))
	switch {
	case !defined:
		what = fmt.Sprintf("declare %s (a new alias)", what)
	case was == account:
		what = fmt.Sprintf("keep %s", what)
	default:
		what = fmt.Sprintf("move %s (from %s)", what, pluginwire.Printable(was))
	}
	if keys := accountsAliasKeyNames(lc, alias); len(keys) > 0 {
		what += fmt.Sprintf("; %s now choose %s", strings.Join(quoteKeys(keys), ", "), pluginwire.Printable(account))
	} else {
		what += "; no key names this alias yet"
	}
	what += "; publish the policy node and deploy the job set from the config this writes (new launches only: a run in flight keeps the account its launch resolved)"
	return what
}

// accountsAliasSetCommand and accountsAliasRmCommand are the commands a refusal
// prints for the person to run at their own terminal: the same edit, spelled
// whole, because nothing they type inside a session is remembered.
func accountsAliasSetCommand(alias, account string) string {
	return "fugaro accounts alias set " + quoteWord(alias) + " " + quoteWord(account)
}

func accountsAliasRmCommand(alias string) string {
	return "fugaro accounts alias rm " + quoteWord(alias)
}

// accountsAliasLogName is the name the two alias writes file their runs under:
// one converge serves both, so the name a person typed is built here rather
// than written at the constructor (report_exempt.go's builtLogNames).
func accountsAliasLogName(removing bool) string {
	if removing {
		return "accounts-alias-rm"
	}
	return "accounts-alias-set"
}

// accountsAliasDoor is the verb this command's own converge runs under: the door
// accountsAddRunStages names on every line it prints, and the fact
// initOptions.ownerHand reads to tell a run that IS the owner's hand at their
// terminal from one that is not (D2097).
const accountsAliasDoor = "accounts alias"

// ownerHand is whether this run's own command already IS the owner's hand at
// their terminal: `fugaro accounts alias set` and `accounts alias rm` refuse a
// coding agent's session before they read anything (accountsAliasSession,
// D2097), so the account choice their publish carries was made by the owner
// standing at their own terminal. The command they typed is that confirmation,
// and the typed name the publish asks of every other door would be a second one
// for the same decision — which the no-prompt policy does not have (D1540, F10).
func (o *initOptions) ownerHand() bool { return o.door == accountsAliasDoor }

// accountsAliasConverge is the publish and the deploy, through `accounts add`'s
// own runner over the registered stages (accountsAddRunStages): the node has to
// carry the account the keys now resolve to, and the job set has to mount that
// account's token, whichever command moved them.
func accountsAliasConverge(ctx context.Context, cmd *cobra.Command, after *localcfg.Config, wrote []byte, path, alias, target string, removing bool) error {
	run := stepRunFor(cmd, accountsAliasLogName(removing), cmd.ErrOrStderr())
	defer run.close()
	stages, err := accountsAddRunStages(ctx, cmd, run, after, path, wrote, accountsAliasDoor)
	if err != nil {
		return err
	}
	// The block the node carries is read back off the node and never off the
	// derivation the sentence named: this command's last line says there is
	// nothing left to run, and a line claiming a member the node does not carry
	// would be the one sentence it may not print.
	members, why := accountsAliasNode(ctx, after)
	accountsAliasReport(cmd, after, stages, alias, target, removing, members, why)
	return nil
}

// accountsAliasNode reads the published node's account-choice members back: what
// each of them resolves to now, which is what a new launch reads. why says why
// the node could not be read, and is "" when it was.
func accountsAliasNode(ctx context.Context, lc *localcfg.Config) (map[string]string, string) {
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, oneLine(err.Error())
	}
	_, found, raw, err := readPolicyNode(ctx, db, lc)
	switch {
	case err != nil:
		return nil, oneLine(err.Error())
	case !found:
		return nil, "nothing is published at " + projectpolicy.PathShared()
	}
	p, err := publishedPolicyOf(raw, found)
	if err != nil {
		return nil, oneLine(err.Error())
	}
	return policyAccountValues(p), ""
}

// accountsAliasReport is the command's answer: the entry that moved, the
// members the node now carries, what the installation did, and the one line that
// says what is left — which is nothing.
//
// target is the account the alias stood for, which the removal reads before the
// edit: the block no longer holds it once the file is written, and the job row
// and the closing line are about that account, not about the alias name.
func accountsAliasReport(cmd *cobra.Command, lc *localcfg.Config, stages map[string]initflow.StageResult, alias, target string, removed bool, members map[string]string, why string) {
	w := cmd.OutOrStdout()
	row := func(label, value, rest string) {
		fmt.Fprintf(w, "%-10s%-18s%s\n", label, pluginwire.Printable(value), rest)
	}
	if removed {
		row("alias", alias, "claude_account_aliases."+pluginwire.Printable(alias)+" removed")
	} else {
		row("alias", alias, "claude_account_aliases."+pluginwire.Printable(alias)+" = "+pluginwire.Printable(target))
	}
	// The jobs are the resolved account's, whichever alias moved: the job set is
	// per (profile, provider, account), so this is the same job `accounts add`
	// names for that account.
	profile := accountsAddProfileOf(lc, target)
	row("deploy", "jobs", accountsAddDeployLine(lc, profile, target, stages[initflow.Installation]))
	row("publish", "policy node", accountsAliasPublishLine(stages[initflow.Policy], members, why))
	fmt.Fprintln(w, accountsAliasLastLine(alias, target, removed))
}

// accountsAliasPublishLine is what the published node's account-choice members
// now resolve to, read back from the node. A stage that was skipped prints its
// own reason, and a node that could not be read prints the reader's.
func accountsAliasPublishLine(st initflow.StageResult, members map[string]string, why string) string {
	switch {
	case st.State == initflow.Skipped:
		return "skipped: " + oneLine(st.Detail)
	case why != "":
		return "not read: " + why
	case len(members) == 0:
		return "no account-choice member: no key of this config names an account, so the node carries none"
	}
	keys := slices.Sorted(maps.Keys(members))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" = "+pluginwire.Printable(members[k]))
	}
	return strings.Join(parts, "; ")
}

// accountsAliasLastLine is the command's last line: the thing the owner does
// next, which is nothing. It is a run and never an update, as `accounts add`'s
// is, and what it says about a run already going is the half of D2094 the owner
// needs: a repoint moves NEW launches, and a run in flight keeps the account its
// launch resolved.
func accountsAliasLastLine(alias, target string, removed bool) string {
	if removed {
		return fmt.Sprintf("Nothing else to run: %s is gone from this machine's config, and the account %s named is untouched.\n",
			pluginwire.Printable(alias), pluginwire.Printable(target))
	}
	return fmt.Sprintf("Nothing else to run: new launches resolve %s to %s; a run already going keeps the account its launch resolved.\n",
		pluginwire.Printable(alias), pluginwire.Printable(target))
}
