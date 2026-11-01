# Launching a run for a repository without its checkout: spec

**Status: design only.** Nothing here is implemented. The numbered decisions this file leans on
(**D901**–**D919**) are in [reconciliation.md §3](reconciliation.md), each with the cost of the
alternative; where this file and that one disagree, that one wins.

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

**What the tree does today, and what PR #1477 changes.** On `main` at `432623d9c`, a `co == nil`
launch returns `(nil, nil)` — the per-repository job, "the job it has always had" — and
`docs/checkout.md`'s "Known gaps" records the consequence: a `checkout: clone` workflow launched
with `--repo X` from somewhere else is judged baked. **PR #1477 (open, not on `main`) replaces that
`return nil, nil` with a refusal** when the project's policy carries at least one profile. The
refusal is what this spec answers; it is not in the tree this design was reconciled against, and
`reconciliation.md` F1 records that.

## 2. Three sources, in this order, and no fourth

The document is learned from the first source that can answer. **The trigger is narrow on purpose:
none of this runs unless the code is about to refuse.** A launch that works today is not touched —
not its job, not its messages, not its cost.

| # | Source | When it answers | Cost |
| --- | --- | --- | --- |
| 1 | The checkout's **object database** at the ref: `git cat-file blob <at>:fugaro.yaml`, the reader `checkoutFugaroYAMLAtRef` already is, promoted from the auth axis to the whole document | A checkout of the repository exists in the working directory (so `checkoutRoot` names one), and the ref's commit carries the file — even when the working tree has another branch checked out, or the file is not in the tree at all | One local `git` call, no network, no credential |
| 2 | The **job the run being continued recorded**: `runstore.Launch.Job` from the run's own `launch.json`, backfilled from `result.json`'s execution when the CLI died before writing one — `existingLaunch` (`internal/cli/run.go`) already does exactly this backfill | `--retry`, and a follow-up (`--pr`) whose previous run has a launch record | One object read in the runs bucket, which the command already makes |
| 3 | The **forge**: `fugaro.yaml` at `spec.Ref` of `spec.Repo`, read with the installation's own App credential (§5) | A human session, no checkout of the repository anywhere, and the repository's provider is one a shared job serves (`infra.SharedJobProvider`, today `github`) | One Secret Manager read, one installation-token mint, one API call |
| — | **The refusal**, in the shape #1477 gives it, naming the cause and the way out (§7) | None of the three answers | — |

Two rules hold the order together.

**Source 1 is tried first because it is free and exact.** It answers for the checkout the operator
is standing in, at the same ref the checker reads, from the same objects — so a launcher and a
runner reading it cannot disagree. It also fixes a case today's code gets wrong for a reason that
has nothing to do with the console: `checkoutParse` returns `nil` both when there is no checkout
**and when there is one whose working tree has no `fugaro.yaml`** (its `readFugaroYAML` error path
leaves the result zero). An operator in a checkout of `X`, on a branch whose tree predates the file,
is told today (and by #1477) to "run it from `X`'s checkout" — which is where they are.

**Source 3 is the last resort and only where a shared job is possible at all.** For a repository
whose provider no shared job serves, there is nothing to learn: step 4 above keeps the
per-repository job for every Bitbucket repository whatever its `fugaro.yaml` says, and the
repository's provider is known without a checkout — `repoSlug` has already refused the launch if the
local config does not name it. So the launch without a checkout proceeds on the per-repository job
with **no read and no credential**, and that is not a guess: it is the same answer the checkout path
gives for the same repository. (Narrowing, D901.)

**There is no fourth source, and in particular there is no guessing `standard`.** The project layer
can carry a workflow's `checkout:` kind (`docs/project-layer.md`, the key's layers are `profile,
repo`) but **never a `job_profile`** — that key is `repo` only. So without the repository's own file
the launcher can know a workflow is a clone workflow and still not know which shared job it runs on,
and `standard` is exactly the wrong answer in the case that matters: a project publishes profiles
because its repositories name them.

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

Source 3 is the only part of this design that holds a provider credential in the launcher's process,
and it uses **the installation's existing GitHub App**, read the way the tree already reads it for
`--check-github-app` and `project sync --github-app-key`:

- **What it reads:** the App's private key from Secret Manager with the operator's own credentials
  (`gcp.Secrets.Access`), after a one-line notice to stderr, in memory only, never printed, logged,
  passed as an argument or put in an error — the rules `internal/cli/githubapp.go`'s header already
  states, reused rather than restated.
- **Which App:** `repos.<repo>.github_app_id` (the operator's local config) and the App key secret
  `config.GitHubAppKeySecret`, project-wide or per repository, exactly as `--check-github-app`
  resolves them.
- **Which token:** an installation token minted for `spec.Repo` with the **narrowest** permission
  set this read needs — `contents:read` and `metadata:read`, a new named set in
  `internal/gitprov/github` beside `RunTokenPermissions` and `CoordinatorTokenPermissions`. **Not**
  the run's own set, which is write-capable: a launcher reading one file must not hold a token that
  could push. The token is not cached anywhere beyond the call, and nothing else is fetched with it.
- **One request:** `GET /repos/{owner}/{repo}/contents/fugaro.yaml?ref={spec.Ref}`, through the
  replaceable `githubAPIURL`/`githubAPIClient` seams the package already has, so a test replays it
  (`internal/gitprov/httpfixture`).
- **Refused in a coding agent's session, before anything is read** — `refuseKeyReadInAgent`, with
  the launch named where the flag is named, so the operator reads the same rule in the same words and
  the value never reaches an agent's process. There, the launch refuses exactly as #1477 refuses it
  today, and an agent's way out is the right one: run it from the repository's checkout.
- **No new secret, no new stored token, no new principal, no new IAM binding.** The operator already
  needs `secretmanager.versions.access` on that secret to run `--check-github-app`; a console launch
  now uses it on a path that does not pass a flag (D902).

The rejected credentials, with their costs, are D902's alternatives: the credentials service (its
mint is bound to a `JobAccount` — `internal/credsvc` — so a launcher cannot be its caller, and
Bitbucket has nothing there), the operator's ambient forge credential (`gh`, a git credential
helper: Fugaro would read a person's credential out of another tool that may not exist, and
`docs/git-providers.md`'s "the CLI holds no provider credential" would stop being true in a new
way), and a new token the operator stores for launches (a new secret to create and rotate that
duplicates the App).

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
| No checkout anywhere, provider is one a shared job serves, **no permission** to read the App key secret | Refused. The message names the secret and the permission: `cannot read owner/repo-a's fugaro.yaml at main through github: ... ; the run's job is decided from that file, so this launch cannot be placed. Run it from owner/repo-a's checkout, or grant secretmanager.versions.access on <secret> and retry` |
| No checkout, the App is **not installed** on the repository (or the mint is refused) | Refused, cause in one line (`github.CheckInstallation`'s wording already names this), same two ways out |
| No checkout, **no network** / the forge answers 5xx | Refused, `remote(...)`'s wording for an unreachable service, same two ways out |
| No checkout, the ref does not exist at the forge | Refused, naming the ref: a deleted or never-pushed branch is not a reason to read another one |
| No checkout, the file **is there but the workflow is not**, or has no `checkout:` kind we can read | Unchanged from today: `resolveWorkflow`'s "cannot tell which workflow of `%s` to run; pass `--workflow`", and the per-repository job for anything but `clone` |
| No checkout, the file is **not in the repository at all** | The per-repository job. A document that is not there selects no profile and no clone workflow, and the project layer cannot select a profile for it (§2). The project layer's `checkout:` kind is read as always, so a clone workflow the *layer* defines still reaches a shared job |
| **A coding agent's session**, no checkout, profile published | Refused by `refuseKeyReadInAgent` before any read, naming the checkout as the way out |
| A **checkout exists** but its working tree has no `fugaro.yaml` | Source 1 answers: the ref's file is read from the checkout's objects, and the launch is decided as if the file were in the tree. If the ref's commit genuinely has no file, the row above applies |
| `--retry` / `--pr` of a run that has a launch record | The recorded job (§8, D907); no read |
| `--retry` / `--pr` of a run that never launched | Source 1, then source 3, as for any other launch |

`#1477`'s message stops being the one an operator sees for the last two rows, and stops claiming
"launched from outside `X`'s checkout" when the cause is a file the checkout does not carry. The
message is the design's, not a cosmetic afterthought: the two causes have two different remedies,
and the row above is the one place today's text is simply untrue.

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
  this design is not optional for the GUI — with #1477's refusal and no source 3, *every* console
  launch on an installation that has published a profile is refused, the repository the operator is
  looking at included.
- The five-field argv schema (`--repo`, `--title=`, `--plan-task=`, `--json`, `--task-file -`) is
  unchanged. The read happens inside the CLI, under the same rules as any other launch, and the
  console does not gain a control, a cached document or a new failure to render: it shows the CLI's
  message verbatim, as it does for every other refusal.
- The console's launches are at the **base branch** (`--ref` is on `NEVER_REACHABLE`), so the
  document read is the base branch's, and no branch can influence a console launch (§3).
- The console's child carries the operator's own Google identity already — it writes the task to the
  runs bucket and starts the execution — so the Secret Manager read is a permission the operator
  either has (they created the key through `fugaro setup`, which reads the same secret) or is told
  about in one line (§7). D906's rejected alternative is a sixth field in the launch contract and a
  launch-screen control, which buys nothing here: every console launch is from outside a checkout, so
  the control would be permanently on, and the console's release would have to ship before the GUI
  could launch anything at all on a migrated installation.

## 11. The effect on the refusal

#1477's refusal stays, verbatim in shape, for the launches that genuinely cannot be decided, and it
is **narrowed** in two places that need no credential: the provider no shared job serves (§2), and
the checkout whose object database has the file (§2, source 1). Everything else is answered by a
read the operator's own identity can make, or by the run's own record.

| Launch | `main` today | #1477 | This design |
| --- | --- | --- | --- |
| From `X`'s checkout, `fugaro.yaml` in the tree | decided from the tree | same | same |
| From `X`'s checkout, tree has no file, ref does | per-repository job (judged baked) | refused, message says "outside X's checkout" | decided from the checkout's objects at the ref |
| Anywhere, no checkout of `X`, Bitbucket, profiles published | per-repository job | refused | per-repository job — no shared Bitbucket job exists |
| Anywhere, no checkout of `X`, GitHub, profiles published, human session | per-repository job (the stale judgement `docs/checkout.md` records) | refused | decided by the forge read at the ref |
| The same, coding agent's session | per-repository job | refused | refused, `refuseKeyReadInAgent`'s wording added |
| Anywhere, no checkout, **no profiles published** | per-repository job | launch proceeds | unchanged |
| The console's launch of the repository it is showing | per-repository job (wrong) | refused (broken GUI) | decided by the forge read at the ref |
| `--retry` / `--pr` outside the checkout | per-repository job (wrong when the run was on a shared job) | refused | the run's recorded job |
| The read cannot be made (permission, network, no installation) | per-repository job | refused | refused, cause and both ways out named |

## 12. Threat model

**Assets.** The GitHub App's private key (an installation-wide credential: every repository the App
is installed on, read and write). The run's own credential. The published policy and its admission
lists. The budget.

**The read's authority.** It is made with the operator's own Google identity, on their machine, for a
repository their installation serves, and it carries the narrowest installation token GitHub can
mint (`contents:read`, `metadata:read`) — never the run's write-capable set, never the repository's
pushed credential. The key is in memory for the duration of the call and is never printed, logged,
put in an argument, put in an error, or written to disk; the token is not stored. In a coding agent's
session the read does not happen at all, so the key never enters an agent's process — the rule
`internal/cli/githubapp.go` states and `refuseKeyReadInAgent` enforces for the tree's two other
readers.

**What a hostile branch gains: nothing it does not have.** The document is read at `spec.Ref`, which
is the ref the run checks out and the ref the runner will re-read; the checkout path already places
runs by that ref's file. Every selection the file can express passes the policy's own gates (§4), and
the runner re-decides both axes at run start and halts rather than widening. The two things a branch
author cannot do through this path are worth stating: they cannot make a **console** launch read
their branch (it always reads the base branch), and they cannot select a profile their repository is
not admitted to.

**What the launcher gains.** Exactly one new power: it can read one file of a repository the
installation serves, where before it could read only a checkout's objects. The confused-deputy angle
is pinned to `spec.Repo` and `spec.Ref`: the token is minted *for that repository*, so a refusal to
install the App on it is a refusal to read it, and nothing in the request can name another
repository.

**What must never happen**, and is a test in the plan:

- The launcher never uses the run's own token set for this read.
- The launcher never fetches into a checkout: `checkoutFugaroYAMLAtRef` does not fetch today
  ("a fetch is a network write into a repository this command does not own"), and the forge read is
  an API call that does not touch the checkout either.
- The fetched document is never written to disk, and no document is reused across commands (§6).
- The launcher never falls back to the per-repository job when a read **fails**: a read that cannot
  be made — no permission, no network, no installation, a ref the forge does not have — refuses, in
  §7's first four rows. The per-repository job is the outcome in the two rows below them, and it is
  a decision rather than a fallback: there the document *was* read (or the ref's commit genuinely
  carries none, which selects nothing), and nothing in it selects a shared job.
- No credential value, token or key appears in an error, a log line or an audit line — the refusal
  names the secret, never its content.
- The run's recorded job is treated as data from the runs bucket, and its only effect is to name a
  job the runner then re-checks; a name the project did not deploy is refused at run start.
