# Implementation plan — `fugaro setup` and `fugaro update`

**Goal.** Do what [spec.md](spec.md) asks for **as amended by
[reconciliation.md](reconciliation.md)**: replace `fugaro init` with two
commands — **`fugaro setup`**, the interactive first-time wizard (project,
billing, Firebase, installation, then the first repository), and **`fugaro
update`**, the re-converge after a CLI upgrade — default the project id to
`fugaro-<project name>`, ask every missing value interactively while keeping a
flag for everything, list the open billing accounts the logged-in user can use,
and rename the retired word everywhere a user or an agent reads it today.
Where the reconciliation and the brief disagree, **the reconciliation wins**.

**Status: plan only; nothing is implemented; decisions open.** Not one line of
any task below has been written or run. **D1–D12 in
[reconciliation.md](reconciliation.md) §5 are open**: every one carries a
recommendation and none is taken. Answering a decision is not approving a task.
The gate at the end of this file is **not passed**.

**Checked against.** `main` at `82d366af`, the same commit as
[reconciliation.md](reconciliation.md). Every path and symbol below was
located with `git grep` and re-checked to exist before it was written; a path
marked **new** does not exist yet. Line numbers are never cited. Three goldens
are named throughout and are the plan's best verification target:
`internal/cli/testdata/init_commands.golden` (every stage's
"left for you" text, rewritten by `internal/cli/init_output_test.go`'s
`-update` flag), `internal/cli/testdata/init_review.golden` (`-update-review`)
and `internal/cli/testdata/init_retire.golden` (`-update-retire`).

**Spec.** [spec.md](spec.md) (verbatim). **Reconciliation.** [reconciliation.md](reconciliation.md)
— C1–C3, F1–F24, D1–D12, O1–O5, the UNVERIFIED table. **Index.** [README.md](README.md).

**Related.** [gcp-setup.md](../gcp-setup.md) · [gcp-live-checklist.md](../gcp-live-checklist.md)
(Checks 27 and 111) · [m11-setup-and-skills.md](../design/m11-setup-and-skills.md) ·
[2026-10-04-m11-setup-and-skills.md](../plans/2026-10-04-m11-setup-and-skills.md)
(the converge-loop design this builds on) · [v1.md](../design/v1.md) ·
[credentials-service/](../credentials-service/) (the shape this folder follows)

---

## What this does not do

- **No engine.** `internal/initflow` is unchanged except for one thing (W1.1,
  O2): it stops printing a hard-coded command name. Every stage, every
  confirmation class and every refusal stays where it is.
- **No bucket renaming.** D4 accepts `fugaro-runs-fugaro-<name>` when the id
  default is used. Nothing about `fugaro-runs-<gcp-project>`,
  `fugaro-tfstate-<gcp-project>` or the managed fact's path moves.
- **No historical rewrite.** `docs/plans/**`, `docs/releases/v*`,
  `docs/design/**` describing shipped state, `docs/credentials-service/**`,
  `docs/website/**` and **`docs/setup-wizard/**` itself** keep the old name
  (D9, F20). The rename's guard test enforces the line, and this folder is on
  the exempt side of it: its four documents are where the retirement is
  described, so they name the retired command **75 times** by necessity.
- **No fourth verb** (O1): `setup`, `update`, `upgrade`, the hidden `init`
  tombstone, and `fugaro config init` left alone.
- **No `fugaro upgrade` change beyond its text.** D1 (recommended) keeps the two
  commands apart.

---

## Builds on merged code

| What exists | Where | Used by |
|---|---|---|
| The converge loop: `Stage`, the canonical `order` with `needs`/`noYes`, states, `Left`/`LeftKind`, `Options`, `Result`, `Run`, `Validate`, the preview and its plan view | `internal/initflow/loop.go`, `stage.go` | W1.1, W1.2, every task that asserts a state |
| The confirmation machinery: `ConfirmClass` (`YesOrTyped`, `Typed`), `Conditions`, `Managed`, `CanConfirm`, `CanConfirmManaged`, `CanConfirmRun`, `runCovered`, `RunCovers`, `YesCovers`, `noYes`, `SelfConfirming`, `InputStage.Missing()` | `internal/initflow/stage.go` | W0.1, W1.3, W3.5, W4.3 |
| The stage registration, the `engineStage` adapter, `outcome()` | `internal/cli/init_stages.go` | W1.2, W1.3, W5.1 |
| The command, every flag and its usage text, `initOptions`, `newInitCmdWith`, `runInit`, `markCreated`, `installOptions`, the `strictCreate`/`created` pair | `internal/cli/init.go` | W1.2, W2.1, W2.2, W3.3 |
| The interactive inputs: `gatherInputs`, `prompt`, `canAsk`, `firstRunMissing`, `suggestGCPProject`, `sanitiseName`/`suggestName`, `installationName`/`readInstallationName`, `const defaultRegion` | `internal/cli/init_inputs.go` | W3.1, W3.2, W3.5 |
| The project stage and its three typed banners: `projectStage`, `typed`, `canType`, `checkCreateProject`, `guided`, `billingHelp`, `existingNote`, `sanitized` | `internal/cli/init_project.go` | W1.3, W4.3, W4.4, W4.5 |
| The typed Firestore location and the billable build | `internal/cli/init_firebase.go`, `init_images.go`, `init_review.go` | W0.1, W3.2 |
| The repository stage and its gates: `newRepositoryStage`, `authorize`, `resolveRepoTarget`, `checkoutProject`, `checkoutNote`, `unknownDetail` | `internal/cli/init_repo_stage.go`, `init_repo_gate.go`, `init_repo_target.go` | W3.4 |
| The Cloud Billing fake: `SetBilling`, `UseCRM`, `SetSuspended`, `accounts`, `Link` | `internal/gcpfake/billing.go` | W4.2, W4.3 |
| The billing preflight: `Billing(...)`, "the Cloud Billing API is disabled on <quota>, the quota project of your credentials" | `internal/preflight/google.go` | W4.4 |
| The honest UNVERIFIED list against the real services | `internal/infra/projectcreate.go` | W4.1, W4.5 |
| Credential hygiene: `Options.Redact`, `loop.redact`/`redactAll`/`redactedCause`, `held`, `maxHeldSecrets` | `internal/initflow/loop.go`, `internal/cli/initsecrets.go` | W4.3, W0.1 |
| `restore` invoking init as a child: `restoreInitCmd = newInitCmdWith`, and `TestRestoreSeamsAreProductionsFunctions` pinning the pointer | `internal/cli/restore.go`, `restore_test.go` | W1.2 |
| The three goldens and their rewrite flags | `internal/cli/init_output_test.go` (`-update`), `init_review_test.go` (`-update-review`), `init_retire_test.go` (`-update-retire`) | W1.2, W2.1 |
| The docs pins | `internal/cli/docs_*_test.go` (≈40), `internal/clidoc/cliref_test.go`, `internal/cli/docs_website_*_test.go`, `plugin/{readme,setup_skill,skills_lint}_test.go` | W2.3, W2.4, W2.6 |
| The generated reference and its generator | `internal/clidoc/cliref.go`, `website/content/cli-reference.md` | W2.4 |
| The dated-record precedent for a retirement | `plugin/readme_test.go`'s `TestReadmeNamesNoRetiredSkill` `skip` regex | W0.2, W2.6 |
| The live-check register and its numbering rule | `docs/gcp-live-checklist.md` | W0.3, W6.1, W6.2 |

---

## Slice summary

| Slice | What it is | Tasks | Mergeable alone? | When |
|---|---|---|---|---|
| **0 — Verify first** | The refusal matrix and the rename census pinned as tests; Checks 27 and 111 written | W0.1–W0.3 | Yes — two new test files and one document | W0.1 and W0.2 now, on the owner's go; W0.3 as soon as **D10** is answered |
| **1 — The two front doors** | `initflow` stops naming a command; `setup` and `update` registered; `init` a tombstone; `restore` repointed; `setup` is the wizard | W1.1–W1.3 | Yes — the CLI keeps converging; the old name still resolves to the tombstone | After Slice 0 |
| **2 — The rename** | The retired word out of every live string, document, skill, Terraform description, generated file and pinned test; the census emptied | W2.1–W2.7 | Yes, in the order given — a partial rename is a failing docs pin, which is the intent | After W1.2 (W2.5 can start at any time) |
| **3 — The wizard's questions** | The id default, the region kept apart from the Firestore location, `--firebase-project`, the first-repository question, and the two-phase input | W3.1–W3.5 | Yes — each leaves a working wizard with one more question answered | After Slice 1 |
| **4 — The billing picker** | The Cloud Billing client, the gcloud fallback, the picker, the API-enable refusal, and the comments the reversal falsifies | W4.1–W4.5 | Yes — with the picker absent or refused, today's `guided()` line is what runs | After W3.5 |
| **5 — `fugaro update`** | Its stage set, its refusals, its non-interactive contract, and the e2e tests renamed | W5.1–W5.2 | Yes | After Slice 1 |
| **6 — The checks** | Check 27 rewritten for `setup`, Check 111 for the picker's failure modes | W6.1–W6.2 | Owner-run; no commit but the recorded result | After a release |
| **7 — Later, unscheduled** | A deprecated alias, a second installation on one machine, an interrupted wizard, a picker preview | — | — | — |

---

## Check numbers this plan takes

**27 is rewritten; 111 is new.** `docs/gcp-live-checklist.md`'s **Check 27**
(M11's simple setup, on a throwaway project, **NOT RUN**) covers the
never-exercised `--create-project` / `--link-billing` path; its steps name flags
and a command this work renames, so its text is stale rather than performed, and
it is rewritten **in place** — same number, still NOT RUN (D10). The register's
written sections run to **89** (81, 82 and 89 are written, in the mobile block);
90–99 are the credentials service, 100–109 the mobile functional spec, and
**110 is claimed by an open pull request** — the register's rule is that
whichever of two open claims merges second renumbers itself. **111 is therefore
the next free number**, and it is taken here.

| # | The check | Written by | Run at | Where |
|---|---|---|---|---|
| **27** (rewritten) | **`fugaro setup` end to end.** From an empty shell on a throwaway project: the wizard asks in order; Enter accepts the id `fugaro-<name>`; the created project types its ID; the billing picker lists the owner's open accounts by number; the link types the account's ID; the Firestore location types `us-east5`; the first image build types its confirmation; installation, Firebase and the first repository converge; then `fugaro update` **twice**, the second run asserting "No changes" for every stage | W0.3 | W6.1 | Installation `fugaro`, sandbox repository, throwaway project |
| **111** (new) | **The picker's failure modes.** API disabled on the quota project; a user without `billing.accounts.list`; no TTY; `--json`; an agent session — each asserting the guided `gcloud billing projects link` line, that nothing was enabled, and that no account list appears in any output | W0.3 | W6.2 | Installation `fugaro` |

Check 27's old text is **replaced, not supplemented**; a number whose subject was
rewritten stays the same number and is not reused elsewhere.

---

## Conventions every task follows

1. **Docs and pins move together.** Every task that renames a *string* renames
   the document or test that pins that string **in the same commit**. A red pin
   is the specification, not an obstacle: run the pin first, read the sentence
   it names, change it on purpose. The guards are
   `GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc ./plugin/... -run 'Docs|Lint|CliRef|Readme|Skill' -count=1`.
2. **The goldens are regenerated, never hand-edited.**
   `go test ./internal/cli -run TestInitOutputView -update`, `-update-review`,
   `-update-retire`; the regenerated diff is read line by line in review,
   because it *is* the user-visible text.
3. **Nothing is decided here.** A task that would take a D-row's decision stops
   and asks. In particular no task implements the billing list (D6), an alias
   (D2), an automatic API enable (D3) or the merge (D1) as if it were settled.
4. **No secret-shaped literals** in tests, fixtures or docs. Fake account ids in
   the billing tests are built at run time (a constant prefix plus `t.Name()`),
   never a literal that looks like a key. `images/trivy-secret.yaml` and
   `plugin/skills_lint_test.go` already enforce their areas.
5. **Hermetic tests.** No network, no real cloud, no `gcloud` on PATH needed:
   `internal/gcpfake` (its `billing.go` already models `accounts`, `Link`,
   `SetSuspended`, `UseCRM`), `internal/testutil`, `t.TempDir()`. The gcloud
   fallback (W4.2) is tested through an injected command runner, never by
   executing `gcloud`.
6. **No agent edits `.github/workflows/`.** Nothing in this work needs one; if
   the website's build grows a step, the file is staged under
   `docs/pending-workflows/` for the owner to copy.
7. **The refusals are immutable.** No task weakens `canType()`, `RunCovers`,
   `YesCovers`, `Managed`, the agent marker check, the impersonation check, the
   quota-project check or the `ACTIVE` project check. A task that has to change
   one says so in its own entry and gets opus plus a mandatory independent
   review.
8. **Credentials are never printed or stored.** `Options.Redact` stays wired for
   both front doors. The billing picker (W4.3) prints account **identifiers**,
   which are not credentials, and never under `--json`.
9. **`GOFLAGS=-p=2`** for anything touching `internal/cli`, `internal/initflow`
   or `internal/infra`. Before merge, `fugaro verify build` and
   `fugaro verify test` on the final commit.
10. **One file, one writer per level.** The levels below are disjoint by file;
    `internal/cli` is one lane, which is why the table looks thin.
11. **The dated records are read-only** (D9): `docs/plans/**`,
    `docs/releases/v*`, `docs/design/**`, `docs/credentials-service/**`,
    `docs/website/**` and **this whole folder, `docs/setup-wizard/**`** — never
    edited by a rename task. These six are exactly W0.2's `datedRecords`
    constant, and the two plan folders on the list (`docs/website/`,
    `docs/credentials-service/`) are another plan's record, while
    `docs/setup-wizard/`'s prose is *about* the retirement and cannot stop
    naming the retired command.

---

## Global Constraints

1. **Docs only in this pull request.** It adds `docs/setup-wizard/{spec.md,
   reconciliation.md,implementation-plan.md,README.md}` and edits nothing else.
   No Go, Terraform, schema, skill or workflow change. (The tasks below edit
   files outside `docs/`; this pull request does not.)
2. **No task is approved.** Each starts on the owner's explicit go for that task
   or a named group.
3. **Slices 1–5 wait for the D-rows they depend on** — D12 was opened by this
   plan's own review and is the newest of them — and the
   "Decisions → tasks" table below is this map in full: W1.2 on **D2** (the
   tombstone's text, and whether there is an alias at all) and W1.3 on **D11**
   (does `setup` create by default?); W3.1 and W3.2 on **D4**, W3.3 on **D7**,
   W3.4 on **D5**; W4.1, W4.3 and W4.5 on **D6**, W4.4 on **D3**; W5.1 on
   **D1**, **D5** and **D12**; W2.3 on **D8**; W0.2 and W2.6 on **D9**; W0.3,
   W6.1 and W6.2 on **D10**. W3.5 waits on **F12** rather than on a decision.
4. **The wizard never softens a confirmation.** `--yes`, `--non-interactive`,
   `--json`, a pipe and an agent session behave exactly as
   `internal/initflow`'s `YesCovers`/`CanConfirm` say today; W0.1's matrix is the
   test both front doors must pass, and no task may relax a row of it.
5. **The rename does not touch the cloud.** W2.x changes text, comments and
   descriptions only: no Terraform resource, variable name or path moves, so an
   apply on an existing installation sees no diff.
6. **`fugaro config init` stays.** W1.2 does not rename it, and the tombstone
   must not shadow it (F21); `TestDocsCheckoutCommandsExist` resolves both.
7. **The dated records keep the old name** (Convention 11), and W2.6's guard
   test fails if the exemption list is wrong in either direction: `datedRecords`
   is **one** constant, written in W0.2 and referenced — never restated — by
   every other task, because two copies of the list are two lists that drift.
8. **The billing picker runs only where `canType()` says yes.** Under `--json`,
   `--non-interactive`, no TTY or an agent session the run uses today's
   `guided()` line and lists nothing (D6, W4.3).

---

## Review Focus

A reviewer of **this pull request** (docs only) should check:

- That every path and symbol below exists on `main` at `82d366af`, or is marked
  **new**, and that no task claims a test protects what it does not.
- That no task takes a decision: the D-row each slice waits on is named in
  Global Constraint 3 and repeated in the task's own entry.
- That the rename's line is enforceable (W2.6's guard test fails in both
  directions), that `datedRecords` is stated in exactly **one** place (W0.2),
  and that the six roots it names match D9's answer — in particular that
  `docs/website/` is on the list and `docs/website/redesign/**`, which names
  the command zero times, is not.
- That Check 27's rewrite and Check 111 are the owner's to run, are sandbox
  only, never touch `belong`, and follow the register's form.
- That the levels contain no two writers of one file — check it against **`### The
  file lanes`**, which lists every file with more than one writer and its
  writers in level order; that every "Depends on" line is an edge below and
  every edge is a "Depends on" line (the lanes are deliberately *not* edges, and
  are listed separately for that reason); and that the critical path is the one
  named.
- That the refusals of F7/F8/F9 in the reconciliation have no task that
  weakens them.

A reviewer of a **task** reads its own "Reviewer" line.

---

## Decisions → tasks

| Decision | Where it lands |
|---|---|
| D1 (merge `update`/`upgrade`?) | Nothing here implements a merge; a "yes" adds a task to Slice 7 and changes W5.1's boundary text only |
| D2 (an `init` alias?) | W1.2 (the tombstone is the recommended answer; an alias is a different W1.2) |
| D3 (may the wizard enable the Cloud Billing API?) | W4.4 |
| D4 (the id default, the region) | W3.1, W3.2 |
| D5 (who enrols a repository) | W3.4, W5.1 |
| D6 (the billing list) | W4.1, W4.3, W4.5 |
| D7 (`--firebase` → `--firebase-project`) | W3.3, and W2.1 for the ~50 strings that say "re-run `init --firebase`" |
| D8 (the skill's name) | W2.3 |
| D9 (the rename's line) | W0.2, W2.6 |
| D10 (Check 27, 111) | W0.3, W6.1, W6.2 |
| D11 (create by default?) | W1.3 |
| D12 (`update --yes`'s confirmations) | W5.1, and W0.1's matrix row for `update` |
| F12 (two phases) | W3.5 |
| F16 (`update`'s refusals) | W5.1 |
| F21 (`fugaro config init`) | W1.2, Global Constraint 6 |
| O1 (no fourth verb) | W1.2 |
| O2 (`initflow` names no command) | W1.1 |

---

## File structure

### Created

| Path | Task | What it is |
|---|---|---|
| `internal/cli/setup_refusals_test.go` (**new**) | W0.1 (W5.1 adds `update`'s column) | The refusal matrix both front doors must pass |
| `internal/cli/docs_live_init_census_test.go` (**new**) | W0.2 (emptied by W2.6) | The live-name census and its frozen, shrinking list |
| `internal/cli/setup.go` (**new**) | W1.2 | The `setup` command: `Use`, `Short`, `Long`, the flag set, the run |
| `internal/cli/update.go` (**new**) | W1.2 | The `update` command, its stage set and its refusals |
| `internal/cli/retired.go` (**new**) | W1.2 | The hidden `init` tombstone: one message, two names, non-zero exit |
| `internal/cli/wizard_stage.go` (**new**) | W3.5 | The input phase as one `initflow.InputStage` whose `Missing()` names every flag |
| `internal/billing/accounts.go` (**new**) | W4.1 | The Cloud Billing client: open accounts only, ids and display names, a typed disabled error |
| `internal/billing/gcloud.go` (**new**) | W4.2 | The `gcloud billing accounts list` fallback behind an injected runner |
| `internal/cli/billing_prompt.go` (**new**) | W4.3 | The picker: numbering, the single-account default, the none case, the four conditions |
| `internal/e2e/update_test.go` (**new**, from `init_test.go`) | W5.2 | The 26 `TestInitRepo*` tests (of 27 `TestInit*`), renamed |
| `internal/cli/{install,wizard,converge}*.go` (**new**, from `init*.go`) | W2.7 | The pure rename, if the owner wants it (droppable) |

### Edited (the shared ones; every task lists its own)

| Path | Written by (level) |
|---|---|
| `docs/gcp-live-checklist.md` | W0.3 (L0), W2.6 (L5) |
| `internal/initflow/loop.go`, `loop_test.go` | W1.1 (L0) |
| `internal/cli/init.go`, `setup.go`, `update.go`, `retired.go`, `root.go`, `restore.go`, `restore_test.go`, `testdata/init_commands.golden`, `testdata/init_review.golden`, `testdata/init_retire.golden` | W1.2 (L1), W2.1 (L2), W5.1 (L7) |
| `internal/cli/init_stages.go` | W1.3 (L3), W5.1 (L7) |
| `internal/cli/init_project.go` | W1.3 (L3), W3.3 (L4), W4.3 (L9), W4.5 (L10) |
| `internal/cli/init_inputs.go` | W3.1 (L4), W3.2 (L5), W3.5 (L6), W4.3 (L9) |
| `internal/cli/init_firebase.go` | W3.3 (L4) |
| `internal/cli/init_repo_gate.go`, `init_repo_target.go` | W3.4 (L5) |
| `internal/infra/projectcreate.go` | W4.5 (L10) |
| `internal/preflight/google.go` | W4.4 (L10) |
| `README.md`, `docs/gcp-setup.md`, `docs/project-*.md`, `docs/multi-model.md`, `docs/git-providers.md`, `docs/dogfooding.md`, `docs/release.md`, `SECURITY.md`, `plugin/skills/**`, `.claude/skills/new-release/SKILL.md` | W2.3 (L3) |
| `website/data/install.yaml`, `website/src/pages/getting-started.astro`, `website/src/components/Changelog.astro`, `website/test/*.mjs`, `website/content/cli-reference.md`, `website/content/security.md` | W2.4 (L4) |
| `deploy/terraform/**/*.tf` (29 files naming the command, of 58 in the tree), `deploy/terraform/terraform.go`, `deploy/terraform/gcp/roots/repo/tests/testdata/README.md` | W2.5 (L2) |
| `internal/config/example.yaml`, `internal/config/example.go`, `schemas/fugaro.schema.json`, `images/verify-public.sh` | W2.6 (L5) |
| `internal/e2e/init_test.go` → `update_test.go` | W5.2 (L8) |

---

## Parallel execution

### The edges

Every line here is exactly one task's "Depends on" line below, and nothing else.
A task that also waits on a **decision** shows it in parentheses; the D-row each
one waits on is in Global Constraint 3, and a D-row is not a task and never
appears on the left of an arrow. A parenthesised D-row is the one the task
*waits* on; where a D-row instead gates a merge (W4.1 under D6), the task's own
"Depends on" line says so and it is not a wait.

```
W0.1 ← nothing     W0.2 ← nothing     W0.3 ← nothing (D10)   W1.1 ← nothing
W4.1 ← nothing     W4.2 ← W4.1        W2.5 ← nothing

W1.2 ← W1.1
W2.1 ← W1.2        W2.2 ← W1.2        W1.3 ← W1.2
W2.3 ← W2.1        W2.4 ← W2.1, W2.2
W2.6 ← W2.3, W2.4, W2.5, W0.2         W2.7 ← W2.6

W3.1 ← W1.3        W3.2 ← W1.3        W3.3 ← W1.3        W3.4 ← W1.3
W3.5 ← W3.1, W3.2, W3.3, W3.4         W5.1 ← W1.3        W5.2 ← W5.1

W4.3 ← W4.2, W3.5, W1.3
W4.4 ← W4.3        W4.5 ← W4.3

W6.1 ← W2.7, W3.5, W5.1, W5.2, W4.5 (owner; plus a release)
W6.2 ← W4.4, W4.5 (owner; plus a release)
```

### Levels

| Level | Tasks | Reviewer | Files (disjoint within the level) |
|---|---|---|---|
| **L0** | W0.1 the refusal matrix · W0.2 the census · W0.3 the checks written · W1.1 `initflow` names no command · W4.1 the billing client | standard · standard · standard · opus · **opus + mandatory** | `internal/cli/setup_refusals_test.go` · `internal/cli/docs_live_init_census_test.go` · `docs/gcp-live-checklist.md` · `internal/initflow/loop.go`, `loop_test.go` · `internal/billing/accounts.go` |
| **L1** | W1.2 the two commands, the tombstone, `restore` · W4.2 the gcloud fallback | opus · standard | `internal/cli/{init,setup,update,retired,root,restore}.go`, `restore_test.go`, `testdata/*.golden` · `internal/billing/gcloud.go`, `internal/gcpfake/billing.go` |
| **L2** | W2.1 the `internal/cli` strings · W2.2 the strings outside `internal/cli` · W2.5 Terraform | standard · standard · standard | ~30 `internal/cli/*.go` + the three goldens · `internal/{infra,budget,runner,managed,config,localcfg,projectpolicy,preflight,backend/gcp}/*.go` · `deploy/terraform/**` |
| **L3** | W1.3 `setup` is the wizard · W2.3 live markdown, README and the skills | opus · standard | `internal/cli/{setup,init_stages,init_project}.go` · `README.md`, `docs/`, `plugin/skills/**`, `.claude/skills/new-release/SKILL.md` |
| **L4** | W3.1 the id default · W3.3 `--firebase-project` · W2.4 the website and the generated reference | standard · standard · standard | `internal/cli/init_inputs.go` · `internal/cli/{init,init_firebase,init_project}.go` · `website/**` |
| **L5** | W3.2 the region and the Firestore location · W3.4 the first-repository question · W2.6 the census emptied and the tail | standard · standard · standard | `internal/cli/init_inputs.go` · `internal/cli/{init_repo_gate,init_repo_target,setup}.go` · `internal/config/{example.yaml,example.go}`, `schemas/`, `images/verify-public.sh`, `docs/gcp-live-checklist.md`, `docs_live_init_census_test.go` |
| **L6** | W3.5 the two-phase wizard stage | opus | `internal/cli/wizard_stage.go`, `init_inputs.go` |
| **L7** | W5.1 `update`'s stage set, refusals and non-interactive contract | opus | `internal/cli/{update,init_stages,init}.go`, `internal/cli/setup_refusals_test.go`, `docs/gcp-setup.md` (its `update` section) |
| **L8** | W5.2 the e2e tests, renamed | standard | `internal/e2e/init_test.go` → `update_test.go` |
| **L9** | W4.3 the picker prompt | **opus + mandatory** | `internal/cli/billing_prompt.go`, `init_project.go`, `init_inputs.go` |
| **L10** | W4.4 the API-enable refusal · W4.5 the comments that become false | **opus + mandatory** · standard | `internal/cli/billing_prompt.go`, `internal/preflight/google.go` · `internal/cli/init_project.go`, `internal/infra/projectcreate.go` |
| **L11** | W2.7 the pure file rename | standard | the `internal/cli/init*.go` → `install*.go` move only |
| **L12** | W6.1 Check 27 · W6.2 Check 111 | owner · owner | none (the owner records the FACT lines) |

**Widest levels: L0 (five) and L5 (three).** The levels obey one rule, and it is
the one a reviewer can check mechanically: **no task sits at or below the level
of another task it shares a file with.** That is what "no two writers of one
file in a level" means, and it is a *lower bound* — a task may sit later than
its edges and lanes require.

**W2.7 has a level to itself (L11), and that is exactly that rule.** Its edge,
`W2.7 ← W2.6`, puts it at L6 alongside W3.5, and W3.5 *edits*
`internal/cli/init_inputs.go` while W2.7 *renames* it — two writers of one file
in one level, which Convention 10 forbids. The rename wants to be last anyway
(one reviewable `git mv` commit, no content change), so it sits after L10, when
every behaviour and every edit to an `init*.go` file has landed, and the two
owner-run checks move to L12 behind it — which is where their other edge,
`W6.1 ← W2.7`, already asked them to be.

### The file lanes

The edges above are **not** the only ordering, and the levels are not derivable
from them alone. `internal/cli` is one lane: several tasks touch one file
without depending on each other, and the file forces the order. Each chain
below is one file, in level order, with the level in brackets; a task appears
once per file it writes.

```
internal/cli/init_inputs.go        W3.1 (L4) → W3.2 (L5) → W3.5 (L6) → W4.3 (L9)
internal/cli/init_project.go       W1.3 (L3) → W3.3 (L4) → W4.3 (L9) → W4.5 (L10)
internal/cli/init.go               W1.2 (L1) → W2.1 (L2) → W3.3 (L4) → W5.1 (L7)
internal/cli/init_stages.go        W1.2 (L1) → W1.3 (L3) → W5.1 (L7)
internal/cli/setup.go              W1.2 (L1) → W1.3 (L3) → W3.4 (L5)
internal/cli/update.go             W1.2 (L1) → W5.1 (L7)
internal/cli/billing_prompt.go     W4.3 (L9) → W4.4 (L10)
testdata/*.golden                  W1.2 (L1) → W2.1 (L2)
docs/gcp-live-checklist.md         W0.3 (L0) → W2.6 (L5)
docs_live_init_census_test.go      W0.2 (L0) → W2.6 (L5)
setup_refusals_test.go             W0.1 (L0) → W5.1 (L7)
docs/gcp-setup.md                  W2.3 (L3) → W5.1 (L7)

every internal/cli/*.go            ← W1.2 (L1) · W2.1 (L2) · W1.3 and later (L3+)
```

W2.1's line is a **sweep**, not a single file: it rewrites every
`internal/cli/*.go` that names the command, so it sits between W1.2 (L1) and
the first task after it (W1.3, L3) on every chain it touches — including the
ones where it is not the task that changes the behaviour.

Every file written by more than one task is on this list, and no chain goes
backwards. Three consequences a reviewer should spot: **W3.2 is at L5 because
W3.1 writes `init_inputs.go` at L4**, not because of an edge (its only edge is
`← W1.3`); **W3.4 is at L5** for the same reason via `setup.go`; and **W2.7 has
a level to itself at L11**, after the last writer of any `init*.go` file. The
thinness is the honest shape of this work — `internal/cli` is one lane, so the
tasks that reshape the wizard follow one another by file even where they do not
depend on each other logically.

### The critical path

**W1.1 → W1.2 → W1.3 → W3.1 → W3.2 → W3.5 → W4.3 → W4.4 → W6.2 (owner)** —
nine steps, the last owner-run. That is the billing leg, not the rename leg:
**W3.1 → W3.2 is the `internal/cli/init_inputs.go` lane, not an edge** — the
edges give `W3.2 ← W1.3` only — and W3.1 and W3.2 are sequential because they
write one file, not because either waits on the other. The rename leg is shorter — W1.2 → W2.1 → W2.3 → W2.6 → W2.7 → W6.1 —
and W2.5 (Terraform) is free to start at once, since it depends on nothing.
Two things shorten the rest: W4.1 (the billing client) depends on nothing and
can be built while the wizard is still being reshaped, and the owner-run checks
are scheduled as soon as their text is written (W0.3). **The release that
carries a CLI change is a hidden step before both checks**: they run a released
or owner-built binary.

### What must be sequential

1. **W0.1 and W0.2 before anything that renames or reshapes** — they are the
   pins, and a pin written after the change pins the wrong thing.
2. **W1.1 before W1.2.** `setup` and `update` cannot print a caller-supplied
   label until the loop accepts one.
3. **W1.2 before W1.3 and every W2.x and W3.x.** The strings being renamed and
   the questions being reworded both name a command that must exist.
4. **W3.5 before W4.3.** The picker is one of the wizard's questions, so the
   input phase has to exist.
5. **W4.1 and W4.2 before W4.3.** The picker asks a client that lists accounts.
6. **W2.1–W2.5 before W2.6**, which empties the census and flips its assertion
   to "`pendingRename` is empty and `retirementText` is exactly the four files
   whose subject is the retirement".
7. **W3.5 before W2.7**, even though nothing in W2.7 depends on it: W3.5 edits
   `internal/cli/init_inputs.go` and W2.7 renames it, and the rename waits until
   nothing else writes an `init*.go` file (L11, after L10).
8. **W4.3 before W4.4 and W4.5.** The refusal and the corrected comments are
   about code that must exist first.
9. **Every file lane, not only the edges above** (`### The file lanes`). Four
   orderings here are *file* ordering and have no edge behind them: **W3.1
   before W3.2** (`init_inputs.go`), **W1.3 before W3.4** (`setup.go`), **W2.3
   before W5.1** (`docs/gcp-setup.md`) and **W1.2/W2.1 before W3.3/W5.1**
   (`init.go`). A task that renames a file (W2.7) waits for every writer of it,
   which is why its level is last.

## Slice 0 — Verify first

*W0.1 and W0.2 are safe to start at once — two new test files, neither waiting
on a decision. W0.3 writes the checks' text, which is **D10's recommendation**,
so it waits for D10 to be answered.*

### Task W0.1: The refusal matrix, pinned — test

**Files.** `internal/cli/setup_refusals_test.go` (**new**).
**What it pins.** A table over the stages `internal/cli/init_stages.go`
registers, crossed with `internal/initflow/stage.go`'s `runCovered` map and its
`noYes` declarations, and five run modes — `--yes`,
`--non-interactive` (with and without `--yes`), `--json`, no terminal, and an
agent session — asserting for each the state the loop reaches and, specifically,
that **project creation, the billing link, the Firestore location and a billable
first image build are never covered**: each answers `needs-you` with a
`LeftKind` of `prompt`, and `initflow.YesCovers(name)` is false for the stages
that own them. It reuses today's harness (`internal/initflow/loop_test.go`'s
`TestYesDoesNotCoverCreateOrBilling` is the seed) and adds the agent and
`--json` columns, which no single test covers today.
**Red step.** None possible: this is a pin, and it passes on today's code **by
design**. The review checks it would fail if a row were relaxed, by relaxing one
locally and not committing it.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Refusal|YesCovers|Confirm' -count=1`.
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `cli: pin the confirmation matrix both front doors must pass (no task may relax a row)`

### Task W0.2: The rename census and its frozen list — test

**Files.** `internal/cli/docs_live_init_census_test.go` (**new**).
**What it pins.** D9's line, as a test that fails in **both** directions: it
walks the live roots (`README.md`, `CONTRIBUTING.md`, `docs/` minus the dated
roots, `plugin/`, `.claude/`, `website/`, `schemas/`, `scripts/`, `internal/`,
`images/`, `deploy/`), collects every file naming `fugaro init` or `init --repo`,
and asserts that set equals a checked-in `pendingRename` list plus the
`datedRecords` list. A new occurrence fails (the set grew); a renamed file that
was not removed from `pendingRename` fails (the set shrank). The
`datedRecords` list is **the six dated roots, and it is stated once, here**:
`docs/plans/`, `docs/releases/v`, `docs/design/`, `docs/credentials-service/`,
`docs/website/` and **`docs/setup-wizard/` itself** — matching
`plugin/readme_test.go`'s `TestReadmeNamesNoRetiredSkill` `skip` regex in
spirit. Every other task that mentions the exemption list repeats this one
verbatim rather than paraphrasing it, because a second copy is a second list
that can disagree: `docs/website/` is a whole second artifact's record
(`docs/website/README.md`, decisions D90–D109, dated 2026-10-16) and its
`reconciliation.md` and `implementation-plan.md` name the command 3 times
between them, while `docs/website/redesign/**` names it **zero** times — so the
redesign folder is not the thing to exempt, the folder that holds the plan is.
The handful of *generated* files (`website/content/cli-reference.md`,
`website/content/security.md`) are asserted to be in `pendingRename` and are
removed from it by W2.4, which regenerates them rather than editing them.
**A second exemption class, `retirementText`, is needed for the flip to be
achievable**, and it is not a judgement call: `internal/cli/retired.go` and its
test, `internal/cli/testdata/init_retire.golden` and this census file itself
name `fugaro init` **as the retired verb** — that is their whole job, and no
rename can remove it. They are listed separately, so "the live tree names
`fugaro init` nowhere" stays a claim with teeth: W2.6 asserts `pendingRename` is
empty and `retirementText` is exactly those four paths.
**Red step.** None: passes today with `pendingRename` = today's census
(410 non-test Go lines across 107 files, 378 test lines across 106 files, 111
markdown files outside this folder, 29 Terraform files naming the command of
58). W2.6 **flips the last assertion to `len(pendingRename) != 0 → fatal`**,
and that is where the pin bites.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Census|RetiredInit' -count=1`.
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `cli: the census of every file that still names fugaro init, and the line the rename must not cross`

### Task W0.3: Checks 27 and 111, written into the register — docs

**Files.** `docs/gcp-live-checklist.md` (Check 27's section replaced in place;
Check 111's register row and section added; the paragraph that records reserved
numbers gains 111's claim).
**What the owner types.** Check 27: the hard-coded project guard (`${P:?}` on
every `gcloud`, installation `fugaro` only, **never `belong`**, a throwaway
project and a sandbox repository); the commands in the summary's table above,
each a line the owner can paste; `FACT:` lines for what the wizard asked, what
the picker listed (how many accounts, and whether the API or gcloud answered),
and the second `fugaro update`'s "No changes"; then the Restore (delete the
throwaway project, the bucket, the repository's jobs) whose final assertion
reads `0`. Check 111: the five modes, the guided line each prints, and a `FACT:`
line that nothing was enabled and no account list was printed.
**Tests.** None (a document). **Red step.** n/a (docs).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Checklist' -count=1`.
**Depends on.** **D10's answer** — the task *is* D10's recommendation (rewrite
Check 27 in place, take 111), so writing it takes the decision rather than
assuming it. It depends on no *other task*; the checks describe the target state
and are **not run** until W6.1 and W6.2. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Check 27 rewritten for fugaro setup, and Check 111 for the billing picker's refusals`

---

## Slice 1 — The two front doors

### Task W1.1: `initflow` stops naming a command — opus

**Files.** `internal/initflow/loop.go`, `internal/initflow/loop_test.go`.
**What changes.** `Options.Program` (**new** field; `""` means the current
defaults, so the package stays usable and no test that does not care has to
change). `loop.view`'s header (`"fugaro init: project %s (GCP %s, %s)"`),
`loop.finish`'s `"fugaro init: result"`, `loop.fail`'s `"fix what the error says
and rerun fugaro init: it resumes at <stage>"` and `loop.cancelled`'s
`"rerun fugaro init: it resumes at <stage>"` use it. The CLI sets it in
`internal/cli/init_stages.go` from whichever front door is running.
**Why it is a task of its own.** The loop is printed by both doors and cannot
know which one it is (O2 in the reconciliation). A naive find-and-replace
produces a package that names a command that does not exist.
**Tests.** `TestLoopNamesTheCallerProgram` (**new**: `Program: "fugaro update"`
appears in the header, the result line and the failure fix);
`TestLoopDefaultProgramIsUnchanged` (pins the `""` behaviour, so a caller that
forgets to set it is not silently broken); `internal/initflow/loop_test.go`'s
existing text assertions updated.
**Red step.** `TestLoopNamesTheCallerProgram` first (no `Program` field exists).
**Verification.** `GOFLAGS=-p=2 go test ./internal/initflow/... -count=1`.
**Depends on.** Nothing. **Reviewer.** opus.
**Commit.** `initflow: the loop prints the program its caller names, not a hard-coded fugaro init`

### Task W1.2: `setup`, `update`, the tombstone, `restore`, the goldens — opus

**Files.** `internal/cli/setup.go` (**new**), `update.go` (**new**),
`retired.go` (**new**), `init.go` (`newInitCmdWith` becomes the shared
converge-command constructor; its ~210-line `Long` is split into `setup`'s and
`update`'s), `root.go` (`root.AddCommand` registers `setup`, `update`, the
hidden tombstone and no `init`), `restore.go` (`restoreInitCmd` repointed at the
shared constructor, and its printed `"fugaro init --create-project --managed
uses: fugaro init %s"` reworded), `restore_test.go`
(`TestRestoreSeamsAreProductionsFunctions`'s pointer pair updated),
`docs_base_test.go` (its `"fugaro init": newInitCmd` map key), the three
`testdata/*.golden` files regenerated with their flags.
**What changes.** `fugaro setup` = the wizard; `fugaro update` = the re-converge;
both take the flags that belong to them, and a flag that only makes sense in one
(e.g. `--create-project`, `--link-billing`) is refused in the other with a
message naming the right one. The tombstone: a **hidden** `init` command, no
flags of its own, that applies nothing and exits non-zero saying `` `fugaro init`
is now `fugaro setup` (first run) and `fugaro update` (after a CLI upgrade) ``.
D2 is the decision here: the recommendation is the tombstone, not an alias, and
an alias is a different version of this task.
**Interfaces.** `setup` keeps `addCloudFlags` (`--config`, `--project`,
`--gcp-project`, `--region`) and the installation flags; `update` keeps them
minus everything that creates. Exit codes are `initflow.Result.ExitCode()`'s,
unchanged. `--json` on both prints `Result.JSON()`.
**Tests.** `TestInitIsRetiredAndSaysBothNames`, `TestRetiredInitAppliesNothing`
(no client is constructed; a fake that fails on any call stays untouched),
`TestSetupAndUpdateAreRegistered`, `TestSetupRefusesAnUpdateOnlyFlag` and its
mirror, `TestConfigInitIsUntouched` (F21: `fugaro config init` still resolves
and still writes the minimal config), `TestUpdateRefusesAnArgument` (`update`
takes no path, unlike `upgrade [PATH...]`), the golden tests.
**Red step.** `TestInitIsRetiredAndSaysBothNames` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Setup|Update|Retired|ConfigInit|Golden|Output' -count=1`, then `fugaro verify test`.
**Depends on.** W1.1. **Reviewer.** opus.
**Commit.** `cli: fugaro setup and fugaro update replace fugaro init; init is a tombstone naming both`

### Task W1.3: `setup` is the wizard — the create/adopt question, the stage set, every question's flag — opus

**Files.** `internal/cli/setup.go` (grows its real `Long`, its question list and
its run), `internal/cli/init_stages.go` (which stages `setup` registers:
`projectStage` **always**, `computeStage` still only under
`--provision-batch`), `internal/cli/init_project.go` (the stage answers
create-or-adopt from the collected answer instead of a flag).
**What changes (F24, D11).** `setup`'s first question is the project:
`` `fugaro-<name>` (new) `` is the default answer, "an existing project to
adopt" the second. The typed banner for creation is unchanged, and
`--create-project` remains for a scripted run — a flag for **every** question,
so a script never needs a terminal for anything but the three typed
confirmations. When the installation already exists (`installationExists`,
`loadInitConfig`), `setup` prints one line — "this installation is already set
up" — keeps the existing project, and converges as `update` does rather than
offering to create a second one. **D11 is the decision.**
**Tests.** `TestSetupAsksCreateOrAdoptWithCreateDefault`,
`TestSetupAdoptsWhenTheUserTypesAnExistingID`,
`TestSetupCreatesOnlyWithTheTypedConfirmation` (and that `--yes` does not
cover it — W0.1's matrix gains a row), `TestSetupOnAnExistingInstallationConvergesAndSaysSo`,
`TestEveryWizardQuestionHasAFlag` (**new**, a table: question → flag → the
`Missing()` name `--non-interactive` prints, so the contract is enforced
mechanically), `TestSetupOutsideACheckoutSkipsTheRepositoryQuestion` (pinned
here as pass-through; W3.4 owns the wording).
**Red step.** `TestEveryWizardQuestionHasAFlag` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Setup|Wizard|Project' -count=1 && fugaro verify test`.
**Depends on.** W1.2, and D11's answer. **Reviewer.** opus.
**Commit.** `cli: fugaro setup asks whether to create the project, defaults to creating it, and degrades to update on an installation that exists`

---

## Slice 2 — The rename

*The mechanical majority. Each task leaves the tree green: a partial rename is a
failing pin, and the pin's failure names the next sentence.*

### Task W2.1: The `internal/cli` strings — standard

**Files.** ~30 `internal/cli/*.go`: `doctor.go`, `doctor_bucket.go`,
`config_list.go`, `exec.go`, `image.go`, `image_refresh.go`,
`image_refresh_rules.go`, `image_refresh_steps.go`, `imagecheck.go`, `init.go`,
`init_adopt.go`, `init_anchor.go`, `init_firebase.go`, `init_fugaroyaml.go`,
`init_images.go`, `init_inputs.go`, `init_plugin_stage.go`, `init_project.go`,
`init_repo_gate.go`, `init_repo_stage.go`, `init_repo_target.go`, `init_retire.go`
(`retireNextCommand`'s value is the literal `"fugaro init --repo . --retire"`),
`init_review.go`, `init_stages.go`, `initsecrets.go`, `launch_budget.go`,
`operator_write.go`, `project.go`, `project_cmd.go`, `recipes_skew.go`,
`report.go`, `sharedcfg.go`, `upgrade.go`, `upgrade_cloud.go`, `validate.go`,
`budget.go`, `cloud.go`; the three goldens regenerated; the ~90
`internal/cli/*_test.go` assertions that read those strings updated.
**Which name each string gets (F19, D7).** This is judgement, not search and
replace, and the task's review is about that:
- "set up an installation / first run here" → `` `fugaro setup` ``
  (`config_list.go`'s `printConfigList`, `init_inputs.go`'s `missingInput`,
  `doctor.go`'s `Fix: "run fugaro init"`, `doctorGitHubApps`,
  `init_anchor.go`'s hints, and the `errNoState` family in `init.go`).
- "re-converge the installation / this repository after an upgrade" →
  `` `fugaro update` `` — and, where the string means *this checkout's
  repository*, `` `fugaro update` in the checkout ``. That covers the ~50
  `re-run 'fugaro init --firebase'` strings (`doctor.go`'s `doctorRulesSkew` and
  `doctorPolicyChecks`, `project_cmd.go`'s `rulesMarker*Text`,
  `launch_budget.go`, `history_rollover.go`, `report.go`, `budget.go`,
  `cloud.go`, `image_refresh*.go`) and the `init --repo` family
  (`exec.go`'s `oldJobEnvReason`, `imagecheck.go`, `checkjob.go`'s callers,
  `init_retire.go`).
- `--anchor` and `--publish-config` strings (`.claude/skills/new-release`,
  `docs/release.md`, `init_fugaroyaml.go`) → `fugaro update --anchor` /
  `fugaro update --publish-config`: they are re-converges, not first runs.
  **The reviewer's one question per string is "would a first-time user or an
  upgrading user see this?", and the answer picks the verb.**
**Tests.** The existing assertions, updated; plus
`TestEveryLeftForYouNamesACommandThatExists` (**new**: every command string in
the regenerated `init_commands.golden` and `init_review.golden` resolves against
`NewRootCmd()`, which is `internal/cli/docs_checkout_test.go`'s
`TestDocsCheckoutCommandsExist` idea applied to the goldens).
**Red step.** `TestEveryLeftForYouNamesACommandThatExists` — it fails today for
`fugaro init`'s two successors' absence in the goldens' vocabulary, and is the
cheapest way to see every string that has to move.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1` (the whole
package, since this task touches most of it) and `fugaro verify test`.
**Depends on.** W1.2, and D7's answer. **Reviewer.** standard.
**Commit.** `cli: every message names fugaro setup, fugaro update or fugaro upgrade, whichever the reader is doing`

### Task W2.2: The strings outside `internal/cli` — standard

**Files.** `internal/infra/{discover,discover_firebase,firebase,firebasedb,hardening,installation,projectcreate,projectjobs,readiness,checkjob,tf/env,tf/managed_cover}.go`,
`internal/budget/{rollover,registry,session}.go`,
`internal/runner/{project,projectpolicy,projectjob,halt,gateway,budget_session}.go`,
`internal/managed/budget.go`, `internal/localcfg/{localcfg,select}.go`,
`internal/config/{config,providers,routing}.go`,
`internal/projectpolicy/{read,profiles}.go`,
`internal/preflight/google.go`, `internal/backend/gcp/run.go`, and the
comment-only mentions listed in the reconciliation's §4 tail.
**Which name each gets.** The same rule as W2.1. Note two families: the
*operator* strings ("an operator runs fugaro init (0.7.0 or later)") become
`` `fugaro update` `` — the operator's job after an upgrade — and the
*repository* strings ("run fugaro init --repo in the checkout") become
`` `fugaro update` in the checkout ``. `internal/infra/projectcreate.go`'s
`"rerun fugaro init --create-project"` becomes `"rerun fugaro setup"`, and its
head comment is W4.5's.
**Tests.** The ~120 test files asserting those strings (the reconciliation's
survey lists them by function); every one is updated in this commit.
**Red step.** `GOFLAGS=-p=2 go test ./internal/infra/... ./internal/budget/... ./internal/runner/... -count=1` before the edit — it is green; after the edit but before the test text, it names each assertion.
**Verification.** `GOFLAGS=-p=2 go test ./internal/... -count=1` and `fugaro verify test`.
**Depends on.** W1.2. **Reviewer.** standard.
**Commit.** `infra, budget, runner: the messages name fugaro update or fugaro setup, not fugaro init`

### Task W2.3: Live markdown, README and the skills — standard

**Files.** `README.md` (16), `docs/gcp-setup.md` (113 — including its title
`# GCP setup with \`fugaro init\`` and its
`## The short path: \`fugaro init\`, then \`/fugaro:setup\`` section),
`docs/project-layer.md`, `docs/project-claude-accounts.md`,
`docs/multi-model.md`, `docs/git-providers.md`, `docs/dogfooding.md`,
`docs/release.md`, `SECURITY.md`, `docs/coordinator/**`, `docs/mobile/**`,
`docs/in-job-review/**`, `plugin/skills/setup/SKILL.md` and
`plugin/skills/setup/reference/**`, `plugin/skills/upgrade/**`,
`.claude/skills/new-release/SKILL.md`; and the pins updated in the same commit:
`plugin/readme_test.go`'s `TestReadmeGettingStartedIsTwoSteps` (step 1,
"`fugaro init` again"), `plugin/setup_skill_test.go`'s five tests,
`plugin/skills_lint_test.go`'s `userRunsAllowed` list and its `--yes` rule,
`internal/cli/docs_project_wide_git_test.go` (the literal "Init's prompts (step
9) have no `--repo` to pass"), `internal/cli/docs_upgrade_test.go`,
`internal/cli/docs_base_test.go`, and the ≈40 `docs_*_test.go` files whose
sentences move.
**What changes beyond the word (F17, D8).** The README's two steps stay two
steps, with step 1 now `` `fugaro setup` ``; `docs/gcp-setup.md`'s title and
short path follow; the skill keeps its name and its order (it runs **after** the
wizard) and its opening line says which one it is — "`fugaro setup` did the
cloud; this skill writes `fugaro.yaml` and opens the pull request" — and every
`fugaro init --repo --plan-only` / `fugaro init --repo --retire` it hands the
user becomes `fugaro update --plan-only` / `fugaro update --retire`.
**What does not change.** Every sentence that is a *claim about behaviour*
("`--yes` never covers creating a project") stays true and stays; only the
command's name moves. The dated records are untouched (Convention 11).
**Red step.** `GOFLAGS=-p=2 go test ./plugin/... ./internal/cli -run 'Docs|Lint|Readme|Skill' -count=1` — red throughout, naming each pinned sentence.
**Verification.** Convention 1's command, then `fugaro verify test`.
**Depends on.** W2.1. **Reviewer.** standard.
**Commit.** `docs, README and the plugin skills: fugaro setup and fugaro update replace fugaro init`

### Task W2.4: The website and the generated reference — standard

**Files.** `website/data/install.yaml` (its `plugin` path's three commands:
`fugaro init` → `fugaro setup`; `/fugaro:setup` unchanged; `fugaro upgrade
--local` unchanged), `website/src/pages/getting-started.astro` (3 mentions, the
seven-step `#setup` `<ol>` collapsing to about three, and the anchor fragment
`the-short-path-fugaro-init-then-fugarosetup`, which appears **twice** — the
slug keeps the hyphen inside `fugaro-init` that the name itself does not have,
because it is the heading's words joined; the heading and both fragments move
together, and `website/test/*.mjs` resolves them),
`website/src/components/Changelog.astro`, `website/test/docs-hub.mjs`,
`website/test/caps-and-costs.mjs`, `website/content/cli-reference.md`
(**regenerated** with `go run ./internal/clidoc`, never hand-edited),
`website/content/security.md` (**regenerated** by `internal/clidoc/secdoc.go`
from `SECURITY.md`).
**What changes beyond the word.** The Getting started page's seven steps become
the brief's shorter sequence — one command, and the wizard asks the rest — and
its prose about passing flags goes. `website/data/install.yaml` is pinned
byte-for-byte by `internal/cli/docs_website_install_test.go`, so the pin moves in
this commit.
**Tests.** `internal/cli/docs_website_install_test.go`,
`internal/clidoc/cliref_test.go`'s `TestCliReferenceIsCurrent`,
`internal/cli/docs_website_cliref_test.go`'s
`TestCliReferenceNamesEveryCommandAndFlag` / `TestCliReferenceInventsNothing`
(the reference matches the cobra tree in **both** directions — this is the test
that proves the rename reached the help text),
`internal/cli/docs_website_security_test.go`.
**Red step.** `go test ./internal/clidoc -run CliRef -count=1` — it fails until
the generator is rerun after W1.2's cobra changes, and its diff is the review.
**Verification.** `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/clidoc ./internal/cli -run 'CliRef|Website|Docs' -count=1 && (cd website && npm ci && npm test)` — the website's own tests, which resolve the anchor fragments.
**Depends on.** W2.1, W2.2. **Reviewer.** standard.
**Commit.** `website: the getting-started sequence is fugaro setup, and the CLI reference is regenerated`

### Task W2.5: Terraform, the schema and the example config — standard

**Files.** 29 of the tree's 58 `deploy/terraform/**/*.tf` files — those whose
variable and output
`description`s naming the command, e.g.
`deploy/terraform/gcp/modules/repo/variables.tf`, `modules/installation/*.tf`,
`modules/firebase/*.tf`, `modules/workflow/*.tf`, `modules/project/job.tf`,
`roots/*/{main,variables,outputs}.tf`), `deploy/terraform/terraform.go`'s
package comment, `deploy/terraform/gcp/roots/repo/tests/testdata/README.md`,
`internal/config/example.go`'s `exampleProjectLines` +
`internal/config/example.yaml` (the comment line naming `--config-only`'s
writer), `schemas/fugaro.schema.json` (`gcp_project`'s `description`), and
`images/verify-public.sh`.
**Why its own task.** These are not fugaro's own output: they are
`terraform plan` text and public artifacts, independently reviewable, and they
depend on nothing in Slice 1 — which is why this task starts first and can merge
before the CLI changes.
**Constraint.** **Global Constraint 5**: text, comments and descriptions only.
No resource, variable, output or path moves, so an apply on an existing
installation produces no diff. The one thing to check is whether a test
byte-compares a `description`; the red step finds out.
**Tests.** `GOFLAGS=-p=2 go test ./deploy/... ./schemas/... ./internal/config/... -count=1`
(the Terraform golden and schema-corpus tests) and
`internal/cli/docs_project_layer_test.go` / `validate_test.go` for the example.
**Red step.** `GOFLAGS=-p=2 go test ./deploy/... ./internal/config/... -count=1` before the edit — any failure names a compared description, which is then changed on purpose.
**Verification.** As the red step, after the edit; plus
`terraform fmt -check -recursive deploy/terraform` (a description edit must not
reflow).
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `terraform, schema and the example config: the descriptions name fugaro setup or fugaro update; no resource moves`

### Task W2.6: The census emptied, and the tail — standard

**Files.** `internal/cli/docs_live_init_census_test.go` (W0.2's file: the final
assertion flips to "`pendingRename` is empty, and `retirementText` is exactly
the four files whose subject is the retirement"), the leftovers the other rename
tasks did not own — `docs/gcp-live-checklist.md`'s remaining prose mentions
(other than Checks 27 and 111, which are W0.3's and already name the new
commands). The exempt roots are **exactly W0.2's six, no more and no fewer** —
`docs/plans/`, `docs/releases/v`, `docs/design/`, `docs/credentials-service/`,
`docs/website/` and `docs/setup-wizard/` — and this task does not restate them
from memory: the guard's `datedRecords` is one constant, and W0.2 is where it is
written. `docs/setup-wizard/` is among them because it is where the retirement
is described (75 occurrences, four documents). `docs/website/redesign/**` is
**not** on the list, and that is deliberate: it names the command **zero**
times, and an exemption for a root with no occurrences is exactly the "wrong in
one direction" Global Constraint 7 forbids. The root that does name it,
`docs/website/`, is on the list.
**Tests.** `internal/cli/docs_live_init_census_test.go` — the flip **is** the
test.
**Red step.** Flip the assertion first: it fails and prints the files still
naming `fugaro init`, which is this task's to-do list.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run Census -count=1 && GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc ./plugin/... -run 'Docs|Lint|CliRef|Readme|Skill' -count=1` and `fugaro verify test`.
**Depends on.** W2.3, W2.4, W2.5, W0.2. **Reviewer.** standard.
**Commit.** `docs: the live tree names fugaro init nowhere; the census is empty and the guard now enforces it`

### Task W2.7: The file rename, last and pure — standard *(droppable)*

**Files.** `git mv` only: `internal/cli/init.go` → `install.go`,
`init_stages.go` → `install_stages.go`, `init_project.go` →
`install_project.go`, `init_inputs.go` → `wizard_inputs.go`, `init_stages.go`'s
siblings as the owner prefers, and the corresponding `init*_test.go` files.
**Why last.** Doing it first would put a rename in every subsequent diff; doing
it last makes one reviewable commit. It changes **no content**, so it can be
dropped entirely without affecting any other task — the two front doors live in
`setup.go` and `update.go` and the engines' file names are cosmetic. Its level
is **L11, on its own**: its only logical edge is `W2.7 ← W2.6` (L5), which
would place it at L6 next to W3.5 — and W3.5 edits `init_inputs.go` while this
task renames it, two writers of one file in one level. Renaming after L10, when
every edit to an `init*.go` file has landed, keeps Convention 10 true.
**Tests.** None new: the whole suite must pass **unchanged**, which is the proof
the move was pure.
**Red step.** n/a (no behaviour). **Verification.** `git show --stat` reports
renames, not add/delete pairs; `GOFLAGS=-p=2 go test ./internal/cli/... -count=1`.
**Depends on.** W2.6. **Reviewer.** standard.
**Commit.** `cli: the install engine's files are named for what they do (pure rename, no content change)`

---

## Slice 3 — The wizard's questions

### Task W3.1: The project id's default — standard

**Files.** `internal/cli/init_inputs.go` (`gatherInputs`'s first question,
`suggestGCPProject`, `sanitiseName`/`suggestName`, a new
`defaultGCPProject(name)`), `internal/cli/init_inputs_test.go`.
**What changes (F3, D4).** The default answer is `"fugaro-" + suggestName(name)`.
`suggestGCPProject()`'s credential-derived project (GOOGLE_CLOUD_PROJECT or the
ADC's `quota_project_id`, never gcloud's default) is offered as the **second**
choice when it differs, with one line saying why ("you are already working in
`<id>`"). The chosen id is **checked before any create** — availability and
ownership — the way the typed banner already requires. Region is untouched here
(W3.2).
**Interfaces.** The question's shape: `prompt(label, def, valid)` keeps its
three-tries contract; a second suggestion is a new parameter or a
`promptChoice` helper — the task's call, and the review asks that the
`--non-interactive` refusal still names **every** missing flag in one error.
**Tests.** `TestProjectIDDefaultIsFugaroName`, `TestProjectIDSuggestionIsTheCredentialProjectSecond`,
`TestProjectIDIsCheckedBeforeAnyCreate`, `TestProjectIDDefaultIsSanitised`
(a project name with capitals, spaces or a leading digit yields a legal id),
`TestProjectIDDefaultIsNotTakenWhenItExists` (the wizard re-asks rather than
failing), `TestGatherInputsNonInteractiveNamesEveryFlag`.
**Red step.** `TestProjectIDDefaultIsFugaroName` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Input|ProjectID|Prompt' -count=1`.
**Depends on.** W1.3, and D4's answer. **Reviewer.** standard.
**Commit.** `cli: the project id defaults to fugaro-<project name>, checked before any create`

### Task W3.2: The region question, and the Firestore location kept apart — standard

**Files.** `internal/cli/init_inputs.go`, `internal/cli/init_inputs_test.go`,
`internal/cli/init_firestore_test.go`.
**What changes (C1).** The region question keeps `const defaultRegion =
"us-east5"` as its default, is asked once, and its label says what it is the
region **of** (the installation's jobs and scheduler). The Firestore location
question (`init_firebase.go`, typed) is untouched, and the two are never derived
from one another: a test fails if a single expression ever serves both.
**Tests.** `TestRegionDefaultIsUsEast5`, `TestRegionQuestionIsNotTheFirestoreQuestion`
(a golden of both prompts, so the two labels cannot converge),
`TestRegionCannotChangeTheFirestoreLocation` (the Firestore prompt still comes
from `infra.FirestoreLocation` and is still typed).
**Red step.** `TestRegionQuestionIsNotTheFirestoreQuestion` first (the label
does not exist yet).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Region|Firestore|Input' -count=1`.
**Depends on.** W1.3, and D4's answer. **Reviewer.** standard.
**Commit.** `cli: the region question says what it is the region of, and never stands in for the Firestore location`

### Task W3.3: `--firebase` becomes `--firebase-project` — standard

**Files.** `internal/cli/init.go` (the flag's registration and usage text),
`internal/cli/init_firebase.go`, `internal/cli/init_project.go`
(`checkCreateProject`'s pairing rule), `internal/infra/firebase.go`'s
`CheckFirebaseProject` text, and their tests.
**What changes (C2, F4, D7).** One id by default: `--firebase-project` is
advanced, defaults to the GCP project id, and the pairing rule keeps refusing a
created project whose Firebase project differs unless the flag says so. The old
name is not kept as an alias (D2's reasoning applies to a flag too); a scripted
run that passes `--firebase` gets an unknown-flag error naming
`--firebase-project`. The ~50 "re-run `fugaro init --firebase`" strings are
W2.1's and W2.2's, not this task's — this task changes the **flag**, not the
prose.
**Tests.** `TestFirebaseProjectDefaultsToTheGCPProject`,
`TestFirebaseProjectPairingRuleIsUnchanged` (port `init_project_test.go`'s
existing pairing cases onto the new name), `TestFirebaseProjectIsRefusedWithoutCreateProject`,
`TestOldFirebaseFlagIsGone` (the usage text has no `--firebase`, and the
unknown-flag error names the new one).
**Red step.** `TestFirebaseProjectDefaultsToTheGCPProject` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Firebase|CreateProject' -count=1`.
**Depends on.** W1.3, and D7's answer. **Reviewer.** standard.
**Commit.** `cli: --firebase-project, defaulting to the project's own id; the separate Firebase project stays an advanced flag`

### Task W3.4: The first-repository question — standard

**Files.** `internal/cli/setup.go`, `internal/cli/init_repo_gate.go`
(`checkoutNote`, `unknownDetail`), `internal/cli/init_repo_target.go`
(`resolveRepoTarget`'s four refusal texts), and their tests.
**What changes (F14, D5).** Inside a checkout, `setup` asks one extra question —
"onboard `<owner/name>`?" — defaulting to yes, and uses the existing repository
stage; outside a checkout it skips the question and prints one line saying how to
enrol one later. `update` never enrols silently (W5.1 owns that side). The four
`resolveRepoTarget` refusals keep their substance and change their verb.
**Tests.** `TestSetupAsksToOnboardTheCheckoutsRepository`,
`TestSetupOutsideACheckoutSaysHowToEnrol` (the text names `fugaro setup`),
`TestSetupRefusesToOnboardWithoutTheTypedApply` (the run's one confirmation
covers it; a billable build still does not),
`TestSetupOnboardingIsASeparateAnswerFromTheProjectQuestion`.
**Red step.** `TestSetupAsksToOnboardTheCheckoutsRepository` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Repo|Checkout|Setup' -count=1`.
**Depends on.** W1.3, and D5's answer. **Reviewer.** standard.
**Commit.** `cli: setup asks whether to onboard the checkout's repository, and says how to enrol one later when run outside it`

### Task W3.5: The two-phase wizard — every question before the first apply — opus

**Files.** `internal/cli/wizard_stage.go` (**new**),
`internal/cli/init_inputs.go` (the collected answers become one value the stage
carries), `internal/cli/setup.go` (the stage is registered first),
`internal/cli/init_stages.go`.
**What changes (F12).** `initflow`'s canonical order is unchanged and decides
the **apply** order; the wizard's **questions** are all asked up front, before
`initflow.Run` starts applying. The new `wizardStage` implements
`initflow.InputStage`: its `Missing()` returns every question's flag (so
`--non-interactive` produces one error naming all of them, exactly as today's
`gatherInputs` does), its `Check` is `done` once the answers are complete and
`needs-you` before, and its `Left()` is the unanswered question with its
`LeftKind` — `prompt` at a terminal, `command` otherwise. This is the task that
makes the wizard a wizard rather than a sequence of interruptions between
applies.
**Tests.** `TestWizardCollectsEveryAnswerBeforeTheFirstApply` (a fake stage
records the order and asserts no apply precedes the last answer),
`TestWizardStageIsAnInputStage` (its `Missing()` is reached by
`initflow.Run`'s non-interactive check),
`TestWizardNeedsYouNamesTheQuestionAndAKind`,
`TestWizardUnderJSONAsksNothingAndNamesEveryFlag`,
`TestWizardResumesAtTheUnansweredQuestion` (an interrupted run's `Left()` is
where it stopped).
**Red step.** `TestWizardCollectsEveryAnswerBeforeTheFirstApply` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/initflow -run 'Wizard|Input|Setup' -count=1 && fugaro verify test`.
**Depends on.** W3.1, W3.2, W3.3, W3.4. **Reviewer.** opus.
**Commit.** `cli: the wizard asks every question before the loop applies anything`

---

## Slice 4 — The billing picker

*D6 is the decision; D3 is the API-enable question. Nothing here enables an API
or lists an account until the owner answers both.*

### Task W4.1: The Cloud Billing client — **opus + mandatory** — SECURITY-SENSITIVE *(privacy)*

**Files.** `internal/billing/accounts.go` (**new**),
`internal/billing/accounts_test.go` (**new**).
**What it is.** `List(ctx, credentials) ([]Account, error)` over the Cloud
Billing API's `billingAccounts.list`, using the user's ADC, with `Account{ID,
DisplayName, Open}`. **Open accounts only** (a closed account cannot be linked),
`billingAccounts.list` reached through the existing cloud clients' pattern, a
typed `ErrDisabled{QuotaProject, EnableCommand}` when the API is off (F6's
`internal/preflight/google.go` wording is the model), and a typed
`ErrNoPermission` when the caller lacks `billing.accounts.list`. **No caching, no
persisting, no logging of the list.** The package's head comment states what it
is allowed to do and that it is called from exactly one place (W4.3) under
`canType()`'s four conditions — the reversal of `internal/infra/projectcreate.go`'s
rule is written down here, and W4.5 fixes the comment that becomes false.
**Tests.** With `internal/gcpfake`'s billing fake and a fake credentials source:
`TestListReturnsOpenAccountsOnly`, `TestListReturnsIDAndDisplayName`,
`TestListRefusedWhenTheAPIIsDisabled` (the error carries the quota project and
the `gcloud services enable` line), `TestListRefusedWithoutThePermission`,
`TestListIsNeverRetriedIntoASecondCall`, `TestListLogsNoAccount` (a captured
logger has no id in it). Account ids are built at run time (Convention 4).
**Red step.** `TestListReturnsOpenAccountsOnly` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/billing/... -count=1`.
**Depends on.** Nothing; D6's answer gates the merge, not the writing. **Reviewer.** opus + **mandatory independent review** (it is the one new surface that reveals what a user can reach).
**Commit.** `billing: list the open billing accounts a user can use, ids and display names only`

### Task W4.2: `gcloud billing accounts list` as the fallback — standard

**Files.** `internal/billing/gcloud.go` (**new**), `internal/billing/gcloud_test.go`
(**new**), `internal/gcpfake/billing.go` (the fake's list side, if the tests
share it).
**What it is.** A fallback that runs `gcloud billing accounts list` through an
**injected** runner (a `func(ctx, args...) ([]byte, error)`, never a direct
`exec`), parses the table, and returns the same `Account` shape — so the picker
has one input type and the tests run no binary. The wizard prefers the API and
falls back only when it is unavailable *and* `gcloud` is on PATH, and **says
which one answered** (the reconciliation notes the two can disagree).
**Tests.** `TestGcloudFallbackParsesOpenAccounts`,
`TestGcloudFallbackUsedOnlyWhenTheAPIIsUnavailable` (an API success is never
followed by a gcloud call),
`TestGcloudFallbackAbsentSaysWhichLineToRun` (neither source → today's
`guided()` text),
`TestGcloudOutputNeverStored` and `TestGcloudArgsAreOnlyTheListCommand`.
**Red step.** `TestGcloudFallbackParsesOpenAccounts` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/billing/... -count=1`.
**Depends on.** W4.1. **Reviewer.** standard.
**Commit.** `billing: a gcloud billing accounts list fallback behind an injected runner`

### Task W4.3: The picker, and the four conditions it runs under — **opus + mandatory** — SECURITY-SENSITIVE *(privacy)*

**Files.** `internal/cli/billing_prompt.go` (**new**), `internal/cli/init_project.go`
(the call site: the picker replaces the plain `guided()` line **only** in the
interactive wizard), `internal/cli/init_inputs.go` (the billing question joins
the wizard's questions), `internal/cli/billing_prompt_test.go` (**new**).
**What it changes (D6).** When `canType()` is true — a real terminal, not
`--non-interactive`, not `--json`, not an agent session — the billing question
lists the open accounts by number with display name and id, picks by number,
takes a single account as the default, and on none says the one clear thing
about creating one in the console and prints today's guided `gcloud billing
projects link` line. When `canType()` is false, **nothing is listed and the
run behaves exactly as today** (Global Constraint 8). The account id still goes
through the existing typed banner before any link, and `--yes` still never
covers it (W0.1's matrix gains a row). What is picked is **not** stored anywhere
but where it is today.
**Tests.** `TestPickerOffersOpenAccountsByNumber`,
`TestPickerPicksByNumberAndReasksOnNonsense`,
`TestPickerSingleAccountIsTheDefault`,
`TestPickerNoAccountsNamesTheConsoleAndTheGuidedLine`,
`TestPickerNeverRunsUnderJSON`, `TestPickerNeverRunsNonInteractive`,
`TestPickerNeverRunsInAnAgentSession`, `TestPickerNeverRunsWithoutATerminal`,
`TestPickerStillTypesTheAccountBeforeLinking`,
`TestPickerStoresNothing` (the config written is byte-identical to today's),
`TestPickerOutputHasNoAccountUnderJSON`.
**Red step.** `TestPickerNeverRunsUnderJSON` first — it fails today because the
picker does not exist, and it is the test that keeps the reversal narrow.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Billing|Picker|Project' -count=1 && fugaro verify test`.
**Depends on.** W4.2, W3.5, W1.3, and D6's answer. **Reviewer.** opus + **mandatory independent review**.
**Commit.** `cli: the wizard lists the open billing accounts to pick from, at a real terminal only`

### Task W4.4: The refusal when the Cloud Billing API is not enabled — **opus + mandatory**

**Files.** `internal/cli/billing_prompt.go`, `internal/preflight/google.go`,
`internal/cli/billing_prompt_test.go`.
**What it changes (D3).** The three cases, and nothing else:
- the quota project **is** the project being set up → the wizard may offer to
  enable `cloudbilling.googleapis.com`, behind a **typed** confirmation that
  `--yes` never covers, naming the project and the API;
- the quota project is **another** project → refuse, and print the exact
  `gcloud services enable cloudbilling.googleapis.com --project <quota>` line
  plus who to ask;
- either way, the fallback is W4.2's gcloud path or today's guided
  `gcloud billing projects link` text, which needs no API.
`internal/preflight/google.go`'s `Billing()` wording gains the same distinction.
**Tests.** `TestEnableOfferedOnlyWhenTheQuotaProjectIsTheProject`,
`TestEnablingIsTypedAndNeverUnderYes`,
`TestRefusalWhenTheQuotaProjectIsAnotherProject` (it names the line and the
project, and enables nothing),
`TestNothingIsEnabledWithoutAnAnswer`,
`TestThePickerStillWorksWithNoAPI` (the gcloud fallback path).
**Red step.** `TestRefusalWhenTheQuotaProjectIsAnotherProject` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/preflight -run 'Billing|Enable|Quota' -count=1`.
**Depends on.** W4.3, and D3's answer. **Reviewer.** opus + **mandatory independent review** (it is the only new surface that can change a third project).
**Commit.** `cli: enabling the Cloud Billing API is typed, offered only on the project being set up, and never silent`

### Task W4.5: The rules that become false — standard

**Files.** `internal/infra/projectcreate.go` (its head comment: "init does not
list the caller's billing accounts (`billingAccounts.list`) even to guide,
because that reveals every account the user can reach and the account is theirs
to name" — **false after W4.3**; it becomes the narrow statement W4.1 wrote down,
with the conditions under which the list *is* asked for, and its UNVERIFIED list
gains one row for the picker's permissions), `internal/cli/init_project.go` (its
doc comment's "list the user's billing accounts, or print the account a project
is linked to" — the first half is false, the second half stays true), and
`internal/cli/init_project_test.go` where it pins those words.
**Why it is separate.** It is the only place where the reversal has to be
*described* rather than implemented, it is reviewable on its own, and it depends
on W4.3 having decided the final shape.
**Tests.** `TestTheBillingListIsNarrow` (**new**: the picker's call site is
reached only through `canType()`, and the account a project is *already linked
to* is never printed — the half of the old rule that survives).
**Red step.** n/a (comments and one new test).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/infra -count=1 && fugaro verify test`.
**Depends on.** W4.3. **Reviewer.** standard.
**Commit.** `infra, cli: the comments say what the billing list may do now, and keep what it still never does`

---

## Slice 5 — `fugaro update`

### Task W5.1: `update`'s stage set, its refusals and its non-interactive contract — opus

**Files.** `internal/cli/update.go`, `internal/cli/init_stages.go` (the stages
`update` registers, and the `runCovered` row that says whether `--yes` may
cover them — the map lives in `internal/initflow/stage.go`),
`internal/cli/setup_refusals_test.go` (W0.1's matrix gains `update`'s column),
`internal/cli/init.go` (the
`errNoState` family's wording), `docs/gcp-setup.md` (its `fugaro update`
section), their tests.
**What it is (F11, F16, D1, D5).** `update` registers everything `setup` does
**except** the project stage's creation path and `computeStage`: preflight,
services, installation, Firebase, images, secrets, plugin, and each enrolled
repository. Its refusals, each with a test:
- `--create-project` and `--link-billing` → refused, naming `fugaro setup`;
- no installation → refused, naming `fugaro setup`
  (`"there is no Terraform state bucket gs://%s, so no installation"`);
- an unenrolled checkout → not silently enrolled: one line naming `fugaro
  setup`, and exit code 1 (`left for you`), which `Result.ExitCode()` already
  means.
And its contract: `update` under `--yes` covers **the ordinary steps of every
stage it owns** — the same five `internal/initflow/stage.go`'s `runCovered`
names, `Installation`, `Firebase`, `Images`, `Installation2` and `Repository` —
and **exactly the same four things stay Typed as in `setup`**, because they are
money or permanent whatever command asks for them: the Firestore database's
location, a Cloud Build, replacing a registry tag, and onboarding an unlisted
repository. `runCovered`'s own comment says so, and `update` does **not** relax
a row of it: `update --yes` on a checkout whose image must be rebuilt still
stops and asks (and, with no terminal, answers `needs-you` and exits 1), which
is the honest reading of "non-interactive friendly" — nothing is money,
permanent or someone else's *without the confirmation the run already
requires*. W0.1's matrix gains `update`'s column, and D12 owns the question of
whether that is the contract the owner wants.
`update` with neither `--yes` nor a terminal plans only, as `initflow.Run`
already says.
**Tests.** `TestUpdateRefusesCreateProjectAndLinkBilling`,
`TestUpdateWithoutAnInstallationNamesSetup`,
`TestUpdateLeavesAnUnenrolledRepositoryAloneAndExitsOne`,
`TestUpdateUnderYesCoversEveryStageItOwns` (a covered stage applies with no
typed confirmation; **planting the bug** — relaxing a Typed step to covered —
must fail it, and it fails W0.1's matrix too),
`TestUpdateNonInteractiveWithoutYesPlansOnly`,
`TestUpdateConvergesTwiceAndSaysNoChanges` (the second run's every stage is
`done`/"No changes", which is Check 27's assertion in miniature),
`TestUpdateNeverCreatesAnything` (the project-creation client is never reached).
**Red step.** `TestUpdateRefusesCreateProjectAndLinkBilling` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Update' -count=1 && fugaro verify test`.
**Depends on.** W1.3, and D1's, D5's and **D12**'s answers. **Reviewer.** opus.
**Commit.** `cli: fugaro update re-converges what exists, refuses to create anything, and keeps the confirmations that are money or permanent`

### Task W5.2: The e2e tests, renamed for what they test — standard

**Files.** `internal/e2e/init_test.go` → `internal/e2e/update_test.go`; its
**26 `TestInitRepo*` tests become `TestUpdate*`**, plus the 27th `TestInit*`,
`TestInitForgetThenReinitImports`, which takes the same prefix — the split is
the one already in the file: the two that build an installation
(`TestInitRepoBuildsThenDeploys`, `TestInitRepoBuildNeedsConfirmation`) are the
only candidates for `TestSetup*`, and that is the owner's to call with W1.3.
Every one of the 26 keeps its assertions, in the groups the file already has:

- **adoption** — `AdoptsBootstrapResources`, `AdoptedSwitchesImageAfterBuild`;
- **the build** — `BuildsThenDeploys`, `FailedBuildStillRecordsRepo`,
  `BuildNeedsConfirmation`, `WarnsBaseImageOutsideBaseRegistry`;
- **what the run records** — `RecordsVertex`, `LeavesInstallationWorkdir`;
- **refusals** — `RefusesForeign`, `RefusesInstallationFlags`,
  `RefusesUnnamedInstallation`, `RefusesForgottenInstallation`,
  `RefusesInstallationWithoutTagMover`, `NeedsInstallation`, `NeedsFugaroYAML`,
  `GitHubNeedsAppID`;
- **idempotence** — `Idempotent`;
- **secrets and variables** — `FreshPrintsSecrets`, `PrintVarsNoCalls`,
  `AllowJobDeleteHighlighted`;
- **`--forget` and its resumption** — `Forget`,
  `ForgetResumesAfterPartialFailure`, `ForgetStateRmFailsKeepsObject`,
  `ForgetRefusesUnversionedStateBucket`;
- **cloud errors and enables** — `StorageDisabledIsRemote`,
  `EnablesResourceManager`.
**Why it is its own task.** These are the closest thing to a whole-run test this
repository has without a live cloud, and they are the proof that `update` is the
same behaviour under a new name.
**Tests.** The file **is** the tests.
**Red step.** `GOFLAGS=-p=2 go test ./internal/e2e/... -count=1` before the
rename — green; after the command rename (W1.2) but before the test text, it is
red and names each call.
**Verification.** `GOFLAGS=-p=2 go test ./internal/e2e/... -count=1`.
**Depends on.** W5.1. **Reviewer.** standard.
**Commit.** `e2e: the install-and-onboard tests are the update tests`

---

## Slice 6 — The checks

*Owner-run. No agent performs any step. Both run a released or owner-built
binary, so a release is a hidden step before each.*

### Task W6.1: Check 27 — OWNER-RUN

**Files.** None; the owner records the `FACT:` lines in `docs/gcp-live-checklist.md`.
**Depends on.** W2.7, W3.5, W4.5, W5.2, and a release. **Verification.** The
owner runs the rewritten Check 27 on installation `fugaro` — throwaway project,
sandbox repository only, **never `belong`**, `belong` untouched — the Restore's
final assertion reads `0`, and the result is recorded. **Reviewer.** owner.
**No agent performs any step.**

### Task W6.2: Check 111 — OWNER-RUN

**Files.** None; the owner records the `FACT:` lines.
**Depends on.** W4.4, W4.5, and a release. **Verification.** The owner runs
Check 111 on installation `fugaro`: each of the five modes prints the guided
line, nothing is enabled, no account list appears in any output, and the result
is recorded. **Reviewer.** owner. **No agent performs any step.**

---

## Slice 7 — Later, unscheduled

Each needs its own plan amendment and the owner's go.

- **A deprecated `init` alias** (D2's alternative), if the owner prefers it to
  the tombstone: one release, bare `init` mapping to `update`, and the docs
  mention removed in the following release.
- **A second installation on one machine.** D11 makes `setup` degrade to
  `update` when an installation exists; supporting two on one machine means
  naming them, which is a config change and not a wizard change.
- **An interrupted wizard.** W3.5's `wizardStage` resumes at the unanswered
  question; a Ctrl-C between answers has no story yet, and neither does a
  half-created project whose creation succeeded and whose installation did not
  (today's resume is `initflow`'s, which is enough for a rerun).
- **A picker preview.** `--show-accounts`, printing the list and exiting, for a
  user who wants to see it without running the wizard — refused under `--json`
  like everything else, and only if the owner wants it.
- **A rename of the `init*.go` files in `internal/infra` and the tests whose
  names say `init`** (the survey's long tail), after W2.7's precedent.

---

## Risks

- **D6 is the whole risk of this work.** Reversing "init never lists the
  caller's billing accounts" puts an enumeration of everything a user can reach
  on screen. The plan narrows it to open accounts, ids and display names, one
  call site, a real terminal only, nothing stored, nothing under `--json` — but
  the decision, not the plan, is what makes that true, and W4.1 and W4.3 are
  marked for a mandatory independent review because of it.
- **The rename is ~790 Go lines and 111 markdown files.** Its risk is not
  effort but *silence*: a stale `fugaro init` in live text is a user typing a
  command that no longer exists. W0.2's census and W2.6's flip are the
  mitigation, and they are the reason the census is written before anything
  moves.
- **W1.1 is a small API change to a shared package.** If `Options.Program`
  defaults badly, a caller that forgets it prints the wrong command. The
  `""`-default test is the mitigation.
- **The billing picker's two sources can disagree** (`billingAccounts.list`
  against `gcloud billing accounts list`). The plan says which answered rather
  than pretending they match, and this is in the reconciliation's UNVERIFIED
  table.
- **`internal/cli` is one lane.** The plan's parallelism is thin and the
  critical path is nine steps; a task that touches two of the wizard's files at
  once lengthens it. Stated rather than hidden.
- **No task here is approved, and the D-rows are open.** Anything built before
  D1–D12 are answered may have to be undone; the slivers least at risk are
  W0.1, W0.2, W2.5 and W4.1, which take no decision.

---

## OWNER APPROVAL GATE

**Not passed. No task is approved. Nothing is implemented.** D1–D12 in
[reconciliation.md](reconciliation.md) §5 are open; each carries a
recommendation, and answering one is not a go. To start any task the owner names
it (or a slice) and says go. The tasks that wait on no decision and no other
task are **W0.1, W0.2, W1.1, W2.5 and W4.1** — the two pins, the loop's one
change, the Terraform descriptions and the billing client. Everything else
waits on the D-row named in Global Constraint 3; W0.3 writes the checks' text,
which *is* D10's recommendation, so it waits on **D10** and on no task.
