# Reconciliation — `fugaro setup` and `fugaro update`

**Status: approved to build; implementation in progress.**

Oct 20, 2026 · @Dimi

What the brief asks for, set against what the tree does today. Every finding has
a path and a symbol (never a line number, which moves). Where this document
disagrees with [spec.md](spec.md), **this document wins**: spec.md is the owner's
words, this is what the code says about them.

**D1–D12 in §5 were answered by the owner on 2026-10-20** — every one of them as
recommended except **D12**, which is answered the other way. Each decision carries
its answer in its own entry, and the answers are what the
[implementation plan](implementation-plan.md) is now built on.

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
~790 Go lines, 26 live markdown files and 29 Terraform files** (§3 F20, §4 — the
111 markdown files that name the command include the 85 that sit, at that commit,
in eight of the ten dated roots the rename leaves alone).

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

**C3. The count of "places that name `fugaro init`" is the real estimate.**
Every figure below is stated next to the command that produces it, and each is
read at the commit this document is checked against, `82d366af` — the counts
drift as the tree moves, so **the command is the fact and the number is its
value at that commit**. In Go: 410 non-test lines across 107 files
(`git grep -c 'fugaro init' 82d366af -- '*.go' | grep -v _test.go | awk -F:
'{s+=$NF} END {print s}'` for the lines, and the same pipeline with `-l` in
place of `-c` **piped to `wc -l`** for the files — `-l` prints paths, so the
`awk` that sums the third field prints 0 there), including 7
`selfCommand() + " init"` sites
(`git grep -n 'selfCommand() + " init"' 82d366af -- '*.go' | wc -l`); 378 test
lines across 106 files (the same two commands against `'*_test.go'`). In
markdown: 111 files outside this folder
(`git grep -l 'fugaro init' 82d366af -- '*.md'
':(exclude)docs/setup-wizard/**' | wc -l` — the exclusion is a pathspec rather
than a `grep -v '^docs/setup-wizard/'`, because a revision makes `git grep`
print `82d366af:` in front of every path, so the anchored pattern would match
nothing and the command would quietly count this folder too), of which **26 are
live** (the rename's) and **85 sit in the dated roots** (left alone) — §4 names
the 26 live files one by one and gives
the dated ones **by root, with each root's count**, because a dated root is
exempt as a whole rather than file by file. Single files:
158 mentions in `docs/gcp-live-checklist.md` (`git grep -c 'fugaro init'
82d366af -- docs/gcp-live-checklist.md`) and 113 in `docs/gcp-setup.md` (the
same command for that path). Plus 29 of the tree's 58 `.tf` files
(`git grep -l 'fugaro init' 82d366af -- '*.tf' | wc -l`;
`git ls-tree -r --name-only 82d366af | grep -c '\.tf$'`), and
`website/content/cli-reference.md` (generated) and `website/data/install.yaml`
(held to its `source:` files by a docs pin).

`docs/setup-wizard/` itself names the command throughout its four documents and
is exempt (F20, D9). Its own figure is volatile, because these documents are
edited by the work they describe, so it gets the command rather than a number:
`git grep -o -c 'fugaro init' -- docs/setup-wizard`. See F20 for where the line
between "rename" and "leave alone" has to be drawn, and §4 for the file lists
themselves.

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

`internal/cli/init_stages.go`'s `converge()` registers the concrete stages:
`preflightStage`, `newInstallationStage`, `newFirebaseStage`, `e.images` (the
images stage), `newInstallation2Stage` (installation, second pass),
`newSecretsStage`, `newPluginStage`, `e.repo` (the repository stage), plus
`newProjectStage` and `newComputeStage` only under `--create-project` and
`--provision-batch`. **`initflow.Services` is not among them**: it is a slot in
`internal/initflow`'s canonical `order` (`stage.go`: `Services` *needs* `Project`
and `Installation` *needs* `Services`), and `internal/cli` contains no reference
to it at all — so a stage list built from `Names()` would name a stage no front
door registers.

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

`internal/initflow/stage.go` holds the arbiters: `runCovered` — installation,
Firebase, images, installation-second-pass, repository — and `RunCovers`, which
is `runCovered[name] ∧ YesCovers(name)`; `noYes` (seven occurrences, same file)
is what a stage declares itself as never covered. `internal/cli/init_stages.go`
is what *registers* the stages and hands them to `initflow.Run`. So project
creation, secrets and compute are **never** covered by `--yes`, and
`internal/initflow/loop_test.go`'s `TestYesDoesNotCoverCreateOrBilling` pins
exactly that.

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
- Agent sessions: `internal/cli/initsecrets.go`'s `agentMarkers` (the list:
  `CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SSE_PORT`,
  `CLAUDE_CODE_REMOTE`, `CURSOR_AGENT`, `AI_AGENT`) and `agentMarker(os.Getenv)`,
  which returns the first one set → `agentEnv`; with one set nothing is applied,
  `--yes` or not.
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
`internal/cli/upgrade.go`'s `upgradeCheckout` says
"set it up with `fugaro init` and `/fugaro:setup` first".

**Consequence.** The rename has to rewrite these strings, and the merge question
(D1) is a real one: the two commands overlap on the repository's jobs and
images, and only the installation half is unique to `update`.

### F14 — The first-repository step already works from a checkout · **Exists**

`init_repo_stage.go`'s `newRepositoryStage` is stage 9; `init_repo_target.go`'s
`resolveRepoTarget` and `internal/cli/project.go`'s `checkoutProject` pick the
checkout's repository and its
project; `init_repo_gate.go`'s `checkoutNote`/`unknownDetail` handle the
unlisted-repository case; `authorize` asks for the GitHub App ID once;
`--onboard-repo <owner/name>` exists for a repository that is not the checkout's.

**Consequence.** "…then the first repository" is the existing last stage.
Inside a checkout the wizard asks one extra question ("onboard this
repository?") and otherwise needs no new code. See D5.

### F15 — Budget mode exists with a default · **Exists**

`--budget-mode` is `off|observe|enforce`; `internal/cli/init.go` registers the
flag and documents the seed, and the seed itself is
`internal/infra/firebasedb.go`'s `DB.Plan` (an absent mode becomes
`budget.ModeObserve`); `--budget-admin` goes with it. `init_project.go`'s refusal
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
read-only plan and `fugaro init --repo --retire`. Six pins hold the current
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
init`, `/fugaro:setup`, `fugaro upgrade --local` — and
`internal/cli/docs_website_install_test.go` holds it to the repository: the test
reads the file **as bytes** but asserts **containment**, not a byte comparison —
every command in it must appear verbatim in the file its `source:` names (plus
`build`/archive/Homebrew/platform checks). `website/src/pages/getting-
started.astro` is hand-kept, not generated: its `#setup` block is a seven-step
`<ol>`, it reads `const [initCommand, setupCommand] = pluginPath.commands`, and
its anchor fragment `the-short-path-fugaro-init-then-fugarosetup` appears twice
(the slug is the heading's words joined with hyphens, so it keeps a hyphen the
name `fugaro-init` does not have).
`website/test/caps-and-costs.mjs` names the command too (once, resolving those
anchors). `website/test/docs-hub.mjs` does **not**: its 450 lines contain no
occurrence of `init` at all, so no task touches it.

**Consequence.** A rename here is a two-file change plus the anchor fragment
plus the pin test. The brief's "one command, the wizard asks the rest" means
the seven steps collapse to about three, and the page's prose about passing
flags has to go.

### F19 — `fugaro doctor` and ~50 other commands tell the user to run `fugaro init` · **Gap**

§4 below lists every file that names the command, by package and count, and
these four documents are the whole of it — there is no separate survey
document. The shape of it: `doctor.go`'s
`runDoctor` (`Fix: "run fugaro init"`), `doctorGitHubApps`,
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
| `plugin/setup_skill_test.go` — `TestSetupSkillMentionsDoctorFirst`, `TestSetupSkillNeverAppliesInit`, `TestSetupSkillContent`, `TestSetupSkillHandlesSeveralProjects` (the file's own count is not a fact worth quoting: `grep -c '^func Test' plugin/setup_skill_test.go`) | the skill's steps, its question list, `doctor` first, never applies init, several projects |
| `plugin/skills_lint_test.go` — the file has 41 `func Test` functions (`grep -c '^func Test' plugin/skills_lint_test.go`), of which **eight** name the command: the six that read a skill's commands and its rules — `TestLintRejectsYesNearInit`, `TestLintInitAppliesOnlyInUserRuns`, `TestLintUserRunsBlocksAreNotExempt`, `TestLintOwnerCommandsHoweverTheyAreWritten`, `TestLintForbidsOnboardingAndMoneyFlags`, `TestLintUserRunsYesOnlyOnUpgrade` — plus `TestLintJoinsContinuedLines` and `TestLintBangShell`, whose fixtures name it. Both figures are here so the row can be checked; the file, not the count, is the authority | `userRunsAllowed` (contains `"fugaro init"`), `--yes` never near init, owner commands however written |
| `internal/cli/docs_project_wide_git_test.go` | the literal "Init's prompts (step 9) have no `--repo` to pass" in the skill and its `reference/decisions.md` |
| `internal/cli/docs_upgrade_test.go` | `fugaro upgrade` and its flags named in README, `docs/gcp-setup.md`, `docs/release.md`, `.claude/skills/new-release/SKILL.md`, `docs/design/v1.md`; the literal sentence "`/fugaro:upgrade` does the local half and hands you the rest"; `brew upgrade dimipaun/tap/fugaro && fugaro upgrade` in three files |
| `internal/cli/docs_website_install_test.go` | `website/data/install.yaml` read as bytes and held to the file each entry's `source:` names: every command appears verbatim in it — **containment, not a byte comparison** |
| `internal/clidoc/cliref_test.go` — `TestCliReferenceIsCurrent`, … | `website/content/cli-reference.md` is `go run ./internal/clidoc`'s output |
| `internal/cli/docs_website_cliref_test.go` — `TestCliReferenceNamesEveryCommandAndFlag`, `TestCliReferenceInventsNothing` | the reference matches the cobra tree in **both** directions |
| `internal/cli/docs_*_test.go` (**44** files at `82d366af` — `git ls-tree --name-only 82d366af internal/cli/ \| grep -cE 'docs_.*_test\.go$'` — of which **12** name the command, `git grep -l -E 'fugaro init\|init --repo' 82d366af -- 'internal/cli/docs_*_test.go'`) | each holds sentences and check numbers in `docs/gcp-setup.md`, `docs/gcp-live-checklist.md`, the design docs; the glob also covers the six `docs_website_*_test.go` files, and **none** of those names the command, so the 12 — not the 44 — are the rename's to move |
| `internal/config/example.go`'s `exampleProjectLines` | `internal/config/example.yaml`, validated by `validate_test.go` and `docs_project_layer_test.go` |
| `schemas/fugaro.schema.json` | a `description` naming `fugaro init`, "never guessed from the project name" |
| 29 `deploy/terraform/**/*.tf` files (of 58 in the tree) | 15 `description` lines naming the command, shown in `terraform plan`; 8 `error_message`s; the rest comments |

**Consequence.** Two rules, and the owner answered them in **D9** because they
are judgement: (a) **live user-facing text is renamed** — everything a user or
an agent reads today, including skill files, Terraform descriptions and the
generated CLI reference; (b) **dated records are not rewritten**. The dated
roots are **ten**, and nine of them are one of two kinds: a **dated record of
this tree's own past** — `docs/plans/**`, `docs/releases/v*`, `docs/design/**`
describing a shipped state — or **another artifact's documents**, which are a
record of a different work and not this rename's to rewrite:
`docs/credentials-service/**`, `docs/website/**`, `docs/coordinator/**`,
`docs/in-job-review/**`, `docs/mobile/**`, `docs/windows-cli/**`. The tenth is
**`docs/setup-wizard/**` itself**: its spec.md is the owner's words, and its
reconciliation and plan
cannot describe a rename without naming what is renamed.

The artifact folders added here — `docs/coordinator/`,
`docs/in-job-review/`, `docs/mobile/` and, on the review's finding,
`docs/windows-cli/` — are the corrections this section needed:
they hold another artifact's `reconciliation.md` (and, for the mobile and the
Windows work, an `implementation-plan.md` too), exactly like
`docs/credentials-service/` and
`docs/website/`, which were already exempt; treating them as live text was
inconsistent with the rule that exempted their twins. The rule is D9's, and the
list is its application: a root earns its place by
naming the command (`docs/mobile/` 4 files and 11 mentions, `docs/coordinator/`
2 and 8, `docs/in-job-review/` 1 and 4, `docs/windows-cli/` 1 and 1), so
`docs/voice-module/**`, which is also an artifact folder but names it **zero**
times, is **not** on the list — an exemption for a root with no occurrences is
the "wrong in one direction" case W0.2's guard forbids.
**`docs/windows-cli/**` is the tenth root, and it was read wrong first**, which
is why the counts above sit with it. It does not exist at `82d366af` — the
commit every figure in this section is read at — so an earlier revision of this
paragraph called it an artifact folder that names the command **zero** times,
which was true of an absent folder and false of the tree: it exists on `main`
today — **five** files there
(`git ls-tree -r --name-only origin/main docs/windows-cli`): `spec.md`,
`reconciliation.md`, `implementation-plan.md`, `README.md` and `wsl.md`, with its
own header saying "plan only; nothing is implemented; decisions open" — and its
`reconciliation.md` names
`fugaro init` **once**, in F37's cross-reference to this document's own count.
The likeness to the four folders beside it is "another artifact's plan folder of
its own", not a file-for-file match: `docs/mobile/` is **thirteen** files at the
same ref, a `voice-session/` subtree among them.
By the rule above it is another artifact's dated plan record and belongs on the
list — the same correction the three folders before it needed, and the same
"wrong in one direction" that a folder absent at the base commit invites. Its
figure is read with the command rather than at `82d366af`:
`git grep -c 'fugaro init' -- docs/windows-cli`.

This follows the tree's own precedent: `plugin/readme_test.go`'s
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
project, and it covers the `--create-project` / `--link-billing` path. **The
register states its status twice and the two statements disagree.** Its row, its
heading and its prose say **NOT RUN** ("No step below has been run; nothing here
is a claim that it works"), while the same section carries "### Results of the
seventh live run (M11, check 27)" — run 2026-10-04 and 2026-10-05 by the
maintainer with the `0.2.0` release binary and Claude Code 2.1.289, on the
throwaway project `fugaro-live-20261005` — which records PASS or VERIFIED for
the typed `--create-project` / `--link-billing` confirmations, the clean converge
and its `No changes` rerun, the mirror from ghcr.io to Artifact Registry, the
hidden secret prompts, the hostile-clone gate, `doctor`, the teammate's adopt
run, `/fugaro:setup` and the prompt-injection probe. The row itself calls the
four Claude Code spikes "all verified live 2026-10-04". This work does not try to
settle which statement the owner means; it records both, and it stops asserting
the stronger one. The register's written sections run to **89** (81, 82 and
89 are written; 80–89 are the mobile block), and 110 is claimed by an open pull
request — the register's rule is that whichever of two open claims merges second
renumbers itself. **111 is therefore the next free number.**

**Consequence.** Check 27 must be rewritten for `fugaro setup` (its steps name
flags that are about to change under it), and the rewrite must carry forward what
the recorded run proved rather than promising coverage afresh: the steps that
name `fugaro setup` and `fugaro update` are the ones **no one has run**, and the
run under the section was against the commands this work renames. The billing
picker also needs live coverage of its own — the one part of this work that
cannot be proven with fakes, because its whole subject is what the Cloud Billing
API answers for a real user. See D10.

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

**This section is the map** — there is no separate naming-survey document, and
this folder's four files are the whole deliverable. Every file that names the
command is accounted for below by category, in **both** of the forms it can take
— the `fugaro init` pattern the counts are all built on, and the bare
`init --repo` form the second pattern paragraph below names file by file — with
the number the category's own command prints at `82d366af`; the per-symbol
breakdown inside each file is the rename task's own red step. The commands are
the facts and the numbers are their values at that commit (C3), so a reader
checks a line by running it rather than by trusting it.

**Go — user-facing strings (the rename's core).** 107 non-test files, 410 lines,
7 `selfCommand() + " init"` sites — `git grep -l 'fugaro init' 82d366af --
'*.go' | grep -v _test.go | wc -l`, `… | grep -v _test.go | awk -F: '{s+=$NF}
END {print s}'`, and `git grep -n 'selfCommand() + " init"' 82d366af -- '*.go' |
wc -l`. 44 of the 107 are in `internal/cli`, and they are named one by one
because this bullet is the one W2.1 sweeps:

- `internal/cli` — **44 files**: `allow_delete.go`, `budget.go`, `cloud.go`,
  `config_list.go`, `doctor.go`, `doctor_bucket.go`, `exec.go`,
  `history_rollover.go`, `image.go`, `image_layer.go`, `image_refresh{,_steps,_rules}.go`,
  `imagecheck.go`, `init*.go` (15 files: the command itself and every stage —
  `init.go`, `init_adopt.go`, `init_anchor.go`, `init_firebase.go`,
  `init_fugaroyaml.go`, `init_images.go`, `init_inputs.go`,
  `init_plugin_stage.go`, `init_project.go`, `init_repo_{gate,stage,target}.go`,
  `init_retire.go` (`const retireNextCommand = "fugaro init --repo . --retire"`),
  `init_stages.go`, `initsecrets.go`), `launch_budget.go`,
  `lswarn.go`, `managed_flag.go`, `operator_write.go`, `project.go`,
  `project_cmd.go`, `recipes_skew.go`, `report.go`, `restore.go`, `roles.go`,
  `secrets.go`, `sharedcfg.go`, `upgrade{,_cloud}.go`, `validate.go`.
  One more `internal/cli` file belongs to W2.1 without being on this list, and
  the difference is the census's own pattern: `init_review.go` builds its retry
  as `args := []string{"init"}` — the bare subcommand, with no `fugaro` in
  front of it — so a `fugaro init` grep does not match it, while the rename
  still has to move it. A rename task's file list is therefore not the census
  and the census is not the task list; both are correct, and it is the census
  that W0.2 pins.
- `internal/infra` (23 files): `apis.go`, `checkjob.go`, `discover.go`,
  `discover_firebase.go`, `firebase.go`, `firebasedb.go`, `hardening.go`,
  `installation.go`, `managed/{deletelist,marker}.go`, `project.go`,
  `projectcreate.go` (the F5 comment), `projectjobs.go`, `readiness.go`,
  `repo.go`, `retire.go`, `spec.go`, `statebucket.go`, `tf/{env,managed_cover,tf}.go`,
  `tfvars.go`, `workdir.go`
- `internal/initflow`: `loop.go` (the progress header and the Fix text) and
  `stage.go` — both, which is why W1.1 owns the whole package
- `internal/{basepin,budget,config,gateway,gcpfake,imagecheck,localcfg,managed,mirror,preflight,projectpolicy,runner}` and
  `internal/backend/{backendtest,gcp}`, plus `deploy/terraform/terraform.go`
- plus a long tail of comment-only mentions (no user string), counted in the
  file totals above and named by the package bullets — these are free to rename
  but do not have to be part of the same commit.

**Go — tests.** 378 lines across 106 files
(`git grep -c 'fugaro init' 82d366af -- '*_test.go' | awk -F: '{s+=$NF} END
{print s}'` for the lines, `git grep -l 'fugaro init' 82d366af -- '*_test.go' |
wc -l` for the files — not the same pipeline with `-l`, which prints paths and
so sums 0): the docs pins of F20, plus
`internal/e2e/init_test.go` (26 `TestInitRepo*` tests of 27 `TestInit*`, which rename to
`TestUpdate*`/`TestSetup*` and move), `internal/cli/init*_test.go`,
`internal/initflow/loop_test.go`, and assertion helpers in ~40 other packages.

**There are two patterns, not one, and the second is where a file list goes
wrong.** Everything above is the `fugaro init` pattern, and it is the census's
pattern. A file can also name the command the way a user types it **without the
`fugaro` in front** — the bare `init --repo` form, which appears in refusal
messages, in comments and, in one case, in a test's argument list. That set is
**15 files**, and none of them is in any count above:

```
comm -13 <(git grep -l 'fugaro init' 82d366af | sort) \
         <(git grep -l -- 'init --repo' 82d366af | sort)   # 15 files
```

- **`internal/cli/gitsecret.go`** — the one that is **live user-facing
  text**: `driftError.Error()`, a refusal that ends "Run %s init --repo %s in
  its checkout first, so the mounts and the grants agree again". This is the
  file that makes the difference matter: the other five live ones are comments,
  but this one prints a command and would print a retired one.
- `internal/cli/shipped_gates_test.go` — the args in
  `TestProvisionBatchRefusedWhileNotShipped`'s table,
  `{"init", "--repo", "--provision-batch"}`, passed to the command tree. It is
  not a string to rename but an entry point to re-point; W2.1 owns it.
- comments only (live, non-test): `internal/backend/backend.go`,
  `internal/cli/budget_preview.go`, `internal/cli/config_publish.go`,
  `internal/config/project_secrets.go`, `internal/runner/policy.go` — W2.2's.
- comments only (tests): `internal/cli/{launch_budget,secrets}_test.go` (W2.1);
  `internal/infra/{statebucket,workdir}_test.go` and
  `internal/runner/{policy,provider_policy,providers}_test.go` (W2.2).
- `docs/design/layered-config.md` (4 lines) — a dated root (D9), left alone
  like the other 30 files in `docs/design/**`, even though the `fugaro init`
  pattern does not count it in the 85.

W0.2's walk matches **both** patterns, so W2.1's and W2.2's lists have to carry
these files or W2.6's "every live file is owned by exactly one of W2.1–W2.5" is
false — which it was, in the first draft of this section.

**Markdown — 111 files** outside this folder name the command
(`git grep -l 'fugaro init' 82d366af -- '*.md'
':(exclude)docs/setup-wizard/**' | wc -l`), and the split between what the
rename touches and what
it leaves alone is **26 live files against 85 in the dated roots** — 85 *at
`82d366af`*, where two of the ten roots do not exist yet (this folder and
`docs/windows-cli/**`, whose plan records postdate it) and so contribute none of
them.

Live (renamed) — 26 files: `README.md` (16 mentions), `CONTRIBUTING.md`,
`docs/gcp-setup.md` (113 — its title is `# GCP setup with \`fugaro init\``),
`docs/gcp-live-checklist.md` (158), `docs/project-layer.md`,
`docs/project-claude-accounts.md`, `docs/multi-model.md`, `docs/git-providers.md`,
`docs/dogfooding.md`, `docs/release.md`, `SECURITY.md` (7), `docs/checkout.md`,
`docs/backends.md`, `docs/base-image.md`, `docs/base-image-migration.md`,
`docs/recipes.md`, `docs/releases/README.md` (the releases *index* is live;
`docs/releases/v*` beside it are the dated records),
`deploy/terraform/gcp/roots/repo/tests/testdata/README.md`,
`plugin/skills/setup/SKILL.md` and `plugin/skills/setup/reference/{decisions,transitions}.md`,
`plugin/skills/working/reference/status.md`,
`.claude/skills/new-release/SKILL.md`, `website/README.md` (1, inside a quoted
test description — the file the Website bullet below lists and this one had
dropped; the count of 26 is right and the enumeration was 25), and the two
**generated** files `website/content/cli-reference.md` and
`website/content/security.md`.

Dated (left alone) — 85 files in the **ten roots**, each root D9's own rule
(F20), and eight of the ten contribute files *at `82d366af`*:
the dated records of this tree's past — `docs/plans/**` (32), `docs/releases/v*`
(12), `docs/design/**` (30) — and the six folders that hold **another
artifact's** documents — `docs/credentials-service/**` (2, 11 mentions),
`docs/website/**` (2, 3 mentions, all in its own `implementation-plan.md` and
`reconciliation.md`; `docs/website/redesign/**` names the command zero times) —
plus the four this section was corrected to exempt, because they are the same
kind of record as the two beside them: `docs/coordinator/**` (2 files, 8
mentions), `docs/in-job-review/**` (1, 4) and `docs/mobile/**` (4, 11, including
its `implementation-plan.md` and its `functional-spec-reconciliation.md`), and
`docs/windows-cli/**` (1 file, 1 mention — it postdates `82d366af`, so it
contributes none of the 85 and its figure comes from
`git grep -c 'fugaro init' -- docs/windows-cli`; D9's paragraph above says why
it is a root) — and,
tenth, `docs/setup-wizard/**` itself, whose four documents name the command
throughout (`git grep -o -c 'fugaro init' -- docs/setup-wizard`) and whose prose
is *about* the retirement. `docs/voice-module/**` and
`docs/pending-workflows/**` name the command zero times and are not on the list.

**Terraform — 29 of the tree's 58 `.tf` files**
(`git grep -l 'fugaro init' 82d366af -- '*.tf' | wc -l`;
`git ls-tree -r --name-only 82d366af | grep -c '\.tf$'`). They name the command
on 54 lines (`git grep -c 'fugaro init' 82d366af -- '*.tf' | awk -F: '{s+=$NF}
END {print s}'`), and the mechanism is mixed: **15 are `description = …`
attributes** on variables and outputs, what `terraform plan` shows
(`git grep -n description 82d366af -- '*.tf' | grep -c 'fugaro init'`), **8 are
`error_message`s** in `validation` blocks (`git grep -n error_message 82d366af
-- '*.tf' | grep -c 'fugaro init'`), and the rest are comments. Examples:
`deploy/terraform/gcp/modules/repo/variables.tf` ("Every name arrives from
`fugaro init --repo`'s terraform.tfvars.json"), and the four
`gcp/roots/{compute,firebase,installation,repo}/tests/*.tftest.hcl` files whose
header comments say the names are "the ones `fugaro init` writes". The same task
(W2.5) also takes `deploy/terraform/terraform.go` — a Go file whose package
comment names the command, and the one Go file outside `internal/` in this
section. Rename, but in its own commit: these are `terraform plan` output and
comments, not fugaro's own text, and an edit is independently reviewable.

**Website.** `website/data/install.yaml` (pinned, 2 mentions),
`website/src/pages/getting-started.astro` (hand-kept, 3 mentions, two anchor
fragments), `website/content/cli-reference.md` (generated — regenerate, never
hand-edit), `website/content/security.md` (generated from `SECURITY.md`),
`website/src/components/Changelog.astro` (1), `website/test/caps-and-costs.mjs`
(1), `website/README.md` (1, inside a quoted test description).
`website/test/docs-hub.mjs` names the command **zero** times — its 450
lines contain no occurrence of `init` at all — so it is **not** on this list and
no task touches it.

**Other.** `internal/config/example.yaml` + `example.go`, `schemas/fugaro.schema.json`,
`images/verify-public.sh`, `images/history/Dockerfile` (one comment naming
`fugaro init --firebase`), and the **two goldens that hold printed command
text**, which no other category above covers —
`internal/cli/testdata/init_commands.golden` (**11** mentions, every stage's
"left for you" text) and `internal/cli/testdata/init_retire.golden` (**4**),
both named by `git grep -l 'fugaro init' 82d366af -- 'internal/cli/testdata'`
and both regenerated by W2.1 through their own tests. The third golden the plan
names, `internal/cli/testdata/init_review.golden`, is **not** on the list
because it names the command **zero** times: the review screen builds its
command as `args := []string{"init"}`, with the program name supplied elsewhere
— the same reason `init_review.go` itself is off the Go census and on W2.1's
list (the `internal/cli` bullet above, and W2.1 in the plan).

---

## 5. Decisions

**All twelve were answered by the owner on 2026-10-20.** Each entry keeps the
recommendation it was written with, the reason for it, the alternative and its
cost, and then the answer, marked **Answered 2026-10-20** in its own line. The
answers — not the recommendations — are what the
[implementation plan](implementation-plan.md) is built on.

**Eleven of the twelve are answered as recommended**: D1, D2, D3, D4, D5, D6
(with both of its sub-questions), D7, D8, D9, D10 and D11. The exception is
**D12**, answered the other way — `--yes` covers everything `update` owns —
and its entry is where that is written out.

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

**Answered 2026-10-20: as recommended — two commands, no merge.** `fugaro
upgrade` stays per checkout and `fugaro update` stays the installation's, and
the one-line boundary above is the text W5.1 documents. Nothing in the plan
merges them, and Slice 7 keeps no task for it.

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

**Answered 2026-10-20: as recommended — no alias, a tombstone.** W1.2's
`retired.go` is the hidden command: it applies nothing, exits non-zero, and
says the one sentence naming both successors.

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

**Answered 2026-10-20: as recommended — only with a typed confirmation, only
when the API is disabled, and never when the quota project is not the project
being set up.** W4.4's refusal is written to exactly these three rules, and
`--yes` never covers the enable.

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

**Answered 2026-10-20: as recommended** — the id defaults to
`fugaro-<project name>` with `suggestGCPProject()`'s value offered as the second
choice when it differs; the region stays `defaultRegion = "us-east5"`, shown as
the default and never derived from the Firestore location (C1); and the double
prefix `fugaro-runs-fugaro-<name>` is accepted, with nothing about bucket naming
changed by this work. W3.1 and W3.2 are the tasks.

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

**Answered 2026-10-20: as recommended — `setup` owns enrolment, `update` never
enrols.** W3.4 is the wizard's first-repository question; W5.1 is `update`'s
one-line refusal and its exit code 1.

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

**Answered 2026-10-20: as recommended, both sub-questions included** — the
wizard lists the open accounts the logged-in user can use, in the interactive
wizard at a real terminal only, never under `--json`, `--non-interactive` or an
agent session, and never stored; the picker asks the Cloud Billing API with the
user's ADC **and** falls back to `gcloud billing accounts list` when the API is
unavailable and gcloud is on PATH, saying which one answered; and the account a
project is **already linked to** is **not** printed when adopting an existing
project. The rule in `internal/infra/projectcreate.go` and
`internal/cli/init_project.go` is reversed **narrowly and only here**, and W4.1,
W4.3 and W4.5 carry the reversal and the two comments it falsifies.

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

**Answered 2026-10-20: as recommended** — `--firebase` becomes
`--firebase-project`, advanced, defaulting to the GCP project id, and the ~50
"re-run `fugaro init --firebase`" strings are rewritten to `fugaro update` in
one pass (W3.3, W2.1).

*Alternative:* keep `--firebase` and add `--firebase-project` beside it. Cost:
two names for one thing, which is the confusion the brief opened with.

### D8 — `fugaro setup` (CLI) beside `/fugaro:setup` (skill)

**Recommend: keep both names.** The skill runs *after* the wizard (F17) and its
own steps are unchanged apart from the CLI name it hands the user. One story —
"setup" — told in two places, CLI first, is better than inventing a second word;
and the retired-skill test (`plugin/readme_test.go`'s
`TestReadmeNamesNoRetiredSkill`) already forbids the obvious alternative — a
per-command skill named after one of the retired verbs, which its `retired`
regex lists by name. The skill's opening line must say which
one it is: "`fugaro setup` (the CLI) did the cloud; this skill writes
`fugaro.yaml` and opens the pull request."

**Answered 2026-10-20: as recommended — keep both names.** The skill keeps its
name and its order, and only the CLI name it hands the user moves (W2.3).

*Alternative:* rename the skill (`/fugaro:configure`, `/fugaro:repo`). Cost: a
new skill name, four pinned tests, and a user who now has to remember two
unrelated words for the same moment.

### D9 — Where is the line between "rename" and "leave the record alone" (F20)?

**Recommend: rename live text, never rewrite dated records.** Renamed:
user-facing strings, skill files, README, the live `docs/` guides, generated
website content, Terraform descriptions. Left alone — **ten roots**, and the
list was corrected when this decision was answered, because three of them were
inconsistent with the rule that exempted their twins: `docs/plans/**`,
`docs/releases/v*`, `docs/design/**` describing shipped state,
`docs/credentials-service/**`, `docs/website/**`, then
**`docs/coordinator/**`, `docs/in-job-review/**` and `docs/mobile/**`** — each of
which holds another artifact's `reconciliation.md` (and, for the mobile work, an
`implementation-plan.md`), exactly like the credentials-service and website
folders already on the list — and **this whole folder** itself: its
spec.md is the owner's words, and its reconciliation and plan cannot describe a
rename without naming what is renamed. The ten roots are the plan's one
`datedRecords` constant, written in W0.2. This is the tree's own precedent, in
`TestReadmeNamesNoRetiredSkill`'s `skip` regex, and it is the only way a
~790-line rename stays reviewable. The plan adds one **new** guard test that
asserts the split explicitly, so the line is enforced and not merely intended.
Exempting a root that names the command nowhere is the case the guard forbids —
so `docs/voice-module/**` and `docs/pending-workflows/**`
are **not** on the list, and `docs/website/redesign/**` is not a root of its own.
**The tenth root was added after this answer, and it is the answer applied once
more rather than changed.** `docs/windows-cli/**` does not exist at `82d366af`,
so the first draft read it the way it read `docs/voice-module/**` — an artifact
folder naming the command zero times — and that was true of an absent folder and
false of the tree: it holds another artifact's four plan documents (`spec.md`,
`reconciliation.md`, `implementation-plan.md`, `README.md`, the `docs/mobile/`
shape) and its own reconciliation names `fugaro init` once, in a cross-reference
to this document's count. By the rule that made the other artifact folders
roots, it is one; the count is read with the command
(`git grep -c 'fugaro init' -- docs/windows-cli`) rather than at a commit where
the folder is missing.

**Answered 2026-10-20: as recommended, with the root list above — nine as the
answer was taken, **ten** now that `docs/windows-cli/**` is read against the
tree rather than against `82d366af`.** W0.2 writes
the constant and W2.6's flip is what enforces it.

*Alternative:* rename everything including history. Cost: ~30 dated design and
plan documents rewritten so they no longer describe what actually happened, and
a diff nobody can review.

### D10 — Check 27, and the number for the picker

**Recommend: rewrite Check 27 in place for `fugaro setup`, and take 111 for the
billing picker.** Check 27 was written for a path (`fugaro init
--create-project --link-billing`) that this work renames and re-fronts, so its
command lines are stale text; keeping the number avoids churning the register.
Its status is the register's own contradiction (F22): the section says NOT RUN
and carries a PASS/VERIFIED run of that very path, so the honest reason to re-run
it is that **the rewritten steps** are the ones no one has performed, not that
nobody ever walked the path. Its rewritten form must cover: the wizard from an
empty shell, the default id `fugaro-<name>` accepted by Enter, the typed project
creation, the **billing picker against a real account** (does the Cloud Billing
API answer, does gcloud fall back correctly, does an account with no permission
produce the guided line rather than a crash), the typed billing link, the typed
Firestore location, the first image build, installation, Firebase, and the first
repository — then `fugaro update` on the same installation **twice**, asserting
"No changes" the second time.

**It must also keep everything the check proves today**, with the commands
renamed: the plugin wiring and the four Claude Code spikes (V1–V4), the image
mirror against the real ghcr.io and Artifact Registry, the hidden secret prompts
and their refusals, the **hostile-clone gate**, `doctor` and `update-skills`, the
teammate's adopt run, `/fugaro:setup`, and the **prompt-injection probe**. The
two security probes are the reason this is a requirement and not a preference:
`grep -n 'injection\|hostile' docs/gcp-live-checklist.md` returns Check 27's
register row and its section and nothing else in the whole register, so a
rewrite that replaced the text instead of re-fronting it would delete the only
live coverage of either — the "register with a hole in it" this decision's
alternative was rejected for. So the rewrite is **rename the commands, keep the
probes**: the wizard's sequence goes in front, and every step that probes
something stays.

**Check 111** covers the picker's failure modes only: API disabled on the quota
project, a user with no `billing.accounts.list`, no TTY, `--json`, and an agent
session — each asserting the guided fallback and that nothing was enabled.
Both are owner-run and sandbox only. Check 111 is new, so nothing in it has been
run. Check 27's **rewritten** steps are unrun — and so is its current text's, in
the sense that matters: the run recorded under the section was against the
commands this work renames (F22). W0.3 is where the section's two status
statements are reconciled, because it is the task that rewrites the section.

**Answered 2026-10-20: as recommended — Check 27 is rewritten in place and
Check 111 is the picker's.** W0.3 writes both, W6.1 and W6.2 are the owner's
runs, and the rewrite is **rename the commands, keep the probes**.

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

**Answered 2026-10-20: as recommended — `setup` creates by default and degrades
to `update` on an installation that already exists.** The first question is the
project id, with `fugaro-<name>` (new) as the default answer and adoption as the
second; `--create-project` stays for a scripted run; and the project stage is
always registered in `setup` (W1.3).

*Alternative:* keep creation strictly opt-in (`--create-project` required, as
today) and make the wizard adopt-only unless told otherwise. Cost: the brief's
own example command has to keep its flag, and the first-time user — who has no
project to adopt — is refused by the wizard that exists to set them up.

### D12 — Is `update --yes` as non-interactive as the brief's "non-interactive friendly" sounds (F7, F16)?

**This decision was opened by the review of this plan, not by the brief.** The
brief says `fugaro update` is the re-converge "after a CLI upgrade", and calls
it "non-interactive friendly"; the plan's first draft read that as *`--yes`
applies everything `update` owns with no typed confirmation* — which
`internal/initflow/stage.go` contradicts. `runCovered` covers the ordinary steps
of Installation, Firebase, Images, Installation2 and Repository, and its own
comment says that **within** those stages the Firestore database's location, a
**Cloud Build**, replacing a registry tag and onboarding an unlisted repository
**stay `Typed`** — the sentence the answer below makes false, and which the plan
now has W5.1 correct, in that comment only: nothing else in the file moves.
(Those four are `internal/cli` call sites rather than entries
in the map — the map is stage names — and two of them ride stages `YesCovers` is
already true for. The section below says which, because D12's whole answer is
built there.) A Cloud Build is billable and the Firestore location is
permanent, so `update --yes` can legitimately stop on an upgrade that has to
rebuild an image — and W0.1's matrix pins that no row may be relaxed.

**Recommend: keep `update`'s confirmations identical to `setup`'s.** Same five
covered stages, same four Typed steps, nothing relaxed for the re-converge. The
cost is that "non-interactive friendly" means *"no question that the run does
not already have to ask"*: a CI job that upgrades the CLI and rebuilds an image
with no terminal gets `needs-you` and exit code 1 (the loop's existing "left for
you"), not a silent build. That is the honest reading, it needs no code change
to `runCovered`, and it keeps one matrix for both front doors.

**Answered 2026-10-20: the other way — `--yes` covers everything `update`
owns.** The owner overruled the recommendation. `fugaro update --yes` is total
over the stage set it registers — preflight, installation, Firebase, images,
installation (second pass), secrets, plugin and each enrolled repository — with
**no step left `Typed`**: the Firestore database's location, a Cloud Build,
replacing a registry tag and onboarding a repository the run was not told about
are all covered when `update` asks for them. A CI job
that upgrades the CLI and has to rebuild an image gets the build, not
`needs-you` and exit code 1.

`setup` is where the confirmations stay, and the answer names **exactly three**:
creating the project (either banner) and linking the billing account — both
`setup`'s alone, since `update` refuses `--create-project` and `--link-billing`
outright — and the Firestore database's location, which `setup` keeps `Typed`
while `update` covers it. So the rule is exact rather than approximate: `--yes`
never covers **creating the project or linking the billing account, in either
front door**, and never covers **the Firestore database's location in `setup`**
— the only door that asks for it as a first-time question.

The answer is written out row by row rather than summarised, because the rest of
the matrix is what actually moves: **a billable first image build**,
**replacing a registry tag** and **onboarding an unlisted repository** become
ordinary covered rows **in both front doors**, where they are `Typed` today, and
**the Firestore database's location** becomes covered **in `update` alone**,
where `setup` keeps it typed. Those **four** rows are the whole of what this
answer changes about a confirmation; the location is the one that moves in one
door rather than two, and the paragraph above is where that is stated.

**A fifth row, added later (update-converge UC3b, 2026-10-31).** `fugaro update`
gained a `checkout` stage that runs `fugaro upgrade`'s own pin, plugin and cloud
steps for the checkout it is run in, so `update` reaches the image refresh too:
**a rebuild of the checkout's workflow images** — one billable build, one
confirmation each, as `fugaro upgrade` and `fugaro image refresh` ask — is now
one of the steps `update` owns, and `--yes` covers it the same way this answer
covers a repository's first build. It is `update`'s alone (`setup` registers no
such stage), it is listed on `update`'s review screen beside the other steps that
keep their own confirmations, and without `--yes` and without a terminal the
stage leaves the whole checkout for the user rather than asking. Everything else
in this answer stands: an image rebuild is an **ordinary** covered row, so it
moves nothing in the "never covers" list.

What does **not** move: `canType()`'s four conditions and every refusal of F8 (an
agent session still applies nothing, `--yes` or not); the secrets stage's
`noYes` in both front doors — a secret is a *value*, typed in a hidden prompt,
which `--yes` cannot supply, and this answer is about confirmations, not values;
and the read-only preview, which `--yes` never skips (F10).

**Where it lands.** W5.1's contract is now the opposite of the recommendation,
and — because of where those four steps are actually enforced — its opposite is
expressed at the **call sites**, not in `internal/initflow`. `runCovered` is a
map of five stage *names*; the typed steps inside those stages are `askTyped`
and `canConfirmTyped(initflow.Typed, …)` guards in `internal/cli`, and two of
them ride stages whose `YesCovers` is **already true** (the Firestore location
rides `Firebase` — `init_firebase.go`'s `promptLeft(initflow.Firebase, …)` —
and the billable build rides `Images`), so no change to `runCovered` could
relax them and no test written against `YesCovers` could pin them. W5.1
therefore writes one line in `internal/initflow/stage.go` — the `runCovered`
comment, whose "stay `Typed`" clause this answer falsifies, restated
door-neutrally because the package names no command — and changes nothing else
there: the lane written for it in an earlier draft, `W1.1 (L0) → W5.1 (L7)`,
stands as a **comment-only** one after being withdrawn as a code change. W5.1
relaxes `init_firebase.go`'s `confirmLocation` (in `update` only),
`init.go`'s `buildImages`, `init_images.go`'s `confirmReplace` and
`init_repo_gate.go`'s `confirmRepo`. W0.1's matrix gains `update`'s column with
every row covered, its stage table keeps the five covered and three `noYes`
stages exactly as they are, and its call-site table carries the four steps
above; "no row may be relaxed" becomes "**these three rows may never be
relaxed, in either front door**". The one assertion outside W0.1's file that
this row reaches is W3.4's: the `setup`-side onboarding test is written at L5
**as today's value** — the question W3.4 adds authorizes nothing by itself, so
under `--yes` the gate is unchanged — and W5.1's commit renames and inverts it
in `internal/cli/init_repo_gate_test.go`, in the same change that flips W0.1's
row, because this step is covered in both front doors.

*Alternative:* give `update` a way to cover the billable and permanent steps
non-interactively — a `--rebuild` or `--accept-billable` flag, or refusing those
stages under `--yes` and exiting 1 with the reason. Cost: a second confirmation
class to reason about, a new row of W0.1's matrix that no longer holds for
`setup`, and a flag whose whole purpose is to skip a confirmation — which is the
one kind of change `SECURITY.md` and F8/F9 are meant to make hard. It is the
owner's call, and until it is made no task relaxes a row.

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
| The exact count of places naming `fugaro init` | Counted by grep on `main` at `82d366af`: 410 non-test Go lines across 107 files, 378 test lines across 106 files, 111 markdown files outside `docs/setup-wizard/` (26 live and 85 in the ten dated roots, eight of which hold them at that commit), 29 Terraform files naming the command out of 58. Each figure's command is stated next to it in §4 and C3, so a different grep is a different number by construction; the plan does not depend on the counts, only on the categories — except in the one place where a list *is* the work, which is W2.1's `internal/cli` files, and those are named one by one. |
| That the wizard does not break `internal/cli/docs_checkout_test.go`'s command resolution | Not run. It resolves `` `fugaro …` `` against `NewRootCmd()`; it is expected to **fail** until the docs are renamed in the same commit, which is the point. |
