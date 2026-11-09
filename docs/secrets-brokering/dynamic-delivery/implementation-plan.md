# Dynamic secret delivery: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md) (D1930–D1937, R1–R6, answered 2026-11-09, all as recommended). Each
task needs the owner's own explicit go; answering a decision is not approving a task. Base: `main` at
`934bb23f8`. Numbers held: **D1930–D1959**, **Checks 510–519** (510 and 511 used here; 512–519 reserved).

## Conventions every task follows

- Security-sensitive: [.claude/rules/credentials-service.md](../../../.claude/rules/credentials-service.md)
  (independent review; one audit line per refusal; no credential value in a line, log or error) and
  [.claude/rules/terraform.md](../../../.claude/rules/terraform.md) for `deploy/terraform/**`,
  `internal/infra/**`. `CLAUDE.md`'s HARD RULE: `fugaro update` converges everything, safe in flight.
- No secret-shaped literal in a test or fixture. No agent runs a live cloud action; live steps are
  OWNER-RUN scripts under `scripts/live-checks/`. The sandbox project is never `belong` (D147).
- Docs are tested: a doc change runs `go test ./internal/cli/ -run Docs`; `SECURITY.md` changes regenerate
  `website/content/security.md` in the same PR.
- A field never changes meaning: new names only (`credential_owner`, `slip`, `owner`, `credential`).
- Release actions go in `### For operators` of the same PR (`docs/releases/README.md`).
- Reviewer tier is **opus + mandatory independent review** for every task here except where a task says
  standard.

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | No two (id, name) pairs give one secret ID (F6, R1) | DS0.1 |
| G2 | The intake never reads, never touches the bundle or a name outside the two prefixes, and derives the prefix from the verified `sub` (R3) | DS2.1, DS2.2 |
| G3 | A slip ID alone is never enough; the claim binds the verified job account once (F7, D1937) | DS1.3, DS1.6 |
| G4 | Liveness and stop switches are read per call, uncached (F14, R4) | DS1.6 |
| G5 | The agent never holds a slip or an identity token; only the runner does (D1933) | DS1.5 |
| G6 | A hostile launcher or coding agent cannot read a value, overwrite another user's secret or write `fugaro-p-` unless admin (F8, F9) | DS2.1, DS2.3 |
| G7 | A pre-existing installation keeps working after `update` alone; bundle entries are read second (R6) | DS3.3, DS4.1 |
| G8 | Pool and token use is checked against owner and allowed users at issue time, default owner-only (D1936) | DS3.2 |
| G9 | No credential value in the slip record, audit line or run record (F19) | DS1.1, DS1.6 |
| G10 | `TestServiceHasNoAdminRoutes` still holds: the slip routes are the service's own contract, not an admin surface | DS1.3 |

## Slices

- **DS0 inert and owner-run**: the name grammar and the two live checks.
- **DS1 slips**: store, caller identity, grant and claim, launcher, runner, per-call verification.
- **DS2 intake**: the function, its Terraform, the CLI verbs, offboarding.
- **DS3 delivery**: pools with owners, the per-secret read, token ownership on the record.
- **DS4 migration and release**: convergence test, doctor, docs, operator notes. Flipping a Terraform mount
  off a job (D500's flip lists) for the other auth modes and workflow secrets is **not in this plan**: it is
  a later plan after Checks 512+ (recorded in §Declined).

## Tasks

Common to all: *Commit* is `<scope>: <subject>` (the trailer lines follow the repo rule). *Command* is the
named tests only.

### DS0.1 The secret name grammar (new package)
- **Files:** `internal/secretname/secretname.go` (new), `internal/secretname/secretname_test.go` (new)
- **Interfaces:** produces `User(id, name)`, `Project(name)`, `Parse(secretID)`; consumed by DS2.1, DS3.3.
- **Schema/rules/config:** none.
- **Tests:** `TestNoTwoPairsOneSecretID` (generated pairs), `TestSeparatorNotInName`, `TestLengthLimit`, `TestParseRoundTrip`.
- **Command:** `go test ./internal/secretname/ -count=1`
- **Depends on:** none. **Commit:** `secretname: the user and project secret ID grammar (R1)`
- **Red step:** the grammar test with `-` as the separator fails on the collision pair `12`/`3-foo` vs `123`/`foo`.
- **Tier:** opus + mandatory independent review. Not owner-run.

### DS0.2 Checks 510 and 511, written (one task, one writer of the register)
- **Files:** `scripts/live-checks/check510.sh` (new), `scripts/live-checks/check511.sh` (new), `docs/gcp-live-checklist.md` (edit), `docs/secrets-brokering/dynamic-delivery/check510-steps.md` (new)
- **What they measure:** **510** (U3): in the sandbox project, a name-prefix IAM condition admits `versions.add`/`secrets.delete` for a `fugaro-u-` secret and refuses the same on another name, create works unconditioned, and the maximum secret ID length. **511** (U4, U5): a request every N seconds keeps one service instance up between runs, and an agent-side process cannot fetch the job account's identity token from the metadata server.
- **Tests:** `go test ./internal/cli/ -run Docs` (register pins), `bash -n` on both scripts.
- **Command:** `go test ./internal/cli/ -run Docs -count=1`
- **Depends on:** none. **Commit:** `live-checks: Checks 510 and 511, written (not run)`
- **Red step:** the docs test fails until the register row and steps file exist.
- **Tier:** standard. Not owner-run (writing); the run is DS0.3.

### DS0.3 OWNER-RUN: run Checks 510 and 511
- **The owner types** (sandbox project, never `belong`): `scripts/live-checks/check510.sh --dry-run`, then `scripts/live-checks/check510.sh`, then the same for `check511.sh`, and pastes the result blocks back. No agent performs any step.
- **Depends on:** DS0.2. **Gate for:** DS2.2 (the IAM condition shape) and DS1.5 (the heartbeat). A failed 510 or 511 stops those tasks and returns to the owner as a decision.

### DS1.1 The slip store
- **Files:** `internal/creds/slips.go` (new), `internal/creds/slips_test.go` (new), `internal/creds/boundary_test.go` (edit)
- **Interfaces:** produces `Slip{ID, Owner, Allowed, Pool, MaxRuntime, Claimed{Account, At}}`, `Create`, `Claim` (compare-and-set), `End`; consumed by DS1.3, DS1.6. RTDB `creds/slips/<id>`; **no credential value is stored** (G9). The rules already deny every run identity `creds/`.
- **Tests:** `TestClaimIsOnce`, `TestSlipHoldsNoValue`, `TestBoundaryAllowsSlipsOnlyViaService`, `TestExpiredSlipRefused`.
- **Command:** `go test ./internal/creds/ -run 'Slip|Boundary' -count=1`
- **Depends on:** none. **Commit:** `creds: the slip record under creds/slips (D1932)`
- **Red step:** `TestClaimIsOnce` fails with a non-atomic claim.
- **Tier:** opus + mandatory independent review.

### DS1.2 A person caller keyed on the Google `sub`
- **Files:** `internal/credsvc/auth.go` (edit), `internal/credsvc/auth_test.go` (edit)
- **Interfaces:** produces `PersonSub(ctx, token) (sub string, email string, err)` for the grant request and the intake; the person class still admits nothing else.
- **Tests:** `TestPersonSubIsTheTokenSub`, `TestPersonRefusedWithoutVerifiedEmail`, `TestPersonClassStillRefusedOnRunRoutes`.
- **Command:** `go test ./internal/credsvc/ -run 'Person' -count=1`
- **Depends on:** none. **Commit:** `credsvc: the person caller's account ID (F5)`
- **Red step:** a token with an email and no `sub` is admitted before the change.
- **Tier:** opus + mandatory independent review.

### DS1.3 Grant and claim routes
- **Files:** `internal/credsvc/slip.go` (new), `internal/credsvc/slip_test.go` (new), `internal/credsvc/wire.go` (edit), `internal/credsvc/server.go` (edit)
- **Interfaces:** consumes DS1.1, DS1.2; produces `POST /v1/slip` (launcher grant, person class) and `POST /v1/slip/claim` (job class, first call). A claim binds the **verified job account**; the execution name is recorded for audit only (D1937).
- **Tests:** `TestGrantRefusedForAnotherOwnersToken`, `TestSlipIDAloneIsNotEnough`, `TestSecondClaimRefused`, `TestClaimBindsVerifiedAccount`, `TestServiceHasNoAdminRoutes`.
- **Command:** `go test ./internal/credsvc/ -run 'Slip|HasNoAdminRoutes' -count=1`
- **Depends on:** DS1.1, DS1.2. **Commit:** `credsvc: slip grant and claim routes (D1937)`
- **Red step:** `TestSecondClaimRefused` fails before the claim is conditional.
- **Tier:** opus + mandatory independent review.

### DS1.4 The launcher asks for a slip
- **Files:** `internal/cli/run.go` (edit), `internal/cli/run_slip_test.go` (new)
- **Interfaces:** consumes DS1.3; passes `FUGARO_SLIP` to the job (never to the agent: DS1.5). Version-skew constant for a service without the route: the run proceeds on the existing path.
- **Tests:** `TestRunRequestsSlipWhenServiceServesIt`, `TestRunFallsBackWithoutSlipRoute`, `TestSlipIDNotInTaskFile`.
- **Command:** `go test ./internal/cli/ -run 'RunSlip' -count=1`
- **Depends on:** DS1.3. **Commit:** `run: request a slip and pass its ID to the job`
- **Red step:** `TestSlipIDNotInTaskFile` fails if the ID reaches the task file.
- **Tier:** opus + mandatory independent review.

### DS1.5 The runner claims, heartbeats, retries
- **Files:** `internal/runner/budget_session.go` (edit), `internal/gateway/service.go` (edit), `internal/runner/slip_test.go` (new)
- **Interfaces:** consumes DS1.3; the runner claims at the first model call, sends a keep-warm request while the run lives (D1934), retries credential and proxy requests with backoff; the slip and identity token stay in the runner's process, not in the agent's environment (G5).
- **Tests:** `TestClaimOnFirstCall`, `TestAgentEnvHoldsNoSlip`, `TestBackoffOnServiceError`, `TestHeartbeatStopsWithRun`.
- **Command:** `go test ./internal/runner/ -run 'Slip' -count=1 -timeout 15m`
- **Depends on:** DS1.3, DS0.3 (U4/U5 pass). **Commit:** `runner: claim the slip, keep the service warm (D1933, D1934)`
- **Red step:** `TestAgentEnvHoldsNoSlip` fails if the variable is exported to the agent.
- **Tier:** opus + mandatory independent review.

### DS1.6 Per-call verification, the cache, the audit fields
- **Files:** `internal/credsvc/server.go` (edit), `internal/credsvc/audit.go` (edit), `internal/credsvc/slip_verify_test.go` (new)
- **Interfaces:** every run call checks slip ID, the job token's account against the claim, expiry and max runtime; the slip body is cached 5 minutes, the outcome and stop switches are not (R4); the audit line gains `slip`, `owner`, `credential` (R5); issuance ends at max runtime.
- **Tests:** `TestFinishedRunRefusedUncached`, `TestCacheOnlyHoldsSlipBody`, `TestMaxRuntimeEndsIssuance`, `TestOneAuditLinePerRefusal`.
- **Command:** `go test ./internal/credsvc/ -run 'SlipVerify|OneAuditLine' -count=1`
- **Depends on:** DS1.3. **Commit:** `credsvc: verify the slip on every call (R4, R5)`
- **Red step:** `TestFinishedRunRefusedUncached` fails if liveness reads the cache.
- **Tier:** opus + mandatory independent review.

### DS2.1 The intake function
- **Files:** `internal/intake/server.go` (new), `internal/intake/handlers.go` (new), `internal/intake/server_test.go` (new), `cmd/fugaro-intake/main.go` (new; or the existing credentials serve pattern in `internal/cli/credentials_serve.go`, which this task reads first and follows)
- **Interfaces:** consumes `secretname` and DS1.2; produces create, add-version, list (names and dates), delete under the caller's prefix; `fugaro-p-` only for an admin (D1935); an admin-only delete of another user's prefix for offboarding. One audit line per call; never a value in a line (G2, G6).
- **Tests:** `TestPrefixFromSubNotFromBody`, `TestRefusesNameOutsideGrammar`, `TestNonAdminRefusedSharedPrefix`, `TestListNeverReturnsValue`, `TestNoReadMethodExists`.
- **Command:** `go test ./internal/intake/ -count=1`
- **Depends on:** DS0.1, DS1.2. **Commit:** `intake: the secret intake function (R3)`
- **Red step:** `TestPrefixFromSubNotFromBody` fails if the prefix is taken from the request.
- **Tier:** opus + mandatory independent review.

### DS2.2 Terraform: the intake, its account, the service's prefix accessor
- **Files:** `deploy/terraform/gcp/modules/installation/intake.tf` (new), `deploy/terraform/gcp/modules/installation/credentials.tf` (edit), `deploy/terraform/gcp/modules/installation/iam.tf` (edit), `deploy/terraform/secret_readers_test.go` (edit), `docs/secrets-brokering/secret-readers.md` (edit), `deploy/terraform/terraform_test.go` (edit)
- **Interfaces:** the intake account holds create (unconditional) and version-add, list, delete with a name-prefix condition; no accessor. The service's account gains an accessor with the same condition. A guard test holds both conditions, and the exceptions table gains one row for the service's prefix accessor (F18).
- **Tests:** `TestIntakeAccountHasNoAccessor`, `TestIntakeRolesAreConditioned`, `TestServicePrefixAccessorIsConditioned`, `TestSecretReadersTableMatches`.
- **Command:** `go test -tags terraform ./deploy/terraform/ -run 'Intake|SecretReaders' -count=1` and `terraform fmt -check -recursive deploy/terraform`
- **Depends on:** DS2.1, DS0.3 (Check 510 passed). **Commit:** `terraform: the intake and the prefix-conditioned reader (D1931)`
- **Red step:** the guard fails if the intake account holds `secretAccessor`.
- **Tier:** opus + mandatory independent review. Applied by `fugaro update` (HARD RULE).

### DS2.3 The user verbs
- **Files:** `internal/cli/secrets.go` (edit), `internal/cli/secrets_intake.go` (new), `internal/cli/secrets_intake_test.go` (new)
- **Interfaces:** `fugaro secrets set|ls|rm` for a user (`--project` for shared) call the intake with the caller's ID token; value from a hidden prompt or stdin only; refused inside an agent session or a run (F9, F12).
- **Tests:** `TestSetRefusedInAgentSession`, `TestValueNeverInArgsOrEnv`, `TestLsPrintsNamesAndDates`.
- **Command:** `go test ./internal/cli/ -run 'SecretsIntake' -count=1`
- **Depends on:** DS2.1. **Commit:** `cli: user secrets through the intake`
- **Red step:** `TestSetRefusedInAgentSession` fails without the guard.
- **Tier:** opus + mandatory independent review. Docs: `go test ./internal/cli/ -run Docs`.

### DS2.4 Offboarding
- **Files:** `internal/cli/offboard.go` (new), `internal/cli/offboard_test.go` (new)
- **Interfaces:** an admin command deletes a user's prefix through the intake, ends the user's open slips (DS1.1) and removes the invoker grant through `update`'s own converge path (F20).
- **Tests:** `TestOffboardDeletesPrefixAndEndsSlips`, `TestOffboardRefusedForNonAdmin`, `TestPoolLosesRemovedMembers`.
- **Command:** `go test ./internal/cli/ -run 'Offboard' -count=1`
- **Depends on:** DS2.1, DS1.1. **Commit:** `cli: offboard a member (D1935)`
- **Red step:** the non-admin test fails before the role check.
- **Tier:** opus + mandatory independent review.

### DS3.1 Pools with an owner and allowed users
- **Files:** `internal/config/craft.go` (edit), `internal/projectpolicy/policy.go` (edit), `internal/projectpolicy/version.go` (edit), `internal/projectpolicy/migrate.go` (edit), `schemas/project-layer.schema.json` (edit), tests beside each
- **Interfaces:** additive `owner`, `allowed_users`; default owner-only; policy version bump with the migration run by `update`.
- **Tests:** `TestPoolOwnerDefaultsToOnlyOwner`, `TestMigrationFromPreviousVersionConverges`, `TestSchemaMatchesDecoder`.
- **Command:** `go test ./internal/config/ ./internal/projectpolicy/ -run 'Pool|Migrat|Schema' -count=1`
- **Depends on:** none. **Commit:** `policy: pool owner and allowed users, version bump (D1936)`
- **Red step:** the convergence test fails against the previous version's fixture.
- **Tier:** opus + mandatory independent review. Release action: DS4.3.

### DS3.2 Pool authorisation at issue time
- **Files:** `internal/credsvc/slip.go` (edit), `internal/credsvc/slip_pool_test.go` (new)
- **Interfaces:** consumes DS3.1; a slip naming a token or pool is refused unless the launcher is owner or allowed; pools resolve at issue time, so removed members vanish (G8).
- **Tests:** `TestSlipRefusedForNonMember`, `TestOwnerOnlyByDefault`, `TestPoolResolvedAtIssue`.
- **Command:** `go test ./internal/credsvc/ -run 'SlipPool' -count=1`
- **Depends on:** DS1.3, DS3.1. **Commit:** `credsvc: authorise a pool at issue time`
- **Red step:** the non-member test fails before the check.
- **Tier:** opus + mandatory independent review.

### DS3.3 The per-secret read, in order
- **Files:** `internal/credsvc/dynamicsecrets.go` (new), `internal/credsvc/dynamicsecrets_test.go` (new), `internal/credsvc/cascade.go` (edit)
- **Interfaces:** consumes `secretname`; resolves a user-supplied credential by its per-secret name, then by the bundle entry (R6); no value in an error or line; failed read fails closed, never falls back to a value the job holds (D143).
- **Tests:** `TestPerSecretBeforeBundle`, `TestBundleEntryStillServes`, `TestFailedReadFailsClosed`, `TestNoValueInErrors`.
- **Command:** `go test ./internal/credsvc/ -run 'Dynamic|Cascade' -count=1`
- **Depends on:** DS0.1, DS1.3. **Commit:** `credsvc: read user credentials by name, then the bundle (R6)`
- **Red step:** `TestPerSecretBeforeBundle` fails when the bundle is consulted first.
- **Tier:** opus + mandatory independent review.

### DS3.4 Token ownership on the run record
- **Files:** `internal/runstore/runstore.go` (edit), `internal/runstore/runstore_owner_test.go` (new), `internal/runner/record_owner.go` (new)
- **Interfaces:** additive `credential_owner` beside `oauth_account` and `stage_accounts`, never changing either (F16). The reader tests in `internal/runstore/cost_readers_guard_test.go` stay green.
- **Tests:** `TestRecordNamesOwnerOfTokenUsed`, `TestExistingFieldsUnchanged`.
- **Command:** `go test ./internal/runstore/ -run 'Owner|Fields' -count=1`
- **Depends on:** DS1.6. **Commit:** `runstore: credential_owner on the run record (F16)`
- **Red step:** the owner test fails with the field absent.
- **Tier:** standard.

### DS4.1 Convergence under update
- **Files:** `internal/cli/update.go` (edit), `internal/cli/update_slips_test.go` (new)
- **Interfaces:** `update` deploys the intake, applies the prefix accessor and publishes the policy migration, with no other step; a run in flight keeps working (`TestUpdateLeavesARunInFlightRunning` stays green). An installation at the previous version converges.
- **Tests:** `TestUpdateConvergesFromPreviousInstallation`, `TestUpdateLeavesARunInFlightRunning`, `TestUpdateAsksNoRunToFinish`.
- **Command:** `go test ./internal/cli/ -run 'UpdateConverges|UpdateLeavesARunInFlight' -count=1`
- **Depends on:** DS2.2, DS3.1. **Commit:** `update: converge to dynamic delivery (HARD RULE)`
- **Red step:** the previous-installation fixture does not converge before the change.
- **Tier:** opus + mandatory independent review.

### DS4.2 Doctor
- **Files:** `internal/cli/doctor_brokering.go` (edit), `internal/cli/doctor_brokering_test.go` (edit)
- **Interfaces:** reports the intake deployed, its account holding no accessor, the prefix condition present, and a per-secret name that no grammar admits. Short output (the one-screen rule).
- **Tests:** `TestDoctorIntakeHasNoAccessor`, `TestDoctorFlagsUnparseableSecretName`.
- **Command:** `go test ./internal/cli/ -run 'DoctorBrokering' -count=1`
- **Depends on:** DS2.2. **Commit:** `doctor: the intake and the prefix accessor`
- **Red step:** the accessor test fails before the check.
- **Tier:** opus + mandatory independent review.

### DS4.3 Docs, SECURITY and release notes
- **Files:** `SECURITY.md` (edit), `website/content/security.md` (regenerated), `docs/releases/vNEXT.md` (edit or new), `docs/secrets-brokering/README.md` (edit)
- **Interfaces:** states the new trust boundary (intake write power, the prefix accessor, the slip) and what a hostile launcher can do; `### For operators`: "run `fugaro update`" plus what it asks.
- **Tests:** `go test ./internal/cli/ -run Docs -count=1`; `npm run check` in `website/` for the regenerated page.
- **Depends on:** DS2.3, DS3.2, DS4.1. **Commit:** `docs: SECURITY and the release notes for dynamic delivery`
- **Red step:** the website security test fails on drift before regeneration.
- **Tier:** opus + mandatory independent review.

## Parallel execution

**Edges** (from the Depends-on lines): DS0.3←DS0.2; DS1.3←DS1.1,DS1.2; DS1.4←DS1.3; DS1.5←DS1.3,DS0.3; DS1.6←DS1.3; DS2.1←DS0.1,DS1.2; DS2.2←DS2.1,DS0.3; DS2.3←DS2.1; DS2.4←DS2.1,DS1.1; DS3.2←DS1.3,DS3.1; DS3.3←DS0.1,DS1.3; DS3.4←DS1.6; DS4.1←DS2.2,DS3.1; DS4.2←DS2.2; DS4.3←DS2.3,DS3.2,DS4.1.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | DS0.1, DS0.2, DS1.1, DS1.2, DS3.1 |
| 1 | DS0.3 (OWNER-RUN), DS1.3, DS2.1 |
| 2 | DS1.4, DS1.5, DS1.6, DS2.2, DS2.3, DS2.4, DS3.2, DS3.3 |
| 3 | DS3.4, DS4.1, DS4.2 |
| 4 | DS4.3 |

**Critical path:** DS1.2 → DS2.1 → DS2.2 → DS4.1 → DS4.3 (with DS0.2 → DS0.3 gating DS2.2 in parallel).
Sequential only where an interface or a gate forces it: slips need the store and the caller identity; the
Terraform needs the function and the owner's Check 510.

**Shared files** (no level holds two writers):

| File | Writers (level) |
| --- | --- |
| `internal/credsvc/server.go` | DS1.3 (1), DS1.6 (2) |
| `internal/credsvc/slip.go` | DS1.3 (1), DS3.2 (2) |
| `internal/credsvc/auth.go` | DS1.2 (0) |
| `internal/credsvc/cascade.go` | DS3.3 (2) |
| `internal/credsvc/audit.go`, `wire.go` | DS1.6 (2), DS1.3 (1) |
| `internal/cli/secrets.go` | DS2.3 (2) |
| `internal/cli/update.go` | DS4.1 (3) |
| `internal/cli/doctor_brokering.go` | DS4.2 (3) |
| `internal/cli/run.go` | DS1.4 (2) |
| `internal/runner/budget_session.go`, `internal/gateway/service.go` | DS1.5 (2) |
| `internal/runstore/runstore.go` | DS3.4 (3) |
| `internal/config/craft.go`, `internal/projectpolicy/*` | DS3.1 (0) |
| `deploy/terraform/gcp/modules/installation/credentials.tf`, `iam.tf`, `secret_readers_test.go`, `secret-readers.md` | DS2.2 (2) |
| `docs/gcp-live-checklist.md` | DS0.2 (0) |
| `SECURITY.md`, `website/content/security.md`, `docs/secrets-brokering/README.md` | DS4.3 (4) |

## Checks

| Check | Measures | Run by |
| --- | --- | --- |
| 510 | IAM name-prefix conditions on version-add and delete, unconditioned create, secret ID length (U3) | Owner (DS0.3), sandbox project |
| 511 | A request every N seconds keeps an instance up; the agent cannot fetch the identity token (U4, U5) | Owner (DS0.3) |
| 512–519 | Reserved, unused | |

## Declined, and why

- **Flipping the Terraform mounts off the jobs** (the addendum's "Removed"): a later plan, per D500 and R2, after
  the model path is proven by a live run; this plan makes the service the reader and leaves every mount in
  place.
- **Firestore for slips** (D1932) and **jobs calling the proxy directly** (D1933): declined by the owner's answers.

## Post-deploy actions

`### For operators` of the release: run `fugaro update`; it deploys the intake, applies the accessor and
migrates the policy, and says so in one line when runs are running. Nothing else (DS4.3).
