# The hosted MCP server: implementation plan

**Nothing here is approved and nothing here may start.** The design is [spec.md](spec.md), the
findings and the fourteen decisions are [reconciliation.md](reconciliation.md). This plan is written
to **every recommendation**, so an owner who answers a decision the other way changes a task, not a
paragraph — §1 says which task each answer moves.

## 1. What the owner must answer before any part starts

| D | The question | Written to | If the owner answers the other way |
| --- | --- | --- | --- |
| D920 | Public endpoint, or P8's IAM invoker | Public, bearer token is the gate | **M5, M6, M8, M9, M10 are void, and M4 and M7 shrink.** An IAM-invoked endpoint has no metadata documents, no registration and no token endpoint; what survives is M1–M3 and a tool registry reached by a Google identity. The plan is then a different, much smaller plan. |
| D921 | The OAuth library | `zitadel/oidc` | **M5's shape changes** (the endpoints stay; who implements them does not), and `go.mod` gains a different dependency. |
| D922 | Where the token records live | A subtree nobody may read | **M3 changes** from "name and deny" to "grant the coordinator's account", and its red step inverts. |
| D923 | How the person signs in | The installation's own custom-token flow | **M6 changes**, and a decision already taken once (`docs/mobile-auth` D221) is reopened. |
| D924 | How the spend limit is enforced | A variable of its own, tighter-of-two | **M9 loses its core change** and gains the launcher-side arithmetic instead. |
| D925, D926 | Hotfix as a flag; the window's shape | Flag beside the window; an empty window refused at mint | **M1 changes**, and M9's refusal message changes. |
| D927 | The v1 scopes | `tasks:read`, `tasks:write` | **M7 and M10 gain a scope**, and the console's blocked write (F30) gains a route. |
| D928 | Who owns the redirect allowlist | The project's configuration | **M5 and M9 gain a configuration field** and the project-format version question that comes with it. |
| D929 | What an unattended caller is | A pre-registered confidential client | **M5 and M9 gain a key-exchange endpoint**, and the owner's own non-goal is reopened. |
| D930 | Lifetimes and rotation | Short access tokens, rotating refresh, one-delete revocation | **M2 and M10 change**; a long-lived-token design deletes most of M2. |
| D931 | How a run is started, and the coordinator's own tools | `internal/backend`, one new grant, both tool sets on one endpoint | **M9 changes** (the sweep instead of a direct launch), and the tool-naming decision moves into M7. |
| D932 | Where `stream_logs` reads | The run's own artefacts | **M8 and M4 change**: one more grant, able to read every log line in the project. |
| D933 | Who a person is | The project's launcher and operator lists | **M7 and M10 change**, and a permission model that does not exist yet has to be designed. |

## 2. The order, and what forces it

```
M1 ──▶ M2 ──▶ M5 ──▶ M7 ──▶ M8
        │      ▲      │
M3 ─────┘      │      └──▶ M9 ──▶ M10
               │
M4 ────────────┘        (M4 and M6 are the two that need a live installation)
M6 ────────────┘
```

Three dependencies force that shape and nothing else does:

1. **M4 before M5.** The endpoints have nowhere to run until the coordinator has a service and an
   account of its own. M4 is the only task that needs Terraform and the only one that cannot be
   proven hermetically beyond a plan test.
2. **M2 before M5.** The token endpoint's whole job is to mint a record M2 defines.
3. **M1 before everything that reads a limit**, because "only narrows" is the one property every
   other task assumes.

**M3 is independent** of all of them and can land first: it is a rules file and its tests, in a
package that has nothing to do with the coordinator.

## 3. The tasks

Every task is written to be mergeable on its own. **SEC** marks a task whose diff an independent
review must read before it merges, which the brief requires for the threat model as a whole and which
is asked here for every task that changes who may reach what.

### M1 — The limits vocabulary, and "only narrows"

- **Files (6):** `ee/coordinator/mcpauth/limits.go`, `limits_test.go`, `ee/coordinator/mcpauth/token.go`,
  `token_test.go`, `ee/coordinator/mcpauth/limits_table_test.go`, `ee/coordinator/mcpauth/doc.go`.
- **Interface:** `Limits{MinRigor, MaxRigor rigor.Mode; Hotfix bool; SpendUSD float64; Scopes []string}`
  and `func (l Limits) Narrow(project Limits) (Limits, error)`. `Narrow` takes the tighter side of
  every field and returns an error naming the field when the result admits nothing.
- **Red step:** `TestLimitsOnlyNarrow` — a token asking for `critical` under a project policy of
  `pro` resolves to `pro`, and a token whose spend exceeds the run's ceiling resolves to the
  ceiling. It fails to compile before `Narrow` exists, which is the red.
- **Tests:** `TestLimitsOnlyNarrow`, `TestHotfixIsNotOnTheAxis` (a `min: pro` token refuses Hotfix
  with the flag off, and a Hotfix token passes a `min: poc` policy — the `Rank()`-0 case of D925),
  `TestEmptyWindowIsRefusedAtMint`, `TestUnknownScopeIsRefusedNotIgnored`.
- **SEC:** no grant changes; reviewed with the threat model because it is the rule the whole folder
  rests on.
- **Depends on:** nothing.

### M2 — Minting and the store: hashes only

- **Files (6):** `ee/coordinator/mcpauth/store.go`, `store_test.go`, `mint.go`, `mint_test.go`,
  `ee/coordinator/mcpauth/fake_test.go`, `ee/coordinator/testdata/mcpauth/README.md`.
- **Interface:** `Store` over the project's database with `PutClient`, `GetClient`, `PutToken`,
  `LookupByHash`, `RevokeFamily`; `Mint(identity, client, limits) (access, refresh string, err error)`.
- **Red step:** `TestMintStoresOnlyTheHash` — every value written through the fake database is
  compared against the two strings `Mint` returned, and the test fails if any record contains either
  or any prefix of either. Written first, it fails because there is no `Mint`.
- **Tests:** `TestMintStoresOnlyTheHash`, `TestLookupFindsByThePresentedToken`,
  `TestRefreshRotatesAndReplayRevokesTheFamily`, `TestRevokedTokenIsNotFound`.
- **SEC:** yes — it is the task that decides what is at rest.
- **Depends on:** M1.

### M3 — The rules name the token records' home and grant it to nobody

- **Files (3):** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`,
  `internal/budget/rules/mcpauth_test.go`.
- **Interface:** a `mcpauth` subtree in the template with `.read` and `.write` both false, named in
  the file so that the next person who adds a grant can see it is there, plus the constant that the
  parity test reads it through.
- **Red step:** `TestMCPAuthIsNamedAndDeniedToEveryClient` — it asserts the subtree is **named** in
  the template and that the emulator refuses a read as `USER` and as a run's `TOKEN`. It fails
  before the template change because the key is absent. (The root's blanket denial already refuses
  such a read, which is exactly why the test has to assert the *name*: an unnamed subtree is
  protected by a rule nobody reading the file can see.)
- **Tests:** `TestMCPAuthIsNamedAndDeniedToEveryClient`, and `TestRulesParity` must stay green.
- **SEC:** yes — it is the rules change that stands between token records and every signed-in person
  of the project.
- **Depends on:** nothing. **Can land first.**
- **Do not touch:** `internal/budget/rules/rules.go` is shared with every other rules work; the
  change is one constant and one template key, and a conflict there is a rebase, not a redesign.

### M4 — The coordinator's public service, its own account and its grants

- **Files (7):** `deploy/terraform/gcp/modules/installation/coordinator.tf`,
  `deploy/terraform/gcp/modules/installation/outputs.tf`,
  `deploy/terraform/gcp/modules/installation/coordinator_service_test.go` (a plan-level test),
  `internal/infra/tf/cover.go` (only if a new type appears — F2 says one does not),
  `internal/infra/coordinator_service.go`, `internal/infra/coordinator_service_test.go`,
  `ee/coordinator/service.go`.
- **Interface:** a `google_cloud_run_v2_service` for the coordinator with `allUsers` invoker (D920),
  its own service account, no model credential mounted, and the launch role of M9 declared but not
  yet used.
- **Red step:** `TestCoordinatorServiceIsPublicAndCarriesNoCredential` — it fails against the current
  Terraform because there is no service, and it asserts the two things that matter (the invoker
  binding is `allUsers`, and the container carries no secret whose value is a model credential).
- **Tests:** `TestCoordinatorServiceIsPublicAndCarriesNoCredential`,
  `TestCoordinatorServiceHasItsOwnAccount`, and the existing `cover.go` tests must stay green.
- **SEC:** **yes, mandatory.** It creates a public endpoint and a new service account.
- **Depends on:** D920 answered.
- **Owner check:** the service is applied only by `fugaro update`, and this plan does not run it.

### M5 — The authorization server: its two metadata documents and its four endpoints

- **Files (9):** `ee/coordinator/mcpauth/server.go`, `server_test.go`, `metadata.go`,
  `metadata_test.go`, `register.go`, `register_test.go`, `authorize.go`, `token.go`,
  `authorize_test.go`.
- **Interface:** `/.well-known/oauth-protected-resource`, `/.well-known/oauth-authorization-server`,
  `POST /oauth/register`, `GET /oauth/authorize`, `POST /oauth/token`, `POST /oauth/revoke`, mounted
  on the service M4 creates.
- **Red step:** `TestNoTokenIsA401NamingTheMetadataURL` — the MCP endpoint answers 401 with a
  `WWW-Authenticate` header whose resource-metadata URL resolves, and the test fails because the
  endpoint does not exist.
- **Tests:** `TestNoTokenIsA401NamingTheMetadataURL`, `TestMetadataPointsAtItsOwnServer`,
  `TestRegisterIsOpenAndGrantsNothing`, `TestPKCEIsRequiredAndPlainIsRefused`,
  `TestCodeIsSingleUseAndBoundToTheRedirectURI`, `TestRedirectURIMustMatchExactly`,
  `TestRedirectURIMustBeLoopbackOrAllowlisted`, `TestRefreshTokenIsSingleUse`.
- **SEC:** **yes, mandatory** — this is the endpoint the threat model's redirect-URI and open-
  registration paragraphs are about.
- **Depends on:** M2, M4, D921, D928.

### M6 — Firebase at the token endpoint

- **Files (4):** `ee/coordinator/mcpauth/firebase.go`, `firebase_test.go`,
  `ee/coordinator/mcpauth/firebase_keys.go`, `ee/coordinator/testdata/mcpauth/jwks.json`.
- **Interface:** `VerifyIDToken(ctx, raw string) (Identity, error)`, sharing the verification rules
  `internal/credsvc` already implements (issuer built from the project id, audience compared to the
  bare project id, keys from the public endpoint) and never echoing a token in a refusal.
- **Red step:** `TestTokenFromAnotherProjectIsRefused` — a token whose audience is another project's
  id is refused, and the refusal names neither the token nor the audience. It fails because there is
  no verifier.
- **Tests:** `TestTokenFromAnotherProjectIsRefused`, `TestWrongIssuerIsRefused`,
  `TestExpiredTokenIsRefused`, `TestRefusalNeverEchoesTheToken`.
- **SEC:** **yes** — token verification is where a mistake is silent.
- **Depends on:** M4. **Reuses rather than duplicates** `internal/credsvc`'s verification; the review
  must ask why it is a second implementation, and the answer has to be that `internal/credsvc` is a
  different service's package with a different configuration surface — if the reviewer disagrees, the
  right fix is to extract the verifier, and that is a finding for the owner rather than a refactor in
  this task.

### M7 — The resource server and the tool registry

- **Files (7):** `ee/coordinator/mcpauth/resource.go`, `resource_test.go`,
  `ee/coordinator/mcpauth/tools.go`, `tools_test.go`, `ee/coordinator/mcpauth/scopes.go`,
  `scopes_test.go`, `ee/coordinator/mcpauth/identity.go`.
- **Interface:** one handler that reads the bearer token, validates it (issuer, audience, expiry,
  revocation, scopes), resolves the identity through the project's launcher and operator lists
  (D933), and dispatches JSON-RPC; one registry that answers `tools/list` with **both** the MCP
  tools and the coordinator's own (D931).
- **Red step:** `TestUnknownScopeIsRefusedNotIgnored` — a token carrying a scope the server does not
  know is refused rather than treated as holding no scopes. It fails because there is no registry.
- **Tests:** `TestUnknownScopeIsRefusedNotIgnored`, `TestIdentityOffTheListsIsRefused`,
  `TestToolsListNamesBothSetsOnce`, `TestAnExpiredTokenIsRefused`,
  `TestAToolAnswerCarriesNoCredential` (a table over every tool, asserting the answer's text
  contains none of the project's credential names).
- **SEC:** **yes, mandatory.**
- **Depends on:** M2, M5, D933.

### M8 — The read tools

- **Files (5):** `ee/coordinator/mcpauth/tool_status.go`, `tool_status_test.go`,
  `ee/coordinator/mcpauth/tool_logs.go`, `tool_logs_test.go`, `ee/coordinator/mcpauth/tool_list.go`.
- **Interface:** `get_task_status`, `stream_logs` (from the run's own recorded artefacts, D932),
  `get_result`, `list_tasks`, each reading through `internal/runstore` and the runs bucket prefix the
  project already owns.
- **Red step:** `TestListTasksNamesOnlyTheProjectsRuns` — a run belonging to another project is
  absent from the answer, and the test fails because the tool does not exist.
- **Tests:** `TestListTasksNamesOnlyTheProjectsRuns`, `TestStatusOfAnUnknownRunIsRefused`,
  `TestLogsComeFromTheRunsOwnArtefacts`, `TestResultNeverQuotesAToken`.
- **SEC:** yes — read reach.
- **Depends on:** M7.

### M9 — `submit_task`, `cancel_task`, and the narrowed ceiling

- **Files (8):** `ee/coordinator/mcpauth/tool_submit.go`, `tool_submit_test.go`,
  `ee/coordinator/mcpauth/tool_cancel.go`, `tool_cancel_test.go`,
  `ee/coordinator/mcpauth/launch.go`, `launch_test.go`, `internal/runner/spend.go`,
  `internal/runner/spend_test.go`.
- **Interface:** `submit_task` starts the project's own job through `internal/backend` with the
  token's spend limit in a variable of its own on the execution override (D924); the run's ceiling
  becomes the tighter of that and the job's own. `cancel_task` writes the marker `fugaro cancel`
  writes.
- **Red step:** `TestTokenCeilingOnlyNarrowsTheRunsOwn` in `internal/runner/spend_test.go` — a run
  whose environment carries a token ceiling **above** the job's own resolves to the job's own, and
  one below resolves to the token's. It fails before the variable exists.
- **Tests:** `TestTokenCeilingOnlyNarrowsTheRunsOwn`,
  `TestSubmitRefusesAModeOutsideTheTokensWindow`, `TestSubmitRefusesHotfixWithoutTheFlag`,
  `TestSubmittedRunCarriesTheTokenCeiling`, `TestSubmitNeverMountsACredential`,
  `TestCancelOfAnotherProjectsRunIsRefused`.
- **SEC:** **yes, mandatory.** It adds the launch grant, and it is the one task that spends money.
- **Depends on:** M4 (the grant), M7, D924.
- **Hand-off:** `internal/runner/spend.go` is a core file and the 0.15.0 window has other writers on
  the runner. This task belongs to a release where that is not true, and its planner must say so
  before it starts (§4).

### M10 — The audit record, revocation, and what an operator sees

- **Files (6):** `ee/coordinator/mcpauth/audit.go`, `audit_test.go`,
  `ee/coordinator/mcpauth/tool_revoke.go`, `tool_revoke_test.go`,
  `ee/coordinator/mcpauth/audit_record.go`, `audit_record_test.go`.
- **Interface:** one structured line per call and per refusal, naming the identity, the tool, the
  scopes and the outcome and never a value, on `internal/credsvc/audit.go`'s pattern; a bounded
  per-identity record an operator can read; and revocation reachable from the API and from a
  person's own surface.
- **Red step:** `TestEveryRefusalWritesOneLineNamingNoValue` — the audit line for a refusal whose
  token is a recognizable marker string contains neither the marker nor its first eight characters.
  It fails because there is no audit writer.
- **Tests:** `TestEveryRefusalWritesOneLineNamingNoValue`, `TestAuditRecordIsBounded`,
  `TestRevokedFamilyIsRefusedOnTheNextCall`, `TestOperatorSeesTheRecordAndALauncherDoesNot`.
- **SEC:** yes.
- **Depends on:** M5, M7.

## 4. The shared-files table

Files more than one task touches. A task that finds one of these already changed reads the change
before writing, and a planner who finds two tasks in the same row in one release serialises them.

| File | Tasks | What each does to it |
| --- | --- | --- |
| `ee/coordinator/mcpauth/limits.go` | M1, M9 | M1 defines `Limits` and `Narrow`; M9 reads them at submit |
| `ee/coordinator/mcpauth/token.go` | M1, M2 | M1 defines the token's shape; M2 mints it |
| `ee/coordinator/mcpauth/store.go` | M2, M5, M10 | M2 defines the store; M5 writes through it; M10 revokes through it |
| `ee/coordinator/mcpauth/server.go` | M5, M6, M7 | M5 mounts the five endpoints; M6 adds the token endpoint's verifier; M7 adds the resource handler |
| `ee/coordinator/mcpauth/tools.go` | M7, M8, M9, M10 | the registry: each of the three tool tasks registers into it |
| `ee/coordinator/service.go` | M4, M5, M7 | M4 creates the service's entry point; M5 and M7 mount handlers on it |
| `deploy/terraform/gcp/modules/installation/coordinator.tf` | M4, M9 | M4 creates the service and the account; M9 adds the launch grant |
| `deploy/terraform/gcp/modules/installation/outputs.tf` | M4, M9 | the service's URL, then the grant |
| `internal/runner/spend.go` | M9 alone, but **shared with every other 0.15.0 runner task** | one variable and its merge; see §5 |
| `SECURITY.md` | M4, M9, M10 | a public endpoint, a launch grant, and what the audit does not record — **the repository's standing rule is that a security-relevant change updates it in the same PR** |

## 5. The release slot

**The design lands with the 0.15.0 documentation window. The implementation targets 0.16.0,
"Moose".**

Why the design is 0.15.0 and not later: it is a document, it changes no behaviour, and the two works
it depends on — the coordinator's P7/P8 and the console's graph write — are blocked on it. Holding it
for a later minor holds them too.

Why the implementation is **0.16.0** and not 0.15.0:

1. **The coordinator's own front door is not built.** P7 and P8 wait on this design
   (`docs/coordinator/README.md`:26), so the service M4 creates and the account M5 runs on do not
   exist yet. A release cannot carry an MCP endpoint on a service the same release is still
   designing.
2. **0.15.0's window is already full.** Five designs and two planner runs are in it, and this
   folder's own M9 is a **core** change to `internal/runner/spend.go` — a file with a declared single
   writer in that window. Landing M9 in 0.15.0 means either that writer is overridden or the two
   changes are serialised by hand; neither is worth it for a feature whose first dependency is not
   built.
3. **The update has to carry it.** The repository's standing rule is that a release ships no
   operator step beyond `fugaro update`, and M4's service and M9's grant are both Terraform that the
   update applies. That is a migration with a convergence test — a test that an installation at the
   previous version converges — and it belongs in a release that is not already carrying the same
   work for three other features.

**What would move it earlier:** if the owner answers D920 the other way (P8's IAM invoker), M5's
OAuth half disappears, and M1–M3 plus a small M7 fit into whatever release is next.

## 6. Test rules for whoever implements this

- **Hermetic, always.** No live project, no real Firebase, no real Cloud Logging: `internal/gcpfake`,
  `internal/testutil`, `t.TempDir()`. The rules tests use the package's own emulator harness. The one
  thing this plan cannot prove hermetically is the public reachability of M4's service, which is
  [reconciliation.md](reconciliation.md) §6's first owner check.
- **Named tests, never whole packages.** `internal/cli` and `internal/runner` are never run whole;
  every command is `GOFLAGS=-p=2 go test ./<package>/ -run 'Name|Name' -count=1`.
- **A test that reads a changed file runs.** For every file a task touches, the implementing run
  greps the test tree for its base name and runs each hit — the wider pin gate this repository
  applies to every change.
- **No secret-shaped literal, in a test or a fixture.** A token in a test is a marker string that
  cannot be mistaken for a real one, and no fixture contains key material.

## 7. What this plan does not own

- **The coordinator's P7 and P8.** D196 already put the front door's home in the coordinator's own
  service; this plan builds the MCP half of it and nothing else. P7's ID-token minting, its per-person
  invoker grants and its IAM checks are the coordinator work's, and M4 must not silently take them
  over — where they overlap, the coordinator's plan wins and this one adapts.
- **The console's graph write.** D927 keeps `roadmap:write` out of v1; GV3.1 and GV3.2 stay blocked
  and that work comes back to ask.
- **The central-orchestrator plan.** D196 answered that the coordinator's service is its own; the
  orchestrator reaches it as a client like any other, and this plan adds nothing to it.
- **A local stdio MCP server.** The owner's non-goal, and this plan does not creep into it.
- **Any change to `SECURITY.md`, `README.md`, `website/**`, `plugin/**` or `.github/workflows/**`
  made by the run that writes *this* document** — those are other works' files in this window. The
  implementation tasks of §3 may touch `SECURITY.md` when they run, because by then the change is
  theirs.
