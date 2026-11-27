package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/routing"
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
// the whole list and the people it is served to, `rm` removes it. They read and
// write the installation's own config file and reach no bucket, no database and
// no host — except `ls --json`, whose figures are the credentials service's and
// are read from the project's database as the person running it (PL10).
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
			return userErr("unknown accounts pool subcommand; want ls, add, rm, designate, contribute, withdraw or rm-member (see fugaro accounts pool --help)")
		},
	}
	cmd.AddCommand(newAccountsPoolLsCmd(), newAccountsPoolAddCmd(), newAccountsPoolRmCmd(),
		newAccountsPoolDesignateCmd(), newAccountsPoolContributeCmd(),
		newAccountsPoolWithdrawCmd(), newAccountsPoolRmMemberCmd())
	return cmd
}

// accountsPoolReadOptions are the flags `accounts pool ls` takes: the same
// cloud selection `craft ls` takes, because it reads the very same config that
// command reads. The writes below take none of them.
type accountsPoolReadOptions struct {
	cloud cloudOptions
	// json is --json: the document of C2's contract
	// (docs/model-credentials-routing/contract-pools.md), which the console's
	// pools screen draws (VS2) and whose figures PL10 counts. It carries names
	// and figures and never a value (F19).
	json bool
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
	strategy string
	// rule is --rule: the pool's own rule (PL1's member, R5). It is written
	// beside --strategy and never instead of it: `strategy` keeps its meaning
	// for a pool that names one (R21), and RuleOrDefault reads the rule first.
	rule string
	// serviceAccount is --service-account: the address of the service account
	// the pool belongs to, written as the pool's principal (config.PoolPrincipal,
	// kind service-account). It is the enforcing half of a pool's ownership, the
	// one a run's verified principal is matched against (D3181, R7).
	serviceAccount string
	owner          string
	allowUsers     []string
	layer          string
	yes            bool
	// personal is --personal (PL9): the pool is the caller's OWN, kept at the
	// installation's credentials service and served to the runs that caller
	// launches, rather than an entry of this project's config. It carries none
	// of the config flags above, because none of them has a field in that
	// record, and it is refused in a coding agent's session with the rest of
	// them.
	personal bool
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
(OAuth before key: D154). A pool is served on one of those halves and may not
list members of both: a gateway holds one upstream per stage and the service
selects by tier, so neither can walk an order that spans them, and a pool that
names both is refused when it is declared. Name two pools, one per half, and let
each craft's access name the one its stages need.

The table is offline: it reads the selected project's config file and reaches no
bucket, no database and no host.

--json prints the document the console's pools screen reads
(docs/model-credentials-routing/contract-pools.md): the project, its pools and
each pool's members, with the state, the next reset, the usage and the headroom
of each member. The figures are the credentials service's own — the calls and
tokens each member has served in the window it counts, the instant a mark frees
a member, or the time its leases lapse, and what is left of its limit — so this
half reads the project's database as the person running it, and a project with
no database, or one whose read fails, prints the pools with the figures absent
and says why. The read writes nothing, the leases included: they are read
without the prune their own read makes. Headroom is there only where a provider
reported a utilisation figure: it is never guessed. No token, no key and no
secret appears in the document, for anyone: it carries names and figures and
never a value (F19).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runAccountsPoolLs(cmd, o) },
	}
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().BoolVar(&o.json, "json", false, "print the machine-readable document instead of the table: the project, its pools, and each pool's members with their state, next reset, usage and headroom (never a value)")
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
	var credentials []config.Credential
	if lc != nil {
		pools, credentials = lc.Pools, lc.Credentials
	}
	if !o.json {
		return writeAccountsPoolLs(cmd.OutOrStdout(), lc, pools, credentials)
	}
	figures, note, err := accountsPoolFiguresOf(cmd.Context(), lc, pools)
	if err != nil {
		return err
	}
	if note != "" {
		// The note goes to stderr and never into the document: the document is
		// C2's shape, which has no member for it, and a note inside it would be
		// a member every reader of the contract refuses (its schema).
		fmt.Fprintln(cmd.ErrOrStderr(), note)
	}
	return writeAccountsPoolLsJSON(cmd.OutOrStdout(), lc, pools, figures)
}

// accountsPoolFigures is what the service holds about one member: the state its
// mark is in, the instant that mark — or the member's leases — frees it, and the
// usage of the window the service counts.
type accountsPoolFigures struct {
	state     creds.State
	nextReset time.Time
	usage     creds.Usage
	// counted is whether the service has counted anything for this member at
	// all, which is what tells a member with no figures from one whose figures
	// are zero — the same distinction the node's own absence carries (creds.Usage).
	counted bool
}

// accountsPoolFiguresOf reads the figures of every member of these pools, and
// it is the read `fugaro accounts ls` makes: the same subtree (creds/), the same
// operator's identity (the person's ADC), and the same bound (accountsStateBudget).
//
// The note it answers is never a failure, for the reason accountsStates' is
// never one: the pools and their members are a local read and stay right whether
// or not a figure could be read, so a project with no budget database, a
// database that is down and a read the person's rules refuse all answer with the
// pools and a note. The one error is the refusal to open the cloud at all
// (refuseHTTP2Debug).
//
// The figures are display data and this is a read of it (F19): nothing here
// decides anything about a run, and nothing here is a value.
//
// Who may read them is not this command's to decide, and today only a
// database-admin identity does: the published rules close creds/ to every
// client, so a launcher's own ADC is refused this subtree unless it holds
// firebasedatabase.admin, and every other launcher gets the note with the
// figures absent — which is the honest answer and not a failure. The grant that
// would let a person read the figures of their own and the shared pools is
// still owed, and it is a credentials-rules generation of its own
// (budget/rules.CredsRulesVersion), not a line here.
func accountsPoolFiguresOf(ctx context.Context, lc *localcfg.Config, pools []config.Pool) (map[string]accountsPoolFigures, string, error) {
	members := accountsPoolMembers(pools)
	if lc == nil || len(members) == 0 {
		return nil, "", nil
	}
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, fmt.Sprintf("project %s has no Firebase budget backend, so no usage could be read (the figures live in its database; fugaro update --firebase-project %s adds it)", lc.Name, firebaseProjectIDWord(lc.GCPProject)), nil
	}
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, accountsStateBudget)
	defer cancel()
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, figuresUnread(err), nil
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, figuresUnread(err), nil
	}
	store := &creds.RTDBStore{DB: db}
	marks, err := store.Marks(ctx)
	if err != nil {
		return nil, figuresUnread(err), nil
	}
	usage, err := store.Usages(ctx)
	if err != nil {
		return nil, figuresUnread(err), nil
	}
	// The leases are read without the prune their own read makes
	// (creds.RTDBStore.LeasesUnpruned, and not Leases): this command is a read,
	// and the service is the credentials database's only writer. They are read
	// because the reset the contract promises is a mark's own instant, or the
	// time the member's leases lapse.
	leases, err := store.LeasesUnpruned(ctx)
	if err != nil {
		return nil, figuresUnread(err), nil
	}
	now := time.Now()
	out := make(map[string]accountsPoolFigures, len(members))
	for _, name := range members {
		f := accountsPoolFigures{state: creds.StateAvailable}
		if m, ok := marks[name]; ok && m.State != "" {
			f.state = m.State
			if !m.Until.IsZero() {
				f.nextReset = m.Until
			}
		}
		if f.nextReset.IsZero() {
			if at, ok := earliestLeaseLapse(leases, name, now); ok {
				f.nextReset = at
			}
		}
		if u, ok := usage[name]; ok {
			f.usage, f.counted = u, true
		}
		out[name] = f
	}
	return out, "", nil
}

// earliestLeaseLapse is when a member's live leases lapse: the earliest of
// them, which is the first instant one of its holders lets go, and whether any
// holds it at all. A lapsed lease holds nothing (creds.Lease.Live), so it is
// not a reset to wait for.
func earliestLeaseLapse(leases []creds.Lease, name string, now time.Time) (time.Time, bool) {
	var best time.Time
	for _, l := range leases {
		if l.Credential != name || !l.Live(now) {
			continue
		}
		if best.IsZero() || l.Until.Before(best) {
			best = l.Until
		}
	}
	return best, !best.IsZero()
}

// figuresUnread is the note a read that could not be made answers with: the
// reason, and nothing about any member, because nothing was read.
func figuresUnread(err error) string {
	return fmt.Sprintf("no usage could be read within %s: %v", accountsStateBudget, err)
}

// accountsPoolMembers is every member of every pool, once each, in the order the
// config lists them: the members whose figures the read asks for.
func accountsPoolMembers(pools []config.Pool) []string {
	var out []string
	seen := make(map[string]bool)
	for _, p := range pools {
		for _, m := range p.Members {
			if seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// writeAccountsPoolLsJSON is the document C2's contract declares
// (routing.PoolListDoc): the project, its pools and each pool's members, so that
// three sides print, fill and read one shape rather than three. The figures are
// what the service counted (PL10), and a member nothing was counted for is the
// word a member nothing holds has (creds.StateAvailable) with its reset, its
// usage and its headroom null — absent, and never invented. A project with no
// pool prints an empty list and never a null (PoolListDoc.MarshalJSON says why).
func writeAccountsPoolLsJSON(w io.Writer, lc *localcfg.Config, pools []config.Pool, figures map[string]accountsPoolFigures) error {
	doc := routing.PoolListDoc{Project: "", Pools: []routing.PoolEntry{}}
	if lc != nil {
		doc.Project = lc.Name
	}
	for _, p := range pools {
		entry := routing.PoolEntry{
			Name:      p.Name,
			Rule:      routing.Rule(p.Rule),
			Members:   []routing.PoolMember{},
			Threshold: nil,
		}
		if p.Principal != nil {
			entry.Principal = routing.Principal{Kind: routing.PrincipalKind(p.Principal.Kind), ID: p.Principal.ID}
		}
		if p.Threshold != nil {
			t := routing.Threshold(*p.Threshold)
			entry.Threshold = &t
		}
		for _, m := range p.Members {
			member := routing.PoolMember{Name: m, State: string(creds.StateAvailable)}
			if f, ok := figures[m]; ok {
				member.State = string(f.state)
				if !f.nextReset.IsZero() {
					at := f.nextReset.UTC()
					member.NextReset = &at
				}
				if f.counted {
					member.Usage = &routing.PoolUsage{Calls: f.usage.Calls, Tokens: f.usage.Tokens}
					member.Headroom = f.usage.Headroom()
				}
			}
			entry.Members = append(entry.Members, member)
		}
		doc.Pools = append(doc.Pools, entry)
	}
	return writeJSON(w, doc)
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
// person reads it. A pool this release writes is served on one path, because a
// pool that lists members of both is refused where it is written (D1668); a
// config an earlier release wrote may still hold one that names both, and both
// names are joined for it, with the boundary its failover is left at, rather
// than hidden from the person reading the file. A pool with no members at all
// says nothing is served.
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

A pool is served on one half of a run — the credentials service for a Claude
OAuth or Anthropic-key member, a run's own gateway for an OpenRouter route or
the built-in vertex — and neither half can walk an order that spans them, so a
list naming both halves is refused too: name two pools, one per half, and let
each craft's access name the one its stages need. A pool a config already holds
that names both is left as it is and declared beside, not rewritten here.

--strategy failover (the default) or round-robin. A pool that already exists
keeps the strategy it has when the flag is left out.

--rule ordered, round-robin or failover is how the service picks among the
members the pool can use (docs/model-credentials-routing/contract-pools.md):
ordered takes the first usable member in the order written, and the other two
keep the meanings the strategies of those names have. A pool that names no rule
is served by its strategy and, where it names neither, by failover, so a pool
written before rules existed moves only where its owner writes one.

--service-account ADDRESS names the service account this pool belongs to: the
address its runs' tokens carry, written as the pool's principal. It is the
enforcing half of who the pool's tokens are handed to and is not read off
--owner, which stays an email address and keeps its meaning.

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
	f.StringVar(&o.rule, "rule", "", "how the service picks among the members the pool can use ("+strings.Join(config.PoolRules, ", ")+"). Left out, a pool that exists keeps the rule it has and is served by its strategy")
	f.StringVar(&o.serviceAccount, "service-account", "", "the address of the service account this pool belongs to, written as the pool's principal")
	f.StringVar(&o.owner, "owner", "", "the person the pool is served to, by email: the owner a craft's access is checked against. Left out, a pool that exists keeps the owner it has")
	f.StringArrayVar(&o.allowUsers, "allow-user", nil, "another person the pool is served to, by email; repeat the flag for each. Left out, a pool that exists keeps the users it has")
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	f.BoolVar(&o.personal, "personal", false, "record the pool as YOUR OWN, at the installation's credentials service, rather than in this project's config: it is served to the runs you launch and to nobody else, its members are the names of containers of your own, and its flags are --rule and the member list alone")
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
	// A personal pool is not this project's config entry, so it is not the
	// admin gate's act and not this file's write: it is recorded at the
	// installation's credentials service, keyed on the person's own verified
	// subject (PL9, R26).
	if o.personal {
		return runAccountsPoolAddPersonal(cmd, name, members, o, lc)
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
	// --rule and --strategy are two spellings of one decision (R21): the rule
	// is the pool's own and the strategy is what a pool that names no rule is
	// served by, so a command that wrote both would leave a person reading the
	// file two answers to one question. The rule is the one to keep — it is the
	// spelling this release writes and the one a reader resolves first.
	if o.rule != "" {
		if o.strategy != "" {
			return userErr("--rule and --strategy decide the same thing, and a pool keeps one answer to it: --rule %s is the spelling this release writes and the one a reader resolves first, so drop --strategy; nothing was written", pluginwire.Printable(o.rule))
		}
		pool.Rule = o.rule
	}
	if o.serviceAccount != "" {
		pool.Principal = &config.PoolPrincipal{Kind: config.PrincipalServiceAccount, ID: o.serviceAccount}
	}
	if o.owner != "" {
		pool.Owner = o.owner
	}
	if len(o.allowUsers) > 0 {
		pool.AllowedUsers = o.allowUsers
	}
	pools := accountsPoolNext(lc, pool)
	if ps := config.ValidatePools(pools, lc.Credentials); len(ps) > 0 {
		return userErr("%s", ps[0].Message)
	}
	// And the rule no reader of a pool holds, because a pool an earlier release
	// wrote may already break it: this command is the one door a NEW member list
	// comes through, so it is the door that refuses to write a crossing
	// (config.ValidatePoolPaths, D1668). It judges the pool being written and not
	// the block it lands in: a pool 0.25.0 already wrote stays exactly as it is —
	// the split that converges it is MC21's — and a refusal over the whole block
	// would leave an installation holding one unable to declare the two pools the
	// sentence tells it to.
	if ps := config.ValidatePoolPaths(poolsWithNewMembers(pool, members), lc.Credentials); len(ps) > 0 {
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
	if o.rule != "" {
		if next, err = yamledit.UpsertSeqItemByKey(next, []string{"pools"}, "name", name,
			map[string]string{"rule": o.rule}); err != nil {
			return nil, userErr("pools.%s.rule: %v", pluginwire.Printable(name), err)
		}
	}
	if o.serviceAccount != "" {
		if next, err = yamledit.UpsertSeqItemMapByKey(next, []string{"pools"}, "name", name, "principal",
			map[string]string{"kind": config.PrincipalServiceAccount, "id": o.serviceAccount}); err != nil {
			return nil, userErr("pools.%s.principal: %v", pluginwire.Printable(name), err)
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
	// A pool declared here is not a removed one: the record a removal left is
	// cleared on the same bytes, or the reconcile would go on taking the pool
	// off the node the owner has just declared it for (F22). Nothing is
	// removed where the record does not name the pool.
	if next, err = yamledit.RemoveListItemWithOptions(next, []string{"node_removals", "pools"}, name,
		yamledit.RemoveOptions{DropEmptyParents: true}); err != nil {
		return nil, userErr("node_removals.pools: %v", err)
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
	if o.rule != "" {
		what += ", rule " + o.rule
	}
	if o.serviceAccount != "" {
		what += ", principal service-account " + pluginwire.Printable(o.serviceAccount)
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
the declaration from that file. The removal is recorded beside it, because the
published node keeps a pool the config says nothing about: the record is what
says the pool was taken away rather than never stated, and it is what a
reconcile acts on. fugaro update publishes the pools this file states and never
deletes a definition the file no longer states, and no reconcile is in this
release yet, so the removal withdraws nothing from the node: a node that
already carries the pool goes on serving it. A repository that selected the
pool keeps its own file untouched.

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
	// The removal is recorded on the same bytes (F22, D2607): a publish keeps a
	// node member the local config says nothing about, so a pool taken away
	// without the record would go on being served by the node and the
	// credentials it walks would go on being spent. The record is the only
	// statement that the pool was removed rather than never declared.
	if next, err = yamledit.AddListItem(next, []string{"node_removals", "pools"}, name); err != nil {
		return userErr("node_removals.pools: %v", err)
	}
	if _, err := localcfg.Parse(next); err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	if err := settingsLayerGate(cmd, target, wo, key, old, next); err != nil {
		return err
	}
	what := fmt.Sprintf("remove pools.%s (members %s) and record it as removed, for a reconcile that is not in this release: the node goes on serving it", pluginwire.Printable(name), strings.Join(pool.Members, ", "))
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
	if o.rule != "" {
		parts = append(parts, "--rule", quoteWord(o.rule))
	}
	if o.serviceAccount != "" {
		parts = append(parts, "--service-account", quoteWord(o.serviceAccount))
	}
	if o.owner != "" {
		parts = append(parts, "--owner", quoteWord(o.owner))
	}
	for _, u := range o.allowUsers {
		parts = append(parts, "--allow-user", quoteWord(u))
	}
	return strings.Join(parts, " ")
}

// poolsWithNewMembers is what the crossing rule judges: only a member list
// this command was given, because an owner-only edit of a pool an earlier
// release wrote must not be refused over the list it keeps.
func poolsWithNewMembers(pool config.Pool, members []string) []config.Pool {
	if len(members) == 0 {
		return nil
	}
	return []config.Pool{pool}
}
