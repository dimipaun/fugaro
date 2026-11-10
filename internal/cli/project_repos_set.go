package cli

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is D1173's writer for the two shapes a bare scalar cannot express
// (docs/config-management/spec.md §3.4): a job profile's block — the seven keys
// the registry spells `job_profiles.*` (internal/settings/registry.go:647) and
// this machine's project config spells `profiles.*` — and a repository's
// `workflows` selection. Both are entries of an object list addressed by NAME
// and never by position (D1163), and both are written into the local project
// config, the file `fugaro project policy publish` builds the node from (the
// profiles) and `fugaro doctor` reads a repository's selection out of.
//
// The `repos.*` scalars and lists are NOT here: D1173 hands them to the generic
// writer, so they are `fugaro settings set repos.<owner/name>.<key>` — the same
// allowlist, the same refusals, the same write path. What this command adds is
// the two edits a scalar write cannot make: a block of seven keys at once under
// a name, and a whole list in place of the one item an --add or a --remove
// moves.
//
// The write is the settings path's own (applySettingsEdit: the diff, the
// sentence, the coding-agent-session refusal, the backup, the byte
// compare-and-swap), read through the managed gate exactly as `settings set` is
// (settingsManagedStep, gate.recorded) and held against the file's own loader
// first (settingsReparse). It is SEC for the gate's reason: a profile carries
// `secrets` and `allow_repos`, the lists a run's mounts and its admission are
// decided by, so a coding agent's session is refused this write — by the gate
// in a managed project, and by the settings path's own fence everywhere else.

func newProjectReposSetCmd() *cobra.Command {
	var o projectReposSetOptions
	cmd := &cobra.Command{
		Use:   "set (REPO --workflows NAME[,NAME...]) | (--job-profile NAME [members])",
		Short: "Write a repository's workflow selection or a job profile, in this machine's project config",
		Long: `Write one of the two shapes the generic writer cannot express, in this
machine's project config:

    fugaro project repos set acme/app --workflows web,docs
    fugaro project repos set --job-profile gpu --provider github --cpu 4 \
        --memory 8Gi --timeout-class 12h --secrets acme-db --allow-repos acme/app

A repository's workflows selection says which of the repository's own workflows
this project runs, and it is written WHOLE: the names given are the selection,
so a second run replaces it rather than adding to it. The repository is one the
project's config lists (fugaro update --repo onboards one); a name it does not
list is refused, because a selection stored under a name nothing looks up is a
line nobody sees fail.

A job profile is what a Cloud Run job is deployed as, addressed by name
(D1163): the seven keys are provider, cpu, memory, timeout_class, secrets,
allow_repos and claude_accounts, and the flags below write them. The block is
created when the config does not hold one under that name, and a NEW profile
must name its own --provider: a profile with no provider is one no job of it can
be deployed, and fugaro project policy publish refuses such a policy.

What a profile's values owe — a known provider, a cpu/memory shape Cloud Run
serves, a duration timeout_class — is the policy's own rule
(projectpolicy.validProfiles), applied by fugaro project policy publish and read
back by every run; this command writes the file and refuses only what the file
itself would not read.

The repos.* scalars and lists are the generic writer's (D1173):

    fugaro settings set repos.acme/app.base_branch main
    fugaro settings set repos.acme/app.workflows --add docs

This write is a person's own: fugaro update publishes it, and a coding agent's
session is refused it, in a managed project by that project's gate.

Every write ends with one line: next: fugaro update.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectReposSet(cmd, args, o)
		},
	}
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.jobProfile, "job-profile", "", "the job profile to write, by name; with it, the members below write the block's keys")
	f.StringVar(&o.provider, "provider", "", "profiles.NAME.provider: the git provider the profile's job holds a credential for (required for a new profile)")
	f.IntVar(&o.cpu, "cpu", 0, "profiles.NAME.cpu: the Cloud Run cpu count the job is deployed with")
	f.StringVar(&o.memory, "memory", "", "profiles.NAME.memory: the Cloud Run memory the job is deployed with, e.g. 4Gi")
	f.StringVar(&o.timeoutClass, "timeout-class", "", "profiles.NAME.timeout_class: the job's task timeout as a duration, e.g. 6h or 90m")
	f.StringSliceVar(&o.secrets, "secrets", nil, "profiles.NAME.secrets: the Secret Manager names the profile's job mounts, comma-separated; the list is written whole")
	f.StringSliceVar(&o.allowRepos, "allow-repos", nil, "profiles.NAME.allow_repos: the repositories the profile serves, comma-separated; the list is written whole")
	f.StringSliceVar(&o.claudeAccounts, "claude-accounts", nil, "profiles.NAME.claude_accounts: the Claude accounts the profile serves, comma-separated; the list is written whole")
	f.StringSliceVar(&o.workflows, "workflows", nil, "repos.REPO.workflows: which of the repository's own workflows this project runs, comma-separated; the selection is written whole")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this write would otherwise get instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back. Refused where nobody can answer it (a coding agent's session, a pipe, --non-interactive); a no-op with a sentence in a project that is not managed; refused beside --yes")
	return cmd
}

// projectReposSetOptions are the command's flags: the two shapes' own, and the
// settings family's --yes and --ask (management_ask.go), which is what the
// write path's own question is answered by.
type projectReposSetOptions struct {
	project string
	config  string
	yes     bool
	ask     bool
	// jobProfile is the profile the write addresses by name (D1163). It is what
	// chooses between the command's two shapes: with it the members below are
	// the block's keys, and without it the one argument is a repository whose
	// workflows --workflows replaces.
	jobProfile     string
	provider       string
	cpu            int
	memory         string
	timeoutClass   string
	secrets        []string
	allowRepos     []string
	claudeAccounts []string
	workflows      []string
}

// projectReposMember is one key of a profile's block: the field as the command
// line spells it, the path under the profile's name, and either a scalar or the
// whole list a list flag names.
type projectReposMember struct {
	field string
	path  string
	value string
	// list is the whole list when the member is one, and nil for a scalar: a
	// list is written as the members it names and not as a string.
	list []string
}

// runProjectReposSet is `project repos set`: the shape is the flags', and each
// shape's own function writes it.
func runProjectReposSet(cmd *cobra.Command, args []string, o projectReposSetOptions) error {
	if err := askConflict(askFlags{ask: o.ask, yes: o.yes}); err != nil {
		return err
	}
	sel, lc, err := settingsAimedSelection(settingsWriteOptions{project: o.project, config: o.config})
	if err != nil {
		return err
	}
	target := settingsTarget{layer: settingsLayerLocal, kind: settingsDocLocal, path: sel.Path, project: lc.Name}
	if o.jobProfile == "" {
		if len(args) != 1 {
			return userErr("fugaro project repos set writes one of two shapes: a repository's workflow selection (`fugaro project repos set owner/name --workflows web,docs`) or a job profile (`fugaro project repos set --job-profile NAME --provider github`)")
		}
		return runProjectReposSetWorkflows(cmd, args[0], o, sel, lc, target)
	}
	if len(args) > 0 {
		return userErr("--job-profile addresses a job profile by name, so it takes no repository argument: `fugaro project repos set %s --workflows web` writes that repository's selection and `fugaro project repos set --job-profile NAME --provider github` writes a profile", quoteWord(args[0]))
	}
	if len(o.workflows) > 0 {
		return userErr("--workflows is a repository's selection and --job-profile names a job profile: a profile's keys are --provider, --cpu, --memory, --timeout-class, --secrets, --allow-repos and --claude-accounts, and `fugaro project repos set owner/name --workflows web,docs` writes the selection")
	}
	return runProjectReposSetProfile(cmd, o, sel, lc, target)
}

// runProjectReposSetProfile writes a job profile's block: the keys the flags
// name, under the name --job-profile addresses, in this machine's project
// config.
func runProjectReposSetProfile(cmd *cobra.Command, o projectReposSetOptions, sel localcfg.Selection, lc *localcfg.Config, target settingsTarget) error {
	name := o.jobProfile
	if !config.ProjectNameRE.MatchString(name) {
		return userErr("--job-profile %s: a profile name must be 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit", safetext.Field(name))
	}
	members := projectReposProfileMembers(cmd, o)
	if len(members) == 0 {
		return userErr("--job-profile %s writes the block's keys and none was named: name at least one of --provider, --cpu, --memory, --timeout-class, --secrets, --allow-repos and --claude-accounts (the block is what `fugaro settings` reads and `fugaro project policy publish` publishes)", pluginwire.Printable(name))
	}
	// A name the config does not define is a profile this write CREATES, and a
	// profile is whole only with its provider: the key is required by the
	// policy's own rules (projectpolicy.validProfiles), so a create that leaves
	// it out writes a block whose every publish is refused and whose every run
	// is one no job exists for. The refusal names the profiles the file holds,
	// as the accounts family's --profile does for the same block.
	if _, defined := lc.Profiles[name]; !defined && !cmd.Flags().Changed("provider") {
		return userErr("profiles.%s is not a profile this project defines (it defines %s): a new profile is created with the keys that make it whole, --provider first — a profile with no provider is one no job of it can be deployed, and `fugaro project policy publish` refuses it",
			pluginwire.Printable(name), projectReposProfileNames(lc))
	}
	target.keyPath = []string{"profiles", name}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	next, err := projectReposSetMembers(old, target.keyPath, members)
	if err != nil {
		return err
	}
	return projectReposSetApply(cmd, projectReposSetWrite{
		target:     target,
		sel:        sel,
		lc:         lc,
		key:        "job_profiles." + name,
		old:        old,
		next:       next,
		what:       fmt.Sprintf("write profiles.%s in %s", name, target.path),
		command:    projectReposSetProfileCommand(name, members, o),
		subcommand: "project repos set",
		o:          o,
	})
}

// runProjectReposSetWorkflows writes a repository's workflow selection: the
// whole list under the entry the config spells, for a repository the project's
// config lists.
func runProjectReposSetWorkflows(cmd *cobra.Command, repo string, o projectReposSetOptions, sel localcfg.Selection, lc *localcfg.Config, target settingsTarget) error {
	if len(o.workflows) == 0 {
		return userErr("`fugaro project repos set %s` writes the repository's workflow selection: name it with --workflows web,docs", quoteWord(repo))
	}
	for _, name := range o.workflows {
		if name == "" {
			return userErr("--workflows names the workflows the project runs, one name per member, and this list has an empty member")
		}
		if !config.WorkflowNameRE.MatchString(name) {
			return userErr("--workflows %s: a workflow name is 1 to 20 characters of a-z, 0-9 and '-', starting with a letter, and it is the name a run names on the command line", safetext.Field(name))
		}
	}
	resolved, err := repoArg(cmd.Context(), settingsAim(o.project, o.config), repo)
	if err != nil {
		return err
	}
	canon, err := task.CanonicalRepo(resolved)
	if err != nil {
		return userErr("the repository %s: %v", pluginwire.Printable(repo), err)
	}
	entry, known := projectReposEntry(lc, canon)
	if !known {
		return userErr("the repository %s is not one of project %s's repositories in the local config, so no run would ever read this selection: `fugaro update --repo` in its checkout onboards it, and `fugaro project repos ls` lists what the project has",
			pluginwire.Printable(canon), pluginwire.Printable(lc.Name))
	}
	target.keyPath = []string{"repos", entry}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	next := old
	if next, err = projectReposSetList(next, append(slices.Clone(target.keyPath), "workflows"), o.workflows); err != nil {
		return userErr("repos.%s.workflows: %v", entry, err)
	}
	return projectReposSetApply(cmd, projectReposSetWrite{
		target:     target,
		sel:        sel,
		lc:         lc,
		key:        "repos." + entry + ".workflows",
		old:        old,
		next:       next,
		what:       fmt.Sprintf("write repos.%s.workflows in %s", entry, target.path),
		command:    projectReposSetWorkflowsCommand(entry, o),
		subcommand: "project repos set",
		o:          o,
	})
}

// projectReposEntry is the key the config spells the repository under, and
// whether it lists it at all: the entry is addressed by name, so a file that
// spells the name some other way (a checkout path it was enrolled by) keeps its
// own spelling rather than gaining a second entry beside it.
func projectReposEntry(lc *localcfg.Config, canon string) (string, bool) {
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		if name == canon {
			return name, true
		}
		if c, err := task.CanonicalRepo(name); err == nil && c == canon {
			return name, true
		}
	}
	return "", false
}

// projectReposProfileMembers reads the profile flags into the block's keys, in
// the order the registry spells them. A flag that was not given writes nothing:
// a profile is edited key by key, and a member the command line did not name is
// left exactly as the file holds it.
func projectReposProfileMembers(cmd *cobra.Command, o projectReposSetOptions) []projectReposMember {
	f := cmd.Flags()
	var out []projectReposMember
	add := func(field, flag, value string) {
		if f.Changed(flag) {
			out = append(out, projectReposMember{field: field, path: field, value: value})
		}
	}
	add("provider", "provider", o.provider)
	if f.Changed("cpu") {
		out = append(out, projectReposMember{field: "cpu", path: "cpu", value: strconv.Itoa(o.cpu)})
	}
	add("memory", "memory", o.memory)
	add("timeout_class", "timeout-class", o.timeoutClass)
	for _, l := range []struct {
		field  string
		flag   string
		values []string
	}{
		{"secrets", "secrets", o.secrets},
		{"allow_repos", "allow-repos", o.allowRepos},
		{"claude_accounts", "claude-accounts", o.claudeAccounts},
	} {
		if f.Changed(l.flag) {
			out = append(out, projectReposMember{field: l.field, path: l.field, list: l.values})
		}
	}
	return out
}

// projectReposSetMembers applies one profile's keys to the document: the
// scalars with SetScalar, the lists whole.
func projectReposSetMembers(old []byte, block []string, members []projectReposMember) ([]byte, error) {
	next := old
	var err error
	for _, m := range members {
		path := append(slices.Clone(block), m.path)
		if m.list != nil {
			if next, err = projectReposSetList(next, path, m.list); err != nil {
				return old, userErr("profiles.%s.%s: %v", block[1], m.field, err)
			}
			continue
		}
		if next, err = yamledit.SetScalar(next, path, m.value); err != nil {
			return old, userErr("profiles.%s.%s: %v", block[1], m.field, err)
		}
	}
	return next, nil
}

// projectReposSetList writes a list WHOLE: every member the key holds that the
// list does not name is removed, and every member it names is added. It is what
// makes the flags of this command different from the generic writer's --add and
// --remove, which move one item an invocation: a selection is the list itself,
// and an operator who names three workflows means three.
//
// A list written with nothing in it is not expressible here, and that is the
// loader's own rule rather than this command's: an absent key and an empty one
// mean different things (a repository's `workflows:` with nothing under it, a
// profile's `allow_repos:` with nothing in it is refused outright), so the way
// to take a member away is `fugaro settings unset`.
func projectReposSetList(data []byte, path []string, wanted []string) ([]byte, error) {
	var have []string
	if current := settingsValueIn(data, path); current != "" {
		have = strings.Split(current, ",")
	}
	out := data
	var err error
	for _, item := range have {
		if slices.Contains(wanted, item) {
			continue
		}
		if out, err = yamledit.RemoveListItemWithOptions(out, path, item, yamledit.RemoveOptions{DropEmptyParents: true}); err != nil {
			return data, err
		}
	}
	for _, item := range wanted {
		if slices.Contains(have, item) {
			continue
		}
		if out, err = yamledit.AddListItem(out, path, item); err != nil {
			return data, err
		}
	}
	return out, nil
}

// projectReposProfileNames is the profiles the config defines, for a refusal
// that has to say what the file holds instead.
func projectReposProfileNames(lc *localcfg.Config) string {
	if len(lc.Profiles) == 0 {
		return "none"
	}
	return strings.Join(slices.Sorted(maps.Keys(lc.Profiles)), ", ")
}

// projectReposSetWrite is one pending edit of this machine's project config.
type projectReposSetWrite struct {
	target settingsTarget
	sel    localcfg.Selection
	lc     *localcfg.Config
	// key is the settings key this write is about, in the registry's own
	// spelling (job_profiles.NAME, repos.REPO.workflows): it is what the managed
	// gate's record names, and what a coding agent's session reads in the
	// allowlist's refusal.
	key  string
	old  []byte
	next []byte
	// what is the sentence naming what changes, command the same edit spelled
	// for a person's own terminal, and subcommand the name the gate's record
	// carries.
	what       string
	command    string
	subcommand string
	o          projectReposSetOptions
}

// projectReposSetApply is the write both shapes make: the loader's own read of
// the edited bytes, the managed gate's read, the write path and the record of
// it — the same four steps `settings set` takes, in the same order.
func projectReposSetApply(cmd *cobra.Command, w projectReposSetWrite) error {
	if !yamledit.Changed(w.old, w.next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: already reads what was asked for\n", w.target.path)
		return nil
	}
	if err := settingsReparse(w.target, w.next); err != nil {
		return err
	}
	edit := settingsEdit{
		Path:    w.target.path,
		Old:     w.old,
		New:     w.next,
		What:    w.what,
		Kind:    settingsDocLocal,
		Command: w.command,
		Yes:     w.o.yes,
		Ask:     w.o.ask,
	}
	// The gate is read where this command would ask, exactly as `settings set`
	// on this document reads it: the local project config is not a repository's
	// own fugaro.yaml, so a coding agent's session meets the gate's own
	// allowlist and is refused with the record of the refusal (D1014), and the
	// owner's terminal writes on the gate's verdict.
	managed, gate, err := settingsManagedStep(cmd.Context(), w.lc, w.sel.Path, w.target, w.key, false, "", w.old, w.next, w.subcommand, settingsGateReads(cmd, w.o.yes, w.o.ask, false))
	if err != nil {
		return err
	}
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: w.o.ask, yes: w.o.yes}, managed.Managed, settingsEditConditions(cmd, edit)); err != nil {
		return err
	}
	edit.Managed = managed
	if err := applySettingsEdit(cmd, w.lc, edit); err != nil {
		return err
	}
	if err := gate.recorded(cmd.Context()); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// projectReposSetProfileCommand is the profile edit spelled for a person's own
// terminal, which is what a refusal prints (settingsEdit's Command): the keys
// this run was asked for, in the order the block spells them, and never a key
// the command line did not name.
func projectReposSetProfileCommand(name string, members []projectReposMember, o projectReposSetOptions) string {
	parts := []string{"fugaro", "project", "repos", "set", "--job-profile", quoteWord(name)}
	for _, m := range members {
		value := m.value
		if m.list != nil {
			value = strings.Join(m.list, ",")
		}
		parts = append(parts, "--"+strings.ReplaceAll(m.field, "_", "-"), quoteWord(value))
	}
	return strings.Join(append(parts, projectReposAimArgs(o)...), " ")
}

// projectReposSetWorkflowsCommand is the selection edit spelled for a person's
// own terminal.
func projectReposSetWorkflowsCommand(repo string, o projectReposSetOptions) string {
	parts := []string{"fugaro", "project", "repos", "set", quoteWord(repo), "--workflows", quoteWord(strings.Join(o.workflows, ","))}
	return strings.Join(append(parts, projectReposAimArgs(o)...), " ")
}

// projectReposAimArgs is the aim that travelled with the command line, so a
// refusal prints the command that would land where this one tried to.
func projectReposAimArgs(o projectReposSetOptions) []string {
	var out []string
	if o.project != "" {
		out = append(out, "--project", quoteWord(o.project))
	}
	if o.config != "" {
		out = append(out, "--config", quoteWord(o.config))
	}
	return out
}
