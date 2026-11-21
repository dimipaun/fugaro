package projectpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// Version 2 of the workspace's credentials node (task C3 of
// docs/workspace-service/implementation-plan.md, decisions R6 and R7,
// docs/workspace-service/contract-node.md). This file is the columns version 2
// adds, the reader that reads its rows and the rules only it has; the value
// both versions decode into, `Validate` and the reader's dispatch are in
// workspace_credentials.go.
//
// **Why a version at all.** Version 1 says how the workspace reaches each
// member — the member's job service accounts, its Firebase project, the region
// its database is in, its `fp` and its exchange URL — and which member projects
// each credential of the pool is offered to. Two things it does not say, and
// both are things the service cannot do without:
//
//   - **Which repositories a job account is a job of** (R6, D2992). The service
//     resolves a caller by folding the email of the run's verified identity and
//     looking the account up in the member's row. A shared credential's call is
//     then served for the repository the run is on — and a version-1 row names
//     no repository, so the only thing that could hold the run to one is the
//     member's own policy. The account that presents an identity is not held to
//     the repository it is a job of at all, which is the half of the
//     workspaces design's O3 that F42 found missing: the shared job's
//     `allow_repos` is a list of *patterns*, and the workspace compares the
//     concrete slug the run identity carries.
//   - **What kind of credential a pool entry is** (U2, D2978, F15). The pool
//     was model-only; version 2's pool holds a Bitbucket token and a GitHub
//     credential too, and the entry has to say which it is before the service
//     can pick the route that may serve it, and which repositories a forge
//     credential reaches.
//
// **A pattern is refused, and that is the point of the column.** A repository
// is written in this node the one way it is written in a policy and an
// allow-list — canonical, `<provider>/<owner>/<name>` (`Canonical`) — and a
// pattern is not that spelling: `bitbucket/acme/*` and a bare `*` are refused
// where they are written, by the same grammar that refuses them in an
// allow-list. The reason is F42's: a pattern is a declaration about
// repositories that do not exist yet, checked by the workspace against a
// member's live profile only when a run arrives, and the workspace's list is
// meant to be the owner's own statement of the jobs they declared. What the
// member's own policy admits is still asked, on the intersection (R5); this
// column is what the run is held to *before* that.
//
// **A version-1 node is still read** (the acceptance of C3), and its value is
// the honest one rather than a filled-in guess: an account of a version-1 row
// carries an empty repository list, because version 1 never said which
// repositories it is a job of and a reader that invented them would publish a
// declaration nobody made for the service to serve from. A scope reading such a
// row refuses every repository for that account, with the refusal that names
// the workspace owner's `fugaro update` (R19) — which is also the state of a
// member the owner has added a job account for without declaring its
// repositories yet, so the two read the same and are refused the same way.
//
// **The version moves with a migration and a guard, and neither is here.** The
// node's writer (WS4) carries a published version-1 node forward in place
// (internal/projectpolicy/workspace_credentials_migrate.go) and publishes
// version 2 only after its own deploy of the service that reads it succeeded in
// the same update (R7): the service that reads a node is never older than the
// CLI that wrote it. This file knows nothing about when a node is written —
// only about what version 2 is.

// SharedKindModel is the kind of a pool entry that is a model credential: the
// only kind a version-1 pool could hold (F15), and what a version-1 shared
// entry is read as.
//
// **A forge entry's kind is a kind a repository's `git.provider` may be**
// (config.Providers), and not a second vocabulary. The kind is the first
// segment of every canonical repository of that forge, so a forge entry's
// kind and the provider segment of the repositories it covers are one string:
// nothing here maps one spelling onto another, and a third forge is a kind in
// the config's own list rather than an entry in a table of this file's.
const SharedKindModel = "model"

// WorkspaceJobAccount is one job service account of a member's row and the
// concrete repositories it is a job of (R6, D2992): the identities the
// workspace's service resolves a caller by, each held to the repositories a run
// presenting it may be on.
//
// **It is a list of its own, and `job_accounts` is not retyped.** A field's
// meaning never changes (CLAUDE.md): `job_accounts` is the list of addresses a
// version-1 node carries and it keeps exactly that meaning, so version 2 adds
// the repositories beside it rather than turning an address into an object. The
// two lists are one declaration read in one order, and Validate holds them
// together: the entry at position i names the account `job_accounts[i]` names.
// A row whose halves disagreed would be a caller whose repositories depend on
// which half the reader looked at — the reasoning the row's `project`/`fp` pair
// is read by.
type WorkspaceJobAccount struct {
	// Account is the account's address, exactly as job_accounts carries it.
	Account string
	// Repos are the concrete repositories this account is a job of, canonical
	// ("github/acme/app"), sorted and each once. **One for a repository's own
	// job, several for a shared project job** (the workspaces design's O3 and
	// O4, D2992): a shared job account is a job of every repository the job it
	// belongs to serves, and this list is what the scope holds the run's
	// verified repository to. A pattern is not one of them, and Validate
	// refuses it where it is written.
	//
	// An empty list is a state rather than a mistake: it says the account is a
	// job of no repository this owner has declared, which is what a version-1
	// row decodes to and what a member whose owner has not declared its
	// repositories yet carries. A run on such a member's account is refused by
	// the scope with the reason that names the owner's `fugaro update` (R19),
	// never served.
	Repos []string
}

// workspaceCredentialsNodeV2 is the node as this Fugaro writes it, version 2:
// the shape the JSON of `workspace_credentials/<name>` has, and the shape this
// file's reader reads. It is a struct with json tags rather than a map, for the
// workspace node's own reason — the encoding is one order and one spelling
// every time, so a publish can compare a node against the bytes it would write.
type workspaceCredentialsNodeV2 struct {
	Version int                               `json:"version"`
	Name    string                            `json:"name"`
	Members []workspaceCredentialMemberNodeV2 `json:"members"`
	Shared  []workspaceSharedNodeV2           `json:"shared"`
}

type workspaceCredentialMemberNodeV2 struct {
	Project         string                    `json:"project"`
	FirebaseProject string                    `json:"firebase_project"`
	DatabaseRegion  string                    `json:"database_region"`
	FP              string                    `json:"fp"`
	JobAccounts     []string                  `json:"job_accounts"`
	JobAccountRepos []workspaceJobAccountNode `json:"job_account_repos"`
	ExchangeURL     string                    `json:"exchange_url"`
}

// workspaceJobAccountNode is one entry of a version-2 member row's
// job_account_repos: an account and its repositories, written as an object
// rather than as two parallel lists, so the two cannot be read apart.
type workspaceJobAccountNode struct {
	Account string   `json:"account"`
	Repos   []string `json:"repos"`
}

// workspaceSharedNodeV2 is one entry of the pool. `all_members` and `projects`
// are exclusive, so a version-2 entry that offers the credential to every
// member writes no projects key at all, and one that names projects writes no
// all_members key: the two are written by the same encoder, which is why
// `projects` and `covers` are omitted when they are empty rather than written
// as empty lists — an omitted key and an empty list would be two spellings of
// the same refusal.
type workspaceSharedNodeV2 struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	AllMembers bool     `json:"all_members,omitempty"`
	Projects   []string `json:"projects,omitempty"`
	Covers     []string `json:"covers,omitempty"`
}

// The keys one row of a version-2 node may carry. They are longer than version
// 1's: a member's row names the repositories of each of its job accounts, and a
// shared entry names the kind of credential it is, the all_members key and the
// repositories a forge entry covers.
var (
	workspaceCredentialsMemberKeysV2 = []string{"project", "firebase_project", "database_region", "fp", "job_accounts", "job_account_repos", "exchange_url"}
	workspaceCredentialsSharedKeysV2 = []string{"name", "kind", "all_members", "projects", "covers"}
	// workspaceJobAccountKeys are the keys one entry of job_account_repos may
	// carry. An entry is an object written inside a row, so the row's closure
	// reaches into it (checkJobAccountEntries).
	workspaceJobAccountKeys = []string{"account", "repos"}
)

// workspaceCredentialsRowsV2 is version 2's row shapes, for the strict reader:
// the keys each kind of row may carry, and what such a row names, for the
// refusal an unknown key inside one is made with. The sentences are what an
// operator is told the shape is, so they name every column — a version-2 row
// whose repositories (or kind, or covers) a refusal did not mention would send
// its reader to the version-1 document for the answer.
var workspaceCredentialsRowsV2 = workspaceCredentialsRows{
	members:    workspaceCredentialsMemberKeysV2,
	shared:     workspaceCredentialsSharedKeysV2,
	memberWhat: "a member's row names the member's project, its Firebase project, the region its database is in when it is not the default one, its fp, its job service accounts, the concrete repositories each of those accounts is a job of, and its exchange URL, and nothing else — the database the service reads is derived from the verified iss and no bucket is bound",
	sharedWhat: "a shared entry names the credential, the kind of credential it is, the member projects it is offered to or all_members, and the repositories a forge entry covers, and nothing else — the credential's value is nowhere in the node (rule 2 of spec.md §4)",
}

// decodeWorkspaceCredentialsV2 reads a version-2 node's shape. It is the
// reader's first half and Validate is its second: this function reads what the
// node says, and Validate decides whether a node may say it (a row whose
// repositories do not match its accounts, a shared entry that offers itself to
// no one, a cover that is a pattern).
func decodeWorkspaceCredentialsV2(raw []byte) (*WorkspaceCredentials, error) {
	var n workspaceCredentialsNodeV2
	if err := decodeStrictRows(raw, &n, workspaceCredentialsRowsV2); err != nil {
		return nil, err
	}
	if err := checkJobAccountEntries(raw); err != nil {
		return nil, err
	}
	w := &WorkspaceCredentials{Version: n.Version, Name: n.Name}
	for _, m := range n.Members {
		accounts := make([]string, 0, len(m.JobAccounts))
		accounts = append(accounts, m.JobAccounts...)
		repos := make([]WorkspaceJobAccount, 0, len(m.JobAccountRepos))
		for _, e := range m.JobAccountRepos {
			entryRepos := make([]string, 0, len(e.Repos))
			entryRepos = append(entryRepos, e.Repos...)
			repos = append(repos, WorkspaceJobAccount{Account: e.Account, Repos: entryRepos})
		}
		w.Members = append(w.Members, WorkspaceCredentialMember{
			Project: m.Project, FirebaseProject: m.FirebaseProject, DatabaseRegion: m.DatabaseRegion, FP: m.FP,
			JobAccounts: accounts, JobAccountRepos: repos, ExchangeURL: m.ExchangeURL,
		})
	}
	for _, s := range n.Shared {
		projects := make([]string, 0, len(s.Projects))
		projects = append(projects, s.Projects...)
		// covers is copied the way it arrived: an entry with no covers is an
		// entry with no list, not an entry with an empty one, so a node read
		// and written again is the same node.
		covers := append([]string(nil), s.Covers...)
		w.Shared = append(w.Shared, WorkspaceShared{
			Name: s.Name, Kind: s.Kind, AllMembers: s.AllMembers, Projects: projects, Covers: covers,
		})
	}
	return w, nil
}

// checkJobAccountEntries refuses an unknown key inside one of a member row's
// job_account_repos entries. The row reader closes a row's own keys, and an
// entry of job_account_repos is an object written inside one: a key beside
// `account` and `repos` is a field this Fugaro does not write, in the very place
// a member's repositories are declared, and a reader that ignored it would be
// the quiet acceptance the row's own closure exists to refuse.
//
// An entry's keys are held to the node's own case rule (checkMemberNames):
// encoding/json folds a member name by Unicode simple case folding before it
// matches it, so an entry that spelled `Repos` would be read as `repos` whoever
// wrote it.
func checkJobAccountEntries(raw []byte) error {
	var doc struct {
		Members []struct {
			JobAccountRepos []json.RawMessage `json:"job_account_repos"`
		} `json:"members"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	for _, m := range doc.Members {
		for _, e := range m.JobAccountRepos {
			names, err := objectMemberNames(e)
			if err != nil {
				return err
			}
			if err := checkMemberNames("a job account's entry of the workspace credentials node", names, workspaceJobAccountKeys); err != nil {
				return err
			}
			for _, n := range names {
				if !slices.Contains(workspaceJobAccountKeys, n) {
					return fmt.Errorf("it carries the key %q, which this Fugaro does not write in a job account's entry: an entry of job_account_repos names the account and the concrete repositories it is a job of, and nothing else", n)
				}
			}
		}
	}
	return nil
}

// validateJobAccountRepos holds a row's job accounts and their repositories to
// being one declaration: the repositories of `job_accounts[i]` are
// `job_account_repos[i].Repos`, each of them a concrete canonical repository.
//
// **An empty list of lists is a state, a half-written one is not.** A row that
// declares no repositories at all is a version-1 row (its own reader never
// invents them) or a member whose owner has not declared them yet, and the
// scope refuses its runs with the reason that names the owner's update (R19).
// A row that declares SOME of them is neither: it is a declaration whose
// reading would depend on which half a reader believed, so it is refused here
// rather than half-read.
func validateJobAccountRepos(i int, m WorkspaceCredentialMember) error {
	if len(m.JobAccountRepos) == 0 {
		return nil
	}
	if len(m.JobAccountRepos) != len(m.JobAccounts) {
		return fmt.Errorf("members[%d].job_account_repos names %d of the row's job service accounts: every account of the row carries the repositories it is a job of, in the row's own account order, so that an account's repositories are the ones declared beside it", i, len(m.JobAccountRepos))
	}
	for j, e := range m.JobAccountRepos {
		if e.Account != m.JobAccounts[j] {
			return fmt.Errorf("members[%d].job_account_repos[%d] does not name the account members[%d].job_accounts[%d] names: the two lists are one declaration written in one order, and a row whose halves disagreed is a caller whose repositories depend on which half the reader looked at", i, j, i, j)
		}
		if !slices.IsSorted(e.Repos) {
			return fmt.Errorf("members[%d].job_account_repos[%d].repos is not sorted", i, j)
		}
		for k, r := range e.Repos {
			if !concreteRepo(r) {
				return fmt.Errorf("members[%d].job_account_repos[%d].repos[%d] is not a concrete repository: a repository is written %s, and a pattern is not one — the workspace owner declares the repositories the account is a job of, and the member's own policy is what decides whether a run of one of them may be served (R5, F42)", i, j, k, repoSpelling)
			}
			if k > 0 && e.Repos[k-1] == r {
				return fmt.Errorf("members[%d].job_account_repos[%d].repos[%d] names a repository twice", i, j, k)
			}
		}
	}
	return nil
}

// validateSharedEntryV2 holds one pool entry to version 2's rules: it is
// offered to the members it names or to every member and never to both, it says
// which kind of credential it is, and a forge entry names the concrete
// repositories it covers and a model entry names none.
func validateSharedEntryV2(i int, s WorkspaceShared) error {
	if s.AllMembers && len(s.Projects) > 0 {
		return fmt.Errorf("shared[%d] carries all_members beside projects: the two are exclusive — an entry is offered to the member projects it names, or to every member of the workspace — and a reader handed both would have to invent which one the owner meant", i)
	}
	if len(s.Projects) == 0 && !s.AllMembers {
		return fmt.Errorf("shared[%d] offers %q to no project: a shared credential with an empty allow-list is a half-written declaration, and writing the projects out — or all_members, to offer it to every member of the workspace — is the only way to say who may be served it", i, s.Name)
	}
	if s.Kind != SharedKindModel && !forgeKind(s.Kind) {
		return fmt.Errorf("shared[%d] does not name the kind of the credential it offers: a pool entry is a model credential (kind %q) or a forge credential, whose kind is one a repository's git.provider may be (config.Providers), and the kind is what tells the service which route may serve the entry", i, SharedKindModel)
	}
	// Which of the two a forge entry is, and whether a model entry covers
	// anything at all, is asked before the covers themselves are read: an entry
	// whose kind and whose coverage disagree is refused for the disagreement
	// ("a model credential reaches no repository", "a forge credential with an
	// empty coverage is half-written") rather than for the first cover that
	// fails to be the entry's own kind, which would name the symptom.
	if s.Kind == SharedKindModel && len(s.Covers) > 0 {
		return fmt.Errorf("shared[%d] covers repositories and is a model credential: a model credential is served on the model routes and reaches no repository of its own — the repositories a model call may be made for are the member's own policy's business, and a pool entry that named them would be a second, weaker answer to the question the member's policy already answers", i)
	}
	if s.Kind != SharedKindModel && len(s.Covers) == 0 {
		return fmt.Errorf("shared[%d] is a %s credential and covers no repository: a forge entry with an empty coverage is a half-written declaration, and naming the repositories the workspace owner's credential is stored for is the only way to say which runs may be served it", i, s.Kind)
	}
	if !slices.IsSorted(s.Covers) {
		return fmt.Errorf("shared[%d].covers is not sorted", i)
	}
	for j, c := range s.Covers {
		if !concreteRepo(c) || !strings.HasPrefix(c, s.Kind+"/") {
			return fmt.Errorf("shared[%d].covers[%d] is not a concrete repository of the entry's own kind: a forge entry covers repositories written %s, whose first segment is the entry's kind, and a pattern is not one — the coverage is the workspace owner's list of the repositories their own credential is stored for (U2, F42)", i, j, repoSpelling)
		}
		if j > 0 && s.Covers[j-1] == c {
			return fmt.Errorf("shared[%d].covers[%d] names a repository twice", i, j)
		}
	}
	return nil
}

// forgeKind reports whether a kind is a forge kind: one a repository's
// git.provider may be. It reads the config's own list rather than spelling the
// kinds out, which is what keeps a third forge a kind in that list and not a
// change in this file (the list is held to the runner's own by
// internal/config's TestProviderKindsAgreeWithTheConfigsList).
func forgeKind(kind string) bool { return slices.Contains(config.Providers, kind) }

// repoSpelling is the one spelling of a concrete repository in a refusal: it is
// the form Canonical produces and the form every allow-list and the repository
// child of a policy are written in, so an operator reads the same shape in the
// refusal as in the node they are editing.
//
// It is written without the provider kinds themselves: which kinds exist is
// config.Providers' answer (forgeKind), and a sentence that spelled them would
// be a second list.
const repoSpelling = "<provider>/<owner>/<name>, with a provider segment a repository's git.provider may be"

// concreteRepo reports whether one entry of the node is a concrete canonical
// repository and not a pattern: it is the spelling Canonical produces, its
// provider segment is one a repository's git.provider may be (config.Providers,
// which ValidPattern uses for the same reasoning), and nothing in it is a
// wildcard — a "*" is not a segment character of task.CanonicalRepo's grammar,
// which is what refuses "bitbucket/acme/*" and the bare "*" without a second
// rule here.
func concreteRepo(s string) bool {
	provider, rest, ok := strings.Cut(s, "/")
	if !ok || !forgeKind(provider) {
		return false
	}
	c, err := Canonical(provider, rest)
	return err == nil && c == s
}

// workspaceCredentialsNodeVersion reads the version off a node without decoding
// its rows, so the reader can hand the document to the version's own row reader
// (and refuse a version no release wrote before it reads anything else).
//
// A node with no version key at all is version 0, which no release wrote: a
// version-1 node was written by the encoder beside this file, which always
// writes the key, so an absent one is a hand edit and is refused as a version
// this reader does not read rather than being guessed at. A version that is not
// an integer, or is below zero, is refused the same way and for the same
// reason.
func workspaceCredentialsNodeVersion(raw []byte) (int, error) {
	var doc struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, errors.New("its version is not an integer")
	}
	if doc.Version == nil {
		return 0, nil
	}
	if *doc.Version < 0 {
		return 0, fmt.Errorf("its version is %d, and no release ever wrote a workspace credentials node below 0", *doc.Version)
	}
	return *doc.Version, nil
}
