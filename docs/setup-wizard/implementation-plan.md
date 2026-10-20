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

**Status: approved to build; implementation in progress.** **D1–D12 in
[reconciliation.md](reconciliation.md) §5 were answered by the owner on
2026-10-20** — eleven as recommended and **D12 the other way**
(`` `update --yes` `` covers everything `update` owns). The gate at the end of
this file is **passed**: the plan may be built, in the slice order below. Being
approved to build is not a go for every task at once — Global Constraint 2
still asks the owner to name a task or a group before it starts, and no task
below has been written or run yet. Where a task's text and an answer disagree,
**the answer wins** and the task is corrected before it is built.

**Checked against.** `main` at `82d366af`, the same commit as
[reconciliation.md](reconciliation.md). Every path and symbol below was
located with `git grep` and re-checked to exist before it was written; a path
marked **new** does not exist yet. Line numbers are never cited. Three goldens
are named throughout and are the plan's best verification target:
`internal/cli/testdata/init_commands.golden` (every stage's
"left for you" text, rewritten by `TestPrintedCommandsGolden`'s `-update`
flag), `internal/cli/testdata/init_review.golden` (`TestReviewScreenGolden`,
`-update-review`) and `internal/cli/testdata/init_retire.golden`
(`TestInitRepoRetireTranscriptGolden`, `-update-retire`).

**Spec.** [spec.md](spec.md) (verbatim). **Reconciliation.** [reconciliation.md](reconciliation.md)
— C1–C3, F1–F24, D1–D12, O1–O5, the UNVERIFIED table. **Index.** [README.md](README.md).

**Related.** [gcp-setup.md](../gcp-setup.md) · [gcp-live-checklist.md](../gcp-live-checklist.md)
(Checks 27 and 111) · [m11-setup-and-skills.md](../design/m11-setup-and-skills.md) ·
[2026-10-04-m11-setup-and-skills.md](../plans/2026-10-04-m11-setup-and-skills.md)
(the converge-loop design this builds on) · [v1.md](../design/v1.md) ·
[credentials-service/](../credentials-service/) (the shape this folder follows)

---

## What this does not do

- **No engine.** `internal/initflow` is unchanged except for two
  comment-level things: W1.1 (O2) stops printing a hard-coded command name, and
  W5.1 corrects `runCovered`'s doc comment, whose "stay `Typed`" clause D12's
  answer makes false. Every stage, every
  confirmation class and every refusal stays where it is.
- **No bucket renaming.** D4 accepts `fugaro-runs-fugaro-<name>` when the id
  default is used. Nothing about `fugaro-runs-<gcp-project>`,
  `fugaro-tfstate-<gcp-project>` or the managed fact's path moves.
- **No historical rewrite.** The **ten** dated roots —
  `docs/plans/**`, `docs/releases/v*`, `docs/design/**`,
  `docs/credentials-service/**`, `docs/website/**`, `docs/coordinator/**`,
  `docs/in-job-review/**`, `docs/mobile/**`, **`docs/windows-cli/**`** and
  **`docs/setup-wizard/**`
  itself** — keep the old name (D9, F20). `docs/plans/**`, `docs/releases/v*`
  and `docs/design/**` are this tree's own record of what shipped on a date;
  the other six — `docs/website/`, `docs/credentials-service/`,
  `docs/coordinator/`, `docs/in-job-review/`, `docs/mobile/` and
  `docs/windows-cli/` — each hold
  another artifact's own `reconciliation.md` (the mobile folder its
  `implementation-plan.md` too), describing work as it was planned on its own
  date, so rewriting the command inside them would be editing a record rather
  than fixing a document. The rename's guard test
  enforces the line, and this folder is on
  the exempt side of it: its four documents are where the retirement is
  described, so they name the retired command throughout (the count is volatile
  — every edit to these documents changes it — so it is the command, not a
  figure: `git grep -o -c 'fugaro init' -- docs/setup-wizard`).
- **No fourth verb** (O1): `setup`, `update`, `upgrade`, the hidden `init`
  tombstone, and `fugaro config init` left alone.
- **No `fugaro upgrade` change beyond its text.** D1 (answered as recommended)
  keeps the two commands apart.

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
| The repository stage and its gates: `newRepositoryStage`, `authorize`, `resolveRepoTarget`, `checkoutNote`, `unknownDetail` | `internal/cli/init_repo_stage.go`, `init_repo_gate.go`, `init_repo_target.go`; `checkoutProject` is `internal/cli/project.go` | W3.4 |
| The Cloud Billing fake: `SetBilling`, `UseCRM`, `SetSuspended`, `accounts`, `Link` | `internal/gcpfake/billing.go` | W4.2, W4.3 |
| The billing preflight: `Billing(...)`, "the Cloud Billing API is disabled on <quota>, the quota project of your credentials" | `internal/preflight/google.go` | W4.4 |
| The honest UNVERIFIED list against the real services | `internal/infra/projectcreate.go` | W4.1, W4.5 |
| Credential hygiene: `Options.Redact`, `loop.redact`/`redactAll`/`redactedCause`, `held`, `maxHeldSecrets` | `internal/initflow/loop.go`, `internal/cli/initsecrets.go` | W4.3, W0.1 |
| `restore` invoking init as a child: `restoreInitCmd = newInitCmdWith`, and `TestRestoreSeamsAreProductionsFunctions` pinning the pointer | `internal/cli/restore.go`, `restore_test.go` | W1.2 |
| The three goldens and the tests that own them | `TestPrintedCommandsGolden` (`internal/cli/init_output_test.go`, `-update`), `TestReviewScreenGolden` (`init_review_test.go`, `-update-review`), `TestInitRepoRetireTranscriptGolden` (`init_retire_test.go`, `-update-retire`) | W1.2, W2.1 |
| The docs pins | `internal/cli/docs_*_test.go` (44 files at `82d366af`, of which **12** name the command — the glob covers `docs_website_*_test.go` too, none of whose six names it), `internal/clidoc/cliref_test.go`, `plugin/{readme,setup_skill,skills_lint}_test.go` | W2.3, W2.4, W2.6 |
| The generated reference and its generator | `internal/clidoc/cliref.go`, `website/content/cli-reference.md` | W2.4 |
| The dated-record precedent for a retirement | `plugin/readme_test.go`'s `TestReadmeNamesNoRetiredSkill` `skip` regex | W0.2, W2.6 |
| The live-check register and its numbering rule | `docs/gcp-live-checklist.md` | W0.3, W6.1, W6.2 |

---

## Slice summary

| Slice | What it is | Tasks | Mergeable alone? | When |
|---|---|---|---|---|
| **0 — Verify first** | The refusal matrix and the rename census pinned as tests; Checks 27 and 111 written | W0.1–W0.3 | Yes — two new test files and one document | W0.1 and W0.2 first, on the owner's go; W0.3 now that **D10** is answered |
| **1 — The two front doors** | `initflow` stops naming a command; `setup` and `update` registered; `init` a tombstone; `restore` repointed; `setup` is the wizard | W1.1–W1.3 | Yes — the CLI keeps converging; the old name still resolves to the tombstone | After Slice 0 |
| **2 — The rename** | The retired word out of every live string, document, skill, Terraform description, generated file and pinned test; the census emptied | W2.1–W2.7 | Yes, in the order given — a partial rename is a failing docs pin, which is the intent | After W1.2 (W2.5 can start at any time) |
| **3 — The wizard's questions** | The id default, the region kept apart from the Firestore location, `--firebase-project`, the first-repository question, and the two-phase input | W3.1–W3.5 | Yes — each leaves a working wizard with one more question answered | After Slice 1 |
| **4 — The billing picker** | The Cloud Billing client, the gcloud fallback, the picker, the API-enable refusal, and the comments the reversal falsifies | W4.1–W4.5 | Yes — with the picker absent or refused, today's `guided()` line is what runs | After W3.5; W4.1's decision, **D6**, is answered |
| **5 — `fugaro update`** | Its stage set, its refusals, its non-interactive contract, and the e2e tests renamed | W5.1–W5.2 | Yes | After Slice 1 |
| **6 — The checks** | Check 27 rewritten for `setup` — its commands renamed and the wizard's sequence in front, with every probe it already carries kept — and Check 111 for the picker's failure modes | W6.1–W6.2 | Owner-run; no commit but the recorded result | After a release |
| **7 — Later, unscheduled** | A deprecated alias, a second installation on one machine, an interrupted wizard, a picker preview | — | — | — |

---

## Check numbers this plan takes

**27 is rewritten; 111 is new.** `docs/gcp-live-checklist.md`'s **Check 27**
(M11's simple setup, on a throwaway project) covers the `--create-project` /
`--link-billing` path; its steps name flags and a command this work renames, so
its text is stale, and it is rewritten **in place** — same number (D10). The
register states its status twice and the two statements disagree (F22): the row,
the heading and the prose say **NOT RUN** ("No step below has been run"), while
the same section carries "Results of the seventh live run (M11, check 27)" — run
2026-10-04 and 2026-10-05 with the `0.2.0` release on the throwaway project
`fugaro-live-20261005` — which records PASS or VERIFIED for the typed
confirmations, the clean converge and its `No changes` rerun, the mirror, the
hidden secret prompts, the hostile-clone gate, `doctor`, the teammate's adopt
run, `/fugaro:setup` and the prompt-injection probe. What has not been run is the
**rewritten** text, whose commands and question order are new. The register's
written sections run to **89** (81, 82 and 89 are written, in the mobile block);
90–99 are the credentials service, 100–109 the mobile functional spec, and
**110 is claimed by an open pull request** — the register's rule is that
whichever of two open claims merges second renumbers itself. **111 is therefore
the next free number**, and it is taken here.

| # | The check | Written by | Run at | Where |
|---|---|---|---|---|
| **27** (rewritten) | **`fugaro setup` end to end, and everything Check 27 already proves.** New: from an empty shell on a throwaway project the wizard asks in order; Enter accepts the id `fugaro-<name>`; the created project types its ID; the billing picker lists the owner's open accounts by number; the link types the account's ID; the Firestore location types `us-east5`; the first image build types its confirmation; installation, Firebase and the first repository converge; then `fugaro update` **twice**, the second run asserting "No changes" for every stage. **Kept, with their commands renamed**: the plugin wiring and the four Claude Code spikes (V1–V4), the image mirror against real ghcr.io and Artifact Registry, hidden secret prompts and their refusals, the **hostile-clone gate**, `doctor` and `update-skills`, the teammate's adopt run, `/fugaro:setup`, and the **prompt-injection probe** | W0.3 | W6.1 | Installation `fugaro`, sandbox repository, throwaway project |
| **111** (new) | **The picker's failure modes.** API disabled on the quota project; a user without `billing.accounts.list`; no TTY; `--json`; an agent session — each asserting the guided `gcloud billing projects link` line, that nothing was enabled, and that no account list appears in any output | W0.3 | W6.2 | Installation `fugaro` |

Check 27 keeps its number — a number whose subject was rewritten stays the same
number and is not reused elsewhere — but its text is **rewritten in place, and
nothing it already proves is dropped**. Two things move: the command lines and
flags are renamed (`fugaro init` → `fugaro setup`, and the converge-again steps
→ `fugaro update`), and the wizard's question order replaces the old step order
at the front. The **probes stay**, because the register has no other home for
them: `grep -n 'injection\|hostile' docs/gcp-live-checklist.md` names Check 27's
row and its section and nothing else, so deleting them would be the hole in the
register that D10's alternative was rejected for. The same holds for the four
Claude Code spikes (V1–V4), the mirror against the real registries, `doctor`,
`update-skills`, the teammate's adopt run and `/fugaro:setup`: no other check
names them, and the run recorded under Check 27 is the only evidence any of them
has (the spikes VERIFIED live 2026-10-04, the rest PASS), so a rewrite that
dropped them would delete a record that has already been paid for. W0.3's rule
is therefore **rename the commands, keep the probes** — and its summary row
above is the list it must not lose.

---

## Conventions every task follows

1. **Docs and pins move together.** Every task that renames a *string* renames
   the document or test that pins that string **in the same commit**. A red pin
   is the specification, not an obstacle: run the pin first, read the sentence
   it names, change it on purpose. The guards are
   `GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc ./plugin/... -run 'Docs|Lint|CliRef|Readme|Skill' -count=1`.
2. **The goldens are regenerated, never hand-edited** — each by the test that
   owns it, named here because a `-run` that matches nothing **exits 0 and
   regenerates nothing**, silently:
   `testdata/init_commands.golden` by `TestPrintedCommandsGolden` with `-update`
   (`internal/cli/init_output_test.go`), `testdata/init_review.golden` by
   `TestReviewScreenGolden` with `-update-review` (`init_review_test.go`), and
   `testdata/init_retire.golden` by `TestInitRepoRetireTranscriptGolden` with
   `-update-retire` (`init_retire_test.go`). Each task that regenerates one
   runs its own test **and checks the file's mtime changed**; the regenerated
   diff is read line by line in review, because it *is* the user-visible text.
3. **The decisions are answered, and a task may not contradict one.**
   [reconciliation.md](reconciliation.md) §5 carries the owner's answer to each
   of D1–D12 (2026-10-20), so a task whose "Depends on" line names a D-row
   (`W0.3` on D10, `W4.1` on D6, `W5.1` on D12) is no longer waiting: the
   dependency is discharged, and the task must be built to the **answer**, not
   to the recommendation it was written beside. Where they differ — **D12 and
   only D12** — the answer is what the task text below says, and a task that
   discovers a stale assumption stops and says so rather than building either
   version.
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
7. **The refusals are immutable** — with **one** exception, and it is the
   owner's. No task weakens `canType()`, `RunCovers`, `runCovered`, `YesCovers`,
   `Managed`, the agent marker check, the impersonation check, the
   quota-project check or the `ACTIVE` project check. The exception is
   **W5.1**, which relaxes three **call-site** confirmations and one call site
   in `update` only (`confirmLocation`, `buildImages`, `confirmReplace`,
   `confirmRepo` — its own entry lists them) because **D12 was answered
   against the recommendation**: it is exactly the row-relaxation this
   convention exists to make hard, and it is written out in reconciliation §5
   rather than decided by a task. Nothing in `internal/initflow` changes
   behaviour: `runCovered` is read as it stands, and the one line W5.1 writes
   there is that map's doc comment, whose "stay `Typed`" clause the answer makes
   false (its own entry says why, and why the corrected comment names no front
   door). W5.1 says so in its own entry and carries a
   **mandatory independent review**, which any future change of this kind must
   too.
8. **Credentials are never printed or stored.** `Options.Redact` stays wired for
   both front doors. The billing picker (W4.3) prints account **identifiers**,
   which are not credentials, and never under `--json`.
9. **`GOFLAGS=-p=2`** for anything touching `internal/cli`, `internal/initflow`
   or `internal/infra`. Before merge, `fugaro verify build` and
   `fugaro verify test` on the final commit.
10. **One file, one writer per level.** The levels below are disjoint by file;
    `internal/cli` is one lane, which is why the table looks thin.
11. **The dated records are read-only** (D9): the **ten** roots —
    `docs/plans/**`, `docs/releases/v*`, `docs/design/**`,
    `docs/credentials-service/**`, `docs/website/**`, `docs/coordinator/**`,
    `docs/in-job-review/**`, `docs/mobile/**`, `docs/windows-cli/**` and **this
    whole folder, `docs/setup-wizard/**`** — are never edited by a rename task.
    These ten are exactly W0.2's `datedRecords` constant. Six of them
    (`docs/website/`, `docs/credentials-service/`, `docs/coordinator/`,
    `docs/in-job-review/`, `docs/mobile/`, `docs/windows-cli/`) are another
    artifact's plan record, while `docs/setup-wizard/`'s prose is *about* the
    retirement and cannot stop naming the retired command.

---

## Global Constraints

1. **Docs only in this pull request.** It adds `docs/setup-wizard/{spec.md,
   reconciliation.md,implementation-plan.md,README.md}` and edits nothing else.
   No Go, Terraform, schema, skill or workflow change. (The tasks below edit
   files outside `docs/`; this pull request does not.)
2. **Approved to build is not approved per task.** Each task starts on the
   owner's explicit go for that task or a named group; the answered D-rows say
   *what* to build, never *when*.
3. **The D-rows a slice waits on are answered** (reconciliation §5,
   2026-10-20), and the "Decisions → tasks" table below is this map in full:
   W1.2 on **D2** (the tombstone's text, and whether there is an alias at all)
   and W1.3 on **D11** (does `setup` create by default?); W3.1 and W3.2 on
   **D4**, W3.3 on **D7**, W3.4 on **D5**; W4.1, W4.3 and W4.5 on **D6**, W4.4
   on **D3**; W5.1 on **D1**, **D5** and **D12**; W2.3 on **D8**; W0.2 and W2.6
   on **D9**; W0.3, W6.1 and W6.2 on **D10**. W3.5 waits on **F12** rather than
   on a decision. Every one of these is answered as recommended except **D12**,
   whose answer is the other way: §5's D12 paragraph is the contract W5.1
   builds and W0.1's matrix asserts.
4. **The wizard never softens a confirmation.** `--yes`, `--non-interactive`,
   `--json`, a pipe and an agent session behave exactly as
   `internal/initflow`'s `YesCovers`/`CanConfirm` say today; W0.1's matrix is the
   test both front doors must pass. After **D12** three rows of it may never be
   relaxed — **creating the project, linking the billing account and the
   Firestore database's location, all three `setup`'s** (the first two are
   refused outright by `update`, which covers the third) — while a billable
   image build, replacing a registry tag and onboarding an unlisted repository
   are **covered**, in both front doors, where they were `Typed` before D12 was
   answered. That is the whole of what the answer moves, and it is written out
   row by row in §5.
   **None of those rows is a stage row.** `internal/initflow/stage.go`'s
   `runCovered` is five stage *names*, read once by
   `RunCovers(name) = runCovered[name] && YesCovers(name)`;
   `internal/initflow`'s own three `noYes` stages (`Project`, `Secrets`,
   `Compute`) are the whole of what the package decides — and they stay exactly
   as they are, `runCovered`'s five entries included. The map's doc comment is
   the one thing D12 falsifies in that file: it calls the Firestore location, a
   Cloud Build, a registry tag and an unlisted repository steps that "stay
   `Typed`", which is true of one front door and false of the other, so W5.1
   corrects the comment (door-neutral, because the package names no front door)
   and writes nothing else there.
   Every step D12 moves —
   or holds — is a **call-site** guard in `internal/cli`: `init_firebase.go`'s
   `confirmLocation`, `init.go`'s `buildImages`, `init_images.go`'s
   `confirmReplace`, `init_repo_gate.go`'s `confirmRepo` (all four named in
   W5.1). Two of them ride stages whose `YesCovers` is **already true**
   (the location rides `Firebase`, the build rides `Images`), so no change to
   `runCovered` can express the answer, and no test written against
   `YesCovers` can pin it — W0.1's second table pins the call sites instead.
   One more confirmation is typed by a *different* decision — the offer to
   enable `cloudbilling.googleapis.com` (D3, W4.4), which `--yes` never covers
   either — so this list is D12's rows and not every typed step in the work.
5. **The rename does not touch the cloud.** W2.x changes text, comments and
   descriptions only: no Terraform resource, variable name or path moves, so an
   apply on an existing installation sees no diff.
6. **`fugaro config init` stays.** W1.2 does not rename it, and the tombstone
   must not shadow it (F21); `TestDocsCheckoutCommandsExist` resolves both.
7. **The dated records keep the old name** (Convention 11), and W2.6's guard
   test fails if the exemption list is wrong in either direction: `datedRecords`
   is **one** constant, written in W0.2 with D9's **ten** roots and referenced —
   never restated — by every other task, because two copies of the list are two
   lists that drift.
8. **The billing picker runs only where `canType()` says yes.** Under `--json`,
   `--non-interactive`, no TTY or an agent session the run uses today's
   `guided()` line and lists nothing (D6, W4.3).

---

## Review Focus

A reviewer of **this pull request** (docs only) should check:

- That every path and symbol below exists on `main` at `82d366af`, or is marked
  **new**, and that no task claims a test protects what it does not.
- That every task agrees with the **answers** in reconciliation §5 rather than
  with the recommendation it was drafted beside — the D-row each slice waits on
  is named in Global Constraint 3 and repeated in the task's own entry, and
  **D12 is the one answer that moved a task's text**.
- That the rename's line is enforceable (W2.6's guard test fails in both
  directions), that `datedRecords` is stated in exactly **one** place (W0.2),
  and that the **ten** roots it names match D9's rule — in particular that
  `docs/website/`, `docs/coordinator/`, `docs/in-job-review/`, `docs/mobile/`
  and `docs/windows-cli/` (added on the review's finding: it postdates
  `82d366af` but holds another artifact's plan documents and names the command
  once) are on the list, and that `docs/website/redesign/**`,
  `docs/voice-module/**` and `docs/pending-workflows/**`, which name the command
  zero times, are not.
- That Check 27's rewrite and Check 111 are the owner's to run, are sandbox
  only, never touch `belong`, and follow the register's form.
- That the levels contain no two writers of one file — check it against **`### The
  file lanes`** *and* against **`### Edited (the shared ones)`**, which are two
  statements of the same list and must agree file for file and writer for
  writer; the lanes list every file with more than one writer and its
  writers in level order; that every "Depends on" line is an edge below and
  every edge is a "Depends on" line (the lanes are deliberately *not* edges, and
  are listed separately for that reason); and that the critical path is the one
  named.
- That the refusals of F7/F8/F9 in the reconciliation have no task that
  weakens them.

A reviewer of a **task** reads its own "Reviewer" line.

---

## Decisions → tasks

Every row below is **answered** (reconciliation §5, 2026-10-20); the table is
where each answer is built, not what is still owed.

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
| D12 (`update --yes`'s confirmations) | W5.1 — **answered the other way**, so W5.1 relaxes four **call sites** in `internal/cli` (`confirmLocation` in `update` only, `buildImages`, `confirmReplace`, `confirmRepo`) and corrects `internal/initflow/stage.go`'s `runCovered` comment, which the answer makes false; the map itself is read unchanged — and W0.1's matrix, whose second table is those call sites |
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
| `internal/cli/retired_test.go` (**new**) | W1.2 | `TestInitIsRetiredAndSaysBothNames`, `TestRetiredInitAppliesNothing`; one of `retirementText`'s three |
| `internal/cli/wizard_stage.go` (**new**) | W3.5 | The input phase as one `initflow.InputStage` whose `Missing()` names every flag |
| `internal/billing/accounts.go` (**new**) | W4.1 | The Cloud Billing client: open accounts only, ids and display names, a typed disabled error |
| `internal/billing/gcloud.go` (**new**) | W4.2 | The `gcloud billing accounts list` fallback behind an injected runner |
| `internal/cli/billing_prompt.go` (**new**) | W4.3 | The picker: numbering, the single-account default, the none case, the four conditions |
| `internal/e2e/update_test.go` (**new**, from `init_test.go`) | W5.2 | The 26 `TestInitRepo*` tests (of 27 `TestInit*`), renamed |
| `internal/cli/{install,wizard,converge}*.go` (**new**, from `init*.go`) | W2.7 | The pure rename, if the owner wants it (droppable) |

### Edited (the shared ones; every task lists its own)

| Path | Written by (level) |
|---|---|
| `internal/cli/init.go` | W1.2 (L1), W2.1 (L2), W3.3 (L4), W5.1 (L7) |
| `internal/cli/init_stages.go` | W1.2 (L1), W2.1 (L2), **W1.3 (L3)**, W3.5 (L6), W5.1 (L7) |
| `internal/cli/setup.go` | W1.2 (L1), **W1.3 (L3)**, W3.4 (L5), W3.5 (L6) |
| `internal/cli/update.go` | W1.2 (L1), W5.1 (L7) |
| `internal/cli/restore.go` | W1.2 (L1), W2.1 (L2) |
| `internal/cli/testdata/init_commands.golden`, `internal/cli/testdata/init_review.golden`, `internal/cli/testdata/init_retire.golden` | W1.2 (L1), W2.1 (L2) |
| `internal/cli/init_project.go` | W2.1 (L2), W1.3 (L3), W3.3 (L4), W4.3 (L9), W4.5 (L10) |
| `internal/cli/init_inputs.go` | W2.1 (L2), W3.1 (L4), W3.2 (L5), W3.5 (L6), W4.3 (L9) |
| `internal/cli/init_firebase.go` | W2.1 (L2), W3.3 (L4), W5.1 (L7) |
| `internal/cli/init_images.go` | W2.1 (L2), W5.1 (L7) |
| `internal/cli/init_repo_gate.go`, `init_repo_target.go` | W2.1 (L2), W3.4 (L5) (`init_repo_gate.go` also W5.1 (L7)) |
| `internal/cli/init_inputs_test.go` | W3.1 (L4), W3.2 (L5) |
| `internal/cli/billing_prompt.go`, `billing_prompt_test.go` (**new** at W4.3) | W4.3 (L9), W4.4 (L10) |
| `internal/cli/setup_refusals_test.go` (**new** at W0.1) | W0.1 (L0), W5.1 (L7) |
| `internal/cli/docs_live_init_census_test.go` (**new** at W0.2) | W0.2 (L0), W2.6 (L5) |
| `internal/cli/docs_*_test.go` (44 at `82d366af`, **12** of them naming the command) | W2.1 (L2), W2.3 (L3) |
| `internal/initflow/stage.go` | W1.1 (L0), W5.1 (L7) — the second writes its `runCovered` doc comment **only**; the map and `RunCovers` are unchanged |
| `internal/e2e/init_test.go` → `update_test.go` | W2.2 (L2), W5.2 (L8) |
| `internal/preflight/google.go` | W2.2 (L2), W4.4 (L10) |
| `internal/infra/projectcreate.go` | W2.2 (L2), W4.5 (L10) |
| `internal/config/example.yaml`, `internal/config/example.go`, `schemas/fugaro.schema.json`, `images/verify-public.sh` | W2.5 (L2), W2.6 (L5) |
| `docs/gcp-live-checklist.md` | W0.3 (L0), W2.6 (L5) |
| `docs/gcp-setup.md` | W2.3 (L3), W5.1 (L7) |

The single-writer files are **not** in this table and are listed by their own
task instead (`### Created` above and each task's **Files** line): the W2.3
document sweep (`README.md`, `CONTRIBUTING.md`, `SECURITY.md`, the live `docs/`
guides, the skills), the W2.4 website and generated content, the W2.5 Terraform
and schema corpus, and `internal/initflow/loop.go`/`loop_test.go` (W1.1). The
six `internal/cli/docs_website_*_test.go` files sit with W2.4 for the same
reason: **none of them names the command** (`git grep -l 'fugaro init'
82d366af -- 'internal/cli/docs_website_*_test.go'` prints nothing), so W2.1 has
nothing to change in them, and the three W2.4 updates are its own pins
(`docs_website_install_test.go`, `docs_website_cliref_test.go`,
`docs_website_security_test.go`; the other three are untouched).

This table and **`### The file lanes`** are two statements of one list: a path
in one and not the other, or a different writer set for the same path, is a
defect in whichever was edited last. It is checked the same way in review.

---

## Parallel execution

### The edges

Every line here is exactly one task's "Depends on" line below, and nothing else.
**No line carries a D-row any more**: D1–D12 were answered on 2026-10-20, so the
decisions that used to be parents here — D10 for W0.3 and D6 for W4.1, the two
tasks whose own text *wrote* the recommendation down — are discharged, and a
D-row was never a task and never appears on the left of an arrow anyway.

```
W0.1 ← nothing     W0.2 ← nothing     W0.3 ← nothing        W1.1 ← nothing
W4.1 ← nothing     W4.2 ← W4.1        W2.5 ← nothing

W1.2 ← W1.1
W2.1 ← W1.2        W2.2 ← W1.2        W1.3 ← W1.2
W2.3 ← W2.1        W2.4 ← W2.1, W2.2, W2.3
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
| **L0** | W0.1 the refusal matrix · W0.2 the census · W0.3 the checks written · W1.1 `initflow` names no command · W4.1 the billing client | standard · standard · standard · opus · **opus + mandatory** | `internal/cli/setup_refusals_test.go` · `internal/cli/docs_live_init_census_test.go` · `docs/gcp-live-checklist.md` · `internal/initflow/loop.go`, `loop_test.go`, `stage.go` · `internal/billing/accounts.go` |
| **L1** | W1.2 the two commands, the tombstone, `restore` · W4.2 the gcloud fallback | opus · standard | `internal/cli/{init,setup,update,retired,root,restore}.go`, `restore_test.go`, `testdata/*.golden` · `internal/billing/gcloud.go`, `internal/gcpfake/billing.go` |
| **L2** | W2.1 the `internal/cli` strings · W2.2 the strings outside `internal/cli` · W2.5 Terraform | standard · standard · standard | the 48 `internal/cli/*.go` of W2.1's list, the 67 `internal/cli/*_test.go` files naming the command, the three goldens · every non-test `.go` outside `internal/cli` that names the command (`internal/{infra,budget,runner,managed,config,localcfg,projectpolicy,preflight,gcpfake,gateway,imagecheck,mirror,basepin,backend/{gcp,backendtest}}/**`, minus `config/example.go`) · `deploy/terraform/**`, `schemas/`, `internal/config/example.{go,yaml}`, `images/verify-public.sh`, `images/history/Dockerfile` |
| **L3** | W1.3 `setup` is the wizard · W2.3 live markdown, README and the skills | opus · standard | `internal/cli/{setup,init_stages,init_project}.go` · `README.md`, `docs/`, `plugin/skills/**`, `.claude/skills/new-release/SKILL.md` |
| **L4** | W3.1 the id default · W3.3 `--firebase-project` · W2.4 the website and the generated reference | standard · standard · standard | `internal/cli/init_inputs.go` · `internal/cli/{init,init_firebase,init_project}.go` · `website/**` |
| **L5** | W3.2 the region and the Firestore location · W3.4 the first-repository question · W2.6 the census emptied and the tail | standard · standard · standard | `internal/cli/init_inputs.go` · `internal/cli/{init_repo_gate,init_repo_target,setup}.go` · `internal/config/{example.yaml,example.go}`, `schemas/`, `images/verify-public.sh`, `docs/gcp-live-checklist.md`, `docs_live_init_census_test.go` |
| **L6** | W3.5 the two-phase wizard stage | opus | `internal/cli/wizard_stage.go`, `init_inputs.go` |
| **L7** | W5.1 `update`'s stage set, refusals and non-interactive contract | **opus + mandatory** | `internal/cli/{update,init_stages,init,init_firebase,init_images,init_repo_gate}.go` (the four D12 call sites: `confirmLocation`, `buildImages`, `confirmReplace`, `confirmRepo`), `internal/cli/setup_refusals_test.go`, `docs/gcp-setup.md` (its `update` section) — and `internal/initflow/stage.go`'s `runCovered` comment, which the answer makes false; the map itself is read unchanged |
| **L8** | W5.2 the e2e tests, renamed | standard | `internal/e2e/init_test.go` → `update_test.go` |
| **L9** | W4.3 the picker prompt | **opus + mandatory** | `internal/cli/billing_prompt.go`, `init_project.go`, `init_inputs.go` |
| **L10** | W4.4 the API-enable refusal · W4.5 the comments that become false | **opus + mandatory** · standard | `internal/cli/billing_prompt.go`, `internal/preflight/google.go` · `internal/cli/init_project.go`, `internal/infra/projectcreate.go` |
| **L11** | W2.7 the pure file rename | standard | the `internal/cli/init*.go` → `install*.go` move only |
| **L12** | W6.1 Check 27 · W6.2 Check 111 | owner · owner | none (the owner records the FACT lines) |

**Widest levels: L0 (five), and L2 and L5 (three each).** The levels obey one rule, and it is
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
internal/cli/init.go                  W1.2 (L1) → W2.1 (L2) → W3.3 (L4) → W5.1 (L7)
internal/cli/init_stages.go           W1.2 (L1) → W2.1 (L2) → W1.3 (L3) → W3.5 (L6) → W5.1 (L7)
internal/cli/setup.go                 W1.2 (L1) → W1.3 (L3) → W3.4 (L5) → W3.5 (L6)
internal/cli/update.go                W1.2 (L1) → W5.1 (L7)
internal/cli/restore.go               W1.2 (L1) → W2.1 (L2)
internal/cli/testdata/*.golden        W1.2 (L1) → W2.1 (L2)
internal/cli/init_project.go          W2.1 (L2) → W1.3 (L3) → W3.3 (L4) → W4.3 (L9) → W4.5 (L10)
internal/cli/init_inputs.go           W2.1 (L2) → W3.1 (L4) → W3.2 (L5) → W3.5 (L6) → W4.3 (L9)
internal/cli/init_firebase.go         W2.1 (L2) → W3.3 (L4) → W5.1 (L7)
internal/cli/init_images.go           W2.1 (L2) → W5.1 (L7)
internal/cli/init_repo_gate.go        W2.1 (L2) → W3.4 (L5) → W5.1 (L7)
internal/cli/init_repo_target.go      W2.1 (L2) → W3.4 (L5)
internal/cli/billing_prompt.go        W4.3 (L9) → W4.4 (L10)
internal/cli/billing_prompt_test.go   W4.3 (L9) → W4.4 (L10)
internal/cli/init_inputs_test.go      W3.1 (L4) → W3.2 (L5)
internal/cli/docs_*_test.go           W2.1 (L2) → W2.3 (L3)   (44 files at 82d366af; the 12 naming the command)
internal/cli/setup_refusals_test.go   W0.1 (L0) → W5.1 (L7)
internal/cli/docs_live_init_census_test.go  W0.2 (L0) → W2.6 (L5)
internal/initflow/stage.go            W1.1 (L0) → W5.1 (L7)   (the second, the runCovered comment only)
internal/e2e/init_test.go             W2.2 (L2) → W5.2 (L8)   (becomes update_test.go)
internal/preflight/google.go          W2.2 (L2) → W4.4 (L10)
internal/infra/projectcreate.go       W2.2 (L2) → W4.5 (L10)
internal/config/example.go            W2.5 (L2) → W2.6 (L5)
internal/config/example.yaml          W2.5 (L2) → W2.6 (L5)
schemas/fugaro.schema.json            W2.5 (L2) → W2.6 (L5)
images/verify-public.sh               W2.5 (L2) → W2.6 (L5)
docs/gcp-live-checklist.md            W0.3 (L0) → W2.6 (L5)
docs/gcp-setup.md                     W2.3 (L3) → W5.1 (L7)
```

**`internal/initflow/stage.go` is W1.1's file first, and W5.1's comment is
D12's whole footprint in it.** An earlier draft carried a second writer on that
line — `W5.1 (L7)`, marked
"only if the owner answers against the recommendation" — on the assumption that
answering against it meant changing `runCovered`. It does not. `runCovered` is a
**five-entry map of stage names** (`Installation`, `Firebase`, `Images`,
`Installation2`, `Repository`), read once by
`RunCovers(name) = runCovered[name] && YesCovers(name)`, and `internal/initflow`
decides nothing finer than that. Every step D12 moves is a **call-site** guard in
`internal/cli` — W5.1's own entry names them: `init_firebase.go`'s
`confirmLocation` (the Firestore location), `init.go`'s `buildImages` (a billable
first image build), `init_images.go`'s `confirmReplace` (a registry tag) and
`init_repo_gate.go`'s `confirmRepo` (an unlisted repository). Two of the four ride
stages whose `YesCovers` is **already true** — the location rides `Firebase`
(`init_firebase.go`'s `promptLeft(initflow.Firebase, …)`), the build rides
`Images` — so a `runCovered` change could not express the answer and a test
written against `YesCovers` could not pin it (W0.1's second table pins the call
sites instead). That is why the "unconditionally" paragraph that used to stand
here is gone and why `init_images.go` appears above at all: W5.1 writes it, so it
is a two-writer file from the moment D12 is built.
**What D12 does falsify in that file is a sentence of prose, and the draft that
withdrew the lane missed it.** The `runCovered` doc comment ends by listing the
Firestore database's location, a Cloud Build, replacing a registry tag and
onboarding an unlisted repository among the things that "stay `Typed`" — true of
`setup`, false of `update`, which is why this review round found the comment
false with no task able to correct it. So the lane is restored as a
comment-only one: W5.1 rewrites that clause to say the map answers for whole
stages and that a step finer than its stage is the caller's own confirmation,
placed where the step is taken — door-neutral, because
`internal/initflow` names no command (O2) — and leaves the map,
`RunCovers`, `YesCovers` and the canonical order exactly as they are.

W2.1 is a **sweep**, and the way it appears above is the precise version of the
old catch-all line: it writes **every `internal/cli/*.go` and every
`internal/cli/*_test.go` that names the retired command at L2** — today's 44
(48 with the three `init --repo`-only files and `init_review.go`, which the
`fugaro init` pattern misses) plus any file W1.2
adds that names it; the tombstone's `retired.go` and `retired_test.go` are
`retirementText` and are *not* renamed. That is why `W2.1 (L2)` appears in the
chains for `init.go`, `init_stages.go`, `restore.go`, `init_project.go`,
`init_inputs.go`, `init_firebase.go`, `init_images.go`, `init_repo_gate.go`,
`init_repo_target.go`, the goldens and the two `_test.go` groups: for those files
W2.1 is a real writer
that changes no behaviour, and a chain that left it out would put two writers of
one file in one level. **W1.2's own new files carry no `W2.1 (L2)` step** —
`setup.go`, `update.go` and `root.go` are written for the new commands and
`retired.go`'s one message names the old one *as retired*, which is
`retirementText` and is not renamed — but if W1.2 leaves the retired name in a
file that is not the tombstone, the L2 census names it and that file's chain
gains the step. The census test is what keeps this from being a memory.

**The rule this list enforces.** *Every file written by more than one task is
here, with its writers in level order, and no chain goes backwards* — and
**`### Edited (the shared ones)` is the same list written as a table, so the two
must agree file for file and writer for writer.** They disagreed before this
revision: the table dropped W3.5 from `init_stages.go` and `setup.go`'s chains
(it kept the lumped W1.2/W2.1/W5.1 row), and neither list carried
`internal/e2e/init_test.go` (W2.2 → W5.2) or `internal/cli/docs_*_test.go`
(W2.1 → W2.3), which made the "every file" claim false. The same revision went
wrong in the other direction on the neighbouring glob: it listed
`internal/cli/docs_website_*_test.go` (W2.1 → W2.4), but the six files that glob
matches name the command **nowhere**, so W2.1 never opens them and the row is
struck — they are W2.4's alone. A shared-file row for a file no census names is
the same defect as a missing row, read the other way. Three consequences a
reviewer should spot:
**W3.2 is at L5 because W3.1 writes `init_inputs.go` at L4**, not because of an
edge (its only edge is `← W1.3`); **W3.4 is at L5** for the same reason via
`setup.go`; and **W2.7 has a level to itself at L11**, after the last writer of
any `init*.go` file. The thinness is the honest shape of this work —
`internal/cli` is one lane, so the tasks that reshape the wizard follow one
another by file even where they do not depend on each other logically.

### The critical path

**W1.1 → W1.2 → W1.3 → W3.1 → W3.2 → W3.5 → W4.3 → W4.4 → W6.2 (owner)** —
nine steps, the last owner-run. That is the billing leg, not the rename leg:
**W3.1 → W3.2 is the `internal/cli/init_inputs.go` lane, not an edge** — the
edges give `W3.2 ← W1.3` only — and W3.1 and W3.2 are sequential because they
write one file, not because either waits on the other. The rename leg is shorter — W1.2 → W2.1 → W2.3 → W2.6 → W2.7 → W6.1 —
and W2.5 (Terraform) is free to start at once, since it depends on nothing.
Two things shorten the rest: W4.1 (the billing client) depends on no task — its
decision, **D6**, is answered — so it can be built beside the wizard while the
wizard is still being reshaped, and the owner-run checks
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
   to "`pendingRename` is empty and the only files left naming the command are
   the three whose subject is the retirement" — and **W2.3 before W2.4** inside
   that range, because W2.4 regenerates `website/content/security.md` from
   W2.3's `SECURITY.md` and `install.yaml`'s commands are held verbatim against
   W2.3's `README.md`.
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
on anything. W0.3 writes the checks' text, which **is D10's recommendation**,
and D10 was answered as recommended on 2026-10-20, so it is free to start too.*

### Task W0.1: The refusal matrix, pinned — test

**Files.** `internal/cli/setup_refusals_test.go` (**new**).
**What it pins, and it is two tables, not one.** The confirmations in this tree
are enforced in two places, and a pin that confuses them asserts something
false:

1. **The stage rows.** Which *stages* `--yes` covers, read from
   `internal/initflow/stage.go`'s `runCovered` map through
   `RunCovers(name) = runCovered[name] && YesCovers(name)`, and which stages
   declare `noYes`. Five are covered (`Installation`, `Firebase`, `Images`,
   `Installation2`, `Repository`) and three are not (`Project`, `Secrets`,
   `Compute`) — that is the whole of what the map says.
2. **The call-site rows.** The steps *within* a covered stage that are typed at
   the call site today, and that are **not** in the map: each is an
   `askTyped` or
   `canConfirmTyped(initflow.Typed, r.conditions())` guard in `internal/cli`
   (W5.1's table names the four, and D12's answer — three of them covered in
   **both** front doors, the Firestore database's location in `update` alone
   with `setup` keeping it typed — is where that table is flipped).
   **This is why the second table cannot be
   written with `YesCovers`**: the Firestore database's location rides stage
   `Firebase` (`promptLeft(initflow.Firebase, …)` in `init_firebase.go`) and a
   billable first image build rides `Images` — both stages `YesCovers` is
   **true** for, today and under any change to `runCovered`. Their pin is the
   call site, and the *shape* of the refusal differs by step — which is the
   second reason the table is per row rather than one rule over `LeftKind`:
   - **the Firestore location** (`init_firebase.go`'s `confirmLocation`) returns
     a `NeedsYouError` whose `Left` is `promptLeft(initflow.Firebase, …)` — kind
     `prompt` — and the `--json`/agent columns must not change that;
   - **a billable first image build** (`init.go`'s `buildImages`) and
     **replacing a registry tag** (`init_images.go`'s `confirmReplace`) go
     through `askTyped`, which returns `reachable == false` (nothing was asked,
     nothing applied) and whose caller turns that into
     `promptLeft(initflow.Images, …)`;
   - **onboarding an unlisted repository** is the one that is **not** always a
     `prompt`: the repository stage (`init_repo_stage.go`'s `authorize`) leaves
     the run with `onboardLeft` — kind `command` for an ordinary origin, kind
     `console` when the origin is rewritten or the session is a coding agent's —
     and the `--repo` door's `gateRepo` refuses with a plain `userErr` instead.
     A pin that asserted kind `prompt` for this row would be wrong today.
   Nothing is applied in any of those shapes, and that is the assertion that
   matters: `askTyped` returning `reachable == false` means nothing was asked
   (its caller leaves the step), a `NeedsYouError` stops the run in the
   `NeedsYou` state, and `gateRepo`'s `userErr` stops it with exit code 1 — three
   different ways of saying "this did not happen", which is why the table
   asserts each row's own shape rather than a single rule over `LeftKind`.

Both tables are crossed with five run modes — `--yes`, `--non-interactive`
(with and without `--yes`), `--json`, no terminal, and an agent session — and
assert for each the state the loop reaches. The file reuses today's harness
(`internal/initflow/loop_test.go`'s `TestYesDoesNotCoverCreateOrBilling` is the
stage table's seed) and adds the agent and `--json` columns, which no single
test covers today.
**Three rows may never be relaxed, and D12 says which.** After the owner's
answer the never-relaxed set is **creating the project, linking the billing
account and the Firestore database's location** — all three `setup`'s, the first
two refused outright by `update` (W5.1) and the third covered there. Project
creation and the billing link sit under `Project`, the `noYes` stage, **and**
behind the typed banners (`init_project.go`'s `projectStage.typed`), so for
those two both tables apply; the location has no stage row and is the call site
alone. Each row carries its **D12 provenance** in a comment — "the owner's
answer of 2026-10-20, Global Constraint 4" — so a later task that relaxes one
has a written rule in front of it.
**Four rows moved, and this pin names them rather than hiding them.** A
**billable first image build**, **replacing a registry tag** and **onboarding an
unlisted repository** are typed today and become **covered in both front doors**
(the answer keeps only three things typed, and none of them is these), while the
**Firestore database's location** becomes covered **in `update` only** — `setup`
keeps it, because `setup` is the door that asks for it as a first-time question.
This task
writes each as today's value with the target beside it (`covered after W5.1`, and
for the location `covered in update after W5.1`), so the file states the moves in
the commit that introduces them; **W5.1**
flips them and is where the change is asserted (the lane
`setup_refusals_test.go W0.1 (L0) → W5.1 (L7)`). Nothing else moves — in
particular **no stage row does**: D12 is a call-site answer, and a task that
found itself editing `runCovered` would have misread it.
**Red step.** None possible today: the pin passes on today's code **by design**,
because the rows that never relax are today's rows and the four that move are
recorded as today's value. The review checks it would fail if a row were
relaxed, by relaxing one locally and not committing it; W5.1's flip is where a
red step exists.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Refusal|YesCovers|Confirm' -count=1`.
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `cli: pin the confirmation matrix both front doors must pass (three rows may never be relaxed, four move)`

### Task W0.2: The rename census and its frozen list — test

**Files.** `internal/cli/docs_live_init_census_test.go` (**new**).
**What it pins.** D9's line, as a test that fails in **both** directions: it
walks the live roots (`README.md`, `CONTRIBUTING.md`, `SECURITY.md`, `docs/`
minus the dated roots, `plugin/`, `.claude/`, `website/`, `schemas/`, `scripts/`,
`internal/`, `images/`, `deploy/`), collects every file naming `fugaro init` or
`init --repo`, and asserts that set equals a checked-in `pendingRename` list plus
the `datedRecords` roots plus `retirementText`. A new occurrence fails (the set
grew); a renamed file that was not removed from `pendingRename` fails (the set
shrank). `SECURITY.md` is a root **file**, not a directory, and it is on the walk
because it names the command **seven** times (`grep -c 'fugaro init'
SECURITY.md`) and W2.3 renames it; every other root file and every top-level
directory that names it zero times is left off, so the walk and the rename's
task list cover the same set. The `datedRecords` list is **the ten dated roots,
and it is stated once, here**:
`docs/plans/`, `docs/releases/v`, `docs/design/`, `docs/credentials-service/`,
`docs/website/`, `docs/coordinator/`, `docs/in-job-review/`, `docs/mobile/`,
`docs/windows-cli/` and **`docs/setup-wizard/` itself** — matching
`plugin/readme_test.go`'s `TestReadmeNamesNoRetiredSkill` `skip` regex in
spirit. Every other task that mentions the exemption list repeats this one
verbatim rather than paraphrasing it, because a second copy is a second list
that can disagree. The rule is **"another artifact's dated plan record"**, and
it is applied uniformly rather than by folder: `docs/website/` is a whole second
artifact's record (`docs/website/README.md`, decisions D90–D109, dated
2026-10-16) whose `reconciliation.md` and `implementation-plan.md` name the
command 3 times between them; `docs/coordinator/`, `docs/in-job-review/` and
`docs/mobile/` each hold another artifact's `reconciliation.md` — the mobile
folder its `implementation-plan.md` too — exactly like the credentials-service
folder already on the list. Three of these nine were added by **D9's answer of
2026-10-20**, because leaving them out was the list contradicting its own rule:
they are the same kind of record as `docs/website/`, and a rename task editing
them would be rewriting another plan's history. The tenth,
**`docs/windows-cli/`**, is the same rule applied to a folder that did not
exist at `82d366af`: the census below is read there, so it cannot see the
folder, but the base holds it and it names the command **once**, in another
artifact's dated plan record (`docs/windows-cli/reconciliation.md`, F37).
Exempting a root that names the command
**zero** times stays forbidden in both directions, which is why
`docs/website/redesign/**` is not a root of its own and `docs/voice-module/**`
and `docs/pending-workflows/**` are not on the list: they name it nowhere.
The handful of *generated* files (`website/content/cli-reference.md`,
`website/content/security.md`) are asserted to be in `pendingRename` and are
removed from it by W2.4, which regenerates them rather than editing them.
**A second exemption class, `retirementText`, is needed for the flip to be
achievable**, and it is not a judgement call: it is **three** paths, and each
names `fugaro init` **as the retired verb**, which is its whole job and which no
rename can remove — `internal/cli/retired.go` (W1.2's new tombstone, whose one
message says `` `fugaro init` is now `fugaro setup` … ``),
`internal/cli/retired_test.go` (W1.2's new test, which asserts that message) and
this census file itself (whose `pendingRename` literal and whose scan pattern
are the string). None of the three exists at L0; the class is a list of paths
that **may** name the command, so an absent path contributes nothing and W1.2
adds the first two. They are listed separately, so "the live tree names `fugaro
init` nowhere" stays a claim with teeth: W2.6 asserts `pendingRename` is empty
and the files naming the command are exactly `datedRecords` plus these three.
**`internal/cli/testdata/init_retire.golden` is deliberately not among them**,
even though it names the command four times today: it is a *transcript*, not a
retirement statement, and all four are strings W2.1 renames (the
`retireNextCommand` line, the `--anchor` hint, the `--repo` rerun and the
`build.service_account` deprecation warning), so W1.2/W2.1 regenerate it with
`TestInitRepoRetireTranscriptGolden -update-retire` exactly like the other two
goldens. An exemption for a file the rename empties is the "wrong in one
direction" case Global Constraint 7 forbids.
**Two patterns, and `pendingRename` holds both.** The scan matches the
`fugaro init` pattern **and** the bare `init --repo` form, and the two do not
find the same files: at `82d366af` the first finds 367 files and the second
finds **15** that the first misses
(`comm -13 <(git grep -l 'fugaro init' 82d366af | sort) <(git grep -l -- 'init --repo' 82d366af | sort)`).
They are named file by file in [reconciliation.md](reconciliation.md) §4, and
they are in `pendingRename` from this task onward: `internal/cli/gitsecret.go`
(the one **live string** — a refusal that prints `%s init --repo %s`),
`internal/cli/shipped_gates_test.go` (the args `{"init", "--repo",
"--provision-batch"}` in a table, not a string), `internal/cli/{budget_preview,
config_publish}.go`, `internal/backend/backend.go`,
`internal/config/project_secrets.go`, `internal/runner/policy.go`,
`internal/cli/{launch_budget,secrets}_test.go`,
`internal/infra/{statebucket,workdir}_test.go`,
`internal/runner/{policy,provider_policy,providers}_test.go` — **fourteen** live
files — and `docs/design/layered-config.md`, which is a dated root and is
exempt (note that the `fugaro init` pattern does not count it in the 85, so it
is the one file in the dated set that the *other* pattern put there). Without
this the walk and the rename's task list do **not** cover the same set: those
fourteen files would sit in `pendingRename` owned by no task, and W2.6's "every
live file is owned by exactly one of W2.1–W2.5" would be false.
**Red step.** None: passes today with `pendingRename` = today's census at
`82d366af` — 410 non-test Go lines across 107 files, 378 test lines across 106
files, 111 markdown files outside this folder (**26 live** and **85 in the dated
roots**), 29 Terraform files naming the command of 58, **and the 14 live files
the `init --repo` pattern adds** — every figure reproducible
by the command stated beside it in
[reconciliation.md](reconciliation.md) §4. W2.6 **flips the last assertion to
`len(pendingRename) != 0 → fatal`**, and that is where the pin bites.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Census|RetiredInit' -count=1`.
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `cli: the census of every file that still names fugaro init, and the line the rename must not cross`

### Task W0.3: Checks 27 and 111, written into the register — docs

**Files.** `docs/gcp-live-checklist.md` (Check 27's section rewritten in place —
its commands renamed and the wizard's sequence put in front, **with every probe
it already carries kept**; Check 111's register row and section added; the
paragraph that records reserved numbers gains 111's claim).
**What the owner types.** Check 27: the hard-coded project guard (`${P:?}` on
every `gcloud`, installation `fugaro` only, **never `belong`**, a throwaway
project and a sandbox repository); the commands in the summary's table above,
each a line the owner can paste; `FACT:` lines for what the wizard asked, what
the picker listed (how many accounts, and whether the API or gcloud answered),
and the second `fugaro update`'s "No changes"; then the Restore (delete the
throwaway project, the bucket, the repository's jobs) whose final assertion
reads `0`. Check 111: the five modes, the guided line each prints, and a `FACT:`
line that nothing was enabled and no account list was printed.
**Rename the commands, keep the probes.** The rewrite is not a deletion: the
four Claude Code spikes (V1–V4), the image mirror against the real registries,
the hidden secret prompts and their refusals, the hostile-clone gate, `doctor`
and `update-skills`, the teammate's adopt run, `/fugaro:setup` and the
prompt-injection probe all stay in Check 27 with their commands renamed, and the
section's own `FACT:`/expected table keeps its rows. The reason is in the
register itself: `grep -n 'injection\|hostile' docs/gcp-live-checklist.md`
matches Check 27 and nothing else, so nothing else would run those two probes.
The red step for this task is that grep plus a count of the steps the old text
carries — the new section must have at least as many.
**And it settles the section's own status (F22).** Check 27 says **NOT RUN** and
carries the seventh live run's PASS/VERIFIED results in the same section. The
rewrite is where the two statements are made to agree: the recorded run stays as
the record of what the **old** commands proved, and the rewritten steps are
marked not yet run against `fugaro setup` and `fugaro update`. After the rewrite
the row, the heading, the prose and the results table say one thing.
**Tests.** None (a document). **Red step.** The two greps above: the numbered
steps of the old Check 27, and `grep -n 'injection\|hostile'
docs/gcp-live-checklist.md` — the new section must carry every one of them.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Checklist' -count=1`.
**Depends on.** Nothing — the task *is* D10's recommendation (rewrite Check 27
in place, take 111) and **D10 was answered as recommended on 2026-10-20**, so
the text it writes is the answer and not an assumption. It depends on no *other
task*; the checks describe the target state and are **not run** until W6.1 and
W6.2. **Reviewer.** standard.
**Commit.** `docs(gcp-live-checklist): Check 27 rewritten for fugaro setup, and Check 111 for the billing picker's refusals`

---

## Slice 1 — The two front doors

### Task W1.1: `initflow` stops naming a command — opus

**Files.** `internal/initflow/loop.go`, `internal/initflow/loop_test.go`, and
`internal/initflow/stage.go` — its package comment's first line, "Package
initflow is the converge loop behind `fugaro init`", is a mention no other task
owns. (W5.1 writes one more line in `stage.go`, the `runCovered` comment; this
task is still the file's first writer, and the two edits do not overlap.)
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
`docs_base_test.go` (its `"fugaro init": newInitCmd` map key),
`init_stages.go` — `converge()` passes the front door's name into W1.1's
`Options.Program`, which is the one line W1.1 leaves for this task — and the
three `testdata/*.golden` files regenerated by their own tests with their own
flags (Convention 2).
**What changes.** `fugaro setup` = the wizard; `fugaro update` = the re-converge;
both take the flags that belong to them, and a flag that only makes sense in one
(e.g. `--create-project`, `--link-billing`) is refused in the other with a
message naming the right one. The tombstone: a **hidden** `init` command, no
flags of its own, that applies nothing and exits non-zero saying `` `fugaro init`
is now `fugaro setup` (first run) and `fugaro update` (after a CLI upgrade) ``.
D2 is the decision here, and it was **answered as recommended on 2026-10-20**:
the tombstone, not an alias. An alias would be a different version of this task,
and it is not being built.
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
offering to create a second one. **D11 was answered as recommended on
2026-10-20: `setup` creates by default**, which is what the paragraph above
builds.
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
**Depends on.** W1.2 (D11's answer, in hand). **Reviewer.** opus.
**Commit.** `cli: fugaro setup asks whether to create the project, defaults to creating it, and degrades to update on an installation that exists`

---

## Slice 2 — The rename

*The mechanical majority. Each task leaves the tree green: a partial rename is a
failing pin, and the pin's failure names the next sentence.*

### Task W2.1: The `internal/cli` strings — standard

**Files.** **48** `internal/cli/*.go`. The list is the census, not a
recollection of it, so it is stated with its command — `git grep -l 'fugaro init'
82d366af -- 'internal/cli/*.go' | grep -v _test.go | wc -l` says **44** — plus
the four files the census's pattern misses and this task still owns: the **3**
that name the command only as the bare `init --repo`, named with their reasons
below, and `init_review.go`, added on purpose: 44 + 3 + 1 = **48**.

```
allow_delete.go     budget.go           cloud.go            config_list.go
doctor.go           doctor_bucket.go    exec.go             history_rollover.go
image.go            image_layer.go      image_refresh.go    image_refresh_rules.go
image_refresh_steps.go  imagecheck.go   init.go             init_adopt.go
init_anchor.go      init_firebase.go    init_fugaroyaml.go  init_images.go
init_inputs.go      init_plugin_stage.go init_project.go    init_repo_gate.go
init_repo_stage.go  init_repo_target.go init_retire.go      init_stages.go
initsecrets.go      launch_budget.go    lswarn.go           managed_flag.go
operator_write.go   project.go          project_cmd.go      recipes_skew.go
report.go           restore.go          roles.go            secrets.go
sharedcfg.go        upgrade.go          upgrade_cloud.go    validate.go
budget_preview.go   config_publish.go   gitsecret.go
```

`init_retire.go` is in the list for its `retireNextCommand` value, the literal
`"fugaro init --repo . --retire"`. The last line is the second census pattern
(reconciliation §4 names it): `budget_preview.go`, `config_publish.go` and
`gitsecret.go` name `init --repo` with no `fugaro` in front, so the command
above does not find them — and **`gitsecret.go`'s `driftError.Error()` is live
user-facing text**,
a refusal that ends "Run %s init --repo %s in its checkout first". They are in
the list, in `pendingRename` (W0.2) and therefore owned; leaving them to the
sweep is exactly how they came to sit in the census owned by nobody.
The 48th file is **`init_review.go`**, and it
is named separately because the census command does **not** match it either: its command
line is built as `args := []string{"init"}` with the program name supplied
elsewhere, so `fugaro init` never appears in the file. It is still this task's —
the review screen prints the command it is telling the user to run — and leaving
it out of the list while relying on the sweep to catch it is exactly the mistake
the reconciliation's §4 census was re-derived to fix. A file the sweep finds and
this list does not is a file whose strings nobody chose deliberately.
It touches **every one of these files**, plus the three goldens regenerated by
their own tests and the **67** `internal/cli/*_test.go` files that name the
command (`git grep -l 'fugaro init' 82d366af -- 'internal/cli/*_test.go' | wc
-l`, **64**, plus `launch_budget_test.go`, `secrets_test.go` and
`shipped_gates_test.go`, the three the `init --repo` pattern adds — the last of
them not an assertion string but the argument list `{"init", "--repo",
"--provision-batch"}` passed to the command tree), whose assertions read the
strings being renamed — see W2.2 for why those
two sets of test files are written here *and* by W2.3/W2.4.
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
**Depends on.** W1.2 (D7's answer, in hand). **Reviewer.** standard.
**Commit.** `cli: every message names fugaro setup, fugaro update or fugaro upgrade, whichever the reader is doing`

### Task W2.2: The strings outside `internal/cli` — standard

**Files.** **Every non-test `.go` file outside `internal/cli` that names the
command**, in these packages — an enumeration is the wrong shape for a sweep
this wide, so the rule is the list and `grep` is the check:

```
internal/infra/** (incl. tf/ and managed/)   internal/budget/**
internal/runner/**                          internal/managed/**
internal/config/** (except example.go)      internal/localcfg/**
internal/projectpolicy/**                   internal/preflight/**
internal/backend/gcp/**                     internal/backend/backendtest/**
internal/gcpfake/**                         internal/gateway/**
internal/imagecheck/**                      internal/mirror/**
internal/basepin/**
```

`grep -rl 'fugaro init' internal/ --include='*.go' | grep -v _test.go | grep -v
'^internal/cli/' | grep -v '^internal/initflow/' | grep -v
'^internal/config/example.go$'` is the exact command — the last two filters are
the two exclusions below, and without them it names `internal/initflow/loop.go`,
`internal/initflow/stage.go` and `internal/config/example.go`, which are not
this task's; if it names a package not listed above, this list is what is wrong.
**Three files are this task's without being on that command's output**, because
they name the command only as the bare `init --repo` (reconciliation §4's second
pattern): `internal/backend/backend.go` (`LaunchSpec.Env`'s comment),
`internal/config/project_secrets.go` (`PerRepositorySecret`'s doc comment) and
`internal/runner/policy.go` (`envCeiling`'s doc comment), all
three comments rather than strings — the same three that made W2.6's ownership
sentence false. The five `_test.go` files beside them
(`internal/infra/{statebucket,workdir}_test.go`,
`internal/runner/{policy,provider_policy,providers}_test.go`) are in the Tests
line below for the same reason.
Named examples of the user-facing ones:
`internal/infra/{discover,discover_firebase,firebase,firebasedb,hardening,installation,projectcreate,projectjobs,readiness,checkjob,tf/env,tf/managed_cover}.go`,
`internal/budget/{rollover,registry,session}.go`,
`internal/runner/{project,projectpolicy,projectjob,halt,gateway,budget_session}.go`,
`internal/managed/budget.go`, `internal/localcfg/{localcfg,select}.go`,
`internal/config/{config,providers,routing}.go`,
`internal/projectpolicy/{read,profiles}.go`,
`internal/preflight/google.go`, `internal/backend/gcp/run.go`.
**Two entries in this list are not this task's**: `internal/config`'s
`example.go` (W2.5 owns it, and no file has two writers in one level) and
`internal/initflow` entire — its three files that name the command
(`loop.go`, `loop_test.go` and `stage.go`) are all W1.1's, so the sweep skips
`internal/initflow/**` and takes `internal/config/**` minus `example.go`.
**Which name each gets.** The same rule as W2.1. Note two families: the
*operator* strings ("an operator runs fugaro init (0.7.0 or later)") become
`` `fugaro update` `` — the operator's job after an upgrade — and the
*repository* strings ("run fugaro init --repo in the checkout") become
`` `fugaro update` in the checkout ``. `internal/infra/projectcreate.go`'s
`"rerun fugaro init --create-project"` becomes `"rerun fugaro setup"`, and its
head comment is W4.5's.
**Tests.** **The 42 test files outside `internal/cli` that assert those strings**
— not the 106 the reconciliation counts tree-wide, which is the number *with*
`internal/cli`'s 64 and the three plugin files in it. The derivation, each figure
with its command in §4: 106 tree-wide
(`git grep -l 'fugaro init' 82d366af -- '*_test.go' | wc -l`), **64** of them in
`internal/cli` (W2.1's, and the previous paragraph is about the split inside
them), leaving **42** outside; of those, **3** are `plugin/`'s and belong to
W2.3 (`plugin/readme_test.go`, `plugin/setup_skill_test.go`,
`plugin/skills_lint_test.go`), **1** is `internal/initflow/loop_test.go`
(W1.1's) and **1** is `deploy/terraform/terraform_test.go` (W2.5's) — so **37**
are this task's, and to them the `init --repo` pattern adds the **5** test files
no `fugaro init` grep finds (`internal/infra/{statebucket,workdir}_test.go`,
`internal/runner/{policy,provider_policy,providers}_test.go`), for **42**
test files this task updates. **The two 42s are different sets, and the
coincidence is worth stating**: 37 + 5 = 42, and 42 is also the count of test
files *matching `fugaro init` outside `internal/cli`* — the five added files are
not among that 42, since by construction they do not match that pattern. Every
one included above is updated
in this commit — **except the two sets that belong to later tasks**, and this is
the only place the split is written down. Inside `internal/cli`, the
`docs_*_test.go` files are W2.1's strings and W2.3's sentences, so they chain
`W2.1 (L2) → W2.3 (L3)` and are updated there — but only the **12** of the 44
that name the command (`git grep -l -E 'fugaro init|init --repo' 82d366af --
'internal/cli/docs_*_test.go'`); the other 32 name it nowhere and this task
leaves them alone, the six `docs_website_*_test.go` among them (the three
website pins that do move, `docs_website_{install,cliref,security}_test.go`, are
W2.4's and are listed there). And **`internal/e2e/init_test.go`**, whose 26
`TestInitRepo*` tests are the closest thing to a whole-run test this repository
has, is updated here for
its strings and then **renamed** by W5.2 — the lane
`internal/e2e/init_test.go W2.2 (L2) → W5.2 (L8)` is that file's and is why
W5.2's diff is a rename rather than a rewrite. `internal/e2e/live_budget_test.go`
is this task's alone.
**Red step.** `GOFLAGS=-p=2 go test ./internal/infra/... ./internal/budget/... ./internal/runner/... -count=1` before the edit — it is green; after the edit but before the test text, it names each assertion.
**Verification.** `GOFLAGS=-p=2 go test ./internal/... -count=1` and `fugaro verify test`.
**Depends on.** W1.2. **Reviewer.** standard.
**Commit.** `infra, budget, runner: the messages name fugaro update or fugaro setup, not fugaro init`

### Task W2.3: Live markdown, README and the skills — standard

**Files.** `README.md` (16), `CONTRIBUTING.md`, `docs/gcp-setup.md` (113 —
including its title
`# GCP setup with \`fugaro init\`` and its
`## The short path: \`fugaro init\`, then \`/fugaro:setup\`` section),
`docs/project-layer.md`, `docs/project-claude-accounts.md`,
`docs/multi-model.md`, `docs/git-providers.md`, `docs/dogfooding.md`,
`docs/release.md`, `SECURITY.md`, `docs/checkout.md`, `docs/backends.md`,
`docs/base-image.md`, `docs/base-image-migration.md`, `docs/recipes.md`,
`docs/releases/README.md` (the releases index — live; the dated records beside
it are `docs/releases/v*`, which the ten roots exempt),
`plugin/skills/setup/SKILL.md` and
`plugin/skills/setup/reference/**`, `plugin/skills/working/reference/status.md`,
`.claude/skills/new-release/SKILL.md`; and the pins updated in the same commit:
`plugin/readme_test.go`'s `TestReadmeGettingStartedIsTwoSteps` (step 1,
"`fugaro init` again"), `plugin/setup_skill_test.go`'s
`TestSetupSkillMentionsDoctorFirst`, `TestSetupSkillNeverAppliesInit`,
`TestSetupSkillContent` and `TestSetupSkillHandlesSeveralProjects`,
`plugin/skills_lint_test.go`'s `userRunsAllowed` list and its `--yes` rule,
`internal/cli/docs_project_wide_git_test.go` (the literal "Init's prompts (step
9) have no `--repo` to pass"), `internal/cli/docs_upgrade_test.go`,
`internal/cli/docs_base_test.go`, and the **12** `docs_*_test.go` files whose
sentences move — of the 44 that glob matches, the ones that name the command
(`git grep -l -E 'fugaro init|init --repo' 82d366af -- 'internal/cli/docs_*_test.go'`).
**What changes beyond the word (F17, D8).** The README's two steps stay two
steps, with step 1 now `` `fugaro setup` ``; `docs/gcp-setup.md`'s title and
short path follow; the skill keeps its name and its order (it runs **after** the
wizard) and its opening line says which one it is — "`fugaro setup` did the
cloud; this skill writes `fugaro.yaml` and opens the pull request" — and every
`fugaro init --repo --plan-only` / `fugaro init --repo --retire` it hands the
user becomes `fugaro update --plan-only` / `fugaro update --retire`.
**What does not change.** Every sentence that is a *claim about behaviour*
("`--yes` never covers creating a project") stays true and stays; only the
command's name moves. The dated records are untouched (Convention 11) —
including **`docs/coordinator/**`, `docs/mobile/**` and
`docs/in-job-review/**`**, which this task's list carried until **D9 was
answered on 2026-10-20**: each holds another artifact's `reconciliation.md` (the
mobile one its `implementation-plan.md` too), so they are dated records and not
live guides, and the rename does not enter them. `docs/website/` was already on
that side for the same reason.
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
together, and `website/test/caps-and-costs.mjs` resolves them),
`website/src/components/Changelog.astro`, `website/test/caps-and-costs.mjs`
(**not** `website/test/docs-hub.mjs`: its 450 lines name the command zero
times), `website/README.md` (one mention, inside a quoted test description —
"rendered \"fugaro init in your own terminal.\"" — so the quote moves with the
test), `website/content/cli-reference.md`
(**regenerated** with `go run ./internal/clidoc`, never hand-edited),
`website/content/security.md` (**regenerated** by `internal/clidoc/secdoc.go`
from `SECURITY.md`).
**What changes beyond the word.** The Getting started page's seven steps become
the brief's shorter sequence — one command, and the wizard asks the rest — and
its prose about passing flags goes. `website/data/install.yaml` is read as bytes
by `internal/cli/docs_website_install_test.go`, which asserts **containment, not
a byte comparison**: every command in it must appear verbatim in the file its
`source:` names. The pin moves in this commit — and W2.3's `README.md` is one of
the two files those `source:` lines name, which is why this task depends on it.
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
**Depends on.** W2.1, W2.2, **W2.3**. W2.3 is a real edge and not just a shared
file: this task regenerates `website/content/security.md` from `SECURITY.md`
(W2.3's), and `TestWebsiteInstallFactsMatchTheRepository` requires every command
in `website/data/install.yaml` to appear verbatim in the file its `source:`
names — `README.md`'s Getting started, which is also W2.3's. Landing this task
first would leave the tree red, which is why Slice 2's answer is "Yes, **in the
order given**" and not "Yes". **Reviewer.** standard.
**Commit.** `website: the getting-started sequence is fugaro setup, and the CLI reference is regenerated`

### Task W2.5: Terraform, the schema and the example config — standard

**Files.** 29 of the tree's 58 `deploy/terraform/**/*.tf` files — every `.tf`
file that names the command (`grep -rl 'fugaro init' deploy/terraform
--include='*.tf' | wc -l`). The mentions are mixed, and the task's rule is
"rename the mention, move nothing": **15 `description = …` attributes** on
variables and outputs (`grep -rn 'description' deploy/terraform --include='*.tf'
| grep -c 'fugaro init'`), **8 `error_message`s** in `validation` blocks, and the
rest comments — e.g.
`deploy/terraform/gcp/modules/repo/variables.tf`, `modules/installation/*.tf`,
`modules/firebase/*.tf`, `modules/workflow/*.tf`, `modules/project/job.tf`,
`roots/*/{main,variables,outputs}.tf`), `deploy/terraform/terraform.go`'s
package comment, `deploy/terraform/gcp/roots/repo/tests/testdata/README.md`,
`internal/config/example.go`'s `exampleProjectLines` +
`internal/config/example.yaml` (the comment line naming `--config-only`'s
writer), `schemas/fugaro.schema.json` (`gcp_project`'s `description`),
`images/verify-public.sh`, `images/history/Dockerfile` (one comment: "docker
push it (fugaro init --firebase prints the commands)"), and the four
`deploy/terraform/gcp/roots/{compute,firebase,installation,repo}/tests/*.tftest.hcl`
files, whose header comments say the names are "the ones `fugaro init` writes".
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
**Red step.** `GOFLAGS=-p=2 go test ./deploy/... ./internal/config/... -count=1` before the edit — any failure names a compared description, which is then changed on purpose. The 15 `description` lines are the ones a test could byte-compare; the 8 `error_message`s and the comments are text no test reads, and the golden tests are what would say otherwise.
**Verification.** As the red step, after the edit; plus
`terraform fmt -check -recursive deploy/terraform` (a description edit must not
reflow).
**Depends on.** Nothing. **Reviewer.** standard.
**Commit.** `terraform, schema and the example config: the descriptions name fugaro setup or fugaro update; no resource moves`

### Task W2.6: The census emptied, and the tail — standard

**Files.** `internal/cli/docs_live_init_census_test.go` (W0.2's file: the final
assertion flips to "`pendingRename` is empty, and the only files left naming the
command are `retirementText`'s three — the tombstone, its test and this file"),
plus `docs/gcp-live-checklist.md`
— its remaining prose beyond Checks 27 and 111, which W0.3 wrote and which
already name the new commands.
**The leftover set is closed, and this is the whole of it.** Once W2.1–W2.5 have
landed, the only files outside the ten exempt roots and the three
`retirementText` files that still name the command are `docs/gcp-live-checklist.md`
and — if W2.5 has not landed yet — `schemas/fugaro.schema.json`,
`internal/config/example.{go,yaml}` and `images/verify-public.sh`. **Every live
file is owned by exactly one of W2.1–W2.5**, and that now includes the **14 live
files that name the command only as the bare `init --repo`** — six in
`internal/cli` (W2.1: `gitsecret.go`, `budget_preview.go`, `config_publish.go`
and their three test files), eight elsewhere (W2.2:
`internal/backend/backend.go`, `internal/config/project_secrets.go`,
`internal/runner/policy.go` and the five tests beside them),
which is exactly the gap that made this sentence false before — see
[reconciliation.md](reconciliation.md) §4's second-pattern paragraph and
`### The file lanes` for the
files with more than one writer; anything else the red step prints is a file a
rename task missed and belongs to that task, not to this one.
The exempt roots are **exactly W0.2's ten, no more and no fewer** —
`docs/plans/`, `docs/releases/v`, `docs/design/`, `docs/credentials-service/`,
`docs/website/`, `docs/coordinator/`, `docs/in-job-review/`, `docs/mobile/`,
`docs/windows-cli/` and `docs/setup-wizard/` — and this task does not restate
them from memory: the guard's `datedRecords` is one constant, and W0.2 is where
it is written. Six of the ten are another artifact's plan record
(`docs/website/`, the three D9 added, `docs/credentials-service/` and the
`docs/windows-cli/` folder the review's finding added — it postdates
`82d366af`, so a census read there cannot see it), and
if the flip prints a file inside one of them, the constant is wrong — not the
file. `docs/setup-wizard/` is among them because it is where the retirement
is described (four documents, every one of them; the count is volatile, so the
command: `git grep -o -c 'fugaro init' -- docs/setup-wizard`). `docs/website/redesign/**` is
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
**Depends on.** W1.3 (D4's answer, in hand). **Reviewer.** standard.
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
**Depends on.** W1.3 (D4's answer, in hand). **Reviewer.** standard.
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
**Depends on.** W1.3 (D7's answer, in hand). **Reviewer.** standard.
**Commit.** `cli: --firebase-project, defaulting to the project's own id; the separate Firebase project stays an advanced flag`

### Task W3.4: The first-repository question — standard

**Files.** `internal/cli/setup.go`, `internal/cli/init_repo_gate.go`
(`checkoutNote`, `unknownDetail`), `internal/cli/init_repo_target.go`
(`resolveRepoTarget`'s four refusal texts), and their tests.
**What changes (F14, D5).** Inside a checkout, `setup` asks one extra question —
"onboard `<owner/name>`?" — defaulting to yes, and uses the existing repository
stage; outside a checkout it skips the question and prints one line saying how to
enrol one later. `update` never enrols silently (W5.1 owns that side). The four
`resolveRepoTarget` refusals keep their substance and change their verb — and
one row moves: onboarding a repository the run was not told about is **covered
by `--yes`** after D12, where it was `Typed` before, so this task asserts the
new row and W5.1's matrix row is the same change seen from `update`'s side.
**Tests.** `TestSetupAsksToOnboardTheCheckoutsRepository`,
`TestSetupOutsideACheckoutSaysHowToEnrol` (the text names `fugaro setup`),
`TestSetupOnboardsUnderYesWithoutATypedConfirmation` (**D12's answer**: an
unlisted repository was `Typed` before 2026-10-20 and is covered now, in both
front doors — W5.1 owns the row and flips it),
`TestSetupOnboardingIsASeparateAnswerFromTheProjectQuestion`.
**Red step.** `TestSetupAsksToOnboardTheCheckoutsRepository` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Repo|Checkout|Setup' -count=1`.
**Depends on.** W1.3 (D5's answer, in hand). **Reviewer.** standard.
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

*D6 (the billing list) and D3 (may the wizard enable the Cloud Billing API?)
were both answered as recommended on 2026-10-20, so nothing here waits: the
picker lists open accounts at a real terminal only, and an API is enabled only
behind a typed confirmation the owner gives in the run.*

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
**Depends on.** Nothing — the task *is* D6's recommendation (its head comment
writes the reversal of `internal/infra/projectcreate.go`'s rule down) and **D6
was answered as recommended on 2026-10-20**, so what it writes is the answer,
exactly as W0.3 writes D10's. It depends on no *other task*. **Reviewer.** opus + **mandatory independent review** (it is the one new surface that reveals what a user can reach).
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
**Depends on.** W4.2, W3.5, W1.3 (D6's answer, in hand). **Reviewer.** opus + **mandatory independent review**.
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
**Depends on.** W4.3 (D3's answer, in hand). **Reviewer.** opus + **mandatory independent review** (it is the only new surface that can change a third project).
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
`update` registers), **the four call sites D12's answer moves** —
`internal/cli/init_firebase.go` (`confirmLocation`, the Firestore database's
location), `internal/cli/init_images.go` (`confirmReplace`, a registry tag),
`internal/cli/init_repo_gate.go` (`confirmRepo`, the typed owner/name),
`internal/cli/init.go` (`buildImages`' Cloud Build confirmation, and the
`errNoState` family's wording) — `internal/cli/setup_refusals_test.go` (W0.1's
file: `update`'s column, and the call-site rows D12 moves are flipped here),
`docs/gcp-setup.md` (its `fugaro update` section), their tests, and
**`internal/initflow/stage.go`'s `runCovered` doc comment — the comment only,
and this is the whole of D12's footprint in `internal/initflow`**.
**In that file, the comment is the only thing that moves.** The contract below
says why — `runCovered` is a five-entry map of stage names and there is no
within-stage rule in it to change, so D12 is implemented at the four call sites
above, and `stage.go`'s behaviour (`RunCovers`, `YesCovers`, `noYes`, the
canonical order) is read and not written. What the answer does falsify is the
map's doc comment, whose last clause lists the Firestore database's location, a
Cloud Build, replacing a registry tag and onboarding an unlisted repository
among the steps that "stay `Typed`": true of `setup`, false of `update`. The
corrected clause says the map answers for whole stages and that a step finer
than its stage is the confirmation of the caller that takes it, placed where the
step is taken — door-neutral, because `internal/initflow` names no command (O2,
W1.1). The lane
`internal/initflow/stage.go W1.1 (L0) → W5.1 (L7)` written in an earlier draft
was **withdrawn as a code change, and is restored here as this comment-only
one** — it was drawn on the assumption that D12 would be a `runCovered` change,
which is not where these steps live.
**What it is (F11, F16, D1, D5).** `update` registers everything `setup` does
**except** the project stage's creation path and `computeStage`: preflight,
installation, Firebase, images, installation (second pass), secrets, plugin, and
each enrolled repository. There is **no `services` stage** — `initflow.Services`
is a slot in the canonical order (`internal/initflow/stage.go`) that
`internal/cli` never registers, and this list is built from `converge()`'s, not
from `Names()`. Its refusals, each with a test:
- `--create-project` and `--link-billing` → refused, naming `fugaro setup`;
- no installation → refused, naming `fugaro setup`
  (`"there is no Terraform state bucket gs://%s, so no installation"`);
- an unenrolled checkout → not silently enrolled: one line naming `fugaro
  setup`, and exit code 1 (`left for you`), which `Result.ExitCode()` already
  means.
And its contract. **This is D12's answer of 2026-10-20, which overruled the
recommendation this task was first drafted against**, so read it here rather
than from §5's recommendation: `update` under `--yes` covers **everything
`update` owns** — the five stages `internal/initflow/stage.go`'s `runCovered`
names, `Installation`, `Firebase`, `Images`, `Installation2` and `Repository`,
**and** every step *within* them that is money or permanent. A Cloud Build, the
Firestore database's location, replacing a registry tag and onboarding an
unlisted repository are **covered when `update` asks for them**: a CI job that
upgrades the CLI and has to rebuild an image gets the build, not `needs-you` and
exit code 1. `update --yes` is total over its own stage set; what it does **not**
own it cannot cover, and the three rows that may never be relaxed are all
`setup`'s: **creating the project** and **linking the billing account** — which
`update` refuses outright and never reaches — and **the Firestore database's
location**, which `setup` keeps `Typed` and `update` covers. `setup` is
therefore where the confirmations live.

**Where that is implemented, and it is not `runCovered`.** `runCovered` is a map
of **five stage names** read once, by
`RunCovers(name) = runCovered[name] && YesCovers(name)`: it says whether `--yes`
covers a whole *stage*, and it has no within-stage rule to change. The four
steps D12 moves are per-call-site guards inside the stage's own apply, and each
of them is `askTyped` or `canConfirmTyped(initflow.Typed, r.conditions())`:

| Step | Where the guard is | Today |
|---|---|---|
| the Firestore database's location | `internal/cli/init_firebase.go`'s `confirmLocation` | typed (its `Left` rides stage `Firebase`, whose `YesCovers` is **true**) |
| a billable first image build | `internal/cli/init.go`'s `buildImages` | typed |
| replacing a registry tag | `internal/cli/init_images.go`'s `confirmReplace` | typed |
| onboarding an unlisted repository | `internal/cli/init_repo_gate.go`'s `confirmRepo` | typed (owner/name typed at the prompt) |

So this task changes those **four call sites** — making the ones `update` owns
apply under `--yes`, and leaving `setup`'s typed — and it changes nothing in
`internal/initflow/stage.go` but that map's doc comment, which this answer
falsifies: the three `noYes` stages
(`Project`, `Secrets`, `Compute`) and the five covered ones stay exactly as they
are, and so do the map, `RunCovers` and the canonical order. No test can pin a
comment, so the corrected sentence is a **review item** rather than a test: it
must say that the map answers for whole stages and that the four steps above are
each their caller's own confirmation, and it must name no front door — W1.1's
guard over the package is the test that holds the second half. The per-door half
of the answer is the reason no single shared rule can
carry it: the same call site must behave differently in the two front doors, so
the guard has to know which door is running (W1.1's `Options.Program` names it,
and `initRun` can carry it). The task's own call is how that door reaches
`r.conditions()`; the review's question is whether the call site can still say
*why* it is typed, because that is the sentence the next reader needs.
W0.1's file carries the stage-level rows and the call-site rows as two separate
assertions (see its own entry), and the rows D12 moves are flipped there in this
commit.
`update` with neither `--yes` nor a terminal plans only, as `initflow.Run`
already says.
**Tests.** `TestUpdateRefusesCreateProjectAndLinkBilling`,
`TestUpdateWithoutAnInstallationNamesSetup`,
`TestUpdateLeavesAnUnenrolledRepositoryAloneAndExitsOne`,
`TestUpdateUnderYesCoversEveryStageItOwns` (every stage `update` registers
applies under `--yes`), and **`TestUpdateAppliesTheFourStepsItOwns`** — the
call-site half, and the part D12 wrote: `update --yes` applies the Cloud Build,
the Firestore location, the registry-tag replacement and the unlisted
repository's onboarding with no typed confirmation, each asserted at its own
call site rather than through `YesCovers`; **planting the bug** in either
direction must fail it — covering a call site that must stay `Typed` in
`setup`, and leaving one `Typed` in `update` that D12 covers — and the first of
those fails W0.1's file too,
`TestUpdateNonInteractiveWithoutYesPlansOnly`,
`TestUpdateConvergesTwiceAndSaysNoChanges` (the second run's every stage is
`done`/"No changes", which is Check 27's assertion in miniature),
`TestUpdateNeverCreatesAnything` (the project-creation client is never reached).
**Red step.** `TestUpdateRefusesCreateProjectAndLinkBilling` first.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Update' -count=1 && fugaro verify test`.
**Depends on.** W1.3 (its D1, D5 and **D12** answers are in hand, D12 in the
form above). **Reviewer.** opus + **mandatory independent review** — it is the
one task in this plan that relaxes a confirmation (Convention 7).
**Commit.** `cli: fugaro update re-converges what exists, refuses to create anything, and covers every step it owns under --yes`

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
**Depends on.** W2.7, W3.5, W5.1, W5.2, W4.5, and a release. **Verification.** The
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
  names say `init`** — the comment-only tail of W2.2's sweep, which renames no
  string and is therefore not worth a commit on its own — after W2.7's
  precedent.

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
- **D12's answer is the newest thing in the plan, and it is the one that moves
  behaviour rather than text.** `update --yes` now covers a Cloud Build, the
  Firestore location and a registry-tag replacement, and `setup` keeps the three
  rows that may never be relaxed. The risk is not the answer but its **reach** —
  and, as with every other part of D12, the reach is at the **call sites**: W5.1
  relaxes `confirmLocation` (in `update` only),
  `buildImages`, `confirmReplace` and `confirmRepo`, W0.1's matrix has to be
  flipped in the same commit, and W3.4's onboarding test changes with it. The
  one place it writes in `internal/initflow` is `runCovered`'s doc comment,
  which its answer makes false — prose, not the map. Two of
  those four steps ride stages (`Firebase`, `Images`) whose `YesCovers` is
  already true, so a task that found itself editing `runCovered` would have read
  the answer wrong — that misreading is the specific mistake this bullet warns
  about. The mitigation is that the answer is written out row by row in §5 and
  that W0.1's file carries D12's provenance in a comment beside each row, so the
  diff says which rows moved and why.
- **The build is approved; the tasks are not started.** D1–D12 are answered, so
  nothing here is waiting on a decision — but Global Constraint 2 still asks for
  a go per task or group, and the slivers that need no behaviour decision are
  W0.1, W0.2 and W2.5.

---

## OWNER APPROVAL GATE

**Passed 2026-10-20. The plan may be built.** D1–D12 in
[reconciliation.md](reconciliation.md) §5 are **answered** — eleven as
recommended, **D12 the other way** — so nothing below waits on a decision any
more: the status line of this file is *approved to build; implementation in
progress*. Two things the gate does **not** do. It does not approve every task at
once: to start one the owner names it (or a slice) and says go (Global
Constraint 2). And it does not make the answers advisory: a task whose text
still reads like the recommendation — **W5.1 is the one that did**, and its entry
above now carries D12's answer instead — is built to the answer, and the
reconciliation's §5 paragraph is the contract.

The tasks that wait on no decision and no other task are **W0.1, W0.2, W1.1,
W2.5, W4.1 and W0.3** — the two pins, the loop's one change, the Terraform
descriptions, the billing client (D6) and the checks' text (D10). Everything
else waits only on the tasks named in `### The edges`.
