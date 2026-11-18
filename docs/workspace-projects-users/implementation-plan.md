# Workspace projects and users: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). **Nothing is
implemented and no task is approved**: each task needs the owner's explicit go by name. The plan is
written to the answers of 2026-11-18 (D2800–D2819, every one as recommended; D2801, D2802, D2811,
D2813 and D2817 amended and D2818–D2819 added that day, reconciliation O3 and O5), and the graph
beside it is [implementation-plan.graph.json](implementation-plan.graph.json).

**The repository's settings.** `rigor.default` is `startup` with no area rules, and
`planning.decoupling` is on (read with `fugaro settings planning.decoupling rigor.default rigor.areas
--repo dimipaun/fugaro --json` on 2026-11-18), so the contract-first pass decouples aggressively.
Because D2800 puts IAM authority over members in this design, **every task that writes IAM, a roster,
a mirror, an audit trail or a credential is reviewed at opus with a mandatory independent review**,
whatever the mode; the tasks say which.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/terraform.md` (WU3), `.claude/rules/credentials-service.md`
(WU0: `website/content/security.md` is `SECURITY.md` regenerated with `go run ./internal/clidoc`;
WU9), `.claude/rules/website.md` (I1's CLI reference), `.claude/rules/ee.md` (nothing here imports
`ee/`; the sign-on's verifier stays in `ee/coordinator/oauth`, D2810).

- **Always.** Red test first; targeted tests only (`go test ./internal/cli/ -run 'TestX' -count=1`);
  `gofmt -l .` prints nothing; hermetic tests against `internal/gcpfake` and `t.TempDir()`; JSON
  output gets a schema in `schemas/` and decodes strictly; every write command refuses in a coding
  agent's session (A7), asks nothing (a refusal names the flag, e.g. `--external`,
  `--no-save-state`) and goes through the command reporter (D1351). **A task's tests use fakes
  declared in its own `_test.go` files, never the contract's stub**, so I1 can delete the stubs
  without rewriting them. Comments say why.
- **Ask first.** A new dependency; any change to a contract's files (raise a contract change
  `R-…`, never work around it); any change to `fugaro update`'s stages (this plan adds none, R7).
- **Never.** A secret-shaped literal; a live cloud call from a test or an agent; a write from
  `workspace project status`; a binding on a member project written outside that member's own
  Terraform (D2801, A6); a Terraform apply of one installation from another installation's machine
  (F7); a workspace service account that is also the credentials service's account (F20); an SA key
  (D2803); an edit under `.github/workflows/`.
- **The HARD RULE.** `fugaro update` gains nothing here: the roster node is created by its first
  write (R7), linking is opt-in (D2819), and an existing workspace at the previous version keeps
  working with `update` alone. The `workspace member` rename lands in I1, in the same change that
  registers its replacement.

## 2. Global constraints and review focus

| Constraint the spec implies | Pinned by |
| --- | --- |
| The authority is given by the member's owner on their own machine, never taken (D2800, D2819) | WU18: `link` refuses unless the selected config is the member's own and its pin names the workspace |
| No path needs a write on the workspace's database for a project admin (F11, D2811, D2817) | WU8 test: a grant by a project admin writes only the member's roster and the member's Firestore |
| The workspace credentials service's account still holds no write on a member (D1299) | WU3's tftest |
| Removing a workspace admin removes every grant and binding they held, in one command (D2818) | WU7 `TestRmAdminRemovesEveryGrantAndBinding` |
| Drift is measured against the workspace node, never the mirror (D2813) | WU12 `TestDriftIgnoresARewrittenMirror` |
| Drift never reports Fugaro's own accounts, the workspace IAM SA or folder inheritance (D2806) | WU12 `TestDriftIgnoresFugaroAccounts` |
| A project already in another workspace or folder is refused (R2) | WU5, WU18 tests |
| Nothing is stripped at join (D2805) | WU5 test |
| `status` writes nothing on any path, `--json` included | WU11 test against a fake that fails on any write |
| The roster never lives under the published record (R5) | WU1 test: a roster write leaves `workspaces/<name>`'s ETag unchanged |

## 3. Slices and tasks

Four slices. **S0** (WU0) is the gate. **S1** (C1, C2, and the pure providers WU1, WU3) settles the
shapes and builds what consumes none. **S2** builds every other part against the shapes, side by
side. **S3** (I1, then WU10, WU16, WU17, WU21) wires, refuses the old doors, documents and hands the
owner the live checks. D2803's "folder first": WU2 (folder) is launched before WU20 (SA) in its
level, and Check 720 exercises the folder before any SA link is tried live. WU14 of the first draft
was dropped in review round 1 (R7); its number is not reused.

### WU0: the threat model, rewritten for workspace authority
- Acceptance: `threat-model.md` states what a stolen workspace-admin credential, a compromised
  workspace IAM account, a hostile project admin (who can rewrite the mirror and the member's own
  audit, D2813, D2811) and a hostile member owner each reach, at their real size; it says which of
  `docs/workspaces/spec.md` §9's rules the addendum supersedes ("never widens a member", "never
  writes a member's database", "never a second permission model") and on what consent; `SECURITY.md`
  and `docs/design/v1.md` say the same; an independent security review (`fugaro review --pr N`, run
  by the coordinator) has returned and the owner has answered every finding before any other task
  launches.
- Files: `docs/workspace-projects-users/threat-model.md` (new), `SECURITY.md`,
  `website/content/security.md` (regenerated), `docs/design/v1.md`,
  `internal/cli/docs_workspaces_security_test.go`, `docs/workspaces/README.md`.
- Interfaces: consumes reconciliation §3–§4; produces the threat model C1 and C2 are reviewed against.
- Tests: `docs_workspaces_security_test.go`'s pins, re-pointed at the superseding sentences;
  `docs_website_security_test.go`.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1`.
- Red step: a new pin for the superseding sentence in `docs_workspaces_security_test.go` fails until
  `SECURITY.md` and `docs/design/v1.md` carry it.
- Depends on: none.
- Commit: `docs(workspace-projects-users): threat model for workspace IAM authority (WU0, D2800)`.
- Reviewer tier: opus, mandatory independent review. Six files: four are one sentence each.

### C1: the roster contract
- Acceptance: the roster's Go types (`Roster`, `Person` with `Kind` human|sa and `External`,
  `Domains`, `Admins`, `AuditEvent`, `MirrorEntry`; R6), its path `workspaceRosters/<name>` (R5),
  its JSON Schema, the audit event's schema, the `Store` (ETag read, conditional write and create)
  and `AuditSink` interfaces and their stubs exist, with a contract document saying what each side may
  rely on; the stubs take the arguments, return the shape and do nothing else.
- Files: `internal/workspace/roster.go` (new), `internal/workspace/rosterstore.go` (new),
  `internal/workspace/roster_stub.go` (new), `schemas/workspace-roster.schema.json` (new),
  `schemas/workspace-audit-event.schema.json` (new), `docs/workspace-projects-users/contract.md` (new).
- Interfaces: produces the roster shape every consumer reads or writes.
- Tests: `TestRosterSchemaRoundTrip`, `TestRosterDecodeRefusesUnknownField`, the `schemas` package tests.
- Command: `go test ./internal/workspace/ ./schemas/ -run 'TestRoster|TestSchema' -count=1`.
- Red step: the round-trip test fails until the types and the schema agree.
- Depends on: WU0 (its review may reshape the roster).
- Commit: `workspace: the roster contract (C1, D2817)`.
- Reviewer tier: opus, mandatory independent review (a wire format over IAM). Six files, two of
  them schemas.

### C2: the member-operations contract
- Acceptance: interfaces `MemberOps` (`ReadGrants`, `WriteGrants`, `ReadMirror`, `WriteMirror`,
  `Bindings`, `PendingApply`, `Offboard`, `Audit` to the member's own Firestore), `Authority`
  (`Kind`, `Link`, `Unlink`, `Holds`, `AddAdmin`, `RemoveAdmin`), `StateWriter` (`Save`,
  `LastSaved`) and `Drift(...) DriftReport` exist with stubs and a contract document, which also
  fixes the name of the workspace root's IAM-account output (WU3 writes it, WU20 reads it); every
  method names the member's GCP project and its Firebase project separately (F2).
- Files: `internal/workspace/memberops.go` (new), `internal/workspace/memberops_stub.go` (new),
  `docs/workspace-projects-users/contract-member.md` (new).
- Interfaces: produces what the consumers call; WU2, WU4, WU9, WU12 and WU15 provide concrete types
  I1 adapts onto it.
- Tests: `TestMemberOpsStubShapes`.
- Command: `go test ./internal/workspace/ -run TestMemberOps -count=1`.
- Red step: the shape test fails until each stub returns its declared zero shape.
- Depends on: WU0.
- Commit: `workspace: the member-operations contract (C2, D2801, D2803, D2818)`.
- Reviewer tier: opus, mandatory independent review.

### WU1: the roster node and the workspace audit writer
- Acceptance: the roster lives at `workspaceRosters/<name>` in the workspace's database, written
  only conditionally (a create when absent, an ETag write otherwise; a concurrent change is re-read,
  never overwritten), and a roster write leaves `workspaces/<name>` untouched (R5); the rules deny
  every client there; the audit writer creates Firestore documents and never updates one.
- Files: `internal/workspace/roster_rtdb.go` (new), `internal/workspace/roster_rtdb_test.go` (new),
  `internal/workspace/audit_firestore.go` (new), `internal/budget/rules/workspace.json.tmpl`,
  `internal/budget/rules/testdata/workspace.golden.json`.
- Interfaces: consumes none; produces `RosterNode` (raw JSON and ETag) and `AuditWriter`
  (create-only documents) in basic types; I1 adapts C1's `Store` and `AuditSink` onto them.
- Tests: `TestRosterWriteRefusesStaleETag`, `TestRosterCreateWhenAbsent`,
  `TestRosterWriteLeavesRecordETag`, `TestAuditWriterCreateOnly`, the rules golden case.
- Command: `go test ./internal/workspace/ ./internal/budget/rules/ -run 'TestRoster|TestAuditWriter|Golden' -count=1`.
- Red step: the stale-ETag test fails until the write carries `if-match`.
- Depends on: WU0 (the gate).
- Commit: `workspace: the roster node and the audit writer (WU1, D2811, D2817, R5)`.
- Reviewer tier: opus, mandatory independent review.

### WU2: the folder authority and `workspace init`
- Acceptance: `workspace init` detects an organization the caller can create folders in and
  creates the workspace's folder with the workspace admins as `roles/owner` on it, else records the
  SA mechanism; the folder authority's `Link` moves a project into the folder only from the member
  owner's own credentials and records its original parent on the roster entry, refuses a project
  under another workspace's folder (R2); `Unlink` moves it back; `Holds` reports the project's
  parent; `AddAdmin`/`RemoveAdmin` change the folder's owner binding.
- Files: `internal/infra/folder.go` (new), `internal/gcpfake/crm.go`,
  `internal/workspace/authority_folder.go` (new), `internal/workspace/authority_folder_test.go` (new),
  `internal/cli/workspace_init.go` (new; constructor only, registered by I1).
- Interfaces: consumes C1's roster entry; produces a concrete folder authority I1 adapts to C2's
  `Authority`.
- Tests: `TestWorkspaceInitPicksFolderWithOrg`, `TestWorkspaceInitRecordsSAWithoutOrg`,
  `TestFolderLinkRefusesOtherWorkspaceFolder`, `TestFolderUnlinkRestoresParent`,
  `TestFolderAdminBindingAddedAndRemoved`.
- Command: `go test ./internal/infra/ ./internal/workspace/ ./internal/cli/ -run 'TestWorkspaceInit|TestFolder' -count=1`.
- Red step: the init test fails until the fake answers an organization search.
- Depends on: C1.
- Commit: `workspace: the folder authority and workspace init (WU2, D2803, D2804, D2818)`.
- Reviewer tier: opus, mandatory independent review.

### WU3: the SA fallback's account
- Acceptance: the workspace root creates a workspace IAM account distinct from the credentials
  service's account, with no key, and outputs its email under the name C2 fixes; its token creators
  are not Terraform's (D2818 writes them live); the root's allowed grants stay pinned.
- Files: `deploy/terraform/gcp/roots/workspace/main.tf`, `deploy/terraform/gcp/roots/workspace/outputs.tf`,
  `deploy/terraform/gcp/roots/workspace/tests/workspace.tftest.hcl`,
  `deploy/terraform/terraform_test.go`, `internal/infra/tf/workspace.go`.
- Interfaces: consumes none; produces the output WU20 reads.
- Tests: the tftest case "the IAM account is not the service account and has no key";
  `allowedMemberExprs` in `terraform_test.go`; `internal/infra/tf`'s workspace tests.
- Command: `terraform -chdir=deploy/terraform/gcp/roots/workspace test` and
  `go test ./internal/infra/tf/ -run Workspace -count=1` and `go test -tags terraform ./deploy/terraform/ -count=1`.
- Red step: the tftest assertion fails until the account exists.
- Depends on: WU0 (the gate).
- Commit: `workspace: the SA fallback's account (WU3, D2803, F20)`.
- Reviewer tier: opus, mandatory independent review.

### WU20: the SA authority
- Acceptance: `Link` grants the workspace IAM account `roles/owner` on the member from the member
  owner's own credentials; `Unlink` removes it; `Holds` reports the grant; `AddAdmin`/`RemoveAdmin`
  change the account's token-creator bindings; the account is only ever impersonated.
- Files: `internal/workspace/authority_sa.go` (new), `internal/workspace/authority_sa_test.go` (new).
- Interfaces: consumes C2's output name; produces a concrete SA authority I1 adapts to `Authority`.
- Tests: `TestSALinkGrantsOwner`, `TestSAUnlinkRemovesGrant`, `TestSAAdminTokenCreatorAddedAndRemoved`,
  `TestSANeverReadsAKey`.
- Command: `go test ./internal/workspace/ -run TestSA -count=1`.
- Red step: the link test fails until the fake records the binding.
- Depends on: C2.
- Commit: `workspace: the SA authority (WU20, D2803, D2818)`.
- Reviewer tier: opus, mandatory independent review.

### WU4: member operations
- Acceptance: `ReadGrants`/`WriteGrants` read and write the member's `config/admin/access` with its
  ETag; `ReadMirror`/`WriteMirror` read and write `config/admin/workspace-members` (already denied to
  clients by the `config/admin` rules); `Bindings` reads the member's IAM policy and names the
  member's own Terraform accounts, the workspace IAM account and folder inheritance; `PendingApply`
  compares the roster's derived lists with the lists the member's installation outputs carry;
  `Audit` creates a document in the member's own Firestore. Nothing here runs Terraform.
- Files: `internal/cli/workspace_memberops.go` (new), `internal/cli/workspace_memberops_test.go` (new),
  `internal/cli/workspace_member_audit.go` (new).
- Interfaces: consumes C1's `MirrorEntry` and `AuditEvent`; produces a concrete member writer I1
  adapts to C2's `MemberOps` (except `Offboard`).
- Tests: `TestMemberGrantsWriteRefusesStaleETag`, `TestPendingApplyNamesUnappliedGrants`,
  `TestBindingsLabelsFugaroAccounts`, `TestMemberAuditCreateOnly`.
- Command: `go test ./internal/cli/ -run 'TestMemberGrants|TestPendingApply|TestBindingsLabels|TestMemberAudit' -count=1`.
- Red step: the pending test fails until the outputs' lists are read.
- Depends on: C1.
- Commit: `workspace: read and write a member's roster, mirror and audit (WU4, D2801, D2811, D2813)`.
- Reviewer tier: opus, mandatory independent review.

### WU5: `workspace project add`
- Acceptance: carries `workspace member add`'s flags and table entry unchanged (the record is still
  published by `fugaro update`); on a linked member it adopts the member's grantees into the
  workspace node (outside the domains only with `--external`), writes each workspace admin's stored
  grant (D2818) and the mirror; on an unlinked member it records the entry and says the member owner
  runs `fugaro workspace project link`; it refuses a project in another workspace (R2) and strips
  nothing (D2805).
- Files: `internal/cli/workspace_project.go` (new), `internal/cli/workspace_project_test.go` (new).
- Interfaces: consumes C1 `Store`, `AuditSink`; C2 `Authority.Holds`, `MemberOps`.
- Tests: `TestProjectAddAdoptsGrants`, `TestProjectAddWaitsForLink`, `TestProjectAddRefusesOtherWorkspace`,
  `TestProjectAddNeedsExternalFlag`, `TestProjectAddKeepsMemberFlags`.
- Command: `go test ./internal/cli/ -run TestProjectAdd -count=1`.
- Red step: the adopt test fails against its fake's empty roster.
- Depends on: C1, C2.
- Commit: `workspace: project add (WU5, D2802, D2805)`.
- Reviewer tier: opus, mandatory independent review.

### WU18: `workspace project link|unlink`
- Acceptance: run on the member owner's machine with the member's own config selected and its pin
  naming the workspace (`fugaro workspace add`), `link` calls `Authority.Link` with the owner's own
  credentials and writes the mirror and a member audit event; `unlink` reverses it; both refuse on
  a config that is not the member's or whose pin names another workspace; nothing else changes.
- Files: `internal/cli/workspace_project_link.go` (new), `internal/cli/workspace_project_link_test.go` (new).
- Interfaces: consumes C2 `Authority`, `MemberOps.WriteMirror`, `MemberOps.Audit`.
- Tests: `TestLinkNeedsTheMembersOwnConfig`, `TestLinkRefusesOtherWorkspacePin`, `TestUnlinkReverses`.
- Command: `go test ./internal/cli/ -run 'TestLink|TestUnlink' -count=1`.
- Red step: the own-config test fails until the pin is read.
- Depends on: C2.
- Commit: `workspace: project link, the member owner's consent (WU18, D2800, D2819)`.
- Reviewer tier: opus, mandatory independent review.

### WU6: `workspace project rm`
- Acceptance: saves the state by default through `StateWriter` (a local file it names when no state
  repo is set), `--no-save-state` skips it; removes the workspace admins' stored grants and the
  mirror from the member; on the SA mechanism removes the account's grant; on the folder mechanism
  prints `fugaro workspace project unlink` for the member owner (U6); prints the running-cost
  warning naming the rebuild Scheduler jobs and the credentials service, and R3's console link;
  deletes no GCP or Firebase resource.
- Files: `internal/cli/workspace_project_rm.go` (new), `internal/cli/workspace_project_rm_test.go` (new).
- Interfaces: consumes C1 `Store`; C2 `Authority`, `MemberOps`, `StateWriter`.
- Tests: `TestProjectRmSavesByDefault`, `TestProjectRmDeletesNothing`, `TestProjectRmPrintsShutdownLink`,
  `TestProjectRmRemovesWorkspaceAdminGrants`.
- Command: `go test ./internal/cli/ -run TestProjectRm -count=1`.
- Red step: the save test fails against its fake writer.
- Depends on: C1, C2.
- Commit: `workspace: project rm detaches and never deletes (WU6, D2808)`.
- Reviewer tier: opus, mandatory independent review.

### WU7: `workspace user add|rm|ls` and `workspace domains`
- Acceptance: only workspace admins add or remove members; an address outside the domains needs
  `--external` and is marked external; `--kind sa` records a machine; `--admin` makes a workspace
  admin, writing their stored grant on every linked member and their folder or token-creator
  binding, and `rm` removes every one of them in the same command; the last workspace admin cannot be
  removed; `user rm` calls `MemberOps.Offboard` on every member; `user ls` aggregates the members'
  grants, with a `--json` schema; `domains add|rm|ls` edit the list; every change writes an audit event.
- Files: `internal/cli/workspace_user.go` (new), `internal/cli/workspace_domains.go` (new),
  `internal/cli/workspace_user_test.go` (new), `schemas/workspace-users.schema.json` (new).
- Interfaces: consumes C1 `Store`, `AuditSink`; C2 `MemberOps`, `Authority`.
- Tests: `TestUserAddNeedsExternal`, `TestUserAddOnlyWorkspaceAdmins`, `TestUserRmOffboardsEveryMember`,
  `TestRmAdminRemovesEveryGrantAndBinding`, `TestLastWorkspaceAdminKept`, `TestDomainsRoundTrip`,
  `TestUserLsJSONSchema`.
- Command: `go test ./internal/cli/ -run 'TestUser(Add|Rm|Ls)|TestRmAdmin|TestLastWorkspaceAdmin|TestDomains' -count=1`.
- Red step: the admin-removal test fails until both the grant and the binding are removed.
- Depends on: C1, C2.
- Commit: `workspace: members, machines, admins and allowed domains (WU7, D2806, D2812, D2818)`.
- Reviewer tier: opus, mandatory independent review.

### WU8: `workspace user grant|revoke`
- Acceptance: `grant <email> <project> <role> [--scope …]` refuses an address the member's mirror
  does not list (advisory, D2813), applies roles-scopes' subset and last-admin refusals
  (`access.SubsetOf`), writes the member's roster, audits to the member's Firestore for a project
  admin and to the workspace's for a workspace admin (D2811), and prints that the member's own
  `fugaro update` applies it (D2801); a project admin needs no grant on the workspace project.
- Files: `internal/cli/workspace_user_grant.go` (new), `internal/cli/workspace_user_grant_test.go` (new).
- Interfaces: consumes C1 `AuditSink`; C2 `MemberOps`.
- Tests: `TestGrantRefusesNonMember`, `TestGrantAsProjectAdminWritesOnlyTheMember`,
  `TestGrantCarriesScope`, `TestRevokeKeepsLastAdmin`, `TestGrantNamesTheUpdateThatAppliesIt`.
- Command: `go test ./internal/cli/ -run 'TestGrant|TestRevoke' -count=1`.
- Red step: the non-member test fails against its fake's empty mirror.
- Depends on: C1, C2.
- Commit: `workspace: grant and revoke project roles (WU8, D2801, D2815)`.
- Reviewer tier: opus, mandatory independent review.

### WU9: offboarding a member project
- Acceptance: `fugaro user offboard`'s core is callable for a named member project and provides
  `Offboard` (roster grants dropped, the person's own secrets swept, an audit event written),
  callable by a workspace admin through their stored grant (D2818, D1940); a run whose only
  credential was swept ends with the gateway's existing refusal (U4).
- Files: `internal/cli/offboard.go`, `internal/cli/workspace_offboard.go` (new),
  `internal/cli/workspace_offboard_test.go` (new).
- Interfaces: consumes C2 `MemberOps.Audit`; produces a concrete offboarder I1 adapts to `MemberOps.Offboard`.
- Tests: `TestOffboardMemberSweepsPrefix`, `TestOffboardMemberAudits`, `TestSweptOnlyCredentialRefusesClearly`.
- Command: `go test ./internal/cli/ -run 'TestOffboardMember|TestSweptOnlyCredential' -count=1`.
- Red step: the sweep test fails until the core takes a named project.
- Depends on: C2.
- Commit: `workspace: offboarding reaches every member (WU9, D2813, D2818)`.
- Reviewer tier: opus, mandatory independent review.

### WU11: `workspace project ls` and `status`
- Acceptance: `status [<project>]` prints pass/warn/fail with a fix per row for: reachable; linked
  (`Authority.Holds`; "not linked" names `link`, D2819); credentials service responds; credentials
  stored and not marked broken (F14); images fresh (the rebuild Scheduler jobs' last success); caps
  set and use against them; grants waiting for the member's `fugaro update` (`PendingApply`); state
  repo in sync (`LastSaved`); drift (`Drift`); a single workspace admin (R1); `ls` prints one line per
  member with its worst row; both write nothing on any path.
- Files: `internal/cli/workspace_project_status.go` (new), `internal/cli/workspace_project_status_test.go`
  (new), `schemas/workspace-projects.schema.json` (new).
- Interfaces: consumes C1 `Store`; C2 `Authority`, `MemberOps`, `StateWriter`, `Drift`; calls
  `claudeAccountsStoredCheck` and doctor's brokering reads.
- Tests: `TestStatusRowsInOrder`, `TestStatusWritesNothing`, `TestStatusNotLinked`,
  `TestStatusSingleAdminWarns`, `TestProjectLsJSONSchema`.
- Command: `go test ./internal/cli/ -run 'TestStatus|TestProjectLs' -count=1`.
- Red step: the write-nothing test fails until every read goes through the read-only fake.
- Depends on: C1, C2.
- Commit: `workspace: project ls and status (WU11)`.
- Reviewer tier: standard.

### WU12: drift and the Cloud Audit Logs cross-check
- Acceptance: `Drift` reports extra and missing grants between a member's IAM policy, its roster and
  **the workspace node** (never the mirror, D2813), ignores the member's own Terraform accounts, the
  workspace IAM account and folder inheritance, flags outside-domain principals, and names each
  `SetIamPolicy` in Cloud Audit Logs that neither audit trail holds.
- Files: `internal/workspace/drift.go` (new), `internal/workspace/drift_test.go` (new),
  `internal/infra/auditlogs.go` (new), `internal/gcpfake/logging.go`.
- Interfaces: consumes C1's `Person`, `AuditEvent`; produces `Drift` I1 adapts to C2's signature.
- Tests: `TestDriftIgnoresFugaroAccounts`, `TestDriftIgnoresARewrittenMirror`, `TestDriftExtraAndMissing`,
  `TestDriftNamesConsoleChange`.
- Command: `go test ./internal/workspace/ ./internal/infra/ -run 'TestDrift|TestAuditLogs' -count=1`.
- Red step: the rewritten-mirror test fails until drift reads the workspace node.
- Depends on: C1.
- Commit: `workspace: drift against the workspace node and the audit logs (WU12, D2806, D2811, D2813)`.
- Reviewer tier: opus, mandatory independent review.

### WU13: `workspace project reconcile`
- Acceptance: adds missing grants and removes extras on the member's roster from `Drift`, audits each
  change, and prints that the member's own `fugaro update` applies them; `--dry-run` writes nothing.
- Files: `internal/cli/workspace_project_reconcile.go` (new), `internal/cli/workspace_project_reconcile_test.go` (new).
- Interfaces: consumes C1 `AuditSink`; C2 `Drift`, `MemberOps`.
- Tests: `TestReconcileDryRunWritesNothing`, `TestReconcileRemovesExtra`.
- Command: `go test ./internal/cli/ -run TestReconcile -count=1`.
- Red step: the remove test fails against its fake's empty report.
- Depends on: C1, C2.
- Commit: `workspace: reconcile a member's roster (WU13, D2809)`.
- Reviewer tier: opus, mandatory independent review.

### WU15: the state writer
- Acceptance: `Save` writes the project's settings and spend summary, and the workspace's audit
  export, as a pull request to the E6 designated repository and folder through R9's writer, with no
  designated repository a local file it names; `LastSaved` reads the last snapshot's time.
- Files: `internal/cli/workspace_state.go` (new), `internal/cli/workspace_state_test.go` (new).
- Interfaces: consumes C1's `AuditEvent` and R9's writer; produces a concrete writer I1 adapts to `StateWriter`.
- Tests: `TestStateSaveOpensPR`, `TestStateSaveLocalWithoutDesignatedRepo`, `TestLastSaved`.
- Command: `go test ./internal/cli/ -run 'TestStateSave|TestLastSaved' -count=1`.
- Red step: the PR test fails until R9's writer is called.
- Depends on: C1; **periodic-reviews R9 (#3140) merged** (behaviour, outside this plan and not a graph node).
- Commit: `workspace: state snapshots to the designated repository (WU15, D2807)`.
- Reviewer tier: standard.

### I1: wire the workspace commands
- Acceptance: the stubs are deleted; C1's and C2's interfaces are adapted onto WU1, WU2, WU20, WU4,
  WU9, WU12 and WU15; `newWorkspaceCmd` registers `init`, `project add|ls|rm|status|reconcile|link|unlink`,
  `user add|rm|ls|grant|revoke` and `domains add|rm|ls`, and removes `workspace member` in the same
  change; the command census (`report_exempt.go`), the no-prompt harness, the CLI reference and
  `update_workspace.go`'s refusal text name the new commands.
- Files: `internal/workspace/roster_stub.go` (deleted), `internal/workspace/memberops_stub.go`
  (deleted), `internal/cli/workspace_wiring.go` (new), `internal/cli/workspace.go`,
  `internal/cli/workspace_test.go`, `internal/cli/report_exempt.go`,
  `internal/cli/noprompt_harness_test.go`, `internal/cli/update_workspace.go`,
  `website/content/cli-reference.md` (regenerated).
- Interfaces: joins every part.
- Tests: `TestWorkspaceCommandsRegistered`, the report census, the no-prompt harness,
  `ee/clidoc`'s CLI-reference comparison.
- Command: `go test ./internal/cli/ -run 'TestWorkspaceCommandsRegistered|Census|NoPrompt' -count=1` and
  `go test ./ee/clidoc/ -count=1`.
- Red step: the registration test fails while either stub is wired.
- Depends on: WU1, WU2, WU20, WU3, WU4, WU5, WU18, WU6, WU7, WU8, WU9, WU11, WU12, WU13, WU15.
- Commit: `workspace: wire projects and users (I1)`.
- Reviewer tier: opus, mandatory independent review. Nine files: two deletions, one regeneration and
  four census lines; the end-to-end test is WU21.

### WU21: the end-to-end test
- Acceptance: `TestWorkspaceProjectsUsersEndToEnd` passes against `internal/gcpfake`: init (folder) →
  project add (waits) → link (member owner) → project add (adopts) → user add (refused, then
  `--external`) → grant (pending until the member's update) → status (pending, then clean) → a binding
  added straight in the fake → status names the drift and the audit-log entry → reconcile → user rm
  (prefix swept) → `--admin` removal (grants and binding gone) → project rm (state saved, nothing deleted).
- Files: `internal/cli/workspace_e2e_test.go` (new).
- Interfaces: none.
- Tests and command: `go test ./internal/cli/ -run TestWorkspaceProjectsUsersEndToEnd -count=1`.
- Red step: it fails on the first command any part leaves unwired.
- Depends on: I1 (behaviour: the real commands).
- Commit: `workspace: projects and users end to end (WU21)`.
- Reviewer tier: standard.

### WU10: installation-level refusals on a member
- Acceptance: `fugaro user add|rm|grant|revoke|offboard` refuse on a member project — known by the
  config's workspace key or by the member's mirror node — naming the `fugaro workspace user …`
  command; unchanged on any other project.
- Files: `internal/cli/user.go`, `internal/cli/user_grant.go`, `internal/cli/offboard.go`,
  `internal/cli/user_test.go`.
- Interfaces: reads `localcfg.Config.Workspace` and C2's `ReadMirror` through I1's wiring.
- Tests: `TestUserCommandsRefuseOnMember`, `TestUserCommandsRefuseOnMirrorWithoutKey`,
  `TestUserCommandsUnchangedOutsideWorkspace`.
- Command: `go test ./internal/cli/ -run 'TestUserCommands' -count=1`.
- Red step: the refusal test fails today.
- Depends on: I1 (behaviour: the commands the refusal names must exist).
- Commit: `cli: installation-level user commands defer to the workspace (WU10, D2813)`.
- Reviewer tier: opus.

### WU16: docs and skills
- Acceptance: the plugin skills and docs name the new commands, the opt-in link and the advisory
  mirror; the docs say the Firestore trails are not tamper-proof (D2811); `docs/workspaces/README.md`
  links here; no release file is written (the summary is due in the release's own PR).
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
  project, and checks the folder move and its return (U1, U6); **Check 721** grants a role in the
  console, runs `status` and checks the drift row names the audit-log entry (U5).
- Files: `scripts/live-checks/check-720.sh` (new), `scripts/live-checks/check-720_test.sh` (new),
  `scripts/live-checks/check-721.sh` (new), `scripts/live-checks/check-721_test.sh` (new),
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`.
- Tests: `TestLiveCheckScripts`, and each script's own `_test.sh`.
- Command: `go test ./scripts/ -run TestLiveCheckScripts -count=1`.
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
| 1 | C1, C2, WU1, WU3 |
| 2 | WU2, WU20, WU4, WU5, WU18, WU6, WU7, WU8, WU9, WU11, WU12, WU13, WU15 (WU15 also waits on #3140); the anticipated contract changes R-member-firebase and R-roster-sa, if raised |
| 3 | I1 |
| 4 | WU10, WU16, WU17, WU21 |

**Critical path:** see §5a. Sequential by necessity: WU0 first (D2800's gate: its review may
reshape both contracts), I1 after every side it joins, and the refusals (WU10), the docs, the live
checks and the end-to-end test after the commands exist. WU1 and WU3 are pure providers: they
consume no contract and expose concrete implementations I1 adapts, so they run beside the contracts.

**Shared files** (no level holds two writers of one file):

| File | Writers, by level |
| --- | --- |
| `internal/cli/workspace.go`, `workspace_test.go` | I1 (3) |
| `internal/cli/offboard.go` | WU9 (2), WU10 (4) |
| `internal/cli/user.go`, `user_grant.go`, `user_test.go` | WU10 (4) |
| `internal/budget/rules/workspace.json.tmpl`, its golden file | WU1 (1) |
| `internal/gcpfake/crm.go` | WU2 (2) |
| `internal/gcpfake/logging.go` | WU12 (2) |
| `deploy/terraform/gcp/roots/workspace/*`, `deploy/terraform/terraform_test.go`, `internal/infra/tf/workspace.go` | WU3 (1) |
| `internal/workspace/roster_stub.go`, `memberops_stub.go` | C1, C2 (1); I1 deletes them (3) |
| `internal/workspace/roster.go`, `rosterstore.go`, `memberops.go`, the two contract documents, the two contract schemas | C1, C2 (1) and their changes (2) only, never the same file in one level |
| `docs/workspaces/README.md` | WU0 (0), WU16 (4) |
| `SECURITY.md`, `website/content/security.md`, `docs/design/v1.md` | WU0 (0) |
| `website/content/cli-reference.md`, `report_exempt.go`, `noprompt_harness_test.go`, `update_workspace.go` | I1 (3) |
| `schemas/workspace-projects.schema.json`, `workspace-users.schema.json` | WU11, WU7 (2), one each |

## 5. The dependencies broken and kept

Broken, each by a contract and re-joined by I1 (each side needed only the shape):

- **By C1 (the roster):** WU5, WU6, WU7, WU11 → WU1 (they read and write through `Store`); WU8, WU13 →
  WU1 (they write an `AuditEvent`); WU2 → WU1 (the folder link records the original parent on the
  roster entry); WU4 → WU1 (the mirror is `MirrorEntry`); WU12 → WU1 (drift reads people and kinds);
  WU15 → WU1 (the audit export reads `AuditEvent`).
- **By C2 (member operations):** WU5, WU6, WU7, WU8, WU11, WU13, WU18 → WU4 (they call `MemberOps`);
  WU9 → WU4 (it writes through `MemberOps.Audit`); WU5, WU6, WU7, WU11, WU18 → WU2 (they call
  `Authority`); WU20 → WU3 (the IAM account's output by the name C2 fixes); WU7 → WU9 (`user rm`
  calls `Offboard`); WU11, WU13 → WU12 (they consume `DriftReport`); WU6, WU11 → WU15 (they call
  `StateWriter`).

Kept:

- WU0 → C1, C2, WU1, WU3: D2800's mandatory review gates every task, and its findings may change the
  shapes — a contract written before it would be rewritten.
- C1/C2 → each consumer: the contract is what lets it run beside its provider.
- The parts → I1: I1 joins them. I1 → WU10, WU16, WU17, WU21: the refusals name, and the skills'
  tests, the live checks and the end-to-end test drive, the real commands (behaviour).
- C1 → R-roster-sa and C2 → R-member-firebase: a change amends a file its contract creates.
- #3140 (periodic-reviews R9) → WU15: the writer must exist (behaviour, outside this plan).

### 5a. What the graph read reports

`fugaro plan graph --plan docs/workspace-projects-users/implementation-plan.md`, 2026-11-18, after
review round 1: no refusal, five levels matching §4; **29 edges broken, 2 kept** in the read's own
count, **5 tasks added** (C1, C2, I1 and the two anticipated contract changes); the critical path
goes from **6 tasks, weight 12.5** before the pass to **5 tasks, weight 8.5** after it — WU0 → C1 →
WU11 → I1 → WU10 — "improves". The weights are the plan's own `size` guesses (a contract 0.5, the
largest tasks 3); no run history exists yet for any of them.

## 6. Contract changes anticipated

- **R-member-firebase** on C2, raised by WU4: if a member's Firebase project cannot be read from its
  outputs, every `MemberOps` method takes it as an argument. Reaches WU5, WU8, WU11, WU18.
- **R-roster-sa** on C1, raised by WU7: if a machine member needs an owner or an expiry on `Person`.
  Reaches WU5, WU12.

## 7. Checks this plan takes

From the block **Checks 720–729**: **Check 720** (the folder link, move and its return, U1, U6) and
**Check 721** (console drift named from Cloud Audit Logs, U5), both owner-run (WU17). 722–729 reserved.

## 8. After-deploy steps

None: `fugaro update` is unchanged by this plan (R7, D2819), so the release note's `### For
operators` is "run `fugaro update`". People management is turned on per member by its owner's
`fugaro workspace project link`, a feature's first use rather than an upgrade step.

## 9. Considered and declined

- Writing member-project bindings from the workspace, or applying a member's Terraform from another
  machine (D2801 and its round-1 amendment, F7).
- An organization policy for domains (D2812's alternative).
- A read or write grant on the workspace database for project admins (D2813, D2817 amended).
- A binding gate on project admins' grants: the mirror is advisory and drift is the fence (D2813).
- Making the workspace sign-on's admission read the roster (D2810: not the owner's answer).
- A validity probe for model keys and Claude tokens (F14, open item O4: new behaviour, no design).
- An `update` stage that seeds the roster (first-draft WU14, R7).
- Linking existing members on their next `update` (D2819's alternative).
- Linking fugaro-prod app sign-ins (D2814): no task.

## 10. Cost

23 runs (WU0, C1, C2, WU1–WU13 without WU14, WU15, WU18, WU20, WU21, I1, WU16, WU17), none launched;
the two anticipated contract changes are two more only if raised. The gateway enforces each run's cap
(`fugaro budget set --repo`); the worst case is 23 times that cap, plus one independent review run
for each of the 17 tasks marked "mandatory independent review" (WU0, C1, C2, WU1, WU2, WU3, WU20,
WU4, WU5, WU18, WU6, WU7, WU8, WU9, WU12, WU13, I1).
