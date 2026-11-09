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
// member* (the member's job service accounts, its Firebase project, its Fugaro
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
const WorkspaceCredentialsVersion = 1

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
	// every node DecodeWorkspaceCredentials reads: a node this reader does not
	// understand is refused rather than half-read (P8).
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
	// (P8), and the writer that would *rewrite* the node refuses them, because
	// publishing the node from a config would drop a key nobody modelled — and
	// "tolerated" and "silently dropped" look the same in the node afterwards.
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
	// one member's tokens from another's.
	FirebaseProject string
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
	// ExchangeURL is the member's exchange endpoint, where the workspace's
	// signer mints a member-bound assertion for (spec.md §3, D1293). It is the
	// owner's declaration of where the member is reached, and it is not a
	// credential: nothing signs with it and no token is verified against it.
	ExchangeURL string
}

// WorkspaceShared is one credential of the workspace's pool and the projects it
// may be served to: D1298's per-project allow-list.
type WorkspaceShared struct {
	// Name is the credential's name, which is the name the bundle's entry and
	// the cascade both speak. It is a name and never a value: no node of a
	// database carries a credential (rule 2 of spec.md §4).
	Name string
	// Projects are the member projects the workspace offers this credential
	// to, sorted and each once. An empty list is refused by Validate rather
	// than read as "everybody" or as "nobody": a written shared credential
	// that names no project is a half-written declaration, and the two
	// readings of it are the one mistake that would either widen the pool to
	// every member or hide the mistake behind a list that serves no one.
	Projects []string
}

// workspaceCredentialsNode is the node as this Fugaro writes it: the shape the
// JSON of `workspace_credentials/<name>` has, and the version this file's
// reader reads. It is a struct with json tags rather than a map, so the
// encoding is one order and one spelling every time.
type workspaceCredentialsNode struct {
	Version int                             `json:"version"`
	Name    string                          `json:"name"`
	Members []workspaceCredentialMemberNode `json:"members"`
	Shared  []workspaceSharedNode           `json:"shared"`
}

type workspaceCredentialMemberNode struct {
	Project         string   `json:"project"`
	FirebaseProject string   `json:"firebase_project"`
	FP              string   `json:"fp"`
	JobAccounts     []string `json:"job_accounts"`
	ExchangeURL     string   `json:"exchange_url"`
}

type workspaceSharedNode struct {
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
	n := workspaceCredentialsNode{
		Version: WorkspaceCredentialsVersion,
		Name:    w.Name,
		Members: make([]workspaceCredentialMemberNode, 0, len(w.Members)),
		Shared:  make([]workspaceSharedNode, 0, len(w.Shared)),
	}
	for _, m := range w.Members {
		accounts := make([]string, 0, len(m.JobAccounts))
		accounts = append(accounts, m.JobAccounts...)
		n.Members = append(n.Members, workspaceCredentialMemberNode{
			Project: m.Project, FirebaseProject: m.FirebaseProject, FP: m.FP,
			JobAccounts: accounts, ExchangeURL: m.ExchangeURL,
		})
	}
	for _, s := range w.Shared {
		projects := make([]string, 0, len(s.Projects))
		projects = append(projects, s.Projects...)
		n.Shared = append(n.Shared, workspaceSharedNode{Name: s.Name, Projects: projects})
	}
	out, err := json.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: the workspace credentials node could not be encoded: %w", err)
	}
	return out, nil
}

// DecodeWorkspaceCredentials reads the node this Fugaro writes. found is false
// for a node that is not there at all (null, or nothing), which is the state
// before the first publish and not a refusal.
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
	var n workspaceCredentialsNode
	if err := decodeStrictRows(raw, &n); err != nil {
		return nil, true, &WorkspaceNodeError{Why: "it is not the shape a workspace credentials node has, or one of its rows carries a key this Fugaro does not write", Err: err}
	}
	if n.Version != WorkspaceCredentialsVersion {
		return nil, true, &WorkspaceVersionError{Found: n.Version, Understood: WorkspaceCredentialsVersion}
	}
	w := &WorkspaceCredentials{Version: n.Version, Name: n.Name}
	for _, k := range names {
		if !slices.Contains(workspaceCredentialsKeys, k) {
			w.UnknownKeys = append(w.UnknownKeys, k)
		}
	}
	slices.Sort(w.UnknownKeys)
	for _, m := range n.Members {
		accounts := make([]string, 0, len(m.JobAccounts))
		accounts = append(accounts, m.JobAccounts...)
		w.Members = append(w.Members, WorkspaceCredentialMember{
			Project: m.Project, FirebaseProject: m.FirebaseProject, FP: m.FP,
			JobAccounts: accounts, ExchangeURL: m.ExchangeURL,
		})
	}
	for _, s := range n.Shared {
		projects := make([]string, 0, len(s.Projects))
		projects = append(projects, s.Projects...)
		w.Shared = append(w.Shared, WorkspaceShared{Name: s.Name, Projects: projects})
	}
	if err := w.Validate(); err != nil {
		return nil, true, &WorkspaceNodeError{Why: "it is not a node this Fugaro could have written", Err: err}
	}
	return w, true, nil
}

// decodeStrictRows decodes the node and refuses an unknown key inside any of
// its rows: the top-level shape is read tolerantly (the columns above), and
// every row is read with DisallowUnknownFields, which is the one place a
// security-bearing declaration may not grow a field nobody decided.
func decodeStrictRows(raw []byte, out *workspaceCredentialsNode) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	var rows struct {
		Members []json.RawMessage `json:"members"`
		Shared  []json.RawMessage `json:"shared"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return err
	}
	members := []string{"project", "firebase_project", "fp", "job_accounts", "exchange_url"}
	shared := []string{"name", "projects"}
	for _, r := range rows.Members {
		if err := decodeRow(r, members); err != nil {
			return err
		}
	}
	for _, r := range rows.Shared {
		if err := decodeRow(r, shared); err != nil {
			return err
		}
	}
	return nil
}

// decodeRow refuses a row carrying a key outside known, and names the key: a
// field this Fugaro does not write is either a mistake or an older design's
// field, and both are refusals rather than something to ignore. The name is the
// *key* and never its value, which is the reader's own rule above.
func decodeRow(raw json.RawMessage, known []string) error {
	names, err := objectMemberNames(raw)
	if err != nil {
		return err
	}
	if err := checkMemberNames("a row of the workspace credentials node", names, known); err != nil {
		return err
	}
	for _, n := range names {
		if !slices.Contains(known, n) {
			return fmt.Errorf("it carries the key %q, which this Fugaro does not write in a row: a member's row names the member's project, its Firebase project, its fp, its job service accounts and its exchange URL, and nothing else — the database the service reads is derived from the verified iss and no bucket is bound", n)
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
		if len(s.Projects) == 0 {
			return fmt.Errorf("shared[%d] offers %q to no project: a shared credential with an empty allow-list is a half-written declaration, and writing the projects out is the only way to say who may be served it", i, s.Name)
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
// **The repository legs of the same intersection are deliberately not asked
// here, and this is the one place in the intersection where a leg is left
// out.** The member's own service narrows its credentials by the canonical
// repository the call is on (admittedAccounts takes it from the job account the
// deployment published, never from the run's own header). This deployment
// cannot: what it verifies about a caller is its member, and the repository a
// run is on reaches it only as the run identity's `fs`, a slug that
// `task.Slug` sanitised and no reader can invert into the canonical name the
// member's `allow_repos` lists. Asking a leg whose input the workspace cannot
// verify would be a check that reads whatever the caller said, which is the
// shape the service refuses everywhere else (the X-Fugaro-Run-Job rule). So the
// shared pool is scoped by the credential's NAME — the key this block holds it
// under, which the caller translates (policyKey) — and the workspace's
// per-project allow-list, and a member's own per-repository narrowing of that
// name is not enforced for a pooled credential — it is a bound this shape
// records rather than a check it makes, and the honest way to close it is a
// canonical repository per job account in the owner's per-member table, which
// is a column this table does not carry. What the name leg does buy is the
// whole of D1298's direction: a credential the member's policy never declared
// is refused, so the pool can only serve names the member already admits.
func MemberAdmitsShared(p *Policy, name string) bool {
	if p == nil {
		return false
	}
	_, ok := p.Accounts[name]
	return ok
}
