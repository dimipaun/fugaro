# Workspace projects and users: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). **Nothing is
implemented and no task is approved**: each task needs the owner's explicit go by name. The plan is
written to the answers of 2026-11-18 (D2800–D2817, every one as recommended, D2813 and D2817
amended the same day), and the graph beside it is
[implementation-plan.graph.json](implementation-plan.graph.json).

**The repository's settings.** `rigor.default` is `startup` with no area rules, and
`planning.decoupling` is on (read with `fugaro settings planning.decoupling rigor.default rigor.areas
--repo dimipaun/fugaro --json` on 2026-11-18), so the contract-first pass decouples aggressively.
Because D2800 puts IAM authority over every member in this design, **every task touching IAM, the
roster or a credential is reviewed at opus with a mandatory independent review**, whatever the mode.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/terraform.md` (WU3), `.claude/rules/credentials-service.md`
(WU0, WU9), `.claude/rules/ee.md` (nothing here imports `ee/`; the sign-on's verifier stays in
`ee/coordinator/oauth`, D2810).

- **Always.** Red test first; targeted tests only (`go test ./internal/cli/ -run 'TestX' -count=1`);
  `gofmt -l .` prints nothing; hermetic tests against `internal/gcpfake` and `t.TempDir()`; JSON
  output gets a schema in `schemas/` and decodes strictly; every write command refuses in a coding
  agent's session (A7) and asks nothing (no-prompt policy: a refusal names the flag, e.g.
  `--external`, `--no-save-state`); comments say why.
- **Ask first.** A new dependency; any change to a contract's files (raise a contract change
  `R-…`, never work around it); any change to `fugaro update`'s stage order.
- **Never.** A secret-shaped literal anywhere; a live cloud call from a test or an agent; a write
  from `workspace project status` (D2809); a workspace service account that is also the
  credentials service's account (F20); an SA key (D2803); an edit under `.github/workflows/`.
- **The HARD RULE.** An installation at the previous version converges with `fugaro update` alone:
  the `workspace member` rename (WU5) and the roster node (WU14) ship with that convergence and its
  test; the release note's `### For operators` is "run `fugaro update`".

## 2. Global constraints and review focus

| Constraint the spec implies | Pinned by |
| --- | --- |
| The authority is given by the member's owner at join, never taken (D2800) | WU2, WU3 tests: link refuses without the member owner's own credentials |
| No path holds a write on the workspace database for a project admin (F11, D2817 amended) | WU1 rules test; WU8 test grants as a project admin with no workspace grant |
| The workspace credentials service's account still holds no write on a member (D1299) | WU3's Terraform test |
| A workspace admin's per-member admin is never written into member rosters (R4) | WU7 test |
| Drift never reports Fugaro's own accounts (D2806) | WU12 test with the member's Terraform outputs |
| A project already in another workspace or folder is refused (R2) | WU5 test |
| Nothing is stripped at join (D2805) | WU5 test |
| The last workspace admin and the last project admin cannot be removed (roles-scopes refusals) | WU7, WU8 tests |
| `status` writes nothing on any path, `--json` included | WU11, WU12 tests against a fake that fails on any write |

## 3. Slices and tasks

Four slices. **S0** (WU0) is the gate. **S1** (C1, C2) is inert shapes. **S2** (WU1–WU15) builds
each part against the shapes, side by side. **S3** (I1, WU16, WU17) wires, documents and hands the
owner the live checks. Both authorities are built in WU2; D2803's "folder first" is the order the
owner checks them in (Check 720 exercises the folder before any SA link is tried live).

### WU0: the threat model, rewritten for workspace authority
- Acceptance: `threat-model.md` states what a stolen workspace-admin credential, a compromised
  workspace IAM account and a hostile project admin each reach, at their real size, and says that
  `docs/workspaces/spec.md` §9's first rule is superseded for people and roles by D2800;
  `SECURITY.md`'s workspaces section says the same; an independent security review
  (`fugaro review --pr N`, run by the coordinator) has returned and the owner has answered every
  finding before any other task launches.
- Files: `docs/workspace-projects-users/threat-model.md` (new), `SECURITY.md`,
  `docs/workspaces/README.md`.
- Interfaces: consumes reconciliation §3–§4; produces the threat model C1 and C2 are reviewed against.
- Tests: `TestDocs…` that pin `SECURITY.md`'s workspaces section.
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: a docs test pinning the sentence "a workspace never widens a member" fails until the
  superseding sentence replaces it in both places.
- Depends on: none.
- Commit: `docs(workspace-projects-users): threat model for workspace IAM authority (WU0, D2800)`.
- Reviewer tier: opus, mandatory independent review.

### C1: the roster contract
- Acceptance: the roster's Go types, its JSON Schema, the audit event's schema, the store, audit and
  mirror interfaces and a stub of each exist, and a contract document says what each side may rely
  on; the stub compiles, takes the arguments, returns the shape and does nothing else.
- Files: `internal/workspace/roster.go` (new: `Roster`, `Member` with `Kind` human|sa and
  `External`, `Domains`, `Admins`, `AuditEvent`, `MirrorEntry`), `internal/workspace/rosterstore.go`
  (new: `Store` with ETag `Read`/`Write`, `AuditSink`), `internal/workspace/roster_stub.go` (new),
  `schemas/workspace-roster.schema.json` (new), `schemas/workspace-audit-event.schema.json` (new),
  `docs/workspace-projects-users/contract.md` (new).
- Interfaces: produces the roster shape every S2 task reads or writes.
- Tests: `TestRosterSchemaRoundTrip`, `TestRosterDecodeRefusesUnknownField`, `schemas` package tests.
- Command: `go test ./internal/workspace/ ./schemas/ -run 'TestRoster|TestSchema' -count=1`.
- Red step: the round-trip test fails until the types and schema agree.
- Depends on: WU0 (its review may reshape the roster).
- Commit: `workspace: the roster contract (C1, D2817)`.
- Reviewer tier: opus, mandatory independent review (a wire format over IAM).

### C2: the member-operations contract
- Acceptance: interfaces `MemberOps` (`WriteGrants`, `WriteMirror`, `Converge`, `Bindings`,
  `Offboard`), `Authority` (`Kind`, `Link`, `Unlink`) with constructors `NewFolderAuthority` and
  `NewSAAuthority`, `StateWriter` (`Save`), and `Drift(...) DriftReport` exist with stubs, and a
  contract document that also fixes the name of the workspace root's IAM-account output (WU3 writes
  it, WU2 reads it); every method names the member's GCP project and its Firebase project
  separately (F2).
- Files: `internal/workspace/memberops.go` (new), `internal/workspace/memberops_stub.go` (new),
  `docs/workspace-projects-users/contract-member.md` (new).
- Interfaces: produces what WU2–WU15 implement or call.
- Tests: `TestMemberOpsStubShapes`.
- Command: `go test ./internal/workspace/ -run TestMemberOps -count=1`.
- Red step: the shape test fails until each stub returns its declared zero shape.
- Depends on: WU0.
- Commit: `workspace: the member-operations contract (C2, D2801, D2803)`.
- Reviewer tier: opus, mandatory independent review.

### WU1: the roster store and the audit sink
- Acceptance: the roster lives at `workspaces/<name>/roster` in the workspace's database, written
  only with an ETag-conditional write (a concurrent change is re-read, never overwritten); the rules
  deny every client; the audit sink writes create-only Firestore documents and never updates one.
- Files: `internal/workspace/roster_rtdb.go` (new), `internal/workspace/audit_firestore.go` (new),
  `internal/budget/rules/workspace.json.tmpl`, `internal/budget/rules/workspace_test.go`,
  `internal/workspace/roster_rtdb_test.go` (new).
- Interfaces: consumes none; produces `RosterNode` (the node's raw JSON and its ETag, read and
  conditional write) and `AuditWriter` (create-only documents), in basic types; I1 adapts C1's
  `Store` and `AuditSink` onto them, so WU1 runs beside C1.
- Tests: `TestRosterWriteRefusesStaleETag`, `TestAuditSinkCreateOnly`, the rules harness case for
  `workspaces/<name>/roster`.
- Command: `go test ./internal/workspace/ ./internal/budget/rules/ -run 'TestRoster|TestAudit|TestWorkspaceRules' -count=1`.
- Red step: the stale-ETag test fails until the write carries `if-match`.
- Depends on: WU0 (the gate).
- Commit: `workspace: the roster node and the audit sink (WU1, D2811, D2817)`.
- Reviewer tier: opus.

### WU2: both authorities and `workspace init`
- Acceptance: `workspace init` detects an organization the caller can create folders in and
  creates the workspace's folder with the workspace admins as `roles/owner` on it, else selects the
  SA mechanism. Folder: `Link` moves a project into the folder only with the member owner's own
  credentials, records its original parent on the member's roster entry, refuses a project under
  another workspace's folder (R2); `Unlink` moves it back. SA: `Link` grants the workspace IAM
  account (WU3's output) `roles/owner` on the member with the member owner's own credentials;
  `Unlink` removes that grant.
- Files: `internal/infra/folder.go` (new), `internal/gcpfake/crm.go`,
  `internal/workspace/authority_folder.go` (new), `internal/workspace/authority_sa.go` (new),
  `internal/cli/workspace_init.go` (new; constructor only, registered by I1).
- Interfaces: implements C2's `Authority` both ways; consumes C1's roster entry and the IAM
  account's output name C2 fixes.
- Tests: `TestWorkspaceInitPicksFolderWithOrg`, `TestWorkspaceInitPicksSAWithoutOrg`,
  `TestFolderLinkRefusesOtherWorkspaceFolder`, `TestFolderUnlinkRestoresParent`,
  `TestSALinkGrantsOwnerWithMemberCredentials`.
- Command: `go test ./internal/infra/ ./internal/workspace/ ./internal/cli/ -run 'TestWorkspaceInit|TestFolder|TestSALink' -count=1`.
- Red step: the init test fails against the stub authority.
- Depends on: C1, C2.
- Commit: `workspace: the folder and SA authorities, and workspace init (WU2, D2803, D2804)`.
- Reviewer tier: opus, mandatory independent review.

### WU3: the SA fallback's account
- Acceptance: the workspace root creates a workspace IAM account distinct from the credentials
  service's account, with no key, whose only token creators are the workspace admins, and outputs
  its email under the name C2's contract document fixes.
- Files: `deploy/terraform/gcp/roots/workspace/main.tf`, `…/variables.tf`, `…/outputs.tf`,
  `…/tests/workspace.tftest.hcl`.
- Interfaces: consumes none; produces the output WU2's SA authority reads.
- Tests: the tftest case "the IAM account is not the service account and has no key".
- Command: `go test -tags terraform ./deploy/terraform/ -run Workspace -count=1`.
- Red step: the tftest assertion fails until the account exists.
- Depends on: WU0 (the gate).
- Commit: `workspace: the SA fallback for members outside an organization (WU3, D2803, F20)`.
- Reviewer tier: opus, mandatory independent review.

### WU4: member operations
- Acceptance: `WriteGrants` writes the member's `config/admin/access` with its ETag; `WriteMirror`
  writes `config/admin/workspace-members` (denied to clients); `Converge` runs the member's IAM
  converge from a config rebuilt from the member's installation outputs (U2); `Bindings` reads the
  member's IAM policy and its Terraform outputs' own accounts.
- Files: `internal/cli/workspace_memberops.go` (new), `internal/cli/workspace_memberops_test.go`
  (new), `internal/cli/init_new_machine.go`, `internal/budget/rules/rules.json.tmpl`.
- Interfaces: consumes C1's `MirrorEntry`; produces a concrete member writer whose methods I1
  adapts to C2's `MemberOps` (except `Offboard`).
- Tests: `TestMemberOpsConvergeFromOutputs`, `TestMemberMirrorDeniedToClients`.
- Command: `go test ./internal/cli/ ./internal/budget/rules/ -run 'TestMemberOps|TestMemberMirror' -count=1`.
- Red step: the converge test fails until every IAM input is recovered from the outputs.
- Depends on: C1.
- Commit: `workspace: write, mirror and converge a member (WU4, D2801, D2813)`.
- Reviewer tier: opus, mandatory independent review.

### WU5: `workspace project add` and the rename
- Acceptance: `fugaro workspace member add|rm` becomes `workspace project add|rm`'s owner half and
  `workspace member` is gone; `project add` links the authority, adopts the member's roster grants
  (members imported, outside the domains only with `--external`), writes the mirror, refuses a
  project in another workspace, and strips nothing.
- Files: `internal/cli/workspace.go`, `internal/cli/workspace_project.go` (new),
  `internal/cli/workspace_project_test.go` (new), `internal/cli/workspace_test.go`.
- Interfaces: consumes C1 `Store`, C2 `Authority` and `MemberOps`.
- Tests: `TestProjectAddAdoptsGrants`, `TestProjectAddRefusesOtherWorkspace`,
  `TestProjectAddNeedsExternalFlag`, `TestWorkspaceMemberIsGone`.
- Command: `go test ./internal/cli/ -run 'TestProjectAdd|TestWorkspaceMember' -count=1`.
- Red step: the adopt test fails against the stubs' empty roster.
- Depends on: C1, C2.
- Commit: `workspace: project add, absorbing workspace member (WU5, D2802, D2805)`.
- Reviewer tier: opus.

### WU6: `workspace project rm`
- Acceptance: saves the state by default through `StateWriter` (a local file it names when no state
  repo is set), `--no-save-state` skips it; unlinks the authority; removes the mirror; prints the
  running-cost warning naming the Scheduler rebuilds and the credentials service, and the console
  link of R3; deletes no GCP or Firebase resource.
- Files: `internal/cli/workspace_project_rm.go` (new), `internal/cli/workspace_project_rm_test.go` (new).
- Interfaces: consumes C2 `Authority`, `MemberOps`, `StateWriter`.
- Tests: `TestProjectRmSavesByDefault`, `TestProjectRmDeletesNothing`, `TestProjectRmPrintsShutdownLink`.
- Command: `go test ./internal/cli/ -run TestProjectRm -count=1`.
- Red step: the save test fails against the stub writer.
- Depends on: C2.
- Commit: `workspace: project rm detaches and never deletes (WU6, D2808)`.
- Reviewer tier: opus.

### WU7: `workspace user add|rm|ls` and `workspace domains`
- Acceptance: only workspace admins add or remove members; an address outside the domains needs
  `--external` and is marked external; `--admin` makes a workspace admin, and the last one cannot be
  removed; `--kind sa` records a machine; `user rm` calls `MemberOps.Offboard` on every member;
  `user ls` aggregates the members' grants; `domains add|rm|ls` edit the roster's list; every change
  writes an audit event.
- Files: `internal/cli/workspace_user.go` (new), `internal/cli/workspace_domains.go` (new),
  `internal/cli/workspace_user_test.go` (new).
- Interfaces: consumes C1 `Store`, `AuditSink`; C2 `MemberOps`.
- Tests: `TestUserAddNeedsExternal`, `TestUserAddOnlyWorkspaceAdmins`, `TestUserRmOffboardsEveryMember`,
  `TestLastWorkspaceAdminKept`, `TestDomainsRoundTrip`.
- Command: `go test ./internal/cli/ -run 'TestUser(Add|Rm)|TestLastWorkspaceAdmin|TestDomains' -count=1`.
- Red step: the offboard fan-out test fails against the stub.
- Depends on: C1, C2.
- Commit: `workspace: members, machines and allowed domains (WU7, D2806, D2812)`.
- Reviewer tier: opus, mandatory independent review.

### WU8: `workspace user grant|revoke`
- Acceptance: `grant <email> <project> <role> [--scope …]` refuses an address the member's mirror
  does not list, applies roles-scopes' subset and last-admin refusals (`access.SubsetOf`), writes the
  member's roster and converges it, and works for a project admin holding no grant on the workspace
  project.
- Files: `internal/cli/workspace_user_grant.go` (new), `internal/cli/workspace_user_grant_test.go` (new).
- Interfaces: consumes C2 `MemberOps`, C1 `AuditSink`.
- Tests: `TestGrantRefusesNonMember`, `TestGrantAsProjectAdminNeedsNoWorkspaceWrite`,
  `TestGrantCarriesScope`, `TestRevokeKeepsLastAdmin`.
- Command: `go test ./internal/cli/ -run 'TestGrant|TestRevoke' -count=1`.
- Red step: the non-member test fails against the stub's empty mirror.
- Depends on: C1, C2.
- Commit: `workspace: grant and revoke project roles (WU8, D2801, D2815)`.
- Reviewer tier: opus, mandatory independent review.

### WU9: offboarding a member project
- Acceptance: `fugaro user offboard`'s core is callable for a named member and implements
  `MemberOps.Offboard` (roster grants dropped, the person's own secrets swept); installation-level
  `fugaro user offboard` refuses on a member project and names `fugaro workspace user rm`; a run
  whose only credential was swept ends with the gateway's existing refusal (U4).
- Files: `internal/cli/offboard.go`, `internal/cli/workspace_offboard.go` (new),
  `internal/cli/workspace_offboard_test.go` (new).
- Interfaces: implements C2 `MemberOps.Offboard`.
- Tests: `TestOffboardMemberSweepsPrefix`, `TestUserOffboardRefusesOnMember`.
- Command: `go test ./internal/cli/ -run 'TestOffboard|TestUserOffboard' -count=1`.
- Red step: the refusal test fails until the membership key is read.
- Depends on: C2.
- Commit: `workspace: offboarding reaches every member (WU9, D2813)`.
- Reviewer tier: opus, mandatory independent review.

### WU10: installation-level refusals on a member
- Acceptance: `fugaro user add|rm|grant|revoke` refuse on a project whose config names a workspace,
  with the `fugaro workspace user …` command in the refusal; unchanged on any other project.
- Files: `internal/cli/user.go`, `internal/cli/user_grant.go`, `internal/cli/user_test.go`.
- Interfaces: reads `localcfg.Config.Workspace`.
- Tests: `TestUserCommandsRefuseOnMember`, `TestUserCommandsUnchangedOutsideWorkspace`.
- Command: `go test ./internal/cli/ -run 'TestUserCommands' -count=1`.
- Red step: the refusal test fails today.
- Depends on: WU0 (the gate).
- Commit: `cli: installation-level user commands defer to the workspace (WU10, D2813)`.
- Reviewer tier: opus.

### WU11: `workspace project ls` and `status`
- Acceptance: `status [<project>]` prints pass/warn/fail with a fix per row for: reachable,
  credentials service responds, credentials stored and not marked broken (`--check-credentials`
  in the owner's own terminal reuses doctor's checks, F14), images fresh (the rebuild Scheduler
  jobs' last success), caps set and use against them, state repo in sync, single workspace admin
  (R1); `ls` prints one line per member with its worst row; both write nothing on any path.
- Files: `internal/cli/workspace_project_status.go` (new), `internal/cli/workspace_project_status_test.go`
  (new), `schemas/workspace-projects.schema.json` (new).
- Interfaces: consumes C1 `Store`, C2 `StateWriter`; calls `claudeAccountsStoredCheck` and doctor's
  brokering reads.
- Tests: `TestStatusRowsInOrder`, `TestStatusWritesNothing`, `TestStatusSingleAdminWarns`, `TestProjectLsJSONSchema`.
- Command: `go test ./internal/cli/ -run 'TestStatus|TestProjectLs' -count=1`.
- Red step: the write-nothing test fails until every read goes through the read-only fake.
- Depends on: C1, C2.
- Commit: `workspace: project ls and status (WU11)`.
- Reviewer tier: standard.

### WU12: drift and the Cloud Audit Logs cross-check
- Acceptance: `Drift` reports extra and missing grants between a member's IAM policy, its roster
  and the mirror, ignores the member's own Terraform accounts, flags outside-domain principals,
  and names each `SetIamPolicy` in Cloud Audit Logs that Fugaro's trail lacks; its rows join `status`.
- Files: `internal/workspace/drift.go` (new), `internal/workspace/drift_test.go` (new),
  `internal/infra/auditlogs.go` (new), `internal/gcpfake/logging.go`,
  `internal/cli/workspace_project_drift.go` (new).
- Interfaces: implements C2 `Drift`; consumes C2 `MemberOps.Bindings`.
- Tests: `TestDriftIgnoresFugaroAccounts`, `TestDriftExtraAndMissing`, `TestDriftNamesConsoleChange`.
- Command: `go test ./internal/workspace/ ./internal/infra/ ./internal/cli/ -run 'TestDrift|TestAuditLogs' -count=1`.
- Red step: the Fugaro-accounts test fails against the stub.
- Depends on: C2.
- Commit: `workspace: drift against the roster and the audit logs (WU12, D2806, D2811)`.
- Reviewer tier: opus, mandatory independent review.

### WU13: `workspace project reconcile`
- Acceptance: adds missing grants and removes extras on the member's roster from `Drift`, then
  converges; `--dry-run` prints the change and writes nothing; each change writes an audit event.
- Files: `internal/cli/workspace_project_reconcile.go` (new), `internal/cli/workspace_project_reconcile_test.go` (new).
- Interfaces: consumes C2 `Drift`, `MemberOps`; C1 `AuditSink`.
- Tests: `TestReconcileDryRunWritesNothing`, `TestReconcileRemovesExtra`.
- Command: `go test ./internal/cli/ -run TestReconcile -count=1`.
- Red step: the remove test fails against the stub's empty report.
- Depends on: C1, C2.
- Commit: `workspace: reconcile a member's grants (WU13, D2809)`.
- Reviewer tier: opus, mandatory independent review.

### WU14: `fugaro update` seeds the roster
- Acceptance: on a workspace's own project with no roster node, `fugaro update` seeds it from the
  member table and the members' rosters and writes each member's mirror; with a node present it
  changes nothing; an installation at the previous version converges with no other command (F19).
- Files: `internal/cli/update_workspace_roster.go` (new), `internal/cli/update_workspace_roster_test.go`
  (new), `internal/cli/update.go`.
- Interfaces: consumes C1 `Store`, C2 `MemberOps.WriteMirror`.
- Tests: `TestUpdateSeedsRosterOnce`, `TestUpdateFromPreviousVersionConverges`.
- Command: `go test ./internal/cli/ -run 'TestUpdateSeedsRoster|TestUpdateFromPreviousVersion' -count=1`.
- Red step: the seed test fails today.
- Depends on: C1, C2.
- Commit: `update: seed the workspace roster (WU14, F19)`.
- Reviewer tier: opus.

### WU15: the state writer
- Acceptance: `StateWriter.Save` writes the project's settings and spend summary, and the
  workspace's audit export, as a pull request to the E6 designated repository and folder through
  R9's writer; with no designated repository it writes a local file and names it.
- Files: `internal/cli/workspace_state.go` (new), `internal/cli/workspace_state_test.go` (new).
- Interfaces: consumes C1's `AuditEvent` and R9's writer; produces a concrete writer I1 adapts to
  C2's `StateWriter`.
- Tests: `TestStateSaveOpensPR`, `TestStateSaveLocalWithoutDesignatedRepo`.
- Command: `go test ./internal/cli/ -run TestStateSave -count=1`.
- Red step: the PR test fails against the stub.
- Depends on: C1; **periodic-reviews R9 (#3140) merged** (behaviour: the writer must exist).
- Commit: `workspace: state snapshots to the designated repository (WU15, D2807)`.
- Reviewer tier: standard.

### I1: wire the workspace commands and prove them end to end
- Acceptance: the stubs are deleted; `newWorkspaceCmd` registers `init`, `project add|ls|rm|status|reconcile`,
  `user add|rm|ls|grant|revoke`, `domains add|rm|ls` with the real implementations; the end-to-end
  test passes.
- Files: `internal/workspace/roster_stub.go` (deleted), `internal/workspace/memberops_stub.go`
  (deleted), `internal/cli/workspace.go`, `internal/cli/workspace_e2e_test.go` (new).
- Interfaces: adapts C1's `Store`/`AuditSink` onto WU1, C2's `MemberOps` onto WU4 and WU9,
  `StateWriter` onto WU15, and joins the commands.
- Tests: `TestWorkspaceProjectsUsersEndToEnd`: init (folder) → project add (adopt) → user add
  (refused, then `--external`) → grant → status clean → a binding added straight in the fake →
  status names the drift and the audit-log entry → reconcile → user rm (prefix swept) → project rm
  (state saved, nothing deleted).
- Command: `go test ./internal/cli/ -run TestWorkspaceProjectsUsersEndToEnd -count=1`.
- Red step: the test fails while either stub is wired.
- Depends on: WU1–WU9 and WU11–WU15 (WU10 stands alone).
- Commit: `workspace: wire projects and users end to end (I1)`.
- Reviewer tier: opus, mandatory independent review.

### WU16: docs, skills and the release note
- Acceptance: the plugin skills and docs name the new commands and not `workspace member`; the
  release note's `### For operators` is "run `fugaro update`"; `docs/workspaces/README.md` links here.
- Files: `plugin/skills/working/SKILL.md`, `docs/workspaces/README.md`, `docs/releases/<next>.md`,
  `website/data/release-summaries.yaml`.
- Tests: the plugin's command-name tests; `go test ./internal/cli/ -run Docs`.
- Command: `go test ./plugin/... ./internal/cli/ -run 'Docs|Skill' -count=1`.
- Red step: the plugin test fails while a skill names a command that does not exist.
- Depends on: I1 (behaviour: the commands must exist for the plugin tests).
- Commit: `docs: workspace projects and users (WU16)`.
- Reviewer tier: opus (plugin skills).

### WU17: Checks 720 and 721
- Acceptance: two owner-run scripts with offline tests: **Check 720** creates a test folder, runs
  `workspace init`, `project add` and `project rm` on a throwaway project and checks the folder move
  and its return (U1); **Check 721** grants a role in the console, runs `status` and checks the drift
  row names the audit-log entry (U5).
- Files: `scripts/live-checks/check-720.sh` (new), `scripts/live-checks/check-720_test.sh` (new),
  `scripts/live-checks/check-721.sh` (new), `scripts/live-checks/check-721_test.sh` (new),
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`.
- Tests: `TestLiveCheckScripts`, and each script's own `_test.sh`.
- Command: `go test ./scripts/ -run TestLiveCheckScripts -count=1`.
- Red step: the live-checks test fails until both scripts are listed.
- Depends on: I1 (behaviour: the scripts drive the real commands).
- Commit: `live-checks: Checks 720 and 721 (WU17)`.
- Reviewer tier: standard.
- Owner-run: `scripts/live-checks/check-720.sh --org <org-id> --billing <account>` then
  `scripts/live-checks/check-721.sh --workspace <name> --project <id>`, in the owner's terminal. No
  agent runs any step of either.

## 4. Parallel execution

**Levels** (tasks in one level run at once), as `fugaro plan graph` computes them:

| Level | Tasks |
| --- | --- |
| 0 | WU0 |
| 1 | C1, C2, WU1, WU3, WU10 |
| 2 | WU2, WU4, WU5, WU6, WU7, WU8, WU9, WU11, WU12, WU13, WU14, WU15 (WU15 also waits on #3140) |
| 3 | I1 |
| 4 | WU16, WU17 |

**Critical path:** WU0 → C1 → WU11 → I1 → WU16 (the read's). Sequential by necessity: WU0 first
(D2800's gate: its review may reshape both contracts), I1 after every side it joins, WU16 and WU17
after the commands exist. WU1 and WU3 are pure providers: they consume no contract, expose a
concrete implementation in basic types, and I1 adapts the contract's interfaces onto them, so they
run beside the contracts.

**Shared files** (no level holds two writers of one file):

| File | Writers, by level |
| --- | --- |
| `internal/cli/workspace.go` | WU5 (2), I1 (3) |
| `internal/cli/offboard.go` | WU9 (2) |
| `internal/cli/user.go`, `user_grant.go`, `user_test.go` | WU10 (1) |
| `internal/budget/rules/workspace.json.tmpl`, `workspace_test.go` | WU1 (1) |
| `internal/budget/rules/rules.json.tmpl` | WU4 (2) |
| `internal/gcpfake/crm.go` | WU2 (2) |
| `internal/gcpfake/logging.go` | WU12 (2) |
| `deploy/terraform/gcp/roots/workspace/*` | WU3 (1) |
| `internal/workspace/roster_stub.go`, `memberops_stub.go` | C1, C2 (1); I1 deletes them (3) |
| `internal/workspace/roster.go`, `rosterstore.go`, `memberops.go`, the two contract documents, the two new schemas | C1, C2 (1) and their changes only |
| `docs/workspaces/README.md` | WU0 (0), WU16 (4) |
| `SECURITY.md` | WU0 (0) |
| `schemas/workspace-projects.schema.json` | WU11 (2) |

## 5. The dependencies broken and kept

Broken, 24 edges, each by a contract and re-joined by I1 (each side needed only the shape):

- **By C1 (the roster):** WU5, WU7, WU11, WU14 → WU1 (they read and write through `Store`; the RTDB
  write is WU1's); WU8, WU13 → WU1 (they write an `AuditEvent`, not how the sink stores it);
  WU2 → WU1 (the folder link records the original parent on the roster entry); WU4 → WU1 (the
  mirror is `MirrorEntry`); WU12 → WU1 (drift reads members and kinds); WU15 → WU1 (the audit export
  reads `AuditEvent`).
- **By C2 (member operations):** WU5, WU7, WU8, WU13, WU14 → WU4 (they call `MemberOps`); WU9 → WU4
  (`Offboard` joins that interface); WU12 → WU4 (drift reads `Bindings`); WU5, WU6 → WU2 (they call
  `Authority.Link`/`Unlink`); WU2 → WU3 (the SA authority reads the IAM account's output by the name
  C2 fixes); WU7 → WU9 (`user rm` calls `Offboard`); WU13 → WU12 (reconcile consumes `DriftReport`);
  WU6, WU11 → WU15 (they call `StateWriter`).

Kept:

- WU0 → C1, C2, WU1, WU3, WU10: D2800's mandatory review gates every task, and its findings may
  change the shapes — a contract written before it would be rewritten.
- C1/C2 → each consumer: the contract is what lets it run beside its provider.
- WU1–WU15 → I1: I1 joins them. I1 → WU16, WU17: the skills' tests and the live checks need the
  real commands (behaviour).
- #3140 (periodic-reviews R9) → WU15: the writer must exist (behaviour, outside this plan).

### 5a. What the graph read reports

`fugaro plan graph --plan docs/workspace-projects-users/implementation-plan.md`, 2026-11-18: no
refusal, five levels; **24 edges broken, 3 kept** in the read's own count, **5 tasks added** (C1,
C2, I1 and the two anticipated contract changes); the critical path goes from **6 tasks, weight 13**
before the pass to **5 tasks, weight 9.5** after it: "improves". The weights are the plan's own
`size` guesses (a contract 0.5, the largest tasks 3); no run history exists yet for any of them.

## 6. Contract changes anticipated

- **R-member-firebase** on C2: if U2 shows the member's Firebase project cannot be read from its
  outputs, `Converge` takes it as an argument. Reaches WU4, WU5, WU7, WU8, WU14.
- **R-roster-sa** on C1: if WU0's review asks for a machine's owner or expiry on `Member`. Reaches
  WU1, WU5, WU7, WU12.

## 7. Checks this plan takes

From the block **Checks 720–729**: **Check 720** (the folder move and its return, U1) and **Check 721**
(console drift named from Cloud Audit Logs, U5), both owner-run (WU17). 722–729 reserved.

## 8. After-deploy steps

None beyond `fugaro update`: WU14 seeds the roster and mirrors on the workspace owner's next update,
and WU16's release note says so.

## 9. Considered and declined

- Direct IAM writes from the workspace (D2801's alternative): a second role→binding mapping.
- An organization policy for domains (D2812's alternative).
- A read or write grant on the workspace database for project admins (D2813, D2817 amended): RTDB
  IAM cannot scope it.
- Linking fugaro-prod app sign-ins (D2814): no task.

## 10. Cost

21 runs (WU0, C1, C2, WU1–WU17, I1), none launched. The gateway enforces each run's cap
(`fugaro budget set --repo`); the worst case is 21 times that cap, plus one independent review run per
task marked "mandatory independent review" (12: WU0, C1, C2, WU2, WU3, WU4, WU7, WU8, WU9, WU12,
WU13, I1).
