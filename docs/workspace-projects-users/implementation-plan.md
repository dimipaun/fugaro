# Workspace projects and users: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). **Nothing is
implemented, and no task is approved except WU0**, which the owner approved on 2026-11-18 to launch
once this plan is on `main`; every other task needs the owner's explicit go by name. The plan is
written to the answers of 2026-11-18 (D2800–D2825, D2816 unused; every one as recommended except
**D2822 and D2824, which are not the recommendation**; the amendments of that day are reconciliation
O3, O5, O6 and O7, and the resolutions R1–R14 are its §4), and
the graph beside it is [implementation-plan.graph.json](implementation-plan.graph.json).

**The repository's settings.** `rigor.default` is `startup` with no area rules, and
`planning.decoupling` is on (read with `fugaro settings planning.decoupling rigor.default rigor.areas
--repo dimipaun/fugaro --json` on 2026-11-18), so the contract-first pass decouples aggressively.
Because D2800 puts IAM authority over members in this design, **every task that writes IAM, a roster,
a mirror, an audit trail, a repository or a credential is reviewed at opus with a mandatory
independent review**, whatever the mode; the tasks say which.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/terraform.md` (WU3), `.claude/rules/credentials-service.md`
(WU0: `website/content/security.md` is `SECURITY.md` regenerated with `go run ./internal/clidoc`;
WU9, WU22), `.claude/rules/website.md` (WU0's and I1's website files: `npm run check` in `website/`),
`.claude/rules/ee.md` (nothing here imports `ee/`; the sign-on's verifier stays in
`ee/coordinator/oauth`, D2810).

- **Always.** Red test first; targeted tests only (`go test ./internal/cli/ -run 'TestX' -count=1`);
  `gofmt -l .` prints nothing; hermetic tests against `internal/gcpfake` and `t.TempDir()`; JSON
  output gets a schema in `schemas/` and decodes strictly; every write command refuses in a coding
  agent's session (A7) and asks nothing (a refusal names the flag, e.g. `--external`,
  `--no-save-state`). **A command constructor built in S2 does not build its own reporter**: I1
  wraps each command with the reporter (D1354) and adds its census row when it registers it, so no
  level-2 task turns `TestEveryMutatingCommandWritesALog` red. **A task's tests use fakes declared in
  its own `_test.go` files, never the contract's stub**, so I1 deletes the stubs without rewriting
  them, and **every fake and test helper a task declares carries its task id** (`wu8FakeMember`), so
  the level-2 tasks in `internal/cli` merge without a name clash. A Command's `-run` pattern
  abbreviates: the run anchors it to the task's listed test names (`-run '^(TestA|TestB)$'`). Every
  member-controlled string `status`, `ls` and drift print is escaped, as
  `TestUpdateEscapesWhatTheRecordMigrationEchoes` pins for the record. Every write of a member's
  roster keeps `fugaro user grant`'s refusal on unmigrated lists (`TestGrantRefusesOnUnmigratedLists`).
  Comments say why.
- **Ask first.** A new dependency; any change to a contract's files (raise a contract change
  `R-…`, never work around it); any change to `fugaro update`'s stages (this plan adds none, R7);
  any change to the member roster's format (`internal/access`; D2806 keeps it unchanged).
- **Never.** A secret-shaped literal; a live cloud call from a test or an agent; a write from
  `workspace project status` (its `--check-credentials` reads a credential and writes nothing); a
  binding on a member project written outside that member's own Terraform (D2801, A6); a Terraform
  apply of one installation from another installation's machine (F7); a workspace service account
  that is also the credentials service's account (F20); an SA key (D2803); a new field in the
  member roster (D2818 as corrected); an edit under `.github/workflows/`.
- **The HARD RULE.** `fugaro update` gains no stage and no step (R7): the roster node is created by
  its first write, no rules version moves, linking is opt-in (D2819), and an unlinked member, every
  existing one included, keeps `fugaro user …` unchanged (R11). WU3's one new resource is converged
  by the workspace root's existing apply. The `workspace member` rename lands in I1, in the change
  that registers its replacement.

## 2. Global constraints and review focus

| Constraint the spec implies | Pinned by |
| --- | --- |
| The authority is given by the member's owner on their own machine, never taken (D2800, D2819) | WU18: `link` refuses unless the selected config is the member's own and its pin names the workspace |
| No path needs a write on the workspace's database for a project admin (F11, D2811, D2817) | WU8: a grant by a project admin writes only the member's roster and its audit node |
| The workspace credentials service's account still holds no write on a member (D1299) | WU3's tftest |
| Removing a workspace admin removes the folder or SA authority at once and queues every member-level grant (D2818 as corrected) | WU7 `TestRmAdminRemovesBindingsAndQueuesGrants` |
| Drift is measured against the workspace node, never the mirror (D2813) | WU12 `TestDriftIgnoresARewrittenMirror` |
| Drift never reports Fugaro's own accounts, and does report folder bindings and token creators that are not workspace admins (D2806, R13) | WU12 `TestDriftIgnoresFugaroAccounts`, `TestDriftReportsAFolderOwnerWhoIsNotAnAdmin` |
| A project already in another workspace or folder is refused (R2, R14) | WU5, WU18 tests |
| Nothing is stripped at join (D2805) | WU5 test |
| An unlinked member keeps `fugaro user …` (R11, D2819) | WU10 `TestUserCommandsUnchangedOnAnUnlinkedMember` |
| `status` writes nothing on any path, `--json` and `--check-credentials` included | WU11, WU22 tests against fakes that fail on any write |
| The roster never lives under the published record (R5) | WU1: a roster write leaves `workspaces/<name>`'s ETag unchanged |
| The member roster's format never changes (D2806, D2818) | WU4 `TestMemberGrantsDecodeWithTodaysAccess` (every member write goes through WU4) |
| Only an admin backed by the live folder or SA binding is propagated or trusted (D2823) | WU5 `TestProjectAddPropagatesOnlyBoundAdmins`, WU12 `TestDriftTrustsOnlyBoundAdmins`, WU11 `TestStatusFlagsAdminTampering` |
| A member owner's `unlink` drops the authority only; the grants go with `project rm` (D2822) | WU18 `TestUnlinkDropsOnlyTheAuthority`, WU11 `TestStatusUnlinkedGrantsLeft` |
| The state writer never pushes addresses to a public repository (D2824) | WU15 `TestStateSaveRefusesAPublicRepo` |

## 3. Slices and tasks

Four slices. **S0** (WU0) is the gate. **S1** (C1, C2, and the pure providers WU1, WU2, WU3, WU22)
settles the shapes and builds what consumes none; `workspace init` (WU24) and `project save` (WU23)
are S2 consumers. **S2** builds every other part against the shapes,
side by side. **S3** (I1, then WU10, WU16, WU17, and WU21 after WU10) wires, refuses the old doors on
linked members, documents and hands the owner the live checks. D2803's "folder first": WU2 (folder) is
launched before WU20 (SA), and Check 720 exercises the folder before any SA link is tried live.
WU14 of the first draft was dropped in review round 1 (R7) and WU19 was never used; neither number
is reused.

### WU0: the threat model, rewritten for workspace authority
- Acceptance: `threat-model.md` states, at their real size, what each of these reaches: a stolen
  workspace-admin credential; a compromised workspace IAM account; the workspace credentials
  service's account (its custom role can rewrite `workspaceRosters`, `Admins` included); the
  workspace project's owners, editors and budget admins (de facto workspace admins: they can write
  the roster and grant themselves token-creator); a hostile project admin (rewrites the mirror and
  the member's audit node, D2813, D2811); a hostile member owner; and every member's launchers and
  viewers, who read the mirror's addresses and external marks. It states that a workspace admin's
  `roles/owner` on a member reads every member secret, personal Claude tokens included, against
  §9's "never hands out a credential"; that a member's budget admins can rewrite
  `config/admin/workspace-link` (R14), which WU2's `Unlink` guards; and the revocation lag of D2818
  as corrected. It says which of `docs/workspaces/spec.md` §9's rules the addendum supersedes
  ("never widens a member", "never writes a member's database", "never a second permission model")
  and on what consent; `SECURITY.md` and `docs/design/v1.md` say the same; an independent security
  review (`fugaro review --pr N`, run by the coordinator) has returned and the owner has answered
  every finding before any other task launches.
- Files: `docs/workspace-projects-users/threat-model.md` (new), `SECURITY.md`,
  `website/content/security.md` (regenerated), `docs/design/v1.md`,
  `internal/cli/docs_workspaces_security_test.go`, `docs/workspaces/README.md`.
- Interfaces: consumes reconciliation §3–§4; produces the threat model C1 and C2 are reviewed against.
- Tests: `TestSecurityMdStatesTheWorkspaceThreatModel`, `TestDesignV1NamesTheWorkspace`,
  `TestWebsiteSecurityPage*`, and `website/test/security.mjs`.
- Command: `go test ./internal/cli/ -run 'TestSecurityMdStatesTheWorkspaceThreatModel|TestDesignV1NamesTheWorkspace|TestWebsiteSecurityPage|Docs' -count=1`
  and `npm run check` in `website/`.
- Red step: a new pin for the superseding sentence in `docs_workspaces_security_test.go` fails until
  `SECURITY.md` and `docs/design/v1.md` carry it.
- Depends on: none.
- Commit: `docs(workspace-projects-users): threat model for workspace IAM authority (WU0, D2800)`.
- Reviewer tier: opus, mandatory independent review. Six files: four are one sentence each.

### C1: the roster contract
- Acceptance: the roster's Go types (`Roster`, `Person` with `Kind` human|sa, `External` and, for a
  machine, its expected per-project roles (D2806); `Domains`, `Admins`, the set of member grants the
  workspace wrote (D2818's mark, kept here and never in a member roster), `AuditEvent`,
  `MirrorEntry`; `Mechanism` (folder or SA) and `FolderID`, which `workspace init` writes with the
  caller as the first admin; R6), its path `workspaceRosters/<name>` (R5), its JSON Schema, the audit event's
  schema, the `Store` (ETag read, conditional write and create) and `AuditSink` interfaces and their
  stubs exist, with a contract document saying what each side may rely on.
- Files: `internal/workspace/roster.go` (new), `internal/workspace/rosterstore.go` (new),
  `internal/workspace/roster_stub.go` (new), `schemas/workspace-roster.schema.json` (new),
  `schemas/workspace-audit-event.schema.json` (new), `docs/workspace-projects-users/contract.md` (new).
- Interfaces: produces the roster shape every consumer reads or writes.
- Tests: `TestRosterSchemaRoundTrip`, `TestRosterDecodeRefusesUnknownField`, the `schemas` package tests.
- Command: `go test ./internal/workspace/ ./schemas/ -run 'TestRoster|TestSchema' -count=1`.
- Red step: the round-trip test fails until the types and the schema agree.
- Depends on: WU0 (its review may reshape the roster).
- Commit: `workspace: the roster contract (C1, D2806, D2817)`.
- Reviewer tier: opus, mandatory independent review (a wire format over IAM). Six files, two of
  them schemas.

### C2: the member-operations contract
- Acceptance: interfaces `MemberOps` (`ReadGrants`, `WriteGrants`, `ReadMirror`, `WriteMirror`,
  `Bindings`, `PendingApply`, `Offboard`, `Audit` to the member's `config/admin/workspace-audit`),
  `Authority` (`Kind`, `Link`, `Unlink`, `Holds`, `AddAdmin`, `RemoveAdmin`, `AdminBindings`), `StateWriter` (`Save`,
  `LastSaved`), `CredentialProbe` (`Probe`) and `Drift(...) DriftReport` (WU12's implementation is
  named `ComputeDrift`, so it never collides with this stub in the same package) exist with stubs and a
  contract document; the workspace root's IAM-account output is named **`workspace_iam_account`**
  (fixed here and in WU3's acceptance, so WU3 needs no edge to C2); every method names the member's
  GCP project and its Firebase project separately (F2, R10).
- Files: `internal/workspace/memberops.go` (new), `internal/workspace/memberops_stub.go` (new),
  `docs/workspace-projects-users/contract-member.md` (new).
- Interfaces: produces what the consumers call; WU2, WU4, WU9, WU12, WU15, WU20 and WU22 provide
  concrete types I1 adapts onto it.
- Tests: `TestMemberOpsStubShapes`.
- Command: `go test ./internal/workspace/ -run TestMemberOps -count=1`.
- Red step: the shape test fails until each stub returns its declared zero shape.
- Depends on: WU0.
- Commit: `workspace: the member-operations contract (C2, D2801, D2803, D2818, D2820)`.
- Reviewer tier: opus, mandatory independent review.

### WU1: the roster node and the workspace audit writer
- Acceptance: the roster lives at `workspaceRosters/<name>` in the workspace's database, written
  only conditionally (a create when absent, an ETag write otherwise; a concurrent change is re-read,
  never overwritten), and a roster write leaves `workspaces/<name>` untouched (R5); no rules change
  (the root denies every client, R7); the audit writer creates Firestore documents and never
  updates one.
- Files: `internal/workspace/roster_rtdb.go` (new), `internal/workspace/roster_rtdb_test.go` (new),
  `internal/workspace/audit_firestore.go` (new).
- Interfaces: consumes none; produces `RosterNode` (raw JSON and ETag) and `AuditWriter`
  (create-only documents) in basic types; I1 adapts C1's `Store` and `AuditSink` onto them.
- Tests: `TestRosterWriteRefusesStaleETag`, `TestRosterCreateWhenAbsent`,
  `TestRosterWriteLeavesRecordETag`, `TestAuditWriterCreateOnly`.
- Command: `go test ./internal/workspace/ -run 'TestRoster|TestAuditWriter' -count=1`.
- Red step: the stale-ETag test fails until the write carries `if-match`.
- Depends on: WU0 (the gate).
- Commit: `workspace: the roster node and the audit writer (WU1, D2811, D2817, R5)`.
- Reviewer tier: opus, mandatory independent review.

### WU2: the folder authority
- Acceptance: the folder authority's `Link` moves the member's project — and its Firebase project
  when separate (R10) — into the workspace's folder from the member owner's own credentials, records
  the original parent in the member's `config/admin/workspace-link` (R14), and refuses a project
  under another workspace's folder (R2); `Unlink` prints the recorded parent and moves the project
  back there, refusing a recorded parent inside any workspace's folder (a budget admin can rewrite
  the record, WU0); `Holds` reports the parent; `AddAdmin`/`RemoveAdmin` change the folder's owner
  binding; `AdminBindings` lists the folder's owners; `CreateFolder` creates a folder with the
  caller as owner and places a project in it (for WU24).
- Files: `internal/infra/folder.go` (new), `internal/infra/folder_test.go` (new),
  `internal/gcpfake/crm.go`, `internal/workspace/authority_folder.go` (new),
  `internal/workspace/authority_folder_test.go` (new).
- Interfaces: consumes none; produces a concrete folder authority I1 adapts to C2's `Authority`.
  `crm.go` gains folders, moves, organization search and project `setIamPolicy` for this task's and
  WU21's tests.
- Tests: `TestFolderLinkMovesTheFirebaseProjectToo`, `TestFolderLinkRefusesOtherWorkspaceFolder`,
  `TestFolderUnlinkRestoresParent`, `TestFolderUnlinkRefusesATamperedParent`,
  `TestFolderAdminBindingAddedAndRemoved`, `TestCreateFolderPlacesTheProject`.
- Command: `go test ./internal/infra/ ./internal/workspace/ -run 'TestFolder|TestCreateFolder' -count=1`.
- Red step: the link test fails until the fake records a move.
- Depends on: WU0 (the gate).
- Commit: `workspace: the folder authority (WU2, D2803, D2804, D2818, R10, R14)`.
- Reviewer tier: opus, mandatory independent review.

### WU24: `workspace init`
- Acceptance: `workspace init` detects an organization the caller can create folders in, creates
  the workspace's folder with the caller as `roles/owner`, places the workspace's own project in it
  (R8), and writes the roster's `Mechanism`, `FolderID` and the caller as its first admin; with no
  organization it records the SA mechanism; it refuses a workspace whose roster already exists.
- Files: `internal/cli/workspace_init.go` (new; constructor only, registered by I1),
  `internal/cli/workspace_init_test.go` (new).
- Interfaces: consumes C1 `Store`; C2 `Authority`.
- Tests: `TestWorkspaceInitPicksFolderWithOrg`, `TestWorkspaceInitPlacesTheWorkspaceProject`,
  `TestWorkspaceInitRecordsSAWithoutOrg`, `TestWorkspaceInitWritesTheFirstAdmin`,
  `TestWorkspaceInitRefusesASecondInit`.
- Command: `go test ./internal/cli/ -run TestWorkspaceInit -count=1`.
- Red step: the folder test fails until its fake answers an organization search.
- Depends on: C1, C2.
- Commit: `workspace: workspace init (WU24, D2803, R8)`.
- Reviewer tier: opus, mandatory independent review.

### WU3: the SA fallback's account
- Acceptance: the workspace root creates a workspace IAM account distinct from the credentials
  service's account, with no key, and outputs its email as `workspace_iam_account`; its token
  creators are not Terraform's (D2818 writes them live); the root's allowed grants stay pinned; and
  the tftest case and the root's own comment say what "no key" is — **the root writes none**, not that
  none can exist — naming the two paths that make one anyway on a project Fugaro does not own
  (`iam.serviceAccounts.actAs`, which `roles/editor` and `roles/iam.serviceAccountUser` carry, and
  `iam.serviceAccountKeys.create`, which `roles/editor` resolves to), which WU11's `status` takes up
  and which the organization policy `constraints/iam.disableServiceAccountKeyCreation` is the only
  control that closes.
- Files: `deploy/terraform/gcp/roots/workspace/main.tf`, `deploy/terraform/gcp/roots/workspace/outputs.tf`,
  `deploy/terraform/gcp/roots/workspace/tests/workspace.tftest.hcl`,
  `deploy/terraform/terraform_test.go`, `internal/infra/tf/workspace.go`,
  `internal/infra/tf/workspace_test.go`.
- Interfaces: consumes none; produces the output WU20 reads.
- Tests: the tftest case "the IAM account is not the service account and Terraform makes no key for
  it" (the assertion is about the root's own resources, and no assertion here claims no key can exist);
  `allowedMemberExprs` in `terraform_test.go`; `TestWorkspacePlanIsTheServiceAndItsOneGrant`, renamed
  for two accounts.
- Command: `terraform -chdir=deploy/terraform/gcp/roots/workspace test` and
  `go test ./internal/infra/tf/ -run Workspace -count=1` and `go test -tags terraform ./deploy/terraform/ -count=1`.
- Red step: the tftest assertion fails until the account exists.
- Depends on: WU0 (the gate).
- Commit: `workspace: the SA fallback's account (WU3, D2803, F20)`.
- Reviewer tier: opus, mandatory independent review. Six files: three are test files.

### WU22: the credential probe
- Acceptance: `Probe` reads each project model key and each project-owned Claude account a member
  references in memory, in the owner's own terminal, as `doctor --check-bitbucket-token` reads the
  bundle, and makes one minimal authenticated call per credential; it returns pass or fail with the
  provider's status and never the value; it never reads a personal token under a person's own
  prefix (D2825), whose broken marks `status` still shows; it refuses in a coding agent's session;
  it needs no credentials-service route (U7).
- Files: `internal/cli/workspace_probe.go` (new), `internal/cli/workspace_probe_test.go` (new).
- Interfaces: consumes none; produces a concrete probe I1 adapts to C2's `CredentialProbe`.
- Tests: `TestProbeNeverPrintsTheValue`, `TestProbeRefusedInAnAgentSession`,
  `TestProbeReportsAProviderRefusal`, `TestProbeSkipsPersonalTokens`.
- Command: `go test ./internal/cli/ -run 'TestProbe(NeverPrints|RefusedInAn|ReportsA|SkipsPersonal)' -count=1`.
- Red step: the personal-token test fails until the person prefix is skipped.
- Depends on: WU0 (the gate).
- Commit: `workspace: the credential probe (WU22, D2820, D2825)`.
- Reviewer tier: opus, mandatory independent review.

### WU20: the SA authority
- Acceptance: `Link` grants the workspace IAM account `roles/owner` on the member — and on its
  Firebase project when separate (R10) — from the member owner's own credentials; `Unlink` removes
  it; `Holds` reports the grant; `AddAdmin`/`RemoveAdmin` change the account's token-creator bindings
  and the admin's owner grant on the workspace's own project (R8), printing Google's
  console-invitation step when Google refuses an owner grant by API outside an organization (U8,
  D2825); `AdminBindings` lists the token creators; the account is only ever impersonated.
- Files: `internal/workspace/authority_sa.go` (new), `internal/workspace/authority_sa_test.go` (new).
- Interfaces: consumes C2's output name; produces a concrete SA authority I1 adapts to `Authority`.
- Tests: `TestSALinkGrantsOwner`, `TestSALinkCoversTheFirebaseProject`, `TestSAUnlinkRemovesGrant`,
  `TestSAAdminTokenCreatorAddedAndRemoved`, `TestSAAddAdminPrintsTheInviteStep`, `TestSANeverReadsAKey`.
- Command: `go test ./internal/workspace/ -run TestSA -count=1`.
- Red step: the link test fails until its fake records the binding.
- Depends on: C2.
- Commit: `workspace: the SA authority (WU20, D2803, D2818, R8, R10)`.
- Reviewer tier: opus, mandatory independent review.

### WU4: member operations
- Acceptance: `ReadGrants`/`WriteGrants` read and write the member's `config/admin/access` with its
  ETag in today's format (decoded back with `access.Decode`); `ReadMirror`/`WriteMirror` read and
  write `config/admin/workspace-members`; `Audit` creates entries under
  `config/admin/workspace-audit` (both already denied to clients by the `config/admin` rules);
  `Bindings` reads the member's IAM policy and names the member's own Terraform accounts and the
  workspace IAM account; `PendingApply` compares the roster's derived launcher and operator lists
  with the installation outputs `launchers` and `operators`, read from the member's state bucket,
  and the derived admin list with the published admin-list object (`access.PathAdminList`, D1940),
  and reports viewers and repository-scope grants as "not measured" (no output carries them);
  `WriteGrants` refuses on unmigrated lists, as `fugaro user grant` does.
  Nothing here runs Terraform.
- Files: `internal/cli/workspace_memberops.go` (new), `internal/cli/workspace_memberops_test.go` (new),
  `internal/cli/workspace_member_audit.go` (new).
- Interfaces: consumes C1's `MirrorEntry` and `AuditEvent`; produces a concrete member writer I1
  adapts to C2's `MemberOps` (except `Offboard`).
- Tests: `TestMemberGrantsWriteRefusesStaleETag`, `TestMemberGrantsDecodeWithTodaysAccess`,
  `TestPendingApplyNamesUnappliedOperators`, `TestPendingApplyNamesAnUnpublishedAdmin`,
  `TestPendingApplyViewersNotMeasured`, `TestMemberGrantsRefuseUnmigratedLists`,
  `TestBindingsLabelsFugaroAccounts`, `TestMemberAuditCreateOnly`.
- Command: `go test ./internal/cli/ -run 'TestMemberGrants|TestPendingApply|TestBindingsLabels|TestMemberAudit' -count=1`.
- Red step: the pending test fails until the outputs' lists are read.
- Depends on: C1.
- Commit: `workspace: read and write a member's roster, mirror and audit (WU4, D2801, D2811, D2813)`.
- Reviewer tier: opus, mandatory independent review.

### WU5: `workspace project add`
- Acceptance: run by the workspace project's owner on the machine holding the member table (D2821),
  it carries `workspace member add`'s flags and table entry unchanged (the record is still published
  by `fugaro update`); on a linked member it adopts the member's grantees into the workspace node
  (outside the domains only with `--external`), writes the stored grant of each workspace admin
  who also holds the live binding (D2818, D2823; the mark kept in the workspace node) and the mirror; on an unlinked member it records the entry
  and says the member owner runs `fugaro workspace project link`, reading no document of the member's own database (R14: the
  mirror and the parent folder are readable once linked, so R2's refusal for a project this workspace does not hold is the
  authority's own, made at `link`); a held member whose database carries a member list that is not this workspace's is refused
  (R2, R14); and it strips nothing (D2805).
- Files: `internal/cli/workspace_project.go` (new), `internal/cli/workspace_project_test.go` (new).
- Interfaces: consumes C1 `Store`, `AuditSink`; C2 `Authority.Holds`, `MemberOps`.
- Tests: `TestProjectAddAdoptsGrants`, `TestProjectAddWaitsForLink`, `TestProjectAddRefusesOtherWorkspace`,
  `TestProjectAddNeedsExternalFlag`, `TestProjectAddKeepsMemberFlags`, `TestProjectAddOnlyOnTheTablesMachine`,
  `TestProjectAddPropagatesOnlyBoundAdmins`.
- Command: `go test ./internal/cli/ -run TestProjectAdd -count=1`.
- Red step: the adopt test fails against its fake's empty roster.
- Depends on: C1, C2.
- Commit: `workspace: project add (WU5, D2802, D2805, D2821)`.
- Reviewer tier: opus, mandatory independent review.

### WU18: `workspace project link|unlink`
- Acceptance: run on the member owner's machine with the member's own config selected and its pin
  naming the workspace (`fugaro workspace add`), `link` calls `Authority.Link` with the owner's own
  credentials and writes a member audit entry (the mirror is written by the workspace owner's
  `project add`, since a member owner cannot read the roster); `unlink` drops only the authority —
  the folder move back or the SA's owner grant — and writes a member audit entry, and says that the
  workspace admins' stored grants stay until the workspace project owner's `project rm` (D2822);
  in the folder model `link` reports the policy difference the move makes — the member's project loses
  the IAM and organization policies it inherited from its old parent and takes on the workspace
  folder's — naming the policies it can read on both sides, so the member owner sees what the move
  changes before it is made; both refuse on a config that is not the member's or whose pin names
  another workspace.
- Files: `internal/cli/workspace_project_link.go` (new), `internal/cli/workspace_project_link_test.go` (new).
- Interfaces: consumes C2 `Authority`, `MemberOps.Audit`.
- Tests: `TestLinkNeedsTheMembersOwnConfig`, `TestLinkRefusesOtherWorkspacePin`, `TestUnlinkDropsOnlyTheAuthority`.
- Command: `go test ./internal/cli/ -run 'TestLink|TestUnlink' -count=1`.
- Red step: the own-config test fails until the pin is read.
- Depends on: C2.
- Commit: `workspace: project link and unlink, the member owner's consent (WU18, D2800, D2819, D2822)`.
- Reviewer tier: opus, mandatory independent review.

### WU6: `workspace project rm`
- Acceptance: run by the workspace project's owner (D2821); saves the state by default through
  `StateWriter` to the member's E6 repository (D2807) and a local file, `--no-save-state` skips it;
  removes the workspace admins' stored grants and the mirror from the member, also after the member
  owner's `unlink` (D2822); on the SA mechanism
  removes the account's grant; on the folder mechanism prints `fugaro workspace project unlink` for
  the member owner (U6); prints the running-cost warning naming the rebuild Scheduler jobs and the
  credentials service, and R3's console link; deletes no GCP or Firebase resource.
- Files: `internal/cli/workspace_project_rm.go` (new), `internal/cli/workspace_project_rm_test.go` (new).
- Interfaces: consumes C1 `Store`; C2 `Authority`, `MemberOps`, `StateWriter`.
- Tests: `TestProjectRmSavesByDefault`, `TestProjectRmDeletesNothing`, `TestProjectRmPrintsShutdownLink`,
  `TestProjectRmRemovesWorkspaceAdminGrants`, `TestProjectRmAfterUnlinkRemovesTheGrants`.
- Command: `go test ./internal/cli/ -run TestProjectRm -count=1`.
- Red step: the save test fails against its fake writer.
- Depends on: C1, C2.
- Commit: `workspace: project rm detaches and never deletes (WU6, D2808, D2821)`.
- Reviewer tier: opus, mandatory independent review.

### WU7: `workspace user add|rm|ls` and `workspace domains`
- Acceptance: only workspace admins add or remove members; an address outside the domains needs
  `--external` and is marked external; `--kind sa` records a machine with its expected roles in the
  workspace node only (D2806); `--admin` makes a workspace admin — their folder or token-creator
  binding and their owner grant on the workspace's own project at once (R8), and a stored grant on
  every linked member, applied at that member's next `fugaro update` — and `rm --admin` removes the
  bindings at once and queues every stored grant's removal the same way; the last workspace admin
  cannot be removed; `user rm` calls `MemberOps.Offboard` on every linked member; `user ls`
  aggregates the members' grants, with a `--json` schema; `domains add|rm|ls` edit the list; every
  change writes an audit event.
- Files: `internal/cli/workspace_user.go` (new), `internal/cli/workspace_domains.go` (new),
  `internal/cli/workspace_user_test.go` (new), `schemas/workspace-users.schema.json` (new).
- Interfaces: consumes C1 `Store`, `AuditSink`; C2 `MemberOps`, `Authority`.
- Tests: `TestUserAddNeedsExternal`, `TestUserAddOnlyWorkspaceAdmins`, `TestUserAddMachineWritesNoMember`,
  `TestUserRmOffboardsEveryMember`, `TestRmAdminRemovesBindingsAndQueuesGrants`, `TestLastWorkspaceAdminKept`,
  `TestDomainsRoundTrip`, `TestUserLsJSONSchema`.
- Command: `go test ./internal/cli/ -run 'TestUser(Add|Rm|Ls)|TestRmAdmin|TestLastWorkspaceAdmin|TestDomains' -count=1`.
- Red step: the admin-removal test fails until the binding goes and the grant is queued.
- Depends on: C1, C2.
- Commit: `workspace: members, machines, admins and allowed domains (WU7, D2806, D2812, D2818)`.
- Reviewer tier: opus, mandatory independent review.

### WU8: `workspace user grant|revoke`
- Acceptance: on a linked member (R11), `grant <email> <project> <role> [--scope …]` refuses an
  address the member's mirror does not list (advisory, D2813), applies roles-scopes' subset and
  last-admin refusals (`access.SubsetOf`), writes the member's roster in today's format — which
  needs `roles/firebasedatabase.admin` on the member, as `fugaro user grant` does today, refused with
  that sentence otherwise — audits to the member's `config/admin/workspace-audit` (D2811), and prints
  that the member's own `fugaro update` applies it (D2801); on an unlinked member it refuses and
  names `link`; a project admin needs no grant on the workspace project.
- Files: `internal/cli/workspace_user_grant.go` (new), `internal/cli/workspace_user_grant_test.go` (new).
- Interfaces: consumes C1 `AuditSink`; C2 `MemberOps`.
- Tests: `TestGrantRefusesNonMember`, `TestGrantAsProjectAdminWritesOnlyTheMember`,
  `TestGrantNeedsDatabaseAdmin`, `TestGrantCarriesScope`, `TestRevokeKeepsLastAdmin`,
  `TestGrantNamesTheUpdateThatAppliesIt`.
- Command: `go test ./internal/cli/ -run 'TestGrant|TestRevoke' -count=1`.
- Red step: the non-member test fails against its fake's empty mirror.
- Depends on: C1, C2.
- Commit: `workspace: grant and revoke project roles (WU8, D2801, D2815)`.
- Reviewer tier: opus, mandatory independent review.

### WU9: offboarding a member project
- Acceptance: `fugaro user offboard`'s core is callable for a named member project and provides
  `Offboard` (roster grants dropped, the person's own secrets swept, an audit entry written);
  callable by a workspace admin once their stored grant has reached the member's derived admin list
  (D2818, D1940) — before that it says which member's `fugaro update` is pending and sweeps nothing
  there; a run whose only credential was swept ends with the gateway's existing refusal (U4).
- Files: `internal/cli/offboard.go`, `internal/cli/workspace_offboard.go` (new),
  `internal/cli/workspace_offboard_test.go` (new), `internal/runner/accountchoice_test.go`.
- Interfaces: consumes C2 `MemberOps.Audit`; produces a concrete offboarder I1 adapts to `MemberOps.Offboard`.
- Tests: `TestOffboardMemberSweepsPrefix`, `TestOffboardMemberWaitsForTheAdminList`,
  `TestOffboardMemberAudits`, `TestSweptOnlyCredentialRefusesClearly`.
- Command: `go test ./internal/cli/ -run TestOffboardMember -count=1` and
  `go test ./internal/runner/ -run TestSweptOnlyCredentialRefusesClearly -count=1`.
- Red step: the sweep test fails until the core takes a named project.
- Depends on: C2.
- Commit: `workspace: offboarding reaches every member (WU9, D2813, D2818)`.
- Reviewer tier: opus, mandatory independent review.

### WU11: `workspace project ls` and `status`
- Acceptance: `status [<project>]` prints pass/warn/fail with a fix per row for: reachable; linked
  (`Authority.Holds`; "not linked" names `link`, D2819); credentials service responds; credentials
  stored and not marked broken, and with `--check-credentials` in the owner's own terminal the
  probe's result (D2820); images fresh (the rebuild Scheduler jobs' last success); caps set and use
  against them; grants waiting for the member's `fugaro update` (`PendingApply`, with "not measured"
  where it says so); "unlinked, grants left" after a member owner's `unlink` (D2822); state repo in
  sync (`LastSaved`, saved by `project save` or `project rm`, D2824); drift (`Drift`); admin tampering
  — an `Admins` entry without the live binding, or a binding holder not in `Admins` (D2823); a single
  workspace admin (R1); the workspace IAM account's own **user-managed keys** (`iam.serviceAccountKeys.list`,
  each with its key id and age) and every principal that can **act as** it (`iam.serviceAccounts.actAs`,
  read from the account's policy) — both rows carrying a fix that says in one line that a key outlives
  `rm --admin` and survives until it is deleted on the account or the account disabled, and naming
  `constraints/iam.disableServiceAccountKeyCreation` as the control Fugaro cannot set for the owner, so
  the removal commands' own output and `status` agree that a key is not the binding; every
  member-controlled string escaped; `ls` prints one line per member with its worst row; neither writes anything on any path.
- Files: `internal/cli/workspace_project_status.go` (new), `internal/cli/workspace_project_status_test.go`
  (new), `schemas/workspace-projects.schema.json` (new).
- Interfaces: consumes C1 `Store`; C2 `Authority`, `MemberOps`, `StateWriter`, `Drift`,
  `CredentialProbe`; calls `claudeAccountsStoredCheck` and doctor's brokering reads.
- Tests: `TestStatusRowsInOrder`, `TestStatusWritesNothing`, `TestStatusNotLinked`,
  `TestStatusCheckCredentialsOnlyInOwnTerminal`, `TestStatusSingleAdminWarns`, `TestStatusFlagsAdminTampering`, `TestStatusUnlinkedGrantsLeft`,
  `TestStatusNamesAccountKeysAndActAsHolders` (the account's user-managed keys and the principals that
  can act as it are rows; a key outlives `rm --admin`, and `unlink` bounds it for that member alone),
  `TestStatusEscapesMemberStrings`, `TestProjectLsJSONSchema`.
- Command: `go test ./internal/cli/ -run 'TestStatus|TestProjectLs' -count=1`.
- Red step: the write-nothing test fails until every read goes through the read-only fake.
- Depends on: C1, C2.
- Commit: `workspace: project ls and status (WU11, D2820)`.
- Reviewer tier: standard.

### WU12: drift and the Cloud Audit Logs cross-check
- Acceptance: `ComputeDrift` (not `Drift`, which is C2's stub until I1) reports extra and missing
  grants between a member's IAM policy, its roster and **the workspace node** (never the mirror,
  D2813), and for machines between their bindings and the roles the node records (D2806); it trusts
  only the admins that also hold the live binding, read through `Authority.AdminBindings` (D2823);
  it ignores the member's own Terraform accounts and the workspace IAM account's own grants (its
  owner grant and, in the SA model, the Firebase admin binding the member's own `update` derives from
  project owners); it reports any folder owner or token creator who is not a bound workspace admin
  (R13) and names budget admins as implicit admins; it flags outside-domain principals; it names each
  Cloud Audit Logs `SetIamPolicy` whose resulting binding is drift and which neither audit trail
  holds, and never a member's own `update` (R13).
- Files: `internal/workspace/drift.go` (new), `internal/workspace/drift_test.go` (new),
  `internal/infra/auditlogs.go` (new), `internal/infra/auditlogs_test.go` (new),
  `internal/gcpfake/logging.go`.
- Interfaces: consumes C1's `Person`, `AuditEvent`; C2's `Authority.AdminBindings`; produces
  `ComputeDrift`, which I1 adapts to C2's `Drift`.
- Tests: `TestDriftIgnoresFugaroAccounts`, `TestDriftIgnoresARewrittenMirror`,
  `TestDriftTrustsOnlyBoundAdmins`, `TestDriftReportsAFolderOwnerWhoIsNotAnAdmin`,
  `TestDriftChecksMachinesAgainstTheRecord`, `TestDriftExtraAndMissing`,
  `TestDriftNamesConsoleChangeNotUpdate`, `TestAuditLogsReadsSetIamPolicy`.
- Command: `go test ./internal/workspace/ ./internal/infra/ -run 'TestDrift|TestAuditLogsReadsSetIamPolicy' -count=1`.
- Red step: the rewritten-mirror test fails until drift reads the workspace node.
- Depends on: C1, C2.
- Commit: `workspace: drift against the workspace node and the audit logs (WU12, D2806, D2811, D2813, D2823, R13)`.
- Reviewer tier: opus, mandatory independent review.

### WU13: `workspace project reconcile`
- Acceptance: adds missing grants and removes extras on the member's roster from `Drift` (people
  only; a machine's drift is reported, never written, D2806; an admin grant only for an admin who
  holds the live binding, D2823), audits each change, and prints that
  the member's own `fugaro update` applies them; `--dry-run` writes nothing.
- Files: `internal/cli/workspace_project_reconcile.go` (new), `internal/cli/workspace_project_reconcile_test.go` (new).
- Interfaces: consumes C1 `AuditSink`; C2 `Drift`, `MemberOps`.
- Tests: `TestReconcileDryRunWritesNothing`, `TestReconcileRemovesExtra`, `TestReconcileLeavesMachines`.
- Command: `go test ./internal/cli/ -run TestReconcile -count=1`.
- Red step: the remove test fails against its fake's empty report.
- Depends on: C1, C2.
- Commit: `workspace: reconcile a member's roster (WU13, D2809)`.
- Reviewer tier: opus, mandatory independent review.

### WU15: the state writer
- Acceptance: `Save` writes the project's settings and spend summary — and, for the workspace, its
  audit export — always to a local file it names, and, when the E6 designated repository is set, as
  a pull request: clone, commit to a new branch, push, and open the pull request through the
  repository's own provider (`gh` for GitHub; `internal/gitprov` for Bitbucket, which has no CLI),
  from the owner's terminal with the owner's own credentials (D2807); before it pushes it prints the
  target repository and where its setting came from, and it refuses a public repository, naming a
  `--repo` override, because the snapshot carries addresses (D2824); it refuses in a coding agent's
  session; `LastSaved` reads the last snapshot's time.
- Files: `internal/cli/workspace_state.go` (new), `internal/cli/workspace_state_test.go` (new).
- Interfaces: consumes C1's `AuditEvent`; produces a concrete writer I1 adapts to `StateWriter`.
- Tests: `TestStateSaveAlwaysWritesLocal`, `TestStateSaveOpensPR`, `TestStateSaveRefusesAPublicRepo`,
  `TestStateSaveNamesTheTargetAndItsSource`, `TestStateSaveRefusedInAgentSession`, `TestLastSaved`.
- Command: `go test ./internal/cli/ -run 'TestStateSave|TestLastSaved' -count=1`.
- Red step: the public-repository test fails until the visibility is read.
- Depends on: C1.
- Commit: `workspace: state snapshots to the designated repository (WU15, D2807, D2824)`.
- Reviewer tier: opus, mandatory independent review (it pushes to a repository with the owner's credentials).

### WU23: `workspace project save`
- Acceptance: run by the workspace project's owner, `project save [<project>]` saves one member's
  state, or every member's with no argument, and `--audit` saves the workspace's audit export,
  through `StateWriter` (D2824); it is the only saver besides `project rm`; it refuses in a coding
  agent's session and asks nothing.
- Files: `internal/cli/workspace_project_save.go` (new), `internal/cli/workspace_project_save_test.go` (new).
- Interfaces: consumes C1 `Store`; C2 `StateWriter`.
- Tests: `TestProjectSaveOneMember`, `TestProjectSaveEveryMember`, `TestProjectSaveAuditExport`.
- Command: `go test ./internal/cli/ -run TestProjectSave -count=1`.
- Red step: the one-member test fails against its fake writer.
- Depends on: C1, C2.
- Commit: `workspace: project save (WU23, D2824)`.
- Reviewer tier: opus.

### I1: wire the workspace commands
- Acceptance: the stubs are deleted; C1's and C2's interfaces are adapted onto WU1, WU2, WU20, WU4,
  WU9, WU12 (`ComputeDrift`), WU15 and WU22; `newWorkspaceCmd` registers `init`,
  `project add|ls|rm|status|reconcile|link|unlink|save`, `user add|rm|ls|grant|revoke` and
  `domains add|rm|ls`, each wrapped with the reporter (D1354), and removes `workspace member` in the
  same change; the census, the no-prompt harness, the CLI reference and `update_workspace.go`'s
  refusal text and test name the new commands.
- Files: `internal/workspace/roster_stub.go` (deleted), `internal/workspace/memberops_stub.go`
  (deleted), `internal/cli/workspace_wiring.go` (new), `internal/cli/workspace.go`,
  `internal/cli/workspace_test.go`, `internal/cli/report_exempt.go`,
  `internal/cli/noprompt_harness_test.go`, `internal/cli/update_workspace.go`,
  `internal/cli/update_workspace_test.go`, `website/content/cli-reference.md` (regenerated).
- Interfaces: joins every part.
- Tests: `TestWorkspaceCommandsRegistered`, `TestEveryCommandIsOnTheReporterOrExempt`,
  `TestEveryMutatingCommandWritesALog`, `TestEveryCommandRunsWithClosedStdin`,
  `TestCoreCommandsAreInTheReference`, `ee/clidoc`'s CLI-reference comparison.
- Command: `go test ./internal/cli/ -run 'TestWorkspaceCommandsRegistered|TestEveryCommandIsOnTheReporterOrExempt|TestEveryMutatingCommandWritesALog|TestEveryCommandRunsWithClosedStdin|TestMembership|TestWorkspaceRemoval|TestUpdate.*Workspace' -count=1`,
  `go test ./internal/clidoc/ -run TestCoreCommandsAreInTheReference -count=1`,
  `go test ./ee/clidoc/ -count=1`, and `npm run check` in `website/`.
- Red step: the registration test fails while either stub is wired.
- Depends on: WU1, WU2, WU20, WU3, WU4, WU5, WU18, WU6, WU7, WU8, WU9, WU11, WU12, WU13, WU15, WU22,
  WU23, WU24.
- Commit: `workspace: wire projects and users (I1)`.
- Reviewer tier: opus, mandatory independent review. Ten files: two deletions, one regeneration and
  five census or text lines; the end-to-end test is WU21.

### WU21: the end-to-end test
- Acceptance: `TestWorkspaceProjectsUsersEndToEnd` passes against `internal/gcpfake`: init (folder) →
  project add (waits) → link (member owner) → project add (adopts) → user add (refused, then
  `--external`) → grant (pending) → the member's `fugaro update` simulated by writing the launcher
  and operator lists into the fake's installation outputs → status (clean) → a binding added straight
  in the fake → status names the drift and the audit-log entry → reconcile → user rm (prefix swept
  once the admin list is published) → `--admin` removal (binding gone, grants queued) → project rm
  (state saved locally, nothing deleted), with WU10's refusals in place.
- Files: `internal/cli/workspace_e2e_test.go` (new), `internal/gcpfake/iam.go`.
- Interfaces: none.
- Tests and command: `go test ./internal/cli/ -run TestWorkspaceProjectsUsersEndToEnd -count=1`.
- Red step: it fails on the first command any part leaves unwired.
- Depends on: I1 and WU10 (behaviour: the real commands, with the old doors refused, which share
  `offboard.go` with `workspace user rm`).
- Commit: `workspace: projects and users end to end (WU21)`.
- Reviewer tier: standard.

### WU10: installation-level refusals on a linked member
- Acceptance: on a **linked** member (R11) — its mirror present and `Authority.Holds` true —
  `fugaro user add|rm|grant|revoke|offboard` and `fugaro setup|update --operator/--admin/--budget-admin`'s
  fold into the roster (R12) refuse, naming the `fugaro workspace user …` command, and the existing
  hints that name those doors (`offboard.go`'s `fugaro update --admin`, `user_grant.go`'s
  `--budget-admin`) name the workspace command there instead; `fugaro workspace user rm` still
  offboards through WU9's core; on an unlinked member, and on any other project, nothing changes.
- Files: `internal/cli/user.go`, `internal/cli/user_grant.go`, `internal/cli/offboard.go`,
  `internal/cli/update_access.go`, `internal/cli/user_test.go`.
- Interfaces: reads C2's `ReadMirror` through I1's wiring.
- Tests: `TestUserCommandsRefuseOnALinkedMember`, `TestUserCommandsUnchangedOnAnUnlinkedMember`,
  `TestOperatorFlagFoldRefusedOnALinkedMember`, `TestUserCommandsUnchangedOutsideWorkspace`,
  `TestWorkspaceUserRmStillOffboards`.
- Command: `go test ./internal/cli/ -run 'TestUserCommands|TestOperatorFlagFold|TestWorkspaceUserRmStillOffboards' -count=1`.
- Red step: the linked-member refusal test fails today.
- Depends on: I1 (behaviour: the commands the refusal names must exist).
- Commit: `cli: installation-level user commands defer to the workspace on linked members (WU10, D2813, R11, R12)`.
- Reviewer tier: opus, mandatory independent review.

### WU16: docs and skills
- Acceptance: the plugin skills and docs name the new commands, the opt-in link, the advisory
  mirror and the update lag of a grant; the docs say the audit trails are not tamper-proof (D2811);
  `docs/workspaces/README.md` links here; no release file is written (the summary is due in the
  release's own PR).
- Files: `plugin/skills/working/SKILL.md`, `docs/workspaces/README.md`,
  `internal/cli/docs_workspace_projects_users_test.go` (new).
- Tests: the plugin's command-name tests; the new docs test.
- Command: `go test ./plugin/... ./internal/cli/ -run 'Docs|Skill' -count=1`.
- Red step: the new docs test pins the skill's sentence on `workspace project link` and fails until it is written.
- Depends on: I1 (behaviour: the plugin tests need the real commands).
- Commit: `docs: workspace projects and users (WU16)`.
- Reviewer tier: opus (plugin skills).

### WU17: Checks 720 and 721
- Acceptance: two owner-run scripts with offline tests: **Check 720** creates a test folder, runs
  `workspace init`, `project link`, `project add`, `project rm` and `project unlink` on a throwaway
  project, checks the folder move and its return (U1, U6), checks whether an owner grant by API
  succeeds outside an organization (U8), and prints R3's console link for the owner to open (U3);
  **Check 721** grants a role in the
  console, runs `status` and checks the drift row names the audit-log entry and does not name the
  member's own update (U5).
- Files: `scripts/live-checks/check-720.sh` (new), `scripts/live-checks/check-720_test.sh` (new),
  `scripts/live-checks/check-721.sh` (new), `scripts/live-checks/check-721_test.sh` (new),
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`.
- Tests: `TestLiveCheckScripts`, each script's own `_test.sh`, and the docs tests that pin the checklist.
- Command: `go test ./scripts/ -run TestLiveCheckScripts -count=1` and `go test ./internal/cli/ -run Docs -count=1`.
- Red step: the live-checks test fails until both scripts are listed.
- Depends on: I1 (behaviour: the scripts drive the real commands).
- Commit: `live-checks: Checks 720 and 721 (WU17)`.
- Reviewer tier: standard. Six files, four of them the two scripts and their tests.
- Owner-run: `scripts/live-checks/check-720.sh --org <org-id> --billing <account>` then
  `scripts/live-checks/check-721.sh --workspace <name> --project <id>`, in the owner's terminal. No
  agent runs any step of either.

## 4. Parallel execution

**Levels** (tasks in one level run at once), as `fugaro plan graph` computes them:

| Level | Tasks |
| --- | --- |
| 0 | WU0 |
| 1 | C1, C2, WU1, WU2, WU3, WU22 |
| 2 | WU20, WU24, WU4, WU5, WU18, WU6, WU7, WU8, WU9, WU11, WU12, WU13, WU15, WU23; the anticipated contract changes R-member-firebase and R-roster-sa, if raised |
| 3 | I1 |
| 4 | WU10, WU16, WU17 |
| 5 | WU21 |

**Critical path:** see §5a. Sequential by necessity: WU0 first (D2800's gate: its review may
reshape both contracts), I1 after every side it joins, and the refusals (WU10), the docs, the live
checks and the end-to-end test after the commands exist. WU1, WU2, WU3 and WU22 are pure providers:
they consume no contract and expose concrete implementations I1 adapts, so they run beside the
contracts. D2803's folder-first order is a launch order inside level 1 and 2, not an edge.

**Shared files** (no level holds two writers of one file):

| File | Writers, by level |
| --- | --- |
| `internal/cli/workspace.go`, `workspace_test.go`, `update_workspace.go`, `update_workspace_test.go` | I1 (3) |
| `internal/cli/offboard.go` | WU9 (2), WU10 (4) |
| `internal/runner/accountchoice_test.go` | WU9 (2) |
| `internal/infra/folder.go`, `folder_test.go` | WU2 (1) |
| `internal/infra/auditlogs.go`, `auditlogs_test.go` | WU12 (2) |
| `internal/cli/workspace_init.go`, `workspace_init_test.go` | WU24 (2) |
| `internal/cli/user.go`, `user_grant.go`, `user_test.go`, `update_access.go` | WU10 (4) |
| `internal/gcpfake/crm.go` | WU2 (1) |
| `internal/gcpfake/logging.go` | WU12 (2) |
| `internal/gcpfake/iam.go` | WU21 (5) |
| `deploy/terraform/gcp/roots/workspace/*`, `deploy/terraform/terraform_test.go`, `internal/infra/tf/workspace.go`, `workspace_test.go` | WU3 (1) |
| `internal/workspace/roster_stub.go`, `memberops_stub.go` | C1, C2 (1); I1 deletes them (3) |
| `internal/workspace/roster.go`, `rosterstore.go`, `memberops.go`, the two contract documents, the two contract schemas | C1, C2 (1) and their changes (2) only, never the same file in one level |
| `docs/workspaces/README.md` | WU0 (0), WU16 (4) |
| `SECURITY.md`, `website/content/security.md`, `docs/design/v1.md` | WU0 (0) |
| `website/content/cli-reference.md`, `report_exempt.go`, `noprompt_harness_test.go` | I1 (3) |
| `schemas/workspace-projects.schema.json`, `workspace-users.schema.json` | WU11, WU7 (2), one each |

## 5. The dependencies broken and kept

Broken, each by a contract and re-joined by I1 (each side needed only the shape):

- **By C1 (the roster):** WU5, WU6, WU7, WU11, WU23, WU24 → WU1 (they read and write through `Store`); WU8, WU13 →
  WU1 (they write an `AuditEvent`); WU4 → WU1 (the mirror is `MirrorEntry`); WU12 → WU1 (drift reads
  people, kinds and machines' roles); WU15 → WU1 (the audit export reads `AuditEvent`).
- **By C2 (member operations):** WU5, WU6, WU7, WU8, WU11, WU13, WU18 → WU4 (they call `MemberOps`);
  WU9 → WU4 (it writes through `MemberOps.Audit`); WU5, WU6, WU7, WU11, WU18 → WU2 (they call
  `Authority`); WU20 → WU3 (the output `workspace_iam_account`); WU7 → WU9 (`user rm` calls
  `Offboard`); WU11, WU13 → WU12 (they consume `DriftReport`); WU6, WU11 → WU15 (they call
  `StateWriter`); WU11 → WU22 (it calls `CredentialProbe`); WU23 → WU15 (it calls `StateWriter`);
  WU24 → WU2 and WU24 → WU20 (`workspace init` calls `Authority`); WU12 → WU2 and WU12 → WU20 (drift
  reads `Authority.AdminBindings`).

Kept:

- WU0 → C1, C2, WU1, WU2, WU3, WU22: D2800's mandatory review gates every task, and its findings may
  change the shapes — a contract written before it would be rewritten.
- C1/C2 → each consumer: the contract is what lets it run beside its provider.
- The parts → I1: I1 joins them. I1 → WU10, WU16, WU17, WU21: the refusals name, and the skills'
  tests, the live checks and the end-to-end test drive, the real commands (behaviour). WU10 → WU21:
  the end-to-end test runs `workspace user rm` through `offboard.go` with WU10's refusals in place.
- C1 → R-roster-sa and C2 → R-member-firebase: a change amends a file its contract creates.

### 5a. What the graph read reports

`fugaro plan graph --plan docs/workspace-projects-users/implementation-plan.md`, 2026-11-18, after
review round 3: no refusal, six levels matching §4; **36 edges broken, 5 kept** in the read's own
count, **5 tasks added** (C1, C2, I1 and the two anticipated contract changes); the critical path
goes from **7 tasks, weight 13.5** before the pass to **6 tasks, weight 9.5** after it — WU0 → C1 →
WU11 → I1 → WU10 → WU21 — "improves". The weights are the plan's own `size` guesses (a contract 0.5,
the largest tasks 3); no run history exists yet for any of them.

## 6. Contract changes anticipated

- **R-member-firebase** on C2, raised by WU4: if a member's Firebase project cannot be read from its
  outputs, every `MemberOps` method takes it as an argument. Reaches WU5, WU8, WU11, WU18.
- **R-roster-sa** on C1, raised by WU7: if a machine member needs an owner or an expiry on `Person`.
  Reaches WU5, WU12.

## 7. Checks this plan takes

From the block **Checks 720–729**: **Check 720** (the folder link, move and its return, U1, U6) and
**Check 721** (console drift named from Cloud Audit Logs, a member's own update not named, U5), both
owner-run (WU17). 722–729 reserved.

## 8. After-deploy steps

None: `fugaro update` gains no stage or step (R7, D2819), so the release note's `### For operators`
is "run `fugaro update`". People management is turned on per member by its owner's
`fugaro workspace project link`, a feature's first use rather than an upgrade step.

## 9. Considered and declined

- Writing member-project bindings from the workspace, or applying a member's Terraform from another
  machine (D2801 and its round-1 amendment, F7).
- An organization policy for domains (D2812's alternative).
- A read or write grant on the workspace database for project admins (D2813, D2817 amended).
- A binding gate on project admins' grants: the mirror is advisory and drift is the fence (D2813).
- Making the workspace sign-on's admission read the roster (D2810: not the owner's answer).
- An `update` stage that seeds the roster, or a rules change for it (first-draft WU14, R7).
- Linking existing members on their next `update` (D2819's alternative).
- Storing machine grants or a workspace mark in the member roster (D2806, D2818 as corrected: a
  format migration older member CLIs would refuse).
- Saving state through periodic-reviews R9 (D2807 as amended: R9 carries reports only).
- `project add|rm` by any workspace admin (D2821's alternative).
- Giving members' admins `roles/datastore.user` for audit (D2811's round-2 alternative).
- Carrying the workspace's mark in the member's mirror so a member owner's `unlink` removes the
  grants (D2822's recommendation, not the owner's answer).
- Saving state on `project rm` only, without the in-sync row and the audit export (D2824's
  recommendation, not the owner's answer).
- A credentials-service route for the probe, or probing personal tokens (D2825).
- Moving the roster out of the database (D2823's alternative).
- Linking fugaro-prod app sign-ins (D2814): no task.

## 10. Cost

26 runs (WU0, C1, C2, WU1–WU13 without WU14, WU15, WU18, WU20–WU24, I1, WU16, WU17), none launched;
the two anticipated contract changes are two more only if raised. The gateway enforces each run's
cap (`fugaro budget set --repo`); the worst case is 26 times that cap, plus one independent review
run for each of the 21 tasks marked "mandatory independent review" (WU0, C1, C2, WU1, WU2, WU3,
WU22, WU20, WU24, WU4, WU5, WU18, WU6, WU7, WU8, WU9, WU12, WU13, WU15, I1, WU10). The owner's go so
far covers **WU0 only**, once this plan is on `main`.
