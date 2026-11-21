package workspace

// The member-operations contract (C2): the seam between the commands that manage
// a workspace's projects, people, roles and state (WU5-WU13, WU23, WU24) and the
// code that actually reaches a member project (WU2, WU4, WU9, WU12, WU15, WU20,
// WU22). Every declaration here is a shape and nothing else: the concrete sides
// are written beside it and I1 adapts them onto these types and deletes
// memberops_stub.go with its test. What a consumer may call, and what a provider
// owes, is stated in docs/workspace-projects-users/contract-member.md.
//
// Three properties are this file's own, and none of them can be re-derived from
// a method list:
//
//   - A member is named by BOTH of its projects -- its GCP project and its
//     Firebase project -- on every method that reaches one (F2, R10). They are
//     one project only sometimes: `docs/design/v1.md` (D3, revised 2026-10-04)
//     lets an installation's Firebase project be a project of its own, which is
//     why `doctor_workspace.go`'s workspaceSplitCheckID exists. An operation that
//     reached the wrong one of the two would half-apply itself -- the folder move
//     and the service account's grant cover both projects or neither -- so the
//     pair travels together, named, and never as one field.
//   - This file restates no member format. A member's documents travel as the
//     bytes its database holds: its roster of grants is internal/access's own
//     format, which every member CLI already reads and which D2806 and D2818
//     keep unchanged, and its mirror's entry shape is the roster contract's
//     (C1), written beside this one. Neither can be named as a type here: the
//     grant types live in internal/access, which reaches this package through
//     internal/projectpolicy, so naming them would be an import cycle -- the
//     same reason WU2's Folders interface is written in basic types. A
//     consumer is in internal/cli, which may import both, and there is exactly
//     one reader of each format rather than a second spelling of it. The mirror
//     is ADVISORY besides: a member's admins hold their own database's write and
//     can rewrite it, so drift, never the mirror, is the fence (D2813,
//     threat-model.md §3).
//   - Nothing here writes a member's IAM (D2801, A6). A grant means a row in the
//     member's own roster, and the member's OWN `fugaro update` turns that roster
//     into bindings; a workspace admin's authority over the member is the
//     folder's or the account's binding and nothing else.
//
// The member's own documents travel with the ETag a conditional write is made
// under, as WU1's RosterNode does on the workspace's side: a write that lost a
// race is `rtdb.ErrPrecondition` with nothing written, and the caller reads again
// and judges against the bytes that are really there, so two admins editing one
// member cannot lose each other's work (D2817).

import (
	"context"
	"time"
)

const (
	// PathMemberMirror is the member list the workspace keeps in the member's own
	// database (D2813, D2817): the addresses, their kinds and their external
	// marks, and no role or grant of anybody -- those stay in the member's own
	// roster. It sits under the same fence the roster does (`config/admin`, the
	// parent of access.PathNode), so it is reached over IAM and denied to every
	// client token. Its entries are C1's, and the copy in a member's database is
	// readable by that member's own launchers, operators and viewers, which hold
	// the member database's viewer role (threat-model.md §3).
	PathMemberMirror = "config/admin/workspace-members"

	// PathMemberAudit is the member's own audit trail (D2811): create-only
	// entries, written with the permission the member's roster write itself
	// needs -- a project admin holds no write on the workspace's project (F11).
	// It is as tamperable as the roster, and the design says so: whoever holds
	// the member's database write can rewrite either, and the cross-check that
	// makes an unfiled change visible is Cloud Audit Logs (R13, WU12).
	PathMemberAudit = "config/admin/workspace-audit"

	// WorkspaceIAMAccountOutput is the workspace root's Terraform output that
	// names the workspace's IAM account (WU3, D2803): the account the SA
	// mechanism grants `roles/owner` on a member and impersonates only, with
	// Terraform making no key for it (F20). The name is fixed here so that the
	// root that writes it (WU3), the authority that grants with it (WU20) and
	// the drift check that ignores its own grants (WU12) spell one string.
	WorkspaceIAMAccountOutput = "workspace_iam_account"
)

// MemberTarget names the member a call acts on: its GCP project, and its
// Firebase project when that is a project of its own (F2, R10). Firebase is
// empty for a member whose Firebase project is its GCP project, and the two
// spell one project then; an implementation that needs the pair's canonical
// form reads it as it reads WU2's FolderMember, whose `canonical` drops the
// Firebase of its own.
type MemberTarget struct {
	Project  string
	Firebase string
}

// MemberOps is what a workspace's commands do to one member project: read and
// write its roster of grants, read and write its mirror, read its policy, report
// what waits for its own `fugaro update`, offboard a person, and file an audit
// entry. Every method names the member's GCP project and its Firebase project
// separately (F2, R10), and none of them runs Terraform (D2801): the member's own
// `fugaro update` is what turns a written grant into a binding.
//
// WU4 provides the concrete type, except for Offboard, which is WU9's core; I1
// adapts both onto this interface.
type MemberOps interface {
	// ReadGrants reads the member's roster, `config/admin/access`, with the ETag
	// a conditional write is made under. The bytes are that node's own format
	// (internal/access), decoded by the caller; an absent node is the
	// installation whose lists were never migrated, which Found tells from an
	// empty roster and which is emphatically not "everyone".
	ReadGrants(ctx context.Context, m MemberTarget) (MemberDoc, error)

	// WriteGrants writes the member's roster document, conditional on doc.ETag:
	// a node another writer moved under this one is a refusal rather than an
	// overwrite. The document must be one the member's own CLI reads, and the
	// write refuses an unmigrated list, as `fugaro user grant` does, so that no
	// workspace path reaches a member whose own CLI would refuse the same write.
	WriteGrants(ctx context.Context, m MemberTarget, doc MemberDoc, raw []byte) error

	// ReadMirror reads the member's mirror at PathMemberMirror. It is the read a
	// grant's advisory gate and the installation-level refusals key on: a member
	// whose mirror is present is a linked one (R11). An absent node is a member
	// the workspace has not mirrored into.
	ReadMirror(ctx context.Context, m MemberTarget) (MemberDoc, error)

	// WriteMirror writes the mirror document, conditional on doc.ETag, and
	// refuses a document that is not a JSON object -- the write replaces the
	// whole node, and a member's own admins read it as the member list.
	WriteMirror(ctx context.Context, m MemberTarget, doc MemberDoc, raw []byte) error

	// Bindings reads the member's IAM policy and names what Fugaro knows each
	// bound principal is (D2806, F17): the member's own Terraform accounts and
	// the workspace IAM account (WorkspaceIAMAccountOutput) among them, so that
	// a caller can tell a person from a machine from one of Fugaro's own
	// accounts. It reads and writes nothing else.
	Bindings(ctx context.Context, m MemberTarget) ([]MemberBinding, error)

	// PendingApply compares what the record says the member's IAM should carry
	// (want) with what the member has applied: the installation outputs
	// `launchers` and `operators` in the member's state bucket, and the
	// published admin list (access.PathAdminList, D1940). It reads; it applies
	// nothing, and no apply of one installation happens from another machine
	// (F7, D2801).
	PendingApply(ctx context.Context, m MemberTarget, want ApplyWant) (Pending, error)

	// Offboard sweeps one person off the member (WU9, F13): their stored grants
	// dropped, their own secrets swept by the intake, an audit entry filed. It
	// is refused until the workspace's stored grant has reached the member's
	// derived admin list, because until then the member's own `fugaro update` is
	// what is pending (D2818, D1940).
	Offboard(ctx context.Context, m MemberTarget, address string) error

	// Audit creates one entry in the member's trail at PathMemberAudit, under id,
	// and never updates one (D2811): a second write of an id is a refusal the
	// caller sees. The event is C1's AuditEvent as JSON and this contract reads
	// no field of it, as WU1's AuditWriter does for the workspace's trail.
	Audit(ctx context.Context, m MemberTarget, id string, event []byte) error
}

// MemberDoc is one of the member's own documents as its database holds it: the
// bytes, exactly as stored, and the ETag a conditional write is made under. It
// is the member side of WU1's RosterNode.
type MemberDoc struct {
	// Raw is the document. It is empty for a node that is not there, which Found
	// tells from a node written empty.
	Raw []byte
	// ETag is what the node carries now; the null ETag when it is absent, which
	// is the precondition that CREATES it. A write under an ETag this contract
	// handed back is the only safe one: `rtdb.PutIfMatch` refuses an empty ETag
	// rather than treating it as unconditional.
	ETag string
	// Found is false for a node that is not there. An absent mirror is a member
	// the workspace has not mirrored into, which is not an empty mirror.
	Found bool
}

// PrincipalKind is what a principal bound on a member is, as far as Fugaro can
// tell from the member's own outputs and the workspace's record (D2806, F17).
// Drift rests on it: a binding of Fugaro's own accounts is never a difference,
// and a person's or a machine's is (WU12).
type PrincipalKind string

const (
	// PrincipalUnknown -- the zero kind -- is a principal Fugaro cannot place:
	// not a person, not a recorded machine, not one of its own accounts. A
	// reader must treat it as somebody else's binding and not as one to ignore,
	// which is why the zero value means "unplaced" rather than "fine".
	PrincipalUnknown PrincipalKind = ""

	// PrincipalPerson is a person: a Google account, the human members of the
	// workspace and of the member (A3, A4).
	PrincipalPerson PrincipalKind = "person"

	// PrincipalMachine is a machine the workspace records as a member of kind
	// `sa`: CI, a coordinator, a central orchestrator (D2806). Its real IAM
	// stays where it is; the record is what drift and audit compare against.
	PrincipalMachine PrincipalKind = "machine"

	// PrincipalTerraform is one of the member's own Terraform accounts -- the
	// job account, the build identity, the history account -- read from the
	// member's outputs (F17). Drift ignores its grants.
	PrincipalTerraform PrincipalKind = "member-terraform"

	// PrincipalWorkspaceIAM is the workspace's own IAM account, the account
	// WorkspaceIAMAccountOutput names (WU3, D2803). Drift ignores the grants it
	// holds on a member: its `roles/owner` is the SA mechanism itself, and, in
	// the SA model, the Firebase admin binding the member's own `update` derives
	// from its project owners (WU12).
	PrincipalWorkspaceIAM PrincipalKind = "workspace-iam"
)

// MemberBinding is one principal bound to one role on one of the member's
// projects. The project is named because a member's IAM is two policies when its
// Firebase project is a project of its own (F2, R10).
type MemberBinding struct {
	Project   string
	Principal string
	Role      string
	// Kind is what Fugaro knows the principal is; PrincipalUnknown is a binding
	// nobody has placed (D2806, F17).
	Kind PrincipalKind
}

// ApplyWant is what the record says a member's IAM should already carry: the
// lists the member's own `fugaro update` derives from its roster (F6, D1940).
// The caller reads them from the workspace node and the member's record, and
// passes them here so that what "waiting" means is decided in one place.
type ApplyWant struct {
	// Launchers and Operators are the entries of the member installation's
	// outputs of those names (its state bucket's outputs).
	Launchers []string
	Operators []string
	// Admins is the published admin list's own list (access.PathAdminList,
	// D1940), which is where a workspace admin's stored grant surfaces once the
	// member has applied it.
	Admins []string
}

// The two marks a Pending report carries where nothing can be compared (WU4):
// no installation output and no published object names a viewer or a
// repository-scope grant, so for those "waiting" cannot be told from "applied"
// and a report must say so rather than read as clean (D2815).
const (
	NotMeasuredViewers = "viewers"
	NotMeasuredScopes  = "repository-scope grants"
)

// Pending is what of ApplyWant has not reached the member yet.
type Pending struct {
	// Launchers, Operators and Admins are the entries of ApplyWant the member
	// does not carry yet: the grants waiting for its own `fugaro update`, which
	// `workspace project status` prints as a row (D2801, D2818).
	Launchers []string
	Operators []string
	Admins    []string
	// NotMeasured holds the marks of what this comparison cannot decide --
	// NotMeasuredViewers and NotMeasuredScopes -- and is empty only when every
	// list ApplyWant carries was really compared.
	NotMeasured []string
}

// Authority is the mechanism a workspace holds its members by, and it is one
// half of the consent the addendum's authority rests on (D2800): Link and Unlink
// are the MEMBER OWNER's acts, made from that owner's own credentials on their
// own machine, and AddAdmin, RemoveAdmin and AdminBindings write and read the
// WORKSPACE's own side of the binding (the folder's owners, or the account's
// token creators, D2818, R8). Nothing here writes a binding on a member project
// (D2801, A6).
//
// WU2 provides the folder mechanism and WU20 the SA fallback; I1 adapts them.
type Authority interface {
	// Kind reports which mechanism this authority is. It takes no member: the
	// mechanism is the workspace's, chosen once by `workspace init` (D2803).
	Kind() AuthorityKind

	// Link gives the workspace the authority over the member, covering the
	// member's GCP project AND its Firebase project when that is a project of
	// its own (R10). It refuses a member whose project is already another
	// workspace's (R2, R14) and one that cannot be placed back, and it records
	// what it moved so that Unlink has a destination (R14, WU2).
	Link(ctx context.Context, m MemberTarget) error

	// Unlink drops the authority and only the authority (D2822): the workspace
	// admins' stored grants stay in the members' rosters until the workspace
	// project's owner runs `workspace project rm`, and `status` shows the member
	// as "unlinked, grants left" in between.
	Unlink(ctx context.Context, m MemberTarget) error

	// Holds reports whether the workspace holds the member now. It is the read
	// the installation-level refusals and a grant's gate key on, with the
	// member's mirror present (R11).
	Holds(ctx context.Context, m MemberTarget) (bool, error)

	// AddAdmin gives a workspace admin their own binding: `roles/owner` on the
	// workspace's folder, or the workspace IAM account's token creator (and, in
	// the SA model, an owner grant on the workspace's own project, R8). It is
	// applied at once and reaches every member the mechanism covers (D2818 as
	// corrected).
	AddAdmin(ctx context.Context, admin string) error

	// RemoveAdmin drops that binding, at once. What lags is the stored grant's
	// resource-level bindings, which follow at each member's own `fugaro update`
	// (D2801, D2818).
	RemoveAdmin(ctx context.Context, admin string) error

	// AdminBindings lists the members the LIVE bindings name -- the folder's
	// owners, or the account's token creators -- which is what the workspace
	// node's `Admins` is compared against (D2823). Only an admin holding one of
	// these is propagated or trusted, because the accounts that can rewrite
	// `Admins` cannot set a binding (WU0's threat model).
	AdminBindings(ctx context.Context) ([]string, error)
}

// AuthorityKind is which mechanism a workspace holds its members by (D2803).
// The empty kind is an authority with no mechanism at all -- a workspace whose
// `init` has not run -- which the contract names no constant for: a command that
// reads a kind must handle it rather than assume the two below are the whole set.
type AuthorityKind string

const (
	// AuthorityFolder is the folder mechanism, the one a workspace with a GCP
	// organization gets: the members sit in the workspace's folder and its
	// admins are bound to `roles/owner` on it (D2804).
	AuthorityFolder AuthorityKind = "folder"

	// AuthoritySA is the service-account fallback, for a workspace with no
	// organization: the workspace IAM account (WorkspaceIAMAccountOutput) is
	// granted `roles/owner` on each member and impersonated only, and workspace
	// admins are its token creators (D2803, F20).
	AuthoritySA AuthorityKind = "sa"
)

// Snapshot is what a save is of (D2807, D2824): a member's settings and spend
// summary, or the workspace's own audit export. The two are different snapshots
// of different projects, which is why the audit export is a flag of its own on
// the command (`project save --audit`) rather than a member that does not exist.
type Snapshot struct {
	// Workspace is the workspace's own project name: the audit export is the
	// workspace's, and a member's snapshot is filed under the workspace it
	// belongs to.
	Workspace string
	// Member is the member whose settings and spend summary are saved. It is the
	// zero one for the workspace's audit export, which is not any member's.
	//
	// Its two fields are named the way the workspace names a member, not the way
	// the member-reaching calls of this package name one: Project is the
	// member's Fugaro project name -- the key of the owner's member table, the
	// record's members[].project and the name the workspace's credentials node
	// is read by (spec.md §5) -- and Firebase is the member's Firebase project,
	// which is the project a snapshot of that member is read from. A save runs
	// on the workspace owner's machine, which holds no GCP project id for a
	// member at all, and no snapshot reaches a member's GCP project: a caller
	// that needs that pair takes it from MemberTarget's other consumers.
	Member MemberTarget
	// Audit is true for the workspace's audit export and false for a member's
	// settings and spend summary.
	Audit bool
}

// SnapshotWritten is where a save left a snapshot.
type SnapshotWritten struct {
	// File is the local file the save always writes and names, whether or not a
	// designated repository is set.
	File string
	// PullRequest is the pull request a save opened in the E6 designated
	// repository (D2807): its URL, or empty when no repository is set, where the
	// local file is the whole save.
	PullRequest string
}

// StateWriter writes and reads a project's state snapshots (WU15, D2807, D2824).
// It is what `project rm` saves through by default and what `workspace project
// save` calls; it refuses a public repository, because a snapshot carries
// addresses and E6's public-repository allowance assumed it never would (D2753,
// D2824), and it refuses in a coding agent's session (A7).
type StateWriter interface {
	// Save writes one snapshot: always to a local file it names, and, when the
	// E6 designated repository is set, as a pull request from the owner's own
	// credentials -- clone, commit to a branch, push, open (D2807).
	Save(ctx context.Context, s Snapshot) (SnapshotWritten, error)

	// LastSaved reports when the snapshot for s was last taken, and whether one
	// was: found is false for a project that has never been saved, which is not
	// the zero time -- `status` shows the two differently.
	LastSaved(ctx context.Context, s Snapshot) (at time.Time, found bool, err error)
}

// CredentialProbe reads the credentials a member references to find out whether
// they still work (D2820, D2825). It is `workspace project status
// --check-credentials`, and it is the owner's own command: it reads values into
// memory and spends them on one minimal authenticated call each, in the owner's
// own terminal, refused in a coding agent's session as `doctor --check-*` are.
//
// A personal token -- a person's own Claude account, under their own prefix --
// is never probed (D2825): its broken mark is what `status` shows, and a
// workspace admin's terminal must not read people's personal credentials.
//
// WU22 provides the concrete probe; I1 adapts it.
type CredentialProbe interface {
	// Probe makes one minimal authenticated call per credential the member
	// references and answers what each provider said. No value of a credential
	// is returned, printed, logged or put in an error.
	Probe(ctx context.Context, m MemberTarget) ([]CredentialResult, error)
}

// CredentialKind is what sort of credential a probe read (D2825).
type CredentialKind string

const (
	// CredentialModelKey is a model API key the member's own configuration
	// references.
	CredentialModelKey CredentialKind = "model key"

	// CredentialClaudeAccount is a project-owned Claude account: a token the
	// member's own bundle holds under the project's prefix. A personal one is
	// never probed (D2825).
	CredentialClaudeAccount CredentialKind = "claude account"
)

// CredentialResult is one credential's answer: which credential, what the
// provider said, and nothing of the value.
type CredentialResult struct {
	// Name is the credential as the member's own configuration names it.
	Name string
	// Kind is what sort of credential it is.
	Kind CredentialKind
	// OK is whether the call the probe made succeeded.
	OK bool
	// Status is the provider's own answer: its status code, or the words of its
	// refusal. It never carries the credential.
	Status string
}

// Drift measures a member's live IAM against what the workspace node records,
// and against nothing a member can rewrite: the fence is the workspace node and
// never the mirror, because a member's own admins hold their database's write
// (D2813, WU0's threat model).
//
// It is a function type rather than an interface because it is one read with one
// answer: an implementation (WU12's `ComputeDrift`, which I1 adapts here) closes
// over what it needs -- the workspace node, the member's policy, the audit logs
// and Authority.AdminBindings -- and a consumer (`status`, `reconcile`) holds
// one value of this type.
type Drift func(ctx context.Context, m MemberTarget) (DriftReport, error)

// DriftReport is what a member's live IAM says against the workspace node
// (D2813, D2823). It is a report and not a verdict: a reader prints a row per
// difference and decides nothing from an empty one while Unmeasured holds
// anything.
type DriftReport struct {
	// Extra are the bindings the member holds that neither its roster nor the
	// workspace node states: what `reconcile` removes, for people only -- a
	// machine's difference is reported and never written (D2806).
	Extra []DriftBinding
	// Missing are the grants the record states that the member's IAM does not
	// carry: what `reconcile` adds, and, for a workspace admin, what waits for
	// the member's own `fugaro update` (D2801, D2818).
	Missing []DriftBinding
	// Unmeasured names what this report could not compare, for the reason it
	// gives. A reader must not read an empty Extra and Missing as "in sync"
	// while this is not empty.
	Unmeasured []string
	// ConsoleChanges are the IAM changes Cloud Audit Logs records whose
	// resulting binding is drift and which no trail of Fugaro's holds (D2811,
	// R13): a change made in Google's console, named rather than counted.
	ConsoleChanges []ConsoleChange
}

// DriftBinding is one binding on one side of the difference. Both of the
// member's projects are read (F2, R10), so the project is part of the finding.
type DriftBinding struct {
	Project   string
	Principal string
	Role      string
	// Kind is what Fugaro knows the principal is (D2806, F17). A difference
	// Fugaro's own accounts make is not drift and is not reported, so a binding
	// here is a person, a machine, or one nobody has placed.
	Kind PrincipalKind
	// External is true for a person outside the workspace's allowed domains,
	// which drift flags (D2812).
	External bool
	// Implicit is true for a binding that comes with an account rather than from
	// a grant: a member's budget admins hold their database's write, so they are
	// admins de facto (threat-model.md §3). Drift names them instead of counting
	// them as extra.
	Implicit bool
}

// ConsoleChange is one IAM change Cloud Audit Logs records whose resulting
// binding is drift and which neither trail of Fugaro's holds (D2811, R13). A
// member's own `fugaro update` is never one of these: its bindings are the
// record's, so they are not drift in the first place.
type ConsoleChange struct {
	Project   string
	Principal string
	Role      string
	// Entry names the Cloud Audit Logs entry the change was read from, so a
	// person can look it up in Google's console.
	Entry string
	// At is when Google logged it.
	At time.Time
}
