package projectpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// The workspace's credentials node: `workspace_credentials/<name>` in the
// workspace's OWN database (docs/workspaces/spec.md §4, D1297/D1298), read by
// the workspace's own credentials service and written by the workspace owner's
// installation.
//
// **It is a node of its own, and not a part of `workspaces/<name>`.** W2's
// record is the workspace's statement about the group — its members and its cap
// and nothing else — and it is published to the workspace's database where the
// owner's own CLI and the workspace's service read it. What it deliberately
// does not carry is the per-member table the service resolves a caller by:
// that table is the owner's *declaration about how the workspace reaches each
// member* (the member's job service accounts, its Firebase project and the
// region of that project's database when it is not the default one, its Fugaro
// project and its exchange URL, spec.md §5), it is many times the size of the
// record, and mixing the two would put the identity of every member's jobs in
// the document a person reads to see who is in the group. So the table lives
// here, in one node, and the record stays what §2 says it is.
//
// **What the node may not carry, and why the shape is strict about it.** There
// is no database URL and no runs bucket in a member's row. The database the
// service reads a member's policy, ledger and outcomes from is **derived from
// the Firebase project the member's verified `iss` names** (finding 6 of the
// post-merge security review, spec.md §4), so an owner cannot pair member B's
// accounts with member A's — possibly looser — policy: the declared values and
// the verified `iss` must name one member, and the database follows the `iss`.
// The bucket grant is gone (O1). Both facts are held by the *decoder* rather
// than stated in a comment: a row is decoded with unknown fields refused, so a
// `database_url` key an owner copies in from an older design is a refusal that
// names the field and never a value the service would go on to read.
//
// **Every credential of the workspace's pool carries its per-project
// allow-list here** (D1298). The allow-list is the owner's declaration like the
// rest of the node, and the effective set is that list intersected with the
// member's own policy (internal/creds's Shared and AdmitShared): the node says
// which projects a credential is offered to, and the member's own published
// policy says whether the member takes it.
//
// **The name is the workspace's**, which is the project name of the
// installation that publishes it, exactly as the record's is: the two nodes are
// written by one installation into one database and a workspace whose two
// documents named different workspaces would be a workspace with no answer to
// "which one is it".
const pathWorkspaceCredentials = "workspace_credentials"

// WorkspaceCredentialsVersion is the version of the workspace's credentials
// node this Fugaro writes. It is a version of its own, for the reason
// WorkspaceVersion is: the nodes are read by different readers (this one by the
// workspace's service, the record by the owner's CLI and the service's member
// list) and a bump of one says nothing about the other.
//
// **It is 2 because the columns are new ones**: a member row's job accounts each
// carry the concrete repositories they are a job of, and a pool entry carries
// the kind of credential it is and, for a forge entry, the repositories it
// covers (R6, R7, U2 — the shape and its reasoning are in
// workspace_credentials_v2.go). Version 1 is still read by this reader, because
// a workspace whose owner has not run its `fugaro update` yet holds one, and
// what it decodes to is what it says: no repositories for an account and the
// model kind for a pool entry.
const WorkspaceCredentialsVersion = 2

// PathWorkspaceCredentials is one workspace's credentials node, relative to the
// workspace's own database root and without a leading slash. The caller passes
// the raw name, which is validated long before a path is built (the node's own
// reader holds it to config.ProjectNameRE, and every project name is already a
// legal path segment).
func PathWorkspaceCredentials(name string) string {
	return pathWorkspaceCredentials + "/" + seg("name", name)
}

// PathWorkspaceCredentialsParent is the parent of the credentials nodes,
// without a trailing slash. It exists for the same reader PathWorkspaces does:
// the workspace's own service reads one node and the owner's CLI may list them,
// and no member holds a grant on the workspace's project at all.
func PathWorkspaceCredentialsParent() string { return pathWorkspaceCredentials }

// WorkspaceCredentials is the workspace's credentials node decoded: the
// per-member table and the shared pool's allow-lists.
type WorkspaceCredentials struct {
	// Version is the node's version. It is WorkspaceCredentialsVersion for
	// every node this Fugaro wrote, and 1 for a node the release before it
	// wrote, which DecodeWorkspaceCredentials still reads (the repositories of
	// a job account are version 2's column and a version-1 row declares none).
	// A version this reader does not understand is refused rather than
	// half-read (P8).
	Version int
	// Name is the workspace's name, which is the project name of the
	// installation that publishes it and the last segment of its path.
	Name string
	// Members are the workspace's members, in project-name order and each
	// once: how the workspace's service reaches each of them.
	Members []WorkspaceCredentialMember
	// Shared are the credentials of the workspace's pool with the projects each
	// is offered to, in name order and each once (D1298).
	Shared []WorkspaceShared
	// UnknownKeys lists the top-level keys of the node this Fugaro does not
	// write, sorted. Decode fills it in and never writes it back: the keys are
	// tolerated so a node a newer Fugaro wrote does not brick an older reader
	// (P8).
	//
	// The list is what a writer has to read, and the node has one:
	// WorkspaceCredentialsNode is the encoder and it holds the value to
	// Validate alone, which never looks at this list, so a caller that decoded
	// a node and re-encoded it would drop these keys silently. The publish stage
	// refuses that instead — internal/cli/update_workspace.go reads this list
	// off the decoded node and refuses the rewrite, naming the keys and
	// `fugaro update`, because dropping a key nobody modelled and tolerating it
	// look the same in the node afterwards. (The workspace's own record and the
	// policy node's migration refuse a rewrite the same way.)
	UnknownKeys []string
}

// WorkspaceCredentialMember is one member's row: how the workspace's service
// resolves a caller of that member and pins the tokens it presents.
type WorkspaceCredentialMember struct {
	// Project is the member's Fugaro project name — the namespace both nodes
	// speak, and the name the shared pool's allow-lists use.
	Project string
	// FirebaseProject is the member's Firebase project id: the `aud` of a run
	// identity minted there, and the half of the pair `aud`/`iss` that tells
	// one member's tokens from another's. It is also the fact the member's
	// database is derived from, and one Firebase project belongs to one member:
	// Validate refuses a second row naming it, because two rows naming one would
	// read one member's policy, ledger and outcomes for both.
	FirebaseProject string
	// DatabaseRegion is the region of the member's Realtime Database, and it is
	// empty for a database in the project's default region. A workspace's member
	// may be an adopted project whose database was created in a region, and the
	// tree names that database `<firebase_project>-default-rtdb.<region>.firebasedatabase.app`
	// rather than the legacy `<firebase_project>-default-rtdb.firebaseio.com`
	// (internal/infra's CheckFirebaseOutputsFor, which admits exactly those two
	// shapes and nothing else). The project id still decides WHOSE database it
	// is — finding 6's derivation, which is what the run identity's `iss` is
	// held to — and this field only says where in that project it lives, so a
	// row naming another project's region is a URL that project does not have,
	// read and refused as such rather than served.
	DatabaseRegion string
	// FP is the member's Fugaro project name as the `fp` claim carries it. It
	// is a field of its own rather than a second read of Project, because the
	// claim is what the check is made against and the two are compared: a row
	// whose two halves disagreed would admit a member's token as another
	// member's run.
	FP string
	// JobAccounts are the member's job service accounts, by email: the
	// identities the workspace's service resolves a caller by. Each is an IAM
	// service account address, and one account belongs to one member of one
	// workspace: an address two rows name would be a caller whose member
	// depends on which row the lookup happened to read.
	JobAccounts []string
	// JobAccountRepos are the concrete repositories those accounts are jobs
	// of, one entry per account and in the same order, adding version 2's
	// column to the addresses above (R6, D2992; WorkspaceJobAccount carries the
	// reasoning, and a pattern is refused). It is empty for a row that declares
	// none — which is what a version-1 node's row decodes to, and what the
	// scope refuses a run on with the reason that names the owner's update
	// (R19) — and a list that declares some of them is refused by Validate.
	JobAccountRepos []WorkspaceJobAccount
	// ExchangeURL is the member's exchange endpoint, where the workspace's
	// signer mints a member-bound assertion for (spec.md §3, D1293). It is the
	// owner's declaration of where the member is reached, and it is not a
	// credential: nothing signs with it and no token is verified against it.
	ExchangeURL string
}

// WorkspaceShared is one credential of the workspace's pool, the kind it is and
// the reach it is offered with: D1298's per-project allow-list, and version 2's
// columns beside it.
type WorkspaceShared struct {
	// Name is the credential's name, which is the name the bundle's entry and
	// the cascade both speak. It is a name and never a value: no node of a
	// database carries a credential (rule 2 of spec.md §4).
	Name string
	// Kind is the kind of credential this entry is: SharedKindModel, or a kind
	// a repository's git.provider may be (config.Providers) for a forge entry
	// — which is also the first segment of every repository Covers names (U2,
	// D2978, F15). It is what tells the service which route may serve the
	// entry, and Validate refuses an entry that names none.
	Kind string
	// AllMembers is version 2's key for a pool entry that is offered to every
	// member of the workspace (R7, D2977). It is EXCLUSIVE with Projects, and
	// the two are written by one encoder: an entry carries the projects it is
	// offered to, or it carries all_members, never both — Validate refuses a
	// pair, because a reader handed both would have to invent which one the
	// owner meant.
	//
	// The workspace owner's writer publishes it only for a workspace with a cap
	// set and only once the cap meter is wired (D2977): the allow-list is the
	// owner's fence over their own money, and without the meter nothing bounds a
	// member that joins later. That rule is the writer's — the node carries no
	// cap, so the node's own reader cannot ask it.
	AllMembers bool
	// Projects are the member projects the workspace offers this credential
	// to, sorted and each once. An empty list is refused by Validate rather
	// than read as "everybody" or as "nobody": a written shared credential
	// that names no project — and carries no AllMembers — is a half-written
	// declaration, and the two readings of it are the one mistake that would
	// either widen the pool to every member or hide the mistake behind a list
	// that serves no one.
	Projects []string
	// Covers are the concrete repositories a forge entry reaches, canonical
	// and each sorted and once, whose provider segment is the entry's own Kind
	// (U2, the owner's declaration — the plan's constraint that a forge entry's
	// coverage is the workspace's list and never the member's). A pattern is
	// refused where it is written, for the reason JobAccountRepos refuses one:
	// F42's coverage is a list of repositories the owner declared, and a
	// pattern is a declaration about repositories nobody has declared.
	//
	// It is empty for a model entry, which reaches no repository of its own
	// (the repositories a model call may be made for are the member's own
	// policy's business), and Validate refuses both mismatches: a model entry
	// that covers repositories and a forge entry that covers none.
	Covers []string
}

// The version-1 node's own shape: what a node written by the release before
// this one holds, and what this reader still reads. The version-2 shape this
// Fugaro now writes is in workspace_credentials_v2.go, beside the columns it
// adds; these structs exist for the reader that reads the older document, and
// nothing writes them.
type workspaceCredentialsNodeV1 struct {
	Version int                               `json:"version"`
	Name    string                            `json:"name"`
	Members []workspaceCredentialMemberNodeV1 `json:"members"`
	Shared  []workspaceSharedNodeV1           `json:"shared"`
}

type workspaceCredentialMemberNodeV1 struct {
	Project         string   `json:"project"`
	FirebaseProject string   `json:"firebase_project"`
	DatabaseRegion  string   `json:"database_region"`
	FP              string   `json:"fp"`
	JobAccounts     []string `json:"job_accounts"`
	ExchangeURL     string   `json:"exchange_url"`
}

type workspaceSharedNodeV1 struct {
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
}

// workspaceCredentialsKeys are the top-level keys this Fugaro writes. Every
// other key is tolerated and listed in WorkspaceCredentials.UnknownKeys, and a
// spelling of a known one that differs only in case is refused
// (checkMemberNames), for the workspace node's own reason: encoding/json folds
// a member name, so two spellings would be one member whose value depends on
// the order the document wrote them in.
var workspaceCredentialsKeys = []string{"version", "name", "members", "shared"}

// WorkspaceCredentialsNode builds the node this Fugaro publishes: the bytes a
// conditional write puts at PathWorkspaceCredentials(w.Name).
//
// The value is held to its own rules first (Validate), so a node is never built
// from a document the reader would refuse. The empty lists are written as empty
// lists and not as null, so the node says "this member's row names no account"
// the way the reader reads it.
func WorkspaceCredentialsNode(w *WorkspaceCredentials) ([]byte, error) {
	if err := w.Validate(); err != nil {
		return nil, fmt.Errorf("projectpolicy: the workspace credentials node is not one this Fugaro publishes: %w", err)
	}
	n := workspaceCredentialsNodeV2{
		Version: WorkspaceCredentialsVersion,
		Name:    w.Name,
		Members: make([]workspaceCredentialMemberNodeV2, 0, len(w.Members)),
		Shared:  make([]workspaceSharedNodeV2, 0, len(w.Shared)),
	}
	for _, m := range w.Members {
		accounts := make([]string, 0, len(m.JobAccounts))
		accounts = append(accounts, m.JobAccounts...)
		repos := make([]workspaceJobAccountNode, 0, len(m.JobAccountRepos))
		for _, e := range m.JobAccountRepos {
			entryRepos := make([]string, 0, len(e.Repos))
			entryRepos = append(entryRepos, e.Repos...)
			repos = append(repos, workspaceJobAccountNode{Account: e.Account, Repos: entryRepos})
		}
		n.Members = append(n.Members, workspaceCredentialMemberNodeV2{
			Project: m.Project, FirebaseProject: m.FirebaseProject, DatabaseRegion: m.DatabaseRegion, FP: m.FP,
			JobAccounts: accounts, JobAccountRepos: repos, ExchangeURL: m.ExchangeURL,
		})
	}
	for _, s := range w.Shared {
		projects := make([]string, 0, len(s.Projects))
		projects = append(projects, s.Projects...)
		covers := make([]string, 0, len(s.Covers))
		covers = append(covers, s.Covers...)
		n.Shared = append(n.Shared, workspaceSharedNodeV2{
			Name: s.Name, Kind: s.Kind, AllMembers: s.AllMembers, Projects: projects, Covers: covers,
		})
	}
	out, err := json.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: the workspace credentials node could not be encoded: %w", err)
	}
	return out, nil
}

// DecodeWorkspaceCredentials reads the node this Fugaro writes, and the one the
// release before it wrote. found is false for a node that is not there at all
// (null, or nothing), which is the state before the first publish and not a
// refusal.
//
// **The version is read first, and the document is read by that version's own
// row reader.** Version 2 is the shape this Fugaro writes
// (workspace_credentials_v2.go); version 1 is the shape the release before it
// wrote, and the reader reads it rather than refusing it because a workspace
// whose owner has not run `fugaro update` yet holds one — the service must
// still place that workspace's callers after the release, and the node's own
// writer migrates the document in place (WS4). What version 1 does not say is
// not invented: a job account of a version-1 row carries an empty repository
// list, and a version-1 pool entry is the model credential the pool held before
// version 2 (F15). A version no release wrote is refused with
// *WorkspaceVersionError rather than read as the nearest one.
//
// An unknown top-level key is tolerated and listed (UnknownKeys) and an unknown
// key *inside a row is refused*: the top-level tolerance is forward
// compatibility for a reader older than the writer (P8), and a row is a
// security-bearing declaration whose fields are exactly the ones §5 names — a
// `database_url` in one would be the splice finding 6 closed, and a decoder
// that ignored it would be the design quietly accepting it again.
//
// Everything the node states is checked for what it must mean (Validate), and a
// refusal never repeats a value: the addresses, projects and URLs of a member's
// row are another installation's configuration, and the package's rule is that
// a refusal names the field and not what a hand wrote in it.
func DecodeWorkspaceCredentials(raw []byte) (*WorkspaceCredentials, bool, error) {
	if IsNull(raw) {
		return nil, false, nil
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, true, &WorkspaceNodeError{Why: "the workspace credentials node is not a JSON object", Err: err}
	}
	if err := checkMemberNames("the workspace credentials node", names, workspaceCredentialsKeys); err != nil {
		return nil, true, &WorkspaceNodeError{Why: "one of its top-level keys is written twice, or is a key this Fugaro writes spelled in another case", Err: err}
	}
	version, err := workspaceCredentialsNodeVersion(raw)
	if err != nil {
		return nil, true, &WorkspaceNodeError{Why: err.Error()}
	}
	var w *WorkspaceCredentials
	switch version {
	case 1:
		w, err = decodeWorkspaceCredentialsV1(raw)
	case WorkspaceCredentialsVersion:
		w, err = decodeWorkspaceCredentialsV2(raw)
	default:
		return nil, true, &WorkspaceVersionError{Found: version, Understood: WorkspaceCredentialsVersion}
	}
	if err != nil {
		return nil, true, &WorkspaceNodeError{Why: "it is not the shape a workspace credentials node has, or one of its rows carries a key this Fugaro does not write", Err: err}
	}
	for _, k := range names {
		if !slices.Contains(workspaceCredentialsKeys, k) {
			w.UnknownKeys = append(w.UnknownKeys, k)
		}
	}
	slices.Sort(w.UnknownKeys)
	if err := w.Validate(); err != nil {
		return nil, true, &WorkspaceNodeError{Why: "it is not a node this Fugaro could have written", Err: err}
	}
	return w, true, nil
}

// decodeWorkspaceCredentialsV1 reads the version-1 shape: the node the release
// before this one wrote, which says how the workspace reaches each member and
// which projects a pool entry is offered to, and nothing about the repositories
// an account is a job of or the kind a credential is.
//
// The two columns version 2 adds are read as the version's own silence rather
// than filled in: an account carries an empty repository list (a reader that
// invented them would publish a declaration nobody made, and the scope refuses
// a run on such an account with the reason that names the owner's update, R19),
// and a pool entry is a model credential, which is the only kind the pool held
// before version 2 (F15) — a version-1 bundle's entries are Claude tokens and
// the project's Anthropic key, and a forge credential could not have been
// offered by a node that had no way to say which forge it was.
func decodeWorkspaceCredentialsV1(raw []byte) (*WorkspaceCredentials, error) {
	var n workspaceCredentialsNodeV1
	if err := decodeStrictRows(raw, &n, workspaceCredentialsRowsV1); err != nil {
		return nil, err
	}
	w := &WorkspaceCredentials{Version: n.Version, Name: n.Name}
	for _, m := range n.Members {
		accounts := make([]string, 0, len(m.JobAccounts))
		accounts = append(accounts, m.JobAccounts...)
		w.Members = append(w.Members, WorkspaceCredentialMember{
			Project: m.Project, FirebaseProject: m.FirebaseProject, DatabaseRegion: m.DatabaseRegion, FP: m.FP,
			JobAccounts: accounts, ExchangeURL: m.ExchangeURL,
		})
	}
	for _, s := range n.Shared {
		projects := make([]string, 0, len(s.Projects))
		projects = append(projects, s.Projects...)
		w.Shared = append(w.Shared, WorkspaceShared{Name: s.Name, Kind: SharedKindModel, Projects: projects})
	}
	return w, nil
}

// workspaceCredentialsRows is one version's row shapes, for the strict reader:
// the keys each kind of row may carry, and what such a row names, so that a
// refusal of an unknown key says what the shape it refused was. The versions
// differ (version 2's member row carries the repositories of each job account,
// and its shared entry the kind, the all_members key and the covers of a forge
// entry), so the lists and the sentences are passed in rather than written once
// and read for both: a version read with the other version's row keys would
// refuse the columns it exists to carry.
type workspaceCredentialsRows struct {
	members    []string
	shared     []string
	memberWhat string
	sharedWhat string
}

// workspaceCredentialsRowsV1 is the version-1 shape's rows, and the sentences a
// refusal of one is made with.
var workspaceCredentialsRowsV1 = workspaceCredentialsRows{
	members:    []string{"project", "firebase_project", "database_region", "fp", "job_accounts", "exchange_url"},
	shared:     []string{"name", "projects"},
	memberWhat: "a member's row names the member's project, its Firebase project, the region its database is in when it is not the default one, its fp, its job service accounts and its exchange URL, and nothing else — the database the service reads is derived from the verified iss and no bucket is bound",
	sharedWhat: "a shared entry names the credential and the member projects it is offered to, and nothing else — the credential's value is nowhere in the node (rule 2 of spec.md §4)",
}

// decodeStrictRows decodes the node in one version's shape and refuses an
// unknown key inside any of its rows: the top-level shape is read tolerantly
// (the columns above), and every row is read against the keys its version's
// shape has, which is the one place a security-bearing declaration may not grow
// a field nobody decided.
func decodeStrictRows(raw []byte, out any, rows workspaceCredentialsRows) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	var lists struct {
		Members []json.RawMessage `json:"members"`
		Shared  []json.RawMessage `json:"shared"`
	}
	if err := json.Unmarshal(raw, &lists); err != nil {
		return err
	}
	for _, r := range lists.Members {
		if err := decodeRow(r, rows.members, rows.memberWhat); err != nil {
			return err
		}
	}
	for _, r := range lists.Shared {
		if err := decodeRow(r, rows.shared, rows.sharedWhat); err != nil {
			return err
		}
	}
	return nil
}

// decodeRow refuses a row carrying a key outside known, and names the key: a
// field this Fugaro does not write is either a mistake or an older design's
// field, and both are refusals rather than something to ignore. The name is the
// *key* and never its value, which is the reader's own rule above, and what the
// row is a row *of* is the caller's sentence, so a shared entry's refusal
// describes a shared entry rather than repeating a member's row.
func decodeRow(raw json.RawMessage, known []string, what string) error {
	names, err := objectMemberNames(raw)
	if err != nil {
		return err
	}
	if err := checkMemberNames("a row of the workspace credentials node", names, known); err != nil {
		return err
	}
	for _, n := range names {
		if !slices.Contains(known, n) {
			return fmt.Errorf("it carries the key %q, which this Fugaro does not write in a row: %s", n, what)
		}
	}
	return nil
}

// Validate holds the node to what it must mean, and it is the reader's rules
// rather than the file's: a node hand-edited in the database is read by the
// same rules the publisher applied.
func (w *WorkspaceCredentials) Validate() error {
	if !config.ProjectNameRE.MatchString(w.Name) {
		return fmt.Errorf("the node names the workspace %q, which is not a project name (the grammar a workspace shares with a project, F21)", w.Name)
	}
	seenProject := make(map[string]bool, len(w.Members))
	seenFirebase := make(map[string]bool, len(w.Members))
	seenAccount := make(map[string]bool, len(w.Members))
	for i, m := range w.Members {
		if !config.ProjectNameRE.MatchString(m.Project) {
			return fmt.Errorf("members[%d] names a project that is not a project name", i)
		}
		if seenProject[m.Project] {
			return fmt.Errorf("members[%d] names %q twice: a member is one row, and two rows would make the lookup's answer depend on which one it read", i, m.Project)
		}
		seenProject[m.Project] = true
		if m.Project != m.FP {
			return fmt.Errorf("members[%d] names the project %q and the fp %q, which are not the same: the fp claim is what the run identity is checked against, so a row whose two halves disagree would admit one member's token as another's", i, m.Project, m.FP)
		}
		if !config.ProjectNameRE.MatchString(m.FirebaseProject) {
			return fmt.Errorf("members[%d] names a Firebase project that is not a project name", i)
		}
		// One Firebase project belongs to one member, and the refusal is the same
		// rule the account and the project name are held to — for a sharper
		// reason: the database a member's policy, ledger and outcomes are read
		// from is DERIVED from this field (finding 6), so two rows naming one
		// Firebase project are two members reading one database, which is the
		// splice the derivation exists to make unrepresentable. The refusal names
		// no value, like the account's: a row is another installation's
		// configuration.
		if seenFirebase[m.FirebaseProject] {
			return fmt.Errorf("members[%d] names a Firebase project another row already names: one Firebase project belongs to one member, and the database a member's state is read from is derived from it, so two rows naming one would read one member's state for both", i)
		}
		seenFirebase[m.FirebaseProject] = true
		if !ValidDatabaseRegion(m.DatabaseRegion) {
			return fmt.Errorf("members[%d].database_region is not a region a Realtime Database host is written with: the service derives the member's database URL from the Firebase project and this region, and a region the tree's own host grammar does not admit derives a host that is not a database — an adopted project in a region is named there by the region alone", i)
		}
		if err := validExchangeURL(m.ExchangeURL); err != nil {
			return fmt.Errorf("members[%d].exchange_url %v", i, err)
		}
		if len(m.JobAccounts) == 0 {
			return fmt.Errorf("members[%d] names no job service account: a member the service cannot resolve a caller by is a member whose shared-credential calls are all refused, and an empty row is a mistake rather than a member with no jobs", i)
		}
		for j, a := range m.JobAccounts {
			if !validAccountAddress(a) {
				return fmt.Errorf("members[%d].job_accounts[%d] is not a service account address", i, j)
			}
			folded := strings.ToLower(a)
			if seenAccount[folded] {
				return fmt.Errorf("members[%d].job_accounts[%d] is an address another row already names: one account belongs to one member, and an address two rows name would be a caller whose member depends on which row the lookup read", i, j)
			}
			seenAccount[folded] = true
		}
		// The repositories of the row's accounts are version 2's column and the
		// rules only it has, and they are asked after the row's own fields: a row
		// whose account is not an address is refused for the address, not for the
		// repositories that name it.
		if err := validateJobAccountRepos(i, m); err != nil {
			return err
		}
	}
	if !slices.IsSortedFunc(w.Members, func(a, b WorkspaceCredentialMember) int {
		return strings.Compare(a.Project, b.Project)
	}) {
		return errors.New("its members are not in project-name order: the node is written one way every time so a publish can compare it against the bytes it would write")
	}
	seenShared := make(map[string]bool, len(w.Shared))
	for i, s := range w.Shared {
		if s.Name == "" {
			return fmt.Errorf("shared[%d] names no credential", i)
		}
		if seenShared[s.Name] {
			return fmt.Errorf("shared[%d] names the credential %q twice: one credential carries one allow-list, and two entries would make the effective set depend on which one was read", i, s.Name)
		}
		seenShared[s.Name] = true
		// The entry's kind, its all_members key and the repositories a forge
		// entry covers are version 2's columns, and their rules are the
		// version's own file's (workspace_credentials_v2.go); the allow-list's
		// are asked here beside them, in the order the entry's fields are read.
		if err := validateSharedEntryV2(i, s); err != nil {
			return err
		}
		if !slices.IsSorted(s.Projects) {
			return fmt.Errorf("shared[%d].projects is not sorted", i)
		}
		for j, p := range s.Projects {
			if !config.ProjectNameRE.MatchString(p) {
				return fmt.Errorf("shared[%d].projects[%d] is not a project name", i, j)
			}
			if j > 0 && s.Projects[j-1] == p {
				return fmt.Errorf("shared[%d].projects[%d] names %q twice", i, j, p)
			}
		}
	}
	if !slices.IsSortedFunc(w.Shared, func(a, b WorkspaceShared) int {
		return strings.Compare(a.Name, b.Name)
	}) {
		return errors.New("its shared credentials are not in name order: the node is written one way every time, for the members' reason")
	}
	return nil
}

// validAccountAddress holds a job service account to the rule the service's own
// fold needs: a printable ASCII address with an `@` and no whitespace. It is a
// shape check and not an IAM check — whether the account exists is the
// platform's answer — and it is here rather than in the service because a row
// the service could never resolve is a row the node may not carry.
func validAccountAddress(a string) bool {
	if a == "" || !strings.Contains(a, "@") || strings.ContainsAny(a, " \t\r\n") {
		return false
	}
	for i := 0; i < len(a); i++ {
		if c := a[i]; c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// ValidDatabaseRegion holds a region to the characters a Realtime Database host
// is written with, which is the tree's own grammar and not one invented here:
// `internal/infra` recognises a regional database as
// `<project>-default-rtdb.<region>.firebasedatabase.app` over `[a-z0-9-]+`, and
// a region outside that class derives a host that is not a host at all. It is
// the same division of labour the Firebase project id has — the shape is this
// package's and whether the database exists is the platform's — and an empty
// region is the project's default database, which is a state and not a mistake.
//
// It is exported because the service derives the host from it too (credsvc's
// workspaceDatabase) and may be handed a table that never went through this
// node: a region is a piece of a URL whatever built the row, and one spelling
// of "what a region may be" is what keeps the two from drifting.
func ValidDatabaseRegion(region string) bool {
	if region == "" {
		return true
	}
	for i := 0; i < len(region); i++ {
		if c := region[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// validExchangeURL holds a member's exchange URL to the shape a token's
// audience has: an https URL with no query and no fragment. The audience a
// member-bound assertion carries is that URL (spec.md §3), and a URL with a
// query in it is one no assertion this design mints would match — a member
// whose every sign-in is refused, which reads as a broken exchange rather than
// as the misconfiguration it is.
func validExchangeURL(raw string) error {
	if !strings.HasPrefix(raw, "https://") {
		return errors.New("is not an https URL")
	}
	rest := strings.TrimPrefix(raw, "https://")
	if rest == "" || strings.HasPrefix(rest, "/") {
		return errors.New("names no host")
	}
	if strings.ContainsAny(rest, "?#") {
		return errors.New("carries a query or a fragment: a member-bound assertion's audience is the endpoint's URL, so an exchange URL with either admits nobody")
	}
	return nil
}

// MemberAdmitsShared is the member side of D1298's intersection: whether the
// member's OWN published policy admits a credential of the workspace's pool
// (spec.md §4: "the member's own policy, which may refuse a provider or a
// credential the workspace holds").
//
// **It is the member's existing vocabulary and not a second one.** A credential
// is admitted the way every other credential of the member's installation is:
// the member's own `claude_accounts` names it, which is the block `fugaro
// project policy publish` derives into the node's `accounts` (DeriveAccounts).
// The workspace's pool therefore adds credentials to a member without inventing
// a key a member has to learn: an administrator who wants a shared credential
// used here writes the same declaration they write for a credential of their
// own, and one who writes nothing is served nothing from the workspace.
//
// `name` is therefore the key THIS policy speaks, and the caller is the one that
// translates a pool credential into it: the pool's one tier-B credential is the
// project's own Anthropic API key, which the bundle holds as
// config.AnthropicSecret and this block files under config.AnthropicKeyAccount
// (the sentinel DeriveAccounts derives an entry for). That pairing is the
// pool's own rule and it lives with the tier that makes it (internal/creds'
// policyKey), so a reader comparing the two spellings finds them paired in one
// place — the same pairing the installation's own cascade makes.
//
// **A refusal is the member's, and naming it so is the point.** D1298's whole
// safety property is that the member's policy is the tighter side, so a
// credential the member has not admitted is refused with the member's policy as
// the reason and never with the workspace's — the operator is not sent to the
// workspace for a refusal the member made.
//
// **This is the NAME leg, and the intersection's repository leg is not asked
// here — it is asked by the service, at the repository's own node.** The
// member's policy is scoped by the credential's NAME (the key this block holds
// it under, which the caller translates, policyKey) and by the workspace's
// per-project allow-list; the repository the run is on is asked separately, from
// the repository child the run identity's verified slug names, and it is the
// service that reads it (credsvc.Workspace.Shared's admittedOn over
// projectpolicy.PathRepo). So a credential this policy names but the run's
// repository is not admitted to is not served, and the whole of D1298's
// direction still holds: the pool can only serve names the member already
// admits, on repositories the member's own database admits them to.
//
// **What is not re-asked is the pairing of the run's repository with the
// calling job account.** The member's own front door holds the account's
// repository to the run identity's slug, and the table this row belongs to names
// a member's job accounts and no repository, so the workspace's service has no
// slug to make that comparison with. The two `allow_repos` legs ARE re-asked,
// and the service is what re-asks them: they are the member's own service's
// other check, asked by the canonical `owner/repo` name (admittedAccounts) — the
// project's own allow-list, the leg that closes the `repos rm` gap because
// removing a repository does not touch its child, and the account's — and a
// slug cannot be inverted into that name (F18). It does not have to be: the
// account's list is exact (validAccounts refuses a wildcard) and the publish
// path keys the child with task.Slug of the same name, so the entry whose slug
// is the verified one IS the run's repository, and credsvc.Workspace.Shared
// recovers it there (canonicalOfSlug) and asks both legs with it. A child that
// is stale against the policy it was derived from — a `repos rm` whose child was
// not removed, two concurrent publishes, a hand-edited node — is therefore
// refused by the service exactly as the member's own cascade refuses it. The
// pairing is spec.md §8's residual risk 7, and closing it is the owner's call:
// the repository per job account is a column the owner's per-member table does
// not carry.
func MemberAdmitsShared(p *Policy, name string) bool {
	if p == nil {
		return false
	}
	_, ok := p.Accounts[name]
	return ok
}
