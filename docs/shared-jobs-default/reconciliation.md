# Reconciliation: project-level shared jobs as the default

Checked against `origin/main` at `a2e77dc3a` on 2026-11-13. Owner of the spec: the project owner
(the spec names none; it was handed over as an urgent request). **Where this document disagrees with
[spec.md](spec.md), this document wins.** Nothing here is implemented and no task is approved.

The spec was written without the repository in front of it. Most of what it asks for is partly built:
the shared project job, the Bitbucket proxy, the project image catalog and the disabling of repo-level
images all exist. What is missing is narrower than the spec reads: **Bitbucket is not admitted to a shared
job, `checkout: baked` still picks a per-repository job, the build holds one repository's credential, no
step creates a default profile, and nothing retires the per-repository jobs.**

## Capability map (approved, D2300)

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `bb-shared-jobs` | Bitbucket admitted to a shared job: deployer, launch, runner, credential-service pairing; baked workflows run on shared jobs; hermetic end-to-end tests and the owner-run live checks | the open B5.1 (#2545) and the existing proxy |
| `project-image-bake` | A project image bakes several repositories; the build's read token comes from the credential service; the built image is scanned for leftover credentials | `bb-shared-jobs` (the token path) |
| `git-nested` | Submodules and LFS through the proxy, with the "repository not covered" message | `bb-shared-jobs` |
| `default-profile-migrate` | `fugaro update` writes the default profile and the default catalog entry, turns the proxy on for Bitbucket projects, retires the per-repository jobs, warns on a leftover reference | `bb-shared-jobs`, `project-image-bake` |

Build order: `bb-shared-jobs` first, the smallest slice that gives a Bitbucket project a working job.

## Assumptions

- **A1.** "Project" is the Fugaro project (many repositories, one GCP, one Firebase), as in `docs/design/project-centric.md`.
- **A2.** "Profile" is `config.JobProfile`, selected by a workflow's `job_profile`; "shared job" is one Cloud Run job per (profile, account) deployed by `internal/infra/projectjobs.go`.
- **A3.** "Bitbucket proxy" is the credentials-service route `internal/credsvc/bitbucketproxy.go`, not a new component.
- **A4.** "Project image" is a catalog entry (`internal/config/catalog.go`), not a new image kind.
- **A5.** "Next release" is the release that follows the `bb-shared-jobs` slice; the release notes carry only "run `fugaro update`" (CLAUDE.md's hard rule).

## What the repository already has

| Spec concept | What the repository has |
| --- | --- |
| Shared project job | `infra.ProjectJobs`, `runner/projectjob.go`, `cli.launchProjectJobFor`; GitHub only |
| "Disabled as untested" | No switch: the hard-coded `sharedJobProviders` list behind `infra.SharedJobProvider` |
| Bitbucket proxy for git and REST | `credsvc/bitbucketproxy.go` (smart HTTP and REST), `gitproxy.Classify`, `Decide`, `CheckPRWrite` |
| "Fugaro-issued narrowed token" | None. `credsvc/slip.go` is a one-time delivery of named secrets. Narrowing today is the verified run identity paired to one repository |
| Shared job admission by profile | Open draft #2545 (B5.1), GitHub class: `pairShared`, `SharedJobAccounts` |
| Repo-level images disabled | `repoImagesDisabledNotice` and the commands in `internal/cli/image.go`; `catalog.JobImage` resolves the project image |
| Project image catalog and its build | `internal/backend/gcp/catalog.go` (`catalogBuildSteps`), `cli/image_build_catalog.go` |
| Fetch if baked, else clone | Implicit in `run.openCheckout`, `gitops.OpenOrClone`, `FetchBase`; baked layout `/work/baked/<slug>` |
| Leak scan | `images/scan.sh --secrets` (Trivy) and `images/smoke.sh`, for the base images only |
| Retiring an orphaned job | `cli/init_retire.go`, `infra/retire.go`; only for a changed `fugaro.yaml` |
| Bitbucket proxy verification | Check 191 (`scripts/live-checks/check191.sh`), not run |

## Findings

- **F1 (Wrong).** The spec says shared jobs are "disabled as untested". There is no such gate. The first limit is `sharedJobProviders = []string{"github"}` in `internal/infra/projectjobs.go`, set by the owner's 2026-10-15 ruling that a Bitbucket credential cannot be scoped to one project's repositories; the 2026-10-27 ruling D462 (workspace or project token) overtook it. It is not the only one: the service's `verifyJob` in `internal/credsvc/auth.go` refuses the shared class outright until C3.9 is on main (and #2545 keeps the Bitbucket route refused until B5.3), `credentialsServiceLaunchEnv` in `internal/cli/run.go` gives a job with a name no service URL (B5.2), and `(*run).proxiedBitbucket` in `internal/runner/project.go` is false for a project job that is not a review run. "Remove the gate" is therefore code in the deployer, the service, the launcher and the runner, not a switch.
- **F2 (Exists, and Wrong).** The proxy already carries clone, fetch and push as well as REST, and swaps the real token in. Decision 5's transport is built. The "Fugaro-issued tokens narrowed to the job's repos" are not: the narrowing is the verified identity paired to one repository, and `pairShared` (#2545) extends it to a profile's `allow_repos`. See D2302.
- **F3 (Gap).** Whether `pairShared` and the proxy route accept a Bitbucket shared job is unverified (U1). #2545 is a draft for the GitHub class.
- **F4 (Wrong).** "Terraform creates a job per repo" is true per workflow: `deploy/terraform/gcp/modules/repo/workflows.tf` has one job per workflow, and the repo module also holds a registry, a build service account, a check job and secret containers. Adding a repository today runs `newRepositoryStage` and a per-repository apply. Making "add a repo is a config change" true needs the repo module out of the default path, not just its jobs.
- **F5 (Exists).** Repo-level images are already disabled (`docs/image-setup-skill`, IS1 and IS2). Two things are not: `fugaro update`'s images stage still converges per-repository images, and a project with no catalog default falls back to the repository's derived image.
- **F6 (Rule).** The catalog build takes one repository's credential (`fugaro image git-credential`, a BuildKit secret mount) and not the credential service's, and the owner's O3 (b) limits a bake to the build's own repository. The spec's multi-repo bake overturns O3 (b). See D2301.
- **F7 (Exists).** Fetch-if-baked-else-clone exists in effect (`openCheckout`). It is implicit, and whether the baked fetch uses the proxy is unverified (U3).
- **F8 (Gap).** The leak scan covers base images and is not a step of the catalog build.
- **F9 (Gap).** No code routes submodules or LFS through the proxy. The runner's `insteadOf` for `https://bitbucket.org/` may cover nested URLs by accident (U2). The "repository not covered" message does not exist. `gitops` turns LFS filters off.
- **F10 (Gap, the largest).** `checkout: baked` is the default, and it selects the per-repository job: `launchProjectJobFor` takes a shared job only for `checkout: clone`. With repo-level images off, a Bitbucket project's default workflow has no job. The spec's "only active model" needs the checkout mode to stop choosing the job model. See D2304.
- **F11 (Gap).** No code writes a default profile. `buildProjectPolicy` refuses a profiles block that lacks `standard`, and a project with no `profiles:` deploys no coding job (only the implicit review profile).
- **F12 (Gap).** Nothing records that a shared job ran successfully for a project. The launch record has the job name (`Job` in `internal/runstore/launch.go`, which `continuedJob` reads) and the run record has `Outcome`, but no reader joins them per project. With D2303 no task needs one.
- **F13 (Rule).** CLAUDE.md's hard rule: `fugaro update` is the whole upgrade and is safe with runs in flight. Each new step needs a row in `docs/design/update-in-flight.md`, a test that a previous-version installation converges, and a mention in the in-flight line when it can reach a run. Retiring a job deletes the executions on it, which the line already names.
- **F14 (Rule).** The proxy applies only to installations with `proxy: true` and a runner at `proxySince` (0.22.0). The flip (B1.13 part two, #2166) is held on Check 191. A shared Bitbucket job holds no token, so it depends on the proxy and not on the flip.
- **F15 (Open).** A shared job holds one GitHub App; `projectjobs.go` refuses a project whose local config records two. For Bitbucket the equivalent is one workspace token per project, and a project spanning workspaces is unverified (U8).
- **F16 (Rule).** Overlapping plans: `docs/bitbucket-workspace-token` (T2.2, T2.4, T2.6, T2.7 wait on B5.1), `docs/secrets-brokering` (S5, B5.1–B5.9, answered and approved 2026-11-10), `docs/project-image-catalog`, `docs/repo-image-removal` (parked, D1900). **Two of this plan's tasks are theirs, re-scoped**: S2 is B5.3 (the Bitbucket route for a shared job) and S1 with S4 carry T2.2 (the deployer and the launcher for Bitbucket). The older rows are closed by whoever merges the task, and the task's pull request names both numbers. B5.2 (the service URL for a project job, the invoker grant and rendering the shared-job accounts) is a prerequisite and is listed as P1.
- **F17 (Open).** A shared job holds no Claude OAuth token (`docs/design/project-centric.md` §4.1), and Check 191 was held because no Claude account was admitted to a per-repository Bitbucket job (#2549, #2550). A Bitbucket shared job's model access goes through the profile's `claude_accounts` and the cascade; the first live check has to prove it.
- **F18 (Gap).** The spec's list of what Terraform removes names the jobs only; the whole repository root goes (D2312 as amended). The per-repository check job, build service account and registry belong to the same module and the same retirement. See M2 and M7.
- **F19 (Open, trust boundary).** A multi-repository build token is a single point of compromise for every repository it covers. As the bake works today the clone is a `RUN` in the entry's own last stage (`internal/imagebake/imagebake.go`), using the entry image's `git`, its `/etc/gitconfig` and `/usr/local/lib/fugaro/finalize-checkout`, all of which the entry's earlier `RUN` steps can replace, so the entry's code can read the secret mount. With one token over several repositories that is read access to all of them. The fence therefore moves: the clone runs in a **separate stage built from the pinned base digest with `GIT_CONFIG_NOSYSTEM=1`**, and its result is copied into the entry with `COPY --from`; the token is a BuildKit secret mount of that stage alone, read-only, short-lived and minted per build for the entry's repositories. **Review round 2 shows that is still not enough:** BuildKit hands a build's secret to every `RUN` that asks for it by id, and catalog entries are hand-written Dockerfiles that `internal/catalog/fetch.go` itself tells authors to write `RUN --mount=type=secret,id=git-credentials` in, so a secret mounted in the entry's build is readable by any entry step, whatever stage the clone is in. The fence is therefore a **separate build step whose Dockerfile Fugaro generates entirely** (from the pinned base digest, with `GIT_CONFIG_NOSYSTEM=1`): it alone holds the credential, and the entry's build carries no `--secret` and no identity; the checkout reaches it as a named build context. The scan (B3) looks for credentials, not for source the entry copied, so it is a second fence and not the first. Whether an entry may still carry a credential of its own (the `RUN --mount=type=secret` the catalog allows today) is the owner's call: open item 7.
- **F20 (Gap).** The spec's "release blockers" name an end-to-end test on both providers. Hermetic tests use `gcpfake` and `gitprov/fake`; the live part is owner-run (Checks 191, 580 and 581).
- **F21 (Wrong).** Decision 4 says a GitHub job gets a token narrowed to its repositories. Today a shared GitHub job mounts the App's private key (`github-app-key`, `internal/infra/projectjobs.go`) and mints for itself until secrets-brokering's B5.7 flip. The narrowing is the service's, and a shared job is narrowed only after that flip, which this plan does not do.
- **F22 (Rule, found in review).** `checkProjectCheckoutOrigin` in `internal/runner/projectjob.go` refuses any baked checkout on a project job, because hooks and URL rewrites in its `.git` would run with the profile's credential. D2304 cannot ship without a decision on how a baked checkout is trusted. See D2308.
- **F23 (Open, found in review).** An image that bakes several repositories lets a run for repository A read repository B's code with no credential under `/work/baked/`, the refusal `docs/image-setup-skill` F18 made and D2301 overturns. See D2309.

## Decisions

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D2300 | Approve the capability map and the build order | Approve | **Approve (as recommended), 2026-11-13.** |
| D2301 | Overturn O3 (b): may one build bake several repositories with one narrowed token | Yes, via a credential-service build route, security-sensitive | **Yes (as recommended), 2026-11-13.** |
| D2302 | How is a Bitbucket shared job narrowed | Reuse the verified-identity pairing against the profile's `allow_repos` (#2545's model), no new token format | **Reuse the pairing (as recommended), 2026-11-13.** The spec's "Fugaro-issued token" is dropped: the job holds no token at all. |
| D2303 | Retire gate: when may `update` retire a repository's own jobs | A per-project record of a ready run on a shared job | **No gate: retire at update, 2026-11-13. Not the recommendation.** Followed through: `update` retires on its own, and the plan adds a structural precondition (the repository is admitted to a deployed profile, the project image resolves and the proxy is on) in place of a run record. The spec's "only after one success" is dropped. See Open items. |
| D2304 | What does `checkout: baked` mean once shared jobs are the only model | Baked runs on shared jobs: the checkout mode stops choosing the job model | **Baked runs on shared jobs (as recommended), 2026-11-13.** |
| D2305 | What default image does `update` create | A default catalog entry that is the pinned base image (`source: image`), nothing builds | **Catalog default on the base image (as recommended), 2026-11-13.** |
| D2306 | How do Bitbucket projects reach the proxy | `update` turns the proxy on (`project_secrets.bitbucket-token.proxy`, with the credentials service `update` already deploys) | **`update` turns it on (as recommended), 2026-11-13.** |
| D2308 | How is a baked checkout trusted on a project job (F22) | Sanitise and verify: the clone is built in a separate stage, and the runner resets `.git` config and hooks to a fixed state and verifies the origin before any fetch | **Sanitise and verify (as recommended), 2026-11-13.** |
| D2309 | What happens to the other baked repositories during a run (F23) | The runner removes every baked directory but the run's own | **First answered "accept the read" (not the recommendation); amended later on 2026-11-13 by the owner to the recommendation: the runner removes the other baked directories before the agent starts.** Task S9. |
| D2310 | A project that mixes providers (open item 6): which profile does `update` write | A profile per provider, named by the provider, written when that provider has none | **A profile per provider (as recommended), 2026-11-13.** |
| D2311 | May a catalog entry keep a credential of its own (open item 7) | No: entry builds are credential-free | **No credential in entries (as recommended), 2026-11-13.** The catalog refuses an entry that mounts a secret (B9). |
| D2312 | Is "no job apply" enough for adding a repository (open item 8) | Yes, the root still applies its secrets and catalog check | **"No job apply is enough" (as recommended), 2026-11-13; amended later the same day by the owner: no repository-level Terraform at all.** After reading `secrets.tf` and `catalogcheck.tf`, the owner ruled the root goes: adding or removing a repository applies nothing, and the spec's "no Terraform re-run" stands as written. |
| D2313 | What becomes of the secrets a repository declares in `fugaro.yaml` (they were created by the repo root) | Became project secrets, adopted without destroying | **Amended the same day by the owner, 2026-11-13, "repository-level secrets are in a way similar to repository-level images": disabled for now, as the images are. A repository's `secrets:` is accepted and ignored with a warning; existing containers and their values are left alone; the code stays so the feature can return.** (The first answer, "become project secrets", was withdrawn.) M1. |
| D2314 | May one project span more than one Bitbucket workspace (F15) | One workspace per project | **One workspace per project (as recommended), 2026-11-13.** S6 refuses a second workspace, naming it. |

Resolutions (one defensible answer, so not asked):

- **R1.** The default profile is `standard`, written to the local config's `profiles:` and published with the policy node by `update`, only when the project has none; an existing profile, edited or not, is never read for rewriting. `config.JobProfile` has no image field: the image is the catalog default (D2305). The provider is the provider of the project's repositories, `claude_accounts` is left to the project's default account, and `allow_repos` is derived from the repositories the project enrols (the policy's own `Admits` and the node's derived `bitbucket_token.allow_repos` decide a call, not the empty list), with the run's repositories coming from the task. `update` prints what it created. A project that mixes GitHub and Bitbucket repositories gets a default profile per provider (D2310, R8).
- **R2.** The leak scan reuses `images/scan.sh --secrets` and extends it to the four places the spec names (git config, `.netrc`, credential helper store, environment variables); it runs as a step of the catalog build before the promote step.
- **R3.** The deprecation warning rides the existing update nag and `doctor` (no new mechanism): it names the shared-job replacement, once per repository still pointing at a per-repository job.
- **R4.** Submodule and LFS requests are classified by `gitproxy.Classify` as fetch-class and checked by the same `Decide`, so an uncovered repository is refused by the proxy with the repository's name; the runner rewrites submodule URLs and the LFS endpoint to the forwarder.
- **R5.** The Check 191 blocker stays the owner's: it is a prerequisite the plan lists, not a task an agent performs.
- **R6.** The Bitbucket half of the build credential (the review's finding that pairing returns no token): a Bitbucket build holds no token either. The build route answers a build identity with a **proxy grant** (the entry's repositories, paired to the identity) and the generated clone step's git talks to the proxy, as a job's does; the identity's ID token is available to that step only, never to the entry's build; a GitHub build gets a narrowed installation token. C2's response is therefore one of two shapes.
- **R8.** The provider-default profiles of D2310: `standard` stays the first provider's profile and another provider's is `standard-<provider>`; a workflow that names no profile resolves to the default of its repository's provider (S4), and publishing requires one default per provider in use instead of `standard` alone (S5).
- **R7.** The proxy forwards the one workspace token in the credentials bundle. A project that holds only per-repository access keys has none; `update` asks for it once, with the prompt `fugaro secrets` already uses, when the project's Bitbucket secret has none, and S6 carries the step.

## Open tensions and open items

- **Open item 1** is confirmed (2026-11-13): a failed first shared run is repaired by running `update` again and relaunching; there is no fallback to the old job.
- **Open item 2** is confirmed (2026-11-13): the proxy goes on before the flip, and the job keeps its own token mounted until B1.13 part two.
- **Open item 6** is answered: D2310.
- **Open item 7** is answered: D2311.
- **Open item 8** is answered: D2312, as amended (no repository-level Terraform).
- **Open item 3** is answered (2026-11-13), as recommended: one Bitbucket workspace per project; `update` refuses a second one naming it (S6). Recorded as D2314.
- **Open item 4** is answered (2026-11-13): D2309 is amended, the runner removes the other baked directories (S9).
- **Open item 5** is confirmed (2026-11-13): M6, M7, S10 and M2 launch only after Check 580 passes, and M2 retires a job only when no execution runs on it.

## UNVERIFIED

- **U1.** That `pairShared` and the Bitbucket proxy route accept a shared job's identity. Settled by reading #2545's diff when it merges, or by S2.
- **U2.** Whether `gitproxy.Classify` accepts or refuses `/lfs/` paths, and whether the runner's `insteadOf` reaches a submodule URL. Settled by N1's red step.
- **U3.** Whether a baked checkout's `FetchBase` goes through the forwarder on a proxied run. Settled by S3's test.
- **U4.** The lifetime of the catalog build's GitHub installation token. Settled by reading `internal/cli/gitcred.go` in B1.
- **U5.** Whether the catalog build's `smoke` step includes `images/smoke-credentials.sh`. Settled in B3.
- **U6.** Whether the runner works unchanged on a Bitbucket shared job. Settled by S3 and I1.
- **U8.** Whether `update` already rebuilds a project's catalog entries after a Fugaro release (the spec's per-project rebuild); and which other consumers use the repo module's secret containers and catalog check, which M1 leaves in place. Settled by reading `newImagesStage` in M6. (The repo module's secret containers and catalog check are no longer a question: repository-level secrets are disabled and their containers left alone, D2313, and the check acts as the project identity, B6.)
- **U7.** Cloud Run limits for the number of jobs and the 32 MiB push limit seen in Check 190 (a 413 over HTTP/1.1; h2c end to end is B1.5's). The owner can confirm.
