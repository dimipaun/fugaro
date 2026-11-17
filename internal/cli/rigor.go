package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
//     refusal are the ones every other project-layer write already makes. §3
//     step 4's two absences are split before that branch is reached
//     (rigorCreateLayerFile), because the branch makes one refusal of both: a
//     file absent while the project HAS a published layer keeps the shared
//     refusal, with `fugaro update` named as the remedy (R1's seed, merged — the
//     case the writer's sentence is written for), and a file absent while the
//     project has published NO layer is CREATED, as a project layer document
//     carrying the three anchor values of the local project config. The edit
//     itself still goes through the one write path, so the file the command
//     leaves behind carries the change that was asked for and not an empty
//     shell — and a refusal between the creation and that write takes the
//     created file away again, which is what keeps every "nothing was written"
//     below true of the file as well as of its bytes.
//   - §3 step 1 is the vocabulary, step 2 D834's `hotfix` refusal, §5's glob
//     and name rules are rigor.Area.Validate's own, and §5's duplicate-glob
//     refusal is the loader's (internal/config's validateRigor, F14) — every one
//     of them the rule's own words rather than a second copy in this file.
//   - §3 step 5 is settingsReparse, and step 6's diff, confirmation, backup and
//     atomic write are applySettingsEdit's, which this command calls and does
//     not repeat (SD5, the joint order's §5).
//   - §3 step 7 is the `next: fugaro update` line, printed by the last line of
//     runRigorSet.
//   - M3 (docs/config-ownership/implementation-plan.md) adds the second target
//     the two keys have: `--layer repo` writes them into the checkout's own
//     fugaro.yaml, where they are live once the owner's branch is merged to the
//     base branch and nowhere before (the composite D1564) — this command
//     commits, pushes and opens nothing, and it ends there with the sentence
//     saying so in place of `next: fugaro update`. The project layer file stays
//     the default layer, because its path is derived and D830's "there is
//     nothing to choose" is still how a bare `fugaro rigor set <mode>` reads;
//     the flag refuses a layer the key does not have, naming the layers it does
//     (contract §6, T3). The rank rule below is one rule over one more layer: a
//     repository's write is judged against the project layer this machine holds
//     as well as against the file itself, so a mode below what that
//     repository's runs resolve to today is the widening step there too
//     (D2617), and a widening is `Typed` — refused in a coding agent's session
//     whatever the gate says.
//
// The rank rule is §3's own paragraph: the mode an edit is compared against is
// what this project resolves to TODAY — rigor.Resolve's answer for these inputs,
// read before anything is written — and not the value of one `default` key. An
// area rule and the layer's default are both in play, so a comparison against
// either alone would ask for the sensitive step over a harmless change or miss a
// real lowering. Where the new mode ranks below that answer, the step is the
// sensitive one; D1180 (config-management's reconciliation) settles what that
// step IS here: it is the widening of D846/D1552, not D832's typed project
// name — a coding agent's session is refused it whatever a run carries, and the
// owner's own terminal reads what it lowers and asks nothing.
//
// This command writes the mode every run of the project starts from, which is
// the depth and the cost of every review a run gets, and it writes it into the
// one document `fugaro update` publishes: a mistyped value here is a whole
// project reviewing less, which is why every rule above refuses rather than
// repairs.
//
// The `fugaro rigor` group was created by whichever of this task, G7 (`fugaro
// rigor floor`) and the settings design's S7 landed first — this one — and each
// of the others adds its own subcommand to it: G7's `floor` is in
// rigor_floor.go, and S7 was closed as G7's duplicate on 2026-11-10.

// rigorHotfixRefusal is D834's sentence for `hotfix`, in the words this command
// gives it. It is a constant because it is one sentence spoken at two doors: the
// wizard's rigor question refuses the same value with it (setup.go's
// setupRigorMode), and a second spelling is a second refusal.
const rigorHotfixRefusal = "hotfix is not a mode a layer sets: it is a per-run mode, reached with `fugaro run --mode hotfix`, and no layer may set it as a default or as an area rule; nothing was written"

// rigorOptions are `fugaro rigor set`'s flags. It takes no --dry-run: the
// settings family's dry run reads a registry row's value, and this edit is a
// sequence item as often as a scalar.
type rigorOptions struct {
	project string
	config  string
	// layer is --layer: where the write lands. Empty is the project layer file,
	// which is the target D830 derived the path for and the one every write
	// reached before M3; `repo` is a checkout's own fugaro.yaml, which --repo
	// names.
	layer string
	// repo is --repo: the repository a `--layer repo` write is for, as
	// owner/name or as a path to its checkout. Empty is the checkout the
	// command runs in.
	repo string
	// repoPath is the checkout --repo named when it named one by path: the
	// reduction to owner/name loses which clone that was, so the path is what
	// the write lands in (repoArgPath, settingsRepoFile).
	repoPath string
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

` + "`--layer repo`" + ` writes the same two keys into a checkout's own ` + "`fugaro.yaml`" + `
instead: there they are live once your branch is merged to the base branch,
which is what every run reads, and this command commits, pushes and opens
nothing. The project layer file stays the layer a write lands in when no
` + "`--layer`" + ` says otherwise.

A mode is ` + "`poc`, `startup`, `pro`, `critical`" + ` or ` + "`hotfix`" + `, and ` + "`hotfix`" + ` is the one
no layer sets: it is a per-run mode, reached with
` + "`fugaro run --mode hotfix`" + ` from wherever a run is launched — a coding
agent's session included (the owner's ruling of 2026-11-16). A mode below what the
project resolves to today is
the sensitive direction, so a coding agent's session is refused it with the
command to run at your own terminal, and your own terminal writes it and asks
nothing.`,
	}
	cmd.AddCommand(newRigorSetCmd())
	cmd.AddCommand(newRigorFloorCmd())
	return cmd
}

func newRigorSetCmd() *cobra.Command {
	var o rigorOptions
	cmd := &cobra.Command{
		Use:   "set MODE [--area GLOB [--name NAME]] [--layer LAYER] [--repo REPO]",
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

` + "`--layer repo`" + ` writes the same key into a checkout's own ` + "`fugaro.yaml`" + `, where
the two keys are spelled ` + "`rigor.default`" + ` and ` + "`rigor.areas`" + `: that file is the
repository's own, reviewed like the rest of it, and what it sets is live once
your branch is merged to the base branch — every run reads the base branch, and
this command commits, pushes and opens nothing. ` + "`--repo`" + ` names the repository,
as owner/name or as a path to its checkout; without it the checkout the command
runs in is the one written. A layer the key does not have is refused naming the
layers it does: ` + "`local`" + ` is not one of them.

A mode below what this project's runs resolve to today is the sensitive
direction: a coding agent's session is refused it with the diff and the command
to run at your own terminal, and your own terminal writes it and asks
nothing. ` + "`hotfix`" + ` is refused in either form, naming ` + "`fugaro run --mode hotfix`" + `.

The command prints the review floor beside the write, and it cannot lower it:
the floor is outside every layer, and ` + "`fugaro rigor floor`" + ` is the command that
changes one.

With no ` + "`--layer`" + ` it writes one file: this machine's own project layer file,
whose path is derived from the selected project's name. A project that has
published no layer and has no local file of its own gets one — a project layer
document carrying this installation's anchors and the change that was asked for
— so a fresh installation can set a mode. A project that HAS published a layer
is not written over: with no local file there the command refuses and names
` + "`fugaro update`" + `, whose Layer stage seeds the file from that layer.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRigorSet(cmd, args[0], o)
		},
	}
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.layer, "layer", "", "where the write lands: project (this machine's project layer file, the default) or repo (a checkout's own fugaro.yaml, where the keys are spelled rigor.default and rigor.areas and are live once your branch is merged to the base branch)")
	f.StringVar(&o.repo, "repo", "", "the repository a --layer repo write is for, as owner/name or as a path to its checkout; without it, the checkout the command runs in")
	f.StringVar(&o.area, "area", "", "write this glob's rule in defaults.rigor.areas instead of the layer's default (e.g. docs/**)")
	f.StringVar(&o.name, "name", "", "the rule's name; required for a glob the layer does not carry yet, and a rename for one it does")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this write would otherwise get instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back. Refused where nobody can answer it (a coding agent's session, a pipe); a no-op with a sentence in a project that is not managed")
	return cmd
}

// The four registry rows this command writes, by the spelling the settings
// write path looks a row up by. They are the rigor design's own keys and the
// registry already carries them with the design's spelling (internal/settings'
// layerRow rows and its two repo keys, D1169: the rows are re-spelled only if
// the design's own spelling differs, and it does not), so this file adds none.
// The project layer prefixes the layer's keys with `layer.`; a repository's own
// fugaro.yaml spells the same two keys bare.
const (
	rigorDefaultKey     = "layer.defaults.rigor.default"
	rigorAreasKey       = "layer.defaults.rigor.areas.*.mode"
	rigorRepoDefaultKey = "rigor.default"
	rigorRepoAreasKey   = "rigor.areas"
)

// rigorKey is the registry row one write goes through, in the spelling of the
// layer it lands in. A layer that is neither — `--layer local`, or a word no
// layer answers for — is looked up under the repository's spelling on purpose:
// that row carries both layers, so the resolver's refusal names the layers the
// key HAS (contract §6, T3: `it may be set in project, repo`) rather than the
// layers one spelling of it has, and the unknown layer is refused before it by
// the same resolver.
func rigorKey(layer string, area bool) string {
	switch {
	case area && layer == settingsLayerProject:
		return rigorAreasKey
	case area:
		return rigorRepoAreasKey
	case layer == settingsLayerProject:
		return rigorDefaultKey
	}
	return rigorRepoDefaultKey
}

// runRigorSet is `fugaro rigor set`, in the design's §3 order: the vocabulary,
// D834's refusal, the target, the old document, the edit, the loader's read of
// it, the rank rule, and then the one write path with the gate beside it.
func runRigorSet(cmd *cobra.Command, name string, o rigorOptions) error {
	mode := rigor.Mode(name)
	// §3 step 1: the vocabulary. The design asks for the list in the message "as
	// `fugaro run --mode` refuses it" — that refusal and not its words: run's
	// sentence names a `--mode` flag and offers to leave it off, and this command
	// has no such flag (the mode is its argument) and nothing to resolve from.
	// The list is rigor.ModeList's one spelling either way.
	if !rigor.Known(mode) {
		return userErr("%q is not a rigor mode: use %s; nothing was written", name, rigor.ModeList())
	}
	// §3 step 2 (D834): hotfix is off the axis and is a per-run mode (D12),
	// so no layer sets it — as a default or as a rule — and the refusal names
	// the one door that reaches it, a run's own launch. It never offers a flag
	// that would write it, because no layer write of it is what this refusal
	// is. That door needs no opt-in and no session fence: the owner's ruling
	// of 2026-11-16 says the D832/SD9 fence is about this write and not about
	// a run's own request.
	if mode == rigor.Hotfix {
		return userErr(rigorHotfixRefusal)
	}
	// A name belongs to a rule, and the rule is the one the glob addresses: a
	// name with no glob names nothing this command can find.
	if o.name != "" && o.area == "" {
		return userErr("--name names an area rule's name, so it takes --area: a rule is addressed by its glob, and `fugaro rigor set %s` writes the layer's own default", name)
	}
	// The layer is D830's own unless --layer names another: the project layer
	// file, whose path is derived from the project's name. The repository's own
	// file is the second target the two keys have (M3) and it is reached by
	// asking for it, because a bare `fugaro rigor set <mode>` is documented as
	// writing the layer `fugaro update` publishes.
	layer := o.layer
	if layer == "" {
		layer = settingsLayerProject
	}
	key := rigorKey(layer, o.area != "")
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
	// The raw --repo travels to the resolver for every layer, so that a --repo
	// on a write that lands elsewhere is refused in the resolver's own words
	// (`--repo names a repository, so it goes with --layer repo`) rather than
	// silently ignored; the repository branch below replaces it with the
	// resolved name.
	opts := settingsWriteOptions{layer: layer, repo: o.repo, project: o.project, config: o.config}
	// The repository's name, for the two sentences below that have to say whose
	// runs the write decides: --repo when it was given, and else the origin of
	// the checkout the command runs in — which is the same checkout
	// settingsRepoFile lands the write in when no --repo names another one.
	repo := ""
	if layer == settingsLayerRepo {
		if o.repoPath, err = repoArgPath(cmd.Context(), o.repo); err != nil {
			return err
		}
		if repo, err = repoArg(cmd.Context(), settingsAim(o.project, o.config), o.repo); err != nil {
			return err
		}
		opts.repoPath = o.repoPath
		_, here := checkoutOriginAt("")
		if repo == "" {
			repo = here
		}
		// A checkout whose origin names no repository is one this command
		// cannot hand its change over from: the sentence it ends with names the
		// repository the change is in, and `fugaro update --repo` in that
		// checkout is what gives it a name. (settingsRepoFile would write the
		// file without one, which is `fugaro settings set --layer repo`'s own
		// path and its own sentence; this command has a sentence of its own.)
		if repo == "" {
			return userErr("a --layer repo write names the repository: `%s --repo owner/name`, or run it from that repository's checkout", rigorSetCommand(name, o))
		}
		// The checkout the command runs in is the file to edit when it is that
		// repository's; the repository is named to the resolver only when it is
		// not, so the remembered checkout is what the write lands in
		// (settingsRepoFile's two steps, which this command reads the same way a
		// --repo owner/name write does).
		if here != "" && strings.EqualFold(here, repo) {
			opts.repo = ""
		} else {
			opts.repo = repo
		}
	}
	// §3 step 4's first absence is this command's to answer, for the file it is
	// about: with no local layer file and no published layer there is nothing to
	// seed the file from and the shared resolver's refusal names a remedy that
	// cannot run, so the file is created here and the edit is written into it by
	// the one write path below. The creation is taken back on every path that
	// does not write over it, so what a refusal leaves is the machine it found.
	// A repository's own fugaro.yaml is the file the checkout carries and never
	// a file this command creates, so only a layer write asks.
	if layer == settingsLayerProject {
		createdPath, createdShell, err := rigorCreateLayerFile(cmd.Context(), lc)
		if err != nil {
			return err
		}
		defer func() {
			if createdShell != nil {
				rigorDiscardCreatedLayer(createdPath, createdShell)
			}
		}()
	}
	target, err := settingsResolveTarget(cmd.Context(), row, opts, lc, sel)
	if err != nil {
		return err
	}
	if o.area != "" && layer == settingsLayerProject {
		// The edit lands on the whole rule and not on the `mode` leaf the row is
		// spelled for: UpsertSeqItemByKey writes the rule's fields together, and
		// the path the gate and the loader are handed is the sequence that moves
		// — which is also what the diff shows. The repository's spelling of the
		// row is the sequence itself (rigor.areas), so it needs no override.
		target.keyPath = []string{"defaults", "rigor", "areas"}
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	// §3 step 5's read, and §5's duplicate-glob refusal with it: this document's
	// own loader is what says that a project layer file is a project layer and
	// that a checkout's fugaro.yaml is a fugaro.yaml, and a glob either carries
	// twice is a document this edit cannot address — one glob addresses one
	// rule, and the loader's sentence names the pair (F14, D838). Reading first
	// is also what makes the edit itself: it is the document the rule's own
	// fields are written against.
	prev, problems := rigorReadDocument(target, old)
	if len(problems) > 0 {
		return userErr("%s: %s", target.path, problems[0].String())
	}
	next, err := rigorEdit(old, prev.block, mode, o, target)
	if err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s already reads what was asked for\n", target.path, strings.Join(target.keyPath, "."))
		return nil
	}
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	edited, problems := rigorReadDocument(target, next)
	if len(problems) > 0 {
		// A backstop: settingsReparse read the same bytes in the same words one
		// line above, and a document that does not read back is refused rather
		// than resolved as an empty one, which would find no lowering at all.
		return userErr("%s would not read back after the edit: %s", target.path, problems[0].String())
	}
	// §3's rank rule: the baseline is a RESOLUTION of the document as it is
	// today, over the same inputs the edit is about — the rule's own glob for
	// the area form, nothing for the layer's default — and the mode after the
	// edit is the same resolution over the edited document. Both are read
	// before anything is written. The area form carries the two baselines that
	// resolution cannot see: the mode of the rule the edit replaces, for a glob
	// that does not match its own literal text (rigorAreaLowered), and the
	// document's own default, for a rule it does not carry yet (rigorAreaIsNew).
	//
	// The layers the resolution reads are the ones a run of this document
	// resolves through: the document itself, and — for a repository's own file —
	// the project layer this machine holds below it (rigorMachineBlock), so a
	// mode below what that repository's runs resolve to today is the widening
	// step here exactly as it is for a layer write (D2617). A project layer file
	// has nothing below it: the two layers below it are the built-in default and
	// the repository's own file, and neither is a value this layer may be
	// compared against (rigorBaseline's own comment).
	base := rigorBaseline{repo: layer == settingsLayerRepo}
	if base.repo {
		base.below = rigorMachineBlock(lc)
	}
	baseline := base.mode(prev.block, o.area)
	after := base.mode(edited.block, o.area)
	// The document's own default: the same resolution as baseline for the default
	// form, and the mode every path falls back to for the area one — which is
	// the baseline a rule the document does not carry yet is compared against.
	projectDefault := base.mode(prev.block, "")
	typed, lowered, why := false, "", ""
	switch {
	case after.Rank() < baseline.Rank():
		typed = true
		lowered = fmt.Sprintf("%s from %s to %s", base.subject(repo), baseline, after)
	case o.area != "" && rigorAreaIsNew(prev.block.areas, o.area) && mode.Rank() < projectDefault.Rank():
		// A rule the layer does not carry yet is the one lowering the two cases
		// around this one cannot see. Adding a rule never lowers a resolution —
		// it can only raise or keep it — so the resolution over the glob reads
		// the same before and after, and an area rule REPLACES the default for
		// the paths it matches (rigor.Resolve's own precedence): with the layer's
		// default at `critical`, `--area docs/** poc` is a real lowering that
		// neither the resolution above nor the rule's own mode (there is no rule
		// yet) reports. The default is the floor every resolution falls back to,
		// so a new rule below it is the safe direction: a new rule whose paths a
		// stricter existing rule already covers is flagged too, and no real
		// lowering is missed.
		typed = true
		lowered = fmt.Sprintf("the new rule %s, which sets %s where %s resolves to %s today", safetext.Field(o.area), mode, base.where(), projectDefault)
	case o.area != "" && rigorAreaLowered(prev.block.areas, o.area, mode):
		// The resolution above reads the glob as a PATH, so a glob that does not
		// match its own text — `docs/{a,b}/**`, `docs/[ab]*` — resolves to the
		// layer's default against itself and a rule lowered through such a glob
		// reads as no change at all. The rule this edit replaces is the second
		// baseline, and dropping its own mode is the lowering whatever the
		// resolution over the glob's literal text says.
		typed = true
		lowered = fmt.Sprintf("the rigor mode of the rule %s", safetext.Field(o.area))
	}
	if typed {
		why = rigorWideningWhy(base, repo, lowered)
		// The sentence is the publish door's own where the two documents name
		// the rule it lowers (layerRigorRules, D1026): the same layer reaching
		// runs by two doors says one thing about what left it. It is about the
		// layer file — the document the gate judges and the one whose floors a
		// revision publishes — so a repository's own file, which no publish stage
		// carries, keeps the sentence above.
		if rules := layerRigorRules(prev.layer, edited.layer); len(rules) > 0 {
			why = rules[0]
		}
	}
	// The review floor is outside every layer, so this command cannot lower it
	// and does not try: what it does is say where the floor stands and name the
	// command that changes one (SD10, D1170's own separation of the two nouns).
	// That command is `fugaro rigor floor`, G7's, and it landed beside this one:
	// the line named no command while it did not exist, because sending a person
	// to a name that printed this group's own help is worse than saying nothing.
	fmt.Fprintf(cmd.OutOrStdout(), "review floor: %s; this command cannot lower it: the floor is outside every layer, and `fugaro rigor floor` is the command that changes one\n", rigorFloorLine(lc))
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
	// layer writes read it: a coding agent's session reads the marks for a layer
	// write, and the owner's terminal reads them and writes on the gate's
	// verdict. A session's own refusal comes first at the write path and
	// whatever --yes says, which is why the read is skipped for the sensitive
	// class at the owner's terminal and nowhere else (settingsGateReads). A
	// repository's own file reads no mark unless the edit is the widening class
	// (settingsGateAsks): a session writes that file anyway, and the widening is
	// the one write there that no gate admits (D2617).
	managed, gate, err := settingsManagedStep(cmd.Context(), lc, sel.Path, target, key, typed, why, old, next, "rigor set", settingsGateReads(cmd, o.yes, o.ask, typed))
	if err != nil {
		return err
	}
	// The layer document's own two refusals, after the gate and never instead
	// of it: the reserved keys and the keys that run as shell (spec §3.6, D1175).
	// A rigor edit moves neither, and the check is here because this is a layer
	// write and a later change to what the key holds would otherwise bypass it.
	// A repository's own file is no layer file, and settingsLayerGate answers
	// nothing for it.
	if err := settingsLayerGate(cmd, target, settingsWriteOptions{layer: layer}, key, old, next); err != nil {
		return err
	}
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: o.ask}, managed.Managed, settingsEditConditions(cmd, edit)); err != nil {
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
	if target.kind == settingsDocRepo {
		// D1564's composite, in the words `models use` gives the same fact: the
		// change is in the checkout's working tree and nowhere else. Nothing is
		// committed, pushed or opened — every run reads the base branch, so the
		// mode is live when the owner's own branch is merged to it, which is why
		// there is no `next: fugaro update` here either: that stage publishes the
		// project layer file, and this write is not in it.
		fmt.Fprintf(cmd.OutOrStdout(), "the change is in %s's working tree and nowhere else: review it, commit it on your own branch and merge that to the base branch — every run reads the base branch, and this command never commits or pushes\n", oneLine(repo))
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// rigorCreateLayerFile creates this machine's own project layer file when §3
// step 4's first absence is the state: the file is not there, and the project
// has published no layer. It returns the bytes it wrote — nil when it wrote
// none, because the file is there, or because the project HAS a published layer,
// where the shared resolver's own refusal is what the caller then makes.
//
// A published layer is not created over, and a bucket that cannot be read is the
// same answer for the same reason: publishing a fresh, nearly empty layer over
// the project's real one is the one mistake §3 step 4 names, so an answer of
// "this project has published no layer" is the only one this command creates on.
// Both of those cases fall through to settingsResolveTarget, whose sentence
// names `fugaro update` — the Layer stage that seeds the file from the published
// layer.
//
// The document it writes carries the three anchor keys — the layer format's own
// version, which is 1, with the installation's Name and GCPProject — and
// nothing else of that file: the rest of it (`region`, `runs_bucket`,
// `base_images`, `image_source`, `backend`) are keys the layer does not have,
// and the layer parser refuses any top-level key outside its own set. The
// `defaults:` block the command is there to write follows through the one write
// path, so what the command leaves is the change that was asked for and not
// three bare keys — and the shell exists at all only because that path compares
// and swaps against a file it re-reads, which is why the caller takes it away
// again wherever the write does not land.
func rigorCreateLayerFile(ctx context.Context, lc *localcfg.Config) (string, []byte, error) {
	path, err := localcfg.LayerFile(os.Getenv, lc.Name)
	if err != nil {
		// A path that cannot be derived is settingsResolveTarget's own refusal.
		return "", nil, nil
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		return path, nil, nil
	case !errors.Is(err, fs.ErrNotExist):
		// A path that cannot be read is not an absent one, and it is refused
		// where every other project-layer write refuses it.
		return path, nil, nil
	}
	if _, _, exists, err := layerPublishedRead(ctx, lc, recordReadURL(lc)); err != nil || exists {
		return path, nil, nil
	}
	// The version is the layer format's, not the local config's
	// (localcfg.ConfigVersion): the two documents are separate formats, and the
	// layer parser refuses anything but 1 (config.validateLayer).
	shell := []byte(fmt.Sprintf("version: 1\nproject: %s\ngcp_project: %s\n", lc.Name, lc.GCPProject))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", nil, userErr("creating %s: %v", path, err)
	}
	if err := writeFileAtomic(path, shell); err != nil {
		return "", nil, userErr("creating %s: %v", path, err)
	}
	return path, shell, nil
}

// rigorDiscardCreatedLayer removes the file rigorCreateLayerFile wrote, while it
// still holds exactly the bytes that helper wrote: a refusal between the
// creation and the write, and the write path's own refusal of the write, both
// leave the file gone rather than a document nobody asked for, and a write that
// did land is never undone. The read is the one every reader of a layer file
// makes (config.ReadRegular), so a file swapped for a symlink or a FIFO in the
// meantime is left alone rather than removed through.
func rigorDiscardCreatedLayer(path string, shell []byte) {
	cur, err := config.ReadRegular(path, config.LayerMaxBytes)
	if err != nil || !bytes.Equal(cur, shell) {
		return
	}
	os.Remove(path)
}

// rigorBlock is one document's own rigor configuration, in the vocabulary the
// edit and the rank rule are written in: the mode and the rules, whether they
// were read from this machine's project layer file or from a repository's own
// fugaro.yaml. The two documents spell the same two keys differently (M3), so
// reading them into one shape is what lets the rules below be one cap for both.
type rigorBlock struct {
	def   rigor.Mode
	areas []rigor.Area
}

// rigorDocument is a document a write is about, read in its own loader: the
// block its rigor keys carry, and — for the project layer file — the parsed
// layer itself, which the publish door's own widening sentence reads
// (layerRigorRules, D1026). layer is nil for a repository's own fugaro.yaml:
// that document is no layer file and no publish stage carries it.
type rigorDocument struct {
	block rigorBlock
	layer *config.ProjectLayer
}

// rigorReadDocument reads the document a write is about in that document's own
// loader and answers the rigor block it carries. The project layer file is read
// by config.ParseProjectLayer against the anchor the write path reads it
// against, so a layer file that is not a layer file is refused before any rule
// below is asked; a checkout's fugaro.yaml is read by config.Parse, the reader
// `fugaro validate` and every run of that file read, and its rigor: block is
// Config.Rigor — Config.RepoRigor is filled in by the resolver and not by a
// parse, so reading the file itself is what this needs.
//
// The problems are the loader's own, unwrapped: the caller words them (a
// document that does not read is one refusal, the edited bytes that do not read
// back are another). A document that carries no rigor: block at all answers the
// zero block, which is a mode of "" and no rules — exactly what the resolver
// reads a document that names none as (rigor.Config's own "sets none").
func rigorReadDocument(target settingsTarget, data []byte) (rigorDocument, []config.Problem) {
	if target.kind == settingsDocRepo {
		cfg, ps := config.Parse(data)
		if len(ps) > 0 {
			return rigorDocument{}, ps
		}
		block := rigorBlock{}
		if cfg.Rigor != nil {
			block.def, block.areas = cfg.Rigor.Default, cfg.Rigor.Areas
		}
		return rigorDocument{block: block}, nil
	}
	l, ps := config.ParseProjectLayer(data, target.anchor)
	if len(ps) > 0 {
		return rigorDocument{}, ps
	}
	return rigorDocument{
		block: rigorBlock{def: l.Defaults.Rigor.Default, areas: l.Defaults.Rigor.Areas},
		layer: l,
	}, nil
}

// rigorBaseline is what an edit is compared against (D832): the layers a run of
// the document being edited resolves through, below the block the document
// itself carries (D20's two layers).
type rigorBaseline struct {
	// repo is whether the document being edited is a repository's own
	// fugaro.yaml: its block is then the resolution's repo half and the project
	// layer's sits below it. A project layer write is the other way round — the
	// document IS the project half — and it has nothing below it here, which is
	// why this is a flag rather than a second block.
	//
	// What a layer write deliberately leaves out is every value below its own
	// file: a repository's rigor.default and a run's --mode are not things this
	// layer may be compared against, because the layer is what decides for every
	// repository that names nothing narrower, and a repository that lowers its
	// own mode is the repository's own decision (D21's precedence). Its built-in
	// default is inside rigor.Resolve rather than a second field here.
	repo bool
	// below is the half under the document, read only when repo is true: the
	// project layer this machine holds. It is what makes a repository's write
	// that drops the mode below the project's a real lowering of that
	// repository's runs, and not only a change of one file's text.
	below rigorBlock
}

// mode is what these inputs resolve to for one glob, over the document's own
// block as the edit leaves it: the resolution §3's rank rule compares two of,
// and — with glob empty — the mode every path of that document falls back to.
func (b rigorBaseline) mode(block rigorBlock, glob string) rigor.Mode {
	cfg := rigor.Config{Project: b.below.def, ProjectAreas: b.below.areas}
	if b.repo {
		cfg.Repo, cfg.RepoAreas = block.def, block.areas
	} else {
		cfg.Project, cfg.ProjectAreas = block.def, block.areas
	}
	req := rigor.Request{}
	if glob != "" {
		req.Paths = []string{glob}
	}
	return rigor.Resolve(cfg, req).Mode
}

// subject names the mode a resolution decides, for the sentence a widening
// write prints: one repository's for a checkout's own file, this project's for
// a layer write. repo is the repository the write is for, empty for a layer
// write.
func (b rigorBaseline) subject(repo string) string {
	if b.repo {
		return oneLine(repo) + "'s rigor mode"
	}
	return "the project's rigor mode"
}

// where is the noun the "a new rule sets less than this" sentence falls back to:
// the resolution the rule's paths would otherwise be judged by, which is one
// repository's for a checkout's own file and the project's for a layer write.
func (b rigorBaseline) where() string {
	if b.repo {
		return "this repository"
	}
	return "this project"
}

// rigorMachineBlock is the project layer's own rigor block as this machine holds
// it: the file `fugaro update` seeds and publishes, read the way every reader of
// it reads it (readLayerFile, projectLayerProblems). A file that is absent,
// unreadable or does not read answers nothing rather than the half of it that
// parsed — the same reading init_review.go's projectRigor makes of the same file
// and for the same reason — and the built-in default then stands in for it, which
// is what a repository name nothing of its own resolves through anyway.
//
// The published object is not read for it: this is the layer under a
// repository's own write, one input of a comparison, and a cloud read for it
// would make a write of a checkout's file depend on the runs bucket answering
// (D1564: the write is judged on the machine it is made on). The local file is
// the same value that object is published FROM, so it is the answer this machine
// has.
func rigorMachineBlock(lc *localcfg.Config) rigorBlock {
	if lc == nil || lc.Name == "" {
		return rigorBlock{}
	}
	path, err := localcfg.LayerFile(os.Getenv, lc.Name)
	if err != nil {
		return rigorBlock{}
	}
	data, err := readLayerFile(path)
	if err != nil {
		return rigorBlock{}
	}
	l, problems := projectLayerProblems(data, lc)
	if len(problems) > 0 {
		return rigorBlock{}
	}
	return rigorBlock{def: l.Defaults.Rigor.Default, areas: l.Defaults.Rigor.Areas}
}

// rigorWideningWhy is the sentence a widening write prints (D1552, D1555): what
// it lowers, and why the lowering matters. The half of it that differs between
// the two layers is whose runs the mode decides and which document is where it
// starts from — one repository's branch or every branch of the project — because
// that is the difference between the two writes a person is asked to weigh.
func rigorWideningWhy(base rigorBaseline, repo, lowered string) string {
	if base.repo {
		return "this lowers " + lowered + ": the mode a run resolves decides how much review every run of " + oneLine(repo) + "'s branches gets, and this file is where it starts from"
	}
	return "this lowers " + lowered + ": the mode a run resolves decides how much review every run of this project's branches gets, and this layer is where it starts from"
}

// rigorEdit builds the edited document: the document's own default as a scalar
// (§3), or one rule of its areas list addressed by its glob (§5). The two paths
// are the resolver's own keyPath, so one body writes both spellings of the pair
// — `defaults.rigor.*` in a project layer file and `rigor.*` in a checkout's
// fugaro.yaml (M3).
//
// A rule the document does not carry yet is added and needs a name; one it
// carries has its mode replaced and its name kept unless --name renames it. The
// rule is written whole through yamledit, so every other byte of the file — a
// comment, a block, another rule — is left as it was, and the rule's own shape
// is held to rigor.Area.Validate before a byte is built: the check is the rule's
// own, so the bounds (MaxAreaPattern, MaxAreaName, the doublestar pattern,
// "no ..") are the ones internal/config refuses the document for.
func rigorEdit(old []byte, prev rigorBlock, mode rigor.Mode, o rigorOptions, target settingsTarget) ([]byte, error) {
	name := strings.Join(target.keyPath, ".")
	if o.area == "" {
		next, err := yamledit.SetScalar(old, target.keyPath, string(mode))
		if err != nil {
			return nil, userErr("%s: %v", name, err)
		}
		return next, nil
	}
	rule, index := rigorAreaIn(prev.areas, o.area)
	if rule == nil && o.name == "" {
		// The example names the layer when the document is a checkout's own
		// file: without --layer the same command writes this machine's project
		// layer file, and a sentence that handed a person that command would
		// send the rule somewhere else than the one they asked for.
		where := ""
		if target.kind == settingsDocRepo {
			where = " --layer repo"
		}
		return nil, userErr("--area %s is a glob this document does not carry yet, so it needs --name: a rule's name is what the run record and the pull request say decided the mode, and `fugaro rigor set %s --area %s --name NAME%s` adds it", safetext.Field(o.area), string(mode), safetext.Field(o.area), where)
	}
	written := rigor.Area{Name: o.name, Match: o.area, Mode: mode}
	if rule != nil {
		written.Name = rule.Name
		if o.name != "" {
			written.Name = o.name
		}
	}
	if msg := written.Validate(); msg != "" {
		return nil, userErr("%s[%d]: %s; nothing was written", name, index, msg)
	}
	fields := map[string]string{"mode": string(mode), "name": written.Name}
	next, err := yamledit.UpsertSeqItemByKey(old, target.keyPath, "match", o.area, fields)
	if err != nil {
		return nil, userErr("%s: %v", name, err)
	}
	return next, nil
}

// rigorAreaIn is the rule of a list whose glob is glob, with the index a refusal
// names it by: nil and the append position when the list carries none. The
// comparison is the glob as written — the same comparison UpsertSeqItemByKey
// addresses the item by — because the rule this command edits is the one that
// address lands on.
func rigorAreaIn(areas []rigor.Area, glob string) (*rigor.Area, int) {
	for i := range areas {
		if areas[i].Match == glob {
			return &areas[i], i
		}
	}
	return nil, len(areas)
}

// rigorAreaLowered says whether the area edit replaces the mode of a rule the
// document already carries with a lower one. It is the rank rule's second
// baseline for the area form: rigorBaseline.mode reads the glob as a path, and a
// glob that does not match its own literal text (a brace or a character class)
// resolves to the document's own default against itself, so the resolution alone
// misses the lowering of such a rule.
func rigorAreaLowered(areas []rigor.Area, glob string, mode rigor.Mode) bool {
	rule, _ := rigorAreaIn(areas, glob)
	return rule != nil && mode.Rank() < rule.Mode.Rank()
}

// rigorAreaIsNew says whether the area form adds a rule the document does not
// carry yet: the edit whose mode no resolution over the glob can see before it
// is written, because the rule that would match the glob is the one being added.
func rigorAreaIsNew(areas []rigor.Area, glob string) bool {
	rule, _ := rigorAreaIn(areas, glob)
	return rule == nil
}

// rigorWhat is the sentence naming what changes, printed before the write
// (settingsEdit's own What). The key is the document's own spelling of it, so
// the sentence names what a person will find in the file they are about to
// edit — defaults.rigor.default in a project layer file, rigor.default in a
// checkout's fugaro.yaml.
func rigorWhat(o rigorOptions, mode rigor.Mode, target settingsTarget) string {
	key := strings.Join(target.keyPath, ".")
	if o.area == "" {
		return fmt.Sprintf("set %s to %s in %s", key, mode, target.path)
	}
	return fmt.Sprintf("set the rule %s of %s to %s in %s", safetext.Field(o.area), key, mode, target.path)
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
	// --layer and --repo only when they were given: the project layer file is
	// the layer a bare command writes, and a --repo filled in from the checkout
	// the command ran in is that same checkout, so the command a person pastes
	// is the edit that was refused and nothing more.
	if o.layer != "" {
		parts = append(parts, "--layer", quoteWord(o.layer))
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
