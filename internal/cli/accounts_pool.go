package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro accounts pool`: the named pools of the local config
// (docs/model-crafts/spec.md §2). A pool is an ordered list of the credentials
// the same document defines, with a strategy — failover walks the list and
// round-robin shares the load — and a craft's access may name one wherever it
// may name a credential. Only the owner creates one, in this installation's own
// config and nowhere a launcher can write: a pool is a spending and data-egress
// grant, exactly as a credential is (F12, D1628).
//
// The three commands are D1631's and the plan's MC5: `ls` reads, `add` declares
// the whole list and the people it is served to, `rm` removes it. All three are
// offline: they read and write the installation's own config file and reach no
// bucket, no database and no host.
//
// The two writes are the shared settings path's (settings plan F4): they resolve
// their layer through settingsResolveTarget, are judged by settingsLayerGate,
// and land through applySettingsEdit, so the diff, the sentence and the fence
// are the ones every other settings write has. A pool write is refused in a
// coding agent's session whatever that gate answers (F22, D2617): the document
// is the owner's own config, which the gate has no marks for, so a refusal that
// waited on it would be no refusal at all.
func newAccountsPoolCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "List, declare and remove the named pools a craft's access may name",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown accounts pool subcommand; want ls, add or rm (see fugaro accounts pool --help)")
		},
	}
	cmd.AddCommand(newAccountsPoolLsCmd(), newAccountsPoolAddCmd(), newAccountsPoolRmCmd())
	return cmd
}

// accountsPoolReadOptions are the flags `accounts pool ls` takes: the same
// cloud selection `craft ls` takes, because it reads the very same config that
// command reads. The writes below take none of them.
type accountsPoolReadOptions struct {
	cloud cloudOptions
}

// accountsPoolWriteOptions are the flags the two pool writes take, registered
// where each is used.
//
// Neither takes --config/--project, exactly as `models add` and `accounts rm`
// take none: the file they edit is the selected project's own config, which
// those commands resolve through settingsSelection and not through the cloud
// flags a read carries. Nor does either take --ask: that flag puts back a
// confirmation a managed project's gate would otherwise have stood in for
// (accounts add registers it because it reads that gate), and neither of these
// reads one.
//
// Neither takes --legacy-open either, and that is a decision rather than an
// omission (D2608): the marker says the pool predates owners and the published
// node reads it as open to every account of the installation, so the migration
// that grandfathers such a pool is its only writer. A creation flag for it would
// let a person declare a pool with no owner and no boundary at all.
type accountsPoolWriteOptions struct {
	strategy   string
	owner      string
	allowUsers []string
	layer      string
	yes        bool
}

func newAccountsPoolLsCmd() *cobra.Command {
	var o accountsPoolReadOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the named pools and the members each one walks",
		Long: `List the pools the installation's own config defines (pools, docs/model-crafts/spec.md §2): each pool's
strategy, its members in the order it walks them, and which half of a run
serves them.

An OpenRouter or Vertex member is served by a run's own gateway; a Claude OAuth
or Anthropic-key member by the credentials service, which selects by tier
(OAuth before key: D154). A pool whose members need both is one that can only
fail over where the credential is chosen again, at a stage boundary.

The read is offline: it reads the selected project's config file and reaches no
bucket, no database and no host.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runAccountsPoolLs(cmd, o) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// runAccountsPoolLs reads the same selected project config `fugaro craft ls`
// reads (craftLsConfig): the pools are the installation's own answer, and the
// selection rules — an explicit --project/--config, the environment, the
// checkout's own project — are the same ones every other offline read resolves.
func runAccountsPoolLs(cmd *cobra.Command, o accountsPoolReadOptions) error {
	lc, err := craftLsConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	var pools []config.Pool
	var creds []config.Credential
	if lc != nil {
		pools, creds = lc.Pools, lc.Credentials
	}
	return writeAccountsPoolLs(cmd.OutOrStdout(), lc, pools, creds)
}

func writeAccountsPoolLs(w interface {
	Write([]byte) (int, error)
}, lc *localcfg.Config, pools []config.Pool, creds []config.Credential) error {
	if lc == nil {
		fmt.Fprintln(w, "pools: no project config selected, so no pool is defined")
		return nil
	}
	fmt.Fprintf(w, "pools for project %s (offline)\n", oneLine(lc.Name))
	fmt.Fprintln(w)
	if len(pools) == 0 {
		fmt.Fprintln(w, "no pools are defined: a craft's access may name a credential on its own, or a pool you declare with fugaro accounts pool add")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTRATEGY\tMEMBERS\tSERVED BY")
	for _, p := range pools {
		paths, unplaced := config.PoolServingPaths(p, creds)
		served := accountsPoolServingText(paths)
		if len(unplaced) > 0 {
			served += fmt.Sprintf(" (no path serves %s)", strings.Join(unplaced, ", "))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			cellOneLine(p.Name), cellOneLine(p.StrategyOrDefault()),
			cellOneLine(strings.Join(p.Members, ", ")), served)
	}
	return tw.Flush()
}

// accountsPoolServingText is the path a pool's members are served by, as a
// person reads it: the two paths by name, and the boundary a pool of both kinds
// fails over at (D1634). A pool with no members at all — a document the loader
// refuses, which this read still renders rather than hiding — says nothing is
// served.
func accountsPoolServingText(paths []string) string {
	var out []string
	for _, p := range paths {
		switch p {
		case config.CredentialPathCredsvc:
			out = append(out, "the credentials service")
		case config.CredentialPathRunner:
			out = append(out, "a run's own gateway")
		default:
			out = append(out, p)
		}
	}
	switch len(out) {
	case 0:
		return "nothing"
	case 1:
		return out[0]
	default:
		return strings.Join(out, " and ") + ": failover at a stage boundary"
	}
}

func newAccountsPoolAddCmd() *cobra.Command {
	var o accountsPoolWriteOptions
	cmd := &cobra.Command{
		Use:   "add NAME [MEMBER...]",
		Short: "Declare a pool with these members, in this order",
		Long: `Declare pools.<NAME> in this project: the credentials it walks, in the order it
walks them, and the people it is served to.

The members are credential names the same config defines (fugaro accounts ls
names the accounts; the Claude OAuth ones a credential entry each), and they are
written in the order given — failover tries them in that order and round-robin
starts from the first. A credential the config does not define, a repeated
member, a name this command may not use and a strategy that is not failover or
round-robin are all refused before the file is touched. A member list given on
the command line is the whole list every time, so a member you leave out is
dropped; leave the list off entirely and a pool that exists keeps the members it
has.

--strategy failover (the default) or round-robin. A pool that already exists
keeps the strategy it has when the flag is left out.

--owner EMAIL names the person the pool is served to, and --allow-user EMAIL,
which may be repeated, names the others. The owner is required of a pool that is
published to the policy node, and it is what a craft's access is checked
against: without one, only the pool's own installation may use it. An address
that is not an email, an address listed twice and an owner given to a pool the
migration marked legacy-open are all refused before the file is touched.

--layer picks the layer the write lands in. A pool is declared in this
installation's own config, so the only layer it has is local, and --layer with
any other layer is refused naming the one it does have.

Run it at your own terminal: a pool decides where a project's calls may be
spent and whose key they may carry, so a coding agent's session is refused.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsPoolAdd(cmd, args[0], args[1:], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.strategy, "strategy", "", "how the pool walks its members: failover (the default: try them in order) or round-robin (share the load). Left out, a pool that exists keeps the strategy it has")
	f.StringVar(&o.owner, "owner", "", "the person the pool is served to, by email: the owner a craft's access is checked against. Left out, a pool that exists keeps the owner it has")
	f.StringArrayVar(&o.allowUsers, "allow-user", nil, "another person the pool is served to, by email; repeat the flag for each. Left out, a pool that exists keeps the users it has")
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	return cmd
}

// runAccountsPoolAdd is the order every settings write here takes: everything
// that can refuse does, and it refuses on the config this would write, before
// the file is read for the write at all. The layer is the shared resolver's
// answer and the document's own rules are the shared gate's (F4, T3), so the
// pool's two writes ask for a layer exactly as `settings set` does.
func runAccountsPoolAdd(cmd *cobra.Command, name string, members []string, o accountsPoolWriteOptions) error {
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if err := accessGateProject(cmd.Context(), lc, nil, "nothing was written", "declaring a model pool"); err != nil {
		return err
	}
	pool, exists := accountsPoolOf(lc, name)
	if !exists {
		if len(members) == 0 {
			// The list on the command line is the whole of what this command
			// declares (D1631), and a pool with no members serves nothing, so a
			// new one has to state them: the block's own rule would refuse the
			// empty list anyway, and this says which of the two things went
			// wrong.
			return userErr("pools.%s: a new pool states the members it walks; list at least one credential, or name a pool this config already defines", pluginwire.Printable(name))
		}
		pool = config.Pool{Name: name}
	}
	// The flags that were given are the fields they name, and the fields they
	// leave out are the pool's own: a pool is judged as it would be written,
	// by the block's rule (config.ValidatePools) against the credentials this
	// document defines, and its sentences are the ones the loader prints for
	// the same block, so the two cannot part company.
	if len(members) > 0 {
		pool.Members = members
	}
	if o.strategy != "" {
		pool.Strategy = o.strategy
	}
	if o.owner != "" {
		pool.Owner = o.owner
	}
	if len(o.allowUsers) > 0 {
		pool.AllowedUsers = o.allowUsers
	}
	if ps := config.ValidatePools(accountsPoolNext(lc, pool), lc.Credentials); len(ps) > 0 {
		return userErr("%s", ps[0].Message)
	}
	key := accountsPoolKey(name, "members")
	row, ok := accountsPoolRow(key)
	if !ok {
		return userErr("%s: this fugaro has no registry row for a pool's members, so it cannot say which layer it is written in; upgrade it or file a bug", key)
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
	next, err := accountsPoolAddEdit(old, name, members, o)
	if err != nil {
		return err
	}
	if _, err := localcfg.Parse(next); err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	if err := settingsLayerGate(cmd, target, wo, key, old, next); err != nil {
		return err
	}
	command := accountsPoolAddCommand(name, members, o)
	return accountsPoolWrite(cmd, lc, settingsEdit{
		Path:    target.path,
		Old:     old,
		New:     next,
		What:    accountsPoolAddWhat(pool, o),
		Kind:    target.kind,
		Command: command,
		Yes:     o.yes,
	}, key)
}

// accountsPoolAddEdit is the bytes this write leaves: the pool's line created
// where it is new and its fields rewritten where it is not, each field by the
// leaf that holds it, so a flag left out is a field left alone. The order the
// calls are made in is the order the fields read in a new pool.
func accountsPoolAddEdit(old []byte, name string, members []string, o accountsPoolWriteOptions) ([]byte, error) {
	next := old
	var err error
	if o.strategy != "" {
		if next, err = yamledit.UpsertSeqItemByKey(next, []string{"pools"}, "name", name,
			map[string]string{"strategy": o.strategy}); err != nil {
			return nil, userErr("pools.%s.strategy: %v", pluginwire.Printable(name), err)
		}
	}
	if len(members) > 0 {
		if next, err = yamledit.UpsertSeqItemListByKey(next, []string{"pools"}, "name", name, "members", members); err != nil {
			return nil, userErr("pools.%s.members: %v", pluginwire.Printable(name), err)
		}
	}
	if o.owner != "" {
		if next, err = yamledit.UpsertSeqItemByKey(next, []string{"pools"}, "name", name,
			map[string]string{"owner": o.owner}); err != nil {
			return nil, userErr("pools.%s.owner: %v", pluginwire.Printable(name), err)
		}
	}
	if len(o.allowUsers) > 0 {
		if next, err = yamledit.UpsertSeqItemListByKey(next, []string{"pools"}, "name", name, "allowed_users", o.allowUsers); err != nil {
			return nil, userErr("pools.%s.allowed_users: %v", pluginwire.Printable(name), err)
		}
	}
	return next, nil
}

// accountsPoolNext is the pools block this write would leave behind: the pool
// it writes, in the place the one it replaces held, and every other pool where
// it is.
func accountsPoolNext(lc *localcfg.Config, pool config.Pool) []config.Pool {
	out := make([]config.Pool, 0, len(lc.Pools)+1)
	replaced := false
	for _, p := range lc.Pools {
		if p.Name == pool.Name {
			out, replaced = append(out, pool), true
			continue
		}
		out = append(out, p)
	}
	if !replaced {
		out = append(out, pool)
	}
	return out
}

// accountsPoolAddWhat is the sentence naming what this write does (D1540): the
// whole list, in order, the strategy it will have, and the people it is served
// to where the flags named them.
func accountsPoolAddWhat(pool config.Pool, o accountsPoolWriteOptions) string {
	what := fmt.Sprintf("declare pools.%s.members = [%s], in this order",
		pluginwire.Printable(pool.Name), strings.Join(pool.Members, ", "))
	if o.strategy != "" {
		what += ", strategy " + o.strategy
	} else {
		what += ", keeping the strategy the pool has (failover when it is new)"
	}
	if o.owner != "" {
		what += ", owner " + pluginwire.Printable(o.owner)
	}
	if len(o.allowUsers) > 0 {
		what += ", allowed users " + strings.Join(o.allowUsers, ", ")
	}
	return what
}

func newAccountsPoolRmCmd() *cobra.Command {
	var o accountsPoolWriteOptions
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a pool from this project's config",
		Long: `Remove pools.<NAME> from this project's config: the pool goes, and a craft whose
access names it is cleared with it rather than left pointing at nothing.

A pool is declared in this installation's own config, and this command removes
the declaration from that file. A repository that selected the pool keeps its
own file untouched.

--layer picks the layer the removal lands in. A pool is declared in this
installation's own config, so the only layer it has is local, and --layer with
any other layer is refused naming the one it does have.

Run it at your own terminal: a pool decides where a project's calls may be
spent, so a coding agent's session is refused.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAccountsPoolRm(cmd, args[0], o) },
	}
	cmd.Flags().StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	return cmd
}

func runAccountsPoolRm(cmd *cobra.Command, name string, o accountsPoolWriteOptions) error {
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if err := accessGateProject(cmd.Context(), lc, nil, "nothing was written", "removing a model pool"); err != nil {
		return err
	}
	pool, ok := accountsPoolOf(lc, name)
	if !ok {
		return userErr("pools.%s is not defined in %s, which defines %s: nothing was written",
			pluginwire.Printable(name), sel.Path, accountsPoolNames(lc.Pools))
	}
	key := accountsPoolKey(name, "name")
	row, ok := accountsPoolRow(key)
	if !ok {
		return userErr("%s: this fugaro has no registry row for a pool's name, so it cannot say which layer it is written in; upgrade it or file a bug", key)
	}
	wo := settingsWriteOptions{layer: o.layer}
	target, err := settingsResolveTarget(cmd.Context(), row, wo, lc, sel)
	if err != nil {
		return err
	}
	// A craft whose access names the pool is cleared with it, on the same bytes:
	// the pool is going, so an access left behind resolves to no credential at
	// all, and the key is one no command of this release writes — the migration
	// is its only writer (init_craft_migrate.go) and the generic path refuses a
	// family-owned row (settings_set.go) — so a refusal would name a step the
	// owner cannot take. It is the treatment `accounts rm` gives the crafts that
	// named the typed entry it removes (craftsNaming).
	cleared := craftsNaming(lc, name)
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	next, err := yamledit.RemoveSeqItemByKeyWithOptions(old, []string{"pools"}, "name", name,
		yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		return userErr("pools.%s: %v", pluginwire.Printable(name), err)
	}
	for _, craft := range cleared {
		n, err := yamledit.RemoveKeyWithOptions(next, []string{"crafts", craft, "access"},
			yamledit.RemoveOptions{DropEmptyParents: true})
		if err != nil {
			return userErr("crafts.%s.access: %v", craft, err)
		}
		next = n
	}
	if _, err := localcfg.Parse(next); err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	if err := settingsLayerGate(cmd, target, wo, key, old, next); err != nil {
		return err
	}
	what := fmt.Sprintf("remove pools.%s (members %s)", pluginwire.Printable(name), strings.Join(pool.Members, ", "))
	if len(cleared) > 0 {
		what += fmt.Sprintf("; clear %s, which named it and would have nothing to spend without it", craftAccessKeys(cleared))
	}
	command := "fugaro accounts pool rm " + quoteWord(name)
	return accountsPoolWrite(cmd, lc, settingsEdit{
		Path:    target.path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    target.kind,
		Command: command,
		Yes:     o.yes,
	}, key)
}

// accountsPoolWrite is the write both pool commands make: the fence, then the
// edit, then the after-write hook.
//
// The fence is the pools' own and it is unconditional (F22, D2617). A pool
// decides where a project's calls may be spent and whose key they may carry, so
// a coding agent's session writes neither the pool nor its removal, --yes
// included. applySettingsEdit's fence cannot stand for it: the document is the
// owner's own local config, which no managed project's gate holds marks for, so
// the gate's answer there is always the zero value and a fence read off it
// would refuse every write for a reason that is not this one — and would stop
// refusing the moment a gate admitted. The refusal prints what it refused and
// names the command to run at a terminal, in applySettingsEdit's own words.
func accountsPoolWrite(cmd *cobra.Command, lc *localcfg.Config, e settingsEdit, key string) error {
	if marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); marker != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n%s", e.Path, settingsEditDiff(e.Old, e.New))
		return userErr("refusing to write %s in a coding agent's session (%s is set): %s; run this yourself at your own terminal: %s; nothing was written",
			e.Path, marker, e.What, e.Command)
	}
	if err := applySettingsEdit(cmd, lc, e); err != nil {
		return err
	}
	return runAfterWrite(cmd.Context(), cmd, afterWriteChange{Key: key, Store: settings.StoreLocal})
}

// accountsPoolKey is one leaf of one pool, spelled the way the registry spells
// it: pools.<name>.<leaf>.
func accountsPoolKey(name, leaf string) string {
	return "pools." + name + "." + leaf
}

// accountsPoolRow is the registry row of a pool leaf, at the concrete name the
// command writes. The registry holds the leaves under their pattern — pools.*.name
// and its siblings — because a pool is a list item and its name is the item's
// own key, so the row is looked up at the name and, failing that, matched
// against the pool patterns and returned under the key the write is about.
func accountsPoolRow(key string) (settings.Row, bool) {
	if row, ok := settings.Lookup(key); ok {
		return row, true
	}
	for _, row := range settings.Rows() {
		if !strings.HasPrefix(row.Key, "pools.") || !settingsPatternMatch(row.Key, key) {
			continue
		}
		row.Key = key
		return row, true
	}
	return settings.Row{}, false
}

// accountsPoolOf finds a pool by name, and reports whether the block holds one.
func accountsPoolOf(lc *localcfg.Config, name string) (config.Pool, bool) {
	for _, p := range lc.Pools {
		if p.Name == name {
			return p, true
		}
	}
	return config.Pool{}, false
}

// accountsPoolNames names the pools a config defines for a refusal, bounded and
// quoted, because a name comes from a file and this text goes to a terminal.
func accountsPoolNames(pools []config.Pool) string {
	out := make([]string, 0, len(pools))
	for _, p := range pools {
		out = append(out, pluginwire.Printable(p.Name))
	}
	if len(out) == 0 {
		return "no pool"
	}
	return strings.Join(out, ", ")
}

// accountsPoolAddCommand is the command a refusal in a coding agent's session
// prints for the person to run at their own terminal: this same write, spelled
// whole.
func accountsPoolAddCommand(name string, members []string, o accountsPoolWriteOptions) string {
	parts := []string{"fugaro", "accounts", "pool", "add", quoteWord(name)}
	for _, m := range members {
		parts = append(parts, quoteWord(m))
	}
	if o.strategy != "" {
		parts = append(parts, "--strategy", quoteWord(o.strategy))
	}
	if o.owner != "" {
		parts = append(parts, "--owner", quoteWord(o.owner))
	}
	for _, u := range o.allowUsers {
		parts = append(parts, "--allow-user", quoteWord(u))
	}
	return strings.Join(parts, " ")
}
