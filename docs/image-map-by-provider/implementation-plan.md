# Image map keyed by provider: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped it are D3370–D3377, all as recommended. D3372 was amended after review and
confirmed. One open tension waits on the owner: D3376 against its stated cost (O3).
- **D3370:** the key is `provider/owner/name`. A writer holds the provider to the ones Fugaro
  serves; a reader holds it only to its shape (R10).
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
- A test, golden or script that pins the old member's output or a message changes in the task that
  changes that code. A test that only uses `image_map` as an input stays (R12).
- Messages say "the repository-to-image map (`repo_images`)", never "repo images" (R11).
- Boundaries:
  - **Always:**
    - turn a repository into a key with U1a's `repoImageKey`, and write only providers in
      `config.Providers` (R10);
    - read the map through T0's `EffectiveRepoImages` and T0b's effective reader, never by
      indexing a map by a bare name;
    - migrate a legacy map before writing `repo_images` (R13);
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
| An older reader of the new node sees no map. A node with a provider this release does not know still decodes (R10) | T0's decode tests, which simulate the older reader with a node it does not know |
| A build token never covers a repository at the other provider | S1 |
| An unserved key poisons no entry (R9) | S1, R2c, R1 (`catalogcheck`) |
| No key is written without a served provider | U1a (`repoImageKey`), S2, S3, U1b (the publisher) |
| No mapping is lost to a write made before `update` (R13) | S2, S3, R3 |
| The old member never comes back once `repo_images` exists | T0b (`TestEffectivePrefersAnyRepoImages`), S3 |

## Tasks

### T0: the member, its type, the node's effective map and the request's fields
- Acceptance:
  - `projectpolicy.RepoImages` (member `repo_images`) round-trips through the node's escape;
  - on decode, `ValidRepoImages` holds a key to `task.ValidProvider`'s shape and a canonical path,
    and a provider this release does not know is kept, not refused (R10). An old nested key,
    `acme/team/app`, decodes and serves nothing;
  - `knownTopLevelKeys` lists `repo_images`;
  - `Policy.EffectiveRepoImages` returns the node's `repo_images`, else its legacy map, and says
    which (R2);
  - `catalog.ImageRequest` gains `Provider`, `RepoImages` and `LegacyMap`, alongside today's
    `ImageMap`, which keeps working until X1's callers are moved. `mappedEntry` reads
    `RepoImages[Canonical(Provider, repo)]` when `RepoImages` is set, else the legacy map;
  - `UnknownMappedEntryError`'s message names the repository-to-image map (`repo_images`), and
    `run_test.go`'s assertion moves with it;
  - `issuesync.go`'s comments name `repo_images`;
  - the README's file table lists `contract.md`.
- Files:
  - `internal/projectpolicy/repoimages.go` (new)
  - `internal/projectpolicy/repoimages_test.go` (new)
  - `internal/projectpolicy/policy.go`
  - `internal/projectpolicy/issuesync.go`
  - `internal/catalog/select.go`
  - `internal/catalog/select_test.go`
  - `internal/cli/run_test.go`
  - `docs/image-map-by-provider/contract.md` (new) and `README.md`
- Tests:
  - `TestRepoImagesRoundTrip`;
  - `TestRepoImagesDecodeKeepsAnUnknownProvider`, `TestValidRepoImagesRefusesABadShape`;
  - `TestPolicyDecodeToleratesRepoImagesForOldReader`, `TestEffectiveRepoImagesPerMember`;
  - `TestMappedEntryByProvider`, `TestMappedEntryLegacyMap`;
  - `TestALaunchTheResolverRefusesIsRefused`.
- Command:
  `go test ./internal/projectpolicy/ ./internal/catalog/ -run 'RepoImages|MappedEntry' -count=1 && go test ./internal/cli/ -run 'ALaunchTheResolverRefusesIsRefused|Docs' -count=1`
- Red step: `TestMappedEntryByProvider` maps `github/acme/app` and `bitbucket/acme/app` to two
  entries. It fails while the lookup ignores the provider.
- Depends on: none.
- Commit: `image-map-by-provider T0: the repo_images member, its type and the lookup by provider`
- Reviewer tier: opus with an independent review (a wire format of the node, which the credentials
  service reads).

### T0b: the local config, its effective reader and the settings row
- Acceptance:
  - `localcfg.Config.RepoImages` (yaml `repo_images`) is decoded and checked on read as T0's
    decode is;
  - `localcfg.Config.ImageMap` stays, so `update` can migrate it;
  - the effective reader builds on `EffectiveRepoImages`: local `repo_images`, then the node's, then
    the legacy map, local first (R2);
  - `localcfg/shared.go` drops `repo_images` as it drops `image_map` (R7);
  - the settings registry gains a `repo_images` row beside `image_map`'s (R15), with
    `settings.schema.json` and the writer-conformance golden.
- Files:
  - `internal/localcfg/localcfg.go`
  - `internal/localcfg/localcfg_test.go`
  - `internal/localcfg/shared.go`
  - `internal/settings/registry.go`
  - `schemas/settings.schema.json`
  - `internal/cli/testdata/writer_gaps.golden`
- Tests:
  - `TestEffectivePrefersAnyRepoImages`, `TestLocalRepoImagesDecoded`;
  - `TestRegistryCoversEveryConfigField`, `TestRegistryHasARowForEveryRealConfigField`;
  - `TestWriterConformanceEveryRowHasAWriterOrALabel`, `TestWriterConformanceStrict`.
- Command:
  `go test ./internal/localcfg/ -run 'RepoImages|EffectivePrefers' -count=1 && go test ./internal/settings/ ./schemas/ -count=1 && go test ./internal/cli/ -run 'WriterConformance' -count=1`
- Red step: `TestEffectivePrefersAnyRepoImages` (a local `image_map` and a node `repo_images`)
  expects the node's. It fails under today's keep rule.
- Depends on: T0 (`localcfg` imports `projectpolicy` and builds on `EffectiveRepoImages`).
- Commit: `image-map-by-provider T0b: repo_images in the local config, and the effective reader`
- Reviewer tier: standard.

### T0c: the shared config and the generic setter refuse repo_images
- Acceptance:
  - `sharedForbidden` and the shared config's non-zero check refuse `repo_images` (R7);
  - `settings set repo_images` is refused with a pointer to `fugaro image map set`, as
    `image_map` is (`settings_set.go`'s exclusions).
- Files:
  - `internal/cli/sharedcfg.go`
  - `internal/cli/sharedcfg_test.go`
  - `internal/cli/settings_set.go`
  - `internal/cli/settings_set_test.go`
- Tests: `TestSharedConfigRefusesEveryForbiddenSection`, `TestSettingsSetRefusesRepoImages`.
- Command:
  `go test ./internal/cli/ -run 'SharedConfigRefusesEveryForbiddenSection|SettingsSetRefusesRepoImages|SettingsSetWritesTheBakeSwitch' -count=1`
- Red step: a shared config carrying `repo_images` is accepted.
- Depends on: T0b (the field).
- Commit: `image-map-by-provider T0c: the shared config and settings set refuse repo_images`
- Reviewer tier: opus with an independent review (the shared config is a write path any launcher
  reaches).

### U1a: the migration function and the key helper
- Acceptance:
  - `repoImageKey` turns a repository into its key: R1's provider order, then
    `projectpolicy.Canonical`, refusing a provider outside `config.Providers`;
  - `migrateImageMap` turns a legacy map into `repo_images`: two providers give two keys (D3373),
    an unknown provider gives every provider (D3375), and an unenrolled name is dropped with one
    line;
  - running the migration a second time changes nothing.
- Files:
  - `internal/cli/repo_images_migrate.go` (new)
  - `internal/cli/repo_images_migrate_test.go` (new)
- Tests: `TestMigrateImageMapOneProvider`, `...TwoProviders`, `...UnknownProvider`,
  `...Unenrolled`, `...Idempotent`, `TestRepoImageKeyRefusesUnservedProvider`.
- Command: `go test ./internal/cli/ -run 'MigrateImageMap|RepoImageKey' -count=1`
- Red step: `...TwoProviders` expects two keys, and fails before the function exists.
- Depends on: T0b (the effective reader; U1a reads `allow_repos` from the node, so nothing of L5).
- Commit: `image-map-by-provider U1a: the migration from image_map to repo_images, and repoImageKey`
- Reviewer tier: opus with an independent review (it decides what code each repository's jobs run).

### U1b: the Policy stage migrates the node and the local config
- Acceptance:
  - the `Policy` stage counts an `image_map` on the node, or in the local config, as drift
    (`policyMigrationFor`, `policyMigrateStage.Check`);
  - it rewrites the local config with `userSave` and publishes `repo_images`, removing
    `image_map` in the same write (D3376, R14). In a coding agent's session the stage is skipped,
    as today;
  - `firstPublishNodeOnlyMembers` and the default-entry refusal read the effective map;
  - `project policy show --json` prints `repo_images` (R5);
  - an installation whose only drift is `image_map` converges in one `update`;
  - the comment in `internal/apply/steps.go` names `repo_images`.
- Files:
  - `internal/cli/init_policy_migrate.go`
  - `internal/cli/init_policy_migrate_test.go`
  - `internal/cli/init_policy_publish.go`
  - `internal/cli/init_policy_publish_test.go`
  - `internal/cli/project_cmd.go`
  - `internal/cli/update_converge_test.go`
  - `internal/apply/steps.go`
- Tests:
  - `TestPolicyStageMigratesImageMap`, `TestPolicyStageImageMapIsDrift`,
    `TestPolicyShowPrintsRepoImages`;
  - `TestUpdatePublishesTheFirstPolicyNodeForAnImageMap`,
    `TestPolicyConfigDriftSeesTheImageMapAndTheSwitches`, `TestUpdateConvergesPreviousPolicy`,
    whose assertions move to `repo_images`.
- Command:
  `go test ./internal/cli/ -run 'PolicyStage|PolicyShowPrintsRepoImages|ImageMap|UpdateConverges' -count=1`
- Red step: `TestPolicyStageImageMapIsDrift`: a node whose only drift is `image_map` reads as done.
- Depends on: U1a (behaviour: the stage calls the migration).
- Commit: `image-map-by-provider U1b: the Policy stage moves image_map to repo_images`
- Reviewer tier: opus with an independent review (it writes the policy node).

### R1: launch and update's job images read by provider
- Acceptance:
  - `run.go` (the selection and `mappedEntryName`) and `infra/spec.go` (`repoCtx.imageRequest`, the
    provider from `repoCtx.provider`, F31) pass `Provider` and the effective map;
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
- Tests: `TestLaunchImageByProvider` (new, in `update_jobimages_test.go`),
  `TestUpdateMovesARepositoryJobToTheMappedEntry`,
  `TestAPlainUpdateMovesARepositoryJobToTheMappedEntry`,
  `TestCatalogCheckInlineEntryByProvider`, `TestCatalogCheckSkipsUnservedKey`.
- Command:
  `go test ./internal/cli/ -run 'LaunchImageByProvider|MappedEntry' -count=1 && go test ./internal/infra/ -run 'CatalogCheck' -count=1`
- Red step: a launch of the Bitbucket `acme/app` picks the GitHub key's entry.
- Depends on: T0b (the effective reader).
- Commit: `image-map-by-provider R1: launch and update's job images read repo_images by provider`
- Reviewer tier: standard.

### R2a: the image catalog commands read by provider
- Acceptance: `image_catalog.go` (`effectiveImageMap`, `publishedImageMap`, `catalogPointedAt`) and
  `image_bake_note.go` (`noteBakedExposure`) read the effective map by provider. They return the
  effective map, so doctor and `image map set` call them unchanged (F32).
- Files:
  - `internal/cli/image_catalog.go`
  - `internal/cli/image_bake_note.go`
  - `internal/cli/image_catalog_test.go`
  - `internal/cli/image_refresh_test.go`
- Tests: `TestCatalogPointedAtByProvider`, `TestBakeNoteByProvider`, and the `TestRefresh` tests.
- Command: `go test ./internal/cli/ -run 'PointedAtByProvider|BakeNoteByProvider|Refresh' -count=1`
- Red step: `catalogPointedAt` names both repositories of one name.
- Depends on: T0b.
- Commit: `image-map-by-provider R2a: the image catalog commands read repo_images by provider`
- Reviewer tier: standard.

### R2c: the catalog build and the bake read by provider
- Acceptance:
  - `image_build_catalog.go` passes provider keys to `imagebake`;
  - `imagebake.Decide` and `entryRepos` read `Map` by `provider/owner/name` and skip a key whose
    provider does not serve the repository, while an unadmitted repository at the right provider is
    still refused (R9);
  - the comments in `backend/gcp/catalog.go` and `config/catalog.go` cite `repo_images` and
    `projectpolicy.ValidRepoImages`.
- Files:
  - `internal/cli/image_build_catalog.go`
  - `internal/cli/image_build_catalog_test.go`
  - `internal/imagebake/imagebake.go`
  - `internal/imagebake/imagebake_test.go`
  - `internal/cli/catalog_bake_e2e_test.go`
  - `internal/backend/gcp/catalog.go`, `internal/config/catalog.go` (comments only)
- Tests: `TestBuildCatalogBakesByProvider`, `TestDecideByProviderKey`,
  `TestEntryReposSkipsAKeyAtTheWrongProvider`, `TestEntryReposStillRefusesAnUnadmittedRepo`,
  `TestCatalogBuildBakesTwoReposThroughTheService`.
- Command:
  `go test ./internal/imagebake/ -count=1 && go test ./internal/cli/ -run 'BakesByProvider|CatalogBuildBakesTwoReposThroughTheService' -count=1`
- Red step: `TestEntryReposSkipsAKeyAtTheWrongProvider`: a `bitbucket/acme/app` key with no such
  repository refuses the whole bake.
- Depends on: T0b.
- Commit: `image-map-by-provider R2c: the catalog build and the bake read repo_images by provider`
- Reviewer tier: standard.

### R2b: image survey prints repo_images (version 2)
- Acceptance:
  - `image survey --json` is version 2 (R4): `setup.repo_images` holds provider keys, and
    `setup.legacy_image_map` holds the bare-name map only while the effective map is legacy;
  - the text form prints whichever the project has;
  - `skills_test.go`'s field table names both members.
- Files:
  - `internal/cli/image_survey.go`
  - `internal/cli/image_survey_test.go`
  - `schemas/image-survey.schema.json`
  - `internal/cli/skills_test.go`
- Tests: `TestSurveyJSONMatchesSchema`, `TestImageSurveyRepoImagesV2`,
  `TestImageSurveyLegacyMapV2`, `TestLintJSONFieldsExist`, `TestSkillJSONClaimsMatchTheTable`, and
  `schemas/schemas_test.go`.
- Command:
  `go test ./internal/cli/ -run 'Survey|LintJSONFieldsExist|SkillJSONClaimsMatchTheTable' -count=1 && go test ./schemas/ -count=1`
- Red step: `TestSurveyJSONMatchesSchema` fails against a version-1 document.
- Depends on: T0b.
- Commit: `image-map-by-provider R2b: image survey --json version 2 prints repo_images`
- Reviewer tier: standard.

### R2d: the live checks read the survey's version 2
- Acceptance: `check-500.sh` and `check-581.sh` read `setup.repo_images`, and
  `setup.legacy_image_map` when it is present. Their shell tests' fixtures move with them.
- Files:
  - `scripts/live-checks/check-500.sh`
  - `scripts/live-checks/check-500_test.sh`
  - `scripts/live-checks/check-581.sh`
  - `scripts/live-checks/check-581_test.sh`
- Tests: `TestLiveCheckScripts`.
- Command: `go test ./scripts -run TestLiveCheckScripts -count=1`
- Red step: the scripts' tests fail on a version-2 fixture.
- Depends on: R2b (behaviour: the document the scripts read).
- Commit: `image-map-by-provider R2d: checks 500 and 581 read the survey's version 2`
- Reviewer tier: standard.

### R3: doctor, repos view and repos rm by provider
- Acceptance:
  - both of doctor's map readers (`doctor_layer.go`, online and offline) and `repos view` read by
    provider;
  - doctor names a file that holds both members (R2);
  - `project repos rm` migrates a legacy map first (R13), then removes the repository's key under
    its provider (R8). Its line names `repo_images`, and `check670_test.sh`'s stub moves with it.
- Files:
  - `internal/cli/doctor_layer.go`
  - `internal/cli/doctor_layer_test.go`
  - `internal/cli/repos_view.go`
  - `internal/cli/repos_view_test.go`
  - `internal/cli/project_repos_rm.go`
  - `internal/cli/project_repos_rm_test.go`
  - `scripts/live-checks/check670_test.sh`
- Tests: `TestReposViewImageByProvider`, `TestReposViewImageSourceIsNamedOnce`,
  `TestDoctorNamesBothMembers` (in `doctor_layer_test.go`), the `TestReposRm` tests,
  `TestReposRmRemovesProviderKey`, `TestReposRmMigratesALegacyMapFirst`, `TestLiveCheckScripts`.
- Command:
  `go test ./internal/cli/ -run 'ReposView|DoctorNamesBothMembers|ReposRm' -count=1 && go test ./scripts -run TestLiveCheckScripts -count=1`
- Red step: `rm` of the GitHub `acme/app` leaves its key.
- Depends on:
  - U1a (`repoImageKey` and the migration, R13);
  - R2a (doctor calls `effectiveImageMap`, F32).
- Commit: `image-map-by-provider R3: doctor, repos view and repos rm read repo_images by provider`
- Reviewer tier: standard.

### S1: the build token covers the request's provider only
- Acceptance:
  - `buildEntryRepos` reads the node through `EffectiveRepoImages` (R2);
  - on `repo_images`, the entry's set is the keys of the request's provider, skipping a key its
    table does not serve (R9);
  - a repository mapped only at the other provider is refused by name (spec acceptance 4);
  - on a legacy map, it reads as today.
- Files: `internal/credsvc/buildtoken.go`, `internal/credsvc/buildtoken_test.go`
- Tests: `TestBuildTokenRefusesOtherProvidersKey`, `TestBuildTokenSkipsUnservedKey`,
  `TestBuildTokenReadsOldMapUntilMigrated`.
- Command: `go test ./internal/credsvc/ -run 'BuildToken' -count=1`
- Red step: a Bitbucket request for a repository mapped only at GitHub is granted.
- Depends on: T0 (the node's member and `EffectiveRepoImages`; `credsvc` reads no local config).
- Commit: `image-map-by-provider S1: a build token covers the request's provider only`
- Reviewer tier: opus with an independent review (the credentials service).

### S2: image map set writes repo_images
- Acceptance:
  - the argument is read by R6. `image map set acme/app go` writes `github/acme/app` through
    `repoImageKey`, and `image map set bitbucket/acme/app go` writes that key when it is
    enrolled;
  - an unenrolled key or unknown provider is refused, naming the form and
    `fugaro project repos add`;
  - a legacy map is migrated into the file first (R13);
  - the managed and agent-session refusals (`image_map_managed.go`) name the command as it now
    reads;
  - the help, `cli-reference.md` and `next_step_allow.golden` are regenerated.
- Files:
  - `internal/cli/image_map.go`
  - `internal/cli/image_map_managed.go`
  - `internal/cli/image_map_test.go`
  - `website/content/cli-reference.md`
  - `internal/cli/testdata/next_step_allow.golden`
- Tests:
  - the `TestImageMapSet` tests;
  - `TestImageMapSetWritesProviderKey`, `TestImageMapSetTakesAnAdmittedOtherProvider`,
    `TestImageMapSetRefusesUnenrolled`, `TestImageMapSetMigratesALegacyMapFirst`;
  - `TestNoMessageHandsOverAFollowedUpCommand`;
  - `TestCliReferenceNamesEveryCommandAndFlag`, `TestCliReferenceInventsNothing`.
- Command:
  `go test ./internal/cli/ -run 'ImageMap|NoMessageHandsOverAFollowedUpCommand|CliReference' -count=1 && (cd website && npm run check)`
- Red step: `...WritesProviderKey` reads `acme/app` back.
- Depends on:
  - U1a (`repoImageKey` and the migration);
  - R2a (`noteBakedExposure`, F32).
- Commit: `image-map-by-provider S2: image map set writes repo_images by provider`
- Reviewer tier: standard.

### S3: project repos add writes repo_images
- Acceptance: `project repos add` migrates a legacy map first, including the one it seeds from
  the effective map (`imageSeeded`, R13). Then it writes the new repository's key in
  `repo_images` through `repoImageKey`, and never writes `image_map`.
- Files:
  - `internal/cli/project_enroll_converge.go`
  - `internal/cli/project_enroll_converge_test.go`
- Tests: `TestReposAddWritesProviderKey`, `TestReposAddNeverWritesImageMap`,
  `TestReposAddMergesThePublishedImageMap`, and the other `TestReposAdd` tests.
- Command: `go test ./internal/cli/ -run 'ReposAdd' -count=1`
- Red step: `TestReposAddNeverWritesImageMap` finds `image_map` in the file.
- Depends on:
  - U1a;
  - R3 (`reposViewAllowOf` and `reposViewRowOf`, F32).
- Commit: `image-map-by-provider S3: repos add writes repo_images`
- Reviewer tier: standard.

### D1a: the skills
- Acceptance: the `images` and `setup` skills name `repo_images`, its `provider/owner/name` keys
  and the survey's version 2. `images_skill_test.go`'s literal moves with them, and the skills lint
  passes.
- Files:
  - `plugin/skills/images/SKILL.md`
  - `plugin/skills/setup/SKILL.md`
  - `plugin/images_skill_test.go`
- Tests: `go test ./plugin/`, `TestLintJSONFieldsExist`, `TestSkillJSONClaimsMatchTheTable`,
  `TestProjectLayerSkillClaimsRealFields`.
- Command:
  `go test ./plugin/ -count=1 && go test ./internal/cli/ -run 'LintJSONFieldsExist|SkillJSONClaimsMatchTheTable|ProjectLayerSkillClaimsRealFields' -count=1`
- Red step: none (documents). A pinned sentence changes together with its test.
- Depends on:
  - R2b: the lint fails on a field that does not exist yet;
  - S2: the help text.
- Commit: `image-map-by-provider D1a: the skills name repo_images`
- Reviewer tier: opus with an independent review (plugin skills).

### D1b: the documents and the security pages
- Acceptance: these documents name `repo_images`:
  - `docs/project-layer.md`;
  - `docs/gcp-live-checklist.md`, with Check 940's row and Check 581's survey member;
  - `docs/how-to/offboarding.md`;
  - `SECURITY.md`, with the build-token rule;
  - `website/content/security.md`.

  The Docs tests and the website check pass.
- Files:
  - `docs/project-layer.md`
  - `docs/gcp-live-checklist.md`
  - `docs/how-to/offboarding.md`
  - `SECURITY.md`
  - `website/content/security.md`
- Tests: `go test ./internal/cli/ -run Docs`, and `npm run check` in `website/`.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`
- Red step: none (documents).
- Depends on: S1 (the build-token rule), R2b (version 2), S2 (the help text).
- Commit: `image-map-by-provider D1b: the documents and security pages name repo_images`
- Reviewer tier: opus with an independent review (`SECURITY.md`).

### K1: Check 940, as a script
- Acceptance:
  - `scripts/live-checks/check940.sh` reads, without writing:
    - the project's node, showing `repo_images` present and `image_map` absent;
    - `image survey --json` at version 2;
    - one job image per provider the project has. With no Bitbucket repository, it skips that
      step with a line (V3).
  - It prints PASS or FAIL per step, and is registered in `liveCheckScripts`.
  - `check196.sh`, its test and its steps read `repo_images`, else `image_map`.
- Files:
  - `scripts/live-checks/check940.sh` (new)
  - `scripts/live-checks/check940_test.sh` (new)
  - `scripts/live_checks_test.go`
  - `scripts/live-checks/check196.sh`
  - `scripts/live-checks/check196_test.sh`
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
  - no caller sets `catalog.ImageRequest.ImageMap` any more, and the field is removed.
- Files:
  - `internal/cli/repo_images_e2e_test.go` (new)
  - `internal/cli/update_in_flight_test.go`
  - `internal/catalog/select.go`
- Tests: `TestRepoImagesEndToEnd`, `TestRepoImagesConvergesFromPreviousRelease`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command:
  `go build ./... && go test ./internal/cli/ -run 'RepoImagesEndToEnd|RepoImagesConvergesFromPreviousRelease|TestUpdateLeavesARunInFlightRunning' -count=1 && go test ./internal/catalog/ -count=1`
- Red step: `TestRepoImagesEndToEnd` is run once with R3's reader switched back to the bare-name
  lookup, and must fail. That shows it reaches every reader.
- Depends on: U1b, R1, R2a, R2c, R3 and S3 (behaviour: it exercises them together).
- Commit: `image-map-by-provider X1: repo_images end to end, and the converge from the release before`
- Reviewer tier: standard.

## Parallel execution

| Level | Tasks |
| --- | --- |
| 0 | T0, K1 |
| 1 | T0b, S1 |
| 2 | T0c, U1a, R1, R2a, R2b, R2c |
| 3 | U1b, R2d, R3, S2 |
| 4 | S3, D1a, D1b |
| 5 | X1 |

- **Critical path, as the read reports it:** T0 → T0b → R2a → R3 → S3 → X1, six tasks. T0 is the bottleneck.
- **What must be sequential:**
  - T0 → T0b: `localcfg` builds on T0's reader.
  - U1a → U1b, R3, S2 and S3: they call `repoImageKey` and the migration.
  - R2a → R3 and S2, and R3 → S3: shared helpers (F32).
  - R2b → R2d and D1a, and S2 → D1a and D1b: the document read, the lint and the help text.
  - X1 last.

### Shared files

No level holds two writers of one file. X1 writes `select.go` after T0 (levels 0 and 5).
`scripts/live_checks_test.go` is K1's alone: R2d and R3 change scripts' own test files, not the
registry.

| File | Writer |
| --- | --- |
| `internal/projectpolicy/policy.go`, `issuesync.go`, `repoimages*.go`; `internal/catalog/select.go`, `select_test.go`; `internal/cli/run_test.go`; `contract.md`, `README.md` | T0 (X1 at level 5 edits `select.go`) |
| `internal/localcfg/localcfg.go`, `shared.go`; `internal/settings/registry.go`; `schemas/settings.schema.json`; `internal/cli/testdata/writer_gaps.golden` | T0b |
| `internal/cli/sharedcfg.go`, `settings_set.go` and their tests | T0c |
| `internal/cli/repo_images_migrate.go` | U1a |
| `internal/cli/init_policy_migrate.go`, `init_policy_publish.go`, `project_cmd.go`, `update_converge_test.go`; `internal/apply/steps.go` | U1b |
| `internal/cli/run.go`, `update_jobimages_test.go`; `internal/infra/spec.go`, `catalogcheck.go`, `readiness.go` | R1 |
| `internal/cli/image_catalog.go`, `image_bake_note.go`, `image_refresh_test.go` | R2a |
| `internal/cli/image_build_catalog.go`, `catalog_bake_e2e_test.go`; `internal/imagebake/`; `internal/backend/gcp/catalog.go`; `internal/config/catalog.go` | R2c |
| `internal/cli/image_survey.go`, `skills_test.go`; `schemas/image-survey.schema.json` | R2b |
| `scripts/live-checks/check-500*.sh`, `check-581*.sh` | R2d |
| `internal/cli/doctor_layer.go`, `repos_view.go`, `project_repos_rm.go`; `scripts/live-checks/check670_test.sh` | R3 |
| `internal/credsvc/buildtoken.go` | S1 |
| `internal/cli/image_map.go`, `image_map_managed.go`, `testdata/next_step_allow.golden`; `website/content/cli-reference.md` | S2 |
| `internal/cli/project_enroll_converge.go` | S3 |
| `plugin/skills/images/SKILL.md`, `plugin/skills/setup/SKILL.md`, `plugin/images_skill_test.go` | D1a |
| `docs/project-layer.md`, `docs/gcp-live-checklist.md`, `docs/how-to/offboarding.md`, `SECURITY.md`, `website/content/security.md` | D1b |
| `scripts/live-checks/check940*.sh`, `check196*.sh`; `scripts/live_checks_test.go`; `docs/secrets-brokering/check196-steps.md` | K1 |
| `internal/cli/update_in_flight_test.go` | X1 |

### Launch notes: files other plans write (F20)

L11 ships one release after L1 (D3374), so most of L5 and L8 merge first. A task launches after the
other plan's task that shares its file has merged, or it rebases onto it:

| L11 task | Waits for |
| --- | --- |
| T0b | L8 XB3 and AB1 (`registry.go`, `settings.schema.json`) |
| U1b | L5 U5 (`init_policy_migrate.go`), L8 I2 (`update_converge_test.go`) |
| R1 | L8 PR2 (`run.go`). L8's XB2 is its release B: rebase rather than wait |
| S2 | L8 S4 and RG3, L5 U3b, U9b and U10 (`cli-reference.md`) |
| S3 | L5 U9a (`project_enroll_converge.go`) |
| D1a | L8 D2 (`setup/SKILL.md`) |
| D1b | L8 S0, XB1 and D1 (`project-layer.md`), L8 CK2 and CK3, L5 U7b (`gcp-live-checklist.md`) |
| K1 | L8 CK2 and CK3 (`scripts/live_checks_test.go`) |
| X1 | L5 I1 and L8 I2 (`update_in_flight_test.go`) |

## The dependencies broken and kept

The contract-first pass ran, with decoupling on. The shared shape is in its own tasks (T0 and T0b,
plus `repoImageKey` in U1a), so no edge between two other tasks carries only an interface:
- the readers need T0b's effective reader, not U1b's migrated data, because their tests build nodes
  and files themselves;
- the writers (R3, S2, S3) need U1a's behaviour, the migration they run first (R13), which a stub
  could not give them.

T0 and T0b are real code, not stubs, so they are ordinary tasks, and no integration task is
needed. Every remaining edge is kept, with the reason written beside it in the graph:
- types the dependent compiles against;
- helpers it calls (F32);
- behaviour it runs.

K1 depends on nothing: a script needs only the member's name. The read reports no decoupling, so
there is no gain line.

**The estimate, as `fugaro plan graph` printed it.** It is informational and reserves no budget:
`ready in 300–1200 min, no history yet for 8 tasks (18 tasks; tiers for 10, seeds for 8; from the cached history of 2026-11-22 07:25 UTC)`

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
- **One T0.** It would have touched about fifteen files. It is split into T0 (`projectpolicy` and
  `catalog`), T0b (`localcfg` and the registry) and T0c (the CLI's refusals). T0, U1b and R2c are
  still over five files: each extra file is a comment or a test that moves with its code (R12).
- **Bumping `projectpolicy.Version`** (R3): it would stop runs in flight on an older credentials
  service.
- **`Source.Repo`** (D3377): a follow-up (O2).
