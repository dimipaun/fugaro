# Image map keyed by provider (lift L11): reconciliation

This document is checked against `origin/main` at `3dfc5cb8f`, on 2026-11-25. It amends
[spec.md](spec.md). **Where the two disagree, this document wins.**

## Spec concepts and what the repository already has

| Spec concept | What the repository already has |
| --- | --- |
| `provider/owner/name` | `projectpolicy.Canonical` (`internal/projectpolicy/match.go`). The allow-list (`allow_repos`, `projectpolicy.MatchRepo`) and `clone_urls` are keyed by it already (F1) |
| The map on the node | `projectpolicy.Policy.ImageMap` and the `ImageMap` type, escaped by `escKey`/`unescKey`, and checked by `ValidImageMap` |
| The map in the local config | `localcfg.Config.ImageMap`, read through `ImageMapEffective` (the keep rule, D1140). The shared config drops it (`localcfg/shared.go`), and `ParseShared` and `sharedcfg.go` refuse it |
| The provider of a repository | `localcfg.Repo.Provider`, else the checkout's `git.provider`, through `cloudEnv.repoProvider` (`internal/cli/project_job.go`) |
| The job's lookup | `catalog.ImageRequest.mappedEntry` (`internal/catalog/select.go`) |
| The build-token check | `credsvc.Server.buildEntryRepos` (`internal/credsvc/buildtoken.go`). The request already carries `Provider` (F6) |

## Assumptions

- **A1.** "Enrolled" means the local config's `repos` at their providers, plus the repositories an
  exact `allow_repos` entry names (R1). This is L5's D3350 count, read from what `main` already
  has (F27).
- **A2.** One capability, so there is no capability map. Every part reads or writes one member.
- **A3.** The map's values (catalog entry names) do not change.
- **A4.** Provider kinds are those `config.Providers` serves: `github` and `bitbucket`.

## Findings

- **F1 (Exists).** `projectpolicy.Canonical(provider, repo)` returns `provider/owner/name`. It is
  the allow-list's form, and `clone_urls` is keyed by it too (`image_build_catalog.go`, the comment
  on the two key spaces). L11 adds no new format: `repo_images` uses the same one.
- **F2 (Gap).** The local config's `repos` is keyed by `owner/name`, with "one provider per
  owner/name" (`localcfg.Repo`). So one machine's config cannot hold both a GitHub and a Bitbucket
  `acme/app`. The collision arises through published admits (`allow_repos` names a provider) and
  through stale node data, which the audit found in real data (report §4). The design stands. The
  migration's "both providers" case (D3373) comes from admits, or from D3375.
- **F3 (Gap).** `localcfg.Repo.Provider` may be empty: then the checkout's `git.provider` decides
  (`repoProvider`). `update` from anywhere (L5) has no checkout. A provider that is still unknown is
  D3375's case.
- **F4 (Wrong).** The spec says "the policy stage writes the new map before any image build of the
  same `update`". The stage order in `internal/initflow/stage.go` (`order`) runs `Images` before
  `Policy`, and `CredentialsService` after `Policy`. During the first `update`, the new code
  therefore reads a node that still holds `image_map`. Strict refusal (Q3 as first answered) would
  break that `update`. D3372 is amended (R2).
- **F5 (Gap).** There are more readers than the spec's nine `ImageRequest` sites (the code has ten; `infra/projectjobs.go` passes no map). Each is a direct
  lookup or a writer of the map:
  - `run.go`: `mappedEntryName`, and the selection;
  - `infra/catalogcheck.go`: the rebuild account of an inline entry;
  - `image_bake_note.go`;
  - `image_catalog.go`: `effectiveImageMap`, `publishedImageMap`, `catalogPointedAt` and its
    comparison;
  - `image_survey.go`: the text and `--json`;
  - `project_repos_rm.go`: `rm` removes the repository's map entry;
  - `init_policy_publish.go`: the diff lines;
  - `project_cmd.go`: the publisher, and `showPolicy`;
  - `init_policy_migrate.go`: `firstPublishNodeOnlyMembers` and the default-entry refusal;
  - `localcfg/shared.go`: the shared config's refusal;
  - `internal/settings/registry.go`: the `image_map` row.

  Every one lands in a task.
- **F6 (Exists).** `BuildTokenRequest` carries `Provider` and canonicalises each repository.
  `buildEntryRepos` already uses `projectpolicy.Canonical` for the installation's table, and already
  refuses a repository the other provider holds.
- **F7 (Exists).** Unknown top-level members of the node are tolerated (`knownTopLevelKeys`, P8). An
  older reader sees no `repo_images` and reads "maps nothing", a state the member has always had
  (`omitempty`).
- **F8 (Rule).** "A field never changes meaning": the keys change meaning, so the member gets a new
  name (D3371). `image survey --json` requires `setup.image_map` (`schemas/image-survey.schema.json`,
  version 1), and removing it bumps the document's version (R4).
- **F9 (Gap).** A catalog entry's dockerfile source, `Source.Repo`, is `owner/name` with no provider:
  the same kind of collision. D3377 leaves it to a follow-up (O2).
- **F10 (Gap).** No runner reads the map. `internal/runner` has no reader, so runs in flight are not
  touched. The credentials service reads it only for build tokens. `update`'s own builds run in
  `Images`, before `Policy`, and are made against the node as it stood.
- **F11 (Exists).** A flag or help change regenerates `website/content/cli-reference.md`
  (`go run ./ee/clidoc`). `TestCliReferenceNamesEveryCommandAndFlag` and
  `TestCliReferenceInventsNothing` (`docs_website_cliref_test.go`) pin it.
- **F12 (Gap, review round 1).** More writers and readers, beyond F5:
  - **`project repos add`** writes `image_map[owner/name]`, with no provider
    (`project_enroll_converge.go`, `enrolImageMap`).
  - **The bake composer** (`internal/imagebake`, `Decide` and `entryRepos`) reads
    `Request.Map[owner/name]`.
  - **The shared config's refusal** is also in `internal/cli/sharedcfg.go` (`sharedForbidden`, and
    its non-zero check), not only in `localcfg/shared.go`.
  - **A second reader in doctor:** `doctor_layer.go` (the offline arm).
  - **User-facing fix text** in `infra/readiness.go` names `image_map`.
  - **Stale comments:** `backend/gcp/catalog.go` cites `config.ValidImageMap`, which does not
    exist, and `projectpolicy/issuesync.go` names the map.

  Each lands in a task.
- **F13 (Wrong, review round 1).** L5's reader cannot supply a provider. `repofiles.Read` answers a
  repository with no known provider as unread (`reader.go`), so R1's step 3 is the checkout's
  `git.provider` (`repoProvider`), not a default-branch read.
- **F14 (Gap, review round 1).** D3375's keys can name a repository that is not served at that
  provider: `bitbucket/acme/app` when `acme/app` is only at GitHub. If such a key sits on an entry,
  `buildEntryRepos` answers that whole entry with a 503, and `imagebake.entryRepos` refuses "a
  repository the project does not admit". Today a mapped name with no repository poisons nothing.
  R9 keeps D3375's intent.
- **F15 (Wrong, review round 1).** `projectpolicy.Canonical` checks a provider's *shape*
  (`task.ValidProvider`), not that it is a provider Fugaro serves. An old nested key such as
  `acme/team/app` would pass as provider `acme`. `canonicalRepoArg` has the same gap, and it refuses
  `owner/name` when the local provider is empty. R10 closes both.
- **F16 (Gap, review round 1).** The old member must not come back. A per-key fallback to
  `image_map` would let one written by a CLI from before L1 revive the collision. R2 is per member.
- **F17 (Gap, review round 1).** These are the known gaps:
  - **A CLI from before L1, with no local map, reading a migrated node** sees no map. It picks the
    catalog's default entry instead of the mapped one: a wrong image, not a refusal. This is the
    gap the owner accepted for CLIs from before L1 (L1's Q2, D3374).
  - **A CLI from before L1, reading a migrated local config,** fails to load it: `localcfg` decodes
    strictly. That is loud.
  - **The old credentials service** refuses every build token between `Policy` and
    `CredentialsService`, and for as long as an `update` stopped between them. The project's
    scheduled catalog rebuilds are among them. The next `update` ends it. This is the cost of D3376,
    stated in O3.
- **F18 (Rule, review round 1).** `repo_images` sits beside the "repo-level images" vocabulary of
  D1900 (`repoImagesOff`, `internal/cli/image.go`), which means a repository's own image rows. R11
  keeps the two apart.
- **F19 (Gap, review round 1).** Existing tests and goldens fixture the old member:
  - `update_converge_test.go`, `update_jobimages_test.go`, `catalog_bake_e2e_test.go`,
    `init_policy_publish_test.go`, `init_policy_migrate_test.go`,
    `project_enroll_converge_test.go`, `image_refresh_test.go`, `sharedcfg_test.go` and
    `skills_test.go`;
  - the goldens `writer_gaps.golden` and `next_step_allow.golden`;
  - `plugin/images_skill_test.go`.

  Live checks read it too: `check-500.sh` and `check-581.sh` read the survey's
  `setup.image_map`, and `check196.sh` reads the node's. So do `SECURITY.md` and the website's
  security page. Each lands in the task that changes the code it pins.
- **F20 (Gap, review round 1).** Other plans write some of the same files:
  - L5:
    - U5 writes `init_policy_migrate.go`.
    - U9a writes `project_enroll_converge.go`.
    - U7b writes `gcp-live-checklist.md`.
    - U3b, U9b and U10 write `cli-reference.md`.
    - I1 writes `update_in_flight_test.go`.
  - L8:
    - PR2 and XB2 write `run.go`.
    - XB3 and AB1 write `registry.go` and `settings.schema.json`.
    - S0, XB1 and D1 write `project-layer.md`.
    - D2 writes `setup/SKILL.md`.
    - CK2 and CK3 write `gcp-live-checklist.md`.
    - S4 and RG3 write `cli-reference.md`.
    - I2 writes `update_in_flight_test.go` and `update_converge_test.go`.

  The plan's launch notes order L11's tasks after the ones they share a file with. Amended after
  review round 2: L8's CK2 and CK3 also write `scripts/live_checks_test.go`.
- **F21 (Gap, review round 2).** `Decode` fails the whole node when the map is invalid
  (`projectpolicy` `ValidImageMap` in the decode). A provider check on decode would turn a later
  release's new provider into a refusal of the node by every older reader, the credentials service
  among them. R10 is amended so the check applies at the write.
- **F22 (Gap, review round 2).** More pins of the old member's output or messages:
  - `run_test.go` (`UnknownMappedEntryError`'s message, in `select.go`);
  - `settings_set_test.go` (`settings set image_map`, refused);
  - `check-500_test.sh` and `check-581_test.sh` (the survey's `setup.image_map`);
  - `check196_test.sh`;
  - `check670_test.sh` (`rm`'s `image_map[...]: removed` line);
  - `image_map_managed.go` (the agent-session refusal names the command);
  - stale comments in `internal/apply/steps.go` and `internal/config/catalog.go`.

  Tests that only use `image_map` as an input, among them `policy_test.go`,
  `image_resolver_test.go` and `recipes_skew_catalog_test.go`, stay valid under R2 (R12).
- **F23 (Gap, review round 2).** A writer run after `brew upgrade` and before `update` meets a legacy
  map. Writing `repo_images` beside it would make R2 read only the new member, so every other
  mapping would fall to the default entry. `project repos add` also seeds its file from the
  effective map (`imageSeeded`). R13 fixes this.
- **F24 (Gap, review round 2).** The registry tests (`TestRegistryCoversEveryConfigField`,
  `TestRegistryHasARowForEveryRealConfigField`) cover every `localcfg.Config` field. So the new
  field's row lands with the field (R15), and `image_map`'s row stays while the field does.
- **F25 (Gap, review round 2).** The credentials service reads no local config, so R2's rule needs
  a node-side half that both it and `localcfg` use (`Policy.EffectiveRepoImages`, T0).
- **F26 (Gap, review round 2).** No task wired the local config's rewrite into `update`. R14 places
  it at the `Policy` stage.
- **F27 (Wrong, review round 2).** A1 and R1 leaned on L5's enrolled list (D3350), which is not on
  `main`. The published `allow_repos` is, so R1 reads it directly, and L11 does not wait for L5.
- **F28 (Gap, review round 2).** "Served" was undefined on the CLI side, and skipping in `imagebake`
  would have swallowed today's refusal of an unadmitted repository. R9 is amended.
- **F29 (Rule, review round 2).** The survey's version 2 had no rule for a project whose map is
  still legacy. Printing bare keys under `repo_images` would reuse the name with the old meaning.
  R4 is amended.
- **F30 (Gap, review round 2).** `image map set` refuses a repository the local config does not
  list (`onDisk.RepoFor`). By F2, the second provider's repository is never listed, so
  `bitbucket/acme/app` needs the admits. A three-segment argument could also be a relative checkout
  path. R6 is amended.
- **F31 (Wrong, review round 2).** The spec's "the job's own input (`infra` spec `Provider`)" does
  not exist as named. The job's provider is `repoCtx.provider`, from `Git.Provider` in the job's
  configuration (`infra/spec.go`).
- **F32 (Gap, review round 2).** Some helpers are shared across tasks:
  - `effectiveImageMap` (`image_catalog.go`) is called by doctor (`doctor_layer.go`);
  - `reposViewAllowOf` and `reposViewRowOf` (`repos_view.go`) are called by `repos add`;
  - `noteBakedExposure` (`image_bake_note.go`) is called by `image map set`.

  The plan orders those tasks.

## Decisions

All answered by the owner. D3370–D3374 are the spec's Q1–Q5, answered on 2026-11-25. D3375–D3377
were raised here and answered the same day.

| # | Question | Answer |
| --- | --- | --- |
| D3370 | Q1: the key's form | **`provider/owner/name`, flat**, from `projectpolicy.Canonical`. As recommended |
| D3371 | Q2: the member's name | **`repo_images` everywhere**: the node, the local config and `--json`. `image_map` retires. As recommended |
| D3372 | Q3: an old-form key | First answer: refused, with the fix. **Amended after review, and confirmed by the owner 2026-11-25** (F4). An old `image_map` *member*, on the node or in a local config not yet updated, is read as it is read today (no provider, matching every provider) until `update` rewrites it. An old-form *key* inside `repo_images` is refused with the fix. The collision cannot return, because nothing writes `repo_images` without a provider |
| D3373 | Q4: a name at two providers | **Both keys, same entry.** As recommended |
| D3374 | Q5: when it ships | **One release after L1**, as L8. As recommended |
| D3375 | An enrolled name whose provider `update` cannot find (no local provider, no admit naming it, default branch unread) | **Every provider** (`github` and `bitbucket`): today's behaviour exactly. As recommended |
| D3376 | The old `image_map` member when `Policy` writes `repo_images` | **Removed in the same write.** As recommended. Review round 1 found that the old credentials service's refusal can outlast "seconds" (F17), and that a CLI from before L1 picks the default entry rather than refusing. Both are stated in F17, and O3 asks the owner to confirm |
| D3377 | A catalog entry's `Source.Repo` (F9) | **A follow-up, not L11.** As recommended |

## Resolutions

- **R1.** **Provider resolution in the migration**, in order. Amended after review rounds 1 and 2
  (F13, F27).
  1. the local config's entry (`localcfg.Repo.Provider`);
  2. the providers of the node's own `allow_repos` entries that name the repository exactly (no
     wildcard). This is read from the published policy on `main` today, so it needs nothing from
     L5;
  3. the checkout's `git.provider`, when this machine has a checkout (`repoProvider`);
  4. D3375 (every provider).

  A name that is none of these, so not enrolled at all, is dropped with one line, as the spec says.
  **The "enrolled" set** is steps 1 and 2: the local config's `repos` at their providers, plus the
  exact admits.
- **R2.** **The temporary read of the old member** (D3372). It is per member, never per key (F16).
  - Any `repo_images` wins over any `image_map`: the local config's `repo_images` first, then the
    node's. `image_map`, local first, is read only when neither place has `repo_images`.
  - The node's half is `projectpolicy.Policy.EffectiveRepoImages` (T0). The credentials service
    reads the node through it, and `localcfg`'s effective reader builds on it, so the rule is
    written once (F25).
  - `catalog.ImageRequest` carries `RepoImages` and `LegacyMap`, two fields, and the effective
    reader fills exactly one. A legacy map is looked up by bare name, as today.
  - A file that holds both members reads `repo_images` and ignores `image_map`. Doctor names it,
    and the next `update` removes it.
  - Removing the legacy read is a follow-up one release later (O1), as L5 does with its fold.
- **R3.** **No policy version bump.** Bumping `projectpolicy.Version` would make an older credentials
  service refuse every node it reads, and it re-reads the node on each model call, so runs in flight
  would stop (the HARD RULE). Removing an `omitempty` member is a state older readers already handle
  (F7). L1's stamp fences older CLIs (D3374).
- **R4.** **`image survey --json` goes to version 2.** Amended after review round 2 (F29).
  - `setup.repo_images` holds `provider/owner/name` keys, and is empty while the project's
    effective map is still the legacy one.
  - `setup.legacy_image_map` holds the bare-name map only in that case, and is absent otherwise.
    No member carries a bare name under the new name.
  - `imageSurveyDocVersion` and the schema's `const` change together, and the `images` skill reads
    both members.
- **R5.** **`project policy show --json`** prints `repo_images` in the readable spelling (the
  `showPolicy` shadow), and `image_map` while the node still has it.
- **R6.** **`fugaro image map set`** keeps its name: it is a verb, not the member. It writes
  `repo_images` in the local config. Amended after review rounds 1 and 2 (F15, F30):
  - an argument that names an existing directory is a checkout path, as today;
  - otherwise, three or more segments whose first is in `config.Providers` are
    `provider/owner/name`;
  - anything else is resolved by `repoArg`, and its provider by `repoProvider`;
  - the key must be enrolled (R1's set): the local `repos` at that provider, or an exact admit
    that names it. Otherwise it is refused, naming the form and `fugaro project repos add`;
  - an unknown provider is refused the same way.
- **R7.** **The shared config** refuses `repo_images` for `image_map`'s reason. `ParseShared`
  refuses it and `localcfg/shared.go` drops it, as each does for `image_map`.
  `internal/cli/sharedcfg.go`'s `sharedForbidden` lists it (F12).
- **R8.** **`project repos rm`** removes the repository's `repo_images` key under its provider.
  **`project repos add`** writes one there (F12).
- **R9.** **A key whose provider does not serve that repository is skipped, never refused** (F14).
  Amended after review round 2 (F28):
  - "Serves" is the enrolled set (R1). In the credentials service it is its own `s.cfg.Repos`
    table.
  - This applies where keys are enumerated: the build token's entry set (S1), the bake's
    `entryRepos` and `catalogcheck`'s rebuild account.
  - A key at the right provider for a repository the project does not admit is still refused, as
    `imagebake` refuses it today.
  - A *request* naming a repository at the wrong provider is still refused by name.
- **R10.** **The provider is held to `config.Providers` when a key is written, and only to its
  shape when it is read.** Amended after review round 2 (F21).
  - The writers (U1a, S2, S3, the publisher) refuse a provider Fugaro does not serve.
  - `ValidRepoImages`, on decode, holds a key to `task.ValidProvider`'s shape and a canonical path.
    A well-shaped key at a provider this release does not know is kept and skipped (R9), never an
    error. So a later release that adds a provider (GitLab, B3) cannot make an older credentials
    service refuse the node.
  - An old nested key such as `acme/team/app` is refused by every writer. A reader skips it,
    because `acme` serves nothing. Spec acceptance 5 holds at the write.
- **R11.** **Code and messages say "the repository-to-image map (`repo_images`)"**, never "repo
  images", so it stays apart from D1900's repo-level image rows (F18). The Go type is `RepoImages`
  in `projectpolicy`, and no identifier in `internal/cli` reuses the `repoImages` stem of D1900's
  helpers.
- **R12.** **A test, golden or script that pins written output or a message moves with the code
  that changes it** (F19, F22): in the same task, never in a later clean-up. A test that only
  fixtures `image_map` as an input stays as it is, because R2 still reads it.
- **R13.** **A writer meets a legacy map first** (F23). `image map set`, `project repos add` and
  `project repos rm` write `repo_images`. When the effective map is still the legacy one, they first
  migrate it into the local config with U1a's function, then apply their change, so no mapping is
  lost. One helper, `repoImageKey`, written in U1a, turns a repository into its key: R1's provider
  order, then `projectpolicy.Canonical`. Every writer uses it.
- **R14.** **`update` rewrites the local config at the `Policy` stage** (F26), in the same step that
  publishes from it, with the file's backup (`userSave`). In a coding agent's session the `Policy`
  stage is skipped, as it is today, so neither file changes, and the readers keep the legacy map
  (R2).
- **R15.** **The settings registry keeps both rows** (F24). `repo_images` is added beside
  `image_map` in the task that adds the field (T0b), with `settings.schema.json`, because the
  registry tests cover every config field.

## Open tensions

- **D3376 against its stated cost (O3).** The owner chose to remove the old member in the same
  write, when the stated cost was "seconds". Review found the old credentials service can refuse
  build tokens for longer (F17). This waits on the owner's confirmation. The plan builds D3376 as
  answered.

## Open items

- **O1.** A follow-up one release after L11: remove R2's read of the old member.
- **O2.** A follow-up spec for `Source.Repo` (D3377). L3 moves the catalog's records and may carry
  it.
- **O3.** The cost of D3376 (F17): the old credentials service refuses build tokens from `Policy`
  until `CredentialsService`, longer if an `update` stops between them, and that includes the
  scheduled catalog rebuilds. The owner chose D3376 being told only "for the seconds until its own
  redeploy". A request to confirm it, now that the longer case is stated. The alternative is to
  remove `image_map` in a step after `CredentialsService`.

## UNVERIFIED

- **V1.** That every published admit names its provider. `allow_repos` patterns are
  `provider/owner/name` by `projectpolicy.ValidPattern`, but a wildcard such as `github/acme/*`
  covers names `update` cannot list. The task that implements R1 settles this; a wildcard admit
  without a listed name leaves R1 to step 3 or 4.
- **V2.** That no console or mobile code reads `image_map`. `git grep` finds none in `console/`,
  `mobile/` or `shared/`. Check 940 confirms it on a live installation.
- **V3.** Whether project `fugaro` holds a Bitbucket repository for Check 940's third step. The
  known Bitbucket sandbox is the workspace `dimi_bmb`. The owner settles this when running the
  check; the script skips that step, with a line, when the project has no Bitbucket repository.
