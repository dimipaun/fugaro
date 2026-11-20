package workspace

// The roster node: `workspaceRosters/<name>` in the workspace's own database, a
// SIBLING of the published record and never under it (R5). The place matters and
// was found in review: `fugaro update`'s publish stage rewrites
// `workspaces/<name>` whole under one ETag and refuses a node carrying keys it
// does not know (`internal/cli/update_workspace.go`, `node.UnknownKeys`), so a
// roster inside the record would stop the update of every CLI that predates it —
// the exact failure the HARD RULE forbids. A node of its own carries its own
// ETag and nobody else's.
//
// The rules need no change for it (R7): the workspace rules' root denies every
// client (`internal/budget/rules/workspace.json.tmpl`), so a location the
// document does not name is denied by the root itself, and the roster is reached
// by IAM alone — the workspace's own service, its admins and the workspace
// owner's CLI, which is who D2817 says writes it.
//
// Every write here is CONDITIONAL, and that is the whole reason the node has an
// ETag. A read answers the raw document beside the ETag it was read at; a write
// lands only while the node still carries that ETag; and a node another writer
// moved under this one is rtdb.ErrPrecondition, which the caller answers by
// reading again and judging against the bytes that are really there. Nothing is
// written on that path, so two admins editing the roster cannot lose each
// other's work — the property D2817 rests on ("two admins never overwrite each
// other"). An ABSENT node is the first write and not an error: its read answers
// the null ETag, and a write under that creates the node, which is why no
// `fugaro update` stage seeds one (R7).
//
// The roster document's own shape is C1's (`internal/workspace/roster.go` and
// schemas/workspace-roster.schema.json): this file moves bytes and ETags, and
// reads no field of them.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// pathWorkspaceRosters is the parent of the roster nodes, beside the published
// records and not inside them (R5).
const pathWorkspaceRosters = "workspaceRosters"

// RosterNode is the roster node as the database holds it: the raw JSON document
// and the ETag a conditional write is made under.
//
// Raw is empty for a node that is not there — which Read reports as not found,
// because an absent roster is a workspace whose first write has not happened and
// not an empty one — and ETag is the null ETag then, which is the precondition
// that CREATES the node. A node to write under comes from Read and from nowhere
// else: an ETag this package did not read is a precondition on a document nobody
// saw, and rtdb.PutIfMatch refuses the empty one rather than treating it as
// "write unconditionally".
type RosterNode struct {
	Raw  json.RawMessage
	ETag string
}

// RosterStore reads and writes one workspace's roster node through db. It holds
// the client and the name and nothing else: the roster itself lives in the
// database, under an ETag, so two stores of one workspace are interchangeable
// and neither is a cache another writer can fall behind.
type RosterStore struct {
	db   *rtdb.Client
	name string
}

// NewRosterStore returns a store of name's roster node in db.
//
// The name is held to the project-name grammar here (config.ProjectNameRE),
// which is the grammar the record's own validation holds it to
// (workspace.Record.Validate): a workspace name IS a project name (F21), so it
// is already a legal database key and the path needs no escaping. A name that is
// not one is refused before any path exists, rather than escaped into a
// different node.
func NewRosterStore(db *rtdb.Client, name string) (*RosterStore, error) {
	if db == nil {
		return nil, errors.New("workspace: a roster store needs a database client: the roster node lives in the workspace's own database")
	}
	if !config.ProjectNameRE.MatchString(name) {
		return nil, fmt.Errorf("workspace: %q is not a project name, so it is not a workspace name and has no roster node", name)
	}
	return &RosterStore{db: db, name: name}, nil
}

// Name is the workspace whose roster this store reads and writes.
func (s *RosterStore) Name() string { return s.name }

// Path is the roster node's path, relative to the workspace's database root.
func (s *RosterStore) Path() string { return pathWorkspaceRosters + "/" + s.name }

// Read reads the roster with the ETag a conditional write is made under. found
// is false for a workspace whose roster has never been written: that is the
// state the first write creates, and not an error.
//
// A read that fails — denied, unreachable, a body that is not JSON — is an
// error, and a caller must refuse on it rather than write. An unreadable node
// has no ETag to judge a change against, and a write under a guessed one is
// exactly the overwrite this node exists to prevent; the same reading this
// package's access roster takes of a failed read (internal/access/store.go).
func (s *RosterStore) Read(ctx context.Context) (RosterNode, bool, error) {
	var raw json.RawMessage
	etag, found, err := s.db.GetETag(ctx, s.Path(), &raw)
	if err != nil {
		return RosterNode{}, false, fmt.Errorf("reading %s: %w", s.Path(), err)
	}
	if !found {
		return RosterNode{ETag: etag}, false, nil
	}
	return RosterNode{Raw: raw, ETag: etag}, true, nil
}

// Write writes raw at the roster node, conditional on node.ETag: it lands only
// while the node still carries that ETag, and a node that moved under this
// writer is rtdb.ErrPrecondition with nothing written. That refusal is the
// answer, not a failure: the caller reads the node again (Read) and judges
// against what is really there, so the change it never saw survives and this
// writer's is what is remade.
//
// An absent node is the create: Read answers it with the null ETag, and a write
// under that lands only while the node is still absent, so two first writers
// cannot both create one over the other.
//
// raw is the roster document and must be a JSON object: the node holds a
// document, and a scalar or an array under it is a shape C1's reader would
// refuse at the next read, after the write had already replaced a roster.
//
// It must also carry a value. The database prunes a null and an empty object on
// the way in, so a document that prunes away entirely is not an empty roster but
// a DELETE of the node, in the real database and in the fake alike
// (`internal/gcpfake/rtdb.go`, normalise). A `{}` or an all-null roster would
// therefore remove every admin, member and grant at once and leave Read
// answering found=false — a workspace that looks like one whose first write
// never happened, and whose next create then lands under the null ETag with no
// roster left to judge it against. The refusal is the answer: a caller with no
// roster to write has a decision to make (which member, which grant), not a
// document to send.
func (s *RosterStore) Write(ctx context.Context, node RosterNode, raw []byte) error {
	doc := bytes.TrimSpace(raw)
	if len(doc) == 0 || doc[0] != '{' || !json.Valid(doc) {
		return fmt.Errorf("writing %s: the roster is not a JSON object", s.Path())
	}
	if !rosterCarriesValue(doc) {
		return fmt.Errorf("writing %s: the roster carries no value, so the write would delete the node", s.Path())
	}
	if err := s.db.PutIfMatch(ctx, s.Path(), node.ETag, json.RawMessage(doc)); err != nil {
		return fmt.Errorf("writing %s: %w", s.Path(), err)
	}
	return nil
}

// rosterCarriesValue reports whether anything in doc survives the database's own
// pruning — a null and an empty object go, as they do in the real database
// (`internal/gcpfake/rtdb.go`, normalise) — and so whether the PUT writes
// anything at all. It walks the document the way the pruning does, which is why
// the check is deeper than the top level: {"version":{"members":null}} prunes to
// {} and deletes the node exactly as {} does.
//
// doc must be a JSON object already validated by Write.
func rosterCarriesValue(doc []byte) bool {
	var v any
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return false
	}
	return carriesValue(v)
}

func carriesValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case map[string]any:
		for _, c := range x {
			if carriesValue(c) {
				return true
			}
		}
		return false
	default:
		return true
	}
}
