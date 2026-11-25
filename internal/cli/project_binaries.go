package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro project binaries` — K2 of
// docs/config-ownership/implementation-plan.md, which is D2613's answer for the
// binaries block: the paths a run may add a binary at (`binaries.allow`) and the
// cap one is held to (`binaries.max_bytes`).
//
// The block is the owner's grant over the bytes a run may put on a branch, and
// this machine's project config is its carrier from AR1 on (D981–D983, D986):
// `fugaro.yaml` has no such key anywhere, because that file lives in the
// checkout the run's own agent writes and a key there would let the coder admit
// the very payload the rule refuses, and the shared config refuses it for the
// launcher's reason besides. The node that carries it to a run is the project's
// policy node, whose stage every write here runs (the after-write hook below),
// so the block is written here and made live here.
//
// The three verbs are the two directions of the list and the one scalar of the
// block: `allow` admits the paths one pattern matches, `deny` takes a pattern
// away, and `max-bytes` writes the cap. The direction is what D2617 classifies,
// and it is the classification below rather than a per-verb constant: admitting
// paths is the widening write — a run may add a file it could not add before —
// so it is `Typed` and a coding agent's session is refused it whatever the gate
// says; a pattern taken away, and a cap lowered, only ever narrow what a run may
// add, so neither is a widening — they meet the fence every write to this file
// meets instead, which is applySettingsEdit's, and a coding agent's session is
// refused them unless the project's own marks admit a removing write.
//
// The write itself is the one write path (settings_write.go), exactly as every
// other settings write is: settingsLocalFile resolves the file, applySettingsEdit
// is the diff, the sentence, the coding-agent fence, the backup and the atomic
// write, and the block's own rule is projectpolicy's — ValidBinariesAllow and
// ValidBinariesMaxBytes, the same two functions the local loader and the node's
// decode call, so a pattern this command accepts is one a publish can carry
// and one a run's finalize enforces. A second spelling of either rule
// here is how a lock comes to mean two things.
//
// Every write ends with the after-write hook (C1, D2615), and the block is
// exactly the case the hook is for: the value lands in this machine's config
// while the node a run reads is what serves it, so the change owes the policy
// stage and this command runs it rather than leaving the owner a second command.
// A stage that does not leave the change live is one sentence and a non-zero
// exit (afterWriteNotLive, D2614), and the write stays either way.
//
// One fact about the block is not obvious from the two key names and is the
// reason two of the refusals below exist: **the block this file states is the
// whole block a publish carries**. Once the config carries a `binaries:` block
// it replaces the node's, byte for byte, and a config that states none keeps the
// node's (BinariesEffective, D987) — so the first write of one member into a
// file that carried no block states a block, and that block's other member is
// then absent from the node as well. A file that states `max_bytes` alone
// publishes a project that admits no path at all, and a file that states
// `allow: []` is how a published admission is taken away on purpose.

// The two registry rows this command writes, by the spelling the settings write
// path looks a row up by.
const (
	binariesAllowKey    = "binaries.allow"
	binariesMaxBytesKey = "binaries.max_bytes"
)

// newProjectBinariesCmd is `fugaro project binaries`: the paths a run of the
// project may add a binary at, and the cap one is held to.
func newProjectBinariesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "binaries",
		Short: "The paths a run may add a binary at, and the cap one is held to",
		Long: `Write the binaries block this machine's project config carries: the paths a run of the project may add a binary at, and how large one may be.

The block is the owner's grant, and a run of the project is the only reader: each of the three writes below publishes it into the project's policy node, where the run's finalize holds an added file to it — one at a path no pattern admits, or one above the cap, fails the run and is held out of the commit. No branch can widen the grant: ` + "`fugaro.yaml`" + ` carries no such key, and the shared config refuses it.

` + "`allow PATTERN`" + ` admits the paths one doublestar glob matches, relative to the repository root; ` + "`deny PATTERN`" + ` takes one pattern away. Admitting is the widening write, so it is refused in a coding agent's session whatever the gate says and written at your own terminal, where it says what it widens and asks nothing. Taking a pattern away narrows the grant and is not the widening step, so what it meets is the fence every write to this file meets: a coding agent's session is refused it unless the project's own marks admit a removing write.

` + "`max-bytes N`" + ` writes the cap in bytes. It may only be lowered, to at most 2097152 (2 MiB, the ceiling the rule ships); lowering it from a cap this file states narrows the grant and meets the fence every write to this file meets, while raising it back, or writing a cap into a file that states no block at all, is the widening direction and refused in a session whatever the gate says — the published cap and grant cannot be read off this file, so that write is the owner's own step.

The block this file states is the whole block that reaches the project's policy node: a file that carries a ` + "`binaries:`" + ` block replaces the published one, while a file that says nothing keeps it.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newProjectBinariesAllowCmd(), newProjectBinariesDenyCmd(), newProjectBinariesMaxBytesCmd())
	return cmd
}

func newProjectBinariesAllowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "allow <pattern>",
		Short: "Admit the paths one pattern of the project's grant matches",
		Long: `Add one pattern to the binaries.allow list of this machine's project config: the paths a run of the project may add a file at, one doublestar glob relative to the repository root.

The pattern is held to the rule the node itself is held to (` + "`projectpolicy.ValidBinariesAllow`" + `): non-empty, relative, with no parent component, at most 128 characters, and a glob the matcher reads. A misspelt pattern is refused here rather than written, because a pattern the matcher cannot read is a lock that is not there — it admits nothing while looking like a grant.

Admitting paths is the widening direction: the write is refused in a coding agent's session, whatever the gate would have said, and your own terminal writes it and asks nothing.

The block this file states is the whole block this write publishes, so a file that stated none replaces the published one with this list — which is why the sentence beside this write names the paths it admits.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectBinariesAllow(cmd, args[0]) },
	}
}

func runProjectBinariesAllow(cmd *cobra.Command, pattern string) error {
	sel, lc, err := settingsLocalFile("fugaro project binaries edits")
	if err != nil {
		return err
	}
	if err := projectpolicy.ValidBinariesAllow([]string{pattern}); err != nil {
		return userErr("%v; nothing was written", err)
	}
	target := settingsTarget{kind: settingsDocLocal, path: sel.Path}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	next, err := projectBinariesSplice(sel.Path, func() ([]byte, error) {
		return yamledit.AddListItem(old, projectBinariesPath(binariesAllowKey), pattern)
	})
	if err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: binaries.allow already admits the paths %s matches; nothing was written\n", sel.Path, safetext.Field(pattern))
		return nil
	}
	return projectBinariesWrite(cmd, sel, lc, old, next, settingsEdit{
		Old:      old,
		New:      next,
		What:     fmt.Sprintf("add %s to binaries.allow in %s", safetext.Field(pattern), sel.Path),
		Typed:    true,
		TypedWhy: projectBinariesAllowWhy(lc, pattern),
		Command:  fmt.Sprintf("fugaro project binaries allow %s", quoteWord(pattern)),
	}, binariesAllowKey)
}

// projectBinariesAllowWhy is the sentence a widening allow prints at the
// owner's terminal, and the whole of what the refusal in a coding agent's
// session says beside the diff: what the pattern admits, and — because the block
// this file states is the block a publish carries — what becomes of a grant the
// node holds and this file does not state.
func projectBinariesAllowWhy(lc *localcfg.Config, pattern string) string {
	why := fmt.Sprintf("this widens the project's binary admission by admitting every path %s matches: a run of the project may add a file there, within the cap, instead of failing finalize", safetext.Field(pattern))
	if lc != nil && lc.Binaries == nil {
		return why + "; this file states no binaries block yet, and the block it states now is the whole one this command publishes: whatever paths the published node admits beside this one are replaced by the list this file carries"
	}
	return why
}

func newProjectBinariesDenyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deny <pattern>",
		Short: "Take one pattern away from the project's binary admission",
		Long: `Remove one pattern from the binaries.allow list of this machine's project config, so a run may no longer add a file at the paths it matches. Every other pattern stands.

The pattern is taken away whole and compared as written, so it is the exact entry the file carries — ` + "`fugaro project binaries deny brand/*.png`" + ` removes that entry and does not re-aim another one at those paths.

A pattern this file does not carry is not a failure and nothing is written: the list this file states is the grant a publish carries, so a file that states one admits no path but the ones in it. A file that states no ` + "`binaries:`" + ` block at all is refused instead, because there is no entry beside the node's to take away: the published list is what stands until this file states one, and the only edit a removal could make there would take the whole admission away rather than one pattern.

Taking a pattern away narrows the grant and is not the widening step, so it meets the fence every write to this file meets: a coding agent's session is refused it unless the project's own marks admit a removing write. The block this file states is the whole block that reaches the node, and the change reaches a run through this write's own publish.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectBinariesDeny(cmd, args[0]) },
	}
}

func runProjectBinariesDeny(cmd *cobra.Command, pattern string) error {
	sel, lc, err := settingsLocalFile("fugaro project binaries edits")
	if err != nil {
		return err
	}
	// A file that states no binaries block is the one case a removal cannot
	// answer. What it would have to write is an entry taken out of a list the
	// file does not carry, and the entry it would have to leave behind is the
	// published one — the list a stated block replaces: writing `binaries:
	// {allow: []}` would take away every published admission rather than the one
	// pattern asked about, and writing nothing would leave the pattern admitted
	// while the command said it denied it. So the refusal names both, and the
	// command that states the block is the allow beside this one.
	if lc.Binaries == nil {
		return userErr("%s states no binaries block, so there is no allow entry to take away: a file that says nothing keeps the published node's list (D987), and the only list a removal could write here is an empty one — which admits nothing at all. Nothing was written: state the block this project should have with `fugaro project binaries allow <pattern>` for each path it admits, or write the `binaries:` block in %s by hand", sel.Path, sel.Path)
	}
	if !projectBinariesAdmits(lc.Binaries.Allow, pattern) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: binaries.allow carries no %s; nothing was written\n", sel.Path, safetext.Field(pattern))
		return nil
	}
	target := settingsTarget{kind: settingsDocLocal, path: sel.Path}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	// The empty list is left as a written empty list and never as a removed key:
	// `binaries:\n  allow:\n` is the file stating a block that admits nothing,
	// which a publish carries onto the node as the absent block (D987), while a
	// `binaries:` key taken away is the file saying nothing about the block — the
	// same bytes a project that never set one publishes, and the opposite of what
	// a removal of the last pattern is asked for.
	next, err := projectBinariesSplice(sel.Path, func() ([]byte, error) {
		return yamledit.RemoveListItem(old, projectBinariesPath(binariesAllowKey), pattern)
	})
	if err != nil {
		return err
	}
	return projectBinariesWrite(cmd, sel, lc, old, next, settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("remove %s from binaries.allow in %s", safetext.Field(pattern), sel.Path),
		Command: fmt.Sprintf("fugaro project binaries deny %s", quoteWord(pattern)),
	}, binariesAllowKey)
}

func newProjectBinariesMaxBytesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "max-bytes <n>",
		Short: "Write the cap an added binary is held to",
		Long: `Write binaries.max_bytes in this machine's project config: the largest a single file a run adds may be, in bytes.

The cap may only be lowered. ` + "`projectpolicy.ValidBinariesMaxBytes`" + ` is the rule and it refuses anything above 2097152 (2 MiB), which is the ceiling the rule ships; a project that writes no cap at all is held to that ceiling, and a cap of zero is refused rather than read as the absent member, because zero is the one value that would silently raise the cap to it.

Lowering the cap from the one this file states narrows the grant and is not the widening step, so it meets the fence every write to this file meets: a coding agent's session is refused it unless the project's own marks admit a removing write. Raising it back, or writing a cap into a file that states no binaries block at all, is the widening direction, refused in a session whatever the gate says: the published cap is what a file that states none leaves in force and cannot be read here, so that write is the owner's own step — and the block this file states is the whole block that reaches the node, so a first write of one member beside it is a statement about the other member as well.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectBinariesMaxBytes(cmd, args[0]) },
	}
}

func runProjectBinariesMaxBytes(cmd *cobra.Command, value string) error {
	sel, lc, err := settingsLocalFile("fugaro project binaries edits")
	if err != nil {
		return err
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return userErr("max-bytes takes a whole number of bytes, and %s is not one: write the cap as digits, e.g. 1048576 for 1 MiB; nothing was written", safetext.Field(value))
	}
	if err := projectpolicy.ValidBinariesMaxBytes(n); err != nil {
		return userErr("%v; nothing was written", err)
	}
	target := settingsTarget{kind: settingsDocLocal, path: sel.Path}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	next, err := projectBinariesSplice(sel.Path, func() ([]byte, error) {
		return yamledit.SetScalar(old, projectBinariesPath(binariesMaxBytesKey), strconv.FormatInt(n, 10))
	})
	if err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: binaries.max_bytes already reads %d; nothing was written\n", sel.Path, n)
		return nil
	}
	typed, why := projectBinariesMaxBytesDirection(lc, n)
	return projectBinariesWrite(cmd, sel, lc, old, next, settingsEdit{
		Old:      old,
		New:      next,
		What:     fmt.Sprintf("set binaries.max_bytes to %d in %s", n, sel.Path),
		Typed:    typed,
		TypedWhy: why,
		Command:  fmt.Sprintf("fugaro project binaries max-bytes %d", n),
	}, binariesMaxBytesKey)
}

// projectBinariesMaxBytesDirection is the cap write's classification (D2617):
// whether it is the `Typed` step, and the sentence saying what it widens.
//
// A cap is read against the one this file states, and a file that states none is
// read as unknown rather than as the ceiling: the config's silence about the
// block is what keeps the published cap, and nothing on this machine reads it —
// so a first write of the member is classified as the owner's own step, which is
// the direction that cannot be wrong. A file that states a block is answered
// from it, and there the ceiling is what a missing member means, so only a value
// above the member the block carries is a raising write; a lowered cap is a
// narrowing one and a session may make it.
func projectBinariesMaxBytesDirection(lc *localcfg.Config, n int64) (bool, string) {
	if lc == nil || lc.Binaries == nil {
		return true, fmt.Sprintf("this writes binaries.max_bytes into a file that states no binaries block, and the block this file states is the whole one this command publishes: the published grant's own cap is not read here, so the cap this write states is the owner's own step — raising it admits a larger file at every path the project admits, and the paths this file states are the whole list the node will carry")
	}
	if lc.Binaries.MaxBytes != 0 && n > lc.Binaries.MaxBytes {
		return true, fmt.Sprintf("this widens the project's binary admission by raising the cap from %d to %d bytes: a run may add a file it could not add before, at every path the project admits", lc.Binaries.MaxBytes, n)
	}
	return false, ""
}

// projectBinariesWrite is the whole write path the three verbs share: the
// document's own read-back of the edited bytes, the one applySettingsEdit that
// diffs, fences, backs up and writes them, and the after-write hook the block
// owes.
//
// The read-back is the block's own relative and not every rule of it: the local
// loader is where the block's spellings are judged (localcfg.Parse calls the
// same two projectpolicy functions), and a file this command writes must be one
// that loader reads, or the installation is left with a config no command of
// this binary opens — which is the one failure the HARD RULE of `fugaro update`
// cannot survive.
func projectBinariesWrite(cmd *cobra.Command, sel localcfg.Selection, lc *localcfg.Config, old, next []byte, e settingsEdit, key string) error {
	if err := settingsReparse(settingsTarget{kind: settingsDocLocal, path: sel.Path}, next); err != nil {
		return err
	}
	e.Path, e.Kind = sel.Path, settingsDocLocal
	if err := applySettingsEdit(cmd, lc, e); err != nil {
		return err
	}
	return runAfterWrite(cmd.Context(), cmd, afterWriteChange{Key: key, Store: settings.StoreLocal})
}

// projectBinariesPath is the key's own path in a document: the two members are
// one block of the config file, and the registry spells them with the dot.
func projectBinariesPath(key string) []string { return strings.Split(key, ".") }

// projectBinariesAdmits reports whether an allow list carries an entry as
// written. It is an exact comparison and not a pattern match: a removal takes
// away the entry a person wrote, and an entry that matches the same paths is
// another entry of the list.
func projectBinariesAdmits(allow []string, pattern string) bool {
	for _, entry := range allow {
		if entry == pattern {
			return true
		}
	}
	return false
}

// projectBinariesSplice is one edit of the block through the shared editor, with
// the refusal the editor's own rules make said in the owner's words: it will not
// splice into a flow collection, a mapping that carries a merge key or a
// tab-indented block, and a write must never fall back to a re-marshalled
// document, which would drop the comments and the key order of everything it
// does not touch (D790). The file is named and the reason is the editor's, so
// the operator is told which block to put in block style rather than only that
// the edit failed.
func projectBinariesSplice(path string, edit func() ([]byte, error)) ([]byte, error) {
	next, err := edit()
	if err == nil {
		return next, nil
	}
	reason := yamledit.ReasonOf(err)
	if reason == "" {
		return nil, err
	}
	return nil, userErr("%s cannot be changed by one edit (%s), and this command keeps every comment and key order it does not touch, so it wrote nothing: write the entry in by hand, or put that block in block style — one item per line — and repeat the command", path, reason)
}
