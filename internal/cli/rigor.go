package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro rigor set` — config-management G6 (D1169), which is the
// command docs/rigor-command/ designed (D830–D839, D900) read against this tree
// and written through `applySettingsEdit`, the one write path
// (settings_write.go).
//
// What the design asks for, and where each part of it lives:
//
//   - §3's target is this machine's own project layer file, the one path
//     localcfg derives from the project's name and `fugaro update`'s Layer stage
//     seeds and publishes (D830, D831). This file resolves it through
//     settingsResolveTarget's `--layer project` branch, so the path, the anchor
//     the document is read against and the "no local project layer file"
//     refusal are the ones every other project-layer write already makes — the
//     design's step 4, read from the writer rather than restated here.
//   - §3 step 1 is the vocabulary, step 2 D834's `hotfix` refusal, §5's glob
//     and name rules are rigor.Area.Validate's own, and §5's duplicate-glob
//     refusal is the loader's (internal/config's validateRigor, F14) — every one
//     of them the rule's own words rather than a second copy in this file.
//   - §3 step 5 is settingsReparse, and step 6's diff, confirmation, backup and
//     atomic write are applySettingsEdit's, which this command calls and does
//     not repeat (SD5, the joint order's §5).
//   - §3 step 7 is the `next: fugaro update` line, printed by the last line of
//     runRigorSet.
//
// The rank rule is §3's own paragraph: the mode an edit is compared against is
// what this project resolves to TODAY — rigor.Resolve's answer for these inputs,
// read before anything is written — and not the value of one `default` key. An
// area rule and the layer's default are both in play, so a comparison against
// either alone would ask for the sensitive step over a harmless change or miss a
// real lowering. Where the new mode ranks below that answer, the step is the
// sensitive one; D1180 (config-management's reconciliation) settles what that
// step IS here: it is the `Typed` class of D846/D1552, not D832's typed project
// name — a coding agent's session is refused it whatever `--yes` says, and the
// owner's own terminal reads what it lowers and asks nothing.
//
// This command writes the mode every run of the project starts from, which is
// the depth and the cost of every review a run gets, and it writes it into the
// one document `fugaro update` publishes: a mistyped value here is a whole
// project reviewing less, which is why every rule above refuses rather than
// repairs.
//
// The `fugaro rigor` group is created by whichever of this task, G7 (`fugaro
// rigor floor`) and the settings design's S7 lands first, and each of the others
// adds its own subcommand to it.

// rigorOptions are `fugaro rigor set`'s flags. It takes no --layer (D830: the
// path is derived and there is nothing to choose) and no --dry-run (the
// settings family's dry run reads a registry row's value, and this edit is a
// sequence item as often as a scalar).
type rigorOptions struct {
	project string
	config  string
	// area is --area: the glob of the rule to write. Empty is the layer's own
	// default, which is §3's form.
	area string
	// name is --name: the rule's name. It is required when the glob is not in
	// the layer yet, and it renames the rule when it is (§5).
	name string
	yes  bool
	// ask is --ask, the settings family's own: in a project whose marks say it
	// is managed, ask the confirmation this write would otherwise get instead
	// of writing on the gate's verdict.
	ask bool
}

// newRigorCmd is `fugaro rigor`: the mode a project's runs are held to.
func newRigorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rigor",
		Short: "Set the rigor mode a project's runs are held to",
		Long: `The rigor mode decides how much review a run gets and what it costs.

` + "`fugaro rigor set <mode>`" + ` writes ` + "`defaults.rigor.default`" + ` of this machine's own
project layer file, and ` + "`--area <glob>`" + ` writes one rule of
` + "`defaults.rigor.areas`" + ` — the mode a run touching that glob is held to, which is
the strictest of the rules that match it. The file is the one ` + "`fugaro update`" + `
publishes, and every write ends with ` + "`next: fugaro update`" + `.

A mode is ` + "`poc`, `startup`, `pro`, `critical`" + ` or ` + "`hotfix`" + `, and ` + "`hotfix`" + ` is the one
no layer sets: it is an operator's per-run mode, reached with
` + "`fugaro run --mode hotfix`" + `. A mode below what the project resolves to today is
the sensitive direction, so a coding agent's session is refused it with the
command to run at your own terminal, and your own terminal writes it and asks
nothing.`,
	}
	cmd.AddCommand(newRigorSetCmd())
	return cmd
}

func newRigorSetCmd() *cobra.Command {
	var o rigorOptions
	cmd := &cobra.Command{
		Use:   "set MODE [--area GLOB [--name NAME]]",
		Short: "Write the project layer's rigor default, or one area rule",
		Long: `Write a rigor mode into this machine's own project layer file.

With no --area the command writes ` + "`defaults.rigor.default`" + `: the mode every run of
this project starts from unless an area rule or a repository's own file moves
it. With --area GLOB it writes one rule of ` + "`defaults.rigor.areas`" + `: a glob a rule
already carries has its mode replaced, and a glob the layer does not carry yet
is added and then needs --name — the rule's name is what the run record and the
pull request say decided the mode. --name on a rule the layer already carries
renames it. A second rule with the same glob is refused: one glob addresses one
rule, and two rules matching one path leave only the strictest of them deciding.

A mode below what this project resolves to today is the sensitive direction: a
coding agent's session is refused it with the diff and the command to run at
your own terminal, ` + "`--yes`" + ` included, and your own terminal writes it and asks
nothing. ` + "`hotfix`" + ` is refused in either form, naming ` + "`fugaro run --mode hotfix`" + `.

The command prints the review floor beside the write, and it cannot lower it:
` + "`fugaro rigor floor`" + ` is where a floor is changed.

It writes one file — this machine's own project layer file, whose path is
derived from the selected project's name — and it refuses when that file does
not exist, naming ` + "`fugaro update`" + `, which seeds it from the published layer.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRigorSet(cmd, args[0], o)
		},
	}
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.area, "area", "", "write this glob's rule in defaults.rigor.areas instead of the layer's default (e.g. docs/**)")
	f.StringVar(&o.name, "name", "", "the rule's name; required for a glob the layer does not carry yet, and a rename for one it does")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this write would otherwise get instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back. Refused where nobody can answer it (a coding agent's session, a pipe, --non-interactive); a no-op with a sentence in a project that is not managed; refused beside --yes")
	return cmd
}

// The two registry rows this command writes, by the spelling the settings write
// path looks a row up by. They are the rigor design's own keys and the registry
// already carries them with the design's spelling (internal/settings'
// layerRow rows, D1169: the rows are re-spelled only if the design's own
// spelling differs, and it does not), so this file adds none.
const (
	rigorDefaultKey = "layer.defaults.rigor.default"
	rigorAreasKey   = "layer.defaults.rigor.areas.*.mode"
)

// runRigorSet is `fugaro rigor set`, in the design's §3 order: the vocabulary,
// D834's refusal, the target, the old document, the edit, the loader's read of
// it, the rank rule, and then the one write path with the gate beside it.
func runRigorSet(cmd *cobra.Command, name string, o rigorOptions) error {
	if err := askConflict(askFlags{ask: o.ask, yes: o.yes}); err != nil {
		return err
	}
	mode := rigor.Mode(name)
	// §3 step 1: the vocabulary, refused in `fugaro run --mode`'s own words, so
	// a near miss is the same mistake wherever a person makes it.
	if !rigor.Known(mode) {
		return userErr("--mode %q: use %s, or leave it off to resolve the mode from the run's own configuration", name, rigor.ModeList())
	}
	// §3 step 2 (D834): hotfix is off the axis and is an operator's own run
	// mode (D12), so no layer sets it — as a default or as a rule — and the
	// refusal names the one door that reaches it. It never offers a flag that
	// would write it, because no layer write of it is what this refusal is.
	if mode == rigor.Hotfix {
		return userErr("hotfix is not a mode a layer sets: it is an operator's own per-run mode, reached with `fugaro run --mode hotfix`, and no layer may set it as a default or as an area rule; nothing was written")
	}
	// A name belongs to a rule, and the rule is the one the glob addresses: a
	// name with no glob names nothing this command can find.
	if o.name != "" && o.area == "" {
		return userErr("--name names an area rule's name, so it takes --area: a rule is addressed by its glob, and `fugaro rigor set %s` writes the layer's own default", name)
	}
	key := rigorDefaultKey
	if o.area != "" {
		key = rigorAreasKey
	}
	row, ok := settings.Lookup(key)
	if !ok {
		// The registry is in this binary, so a row that is not there is this
		// program's own mistake and not the person's: the value was validated
		// above and the key below is one this file names.
		return fmt.Errorf("internal: the settings registry holds no %s row", key)
	}
	sel, lc, err := settingsAimedSelection(settingsWriteOptions{project: o.project, config: o.config})
	if err != nil {
		return err
	}
	target, err := settingsResolveTarget(cmd.Context(), row, settingsWriteOptions{layer: settingsLayerProject, project: o.project, config: o.config}, lc, sel)
	if err != nil {
		return err
	}
	if o.area != "" {
		// The edit lands on the whole rule and not on the `mode` leaf the row is
		// spelled for: UpsertSeqItemByKey writes the rule's fields together, and
		// the path the gate and the loader are handed is the sequence that moves
		// — which is also what the diff shows.
		target.keyPath = []string{"defaults", "rigor", "areas"}
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	// §3 step 5's read, and §5's duplicate-glob refusal with it: this layer's
	// own loader is what says the document is a project layer, and a glob it
	// carries twice is a layer this edit cannot address — one glob addresses one
	// rule, and the loader's sentence names the pair (F14, D838). Reading first
	// is also what makes the edit itself: it is the document the rule's own
	// fields are written against.
	prev, problems := config.ParseProjectLayer(old, target.anchor)
	if len(problems) > 0 {
		return userErr("%s: %s", target.path, problems[0].String())
	}
	next, err := rigorEdit(old, prev, mode, o, target)
	if err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s already reads what was asked for\n", target.path, rigorKeyName(o))
		return nil
	}
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	edited, problems := config.ParseProjectLayer(next, target.anchor)
	if len(problems) > 0 {
		// A backstop: settingsReparse read the same bytes in the same words one
		// line above, and a layer that does not read back is refused rather than
		// resolved as an empty one, which would find no lowering at all.
		return userErr("%s would not read back after the edit: %s", target.path, problems[0].String())
	}
	// §3's rank rule: the baseline is a RESOLUTION of the document as it is
	// today, over the same inputs the edit is about — the rule's own glob for
	// the area form, nothing for the layer's default — and the mode after the
	// edit is the same resolution over the edited document. Both are read
	// before anything is written.
	baseline := rigorResolved(prev, o.area)
	after := rigorResolved(edited, o.area)
	typed, why := false, ""
	if after.Rank() < baseline.Rank() {
		// The sensitive direction. The sentence is the publish door's own where
		// the two documents name the rule it lowers (layerRigorRules, D1026):
		// the same layer reaching runs by two doors says one thing about what
		// left it.
		typed = true
		why = fmt.Sprintf("this lowers the project's rigor mode from %s to %s: the mode a run resolves decides how much review every run of this project's branches gets, and this layer is where it starts from", baseline, after)
		if rules := layerRigorRules(prev, edited); len(rules) > 0 {
			why = rules[0]
		}
	}
	// The review floor is outside every layer, so this command cannot lower it
	// and does not try: what it does is say where the floor stands and which
	// command changes one (SD10, D1170's own separation of the two nouns).
	fmt.Fprintf(cmd.OutOrStdout(), "review floor: %s; this command cannot lower it: fugaro rigor floor is where a floor is changed\n", rigorFloorLine(lc))
	edit := settingsEdit{
		Path:     target.path,
		Old:      old,
		New:      next,
		What:     rigorWhat(o, mode, target),
		Typed:    typed,
		TypedWhy: why,
		Kind:     target.kind,
		Command:  rigorSetCommand(name, o),
		Yes:      o.yes,
		Ask:      o.ask,
	}
	// The gate is read where this command would ask (M0), exactly as the other
	// project-layer writes read it: a coding agent's session reads the marks for
	// a layer write, and the owner's terminal reads them and writes on the
	// gate's verdict. A session's own refusal comes first at the write path and
	// whatever --yes says, which is why the read is skipped for the sensitive
	// class at the owner's terminal and nowhere else (settingsGateReads).
	managed, gate, err := settingsManagedStep(cmd.Context(), lc, sel.Path, target, key, typed, why, old, next, "rigor set", settingsGateReads(cmd, o.yes, o.ask, typed))
	if err != nil {
		return err
	}
	// The layer document's own two refusals, after the gate and never instead
	// of it: the reserved keys and the keys that run as shell (spec §3.6, D1175).
	// A rigor edit moves neither, and the check is here because this is a layer
	// write and a later change to what the key holds would otherwise bypass it.
	if err := settingsLayerGate(cmd, target, settingsWriteOptions{layer: settingsLayerProject}, key, old, next); err != nil {
		return err
	}
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: o.ask, yes: o.yes}, managed.Managed, settingsEditConditions(cmd, edit)); err != nil {
		return err
	}
	edit.Managed = managed
	if err := applySettingsEdit(cmd, lc, edit); err != nil {
		return err
	}
	// The record is written after the write and for a write that happened
	// (D1014): an edit that reached applySettingsEdit wrote or was refused, so
	// this call is the only one this command makes.
	if err := gate.recorded(cmd.Context()); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// rigorEdit builds the edited document: the layer's own default as a scalar
// (§3), or one rule of defaults.rigor.areas addressed by its glob (§5).
//
// A rule the layer does not carry yet is added and needs a name; one it carries
// has its mode replaced and its name kept unless --name renames it. The rule is
// written whole through yamledit, so every other byte of the file — a comment, a
// block, another rule — is left as it was, and the rule's own shape is held to
// rigor.Area.Validate before a byte is built: the check is the rule's own, so
// the bounds (MaxAreaPattern, MaxAreaName, the doublestar pattern, "no ..") are
// the ones internal/config refuses a layer for.
func rigorEdit(old []byte, prev *config.ProjectLayer, mode rigor.Mode, o rigorOptions, target settingsTarget) ([]byte, error) {
	if o.area == "" {
		next, err := yamledit.SetScalar(old, []string{"defaults", "rigor", "default"}, string(mode))
		if err != nil {
			return nil, userErr("%s: %v", rigorDefaultKey, err)
		}
		return next, nil
	}
	rule, index := rigorAreaIn(prev, o.area)
	if rule == nil && o.name == "" {
		return nil, userErr("--area %s is a glob the layer does not carry yet, so it needs --name: a rule's name is what the run record and the pull request say decided the mode, and `fugaro rigor set %s --area %s --name NAME` adds it", safetext.Field(o.area), string(mode), safetext.Field(o.area))
	}
	written := rigor.Area{Name: o.name, Match: o.area, Mode: mode}
	if rule != nil {
		written.Name = rule.Name
		if o.name != "" {
			written.Name = o.name
		}
	}
	if msg := written.Validate(); msg != "" {
		return nil, userErr("defaults.rigor.areas[%d]: %s; nothing was written", index, msg)
	}
	fields := map[string]string{"mode": string(mode), "name": written.Name}
	next, err := yamledit.UpsertSeqItemByKey(old, []string{"defaults", "rigor", "areas"}, "match", o.area, fields)
	if err != nil {
		return nil, userErr("defaults.rigor.areas: %v", err)
	}
	return next, nil
}

// rigorAreaIn is the rule of a layer whose glob is glob, with the index a
// refusal names it by: nil and the append position when the layer carries none.
// The comparison is the glob as written — the same comparison
// UpsertSeqItemByKey addresses the item by — because the rule this command
// edits is the one that address lands on.
func rigorAreaIn(prev *config.ProjectLayer, glob string) (*rigor.Area, int) {
	for i := range prev.Defaults.Rigor.Areas {
		if prev.Defaults.Rigor.Areas[i].Match == glob {
			return &prev.Defaults.Rigor.Areas[i], i
		}
	}
	return nil, len(prev.Defaults.Rigor.Areas)
}

// rigorResolved is what these inputs resolve to: the layer's own rigor block,
// and, for the area form, the glob the edit is about. The repository layer and
// the run's own request are absent on purpose — this command edits the project
// layer, and a repository's `rigor.default` or a run's `--mode` is not a value
// this layer may be compared against: the layers below it are what its edit
// decides for every run that resolves through it.
func rigorResolved(prev *config.ProjectLayer, glob string) rigor.Mode {
	req := rigor.Request{}
	if glob != "" {
		req.Paths = []string{glob}
	}
	return rigor.Resolve(rigor.Config{
		Project:      prev.Defaults.Rigor.Default,
		ProjectAreas: prev.Defaults.Rigor.Areas,
	}, req).Mode
}

// rigorKeyName is the key an edit names in the sentence a no-op prints: the
// document's own spelling of what was asked for.
func rigorKeyName(o rigorOptions) string {
	if o.area == "" {
		return "defaults.rigor.default"
	}
	return "defaults.rigor.areas"
}

// rigorWhat is the sentence naming what changes, printed before the write
// (settingsEdit's own What).
func rigorWhat(o rigorOptions, mode rigor.Mode, target settingsTarget) string {
	if o.area == "" {
		return fmt.Sprintf("set defaults.rigor.default to %s in %s", mode, target.path)
	}
	return fmt.Sprintf("set the rule %s of defaults.rigor.areas to %s in %s", safetext.Field(o.area), mode, target.path)
}

// rigorSetCommand is this edit spelled for a person's own terminal, which is
// what a refusal in a coding agent's session prints. The flags are the ones
// that were given, so the command a person pastes is the edit that was refused
// and nothing more.
func rigorSetCommand(mode string, o rigorOptions) string {
	parts := []string{"fugaro", "rigor", "set", quoteWord(mode)}
	if o.area != "" {
		parts = append(parts, "--area", quoteWord(o.area))
		if o.name != "" {
			parts = append(parts, "--name", quoteWord(o.name))
		}
	}
	if o.project != "" {
		parts = append(parts, "--project", quoteWord(o.project))
	}
	if o.config != "" {
		parts = append(parts, "--config", quoteWord(o.config))
	}
	return strings.Join(parts, " ")
}

// rigorFloorLine is the owner's review floor as this command prints it beside a
// write: the policy node's own rendering of the same value
// (reviewFloorText), read from the local project config, which is where the
// owner sets it (D722, G1). The floor is outside every layer, so no rigor write
// can move it, and the sentence beside it names the command that can — which is
// the design's own reason for printing it (§3, SD10).
func rigorFloorLine(lc *localcfg.Config) string {
	var floor map[rigor.Mode][]policy.Level
	if lc != nil {
		if f, err := lc.ReviewFloorPolicy(); err == nil {
			floor = f
		}
	}
	return reviewFloorText(&projectpolicy.Policy{ReviewFloor: floor})
}
