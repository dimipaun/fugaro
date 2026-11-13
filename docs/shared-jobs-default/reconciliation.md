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

- **F1 (Wrong).** The spec says shared jobs are "disabled as untested". There is no such gate. The cause is `sharedJobProviders = []string{"github"}` in `internal/infra/projectjobs.go`, a limit set by the owner's 2026-10-15 ruling that a Bitbucket credential cannot be scoped to one project's repositories. The 2026-10-27 ruling D462 (workspace or project token) overtook it. "Remove the gate" is therefore a code change to the deployer, the launcher and the profile validation, not a switch.
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
- **F12 (Gap).** Nothing records that a shared job ran successfully for a project. The run record has `Outcome` and `Execution`, but the job name is not a field (U7). See D2303.
- **F13 (Rule).** CLAUDE.md's hard rule: `fugaro update` is the whole upgrade and is safe with runs in flight. Each new step needs a row in `docs/design/update-in-flight.md`, a test that a previous-version installation converges, and a mention in the in-flight line when it can reach a run. Retiring a job deletes the executions on it, which the line already names.
- **F14 (Rule).** The proxy applies only to installations with `proxy: true` and a runner at `proxySince` (0.22.0). The flip (B1.13 part two, #2166) is held on Check 191. A shared Bitbucket job holds no token, so it depends on the proxy and not on the flip.
- **F15 (Open).** A shared job holds one GitHub App; `projectjobs.go` refuses a project whose local config records two. For Bitbucket the equivalent is one workspace token per project, and a project spanning workspaces is unverified (U8).
- **F16 (Rule).** Overlapping plans: `docs/bitbucket-workspace-token` (T2.2, T2.4, T2.6, T2.7 wait on B5.1), `docs/secrets-brokering` (S5, B5.1–B5.9), `docs/project-image-catalog`, `docs/repo-image-removal` (parked, D1900). This plan cites their tasks and does not duplicate them.
- **F17 (Open).** A shared job holds no Claude OAuth token (`docs/design/project-centric.md` §4.1), and Check 191 was held because no Claude account was admitted to a per-repository Bitbucket job (#2549, #2550). A Bitbucket shared job's model access goes through the profile's `claude_accounts` and the cascade; the first live check has to prove it.
- **F18 (Gap).** The spec's list of what Terraform removes names the jobs only. The per-repository check job, build service account and registry belong to the same module and the same retirement. See T14.
- **F19 (Open, trust boundary).** A multi-repository build token is a single point of compromise for every repository it covers. The build runs the entry's own Dockerfile, so the token must never reach a `RUN` step: it is a BuildKit secret mount used only by the clone step, read-only, short-lived, minted per build for the repositories the entry bakes, and revoked or expired at the build's end. A hostile entry can still read what the clone step wrote into the image, which the scan (T10) refuses.
- **F20 (Gap).** The spec's "release blockers" name an end-to-end test on both providers. Hermetic tests use `gcpfake` and `gitprov/fake`; the live part is owner-run (Checks 191, 580 and 581).

## Decisions

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D2300 | Approve the capability map and the build order | Approve | **Approve (as recommended), 2026-11-13.** |
| D2301 | Overturn O3 (b): may one build bake several repositories with one narrowed token | Yes, via a credential-service build route, security-sensitive | **Yes (as recommended), 2026-11-13.** |
| D2302 | How is a Bitbucket shared job narrowed | Reuse the verified-identity pairing against the profile's `allow_repos` (#2545's model), no new token format | **Reuse the pairing (as recommended), 2026-11-13.** The spec's "Fugaro-issued token" is dropped: the job holds no token at all. |
| D2303 | Retire gate: when may `update` retire a repository's own jobs | A per-project record of a ready run on a shared job | **No gate: retire at update, 2026-11-13. Not the recommendation.** Followed through: `update` retires on its own, and the plan adds a structural precondition (the repository is admitted to a deployed profile, the project image resolves and the proxy is on) in place of a run record. The spec's "only after one success" is dropped. See Open items. |
| D2304 | What does `checkout: baked` mean once shared jobs are the only model | Baked runs on shared jobs: the checkout mode stops choosing the job model | **Baked runs on shared jobs (as recommended), 2026-11-13.** |
| D2305 | What default image does `update` create | A default catalog entry that is the pinned base image (`source: image`), nothing builds | **Catalog default on the base image (as recommended), 2026-11-13.** |
| D2306 | How do Bitbucket projects reach the proxy | `update` turns the proxy and credentials-service switches on | **`update` turns it on (as recommended), 2026-11-13.** |

Resolutions (one defensible answer, so not asked):

- **R1.** The default profile is `standard`, written to the local config's `profiles:` and published with the policy node by `update`, only when the project has none; an existing profile, edited or not, is never read for rewriting. It names the project image, `allow_repos` empty, and the run's repositories come from the task. `update` prints what it created.
- **R2.** The leak scan reuses `images/scan.sh --secrets` and extends it to the four places the spec names (git config, `.netrc`, credential helper store, environment variables); it runs as a step of the catalog build before the promote step.
- **R3.** The deprecation warning rides the existing update nag and `doctor` (no new mechanism): it names the shared-job replacement, once per repository still pointing at a per-repository job.
- **R4.** Submodule and LFS requests are classified by `gitproxy.Classify` as fetch-class and checked by the same `Decide`, so an uncovered repository is refused by the proxy with the repository's name; the runner rewrites submodule URLs and the LFS endpoint to the forwarder.
- **R5.** The Check 191 blocker stays the owner's: it is a prerequisite the plan lists, not a task an agent performs.

## Open tensions and open items

- **Open item 1 (confirm).** D2303 departs from the spec's own requirement that retirement wait for one successful shared run. The plan builds the structural precondition in its place; confirm that a failed first shared run, after the per-repository jobs are gone, may be repaired by re-running `fugaro update` rather than by falling back.
- **Open item 2.** D2306 moves existing per-repository Bitbucket jobs onto the proxy before the flip (B1.13 part two) that Check 191 gates. The plan keeps the job's own token mounted until the flip, as `docs/secrets-brokering` says; confirm the order.
- **Open item 3.** F15: confirm whether a project may span Bitbucket workspaces. The plan assumes one workspace per project.

## UNVERIFIED

- **U1.** That `pairShared` and the Bitbucket proxy route accept a shared job's identity. Settled by reading #2545's diff when it merges, or by T2.
- **U2.** Whether `gitproxy.Classify` accepts or refuses `/lfs/` paths, and whether the runner's `insteadOf` reaches a submodule URL. Settled by T11's red step.
- **U3.** Whether a baked checkout's `FetchBase` goes through the forwarder on a proxied run. Settled by T3's test.
- **U4.** The lifetime of the catalog build's GitHub installation token. Settled by reading `internal/cli/gitcred.go` in T8.
- **U5.** Whether the catalog build's `smoke` step includes `images/smoke-credentials.sh`. Settled in T10.
- **U6.** Whether the runner works unchanged on a Bitbucket shared job. Settled by T3 and T7.
- **U7.** The run record field that names the job. Not needed by this plan after D2303.
- **U8.** Cloud Run limits for the number of jobs and the 32 MiB push limit seen in Check 190 (a 413 over HTTP/1.1; h2c end to end is B1.5's). The owner can confirm.
