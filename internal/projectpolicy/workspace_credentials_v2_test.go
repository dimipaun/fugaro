package projectpolicy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// v2NodeDoc is a node as a reader in the database meets it: the writer's own
// bytes read back as JSON, so a test can change one key the way a hand edit of
// the published node would. The writer refuses a document it would not read
// (WorkspaceCredentialsNode calls Validate), so a test that wants a bad node has
// to build it from a good one this way.
func v2NodeDoc(t *testing.T, w *WorkspaceCredentials) map[string]any {
	t.Helper()
	raw, err := WorkspaceCredentialsNode(w)
	if err != nil {
		t.Fatalf("WorkspaceCredentialsNode: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the node is not JSON: %v", err)
	}
	return doc
}

// v2Decode reads an edited document the way the service reads the published
// node, and fails the test if the document is not JSON at all.
func v2Decode(t *testing.T, doc map[string]any) (*WorkspaceCredentials, bool, error) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return DecodeWorkspaceCredentials(raw)
}

func v2Row(doc map[string]any, i int) map[string]any {
	return doc["members"].([]any)[i].(map[string]any)
}

func v2Shared(doc map[string]any, i int) map[string]any {
	return doc["shared"].([]any)[i].(map[string]any)
}

// v2Entries is a row's job_account_repos as the document carries it.
func v2Entries(row map[string]any) []any {
	entries, _ := row["job_account_repos"].([]any)
	return entries
}

// v2ReposOf is the repositories of the entry naming the row's live account.
func v2ReposOf(row map[string]any) []any {
	entries := v2Entries(row)
	if len(entries) == 0 {
		return nil
	}
	repos, _ := entries[0].(map[string]any)["repos"].([]any)
	return repos
}

// TestNodeVersionTwoValidates: version 2 of the workspace credentials node is
// the shape this Fugaro writes and the shape the workspace's service reads
// (R6, R7, U2). Two columns are version 2's, and the test is about both of
// them: what a member's row says about which repositories each job account is a
// job of, and what a pool entry says about the kind of credential it is and the
// repositories a forge credential covers.
//
// **The reader is asked, not the writer.** Every refusal below is a hand edit
// of the published document and is read with DecodeWorkspaceCredentials, because
// the node the service reads is the one in the database and not the one the
// owner's CLI built — the writer's own refusals are the other half and are
// asserted at the end.
//
// **A repository is concrete, and that is the point of the column** (F42). A
// version-1 row named accounts and no repositories at all, so nothing held an
// account to the repository a run presenting it was on; a pattern is refused
// where it is written for the reason the member's own `allow_repos` list is
// asked on the intersection instead (R5): a pattern is a declaration about
// repositories that do not exist yet and this column is the owner's statement of
// the jobs they declared.
func TestNodeVersionTwoValidates(t *testing.T) {
	doc := v2NodeDoc(t, credentialsFixture())

	// The columns version 2 declares are the ones actually written: a name a
	// reader could not find in the document is a column no other task can rely
	// on, and every one of them is read back by the reader below.
	if got := doc["version"]; got != float64(WorkspaceCredentialsVersion) {
		t.Errorf("version = %v, want %d: the node this Fugaro writes is version %d", got, WorkspaceCredentialsVersion, WorkspaceCredentialsVersion)
	}
	row := v2Row(doc, 0)
	if got, want := len(v2Entries(row)), len(row["job_accounts"].([]any)); got != want {
		t.Errorf("members[0] carries %d job_account_repos for %d job_accounts: every account of the row carries the repositories it is a job of", got, want)
	}
	if got, want := v2ReposOf(row), []any{"github/owner/repo-a"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("members[0].job_account_repos[0].repos = %v, want %v", got, want)
	}
	entry := v2Entries(row)[0].(map[string]any)
	if got, want := entry["account"], row["job_accounts"].([]any)[0]; got != want {
		t.Errorf("job_account_repos[0].account = %v, want the account job_accounts[0] names (%v)", got, want)
	}
	if got := v2Shared(doc, 0)["kind"]; got != SharedKindModel {
		t.Errorf("shared[0].kind = %v, want %q: a pool entry says which kind of credential it is", got, SharedKindModel)
	}

	// A forge entry is the other half of U2: the kind is one a repository's
	// git.provider may be, and the entry names the concrete repositories its
	// credential is stored for. It is round-tripped through the document so the
	// columns are the writer's and the reader's together.
	forge := credentialsFixture()
	forge.Shared[0] = WorkspaceShared{
		Name:       "bitbucket-token-team",
		Kind:       "bitbucket",
		AllMembers: true,
		Covers:     []string{"bitbucket/acme/app", "bitbucket/acme/other"},
	}
	raw, err := WorkspaceCredentialsNode(forge)
	if err != nil {
		t.Fatalf("WorkspaceCredentialsNode of a forge entry: %v", err)
	}
	got, found, err := DecodeWorkspaceCredentials(raw)
	if err != nil || !found {
		t.Fatalf("DecodeWorkspaceCredentials of a forge entry = (%v, %v), want the node it was written from", found, err)
	}
	want := forge.Shared[0]
	gotEntry := got.Shared[0]
	if gotEntry.Kind != want.Kind || gotEntry.Name != want.Name {
		t.Errorf("the forge entry decoded to %+v, want %+v", gotEntry, want)
	}
	if !gotEntry.AllMembers {
		t.Error("a forge entry offered to every member decoded with all_members false")
	}
	if strings.Join(gotEntry.Covers, ",") != strings.Join(want.Covers, ",") {
		t.Errorf("the forge entry's covers decoded to %v, want %v", gotEntry.Covers, want.Covers)
	}

	cases := []struct {
		name  string
		key   string // the key the refusal has to name
		value string // a value the refusal must not repeat ("" when it has none)
		mut   func(doc map[string]any)
	}{
		{"a row whose repositories are a pattern", "job_account_repos", "github/owner/*", func(doc map[string]any) {
			row := v2Row(doc, 0)
			row["job_account_repos"] = []any{map[string]any{"account": row["job_accounts"].([]any)[0], "repos": []any{"github/owner/*"}}}
		}},
		{"a row whose repositories are the bare pattern", "job_account_repos", "*", func(doc map[string]any) {
			row := v2Row(doc, 0)
			row["job_account_repos"] = []any{map[string]any{"account": row["job_accounts"].([]any)[0], "repos": []any{"*"}}}
		}},
		{"a row whose repositories are not sorted", "job_account_repos", "", func(doc map[string]any) {
			row := v2Row(doc, 0)
			row["job_account_repos"] = []any{map[string]any{"account": row["job_accounts"].([]any)[0], "repos": []any{"github/owner/repo-b", "github/owner/repo-a"}}}
		}},
		{"a row that names one repository twice", "job_account_repos", "", func(doc map[string]any) {
			row := v2Row(doc, 0)
			row["job_account_repos"] = []any{map[string]any{"account": row["job_accounts"].([]any)[0], "repos": []any{"github/owner/repo-a", "github/owner/repo-a"}}}
		}},
		{"a row that names a repository of a provider a repository cannot be", "job_account_repos", "gitlab/acme/app", func(doc map[string]any) {
			row := v2Row(doc, 0)
			row["job_account_repos"] = []any{map[string]any{"account": row["job_accounts"].([]any)[0], "repos": []any{"gitlab/acme/app"}}}
		}},
		{"a row that spells a repository as this Fugaro does not write it", "job_account_repos", "github/Owner/Repo-A", func(doc map[string]any) {
			// Canonical is the spelling of every allow-list and of this column:
			// a repository written in another case is the same repository with
			// a second spelling, and two spellings of one repository are two
			// entries the workspace would have to compare by hand.
			row := v2Row(doc, 0)
			row["job_account_repos"] = []any{map[string]any{"account": row["job_accounts"].([]any)[0], "repos": []any{"github/Owner/Repo-A"}}}
		}},
		{"a row whose repositories name another account", "job_account_repos", "fugaro-job-9@project-a.iam.example.com", func(doc map[string]any) {
			row := v2Row(doc, 0)
			row["job_account_repos"] = []any{map[string]any{
				"account": "fugaro-job-9@project-a.iam.example.com",
				"repos":   []any{"github/owner/repo-a"},
			}}
		}},
		{"a row that declares some of its accounts' repositories", "job_account_repos", "", func(doc map[string]any) {
			row := v2Row(doc, 0)
			row["job_accounts"] = []any{row["job_accounts"].([]any)[0], "fugaro-job-2@project-a.iam.example.com"}
		}},
		{"an unknown key inside a job account's entry", "a_column_no_reader_knows", "marker-value", func(doc map[string]any) {
			v2Entries(v2Row(doc, 0))[0].(map[string]any)["a_column_no_reader_knows"] = "marker-value"
		}},
		{"a shared entry that names no kind", "kind", "", func(doc map[string]any) {
			delete(v2Shared(doc, 0), "kind")
		}},
		{"a shared entry whose kind is not a kind", "kind", "oauth", func(doc map[string]any) {
			v2Shared(doc, 0)["kind"] = "oauth"
		}},
		{"a model entry that covers repositories", "covers", "", func(doc map[string]any) {
			v2Shared(doc, 0)["covers"] = []any{"github/owner/repo-a"}
		}},
		{"a forge entry that covers no repository", "covers", "", func(doc map[string]any) {
			v2Shared(doc, 0)["kind"] = "bitbucket"
		}},
		{"a forge entry whose cover is a pattern", "covers", "github/owner/*", func(doc map[string]any) {
			s := v2Shared(doc, 0)
			s["kind"] = "github"
			s["covers"] = []any{"github/owner/*"}
		}},
		{"a forge entry whose cover is another provider's", "covers", "bitbucket/acme/app", func(doc map[string]any) {
			// The kind IS the first segment of every repository the entry
			// covers, rather than a second vocabulary the two could disagree
			// in: a GitHub entry covering a Bitbucket repository is an entry
			// whose kind and whose coverage name two different credentials.
			s := v2Shared(doc, 0)
			s["kind"] = "github"
			s["covers"] = []any{"bitbucket/acme/app"}
		}},
		{"a forge entry whose covers are not sorted", "covers", "", func(doc map[string]any) {
			s := v2Shared(doc, 0)
			s["kind"] = "github"
			s["covers"] = []any{"github/owner/repo-b", "github/owner/repo-a"}
		}},
		{"a forge entry that covers one repository twice", "covers", "", func(doc map[string]any) {
			s := v2Shared(doc, 0)
			s["kind"] = "github"
			s["covers"] = []any{"github/owner/repo-a", "github/owner/repo-a"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := v2NodeDoc(t, credentialsFixture())
			tc.mut(doc)
			got, found, err := v2Decode(t, doc)
			if err == nil || !found || got != nil {
				t.Fatalf("DecodeWorkspaceCredentials with %s = (%v, %v, %v), want a refusal", tc.name, got, found, err)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the refusal %q does not name %s: an operator has to be told which column is not one this Fugaro writes", err, tc.key)
			}
			if tc.value != "" && strings.Contains(err.Error(), tc.value) {
				t.Errorf("the refusal %q repeats the value it refused: a row is another installation's configuration and no refusal carries it", err)
			}
			if strings.Contains(err.Error(), "marker-value") {
				t.Errorf("the refusal %q repeats a value it refused", err)
			}
		})
	}

	// And the writer refuses each of them too, so a node the owner's CLI builds
	// is refused where it is built rather than at the service that reads it.
	for _, tc := range []struct {
		name string
		mut  func(w *WorkspaceCredentials)
	}{
		{"a pattern", func(w *WorkspaceCredentials) {
			w.Members[0].JobAccountRepos[0].Repos = []string{"github/owner/*"}
		}},
		{"a row that declares some of its accounts' repositories", func(w *WorkspaceCredentials) {
			w.Members[0].JobAccounts = append(w.Members[0].JobAccounts, "fugaro-job-2@project-a.iam.example.com")
		}},
		{"a shared entry whose kind is not a kind", func(w *WorkspaceCredentials) {
			w.Shared[0].Kind = "oauth"
		}},
		{"a model entry that covers repositories", func(w *WorkspaceCredentials) {
			w.Shared[0].Covers = []string{"github/owner/repo-a"}
		}},
		{"a forge entry that covers no repository", func(w *WorkspaceCredentials) {
			w.Shared[0].Kind = "bitbucket"
		}},
	} {
		t.Run("the writer refuses "+tc.name, func(t *testing.T) {
			w := credentialsFixture()
			tc.mut(w)
			if _, err := WorkspaceCredentialsNode(w); err == nil {
				t.Fatalf("WorkspaceCredentialsNode accepted %s: the reader would refuse the node it wrote", tc.name)
			}
		})
	}
}

// TestAllMembersExcludesProjects: `all_members` and `projects` are one
// declaration written two ways — the members a shared credential is offered to,
// or every member of the workspace — and so exactly one of them is written
// (R7). A node carrying both is refused rather than read as one of the two,
// because the reader that picked would be the reader that decided which
// members a credential is offerable to, and that is the workspace owner's
// decision.
//
// **This is C3's red step.** With the exclusivity rule absent, the node below is
// accepted, and the assertion in the last block is what fails.
func TestAllMembersExcludesProjects(t *testing.T) {
	// Both states are the node's, and each is written as itself: an entry that
	// names its members carries `projects` and no `all_members` at all (the
	// key is omitted, so a reader cannot find a false beside a list and has to
	// read the list), and an entry offered to every member carries
	// `all_members` and no `projects`.
	for _, tc := range []struct {
		name      string
		entry     WorkspaceShared
		wantAll   bool
		wantKey   string
		absentKey string
	}{
		{
			name:      "the member projects it names",
			entry:     WorkspaceShared{Name: "claude-oauth-token-team", Kind: SharedKindModel, Projects: []string{"project-a"}},
			wantAll:   false,
			wantKey:   "projects",
			absentKey: "all_members",
		},
		{
			name:      "every member of the workspace",
			entry:     WorkspaceShared{Name: "claude-oauth-token-team", Kind: SharedKindModel, AllMembers: true},
			wantAll:   true,
			wantKey:   "all_members",
			absentKey: "projects",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := credentialsFixture()
			w.Shared[0] = tc.entry
			doc := v2NodeDoc(t, w)
			shared := v2Shared(doc, 0)
			if _, ok := shared[tc.wantKey]; !ok {
				t.Errorf("the entry carries no %s: the writer states the members the credential is offered to one way or the other, never by saying nothing", tc.wantKey)
			}
			if _, ok := shared[tc.absentKey]; ok {
				t.Errorf("the entry carries %s beside %s: the two are exclusive, so the writer writes one of them", tc.absentKey, tc.wantKey)
			}
			got, found, err := v2Decode(t, doc)
			if err != nil || !found {
				t.Fatalf("DecodeWorkspaceCredentials = (%v, %v), want the node it was written from", found, err)
			}
			if got.Shared[0].AllMembers != tc.wantAll {
				t.Errorf("the entry decoded with all_members = %v, want %v", got.Shared[0].AllMembers, tc.wantAll)
			}
		})
	}

	doc := v2NodeDoc(t, credentialsFixture())
	shared := v2Shared(doc, 0)
	shared["all_members"] = true // beside the projects the fixture's entry names
	got, found, err := v2Decode(t, doc)
	if err == nil || !found || got != nil {
		t.Fatalf("DecodeWorkspaceCredentials with all_members beside projects = (%v, %v, %v), want a refusal: a reader handed both would have to invent which one the workspace owner meant", got, found, err)
	}
	for _, key := range []string{"all_members", "projects"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the refusal %q does not name %s: the operator needs both keys to know which one to remove", err, key)
		}
	}

	// The writer refuses it too: a node the owner's CLI builds is refused where
	// it is built, and never reaches the database for the service to read.
	w := credentialsFixture()
	w.Shared[0].AllMembers = true
	if _, err := WorkspaceCredentialsNode(w); err == nil {
		t.Fatal("WorkspaceCredentialsNode accepted all_members beside projects")
	}
}

// TestVersionOneStillDecodes: a workspace whose owner has not run `fugaro
// update` holds the version-1 node the release before this one wrote, and the
// service has to be able to place that workspace's callers after the release —
// so version 1 is read, and it is read as itself. What it does not say is not
// invented: an account of a version-1 row carries an empty repository list
// (a reader that filled one in would publish a declaration nobody made, and the
// scope refuses such an account's runs with the reason that names the owner's
// update, R19), and a version-1 pool entry is the model credential the pool held
// before version 2, which is the only kind it could have held (F15).
func TestVersionOneStillDecodes(t *testing.T) {
	v1 := []byte(`{
	  "version": 1,
	  "name": "workspace-alpha",
	  "members": [
	    {
	      "project": "project-a",
	      "firebase_project": "project-a",
	      "database_region": "",
	      "fp": "project-a",
	      "job_accounts": ["fugaro-job-1@project-a.iam.example.com"],
	      "exchange_url": "https://exchange.project-a.example.com"
	    }
	  ],
	  "shared": [{"name": "claude-oauth-token-team", "projects": ["project-a"]}]
	}`)
	got, found, err := DecodeWorkspaceCredentials(v1)
	if err != nil || !found {
		t.Fatalf("DecodeWorkspaceCredentials of a version-1 node = (%v, %v), want the node read", found, err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1: the node says which version it is and the reader reads that version", got.Version)
	}
	if got.UnknownKeys != nil {
		t.Errorf("UnknownKeys = %v for a version-1 node, want none: every key of it is one this Fugaro writes", got.UnknownKeys)
	}
	if len(got.Members) != 1 || len(got.Members[0].JobAccounts) != 1 {
		t.Fatalf("the version-1 row decoded to %+v, want the one member and its one account", got.Members)
	}
	if got.Members[0].JobAccountRepos != nil {
		t.Errorf("a version-1 row decoded to %d repository lists, want none: version 1 never said which repositories an account is a job of, and a reader that invented them would serve a declaration the owner never made", len(got.Members[0].JobAccountRepos))
	}
	entry := got.Shared[0]
	if entry.Kind != SharedKindModel {
		t.Errorf("a version-1 pool entry decoded with kind %q, want %q: the pool held model credentials and nothing else before version 2 (F15)", entry.Kind, SharedKindModel)
	}
	if entry.AllMembers {
		t.Error("a version-1 pool entry decoded with all_members true: version 1 had no way to say it, so the entry is offered to the projects it names")
	}
	if len(entry.Covers) != 0 {
		t.Errorf("a version-1 pool entry decoded with covers %v: version 1 never said which repositories a credential reaches", entry.Covers)
	}

	// The version is what picks the row reader, so a version-2 column under
	// version 1 is refused rather than ignored: a document carrying one is a
	// document a hand edited (or a writer that forgot to move the version), and
	// reading it as version 1 would drop the very declaration the column makes
	// — a member's repositories, or the kind of a credential the service has to
	// know before it may serve it.
	var doc map[string]any
	if err := json.Unmarshal(v1, &doc); err != nil {
		t.Fatalf("the version-1 node is not JSON: %v", err)
	}
	v2Row(doc, 0)["job_account_repos"] = []any{map[string]any{
		"account": "fugaro-job-1@project-a.iam.example.com",
		"repos":   []any{"github/owner/repo-a"},
	}}
	decoded, found, err := v2Decode(t, doc)
	if err == nil || !found || decoded != nil {
		t.Fatalf("DecodeWorkspaceCredentials of a version-1 node carrying job_account_repos = (%v, %v, %v), want a refusal", decoded, found, err)
	}
	if !strings.Contains(err.Error(), "job_account_repos") {
		t.Errorf("the refusal %q does not name job_account_repos: the operator needs to be told which key a version-1 row does not carry", err)
	}

	// The same, one level out: a version-1 pool entry that carries a kind is a
	// document whose writer and whose version disagree.
	if err := json.Unmarshal(v1, &doc); err != nil {
		t.Fatalf("the version-1 node is not JSON: %v", err)
	}
	v2Shared(doc, 0)["kind"] = SharedKindModel
	decoded, found, err = v2Decode(t, doc)
	if err == nil || !found || decoded != nil {
		t.Fatalf("DecodeWorkspaceCredentials of a version-1 entry carrying a kind = (%v, %v, %v), want a refusal", decoded, found, err)
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("the refusal %q does not name kind: the operator needs to be told which key a version-1 pool entry does not carry", err)
	}
}

// v2Schema compiles schemas/workspace-credentials.schema.json under its own $id.
func v2Schema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-credentials.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/workspace-credentials.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// v2Validate validates a node's bytes against the compiled schema.
func v2Validate(t *testing.T, sch *jsonschema.Schema, raw []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

// TestNodeVersionTwoSchema: schemas/workspace-credentials.schema.json describes
// the node the reader reads. C3's acceptance is that the schema is published,
// and a schema is published for the two readers who complete a document from it
// rather than decode it: an editor's completion and a reviewer's eye. A key one
// side knows and the other does not is a document that reads as valid in one
// place and is refused in the other, so the node this Fugaro writes is held to
// the schema here, and the schema's own closure is held to the reader's.
func TestNodeVersionTwoSchema(t *testing.T) {
	sch := v2Schema(t)

	// Both states of a pool entry, and a forge entry with its coverage: the
	// documents the writer produces are documents the schema admits.
	entryStates := []*WorkspaceCredentials{credentialsFixture(), credentialsFixture()}
	entryStates[0].Shared[0] = WorkspaceShared{Name: "claude-oauth-token-team", Kind: SharedKindModel, AllMembers: true}
	entryStates[1].Shared[0] = WorkspaceShared{
		Name: "bitbucket-token-team", Kind: "bitbucket", Projects: []string{"project-a"},
		Covers: []string{"bitbucket/acme/app"},
	}
	for i, w := range entryStates {
		raw, err := WorkspaceCredentialsNode(w)
		if err != nil {
			t.Fatalf("WorkspaceCredentialsNode: %v", err)
		}
		if err := v2Validate(t, sch, raw); err != nil {
			t.Errorf("schemas/workspace-credentials.schema.json refuses the node the writer produces (%d): %v\n%s", i, err, raw)
		}
	}

	// An entry that carries both is not the shape, in the schema as in the
	// reader: the two keys are exclusive (R7).
	doc := v2NodeDoc(t, credentialsFixture())
	v2Shared(doc, 0)["all_members"] = true
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := v2Validate(t, sch, raw); err == nil {
		t.Error("schemas/workspace-credentials.schema.json admits all_members beside projects: the reader refuses the pair (R7), and a schema that admitted it would complete exactly the node that is refused")
	}
	// An entry that carries neither is not the shape either: a written shared
	// credential is offered to the members it names or to every member.
	doc = v2NodeDoc(t, credentialsFixture())
	delete(v2Shared(doc, 0), "projects")
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := v2Validate(t, sch, raw); err == nil {
		t.Error("schemas/workspace-credentials.schema.json admits a shared entry that names no project and no all_members: the reader refuses the half-written declaration")
	}
	// And a pattern is not a repository, in the schema as in the reader (F42).
	doc = v2NodeDoc(t, credentialsFixture())
	row := v2Row(doc, 0)
	row["job_account_repos"] = []any{map[string]any{"account": row["job_accounts"].([]any)[0], "repos": []any{"github/owner/*"}}}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := v2Validate(t, sch, raw); err == nil {
		t.Error("schemas/workspace-credentials.schema.json admits a pattern where a concrete repository is written: the reader refuses it (F42)")
	}

	// The schema's closure is the reader's: every object below the top level is
	// closed, because a key inside a row is a field this Fugaro does not write
	// and the decoder refuses it, and the top level is NOT closed, because a
	// node a newer Fugaro wrote is tolerated there (P8).
	var schemaDoc map[string]any
	schemaRaw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-credentials.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(schemaRaw, &schemaDoc); err != nil {
		t.Fatal(err)
	}
	if open, ok := schemaDoc["additionalProperties"].(bool); ok && !open {
		t.Error("schemas/workspace-credentials.schema.json closes the top level, and the reader tolerates an unknown key there (UnknownKeys, P8)")
	}
	for _, at := range [][]string{
		{"$defs", "member"},
		{"$defs", "shared"},
		{"$defs", "job_account"},
	} {
		var cur any = schemaDoc
		for _, k := range at {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("schemas/workspace-credentials.schema.json has no %s", strings.Join(at, "."))
			}
			cur = m[k]
		}
		def, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("schemas/workspace-credentials.schema.json has no %s", strings.Join(at, "."))
		}
		if v, ok := def["additionalProperties"].(bool); !ok || v {
			t.Errorf("schemas/workspace-credentials.schema.json's %s.additionalProperties is %v, and the reader refuses an unknown key inside a row", strings.Join(at, "."), def["additionalProperties"])
		}
	}
}
