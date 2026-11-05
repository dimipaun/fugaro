# A repository build context for catalog `dockerfile` entries

**Status: design only. This is an amendment to [spec.md](spec.md), it is not approved, and no task in
it is approved to build.** Nothing here is code: no Go, no Terraform, no schema, no rules, no
`.github/workflows` change and no live action. It is written against the tree on `origin/main` at the
commit this branch merged, and every claim below was read there rather than assumed from the brief.

It answers one gap the folder never reconciled: **a catalog entry whose source is a Dockerfile cannot
name a build context, so a repository's ordinary CI Dockerfile — a repository-root context with
`COPY` — cannot be built by the catalog at all.** That is the one thing that keeps the per-repository
`image:`/`dockerfile:` keys alive, and it is why this amendment sits where
[docs/repo-image-removal/](../repo-image-removal/README.md) removes them.

**Where this file disagrees with [spec.md](spec.md), this file wins** — the rule
[reconciliation.md](reconciliation.md) already carries, extended to the amendment. The owner's spec is
**not edited**: the amended sentence is quoted here verbatim and marked
**amended after review**, so the original stays visible beside its amendment. Where this file disagrees
with [reconciliation.md](reconciliation.md), the reconciliation wins and this file is re-read.

**Answering anything here is not approving anything.** The folder's rule stands: the build tasks are
planned, and **nothing is built until the owner approves the plan** ([spec.md](spec.md) §Owner rulings).

## Numbers this amendment holds

The folder's block is taken and stays taken: **D1140–D1159 are
[docs/project-image-catalog/](README.md)'s**, D1159 is its reserved tail, and **this amendment takes no
`D` number at all** — a second block inside one folder's own design would make every "the highest
written" reading in [docs/config-management/](../config-management/README.md) and
[docs/repo-image-removal/](../repo-image-removal/reconciliation.md) §3 re-read for one amendment. So
every number below sits in another series, exactly as `docs/bitbucket-credentials` puts the owner's
items in **O1–O7**:

| Block | Use |
| --- | --- |
| **G1–G4** | the gaps this amendment closes, read against the code. They are **not findings**: the folder's findings are **F1–F16** ([reconciliation.md](reconciliation.md) §1) and stay exactly those sixteen. A gap here is a place where the code and a sentence of the folder's own documents disagree; a finding is an ambiguity in the owner's spec. |
| **A1–A8** | this amendment's own decisions, stated here in §2 with the recommendation and the cost of the alternative, the shape [reconciliation.md](reconciliation.md) §2 already uses for D1140–D1159 |
| **O1–O4** | the **open items** — the owner's to answer, §9. **None of them is decided here.** |
| **C1–C4** | the build tasks, §5. **Nothing is built and no task is approved.** |

## 1. The gaps (G1–G4)

- **G1. The owner's spec asks for a context and the code has none.**
  [spec.md](spec.md) §Where Dockerfiles live, last bullet: *"Build context: a catalog entry can set it
  explicitly. By default, a repo-referenced Dockerfile uses its own repo as context, shallow-cloned at
  build time, so COPY works. An inline definition gets an empty context plus any baked repos. Builds
  run only nightly or on demand, so the clone cost doesn't matter."*
  [reconciliation.md](reconciliation.md) never ruled on it: **F3** names the absence and stops at the
  grammar (*"The spec's `repo-name:path` form … does not exist, and neither does a branch or a build
  context"*), and no D-row of D1140–D1159 mentions a context. So the one sentence of the spec that
  decides whether a repository's own Dockerfile can be built at all is the one sentence the folder left
  standing.

- **G2. The catalog build's context is the Dockerfile and nothing else.**
  `CatalogDockerfileStep` (`internal/backend/gcp/build.go`) writes the copy to
  `/workspace/context/Dockerfile` and *"a catalog build reads one object instead of a checkout"*;
  `catalogBuildSteps` (`internal/backend/gcp/catalog.go`) lists `prep, credential, dockerfile,
  catalog-pin, build, smoke, promote, catalog-tag, catalog-record, untag` and its own comment says it:
  *"a catalog build never clones a repository into the build context"*. The build step then runs
  `docker build … --tag "$$candidate" /workspace/context`, so `COPY` in a catalog Dockerfile can reach
  exactly one file — the Dockerfile itself. `TestCatalogBuildReadsTheCopiedDockerfile`
  (`internal/backend/gcp/catalog_test.go`) pins that, over `b.Steps`: some step mentioning `clone` is a
  failure of the test as it stands.

- **G3. "One commit" is not guaranteed today, and the commit is already load-bearing.** The copy reads
  the Dockerfile **at the entry's branch** — `r.Contents(ctx, s.Path, s.Branch)`
  (`internal/catalog/fetch.go`, the `ContentsReader` interface in `internal/gitprov/gitprov.go`),
  which resolves a ref and never answers a commit — while the build is submitted with
  `SourceCommit: commit` from `gitRead(ctx, root, "rev-parse", "HEAD")`
  (`internal/cli/image_build_catalog.go`), the **operator's checkout HEAD**. That value is not merely a
  record and a label: `catalogPinStep` writes it to `/workspace/out/source-commit`, and the build step —
  the one `images/derived/cloudbuild.yaml` states, reused whole by `CatalogBuildRequest` — passes it as
  `--build-arg "FUGARO_COMMIT=$$commit"` and as
  `--label "org.opencontainers.image.revision=$$commit"`. **The Dockerfile in that same build reads
  `FUGARO_COMMIT` and clones at it** (that is exactly D1208's shape, G4), so today a migrated entry's
  Dockerfile can come from one commit and its clone be pinned to another, with the record and the
  revision label naming the second. For an entry that never reads `FUGARO_COMMIT` this costs nothing;
  the moment a build checks a context out **at that commit**, the two values must be one.

- **G4. The removal works around G2 by hand.**
  [docs/repo-image-removal/](../repo-image-removal/spec.md) §3.2, D1208: the migrated entry's Dockerfile
  *"clones `$REPO_URL` at `$FUGARO_COMMIT`, warms the cache, and ends with `finalize-checkout`"*, and
  R0's red step reads the dogfood Dockerfile and *"fails if it does not end with `finalize-checkout`"*
  ([implementation-plan.md](../repo-image-removal/implementation-plan.md) §R0). That is correct, and it
  is the only shape available today. It also means **every** migrated repository hand-writes a clone,
  a credential mount reference and a checkout finaliser inside its own Dockerfile — the per-repository
  build input the removal is trying to delete, re-typed by hand into every entry.

## 2. The amendment (A1–A8)

### A1. `context:` on a `source: { kind: dockerfile }` entry

An entry's dockerfile source gains one optional key: the **build context**, a repository-relative path
in the entry's own repository, at the entry's own `branch`, defaulting to the **repository root** when
it is written as `.` or as `""`. It is refused on an `inline` source and on an `image` source by the
rule that already exists — `addCatalogWrongKind` (`internal/config/catalog.go`) refuses every field
that belongs to another kind, and `context` joins `repo`, `path` and `branch` as one of the
dockerfile source's own.

It needs **its own path rule**, and the difference is the whole point: `catalogPathMessage` refuses
`"."` — *"must name a Dockerfile, not the repository root"* — which is exactly the value a context
wants. So `context` gets `catalogContextMessage`, the same rules as the Dockerfile's path (relative,
no `..` segment, clean, no control character, `maxCatalogPathLen` = 200) **minus** the
not-the-repository-root rule, because a repository-root context is the common case this amendment
exists for.

An entry with **no** `context` keeps today's meaning exactly: the copy's directory holds only the
Dockerfile, and the request sent to Cloud Build is the one `CatalogBuildRequest` builds today, field
for field. That is the issue's own requirement and it is what makes this amendment additive: an
installation that writes no `context` sees no change in what is submitted, what is built and what is
recorded.

- **Cost of the alternative.** Making the context **implicit** — "a repo-referenced Dockerfile always
  uses its repository as its context", which is what [spec.md](spec.md) §Where Dockerfiles live asks
  for as the *default* — changes every existing entry's build in one release: every catalog Dockerfile
  that today sees one file would suddenly see a whole tree, and one that does `COPY Dockerfile /x`
  would copy a different thing or fail. It also makes the nightly rebuild story worse, not better
  (A5): an implicit context is a whole repository that the check must watch for **every** entry. The
  opt-in key is the smaller change and it is the one that keeps a definition's meaning.

### A2. The Dockerfile and its context come from **one commit**

The copy resolves the source's ref to a commit **before** it reads the Dockerfile, reads the file at
that commit (both `gitprov` adapters already accept a commit where they accept a branch:
`Contents(ctx, path, ref)` is the `ref` query parameter on GitHub and a commit-or-branch ref on
Bitbucket, whose `validContentsRef` says so), and answers with it. That commit is what the submission
records as `SourceCommit`, what `catalogPinStep` writes to `/workspace/out/source-commit` and the build
step therefore passes as `--build-arg FUGARO_COMMIT` **and** as
`--label org.opencontainers.image.revision` (the two the Dockerfile's own clone and the record both
read, G3), and what the context is checked out at. **One commit, named in four places that agree by
construction.**

What it costs: one extra provider read per dockerfile source per `fugaro image build` — a ref → commit
resolve, which GitHub does not have today (`internal/gitprov/github` has no commits read) and Bitbucket
does, unexported (`resolveFullHash`, `internal/gitprov/bitbucket/bitbucket.go`). The value's *meaning*
does not change — the field is already documented as *"the commit the definition was taken from"*
(`gcp.CatalogDefinition.SourceCommit`) and the build already says *"it cannot resolve a ref itself"* —
so this is a writer made to honour its own documentation, **not a wire-field change**: `source_commit`
keeps its name and its meaning, and the console, the history reader and the plugin keep reading it as a
commit.

### A3. The context reaches the build by a clone **outside** the Docker build

A new step runs before the copy is written, between `credential` and `dockerfile`:

- it clones the entry's repository **at the recorded commit** into `/workspace/context` with the
  credential on the same private volume the derived path already uses — the file `/creds/git-credentials`
  (mode 0600, `internal/cli/gitcred.go`'s `writeCredential`) inside the `/creds` directory the derived
  path's `prep` step creates 0700 and owns as the image's user, written by `fugaro image git-credential`
  — and passes the helper with
  `git -c credential.helper='store --file=/creds/git-credentials'` (the exact shape
  `images/derived/cloudbuild.yaml`'s `source` step uses);
- the Dockerfile step then writes the pinned copy into the tree **at its own repository-relative
  path**, so the Dockerfile a build reads is the one inside the context it builds, at the commit both
  came from;
- the build step gains `--file <path>` and nothing else. Its context stays `/workspace/context`, its
  secrets stay BuildKit **file** secrets, its labels and digests are unchanged.

The derived path already proves the machinery: `credentialEnv`, `credentialSecretEnv`, the
`fugaro-creds` volume, `CheckRepoURL` (which refuses any URL whose host or port would send the
credential somewhere it is scoped to) and the `git-credentials` file secret are all in the tree, and
the design reuses them rather than adding a second credential path. The step's directory mode is the
`source` step's, not the `dockerfile` step's: `CatalogDockerfileStep`'s own comment already says why —
*"A catalog step that does write there as the image's user needs the source step's mode and not this
one"*.

- **Cost of the alternative.** Cloning **inside** the Docker build — which is what D1208's Dockerfile
  does today, with `--secret id=git-credentials` — needs no new step at all, and it is why today's
  shape works. But then the *entry's Dockerfile* must contain the clone, which is G4: every repository
  re-types the same machinery, and a Dockerfile that is a repository's ordinary CI Dockerfile (the
  thing this amendment is for) cannot be used as it stands.

### A4. What the context holds, `.dockerignore`, and the checkout contract

The context tree is the repository at the committed pin, cloned with the credential **helper** and not
with a credential in the URL. That single detail is what keeps the credential out of the image (A6) and
what makes the smoke pass for free: the clone writes `remote.origin.url` as the plain `https://` URL
and `HEAD` at the pinned commit, which is exactly what the runtime contract's checkout check asserts —
`checkCheckout` (`internal/image/selftest.go`) requires `/work/repo` to be a git checkout **at
`spec.Commit`**, with `origin` equal to the repository URL and **no** credential helper, header or
credential-bearing rewrite in **any** git config scope. A Dockerfile that does `COPY . /work/repo`
therefore satisfies it as it stands, and **does not call `finalize-checkout`** — there is nothing to
strip, because nothing credential-shaped was ever written into the context's config. (D1208's
`finalize-checkout` rule therefore narrows to entries **without** a `context`: it exists to clean a
checkout the Dockerfile cloned *inside* the build with a BuildKit secret.)

`.dockerignore` is read by Docker from the context root, as it is for any `docker build`, and this
design reads it the way a person would expect, with two consequences a reader must know:

- a repository whose `.dockerignore` excludes a path its Dockerfile `COPY`s gets that file missing or
  gets a build failure — the same failure it gets locally, and the reason the design does not try to
  merge or override the file;
- a repository whose `.dockerignore` excludes `.git` **cannot** satisfy the checkout contract by
  copying the context, because `checkCheckout` needs a git dir; such an entry must create the checkout
  itself (today's D1208 shape) or narrow its `.dockerignore`. The design does **not** force `.git` into
  the context against a repository's own `.dockerignore`: a build input that overrides the repository's
  own exclusion file is a second, hidden definition of what the context is.

- **Cost of the alternative.** Stripping `.git` from the context before the build (a smaller context, a
  smaller image) makes `COPY . /work/repo` unable to satisfy the smoke, so **every** context entry
  would have to run `git init`-and-fetch or a `finalize-checkout`-shaped dance of its own — G4 again.

### A5. The record, and the rebuild triggers

What was built is recorded **once**, in the record the daily check already reads
(`CatalogRecordKey`, `imagecheck.CatalogRecordState`), and it gains one field:
**`context_sha256`**, the identity of the context the image was built from. The Dockerfile's own
identity stays where it is (`definition_sha256`, the copy's bytes; `definition_generation`, the
object's generation; `source_commit`, the commit from A2), and the revision label stays
`org.opencontainers.image.revision=$commit`. Nothing is re-meant and nothing is renamed; the field is
**added**, which the field rule allows, and a reader that does not know it ignores it
(`ParseCatalogRecordState` decodes into a struct and ignores unknown members) — while a **new** check
reading an **old** record sees `context_sha256 == ""`, which it treats as *unknown*, not as *equal*:
an entry built before this amendment rebuilds once and records the field. `CatalogRecordVersion` stays
1 for the same reason: an old reader must keep reading a new record, and it does.

The trigger table ([reconciliation.md](reconciliation.md) §4) keeps its four rows, and
**`catalog-definition` means one thing more**: the entry's definition changed when *either* the
Dockerfile copy's sha256 **or** the context's identity differs from the record's. The context's
identity is:

- **the narrower path list, when the entry names one** (`context` carrying a `paths` list, the shape
  `image.key_files` already has): a digest over the paths' identities at the entry's branch head. A
  path that is gone fires the trigger; a path whose content changed fires it; a commit that touches
  nothing on the list does not.
- **the branch head's commit, when it does not**: a change under the context on the branch means the
  branch moved, and the check rebuilds. Wider than "a file under the context changed" — a commit
  anywhere on the branch fires it — and honest about it: it is one bounded ref read per entry per
  night, it needs no tree walk, and `max-age` already rebuilds an idle entry on its own schedule.

**The check never clones.** Today it reads bucket objects and no repository at all: the copied
definition, the entry's record and the check's own state object, all through `t.bucket`
(`internal/cli/image_check_catalog.go`), and it compares the copy's `sha256.Sum256` with the record's
through `DecideCatalog`'s `in.DefinitionSHA256 != rec.DefinitionSHA256`. Under this amendment it reads
the same objects **plus one bounded provider read per entry with a context** — a ref resolve, or the
named paths' identities — and never a checkout, never a blob tree, never a clone. **What that read
costs is the owner's, and it is O2**: the project-level catalog check job would hold a repository
credential where today it holds none, which is a grant and a Terraform change, and the alternative
(the context rides `max-age` alone, with no repository read at all) is priced there.

### A6. Bounds, and what a huge context costs

- **The definition's bound does not change**: the copy is bounded by `catalog.MaxDockerfileBytes` (1
  MiB, `gitprov.MaxContentsBytes`) wherever it is resolved.
- **The context has no bound in the code today and this amendment does not add one**: the clone's size
  is the repository's size at that commit, and the only ceilings are Cloud Build's own — the build's
  `Timeout` is `"3600s"` and its `options.MachineType` is the installation's
  (`gcp.CatalogBuildRequest`), the worker's disk and the daemon's context upload are Cloud Build's.
  Whether to add a bound — and where: a file count and a byte total, read by the CLI where it resolves
  the context and refused **before** the copy is written, so a declined build leaves no trace — is
  **O3**.
- **What a huge context costs** is four things, and none of them is silent: build minutes (a context
  upload and a clone the entry did not pay before), the worker's disk, the image's own size for
  whatever the Dockerfile `COPY`s, and — the one that is not obvious — the **rebuild trigger's**
  fidelity, since a whole-repository context with no `paths` list rebuilds on any commit (A5).

### A7. The node, the schema and the skew

`context` is a new member of `image_catalog.entries[].source` in the **published policy node**
(D1140), so it reaches four places and each has a rule:

| Surface | What it gets | What reads it |
| --- | --- | --- |
| `internal/config.Source` | the field, its yaml and json spellings, `catalogContextMessage`, and `addCatalogWrongKind` | the local configuration's strict loader, `ValidateCatalog` |
| the published node | the same value, through `projectpolicy.ImageCatalog = config.Catalog` | every launcher and every run |
| `internal/projectpolicy`'s **closed member list** (`catalogSourceMembers`) | one more name — **this is the skew** | the node's decoder, in the CLI and in the runner |
| `internal/settings/registry.go` and `schemas/settings.schema.json` | one row each. The source's leaves are **six** today — `kind`, `repo`, `path`, `branch`, `inline`, `image`, the list `catalogSourceMembers` holds — and `context` makes them **seven**; the whole entry's leaf set is **ten**, and the registry's rows go **325 → 326** | `fugaro settings`, and the two documents that state the figure ([docs/config-management/](../config-management/README.md) F1, and the pinned `cfmRegistryRows`) re-pointed in the same commit |

**The skew is the shape D1156 already states, one level down and harder.** D1156's honest note is that
`image_catalog` itself is a new *top-level* key: a reader that does not know it lists it in
`UnknownKeys` — tolerated by a runner (`internal/runner/projectpolicy.go` logs it) and refused by the
CLI (`refuseUnknownPolicyKeys`) — so a project that names a catalog has every launcher upgraded before
it can launch again, and a project that names none has a node byte for byte what it was. A `context`
member is different in one way that must be said plainly: `decodeImageCatalog` holds every member name
to the documented spelling through **`checkMemberNames` before the decoder runs**, so a reader that
knows `image_catalog` but predates `context` does not tolerate the member — it **fails the whole block
decode**, in the CLI *and* in the runner. The population is narrower (only readers that already know
`image_catalog`) and the failure is louder (a decode error, not a listed unknown key), and the remedy
is the same one: the node is written by `fugaro update` and read by a CLI at least as new. The
publisher's keep rule is untouched: an installation that names no catalog writes no key, and an
installation whose entries name no `context` writes a node whose bytes are what they are today.

**Whether the policy `Version` moves is the owner's, and it is O1**: `Version = 4` today
(`internal/projectpolicy/version.go`), and D1156's precedent is that an added key rides a version
without moving it.

### A8. What this amendment does not change

- **An entry without `context`.** Byte for byte today's build: the same steps, the same request, the
  same record. Nothing in an existing catalog is rebuilt because this design merged.
- **The inline source's empty context.** [spec.md](spec.md) §Where Dockerfiles live already says it
  (*"An inline definition gets an empty context plus any baked repos"*), and this amendment adds
  nothing to it: an inline definition is written from project state and its context stays what the
  build makes it.
- **Baked repositories.** [spec.md](spec.md) §Baked repos and D1153 are untouched: a context is a
  **build** input, a baked repository is a **run-time** one, and an entry may have both. `lockfiles`
  keeps firing on a baked repository's dependency files exactly as it does.
- **The build account, the bucket grants and the record key.** The build still runs as the entry's
  repository's build account (`BuildServiceAccountID`), still reads `builds/catalog/` under
  `CatalogReadCondition` (read-only, and it stays read-only: a repository's build must not be able to
  rewrite a definition every other repository's jobs run) and still writes its record under its own
  `builds/<slug>/`. No new secret, no new prefix, no new role on the build side.
- **The selection.** The tag-cover rule, `image_name` as a pin and `needs` as a requirement are
  untouched: a context is a property of how an entry is *built*, never of how one is *chosen*.
- **The removal.** [docs/repo-image-removal/](../repo-image-removal/implementation-plan.md) R0–R10 and
  the order its §2 (**Order, and what forces it**) states are not amended. This design widens what a migrated entry **may** be; it does not
  move a task, and the removal's order (**entry built and pinned before anything is removed**) is what
  makes either shape safe.

## 3. What of the spec this amends, verbatim, with the original kept visible

### 3.1 [spec.md](spec.md) §Where Dockerfiles live, last bullet — **amended after review**

> Build context: a catalog entry can set it explicitly. By default, a repo-referenced Dockerfile uses
> its own repo as context, shallow-cloned at build time, so COPY works. An inline definition gets an
> empty context plus any baked repos. Builds run only nightly or on demand, so the clone cost doesn't
> matter.

**AMENDED AFTER REVIEW (this design):** three clauses of this sentence are amended and one is kept.

- *"a catalog entry can set it explicitly"* — **kept, and it is what this design builds**: the key is
  `context` (A1), written by the entry's author, refused on the kinds that cannot have one.
- *"By default, a repo-referenced Dockerfile uses its own repo as context, shallow-cloned at build
  time"* — **amended**: there is **no such default**. An entry that writes no `context` is built
  exactly as it is today, from the copy alone (A1, A8). The default the sentence describes would
  re-mean every entry that exists, and the alternative's cost is stated in A1.
- *"so COPY works"* — **kept as the reason the key exists**, and strengthened: the context is checked
  out at **one commit**, the same one the Dockerfile came from (A2), so `COPY` reaches a tree and not a
  moving branch.
- *"Builds run only nightly or on demand, so the clone cost doesn't matter"* — **amended**: the clone
  cost does not matter *much* — it is one clone per build, and the build is a Cloud Build run that
  already pulls a base image and pushes a candidate — but it is not free and it is not unbounded: the
  clone and the daemon's context upload are paid in the build's own `3600s` timeout and on Cloud
  Build's worker (A6), and the **check** pays nothing, because it reads a ref or a path list and never
  a checkout (A5).

### 3.2 The folder's own decisions and findings this amendment touches

Each is **amended after review**; the original text is left where it is in its own file, and a reader
who wants it should read it there.

- **F3** ([reconciliation.md](reconciliation.md) §1) — *"neither does a branch or a build context"*.
  The sentence is a fact about the tree **at the folder's reconciled commit** and stays true of that
  commit; this amendment closes the second half of it. F3's verdict (**Conflicts**) is unchanged: the
  catalog's grammar is still new relative to `dockerfile:`.
- **D1140** — the catalog travels in the published policy node and the node holds the **pointer**, not
  the bytes. Unchanged in shape; the pointer now has one more field (A7).
- **D1153** — baked repos and the run-time fetch forward. Unchanged, and §A8 says why the two do not
  collide.
- **D1156** — the skew note. **Extended**, not replaced: A7 states the same rule for a member rather
  than a top-level key, and says honestly where the member's failure is harder.
- **[reconciliation.md](reconciliation.md) §4's trigger table** — the `catalog-definition` row gains
  the context's identity (A5). Its four rows stay four rows.
- **PIC3 and PIC4** ([implementation-plan.md](implementation-plan.md)) — PIC4's *"builds it with the
  entry's context"* is the phrase this amendment makes literal: that context is, and stays, the copy's
  own directory unless the entry names a `context`. PIC3's copy is unchanged except for the commit it
  reads at (A2). The sentence the amendment really amends is not in the plan but in the code —
  `catalogBuildSteps`'s comment, *"a catalog build never clones a repository into the build context"*
  (`internal/backend/gcp/catalog.go`), which becomes *"never clones for an entry that names no
  context"*. Both sentences are left visible where they are; the tasks that build this amendment are
  §5's, not PIC1–PIC7 re-opened.

### 3.3 What it does not amend, and one pointer

- **[spec.md](spec.md), [reconciliation.md](reconciliation.md) and
  [implementation-plan.md](implementation-plan.md) are not edited by this amendment.** The owner's spec
  is verbatim and stays verbatim; the reconciliation and the plan are the folder's own documents and
  their counts (F1–F16, D1140–D1159, PIC1–PIC7) stay exactly what they say.
- **A pointer, not an amendment, to
  [docs/repo-image-removal/](../repo-image-removal/reconciliation.md) D1208**: that decision (**"the
  entry's own Dockerfile does the clone, the warm-up and a final `finalize-checkout`, and the engine
  gains NO bake mode"**) was answered by the owner on 2026-11-04 against a tree with no context key,
  and its *reason* — *"an engine-side bake keyed by repository re-creates the per-repository build
  input inside the catalog, which is the thing the ruling removes"* — is a reason against a bake, not
  against a context. This amendment adds no bake mode and no engine-rendered Dockerfile: the entry's
  Dockerfile is still the repository's own file, written by a person. An entry **with** a `context` is
  a second shape beside D1208's, and §A4 says which one has to call `finalize-checkout`.

## 4. The security analysis, as an attacker would see it

### 4.1 Who can change the context's contents between the pin and the build

Three moments, and the answer at each:

1. **Between the copy and the submission** (`fugaro image build` resolves, copies, then submits). The
   copy is written read-compare-write and the submission states the object's **generation** and the
   **sha256 of the bytes it read back** (`writeCatalogDockerfile`, then `rb.Read` +
   `sha256.Sum256` in `internal/cli/image_build_catalog.go`); the build's pin step verifies both
   against what it actually read and fails when they differ. Under A2 the submission also states the
   **commit** the copy and the context were resolved at, so a push to the branch between the two
   moments changes neither.
2. **Between the submission and the build.** The context is fetched **by commit**, not by branch
   (`git fetch` of the recorded SHA), so a push changes nothing; a force-push that rewrites the commit
   and garbage-collects it makes the **fetch fail** — the build fails, it does not build a different
   tree. That is the property to want, and it is the one `imagecheck.GitTree.IsAncestor` gives the
   workflow check's `built-commit-gone` today.
3. **Between the build and the record.** The record step reads the pushed digest, the base digest, the
   commit and the definition's identity from files the pin step wrote and refuses anything that is not
   a digest; the record's `context_sha256` is the pin step's value, like the rest.

Nothing here is new machinery: it is the shape the catalog path already has, with the commit brought
into agreement with it (G3, A2).

### 4.2 One commit means one tree

This is the property that makes the amendment worth its cost, and the reason A2 is not optional. The
Dockerfile's bytes, the context tree, the `_FUGARO_COMMIT` build argument, the record's
`source_commit` and the image's revision label are all the **same commit**:

- the Dockerfile cannot come from a newer commit than the context, so a Dockerfile that expects a file
  its sibling commit added cannot silently build against an older tree;
- the smoke's own checkout check (`checkCheckout` compares `/work/repo`'s `HeadSHA` with
  `spec.Commit`, which is the same commit) cannot pass on a tree that is not that commit, so a context
  entry that copies the context into `/work/repo` **fails its build** rather than shipping a
  mislabelled image;
- a reader who takes the revision label and asks "what was in this image?" gets an answer that is true
  of the whole image and not only of its Dockerfile.

### 4.3 A compromised repository already controls its Dockerfile

A repository's write access lets it put anything it likes into the image: the Dockerfile is executed by
`docker build`, `RUN` is arbitrary code, and `image.setup` — for an entry migrated from a per-repository
image — has always been a command the repository writes. **A context adds no new principal and no new
capability to that.** The same repository supplies both inputs, from the same commit, read with the
same credential:

- the repository cannot reach **another** repository's context: `context` is a path inside the entry's
  **own** repository (`Source.Repo`), and the clone URL, its host and its port are checked by
  `CheckRepoURL` before the credential is used, exactly as they are for the definition fetch;
- the repository cannot reach the **credentials service** or another repository's secret: the clone
  uses the same per-repository provider credential the copy already uses, and no build secret is added
  by a context (`_SECRET_ENVS`'s path is unchanged, D1150);
- the repository **can** make the image larger, slower to build, and fail its own build. That is
  already true of its Dockerfile.

### 4.4 The credential stays out of the image

- The credential's value lives in the file `/creds/git-credentials` on the `fugaro-creds` **volume** —
  mode **0600**, inside the `/creds` directory the `prep` step makes 0700 and owns as the image's user
  — written by the `credential` step and removed by the build's own
  `trap` (`rm -f /creds/git-credentials`) — the same file, the same volume and the same teardown the
  derived path uses. The `images/cloudbuild_test.go` rules that keep it there (no `/workspace/…creds`
  path anywhere, every `git-credentials` mention either `/creds/git-credentials` or
  `id=git-credentials`) apply to the new step unchanged.
- The helper reaches git as a **command-line** `-c credential.helper=…`, which git reads and does not
  persist, so the context's `.git/config` holds the repository's plain `https://` **URL** and nothing
  else. The credential is never in a URL, so it is in no `remote.origin.url`, no `http.extraheader`,
  no `insteadOf`.
- A context that is copied into `/work/repo` therefore satisfies `checkCheckout`'s credential rule
  (no helper, header or credential-bearing rewrite in **any** scope) with nothing to strip — and
  `finalize-checkout` is not called, because it would fail on a tree it has nothing to clean (§A4).
- The Dockerfile's **own** secrets (`_SECRET_ENVS`, BuildKit file secrets) are untouched: a context
  changes where the Dockerfile reads its files, never how a build secret reaches it.

### 4.5 What the amendment does not widen

No new secret is read, no secret's scope changes, no bucket prefix is added, the copy stays read-only to
the build account, the registry and its retention are untouched, and an entry that names no `context`
submits the request it submits today. The one **new** read this design proposes is on the **check** side
and it is not decided here (A5, **O2**): if the owner takes the precise trigger, the project-level
catalog check job gains a repository credential read per entry with a context, which is a grant a
reviewer must read beside `internal/infra/tf/cover.go` the way every new principal's grant is read —
and if the owner takes the alternative, no credential is read at all and the context's freshness rests
on `max-age`.

## 5. The build tasks (C1–C4)

**Nothing here is approved to build.** The task shape is
[implementation-plan.md](implementation-plan.md)'s and [docs/bitbucket-credentials/](../bitbucket-credentials/implementation-plan.md)'s:
Files, Interface, Tests, Command, Depends on, Red step, Reviewer, Owner-run, Commit. The tests named
are the ones that must pass; the red step is the one that must fail on `main` as it stands. A task
marked **SEC** gets the owner's independent review in addition to the usual one.

This amendment's own **pin** is not a task: the pins for this file and the README's extension go into
the existing `internal/cli/docs_project_image_catalog_test.go` **in this design's own pull request**
(no new `docs_*_test.go`, so the census of folders that ship one is unchanged), the way the folder's
own pins did.

### C1. `context` in the source grammar, and its row

- **Interface.** `Source.Context` (yaml and json `context,omitempty`), `catalogContextMessage` — the
  Dockerfile path's own rules minus the not-the-repository-root rule — `addCatalogWrongKind` extended
  with the field, and one settings row. A golden fixture with a context in the local configuration.
- **Files (7).** `internal/config/catalog.go`, `internal/config/catalog_test.go`,
  `internal/settings/registry.go` (one row: 325 → **326**), `schemas/settings.schema.json` (one
  member), `internal/cli/docs_config_management_test.go` (`cfmRegistryRows`),
  `docs/config-management/README.md` and `docs/config-management/reconciliation.md` (the two sentences
  that state 325, re-pointed in the same commit — the folder's own rule is that the documents state the
  figure they are the ones arguing from).
- **Tests.** `TestCatalogContextIsRefusedOnAnInlineSource`,
  `TestCatalogContextIsRefusedOnAnImageSource`, `TestCatalogContextAcceptsTheRepositoryRoot`,
  `TestCatalogContextRejectsAPathOutsideTheRepository`,
  `TestCatalogEntryWithoutAContextValidatesExactlyAsToday`, and the config-management pin re-run.
- **Command.** `GOFLAGS=-p=2 go test ./internal/config/ -run 'Catalog' -count=1` and
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'DocsConfigManagement' -count=1`.
- **Red step.** `TestCatalogContextAcceptsTheRepositoryRoot` fails: there is no `Context` field, and
  `catalogPathMessage` refuses `"."` for the only path the source has.
- **Depends on.** None.
- **Reviewer:** standard. **Owner-run:** no.
- **Commit:** `feat(config): a repository build context for a catalog dockerfile source`.

### C2. One commit: the copy resolves the ref and reads at it

- **Interface.** A ref → commit read on the provider (GitHub new; Bitbucket's `resolveFullHash`
  reused), `catalog.Fetch` reading the file **at the commit** and answering with it,
  `writeCatalogDockerfile` returning it, and `fugaro image build` recording **that** commit rather than
  the operator's checkout HEAD. **SEC** — a credential path, and the record's meaning is what is being
  defended.
- **Files (10).** `internal/gitprov/gitprov.go` (the interface), `internal/gitprov/github/` (the commits
  read), `internal/gitprov/bitbucket/` (the export), `internal/gitprov/fake/fake.go`,
  `internal/catalog/fetch.go`, `internal/catalog/fetch_test.go`, `internal/cli/catalog_copy.go`,
  `internal/cli/image_build_catalog.go`, and the two tests
  (`internal/cli/catalog_copy_test.go`, `internal/cli/image_build_catalog_test.go`).
- **Tests.** `TestCatalogCopyResolvesTheRefToOneCommit`,
  `TestCatalogCopyReadsTheDockerfileAtTheResolvedCommit`,
  `TestCatalogCopyRefusesARefThatDoesNotResolve`,
  `TestCatalogBuildRecordsTheCommitsTheCopyRead`, `TestCatalogCopyUsesTheExistingCredentialAndLogsNone`
  (the existing test, extended to the resolver), `TestCatalogCopyIsBounded`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/catalog/ ./internal/cli/ -run 'CatalogCopy|CatalogFetch' -count=1`.
- **Red step.** `TestCatalogCopyResolvesTheRefToOneCommit` fails: `ContentsReader.Contents` reads a ref
  and no source answers a commit, so the copy has nothing to resolve to.
- **Depends on.** None (it is independent of C1: an entry with no context gains the same resolved
  commit).
- **Reviewer:** opus **plus the independent review a SEC task gets** (a credential path, and the value
  the revision label and the record carry).
- **Owner-run:** no. **Commit:** `fix(catalog): the Dockerfile copy and its record name one commit`.

### C3. The clone step, the `--file` build, and the record's field

- **Interface.** A `catalog-context` step, inserted only when the entry names a context, between
  `credential` and `dockerfile`; the Dockerfile copy written into the tree at its own
  repository-relative path; `--file` added to the build step only for such an entry; the record's new
  `context_sha256`; `_FUGARO_COMMIT` reaching the build as the resolved commit. **SEC** — it runs a
  clone with the repository's credential.
- **Files (8).** `internal/backend/gcp/catalog.go`, `internal/backend/gcp/build.go` (the copy's
  destination path), `internal/backend/gcp/catalog_test.go` (the *no step clones* assertion becomes
  *an entry without a context has no clone step, and one with a context has exactly that step*),
  `internal/backend/gcp/catalog_record_test.go`, `internal/image/render.go` (read: the file-secret
  mounts the build step keeps), `images/derived/cloudbuild.yaml` (read: the `credential` step and the
  `fugaro-creds` volume the new step uses), `internal/backend/gcp/build_test.go`, and
  `internal/cli/image_build_catalog.go` (read: the confirmation and the copy order).
- **Tests.** `TestCatalogContextStepClonesAtThePinnedCommit`,
  `TestCatalogContextStepUsesTheCredentialFileAndNeverAURLArgument`,
  `TestCatalogContextKeepsTheCredentialOutOfTheImage`
  (no `/workspace/…creds` path, the teardown, the helper passed with `-c`),
  `TestCatalogBuildWithoutAContextIsByteForByteTodaysRequest`,
  `TestCatalogContextBuildPassesFileAndTheContextRoot`,
  `TestCatalogBuildRecordsTheContextsIdentity`, `TestCatalogContextStepRefusesAURLThatIsNotTheProviders`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/backend/gcp/ -run 'Catalog' -count=1` and
  `GOFLAGS=-p=2 go test ./images/ -count=1`.
- **Red step.** `TestCatalogContextStepClonesAtThePinnedCommit` fails, and
  `TestCatalogBuildWithoutAContextIsByteForByteTodaysRequest` **passes** today — it is the control that
  keeps this task additive.
- **Depends on.** C1 (the field) and C2 (the commit).
- **Reviewer:** opus **plus the independent review a SEC task gets** (a clone with the repository
  credential inside a build that also mounts secrets).
- **Owner-run:** no. **Commit:** `feat(gcp): a catalog build clones an entry's context at one commit`.

### C4. The trigger, and the check's read

- **Interface.** `imagecheck.CatalogRecordState.ContextSHA256` and the
  `catalog-definition` rule of §A5 — the context's identity from the narrower path list, or from the
  branch head's commit — with the check reading it **without cloning**. Its size depends on **O2**:
  under the recommendation it is a bounded provider read per entry with a context; under the
  alternative the trigger covers the copy alone and the context rides `max-age`. **SEC** — under the
  recommendation the check reads a repository credential.
- **Files (7).** `internal/imagecheck/check.go`, `internal/imagecheck/check_test.go`,
  `internal/imagecheck/catalog.go`, `internal/cli/image_check_catalog.go`,
  `internal/cli/image_check_catalog_test.go`, `internal/gitprov/gitprov.go` (read, if the path list is
  taken: the file read that also answers an identity), and the folder's docs under
  `docs/project-image-catalog/` **only if** this file's §A5 changes its recommendation when O2 is
  answered.
- **Tests.** `TestCheckTriggersOnAContextChange`,
  `TestCheckDoesNotRebuildWhenOnlyTheNarrowerPathsAreUnchanged` (the negative that makes the trigger
  worth having), `TestCheckRebuildsOnceWhenTheRecordPredatesTheContextField`,
  `TestCatalogCheckClonesNothing` (the property: no step, no clone, one bucket read and one bounded
  provider read), `TestCatalogCheckRefusesUnreadableContextState`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/imagecheck/ -run 'Catalog' -count=1` and
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'CatalogCheck' -count=1`.
- **Red step.** `TestCheckTriggersOnAContextChange` fails: the record has no context field and the
  trigger compares the copy's sha256 alone.
- **Depends on.** C2 (the resolver) and C3 (the record's field).
- **Reviewer:** opus **plus the independent review a SEC task gets** (a repository credential on the
  project-level check job) **under O2's recommendation**; standard under the alternative.
- **Owner-run:** **yes, half of it.** The unit tests are an agent's; the check's observable half is the
  named live check below, and **no agent performs any step of it**.
- **Commit:** `feat(imagecheck): the context is part of a catalog entry's definition`.

## 6. Parallel execution

**Edges** (from each "Depends on"): C1 → C3, C2 → C3, C2 → C4, C3 → C4.

| Level | Tasks |
| --- | --- |
| L0 | C1, C2 |
| L1 | C3 |
| L2 | C4 |

**Critical path:** C1 or C2 → C3 → C4. It is short and it is serial where it has to be: C3 is the only
task that changes what a build submits, and C4 is the only one that changes what the check decides
about a build it did not make, so a reviewer reads them **in order** — the record's field (C3) is the
thing C4 reads.

**Must be sequential, and why.** C3 → C4 share `internal/backend/gcp/catalog.go`'s record shape and
`internal/imagecheck`'s reader of it: the writer and the reader of one wire field land in that order,
and a reader that lands first reads a field nobody writes. C1 → C3 share `internal/config/catalog.go`
(C1 writes the field, C3 reads it), and C2 → C3 share the commit (C3 clones at the value C2's producer
records).

**Shared files.** No level has two writers of one file. Recheck this whenever a task's Files line
changes.

| File | Writers (level) |
| --- | --- |
| `internal/config/catalog.go` | C1 (L0) |
| `internal/settings/registry.go`, `schemas/settings.schema.json`, `internal/cli/docs_config_management_test.go`, `docs/config-management/*.md` | C1 (L0) |
| `internal/gitprov/gitprov.go`, `github/**`, `bitbucket/**`, `fake/fake.go`, `internal/catalog/fetch.go` | C2 (L0) |
| `internal/cli/catalog_copy.go`, `internal/cli/image_build_catalog.go` | C2 (L0) |
| `internal/backend/gcp/catalog.go`, `internal/backend/gcp/build.go` | C3 (L1) |
| `internal/imagecheck/check.go`, `catalog.go` | C4 (L2) |
| `internal/cli/image_check_catalog.go` | C4 (L2) |
| `docs/project-image-catalog/context-design.md` | this design's own pull request (no task) |

## 7. The live check

**One, and it is named and not numbered**, exactly as [implementation-plan.md](implementation-plan.md)
§Live checks states it: the register's own rule is that a claim above the highest written section is
settled at its merge, and the task that writes a check takes the number free at **its** merge. This
amendment adds **one step** to the folder's existing named check — the one that builds a catalog image
on the sandbox and launches a run on it — and it is **owner-run**: **no agent performs any step of it**.

**The added step:** an entry whose source is a Dockerfile **with a `context`** is published, built
(`fugaro image build <entry>`), and launched from; the owner confirms the run, confirms
`docker history`/the image's `/work/repo` is at the commit in the record's `source_commit`, and confirms
that a commit pushed to a path **outside** the context does **not** rebuild the entry while one pushed
inside it does. Cost: one sandbox build and one run's spend; nothing created, enabled or deleted
outside the sandbox.

## 8. Release actions

These go in the `### For operators` section of the release notes, in the pull request that ships the
build ([docs/releases/README.md](../../docs/releases/README.md); each step labelled per-installation or
per-repository).

- **C1–C4, per installation: `fugaro update`**, and nothing else. The image pin moves, the field
  arrives in the code, and the node is published by the same policy stage that publishes
  `verifier_accounts` — **no second command**, per the HARD RULE.
- **C1–C4, per person, and only for an installation that publishes an entry carrying a `context`:
  upgrade every launcher before publishing it.** This is D1156's skew extended to a member (§A7): a
  launcher whose CLI predates `context` **fails to decode the whole `image_catalog` block**, in the CLI
  and in the runner, because the member names are checked strictly before the decoder runs. An
  installation whose entries name no `context` writes the node it writes today and takes no step at
  all.
- **Under O2's recommendation, per installation:** the catalog check job's service account gains its
  read of the repositories' provider credentials, which `fugaro update` applies with the rest of the
  Terraform. This line exists **only if O2 is answered the recommended way**; the alternative adds no
  grant and no line.

## 9. Open items (O1–O4) — the owner's, and none is decided here

### O1. Does the policy `Version` move?

- **Recommendation: it does not.** `Version` stays **4**, and the skew is stated where D1156 already
  states it, extended to a member (§A7). The alternative — a bump to 5 with `VersionSince[5]` — gives
  every older CLI one clean `SkewSentence` (*"this node is version 5; policy version 5 arrived in
  fugaro <release>"*, `internal/projectpolicy/version.go`) instead of a decode error about a member
  name, and it covers readers that predate `image_catalog` entirely.
- **Cost of the recommendation:** a launcher that predates `context` but knows `image_catalog` fails
  with a member-name decode error rather than a version sentence — accurate but less helpful, and it
  reaches the **runner** as well as the CLI.
- **Cost of the alternative:** `version` is written into **every** node, so bumping it changes every
  project's bytes and forces an upgrade on installations that never write a catalog or a context —
  which is the byte-for-byte promise D1156's keep rule is built on. It is the heavier change and
  D1156 already says so.

### O2. How does the nightly check learn that the context moved?

- **Recommendation: the precise read** (§A5's rule: the narrower path list where the entry names one,
  the branch head's commit where it does not). The check reads one bounded provider read per entry with
  a context and **never clones**, which is what keeps the catalog check's cost where D1148 put it.
- **Cost of the recommendation:** the project-level check job gains a **repository credential read**
  per entry with a context, which is a new grant on a job that holds none today — Terraform, a
  reviewer's read of `internal/infra/tf/cover.go`, and C4 becoming a **SEC** task.
- **Alternative: the context rides `max-age`.** No repository read at all, no grant, no Terraform, and
  C4 shrinks to the record's field and the age rule. The cost is the spec's own §Building rule: an
  upstream change the context depends on lands up to `max_age` late (14 days today, 7 once the
  published base images land), and the spec's *"rebuilds it only if its definition changed"* is not
  answered for a context.

### O3. Is the context bounded, and where?

- **Recommendation: bounded where the CLI resolves it** — a file count and a byte total read with the
  ref resolve, refused **before** the copy is written, so a refused build leaves no trace in the bucket
  (`writeCatalogDockerfile`'s own rule). The bound is a constant beside `catalog.MaxDockerfileBytes`,
  and its refusal names the size it read and the number it may not exceed.
- **Cost of the recommendation:** the publish path grows one provider read of the tree (a new
  capability beside the file read) and one more refusal to document; a repository over the bound must
  narrow its context with a `paths` list or move the Dockerfile.
- **Alternative: no bound.** Cloud Build's own limits are the ceiling (the `3600s` timeout, the
  worker's disk, the daemon's context upload), the failure is a failed build the operator reads, and
  nothing new is added to the publish path. The cost is that a context bigger than a build can take is
  paid for once per attempt, nightly, until someone notices.

### O4. Which release carries this build, and in what order against the removal?

- **The reading in the tree.** [docs/repo-image-removal/](../repo-image-removal/README.md) puts the
  removal of the per-repository `image:`/`dockerfile:` keys in **0.19.0** (the owner's move of
  2026-11-04). This brief names the same removal chain as the **0.20.0** one. The two readings differ
  by one release, and this file does not settle it: a reader should trust the folder that owns the
  removal, and re-read it at merge.
- **Recommendation: C1–C4 land in the removal's own release, ordered ahead of its first removal task.**
  The removal's order ([implementation-plan.md](../repo-image-removal/implementation-plan.md) §2) and
  its spec's own §How a repository migrates already require the entry to exist and be built before any
  key is removed;
  C3 is what lets an entry be a repository's ordinary CI Dockerfile, so it belongs in the same window
  as R0 and ahead of R2–R8. Under either slot the ordering is identical — **catalog ships → C1–C4 →
  R0 → … → R10** — and only the release number in the notes changes.
- **Cost of the recommendation:** the removal's release carries one more SEC task (C2, C3, and C4 under
  O2's recommendation) beside a slice that already carries four independent reviews, and the 0.19.0
  tag's owner-run check reads one more step.
- **Alternative: C1–C4 land in the release before the removal (0.19.0, with the removal moving to
  0.20.0).** The removal's tasks then rest on a context key that has already shipped and been used at
  least once, at the cost of a release's worth of delay in removing keys nobody is meant to keep using.

## 10. What this amendment is not

- **Not code, and not an approval.** Nothing is built, and **no task here is approved to build**.
- **Not a new block, and not a renumbering.** No `D` number is taken (D1140–D1159 stay the folder's,
  D1159 reserved), no finding is added (F1–F16 stay the folder's sixteen), and no file of the folder's
  own is edited by this amendment.
- **Not a bake.** [docs/repo-image-removal/](../repo-image-removal/reconciliation.md) D1208's ruling is
  untouched: the entry's Dockerfile is still the repository's own file, and the engine renders nothing.
- **Not a lift of the `checkout: shared` refusal**, and not a change to `checkout`'s two values or
  their meanings ([docs/repo-image-removal/](../repo-image-removal/spec.md) §2.2, D1191).
- **Not a change to what decides a job's image.** The tag-cover rule, `image_name` and `needs` are
  untouched: this is a build input, not a selection input.
- **Not a wire-format change.** `source_commit` keeps its name and its meaning, the record gains one
  field and keeps its version, and the console, the mobile app, the history reader and the plugin read
  every field they read today.
