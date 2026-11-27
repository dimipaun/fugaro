package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro accounts craft`, `fugaro accounts grade` and `fugaro
// accounts repo`: the three semantic commands D2613 gives the accounts family
// for the leaves only a person may write (docs/config-ownership, A2).
//
// Why they exist. A craft's `access`, a grade's `access` and a repository's own
// `claude_accounts` list are grants: each one names a credential or a pool whose
// spend the named stage or the named repository's runs may carry, and each one
// is read by every run of the installation. The generic settings path refuses
// the rows for that reason (settings_set.go), and before these commands the only
// way to write one was a hand edit of the file — which is what the owner's own
// direction item 8 ("no field is manual-only without a stated reason") and
// D2613 answer with a command.
//
// The three writes are the shared settings path's, exactly as `accounts pool`'s
// are (F4): the layer is the resolver's answer (T3), the document's own rules
// are settingsLayerGate's, and the write lands through applySettingsEdit, so the
// diff, the sentence, the backup and the fence are the ones every other settings
// write has.
//
// A widening write is `Typed` (D2617): each of the three grants one. A coding
// agent's session may not write one whatever the managed gate answers — the
// marker fence in applySettingsEdit is what refuses it, and on a repository's
// own file that fence is exactly the Typed one, since a session may write the
// file it could edit anyway — and at the owner's own terminal the write asks
// nothing and prints what it widens instead (D1540, D1552).
//
// Every write ends with the after-write hook (contract §3), handed the store it
// really wrote in rather than its row's: a local write owes the `policy` stage
// (the node serves the crafts and the derived `accounts` block), a project-layer
// write owes the `layer` stage, and a repository's own file is live when the
// branch is merged and owes nothing (D1564).
func newAccountsCraftCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "craft",
		Short: "Point a craft at the credential or pool its calls go out on",
		Long: `Point one craft of this installation at the credential or pool its stages spend.

The five crafts are coder, review, senior-review, security-review and
merge-judge (fugaro craft ls lists what each one resolves to today). The two
halves of a craft inherit separately: a craft that sets no access of its own
keeps the one it follows, and the project's default credential when nothing sets
one, so this command writes the one half and leaves the craft's model alone.

A craft is declared in this installation's own config, so it has that one layer:
--layer with any other layer is refused naming the one it does have.

A craft decides which credential a stage's calls go out on, so it is a widening
grant and this command says so: run it at your own terminal. A coding agent's
session is refused whatever the managed gate answers (D2617).`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown accounts craft subcommand; want set or rm (see fugaro accounts craft --help)")
		},
	}
	cmd.AddCommand(newAccountsCraftSetCmd(), newAccountsCraftRmCmd())
	return cmd
}

// accountsAccessOptions are the flags `accounts craft set` and `accounts grade
// set` share: the access being granted and the layer the write lands in. There
// is no --ask: the flag puts back the confirmation a managed project's gate
// would have stood in for, and neither command reads that gate's answer for
// anything but the fence below (accountsPoolWriteOptions says the same).
type accountsAccessOptions struct {
	access string
	layer  string
}

func newAccountsCraftSetCmd() *cobra.Command {
	var o accountsAccessOptions
	cmd := &cobra.Command{
		Use:   "set NAME --access CREDENTIAL|POOL",
		Short: "Retired: the project's routing table says which credential a craft's calls go out on",
		Long: `crafts.<NAME>.access is retired: which credential a craft's calls go out on is
the project's routing table now (docs/model-credentials-routing/spec.md), so
this command writes nothing and refuses, naming the command that does.

` + "`fugaro routing ls`" + ` shows the table and ` + "`fugaro routing set`" + ` writes the entry a craft's
model is served by; a subscription account has to sit in a pool first
(` + "`fugaro accounts pool add`" + `), because a pool is what a run spends a token through.

The two flags are kept for one release so that a line already written down
reaches this sentence rather than "unknown flag", and neither is read.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsCraftSet(cmd, args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.access, "access", "", "retired: the project's routing table says which credential a craft's calls go out on, and `fugaro routing set` writes the entry")
	cmd.Flags().StringVar(&o.layer, "layer", "", "retired with --access: a craft's access has no layer to write any more")
	return cmd
}

func runAccountsCraftSet(cmd *cobra.Command, name string, o accountsAccessOptions) error {
	if !slices.Contains(config.CraftNames, name) {
		return userErr("%s: a craft is one of %s", oneLine(name), strings.Join(config.CraftNames, ", "))
	}
	return accountsRetiredAccess("crafts." + name + ".access")
}

// accountsRetiredAccess is the refusal the three retired access writes share
// (MG4 of docs/model-credentials-routing/implementation-plan.md): a craft's
// access, a grade's access and the cascade's two members the chain replaced. The
// key is named, the write is refused, and the sentence names the command that
// owns it now — the project's routing table for which credential serves a model,
// and a pool for a subscription account, which may only be spent from one.
//
// It is one sentence and not three: all three commands refuse at their first
// line, before the file is read and before any gate, so a coding agent's session
// and a person at their terminal read the same answer, and neither is asked
// anything (D1540).
func accountsRetiredAccess(key string) error {
	return userErr("%s is retired: which credential reaches a model is the project's routing table now, so `fugaro routing set` writes the entry (`fugaro routing ls` shows it), and a subscription account has to sit in a pool first (`fugaro accounts pool add`); nothing was written", key)
}

func newAccountsCraftRmCmd() *cobra.Command {
	var o accountsAccessOptions
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Take a craft out of this project's config",
		Long: `Remove crafts.<NAME> from this installation's own config: the craft's model and
its access go together, because a craft is one block and half of it left behind
is a craft no layer states.

The removal is recorded beside the block, and the record is what takes the craft
off the published node (F22, D2607): silence in this file keeps the node's own
copy, so a craft taken away without the record would go on being served. A craft
of this installation's own config owes the policy stage, and this command runs
that stage before it returns, so the node stops stating the craft as this command
returns; a craft the file no longer states and the node no longer holds leaves the
record behind as nothing, and that same publish clears it.

Nothing here removes the credential the craft named: a credential is removed by
fugaro accounts rm, which clears the access of every craft that named it.

A craft is declared in this installation's own config, so it has that one layer:
--layer with any other layer is refused naming the one it does have.

Run it at your own terminal: this command changes which credential a stage's
calls go out on.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAccountsCraftRm(cmd, args[0], o) },
	}
	cmd.Flags().StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+"); a craft is this installation's own config, so local is the layer it has")
	return cmd
}

func runAccountsCraftRm(cmd *cobra.Command, name string, o accountsAccessOptions) error {
	if !slices.Contains(config.CraftNames, name) {
		return userErr("%s: a craft is one of %s", oneLine(name), strings.Join(config.CraftNames, ", "))
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if err := accessGateProject(cmd.Context(), lc, nil, "nothing was written", "removing a craft's access"); err != nil {
		return err
	}
	craft := lc.Crafts.Of(name)
	if craft == (config.Craft{}) && !slices.Contains(removedNames(lc).Crafts, name) {
		return userErr("crafts.%s is not set in %s, so there is nothing to remove; `fugaro craft ls` shows what each craft resolves to today", name, sel.Path)
	}
	key := "crafts." + name + ".access"
	row, ok := settings.Lookup(key)
	if !ok {
		return userErr("%s: this fugaro has no registry row for a craft's access, so it cannot say which layer it is written in; upgrade it or file a bug", key)
	}
	wo := settingsWriteOptions{layer: o.layer}
	target, err := settingsResolveTarget(cmd.Context(), row, wo, lc, sel)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	next, err := accountsCraftRmEdit(old, name)
	if err != nil {
		return err
	}
	what := fmt.Sprintf("remove crafts.%s and record it as removed, so this write's own publish takes it off the node", name)
	if craft.Access != "" {
		what += fmt.Sprintf(" (its access was %s, and the craft's stages fall back to what it follows)", pluginwire.Printable(craft.Access))
	}
	if craft.Model != "" {
		what += fmt.Sprintf(" (its model was %s too: a craft is one block)", pluginwire.Printable(craft.Model))
	}
	return accountsAccessWrite(cmd, lc, target, row, wo, key, settingsEdit{
		Path:    target.path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    target.kind,
		Command: "fugaro accounts craft rm " + quoteWord(name),
	})
}

// accountsCraftRmEdit is the bytes this write leaves: the craft's whole block
// taken away, and its name written into the removal record beside it (F22,
// D2607). The block goes whole rather than by its access leaf because a craft is
// one thing — its model and its access are the two halves of it, and the node
// stores both or neither — and the record is the only statement that the craft
// was taken away rather than never stated.
func accountsCraftRmEdit(old []byte, name string) ([]byte, error) {
	next, err := yamledit.RemoveKeyWithOptions(old, []string{"crafts", name},
		yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		return nil, userErr("crafts.%s: %v", name, err)
	}
	next, err = yamledit.AddListItem(next, []string{"node_removals", "crafts"}, name)
	if err != nil {
		return nil, userErr("node_removals.crafts: %v", err)
	}
	return next, nil
}

func newAccountsGradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grade",
		Short: "Point an escalation grade at the credential its rung's calls go out on",
		Long: `Point one of the escalation ladder's grades at the credential its rung's calls go
out on.

The two grades are strong and strongest (docs/model-escalation). A grade is a
rung of the ladder: when a run escalates to it, the model and the credential it
resolves are what serve the rest of that run, so its access is the credential the
escalated calls are spent on.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown accounts grade subcommand; want set (see fugaro accounts grade --help)")
		},
	}
	cmd.AddCommand(newAccountsGradeSetCmd())
	return cmd
}

func newAccountsGradeSetCmd() *cobra.Command {
	var o accountsAccessOptions
	cmd := &cobra.Command{
		Use:   "set strong|strongest --access CREDENTIAL|POOL",
		Short: "Retired: the project's routing table says which credential an escalation rung's calls go out on",
		Long: `model_grades.<GRADE>.access is retired: which credential serves a model is the
project's routing table now (docs/model-credentials-routing/spec.md), and a
rung's escalated calls are routed by the model they ask for like every other
call, so this command writes nothing and refuses, naming the command that does.

` + "`fugaro routing ls`" + ` shows the table and ` + "`fugaro routing set`" + ` writes the entry the grade's
model is served by; a subscription account has to sit in a pool first
(` + "`fugaro accounts pool add`" + `).

A grade's own model (model_grades.<GRADE>.model) is unchanged by this: it says
which model the rung runs, and the table says which credential reaches it.

The two flags are kept for one release so that a line already written down
reaches this sentence rather than "unknown flag", and neither is read.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsGradeSet(cmd, args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.access, "access", "", "retired: the project's routing table says which credential an escalated call goes out on, and `fugaro routing set` writes the entry")
	cmd.Flags().StringVar(&o.layer, "layer", "", "retired with --access: a grade's access has no layer to write any more")
	return cmd
}

func runAccountsGradeSet(cmd *cobra.Command, grade string, o accountsAccessOptions) error {
	if !slices.Contains(config.GradeNames, grade) {
		return userErr("%s: a grade is one of %s", oneLine(grade), strings.Join(config.GradeNames, ", "))
	}
	return accountsRetiredAccess("model_grades." + grade + ".access")
}

func newAccountsRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Give a repository the Claude accounts its own runs may be served by",
		Long: `Give a repository of this project the accounts its own runs may be served by.

A repository's runs reach an account by two roads: the profile its jobs land on
names the accounts (fugaro accounts add does that as well), and a repository that
has no profile — every one a workflow sends to a per-repository job, and every
Bitbucket repository — names them in its own entry. This command writes the
second one.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown accounts repo subcommand; want add (see fugaro accounts repo --help)")
		},
	}
	cmd.AddCommand(newAccountsRepoAddCmd())
	return cmd
}

func newAccountsRepoAddCmd() *cobra.Command {
	var o accountsAccessOptions
	cmd := &cobra.Command{
		Use:   "add REPO ACCOUNT",
		Short: "Add an account to a repository's own claude_accounts list",
		Long: `Write repos.<REPO>.claude_accounts: the accounts a repository's own runs may be
served by, for the repositories no profile's job serves
(docs/project-claude-accounts.md §3).

An account named there must cover the repository — its own allow_data_to, or the
default account's project_secrets entry, must list it — and the publish that
derives the policy's accounts block refuses one that does not: add the repository
to the account's own list as well, or leave the account off this one.

The repository's entry must state its provider, because the published entry is
provider-qualified and this list is the only place a per-repository job's
provider is stated: an entry that names accounts and no provider is refused
before the file is touched. ` + "`fugaro update --repo .`" + ` writes the entry from a
checkout of the repository.

--layer picks the layer the write lands in. This list lives in this
installation's own config and in no fugaro.yaml and no layer file, so local is
the only layer it has, and --layer with any other is refused naming it.

Naming an account here widens what that repository's runs may spend, so it is a
widening write: a coding agent's session is refused whatever the managed gate
answers (D2617). Run it at your own terminal.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsRepoAdd(cmd, args[0], args[1], o)
		},
	}
	cmd.Flags().StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+"); this list is this installation's own config, so local is the layer it has")
	return cmd
}

func runAccountsRepoAdd(cmd *cobra.Command, repo, account string, o accountsAccessOptions) error {
	if !settingsRepoRE.MatchString(repo) {
		return userErr("repository %s: a repository is owner/name", oneLine(repo))
	}
	if msg := config.ValidClaudeAccountRef(account); msg != "" {
		return userErr("repos.%s.claude_accounts: %s", repo, msg)
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if err := accessGateProject(cmd.Context(), lc, nil, "nothing was written", "admitting a repository to an account"); err != nil {
		return err
	}
	// The axis's three non-account values are admitted without a declaration,
	// exactly as config.ValidClaudeAccountRef treats them: `default` names the
	// installation's own account of that name, and the project's Anthropic key
	// and the project's provider key name no account this config could declare
	// (config.AxisNonAccountValue), so a refusal would demand a declaration the
	// owner has no command to write. Only a name the config does not declare and
	// the axis does not own is the refusal below.
	if _, defined := lc.ClaudeAccounts[account]; !defined && account != config.DefaultClaudeAccount && !config.AxisNonAccountValue(account) {
		return userErr("repos.%s.claude_accounts: the account %s is not declared in %s, which declares %s; declare it with `fugaro accounts add %s` first",
			repo, pluginwire.Printable(account), sel.Path, accountsRepoKnownAccounts(lc), pluginwire.Printable(account))
	}
	entry, known := lc.Repos[repo]
	if !known {
		return userErr("repos.%s: this installation's config has no entry for that repository, and the entry a list like this lives in is the one `fugaro update --repo .` writes from a checkout of it: onboard the repository first; nothing was written", repo)
	}
	if entry.Provider == "" {
		return userErr("repos.%s states no provider, and an entry that names accounts is provider-qualified (owner/name is published as provider/owner/name): write provider on the entry (`fugaro update --repo .` writes it) and add the account once it is there; nothing was written", repo)
	}
	if slices.Contains(entry.ClaudeAccounts, account) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: repos.%s.claude_accounts already names %s\n", sel.Path, repo, pluginwire.Printable(account))
		return nil
	}
	key := "repos." + repo + ".claude_accounts"
	row, ok := accountsRepoRow(key)
	if !ok {
		return userErr("%s: this fugaro has no registry row for a repository's accounts list, so it cannot say which layer it is written in; upgrade it or file a bug", key)
	}
	wo := settingsWriteOptions{layer: o.layer}
	target, err := settingsResolveTarget(cmd.Context(), row, wo, lc, sel)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	next, err := yamledit.AddListItem(old, target.keyPath, account)
	if err != nil {
		return userErr("repos.%s.claude_accounts: %v", repo, err)
	}
	what := fmt.Sprintf("admit %s's own runs to the account %s: repos.%s.claude_accounts gains it", repo, pluginwire.Printable(account), repo)
	return accountsAccessWrite(cmd, lc, target, row, wo, key, settingsEdit{
		Path:     target.path,
		Old:      old,
		New:      next,
		What:     what,
		Kind:     target.kind,
		Command:  "fugaro accounts repo add " + quoteWord(repo) + " " + quoteWord(account),
		Typed:    true,
		TypedWhy: fmt.Sprintf("repos.%s.claude_accounts admits runs of that repository to an account's token, which no profile of this project admits them to", repo),
	})
}

// accountsRepoRow is the registry row of a repository's own accounts list at the
// concrete repository the command writes: the list is a wildcard row
// (repos.*.claude_accounts) because a repository is not a closed set, so the row
// is looked up at the name and, failing that, matched against the pattern and
// returned under the key the write is about — accountsPoolRow's rule.
func accountsRepoRow(key string) (settings.Row, bool) {
	if row, ok := settings.Lookup(key); ok {
		return row, true
	}
	for _, row := range settings.Rows() {
		if !strings.HasPrefix(row.Key, "repos.") || !settingsPatternMatch(row.Key, key) {
			continue
		}
		row.Key = key
		return row, true
	}
	return settings.Row{}, false
}

// accountsRepoKnownAccounts names the accounts the config declares, for the
// refusal that says a name is not one of them. A config that declares none says
// so rather than listing nothing, because the repair is `accounts add`.
func accountsRepoKnownAccounts(lc *localcfg.Config) string {
	names := slices.Sorted(maps.Keys(lc.ClaudeAccounts))
	if len(names) == 0 {
		return "no account at all"
	}
	return "the accounts " + strings.Join(names, ", ")
}

// accountsAccessWrite is the tail the three commands share: the layer's own
// gate, the shared edit with its fence and its diff, and the after-write hook
// handed the store the write really landed in (contract §3, §4).
//
// The store is the target's and never the row's: a grade's access has two layers
// and one row per layer, and the row that answers for the key is the one whose
// argv names the layer — so the hook is handed what was written, not what could
// have been. A repository's own file owes no stage (D1564: the branch merge is
// what makes it live) and the hook is asked anyway, because what a store owes is
// the hook's rule and not this command's; what the caller reads is the line that
// says the change is in a working tree and nowhere else.
func accountsAccessWrite(cmd *cobra.Command, lc *localcfg.Config, target settingsTarget, row settings.Row, o settingsWriteOptions, key string, e settingsEdit) error {
	// The edited bytes are read back by the document's own strict loader before
	// anything else (spec §4.2 step 3): a grant written into a file this Fugaro
	// cannot read is a file the owner's very next command refuses, and the
	// loader's sentence names the leaf where the pool's own write reads the same
	// way (accounts_pool.go).
	if err := settingsReparse(target, e.New); err != nil {
		return err
	}
	if err := settingsLayerGate(cmd, target, o, row.Key, e.Old, e.New); err != nil {
		return err
	}
	if err := applySettingsEdit(cmd, lc, e); err != nil {
		return err
	}
	if target.kind == settingsDocRepo {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: the change is in this file's working tree and nowhere else: review it, commit it on your own branch and merge that to the base branch — every run reads the base branch, and this command never commits or pushes\n", oneLine(target.path))
	}
	// The store is also what makes the sentence the caller printed true rather
	// than a promise about a command the owner would have to run: a key of this
	// machine's own config owes the policy stage (settings' layerSteps names that
	// layer's stage), the crafts block and the derived accounts block reach a run
	// off the published node and nowhere else, and the hook runs that stage before
	// this returns — so "the node stops stating the craft as this command returns"
	// is this write's own follow-up, and a stage that cannot leave the change live
	// is answered by the hook's own not-live sentence instead
	// (TestCraftWritesReachTheNode reads the node in a world that has one). A
	// stage that finds nothing to publish to — a project with no database the
	// node is read through — returns without a word, and no node serves the craft
	// there for the sentence to be false about; a node a credentials move is
	// holding mid-flight is the one state in which this command's sentence comes
	// true late, when the move lets the publish through.
	return runAfterWrite(cmd.Context(), cmd, afterWriteChange{Key: key, Store: accountsAccessStore(target)})
}

// accountsAccessStore is where a target's write lands, in the registry's own
// names: the shared store vocabulary is what the hook reads the steps off.
func accountsAccessStore(t settingsTarget) settings.Store {
	switch t.kind {
	case settingsDocLayer:
		return settings.StoreProject
	case settingsDocRepo:
		return settings.StoreRepo
	default:
		return settings.StoreLocal
	}
}
