# Workspaces: the implementation plan

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** The sixteen decisions in [reconciliation.md](reconciliation.md) §4 (**D1290–D1305**) are
**answered**, every one as recommended, so no task below is waiting on a decision any more. **No
task is approved**: the owner approves each task by name, and one gate is still shut — [spec.md](spec.md)
§8 is a threat model whose independent SECURITY review is mandatory before this design merges and
**which the coordinator runs**, not this plan's author and not the runs that implement anything.

Repository names in this file are placeholders (`owner/repo-a`, `owner/repo-b`), a person is
`person-1`, and a workspace is `workspace-alpha`. No test named here uses a real credential, a real
address or a string shaped like a secret: the fixtures use marker values, and §6 states the rule.

## 1. The decisions the plan is written to

**Answered by the owner on 2026-11-04, every one as recommended.** The table is kept because each
row still names the task the answer binds; nothing in it is open. A reader looking for what was
asked and what the alternative cost should read [reconciliation.md](reconciliation.md) §4 and
[README.md](README.md).

| Decision | Blocks | Answer the plan is written to |
| --- | --- | --- |
| D1290 | W3 | The workspace has its own Firebase project |
| D1291 | W0, W1 | At most one workspace per project |
| D1292 | W2 | The record is declared locally and published by `fugaro update` |
| D1293, D1294 | W4 | One sign-in at the workspace, one exchange per member, at the member |
| D1295 | W5 | The member's own lists are the whole of the admission |
| D1296 | W4 | Hashes only |
| D1297 | W6 | One workspace bundle, one workspace service |
| D1298 | W6 | A per-project allow-list, intersected with the member's policy |
| D1299 | W7 | Enforced at the service, advisory at the member's gateway |
| D1300 | W7, W9 | The cap bounds the workspace's own credentials' spend |
| D1301 | W7 | A rollup from each member's ledger, `stale` rather than zero |
| D1302 | W8 | `fugaro budget kill` gains a workspace scope |
| D1303 | W1, W8 | Local writes and reads; `update` carries the cloud |
| D1304 | — | No console or mobile task in this plan |
| D1305 | W9 | Not a new licensed unit and not a new price |

**One thing must be read before any task starts:** [spec.md](spec.md) §8 is a threat model, its
independent SECURITY review is **mandatory before this design merges** by the owner's brief, and
**the coordinator runs it** — it is not a task of this plan. W4, W5, W6 and W7 each add a surface
that section describes, and none of them starts before that review has been read and its findings
answered. (The brief first put the review before the decisions; the owner answered all sixteen on
2026-11-04, so the gate it holds now is the merge.)

## 2. The order, and what forces it

Three dependencies force the order, and none of them is a preference:

1. **The record comes before everything** (W0, W1, W2). A member cannot join a workspace whose record
   has no shape, and the shape is what the later tasks validate against.
2. **The exchange is the MCP design's authorization server** (W4). `docs/mcp-server/` §3 puts one
   inside a member's coordinator, and that folder is **on `main`** now — merged as #1487 on
   2026-11-04 — though none of its code is written: it is a design folder with the same status line
   this one carries. W4 is written to that design's interface; **if that design changes its library
   (its D921) or its flow, W4 changes with it**, and if the owner declines the MCP design, W4 has no
   host and this plan's SSO half does not start. That dependency is the whole reason §5 recommends
   the slot it does.
3. **The shared pool comes before the cap** (W6 before W7). The cap is enforced where the calls are
   paid for (D1299), and until there is one place that pays for them there is no such place. A cap
   built first would be the member-gateway-only version D1299 rejects.

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
type Cap struct{ Monthly pricing.Micros; Set bool }

func Parse(raw []byte) (*Record, error) // strict: an unknown field is an error
func (r *Record) Validate() error
```

**Money is `pricing.Micros`, never a float.** The record holds the cap as integer micro-dollars
(`internal/pricing/pricing.go:18`), not as the `float64` the config layer's own caps use
(`internal/config/config.go:166`): the YAML writes a dollar amount, `Parse` converts it once with
`pricing.FromUSD` (`internal/pricing/pricing.go:26-28`), and below that point nothing does money
arithmetic in a float. That conversion is also what gives the cap a project's bounds — `FromUSD`
refuses a negative amount and anything above `pricing.MaxUSD`, the same refusal the config layer's
`validateBudget` makes for a project's caps (`internal/config/validate.go`:850, its two `FromUSD`
calls at `:861` and `:866`) — and micro-dollars are the unit the budget database stores
(`internal/budget/model.go:5-8`), so the published node needs no second conversion. `schemas/workspace.schema.json` carries the dollar amount as a number, as every other
schema here does: the schema is what a person writes, and the struct is the exact side of the
boundary.

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
`TestRecordRefusesACapThatIsNotPositiveWhenSet`,
`TestCapIsExactMicrosAndNotAFloat` (a cap of `0.1` is exactly `100_000` µ$: the parsed field is
integer micro-dollars, and a `float64` field is the mistake this test exists to make impossible),
`TestRecordRoundTripsThroughTheSchema` (the `schemas/` file and the struct agree).

**SEC:** no. It is a document shape with no cloud and no credential.

### W1 — the local layer, and the `fugaro workspace` reads and writes

**Interface.** `localcfg.Config` gains `Workspace string \`yaml:"workspace,omitempty"\``; the new
`fugaro workspace` family (D1303) with `ls`, `show`, `add`, `rm`, `member add`, `member rm`, `cap set`
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
`TestLocalcfgRefusesASecondWorkspaceKey` (D1291 is a scalar).

**SEC:** no. Local file edits and offline reads.

### W2 — the workspace node, and `fugaro update`'s publish stage

**Interface.** The **workspace owner's** `fugaro update` gains the stage that writes
`workspaces/<name>` in the workspace's own database from the workspace's local record, behind its own
confirmation, migrating an older node in place (never "delete and publish again"), on the policy
stage's pattern. **A member's `fugaro update` writes nothing there** — a member holds no grant on the
workspace's project (§5 of [spec.md](spec.md)) — so the stage is the owner's and the member's run of
the same command converges only the member's own installation.

**Who writes it, and why that is the whole of the rule.** Only the workspace owner's installation
holds a credential for the workspace's project, so only it can write `workspaces/<name>`. The
consequences are the design, not an accident:

- **Membership takes both sides.** A member is in the workspace when **the owner's record lists it**
  *and* **the member's own project config names the workspace**. Either alone is not membership: an
  owner that lists a project which has not named the workspace has published a claim about a machine
  it does not control, and a member that names a workspace whose record does not list it is asking to
  be admitted by a group that never admitted it. Both commands are local edits by different people,
  so the two sides converge on the next `fugaro update` on each side, and `workspace show`'s drift
  report is what names a half-done membership rather than either half silently winning.
- **Each member pins the issuer it trusts, in its own config.** A member's config records the
  workspace's Firebase project id and the workspace's issuer, and its exchange verifies **that**
  issuer — never one the workspace's own record supplies. A workspace record that could name an
  issuer would be a record that could point every member at an identity provider of its choosing,
  which is the one direction rule 1 does not survive.

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
`TestUpdateLeavesAMemberWithNoWorkspaceAlone`,
`TestUpdateOnAMemberNeverWritesTheWorkspaceNode` (the member holds no grant; a stage that tried would
be refused by IAM, and a task that reached for it is the defect this test names),
`TestMembershipNeedsBothSides` (listed but not naming the workspace, and naming it but not listed,
are both not membership and both are reported as drift).

**SEC:** **yes.** It is the write that decides who is in the group, and the record it publishes is the
one every member's exchange reads for the member list. A publish stage that admitted a member on one
side's word, or that let the record name the issuer, would be the workspace widening a member — the
one thing rule 1 forbids.

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
`TestWorkspaceRulesDenyEveryRunTokenTheTokenRecord` (D1296: hashes only, and no client reads them),
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

**Interface.** The exchange admits the person only when **two** things hold: the workspace token's
`email_verified` is true, and the member's launchers/operators lists (`fugaro user`) name their
address, compared with `mobileauth.NormalizeEmail`'s one rule. It reads the lists and never writes
them. The verified-address half is the credentials service's own rule for every class of caller
(`internal/credsvc/auth.go`:1018, `checkCallerToken`), and it is required here for the same reason:
an unverified address is one nobody proved they own.

**Files (5):** `ee/coordinator/oauth/admit.go`, `ee/coordinator/oauth/admit_test.go`,
`internal/projectpolicy/members.go`, `internal/projectpolicy/members_test.go`,
`internal/cli/workspace_admit_test.go`.

**Red step.** `TestAdmitRefusesAnAddressTheMembersListsDoNotName` fails, and
`TestAdmitDoesNotWidenAMembersLists` is its other half: the task exists to prove the workspace cannot
make someone a launcher, so a test that only proved the refusal would not be the test.

**Tests.** `TestAdmitRefusesAnAddressTheMembersListsDoNotName`,
`TestAdmitRefusesAnUnverifiedAddress` (a token whose `email_verified` is absent, false or not a
bool is refused, and the refusal names the claim and never the address — the `credsvc` rule at
`internal/credsvc/auth.go`:1018, applied to the exchange),
`TestAdmitDoesNotWidenAMembersLists`, `TestAdmitIsCaseFoldedOnTheDomainOnly` (the `mobileauth`
rule), `TestAdmitRefusesADisplayNameForm` (a name is not an admission).

**SEC:** **yes.** It is an authorization decision.

### W6 — the workspace credentials service and the bundle

**Interface.** The credentials service (`docs/credentials-service/`) is deployed once in the
workspace's project. Its front door's admitted set becomes the **workspace's** member job service
accounts; its bundle is the workspace's one secret; every shared credential carries a per-project
allow-list, and the effective set is that list intersected with the member's own policy.

**The caller's member is the verified service account, and the token's pins have to admit it.** The
service resolves a caller by folding the token's `email` and looking it up in its own account table
(`internal/credsvc/auth.go`:727), and it returns the configured spelling of what it found (`:738`) —
that, and nothing in a request body, header or query, is what names the member. `verifyRun`
(`:1047`) additionally refuses a token whose `aud` is not this service's Firebase project (`:1069`),
whose `iss` is not that project's securetoken issuer (`:1072`), or whose `fp` is not this service's
Fugaro project (`:1105`), so the workspace deployment accepts **each member's** Firebase project as
the issuer and **each member's** Fugaro project as `fp` and holds all three to the same member: the
account's, the issuer's and the `fp`'s must name one member, or the call is refused. This is a change
to `verifyRun`'s pinning and not only to the list it looks the account up in, and it is the half of
this task that a reader is most likely to miss.

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
`TestWorkspaceServiceRefusesATokenWhoseIssuerAndAccountDisagree` (the account names `owner/repo-a`
and the token's `aud`/`iss`/`fp` name `owner/repo-b`: the call is refused, because the member is
the account and the pins have to agree with it),
`TestWorkspaceServiceNeverReturnsACredentialValue` (the package's rule 2),
`TestWorkspaceBundleIsOneSecretWithOneGrant` (the Q6 shape, applied to the workspace).

**SEC:** **yes.** It holds and proxies model credentials.

### W7 — the workspace cap: the rollup and the enforcement

**Interface.** The workspace service recomputes **one** basis for the cap — the rollup (D1301) — from
each member's own run ledger on the history job's schedule, recording a member it cannot read as
`stale`, and publishes the advisory headroom to each member's database. The member's gateway reads
the headroom and stops a run early; it never decides the cap.

**One basis, and it is the rollup.** The service keeps no count of its own: the cap is measured
against the sum of the members' ledgers, and a task that added a second measurement — metering the
calls the service pays for at its own proxy — would be a change to D1301, which this round did not
make and [reconciliation.md](reconciliation.md) §8 records as an owner decision. What the service
enforces at the shared credential's front door is a **refusal** and not a count, and the case that
matters is the unreadable member: a `stale` member's shared-credential calls are refused, failing
closed, because a member nobody can read is a member whose spend nobody can bound — the same reason
the rollup records it as `stale` rather than zero.

**The grant it needs, and what that grant also reaches.** The rollup's read of a member's ledger and
its write of the advisory headroom are one IAM grant on the member's database, and it is the
credentials service's own custom role: exactly `firebasedatabase.instances.get` and
`firebasedatabase.instances.update` (`deploy/terraform/gcp/modules/installation/credentials.tf`:242-246).
The task does not invent a narrower one, because there is no narrower one: Realtime Database has no
path-level scope, `get` reads **any** node of the database and `update` writes **any** node of it
(`credentials.tf`:201-210), a condition cannot fence a path (`credentials.tf`:251-253), and IAM is
what the service account is subject to rather than the rules
(`deploy/terraform/gcp/modules/firebase/iam.tf`:52). So this task's grant is a whole-database read
and a whole-database write on every member, and what it buys is bounded by the code that holds it and
by nothing else — which is why spec §5's rule 1 is a property of the code, and why the service's own
role never grows to `firebasedatabase.admin` (`credentials.tf`:215-219). Stated here rather than in
spec §5 alone because it is this task, and not W6, that first needs the write.

**Files (8):** `internal/credsvc/workspace_cap.go`, `internal/credsvc/workspace_cap_test.go`,
`internal/budget/workspace.go`, `internal/budget/workspace_test.go`,
`internal/budget/workspace_rollup.go`, `internal/budget/workspace_rollup_test.go`,
`internal/gateway/workspace_headroom.go`, `internal/gateway/workspace_headroom_test.go`.

**Red step.** `TestRollupRecordsAnUnreadableMemberAsStaleNotZero` fails. It is the test the task
exists for: an unreadable member that read as zero would look free, and the workspace would spend
its whole cap in the member nobody could see.

**Tests.** `TestRollupRecordsAnUnreadableMemberAsStaleNotZero`,
`TestStaleMembersSharedCredentialCallsAreRefused` (the fail-closed half: a member the rollup could
not read is refused at the front door rather than served and counted, so an unreadable member can
neither spend the cap nor spend around it),
`TestWorkspaceCapRefusesTheCallThatWouldExceedIt`,
`TestWorkspaceCapIsNotWidenedByAMembersOwnCap` (the merge is min, and the workspace can only
tighten), `TestHeadroomIsAdvisoryAndTheGatewayNeverDecidesTheCap`,
`TestWorkspaceCapBoundsOnlyTheCredentialsItPaysFor` (D1300, stated as a test so the docs cannot drift
from it).

**SEC:** **yes.** It is a spend ceiling and a cross-project read (spec §8).

### W8 — the stop, and `fugaro workspace cap`

**Interface.** `fugaro budget kill` gains a workspace scope (D1302), which is the workspace service's
own stop flag; `fugaro workspace cap set --workspace <name> --monthly <amount>` records the cap
locally and `fugaro update` publishes it. **The stop's scope is stated and not implied**, because a
stop is the one control whose over-reach would be a workspace reaching into a member (spec §9): it
stops the runs of every member of the workspace and makes the workspace's credentials service refuse
every shared-credential call, and it touches nothing else — not a member's own credentials, not a
member's own budget or caps, not a member's sign-in, not a member's runs that never touch the
workspace.

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

**Recommended: 0.20.0 or later.** Re-slotted on 2026-11-04: the slot this section used to recommend
— design in 0.15.0, code in 0.17.0 — has **shipped** (0.15.0, 0.16.0 and 0.17.0 are all released
files in `docs/releases/`, and 0.17.0 is still the newest CLI release on `origin/main` at
`c2ca0aee`), so
the recommendation is remade rather than kept. The design is still only documentation and costs a
release nothing; the code does not belong in the next release either, for the reasons below.

**Why the design can ride any release.** This folder is documentation, the owner asked for a design
and approved one on 2026-10-31, and it changes no behaviour.

**Why the code is 0.20.0 or later, and not the next release:**

1. **It is big.** Ten tasks, three of them new deployables (a Firebase project of its own, a second
   credentials service, an exchange endpoint) and two new wire formats with schemas. Nothing on the
   plan is a flag on something that exists; W6 is a *second* deployment of the credentials service
   and W7 is a second spend ceiling beside the project's.
2. **It is security-heavy.** Five of the ten tasks carry the plan's own **SEC** flag (W3, W4, W5,
   W6 and W7: an identity boundary, an authorization decision, a credential pool and a spend
   ceiling), and [spec.md](spec.md) §8's threat model needs an independent SECURITY review **before
   this design merges**, run by the coordinator. Shipping an identity boundary, a cross-project read
   and a shared credential pool before that review's findings have had a release to settle in would
   put a security feature on the release's schedule rather than the review's.
3. **The exchange has no host until the MCP server exists.** W4 is written to the MCP design's
   authorization server. That design is **on `main`** (merged as #1487 on 2026-11-04) but **none of
   its code is written** — it is a design folder, and it recommends a slot of its own for its code —
   so a workspace cannot mint a member token before there is a server that mints member tokens.
4. **The shared pool has no host until the credentials service's later slice.** The service's Slice 1
   is still its first product slice and the rest are unscheduled
   (`docs/credentials-service/implementation-plan.md`), and W6 is a *second* deployment of it, not a
   flag on the first.
5. **The workspace's services are `ee/`.** Rulings A5 and A6 (`docs/ee-reorg/spec.md`:7) put the
   coordinator and the credentials service's hosted half under the business folder, and the
   workspace's exchange endpoint and its service deployment are those. A release that shipped them
   in the MIT core would contradict a standing ruling, so the slot has to be one where the `ee/`
   placement is settled.

**Why not later than the 0.20.0 window:** nothing else the workspace needs is unbuilt by then. The
`update` stage (W2), the kill scope (W8) and the docs (W9) are ordinary work on surfaces that exist
today.

**What would move it.** If the owner declines the MCP design, W4 and W5 have no host and the SSO
half waits for whichever design replaces it — the record, the node, the credentials pool and the cap
(W0–W3, W6–W8) do not depend on it and could ship earlier, as a workspace without single sign-on.
That is a real option and the owner may prefer it; this plan does not take it, because the owner's
spec names the sign-in as one of the four things a workspace is. And a SECURITY finding that asks
for a redesign of §8's surfaces moves the slot again: the review's answer, not this section, decides
when W4–W7 can start.

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
  `workspace-alpha`; an address is at `example.com`.
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
- **The console and the mobile app.** D1304: a console workspace screen is a later design, and it
  reads a versioned CLI document this plan's W1 produces.
- **The subscriptions key.** D1305: a workspace is not a licensed unit, so no key, seat or price work
  is in this plan.
- **A workspace-wide repository allow-list.** Refused by [spec.md](spec.md) §9 and F18.
- **Any live check, by number.** The owner-run checks are named in
  [reconciliation.md](reconciliation.md) §6 and numbered by whichever task writes them.
