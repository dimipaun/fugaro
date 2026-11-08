package cli

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is the one write path of the settings commands: the diff, the
// sentence, the coding-agent-session refusal, the backup and the atomic
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
	// What is the sentence naming what changes. It is printed before the write —
	// the whole of what the y/N confirmation used to show, minus the prompt
	// itself (D1540) — and it is the sentence the one question a write may still
	// ask is built from (announceSettingsWrite, D1544).
	What string
	// Typed marks the widening directions: a loosened ceiling, a floor
	// removed, a followup key turned on. They are the owner's own step, so
	// the marker fence below refuses them in a coding agent's session whatever
	// --yes says, and at the owner's own terminal they ask nothing at all
	// (D1552, D1555; D1540 is why no prompt replaces the typed name here).
	Typed bool
	// TypedWhy is the sentence saying what this form widens: it is printed
	// before the write, and it is the sentence the coding-agent refusal above
	// carries beside the marker and the command.
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
	// Yes is --yes. The confirmations it used to answer are gone (D1540), so
	// nothing in this path reads it as an answer any more: it stands in the
	// conditions the fence and the gate are decided under, and the flag itself
	// is T11's to remove.
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

// settingsEditDiff is the line diff every settings write prints. It is built here
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

	// The refusal comes before the sentence and before anything is written
	// (SD9). The bytes are the caller's — it read them to build the
	// diff this prints — so what is refused here is the write and the sentence,
	// never the read. A session outside a repository writes
	// nothing at all, `--yes` included; a session inside one writes the file
	// it could edit anyway, except when the edit is the widening one — the
	// owner's own hand, whose file decides the ceiling of every run the
	// branch starts.
	//
	// The one thing that lifts the refusal is the managed gate's answer
	// (D1012): a caller that read the gate and was admitted for THIS edit's
	// class passes it in e.Managed, and the step proceeds as it would for a
	// person. Every other value — the zero value every other caller passes, a
	// class the gate does not cover, an edit whose class is Typed — keeps the
	// refusal exactly as it was, and a caller that passes nothing changes
	// nothing. The marker is the fence itself for a widening (D1552, D1555):
	// the class rule is the second thing that refuses one, and a fence read off
	// a class the same release deletes would leave the ceiling unfenced.
	conds := settingsEditConditions(cmd, e)
	if marker := conds.Agent; marker != "" && (!e.insideRepo() || e.Typed) && !initflow.CanConfirmManaged(e.editClass(), conds, e.Managed) {
		fmt.Fprintf(out, "%s\n%s", e.Path, diff)
		return userErr("refusing to write %s in a coding agent's session (%s is set): %s; run this yourself at your own terminal: %s; nothing was written",
			e.Path, marker, e.What, e.Command)
	}

	// The dry run stops here: the classification is the caller's, and what is
	// left of this function is the sentence, the backup and the write,
	// none of which a dry run does (D1106). It is after the refusal above on
	// purpose — a dry run answers for the write, so it is refused wherever the
	// write would be, and the fence stays the first thing either path meets.
	if e.DryRun != nil {
		return writeSettingsWriteDoc(out, e.DryRun.document(e, changed), e.DryRun.jsonOut)
	}

	fmt.Fprintf(out, "%s\n%s", e.Path, diff)
	// No write asks anything (D1540), so what a person reads is one of three
	// things. A managed project's own terminal reads the gate's sentence (the
	// ruling of 2026-11-01): the gate read this edit's document, admitted it, and
	// its verdict IS what the write proceeds on — the diff above is the whole of
	// what the owner reads. A widening reads what it widens (D1552, D1555):
	// initflow.GateConfirms is false for one whatever the verdict was (D1026, and
	// settingsEdit's own editClass). Every other edit reads the sentence naming
	// it, and the one question a write may still ask is the one --ask puts back
	// (D1544). A project that is not managed, a mark that did not answer and an
	// edit the gate refused all arrive here as the zero value every fence reads
	// exactly as it did before the gate existed.
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
		// A widening is the owner's own step, and the owner's own terminal
		// asks nothing for it (D1552, D1555): a coding agent's session was
		// refused above, whatever --yes said, and what is left here is the
		// sentence saying what this write widens and the sentence saying what
		// it writes — the whole of what the typed name's confirmation used to
		// show, minus the prompt itself. The typed name this branch used to ask for
		// was the last prompt of a raise form, and it is gone with D1540: no
		// command waits on stdin, and a question a session can answer is not a
		// fence.
		fmt.Fprintf(out, "%s\n%s\n", e.TypedWhy, e.What)
	} else if err := announceSettingsWrite(cmd, e.Managed.Managed, e.What); err != nil {
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
// and not passed beside Managed, because the two facts a write is decided
// under have to agree: a caller that could name a class of its own
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
	return initflow.Conditions{Terminal: stdinIsTerminal(cmd.InOrStdin()), Yes: e.Yes, Agent: agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())), Ask: e.Ask}
}

// announceSettingsWrite says what one settings write does and asks whether to
// make it — a question only --ask in a project the marks answered managed ever
// raises (D1540, D1544). Without the flag it prints the sentence, writes
// nothing of its own and returns nil: the run proceeds, which is the policy's
// answer to a step that used to ask, and the fence, the compare-and-swap and
// the backup are what bound it. With the flag the sentence is followed by the
// one question the flag is for, asked by the one sanctioned reader
// (askConfirm), which refuses where nobody can answer it instead of letting a
// silent proceed stand in.
//
// It is the one place a settings write says its sentence and asks, so the
// callers that write outside applySettingsEdit — `accounts add` and `secrets rm`
// on the paths where that helper returns before it announces anything — ask in
// the same words as the helper does. A "no" is an answer and not an error: every
// caller here answers it the same way, because every one of them leaves the same
// thing behind, a document byte-identical to the one it read.
//
// managed is the gate's own answer for this write (initflow.Managed.Managed,
// the same value askDecide decides the flag's line and refusal under), and it
// is what makes the question the flag's question. In a project the marks did
// not answer "managed" for there is nothing for --ask to restore — that is the
// line initflow.AskUnmanaged prints — so the flag asks nothing here, and a
// question would be the one path where it changed a step in a project it says
// it leaves alone: a y/N at the owner's terminal, and, where nobody can answer,
// a refusal naming a managed project that is not this one. A command that
// registers no --ask passes false, which is the same answer askConfirm gives
// the flag it does not have.
func announceSettingsWrite(cmd *cobra.Command, managed bool, what string) error {
	fmt.Fprintln(cmd.OutOrStdout(), what)
	if !managed {
		return nil
	}
	ok, err := askConfirm(cmd, "make this change?")
	if err != nil {
		return err
	}
	if !ok {
		return userErr("not confirmed; nothing was written")
	}
	return nil
}
