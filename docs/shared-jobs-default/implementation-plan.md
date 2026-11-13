# Implementation plan: project-level shared jobs as the default

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (decisions D2300–D2313, findings F1–F23).
**Nothing is implemented, and no task is approved: each needs the owner's explicit go.** The answers that shaped the plan:
shared jobs take Bitbucket by pairing and not by a new token (D2302); a build may bake several repositories with a
credential-service token (D2301, which supersedes O3 (b)); `checkout: baked` runs on shared jobs (D2304); `update`
writes a default profile and a base-image catalog entry (R1, D2305), turns the proxy on (D2306) and retires the
per-repository jobs with no run gate (D2303, not the recommendation); a baked checkout is sanitised and verified (D2308) and a run may read the project's other baked repositories (D2309, not the recommendation).

The decoupling pass is **on** (`fugaro settings planning.decoupling` reads `true`, the default). Mode: `fugaro settings
rigor.default` was not read for each path; every task below that touches credentials, a write guard, Terraform or IAM is
flagged opus with an independent review, and the contracts over credentials get the strictest rigor.

## Prerequisites outside this plan

- **P1.** B5.1 (#2545) and B5.2 (`docs/secrets-brokering`) merged: the GitHub shared-job class in the credential service, and the service URL, the invoker grant and the rendered shared-job accounts for a project job (`credentialsServiceLaunchEnv`). S2 extends B5.1's `pairShared`; I1 cannot pass without both.
- **P2.** Check 191 (owner-run, `scripts/live-checks/check191.sh`) passed, with #2549 and #2550 landed. The spec's release blocker; **no task starts it and no agent performs it**.
- **S2 is `docs/secrets-brokering`'s B5.3, and S1 with S4 carry `docs/bitbucket-workspace-token`'s T2.2**: the pull request of each names both numbers, and whoever merges it closes the older row in `docs/status.md`. T2.7 and the workspace token's other tasks stay theirs.
- **Launch gates (owner-run results, so not graph edges)**: S6 launches only after Check 191 passes (P2); M6, M7 and M2 launch only after Check 580 passes, because with no retire gate (D2303) that check is the live proof before a per-repository job is deleted. The graph draws no edge from a provider to its contract (the schema refuses one), so S1, S2 and S5 launch after C1 merges and B1 and B8 after C2 merges.

## Conventions

- **Always**: tests before the commit; targeted `go test ./<pkg>/ -run '<names>' -count=1` (CLAUDE.md); `gofmt -l .` empty; a doc change runs `go test ./internal/cli/ -run Docs`; a task's pull request names its `T#` and `D#`.
- **Ask first**: a schema or wire-format change (a field is added, never retyped: CLAUDE.md "a field never changes meaning"); a new dependency; any `.github/workflows` change (stage it under `docs/pending-workflows/`, never edit it).
- **Never**: a secret-shaped literal in a test, doc or fixture; a live cloud action by an agent; a TODO comment; a token or credential file in an image layer; deleting the repo-bound or repo-level image code (spec: out of scope).
- Hermetic tests only: `internal/gcpfake`, `internal/gitprov` fake, `internal/testutil`, `t.TempDir()`.
- **Hard rule** (CLAUDE.md): every `fugaro update` step is idempotent, safe with runs in flight, has a row in `docs/design/update-in-flight.md` and a test that an installation at the previous version converges. The release notes' `### For operators` is "run `fugaro update`" and what it asks.
- Reviewer tier **opus + independent review** for: S2, S9, B1, B2, B5, C1, C2, N1, M1, M6, M7, M2, S6, B3, I1, I2 (credentials, the proxy, Terraform, IAM, the runner's baked-checkout guard). Standard for the rest.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A shared Bitbucket job mounts no token; the runner's git, REST, submodule and LFS traffic all reach the forwarder | S3, N2, I1 |
| A baked checkout on a project job is reset to a fixed `.git` state and its origin verified before any fetch; the clone is built in a stage the entry cannot alter | S9, B2 |
| Every other caller class is refused on the build route, and the build identity is refused on every other route | B1 |
| A run on a narrow profile can read the project's other baked repositories (D2309, stated, not removed) | S7, I2 |
| A call for a repository the policy does not admit is refused by the service, naming the repository | S2, N1 |
| The entry's own build step carries no `--secret` and no identity; only the generated clone step holds the credential | B2 |
| The build token is read-only, short-lived, covers only the entry's repositories and reaches no `RUN` step | B1, B2, B3 |
| No token or credential file survives in any image layer: git config, `.netrc`, credential helper store, environment | B3 |
| `update` never rewrites an existing profile; a second run changes nothing | S5 |
| `update` with a run in flight leaves it running; the retirement deletes only an orphaned job and is named in the in-flight line | M2 |
| Adding or removing a repository applies no repository-level Terraform at all | M7 |
| A repository's `secrets:` is accepted and ignored with a warning; no run mounts it | M1, M7 |
| A retired root's secret containers and their values survive | M2 |
| A project with no catalog default gets a base-image default, so a job always has an image | S5 |

## Slice 1: `bb-shared-jobs` (the urgent slice)

### C1: the shared-job contract
- Acceptance: `internal/sharedjob` declares the types and signatures both sides call: `Pairing` (run identity, profile, repository → allowed or refused with the repository named), `JobChoice` (repository and workflow → profile or none) and the Bitbucket profile's fields; `contract.md` says what each side may rely on; the stubs return the shape and do nothing else.
- Files: `internal/sharedjob/sharedjob.go` (new), `internal/sharedjob/stub.go` (new), `docs/shared-jobs-default/contract.md` (new).
- Interfaces: produces `sharedjob.Pairing` and `sharedjob.JobChoice`; consumes the existing `projectpolicy` profile types.
- Tests: `TestStubsReturnTheContractShape`.
- Command: `go test ./internal/sharedjob/ -count=1`
- Red step: the test fails to compile until the types exist.
- Depends on: none.
- Commit: `sharedjob: the contract a Bitbucket shared job is built against (C1)`.
- Reviewer tier: opus, independent review (`fugaro review --pr N`) before S1–S4 launch.
- Owner-run: no.

### S1: the deployer admits Bitbucket profiles
- Acceptance: `ProjectJobs` can deploy a job for a valid Bitbucket profile with no Bitbucket secret mounted and the proxy required, and a profile that mounts the Bitbucket secret stays refused; **`sharedJobProviders` itself is left as it is**, so S1 to S4 merge with no effect on a Bitbucket project and a release cut midway breaks nothing; I1 makes the one-line change.
- Files: `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`, `internal/projectpolicy/profiles.go`, `internal/projectpolicy/profiles_test.go`.
- Interfaces: consumes C1's profile fields; produces a Bitbucket job in the installation root.
- Tests: `TestProjectJobsDeployABitbucketProfile` (with the provider admitted in the test), `TestBitbucketProfileMountsNoToken`, `TestSharedJobProvidersAdmitsBitbucketAfterI1`.
- Command: `go test ./internal/infra/ ./internal/projectpolicy/ -run 'ProjectJobs|BitbucketProfile|SharedJobProviders' -count=1`
- Red step: `TestProjectJobsDeployABitbucketProfile` fails because the provider list admits GitHub alone.
- Depends on: none in the graph (S1 is the provider side of C1's broken edge S4 → S1); it is built against `contract.md` and launches after C1 merges. It carries T2.2's deployer half.
- Commit: `infra: a shared project job serves a Bitbucket profile (S1, T2.2, D2302)`.
- Reviewer tier: standard.
- Owner-run: no.

### S2: the proxy pairs a shared Bitbucket job
- Acceptance: the shared class is no longer refused outright for the Bitbucket route (`verifyJob`); a call on it by a shared job's verified identity is paired to the run's repository and admitted only when the policy admits it (`Policy.Admits`: provider, project and profile lists) and the node's `bitbucket_token.allow_repos` covers it; any other repository is refused with its name; a per-repository job's pairing is unchanged. This is B5.3.
- Files: `internal/credsvc/bitbucketproxy.go`, `internal/credsvc/server.go`, `internal/credsvc/auth.go`, `internal/credsvc/bitbucketproxy_test.go`, `internal/credsvc/sharedjobs_test.go`.
- Interfaces: consumes C1's `Pairing` and #2545's `pairShared`; produces the refusal text the runner shows.
- Tests: `TestBitbucketProxyPairsASharedJob`, `TestBitbucketProxyRefusesARepoOutsideTheProfile`, `TestBitbucketProxyPerRepoPairingUnchanged`.
- Command: `go test ./internal/credsvc/ -run 'BitbucketProxy' -count=1`
- Red step: the first test fails with the identity unpaired.
- Depends on: none in the graph (the provider side of C1's broken edge S3 → S2); built against `contract.md`, launched after C1 merges; P1.
- Commit: `credsvc: the Bitbucket proxy admits a shared job by its profile (S2, B5.3, D2302)`.
- Reviewer tier: opus, mandatory independent review, one mutation check (a repository outside `allow_repos` must fail).
- Owner-run: no.

### S3: the runner on a shared Bitbucket job
- Acceptance: a run on a shared Bitbucket job is proxied (`(*run).proxiedBitbucket` is true for a project job's coding run), installs the tokenless credential helper and the `insteadOf` rewrite to the forwarder, runs the baked-or-clone checkout, and halts with a clear message when the proxy refuses a repository.
- Files: `internal/runner/runner.go`, `internal/runner/project.go`, `internal/runner/gitproxy_test.go`, `internal/runner/shared_bitbucket_test.go` (new).
- Interfaces: consumes C1's `Pairing` refusal shape; produces the run's git traffic on the forwarder.
- Tests: `TestSharedBitbucketJobUsesTheForwarder`, `TestSharedBitbucketJobHoldsNoToken`, `TestBakedFetchGoesThroughTheForwarder` (settles U3).
- Command: `go test ./internal/runner/ -run 'SharedBitbucket|BakedFetchGoesThrough' -count=1`
- Red step: the first test fails because the runner builds the provider with the mounted token.
- Depends on: C1.
- Commit: `runner: a shared Bitbucket job reaches git only through the forwarder (S3)`.
- Reviewer tier: standard; opus if the review finds a credential path.
- Owner-run: no.

### S4: the launcher serves Bitbucket and baked workflows
- Acceptance: `launchProjectJobFor` returns a shared job for a Bitbucket repository and for a `checkout: baked` workflow whenever a deployed profile admits the repository; the runner's candidate logic agrees; `checkout` no longer chooses the job model (D2304).
- Files: `internal/cli/project_job.go`, `internal/cli/launch_budget.go`, `internal/runner/projectjob.go`, `internal/cli/project_job_test.go`, `internal/runner/projectjob_test.go`.
- Interfaces: consumes C1's `JobChoice`; produces the launch's job name.
- Tests: `TestLaunchProjectJobServesBitbucket`, `TestLaunchProjectJobServesBakedWorkflows`, `TestRunnerCandidatesMatchTheLauncher`.
- Command: `go test ./internal/cli/ ./internal/runner/ -run 'LaunchProjectJob|RunnerCandidates' -count=1`
- Red step: `TestLaunchProjectJobServesBakedWorkflows` fails because baked returns the per-repository job.
- Depends on: C1.
- Commit: `cli: shared jobs serve Bitbucket and baked workflows (S4, T2.2, D2304)`.
- Reviewer tier: standard.
- Owner-run: no.

### S5: `update` writes the default profile and default image
- Acceptance: for a project with no `standard` profile, `fugaro update` writes one (R1: the project's provider, the default account, `allow_repos` derived from the enrolled repositories; no image field, the image is the catalog default) to the local config **before the installation stage**, so the stage that turns profiles into project jobs deploys its job in the same `update`, and publishes it in the policy stage; it also writes a catalog default entry on the base image when the catalog has none, and prints what it created; an existing profile and an existing catalog default are never touched; a second `update` is a no-op; a project that mixes providers gets one profile per provider, named by the provider and written when that provider has none (D2310).
- Files: `internal/cli/init_policy_migrate.go`, `internal/cli/init_policy_publish.go`, `internal/cli/init_stages.go`, `internal/cli/init_policy_migrate_test.go`, `internal/cli/update_default_profile_test.go` (new).
- Interfaces: consumes C1's profile fields and `catalog.Entry`; produces `profiles.standard` and the default entry.
- Tests: `TestUpdateDeploysTheDefaultProfilesJobInTheSameUpdate`, `TestUpdateCreatesTheDefaultProfile`, `TestUpdateNeverRewritesAnEditedProfile`, `TestUpdateDefaultProfileIsIdempotent`, `TestUpdatePreviousVersionConverges`.
- Command: `go test ./internal/cli/ -run 'UpdateDeploysTheDefaultProfilesJob|UpdateCreatesTheDefaultProfile|UpdateNeverRewrites|UpdateDefaultProfile|UpdatePreviousVersion' -count=1`
- Red step: the first test fails because no step writes a profile.
- Depends on: none in the graph; it reads the existing profile types, and launches after C1 merges so it writes C1's fields.
- Commit: `update: create the default shared-job profile and base-image entry (S5, R1, D2305)`.
- Reviewer tier: standard.
- Owner-run: no.

### S6: `update` turns the proxy on
- Acceptance: for a project with a Bitbucket repository, `update` sets `project_secrets.bitbucket-token.proxy` in the local config (the switch `fugaro secrets --proxy` writes, which the existing policy stage then publishes as `bitbucket_token.proxy`, with `allow_repos` derived from the project's Bitbucket repositories by `DeriveBitbucketToken`) and the runner pin at `proxySince`, in the one converge step; when the project's Bitbucket secret holds no workspace token it asks for one once, with the prompt `fugaro secrets` uses (R7); a project already on it is unchanged; the stage's place in `initStages` is fixed so that the local config and the published node never disagree for `resolveBitbucketProxied` during the one `update` (the switch is written in the policy stage's migration, or before the repository stage together with the node it implies, whichever the red step shows converges), and a project at the previous version converges in one `update`; the in-flight line names the step and `update-in-flight.md` has its row.
- Files: `internal/cli/update_proxy_switch.go` (new), `internal/cli/init_stages.go`, `internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md`, `internal/cli/update_in_flight_test.go`.
- Interfaces: consumes the existing switches; produces the converge.
- Tests: `TestUpdateTurnsTheBitbucketProxyOn`, `TestUpdateBitbucketProxyConvergesInOneUpdate`, `TestUpdateAsksForTheWorkspaceTokenOnce`, `TestUpdateInFlightLineNamesEveryStepThatCanReachARun`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'UpdateTurnsTheBitbucketProxyOn|UpdateBitbucketProxyConverges|UpdateAsksForTheWorkspaceToken|UpdateInFlight|UpdateLeavesARunInFlight' -count=1`
- Red step: the first test fails with the switches off.
- Depends on: S5 (S5 registers the profile stage in `init_stages.go` and owns `init_policy_migrate.go`, where the switch may be written); launches only after Check 191 passes (P2).
- Commit: `update: Bitbucket projects converge onto the credential proxy (S6, D2306, R7)`.
- Reviewer tier: opus, independent review (credentials switch).
- Owner-run: no.

### I1: wire the sides and prove it end to end
- Acceptance: the stubs are deleted and `sharedJobProviders` admits `bitbucket` (the one-line switch S1 left, with the pinned reason in the test); a hermetic test runs a shared job for GitHub and for Bitbucket: clone, fetch, push and open a pull request, with an unadmitted repository refused, and a baked checkout accepted only after S9's reset.
- Files: `internal/sharedjob/stub.go` (deleted), `internal/infra/projectjobs.go`, `internal/cli/shared_job_e2e_test.go` (new).
- Interfaces: consumes S1–S4 and C1.
- Tests: `TestSharedJobEndToEndGitHub`, `TestSharedJobEndToEndBitbucket`.
- Command: `go test ./internal/cli/ -run 'SharedJobEndToEnd' -count=1`
- Red step: fails while the stubs stand.
- Depends on: S1, S2, S3, S4 (they are the sides), S9 (the baked checkout on a shared job), C1 (the stubs it deletes); P1.
- Commit: `cli: shared jobs, wired and proven for both providers (I1)`.
- Reviewer tier: opus.
- Owner-run: no.

### S9: a baked checkout on a project job is sanitised and verified
- Acceptance: the refusal in `checkProjectCheckoutOrigin` is replaced (D2308): at job start the runner resets the baked checkout's `.git` config and hooks to a fixed state, refuses a `.git` that is a file pointing elsewhere, alternates, a `commondir`, and any config or hooks under `.git/modules/*`, cleans untracked files, verifies the origin against the run's repository, and only then fetches; a checkout it cannot reset halts the run with a message naming the reason; a project job never runs a hook from a baked `.git`.
- Files: `internal/runner/projectjob.go`, `internal/runner/projectjob_test.go`, `internal/gitops/gitops.go`, `internal/gitops/reset_test.go` (new).
- Interfaces: consumes S3's proxied runner and S4's candidate logic (the same file); produces the baked-or-clone checkout on a project job.
- Tests: `TestBakedCheckoutOnAProjectJobIsResetBeforeFetch`, `TestBakedCheckoutWithAPlantedHookNeverRunsIt`, `TestBakedCheckoutWithAnotherOriginHalts`, `TestBakedCheckoutWithAGitdirFileAlternatesOrSubmoduleHooksHalts`.
- Command: `go test ./internal/runner/ ./internal/gitops/ -run 'BakedCheckoutOnAProjectJob|PlantedHook|AnotherOrigin|GitdirFileAlternates' -count=1`
- Red step: `TestBakedCheckoutWithAPlantedHookNeverRunsIt` fails because the guard refuses the checkout instead of resetting it, and a planted hook is never exercised.
- Depends on: S4 (the same file, `projectjob.go`), S3 (the proxied fetch it resets for).
- Commit: `runner: a baked checkout on a project job is reset and verified (S9, D2308)`.
- Reviewer tier: opus, mandatory independent review, one mutation check (a hook left in place must fail the test).
- Owner-run: no.

### S7: documents and the untested wording
- Acceptance: `docs/git-providers.md` no longer says Bitbucket stays per-repository; `docs/checkout.md` states that baked is a cache; the pinned reach wording and the docs tests agree.
- Files: `docs/git-providers.md`, `docs/checkout.md`, `docs/design/project-centric.md`, `internal/cli/docs_project_wide_git_test.go`.
- Interfaces: none.
- Tests: `TestDocsStateTheBitbucketProjectTokenReach`, the Docs filter; the documents also state D2309 (a run may read the project's other baked repositories) plainly.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: the pinned sentence in `docs_project_wide_git_test.go` is rewritten first, and the test fails until `docs/git-providers.md` carries the new wording.
- Depends on: I1 (the documents describe the behaviour).
- Commit: `docs: Bitbucket runs on shared jobs, and baked is a cache (S7)`.
- Reviewer tier: standard.
- Owner-run: no.

### S8: the Bitbucket shared-job live check script
- Acceptance: `check-580.sh` and its test run one Bitbucket shared-job run on the sandbox (clone, fetch, push, a pull request, and the run's first model call through the profile's `claude_accounts`, F17) and print the register text; the script refuses `belong`.
- Files: `scripts/live-checks/check-580.sh` (new), `scripts/live-checks/check-580_test.sh` (new), `docs/gcp-live-checklist.md`.
- Interfaces: consumes I1.
- Tests: `check-580_test.sh` against the fakes.
- Command: `bash scripts/live-checks/check-580_test.sh`
- Red step: the test fails with no script.
- Depends on: I1.
- Commit: `live-checks: Check 580, the Bitbucket shared job (S8)`.
- Reviewer tier: standard.
- Owner-run: **Check 580**; the owner types `scripts/live-checks/check-580.sh`. P2 (Check 191) first. No agent performs it.

## Slice 2: `project-image-bake`

### C2: the build-token contract
- Acceptance: `internal/buildtoken` declares the request (repositories, time to live), the response (one of a read-only token with an expiry, or a proxy grant: R6) and the client interface; `contract-build-token.md` states the F19 rules (read-only, per build, the clone in a step Fugaro generates entirely, the entry's build holding no secret and no identity); the stub returns the shape.
- Files: `internal/buildtoken/buildtoken.go` (new), `internal/buildtoken/stub.go` (new), `docs/shared-jobs-default/contract-build-token.md` (new).
- Interfaces: produces `buildtoken.Request`, `buildtoken.Response`, `buildtoken.Client`.
- Tests: `TestBuildTokenStubShape`.
- Command: `go test ./internal/buildtoken/ -count=1`
- Red step: compile failure until the types exist.
- Depends on: none.
- Commit: `buildtoken: the contract a catalog build's read token is built against (C2)`.
- Reviewer tier: opus, independent review before B1 and B2 launch.
- Owner-run: no.

### B1: the credential-service build route
- Acceptance: the repositories a build may be given are read from the published image map for the entry the request names, and `contract-build-token.md` states that the one project identity can reach the union of every entry's repositories; for a build identity the service answers a GitHub build with a read-only, short-lived installation token covering exactly the requested repositories of the caller's own project, and a Bitbucket build with a proxy grant (the entry's repositories paired to the identity; no token, R6); it refuses a repository outside the entry's and audits the mint; every other caller class is refused on the build route and the build identity on every other route.
- Files: `internal/credsvc/buildtoken.go` (new), `internal/credsvc/server.go`, `internal/credsvc/bitbucketproxy.go`, `internal/credsvc/buildtoken_test.go` (new), `internal/credsvc/audit.go`.
- Interfaces: consumes C2's `Request`; produces `Response`.
- Tests: `TestBuildTokenIsReadOnly`, `TestBuildTokenCoversOnlyTheRequestedRepos`, `TestBuildTokenExpires`, `TestBuildTokenMintIsAudited`, `TestBitbucketBuildGetsAProxyGrantAndNoToken`, `TestEveryOtherClassRefusedOnTheBuildRoute`, `TestBuildIdentityRefusedOnEveryOtherRoute`.
- Command: `go test ./internal/credsvc/ -run 'BuildToken|BitbucketBuildGetsAProxyGrant|EveryOtherClassRefused|BuildIdentityRefused' -count=1`
- Red step: `TestBuildTokenIsReadOnly` fails with no route.
- Depends on: S2 (the same `server.go` pairing code; a behaviour edge kept: one file, one service); it is the provider side of C2's broken edge B2 → B1, built against `contract-build-token.md` and launched after C2 merges.
- Commit: `credsvc: a build's narrowed read token (B1, D2301)`.
- Reviewer tier: opus, mandatory independent review, one mutation check (a write scope must fail).
- Owner-run: no.

### B2: the catalog build bakes several repositories
- Acceptance: `imagebake.Decide` may bake several repositories into one entry (D2301, superseding O3 (b)); the build asks `buildtoken.Client` for one credential over the entry's repositories, read from the published image map for the named entry (the build identity itself can reach the union of every entry's repositories: stated in `contract-build-token.md`); submodules of a baked repository that the credential does not cover fail naming the repository; an entry that serves a repository the project does not admit is refused.
- Files: `internal/cli/gitcred.go`, `internal/imagebake/imagebake.go`, `internal/imagebake/bake_test.go`, `internal/cli/image_build_catalog.go`, `internal/cli/image_build_catalog_test.go`.
- Interfaces: consumes C2's `Client`; produces the multi-repo bake plan.
- Tests: `TestDecideBakesSeveralRepos`, `TestBakedSubmoduleOutsideTheCredentialNamesTheRepo`, `TestCatalogBuildRefusesAnUnadmittedRepo`, `TestCredentialIsReadFromTheNamedEntrysRepos`.
- Command: `go test ./internal/imagebake/ ./internal/cli/ -run 'DecideBakes|BakedSubmodule|CatalogBuildRefuses|CredentialIsReadFromTheNamedEntry' -count=1`
- Red step: `TestDecideBakesSeveralRepos` fails because `Decide` bakes one repository (O3 (b)).
- Depends on: C2.
- Commit: `catalog: a project image bakes several repositories (B2, D2301)`.
- Reviewer tier: opus (credential in a build).
- Owner-run: no.

### B8: the generated clone step
- Acceptance: the clone runs in a **separate build step whose Dockerfile Fugaro generates entirely**, from the pinned base digest with `GIT_CONFIG_NOSYSTEM=1`, and the checkout reaches the entry's build as a named build context, so the entry's build carries no `--secret` and no identity and no entry `RUN` step can read the credential or alter the clone's tools (F19); the credential is stored per host (the pattern `catalogContextStep` uses) and a URL on any other host, in `.gitmodules` or `.lfsconfig`, is refused before any credential is offered, with `protocol.ext` and `protocol.file` set to never; submodules and LFS objects of a baked repository are fetched in this step; the credential is revoked at the step's end where the provider allows, and otherwise expires within the build's timeout.
- Files: `internal/backend/gcp/catalog.go`, `internal/backend/gcp/catalog_clone.go` (new), `internal/backend/gcp/catalog_test.go`, `internal/backend/gcp/catalog_clone_test.go` (new).
- Interfaces: consumes C2's `Client`; produces the checkout as a build context.
- Tests: `TestCloneStepIsGeneratedAndSeparateFromTheEntryBuild`, `TestEntryBuildCarriesNoSecretAndNoIdentity`, `TestCloneStepRefusesAForeignHostInGitmodulesAndLfsconfig`, `TestCloneStepSetsProtocolExtAndFileToNever`, `TestCloneStepCredentialIsRevokedAtTheEnd`.
- Command: `go test ./internal/backend/gcp/ -run 'CloneStep|EntryBuildCarries' -count=1`
- Red step: `TestEntryBuildCarriesNoSecretAndNoIdentity` fails because the entry's own last stage holds the secret mount.
- Depends on: C2 (the shape).
- Commit: `catalog: the clone runs in a step the entry cannot touch (B8, F19)`.
- Reviewer tier: opus, mandatory independent review.
- Owner-run: no.

### B9: the catalog refuses an entry that mounts a secret
- Acceptance: catalog validation refuses a Dockerfile entry or inline entry that asks for any `RUN --mount=type=secret` or an `--secret` build argument, naming the line and saying the clone step supplies the checkout (D2311); the guidance text in `internal/catalog/fetch.go` that tells authors to mount `git-credentials` is replaced; an existing published entry that does is reported once by `update` and `doctor` and not rebuilt.
- Files: `internal/catalog/fetch.go`, `internal/catalog/fetch_test.go`, `internal/cli/doctor_catalog.go` (new), `internal/cli/doctor_catalog_test.go` (new).
- Interfaces: consumes B8's generated clone step; produces the refusal.
- Tests: `TestCatalogRefusesAnEntryThatMountsASecret`, `TestCatalogGuidanceNoLongerTeachesTheGitCredentialMount`, `TestDoctorReportsAnEntryThatMountsASecret`.
- Command: `go test ./internal/catalog/ ./internal/cli/ -run 'RefusesAnEntryThatMountsASecret|GuidanceNoLonger|DoctorReportsAnEntryThatMounts' -count=1`
- Red step: the first test fails because the catalog tells authors to write the mount.
- Depends on: B8 (the clone step that replaces the mount).
- Commit: `catalog: an entry mounts no secret; the clone step supplies the checkout (B9, D2311)`.
- Reviewer tier: opus (credentials).
- Owner-run: no.

### B5: the build identity
- Acceptance: a project has one build service account for catalog builds, granted invoker on the credentials service and rendered into the service's account sets as the build class (B1), so a shared repository's catalog build has an account to run as after M2 destroys the per-repository one; the account holds no write role on any repository registry but the project's own.
- Files: `deploy/terraform/gcp/modules/project/build_identity.tf` (new), `internal/infra/build_identity.go` (new), `internal/infra/build_identity_test.go` (new), `internal/credsvc/auth.go`, `internal/credsvc/auth_test.go`.
- Interfaces: consumes B1's build class; produces the account `CatalogBuildSpec` runs as.
- Tests: `TestProjectHasOneBuildIdentity`, `TestBuildIdentityIsInvokerOnTheService`, `TestBuildIdentityIsInTheServicesBuildClass`; `terraform fmt -check`.
- Command: `go test -tags terraform ./internal/infra/ ./internal/credsvc/ -run 'BuildIdentity' -count=1`
- Red step: the first test fails because no project-level build account exists.
- Depends on: B1 (the class it is rendered into; `auth.go` is S2's file at L0).
- Commit: `infra: the project's build identity for catalog builds (B5, D2301)`.
- Reviewer tier: opus (IAM, Terraform).
- Owner-run: no.

### B6: catalog builds and the catalog check run as the build identity
- Acceptance: `CatalogBuildSpec` runs as the project's build identity (B5) and `catalogcheck.tf` grants actAs on it and not on the per-repository build account; the identity holds the writer and tag-mover roles on the shared registry that the per-repository account held through `registry.tf`, and nothing wider.
- Files: `internal/backend/gcp/catalog.go`, `internal/backend/gcp/catalog_test.go`, `deploy/terraform/gcp/modules/repo/catalogcheck.tf`, `deploy/terraform/gcp/modules/project/build_identity.tf`.
- Interfaces: consumes B5's account; produces the account the build and check run as.
- Tests: `TestCatalogBuildRunsAsTheBuildIdentity`, `TestCatalogCheckActsAsTheBuildIdentity`, `TestBuildIdentityHoldsOnlyTheRegistryRoles`; `terraform fmt -check`.
- Command: `go test -tags terraform ./internal/backend/gcp/ ./internal/infra/ -run 'RunsAsTheBuildIdentity|CatalogCheckActsAs|HoldsOnlyTheRegistryRoles' -count=1`
- Red step: the first test fails because the spec requires the repository's build account.
- Depends on: B5 (the account), B3 (the same file, `catalog.go`; one writer per level).
- Commit: `catalog: builds and the check run as the project's build identity (B6)`.
- Reviewer tier: opus (IAM).
- Owner-run: no.

### I2: wire the build to the route
- Acceptance: the stub is deleted and a hermetic test builds a catalog entry over two repositories with a credential from the service and bakes both; a second test shows a run for one of them can read the other under `/work/baked` (D2309, pinned so the choice is visible).
- Files: `internal/buildtoken/stub.go` (deleted), `internal/cli/catalog_bake_e2e_test.go` (new).
- Interfaces: consumes B1, B2, C2.
- Tests: `TestCatalogBuildBakesTwoReposThroughTheService`, `TestARunCanReadTheProjectsOtherBakedRepos`.
- Command: `go test ./internal/cli/ -run 'CatalogBuildBakesTwoRepos|ARunCanReadTheProjectsOtherBakedRepos' -count=1`
- Red step: fails while the stub stands.
- Depends on: B1, B2, B8, C2.
- Commit: `catalog: the build reaches the credential service (I2)`.
- Reviewer tier: opus.
- Owner-run: no.

### B3: the leak scan in the catalog build
- Acceptance: the catalog build runs the scan before the promote step and refuses an image that holds git config credentials, a `.netrc`, a credential-helper store or a token in the environment or in any layer; a clean image passes.
- Files: `images/scan.sh`, `images/scan_test.go`, `images/trivy-secret.yaml`, `internal/backend/gcp/catalog.go`, `internal/backend/gcp/catalog_test.go`.
- Interfaces: consumes the built image reference.
- Tests: `TestScanRefusesAnImageWithAGitCredential`, `TestScanRefusesANetrc`, `TestScanRefusesATokenInTheEnvironment`, `TestCatalogBuildStepsRunTheScanBeforePromote`.
- Command: `go test ./images/ ./internal/backend/gcp/ -run 'Scan|CatalogBuildSteps' -count=1`
- Red step: the scan tests fail on fixtures with each leftover.
- Depends on: I2 (the same `catalog.go`; one writer per level), B8.
- Commit: `catalog: the build scans its image for leftover credentials (B3, R2)`.
- Reviewer tier: opus.
- Owner-run: no.

### B4: the image-build live check script
- Acceptance: `check-581.sh` and its test build one catalog entry over two repositories on the sandbox, run the scan and print the register text.
- Files: `scripts/live-checks/check-581.sh` (new), `scripts/live-checks/check-581_test.sh` (new), `docs/gcp-live-checklist.md`.
- Interfaces: consumes B3.
- Tests: `check-581_test.sh`.
- Command: `bash scripts/live-checks/check-581_test.sh`
- Red step: no script.
- Depends on: B3.
- Commit: `live-checks: Check 581, the multi-repository image build (B4)`.
- Reviewer tier: standard.
- Owner-run: **Check 581**; the owner types `scripts/live-checks/check-581.sh`.

## Slice 3: `git-nested`

### N1: the proxy carries LFS and submodules
- Acceptance: `gitproxy.Classify` classifies LFS batch and object paths as fetch-class and the same `Decide` applies; a nested request for a repository outside the profile is refused naming that repository; an LFS write is refused.
- Files: `internal/gitproxy/rules.go`, `internal/gitproxy/rules_test.go`, `internal/credsvc/bitbucketproxy.go`, `internal/credsvc/bitbucketproxy_nested_test.go` (new).
- Interfaces: consumes S2's pairing; produces the refusal text.
- Tests: `TestClassifyLFSBatchAsFetch`, `TestProxyRefusesAnUncoveredSubmoduleRepo`, `TestProxyRefusesAnLFSWrite`.
- Command: `go test ./internal/gitproxy/ ./internal/credsvc/ -run 'ClassifyLFS|UncoveredSubmodule|LFSWrite' -count=1`
- Red step: the first test fails (U2).
- Depends on: I1 (same `bitbucketproxy.go` as S2).
- Commit: `gitproxy: LFS and submodule traffic, checked like any fetch (N1, R4)`.
- Reviewer tier: opus.
- Owner-run: no.

### N2: the runner points nested fetches at the forwarder
- Acceptance: submodule URLs and the LFS endpoint resolve to the forwarder; a submodule outside the token's coverage fails with a message naming the repository; LFS filters stay off for a clone with no LFS endpoint.
- Files: `internal/runner/runner.go`, `internal/gitops/gitops.go`, `internal/gitops/submodule_test.go` (new), `internal/runner/gitproxy_test.go`.
- Interfaces: consumes the forwarder; produces nested fetches on it.
- Tests: `TestSubmoduleURLsUseTheForwarder`, `TestLFSEndpointUsesTheForwarder`, `TestUncoveredSubmoduleNamesTheRepo`.
- Command: `go test ./internal/gitops/ ./internal/runner/ -run 'SubmoduleURLs|LFSEndpoint|UncoveredSubmodule' -count=1`
- Red step: the first test fails on the original URL.
- Depends on: I1 (the same `runner.go` as S3).
- Commit: `runner: submodules and LFS ride the forwarder (N2, R4)`.
- Reviewer tier: standard.
- Owner-run: no.

### N3: nested fetch end to end
- Acceptance: a hermetic run clones a repository with one submodule and one LFS file through the fake proxy, and refuses the uncovered submodule's repository by name.
- Files: `internal/runner/nested_e2e_test.go` (new).
- Interfaces: consumes N1 and N2 (no contract: they do not call each other).
- Tests: `TestNestedFetchEndToEnd`.
- Command: `go test ./internal/runner/ -run NestedFetchEndToEnd -count=1`
- Red step: fails with N1 or N2 absent.
- Depends on: N1, N2.
- Commit: `runner: nested fetch proven end to end (N3)`.
- Reviewer tier: standard.
- Owner-run: no.

## Slice 4: `default-profile-migrate`

### M6: the repository spec and the images stage stop emitting per-repository things for a shared repository
- Acceptance: for a repository a deployed profile admits, `infra/spec.go` marks it shared and emits no workflow job, registry, build account or check job inputs, and the images stage (`newImagesStage`, `refreshBuilds`) no longer converges per-repository images for it and converges the project's catalog entries per project, not per repository (F5, spec decision 3, U8); a repository no profile admits keeps both.
- Files: `internal/infra/spec.go`, `internal/infra/spec_test.go`, `internal/cli/init_images.go`, `internal/cli/init_images_test.go`.
- Interfaces: consumes S5's profile; produces the `shared` mark the module reads.
- Tests: `TestSpecMarksASharedRepoAndEmitsNoJobs`, `TestImagesStageSkipsASharedRepo`, `TestARepoNoProfileAdmitsKeepsItsJobs`.
- Command: `go test ./internal/infra/ ./internal/cli/ -run 'SpecMarksASharedRepo|ImagesStageSkips|ARepoNoProfileAdmits' -count=1`
- Red step: the first test fails because every workflow yields job inputs.
- Depends on: I1 (shared jobs work), S5 (the default profile exists); launches after Check 580 passes.
- Commit: `infra: a shared repository is marked and emits no jobs of its own (M6, F5)`.
- Reviewer tier: opus (Terraform inputs, IAM).
- Owner-run: no.

### M1: repository-level secrets are accepted, ignored and warned about
- Acceptance: a workflow's `secrets:` is accepted by validation and ignored: no job mounts it and no record of it is written for the credential service; `fugaro validate` and `update` print one warning per repository saying repository-level secrets are disabled for now and ignored, and naming project secrets; the repository-scope forms of `fugaro secrets` and `fugaro secrets rm` answer a notice (exit 0, change nothing) in the shape of `repoImagesDisabledNotice`; nothing is deleted, so the feature can return (D2313).
- Files: `internal/config/secrets_disabled.go` (new), `internal/config/secrets_disabled_test.go` (new), `internal/cli/secrets.go`, `internal/cli/secrets_rm.go`, `internal/cli/update_workflowsecrets.go`.
- Interfaces: consumes the workflow's `secrets:` field; produces the warning text M7 prints at `update`.
- Tests: `TestWorkflowSecretsAreAcceptedAndIgnored`, `TestValidateWarnsThatRepoSecretsAreIgnored`, `TestRepoScopeSecretsCommandsAnswerTheNotice`, `TestNoWorkflowSecretsRecordIsWrittenForAnIgnoredSecret`.
- Command: `go test ./internal/config/ ./internal/cli/ -run 'WorkflowSecretsAreAccepted|ValidateWarnsThatRepoSecrets|RepoScopeSecretsCommands|NoWorkflowSecretsRecord' -count=1`
- Red step: the first test fails because the secret is mounted by a repository job.
- Depends on: none.
- Commit: `config: repository-level secrets are accepted and ignored with a warning (M1, D2313)`.
- Reviewer tier: opus (secrets).
- Owner-run: no.

### M7: onboarding a repository applies nothing
- Acceptance: for a repository a deployed profile admits, `newRepositoryStage` and `repoEngine` enrol the repository (the profile's `allow_repos`, the project sync, the policy), apply no repository-level Terraform at all (D2312 as amended) and print M1's warning once when the repository declares `secrets:`; a repository no profile admits keeps the existing path; removing a repository is a config change only.
- Files: `internal/cli/init_repo_stage.go`, `internal/cli/init.go`, `internal/cli/init_repo_stage_test.go`.
- Interfaces: consumes M6's shared mark and M1's warning; produces an apply-free onboarding.
- Tests: `TestOnboardingASharedRepoAppliesNoTerraform`, `TestRemovingASharedRepoIsAConfigChange`, `TestARepoNoProfileAdmitsKeepsTheRepoRoot`.
- Command: `go test ./internal/cli/ -run 'OnboardingASharedRepoApplies|RemovingASharedRepo|ARepoNoProfileAdmitsKeeps' -count=1`
- Red step: the first test fails because the stage runs the repository root's apply.
- Depends on: M6 (the mark), M1 (the warning and the ignoring, before the repository root is skipped); launches after Check 580 passes.
- Commit: `update: a shared repository is enrolled with no repository-level Terraform (M7, D2312)`.
- Reviewer tier: opus (Terraform, IAM).
- Owner-run: no.

### M2: `update` retires the per-repository jobs
- Acceptance: `update` retires a repository's whole root (its jobs, registry, build account and check job destroyed with the confirmation `update` asks; its secret containers, which hold the values a returning repository-level secrets feature will want, removed from the root's state and never destroyed) once its structural precondition holds (admitted to a deployed profile, project image resolves, the proxy on for a Bitbucket repository, **and no execution running on the job**, so a job with a run in flight is retired by a later `update`) and not before; the confirmation retirement needs outside a managed project is asked inside `update`, never as a second command, and the in-flight line names the retirement; an installation at the previous version converges; a failed first shared run is repaired by running `update` again, which re-converges the profile, the image and the proxy, and the user relaunches the run.
- Files: `internal/cli/init_retire.go`, `internal/infra/retire.go`, `internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md`, `internal/cli/init_retire_test.go`.
- Interfaces: consumes M1, S5, S6; produces the retirement.
- Tests: `TestUpdateRetiringARootLeavesItsSecretContainers`, `TestUpdateRetiresTheRepoJobsOfAMigratedRepo`, `TestUpdateDoesNotRetireBeforeThePrecondition`, `TestUpdateDoesNotRetireAJobWithARunningExecution`, `TestUpdateRepairsAFailedFirstSharedRun`, `TestUpdatePreviousVersionConvergesToSharedJobs`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'UpdateRetiring|UpdateRetires|UpdateDoesNotRetire|UpdateRepairsAFailedFirstSharedRun|UpdatePreviousVersionConverges|UpdateLeavesARunInFlight' -count=1`
- Red step: the first test fails: nothing retires a repository root.
- Depends on: M7 (no root is applied any more, so the old one is retired), M1 (repository-level secrets are ignored before their containers are cut loose), B6 (the build and the check act as the project identity before the per-repository account is destroyed), S5 (the profile), S6 (the proxy, and the `update_in_flight.go` rows: one writer per level); launches after Check 580 passes.
- Commit: `update: retire the per-repository jobs of a migrated repository (M2, D2303)`.
- Reviewer tier: opus, independent review, one mutation check (the precondition off must keep the jobs).
- Owner-run: no.

### M3: the deprecation warning
- Acceptance: when a workflow still targets a per-repository job, `update` and `doctor` name it once with the shared-job replacement; nothing else changes.
- Files: `internal/cli/updatewarn.go`, `internal/cli/doctor_jobs.go` (new), `internal/cli/updatewarn_test.go`.
- Interfaces: consumes S4's `JobChoice`.
- Tests: `TestUpdateWarnsOnARepoBoundJob`, `TestDoctorNamesTheSharedReplacement`.
- Command: `go test ./internal/cli/ -run 'UpdateWarnsOnARepoBoundJob|DoctorNamesTheSharedReplacement' -count=1`
- Red step: the first test fails with no warning.
- Depends on: S4 (it reads the launcher's decision).
- Commit: `cli: a per-repository job warns, naming its replacement (M3, R3)`.
- Reviewer tier: standard.

### M4: release notes and the website summary
- Acceptance: the release's notes say "run `fugaro update`" and what it asks, name the proxy turn-on and the retirement, and add the summary line (no backticks).
- Files: the release notes of the release that carries the work (the next unreleased file under `docs/releases/`, named at launch from `docs/release-streams.yaml`), `website/data/release-summaries.yaml`.
- Interfaces: none.
- Tests: the Docs filter and `npm run check` in `website/`.
- Command: `go test ./internal/cli/ -run Docs -count=1`, then `npm run check` in `website/`.
- Red step: the release-notes docs test that requires each release file's summary line fails until the line is added.
- Depends on: M2, S7.
- Commit: `docs(releases): shared jobs are the default; run fugaro update (M4)`.
- Reviewer tier: standard.

### M5: the migration live check script
- Acceptance: `check-582.sh` and its test run `update` on a sandbox project with a per-repository job and assert the profile, the image, the proxy and the retirement, then one run on the shared job.
- Files: `scripts/live-checks/check-582.sh` (new), `scripts/live-checks/check-582_test.sh` (new), `docs/gcp-live-checklist.md`.
- Interfaces: consumes M2.
- Tests: `check-582_test.sh`.
- Command: `bash scripts/live-checks/check-582_test.sh`
- Red step: no script.
- Depends on: M2 (what it asserts), B4 (the same `docs/gcp-live-checklist.md`; one writer per level).
- Commit: `live-checks: Check 582, the migration (M5)`.
- Reviewer tier: standard.
- Owner-run: **Check 582**; the owner types `scripts/live-checks/check-582.sh`, never on `belong`.

## Parallel execution

**Edges kept** (the dependent needs behaviour or the same file); the CLI's read counts 26, leaving out the edges that touch an integration task, and this list names all of them:
- S9 → S4, S3: the same file, `runner/projectjob.go`, and the proxied fetch it resets for.
- I1 → S9: the integration runs the baked checkout on a shared job.
- S7 → I1, S8 → I1: they document and exercise what I1 wires.
- B5 → B1: the class it is rendered into.
- B6 → B5, B6 → B3: the identity, and the same file `catalog.go`.
- B9 → B8: the clone step that replaces the mount.
- B8 → C2, I2 → B8, B3 → B8: the shape, the integration, and the same file `catalog.go`.
- S6 → S5: S5 registers a stage in `init_stages.go` and owns `init_policy_migrate.go`.
- B1 → S2: one file, `credsvc/server.go`, and the same pairing code; a contract would not split one file.
- B3 → I2, N1 → I1, N2 → I1, M2 → S6, B4 → S8: one file each (`catalog.go`, `bitbucketproxy.go`, `runner.go`, `update_in_flight.go`, `docs/gcp-live-checklist.md`), so one writer per level.
- M7 → M6, M7 → M1: the mark, and the warning and ignoring before the root is skipped.
- M6 → I1, M6 → S5: marking a repository shared is safe only once shared jobs work and the default profile exists.
- M2 → M7, M2 → M1, M2 → B6, M2 → S5, M2 → S6: the retirement of a root follows the apply-free onboarding, the ignoring of repository secrets, the project identity, the profile and the proxy.
- M3 → S4: the warning reads the launcher's decision.
- M4 → M2, M4 → S7, M5 → M2, M5 → B4 (one checklist file), N3 → N1 and N2, B4 → B3: behaviour.

**Edges broken by contract** (3):
- S3 needed S2 for the pairing's refusal shape only: broken by **C1**, re-joined in **I1**.
- S4 needed S1 for the provider list and the profile shape only: broken by **C1**, re-joined in **I1**.
- B2 needed B1 for the request and response shape only: broken by **C2**, re-joined in **I2**.

The graph is in [implementation-plan.graph.json](implementation-plan.graph.json). Its schema refuses an edge from a provider to its contract and from an integration to its contract, so S1, S2, B1 and B8 are drawn without that edge and launch after their contract merges (see the launch gates above).

**Levels** (as `fugaro plan graph` reads them; tasks of a level run at once):
- L0: C1, C2, S1, S2, S5, B8, M1 (CR1 and CR2 are anticipated change requests, never launched unless raised)
- L1: S3, S4, S6, B1, B2, B9
- L2: I2, M3, S9, B5
- L3: I1, B3
- L4: S7, S8, N1, N2, M6, B6
- L5: B4, N3, M7
- L6: M2
- L7: M4, M5

**Critical path** (the read): C1 → S3 → S9 → I1 → M6 → M7 → M2 → M4. It is sequential because the old repository roots are retired only once shared jobs work (and the baked checkout is trusted), the repository records are marked shared, and onboarding no longer applies a root; the release notes close it. The owner-run checks follow.

**Shared-files table** (no level holds two writers of one file; the contract's own `stub.go` is deleted by its integration task):

| File | Tasks (by level) |
| --- | --- |
| `internal/credsvc/server.go` | S2 (L0), B1 (L1) |
| `internal/credsvc/bitbucketproxy.go` | S2 (L0), B1 (L1), N1 (L4) |
| `internal/credsvc/auth.go` | S2 (L0), B5 (L2) |
| `internal/runner/runner.go` | S3 (L1), N2 (L4) |
| `internal/runner/projectjob.go` | S4 (L1), S9 (L2) |
| `internal/gitops/gitops.go` | S9 (L2), N2 (L4) |
| `internal/backend/gcp/catalog.go`, `catalog_test.go` | B8 (L0), B3 (L3), B6 (L4) |
| `internal/infra/projectjobs.go` | S1 (L0), I1 (L3) |
| `internal/cli/init_stages.go`, `internal/cli/init_policy_migrate.go` | S5 (L0), S6 (L1; the switch's alternative home) |
| `internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md` | S6 (L1), M2 (L6) |
| `docs/gcp-live-checklist.md` | S8 (L4), B4 (L5), M5 (L7) |
| `internal/cli/init.go`, `internal/cli/init_repo_stage.go` | M7 (L5) |
| `deploy/terraform/gcp/modules/project/build_identity.tf` | B5 (L2), B6 (L4) |
| `internal/sharedjob/stub.go` | C1 (L0), I1 (L3) |
| `internal/buildtoken/stub.go` | C2 (L0), I2 (L2) |

CR1 and CR2 share files with C1 and C2 and are launched only when raised, never at level 0.

**Contract changes anticipated**: CR1 amends C1 if S2's review finds the pairing needs the workflow name (it reaches S3 and S4; launched runs are followed up by a run on their pull request that names `internal/sharedjob/sharedjob.go` by path); CR2 amends C2 if B1's review finds a time-to-live cap belongs in the request (it reaches B2).

**The gain** (the read): 3 edges broken, 26 kept, 6 nodes added (two contracts, two integrations, two anticipated change requests); the weighted path goes from 19.5 to 19, which the CLI reports as an improvement. The gain is small on purpose: the dominant chain is the migration (M6 → M7 → M2), whose edges are behaviour edges, and the contracts mainly let the Bitbucket runner, launcher and deployer, and the build and the route, run side by side.

## Checks the plan takes

| Check | Measures | Who |
| --- | --- | --- |
| 191 (existing), 192 | The Bitbucket proxy end to end (the spec's release blocker) | The owner |
| 580 | A Bitbucket shared job: clone, fetch, push, a pull request | The owner |
| 581 | A multi-repository catalog build and the leak scan | The owner |
| 582 | `update` migrates a project and retires its per-repository jobs | The owner |

## After-deploy steps

None beyond `fugaro update`: S5, S6 and M2 do the profile, the proxy switch and the retirement inside it. M4 writes the release notes' `### For operators` as "run `fugaro update`" and what it asks (the retirement is named, as the in-flight line names it).

## Considered and declined

- **A run gate for retirement** (the spec's "after one success"): declined by the owner's answer to D2303. The structural precondition in M2 stands in its place.
- **A new Fugaro-issued Bitbucket token**: declined by D2302; the job holds nothing.
- **Deleting the repo-bound and repo-level image code**: out of scope in the spec.
- **A runner step that removes the other baked repositories** (the review's recommendation): declined by the owner's answer to D2309; I2 pins the read so the choice stays visible.
- **Narrowing a shared GitHub job to its repositories** (decision 4's GitHub half, F21): it waits on secrets-brokering's B5.7 flip, which this plan does not do; until it, a shared GitHub job mounts the App key.
- **Graph edges from a provider or an integration to its contract**: the graph schema refuses them, so the plan says the launch order in prose.
- **A run record that says a shared job succeeded**: not needed once D2303 dropped the gate; the launch record already names the job (F12).
- **Keeping a per-repository root for secrets and the catalog check** (the plan's first reading): declined by the owner on 2026-11-13, "no repo-level Terraform" (D2312 as amended); the catalog check acts as the project identity (B6).
- **Adopting repository secrets as project secrets** (the plan's second reading): declined by the owner on 2026-11-13; repository-level secrets are disabled for now like repository-level images (D2313), accepted and ignored with a warning, and their code stays.
- **Deleting the repository-level secrets code**: out of scope, as for images; decide later.
