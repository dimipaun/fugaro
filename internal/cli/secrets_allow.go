package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// secrets_allow.go is `fugaro secrets allow` and `fugaro secrets deny`: the two
// directions of one field, project_secrets.NAME.allow_data_to — the repositories
// the value is stored for, which is the list of them that may mount it (spec
// §5). They are the second spelling of `secrets set`'s policy flags: `allow`
// writes what --allow-data-to writes, `deny` writes the direction that had no
// flag, and both go through secretsSetPolicy, so the block, the name's rule, the
// pattern's shape, the widening classification, the coding agent's session
// refusal, the diff, the backup and the compare-and-swap are the ones the
// family's policy write already pays (D847, D1171).
//
// They read no value and store none (spec §8): allow_data_to names
// repositories, never a value, and the value stays `secrets set`'s. `secrets
// allow NAME PATTERN` touches no Secret Manager container and no version.
//
// The one line they end on is `next: fugaro update`, because the published
// policy's entry is derived from this list and published by that command alone.

// secretsAllowOptions is what the pair takes. Neither form has a flag that
// carries a value: the value is `secrets set`'s, and this file never reads one.
type secretsAllowOptions struct {
	proxy bool
	// allRepos is `secrets allow --all-repos`: the record's own member that
	// admits every repository enrolled in the project, whatever its owner —
	// the one reach the operator-local block's owner-literal rule has no
	// spelling for (O2, answered 2026-11-23). It is a flag of `allow` alone:
	// `deny` narrows a list, and the switch is not a list.
	allRepos bool
	yes      bool
}

// newSecretsAllowCmd is `fugaro secrets allow`.
func newSecretsAllowCmd() *cobra.Command {
	var o secretsAllowOptions
	cmd := &cobra.Command{
		Use:   "allow NAME PATTERN...",
		Short: "Add repositories to a project secret's allow-list (project_secrets.NAME.allow_data_to)",
		Long: `Add each PATTERN to project_secrets.NAME.allow_data_to — or to
workflow_secrets.NAME.allow_data_to for a name your local config holds there —
the list of repositories the value is stored for and mounted in.

A PATTERN is a repository as owner/name, and may use * inside a segment, never
across /: myworkspace/* is a whole workspace and myworkspace/api-* a family,
while the owner segment is literal (one project secret is one owner's
credential). A pattern means every repository it matches, now and later, so
adding one is a widening: it is one more repository the value is mounted in.

With --proxy, and no PATTERN, this writes project_secrets.NAME.proxy on its
own: the Bitbucket token's switch for the credentials service, which is also a
widening, because it hands the token to the service rather than to the job.
--proxy=false takes the switch away again and is the one form here that is not
a widening. --proxy is refused for a workflow secret, whose entry holds no such
member (D493, D1275).

Adding a repository or turning the switch on is the owner's own step: it asks
nothing at your own terminal, and a coding agent's session is refused it, with
the command to run yourself (D1552). This whole family is refused in a session,
and no flag lifts it. Nothing here reads or prints a value.

A workflow secret's list is written twice in one run: to the local block, and to
the project record at creds/projectsecrets, which is the list the credentials
service admits a fetch by (D3328). The record is the project's database, so this
needs the Firebase backend the project's runs use; the project block's names
have no record entry and are a local edit alone.

This ends on one line: next: fugaro update — the published policy's entry is
derived from this list, so the change reaches the project's runs in that run and
not before it.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecretsAllowDeny(cmd, o, args[0], args[1:], true)
		},
	}
	cmd.Flags().BoolVar(&o.proxy, "proxy", false, "set project_secrets.NAME.proxy, the Bitbucket token's switch for the credentials service (--proxy=false turns it off)")
	cmd.Flags().BoolVar(&o.allRepos, "all-repos", false, "admit every repository enrolled in the project, whatever its owner, in the name's project record entry (--all-repos=false takes it away); it names no local pattern and is not written to the local block")
	return cmd
}

// newSecretsDenyCmd is `fugaro secrets deny`.
func newSecretsDenyCmd() *cobra.Command {
	var o secretsAllowOptions
	cmd := &cobra.Command{
		Use:   "deny NAME PATTERN...",
		Short: "Remove repositories from a project secret's allow-list",
		Long: `Remove each PATTERN from project_secrets.NAME.allow_data_to — or from
workflow_secrets.NAME.allow_data_to for a name your local config holds there —
so the value is no longer stored for it and no longer mounted in it.

This is the one edit of this family that narrows: the repositories that remain
are the ones the value still reaches. A pattern the entry does not name is not
an error and not a write — the command says there is no change, because the
state it was asked for is the state the file is in.

A removal that leaves the list empty is allowed and is not a removal of the
entry: the value is then stored for no repository, and both the local entry and
the project record's entry stay for the day a pattern is added back.

This whole family is refused in a coding agent's session (spec §5): what a
secret reaches is a decision no plan shows. Nothing here reads or
prints a value.

This ends on one line: next: fugaro update — the published policy's entry is
derived from this list, so the change reaches the project's runs in that run and
not before it.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecretsAllowDeny(cmd, o, args[0], args[1:], false)
		},
	}
	return cmd
}

// runSecretsAllowDeny is both forms: it hands the family's one policy write the
// direction this form is, and the write does the rest.
//
// The two forms are one function because they are one entry read two ways: an
// addition and a removal that disagreed about which block a name lives in, or
// about what a pattern may look like, would be two answers to one question.
func runSecretsAllowDeny(cmd *cobra.Command, o secretsAllowOptions, name string, patterns []string, allow bool) error {
	so := secretsOptions{denyDataTo: patterns, yes: o.yes, form: "deny"}
	if allow {
		// A name with no pattern, no switch and no --all-repos names nothing to
		// write: the switch and the record's all-repos member are the two other
		// things this form can turn, and a run that reached secretsSetPolicy
		// with everything empty would take the switch away (--proxy=false's own
		// edit) for a person who asked for none of them.
		if len(patterns) == 0 && !cmd.Flags().Changed("proxy") && !cmd.Flags().Changed("all-repos") {
			return userErr("secrets allow %s names neither a repository to add, the switch to turn on, nor --all-repos: pass one or more PATTERNs (a repository as owner/name, with * inside a segment allowed), --proxy to set %s.proxy, or --all-repos to admit every repository of the project",
				pluginwire.Printable(name), pluginwire.Printable(secretsBlockProject+"."+name))
		}
		so = secretsOptions{allowDataTo: patterns, proxy: o.proxy, allRepos: o.allRepos, allReposSet: cmd.Flags().Changed("all-repos"), yes: o.yes, form: "allow"}
	}
	return secretsSetPolicy(cmd, so, name)
}

// secretsPolicyRecordDB opens the database the project record lives in, or
// refuses before either write is made: the record is read by the credentials
// service, so a workflow secret's allow-list cannot be written by an
// installation whose project has no database to hold it.
//
// The ownership question every writer into that database asks is asked here too
// (requireOwnBudgetDB), before the name's entry is read: a record written into
// another installation's database would name this project's names to another
// project's service.
func secretsPolicyRecordDB(cmd *cobra.Command, lc *localcfg.Config) (*rtdb.Client, error) {
	if _, db, err := budgetDBFor(cmd.Context(), lc); err != nil {
		return nil, userErr("nothing was written: a workflow secret's allow-list is read by the credentials service out of the project's database, so this write needs the project's Firebase backend: %v", err)
	} else if err := requireOwnBudgetDB(cmd.Context(), lc, db, "nothing was written"); err != nil {
		return nil, err
	} else {
		return db, nil
	}
}

// secretsPolicyWriteRecord writes the name's allowed repositories into the
// project record at creds/projectsecrets, beside the local block the same run
// writes: the record is the list a version-2 fetch is admitted by (IS1, D3333)
// and the local block is what version 1 and the mounts still read (S1).
//
// The write is the compare-and-swap internal/creds makes: the record is read,
// the one name's entry is replaced, and the answer is written under the ETag
// that was read, so an entry another operator allow-listed in between is not
// dropped (G8, TestSecretsAllowLosesNoConcurrentWrite).
//
// The name's all_repos member is the record's own and is left as it stands: a
// list write narrows or widens the patterns and never touches the switch, which
// only --all-repos moves (secretsPolicyWriteAllRepos).
func secretsPolicyWriteRecord(cmd *cobra.Command, lc *localcfg.Config, name string, repos []string) error {
	db, err := secretsPolicyRecordDB(cmd, lc)
	if err != nil {
		return err
	}
	err = creds.WriteProjectSecrets(cmd.Context(), db, func(cur creds.ProjectSecrets, _ bool) (creds.ProjectSecrets, error) {
		rule, _ := cur.Rule(name)
		rule.Repos = slices.Clone(repos)
		return cur.WithRule(name, rule), nil
	})
	if err != nil {
		return dbErr(lc, err, true)
	}
	fmt.Fprintln(cmd.OutOrStdout(), secretsPolicyRecordLine(name, repos, false))
	return nil
}

// secretsPolicyWriteAllRepos moves the record's own all_repos member for the
// name and writes nothing to the local block, which has no spelling for a
// wildcard owner (O2). A `deny` emptied list is left alone: the switch and the
// list are two reaches, and turning one off says nothing about the other — so
// the line it prints is read back out of the record the write left, not built
// from the switch that was passed: `--all-repos=false` on a name whose entry
// still lists repositories leaves those repositories admitted, and a line
// saying "no repository" would be a reach the record does not hold.
func secretsPolicyWriteAllRepos(cmd *cobra.Command, lc *localcfg.Config, name string, on bool) error {
	db, err := secretsPolicyRecordDB(cmd, lc)
	if err != nil {
		return err
	}
	var after creds.ProjectSecretRule
	err = creds.WriteProjectSecrets(cmd.Context(), db, func(cur creds.ProjectSecrets, _ bool) (creds.ProjectSecrets, error) {
		rule, _ := cur.Rule(name)
		rule.AllRepos = on
		after = rule
		return cur.WithRule(name, rule), nil
	})
	if err != nil {
		return dbErr(lc, err, true)
	}
	fmt.Fprintln(cmd.OutOrStdout(), secretsPolicyRecordLine(name, after.Repos, after.AllRepos))
	return nil
}

// secretsPolicyAllReposKey is the record's own member for one name, spelled for
// a refusal to print: the switch is not a key of the local file, so a message
// that named one would send the person to a node their config does not hold
// (secretsPolicyWriteAllRepos writes no such key).
func secretsPolicyAllReposKey(name string) string {
	return creds.PathProjectSecrets() + "." + name + ".all_repos"
}

// secretsPolicyRecordLine is the one line each record write ends on: the node,
// the name, and what a run of that name's repositories is now admitted by. A
// name and repository patterns, never a value (G18).
func secretsPolicyRecordLine(name string, repos []string, allRepos bool) string {
	reach := "no repository"
	switch {
	case allRepos:
		reach = "every repository enrolled in the project"
	case len(repos) > 0:
		reach = strings.Join(repos, ", ")
	}
	return fmt.Sprintf("%s: %s may be fetched by %s", creds.PathProjectSecrets(), pluginwire.Printable(name), reach)
}
