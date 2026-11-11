package cli

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro rigor floor` — config-management G7 (D1170), the command
// the settings design designed as its SD10 and the owner closed as this task's
// duplicate on 2026-11-10.
//
// What a floor is, and why this command is the plan's SEC one. A mode's floor
// is the review levels a run of that mode must run whatever a lower layer asks
// for (D722); the value is the owner's alone, it lives in this machine's own
// project config under `review_floor`, and `fugaro project policy publish`
// copies it into the policy node the runner fails closed on — so a change here
// is a whole project reviewing more or less, and it is why every write ends
// with `next: fugaro update`. `set` and `unset` are the write half; the read is
// `fugaro settings get review_floor` and the floor line `fugaro rigor set`
// prints, which is the design's own separation of the two nouns.
//
// The confirmation is the whole of what this file is written around, and it is
// the one place in this plan where `--yes` is refused by design (D846, D1170):
// taking a level away — by lowering a mode's floor or by removing the mode's
// entry — is a run reviewing less, so it goes through `confirmTypedStrict`,
// where nothing stands in for the project's name typed at a terminal. A floor's
// removal is not a plan a document can show — what it deletes is a floor — and
// the flag a person types in advance, in a script, possibly from an agent, is
// exactly what the refusal exists for. Adding a mode the file does not carry,
// or a level a mode's floor does not carry, asks nothing: it can only require
// more review, never less.
//
// The agent-session fence comes FIRST, before the document is read and before
// the confirmation (D1026): a coding agent's session is refused both
// directions outright, whatever `--yes` says, so the typed name is never asked
// of a session that cannot answer it, and a managed project's gate never sees
// the step at all. That is why this command reads no gate (the plan's §5:
// G7 needs neither M0 nor M6) — the refusal is this file's own, and it is
// stronger than any admission.
//
// The edit is the shared editor's, never a splice (D847): a mode's entry is
// `yamledit`'s AddListItem and RemoveKey, and the removal of a mode's floor is
// RemoveKeyWithOptions. The diff, the backup, the compare-and-swap and the
// atomic write are applySettingsEdit's, called here and not repeated.
//
// Two things SD10's design carries that this task does not, listed rather than
// dropped (S7's own note, for the owner to decide): the read in `show`'s own
// shape, and the two editor operations a written empty mapping (`review_floor:
// {}`) needs. Without the second, `unset` of a mode's LAST entry leaves the key
// gone rather than written empty, and the two spellings mean different things
// at publish — an absent key keeps the node's floor, a written `{}` takes it
// away (localcfg's own comment on the field, and buildProjectPolicy). This file
// reports that rather than inventing the operation.

// rigorFloorKey is the key this command writes, by the spelling the registry
// and the settings write path look a row up by. The registry already carries it
// with the design's own spelling (internal/settings' `loc` row, owner `rigor
// floor`), so this file adds no row.
const rigorFloorKey = "review_floor"

// rigorFloorOptions are the two subcommands' flags. There is no --layer: the
// floor is the owner's own key and lives in the local project config and
// nowhere else, so there is nothing to choose. --yes is registered and stands
// in for nothing: the flag is what D1170 refuses, and a command that did not
// take it would refuse the run at the parser with a sentence about flags rather
// than with the typed confirmation the step is.
type rigorFloorOptions struct {
	project string
	config  string
	yes     bool
}

// newRigorFloorCmd is `fugaro rigor floor`: the review floor, and the command
// that changes one.
func newRigorFloorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "floor",
		Short: "Set or remove the review floor a project's runs are held to",
		Long: `The review floor is the owner's own: per rigor mode, the review levels a run
of that mode must run whatever a lower layer asks for. It lives in this
machine's project config (` + "`review_floor`" + `) and ` + "`fugaro project policy publish`" + `
copies it into the policy node the runner fails closed on, so a change here
reaches runs at the next ` + "`fugaro update`" + `.

` + "`fugaro rigor floor set MODE LEVELS`" + ` writes one mode's floor and
` + "`fugaro rigor floor unset MODE`" + ` removes it. A removal and a lowering take the
project's name typed at a terminal — a floor is a minimum, so taking a level
away is a whole project reviewing less, and no flag stands in for the name.
Adding a mode the file does not carry, or a level a mode's floor does not
carry, asks nothing: it can only require more review.

A coding agent's session is refused both directions outright, whatever the
flags say.`,
	}
	cmd.AddCommand(newRigorFloorSetCmd())
	cmd.AddCommand(newRigorFloorUnsetCmd())
	return cmd
}

func newRigorFloorSetCmd() *cobra.Command {
	var o rigorFloorOptions
	cmd := &cobra.Command{
		Use:   "set MODE LEVELS",
		Short: "Write one rigor mode's review floor",
		Long: `Write the review levels a run of MODE must run, into this machine's project
config.

LEVELS is a comma-separated list of ` + "`l1`" + `, ` + "`l2`" + ` and ` + "`l3`" + ` — the levels
docs/review-levels names — and the floor is the whole list: a level it drops
stops being required of every run of that mode.

Adding a mode the file does not carry, or a level the mode's floor does not
carry, is the ordinary write and asks nothing. Dropping a level is a lowering,
and it takes the project's name typed at a terminal: ` + "`--yes`" + ` does not confirm
it, and neither does anything else. The command refuses an empty list — a floor
with no level in it says nothing, and removing the mode's entry is what
` + "`fugaro rigor floor unset`" + ` is for.

A coding agent's session is refused the write outright, whatever the flags say.

It writes one file: this machine's own project config. The change reaches the
policy node the runner reads at the next ` + "`fugaro update`" + `, whose policy stage
publishes it.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRigorFloor(cmd, o, args[0], args[1], false)
		},
	}
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.BoolVar(&o.yes, "yes", false, "accepted and refused: a lowering takes the project's name typed at a terminal, and no flag stands in for it")
	return cmd
}

func newRigorFloorUnsetCmd() *cobra.Command {
	var o rigorFloorOptions
	cmd := &cobra.Command{
		Use:   "unset MODE",
		Short: "Remove one rigor mode's review floor",
		Long: `Remove the review floor MODE carries: from the next ` + "`fugaro update`" + ` the
mode's runs are held to what their rigor mode and the repository ask for, and
to nothing the owner requires.

A removal takes the project's name typed at a terminal: ` + "`--yes`" + ` does not
confirm it, and neither does anything else — what it deletes is a floor, which
no diff of a plan can show. A coding agent's session is refused the removal
outright, whatever the flags say.

It writes one file: this machine's own project config, and the change reaches
the policy node at the next ` + "`fugaro update`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRigorFloor(cmd, o, args[0], "", true)
		},
	}
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.BoolVar(&o.yes, "yes", false, "accepted and refused: a removal takes the project's name typed at a terminal, and no flag stands in for it")
	return cmd
}

// runRigorFloor is both subcommands' body, in the order the refusal D1170 asks
// for: the session fence, the vocabulary, the document, the edit, the loader's
// read of it, the typed confirmation where the edit takes something away, and
// then the one write path.
func runRigorFloor(cmd *cobra.Command, o rigorFloorOptions, modeArg, levelsArg string, remove bool) error {
	// The fence, before anything is read and before any question is printed: a
	// coding agent's session gets no answer at all, not even the diff. It is
	// this command's own and not the write path's, because the ORDER is the
	// point — the write path's fence would refuse the same run after the typed
	// name had been asked for, which is a session reading a question it cannot
	// answer (D1026).
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was written: the review floor is the owner's own minimum — the levels every run of a mode must run — and lowering or removing one is a whole project reviewing less, so it is changed by a person: %s", initflow.AgentRefusal(m))
	}
	mode := rigor.Mode(modeArg)
	if !rigor.Known(mode) {
		return userErr("%q is not a rigor mode: use %s; nothing was written", modeArg, rigor.ModeList())
	}
	var levels []policy.Level
	if !remove {
		var err error
		if levels, err = rigorFloorLevels(levelsArg, mode); err != nil {
			return err
		}
	}
	row, ok := settings.Lookup(rigorFloorKey)
	if !ok {
		// The registry is in this binary, so a row that is not there is this
		// program's own mistake and not the person's: the key is one this file
		// names.
		return fmt.Errorf("internal: the settings registry holds no %s row", rigorFloorKey)
	}
	sel, lc, err := settingsAimedSelection(settingsWriteOptions{project: o.project, config: o.config})
	if err != nil {
		return err
	}
	target, err := settingsResolveTarget(cmd.Context(), row, settingsWriteOptions{layer: settingsLayerLocal, project: o.project, config: o.config}, lc, sel)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	// The document as it is, through the loader that owns the key's rule
	// (localcfg.Parse runs policy.ParseFloor): the floor a change is compared
	// against is the file's own, and a file whose floor does not read back is
	// refused here rather than resolved as an empty one, which would find no
	// lowering at all.
	prev, err := localcfg.Parse(old)
	if err != nil {
		return userErr("%s: %v", target.path, err)
	}
	next, taking, err := rigorFloorEdit(old, prev.ReviewFloor, mode, levels, remove)
	if err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", target.path, rigorFloorNoChange(mode, remove))
		return nil
	}
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	why := rigorFloorWhy(mode, remove)
	if taking {
		// D1170's step: no flag stands in for the project's name, and the
		// refusal names none, because a refusal that named one would be read by
		// whoever it refuses as the thing to pass next.
		if err := confirmTypedStrict(cmd, lc, why, rigorFloorWhat(target, prev.ReviewFloor, mode, levels, remove)); err != nil {
			return err
		}
	}
	edit := settingsEdit{
		Path:     target.path,
		Old:      old,
		New:      next,
		What:     rigorFloorWhat(target, prev.ReviewFloor, mode, levels, remove),
		Typed:    taking,
		TypedWhy: why,
		Kind:     target.kind,
		Command:  rigorFloorCommand(mode, levels, remove, o),
		Yes:      o.yes,
	}
	if err := applySettingsEdit(cmd, lc, edit); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// rigorFloorLevels is the LEVELS argument as the policy package's vocabulary:
// the closed set, in its own order, and the refusals a value outside it earns.
// The list is written once for the whole argument rather than level by level,
// because a floor is a set and a person naming one should not have to spell the
// levels in the file's order.
func rigorFloorLevels(arg string, mode rigor.Mode) ([]policy.Level, error) {
	seen := map[policy.Level]bool{}
	levels := make([]policy.Level, 0, len(policy.Levels))
	for _, name := range strings.Split(arg, ",") {
		name = strings.TrimSpace(name)
		l, ok := policy.ParseLevel(name)
		if !ok {
			return nil, userErr("%q is not a review level: use %s; nothing was written", name, rigorLevelList())
		}
		if seen[l] {
			return nil, userErr("review level %s is listed twice; nothing was written", name)
		}
		seen[l] = true
		levels = append(levels, l)
	}
	if len(levels) == 0 {
		return nil, userErr("no review level was named: a floor with no level in it says nothing, and `fugaro rigor floor unset %s` is what removes a mode's floor; name the levels that must run (%s)", mode, rigorLevelList())
	}
	// The written order is the vocabulary's, so the same floor is the same
	// bytes however it was typed.
	slices.SortFunc(levels, func(a, b policy.Level) int {
		return slices.Index(policy.Levels, a) - slices.Index(policy.Levels, b)
	})
	return levels, nil
}

// rigorLevelList is the level vocabulary as a refusal names it, read off the
// list itself rather than written out again.
func rigorLevelList() string {
	names := make([]string, len(policy.Levels))
	for i, l := range policy.Levels {
		names[i] = string(l)
	}
	return strings.Join(names, ", ")
}

// rigorFloorEdit builds the edited document, and says whether the edit takes
// something away — a level a mode's floor carried, or the mode's whole entry —
// which is the direction D1170 puts behind the typed name.
//
// The operations are the shared editor's own, never a splice (D847), and each
// is the smallest one the editor can make: a mode whose entry goes is
// RemoveKeyWithOptions', and a mode whose levels change has its entry written
// whole. A mode the edit does not name is not touched at all, so its comments
// and its place in the file survive a change to another one.
//
// The empty result is the caller's no-op: an unset of a mode the file does not
// carry, and a set that names exactly the levels the mode already holds (in any
// order — a floor is a set), both return the input unchanged, and the write path
// writes nothing for them.
func rigorFloorEdit(old []byte, floor map[string][]string, mode rigor.Mode, levels []policy.Level, remove bool) ([]byte, bool, error) {
	path := []string{rigorFloorKey, string(mode)}
	held := rigorFloorHeld(floor[string(mode)])
	if remove {
		if len(held) == 0 {
			return old, false, nil
		}
		// DropEmptyParents: a mode that was the file's last floor takes the
		// `review_floor` key with it, so what is left is a document with no
		// floor rather than a key written with no value.
		next, err := yamledit.RemoveKeyWithOptions(old, path, yamledit.RemoveOptions{DropEmptyParents: true})
		if err != nil {
			return nil, false, userErr("%s.%s: %v", rigorFloorKey, mode, err)
		}
		return next, true, nil
	}
	taking := false
	for _, l := range held {
		if !slices.Contains(levels, l) {
			taking = true
		}
	}
	if slices.Equal(held, levels) {
		return old, false, nil
	}
	// The mode's entry is written whole — the old one taken out, the new one
	// added — and never spliced item by item. A floor a person wrote by hand is
	// as likely to be a flow sequence (`pro: [l2, l3]`) as a block one, and the
	// editor's list operations refuse to splice into a flow collection, so an
	// item-by-item edit would leave the commonest hand-written shape
	// unchangeable. Writing the entry whole also makes the file canonical: the
	// levels in the vocabulary's order, however they were typed.
	next := old
	if len(held) > 0 {
		var err error
		if next, err = yamledit.RemoveKey(next, path); err != nil {
			return nil, false, userErr("%s.%s: %v", rigorFloorKey, mode, err)
		}
	}
	for _, l := range levels {
		var err error
		if next, err = yamledit.AddListItem(next, path, string(l)); err != nil {
			return nil, false, userErr("%s.%s: %v", rigorFloorKey, mode, err)
		}
	}
	return next, taking, nil
}

// rigorFloorHeld is a written floor's levels for one mode, in the vocabulary
// the editor addresses them by. The values were validated by the loader that
// read the document, so a name this refuses cannot come from a file — it comes
// from a document the caller built, and the refusal says so rather than
// dropping it.
func rigorFloorHeld(written []string) []policy.Level {
	held := make([]policy.Level, 0, len(written))
	for _, name := range written {
		if l, ok := policy.ParseLevel(name); ok {
			held = append(held, l)
		}
	}
	return held
}

// rigorFloorNoChange is the sentence an edit that changes nothing prints: the
// state the file is already in, said in the document's own terms.
func rigorFloorNoChange(mode rigor.Mode, remove bool) string {
	if remove {
		return fmt.Sprintf("%s carries no floor, so there is nothing to remove", mode)
	}
	return fmt.Sprintf("%s already carries the floor that was asked for", mode)
}

// rigorFloorWhat is the sentence naming what changes, printed before the write
// and read again in the typed confirmation's own banner (settingsEdit's What).
// It shows the floor as it is and as it would be, because that is the whole of
// what the person confirming is deciding.
func rigorFloorWhat(target settingsTarget, floor map[string][]string, mode rigor.Mode, levels []policy.Level, remove bool) string {
	held := rigorFloorHeld(floor[string(mode)])
	if remove {
		return fmt.Sprintf("remove %s.%s (%s) in %s", rigorFloorKey, mode, rigorLevelsText(held), target.path)
	}
	return fmt.Sprintf("set %s.%s from %s to %s in %s", rigorFloorKey, mode, rigorFloorHeldText(held), rigorLevelsText(levels), target.path)
}

// rigorFloorHeldText is a mode's floor as it is, for the sentence above: a mode
// the file does not carry has none, and saying "from none" is what makes an
// added entry read as an addition rather than as a replacement.
func rigorFloorHeldText(held []policy.Level) string {
	if len(held) == 0 {
		return "none"
	}
	return rigorLevelsText(held)
}

func rigorLevelsText(levels []policy.Level) string {
	names := make([]string, len(levels))
	for i, l := range levels {
		names[i] = string(l)
	}
	return strings.Join(names, ", ")
}

// rigorFloorWhy is the sentence the typed confirmation carries as the reason
// nothing else confirms this step: what the edit takes away, in the words the
// person reading it acts on. It is also what a refusal in a coding agent's
// session would print beside the command, and what a run with no terminal
// reads after the reason it needs one.
func rigorFloorWhy(mode rigor.Mode, remove bool) string {
	if remove {
		return fmt.Sprintf("removing %s's review floor is a whole project reviewing less, so it takes the project's name typed at a terminal, and no flag stands in for it", mode)
	}
	return fmt.Sprintf("lowering %s's review floor is a whole project reviewing less, so it takes the project's name typed at a terminal, and no flag stands in for it", mode)
}

// rigorFloorCommand is this edit spelled for a person's own terminal, which is
// what a refusal in a coding agent's session prints. The flags are the ones
// that were given, so the command a person pastes is the edit that was refused
// and nothing more.
func rigorFloorCommand(mode rigor.Mode, levels []policy.Level, remove bool, o rigorFloorOptions) string {
	parts := []string{"fugaro", "rigor", "floor"}
	if remove {
		parts = append(parts, "unset", string(mode))
	} else {
		parts = append(parts, "set", string(mode), quoteWord(rigorLevelsText(levels)))
	}
	if o.project != "" {
		parts = append(parts, "--project", quoteWord(o.project))
	}
	if o.config != "" {
		parts = append(parts, "--config", quoteWord(o.config))
	}
	return strings.Join(parts, " ")
}
