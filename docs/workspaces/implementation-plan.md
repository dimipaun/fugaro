# Workspaces: the implementation plan

**Nothing here is approved.** Every decision in [reconciliation.md](reconciliation.md) §4 is open,
and no task below starts before the owner has answered the decisions it depends on and has approved
that task by name. This plan is written to the **recommended** answer of each decision; a decision
answered the other way changes the task that names it.

Repository names in this file are placeholders (`owner/repo-a`, `owner/repo-b`), a person is
`person-1`, and a workspace is `workspace-a`. No test named here uses a real credential, a real
address or a string shaped like a secret: the fixtures use marker values, and §6 states the rule.

## 1. What the owner must answer first

| Decision | Blocks | Recommendation the plan is written to |
| --- | --- | --- |
| D980 | W3 | The workspace has its own Firebase project |
| D981 | W0, W1 | At most one workspace per project |
| D982 | W2 | The record is declared locally and published by `fugaro update` |
| D983, D984 | W4 | One sign-in at the workspace, one exchange per member, at the member |
| D985 | W5 | The member's own lists are the whole of the admission |
| D986 | W4 | Hashes only |
| D987 | W6 | One workspace bundle, one workspace service |
| D988 | W6 | A per-project allow-list, intersected with the member's policy |
| D989 | W7 | Enforced at the service, advisory at the member's gateway |
| D990 | W7, W9 | The cap bounds the workspace's own credentials' spend |
| D991 | W7 | A rollup from each member's ledger, `stale` rather than zero |
| D992 | W8 | `fugaro budget kill` gains a workspace scope |
| D993 | W1, W8 | Local writes and reads; `update` carries the cloud |
| D994 | — | No console or mobile task in this plan |
| D995 | W9 | Not a new licensed unit and not a new price |

**One thing must be read before any task starts:** [spec.md](spec.md) §8 is a threat model and its
independent review is **mandatory** by the owner's brief. W4, W5, W6 and W7 each add a surface that
section describes, and none of them starts before that review has been read and its findings
answered.

## 2. The order, and what forces it

Three dependencies force the order, and none of them is a preference:

1. **The record comes before everything** (W0, W1, W2). A member cannot join a workspace whose record
   has no shape, and the shape is what the later tasks validate against.
2. **The exchange is the MCP design's authorization server** (W4). `docs/mcp-server/` §3 puts one
   inside a member's coordinator, and it is in **open pull request #1487**, not on `main`. W4 is
   written to that design's interface; **if that design changes its library (its D921) or its flow,
   W4 changes with it**, and if the owner declines the MCP design, W4 has no host and this plan's
   SSO half does not start. That dependency is the whole reason §5 recommends the slot it does.
3. **The shared pool comes before the cap** (W6 before W7). The cap is enforced where the calls are
   paid for (D989), and until there is one place that pays for them there is no such place. A cap
   built first would be the member-gateway-only version D989 rejects.

Everything else is independent: W3 (the workspace's Firebase and its rules) can run beside W1, and
W8 and W9 come last because they describe a thing that exists.

## 3. The tasks

Each task is independently mergeable, names its own red step, and carries a **SEC** flag: a task
flagged **SEC** is security-sensitive and needs an independent review of its own before it merges.

### W0 — the workspace record

**Interface.** A new pure package `internal/workspace`:

```go
type Record struct {
    Name     string
    Members  []Member
    Identity IdentityProvider
    Cap      Cap
}
type Member struct{ Project string; Repos []string }
type IdentityProvider struct{ FirebaseProject string; Issuer string }
type Cap struct{ MonthlyUSD float64; Set bool }

func Parse(raw []byte) (*Record, error) // strict: an unknown field is an error
func (r *Record) Validate() error
```

**Files (5):** `internal/workspace/workspace.go`, `internal/workspace/workspace_test.go`,
`internal/workspace/export_test.go`, `internal/workspace/testdata/record-a.yaml`,
`schemas/workspace.schema.json`.

**Red step.** `internal/workspace/workspace_test.go`'s
`TestRecordRefusesAnUnknownField` fails to build — the package does not exist — and it is the test
the task exists for: every config and wire format here decodes strictly, and a record that silently
accepted a field nobody decided would be a shape two writers could disagree about.

**Tests.** `TestRecordRefusesAnUnknownField`,
`TestRecordRefusesAMemberListThatIsNotSortedUnique`,
`TestRecordNameUsesTheProjectNameGrammar` (F21: it reuses `config.ProjectNameRE`, not a second
grammar), `TestRecordRefusesASecondIdentityProvider`,
`TestRecordRefusesACapThatIsNotPositiveWhenSet`, `TestRecordRoundTripsThroughTheSchema` (the
`schemas/` file and the struct agree).

**SEC:** no. It is a document shape with no cloud and no credential.

### W1 — the local layer, and the `fugaro workspace` reads and writes

**Interface.** `localcfg.Config` gains `Workspace string \`yaml:"workspace,omitempty"\``; the new
`fugaro workspace` family (D993) with `ls`, `show`, `add`, `rm`, `member add`, `member rm`, `cap set`
— every write a local config edit through the same check, diff, backup and atomic write the `models`
commands use, and every one ending with `next: fugaro update`.

**Files (7):** `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`,
`internal/cli/workspace.go`, `internal/cli/workspace_test.go`, `internal/cli/workspace_json.go`,
`internal/cli/workspace_json_test.go`, `schemas/workspace-list.schema.json`.

**Red step.** `TestWorkspaceAddWritesOneKeyAndKeepsEveryOtherByte` fails — the command does not
exist. It is the test the task exists for: a workspace membership must be one key added to a file
that is otherwise byte-identical (comments, ordering, quoting), the same promise the `models` writes
make and the same one `yamledit` exists to keep.

**Tests.** `TestWorkspaceAddWritesOneKeyAndKeepsEveryOtherByte`,
`TestWorkspaceRmRemovesTheKeyAndNothingElse`,
`TestWorkspaceLsListsEveryMembershipOffline` (no bucket, no API, no credentials),
`TestWorkspaceLsJSONIsVersioned` (the document carries a version, and only additions are allowed
within it), `TestWorkspaceLsJSONCarriesNoCredentialShapedField` (the `config list` rule: a named set
of fields, not a marshalled `localcfg.Config`),
`TestWorkspaceWritesNothingUnderACodingAgentsMarker` (the `models` rule, `--yes` included),
`TestLocalcfgRefusesASecondWorkspaceKey` (D981 is a scalar).

**SEC:** no. Local file edits and offline reads.

### W2 — the workspace node, and `fugaro update`'s publish stage

**Interface.** `fugaro update` gains the stage that writes `workspaces/<name>` in the workspace's own
database from the member configs it can read, behind its own confirmation, migrating an older node in
place (never "delete and publish again"), on the policy stage's pattern.

**Files (5):** `internal/projectpolicy/workspace.go`, `internal/projectpolicy/workspace_test.go`,
`internal/cli/update_workspace.go`, `internal/cli/update_workspace_test.go`, `internal/cli/update.go`.

**Shared with another work:** `internal/cli/update.go` is the update-converge work's file (its
policy stage). **The rule is one writer at a time**: this task's stage is added after that work's
stage has merged, or both stages land in one commit by agreement — never two branches editing the
stage list at once.

**Red step.** `TestUpdatePublishesTheWorkspaceNodeAndMigratesAnOlderOneInPlace` fails: the stage does
not exist, and the migration half is the reason it exists — an installation that already has a
workspace node from an older release must converge, not be asked to publish again.

**Tests.** `TestUpdatePublishesTheWorkspaceNodeAndMigratesAnOlderOneInPlace`,
`TestUpdateRefusesToPublishAWorkspaceWithNoMembers`,
`TestUpdateNamesTheOneCommandAndNoOther` (no `workspace publish` anywhere in the output),
`TestUpdateLeavesAMemberWithNoWorkspaceAlone`.

**SEC:** no. It publishes a member list; it publishes no credential and no address.

### W3 — the workspace's own Firebase project and its rules

**Interface.** The workspace's Firebase project is created and converged by the installation path
(`internal/infra` over the roots in `deploy/terraform/gcp`), and the workspace's database rules ship
with it: `workspaces/<name>` is written by the workspace's own service account and read by **no
client**.

**Files (8):** `internal/infra/tf/workspace.go`, `internal/infra/tf/workspace_test.go`,
`internal/budget/rules/workspace.go`, `internal/budget/rules/workspace.json.tmpl`,
`internal/budget/rules/workspace_test.go`, `internal/cli/workspace_create.go`,
`internal/cli/workspace_create_test.go`, `deploy/terraform/gcp/roots/workspace/main.tf`.

**Red step.** `TestWorkspaceRulesDenyEveryRunTokenTheRecord` fails — the template does not exist. It
is the test the task exists for: the record, the token hashes and the rollup live in a database, and
a rules file that admitted a run token to them would be a leak with a public address.

**Tests.** `TestWorkspaceRulesDenyEveryRunTokenTheRecord`,
`TestWorkspaceRulesDenyEveryRunTokenTheTokenRecord` (D986: hashes only, and no client reads them),
`TestWorkspaceRulesAllowTheServicesOwnAccount`,
`TestWorkspaceRulesVersionIsPinnedAndMigrated` (the rules marker's own pattern, so an older CLI
converges rather than half-applies).

**SEC:** **yes.** It creates an identity boundary and writes the rules that hold it.

### W4 — the exchange at a member

**Interface.** A member's authorization server (the MCP design's §3) gains the token-exchange grant:
it verifies a workspace token against the workspace's published keys, its issuer and its audience,
and mints the member's own token. **It depends on the MCP design's implementation (its M3)**, and it
adds no endpoint to the workspace.

**Files (7):** `ee/coordinator/oauth/workspace.go`, `ee/coordinator/oauth/workspace_test.go`,
`ee/coordinator/oauth/workspace_keys.go`, `ee/coordinator/oauth/workspace_keys_test.go`,
`internal/cli/workspace_login.go`, `internal/cli/workspace_login_test.go`,
`schemas/workspace-login.schema.json`.

**Red step.** `TestExchangeRefusesAWorkspaceTokenFromAnotherWorkspace` fails — the grant does not
exist. It is the test the task exists for: the whole security claim of the SSO half is that a
workspace token is a bearer for *one* member's exchange and for nothing else, and a grant that
verified the signature without the issuer and the audience would accept any workspace's token.

**Tests.** `TestExchangeRefusesAWorkspaceTokenFromAnotherWorkspace`,
`TestExchangeRefusesAnExpiredWorkspaceToken`, `TestExchangeNeverEchoesTheToken` (the `credsvc`
rule, applied to the new endpoint),
`TestExchangeRefusalIsIndistinguishableForAnUnknownMember` (spec §8: no member enumeration),
`TestExchangeMintsAMemberTokenWithTheMembersOwnLimits`,
`TestExchangeStoresOnlyHashes`.

**SEC:** **yes.** It is the identity surface, and [spec.md](spec.md) §8 is its review brief.

### W5 — the member's admission, from the member's own lists

**Interface.** The exchange admits the person only when the member's launchers/operators lists
(`fugaro user`) name their address, compared with `mobileauth.NormalizeEmail`'s one rule. It reads
the lists and never writes them.

**Files (5):** `ee/coordinator/oauth/admit.go`, `ee/coordinator/oauth/admit_test.go`,
`internal/projectpolicy/members.go`, `internal/projectpolicy/members_test.go`,
`internal/cli/workspace_admit_test.go`.

**Red step.** `TestAdmitRefusesAnAddressTheMembersListsDoNotName` fails, and
`TestAdmitDoesNotWidenAMembersLists` is its other half: the task exists to prove the workspace cannot
make someone a launcher, so a test that only proved the refusal would not be the test.

**Tests.** `TestAdmitRefusesAnAddressTheMembersListsDoNotName`,
`TestAdmitDoesNotWidenAMembersLists`, `TestAdmitIsCaseFoldedOnTheDomainOnly` (the `mobileauth`
rule), `TestAdmitRefusesADisplayNameForm` (a name is not an admission).

**SEC:** **yes.** It is an authorization decision.

### W6 — the workspace credentials service and the bundle

**Interface.** The credentials service (`docs/credentials-service/`) is deployed once in the
workspace's project. Its front door's admitted set becomes the **workspace's** member job service
accounts; its bundle is the workspace's one secret; every shared credential carries a per-project
allow-list, and the effective set is that list intersected with the member's own policy.

**Files (9):** `internal/credsvc/workspace.go`, `internal/credsvc/workspace_test.go`,
`internal/credsvc/bundle_workspace.go`, `internal/credsvc/bundle_workspace_test.go`,
`internal/creds/workspace.go`, `internal/creds/workspace_test.go`,
`internal/projectpolicy/workspace_credentials.go`,
`internal/projectpolicy/workspace_credentials_test.go`, `internal/cli/creds.go`.

**Red step.** `TestWorkspaceCredentialIsRefusedWhenTheMembersPolicyExcludesIt` fails. It is the test
the task exists for: the only-tighten rule is what makes a shared pool safe, and a service that
served a credential a member's policy excluded would be a policy bypass.

**Tests.** `TestWorkspaceCredentialIsRefusedWhenTheMembersPolicyExcludesIt`,
`TestWorkspaceCredentialIsRefusedForAMemberOutsideItsAllowList`,
`TestWorkspaceServiceRefusesAJobServiceAccountOutsideTheWorkspace`,
`TestWorkspaceServiceNeverReturnsACredentialValue` (the package's rule 2),
`TestWorkspaceBundleIsOneSecretWithOneGrant` (the Q6 shape, applied to the workspace).

**SEC:** **yes.** It holds and proxies model credentials.

### W7 — the workspace cap: the rollup and the enforcement

**Interface.** The workspace service counts what the calls it pays for cost, against the cap; it
recomputes the rollup from each member's own run ledger on the history job's schedule, recording a
member it cannot read as `stale`; and it publishes the advisory headroom to each member's database.
The member's gateway reads the headroom and stops a run early; it never decides the cap.

**Files (8):** `internal/credsvc/workspace_cap.go`, `internal/credsvc/workspace_cap_test.go`,
`internal/budget/workspace.go`, `internal/budget/workspace_test.go`,
`internal/budget/workspace_rollup.go`, `internal/budget/workspace_rollup_test.go`,
`internal/gateway/workspace_headroom.go`, `internal/gateway/workspace_headroom_test.go`.

**Red step.** `TestRollupRecordsAnUnreadableMemberAsStaleNotZero` fails. It is the test the task
exists for: an unreadable member that read as zero would look free, and the workspace would spend
its whole cap in the member nobody could see.

**Tests.** `TestRollupRecordsAnUnreadableMemberAsStaleNotZero`,
`TestWorkspaceCapRefusesTheCallThatWouldExceedIt`,
`TestWorkspaceCapIsNotWidenedByAMembersOwnCap` (the merge is min, and the workspace can only
tighten), `TestHeadroomIsAdvisoryAndTheGatewayNeverDecidesTheCap`,
`TestWorkspaceCapBoundsOnlyTheCredentialsItPaysFor` (D990, stated as a test so the docs cannot drift
from it).

**SEC:** **yes.** It is a spend ceiling and a cross-project read (spec §8).

### W8 — the stop, and `fugaro workspace cap`

**Interface.** `fugaro budget kill` gains a workspace scope (D992), which is the workspace service's
own stop flag; `fugaro workspace cap set --workspace <name> --monthly <amount>` records the cap
locally and `fugaro update` publishes it.

**Files (5):** `internal/cli/budget_kill_workspace.go`, `internal/cli/budget_kill_workspace_test.go`,
`internal/cli/workspace_cap.go`, `internal/cli/workspace_cap_test.go`,
`internal/budget/workspace_kill_test.go`.

**Red step.** `TestWorkspaceKillStopsAMemberRunInFlight` fails: the scope does not exist. It is the
test the task exists for — the stop is the one control that does not depend on any member's honesty
(spec §8), so it is the control that must be proven to reach a member.

**Tests.** `TestWorkspaceKillStopsAMemberRunInFlight`,
`TestWorkspaceKillDoesNotStopAnotherWorkspace`,
`TestWorkspaceCapSetWritesTheLocalConfigOnly`, `TestWorkspaceCapIsNeverSpelledGlobal` (F10).

**SEC:** **yes.** It is a stop control over every member.

### W9 — the docs, and the release note's operator line

**Interface.** The documentation the tasks above make true: `SECURITY.md`'s workspace section,
`docs/design/v1.md`'s project section, the CLI reference the website generates, and the release
note's `### For operators` entry.

**Files (≤6, and none of them in this pull request):** `SECURITY.md`, `docs/design/v1.md`,
`docs/release.md`'s checklist if it names the surfaces, `docs/releases/vX.Y.Z.md`, and the
`website/` pages the CLI reference is generated into.

**Red step.** The docs pins the release adds fail (`go test ./internal/cli/ -run Docs`), because the
sentences they pin are not written yet.

**Tests.** The `internal/cli` docs pins the change adds, and `website/`'s own checks if a website
page moves.

**SEC:** no, but it is the task that must state, in `SECURITY.md`, the residual risks
[spec.md](spec.md) §8 lists — a release that shipped the capability without them would be a
documentation failure of a security feature.

## 4. The shared-files table

Files more than one task touches, and the rule for each. **One writer at a time** is the rule
wherever two tasks would edit the same file; the second waits for the first to merge.

| File | Tasks | Rule |
| --- | --- | --- |
| `internal/localcfg/localcfg.go` | W1 | One writer. W1 owns the `workspace:` key; a later task that needs another key takes it in its own commit |
| `internal/cli/update.go` | W2 | One writer, and it is **not** this work's: the update-converge work's stage list. W2's stage is added after that work's stage has merged |
| `internal/cli/creds.go` | W6 | One writer. W6 owns the workspace bundle's `creds` verbs; the credentials service's own tasks keep the rest |
| `internal/cli/budget.go` | W8 | One writer for the kill scope; `fugaro budget set`'s own scopes are untouched |
| `internal/budget/rules/*.json.tmpl` | W3, W7 | W3 owns the workspace's own template; W7 adds no rule to it. The member's template is untouched by both |
| `internal/projectpolicy/*.go` | W2, W5, W6 | W2 owns `workspace.go`, W5 `members.go`, W6 `workspace_credentials.go`: three files, no overlap |
| `schemas/*.json` | W0, W1, W4 | One file each (`workspace`, `workspace-list`, `workspace-login`); the schema directory's own test checks every file is referenced by a decoder |
| `ee/coordinator/oauth/*.go` | W4, W5 | W4 owns `workspace*.go`, W5 `admit.go`; W5 depends on W4's verified-identity type and takes it as given |

## 5. The release slot

**Recommended: the design belongs to the 0.15.0 documentation window; the code belongs to
0.17.0.**

Why the design is 0.15.0's: this folder is documentation, the owner asked for a design and approved
one on 2026-10-31, and it costs nothing to carry it beside the release it will be built after. It is
the same slot the MCP design takes for its own documents.

Why the code is **0.17.0** and not earlier:

1. **The exchange has no host until the MCP server exists.** W4 is written to the MCP design's
   authorization server, and that design recommends **0.16.0** for its code
   (`docs/mcp-server/implementation-plan.md` §5). A workspace cannot mint a member token before
   there is a server that mints member tokens.
2. **The shared pool has no host until the credentials service's later slice.** The service's Slice 3
   is unscheduled (`docs/credentials-service/implementation-plan.md`), and W6 is a *second*
   deployment of it, not a flag on the first. Building the workspace's pool on an unbuilt slice
   would make this work's first task depend on another plan's last.
3. **The workspace's services are `ee/`.** Rulings A5 and A6 (`docs/ee-reorg/spec.md`:7) put the
   coordinator and the credentials service's hosted half under the business folder, and the
   workspace's exchange endpoint and its service deployment are those. A release that shipped them
   in the MIT core would contradict a standing ruling, so the slot has to be one where the `ee/`
   placement is settled — which it is not yet at this base.

Why not later than 0.17.0: nothing else the workspace needs is unbuilt by then. The `update` stage
(W2), the kill scope (W8) and the docs (W9) are ordinary work on surfaces that exist today.

**What would move it.** If the owner declines the MCP design, W4 and W5 have no host and the SSO
half waits for whichever design replaces it — the record, the node, the credentials pool and the cap
(W0–W3, W6–W8) do not depend on it and could ship earlier, as a workspace without single sign-on.
That is a real option and the owner may prefer it; this plan does not take it, because the owner's
spec names the sign-in as one of the four things a workspace is.

## 6. The test rules

- **Hermetic.** No network, no home directory, no real cloud: `internal/gcpfake`,
  `internal/testutil`, `t.TempDir()`, and the RTDB emulator harness the rules tests already use
  (`internal/budget/rules/emulator`). Anything needing an account is behind the `live` tag, and
  **no agent runs a live step**: the owner-run checks are [reconciliation.md](reconciliation.md) §6's
  table, named and not numbered.
- **No secret-shaped literals.** No test, fixture, doc or log line carries a credential, a key, a
  token or anything shaped like one. The fixtures use marker strings (`"marker-workspace-token"`,
  `"marker-job-email"`), and a test fails a fixture that looks like a real one.
- **No real names.** Repository names are `owner/repo-a`; a person is `person-1`; a workspace is
  `workspace-a`; an address is at `example.com`.
- **The red step is the only mutation proof.** Each task's red step is run and seen to fail for the
  reason the task exists, and that is stated in the pull request. No further mutation testing.
- **Targeted tests.** `GOFLAGS=-p=2`; a named `-run` filter; never the whole `internal/cli` or
  `internal/runner` package.
- **Docs are tested.** Any task that touches `docs/`, `SECURITY.md` or the website runs
  `go test ./internal/cli/ -run Docs` in the same pull request and keeps the pinned fact while
  changing the wording in both places.

## 7. What this plan does not own

- **The MCP server's tasks.** `docs/mcp-server/implementation-plan.md`'s M1–M10 are that work's; W4
  and W5 are written to their interface and start after them.
- **The credentials service's own slices.** W6 is a second deployment of that service, and the
  service's own plan owns the first.
- **The console and the mobile app.** D994: a console workspace screen is a later design, and it
  reads a versioned CLI document this plan's W1 produces.
- **The subscriptions key.** D995: a workspace is not a licensed unit, so no key, seat or price work
  is in this plan.
- **A workspace-wide repository allow-list.** Refused by [spec.md](spec.md) §9 and F18.
- **Any live check, by number.** The owner-run checks are named in
  [reconciliation.md](reconciliation.md) §6 and numbered by whichever task writes them.
