package cli

import (
	"bufio"
	"context"
	"encoding/json"
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
	"github.com/dimipaun/fugaro/internal/task"
)

// newAccountsCmd is `fugaro accounts`: read-only lookups over the local
// config's claude_accounts, parallel to secrets' and recipes' group commands.
func newAccountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "accounts",
		Aliases: []string{"account"},
		Short:   "List the named Claude accounts a repository may use",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown accounts subcommand; want ls or clear (see fugaro accounts --help)")
		},
	}
	cmd.AddCommand(newAccountsLsCmd(), newAccountsClearCmd())
	return cmd
}

type accountsOptions struct {
	cloud  cloudOptions
	repo   string
	asJSON bool
}

func (o *accountsOptions) addFlags(cmd *cobra.Command) {
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().StringVar(&o.repo, "repo", "", "repository as owner/name (default: this checkout's origin)")
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
}

// accountsListDoc is fugaro accounts ls --json's shape: the repository and
// the names it may choose among (config.AllowedClaudeAccounts's own order,
// "default" first), names only — never a secret ID or a token value.
//
// Complete is false when project selection failed for a reason other than
// an explicit --config/--project/$FUGARO_CONFIG/$FUGARO_PROJECT (an
// ambiguous or unreadable project config, for example): Accounts is then
// "default" alone, which may be short the repository's real
// claude_accounts, not a true answer that none are configured. A --json
// caller must check this field before concluding "this repository has only
// the default account" (review finding, task 9b round 2: the old exit-0,
// stderr-only note was invisible to a --json caller).
type accountsListDoc struct {
	Repo     string   `json:"repo"`
	Accounts []string `json:"accounts"`
	Complete bool     `json:"complete"`
	Note     string   `json:"note,omitempty"` // the selection failure, when Complete is false
	// States is every listed account's credential state, keyed by name, and
	// StateNote is why there are none when there are none. An account the
	// cascade has never held has the state "available": a credential with no
	// node, one whose node says available and one whose mark was cleared are
	// the same credential (creds.Mark's own doc), and a caller that had to tell
	// them apart would be reading a distinction nothing makes.
	//
	// A state is a word of internal/creds and, for an exhaustion, the reset
	// time it reported — names and states only, never a token or a secret ID
	// (F27). States is absent, with StateNote naming why, when no mark could be
	// read: a project with no budget database, or a read the person's rules
	// refused. "Every account is available" is a claim this command may only
	// make about marks it actually read.
	States    map[string]accountState `json:"states,omitempty"`
	StateNote string                  `json:"state_note,omitempty"`
}

// accountState is one account's state as fugaro accounts ls --json reports it:
// the word internal/creds stores (available, exhausted, backed_off or broken,
// or a state a newer release wrote) and, for an exhaustion, the instant the
// hold lapses, in RFC 3339.
type accountState struct {
	State string `json:"state"`
	Until string `json:"until,omitempty"`
}

func newAccountsLsCmd() *cobra.Command {
	var o accountsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the named Claude accounts (claude_accounts in the local config) a repository may use with fugaro run --token",
		Long: `List the names fugaro run --token NAME (and a repository's own agent.oauth_token)
accept for a repository: "default" (the repository's own unnamed
claude-oauth-token secret, always allowed) first, then every claude_accounts
name in the local config whose allow_data_to lists the repository, sorted
(config.AllowedClaudeAccounts, the one place that decides the question).

This is a local, read-only lookup, offline whenever a project config is
already selected on this machine: it reads the local config file the CLI
would otherwise select for a cloud command, and never calls Secret Manager,
never reads or prints a token value, only names.

--json's "complete" is false when no project config could be selected for a
reason other than an explicit --config/--project/$FUGARO_CONFIG/$FUGARO_PROJECT
(several project configs with nothing to pick one, for example): "accounts"
is then "default" alone, which may be short the repository's real
claude_accounts, not a true answer that none are configured; "note" says why.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return accountsLs(cmd, o)
		},
	}
	o.addFlags(cmd)
	return cmd
}

// accountsConfig is the local config selected for fugaro accounts ls, nil
// when none is (reported as a note, not an error, the same leniency
// pricesConfig gives fugaro budget prices): the command's whole point still
// answers with "default" alone, offline, rather than failing outright. It
// never falls back to the shared config published to the runs bucket
// (selectProject's sharedOK path): that would be a network call for a
// command billed as offline, and claude_accounts is never carried by the
// shared config anyway (localcfg.Config.Shared strips it).
//
// note is "" when lc is authoritative (selection succeeded, whether or not
// a config was actually found — selectFrom returning no error at all only
// happens when one was); it names the selection failure otherwise, for the
// caller to carry into accountsListDoc.Complete/Note, so a --json caller
// can tell "genuinely no other accounts" from "couldn't tell: selection
// failed", which an exit-0, stderr-only note could not (review finding,
// task 9b round 2).
func accountsConfig(cmd *cobra.Command, o cloudOptions) (lc *localcfg.Config, note string, err error) {
	ctx := cmd.Context()
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, "", err
	}
	sel, lc, err := selectFrom(o, co, false)
	if err != nil {
		explicit := o.config != "" || o.project != "" || os.Getenv("FUGARO_PROJECT") != "" || os.Getenv("FUGARO_CONFIG") != ""
		if explicit {
			return nil, "", err
		}
		note = fmt.Sprintf("no project config could be selected, so only the always-available default account is listed (%v)", err)
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", note)
		return nil, note, nil
	}
	return lc, "", announce(o, sel, lc)
}

func accountsLs(cmd *cobra.Command, o accountsOptions) error {
	ctx := cmd.Context()
	repo := o.repo
	if repo == "" {
		var err error
		if repo, err = originRepo(ctx); err != nil {
			return err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return userErr("--repo: %v", err)
	}
	lc, note, err := accountsConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	var claudeAccounts map[string]config.ClaudeAccount
	if lc != nil {
		claudeAccounts = lc.ClaudeAccounts
	}
	names := config.AllowedClaudeAccounts(claudeAccounts, repo)
	doc := accountsListDoc{Repo: repo, Accounts: names, Complete: note == "", Note: note}
	doc.States, doc.StateNote = accountsStates(ctx, lc, names, note)
	if doc.StateNote != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", doc.StateNote)
	}
	return printAccounts(cmd.OutOrStdout(), doc, o.asJSON)
}

// accountsStates is every listed account's credential state, read from the
// cascade's own subtree (creds/marks, internal/creds). The second return is
// the note to carry when there is nothing to show, and it is never an error:
// the names are a local read and stay right whether or not a mark could be
// read, so a project with no budget database, a database that is down and a
// read the person's rules refuse all answer with the names and a note.
//
// The read is the operator's, as the person (ADC), not the service's: the
// marks are what the service writes and what an operator is meant to see
// (D141), and the rules grant the read to an admin. The note names the reason
// rather than printing every account as available, which is a claim about
// marks this command did not read.
func accountsStates(ctx context.Context, lc *localcfg.Config, names []string, selNote string) (map[string]accountState, string) {
	if lc == nil {
		return nil, "no project config could be selected, so no credential state could be read (" + selNote + ")"
	}
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, fmt.Sprintf("project %s has no Firebase budget backend, so no credential state could be read (the cascade's marks live in its database; fugaro init --firebase adds it)", lc.Name)
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, fmt.Sprintf("no credential state could be read: %v", err)
	}
	// The database must be this project's own (the check every other reader and
	// writer makes): a budget.rtdb_url pointing at another project's database
	// would otherwise have this command print that project's marks as this
	// project's states, which is a wrong answer rather than a missing one.
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, fmt.Sprintf("no credential state could be read: %v", err)
	}
	marks, err := (&creds.RTDBStore{DB: db}).Marks(ctx)
	if err != nil {
		return nil, fmt.Sprintf("no credential state could be read: %v", err)
	}
	out := make(map[string]accountState, len(names))
	for _, n := range names {
		out[n] = accountStateOf(marks[n])
	}
	return out, ""
}

// accountStateOf is one mark as the document spells it: the state internal/creds
// stores, and the reset time an exhaustion reported. A mark this release does
// not model is shown as the word it carries rather than as available — the
// one direction this reader must not round in.
func accountStateOf(m creds.Mark) accountState {
	s := accountState{State: string(m.State)}
	if s.State == "" {
		s.State = string(creds.StateAvailable)
	}
	if s.State == string(creds.StateExhausted) && !m.Until.IsZero() {
		s.Until = m.Until.UTC().Format(time.RFC3339)
	}
	return s
}

// stateText is one state as the text column spells it, the plan's words: the
// four states, and "exhausted until T" with the reset time spelled out.
func stateText(s accountState) string {
	switch s.State {
	case string(creds.StateAvailable):
		return "available"
	case string(creds.StateExhausted):
		if s.Until == "" {
			return "exhausted"
		}
		t, err := time.Parse(time.RFC3339, s.Until)
		if err != nil {
			return "exhausted until " + cellOneLine(s.Until)
		}
		return "exhausted until " + t.UTC().Format("2006-01-02 15:04Z")
	case string(creds.StateBackedOff):
		return "backed off"
	case string(creds.StateBroken):
		return "broken"
	}
	return cellOneLine(s.State)
}

// nameCell is a name as a table cell: Printable escapes the control characters
// a name could carry (ValidateClaudeAccounts refuses them upstream, but this
// function does not assume that check ran), and a tab is spelled out too,
// because in a tabwriter row a literal tab opens the next column instead of
// staying inside this cell.
func nameCell(n string) string {
	return strings.ReplaceAll(pluginwire.Printable(n), "\t", `\u0009`)
}

// printAccounts writes doc: the names alone when no state was read, and the
// plan's state column beside them when one was.
func printAccounts(w io.Writer, doc accountsListDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	if doc.States == nil {
		for _, n := range doc.Accounts {
			if _, err := fmt.Fprintln(w, pluginwire.Printable(n)); err != nil {
				return err
			}
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, n := range doc.Accounts {
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", nameCell(n), stateText(doc.States[n])); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// ------------------------------------------------------------------- clear

type accountsClearOptions struct {
	cloud cloudOptions
	yes   bool
}

// newAccountsClearCmd is `fugaro accounts clear NAME`: the operator's write to
// the cascade's state (D141, C1.21). The service marks a credential broken
// when its token is refused, and nothing but an operator ever puts it back in
// rotation — no probe, no reset time, no usage report (creds.Break). After
// replacing the token, this is how the mark is cleared.
//
// It writes exactly one node, creds/marks/<NAME>, with creds.Cleared(), as the
// person (ADC, an admin of the database): the service has no admin endpoint
// (D143) and the CLI is the operator's own hand. The write is conditional
// (creds.RTDBStore.UpdateMark → PutIfMatch), so a mark the service changed
// between this command's read and its write is re-read rather than
// overwritten, and the state is judged again on the value actually written
// under.
//
// Names and states only: nothing here reads or writes a token, and the command
// prints neither a value nor a secret ID (F27).
func newAccountsClearCmd() *cobra.Command {
	var o accountsClearOptions
	cmd := &cobra.Command{
		Use:   "clear NAME",
		Short: "Clear one account's broken or exhausted credential mark, after replacing its token",
		Long: `Clear one Claude account's credential mark, so the cascade may route work to it
again. This is an operator's command: it writes the project's own database,
acting as you (ADC), and it is the only thing that ends a broken mark.

Only the two states an operator's action is what ends may be cleared:

  broken     the service saw a 401 (D139) and nothing clears it on its own.
             Replace the token first, then clear the mark here.
  exhausted  the account reported a usage limit and holds until its reset time.

A backed-off mark is refused: a probe is what clears it (C1.12), and clearing it
by hand would route real work to a credential that has not answered since it was
held. A state this Fugaro does not know is refused too, because clearing it
could take off a hold a newer release placed.

The write is one node — creds/marks/NAME, in this installation's own database —
and it is conditional on the value read (PutIfMatch), so a mark the service
changed at the same moment is not lost.

NAME is "default" (the repository's own unnamed claude-oauth-token secret) or a
claude_accounts name of the selected project config; any other name is refused
rather than written, so a typo cannot leave a mark node for a credential that
does not exist. Nothing here reads or prints a token value, a secret ID or a
prefix of one: names and states only.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return accountsClear(cmd, o, args[0]) },
	}
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().BoolVar(&o.yes, "yes", false, "skip the typed confirmation (read what it does first)")
	return cmd
}

// accountsClear is newAccountsClearCmd's work.
func accountsClear(cmd *cobra.Command, o accountsClearOptions, name string) error {
	ctx := cmd.Context()
	// The local config, never the shared one: the write is the operator's own
	// installation's database, and claude_accounts — which says whether NAME is
	// an account this installation has at all — is never carried by the shared
	// config (localcfg.Config.Shared strips it).
	lc, note, err := accountsConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	if lc == nil {
		return userErr("no project config could be selected, so there is no database to clear %q in (%s)", name, note)
	}
	if name != config.DefaultClaudeAccount {
		if _, ok := lc.ClaudeAccounts[name]; !ok {
			return userErr("no claude_accounts entry of project %s is named %q: this writes one account's mark, and a name the installation does not know would leave a mark node for a credential that does not exist (fugaro accounts ls lists the names)", lc.Name, name)
		}
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return err
	}
	// This writes, so the database must be this project's own — the check
	// `fugaro budget set` and `fugaro budget kill` make before they write: a
	// budget.rtdb_url pointing at another project's database would otherwise
	// have the operator clear a mark in an installation that is not theirs.
	if err := checkDBProject(ctx, db, lc); err != nil {
		return err
	}
	store := &creds.RTDBStore{DB: db}

	// What is there now, read the same way `fugaro accounts ls` reads it: the
	// whole subtree, decoded strictly, so a node this release does not model
	// fails the read rather than being taken for an available credential
	// (creds.RTDBStore.Marks). The state decides both what the confirmation
	// says and whether the clear is allowed at all.
	marks, err := store.Marks(ctx)
	if err != nil {
		return remote(err)
	}
	prev := marks[name]
	switch prev.State {
	case creds.StateBroken, creds.StateExhausted:
	case "", creds.StateAvailable:
		return userErr("account %q has no mark to clear: it is available", name)
	case creds.StateBackedOff:
		return userErr("account %q is backed off, and a probe is what clears that state (C1.12), not an operator: clearing it by hand would route real work to a credential that has not answered since it was held", name)
	default:
		return userErr("account %q carries the state %q, which this Fugaro does not know: clearing it could take off a hold a newer release placed, so nothing was changed", name, oneLine(string(prev.State)))
	}

	what := fmt.Sprintf("clear the %s mark of Claude account %s, putting it back in the cascade's rotation",
		stateText(accountStateOf(prev)), pluginwire.Printable(name))
	if err := confirmTyped(cmd, bufio.NewReader(cmd.InOrStdin()), lc, o.yes, what); err != nil {
		return err
	}

	written, err := store.UpdateMark(ctx, name, func(cur creds.Mark, _ bool) (creds.Mark, error) {
		// The state is judged again here, on the value this write is
		// conditional on: the read above is only what the confirmation said,
		// and a service that marked the credential between the two must not
		// have its change overwritten by a clear meant for another state.
		switch cur.State {
		case creds.StateBroken, creds.StateExhausted:
			return creds.Cleared(), nil
		case "", creds.StateAvailable:
			return creds.Mark{}, userErr("account %q became available while this ran, so there is nothing to clear; nothing was written", name)
		default:
			return creds.Mark{}, userErr("account %q became %q while this ran, which this Fugaro does not clear; nothing was written", name, oneLine(string(cur.State)))
		}
	})
	if err != nil {
		return remote(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "cleared the %s mark of %s (now %s)\n",
		stateText(accountStateOf(prev)), pluginwire.Printable(name), stateText(accountStateOf(written)))
	return nil
}
