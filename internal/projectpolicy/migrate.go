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
	3: migrateV3ToV4,
	4: migrateV4ToV5,
}

// tooOld names the versions this Fugaro reads but has no migrator for. The
// refusal is not a sentence of this file's: a version below Version is a node
// older than its reader, which is one direction of the skew version.go's
// CLISkewNewer already words, so Migrate answers with the *VersionError Decode
// returns and the caller prints the remedy that fits its side (SkewSentence)
// rather than a second wording of the same move that could drift from it.
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
//   - a node already at Version comes back byte for byte, with no changes. It
//     is not judged here: whether it is a policy this Fugaro can use is
//     Decode's answer, and Migrate's is only whether it can be moved forward;
//   - a node written in another version is refused with a *VersionError, the
//     value Decode returns and SkewSentence words, in both directions: Found
//     above Understood is a node ahead of this Fugaro (upgrade it), and Found
//     below it is one behind — the direction CLISkewNewer words, whose remedy
//     is `fugaro project policy publish --delete` and a fresh publish. A
//     version below the chain's first step is that second case, and carries no
//     type of its own so a caller that already branches on *VersionError for
//     the older direction recognises it;
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
// The last step is followed by the reader's own judgement: the migrated bytes
// must decode with Decode, or the migration is refused. That is what holds a
// step to the shape the current reader reads rather than to the shape its
// author believed in, and it is how a version-3 node whose profile mounts
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
		return raw, nil, nil
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
	if _, found, err := Decode(cur); err != nil || !found {
		if err == nil {
			err = errors.New("the node is empty")
		}
		var note string
		for _, ch := range changes {
			if ch.Note != "" {
				note = ch.Note
			}
		}
		return raw, changes, &MalformedError{Why: "the migrated node is one the current reader refuses", Note: note, Err: err}
	}
	return cur, changes, nil
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
