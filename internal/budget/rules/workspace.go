package rules

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

// The workspace family's rules (docs/workspaces/implementation-plan.md W3,
// spec.md §2, §5).
//
// The workspace's own database holds four things no client may read: the record
// at workspaces/<name> — the member list and the cap, published by the workspace
// OWNER's `fugaro update` (W2) — the hashes of the tokens the workspace admitted
// (D1296), the meter the workspace's cap is taken from and the rollup the
// reconciliation alarm compares (W7). The record is read by the workspace's own
// service and by the workspace owner's CLI, and by nothing else (spec §2); no
// member's client reads it at all, because a member holds no grant on the
// workspace's project (§5).
//
// Those four subtrees are this family's, and every one of them denies every
// client: the identities that DO reach them reach the database over IAM, which
// bypasses the rules. That is the whole design of this file, and it is why
// nothing here reads auth.token — a rule naming a token claim would be a rule
// naming an identity a client can be, and there is no client these nodes are
// for (testdata/workspace.golden.json is the pin, and workspace_test.go is what
// holds the statements apart).
//
// They ship as nodes of the document the database already runs under, and not
// as a document of their own, because a Realtime Database carries exactly ONE
// rules document (rtdb's settingsRules, an instance cannot have a second): the
// workspace's own project is a Fugaro installation whose database carries the
// budget family — its mark, its mode, its caps, its runs — and a second
// document would be a deploy that took the first one away. So the workspace
// family is merged into the generated document (Generate, below) under names no
// other family has, and the merge refuses a name the document already carries
// rather than overwriting it: a later family that wanted `tokens` for something
// else has to say so here.
//
// The marker below is what makes it converge rather than half-apply
// (marker.go's credentials family is the pattern): the generation is read
// before the deploy and written after it, a marker above this binary's refuses
// the deploy, and a marker below it (or absent) is a database whose rules
// predate this family and is the upgrade the same one command performs. A
// database whose document lost these nodes — an older Fugaro's deploy over
// them, which its own doctor told its owner to make — is caught by the
// document comparison every deploy already makes (internal/infra's SameJSON),
// not by the marker.
//
//go:embed workspace.json.tmpl
var workspaceSource string

// PathWorkspaceRulesMarker is the workspace rules family's generation marker,
// `config/limits/workspaceRules`.
//
// It sits under `config/limits` for the reason the credentials family's marker
// does (marker.go): that is the prefix a reader of a marker already reaches
// over IAM. The workspace's own database is reached by the workspace's own
// service and the owner's CLI and by nobody else, and both of them hold their
// credential over IAM, so the marker needs no rule of its own.
//
// It is a path of its own and not `config/limits/policyRules`: the two markers
// count different families, and a reader that confused them would report a skew
// the installation does not have.
func PathWorkspaceRulesMarker() string { return "config/limits/workspaceRules" }

// WorkspaceRulesVersion is the generation of the workspace rules this Fugaro
// carries: the first generation of the family — the record, the admitted-token
// hashes, the meter and the rollup, all shut to every client. It counts this
// family's changes only, and a change to it is a change to a document a
// database already runs under, so it is bumped in the same change that moves
// the document (workspace_test.go pins the number).
const WorkspaceRulesVersion = 1

// The database's locations, as this task ships them. They are exported because
// the tasks that write into them are not this one: W2 publishes the record at
// workspaces/<name> (projectpolicy.PathWorkspace, which owns that path's shape),
// W4 keeps the admitted-token hashes under WorkspaceTokenPath, and W7's meter
// and rollup live under WorkspaceMeterPath and WorkspaceRollupPath. What this
// file owns is that the database's SHAPE has a name a later task reads rather
// than a literal it retypes — the rules document and the writer must agree on
// the path, and a second spelling of it is a node nothing denies.
const (
	// WorkspaceTokenPath is the subtree of the admitted tokens' hashes (D1296):
	// hashes only, and no client reads them.
	WorkspaceTokenPath = "tokens"
	// WorkspaceMeterPath is the subtree the workspace's cap is taken from: the
	// reservations and the settles of the calls the workspace's own credentials
	// paid for (W7).
	WorkspaceMeterPath = "meter"
	// WorkspaceRollupPath is the subtree the reconciliation alarm compares a
	// member's ledger against (W7). It is not the basis of the cap any more.
	WorkspaceRollupPath = "rollup"
	// WorkspaceRecordPath is the subtree the workspace's record lives at, the
	// parent of projectpolicy.PathWorkspace's own node.
	WorkspaceRecordPath = "workspaces"
)

// GenerateWorkspace returns the workspace family's rules as a document of their
// own, indented and deterministic: what workspace_test.go reads to hold the
// family's own statements (the record, the token hashes, the meter and the
// rollup deny every client, and no rule here reads a token claim), and what a
// reader goes to for the family without the budget document around it.
//
// It is NOT what a database is deployed with: Generate (rules.go) merges this
// document's nodes into the budget document, and that merged document is the
// one PUT. Two entry points rather than one because they answer two questions —
// "what does this family say" and "what does the database run under" — and the
// second is the only one a deploy may use.
//
// The template carries no macro: the family names no identity, so there is no
// expression to expand — and that absence is a property a reader can check
// (TestWorkspaceRulesDenyEveryRunTokenTheRecord fails a node that reads a token
// claim). It is still generated and still pinned, for the budget family's
// reason: this is security code, the golden file is what a change has to walk
// past on purpose, and a hand-edited document is a document nobody reviewed.
func GenerateWorkspace() ([]byte, error) {
	root, err := workspaceRules()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := root.render(&out, 0, nil); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	if !json.Valid(out.Bytes()) {
		return nil, fmt.Errorf("rules: the generated workspace rules are not JSON")
	}
	return out.Bytes(), nil
}

// workspaceRules parses the family's template into the ordered tree the
// document generator merges (mergeWorkspaceRules).
func workspaceRules() (*node, error) {
	root, err := parse(json.NewDecoder(bytes.NewReader([]byte(workspaceSource))))
	if err != nil {
		return nil, fmt.Errorf("rules: the workspace template is not JSON: %w", err)
	}
	return root, nil
}

// mergeWorkspaceRules appends the workspace family's nodes to the budget
// document, which is the document a database is deployed with.
//
// It copies the family's own top-level nodes and nothing else: the document's
// root denies every client already (rules.json.tmpl), so the family's root keys
// are the same two literals, and the family owns no `config` — the marker's
// path is written over IAM and the document's own `config` rules are the budget
// family's.
//
// A name the document already carries is a refusal and never an overwrite. The
// two documents are generated in this package, so a collision means a family
// was added or renamed without this merge being read, and the two readings of
// that name — whose nodes they are, and what they deny — cannot both be served
// by one deploy. The error names the node, so the fix is a decision and not a
// guess (TestWorkspaceRulesDoNotCollideWithTheBudgetDocument holds the names
// apart today).
func mergeWorkspaceRules(doc, family *node) error {
	rules, ok := childOf(doc, "rules")
	if !ok || !rules.obj {
		return fmt.Errorf("rules: the generated document has no rules object")
	}
	fam, ok := childOf(family, "rules")
	if !ok || !fam.obj {
		return fmt.Errorf("rules: the workspace template has no rules object")
	}
	for i, k := range fam.keys {
		if k == ".read" || k == ".write" {
			continue
		}
		if _, exists := childOf(rules, k); exists {
			return fmt.Errorf("rules: the workspace family's %q node is already a node of the budget document: the two cannot be merged under one name, so one of them has to be renamed", k)
		}
		rules.keys = append(rules.keys, k)
		rules.vals = append(rules.vals, fam.vals[i])
	}
	return nil
}

// childOf returns the child of an object node by key.
func childOf(n *node, key string) (*node, bool) {
	if n == nil || !n.obj {
		return nil, false
	}
	for i, k := range n.keys {
		if k == key {
			return n.vals[i], true
		}
	}
	return nil, false
}

// CheckWorkspaceRulesMarker answers whether this Fugaro may deploy its workspace
// rules over the generation the database already carries: found is whether the
// marker is there at all, deployed the generation it holds.
//
// A marker ABOVE this binary's is a newer Fugaro's document, and the refusal is
// the same one the policy and credentials families give (internal/infra's
// DB.Plan, which is where it is asked): this binary's document would replace a
// newer one and then record its own older generation in the marker, which is
// the downgrade `fugaro doctor` warns about and no older binary can repair.
// Nothing is written.
//
// Everything else is the upgrade: a marker BELOW this generation, and an absent
// marker (generation 0, a database whose rules were deployed before this family
// existed), are both a database whose rules predate these, and deploying this
// binary's document over them — and recording this generation — is exactly what
// the one command does.
func CheckWorkspaceRulesMarker(found bool, deployed int) error {
	if found && deployed > WorkspaceRulesVersion {
		return fmt.Errorf("the deployed security rules carry workspace generation %d (%s = %d) and this Fugaro's are generation %d: the nodes the workspace's own database runs under came from a newer Fugaro, so this run deploys nothing there — upgrade Fugaro (brew upgrade dimipaun/tap/fugaro, then fugaro update), and run fugaro update again with the newer binary, which is the one that deploys here",
			deployed, PathWorkspaceRulesMarker(), deployed, WorkspaceRulesVersion)
	}
	return nil
}
