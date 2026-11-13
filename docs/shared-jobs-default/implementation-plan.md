# Implementation plan: project-level shared jobs as the default

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (decisions D2300–D2306, findings F1–F20).
**Nothing is implemented, and no task is approved: each needs the owner's explicit go.** The answers that shaped the plan:
shared jobs take Bitbucket by pairing and not by a new token (D2302); a build may bake several repositories with a
credential-service token (D2301, which supersedes O3 (b)); `checkout: baked` runs on shared jobs (D2304); `update`
writes a default profile and a base-image catalog entry (R1, D2305), turns the proxy on (D2306) and retires the
per-repository jobs with no run gate (D2303, not the recommendation).

The decoupling pass is **on** (`fugaro settings planning.decoupling` reads `true`, the default). Mode: `fugaro settings
rigor.default` was not read for each path; every task below that touches credentials, a write guard, Terraform or IAM is
flagged opus with an independent review, and the contracts over credentials get the strictest rigor.

## Prerequisites outside this plan

- **P1.** B5.1 (#2545, `docs/secrets-brokering`) merged: the GitHub shared-job class in the credential service. S2 extends its `pairShared`.
- **P2.** Check 191 (owner-run, `scripts/live-checks/check191.sh`) passed, with #2549 and #2550 landed. The spec's release blocker; **no task starts it and no agent performs it**.
- Tasks of `docs/bitbucket-workspace-token` (T2.2, T2.7) and `docs/secrets-brokering` (B5.x) that this plan overlaps stay theirs; S1 and S2 name them in their commits and a merger closes the older row.

## Conventions

- **Always**: tests before the commit; targeted `go test ./<pkg>/ -run '<names>' -count=1` (CLAUDE.md); `gofmt -l .` empty; a doc change runs `go test ./internal/cli/ -run Docs`; a task's pull request names its `T#` and `D#`.
- **Ask first**: a schema or wire-format change (a field is added, never retyped: CLAUDE.md "a field never changes meaning"); a new dependency; any `.github/workflows` change (stage it under `docs/pending-workflows/`, never edit it).
- **Never**: a secret-shaped literal in a test, doc or fixture; a live cloud action by an agent; a TODO comment; a token or credential file in an image layer; deleting the repo-bound or repo-level image code (spec: out of scope).
- Hermetic tests only: `internal/gcpfake`, `internal/gitprov` fake, `internal/testutil`, `t.TempDir()`.
- **Hard rule** (CLAUDE.md): every `fugaro update` step is idempotent, safe with runs in flight, has a row in `docs/design/update-in-flight.md` and a test that an installation at the previous version converges. The release notes' `### For operators` is "run `fugaro update`" and what it asks.
- Reviewer tier **opus + independent review** for: S2, B1, C1, C2, N1, M1, M2, S6, B3 (credentials, the proxy, Terraform, IAM). Standard for the rest.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A shared Bitbucket job mounts no token; the runner's git, REST, submodule and LFS traffic all reach the forwarder | S3, N2, I1 |
| A call for a repository outside the profile's `allow_repos` is refused by the service, naming the repository | S2, N1 |
| The build token is read-only, short-lived, covers only the entry's repositories and reaches no `RUN` step | B1, B2, B3 |
| No token or credential file survives in any image layer: git config, `.netrc`, credential helper store, environment | B3 |
| `update` never rewrites an existing profile; a second run changes nothing | S5 |
| `update` with a run in flight leaves it running; the retirement deletes only an orphaned job and is named in the in-flight line | M2 |
| Adding or removing a repository needs no Terraform apply | M1 |
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
- Acceptance: `infra.SharedJobProvider("bitbucket")` is true; `ProjectJobs` deploys a job for a valid Bitbucket profile with no Bitbucket secret mounted and the proxy required; a profile that mounts the Bitbucket secret stays refused.
- Files: `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`, `internal/projectpolicy/profiles.go`, `internal/projectpolicy/profiles_test.go`.
- Interfaces: consumes C1's profile fields; produces a Bitbucket job in the installation root.
- Tests: `TestProjectJobsDeployABitbucketProfile`, `TestBitbucketProfileMountsNoToken`, `TestSharedJobProviders`.
- Command: `go test ./internal/infra/ ./internal/projectpolicy/ -run 'ProjectJobs|BitbucketProfile|SharedJobProviders' -count=1`
- Red step: `TestProjectJobsDeployABitbucketProfile` fails because the provider list admits GitHub alone.
- Depends on: none in the graph (S1 is the provider side of C1's broken edge S4 → S1); it is built against `contract.md` and launches after C1 merges.
- Commit: `infra: a shared project job serves a Bitbucket profile (S1, D2302)`.
- Reviewer tier: standard.
- Owner-run: no.

### S2: the proxy pairs a shared Bitbucket job
- Acceptance: a call on the Bitbucket proxy route by a shared job's verified identity is paired to the run's repository and admitted only when that repository is in the profile's `allow_repos`; any other repository is refused with its name; a per-repository job's pairing is unchanged.
- Files: `internal/credsvc/bitbucketproxy.go`, `internal/credsvc/server.go`, `internal/credsvc/bitbucketproxy_test.go`, `internal/credsvc/sharedjobs_test.go`.
- Interfaces: consumes C1's `Pairing` and #2545's `pairShared`; produces the refusal text the runner shows.
- Tests: `TestBitbucketProxyPairsASharedJob`, `TestBitbucketProxyRefusesARepoOutsideTheProfile`, `TestBitbucketProxyPerRepoPairingUnchanged`.
- Command: `go test ./internal/credsvc/ -run 'BitbucketProxy' -count=1`
- Red step: the first test fails with the identity unpaired.
- Depends on: none in the graph (the provider side of C1's broken edge S3 → S2); built against `contract.md`, launched after C1 merges; P1.
- Commit: `credsvc: the Bitbucket proxy admits a shared job by its profile (S2, D2302)`.
- Reviewer tier: opus, mandatory independent review, one mutation check (a repository outside `allow_repos` must fail).
- Owner-run: no.

### S3: the runner on a shared Bitbucket job
- Acceptance: a run on a shared Bitbucket job installs the tokenless credential helper and the `insteadOf` rewrite to the forwarder, runs the baked-or-clone checkout, and halts with a clear message when the proxy refuses a repository.
- Files: `internal/runner/runner.go`, `internal/runner/projectjob.go`, `internal/runner/gitproxy_test.go`, `internal/runner/projectjob_test.go`.
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
- Files: `internal/cli/project_job.go`, `internal/cli/launch_budget.go`, `internal/runner/project.go`, `internal/cli/project_job_test.go`.
- Interfaces: consumes C1's `JobChoice`; produces the launch's job name.
- Tests: `TestLaunchProjectJobServesBitbucket`, `TestLaunchProjectJobServesBakedWorkflows`, `TestRunnerCandidatesMatchTheLauncher`.
- Command: `go test ./internal/cli/ ./internal/runner/ -run 'LaunchProjectJob|RunnerCandidates' -count=1`
- Red step: `TestLaunchProjectJobServesBakedWorkflows` fails because baked returns the per-repository job.
- Depends on: C1.
- Commit: `cli: shared jobs serve Bitbucket and baked workflows (S4, D2304)`.
- Reviewer tier: standard.
- Owner-run: no.

### S5: `update` writes the default profile and default image
- Acceptance: for a project with no `standard` profile, `fugaro update` writes one to the local config and publishes it, plus a catalog default entry on the base image when the catalog has none, and prints what it created; an existing profile and an existing catalog default are never touched; a second `update` is a no-op.
- Files: `internal/cli/init_policy_migrate.go`, `internal/cli/init_policy_publish.go`, `internal/cli/init_policy_migrate_test.go`, `internal/cli/update_default_profile_test.go` (new).
- Interfaces: consumes C1's profile fields and `catalog.Entry`; produces `profiles.standard` and the default entry.
- Tests: `TestUpdateCreatesTheDefaultProfile`, `TestUpdateNeverRewritesAnEditedProfile`, `TestUpdateDefaultProfileIsIdempotent`, `TestUpdatePreviousVersionConverges`.
- Command: `go test ./internal/cli/ -run 'UpdateCreatesTheDefaultProfile|UpdateNeverRewrites|UpdateDefaultProfile|UpdatePreviousVersion' -count=1`
- Red step: the first test fails because no step writes a profile.
- Depends on: none in the graph; it reads the existing profile types, and launches after C1 merges so it writes C1's fields.
- Commit: `update: create the default shared-job profile and base-image entry (S5, R1, D2305)`.
- Reviewer tier: standard.
- Owner-run: no.

### S6: `update` turns the proxy on
- Acceptance: for a project with a Bitbucket repository, `update` sets the proxy and credentials-service switches and the runner pin at `proxySince`, in the one converge step; a project already on it is unchanged; the in-flight line names the step and `update-in-flight.md` has its row.
- Files: `internal/cli/init_credentials.go`, `internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md`, `internal/cli/update_in_flight_test.go`.
- Interfaces: consumes the existing switches; produces the converge.
- Tests: `TestUpdateTurnsTheBitbucketProxyOn`, `TestUpdateInFlightLineNamesEveryStepThatCanReachARun`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'UpdateTurnsTheBitbucketProxyOn|UpdateInFlight|UpdateLeavesARunInFlight' -count=1`
- Red step: the first test fails with the switches off.
- Depends on: none.
- Commit: `update: Bitbucket projects converge onto the credential proxy (S6, D2306)`.
- Reviewer tier: opus, independent review (credentials switch).
- Owner-run: no.

### I1: wire the sides and prove it end to end
- Acceptance: the stubs are deleted; a hermetic test runs a shared job for GitHub and for Bitbucket: clone, fetch, push and open a pull request, with the repository an unadmitted one refused.
- Files: `internal/sharedjob/stub.go` (deleted), `internal/cli/shared_job_e2e_test.go` (new).
- Interfaces: consumes S1–S4 and C1.
- Tests: `TestSharedJobEndToEndGitHub`, `TestSharedJobEndToEndBitbucket`.
- Command: `go test ./internal/cli/ -run 'SharedJobEndToEnd' -count=1`
- Red step: fails while the stubs stand.
- Depends on: S1, S2, S3, S4 (they are the sides), C1.
- Commit: `cli: shared jobs, wired and proven for both providers (I1)`.
- Reviewer tier: opus.
- Owner-run: no.

### S7: documents and the untested wording
- Acceptance: `docs/git-providers.md` no longer says Bitbucket stays per-repository; `docs/checkout.md` states that baked is a cache; the pinned reach wording and the docs tests agree.
- Files: `docs/git-providers.md`, `docs/checkout.md`, `docs/design/project-centric.md`, `internal/cli/docs_bitbucket_token_test.go`.
- Interfaces: none.
- Tests: `TestDocsStateTheBitbucketProjectTokenReach`, the Docs filter.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: the docs test fails once the sentence is changed in one place only.
- Depends on: I1 (the documents describe the behaviour).
- Commit: `docs: Bitbucket runs on shared jobs, and baked is a cache (S7)`.
- Reviewer tier: standard.
- Owner-run: no.

### S8: the Bitbucket shared-job live check script
- Acceptance: `check-580.sh` and its test run one Bitbucket shared-job run on the sandbox (clone, fetch, push, a pull request) and print the register text; the script refuses `belong`.
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
- Acceptance: `internal/buildtoken` declares the request (repositories, time to live), the response (a read-only token, expiry) and the client interface; `contract-build-token.md` states the F19 rules (read-only, per build, no `RUN` step); the stub returns the shape.
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
- Acceptance: the service mints a read-only, short-lived token covering exactly the requested repositories of the caller's own project for a build identity, refuses a repository outside the entry's, and audits the mint; GitHub uses an installation token narrowed to the repositories, Bitbucket the proxy's pairing (D2302).
- Files: `internal/credsvc/buildtoken.go` (new), `internal/credsvc/server.go`, `internal/credsvc/buildtoken_test.go` (new), `internal/credsvc/audit.go`.
- Interfaces: consumes C2's `Request`; produces `Response`.
- Tests: `TestBuildTokenIsReadOnly`, `TestBuildTokenCoversOnlyTheRequestedRepos`, `TestBuildTokenExpires`, `TestBuildTokenMintIsAudited`.
- Command: `go test ./internal/credsvc/ -run BuildToken -count=1`
- Red step: `TestBuildTokenIsReadOnly` fails with no route.
- Depends on: S2 (the same `server.go` pairing code; a behaviour edge kept: one file, one service); it is the provider side of C2's broken edge B2 → B1, built against `contract-build-token.md` and launched after C2 merges.
- Commit: `credsvc: a build's narrowed read token (B1, D2301)`.
- Reviewer tier: opus, mandatory independent review, one mutation check (a write scope must fail).
- Owner-run: no.

### B2: the catalog build uses the route and bakes several repositories
- Acceptance: the build step asks `buildtoken.Client` for one token over the entry's repositories; `imagebake.Decide` may bake several repositories into one entry; the token is a BuildKit secret mount used by the clone step only; an entry that serves a repository the project does not admit is refused.
- Files: `internal/backend/gcp/catalog.go`, `internal/cli/gitcred.go`, `internal/imagebake/imagebake.go`, `internal/imagebake/imagebake_test.go`, `internal/cli/image_build_catalog.go`.
- Interfaces: consumes C2's `Client`; produces the multi-repo bake plan.
- Tests: `TestDecideBakesSeveralRepos`, `TestCatalogBuildTokenIsASecretMount`, `TestCatalogBuildRefusesAnUnadmittedRepo`.
- Command: `go test ./internal/imagebake/ ./internal/backend/gcp/ ./internal/cli/ -run 'DecideBakes|CatalogBuildToken|CatalogBuildRefuses' -count=1`
- Red step: `TestDecideBakesSeveralRepos` fails because `Decide` bakes one repository (O3 (b)).
- Depends on: C2.
- Commit: `catalog: a project image bakes several repositories (B2, D2301)`.
- Reviewer tier: opus (credential in a build).
- Owner-run: no.

### I2: wire the build to the route
- Acceptance: the stub is deleted and a hermetic test builds a catalog entry over two repositories with a token from the service and bakes both.
- Files: `internal/buildtoken/stub.go` (deleted), `internal/cli/catalog_bake_e2e_test.go` (new).
- Interfaces: consumes B1, B2, C2.
- Tests: `TestCatalogBuildBakesTwoReposThroughTheService`.
- Command: `go test ./internal/cli/ -run CatalogBuildBakesTwoRepos -count=1`
- Red step: fails while the stub stands.
- Depends on: B1, B2, C2.
- Commit: `catalog: the build reaches the credential service (I2)`.
- Reviewer tier: opus.
- Owner-run: no.

### B3: the leak scan in the catalog build
- Acceptance: the catalog build runs the scan before the promote step and refuses an image that holds git config credentials, a `.netrc`, a credential-helper store or a token in the environment or in any layer; a clean image passes.
- Files: `images/scan.sh`, `images/scan_test.go`, `images/trivy-secret.yaml`, `internal/backend/gcp/catalog.go`.
- Interfaces: consumes the built image reference.
- Tests: `TestScanRefusesAnImageWithAGitCredential`, `TestScanRefusesANetrc`, `TestScanRefusesATokenInTheEnvironment`, `TestCatalogBuildStepsRunTheScanBeforePromote`.
- Command: `go test ./images/ ./internal/backend/gcp/ -run 'Scan|CatalogBuildSteps' -count=1`
- Red step: the scan tests fail on fixtures with each leftover.
- Depends on: I2 (the same `catalog.go`; one writer per level).
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

### M1: remove the per-repository jobs from the default Terraform path
- Acceptance: a repository that a deployed profile admits gets no workflow job, registry, build account or check job from `modules/repo`; adding or removing a repository needs no Terraform apply; a repository that opts out keeps the per-repository path.
- Files: `deploy/terraform/gcp/modules/repo/workflows.tf`, `deploy/terraform/gcp/modules/repo/registry.tf`, `deploy/terraform/gcp/modules/repo/check.tf`, `internal/infra/spec.go`, `internal/infra/spec_test.go`.
- Interfaces: consumes S5's profile; produces the reduced root.
- Tests: `TestRepoRootOmitsTheJobsForASharedRepo`, `TestAddingARepoNeedsNoApply`; `terraform fmt -check`.
- Command: `go test -tags terraform ./internal/infra/ -run 'RepoRootOmits|AddingARepo' -count=1`
- Red step: the first test fails because every workflow gets a job.
- Depends on: I1 (shared jobs work), S5 (the default profile exists).
- Commit: `infra: a shared repository has no jobs of its own (M1, spec Infrastructure)`.
- Reviewer tier: opus (Terraform, IAM).
- Owner-run: no.

### M2: `update` retires the per-repository jobs
- Acceptance: `update` retires a repository's own jobs once its structural precondition holds (admitted to a deployed profile, project image resolves, proxy on) and not before, with the `retire` of an orphaned job and its executions named in the in-flight line; an installation at the previous version converges in one `update`; a failed first shared run is repaired by running `update` again.
- Files: `internal/cli/init_retire.go`, `internal/infra/retire.go`, `internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md`, `internal/cli/init_retire_test.go`.
- Interfaces: consumes M1, S5, S6; produces the retirement.
- Tests: `TestUpdateRetiresTheRepoJobsOfAMigratedRepo`, `TestUpdateDoesNotRetireBeforeThePrecondition`, `TestUpdatePreviousVersionConvergesToSharedJobs`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'UpdateRetires|UpdateDoesNotRetire|UpdatePreviousVersionConverges|UpdateLeavesARunInFlight' -count=1`
- Red step: the first test fails: nothing retires a repository root.
- Depends on: M1 (what is removed), S5 (the profile), S6 (the proxy, and the `update_in_flight.go` rows: one writer per level).
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
- Files: `docs/releases/vX.Y.Z.md` (new; the release that carries it), `website/data/release-summaries.yaml`.
- Interfaces: none.
- Tests: the Docs filter and `npm run check` in `website/`.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: the release-notes docs test fails without the entry.
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

**Edges kept** (the dependent needs behaviour or the same file):
- S7 → I1, S8 → I1: they document and exercise what I1 wires.
- B1 → S2: one file, `credsvc/server.go`, and the same pairing code.
- B3 → I2, N1 → I1, N2 → I1, M2 → S6: one file each (`catalog.go`, `bitbucketproxy.go`, `runner.go`, `update_in_flight.go`) so one writer per level.
- M1 → I1, M1 → S5, M2 → M1: Terraform removal is meaningful only after shared jobs work and the default profile exists.
- M3 → S4, M4 → M2, M5 → M2 and B4 (one checklist file), N3 → N1/N2, B4 → B3: behaviour.

**Edges broken by contract**:
- S3 needed S2 for the pairing's refusal shape only: broken by **C1**, re-joined in **I1**.
- S4 needed S1 for the provider list only: broken by **C1**, re-joined in **I1**.
- B2 needed B1 for the request and response shape only: broken by **C2**, re-joined in **I2**.

The graph is in [implementation-plan.graph.json](implementation-plan.graph.json). The read of `fugaro plan graph` is reported under "The gain" below.

**Levels** (as `fugaro plan graph` reads them; tasks of a level run at once):
- L0: C1, C2, S1, S2, S5, S6 (S1 and S2 are the provider sides of a contract the graph draws no edge to; launch them after C1 merges, and B1 after C2)
- L1: S3, S4, B1, B2
- L2: I1, I2, M3
- L3: S7, S8, N1, N2, M1, B3
- L4: N3, M2, B4
- L5: M4, M5

**Critical path** (the read): C1 → S3 → I1 → M1 → M2 → M4. It is sequential because the retirement is meaningful only once shared jobs work and Terraform no longer emits the per-repository jobs, and the release notes close it; the owner-run checks follow (M5 waits on B4 for one checklist file).

**Shared-files table** (no level holds two writers of one file; the contract's own `stub.go` is deleted by its integration task):

| File | Tasks (by level) |
| --- | --- |
| `internal/credsvc/server.go` | S2 (L0), B1 (L1) |
| `internal/credsvc/bitbucketproxy.go` | S2 (L0), N1 (L3) |
| `internal/runner/runner.go` | S3 (L1), N2 (L3) |
| `internal/backend/gcp/catalog.go` | B2 (L1), B3 (L3) |
| `internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md` | S6 (L0), M2 (L4) |
| `docs/gcp-live-checklist.md` | S8 (L3), B4 (L4), M5 (L5) |
| `internal/cli/init_policy_migrate.go` | S5 (L0) |
| `internal/sharedjob/stub.go` | C1 (L0), I1 (L2) |
| `internal/buildtoken/stub.go` | C2 (L0), I2 (L2) |


**Contract changes anticipated**: R1 amends C1 if S2's review finds the pairing needs the workflow name (reaches S3 and S4, whose launched runs are followed up by a run that names `internal/sharedjob/sharedjob.go` by path); R2 amends C2 if B1's review finds a time-to-live cap belongs in the request (reaches B2).

**The gain** (the read): 3 edges broken, 13 kept, 6 tasks added; the weighted path goes from 15.5 to 15, which the CLI reports as an improvement. The gain is small on purpose: the dominant chain is the migration (M1 → M2), whose edges are behaviour edges, and the contracts mainly let the Bitbucket runner, launcher and deployer, and the build and the route, run side by side.

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
- **A task for each Terraform file of M1**: M1 touches five files and stays one task.
