# Forge, authorship and images: reconciliation

**Reconciled against** `origin/main` at `c192e8766` (2026-10-28). **Where this file disagrees with
[spec.md](spec.md), this file wins.** Nothing here is implemented, and no task is approved.

**Numbers:** this work holds **D600–D679** and **Checks 230–249** ([README.md](README.md)). Findings
are F1…; resolutions R1…; open items O1…; unverified facts U1….

**Every decision row is answered (2026-10-29):** each is the recommendation except D618, which the
owner chose against it (T6 and O6, resolved 2026-10-29). Answers are not task approvals.

## 1. Summary

The spec reads greenfield. It is not. Of its five parts:

- **Commit authorship** is small and new: every run commit today is authored *and* committed by
  `Fugaro <fugaro@users.noreply.invalid>`, with no trailers. But the launcher identity it would write
  is a **self-declared string** today, and commits are made **by the agent's own `git`**, not by
  the runner alone. Both shape the design (F1–F6).
- **The forge layer** largely exists as `gitprov.Provider` (GitHub, Bitbucket, fake). What is new is
  GitLab (no line of it exists) and reading CI status. The spec's claim "adapters never store
  long-lived secrets in jobs" is the **end state** of the secrets-brokering plan, not today (F9–F14).
- **Multiple repositories in one image or one run** collides with the deepest invariant in the code:
  **one run, one repository**. The task, the run store's keys, the job and its service account, the
  image and registry, the git token, the gateway's budget claims, the in-job review, the PR flow, the
  digest and checkpoints are all per repository (F15–F18). Change sets are an architecture change.
- **Intent, wildcards, sync and markers** mostly **exist under other names**: the published policy's
  `allow_repos` with wildcards (`projectpolicy.MatchRepo`), `fugaro project repos add|rm|ls`, and
  `fugaro project sync`, which already expands the GitHub App's installed repositories. The repo
  pointer is the committed `fugaro.yaml`'s `gcp_project:`. The monorepo state branch conflicts with an
  owner ruling (D14 of 2026-10-15) and the design text beside it (not orphan) (F19–F27).
- **Developer onboarding** assumes a backend that does not exist. Developers are **GCP IAM members**
  today (the `launchers`/`operators` lists, applied by Terraform), Firebase admits **only custom
  tokens** signed by Fugaro's signer, and the CLI talks straight to GCP. "No IAM per developer"
  needs a hosted launch path first. **Token pooling already exists** as the credentials service's
  Tier A cascade, so the terms question the spec raises applies to code on `main` today (F28–F38).

The spec says "Open questions: none". This reconciliation finds **fifty** that need the owner
(§4), plus the ones only counsel or the owner can answer (§6).

## 2. Spec concept → what the repository already has

| Spec concept | What exists | Where | Verdict |
| --- | --- | --- | --- |
| Author = launcher, committer = bot | Author and committer both `Fugaro <fugaro@users.noreply.invalid>`, exported into the agent's env too | `internal/gitops/gitops.go` `Identity`, `IdentityEnv`; `internal/runner/runner.go` (the agent env loop over `gitops.Identity`) | New (small) |
| Trailers `Fugaro-Job/Rigor/Model` | None. Run ID, rigor and per-stage model are recorded | `runstore.Record.RunID`, `Record.Rigor`, `StageTiming.Model`; `task.NewRunID` | New |
| Never `Signed-off-by` | Nothing adds it today | `internal/gitops` (no `-s`, no trailer) | Exists (keep) |
| Launcher identity | `task.Spec.RequestedBy` → `runstore.Launch.LaunchedBy`, from `localcfg.Config.Me` (`user:` or `git config user.email`), self-declared; also the budget token's `rb` claim | `internal/localcfg/localcfg.go` `Me`; `internal/runstore/launch.go`; `internal/budget/token/token.go` `Claims.RB` | Exists, unverified |
| Forge interface (mint, commit/push, PR draft↔ready, comments, CI status) | `gitprov.Provider`: `EnsurePR`, `UpdatePR`, `ApplyReady`, `Comment`, `Comments`, `PullRequest`, `FetchPRHead`, `GitAuth`, `Repository`; `TokenSource`; commits and pushes are `gitops` (local `git`) | `internal/gitprov/gitprov.go`, `internal/gitops` | Exists, except CI status |
| GitHub, GitLab, Bitbucket | GitHub and Bitbucket (Cloud); `KindGitHub`, `KindBitbucket` only | `internal/gitprov/{github,bitbucket,fake}`, `providers.FromEnv` | GitLab new |
| Short-lived repo-scoped tokens from the security service | GitHub installation tokens minted by the credentials service when opted in; otherwise in the job | `internal/credsvc/github.go`, `internal/gitprov/github/apptoken.go` | Exists for GitHub |
| PR draft until senior review passes | Early draft after implement/fix; at finalize `Decide` makes it ready on a passing verified test on the final SHA and a senior `ship` verdict (or the rigor mode's findings policy) | `internal/runner/prflow.go` `afterStage`; `internal/runner/outcome.go` `Decide`; `runner.go` `seniorReview`; `runstore.TierSenior` | Exists |
| Signed commits | None | — | New |
| Multi-repo images | One derived image per (repository, workflow), in the repository's own registry, source baked at a pinned commit | `images/derived/Dockerfile.tmpl`, `images/derived/cloudbuild.yaml`, `internal/backend/gcp/build.go` `BuildRequest`, `names.go` `ImageName` | Per repo only |
| Fetch and fast-forward at start | The runner moves the baked checkout to the task's ref; `checkout: clone` workflows clone at start from the base image | `docs/checkout.md`, `internal/runner` | Exists |
| BuildKit secret for the build's git token | `--mount=type=secret,id=git-credentials`, written by `fugaro image git-credential` | `images/derived/Dockerfile.tmpl` (the mount), `images/derived/cloudbuild.yaml` (the credential step), `internal/cli/gitcred.go` | Exists |
| Content-hash tags, jobs pin digests | `candidate-$BUILD_ID` → smoke → `:latest` retag; jobs run `:latest` | `images/derived/cloudbuild.yaml` (tags), `internal/infra/spec.go` (`:latest`), `internal/imagecheck/record.go` | Differs |
| Project-level Dockerfiles | `.fugaro/<workflow>.Dockerfile` per repository workflow (`dockerfile:`) | `internal/config/config.go` (`Workflow.Dockerfile`), template header | Per repo only |
| Repo allow list with wildcards | `allow_repos` in the published policy, `MatchRepo` segment wildcards | `internal/projectpolicy/match.go`, `config/policy/shared` | Exists |
| Expand wildcards via the forge | `fugaro project sync` offers the GitHub App's installed repositories the allow-list does not admit | `internal/cli/project_enroll.go` | Exists (GitHub) |
| `fugaro repo sync` | `fugaro project repos add|rm|ls`, `fugaro project sync` | `internal/cli/project_enroll.go` | Exists (other name) |
| `fugaro.lock` | Nothing; nearest is the build record `builds/<slug>/<workflow>/image.json` | `internal/imagecheck/record.go` `Record` | New |
| Repo marker | The committed `fugaro.yaml`'s `gcp_project:` already points a checkout at its project (shared config fetch) | `docs/plans/2026-10-06-shared-config.md`, `internal/localcfg/shared.go` | Exists |
| Monorepo `.fugaro/` + `fugaro-state` orphan branch | `.fugaro/` holds recipes and Dockerfiles; the project backing folder is designed as `fugaro-project/` on branch `fugaro-project`, (D14, 2026-10-15), **not orphan** (§8's design text), not built | `docs/design/project-centric.md` §8 | Ruled otherwise |
| Developers in Firebase Auth, roles | Developers are IAM members via `launchers`/`operators`; RTDB rules admit custom tokens only; roles exist only in docs | `deploy/terraform/gcp/modules/firebase/iam.tf`, `internal/budget/rules/rules.go`, `internal/cli/init_firebase.go` `refusePublicSignUp`, `docs/mobile-auth/` | New, conflicts |
| `fugaro user`, `join`, `token` | None. `fugaro secrets`, `fugaro creds`, `fugaro accounts` exist | `internal/cli/secrets.go`, `creds.go`, `accounts.go` | New |
| Contributed Claude tokens, pool, failover | Named accounts `claude-oauth-token-<name>`; the cascade's Tier A pools subscription tokens with round robin and exhaustion marks | `docs/project-claude-accounts.md`, `internal/creds/select.go`, `internal/credsvc/cascade.go` | Exists |
| Secret prefix IAM condition `user-token-` | The service holds **one** unconditioned grant on the bundle; the prefix fence was explicitly replaced (Q6, 2026-10-19) | `deploy/terraform/gcp/modules/installation/credentials.tf` | Ruled otherwise |
| Members tab, mobile suspend | Console: one write, launch (D22); mobile: mailbox actions `cancel`, `merge`, `confirm` | `docs/design/desktop-console-v2.md`, `internal/mailbox/message.go` `Actions` | New writes |
| Seat licence issued by the CLI | Key-minting service planned under `ee/`, deferred; D523: no licence notice for now | `docs/ee-reorg/` | Deferred |

## 3. Findings

### Commit authorship

**F1, Gap (the hard one).** The agent commits with its own `git`. The runner puts `gitops.Identity`
into the agent's environment (`internal/runner/runner.go`, the loop over `gitops.Identity`) and
commits only leftovers and its own steps (`Repo.CommitAll` in `gate.go`, `digest.go`, finalize).
Fugaro has **no agent sandbox** (docs/secrets-brokering §1), so the agent can set any author, any
trailer or none, and while it holds a git credential in a stage it can push itself. *Consequence:*
author and trailers set by the environment are a **default, not a guarantee**. Nothing the runner does afterwards can make them one: rewriting breaks verification and
checkpoints (F2), and the agent can push the run branch itself with its token (the runner's `Push`
expects it), so a rewrite of unpushed commits is bypassed by pushing first. *Amended in review round
3:* commits are **born stamped** (the agent's environment carries the author; a runner-owned
`prepare-commit-msg` hook, set through environment configuration and chained to the repository's own
hooks, adds the trailers), nothing is rewritten, and the trailers are stated as a record, not proof
(D603). Earlier rounds planned a rewrite (with `OwnCommits`, a stamped-ID list and a checkpoint
gate); the round-3 review showed each piece defeated by in-stage verification or an agent push.

**F2, Gap.** Readiness is tied to commit IDs. `Decide(records, finalSHA, …)`
(`internal/runner/outcome.go`, `latestVerifiedTest`) matches the verified test record's SHA exactly,
and **the agent verifies inside its own stage** ("Commit first, then verify",
`internal/runner/prompts.go`). Mid-stage checkpoints (on unless `git.pr.checkpoints` is false) push
while the agent works and never commit or touch the working tree or the index
(`internal/runner/checkpoint.go`). *Consequence (amended in review round 3):* any rewrite after a
commit is made leaves the run unverified or diverges from what was pushed, so the stamp must happen
**when the commit is made** (D603, D640).

**F3, Rule.** The launcher identity is self-declared. `localcfg.Config.Me` answers the local
config's `user:`, else `git config user.email`; `docs/coordinator/reconciliation.md` #11 calls it "a
self-declared, unauthenticated string"; `internal/cli/roles.go` says "the identity a run recorded is
untrusted". The budget token's `rb` claim is signed by the launcher's own minting and carries the
same string. *Consequence:* "author = launcher" written from today's data is **forgeable by any
launcher**. That is acceptable under D475 (launchers are trusted, 2026-10-27) only if it is called a
claim. The spec's "signed job token minted at launch with the launcher's identity" does not exist and
cannot be built on a value the launcher chooses: an authenticated identity needs Google's own
assertion (the launcher's OAuth identity, verified by a service) (D604, U1).

**F4, Gap.** Launches with no human: `fugaro run` from CI, the coordinator (no launch path today,
but planned), a follow-up run, a review or artifact run (which never push), a recipe. *Consequence:*
the author falls back to the Fugaro identity when the launcher is empty or not a person (D605).

**F5, Gap (privacy).** An author email is personal data written into a **permanent, widely
replicated** history. The project export design already refuses to write the operator's identity into
a repository others read (`docs/design/project-centric.md` §8: author and committer "fixed, never the
operator's own"). Erasure under GDPR or PIPEDA cannot remove it from forks and clones. *Consequence:*
the owner and counsel decide (D606); the plan supports a per-person noreply override.

**F6, Exists / Gap.** `Fugaro-Job:` names a **run** in this repository. A *job* here is the Cloud
Run job, one per repository and workflow (`gcp.JobName(slug, workflow)`, `gcp.WorkflowVariant`) or
one per profile and account for shared jobs (`docs/design/project-centric.md` §4.1); the per-execution
ID is the run ID (`task.ValidRunID`, `^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`). The model is recorded per
stage as the **configured pin** (`StageTiming.Model`), not the served model; a commit's stage is
knowable only if the stamp runs per stage (F2). Rigor is `Record.Rigor.Mode`. (D601, D602)

**F7, Exists.** `Signed-off-by` is never added today. The spec's rule stands (R1).

**F8, Gap.** The seat audit trail. The spec says the author doubles as a seat record. Seats are
"anyone who launches jobs, by any means" (D433, 2026-10-27; `docs/licensing/subscription-terms.md`
§5.1), and a seat moves at most once in any 30 days (§5.3, B14 of 2026-10-27). A self-declared author
(F3) is no audit trail, and the commit carries it only when a run pushes. Launch records are no
better as they stand: `launch.json` is "untrusted" (`internal/runstore/launch.go`), writable by
launchers and by the job, and `runs/` is deleted after `runs_days` (default 90, the bucket's
lifecycle rule). *Consequence:* the author is not a seat record; how seats are counted stays in
D433's domain (R2).

### Forge

**F9, Exists.** The interface is `gitprov.Provider` (above). Commits and pushes are local `git`
through `gitops` (`CommitAll`, `Push` with `--force-with-lease`, checkpoint pushes in
`internal/runner/checkpoint.go`), not a forge API. Token minting is `GitAuth`/`TokenSource` and, with
the service on, `github.ServiceTokens`. *Consequence:* the spec's five-method interface is not
adopted; its items map onto the existing methods, plus one new read (F10). (D611)

**F10, Gap.** No code reads CI checks or pipeline status: the runner runs the repository's tests
itself (`internal/runner/gate.go` `verifyOnce`). A change set's "ready together" and a coordinator
merge block would need it. The GitHub App's permissions are Contents RW, Pull requests RW, Issues R,
Metadata R (`docs/git-providers.md`); reading checks needs **Checks: read** and **Commit statuses:
read**, writing a blocking status needs **write**. Every App permission change must be accepted by
each installation's owner. (D615)

**F11, Gap.** GitLab is absent. `KindGitHub`/`KindBitbucket` are switched on in config validation,
`providers.FromEnv`, the image build's credential step (`images/derived/cloudbuild.yaml`, Bitbucket
and GitHub forms only), the secret names and Terraform, the credentials service's mint, the
schemas and the plugin skills. A third forge is an L-sized, security-sensitive change touching all of
them, plus GitLab's own semantics (merge requests, `Draft:` title prefix, project and group access
tokens). (D610)

**F12, Wrong.** "Adapters never store long-lived secrets in jobs" is not today. GitHub jobs still
mount the App private key (`config.ServiceHeldSecretNames` is empty; the flip, PR #907, waits on
Check 96). Bitbucket jobs mount the long-lived token and the agent receives it as `FUGARO_GIT_TOKEN`
(D462 accepted the reach; F7 of `docs/bitbucket-workspace-token/reconciliation.md`). The Bitbucket
proxy that removes it is S1 of secrets-brokering, **0.14.0** (D490), and the token leaves the job
only after its own flip. *Consequence:* the spec's sentence is the end state of D490–D500; this plan
adds no new long-lived job secret (Global Constraint 3).

**F13, Rule.** "Cloud-hosted forges only" agrees with D366 (Bitbucket Data Center out, 2026-10-26)
but D474 (2026-10-27) admits an **owner-allow-listed** Bitbucket API root for self-managed use. The
two are reconciled by "supported = cloud; an allow-listed root is accepted, unsupported" (D612).

**F40, Gap (review round 1).** Open PR #1264 ("runner: a github run's commits carry the App's bot
identity") changes, for GitHub only, author **and** committer to the App's `[bot]` noreply identity,
resolved per run when the provider opens, and its brief asks to stay compatible with this spec.
*Consequence:* this plan never hard-codes `Fugaro <fugaro@users.noreply.invalid>`; it uses the run's
identity as `main` resolves it per run when A1 starts (D645).

**F14, Gap (signing).** Today nothing is signed. The spec's three per-forge paths:
(a) GitHub **API commits** would replace `gitops`' commit-and-push and the checkpoint pushes: a
rewrite of the push path for one forge, and whether an App-created commit with a *user* author shows
Verified is UNVERIFIED (U2). (b) GitLab's API signing is UNVERIFIED (U3). (c) "The security service
holds a per-project key and signs **in the job**": a key in the job contradicts the spec's own
"never store long-lived secrets in jobs"; a remote signer (git's `gpg.ssh.program` pointing at a
loopback that asks the service to sign) keeps the key out but signs **whatever the agent commits**,
since there is no sandbox. Branch protection that requires signatures usually guards the default
branch, which humans merge into. (D608, D609)

### Senior review and the PR lifecycle

**F15, Exists.** "Draft until senior review passes" is the runner's finalize rule today (`Decide`
with `seniorReview`; `runstore.TierSenior`; the first tier never decides readiness). The independent
review this repository uses on its **own** PRs (local agents outside the cloud loop, the owner's
process) is not the product and is not this rule. A change set needs a **hold**: no single run may
flip its PR ready alone (D614). **The hold binds the runner, not the agent** (review round 1): the
agent holds a token with `pull_requests: write` and can mark its PR ready or edit its body (sibling
links, merge order). The settle step must re-read every PR's state rather than trust it, and a
follow-up run (`fugaro run --pr N`) on a member PR must inherit the hold.

**F41, Gap (review round 1).** Self-approval. The PR is opened by the bot, so a launcher may approve
a PR whose commits now name them as author; the forge's "not your own PR" rule does not see it.
Authorship does not create this (the launcher can approve the bot's PR today) but it is now visible
in blame with no second person. (D643)

### Multiple repositories

**F16, Rule (architecture).** One run, one repository: `task.Spec.Repo` (one string); run state at
`runs/<repo-slug>/<run-id>/`; one Cloud Run job and service account per repository and workflow (or
per profile for shared jobs, still one repository per run); the derived image and its registry per
repository; the GitHub installation token for **one** repository (`apptoken.go`,
`"repositories": []string{s.repo}`); the credentials service's `JobAccountsFor` and D496's proxy
rule "the task's repository" per repository; checkpoints, early drafts, digest, in-job review and
`Decide` per repository. *Consequence:* "one job whose changes span several repos" is a redesign of
the runner and the trust model, not a feature (D613).

**F17, Gap.** Read-only context is the cheaper half. A run that may **read** sibling repositories
needs a read token for them (a GitHub installation token can name several repositories; a Bitbucket
workspace or project token reaches them already, D461) and a clone step; it writes nothing new. It
widens what one run's agent can read (and, with no sandbox, exfiltrate) to every context repository.
(D613, D617) **The service cannot read the task** (review round 2): it holds "no project role, no
bucket" (`credentials.tf`), and `task.json` sits under the job's own writable prefix, so the context
list the service trusts must come from a **launcher-minted claim** in the run identity, as D497a binds
the run's branch, never from the task file or the caller's request.

**F46, Gap (review round 2).** Squash merges. This repository squash-merges, and the squash commit
on `main` is authored by the PR's opener (the App's bot), not by the branch's commit authors. Whether
the launcher-as-author and the trailers survive into `main`'s blame on a squash merge is UNVERIFIED
(U9); the spec's "shows up in blame" may hold only for merge commits and rebases. (D648)

**F18, Rule.** The coordinator is advisory: "It never blocks or reorders merges"
(`docs/coordinator/spec.md`), and "Fugaro never merges" (`docs/design/v1.md`), with one qualification:
the mobile mailbox's `merge` action has the consumer run `gh pr merge` on a person's command
(`internal/mailbox/message.go`), with no knowledge of a merge order. The coordinator's GitHub token
is `pull_requests: read` (`coordinatorTokenPermissions`, Q3(A)), and a writing coordinator was refused
(Q10 C, `internal/gitprov/github/apptoken.go`): a settle step that flips PRs ready reverses that
(D641). Its collision model
is per repository (`internal/coordinator/view.go` `View.Repo`). It moves under `ee/` (ee-reorg D530).
The spec's "coordinator enforces merge order and blocks a consumer merge" reverses the advisory
ruling and needs a forge write (F10). (D615, D616)

### Images, intent and lock

**F19, Exists.** The repo allow list is the policy's `allow_repos`, published by
`fugaro project policy publish`, edited by `fugaro project repos add|rm`, matched by
`projectpolicy.MatchRepo` (segment wildcards like `github/acme/*`; bare `acme/*` refused), and
**enforced by the runner fail-closed**. It already is the security boundary the spec asks for.
`fugaro project sync` expands through the GitHub App's installed repositories, with a confirmation.
*Consequence:* no `fugaro repo` family; extend `fugaro project sync` (D623).

**F20, Rule.** `fugaro.yaml` is **the repository's** config and "the only place for repo-specific
behaviour" (`CLAUDE.md`); it decodes strictly against `schemas/fugaro.schema.json`. Making it a
**project** intent file in a project repository changes its meaning and duplicates the published
policy and the project layer (`fugaro/project-layer.yaml`, `fugaro/config.yaml` in the runs bucket).
(D617)

**F21, Gap.** `allow_data_to` is a different boundary: per **credential** (which repositories a model
provider, a Claude account or a project secret may serve; D464's one-segment wildcard). The spec's
"allow list" is per **project membership**. Both stay; a repository the policy admits but a
credential's `allow_data_to` does not still cannot use that credential (R4).

**F22, Differs.** Tags and pins: builds push `candidate-$BUILD_ID`, smoke-test it by digest, retag
`:latest`; jobs run `:latest` (`internal/infra/spec.go`), and "a job runs the repository's :latest,
which no launch compares with ImageDigest" (`internal/imagecheck/record.go`). Pinning a digest per
build means changing the job definition per build: "Cloud Run cannot vary the image, service
account, CPU or memory per execution" (`docs/design/project-centric.md` §4.1). (D619)

**F23, Differs.** Layer order: the template clones the source **before** `mise install`, the
warm-up and `image.setup`, because they read the repository's files (lockfiles, `mise.toml`). "Source
last" needs the lockfiles extracted at the pinned commit into an earlier layer. Real gain, own task
(D620).

**F24, Gap (cost).** Rebuilds today are trigger-based: a daily image check that rebuilds on lockfile
change, base change, or 14-day age (`config.Rebuild` defaults, `internal/cli/imagecheck.go`). A
nightly rebuild of an image carrying 50+ repositories is a Cloud Build cost **outside the run
budget** (the budget meters runs, not builds). (D621)

**F25, Gap (security).** A project image with N repositories is readable by anyone with pull rights
on its registry, so one reader reads every baked repository. Today each repository's image is in its
own registry. SECURITY.md has no statement about baked source in registries. (Task C4.3)

**F26, Gap.** A generator command that prints the repo list runs code. Where it runs decides who it
can hurt. On the operator's machine it is their own risk; in a job or Cloud Build it runs with
installation credentials. Intersecting its output with `allow_repos` is the existing check (D622).

**F27, Rule.** The monorepo state branch: the spec's orphan `fugaro-state` branch departs from the
owner's D14 (2026-10-15, `docs/design/project-centric.md` §8: folder `fugaro-project/`, branch
`fugaro-project`, PR mode opt-in) and from the same section's design text that the branch is
"bootstrapped from the default branch, not created orphan"; phase 6, not built. The spec's ".fugaro/ holds the config" also overloads `.fugaro/`, which holds recipes and
Dockerfiles. (D626)

### Developer onboarding

**F28, Wrong.** "Developers are never GCP/Firebase IAM members": today they are. `launchers` and
`operators` become IAM members in Terraform (`modules/firebase/iam.tf` `local.people`: RTDB and
Firestore viewer, the signer's token-minter role; `modules/compute/iam.tf` `launcher_jobs_editor`),
and `fugaro run` starts the Cloud Run job **with the launcher's own credentials**. *Consequence:*
"no IAM per developer" needs a **hosted launch path** that starts jobs on developers' behalf. That is
the central orchestrator's scale (`docs/central-orchestrator/`), not an onboarding command (D627).

**F29, Rule.** Firebase admits only custom tokens signed by Fugaro's signer
(`refusePublicSignUp`: "only custom tokens signed by Fugaro's signer may work"); the rules check
custom claims (`internal/budget/rules/rules.go` `userClaim`, `pairingClaim`); the mobile-auth plan
recommends that no provider session reaches the database (D221, plan only). Google sign-in as
"Firebase Auth end users" changes that boundary (D628).

**F30, Rule.** Roles viewer/operator/admin: D446 (2026-10-27) "a separate design first; enforced
system-wide, the cloud included". The spec's command table is that design's output, not its
substitute. Launcher and operator exist in code as IAM lists (`internal/cli/roles.go`,
`fugaro setup --launcher/--operator`); viewer, operator and admin as a role model exist in docs only
(`docs/mobile-auth/spec.md`).

**F44, Gap (review round 2).** A role claim minted by "Fugaro's signer" is forgeable by every
launcher today: every launcher and operator holds the signer's `token_minter` role
(`deploy/terraform/gcp/modules/firebase/iam.tf`, `local.people`). A role claim needs a minter the
people being ranked cannot call. (D628, E0)

**F45, Gap (review round 2).** Teammate onboarding already exists in two forms: a cloud command in a
checkout whose `fugaro.yaml` names `gcp_project:` fetches the shared config with no setup, and
`fugaro update --gcp-project` writes a new machine's config from the installation's outputs and names
the roles and whom to ask (`internal/cli/init_new_machine.go`). `fugaro join` adds little beyond
`fugaro update-skills` (E3 withdrawn).

**F31, Gap.** "Console and CLI call the same backend API": there is no backend API; the CLI calls
GCP and Firebase directly and the console spawns the CLI. (D627)

**F32, Rule.** Suspended users "don't count as seats" lets a customer suspend and resume to rotate a
seat faster than §5.3's once in 30 days, which already allows a move at any time "when the person
leaves the Customer" (D629).

**F42, Gap (review round 1).** `fugaro user` over the IAM lists has two traps. `localcfg`'s
`memberRE` accepts `domain:`, so the spec's `--domain` would grant a whole domain the launcher roles,
the signer's token-minter role included (`budget_admins` already refuses `domain:`). And the lists
live in each operator's local config (`Terraform` is not in the shared config), so one admin's stale
list plus `fugaro update` silently revokes members another admin added. (E1)

**F43, Gap (review round 1).** `localcfg.Config.Shared()` copies every field and blanks named ones
(`internal/localcfg/shared.go`). A new local-config field (a commit address, a resolver command) is
published to the runs bucket, which a launcher can write, unless it is blanked there.

**F33, Deferred.** "The CLI issues the seat licence automatically": the key-minting service is
deferred under `ee/` and D523 forbids any licence notice for now (D630).

**F34, Exists / Rule.** Contributed tokens: `fugaro secrets set claude-oauth-token-<name>
--project-wide` (stdin or hidden prompt only, never argv) and `claude_accounts.<name>` with
`allow_data_to` exist (`docs/project-claude-accounts.md`), and they are **operator** acts: a
teammate's machine "holds the same file but not the rights". A named account is selectable by any
launcher of an admitted repository (`fugaro run --token`, `agent.oauth_token`): it is scoped by
repository, not by person, so **"Own" is not expressible today**. The credentials service reads **one**
bundle secret through **one unconditioned Secret Manager grant** (it also holds its RTDB role), and the comment on it records that the prefix
fence was replaced (Q6, 2026-10-19; `TestCredentialsServiceGrantsAreTheOneBundle`). The service
has **no write route** (`/v1/messages`, `/v1/messages/count_tokens`,
`/v1/github/installation-token`). *Consequence:* the spec's `user-token-` prefix with create and
add-version rights reverses Q6 and adds the service's first write path (D631).

**F35, Gap.** The CLI never runs `claude setup-token`; it tells the user to run it and paste
(`internal/cli/initsecrets.go`). Running it from the CLI means parsing its output (format
UNVERIFIED, U5) and holding the token in the CLI process (D632).

**F36, Exists (and a legal flag).** Pooling exists: the cascade's Tier A uses shared subscription
OAuth tokens, round robin (`creds.PathRoundRobin`), exhaustion marks in RTDB `creds/`; Check 90 found
Tier A viable (2026-10-21). The spec's terms question ("whether pooling one person's Claude
subscription for others' jobs fits Anthropic's terms") **applies to this existing code**, not only to
the new pool. Stated neutrally: it is the owner's and counsel's to confirm (O2).

**F37, UNVERIFIED (U6).** A reserve threshold "from response headers on subscription tokens": no code
or doc in the repository shows such headers; how Claude Code obtains its quota display is unknown
here. Nothing may be built on it until verified (D634).

**F38, Rule.** The Members tab is a console write surface beyond D22 (launch only) and D34 ("not
yet" for cancel/retry). Mobile "suspend" is a new action beside `cancel`, `merge`, `confirm`, under
D113 (mailbox writes only) (D635, D636).

### Licence boundary

**F39, Rule.** ee-reorg (2026-10-29, D520–D536): governance, the cost report and the coordinator go
under `ee/`; core keeps wire and mechanism (D521); untagged builds stub `ee` commands (D522); no
licence notices (D523). The go-to-market spec lists "SSO and roles" as `ee/`. The secrets-brokering
proxy is MIT (its R4). Nothing rules on authorship, forge adapters, user management, token pooling
or change sets (D637).

## 4. Decisions (D600–D649), for the owner

Each row: the question, the recommendation and its reason, the alternative and its cost, and the
owner's answer (all answered 2026-10-29: every one is the recommendation except D618). D640–D645 were added in review round 1, D646–D648 in round 2 and D649 in round 3; D603, D604, D616, D640, D642, D644 and D646 were re-recommended in round 3. D650–D679 are reserved and unused.

| # | Question | Recommendation | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D600** | The first slice | **Slice A alone first** (authorship and trailers): small, MIT, no new trust boundary, useful on its own; everything else after its own go | Start with the forge layer or images: larger, and no part of them needs A | **2026-10-29:** Slice A alone first. Why: smallest useful slice, no new trust boundary. |
| **D601** | Trailer names (F6) | **`Fugaro-Run:`** (the run ID), `Fugaro-Rigor:`, `Fugaro-Model:` | The spec's `Fugaro-Job:`: names the Cloud Run job, which is per repository, not per run, and confuses every doc that uses "job" | **2026-10-29:** `Fugaro-Run`. Why: names the run, not the per-repository job. |
| **D602** | What `Fugaro-Model:` holds (F6) | **The configured model of the stage that made the commit**, documented as the pin, not the served model; omitted for runner-only commits and when the stage pins no model (`StageTiming.Model` is empty) | Omit the trailer until the served model is recorded: loses the link the spec wants | **2026-10-29:** The stage's configured model (the pin). Why: honest about what is recorded; omitted when none. |
| **D603** | Who stamps author and trailers (F1, F2) | **Amended in review round 3: the commit is born stamped.** The agent's environment carries the author, and a runner-owned `prepare-commit-msg` hook (set through environment configuration, chained to the repository's own hooks, not suppressed by `--no-verify`) adds the trailers; the runner's own commits use the same environment; nothing is rewritten; finalize logs how many run commits lack `Fugaro-Run`. (Rounds 1–2 recommended a rewrite after each stage; it breaks in-stage verification and checkpoints, and an agent push bypasses it) | A rewrite after each stage: leaves runs unverified (F2). A `commit-msg` hook: `--no-verify` skips it | **2026-10-29:** Stamped as made, never rewritten. Why: a rewrite breaks in-stage verification and is bypassed by an agent push. |
| **D604** | Whose identity is the author (F3) | **The recorded launcher (`requested_by`) as a stated claim**, with the commit address limited to the launcher's own (D649); an authenticated identity replaces it when Slice E exists. D475 (launchers trusted) covers a launcher reaching a run's credential, not writing a colleague's name into history, so the claim's limits are this row's own | Wait for a verified identity before shipping A: delays the smallest useful slice by the whole of E | **2026-10-29:** The launcher, as a stated claim. Why: ships A without waiting for a verified identity. |
| **D605** | Author with no person (F4): CI, coordinator, follow-ups, bots | **The Fugaro identity, as today**, when `requested_by` is empty or not an email | Use a service-account address as author: noise in blame, and no person is accountable | **2026-10-29:** The Fugaro identity when no person launched. Why: no one is accountable for a bot's run. |
| **D606** | Author email and privacy (F5) | **Opt-in per repository** (a `git.author: launcher` key in `fugaro.yaml`, default `fugaro` = today's author; no policy version bump, so it does not collide with the Bitbucket S2 policy bump of D477/D493), and each person may set a commit address (for example a forge noreply address) in their local config; owner and counsel confirm the default | On by default, as the spec says: personal emails written irreversibly into histories others read; reverses the project-export precedent. Or a project-policy setting: a policy version bump, a lockstep upgrade | **2026-10-29:** Opt-in per repository through `git.author`. Why: personal emails in shared history are irreversible. |
| **D607** | API commits for GitHub's Verified badge (F14) | **Keep local `git` commits and `git push`** on every forge; no API commits now | GitHub API commits: rewrites the push and checkpoint path for one forge; whether a user-authored App commit shows Verified is unverified (U2) | **2026-10-29:** No API commits; keep local `git` and `git push`. Why: avoids rewriting the push path for one forge. |
| **D608** | Commit signing (F14) | **Defer all signing** until a customer repository requires signed commits on the branches runs push; then design a remote signer through the credentials service (no key in the job) | Build the spec's per-project SSH key now: a new long-lived key class and a signer that signs whatever the agent commits | **2026-10-29:** No signing now; later one service-held key per project and forge. Why: no key in the job, and no customer needs it yet. |
| **D609** | If signing is built, whose key | **One key per project per forge, held by the credentials service, registered to the bot**, as the spec says | Per-person keys: per-developer key management, which the spec avoids | **2026-10-29:** One key per project per forge, held by the credentials service. Why: avoids per-developer key management. |
| **D610** | GitLab (F11) | **Not in 0.14.** First the forge-kind cleanup (B1); GitLab after a named need, with its own reconciliation of tokens and draft semantics | Build GitLab now: an L-sized, security-sensitive change across config, infra, builds, the credentials service and the skills | **2026-10-29:** No GitLab in 0.14. Why: it needs a named need and its own reconciliation first. |
| **D611** | The forge interface (F9) | **Keep `gitprov.Provider`**; add only a CI-status read when change sets need it; minting stays in `TokenSource` and the service | Adopt the spec's five-method interface: renames working code for no capability | **2026-10-29:** Keep `gitprov.Provider`. Why: renaming working code adds no capability. |
| **D612** | Cloud-only forges (F13) | **Supported = cloud forges; D474's owner-allow-listed Bitbucket root stays accepted, unsupported** | Refuse allow-listed roots: reverses D474 | **2026-10-29:** Cloud GitHub/Bitbucket supported; an allow-listed Bitbucket root accepted but unsupported. Why: keeps D474. |
| **D613** | Multi-repo model (F16, F17) | **Staged:** (1) read-only **context repositories** in a run; (2) **change sets as linked runs**, one run per writable repository, sharing a change-set ID; one run writing several repositories is **declined for now** | The spec's one run with several writable worktrees: redesigns task, run store, jobs, tokens, gateway claims, review, PR flow, digest and checkpoints | **2026-10-29:** Read-only context repositories, then linked runs. Why: one run with several writable worktrees redesigns too much. |
| **D614** | "All PRs ready together" (F15) | **Runs of a change set keep their PR draft**; a change-set step flips all ready when every run's senior verdict is `ship` and its tests passed. A coordination aid for honest runs, not a security control (T5): each member's agent can write its record and flip its PR | Each PR flips on its own, linked only: simpler, loses the spec's guarantee | **2026-10-29:** Draft hold plus settle, as a coordination aid only. Why: gives the spec's guarantee for honest runs without claiming security. |
| **D615** | Merge order enforcement (F10, F18) | **Advisory**: the order is written in each PR and shown by the coordinator; no blocking | A blocking status check: new App permission (Commit statuses or Checks write) every installation must accept, and reverses "never blocks or reorders merges" | **2026-10-29:** Merge order advisory. Why: a blocking check needs a new App permission and reverses the coordinator's ruling. |
| **D616** | Where change sets live (licence) | **`ee/` behaviour on core mechanism** (amended in review rounds 2 and 3): the task fields, the hold and the status line are core mechanism (ee-reorg D521: core keeps wire and mechanism); the settle step and ordering are under `ee/`; `--change-set` is refused by an untagged build (the paid-command stub pattern of D522, applied to a flag) and by every build until the settle step ships, so no change set is stuck in draft | Linking and hold usable in core, settle in `ee/`: a core build's change sets never leave draft. All in core: puts coordinator behaviour outside `ee/`. The cost of the recommendation: an untagged build has no change sets at all | **2026-10-29:** `ee/` behaviour on a core mechanism. Why: core keeps wire and mechanism; the settle step is paid. |
| **D617** | Where the project's intent lives (F19, F20) | **The published policy** (`allow_repos`, `fugaro project repos`, `fugaro project sync`) as the intent; a generated lock stored in the runs bucket beside the build records | The spec's project Git repository with `fugaro.yaml` as intent and `fugaro.lock`: changes `fugaro.yaml`'s meaning, duplicates the policy, adds a repository every project must have | **2026-10-29:** The policy is the intent; the lock lives in the bucket. Why: no second source of truth and no new repository. |
| **D618** | Per-project images (spec) vs per-repository (today) | **Keep per-(repository, workflow) images**; a project image is designed later as an opt-in, after context repositories (D613 step 1) show a need | Replace per-repository images: migrates every installation, the registry layout, job definitions and `fugaro image refresh` | **2026-10-29:** NOT THE RECOMMENDATION: shared images across repos, scoped (on being asked) to the recommended scope: an image is shared only between repos with an identical resolved dependency set, and a change to one forks it automatically. Why: one build serves many repos at no isolation cost. Follow-through: plan C7 and C8, T6, O6. **2026-10-29 (T6 resolved):** a divergence is never forked automatically: `fugaro image refresh` and `project sync` propose fork, adopt or stay pinned and wait (propose-then-confirm, as D625); the shared image is never mutated; the bucket lock is the one source. |
| **D619** | Content-hash tags and digest pins (F22) | **Record and check the digest at launch (warn on mismatch)**; no pinning, since Cloud Run cannot vary the image per execution | Pin by updating the job definition per build: an infra apply per build | **2026-10-29:** Warn on digest mismatch. Why: Cloud Run cannot vary the image per execution, so no pinning. |
| **D620** | Layer order (F23) | **Yes, as its own task**: lockfiles and tool files extracted at the pinned commit before the warm-up, source last | Keep today's order: every source change re-runs the warm-up | **2026-10-29:** Lockfiles before source. Why: a source change should not re-run the warm-up. |
| **D621** | Rebuild cadence (F24) | **Keep trigger-based rebuilds** (lockfiles, base, age); no blanket nightly rebuild; state that builds are outside the run budget | Nightly rebuild plus on main moving: Cloud Build cost per repository per night, unmetered | **2026-10-29:** Trigger-based rebuilds. Why: a blanket nightly rebuild costs per repository and is unmetered. |
| **D622** | The generator command (F26) | **Only on the operator's machine**, inside `fugaro project sync`; its output intersected with `allow_repos`, dropped entries warned | Run it in a cloud job: runs arbitrary code with installation credentials | **2026-10-29:** The resolver runs only on the operator's machine. Why: a cloud job would run arbitrary code with installation credentials. |
| **D623** | `fugaro repo sync` (F19) | **Extend `fugaro project sync`** (Bitbucket workspace listing, `--check`), no new command family | A new `fugaro repo` family: two commands for one allow-list | **2026-10-29:** Extend `fugaro project sync`. Why: one command for one allow-list. |
| **D624** | Repo markers | **The committed `fugaro.yaml`'s `gcp_project:` is the marker** (exists; a marker-only file validates only with `git.provider` and a project layer that supplies workflows, U8); the require-marker setting is deferred | Build require-marker now: an extra fetch per repository on every sync | **2026-10-29:** `gcp_project:` is the marker. Why: it already exists; require-marker adds a fetch per repository. |
| **D625** | Auto-add on sync | **Propose, then confirm** (today's `project sync`); a setup run for a failing repository only on an explicit flag | Fully automatic add, rebuild and setup run: spends budget and opens PRs unattended | **2026-10-29:** Propose, then confirm. Why: automatic add spends budget and opens PRs unattended. |
| **D626** | Monorepo state branch (F27) | **Keep D14**: `fugaro-project/` on branch `fugaro-project`, bootstrapped from the default branch (project-centric phase 6) | The spec's orphan `fugaro-state`: reverses an owner ruling | **2026-10-29:** Keep D14 (bucket, not an orphan branch). Why: reverses no owner ruling. |
| **D627** | Developer onboarding without IAM (F28, F31) | **Design first (D446)**; meanwhile `fugaro user` is a front for the existing `launchers`/`operators` lists (each change is an owner-run `fugaro update`) | Build Firebase Auth users and a hosted launch API now: the central orchestrator's scale, new trust boundary | **2026-10-29:** Roles design first; `fugaro user` over the IAM lists meanwhile. Why: hosted onboarding is the central orchestrator's scale. |
| **D628** | Sign-in shape (F29) | **Google sign-in exchanged for a custom token minted by a service the people being ranked cannot call** (today every launcher holds the signer's minter role, F44), role as a claim; public sign-up stays refused | Enable Firebase's Google provider directly: reverses `refusePublicSignUp` and mobile-auth D221's recommendation | **2026-10-29:** A minter the ranked people cannot call. Why: today every launcher holds the minter role; public sign-up stays refused. |
| **D629** | Suspension and seats (F32) | **A suspended person keeps the seat until the 30-day move rule allows moving it**; counsel words it | Suspended users free their seat at once: seat rotation by suspend/resume | **2026-10-29:** Suspension does not free a seat early. Why: avoids seat rotation by suspend and resume. |
| **D630** | Seat licence on join (F33) | **No licence step in `fugaro join`** until the key-minting service ships (D523) | Issue keys now: needs the deferred `ee/` service | **2026-10-29:** No licence in join. Why: the key-minting service is deferred (D523). |
| **D631** | Where contributed tokens live (F34) | **Bundle entries**, written through a new authenticated service route once E's identity exists. That route **changes Q6's shape**: the service then needs a version-adder role on the bundle beside its accessor, and contributors' writes are a read-modify-write of the bundle that also holds admin entries; E0 designs both; until then an operator stores a teammate's token with today's `fugaro secrets set` or `fugaro creds set`, as now | The spec's `user-token-` prefix with create rights: reverses Q6 and gives the service create rights in Secret Manager | **2026-10-29:** Bundle entries inside the one secret. Owner confirmed; this amends Q6's shape (the service gains a version-adder role). Why: avoids create rights in Secret Manager. |
| **D632** | Does `fugaro token add` run `claude setup-token` (F35) | **No**: the person runs it and pipes the token to stdin, as `secrets set` does | The CLI runs it and captures the output: output format unverified (U5), token in the CLI's memory and scrollback | **2026-10-29:** The CLI never runs setup-token. Why: output format is unverified and the token would sit in CLI memory. |
| **D633** | Token policy default (F36) | **Own** for personally contributed tokens, which needs a per-person binding that does not exist (F34: accounts are scoped by repository), so it is built from E0; pooling stays where it exists (Tier A) pending O2 | Pool by default: the terms question unresolved | **2026-10-29:** Own, which needs a per-person binding. Why: the pool-by-default terms question is unresolved. |
| **D634** | Reserve thresholds (F37) | **Not built until U6 is verified**; exhaustion marks remain the mechanism | Build on assumed headers: may read nothing, or the wrong thing | **2026-10-29:** No reserves until verified. Why: do not build on unverified headers (U6). |
| **D635** | Console Members tab (F38) | **Read-only list first**; every write waits for E's identity and a separate go | The spec's tab with writes: moves the console's fence past D22/D34 | **2026-10-29:** Members tab read-only first. Why: writes wait for E's identity. |
| **D636** | Mobile suspend (F38) | **Deferred until `fugaro user suspend` exists**; then a mailbox action with confirmation | Build now: an action with nothing behind it | **2026-10-29:** Mobile suspend deferred. Why: nothing stands behind the action yet. |
| **D637** | Licence placement (F39) | **MIT:** authorship, forge adapters (GitLab included), context repositories, `fugaro user` over the IAM lists, token storage. **`ee/`:** change sets (D616), SSO and domain or workspace invites, roles enforcement beyond the lists | Roles and user management in core: conflicts with go-to-market's `ee/` list | **2026-10-29:** Follow the reconciliation's licence split. Why: matches go-to-market's `ee/` list. |
| **D638** | Workspace-level invites | **Wait for the workspaces spec**, not in the repository | Build project-by-project invites now and fold later: rework | **2026-10-29:** Wait for the workspaces spec. Why: avoids rework folding project invites later. |
| **D639** | Spec's "Open questions: none" | **Replace with §6 of this file** | — | **2026-10-29:** List the open items in the plan; none blocks slice A. Why: the questions are tracked in §6. |
| **D640** | Mid-stage checkpoint pushes (F2) | **Unchanged** (amended in review round 3): with commits born stamped, checkpoints push stamped commits and need no gate. (Round 2 recommended a read-only author gate; it is withdrawn with the rewrite) | Turn checkpoints off when stamping: loses the crash protection checkpoints exist for | **2026-10-29:** Checkpoints unchanged. Why: commits are born stamped, so no gate is needed. |
| **D641** | The settle step needs PR write (F18) | **A separate, settle-only token** (`pull_requests: write`) minted for the settle step, beside the coordinator's read-only one; reverses Q10 C for this one act | Each member run's finalize flips its siblings: every run then needs write on other repositories | **2026-10-29:** A settle-only write token. Why: member runs then need no write on other repositories. |
| **D642** | Who may join a change set | **Membership fixed at launch**: one launch creates every member run and writes the full member list into each member's task; the settle step acts only when every member lists the same set. A task file is writable by launchers and by runs of the same repository (SECURITY.md), so a hostile member can stall a set but not widen it unseen | A free-form `--change-set ID` any launcher passes: anyone can join or stall someone else's set | **2026-10-29:** Membership fixed at launch. Why: a hostile member can stall a set but not widen it. |
| **D643** | Self-approval (F41) | **Document it**; no code. Repositories that need two people use their forge's review rules | A runner-side refusal: Fugaro cannot see who approves | **2026-10-29:** Document that self-approval is possible. Why: Fugaro cannot see who approves; forge rules handle it. |
| **D644** | An agent-written `Signed-off-by` (R1) | **Leave it** (amended in review round 3): with nothing rewritten, the hook sees only the message being made; removing a line there would also strip a real person's sign-off carried by a cherry-pick. The docs say Fugaro never adds one | Remove it in the hook: strips cherry-picked human sign-offs too | **2026-10-29:** Leave an existing `Signed-off-by`. Why: removing it would strip a cherry-picked human sign-off. |
| **D645** | The committer identity (F40) | **The run's identity as `main` has it** (`Fugaro`, or the App's `[bot]` identity on GitHub if #1264 merges); the spec's "committer = the forge bot" is then met on GitHub | Hold this plan until #1264 settles: delays A for no gain | **2026-10-29:** The run's identity as main has it (#1264's bot identity, after its fix round). Why: meets the committer-is-the-bot goal without delaying A. |
| **D646** | A commit whose author is someone else (a cherry-pick; an agent's `--author`) | **Keep the author git gives it** (amended in review round 3): nothing is rewritten; `Fugaro-Run` links the commit to its run and launcher. (Round 2 recommended re-authoring with a `Fugaro-Original-Author:` trailer; it needs a rewrite and writes personal data by default) | Re-author at commit time in the hook: impossible for a cherry-pick's author, which git sets before the hook runs (U10) | **2026-10-29:** Keep the author git gives. Why: nothing is rewritten; `Fugaro-Run` links the commit to its run. |
| **D647** | Trailers by default (F6) | **On for every run commit** (no personal data: run ID, rigor, model pin); stated in the release notes' Highlights | Opt-in with the author: no link from commits to runs by default | **2026-10-29:** Trailers on by default. Why: they carry no personal data. |
| **D648** | Squash merges (F46) | **Measure first** (Check 230 adds a squash merge) before any further work; if the launcher and trailers do not survive, the PR body carries them for the squash message | Assume they survive: the spec's "shows up in blame" may silently fail on squash-merging repositories | **2026-10-29:** Check 230 verifies trailers survive a squash merge. Why: the blame claim may fail silently on squash-merging repos. |
| **D649** | Which commit address a launcher may set | **Only their own**: `commit_email` must equal the launcher's recorded identity or be a forge noreply address; anything else is refused at launch | Free-form: one launcher can attribute commits to a colleague, and the forge links them to that person's account | **2026-10-29:** Limited to the launcher's own address or the Fugaro identity. Why: one launcher must not attribute commits to a colleague. |

**Resolutions** (one defensible answer each):

- **R1.** `Signed-off-by` is never added automatically (F7). D644 recommends leaving any that is
  already in a message.
- **R2.** The commit author is not a seat record (F8). This plan counts nothing; seat counting is
  D433's and the subscriptions plan's.
- **R3.** No new long-lived secret enters a job in any slice of this plan (F12).
- **R4.** `allow_data_to` stays the per-credential boundary; `allow_repos` the membership boundary
  (F21).

## 5. Tensions between the spec's parts

- **T1. "Author = launcher" against "the author comes from a signed job token".** A cannot have
  both until E: D604 ships the claim first.
- **T2. "Never long-lived secrets in jobs" against "signs in the job" (Bitbucket).** D608 defers;
  any signer is remote.
- **T3. "Allow list is the security boundary" against "a buggy generator can never widen access".**
  True only if the generator never writes the policy: D622 keeps it read-only and local.
- **T4. "Coordinator enforces merge order" against the coordinator's advisory ruling and the `ee/`
  move.** D615, D616.
- **T5. "All PRs ready together" against no sandbox (review round 2).** Every member's record and
  PR can be written by that member's agent (SECURITY.md: runs of one repository share a service
  account and its prefixes; the agent can read the job's token). The ready-together step is a
  coordination aid for honest runs, as a single run's readiness is today, not a security control;
  human review stays the gate (D614).
- **T6. Shared images (D618, not the recommendation) against Cloud Run's per-job image and the
  other image rules.** (a) Cloud Run cannot vary the image per execution (U4), so each repository's
  job still points at a shared digest: every build that changes a repository's hash updates that
  repository's job to the new digest (an infra apply or job update per build, which D619 rejected
  as a per-build cost for pinning; here it is the price of sharing). (b) Interplay: D619 warns on a
  digest mismatch, so the build record is keyed by the dependency-set hash and the warning compares
  the job's digest with the lock's digest for that hash; D621's trigger-based rebuilds fire per
  hash, not per repository, and a trigger in one repository rebuilds the image its siblings use;
  per-repository base images and the derived images Cloud Build makes per repository must be
  reconciled with one image used by several (which repository's source is baked in, given that
  today's derived image bakes one repository's source at its commit, SECURITY.md registry note
  C6). (c) Blast radius: when two repositories share a build and one changes its dependencies, the
  hash changes and that repository forks to a new image; the shared image is never mutated in place,
  so the other repository is untouched. The owner is asked to confirm all three (O6).

  **Resolved 2026-10-29 (original text above kept).**
  - **(a) Job digest:** as recommended: each repository's job points at the shared digest and the job
    is updated per build, an operator-machine step. The image-count question ("how do we make sure we
    do not explode the number of images on trivial differences?") is answered by three controls the
    owner accepted: (1) a **normalised hash key**: only resolved package names, versions, toolchain
    and base image count; file order, comments, formatting, the repository name and build or test
    commands are ignored; (2) **superset reuse**: before building, use an existing image that already
    contains the repository's whole dependency set at the same versions, even with extras (the cost:
    a wider supply-chain surface in that image than the repository needs); (3) an **image-count cap
    warning** in `doctor` and `fugaro image ls`, and automatic clean-up of images no job has used for
    N days (proposed N = 30; the owner confirms the value). The owner did not choose "dev-only
    dependencies create no image".
  - **(a), refined 2026-10-31 (D618 as answered):** control (1)'s package input is the resolved
    package's name, its version **and the integrity or checksum its key file states for that
    version's artifact** — two repositories that resolved one version to two artifacts are two
    dependency sets, since the image is built from the artifact — and a package a key file resolves
    from a source that is not the ecosystem's registry (a git, file or path source, an npm alias, a
    Bundler `GIT` block, a composer `dev-` branch, …) leaves the repository **unkeyed** rather than
    keyed by the versions beside it. §2.3 and §2.4 of the design hold the rule and its per-ecosystem
    shapes; C8a's reduction (`internal/imagecheck/reduce.go`) is built to it. **Review round 2
    (2026-10-31)** narrowed two things further: the fact's three fields are each written as its own
    quoted literal with the third always present, so two different triples can never be one fact (the
    canonical form's `key v2`), and a package a file states **no** integrity for is keyed only when
    the file also says it came from the ecosystem's default registry — a private registry is fine
    beside an integrity, not as the only thing an entry has. The four inputs and the controls are
    otherwise as resolved above.
  - **(b) Interplay:** as recommended: the bucket lock (hash to digest) is the one source. D619 warns
    on a digest mismatch against the lock; D621 rebuilds only when an input hash changes; derived
    images are built from the shared base for that hash.
  - **(c) Blast radius: NOT THE RECOMMENDATION.** The recommendation was an automatic fork. The owner
    chose a fork under user control: when a repository's hash diverges from its shared image,
    `fugaro image refresh` and `project sync` propose and wait (propose-then-confirm, the shape of
    D625): fork to a new image, adopt another repository's compatible image, or stay pinned to the
    old image with a warning. Nothing is built silently, and a shared image is never mutated in
    place.

## 6. Open items for the owner

- **O1. Every decision D600–D649** is answered (2026-10-29). Every answer is the recommendation
  except D618, which the owner chose against it (T6, O6). Answers are not task approvals.
- **O2. Anthropic's terms** for (a) the existing Tier A pooling and (b) contributed personal tokens
  used for other people's jobs: the owner's and counsel's (F36). Not a technical question.
- **O3. Privacy:** whether launcher emails in commits need a notice or consent (F5): owner and
  counsel.
- **O4. Seat wording** for suspension (D629): counsel.
- **O5.** The roles design (D446) is a prerequisite of Slice E's writes; nobody owns it yet.
- **O6. Confirm T6** (shared images, D618): the job-update-per-build cost, the interplay with D619,
  D621 and the derived images, and fork-never-mutate. **Resolved 2026-10-29:** (a) and (b) as
  recommended with the three image-count controls; (c) a fork under user control, not the
  recommendation (see T6). Still open: the unused-image retention N (proposed 30 days).

## UNVERIFIED

- **U1.** Whether a service can obtain the launcher's identity authenticated by Google at launch
  (for example an ID token sent with the launch, verified by the credentials service) without a
  hosted launch API. Not checked.
- **U2.** Whether a GitHub App's API-created commit with a user author shows Verified.
- **U3.** GitLab.com and self-managed signing of API and web commits.
- **U4.** *Resolved in review round 1: no.* `docs/design/project-centric.md` §4.1: Cloud Run cannot
  vary the image per execution. Check 233 is withdrawn and stays reserved.
- **U5.** `claude setup-token`'s output format and whether it is stable for parsing.
- **U6.** Whether Claude Code reads subscription usage windows from response headers, and which
  ones. Do not build on it.
- **U7.** Whether a GitHub App installation token naming several repositories keeps per-repository
  permission narrowing as needed for read-only context (Check 232).
- **U8.** *Partly resolved in review rounds 1 and 2.* `schemas/fugaro.schema.json` requires only
  `version` and `project`; `fugaro validate` on a `version`/`project`/`gcp_project` file reports only
  `git.provider` missing where the project layer supplies workflows. So a near-marker validates with
  `git.provider` added and a project layer; without a layer it has no workflow (D624).
- **U9.** Whether GitHub's and Bitbucket's squash merge keep the branch commits' authors (for example
  as `Co-authored-by:`) and their trailers in the squash message (Check 230's squash step, D648).
- **U10.** Whether `GIT_AUTHOR_*` in the environment overrides a cherry-pick's original author
  (believed not: cherry-pick keeps the original unless `--reset-author`; not checked here), and that
  `prepare-commit-msg` runs for every commit the agent makes, `--no-verify` included (githooks(5)
  says it is not suppressed; A1's `TestStampHookRunsUnderNoVerify` pins it).
