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

// TestWorkspaceCredentialsRefusesARowItCannotResolve: the two shape rules a
// member's row is held to, and each is a row the workspace's service would
// otherwise carry and never be able to use.
//
// **The account address.** The service resolves a caller by folding the token's
// `email` and looking it up (foldEmail): an address with no `@`, one with
// whitespace in it, or one that is not printable ASCII is an address the fold
// refuses, so a row naming one is a member whose jobs are all refused for a
// reason nothing in the row explains. The rule is a shape check and not an IAM
// check — whether the account exists is the platform's answer — which is why it
// lives with the node and not with the service.
//
// **The exchange URL.** The audience a member-bound assertion carries is that
// URL (spec.md §3), and the endpoint matches it exactly: a URL with a query or a
// fragment, one that is not https, or one that names no host admits nobody, and
// a member whose every sign-in is refused reads as a broken exchange rather than
// as the misconfiguration it is. Both refusals name the FIELD and the index and
// never what the row wrote: a row is another installation's configuration.
func TestWorkspaceCredentialsRefusesARowItCannotResolve(t *testing.T) {
	cases := []struct {
		name  string
		field string // the key the refusal has to name
		value string
		mut   func(m *WorkspaceCredentialMember)
	}{
		{"an account address with no @", "job_accounts", "fugaro-job-1", func(m *WorkspaceCredentialMember) {
			m.JobAccounts = []string{"fugaro-job-1"}
		}},
		{"an account address with whitespace in it", "job_accounts", "fugaro-job-1@project-a. iam.example.com", func(m *WorkspaceCredentialMember) {
			m.JobAccounts = []string{"fugaro-job-1@project-a. iam.example.com"}
		}},
		{"an account address that is not printable ASCII", "job_accounts", "fugaro-job-1@pröject-a.example.com", func(m *WorkspaceCredentialMember) {
			m.JobAccounts = []string{"fugaro-job-1@pröject-a.example.com"}
		}},
		{"an empty account address", "job_accounts", "", func(m *WorkspaceCredentialMember) {
			m.JobAccounts = []string{""}
		}},
		{"a second account that is not an address", "job_accounts", "not-an-address", func(m *WorkspaceCredentialMember) {
			// The FIRST account is a good one: the rule is asked of every entry
			// of the list and not of its first, which is the shape a member with
			// one bad account among several has.
			m.JobAccounts = []string{"fugaro-job-1@project-a.iam.example.com", "not-an-address"}
		}},
		{"an exchange URL that is not https", "exchange_url", "http://exchange.project-a.example.com", func(m *WorkspaceCredentialMember) {
			m.ExchangeURL = "http://exchange.project-a.example.com"
		}},
		{"an exchange URL that names no host", "exchange_url", "https://", func(m *WorkspaceCredentialMember) {
			m.ExchangeURL = "https://"
		}},
		{"an exchange URL whose host is written as a path", "exchange_url", "https:///exchange", func(m *WorkspaceCredentialMember) {
			m.ExchangeURL = "https:///exchange"
		}},
		{"an exchange URL with a query", "exchange_url", "https://exchange.project-a.example.com/?a=1", func(m *WorkspaceCredentialMember) {
			m.ExchangeURL = "https://exchange.project-a.example.com/?a=1"
		}},
		{"an exchange URL with a fragment", "exchange_url", "https://exchange.project-a.example.com/#a", func(m *WorkspaceCredentialMember) {
			m.ExchangeURL = "https://exchange.project-a.example.com/#a"
		}},
		{"an empty exchange URL", "exchange_url", "", func(m *WorkspaceCredentialMember) {
			m.ExchangeURL = ""
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := credentialsFixture()
			tc.mut(&w.Members[0])
			_, err := WorkspaceCredentialsNode(w)
			if err == nil {
				t.Fatalf("WorkspaceCredentialsNode with %s = a node, want a refusal: the service could never resolve the row", tc.name)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("the refusal %q does not name the field %s: an operator has to be told which part of the row is not one this Fugaro writes", err, tc.field)
			}
			if tc.value != "" && strings.Contains(err.Error(), tc.value) {
				t.Errorf("the refusal %q repeats the value it refused: a row is another installation's configuration and no refusal carries it", err)
			}
			// And the refusal is the reader's too: a node hand-edited in the
			// database is read by the same rules the publisher applied, so the
			// row that could not be built cannot be read either.
			doc := map[string]any{
				"version": WorkspaceCredentialsVersion,
				"name":    w.Name,
				"members": []any{map[string]any{
					"project":          w.Members[0].Project,
					"firebase_project": w.Members[0].FirebaseProject,
					"fp":               w.Members[0].FP,
					"job_accounts":     w.Members[0].JobAccounts,
					"exchange_url":     w.Members[0].ExchangeURL,
				}},
				"shared": []any{},
			}
			edited, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if got, found, err := DecodeWorkspaceCredentials(edited); err == nil || !found || got != nil {
				t.Errorf("DecodeWorkspaceCredentials with %s = (%v, %v, %v), want a refusal", tc.name, got, found, err)
			}
		})
	}
	// The fixture's own two rows are the shape the rules admit, so the refusals
	// above are the shape and not the fixture.
	if _, err := WorkspaceCredentialsNode(credentialsFixture()); err != nil {
		t.Fatalf("WorkspaceCredentialsNode of the fixture: %v", err)
	}
}

// TestWorkspaceCredentialsRefusesOneFirebaseProjectInTwoRows: the member's
// database is DERIVED from the row's firebase_project (finding 6), so two rows
// naming one Firebase project are two members whose policy, ledger and outcomes
// are read from one database — the splice the derivation exists to make
// unrepresentable, arrived at by a duplicated field rather than by a
// `database_url`. The plan's property is that "two members' accounts can never
// be pointed at one database", and the derivation alone does not hold it: a
// table that names one Firebase project twice derives one URL twice.
//
// It is refused exactly as the duplicate account and the duplicate project name
// are, and the refusal names no value: a row is another installation's
// configuration, so it names the row and not what the hand wrote in it.
func TestWorkspaceCredentialsRefusesOneFirebaseProjectInTwoRows(t *testing.T) {
	w := credentialsFixture()
	// Both rows still name their own Project and FP — only the Firebase project
	// the database is derived from is the same, which is the shape the other two
	// checks cannot see.
	w.Members[1].FirebaseProject = w.Members[0].FirebaseProject
	if _, err := WorkspaceCredentialsNode(w); err == nil {
		t.Fatal("WorkspaceCredentialsNode accepted one Firebase project in two rows: the two members would read one database")
	} else if strings.Contains(err.Error(), w.Members[1].FirebaseProject) {
		t.Errorf("the refusal %q names the Firebase project it refused: a row is another installation's configuration and a refusal names the row", err)
	}
	// The unmodified table is still a node this Fugaro publishes, so the refusal
	// above is the duplication and not the fixture.
	if _, err := WorkspaceCredentialsNode(credentialsFixture()); err != nil {
		t.Fatalf("WorkspaceCredentialsNode of the fixture: %v", err)
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
// workspace invents, and both directions of the difference are refusals: a
// credential the member's policy does not name is refused, and a policy that
// could not be read is refused rather than read as one that admits everything.
//
// The repository legs of the member's own intersection are NOT asked here, and
// the test says so as a fact: MemberAdmitsShared takes no repository, because
// the repository a run is on reaches the workspace only as the run identity's
// sanitised slug and a check over an input a service cannot verify is not a
// check.
func TestWorkspaceMemberPolicyAdmitsOnlyWhatItNames(t *testing.T) {
	p := &Policy{
		AllowRepos: []string{"github/owner/repo-a"},
		Accounts:   map[string]Account{"claude-oauth-token-team": {AllowRepos: []string{"github/owner/repo-a"}}},
	}
	if !MemberAdmitsShared(p, "claude-oauth-token-team") {
		t.Error("MemberAdmitsShared refused a credential the member's own policy names")
	}
	if MemberAdmitsShared(p, "claude-oauth-token-other") {
		t.Error("MemberAdmitsShared admitted a credential the member's own policy does not name: a workspace cannot admit what the member's policy never declared")
	}
	if MemberAdmitsShared(nil, "claude-oauth-token-team") {
		t.Error("MemberAdmitsShared admitted a credential with no policy at all: a policy that could not be read is not one that admits everything")
	}
	empty := &Policy{}
	if MemberAdmitsShared(empty, "claude-oauth-token-team") {
		t.Error("MemberAdmitsShared admitted a credential to a policy whose accounts block is empty")
	}
}
