package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro accounts pool`: the named pools of the local config
// (docs/model-crafts/spec.md §2). A pool is an ordered list of the credentials
// the same document defines, with a strategy — failover walks the list and
// round-robin shares the load — and a craft's access may name one wherever it
// may name a credential. Only the owner creates one, here or in the published
// node and nowhere a launcher can write: a pool is a spending and data-egress
// grant, exactly as a credential is (F12, D1628).
//
// The three commands are D1631's and the plan's MC5: `ls` reads, `add` declares
// the whole list, `rm` removes it. All three are offline: they read and write
// the installation's own config file and reach no bucket, no database and no
// host.
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

// accountsPoolOptions are the flags the pool writes take.
type accountsPoolOptions struct {
	cloud    cloudOptions
	strategy string
	yes      bool
	// ask is the write path's --ask, exactly as accounts add's is: the flag is
	// refused beside --yes and asks nothing where nobody could answer
	// (managed_ask.go). It is here because these are the same writes' family
	// and a person who learned the flag on one command must not lose it on the
	// next.
	ask bool
}

func (o *accountsPoolOptions) addWriteFlags(cmd *cobra.Command) {
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().StringVar(&o.strategy, "strategy", "", "how the pool walks its members: failover (the default: try them in order) or round-robin (share the load). Left out, a pool that exists keeps the strategy it has")
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	cmd.Flags().BoolVar(&o.ask, "ask", false, "ask the confirmation this write would otherwise get; refused where nobody can answer it (a coding agent's session, a pipe), and beside --yes")
}

func newAccountsPoolLsCmd() *cobra.Command {
	var o accountsPoolOptions
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
func runAccountsPoolLs(cmd *cobra.Command, o accountsPoolOptions) error {
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
	var o accountsPoolOptions
	cmd := &cobra.Command{
		Use:   "add NAME MEMBER...",
		Short: "Declare a pool with these members, in this order",
		Long: `Declare pools.<NAME> in this project: the credentials it walks, in the order it
walks them.

The members are credential names the same config defines (fugaro accounts ls
names the accounts; the Claude OAuth ones a credential entry each), and they are
written in the order given — failover tries them in that order and round-robin
starts from the first. A credential the config does not define, a repeated
member, a name this command may not use and a strategy that is not failover or
round-robin are all refused before the file is touched.

--strategy failover (the default) or round-robin. A pool that already exists
keeps the strategy it has when the flag is left out, and this command replaces
its members with the list on the command line: it is the whole list every time,
so a member you leave out is dropped.

Run it at your own terminal: a pool decides where a project's calls may be
spent and whose key they may carry, so a coding agent's session is refused,
--yes included.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsPoolAdd(cmd, args[0], args[1:], o)
		},
	}
	o.addWriteFlags(cmd)
	return cmd
}

// runAccountsPoolAdd is the order every settings write here takes: everything
// that can refuse does, and it refuses on the config this would write, before
// the file is read for the write at all.
func runAccountsPoolAdd(cmd *cobra.Command, name string, members []string, o accountsPoolOptions) error {
	if err := askConflict(askFlags{ask: o.ask, yes: o.yes}); err != nil {
		return err
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	// The pool this command would write, judged by the block's own rule
	// (config.ValidatePools) against the credentials this document defines: the
	// name, the strategy, the members and the namespace a credential shares
	// with a pool are one rule, and its sentences are the ones the loader
	// prints for the same block, so the two cannot part company.
	pool := config.Pool{Name: name, Strategy: o.strategy, Members: members}
	if ps := config.ValidatePools(accountsPoolNext(lc, pool), lc.Credentials); len(ps) > 0 {
		return userErr("%s", ps[0].Message)
	}
	command := accountsPoolAddCommand(name, members, o.strategy)
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next := old
	if o.strategy != "" {
		if next, err = yamledit.UpsertSeqItemByKey(next, []string{"pools"}, "name", name,
			map[string]string{"strategy": o.strategy}); err != nil {
			return userErr("pools.%s.strategy: %v", pluginwire.Printable(name), err)
		}
	}
	if next, err = yamledit.UpsertSeqItemListByKey(next, []string{"pools"}, "name", name, "members", members); err != nil {
		return userErr("pools.%s.members: %v", pluginwire.Printable(name), err)
	}
	if _, err := localcfg.Parse(next); err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	what := accountsPoolAddWhat(pool, o.strategy)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    settingsDocLocal,
		Command: command,
		Yes:     o.yes,
		Ask:     o.ask,
	}); err != nil {
		return err
	}
	return nil
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
// whole list, in order, and the strategy it will have.
func accountsPoolAddWhat(pool config.Pool, strategy string) string {
	what := fmt.Sprintf("declare pools.%s.members = [%s], in this order",
		pluginwire.Printable(pool.Name), strings.Join(pool.Members, ", "))
	if strategy != "" {
		return what + ", strategy " + strategy
	}
	return what + ", keeping the strategy the pool has (failover when it is new)"
}

func newAccountsPoolRmCmd() *cobra.Command {
	var o accountsPoolOptions
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a pool from this project's config",
		Long: `Remove pools.<NAME> from this project's config: the pool goes, and a craft whose
access names it is refused rather than left pointing at nothing.

It removes the pool from the installation's config only. A repository that
selected the pool keeps its own file untouched, and the pool the policy node
carries is what the next fugaro update publishes.

Run it at your own terminal: a pool decides where a project's calls may be
spent, so a coding agent's session is refused, --yes included.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAccountsPoolRm(cmd, args[0], o) },
	}
	o.addWriteFlags(cmd)
	return cmd
}

func runAccountsPoolRm(cmd *cobra.Command, name string, o accountsPoolOptions) error {
	if err := askConflict(askFlags{ask: o.ask, yes: o.yes}); err != nil {
		return err
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	pool, ok := accountsPoolOf(lc, name)
	if !ok {
		return userErr("pools.%s is not defined in %s, which defines %s: nothing was written",
			pluginwire.Printable(name), sel.Path, accountsPoolNames(lc.Pools))
	}
	// A craft that names the pool is refused rather than left pointing at
	// nothing: removing it would leave a config whose access resolves to no
	// credential at all, and the sentence names the craft to point elsewhere.
	for _, craft := range config.CraftNames {
		if access := lc.Crafts.Of(craft).Access; access != "" && access == name {
			return userErr("crafts.%s.access names pools.%s, so removing it would leave that craft with nothing to spend: point crafts.%s at another credential or pool first, then run this again; nothing was written",
				craft, pluginwire.Printable(name), craft)
		}
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next, err := yamledit.RemoveSeqItemByKeyWithOptions(old, []string{"pools"}, "name", name,
		yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		return userErr("pools.%s: %v", pluginwire.Printable(name), err)
	}
	if _, err := localcfg.Parse(next); err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	what := fmt.Sprintf("remove pools.%s (members %s)", pluginwire.Printable(name), strings.Join(pool.Members, ", "))
	command := "fugaro accounts pool rm " + quoteWord(name)
	return applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    settingsDocLocal,
		Command: command,
		Yes:     o.yes,
		Ask:     o.ask,
	})
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
func accountsPoolAddCommand(name string, members []string, strategy string) string {
	parts := []string{"fugaro", "accounts", "pool", "add", quoteWord(name)}
	for _, m := range members {
		parts = append(parts, quoteWord(m))
	}
	if strategy != "" {
		parts = append(parts, "--strategy", quoteWord(strategy))
	}
	return strings.Join(parts, " ")
}
