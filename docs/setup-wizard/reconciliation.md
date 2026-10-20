# Reconciliation — `fugaro setup` and `fugaro update`

**Status: plan only; nothing is implemented; decisions open.**

Oct 20, 2026 · @Dimi

What the brief asks for, set against what the tree does today. Every finding has
a path and a symbol (never a line number, which moves). Where this document
disagrees with [spec.md](spec.md), **this document wins**: spec.md is the owner's
words, this is what the code says about them.

## Checked against

`main` at `82d366af`. Read in full or in the parts that matter:

- `internal/cli/init.go`, `init_inputs.go`, `init_project.go`, `init_stages.go`,
  `init_review.go`, `init_firebase.go`, `init_repo_stage.go`, `init_repo_gate.go`,
  `init_repo_target.go`, `init_anchor.go`, `init_plugin_stage.go`, `init_images.go`,
  `init_retire.go`, `init_adopt.go`, `init_fugaroyaml.go`, `initsecrets.go`,
  `restore.go`, `upgrade.go`, `upgrade_cloud.go`, `doctor.go`, `doctor_bucket.go`,
  `cloud.go`, `configcmd.go`, `config_init.go`
- `internal/initflow/{stage.go,loop.go}` — the converge loop
- `internal/infra/{projectcreate.go,firebase.go,firebasedb.go,discover.go,hardening.go,projectjobs.go,readiness.go,tf/env.go}`
- `internal/gcpfake/billing.go`, `internal/preflight/{preflight.go,google.go}`
- `internal/clidoc/{cliref.go,secdoc.go}`, `website/content/cli-reference.md`
- `docs/gcp-setup.md`, `docs/design/m11-setup-and-skills.md`,
  `docs/plans/2026-10-04-m11-setup-and-skills.md`, `docs/gcp-live-checklist.md`
- `plugin/skills/setup/`, `plugin/skills/upgrade/`, `plugin/skills_lint_test.go`,
  `plugin/setup_skill_test.go`, `plugin/readme_test.go`
- `README.md`, `website/data/install.yaml`, `website/src/pages/getting-started.astro`
- the docs-pin tests under `internal/cli/docs_*_test.go`, `internal/clidoc/*_test.go`

---

## 1. The headline

The brief reads as if `fugaro setup` has to be built. It mostly does not: the
stage engine, the converge loop, the interactive prompts, the typed
confirmations, the refusals and the read-only preview all exist and are
tested. What does not exist is the **shape**: one command name for two jobs,
no defaults for two of the three inputs, and no billing list.

| What the brief asks for | What the tree has today | Class |
|---|---|---|
| An interactive wizard | `internal/initflow` + `internal/cli/init_stages.go` converge loop; `init_inputs.go`'s `gatherInputs` already prompts for project id, region and name | **Exists** |
| A separate update/refresh command | A rerun of `init` / `init --repo` already converges everything and says "No changes" | **Exists** |
| `setup` and `update` as the two names | one command, `init [--repo [PATH]]` | **Gap** |
| Don't specify `--gcp-project` *and* `--firebase` | `--firebase` is a separate id with no default; the owner's answer 2 (one id) makes it equal by default | **Gap** |
| Default `fugaro-<project>` for the id | no id default at all; the suggestion is the ADC's `quota_project_id` or `GOOGLE_CLOUD_PROJECT` | **Gap** |
| Region defaults to "the one we use" | `defaultRegion = "us-east5"` in `init_inputs.go`; **a second, independent** `us-east5` for the Firestore location in `internal/infra/firestore.go` | **Wrong** |
| List billing accounts to choose from | deliberately **refused**: `projectcreate.go` and `init_project.go` say init never lists them | **Rule** |
| `init` for creation *and* update | true today: one command, one flag apart | **Exists** |
| The wizard creates the project | creation is opt-in (`--create-project`); a bare run adopts | **Gap** (F24) |

Two findings dominate the cost of this work, and they are not the wizard:
**the billing list reverses a written rule** (§3 F5) and **the rename touches
~900 Go lines, ~110 markdown files and 36 Terraform files** (§3 F20, §4).

---

## 2. Corrections

**C1. "region to the one we use" is two constants, not one.** There is no
single "our region". `defaultRegion = "us-east5"` (`internal/cli/init_inputs.go`)
is the GCP region the jobs and the scheduler run in. `infra.FirestoreLocation`
(`internal/infra/firestore.go`) is the Firestore database's location, which
happens to be `us-east5` too and is permanent. Changing one must never change
the other: a Firestore location cannot be changed after creation, and
`internal/cli/restore.go` says so out loud. Any wizard copy must keep the two
apart and must not derive one from the other. See D4.

**C2. `--firebase` does not default to `fugaro-<project>`.** The brief's
sentence ("`--firebase` should default to `fugaro-<project>`") is superseded by
the owner's answer 2: there is one id, and it defaults to `fugaro-<name>`, so
`--firebase` is not a separate value at all unless the advanced flag says so.
The flag's usage text today (`internal/cli/init.go`, `f.StringVar(&o.firebase,
"firebase", ...)`) treats it as an independent project to adopt. See F4, D7.

**C3. The count of "places that name `fugaro init`" is the real estimate.** In
Go: 409 non-test lines and 511 test lines, across ~84 non-test files, including
19 `selfCommand() + " init"` sites. In markdown: 112 files, 158 mentions in
`docs/gcp-live-checklist.md` alone and 113 in `docs/gcp-setup.md`. Plus 36
Terraform files whose variable `description`s name the command, and
`website/content/cli-reference.md` (generated) and `website/data/install.yaml`
(pinned byte-for-byte). See F20 for where the line between "rename" and "leave
alone" has to be drawn.

---

## 3. Findings

### F1 — The converge loop already exists, and the wizard is a caller of it · **Exists**

`internal/initflow` defines `Stage` (`Name`, `Check`, `Plan`, `Apply`, `Verify`,
`Left`), the canonical `order []slot` with `needs`/`noYes`, the states
(`done|changed|blocked|needs-you|skipped|failed|will-do`), `Left`/`LeftKind`,
and the confirmation machinery (`ConfirmClass` with `YesOrTyped` and `Typed`,
`Conditions`, `Managed`, `CanConfirm`, `CanConfirmManaged`, `CanConfirmRun`,
`RunCovers`, `YesCovers`). `loop.Run` runs a read-only preview, prints the plan
view, then converges, stopping at the first stage that needs the user or fails.

`internal/cli/init_stages.go` registers the concrete stages: `preflightStage`,
`newProjectStage` (only under `--create-project`), services, installation,
Firebase, images, installation (second pass), secrets, plugin, repository, and
`newComputeStage` (only under `--provision-batch`).

**Consequence.** `fugaro setup` and `fugaro update` are two *front doors to the
same loop*, differing in which stages are registered, whether a missing input
prompts or is refused, and what the closing text says. No new engine. The
implementation plan's first task is therefore renaming and re-fronting, not
building.

### F2 — Interactive asking for missing values already exists · **Exists**

`init_inputs.go`'s `gatherInputs` asks, in order, for the GCP project id, the
region, and the project name; each answer has a suggestion and three tries
(`prompt`). `canAsk()` is `¬--non-interactive ∧ ¬--yes ∧ ¬--json ∧
¬--print-vars ∧ terminal`; when it is false, `gatherInputs` refuses with **one**
error naming **every** missing flag (`MissingInputsError`), and
`initflow.Run` repeats that check for `InputStage.Missing()`. The first-run
banner is `init_inputs.go`'s `missingInput`: "fugaro init: this is the first
run here; each answer has a suggestion, press Enter to accept it."

**Consequence.** "If missing, these should be asked interactively" is half
built. What is missing is (a) a default for the id, (b) the prompt wording and
the banner naming `setup`, and (c) an explicit rule for the no-TTY case, which
already exists and must be documented rather than invented.

### F3 — No project-id default; the suggestion comes from credentials · **Gap**

`init_inputs.go`'s `suggestGCPProject()` returns `GOOGLE_CLOUD_PROJECT` or the
ADC's `quota_project_id` — and deliberately never gcloud's configured default
project. It is a *suggestion for adoption*, not a name to create. The owner's
`fugaro-<project name>` default is new, and it changes the character of the
question: today the wizard offers to adopt the project you are already in;
after this it offers to create a project named after your Fugaro project.

**Consequence.** The wizard needs both: the default `fugaro-<name>`, and — when
`suggestGCPProject()` returns something different — that suggestion offered as
the second choice, because the overwhelmingly common case for a second
installation is "adopt the project I am already working in". See D4.

### F4 — `--gcp-project` and `--firebase` are two ids today, with a pairing rule · **Gap**

`init_project.go`'s `checkCreateProject()` refuses `--create-project` without
`--gcp-project <id>`, refuses `--link-billing` without `--create-project`, and
requires a managed project's budget flags. Separately, `--firebase <id>` is "the
Firebase project to adopt", which may be the same project or another one; the
usage text (`internal/cli/init.go`) describes adopting it and building the
budget backend in it.

**Consequence.** The owner's answer 2 collapses the common case to one id and
keeps the separate one as `--firebase-project`. The pairing rule stays (a
created project is its own Firebase project unless told otherwise), and it
becomes the *default path* rather than a special case.

### F5 — The billing-account picker contradicts a written rule · **Rule** (the big one)

`internal/infra/projectcreate.go`'s head comment says, in the code's own words,
that init does **not** list the caller's billing accounts (`billingAccounts.list`)
*even to guide*, "because that reveals every account the user can reach and the
account is theirs to name". `internal/cli/init_project.go` repeats it in its
doc comment ("list the user's billing accounts, or print the account a project
is linked to") and in `guided()`, whose whole job is to print the gcloud line
with a placeholder:

```
gcloud billing projects link <id> --billing-account=BILLING_ACCOUNT_ID
```

`billingHelp()` is the refusal text around it.

**Consequence.** The brief asks for the opposite. This is a deliberate rule, not
an accident, and it must be reversed explicitly by the owner rather than
quietly implemented — the reversal also makes two comments false, and it needs
a new privacy story: what is listed, to whom, on what surface, and what is never
listed. See D6, which recommends a narrow reversal (open accounts, ids and
display names only, in the interactive wizard at a real terminal, never under
`--json`, `--non-interactive` or an agent session) and keeping the rule for
every non-interactive path.

### F6 — The guided fallback exists and stays · **Exists**

`init_project.go`'s `guided()` and `billingHelp()` print the exact gcloud
command; `internal/preflight/google.go`'s `Billing()` reports "the Cloud Billing
API is disabled on `<quota>`, the quota project of your credentials".

**Consequence.** The picker is an addition to the guided path, not a
replacement: when the list cannot be had (no permission, API disabled, no
terminal, headless), the wizard falls back to today's text, unchanged. This is
what makes F5's reversal safe.

### F7 — Typed confirmations: three banners, a location, and a billable build · **Exists**

`init_project.go`'s `projectStage.typed(banner, want, prompt)` shows three
banners — **CREATE the project**, **ADD Firebase to an existing project**, and
**this LINKS billing account X to project Y** — each asking the user to type the
project's name or id, and each reachable only through `canType()` (`terminal ∧
¬--non-interactive ∧ ¬--json ∧ ¬agentEnv`). The Firestore location is typed in
`init_firebase.go`; a billable first image build is typed in `init_images.go`
and `init_review.go`.

`initflow.RunCovers` and `YesCovers` are the arbiters: `init_stages.go`'s
`runCovered` is installation, Firebase, images, installation-second-pass,
repository — so project creation, secrets and compute are **never** covered by
`--yes`, and `internal/initflow/loop_test.go`'s `TestYesDoesNotCoverCreateOrBilling`
pins exactly that.

**Consequence.** All of it stays, verbatim in behaviour. The wizard may
*reorder* the questions (F12) but must not soften a single one of these. The
plan's global constraints make this a hard rule with its own test.

### F8 — The refusals that stay · **Exists**

- Impersonation: `internal/preflight/preflight.go`'s `impersonateEnv`
  (`GOOGLE_IMPERSONATE_SERVICE_ACCOUNT`) → the `env-impersonation` check; and
  `internal/infra/tf/env.go`'s `Env`: "fugaro init runs Terraform as your own
  credentials only, so unset it".
- Quota project: `infra.CredentialsQuotaProject` / `NoQuotaProjectProblem`; the
  doctor's billing-API check.
- A project held by someone else, or pending deletion: `infra.ReadProject`'s
  state check (`ACTIVE` required) and the `gcloud projects undelete` fix.
- Agent sessions: `agentMarker` (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`,
  `CLAUDE_CODE_SSE_PORT`, `CLAUDE_CODE_REMOTE`, `CURSOR_AGENT`, `AI_AGENT`) →
  `agentEnv`; with one set nothing is applied, `--yes` or not.
- `refuseAppKeyReadInAgent`, `refuseHTTP2Debug`.

**Consequence.** `fugaro setup` inherits every one of these for free by calling
the same stages. `fugaro update` must keep them too — an update is not a licence
to skip the impersonation check, and the one place it may legitimately skip
something is a *creation* refusal it never reaches (F16).

### F9 — Credentials are never printed or stored · **Exists**

`initflow.Options.Redact` removes held secret values from every string the loop
records or prints; `loop.redact`/`redactAll`/`redactedCause` apply it; the
secret stages hold values in memory only (`initsecrets.go`'s hidden prompt,
`held`, `maxHeldSecrets`). `internal/cli/initsecrets_pty_test.go`'s
`TestSecretNeverInErrors` pins it.

**Consequence.** The billing picker (F5) is the one new surface that prints
account identifiers in a list. Account ids are not credentials, but the
enumeration itself is the sensitive part, and that is what D6 is about. Nothing
in this work may print a token, and `--json` output must not gain an accounts
field.

### F10 — The run shows the whole plan before it applies anything · **Exists**

`loop.preview` checks every stage read-only, prints `view()`, calls
`Options.OnPreview`, and only then converges; `init_review.go`'s `runReview` /
`reviewScreen` is "the run's one confirmation" over the stages `RunCovers`
covers. `--plan-only` stops after the preview; `planOnlyReview` is the same
screen in that mode; exit codes are in `Result.ExitCode()` (2 failed, 1
left-for-you, 0 done), so a script never reads "left for you" as done.

**Consequence.** The wizard's "show the whole plan before applying anything"
requirement is met by construction. The plan's tests should assert the wizard
*keeps* it (an ordering regression is the plausible mistake), not build it.

### F11 — A rerun already converges; `update` is a renaming · **Exists**

`init` re-run against an existing installation runs `Check` on every stage, and
each returns `Done`/"No changes" when there is nothing to do. `init_stages.go`'s
`outcome()` maps the result onto exit codes and calls `markCreated`.

**Consequence.** `fugaro update` = the same loop, registered without
`projectStage` and without `computeStage` (nothing to create), with `--yes`
allowed to be the normal mode, and with the closing text naming `update`. It is
a small task. The interesting part is *what it refuses* (F16) and how it
differs from `upgrade` (F13).

### F12 — The questions are asked in an order chosen by the loop, not the wizard · **Rule**

`initflow`'s canonical `order []slot` (with `needs` and `noYes`) decides
execution order regardless of the order stages are passed; `loop.preview` marks
a stage that waits as `will-do … (after X)`.

**Consequence.** The brief's requested question order (project → billing →
Firebase → installation → first repository) is **not** the loop's order: the
loop refuses to plan Firebase before the installation's outputs exist. The
wizard can ask *all* questions up front (which it should — that is the point of
a wizard) and then let the loop do the work in its own order. The reconciled
wizard therefore has two phases, and the plan must say so: **an input phase**
(project name, project id, region, owner email, budget mode, billing account,
first repository) and **an apply phase** (the loop, in `initflow`'s order, with
the plan view before anything is applied).

### F13 — `fugaro upgrade` is a different command, and it names `init` as the fix · **Rule**

`upgrade.go`'s `upgradeSteps = []upgradeStep{{"pin", pinStep}, {"plugin",
pluginStep}, {"cloud", cloudStep}}`, `Use: "upgrade [PATH...]"`: per checkout,
pin the plugin to this Fugaro, install or update it in Claude Code, then refresh
the repository's images and jobs. It never touches the installation, never
creates anything, and needs no owner permissions. Its cloud step *defers* to
`init --repo` in several places: `upgrade_cloud.go`'s `checkJobCopyCaveat`,
`jobConfigCopyNote`, `jobConfigCopyMountNote`, `jobConfigCopyPolicyNote` all say
"run `fugaro init --repo .` in your own terminal (never run automatically)", and
`upgradeCheckout` says "set it up with `fugaro init` and `/fugaro:setup` first".

**Consequence.** The rename has to rewrite these strings, and the merge question
(D1) is a real one: the two commands overlap on the repository's jobs and
images, and only the installation half is unique to `update`.

### F14 — The first-repository step already works from a checkout · **Exists**

`init_repo_stage.go`'s `newRepositoryStage` is stage 9; `init_repo_target.go`'s
`resolveRepoTarget`/`checkoutProject` pick the checkout's repository and its
project; `init_repo_gate.go`'s `checkoutNote`/`unknownDetail` handle the
unlisted-repository case; `authorize` asks for the GitHub App ID once;
`--onboard-repo <owner/name>` exists for a repository that is not the checkout's.

**Consequence.** "…then the first repository" is the existing last stage.
Inside a checkout the wizard asks one extra question ("onboard this
repository?") and otherwise needs no new code. See D5.

### F15 — Budget mode exists with a default · **Exists**

`--budget-mode` is `off|observe|enforce`; `internal/cli/init.go` seeds an absent
one as `observe`; `--budget-admin` goes with it. `init_project.go`'s refusal
for a managed project requires an explicit monthly budget *with no default and
no suggestion taken for you* ("`TestManagedBudgetHasNoDefault`" in
`init_budget_test.go`).

**Consequence.** The wizard asks the mode once, defaults to `observe`, and must
**not** introduce a default for the managed budget amount — that refusal is
pinned by a test and is about money.

### F16 — `fugaro update` has no refusal story yet · **Gap**

Nothing today distinguishes "a run that converges" from "a run that creates".
`--create-project` and `--link-billing` are opt-in flags, so the distinction is
implicit.

**Consequence.** `update` needs an explicit, tested refusal set: it must refuse
`--create-project`/`--link-billing` (that is `setup`'s), refuse when there is no
installation to update (`errNoState`: "there is no Terraform state bucket
gs://%s, so no installation: run fugaro init first"), refuse to enrol a
repository it was not asked to (`update` converges *enrolled* repositories), and
keep every refusal in F8. `errNoState`'s wording becomes "run `fugaro setup`
first".

### F17 — The plugin skill still runs *after* the wizard; only the CLI name it hands the user changes · **Rule**

`plugin/skills/setup/SKILL.md` is the `/fugaro:setup` skill: ten steps, a
twelve-topic question list, which the README's Getting started says to run
**after** `fugaro init`. It carries `fugaro init --repo --plan-only` as its
read-only plan and `fugaro init --repo --retire`. Four tests pin the current
order and wording: `plugin/readme_test.go`'s
`TestReadmeGettingStartedIsTwoSteps` (README's `## Getting started` is exactly
two numbered steps, step 1 containing `` `fugaro init` `` and step 2
`` `/fugaro:setup` ``), `plugin/setup_skill_test.go`'s
`TestSetupSkillMentionsDoctorFirst`, `TestSetupSkillNeverAppliesInit`,
`TestSetupSkillHandlesSeveralProjects`, `TestSetupSkillContent`, and
`plugin/skills_lint_test.go`'s `userRunsAllowed` list, which contains the
literal `"fugaro init"`.

**Consequence.** The *order stays* (CLI first, skill second) — the owner's
answer 1 makes `fugaro setup` the interactive wizard, and the skill still runs
after it to write `fugaro.yaml` and open the pull request. What changes is the
CLI name the skill hands the user: `fugaro init` → `fugaro setup` (and
`fugaro init --repo --plan-only` → `fugaro update --plan-only`). Note the near
name collision — `fugaro setup` (CLI) and `/fugaro:setup` (skill) — and see D8.

### F18 — The website prints the old sequence · **Gap**

`website/data/install.yaml`'s `plugin` path lists three commands — `fugaro
init`, `/fugaro:setup`, `fugaro upgrade --local` — and is pinned byte-for-byte
by `internal/cli/docs_website_install_test.go`. `website/src/pages/getting-
started.astro` is hand-kept, not generated: its `#setup` block is a seven-step
`<ol>`, it reads `const [initCommand, setupCommand] = pluginPath.commands`, and
its anchor fragment `the-short-path-fugaroinitthen-fugarosetup` appears twice.
`website/test/docs-hub.mjs` and `website/test/caps-and-costs.mjs` also name it.

**Consequence.** A rename here is a two-file change plus the anchor fragment
plus the pin test. The brief's "one command, the wizard asks the rest" means
the seven steps collapse to about three, and the page's prose about passing
flags has to go.

### F19 — `fugaro doctor` and ~50 other commands tell the user to run `fugaro init` · **Gap**

The naming survey lists every emitting symbol; the shape of it: `doctor.go`'s
`runDoctor` (`Fix: "run fugaro init"`), `doctorGitHubApps`, `doctorManagedDeployer`,
`doctorCosign`, `sharedConfigChecks`, `doctorPolicyChecks`, `routingUnresolvedNoRepo`,
`doctorSecretScopeChecks`, `doctorRulesSkew`; `doctor_bucket.go`'s
`tokenGrantChecks`; `config_list.go`'s `printConfigList`; `exec.go`'s
`oldJobEnvReason`; and — outside `internal/cli` — `internal/infra/discover.go`,
`discover_firebase.go`, `firebase.go`, `hardening.go`, `installation.go`,
`projectcreate.go`, `projectjobs.go`, `readiness.go`, `checkjob.go`,
`internal/budget/{rollover,registry,session}.go`,
`internal/runner/{project,projectpolicy,projectjob,halt,gateway,budget_session}.go`,
`internal/managed/budget.go`, `internal/localcfg/{localcfg,select}.go`,
`internal/config/{config,providers,routing}.go`, `internal/initflow/loop.go`
(the progress header `"fugaro init: project %s (GCP %s, %s)"` and the failure
fix text), and `internal/projectpolicy/read.go`.

**Consequence.** This is the bulk of the mechanical work, and it is not
mechanical in one place: **which** command each string should name is a
judgement (`setup`, `update`, or `fugaro update --repo .` for the
`init --repo .` strings). `internal/initflow/loop.go`'s two strings are printed
by *both* front doors, so they must become caller-supplied text rather than a
constant. `restore.go` is special: it holds `restoreInitCmd = newInitCmdWith`
and prints `"fugaro: creating the project through the same path fugaro init
--create-project --managed uses: fugaro init %s"` — it *calls* the init command
as a child, so it must be repointed at the new command's constructor.

### F20 — The docs pins are the rename's true cost, and the line to draw · **Rule**

A rename is only complete when it passes these, which is a good thing — they are
the checklist:

| Pin | What it holds |
|---|---|
| `plugin/readme_test.go` — `TestReadmeGettingStartedIsTwoSteps` | README's Getting started: exactly 2 steps, step 1 names the CLI command, step 2 `/fugaro:setup`, and the section says "merge", "`fugaro init` again", "`.claude/settings.json`", "install" |
| `plugin/setup_skill_test.go` — five tests | the skill's steps, its question list, `doctor` first, never applies init, several projects |
| `plugin/skills_lint_test.go` — nine tests | `userRunsAllowed` (contains `"fugaro init"`), `--yes` never near init, owner commands however written |
| `internal/cli/docs_project_wide_git_test.go` | the literal "Init's prompts (step 9) have no `--repo` to pass" in the skill and its `reference/decisions.md` |
| `internal/cli/docs_upgrade_test.go` | `fugaro upgrade` and its flags named in README, `docs/gcp-setup.md`, `docs/release.md`, `.claude/skills/new-release/SKILL.md`, `docs/design/v1.md`; the literal sentence "`/fugaro:upgrade` does the local half and hands you the rest"; `brew upgrade dimipaun/tap/fugaro && fugaro upgrade` in three files |
| `internal/cli/docs_website_install_test.go` | `website/data/install.yaml` byte-for-byte |
| `internal/clidoc/cliref_test.go` — `TestCliReferenceIsCurrent`, … | `website/content/cli-reference.md` is `go run ./internal/clidoc`'s output |
| `internal/cli/docs_website_cliref_test.go` — `TestCliReferenceNamesEveryCommandAndFlag`, `TestCliReferenceInventsNothing` | the reference matches the cobra tree in **both** directions |
| `internal/cli/docs_*_test.go` (≈40 files) | each holds sentences and check numbers in `docs/gcp-setup.md`, `docs/gcp-live-checklist.md`, the design docs |
| `internal/config/example.go`'s `exampleProjectLines` | `internal/config/example.yaml`, validated by `validate_test.go` and `docs_project_layer_test.go` |
| `schemas/fugaro.schema.json` | a `description` naming `fugaro init`, "never guessed from the project name" |
| 36 `deploy/terraform/**/*.tf` files | variable `description`s naming the command, shown in `terraform plan` |

**Consequence.** Two rules, and they need an owner decision because they are
judgement: (a) **live user-facing text is renamed** — everything a user or an
agent reads today, including skill files, Terraform descriptions and the
generated CLI reference; (b) **dated records are not rewritten** —
`docs/plans/**`, `docs/releases/v*`, `docs/design/**` describing a shipped
state, and `docs/credentials-service/**`, which is another plan's record. This
follows the tree's own precedent: `plugin/readme_test.go`'s
`TestReadmeNamesNoRetiredSkill` carries a `skip` regex over
`docs/plans/`, `docs/design/m11-` and the spec-source precisely because a
retired name may live in a dated record. See D9.

### F21 — `fugaro config init` is a different command with the same word · **Gap**

`internal/cli/configcmd.go` registers `newConfigCmd`, and
`config_init.go`'s `Use: "init"` is `fugaro config init` — "Write the minimal
fugaro.yaml of a repository whose project publishes a project layer".

**Consequence.** Removing `fugaro init` leaves `fugaro config init`. That is
fine and should stay (it is about a *config file*, not a project), but the plan
must not sweep it up in a find-and-replace, and the hidden-`init` tombstone (D2)
must not shadow it. `internal/cli/docs_checkout_test.go` resolves
`` `fugaro …` `` against the cobra tree, so `fugaro config init` in docs keeps
resolving.

### F22 — Check 27 is written for the path this work moves · **Gap**

`docs/gcp-live-checklist.md`'s **Check 27** is M11's simple setup on a throwaway
project, marked NOT RUN, and it covers the never-exercised `--create-project` /
`--link-billing` path. The highest written section is 70c; 110 is claimed by an
open pull request, and the register's rule is that whichever of two open claims
merges second renumbers itself. **111 is the next free number.**

**Consequence.** Check 27 must be rewritten for `fugaro setup` (its steps name
flags that are about to change under it), and the billing picker needs live
coverage of its own — the one part of this work that cannot be proven with
fakes, because its whole subject is what the Cloud Billing API answers for a
real user. See D10.

### F23 — `--json` and the docs test resolve flags by name · **Rule**

`internal/cli/docs_checkout_test.go`'s `TestDocsCheckoutCommandsExist` resolves
every `` `fugaro …` `` in the checkout docs against `NewRootCmd()` with
`cmdHasFlag`. `docs_base_test.go` builds a map keyed `"fugaro init":
newInitCmd` and defines `commandBoundaryRE`/`docFlagToken`.

**Consequence.** Renaming a command or a flag in code without renaming it in
docs is a **test failure, by design** — the pins are the specification of this
rename. The plan's tasks should each name the pin that turns red.

### F24 — Creation is opt-in today; the wizard makes it the default path · **Gap**

`init_stages.go` registers `newProjectStage(e)` **only** under `--create-project`,
and `init_project.go`'s `checkCreateProject()` only runs under it: today a bare
`fugaro init` *adopts* and never creates. The brief's example command carries
`--create-project`, and the owner's wizard is "the interactive first-time
wizard (project, billing, …)" — so in `fugaro setup` the project stage is
always registered and the first question decides what it does: create
`fugaro-<name>`, or adopt a project that already exists. The typed banner (F7)
is unchanged either way.

Two consequences the plan must carry. First `--create-project` becomes
redundant in `setup` (kept for a scripted run, which must still work: every
question has a flag). Second, `setup` run against an installation that already
exists must not create anything — `installationExists` / `loadInitConfig` and
`errNoState` already answer "is there an installation here", so the wizard's
recommendation is to say so in one line and converge as `update` would, rather
than refusing outright. See D11.

---

## 4. Every place that names `fugaro init`

The exhaustive per-symbol list is in the naming survey; this is the map to work
from, with the counts that decide the ordering of the work.

**Go — user-facing strings (the rename's core).** ~84 non-test files, 409 lines,
19 `selfCommand() + " init"` sites:

- `internal/cli` (the majority): `doctor.go`, `doctor_bucket.go`,
  `image_refresh{,_steps,_rules}.go`, `imagecheck.go`, `image.go`, `init*.go`
  (every stage), `initsecrets.go`, `init_anchor.go`, `init_adopt.go`,
  `init_firebase.go`, `init_fugaroyaml.go`, `init_images.go`, `init_inputs.go`,
  `init_plugin_stage.go`, `init_project.go`, `init_repo_{gate,stage,target}.go`,
  `init_retire.go` (`const retireNextCommand = "fugaro init --repo . --retire"`),
  `init_review.go`, `init_stages.go`, `launch_budget.go`, `operator_write.go`,
  `project.go`, `project_cmd.go`, `recipes_skew.go`, `report.go`, `restore.go`,
  `sharedcfg.go`, `upgrade{,_cloud}.go`, `validate.go`, `budget.go`, `cloud.go`,
  `config_list.go`, `exec.go`
- `internal/infra`: `projectcreate.go` (the F5 comment), `firebase.go`,
  `firebasedb.go`, `discover.go`, `discover_firebase.go`, `hardening.go`,
  `installation.go`, `projectjobs.go`, `readiness.go`, `checkjob.go`, `tf/env.go`,
  `tf/managed_cover.go`
- `internal/initflow/loop.go` (the progress header and the Fix text)
- `internal/{budget,runner,managed,config,localcfg,projectpolicy,preflight,backend/gcp}`
- plus a long tail of comment-only mentions (no user string), listed in the
  survey — these are free to rename but do not have to be part of the same
  commit.

**Go — tests.** 511 lines: the docs pins of F20, plus
`internal/e2e/init_test.go` (14 `TestInitRepo*` tests, which rename to
`TestUpdate*`/`TestSetup*` and move), `internal/cli/init*_test.go`,
`internal/initflow/loop_test.go`, and assertion helpers in ~40 other packages.

**Markdown — 112 files.** Live (renamed): `README.md` (16),
`docs/gcp-setup.md` (113 — its title is `# GCP setup with \`fugaro init\``),
`docs/gcp-live-checklist.md` (158), `docs/project-layer.md`,
`docs/project-claude-accounts.md`, `docs/multi-model.md`, `docs/git-providers.md`,
`docs/dogfooding.md`, `docs/release.md`, `SECURITY.md`, `docs/coordinator/`,
`docs/mobile/`, `docs/in-job-review/`, `plugin/skills/setup/SKILL.md` and
`plugin/skills/setup/reference/*`, `.claude/skills/new-release/SKILL.md`.
Dated (left alone): `docs/plans/**`, `docs/releases/v*`,
`docs/design/**`, `docs/credentials-service/**`.

**Terraform — 36 files.** Variable and output `description`s saying what `fugaro
init` writes, e.g. `deploy/terraform/gcp/modules/repo/variables.tf` ("Every name
arrives from `fugaro init --repo`'s terraform.tfvars.json"),
`roots/repo/tests/testdata/README.md`. Rename, but in its own commit: these are
`terraform plan` output, not fugaro's own text, and a description edit is
independently reviewable.

**Website.** `website/data/install.yaml` (pinned), `website/src/pages/getting-
started.astro` (hand-kept, 3 mentions, two anchor fragments),
`website/content/cli-reference.md` (generated — regenerate, never hand-edit),
`website/content/security.md` (generated from `SECURITY.md`),
`website/src/components/Changelog.astro`, `website/test/caps-and-costs.mjs`,
`website/test/docs-hub.mjs`.

**Other.** `internal/config/example.yaml` + `example.go`, `schemas/fugaro.schema.json`,
`images/verify-public.sh`, `docs/website/redesign/` (a dated plan record).

---

## 5. Decisions

Each is the owner's to make. Recommended option first, with the reason; the
alternative and what it costs. **Nothing here is decided.**

### D1 — Does `fugaro update` merge into `fugaro upgrade`?

**Recommend: no. Keep two commands, and say the boundary in one line each.**
`fugaro upgrade` is per checkout and needs nothing from the cloud (`pin`,
`plugin`, `cloud` = `image refresh`): any teammate can run it. `fugaro update`
converges the installation and the enrolled repositories, so on the installation
half it needs owner permissions. Merging forces every teammate's upgrade to
carry the installation's requirements — or forces the merged command to silently
do less depending on who runs it, which is worse.

The line to document: **`fugaro upgrade` brings this machine's checkouts to this
Fugaro; `fugaro update` brings the cloud to it.** After `brew upgrade
dimipaun/tap/fugaro`: `fugaro upgrade` in each checkout, then `fugaro update`.
`upgrade`'s cloud step keeps deferring the repository's jobs to `update`
(today's `init --repo .` notes), which is the one overlap and the only place the
two commands touch.

*Alternative:* one command, `fugaro upgrade`, doing everything. Cost: the
checkout-local command becomes permission-dependent, its `PATH...` shape stops
fitting (an installation is not a path), and `--local` loses its meaning.

### D2 — Is there a deprecated `init` alias for one release?

**Recommend: no alias, but a tombstone.** Replace `init` with a hidden command
that applies nothing and exits non-zero with one message naming both
successors: "`fugaro init` is now `fugaro setup` (first time) and `fugaro
update` (after a CLI upgrade)". Reason: an alias has to pick one meaning, and
picking wrong is worse than refusing; a tombstone makes the removed command
self-explanatory at exactly the moment confusion is possible, costs one small
function and one test, and appears in no document.

*Alternative:* a real alias, mapping bare `init` to `update` (the common case)
and `init --create-project` to `setup`. Cost: keeps ~900 naming sites
plausible-looking for a release, and the mapping is wrong for every user whose
first run was `init` — who is exactly the user who will type it.

### D3 — May the wizard enable the Cloud Billing API itself?

**Recommend: only with a typed confirmation, only when the API is disabled, and
never when the quota project is not the project being set up.** The list needs
`cloudbilling.googleapis.com` enabled **on the quota project of the user's
credentials** (`internal/preflight/google.go`'s `Billing()`), which in the
general case is *not* the project the wizard is creating or adopting. Enabling
an API on someone else's project as a side effect of a setup wizard is exactly
the kind of surprise the refusals in F8 exist to prevent. So:

- quota project **is** the project being set up → offer to enable it, typed
  (`--yes` never covers it), naming the project and the API;
- quota project is **another** project → refuse, and print the exact
  `gcloud services enable cloudbilling.googleapis.com --project <quota>` line
  plus who to ask;
- always fall back to F6's guided `gcloud billing projects link` text, which
  needs no API.

*Alternative:* always enable behind a typed confirmation. Cost: writes to a
project the user did not choose in the wizard, and on a project that may be
shared with other work.

### D4 — The default project id, and the region

**Recommend: `fugaro-<project name>` exactly as the owner said, with today's
credential suggestion offered as the second choice when it differs.**
`sanitiseName`/`suggestName` already produce a safe `<project name>`, so the
default is `"fugaro-" + suggestName(...)`; the prompt shows it, Enter accepts
it, and typing another id is the escape hatch. When `suggestGCPProject()`
returns something different, the prompt shows both and says why they differ
("you are already working in `<id>`"). The default is **checked before any
create** — availability and ownership — exactly as the typed banner requires.

Region: **keep `defaultRegion = "us-east5"`**, shown as the default with Enter
to accept, and never derived from the Firestore location (C1). One decision
hides here and should be made consciously: the bucket names derive from the id,
so this default turns `fugaro-runs-<id>` into `fugaro-runs-fugaro-<name>`
(`init.go`'s `"fugaro-runs-" + r.res.Created` check, `tfvars.go`'s prefix check,
and the managed fact at `gs://fugaro-runs-<gcp-project>/fugaro/managed.json`).
**Recommend: accept the double prefix and change nothing about bucket naming in
this work.** It is a name, it is already derived, and restructuring bucket
naming would be a migration for no gain.

*Alternative:* default the id to `<project name>` (no prefix) — shorter, but
collides with the user's other projects and with names they already own.

### D5 — Does the wizard enrol a repository, or does `update`?

**Recommend: `setup` owns enrolment; `update` converges what is enrolled and
names the command for the rest.** `setup` run inside a checkout asks one extra
question ("onboard `<owner/name>`?") and uses the existing repository stage; run
outside a checkout it skips the question and says how to enrol one later
(today's `init_repo_gate.go`'s `checkoutNote`, reworded). `update` never enrols
silently: inside a checkout whose repository is not enrolled it prints one line
naming `fugaro setup`, and exits with `left-for-you` (exit 1), which is already
what `Result.ExitCode()` means.

*Alternative:* `update --repo` enrols on request. Cost: a second interactive-ish
path for a one-time action, and `update`'s "non-interactive friendly" promise
gets a hole in it.

### D6 — Does the wizard list billing accounts at all (F5)?

**Recommend: yes, narrowly, and keep the rule everywhere else.** List **open
accounts only** (a closed account cannot be linked), show **display name and
id**, pick by number, one account is the default, none is one message naming the
console and the guided gcloud line. Show it **only** in the interactive wizard
at a real terminal — never under `--json`, `--non-interactive`, or an agent
session (the same four conditions as `canType()`), and never store the list.
`internal/infra/projectcreate.go`'s comment and `init_project.go`'s doc comment
become false and must be rewritten in the same commit, and
`internal/gcpfake/billing.go` (which already has `accounts`, `Link`,
`SetSuspended`, `UseCRM`) gets the fakes.

Two sub-questions the owner should answer with it: **(i)** does the picker use
the Cloud Billing API with the user's ADC, `gcloud billing accounts list` as a
fallback, or both? — recommend **both**: the API first (it is what Firebase
does), gcloud only when the API is unavailable and gcloud is on PATH, and say
which one answered, because the two can disagree. **(ii)** Is the account the
project is *already linked to* printed when adopting an existing project? —
recommend **no**: that is a different permission from listing, and today's rule
covers it.

*Alternative:* keep the rule as written, and have the wizard print the gcloud
line with the placeholder, changing nothing. Cost: the brief's explicit request
goes unmet.

### D7 — `--firebase` becomes `--firebase-project`, and what happens to the rest

**Recommend: rename `--firebase` → `--firebase-project` (advanced, defaults to
the GCP project id) and retire the `fugaro init --firebase <id>` *phrase* from
every message that uses it as "re-run this".** `--firebase` is a false friend:
it reads like a toggle and is a project id. But ~50 strings say "re-run `fugaro
init --firebase`" — most of them (`doctorRulesSkew`, `doctorPolicyChecks`,
`project_cmd.go`'s `rulesMarker*Text`, `launch_budget.go`) mean *converge the
Firebase stage*, which after this work is plain `fugaro update`. Rewriting them
to `fugaro update` is both the rename and a simplification, and it must be done
in one pass or the docs pins will catch the mix.

*Alternative:* keep `--firebase` and add `--firebase-project` beside it. Cost:
two names for one thing, which is the confusion the brief opened with.

### D8 — `fugaro setup` (CLI) beside `/fugaro:setup` (skill)

**Recommend: keep both names.** The skill runs *after* the wizard (F17) and its
own steps are unchanged apart from the CLI name it hands the user. One story —
"setup" — told in two places, CLI first, is better than inventing a second word;
and the retired-skill test (`plugin/readme_test.go`'s
`TestReadmeNamesNoRetiredSkill`) already forbids the obvious alternative
(`fugaro:onboard`, `fugaro:launch`, …). The skill's opening line must say which
one it is: "`fugaro setup` (the CLI) did the cloud; this skill writes
`fugaro.yaml` and opens the pull request."

*Alternative:* rename the skill (`/fugaro:configure`, `/fugaro:repo`). Cost: a
new skill name, four pinned tests, and a user who now has to remember two
unrelated words for the same moment.

### D9 — Where is the line between "rename" and "leave the record alone" (F20)?

**Recommend: rename live text, never rewrite dated records.** Renamed:
user-facing strings, skill files, README, the live `docs/` guides, generated
website content, Terraform descriptions. Left alone: `docs/plans/**`,
`docs/releases/v*`, `docs/design/**` describing shipped state,
`docs/credentials-service/**`, and this folder's own spec.md (which is the
owner's words). This is the tree's own precedent, in
`TestReadmeNamesNoRetiredSkill`'s `skip` regex, and it is the only way a
900-line rename stays reviewable. The plan adds one **new** guard test that
asserts the split explicitly, so the line is enforced and not merely intended.

*Alternative:* rename everything including history. Cost: ~30 dated design and
plan documents rewritten so they no longer describe what actually happened, and
a diff nobody can review.

### D10 — Check 27, and the number for the picker

**Recommend: rewrite Check 27 in place for `fugaro setup`, and take 111 for the
billing picker.** Check 27 is NOT RUN and was written for a path (`fugaro init
--create-project --link-billing`) that this work renames and re-fronts, so its
steps are stale text rather than a test anyone has performed; keeping the number
avoids churning the register. Its rewritten form must cover: the wizard from an
empty shell, the default id `fugaro-<name>` accepted by Enter, the typed project
creation, the **billing picker against a real account** (does the Cloud Billing
API answer, does gcloud fall back correctly, does an account with no permission
produce the guided line rather than a crash), the typed billing link, the typed
Firestore location, the first image build, installation, Firebase, and the first
repository — then `fugaro update` on the same installation **twice**, asserting
"No changes" the second time.

**Check 111** covers the picker's failure modes only: API disabled on the quota
project, a user with no `billing.accounts.list`, no TTY, `--json`, and an agent
session — each asserting the guided fallback and that nothing was enabled.
Both are owner-run, sandbox only, and stay NOT RUN until the owner runs them.

*Alternative:* one new check and retire 27. Cost: a register with a hole in it.

### D11 — Does `fugaro setup` create by default, or adopt (F24)?

**Recommend: create by default, and degrade to `update` on an installation that
already exists.** `setup` is the first-time wizard; its first question is "the
project id — `fugaro-<name>` (new), or an existing project to adopt?", with
creation as the default answer and adoption as the second. The typed banner for
creation is unchanged, and a scripted run passes `--create-project` explicitly
because `setup` without it is ambiguous. When the installation already exists
(`installationExists`, `loadInitConfig`), `setup` prints one line — "this
installation is already set up" — keeps the existing project, and converges as
`update` does rather than offering to create a second one; creating a *second*
installation on one machine is `fugaro init`'s old ambiguity and is out of
scope here.

*Alternative:* keep creation strictly opt-in (`--create-project` required, as
today) and make the wizard adopt-only unless told otherwise. Cost: the brief's
own example command has to keep its flag, and the first-time user — who has no
project to adopt — is refused by the wizard that exists to set them up.

---

## 6. Open items

**O1.** The final command list is a decision, not a finding: `setup`, `update`,
`upgrade`, the hidden `init` tombstone, and `fugaro config init` untouched.
Whoever implements must not add a fourth verb.

**O2.** `internal/initflow/loop.go`'s `"fugaro init: project %s (GCP %s, %s)"`
header, `"fugaro init: result"` and the two Fix texts are printed by both front
doors. They must become caller-supplied (`Options.Program`, or `Options.Out`
plus a name field) — a small API change to a tested package, and the one place
where a naive replace would produce a package that names a command it cannot
know.

**O3.** `restore.go`'s `restoreInitCmd = newInitCmdWith` and its printed
`"fugaro: creating the project through the same path fugaro init --create-project
--managed uses: fugaro init %s"` mean `restore` invokes the init command as a
child. The rename must repoint it at the new construction, and `fugaro restore`
is not otherwise in scope.

**O4.** `--budget-mode`'s absent-seeds-`observe` default and the managed
project's no-default budget amount are money behaviour; the wizard asks the mode
once and adds no default for the amount (F15).

**O5.** The wizard's question list and the loop's execution order are different
sequences (F12). The implementation plan's input stage must collect every answer
before the loop starts, and must not be implemented as prompts interleaved with
applies.

---

## 7. UNVERIFIED

| Claim | Why it is unverified |
|---|---|
| That `billingAccounts.list` with the user's ADC returns the same accounts `gcloud billing accounts list` does | Not called. The two can disagree (different credentials, different quota projects); the design assumes they can and says which answered. |
| That the Cloud Billing API's `billing.accounts.list` permission is granted to a typical owner in the owner's two installations | Not called. `internal/infra/projectcreate.go` already carries an UNVERIFIED list against the real services; the picker adds one more row to it. |
| That every one of the ~50 `re-run fugaro init --firebase` strings means "converge the Firebase stage" | Read, not executed; each was judged by its surrounding symbol. Any exception should be found by the task that rewrites them, and its test should name the string. |
| That no Terraform `description` is byte-compared by a test | Not exhaustively grepped. If one is, the Terraform rename task's first red step will say so. |
| The exact count of places naming `fugaro init` | Counted by grep on `main` at `82d366af`: 409 non-test Go lines, 511 test lines, 112 markdown files, 36 Terraform files. A different grep would give a slightly different number; the plan does not depend on the count, only on the categories. |
| That the wizard does not break `internal/cli/docs_checkout_test.go`'s command resolution | Not run. It resolves `` `fugaro …` `` against `NewRootCmd()`; it is expected to **fail** until the docs are renamed in the same commit, which is the point. |
