package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// `fugaro project profile`: the project's job profiles, which are the shapes
// its Cloud Run jobs are deployed as — one job per (profile, provider, account),
// with the profile's cpu, memory, timeout and secrets on it (design
// project-centric §4.1). The block lives in the operator's own config, reaches
// a run through the published policy, and is deployed by `fugaro update`
// (spec §5, row G6).
//
// The file is the whole of what these commands write, and they write it through
// the one editor and the one write path (settings plan F4): yamledit splices
// the keys it changes and keeps every comment and every other byte, and
// applySettingsEdit carries the diff, the coding-agent-session refusal, the
// backup and the atomic write. A profile this door writes is held to the rule
// the publisher itself applies (projectpolicy.CheckProfiles over the accounts
// the same config derives), so a profile the next `fugaro update` would refuse
// cannot be written here.

func newProjectProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "The project's job profiles: the shapes its jobs are deployed as",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectProfileLsCmd(), newProjectProfileShowCmd(),
		newProjectProfileAddCmd(), newProjectProfileSetCmd(), newProjectProfileRmCmd())
	return cmd
}

type projectProfileReadOptions struct {
	json bool
}

func newProjectProfileLsCmd() *cobra.Command {
	var o projectProfileReadOptions
	cmd := &cobra.Command{
		Use:   "ls [--json]",
		Short: "List this project's job profiles, and the jobs each one maps to",
		Long: "List the job profiles this project's own config defines.\n\n" +
			"Each line is one profile: the provider and the Cloud Run shape its job is\n" +
			"deployed as, the secrets the job mounts (by NAME — a config carries no value),\n" +
			"the repositories it admits, and the Claude accounts it serves. The line after\n" +
			"it says which jobs it maps to: one per (profile, provider, account).\n\n" +
			"A config with no profiles: block defines none, and a policy built from it\n" +
			"sets no profile at all — every workflow then runs on the job its own\n" +
			"environment names.\n\n" +
			"The read is offline: it opens this machine's project config and nothing else.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectProfileLs(cmd, o.json) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print machine-readable output")
	return cmd
}

func newProjectProfileShowCmd() *cobra.Command {
	var o projectProfileReadOptions
	cmd := &cobra.Command{
		Use:   "show [NAME] [--json]",
		Short: "Show one job profile of this project",
		Long: "Show one job profile of this project's own config, with the jobs it maps to.\n\n" +
			"Without NAME it shows standard, the profile every workflow that names none\n" +
			"runs on (a policy that carries profiles must define it, which is why a\n" +
			"config that sets no profile at all is refused here rather than shown empty).\n\n" +
			"The read is offline: it opens this machine's project config and nothing else.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runProjectProfileShow(cmd, name, o.json)
		},
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print machine-readable output")
	return cmd
}

type projectProfileAddOptions struct {
	provider     string
	cpu          int
	memory       string
	timeoutClass string
	yes          bool
}

func newProjectProfileAddCmd() *cobra.Command {
	var o projectProfileAddOptions
	cmd := &cobra.Command{
		Use:   "add NAME --provider P --cpu N --memory M --timeout-class C",
		Short: "Define a job profile: the shape a workflow's job is deployed as",
		Long: "Define a job profile in this project's own config.\n\n" +
			"The four values are the job's whole shape and all four are required: one\n" +
			"Cloud Run job per (profile, provider, account) is deployed with them, and a\n" +
			"profile missing one of them describes no job at all. The provider is one of\n" +
			"the git providers Fugaro has a path for, because it is part of the job's\n" +
			"identity, of the slug its runs are stored under and of the credential the job\n" +
			"holds.\n\n" +
			"The profile is checked against the same rules the publisher applies to the\n" +
			"published policy, so what it writes is what `fugaro update` will deploy: the\n" +
			"name, the provider, the Cloud Run shape of cpu and memory, and the timeout\n" +
			"within Cloud Run's ceiling. A config that carries a profiles: block must\n" +
			"define standard, which is the profile every workflow that names none runs on.\n\n" +
			"The block reaches a run through the published policy, so the change is\n" +
			"deployed by `fugaro update`, which publishes it and deploys the jobs.\n\n" +
			"Only the keys it writes are rewritten: every comment and every other key of\n" +
			"the file is kept.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectProfileAdd(cmd, args[0], &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.provider, "provider", "", "the git provider the profile's job serves (github, gitlab, bitbucket or fake)")
	f.IntVar(&o.cpu, "cpu", 0, "the vCPUs the job's Cloud Run task gets")
	f.StringVar(&o.memory, "memory", "", "the memory the job's task gets, for example 4Gi")
	f.StringVar(&o.timeoutClass, "timeout-class", "", "the job's task timeout, for example 6h")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func newProjectProfileSetCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "set NAME KEY VALUE",
		Short: "Change one key of one job profile",
		Long: "Change one key of one job profile in this project's own config.\n\n" +
			"KEY is one of the profile's own keys — provider, cpu, memory, timeout_class,\n" +
			"secrets, allow_repos, claude_accounts — and VALUE is what it becomes. The\n" +
			"three list keys take a comma-separated list, and an empty VALUE removes the\n" +
			"key rather than writing a list with nothing in it: absent secrets mount\n" +
			"nothing, and an absent allow_repos falls back to the project's own list,\n" +
			"where a written empty one is ambiguous and refused.\n\n" +
			"Every other key and every comment of the file is kept. The edited block is\n" +
			"held to the rules the publisher applies to the published policy, so a profile\n" +
			"this writes is one `fugaro update` deploys.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectProfileSet(cmd, args[0], args[1], args[2], yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func newProjectProfileRmCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a job profile no workflow selects",
		Long: "Remove a job profile from this project's own config.\n\n" +
			"The removal is refused while a workflow this machine can read selects the\n" +
			"profile: a workflow whose job_profile the published policy does not define\n" +
			"has every run refused at start, one run at a time, at the launcher rather\n" +
			"than here. Change the workflow's job_profile first, then remove the profile.\n\n" +
			"A repository this machine holds no checkout for cannot be read, so it is\n" +
			"named as unread rather than assumed: a workflow of one may still select the\n" +
			"profile, and its next run says so.\n\n" +
			"Removing the last profile takes the profiles: block with it, which is how a\n" +
			"config says it sets no profile at all. Only the keys it removes are\n" +
			"rewritten: every comment and every other key of the file is kept.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectProfileRm(cmd, args[0], yes) },
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

// projectProfileEntry is one profile of the read's document. Every list member
// is written even when the file carries none: a consumer drawing the difference
// between "mounts nothing" and "the field is missing" would otherwise read the
// absent one as unknown.
type projectProfileEntry struct {
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	CPU            int      `json:"cpu"`
	Memory         string   `json:"memory"`
	TimeoutClass   string   `json:"timeout_class"`
	Secrets        []string `json:"secrets"`
	AllowRepos     []string `json:"allow_repos"`
	ClaudeAccounts []string `json:"claude_accounts"`
	// Jobs is the jobs the profile maps to, in the words the text form prints:
	// one per (profile, provider, account), the key Task 3.2 deploys by.
	Jobs string `json:"jobs"`
}

type projectProfileDoc struct {
	// Path is the file the profiles were read from, "" when this machine holds
	// no config file for the selected project.
	Path     string                `json:"path"`
	Profiles []projectProfileEntry `json:"profiles"`
}

// projectProfileEntryOf is one profile in the read's own terms: the config's
// carrier, copied whole into the document.
func projectProfileEntryOf(name string, pr projectpolicy.Profile) projectProfileEntry {
	return projectProfileEntry{
		Name:           name,
		Provider:       pr.Provider,
		CPU:            pr.CPU,
		Memory:         pr.Memory,
		TimeoutClass:   pr.TimeoutClass,
		Secrets:        nonNil(pr.Secrets),
		AllowRepos:     nonNil(pr.AllowRepos),
		ClaudeAccounts: nonNil(pr.ClaudeAccounts),
		Jobs:           profileJobsText(name, pr),
	}
}

func runProjectProfileLs(cmd *cobra.Command, jsonOut bool) error {
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	profiles := policyProfiles(lc)
	if jsonOut {
		doc := projectProfileDoc{Path: sel.Path, Profiles: []projectProfileEntry{}}
		for _, name := range slices.Sorted(maps.Keys(profiles)) {
			doc.Profiles = append(doc.Profiles, projectProfileEntryOf(name, profiles[name]))
		}
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	return writeProjectProfileLines(cmd.OutOrStdout(), sel.Path, profiles)
}

func runProjectProfileShow(cmd *cobra.Command, name string, jsonOut bool) error {
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if name == "" {
		name = projectpolicy.DefaultProfileName
	}
	pr, ok := lc.Profiles[name]
	if !ok {
		return userErr("%s defines no profile %q: it defines %s (fugaro project profile ls lists them)", projectProfileWhere(sel.Path), name, projectProfileDefined(lc))
	}
	profile := projectpolicy.Profile{JobProfile: pr}
	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), projectProfileDoc{Path: sel.Path, Profiles: []projectProfileEntry{projectProfileEntryOf(name, profile)}})
	}
	writeProjectProfileLine(cmd.OutOrStdout(), name, profile)
	return nil
}

// writeProjectProfileLines is the read's text form: one pair of lines per
// profile, in name order, in the same words `project policy show` prints the
// published block in — one reader, one spelling.
func writeProjectProfileLines(out io.Writer, path string, profiles map[string]projectpolicy.Profile) error {
	fmt.Fprintf(out, "profiles %s:\n", projectProfileWhere(path))
	for _, name := range slices.Sorted(maps.Keys(profiles)) {
		writeProjectProfileLine(out, name, profiles[name])
	}
	if len(profiles) == 0 {
		fmt.Fprintln(out, "  no profile: this config sets no profiles block, so a policy built from it defines none and every workflow runs on the job its own environment names")
	}
	return nil
}

func writeProjectProfileLine(out io.Writer, name string, pr projectpolicy.Profile) {
	fmt.Fprintf(out, "  profile %s: %s\n", name, profileText(pr))
	fmt.Fprintf(out, "  profile %s jobs: %s\n", name, profileJobsText(name, pr))
}

// projectProfileWhere names the file a read read, for a sentence or a header: a
// project selected from a layer and with no file of its own has no profiles for
// this command to show, and saying which file it looked for is the whole of the
// help such a read can give.
func projectProfileWhere(path string) string {
	if path == "" {
		return "this machine's config for the selected project (no file of its own)"
	}
	return path
}

// projectProfileDefined names the profiles a config does define, for a refusal
// that has to say where to look instead.
func projectProfileDefined(lc *localcfg.Config) string {
	names := slices.Sorted(maps.Keys(lc.Profiles))
	if len(names) == 0 {
		return "none at all (it sets no profiles block)"
	}
	return strings.Join(names, ", ")
}

func runProjectProfileAdd(cmd *cobra.Command, name string, o *projectProfileAddOptions) error {
	if missing := projectProfileAddMissing(cmd, o); len(missing) > 0 {
		return userErr("a job profile is the whole shape its job is deployed as, and %s says nothing about %s: name all four — --provider, --cpu, --memory and --timeout-class (fugaro project profile set NAME KEY VALUE changes one key of a profile that exists)",
			strings.Join(missing, ", "), projectProfileAddWhat(missing))
	}
	sel, lc, err := settingsLocalFile("project profile add")
	if err != nil {
		return err
	}
	if err := projectpolicy.ValidProfileName(name); err != nil {
		return userErr("%v", err)
	}
	if _, exists := lc.Profiles[name]; exists {
		return userErr("profiles.%s is already defined in %s, and this command defines a profile: it would rewrite the whole shape, where `fugaro project profile set %s KEY VALUE` changes one key of it", name, sel.Path, name)
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next := old
	for _, kv := range [][2]string{
		{"provider", o.provider},
		{"cpu", strconv.Itoa(o.cpu)},
		{"memory", o.memory},
		{"timeout_class", o.timeoutClass},
	} {
		if next, err = projectProfileScalar(sel.Path, next, []string{"profiles", name, kv[0]}, kv[1]); err != nil {
			return err
		}
	}
	e := settingsEdit{
		Old:  old,
		New:  next,
		What: fmt.Sprintf("define job profile %s in %s: provider %s, cpu %d, memory %s, timeout %s — one job per (profile, provider, account), and every workflow that selects it runs on one of them", name, sel.Path, o.provider, o.cpu, o.memory, o.timeoutClass),
		Command: fmt.Sprintf("fugaro project profile add %s --provider %s --cpu %d --memory %s --timeout-class %s",
			name, o.provider, o.cpu, o.memory, o.timeoutClass),
		Yes: o.yes,
	}
	if err := projectConfigEditLocal(cmd, lc, sel, e); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

// projectProfileAddMissing is the flags `project profile add` was given none of,
// in the order the command's own help names them.
func projectProfileAddMissing(cmd *cobra.Command, o *projectProfileAddOptions) []string {
	var missing []string
	for _, f := range []struct {
		name  string
		given bool
	}{
		{"--provider", o.provider != ""},
		{"--cpu", cmd.Flags().Changed("cpu") && o.cpu > 0},
		{"--memory", o.memory != ""},
		{"--timeout-class", o.timeoutClass != ""},
	} {
		if !f.given {
			missing = append(missing, f.name)
		}
	}
	return missing
}

// projectProfileAddWhat is what a profile is missing when one of the four flags
// was not given: the sentence says which key of the job it is, because "cpu" and
// "memory" alone do not say whether the number is the job's or its runs'.
func projectProfileAddWhat(missing []string) string {
	what := make([]string, 0, len(missing))
	for _, flag := range missing {
		switch flag {
		case "--provider":
			what = append(what, "the git provider its job serves")
		case "--cpu":
			what = append(what, "the vCPUs its job's task gets")
		case "--memory":
			what = append(what, "the memory its job's task gets")
		case "--timeout-class":
			what = append(what, "its job's task timeout")
		}
	}
	if len(what) == 1 {
		return what[0]
	}
	return strings.Join(what[:len(what)-1], ", ") + " and " + what[len(what)-1]
}

func runProjectProfileSet(cmd *cobra.Command, name, key, value string, yes bool) error {
	sel, lc, err := settingsLocalFile("project profile set")
	if err != nil {
		return err
	}
	pr, ok := lc.Profiles[name]
	if !ok {
		return userErr("%s defines no profile %q: it defines %s; `fugaro project profile add %s --provider ... --cpu ... --memory ... --timeout-class ...` defines one", projectProfileWhere(sel.Path), name, projectProfileDefined(lc), name)
	}
	key, err = projectProfileKey(key)
	if err != nil {
		return err
	}
	if key == "cpu" {
		if _, err := strconv.Atoi(value); err != nil {
			return userErr("cpu is a whole number of vCPUs the job's Cloud Run task gets, and %s is not one: name the number, for example 2", config.CodeSpan(value))
		}
	}
	at := []string{"profiles", name, key}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	var next []byte
	if slices.Contains(projectProfileListKeys(), key) {
		have := projectProfileListOf(pr, key)
		next, err = settingsSetList(old, at, have, projectProfileList(value))
		next, err = workspaceEditResult(sel.Path, next, err)
	} else {
		next, err = projectProfileScalar(sel.Path, old, at, value)
	}
	if err != nil {
		return err
	}
	e := settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("set profiles.%s.%s to %s in %s", name, key, projectProfileValueText(key, value), sel.Path),
		Command: fmt.Sprintf("fugaro project profile set %s %s %s", name, key, value),
		Yes:     yes,
	}
	if err := projectConfigEditLocal(cmd, lc, sel, e); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

// projectProfileKeysPrefix is the registry's own spelling of the keys one
// profile carries (`job_profiles.*.<key>`), which is the block the settings read
// prints. The file spells the same block `profiles.<name>.<key>`, and the
// vocabulary is the same one: a key this command writes is a key the registry
// has a row for.
const projectProfileKeysPrefix = "job_profiles.*."

// projectProfileKey is the key a `set` names, in the registry's spelling:
// everything else is refused with the list of what a profile has, taken from
// the registry rather than respelled here, so a key added to config.JobProfile
// without a row cannot be written by a second list that drifted from it.
func projectProfileKey(key string) (string, error) {
	names := projectProfileKeyNames()
	if slices.Contains(names, key) {
		return key, nil
	}
	return "", userErr("a job profile has no key %s: it carries %s, one job's whole shape (what the registry's rows for this block name, and what `fugaro project profile ls` prints)", config.CodeSpan(key), strings.Join(names, ", "))
}

func projectProfileKeyNames() []string {
	var out []string
	for _, r := range settings.Rows() {
		if k, ok := strings.CutPrefix(r.Key, projectProfileKeysPrefix); ok && k != "" && !strings.Contains(k, ".") {
			out = append(out, k)
		}
	}
	return out
}

// projectProfileListKeys is the three keys of a profile whose value is a list:
// the value is comma-separated, and an empty one removes the key rather than
// writing a list with nothing in it.
func projectProfileListKeys() []string { return []string{"secrets", "allow_repos", "claude_accounts"} }

// projectProfileListOf is one of those keys as the config decoded it, which is
// what a list edit compares the wanted values against.
func projectProfileListOf(pr config.JobProfile, key string) []string {
	switch key {
	case "secrets":
		return pr.Secrets
	case "allow_repos":
		return pr.AllowRepos
	default:
		return pr.ClaudeAccounts
	}
}

// projectProfileList is a comma-separated value as the list it names. An empty
// value is no list at all, which is what removes the key.
func projectProfileList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// projectProfileValueText is the value as a sentence prints it, with an empty
// one named as what it does rather than as nothing at all.
func projectProfileValueText(key, value string) string {
	if value == "" && slices.Contains(projectProfileListKeys(), key) {
		return "nothing (the key is removed)"
	}
	return config.CodeSpan(value)
}

// projectProfileScalar is one scalar key of one profile, written by the editor:
// the splice refusal is the one place a file the editor cannot edit is
// explained.
func projectProfileScalar(path string, src []byte, at []string, value string) ([]byte, error) {
	out, err := yamledit.SetScalar(src, at, value)
	return workspaceEditResult(path, out, err)
}

func runProjectProfileRm(cmd *cobra.Command, name string, yes bool) error {
	sel, lc, err := settingsLocalFile("project profile rm")
	if err != nil {
		return err
	}
	if _, ok := lc.Profiles[name]; !ok {
		return userErr("%s defines no profile %q: it defines %s", projectProfileWhere(sel.Path), name, projectProfileDefined(lc))
	}
	users, unseen := projectProfileUsers(lc, name)
	if len(users) > 0 {
		return userErr("profile %s is selected by %s, and a workflow whose job_profile the published policy does not define has every run refused at start (the policy is checked again at each launch, so the miss is one run at a time): change that workflow's job_profile first, publish with fugaro update, and remove the profile after; nothing was written",
			name, strings.Join(users, ", "))
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next, err := yamledit.RemoveKeyWithOptions(old, []string{"profiles", name}, yamledit.RemoveOptions{DropEmptyParents: true})
	if next, err = workspaceEditResult(sel.Path, next, err); err != nil {
		return err
	}
	e := settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("remove job profile %s from %s: nothing selects it, so no run reads it; removing the last profile takes the profiles: block with it, and a config with no block sets no profile at all", name, sel.Path),
		Command: "fugaro project profile rm " + name,
		Yes:     yes,
	}
	if err := projectConfigEditLocal(cmd, lc, sel, e); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	if len(unseen) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "unread here: %s — no checkout this machine can read, so a workflow of one of them may still select profile %s, and its next run says so if one does\n",
			strings.Join(unseen, ", "), name)
	}
	return nil
}

// projectConfigEditLocal is one edit of the selected local project config,
// checked and written: the strict loader the file itself is read with, and then
// the profiles rule the publisher applies to the block the policy carries
// (projectProfileCheck). Both of this group's write families call it — `project
// routing` needs only the loader, and the profiles half is a no-op for a config
// that carries no profiles block — so one door is the whole of what they write
// through, and a profile written here cannot be one `fugaro update` refuses.
func projectConfigEditLocal(cmd *cobra.Command, lc *localcfg.Config, sel localcfg.Selection, e settingsEdit) error {
	e.Path, e.Kind = sel.Path, settingsDocLocal
	if yamledit.Changed(e.Old, e.New) {
		after, err := modelsReparseLocal(sel.Path, e.New)
		if err != nil {
			return err
		}
		if err := projectProfileCheck(sel.Path, after); err != nil {
			return err
		}
	}
	return applySettingsEdit(cmd, lc, e)
}

// projectProfileCheck holds the edited config to the rules the publisher
// applies to the same blocks, over the two blocks the same file derives: the
// profiles a policy carries must define standard, name a known provider, be a
// Cloud Run shape, carry a timeout within the ceiling, admit only repositories
// their own provider's patterns reach and only accounts the policy defines
// (projectpolicy.validProfiles, reached here through CheckProfiles) — and a
// profile that mounts the Bitbucket token must admit only what that token's
// block admits, which is the half of the rule that is a property of the two
// blocks together (CheckBitbucketProfileRepos, over the token
// DeriveBitbucketToken answers, exactly as buildProjectPolicy derives the pair
// and checkPolicyBytes judges the node). Both halves are the publisher's own
// functions rather than a second copy of the rule, and both are asked in the
// order the node's decoder asks them. A config that sets no profiles block sets
// no profile at all, and there is nothing to check.
func projectProfileCheck(path string, lc *localcfg.Config) error {
	if len(lc.Profiles) == 0 {
		return nil
	}
	profiles := policyProfiles(lc)
	accounts, err := projectpolicy.DeriveAccounts(profiles, lc.ClaudeAccounts, lc.ProjectSecrets)
	if err == nil {
		err = projectpolicy.CheckProfiles(profiles, accounts)
	}
	var token *projectpolicy.BitbucketToken
	if err == nil {
		token, err = projectpolicy.DeriveBitbucketToken(profiles, lc.ProjectSecrets)
	}
	if err == nil {
		err = projectpolicy.CheckBitbucketProfileRepos(profiles, token)
	}
	if err != nil {
		return userErr("%s would not publish as a policy: %v", path, err)
	}
	return nil
}

// projectProfileUsers is the workflows of this project's repositories whose own
// fugaro.yaml selects the profile, as "<repository> (<workflow>)" each, and the
// repositories this machine cannot read. It is the checkout scan `models rm`
// makes for a pinned model, asked about a profile: both are a name a
// repository's own file selects, and neither can be answered from the project's
// config alone.
//
// A workflow that names no profile is not a user of any: it runs on standard,
// which the check above refuses to leave the block without, and a config that
// carries no block at all has no run to refuse.
func projectProfileUsers(lc *localcfg.Config, name string) (users, unseen []string) {
	cfgs, unreadable := projectCheckoutConfigs(lc)
	unseen = unreadable
	for _, repo := range slices.Sorted(maps.Keys(cfgs)) {
		cfg := cfgs[repo]
		for _, w := range slices.Sorted(maps.Keys(cfg.Workflows)) {
			if cfg.Workflows[w].JobProfile == name {
				users = append(users, fmt.Sprintf("%s (%s)", repo, w))
			}
		}
	}
	return users, unseen
}
