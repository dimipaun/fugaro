# Implementation plan: serve `fugaro-base` through an Artifact Registry remote repository

Source: [docs/design/registry-remote-repository.md](../design/registry-remote-repository.md). The owner-run check is [docs/gcp-live-checklist.md](../gcp-live-checklist.md) **Check 43**, written in full by this change. **This plan is not executed in this change** (docs only): it is handed to the owner to rule on (design §10) and, once approved, implemented task by task.

**What this plan does, in one line:** deletes the base image's copy step (`fugaro init --base`/`image refresh`/`upgrade` no longer push 1.2 GB into `fugaro-base`), and replaces it with *resolve on ghcr.io → verify the cosign signature of that digest (#318's Task 1/2 machinery, kept) → read that digest back through a new per-installation Artifact Registry **remote repository** → pin `base_images.base` to the remote path at that digest*.

## Changes to #318's plan (`docs/plans/2026-10-13-retire-image-mirror.md`) — exactly what to drop

| #318 task | Fate |
|---|---|
| Task 1 — sign by OCI digest, `-pinned` tag, `cosign-installer` pin | **Keep, unchanged, and it is a precondition of this plan.** Nothing here replaces it: it is what the resolve step verifies |
| Task 2 — `internal/basepin` + `mirror.ResolveDigest` | **Keep, amended**: `basepin.Resolve` returns the **index** digest to its caller, and the reference it feeds is the remote path (this plan's Task 2/3), not a ghcr.io one |
| Task 3 — the copy gains a verification pre-step | **DROP ENTIRELY.** Its whole premise is that a copy still happens. The call site it edits (`internal/cli/init_images.go`'s `read()`, `mirror.Plan` at `:288`) is replaced by this plan's Task 3 |
| Task 4 — CLI confirmation text ("still copies") | **DROP ENTIRELY**, replaced by this plan's Task 5 |
| Task 5 — docs | Keep in spirit, rewritten (this plan's Task 6) |
| Task 6 — full sweep | Keep (this plan's Task 7) |
| Task 7 — live checks 39a-39f | **Amend:** 39a, 39c, 39e stay; **39b is dropped** (it asserts the copy lands from the `-pinned` tag — there is no copy); **39f is superseded by Check 43f** (the same round trip against the new path); 39d stays out of scope. The checklist's numbering ruling is updated accordingly (Task 8) |

`docs/design/retire-image-mirror.md` itself is not rewritten: it gains one status line pointing here, and stays as the design of the copy-plus-verification it describes (design §9).

## Global constraints

- **No live cloud or network in any automated test.** Every new fake (`basepin.OCIVerifier`, the remote-repository read-back client) is in-process; the real signature, the real remote-repository path and the real Cloud Build pull are the owner's Check 43, marked NOT RUN until the owner runs it.
- **Never touch `belong`** (EdgeWeb's and EdgeServer's installation) in any live step or in any command that is not read-only. Every live item here is scoped to `fugaro`/`fugaro-dev` and the sandbox repository, exactly as the existing checklist entries are.
- **Docs only in *this* change.** No Go, Terraform or workflow file is edited now.
- **No new `.github/workflows/**` file and no edit to one** (Fugaro cannot push such a commit). #318's Task 1 already stages its workflow change under `docs/pending-workflows/`; this plan adds nothing there.
- **Nothing here weakens an existing guard.** `tf.Guard`, `internal/infra/tf/cover.go`'s rules, `managedVersion`/`isManaged`'s refusal to touch a custom base, the `build_base` grant on `fugaro-base` (`modules/repo/build.tf:44-50`) — none is relaxed. The three Terraform tests that constrain registry shape (`TestPreventDestroy`, `TestMirrorRunsAsUserNotBuildAccount`, `TestTagMoverOnlyOnOwnRegistry`) and `cover.go`'s `TestGrantRulesAreTheModules` are **extended**, never edited to permit something new.
- **Each task is independently reviewable:** a task's tests fail before its implementation and pass after.
- **Security-sensitive tasks are flagged for opus review** inline.
- **Docs match code.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint'` and `gofmt -l .` after every task.
- **The owner's live check runs before the implementation only if the owner wants it to.** Check 43a/43b need **no Fugaro change at all** (three `gcloud`/`docker` commands against the sandbox project) and settle design §2.12 a/b/c/g — design §2's one inference. If they fail, this plan stops there, nothing shipped and nothing migrated.

## Task 1: Terraform — the remote repository, its grants, and the plumbing

**Starting state:** `deploy/terraform/gcp/modules/installation/registry.tf` owns `google_artifact_registry_repository.base` (repository_id `var.names.base_registry`, format `DOCKER`, cleanup policies gated on `var.registry_cleanup`, `prevent_destroy = true`). `internal/infra/tfvars.go:21-22` holds `BaseRegistry`/`LegacyRegistry`; `InstallationNames` (`:111-117`) carries them into the installation root's tfvars; `InstallationOutputs` carries `registry_host`/`base_registry` back.

**Files:**
- Modify: `deploy/terraform/gcp/modules/installation/registry.tf` (the new resource), `variables.tf` (the `names` object type and its validation, `:54-78`), `outputs.tf` (`ghcr_registry`), `modules/installation/iam.tf` (the operators' reader grant), `modules/repo/build.tf` (the build account's reader grant, beside `build_base`), `deploy/terraform/gcp/roots/installation/outputs.tf` and `roots/repo/variables.tf` (threading it exactly as `base_registry` is threaded)
- Modify: `internal/infra/tfvars.go` (`GhcrRegistry = "fugaro-ghcr"`, `InstallationNames.GhcrRegistry`, `Installation()`), `internal/infra/spec.go` (`InstallationOutputs.GhcrRegistry` and wherever `BaseRegistry` is read off the outputs), `internal/infra/discover.go` only if a name lookup is needed for the new output
- Modify: `deploy/terraform/terraform_test.go` (new assertions), `internal/infra/tfvars_test.go` / `internal/infra/spec_test.go` (the names round-trip), `internal/infra/generated_root_test.go` (it already fails without the matching Terraform block for every `importTable` kind — no new kind is added, so this is a regression net, not an edit)

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
      custom_repository { uri = "https://ghcr.io" }
    }
  }
  lifecycle { prevent_destroy = true }
}
```

- **No `cleanup_policies`** (design §7: an eviction during an upstream outage turns a warm pull into a 504) and **no `upstream_credentials`** (the packages are public; the repository is created unauthenticated, which is the only mode that cannot need rotation, design §2.3).
- **`disable_upstream_validation` is left at its default (validation on)**, so a typo'd upstream URI fails the apply rather than a job's first pull (design §2.3).
- **`repository_id` must satisfy `internal/infra/tf/cover.go`'s `repoID = ^fugaro(-[a-z0-9-]+)?$` (`:154`)**: `fugaro-ghcr` does, and the module's own `names` validation (`variables.tf:75-78`) must accept it too.
- **`prevent_destroy = true` is required** by `TestPreventDestroy`; the remote repository is not disposable (the migration never deletes it).
- Grants: `google_artifact_registry_repository_iam_member.ghcr_reader` to `var.operators` (`roles/artifactregistry.reader`) — the operator's own credentials do Task 3's read-back; and `google_artifact_registry_repository_iam_member.build_ghcr` in `modules/repo/build.tf` for the build account, whose Cloud Build pulls the derived build's step image and its `FROM` through the remote repository (design §6).
- Both are `roles/artifactregistry.reader`, the only role `cover.go`'s `grantRules` allowlist permits on a `fugaro-` registry for people or accounts (`cover.go:93-97`), so `TestGrantRulesAreTheModules` keeps passing unchanged.
- The installation module's API list already includes `artifactregistry.googleapis.com` (`modules/installation/apis.tf:10`) — **no new API, no new secret, no Secret Manager enablement** (design §2.3).

- [ ] **Step 1: Write the failing tests**

```go
// TestRemoteRepositoryIsARemotePullThroughProxyOfGhcr: the installation
// owns a Docker remote repository over https://ghcr.io, in the project it
// installs into, named like every other fugaro registry, unauthenticated
// (the packages are public), without cleanup policies (design §7), with
// prevent_destroy (the migration never deletes it), and with upstream
// validation left on (design §2.3) so a typo fails the apply.
func TestRemoteRepositoryIsARemotePullThroughProxyOfGhcr(t *testing.T) {
	data := readTerraform(t, "modules/installation/registry.tf")
	var f struct {
		Resource struct {
			GoogleArtifactRegistryRepository map[string]struct {
				RepositoryID string `hcl:"repository_id"`
				Format       string `hcl:"format"`
				Mode         string `hcl:"mode"`
				Remote       []struct {
					Docker []struct {
						Custom []struct {
							URI string `hcl:"uri"`
						} `hcl:"custom_repository"`
					} `hcl:"docker_repository"`
				} `hcl:"remote_repository_config"`
				Cleanup []any `hcl:"cleanup_policies"`
				Lifecycle []struct {
					PreventDestroy bool `hcl:"prevent_destroy"`
				} `hcl:"lifecycle"`
			} `hcl:"google_artifact_registry_repository"`
		} `hcl:"resource"`
	}
	// ... decode, then:
	//   mode == "REMOTE_REPOSITORY", format == "DOCKER"
	//   remote[0].docker[0].custom[0].URI == "https://ghcr.io"
	//   len(cleanup) == 0, lifecycle[0].PreventDestroy == true
	//   repository_id is a var.names reference, not a literal
}
```

plus, in `deploy/terraform/terraform_test.go`, one assertion that **both** registry reader grants exist and name `var.installation.ghcr_registry` (the operators' in `modules/installation/iam.tf`, the build account's in `modules/repo/build.tf`), and in `internal/infra` a `TestInstallationNamesCarryTheRemoteRepository` over `Installation()`'s tfvars JSON.

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
- Modify: `internal/mirror/ref.go` (`RemoteRef`), `internal/mirror/mirror.go` (the read-back, reusing the destination-side authenticated client and the `Docker-Content-Digest` handling at `:688-712`)
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
// remote repository with the caller's own Google credentials and fails
// unless the registry answers with that exact digest. It reads one
// manifest and no layers. It is the design's §3.2 step 3.
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
- Modify: `internal/cli/init_images.go` (the base-kind item path, `read()`'s plan/apply branch, `record()`'s value, the `Apply()` confirmation, `sourceLine()`), `internal/cli/image_refresh_rules.go` (`refreshBase`'s `want`), `internal/cli/init_fugaroyaml.go` if the local-config write path names the destination
- Modify: `internal/cli/init_images_test.go` (its rig, `newImagesRegistry`/`newImagesRigFor` at `:45`/`:147`, gains a remote-repository fixture; `TestImagesStageOnlyKinds` at `:709` and `TestInitRefusesWhenFugaroBaseIsNotPublished` at `:292` are re-pointed at the new path), `internal/cli/image_refresh_rules_test.go`, `internal/cli/upgrade_cloud_test.go` if its fixtures assert a copied base

**Interfaces:**

1. **A base-kind item carries a pinned reference instead of a destination.** `items()`'s base branch stops calling `mirror.Dest` and instead records `upstream` (`dimipaun/fugaro-base`) and the kind's version; `read()`'s base branch, in place of `mirror.Plan`, calls the kept #318 Task 2 hook `resolvePinnedBase(ctx, source, kind, version)` → the verified index digest, then `mirror.VerifyThroughRemote(...)` for that digest, then `mirror.RemoteRef(...)`. **A var `resolvePinnedBase` (the #318 Task 2 hook, retained for the tests' sake) is how the tests fake the network**, exactly as #318's Task 3 test does.
2. **The copy for base kinds is gone.** No `mirror.Plan`/`m.Copy` call for a base kind, so `--replace-image base` has nothing to replace: it becomes a **user error naming the removal** (design §5.1). `history` keeps the whole existing path, unchanged.
3. **`record()` writes `RemoteRef(...)`'s string** for a base kind (`:522`'s `p.item.dst.String()` becomes the pinned reference), and the line it prints keeps the `(digest …)` suffix it already prints.
4. **`refreshBase`'s `want`** (`image_refresh_rules.go:41-45`) returns the pinned reference for the same version, read from the local config's existing value rather than recomputed — a refresh does not re-verify an unchanged pin. A refresh that finds a **newer** release runs the full resolve-verify-read-back path (Task 3's item 1) for it. `newer()`/`curVersion()` are unchanged, which is why the tag stays in the reference (design §3.1).
5. **`--expect-digest base=sha256:<hex>` is narrowed to "refuse unless the verified digest is this one"** (design §5.1): the existing `expectRE` validation (`:156`) is unchanged, and the value is compared against the digest the verification produced, before anything is written.

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
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestInitBasePins|TestInitBaseRefusesWhenTheRemote|TestReplaceImageRefusesBase' -count=1`
Expected: FAIL (`mirror.RemoteRef`, `mirror.VerifyThroughRemote` and the new `refreshBase` value do not exist).

- [ ] **Step 3: Implement**

Run: `go test ./internal/cli/... -count=1`
Expected: PASS. Then confirm the copy is really gone: `grep -n 'mirror.Dest' internal/cli/*.go` must show no base-kind call site, and `grep -rn 'm.Copy' internal/cli/` only the history path.

## Task 4: the new path is recognized everywhere a base image's shape is read

**Files:**
- Modify: `internal/cli/init_images.go`'s `managedVersion` (`:137-140`), `internal/cli/sharedcfg.go`'s `baseImageRE` (`:301-308`), `internal/infra/spec.go`'s `BaseImageWarnings` (`:841-856`), `internal/cli/image_refresh_rules.go`'s `customBaseRefusal` (`:104-110`)
- Modify: the corresponding tests: `internal/cli/init_images_test.go`, `internal/cli/sharedcfg_test.go`, `internal/infra/spec_test.go`, `internal/cli/image_refresh_rules_test.go`

**Interfaces:**

1. **`managedVersion(ref, host, kind)` accepts either repository id** — `<host>/fugaro-base/fugaro-<kind>:` (a pre-migration entry, or a custom base) **or** `<host>/fugaro-ghcr/<upstream path>/fugaro-<kind>:` (the new pin) — and, in both cases, an optional trailing `@sha256:<64 hex>`. It returns the release version, as today, which is what `isManaged`, `curVersion` and `newer()` need; an entry matching neither shape still reports "not managed" and is still never touched.
2. **`baseImageRE(host)` accepts the remote path too**: `^<host>/fugaro-(base|ghcr)(/[A-Za-z0-9._-]+)*(:<tag>)?(@sha256:<64 hex>)?$`, keeping the existing tolerance for the optional tag and digest. Its shared-config validation message keeps naming both registries.
3. **`BaseImageWarnings` (`spec.go:846`)** accepts a base image under **either** `<host>/<base_registry>/` **or** `<host>/<ghcr_registry>/`, and keeps warning for anything else (a `ghcr.io/...` value, or another project's registry) with the same wording plus the new registry's name. It reads `GhcrRegistry` from the outputs Task 1 added. Without this, every migrated installation would get a false warning on every `init --repo`/`doctor`.
4. **`customBaseRefusal`** keeps refusing a `dev-<sha>` base, and its message gains the second reason (design §5.4): the CLI has nothing to pin over a custom base, so `--base-image` is the only way forward.
5. **`baseRefReleaseRE` (`internal/cli/init_fugaroyaml.go:369-375`) needs no change** — it already tolerates a digest suffix and any host/path prefix ending in `fugaro-<kind>` — **and this task adds a test that proves it**, so a later edit cannot narrow it silently.

- [ ] **Step 1: Write the failing tests**

```go
// TestManagedVersionReadsTheRemotePathAndItsDigest: the two shapes a
// release base can have, each with and without the digest suffix, and the
// two that must never be treated as a release (a custom dev tag, and a
// ghcr.io-shaped value #318's rejected option would have produced).
func TestManagedVersionReadsTheRemotePathAndItsDigest(t *testing.T) { ... }

// TestBaseRefReleaseREAcceptsThePinnedRemotePath: recipes_skew, the
// layered-config gate and imagePredates all read a base_ref through this
// regexp; a narrowed edit elsewhere must not silently make every migrated
// repository look like it predates the release.
func TestBaseRefReleaseREAcceptsThePinnedRemotePath(t *testing.T) { ... }

// TestBaseImageWarningsAcceptTheRemoteRepository: a migrated
// base_images.base is in the installation's own project, so it must not
// warn; a ghcr.io-shaped one still must.
func TestBaseImageWarningsAcceptTheRemoteRepository(t *testing.T) { ... }
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ ./internal/infra/ -run 'ManagedVersion|BaseRefReleaseRE|BaseImageWarnings' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Run: `go test ./internal/cli/... ./internal/infra/... -count=1`
Expected: PASS.

## Task 5: the confirmation and the preview stop claiming a copy

**Files:**
- Modify: `internal/cli/init_images.go` (`Apply()`'s confirmation block, `:410-420`; `unpinnedText` `:246-251`; `sourceLine` `:569-583`)

**Interfaces:**

1. The `"copies %d image(s), %.1f MB"` sentence becomes conditional on the run: for a run with a base-kind item it becomes *"verifies the base image's digest against its signed release and pins it in your registry's remote repository (nothing is copied)"*; a run that still copies (`history`) keeps the existing sentence with the copy counts, since it is still true of what it describes.
2. The trailing sentence *"The digests above are what the release tags resolve to now, which is what the registry says, not a signature."* is **kept for history only** and **dropped for base**: a verified, signed digest is exactly what it says it is not. The base kind is also removed from `unpinnedText`'s list unconditionally, since after Task 3 any base-kind item that reaches the confirmation was verified (design §3.2) — `--expect-digest base=…` is no longer the only way a kind can be pinned.
3. `sourceLine()`'s terse preview gets the same base-kind exclusion and no new sentence.

- [ ] **Step 1: Write the failing tests**

```go
// TestImagesStageConfirmationSaysTheBaseIsNotCopied: one run with both a
// base-kind pin and a history copy, so both provenance sentences can be
// asserted at once. The assertions are specific to the NEW wording —
// generic "verif"/"copies" substrings already appear in today's unrelated
// text and would make this pass before the change.
func TestImagesStageConfirmationSaysTheBaseIsNotCopied(t *testing.T) { ... }
// and: TestImagesPreviewDoesNotListBaseAsUnpinned
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestImagesStageConfirmationSaysTheBaseIsNotCopied|TestImagesPreviewDoesNotListBaseAsUnpinned' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

## Task 6: Docs

**Files:**
- Modify: `docs/gcp-setup.md` §"The base image" (the copy paragraph: `fugaro init --base` no longer copies; it verifies and pins a digest through the installation's remote repository; the 1.2 GB note goes), §"What it costs" (the storage line), and — only if design §10.5 is approved — one paragraph on `gcloud artifacts vpcsc-config allow` for a VPC-SC project
- Modify: `docs/base-image.md` (the local-build note and wherever it describes pulling the base from `fugaro-base`), `docs/release.md` (`publish-base`'s signing and the `-pinned` tag stay; add that the CLI pins the remote path at that digest)
- Modify: `docs/design/retire-image-mirror.md` (one status line at the top pointing to `registry-remote-repository.md`; **nothing else** — it is the historical design of the copy, design §9)
- Modify: `docs/gcp-live-checklist.md` (Task 8 of this change already writes Check 43; the implementation adds the results table it now carries as NOT RUN)
- No change: `SECURITY.md`, `docs/base-image-migration.md` (its per-kind migration text is about recipes, not the base's storage)

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
- [ ] Re-read `docs/design/registry-remote-repository.md` against the final code, and fix whichever of the two is wrong.

## Task 8 (owner-run; live check Check 43, NOT RUN until the owner runs it)

**Already written by this change** in `docs/gcp-live-checklist.md` — the numbering ruling (Check 42 reserved for the config-transitions work, Check 43 reserved by this plan), the table row, and the `## Check 43` section with its FACT lines and its restore. The implementation's only job is to record the results there and to flip the row from NOT RUN.

Scoped to **`fugaro`/`fugaro-dev` and the sandbox repository only — never `belong`** — with **no live apply by an agent**. `43a` and `43b` need **no Fugaro change** and are the design's go/no-go (design §2.12 a/b/c/g). `43c`–`43f` need the implementation in place. Nothing else in this plan reserves a check number; the design's §2.12 table maps every UNVERIFIED claim to the item that settles it.

**The migration itself, for the two installations that exist (design §5.4), is not a live check and is not run by an agent.** It is: re-run `fugaro init` (creating the remote repository and its grants), then `fugaro init --base base`/`fugaro image refresh` in each repository, once per repository, by the owner. **No backward compatibility is built** — Fugaro is pre-release and the two installations migrate by re-running the commands.
