package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
	claude  bool
	yes     bool
	// ask is --ask (the owner's rule of 2026-11-04, and --yes's symmetric
	// half): in a project whose marks say it is managed the declaration and
	// the converge below are written on the gate's verdict with no prompt, and
	// the flag asks the y/N this file has always asked instead. Unmanaged, it
	// says so and changes nothing; a session that could not answer refuses
	// (managed_ask.go). It is refused beside --yes.
	ask bool
}

func newAccountsAddCmd() *cobra.Command {
	var o accountsAddOptions
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Declare a named Claude account and the repositories its token may be used by",
		Long: `Declare a named Claude OAuth account in this project, and converge the
installation on it.

It writes claude_accounts.<name>.allow_data_to — the repositories the account's
token may be used by, and the field every other rule is built on — and adds the
name to one profile's claude_accounts, so the profile's jobs may serve it.

--repos defaults to this checkout's origin repository. A profile that lists
accounts must write its own allow_repos, and every account it lists must cover
every repository on it: an account that covers less is refused before the file
is touched, naming the repositories it is missing, because widening the account
silently is what that rule exists to prevent.

Then it converges, in one command and behind the one confirmation above: the
token is stored project-wide as a new version of the account's own secret, the
account's broken mark is cleared, and the publisher, the job set and this
checkout's repository root are converged — the same stages fugaro update runs,
invoked here rather than forked. It ends on the line that says what is left,
which is a fugaro run and never an update.

--claude runs Anthropic's own claude setup-token on your own terminal first,
so the login is the one you would have run yourself: it gets your stdin, stdout
and stderr, and fugaro reads none of what it prints. The token is then read at
the hidden prompt below, exactly as it is without the flag — that prompt is the
only source of the value, so a login you cancelled or that failed costs one
paste rather than the command. A login that failed stores nothing and still
leaves you the prompt.

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
	f.BoolVar(&o.claude, "claude", false, "run Anthropic's own claude setup-token login on this terminal first, then paste the token at the hidden prompt")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this declaration would otherwise get instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back. Refused where nobody can answer it (a coding agent's session, a pipe); a no-op with a sentence in a project that is not managed; refused beside --yes")
	return cmd
}

// runAccountsAdd is the order spec §2.2 forces: everything that can refuse
// does, and the subset rule is applied to the bytes this would write, before
// the file is read for the write at all.
func runAccountsAdd(cmd *cobra.Command, name string, o accountsAddOptions) error {
	if err := askConflict(askFlags{ask: o.ask, yes: o.yes}); err != nil {
		return err
	}
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

	// A name this file already defines is a rotation (spec §2.2 step 1): the
	// token written for it replaces the one in use, and the confirmation says
	// so. It is read from the config as it was, before the edit above.
	_, rotation := lc.ClaudeAccounts[name]
	beyond := accountsAddBeyond(cmd.Context(), repos)
	what := accountsAddWhat(after, name, profile, beyond, rotation)
	command := accountsAddCommand(name, repos, profile, o.claude)
	changed := yamledit.Changed(old, next)

	// The managed gate, before anything is screened or written
	// (accounts_managed.go; the managed plan's §2 accounts row): in a managed
	// project's coding agent's session it decides whether this declaration is a
	// step the session may write, and it refuses a widening and a token entry
	// here — before the login is screened and before the diff is shown — so a
	// refusal leaves the file exactly as it was. Everywhere else it answers the
	// zero Managed, which is every fence below unchanged.
	m, gate, err := accountsAddGate(cmd.Context(), cmd, lc, o, name, profile, beyond, next)
	if err != nil {
		return err
	}
	// --ask, after the gate's own decision and before anything is screened,
	// run or written: the line it prints, or the refusal that stops this
	// command where nobody could answer the question it restores
	// (managed_ask.go). A gate refusal above is still the gate's own sentence.
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: o.ask, yes: o.yes}, m.Managed, askConditions(cmd, false, false)); err != nil {
		return err
	}

	// §3's act one is screened here and run below, and the split is the owner's
	// ruling of 2026-11-02: everything that can refuse the login — a coding
	// agent's session, a terminal that is not there, an exported credential, a
	// claude fugaro will not run — happens before the diff and before the
	// confirmation, so nobody is asked to answer for a login that cannot happen
	// and every refusal leaves the file exactly as it was.
	var claudeBin string
	if o.claude {
		if claudeBin, err = accountsAddClaudePrepare(cmd, sel.Path, what, command); err != nil {
			return err
		}
		// The block a person answers names the program it is about to run, at
		// the path it resolved to, so the answer is for the login itself and not
		// for a sentence about it (spec §3.1 step 1).
		what += fmt.Sprintf("; run %s setup-token on this terminal", pluginwire.Printable(claudeBin))
	}

	// Managed is the gate's answer above, passed into the one write path: its
	// zero value is what every other caller of that helper passes, and it is the
	// refusal this file has always made, byte for byte.
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    settingsDocLocal,
		Managed: m,
		Command: command,
		Yes:     o.yes,
		Ask:     o.ask,
	}); err != nil {
		return err
	}
	// The record of the admission, written AFTER the write it was admitted for
	// and never before it (D1014, spec §6): a step the gate admitted that never
	// reached its write leaves no record at all, which is why this is here and
	// not beside the admit above.
	if changed {
		if err := gate.recorded(cmd.Context()); err != nil {
			return err
		}
	}
	// A file that was already what this command would write — a rotation
	// re-run, the account declared and the profile serving it — returns from
	// applySettingsEdit before its refusal and before its own sentence, and
	// both are still owed here: the converge below reads the token from stdin,
	// stores a new version of it project-wide and clears the account's mark,
	// which is the write the refusal is about and the change the sentence names.
	// A session writes nothing, --yes included (spec §2.4), and the marker fence
	// comes first, as it does in applySettingsEdit — the same rule, lifted by
	// the same answer and for the same class, so a session the gate admitted to
	// the declaration is not refused the step it declared.
	if !changed {
		if marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); marker != "" && !initflow.CanConfirmManaged(initflow.YesOrTyped, initflow.Conditions{Agent: marker}, m) {
			return userErr("refusing to write %s in a coding agent's session (%s is set): %s; run this yourself at your own terminal: %s; nothing was written",
				sel.Path, marker, what, command)
		}
		if err := announceSettingsWrite(cmd, what); err != nil {
			return err
		}
	}
	// §3's act one itself, after the one confirmation and before the token is
	// stored below: the login runs on the owner's own terminal because they
	// answered for it, and what it prints is still not the value that is kept —
	// the hidden prompt below remains the only token path.
	if o.claude {
		if err := accountsAddClaudeRun(cmd, claudeBin); err != nil {
			return err
		}
	}
	// The token, which is the one step of this command no gate admits: the value
	// is a secret entering the project, and a secret is the class a plan gate
	// never judges (D1026). It is refused here — before the converge reads it,
	// stores it and converges on it — so a coding agent's session is left with
	// the declaration and nothing else, and with the sentence naming the command
	// its owner runs at their own terminal (spec §4.2).
	if marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); marker != "" {
		return accountsAddTokenStep(marker, name, command)
	}
	if profile == "" {
		fmt.Fprintf(cmd.OutOrStdout(), "no profile serves %s yet: this project's jobs do not select it until one does\n", name)
	}
	// The converge (spec §2.2 steps 5–8). It runs on the file the edit above
	// wrote — the stages read it, and the account is in it — and on its bytes,
	// which the engines take for the same reason `fugaro update` does.
	return accountsAddConverge(cmd.Context(), cmd, after, sel.Path, next, name, profile, repos)
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
	if profile == "" {
		return nil
	}
	allow := after.Profiles[profile].AllowRepos
	// The rule's own precondition, and the reason it is checked here rather
	// than left to the publish: a profile that lists accounts writes its own
	// list, exactly. An absent one means the project's enrollment, which no
	// account is required to cover, so which repositories the profile admits
	// would be undecidable against the accounts it serves; and a wildcard
	// makes the same question undecidable repository by repository. Both are
	// states `project policy publish` refuses (projectpolicy.validProfileAccounts),
	// and this command would otherwise write a config the owner's very next
	// publish refuses.
	if len(allow) == 0 {
		return userErr("profiles.%s.claude_accounts: a profile that lists accounts must write its own allow_repos, and this one writes none: an absent list means the project's enrollment, which no account is required to cover, so which repositories the profile admits is undecidable — add allow_repos to profiles.%s, naming the repositories it admits, then run this again; nothing was written",
			pluginwire.Printable(profile), pluginwire.Printable(profile))
	}
	for _, pattern := range allow {
		if strings.Contains(pattern, "*") {
			return userErr("profiles.%s.allow_repos carries the wildcard %s, and a profile that lists accounts must name exact repositories: whether an account covers this profile is decided repository by repository, and a wildcard makes that undecidable — write the repositories themselves, then run this again; nothing was written",
				pluginwire.Printable(profile), pluginwire.Printable(pattern))
		}
	}
	for _, listed := range after.Profiles[profile].ClaudeAccounts {
		covered := after.ClaudeAccounts[listed].AllowDataTo
		missing := []string{}
		for _, r := range allow {
			if !accountsAddCovers(covered, after.Profiles[profile].Provider, r) {
				missing = append(missing, r)
			}
		}
		if len(missing) == 0 {
			continue
		}
		head := fmt.Sprintf("profiles.%s.allow_repos lists %s and claude_accounts.%s.allow_data_to does not cover %s: an account a profile lists covers every repository that profile admits, and the profile's repositories are the intersection of the two and never their union — ",
			pluginwire.Printable(profile), strings.Join(missing, ", "), pluginwire.Printable(listed), strings.Join(missing, ", "))
		// The remedy is the failing account's own: --repos writes the
		// allow_data_to of the account named on the command line, so a
		// profile's pre-existing account is not this command's to widen, and
		// offering the flag for it would send the person to the wrong
		// account's list — the one thing this rule exists to prevent.
		if listed != name {
			return userErr("%sclaude_accounts.%s is not the account this command is adding: widen claude_accounts.%s.allow_data_to with the repositories above, narrow profiles.%s.allow_repos, or drop %s from the profile; nothing was written",
				head, pluginwire.Printable(listed), pluginwire.Printable(listed), pluginwire.Printable(profile), pluginwire.Printable(listed))
		}
		return userErr("%spass --repos %s to include %s, or drop %s from the profile; nothing was written",
			head, strings.Join(append(slices.Clone(covered), missing...), ","), pluginwire.Printable(name), pluginwire.Printable(listed))
	}
	return nil
}

// accountsAddCovers reports whether an account's allow_data_to covers one
// entry of the profile's own allow_repos.
//
// The two lists spell a repository differently, and the difference is the whole
// reason this is a function: allow_data_to holds `owner/name` slugs — that is
// the shape config.ValidateClaudeAccounts holds it to, and the shape --repos
// writes — while a profile's allow_repos holds the canonical
// `provider/owner/name`, which is what the published node carries and what
// projectpolicy.ValidPattern validates. So the question "does this account
// cover this repository" is asked through projectpolicy.Canonical, the one
// function publish derives accounts.<name>.allow_repos with, and an entry that
// is already canonical matches literally too. Comparing the two lists
// string-against-string is what refused a real project config: its profile
// admits github/owner/repo-a, the account's allow_data_to says owner/repo-a,
// and the string comparison reads that as "not covered".
func accountsAddCovers(covered []string, provider, pattern string) bool {
	for _, slug := range covered {
		if slug == pattern {
			return true
		}
		if canonical, err := projectpolicy.Canonical(provider, slug); err == nil && canonical == pattern {
			return true
		}
	}
	return false
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

// accountsAddWhat is the one confirmation's text (D868, spec §2.6): every
// change the command makes — the two keys it writes, the secret it stores
// project-wide, the policy node it publishes, the job it deploys — and, only
// when the token may reach a repository beyond this checkout, the sentence
// saying what that widening means. The rest of the block is printed either way,
// so the confirmation a person reads is never a bare y.
//
// It is one confirmation for the whole command and not two: the converge is
// what --yes covers here, and a person who read this block and answered it has
// answered for the stages too (spec §2.5).
//
// The list is the one the account ends up with, read back from the config this
// would write, and not the --repos it was given: re-declaring an account adds
// to the list it already has, and a confirmation that named the flag would
// print a shorter list than the file it asks to write. The published list is
// the derivation's — the account's repositories qualified with the provider of
// the profile that serves it — so what the node will carry is stated, not
// guessed.
func accountsAddWhat(lc *localcfg.Config, name, profile string, beyond []string, rotation bool) string {
	allow := lc.ClaudeAccounts[name].AllowDataTo
	what := fmt.Sprintf("declare claude_accounts.%s.allow_data_to = [%s]", pluginwire.Printable(name), strings.Join(allow, ", "))
	if rotation {
		what += " (a rotation: the account already exists, and the token written for it replaces the one in use)"
	}
	if profile != "" {
		what += fmt.Sprintf("; profile profiles.%s.claude_accounts += %s", pluginwire.Printable(profile), pluginwire.Printable(name))
	}
	what += fmt.Sprintf("; secret a new version of %s (project-wide)", config.ClaudeAccountSecret(name))
	if repos, ok := accountsAddPublishedRepos(lc, name); ok {
		what += fmt.Sprintf("; publish accounts.%s.allow_repos = [%s] into the policy node", pluginwire.Printable(name), strings.Join(repos, ", "))
	} else {
		what += "; publish the policy node from the config this writes"
	}
	if profile != "" {
		what += fmt.Sprintf("; deploy %s (and its service account)",
			pluginwire.Printable(gcp.ProjectJobName(profile, lc.Profiles[profile].Provider, name)))
	} else {
		what += "; deploy no job: no profile serves it yet"
	}
	if len(beyond) > 0 {
		what += fmt.Sprintf("; a launcher that may start a run on %s can have that run print %s's token", strings.Join(beyond, ", "), pluginwire.Printable(name))
	}
	return what
}

// accountsAddPublishedRepos is the list accounts.<name>.allow_repos will carry,
// through the derivation the Policy stage itself publishes with
// (projectPublishMembers): ok is false for an account no profile names, which
// has no entry in the block at all.
func accountsAddPublishedRepos(lc *localcfg.Config, name string) ([]string, bool) {
	members, err := projectPublishMembers(lc)
	if err != nil {
		return nil, false
	}
	acc, ok := members[name]
	return acc.AllowRepos, ok
}

// accountsAddCommand is the command a refusal in a coding agent's session
// prints for the person to run at their own terminal: this same edit, spelled
// whole, because nothing they type here is remembered. It carries --claude when
// the run that was refused asked for it: the login is half of what that run
// would have done, and a command without the flag would send the person to a
// different one.
func accountsAddCommand(name string, repos []string, profile string, claude bool) string {
	parts := []string{"fugaro", "accounts", "add", quoteWord(name), "--repos", quoteWord(strings.Join(repos, ","))}
	if claude {
		parts = append(parts, "--claude")
	}
	if profile != "" {
		parts = append(parts, "--profile", quoteWord(profile))
	}
	return strings.Join(parts, " ")
}
