package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is the one write path of the settings commands: the diff, the
// confirmation, the coding-agent-session refusal, the backup and the atomic
// write, in one place (settings plan F4, D847). S6–S9 of that plan and the
// rigor design's R4 and R5 call it rather than carrying a copy, so `settings
// set`, `rigor set` and `accounts add` cannot come to ask differently for the
// same kind of change. The bytes are the caller's: an editor (internal/yamledit)
// produces New from Old, and this helper only decides whether writing them is
// allowed and then does it.
//
// backupFile and writeFileAtomic themselves live beside the other writers
// (internal/cli/init.go) and are called here, not duplicated: they were already
// the package's one copy before this file existed.

// settingsEdit is one pending edit of a local document.
type settingsEdit struct {
	// Path is the file the edit lands in.
	Path string
	// Old is the file as it is and New as it would be, both read and produced
	// by the caller. The helper writes nothing when the two are equal.
	Old, New []byte
	// What is the sentence the confirmation shows, naming what changes.
	What string
	// Typed asks for the project's name rather than y/N. It is the widening
	// directions: a loosened ceiling, a floor removed, a followup key turned
	// on. --yes answers it outside a coding agent's session (D1107, which
	// amends D846 for `settings set|unset` alone); inside one the marker fence
	// comes first and --yes never lifts it.
	Typed bool
	// TypedWhy is the sentence saying why this form is asked for, and who may
	// answer it: it names --yes where the caller says a flag stands in
	// (D1107), and the refusal of a session with no terminal repeats it.
	TypedWhy string
	// Ask is the owner's --ask: the confirmations this write would have been
	// given in a project that is not managed, asked here instead of being
	// replaced by the gate's verdict (initflow.Conditions.Ask, and the rule of
	// 2026-11-04 that makes it --yes's symmetric half). It never touches the
	// gate above: the read, the judgement and the admission are made exactly
	// where they were, and the flag answers only the question of what this
	// write is confirmed BY.
	Ask bool
	// Kind is the document Path is: the local project config, the local project
	// layer file, or a repository's fugaro.yaml. It is one fact and not two,
	// because both things it decides must agree: the reader of the
	// compare-and-swap (a checkout's file and the layer file are read through
	// config.ReadRegular, the local config as the person's own dotfile) and
	// whether an agent session may write the file at all — it may write one it
	// could edit anyway, a repository's, unless the edit is Typed.
	Kind settingsDocKind
	// Command is the command a refusal in a coding agent's session prints for
	// the person to run at their own terminal. It is the whole of what the
	// refusal may produce beside the diff.
	Command string
	// Yes is --yes. It answers the y/N confirmation, and the typed one too
	// wherever the caller says a flag may (D1107).
	Yes bool
	// Managed is the managed gate's answer for the step this edit is (D1012): a
	// caller that read the gate passes what it answered, and every caller that
	// did not passes the zero value, whose meaning is "not managed" — the
	// refusal below exactly as it was before the gate existed. It can only ever
	// lift that refusal, for a step of a class the gate covers, in a project the
	// marks say is managed, whose document the gate admitted (D1026: a Typed
	// edit is never admitted, whatever a plan looked like). A caller that hands
	// in a value it did not read from a gate is lying about a read, which no
	// field here can prevent — the tests that pin the wiring are what catch it.
	Managed initflow.Managed
	// DryRun, when it is not nil, is the classification the caller made: this
	// edit is never confirmed, never backed up and never written, and what
	// would have happened is printed as the dry run's document instead
	// (D1106). It comes after the coding-agent-session refusal below, so a dry
	// run is refused exactly where the write it describes would be.
	DryRun *settingsDryRun
}

// settingsEditDiff is the line diff the confirmation shows. It is built here
// rather than at each caller so every settings write prints the same shape.
func settingsEditDiff(old, new []byte) string {
	return lineDiff(string(old), string(new))
}

// applySettingsEdit shows, confirms, backs up and writes one edit. It returns
// nil only when the edit was written or when there was nothing to write; every
// refusal leaves the file byte-identical and says why.
func applySettingsEdit(cmd *cobra.Command, lc *localcfg.Config, e settingsEdit) error {
	out := cmd.OutOrStdout()
	changed := yamledit.Changed(e.Old, e.New)
	if !changed && e.DryRun == nil {
		fmt.Fprintf(out, "%s: no change\n", e.Path)
		return nil
	}
	diff := settingsEditDiff(e.Old, e.New)

	// The refusal comes before the confirmation and before anything is
	// written (SD9). The bytes are the caller's — it read them to build the
	// diff this prints — so what is refused here is the write and the
	// confirmation, never the read. A session outside a repository writes
	// nothing at all, `--yes` included; a session inside one writes the file
	// it could edit anyway, except when the edit is the typed one — the
	// owner's own hand, whose file decides the ceiling of every run the
	// branch starts.
	//
	// The one thing that lifts the refusal is the managed gate's answer
	// (D1012): a caller that read the gate and was admitted for THIS edit's
	// class passes it in e.Managed, and the step proceeds as it would for a
	// person. Every other value — the zero value every other caller passes, a
	// class the gate does not cover, an edit whose class is Typed — keeps the
	// refusal exactly as it was, and a caller that passes nothing changes
	// nothing.
	conds := settingsEditConditions(cmd, e)
	if marker := conds.Agent; marker != "" && (!e.insideRepo() || e.Typed) && !initflow.CanConfirmManaged(e.editClass(), conds, e.Managed) {
		fmt.Fprintf(out, "%s\n%s", e.Path, diff)
		return userErr("refusing to write %s in a coding agent's session (%s is set): %s; run this yourself at your own terminal: %s; nothing was written",
			e.Path, marker, e.What, e.Command)
	}

	// The dry run stops here: the classification is the caller's, and what is
	// left of this function is the confirmation, the backup and the write,
	// none of which a dry run does (D1106). It is after the refusal above on
	// purpose — a dry run answers for the write, so it is refused wherever the
	// write would be, and the fence stays the first thing either path meets.
	if e.DryRun != nil {
		return writeSettingsWriteDoc(out, e.DryRun.document(e, changed), e.DryRun.jsonOut)
	}

	fmt.Fprintf(out, "%s\n%s", e.Path, diff)
	// The owner's own terminal in a managed project asks nothing (the ruling of
	// 2026-11-01): the gate read this edit's document, admitted it, and its
	// verdict IS the confirmation — the diff above is the whole of what the
	// owner reads. The class is still the class: initflow.GateConfirms is false
	// for a Typed edit whatever a plan looked like (D1026, and settingsEdit's
	// own editClass), so the widening directions keep the typed name, and a
	// project that is not managed, a mark that did not answer and an edit the
	// gate refused all arrive here as the zero value every fence reads exactly
	// as it did before the gate existed.
	//
	// Which sentence that is read as is the session's own, and nothing else in
	// this path is: a coding agent's session reads AgentProceeded, which names
	// the marker this step proceeds under, and the owner's terminal reads
	// GateProceeded, which says where the verdict was recorded. gateSentence is
	// the one place that choice is made (update_managed.go), so this surface
	// cannot be the one that prints the owner's line to a session.
	if !e.Yes && initflow.GateConfirms(e.editClass(), conds, e.Managed) {
		fmt.Fprintf(out, "  %s\n", gateSentence(conds.Agent))
	} else if e.Typed {
		// The widening is named before the prompt and not only in the
		// refusal a session with no terminal gets: the confirmation a person
		// reads is one block, and the sentence saying what makes this edit
		// different from the ordinary y/N is part of it.
		fmt.Fprintf(out, "%s\n", e.TypedWhy)
		// --yes answers the typed form too, and only here: the refusal above
		// has already decided that this is not a coding agent's session, so
		// what is left to confirm is the person's own word for it (D1107).
		if e.Yes {
			fmt.Fprintln(out, "  confirmed by --yes")
		} else if err := confirmTypedStrict(cmd, lc, e.TypedWhy, e.What); err != nil {
			return err
		}
	} else if err := confirmSettingsWrite(cmd, lc, e.Path, e.Yes, e.What); err != nil {
		return err
	}

	// The file may have moved since the caller read it — another writer, or
	// this same person in another terminal — and writing the bytes built from
	// the older read would silently discard that edit. The comparison is the
	// whole of the compare-and-swap: a file that changed is a refusal with
	// nothing written, so the person repeats the edit against what is there.
	// The re-read is the reader the caller's own read used — config.ReadRegular
	// for a checkout's file and for the project layer file, os.ReadFile for the
	// local config — so a swap for a symlink is refused here too rather than
	// written through.
	current, err := readSettingsCurrent(e)
	if err != nil {
		return userErr("re-reading %s before writing it: %v", e.Path, err)
	}
	if !bytes.Equal(current, e.Old) {
		return userErr("%s changed since it was read: nothing was written; read it again and repeat the edit", e.Path)
	}

	backup, err := backupFile(e.Path, e.Old, time.Now())
	if err != nil {
		return userErr("backing up %s before writing it: %v", e.Path, err)
	}
	if err := writeFileAtomic(e.Path, e.New); err != nil {
		return userErr("writing %s: %v", e.Path, err)
	}
	fmt.Fprintf(out, "wrote %s (backup at %s)\n", e.Path, backup)
	return nil
}

// insideRepo reports whether the edit lands in a repository's own fugaro.yaml
// (`--layer repo`), the one document an agent session may write.
func (e settingsEdit) insideRepo() bool { return e.Kind == settingsDocRepo }

// editClass is the confirmation class of this edit, which is the class the
// managed gate reads: Typed is D846's widening form — a loosened ceiling, a
// floor removed — and everything else is the ordinary y/N. It is derived here
// and not passed beside Managed, because the two facts a confirmation is
// decided under have to agree: a caller that could name a class of its own
// could name the one the gate covers for an edit that is Typed, and D1026's
// rule is exactly that no plan gate admits one.
func (e settingsEdit) editClass() initflow.ConfirmClass {
	if e.Typed {
		return initflow.Typed
	}
	return initflow.YesOrTyped
}

// readSettingsCurrent re-reads the file one edit is about to overwrite, for
// the comparison that refuses an edit built on bytes that have moved. It reads
// the document the way the caller's own read read it: a checkout's fugaro.yaml
// and the project layer file through config.ReadRegular, which refuses a
// symlink, a FIFO or an oversized file, so the swap it guards against cannot be
// made with one; the local project config through os.ReadFile, as every other
// reader of that file in this package reads the person's own dotfile.
func readSettingsCurrent(e settingsEdit) ([]byte, error) {
	if e.Kind == settingsDocLocal {
		return os.ReadFile(e.Path)
	}
	return config.ReadRegular(e.Path, config.LayerMaxBytes)
}

// settingsEditConditions is who is there for one edit, in initflow's own terms:
// the command's own stdin, the edit's own --yes and the marker of the session
// it runs in. It is read once per edit, so the fence above and the gate below
// are decided under one struct rather than two readings that could disagree.
func settingsEditConditions(cmd *cobra.Command, e settingsEdit) initflow.Conditions {
	return initflow.Conditions{Terminal: stdinIsTerminal(cmd.InOrStdin()), Yes: e.Yes, Agent: agentMarker(os.Getenv), Ask: e.Ask}
}

// confirmSettingsWrite is the ordinary y/N confirmation: --yes answers it, a
// terminal asks, and anything else refuses rather than guessing. It is
// confirmTyped's shape without the typed name, because most settings writes are
// not the widening directions D846 reserves the typed form for.
func confirmSettingsWrite(cmd *cobra.Command, lc *localcfg.Config, path string, yes bool, what string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "⚠ CONFIRM (project %s): %s\n", safetext.Field(lc.Name), what)
	if yes {
		fmt.Fprintln(out, "  confirmed by --yes")
		return nil
	}
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("%s, or pass --yes once you have read what it does", initflow.NeedsTerminal("the confirmation of this write"))
	}
	fmt.Fprintf(out, "Write this to %s? [y/N]: ", path)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", err)
	}
	if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
		return userErr("not confirmed; nothing was written")
	}
	return nil
}
