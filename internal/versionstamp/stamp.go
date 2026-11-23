// Package versionstamp is the required-CLI stamp: the node an installation
// publishes to say the oldest fugaro release that may act on it, the strict
// reader and writer of that node, and the refusal a binary below it prints.
//
// **What a writer may rely on.** `Encode` writes the three members the node
// carries — `schema`, `min` and `set_at` — in one canonical form, and it is the
// only bytes a writer puts on the node: `min` is written without a leading `v`,
// and `set_at` in UTC. `Decode` reads those bytes back as the same Stamp.
// `schemas/required-cli.schema.json` is the same shape spelled for an editor, and
// a test holds it to `Decode` in both directions.
//
// **What a reader may rely on.** `Decode` is strict: a key this code does not
// model, a `schema` it does not read, a `min` that `updatecheck.Valid` rejects
// and a `set_at` that is not an RFC 3339 instant are each a `*MalformedError`
// that names the node and the one thing that repairs it. A node that is not
// there at all is not a refusal: `found` is false.
//
// **What the stamp is not.** It is a fence and not a boundary (spec: IAM is the
// boundary), it never falls (D3262) and nothing but `fugaro update` writes it.
// A command that reads it through `Source` fails open on every read failure
// except two: a stamp that is present and malformed, and a cached value above
// the running binary that is younger than `CacheTTL` (D3264, bounded by D3268).
// The refusal itself is `BelowError`, whose sentence is the one the spec pins,
// with `updatecheck.BrewCommand` as the fix.
//
// The comparison and the version shapes are `updatecheck`'s (`Valid`, `Newer`,
// `CanonicalRelease`, `BrewCommand`); this package writes no semver code of its
// own, so a stamp and a release are compared the one way the rest of the CLI
// compares them.
package versionstamp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// Path is the node the stamp lives at, relative to the project's database root
// and without a leading slash. It sits beside projectpolicy.PathShared, and the
// rules template grants nothing on config/policy, so no token reads or writes it:
// admins write it over IAM and every reader holds roles/firebasedatabase.viewer.
const Path = "config/policy/required_cli"

// SchemaVersion is the version of the stamp this fugaro writes and reads. A
// reader refuses a version it does not know rather than reading what it
// recognises: a stamp a later release wrote may mean something this binary would
// read as its own, and the whole point of the node is that a binary below it
// stops. A member of the node added in a later release bumps this, and a
// *published* release is the only writer (D3269), so a version this code does not
// know means an upgrade.
const SchemaVersion = 1

// Stamp is the required-CLI stamp decoded: the oldest fugaro release that may
// act on the project, and when this installation's own `fugaro update` wrote it.
type Stamp struct {
	// Schema is the node's version: SchemaVersion for every stamp Decode reads.
	Schema int
	// Min is the oldest release that may act on the project, plain X.Y.Z and
	// never with a `v` prefix once Encode has written it (A3). A CLI below it is
	// refused by Below.
	Min string
	// SetAt is when the node was written, for the refusal sentence's date. It is
	// always present: a stamp without one is a hand edit and is refused.
	SetAt time.Time
}

// stampWire is the stamp as the node carries it and the one spelling Encode
// writes. The member order here is the order the bytes come out in, which is
// what the write's ETag covers: a writer that spelled one stamp two ways would
// see every update as a change.
type stampWire struct {
	Schema int       `json:"schema"`
	Min    string    `json:"min"`
	SetAt  time.Time `json:"set_at"`
}

// Encode writes s as the bytes a writer puts on the node: `min` canonicalised
// to X.Y.Z without a `v` (updatecheck.CanonicalRelease, so a `v` prefix a caller
// carries is dropped rather than refused), `set_at` in UTC. A schema outside
// SchemaVersion or a `min` that is not a release version is refused here rather
// than published: the node is read by every later release, and the writer is the
// only side that can still say no.
func Encode(s Stamp) ([]byte, error) {
	if s.Schema != SchemaVersion {
		return nil, fmt.Errorf("versionstamp: schema %d is not the version this fugaro writes (%d)", s.Schema, SchemaVersion)
	}
	min, ok := updatecheck.CanonicalRelease(s.Min)
	if !ok {
		return nil, fmt.Errorf("versionstamp: %q is not a release version (X.Y.Z)", s.Min)
	}
	if s.SetAt.IsZero() {
		return nil, errors.New("versionstamp: the stamp carries no set_at time")
	}
	out, err := json.Marshal(stampWire{Schema: s.Schema, Min: min, SetAt: s.SetAt.UTC()})
	if err != nil {
		return nil, fmt.Errorf("versionstamp: encoding the stamp: %w", err)
	}
	return out, nil
}

// The two repairs a malformed stamp has, and the only two things that can have
// produced one.
const (
	// fixUpgrade is the repair for an unknown schema: only a published release
	// writes the node (D3269), so a version this code does not read was written
	// by a newer one and the answer is to move this binary forward.
	fixUpgrade = "it was written by a newer fugaro, so upgrade with " + updatecheck.BrewCommand

	// fixHandEdit is the repair for everything else: no release writes an
	// unknown key, a `min` that is not a version or a stamp without a `set_at`,
	// so the node was edited by hand, and D3262's no-lowering rule leaves a hand
	// edit in the Firebase console as the one recovery there is (T7).
	fixHandEdit = "it is a hand edit, so correct " + Path + " in the Firebase console"
)

// MalformedError is the refusal of a stamp this fugaro cannot read. It is the
// one case where a read fails closed: an absent node means no fence, but a node
// that is there and unreadable is a node somebody meant, and going on past it
// would act on a project whose fence this binary cannot see.
type MalformedError struct {
	// Node is the damaged node's path, so the sentence names where to look.
	Node string
	// reason is what is wrong with it and fix is the one thing that repairs it,
	// chosen by what the fault implies (fixUpgrade or fixHandEdit).
	reason, fix string
}

func (e *MalformedError) Error() string {
	return fmt.Sprintf("%s holds a stamp this fugaro cannot read: %s; %s", e.Node, e.reason, e.fix)
}

// malformed is the one place a MalformedError is built, so no refusal forgets
// the node or the fix.
func malformed(reason, fix string) *MalformedError {
	return &MalformedError{Node: Path, reason: reason, fix: fix}
}

// Decode reads the stamp a node carries. found is false for a node that is not
// there at all (null, or nothing), which is a project with no fence and not a
// refusal.
//
// It decodes strictly, in the order that names the cause: the `schema` is read
// first and under its exact spelling (schemaMember), so a node a later release
// wrote is refused as a version this code does not know — its own fix is the
// upgrade — while a schema spelled any other way is a hand edit; then the
// members are walked, so an unknown or twice-written key is refused (the typed
// decode below cannot say either, because encoding/json matches
// case-insensitively and lets the last spelling win); then the typed decode and
// the checks on `min` and `set_at`. `min` is returned canonical
// (updatecheck.CanonicalRelease), so a `v` a hand edit added is dropped and one
// stamp has one value, whatever spelling the node carries.
func Decode(raw []byte) (Stamp, bool, error) {
	if projectpolicy.IsNull(raw) {
		return Stamp{}, false, nil
	}
	rawSchema, hasSchema, err := schemaMember(raw)
	if err != nil {
		return Stamp{}, true, err
	}
	if !hasSchema {
		return Stamp{}, true, malformed("it carries no schema member", fixHandEdit)
	}
	var schema int
	if err := json.Unmarshal(rawSchema, &schema); err != nil || projectpolicy.IsNull(rawSchema) {
		return Stamp{}, true, malformed("its schema is not a version number", fixHandEdit)
	}
	if schema != SchemaVersion {
		return Stamp{}, true, malformed(fmt.Sprintf("its schema is %d and this fugaro reads %d", schema, SchemaVersion), fixUpgrade)
	}
	if err := refuseOtherSpellings(raw); err != nil {
		return Stamp{}, true, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var w stampWire
	if err := dec.Decode(&w); err != nil {
		return Stamp{}, true, malformed("it could not be read: "+err.Error(), fixHandEdit)
	}
	min, ok := updatecheck.CanonicalRelease(w.Min)
	if !ok {
		return Stamp{}, true, malformed(fmt.Sprintf("its min is %q, which is not a release version (X.Y.Z)", w.Min), fixHandEdit)
	}
	if w.SetAt.IsZero() {
		return Stamp{}, true, malformed("it carries no set_at instant", fixHandEdit)
	}
	return Stamp{Schema: w.Schema, Min: min, SetAt: w.SetAt}, true, nil
}

// schemaMember returns the raw `schema` member, found only under that exact
// spelling. It exists because the choice between the two repairs turns on who
// wrote the node: an unknown schema means a newer release wrote it (fixUpgrade)
// and everything else means a hand edit (fixHandEdit). The typed decode cannot
// answer that — encoding/json matches a key case-insensitively and lets the
// later of two spellings win — so {"SCHEMA":2}, {"Schema":2} and a node with
// `schema` twice would all be read as 2 and sent to the upgrade repair, when no
// release writes any of those spellings and the console is what repairs them.
//
// Members other than `schema` are left to refuseOtherSpellings below, which runs
// once the version is known: a genuine stamp from a newer release may carry a
// member this binary does not know, and that node's fix is still the upgrade.
func schemaMember(raw []byte) (json.RawMessage, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, false, malformed("it could not be read: "+err.Error(), fixHandEdit)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, false, malformed("it is not one JSON object", fixHandEdit)
	}
	var schema json.RawMessage
	seen := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false, malformed("it could not be read: "+err.Error(), fixHandEdit)
		}
		name, ok := tok.(string)
		if !ok {
			return nil, false, malformed("a member name is not a string", fixHandEdit)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false, malformed("it could not be read: "+err.Error(), fixHandEdit)
		}
		switch {
		case name == "schema" && seen:
			return nil, false, malformed(`it carries the member "schema" twice: a stamp writes each of its members once`, fixHandEdit)
		case name == "schema":
			schema, seen = value, true
		case strings.EqualFold(name, "schema"):
			return nil, false, malformed(fmt.Sprintf("it carries the unknown member %q: the stamp's members are schema, min and set_at, spelled exactly", name), fixHandEdit)
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, false, malformed("it could not be read: "+err.Error(), fixHandEdit)
	}
	return schema, seen, nil
}

// refuseOtherSpellings walks the node's object and refuses a member this
// contract does not name, spelled exactly, written once. It is the half of
// strictness the typed decode cannot have — internal/creds' credential-route
// record and internal/access' grants node walk their own documents the same way
// for the same reason.
//
// encoding/json matches an object key to a struct field case-insensitively and
// lets the later of two spellings of a key win, so {"schema":1,"MIN":"9.9.9"}
// would raise the fence behind DisallowUnknownFields' back, and a Realtime
// Database node holds the two spellings as two children. A stamp that spells one
// of its members any other way is one this reader cannot vouch for, and a member
// nobody wrote in this contract must not move the fence.
func refuseOtherSpellings(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return malformed("it could not be read: "+err.Error(), fixHandEdit)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return malformed("it is not one JSON object", fixHandEdit)
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return malformed("it could not be read: "+err.Error(), fixHandEdit)
		}
		name, ok := tok.(string)
		if !ok {
			return malformed("a member name is not a string", fixHandEdit)
		}
		switch name {
		case "schema", "min", "set_at":
		default:
			return malformed(fmt.Sprintf("it carries the unknown member %q: the stamp's members are schema, min and set_at, spelled exactly", name), fixHandEdit)
		}
		if seen[name] {
			return malformed(fmt.Sprintf("it carries the member %q twice: a stamp writes each of its members once", name), fixHandEdit)
		}
		seen[name] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return malformed("it could not be read: "+err.Error(), fixHandEdit)
		}
	}
	if _, err := dec.Token(); err != nil {
		return malformed("it could not be read: "+err.Error(), fixHandEdit)
	}
	return nil
}

// Below reports whether version is below the stamp: the running binary is older
// than the oldest release that may act on the project, and a command must refuse
// with BelowError.
//
// It is updatecheck.Newer and nothing else, so the two answers the CLI gives
// about versions cannot drift. A version that is not a plain X.Y.Z — a `dev`
// build, a pre-release tag — is never below anything: such a binary has nothing
// to compare with and skips the fence (spec, "Dev builds").
func Below(stamp Stamp, version string) bool {
	return updatecheck.Newer(stamp.Min, version)
}

// BelowError is the refusal of a command whose project requires a newer CLI. It
// takes the ordinary refusal path — PrintFailure's sentence on stderr, exit code
// ExitUserError — and there is no flag, no environment switch and no prompt that
// overrides it (A5).
type BelowError struct {
	// Stamp is the project's stamp: Min is the release required and SetAt is
	// when the installation's own update wrote it.
	Stamp Stamp
	// Version is the running binary's version.
	Version string
}

// Error is the spec's one refusal sentence. Every word of it is fixed: the
// required release and the date come from the node, the running version from the
// binary, and the fix is updatecheck.BrewCommand, the same command CLISkew
// names.
func (e *BelowError) Error() string {
	return fmt.Sprintf(
		"this project requires fugaro %s or newer (set by fugaro update on %s) and this is %s, so nothing was done: upgrade with %s, then run fugaro update",
		e.Stamp.Min, e.Stamp.SetAt.UTC().Format("2006-01-02"), e.Version, updatecheck.BrewCommand)
}
