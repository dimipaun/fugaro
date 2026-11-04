# Project image catalog: reconciliation against the code

**Reconciled against** `origin/main` at `1fc82c43` (2026-11-02) and against the open pull requests of
2026-11-02. **Where this file disagrees with [spec.md](spec.md), this file wins.** Nothing here is
implemented and no task is approved: answering a decision is not approving a task.

Line numbers are that commit's. Each citation names a file and a symbol, and the number is the
symbol's **declaration** line as it stood there; a line drifts the first time anyone edits the file
above it, so the **symbol**, not the number, is what a reader should search for, and every claim also
states the fact in words.

**Numbers:** this work holds **D1140–D1159**. The highest decision **written** on `main` is **D1139**
(`docs/console-settings-edit/reconciliation.md`, that work's block D1100–D1139, of which D1122–D1139
are reserved and unused); the highest any **open** pull request writes is **D1066**
([PR #1602](https://github.com/dimipaun/fugaro/pull/1602)) and **D1040**
([PR #1496](https://github.com/dimipaun/fugaro/pull/1496)). No `D114x` or `D115x` appears anywhere in
`docs/` on this tree or in an open pull request's, so **D1140** is free and the block is taken above
every number either holds. Findings are **F1–F16**, this folder's own.

**The owner answered seven decisions on 2026-11-02** — **D1140** (where the definitions live),
**D1146** (a manual pin), **D1149** (the `docker` tag's backend), **D1150** (build secrets),
**D1152** (the registry), **D1155** (an override plus a catalog reference) and **D1158** (the build's
release) — each as the recommendation, and each written into its row and into every place that
depends on it. **The owner answered D1148 on 2026-11-03: build the scheduled rebuilds, in 0.18.0**
(its row, and §4). That answer is **not** this folder's first round's mistake coming back — that
round marked D1148 answered with no ruling behind it, and this one records a ruling with a date and
a release on it. The remaining decisions are **open**: they are the plan's to write and the owner's
to answer, and nothing is built until the owner approves the plan ([README.md](README.md)).

## 1. The findings

Each finding is **Exists** (the code has it), **Missing** (it does not) or **Conflicts** (it exists
in a shape the spec does not expect), with its consequence for the spec.

**F1: Exists. Images are per `(repository, workflow)`, not per project.**
Every workflow gets its own derived image, built from the workflow's `image:` block and named from
the repository's slug (`docs/design/v1.md` §7.2; `internal/infra/spec.go:936`, `image :=
gcp.ImageName(c.registryPath(), slug, name) + ":latest"`). There is no project-level image object
anywhere. *Consequence:* the catalog is a **new layer above** the derived image, not a rename of it:
the per-workflow image stays, and the catalog is what the default one is built from.

**F2: Missing. `image:` is a build-customisation block with no name.**
`Workflow.Image` (`internal/config/config.go:603`, `Image Image`) is `type Image struct`
(`internal/config/config.go:730`) holding `Apt`, `Setup`, `SkipBuildScripts`, `Tools`, `KeyFiles`
(`:737`, `:738`, `:743`, `:747`, `:760`) and two 0.7.0 tombstones (`Node` `:733`, `JDK` `:736`). It
carries **no name**, no tags and no default marker, and `IsZero()` (`internal/config/config.go:764`)
answers only whether the block sets nothing. *Consequence:* the spec's named entries with tags and a
default are new fields; they cannot be spelled with today's block.

**F3: Conflicts. `dockerfile:` is one repository-relative path, and only the repository's own.**
`Workflow.Dockerfile string` (`internal/config/config.go:604`) is a single path, refused unless it is
relative and inside the repository (`validateImage`, `internal/config/image.go:109`; its `dockerfile`
message is at `:156`: "must be a relative path inside the repository"). The spec's `repo-name:path` form — a Dockerfile in a **named**
repository — does not exist, and neither does a branch or a build context. *Consequence:* the
catalog's `source: dockerfile` needs a new grammar (repo + path + optional branch), and the existing
`dockerfile:` key keeps its meaning.

**F4: Exists. The daily image check already rebuilds on definition, base and dependency-file
changes.**
`internal/imagecheck/check.go` decides from triggers (`no-record`, `image-config`, `lockfiles`,
`base`, `paths`, `max-age`, `built-commit-gone`, `latest-drift`; `docs/design/v1.md` §7.2) and writes
`builds/<slug>/<workflow>/check.json` (`CheckKey`, `internal/imagecheck/check.go:221`). The
`lockfiles` trigger already reads the workflow's key files (`KeyFiles`,
`internal/imagecheck/record.go:237`). *Consequence:* the spec's nightly-rebuild rule is **mostly
built**; the catalog adds a trigger and points it at catalog entries, and it does not need a new
scheduler.

**F5: Exists, as `internal/imagecheck` (C8a). The dependency-set key, the superset lookup and the
lock are built.**
`Key(r Resolved) string` (`internal/imagecheck/key.go:52`), `KeyVersion = 3`
(`internal/imagecheck/key.go:26`), `Superset(lock *Lock, resolved Resolved) (string, bool)`
(`internal/imagecheck/superset.go:33`), and `Lock`/`LockedImage`/`ParseLock`/`Update`
(`internal/imagecheck/lock.go:45`, `:51`, `:93`, `:226`) under `LockKey = "builds/image-lock.json"`
(`internal/imagecheck/lock.go:37`). C8b (refresh, proposal, the per-repository job update) and C8c
(`image ls`, prune, the cap warning) are **not** built: `Update` has no caller in `internal/` outside
its own file, and there is no `fugaro image prune`. *Consequence:* the catalog must **not** collide
with the lock: it is a different object with a different key, and **D1141** settles their order.

**F6: Conflicts. `checkout: shared` is refused outright today.**
`validateCheckout` (`internal/config/image.go:290`) refuses `checkout: shared` while
`sharedCheckoutShipped` (`internal/config/image.go:245`) is false, with `SharedCheckoutRefusal`
(`internal/config/image.go:266`): "no released fugaro builds or refreshes a shared dependency-layer
image yet…". The kind's three values are `CheckoutBaked`/`CheckoutClone`/`CheckoutShared`
(`internal/config/config.go:645`, `:646`, `:652`), defaulting to `baked`
(`internal/config/defaults.go:280`). *Consequence:* the catalog is a **baked** story — it says what a
baked image is built from — and does not lift this refusal (**D1142**).

**F7: Exists. The job's image is fixed at deploy time, and Cloud Run cannot vary it per execution.**
`internal/infra/spec.go:936` writes the job's `Image` once, and the launcher only *judges* it
(`warnImageDigestDrift`, `internal/cli/run.go:1009`) — the comment at `internal/cli/run.go:990`
states Cloud Run cannot vary the image per execution. Moving a job's image is `PlanWorkflowJob`
(`internal/infra/checkjob.go:377`, writing `ct["image"]` at `:443`) or the check job's
`RewriteCheckSpec` (`internal/infra/checkjob.go:66`). *Consequence:* the catalog's "the job runs an
image that covers its repos" is a **deploy-time** decision, made when the job is deployed, not at
launch; the tag-cover choice is computed where the job spec is built (**D1146**).

**F8: Missing. There is no project-level Artifact Registry repository for catalog images.**
A repository's images live in its own registry (`RegistryRepoID(slug)`,
`internal/backend/gcp/names.go:251`; `deploy/terraform/gcp/modules/repo/registry.tf:10`), and the
installation has one shared registry (`SharedRegistryRepoID()`, `internal/backend/gcp/names.go:267`;
`deploy/terraform/gcp/modules/installation/registry.tf:93`, a hard-coded literal). A catalog image is
per **project**, not per repository. *Consequence:* the storage is a new decision (**D1152**).

**F9: Conflicts. The credentials service is a closed-set model-credential bundle, not a build-secret
broker.**
The service's routes are the model path, the review sign/verify routes and the GitHub mint
(`internal/credsvc/server.go:35-48`), and its bundle is one Secret Manager document whose key set is
**closed** — model credentials, the GitHub App key, the Bitbucket credential, the review-signing key
(`internal/credsvc/bundle.go:30-52`). Workflow build secrets travel a **different** path today: the
workflow's `secrets:` (`internal/config/config.go:607`) are declared in `_SECRET_ENVS`
(`internal/backend/gcp/build.go:185`), added to `availableSecrets`/`secretEnv`, and mounted as
BuildKit **file** secrets (`docs/design/v1.md` §7.2; `internal/image/render.go:132`), because Cloud
Build's docker refuses `env=` secret mounts. *Consequence:* the spec's "build secrets come from the
project's security service" is **not** how build secrets reach a build today; the owner's answer is
that the catalog **reuses the existing workflow-secret path** and the service is **unchanged**
(**D1150**, answered 2026-11-02).

**F10: Exists, as `internal/image`. The runtime contract is the selftest, not a published list.**
`SelftestSpec` (`internal/image/selftest.go:30`) and `Selftest` (`:160`) check: not root, no
passwordless sudo, `tini` as PID 1 (`CheckInit`), the presence checks of `images/base/tools.tsv`
(`Tools`; the `critical` tier is the derived image's smoke), every mise tool installed (`Mise`), the
baked checkout at its commit with an https origin and no credentials, the setuid/setgid and
capability scan (`RootChecks`), and `fugaro verify build` (`SkipVerify` false). `SpecForCloud`
(`:80`) is the structural subset the Cloud Build smoke runs. *Consequence:* "the contract list" is
`images/base/tools.tsv`'s `critical` rows plus these structural checks — there is no separate
document, and the spec's "quick check inside the image" is **already built** (**D1151**).

**F11: Exists. The published policy node has a closed known-key set and an operator-owned rule.**
`Policy` (`internal/projectpolicy/policy.go:130`) with `knownTopLevelKeys`
(`internal/projectpolicy/policy.go:295`), `Version = 4` (`internal/projectpolicy/version.go:79`), and
`UnknownKeys` (`internal/projectpolicy/policy.go:290`): the runner tolerates an unknown key and logs
it (`internal/runner/projectpolicy.go:125`), the CLI **refuses** a node carrying one
(`refuseUnknownPolicyKeys`, `internal/cli/project_cmd.go:2836`). Operator-owned keys are refused in
the shared config by `sharedForbidden` (`internal/cli/sharedcfg.go:209`). The project-layer file has
its own reserved keys, and **`environments`** — the image catalog of `docs/design/layered-config.md`
§9 — is one of them (`internal/config/layer.go:264`: "the image catalog (named environments) arrives
with the single base image … this phase refuses it"). *Consequence:* a new published key carries the
`verifier_accounts` skew (**D1156**), and the name `environments` is **already reserved** for exactly
this concept.

**F12: Conflicts. One run, one repository.**
The task, the run store's keys, the job, its service account, the image, the registry and the git
token are all per repository (`docs/forge-authorship-images/reconciliation.md` F15–F18: "one run,
one repository" is "the deepest invariant in the code"). A "multi-repo job" the spec's §"Choosing an
image for a job" speaks of **does not exist**. *Consequence:* the multi-repo half of the tag-cover
rule is designed **for a job that does not exist yet**; the catalog must be written so the single-repo
case works today and the multi-repo case is the change-set work's to reach (**D1154**).

**F13: Missing. `fugaro image ls|add|rm|promote` do not exist.**
The `image` family is `build`, `render`, `selftest` (hidden), `git-credential` (hidden), `gate`
(hidden), `record` (hidden), `check`, `status`, `refresh` (`internal/cli/image.go:33-34`; the
`git-credential`/`gate`/`record` files are `internal/cli/gitcred.go`, `internal/cli/image_record.go`).
`ls`, `add`, `rm` and `promote` are absent. `image status` (`internal/cli/imagecheck.go:945`) reads
per-workflow records; `image refresh` (`internal/cli/image_refresh.go:335`) moves a repository onto a
release base image. *Consequence:* the spec's CLI sketch is four new subcommands, and two of the four
names are already spoken for (**D1143–D1145**).

**F14: Exists, as a naming collision. `catalogSince`/`catalogShipped` are the *price* catalog.**
`catalogSince` (`internal/cli/recipes_skew.go:135`) and `catalogShipped`
(`internal/cli/recipes_skew.go:152`) gate a route's `require_zdr`/`price_policy` — the model
**price** catalog, unrelated to images. *Consequence:* a new `catalogShipped` name would collide; the
image catalog's gate must be spelled apart from it (**D1156**).

**F15: Exists, as a collision. `promote` is a Cloud Build step, not a CLI command.**
The derived-image build's steps are `prep`, `credential`, `source`, `render`, `build`, `smoke`,
`gate`, `promote`, `record`, `untag` (`images/derived/cloudbuild.yaml`; `docs/design/v1.md` §7.2),
and `promote` is the step that moves `:latest` by digest. There is no `fugaro image promote`
command. *Consequence:* the spec's `fugaro image promote` is a new **CLI** command whose name collides
with a build step's, and the reconciliation says which one a reader is looking at (**D1145**).

**F16: Exists. The `docker` tag has no execution backend today on Cloud Run.**
Cloud Run has no Docker daemon: "Cloud Run has no Docker daemon, so the tests' usual container
fallback is unavailable" (`docs/design/v1.md` §7.1.1). The Batch/VM backend exists in code
(`internal/backend/batch/`) and is designed to bind-mount the host's Docker socket
(`docs/design/compute-backend.md`: the agent's `docker` CLI "talks to the host daemon"). The workflow
key that would turn it on, `docker: true`, is **C16's and does not exist in this tree**:
`internal/backend`'s `LaunchSpec.Docker` (`internal/backend/backend.go:153`) and `internal/agent`'s
`EnvSpec.Docker` (`internal/agent/env.go:65`) are the seam's two halves, and `internal/config` carries
no `docker:` field for either to be set from (`internal/cli/validate_batch.go:46` records exactly
that). *Consequence:* the spec's `docker` tag names a capability that today only the VM backend has,
and even there nothing in the tree writes the key that asks for it (**D1149**).

## 2. The decisions

**D1140–D1159 are this work's block.** Each row states the options, the **recommendation first**, and
the cost of the alternative. **D1140**, **D1146**, **D1149**, **D1150**, **D1152**, **D1155** and
**D1158** are **ANSWERED** by the owner on 2026-11-02, each as the recommendation; **D1148** is
**ANSWERED** by the owner on 2026-11-03, not as the row's recommendation but as a **build** in
0.18.0; the rest are **open**. Where a decision's answer is a
published key, the key is **omitted when empty** and the skew is stated in **D1156**.

| # | Question | Options, recommendation first | Cost of the alternative |
| --- | --- | --- | --- |
| **D1140** | Where do catalog definitions live: Firebase project state, the project Git repo, or both? (**open question 1**) | **ANSWERED by the owner, 2026-11-02, as recommended: the PUBLISHED PROJECT POLICY NODE — a new top-level key `image_catalog` of `internal/projectpolicy.Policy` — COPIED FROM THE LOCAL CONFIGURATION `fugaro update` already holds, operator-owned via `sharedForbidden`, exactly as `verifier_accounts` travels (D1075).** The **Dockerfile bytes** are not in the node: they are copied into the runs bucket under `builds/catalog/` before the build, as the spec's §"Where Dockerfiles live" requires, and the node holds the **pointer** (repo + path + branch, or an inline hash). **The key is `image_catalog` and NOT the layer name `environments`**: `environments` is already reserved for a different design (`internal/config/layer.go:264`) and this work does not take it (§5, item 3). This is how policy is already published: `fugaro update`'s policy stage copies the local configuration into the node, its generations are recorded (D854, `docs/policy-migration.md:88`), and an operator-owned key never travels in the shared config a launcher can write | **The project Git repo** (a `.fugaro/catalog.yaml` committed beside `fugaro.yaml`) puts the definitions where a reviewer sees them in a PR, but it needs a project repo that may not exist (the spec's own §"Where Dockerfiles live" says "the project repo is optional"), it makes the catalog unreadable to a launcher without a checkout, and it is a **second** publication path beside `fugaro update` — the HARD RULE's "one command is the whole upgrade" forbids that. **Both** is two sources of truth for one key, which the project-layer design already refuses elsewhere. The **local configuration alone** (never published) cannot be read by the cloud build or the check job, which run without the operator's machine |
| **D1141** | What is the catalog's relationship to the shared-images design (D617–D622, C8a built, C8b/C8c not)? | **The owner's ruling, 2026-11-02: CATALOG FIRST.** The catalog is the **default** way a repository's image is defined. The dependency-set key, the superset lookup and the lock stay in the tree (`internal/imagecheck`), **C8b/C8c stay unscheduled and are not cancelled**, and [docs/forge-authorship-images/shared-images.md](../forge-authorship-images/shared-images.md) gains one relationship note saying so. A catalog entry **may** later be keyed by a resolved dependency set — the two are composable, because a catalog entry's `source: dockerfile` is just a definition — but the catalog does not depend on the key, and the key does not depend on the catalog | **Cancelling the shared design** would delete C8a's built code and a decided design (D618 as answered), which the owner's ruling forbids. **Building the catalog on top of the key** (a catalog entry per resolved dependency set) inverts the spec's whole point — hand-curated images, no dependency detection — and makes the catalog wait for C8b/C8c |
| **D1142** | How does the catalog interact with `checkout: baked\|clone\|shared`? | **The catalog is what a `checkout: baked` image is built FROM; it changes no checkout kind.** `baked` stays the default (`internal/config/defaults.go:280`), and a repository that names a catalog entry still declares `checkout: baked` — the entry replaces the per-workflow `image:`/`dockerfile:` **definition**, not the kind. `checkout: clone` keeps its meaning (the job runs the base image, no build) and **refuses** a catalog reference the way it refuses `image:`/`dockerfile:` today (`internal/config/image.go:322-344`). `checkout: shared` keeps its own refusal (`SharedCheckoutRefusal`, **F6**) and is **not** lifted by this work | **A fourth checkout kind** (`checkout: catalog`) would multiply the launch gates (`checkCloneImageSince`, `checkSharedImageSince`, `checkImageSince`, `internal/cli/recipes_skew.go:968`, `:1026`, `:797`) and the job-shape rules for no gain, since a catalog image **is** a baked image with a project-level definition |
| **D1143** | `fugaro image ls`: one command for the catalog and the shared lock, or a section? | **One command, two sources.** `fugaro image ls` lists **catalog entries** (name, tags, default, digest) by default; `--shared` lists the shared-image lock's entries (the C8c design's columns). The catalog is what a person reads day to day; the shared lock is an operator's inventory. This is the shape `fugaro image status` already takes (one command, per-workflow rows) | **Two commands** (`image ls` for the catalog, `image lock ls` for the lock) doubles the surface and leaves "which `ls` do I run" unanswered. **The lock only** (C8c's plan) leaves the catalog with no listing command, and the spec's §CLI sketch asks for one |
| **D1144** | `fugaro image build`: extend the existing subcommand or add a new one? | **Extend the existing one.** `fugaro image build [name]` builds the **named catalog entry** (or all of them) when a catalog is published; with no catalog and no name it keeps today's meaning exactly — build the workflow's derived image (`internal/cli/image.go:74`). The two never collide because a catalog and a per-workflow `image:` are the same thing at different scopes (D1142), and `--workflow` still selects the per-workflow case | **A new `fugaro catalog build`** adds a second verb for one concept and forces every doc and skill to name both; **overloading without a rule** makes `image build` ambiguous, which is why the rule above is stated |
| **D1145** | `image add`, `image rm` and `image promote`: new subcommands, and the `promote` name collision | **Three new subcommands.** `fugaro image add <name> --tags … --dockerfile <repo:path> \| --inline <file>` and `fugaro image rm <name>` are the spec's; `fugaro image promote <repo> [name]` is the migration (D1147). The **name `promote` collides with the Cloud Build step** `promote` (`images/derived/cloudbuild.yaml`; F15): they are different things — the CLI command copies a repository's image definition into the catalog, the build step moves `:latest` — and each doc that names one says which. `image rm` **refuses** an entry a repository or a job still points at, the rule `image prune` already carries in the C8c design | **Naming the migration something else** (`image adopt`, `image import`) loses the spec's own word and its "opts in" sense; **hiding the collision** (no note) leaves two `promote`s a reader conflates |
| **D1146** | The tag-cover selection: algorithm, tie-breaks, and the fail-before-start message | **ANSWERED by the owner, 2026-11-02, as recommended: a manual pin acts AT DEPLOY TIME and prints a WARNING; an empty needed set picks the DEFAULT; and the choice is made where the job spec is built.** The algorithm is total and deterministic: needed tags are the union over the job's repositories; a candidate **covers** the set iff every needed tag is in its tags; among covering candidates pick the one with the **fewest tags**; on a tie prefer the **default** if it covers, else the **lexicographically first name**; a needed set that is **empty** picks the **default** outright, not the entry with the fewest tags (a repository that names nothing needs nothing, and the spec's "nothing: it uses the project default" is what that means); a **pin** — a hand-pinned image, or a repository's `image: <name>` — short-circuits the whole choice and is used as given, with a **warning** printed at deploy time if it does not cover. No candidate covering the set is a **refusal before the job starts**, with the exact message in §3.3 | **First-match by catalog order** is order-dependent and non-reproducible; **most tags** picks the largest image and is the opposite of the spec's "fewest"; **silently using the default** hides a missing capability until the job fails mid-run |
| **D1147** | Promote semantics: what `fugaro image promote <repo> [name]` does | **It reads the repository's existing image definition and writes a catalog entry, then points the repository at it — never the reverse, and never automatically.** The definition copied is what the repository has today: its workflow `image:` block (rendered through the engine's template, `internal/image/render.go:144`) or its `dockerfile:` path. The entry's name is `[name]`, else the repository's name. The repository's `fugaro.yaml` then names `image: <name>` (D1155) in the same commit, so a reader sees one change. Nothing is converted unless the command is run: existing images keep working untouched | **Automatic conversion on `fugaro update`** would rewrite every repository's image definition in one release with no confirmation, which the HARD RULE's "no silent repair" and the spec's "never converted automatically" both forbid. **Writing the entry but not repointing the repository** leaves a catalog entry nothing uses |
| **D1148** | Should images be rebuilt on a schedule for base-image security updates? (**open question 2**) | **ANSWERED by the owner, 2026-11-03: BUILD IT, IN 0.18.0.** The answer is the recommendation's **yes** with a different shape: **one project-level scheduled check** evaluates **every catalog entry** with the trigger set §4 states (`catalog-definition`, `base`, `lockfiles` for a baked entry, `max-age`), reusing the daily check's code — its back-off, its paused-until-a-record rule, its record (`internal/imagecheck`'s `DecideCatalog` and `CatalogCheckState`, and `CheckCatalog`, the one pass over the catalog's buildable entries) — and **each entry that fires gets one catalog build**: the **same build account** and the **same BuildKit-secret path** the entry's own `fugaro image build` uses (**D1150**), the same smoke and the same record. The schedule is a **new Terraform piece**: one Cloud Scheduler job and the target it invokes, at installation level, **not** a job per repository — the row's own recommendation ("no new scheduler, no new job family") is what the owner **did not** take. **No new account and no broader grant**: the build account's existing catalog grants (`builds/catalog/` read and the shared-registry tag mover) are what the scheduled build runs on | **A second nightly job for catalog images** duplicates the check's back-off, its paused-until-a-record rule and its per-repository scheduler, and pays a second Cloud Scheduler line per project. **No schedule** (rebuild only on demand) leaves a base-image CVE in a catalog image until someone runs `image build`, which the spec's §Building explicitly does not want |
| **D1149** | How does the `docker` tag map to an execution backend (gen-2 Cloud Run or VM)? (**open question 3**) | **ANSWERED by the owner, 2026-11-02: VM/Batch only, and there is ONE rule everywhere.** The rule: **on Cloud Run, a docker-tagged entry is left out of the candidates unless `docker` is needed; if `docker` is needed, Cloud Run refuses.** So a Cloud Run job's candidate set is the catalog's entries **minus** every entry tagged `docker`; a needed set that carries `docker` therefore has no candidate on Cloud Run and is the §3.3 refusal, naming the backend. A VM (Cloud Batch) job's candidate set is the whole catalog, and its container has the host's Docker socket (`internal/backend/batch/`, `docs/design/compute-backend.md`). The tag is a **capability claim** the algorithm checks against the job's backend, not an image property | **Building a Docker daemon into a Cloud Run image** is not possible (no privileged containers, no `dockerd` — `docs/design/compute-backend.md`). **Treating `docker` as a normal tag** lets a job start on Cloud Run and fail the first time it runs `docker`, which is the fail-after-start the spec forbids |
| **D1150** | **SEC.** How does a catalog build get its build secrets, and what is the security property? | **ANSWERED by the owner, 2026-11-02, as recommended: the EXISTING WORKFLOW-SECRET PATH, and the credentials service is UNCHANGED.** Reuse that path unchanged: the value lives in Secret Manager, the catalog entry names the logical secret, and the build mounts it as a BuildKit FILE secret — never a layer, an ENV, a build argument or a log.** This is what `_SECRET_ENVS` and `internal/image/render.go:132`'s `secretMounts` already do (`docs/design/v1.md` §7.2: "the value is on no command line and in no layer, `ENV`, build argument or log"), and what `images/scan.sh --secrets` (`images/trivy-secret.yaml`'s empty allow-list) enforces. The credentials service's closed bundle (`internal/credsvc/bundle.go:30-52`, F9) is **not** widened: a build secret is a workflow secret, and the service holds model credentials. BuildKit `env=` mounts stay refused (Cloud Build's docker rejects `exec.secretenv`) | **Widening the credentials service's bundle** with build secrets puts a private npm token in the same document as the model credentials, under the same `secretAccessor` grant, and grows the closed key set the loader refuses a typo against — for a secret the build path already carries without it. **A plain Docker `ARG`/`ENV`** writes the value into the image's history, which the scan refuses and which the spec's §Build secrets forbids in as many words |
| **D1151** | What is "the contract list" the runtime contract checks? | **The built `images/base/tools.tsv` `critical` rows plus the structural selftest — there is no separate list to publish.** The post-build check runs `image.SpecForCloud`'s checks (`internal/image/selftest.go:80`): not root, no passwordless sudo, `tini` as PID 1, the `critical` tools (`fugaro`, `claude`, `git`, `gh`, `mise`, `tini`, `finalize-checkout`; `images/base/tools.tsv:8-14`), every mise tool installed, and the checkout's origin and credentials. `fugaro verify build` is **not** in the cloud check (`SkipVerify`), because it would run repository code. The catalog's own addition is one thing the spec asks for and today's smoke does not: the check runs on **any** base, so it also asserts the image carries `git` and a shell even when it does not `FROM` a Fugaro base | **A new hand-written "contract list" document** duplicates `tools.tsv` and drifts from it. **Checking the `FROM` line** is what the spec's §Runtime contract says Fugaro does **not** do |
| **D1152** | Where do built catalog images live, and under what names and retention? | **ANSWERED by the owner, 2026-11-02, as recommended: the SHARED registry with the EXISTING retention.** A project-level registry, one image per catalog entry, tagged by content hash with `latest` beside it.** The installation already has one shared registry (`SharedRegistryRepoID()`, `internal/backend/gcp/names.go:267`; `deploy/terraform/gcp/modules/installation/registry.tf:93`), and a catalog image is per **project**, so it lives there, named from the entry: `…/fugaro-shared-images-<hex>/catalog-<name>:<hash>` with a moving `:latest`. Retention is the registry's existing cleanup posture (keep `latest` and the 3 newest, delete untagged after 14 days, delete `candidate-` after 2 — `deploy/terraform/gcp/modules/installation/registry.tf:93-155`), which is the spec's "a few recent versions are kept for rollback". **What the code does today is `:latest`, not a digest**: `internal/infra/spec.go:936` writes `gcp.ImageName(...)+":latest"` into the job spec, and the launcher only *warns* when the running digest drifts from the build record's (`warnImageDigestDrift`, `internal/cli/run.go:1009`). The catalog keeps that shape — the content-hash tag is what a rebuild moves, `:latest` is what the job names — and **pinning a job to a digest is not part of this plan** | **A registry per project** would create a new Artifact Registry repository per installation for no isolation the shared one lacks (both are in the project). **Per-repository registries** (`RegistryRepoID`) put one project-level image in one repository's registry, so a second repository's job could not pull it without a grant |
| **D1153** | Baked repos: how does a job fetch forward on top of the baked copy? | **The catalog entry lists baked repositories; at launch the runner fetches the ref and force-checks-out on top of the baked copy, exactly as it does today.** `docs/design/v1.md` §4.1 already does this for the one repository a baked image holds: "at bootstrap the runner fetches the ref and force-checks it out", and `internal/imagecheck/gittree.go:205`'s `Changed` is what the nightly check uses to see a dependency-file change. A catalog entry with **several** baked repositories is the multi-repo case (**D1154**) and is designed, not built. "Ordinary commits never need a rebuild" is true because the fetch is the delta | **Rebuilding on every commit** contradicts the spec's "ordinary commits never need a rebuild" and burns a build per push. **No fetch** bakes stale source, which the spec's §Baked repos forbids |
| **D1154** | Multi-repo and linked runs: what does a "multi-repo job" mean today? | **Today, nothing: one run, one repository (F12).** The catalog is written so the **single-repo** case is complete and the **multi-repo** case is the change-set work's (`docs/forge-authorship-images/`'s slice D, gated on D613–D616). The tag-cover rule (**§3**) is stated for the union of a job's repositories so it is correct the day a multi-repo job exists, and today the union has one member — which makes it the repository's image, its tag match, or the default, exactly the spec's single-repo rule. A **linked** run (one job ID across several repositories, `docs/forge-authorship-images/spec.md` §"Multiple repos in one image") is that same change-set work's | **Designing a multi-repo job here** duplicates the change-set design and pre-empts D613–D616. **Ignoring multi-repo** would make §3 wrong the day it lands, so the rule is stated generally and the single-repo case is what ships |
| **D1155** | Precedence: repo-level override, `image: <name>`, `needs: [tags]`, project default | **ANSWERED by the owner, 2026-11-02, as recommended: an override and a catalog reference used together are REFUSED, and the error names BOTH keys.** In that order, first match wins: (1) a repository's own `image:`/`dockerfile:` block — today's keys, unchanged — is the override and wins outright; (2) `image: <name>` names one entry; (3) `needs: [tags]` runs the tag-cover algorithm (**§3**) over the project's entries; (4) nothing set uses the project default. A repository that sets **both** an override and a catalog reference is **refused** at `fugaro validate`, and the message names **both keys** — the override's (`image`/`dockerfile`) and the catalog reference's (`image_name`/`needs`), in the shape `image and dockerfile are mutually exclusive` already takes (`internal/config/image.go:115`) | **The catalog winning over a repo override** makes the override useless, which the spec's §Repo configuration item 4 forbids. **No refusal for both** leaves the precedence unstated in the config, so two readers could disagree |
| **D1156** | **Skew.** What does the new published key cost an older reader, and what is its honest note? | **One new top-level key, `image_catalog`, and the `verifier_accounts` note verbatim in shape: the publisher writes NOTHING when the catalog is empty, so a project that names no catalog has a node byte for byte what it was, and no reader meets the skew; a project that names one has every launcher on an older CLI upgrade before it can launch again, because the CLI refuses a node carrying an unknown key (`refuseUnknownPolicyKeys`, `internal/cli/project_cmd.go:2836`) while a runner tolerates it and logs it (`internal/runner/projectpolicy.go:125`).** The key is operator-owned (`sharedForbidden`, `internal/cli/sharedcfg.go:209`), the publisher keeps what the node holds when the config is silent (the `verifier_accounts` keep rule), and `fugaro update` is the one command that publishes it — no second command, per the HARD RULE. The **image-catalog gate** is spelled apart from `catalogSince`/`catalogShipped` (F14): it is `imageCatalogSince`/`imageCatalogShipped` | **A `Version` bump** (`internal/projectpolicy/version.go:79`) is heavier than the key needs: `verifier_accounts` proved a top-level key rides a version without moving it, and the field rule makes an added key additive. **No skew note** is the failure the `verifier_accounts` entry exists to prevent: a teammate launches on an older CLI, the CLI refuses the node, and nobody knows why |
| **D1157** | Core or `ee/`? | **Core (MIT), the whole catalog.** It is image definition, build and selection — the engine's own job, beside `internal/image`, `internal/imagecheck` and `internal/infra`, none of which is `ee/`. No notice applies (the `ee/` tree is `ee/`, and the change-set/coordinator half of multi-repo (D1154) is that work's licence, not this one's) | **Moving the catalog to `ee/`** would put a project's own image definitions behind the paid tree for no behaviour that is `ee/`'s, and would split the image story across two licences |
| **D1158** | Which release carries the **build**? | **ANSWERED by the owner, 2026-11-02: the BUILD TASKS START IN 0.17.0** — the owner first put them in 0.16.0, then cut 0.16.0's scope (too big) and moved them to 0.17.0; the design itself is 0.16.0. PIC1–PIC7 are 0.17.0 tasks and none is launched before the 0.16.0 tag is cut. The catalog's `image_catalog` key and the shared-image operator steps (C8b/C8c, unscheduled) are **not** in this window unless the owner puts them there | **0.16.0** was the owner's first answer, withdrawn when 0.16.0's scope was cut: the cloud senior-reviewer chain is already security-sensitive and large, and four of the seven build tasks need independent review. **0.15.0 or earlier** is behind the design |
| **D1159** | (reserved) | — | — |

## 3. Choosing an image for a job (D1146)

The rule is stated for the general case — a job over a set of repositories — because the multi-repo
job (D1154) will need it; today the set has one member.

### 3.1 The algorithm

Input: the job's repositories `R`; for each `r ∈ R` either a **pin** — a hand-pinned image, or a
repository that names an entry with `image: <name>` (D1155) — or the tags it needs, from `r`'s
`needs:`, else the empty set when `r` names nothing; and the project's catalog entries `E`, each with
`tags(e)`, the marker `default(e)`, and possibly the tag `docker`.

0. **A pin is not the algorithm.** A repository that names an entry (`image: <name>`, D1155) is a
   **pin**: the named entry is used as it is, the choice below does not run for it, and if its tags do
   not cover the job's needed set Fugaro **warns**, naming the missing tags (D1146, answered
   2026-11-02 — the pin acts at deploy time and prints the warning). A hand-pinned image is the same.
   The steps below apply to the repositories that do **not** pin.
1. **The candidate set** `E_job` is `E`, minus every entry tagged `docker` when the job runs on Cloud
   Run (D1149, §3.2). A VM (Cloud Batch) job keeps the whole catalog.
2. **The needed set** `N = ⋃_{r ∈ R} tags(r)` over the repositories that do not pin. Duplicates
   collapse; the set is unordered.
3. **An empty needed set picks the default.** When `N = ∅`, every repository named nothing, and the
   spec's "1. Nothing: it uses the project default" is what that means: the entry marked
   `default(e)` is chosen and the tag counts are not compared. Without this step the entry with the
   **fewest tags** would win, which is not the spec's rule for a repository that asked for nothing.
4. **Covering candidates** `C = { e ∈ E_job : N ⊆ tags(e) }`. A candidate covers iff **every** needed
   tag is one of its tags; extra tags are allowed.
5. **Choose** `min` over `C` by, in order:
   1. **fewest tags** — `|tags(e)|` ascending. This is the spec's "the catalog image with the fewest
      tags that still covers them all": the smallest image that still works, so a job does not run a
      universal image when a narrow one covers it.
   2. **the default, when it qualifies** — on a tie in tag count, `default(e)` wins. The spec's
      "use the default when it qualifies".
   3. **the lexicographically first name** — otherwise `name` ascending, bytewise. This is the
      spec's "otherwise pick by name", and it makes the choice total: two runs of the same inputs
      pick the same entry, with no dependence on catalog order.
6. **No candidate** (`N ≠ ∅`, `C = ∅`, no pin) is a **refusal before the job starts** (§3.3).

Ties are only ever broken by steps 5.2 and 5.3, and both are total, so `min` is unique.

### 3.2 The `docker` tag

There is **one rule**, and it is D1149's (answered 2026-11-02): **on Cloud Run, an entry tagged
`docker` is left out of the candidate set (§3.1 step 1) unless `docker` is needed; if `docker` is
needed, Cloud Run refuses.** So a Cloud Run job whose repositories name no `docker` never sees a
docker-tagged entry at all — it cannot be chosen by accident — and a Cloud Run job that **does** need
`docker` has `C = ∅` and gets §3.3's second message, naming the backend. That is the spec's "fail
before the job starts", rather than a job that starts and dies on its first `docker` command. A VM
(Cloud Batch) job keeps the whole catalog: its container carries the host's Docker socket
(`internal/backend/batch/`), so a docker-tagged entry is an ordinary candidate there. The tag is a
**claim** the algorithm checks against the job's backend, not an image property.

### 3.3 The fail-before-start message

When no image covers the needed tags, the refusal is raised where the job spec is built (F7), before
any job starts, and reads — with the tags and the entries filled from the catalog:

```
no image in this project's catalog covers the tags this job needs: [postgres redis firebase-emulator]
the catalog's images cover: backend [node postgres redis], frontend [node], data [postgres redis]
add an entry that covers the missing tags (fugaro image add <name> --tags postgres redis firebase-emulator), or add a "universal" entry that covers every tag this project uses
```

Every entry there is a true near miss: none carries `firebase-emulator`, so none covers the needed
set and the refusal is real. An example whose `universal` entry held every needed tag would be a
message the algorithm could never print.

and when the missing capability is the backend (D1149):

```
this job's image is tagged docker, which needs a VM backend: Cloud Run gen-2 has no Docker daemon
run this job on the Batch/VM backend, or choose an image that does not carry the docker tag
```

Both name the missing tags (or the missing backend) and a next move, as the spec's §"Choosing an
image for a job" requires. Neither starts a job.

## 4. Promote (D1147), and the nightly triggers (D1148)

**Promote.** `fugaro image promote <repo> [name]` reads the repository's image definition — its
workflow `image:` block rendered through `internal/image/render.go:144`, or its `dockerfile:` path —
writes a catalog entry (name `[name]`, else the repository's name) into the published node (D1140),
copies the Dockerfile bytes into `builds/catalog/` (D1140), and points the repository at the entry
(`image: <name>`, D1155) in the same commit. It is never automatic: `fugaro update` does not convert
anything. It refuses a name already taken, and it refuses a repository whose workflows disagree on a
definition (two workflows with different `image:` blocks cannot become one entry).

**Nightly triggers.** The check (`internal/imagecheck/check.go`) gains one trigger and reuses three,
evaluated per catalog entry:

| Trigger | Fires when |
| --- | --- |
| `catalog-definition` (**new**) | the entry's definition changed: its `source` (a Dockerfile's blob, or an inline definition's hash), its `tags`, its `base`, or the engine's template salt (`internal/backend/gcp/build.go:81`'s `TemplateSalt`) |
| `base` (**exists**) | the entry's base image digest changed (`docs/design/v1.md` §7.2) |
| `lockfiles` (**exists**) | a **baked** repository's dependency files changed — the blobs `imagecheck.KeyFiles` already returns (`internal/imagecheck/record.go:237`) |
| `max-age` (**exists**) | the image is older than the workflow's `rebuild.max_age` (default 14 days until the published base images land, then 7 — `docs/design/v1.md` §7.2). This is the **scheduled security rebuild** D1148 answers (2026-11-03) |

`image-config` and `no-record` remain un-switchable-off, as today. The check keeps its back-off and
its paused-until-a-record rule, for catalog entries exactly as for workflows.

**What is scheduled** (the owner's answer, 2026-11-03): the catalog's rebuilds are **one
project-level scheduled check**, not one schedule per entry and not the per-repository check
extended — one Cloud Scheduler job and the target it invokes, at installation level, submitting one
catalog build per entry that fires, as the **same build account** the entry's own `fugaro image
build` runs as, over the **same BuildKit-secret path** (**D1150**), with the same smoke and the same
record. The **workflow** check keeps its own one-Job-per-repository scheduler, unchanged.

## 5. What a merger must re-check

1. **The D-block** ([README.md](README.md) §"Numbers this work holds"): re-read the open pull
   requests and the merged tree, and renumber to the first free block if D1140 has moved.
2. **The `:line` citations** under the convention at the top of this file.
3. **The reserved name `environments`** (`internal/config/layer.go:264`): if the layered-config
   Phase 2 work lands before this, the catalog's node key and its layer spelling are that work's to
   reconcile, and this folder's `image_catalog` is renamed to it rather than shipped beside it.
4. **F9's two secret paths** (`internal/credsvc`'s bundle and the workflow-secret BuildKit mounts):
   if either moves before the build tasks start, D1150 is re-read.
