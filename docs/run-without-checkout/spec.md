# Launching a run for a repository without its checkout: spec

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved.**

The numbered decisions this file leans on (**D901**–**D919**) are in
[reconciliation.md §3](reconciliation.md), each with the cost of the alternative and the owner's answer
of 2026-11-04 beside it; where this file and that one disagree, that one wins. **The answer this file
is written around is D902's, and it is not the design's recommendation: the read is made through the
installation's credentials service, as the operator themselves, so the CLI holds no App key and reads
no environment variable.**

## 1. The decision, and what it reads today

A run is one execution of one Cloud Run job, and the job is chosen by the launcher before anything
is started. `internal/cli/project_job.go`'s `launchProjectJobFor` is that choice, and it makes it
from the checkout in the working directory alone:

1. `co := checkoutConfig(ctx, spec.Repo)` — the `fugaro.yaml` of a checkout of `spec.Repo` whose
   `origin` is `spec.Repo` (`internal/cli/cloud.go`'s `checkoutRoot`/`checkoutParse`). No checkout
   in the working directory, or no file in it, and `co` is `nil`.
2. `w, ok := co.Workflows[spec.Workflow]` — the workflow's **`checkout:` kind**. Anything but
   `clone` is the per-repository job (`backend.LaunchSpec.Job` empty, which
   `internal/backend/backend.go` resolves to `gcp.JobName(slug, gcp.WorkflowVariant(...))`).
3. The published policy, then `w.JobProfileOrDefault()`, then `p.ProfileFor(profile)`. A profile the
   policy does not define is not this launcher's to refuse: the run is left to the per-repository
   job and the runner's own `checkJobProfile` refuses it at run start.
4. `provider := e.repoProvider(spec.Repo, co)`, and `provider != pr.Provider ||
   !infra.SharedJobProvider(pr.Provider)` keeps the per-repository job — a profile serves exactly
   its own provider, and no shared job serves Bitbucket (`internal/infra/projectjobs.go`, the one
   place both the deployer and the launcher read that fact).
5. `profileAdmissionRefusal`, `projectJobAgentAuth` (which reads the ref's `fugaro.yaml` **out of
   the checkout's own object database**, `checkoutFugaroYAMLAtRef`, `git cat-file`, never the
   network and never the working tree) and `projectJobAccountFor` — three gates, then
   `gcp.ProjectJobName(profile, provider, account)` and the job's timeout.

Step 1 is the whole problem: without a checkout, the launcher answers "no" to a question that has an
answer, and the answer is one Cloud Run job's name.

**What the tree does today.** #1477 has **merged**: a `co == nil` launch returns a **refusal** when the
project's published policy carries at least one profile, and `(nil, nil)` — the per-repository job,
"the job it has always had" — only when it carries none. That refusal is what this spec answers, and
`reconciliation.md` F1 records the state it is in. Its message names both causes of the missing
checkout ("the working directory is not `X`'s checkout, or its `fugaro.yaml` could not be read") but
gives one way out for both, and `docs/checkout.md`'s "Known gaps" records the consequence: the console
has nowhere else to launch from (F9), so on an installation whose policy defines a profile every
console launch of every repository is refused.

## 2. Three sources, in this order, and no fourth

The document is learned from the first source that can answer. **The trigger is narrow on purpose:
none of this runs unless the code is about to refuse.** A launch that works today is not touched —
not its job, not its messages, not its cost.

| # | Source | When it answers | Cost |
| --- | --- | --- | --- |
| 1 | The checkout's **object database** at the ref: `git cat-file blob <at>:fugaro.yaml`, the reader `checkoutFugaroYAMLAtRef` already is, promoted from the auth axis to the whole document | A checkout of the repository exists in the working directory (so `checkoutRoot` names one), and the ref's commit carries the file — even when the working tree has another branch checked out, or the file is not in the tree at all | One local `git` call, no network, no credential |
| 2 | The **job the run being continued recorded**: `runstore.Launch.Job` from the run's own `launch.json`, backfilled from `result.json`'s execution when the CLI died before writing one — `existingLaunch` (`internal/cli/run.go`) already does exactly this backfill | `--retry`, and a follow-up (`--pr`) whose previous run has a launch record | One object read in the runs bucket, which the command already makes |
| 3 | The **forge**: `fugaro.yaml` at `spec.Ref` of `spec.Repo`, read with a read-only installation token the installation's **credentials service** mints for the operator as a person (§5) | A human session, no checkout of the repository anywhere, the installation has opted into the credentials service, and the repository's provider is one a shared job serves (`infra.SharedJobProvider`, today `github`) | One credentials-service call — audited, and answered only for a repository the installation holds — and one API call |
| — | **The refusal**, in the shape #1477 gives it, naming the cause and the way out (§7) | None of the three answers | — |

Two rules hold the order together.

**Source 1 is tried first because it is free and exact.** It answers for the checkout the operator
is standing in, at the same ref the checker reads, from the same objects — so a launcher and a
runner reading it cannot disagree. It also fixes a case today's code gets wrong for a reason that
has nothing to do with the console: `checkoutParse` returns `nil` both when there is no checkout
**and when there is one whose working tree has no `fugaro.yaml`** (its `readFugaroYAML` error path
leaves the result zero). An operator in a checkout of `X`, on a branch whose tree predates the file,
is told today (by the merged refusal) to "run it from `X`'s checkout" — which is where they are.

**Source 3 is the last resort and only where a shared job is possible at all.** For a repository
whose provider no shared job serves, there is nothing to learn: step 4 above keeps the
per-repository job for every Bitbucket repository whatever its `fugaro.yaml` says, and the
repository's provider is known without a checkout — `repoSlug` has already refused the launch if the
local config does not name it. So the launch without a checkout proceeds on the per-repository job
with **no read and no credential**, and that is not a guess: it is the same answer the checkout path
gives for the same repository. (Narrowing, D901.)

**Source 3 is also bounded by the installation's own opt-in, and that is the cost of the owner's answer
to D902.** The credentials service ships dormant — an installation opts in with `fugaro update
--provision-credentials`, the owner's own command — and making the read the service's means the
launcher can read nothing where there is no service: those launches keep the tree's refusal, with the
checkout as the way out (§7, and reconciliation O4). It is not a regression — the tree refuses them
today — and it is the trade the answer makes for a launcher that holds no App key at all.

**There is no fourth source, and in particular there is no guessing `standard`.** The project layer
can carry a workflow's `checkout:` kind (`docs/project-layer.md`, the key's layers are `profile,
repo`) but **never a `job_profile`** — that key is `repo` only. So without the repository's own file
the launcher can know a workflow is a clone workflow and still not know which shared job it runs on,
and `standard` is exactly the wrong answer in the case that matters: a project publishes profiles
because its repositories name them. **The layer is also never resolved on its own.** The config the
launcher's decision reads is the repository's document with the project layer merged *under* it —
`checkoutParse` reads the file first and only then calls `resolveFugaroYAML` with it
(`internal/cli/cloud.go`), so a repository with no document anywhere has no merged config, no
`checkout:` kind and no profile to read, and its launch keeps the per-repository job (§7). A layer-only
resolution would place a run by a document the checkout path never reads alone, and would do it on the
`standard` default this paragraph exists to refuse.

## 3. The ref

**The document is read at `spec.Ref` — the ref the run checks out — and at no other ref.** (D903.)

- It is the only ref that keeps the launcher and the runner in agreement. The runner resolves the
  workflow and the profile from `origin/<spec.Ref>` and halts when the job it runs on does not match
  (`checkJobProfile`, `internal/runner/project.go`; `checkProjectJobAccount`,
  `internal/runner/projectjob.go`). A launcher that read another ref would turn its own decision
  into a paid halt at run start.
- It is what the checkout path already does. `checkoutFugaroYAMLAtRef` reads the ref's file from the
  objects, and `projectJobAgentAuth` judges the run's credential by it. A launch from a checkout of
  `X` at `topic` is already placed by `topic`'s `fugaro.yaml`. Reading at `spec.Ref` therefore grants
  a branch author **no authority a checkout launch does not already grant** (§4).
- For the console it is the base branch, and nothing exotic: the console's launch child passes no
  `--ref` (it is on `NEVER_REACHABLE`), so `spec.Ref` is resolved from the layers as always
  (`internal/cli/basebranch.go`) and a console launch is placed by the base branch's document. A
  console launch can never be influenced by a branch. For `fugaro run --ref topic --repo X`, the
  document read is `topic`'s — as it is today from `X`'s checkout.

A ref the forge cannot resolve — a deleted branch, a force-pushed one, a ref that only exists
locally — is a **failure**, not a licence to fall back (§7). The alternative (read the default
branch instead) is D903's rejected option.

## 4. What the document is trusted for

The launcher trusts the ref's `fugaro.yaml` for exactly two things: the workflow's `checkout:` kind
and its `job_profile`. Both then pass through gates that were not written by the file's author:

| Gate | What it holds | Where |
| --- | --- | --- |
| `profileAdmissionRefusal` | the repository must be admitted to the profile by the **published policy**'s own `allow_repos` (the project's list and the profile's, `projectpolicy.Admits`) | launcher, `run_allowlist.go` |
| `projectJobAccountFor` / `checkProjectJobAccountCovers` | the account axis: the job named is the one that holds the Claude credential the run selected, and the token it holds must cover this repository | launcher |
| `checkProjectJobAccount` | the same account question, re-decided at run start | runner |
| `checkJobProfile` | the run's job profile must be the workflow's `JobProfileOrDefault()`, re-decided at run start; a mismatch **halts** the run | runner, `internal/runner/project.go` |
| `admissionCheck` / `AdmissionRefusal` | the repository is held to the profile's `allow_repos` again, before any git credential exists | runner |

So the most a hostile branch can do is select, among the profiles the published policy already
admits *that repository* to, the one its run would use — which is precisely what a branch does today
when the operator launches from a checkout at that branch. It cannot create a profile, cannot widen
an `allow_repos`, cannot name a job the project did not deploy, and cannot reach another
repository's credential: a shared job holds one credential for the repositories its profile admits,
and the admission is the policy's.

**What changes for the operator is the audience of that choice, not the choice.** A branch author
who could influence a *console* launch would be new; the console always launches at the base branch,
so they cannot. A branch author who can influence a `fugaro run --ref <their branch> --repo X` run
from outside `X`'s checkout gains nothing over `fugaro run --ref <their branch>` from inside it.

## 5. The credential

Source 3 is the one part of this design that puts a provider credential in the launcher's process, and
**the owner's answer to D902 is that it is not the installation's own**: the read is made through the
installation's **credentials service**, as the person running the command. The design's own
recommendation was the App's key, read as `--check-github-app` reads it, and the owner did not take it.

- **What the launcher presents:** its own Google ID token, minted with `gcloud auth
  print-identity-token`. The service verifies it against the installation's own **people list**
  (`internal/credsvc`'s `VerifyPersonCaller`, the class the read mints admit), and a caller who is not
  one of them is refused there, by name, before anything is signed.
- **What the service does:** mints a **read-only** GitHub installation token for exactly one
  repository, from the App key the service's bundle holds. **The launcher never holds that key:** no
  secret is read, no key material enters the process, and the client has no field in which to ask for
  another permission (`internal/gitprov/github/serviceread.go`). Every mint writes one audit line the
  launcher cannot suppress (`internal/credsvc`'s `PurposeGitHubRead`: the person who asked and the
  repository it was for). **The permission the read needs is not the one the route mints with today.**
  The route asks with `github.CoordinatorTokenPermissions`, which is `pull requests` and `metadata`,
  read — the coordinator reads no file, and its set is pinned by two tests so that it cannot quietly
  grow one — so `GET .../contents/fugaro.yaml` is refused with today's token, before the permissions
  question is answered at all (reconciliation F6). The set the read needs carries `contents: read`, and
  it is the first task of the plan (A0; the choice between a set of its own and a route of its own is
  D915). Until it lands, this section describes the read as it is meant to work and A0 is what makes it
  work; the set **never** becomes the run's (`RunTokenPermissions`) and never widens the coordinator's.
- **Which repository:** `spec.Repo`, and only it. The service resolves the name through the
  installation's own inventory and refuses a repository it does not hold, so a refusal to install the
  App on a repository is a refusal to read it.
- **Which App:** the App ID travels in the request body and is not a secret — the same number the
  platform passes to a job as `FUGARO_GITHUB_APP_ID` — and the launcher takes it from the project's own
  recorded value, `repos.<repo>.github_app_id`. **No environment variable is read**, by the launcher or
  by the service.
- **Where the service is:** the installation's published `credentials_service.url` (`internal/localcfg`),
  the block the credentials stage writes and the shared config carries to every machine of the
  installation. An installation without that block has no read at all (§2, §7).
- **One request:** `GET /repos/{owner}/{repo}/contents/fugaro.yaml?ref={spec.Ref}`, through the
  replaceable seams the package already reads a base URL and an HTTP client from (`github.Options`'s
  `BaseURL` and `HTTP`), so a test replays it (`internal/gitprov/httpfixture`).
  The token is held for that call and stored nowhere. The mint's client already exists
  (`github.ServiceReadTokens`, a `gitprov.TokenSource`), and so does the reader: `github.Provider`
  implements `gitprov.ContentsReader` (`Contents(ctx, path, ref)`, `internal/gitprov/github/contents.go`),
  and a provider is built to hold no key at all by `Options.ServiceHeldKey` plus `UseServiceTokens`,
  which takes any `gitprov.TokenSource` — including the person's read mint. What does not exist is the
  launcher's own person identity (reconciliation F13), and what does not cover the read is the
  permission set the mint carries today (F6, §5's bullet above).
- **Refused in a coding agent's session, before anything is read** — `refuseKeyReadInAgent`, with the
  launch named where the flag is named, exactly as `internal/cli/doctor_brokering.go` already refuses
  the credentials service's bundle. There, the launch refuses as the tree refuses it today, and an
  agent's way out is the right one: run it from the repository's checkout.
- **No new secret, no new stored token, no new principal and no new IAM binding** — but **not no
  credentials-service change and not no operator step.** The service, its people list, its bundle, its
  route and its audit line are all in the tree (the read mint shipped in 0.18.0), and the launcher is
  one more caller of a route that exists; the person's own identity is the credential every other
  command a person runs already reaches the project with. What does change is the service's own mint:
  the set it asks with has to cover a file read (A0, D915), and **the credentials service must be
  redeployed for the installation to serve it** — an installation that has run the version carrying A0
  but not redeployed the service still mints the old set, and the read is refused, so the launch keeps
  the tree's refusal until it has (§7). The limit is also where the service is not: see §2.

The rejected credentials, with their costs, are D902's alternatives, including the one this design
recommended: the installation's **App key**, read as `--check-github-app` reads it (the launcher would
hold an installation-wide credential, one that can mint for every repository the App serves, to read
one file); the operator's ambient forge credential (`gh`, a git credential helper: Fugaro would read a
person's credential out of another tool that may not exist, and `docs/git-providers.md`'s "the CLI
holds no provider credential" would stop being true in a new way); and a new token the operator stores
for launches (a new secret to create and rotate that duplicates the App).

## 6. Caching

**There is no cache.** (D904.) The launch performs at most one forge read, holds the bytes for the
duration of the command, and writes nothing to disk — no file under the config directory, no object
in a bucket, no entry in the run's record beyond the job name it already stores.

- The redundant reads within one command are already memoized where they happen: the CLI's
  `checkoutParseCache` (working tree, default branch, layer), `checkoutOf`'s one-shot closure, and
  the policy read. Source 3's read is memoized per (repo, ref) in the same command-scoped way, so a
  retry loop or a batch that re-asks the same question pays once.
- A cache that outlives the command would let a document the ref no longer carries place a run —
  the one failure a job decision must not have, because the run and the launcher would then read
  different files and the run would halt after the claim and the budget token were spent.
- The cost of the alternative is D904's: a file under the config directory keyed by (repo, ref) is a
  repository's content cached outside the repository, with no invalidation story that is honest
  about a force-push.

## 7. Failure modes and their messages

Every message names the repository, the ref, what was tried, and the way out. Nothing falls back to
the per-repository job: a failed read means the job **cannot be decided**, and a run silently placed
on a job the project's policy did not choose is worse than a refusal (D905).

| Case | What happens |
| --- | --- |
| No checkout anywhere, provider is one a shared job serves, the installation has **no credentials service** (it has not opted in) | Refused. The message says so in one line and keeps the checkout as the way out: `cannot read owner/repo-a's fugaro.yaml at main as yourself: this installation has no credentials service, so the read that decides the run's job cannot be made. Run it from owner/repo-a's checkout` |
| No checkout, the **caller is not one of the installation's people**, or the repository is **not one the installation holds** | Refused, the service's own refusal relayed (the service names both causes and never the caller's address), same way out |
| No checkout, the service is there but its mint's set does not cover a file read — an installation whose credentials service was not redeployed after A0, or an App not granted `contents` | Refused: `cannot read owner/repo-a's fugaro.yaml at main: the installation's credentials service minted a token that cannot read a file; redeploy it with fugaro update`. The message names the service and the redeploy, never the token and never GitHub's own refusal text |
| No checkout, **no network**, or the credentials service or the forge answers 5xx | Refused, `remote(...)`'s wording for an unreachable service, same way out |
| No checkout, the ref does not exist at the forge | Refused, naming the ref: a deleted or never-pushed branch is not a reason to read another one |
| No checkout, the file **is there but the workflow is not**, or has no `checkout:` kind we can read | Unchanged from today: `resolveWorkflow`'s "cannot tell which workflow of `%s` to run; pass `--workflow`", and the per-repository job for anything but `clone` |
| No checkout, the file is **not in the repository at all** | The per-repository job — and this is a **decision**, not a reading of the layers: a document that is not there selects no profile and no clone workflow, the project layer is never resolved on its own (below), so the launcher reaches the same answer `main` reaches for a workflow the checkout's file does not define (`!ok` → the per-repository job, §1's step 2). The runner's own `checkJobProfile` still judges whatever the document at the ref says when the run starts |
| **A coding agent's session**, no checkout, profile published | Refused by `refuseKeyReadInAgent` before any read, naming the checkout as the way out |
| A **checkout exists** but its working tree has no `fugaro.yaml` | Source 1 answers: the ref's file is read from the checkout's objects, and the launch is decided as if the file were in the tree. If the ref's commit genuinely has no file, the row above applies |
| `--retry` / `--pr` of a run that has a launch record | The recorded job (§8, D907); no read |
| `--retry` / `--pr` of a run that never launched | Source 1, then source 3, as for any other launch |

The tree's message stops being the one an operator sees for the last two rows, and for the launch whose
checkout simply does not carry the file. The message is the design's, not a cosmetic afterthought: the
merged text names both causes and then gives one way out for both, and the operator standing in the
checkout — the row above — is the one it does not help.

## 8. `--retry` and `--pr`

**A repeat and a follow-up reuse the job the run they continue recorded.** (D907.) `runstore.Launch`
records `Job` in `launch.json`, and `existingLaunch` already rebuilds that record from
`result.json`'s execution when the CLI that launched died before writing one — including
`ParseExecution(rec.Execution)`'s job. For `--retry` the run is the same run at the same ref, and for
a follow-up the previous run's `Ref` and `Workflow` are inherited outright (`newFollowUpSpec`,
`internal/cli/followup.go`), so the same workflow at the same ref has the same job by the same
rules.

Reuse cannot widen anything, which is why it is safe to take a bucket object's word for it: the
runner re-decides both axes at run start and halts rather than widening (`checkJobProfile` for the
profile, `checkProjectJobAccount` for the account), and a job name outside the project's policy and
the repository's own jobs is refused there. What it can do is *fail* — if the ref's document changed
between the two runs so that the workflow now selects another profile, the run halts after the
claim; the alternative (re-reading the document and silently changing the job) would launch a
follow-up on a different job than the run it follows, which is worse and is what D907's first
rejected option costs.

**Order:** source 2 is tried before source 3 only for a launch that is continuing a run. A launch
that is not (the console's, a fresh `fugaro run --repo X`) has nothing recorded and goes to source 1
or 3.

## 9. Why the runner cannot pick the job after cloning

The question "let the runner pick the job once it has cloned the repository" has one answer and it is
**no**, and the design should say so rather than leave it as an option:

- A Cloud Run **execution** names its job at creation. The container is started *by* the job; there
  is no point in the run at which another job's container can take over, and the tree has no
  mechanism that re-launches a run onto another job.
- The runner can only **halt**. It reads `origin/<ref>`'s `fugaro.yaml` after cloning, and
  `checkJobProfile` already refuses a run whose job's profile is not its workflow's. So the closest
  thing to "the runner picks" is a run that starts, claims, mints a budget token, bills an execution,
  clones, and then fails with a placement error — the operator's second attempt, after a wasted
  round trip, from a checkout the message tells them to use. Refusing at the launcher is strictly
  cheaper and strictly clearer.
- The launcher is also the only place the choice can be *recorded*: `launch.json`'s `job` is what
  `fugaro logs`, `fugaro cancel`, `fugaro watch` and `fugaro diagnose` read, and a job decided after
  the launch would make that record wrong.

This is why source 3 exists at all. A run cannot be moved to the right job later, so either the
launcher learns the document or the launch is refused.

## 10. The console's launch path and its contract

**The console needs no change, and its contract does not move.** (D906.)

- The console's `run.launch` child always runs in the app's own private, empty directory
  (`console/src/main/launch.ts`; the directory itself is `privateWorkingDirectory`,
  `console/src/main/spawn.ts`), so it is **always** outside every checkout: `checkoutRoot` returns
  `""`, `checkoutParse` returns nil, and source 3 is the only source that can answer. That is why
  this design is not optional for the GUI — with the merged refusal and no source 3, *every* console
  launch on an installation that has published a profile is refused, the repository the operator is
  looking at included.
- The five-field argv schema (`--repo`, `--title=`, `--plan-task=`, `--json`, `--task-file -`) is
  unchanged. The read happens inside the CLI, under the same rules as any other launch, and the
  console does not gain a control, a cached document or a new failure to render: it shows the CLI's
  message verbatim, as it does for every other refusal.
- The console's launches are at the **base branch** (`--ref` is on `NEVER_REACHABLE`), so the
  document read is the base branch's, and no branch can influence a console launch (§3).
- **The console's child needs no new control, and this is not "it already has the credential".** The
  child writes the task to the runs bucket and starts the execution with the operator's ambient
  application-default credential — that is an **access token for Google APIs**, and the credentials
  service verifies a person by an **ID token**, which no Google client library can mint from an
  authorized-user ADC (reconciliation F13). So the ID token is new work inside the CLI (A3's
  `personid.go`, `gcloud auth print-identity-token`), and what makes it invisible to the console is
  where it runs: the mint happens in the `fugaro run` process the console spawns, on the machine the
  operator is sitting at, with the `gcloud` and the account every other command already needs. The
  console passes no flag, reads no new field, and the operator is one of the installation's people, or
  the service refuses the mint and the console renders that refusal (§7). Where the installation has
  not opted into the service there is nothing to read and the launch keeps the tree's refusal (§2), so
  D902's answer is what makes the GUI work on an installation that has. D906's rejected alternative is a sixth field in the launch contract and a
  launch-screen control, which buys nothing here: every console launch is from outside a checkout, so
  the control would be permanently on, and the console's release would have to ship before the GUI
  could launch anything at all on a migrated installation.

## 11. The effect on the refusal

#1477 is merged, so the refusal is `main`'s own behaviour, and it **stays, verbatim in shape**, for the
launches that genuinely cannot be decided. This design **narrows** it in one place that needs no
credential — the provider no shared job serves (§2) — and answers the rest with a read the operator's
own identity can make through the installation's credentials service, or with the run's own record.
**Where the installation has no credentials service that read does not exist and the refusal stands**
(D902's cost, §2).

| Launch | `main` (the merged refusal) | This design |
| --- | --- | --- |
| From `X`'s checkout, `fugaro.yaml` in the tree | decided from the tree | same |
| From `X`'s checkout, tree has no file, ref does | refused, one way out and it is where the operator is | decided from the checkout's objects at the ref |
| Anywhere, no checkout of `X`, Bitbucket, profiles published | refused | per-repository job — no shared Bitbucket job exists |
| Anywhere, no checkout of `X`, GitHub, profiles published, human session, installation has a credentials service | refused | decided by the read through the service, at the ref |
| The same, installation has **no** credentials service | refused | refused, unchanged, naming the checkout |
| The same, the service is there but its mint cannot read a file (not redeployed after A0) | refused | refused, naming the service and the redeploy |
| The same, coding agent's session | refused | refused, `refuseKeyReadInAgent`'s wording added |
| Anywhere, no checkout, **no profiles published** | launch proceeds | unchanged |
| The console's launch of the repository it is showing | refused (broken GUI) | decided by the read at the ref, where the installation has a service |
| `--retry` / `--pr` outside the checkout | refused | the run's recorded job |
| The read cannot be made (no service, network, not a person, repository not held) | refused | refused, the cause and the way out named |

## 12. Threat model

**Assets.** The GitHub App's private key (an installation-wide credential: every repository the App is
installed on, read and write) — **which this design keeps out of the launcher's process altogether**
(D902's answer). The credentials service's bundle, which this design never reads. The run's own
credential. The published policy and its admission lists. The budget.

**The read's authority, as the owner answered it (D902).** It is made with the operator's own Google
identity, on their machine, and it is a call to the installation's credentials service: the service
verifies that identity against the installation's own people list, resolves `spec.Repo` through the
installation's inventory, mints a **read-only, repository-scoped** installation token from the key in
its bundle, and writes one audit line for the act. So the launcher holds no App key, reads no secret
and reads no environment variable; what it holds, for the duration of one API call, is a read-only
token scoped to one repository — never the run's write-capable set, never the repository's pushed
credential. The reach that token carries is the one A0 defines (a file read at least, `contents: read`,
and no write), and the set the service asks with has to be one that reaches a file at all: today's is
`pull requests` and `metadata`, so the read is refused until A0 lands and the service is redeployed
(§5, §7). In a coding agent's session the read does not happen at all, so the token is never minted
for one — `refuseKeyReadInAgent`, the shape `internal/cli/doctor_brokering.go` already applies to the
credentials service's bundle.

**What a hostile branch gains: nothing it does not have.** The document is read at `spec.Ref`, which
is the ref the run checks out and the ref the runner will re-read; the checkout path already places
runs by that ref's file. Every selection the file can express passes the policy's own gates (§4), and
the runner re-decides both axes at run start and halts rather than widening. The two things a branch
author cannot do through this path are worth stating: they cannot make a **console** launch read
their branch (it always reads the base branch), and they cannot select a profile their repository is
not admitted to.

**What the launcher gains.** Exactly one new power: it can read one file of a repository the
installation serves, where before it could read only a checkout's objects. The confused-deputy angle
is pinned to `spec.Repo` and `spec.Ref`: the service mints *for that repository*, resolving the name
through its own inventory, so a repository the installation does not hold is a refusal to read it, and
nothing in the request can name another repository. The power is not the App's: the key stays where it
already was, in the service's bundle.

**What must never happen**, and is a test in the plan:

- **The launcher never holds the App key** (D902's answer): no Secret Manager read for it, no key
  material, no environment variable, and no path in which a key could reach the process the job
  decision runs in.
- The launcher never uses the run's own token set for this read, and asks for no permission at all:
  the set the mint asks with is the service's constant, and A0 gives it one that carries
  `contents: read` — **never a widened `CoordinatorTokenPermissions`**, which is the coordinator's own
  credential's reach and is pinned by two tests that exist to catch exactly that, and never
  `RunTokenPermissions`.
- The launcher never fetches into a checkout: `checkoutFugaroYAMLAtRef` does not fetch today
  ("a fetch is a network write into a repository this command does not own"), and the forge read is
  an API call that does not touch the checkout either.
- The fetched document is never written to disk, and no document is reused across commands (§6).
- The launcher **never falls back to the per-repository job when a read fails** (the owner's answer to
  D905): a read that cannot be made — no credentials service, no network, a caller the service does not
  know, a repository it does not hold, a ref the forge does not have — **refuses**, in §7's refusal
  rows. The per-repository job is the outcome only where the document *was* read, or where the ref's
  commit genuinely carries none and selects nothing, and it is a decision there rather than a fallback:
  nothing in such a document selects a shared job.
- No credential value, token or key appears in an error, a log line or an audit line — the refusal
  names what was missing (the installation's credentials service, the person, the repository), never a
  value.
- The run's recorded job is treated as data from the runs bucket, and its only effect is to name a
  job the runner then re-checks; a name the project did not deploy is refused at run start.
