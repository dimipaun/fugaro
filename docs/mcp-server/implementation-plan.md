# The hosted MCP server: implementation plan

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** The design is [spec.md](spec.md), the findings and the fifteen decisions are
[reconciliation.md](reconciliation.md). **All fifteen were answered on 2026-11-04** — fourteen as
recommended and **D922 not the recommendation** — and this plan is written to those answers: the
"Answered" column below is what the tasks build, and the last column is what a later answer the other
way would move. **No task here is approved, and two things gate the whole plan:** the
independent SECURITY review of [spec.md](spec.md) §8 (which the coordinator runs), and D921's
**unconfirmed licence** ([reconciliation.md](reconciliation.md) §6, checks 1 and 7).

## 1. What the owner answered on 2026-11-04, and what each answer moves

| D | The question | Answered 2026-11-04, and written to | If the owner later answers the other way |
| --- | --- | --- | --- |
| D920 | Public endpoint, or P8's IAM invoker | **Public**, bearer token is the gate — **the recommendation** | **M5, M6, M8, M9, M10 are void, and M4 and M7 shrink.** An IAM-invoked endpoint has no metadata documents, no registration and no token endpoint; what survives is M1–M3 and a tool registry reached by a Google identity. The plan is then a different, much smaller plan. |
| D921 | The OAuth library | **`zitadel/oidc` — the recommendation, with the licence UNCONFIRMED**, so the owner or counsel confirms it before any build: **M5 does not start until that is settled** | **M5's shape changes** (the endpoints stay; who implements them does not), and `go.mod` gains a different dependency. |
| D922 | Where the token records live | **A separate subtree, readable by any signed-in user of the project — NOT the recommendation**, which refused it to everything but the coordinator's service account | **M3 changes** from "named, readable by a signed-in person, writable by nobody else" to "named and denied to every client, granted to the coordinator's account", and its red step inverts again. |
| D923 | How the person signs in | **The installation's own custom-token flow** — the recommendation | **M6 changes**, and a decision already taken once (`docs/mobile-auth` D221) is reopened. |
| D924 | How the spend limit is enforced | **A variable of its own, tighter-of-two** — the recommendation | **M9 loses its core change** and gains the launcher-side arithmetic instead. |
| D925, D926 | Hotfix as a flag; the window's shape | **Flag beside the window; an empty window refused at mint** — the recommendation | **M1 changes**, and M9's refusal message changes. |
| D927 | The v1 scopes | **`tasks:read`, `tasks:write` only** — the recommendation | **M7 and M10 gain a scope**, and the console's blocked write (F30) gains a route. |
| D928 | Who owns the redirect allowlist | **The project's configuration** — the recommendation | **M5 and M9 gain a configuration field** and the project-format version question that comes with it. |
| D929 | What an unattended caller is | **A pre-registered confidential client** — the recommendation | **M5 and M9 gain a key-exchange endpoint**, and the owner's own non-goal is reopened. |
| D930 | Lifetimes and rotation | **Short-lived access tokens, rotating refresh, one-delete revocation** — the recommendation | **M2 and M10 change**; a long-lived-token design deletes most of M2. |
| D931 | How a run is started, and the coordinator's own tools | **`internal/backend`, one new grant, both tool sets on one endpoint** — the recommendation | **M9 changes** (the sweep instead of a direct launch), and the tool-naming decision moves into M7. |
| D932 | Where `stream_logs` reads | **The run's own artefacts** — the recommendation | **M8 and M4 change**: one more grant, able to read every log line in the project. |
| D933 | Who a person is | **The project's launcher and operator lists** — the recommendation | **M7 and M10 change**, and a permission model that does not exist yet has to be designed. |
| D934 | Whether the front door is on by default | **OFF until the owner turns it on** — the recommendation | **M4 gains the switch** and the live checklist gains a state; the other reading puts a public write endpoint on installations that asked only for a read-only view. |

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

**Two gates stand before the first line of any task.** The **independent SECURITY review of
[spec.md](spec.md) §8** — **run by the coordinator**, per the owner's brief — comes first: no task
starts until it has been read and its findings answered, and its scope is §8, every **SEC**-marked
task below (the five whose marker says **mandatory** are M3, M4, M5, M7 and M9) and the one new
privilege this design asks for (M9's launch grant). **D921's unconfirmed licence** is the second
([reconciliation.md](reconciliation.md) §6, the first owner check): it gates **M5** specifically,
because M5 is the task that takes the dependency, and a licence that turns out to be unusable sends
D921 back to its alternatives before any code exists rather than after.

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

### M3 — The rules name the token records' home, open it to a person and close it to every writer

- **Files (3):** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`,
  `internal/budget/rules/mcpauth_test.go`.
- **Interface:** a top-level `mcpauth` subtree in the template — **a sibling of `coordinator/`, not a
  child of it** (D922's answer) — with `.read: "USER"` and `.write: false`, named in the file so that
  the next person who adds a grant can see it is there, plus the constant the parity test reads it
  through. `USER` is the claim the coordinator's own subtree already uses
  (`internal/budget/rules/rules.json.tmpl:10-13`, `internal/budget/rules/rules.go:517`).
- **Red step:** `TestMCPAuthIsNamedAndReadableByAPersonAndWritableByNobody` — it asserts the subtree
  is **named** in the template, that the emulator **admits** a read as `USER`, and that it refuses a
  read as a run's `TOKEN` and refuses a **write** as `USER` and as `TOKEN`. It fails before the
  template change because the key is absent. (The root's blanket denial already refuses the `TOKEN`
  read, which is exactly why the test has to assert the *name*: an unnamed subtree is protected by a
  rule nobody reading the file can see — and D922's answer is why the test asserts the `USER`
  **admission** as well, since the read rule is now a fact the design depends on rather than a
  refusal.)
- **Tests:** `TestMCPAuthIsNamedAndReadableByAPersonAndWritableByNobody`, and `TestRulesParity` must
  stay green.
- **SEC:** **yes, mandatory** — it is the rules change that decides who reads the token records, the
  client list, the revocation state and the whole tool-call audit. **D922's answer widened that
  readership to every signed-in person of the project**, so the review reads this task beside
  [spec.md](spec.md) §8's token-storage paragraph, which states the exposure the answer accepts.
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
- **Depends on:** D920 answered on 2026-11-04 (**it was, the recommendation**), and the SECURITY
  review of [spec.md](spec.md) §8 read.
- **Owner check:** the service is applied only by `fugaro update`, and this plan does not run it.

#### For operators

The front door is **OFF on every installation until the owner turns it on**, and turning it on is
`fugaro update` — not a second command, because the repository's rule is that one command is the
whole upgrade. The flag is update's own, and the release notes' `### For operators` section carries
the one line that says so, in the release that ships M4:

```sh
fugaro update --firebase-project <firebase-project-id> --mcp-server
```

That run creates, in the installation's GCP project:

- the coordinator's Cloud Run **service** — the resident service D196 decided and the coordinator's
  own P7 builds, which is not the aggregation **job** P5 adds;
- its own service account, and the public invoker binding D920 asks for;
- the launch grant on the project's own job (M9), the one new privilege this design asks for;
- the front door's own configuration: the redirect allowlist of D928 and the switch of D934.

An installation that opted into the coordinator for its read-only view acquires **none** of it: the
aggregation job and the front door are separate opt-ins, and a public write endpoint is not something
an upgrade should hand a project that asked for a dashboard. M4 carries the convergence test the
repository requires of a migration: an installation at the previous version, updated with the flag,
ends with exactly the resources above — and an installation that did not pass it ends with none.

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
- **Depends on:** M2, M4, D921, D928 — **and on D921's licence being confirmed**, by the owner or by
  counsel, before the dependency is added ([reconciliation.md](reconciliation.md) §6, check 1). This
  is the one task the licence gates directly.

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
- **Hand-off:** `internal/runner/spend.go` is a core file and **every release's** runner work has
  other writers on it — the 0.15.0 window that made this a hand-off has shipped, and the property it
  named has not gone away. This task belongs to a release where the file has no other writer, and its
  planner must say so before it starts (§4, §5).

### M10 — The audit record, revocation, and what an operator sees

- **Files (6):** `ee/coordinator/mcpauth/audit.go`, `audit_test.go`,
  `ee/coordinator/mcpauth/tool_revoke.go`, `tool_revoke_test.go`,
  `ee/coordinator/mcpauth/audit_record.go`, `audit_record_test.go`.
- **Interface:** one structured line per call and per refusal, naming the identity, the tool, the
  scopes and the outcome and never a value, on `internal/credsvc/audit.go`'s pattern; a bounded
  per-identity record an operator can read; and revocation reachable from the API and from a
  person's own surface. **The record is written into M3's subtree**, which D922's answer makes
  readable by any signed-in person of the project — so "an operator can read it" is the **tool
  surface's** rule (D933) and not the database's, and the two are deliberately different: a launcher
  gets no audit through the API while the raw nodes are not closed to them
  ([spec.md](spec.md) §4, §6 and §8 state the tension).
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
| `internal/runner/spend.go` | M9 alone, but **shared with every other runner task in its release** | one variable and its merge; see §5 — which is why the release slot is chosen for the file's other writers, not only for this work |
| `SECURITY.md` | M4, M9, M10 | a public endpoint, a launch grant, and what the audit does not record — **the repository's standing rule is that a security-relevant change updates it in the same PR** |

## 5. The release slot

**The implementation targets 0.20.0 "Quokka" or later.** The design's own window is spent: **0.15.0,
0.16.0 and 0.17.0 have all shipped** since this folder was written (`docs/release-streams.yaml`'s cli
stream: `current: "0.17.0"`, `next: Ocelot`, so 0.18.0 is Ocelot and **0.20.0 is Quokka**), and the
0.18.0 and 0.19.0 windows are already spoken for. This is a re-slot, not a preference: the three
reasons the first version of this section gave were right about *why* the work needs a window of its
own and wrong only about *which*.

Why the implementation is **0.20.0 or later**, and not the next minor:

1. **The coordinator's own front door is still not built.** P7 and P8 still wait on this design
   (`docs/coordinator/README.md`:26), so the service M4 creates and the account M5 runs on do not
   exist yet. A release cannot carry an MCP endpoint on a service the same release is still
   designing.
2. **The work is big and security-heavy, and the owner's own instruction of 2026-11-04 is that it
   takes a slot of its own for that reason.** Ten tasks, **four of them SEC-marked**, a public
   endpoint, a new service account and **one new privilege** (M9's launch grant), behind an
   independent review that has not run yet. A window that is not already carrying another feature of
   this shape is the only one it fits in whole.
3. **M9 is a core change to a contention-prone file.** `internal/runner/spend.go` is core, and every
   runner task in whatever release carries M9 shares it (§4). Landing it in a full window means
   either a declared single writer is overridden or two changes are serialised by hand; neither is
   worth it for a feature whose first dependency is unbuilt.
4. **The update has to carry it, and the update is now gated.** The repository's standing rule is
   that a release ships no operator step beyond `fugaro update`, and M4's service and M9's grant are
   both Terraform that the update applies. Since this folder was written, **managed mode landed** and
   the gate covers `fugaro update` itself (`internal/cli/managed_gate.go`,
   `docs/managed-agent-converge/README.md`:20), so the migration this task carries — with the
   convergence test that an installation at the previous version ends with exactly M4's resources —
   runs through the gate like every other managed step. That is a heavier migration than it was, and
   it belongs in a release that is not already carrying the same work for three other features.

**What would move it earlier:** if the owner answers D920 the other way (P8's IAM invoker), M5's
OAuth half disappears, and M1–M3 plus a small M7 fit into whatever release is next. Nothing else
here is small enough to pull forward.

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
- **The stateless-orchestrator spec** (open as **#1682**, not on `main` when this was written). Its
  **state tool group** is hosted here: D931's registry serves the coordinator's own tools on the
  same endpoint under their existing names, so the state group reaches MCP clients through this
  front door and this plan builds **no second surface** for it. Which names that group carries, and
  whether any of them needs a scope of its own, is that design's to decide — D927 keeps v1 to two
  scopes, and a tool the two do not cover brings the scope question back as D927's. M7 is the task
  the registry lives in, so it is the one that changes if the group's names arrive after this plan.
- **The managed gate's decisions.** The gate landed while this design was open
  (`internal/cli/managed_gate.go`, `docs/managed-agent-converge/`), and M4's operator path runs
  through it: `fugaro update` is one of the commands the gate covers, so the front door's switch is
  admitted or refused by the gate's own rules and this plan adds **no row** to it. A new command that
  the gate should cover is a managed-mode change, not this work's — and M4's `#### For operators`
  block already routes the switch through `fugaro update` rather than through a second command, which
  is the same rule the gate exists to enforce.
- **A local stdio MCP server.** The owner's non-goal, and this plan does not creep into it.
- **Any change to `SECURITY.md`, `README.md`, `website/**`, `plugin/**` or `.github/workflows/**`
  made by the run that writes *this* document** — those belong to other works, and the run that
  wrote this folder touched `docs/` and its own test and nothing else. The implementation tasks of §3
  may touch `SECURITY.md` when they run, because by then the change is theirs and the repository's
  standing rule is that a security-relevant change updates it in the same PR (§4).
