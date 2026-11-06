# Reconciliation: the issue's claims against the code on `main`

This file reads [spec.md](spec.md) — issue **#1738**, verbatim — against the code as it is on `main`
(`495104e5`, read 2026-11-06), and says for each claim whether it is **Wrong**, **Exists**, a **Gap**
or a **Rule**, or is left **Open** for the owner. Nothing here is a task approval: the tasks are
[implementation-plan.md](implementation-plan.md), and the owner's answers are
[README.md](README.md) §Open items.

**How this was read.** Every claim was checked with `git grep` and by reading the named function, not
by reading a summary of it. **Citations name a path and a symbol, never a line number**, because a
line number is wrong the next time the file is touched and this folder has to survive that. A claim
this file could not settle from the tree is under **§6 UNVERIFIED**, not guessed at.

**Verdicts used here.** *Wrong* — the issue states something the code does not do. *Exists* — the
code already does what the claim says, and the claim adds nothing. *Gap* — the claim is true and the
thing it wants is missing. *Rule* — the claim is a statement of how things must stay, and the code
enforces it. *Open* — the tree cannot settle it; the owner decides.

**The one thing to read first.** `docs/project-image-catalog/context-design.md` — merged on `main`,
in the folder whose spec this issue's feature belongs to — is an **unbuilt, unapproved amendment
titled "A repository build context for catalog `dockerfile` entries"** that already designs exactly
this issue's ask, in this repository's own form: its own gaps **G1–G4**, decisions **A1–A8**, open
items **O1–O6** and build tasks **C1–C5**. It takes no `D` number and no `Check` number. It is the
single most important finding in this file (**F1**, **D1408**), and it is the reason the owner's
first answer is needed before any task below can start.

---

## 1. The issue's claims, one by one

| # | The issue's claim (paraphrased; [spec.md](spec.md) is the copy that wins) | Verdict | Where it lands in the tree |
| --- | --- | --- | --- |
| 1 | A catalog entry from a repository Dockerfile is built with a context that holds **only the Dockerfile** | **Exists** | `internal/backend/gcp/build.go`'s `CatalogDockerfileStep` creates `/workspace/context` and copies one object into `/workspace/context/Dockerfile`; `internal/backend/gcp/catalog.go`'s `catalogBuildSteps` says so in words ("a catalog build never clones a repository into the build context") |
| 2 | The catalog build **copies that one file into the project's bucket** | **Exists** | `internal/config/catalog.go`'s `CatalogDockerfileKey` (`builds/catalog/<entry>/Dockerfile`), written by `internal/cli/catalog_copy.go`'s `writeCatalogDockerfile` (D1140) |
| 3 | The build runs `docker build … /workspace/context` on **a folder containing nothing else** | **Exists** | `images/derived/cloudbuild.yaml`'s build step, reused by the catalog build through `internal/backend/gcp/catalog.go`'s `catalogBuildSteps`; the context directory is made by `CatalogDockerfileStep` and nothing else writes into it |
| 4 | So **every `COPY`/`ADD` of a repository file fails** | **Exists** | Docker resolves `COPY`/`ADD` against the context; the context holds one file named `Dockerfile`. A `COPY Dockerfile …` would resolve; every `COPY <repo path> …` the issue shows cannot |
| 5 | A **typical CI image** is built from the repository root as context (`docker build -f ci/Dockerfile-java .`) | **Exists** | This is exactly the shape the per-repository derived build uses: `images/derived/Dockerfile.tmpl` builds from a checkout of the repository. The *typicality* is the reporter's experience, not a fact this tree can check — see **§6** |
| 6 | Today the operator either **publishes a prebuilt** (`kind: image`) and gives up build, smoke and nightly rebuild | **Exists** | `internal/config/catalog.go`'s `SourceImage` is used as it is; `internal/imagecheck/catalog.go`'s `CheckCatalog` skips `SourceImage` entries (nothing to build, nothing to rebuild), and `internal/infra/readiness.go`'s `Readiness` treats a prebuilt entry as built |
| 7 | …or **rewrites every `COPY` as a `RUN --mount=type=secret,id=git-credentials …` step that clones the repository** inside the build | **Exists** | The secret id is real and available to any Dockerfile a build runs: `images/derived/cloudbuild.yaml`'s build step passes `--secret "id=git-credentials,src=/creds/git-credentials"`, and `images/derived/Dockerfile.tmpl` clones under `--mount=type=secret,id=git-credentials`. The workaround works. It is a **first** clone in a catalog build, not a second one — see claim 10 |
| 8 | The reason for the Dockerfile-only copy (one read path, a record of exactly what was built, no full checkout for one file) is **sound** | **Rule** | Stated in `internal/backend/gcp/catalog.go`'s `catalogBuildSteps` and in `internal/config/catalog.go`'s `Catalog`/`CatalogDockerfileKey` doc comments. It is the design's own sentence, and this issue proposes to keep it for entries that name no context |
| 9 | An optional **build context**: the repository the Dockerfile is in, or a directory of it, checked out at the exact commit the Dockerfile was read at | **Gap** | `internal/config/catalog.go`'s `Source` has `Kind`, `Repo`, `Path`, `Branch`, `Inline`, `Image` and **no context field**; `validateCatalogSource` and `addCatalogWrongKind` enumerate exactly those, so an unknown key is refused twice over: by the strict decode and by the wrong-kind rule |
| 10 | The **Dockerfile's sha256 plus the commit SHA the context was checked out at**, in the build record and the image's revision label | **Gap** (half exists) | The sha256 exists: `internal/backend/gcp/catalog.go`'s `CatalogDefinition.SHA256`, the content tag `_CATALOG_TAG`, and `catalogPinStep`'s `sha256sum /workspace/context/Dockerfile`. The revision label exists: the build step's `--label org.opencontainers.image.revision=$$commit` with `--build-arg FUGARO_COMMIT=$$commit`. **One** commit field exists (`CatalogRecordState.SourceCommit`), and it is the operator's checkout HEAD, not a commit the Dockerfile was read at — see claim 11 |
| 11 | …at the **exact commit the Dockerfile was read at** | **Wrong** | `internal/catalog/fetch.go`'s `fetchDockerfile` calls `r.Contents(ctx, s.Path, s.Branch)` — a ref read, which resolves a branch and never answers a commit. `internal/cli/image_build_catalog.go` sets the record's commit from `gitRead(ctx, root, "rev-parse", "HEAD")` — the operator's own checkout, whose doc comment says "The commit every entry's build is pinned to: the checkout's own HEAD". So today the Dockerfile bytes are read at a branch and the recorded commit is a different repository's HEAD |
| 12 | The nightly catalog check would treat **a change under the context directory** on the branch (or a narrower path list, "like `rebuild.paths`") as a definition change | **Gap** | `internal/imagecheck/check.go`'s `DecideCatalog` compares exactly one definition input, `in.DefinitionSHA256 != rec.DefinitionSHA256`. `rebuild.paths` is a **workflow** key (`internal/config/config.go`'s `Rebuild.Paths`, `internal/cli/settings_set.go`'s `workflows.*.rebuild.paths` and `layer.profiles.*.rebuild.paths`) and has no catalog counterpart — `internal/config/catalog.go` mentions no `rebuild` at all. And `internal/cli/image_check_catalog.go`'s `evaluate` sets `Lockfiles: false` because the job "has no checkout of it and no git credential to read one with" — the same reason a context-change trigger could not be evaluated there as things stand |
| 13 | **The credential stays out of the image**: the checkout uses the repository's provider credential **outside** the Docker build, **as the per-repository build already does**, so no step in the Dockerfile ever sees it | **Wrong** | This is the issue's one factually wrong sentence, and it is wrong about the comparison it rests on. The per-repository build does **not** clone outside the Docker build: `images/derived/cloudbuild.yaml`'s build step passes `--secret "id=git-credentials,src=/creds/git-credentials"` **into** `docker build`, and `images/derived/Dockerfile.tmpl` is where the clone happens, inside a `RUN --mount=type=secret,id=git-credentials`. The credential is not in the image (a BuildKit secret is not a layer), but it *is* available to every `RUN` in every Dockerfile those builds run — the catalog entry's own Dockerfile included. See **§4.3** |
| 14 | The **existing machinery** can be reused: per-repository derived builds already clone the repository with its credential, "so this reuses that path rather than adding a new one" | **Wrong** (in the sense meant) | The machinery that clones is inside the Docker build (claim 13). A context checkout that happens **outside** the Docker build — which is what the issue asks for, and what `docs/project-image-catalog/context-design.md` §A3 designs — is a **new step** in `catalogBuildSteps`, not a reuse. The pieces it would reuse are real and named below (§3.4), but they are not the clone |
| 15 | An entry **without** `context:` keeps today's Dockerfile-only build | **Rule** | An added optional key changes nothing for entries that omit it, provided the decode stays strict about the rest — `internal/localcfg`'s `Parse` sets `dec.KnownFields(true)` and `internal/projectpolicy/policy.go`'s `decodeImageCatalog` sets `DisallowUnknownFields` |

**The shape of the whole thing.** Twelve of the fifteen claims are accurate about today's code, and
the issue is a well-founded bug report: a catalog `dockerfile` entry cannot build the Dockerfile
shape teams actually have. Three are **Wrong**, and two of them — claims 13 and 14 — are the same
error seen twice: the issue believes the per-repository build clones **outside** the Docker build and
so can add a context checkout "rather than a new path", and it does not (F1, F3). The third, claim
11, is that "the exact commit the Dockerfile was read at" names something the code never produces
(F2). None of the three weakens the ask; the first two change the security analysis (§4.3), and the
third changes what the design has to build.

---

## 2. How a catalog `dockerfile` entry is built today

The path, end to end, in the order the bytes move.

**2.1 The definition lives in the operator's policy node.** `internal/config/catalog.go`'s `Catalog`
is `Entries []Entry`; an `Entry` has `Name`, `Tags`, `Source`, `Description`, `Default`. A
`Source` of `Kind: dockerfile` carries `Repo`, `Path` and an optional `Branch`. It is published under
`image_catalog` by `internal/projectpolicy/policy.go`'s `Policy.ImageCatalog` (type alias
`ImageCatalog = config.Catalog`), read by the runner, and refused in the shared config by
`internal/cli/sharedcfg.go`'s `sharedForbidden` (D1140). `internal/localcfg/localcfg.go`'s
`ImageCatalogEffective` is the keep rule: a config that is silent keeps the node's catalog.

**2.2 The operator copies the Dockerfile into the bucket.** `internal/cli/catalog_copy.go`'s
`writeCatalogDockerfile` reads the file through `internal/catalog/fetch.go`'s `Fetch` →
`fetchDockerfile` (`gitprov` `Contents(path, branch)`), compares it with what is already at
`internal/config/catalog.go`'s `CatalogDockerfileKey` (`builds/catalog/<entry>/Dockerfile`), and
writes only if it differs. The ceiling is `internal/catalog/fetch.go`'s `MaxDockerfileBytes`
(`gitprov.MaxContentsBytes`, 1 MiB).

**2.3 The build is submitted as the entry's repository's build account.**
`internal/cli/image_build_catalog.go`'s `runImageBuildCatalog` reads the copy back with its
generation and sha256 and calls `internal/backend/gcp/catalog.go`'s `SubmitCatalog` with a
`CatalogBuildSpec` carrying `RepoURL`, `BaseBranch`, `GitSecretID`, `Base`, `Image`, `Definition`,
`ServiceAccount`, `MachineType`, `WorkflowSecrets`, `Bucket`, `Selftest`. The spec's `check` method
refuses a missing field. The account is the repository the entry belongs to — `internal/infra/catalogcheck.go`'s
`catalogCheckRepos` assigns a Dockerfile-sourced entry to the repository its `source` names, and
`internal/backend/gcp/names.go`'s `CatalogRecordsCondition` doc says that assignment is what makes
"the entry's repository a fact rather than a label".

**2.4 The Cloud Build steps.** `internal/backend/gcp/catalog.go`'s `catalogBuildSteps` reuses
`images/derived/cloudbuild.yaml`'s steps **by id**, replacing the derived build's
`source`/`render`/`gate`/`record` with `dockerfile`/`catalog-pin`/`catalog-tag`/`catalog-record`. The
sequence is `prep`, `credential`, `dockerfile`, `catalog-pin`, `build`, `smoke`, `promote`,
`catalog-tag`, `catalog-record`, `untag`.

**2.5 The context the build sends.** `internal/backend/gcp/build.go`'s `CatalogDockerfileStep` is the
whole of it: `install -d -m 0755 /workspace/context`, then
`gcloud storage cp --quiet "$$BUCKET/$$KEY" /workspace/context/Dockerfile`. Its comment: "a catalog
build reads one object instead of a checkout". The build step then runs `docker build … /workspace/context`.
That directory holds one file.

**2.6 The record.** `catalogPinStep` verifies the object's generation, runs
`sha256sum /workspace/context/Dockerfile`, and writes `/workspace/out/source-commit` from the
`FUGARO_COMMIT` build-arg. `catalogRecordStep` writes `internal/imagecheck/check.go`'s
`CatalogRecordState` to `internal/backend/gcp/catalog.go`'s `CatalogRecordKey`
(`builds/<slug>/catalog/<entry>/image.json`) with one `objects.create` — not `gcloud storage cp`,
because the build account's write grant is an object-scoped `objectUser` condition and `cp` would
need a bucket-level list.

**2.7 The nightly check.** `internal/imagecheck/check.go`'s `DecideCatalog` evaluates, in order:
`force`, no record, the copied definition's sha256, the base ref and digest, a `Lockfiles` boolean,
and `max-age`. `DefinitionGeneration` is carried in the record and **not compared**; the comparison
is on `DefinitionSHA256` alone. `internal/cli/image_check_catalog.go`'s `evaluate` builds the inputs
from the record, the copied Dockerfile and the previous check state, hardcodes `Lockfiles: false`,
and uses `config.DefaultMaxAge`. `internal/imagecheck/catalog.go`'s `CheckCatalog` runs one
`DecideCatalog` per buildable entry. The check job runs as its own account
(`internal/infra/tf/cover.go`'s `CatalogCheckAccountID`) and submits each entry's build as that
entry's repository's build account through `deploy/terraform/gcp/modules/repo/catalogcheck.tf`'s
`google_service_account_iam_member.catalog_check_build` (`roles/iam.serviceAccountUser` and nothing
else).

**2.8 What the issue compares with: the per-repository build.** `images/derived/cloudbuild.yaml`'s
`source` step is `git clone --quiet --depth 1 --branch "$$BASE_BRANCH" -- "$$REPO_URL" /workspace/src`
with `credential.helper='store --file=/creds/git-credentials'`, and its build step runs
`docker build … --secret "id=git-credentials,src=/creds/git-credentials" … /workspace/context`. So
the per-repository path has **two** credential uses: a clone outside the Docker build (into
`/workspace/src`, for the cache warm-up) and the BuildKit secret inside it (for the clone the
Dockerfile itself does). The issue's claim 13 sees the first and misses the second.

---

## 3. What a repository build context would mean

**3.1 For `COPY` and `ADD`.** `COPY <src> <dst>` resolves `<src>` against the context root. Today the
root is a directory holding one file. If the root became a checkout of the repository (or of a
subdirectory of it), the issue's three lines resolve as written, with no Dockerfile change at all —
which is the point, and the reason a `context:` key is worth more than a documented workaround.

**3.2 The commit question.** "Checked out at the exact commit the Dockerfile was read at" needs a
commit, and today there is not one to name: `internal/catalog/fetch.go`'s `fetchDockerfile` reads at
a **branch** (`r.Contents(ctx, s.Path, s.Branch)`), and the record's `SourceCommit` is the operator's
checkout HEAD from `internal/cli/image_build_catalog.go`. A context design has to choose one of:
the branch head resolved at build time (simple, and the Dockerfile and the context can then disagree
— the exact failure the issue's "one record" bullet exists to prevent); the operator's HEAD (a
commit that need not exist in the repository the entry names at all); or a commit resolved **once**,
at copy time, and carried in the node or the bucket beside the Dockerfile. The third is what makes
"one commit" true, and it is what `docs/project-image-catalog/context-design.md` §A2 designs.
The mechanism for checking out a named commit already exists inside `images/derived/Dockerfile.tmpl`:
`git cat-file -e "$commit^{commit}"` falls back to `git fetch --quiet origin "$commit"`, then
`git reset --quiet --hard "${FUGARO_COMMIT:-HEAD}"` — a shallow `--depth 1 --branch` clone does not
contain an arbitrary commit, so the fetch-by-sha fallback is load-bearing there and would be here.

**3.3 Which repository, and which directory.** The issue's example is `context: .` — the repository
root — with a subdirectory such as `ci/` allowed. Two sub-questions the tree cannot answer: whether
the context may name a repository **other** than `source.repo` (the issue's wording says "the
repository the Dockerfile is in", which forbids it), and whether `context: ci/` means the context
*root* is that directory (`COPY maven/settings.xml …` then resolves) or means "the whole repository,
filtered to `ci/`" (the same `COPY` would need `ci/maven/…`). The two readings are not
interchangeable and both are plausible from the issue's text.

**3.4 Where the bytes would come from, and what that costs.** The context's bytes are the
repository's, at whatever commit §3.2 settles on. They are read by the **build account** of the
repository the entry belongs to, which already holds `roles/secretmanager.secretAccessor` on that
repository's build secrets (`deploy/terraform/gcp/modules/repo/build.tf`'s
`google_secret_manager_secret_iam_member.build`) and `roles/artifactregistry.reader` on the base and
ghcr registries. Nothing new is needed to *reach* the repository — `RepoURL` and `GitSecretID` are
already in `CatalogBuildSpec`. What leaves the machine is unchanged from today's derived build: the
repository's own contents, fetched into the project's own Cloud Build workspace, never into a
Fugaro-hosted service. What is new is that the fetched bytes are **the build context**, so they are
copied into the builder's filesystem and any of them may end up in an image layer.

---

## 4. The security analysis, as an attacker would see it

**4.1 A repository-authored context is repository text that decides what the build executes.** This
is the load-bearing change. Today a catalog entry's Dockerfile is operator-published text (D1140),
and a repository that has write access to the entry's `source.repo` can already change the Dockerfile
the entry names — but only at the moment the operator next copies it, and the operator sees the
Dockerfile. A context adds files the operator never reads: `COPY` of a script, an `.m2/settings.xml`,
a `.npmrc`, a `requirements.txt` — anything the Dockerfile then executes. The catalog's own ruling is
that the catalog is operator-owned precisely because it decides what code a run executes
(`internal/projectpolicy/policy.go`'s `Policy.ImageCatalog` doc: "the catalog decides what code a job
runs and which Dockerfile is fetched to build it"). A context extends that decision to the whole
repository, and the owner has to say whether that is acceptable, and with what review.

**4.2 The build account's permissions, and why the context does not widen them.** The build account's
grants are narrow and each is conditioned: `roles/artifactregistry.writer` plus the installation's
`tag_mover` on its **own** registry and on the installation's **shared** registry
(`deploy/terraform/gcp/modules/repo/registry.tf`), `reader` on the base and ghcr registries, and on
the runs bucket `roles/storage.objectUser` conditioned to `builds/<slug>/` plus
`roles/storage.objectViewer` conditioned to `builds/catalog/`. That last one is the one to read:
`internal/backend/gcp/names.go`'s `CatalogReadCondition` and `CatalogBucketPrefix` say the grant is
`objectViewer` and not `objectUser` on purpose, because "a build account that could write one could
make every other repository's jobs run an image built from a definition of its choosing". **A context
feature must not turn that read into a write**, and it must not need to: a checkout is read-only with
respect to the bucket. If a context design decides to stage the context **in the bucket** (for
instance so the nightly check can hash it without a git credential, §4.6), that decision moves bytes
into `builds/catalog/`, which today only the operator writes — and it must not become a path by which
a build account writes there. This is the sharpest security fork in this reconciliation.

**4.3 Secrets in the context, and the credential.** Two separate things, often confused:

- **The git credential.** It is a BuildKit secret, `--secret id=git-credentials`, mounted at
  `/creds/git-credentials` in `images/derived/cloudbuild.yaml`'s build step and consumed by
  `images/derived/Dockerfile.tmpl`. It is not a layer, so it is not in the image — but it **is**
  readable by every `RUN` of every Dockerfile those builds run, including a catalog entry's. The
  issue's claim that no step in the Dockerfile ever sees it is wrong (§1 claim 13). A context design
  that clones **outside** the Docker build would *reduce* the Dockerfile's need for that secret — but
  the secret is passed to `docker build` unconditionally today, so the reduction only happens if the
  design also stops passing it. That is a separate, owner-visible decision.
- **Secrets the repository itself ships.** A context makes it trivial for a build to `COPY` a file
  from the repository into the image: a `.env`, a service-account JSON, a `.npmrc` with a token.
  Today's Dockerfile-only context makes that impossible by construction. This is the concrete cost of
  the feature and belongs in the owner's answer, not in a footnote. The engine cannot detect it; only
  review can, and only if the owner decides review is where it lives.

**4.4 Size.** There is no context size limit today because there is no context. The only ceiling in
the neighbourhood is `internal/catalog/fetch.go`'s `MaxDockerfileBytes` (1 MiB), which bounds one
file. A full checkout is unbounded by the engine: a repository with a large binary in it becomes the
build context, and Cloud Build's workspace, the `docker build` context upload and the resulting
image layers all grow with it. `docs/project-image-catalog/context-design.md` §A6 has a bound and
what a huge context costs; this folder's plan inherits whatever the owner answers in **D1398**.

**4.5 `.dockerignore`.** Nothing in the tree reads, requires or generates a `.dockerignore`
(`git grep` for it finds nothing under `images/`, `internal/` or `deploy/`). With a checkout as the
context, the repository's own `.dockerignore` at the context root would be honoured by Docker — which
is good for size and bad for predictability, because it is repository-authored text that changes what
the build sees, and a build that works in CI can then fail in Fugaro or the other way round. Whether
the engine requires one, forbids one or ignores the question is **D1399**.

**4.6 The nightly check, and the one place the feature could widen a grant.** A context-change
trigger needs the check to know the context's current bytes. The check job has **no checkout and no
git credential** — `internal/cli/image_check_catalog.go`'s `evaluate` says so in the comment on
`Lockfiles: false` — and it holds no secret accessor
(`deploy/terraform/gcp/modules/installation/catalogcheck.tf` gives it a definitions read on
`builds/catalog/` and a records read/write under `builds/<slug>/catalog/`, and nothing else). So a
context trigger has two shapes: **give the check a credential** (widening an account whose whole
design is that it has none), or **stage a hash of the context beside the Dockerfile in
`builds/catalog/`** at copy time, so the check compares a stored hash as it compares the definition
sha256 today. The second is the one that fits the existing design, and it is the one that touches the
`objectViewer`-only grant of §4.2 — the operator would have to write that object, never a build
account.

**4.7 The removed repository-image path, and how this interacts.** `docs/repo-image-removal/` is a
design (owner ruling 2026-11-04; decisions D1190–D1208 all answered; slot 0.20.0) that **removes the
per-repository image path and makes the catalog the only image path**, with tasks R0–R2 in open pull
requests. Its **D1208** answers how a migrated entry bakes the repository's own dependency cache:
"the entry's own Dockerfile does the clone, the warm-up and a final `finalize-checkout`, and the
engine gains NO bake mode" — because "an engine-side bake keyed by repository re-creates the
per-repository build input inside the catalog, which is the thing the ruling removes". That is a
direct hit on this issue:

- **D1208 already ships the workaround this issue complains about**, as the *required* shape of every
  migrated entry. After the removal, the dogfood repository's catalog entry is a hand-written
  Dockerfile that clones `$REPO_URL` at `$FUGARO_COMMIT` with `--mount=type=secret,id=git-credentials`
  and warms the cache itself.
- So a `context:` key is not neutral against that ruling: it is an engine-side mechanism that
  re-creates, inside the catalog, the very input D1208 refused to put there. The difference is real
  and worth stating — D1208 refused a **bake** keyed by repository (engine-side cache state), while
  `context:` is a **checkout** of repository bytes the Dockerfile then decides what to do with — but
  the owner is the one who decides whether that difference is enough. This is **D1409**.
- `docs/project-image-catalog/context-design.md` §O4 asks the same ordering question ("Which release
  carries this build, and in what order against the removal?") and answers it nowhere, by design.

**4.8 What the feature does not widen.** Stated plainly, so the analysis is not read as alarmist: a
`context:` key adds no IAM grant, no secret, no network path and no new account. The bytes it fetches
are bytes the build account can already fetch today, with the credential it already has. The change
is in **what those bytes are used for** — from "one operator-read Dockerfile" to "a whole repository
tree that decides what runs" — and in **who can change them** between the operator's review and the
build: anyone with write access to the entry's branch.

---

## 5. Findings

Each finding names a class and cites a path and a symbol. **Wrong** is a claim that does not match
the code; **Exists** is something already there; **Gap** is something missing; **Rule** is a
constraint the code enforces and a design must keep; **Open** is a question only the owner settles.

### Wrong

- **F1. The issue's security sentence is wrong about the per-repository build.** `images/derived/cloudbuild.yaml`'s
  build step passes `--secret "id=git-credentials,src=/creds/git-credentials"` into `docker build`, and
  `images/derived/Dockerfile.tmpl` clones inside it. Claim 13 of §1 ("the checkout uses the credential
  outside the Docker build, as the per-repository build already does") describes a build this
  repository does not have. The conclusion the issue draws from it — that a context feature cannot
  put the credential in front of the Dockerfile — is not established by the code as it stands.
- **F2. The issue's "exact commit the Dockerfile was read at" has no referent today.**
  `internal/catalog/fetch.go`'s `fetchDockerfile` reads at a ref (`r.Contents(ctx, s.Path, s.Branch)`),
  and `internal/cli/image_build_catalog.go` records the operator's `rev-parse HEAD`. There is no
  commit at which "the Dockerfile was read", so a design has to create one rather than read one.
- **F3. "Reuses that path rather than adding a new one" understates the change.** The cloning
  machinery the issue points at lives inside the Docker build (F1). A checkout outside the Docker
  build is a new step in `internal/backend/gcp/catalog.go`'s `catalogBuildSteps`; the parts that
  *are* reusable are `CatalogBuildSpec`'s `RepoURL`/`GitSecretID`/`BaseBranch` and the `credential`
  step `images/derived/cloudbuild.yaml` already has.

### Exists

- **F4. The Dockerfile-only context, and its reason, are exactly as the issue describes.**
  `internal/backend/gcp/build.go`'s `CatalogDockerfileStep` and `internal/backend/gcp/catalog.go`'s
  `catalogBuildSteps`.
- **F5. The operator-owned catalog.** `internal/projectpolicy/policy.go`'s `Policy.ImageCatalog` and
  `internal/cli/sharedcfg.go`'s `sharedForbidden` (D1140). The catalog is project state, not
  repository state: a repository names an entry (`config.Workflow.ImageName`) or states `needs:`
  tags, and cannot define one.
- **F6. The prebuilt escape hatch, and what it costs.** `internal/config/catalog.go`'s `SourceImage`;
  `internal/imagecheck/catalog.go`'s `CheckCatalog` skips those entries, so a prebuilt entry gets no
  scheduled rebuild — the issue's "gives up Fugaro's build, smoke and nightly rebuild" is accurate.
- **F7. The BuildKit-secret workaround.** The secret id and the mount pattern the issue names are
  real and already used by `images/derived/Dockerfile.tmpl`.
- **F8. The sha256 half of the record.** `internal/backend/gcp/catalog.go`'s `CatalogDefinition.SHA256`,
  `catalogPinStep`, and the content tag `_CATALOG_TAG`.
- **F9. The revision-label half.** The build step's `--label org.opencontainers.image.revision` with
  `--build-arg FUGARO_COMMIT`; `CatalogRecordState.SourceCommit`.
- **F10. The strict decode that will refuse `context:` until it is taught.** `internal/localcfg`'s
  `Parse` (`dec.KnownFields(true)`), `internal/projectpolicy/policy.go`'s `decodeImageCatalog`
  (`DisallowUnknownFields`) and `checkMemberNames`; `internal/config/catalog.go`'s
  `validateCatalogSource`/`addCatalogWrongKind` refuse a field that belongs to no kind.
- **F11. The build account already reaches the repository.** `CatalogBuildSpec`'s `RepoURL` and
  `GitSecretID`, and `deploy/terraform/gcp/modules/repo/build.tf`'s
  `google_secret_manager_secret_iam_member.build`. A context needs no new grant to *fetch*.
- **F12. The exact-commit checkout mechanism.** `images/derived/Dockerfile.tmpl` already does
  `git cat-file -e "$commit^{commit}"` → `git fetch origin "$commit"` → `git reset --hard`.
- **F13. The catalog check job's account, and its shape.** `internal/infra/tf/cover.go`'s
  `CatalogCheckAccountID`; `deploy/terraform/gcp/modules/installation/catalogcheck.tf`'s definitions
  read (`builds/catalog/`) and records read/write (`builds/<slug>/catalog/`); the per-repository
  `actAs` in `deploy/terraform/gcp/modules/repo/catalogcheck.tf`.

### Gap

- **F14. There is no `context` field, in either grammar.** `internal/config/catalog.go`'s `Source`
  has five fields and no context; `schemas/settings.schema.json` lists `image_catalog.entries.*.source.{kind,repo,path,branch,inline,image}`
  and nothing else. A new key is a change in **two** grammars (the local YAML and the node's JSON,
  which share the struct) plus the settings key list.
- **F15. `context: .` — the issue's own example value — is the value the sibling key refuses.**
  `internal/config/catalog.go`'s `catalogPathMessage` returns "must name a Dockerfile, not the
  repository root" for exactly `"."`. If `context:` reuses `catalogPathMessage`, the issue's example
  is refused by construction; if it does not, the two sibling path keys disagree about what a path
  may be. **D1392.**
- **F16. No commit is pinned anywhere in the copy path.** See F2. The copy is a ref read plus the
  operator's HEAD.
- **F17. The nightly check compares one input and cannot see a context.** `internal/imagecheck/check.go`'s
  `DecideCatalog` compares `DefinitionSHA256` only; `DefinitionGeneration` is carried and unused;
  `internal/cli/image_check_catalog.go`'s `evaluate` has no checkout and no credential. A
  context-change trigger needs a new input, and §4.6 names the two ways to supply it. **D1401.**
- **F18. `rebuild.paths` has no catalog counterpart.** `internal/config/config.go`'s `Rebuild.Paths`
  is a workflow/profile key (`internal/cli/settings_set.go`'s `workflows.*.rebuild.paths`,
  `layer.profiles.*.rebuild.paths`); `internal/config/catalog.go` has no `rebuild` at all. The
  issue's "a narrower list of paths, like `rebuild.paths`" would be a new key in the catalog grammar.
  **D1401** covers it.
- **F19. No context size bound exists.** §4.4. The only nearby ceiling is
  `internal/catalog/fetch.go`'s `MaxDockerfileBytes` (1 MiB, one file). **D1398.**
- **F20. No `.dockerignore` handling exists anywhere.** `git grep` finds none under `images/`,
  `internal/` or `deploy/`. §4.5. **D1399.**
- **F21. The context would be cloned by a step that does not exist.** `catalogBuildSteps` has no
  checkout step; the `source` step it replaces is dropped for catalog builds by design
  (`CatalogDockerfileStep`'s "reads one object instead of a checkout"). A context entry re-introduces
  a clone into a path that was built to avoid one, and it must do so **without** the derived build's
  `/workspace/src` cache warm-up, which is a per-repository concern D1208's ruling moved into the
  entry's own Dockerfile. **D1396.**
- **F22. No skew gate exists for a new source key.** `internal/infra/readiness.go`'s
  `imageCatalogShipped` is the precedent: the release that published `image_catalog` flipped a gate
  so an older runner refuses rather than silently misreads. A `context` key needs the same treatment
  before a node carrying it is written. **D1403.**

### Rule

- **F23. An entry without `context:` must keep today's build, byte for byte.** The issue's last
  paragraph, and the design's own sentence in `internal/backend/gcp/catalog.go`'s `catalogBuildSteps`.
- **F24. The catalog is operator-owned, and a build account must never be able to write a definition.**
  `internal/backend/gcp/names.go`'s `CatalogReadCondition` (`objectViewer`, not `objectUser`, on
  `builds/catalog/`) and `deploy/terraform/gcp/modules/repo/build.tf`'s
  `google_storage_bucket_iam_member.catalog_read`. Any staging of context bytes in that prefix is the
  operator's write, never a build's. §4.2.
- **F25. A wire format never changes meaning.** `CatalogRecordState` is read by the check, the
  console, mobile and history. Adding a field is fine; retyping `SourceCommit` is not — and a
  *second* commit (the Dockerfile's, beside the context's) has to be a **new** field with a name that
  says which is which. **D1400.**
- **F26. A field never changes meaning, applied to `source`.** `Source.Path` means "the Dockerfile's
  path". A context directory must not be spelled by giving `path` a second meaning. **D1391.**
- **F27. Nothing in this work may edit `.github/workflows/`.** The repository's own rule: GitHub
  refuses an agent run's push of a workflow change. A CI step for this feature would be staged under
  `docs/pending-workflows/` for a person to copy in.
- **F28. Every path and symbol this folder cites must exist on `main`.** Re-verified before this file
  was committed; the two absences found are recorded in §6.

### Open

- **F29. The merged amendment already designs this feature.** `docs/project-image-catalog/context-design.md`
  is titled "A repository build context for catalog `dockerfile` entries" and carries gaps G1–G4,
  decisions A1–A8, open items O1–O6 and tasks C1–C5 for exactly this ask, in the same folder as the
  spec it amends. It is unbuilt and unapproved. **This folder does not silently become a second plan
  for the same feature**: the owner says whether this reconciliation supersedes it, folds into it, or
  stands beside it. **D1408**, and README §Open items.
- **F30. The removal's ruling points the other way.** `docs/repo-image-removal/`'s **D1208** (owner
  ruling 2026-11-04) answered "how does a migrated entry bake the repository's own dependency cache"
  with "the entry's own Dockerfile does the clone … and the engine gains NO bake mode", on the stated
  ground that an engine-side mechanism keyed by repository "re-creates the per-repository build input
  inside the catalog, which is the thing the ruling removes". A `context:` key is an engine-side
  mechanism that re-creates part of that input. **D1409.**
- **F31. The build still hands the git credential to the Dockerfile.** `images/derived/cloudbuild.yaml`'s
  build step passes `--secret id=git-credentials` unconditionally. A context entry does not need the
  Dockerfile to hold it. Whether the design stops passing it — and what that does to an entry that
  legitimately clones inside its own Dockerfile, as D1208's migrated entries do — is the owner's.
  **D1397.**
- **F32. Who reviews the context's contents.** The catalog is operator-owned because it decides what
  code runs (F5). A context widens that decision to a repository tree the operator does not read
  (§4.1). Whether the answer is review at copy time, a documented trust boundary, or nothing, is a
  product decision and not a code fact. **D1394**/**D1395** carry the commit half of it.

---

## 6. UNVERIFIED

Things this file could not settle from the tree, stated as such rather than guessed at.

- **U1. "A typical CI image is built from the repository root as context."** §1 claim 5. The example
  command is the reporter's; this tree has no corpus of CI configurations to check it against. The
  *shape* is certainly buildable by Docker and is what `images/derived/Dockerfile.tmpl` does for the
  per-repository path, but "typical" is not a fact this repository can hold.
- **U2. Whether `git fetch origin <sha>` succeeds against the providers Fugaro supports.** F12's
  fallback exists in `images/derived/Dockerfile.tmpl` and is exercised by the derived path, but this
  file did not read a provider's server-side configuration (`uploadpack.allowAnySHA1InWant` and
  friends) and the tree does not state it. A context design that pins a commit should say what
  happens when the commit is no longer reachable — the derived path's own comment already describes
  that case for `FUGARO_COMMIT`.
- **U3. Cloud Build's own limits on the submitted context.** §4.4. The engine sets no bound; what
  Cloud Build does with a multi-gigabyte context is not stated anywhere in this tree.
- **U4. Whether any consumer outside the CLI reads `CatalogRecordState.SourceCommit` as "the commit
  the image was built from"** in a way that a context would make untrue. F25 requires a new field
  rather than a re-meaning, but this file did not audit every reader (console, mobile, history,
  plugin). The reader that **is** verified is `internal/imagecheck`'s own comparison, which reads
  `SourceCommit` for the workflow check's `paths` trigger and not for catalog entries.
- **U5. Two absences found while reconciling, recorded so they are not cited later.** There is no
  `internal/backend/gcp/grants.go` (the only `grants.go` is `internal/infra/grants.go`, about reading
  a secret's live IAM policy) and no JSON Schema for the policy node's `image_catalog` block
  (`schemas/settings.schema.json` covers the `fugaro settings` keys, not the node). A design that
  wants either is adding it, not extending it.

---

## 7. Decisions D1390–D1409

**Not one of these is answered here.** The owner's-answer cell is empty in every row, and a task in
[implementation-plan.md](implementation-plan.md) may not start before its row is answered. Each row
states the question, what this reconciliation recommends and why, the alternative, and what the
alternative costs.

| # | Question | Recommendation (and why) | Alternative (and its cost) | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1390** | Does a `dockerfile` entry get an optional `context:` at all, given that the per-repository image path is being removed and its replacement (D1208) is a hand-written Dockerfile that clones for itself? | **Yes, but only after D1408 is answered.** The workaround the removal mandates (clone inside the Dockerfile) makes every migrated entry pay for the engine's missing feature in a way the operator has to maintain by hand, and a `COPY` of the repository's own files cannot be expressed as a clone at all | No: keep the Dockerfile-only context and document the clone-the-repository-yourself pattern as the supported shape | Every migrated entry keeps a hand-written clone, a warm-up and a `finalize-checkout` (D1208) that only its author can get right, and a Dockerfile whose `COPY` lines the engine cannot build stays unbuildable. The feature stays a documented workaround forever |
| **D1391** | Where does the context live in the grammar: a `context:` path on `source` beside `path`, or a separate block naming a repository, a path and a branch? | **A path on `source`** (`source.context: ci/`), because F26 forbids re-meaning `source.path` and the issue's own example spells it that way; a separate block would need its own repo/branch and raise D1393 immediately | `source.context: { repo, path, branch }` | A second repository, a second branch and a second ref to validate, copy, pin and skew-gate — the entry stops having "one commit" at all unless the two refs are forced equal |
| **D1392** | Is `context: .` — the issue's own example — allowed, when `catalogPathMessage` refuses exactly that value for `path`? | **Allowed, and the two keys' messages say why they differ**: `path` names a file, `context` names a directory, and the repository root is a legitimate directory | Reuse `catalogPathMessage` unchanged | The issue's own example is refused at validation, and the operator has to spell the root some other way that the grammar then has to invent |
| **D1393** | May `context` name a repository other than `source.repo`? | **No.** The issue says "the repository the Dockerfile is in"; a second repository means a second credential, a second branch and two commits that can disagree | Allow it | The build account's grants, the record's one commit field and the "one commit" property all become per-repository, and the entry's repository stops being "a fact rather than a label" (`CatalogRecordsCondition`) |
| **D1394** | Which commit is the context checked out at: the branch head at build time, the operator's checkout HEAD, or a commit resolved once at copy time? | **Resolved once, at copy time, and carried beside the Dockerfile** — that is what makes "one commit" true, and it is what `docs/project-image-catalog/context-design.md` §A2 designs | The branch head at build time | The Dockerfile and the context can come from different commits, which is the exact failure the issue's "one record of what was built" bullet exists to prevent; a rebuild triggered by a context change can build the wrong Dockerfile |
| **D1395** | Does the Dockerfile also come from that commit, i.e. does the copy stop being a ref read? | **Yes.** `internal/catalog/fetch.go`'s `fetchDockerfile` reads at a branch; the copy should resolve the ref once and read both the Dockerfile and the context at the same commit, recording it | Keep the ref read for the Dockerfile and pin only the context | Two reads, two commits, and the record's sha256 is then of a Dockerfile that need not be the one the image was built from |
| **D1396** | Who performs the checkout: a new step outside the Docker build, or a `RUN --mount=type=secret` inside the entry's Dockerfile? | **Outside the Docker build**, because that is the only shape that can serve a `COPY` (Docker resolves `COPY` before any `RUN`) — inside the build it is not a context at all | Inside the Dockerfile | It is not a context: `COPY` still cannot see the checkout, and the feature does not exist |
| **D1397** | Does the build keep passing `--secret id=git-credentials` into `docker build` for a context entry? | **Keep passing it.** D1208's migrated entries clone inside their own Dockerfile and need it; stopping it for context entries would make the two entry shapes behave differently for no gain, and F1 shows the secret is not in the image either way | Stop passing it for context entries | An entry whose Dockerfile also clones — every entry migrated under D1208 — breaks, and the entry shapes diverge in a way the operator has to remember |
| **D1398** | Is the context bounded, and where? | **Bound it where the Dockerfile is bounded**: a byte ceiling enforced at copy time, in `internal/catalog/fetch.go`'s neighbourhood, so the refusal names the entry before a build is paid for | No bound | A repository with a large binary becomes an unbounded build context: Cloud Build workspace, context upload and image layers all grow, and the failure surfaces as a Cloud Build timeout rather than a validation message |
| **D1399** | Is `.dockerignore` honoured, required or ignored? | **Honoured, and documented**: Docker honours the repository's own file, and forbidding it would make the same Dockerfile behave differently in CI and in Fugaro | Require one, or generate one | A required file is a new failure mode for every repository; a generated one silently overrides what the repository's author wrote |
| **D1400** | Is the context's own identity recorded, and in which field? | **A new field beside `SourceCommit`** — the Dockerfile's commit and the context's hash, each named for what it is; F25 forbids retyping `SourceCommit` | Reuse `SourceCommit` for the context's commit | A reused name that means something else: the console, mobile and history draw a wrong figure with no error (the repository's own wire-format rule) |
| **D1401** | How does the nightly check learn that the context moved? | **A stored hash beside the Dockerfile in `builds/catalog/`**, written by the operator at copy time, compared the way `DefinitionSHA256` is compared today — it needs no credential in the check job and no new grant | Give the check job a git credential and a checkout | An account whose whole design is that it has none (F13) gains a credential to a repository, and a new read path into every repository's branch — the largest security change in this reconciliation |
| **D1402** | Does a context change fire `catalog-definition`, or a new trigger name? | **A new reason**, because `ReasonCatalogDefinition`'s own doc says it means "the definition a catalog image was built from is not the one in the runs bucket now" — the context is a second input, and a night when both moved must record both | Fold it into `catalog-definition` | Two different causes print the same reason, and an operator reading a night's decisions cannot tell a Dockerfile edit from a repository edit |
| **D1403** | Does `context` move the policy node's `Version`, and what does an older runner do with a node carrying it? | **A skew gate first**, the way `internal/infra/readiness.go`'s `imageCatalogShipped` gated `image_catalog` (F22): the release that writes a node carrying `context` flips it, and no node carries the key before that release ships | Write the key and rely on the strict decode to refuse it | An older runner refuses the whole node rather than the one key, which fails a project's runs rather than its catalog — the skew D1156 exists to avoid |
| **D1404** | Does a context entry's build still read the copied Dockerfile from the bucket? | **Yes**: the copy stays the record of what was built and the thing `catalogPinStep` verifies; the checkout supplies the context only | Read the Dockerfile from the checkout instead | The sha256 the record pins stops being the sha256 of the bytes the build used, and `catalogPinStep`'s verification loses its object |
| **D1405** | May `context` point at a repository the project has no credential for? | **No.** `internal/cli/image_build_catalog.go`'s `catalogBuildEntries` already refuses to build an entry whose Dockerfile is in another repository ("its build runs as that repository's build account"); the same rule covers the context | Allow it | A build with no credential for the context's repository, failing at build time instead of at validation, with the credential for the *entry's* repository in hand |
| **D1406** | Does `context: ci/` mean the context root **is** that directory, or the whole repository filtered to it? | **The root is that directory**, so `COPY maven/settings.xml …` resolves — the reading that makes the issue's own example work with the issue's own Dockerfile | The whole repository, filtered | Every `COPY` in the issue's example needs an `ci/` prefix, so the feature does not let the Dockerfile be used as written — which is the whole point of the issue |
| **D1407** | What do the smoke and gate steps see for a context entry, and does `NoSmoke` keep its meaning? | **Unchanged**: smoke runs the built image as it does today, and `CatalogBuildSpec`'s `NoSmoke` keeps meaning "this entry has no smoke to run" | A different smoke for context entries | Two smoke meanings, and an operator cannot tell from the record which one ran |
| **D1408** | Does this folder supersede `docs/project-image-catalog/context-design.md`, fold into it, or stand beside it? | **Fold into it.** It is the same feature, in the folder whose spec it amends, with its own gaps, decisions and tasks already written; a second plan for one feature is two answers to one question | Stand beside it, or supersede it | Two unbuilt designs for one feature, each with its own `D`/`Check`/task series, and the owner has to reconcile them later at higher cost than now |
| **D1409** | Which release carries it, and in what order against `docs/repo-image-removal`? | **After the removal lands and after D1208's migrated entries are real**, so the design can be read against the shape it is meant to replace rather than guessed at | Ship it beside the removal | The removal's whole premise (D1208: no engine-side bake keyed by repository) and this feature ship in one release, and the contradiction is settled by whoever reads the notes first |

---

## 8. The number block, and how it was read

Read on 2026-11-06 with a fresh `git fetch` of `origin/main` and of **every** open pull request's head
(`refs/pull/*/head` into `refs/remotes/pr/*`, 1783 refs), so "nothing uses this" is a statement about
the whole tree, not about `main` alone.

**Decisions: D1390–D1409.** Commands:

```
git grep -ohE '\bD1[0-9]{3}\b' origin/main -- docs | sort -u | tail -1   # D1389
git grep -lE '\bD1(39|40)[0-9]\b' $(git for-each-ref --format='%(refname)' refs/remotes/)  # (nothing)
```

The highest decision written anywhere is **D1389** (`docs/image-env`, on `main`). Nothing on `main`
and nothing in any of the 1783 open pull-request heads uses **D139x or D140x** — the second command
prints no path at all. Positive controls were run so that "nothing" is not a broken command:
`D1389` is found in `docs/image-env/README.md` and `docs/image-env/reconciliation.md`, and `D1350` is
found in three files of `docs/cli-output-console-graph` on `pr/1780`. Two blocks are known to overlap
each other above `main`'s highest (`pr/1780`'s `docs/cli-output-console-graph` takes D1350–D1369 and
says so in its own §Number block), and that collision is **not this work's to fix**: this block
starts above everything written or claimed, so it collides with neither.

**Checks: Check 292+.** Commands:

```
git grep -ohE '^## Check [0-9]+' origin/main -- docs | awk '{print $3}' | sort -n | tail -1   # 290
# the same, per ref, over every refs/remotes/pr/*                                        # max 290
git grep -l 'Check 291' origin/main -- docs   # docs/image-env's three files
```

The highest **written** `## Check` section anywhere is **290** (`docs/gcp-live-checklist.md`). **291**
is claimed by `docs/image-env` (`README.md`, `implementation-plan.md`, `reconciliation.md`), which
states the register's rule: a claim above the highest written section is settled at its merge, and
whichever of two claims merges second renumbers itself. **292–299 are claimed by nobody**, in `main`
or in any open pull request. `docs/stateless-orchestrator` claims **300–309** and
`pr/1780`'s `docs/cli-output-console-graph` claims **310–314**; both are above this block. This work
therefore takes **Check 292** (with 293–299 available if a task needs a second owner-run check), and
[implementation-plan.md](implementation-plan.md) §Check names it. The register's own row and section
are written by the task that implements the check, under the number free **at that merge** — which is
the register's rule and not this folder's choice.

---

## 9. Open items

These are the owner's, and none is decided here. They are repeated in [README.md](README.md) §Open
items, which is where the owner's answers land.

1. **D1408, first and blocking.** `docs/project-image-catalog/context-design.md` already designs this
   feature (F29). Fold into it, supersede it, or stand beside it — no task in
   [implementation-plan.md](implementation-plan.md) can start before this is answered, because the
   answer decides which folder's tasks are real.
2. **D1409.** `docs/repo-image-removal`'s D1208 (owner ruling 2026-11-04) refused an engine-side bake
   keyed by repository, and this feature re-creates part of that input inside the catalog (F30). The
   owner decides whether the difference — a checkout the Dockerfile decides what to do with, versus
   cache state the engine maintains — is enough, and whether the two ship in one release.
3. **D1401, the security fork.** A context-change trigger needs either a credential in the check job
   or a hash staged in `builds/catalog/` (§4.6). The second is recommended and is the one that keeps
   the `objectViewer`-only grant intact (F24).
4. **D1397.** The build hands the git credential to every Dockerfile it runs (F1, F31). Whether a
   context entry keeps that is a decision with a security argument on both sides.
5. **D1394/D1395.** "The exact commit the Dockerfile was read at" does not exist today (F2, F16); the
   owner decides what replaces it.
6. **D1398.** The context has no bound (§4.4, F19).
