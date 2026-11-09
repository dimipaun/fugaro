# Workspaces: the implementation plan

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** The sixteen decisions in [reconciliation.md](reconciliation.md) §4 (**D1290–D1305**) are
**answered**, every one as recommended, so no task below is waiting on a decision any more. **Two of
them were amended on 2026-11-04, after the design merged and its security review ran** — D1299,
D1300 and D1301, marked below [reconciliation.md](reconciliation.md) §4 and §8 — and the tasks they
bind (W4–W7) carry the amended shape. **No task is approved**: the owner approves each task by name.
The one gate this file used to hold is open: [spec.md](spec.md) §8's threat model was reviewed
independently in the post-merge round — the coordinator runs that review, not this plan's author and
not the runs that implement anything — and the review's **seven findings and the owner's answers to
all of them are [reconciliation.md](reconciliation.md) §2f's F33**. What is left shut is the owner's
approval of each task.

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
| D1299 | W7 | Enforced at the service, advisory at the member's gateway — **amended 2026-11-04: the headroom comes from the service, and the service holds no write on a member** |
| D1300 | W7, W9 | The cap bounds the workspace's own credentials' spend — **amended 2026-11-04 only as it depends on D1301** |
| D1301 | W7 | A rollup from each member's ledger, `stale` rather than zero — **amended 2026-11-04: the cap is metered at the proxy and the rollup becomes a reconciliation alarm** |
| D1302 | W8 | `fugaro budget kill` gains a workspace scope |
| D1303 | W1, W8 | Local writes and reads; `update` carries the cloud |
| D1304 | — | No console or mobile task in this plan |
| D1305 | W9 | Not a new licensed unit and not a new price |

**One thing must be read before any task starts:** [spec.md](spec.md) §8 is a threat model, its
independent SECURITY review is **mandatory before this design merges** by the owner's brief, and
**the coordinator runs it** — it is not a task of this plan. **That review has run** on the merged
design and returned **seven findings**, all answered by the owner on 2026-11-04
([reconciliation.md](reconciliation.md) §2f's **F33**), so its findings are not open; W4, W5, W6 and
W7 each add a surface that section describes, and each is written to the amended answers in the rows
above. (The brief first put the review before the decisions; the owner answered all sixteen on
2026-11-04, and the review ran after the merge.)

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
    Name    string
    Members []Member
    Cap     Cap
}
type Member struct{ Project string; Repos []string }
type Cap struct{ Monthly pricing.Micros; Set bool }

func Parse(raw []byte) (*Record, error) // strict: an unknown field is an error
func (r *Record) Validate() error
```

**The record carries no identity provider and no service account** (spec.md §2). An earlier draft of
this task gave the record an identity-provider field, with a test refusing a second one beside it,
and that is the shape the security re-read of round 3 removed: the issuer a member's exchange trusts
and the account a member's grants bind to are the **member's pin**, in the member's own config (W1),
and a record that carried either would be a record that could aim every member's
exchange and every member's database grant at a party of its choosing — the one direction rule 1 does
not survive. What the record holds is what the workspace states about the group: the members and the
cap. The per-member values the workspace's own service needs to reach a member are not in it either
(spec §5's table, which is the workspace owner's config).

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
grammar), `TestRecordRefusesAnIdentityOrServiceAccountField` (the strict decode refuses an
`identity` block and a `service_account` key: the record carries neither, so a later writer cannot
add one back without this test failing first),
`TestRecordRefusesACapThatIsNotPositiveWhenSet`,
`TestCapIsExactMicrosAndNotAFloat` (a cap of `0.1` is exactly `100_000` µ$: the parsed field is
integer micro-dollars, and a `float64` field is the mistake this test exists to make impossible),
`TestRecordRoundTripsThroughTheSchema` (the `schemas/` file and the struct agree).

**SEC:** no. It is a document shape with no cloud and no credential.

### W1 — the local layer, and the `fugaro workspace` reads and writes

**Interface.** `localcfg.Config` gains the membership key and, beside it, the member's **pin**:

```go
type Config struct {
    // ... the fields it has today
    // Workspace is the membership, a scalar so a project is in at most one
    // workspace (D1291).
    Workspace string `yaml:"workspace,omitempty"`
    // WorkspacePin is the member's own record of which workspace it trusts and
    // which identity acts for it there. Nothing here is read from the workspace's
    // own record (spec.md §2): a record that could supply any of the four would
    // aim the member's exchange and the member's grant at a party of its choosing.
    WorkspacePin WorkspacePin `yaml:"workspace_pin,omitempty"`
}

type WorkspacePin struct {
    FirebaseProject string `yaml:"firebase_project"` // W4 requires this project as the token's aud
    Issuer          string `yaml:"issuer"`           // W4 requires exactly this issuer
    AssertionKey    string `yaml:"assertion_key"`    // W4 verifies a member-bound assertion against this
    ServiceAccount  string `yaml:"service_account"`  // W7 binds the member's grant to this account
}
```

**The fourth value is the assertion-signing key** (finding 1 of the post-merge security review). A
member does not accept the workspace's Firebase token at all: what it verifies is a **member-bound
assertion** the workspace's signer endpoint mints, and the key it verifies that against is **its own
pin** (spec.md §3). It is the member's value, like the other three: the workspace's record names no
key, so a workspace cannot move a member's trust by publishing a different one,
`TestWorkspaceAddWritesThePinTheMemberWasGiven` holds all four, and W4 is where the key is used.

**The issuer is checked against the project, not trusted on its own.** The signature is verified
against **Google's published Firebase signing keys**, which are one global set — `GoogleKeysURL`
(`internal/credsvc/auth.go`:1371-1373) names the one x509 document, and it is the same document for
every Firebase project, so "the keys the pinned project publishes" is not a thing that exists. What
distinguishes one project's tokens from another's is the pair `aud`/`iss`, and main derives the
second from the first: `"https://securetoken.google.com/"+a.cfg.FirebaseProject` (`:1072`). The pin
therefore carries both values and **`Validate` refuses a pair that disagrees** — the issuer must be
exactly `https://securetoken.google.com/<firebase_project>` — so a member that mistypes one of the
two is refused at the write and again at the load, rather than being admitted by one and refused by
the other for the rest of the installation's life.

The new `fugaro workspace` family (D1303) with `ls`, `show`, `add`, `rm`, `member add`, `member rm`,
`cap set` — every write a local config edit through the same check, diff, backup and atomic write
the `models` commands use, and every one ending with `next: fugaro update`. `workspace add` writes
the membership key **and the pin** — the four values it is given, which is the one place a member
decides what it trusts — firebase project, issuer, assertion key and service account — and
`workspace rm` removes both. `workspace member add` is the owner's side: it writes the member entry
**and the four values the service's per-member table is built from** (the member's **job service
accounts**, its **Firebase project id and `fp`**, and its **exchange URL**, spec.md §5), and
`member rm` removes the entry. **The member's database URL is not among them**: it is **derived from
the Firebase project the verified `iss` names** (finding 6 of the post-merge review), so an owner
cannot splice member B's accounts onto member A's database, and **the runs bucket is not among them
either** — O1's answer dropped the bucket grant, and nothing derives it. The two commands are the two
halves of a membership, and neither writes the other's file.

**`workspace show` reads files and no cloud node**, and the two halves it can therefore see are not
the same (spec.md §6). The **workspace owner's** machine holds the record it publishes, so its drift
report is W2's two-sided membership check in full. A **member's** machine holds no grant on the
workspace's project, so its `workspace show` reports the member's own half — the key and the pin it
carries — and says the record cannot be read from here rather than printing an empty member list.

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
`TestLocalcfgRefusesASecondWorkspaceKey` (D1291 is a scalar),
`TestWorkspaceAddWritesThePinTheMemberWasGiven` (spec §2: the project id, the issuer, **the assertion
key** and the service account are the member's own values, and no code path reads them from the
workspace's record — which is the field a later refactor would most plausibly "helpfully" fill in),
`TestMemberAddRefusesToDeclareADatabaseURL` (the member's database is derived from the verified `iss`
and is not a field the owner may set: a splice is a declaration the command will not write),
`TestMemberAddWritesNoRunsBucket` (O1 dropped the bucket grant, so the table carries no bucket),
`TestPinRefusesAnIssuerThatIsNotTheProjectsSecuretokenIssuer` (a pin naming `owner/repo-a`'s project
with another project's issuer, or with an issuer that is not that project's securetoken one, is
refused: the pair has to agree, because only the pair tells one project's tokens from another's).

**SEC:** no. Local file edits and offline reads.

### W2 — the workspace node, and `fugaro update`'s publish stage

**Interface.** The **workspace owner's** `fugaro update` gains the stage that writes
`workspaces/<name>` in the workspace's own database from the workspace's local record, behind its own
confirmation, migrating an older node in place (never "delete and publish again"), on the policy
stage's pattern. **A member's `fugaro update` writes nothing there and reads nothing there** — a
member holds no grant on the workspace's project (§5 of [spec.md](spec.md)) — so the stage is the
owner's, and a member's run of the same command converges only the member's own installation. **The
node's readers are the workspace's own service and the workspace owner's CLI, and no member is one of
them**: a member's exchange verifies the pin in its own config and reads its own lists (W4, W5), and
it never looks at the record.

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
- **Each member pins what it trusts, in its own config.** A member's config records the workspace's
  Firebase project id, the issuer its exchange requires, and the workspace service account its own
  `fugaro update` binds §5's grants to (W1's interface). Its exchange requires **that** project as the
  token's audience and **that** issuer, and verifies the signature against Google's published keys —
  never values the workspace's own record supplies, which is why the record carries neither. A record
  that could name an issuer or a service account would
  be a record that could point every member's exchange, and aim every member's database grant, at a
  party of its choosing, which is the one direction rule 1 does not survive.
- **`workspace show` reports the halves each side can see.** The owner's machine holds the record it
  publishes, so its drift report is the two-sided check above in full. A member's machine holds no
  grant on the workspace's project, so its report is its own half — the key and the pin — plus the
  plain statement that the record cannot be read from here (spec.md §6). A half-done membership is
  named rather than papered over, and neither side ever invents the other's half.

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
`TestUpdateSkipsAPublishWithNoMembers` (a record that lists no member is a workspace that admits
nobody, so nothing is published — and the stage is SKIPPED, never an error: a cap-only config is one
`fugaro workspace cap set` writes and `fugaro workspace member rm` leaves behind, and a Check that
failed on it would stop the run with every stage after it blocked, which is the HARD RULE's one
command failing on a config the release reads),
`TestUpdateWithACapAndNoMemberRunsOn` (the same state through the whole command),
`TestUpdateNamesTheOneCommandAndNoOther` (no `workspace publish` anywhere in the output),
`TestUpdateLeavesAMemberWithNoWorkspaceAlone`,
`TestUpdateOnAMemberNeverWritesTheWorkspaceNode` (the member holds no grant; a stage that tried would
be refused by IAM, and a task that reached for it is the defect this test names),
`TestMembershipNeedsBothSides` (listed but not naming the workspace, and naming it but not listed,
are both not membership and both are reported as drift).

**SEC:** **yes.** It is the write that decides who is in the group — the record the workspace's own
service reads for the member list it rolls up and admits against — on a project the publishing
machine does not own. A publish stage that admitted a member on one side's word, or that let the
record name the issuer or the service account a member binds its grants
to, would be the workspace widening a member — the one thing rule 1 forbids. **No member reads this
node** (spec.md §2): a member's exchange reads its own pin and its own lists, and a member's machine
holds no grant on the workspace's project at all.

### W3 — the workspace's own Firebase project and its rules

**Interface.** The workspace's Firebase project is created and converged by the installation path
(`internal/infra` over the roots in `deploy/terraform/gcp`), and the workspace's database rules ship
with it: `workspaces/<name>` is **published by the workspace owner's `fugaro update`** (W2) and
**read by the workspace's own service**, and the rules keep it — and the token subtree with it — shut
to every other client. **No member's client reads the workspace's database** (spec.md §2): a member
holds no grant on the workspace's project at all (§5).

**Creating the project is `fugaro setup` *and* an owner-run step, and the owner settled this on
2026-11-04.** The project itself and its rules come from the installation path — the owner's
`fugaro setup` for the workspace root, then `fugaro update` to converge it — but **the workspace's
Firebase must also carry real sign-in providers**, and `fugaro setup`'s own Identity Platform step
initializes with **no provider** (`IdentityPlatformStep`,
`internal/infra/identityplatform.go`:32) because that is right for a member's project and wrong for
the workspace's. So the provider enablement is a step **the owner runs** (in the Firebase console or
with `gcloud`), it is listed in the release note's `### For operators` section (W9), and it is the
check `docs/gcp-live-checklist.md` gains rather than something a hermetic test can hold. A workspace
whose project is initialized with no provider has no one who can sign in, and **the pinned provider
set of spec.md §3 is what the step has to produce**: exactly the providers the pin admits.

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

**Interface.** The **workspace's signer endpoint** mints the **member-bound assertion**, and a
member's authorization server (the MCP design's §3) accepts it **at `/oauth/authorize`, as the
sign-in proof** — not as a grant at `/oauth/token` (finding 2 of the post-merge security review), and
with **no second endpoint of this design's own**. The signer endpoint verifies the workspace's
Firebase ID token against **Google's published Firebase signing keys** (`internal/credsvc/auth.go`:1371-1373
— one global set, which is why the pin's project tells tokens apart by its `aud`/`iss` pair and not by
any per-project keys), against the workspace's own project as `aud` and its securetoken issuer as
`iss`, against `email_verified`, and against the **pinned `firebase.sign_in_provider` set**; and it
signs a short-lived JWT whose `aud` is **that member's exchange URL** and whose `jti` is single-use
(spec.md §3). The member verifies that JWT against **the assertion key its own pin carries** (W1), its
own exchange URL, its expiry and the `jti` values it has consumed, and it re-checks the provider and
the verified address itself. **Every one of those is the member's pin and never a value the
workspace's record supplies** (spec.md §2): the record names no key, no issuer and no project (W2
publishes none), so a "helpful" refactor that reached for the record would have nothing to read. The
member keeps the MCP design's PKCE (S256), consent page and paired-device approval (its D923;
`docs/mobile-auth`'s D221); **it depends on the MCP design's implementation (its M3)**.

**Files (8):** `ee/coordinator/oauth/workspace.go`, `ee/coordinator/oauth/workspace_test.go`,
`ee/coordinator/oauth/workspace_assertion.go`, `ee/coordinator/oauth/workspace_assertion_test.go`,
`ee/coordinator/oauth/workspace_signer.go`, `ee/coordinator/oauth/workspace_signer_test.go`,
`internal/cli/workspace_login.go`, `internal/cli/workspace_login_test.go`.

**Red step.** `TestExchangeRefusesAWorkspaceAssertionMintedForAnotherMember` fails — the assertion
does not exist. It is the test the task exists for: the whole security claim of the SSO half is that
what a member accepts is bound to **that one member and to one use**, so a verifier that checked the
signature without the `aud` and the `jti` would honour an assertion minted for another member, or the
same one twice.

**Tests.** `TestExchangeRefusesAWorkspaceAssertionMintedForAnotherMember` (the `aud` is another
member's exchange URL — finding 1),
`TestExchangeRefusesAReplayedAssertion` (a `jti` already consumed at this member is refused, for the
assertion's lifetime — finding 1's single-use rule),
`TestExchangeRefusesAnAssertionReplayedAtAnotherMember` (the same assertion presented at a **second**
member is refused there, because each member's consumed-`jti` set is its own and the `aud` names the
first — the replay that a per-member set alone would not catch),
`TestExchangeRefusesAnExpiredAssertion`, `TestExchangeNeverEchoesTheAssertion` (the `credsvc` rule,
applied to the new endpoint),
`TestExchangeVerifiesTheMembersPinnedKeyNotTheRecords` (an assertion signed by another workspace's
key — one the member's pin does not carry — is refused, and no code path reads the record),
`TestExchangeVerifiesTheMembersPinnedIssuerNotTheRecords` (the signer endpoint refuses a Firebase
token whose `iss` is not the workspace's pinned issuer, and the member refuses an assertion naming a
project other than its pin),
`TestExchangeRefusesWhenTheMemberPinsNoWorkspace` (the member's own `workspace rm` has dropped the
key and the pin: there is no key, no project and no issuer left to verify against, so the exchange
refuses whatever the workspace's record still says about it — the half of a removal that only the
member can complete, spec §3),
`TestExchangeRefusalIsIndistinguishableForAnUnknownMember` (spec §8: no member enumeration),
`TestExchangeAcceptsAProviderThatIsPinnedAndRefusesOneThatIsNot` (finding 7: `google.com` and
`password` are accepted, `custom` and `anonymous` are refused — the claim is re-checked in the
assertion and not taken from the signer's word),
`TestExchangeMintsAMemberTokenWithTheMembersOwnLimits`,
`TestExchangeStoresOnlyHashes` (the consumed-`jti` set is hashes, in a subtree no client may read).

**SEC:** **yes.** It is the identity surface, and [spec.md](spec.md) §8 is its review brief.

### W5 — the member's admission, from the member's own lists

**Interface.** The exchange admits the person only when **three** things hold: the assertion's
`email_verified` is true, **the provider the assertion names is one of the pinned set** (finding 7 of
the post-merge review — `google.com` or `password`, and never `custom` or `anonymous`), and the
member's launchers/operators lists (`fugaro user`) name their address, compared with
`mobileauth.NormalizeEmail`'s one rule. It reads the lists and never writes them. The verified-address half is the credentials service's own rule for every class of caller
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
rule), `TestAdmitRefusesADisplayNameForm` (a name is not an admission),
`TestAdmitRefusesACustomTokenProvider` (finding 7: a person who signed in with a **custom token** —
which anyone holding `signBlob` or `TokenCreator` on any service account of the workspace's project
can mint, with `email` and `email_verified` set to whatever they like — is refused at the member even
if the workspace's signer let one through, because the member re-checks the provider in the assertion
rather than trusting the signer),
`TestAdmitRefusesAnAnonymousProvider` (the anonymous provider proves no address at all),
`TestExchangeRefusesAMemberThatCannotReachTheWorkspacesFirebase` (a member whose machine cannot reach
the workspace's Firebase — no route to the signer endpoint — is **refused**, rather than being
admitted on a cached assertion or a stale local value; the refusal is the member's and it fails
closed).

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

**The account table is the workspace's own per-member table, and the owner's config supplies it.**
Each entry carries the member's job service accounts, its Firebase project id and `fp`, and its
**exchange URL** — and **not its database URL**: that is **derived from the Firebase project the
verified `iss` names** (finding 6 of the post-merge security review), so an owner cannot declare
member B's accounts against member A's looser database. There is **no runs bucket** in the entry
either (O1's answer dropped the bucket grant). Every value that *is* there is the **workspace owner's**
declaration, converged by the workspace owner's `fugaro update` (W2's stage writes the record; this
task's service and W3's root of the workspace's own project carry the table) — never read from a
member's machine and never taken from `workspaces/<name>`, which names the members and carries the cap
and nothing else (spec.md §2, §5). A member the table does not bind is refused at this front door, and
one the comparison cannot read is reported as an alarm by W7's reconciliation: an unbound member is an
unreadable one.

**A member's policy and its outcomes are read per member, from the database its own verified `iss`
names.** `checkLive` (`internal/credsvc/auth.go`:1196) and the outcomes reader it uses
(`RTDBOutcomes`, `internal/credsvc/auth.go`:217) take the database to read from the call rather than
from a field the owner set: the member the call resolved to decides it, so two members' accounts can
never be pointed at one database. The credentials service's own guard that a database URL is
`<Firebase project>-default-rtdb` (`internal/cli/credentials_serve.go`:494) is the shape the derived
URL keeps.

**Whose people the person class holds, and what each route admits.** The deployment's person class is
not the members': it is the **workspace owner's** people, written by the workspace's own `fugaro
update` from the workspace's own config (spec.md §4). Every route names the classes it admits rather
than the deployment carrying one person list a new route inherits an admission from, and the
refusals are tests rather than a convention: the workspace-wide routes — the member list, the cap,
the rollup, which is every member's spend at once — admit no person but the owner's, and the routes
that answer about **one** member admit that member's job service account and **no member's person**,
because one member's people must not be handed every other member's spend by joining a group. That
is what makes the class checkable at all: the list that names a member's people is that member's own
launcher/operator file, which this service holds no grant to read (§5), so an admission it cannot
check is an admission it does not make.

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
`TestWorkspaceServiceReadsAMembersPolicyFromTheDatabaseItsIssNames` (finding 6: the database is
derived from the verified `iss`, not from anything the owner declared, so an entry whose declared
value would point at another member's database is not a thing the table can carry),
`TestWorkspaceServiceRefusesASplicedMemberAccounts` (the splice test: member B's accounts paired with
member A's database — the shape the owner's declaration used to allow — never reads A's policy or
outcomes, because the database comes from B's `iss`),
`TestCredsServiceLiveCheckIsPerMember` (two callers resolving to two members read two databases: the
outcomes reader is chosen by the member the call resolved to and never by a shared field),
`TestWorkspaceServiceRefusesAMembersPersonTheWorkspaceWideRead` (one member's person asks for the
workspace's own figures and is refused by the route's class list, and is refused the same way on the
route that answers about that member: the class holds the workspace owner's people and no member's
person, which is what stops one member's people seeing every other member's spend),
`TestWorkspaceServiceNeverReturnsACredentialValue` (the package's rule 2),
`TestWorkspaceBundleIsOneSecretWithOneGrant` (the Q6 shape, applied to the workspace).

**SEC:** **yes.** It holds and proxies model credentials.

### W7 — the workspace cap: the meter, the alarm and the enforcement

**Interface.** The workspace service **meters each call it pays for**: it **reserves** before the call
and **settles** on the response's usage, **atomically in the workspace's own database** — one
transaction that takes the room and one that records what the call cost (D1301, **amended 2026-11-04
after the post-merge security review**, and [reconciliation.md](reconciliation.md) §8's **O2**). It
delivers each member's advisory headroom **as a response header on the calls it already proxies**, or
from a small route answering only the calling member's job service account — **never by writing a node
into a member's database** (O1). The **rollup becomes a reconciliation alarm**: on the history job's
schedule the service compares **its own count** against the **member's ledger**, per member, and a
disagreement beyond a stated bound is **reported**. The member's gateway reads the headroom and stops
a run early; it never decides the cap.

**The basis is the meter, and the ledger keeps its own cap.** The service's reservation-then-settle
transaction is what the workspace's cap is measured against, so the figure is taken where the money is
spent rather than reconstructed afterwards — the old rollup basis read the *last good* rollup and
could be passed in the gap between two of them (finding 4), and its cross-check compared a member's
ledger with **its own** runs bucket, which the member's operator controls (finding 5). The member's
ledger stays **authoritative for the member's own caps** — two different quantities, each governing
its own cap, and neither renamed into the other. The **overshoot bound is stated as a number**: the cap
can be passed by at most **the reservation in flight**. And **a failed settle refuses**: a call whose
usage cannot be written back is refused rather than served, because a call whose cost the service
cannot record is spending against a cap it cannot see.

**The grant it needs is one read permission, and the write is gone.** Enforcement, the meter and the
alarm need exactly `firebasedatabase.instances.get` on each member — the read half of the credentials
service's own custom role (`deploy/terraform/gcp/modules/installation/credentials.tf`:242-246) — so
this task binds **a role holding that one permission and no write**, and **`instances.update` is not
bound at all** (O1's answer). There is no narrower read than the whole database: Realtime Database has
no path-level scope, `get` reads **any** node of it (`credentials.tf`:201-210), a condition cannot
fence a path (`credentials.tf`:251-253), and IAM is what the service account is subject to rather than
the rules (`deploy/terraform/gcp/modules/firebase/iam.tf`:52). So the grant is a whole-database
**read** on every member, and rule 1's write direction is now a property of **IAM** rather than of the
service's code, because there is no write permission to enforce against. The service's role never
grows to `firebasedatabase.admin` either (`credentials.tf`:215-219).

**The bucket read is gone too.** The old rollup's second read was `roles/storage.objectViewer` on each
member's runs bucket, and the review found that check defeated (a member's operator controls its
ledger and its bucket alike, finding 5). With the basis metered at the proxy, the comparison that
matters is the service's own count against the member's ledger, which needs no bucket, so **no bucket
grant is bound** and the service holds no read of a member's run artifacts or logs at all.

**Who applies it, and to which identity — this task owns that half too.** The grant is applied by the
**member's own `fugaro update`**, on the member's own installation root, and bound to the workspace
service account **named in the member's own config**, beside the issuer, the project and the assertion
key it pins (spec.md §2, §5; W1's interface). It is **one binding**, on the member's database, and the
account is never read from the workspace's record — §2's record names no service account, for the same
reason it names no issuer — so a member decides which identity is handed read access to its own
database. The Terraform half belongs to the **member's** root rather than the workspace's, which is why
it is this task's and not W3's: W3 builds the workspace's project, and this is a binding on a member's.

**A managed project's plan gate refuses this binding, and that is the correct answer.** The binding
grants a role to a principal that is not this installation's own — the workspace's service account,
which is a Fugaro account of *another* project — so it is a **loosening** of the member's IAM, and
the cover never admits one: `internal/infra/tf/cover.go`:860-867 says in as many words that it "never
allows: iam bindings, iam policies, audit configs, service account keys …", and the managed work puts
IAM widening in the set the gate **never** covers (`docs/managed-agent-converge/spec.md` §3.2) and
says the gate can only narrow (§2, D1026). So the stage does not run under the
plan gate's admission: it falls back to its own typed confirmation, which a coding agent's session
cannot give, and the member's owner runs it at their own terminal — once per member, at the moment
they join the workspace. That is the design and not a limitation: an IAM binding that widened a
project's access on the strength of a clean plan is the one thing the gate exists to prevent, and
this task's binding is exactly that shape.

**Files (11):** `internal/credsvc/workspace_cap.go`, `internal/credsvc/workspace_cap_test.go`,
`internal/budget/workspace.go`, `internal/budget/workspace_test.go`,
`internal/budget/workspace_reconcile.go`, `internal/budget/workspace_reconcile_test.go`,
`internal/gateway/workspace_headroom.go`, `internal/gateway/workspace_headroom_test.go`,
`deploy/terraform/gcp/modules/firebase/workspace_access.tf`,
`internal/infra/tf/workspace_access.go`, `internal/infra/tf/workspace_access_test.go`.

**Red step.** `TestWorkspaceCapReservesBeforeTheCallAndSettlesOnItsUsage` fails. It is the test the
task exists for: with the basis at the proxy, a call served without a reservation taken first is a
call that can pass the cap, and the reserve/settle pair is the whole of what makes the workspace's
figure true.

**Tests.** `TestWorkspaceCapReservesBeforeTheCallAndSettlesOnItsUsage`,
`TestWorkspaceCapReservationIsAtomicWithTheWorkspaceDatabase` (the reserve and the settle are each one
transaction in the workspace's own database, so two calls at the cap boundary cannot both take the
last of the room),
`TestFailedSettleRefusesTheCall` (a usage that cannot be written back refuses the call rather than
serving it: a call whose cost the service cannot record spends against a cap it cannot see),
`TestWorkspaceCapOvershootIsBoundedByTheReservationInFlight` (the bound is the reservation, stated as
a number a test can hold rather than as an adjective),
`TestReconciliationAlarmReportsAMemberWhoseLedgerDisagrees` (the service's own count against
the member's ledger, beyond the stated bound: a disagreement is reported, never silently trusted in
either direction),
`TestReconciliationAlarmReportsAnUnboundMember` (a member the per-member table does not bind — a job
account, a Firebase project or an **exchange URL** missing — is reported as an alarm rather than
counted as zero; **there is no database URL or bucket in the entry to be missing**, because the
database is derived from the verified `iss` and the bucket is not bound),
`TestMemberThatCannotBeResolvedIsRefusedAtTheFrontDoor` (an unbound member cannot be resolved as a
caller at all, so its shared-credential calls are refused rather than served and counted),
`TestWorkspaceCapRefusesTheCallThatWouldExceedIt`,
`TestWorkspaceCapIsNotWidenedByAMembersOwnCap` (the merge is min, and the workspace can only
tighten), `TestHeadroomIsAdvisoryAndTheGatewayNeverDecidesTheCap`,
`TestHeadroomIsDeliveredByTheServiceAndWrittenNowhere` (the advisory number arrives on the service's
own response to the member — a header, or a route answering only that member's job service account —
and **no node of the member's database carries it**: O1's answer, and the property that lets the
service hold no write),
`TestWorkspaceCapBoundsOnlyTheCredentialsItPaysFor` (D1300, stated as a test so the docs cannot drift
from it),
`TestMemberUpdateBindsTheWorkspaceGrantToThePinnedAccount` (the member's own `fugaro update` creates
**one** binding — the read-only database role — and binds it to the service account its config pins; a
config whose pin names no account creates none, a record naming a different account changes nothing
because the record is not an input here, and **no bucket binding is created**),
`TestMemberUpdateBindsNoWriteOnTheMembersDatabase` (the role it binds holds
`firebasedatabase.instances.get` and **not** `.update`: rule 1's write direction is IAM's, and a
later change that added the write back would fail this test first).

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
| `internal/infra/tf/*.go` | W3, W7 | W3 owns `workspace.go` (the workspace's own root); W7 owns `workspace_access.go` (the member's bindings). Two files, no overlap |
| `schemas/*.json` | W0, W1, W4 | One file each (`workspace`, `workspace-list`, `workspace-login`); the schema directory's own test checks every file is referenced by a decoder |
| `ee/coordinator/oauth/*.go` | W4, W5 | W4 owns `workspace*.go`, W5 `admit.go`; W5 depends on W4's verified-identity type and takes it as given |

## 5. The release slot

**Recommended: 0.21.0 or later, after the MCP server.** The owner set the floor on 2026-11-04, after
the security review: the workspace's SSO half cannot ship before the **MCP server's** own code does,
because W4's exchange *is* that design's authorization server, so the slot is **one release behind
whichever release carries the MCP server's implementation** — and 0.21.0 is the earliest number that
can be that. Re-slotted on 2026-11-04: the slot this section used to recommend
— design in 0.15.0, code in 0.17.0 — has **shipped** (0.15.0, 0.16.0 and 0.17.0 are all released
files in `docs/releases/`, and 0.17.0 is still the newest CLI release on `origin/main` at
`c2ca0aee`), so
the recommendation is remade rather than kept. The design is still only documentation and costs a
release nothing; the code does not belong in the next release either, for the reasons below.

**Why the design can ride any release.** This folder is documentation, the owner asked for a design
and approved one on 2026-10-31, and it changes no behaviour.

**Why the code is 0.21.0 or later, and not the next release:**

1. **It is big.** Ten tasks, three of them new deployables (a Firebase project of its own, a second
   credentials service, an exchange endpoint) and two new wire formats with schemas. Nothing on the
   plan is a flag on something that exists; W6 is a *second* deployment of the credentials service
   and W7 is a second spend ceiling beside the project's.
2. **It is security-heavy.** Five of the ten tasks carry the plan's own **SEC** flag (W3, W4, W5,
   W6 and W7: an identity boundary, an authorization decision, a credential pool and a spend
   ceiling). [spec.md](spec.md) §8's threat model needed an independent SECURITY review **before this
   design merged**, run by the coordinator — **that review has run, and its seven findings are
   answered** ([reconciliation.md](reconciliation.md) §2f's F33), which is what let the tasks be
   written to the amended shape above. Shipping an identity boundary, a cross-project read and a
   shared credential pool in the release immediately after the review's answers would still put a
   security feature on the release's schedule rather than the review's.
3. **The exchange has no host until the MCP server exists, and this is the dependency that sets the
   floor.** W4 is written to the MCP design's authorization server. That design is **on `main`**
   (merged as #1487 on 2026-11-04) but **none of its code is written** — it is a design folder, and it
   recommends a slot of its own for its code — so a workspace cannot mint a member token before there
   is a server that mints member tokens. The owner's 2026-11-04 answer is that the workspace's code
   therefore lands in **0.21.0 or later, after the MCP server**, and not before it.
4. **The shared pool has no host until the credentials service's later slice.** The service's Slice 1
   is still its first product slice and the rest are unscheduled
   (`docs/credentials-service/implementation-plan.md`), and W6 is a *second* deployment of it, not a
   flag on the first.
5. **The workspace's services are `ee/`.** Rulings A5 and A6 (`docs/ee-reorg/spec.md`:7) put the
   coordinator and the credentials service's hosted half under the business folder, and the
   workspace's exchange endpoint and its service deployment are those. A release that shipped them
   in the MIT core would contradict a standing ruling, so the slot has to be one where the `ee/`
   placement is settled.

**Why not later than the 0.21.0 window:** nothing else the workspace needs is unbuilt by then. The
`update` stage (W2), the kill scope (W8) and the docs (W9) are ordinary work on surfaces that exist
today, and the SSO half's only unbuilt dependency is the MCP server itself, which is what the window
is timed to. W3's owner-run step — the workspace's Firebase must carry the pinned sign-in providers,
and `fugaro setup`'s Identity Platform step initializes with none — is an operator step, not
unbuilt work, and W9 carries it in the release note's `### For operators` line.

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
