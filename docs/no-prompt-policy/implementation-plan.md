# No-prompt policy — implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). **Status: plan only;
nothing is implemented; no task is approved.** Answering D1540–D1547 settled questions; each task below
needs the owner's explicit go. Decisions D1548–D1551 are pending and a task that depends on one says so.
Owner answers that shaped the plan (2026-11-07): D1540 values stay, never hang; D1541 unmanaged refuses
and names the flag-by-argument command; D1542 cap raise is owner-session only; D1543 builds lease an
estimated cost, the count bound goes; D1544 **keep `--ask`** (not the recommendation), remove `--yes`
and `--non-interactive`; D1545 **audit-only gate** (not the recommendation); D1546 no meta-repo
prerequisite; D1547 harness plus lint. Checks 380–389 and D1540–D1559 are reserved here.

## Conventions every part follows

- `.claude/rules/terraform.md` for `internal/infra/**`, `deploy/terraform/**`; `CLAUDE.md` "Things that
  bite" — notably HARD RULE (`fugaro update` is the whole upgrade; any format change ships its
  migration inside `update` with a test that the previous version converges), "Docs are tested", "A
  field never changes meaning", "Never edit `.github/workflows/`".
- Targeted tests, `GOFLAGS=-p=1`; always finish a task with `go test ./internal/cli -run Docs -count=1`.
- Hermetic: `internal/gcpfake`, `internal/testutil`, `t.TempDir()`. No secret-shaped literal. No agent
  performs a live check; every owner-run check ships as ONE idempotent script under
  `scripts/live-checks/` with `--dry-run` and a hermetic fake-gcloud test (owner rule).
- A change that touches a flag a plugin skill names changes the skill in the same task (plugin tests
  compare skills to the CLI).
- Security-sensitive tasks (IAM, cover tables, gate, agent detection, `CLAUDE.md`, rules, plugin skills)
  are **opus + mandatory independent review**: ask for in-run level 3 with `--review-levels`, never also
  a local reviewer.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No command waits on closed stdin | T2 (harness), T1 (lint) |
| Only two helpers may read stdin (`--ask`, value entry) | T1 lint |
| `--ask` never asks in an agent session or with `--json` | T1 helper test |
| An agent session cannot raise a cap; the refusal prints the owner's command | T3 |
| Over-cap error has estimated cost, cap, spent, the one command, non-zero exit | T8 |
| Observe mode builds proceed and `doctor` warns (D1548, pending) | T8, T9 |
| Audit-only gate records every out-of-table item and warns; integrity refusals unchanged (D1549, pending) | T9 |
| Marker format change converges an installation at the previous version | T10 |
| No doc/skill says `--yes`/`--non-interactive` after T11; `--ask` stays documented | T11 |

## Slice 0 — the rail (ships inert)

### T1 — sanctioned helpers and the static lint
- **Files:** `internal/cli/askconfirm.go` (new), `internal/cli/valueentry.go` (new),
  `internal/cli/noprompt_lint_test.go` (new).
- **Interfaces:** produces `askConfirm(cmd, question)` (reads only under `--ask`, owner terminal, not
  JSON, not an agent session; otherwise returns "proceed" or refuses per caller) and
  `readValue(cmd, spec)` (fails at once with the flag/env named when stdin is not a terminal). Consumes
  `stdinIsTerminal`, `agentMarker`.
- **Tests:** `TestAskConfirmNeverReadsOutsideAsk`, `TestAskConfirmRefusesInAnAgentSession`,
  `TestReadValueFailsAtOnceWithNoTerminal`, `TestNoStdinReadOutsideTheTwoHelpers` (red first: a
  stdin read exists in `budget.go`, so the lint fails listing every site — the list is its allow-list,
  shrinking to empty as T3–T7 land; the lint then fails on any non-empty list).
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli -run 'AskConfirm|ReadValue|NoStdinRead' -count=1`
- **Depends on:** none. **Commit:** `cli: the two sanctioned stdin readers and the lint that holds them (T1)`
- **Reviewer:** opus + mandatory independent review (agent detection).

### T2 — the closed-stdin harness
- **Files:** `internal/cli/noprompt_harness_test.go` (new).
- **Interfaces:** walks `NewRootCmd`, runs every leaf with stdin a closed pipe, a short deadline and
  fakes; a listed per-leaf fixture or exemption. Red: today it fails naming every prompting leaf (an
  allow-list that shrinks).
- **Tests:** `TestEveryCommandRunsWithClosedStdin`. **Command:**
  `GOFLAGS=-p=1 go test ./internal/cli -run TestEveryCommandRunsWithClosedStdin -count=1`
- **Depends on:** none. **Commit:** `cli: no command waits on a closed stdin (T2)`. **Reviewer:** standard.
- Settles the UNVERIFIED gcloud question by also failing on a `gcloud` exec without `--quiet`.

## Slice 1 — remove the confirmations (parallel, one family per task)

### T3 — budget: the raise, kill, and the owner-session-only rule (D1542)
- **Files:** `internal/cli/budget.go`, `internal/cli/budget_test.go`.
- Remove `confirmTyped`/`confirmTypedStrict` use; a raise in an agent session refuses and prints the
  exact `fugaro budget set …` command for the owner; the owner session raises with no prompt (`--ask`
  applies, T4 of the tensions). **Tests:** `TestBudgetRaiseRefusesInAnAgentSession`,
  `TestBudgetRaiseNeedsNoPromptAtATerminal`. **Command:**
  `GOFLAGS=-p=1 go test ./internal/cli -run 'TestBudget(Raise|Kill|Set)' -count=1`
- **Depends on:** T1. **Commit:** `budget: a raise asks nothing and an agent session cannot make one (T3)`.
  **Reviewer:** opus + mandatory independent review.

### T4 — image build and refresh: no confirmation, the lease
- **Files:** `internal/cli/image.go`, `image_build_catalog.go`, `image_refresh.go`,
  `image_refresh_managed.go`, `image_refresh_steps.go`, their tests.
- Remove `confirmBuild`/`buildNotConfirmable`; take the lease T8 provides before each submit; over cap →
  T8's error. **Tests:** `TestImageBuildAsksNothing`, `TestImageBuildOverCapFailsWithTheSpecError`.
- **Depends on:** T1, T8. **Commit:** `image: a build asks nothing and leases its cost (T4)`.
  **Reviewer:** opus + mandatory independent review (spends money).

### T5 — settings, accounts, config, delete
- **Files:** `internal/cli/settings_write.go`, `accounts.go`, `accounts_add.go`, `accounts_add_login.go`,
  `accounts_rm.go`, `accounts_managed.go`, `allow_delete.go`, `config_publish.go`, `config_init.go`, tests.
- Remove the y/N and typed confirmations; the owner-terminal login keeps its value entry through T1's
  helper. **Tests:** per command `…AsksNothing`. **Depends on:** T1. **Reviewer:** opus + mandatory
  independent review (accounts add writes credentials-adjacent state).
- **Commit:** `cli: settings, accounts and config writes ask nothing (T5)`.

### T6 — managed flag and the unmanaged refusal (D1541)
- **Files:** `internal/cli/managed_flag.go`, `internal/cli/managed_require.go` (new), `internal/cli/root.go`
  (a command annotation, no per-command edit), tests.
- The flag command takes the GCP project ID as an argument (no typed prompt); a mutating command on an
  unmanaged project refuses with that one command. **Tests:** `TestUnmanagedProjectRefusesAndNamesTheFlagCommand`,
  `TestFlagTakesTheProjectIDAsAnArgument`. **Depends on:** T1. **Reviewer:** opus + mandatory
  independent review (the one-way door of managed marking).
- **Commit:** `managed: flag by argument, and unmanaged projects refuse with the command (T6)`.

### T7 — init and setup
- **Files:** `internal/cli/init.go`, `init_firebase.go`, `init_fugaroyaml.go`, `init_inputs.go`,
  `init_plugin_stage.go`, `init_project.go`, `init_repo_gate.go`, `billing_prompt.go`, `pair.go`,
  `secrets.go`, tests.
- Confirmations removed; billing-account choice, names, pairing code and secrets go through `readValue`,
  failing at once with no terminal. **Tests:** `TestInitAsksNoConfirmation`, `TestBillingChoiceFailsAtOnceWithoutATerminal`.
- **Depends on:** T1. **Reviewer:** standard (secrets.go: opus + review, it is the credentials-adjacent
  reader — keep the hidden entry, change only its door). **Commit:** `init: no confirmations; values fail fast (T7)`.

### T8 — the cost estimator and the lease (D1543, D1548, D1550)
- **Files:** `internal/budget/buildcost.go` (new), `internal/budget/buildcost_test.go` (new),
  `internal/cli/image_cost.go` (new).
- A table keyed by machine type (upper bound, read from the build config in `internal/backend/gcp/build.go`),
  a lease outside a model session, the over-cap error (estimate, cap, spent, the single command, non-zero
  exit). Observe mode records and proceeds. **Tests:** `TestBuildCostIsAnUpperBound`,
  `TestOverCapErrorNamesEstimateCapSpentAndTheCommand`, `TestObserveModeRecordsAndProceeds`.
- **Depends on:** D1548 and D1550 answered. **Reviewer:** opus + mandatory independent review.
- **Commit:** `budget: an image build leases its estimated cost (T8)`.

### T9 — the gate becomes audit-only (D1545, D1549, D1551)
- **Files:** `internal/cli/managed_gate.go`, `managed_apply.go`, `internal/managed/converge.go`,
  `plan.go`, `apply.go`, `audit.go`, `internal/infra/tf/cover.go`, `guard.go`, `managed_cover.go`,
  `internal/cli/doctor_managed_audit.go`, tests (the `TestGateRefuses…`/`TestApplyRefuses…` families are
  rewritten as record-and-warn tests; the integrity ones stay).
- The content refusals named in D1549 record to the `AuditRecord` and warn; `doctor` flags out-of-table
  grants. `managedAsk` is deleted (a prompt). **Tests:** `TestOutOfTableGrantAppliesAndIsRecorded`,
  `TestIntegrityRefusalsStillRefuse`, `TestDoctorFlagsAnOutOfTableGrant`.
- **Depends on:** D1549, D1551 answered; T2 allow-list entry. **Reviewer:** opus + mandatory independent
  review at level 3; this is the task T2 of the tensions is about — its PR body carries the tension.
- **Commit:** `managed: the gate records and warns; it no longer refuses on content (T9)`.

### T10 — remove the build-count bound (D1543)
- **Files:** `internal/managed/builds.go`, `internal/infra/managed/marker.go`, `read.go`,
  `internal/infra/catalogcheck.go`, `internal/cli/image_check_catalog.go`, the schemas under `schemas/`
  the marker/spec shape appears in, tests.
- Marker version bump; `fugaro update` converges an installation at the previous marker version; the
  catalog check spec drops `MaxImageBuilds`. **Tests:** `TestPreviousMarkerVersionConvergesOnUpdate`,
  `TestCatalogCheckSpecNoLongerCarriesABuildCount`.
- **Depends on:** T4. **Release:** `### For operators`: "run `fugaro update`". **Reviewer:** opus + review.
- **Commit:** `managed: the image build count is gone; update migrates the marker (T10)`.

## Slice 2 — the flags, the class system, the words

### T11 — remove `--yes` and `--non-interactive`; skills and docs
- **Files:** every command file that registers them (`accounts*.go`, `budget.go`, `config_init.go`,
  `image_refresh.go`, `init.go`, plus any left), `plugin/skills/{setup,upgrade,working,parallelism}`
  and their `reference/`, `plugin/*_test.go`, `docs/**` that name them, tests.
- **Depends on:** T3, T4, T5, T6, T7, T9, T10. **Tests:** plugin tests, `go test ./internal/cli -run Docs`.
  **Reviewer:** opus + mandatory independent review (plugin skills). **Commit:**
  `cli: --yes and --non-interactive are gone; --ask stays (T11)`.

### T12 — delete the confirm class system
- **Files:** `internal/initflow/stage.go`, `internal/initflow/*_test.go`, `internal/cli/initsecrets.go`
  (`agentMarker` stays), callers' last references.
- Remove `Typed`/`YesOrTyped`, `CanConfirmManaged`, `GateConfirms`, `ConfirmsByGate`, `Managed.Removes/Covers`
  and D1026's pin; keep `NeedsTerminal`/`Conditions` as far as value entry and `--ask` need them.
- **Depends on:** T11. **Reviewer:** opus + mandatory independent review. **Commit:**
  `initflow: the confirmation classes are gone (T12)`.

### T13 — the policy in CLAUDE.md, the rules and SECURITY.md
- **Files:** `CLAUDE.md`, `.claude/rules/terraform.md`, `.claude/rules/credentials-service.md`,
  `SECURITY.md`, `website/content/security.md` (if it names the gate; `npm run check` then), the status line of this folder.
- States the policy and points here as the authority; rewrites the cover-table rule as a tripwire (T2
  of the tensions, option (a)) **only after the owner confirms**. Agents must not add confirmation steps.
- **Depends on:** T9, T11, owner ruling on T2. **Reviewer:** opus + mandatory independent review
  (agent guidance). **Commit:** `docs: no prompts, and what the cover tables now are (T13)`.

### T14 — owner-run checks
- **Files:** `scripts/live-checks/check-380-no-prompt-update.sh`, `check-381-over-cap-build.sh`,
  `check-382-agent-cannot-raise-a-cap.sh` (all new) plus a fake-gcloud test each, `docs/gcp-live-checklist.md` rows.
- **Depends on:** T3, T4, T9. **OWNER-RUN:** `scripts/live-checks/check-380-no-prompt-update.sh` (and
  381, 382); each prints PASS/FAIL, takes `--dry-run`, refuses the `belong` project. No agent runs them.
- **Commit:** `scripts: Checks 380-382 as runnable owner scripts (T14)`.

## Parallel execution

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | T1, T2 |
| 1 | T3, T5, T6, T7, T8 (needs D1548/D1550), T9 (needs D1549/D1551) |
| 2 | T4 (after T8) |
| 3 | T10 (after T4) |
| 4 | T11, then T14 (after T3, T4, T9) |
| 5 | T12 (after T11), T13 (after T9, T11, owner ruling) |

**Critical path:** T8 → T4 → T10 → T11 → T12. T9 is a second long path gated by owner answers to D1549/D1551.

### Shared-files table (no level holds two writers of one file)

| File | Writers (level) |
| --- | --- |
| `internal/cli/budget.go` | T3 (1), T11 (4) — different levels |
| `internal/cli/image*.go` | T4 (2), T11 (4) |
| `internal/cli/accounts*.go`, `settings_write.go`, `config_*.go` | T5 (1), T11 (4) |
| `internal/cli/init*.go`, `pair.go`, `secrets.go`, `billing_prompt.go` | T7 (1), T11 (4) |
| `internal/cli/managed_flag.go`, `root.go` | T6 (1) |
| `internal/cli/managed_gate.go`, `managed_apply.go`, `doctor_managed_audit.go` | T9 (1) |
| `internal/managed/builds.go`, `internal/infra/managed/*` | T10 (3) |
| `internal/initflow/stage.go` | T12 (5) |
| `CLAUDE.md`, `.claude/rules/*`, `SECURITY.md` | T13 (5) |
| `docs/gcp-live-checklist.md` | T14 (4) |

Sequential only where a build dependency forces it: the estimator before the build command that uses it,
the build command before the marker change, every caller before the flags, every flag before the class
system.

## Checks

| Check | Measures | Run by |
| --- | --- | --- |
| 380 | `fugaro update` on the dev project with stdin closed finishes without a prompt | owner, T14 script |
| 381 | an image build over the cap fails with estimate, cap, spent and the command, non-zero | owner, T14 script |
| 382 | a coding-agent session's `budget set` raise refuses and prints the command | owner, T14 script |
| 383–389 | reserved | — |

## Considered and declined

- Literal `-auto-approve` (F6): the saved-plan apply is the equivalent and cannot apply a different plan.
- Keeping the typed GCP-ID prompt for flagging a project managed: replaced by consent by argument (D1541).
- Waiting for the meta repo (D1546).
