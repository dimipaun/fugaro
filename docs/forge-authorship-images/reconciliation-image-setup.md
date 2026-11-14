# Forge, authorship and images: reconciled against the image setup skill

**Status: written 2026-11-10, at the owner's ask of the same day.** Read against `origin/main` at
`256106ec` (2026-11-10) and against [docs/image-setup-skill](../image-setup-skill/) — its spec, its
reconciliation and its plan. **Where this file disagrees with [implementation-plan.md](implementation-plan.md)
or [README.md](README.md) about a task's *state*, this file wins.** It changes no task's design,
reopens no answered decision, and approves nothing.

**The ask.** The owner, 2026-11-10: this folder and [docs/image-setup-skill](../image-setup-skill/)
overlap, "about 16 of forge-authorship-images' tasks being image work"; every task here must say
which task of that plan, or which code on `main`, already builds it.

**The answer.** Of this plan's **25 tasks**: **10 are merged** on `main` — the census below read nine,
and **B1** landed after it (`9af1fd50`, #2332, 2026-11-11) — **1 is closed as
superseded** (E0 — and by [docs/roles-scopes](../roles-scopes/), *not* by image-setup), and **14 are
open**, one of which (**C8**) has its first third built (C8a). **The image overlap is four tasks, not
sixteen**: the two merged ones (C3, C4) and the two open ones (C6, and C8's C8b/C8c). What "about 16"
was is the number of tasks *left* when the status row was written — §1.

**Numbers this file holds:** the block **D650–D659**, out of this folder's own reserved D650–D679,
which [README.md](README.md) reserved and which was re-checked free on `main` and in every open pull
request on 2026-11-10 (§5). Of the block, **D650–D653 are written** below and **D654–D659 are held,
unwritten**, for this file's own follow-ups. **No check number is taken**: everything here is a
reading of the tree.

**The image setup skill is a sibling plan, not a newer copy of this one.** It plans the *project
image catalog* as the one place a project's images are defined, a survey of a project's repositories
and a generated `fugaro:images` skill ([docs/image-setup-skill/implementation-plan.md](../image-setup-skill/implementation-plan.md)).
This folder's image work is the *repository-level* chain: the derived image's layer order (C3), the
digest warning at launch (C4) and the dependency-set-keyed shared image (C7, C8). The two meet in
that chain, and only there.

## 1. What "about 16, much overlapping image-setup" was

[docs/status.md](../status.md) carried `forge-authorship-images | about 9/25 | about 16, much
overlapping image-setup`. The second cell was the count of tasks **left** (25 − 9 = 16), read as
though the folder's remaining work were mostly image work. The plan's own task sections, counted
today against `main`, are:

| State | Count | Tasks |
| --- | --- | --- |
| **Merged** (path and symbol on `main`, §2) | 10 | A1, A2a, A2b, A3, A4, B1 (after the census), C3, C4, C7, E1 |
| **Superseded** (closed, winner named) | 1 | E0 — by [docs/roles-scopes](../roles-scopes/) |
| **Open** | 14 | A5, B2, B3, C1, C5a, C5b, C5c, C6, C8, D1, D2, D3, D4, X1 |

Of the 14 open, **C8's C8a is built** (`internal/imagecheck`'s key, lock and superset, 2026-10-30);
C8b and C8c are not, and `checkout: shared` is refused until a release ships them
(`config.SharedCheckoutRefusal`, `internal/config/image.go`).

**Which of them image-setup's subject reaches:**

| Task | State | The overlap |
| --- | --- | --- |
| C3 — lockfiles before source in the derived image | merged | the derived image is the file IS1's resolver and IS2's disabled commands act on |
| C4 — the digest checked at launch | merged | D619's warning is the shape the catalog also kept (project-image-catalog **D1152**) |
| C6 — `SECURITY.md`: baked source and registry readers | open | its shared-key half is merged with C8a; its baked-source half describes an image the catalog now publishes |
| C8 — build, lock, proposal and job update for shared images | open (C8a built) | the shared layer is the one image kind IS1's resolver names and does not define |

That is four of 25, and it is the whole of the overlap: image-setup's plan touches
`internal/infra/spec.go`, `internal/catalog/`, `internal/projectpolicy/`, `internal/cli/setup.go`,
`internal/runner/runner.go`'s checkout opening and the plugin skills; this folder's open tasks touch
`internal/gitprov/`, `internal/task/`, `internal/cli/run.go`, `internal/imagecheck/`, `ee/` and the
live checklist, and nothing in image-setup's plan creates a forge kind, a CI-status read, a context
repository or a change set. The one file the two plans both write is `docs/gcp-live-checklist.md`,
where they hold different numbers: this folder's Checks 230–235 (233 withdrawn, X1's file) against
image-setup's 500 and 501 (IS13's), so neither block is the other's and no entry is shared.

**A second, conditional overlap — a finding (F1).** C3 and C4 are merged, and both were written for
the *per-repository derived image*: C3 orders that image's layers, C4 warns when the running job's
digest is not the build record's. image-setup's **IS1** replaces the resolver's choice with the
project's catalog image (repo-level `image:`/`dockerfile:`/`rebuild:` ignored with a warning) and
**IS2** makes a repository-level build or refresh answer "repo-level images are disabled". Neither is
merged today (IS1 is draft #2170; IS2 is not started), and **D1900 keeps the code**, so nothing C3 or
C4 built is deleted or broken — but when IS1 and IS2 land, the path C3 fixes and C4 watches is no
longer the path a default workflow's job takes. C3's test and C4's warning stay green; the *default
surface* they were written for moves to the catalog. That is the substance behind "about 16": two of
the four overlapping tasks are already merged work whose subject a sibling plan is retiring, not
sixteen tasks waiting to be found in it.

## 2. Every task, and what builds it

Citations are a path and a symbol on `origin/main` at `256106ec`, never a line number. A merged task
names the code that answers it; a superseded task names the winner; a kept task says why nothing in
image-setup or on `main` covers it. The image-setup task numbers are its plan's
([docs/image-setup-skill/implementation-plan.md](../image-setup-skill/implementation-plan.md),
IS1–IS13 with IS6 unused); its own folder's merged count is stale in both directions as of
2026-11-10, so this file cites its *tasks* rather than its status lines.

| Task | State | What builds it / why it is not covered |
| --- | --- | --- |
| **A1** — a stamp environment and a trailer hook in `gitops` | **Merged** | `internal/gitops/stamp.go` (`StampEnv`, `WriteStampHooks`), `internal/gitops/stamp_hook.sh`, pinned by `internal/gitops/stamp_test.go` (`TestStampHookAddsTrailers`, `TestStampHookChainsToRepositoryHooks`, `TestStampHookNeverAddsSignedOffBy`, `TestStampHookRunsUnderNoVerify`) |
| **A2a** — the author in the task, and the repository's opt-in | **Merged** | `task.Spec.Author`, `task.AuthorProblem`, `task.MaxAuthorNameLen` (`internal/task/task.go`); `config.GitAuthorFugaro`, `config.GitAuthorLauncher`, `config.RepoGitAuthorSet` (`internal/config/config.go`); `schemas/task.schema.json`, `schemas/fugaro.schema.json` |
| **A2b** — the launcher fills the author | **Merged** | `localcfg.Config.CommitName`, `localcfg.Config.CommitEmail` (`internal/localcfg/localcfg.go`); the `authorSince` and `gitAuthorSince` gates (`internal/cli/recipes_skew.go`) |
| **A3** — the runner stamps commits as they are made | **Merged** | `internal/runner/runner.go` (`stampHooks`, `stampAuthor`, `writeStampHooks`), pinned by `internal/runner/stamp_test.go` (`TestRunnerVerifiedCommitIsTheStampedCommit`, `TestRunnerOwnCommitsCarryTrailers`, `TestRunnerLogsUnstampedCommitCount`, `TestRunnerReviewAndArtifactNeverStamp`) |
| **A4** — docs | **Merged** | `docs/git-providers.md` §"Commit author and trailers"; `SECURITY.md` §"Known limits" ("A run's commit author is the launcher's claim, and its trailers are a record"); pinned by `internal/cli/docs_commit_author_test.go` (`TestDocsCommitAuthorIsAClaim`, `TestDocsTrailerNames`, `TestDocsSupportedForges`) |
| **A5** — Check 230 (OWNER-RUN) | **Kept** | the check it measures can run — A3 is merged — but the entry is not written: `docs/gcp-live-checklist.md` has no Check 230 section, and X1 (the entry's only writer) is not merged. image-setup writes into the same file with its own numbers (IS13: Checks 500, 501), and no task of its plan writes a Check 230 entry |
| **B1** — one list of forge kinds | **Merged** (the census read **Kept**) | The census: no `internal/gitprov/kinds.go`; `internal/gitprov/gitprov.go` still declared `KindGitHub`/`KindBitbucket` and `internal/gitprov/providers/providers.go` read them ad hoc. image-setup names forge kinds nowhere; its IS10 survey consumes `gitprov.ContentsReader` and so is forge-agnostic. **`9af1fd50` (#2332) landed the task after this census was read**: `internal/gitprov/kinds.go` declares the two constants and `Kinds`, `FromEnv`'s unknown-kind refusal is built from the list, and `internal/gitprov/kinds_test.go` pins `TestKindsListedOnce` and `TestEveryKindSwitchIsInventoried` — [evidence, point by point](../forge-authorship-images-b1-already-landed.md) |
| **B2** — a CI-status read | **Kept** | no `Checks` on `gitprov.Provider` (`internal/gitprov/gitprov.go`). image-setup's IS11 smokes a catalog entry's prebuilt image — a build-time contract check, not a commit's CI status |
| **B3** — GitLab | **Kept** | gated on D610 ("build"); no file under `docs/` reconciles GitLab, and the plan's own first output (the tokens/`Draft:` reconciliation) is unwritten |
| **C1** — `fugaro project sync` for Bitbucket, with `--check` | **Kept** | `internal/gitprov/bitbucket/` has no `list.go`, and `newProjectSyncCmd` (`internal/cli/project_enroll.go`) offers the GitHub App's installed repositories only |
| **C3** — lockfiles before source in the derived image | **Merged** | `image.RenderInput.KeyFiles` and `ShellKeyFiles` (`internal/image/render.go`), pinned by `internal/image/render_test.go` `TestRenderPutsKeyFilesBeforeSource` (merged as `4c3d50ee`). Its subject is the path image-setup's IS1/IS2 retire — F1 above |
| **C4** — the image digest checked at launch | **Merged** | `warnImageDigestDrift` (`internal/cli/run.go`), pinned by `internal/cli/run_image_digest_test.go` `TestRunDigestWarnsOnMismatch`. The catalog kept the same shape (project-image-catalog D1152: `:latest` in the job, a warning at launch) |
| **C5a** — context repositories in the task | **Kept** | no `Spec.ContextRepos` (`internal/task/task.go`), no `--context` flag on `fugaro run`. image-setup's baked repos (IS7's `bake_repos`, IS8b) put a repository's *source in the image*; a context repository is a *read-only clone for one run* — §5 D652 |
| **C5b** — a read-only token for context repositories | **Kept** | no second mint in `internal/gitprov/github/apptoken.go` / `internal/credsvc/github.go`; nothing in image-setup mints a token |
| **C5c** — the runner clones context repositories read-only | **Kept** | no `internal/runner/context_repos.go`. image-setup's IS8 (`internal/runner/runner.go` `openCheckout`, `TestBakedCheckoutOriginMismatchRefuses`) is adjacent — one image, several repositories' sources — and builds the opposite direction: it *refuses* a checkout that is not the job's own repository instead of adding more |
| **C6** — `SECURITY.md`: baked source and registry readers | **Kept** | its second half is merged with C8a (`SECURITY.md` §"Shared images: what the key protects, and what it does not": the key's integrity rules and what they are not a boundary for, pinned by `internal/cli/docs_security_test.go` `TestSecurityMdStatesTheModelAndItsLimits` — `docs_shared_images_test.go` reads the design and this folder's README, not `SECURITY.md`); its first half — that an image holds its source at the baked commit and that anyone with pull rights on its registry reads it, that context repositories are not baked, that builds are Cloud Build cost outside the run budget — is written nowhere: the design's own line, "C6 is not written, so no SECURITY.md paragraph exists to amend yet" (`shared-images.md` §1), was written before C8a's paragraph landed, and what C6's first half owes is still unwritten. §5 D651 |
| **C7** — shared images keyed by the resolved dependency set: a design | **Merged** | `docs/forge-authorship-images/shared-images.md`, pinned by `internal/cli/docs_shared_images_test.go` `TestDocsSharedImagesDesign` |
| **C8** — build, lock, proposal and job update for shared images | **Kept** (C8a built) | **C8a merged**: `imagecheck.Key` (`internal/imagecheck/key.go`), `imagecheck.ParseLock`/`ReadLock`/`Update` (`internal/imagecheck/lock.go`), `imagecheck.Superset` (`internal/imagecheck/superset.go`), `schemas/image-lock.schema.json` (merged as `251d55a4`, `0f1d13b6`). **C8b/C8c are not**: no `internal/cli/image_ls.go`, `image_proposal.go` or `image_prune.go`, no cap warning in `internal/cli/doctor.go`, and `config.SharedCheckoutRefusal` refuses `checkout: shared` until a release ships the images. §5 D650 |
| **D1** — change sets created together, linked PRs | **Kept** | no `Spec.ChangeSet` (`internal/task/task.go`), no `internal/runstore/changeset.go`. Multi-repository runs are outside image-setup's subject entirely (its images hold several repositories' *sources*, never one run across repositories) |
| **D2** — the draft hold | **Kept** | same; `internal/runner/prflow.go` holds no change-set rule |
| **D3** — settle a change set (`ee/coordinator`) | **Kept** | no `ee/coordinator/changeset.go`; also waits on ee-reorg's coordinator move |
| **D4** — advisory merge order | **Kept** | same, and on D3 |
| **E0** — the roles design (D446) | **Superseded** | E0's own text says it is "a separate spec and reconciliation, owned by the owner"; that spec is now [docs/roles-scopes](../roles-scopes/) (spec 2026-11-07, D1580–D1593, tasks RS1–RS10), extending the roles design E0 names (`docs/design/roles.md`). Reconciliation §6's **O5** — "the roles design (D446) is a prerequisite of Slice E's writes; nobody owns it yet" — is answered by that folder, so nothing is left for E0 to write here. Its one shipped output, `fugaro user`, is E1's |
| **E1** — `fugaro user ls\|add\|rm` over the IAM lists | **Merged** | `internal/cli/user.go` `newUserCmd` (registered in `internal/cli/root.go`), pinned by `internal/cli/user_test.go` (`TestUserAddWritesLauncher`, `TestUserAddRefusesDomain`, `TestUserRefusesStaleLocalList`, `TestUserInviteHasNoSecret`); `plugin/skills/working/SKILL.md` names the command |
| **X1** — the live-check entries | **Kept** | `docs/gcp-live-checklist.md` has no Check 230, 231, 232, 234 or 235 section, and `internal/cli/docs_forge_checks_test.go` does not exist. image-setup reserves Checks 500–509 (its own; IS13 assigns 500 and 501), and secrets-brokering's dynamic-delivery holds Checks 510–519 (510 and 511 assigned, 512–519 reserved): neither block is this folder's, and this folder's 230–249 stays reserved |

## 3. What image-setup does *not* cover

Grouped, so the answer to "how much of this folder is really image work" is legible:

- **Slice A** is one owner-run check away from done (A5, Check 230); the code it measures is merged.
- **Slice B** (B1, B2, B3) is forge work: the kind list, CI status and GitLab. None of it is an image.
- **C1** is the Bitbucket listing in `fugaro project sync`; **C5a–C5c** are read-only context
  repositories. Both are repository-and-run work, not image work.
- **Slice D** (D1–D4) is change sets under `ee/`, itself gated on D613/D614/D616/D641/D642 and on
  ee-reorg's coordinator move.
- **X1** is the live-check register.
- **C6** and **C8's C8b/C8c** are the two open tasks image-setup's subject does touch, and §5 asks
  the owner what to do with each.

## 4. What this reconciliation changes in this folder

- The two status lines ([README.md](README.md), [implementation-plan.md](implementation-plan.md))
  now carry the counts of §1 instead of "about 9/25 merged".
- Every task section in [implementation-plan.md](implementation-plan.md) carries a **State** line
  naming its merged symbol, its winner, or why nothing covers it. No task's design, files, tests,
  dependencies or level is otherwise touched.
- [docs/status.md](../status.md)'s row for this folder says what is merged, what is closed and what
  the image overlap actually is, instead of "about 16, much overlapping image-setup".
- Nothing outside `docs/` changes, and no code, schema, policy or image changes: this is a reading.

## 5. Decisions for the owner (D650–D653 written, D654–D659 held)

These are the questions the overlap leaves open. Each is a recommendation with its alternative and
cost, and an **empty answer cell** — answering one is not approving a task. **Neither D650 nor D653
changes any answered decision**: D617–D622, D618's fork-under-user-control and the 2026-11-02
CATALOG FIRST ruling stand as they are.

**Numbering, re-checked 2026-11-10** (the check the README's numbers table asks for, and a reading of
that day only — open pull requests appear and merge, so a later reconciliation re-runs it rather than
trusting this sentence): the highest
decision written on `main` is **D1959**, the top of secrets-brokering/dynamic-delivery's reserved
block (D1930–D1959, D1947–D1959 unused); the highest used in any open pull request is **D2019**, the
top of vision-model's D2000–D2019 (`fugaro/20261110-152350-dcf2`), with
recon-bitbucket-secrets (`fugaro/20261110-150432-32bd`) holding D1960–D1979 and the
bitbucket-workspace-token folder's D1980–D1999. Neither of those reaches this folder's block: this
folder's own **D600–D679** is intact on `main` and unused in every one of those pull requests
(D650–D679 are reserved and unused there), so these decisions take **D650–D653** from it, above D649 (the
highest answer this folder holds) and below the reserved top; **D654–D659** of the block stay held
for this file's own follow-ups.

| # | Question | Recommendation and reason | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D650** | Do image-setup's catalog-first direction (D1900, IS1, IS2) close **C8b/C8c** — the shared-image refresh, its proposal, `fugaro image ls`, the prune, the cap warning and the per-repository job update? | **No: keep them, unscheduled.** The owner's ruling of 2026-11-02 already says so in the design — "stays a later option and is NOT cancelled" (`shared-images.md`, "Relationship to the project image catalog") — and image-setup did not retire the path: IS1's resolver still answers `checkout: shared` with "the shared-layer image, as `MayApply` already does", and `config.SharedCheckoutRefusal` keeps the kind refused rather than removing it. C8a's merged code and the lock schema then keep a consumer in sight | **Close C8b/C8c** and delete the parked path (`config.SharedCheckoutSince`, `SharedCheckoutRefusal`, the `CheckoutShared` kind, `sharedSince`): the tree loses a config kind, a gate and the only consumer `internal/imagecheck`'s key/lock/superset were built for — work the owner paid for once and would pay for again if dependency-set sharing returns | |
| **D651** | **C6**'s first half — `SECURITY.md` saying that an image holds its source at the baked commit, that anyone with pull rights on its registry reads it, that context repositories are not baked and that builds are Cloud Build cost outside the run budget — is written nowhere. Re-point it at the images the catalog publishes (and at image-setup's baked repos, D1901/IS7/IS8b), or write it as it stands about the per-repository derived image? | **Re-point, and keep the task.** The per-repository derived image is the path D1900/IS1/IS2 retire, so a paragraph written about it would describe a job the resolver no longer returns; the catalog's images (shared registry, `deploy/terraform/gcp/modules/installation/registry.tf`) and a baked catalog entry are where a repository's source will actually sit, and that is what a reader needs to know. The second half (the key's integrity rules and what they are not a boundary for) is already merged and stays | **Write it as it stands**: the sentence is true of the derived image while D1900 keeps the code, and it needs no new reading — but it documents a default path, which is the same mistake F1 records for C3 and C4 | |
| **D652** | Do image-setup's baked repos (D1901, IS7, IS8b) replace **C5a–C5c** — the read-only context repositories a run may read? | **No: keep C5a–C5c.** They are different mechanisms with different postures, and image-setup's own reconciliation says why: a baked image is readable by *every* job that runs it (its F18, and O3, which is still open), while a context repository is a read-only clone for one run, outside the work tree, with a token naming exactly that run's repositories. Baking is a speed setting; context repositories are how a run of one repository reads another without that repository's source becoming readable to a third | **Close C5a–C5c** and tell a launcher to bake the other repositories instead: the run then has the sources, but every job on that image can read them, the build needs a cross-repository credential (O3, unanswered), and no per-run narrowing exists | |
| **D653** | If **C8b/C8c** are ever built, is a shared dependency-layer image still a *derived image per key* (as `shared-images.md` §6's migration assumes), or a **catalog entry keyed by the resolved dependency set** (the composability `shared-images.md`'s 2026-11-02 note proposes and image-setup's IS1 would resolve through)? | **A catalog entry, keyed by the dependency set.** The catalog is now the one place a project's images are defined (D1141, D1900, IS1/IS2); an entry keyed by the set gives the shared layer a definition, a build (`fugaro image build`), a smoke and a retention the installation already has, and needs no second registry layout or job-update mechanics. `imagecheck.Key`/`Superset`/`Lock` stay the keying and reuse half, which is what C8a built | **Keep the per-key derived image**: `shared-images.md` §6's migration writes as it stands, and the owner's 2026-11-02 note already allows it later — at the cost of two ways to define an image, which is the duplication image-setup's whole slice A exists to end | |

## UNVERIFIED

- **U1.** Whether IS1 and IS2 land as drafted (IS1 is open as #2170; IS2 is not started). F1's
  conditional overlap — C3's and C4's subject leaving the default path — is written on that
  assumption and says so; if either task changes shape, F1 is re-read, not assumed.
- **U2.** The image-setup folder's own merged count: its README says 4/14 and its
  [status.md](../status.md) row says 5/14, while `main` carries IS3, IS4, IS5, IS7, IS8, IS9, IS10,
  IS11 and IS11b (nine). This file cites its tasks, never those lines; correcting them is that
  folder's own sweep.
- **U3.** Whether a catalog entry can carry the dependency-set key without a schema change: IS5's
  policy node has `image_map` and `bake_repos` today and no key field. D653 assumes an entry
  *reference* is enough and the key lives in `internal/imagecheck`'s lock, as C8a built it.
