package rules

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const workspaceGoldenPath = "testdata/workspace.golden.json"

// workspaceDatabaseRole is the role the workspace's own service account holds on
// the workspace's own database, as the root's one grant names it: the custom
// role the root creates, holding the instance's two DATA permissions and nothing
// else. Realtime Database IAM has no path-level scope (spec §8), so the
// whole-database REACH is what any grant of it gives — and the operations are
// the only thing left to narrow, which is why it is not roles/firebasedatabase
// .admin (that adds the instance lifecycle: list, create, delete, disable,
// reenable, undelete, none of which the workspace's own service ever makes).
// It is spelled here rather than read from internal/infra/tf so that the rules
// package's test says what it holds the root to without depending on another
// package's table.
const workspaceDatabaseRole = `projects/${var.project}/roles/fugaroWorkspaceDatabase`

// rulesTreeOf decodes a generated document into maps, the way rulesTree does
// for the budget family: the tests below read a document as a tree of locations
// and attributes, not as bytes.
func rulesTreeOf(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var doc map[string]map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the generated rules are not JSON: %v", err)
	}
	if len(doc) != 1 || doc["rules"] == nil {
		t.Fatalf("top level must be exactly {\"rules\": ...}, got %v", keys(doc))
	}
	return doc["rules"]
}

// workspaceTree is the workspace family's own document (GenerateWorkspace): what
// a reader goes to for the family's statements without the budget document
// around it.
func workspaceTree(t *testing.T) map[string]any {
	t.Helper()
	return rulesTreeOf(t, mustGenerateWorkspace(t))
}

// deployedTree is the document a database actually runs under (Generate): the
// budget family's nodes with the workspace family's merged in. Every question
// about what a client may reach is a question about THIS tree, because this is
// the document the deploy PUTs — the family's own document is a reading aid and
// never a deploy.
func deployedTree(t *testing.T) map[string]any {
	t.Helper()
	b, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	return rulesTreeOf(t, b)
}

// workspaceNode returns the node at the workspace family's path in tree.
func workspaceNode(t *testing.T, tree map[string]any, path ...string) map[string]any {
	t.Helper()
	node := tree
	for i, seg := range path {
		child, ok := node[seg].(map[string]any)
		if !ok {
			t.Fatalf("the document carries no %s node: %v", strings.Join(path[:i+1], "/"), keys(node))
		}
		node = child
	}
	return node
}

// denyEveryClient fails when any location of node grants a client a read or a
// write, and when the node names a token claim at all: no rule of this family
// may depend on who a client is, because no client reaches these nodes.
func denyEveryClient(t *testing.T, what string, node map[string]any) {
	t.Helper()
	walk(node, nil, func(path []string, attrs map[string]any) {
		for _, attr := range []string{".read", ".write"} {
			if v, ok := attrs[attr]; ok && v != false {
				t.Errorf("%s grants a client something at /%s (%s %v): every location of the workspace family denies every client, and the workspace's service is admitted by IAM and not by a rule", what, strings.Join(path, "/"), attr, v)
			}
		}
	})
}

// TestWorkspaceRulesGolden pins the workspace family's generated rules. They are
// part of what the workspace's own database runs under, so they are generated,
// never edited by hand, and this file is what a change to them has to walk past
// on purpose: UPDATE_GOLDEN=1 go test ./internal/budget/rules regenerates it,
// and the diff is security code a person reads.
func TestWorkspaceRulesGolden(t *testing.T) {
	got := mustGenerateWorkspace(t)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(workspaceGoldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(workspaceGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the generated workspace rules differ from %s (UPDATE_GOLDEN=1 regenerates; review the diff)", workspaceGoldenPath)
	}
	if !bytes.Equal(got, mustGenerateWorkspace(t)) {
		t.Fatal("GenerateWorkspace is not deterministic")
	}
}

// TestWorkspaceRulesShipWithTheBudgetDocument holds the family to the one
// document a database runs under: a Realtime Database carries exactly one rules
// document, the workspace's own project is a Fugaro installation whose database
// is already the budget family's, and a second document would be a deploy that
// took the first one away. So the generated document carries the family's nodes
// — the same nodes, byte for byte — and the deploy path (internal/infra's
// PutRules over rules.Generate) needs no change to ship them.
func TestWorkspaceRulesShipWithTheBudgetDocument(t *testing.T) {
	doc := deployedTree(t)
	family := workspaceTree(t)
	for _, path := range []string{WorkspaceRecordPath, WorkspaceTokenPath, WorkspaceMeterPath, WorkspaceRollupPath} {
		if _, ok := doc[path]; !ok {
			t.Fatalf("the document the database runs under carries no %s node: the workspace family ships inside it (a database has one rules document), so a family that is missing here is a family nothing deploys", path)
		}
		got, want := doc[path], family[path]
		if !bytes.Equal(mustJSON(t, got), mustJSON(t, want)) {
			t.Errorf("the document's /%s is not the family's own node:\n got %s\nwant %s", path, mustJSON(t, got), mustJSON(t, want))
		}
	}
	// The family owns no root of its own: the document's root denies every
	// client already, and a second `.read` here would be a family claiming a
	// grant the budget family never made.
	if doc[".read"] != false || doc[".write"] != false {
		t.Errorf("the document's root does not deny every client (read %v, write %v)", doc[".read"], doc[".write"])
	}
}

// TestWorkspaceRulesDoNotCollideWithTheBudgetDocument is the merge's own
// refusal, and it is a test of the refusal rather than of today's names: a
// family that took a name the budget document already carries would be merged
// as an overwrite, and a rule of one family would silently replace a rule of
// the other.
func TestWorkspaceRulesDoNotCollideWithTheBudgetDocument(t *testing.T) {
	// Today: the names are disjoint, so the merge is the family's own nodes and
	// the document's count grows by exactly those. Pinned as an equality: a
	// family node renamed away would leave the record denied by nothing.
	doc := deployedTree(t)
	for _, path := range []string{WorkspaceRecordPath, WorkspaceTokenPath, WorkspaceMeterPath, WorkspaceRollupPath} {
		if _, ok := doc[path]; !ok {
			t.Errorf("the deployed document carries no /%s node", path)
		}
		if !isNewWorkspacePath(path) {
			t.Errorf("/%s is a node of the deployed document and is not one of the workspace family's named paths: the additions test (isNamedAddition) would not have allowed it", path)
		}
	}

	// And the refusal itself, on a document that already carries the name.
	doc2, err := workspaceRules()
	if err != nil {
		t.Fatal(err)
	}
	root, err := parse(json.NewDecoder(strings.NewReader(`{"rules": {"tokens": {".read": false}}}`)))
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeWorkspaceRules(root, doc2); err == nil {
		t.Fatal("merging the workspace family into a document that already carries /tokens was admitted: the two readings of that name — whose nodes they are and what they deny — cannot both be served by one deploy")
	} else if !strings.Contains(err.Error(), "tokens") {
		t.Errorf("the refusal does not name the colliding node, so the fix is a guess: %v", err)
	}
}

// TestWorkspaceRulesDenyEveryRunTokenTheRecord is the red step the design names,
// and it is the test this task exists for.
//
// The record at workspaces/<name> is the workspace's own statement about the
// group: the members and the cap the workspace's own service rolls up and
// admits against. It is read by that service and by the workspace owner's CLI,
// and by nothing else (spec §2). A run of a member holds a Firebase ID token of
// its own project — fs, fr, fx, fp — and a member's machine holds no grant on
// the workspace's project at all; a rule that admitted such a token to the
// record would be a leak with a public address, because the database's URL is a
// published name and the token is one every run of every member has.
//
// It is read TWICE: the record's own node and every key below it, and then the
// document the database runs under, where the record sits inside the budget
// family's tree and a rule of that family could reach it. The second reading is
// what makes this a test of the deployed document and not of a fragment:
// /workspaces is denied at every level in the tree a client is judged by.
func TestWorkspaceRulesDenyEveryRunTokenTheRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		tree map[string]any
	}{
		{"the family's own document", workspaceTree(t)},
		{"the document the database runs under", deployedTree(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := workspaceNode(t, tc.tree, WorkspaceRecordPath)
			if node[".read"] != false || node[".write"] != false {
				t.Errorf("/%s does not deny every client (read %v, write %v)", WorkspaceRecordPath, node[".read"], node[".write"])
			}
			record := workspaceNode(t, tc.tree, WorkspaceRecordPath, "$name")
			// The record itself and each key it carries, one by one: the
			// parent's deny is not an answer for a location somebody could
			// grant later, so every level of the record states it.
			if record[".read"] != false || record[".write"] != false {
				t.Errorf("/%s/$name does not deny every client (read %v, write %v): the record is read by the workspace's own service over IAM and by nobody with a client token", WorkspaceRecordPath, record[".read"], record[".write"])
			}
			for _, key := range []string{"version", "name", "members", "cap"} {
				child, ok := record[key].(map[string]any)
				if !ok {
					t.Errorf("the record declares no %s node, so a later change could grant it by adding a rule where there is none: %v", key, keys(record))
					continue
				}
				if child[".read"] != false || child[".write"] != false {
					t.Errorf("the record's %s node does not deny every client (read %v, write %v)", key, child[".read"], child[".write"])
				}
			}
		})
	}
	// And the family as a whole, which is the sentence the red step is about: a
	// run token is admitted NOWHERE in it, so no node of a later family can be
	// reached with one either, and a change that adds one has to add a deny
	// rather than inherit an admission. The family is the part of the document
	// this task owns, and the walk says what it owns rather than what the
	// budget family already denied.
	if bytes.Contains(mustGenerateWorkspace(t), []byte("auth.token")) {
		t.Error("the family reads a token claim: no client reads these nodes, so no rule here may depend on who a client is")
	}
}

// TestWorkspaceRulesDenyEveryRunTokenTheTokenRecord is D1296's half of the
// rules: the workspace keeps the HASHES of the tokens it admits, in the
// workspace's own database, and no client reads them.
//
// A hash is not a token — that is the whole point of storing one — but a reader
// who holds the subtree holds the set of every token that was admitted, and can
// tell which of two candidate tokens is in it. So the subtree is denied at
// every level, in the document the database runs under and not only in the
// family's own reading of it.
func TestWorkspaceRulesDenyEveryRunTokenTheTokenRecord(t *testing.T) {
	doc := deployedTree(t)
	tokens := workspaceNode(t, doc, WorkspaceTokenPath)
	if tokens[".read"] != false || tokens[".write"] != false {
		t.Errorf("/%s does not deny every client (read %v, write %v): the admitted-token hashes are written by the workspace's own service and read by nobody with a client token", WorkspaceTokenPath, tokens[".read"], tokens[".write"])
	}
	hash := workspaceNode(t, doc, WorkspaceTokenPath, "$hash")
	if hash[".read"] != false || hash[".write"] != false {
		t.Errorf("/%s/$hash does not deny every client (read %v, write %v)", WorkspaceTokenPath, hash[".read"], hash[".write"])
	}
	// The meter and the rollup are W7's, and they are the workspace's money:
	// they are shut here for the same reason, because the SHAPE of the database
	// is what this task ships — a later task adds a writer, not a reader.
	for _, path := range []string{WorkspaceMeterPath, WorkspaceRollupPath} {
		node := workspaceNode(t, doc, path)
		if node[".read"] != false || node[".write"] != false {
			t.Errorf("/%s does not deny every client (read %v, write %v)", path, node[".read"], node[".write"])
		}
		workspaceNode(t, doc, path, "$month")
	}
	// Every family node, in the document that is deployed: nothing this task
	// ships admits a client, at any level.
	family := map[string]any{}
	for _, path := range []string{WorkspaceRecordPath, WorkspaceTokenPath, WorkspaceMeterPath, WorkspaceRollupPath} {
		family[path] = doc[path]
	}
	denyEveryClient(t, "the workspace family in the deployed document", family)
	denyEveryClient(t, "the workspace family's own document", workspaceTree(t))
}

// resourceOpen matches the opening line of a resource block in the workspace
// root. The root is a file this repository authors, so the shape is fixed.
var resourceOpen = regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)" "([a-z0-9_]+)" \{`)

// workspaceRootText reads the workspace Terraform root as text, the way
// TestMailboxRulesAgreeWithTheSharedSchema reads the shared schema: what a
// binding NAMES is what this test is about, and a parsed plan would answer the
// same question with more machinery and less of the file a person reviews.
func workspaceRootText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "roots", "workspace", "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// workspaceBlock is one `resource "type" "name" { ... }` block of the root.
type workspaceBlock struct {
	Type, Name, Body string
}

// workspaceResourceBlocks returns the resource blocks of the root. The root is a
// file this repository authors, so the shape is fixed: a block opens on a line
// that declares a resource and closes before the next one.
func workspaceResourceBlocks(t *testing.T, text string) []workspaceBlock {
	t.Helper()
	var out []workspaceBlock
	locs := resourceOpen.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, workspaceBlock{
			Type: text[loc[2]:loc[3]],
			Name: text[loc[4]:loc[5]],
			Body: text[loc[0]:end],
		})
	}
	if len(out) == 0 {
		t.Fatal("the workspace root declares no resource at all: the root is what creates the workspace's own service account and its one grant")
	}
	return out
}

// TestWorkspaceRulesAllowTheServicesOwnAccount is the other half of the tests
// above, and it is stated as a pair on purpose: the rules admit NOBODY, and the
// one identity that reaches the workspace's own database is the workspace's own
// service, over IAM.
//
// IAM on a Realtime Database is whole-database and it bypasses the rules (spec
// §8), so the service's access cannot be a rule — and it must not be one,
// because a rule naming the service would be a rule naming an identity a client
// can be. What this test holds is that the two halves stay apart: the document
// grants no client anything at these nodes (so no client token reaches the
// service's reach), and the root binds the database role to the workspace's own
// service account and to no other principal, so the whole-database reach is that
// account's and the run accounts, the members' accounts and the project's own
// members hold nothing on it.
//
// WS6 gave the root two more grants than the one above — the workspace's own
// credentials service reaches the workspace's ONE bundle container as that same
// account, and the member table's job accounts reach that service's front door
// (D2980, F4, F17) — and none of them is a grant on the database. WC2 added the
// two the roster needs (R6, D2971): one invoke grant per `human` of the
// workspace's own roster, so a person reaches that service with their own
// identity, and the ONE conditioned project-level accessor that same account
// holds on a person's own Claude token container — its condition is what makes
// it a name and not a project, and the root's own cover holds that condition
// byte for byte (internal/infra/tf's WorkspaceNotCovered; deploy/terraform's
// TestNoProjectLevelSecretAccessor is the tree-side half). The table below is
// therefore the whole of the root's reach, one row per member resource it may
// declare, and a grant the table does not know is refused rather than counted:
// the workspace's own project holds the account, its one database role, the
// database grant and the service's own four grants, and nothing else (spec §9;
// internal/infra/tf's WorkspaceNotCovered is the plan-side half of the same
// rule).
func TestWorkspaceRulesAllowTheServicesOwnAccount(t *testing.T) {
	// The document's half: no client token opens the family, so a caller
	// reaches the workspace's database as the service or not at all.
	denyEveryClient(t, "the workspace family in the deployed document", map[string]any{
		WorkspaceRecordPath: deployedTree(t)[WorkspaceRecordPath],
		WorkspaceTokenPath:  deployedTree(t)[WorkspaceTokenPath],
		WorkspaceMeterPath:  deployedTree(t)[WorkspaceMeterPath],
		WorkspaceRollupPath: deployedTree(t)[WorkspaceRollupPath],
	})

	// The root's half: every grant it declares, and what that grant must bind.
	//
	// What counts as binding an account is the block's own member ATTRIBUTE and
	// not a mention of it: the custom role's description says which account
	// holds it, and a sentence is not a grant.
	type rootGrant struct {
		role, member, why string
	}
	const accountRef = "google_service_account.workspace.member"
	want := map[string]rootGrant{
		"google_project_iam_member.workspace_database": {
			role:   `"` + workspaceDatabaseRole + `"`,
			member: accountRef,
			why:    "the workspace's own service account is the one identity that reaches the workspace database, and a grant to anything else is a principal that can read the record and the admitted-token hashes",
		},
		"google_secret_manager_secret_iam_member.workspace_bundle": {
			role:   `"roles/secretmanager.secretAccessor"`,
			member: accountRef,
			why:    "the workspace's service reads its ONE bundle container as its own account: a second reader of the workspace's model credentials on that container, or an UNCONDITIONED project-level accessor, is the pool disclosed to a principal the service did not choose (F4, Q6; the one conditioned project accessor WC2 adds is the row below, and its condition is the whole of why it is not this)",
		},
		"google_cloud_run_v2_service_iam_member.workspace_invoker": {
			role:   `"roles/run.invoker"`,
			member: `"serviceAccount:${each.value}"`,
			why:    "the member table's own job accounts are the only principals that reach that service, and the invoke grant is the ONE grant a member holds in the workspace's project (F17, F29)",
		},
		"google_cloud_run_v2_service_iam_member.workspace_person_invoker": {
			role:   `"roles/run.invoker"`,
			member: `"user:${each.value}"`,
			why:    "a `human` of the workspace's own roster reaches that service with their own Google identity and with no other, one grant per address the roster names, so `fugaro update` takes the grant away when the roster stops naming them (R6, D2971, F25)",
		},
		"google_project_iam_member.workspace_person_token": {
			role:   `"roles/secretmanager.secretAccessor"`,
			member: accountRef,
			why:    "the one project-level accessor of this root, and the whole of why it is safe is its condition: the per-person `fugaro_u_` prefix AND the one personal name the workspace serves, so a container of this project outside those two - the workspace's own bundle, a `fugaro_p_` entry, another project's naming - is not readable through it (R6, D2971; internal/infra/tf's WorkspaceNotCovered refuses a wider one)",
		},
	}
	addrs := []string{
		"google_project_iam_member.workspace_database",
		"google_secret_manager_secret_iam_member.workspace_bundle",
		"google_cloud_run_v2_service_iam_member.workspace_invoker",
		"google_cloud_run_v2_service_iam_member.workspace_person_invoker",
		"google_project_iam_member.workspace_person_token",
	}
	roleAttr := regexp.MustCompile(`(?m)^\s*role\s*=\s*(.+?)\s*$`)
	memberAttr := regexp.MustCompile(`(?m)^\s*member\s*=\s*(.+?)\s*$`)
	seen := map[string]bool{}
	for _, b := range workspaceResourceBlocks(t, workspaceRootText(t)) {
		addr := b.Type + "." + b.Name
		member := ""
		if m := memberAttr.FindStringSubmatch(b.Body); m != nil {
			member = m[1]
		}
		if !strings.HasSuffix(b.Type, "_iam_member") {
			if member != "" {
				t.Errorf("%s binds %s outside a member resource: the root's reach is the three grants below and nothing else", addr, member)
			}
			continue
		}
		g, ok := want[addr]
		if !ok {
			t.Errorf("%s is a member resource this test does not know: the workspace's own project holds the account, its one database role, the database grant and the service's own two grants, and a fourth resource here is a decision a person makes in this file and in the root's own cover together", addr)
			continue
		}
		if seen[addr] {
			t.Errorf("%s is declared twice: one row of the root's reach is one resource", addr)
		}
		seen[addr] = true
		role := "(no role attribute)"
		if m := roleAttr.FindStringSubmatch(b.Body); m != nil {
			role = m[1]
		}
		if role != g.role {
			t.Errorf("%s binds %s, want %s: %s", addr, role, g.role, g.why)
		}
		if member != g.member {
			t.Errorf("%s binds %q, want %q: %s", addr, member, g.member, g.why)
		}
	}
	for _, addr := range addrs {
		if !seen[addr] {
			t.Errorf("the workspace root declares no %s: the account the root creates has no grant at all, so the root and the document disagree about who reaches the database", addr)
		}
	}
}

// TestWorkspaceRulesVersionIsPinnedAndMigrated is the marker's own pattern, and
// it is what makes these nodes safe to ship: the version is written after the
// rules deploy and read before the next one, so a database whose document came
// from a NEWER Fugaro refuses this binary's deploy instead of half-applying —
// writing an older document under a newer marker, which is the state the doctor
// warns about and nobody can repair from the older binary.
//
// A marker BELOW this Fugaro's (or absent: generation 0) is the upgrade and not
// a refusal: it is a database whose rules predate this family, and the next
// `fugaro update` deploys this binary's own document and records this
// generation.
func TestWorkspaceRulesVersionIsPinnedAndMigrated(t *testing.T) {
	if got, want := PathWorkspaceRulesMarker(), "config/limits/workspaceRules"; got != want {
		t.Fatalf("the workspace rules marker moved to %q, and its path is pinned: %q is where a released Fugaro reads and writes it, and a moved path reads as 'no marker' on every database that already has one", got, want)
	}
	if !strings.HasPrefix(PathWorkspaceRulesMarker(), "config/limits/") {
		t.Fatalf("the marker %q left config/limits: every reader of it holds its credential over IAM, which is what makes a node that no rule writes readable at all", PathWorkspaceRulesMarker())
	}
	if WorkspaceRulesVersion != 1 {
		t.Fatalf("the workspace rules generation is %d and this test pins 1: bumping it is a decision a person makes in this file, because the deploy refuses a marker above its own", WorkspaceRulesVersion)
	}
	for _, tc := range []struct {
		name           string
		found          bool
		deployed       int
		wantDeployable bool
	}{
		{"no marker at all is the first deploy", false, 0, true},
		{"a marker at this generation needs no write", true, WorkspaceRulesVersion, true},
		{"a generation below this one is the upgrade", true, WorkspaceRulesVersion - 1, true},
		{"a generation above this one is a newer Fugaro's", true, WorkspaceRulesVersion + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckWorkspaceRulesMarker(tc.found, tc.deployed)
			if tc.wantDeployable && err != nil {
				t.Fatalf("this Fugaro may not deploy its workspace rules over marker found=%v generation %d: %v", tc.found, tc.deployed, err)
			}
			if !tc.wantDeployable {
				if err == nil {
					t.Fatalf("deploying generation %d over a marker at generation %d was admitted: the newer database's document would be replaced by this binary's older one, and the marker would then record the older number", WorkspaceRulesVersion, tc.deployed)
				}
				for _, want := range []string{"fugaro update", "upgrade", selfCommandWord} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q, so the operator reads what happened without reading what to do: %v", want, err)
					}
				}
			}
		})
	}
	// The marker lives under the budget document's config/limits, and no client
	// writes it there either: the deploy writes it as the operator, over IAM,
	// and the document's own rules leave every client outside.
	limits := workspaceNode(t, deployedTree(t), "config", "limits")
	if v, ok := limits[".write"]; ok && v != false {
		t.Errorf("config/limits is writable by a client (%v): the marker is written and read over IAM, and a node a client could write is a generation a client could claim", v)
	}
	if v, ok := deployedTree(t)["config"].(map[string]any)[".write"]; ok && v != false {
		t.Errorf("config is writable by a client (%v): nothing under config is a client's write, the markers included", v)
	}
}

// selfCommandWord is the binary's own name as the refusal has to spell it: the
// sentence the operator reads has to name the command that converges the
// installation, and `fugaro update` is that command (the HARD RULE: one
// command, and it works).
const selfCommandWord = "fugaro"

func mustGenerateWorkspace(t *testing.T) []byte {
	t.Helper()
	b, err := GenerateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
