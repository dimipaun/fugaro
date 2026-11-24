package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/task"
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
//   - RG3 (docs/repo-override-rulings, R6) adds the two homes D3237 gives these
//     keys in the project: `--repo <owner/name>` names a repository of the
//     project, and the write states its level there — one entry of
//     `defaults.rigor.repos` for the layer's default form, and one member of a
//     named rule of `defaults.rigor.areas` for the area form (LD4, D3294) — so
//     a project sets one repository's level without writing a rule for every
//     other one. `--layer repo` is refused for both keys (R15): a repository's
//     own `rigor.default` and `rigor.areas` are the project's since D3237, so
//     the sentence C1 holds for the key in play — whose fix is the command this
//     one has become — is what a `--layer repo` write reads, and the writer
//     registry no longer claims the door (writers_labels.go).
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
// A write that names a repository is ranked over the repository's own inputs:
// the resolution below reads the layer's block with that repository's name and,
// for the area form, the member's paths (D3294: a project area naming this
// repository in a member outranks the project's entry for the whole of it), so
// a mode below what that repository's runs resolve to today is the widening
// step here exactly as it is for the layer's own default.
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
	// layer is --layer: the layer the write lands in. Empty and `project` are
	// both this machine's own project layer file, the one layer a rigor write
	// has had since R15 refused the repository's; `repo` is refused with C1's
	// own sentence for the key in play, and a word that is no layer at all with
	// this command's.
	layer string
	// repo is --repo: the repository of the project the write is about, as
	// owner/name or as a path to its checkout. Empty is the whole project —
	// `defaults.rigor.default`, or a project-wide area rule.
	repo string
	// area is --area: the glob of the rule to write. Empty is the layer's own
	// default, which is §3's form; with --repo it is the paths of the member
	// the write is about.
	area string
	// name is --name: the rule's name. It is required when the glob is not in
	// the layer yet, and it renames the rule when it is (§5); with --repo it is
	// the name of the rule whose member the write is, and it is required in
	// that form whichever state the layer is in.
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
the strictest of the rules that match it. ` + "`--repo <owner/name>`" + ` states one
repository's level instead: ` + "`defaults.rigor.repos.<owner/name>`" + `, or the member
` + "`--repo`" + ` and ` + "`--area`" + ` name of the rule ` + "`--name`" + ` gives. The file is the one
` + "`fugaro update`" + ` publishes, and every write ends with ` + "`next: fugaro update`" + `.

A repository's own ` + "`fugaro.yaml`" + ` states neither key any more: since D3237 the
project sets every repository's rigor mode, so ` + "`--layer repo`" + ` is refused with
the command that states the level in its new home.

A mode is ` + "`poc`, `startup`, `pro`, `critical`" + ` or ` + "`hotfix`" + `, and ` + "`hotfix`" + ` is the one
no layer sets: it is a per-run mode, reached with
` + "`fugaro run --mode hotfix`" + ` from wherever a run is launched — a coding
agent's session included (the owner's ruling of 2026-11-16). A mode below what the
project resolves to today is
the sensitive direction, so a coding agent's session is refused it with the
command to run at your own terminal, and your own terminal writes it and asks
nothing.

` + "`fugaro rigor security add|rm`" + ` writes the paths a change has to touch to be
reviewed as a security-sensitive change, and the project layer's switch that
puts every change there: it writes ` + "`security.patterns`" + ` and the layer's own
` + "`security.always_level_3`" + `, in the same two layers and with the same fence.`,
	}
	cmd.AddCommand(newRigorSetCmd())
	cmd.AddCommand(newRigorFloorCmd())
	cmd.AddCommand(newRigorSecurityCmd())
	return cmd
}

func newRigorSetCmd() *cobra.Command {
	var o rigorOptions
	cmd := &cobra.Command{
		Use:   "set MODE [--area GLOB [--name NAME]] [--repo REPO]",
		Short: "Write the project layer's rigor default, one area rule, or one repository's level",
		Long: `Write a rigor mode into this machine's own project layer file.

With no --area the command writes ` + "`defaults.rigor.default`" + `: the mode every run of
this project starts from unless a member area of the project's, ` + "`defaults.rigor.repos`" + ` or
the run's own ` + "`--mode`" + ` moves it. With --area GLOB it writes one rule of
` + "`defaults.rigor.areas`" + `: a glob a rule already carries has its mode replaced, and a
glob the layer does not carry yet is added and then needs --name — the rule's
name is what the run record and the pull request say decided the mode. --name on
a rule the layer already carries renames it. A second rule with the same glob is
refused: one glob addresses one rule, and two rules matching one path leave only
the strictest of them deciding.

` + "`--repo <owner/name>`" + ` states the level for one repository of the project, as
owner/name or as a path to its checkout, and it is the project layer that carries
it either way. Without --area it writes ` + "`defaults.rigor.repos.<owner/name>`" + ` — the
whole-repository level, which a project area naming the repository and the
repository's own file both outrank. With ` + "`--area GLOB --name NAME`" + ` it writes one
member of that rule: ` + "`{repo: <owner/name>, paths: [GLOB]}`" + `, beside whatever else
the rule is made of. The rule is addressed by its name, and the name is what
identifies it across the project, so a rule the layer does not carry yet is added
and a name already taken by a rule that names no member for this repository is
refused.

Neither key is a repository's own any more (D3237): a repository's ` + "`rigor.default`" + `
and ` + "`rigor.areas`" + ` are the project's, and ` + "`--layer repo`" + ` is refused for both with
the command that states the level here instead. ` + "`--layer local`" + ` is refused too: a
rigor mode lands in the project layer file and nowhere else.

A mode below what this project's runs resolve to today — with --repo, below what
that repository's runs resolve to — is the sensitive direction: a coding agent's
session is refused it with the diff and the command to run at your own terminal,
and your own terminal writes it and asks nothing. ` + "`hotfix`" + ` is refused in either
form, naming ` + "`fugaro run --mode hotfix`" + `.

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
	f.StringVar(&o.layer, "layer", "", "where the write lands: project (this machine's project layer file, the default); repo is refused, because a repository's own fugaro.yaml states neither key any more")
	f.StringVar(&o.repo, "repo", "", "state the level for one repository of the project, as owner/name or as a path to its checkout: defaults.rigor.repos.<owner/name>, or the member --repo and --area name of the rule --name gives. Without it the write is about the whole project")
	f.StringVar(&o.area, "area", "", "the glob of the rule to write in defaults.rigor.areas (e.g. docs/**); with --repo, the paths of that repository the member is about")
	f.StringVar(&o.name, "name", "", "the rule's name: required for a glob the layer does not carry yet and for a --repo member write, and a rename for a rule the layer does")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this write would otherwise get instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back. Refused where nobody can answer it (a coding agent's session, a pipe); a no-op with a sentence in a project that is not managed")
	return cmd
}

// The registry rows this command writes, by the spelling the settings write
// path looks a row up by. The three are the layer's own keys and the registry
// already carries them (internal/settings' layerRow rows, D1169: the rows are
// re-spelled only if the design's own spelling differs, and it does not), so
// this file adds none. The project layer prefixes every key it carries with
// `layer.`.
//
// rigorReposKey is the one row whose concrete key the registry cannot state:
// `defaults.rigor.repos` is a map keyed by the repositories of the project, and
// a repository name is not a vocabulary a table holds (the shape
// `repos.*.claude_accounts` has). The row is the block, and the write states
// one entry of it — the row's key plus the repository.
const (
	rigorDefaultKey = "layer.defaults.rigor.default"
	rigorAreasKey   = "layer.defaults.rigor.areas.*.level"
	rigorReposKey   = "layer.defaults.rigor.repos"
)

// rigorRepoKey is the lifted key in play for a `--layer repo` write, which R15
// refuses in C1's own sentence: the two a repository's fugaro.yaml used to
// spell, and the one the flags were about.
func rigorRepoKey(area bool) string {
	if area {
		return "rigor.areas"
	}
	return "rigor.default"
}

// runRigorSet is `fugaro rigor set`, in the design's §3 order: the vocabulary,
// D834's refusal, the layer, the repository, the target, the old document, the
// edit, the loader's read of it, the rank rule, and then the one write path
// with the gate beside it.
func runRigorSet(cmd *cobra.Command, name string, o rigorOptions) error {
	mode := rigor.Rigor(name)
	// §3 step 1: the vocabulary. The design asks for the list in the message "as
	// `fugaro run --mode` refuses it" — that refusal and not its words: run's
	// sentence names a `--mode` flag and offers to leave it off, and this command
	// has no such flag (the mode is its argument) and nothing to resolve from.
	// The list is rigor.RigorList's one spelling either way.
	if !rigor.Known(mode) {
		return userErr("%q is not a rigor mode: use %s; nothing was written", name, rigor.RigorList())
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
	// R15 (D3237, F23): neither key is a repository's own any more, so the layer
	// this command used to write them into is refused — and it is refused with
	// the one sentence C1 holds for the key in play, which is the sentence
	// `settings set` prints for the same two keys: the two paths write one
	// thing, and a second spelling of the refusal is how the two come to
	// disagree (contract-lift-keys.md (c)).
	if o.layer == settingsLayerRepo {
		k, ok := liftedKeyAt(rigorRepoKey(o.area != ""))
		if !ok {
			return fmt.Errorf("internal: C1's list carries no %s key", rigorRepoKey(o.area != ""))
		}
		return userErr("%s", k.Why)
	}
	// --repo with --area writes one member of a named rule, and a member's
	// repository is not what addresses it: in that form the glob is the paths of
	// the member rather than the rule's own, so the rule is addressed by its
	// name and the name is required whatever the layer already carries.
	if o.area != "" && o.repo != "" && o.name == "" {
		return userErr("--repo with --area writes one member of an area rule, and the rule is addressed by its name: name it with --name; nothing was written")
	}
	// The repository the write is about, resolved before the layer file is
	// created or read: a --repo that names no repository — a path outside a
	// checkout, a name that is not owner/name, a checkout of a repository the
	// project's local config does not list — is refused with the machine it
	// found, and nothing is written anywhere.
	repo := ""
	if o.repo != "" {
		resolved, err := repoArg(cmd.Context(), settingsAim(o.project, o.config), o.repo)
		if err != nil {
			return err
		}
		if repo, err = task.CanonicalRepo(resolved); err != nil {
			return userErr("--repo %s: %v", safetext.Field(o.repo), err)
		}
		// The layer's own reader holds a member's repository to
		// rigor.Member.Validate, which is one repository of a project —
		// owner/name, two segments — while --repo also accepts a deeper path,
		// for a provider that nests its groups: a name the layer cannot spell is
		// refused here rather than written into a file the next reader refuses.
		if msg := (rigor.Member{Repo: repo}).Validate(); msg != "" {
			return userErr("--repo %s: %s; nothing was written", safetext.Field(o.repo), msg)
		}
	}
	// The layer is D830's own unless --layer names another, and since R15 there
	// is only one it can name: the project layer file, whose path is derived
	// from the project's name, and the document `fugaro update` publishes.
	// `repo` was refused above; a word that is no layer at all, and `local`,
	// are refused by the resolver, with the sentences every other settings write
	// reads.
	layer := o.layer
	if layer == "" {
		layer = settingsLayerProject
	}
	// The registry row this write is spelled for: the layer's own default, one
	// rule of its areas, or the per-repository map — whose row is the block,
	// because a map keyed by the repositories of the project is not a key a
	// table spells (rigorReposKey).
	key := rigorDefaultKey
	switch {
	case o.area != "":
		key = rigorAreasKey
	case o.repo != "":
		key = rigorReposKey
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
	// §3 step 4's first absence is this command's to answer, for the file it is
	// about: with no local layer file and no published layer there is nothing to
	// seed the file from and the shared resolver's refusal names a remedy that
	// cannot run, so the file is created here and the edit is written into it by
	// the one write path below. The creation is taken back on every path that
	// does not write over it, so what a refusal leaves is the machine it found.
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
	target, err := settingsResolveTarget(cmd.Context(), row, settingsWriteOptions{layer: layer, project: o.project, config: o.config}, lc, sel)
	if err != nil {
		return err
	}
	switch {
	case o.area != "":
		// The edit lands on the whole rule and not on the `level` leaf the row is
		// spelled for: UpsertSeqItemByKey writes the rule's fields together, and
		// the path the gate and the loader are handed is the sequence that moves
		// — which is also what the diff shows.
		target.keyPath = []string{"defaults", "rigor", "areas"}
	case o.repo != "":
		// The per-repository map is addressed by the repository: its one row is
		// the block, and the entry this write states is the block's own path
		// plus the repository the flags named.
		target.keyPath = append(target.keyPath, repo)
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
	next, err := rigorEdit(old, prev.block, mode, o, target, repo)
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
	// §3's rank rule, read before anything is written: the inputs the edit is
	// about — the repository the flags name, and the glob the rule is about for
	// the area form — resolved against the layer as it is today and against the
	// layer the edit leaves (rigorResolve, D3294). The layer is this machine's
	// project layer file and nothing sits below it: the built-in default is
	// inside rigor.Resolve, and a repository's own file and a run's own --mode
	// are not things this layer may be compared against (D21's precedence).
	//
	// The area form carries the two baselines that resolution cannot see: the
	// mode of the rule the edit addresses, for a glob that does not match its own
	// literal text (rigorRuleLowered), and the layer's own default, for a rule it
	// does not carry yet (rigorRuleIsNew).
	baseline := rigorResolve(prev.block, repo, o.area)
	after := rigorResolve(edited.block, repo, o.area)
	// The mode every path of the layer falls back to: the same resolution with
	// no repository and no glob, which is the baseline a rule the layer does not
	// carry yet is compared against.
	projectDefault := rigorResolve(prev.block, "", "")
	typed, lowered, why := false, "", ""
	switch {
	case after.Rank() < baseline.Rank():
		typed = true
		lowered = fmt.Sprintf("%s from %s to %s", rigorModeSubject(repo), baseline, after)
	case o.area != "" && rigorRuleIsNew(prev.block.areas, o) && mode.Rank() < projectDefault.Rank():
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
		lowered = fmt.Sprintf("the new rule %s, which sets %s where %s resolves to %s today", safetext.Field(o.area), mode, rigorWhere(repo), projectDefault)
	case o.area != "" && rigorRuleLowered(prev.block.areas, o, mode):
		// The resolution above reads the glob as a PATH, so a glob that does not
		// match its own text — `docs/{a,b}/**`, `docs/[ab]*` — resolves to the
		// layer's default against itself and a rule lowered through such a glob
		// reads as no change at all. The rule this edit addresses is the third
		// baseline, and dropping its mode is the lowering whatever the resolution
		// over the glob's literal text says — for the member form, it is the
		// level of the whole area that member is in.
		rule, _ := rigorRuleIn(prev.block.areas, o)
		typed = true
		lowered = fmt.Sprintf("the rigor mode of the rule %s", safetext.Field(rule.Name))
	}
	if typed {
		why = rigorWideningWhy(repo, lowered)
		// The sentence is the publish door's own where the two documents name
		// the rule it lowers (layerRigorRules, D1026): the same layer reaching
		// runs by two doors says one thing about what left it.
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
		What:     rigorWhat(o, mode, target, repo),
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
	// class at the owner's terminal and nowhere else (settingsGateReads).
	managed, gate, err := settingsManagedStep(cmd.Context(), lc, sel.Path, target, key, typed, why, old, next, "rigor set", settingsGateReads(cmd, o.yes, o.ask, typed))
	if err != nil {
		return err
	}
	// The layer document's own two refusals, after the gate and never instead
	// of it: the reserved keys and the keys that run as shell (spec §3.6, D1175).
	// A rigor edit moves neither, and the check is here because this is a layer
	// write and a later change to what the key holds would otherwise bypass it.
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
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// repoWorkTreeLine is D1564's composite, in the words `models use` gives the
// same fact: a `--layer repo` write is in the checkout's own working tree and
// nowhere else. Nothing is committed, pushed or opened — every run reads the
// base branch, so the value is live when the owner's own branch is merged to it,
// which is why a repository-layer write ends with this sentence and not with
// `next: fugaro update`: that stage publishes the project layer file, and this
// write is not in it. `fugaro rigor set` has no such write since R15 refused
// its two keys at that layer; `fugaro rigor security` still has one.
func repoWorkTreeLine(repo string) string {
	return fmt.Sprintf("the change is in %s's working tree and nowhere else: review it, commit it on your own branch and merge that to the base branch — every run reads the base branch, and this command never commits or pushes", oneLine(repo))
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

// rigorBlock is the rigor configuration of the document a write is about, in
// the vocabulary the edit and the rank rule are written in: the layer's own
// mode, its rules, and the level it states for one repository of the project by
// name (LD4, D3294). The document is this machine's project layer file — since
// R15 the one layer these keys have — and its block is what both the edit and
// the fence read, so the two cannot read two shapings of it.
type rigorBlock struct {
	def   rigor.Rigor
	areas []rigor.Area
	// repos is defaults.rigor.repos, keyed as the layer spells it: the level
	// the project states for one repository of it, `owner/name`.
	repos map[string]rigor.Rigor
}

// rigorDocument is the document a write is about, read in its own loader: the
// block its rigor keys carry, and the parsed layer itself, which the publish
// door's own widening sentence reads (layerRigorRules, D1026).
type rigorDocument struct {
	block rigorBlock
	layer *config.ProjectLayer
}

// rigorReadDocument reads the document a write is about in that document's own
// loader and answers the rigor block it carries: config.ParseProjectLayer, read
// against the anchor the write path reads it against, so a layer file that is
// not a layer file is refused before any rule below is asked.
//
// The problems are the loader's own, unwrapped: the caller words them (a
// document that does not read is one refusal, the edited bytes that do not read
// back are another). A document that carries no rigor: block at all answers the
// zero block, which is a mode of "" and no rules — exactly what the resolver
// reads a document that names none as (rigor.Config's own "sets none").
func rigorReadDocument(target settingsTarget, data []byte) (rigorDocument, []config.Problem) {
	l, ps := config.ParseProjectLayer(data, target.anchor)
	if len(ps) > 0 {
		return rigorDocument{}, ps
	}
	return rigorDocument{
		block: rigorBlock{
			def:   l.Defaults.Rigor.Default,
			areas: l.Defaults.Rigor.Areas,
			repos: l.Defaults.Rigor.Repos,
		},
		layer: l,
	}, nil
}

// rigorResolve is what one document resolves to for these inputs (D3294): the
// repository the write names — none for a write about the whole project — and,
// for the area form, the glob the rule is about. The glob is read as a PATH and
// not as a pattern, which is the resolution's own reading: it is why the fence
// carries a second and a third baseline beside this answer.
func rigorResolve(block rigorBlock, repo, glob string) rigor.Rigor {
	req := rigor.Request{Repo: repo}
	if glob != "" {
		req.Paths = []string{glob}
	}
	return rigor.Resolve(rigor.Config{
		Project:      block.def,
		ProjectAreas: block.areas,
		ProjectRepos: block.repos,
	}, req).Level
}

// rigorModeSubject names the mode a widening write drops, for the sentence the
// owner's terminal prints: the repository's own where the write names one, this
// project's otherwise. repo is the repository the flags named, resolved.
func rigorModeSubject(repo string) string {
	if repo != "" {
		return oneLine(repo) + "'s rigor mode"
	}
	return "the project's rigor mode"
}

// rigorWhere is the noun the "a new rule sets less than this" sentence falls
// back to: the resolution a new rule's paths would otherwise be judged by, which
// is one repository's where the write names one and the project's otherwise.
func rigorWhere(repo string) string {
	if repo != "" {
		return "this repository"
	}
	return "this project"
}

// rigorWideningWhy is the sentence a widening write prints (D1552, D1555): what
// it lowers, and why the lowering matters. Both forms write the project layer
// file — the one document `fugaro update` publishes — so the half that differs
// is whose runs the mode decides, which is what a person weighing the write is
// being asked about.
func rigorWideningWhy(repo, lowered string) string {
	whose := "this project's"
	if repo != "" {
		whose = oneLine(repo) + "'s"
	}
	return "this lowers " + lowered + ": the mode a run resolves decides how much review every run of " + whose + " branches gets, and this layer is where it starts from"
}

// rigorEdit builds the edited document: the layer's own default as a scalar
// (§3), one rule of its areas addressed by its glob (§5), the level of one
// repository of the project (LD4, D3294), or one member of a named rule
// (rigorMemberEdit). Every path is the resolver's own keyPath, so one body
// writes every spelling of the pair.
//
// A rule the layer does not carry yet is added and needs a name; one it carries
// has its mode replaced and its name kept unless --name renames it. The rule is
// written whole through yamledit, so every other byte of the file — a comment, a
// block, another rule — is left as it was, and the rule's own shape is held to
// rigor.Area.Validate before a byte is built: the check is the rule's own, so
// the bounds (MaxAreaPattern, MaxAreaName, the doublestar pattern, "no ..") are
// the ones internal/config refuses the document for.
func rigorEdit(old []byte, prev rigorBlock, mode rigor.Rigor, o rigorOptions, target settingsTarget, repo string) ([]byte, error) {
	name := strings.Join(target.keyPath, ".")
	if o.area == "" {
		next, err := yamledit.SetScalar(old, target.keyPath, string(mode))
		if err != nil {
			return nil, userErr("%s: %v", name, err)
		}
		return next, nil
	}
	if o.repo != "" {
		return rigorMemberEdit(old, prev.areas, mode, o, repo, target)
	}
	rule, index := rigorRuleIn(prev.areas, o)
	if rule == nil && o.name == "" {
		return nil, userErr("--area %s is a glob this document does not carry yet, so it needs --name: a rule's name is what the run record and the pull request say decided the mode, and `fugaro rigor set %s --area %s --name NAME` adds it", safetext.Field(o.area), string(mode), safetext.Field(o.area))
	}
	written := rigor.Area{Name: o.name, Match: o.area, Level: mode}
	if rule != nil && written.Name == "" {
		written.Name = rule.Name
	}
	if msg := written.Validate(); msg != "" {
		return nil, userErr("%s[%d]: %s; nothing was written", name, index, msg)
	}
	fields := map[string]string{"level": string(mode), "name": written.Name}
	next, err := yamledit.UpsertSeqItemByKey(old, target.keyPath, "match", o.area, fields)
	if err != nil {
		return nil, userErr("%s: %v", name, err)
	}
	return next, nil
}

// rigorMemberEdit writes one member of a named rule: the rule the layer already
// carries under that name, or a new one when it carries none, gains this
// repository — with the glob as its paths — beside whatever else it is made of.
//
// The rule's own mode is written with the member, because a rule's level is its
// area's and a member names the part of the project that is in that area: the
// level the write states is the level every path of the rule is held to
// (rigor.Area.ValidateProject's own shape, LD4).
//
// The members list is written WHOLE, because that is what yamledit's
// items-field operation does: every member the rule already carries is
// re-emitted with its own repository and paths, and this repository's member is
// added or has the glob unioned into its paths (rigorMemberAdd).
func rigorMemberEdit(old []byte, prev []rigor.Area, mode rigor.Rigor, o rigorOptions, repo string, target settingsTarget) ([]byte, error) {
	where := strings.Join(target.keyPath, ".")
	rule, index := rigorRuleIn(prev, o)
	written := rigor.Area{Name: o.name, Level: mode}
	if rule != nil {
		written.Match = rule.Match
		written.Members = rule.Members
	}
	written = rigorMemberAdd(written, repo, o.area)
	if msg := written.ValidateProject(); msg != "" {
		return nil, userErr("%s[%d]: %s; nothing was written", where, index, msg)
	}
	// Two operations, because a rule carries a scalar mode and a list of
	// mappings: the first writes the rule's own fields together, creating the
	// rule where the layer has none, and the second writes the members list it
	// now has.
	next, err := yamledit.UpsertSeqItemByKey(old, target.keyPath, "name", o.name, map[string]string{"level": string(mode)})
	if err != nil {
		return nil, userErr("%s: %v", where, err)
	}
	if next, err = yamledit.UpsertSeqItemItemsByKey(next, target.keyPath, "name", o.name, "members", rigorMemberItems(written.Members)); err != nil {
		return nil, userErr("%s: %v", where, err)
	}
	return next, nil
}

// rigorMemberAdd is the rule with this repository's member holding glob: the
// member gains the path when it does not name it yet, and a repository the rule
// does not name gains a member whose paths are that one glob. A member with no
// paths is the repository whole, so the glob is already in the area and the
// member is left as it is rather than narrowed to that one path — and the union
// only ever adds, because replacing the member's list would take the paths it
// names out of the area without saying so.
func rigorMemberAdd(a rigor.Area, repo, glob string) rigor.Area {
	members := make([]rigor.Member, 0, len(a.Members)+1)
	found := false
	for _, m := range a.Members {
		if m.Repo != repo {
			members = append(members, m)
			continue
		}
		found = true
		if len(m.Paths) > 0 {
			paths := slices.Clone(m.Paths)
			if !slices.Contains(paths, glob) {
				paths = append(paths, glob)
			}
			m.Paths = paths
		}
		members = append(members, m)
	}
	if !found {
		members = append(members, rigor.Member{Repo: repo, Paths: []string{glob}})
	}
	a.Members = members
	return a
}

// rigorMemberItems is a rule's members as the sequence yamledit writes: the key
// field is the member's repository, and its paths are stated as a list when it
// has any — an empty list is refused by yamledit, and a member with no paths is
// the repository whole rather than a member with no paths.
func rigorMemberItems(members []rigor.Member) []yamledit.SeqItem {
	out := make([]yamledit.SeqItem, 0, len(members))
	for _, m := range members {
		item := yamledit.SeqItem{KeyField: "repo", Key: m.Repo}
		if len(m.Paths) > 0 {
			item.Lists = map[string][]string{"paths": m.Paths}
		}
		out = append(out, item)
	}
	return out
}

// rigorRuleIn is the rule this edit addresses, with the index a refusal names it
// by: nil and the append position when the layer carries none. The project-wide
// form addresses a rule by its glob — one glob addresses one rule (§5) — and the
// --repo member form by its name, because there the glob is the paths of the
// member rather than the rule's own. The comparison is the text as written, the
// same comparison yamledit addresses the item by, because the rule this command
// edits is the one that address lands on.
func rigorRuleIn(areas []rigor.Area, o rigorOptions) (*rigor.Area, int) {
	member := o.area != "" && o.repo != ""
	for i := range areas {
		if member {
			if areas[i].Name == o.name {
				return &areas[i], i
			}
			continue
		}
		if areas[i].Match == o.area {
			return &areas[i], i
		}
	}
	return nil, len(areas)
}

// rigorRuleIsNew says whether the area form addresses a rule the layer does not
// carry yet: the edit whose mode no resolution over the glob can see before it
// is written, because the rule that would match the glob is the one being added.
func rigorRuleIsNew(areas []rigor.Area, o rigorOptions) bool {
	rule, _ := rigorRuleIn(areas, o)
	return rule == nil
}

// rigorRuleLowered says whether the area form writes a mode below the one the
// rule it addresses carries today. It is the rank rule's third baseline: the
// resolution reads the glob as a path, and a glob that does not match its own
// literal text — a brace or a character class — resolves to the layer's default
// against itself, so a rule lowered through such a glob reads as no change at
// all. The member form needs it for a reason of its own: the level it writes is
// the whole rule's, so it lowers every path the rule's members name, and the
// resolution over the one glob the flags gave does not see them.
func rigorRuleLowered(areas []rigor.Area, o rigorOptions, mode rigor.Rigor) bool {
	rule, _ := rigorRuleIn(areas, o)
	return rule != nil && mode.Rank() < rule.Level.Rank()
}

// rigorWhat is the sentence naming what changes, printed before the write
// (settingsEdit's own What). The key is the layer's own spelling of it, so the
// sentence names what a person will find in the file they are about to edit.
func rigorWhat(o rigorOptions, mode rigor.Rigor, target settingsTarget, repo string) string {
	if o.area != "" && o.repo != "" {
		return fmt.Sprintf("set the member %s of the rule %s to %s in %s", safetext.Field(repo), safetext.Field(o.name), mode, target.path)
	}
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
	var floor map[rigor.Rigor][]policy.Level
	if lc != nil {
		if f, err := lc.ReviewFloorPolicy(); err == nil {
			floor = f
		}
	}
	return reviewFloorText(&projectpolicy.Policy{ReviewFloor: floor})
}
