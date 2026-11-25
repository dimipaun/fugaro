# Image map keyed by provider: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped it are D3370–D3377, all as recommended. D3372 was amended after review and
confirmed. O3 asks the owner to confirm D3376 now that its cost is stated.
- **D3370:** the key is `provider/owner/name`, and its provider is one Fugaro serves (R10).
- **D3371:** the member is `repo_images` everywhere.
- **D3372:** an old `image_map` member is read as today until `update` rewrites it. This is per
  member (R2). An old-form key inside `repo_images` is refused.
- **D3373 and D3375:** a name at two providers, or at an unknown one, migrates to every provider it
  may be. A key this installation does not serve at that provider is skipped (R9).
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
  task (F11). A change to `website/content/` also runs `npm run check` in `website/`.
- A test or golden that fixtures the old member changes in the task that changes the code it pins
  (R12).
- Messages say "the repository-to-image map (`repo_images`)", never "repo images" (R11).
- Boundaries:
  - **Always:**
    - spell a key with `projectpolicy.Canonical`, holding its provider to `config.Providers`;
    - read the map through T0b's effective reader, never by indexing a map by a bare name;
    - name a dropped key in one line;
    - skip a key that is not served (R9).
  - **Ask first:** a policy version bump (R3 rules it out), or any new prompt.
  - **Never:**
    - write `repo_images` without a provider;
    - write `image_map`;
    - touch a run's records;
    - let `update` stop on a mapping question.
- Live checks are the owner's (Check 940).
- The project's rigor is `startup`, and decoupling is on (`planning.decoupling`, true).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The first `update` reads the old member before `Policy` rewrites it (F4) | U1b's converge test, X1 |
| A run in flight is untouched | X1 (`TestUpdateLeavesARunInFlightRunning`, with the migration) |
| An old credentials service reading the new node refuses a build by name; it never grants a wrong one | T0's decode test (a node with an unknown member), S1 |
| A build token never covers a repository at the other provider | S1 |
| An unserved key poisons no entry (R9) | S1, R2c, R1 (`catalogcheck`) |
| No key is written without a served provider | T0's validator, U1a, S2, S3 |
| The old member never comes back once `repo_images` exists | T0b (`TestEffectivePrefersAnyRepoImages`), S3 |

## Tasks

### T0: the member, its type and the request's fields
- Acceptance:
  - `projectpolicy.RepoImages` (member `repo_images`) round-trips through the node's escape;
  - `ValidRepoImages` refuses a key whose provider is not in `config.Providers` (R10), or whose
    path is not canonical, and names the fix;
  - `knownTopLevelKeys` lists `repo_images`;
  - `catalog.ImageRequest` gains `Provider`, `RepoImages` and `LegacyMap`. `mappedEntry` reads
    `RepoImages[Canonical(Provider, repo)]` when `RepoImages` is set, else `LegacyMap[repo]` (R2);
  - `projectpolicy/issuesync.go`'s comments name `repo_images`.

  Every existing caller still compiles and behaves the same: `ImageMap` stays as an alias of
  `LegacyMap` until X1.
- Files:
  - `internal/projectpolicy/repoimages.go` (new)
  - `internal/projectpolicy/repoimages_test.go` (new)
  - `internal/projectpolicy/policy.go`
  - `internal/projectpolicy/issuesync.go`
  - `internal/catalog/select.go`
  - `internal/catalog/select_test.go`
  - `docs/image-map-by-provider/contract.md` (new): what every reader may rely on
- Tests:
  - `TestRepoImagesRoundTrip`;
  - `TestValidRepoImagesRefusesOldForm`, `TestValidRepoImagesRefusesUnservedProvider`;
  - `TestPolicyDecodeToleratesRepoImagesForOldReader`;
  - `TestMappedEntryByProvider`, `TestMappedEntryLegacyMap`.
- Command:
  `go test ./internal/projectpolicy/ ./internal/catalog/ -run 'RepoImages|MappedEntry' -count=1`
- Red step: `TestMappedEntryByProvider` maps `github/acme/app` and `bitbucket/acme/app` to two
  entries. It fails while the lookup ignores the provider.
- Depends on: none.
- Commit: `image-map-by-provider T0: the repo_images member, its type and the lookup by provider`
- Reviewer tier: opus with an independent review (a wire format of the node).

### T0b: the local config, the effective reader and the shared config
- Acceptance:
  - `localcfg.Config.RepoImages` (yaml `repo_images`) is validated by `ValidRepoImages`;
  - `localcfg.Config.ImageMap` stays, decoded, so `update` can migrate it;
  - the effective reader returns `repo_images` from the local config, else from the node, else
    the legacy map (R2), and says which;
  - both shared-config refusals name `repo_images` (R7).
- Files:
  - `internal/localcfg/localcfg.go`
  - `internal/localcfg/localcfg_test.go`
  - `internal/localcfg/shared.go`
  - `internal/cli/sharedcfg.go`
  - `internal/cli/sharedcfg_test.go`
  - `internal/cli/testdata/writer_gaps.golden`
- Tests: `TestEffectivePrefersAnyRepoImages`, `TestLocalRepoImagesValidated`,
  `TestSharedRefusesRepoImages`, and the `writer_gaps` golden test.
- Command:
  `go test ./internal/localcfg/ -run 'RepoImages|EffectivePrefers' -count=1 && go test ./internal/cli/ -run 'SharedRefusesRepoImages|WriterGaps|Shared' -count=1`
- Red step: `TestEffectivePrefersAnyRepoImages` (a local `image_map` and a node `repo_images`)
  expects the node's. It fails under today's keep rule.
- Depends on: T0 (`ValidRepoImages`; `localcfg` imports `projectpolicy`).
- Commit: `image-map-by-provider T0b: repo_images in the local config, and the effective reader`
- Reviewer tier: standard.

### U1a: the migration function and the local config's rewrite
- Acceptance:
  - `migrateImageMap` turns the old map into `repo_images` by R1's order: two providers give two
    keys (D3373), an unknown provider gives every provider (D3375), and an unenrolled name is
    dropped with one line;
  - `update` rewrites the local config the same way, with the file's backup (`userSave`);
  - a second run changes nothing.
- Files:
  - `internal/cli/repo_images_migrate.go` (new)
  - `internal/cli/repo_images_migrate_test.go` (new)
- Tests: `TestMigrateImageMapOneProvider`, `...TwoProviders`, `...UnknownProvider`,
  `...Unenrolled`, `...Idempotent`, `TestMigrateLocalConfigRewrites`.
- Command: `go test ./internal/cli/ -run 'MigrateImageMap|MigrateLocalConfig' -count=1`
- Red step: `...TwoProviders` expects two keys, and fails before the function exists.
- Depends on: T0b (the local config's member and the effective reader).
- Commit: `image-map-by-provider U1a: the migration from image_map to repo_images`
- Reviewer tier: opus with an independent review (it rewrites the owner's config).

### U1b: the Policy stage publishes repo_images
- Acceptance:
  - the `Policy` stage counts an `image_map` on the node as drift (`policyMigrationFor`,
    `policyMigrateStage.Check`);
  - it publishes `repo_images` and removes `image_map` in one write (D3376);
  - `firstPublishNodeOnlyMembers` and the default-entry refusal read the effective map;
  - `project policy show --json` prints `repo_images` (R5);
  - an installation whose only drift is `image_map` converges in one `update`.
- Files:
  - `internal/cli/init_policy_migrate.go`
  - `internal/cli/init_policy_migrate_test.go`
  - `internal/cli/init_policy_publish.go`
  - `internal/cli/init_policy_publish_test.go`
  - `internal/cli/project_cmd.go`
  - `internal/cli/update_converge_test.go`
- Tests: `TestPolicyStageMigratesImageMap`, `TestPolicyStageImageMapIsDrift`,
  `TestPolicyShowPrintsRepoImages`, and `update_converge_test.go`'s map assertion, moved to
  `repo_images`.
- Command:
  `go test ./internal/cli/ -run 'PolicyStageMigratesImageMap|PolicyStageImageMapIsDrift|PolicyShowPrintsRepoImages|UpdateConverge' -count=1`
- Red step: `TestPolicyStageImageMapIsDrift`: a node whose only drift is `image_map` reads as done.
- Depends on: U1a (behaviour: the stage calls the migration).
- Commit: `image-map-by-provider U1b: the Policy stage publishes repo_images and removes image_map`
- Reviewer tier: opus with an independent review (it writes the policy node).

### R1: launch and update's job images read by provider
- Acceptance:
  - `run.go` (the selection and `mappedEntryName`) and `infra/spec.go` (`repoCtx.imageRequest`)
    pass `Provider` and the effective map;
  - `infra/catalogcheck.go` picks an inline entry's rebuild account from served keys only (R9);
  - `infra/readiness.go`'s fix text names `repo_images`;
  - two providers of one name get two entries.
- Files:
  - `internal/cli/run.go`
  - `internal/infra/spec.go`
  - `internal/infra/catalogcheck.go`
  - `internal/infra/readiness.go`
  - `internal/infra/catalogcheck_test.go`
  - `internal/cli/update_jobimages_test.go`
- Tests: `TestLaunchImageByProvider` (in `update_jobimages_test.go`),
  `TestCatalogCheckInlineEntryByProvider`, `TestCatalogCheckSkipsUnservedKey`.
- Command:
  `go test ./internal/cli/ -run 'ImageByProvider|JobImages' -count=1 && go test ./internal/infra/ -run 'CatalogCheck' -count=1`
- Red step: a launch of the Bitbucket `acme/app` picks the GitHub key's entry.
- Depends on: T0b (the effective reader).
- Commit: `image-map-by-provider R1: launch and update's job images read repo_images by provider`
- Reviewer tier: standard.

### R2a: the image catalog commands read by provider
- Acceptance: `image_catalog.go` (`effectiveImageMap`, `publishedImageMap`, `catalogPointedAt`)
  and `image_bake_note.go` read the effective map by provider.
- Files:
  - `internal/cli/image_catalog.go`
  - `internal/cli/image_bake_note.go`
  - `internal/cli/image_catalog_test.go`
  - `internal/cli/image_refresh_test.go`
- Tests: `TestCatalogPointedAtByProvider`, `TestBakeNoteByProvider`, and the moved fixtures.
- Command: `go test ./internal/cli/ -run 'PointedAtByProvider|BakeNoteByProvider|ImageRefresh' -count=1`
- Red step: `catalogPointedAt` names both repositories of one name.
- Depends on: T0b.
- Commit: `image-map-by-provider R2a: the image catalog commands read repo_images by provider`
- Reviewer tier: standard.

### R2c: the catalog build and the bake read by provider
- Acceptance:
  - `image_build_catalog.go` passes provider keys to `imagebake`;
  - `imagebake.Decide` and `entryRepos` read `Map` by `provider/owner/name` and skip an unserved
    key (R9);
  - `backend/gcp/catalog.go`'s comments cite `projectpolicy.ValidRepoImages`.
- Files:
  - `internal/cli/image_build_catalog.go`
  - `internal/cli/image_build_catalog_test.go`
  - `internal/imagebake/imagebake.go`
  - `internal/imagebake/imagebake_test.go`
  - `internal/cli/catalog_bake_e2e_test.go`
  - `internal/backend/gcp/catalog.go`
- Tests: `TestBuildCatalogBakesByProvider`, `TestDecideByProviderKey`,
  `TestEntryReposSkipsUnservedKey`, and `catalog_bake_e2e_test.go`.
- Command:
  `go test ./internal/imagebake/ -count=1 && go test ./internal/cli/ -run 'BakesByProvider|CatalogBake' -count=1`
- Red step: `TestEntryReposSkipsUnservedKey`: a `bitbucket/acme/app` key with no such repository
  refuses the whole bake.
- Depends on: T0b.
- Commit: `image-map-by-provider R2c: the catalog build and the bake read repo_images by provider`
- Reviewer tier: standard.

### R2b: image survey prints repo_images (version 2)
- Acceptance:
  - `image survey --json` is version 2, with `setup.repo_images` in place of `setup.image_map`
    (R4), and the text form prints the provider keys;
  - `check-500.sh` and `check-581.sh` read `setup.repo_images`;
  - `skills_test.go`'s field list names it.
- Files:
  - `internal/cli/image_survey.go`
  - `internal/cli/image_survey_test.go`
  - `schemas/image-survey.schema.json`
  - `internal/cli/skills_test.go`
  - `scripts/live-checks/check-500.sh`
  - `scripts/live-checks/check-581.sh`
- Tests: `TestSurveyJSONMatchesSchema`, `TestImageSurveyRepoImagesV2`, `schemas/schemas_test.go`,
  `TestLiveCheckScripts`.
- Command:
  `go test ./internal/cli/ -run 'Survey|Skills' -count=1 && go test ./schemas/ -count=1 && go test ./scripts -run TestLiveCheckScripts -count=1`
- Red step: `TestSurveyJSONMatchesSchema` fails against a version-1 document.
- Depends on: T0b.
- Commit: `image-map-by-provider R2b: image survey --json version 2 prints repo_images`
- Reviewer tier: standard.

### R3: doctor, repos view and repos rm by provider
- Acceptance:
  - both of doctor's map readers (`doctor_layer.go`, online and offline) and `repos view` read by
    provider;
  - doctor names a file that holds both members (R2);
  - `project repos rm` removes the repository's key under its provider (R8).
- Files:
  - `internal/cli/doctor_layer.go`
  - `internal/cli/doctor_layer_test.go`
  - `internal/cli/repos_view.go`
  - `internal/cli/repos_view_test.go`
  - `internal/cli/project_repos_rm.go`
  - `internal/cli/project_repos_rm_test.go`
- Tests: `TestReposViewImageByProvider`, `TestDoctorNamesBothMembers`,
  `TestReposRmRemovesProviderKey`.
- Command:
  `go test ./internal/cli/ -run 'ReposViewImageByProvider|DoctorNamesBothMembers|ReposRmRemovesProviderKey' -count=1`
- Red step: `rm` of the GitHub `acme/app` leaves its key.
- Depends on: T0b.
- Commit: `image-map-by-provider R3: doctor, repos view and repos rm read repo_images by provider`
- Reviewer tier: standard.

### S1: the build token covers the request's provider only
- Acceptance:
  - `buildEntryRepos` builds the entry's set from the `repo_images` keys of the request's
    provider, skipping an unserved key (R9);
  - a repository mapped only at the other provider is refused by name (spec acceptance 4);
  - while the node holds only `image_map`, it reads that as today (R2).
- Files: `internal/credsvc/buildtoken.go`, `internal/credsvc/buildtoken_test.go`
- Tests: `TestBuildTokenRefusesOtherProvidersKey`, `TestBuildTokenSkipsUnservedKey`,
  `TestBuildTokenReadsOldMapUntilMigrated`.
- Command: `go test ./internal/credsvc/ -run 'BuildToken' -count=1`
- Red step: a Bitbucket request for a repository mapped only at GitHub is granted.
- Depends on: T0 (the node's member; `credsvc` reads no local config).
- Commit: `image-map-by-provider S1: a build token covers the request's provider only`
- Reviewer tier: opus with an independent review (the credentials service).

### S2: image map set writes repo_images
- Acceptance:
  - `image map set acme/app go` writes `github/acme/app`, with the provider from `repoProvider`;
  - `image map set bitbucket/acme/app go` is taken as written;
  - an argument whose provider is unknown is refused, naming the `provider/owner/name` form (R6);
  - the settings registry row is `repo_images`;
  - the help, `cli-reference.md` and `next_step_allow.golden` are regenerated.
- Files:
  - `internal/cli/image_map.go`
  - `internal/cli/image_map_test.go`
  - `internal/settings/registry.go`
  - `schemas/settings.schema.json`
  - `website/content/cli-reference.md`
  - `internal/cli/testdata/next_step_allow.golden`
- Tests: `TestImageMapSetWritesProviderKey`, `TestImageMapSetRefusesUnknownProvider`,
  `TestCliReferenceNamesEveryCommandAndFlag`, `TestCliReferenceInventsNothing`, and the
  `next_step_allow` golden test.
- Command:
  `go test ./internal/cli/ -run 'ImageMapSet|CliReference|NextStep' -count=1 && go test ./internal/settings/ ./schemas/ -count=1 && (cd website && npm run check)`
- Red step: `...WritesProviderKey` reads `acme/app` back.
- Depends on: T0b.
- Commit: `image-map-by-provider S2: image map set writes repo_images by provider`
- Reviewer tier: standard.

### S3: project repos add writes repo_images
- Acceptance: `project repos add` (`enrolImageMap`, `reposEnrolmentImage`) writes the new
  repository's key under its provider in `repo_images`, and never writes `image_map` (F12).
- Files:
  - `internal/cli/project_enroll_converge.go`
  - `internal/cli/project_enroll_converge_test.go`
- Tests: `TestReposAddWritesProviderKey`, `TestReposAddNeverWritesImageMap`.
- Command: `go test ./internal/cli/ -run 'ReposAdd|EnrollConverge' -count=1`
- Red step: `TestReposAddNeverWritesImageMap` finds `image_map` in the file.
- Depends on: T0b.
- Commit: `image-map-by-provider S3: repos add writes repo_images`
- Reviewer tier: standard.

### D1a: the skills
- Acceptance: the `images` and `setup` skills name `repo_images`, its `provider/owner/name` keys
  and survey version 2. `images_skill_test.go`'s literal moves with them, and the skills lint
  passes.
- Files:
  - `plugin/skills/images/SKILL.md`
  - `plugin/skills/setup/SKILL.md`
  - `plugin/images_skill_test.go`
- Tests: `go test ./plugin/`, and the skills lint in `internal/cli/skills_test.go`.
- Command: `go test ./plugin/ -count=1 && go test ./internal/cli/ -run 'Skills' -count=1`
- Red step: none (documents). A pinned sentence changes together with its test.
- Depends on:
  - R2b: the skills lint fails if a skill names `setup.repo_images` before the field exists;
  - S2: the skills name the command as its help now reads.
- Commit: `image-map-by-provider D1a: the skills name repo_images`
- Reviewer tier: opus with an independent review (plugin skills).

### D1b: the documents and the security pages
- Acceptance:
  - `docs/project-layer.md`, `docs/gcp-live-checklist.md` (with Check 940's row),
    `docs/how-to/offboarding.md`, `SECURITY.md` and `website/content/security.md` name
    `repo_images`;
  - the Docs tests and the website check pass.
- Files:
  - `docs/project-layer.md`
  - `docs/gcp-live-checklist.md`
  - `docs/how-to/offboarding.md`
  - `SECURITY.md`
  - `website/content/security.md`
- Tests: `go test ./internal/cli/ -run Docs`, and `npm run check` in `website/`.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`
- Red step: none (documents).
- Depends on: S2. Each document names the command as its help now reads.
- Commit: `image-map-by-provider D1b: the documents and security pages name repo_images`
- Reviewer tier: opus with an independent review (`SECURITY.md`).

### K1: Check 940, as a script
- Acceptance: `scripts/live-checks/check940.sh` reads, without writing:
  - the project's node, showing `repo_images` present and `image_map` absent;
  - `image survey --json` at version 2;
  - one job image per provider the project has. With no Bitbucket repository, it skips that step
    with a line (V3).

  It prints PASS or FAIL per step, and is registered in `liveCheckScripts`. `check196.sh` and its
  steps read `repo_images`, else `image_map`.
- Files:
  - `scripts/live-checks/check940.sh` (new)
  - `scripts/live-checks/check940_test.sh` (new)
  - `scripts/live_checks_test.go`
  - `scripts/live-checks/check196.sh`
  - `docs/secrets-brokering/check196-steps.md`
- Tests: `TestLiveCheckScripts`.
- Command:
  `go test ./scripts -run TestLiveCheckScripts -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `check940_test.sh` fails against a fixture node that still holds `image_map`.
- Depends on: none. It needs only the member's name, which D3371 fixes.
- Commit: `image-map-by-provider K1: Check 940 as a script`
- Reviewer tier: standard.
- **Owner-run:** after the release that carries U1b, run `fugaro update`, then
  `scripts/live-checks/check940.sh --project fugaro`.

### X1: end to end
- Acceptance:
  - one test covers a project with a GitHub and a Bitbucket `acme/app` mapped to two entries:
    launch, `update`'s job images, the bake, doctor and `repos view` agree (spec acceptance 1);
  - an installation at the previous release converges in one `update` (acceptance 2 and 7);
  - `TestUpdateLeavesARunInFlightRunning` passes with the migration in the run;
  - `catalog.ImageRequest.ImageMap`, the alias T0 kept, is removed.
- Files:
  - `internal/cli/repo_images_e2e_test.go` (new)
  - `internal/cli/update_in_flight_test.go`
  - `internal/catalog/select.go`
- Tests: `TestRepoImagesEndToEnd`, `TestRepoImagesConvergesFromPreviousRelease`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command:
  `go test ./internal/cli/ -run 'RepoImagesEndToEnd|RepoImagesConvergesFromPreviousRelease|TestUpdateLeavesARunInFlightRunning' -count=1 && go build ./...`
- Red step: none. It is written after the readers, and fails if any reader still indexes by bare
  name.
- Depends on: U1b, R1, R2a, R2c, R3 and S3 (behaviour: it exercises them together).
- Commit: `image-map-by-provider X1: repo_images end to end, and the converge from the release before`
- Reviewer tier: standard.

## Parallel execution

| Level | Tasks |
| --- | --- |
| 0 | T0, K1 |
| 1 | T0b, S1 |
| 2 | U1a, R1, R2a, R2b, R2c, R3, S2, S3 |
| 3 | U1b, D1a, D1b |
| 4 | X1 |

- **Critical path, as the read reports it:** T0 → T0b → U1a → U1b → X1, five tasks. T0 is the bottleneck.
- **What must be sequential:**
  - T0, then T0b: `localcfg` validates with T0's validator.
  - U1a, then U1b: the stage calls the migration.
  - D1a after R2b and S2: the skills lint, and the help text.
  - X1 last.

### Shared files

No level holds two writers of one file. X1 writes `select.go` after T0 (levels 0 and 4).

| File | Writer |
| --- | --- |
| `internal/projectpolicy/policy.go`, `issuesync.go`; `internal/catalog/select.go`, `select_test.go` | T0 (X1 at level 4 removes the alias) |
| `internal/localcfg/localcfg.go`, `shared.go`; `internal/cli/sharedcfg.go`, `sharedcfg_test.go`, `testdata/writer_gaps.golden` | T0b |
| `internal/cli/repo_images_migrate.go` | U1a |
| `internal/cli/init_policy_migrate.go`, `init_policy_publish.go`, `project_cmd.go`, `update_converge_test.go` | U1b |
| `internal/cli/run.go`, `update_jobimages_test.go`; `internal/infra/spec.go`, `catalogcheck.go`, `readiness.go` | R1 |
| `internal/cli/image_catalog.go`, `image_bake_note.go`, `image_refresh_test.go` | R2a |
| `internal/cli/image_build_catalog.go`, `catalog_bake_e2e_test.go`; `internal/imagebake/imagebake.go`; `internal/backend/gcp/catalog.go` | R2c |
| `internal/cli/image_survey.go`, `skills_test.go`; `schemas/image-survey.schema.json`; `scripts/live-checks/check-500.sh`, `check-581.sh` | R2b |
| `internal/cli/doctor_layer.go`, `repos_view.go`, `project_repos_rm.go` | R3 |
| `internal/credsvc/buildtoken.go` | S1 |
| `internal/cli/image_map.go`, `testdata/next_step_allow.golden`; `internal/settings/registry.go`; `schemas/settings.schema.json`; `website/content/cli-reference.md` | S2 |
| `internal/cli/project_enroll_converge.go` | S3 |
| `plugin/skills/images/SKILL.md`, `plugin/skills/setup/SKILL.md`, `plugin/images_skill_test.go` | D1a |
| `docs/project-layer.md`, `docs/gcp-live-checklist.md`, `docs/how-to/offboarding.md`, `SECURITY.md`, `website/content/security.md` | D1b |
| `scripts/live-checks/check940*.sh`, `check196.sh`, `scripts/live_checks_test.go`, `docs/secrets-brokering/check196-steps.md` | K1 |
| `internal/cli/update_in_flight_test.go` | X1 |

### Launch notes: files other plans write (F20)

L11 ships one release after L1 (D3374), so L5 and L8 mostly merge first. A task launches after the
other plan's task that shares its file has merged, or it rebases:

| L11 task | Waits for |
| --- | --- |
| U1b | L5 U5 (`init_policy_migrate.go`), L8 I2 (`update_converge_test.go`) |
| R1 | L8 PR2 and XB2 (`run.go`) |
| S2 | L8 XB3 and AB1 (`registry.go`, `settings.schema.json`), L8 S4 and RG3, L5 U3b, U9b and U10 (`cli-reference.md`) |
| S3 | L5 U9a (`project_enroll_converge.go`) |
| D1a | L8 D2 (`setup/SKILL.md`) |
| D1b | L8 S0, XB1 and D1 (`project-layer.md`), L8 CK2 and CK3, L5 U7b (`gcp-live-checklist.md`) |
| X1 | L5 I1 and L8 I2 (`update_in_flight_test.go`) |

## The dependencies broken and kept

The contract-first pass ran, with decoupling on. With the shared shape in its own tasks (T0 and
T0b), no edge between two other tasks carries only an interface:
- the readers need T0b's effective reader, not U1's migrated data, because their tests build nodes
  and files themselves;
- U1a needs T0b's types, not any reader.

T0 and T0b are real code (types, a validator and the lookup), not stubs, so they are ordinary
tasks, and no integration task is needed. These edges are kept:
- **T0 → T0b, S1, and T0b → the level-2 tasks:** each compiles against their types and readers.
- **U1a → U1b:** behaviour. The stage calls the migration.
- **R2b and S2 → D1a, and S2 → D1b:** a lint and the help text.
- **U1b, R1, R2a, R2c, R3, S3 → X1:** behaviour. X1 exercises them together.

K1 depends on nothing: a script needs only the member's name. The read reports no decoupling, so
there is no gain line.

**The estimate, as `fugaro plan graph` printed it.** It is informational and reserves no budget:
`ready in 300–1200 min, no history yet for 8 tasks (16 tasks; tiers for 8, seeds for 8; from the cached history of 2026-11-22 07:25 UTC)`

## Checks

- **Check 940** (owner-run, K1): after the release that carries U1b. Checks 941–949 are reserved
  and unused.

## After-deploy steps

None beyond `fugaro update`, which does the migration. Each task's release-notes line under
`### For operators` reads "run `fugaro update`". The release notes also say that the release after
L11 drops the read of the old member (O1).

## Considered and declined

- **A contract with a stubbed migration.** Nothing waits on the migration's behaviour, so a stub
  would break no edge.
- **One T0.** It would have touched about ten files. It is split at the package line that
  `localcfg`'s import of `projectpolicy` already draws.
- **Bumping `projectpolicy.Version`** (R3): it would stop runs in flight on an older credentials
  service.
- **`Source.Repo`** (D3377): a follow-up (O2).
