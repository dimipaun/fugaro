package rules

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

// The workspace's OWN database rules (docs/workspaces/implementation-plan.md
// W3, spec.md §2, §5).
//
// A workspace has a database of its own, in the Firebase project the workspace
// is (D1290, §3), and it holds three things no client may read: the record at
// workspaces/<name> — the member list and the cap the workspace's own service
// rolls up and admits against, published by the workspace OWNER's `fugaro
// update` (W2) — the hashes of the tokens the workspace admitted (D1296), and
// the meter the workspace's cap is taken from (W7). The record is read by the
// workspace's own service and by the workspace owner's CLI, and by nothing else
// (spec §2); no member's client reads this database at all, because a member
// holds no grant on the workspace's project (§5).
//
// So this document is default-deny and it names no identity: every location
// denies every client, and the identities that DO reach the database reach it
// over IAM, which bypasses the rules. That is the whole design of this file,
// and it is why nothing here reads auth.token: a rule naming a token claim
// would be a rule naming an identity a client can be, and there is no client
// this database is for (testdata/workspace.golden.json is the pin, and
// workspace_test.go is what holds the three statements apart).
//
// Why a document of its own rather than the budget family's (rules.json.tmpl):
// the two databases answer different questions. The budget rules are a
// deliberate maze of per-family read and write grants, because the installation's
// database is read by launchers, the console, the phone and every run. This one
// has exactly one clientless answer, and shipping it as a second family in one
// document would put the record's path inside a tree some other family's rule
// reaches. A document is deployed whole (internal/infra's PutRules), so the
// boundary is the document.
//
// The marker below is what makes it converge rather than half-apply
// (projectpolicy.PathRulesMarker and rules.marker.go are the pattern): the
// generation is read before the deploy and written after it, a marker above
// this binary's refuses the deploy, and a marker below it (or absent) is an
// installation whose rules predate this document and is the upgrade the same
// one command performs.
//
//go:embed workspace.json.tmpl
var workspaceSource string

// PathWorkspaceRulesMarker is the workspace rules family's generation marker,
// `config/limits/workspaceRules`, in the WORKSPACE'S OWN database.
//
// It sits under `config/limits` for the reason the credentials family's marker
// does (marker.go): that is the prefix every reader of a marker already holds
// over IAM, and IAM is the only way into this database. Nothing here is
// writable through the rules at all (the document denies every client), so the
// marker is written by the deploy as the operator and read by the same
// credential.
//
// It is a path of its own and not `config/limits/policyRules`: the two markers
// count different documents in different databases, and a reader that confused
// them would report a skew the installation does not have.
func PathWorkspaceRulesMarker() string { return "config/limits/workspaceRules" }

// WorkspaceRulesVersion is the generation of the workspace rules this Fugaro
// carries: the first document of the family — the record, the admitted-token
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
)

// GenerateWorkspace returns the workspace's own database rules, indented and
// deterministic.
//
// The template carries no macro: the document names no identity, so there is no
// expression to expand — and that absence is a property a reader can check
// (TestWorkspaceRulesDenyEveryRunTokenTheRecord fails a document that reads a
// token claim). It is still generated and still pinned, for the budget family's
// reason: this is security code, the golden file is what a change has to walk
// past on purpose, and a hand-edited document is a document nobody reviewed.
func GenerateWorkspace() ([]byte, error) {
	root, err := parse(json.NewDecoder(bytes.NewReader([]byte(workspaceSource))))
	if err != nil {
		return nil, fmt.Errorf("rules: the workspace template is not JSON: %w", err)
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

// CheckWorkspaceRulesMarker answers whether this Fugaro may deploy its workspace
// rules over the generation the database already carries: found is whether the
// marker is there at all, deployed the generation it holds.
//
// A marker ABOVE this binary's is a newer Fugaro's document, and the refusal is
// the same one the policy and credentials families give (internal/infra's
// DB.Plan): this binary's document would replace a newer one and then record
// its own older generation in the marker, which is the downgrade `fugaro
// doctor` warns about and no older binary can repair. Nothing is written.
//
// Everything else is the upgrade: a marker BELOW this generation, and an absent
// marker (generation 0, a database whose rules were deployed before this family
// existed), are both an installation whose rules predate these, and deploying
// this binary's document over them — and recording this generation — is exactly
// what the one command does.
func CheckWorkspaceRulesMarker(found bool, deployed int) error {
	if found && deployed > WorkspaceRulesVersion {
		return fmt.Errorf("the deployed workspace rules are generation %d (%s = %d) and this Fugaro's are generation %d: the rules the workspace's own database runs under came from a newer Fugaro, so this run deploys nothing there — upgrade Fugaro (brew upgrade dimipaun/tap/fugaro, then fugaro update), and run fugaro update again with the newer binary, which is the one that deploys here",
			deployed, PathWorkspaceRulesMarker(), deployed, WorkspaceRulesVersion)
	}
	return nil
}
