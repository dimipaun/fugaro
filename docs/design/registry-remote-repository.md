
# Serving `fugaro-base` through an Artifact Registry remote repository: retiring the copy

Status: design for review (2026-10-14), from the owner's question — *"why are we still mirroring?"* — after [retire-image-mirror.md](retire-image-mirror.md) (#318, verify-then-copy) kept the copy and deferred exactly this (`§8`, Option B). **Docs and a plan only: no Go, Terraform or workflow file in this change.** The plan is [docs/plans/2026-10-14-registry-remote-repository.md](../plans/2026-10-14-registry-remote-repository.md); the owner-run check is [docs/gcp-live-checklist.md](../gcp-live-checklist.md) Check 43.

**The one-paragraph answer to the owner.** The copy exists because Cloud Run Jobs and Cloud Build must pull through somewhere the installation controls, and at M11 design time (`docs/design/m11-setup-and-skills.md` §3.3, §14) `ghcr.io` was believed unreadable from them and the packages were private. Both premises are gone: the packages are public (every release's `verify-public` proves anonymous pulls, §2.3), and Artifact Registry **remote repositories support `ghcr.io` as a Docker upstream** (§2.1) — a documented, first-class feature, not a guess. So the installation does not need to *own bytes* to control them: it can own a **pull-through proxy** and reference a **digest** through it. This design deletes the base image's copy step, and with it 1.2 GB pushed through the operator's laptop, a per-installation artifact that only ever held "whatever the tag resolved to when someone last copied", and the whole class of "which copy of the base is this repository on" questions. What replaces it is narrower and stronger: the CLI at `fugaro init --base`/`image refresh`/`upgrade` resolves the release's `-pinned` tag on ghcr.io, **verifies its cosign signature** (#318's Task 1/2 machinery), reads that digest back **through** the remote repository to prove the path works, and pins `base_images.base` to the remote-repository path **at that digest**. Every job from then on pulls a content-addressed digest, not a mutable tag.

## 1. What the owner asked, and what this document is

`docs/design/retire-image-mirror.md` §8 states the deferred option verbatim: *"whether Artifact Registry's remote-repository feature supports an arbitrary third-party upstream such as `ghcr.io` at all is UNVERIFIED and would need its own live check."* This document settles that from public documentation (§2), states which facts are still UNVERIFIED and which live check settles each (§2.12), designs the change (§3), compares the trust model with today's copy (§4), says exactly what is deleted, what stays and what Terraform adds (§5), covers the derived-image build (§6), the failure modes (§7), the owner's live check (§8), and how this relates to #318's tasks (§9).

**Corrections to #318's premise, stated up front, because they change its conclusion.** #318's plan says (its round-five note): *"the daily image check job is itself a Cloud Run Job running directly FROM the base image, and Cloud Run Jobs cannot deploy a bare `ghcr.io` reference (not verified live, assumed conservatively)"*. That assumption is **false as of the documentation read in §2.6**: Cloud Run today documents direct pulls of public GitHub Container Registry images, and recommends an Artifact Registry remote repository *specifically* for higher availability and for private GHCR images. The reason to use a remote repository is therefore no longer "Cloud Run cannot read ghcr.io" — it is §3.5's reason: **keeping exactly one, Artifact-Registry-shaped representation of "what base was this built from"**, which `internal/infra/tf/cover.go`, `submitRebuild`, `refreshVerdict` and `triggers()` all require.

## 2. Verified facts, each with its source

Everything in this section was read from public documentation on **2026-10-14**. Anything not verifiable this way is in §2.12 and marked **UNVERIFIED**, to be settled by Check 43.

### 2.1 Artifact Registry remote repositories support ghcr.io as a Docker upstream — VERIFIED

- *"Remote repositories store artifacts from the following upstream sources: Standard Artifact Registry repositories. External sources such as the following: CentOS, Debian, Docker Hub, **GitHub Container Registry (ghcr.io)**, Maven Central, The Python Package Index (PyPI)."* — [Create remote repositories](https://cloud.google.com/artifact-registry/docs/repositories/remote-repo), §intro.
- The Docker **preset** list holds exactly one entry — `https://registry-1.docker.io`, preset name `DOCKER-HUB` — so ghcr.io is not a preset: it is a **custom upstream**. Custom upstream URLs are supported for Docker, and the documentation's own "Custom URLs" table lists, verbatim: *"Docker | `https://ghcr.io` | GitHub Container Registry"* — [Remote repository overview](https://cloud.google.com/artifact-registry/docs/repositories/remote-overview), §"Custom URLs".
- Constraints that apply to a custom upstream: **HTTPS only** (*"Custom remote repository upstream URIs must use HTTPS. Unencrypted HTTP upstream URIs are not supported"*), **internet-accessible** (*"Remote repositories don't support on-premise or Virtual Private Cloud (VPC) network upstream sources without a public IP address"*), and the repository's *upstream source* is fixed at creation (*"you must configure the following settings, which can't be changed after the repository is created: … Remote repository upstream source"* — Create remote repositories).

### 2.2 The image path through a remote repository

- A remote repository is an ordinary Artifact Registry repository as far as an image reference goes. The documentation's own Docker example: `docker pull us-central1-docker.pkg.dev/PROJECT/quickstart-docker-hub-remote/busybox:latest` — *"us-central1-docker.pkg.dev is the hostname for the Docker repository you created. … quickstart-docker-hub-remote is the ID of the repository you created. busybox is the name of the image you want to pull from Docker Hub"* ([Create a Docker Hub remote repository](https://cloud.google.com/artifact-registry/docs/repositories/create-dockerhub-remote-repository)).
- So the shape is `<location>-docker.pkg.dev/<project>/<remote-repository-id>/<upstream path>[:tag|@digest]`. For Fugaro's upstream that means **`<region>-docker.pkg.dev/<project>/fugaro-ghcr/dimipaun/fugaro-base`** (§3.1). **UNVERIFIED as a whole** (§2.12 a): the documentation's only worked example is a preset upstream whose path happens to be a single segment; that the upstream path is appended verbatim is inferred from it, not stated.

### 2.3 Authentication to a public ghcr.io package — VERIFIED, and it requires nothing

- GitHub: *"You can … access public container images anonymously"* — [Working with the Container Registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry), §intro.
- Artifact Registry remote repositories take an *"Authenticated"* or *"Unauthenticated"* mode; credentials (a username plus a Secret Manager secret version) are optional and can be **added later** to an unauthenticated repository (*"You can add authentication to an unauthenticated repository after you create the repository, but you can't change an authenticated repository to an unauthenticated repository after you create it"* — Create remote repositories). Fugaro's packages are public (§1), so the repository is created **unauthenticated** — no secret, no rotation, nothing in the trust chain.
- *"If you want to authenticate to an upstream repository, then enable the Secret Manager API"* (Create remote repositories) — not needed for us, so `fugaro init` gains no new API and no new secret.
- The upstream is **validated at creation** unless told otherwise: the Terraform provider's `disable_upstream_validation` — *"If true, the remote repository upstream and upstream credentials will not be validated"* ([`google_artifact_registry_repository`](https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/artifact_registry_repository)). The design leaves validation **on** (the provider's default), so a typo'd upstream URI fails the apply rather than a job's first pull.

### 2.4 How tags and digests are resolved and cached — VERIFIED

- *"The first time that you request a version of a package, Artifact Registry downloads and caches the package in the remote repository. The next time you request the same package version, Artifact Registry serves the cached copy. If you request an artifact from an upstream source that doesn't exist, or doesn't contain the version you specified, then the request will fail."* — Remote repository overview, §"How remote repositories work".
- *"Cached artifacts are also still available if the external public repository is offline due to an outage or other issue."* — same page, §"Use cases and benefits". This is the answer to *"what happens if ghcr.io goes down after the first pull"* (§7).
- Mutable metadata is refreshed after a per-format age; for **Docker the row is *List/Get tags cache*, 1 hour** (*"Mutable files such as package indexes and metadata are updated from the upstream source when they become more than the default age"* — same page, §"Updates to package indexes and metadata"). **UNVERIFIED**: whether that 1-hour figure also governs a *manifest fetched by tag* (as opposed to a tag listing), i.e. how long a re-pushed tag stays invisible (§2.12 f).
- The wire protocol is one identifier, not two: *"`<tag-or-digest>` MUST be either (a) the digest of the manifest or (b) a tag. The `<tag-or-digest>` MUST NOT be in any other format."* — [OCI distribution specification](https://github.com/opencontainers/distribution-spec/blob/main/spec.md), §"Pulling manifests". A *client-side* reference may carry both — `referencePat = anchored(capture(namePat), optional(':', capture(tag)), optional('@', capture(digestPat)))` — [distribution/reference](https://github.com/distribution/reference/blob/main/regexp.go) `regexp.go`. That is why §3.1's pinned reference is a client-side string whose digest the client resolves before the request.

### 2.5 How a new tag or digest on ghcr becomes visible

- Through the remote repository, nothing is fetched until it is *requested* (§2.4), and a **digest** request is content-addressed: the only question is whether Artifact Registry will fetch an unknown digest directly. **UNVERIFIED** (§2.12 b, c). This design does not depend on it for correctness in the sense that matters — see the next bullet — but it does depend on it for the *first* pull, which is why Check 43b tests it against a fresh repository.
- **The tag is never resolved through the remote repository.** The CLI resolves `<ver>-pinned` at `ghcr.io` directly, exactly as `internal/mirror` already does for a tag (`mirror.go:165`, anonymous, digest-verified against the `Docker-Content-Digest` header, `mirror.go:688-712`), and the jobs reference a digest. So Artifact Registry's tag cache — 1 hour or otherwise — can never make Fugaro pin, or a job run, the wrong bytes. It can only make a *by-tag* request stale, and this design makes none.

### 2.6 Cloud Run Jobs and Cloud Build can pull through it — VERIFIED

- *"You can directly use container images stored in Artifact Registry, or public images from Docker Hub or GitHub Container Registry. Google recommends the use of Artifact Registry. Public images from GitHub Container Registry and Docker Hub images are cached for up to one hour. You can use container images from other public or private registries (like JFrog Artifactory or Nexus), or private images from GitHub Container Registry, by setting up an Artifact Registry remote repository."* and *"For higher availability, Google recommends deploying these Docker Hub or GitHub Container Registry images using an Artifact Registry remote repository."* — [Deploying container images](https://cloud.google.com/run/docs/deploying), §"Supported container registries and images"; the identical text appears in [Create jobs](https://cloud.google.com/run/docs/create-jobs).
- *"To deploy public or private container images that are not stored in Artifact Registry or Docker Hub, or are not public images in GitHub Container Registry, set up an Artifact Registry remote repository. Artifact Registry remote repositories allow you to: Deploy any public container image. …"* — Deploying container images, §"Deploy images from other registries".
- A remote-repository path is an Artifact Registry path, so it needs nothing new from Cloud Run or Cloud Build: Cloud Build already pulls step images and `FROM` images from Artifact Registry as the build account today, and the owner's 2026-10-12 live test (project `fugaro-dev`, build `c503576c-51d9-4f96-b9e7-debae7d0df39`) already proved `docker pull ghcr.io/dimipaun/fugaro-go:0.6.5` succeeds in Cloud Build in 21 s as the default compute service account (`docs/design/retire-image-mirror.md`, header) — i.e. the pull path this design routes through a proxy is not in question; what the proxy adds is the controlled path and the digest pin.
- Two constraints that touch this design: *"Cloud Run does not support container image layers larger than 9.9 GB when deploying from Docker Hub or an Artifact Registry remote repository with an external registry"* (Deploying container images) — the base image is about 3.8 GB in total and far under that per layer, but it is a ceiling worth knowing; and *"When you create a new job, the Cloud Run service agent needs to be able to access the container, which is the case by default"* (Create jobs) — the runtime pull is by the Cloud Run service agent, in-project here.
- Images may be named *"with a tag … or with an exact digest"* (Deploying container images) — which is what a pinned reference needs. Whether Cloud Run and Cloud Build accept the **combined** `:tag@digest` reference form (§3.1) is **UNVERIFIED** (§2.12 d).

### 2.7 IAM

- Creating a remote repository needs `roles/artifactregistry.admin` on the project (`artifactregistry.repositories.create`; *"Granting the artifactregistry.repositories.create permission to end users through the roles/artifactregistry.admin role is highly privileged"* — Create remote repositories, §"Required roles"), which the operator running `fugaro init` already has.
- Pulling needs *"Artifact Registry Reader (`roles/artifactregistry.reader`) on the Artifact Registry repository of the deployed container image"* (Deploying container images, §"Required roles") — grantable per repository ([Access control with IAM](https://cloud.google.com/artifact-registry/docs/access-control)). Cloud Build's pulls run as the repository's build account, so it needs that role **on the new repository**; see §5.3.

### 2.8 Cost — VERIFIED

- Remote repositories are billed like standard ones: *"The following types of repositories store artifacts: Standard repositories. Remote repositories. …"*, storage at *"$0.00 (Free) / 1 gibibyte hour, per 1 month / account"* for the first 0.5 GiB-month and *"$0.000136986 / 1 gibibyte hour"* above it, per billing account ([Artifact Registry pricing](https://cloud.google.com/artifact-registry/pricing)).
- *"Data transfer into Google Cloud is free."* — same page. The ghcr.io → Artifact Registry leg is therefore not billed. Delivery to a client in the same location is *"$0.00 (Free) / 1 gibibyte"* (same page), which is the case for Cloud Run Jobs, Cloud Build and the operator's own machine in the installation's region. Egress to the internet is Premium-tier priced (same page).
- Net effect for an installation (§3, §4): roughly the same registry storage as today's copy (only what the jobs actually pull is cached — the linux/amd64 layers), **minus** about 1.2 GB uploaded from the operator's machine per copy (`docs/gcp-setup.md` §3: *"About 1.2 GB is copied through your machine"*), and minus the copy's own storage when nothing pulls it.

### 2.9 Regions — VERIFIED

- A repository's location is *"regional or multi-regional"*; a remote repository's location is one of the settings *"which can't be changed after the repository is created"* (Create remote repositories), and `gcloud artifacts locations list` names the supported ones ([Repository locations](https://cloud.google.com/artifact-registry/docs/repositories/repo-locations), where `us-east5` — the installation's own region in `fugaro-dev` — is listed). Whether remote repositories are available in *every* listed location is **UNVERIFIED** (§2.12 k); Check 43a creates one in the installation's own region.

### 2.10 Limits — VERIFIED

- *"Public upstream host reads per organization, per region, per minute | Default | 600"* ([Artifact Registry quotas and limits](https://cloud.google.com/artifact-registry/quotas), §"Remote Repository Limits"); the same page: *"Requests to upstream hosts count against your organization's upstream host read request limit"*, *"Once the artifact has been cached to your remote, requests for that artifact only count against your project's request quota"*, and *"you might be subject to the upstream repositories' read and write quotas"*. Max *"data retrieval from upstream of remote repository per request | 9.9 GB"*.
- Error shapes to expect, from [Troubleshoot remote repositories](https://cloud.google.com/artifact-registry/docs/troubleshoot-remote): **404** *"when the requested artifact doesn't exist in the upstream repository"*; **429** *"when the upstream read limit is exceeded"*; **502** *"when the upstream repository responds with an error code"*; **504** *"when the upstream repository is offline or cannot be contacted"*.
- GitHub's own anonymous GHCR pull limits: **no number found** in the pages read (§2.12 l).

### 2.11 VPC Service Controls — VERIFIED, and it is a real constraint

- *"If Artifact Registry is in a VPC Service Controls service perimeter, Artifact Registry denies access to upstream sources outside the perimeter by default. To allow remote repositories in a specific location to access their configured external sources outside the perimeter, see the instructions for VPC Service Controls configuration."* (Remote repository overview.) The per-location switch is `gcloud artifacts vpcsc-config allow --project PROJECT_ID --location LOCATION` (or `deny`), read with `gcloud artifacts vpcsc-config describe`, and needs `roles/artifactregistry.admin` (Create remote repositories, §"Access to upstream sources in a service perimeter"). Denial blocks only upstream *fetches*: *"existing cached artifacts in remote repositories are still available."*
- This is the one case where this design asks more of an operator than today's copy does, and the documented switch is a single, idempotent command. Projects that cannot open that door keep `--base-image` (bring-your-own), unchanged (§5.2).

### 2.12 What could not be verified here — the live check's own list

| # | UNVERIFIED claim | Settled by |
|---|---|---|
| a | The path an image takes through a remote repository with a **custom** upstream is `<location>-docker.pkg.dev/<project>/<repo>/<upstream path>` (i.e. `…/fugaro-ghcr/dimipaun/fugaro-base`) | Check 43a (a pull, then `gcloud artifacts docker images list`) |
| b | A manifest can be fetched through the remote repository **by digest** when nothing was fetched before (no prior tag request) | Check 43b |
| c | The digest served through the remote repository for that path is **the same digest** as ghcr.io's (content-addressed, unmodified bytes) | Check 43b |
| d | Cloud Run Jobs, Cloud Build and `docker pull` accept the combined `:tag@digest` reference (§3.1) | Check 43c, 43d |
| e | Pushing into a remote repository is refused (the documentation never describes one) | Check 43a (an attempted `docker push` may be skipped; if attempted, it is a sandbox-only, clearly-labelled write) |
| f | A **re-pushed** upstream tag stays invisible for the tag-cache age, and a freshly published digest is fetchable immediately | Check 43e |
| g | Artifact Registry validates an upstream manifest's digest against the requested one on ingest | Check 43b (digest comparison) |
| h | A Cloud Run **Job** re-resolves a tag at each execution (which would make today's tag-shaped reference mutable between executions — the digest pin removes that question) | Check 43c |
| i | Whether Artifact Registry **retains** cached artifacts that no cleanup policy keeps, and how a cleanup policy on the remote repository would interact with a pinned digest | §7; no cleanup policy is configured (§5.3) |
| j | The Terraform provider at the pinned version (`= 8.4.0`) accepts `mode = "REMOTE_REPOSITORY"` with `docker_repository { custom_repository { uri = … } }` | Task 1's `terraform validate` (`internal/infra/generated_root_test.go`) |
| k | Remote repositories are available in `us-east5` specifically | Check 43a |
| l | ghcr.io's own anonymous pull rate limits (no published figure found) | Check 43d (a burst of pulls is out of scope; see §7) |

## 3. The design

### 3.1 One new installation singleton, one new shape for `base_images.base`

**Terraform adds one `google_artifact_registry_repository` per installation** (i.e. per project — an installation is a project in Fugaro's model, and the installation root already owns `fugaro-base`), in `deploy/terraform/gcp/modules/installation/registry.tf`:

```hcl
resource "google_artifact_registry_repository" "ghcr" {
  project       = var.project
  location      = var.region
  repository_id = var.names.ghcr_registry          # default "fugaro-ghcr" (§5.3)
  description   = "Public Fugaro images from ghcr.io, cached for this installation's builds and jobs"
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"
  labels        = { fugaro = "managed" }
  remote_repository_config {
    description = "ghcr.io"
    docker_repository {
      custom_repository { uri = "https://ghcr.io" }
    }
  }
  # No upstream_credentials: the packages are public (§2.3).
  # No cleanup_policies: the cache is the point (§7).
  lifecycle { prevent_destroy = true }             # required of every registry (§5.3)
}
```

The name must match `internal/infra/tf/cover.go`'s `repoID = ^fugaro(-[a-z0-9-]+)?$` (cover.go:154), which every `*_iam_member` on a registry is checked against (cover.go:628-632) — `fugaro-ghcr` does.

**The reference `base_images.base` holds** — and therefore what the check job's container image, `FUGARO_CHECK_SPEC`'s `base_images` map and Cloud Build's `FUGARO_BASE` substitution all carry — is:

```
<region>-docker.pkg.dev/<project>/fugaro-ghcr/dimipaun/fugaro-base:<ver>@sha256:<index-digest>
```

**Why the tag stays even though the digest pins.** The tag is the version marker, and three existing, load-bearing parsers read it:

- `managedVersion(ref, host, kind)` (`internal/cli/init_images.go:137-140`) cuts `"<host>/<repo>/fugaro-<kind>:"` and requires a release version — it answers *"is this entry a release base an earlier run pinned, or a custom/hand-pushed base this CLI must never touch?"*, which is also `isManaged`/`curVersion` and `refreshBase`'s refusal (`image_refresh_rules.go:26-46`).
- `baseRefReleaseRE` (`internal/cli/init_fugaroyaml.go:369-375`), `\A[^\s@]+/fugaro-(base):v?([0-9]+\.[0-9]+\.[0-9]+)(?:@sha256:[0-9a-f]{64})?\z`, drives `imagePredates`/`recipes_skew.go` and `config_publish.go`'s layered-config gate. **It already accepts a digest suffix**, and it does not care which repository the path goes through — a happy coincidence of its `[^\s@]+` prefix, and the reason a digest-pinned release base was already anticipated here.
- `refreshVerdict`'s never-downgrade rule (`image_refresh_rules.go:51-67`) uses `managedVersion`'s version to keep a *newer* pin when an older CLI refreshes. A bare digest carries no order, and a CLI cannot resolve a digest back to a release (only ghcr.io's `-pinned` tags map version → digest, one-way). So the version must remain readable, or "never downgrade" becomes unanswerable — and the fallback (a new `base_versions` map in the local config plus its shared-config validation) is strictly more surface for the same information.

The digest, not the tag, is what the pull resolves (§2.4): the client asks the registry for the digest, so a moved tag cannot change what a job runs. §2.12 d is the one open question — if any of the three consumers rejects the combined form, the fallback is the digest-only reference plus a recorded version, and it is the owner's call (§10).

### 3.2 Where the digest and the signature are verified: the CLI, before it pins

The verification stays exactly where #318 put it — the **operator's machine**, at the one moment Fugaro writes the value that everything else reads — and the copy it used to precede is what disappears:

1. `fugaro init --base base` / `fugaro image refresh` (its base step, `internal/cli/image_refresh_steps.go:42-60`, routing through `internal/cli/init_images.go`'s `imagesStage`) / `fugaro upgrade` (whose cloud step *is* `fugaro image refresh`, `upgrade_cloud.go:433-489`) resolves `ghcr.io/dimipaun/fugaro-base:<releaseVersion()>-pinned` — the nightly-immune tag #318's Task 1 publishes and Task 2's `basepin.Resolve` reads — and gets the **index digest** (`mirror.ResolveDigest`, #318 Task 2's one new export, reusing `mirror.go:688-712`; no returning to the amd64 child, because nothing is being copied any more).
2. It verifies that specific digest's OCI-attached cosign signature against the `images.yml`-on-a-tag-push identity (`basepin.OCIVerifier`, #318 Task 2). No signature, or a wrong identity, refuses — nothing is pinned, nothing is written.
3. **It reads that digest back through the new remote repository** — `.../fugaro-ghcr/dimipaun/fugaro-base@sha256:<digest>`, a manifest read with the operator's own Google credentials (the same authenticated client `internal/mirror` already uses for its destination side, `mirror.go:743-773`) — and refuses unless the registry answers with that exact digest. This one cheap request (no layers) does three things at once: it proves the upstream path shape (§2.12 a), it proves a cold digest fetch through the remote repository works (§2.12 b), and it proves the digest survives the hop unchanged (§2.12 c). **It is this design's equivalent of #318's "verify before copy": verification moved from the bytes being copied to the path that will serve them.**
4. It writes `base_images.base` = the pinned reference (§3.1) into the local config (`init_images.go`'s `record()`, `:487-534`, which already re-reads the config, prints `base_images.<kind> = … (digest …)` and publishes the shared config) — and copies **nothing**.

Everything downstream is unchanged in structure: `fugaro image refresh`'s own later steps read the local config's value to move the check job and the clone jobs (`refreshCheckJob`, `refreshCloneJobs`) and to decide which workflows `refreshBuilds` rebuilds; `fugaro init --repo` renders it into the job, the spec and the build substitution.

### 3.3 What the job then references, and why nothing else had to move

- The check job's container image: `CheckSpec.Image = bases[kind]` (`internal/infra/spec.go:903-905`) → the pinned remote reference.
- `FUGARO_CHECK_SPEC`'s `base_images` (`spec.go:888-891`) → the same string, per kind.
- Cloud Build's `_FUGARO_BASE` substitution (`internal/backend/gcp/build.go:183`) → the same string, used as the derived build's step image and as `--build-arg FUGARO_BASE` (`images/derived/cloudbuild.yaml:75,104,144,159,210,237`).
- Clone workflows' job images (`spec.go:607-609`, `refreshCloneJobs`) → the same string.

**There is still exactly one representation of "what base was this built from", and it is Artifact-Registry-shaped**, which is why the fifth review's blocker in #318 does not reappear here (that review's two-representation problem was created by a ghcr.io-shaped value). Concretely, with no change at all:

- `internal/infra/tf/cover.go`'s `inRegistry` (`cover.go:745-781`) requires every `FUGARO_CHECK_SPEC` base image and every job's `image` to sit under `<registry>/<project>/` — the remote repository is in that project, so it passes.
- `submitRebuild` (`internal/cli/imagecheck.go:695-700`, `s.BaseImages[kind]`), `refreshVerdict` (`image_refresh_rules.go:55`, `rec.BaseRef == want`) and `triggers()` (`internal/imagecheck/triggers.go:34`, `in.BaseRef != rec.BaseRef`) keep comparing like with like.
- The `refChars` guard the spec's refs pass through (`cover.go:727`, `^[A-Za-z0-9._:/@-]+$`) already allows `@` and `:`.

What *does* need updating is every place that recognizes the shape of a **release** base path (the two allowed repository IDs, the new path's extra segment, the digest suffix). That is a bounded, enumerated list, and it is `§5.2` and the plan's Tasks 2–4 — not a new representation, just a second spelling of the same one.

### 3.4 What the check job's "copy dependency" becomes

Today the check job's image is the tag-shaped copy in `fugaro-base` (`docs/design/retire-image-mirror.md` §1: *"its container's `image = var.repo.check.image`"*). After this design it is the pinned remote reference; `RewriteCheckSpec`/`PlanCheckJob` (`internal/infra/checkjob.go:66-125,187`) move both the container image and the spec's map together, exactly as they do now — only the string changes. One consequence worth naming: a **digest-pinned** check job cannot be silently re-pointed by moving a tag, which today's tag-shaped copy can (`§4`).

### 3.5 Why a remote repository rather than pointing the jobs straight at ghcr.io

Cloud Run now documents direct public-GHCR pulls (§2.6), so "just use `ghcr.io/dimipaun/fugaro-base@sha256:…` everywhere" is a real alternative, and it is *rejected* here for reasons that are in the code, not in taste:

- `internal/infra/tf/cover.go`'s `inRegistry` would refuse it: a base image or a check job's image outside `<registry>/<project>/` fails the covered plan (`cover.go:650-651`, `:778-780`), so `fugaro init` could not create the resources this design would need.
- It would reintroduce #318's two-representation problem for `submitRebuild`/`refreshVerdict`/`triggers()` (exact string equality on `base_ref`), and a ghcr-shaped value cannot be compared against an Artifact-Registry-shaped one.
- Cloud Run's own direct-GHCR caching is *"up to one hour"* (§2.6) — an upstream outage with no warm cache would fail a job's pull where a remote repository's cache survives (§2.4).
- A remote repository gives the installation one place to see, audit and (later) credential the upstream, and one project-local path for every consumer, which is what a *per-installation* product wants.

The alternative is recorded, not hidden: if `cover.go`'s registry-confinement rule is ever relaxed, this decision should be revisited.

## 4. The trust model, compared with today's copy

| | Today (the copy, with #318's verification) | This design |
|---|---|---|
| What the job pulls | `<region>-docker.pkg.dev/<project>/fugaro-base/fugaro-base:<ver>` — a **tag** in the installation's own standard repository | `<region>-docker.pkg.dev/<project>/fugaro-ghcr/dimipaun/fugaro-base:<ver>@sha256:<digest>` — a **digest** through a pull-through proxy |
| Who can change what the job runs, without touching a job | anyone who can write `fugaro-base` (operators: `base_writer`, `installation/iam.tf:137-145`) — they can move that tag | nobody: the reference is a digest, and the cache cannot serve different bytes for it (§2.12 c,g) |
| What is verified, when, where | the digest *and* a cosign signature, at copy time, on the operator's machine (#318) | the same digest and the same signature, at pin time, on the operator's machine (§3.2) — plus one read back **through** the new path, which today's copy has no equivalent of |
| What the operator's machine must reach | `ghcr.io` (1.2 GB down) and sigstore's trust root; then 1.2 GB up to Artifact Registry | `ghcr.io` (a manifest, a few KB), sigstore's trust root, and the installation's registry (a manifest) |
| Bytes in the installation's registry | a full copy, ~1.2 GB, written by the operator | only what the jobs actually pull (the linux/amd64 layers), cached on first use |
| Where an attacker who controls a ghcr.io tag gets to | the *next* copy — the same boundary #318's signature check draws | the same boundary: the resolve step still reads a tag, and the signature check refuses a moved or unsigned digest. Nothing else changed |
| New dependency | — | the remote repository's availability on a **cold** pull (rate limits, §2.10; upstream outage, §7) — today's copy is warm by construction |
| New operator step | — | none for a plain project; a VPC-SC project needs one documented `gcloud artifacts vpcsc-config allow` per location (§2.11) |

**Stated plainly: this is a net strengthening, with one accepted new availability dependency.** The strengthening is digest pinning (a tag in a writable registry is no longer the thing jobs run) and the removal of the copy as a mutable artifact; the cost is that the *first* pull of a new digest happens in the job's environment rather than on the operator's machine — mitigated by §3.2's verification read, which warms the manifest (not the layers) and fails the refresh, loudly, if the path cannot serve the digest.

## 5. Exactly what is deleted, what stays, and what Terraform adds

### 5.1 Deleted

- **The base image's copy step**: the base-kind branch of `internal/cli/init_images.go`'s `imagesStage` — its `items()`/`read()`/`Apply()` path for base kinds, which today builds a `mirror.Ref` source at `ghcr.io/dimipaun/fugaro-base:<ver>` (`init_images.go:191-202, 214-227`), calls `mirror.Plan(ctx, it.src, it.dst, expect[it.key()])` (`:288`) and `m.Copy` (`:425`), and records the **destination tag** in `base_images` (`:522`). For base kinds this stage becomes resolve-verify-pin (§3.2) and copies nothing. `internal/mirror`'s copy engine (`Plan`/`Copy`) is **not** deleted: history still uses it (§5.2).
- **`mirror.Dest`'s base-kind call site** (`init_images.go:196`, `image_refresh_rules.go:41-45`) — the destination is no longer a copy into `fugaro-base`.
- **`--expect-digest base=…` as a *destination* pin, and `--replace-image base`.** With nothing written there is nothing to replace, and the pin is produced by verification rather than by the operator. `--expect-digest base=sha256:<hex>` keeps a narrower meaning — *"refuse unless the verified digest is this one"* — and `--replace-image` keeps `history` only (a `base` argument is refused, naming the removal). `--base` keeps its meaning ("this kind's base") and `--base-image` is untouched (§5.2).
- **The confirmation text's copy claim for base kinds**: `init_images.go:413-414`'s *"copies %d image(s), %.1f MB … The digests above are what the release tags resolve to now, which is what the registry says, not a signature. NOT pinned with --expect-digest: %s"*, and `sourceLine()`'s parallel wording (`:569-583`), both of which stop being true for base the moment §3.2 lands (the base's digest **is** signature-verified, and it **is** pinned). This is #318's Task 4, superseded and rewritten rather than kept.
- **The copy-era text in the user docs**, including one premise this design disproves:
  - `docs/gcp-setup.md` §3's *"`fugaro init` copies them for you … About 1.2 GB is copied through your machine"*, `docs/base-image.md` `:3`/`:5`, `docs/base-image-migration.md` `:15` (*"It copies `fugaro-base` into your registry"* — the step's effect on the config is unchanged; its mechanism is not), `README.md` `:91`, and the `docs/gcp-setup.md` §"What it costs" table's per-GB storage line for base images.
  - **`docs/release.md:30`'s *"Cloud Build and Cloud Run in a user's project cannot read ghcr.io, and the project's base registry `fugaro-base` is writable only by people, never by a build account. So the `images` stage of `fugaro init` copies the images …"*** — the first clause is false as of §2.6, and this repository already holds the counter-evidence (the owner's 2026-10-12 Cloud Build pull, recorded in the status line of `docs/design/retire-image-mirror.md`). The surviving justification for a project-local path is the one in §3.5: one representation, one controlled path, and a digest pin. Its `:32`'s *"signing the images and verifying before the copy is a planned follow-up"* becomes "the digest is verified and pinned before anything references it".
- **#318's Task 3 and Task 4** as written (verify-then-copy, and the "still copies" confirmation text) — see §9.

### 5.2 Stays, unchanged

- **The `fugaro-base` standard repository**, and with it: the **history image's copy** (`fugaro-history` → `fugaro-base/history:latest`, `internal/mirror` used exactly as today; explicitly out of scope in #318 too), **`--base-image`** (bring-your-own, written by `init.go:1464-1479`), and the operator's `base_writer`/dev-tag pushes (`docs/gcp-setup.md` §3's `build-base.sh` flow). This is why `fugaro-base` cannot simply *become* the remote repository: it is written to.
- **The build account's reader grant on `fugaro-base`** (`deploy/terraform/gcp/modules/repo/build.tf:44-50`, `build_base`). #318's premise that the copy's build-account grants are deleted does not survive contact with `--base-image`: a custom base is pushed to `fugaro-base`, so a repository building from one still needs that read. It stays, and a **new** reader grant on the remote repository is added beside it (§5.3). A future design that moves custom bases elsewhere could drop it; this one does not.
- Everything else in #318's "stays" list: `--expect-digest` (narrowed, above), `managedVersion`/`isManaged` (extended, Task 2), `internal/mirror`'s engine, `verify-public`, the base image's own content, and `internal/infra/tf/cover.go`'s rules.
- `internal/cli/image_refresh_steps.go`'s step structure (base → check job → builds) and `RewriteCheckSpec`/`PlanCheckJob`'s mechanics.

### 5.3 Terraform adds

| File | Addition |
|---|---|
| `deploy/terraform/gcp/modules/installation/registry.tf` | `google_artifact_registry_repository.ghcr` (§3.1) — `mode = "REMOTE_REPOSITORY"`, custom upstream `https://ghcr.io`, no credentials, no cleanup policies, `prevent_destroy = true` |
| `deploy/terraform/gcp/modules/installation/variables.tf` | `names.ghcr_registry` (validated with the existing registry-ID condition, `variables.tf:75-78`) |
| `deploy/terraform/gcp/modules/installation/iam.tf` | `google_artifact_registry_repository_iam_member.ghcr_reader` to `var.operators` (`roles/artifactregistry.reader`) — §3.2's verification read is done by the operator's own credentials |
| `deploy/terraform/gcp/modules/repo/build.tf` | `google_artifact_registry_repository_iam_member.build_ghcr` (`roles/artifactregistry.reader`, `repository = var.installation.ghcr_registry`) for the build account, whose Cloud Build pulls the derived build's step image and `FROM` through it |
| `deploy/terraform/gcp/modules/installation/outputs.tf`, `roots/installation/outputs.tf`, `roots/repo/variables.tf` | `ghcr_registry` output, threaded like `base_registry` |
| `internal/infra/tfvars.go`, `internal/infra/spec.go` | `GhcrRegistry = "fugaro-ghcr"` beside `BaseRegistry`/`LegacyRegistry` (`tfvars.go:21-22`), in `Names` and the installation spec |

No new **API** is needed: `artifactregistry.googleapis.com` is already in the installation module's list (`modules/installation/apis.tf:10`). No new **import kind** is needed either: an existing installation adopts the resource the ordinary way — it is *absent* from state, so the first `fugaro init` after the upgrade plans a **create**, and `DiscoverInstallation` (`internal/infra/discover.go:401-437`) only looks for the known names, so the new one is never mislabelled "foreign". (If the owner later wants adoption into a state that a hand-run `terraform import` created, that is one new `importTable` entry, `internal/infra/imports.go:83-105`, and `TestGeneratedRootValidates` requires the matching block — which §5.4's resource is.)

Three repository-specific tests constrain the shape and are extended by the plan, not worked around: `deploy/terraform/terraform_test.go`'s `TestPreventDestroy` (`:214`; every `google_artifact_registry_repository` carries `prevent_destroy = true`), `TestMirrorRunsAsUserNotBuildAccount` (`:159`; the only non-reader grant on `fugaro-base` is the operators' `base_writer`) and `TestTagMoverRoleIsTagsDeleteOnly` (`:114`; the tag mover is granted only on a repository's own registry). **One implementation trap worth naming:** `TestMirrorRunsAsUserNotBuildAccount` decides a grant is "on the base registry" by a *substring* match on `base_registry` (or `repository.base.` or `"fugaro-base"`, `:173`), so a new grant block must not name `var.installation.base_registry` for any other reason — the new grants name `ghcr_registry`, which matches none of the three. `internal/infra/tf/cover.go`'s `TestGrantRulesAreTheModules` keeps the new grants inside the `reader` allowlist row (`cover.go:93-97`, `internal/infra/tf/cover_grants_test.go:163`).

### 5.4 Migration: no backward compatibility, and the rule that makes it safe

**Nothing dual-shaped, no transition release, no `*Since` gate.** Fugaro is pre-release and exactly **two installations exist** — the owner's `fugaro` (project `fugaro-dev`, sandbox repository `edgeappinc/fugarosandbox`) and `belong` (EdgeWeb's and EdgeServer's) — which is the same ground `docs/design/project-secrets.md` §6 rules on (*"no backward compatibility needed before release"*) and `docs/design/m9-budget-and-dashboard.md` D18 before it. **Migrating an installation is: re-run `fugaro init` (installing the new remote repository and its grants), then `fugaro init --base base` / `fugaro image refresh` in each repository** — which rewrites `base_images.base` to the pinned remote reference. **No live step of this design's plan touches `belong` without the owner's explicit, separate word.**

What the migration costs, stated plainly for the two installations:

- The first `init` per project **creates** one repository resource and two IAM members — a normal plan, no replaces, no destroys, no `--allow-delete`.
- Rewriting `base_images.base` changes `base_ref`, so **every workflow's build record no longer matches**: `image refresh`'s build step rebuilds each workflow once (each build billable, `docs/gcp-setup.md` §3's ~$0.21 for a 13-minute `E2_HIGHCPU_8` build), and `triggers()` would also fire a rebuild in the daily check's own view. That is the one visible cost of the switch, and it is bounded by the number of workflows.
- The old copies in `fugaro-base` are **not** deleted by this change; the repository's existing cleanup policies (`registry.tf:24-73`: `keep-tagged` for `dev-`/`latest`, `keep-recent`, `delete-untagged`) prune them as they would any other version, and an operator may delete them by hand.
- A repository with a **custom** base (`--base-image`, a `dev-<sha>` tag) is untouched: `isManaged` still says "not a release base", and `customBaseRefusal` (`image_refresh_rules.go:104-110`) still refuses to replace it — now also for the reason that the CLI has nothing to pin over it.
- **One ordering trap, because Check 43a tells the owner to create the repository by hand before the implementation exists.** If that feasibility run leaves `fugaro-ghcr` in `fugaro-dev`, the first `fugaro init` after the implementation plans a **create** for a repository that already exists and the apply fails with `already exists`. Discovery does not adopt it: `DiscoverInstallation` looks up the names it knows one at a time (`discover.go:367-377`, `:423-431`) and a repository that is *not* an import kind is never looked for, so there is nothing to make it an import. The check's own restore deletes it (Check 43's "Restore"), and if it was left in place the fix is a `terraform import module.installation.google_artifact_registry_repository.ghcr projects/<project>/locations/<region>/repositories/fugaro-ghcr` or a delete before the first `init`. This is exactly the shape §10.3's "no explicit deletion" does *not* cover, and it is why the check's restore is written as a ⚠ CONFIRM step rather than optional.

## 6. The derived-image build

Images build **FROM the base**, through the same one value. `_FUGARO_BASE` (`internal/backend/gcp/build.go:183`) is `base_images.base`, and `images/derived/cloudbuild.yaml` uses it as the image of its own `fugaro`-running steps (`:75`, `:104`, `:210`, `:237`), as `FUGARO_BASE` in the environment (`:131`, `:239`), for `docker image inspect … RepoDigests` (`:144`) and as `--build-arg FUGARO_BASE=$$base` (`:159`).

So a derived build pulls the pinned remote reference **twice**: once as the step image and once as the `FROM` — both as the build account, through the remote repository, from the same cached manifest and the same cached layers, once warm.

**The template already expects exactly this shape, which is why nothing in it changes.** Its own comments say so: *"Render and build use one base image. Cloud Build pulls `_FUGARO_BASE` once … so a republished tag between the steps cannot give them different bases"* (`images/derived/cloudbuild.yaml:20-24`), and its `_FUGARO_BASE` substitution is documented as *"the base image; build pins the render step's image by digest"* (`:53`). What makes that true today is a `docker image inspect … RepoDigests` read that the render step writes to `/workspace/base-digest` and the build step turns into `--build-arg FUGARO_BASE=$$base` (`:144-159`) and a `dev.fugaro.base.digest` label (`:166`). With a digest-pinned `_FUGARO_BASE`, the same lines produce the same answer for a stronger reason: the value is a digest before Docker ever sees it, so a moved tag cannot change what the render step, the build step or the label says. The label's digest is then the one the remote repository served, which §3.2's read-back already proved equal to ghcr.io's.

Further consequences:

- The build account needs `roles/artifactregistry.reader` on the **remote** repository (new, §5.3) in addition to `fugaro-base`'s reader grant, which stays for custom bases.
- Both pulls now resolve a **digest** where they used to resolve a tag in the project's own registry: an unchanged pinned base no longer means "whatever `fugaro-base:0.7.1` says today". `fugaro image build --repo … --workflow …` keeps working unchanged (`internal/cli/image.go:200-215`), because it reads the same config value.
- **A local build** (`fugaro image build --local`, `internal/image/local.go:212`) pulls the pinned reference with the operator's own Docker credentials — hence the operators' reader grant on the remote repository (§5.3). Only `BaseRef` (`local.go:61-67`) still names ghcr.io, and only for the *release* fallback when `base_images.base` and `--base` are both unset — unchanged by this design, and reachable only by not running `init --base` at all.
- `.fugaro/*.Dockerfile` files that hard-code `FROM ghcr.io/dimipaun/fugaro-base` keep the lint's allowance (`config.LintDockerfile`, `internal/cli/docs_base_test.go:271`) and keep bypassing the pin — unchanged, and out of scope: a user's own Dockerfile naming the upstream image directly is their own base, exactly as `--base-image` is (§5.2).
- **The pinned digest is the multi-platform *index*, and the consumer picks its platform** — as it already did when it pulled ghcr.io, and unlike the copy, which was amd64-only by construction (`docs/design/base-image.md` §12: *"The installation's copy stays amd64-only, and that is correct"*). Cloud Run and Cloud Build are amd64, so nothing they do changes; what changes is that the installation's own path now also serves the arm64 child, so a local `fugaro image build --local --platform linux/arm64` that reads `base_images.base` pulls through the remote repository instead of failing on an amd64-only copy. **UNVERIFIED** — not in §2.12's table because nothing here depends on it, and the fact is a bonus rather than a requirement: a proxy is content-addressed, so serving any child of the index should follow, but the documentation says nothing about per-platform behavior in a remote repository; Check 43b/43d are where it shows, incidentally. The cost, if the arm64 child is pulled, is its layers too, cached in the installation's registry.

## 7. Failure modes

| Failure | What happens | Why, and what to do |
|---|---|---|
| **ghcr.io is down, and the digest is already cached** | jobs keep pulling; builds keep building | Documented: *"Cached artifacts are also still available if the external public repository is offline"* (§2.4) |
| **ghcr.io is down, and the digest is *not* cached** (a cold pull, or after a cache eviction) | the pull fails; a Cloud Run Job execution fails at start, a Cloud Build fails at its first step | Documented error shapes: 502 (upstream error), 504 (upstream offline) (§2.10). Mitigation, and it is the reason §3.2's read-back exists: the refresh run *warms* the manifest at pin time, so what is left cold is the layers. **The layers are not pre-warmed by design** — downloading 1.2 GB on the operator's machine is exactly what this design removes; if the owner wants a warm cache at pin time (pull the image through the repository once, ~1.2 GB down on the operator's uplink and into registry storage), that is a one-line flag decision (§10) |
| **Artifact Registry's upstream read limit** (600 reads/org/region/minute, §2.10) | 429 *"Resource has been exhausted"* on a cold fetch; cached reads are unaffected (*"requests for that artifact only count against your project's request quota"*) | A burst is only plausible on the first pull of a new digest across many repositories at once; `fugaro image refresh` is an operator-driven, per-repository action, and jobs pull one manifest per cold node. Out of scope to measure (§2.12 l) |
| **A tag is mutated upstream** (the nightly re-pushing `fugaro-base:<v>`, or a compromise) | **nothing changes for an installation**: the reference is a digest, and the tag is only a version marker | The one place a tag still matters is the refresh run's resolve step, and that is where #318's signature check lives: a digest with no valid signature for the `images.yml` identity is refused (§3.2). The `-pinned` tag (nightly-immune) is what the resolve step reads |
| **A digest is deleted or retagged upstream** | the cached copy still serves; a cold pull 404s (*"when the requested artifact doesn't exist in the upstream repository"*, §2.10) | Fail-loud at the next refresh (no signature) and at the next cold pull. Deliberately not defended against by keeping a second copy — that is the design this one replaces |
| **The remote repository's cache is evicted** (a cleanup policy, or lifecycle) | the next pull re-fetches from upstream; if upstream is healthy, invisible | **Why this design configures no cleanup policies on the remote repository** (§3.1, §5.3): the cache is small (what the jobs pull), and an eviction that lands during an upstream outage turns a warm pull into a 504 |
| **A VPC-SC perimeter** denies upstream access | every cold pull fails; *"existing cached artifacts … are still available"* | One documented command per project+location: `gcloud artifacts vpcsc-config allow` (§2.11). A project that cannot open it keeps `--base-image` (unchanged, §5.2) |
| **The upstream path shape is not what §2.2 infers** | §3.2's verification read fails, loudly, at refresh time — before anything is pinned, and before any job is moved | This is precisely why the read-back exists instead of trusting the inference; Check 43a/43b settle it once, live |
| **A consumer rejects the combined `:tag@digest` form** | `fugaro validate`/`init` would fail at the first job or build | §2.12 d, Check 43c/43d; the fallback is the digest-only reference with the version recorded separately (§3.1) |

## 8. The live check for the owner (Check 43)

Written in full in [docs/gcp-live-checklist.md](../gcp-live-checklist.md) as **Check 43** (the next free number; Check 42 is the config-transitions work's, per the owner's numbering note), scoped to **`fugaro-dev` and the sandbox repository only, never `belong`**, needing **no live apply by an agent** and no new code beyond the plan's own tasks: a `gcloud artifacts repositories create` in the sandbox project, three `docker pull`s, one `gcloud artifacts docker images list`, one `fugaro init --base base`, one check-job execution, one `fugaro init --repo` build, and a restore that deletes the repository it created.

Its items, and the UNVERIFIED fact each settles: **43a** the create and the path shape (§2.12 a, k, e); **43b** a cold digest fetch, and that the digest is identical to ghcr.io's (§2.12 b, c, g); **43c** the check job running from the pinned reference, tag-mutation-immune (§2.12 d, f, h); **43d** a Cloud Build with `_FUGARO_BASE` set to the pinned reference, and an ordinary `docker pull` of the combined form (§2.12 d); **43e** a nightly `workflow_dispatch` of `images.yml` leaving the `-pinned` tag (and therefore the pin) alone; **43f** the full sandbox `init --repo`/`image refresh` round trip, with the restore.

The check is written to be runnable **before** the implementation, as a feasibility test of §2's inference (43a/43b alone need no Fugaro change at all — three `gcloud`/`docker` commands against the sandbox project). That is deliberate: it is this design's equivalent of the 2026-10-12 Cloud Build pull test that #318 leaned on, and if 43a/43b fail, the design stops there, with nothing shipped and nothing migrated.

## 9. How this relates to #318's plan (`docs/plans/2026-10-13-retire-image-mirror.md`)

#318's design doc keeps the copy; its §8 defers this option. This design supersedes the *copy*, not the verification, so the plan's tasks split cleanly:

| #318 task | Fate |
|---|---|
| **Task 1** — CI: sign the pushed base image by OCI digest, plus the nightly-immune `<v>-pinned` tag | **Stays, required, unchanged.** This design resolves the same `-pinned` tag and verifies the same signature; without it there is nothing to pin. Its rollout precondition (the workflow file must be on `main` before the release's version-bump merge commit is tagged) applies verbatim |
| **Task 2** — `internal/basepin` (resolve + verify) and `mirror.ResolveDigest` | **Stays, required**, with two small additions: `Resolve` must also return the **index** digest to the caller (nothing dereferences to amd64 any more), and the pinned reference it feeds must be the remote-repository path (§3.1), not a ghcr.io one |
| **Task 3** — the copy gains a verification pre-step, sourced from the `-pinned` tag | **DROPPED.** Its premise — that a copy still happens — is what this design removes. The call site it edits (`init_images.go`'s `read()` around `mirror.Plan`) is replaced by §3.2's resolve-verify-read-pin, which is this plan's Task 3 |
| **Task 4** — CLI confirmation text ("still copies") | **DROPPED.** Two of its three sentences are about copying; the third ("verified by signature") is retained as part of this plan's Task 4 wording |
| **Task 5** — docs | **Kept in spirit, rewritten**: the docs it edits say the copy verifies; they must now say the copy is gone (§5.1's doc list) |
| **Task 6** — full sweep | Stays |
| **Task 7** — live checks 39a-39f | **39a, 39c, 39e stay** (resolve + verify a real signed `-pinned` image; the tamper test; the nightly never moving `-pinned`). **39b is dropped** (it asserts the copy lands from the `-pinned` tag — there is no copy). **39f is dropped in favour of Check 43f**, which is the same round trip against the new path. **39d stays out of scope.** The result: #318's checklist row shrinks to 39a/39c/39e, and this design's row is Check 43a-43f |

`docs/design/retire-image-mirror.md` itself is **not** rewritten by this change: it stays as the historical design of the copy-plus-verification it describes, and gains a status line pointing here. This document is the design of record for the copy's removal.

## 10. Decisions for the owner to rule on

1. **The repository's name and the tag-in-reference form.** `fugaro-ghcr` (§3.1) versus a name that does not name the upstream (`fugaro-upstream`); and `<ver>@sha256:<digest>` (the version marker, §3.1) versus a digest-only reference with a new `base_versions` config map. **Recommendation: `fugaro-ghcr` and the combined form**, with the digest-only fallback held in reserve for §2.12 d. This is the design's one structural choice that a live check (43c/43d) could overrule, and the fallback is small.
2. **Whether the refresh run pre-warms the layers** (§7, cold pull). **Recommendation: no** — the removed 1.2 GB is the point; the manifest read-back is the compromise, and a first-execution pull is already what the check job does today for a fresh copy.
3. **Whether `fugaro-base`'s copy of the base image is deleted after the migration.** **Recommendation: no explicit deletion** — the existing cleanup policies prune it, and `prevent_destroy` is untouched.
4. **Whether history follows.** The remote repository makes `fugaro-history` copyable-free the same way (its jobs would reference a pinned remote digest, its `:latest` reference resolved and signed at refresh time). **Recommendation: a separate, later design** — this one is the base image's, and history's copy is the only remaining user of `internal/mirror`.
5. **The VPC-SC story for bring-your-own projects** (§2.11): document `gcloud artifacts vpcsc-config allow` in `docs/gcp-setup.md`, and keep `--base-image` as the answer for projects that cannot open the perimeter. **Recommendation: both.**

## 11. Non-goals

- Any live cloud or network action by the agent: no repository is created, no image pulled, no project touched. Check 43 is the owner's.
- Retiring the history image's copy (§5.2, §10.4), `internal/mirror`'s copy engine, or `--base-image`.
- Changing `internal/infra/tf/cover.go`'s rules, `internal/mirror`'s trust model, or the base image's own content, size budget, monthly bump or secret scan.
- Implementing Artifact Registry **virtual** repositories, CMEK, cleanup policies or upstream credentials for the new repository: none is needed for a public package, and each would add a trust input (§2.3).
- Any change to `.github/workflows/**` (Fugaro cannot push one; #318's Task 1 stages the workflow change under `docs/pending-workflows/`, and this design adds nothing there).
