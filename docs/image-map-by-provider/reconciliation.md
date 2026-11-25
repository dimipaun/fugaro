# Image map keyed by provider (lift L11): reconciliation

This document is checked against `origin/main` at `3dfc5cb8f`, on 2026-11-25. It amends
[spec.md](spec.md). **Where the two disagree, this document wins.**

## Spec concepts and what the repository already has

| Spec concept | What the repository already has |
| --- | --- |
| `provider/owner/name` | `projectpolicy.Canonical` (`internal/projectpolicy/match.go`). The allow-list (`allow_repos`, `projectpolicy.MatchRepo`) and `clone_urls` are keyed by it already (F1) |
| The map on the node | `projectpolicy.Policy.ImageMap` and the `ImageMap` type, escaped by `escKey`/`unescKey`, and checked by `ValidImageMap` |
| The map in the local config | `localcfg.Config.ImageMap`, read through `ImageMapEffective` (the keep rule, D1140). The shared config refuses it (`localcfg/shared.go`) |
| The provider of a repository | `localcfg.Repo.Provider`, else the checkout's `git.provider`, through `cloudEnv.repoProvider` (`internal/cli/project_job.go`) |
| The job's lookup | `catalog.ImageRequest.mappedEntry` (`internal/catalog/select.go`) |
| The build-token check | `credsvc.Server.buildEntryRepos` (`internal/credsvc/buildtoken.go`). The request already carries `Provider` (F6) |

## Assumptions

- **A1.** "Enrolled" means what L5's D3350 counts: the local config's `repos`, plus the repositories
  named by a published admit.
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
- **F5 (Gap).** There are more readers than the spec's eleven `ImageRequest` sites. Each is a direct
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
  (`go run ./ee/clidoc`), and `docs_website_cliref_test.go` pins it.

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
| D3376 | The old `image_map` member when `Policy` writes `repo_images` | **Removed in the same write.** An old reader sees no map and refuses loudly. As recommended |
| D3377 | A catalog entry's `Source.Repo` (F9) | **A follow-up, not L11.** As recommended |

## Resolutions

- **R1.** **Provider resolution in the migration**, in order:
  1. the local config's entry (`localcfg.Repo.Provider`);
  2. the providers of the published admits naming the name;
  3. the repository's default-branch `fugaro.yaml` `git.provider`, through L5's reader;
  4. D3375 (every provider).

  A name that is none of these, so not enrolled at all, is dropped with one line, as the spec says.
- **R2.** **The temporary read of the old member** (D3372). `catalog.ImageRequest` carries both maps:
  the lookup tries `repo_images` under `projectpolicy.Canonical(Provider, repo)`, then the old map
  under the bare name.
  - `localcfg`'s keep rule is per member: a local `repo_images` beats the node's. When neither file
    has `repo_images`, the old member is read.
  - A file holding both members reads `repo_images` and ignores `image_map`, and doctor names it.
  - Removing that read is a follow-up one release later (O1), as L5 does with its fold.
- **R3.** **No policy version bump.** Bumping `projectpolicy.Version` would make an older credentials
  service refuse every node it reads, and it re-reads the node on each model call, so runs in flight
  would stop (the HARD RULE). Removing an `omitempty` member is a state older readers already handle
  (F7). L1's stamp fences older CLIs (D3374).
- **R4.** **`image survey --json` goes to version 2.** `setup.repo_images` replaces
  `setup.image_map`. `imageSurveyDocVersion` and the schema's `const` change together, and the
  `images` skill reads the new member.
- **R5.** **`project policy show --json`** prints `repo_images` in the readable spelling (the
  `showPolicy` shadow), and `image_map` while the node still has it.
- **R6.** **`fugaro image map set`** keeps its name: it is a verb, not the member. It writes
  `repo_images` in the local config. Its argument is read by `canonicalRepoArg`'s rule.
- **R7.** **The shared config** refuses `repo_images` for `image_map`'s reason (`localcfg/shared.go`).
- **R8.** **`project repos rm`** removes the repository's `repo_images` key under its provider.

## Open tensions

None.

## Open items

- **O1.** A follow-up one release after L11: remove R2's read of the old member.
- **O2.** A follow-up spec for `Source.Repo` (D3377). L3 moves the catalog's records and may carry
  it.

## UNVERIFIED

- **V1.** That every published admit names its provider. `allow_repos` patterns are
  `provider/owner/name` by `projectpolicy.ValidPattern`, but a wildcard such as `github/acme/*`
  covers names `update` cannot list. The task that implements R1 settles this; a wildcard admit
  without a listed name leaves R1 to step 3 or 4.
- **V2.** That no console or mobile code reads `image_map`. `git grep` finds none in `console/`,
  `mobile/` or `shared/`. Check 940 confirms it on a live installation.
