# Implementation plan: retire the image mirror for a digest-pinned FROM

Source: [docs/design/retire-image-mirror.md](../design/retire-image-mirror.md). Depends on the base-image hard cut (`docs/plans/2026-10-08-base-image.md` Tasks 17–21, release 0.7.0) having already landed: `config.Bases == ["base"]`, one `base_images` key. **This plan is not executed in this change** (docs only); it is handed to the owner to rule on and, once approved, implemented task by task.

**A note on this revision (fourth review round — a course correction, substantially shrinking this plan).** A fourth review found that the daily image check job is itself a Cloud Run Job running directly FROM the base image (`check.tf`, `internal/infra/spec.go`'s `check()`), and Cloud Run Jobs cannot deploy a bare `ghcr.io` reference (not verified live, assumed conservatively) — so `base_images.base` must keep its existing, Artifact-Registry-hosted shape, used exactly as today by the check job and `FUGARO_CHECK_SPEC`. This retires **only** Cloud Build's own dependency on that value for its `FUGARO_BASE` substitution, which is now resolved fresh at every submission instead. As a direct consequence, the design doc's earlier Tasks 4 (`cover.go`) and 5 (the Terraform grant) are **dropped entirely** — their whole premise (nothing reads `fugaro-base` for the base kind once builds stop needing it) turned out to be false — and Task 3 is substantially narrower than earlier drafts: a verification pre-step ahead of the *existing*, unchanged copy mechanism, plus the three Cloud Build callers resolving fresh instead of reading a config value.

## Global constraints

- **No live cloud or network in any automated test.** Every fake (`TagResolver`, `OCIVerifier`) is in-process; the real cosign signature and the real Cloud Build pull paths are the owner's live checks (Task 7), each marked NOT RUN until the owner runs it.
- **Never touch `belong`** (EdgeWeb's and EdgeServer's installation) in any live step. Every live check in Task 7 is scoped to `fugaro`/`fugaro-dev`, sandbox repository only, exactly as the existing entries of `docs/gcp-live-checklist.md` already are.
- **Nothing here weakens an existing guard.** `tf.Guard`, `internal/infra/tf/cover.go`, `managedVersion`/`isManaged`, the Terraform `build_base` grant — **none of these are touched by this plan at all** (design §4, §6).
- **Each task is independently reviewable**: a task's tests fail before its implementation and pass after.
- **Security-sensitive tasks are flagged for opus review** inline.
- **A real, owner-applied rollout precondition (design §5, §8).** `docs/pending-workflows/images.yml` (Task 1) can only reach the real `.github/workflows/images.yml` by a human manually copying it in. Task 3 makes every Cloud Build submission depend on a signed `-pinned` image existing. **The workflow file copy must land on `main`, and at least one real tag push must have run with it (producing a signed image, live-checked, Task 7 Check c), before or in the same release that ships Task 3's code.** This can be the same release as Task 1, as long as the copy lands before that release's own tag is cut. The release process cannot enforce this automatically; the owner applies it by hand.
- **Docs match code.** `go test ./internal/cli -run 'Docs|Lint'` and `gofmt -l .` are run after every task.

## Task 1: CI — sign the pushed base image by OCI digest

**Flag for opus review:** this task is the root of the new trust chain (design §2) — a mistake in *which* identity the signature is checked against, or signing on a trigger other than a tag push, silently reintroduces "whoever can write the ghcr.io tag" as the trust anchor while the design doc claims otherwise.

**Scope, narrowed from earlier drafts: base only, not history.** History's copy mechanism is entirely unchanged by this design (design §1, §6) — nothing ever verifies it, so signing it would serve no purpose. An earlier draft of this document signed and tagged `fugaro-history` too; dropped here.

**Starting state:** `.github/workflows/images.yml` already carries the real, merged `fugaro-base`/`publish-base` jobs (commit `356f7b1`); `docs/pending-workflows/` holds only `README.md`. This task's first step stages a *fresh* `docs/pending-workflows/images.yml` as the current real file plus this task's own addition, per `docs/pending-workflows/README.md`'s own instructions (Fugaro cannot push a commit touching `.github/workflows/**`).

**Files:**
- Add: `docs/pending-workflows/images.yml` (a fresh staged copy of the real file plus the additions below)
- Modify: `images/base_workflow_test.go` (new pinning tests below), `docs/pending-workflows/README.md` (its `## images.yml` section rewritten wholesale to describe this diff)

**Interfaces:**
- `publish-base`'s `permissions` gains `id-token: write` (alongside its existing `contents: read`/`packages: write`) — needed for both the OCI signing and the `-pinned` tag push, which are the same job, since OCI signing needs the same registry-write credential the push already has (there is no meaningful way to decouple signer from pusher for this mechanism, unlike an earlier, GitHub-Release-asset draft).
- A second, nightly-immune tag, `fugaro-base:<v>-pinned`, added to the **same** `docker buildx imagetools create` call that already creates `:<v>`/`:<major>`, conditioned on `github.event_name == 'push'` (the nightly/`workflow_dispatch` runs of the same script never create or move it, since the version-picking step treats them identically — always "the latest release tag," never a dispatched ref).
- `cosign sign --yes "ghcr.io/dimipaun/fugaro-base@$digest"`, signing the digest `docker buildx imagetools inspect` resolves from the just-created `-pinned` tag (immediately, same job, same concurrency-serialized run — safe for reasons distinct from, and narrower than, the severe nightly-vs-fresh-resolution race the `-pinned` tag itself exists to close, design §2).
- `sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2` — the exact, real pin `release.yml:95` already uses, not a placeholder.

- [ ] **Step 1: Write the failing tests**

```go
// TestPublishBaseSignsByOCIDigestOnPushOnly pins the signing shape (design
// retire-image-mirror.md §2): publish-base gains id-token: write, a
// cosign-installer step at the exact pin release.yml already uses, a
// -pinned tag added only on a real tag push, and a cosign sign step gated
// the same way.
func TestPublishBaseSignsByOCIDigestOnPushOnly(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string `yaml:"uses"`
				If   string `yaml:"if"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	const cosignPin = "sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6"
	job, ok := wf.Jobs["publish-base"]
	if !ok {
		t.Fatal("no publish-base job")
	}
	if job.Permissions["id-token"] != "write" {
		t.Errorf("permissions[id-token] = %q, want write", job.Permissions["id-token"])
	}
	var installed, taggedPinned, signed bool
	var pinnedGuarded, signGuarded bool
	for _, s := range job.Steps {
		if strings.HasPrefix(s.Uses, cosignPin) {
			installed = true
		}
		// The tag-creation step (identified by "imagetools create", the
		// call it's added to) and the sign step (identified by "cosign
		// sign ") BOTH mention "$VERSION-pinned" in their own run script
		// (the former creates it, the latter inspects it to resolve a
		// digest) — checking "Contains $VERSION-pinned" alone, across
		// every step, would let the sign step's own run (gated by a
		// separate, YAML-level if:, not this in-script check) overwrite
		// pinnedGuarded with a false negative on a later loop iteration,
		// exactly the kind of step-conflation bug an earlier draft of
		// this test had. Keyed on the distinguishing marker instead.
		if strings.Contains(s.Run, "imagetools create") {
			taggedPinned = strings.Contains(s.Run, "$VERSION-pinned")
			pinnedGuarded = strings.Contains(s.Run, `"$GITHUB_EVENT_NAME" = push`)
		}
		if strings.Contains(s.Run, "cosign sign ") {
			signed = true
			signGuarded = strings.Contains(s.If, "github.event_name == 'push'")
		}
	}
	if !installed || !taggedPinned || !signed {
		t.Errorf("cosign-installer=%v, -pinned tag=%v, cosign sign=%v", installed, taggedPinned, signed)
	}
	if !pinnedGuarded {
		t.Error("the -pinned tag addition must be guarded by $GITHUB_EVENT_NAME = push in its own shell script (a different gating mechanism from the sign step's YAML-level if:, since both live inside the same long-running script)")
	}
	if !signGuarded {
		t.Error("the cosign sign step's if: must require github.event_name == 'push'")
	}
}
```

(`TestOnlyPublishJobsHoldPackagesWrite`, the real, already-existing test in this file, needs no change — this task adds no new job and widens no job's `packages: write`.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run TestPublishBaseSignsByOCIDigestOnPushOnly -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Create `docs/pending-workflows/images.yml` by copying today's real `.github/workflows/images.yml` verbatim, then making these additions, all **inside `publish-base`'s existing "Push per architecture and publish the index" step** (one continuous shell script — a mistake an earlier draft of this document made was treating "define `$repo` here" and "use `$repo` in a signing step" as spanning two *separate* GitHub Actions steps, which do **not** share shell variables; everything below stays in the one script that already has `$repo`/`$VERSION`/`$MOVE_MAJOR` in scope):

```yaml
          tags="--tag $repo:$VERSION"
          if [ "$MOVE_MAJOR" = true ]; then
            tags="$tags --tag $repo:$MAJOR"
          fi
          # Nightly-immune: created only on a real tag push, in the SAME
          # create call as :$VERSION/:$MAJOR above, so the nightly's own
          # run of this same script never creates or moves it (design
          # retire-image-mirror.md §2).
          if [ "$GITHUB_EVENT_NAME" = push ]; then
            tags="$tags --tag $repo:$VERSION-pinned"
          fi
          # shellcheck disable=SC2086
          docker buildx imagetools create $tags $digests
          # ... (the existing size-summary block stays here, unchanged) ...
```

Then, as **two more steps in the same job** (after the step above, so `cosign` and the registry login from earlier steps are usable, but each as its own shell — neither references a variable only a prior step's script defined):

```yaml
      - uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2
        if: github.event_name == 'push'
      - name: Sign the base image
        if: github.event_name == 'push'
        env:
          VERSION: ${{ needs.version.outputs.version }}
        run: |
          set -eu
          repo=ghcr.io/dimipaun/fugaro-base
          # docker buildx imagetools create does not print the index's own
          # digest: the only way to learn it is to ask the registry what
          # the tag it just wrote now resolves to. Safe here (unlike
          # resolving the plain tag days or weeks later): this runs
          # immediately after the SAME job's own imagetools create call,
          # under a concurrency group (images-publish-base,
          # cancel-in-progress: false) that already serializes every run
          # touching this image.
          digest=$(docker buildx imagetools inspect "$repo:$VERSION-pinned" --format '{{.Manifest.Digest}}')
          cosign sign --yes "$repo@$digest"
```

`docs/pending-workflows/README.md`'s `## images.yml` section is replaced wholesale to describe this diff (the `-pinned` tag addition and the two new steps in `publish-base`) against the real, already-merged file.

**A cheap, recommended addendum, not required by this task:** extend `images/verify-public.sh`'s existing checks to also confirm `fugaro-base:<v>-pinned` is anonymously readable.

Run: `go test ./images/ -count=1`
Expected: PASS.

## Task 2: `internal/basepin` — resolve a tag and verify its OCI signature

**Flag for opus review:** this is new, security-critical code with no precedent in this repository; a bug that accepts an unverified or wrongly-identified signature as valid defeats the entire design. **Also flagged** because whether `sigstore-go`'s library API cleanly supports OCI-registry-attached signature verification is **unverified at design time** — the implementer confirms this before committing to the library route (design §9 Decision 1).

**Files:**
- Add: `internal/basepin/basepin.go`, `internal/basepin/basepin_test.go`
- Modify: `internal/mirror/mirror.go` (gains one small new exported function, `ResolveDigest`, reusing the package's existing, already-reviewed anonymous-registry-HTTP internals)
- Depends on (new go.mod entry, owner's call per design §9 Decision 1): either `github.com/sigstore/sigstore-go`'s `verify` package or shelling out to the real `cosign verify` binary.

**Interfaces:**

```go
// Package basepin resolves what a release's fugaro-base -pinned tag
// currently names in the registry and verifies that specific digest's
// OCI-attached cosign signature, from images.yml's own tag-push identity
// (design retire-image-mirror.md §2). It is called fresh, at every Cloud
// Build submission (internal/cli, Task 3) — nothing records its result
// anywhere.
package basepin

// TagResolver resolves what source+"/fugaro-"+kind+":"+version+"-pinned"
// currently names in the registry — the same anonymous, digest-verified
// read internal/mirror already does today, against the nightly-immune
// "-pinned" tag, never the plain version tag a nightly rebuild moves.
// kind is the bare base kind ("base" — history is out of scope, Task 1),
// NOT pre-prefixed with "fugaro-": the real implementation builds the
// full image name itself, the same way mirror.Dest's callers already do
// everywhere else in this codebase. (An earlier draft of this document
// had Resolve's caller pass an already-"fugaro-"-prefixed name AND had
// Resolve itself prepend "fugaro-" again, producing
// "ghcr.io/dimipaun/fugaro-fugaro-base" — fixed here by fixing the
// contract, not patching around it at the call site.)
type TagResolver interface {
	ResolveDigest(ctx context.Context, source, kind, version string) (digest string, err error)
}

// CertIdentity is what a signing certificate must name: the workflow ref
// GitHub's OIDC issues for a TAG PUSH specifically (Task 1's own event
// guard is exactly why this identity is the one to expect).
type CertIdentity struct {
	WorkflowRef string // "https://github.com/<owner>/<repo>/.github/workflows/images.yml@refs/tags/vX.Y.Z"
	Issuer      string // "https://token.actions.githubusercontent.com"
}

// OCIVerifier checks a specific digest's OCI-attached cosign signature
// against an identity. ref is "<source>/fugaro-<kind>@sha256:<hex>" — the
// verifier MUST pin its own artifact-digest policy to exactly this digest
// (never a "without artifact" / unsafe mode that accepts a signature for
// some other digest, design §9 Decision 1's "correctness beats convenience"
// framing). The real implementation wraps either sigstore-go's verify
// package or the cosign binary; tests use a fake.
type OCIVerifier interface {
	Verify(ctx context.Context, ref string, id CertIdentity) error
}

// Resolve resolves (via the "-pinned" tag), verifies and returns the
// pinned reference "<source>/fugaro-<kind>:<version>@<digest>" — the
// CLEAN version, no "-pinned" suffix anywhere in the returned string, so
// baseRefReleaseRE and every version-skew gate built on it keep parsing a
// plain "X.Y.Z" unmodified. owner/repoOnGitHub name the GitHub repository
// whose images.yml workflow is trusted (see "Forks and --image-source"
// below). Nothing is ever returned unverified.
func Resolve(ctx context.Context, tr TagResolver, v OCIVerifier, owner, repoOnGitHub, source, kind, version string) (string, error)
```

**Forks and `--image-source`.** `--image-source ghcr.io/<owner>` has always meant "a fork names its own source prefix" (`mirror.ValidSourcePrefix`, unchanged). The expected GitHub repository is `<owner>/fugaro` — the ghcr.io owner path segment of `--image-source`, reused as the GitHub owner, repository name assumed `fugaro` by convention. **A known, pre-existing limitation, not introduced by this task:** `--image-source` is a flag only, never saved to local config, so a fork's `fugaro image refresh` (which reads the local config, not flags, for its base-kind handling) would see its own custom `base_images.base` entry as "not a release image this fugaro verified" and hit `customBaseRefusal` unless `--image-source` is passed every time. Stated here, not fixed — out of scope for this plan.

**A subtlety worth one sentence (design §9 Decision 1, M3):** cosign's OCI signature binds to the **digest**, not to which repository name it is referenced under — so a digest signed as `fugaro-base`'s could, in principle, verify just as well if (by mistake or by copying bytes) the same digest were ever referenced under `fugaro-history`'s name instead. Low impact here, since `Resolve`'s own `ref` is always built from the caller's own, correctly-known `kind`, never read from anywhere an attacker could substitute it — but worth knowing if this code is ever reused more generally.

**Finding the signature (sigstore-go route, if chosen — design §9 Decision 1).** `sigstore-go` itself fetches nothing from a registry; the implementer must locate the signature artifact first. Under cosign v3 (paired with `cosign-installer` v4.x, Task 1's pin), a signature is stored either as an OCI *referrer* of the signed digest, or, where referrers aren't supported, under the fallback tag `sha256-<hex>.sig` in the same repository — the implementer checks which ghcr.io actually uses (not verified here) before writing the fetch. **The verifier must set an artifact-digest policy equal to the resolved digest and must never use a "without artifact" / unsafe verification mode** — accepting a signature with no artifact binding at all would defeat the entire point of digest-pinning.

**Network, beyond ghcr.io (design §2's "new operational cost").** Keyless verification needs the sigstore TUF trusted root and Rekor to be reachable from wherever `Resolve` runs (the operator's machine, or a CI runner doing `fugaro image refresh --yes`) — not only `ghcr.io` itself.

- [ ] **Step 1: Write the failing tests**

```go
package basepin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var sixtyFourHexC = strings.Repeat("c", 64)
var testDigest = "sha256:" + sixtyFourHexC // var, not const: a const cannot be built from strings.Repeat

type fakeResolver struct {
	digest string
	err    error
}

func (f fakeResolver) ResolveDigest(context.Context, string, string, string) (string, error) {
	return f.digest, f.err
}

type fakeVerifier struct {
	wantRef string
	wantID  CertIdentity
	err     error
}

func (f fakeVerifier) Verify(_ context.Context, ref string, id CertIdentity) error {
	if ref != f.wantRef || id != f.wantID {
		return errors.New("verify called with unexpected ref or identity")
	}
	return f.err
}

func wantIdentity(owner, repoOnGitHub, version string) CertIdentity {
	return CertIdentity{
		WorkflowRef: "https://github.com/" + owner + "/" + repoOnGitHub + "/.github/workflows/images.yml@refs/tags/v" + version,
		Issuer:      "https://token.actions.githubusercontent.com",
	}
}

func TestResolve(t *testing.T) {
	tr := fakeResolver{digest: testDigest}
	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	v := fakeVerifier{wantRef: ref, wantID: wantIdentity("dimipaun", "fugaro", "0.7.1")}
	// "base", not "fugaro-base": Resolve builds the full image name itself.
	got, err := Resolve(context.Background(), tr, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.1")
	want := "ghcr.io/dimipaun/fugaro-base:0.7.1@" + testDigest
	if err != nil || got != want {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
}

func TestResolveRefusesAFailedVerification(t *testing.T) {
	tr := fakeResolver{digest: testDigest}
	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	v := fakeVerifier{wantRef: ref, wantID: wantIdentity("dimipaun", "fugaro", "0.7.1"), err: errors.New("certificate identity mismatch")}
	if _, err := Resolve(context.Background(), tr, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.1"); err == nil || !strings.Contains(err.Error(), "certificate identity mismatch") {
		t.Errorf("got %v, want the injected verification error", err)
	}
}

func TestResolveRefusesAResolveError(t *testing.T) {
	tr := fakeResolver{err: errors.New("404")}
	if _, err := Resolve(context.Background(), tr, fakeVerifier{}, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.1"); err == nil {
		t.Error("want an error")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/basepin/... -count=1`
Expected: FAIL (package does not exist yet).

- [ ] **Step 3: Implement**

`Resolve` calls `tr.ResolveDigest(ctx, source, kind, version)`, builds `ref := source+"/fugaro-"+kind+"@"+digest` and the identity, calls `v.Verify(ctx, ref, id)`, and on success returns `source+"/fugaro-"+kind+":"+version+"@"+digest`.

`internal/mirror/mirror.go` gains:

```go
// ResolveDigest reads what ref's tag currently names, anonymously, reusing
// Plan's own first step (not reimplemented).
func (m *Mirror) ResolveDigest(ctx context.Context, ref Ref) (digest string, err error) {
	_, digest, _, err = m.getManifest(ctx, ref, ref.Tag)
	return digest, err
}
```

The real `TagResolver` (wired in Task 3) parses `source` (e.g. `"ghcr.io/dimipaun"`) into host and owner, and builds `mirror.Ref{Host: host, Repo: owner+"/fugaro-"+kind, Tag: version+"-pinned"}` — the owner comes from `source`, **never hard-coded** (an earlier draft of this document hard-coded `"dimipaun"` here, silently ignoring a fork's own `--image-source`). The real `OCIVerifier` is either `sigstore-go`'s image-verification API or a shelled-out `cosign verify --certificate-identity ... --certificate-oidc-issuer ... ref` (design §9 Decision 1).

Run: `go test ./internal/basepin/... ./internal/mirror/... -count=1`
Expected: PASS.

## Task 3: Build submissions resolve fresh; the existing copy gains a verification pre-step

**Flag for opus review:** this is where a build submission stops trusting a static config value and starts trusting a live, verified resolution; a code path that falls back to an unverified value on any error (instead of refusing) would silently reintroduce the weaker trust model the design is replacing it with.

**Scope, narrowed from three earlier drafts' worth of churn: `base_images.base` does not change shape, `managedVersion`/`isManaged` do not change signature, `internal/image/local.go`'s `BaseRef` does not change at all** (an earlier draft made `BaseRef` always refuse, which would have broken `fugaro image build --local` — `internal/cli/image.go:122`, a fourth, local-Docker-build caller distinct from the three Cloud Build submission callers, which the `fugaro:setup` skill loops on repeatedly during iterative development; `BaseRef`'s existing bare-tag fallback is exactly right for that caller and stays).

### Part A: the three Cloud Build callers resolve fresh

**Files:**
- Modify: `internal/cli/image.go` (the build-submission path, not the `--local` path), `internal/cli/init.go` (`buildImages`), `internal/cli/image_refresh_steps.go` (`refreshBuilds`)
- Modify tests: whichever of this plan's cited line ranges' existing test files cover these three functions today

**Interfaces:** each caller, instead of `base := lc.BaseImage(kind); if base == "" { base, err = image.BaseRef(kind, Version) }`, becomes: resolve `base` via `basepin.Resolve(ctx, tr, v, owner, repoOnGitHub, source, kind, releaseVersion())`, refusing cleanly (naming the real reason: no signed image for this release, a development build, or a verification failure) rather than falling back to anything. `source`/`owner`/`repoOnGitHub` come from the existing `--image-source` flag (or its default) and the fixed upstream repository, exactly as `internal/mirror`'s own `allow()` logic already resolves `--image-source` today.

- [ ] **Step 1: Write the failing tests**

(Illustrative shape — the implementer wires this to each of the three files' own existing test fixtures and mocking conventions, which this plan has not inspected in enough depth to sketch byte-exactly for all three call sites at once.)

```go
func TestImageBuildResolvesBaseFreshNotFromConfig(t *testing.T) {
	// A fake basepin.Resolve (injected the same way the file's existing
	// tests inject fakes for other dependencies) is called with the
	// release version, and its result — not any base_images.base value —
	// is what reaches the Cloud Build request's FUGARO_BASE substitution.
}

func TestImageBuildRefusesWhenResolveFails(t *testing.T) {
	// A failing fake basepin.Resolve means no Cloud Build is submitted at
	// all, with a clear, user-facing reason.
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestImageBuildResolvesBaseFreshNotFromConfig|TestImageBuildRefusesWhenResolveFails' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Replace each caller's `base_images.base`-then-`BaseRef`-fallback logic with a direct `basepin.Resolve` call, refusing on any error.

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

### Part B: a verification pre-step ahead of the existing copy

**Files:**
- Modify: `internal/cli/init_images.go` (the images stage's existing base-kind branch — `items()`, `record()`, `confirmReplace` — unchanged in structure, gains one new call before `mirror.Plan`), `internal/cli/image_refresh_rules.go` (`refreshBase`'s existing copy path, same addition)

**Interfaces:** before calling `mirror.Plan(ctx, src, dst, expect)` (the existing call, where `expect` was previously empty or `--expect-digest`'s value), call `basepin.Resolve` first; if it succeeds, its resolved digest becomes `expect` (overriding an absent `--expect-digest`, never a conflicting one — if `--expect-digest` was also given and disagrees, that is the existing mismatch refusal `mirror.Plan` already makes, unchanged). If `basepin.Resolve` fails, the stage refuses before attempting any copy, naming the real reason (no signed image yet, or a verification failure) rather than falling back to an unverified copy.

- [ ] **Step 1: Write the failing tests**

```go
func TestImagesStageVerifiesBeforeCopying(t *testing.T) {
	// A fake basepin.Resolve's result is asserted to be what mirror.Plan's
	// expect parameter receives, when --expect-digest was not given.
}

func TestImagesStageRefusesOnAFailedVerification(t *testing.T) {
	// A failing fake basepin.Resolve means no copy is even planned.
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestImagesStageVerifiesBeforeCopying|TestImagesStageRefusesOnAFailedVerification' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Add the `basepin.Resolve`-then-`mirror.Plan` sequencing described above.

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

## Task 4: CLI confirmation text — add "verified", don't remove "copies"

**Scope, narrowed:** an earlier draft of this task (then numbered 6) removed `--base`, rewrote `--expect-digest`'s meaning, and replaced "copies" wording with "verifies" wording throughout. None of that applies anymore: the copy mechanism is unchanged (design §6), so `--base`, `--expect-digest`, `--replace-image` all stay exactly as they are today, and the existing "copies N image(s), M MB … into your registry" confirmation text stays accurate — it still copies. The **only** change is adding one clause naming the new verification step that now runs first.

**Files:**
- Modify: `internal/cli/init_images.go`, `internal/cli/image_refresh_steps.go` (the confirmation strings Task 3 Part B's new pre-step precedes)

**Interfaces:** the existing confirmation text (e.g. *"copies N image(s), M MB … into your registry"*) gains a leading clause: *"verifies this release's signed digest, then copies N image(s), M MB … into your registry"*.

- [ ] **Step 1: Write the failing test**

```go
func TestImagesStageConfirmationNamesVerification(t *testing.T) {
	// The confirmation text for a base-kind copy contains both "verif" and
	// "copies" — neither replaces the other.
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/cli/ -run TestImagesStageConfirmationNamesVerification -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Add the clause.

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

## Task 5: Docs

**Scope, narrowed:** most of `docs/gcp-setup.md`, `docs/base-image.md` and `docs/base-image-migration.md`'s existing description of the copy mechanism stays accurate (it is unchanged). What needs updating is narrower than earlier drafts assumed.

**Files:**
- Modify: `docs/gcp-setup.md` §"The base image, in `fugaro-base`" (add one paragraph: Cloud Build's own `FUGARO_BASE` is now resolved and verified fresh at every build, not read from `base_images.base`; the copy itself, used for the daily check job, now verifies the signed digest first)
- Modify: `docs/release.md` (the `images.yml` section documents the new `publish-base` signing step and the `-pinned` tag; F1 marked done for the base image specifically)
- No change expected: `SECURITY.md`, `docs/base-image.md`'s "Bring your own" section (`--base-image` is untouched by this design)

**Acceptance:** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` passes.

- [ ] Draft each doc change, citing the design doc's wording.
- [ ] Run the docs/lint suite and `gofmt -l .`.

## Task 6: Full sweep

- [ ] `go build ./...`, `go vet ./...`
- [ ] `go test ./... -count=1`
- [ ] `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
- [ ] `gofmt -l .`
- [ ] Re-read `docs/design/retire-image-mirror.md` against the final code.

## Task 7 (owner-run; live checks, NOT RUN until the owner runs them)

Add to `docs/gcp-live-checklist.md`, in the existing numbered-table format, scoped to `fugaro`/`fugaro-dev`, sandbox repository only, never `belong`:

| # | Check | Command | Expected |
|---|---|---|---|
| 38a | The repository's own build service account (not the default compute SA) resolving and pulling the signed digest, both as a `docker pull` inside a step and as a step's own `name:` image (design retire-image-mirror.md §7, item a) | A one-off Cloud Build submitted as the sandbox repository's build account | Both steps succeed; record the elapsed time and the resolved digest as a `FACT`. |
| 38b | A full derived-image build with a freshly-resolved, digest-pinned `FROM`, end to end | `fugaro image build`/`init --repo` in the sandbox repository's checkout, after the implementation lands | `credential`, `render`, `build`, `smoke`, `gate`, `promote`, `record` all succeed; the build record's `base_ref` is the ghcr.io-pinned form. |
| 38c | OCI-signature verification of a real signed base image, on the operator's own machine | A build submission against a real release that has run Task 1's signing steps | The resolution succeeds and reports the verified digest; a deliberately mismatched expected identity is refused (tamper-test, done by hand once). |
| 38d | Egress/rate-limit behavior under load | — | **Out of scope for this design (§3); not planned.** |
| 38e | The nightly's own run genuinely never touches the `-pinned` tag — a GitHub Actions/ghcr.io check, unrelated to any GCP project | Manually dispatch `images.yml` by `workflow_dispatch` once (never a real tag push), and compare `fugaro-base:<v>-pinned`'s digest before and after | Unchanged; the plain tag's digest changes (today's existing nightly behavior), the `-pinned` tag's does not. A *re-run* of the tag-push workflow itself does move and re-sign `-pinned` again — harmless, not what this check tests. |
| 38f | The check job's own copy, with the new verification pre-step ahead of it, still deploys and runs correctly | A full `fugaro init --repo`/`image refresh` in the sandbox | The check job's container image (the Artifact-Registry copy, now verified-before-copy) deploys and runs unchanged from today. |

Number `38a`–`38f` follows the next free slot after the generic-tool plan's Check 38; the owner renumbers at integration time if another plan has since claimed 38.
