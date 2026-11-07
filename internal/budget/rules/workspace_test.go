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
// the workspace's own database. It is the whole-database role and not a narrower
// one, because Realtime Database IAM has no path-level scope (spec §8), and it
// is spelled here rather than read from internal/infra/tf so that the rules
// package's test says what it holds the root to without depending on another
// package's table.
const workspaceDatabaseRole = `roles/firebasedatabase.admin`

// workspaceTree decodes the generated workspace document into maps, the way
// rulesTree does for the budget family: the tests below read it as a tree of
// locations and attributes, not as bytes.
func workspaceTree(t *testing.T) map[string]any {
	t.Helper()
	b, err := GenerateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the generated workspace rules are not JSON: %v", err)
	}
	if len(doc) != 1 || doc["rules"] == nil {
		t.Fatalf("top level must be exactly {\"rules\": ...}, got %v", keys(doc))
	}
	return doc["rules"]
}

// TestWorkspaceRulesGolden pins the generated workspace rules. They are the
// only thing between a client that carries a workspace token, a run token or a
// member's job account and the workspace's own record, so the document is
// generated, never edited by hand, and this file is what a change to it has to
// walk past on purpose: UPDATE_GOLDEN=1 go test ./internal/budget/rules
// regenerates it, and the diff is security code a person reads.
func TestWorkspaceRulesGolden(t *testing.T) {
	got, err := GenerateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
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
	again, _ := GenerateWorkspace()
	if !bytes.Equal(got, again) {
		t.Fatal("GenerateWorkspace is not deterministic")
	}
}

// TestWorkspaceRulesDenyEveryRunTokenTheRecord is the red step the design
// names, and it is the test this task exists for.
//
// The record at workspaces/<name> is the workspace's own statement about the
// group: the members and the cap the workspace's own service rolls up and
// admits against. It is read by that service and by the workspace owner's CLI,
// and by nothing else (spec §2). A run of a member holds a Firebase ID token of
// its own project — fs, fr, fx, fp — and a member's machine holds no grant on
// the workspace's project at all; a rules file that admitted such a token to
// the record would be a leak with a public address, because the database's URL
// is a published name and the token is one every run of every member has.
//
// The test reads the record's own node and every node below it, and then the
// whole document: no location grants a read or a write to anything but false,
// so no token of any kind is admitted anywhere, and the record is shut at every
// level rather than by its parent alone.
func TestWorkspaceRulesDenyEveryRunTokenTheRecord(t *testing.T) {
	tree := workspaceTree(t)
	node, ok := tree["workspaces"].(map[string]any)
	if !ok {
		t.Fatalf("the document carries no workspaces node: %v", keys(tree))
	}
	if node[".read"] != false || node[".write"] != false {
		t.Errorf("/workspaces does not deny every client (read %v, write %v)", node[".read"], node[".write"])
	}
	record, ok := node["$name"].(map[string]any)
	if !ok {
		t.Fatalf("workspaces carries no record node: %v", keys(node))
	}
	// The record itself and each key it carries, one by one: the parent's deny
	// is not an answer for a location somebody could grant later, so every
	// level of the record states it.
	if record[".read"] != false || record[".write"] != false {
		t.Errorf("/workspaces/\u0000 does not deny every client (read %v, write %v): the record is read by the workspace's own service over IAM and by nobody with a client token", record[".read"], record[".write"])
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
	// And the whole document, which is the sentence the red step is about: a
	// run token is admitted NOWHERE, so no path of a later family can be
	// reached with one either, and a change that adds a family has to add a
	// deny rather than inherit an admission.
	walk(tree, nil, func(path []string, attrs map[string]any) {
		for _, attr := range []string{".read", ".write"} {
			if v, ok := attrs[attr]; ok && v != false {
				t.Errorf("/%s %s is %v: every location of the workspace's own database denies every client, and the workspace's service is admitted by IAM and not by a rule", strings.Join(path, "/"), attr, v)
			}
		}
	})
	if bytes.Contains(mustGenerateWorkspace(t), []byte("auth.token")) {
		t.Error("the document reads a token claim: no client reads this database, so no rule here may depend on who a client is")
	}
}

// TestWorkspaceRulesDenyEveryRunTokenTheTokenRecord is D1296's half of the
// rules: the workspace keeps the HASHES of the tokens it admits, in the
// workspace's own database, and no client reads them.
//
// A hash is not a token — that is the whole point of storing one — but a reader
// who holds the subtree holds the set of every token that was admitted, and can
// tell which of two candidate tokens is in it. So the subtree is denied at
// every level, and the document carries no claim check anywhere near it.
func TestWorkspaceRulesDenyEveryRunTokenTheTokenRecord(t *testing.T) {
	tree := workspaceTree(t)
	tokens, ok := tree[WorkspaceTokenPath].(map[string]any)
	if !ok {
		t.Fatalf("the document carries no %s node: %v", WorkspaceTokenPath, keys(tree))
	}
	if tokens[".read"] != false || tokens[".write"] != false {
		t.Errorf("/%s does not deny every client (read %v, write %v): the admitted-token hashes are written by the workspace's own service and read by nobody with a client token", WorkspaceTokenPath, tokens[".read"], tokens[".write"])
	}
	if hash, ok := tokens["$hash"].(map[string]any); !ok {
		t.Errorf("%s carries no per-token node, so the hashes themselves are denied by their parent alone: %v", WorkspaceTokenPath, keys(tokens))
	} else if hash[".read"] != false || hash[".write"] != false {
		t.Errorf("/%s/$hash does not deny every client (read %v, write %v)", WorkspaceTokenPath, hash[".read"], hash[".write"])
	}
	// The meter and the rollup are W7's, and they are the workspace's money:
	// they are shut here for the same reason, because the SHAPE of the database
	// is what this task ships — a later task adds a writer, not a reader.
	for _, path := range []string{WorkspaceMeterPath, WorkspaceRollupPath} {
		node, ok := tree[path].(map[string]any)
		if !ok {
			t.Fatalf("the document carries no %s node: %v", path, keys(tree))
		}
		if node[".read"] != false || node[".write"] != false {
			t.Errorf("/%s does not deny every client (read %v, write %v)", path, node[".read"], node[".write"])
		}
	}
	walk(map[string]any{"tokens": tokens}, []string{}, func(path []string, attrs map[string]any) {
		for _, attr := range []string{".read", ".write"} {
			if v, ok := attrs[attr]; ok && v != false {
				t.Errorf("the token subtree grants a client something at /%s (%s %v)", strings.Join(path, "/"), attr, v)
			}
		}
	})
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

// TestWorkspaceRulesAllowTheServicesOwnAccount is the other half of the two
// tests above, and it is stated as a pair on purpose: the rules admit NOBODY,
// and the one identity that reaches the workspace's own database is the
// workspace's own service, over IAM.
//
// IAM on a Realtime Database is whole-database and it bypasses the rules (spec
// §8: "IAM on a Realtime Database is whole-database, and it bypasses the
// rules"), so the service's access cannot be a rule — and it must not be one,
// because a rule naming the service would be a rule naming an identity a client
// can be. What this test holds is that the two halves stay apart: the document
// grants no client anything (so no client token reaches the service's reach),
// and the root binds the database role to the workspace's own service account
// and to no other principal, so the whole-database reach is that account's and
// the run accounts, the members' accounts and the project's own members hold
// nothing on it.
func TestWorkspaceRulesAllowTheServicesOwnAccount(t *testing.T) {
	// The document's half: no client token opens anything, so a caller reaches
	// this database as the service or not at all.
	walk(workspaceTree(t), nil, func(path []string, attrs map[string]any) {
		for _, attr := range []string{".read", ".write"} {
			if v, ok := attrs[attr]; ok && v != false {
				t.Errorf("/%s %s is %v: the workspace's own service is admitted by IAM, which bypasses these rules, and every client is denied", strings.Join(path, "/"), attr, v)
			}
		}
	})

	// The root's half: exactly one database grant, bound to the workspace's own
	// service account, and that account holds nothing else.
	blocks := workspaceResourceBlocks(t, workspaceRootText(t))
	const accountRef = "google_service_account.workspace.email"
	grants, naming := 0, 0
	for _, b := range blocks {
		if !strings.HasSuffix(b.Type, "_iam_member") {
			continue
		}
		grants++
		if !strings.Contains(b.Body, workspaceDatabaseRole) {
			t.Errorf("%s.%s binds a role that is not the workspace database role (%s): every grant this root makes on the workspace's own database is that role's", b.Type, b.Name, workspaceDatabaseRole)
		}
		if !strings.Contains(b.Body, accountRef) {
			t.Errorf("%s.%s does not bind %s: the workspace's own service account is the one identity that reaches the workspace database, and a grant to anything else is a principal that can read the record and the admitted-token hashes", b.Type, b.Name, accountRef)
		}
	}
	if grants == 0 {
		t.Fatal("the workspace root binds the database role to nobody: the workspace's own service has no way to read the record it rolls up and admits against")
	}
	for _, b := range blocks {
		if !strings.Contains(b.Body, accountRef) {
			continue
		}
		naming++
		if !strings.HasSuffix(b.Type, "_iam_member") {
			t.Errorf("%s.%s names the workspace's own service account outside a member resource: its reach is the one database grant", b.Type, b.Name)
		}
	}
	if naming == 0 {
		t.Error("no resource binds the workspace's own service account: the account the root creates has no grant at all, so the root and the document disagree about who reaches the database")
	}
}

// TestWorkspaceRulesVersionIsPinnedAndMigrated is the marker's own pattern, and
// it is what makes this document safe to ship: the version is written after the
// rules deploy and read before the next one, so an installation whose document
// came from a NEWER Fugaro refuses this binary's deploy instead of half-
// applying — writing an older document under a newer marker, which is the state
// the doctor warns about and nobody can repair from the older binary.
//
// A marker BELOW this Fugaro's (or absent: generation 0) is the upgrade and not
// a refusal: it is an installation whose rules predate this document, and the
// next `fugaro update` deploys this binary's own document and records this
// generation.
func TestWorkspaceRulesVersionIsPinnedAndMigrated(t *testing.T) {
	if got, want := PathWorkspaceRulesMarker(), "config/limits/workspaceRules"; got != want {
		t.Fatalf("the workspace rules marker moved to %q, and its path is pinned: %q is where a released Fugaro reads and writes it, and a moved path reads as 'no marker' on every installation that already has one", got, want)
	}
	if !strings.HasPrefix(PathWorkspaceRulesMarker(), "config/limits/") {
		t.Fatalf("the marker %q left config/limits: every reader of it holds its credential over IAM, which is what makes a node nothing writes through the rules readable at all", PathWorkspaceRulesMarker())
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
					t.Fatalf("deploying generation %d over a marker at generation %d was admitted: the newer installation's document would be replaced by this binary's older one, and the marker would then record the older number", WorkspaceRulesVersion, tc.deployed)
				}
				for _, want := range []string{"fugaro update", "upgrade", selfCommandWord} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q, so the operator reads what happened without reading what to do: %v", want, err)
					}
				}
			}
		})
	}
	// The marker itself is a node of the workspace's own database, and no
	// client reads it either: the deploy writes it as the operator, over IAM.
	limits, ok := workspaceTree(t)["config"].(map[string]any)["limits"].(map[string]any)
	if !ok {
		t.Fatalf("the document carries no config/limits node, which is where the marker lives: %v", keys(workspaceTree(t)))
	}
	if limits[".read"] != false || limits[".write"] != false {
		t.Errorf("config/limits does not deny every client (read %v, write %v): the marker is written and read over IAM", limits[".read"], limits[".write"])
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
