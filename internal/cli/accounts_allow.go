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
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// accounts_allow.go is `fugaro accounts allow` and `fugaro accounts deny`:
// the two directions of one field, claude_accounts.NAME.allow_data_to — the
// repositories an account's token may be spent from (spec §5). `accounts add`
// and `accounts rm` bind a name to the project; these two edit the list that
// name already carries, without touching a profile, a secret or a job.
//
// They are one file because they are one edit read two ways. Both go through
// the settings commands' one write path (D847), so the diff, the sentence, the
// coding agent's session refusal, the backup and the compare-and-swap are the
// ones every other settings write pays; and both leave the file byte-identical
// when the list already says what they were asked for, which is what makes a
// script that re-runs them safe.
//
// They read no value and store none (spec §8): allow_data_to names
// repositories, never a token, and the token stays `secrets set` / `creds set`
// / `accounts add`'s. The one line they end on is `next: fugaro update`,
// because the node a repository's runs are admitted by is derived from this
// list and published by that command alone.

// accountsAllowOptions is what the pair takes. There is no --json and no flag
// that carries a value: the whole output is a diff a person reads (spec §2.1).
type accountsAllowOptions struct {
	yes bool
}

// newAccountsAllowCmd is `fugaro accounts allow`.
func newAccountsAllowCmd() *cobra.Command {
	var o accountsAllowOptions
	cmd := &cobra.Command{
		Use:   "allow NAME REPO...",
		Short: "Add repositories to a named Claude account's allow-list (claude_accounts.NAME.allow_data_to)",
		Long: `Add each REPO to claude_accounts.NAME.allow_data_to, the list of
repositories the account's token may be used by, and nothing else.

A NAME the config does not declare yet is declared here with the repositories
this run names: allow is the first form of the pair, so an account whose
token may be spent from one repository needs no second command. A NAME it does
declare keeps the repositories its list already holds — the edit appends the
ones named, so a run that repeats an entry writes nothing.

The list is the per-repository decision the published policy's accounts block
is derived from: a profile serves the account to its jobs, and this list is
what bounds the token of each of them. Adding a repository is therefore a
widening of where the token may be spent, and it is read back by the owner's
own publish rather than by this command — nothing here reaches the cloud.

The repositories are written as owner/name, the shape the loader holds this
list to. A repository the file already names is left where it is, and the
sentence above the diff prints the list the file ends up with, not the one the
command line named.

This is a local file edit. It ends on one line: next: fugaro update — the
published policy's accounts block is derived from this list, so the change
reaches the project's runs in that run and not before it.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsAllowList(cmd, args[0], args[1:], true, o)
		},
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the change without a prompt")
	return cmd
}

// newAccountsDenyCmd is `fugaro accounts deny`.
func newAccountsDenyCmd() *cobra.Command {
	var o accountsAllowOptions
	cmd := &cobra.Command{
		Use:   "deny NAME REPO...",
		Short: "Remove repositories from a named Claude account's allow-list",
		Long: `Remove each REPO from claude_accounts.NAME.allow_data_to, so the
account's token may no longer be used by it.

A repository the list does not name is not an error and not a write: the
command says there is no change, because the state it was asked for is the
state the file is in.

An account a profile lists must cover every repository that profile admits —
the profile's repositories are the intersection of the two lists — so a
removal that would leave the profile admitting a repository the account no
longer covers is refused, naming the repository and the profile, with nothing
written. The remedy is to narrow the profile's own allow_repos, or to leave
the entry where it is.

This is a local file edit. It ends on one line: next: fugaro update — the
published policy's accounts block is derived from this list, so the change
reaches the project's runs in that run and not before it.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsAllowList(cmd, args[0], args[1:], false, o)
		},
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the change without a prompt")
	return cmd
}

// runAccountsAllowList is both forms: allow appends each repository to the
// account's allow_data_to, deny removes each one, and the rest — the name's own
// rule, the repository shapes, the subset rule, the sentence and the write —
// is one path, so the two directions cannot come to ask differently for the
// same list.
func runAccountsAllowList(cmd *cobra.Command, name string, repos []string, allow bool, o accountsAllowOptions) error {
	// The name is a DEFINITION and not a reference, so the loader's own rule
	// judges it (config.ValidateClaudeAccounts, the same sentence `accounts
	// add` prints for the same name), and the repositories are judged by that
	// rule's second half — "owner/name", the shape this list is held to —
	// rather than by a second spelling of it here. Read before anything is
	// opened: a name or a repository this program will not write is refused
	// with nothing read and nothing written.
	if ps := config.ValidateClaudeAccounts(map[string]config.ClaudeAccount{name: {AllowDataTo: repos}}); len(ps) > 0 {
		return userErr("%s", ps[0].Message)
	}

	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if !allow {
		if _, declared := lc.ClaudeAccounts[name]; !declared {
			// The remedy is the one command builder this pair has
			// (accountsAllowCommand): a list joined with spaces and quoted once
			// is a single word to the shell, and this program's own validator
			// would refuse the paste — a repository is owner/name, and the
			// joined list is not.
			return userErr("claude_accounts.%s is not declared, so it has no allow-list to remove %s from: declare it with '%s accounts add %s --repos %s' (which stores its token), or add the repositories with '%s'",
				pluginwire.Printable(name), pluginwire.Printable(strings.Join(repos, ", ")),
				selfCommand(), quoteWord(name), quoteWord(strings.Join(repos, ",")),
				accountsAllowCommand(name, repos, true))
		}
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}

	// The edit, on the bytes as they are: allow appends each repository and
	// deny removes each one, so the list the file ends up with is the list it
	// held plus or minus exactly what was named. A repository the list already
	// names is left alone by either direction (yamledit's own rule), which is
	// what makes a re-run a no-op rather than a second entry — and what keeps
	// the second half of this edit, the rewrite of a flow list below, out of a
	// run that has nothing to write.
	held := lc.ClaudeAccounts[name].AllowDataTo
	var edits []string
	for _, repo := range repos {
		if slices.Contains(held, repo) != allow {
			edits = append(edits, repo)
		}
	}
	next, err := accountsAllowEdit(old, name, edits, allow)
	if yamledit.ReasonOf(err) == yamledit.ReasonFlowCollection {
		// A flow sequence is the one shape both directions refuse — an
		// allow_data_to the operator wrote as [owner/name] is one, and this
		// pair is what makes the removal direction reachable on it. The entry
		// has this one field, so it is written again in the block style this
		// program writes a list in, with the repositories it holds, and the
		// edit is made there; the diff shows the reformatting beside it.
		shaped, shapeErr := settingsRewriteList(old, "claude_accounts", name, held, false)
		if shapeErr != nil {
			return shapeErr
		}
		next, err = accountsAllowEdit(shaped, name, edits, allow)
	}
	if err != nil {
		// An entry the block does not hold inside a flow collection is the
		// shape that gets here: the rewrite above found nothing to write again,
		// so it left the bytes as they are and the edit answered with the
		// editor's own sentence. The editor refuses for the file's shape and
		// this program says so in its own words, the ones the models writes
		// print (modelsSpliceRefusal, D790), under the entry to write by hand.
		if refusal, ok := modelsSpliceRefusal("claude_accounts."+name, err); ok {
			return refusal
		}
		return userErr("claude_accounts.%s.allow_data_to: %v", pluginwire.Printable(name), err)
	}
	// Read the edit back strictly before it is written: a file this Fugaro
	// cannot read is a file the next command of the owner's would refuse, and
	// the refusal belongs here, with nothing written.
	after, err := localcfg.Parse(next)
	if err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	if !allow {
		if err := accountsDenySubsetRule(after, name, repos); err != nil {
			return err
		}
	}

	what := accountsAllowWhat(after, name, repos, allow)
	command := accountsAllowCommand(name, repos, allow)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    settingsDocLocal,
		Command: command,
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

// accountsAllowEdit adds each repository to the account's allow_data_to, or
// removes each one, and is run twice by runAccountsAllowList: once on the bytes
// as they are, and, where those hold a flow list, once on the same entry
// written in block style.
func accountsAllowEdit(old []byte, name string, repos []string, allow bool) ([]byte, error) {
	next := old
	var err error
	for _, repo := range repos {
		if allow {
			next, err = yamledit.AddListItem(next, []string{"claude_accounts", name, "allow_data_to"}, repo)
		} else {
			next, err = yamledit.RemoveListItem(next, []string{"claude_accounts", name, "allow_data_to"}, repo)
		}
		if err != nil {
			return nil, err
		}
	}
	return next, nil
}

// accountsDenySubsetRule is the rule a removal can break and an addition
// cannot: an account a profile lists must cover every repository that profile
// admits, because the profile's repositories are the intersection of the two
// lists and never their union (accountsAddSubsetRule, whose rule this is —
// read here on the account's own list after the edit, with the remedy the
// denial direction has).
//
// The rule's two preconditions are the same two accountsAddSubsetRule states
// and for the same reason: a profile that lists accounts writes its own
// allow_repos, exactly. A profile that writes none or writes a wildcard is a
// state the owner's own publish refuses, and a removal cannot be judged
// against it.
func accountsDenySubsetRule(after *localcfg.Config, name string, repos []string) error {
	for _, profile := range slices.Sorted(maps.Keys(after.Profiles)) {
		p := after.Profiles[profile]
		if !slices.Contains(p.ClaudeAccounts, name) {
			continue
		}
		if len(p.AllowRepos) == 0 {
			return userErr("profiles.%s.claude_accounts lists %s and profiles.%s writes no allow_repos: a profile that lists accounts must write its own list, and without one which repositories it admits is undecidable — add allow_repos to profiles.%s, naming the repositories it admits, then run this again; nothing was written",
				pluginwire.Printable(profile), pluginwire.Printable(name), pluginwire.Printable(profile), pluginwire.Printable(profile))
		}
		for _, pattern := range p.AllowRepos {
			if strings.Contains(pattern, "*") {
				return userErr("profiles.%s.allow_repos carries the wildcard %s, and a profile that lists accounts must name exact repositories: whether the account still covers it is decided repository by repository, and a wildcard makes that undecidable — write the repositories themselves, then run this again; nothing was written",
					pluginwire.Printable(profile), pluginwire.Printable(pattern))
			}
		}
		covered := after.ClaudeAccounts[name].AllowDataTo
		var missing []string
		for _, r := range p.AllowRepos {
			if !accountsAddCovers(covered, p.Provider, r) {
				missing = append(missing, r)
			}
		}
		if len(missing) == 0 {
			continue
		}
		// The remedy writes the account's own list, which is the repository as
		// owner/name, and a profile's allow_repos is the canonical spelling the
		// profile's provider qualifies ("github/acme/app"): printing the
		// pattern it was read from would name a command this program's own
		// validator refuses, and quoting the list whole would make it one word.
		keep := make([]string, 0, len(missing))
		for _, r := range missing {
			keep = append(keep, strings.TrimPrefix(r, p.Provider+"/"))
		}
		return userErr("profiles.%s.allow_repos lists %s and claude_accounts.%s.allow_data_to would no longer cover %s: an account a profile lists covers every repository that profile admits, and the profile's repositories are the intersection of the two and never their union — leave %s on the account's list with '%s', or take it out of profiles.%s.allow_repos; nothing was written",
			pluginwire.Printable(profile), strings.Join(missing, ", "), pluginwire.Printable(name), strings.Join(missing, ", "),
			strings.Join(missing, ", "), accountsAllowCommand(name, keep, true), pluginwire.Printable(profile))
	}
	return nil
}

// accountsAllowWhat is the sentence the write prints. The list it names is the
// one the file ends up with, read back from the config this would write and
// not the command line's values: a repository the list already held keeps its
// place, and a sentence built from the arguments would print a shorter list
// than the file it asks to write.
func accountsAllowWhat(after *localcfg.Config, name string, repos []string, allow bool) string {
	verb, prep := "add", "to"
	if !allow {
		verb, prep = "remove", "from"
	}
	key := "claude_accounts." + pluginwire.Printable(name) + ".allow_data_to"
	return fmt.Sprintf("write the allow-list of %s: %s %s %s %s, so the repositories its token may be used by are [%s]",
		pluginwire.Printable(name), verb, pluginwire.Printable(strings.Join(repos, ", ")), prep, key,
		pluginwire.Printable(strings.Join(after.ClaudeAccounts[name].AllowDataTo, ", ")))
}

// accountsAllowCommand is the command a refusal in a coding agent's session
// prints for the person to run at their own terminal: this same edit, spelled
// whole, with every name quoted so a value from the command line cannot become
// a second token of the command the person pastes.
func accountsAllowCommand(name string, repos []string, allow bool) string {
	verb := "allow"
	if !allow {
		verb = "deny"
	}
	parts := []string{selfCommand(), "accounts", verb, quoteWord(name)}
	for _, repo := range repos {
		parts = append(parts, quoteWord(repo))
	}
	return strings.Join(parts, " ")
}
