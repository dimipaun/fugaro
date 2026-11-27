package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro accounts cascade`: the credentials cascade of
// docs/config-ownership, D2613's spelling of it
// (`fugaro accounts cascade set [--fallback ...]`).
//
// Why it exists. The node's `credentials` block decides what a run does when the
// credential it is calling with is exhausted: whether it waits for the shared
// token's reset and never spends the project's money, or falls over to the
// project's Anthropic key and the third party beyond it (D152), how long it
// waits before it fails over, and the backoff and the two owner-headroom
// controls of D142/D153. It is a block only an operator writes, and before this
// command the only door to it was a hand edit of the node itself — which the
// owner's direction item 8 ("no field is manual-only without a stated reason")
// and D2613 answer with a command.
//
// The carrier. The command writes this installation's own config, under the key
// `credential_cascade` N1 added (F23, D2607): the local `credentials` key is
// already the list of typed credentials, so the node's member takes a name of
// its own here, and N4's publish is what carries a written block to the node and
// keeps the node's where this file says nothing. That is why the keys this file
// edits are `credential_cascade.*` and not `credentials.*`, which is the
// spelling the writer registry registers as well, for the refusal
// (writers_cascade.go).
//
// The write is the shared settings path's, exactly as `accounts pool`'s and the
// crafts' are (F4): the layer is the resolver's answer (T3), the edited bytes
// are read back by the document's own loader before anything is written, and the
// write lands through applySettingsEdit, so the diff, the sentence, the backup
// and the fence are the ones every other settings write has.
//
// Two of the members are the owner's own step in one direction — D142's two
// controls written against their defaults — so a write in one of those
// directions is `Typed` (D2617): refused in a coding
// agent's session whatever the managed gate answers, and read by no gate at the
// owner's own terminal, where it prints what it widens instead.
func newAccountsCascadeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cascade",
		Short: "State the credentials cascade this installation publishes to its project",
		Long: `State what a run does when the credential it is calling with is exhausted.

The cascade is the project's own policy and every run of the project reads it off
the published node: it decides whether a run waits for the shared Claude token's
reset and spends nothing, and what it does when the wait is longer than the
project allows.

` + "`fugaro accounts cascade set`" + ` writes it in this installation's own config, under
` + "`credential_cascade`" + `. Silence there keeps whatever the published node holds, and a
member written here is what the next publish carries to it: nothing a member is
set to reaches a run until the project's policy is published, which ` + "`fugaro update`" + `
does.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown accounts cascade subcommand; want set (see fugaro accounts cascade --help)")
		},
	}
	cmd.AddCommand(newAccountsCascadeSetCmd())
	return cmd
}

// accountsCascadeOptions are the six leaves `set` writes and the layer. The
// three of them whose flag takes a value of its own default to the zero value,
// and which of them the caller passed is read from the flag set and never from
// the value: `--concurrency-per-token 0` and a run nobody passed that flag to
// are two different writes, and only one of them is the widening one.
type accountsCascadeOptions struct {
	fallback            string
	waitThreshold       string
	backoffInitial      string
	backoffCap          string
	stopAfterExhaustion bool
	concurrencyPerToken int
	layer               string
}

func newAccountsCascadeSetCmd() *cobra.Command {
	var o accountsCascadeOptions
	cmd := &cobra.Command{
		Use:   "set [--backoff-initial DUR] [--backoff-cap DUR] [--stop-after-exhaustion-per-window=BOOL] [--concurrency-per-token N]",
		Short: "Write the cascade's members in this installation's own config",
		Long: `Write credential_cascade.<member> in this installation's own config. A member
left out keeps what the file already holds, so one flag changes one member and
touches nothing else: every comment and every other byte of the file are kept.

--fallback and --wait-threshold are retired: which source a run falls over to
when its credential is exhausted, and whether it waits for a reset first, are
the own order of the project's routing table's chain now
(docs/model-credentials-routing/spec.md), so ` + "`fugaro routing ls`" + ` shows what serves
each model and ` + "`fugaro routing set`" + ` writes the chain. Both are kept for one
release so that a line already written down is answered rather than refused as
an unknown flag, and neither is read; ` + "`fugaro update`" + ` carries a written ` + "`auto`" + `
into the chain and leaves it out where the member said oauth-only.

--backoff-initial and --backoff-cap are the wait a credential is held for when
its exhaustion came with no reset time: the first wait and the longest the
doubling reaches (15m and 5h by default; a cap below the first wait is
refused).

--stop-after-exhaustion-per-window=false lets a run go on spending on a
credential which has exhausted its window, and --concurrency-per-token=0 is no
cap at all on how many runs may hold one shared credential. Both defaults are
the protective answers (true and 1), so these two flags are spelled with their
value in the flag's own word.

The two members whose value moves the cascade the wider way — false and 0 — are
the owner's own step, so a coding agent's session is refused them
whatever the managed gate answers (D2617). Run this at your own terminal.

--layer picks the layer the write lands in. The cascade is this installation's
own statement of the project's policy, and a launcher who could write it into a
shared file would be choosing everyone's spending, so the only layer it has is
local; --layer with any other layer is refused naming the one it does have.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsCascadeSet(cmd, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.fallback, "fallback", "", "retired: the project's routing table's chain says which source a run falls over to, and `fugaro routing set` writes it")
	f.StringVar(&o.waitThreshold, "wait-threshold", "", "retired: a run moves to the next source of the chain at once rather than waiting for a reset")
	f.StringVar(&o.backoffInitial, "backoff-initial", "", "the first wait for a credential whose exhaustion came with no reset time (a duration; default 15m)")
	f.StringVar(&o.backoffCap, "backoff-cap", "", "the longest that wait doubles to (a duration; default 5h, and a cap below the first wait is refused)")
	f.BoolVar(&o.stopAfterExhaustion, "stop-after-exhaustion-per-window", true, "stop the runs that need a credential which has exhausted its window (the default; --stop-after-exhaustion-per-window=false lets them spend past it)")
	f.IntVar(&o.concurrencyPerToken, "concurrency-per-token", 0, "how many runs may hold one shared credential at a time (default 1; 0 is no cap at all)")
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+"); the cascade is this installation's own config, so local is the layer it has")
	return cmd
}

// cascadeRetiredMembers are the two leaves `accounts cascade set` wrote until
// MG4 (R4 of docs/model-credentials-routing/reconciliation.md): a chain's own
// order replaces the paid fallback and the wait before failing over, so
// `fugaro update` writes a key into the chain where `fallback: auto` was set and
// leaves it out where it was oauth-only, and a run moves to the next source at
// once rather than waiting for a reset. Release 2 rejects both; this release
// reads them, and the flags below stay for one release so that a line already
// written down is answered rather than refused as an unknown flag.
var cascadeRetiredMembers = []cascadeRetiredMember{
	{flag: "fallback", key: "credential_cascade.fallback", leaf: "fallback"},
	{flag: "wait-threshold", key: "credential_cascade.wait_threshold", leaf: "wait_threshold"},
}

// cascadeRetiredMember is one of them: the flag it was written with, the
// registry key a refusal names it by, and the leaf the file spells.
type cascadeRetiredMember struct{ flag, key, leaf string }

// accountsCascadeRetiredLeaf reports whether a leaf of the cascade block is one
// of the two the chain replaced, so that what this command offers a person —
// the flags a call with none is told to pass — is the members it still writes.
func accountsCascadeRetiredLeaf(leaf string) bool {
	return slices.ContainsFunc(cascadeRetiredMembers, func(r cascadeRetiredMember) bool { return r.leaf == leaf })
}

// runAccountsCascadeSet is the order every settings write in this family takes:
// everything that can refuse refuses on the values this run was given, and the
// file is read only once the write is one that can land.
func runAccountsCascadeSet(cmd *cobra.Command, o accountsCascadeOptions) error {
	// The two members the chain replaced, refused before the file is read and
	// before any gate: this release still READS them (the node's own member is
	// what `fugaro update` carries into the chain), and this command no longer
	// writes them, so a run's failover is the table's to state.
	for _, retired := range cascadeRetiredMembers {
		if cmd.Flags().Changed(retired.flag) {
			return accountsRetiredAccess(retired.key)
		}
	}
	// The row this run reads the layer off is a member it still writes, never one
	// of the two above: all six share the one layer, and anchoring on a retired
	// member would leave this command reading its own layer off a key the release
	// after this one refuses to know.
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if err := accessGateProject(cmd.Context(), lc, nil, "nothing was written", "writing the credentials cascade"); err != nil {
		return err
	}
	key := "credential_cascade.concurrency_per_token"
	row, ok := settings.Lookup(key)
	if !ok {
		return userErr("%s: this fugaro has no registry row for the credentials cascade, so it cannot say which layer it is written in; upgrade it or file a bug", key)
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
	next, wrote, err := accountsCascadeEdit(cmd, old, o)
	if err != nil {
		return err
	}
	if len(wrote) == 0 {
		return userErr("accounts cascade set: pass at least one member to write (%s)", accountsCascadeFlagList())
	}
	if !yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: the cascade already reads %s\n", oneLine(target.path), strings.Join(accountsCascadePairs(wrote), ", "))
		return nil
	}
	// The edited bytes are read back by the document's own strict loader before
	// anything else (spec §4.2 step 3): a duration the loader cannot parse and a
	// backoff cap below its own first wait are refused here, in the loader's own
	// sentence, rather than written into a file this Fugaro's very next command
	// would refuse.
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	if err := settingsLayerGate(cmd, target, wo, key, old, next); err != nil {
		return err
	}
	widened := accountsCascadeWidened(wrote)
	edit := settingsEdit{
		Path:     target.path,
		Old:      old,
		New:      next,
		What:     fmt.Sprintf("write the credentials cascade: %s, which the project's node carries to every run", strings.Join(accountsCascadePairs(wrote), ", ")),
		Kind:     target.kind,
		Command:  accountsCascadeCommand(cmd, o),
		Typed:    len(widened) > 0,
		TypedWhy: accountsCascadeTypedWhy(widened),
	}
	if err := applySettingsEdit(cmd, lc, edit); err != nil {
		return err
	}
	// The store is local and the node is the second place the value is served
	// from, which is the pair the hook reads the stages off (D2615): the cascade
	// reaches a run off the published node and nowhere else, so this write owes
	// the policy stage, and the sentence that stage prints is what says whether
	// the node carries it yet. The member named is the first this run wrote,
	// which is the one a person reading the sentence looks for.
	return runAfterWrite(cmd.Context(), cmd, afterWriteChange{Key: wrote[0].key, Store: settings.StoreLocal})
}

// accountsCascadeWritten is one member a run wrote: the registry key the hook is
// handed, the leaf the file spells, the value, and why the write widens
// ("" for the ordinary direction).
type accountsCascadeWritten struct {
	key   string
	leaf  string
	value string
	why   string
}

// accountsCascadeEdit is the bytes this write leaves: every member whose flag
// was passed, written at its own leaf, in the order the flags are declared. A
// flag the caller did not pass is a member left alone, which is what makes one
// flag one change — the file's other members, its comments and its key order are
// all kept, because the editor touches the one leaf.
func accountsCascadeEdit(cmd *cobra.Command, old []byte, o accountsCascadeOptions) ([]byte, []accountsCascadeWritten, error) {
	next := old
	var wrote []accountsCascadeWritten
	for _, leaf := range cascadeWriterLeaves {
		// A retired member never reaches here — runAccountsCascadeSet refuses its
		// flag before the file is read — and skipping it here as well is what keeps
		// the editor from writing one if that refusal ever moves.
		if !cmd.Flags().Changed(leaf.flag) || accountsCascadeRetiredLeaf(leaf.leaf) {
			continue
		}
		value := accountsCascadeValue(leaf.leaf, o)
		out, err := yamledit.SetScalar(next, append([]string{"credential_cascade"}, strings.Split(leaf.leaf, ".")...), value)
		if err != nil {
			return nil, nil, userErr("credential_cascade.%s: %v", leaf.leaf, err)
		}
		next = out
		wrote = append(wrote, accountsCascadeWritten{
			key:   "credential_cascade." + leaf.leaf,
			leaf:  leaf.leaf,
			value: value,
			why:   accountsCascadeWidens(leaf.leaf, value),
		})
	}
	return next, wrote, nil
}

// accountsCascadeWidens is why writing this member at this value is a widening
// direction, and "" when it is not (D142, D2617). The rule is per member and per
// direction rather than per row: a threshold is a figure a person may raise or
// lower without either way being this command's to grade, while these two spend
// what the protective value holds back — the runs a credential which has
// exhausted its window may still carry, and the number of runs holding one
// shared credential at a time.
//
// D142's third widening, the paid fallback, is not here: it is one of the two
// members the chain replaced and this command no longer writes it (MG4,
// cascadeRetiredMembers).
func accountsCascadeWidens(leaf, value string) string {
	switch {
	case leaf == "stop_after_exhaustion_per_window" && value == "false":
		return "credential_cascade.stop_after_exhaustion_per_window false lets a run go on spending on a credential which has exhausted its window (D142)"
	case leaf == "concurrency_per_token" && value == "0":
		return "credential_cascade.concurrency_per_token 0 is no cap at all on how many runs may hold one shared credential (D142)"
	}
	return ""
}

// accountsCascadeWidened is the widening sentences of a run's writes, in the
// order the members were written: what the coding agent's refusal reads and what
// the owner's own terminal prints before the write.
func accountsCascadeWidened(wrote []accountsCascadeWritten) []string {
	var out []string
	for _, w := range wrote {
		if w.why != "" {
			out = append(out, w.why)
		}
	}
	return out
}

// accountsCascadePairs is what a run wrote as `<leaf> = <value>` pairs, for the
// sentence: the member as the file spells it, never the registry's whole key,
// which names the carrier rather than the member.
func accountsCascadePairs(wrote []accountsCascadeWritten) []string {
	out := make([]string, 0, len(wrote))
	for _, w := range wrote {
		out = append(out, w.leaf+" = "+pluginwire.Printable(w.value))
	}
	return out
}

// accountsCascadeTypedWhy is the sentence a widening write is refused with in a
// coding agent's session: empty for an ordinary write, whose edit is never Typed.
func accountsCascadeTypedWhy(widened []string) string {
	if len(widened) == 0 {
		return ""
	}
	return "this widens the credentials cascade, which is the owner's own step: " + strings.Join(widened, "; ")
}

// accountsCascadeCommand is the command line that makes the same write, spelled
// whole: what the refusal a coding agent's session reads names as the thing to
// run at the owner's own terminal. It carries the flags this run was given and
// never a value the file refuses to print, and it names no --layer word because
// the cascade's one layer is the default the command resolves without it.
func accountsCascadeCommand(cmd *cobra.Command, o accountsCascadeOptions) string {
	line := []string{"fugaro", "accounts", "cascade", "set"}
	for _, leaf := range cascadeWriterLeaves {
		if !cmd.Flags().Changed(leaf.flag) {
			continue
		}
		line = append(line, leaf.valueWords(pluginwire.Printable(accountsCascadeValue(leaf.leaf, o)))...)
	}
	return strings.Join(line, " ")
}

// accountsCascadeValue is the value a leaf was given, in the one place the edit
// and the command line above both read it, so the two cannot disagree about what
// was written.
func accountsCascadeValue(leaf string, o accountsCascadeOptions) string {
	switch leaf {
	case "fallback":
		return o.fallback
	case "wait_threshold":
		return o.waitThreshold
	case "backoff.initial":
		return o.backoffInitial
	case "backoff.cap":
		return o.backoffCap
	case "stop_after_exhaustion_per_window":
		return strconv.FormatBool(o.stopAfterExhaustion)
	default:
		return strconv.Itoa(o.concurrencyPerToken)
	}
}

// accountsCascadeFlagList is the members this command writes, as flags, for the
// refusal of a call that passed none.
func accountsCascadeFlagList() string {
	flags := make([]string, 0, len(cascadeWriterLeaves))
	for _, leaf := range cascadeWriterLeaves {
		if accountsCascadeRetiredLeaf(leaf.leaf) {
			continue
		}
		flags = append(flags, leaf.flagArg())
	}
	return strings.Join(flags, ", ")
}
