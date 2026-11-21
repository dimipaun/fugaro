package projectpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
)

// The workspace credentials node's migration, version 1 to version 2: the
// mirror of the workspace record's chain (workspace.go) for the other document
// the workspace owner's `fugaro update` publishes. The two documents share
// their shape and nothing else — different readers in different databases, and
// a version of one says nothing about the other — so the chain, its change
// record and its refusals are written here rather than reused, and a caller
// that renders a diff reads this type rather than the record's.
//
// The node's own writer (internal/cli/update_workspace.go) publishes version 2
// and needs no step; what the steps are for is the node an owner already has:
// the HARD RULE is that `fugaro update` is the whole upgrade, so a release that
// bumps this version must carry the older document forward in place rather than
// asking the owner to publish again (the reconciliation's D1292 refused a
// `fugaro workspace publish` on exactly that ground).
//
// **There is one step and there is no version 0.** The record's chain starts at
// 0 because version 0 is a state with a meaning there — the file shape
// schemas/workspace.schema.json describes, which a person can have published by
// hand. This node has no such state: its path is in a database only the
// workspace's own installation can write, the only thing that ever wrote it is
// the stage below, and every release that wrote it wrote the version key. A
// node that carries no version, or one below 1, is therefore a hand edit, and
// it is refused rather than migrated on a step nobody wrote.

// WorkspaceCredentialsChange is one step of the credentials node's migration,
// in the words a caller renders as the diff: the version the step moves the
// node off, the one it writes, what it changes, and the values it keeps as they
// are.
type WorkspaceCredentialsChange struct {
	From    int
	To      int
	Changed string
	Kept    []string
}

// workspaceCredentialsMigrator is one step of the chain: it reads a node
// written in one version and returns the same node written in the next. A
// step's only input is the node — it may derive nothing from the installation,
// the local config or the network — and it reports what it did as a
// WorkspaceCredentialsChange.
type workspaceCredentialsMigrator func(raw json.RawMessage) (json.RawMessage, WorkspaceCredentialsChange, error)

// workspaceCredentialsMigrators is the chain, keyed by the version a step
// reads. A version that bumps WorkspaceCredentialsVersion adds exactly one
// entry here, in the same change.
var workspaceCredentialsMigrators = map[int]workspaceCredentialsMigrator{
	1: migrateWorkspaceCredentialsV1ToV2,
}

// MigrateWorkspaceCredentials moves a published workspace credentials node
// forward to the version this Fugaro writes, one step at a time, and reports
// what each step did.
//
// The answers, in order, and they are the record's own (MigrateWorkspace) for
// this document:
//
//   - a node already at WorkspaceCredentialsVersion comes back byte for byte,
//     with no changes. Whether it is a node this Fugaro could have written is
//     DecodeWorkspaceCredentials' answer and not this function's;
//   - a node written by a newer Fugaro is refused with a *WorkspaceVersionError:
//     this binary cannot say what a later shape means, so it must not rewrite
//     the document every run of every member is admitted against;
//   - a node no version can be read from — not an object, a version below 1, a
//     top-level key written twice, or a migrated document the current reader
//     still refuses — is refused with a *WorkspaceNodeError, and the node is
//     never half-written: every refusal returns the input untouched.
func MigrateWorkspaceCredentials(raw []byte) ([]byte, []WorkspaceCredentialsChange, error) {
	if IsNull(raw) {
		return raw, nil, &WorkspaceNodeError{Why: "the workspace credentials node is empty"}
	}
	from, err := workspaceCredentialsNodeVersion(raw)
	if err != nil {
		return raw, nil, &WorkspaceNodeError{Why: err.Error()}
	}
	if from > WorkspaceCredentialsVersion {
		return raw, nil, &WorkspaceVersionError{Found: from, Understood: WorkspaceCredentialsVersion}
	}
	if from == WorkspaceCredentialsVersion {
		return raw, nil, nil
	}
	if from < 1 {
		return raw, nil, &WorkspaceNodeError{Why: fmt.Sprintf("it carries version %d, and no release ever wrote a workspace credentials node below version 1: the node is written by the workspace's own installation and never by hand, so this one is an edit of this file's document", from)}
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return raw, nil, &WorkspaceNodeError{Why: "the workspace credentials node is not a JSON object", Err: err}
	}
	if err := checkMemberNames("the workspace credentials node", names, workspaceCredentialsKeys); err != nil {
		return raw, nil, &WorkspaceNodeError{Why: "one of its top-level keys is written twice, or is a key this Fugaro writes spelled in another case", Err: err}
	}
	cur := json.RawMessage(raw)
	var changes []WorkspaceCredentialsChange
	for v := from; v < WorkspaceCredentialsVersion; v++ {
		step, ok := workspaceCredentialsMigrators[v]
		if !ok {
			// The version-bump guard forbids this state: a version with neither
			// a step nor a row is one nobody decided about, and it is refused
			// rather than migrated on a step that is not there.
			return raw, changes, fmt.Errorf("projectpolicy: this Fugaro has no migration step from workspace credentials node version %d, and none is expected below version %d", v, WorkspaceCredentialsVersion)
		}
		out, ch, err := step(cur)
		if err != nil {
			return raw, changes, err
		}
		cur, changes = out, append(changes, ch)
	}
	// The reader's own judgement, as the record's chain ends: the migrated bytes
	// must decode with the reader that reads this version, or the migration is
	// refused. That is what holds a step to the shape the reader reads rather
	// than to the shape its author believed in.
	if _, found, err := DecodeWorkspaceCredentials(cur); err != nil || !found {
		if err == nil {
			err = errors.New("the workspace credentials node is empty")
		}
		return raw, changes, &WorkspaceNodeError{Why: "the migrated node is one the current reader refuses", Err: err}
	}
	return cur, changes, nil
}

// migrateWorkspaceCredentialsV1ToV2 writes the node this Fugaro reads from a
// version-1 one.
//
// Version 2 adds three columns — the kind of a pool entry, the all_members key
// beside its projects, and the repositories of each job account — and the step
// adds none of them from itself: the version-1 reader already answers with the
// two values a version-1 document states by its silence (decodeWorkspaceCredentialsV1:
// a pool entry is a model credential, which is the only kind the pool held
// before the kind was a column, F15; and an account's repository list is empty,
// which is rule 3's state rather than an invented declaration), so the step
// decodes with that reader and re-encodes with the version-2 encoder, which
// writes those two columns and refuses a value the current reader would.
//
// `all_members` is deliberately NOT among them: the writer publishes it for a
// workspace with a cap and an owner who asks for it (D2977), and a migration
// that turned "offered to every member project listed" into "offered to every
// member" would widen a pool entry by a version bump.
func migrateWorkspaceCredentialsV1ToV2(raw json.RawMessage) (json.RawMessage, WorkspaceCredentialsChange, error) {
	w, err := decodeWorkspaceCredentialsV1(raw)
	if err != nil {
		return nil, WorkspaceCredentialsChange{}, &WorkspaceNodeError{Why: "the version-1 node is not the shape the release before this one wrote", Err: err}
	}
	w.Version = WorkspaceCredentialsVersion
	out, err := WorkspaceCredentialsNode(w)
	if err != nil {
		return nil, WorkspaceCredentialsChange{}, &WorkspaceNodeError{Why: "the version-1 node is not one this Fugaro publishes as version 2", Err: err}
	}
	return out, WorkspaceCredentialsChange{
		From:    1,
		To:      WorkspaceCredentialsVersion,
		Changed: "the version field (1 to 2), the kind of every pool entry (the version's silence to " + SharedKindModel + ", the only kind a version-1 pool held) and the repositories of every job account (none declared, which is the state a run on such an account is refused by)",
		Kept:    []string{"name", "members", "shared", "the projects each pool entry is offered to"},
	}, nil
}
