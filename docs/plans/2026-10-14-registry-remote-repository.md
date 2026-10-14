# Implementation plan: serve `fugaro-base` through an Artifact Registry remote repository

Source: [docs/design/registry-remote-repository.md](../design/registry-remote-repository.md). The owner-run check is [docs/gcp-live-checklist.md](../gcp-live-checklist.md) **Check 43**, written in full by this change. **This plan is not executed in this change** (docs only): it is handed to the owner to rule on (design §10) and, once approved, implemented task by task.

**What this plan does, in one line:** deletes the base image's copy step (`fugaro init --base`/`image refresh`/`upgrade` no longer push 1.2 GB into `fugaro-base`), and replaces it with *resolve on ghcr.io → verify the cosign signature of that digest (#318's Task 1/2 machinery, kept) → read that digest back through a new per-installation Artifact Registry **remote repository** → pin `base_images.base` to the remote path at that digest*.

## Changes to #318's plan (`docs/plans/2026-10-13-retire-image-mirror.md`) — exactly what to drop

| #318 task | Fate |
|---|---|
| Task 1 — sign by OCI digest, `-pinned` tag, `cosign-installer` pin | **Keep, unchanged, and it is a precondition of this plan.** Nothing here replaces it: it is what the resolve step verifies. Its rollout precondition (the workflow file must be on `main` before the release's version-bump merge commit is created and tagged) applies verbatim, and it is also why design §8's **43d-43f need it merged *and* a tagged release that ran it** — before that release there is no signed `-pinned` image for the resolve step to find |
| Task 2 — `internal/basepin` + `mirror.ResolveDigest` | **Keep, amended**: `basepin.Resolve` returns the **index** digest to its caller, and the reference it feeds is the remote path (this plan's Task 2/3), not a ghcr.io one |
| Task 3 — the copy gains a verification pre-step | **DROP ENTIRELY.** Its whole premise is that a copy still happens. The call site it edits (`internal/cli/init_images.go`'s `read()`, `mirror.Plan` at `:288`) is replaced by this plan's Task 3 |
| Task 4 — CLI confirmation text ("still copies") | **DROP ENTIRELY**, replaced by this plan's Task 5 |
| Task 5 — docs | Keep in spirit, rewritten (this plan's Task 6) |
| Task 6 — full sweep | Keep (this plan's Task 7) |
| Task 7 — live checks 39a-39f | **Amend:** 39a, 39c, 39e stay, and **39a's Expected text is amended** — `docs/plans/2026-10-13-retire-image-mirror.md:562` ends it with *"the copy lands in the installation's own Artifact Registry"*, and after this plan there is no copy: the same command verifies the same digest and pins the remote path instead. Amend it to *"and the pinned reference is the remote repository's path at that digest; nothing is copied"* (design §9). **39b is dropped** (it asserts the copy lands from the `-pinned` tag — there is no copy); **39f is superseded by Check 43f** (the same round trip against the new path); 39d stays out of scope. The checklist's numbering ruling is updated accordingly (Task 8) |

`docs/design/retire-image-mirror.md` itself is not rewritten: it gains one status line pointing here, and stays as the design of the copy-plus-verification it describes (design §9).

## Global constraints

- **No live cloud or network in any automated test.** Every new fake (`basepin.OCIVerifier`, the remote-repository read-back client) is in-process; the real signature, the real remote-repository path and the real Cloud Build pull are the owner's Check 43, marked NOT RUN until the owner runs it.
- **Never touch `belong`** (EdgeWeb's and EdgeServer's installation) in any live step or in any command that is not read-only. Every live item here is scoped to `fugaro`/`fugaro-dev` and the sandbox repository, exactly as the existing checklist entries are.
- **Docs only in *this* change.** No Go, Terraform or workflow file is edited now.
- **No new `.github/workflows/**` file and no edit to one** (Fugaro cannot push such a commit). #318's Task 1 stages its workflow change under `docs/pending-workflows/` — staging that file **is** that task's first step, and it has not been done yet (the directory holds only its `README.md`, *"Nothing is pending (0.7.0)"*, and `.github/workflows/images.yml` carries neither the `-pinned` tag nor a cosign step). This plan adds nothing there, and nothing here depends on #318's file existing — only on the signed release it produces (design §9).
- **Nothing here weakens an existing guard.** `tf.Guard`, `managedVersion`/`isManaged`'s refusal to touch a custom base, the `build_base` grant on `fugaro-base` (`modules/repo/build.tf:44-50`) — none is relaxed. `internal/infra/tf/cover.go`'s job-image rule **is** changed, and only *narrowed*: a remote repository proxies **all** of ghcr.io, so "under `<registry>/<project>/`" stops confining anything, and `inRegistry` gains the pin's closed shape (owner + `fugaro-<kind>` + the version tag + a required digest, design §3.6, Task 4). The three Terraform tests that constrain registry shape (`TestPreventDestroy` `:214`, `TestMirrorRunsAsUserNotBuildAccount` `:159`, `TestTagMoverRoleIsTagsDeleteOnly` `:114`, all in `deploy/terraform/terraform_test.go`) and `cover.go`'s `TestGrantRulesAreTheModules` (`internal/infra/tf/cover_grants_test.go:163`) are **extended**, never edited to permit something new. **Trap:** `TestMirrorRunsAsUserNotBuildAccount` (`terraform_test.go:173`) treats a `google_artifact_registry_repository_iam_member` block as "on the base registry" when its body contains the substring `base_registry` — the new grants must name `ghcr_registry` and nothing else that matches.
- **Each task is independently reviewable:** a task's tests fail before its implementation and pass after.
- **Security-sensitive tasks are flagged for opus review** inline.
- **Docs match code.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint'` and `gofmt -l .` after every task.
- **The owner's live check runs before the implementation only if the owner wants it to.** Check **43a, 43b and 43c** need **no Fugaro change at all** (a handful of `gcloud`/`docker` commands against the sandbox project) and settle design §2.12 a/b/c/g/e/k/m — design §2's one inference. If they fail, this plan stops there, nothing shipped and nothing migrated. **43d-43f need the implementation *and* a tagged release that ran #318's Task 1** (design §8): before that release there is no signed `-pinned` image, so nothing can be resolved, verified or pinned.

## Task 1: Terraform — the remote repository, its grants, and the plumbing

**Starting state:** `deploy/terraform/gcp/modules/installation/registry.tf` owns `google_artifact_registry_repository.base` (repository_id `var.names.base_registry`, format `DOCKER`, cleanup policies gated on `var.registry_cleanup`, `prevent_destroy = true`). `internal/infra/tfvars.go:21-22` holds `BaseRegistry`/`LegacyRegistry`; `InstallationNames` (`:111-117`) carries them into the installation root's tfvars; `InstallationOutputs` carries `registry_host`/`base_registry` back.

**Files:**
- Modify: `deploy/terraform/gcp/modules/installation/registry.tf` (the new resource), `variables.tf` (the `names` object type and its validation, `:54-78`), `outputs.tf` (`ghcr_registry`), `modules/installation/iam.tf` (the operators' reader grant), `modules/repo/build.tf` (the build account's reader grant, beside `build_base`), `deploy/terraform/gcp/roots/installation/outputs.tf` and `roots/repo/variables.tf` (threading it exactly as `base_registry` is threaded)
- Modify: `internal/infra/tfvars.go` (`GhcrRegistry = "fugaro-ghcr"`, `InstallationNames.GhcrRegistry`, `Installation()`), `internal/infra/spec.go` (`InstallationOutputs.GhcrRegistry` and wherever `BaseRegistry` is read off the outputs)
- Modify: `internal/infra/imports.go` (the `importGhcrRegistry` kind and its `importTable` row) and `internal/infra/discover.go` (`DiscoverInstallation`'s registry lookup, beside the base registry's)
- Modify: `deploy/terraform/terraform_test.go` (new assertions), `internal/infra/tfvars_test.go` / `internal/infra/spec_test.go` (the names round-trip), `internal/infra/discover_test.go` (the new import kind, the adoption and the refusal; `m5Installation` gains the repository, since `internal/infra/generated_root_test.go:95-107` fails when a kind in `importTable` is never generated)

**Interfaces:**

```hcl
resource "google_artifact_registry_repository" "ghcr" {
  project       = var.project
  location      = var.region
  repository_id = var.names.ghcr_registry          # "fugaro-ghcr"
  description   = "Public Fugaro images from ghcr.io, cached for this installation's builds and jobs"
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"
  labels        = { fugaro = "managed" }
  remote_repository_config {
    description = "ghcr.io"
    docker_repository {
      custom_repository {
        uri = "https://ghcr.io"
      }
    }
  }
  lifecycle {
    prevent_destroy = true
  }
}
```

Each attribute is on its own line, as the module writes them elsewhere — `terraform_test.go`'s `hasAttr` is line-anchored (`^\s*attr\s*=\s*val\s*$`), so a one-line `custom_repository { uri = "…" }` would be a passing-looking resource whose test could not see the URI at all.

- **No `cleanup_policies`** (design §7: an eviction during an upstream outage turns a warm pull into a 504) and **no `upstream_credentials`** (the packages are public; the repository is created unauthenticated, which is the only mode that cannot need rotation, design §2.3).
- **`disable_upstream_validation` is left at its default (validation on)**, so a typo'd upstream URI fails the apply rather than a job's first pull (design §2.3).
- **`repository_id` must satisfy `internal/infra/tf/cover.go`'s `repoID = ^fugaro(-[a-z0-9-]+)?$` (`:154`)**: `fugaro-ghcr` does. The module's own `names` validation is a hand-written list, not a pattern applied to every name — `variables.tf:74-77` is `alltrue([for id in [var.names.legacy_registry, var.names.base_registry] : can(regex("^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$", id))])` — so **`ghcr_registry` must be added to that list** (and named in its `error_message`), or the new name ships with no validation at all.
- **`prevent_destroy = true` is required** by `TestPreventDestroy`; the remote repository is not disposable (the migration never deletes it).
- Grants: `google_artifact_registry_repository_iam_member.ghcr_reader` to `var.operators` (`roles/artifactregistry.reader`) — the operator's own credentials do Task 3's read-back; and `google_artifact_registry_repository_iam_member.build_ghcr` in `modules/repo/build.tf` for the build account, whose Cloud Build pulls the derived build's step image and its `FROM` through the remote repository (design §6).
- Both are `roles/artifactregistry.reader`, which `cover.go`'s `grantRules` allowlist permits on an Artifact Registry repository for people or accounts (`cover.go:95-99`) — **and that is not the only role it permits there**: the same block allows `roles/artifactregistry.writer` (`:98`), which is exactly what the installation module's `base_writer` uses (`modules/installation/iam.tf:137-145`). `reader` is the right choice for these two grants because neither the operator's read-back nor the build account ever writes to the remote repository (§5.2), not because the allowlist forbids the alternative. `TestGrantRulesAreTheModules` keeps passing unchanged.
- The installation module's API list already includes `artifactregistry.googleapis.com` (`modules/installation/apis.tf:10`) — **no new API, no new secret, no Secret Manager enablement** (design §2.3).
- **A new import kind, and the ordering trap it removes** (design §5.3). `imports.go` gains `importGhcrRegistry` (`importKind` *"ghcr registry"*) with the `importTable` row `module.installation.google_artifact_registry_repository.ghcr` → `projects/{project}/locations/{region}/repositories/{name}`, and `DiscoverInstallation` (`discover.go:422-431`, the base registry's branch) gains the third registry lookup, exactly the shape of the base registry's:
  - the repository exists and carries `fugaro=managed` → `d.add(importGhcrRegistry, spec.Region, "", spec.Names.GhcrRegistry)` — **adopted**, which is what makes Check 43a's hand-created repository safe to leave in place (it passes `--labels=fugaro=managed`);
  - the repository exists without the label → `d.foreign("Artifact Registry repository "+spec.Names.GhcrRegistry, …)`, the same refusal the base registry gets (`discover.go:197-200`: *"… exists but is not this installation's: it carries …, not fugaro=managed. fugaro refuses to adopt it; …"*) — a named message at discovery time rather than a Terraform `already exists` halfway through an apply;
  - it does not exist → nothing, the ordinary create.
  Making the resource an import kind is not optional bookkeeping: **`internal/infra/generated_root_test.go:95-107` walks every key of `importTable` and fails unless discovery generated it**, so `m5Installation` (`discover_test.go:156-168`) must add the repository and `installationSpec` must carry `GhcrRegistry` — the tests that catch a half-plumbed name are the same ones that prove this kind works.

- [ ] **Step 1: Write the failing tests**

```go
// TestRemoteRepositoryIsARemotePullThroughProxyOfGhcr: the installation
// owns a Docker remote repository over https://ghcr.io, in the project it
// installs into, named like every other fugaro registry, unauthenticated
// (the packages are public), without cleanup policies (design §7), with
// prevent_destroy (the migration never deletes it), and with upstream
// validation left on (design §2.3) so a typo fails the apply.
//
// Assertions use this file's own helpers, resourceBlocks and hasAttr —
// there is no HCL decoder here, and adding one would be a new dependency
// in a test that only needs to read our own HCL (TestResourceBlocks at
// :288 is the model).
func TestRemoteRepositoryIsARemotePullThroughProxyOfGhcr(t *testing.T) {
	walk(t, func(path string, b []byte) {
		for _, blk := range resourceBlocks(t, path, b, "google_artifact_registry_repository") {
			if blk.name != "google_artifact_registry_repository.ghcr" {
				continue
			}
			if !hasAttr(blk.body, "mode", `"REMOTE_REPOSITORY"`) ||
				!hasAttr(blk.body, "format", `"DOCKER"`) ||
				!hasAttr(blk.body, "repository_id", "var.names.ghcr_registry") ||
				!hasAttr(blk.body, "uri", `"https://ghcr.io"`) {
				t.Errorf("%s: %s is not the remote repository (design §3.1)", path, blk.name)
			}
			if strings.Contains(blk.body, "cleanup_policies") {
				t.Errorf("%s: %s must carry no cleanup policies (design §7)", path, blk.name)
			}
			// Validation is left at its default: an unset attribute, so
			// assert the absence of the escape hatch, not its value.
			if strings.Contains(blk.body, "disable_upstream_validation") {
				t.Errorf("%s: %s must not disable upstream validation (design §2.3)", path, blk.name)
			}
			if !hasAttr(blk.body, "labels", `{ fugaro = "managed" }`) {
				t.Errorf("%s: %s must carry fugaro=managed", path, blk.name)
			}
		}
	})
}
```

`walk` (`terraform_test.go:315`) visits each `.tf` file of the embedded tree, so the test needs no path list and cannot miss a rename. `prevent_destroy` is already covered tree-wide by `TestPreventDestroy` (`:214`), and the two grants by the assertions below — this test is about the *shape of the remote repository itself*.

plus, in `deploy/terraform/terraform_test.go`, one assertion that **both** registry reader grants exist and name `var.installation.ghcr_registry` (the operators' in `modules/installation/iam.tf`, the build account's in `modules/repo/build.tf`) — `resourceBlocks` + `hasAttr(blk.body, "role", `"roles/artifactregistry.reader"`)`, the same shape as `TestMirrorRunsAsUserNotBuildAccount` (`:159-193`) — and in `internal/infra` a `TestInstallationNamesCarryTheRemoteRepository` over `Installation()`'s tfvars JSON, plus, in `internal/infra/discover_test.go`, `TestDiscoverInstallationAdoptsTheGhcrRegistry` (marked → imported; unmarked → the `ForeignError` refusal; absent → nothing).

- [ ] **Step 2: Run them to see them fail**

Run: `cd deploy/terraform && go test ./... -count=1` and `go test ./internal/infra/ -run 'TestInstallationNames|TestGeneratedRoot' -count=1`
Expected: FAIL (`ghcr_registry` does not exist; `TestGeneratedRootValidates` fails on the missing Terraform block if the Go side lands first).

- [ ] **Step 3: Implement**

Add the resource, the two grants, the output, the variable, and the Go names. Leave `disable_upstream_validation` unset.

Run: `go test ./internal/infra/... ./deploy/terraform/... -count=1`
Expected: PASS. `terraform validate` runs as part of `internal/infra/generated_root_test.go`'s `TestGeneratedRootValidates` and settles design §2.12 j (the provider at its pinned version accepts these fields).

## Task 2: `internal/mirror` — the pinned reference, and the read-back

**Flag for opus review:** this task is where a wrong digest could be written into a reference the jobs trust. The read-back must compare digests **exactly** and must refuse on any mismatch, never fall back to the requested value.

**Files:**
- Modify: `internal/mirror/ref.go` (`RemoteRef`), `internal/mirror/mirror.go` (the read-back, reusing `getManifest`'s computed-digest rule at `:685-712` and `sum` at `:399`)
- Modify: `internal/mirror/mirror_test.go`

**Interfaces:**

```go
// RemoteRef is the reference an installation pins: the remote repository
// path for upstream image name, carrying both the release tag (the version
// marker managedVersion, baseRefReleaseRE and refreshVerdict's
// never-downgrade rule read) and the verified digest (what the pull
// resolves). See the design's §3.1 for why the tag stays.
func RemoteRef(registryHost, project, remoteRepo, upstream, tag, digest string) string
// → "<region>-docker.pkg.dev/<project>/fugaro-ghcr/dimipaun/fugaro-base:<ver>@sha256:<hex>"

// VerifyThroughRemote reads the manifest for upstream@digest through the
// remote repository with the caller's own Google credentials (the
// operator's ADC; the operator's reader grant is Task 1's, design §5.3)
// and fails unless the bytes it reads hash to exactly that digest. It
// reads one manifest and no layers. It is the design's §3.2 step 3.
//
// It reads the BODY and hashes it — it does not trust
// Docker-Content-Digest. That is getManifest's own rule (mirror.go:685-712:
// "computed, and equal to the one a digest ref names and to the registry's
// header when it sends one"), and a header-only check would prove
// reachability, not unchanged bytes. A remote repository is a proxy: the
// header is the proxy's word about someone else's bytes.
func VerifyThroughRemote(ctx context.Context, client *http.Client, registryHost, project, remoteRepo, upstream, digest string) error
```

`upstream` is the two-segment upstream path (`dimipaun/fugaro-base`), built from `DefaultSources[0]` (`mirror/ref.go`) rather than hard-coded, so a fork's mirror keeps working.

- [ ] **Step 1: Write the failing tests**

```go
// TestRemoteRefCarriesTheTagAndTheDigest: the reference keeps the version
// as a tag and the content as a digest, in the one order the grammar
// allows (distribution/reference's referencePat: name[:tag][@digest]).
func TestRemoteRefCarriesTheTagAndTheDigest(t *testing.T) {
	got := RemoteRef("us-east5-docker.pkg.dev", "fugaro-dev", "fugaro-ghcr",
		"dimipaun/fugaro-base", "0.7.1", "sha256:"+strings.Repeat("a", 64))
	want := "us-east5-docker.pkg.dev/fugaro-dev/fugaro-ghcr/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("a", 64)
	if got != want {
		t.Errorf("RemoteRef = %q, want %q", got, want)
	}
}

// TestVerifyThroughRemoteRefusesAMismatchedDigest: a registry that serves
// a different digest than the one asked for is a failure, not a warning —
// the caller pins what this function proved, and nothing else.
func TestVerifyThroughRemoteRefusesAMismatchedDigest(t *testing.T) {
	srv := httptest.NewServer(...) // 200 with Docker-Content-Digest: sha256:<other>
	err := VerifyThroughRemote(ctx, srv.Client(), "us-east5-docker.pkg.dev", "p", "fugaro-ghcr", "dimipaun/fugaro-base", "sha256:"+strings.Repeat("a", 64))
	// err must be non-nil and must name both digests
}

// TestVerifyThroughRemoteHashesTheBodyNotTheHeader is the one that makes
// the read-back worth doing: the server sends the digest the caller asked
// for in Docker-Content-Digest and a body that hashes to something else.
// A header-only implementation passes this test and pins bytes nobody
// verified; the body-hashing one refuses.
func TestVerifyThroughRemoteHashesTheBodyNotTheHeader(t *testing.T) { ... }
```

plus a test that a 404 (design §2.10's documented shape for a missing upstream version) reads as a clear "the remote repository cannot serve this digest" error rather than a raw status.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/mirror/ -run 'RemoteRef|VerifyThroughRemote' -count=1`
Expected: FAIL (both functions do not exist).

- [ ] **Step 3: Implement**

Run: `go test ./internal/mirror/... -count=1`
Expected: PASS.

## Task 3: `internal/cli` — resolve, verify, read back, pin; and delete the base copy

**Flag for opus review:** this is the trust boundary. The order is resolve → verify signature → read back → write, and any failure must leave `base_images` untouched and copy nothing.

**Starting state:** for base kinds, `imagesStage`'s `items()` (`internal/cli/init_images.go:191-202`) builds `src` from `mirror.ParseSource(allow, allow[0]+"/"+name+":"+ver)` (`:192`) and `dst` from `mirror.Dest(host, lc.GCPProject, dstName, dstTag)` (`:196`), `read()` calls `m.Plan(ctx, it.src, it.dst, expect[it.key()])` (`:288`), and `record()` writes `next.BaseImages[k] = p.item.dst.String()` (`:522`).

**Files:**
- Modify: `internal/cli/init_images.go` (the base-kind item path, `read()`'s plan/apply branch, `record()`'s value, the `Apply()` confirmation, `sourceLine()`), `internal/cli/image_refresh_rules.go` (`refreshBase`'s contract), `internal/cli/image_refresh.go` (the plan's `want`/preview), `internal/cli/upgrade_cloud.go` (`cloudCheck`'s staleness predicate and its reason, `:104-143`, the `cur != want` comparison at `:128`, `:486`), `internal/cli/init_fugaroyaml.go` if the local-config write path names the destination
- Modify: `internal/cli/init_images_test.go` (its rig, `newImagesRegistry`/`newImagesRigFor` at `:45`/`:147`, gains a remote-repository fixture; `TestImagesStageOnlyKinds` at `:709` and `TestInitRefusesWhenFugaroBaseIsNotPublished` at `:292` are re-pointed at the new path), `internal/cli/image_refresh_rules_test.go`, `internal/cli/image_refresh_steps_test.go`, `internal/cli/upgrade_cloud_test.go` (a copied same-version entry must read as **stale**, not current)

**Interfaces:**

1. **A base-kind item carries a pinned reference instead of a destination.** `items()`'s base branch stops calling `mirror.Dest` and instead records `upstream` (`dimipaun/fugaro-base`) and the kind's version; `read()`'s base branch, in place of `mirror.Plan`, calls the kept #318 Task 2 hook `resolvePinnedBase(ctx, source, kind, version)` → the verified index digest, then `mirror.VerifyThroughRemote(...)` for that digest, then `mirror.RemoteRef(...)`. **A var `resolvePinnedBase` (the #318 Task 2 hook, retained for the tests' sake) is how the tests fake the network**, exactly as #318's Task 3 test does.
2. **The copy for base kinds is gone.** No `mirror.Plan`/`m.Copy` call for a base kind, so `--replace-image base` has nothing to replace: it becomes a **user error naming the removal** (design §5.1). `history` keeps the whole existing path, unchanged.
3. **`record()` writes `RemoteRef(...)`'s string** for a base kind (`:522`'s `p.item.dst.String()` becomes the pinned reference), and the line it prints keeps the `(digest …)` suffix it already prints.
4. **`refreshBase` becomes an offline staleness question, and the same version is no longer enough (B3).** Today it computes `want` from the version alone (`image_refresh_rules.go:26-46`): at the same version it returns `mirror.Dest(...)` — the *copy's* path — so a 0.7.1 run on a 0.7.1 CLI is a no-op. After this change the pinned value is a digest, which **cannot be computed offline**, and a pre-migration same-version entry (`<host>/fugaro-base/fugaro-base:0.7.1`, no digest) must move. So the function's contract becomes **"is this entry this release's pin?"** — version **and** §3.6's shape (the remote path, this installation's owner, a `@sha256:` digest) — and it returns `(ref, current bool, err)`:
   - **current** when the entry is this release's pin — the run does nothing for that kind, verifies nothing and rebuilds nothing, and `ref` is the config's existing value;
   - **current** also when the entry is a *newer* managed base (`newer(curVersion(cur, host, kind), ver)`, unchanged) — the never-downgrade rule, and the reason the tag stays in the reference (design §3.1);
   - **stale** otherwise, including the migration case (right version, copied shape) and an empty entry. For a stale kind there is no value to return: the caller runs item 1's resolve-verify-read-back-pin, so `ref` is `""` and the caller must not print one.
   The two callers change with it: `refreshPlan` (`image_refresh.go:250-255`) records currentness instead of a `want` string and its per-workflow preview (`:271`) says *"rebuilt: its base moves to this release's pinned image"* rather than a reference it does not have (step 2's own confirmation prints the value, and `refreshBuilds` at `image_refresh_steps.go:334` keeps comparing against `t.lc.BaseImage(kind)` — the reloaded, post-step-2 value — so a re-pin at the same version still rebuilds). `newer()`/`curVersion()` are unchanged, which is why the tag stays in the reference (design §3.1).
5. **`--expect-digest base=sha256:<hex>` is narrowed to "refuse unless the verified digest is this one"** (design §5.1): the existing `expectRE` validation (`internal/cli/init_images.go:171`, applied in `parseExpectDigests` at `:163`) is unchanged, and the value is compared against the digest the verification produced, before anything is written.
6. **`fugaro upgrade`'s offline check reads the same question (B3).** `cloudCheck` (`upgrade_cloud.go:104-143`) does `want, err := refreshBase(lc, k, ver)` and calls the repository stale when `cur != want` (`:128`); with no digest available offline it must instead use the **currentness predicate** above. Otherwise a 0.7.1 installation whose entry is the copied 0.7.1 image reports `current: no base image to copy and no image to rebuild` (`upgrade_cloud.go:486`) and the migration silently does not happen on the path most users take. `upgrade_cloud_test.go`'s fixtures gain that case, and the stale reason names the shape — *"is <ref>; this release pins it by digest through <registry>"* — rather than printing a value it does not have.

- [ ] **Step 1: Write the failing tests**

```go
// TestInitBasePinsTheRemoteRepositoryAtTheVerifiedDigest is the whole
// design in one assertion: after --base base, the local config names the
// remote-repository path (not a copy in fugaro-base), at the digest the
// signature was verified for, with the release tag still present as the
// version marker; and nothing was pushed anywhere.
func TestInitBasePinsTheRemoteRepositoryAtTheVerifiedDigest(t *testing.T) {
	r := newImagesRig(t, "1.2.3")           // the rig's registry gains the remote repo
	digest := "sha256:" + strings.Repeat("a", 64)
	old := resolvePinnedBase
	resolvePinnedBase = func(ctx context.Context, source, kind, version string) (string, error) {
		return digest, nil
	}
	t.Cleanup(func() { resolvePinnedBase = old })

	out, lc, err := executeStdin(t, "us-east5\n", "init", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := "us-east5-docker.pkg.dev/<project>/fugaro-ghcr/dimipaun/fugaro-base:1.2.3@" + digest
	if lc.BaseImages["base"] != want {
		t.Errorf("base_images.base = %q, want %q", lc.BaseImages["base"], want)
	}
	if r.pushed != 0 {
		t.Errorf("the base kind must not push anything; %d push(es) seen", r.pushed)
	}
}

// TestInitBaseRefusesWhenTheRemoteRepositoryCannotServeTheDigest: a failed
// read-back writes nothing — no half-migrated config.
func TestInitBaseRefusesWhenTheRemoteRepositoryCannotServeTheDigest(t *testing.T) { ... }

// TestReplaceImageRefusesBaseNamesTheRemoval: there is nothing to replace.
func TestReplaceImageRefusesBaseNamesTheRemoval(t *testing.T) { ... }

// TestRefreshBaseIsStaleForACopiedEntryAtTheSameVersion is B3: the entry
// is this release's version, and it is still not this release's pin, so
// the migration must run. The mirror image of it — the pin itself, at the
// same version, is current — is what keeps a refresh from re-verifying
// and rebuilding on every run.
func TestRefreshBaseIsStaleForACopiedEntryAtTheSameVersion(t *testing.T) { ... }

// TestUpgradeSaysTheBaseMustMoveForACopiedEntryAtTheSameVersion is the
// same question on fugaro upgrade's offline path: cloudCheck must not
// answer "no base image to copy and no image to rebuild" for an
// installation that still needs the migration.
func TestUpgradeSaysTheBaseMustMoveForACopiedEntryAtTheSameVersion(t *testing.T) { ... }
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestInitBasePins|TestInitBaseRefusesWhenTheRemote|TestReplaceImageRefusesBase|TestRefreshBaseIsStale|TestUpgradeSaysTheBaseMustMove' -count=1`
Expected: FAIL (`mirror.RemoteRef`, `mirror.VerifyThroughRemote` and the new `refreshBase` contract do not exist).

- [ ] **Step 3: Implement**

Run: `go test ./internal/cli/... -count=1`
Expected: PASS. Then confirm the copy is really gone: `grep -n 'mirror.Dest' internal/cli/*.go` must show no base-kind call site, and `grep -rn 'm.Copy' internal/cli/` only the history path.

## Task 4: the confinement moves from "in our registry" to the pin's shape (B1), and every reader is fixed (B2)

**Flag for opus review: this is the security task.** The remote repository proxies **all** of ghcr.io, so `…/<project>/fugaro-ghcr/<anyone>/<anything>` is inside the prefix every validator tests, and the digest pin protects integrity, not provenance (design §3.6). Four sites lean on that prefix meaning "vetted by us", and three of them are gates, not warnings. This task closes all four **at the same time as** the reference changes shape — shipping the new path without it would accept any public ghcr.io image as this installation's base.

**Files:**
- Modify: `internal/cli/init_images.go` (`managedVersion` `:137-140`; its one-line wrappers `isManaged` `:173` and `curVersion` `:175`), `internal/cli/image_refresh_rules.go` (`refreshBase` `:26-46`, `refreshVerdict` `:51-67`, `customBaseRefusal` `:106-117`), `internal/cli/image_refresh.go` (the kind loop `:250-255` and its kept-check `:254`), `internal/cli/upgrade_cloud.go` (`cloudCheck`'s kind loop `:119-128`) — the owner is threaded to each
- Modify: `internal/infra/tf/cover.go` — **both** tests: `checkSpecs`'s `inRegistry` (`:749-754`, used for the check spec's registry at `:770` and its `BaseImages` at `:779`) **and** the job container image's own inline prefix test (`:650-651`, which does not call `inRegistry`) — plus `Cover` gaining `Owner` and the remote repository ID; and `internal/cli/init_review.go` (`cover()` `:269`, which sets them)
- Modify: `internal/cli/sharedcfg.go` (`baseImageRE` `:301-308`, and the validation that reads it at `:229-238`), `internal/infra/spec.go` (`BaseImageWarnings` `:846-856`), `internal/localcfg/localcfg.go` (the new `ImageSource` scalar beside `BaseImages` `:51-55`) and `internal/localcfg/shared.go` (`MergeShared`'s `keep` list `:89-93` and the `Shared()` drop list `:30-43`) — **the owner record of item 0**
- Modify: `internal/imagecheck/registry.go` (`parseImageRef` `:223-257`) — **B2, a real bug otherwise**
- Modify: `images/derived/cloudbuild.yaml` **only if** design §2.12 (d) comes back negative (Check 43e)
- Modify: the tests: `internal/cli/init_images_test.go`, `internal/cli/image_refresh_rules_test.go`, `internal/cli/sharedcfg_test.go`, `internal/infra/spec_test.go`, `internal/infra/tf/cover_test.go` (and `cover_grants_test.go` if the new field needs it), `internal/cli/init_review_test.go` (its `tf.Cover{…}` literal at `:39` still compiles — a new field is zero — but any review test that expects a plan with a job image to be *covered* must set the owner, or it will now be refused), `internal/imagecheck/registry_test.go`

**Interfaces — the shape rule (design §3.6), stated once:**

```
<host>/<fugaro_ghcr>/<owner>/fugaro-<kind>:<version>@sha256:<64 hex>
```

`<owner>` is the **recorded** source owner: the owner half of the local config's new `image_source` scalar (below), which is `ghcr.io/dimipaun` for a plain installation (`mirror.DefaultSources[0]`, `internal/mirror/ref.go:56`) and `ghcr.io/<owner>` for a fork that pinned with `--image-source`. The image name is `fugaro-<kind>`, and **the digest is required** — a `fugaro-ghcr` reference without one is not this installation's pin.

0. **The owner is recorded once, in the local config, because the flag cannot reach the readers (design §3.6, the row after the site table).** `--image-source` is installation-only: it is in `init.go`'s `installationFlags` (`:1808-1824`, the entry at `:1813`), and `init --repo` refuses it at `:1800-1802`. So on `fugaro init --repo` — the command that renders every job image, the spec and the build substitution — and on any `image refresh`/`upgrade --check` run without the flag, the flag value is **empty**, and an empty owner refuses every remote path: a fork's own legitimate pin would be refused on every run, and a plain installation would fail its own `init --repo`. The record is a new scalar **`image_source`** (`<host>/<owner>`, exactly the string `--image-source` takes, validated by `mirror.ValidSourcePrefix`, `ref.go:81-87`) in `internal/localcfg`'s `Config`, written by the same `record()` call that writes `base_images.<kind>` (`init_images.go:487-534`, the write at `:522`), and published in the shared config so every machine and every repository's checkout reads one value: `Shared()` (`localcfg/shared.go:30-43`) already copies it (a scalar not on its drop list) and `MergeShared` (`:75-107`) needs one `keep(&out.ImageSource, published.ImageSource)` line beside `Region`/`LogView`. `localcfg.Parse` is **strict** (an unknown key is refused), so the field must land in the struct before anything writes it; `sharedcfg.go`'s validation (`:229-238`) gains the field's own checks — syntax, and *"a remote-shaped `base_images` entry with no `image_source` is refused"* — beside the `baseImageRE` loop. **It is never derived from the pin** (the pin is the value under test; deriving the trusted owner from it makes the rule vacuous), and **a recorded source whose host is not `ghcr.io` is refused on the base path** — the branch the pin is formed and read on — because the remote repository's upstream is `https://ghcr.io` (§3.1): `quay.io/acme` could never be served through the proxy, while `image refresh`'s history copy (a direct pull) still accepts any host `ValidSourcePrefix` allows. This item's tests are in step 1's list below: `fugaro init --repo` on a fork's config, with **no** `--image-source` anywhere, must render and **accept** the fork's pin, and a recorded source whose host is not `ghcr.io` must refuse on the base path.

1. **`managedVersion(ref, host, kind, owner)` takes the owner** and accepts the **remote** path only for that owner **and** with a digest. The pre-migration `<host>/fugaro-base/fugaro-<kind>:<version>` rule is unchanged, character for character, so existing entries keep reading as managed and the migration path still works — note today's implementation (`:137-140`) is a `strings.CutPrefix` on `host+"/"+mirror.BaseRepository+"/fugaro-"+kind+":"` plus `releaseRE` on the rest, and `releaseRE` would **reject** a `…:0.7.1@sha256:…` suffix, so the remote branch is a separate cut, not a widened one. It still returns the release version, which is what `isManaged`, `curVersion` and `newer()` need (the tag stays in the reference for exactly this reason, design §3.1). **A foreign owner is "not managed"** — which is the point: `isManaged` is false, so `refreshBase` refuses with `customBaseRefusal`'s existing escape hatch instead of the never-downgrade rule keeping `…/fugaro-ghcr/evil/fugaro-base:99.0.0@sha256:…` forever (design §3.6's first regression). Failing closed, with the documented "remove the entry and rerun" fix.
2. **`cover.go`: the plan-policy gate gets the same shape test, in *two* places, and it is the one that has to be airtight.** There are two registry tests in this file and **they are not the same code**. `checkSpecs`'s `inRegistry` (`:749-754`) is a closure used twice: on the check spec's own `registry` (`:770`) and on each of its `BaseImages` entries (`:779`). A job **container's** `image` is a separate inline test inside `Cover.attributes` (`:650-651`) that never calls `inRegistry` — and it is the one that guards what a *run's job* pulls, i.e. the hole this task exists to close. Changing only `inRegistry` leaves `:650` accepting `<registry>/<project>/fugaro-ghcr/<anyone>/<anything>`. Both become: under the run's registry, with `refChars` and the `..` guard **kept**, and then **either** the `fugaro-base` path (as today) **or** the remote ID followed by exactly `/<owner>/fugaro-<kind>:<version>` and a `@sha256:<64 hex>` (the tag is part of the shape, §3.6's rule — a digest-only reference is refused by this gate). `tf.Cover` gains `Owner` (and the remote repository's ID), set in `init_review.go`'s `cover()` (`:269-283`) from the **local config's recorded `image_source`** — *not* from `e.r.o.imageSource`, which is empty on every `init --repo` (item 0) and would therefore fail the very command this gate exists to check — plus the installation's names; the field is a *parameter*, never read from the plan being covered. **An empty `Owner` refuses every remote path** (fail closed), so a caller that forgets to set it cannot silently reopen the hole. Anything under the remote ID that is not that shape is refused with the existing style of message (`"runs the image %q, outside this run's registry …"` becomes a message naming the shape).
3. **`baseImageRE(host)` accepts the remote path too, without narrowing anything it checks today.** The current regex (`sharedcfg.go:304-308`) is built from a `component` const — `[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*` — precisely so no path component can be `.` or `..` (its comment says so: *"the path's components can't be '.' or '..', and there is no other '@'"*), and that value reaches a Dockerfile `FROM` line. So the replacement **keeps `component`** and adds a second alternative: `^<host>/<fugaro-base>/` + `component(+ /component)* [+ optional :tag] [+ optional @sha256:<64 hex>]` **or** `^<host>/<fugaro-ghcr>/` + `component` + `/fugaro-<kind>:<tag>@sha256:<64 hex>$` — with the two IDs quoted from `infra.BaseRegistry` and `infra.GhcrRegistry`, not spelled out, and the kind taken from the entry's own key. It is a *syntax* gate as it always was (today it accepts `<host>/fugaro-base/<anything>:1.2.3`, which `managedVersion` then calls "not managed"); the confinement is item 1. **The digest requirement is what stops a shared config from naming a bare tag through the proxy.** Its validation message (`sharedcfg.go:231`) keeps its shape and names both registries.
4. **`BaseImageWarnings` (`spec.go:846`)** accepts a base image under **either** `<host>/<base_registry>/` **or** `<host>/<ghcr_registry>/`, and keeps warning for anything else (a `ghcr.io/...` value, or another project's registry) with the same wording plus the new registry's name. It reads `GhcrRegistry` from the outputs Task 1 added. **It is deliberately not a gate** and this task says so in its comment: the build account can read both repositories, so a warning here is a readability hint and a pointer at where a base belongs. Without this, every migrated installation would get a false warning on every `init --repo`/`doctor`.
5. **`customBaseRefusal`** keeps refusing a `dev-<sha>` base, and its message gains the second reason (design §5.4): the CLI has nothing to pin over a custom base, so `--base-image` is the only way forward. Its "not a release image fugaro init copied" phrasing is Task 5's (the wording pass).
6. **B2: `parseImageRef` must accept `name[:tag]@digest`, or the daily check silently skips its base trigger.** `internal/imagecheck/registry.go:223-257` splits on `@` first (`:228-233`), and because that leaves `tagOrDigest` non-empty the tag-splitting block right after it (`:234`) is **skipped whole** (`tagOrDigest == ""` is false), so for `…/fugaro-base:0.7.1@sha256:…` the `:0.7.1` stays **on the repository name**; the reference is not rejected at the `@` branch but later, by `repoNameRE` (`:34` — `:36` is `arHostRE`) at the `:253` check, `:` not being in the repo grammar; `readBaseDigests` (`internal/cli/imagecheck.go:119-130`) logs *"could not read the base image's digest …; its base trigger is skipped"* on **every daily check**, silently, forever. The fix is to strip an optional `:tag` before the `@digest` (the `name[:tag][@digest]` grammar `distribution/reference`'s `referencePat` defines), with a test for the combined form, for the digest-only form (unchanged), and for the tag-only form (unchanged). **The digest branch's behaviour is deliberately kept:** once the reference *is* a digest, `Registry.Digest` returns it at `:66-68` **without a registry call**, so the base trigger cannot fail on a cold cache — that is a feature of this design, not an accident to be "fixed" away. (The alternative, choosing a digest-only reference, avoids this parser bug by never exercising it; §5.5 and design §10.1 put the cost of each form side by side.)
7. **`images/derived/cloudbuild.yaml:144-159` is conditional on *two* unverified things, (d) and (n), and (n) is the one that fails silently.** `docker image inspect … RepoDigests` on a `tag@digest` value is *expected* to answer the digest (the reference already names it), but that is UNVERIFIED — Check 43e settles it with a real Cloud Build. **If 43e fails**, the template's `inspect` step and its `dev.fugaro.base.digest` label must take the digest from the reference instead of asking the daemon. **And the answer it gives is not merely cosmetic:** that value becomes the record's `BaseDigest` (`--base-digest-from`, `internal/cli/image_record.go:170`), which `triggers()` compares against the check's own digest read (`internal/imagecheck/triggers.go:36-38`). The check reads the **index** digest (`Registry.Digest` returns the reference's digest with no call, `registry.go:66-68`); if the daemon answers with the **platform manifest** digest, the comparison never matches and the base trigger fires on **every** daily check — one rebuild per workflow per day, silently, forever (design §2.12 n). So 43e must compare the two digests **explicitly**, and this task must not ship on the inference that they agree. Do not change the template on the strength of the inference alone.
8. **`baseRefReleaseRE` (`internal/cli/init_fugaroyaml.go:369-375`) needs no change** — it already tolerates a digest suffix and any host/path prefix ending in `fugaro-<kind>` — **and this task adds a test that proves it**, so a later edit cannot narrow it silently.
9. **The consumer list is kept honest by a grep, not by this document — and the grep has to be wide enough to find them.** An earlier draft's four-word pattern (`fugaro-base\|BaseRepository\|base_images\|BaseImage`) cannot find several of the readers §5.5 names, because **`grep` is case-sensitive and those readers spell the base reference differently**: `baseImageRE` (`sharedcfg.go:301`), `baseRef`/`baseRefReleaseRE` (`init_fugaroyaml.go:369`), the build record's `BaseRef`/`BaseDigest` fields and their JSON names `base_ref`/`base_digest` (`imagecheck.Record`, compared at `triggers.go:36-38`), `baseImageWarnings`' caller side, and `fugaro-ghcr`/`GhcrRegistry` themselves. The pattern step 3 ends with is therefore this one, and every hit is read — each is either a site above, a site whose behaviour is unchanged, or a site that must be added to the design's §5.5 table:

```bash
grep -rnE 'fugaro-base|BaseRepository|BaseRegistry|base_images|BaseImage|baseImage|base_ref|BaseRef|base_digest|BaseDigest|fugaro-ghcr|GhcrRegistry|managedVersion|isManaged|curVersion|refreshBase|refreshVerdict|inRegistry|RepoDigests|\.Digest\(' \
  --include='*.go' internal/ cmd/ | grep -v _test.go
```

The design's §5.5 table is the record; this grep is what keeps it from rotting. If a hit falls in none of the three buckets, the table is wrong, not the grep.

- [ ] **Step 1: Write the failing tests**

```go
// TestManagedVersionReadsTheRemotePathAndItsDigest: the shapes a release
// base can have (the copied pre-migration one, the pinned remote one), each
// with the digest where it belongs — and the three that must never be
// treated as a release: a foreign owner through the proxy, a remote path
// with NO digest (a bare tag is servable through the proxy), and a custom
// dev tag. The foreign-owner and no-digest cases are the regression this
// task exists for: both must come back "not managed", so refreshBase
// refuses them instead of the never-downgrade rule keeping them.
func TestManagedVersionReadsTheRemotePathAndItsDigest(t *testing.T) { ... }

// TestCoverRefusesAJobImageFromAProxyPathThatIsNotThePin: the plan-policy
// gate — the JOB CONTAINER path, Cover.attributes' own inline prefix test
// (:650-651), which is NOT inRegistry. A plan whose check job's image is
// <registry>/<project>/fugaro-ghcr/<other-owner>/fugaro-base:9.9.9@sha256:…
// must NOT be covered — before this task it passes the prefix test and is.
// Also: the same repository with no digest, and with an empty Cover.Owner
// (fail closed).
func TestCoverRefusesAJobImageFromAProxyPathThatIsNotThePin(t *testing.T) { ... }

// TestCoverRefusesACheckSpecOutsideThePin: the OTHER path — inRegistry
// (:749-754), reached through FUGARO_CHECK_SPEC. The spec's own `registry`
// (:770) and each of its BaseImages (:779) must be refused in exactly the
// same three cases as the job image above. Two tests, not one: a change that
// fixes only inRegistry leaves the job image accepting anything under
// <registry>/<project>/, and vice versa.
func TestCoverRefusesACheckSpecOutsideThePin(t *testing.T) { ... }

// TestParseImageRefAcceptsATagAndADigest is B2: the daily check's own read.
// Without it, every check run logs "its base trigger is skipped" and the
// base trigger never fires for a migrated installation.
func TestParseImageRefAcceptsATagAndADigest(t *testing.T) { ... }

// TestBaseRefReleaseREAcceptsThePinnedRemotePath: recipes_skew, the
// layered-config gate and imagePredates all read a base_ref through this
// regexp; a narrowed edit elsewhere must not silently make every migrated
// repository look like it predates the release.
func TestBaseRefReleaseREAcceptsThePinnedRemotePath(t *testing.T) { ... }

// TestBaseImageWarningsAcceptTheRemoteRepository: a migrated
// base_images.base is in the installation's own project, so it must not
// warn; a ghcr.io-shaped one still must.
func TestBaseImageWarningsAcceptTheRemoteRepository(t *testing.T) { ... }

// TestInitRepoAcceptsAForkPinWithTheRecordedOwner is item 0's reason for
// existing, tested where it actually broke. The config records
// image_source: ghcr.io/acme and base_images.base = the remote path at
// acme's owner; `fugaro init --repo` — which CANNOT take --image-source
// (init.go:1800-1802) — must render the plan and build a cover with
// Owner="acme". Before item 0 the flag value was "", so this refused the
// fork's own pin on every run.
func TestInitRepoAcceptsAForkPinWithTheRecordedOwner(t *testing.T) { ... }

// TestRefuseANonGhcrSourceForABaseKind: the remote repository's upstream is
// https://ghcr.io (§3.1), so quay.io/acme could never be served through the
// proxy. Recording such a source, or pinning a base with one, must refuse
// with that reason; the history kind's direct copy still accepts any host
// ValidSourcePrefix allows.
func TestRefuseANonGhcrSourceForABaseKind(t *testing.T) { ... }
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ ./internal/infra/ ./internal/imagecheck/ -run 'ManagedVersion|BaseRefReleaseRE|BaseImageWarnings|CoverRefuses|ParseImageRef|InitRepoAcceptsAForkPin|NonGhcrSource' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Run: `go test ./internal/cli/... ./internal/infra/... ./internal/imagecheck/... -count=1`
Expected: PASS. Then run the grep of item 9 and read every hit — a consumer this task missed is a claim in the design's §5.5 that is no longer true.

## Task 5: the confirmation, the previews and the refusal messages stop claiming a copy

**Flag for opus review:** this task is wording, and one of its strings is a *refusal* a user acts on. A wrong fix instruction here costs an operator their base image, so the message must name a command that works.

**Files:**
- Modify: `internal/cli/init_images.go` (`Apply()`'s confirmation block, `:410-420`; `unpinnedText` `:246-251`; `sourceLine` `:569-583`; the stage `Detail`/status strings of item 4), `internal/cli/image_refresh_rules.go` (`customBaseRefusal` `:106-117`, `refreshBase`'s "as fugaro init copied" phrasing `:36`), `internal/cli/image_refresh.go` (`:290`'s "copied into your registry" and the flag help), `internal/cli/upgrade_cloud.go` (`:486`'s *"no base image to copy and no image to rebuild"* — a **base kind still needs the migration**, so that sentence must not be reachable for one, which Task 3's item 6 is what makes true), `internal/cli/init.go` / `internal/cli/upgrade.go` (the `--image-source`/`--expect-digest` help texts), `internal/cli/recipes_skew.go` and `internal/cli/init_fugaroyaml.go` (the "run this next" sentences)
- Modify: the tests that assert those strings: `internal/cli/init_images_test.go`, `internal/cli/image_refresh_rules_test.go`, `internal/cli/image_refresh_test.go`, `internal/cli/upgrade_cloud_test.go`

**Interfaces:**

1. The `"copies %d image(s), %.1f MB"` sentence becomes conditional on the run: for a run with a base-kind item it becomes *"verifies the base image's digest against its signed release and pins it in your registry's remote repository (nothing is copied)"*; a run that still copies (`history`) keeps the existing sentence with the copy counts, since it is still true of what it describes.
2. The trailing sentence *"The digests above are what the release tags resolve to now, which is what the registry says, not a signature."* is **kept for history only** and **dropped for base**: a verified, signed digest is exactly what it says it is not. The base kind is also removed from `unpinnedText`'s list unconditionally, since after Task 3 any base-kind item that reaches the confirmation was verified (design §3.2) — `--expect-digest base=…` is no longer the only way a kind can be pinned.
3. `sourceLine()`'s terse preview gets the same base-kind exclusion and no new sentence.
4. **Every remaining sentence that says a base image was or will be *copied* is wrong after Task 3, and the grep — not this list — is what finds them.** Run `grep -rn 'copies\|copied\|copying' internal/cli/*.go | grep -v _test.go` (run for this plan: 60-odd hits) and judge each one. The rule: **the `history` kind's copy keeps its wording** (it is still literally true), **anything about a base kind is re-worded**, and **the config fan-out's copies are untouched** (`config_publish.go`'s *"copied to …"*, `checkJobCopyCaveat` `upgrade_cloud.go:153`, `init_adopt.go:101` — none is about an image). The hits this plan knows are base-kind ones, grouped so the grep's output can be checked against them:
   - **the stage's own status and confirmation**: `init_images.go:243` (*"copies the release images that are missing from your registry"*), `:248` (*"none (every image copied is pinned)"*), `:413-414` (the `"copies %d image(s), %.1f MB …"` sentence quoted in item 1), `:439` (*"images copied and verified by digest"*), `:477`, `:540`;
   - **the plan preview and the stop messages**: `image_refresh.go:168`, `:290` (*"copied into your registry if it lacks it"*), `:327`, `:335`;
   - **the refusals**: `image_refresh_rules.go:36`, `:108`, `:115` (`:105` is the `customBaseRefusal` doc comment above them, not a user-visible string — read it too, since it describes what the message means, but it is not one of the strings to re-word);
   - **the flag help texts**, which a user reads before running anything: `init.go:346`, `:347`, `image_refresh.go:356-357`, `upgrade.go:141-142` — `--image-source`'s *"the registry and owner the release base images are copied from"* and `--expect-digest`'s *"the digest of a source tag … Without it init trusts what the release tag in ghcr.io resolves to now, which is whoever can write that tag"*, which is **no longer how a base kind works** (it is signature-verified and pinned; the sentence stays true for `history`);
   - **the "run this next" pointers** (the sentences that tell a user which command to run next, and what it does): `init.go:173-175` (*"... then `fugaro image refresh --repo <owner/name> --workflow <name>` in the checkout (it copies the base image, ...)"*), `init_fugaroyaml.go:329`, `recipes_skew.go:146`;
   - **comments that describe the deleted stage**: `init_images.go:23`, `:32`, `:59`, `:134`, `image_refresh.go:118`, `:123-125`, `image_refresh_steps.go:39`, `init.go:230` (*"image copies that only add tags"*, a list of what the terminal's one review screen covers — still true of `history`, no longer true of a base kind).
   Two of these are not merely stale wording: `init_images.go:243`/`:248` and `:439` are the strings `fugaro image refresh`'s own summary and the stage's `Detail` are asserted against in tests, and `image_refresh.go:327`/`:335` are the multi-line help a user sees at the top of the plan.
5. **The refusal messages keep their instruction, changed to match what now happens.** `customBaseRefusal`'s fix — *"remove base_images.<kind> from <path> (keep a backup), then rerun"* — is still the right fix, and its reason gains Task 4's second one: there is nothing to pin over a hand-pushed image. `--base-image` stays the documented way to keep your own base. **No message may tell a user to run a command that no longer exists** (`--replace-image base` is refused, `--expect-digest base=…` is narrowed), which is what the test below asserts.

- [ ] **Step 1: Write the failing tests**

```go
// TestImagesStageConfirmationSaysTheBaseIsNotCopied: one run with both a
// base-kind pin and a history copy, so both provenance sentences can be
// asserted at once. The assertions are specific to the NEW wording —
// generic "verif"/"copies" substrings already appear in today's unrelated
// text and would make this pass before the change.
func TestImagesStageConfirmationSaysTheBaseIsNotCopied(t *testing.T) { ... }
// and: TestImagesPreviewDoesNotListBaseAsUnpinned
// and: TestNoMessageOffersARemovedBaseCommand — the refusal and stop strings
// are scanned for "--replace-image base" and for a base-kind
// "--expect-digest" instruction, neither of which works any more.
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestImagesStageConfirmationSaysTheBaseIsNotCopied|TestImagesPreviewDoesNotListBaseAsUnpinned|TestNoMessageOffersARemovedBaseCommand' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

## Task 6: Docs

**Files:**
- Modify: `docs/gcp-setup.md` §"The base image" (the copy paragraph at `:163`: `fugaro init --base` no longer copies; it verifies and pins a digest through the installation's remote repository; the 1.2 GB note goes; the accepted-risk sentence keeps its substance and gains "the digest is now verified and pinned"), §"What it costs" (the storage line, `:564`), and — only if design §10.5 is approved — one paragraph on `gcloud artifacts vpcsc-config allow` for a VPC-SC project
- Modify: `docs/base-image.md` (`:3`, `:5`: "a development build or an earlier release has no image to copy") and `docs/base-image-migration.md` (`:15`: "It copies `fugaro-base` into your registry" — the step's *effect* is unchanged, its mechanism is not)
- Modify: `docs/release.md` — **including a false premise this design disproves**: `:30`'s *"Cloud Build and Cloud Run in a user's project cannot read ghcr.io, and the project's base registry `fugaro-base` is writable only by people, never by a build account. So the `images` stage of `fugaro init` copies the images …"* is no longer the reason (design §1; Cloud Run documents direct public-GHCR pulls) — the copy's surviving justification is the controlled project-local path and the digest pin, and `:32`'s "signing the images and verifying before the copy is a planned follow-up" becomes "the digest is verified and pinned". `publish-base`'s signing and the `-pinned` tag stay; add that the CLI pins the remote path at that digest
- Modify: `README.md` (`:91`: "`fugaro init` copies base images from the release tag in ghcr.io and checks the copy by digest")
- Modify: `docs/backends.md` (`:40`'s "the image mirror" stage name, if it stays a stage), `docs/design/retire-image-mirror.md` (one status line at the top pointing to `registry-remote-repository.md`; **nothing else** — it is the historical design of the copy, design §9)
- Modify: `docs/plans/2026-10-13-retire-image-mirror.md` — **one Expected text, and nothing else**: 39a's row (`:562`) ends *"the copy lands in the installation's own Artifact Registry"*, which this design makes false; amend it to *"and the pinned reference is the remote repository's path at that digest; nothing is copied"* (design §9). The plan is otherwise left as the record of the copy's design, exactly like its design doc
- Modify: `docs/gcp-live-checklist.md` (this change already writes Check 43 and the numbering ruling; the implementation adds the results table it now carries as NOT RUN)
- No change: `SECURITY.md`, `docs/SPEC.md` (it never mentions the copy)

**A test constrains this edit:** `TestGCPSetupBaseImageSectionNamesRealKinds` (`internal/cli/docs_base_test.go:225`) requires §3 to keep the literal tokens `` `fugaro init --base base` ``, `` `--expect-digest base=sha256:<hex>` `` and `` `--base-image base=IMAGE` ``. `--expect-digest base=…` keeps its (narrowed) meaning (Task 3, item 5), so the token stays and the sentence around it changes.

**Acceptance:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint'`

- [ ] Draft each change, citing the design doc's wording.
- [ ] Run the docs/lint suite and `gofmt -l .`.

## Task 7: Full sweep

- [ ] `go build ./...`, `go vet ./...`
- [ ] `go test ./... -count=1`
- [ ] `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint'`
- [ ] `cd deploy/terraform && go test ./... -count=1`
- [ ] `gofmt -l .`
- [ ] `grep -rn 'mirror.Dest\|m.Copy' internal/cli/ internal/infra/` and read every remaining hit: each must be the history path or `--base-image`
- [ ] `grep -rn 'copies\|copied\|copying' internal/cli/*.go | grep -v _test.go` and read every hit against Task 5's rule (the history copy and the config fan-out keep theirs; a base kind's does not)
- [ ] `grep -rn 'fugaro-ghcr\|GhcrRegistry' --include='*.go' .` and read every hit: the reference's shape is the design's §3.6 rule everywhere it is read, and nowhere is it spelled out twice (a literal `"fugaro-ghcr"` outside `tfvars.go` and its tests is a bug)
- [ ] Task 4 item 9's wide consumer grep, run once more against the final tree, and every hit checked against the design's §5.5 table — the four-word draft of that pattern could not see `baseImageRE`, `baseRef`/`BaseRef`, `base_digest`/`BaseDigest` or `.Digest(`, which is exactly how a reader goes missing from the table
- [ ] Re-read `docs/design/registry-remote-repository.md` against the final code, and fix whichever of the two is wrong.

## Task 8 (owner-run; live check Check 43, NOT RUN until the owner runs it)

**Already written by this change** in `docs/gcp-live-checklist.md` — the numbering ruling (Check 42 reserved for the config-transitions work, Check 43 reserved by this plan, **including the amendment to #318's Check 39a Expected text**), the table row, and the `## Check 43` section with its FACT lines and its restore. The implementation's only job is to record the results there and to flip the row from NOT RUN.

Scoped to **`fugaro`/`fugaro-dev` and the sandbox repository only — never `belong`** — with **no live apply by an agent**. **The check names its own project and refuses to run otherwise:** `P` is hard-coded to `fugaro-dev` (never `gcloud config get-value project`, whose ambient default on the owner's machine is a `*-prod` project, while this check creates a repository and rewrites configs), the first block is a guard that stops unless `P` is exactly that and the checkout is the sandbox's, and every `gcloud` command carries `--project="$P"` — this was a real defect in the first draft of the check (design §8). **`43a`, `43b` and `43c` need no Fugaro change** and are the design's go/no-go (design §2.12 a/b/c/g/e/k/m). **43e carries one FACT line that is not about acceptance but about *agreement*** — the digest the derived build recorded (`RepoDigests` → `--base-digest-from` → `rec.BaseDigest`) against the `$D` 43b read (design §2.12 n): if the daemon answers with the platform manifest digest while the check reads the index digest, the base trigger fires on every daily check forever, silently, so the two must be compared rather than assumed equal (Task 4 item 7). **`43d`-`43f` need both the implementation *and* a tagged release that ran #318's Task 1** — before that release there is no signed `-pinned` image to resolve (design §8). Nothing else in this plan reserves a check number; the design's §2.12 table maps every UNVERIFIED claim to the item that settles it.

**The migration itself, for the two installations that exist (design §5.4), is not a live check and is not run by an agent.** It is: re-run `fugaro init` (creating the remote repository and its grants), then `fugaro init --base base`/`fugaro image refresh` in each repository, once per repository, by the owner. **No backward compatibility is built** — Fugaro is pre-release and the two installations migrate by re-running the commands.
