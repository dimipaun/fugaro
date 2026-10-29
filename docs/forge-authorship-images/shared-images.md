# Shared images keyed by the resolved dependency set

**Status: design only; nothing here is implemented.** This is the design the plan's task **C7**
(`implementation-plan.md`) asks for; **C8** builds it, and this file is meant to be concrete enough
that C8 needs no further design. Core MIT: nothing in this design is `ee/` behaviour.

The decisions are settled. **D617**: the published policy is the intent and the lock lives in the
runs bucket beside `builds/`. **D618 as answered** (the owner chose *against* the recommendation):
images are shared between repositories whose resolved dependency set is identical, scoped to exactly
that. **D619**: warn at launch on a digest mismatch; no pinning, since Cloud Run cannot vary the image
per execution (U4). **D620**: key files before the source layer. **D621**: trigger-based rebuilds; a
build is Cloud Build cost outside the run budget. **D622**: the resolver runs only on the operator's
machine. **D625**: propose, then confirm. Tension **T6** and open item **O6** are resolved
(2026-10-29): each repository's job is updated per build, the bucket lock is the one source, and a
divergence is a fork under user control, never automatic. Two values are still the owner's: the
image-count cap (§8) and the retention (§9), listed at the end of this file.

## 1. What is shared, and what is not

An image is shared between repositories whose **resolved dependency set** is identical. The set is
the thing that costs: a build installs it once, and every repository on that key runs an image that
already holds it. Nothing else is shared — not the source, not the workflow, not the repository's
secrets, not its job.

Only a **baked** workflow — one whose job runs a per-repository derived image — takes part. A
`clone` workflow's job runs the installation's base image directly and builds nothing
(`internal/infra/repo.go`'s `NeedsBuild`), so it is outside the key universe: it has no key, no
lock entry and no shared image, and its job is not moved by anything in this design.

The shared image holds the **dependency layer only**. Today's derived image bakes one repository's
source at its commit (`images/derived/Dockerfile.tmpl`, `internal/image/render.go`), and
SECURITY.md's registry note (C6) describes exactly that. The shared image holds no repository's
source at all: it is built from the key files, and a run still gets its source from the clone at
launch (`FUGARO_CHECKOUT=clone`), so one repository's readers never read another's source through a
shared image. C6's paragraph is about the per-repository derived image and needs a second sentence
for the shared one; that sentence is C8's, not this design's.

## 2. The normalised key

### 2.1 The four inputs

The key is computed from exactly four things, and nothing else:

| Input | Where it comes from |
| --- | --- |
| Resolved package names and versions | the workflow's key files — the blobs `imagecheck.KeyFiles` already returns (the workflow's `cache:` key globs, or, with no `cache:`, `config.DefaultCache`'s per-base default), reduced per ecosystem as §2.3 |
| Toolchain | the root `mise.toml` / `.tool-versions` tool pins (`config.MiseConfigFiles`) and `image.tools`' mise tool → version pairs |
| Base image | the base the build ran FROM: the record's `BaseRef` and `BaseDigest` |
| (the key's own version) | the canonical form's version line, §2.4 |

The key is normalised: **only resolved package names, versions, toolchain and base image count**.
One case sits outside those four and is not a fifth input: a key file whose format §2.3 does not
name cannot be reduced, and the repository is then **unkeyed** (§2.5) rather than hashed — a format
Fugaro cannot read must never be silently dropped, because two repositories whose tool files differ
would otherwise share an image.

### 2.2 Why `image.setup` is not an input

The plan's C7 sentence lists `image.setup` among the key's inputs; O6's control (1), the later and
owner-accepted ruling, fixes the inputs to resolved names, versions, toolchain and base image, and
this design follows O6 — stated here rather than silently, because the two texts differ. The
consequence is named where it is paid: the baked `image.setup` is the one of whichever repository
first built that key, so a repository whose `image.setup` differs while producing the same set
inherits that step (§4). A change to `image.setup` still forces a **rebuild** — it is part of the
existing `ImageConfigHash` (`internal/imagecheck/record.go`), which is the rebuild trigger and not
the sharing key — but it does not by itself make a new key, and so does not fork.

### 2.3 Reducing each ecosystem's lockfile to name and version

The workflow's key files are the evidence set; each is reduced by its own format:

| File | Reduction |
| --- | --- |
| `package-lock.json` | every entry of `packages` with a `version` (the root `""` entry is skipped: it is the repository, not a dependency), as its `name` (the path after the last `node_modules/`, or the entry's own `name`) and `version`, transitives included |
| `pnpm-lock.yaml` | every `packages` / `snapshots` key, split at the last `@` into name and version |
| `yarn.lock` | every entry header line `"name@range":` with its `version:` field; the `__metadata` block is ignored |
| `go.sum` | every `module version` line, with the `/go.mod`-suffixed duplicates skipped, so each module@version appears once |
| `requirements.txt` | every `name==version` pin; a line that is not a pin (`-r`, a URL, an unpinned name, a comment) makes the repository unkeyed |
| `poetry.lock`, `uv.lock`, `Pipfile.lock` | each `[[package]]` / `package` entry's `name` and `version` |
| `Cargo.lock` | each `[[package]]` entry's `name` and `version` |
| `Gemfile.lock`, `composer.lock` | each `specs` / `packages` entry's `name` and `version` |

Anything else — a lock file of another format, a tool file that is not a mise config, a
`requirements.txt` that is not pinned — makes the repository **unkeyed** (§2.5). Dev-only
dependencies are not exempt: they make a key like any other, because the owner did not choose to
exempt them (O6).

### 2.4 Canonical form and the hash

The canonical form is text, one line per fact, with a version line first:

```
key v1
base:<base_ref>@<base_digest>
pkg:<name>@<version>
tool:<tool>@<version>
```

- Names and versions are used as the file states them, after the ecosystem's own canonicalisation
  where it defines one (PyPI's PEP 503 normalisation, which folds case, `-`, `_` and `.`; the Go
  module path as written, which is case-sensitive).
- Every line is **deduplicated** and the lines after the version line are sorted bytewise, so the
  order of files, of entries in a file and of JSON or YAML keys cannot matter.
- The bytes end with a single newline. The hash is the lower-case hex **SHA-256** of those bytes,
  computed over UTF-8 with no BOM.
- `key v1` is the reduction's version: a change to §2.3's rules starts a new key universe instead of
  silently reusing images built under the old rules.

### 2.5 What the key ignores, and where it is computed

The key ignores: file order, comments, formatting (whitespace, indentation, key order), the
repository name, the workflow name, and build and test commands — the workflow's own `commands:` and
`image.build`, which are not dependency facts.

The key is computed **only on the operator's machine** (D622), by `fugaro image refresh` and
`fugaro project sync`, from a checkout at the repository's base branch head — the same tree
`imagecheck.KeyFiles` reads there. Never in Cloud Build and never in a job: a job runs with
installation credentials, and C2's resolver command is arbitrary code.

The **local resolver** (the withdrawn plan task C2, D622) is the key's population input, as read
here: a read-only command declared in the project layer which prints the repositories to consider,
one `owner/name` to a line; it runs only on the operator's machine, its output is intersected with
`allow_repos`, and every dropped entry is warned (F26, T3: the resolver never widens access, and it
never writes the policy). It does **not** supply versions: versions come only from the repository's
own committed key files, since a resolver that asserted versions would be a second source of truth
(D617) and would have to run repository code. A repository whose key files do not reduce is
**unkeyed**: it keeps today's per-(repository, workflow) image and its own build record, `image ls`
does not list it (§7), and the refresh treats it as it does today.

### 2.6 The key's image name

A shared image needs a home no single repository owns. Today `RegistryRepoID(slug)` is one Artifact
Registry repository per repository, and `ImageName(registry, slug, workflow)` puts a repository's
derived image in it; a sibling repository's build account cannot push there and a sibling's job
account cannot pull from there. So C8 adds **one shared registry repository per installation**
(`internal/backend/gcp/names.go`, derived like the others: a hash of a fixed purpose word, so it can
never equal a repository's own) and names the image by its key alone:
`<registry>/<shared repo>/img-<first 12 hex of the key>`. The name carries no repository and no
workflow, which is the point: the same name is the same dependency set. The install's second
repository on a key adopts the first's image rather than building a second copy (§4).

## 3. The lock

### 3.1 Path and shape

The lock is one JSON object **under `builds/` in the runs bucket**, at `builds/image-lock.json`.
It is one object because it describes the installation, not a repository; the name cannot collide
with a build record's key, which is always `builds/<slug>/<workflow>/image.json` with the slug as a
directory.

```json
{
  "version": 1,
  "images": {
    "9f2c…": {
      "key": "9f2c…",
      "image": "europe-west1-docker.pkg.dev/project/fugaro-shared-4a1b2c3d4e5f/img-9f2c1a2b3c4d",
      "digest": "sha256:…",
      "base_ref": "web",
      "base_digest": "sha256:…",
      "built_at": "2026-10-29T09:12:04Z",
      "build_id": "…",
      "repositories": ["owner/repo-a", "owner/repo-b"],
      "jobs": {"owner/repo-a": "fugaro-repo-a-web", "owner/repo-b": "fugaro-repo-b-web"},
      "last_used_at": "2026-10-29T09:40:11Z"
    }
  }
}
```

`repositories` and `jobs` are the same set seen twice: which repositories are on this image, and each
one's job name as it is at update time (the operator's machine reads the job, so it knows). They are
the answer to "is a job pointing at this image?" (§9) and to `image ls` (§7). `last_used_at` is the
last time a refresh placed a repository on the image (§9's age is measured from it), which keeps
§3.3's one-writer rule whole: a launch reads the lock (D619's warning) and writes nothing.

### 3.2 Strict decoding, and the schema

The lock decodes **strictly**: unknown fields and an unknown `version` are errors, which is the
opposite of `ParseRecord`'s deliberate tolerance. The reason is that this object is written by every
operator's machine and read by every fugaro version in use, so a tolerant reader is a destructive
one: an older fugaro that ignored a newer field and rewrote the object would drop it for everyone. A
strict reader stops and names the version instead. `schemas/image-lock.schema.json`, which C8 adds,
is the same shape for editors and for the schema checks the repository's other wire formats have.

### 3.3 Who writes it, and when

Only the operator's machine writes the lock: `fugaro image refresh` after a successful build and the
promotion of the image, and `fugaro image prune` when it removes an entry. Cloud Build never touches
it — the build's record step writes only `builds/<slug>/<workflow>/image.json` — and neither the
daily check job nor `fugaro image ls` writes anything.

The lock is written **after** a successful build, and an image already in the lock is
never rebuilt or overwritten in place. A key's entry, once present, keeps its digest; a change to a repository's
dependency set is a change of key, and a new key is a new entry (§5), which is what keeps T6(c)'s
blast radius local: the sibling repositories on the old key are untouched.

### 3.4 Concurrency: two refreshes on one key

Two operators' machines can refresh the same key at the same time. Two mechanisms, in this order:

1. A **claim** per key, `builds/image-lock.claims/<key>`, taken with `blobx.Create` (create-if-absent,
   atomic on GCS) before a build starts and released with a generation-matched delete. A second
   refresh that finds the claim adopts the image the first is building (waiting for its lock entry)
   instead of paying for a second build. A claim whose object is older than the build timeout is
   stale and may be replaced, so a crashed refresh does not block the key forever.
2. The lock write itself is **generation-matched**: `blobx.ReplaceIf(ctx, key, data, gen, prev)` with
   the generation the refresh read. A write that lost a race fails with `blobx.ErrConflict`, and the
   refresh re-reads the lock and re-plans once — it never overwrites whatever the other writer
   recorded. A second conflict exits non-zero naming the two writes, and the operator reruns.

## 4. Superset lookup, and what it costs

A superset reuses an existing image **only when it holds the whole set at the same versions**.
Before building a key, the refresh looks for an existing lock entry that holds the repository's
entire dependency set with identical `pkg:` lines — the same names at the same versions — and may
hold more. The toolchain and the base image are not superset-able: the base must be the identical
ref and digest, and the `tool:` lines identical, because an extra tool or a different base changes
what the image is, while an extra package only widens what is installed. When several entries
qualify, the one with the fewest extra `pkg:` lines wins; ties go to the most recent `built_at`, then
to the lower key hex, so the choice is deterministic and two machines pick the same image.

The cost is stated plainly because the owner accepted it (O6): the repository's image then contains
packages it does not depend on. That is a **wider supply-chain surface** — more code in the image,
more known vulnerabilities to track, more to update, and every reader of that image's registry reads
all of it. It also means the image's dependency layer was installed by another repository's
`image.setup` command (§2.2). The alternative — building one image per key — keeps each image to its
own set and pays a build per key; the design follows the owner's answer and reuses.

## 5. Divergence: propose, then confirm

A repository's key **diverges** when the refresh computes a set that the image the repository is on
does not hold. A divergence is **proposed and confirmed, never forked silently**: T6(c) was resolved
against the automatic fork the reconciliation had recommended.

The proposal names the facts and offers exactly three choices, spelled `fork`,
`adopt <repository>` and `pin`:

| Choice | What it does |
| --- | --- |
| `fork` | builds a new image for the new key; only this repository's job moves to it |
| `adopt <repository>` | puts this repository on the image `<repository>` is on, when that image is a superset of this repository's set at the same versions (§4); the choice names the repository so the operator sees whose image it is and what it carries |
| `pin` | stays pinned: keeps the image the repository is on, digest unchanged, and warns; the job is not touched and nothing is built |

The proposal prints the diff summary the decision needs — packages added, removed and their version
changes by count — and, for `adopt`, the candidates with their extra-package counts. Nothing is
built before an answer, and every choice updates **only this repository's job** (§6).

Interactive, it reads one of the three. `fugaro image refresh`
refuses to build when there is no terminal, or with `--json`: it prints the proposal (as JSON under
`--json`) and exits non-zero, unless `--on-divergence` names the choice —
`--on-divergence=fork`, `--on-divergence=adopt=<repository>`, `--on-divergence=pin`. The flag is the
only way to answer without a terminal, and like today's `--yes` (`internal/cli/image_refresh.go`) it
is **refused in a coding agent's session**. Exit codes: an unanswered proposal exits 1
(`ExitUserError`), a flag that names a repository the lock does not hold, or one whose image is not
a superset, exits 1 with that reason; a build or a job update that failed remotely exits 2
(`ExitRemoteError`).

`fugaro project sync` proposes too — the same facts, the same three choices — and **never builds**:
it prints the proposal and, interactively, the exact `fugaro image refresh --on-divergence=…` line to
run. Sync writes nothing about images; its own confirmation shape is D625's.

## 6. Each repository's own job

Cloud Run cannot vary the image per execution (U4), so the digest lives in the job. After a build, an
adopt or a pin, the operator's machine updates **only the repository whose refresh this is**: the
job's container's image alone, through the existing read-modify-write path
(`internal/infra/checkjob.go`'s `WorkflowJobUpdate`: read the job, refuse anything that is not the
owner's job by its labels, keep the shape, write the image, never return an execution token). A
sibling repository's job is not read and not written, which is what makes the divergence local.
Today that path moves only a `clone` workflow's job, straight to `base_images.base`; C8 extends it so
a **baked** workflow's job moves to `<image>@<digest>` from the lock — the same shape check, one more
caller.

## 7. `fugaro image ls`

`fugaro image ls` reads the lock and prints one row per image; it makes no registry call:

| Column | Value |
| --- | --- |
| `IMAGE` | the image name, untagged (§2.6) |
| `HASH` | the first 12 hex of the key |
| `REPOSITORIES` | the repositories on it, comma-separated, from `repositories` |
| `LAST USE` | the date of `last_used_at`, `YYYY-MM-DD` in UTC |

A final line carries the count and the cap: `12 images (cap 20)`, with the warning's wording (§8)
when the count is over. Under `--json` the shape is
`{"version":1,"count":12,"cap":20,"over_cap":false,"images":[{"key":"…full hex…","image":"…","digest":"sha256:…","repositories":["owner/repo-a"],"jobs":{"owner/repo-a":"…"},"built_at":"…","last_used_at":"…"}]}`
— the full key, the digest and the timestamps the table rounds away. A repository that is unkeyed
(§2.5) is not in the lock and so not listed; its own image is where it is today, in its build record
and `fugaro image status`.

## 8. The image-count cap

`fugaro doctor` and `fugaro image ls` warn when the lock's image count passes a cap. The cap exists
for the question the owner asked when accepting D618 — how to stop trivial differences exploding the
number of images — and the answer's other two parts are the normalised key (§2) and the superset
(§4); this is the alarm, not the mechanism. It is a **warning only**: nothing is refused and nothing
is deleted for being over it.

The proposed cap is **20** images per installation. A fixed default is proposed over a multiple of
the enrolled-repository count because the count is not what grows — a repository's own set does,
across its workflows and its dependency churn — so a cap that scaled with the repositories would
never fire on the case it exists for. The warning names the count, the cap, and the two remedies
(`fugaro image ls`, `fugaro image prune`). The value is the owner's, listed at the end of this file.

## 9. Clean-up: `fugaro image prune`

An entry is **eligible** when both hold:

1. **no job points at it** — its `jobs` is empty, which is what a `fork` leaves behind for the old
   key (a `pin` keeps the repository's job on it, so a pinned image is never eligible), and what a
   repository removed from the installation (`fugaro project sync` never un-enrols; D617/C7: a
   removed repository is dropped from the lock only) leaves behind too; and
2. its `last_used_at` is older than `images.retention_days` — no refresh has placed a repository on
   it for that long, and no run can be on it, since a run's image is fixed at launch and a run does
   not outlive its task timeout.

The clean-up never removes an image a job still points at — never one a job still points at,
whatever its age. `fugaro image prune` removes every eligible entry in one pass — the eligibility is
a rule, so no per-image judgement is asked of the operator — prints each one as it does, asks once
before the first delete (today's confirmation shape; `--yes` answers it unattended, never in a
coding agent's session), and has `--dry-run`, which prints the list and deletes nothing. The registry image is deleted first and the lock entry second, generation-matched
(§3.4), so a crash between the two leaves an entry whose digest the registry no longer has, which
`doctor` reports and the next `prune` finishes.

**`images.retention_days`, default 30, is proposed, owner to confirm.** The key belongs to the
**project layer** — the shared, published part of the operator's local config, beside `base_images`
(`internal/localcfg`) — and not to a repository's own `fugaro.yaml`: an entry is shared by several
repositories and its deletion removes a registry image some other repository may still point at, so
it is shared policy, and only the project layer sets shared policy. A machine-local override is
deliberately not read: it would let one operator's machine prune an installation-wide image on a
retention the other operators never agreed to. The value is validated where the layer is parsed: a
positive integer count of days, and nothing else.

Pruning also covers the per-repository images the migration leaves behind (§10): an image in a
repository's own registry repository whose build record shows no job on it and whose last build is
older than `images.retention_days` is removed the same way.

## 10. Migration from per-(repository, workflow) images

Nothing is rewritten at once; a repository moves when it is next refreshed.

1. Until a refresh, a repository is exactly as it is today: its own registry repository
   (`RegistryRepoID(slug)`), its own image (`ImageName`), its job on that image's digest, and its
   record at `builds/<slug>/<workflow>/image.json`.
2. The next `fugaro image refresh` computes the key (§2). With no entry for it and no superset, it
   builds once into the shared registry repository (§2.6) and moves that repository's job (§6). With
   an entry, or a superset, it adopts and builds nothing. The repository stops building its own
   image; the old one stays where it is, pointed at by nothing, and §9's second clause removes it
   after `images.retention_days`.
3. The second repository on a key finds the first's entry and adopts it, so a key built by two
   repositories is one build, two jobs, one digest.
4. An unkeyed repository (§2.5) never moves.

`fugaro update --repo` keeps creating the per-repository registry repository, since a repository may
be unkeyed and an installation may be mid-migration; deleting it is an infra change and is not part
of this design. The release that ships C8 needs a `### For operators` entry naming the shared
registry repository and the two grants below, in the same PR (`docs/releases/README.md`).

## 11. The build's credential

**Unchanged.** The build still runs as the repository's build account
(`internal/backend/gcp`'s `BuildServiceAccountID`) and still clones with the repository's existing
credential — the GitHub App's read-only installation token, or the `bitbucket-token` secret — from
the repository's own base branch. Sharing introduces no new credential, mounts no new secret, and
stores nothing new: the shared image is built by a repository's own build, from that repository's own
checkout, and the record step writes the same record it writes today (with the key added, C8a).

Two IAM facts follow from §2.6 and are not credentials: the build account needs write on the shared
registry repository, and every enrolled repository's job account needs read on it. Both are
Terraform changes in `deploy/terraform/gcp` that the plan's C8 file list does not name, and they are
the one piece of C8 not covered by its own file list.

## 12. What is baked

The dependency layer, and only it: the toolchain the key names, the packages the key names, and the
base the key names — everything the warm-up and `mise install` produce. No repository's source, no
workflow's `commands:`, no repository secret. Source comes from the clone at launch, as it does for
a `clone` workflow today, and the run's `FUGARO_COMMIT` still decides which commit that is. This is what
C3's layer order makes possible (the C8 split, below): the key-file layer is the shared image's whole content,
and the source layer that follows it is the per-run clone, not a build step.

## C8 split and its dependencies

Three tasks, in the order below; each is independently testable and each has its own red step. The
test names are the plan's C8 list, distributed to the task that makes them pass.

### C8a. The key, the superset and the lock (`imagecheck`)

- **Files (9):** `internal/imagecheck/key.go` (new), `internal/imagecheck/key_test.go` (new),
  `internal/imagecheck/superset.go` (new), `internal/imagecheck/superset_test.go` (new),
  `internal/imagecheck/lock.go` (new), `internal/imagecheck/lock_test.go` (new),
  `internal/imagecheck/record.go` (`Record` gains `Key`), `internal/imagecheck/record_test.go`,
  `schemas/image-lock.schema.json` (new).
- **Behaviour:** `imagecheck.Key(resolved) string` over §2.4's canonical form;
  `imagecheck.Lock` with strict decoding (§3.2) and the generation-matched write (§3.4);
  `imagecheck.Superset(lock, resolved)` per §4. Pure: no cloud, no registry, no bucket —
  `t.TempDir()` and `mem://` at most.
- **Tests:** `TestKeyIdenticalForIdenticalDependencySets`, `TestKeyChangesWithAnyLockfile`,
  `TestKeyIgnoresOrderCommentsFormattingRepoNameAndCommands`, `TestKeyChangesWithVersionToolchainOrBase`,
  `TestSupersetReusesAnImageWithExtras`, `TestSupersetRequiresSameVersions`, `TestLockDecodesStrictly`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/imagecheck/ -run 'Key|Superset|Lock' -count=1`.
- **Red step:** `TestKeyIdenticalForIdenticalDependencySets` fails: there is no `imagecheck.Key`.
- **Depends on:** nothing beyond today's `imagecheck.KeyFiles`. **This is the piece to land first.**
- **Commit:** `imagecheck: the normalised dependency-set key, the superset lookup and the lock`
- **Reviewer:** standard.

### C8b. Refresh, proposal and the per-repository job update

- **Files (10):** `internal/cli/image_refresh.go`, `internal/cli/image_proposal.go` (new),
  `internal/cli/image_refresh_shared_test.go` (new), `internal/cli/image_proposal_test.go` (new),
  `internal/infra/checkjob.go` (a baked workflow's job moves to the lock's digest),
  `internal/infra/checkjob_test.go`, `internal/backend/gcp/names.go` (the shared registry
  repository's name), `internal/backend/gcp/names_test.go`, `internal/image/render.go` (the shared
  render input: the dependency layer alone), `internal/image/render_test.go`.
- **Behaviour:** §2.5's key computation on the operator's machine, §4's superset lookup, §5's
  proposal and `--on-divergence`, §6's single-repository job update, §3.3's write order. The
  Terraform for §2.6's registry repository and §11's two grants is a prerequisite this file list
  does not carry (see the C3/C4 note below, and §11).
- **Tests:** `TestRefreshBuildsOnceForSharedKey`, `TestRefreshForksOnChange`,
  `TestRefreshNeverOverwritesALockedDigest`, `TestRefreshUpdatesOnlyTheChangedRepositoryJob`,
  `TestDivergenceProposesAndWaits`, `TestDivergenceNonInteractiveRefusesWithoutFlag`,
  `TestDivergenceFlagNamesTheChoice`, `TestStayPinnedWarns`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ ./internal/infra/ ./internal/image/ ./internal/backend/gcp/ -run 'Refresh|Divergence|ProposesAndWaits|StayPinned|WorkflowJob|ImageRender' -count=1`.
- **Red step:** `TestRefreshBuildsOnceForSharedKey` fails: two repositories with one set build twice.
- **Depends on:** C8a and **C3** (see below).
- **OWNER-RUN:** a sandbox refresh across two repositories with one set and one with another: one
  build, two jobs on one digest, a proposal (not a build) on a dependency change, and each of fork,
  adopt and stay-pinned.
- **Reviewer:** opus + mandatory independent review (the registry layout and job definitions of every
  installation change).
- **Commit:** `image: repositories with the same resolved dependency set share one image`

### C8c. `image ls`, prune, the cap warning, the reference and the skill

- **Files (8):** `internal/cli/image_ls.go` (new), `internal/cli/image_ls_test.go` (new),
  `internal/cli/image_prune.go` (new), `internal/cli/image_prune_test.go` (new),
  `internal/cli/doctor.go`, `internal/cli/doctor_test.go`, `website/content/cli-reference.md`
  (regenerated by `go run ./internal/clidoc`), `plugin/skills/working/SKILL.md` (the `image`
  commands the skill names — the edit waits until the 0.13.0 freeze on `plugin/**` lifts).
- **Behaviour:** §7's table and `--json`, §8's warning in `doctor` and `image ls`, §9's prune with
  `--dry-run` and its two conditions, §10's migration clause for a repository's own old image.
- **Tests:** `TestImageLsListsHashesAndRepos`, `TestDoctorWarnsOverImageCap`,
  `TestPruneRemovesImagesUnusedForRetentionDays`, `TestPruneKeepsAnImageAJobPointsAt`, plus the CLI
  reference's own test after the regeneration.
- **Command:** `go run ./internal/clidoc`; then
  `GOFLAGS=-p=2 go test ./internal/cli/ ./internal/clidoc/ -run 'ImageLs|ImagePrune|DoctorWarnsOverImageCap|CliReference' -count=1`.
- **Red step:** `TestPruneKeepsAnImageAJobPointsAt` fails: there is no `fugaro image prune`.
- **Depends on:** C8a, C8b.
- **Commit:** `cli: fugaro image ls and image prune, and the image-count cap in doctor`
- **Reviewer:** standard.

### What C8 needs from C3 and C4

- **C3 (lockfiles before source in the derived image): C8 cannot ship without it.** The shared image
  *is* the dependency layer, and a shared image must hold no repository's source (§1, §12) — which is
  only expressible because C3 puts key files before the source layer, so the layer a sibling's job
  runs is the dependency layer and the source layer behind it is the per-run clone. Without
  C3 the first repository's source would sit in the layer every sibling's job runs, and one
  repository's registry readers would read another's source: the security property of D618's scope.
  The **smallest piece C8 needs** is C3's render ordering alone —
  `internal/image/render.go`'s key-files-before-source layout (and the template it renders) with
  `TestRenderPutsKeyFilesBeforeSource` green — not C3's whole task; C8's own `render.go` edit rebases
  on that ordering.
- **C4 (the image digest checked at launch): C8 can ship without it.** C4 adds a *warning*; C8's
  mechanism is the job update (§6). Shipping C8 first loses only the mismatch warning, and C4 then
  lands with the plan's `TestRunDigestComparesAgainstTheLockForItsKey` — the digest compared against the lock for the repository's key, rather than against the build record — which is one change to
  the comparison's source, not a rework of the warning. If C4 ships first as written, C8 owns that
  one change; the lock is C8a's, so either order works.

**Options for the owner** (this is the C3/C4 question the open list asks, stated as options rather
than decided here):

1. **C8a now, C8b/C8c after C3.** C8a is pure and needs nothing shipped; it can land beside C3's work.
   C8b waits for C3's ordering. C4's warning lands whenever it is convenient and takes its
   lock-aware form.
2. **C3 → C4 → C8a/b/c**, the plan's level order, one slice C at a time.
3. **C3 → C8a/b/c with C4 folded into C8b**, so the lock-aware launch warning is written by the task
   that owns the lock.

## Open for the owner

- **The image-count cap value** (§8): proposed **20** images per installation, warned by `doctor` and
  `fugaro image ls`, never enforced.
- **`images.retention_days`, default 30, is proposed, owner to confirm** (§9): owned by the project
  layer, applied by `fugaro image prune`, never to an image a job still points at.
- **The C3/C4 question** (stated as options in the C8 split): whether C8 ships after both, or C8a now with
  C8b/C8c gated on C3 — C8 can ship without C4, and cannot without C3.
