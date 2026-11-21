# The member-operations contract (C2)

**Package:** `internal/workspace`. **Task:** C2 of [implementation-plan.md](implementation-plan.md), level 1, beside C1's
roster contract (`docs/workspace-projects-users/contract.md`, the file C1 creates and this one is written against).
**Restates:** [reconciliation.md](reconciliation.md) D2801, D2803, D2811, D2818, D2820,
D2823, D2824, D2825 and R8, R10, R11, R13. **Status: shape only — nothing here runs.** `internal/workspace/memberops.go`
declares the types below, `internal/workspace/memberops_stub.go` answers their zero shapes, and
`TestMemberOpsStubShapes` (`go test ./internal/workspace/ -run TestMemberOps -count=1`) holds both to what this document
says. No command of the plan exists yet; the folder's [README](README.md) says the same for the whole plan.

The contract is the seam that lets the commands which manage a workspace's projects, people, roles and state
(WU5–WU13, WU23, WU24) be written beside the code that reaches a member project at all (WU2, WU4, WU9, WU12, WU15, WU20,
WU22). Everything here is what a task written against C2 may rely on, and nothing else in the package is promised.

## 1. The rule that shapes every method

**A member is named by both of its projects, separately: its GCP project and its Firebase project** (`MemberTarget`,
F2, R10). They are one project only sometimes — `docs/design/v1.md` (D3, revised 2026-10-04) lets an installation's
Firebase project be a project of its own — and an operation that reached the wrong one of the two would half-apply
itself, because the folder move and the service account's grant cover both projects or neither. So the pair travels
together as one argument, both fields named, and a method that reaches IAM names which project each binding is on.

**Nothing in this contract writes a member's IAM** (D2801, A6). A grant means a row in the member's own roster, and the
member's own `fugaro update` turns that roster into bindings; the only authority writes are `Authority`'s own — the
folder move, the account's grant and the workspace admins' bindings — and no command applies one installation's
Terraform from another installation's machine (F7, D2801).

**Nothing in this contract writes a member's documents unconditionally.** A read answers the bytes beside the ETag they
were read at; a write lands only while the node still carries that ETag; a node another writer moved under this one is
`rtdb.ErrPrecondition` with nothing written, and the caller reads again and judges against what is really there, so two
admins editing one member cannot lose each other's work (D2817).

## 2. What each side owes

| Side | Type | Owes | Task |
| --- | --- | --- | --- |
| the member writer | `MemberOps` | a member's roster, mirror, policy read, pending comparison, audit entry — and never a Terraform run | WU4, except `Offboard` |
| the offboarder | `MemberOps.Offboard` | one person's grants dropped and their own secrets swept on a named member, refused until the stored admin grant has reached the member's derived admin list | WU9 |
| the folder authority | `Authority` | the folder move and its return from the member owner's own credentials, the folder's owner binding, and the live bindings read | WU2 |
| the SA authority | `Authority` | the account's `roles/owner` grant and its removal, the account's token creators, and the live bindings read (`workspace_iam_account`) | WU20 |
| the state writer | `StateWriter` | a snapshot to a local file always, and as a pull request when E6's repository is set | WU15 |
| the credential probe | `CredentialProbe` | one minimal authenticated call per credential, in the owner's own terminal, never a value and never a personal token | WU22 |
| the drift check | `Drift` (`ComputeDrift`) | extra and missing bindings against the workspace node, the console's own changes, and what it could not measure | WU12 |
| the wiring | — | adapts all of them onto these types, registers the commands, and deletes `memberops_stub.go` **and `memberops_stub_test.go`, which is the stub's own test** | I1 |

**Consumers:** WU5, WU6, WU7, WU8, WU11, WU13, WU18, WU23 and WU24 call these types; WU10 reads `MemberOps.ReadMirror`
for its linked-member refusals (R11); WU21 drives the whole family end to end. A consumer's own tests use fakes declared
in its own `_test.go` files, never a stub of this contract, which is what lets I1 delete the stubs without rewriting
anyone's tests.

## 3. `MemberOps`

| Method | Shape | What it answers |
| --- | --- | --- |
| `ReadGrants` | `(ctx, MemberTarget) (MemberDoc, error)` | the member's roster `config/admin/access`, bytes and ETag |
| `WriteGrants` | `(ctx, MemberTarget, MemberDoc, []byte) error` | the roster written under the ETag read; refuses an unmigrated list |
| `ReadMirror` | `(ctx, MemberTarget) (MemberDoc, error)` | the member's mirror `config/admin/workspace-members` |
| `WriteMirror` | `(ctx, MemberTarget, MemberDoc, []byte) error` | the mirror written under the ETag read |
| `Bindings` | `(ctx, MemberTarget) ([]MemberBinding, error)` | the member's policy, each binding placed by `PrincipalKind` |
| `PendingApply` | `(ctx, MemberTarget, ApplyWant) (Pending, error)` | what the record states that the member has not applied |
| `Offboard` | `(ctx, MemberTarget, address string) error` | one person swept off the member |
| `Audit` | `(ctx, MemberTarget, id string, event []byte) error` | one create-only entry under `PathMemberAudit` |

**The documents travel as bytes, and their formats are not restated here.** A member's roster is `internal/access`'s
format — the one every member CLI already reads, which D2806 and D2818 keep unchanged — and a member's mirror entry is
C1's. Neither type can be named in this package: `internal/access` reaches `internal/workspace` through
`internal/projectpolicy`, so naming it would be an import cycle, which is the same reason WU2's `Folders` interface is
written in basic types. The consumers are in `internal/cli`, which imports both, and C2 therefore leaves one reader of
each format rather than a second spelling of it.

`MemberDoc` is the member side of WU1's `RosterNode`: `Raw`, `ETag` and `Found`. `Found` is false for a node that is
not there — a member the workspace has not mirrored into, or an installation whose lists were never migrated, which is
not an empty roster and emphatically not "everyone" (`internal/access`'s own reading of an absent node).

`PendingApply` compares the record's promise with the member's own outputs — the installation outputs `launchers` and
`operators` from the member's state bucket, and the published admin list (`access.PathAdminList`, D1940) — and reports
each list's unapplied entries. **It also says what it could not measure** (`Pending.NotMeasured`: `NotMeasuredViewers`,
`NotMeasuredScopes`), because no output carries a viewer or a repository-scope grant, so for those "waiting" cannot be
told from "applied" (D2815). A reader must not read empty lists as "in sync" while that field is not empty.

`Bindings` places each principal with `PrincipalKind`: `PrincipalPerson`, `PrincipalMachine` (a member of kind `sa`,
D2806), `PrincipalTerraform` (the member's own Terraform accounts, F17) and `PrincipalWorkspaceIAM` (the account
`workspace_iam_account` names, D2803). The empty kind, `PrincipalUnknown`, is a principal nobody placed — a reader must
treat it as somebody else's binding rather than as one to ignore.

## 4. `Authority`

| Method | Shape | What it answers |
| --- | --- | --- |
| `Kind` | `() AuthorityKind` | `AuthorityFolder` or `AuthoritySA` — the workspace's, so it takes no member |
| `Link` | `(ctx, MemberTarget) error` | the authority given, from the member owner's own credentials (D2800, D2819) |
| `Unlink` | `(ctx, MemberTarget) error` | the authority dropped and only the authority (D2822) |
| `Holds` | `(ctx, MemberTarget) (bool, error)` | whether the workspace holds the member now (R11) |
| `AddAdmin` | `(ctx, admin string) error` | the admin's own live binding, applied at once (D2818) |
| `RemoveAdmin` | `(ctx, admin string) error` | that binding dropped, at once |
| `AdminBindings` | `(ctx) ([]string, error)` | the members the live bindings name (D2823) |

`Link` covers the member's GCP project **and** its Firebase project when that is a project of its own, and refuses a
project that already sits in another workspace's folder (R2, R14). `Unlink` writes a member audit entry and leaves the
workspace admins' stored grants in the members' rosters: those go with the workspace project owner's
`workspace project rm`, and `status` shows "unlinked, grants left" in between (D2822). Only an entry of the workspace
node's `Admins` that also holds one of `AdminBindings` is propagated or trusted, because the accounts that can rewrite
`Admins` cannot set a binding (D2823, [threat-model.md](threat-model.md) §3).

## 5. `StateWriter`, `CredentialProbe` and `Drift`

`Snapshot.Member` names its member the way the workspace names a member, and not the way §1's calls do: `Project` is the
member's Fugaro project name — the key of the owner's member table and the record's `members[].project` — which is what
a snapshot is filed under, and `Firebase` is the member's Firebase project, which the snapshot itself is read from. A
save runs on the workspace owner's machine, which holds no GCP project id for a member at all, and no snapshot reaches a
member's GCP project.

`StateWriter.Save` writes one `Snapshot` — a member's settings and spend summary, or the workspace's own audit export —
always to a local file it names and, when E6's designated repository is set, as a pull request from the owner's own
credentials (D2807). It refuses a public repository, naming a `--repo` override, because the snapshot carries addresses
(D2824), and it refuses in a coding agent's session (A7). `LastSaved` reports when a snapshot was last taken and
whether one ever was; a project that was never saved is not a save at the zero time.

`CredentialProbe.Probe` reads each project model key and each project-owned Claude account a member references into
memory, in the owner's own terminal, makes one minimal authenticated call per credential, and answers pass or fail with
the provider's own status. It never returns, prints, logs or puts in an error a value of a credential, and it never
reads a personal token under a person's own prefix (D2820, D2825). It needs no credentials-service route.

`Drift` is a function type, not an interface: one read with one answer, closed over what it needs (the workspace node,
the member's policy, the audit logs and `Authority.AdminBindings`) by WU12's `ComputeDrift`. It is measured against the
workspace node and **never against the mirror** (D2813), it ignores Fugaro's own accounts (D2806, F17), it reports the
folder owners and token creators that are not bound admins (R13), and it names each Cloud Audit Logs `SetIamPolicy`
whose resulting binding is drift and which neither trail of Fugaro's holds — never a member's own `update` (D2811,
R13). `DriftReport.Unmeasured` names what it could not compare, and a reader must not read an empty report as "in sync"
while that field is not empty.

## 6. The names this contract fixes

| Name | Value | Read and written by |
| --- | --- | --- |
| `WorkspaceIAMAccountOutput` | `workspace_iam_account` | WU3's root writes the output, WU20 grants with the account it names, WU12 ignores its grants (D2803, F20) |
| `PathMemberMirror` | `config/admin/workspace-members` | WU4, WU5 and WU10 (D2813, D2817) |
| `PathMemberAudit` | `config/admin/workspace-audit` | WU4 and WU9 (D2811) |

**`workspace_iam_account` is fixed here, and in WU3's acceptance, so WU3 needs no edge to C2.** Both member nodes sit
under `config/admin`, where the member's own roster does (`access.PathNode`): that node's `.read: false` and
`.write: false` are what keep every client token out of all three. It is the fence and not a secret that holds — IAM
bypasses it, and a member's launchers, operators and viewers read their own member's mirror over IAM, which
[threat-model.md](threat-model.md) §3 states at its real size.

## 7. What a stub is, and what it is not

Each interface has a stub (`StubMemberOps`, `StubAuthority`, `StubStateWriter`, `StubCredentialProbe`, `StubDrift`) so
that the shapes are implementable and a caller written before the concrete sides exist has something to hold. **A stub
answers the empty truth**: the zero value of every type it declares — nothing recorded, nothing written, nothing held,
nothing measured. That is deliberately **not** a refusal, because a refusal is a behaviour a consumer could come to
depend on; and it is **not** a claim that the truth was checked: a caller reading a report out of a stub is reading
"nobody has looked". `TestMemberOpsStubShapes` pins the zero shape of every method, the exact method set of every
interface, and the three names above. I1 deletes the stubs, their test, and the stubs' declarations together, in the
change that wires the concrete sides; no release carries one, and none is ever wired to a live member.

## 8. What this document does not decide

- **The member roster's and the mirror's formats.** They are `internal/access`'s and C1's; C2 moves their bytes.
- **What a member's own `fugaro update` derives.** `docs/roles/spec.md` and `internal/access`'s derived lists own
  that; C2 only reports the difference between them and the member (`PendingApply`).
- **Which of the two mechanisms a workspace uses.** `workspace init` writes it into the roster; `Authority.Kind` reads
  the implementation, not a member.
- **Anything a member's own CLI does while it is unlinked.** An unlinked member, existing ones included, keeps
  `fugaro user …` unchanged, and no command of this family reaches it (D2819, R11).
