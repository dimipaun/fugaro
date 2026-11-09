package projectpolicy

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// credentialsFixture is one workspace credentials node: two members and one
// shared credential, with no real address and no value anywhere (the folder's
// rule: projects are project-a and project-b, the addresses are at
// example.com).
func credentialsFixture() *WorkspaceCredentials {
	return &WorkspaceCredentials{
		Name: "workspace-alpha",
		Members: []WorkspaceCredentialMember{
			{
				Project: "project-a", FirebaseProject: "project-a", FP: "project-a",
				JobAccounts: []string{"fugaro-job-1@project-a.iam.example.com"},
				ExchangeURL: "https://exchange.project-a.example.com",
			},
			{
				Project: "project-b", FirebaseProject: "project-b", FP: "project-b",
				JobAccounts: []string{"fugaro-job-1@project-b.iam.example.com"},
				ExchangeURL: "https://exchange.project-b.example.com",
			},
		},
		Shared: []WorkspaceShared{
			{Name: "claude-oauth-token-team", Projects: []string{"project-a", "project-b"}},
		},
	}
}

// TestWorkspaceCredentialsNodeRoundTrips: the node this Fugaro writes is the
// node this Fugaro reads, member by member. It is the whole of what the
// workspace's service resolves a caller by, so a field the encoder drops is a
// member the service can never place.
func TestWorkspaceCredentialsNodeRoundTrips(t *testing.T) {
	want := credentialsFixture()
	want.Version = WorkspaceCredentialsVersion
	raw, err := WorkspaceCredentialsNode(want)
	if err != nil {
		t.Fatalf("WorkspaceCredentialsNode: %v", err)
	}
	got, found, err := DecodeWorkspaceCredentials(raw)
	if err != nil || !found {
		t.Fatalf("DecodeWorkspaceCredentials = (%v, %v), want the node it was written from", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the node round-tripped to\n%+v\nwant\n%+v", got, want)
	}
	if got.UnknownKeys != nil {
		t.Errorf("UnknownKeys = %v for a node this Fugaro wrote, want none", got.UnknownKeys)
	}
	// The path is the workspace's name and nothing else: a reader that built a
	// path from a member's row would read one node where there are several.
	if got, want := PathWorkspaceCredentials("workspace-alpha"), "workspace_credentials/workspace-alpha"; got != want {
		t.Errorf("PathWorkspaceCredentials = %q, want %q", got, want)
	}
}

// TestWorkspaceCredentialsRowRefusesAnUnknownKey: a member's row is a
// security-bearing declaration and its fields are exactly the ones spec §5
// names. The splice the post-merge review closed (finding 6) was an owner
// declaring member B's accounts beside member A's database URL, and the shape
// refuses it twice over: the database is derived from the verified `iss`, and a
// row that carries a database_url at all is a node this Fugaro did not write.
//
// The refusal names the KEY and never its value: a row is another
// installation's configuration, and the package's rule is that no refusal
// repeats what a hand wrote.
func TestWorkspaceCredentialsRowRefusesAnUnknownKey(t *testing.T) {
	raw, err := WorkspaceCredentialsNode(credentialsFixture())
	if err != nil {
		t.Fatalf("WorkspaceCredentialsNode: %v", err)
	}
	rows := []struct {
		name  string
		key   string
		value any
	}{
		{"a database URL", "database_url", "https://project-a-default-rtdb.firebaseio.com"},
		{"a runs bucket", "runs_bucket", "marker-bucket"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("the node is not JSON: %v", err)
			}
			row := doc["members"].([]any)[0].(map[string]any)
			row[tc.key] = tc.value
			edited, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, found, err := DecodeWorkspaceCredentials(edited)
			if err == nil || !found || got != nil {
				t.Fatalf("DecodeWorkspaceCredentials with %s in a row = (%v, %v, %v), want a refusal", tc.key, got, found, err)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the refusal %q does not name the field %q: an operator has to be told which key is not one this Fugaro writes", err, tc.key)
			}
			if strings.Contains(err.Error(), "marker-bucket") || strings.Contains(err.Error(), "firebaseio.com") {
				t.Errorf("the refusal %q repeats the value it refused: a row is another installation's configuration and no refusal carries it", err)
			}
		})
	}
}

// TestWorkspaceCredentialsRefusesOneAccountInTwoRows: one job service account
// belongs to one member. The service resolves a caller by looking the verified
// email up in this table, so an address two rows name would be a caller whose
// member — and therefore whose database, policy and exchange — depends on which
// row the lookup happened to read first.
func TestWorkspaceCredentialsRefusesOneAccountInTwoRows(t *testing.T) {
	w := credentialsFixture()
	w.Members[1].JobAccounts = []string{"FUGARO-JOB-1@PROJECT-A.IAM.EXAMPLE.COM"}
	if _, err := WorkspaceCredentialsNode(w); err == nil {
		t.Fatal("WorkspaceCredentialsNode accepted one account in two rows: an address two members claim is a caller whose member is decided by the lookup order")
	}
}

// TestWorkspaceCredentialsRefusesAProjectAndFpThatDisagree: the row's Project
// is the namespace the shared pool's allow-lists use and its FP is what the
// run identity's `fp` claim must be. The two are compared rather than one read
// twice because a row whose halves disagreed would admit a token minted for one
// member as another member's run — the pins have to name the member the account
// names (spec.md §4).
func TestWorkspaceCredentialsRefusesAProjectAndFpThatDisagree(t *testing.T) {
	w := credentialsFixture()
	w.Members[0].FP = "project-b"
	if _, err := WorkspaceCredentialsNode(w); err == nil {
		t.Fatal("WorkspaceCredentialsNode accepted a row whose project and fp disagree: the account's member and the fp's member have to be the same member")
	}
}

// TestWorkspaceCredentialsRefusesASharedCredentialWithNoProject: writing a
// shared credential and listing no project for it is a half-written
// declaration, refused rather than read as "everybody" (D1298's refused option
// (B)) or as "nobody" (a list that hides the mistake).
func TestWorkspaceCredentialsRefusesASharedCredentialWithNoProject(t *testing.T) {
	w := credentialsFixture()
	w.Shared[0].Projects = nil
	if _, err := WorkspaceCredentialsNode(w); err == nil {
		t.Fatal("WorkspaceCredentialsNode accepted a shared credential with an empty allow-list")
	}
	// And the projects are held to the project grammar: owner/repo-a is a
	// repository and not a project, and an allow-list written in repositories
	// would name members that do not exist.
	w = credentialsFixture()
	w.Shared[0].Projects = []string{"owner/repo-a"}
	if _, err := WorkspaceCredentialsNode(w); err == nil {
		t.Fatal("WorkspaceCredentialsNode accepted a repository name where the allow-list takes a member project")
	}
}

// TestWorkspaceCredentialsToleratesAnUnknownTopLevelKey: the top-level shape is
// read tolerantly, so a node a newer Fugaro wrote does not brick an older
// reader (P8) — and the keys are listed, because "tolerated" and "silently
// dropped" look the same in the node afterwards.
func TestWorkspaceCredentialsToleratesAnUnknownTopLevelKey(t *testing.T) {
	raw, err := WorkspaceCredentialsNode(credentialsFixture())
	if err != nil {
		t.Fatalf("WorkspaceCredentialsNode: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the node is not JSON: %v", err)
	}
	doc["a_key_a_newer_fugaro_writes"] = "marker-value"
	edited, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, found, err := DecodeWorkspaceCredentials(edited)
	if err != nil || !found {
		t.Fatalf("DecodeWorkspaceCredentials = (%v, %v), want the node with the key tolerated", found, err)
	}
	if !reflect.DeepEqual(got.UnknownKeys, []string{"a_key_a_newer_fugaro_writes"}) {
		t.Errorf("UnknownKeys = %v, want the key a newer Fugaro writes", got.UnknownKeys)
	}
}

// TestWorkspaceCredentialsRefusesAnotherVersion: a node written by a newer
// Fugaro is refused with the version's own error rather than half-read. The
// version is this node's and not the record's: the two documents are read by
// different readers and a bump of one says nothing about the other.
func TestWorkspaceCredentialsRefusesAnotherVersion(t *testing.T) {
	raw, err := WorkspaceCredentialsNode(credentialsFixture())
	if err != nil {
		t.Fatalf("WorkspaceCredentialsNode: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the node is not JSON: %v", err)
	}
	doc["version"] = WorkspaceCredentialsVersion + 1
	edited, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, found, err := DecodeWorkspaceCredentials(edited)
	var ver *WorkspaceVersionError
	if !errors.As(err, &ver) || !found {
		t.Fatalf("DecodeWorkspaceCredentials of a newer node = (%v, %v), want a *WorkspaceVersionError", found, err)
	}
}

// TestWorkspaceCredentialsNodeIsAbsentNotRefused: a node that is not there at
// all — the state before the first publish — is found false and no error, so a
// reader can tell "this workspace has not published its credentials" from "this
// workspace published something this Fugaro cannot read". The second is a
// refusal and the first is a state, and reading one as the other is how a
// service comes to serve nothing while looking healthy.
func TestWorkspaceCredentialsNodeIsAbsentNotRefused(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("null")} {
		got, found, err := DecodeWorkspaceCredentials(raw)
		if err != nil || found || got != nil {
			t.Errorf("DecodeWorkspaceCredentials(%q) = (%v, %v, %v), want (nil, false, nil)", raw, got, found, err)
		}
	}
}

// TestWorkspaceMemberPolicyAdmitsOnlyWhatItNames: the member side of D1298's
// intersection, read from the member's own published policy. It is the member's
// existing vocabulary — the accounts block `fugaro project policy publish`
// derives from the member's own claude_accounts — rather than a key the
// workspace invents, and every leg of it refuses alone: a credential the
// member's admin never declared, an account not allowed to this repository, a
// repository the project does not admit, and a policy that could not be read are
// each a refusal the member made.
func TestWorkspaceMemberPolicyAdmitsOnlyWhatItNames(t *testing.T) {
	const repo = "github/owner/repo-a"
	p := &Policy{
		AllowRepos: []string{repo},
		Accounts:   map[string]Account{"claude-oauth-token-team": {AllowRepos: []string{repo}}},
	}
	if !MemberAdmitsShared(p, "claude-oauth-token-team", repo) {
		t.Error("MemberAdmitsShared refused a credential the member's own policy names for this repository")
	}
	if MemberAdmitsShared(p, "claude-oauth-token-other", repo) {
		t.Error("MemberAdmitsShared admitted a credential the member's own policy does not name: a workspace cannot admit what the member's policy never declared")
	}
	if MemberAdmitsShared(p, "claude-oauth-token-team", "github/owner/repo-b") {
		t.Error("MemberAdmitsShared admitted the credential for a repository the account's own allow-list does not name")
	}
	unadmitted := &Policy{
		AllowRepos: []string{"github/owner/repo-b"},
		Accounts:   map[string]Account{"claude-oauth-token-team": {AllowRepos: []string{repo}}},
	}
	if MemberAdmitsShared(unadmitted, "claude-oauth-token-team", repo) {
		t.Error("MemberAdmitsShared admitted a credential in a project whose own allow_repos does not admit the repository")
	}
	if MemberAdmitsShared(nil, "claude-oauth-token-team", repo) {
		t.Error("MemberAdmitsShared admitted a credential with no policy at all: a policy that could not be read is not one that admits everything")
	}
}
