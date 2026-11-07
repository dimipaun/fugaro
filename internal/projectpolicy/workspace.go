package projectpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The workspace record's node: `workspaces/<name>` in the workspace's OWN
// database (docs/workspaces/spec.md §2, D1292), which the workspace owner's
// `fugaro update` writes from the workspace's local record (W2). The node
// names the members and carries the cap, and nothing else: every value a
// member trusts — its identity provider, the issuer its exchange requires, the
// assertion-signing key and the service account it binds its grants to — is
// the member's own pin in the member's own config, and a node that could
// supply one would be a node that could aim a member's exchange and a member's
// grant at a party of its choosing. That is why the shape here is small, and
// why the keys this file does not model have nowhere to go.
//
// It is written as a *versioned document* rather than as a struct the CLI
// re-encodes everywhere, exactly as the policy node is (migrate.go): the
// owner's node survives a release that does not understand it, and a step that
// moves an older one forward has the node itself as its only input.
//
// Money is integer micro-dollars from the record onward (workspace/workspace.go):
// the file the owner declares writes dollars and converts once, so this file
// converts nothing and the node carries micro-dollars as an integer.
const pathWorkspaces = "workspaces"

// WorkspaceVersion is the version of the workspace node this Fugaro writes. It
// is a version of its own and not the policy's: the two documents are read by
// different readers in different databases, and a bump of one says nothing
// about the other.
const WorkspaceVersion = 1

// PathWorkspace is one workspace's record, relative to the workspace's own
// database root and without a leading slash. The caller passes the raw name;
// it is validated long before a path is built (`workspace.Record.Validate`,
// which holds it to config.ProjectNameRE, and every project name is already a
// legal path segment).
func PathWorkspace(name string) string { return pathWorkspaces + "/" + seg("name", name) }

// PathWorkspaces is the parent of the workspace records, without a trailing
// slash. A read of the parent lists the workspaces a database holds, which is
// the owner's own credential's business and not a run's: no member holds a
// grant on the workspace's project at all, and the workspace's own service
// reads the one node it is for (workspace.Reader is not a member's).
func PathWorkspaces() string { return pathWorkspaces }

// Workspace is the workspace node decoded: what the workspace says about the
// group. The values are the record's own (internal/workspace), in the units the
// node carries them in.
type Workspace struct {
	// Version is the node's version. It is WorkspaceVersion for every node
	// DecodeWorkspace reads: an older one is MigrateWorkspace's to carry
	// forward, and Decode is the *current* reader.
	Version int
	// Name is the workspace's name, which is the project name of the
	// installation that publishes it and the last segment of its path.
	Name string
	// Members are the projects in the workspace, in project-name order and
	// each once, with the repositories each contributes sorted.
	Members []WorkspaceMember
	// Cap is the workspace's own spend ceiling, nil when the node states none.
	// A nil cap is not a cap of zero: a cap of zero would forbid every call the
	// workspace's credentials pay for.
	Cap *WorkspaceCap
	// UnknownKeys lists the top-level keys of the node this Fugaro does not
	// write, sorted. Decode fills it in and never writes it back: the keys are
	// tolerated so a node a newer Fugaro wrote does not brick an older reader
	// (P8), and the caller that would *rewrite* the node refuses them, because
	// publishing the record from a config would drop a key nobody modelled —
	// and "tolerated" and "silently dropped" look the same in the node
	// afterwards.
	UnknownKeys []string
}

// WorkspaceMember is one project in the workspace, as the node names it.
type WorkspaceMember struct {
	Project string
	Repos   []string
}

// WorkspaceCap is the workspace's own ceiling, as the node carries it.
type WorkspaceCap struct {
	MonthlyMicros pricing.Micros
}

// workspaceNode is the node as this Fugaro writes it: the shape the JSON of
// `workspaces/<name>` has, and the version this file's reader reads. It is a
// struct with json tags rather than a map, so the encoding is one order and
// one spelling every time — the publish compares a node against the bytes this
// function produces, and two encodings of one record would be two nodes.
type workspaceNode struct {
	Version int                   `json:"version"`
	Name    string                `json:"name"`
	Members []workspaceNodeMember `json:"members"`
	Cap     *workspaceNodeCap     `json:"cap,omitempty"`
}

type workspaceNodeMember struct {
	Project string   `json:"project"`
	Repos   []string `json:"repos"`
}

type workspaceNodeCap struct {
	MonthlyMicros pricing.Micros `json:"monthly_micros"`
}

// workspaceNodeKeys are the top-level keys this Fugaro writes. Every other key
// is tolerated and listed in Workspace.UnknownKeys (checkMemberNames refuses a
// spelling that differs from one of these only in case).
var workspaceNodeKeys = []string{"version", "name", "members", "cap"}

// WorkspaceNode builds the node this Fugaro publishes for a record: the bytes a
// conditional write puts at PathWorkspace(record.Name).
//
// The record is held to its own rules first (workspace.Record.Validate), so a
// node is never built from a record the file would refuse — a member list that
// is not sorted, a repository written twice, a cap of zero. The empty lists are
// written as empty lists and not as null: the node says "this member
// contributes no repository" the same way the file does.
func WorkspaceNode(r *workspace.Record) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("projectpolicy: the workspace record is not one this Fugaro publishes: %w", err)
	}
	n := workspaceNode{Version: WorkspaceVersion, Name: r.Name, Members: make([]workspaceNodeMember, 0, len(r.Members))}
	for _, m := range r.Members {
		repos := make([]string, 0, len(m.Repos))
		repos = append(repos, m.Repos...)
		n.Members = append(n.Members, workspaceNodeMember{Project: m.Project, Repos: repos})
	}
	if r.Cap.Set {
		n.Cap = &workspaceNodeCap{MonthlyMicros: r.Cap.Monthly}
	}
	out, err := json.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: the workspace record could not be encoded: %w", err)
	}
	return out, nil
}

// DecodeWorkspace reads the node this Fugaro writes. found is false for a node
// that is not there at all (null, or nothing), which is the first publish and
// not a refusal.
//
// An unknown top-level key is tolerated and listed (Workspace.UnknownKeys), a
// spelling of a known one that differs only in case is refused
// (checkMemberNames), and everything the node states is checked for what it
// must mean: the name is a project name, every member's project is one, a
// repository is not the empty string, and a cap that is written is more than
// zero — the record's own rules, read on the node rather than trusted from it.
//
// An older node is not this function's: MigrateWorkspace carries it forward,
// and a version below WorkspaceVersion that Decode is handed anyway is refused
// as a version this reader does not read.
func DecodeWorkspace(raw []byte) (*Workspace, bool, error) {
	if IsNull(raw) {
		return nil, false, nil
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, true, &WorkspaceNodeError{Why: "it is not a JSON object", Err: err}
	}
	if err := checkMemberNames("the workspace node", names, workspaceNodeKeys); err != nil {
		return nil, true, &WorkspaceNodeError{Why: "one of its top-level keys is written twice, or is a key this Fugaro writes spelled in another case", Err: err}
	}
	var n workspaceNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, true, &WorkspaceNodeError{Why: "it is not the shape a workspace record has", Err: err}
	}
	w := &Workspace{Version: n.Version, Name: n.Name, Members: make([]WorkspaceMember, 0, len(n.Members))}
	if n.Version != WorkspaceVersion {
		return nil, true, &WorkspaceVersionError{Found: n.Version, Understood: WorkspaceVersion}
	}
	if !config.ProjectNameRE.MatchString(n.Name) {
		return nil, true, &WorkspaceNodeError{Why: fmt.Sprintf("it names the workspace %q, which is not a project name (the grammar a workspace shares with a project, F21)", n.Name)}
	}
	for i, m := range n.Members {
		if !config.ProjectNameRE.MatchString(m.Project) {
			return nil, true, &WorkspaceNodeError{Why: fmt.Sprintf("members[%d] names the project %q, which is not a project name", i, m.Project)}
		}
		repos := make([]string, 0, len(m.Repos))
		for j, repo := range m.Repos {
			if repo == "" {
				return nil, true, &WorkspaceNodeError{Why: fmt.Sprintf("members[%d].repos[%d] is the empty string: a member contributes repositories it names", i, j)}
			}
			repos = append(repos, repo)
		}
		w.Members = append(w.Members, WorkspaceMember{Project: m.Project, Repos: repos})
	}
	if n.Cap != nil {
		if n.Cap.MonthlyMicros <= 0 {
			return nil, true, &WorkspaceNodeError{Why: "its cap is not more than zero: a cap of zero forbids every call the workspace's credentials pay for, and a workspace that states none has no cap block"}
		}
		w.Cap = &WorkspaceCap{MonthlyMicros: n.Cap.MonthlyMicros}
	}
	for _, name := range names {
		if !slices.Contains(workspaceNodeKeys, name) {
			w.UnknownKeys = append(w.UnknownKeys, name)
		}
	}
	return w, true, nil
}

// WorkspaceChange is one step of a workspace node's migration, in the words a
// caller renders as the diff: the version the step moves the node off, the one
// it writes, what it changes, and the values it keeps as they are.
type WorkspaceChange struct {
	From    int
	To      int
	Changed string
	Kept    []string
}

// workspaceMigrator is one step of the chain: it reads a node written in one
// version and returns the same node written in the next. A step's only input is
// the node — it may derive nothing from the installation, the local config or
// the network — and it reports what it did as a WorkspaceChange.
type workspaceMigrator func(raw json.RawMessage) (json.RawMessage, WorkspaceChange, error)

// workspaceMigrators is the chain, keyed by the version a step reads. A version
// that bumps WorkspaceVersion adds exactly one entry here, in the same change.
var workspaceMigrators = map[int]workspaceMigrator{
	0: migrateWorkspaceV0ToV1,
}

// MigrateWorkspace moves a published workspace node forward to the version this
// Fugaro writes, one step at a time, and reports what each step did.
//
// It exists for the same reason the policy node's chain does (migrate.go): a
// node the owner already has must converge on the one command they run, rather
// than being "delete and publish again" — which the HARD RULE refuses for
// `fugaro update` and which no `fugaro workspace publish` exists to be
// (reconciliation D1292's answer A). The answers, in order:
//
//   - a node already at WorkspaceVersion comes back byte for byte, with no
//     changes. It is not judged here: whether it is a record this Fugaro can
//     read is DecodeWorkspace's answer, and Migrate's is only whether it can be
//     moved forward;
//   - a node written by a newer Fugaro is refused with a *WorkspaceVersionError:
//     this binary cannot say what a later shape means, so it must not rewrite
//     the node every run of the workspace is admitted against;
//   - a node no version can be read from — not an object, an integer version
//     below zero, a top-level key written twice, or a migrated document the
//     reader still refuses — is refused with a *WorkspaceNodeError, and the
//     node is never half-written: every refusal returns the input untouched.
func MigrateWorkspace(raw []byte) ([]byte, []WorkspaceChange, error) {
	if IsNull(raw) {
		return raw, nil, &WorkspaceNodeError{Why: "it is empty"}
	}
	from, err := workspaceNodeVersion(raw)
	if err != nil {
		return raw, nil, err
	}
	if from > WorkspaceVersion {
		return raw, nil, &WorkspaceVersionError{Found: from, Understood: WorkspaceVersion}
	}
	if from == WorkspaceVersion {
		return raw, nil, nil
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return raw, nil, &WorkspaceNodeError{Why: "it is not a JSON object", Err: err}
	}
	if err := checkMemberNames("the workspace node", names, workspaceNodeKeys); err != nil {
		return raw, nil, &WorkspaceNodeError{Why: "one of its top-level keys is written twice, or is a key this Fugaro writes spelled in another case", Err: err}
	}
	cur := json.RawMessage(raw)
	var changes []WorkspaceChange
	for v := from; v < WorkspaceVersion; v++ {
		step, ok := workspaceMigrators[v]
		if !ok {
			// The version-bump guard forbids this state: a version with neither
			// a step nor a row is one nobody decided about, and it is refused
			// rather than migrated on a step that is not there.
			return raw, changes, fmt.Errorf("projectpolicy: this Fugaro has no migration step from workspace node version %d, and none is expected below version %d", v, WorkspaceVersion)
		}
		out, ch, err := step(cur)
		if err != nil {
			return raw, changes, err
		}
		cur, changes = out, append(changes, ch)
	}
	// The reader's own judgement, as Migrate's last step is: the migrated bytes
	// must decode with the reader that reads this version, or the migration is
	// refused. That is what holds a step to the shape the reader reads rather
	// than to the shape its author believed in.
	if _, found, err := DecodeWorkspace(cur); err != nil || !found {
		if err == nil {
			err = errors.New("the node is empty")
		}
		return raw, changes, &WorkspaceNodeError{Why: "the migrated node is one the current reader refuses", Err: err}
	}
	return cur, changes, nil
}

// workspaceNodeVersion reads the version off a node without decoding it.
//
// A node with NO version key is version 0, and version 0 is a state with a
// meaning rather than a fault: it is the record's own file shape, which
// schemas/workspace.schema.json describes and which is the only workspace
// document any release before this node existed describes. A person can have
// published one by hand from that schema, and the step below carries it over
// rather than asking them to publish again. A version below zero, or one that
// is not an integer, is a hand edit and is refused (a null version decodes to
// 0, which is the state above).
func workspaceNodeVersion(raw []byte) (int, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, &WorkspaceNodeError{Why: "it is not a JSON object", Err: err}
	}
	vraw, ok := doc["version"]
	if !ok {
		return 0, nil
	}
	var v int
	if err := json.Unmarshal(vraw, &v); err != nil {
		return 0, &WorkspaceNodeError{Why: "its version is not an integer", Err: err}
	}
	if v < 0 {
		return 0, &WorkspaceNodeError{Why: fmt.Sprintf("its version is %d, and no release ever wrote a workspace node below 0", v)}
	}
	return v, nil
}

// migrateWorkspaceV0ToV1 writes the node this Fugaro reads from a node in the
// record's own file shape.
//
// Version 0 is the shape schemas/workspace.schema.json describes — the
// workspace record as the owner declares it — so the step decodes the node with
// workspace.Parse, the one place the record's rules live, and builds the node
// from the record it made. That is also what makes the money right without a
// second conversion here: the file writes dollars, Parse converts them once
// with pricing.FromUSD, and the record it returns already holds micro-dollars
// (workspace/workspace.go's own note on the cap).
//
// The schema's objects are closed (additionalProperties: false) and Parse
// decodes strictly, so a version-0 node carrying a key this Fugaro does not
// model is refused rather than carried into a node nothing reads — which is the
// same reading the strict decoders everywhere else here give it. Nothing is
// lost by it: there is no version-0 writer but the schema, so such a key was
// not written by a release.
func migrateWorkspaceV0ToV1(raw json.RawMessage) (json.RawMessage, WorkspaceChange, error) {
	rec, err := workspace.Parse(raw)
	if err != nil {
		return nil, WorkspaceChange{}, &WorkspaceNodeError{Why: "the version-0 node is not the record shape schemas/workspace.schema.json describes", Err: err}
	}
	out, err := WorkspaceNode(rec)
	if err != nil {
		return nil, WorkspaceChange{}, err
	}
	return out, WorkspaceChange{
		From:    0,
		To:      WorkspaceVersion,
		Changed: "the version field (absent to 1) and the cap (the file's dollars to integer micro-dollars)",
		Kept:    []string{"name", "members"},
	}, nil
}

// WorkspaceVersionError is the refusal of a workspace node written by a newer
// Fugaro. It is a type of its own and not the policy's VersionError: the two
// documents are read by different readers, and a remedy that named the policy's
// move for this node would send the operator to the wrong command.
type WorkspaceVersionError struct{ Found, Understood int }

func (e *WorkspaceVersionError) Error() string {
	return fmt.Sprintf("the published workspace record is version %d and this Fugaro writes version %d", e.Found, e.Understood)
}

// WorkspaceNodeError is the refusal of a workspace node this Fugaro cannot read
// at all: one that is not a JSON object, carries no integer version, carries a
// version no release ever wrote, has a top-level key written twice, states a
// name or a member that is not a project name, states a cap that is not more
// than zero, or is refused by the current reader after a migration.
//
// None of it carries the node's bytes: the message is what an operator reads,
// and a node is a document the owner wrote.
type WorkspaceNodeError struct {
	Why string
	Err error
}

func (e *WorkspaceNodeError) Error() string {
	s := "projectpolicy: the workspace record cannot be read: " + e.Why
	if e.Err != nil {
		s += ": " + strings.TrimPrefix(e.Err.Error(), "projectpolicy: ")
	}
	return s
}

// Unwrap is the refusal behind the class, for a caller that reports the
// reader's own sentence rather than this one.
func (e *WorkspaceNodeError) Unwrap() error { return e.Err }
