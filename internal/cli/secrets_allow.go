package cli

import (
	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/pluginwire"
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
	yes   bool
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

This is a local file edit. It ends on one line: next: fugaro update — the
published policy's entry is derived from this list, so the change reaches the
project's runs in that run and not before it.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecretsAllowDeny(cmd, o, args[0], args[1:], true)
		},
	}
	cmd.Flags().BoolVar(&o.proxy, "proxy", false, "set project_secrets.NAME.proxy, the Bitbucket token's switch for the credentials service (--proxy=false turns it off)")
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
entry: the value is then stored for no repository, and the entry stays for the
day a pattern is added back.

This whole family is refused in a coding agent's session, --yes included (spec
§5): what a secret reaches is a decision no plan shows. Nothing here reads or
prints a value.

This is a local file edit. It ends on one line: next: fugaro update — the
published policy's entry is derived from this list, so the change reaches the
project's runs in that run and not before it.`,
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
		// A name with no pattern and no --proxy names nothing to write: the
		// switch is the only other thing this form can turn, and a run that
		// reached secretsSetPolicy with both empty would take the switch away
		// (--proxy=false's own edit) for a person who asked for neither.
		if len(patterns) == 0 && !cmd.Flags().Changed("proxy") {
			return userErr("secrets allow %s names neither a repository to add nor the switch to turn on: pass one or more PATTERNs (a repository as owner/name, with * inside a segment allowed), or --proxy to set %s.proxy",
				pluginwire.Printable(name), pluginwire.Printable(secretsBlockProject+"."+name))
		}
		so = secretsOptions{allowDataTo: patterns, proxy: o.proxy, yes: o.yes, form: "allow"}
	}
	return secretsSetPolicy(cmd, so, name)
}
