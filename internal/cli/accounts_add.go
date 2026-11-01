package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is the local-config half of `fugaro accounts add`: the name, the
// repositories, the profile, the subset rule and the one confirmation. It
// writes the two keys that decide where a named account's token may be sent
// and which profile serves it, and nothing else — the secret and the converge
// are the second half. The edit is internal/yamledit's and the write is the
// settings commands' shared helper, so this command asks for the same kind of
// change in the same words (D847), and a file this Fugaro cannot read back is
// never written (spec §2.3).

// accountsAddOptions are the flags `accounts add` takes. There is no --json
// and no flag that carries a value: the whole output is a diff a person reads
// (spec §2.1).
type accountsAddOptions struct {
	repos   string
	profile string
	yes     bool
}

func newAccountsAddCmd() *cobra.Command {
	var o accountsAddOptions
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Declare a named Claude account and the repositories its token may be used by",
		Long: `Declare a named Claude OAuth account in this project's local config.

It writes claude_accounts.<name>.allow_data_to — the repositories the account's
token may be used by, and the field every other rule is built on — and adds the
name to one profile's claude_accounts, so the profile's jobs may serve it.

--repos defaults to this checkout's origin repository. A profile that lists
accounts must write its own allow_repos, and every account it lists must cover
every repository on it: an account that covers less is refused before the file
is touched, naming the repositories it is missing, because widening the account
silently is what that rule exists to prevent.

The token itself is a second command's: this one declares the account, and the
converge that stores the secret and deploys the jobs is not built yet.

Run it at your own terminal: this file decides where a credential may be sent,
so a coding agent's session is refused, --yes included.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountsAdd(cmd, args[0], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.repos, "repos", "", "the repositories the token may be used by, as owner/name,owner/name (default: this checkout's origin)")
	f.StringVar(&o.profile, "profile", "", "the profile whose claude_accounts gains the name (default: the only profile, when there is exactly one)")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

// runAccountsAdd is the order spec §2.2 forces: everything that can refuse
// does, and the subset rule is applied to the bytes this would write, before
// the file is read for the write at all.
func runAccountsAdd(cmd *cobra.Command, name string, o accountsAddOptions) error {
	// The name is a DEFINITION and not a reference: the definition's rule is
	// ValidateClaudeAccounts', whose sentences are the ones the loader prints
	// for the same name in the file, so the two cannot part company. It is the
	// definition's and not ValidClaudeAccountRef's because that one answers ""
	// for "default", which is a legal thing for a profile to list and an
	// illegal thing to define.
	if ps := config.ValidateClaudeAccounts(map[string]config.ClaudeAccount{name: {}}); len(ps) > 0 {
		return userErr("%s", ps[0].Message)
	}

	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	repos, err := accountsAddRepos(cmd.Context(), o.repos, name)
	if err != nil {
		return err
	}
	profile, err := accountsAddProfile(o.profile, lc)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}

	// The order is spec §2.3's: the account's own field, then the profile's
	// list, both on the same bytes so the second edit lands beside the first.
	next := old
	for _, repo := range repos {
		if next, err = yamledit.AddListItem(next, []string{"claude_accounts", name, "allow_data_to"}, repo); err != nil {
			return userErr("claude_accounts.%s.allow_data_to: %v", name, err)
		}
	}
	if profile != "" {
		if next, err = yamledit.AddListItem(next, []string{"profiles", profile, "claude_accounts"}, name); err != nil {
			return userErr("profiles.%s.claude_accounts: %v", profile, err)
		}
	}
	// Read the edit back strictly before it is written: a file this Fugaro
	// cannot read is a file the next command of the owner's would refuse, and
	// the refusal belongs here, with nothing written.
	after, err := localcfg.Parse(next)
	if err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	if err := accountsAddSubsetRule(after, name, profile); err != nil {
		return err
	}

	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    accountsAddWhat(name, repos, profile, accountsAddBeyond(cmd.Context(), repos)),
		Kind:    settingsDocLocal,
		Command: accountsAddCommand(name, repos, profile),
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	if profile == "" {
		fmt.Fprintf(cmd.OutOrStdout(), "no profile serves %s yet: this project's jobs do not select it until one does\n", name)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "next: %s is declared, and its token is not stored yet\n", name)
	return nil
}

// accountsAddRepos is the repository list: the flag, or the checkout the
// command runs in. With neither there is no default, and the command refuses
// rather than declaring an account no policy can bound (spec §2.1).
func accountsAddRepos(ctx context.Context, flag, name string) ([]string, error) {
	if flag == "" {
		repo, err := originRepo(ctx)
		if err != nil {
			return nil, userErr("--repos: this checkout has no origin remote to take the repositories from, and claude_accounts.%s.allow_data_to decides where its token may be sent, so an account with none is one no policy can bound: pass --repos owner/name[,owner/name...]", name)
		}
		return []string{repo}, nil
	}
	var out []string
	for _, r := range strings.Split(flag, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		c, err := task.CanonicalRepo(r)
		if err != nil {
			return nil, userErr("--repos: %v", err)
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, userErr("--repos: name at least one repository, as owner/name")
	}
	return out, nil
}

// accountsAddProfile is the profile whose claude_accounts gains the name: the
// flag, or the only profile there is. Two or more without the flag is a
// refusal that lists them, because picking one would be this command choosing
// which jobs may serve a credential. None is not a refusal: the account is
// declared and the command says no job serves it yet (spec §2.1).
func accountsAddProfile(want string, lc *localcfg.Config) (string, error) {
	names := slices.Sorted(maps.Keys(lc.Profiles))
	if want != "" {
		if !slices.Contains(names, want) {
			return "", userErr("--profile %s: this project's config defines %s", pluginwire.Printable(want), accountsAddProfiles(names))
		}
		return want, nil
	}
	switch len(names) {
	case 0:
		return "", nil
	case 1:
		return names[0], nil
	default:
		return "", userErr("this project's config has %d profiles, so name the one whose claude_accounts gains the account: --profile with one of %s", len(names), accountsAddProfiles(names))
	}
}

// accountsAddProfiles names a list of profiles for a refusal, bounded and
// quoted, because a profile's name comes from a file and this text goes to a
// terminal.
func accountsAddProfiles(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, pluginwire.Printable(n))
	}
	if len(out) == 0 {
		return "no profiles"
	}
	return strings.Join(out, ", ")
}

// accountsAddSubsetRule is D3's rule over the config this command would write
// (spec §2.2 step 2): every account a profile lists must cover every
// repository in that profile's own allow_repos. The profile's repositories are
// the intersection of the two and never their union, so an account that covers
// less than the profile admits is refused here, before the file is touched,
// naming what it is missing — silently widening the account is the one thing
// this rule exists to prevent.
func accountsAddSubsetRule(after *localcfg.Config, name, profile string) error {
	if profile == "" || !after.Profiles[profile].AllowReposWritten() {
		return nil
	}
	allow := after.Profiles[profile].AllowRepos
	for _, listed := range after.Profiles[profile].ClaudeAccounts {
		missing := []string{}
		for _, r := range allow {
			if !slices.Contains(after.ClaudeAccounts[listed].AllowDataTo, r) {
				missing = append(missing, r)
			}
		}
		if len(missing) == 0 {
			continue
		}
		covered := slices.Clone(after.ClaudeAccounts[listed].AllowDataTo)
		return userErr("profiles.%s.allow_repos lists %s and claude_accounts.%s.allow_data_to does not cover %s: an account a profile lists covers every repository that profile admits, and the profile's repositories are the intersection of the two and never their union — pass --repos %s to include %s, or drop %s from the profile; nothing was written",
			pluginwire.Printable(profile), strings.Join(missing, ", "), pluginwire.Printable(listed),
			strings.Join(missing, ", "), strings.Join(append(covered, missing...), ","),
			name, pluginwire.Printable(listed))
	}
	return nil
}

// accountsAddBeyond is the repositories --repos names beyond the checkout the
// command runs in: the widening the confirmation says out loud. With no
// checkout there is no default to be measured against, so every named
// repository is one the token may reach from here.
func accountsAddBeyond(ctx context.Context, repos []string) []string {
	here, err := originRepo(ctx)
	if err != nil {
		return slices.Clone(repos)
	}
	out := []string{}
	for _, r := range repos {
		if r != here {
			out = append(out, r)
		}
	}
	return out
}

// accountsAddWhat is the one confirmation's text (D868, spec §2.6): the two
// keys the command writes, and — only when the token may reach a repository
// beyond this checkout — the sentence saying what that widening means. The
// rest of the block is printed either way, so the confirmation a person reads
// is never a bare y.
func accountsAddWhat(name string, repos []string, profile string, beyond []string) string {
	what := fmt.Sprintf("declare claude_accounts.%s.allow_data_to = [%s]", pluginwire.Printable(name), strings.Join(repos, ", "))
	if profile != "" {
		what += fmt.Sprintf("; profile profiles.%s.claude_accounts += %s", pluginwire.Printable(profile), pluginwire.Printable(name))
	}
	if len(beyond) > 0 {
		what += fmt.Sprintf("; allow a launcher that may start a run on %s can have that run print %s's token", strings.Join(beyond, ", "), pluginwire.Printable(name))
	}
	return what
}

// accountsAddCommand is the command a refusal in a coding agent's session
// prints for the person to run at their own terminal: this same edit, spelled
// whole, because nothing they type here is remembered.
func accountsAddCommand(name string, repos []string, profile string) string {
	parts := []string{"fugaro", "accounts", "add", quoteWord(name), "--repos", quoteWord(strings.Join(repos, ","))}
	if profile != "" {
		parts = append(parts, "--profile", quoteWord(profile))
	}
	return strings.Join(parts, " ")
}
