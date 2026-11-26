package projectpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
)

// Change is one step of a migration, in the words a caller renders as the
// diff: the version the step moves the node off, the version it writes, what
// it changes, what it keeps as it is, and the move the operator must still
// make ("" when the step finishes the job).
//
// Kept is the node's own top-level keys that the step does not touch — every
// key but version, read off the node rather than listed here, so a block a
// newer release added or the owner wrote by hand is reported too. It is what
// tells an operator that this is a migration and not a republish: `fugaro
// project policy publish --delete` and a fresh publish lose the entries they
// edited, and the diff has to show that they survived.
type Change struct {
	From    int
	To      int
	Changed string
	Kept    []string
	Note    string
}

// migrator is one step of the chain: it reads a node written in one policy
// version and returns the same node written in the next. A step's only input
// is the node — it may derive nothing from the installation, the local config
// or the network — and it reports what it did as a Change.
type migrator func(raw json.RawMessage) (json.RawMessage, Change, error)

// migrators is the chain, keyed by the version a step reads. A version that
// bumps Version adds exactly one entry here, in the same change.
var migrators = map[int]migrator{
	3:  migrateV3ToV4,
	4:  migrateV4ToV5,
	5:  migrateV5ToV6,
	6:  migrateV6ToV7,
	7:  migrateV7ToV8,
	8:  migrateV8ToV9,
	9:  migrateV9ToV10,
	10: migrateV10ToV11,
}

// tooOld names the versions this Fugaro reads but has no migrator for. The
// refusal is not a sentence of this file's: a version below Version is a node
// older than its reader, which is one direction of the skew version.go's
// CLISkewNewer and RunnerSkewNewer word — the pair for a node no step reaches,
// beside the carried pair a version above them gets — so Migrate answers with
// the *VersionError Decode returns and the caller prints the remedy that fits
// its side (SkewSentence) rather than a second wording of the same move that
// could drift from it.
//
// The table exists so the version-bump guard
// (TestEveryOlderVersionHasAMigratorOrARefusal) can tell a version this Fugaro
// deliberately refuses from one it merely forgot: a bump that adds neither a
// step nor a row fails there. The chain starts at 3, the version the releases
// before the v4 bump wrote (VersionSince[3] is 0.9.0).
var tooOld = map[int]bool{
	1: true,
	2: true,
}

// Migratable reports whether a node written in version v is one the chain can
// carry forward, as opposed to one below its first step (tooOld), whose only
// move is the publisher's delete-then-publish.
//
// It is asked where the node itself is not read: `fugaro doctor` meets an older
// node through Decode alone, and the line it prints — `fugaro update`, which
// migrates, or the delete and a publish — turns on this and nothing else. It
// lives beside the chain so the answer is the chain's own and not a second
// table in the caller that a bump of Version can leave behind. A version above
// this Fugaro's is not migratable either: Migrate refuses it before the walk,
// and the move there is a newer binary.
func Migratable(v int) bool {
	if v > Version {
		return false
	}
	for from := v; from < Version; from++ {
		if _, ok := migrators[from]; !ok {
			return false
		}
	}
	return true
}

// shapeFreeSteps names the versions whose step moves the node's number and
// nothing else: its node is, member for member, the node above it. Version 5
// is one — the `binaries` block version 6 models is a top-level key no
// version-5 release wrote, so a version-5 node is a version-6 node that states
// no grant, which is what a project that never set the key means. Version 6 is
// one too: the `model_grades` member version 7 models is a top-level key no
// version-6 release wrote either, and a node without it sets no grade, which is
// what the ladder's rungs read as "leave this rung to the owner" (D2039).
// Version 7 is the third (AR1): the member version 8 is about arrived at
// version 6 and this bump carries no member at all — what it marks is that the
// project's config states the block from this release on, so a version-7 node
// is already a version-8 node in everything but the number.
//
// Version 8 is NOT one, and it is the fourth counter-example (MC21): its step
// rewrites the node's `craft_pools` and `crafts` blocks, splitting every pool
// whose members span the two halves of a run and repointing the crafts that
// named one, so a version-8 node is not a version-9 node that happens to carry
// no crossing — it is a node whose pools may cross, which the reader above
// refuses (config.ValidatePublishedPools). A reader that admitted it without
// running the step would serve a pool whose listed order the credentials service
// cannot honour, which is the whole of D1668.
//
// Version 9 is the fourth shape-free step (R14, D2752): the `reports_commit`
// member version 10 models is a top-level key no version-9 release wrote, and a
// node without it designates no repository — which is what a project that never
// named one means, and what a report run reads as "commit nothing anywhere".
// The member is safe by default when it is ignored (version.go says what the
// bump does and does not buy), so a version-9 node is a version-10 node in
// everything but the number, and the step is what keeps `fugaro update`'s own
// window from being a refusal: the update's policy stage and the images stage
// it sits beside cannot leave a launcher reading a node it refuses.
//
// The counter-examples are version 3 and version 4, and they are why this
// table is a table and not a rule about numbers. Version 4's step marks every
// ownerless pool legacy-open, so its node is not the one above it. Version 3's: that step's node needs a block (the Bitbucket
// token's allow_repos) that only a publish derives, so its Change carries a
// Note and a reader cannot read every version-3 node as a version-4 one.
//
// Version 10 is the fifth shape-free step (LM21, D3124): version 11 adds no
// member at all — what it marks is the release in which the credentials service
// issues a run's identity instead of the launcher's own grant (version.go's
// paragraph on it) — so a version-10 node is a version-11 node in everything
// but the number. The counter-examples above are why that has to be argued and
// not assumed, and it is: no reader of this release enforces anything by
// version 11, so there is nothing for the step to write beside the number.
// TestShapeFreeStepsOnlyMoveTheNumber holds every entry here to the first
// sentence, over a node carrying a member of every kind.
var shapeFreeSteps = map[int]bool{
	5:  true,
	6:  true,
	7:  true,
	9:  true,
	10: true,
}

// ReadsVersion reports whether a node written in version v is one this reader
// reads as one of its own: v is this Version, or every step between them is
// shape-free.
//
// It exists because a bump of Version is otherwise a window and not a safety
// (version.go, D990): `fugaro update` moves the credentials service onto its
// own release in the stage immediately after the policy stage, so the shape
// question is asked of a node that is already migrated — but the release
// behind's service is the one deployed until that stage applies, the job
// images of the update are replaced at other stages still, and every reader of
// this release meets the older node while those are in flight. A reader that
// refused a shape-identical node would refuse every cascade and
// Bitbucket-proxied model call for the length of the image builds, for a
// policy it reads member for member. A version that is not shape-free is
// refused exactly as it was, with the *VersionError and the remedy
// SkewSentence words: a version-3 node means something the version-4 reader's
// own release had to derive, and reading it as a version-4 one would run a
// policy nobody published.
//
// A version above this reader's is never one: there the reader is the side
// that is behind, and the move is an upgrade of the reader and not a tolerance
// in it.
func ReadsVersion(v int) bool {
	if v > Version {
		return false
	}
	for from := v; from < Version; from++ {
		if !shapeFreeSteps[from] {
			return false
		}
	}
	return true
}

// Migrate moves a published policy node forward to the version this Fugaro
// understands, one step at a time, and reports what each step did.
//
// It exists because a bump of Version is otherwise a wall: 0.14.0 raised the
// version to 4, and every installation holding a version-3 node could only
// reach it with `fugaro project policy publish --delete` and a fresh publish,
// which loses whatever the owner had edited into the node by hand. The chain
// carries such a node over instead, and each step's only input is the node
// itself: what a step cannot derive from the node (the bitbucket_token block,
// which publish derives from the project's local config) it does not invent,
// and a step whose node needs one says so in its Change.
//
// The answers, in order:
//
//   - a node already at Version is not walked, but it is still read for the one
//     step that runs AT a version rather than across two (migrateRemovedMode,
//     AB8a): a node that says the word the budget mode's vocabulary dropped
//     comes back rewritten, with a change, and every other node comes back byte
//     for byte with none. A node it rewrites is judged by Decode at the end
//     like any other migrated document; a node it does not touch is not judged
//     at all: whether it is a policy this Fugaro can use is Decode's answer,
//     and Migrate's is only whether it can be moved forward;
//   - a node written in another version is refused with a *VersionError, the
//     value Decode returns and SkewSentence words, in both directions: Found
//     above Understood is a node ahead of this Fugaro (upgrade it), and Found
//     below it is one behind — the direction SkewSentence words from the chain
//     itself, `fugaro update`'s in-place rewrite for a version a step reaches
//     (CLISkewCarried, RunnerSkewCarried) and `fugaro project policy publish
//     --delete` with a fresh publish for one below the first step
//     (CLISkewNewer, RunnerSkewNewer). A version below the chain's first step
//     is that second case, and carries no type of its own so a caller that
//     already branches on *VersionError for the older direction recognises it;
//   - a node the migration cannot read at all — not a JSON object, no version,
//     a version no release ever wrote, a top-level key written twice, or a
//     migrated document the strict reader still refuses — is refused with a
//     *MalformedError, and the node is never re-encoded on the way: decoding
//     into a map folds a repeated key onto the last one and would change the
//     policy silently;
//   - every refusal returns the input untouched, so a caller that ignores the
//     error cannot write a half-migrated document over the node. The changes
//     are the steps that were read before the refusal, and are empty when the
//     walk had not started; the note of the last of them is carried in the
//     refusal itself, so the operator reads the move that finishes the job
//     beside the reason it stopped.
//
// The chain is followed by the one step that runs at a version rather than
// across two, and the last of them by the reader's own judgement: the migrated
// bytes must decode with Decode, or the migration is refused. That is what
// holds a step to the shape the current reader reads rather than to the shape
// its author believed in, and it is how a version-3 node whose profile mounts
// bitbucket-token is refused here — the block that profile is bounded by is
// derived at publish, so the step reports the republish as the move that
// finishes the job instead of writing a node nothing can read.
func Migrate(raw []byte) ([]byte, []Change, error) {
	from, err := nodeVersion(raw)
	if err != nil {
		return raw, nil, err
	}
	if from > Version {
		return raw, nil, &VersionError{Found: from, Understood: Version}
	}
	if from == Version {
		// The chain has nothing to walk, but the node is still read for the
		// step that runs at this version (migrateRemovedMode): the word the
		// budget mode's vocabulary dropped left it without a bump, so a node
		// already at Version may be one no reader of this release serves and
		// the update that rewrites it is the only way forward.
		out, ch, changed, err := migrateRemovedMode(raw)
		if err != nil || !changed {
			return raw, nil, err
		}
		if err := migratedRefusal(out, nil); err != nil {
			return raw, nil, err
		}
		return out, []Change{ch}, nil
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return raw, nil, &MalformedError{Why: "it is not a JSON object", Err: err}
	}
	if err := checkNoDuplicateNames("the policy node", names); err != nil {
		return raw, nil, &MalformedError{Why: "a top-level key is written twice", Err: err}
	}

	cur := json.RawMessage(raw)
	var changes []Change
	for v := from; v < Version; v++ {
		step, ok := migrators[v]
		if !ok {
			if !tooOld[v] {
				// The version-bump guard forbids this state: a version with
				// neither a step nor a row is one nobody decided about, and it
				// is refused rather than migrated on a step that is not there.
				return raw, changes, fmt.Errorf("projectpolicy: this Fugaro has no migration step from policy version %d, and no refusal for it", v)
			}
			return raw, changes, &VersionError{Found: from, Understood: Version}
		}
		out, ch, err := step(cur)
		if err != nil {
			return raw, changes, err
		}
		cur, changes = out, append(changes, ch)
	}
	// The step that runs at a version rather than across two comes last, so a
	// node the walk carried over is read for the dropped word too: a version-9
	// node that says it is a version-10 node that says it (migrateRemovedMode,
	// R4).
	if out, ch, changed, err := migrateRemovedMode(cur); err != nil {
		return raw, changes, err
	} else if changed {
		cur, changes = out, append(changes, ch)
	}
	if err := migratedRefusal(cur, changes); err != nil {
		return raw, changes, err
	}
	return cur, changes, nil
}

// migratedRefusal is Migrate's last judgement of a document it would write:
// the migrated bytes must decode with Decode, or the migration is refused and
// the input comes back untouched. changes is what the walk read, and the note
// of the last step that carries one travels in the refusal, so the operator
// reads the move that finishes the job beside the reason it stopped.
func migratedRefusal(migrated json.RawMessage, changes []Change) error {
	if _, found, err := Decode(migrated); err != nil || !found {
		if err == nil {
			err = errors.New("the node is empty")
		}
		var note string
		for _, ch := range changes {
			if ch.Note != "" {
				note = ch.Note
			}
		}
		return &MalformedError{Why: "the migrated node is one the current reader refuses", Note: note, Err: err}
	}
	return nil
}

// migrateRemovedMode is the chain's one step that runs at a version rather than
// across two: it rewrites the budget mode from the word the vocabulary dropped
// to observe, in place, and leaves the node's version where it is (AB8a, R4,
// D3010).
//
// It exists because the word left the mode's vocabulary without a bump (D3025):
// a version-10 node an older CLI published, or one an administrator edited by
// hand, says it, and no step across two versions reaches a node that is already
// at Version — while no reader of this release serves such a node (the publish
// door refuses it, and ValidMode stops knowing it, AB2). `fugaro update` is the
// one command the upgrade is (CLAUDE.md), so the rewrite belongs to the
// migration, and it is what makes every node this release writes one its own
// readers can serve.
//
// It reads the mode out of the node's own bytes, before the strict reader is
// asked anything: the node's decoder refuses the word once validation moves
// (AB2's validBudget), so a step that decoded the node first could never load
// the node it exists to rewrite (F22). The mode is asked of the vocabulary's
// own predicate (Mode.Removed) rather than compared to a literal here, and what
// it is rewritten to is that vocabulary's own answer (Mode.Effective), so this
// step cannot read or spell a mode differently from the readers around it.
//
// The edit is confined to the budget block's mode member: every other member of
// the block and every other top-level key comes back byte for byte, because
// this is an edit of the owner's document and not a re-encoding of it. A node
// carrying no budget block, or one whose mode is a live mode, is not touched at
// all — changed says so, and the caller reports no change for it.
func migrateRemovedMode(raw json.RawMessage) (out json.RawMessage, ch Change, changed bool, err error) {
	bs, be, ok := memberValueSpan(raw, "budget")
	if !ok || IsNull(raw[bs:be]) {
		return raw, Change{}, false, nil
	}
	block := raw[bs:be]
	ms, me, ok := memberValueSpan(block, "mode")
	if !ok {
		return raw, Change{}, false, nil
	}
	var mode string
	if err := json.Unmarshal(block[ms:me], &mode); err != nil || !policy.Mode(mode).Removed() {
		return raw, Change{}, false, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw, Change{}, false, &MalformedError{Why: "it is not a JSON object", Err: err}
	}
	eff := policy.Mode(mode).Effective()
	out = spliceMember(raw, bs+ms, bs+me, json.RawMessage(strconv.Quote(eff)))
	return out, Change{
		From:    Version,
		To:      Version,
		Changed: fmt.Sprintf("the budget mode: %s -> %s", mode, eff),
		Kept:    keptKeys(doc, "budget"),
	}, true, nil
}

// DecodeCarriedForward reads a node that may be written one version behind,
// carrying it forward in memory rather than refusing it: the bytes the CLI
// would have written are applied to the bytes just read, for this reader alone.
//
// It exists for the readers that live on the far side of the gap a Version bump
// opens. A bump is a lockstep the operator runs with `fugaro update`, and the
// pieces that read the node are not all migrated in the same stage: the
// credentials service is deployed with the installation's other pieces and
// meets the old node until the policy stage runs, and `fugaro update`'s own
// line about the runs in flight asks the node before its stages have. Read
// refuses such a node — which is right where the reader DECIDES by it, since a
// node no version of this reader understands is not one to enforce — and wrong
// where the reader only asks a question about it, because the answer becomes
// "no policy" over the node every installation holds. Both readers reach the
// node through this function — the service through
// DecodeCarriedForwardShapeFree below, which is this one behind a floor, and the
// CLI's in-flight line through it directly (internal/cli's policyReadsLive) —
// and neither writes anything back: the node is the CLI's to migrate, and a
// second writer of the installation's policy is what the publish path's
// single-writer rule forbids. A reader that SERVES the node and cannot write it
// carries from ShapeFreeFloor up and no lower, because a carry is only as good
// as what the bytes it produces say.
//
// Only the direction the chain can carry is carried. A node ahead of this
// binary, or one below the chain's first step, comes back with the refusal
// Decode made: those are not this reader's to reinterpret, and a migration that
// has no step for a version reports its own refusal rather than inventing one.
// A node already at Version is not walked across anything — the steps for older
// versions do not run and nothing here re-encodes what they would have left.
//
// What a node AT Version is carried for, since AB2, is the one step that runs
// at a version rather than across two: a node that says the word the budget
// mode's vocabulary dropped. That step left no bump behind (D3025), so the node
// the release behind published states this release's version and says the word,
// and a carry that fired only on *VersionError would hand every reader along the
// update's way a refusal for the node the same run is about to rewrite — the
// second command the HARD RULE forbids. Migrate answers for the word itself, and
// for a node it has no step for it returns the bytes untouched and this function
// returns Decode's refusal, so the reach of the carry is the migration's own
// answer and not a second list kept here.
func DecodeCarriedForward(raw []byte) (*Policy, bool, error) {
	pol, found, err := Decode(raw)
	if err == nil {
		return pol, found, err
	}
	migrated, _, merr := Migrate(raw)
	if merr != nil {
		// The node's own refusal stands, in the words Decode gave it: this is
		// a node the chain cannot carry, and the caller's answer is the
		// refusal it would have had without the attempt.
		return pol, found, err
	}
	return Decode(migrated)
}

// ShapeFreeFloor is the oldest policy version a node may be carried forward
// from in memory by the reader that serves it, and it is deliberately not the
// chain's first step.
//
// The chain reaches version 3 (Migratable(3), migrators[3]), and the step it
// takes from there is shape-only: migrateV3ToV4 edits the version field and
// nothing else, because version 4 changed what a reader must ENFORCE and not
// the node's shape. What the node it produces is missing is what no step can
// write, and it is the reason that bump was a republish: bitbucket_token is
// DERIVED at publish from the project's local config (DeriveBitbucketToken), so
// a version-3 node carried forward in memory becomes a version-4 document
// claiming an enforcement the operator never published, with the list its
// Bitbucket credential is bounded by absent. The move for such a node is the
// republish migrateV3ToV4's own note names (bitbucketRepublishNote), and only
// the operator's `fugaro update` can make it.
//
// From 4 up, every step's whole input is the node — migrateV4ToV5 reads the
// pools it marks out of the node it was handed, and is told nothing — so a
// carry in memory states exactly what the operator's own migration writes. A
// bump that adds a step needing data from outside the node (the local config,
// the network, another installation) moves this floor to the version that step
// WRITES, because the node behind it becomes one no reader may serve before the
// operator's own move. That is where 4 comes from: the step below it, 3 → 4, is
// the one whose node needs the republish, and
// TestShapeFreeFloorIsTheVersionAfterTheRepublishBump is where that sentence is
// a test.
const ShapeFreeFloor = 4

// DecodeCarriedForwardShapeFree is DecodeCarriedForward behind ShapeFreeFloor:
// the read of a process that SERVES the published node and can never write it —
// internal/credsvc, deployed by `fugaro update` ahead of the stage that
// migrates the node — so a carry in memory that states more than the
// installation published is not one it may make.
//
// DecodeCarriedForward is right for the writer's own process, which carries the
// node forward to ask a QUESTION about it and then migrates it for real
// (internal/cli's policyReadsLive, whose answer only decides what the update's
// in-flight line says about the policy stage): a version-3 node there is one the
// same command is about to rewrite, so the question is put to the document the
// chain produces and the service re-reads a moment later. A service cannot
// rewrite it, so a version-3 node is refused here with Decode's own answer —
// the refusal its caller would have had without the attempt, which is the
// fail-closed direction for a reader that decides by the node. At the floor and
// above, this is DecodeCarriedForward exactly, with everything that function
// documents: one version behind, in memory, nothing written back.
func DecodeCarriedForwardShapeFree(raw []byte) (*Policy, bool, error) {
	v, err := nodeVersion(raw)
	if err != nil || v < ShapeFreeFloor {
		return Decode(raw)
	}
	return DecodeCarriedForward(raw)
}

// nodeVersion reads the version off a node without decoding it, and refuses
// the classes of node no version can be read from. It runs before the walk
// because the version decides what the walk does, and it is deliberately
// narrow: everything else about the node is judged by the step that reads it
// and by Decode at the end.
//
// A version below 1 is malformed rather than too old. VersionSince starts at
// 1 (the model itself, 0.8.0), so no release ever wrote a node at 0 or below,
// and a node that says so was hand-edited — the same reading MarkerHandEdited
// gives a negative rules generation. A missing version and a version that is
// not an integer fall here too: json.Unmarshal leaves a null version at 0.
func nodeVersion(raw []byte) (int, error) {
	if IsNull(raw) {
		return 0, &MalformedError{Why: "it is empty"}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, &MalformedError{Why: "it is not a JSON object", Err: err}
	}
	vraw, ok := doc["version"]
	if !ok {
		return 0, &MalformedError{Why: "it has no version"}
	}
	var v int
	if err := json.Unmarshal(vraw, &v); err != nil {
		return 0, &MalformedError{Why: "its version is not an integer", Err: err}
	}
	if v < 1 {
		return 0, &MalformedError{Why: fmt.Sprintf("its version is %d, and no Fugaro release wrote a policy version below 1", v)}
	}
	return v, nil
}

// migrateV3ToV4 writes the version field version 4 reads, and nothing else.
//
// Version 4 changed what a reader must *enforce*, not the node's shape: the
// Bitbucket token block and the profile rules that hold a profile mounting it
// to that block, so a version-3 node is version 4's document with the older
// number in it. The step therefore edits the node's own bytes rather than
// decoding it into a map and encoding it again — the second sorts the keys,
// compacts the whitespace, escapes <, > and &, and folds a repeated key onto
// the last one, which is a rewrite of the owner's document and not a migration
// of it. A caller that diffs the two nodes sees one field, and a key this
// Fugaro does not model comes back exactly as it was written.
func migrateV3ToV4(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-3 node is not a JSON object: %w", err)
	}
	out, err := writeVersionField(raw, 3, 4)
	if err != nil {
		return nil, Change{}, err
	}
	return out, Change{
		From:    3,
		To:      4,
		Changed: "the version field: 3 to 4",
		Kept:    keptKeys(doc, "version"),
		Note:    bitbucketRepublishNote(doc),
	}, nil
}

// migrateV4ToV5 writes the version field version 5 reads, and marks every pool
// that names no owner legacy-open.
//
// Version 5 is the first bump in this line that changes the node's SHAPE: a
// craft_pools entry gains owner and allowed_users, and a pool written now must
// name its owner where it is published (D1936). A pool an installation already
// holds has no owner, and no step may invent one — an owner is a person, and
// guessing one would hand a shared token to somebody the owner never chose.
// D1942's answer is to keep it serving exactly whom it served before the
// upgrade: the step writes the explicit legacy_open marker, which is a NEW NAME
// rather than the absence of an owner (F37), so a reader of the node can tell a
// pool that predates owners from one whose owner the operator has yet to name.
//
// The edit is byte-level like the step before it, and it is confined to the one
// member it changes: the pools' own bytes are re-emitted as they were written,
// with the marker inserted into the entries that need it, so a key this Fugaro
// does not model — inside a pool entry or anywhere else — comes back exactly as
// the owner wrote it. A pool that names an owner, and one already marked, are
// left alone: a version-4 node cannot carry the marker, but a hand-written one
// can, and rewriting it would be the second migration resting on a guess about
// the node's history.
//
// The republish note of the step before is not repeated here. A version-4 node
// is either one this chain has already carried over the Bitbucket block's
// derivation or one published by the release that reads it, so the block is
// there or the node was published without a profile mounting the token —
// neither is a move this step can still make.
func migrateV4ToV5(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-4 node is not a JSON object: %w", err)
	}
	out, err := writeVersionField(raw, 4, 5)
	if err != nil {
		return nil, Change{}, err
	}
	changed := "the version field: 4 to 5"
	kept := keptKeys(doc, "version")
	if praw, ok := doc["craft_pools"]; ok && !IsNull(praw) {
		start, end, ok := memberValueSpan(out, "craft_pools")
		if !ok {
			return nil, Change{}, errors.New("projectpolicy: the version-4 node's craft_pools field could not be located")
		}
		pools, marked, err := markLegacyOpenPools(out[start:end])
		if err != nil {
			return nil, Change{}, err
		}
		if marked > 0 {
			next := make(json.RawMessage, 0, len(out)+len(pools)-(end-start))
			next = append(next, out[:start]...)
			next = append(next, pools...)
			next = append(next, out[end:]...)
			out = next
			changed = fmt.Sprintf("the version field: 4 to 5, and legacy_open on %s with no owner", countPools(marked))
			kept = keptKeys(doc, "version", "craft_pools")
		}
	}
	return out, Change{From: 4, To: 5, Changed: changed, Kept: kept}, nil
}

// countPools words the number of pools a step marked, so the diff reads as a
// sentence about the owner's node rather than as a count of entries.
func countPools(n int) string {
	if n == 1 {
		return "1 pool"
	}
	return strconv.Itoa(n) + " pools"
}

// writeVersionField returns the node with its top-level version member written
// as to, and refuses when the member cannot be located. It is the whole of a
// step whose version is the only thing that changed, and the first half of one
// that edits a block as well: the version is written from the caller's `from`
// rather than from the node, because nodeVersion has already read it and a step
// that wrote a number it re-read would move a node it was not handed.
func writeVersionField(raw json.RawMessage, from, to int) (json.RawMessage, error) {
	start, end, ok := memberValueSpan(raw, "version")
	if !ok {
		return nil, fmt.Errorf("projectpolicy: the version-%d node's version field could not be located", from)
	}
	out := make(json.RawMessage, 0, len(raw))
	out = append(out, raw[:start]...)
	out = append(out, strconv.Itoa(to)...)
	out = append(out, raw[end:]...)
	return out, nil
}

// keptKeys is the node's top-level keys a step does not write — every key but
// the ones it names, read off the node rather than listed in the step, so a
// block a newer release added or the owner wrote by hand is reported too.
func keptKeys(doc map[string]json.RawMessage, written ...string) []string {
	kept := make([]string, 0, len(doc))
	for _, name := range sortedStringKeys(doc) {
		if !slices.Contains(written, name) {
			kept = append(kept, name)
		}
	}
	return kept
}

// memberValueSpan is the byte span of the value of one of the node's top-level
// members, and its name is the caller's: a step locates the field it edits by
// nesting rather than by spelling. It is a walk of the document's own tokens
// rather than a search for the text of the member's name, because a name can
// appear inside a block (a profile named version, a key of a block this Fugaro
// does not model) and the node is what the operator's diff is read against.
func memberValueSpan(raw []byte, member string) (start, end int, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, false
	}
	if d, isDelim := tok.(json.Delim); !isDelim || d != '{' {
		return 0, 0, false
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return 0, 0, false
		}
		name, isString := kt.(string)
		if !isString {
			return 0, 0, false
		}
		afterKey := int(dec.InputOffset())
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return 0, 0, false
		}
		afterValue := int(dec.InputOffset())
		if name != member {
			continue
		}
		colon := bytes.IndexByte(raw[afterKey:afterValue], ':')
		if colon < 0 {
			return 0, 0, false
		}
		vs := afterKey + colon + 1
		for vs < afterValue && (raw[vs] == ' ' || raw[vs] == '\t' || raw[vs] == '\n' || raw[vs] == '\r') {
			vs++
		}
		return vs, afterValue, true
	}
	return 0, 0, false
}

// poolLegacyOpenMember is the member the version-5 step inserts: the spelled
// name F37 asks for, written in the node rather than left to the absence of an
// owner.
const poolLegacyOpenMember = `"legacy_open": true`

// markLegacyOpenPools inserts poolLegacyOpenMember into every entry of a
// craft_pools array that names no owner, and reports how many it marked. The
// array's own text is returned with the insertions and nothing else: the
// entries keep their bytes, their order and their spacing, which is what makes
// this an edit of the owner's document rather than a re-encoding of it.
//
// An entry that names an owner is left alone, and so is one that already
// carries the marker: only the entries this step writes count, which is what
// `kept` and the diff's own sentence are read against.
func markLegacyOpenPools(arr []byte) ([]byte, int, error) {
	dec := json.NewDecoder(bytes.NewReader(arr))
	tok, err := dec.Token()
	if err != nil {
		return nil, 0, fmt.Errorf("projectpolicy: the version-4 node's craft_pools is not readable: %w", err)
	}
	if d, isDelim := tok.(json.Delim); !isDelim || d != '[' {
		return nil, 0, errors.New("projectpolicy: the version-4 node's craft_pools is not an array")
	}
	// The decoder's InputOffset names the end of the last token it read, so an
	// entry's bytes run from the end of the one before it: the separators —
	// the commas and the whitespace the owner's document was written with —
	// are re-emitted from that slice rather than computed, which is what keeps
	// the array in one piece.
	prev := int(dec.InputOffset())
	out := make([]byte, 0, len(arr))
	out = append(out, arr[:prev]...)
	marked := 0
	for dec.More() {
		var el json.RawMessage
		if err := dec.Decode(&el); err != nil {
			return nil, 0, fmt.Errorf("projectpolicy: the version-4 node's craft_pools is not readable: %w", err)
		}
		end := int(dec.InputOffset())
		chunk := arr[prev:end]
		lead := len(chunk) - len(bytes.TrimLeft(chunk, " \t\r\n,"))
		out = append(out, chunk[:lead]...)
		body := chunk[lead:]
		if !poolNamesOwner(body) {
			var written bool
			if body, written = withLegacyOpen(body); written {
				marked++
			}
		}
		out = append(out, body...)
		prev = end
	}
	out = append(out, arr[prev:]...)
	return out, marked, nil
}

// poolNamesOwner reports whether one craft_pools entry names an owner. An entry
// that is not a JSON object, or whose owner is not a string, is read as one
// that does NOT name an owner and is marked: Decode refuses the node either
// way, and the marker is the answer that keeps a pool with no readable owner
// from being served to everybody.
func poolNamesOwner(el []byte) bool {
	var pool map[string]json.RawMessage
	if err := json.Unmarshal(el, &pool); err != nil {
		return false
	}
	owner, ok := pool["owner"]
	if !ok || IsNull(owner) {
		return false
	}
	var s string
	return json.Unmarshal(owner, &s) == nil && s != ""
}

// withLegacyOpen returns one craft_pools entry with the marker written first in
// it, and whether it wrote one: an entry that already carries the member — the
// one a hand edit produces, since no version-4 release writes it — is returned
// as it is, and so is bytes that hold no object to write into, which the
// reader refuses either way. Writing first keeps the insert a splice of the
// entry's own bytes. The comma is there only when the entry holds something
// else: a pool written as {} is marked without trailing punctuation.
func withLegacyOpen(el []byte) ([]byte, bool) {
	var pool map[string]json.RawMessage
	if err := json.Unmarshal(el, &pool); err == nil {
		if _, marked := pool["legacy_open"]; marked {
			return el, false
		}
	}
	brace := bytes.IndexByte(el, '{')
	if brace < 0 {
		return el, false
	}
	rest := el[brace+1:]
	insert := []byte(poolLegacyOpenMember)
	if trimmed := bytes.TrimLeft(rest, " \t\r\n"); len(trimmed) > 0 && trimmed[0] != '}' {
		insert = append(insert, ',', ' ')
	}
	out := make([]byte, 0, len(el)+len(insert))
	out = append(out, el[:brace+1]...)
	out = append(out, insert...)
	out = append(out, rest...)
	return out, true
}

// bitbucketRepublishNote is the move that finishes the migration when the node
// carries a profile mounting the project's Bitbucket credential.
//
// Version 4 is the version that reads bitbucket_token, and that block is
// derived at publish from project_secrets.bitbucket-token.allow_data_to in the
// project's local config (DeriveBitbucketToken). A step whose only input is the
// node cannot derive it, and a profile mounting the token without the block is
// a node the current reader refuses — so the step leaves the block absent and
// says so, rather than writing a node nothing reads or taking the mount off a
// profile, which would change the policy.
//
// A node with no such profile gets no note, and that is a limit of what the
// node can say rather than a claim that the block is not needed: whether this
// project has a Bitbucket credential at all lives in the local config, which a
// migrator never reads. Such a node migrates to one without the block, and
// what fails closed on that is downstream — internal/gitproxy refuses a nil
// block — rather than here.
func bitbucketRepublishNote(doc map[string]json.RawMessage) string {
	praw, ok := doc["profiles"]
	if !ok || IsNull(praw) {
		return ""
	}
	var profiles map[string]struct {
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(praw, &profiles); err != nil {
		return ""
	}
	for _, name := range sortedStringKeys(profiles) {
		if !slices.Contains(profiles[name].Secrets, config.BitbucketSecret) {
			continue
		}
		return fmt.Sprintf("republish to derive the Bitbucket block: profiles.%s mounts %s, and bitbucket_token.allow_repos — the list that token is bounded by — is derived at publish from project_secrets.%s.allow_data_to in the project's local config, so the node alone cannot state it",
			name, config.BitbucketSecret, config.BitbucketSecret)
	}
	return ""
}

// MalformedError is the refusal of a node Migrate cannot read at all: one that
// is not a JSON object, carries no integer version, carries a version no
// release ever wrote, has a top-level key written twice, or is refused by the
// current reader after migration.
//
// Why is the class of the fault as a clause of the message, Note is the move a
// step reported that the operator still has to make (the republish, when the
// node's own profile mounts the Bitbucket credential), and Err is the refusal
// behind it when there is one — the strict reader's own, which names the field
// it refused. The note is here and not only in the Change list because the
// caller prints this error and nothing else: a refusal that names a field and
// no move sends the operator to the wrong file.
//
// None of the three carries the node's bytes: the message is what an operator
// reads, and a node is a document that may hold anything.
type MalformedError struct {
	Why  string
	Note string
	Err  error
}

func (e *MalformedError) Error() string {
	s := "projectpolicy: the policy node cannot be migrated: " + e.Why
	if e.Err != nil {
		s += ": " + strings.TrimPrefix(e.Err.Error(), "projectpolicy: ")
	}
	if e.Note != "" {
		s += " — " + e.Note
	}
	return s
}

// Unwrap is the refusal behind the class, for a caller that reports the
// reader's own sentence (the CLI's error handling) rather than this one.
func (e *MalformedError) Unwrap() error { return e.Err }

// migrateV5ToV6 writes the version field version 6 reads, and nothing else.
//
// Version 6 changed what a reader must enforce, not the node's shape: the
// `binaries` block is a new top-level key, and a version-5 node without one is a
// version-6 node that admits no binary, which is what every project that never
// set the key means. So the step edits the node's own bytes, and the entries
// the owner wrote by hand survive it exactly as the steps before it leave them.
//
// It carries no Note: the block is absent by default rather than required, so
// there is nothing for the operator to republish for. A project that wants a
// list publishes one, and never a half-written node the reader refuses.
func migrateV5ToV6(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-5 node is not a JSON object: %w", err)
	}
	out, err := writeVersionField(raw, 5, 6)
	if err != nil {
		return nil, Change{}, err
	}
	return out, Change{
		From:    5,
		To:      6,
		Changed: "the version field: 5 to 6",
		Kept:    keptKeys(doc, "version"),
	}, nil
}

// migrateV6ToV7 writes the version field version 7 reads, and nothing else.
//
// Version 7 changed what a reader must resolve, not the node's shape: the
// `model_grades` member is a new top-level key, and a version-6 node without one
// is a version-7 node that sets no grade — which is what every project that
// never set the key means, and what the ladder's rungs read as "leave this rung
// to the owner" (D2039). So the step edits the node's own bytes, and the entries
// the owner wrote by hand survive it exactly as the steps before it leave them.
//
// It carries no Note: the member is absent by default rather than required, so
// there is nothing for the operator to republish for. A project that wants its
// rungs on named models publishes them, and a grade nobody set refuses at the
// rung rather than being filled in with a model this step would have to invent.
func migrateV6ToV7(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-6 node is not a JSON object: %w", err)
	}
	out, err := writeVersionField(raw, 6, 7)
	if err != nil {
		return nil, Change{}, err
	}
	return out, Change{
		From:    6,
		To:      7,
		Changed: "the version field: 6 to 7",
		Kept:    keptKeys(doc, "version"),
	}, nil
}

// migrateV7ToV8 writes the version field version 8 reads, and nothing else.
//
// Version 8 does not change the node's shape either, and it adds no member of
// its own: the `binaries` block it is about arrived at version 6, and what this
// release adds is where the block is *written* — the project's own config, and
// the publish that carries it into the node. So a version-7 node is a
// version-8 node member for member, and the step edits the node's own bytes
// rather than re-encoding it, exactly as the two shape-free steps before it do.
//
// It carries no Note: there is nothing for the operator to republish for, and
// nothing to republish *with* that the node does not already hold — a project
// that wants an admission writes the key in its config and publishes, which is
// the publish door's work and not this step's.
func migrateV7ToV8(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-7 node is not a JSON object: %w", err)
	}
	out, err := writeVersionField(raw, 7, 8)
	if err != nil {
		return nil, Change{}, err
	}
	return out, Change{
		From:    7,
		To:      8,
		Changed: "the version field: 7 to 8",
		Kept:    keptKeys(doc, "version"),
	}, nil
}

// migrateV9ToV10 writes the version field version 10 reads, and nothing else.
//
// Version 10 adds one member to the node — `reports_commit`, the project's
// designated repository for committed reports and the folder inside it (D2751)
// — and the step is shape-free all the same, because a node that carries no
// such member designates no repository, which is exactly what a project that
// never named one means and what a report run reads as "commit nothing
// anywhere". That is version 6's and version 7's shape rather than version 9's,
// and it is what keeps this release's window from being a refusal: a version-9
// node is read as it stands by this release's readers, so `fugaro update`'s
// policy stage and the images stage beside it cannot leave a launcher reading a
// node it refuses.
//
// It carries no Note: there is nothing for the operator to republish for, and
// nothing to republish *with* that the node does not already hold — a project
// that wants a designation writes the block in its own config and publishes,
// which is the publish door's work and not this step's.
func migrateV9ToV10(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-9 node is not a JSON object: %w", err)
	}
	out, err := writeVersionField(raw, 9, 10)
	if err != nil {
		return nil, Change{}, err
	}
	return out, Change{
		From:    9,
		To:      10,
		Changed: "the version field: 9 to 10",
		Kept:    keptKeys(doc, "version"),
	}, nil
}

// migrateV10ToV11 writes the version field version 11 reads, and nothing else.
//
// Version 11 adds no member to the node: what it marks is the release an
// identity-issuing installation publishes (LM21, D3124) — the release in which
// the credentials service issues a run's identity instead of the launcher's own
// grant — and version.go's paragraph on it has the whole argument for why the
// bump is a lockstep and not an enforcement. The step is shape-free for that
// reason and for version 10's: a version-10 node is a version-11 node in
// everything but the number, so this release's readers read it as it stands
// and `fugaro update`'s own window — the policy stage behind the image builds
// — cannot leave a launcher reading a node it refuses.
//
// It carries no Note: there is nothing for the operator to republish for, and
// nothing to republish *with* that the node does not already hold.
func migrateV10ToV11(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-10 node is not a JSON object: %w", err)
	}
	out, err := writeVersionField(raw, 10, 11)
	if err != nil {
		return nil, Change{}, err
	}
	return out, Change{
		From:    10,
		To:      11,
		Changed: "the version field: 10 to 11",
		Kept:    keptKeys(doc, "version"),
	}, nil
}

// migrateV8ToV9 splits every craft_pools entry whose members span the two
// halves of a run into one entry per half, repoints the crafts whose access
// named such a pool, and writes the version field version 9 reads.
//
// Version 9 is the version at which a pool stays on one path (activation plan
// MC20, D1668): the credentials service selects among its members by tier and
// the listed order only breaks a tie, while a run's own gateway holds one
// upstream per stage and never retries a call across a secret, so a pool naming
// members of both halves is a pool whose order can mean nothing. From this
// release the read of a published node refuses one (config.ValidatePublishedPools,
// which Decode runs), and this step is the other half of that pair: a node an
// earlier release published, or one an administrator edited by hand, carries a
// crossing pool that no validation may repair and no config-side migration can
// reach, so the chain converges it in place, the way `fugaro update` converges
// the installation's own config (internal/cli's derivePathPools).
//
// The split is internal/craft's one rule (craft.SplitCrossPathPools) and not a
// second copy of it: the halves are the node's own craft_credentials types
// through config.CredentialServingPath, the order is the pool's own member
// order, and the craft's half is its model — the node's crafts block, with the
// inheritance config.CraftParent applies — resolved through the node's own
// routing switch and providers block. A step may read nothing but the node, and
// this one does not: a rung the node does not carry (the installation's
// agent.models.<role>) is not its to read, and where the node resolves nothing
// the craft keeps the half the pool's first member is on, which is what the
// config side answers for the same craft.
//
// The edit is confined to the two blocks it rewrites: every other member comes
// back byte for byte (Kept), and so does every pool entry and every craft the
// step does not touch — the entries keep their bytes, their order and their
// spacing, because this is an edit of the owner's document and not a
// re-encoding of it. A node with no crossing pool changes only its number.
//
// It carries no Note: the move it makes is the whole of the move, there is
// nothing for the operator to republish for, and the split it writes is a node
// the current reader reads.
func migrateV8ToV9(raw json.RawMessage) (json.RawMessage, Change, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, Change{}, fmt.Errorf("projectpolicy: the version-8 node is not a JSON object: %w", err)
	}
	out, err := writeVersionField(raw, 8, 9)
	if err != nil {
		return nil, Change{}, err
	}
	changed := "the version field: 8 to 9"
	kept := keptKeys(doc, "version")

	blocks, err := readNodeCraftBlocks(doc)
	if err != nil {
		return nil, Change{}, err
	}
	_, access, splits := craft.SplitCrossPathPools(blocks.pools, blocks.creds, blocks.crafts, blocks.routing, blocks.providers)
	if len(splits) == 0 {
		return out, Change{From: 8, To: 9, Changed: changed, Kept: kept}, nil
	}

	start, end, ok := memberValueSpan(out, "craft_pools")
	if !ok {
		return nil, Change{}, errors.New("projectpolicy: the version-8 node's craft_pools field could not be located")
	}
	rewritten, err := spliceCrossedPools(out[start:end], splits)
	if err != nil {
		return nil, Change{}, err
	}
	out = spliceMember(out, start, end, rewritten)
	changed = fmt.Sprintf("the version field: 8 to 9, and craft_pools: %s", splitSentence(splits))
	kept = keptKeys(doc, "version", "craft_pools")

	if len(access) > 0 {
		cs, ce, ok := memberValueSpan(out, "crafts")
		if !ok {
			return nil, Change{}, errors.New("projectpolicy: the version-8 node's crafts field could not be located")
		}
		repointed, wrote, err := repointCraftAccesses(out[cs:ce], access)
		if err != nil {
			return nil, Change{}, err
		}
		if wrote > 0 {
			out = spliceMember(out, cs, ce, repointed)
			changed += fmt.Sprintf(", and the access of %s, which names the half its own model routes to", countCrafts(wrote))
			kept = keptKeys(doc, "version", "craft_pools", "crafts")
		}
	}
	return out, Change{From: 8, To: 9, Changed: changed, Kept: kept}, nil
}

// nodeCraftBlocks are the members of a version-8 node the split reads: the
// credentials a pool's members are typed by, the pools, the crafts whose access
// may name one, and the routing switch and providers block a craft's model
// resolves through. A member the node does not carry is left empty, which is
// what the reader above it answers for one too.
type nodeCraftBlocks struct {
	creds     []config.Credential
	pools     []config.Pool
	crafts    config.Crafts
	routing   *config.Routing
	providers map[string]config.ModelProvider
}

// readNodeCraftBlocks reads those members off the node, and refuses a member
// that is not the block this version reads: the node is then one no step can
// converge, and the class of the fault is MalformedError's, exactly as it is for
// a node that is not an object at all.
//
// A member it cannot read is refused rather than skipped, because skipping it
// would leave a crossing pool in the node this step hands to Decode — which
// refuses it a moment later with a sentence about the pool rather than about the
// block that could not be read.
func readNodeCraftBlocks(doc map[string]json.RawMessage) (nodeCraftBlocks, error) {
	var b nodeCraftBlocks
	if raw, ok := doc["craft_credentials"]; ok && !IsNull(raw) {
		if err := json.Unmarshal(raw, &b.creds); err != nil {
			return nodeCraftBlocks{}, &MalformedError{Why: "its craft_credentials block is not the block this version reads", Err: err}
		}
	}
	if raw, ok := doc["craft_pools"]; ok && !IsNull(raw) {
		if err := json.Unmarshal(raw, &b.pools); err != nil {
			return nodeCraftBlocks{}, &MalformedError{Why: "its craft_pools block is not the block this version reads", Err: err}
		}
	}
	if raw, ok := doc["crafts"]; ok && !IsNull(raw) {
		if err := json.Unmarshal(raw, &b.crafts); err != nil {
			return nodeCraftBlocks{}, &MalformedError{Why: "its crafts block is not the block this version reads", Err: err}
		}
	}
	if raw, ok := doc["routing"]; ok && !IsNull(raw) {
		if err := json.Unmarshal(raw, &b.routing); err != nil {
			return nodeCraftBlocks{}, &MalformedError{Why: "its routing block is not the block this version reads", Err: err}
		}
	}
	if raw, ok := doc["providers"]; ok && !IsNull(raw) {
		var ps map[string]Provider
		if err := json.Unmarshal(raw, &ps); err != nil {
			return nodeCraftBlocks{}, &MalformedError{Why: "its providers block is not the block this version reads", Err: err}
		}
		b.providers = make(map[string]config.ModelProvider, len(ps))
		for name, p := range ps {
			b.providers[name] = p.ModelProvider
		}
	}
	return b, nil
}

// spliceCrossedPools returns the craft_pools array with every entry the split
// rewrote replaced by the pools that replace it, and nothing else: an entry the
// step leaves alone keeps its own bytes and the array keeps its own separators
// and spacing, which is what makes this an edit of the owner's document rather
// than a re-encoding of it. The halves are written as new objects — they are
// entries no release wrote, and there are no bytes of the owner's to keep.
func spliceCrossedPools(arr []byte, splits []craft.PoolSplit) ([]byte, error) {
	byName := make(map[string]craft.PoolSplit, len(splits))
	for _, s := range splits {
		byName[s.Pool.Name] = s
	}
	dec := json.NewDecoder(bytes.NewReader(arr))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: the version-8 node's craft_pools is not readable: %w", err)
	}
	if d, isDelim := tok.(json.Delim); !isDelim || d != '[' {
		return nil, errors.New("projectpolicy: the version-8 node's craft_pools is not an array")
	}
	prev := int(dec.InputOffset())
	out := make([]byte, 0, len(arr))
	out = append(out, arr[:prev]...)
	for dec.More() {
		var el json.RawMessage
		if err := dec.Decode(&el); err != nil {
			return nil, fmt.Errorf("projectpolicy: the version-8 node's craft_pools is not readable: %w", err)
		}
		end := int(dec.InputOffset())
		chunk := arr[prev:end]
		lead := len(chunk) - len(bytes.TrimLeft(chunk, " \t\r\n,"))
		out = append(out, chunk[:lead]...)
		body := chunk[lead:]
		s, crossed := byName[poolEntryName(body)]
		if !crossed {
			out = append(out, body...)
			prev = end
			continue
		}
		for i, path := range s.Paths() {
			if i > 0 {
				out = append(out, ',')
			}
			half, err := json.Marshal(s.Halves[path])
			if err != nil {
				return nil, fmt.Errorf("projectpolicy: a pool the split derives cannot be written: %w", err)
			}
			out = append(out, half...)
		}
		prev = end
	}
	out = append(out, arr[prev:]...)
	return out, nil
}

// poolEntryName is one craft_pools entry's name, "" for an entry that names
// none. A name is not re-spelled on the way out — the entry the step does not
// rewrite keeps its own bytes — so this reads the name to find the entry and
// writes nothing with it.
func poolEntryName(el []byte) string {
	var pool struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(el, &pool); err != nil {
		return ""
	}
	return pool.Name
}

// repointCraftAccesses returns the crafts object with the access of every craft
// the split repointed written to the name of its half, and reports how many it
// wrote. Everything else in the object is the owner's own bytes: the craft's key,
// the colon after it and the members beside `access` are re-emitted from the
// document rather than re-encoded.
func repointCraftAccesses(obj []byte, access map[string]string) ([]byte, int, error) {
	dec := json.NewDecoder(bytes.NewReader(obj))
	tok, err := dec.Token()
	if err != nil {
		return nil, 0, fmt.Errorf("projectpolicy: the version-8 node's crafts is not readable: %w", err)
	}
	if d, isDelim := tok.(json.Delim); !isDelim || d != '{' {
		return nil, 0, errors.New("projectpolicy: the version-8 node's crafts is not an object")
	}
	prev := int(dec.InputOffset())
	out := make([]byte, 0, len(obj))
	out = append(out, obj[:prev]...)
	written := 0
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, 0, fmt.Errorf("projectpolicy: the version-8 node's crafts is not readable: %w", err)
		}
		name, isString := kt.(string)
		if !isString {
			return nil, 0, errors.New("projectpolicy: the version-8 node's crafts has a member name that is not a string")
		}
		afterKey := int(dec.InputOffset())
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, 0, fmt.Errorf("projectpolicy: the version-8 node's crafts is not readable: %w", err)
		}
		afterValue := int(dec.InputOffset())
		to, repoint := access[name]
		if !repoint {
			out = append(out, obj[prev:afterValue]...)
			prev = afterValue
			continue
		}
		// The value alone is written: the key, the colon and whatever the owner
		// wrote around them are the document's own bytes.
		colon := bytes.IndexByte(obj[afterKey:afterValue], ':')
		if colon < 0 {
			return nil, 0, fmt.Errorf("projectpolicy: the version-8 node's crafts.%s has no access to repoint", pluginwire.Printable(name))
		}
		out = append(out, obj[prev:afterKey+colon+1]...)
		next, wrote, err := replaceMemberString(v, "access", to)
		if err != nil {
			return nil, 0, err
		}
		if !wrote {
			return nil, 0, fmt.Errorf("projectpolicy: the version-8 node's crafts.%s names the pool %s and carries no access string to repoint", pluginwire.Printable(name), pluginwire.Printable(access[name]))
		}
		out = append(out, next...)
		written++
		prev = afterValue
	}
	out = append(out, obj[prev:]...)
	return out, written, nil
}

// replaceMemberString returns obj with the value of one of its members written
// as the JSON string value, and reports whether it wrote anything. A member that
// is not there, or whose value is not a string, is left as it is: the caller's
// own rule decided what the value has to be, and this only edits bytes.
func replaceMemberString(obj []byte, member, value string) ([]byte, bool, error) {
	start, end, ok := memberValueSpan(obj, member)
	if !ok {
		return obj, false, nil
	}
	var cur string
	if err := json.Unmarshal(obj[start:end], &cur); err != nil {
		return obj, false, nil
	}
	if cur == value {
		return obj, false, nil
	}
	out := make([]byte, 0, len(obj)+len(value))
	out = append(out, obj[:start]...)
	out = append(out, strconv.Quote(value)...)
	out = append(out, obj[end:]...)
	return out, true, nil
}

// spliceMember returns raw with the byte span of one of its members replaced.
// The span comes from memberValueSpan over raw itself, so the member's name, its
// colon and its neighbours are the document's bytes and only the value changes.
func spliceMember(raw []byte, start, end int, repl []byte) json.RawMessage {
	out := make(json.RawMessage, 0, len(raw)-(end-start)+len(repl))
	out = append(out, raw[:start]...)
	out = append(out, repl...)
	out = append(out, raw[end:]...)
	return out
}

// splitSentence words one step's craft_pools edit for the diff the operator
// reads: the pool that crossed and the two that replace it, by name.
func splitSentence(splits []craft.PoolSplit) string {
	parts := make([]string, 0, len(splits))
	for _, s := range splits {
		names := make([]string, 0, len(s.Halves))
		for _, path := range s.Paths() {
			names = append(names, pluginwire.Printable(s.Halves[path].Name))
		}
		parts = append(parts, fmt.Sprintf("%s split into %s", pluginwire.Printable(s.Pool.Name), strings.Join(names, " and ")))
	}
	if len(parts) == 1 {
		return "the pool " + parts[0]
	}
	return "the pools " + strings.Join(parts, "; ")
}

// countCrafts words the number of crafts a step repointed, so the diff reads as
// a sentence about the owner's node rather than as a count of entries.
func countCrafts(n int) string {
	if n == 1 {
		return "1 craft"
	}
	return strconv.Itoa(n) + " crafts"
}
