# Image map keyed by provider: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped it are D3370–D3377, all as recommended. D3372 was amended after review and
confirmed.
- **D3370:** the key is `provider/owner/name`.
- **D3371:** the member is `repo_images` everywhere.
- **D3372:** an old `image_map` member is read as today until `update` rewrites it. An old-form key
  inside `repo_images` is refused.
- **D3373 and D3375:** a name at two providers, or at an unknown one, migrates to every provider it
  may be.
- **D3374:** it ships one release after L1.
- **D3376:** `update` removes `image_map` in the write that adds `repo_images`.
- **D3377:** `Source.Repo` is a follow-up.

## Conventions every part follows

- `CLAUDE.md` throughout, in particular:
  - the HARD RULE on `fugaro update`, safe with runs in flight;
  - Fugaro asks for nothing;
  - hermetic tests (`internal/gcpfake`, `t.TempDir()`), no secret-shaped literal;
  - "a field never changes meaning" (R4, R5);
  - comments explain why;
  - targeted tests, and `go test ./internal/cli/ -run Docs` for any document change.
- A help change regenerates `website/content/cli-reference.md` with `go run ./ee/clidoc` in the same
  task (F11).
- Boundaries:
  - **Always:**
    - spell a key with `projectpolicy.Canonical`;
    - read the map through T0's lookup, never by indexing a map by a bare name;
    - name a dropped key in one line.
  - **Ask first:** a policy version bump (R3 rules it out), or any new prompt.
  - **Never:**
    - write `repo_images` without a provider;
    - touch a run's records;
    - let `update` stop on a mapping question.
- Live checks are the owner's (Check 940).
- The project's rigor is `startup`, and decoupling is on (`planning.decoupling`, true).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The first `update` reads the old member before `Policy` rewrites it (F4) | U1's converge test, X1 |
| A run in flight is untouched | X1 (`TestUpdateLeavesARunInFlightRunning`, with the migration) |
| An old reader of the new node sees no map, never a wrong one | T0's decode test (an unknown-member node), S1 |
| A build token never covers a repository at the other provider | S1 |
| No key is written without a provider | T0's validator, U1, S2 |

## Tasks

### T0: the member, its type and the lookup
- Acceptance:
  - `projectpolicy.RepoImages` (member `repo_images`) round-trips through the node's escape;
  - `ValidRepoImages` refuses a key that is not `projectpolicy.Canonical`'s output, naming the fix;
  - `localcfg.Config.RepoImages` (yaml `repo_images`) and `RepoImagesEffective` keep the keep rule
    (D1140);
  - the shared config refuses `repo_images` (R7);
  - `catalog.ImageRequest` gains `Provider` and `RepoImages`. `mappedEntry` tries
    `repo_images[Canonical(Provider, repo)]`, then the old map by bare name (R2).

  Every existing caller still compiles and behaves the same.
- Files:
  - `internal/projectpolicy/repoimages.go` (new)
  - `internal/projectpolicy/repoimages_test.go` (new)
  - `internal/projectpolicy/policy.go`
  - `internal/localcfg/localcfg.go`
  - `internal/localcfg/shared.go`
  - `internal/catalog/select.go`
  - `docs/image-map-by-provider/contract.md` (new): what every reader may rely on
- Interfaces: produces the type, the validator, the effective reader and the two request fields
  every other task uses.
- Tests:
  - `TestRepoImagesRoundTrip`, `TestValidRepoImagesRefusesOldForm`;
  - `TestRepoImagesEffectiveKeepRule`, `TestSharedRefusesRepoImages`;
  - `TestMappedEntryByProvider`, `TestMappedEntryFallsBackToOldMap`.
- Command:
  `go test ./internal/projectpolicy/ ./internal/localcfg/ ./internal/catalog/ -run 'RepoImages|MappedEntry' -count=1`
- Red step: `TestMappedEntryByProvider` maps `github/acme/app` and `bitbucket/acme/app` to two
  entries. It fails while the lookup ignores the provider.
- Depends on: none.
- Commit: `image-map-by-provider T0: the repo_images member, its type and the lookup by provider`
- Reviewer tier: opus with an independent review (a wire format of the node).

### U1: update migrates the map, on the node and in the local config
- Acceptance:
  - the `Policy` stage publishes `repo_images` and removes `image_map` in the same write (D3376);
  - the local config is rewritten the same way;
  - providers come from R1's order (D3373, D3375). An unenrolled name is dropped with one line;
  - a second `update` changes nothing;
  - `project policy show --json` prints `repo_images` (R5).
- Files:
  - `internal/cli/repo_images_migrate.go` (new)
  - `internal/cli/repo_images_migrate_test.go` (new)
  - `internal/cli/project_cmd.go`
  - `internal/cli/init_policy_publish.go`
- Interfaces: consumes T0, and L5's reader (`repofiles.Read`) for step 3 of R1. Produces the
  migrated node and file.
- Tests:
  - `TestMigrateImageMapOneProvider`, `...TwoProviders`, `...UnknownProvider`, `...Unenrolled`;
  - `TestMigrateImageMapIdempotent`, `TestPolicyShowPrintsRepoImages`.
- Command:
  `go test ./internal/cli/ -run 'MigrateImageMap|PolicyShowPrintsRepoImages' -count=1`
- Red step: `...TwoProviders` expects two keys and fails before the migration exists.
- Depends on: T0 (the type).
- Commit: `image-map-by-provider U1: update moves image_map to repo_images, by provider`
- Reviewer tier: opus with an independent review (it writes the policy node).

### R1: launch and update's job images read by provider
- Acceptance: `run.go` (the selection and `mappedEntryName`), `infra/spec.go`
  (`repoCtx.imageRequest`), `infra/catalogcheck.go` and `init_policy_migrate.go` (the default-entry
  refusal and `firstPublishNodeOnlyMembers`) pass `Provider` and `RepoImages`. Two providers of one
  name get two entries.
- Files:
  - `internal/cli/run.go`
  - `internal/infra/spec.go`
  - `internal/infra/catalogcheck.go`
  - `internal/cli/init_policy_migrate.go`
  - their tests
- Interfaces: consumes T0, and `cloudEnv.repoProvider` or the job input's `Provider`.
- Tests: `TestLaunchImageByProvider`, `TestJobSpecImageByProvider`,
  `TestCatalogCheckInlineEntryByProvider`.
- Command:
  `go test ./internal/cli/ ./internal/infra/ -run 'ImageByProvider|InlineEntryByProvider' -count=1`
- Red step: a launch of the Bitbucket `acme/app` picks the GitHub key's entry.
- Depends on: T0 (the type).
- Commit: `image-map-by-provider R1: launch and update's job images read repo_images by provider`
- Reviewer tier: standard.

### R2a: the image commands read by provider
- Acceptance: `image_catalog.go` (`effectiveImageMap`, `publishedImageMap`, `catalogPointedAt`),
  `image_build_catalog.go` and `image_bake_note.go` read `repo_images` by provider.
- Files:
  - `internal/cli/image_catalog.go`
  - `internal/cli/image_build_catalog.go`
  - `internal/cli/image_bake_note.go`
  - `internal/cli/image_catalog_test.go`
- Tests: `TestCatalogPointedAtByProvider`, `TestBuildCatalogBakesByProvider`.
- Command: `go test ./internal/cli/ -run 'PointedAtByProvider|BakesByProvider' -count=1`
- Red step: a build bakes both repositories of one name into one entry.
- Depends on: T0 (the type).
- Commit: `image-map-by-provider R2a: the image commands read repo_images by provider`
- Reviewer tier: standard.

### R2b: image survey prints repo_images (version 2)
- Acceptance: `image survey --json` is version 2, with `setup.repo_images` in place of
  `setup.image_map` (R4). The text form prints the provider keys.
- Files:
  - `internal/cli/image_survey.go`
  - `schemas/image-survey.schema.json`
  - `internal/cli/image_survey_test.go`
- Tests: `TestImageSurveyRepoImagesV2`, and the schema test that already validates the document.
- Command: `go test ./internal/cli/ -run 'ImageSurvey' -count=1`
- Red step: the schema test fails against a version-1 document.
- Depends on: T0 (the type).
- Commit: `image-map-by-provider R2b: image survey --json version 2 prints repo_images`
- Reviewer tier: standard.

### R3: doctor, repos view and repos rm by provider
- Acceptance:
  - doctor's layer check (`workflowCatalogEntry`) and `repos view` read by provider;
  - doctor names a file holding both members (R2);
  - `project repos rm` removes the repository's key under its provider (R8).
- Files:
  - `internal/cli/doctor_layer.go`
  - `internal/cli/repos_view.go`
  - `internal/cli/project_repos_rm.go`
  - `internal/cli/repos_view_test.go`
  - `internal/cli/project_repos_rm_test.go`
- Tests: `TestReposViewImageByProvider`, `TestDoctorNamesBothMembers`,
  `TestReposRmRemovesProviderKey`.
- Command:
  `go test ./internal/cli/ -run 'ReposViewImageByProvider|DoctorNamesBothMembers|ReposRmRemovesProviderKey' -count=1`
- Red step: `rm` of the GitHub `acme/app` leaves its key.
- Depends on: T0 (the type).
- Commit: `image-map-by-provider R3: doctor, repos view and repos rm read repo_images by provider`
- Reviewer tier: standard.

### S1: the build token covers the request's provider only
- Acceptance: `buildEntryRepos` builds the entry's set from the `repo_images` keys of the request's
  provider. A repository mapped only at the other provider is refused by name (spec acceptance 4).
  While the node still has only `image_map`, it reads that as today (R2).
- Files: `internal/credsvc/buildtoken.go`, `internal/credsvc/buildtoken_test.go`
- Tests: `TestBuildTokenRefusesOtherProvidersKey`, `TestBuildTokenReadsOldMapUntilMigrated`.
- Command:
  `go test ./internal/credsvc/ -run 'BuildTokenRefusesOtherProvidersKey|BuildTokenReadsOldMapUntilMigrated' -count=1`
- Red step: a Bitbucket request for a repository mapped only at GitHub is granted.
- Depends on: T0 (the type).
- Commit: `image-map-by-provider S1: a build token covers the request's provider only`
- Reviewer tier: opus with an independent review (the credentials service).

### S2: image map set writes repo_images
- Acceptance:
  - `image map set acme/app go` writes `github/acme/app` (the provider from the local config);
  - `image map set bitbucket/acme/app go` is taken as written;
  - an unlisted `owner/name` is refused, naming the `provider/owner/name` form (R6);
  - the settings registry row is `repo_images`;
  - the help and `cli-reference.md` are regenerated.
- Files:
  - `internal/cli/image_map.go`
  - `internal/cli/image_map_test.go`
  - `internal/settings/registry.go`
  - `schemas/settings.schema.json`
  - `website/content/cli-reference.md`
- Tests: `TestImageMapSetWritesProviderKey`, `TestImageMapSetRefusesUnlisted`, and
  `TestDocsWebsiteCLIRef`.
- Command:
  `go test ./internal/cli/ -run 'ImageMapSet|DocsWebsiteCLIRef' -count=1 && go test ./internal/settings/ -count=1`
- Red step: `...WritesProviderKey` reads `acme/app` back.
- Depends on: T0 (the type).
- Commit: `image-map-by-provider S2: image map set writes repo_images by provider`
- Reviewer tier: standard.

### D1: skills and documents
- Acceptance: the `images` and `setup` skills, `docs/project-layer.md`,
  `docs/gcp-live-checklist.md` and `docs/how-to/offboarding.md` name `repo_images` and its
  `provider/owner/name` keys. The `plugin/` and Docs tests pass.
- Files:
  - `plugin/skills/images/SKILL.md`
  - `plugin/skills/setup/SKILL.md`
  - `docs/project-layer.md`
  - `docs/gcp-live-checklist.md`
  - `docs/how-to/offboarding.md`
- Tests: `plugin/` (`images_skill_test.go`), and `go test ./internal/cli/ -run Docs`.
- Command: `go test ./plugin/ -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents). A pinned sentence that changes is changed in its test together.
- Depends on: T0 (the names, from `contract.md`).
- Commit: `image-map-by-provider D1: the skills and documents name repo_images`
- Reviewer tier: opus with an independent review (plugin skills).

### K1: Check 940, as a script
- Acceptance: `scripts/live-checks/check940.sh` reads, without writing:
  - the project's node, showing `repo_images` present and `image_map` absent;
  - `image survey --json` version 2;
  - a GitHub and a Bitbucket repository's job image.

  It prints PASS or FAIL per step. Its shell test runs against fixtures.
- Files: `scripts/live-checks/check940.sh` (new), `scripts/live-checks/check940_test.sh` (new)
- Tests: `check940_test.sh`.
- Command: `bash scripts/live-checks/check940_test.sh`
- Red step: the test fails against a fixture node that still holds `image_map`.
- Depends on: T0 (the member's name).
- Commit: `image-map-by-provider K1: Check 940 as a script`
- Reviewer tier: standard.
- **Owner-run:** after the release that carries U1, run `fugaro update`, then
  `scripts/live-checks/check940.sh --project fugaro`.

### X1: end to end
- Acceptance: one test covers a project with a GitHub and a Bitbucket `acme/app` mapped to two
  entries: launch, `update`'s job images, doctor and `repos view` agree (spec acceptance 1). An
  installation at the previous release converges in one `update` (acceptance 2 and 7).
  `TestUpdateLeavesARunInFlightRunning` passes with the migration in the run.
- Files: `internal/cli/repo_images_e2e_test.go` (new)
- Tests: `TestRepoImagesEndToEnd`, `TestRepoImagesConvergesFromPreviousRelease`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command:
  `go test ./internal/cli/ -run 'RepoImagesEndToEnd|RepoImagesConvergesFromPreviousRelease|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: none. It is written after the readers, and fails if any reader still indexes by bare
  name.
- Depends on: U1, R1, R2a and R3 (behaviour: it exercises them together).
- Commit: `image-map-by-provider X1: repo_images end to end, and the converge from the release before`
- Reviewer tier: standard.

## Parallel execution

| Level | Tasks |
| --- | --- |
| 0 | T0 |
| 1 | U1, R1, R2a, R2b, R3, S1, S2, D1, K1 |
| 2 | X1 |

- **Critical path, as the read reports it:** T0 → R1 → X1, three tasks. T0 is the bottleneck. T0 → U1 → X1 is as long.
- **What must be sequential:**
  - T0 first, because every task compiles against its type and fields.
  - X1 last, because it exercises the migration and the readers together.

### Shared files

No level holds two writers of one file.

| File | Writer |
| --- | --- |
| `internal/projectpolicy/policy.go`, `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go`, `internal/catalog/select.go` | T0 |
| `internal/cli/project_cmd.go`, `internal/cli/init_policy_publish.go` | U1 |
| `internal/cli/run.go`, `internal/cli/init_policy_migrate.go`, `internal/infra/spec.go`, `internal/infra/catalogcheck.go` | R1 |
| `internal/cli/image_catalog.go`, `internal/cli/image_build_catalog.go`, `internal/cli/image_bake_note.go` | R2a |
| `internal/cli/image_survey.go`, `schemas/image-survey.schema.json` | R2b |
| `internal/cli/doctor_layer.go`, `internal/cli/repos_view.go`, `internal/cli/project_repos_rm.go` | R3 |
| `internal/credsvc/buildtoken.go` | S1 |
| `internal/cli/image_map.go`, `internal/settings/registry.go`, `schemas/settings.schema.json`, `website/content/cli-reference.md` | S2 |
| the five documents | D1 |

**Against other plans:**
- L5's U5 writes `init_policy_migrate.go`, and so does R1. Run R1 after U5 merges, or rebase.
- L8 writes none of these files.

## The dependencies broken and kept

The contract-first pass ran, with decoupling on. With the shared shape in its own task (T0), no
edge between two other tasks carries only an interface:
- the readers need T0's type, not U1's migrated data, because their tests build nodes themselves;
- U1 needs T0's type, not any reader.

T0 is real code (a type, a validator and a lookup), not a stub, so it is an ordinary task, and no
integration task is needed. These edges are kept:
- **T0 → each level-1 task:** each compiles against T0's type and fields.
- **U1, R1, R2a, R3 → X1:** behaviour. X1 exercises them together.

The read reports no decoupling, so there is no gain line.

**The estimate, as `fugaro plan graph` printed it.** It is informational and reserves no budget:
`ready in 210–840 min, no history yet for 6 tasks (11 tasks; tiers for 5, seeds for 6; from the cached history of 2026-11-22 07:25 UTC)`

## Checks

- **Check 940** (owner-run, K1): after the release that carries U1. Checks 941–949 are reserved
  and unused.

## After-deploy steps

None beyond `fugaro update`, which does the migration. Each task's release-notes line under
`### For operators` reads "run `fugaro update`".

## Considered and declined

- **A contract with a stubbed migration.** Nothing waits on the migration's behaviour, so a stub
  would break no edge.
- **Bumping `projectpolicy.Version`** (R3): it would stop runs in flight on an older credentials
  service.
- **`Source.Repo`** (D3377): a follow-up (O2).
