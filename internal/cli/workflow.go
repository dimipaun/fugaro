package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is D1177's writer for a repository's `workflows.*` block
// (docs/config-management/spec.md §3.7): `fugaro workflow add NAME` creates the
// block and `fugaro workflow rm NAME` takes it away, both as `--layer repo`
// writes through applySettingsEdit — the one document a coding agent's session
// may write (settings_write.go) — and both under the managed gate's read and
// record (settingsManagedStep, gate.recorded), exactly as `settings set` is.
//
// The block's keys are the settings write path's and not this file's: a key is
// named through settingsWriteRow — the same allowlist, the same refusals — and
// written with yamledit.SetScalar, so a key this path does not know is refused
// with the sentence `settings set` prints for it. What this file adds is the
// block's own existence and the executable-key refusal (config.ExecutableKeys)
// that a layer publish makes at config_publish.go's --executable-changes gate.

// workflowOptions are the two commands' flags. They are the settings write
// path's own (settingsWriteOptions, minus the entry pair and the dry run: a
// block is created or removed whole, and what it holds is the same key/value
// write `settings set` makes one at a time).
type workflowOptions struct {
	project string
	config  string
	repo    string
	yes     bool
	// ask is --ask, the settings family's own (managed_ask.go): in a project
	// whose marks say it is managed, ask the confirmation this write would
	// otherwise get instead of writing on the gate's verdict. A project that
	// is not managed asks nothing either way.
	ask bool
	// set is add's --set, one FIELD=VALUE per key of the block being created.
	set []string
	// executable is --executable-changes, config publish's flag and sentence:
	// the block's commands and image steps run as shell in this repository's
	// runs, so an edit that changes one is refused without the owner's word.
	executable bool
}

// newWorkflowCmd is `fugaro workflow`: the block's existence, and nothing of
// what it holds.
func newWorkflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Create and remove a repository's workflow blocks",
		Long: `A repository's fugaro.yaml holds one block per workflow, ` + "`workflows.<name>`" + `, and
this group creates one and takes one away. The block's own keys are

` + "`fugaro settings set`" + `'s, written one concrete key at a time:

    fugaro settings set workflows.web.resources.cpu 4 --layer repo
    fugaro settings set workflows.web.commands.test 'go test ./...' --layer repo

` + "`workflow add`" + ` writes the keys it is given with --set and refuses a block the file
already holds; ` + "`workflow rm`" + ` removes one the file holds. Both write the repository's
own fugaro.yaml — the one document a coding agent's session may write — and both
refuse an executable key (commands.build, commands.test, commands.lint,
commands.fix, commands.render, commands.probe, commands.rerun_failed, image.apt,
image.setup, image.tools, image.skip_build_scripts) without --executable-changes:
those values run as shell in every run of this repository.

Every write ends with one line: next: fugaro update.`,
	}
	cmd.AddCommand(newWorkflowAddCmd(), newWorkflowRmCmd())
	return cmd
}

func newWorkflowAddCmd() *cobra.Command {
	var o workflowOptions
	cmd := &cobra.Command{
		Use:   "add NAME --set FIELD=VALUE",
		Short: "Create a repository's workflows.NAME block with the keys --set names",
		Long: `Create the block workflows.NAME in the repository's fugaro.yaml, with the keys
--set names, and refuse a name the file already holds a block for (that block's
keys are ` + "`fugaro settings set workflows.NAME.<key>`" + `'s).

--set takes one FIELD=VALUE per key, FIELD being a key of a workflow without the
block's name: --set resources.cpu=4, --set commands.test='go test ./...'. A key whose
value is a list, a map or an object list is not this flag's — settings set writes
those with --add, --remove, --add-entry and --remove-entry after the block exists.

--repo names the repository whose fugaro.yaml to write (owner/name, or a path
inside its checkout); without it this writes the current directory's checkout,
and a directory that is not one is refused.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkflowAdd(cmd, args[0], o)
		},
	}
	workflowFlags(cmd, &o)
	cmd.Flags().StringArrayVar(&o.set, "set", nil, "a key of the new block as FIELD=VALUE (e.g. --set commands.test='go test ./...'); repeat it for each key")
	cmd.Flags().BoolVar(&o.executable, "executable-changes", false, executableChangesHelp)
	return cmd
}

func newWorkflowRmCmd() *cobra.Command {
	var o workflowOptions
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a repository's workflows.NAME block",
		Long: `Remove the block workflows.NAME from the repository's fugaro.yaml, and with it
every key the block holds. It refuses a name the file holds no block for, and it
removes nothing else: a block this command does not name is left byte for byte
as it was.

--repo names the repository whose fugaro.yaml to write (owner/name, or a path
inside its checkout); without it this writes the current directory's checkout,
and a directory that is not one is refused.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkflowRm(cmd, args[0], o)
		},
	}
	workflowFlags(cmd, &o)
	cmd.Flags().BoolVar(&o.executable, "executable-changes", false, executableChangesHelp)
	return cmd
}

// executableChangesHelp is the flag's own sentence, shared by the two commands:
// the value a run pays for is the same one either way (config_publish.go's
// --executable-changes).
const executableChangesHelp = "allow a change to the block's commands or image steps (they run as shell in this repository's runs)"

// workflowFlags are the flags both commands take.
func workflowFlags(cmd *cobra.Command, o *workflowOptions) {
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.repo, "repo", "", "the repository whose fugaro.yaml to edit (owner/name, or a path inside its checkout); default: the current directory's checkout")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this write would otherwise get instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back. Refused where nobody can answer it (a coding agent's session, a pipe); a no-op with a sentence in a project that is not managed")
}

// runWorkflowAdd is `workflow add`: create the block with the keys --set names.
func runWorkflowAdd(cmd *cobra.Command, name string, o workflowOptions) error {
	if err := workflowNameProblem(name); err != nil {
		return err
	}
	if len(o.set) == 0 {
		return userErr("workflow add writes the keys it is given: name at least one with --set FIELD=VALUE, and a block with no key is one no run can use (for example --set commands.test='go test ./...')")
	}
	sets, err := workflowSets(name, o.set)
	if err != nil {
		return err
	}
	target, sel, lc, err := workflowTarget(cmd, name, o)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	if workflowBlockIn(old, name) {
		return userErr("%s already holds a workflows.%s block: workflow add creates a block, and `fugaro settings set workflows.%s.<key> <value> --layer repo` writes the keys of one the file holds",
			target.path, name, name)
	}
	next := old
	for _, s := range sets {
		if next, err = yamledit.SetScalar(next, append(append([]string{}, target.keyPath...), s.path...), s.value); err != nil {
			return userErr("%s: %v", s.key, err)
		}
	}
	return workflowApply(cmd, workflowWrite{
		target:     target,
		sel:        sel,
		lc:         lc,
		old:        old,
		next:       next,
		what:       fmt.Sprintf("create workflows.%s in %s", name, target.path),
		command:    workflowCommand("add", name, o, sets),
		subcommand: "workflow add",
		o:          o,
	})
}

// runWorkflowRm is `workflow rm`: remove the block, every key of it, and nothing
// else.
func runWorkflowRm(cmd *cobra.Command, name string, o workflowOptions) error {
	if err := workflowNameProblem(name); err != nil {
		return err
	}
	target, sel, lc, err := workflowTarget(cmd, name, o)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	if !workflowBlockIn(old, name) {
		return userErr("%s holds no workflows.%s block: nothing was removed", target.path, name)
	}
	// The whole block goes, and an empty `workflows:` with it: a block is what
	// this command removes, and a key left standing with nothing under it is the
	// shape `fugaro config init` writes and this one does not.
	next, err := yamledit.RemoveKeyWithOptions(old, target.keyPath, yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		return userErr("workflows.%s: %v", name, err)
	}
	return workflowApply(cmd, workflowWrite{
		target:     target,
		sel:        sel,
		lc:         lc,
		old:        old,
		next:       next,
		what:       fmt.Sprintf("remove workflows.%s from %s", name, target.path),
		command:    workflowCommand("rm", name, o, nil),
		subcommand: "workflow rm",
		o:          o,
	})
}

// workflowSet is one --set FIELD=VALUE after its key has been resolved through
// the rows the settings write path already uses.
type workflowSet struct {
	// field is the key as the command line spelled it, which is what a refusal
	// and the command a person would run print.
	field string
	// path is the field's own path under the block's name.
	path []string
	// key is workflows.NAME.FIELD, the spelling the row is looked up by and the
	// one config.ExecutableKeys' patterns match.
	key   string
	value string
}

// workflowSets reads the --set pairs, holding every key to settingsWriteRow:
// the same allowlist, the same refusals, and the same nearest-name hint a
// `settings set` on that key would print. This is the one place a key of this
// command's block is named, so an unknown key is refused here rather than
// written and left for a later run to refuse.
func workflowSets(name string, pairs []string) ([]workflowSet, error) {
	out := make([]workflowSet, 0, len(pairs))
	for _, pair := range pairs {
		field, value, ok := strings.Cut(pair, "=")
		if !ok || field == "" {
			return nil, userErr("--set %s is not a key of the block: a set is FIELD=VALUE, e.g. --set commands.test='go test ./...'", safetext.Field(pair))
		}
		key := "workflows." + name + "." + field
		if _, err := settingsWriteRow(key); err != nil {
			return nil, err
		}
		out = append(out, workflowSet{field: field, path: strings.Split(field, "."), key: key, value: value})
	}
	return out, nil
}

// workflowTarget resolves the repository's fugaro.yaml and the block's path in
// it. It is settingsResolveTarget's `--layer repo` branch with the keyPath this
// group writes: a `--layer repo` write with no --layer flag to name, because
// this group has one target and no other.
func workflowTarget(cmd *cobra.Command, name string, o workflowOptions) (settingsTarget, localcfg.Selection, *localcfg.Config, error) {
	// --repo names a path, repoArg reduces it to owner/name to say which
	// repository it is, and that reduction loses which clone it was: this
	// command writes the checkout it was pointed at, so the path is kept
	// (repoArgPath) and is what settingsRepoFile lands in — a machine that
	// remembers several checkouts of one repository would otherwise take the
	// last of them, which is not the one named.
	repoPath, err := repoArgPath(cmd.Context(), o.repo)
	if err != nil {
		return settingsTarget{}, localcfg.Selection{}, nil, err
	}
	repo, err := repoArg(cmd.Context(), settingsAim(o.project, o.config), o.repo)
	if err != nil {
		return settingsTarget{}, localcfg.Selection{}, nil, err
	}
	sel, lc, err := settingsAimedSelection(settingsWriteOptions{project: o.project, config: o.config})
	if err != nil {
		return settingsTarget{}, sel, lc, err
	}
	path, err := settingsRepoFile(cmd.Context(), repoPath, repo, lc.Name)
	if err != nil {
		return settingsTarget{}, sel, lc, err
	}
	return settingsTarget{
		layer:   settingsLayerRepo,
		kind:    settingsDocRepo,
		path:    path,
		keyPath: []string{"workflows", name},
		project: lc.Name,
	}, sel, lc, nil
}

// workflowWrite is one pending edit of a repository's workflows block.
type workflowWrite struct {
	target settingsTarget
	sel    localcfg.Selection
	lc     *localcfg.Config
	old    []byte
	next   []byte
	// what is the sentence naming what changes, and command the same edit
	// spelled for a person's own terminal (settingsEdit's own two).
	what string
	// command is the same edit spelled for a person's own terminal, and
	// subcommand the name the managed gate's own record carries, so a run's
	// audit shows which of the two landed.
	command    string
	subcommand string
	o          workflowOptions
}

// workflowApply is the write both commands make: the loader's own read of the
// edited bytes, the executable-key refusal (config.ExecutableKeys), then the
// managed gate's read, the write path and the record of it.
func workflowApply(cmd *cobra.Command, w workflowWrite) error {
	if !yamledit.Changed(w.old, w.next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: workflows already reads what was asked for\n", w.target.path)
		return nil
	}
	// The edited document is held against its own strict loader before anything
	// else, exactly as every other settings write holds it (settingsReparse): a
	// value the loader refuses is refused here, with the loader's message.
	if err := settingsReparse(w.target, w.next); err != nil {
		return err
	}
	// The executable keys are compared between the two documents the write path
	// itself compares, so the values judged and the bytes replaced are one
	// artifact — and the list is config.ExecutableKeys', so an executable key a
	// later release adds is gated here with no edit in this file.
	if changes := workflowExecutableChanges(w.target.keyPath[1], w.old, w.next); len(changes) > 0 {
		workflowExecutableBanner(cmd.OutOrStdout(), changes)
		if !w.o.executable {
			return userErr("%s", workflowExecutableRefusal)
		}
	}
	edit := settingsEdit{
		Path:    w.target.path,
		Old:     w.old,
		New:     w.next,
		What:    w.what,
		Kind:    settingsDocRepo,
		Command: w.command,
		Yes:     w.o.yes,
		Ask:     w.o.ask,
	}
	// The gate is read where this command would ask, exactly as
	// `settings set --layer repo` reads it: a repository's own file is the one
	// document a session may write, so a session reads no marks here, and the
	// owner's terminal reads them and writes on the gate's verdict.
	managed, gate, err := settingsManagedStep(cmd.Context(), w.lc, w.sel.Path, w.target, w.target.keyPath[1], false, "", w.old, w.next, w.subcommand, settingsGateReads(cmd, w.o.yes, w.o.ask, false))
	if err != nil {
		return err
	}
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: w.o.ask}, managed.Managed, settingsEditConditions(cmd, edit)); err != nil {
		return err
	}
	edit.Managed = managed
	if err := applySettingsEdit(cmd, w.lc, edit); err != nil {
		return err
	}
	// The record is written after the write and for a write that happened: an
	// edit that reached applySettingsEdit wrote or was refused, so this call is
	// the only one this command makes (D1014).
	if err := gate.recorded(cmd.Context()); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// workflowExecutableBanner prints the executable changes a write is about to
// make, in the shape `config publish` prints them: one banner, then one line per
// key. It is the one place the two doors below and `workflow add|rm` say it.
func workflowExecutableBanner(out io.Writer, changes []string) {
	fmt.Fprintln(out, "=== EXECUTABLE CHANGES: these run as shell in this repository's runs ===")
	for _, c := range changes {
		fmt.Fprintln(out, "  "+pluginwire.Printable(c))
	}
}

// workflowExecutableRefusal is the sentence a write that moves an executable key
// is refused with. It is one constant because it is one refusal: the banner is
// above it, and the flag that would have allowed it is the same on every door.
const workflowExecutableRefusal = "nothing was written: the workflow changes commands or image steps (shown above); review them, then run again with --executable-changes"

// workflowExecutableGate is the refusal for the settings write path: a
// `settings set` or `settings unset` that lands in a repository's fugaro.yaml
// and moves an executable key of one of its workflows is a workflow edit like
// any other (spec §3.7), so it needs the owner's --executable-changes exactly as
// `workflow add|rm` and a layer publish do — and without it the write the gate
// in workflow.go was added for would be one flag away, through the command the
// group's own help points people at.
//
// The comparison is over the two documents the write path itself compares, so
// every shape of edit is judged by it — a scalar set, an --add or --remove of a
// list item, an --add-entry or --remove-entry of a map or an object list — and
// never by how the command spelled it. A dry run writes nothing and is how the
// change is read before it is made, so it is answered rather than refused; the
// write is what is gated.
func workflowExecutableGate(cmd *cobra.Command, target settingsTarget, o settingsWriteOptions, old, next []byte) error {
	if target.kind == settingsDocLayer {
		// The layer's own gate is settingsLayerGate, and it runs after the
		// managed gate rather than here: a managed project's session is refused
		// and recorded by that gate, with its own rule over the profiles block
		// (settingsProfilesRule, D1025), and a refusal printed before it would
		// leave the record with nothing to say.
		return nil
	}
	if target.kind != settingsDocRepo {
		if o.executableChanges {
			return userErr("--executable-changes goes with --layer repo or --layer project: it is the owner's word for a change to a repository's own workflows, or to the project layer's profiles, whose commands and image steps run as shell in every run of the repositories that take them")
		}
		return nil
	}
	if o.dryRun {
		return nil
	}
	changes := workflowsExecutableChanges(old, next)
	if len(changes) == 0 {
		return nil
	}
	workflowExecutableBanner(cmd.OutOrStdout(), changes)
	if !o.executableChanges {
		return userErr("%s", workflowExecutableRefusal)
	}
	return nil
}

// layerExecutableRefusal is workflowExecutableRefusal's sentence for the layer
// file, which is the document this door writes: one constant, because the flag
// that would have allowed it is the same on both.
const layerExecutableRefusal = "nothing was written: the layer changes commands or image steps (shown above); review them, then run again with --executable-changes"

// settingsLayerGate is the two refusals a write into this machine's project
// layer file meets (G4, D1175; spec §3.6), in the order the file's own rules are
// read: the reserved runs no coding agent's session writes (config publish's
// layerReservedRuns), and then the executable keys of a profile, which the
// owner's --executable-changes is the word for.
//
// It is read after the managed gate and never instead of it: a managed project's
// session meets settingsProfilesRule over this same document, is refused by it
// and leaves the refusal in the record (D1025), and a sentence printed before
// that read would be an answer with no record behind it.
func settingsLayerGate(cmd *cobra.Command, target settingsTarget, o settingsWriteOptions, key string, old, newData []byte) error {
	if target.kind != settingsDocLayer {
		return nil
	}
	if err := settingsLayerReservedGate(cmd, target, key, old, newData); err != nil {
		return err
	}
	return layerExecutableGate(cmd, target, o, old, newData)
}

// layerExecutableGate is workflowExecutableGate for a write into this machine's
// project layer file (G4, D1175; spec §3.6). A profile is where a repository's
// commands and image steps are templated from — every repository that takes the
// profile runs them — so a layer edit that moves one is the owner's own step
// exactly as a change to a repository's own workflows is, and it is the same
// step and the same flag `fugaro config publish` asks for on the document it
// uploads.
//
// The comparison, the banner and the values are that command's own
// (executableChanges over config.ExecutableKeys, its banner's words): the two
// doors write one document, and a second comparison here would be a second
// answer to what runs as shell.
func layerExecutableGate(cmd *cobra.Command, target settingsTarget, o settingsWriteOptions, old, next []byte) error {
	if o.dryRun {
		return nil
	}
	// The old document is untrusted and unvalidated — it may hold what a hand
	// edit put there — so one this Fugaro cannot read leaves prev nil and counts
	// every executable key as changed: config publish's own fail-closed default.
	prev, problems := config.ParseProjectLayer(old, target.anchor)
	if len(problems) > 0 {
		prev = nil
	}
	nextLayer, problems := config.ParseProjectLayer(next, target.anchor)
	if len(problems) > 0 {
		// A backstop and not a live path: settingsReparse read the edit back
		// before this, in the same words. It is here so a document that cannot
		// be read is refused rather than compared as an empty layer, which would
		// find no executable change at all.
		return userErr("%s would not read back as a project layer: %s", target.path, problems[0].String())
	}
	changes, err := executableChanges(prev, nextLayer)
	if err != nil {
		return userErr("nothing was written: this fugaro cannot compare executable key %s: upgrade it or file a bug", err)
	}
	if len(changes) == 0 {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), executableChangesBanner)
	for _, c := range changes {
		fmt.Fprintln(cmd.OutOrStdout(), "  "+pluginwire.Printable(c))
	}
	if !o.executableChanges {
		return userErr("%s", layerExecutableRefusal)
	}
	return nil
}

// workflowsExecutableChanges is workflowExecutableChanges for a whole document:
// every executable key of every workflow block either document holds whose value
// differs, sorted by block. It is the settings write path's question — the
// command names one key, and the file may hold any number of blocks — and the
// names are the union of the two documents', so a block a write adds or removes
// is compared as well.
func workflowsExecutableChanges(old, next []byte) []string {
	var out []string
	for _, name := range workflowNamesIn(old, next) {
		out = append(out, workflowExecutableChanges(name, old, next)...)
	}
	return out
}

// workflowNamesIn is every workflow name either document holds, sorted.
func workflowNamesIn(docs ...[]byte) []string {
	names := map[string]bool{}
	for _, data := range docs {
		var doc map[string]any
		if yaml.Unmarshal(data, &doc) != nil {
			continue
		}
		blocks, ok := doc["workflows"].(map[string]any)
		if !ok {
			continue
		}
		for name := range blocks {
			names[name] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// workflowExecutableChanges lists each executable key (config.ExecutableKeys)
// of the block NAME whose value differs between the document the edit starts
// from and the one it would write, as `workflows.web.commands.build: "" ->
// "go build ./..."`.
//
// It is config publish's executableChanges read on the two documents rather
// than on two parsed layers, because a workflow edit is one block and not a
// whole file: the comparison is the same question — which of the keys that run
// as shell does this edit move — asked of the bytes the write path compares, and
// it is driven by config.ExecutableKeys so a key a later release adds is gated
// here with no edit in this file. A value the document does not hold reads as
// the empty string, so a key written from nothing is a change (the fail-closed
// direction executableChanges takes for a layer it cannot compare) and a key
// both documents leave away is not.
func workflowExecutableChanges(name string, old, next []byte) []string {
	var out []string
	for _, pattern := range config.ExecutableKeys {
		key := workflowKeyOf(pattern, name)
		path := strings.Split(key, ".")
		before, after := settingsValueIn(old, path), settingsValueIn(next, path)
		if before == after {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %q -> %q", key, before, after))
	}
	return out
}

// workflowKeyOf is config.ExecutableKeys' pattern with the workflow name filled
// in: the pattern's second segment is its `*`, which is the block's name and
// never a deeper one (workflows.*.image.tools and the like).
func workflowKeyOf(pattern, name string) string {
	parts := strings.Split(pattern, ".")
	if len(parts) > 1 && parts[0] == "workflows" {
		parts[1] = name
	}
	return strings.Join(parts, ".")
}

// workflowBlockIn reports whether the document holds a workflows.NAME block. It
// reads the bytes rather than the parsed config: a block the loader would refuse
// is a block this command must not write over or claim to have removed, and the
// question it answers is the file's own shape.
func workflowBlockIn(data []byte, name string) bool {
	var doc map[string]any
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	blocks, ok := doc["workflows"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = blocks[name]
	return ok
}

// workflowNameProblem is NAME as a block's key, or nil. A name is the key of a
// mapping under `workflows:`, and the rule is the loader's own
// (config.WorkflowNameRE): a name this refuses is one a repository's
// fugaro.yaml could not define, so it is refused here with the rule rather than
// written and left for the reparse to refuse with a path and no rule.
func workflowNameProblem(name string) error {
	if name == "" {
		return userErr("a workflow's name is not empty: name the block to write")
	}
	if !config.WorkflowNameRE.MatchString(name) {
		return userErr("%s is not a workflow name: a name is 1 to 20 characters of a-z, 0-9 and '-', starting with a letter, and it is the name a run names on the command line", safetext.Field(name))
	}
	return nil
}

// workflowCommand is the edit spelled for a person's own terminal, which is what
// a refusal prints (settingsEdit's Command). It is the same edit and not a
// second one: the sets, the repository and the executable flag, with the aim
// that travelled with the command line.
func workflowCommand(op, name string, o workflowOptions, sets []workflowSet) string {
	parts := []string{"fugaro", "workflow", op, quoteWord(name)}
	for _, s := range sets {
		parts = append(parts, "--set", quoteWord(s.field+"="+s.value))
	}
	if o.executable {
		parts = append(parts, "--executable-changes")
	}
	if o.repo != "" {
		parts = append(parts, "--repo", quoteWord(o.repo))
	}
	if o.project != "" {
		parts = append(parts, "--project", quoteWord(o.project))
	}
	if o.config != "" {
		parts = append(parts, "--config", quoteWord(o.config))
	}
	return strings.Join(parts, " ")
}
