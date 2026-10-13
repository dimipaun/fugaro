# Implementation plan: retire the image mirror for a digest-pinned FROM

Source: [docs/design/retire-image-mirror.md](../design/retire-image-mirror.md). Depends on the base-image hard cut (`docs/plans/2026-10-08-base-image.md` Tasks 17–21, release 0.7.0) having already landed: `config.Bases == ["base"]`, one `base_images` key. **This plan is not executed in this change** (docs only); it is handed to the owner to rule on and, once approved, implemented task by task.

## Global constraints

- **No live cloud or network in any automated test.** Every fake (`Source`, `Verifier`, Terraform plans, Cloud Build requests) is in-process; the real cosign signature, the real Cloud Build pull paths and the real Terraform `--allow-delete` flow are the owner's live checks (Task 9), each marked NOT RUN until the owner runs it.
- **Never touch `belong`** (EdgeWeb's and EdgeServer's installation) in any live step. Every live check in Task 9 is scoped to `fugaro`/`fugaro-dev`, sandbox repository only, exactly as the existing entries of `docs/gcp-live-checklist.md` already are.
- **Nothing here weakens an existing guard.** `tf.Guard` still refuses every delete without `--allow-delete`; `Cover.checkSpecs` still refuses everything it refuses today, plus the one narrow addition Task 4 makes explicit and tests both ways (accepts the exact pinned shape, still refuses everything else, including a close-but-wrong one).
- **Each task is independently reviewable**: a task's tests fail before its implementation and pass after, with no task depending on a later one's code to compile (fakes stand in for what a later task provides).
- **Security-sensitive tasks are flagged for opus review** inline, with the specific claim a mistake would break.
- **Docs match code.** A doc describing a flag, command or config key this plan hasn't implemented yet is written as "proposed" until the task that implements it lands; `go test ./internal/cli -run 'Docs|Lint'` and `gofmt -l .` are run after every task.

## Task 1: The release's signed image-digest manifest (CI)

**Flag for opus review:** this task is the root of the new trust chain (design §2) — a mistake in *which* identity the signature is checked against, or in forgetting to remove the file from `docs/pending-workflows/README.md`'s exact-content pin, silently reintroduces "whoever can write the ghcr.io tag" as the trust anchor while the design doc claims otherwise.

**Files:**
- Modify: `docs/pending-workflows/images.yml` (adds a `sign-digests` step to the `publish-base` job, after the index is pushed)
- Add: `images/sign-digests.sh` (the shell logic, kept out of the YAML so it is testable and reviewable on its own)
- Modify: `images/base_workflow_test.go` (new pinning tests), `docs/pending-workflows/README.md` (documents the new step, the new file's exact-content pin)

**Interfaces:**
- `image-digests.txt`: one line per published reference, `<name> sha256:<hex>\n`, where `<name>` is `fugaro-base-amd64`, `fugaro-base-arm64`, `fugaro-base` (the index) and `fugaro-history`. No other content (no comments, no blank lines): the Go side (Task 2) parses it byte-exactly.
- `images/sign-digests.sh <manifest-path>`: writes `<manifest-path>.sigstore.json` next to it with `cosign sign-blob --yes --bundle "<manifest-path>.sigstore.json" "<manifest-path>"` (keyless, the same invocation shape `release.yml` already uses for `checksums.txt`, `docs/release.md` "Verifying a release"). Exits non-zero on any cosign failure; never swallows one.
- Both files are uploaded as release assets by the `publish-base` job: `gh release upload "vX.Y.Z" image-digests.txt image-digests.txt.sigstore.json`.

- [ ] **Step 1: Write the failing tests**

`images/base_workflow_test.go`:

```go
// TestPublishBaseSignsTheDigestManifest pins that publish-base writes and
// signs image-digests.txt after pushing the index, and uploads both the
// manifest and its sigstore bundle to the release (F1 for the base image,
// design retire-image-mirror.md §2): a build that pushes images but never
// signs a digest list would leave fugaro upgrade with nothing to verify.
func TestPublishBaseSignsTheDigestManifest(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["publish-base"]
	if !ok {
		t.Fatal("no publish-base job")
	}
	var sign, upload bool
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "images/sign-digests.sh") {
			sign = true
		}
		if strings.Contains(s.Run, "gh release upload") && strings.Contains(s.Run, "image-digests.txt") {
			upload = true
		}
	}
	if !sign || !upload {
		t.Errorf("publish-base: sign-digests.sh run = %v, gh release upload of image-digests.txt = %v", sign, upload)
	}
}

// TestSignDigestsScriptNeverIgnoresFailure: images/sign-digests.sh must not
// have an `|| true` or a bare `-` error suppression around the cosign call,
// or a signing failure would silently publish an unsigned (and therefore
// unverifiable) manifest.
func TestSignDigestsScriptNeverIgnoresFailure(t *testing.T) {
	data, err := os.ReadFile("sign-digests.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "set -eu") {
		t.Error("sign-digests.sh does not set -eu")
	}
	if strings.Contains(s, "cosign sign-blob") && (strings.Contains(s, "cosign sign-blob") && strings.Contains(s, "|| true")) {
		t.Error("sign-digests.sh ignores a cosign failure")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run 'TestPublishBaseSignsTheDigestManifest|TestSignDigestsScriptNeverIgnoresFailure' -count=1`
Expected: FAIL (no `publish-base` signing step, no `images/sign-digests.sh`).

- [ ] **Step 3: Implement**

`images/sign-digests.sh`:

```sh
#!/bin/sh
# Signs image-digests.txt keyless, the same mechanism release.yml already
# uses for checksums.txt (docs/release.md "Verifying a release"). Run from
# publish-base, after the index is pushed, with GitHub OIDC
# (id-token: write) already granted to the job.
set -eu
manifest="$1"
cosign sign-blob --yes --bundle "$manifest.sigstore.json" "$manifest"
```

`docs/pending-workflows/images.yml`'s `publish-base` job gains, after the `docker buildx imagetools create` step:

```yaml
      - name: Write the digest manifest
        run: |
          : > image-digests.txt
          printf 'fugaro-base-amd64 %s\n' "$(cat amd64-digest.txt)" >> image-digests.txt
          printf 'fugaro-base-arm64 %s\n' "$(cat arm64-digest.txt)" >> image-digests.txt
          printf 'fugaro-base %s\n' "$(docker buildx imagetools inspect "ghcr.io/dimipaun/fugaro-base:${{ needs.version.outputs.version }}" --format '{{.Manifest.Digest}}')" >> image-digests.txt
          printf 'fugaro-history %s\n' "$(cat history-digest.txt)" >> image-digests.txt
      - name: Sign the digest manifest
        run: sh images/sign-digests.sh image-digests.txt
        env: { COSIGN_EXPERIMENTAL: "1" }
      - name: Upload the digest manifest to the release
        run: gh release upload "v${{ needs.version.outputs.version }}" image-digests.txt image-digests.txt.sigstore.json
        env: { GH_TOKEN: "${{ github.token }}" }
```

(`id-token: write` is already a job permission needed for keyless cosign; `TestOnlyPublishJobsHoldPackagesWrite`-style review confirms no *new* job gains `packages: write` — this step runs inside the existing `publish-base` job, which already has it for the image pushes above it.)

`docs/pending-workflows/README.md` gains a bullet under `images.yml`'s diff summary describing the new step and widens its exact-content pin accordingly (the existing `TestReadWorkflowFallsBackOnlyWhilePending`-style tests are unaffected, since they test the fallback mechanism, not this file's content).

Run: `go test ./images/ -count=1`
Expected: PASS.

## Task 2: `internal/basepin` — resolve and verify a release's base digest

**Flag for opus review:** this is new, security-critical code with no precedent in this repository (nothing here has ever verified a sigstore bundle in Go before); a bug that accepts an unverified or wrongly-identified bundle as valid defeats the entire design.

**Files:**
- Add: `internal/basepin/basepin.go`, `internal/basepin/basepin_test.go`, `internal/basepin/parse.go`, `internal/basepin/parse_test.go`
- Depends on (new go.mod entry, owner's call per design §9 item 1): `github.com/sigstore/sigstore-go`'s `verify` package, vendored read-only (no signing capability compiled in).

**Interfaces:**

```go
// Package basepin resolves and verifies the digest a release's fugaro-base
// (or fugaro-history) image is pinned to, from the release's own signed
// image-digests.txt (design retire-image-mirror.md §2). It makes no
// decision about where the result is recorded; that is internal/cli's.
package basepin

// Digest is one parsed line of image-digests.txt.
type Digest struct {
	Name   string // "fugaro-base", "fugaro-base-amd64", "fugaro-base-arm64", "fugaro-history"
	SHA256 string // "sha256:" + 64 hex, lower-case
}

// ParseManifest parses image-digests.txt's exact line format ("<name>
// sha256:<hex>\n", nothing else); a malformed or duplicate-named line is
// refused, since a parser that silently drops or merges lines could hide a
// tampered one.
func ParseManifest(data []byte) ([]Digest, error)

// CertIdentity is what a bundle's signing certificate must name: the
// workflow ref GitHub's OIDC issues for, never release.yml's (a different
// workflow signs the digest manifest than signs checksums.txt).
type CertIdentity struct {
	WorkflowRef string // "https://github.com/<owner>/fugaro/.github/workflows/images.yml@refs/tags/vX.Y.Z"
	Issuer      string // "https://token.actions.githubusercontent.com"
}

// Verifier checks a cosign bundle against a blob and an identity. The real
// implementation wraps sigstore-go's verify package; tests use a fake that
// never touches the network or does real cryptography.
type Verifier interface {
	Verify(blob, bundle []byte, id CertIdentity) error
}

// Source fetches a release's image-digests.txt and its .sigstore.json
// bundle. The real implementation is an HTTP GET of the GitHub Release
// asset (the same release the running binary's own update check already
// reads, docs/release.md "Update checks"); tests use an in-memory fake.
type Source interface {
	Fetch(ctx context.Context, owner, repoOnGitHub, tag string) (manifest, bundle []byte, err error)
}

// Resolve fetches, verifies, parses and returns the pinned reference
// "<source>/<name>:<version>@<digest>" for name ("fugaro-base" or
// "fugaro-history") at this release's version. source is the configured
// image source (mirror.DefaultSources[0] or --image-source, unchanged
// meaning). An unsigned, wrongly-identified, unparseable or name-missing
// manifest is refused; nothing is ever returned unverified.
func Resolve(ctx context.Context, src Source, v Verifier, owner, repoOnGitHub, source, name, version string) (string, error)
```

- [ ] **Step 1: Write the failing tests**

`internal/basepin/parse_test.go`:

```go
package basepin

import "testing"

func TestParseManifest(t *testing.T) {
	data := []byte("fugaro-base-amd64 sha256:" + sixtyFourHex('a') + "\n" +
		"fugaro-base-arm64 sha256:" + sixtyFourHex('b') + "\n" +
		"fugaro-base sha256:" + sixtyFourHex('c') + "\n" +
		"fugaro-history sha256:" + sixtyFourHex('d') + "\n")
	got, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[2].Name != "fugaro-base" || got[2].SHA256 != "sha256:"+sixtyFourHex('c') {
		t.Errorf("got %+v", got)
	}
}

func TestParseManifestRefusesMalformed(t *testing.T) {
	for name, data := range map[string]string{
		"short digest":    "fugaro-base sha256:abcd\n",
		"no sha256 prefix": "fugaro-base " + sixtyFourHex('a') + "\n",
		"duplicate name":  "fugaro-base sha256:" + sixtyFourHex('a') + "\nfugaro-base sha256:" + sixtyFourHex('b') + "\n",
		"trailing junk":   "fugaro-base sha256:" + sixtyFourHex('a') + " extra\n",
		"empty":           "",
	} {
		if _, err := ParseManifest([]byte(data)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func sixtyFourHex(b byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = b
	}
	return string(out)
}
```

`internal/basepin/basepin_test.go`:

```go
package basepin

import (
	"context"
	"errors"
	"testing"
)

type fakeSource struct{ manifest, bundle []byte; err error }

func (f fakeSource) Fetch(context.Context, string, string, string) ([]byte, []byte, error) {
	return f.manifest, f.bundle, f.err
}

type fakeVerifier struct {
	wantID CertIdentity
	err    error
}

func (f fakeVerifier) Verify(blob, bundle []byte, id CertIdentity) error {
	if id != f.wantID {
		return errors.New("wrong identity checked")
	}
	return f.err
}

func validManifest() []byte {
	return []byte("fugaro-base sha256:" + sixtyFourHex('c') + "\nfugaro-history sha256:" + sixtyFourHex('d') + "\n")
}

func TestResolve(t *testing.T) {
	src := fakeSource{manifest: validManifest(), bundle: []byte("bundle")}
	wantID := CertIdentity{WorkflowRef: "https://github.com/dimipaun/fugaro/.github/workflows/images.yml@refs/tags/v0.7.1", Issuer: "https://token.actions.githubusercontent.com"}
	v := fakeVerifier{wantID: wantID}
	got, err := Resolve(context.Background(), src, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "fugaro-base", "0.7.1")
	want := "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + sixtyFourHex('c')
	if err != nil || got != want {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
}

func TestResolveRefusesAFailedVerification(t *testing.T) {
	src := fakeSource{manifest: validManifest(), bundle: []byte("bundle")}
	v := fakeVerifier{err: errors.New("certificate identity mismatch")}
	if _, err := Resolve(context.Background(), src, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "fugaro-base", "0.7.1"); err == nil {
		t.Error("want an error")
	}
}

func TestResolveRefusesAFetchError(t *testing.T) {
	src := fakeSource{err: errors.New("404")}
	if _, err := Resolve(context.Background(), src, fakeVerifier{}, "dimipaun", "fugaro", "ghcr.io/dimipaun", "fugaro-base", "0.7.1"); err == nil {
		t.Error("want an error")
	}
}

func TestResolveRefusesAMissingName(t *testing.T) {
	src := fakeSource{manifest: []byte("fugaro-history sha256:" + sixtyFourHex('d') + "\n"), bundle: []byte("bundle")}
	v := fakeVerifier{wantID: CertIdentity{WorkflowRef: "https://github.com/dimipaun/fugaro/.github/workflows/images.yml@refs/tags/v0.7.1", Issuer: "https://token.actions.githubusercontent.com"}}
	if _, err := Resolve(context.Background(), src, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "fugaro-base", "0.7.1"); err == nil {
		t.Error("want an error: fugaro-base is not in the manifest")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/basepin/... -count=1`
Expected: FAIL (package does not exist yet).

- [ ] **Step 3: Implement**

`Resolve` fetches via `src.Fetch`, verifies the manifest's bytes against the bundle with `v.Verify(manifest, bundle, CertIdentity{WorkflowRef: fmt.Sprintf("https://github.com/%s/%s/.github/workflows/images.yml@refs/tags/v%s", owner, repoOnGitHub, version), Issuer: "https://token.actions.githubusercontent.com"})` **before** parsing a single line (an unverified manifest is never parsed, let alone trusted), then `ParseManifest`s it and looks up `name`; a missing name is refused with the full list of names the manifest did have, for a clear error. The real `Source` (not under test here, wired in Task 3) is a plain HTTPS GET of `https://github.com/<owner>/<repo>/releases/download/v<version>/image-digests.txt` and its `.sigstore.json` sibling. The real `Verifier` wraps `sigstore-go`'s `verify.NewVerifier` keyless-GitHub-Actions trust root, checking the bundle's certificate `SourceRepositoryURI`/workflow ref and OIDC issuer against `CertIdentity` exactly, refusing anything the library itself flags (expired certificate, untrusted root, bundle/blob digest mismatch).

Run: `go test ./internal/basepin/... -count=1`
Expected: PASS.

## Task 3: Wire `basepin` into `init`/`image refresh`'s base step

**Flag for opus review:** this is where "copied" becomes "verified"; a code path that falls back to an unverified value (for example, reusing the old `image.BaseRef` tag-only fallback when `basepin.Resolve` errors, instead of refusing) would silently reintroduce the weaker trust model the design is replacing it with.

**Files:**
- Modify: `internal/cli/init_images.go` (delete the base-kind branch of `items()`, `managedVersion`/`isManaged`/`record()`'s base-kind path), `internal/cli/image_refresh_rules.go` (`refreshBase` resolves+verifies instead of building a `mirror.Dest`), `internal/cli/image_refresh_steps.go` (`refreshBase` step's confirmation text), `internal/image/local.go` (`BaseRef` either gains a verified form or is replaced by a thin call into `basepin` — the plan's implementer picks one, see note below)
- Modify tests: `internal/cli/init_images_test.go`, `internal/cli/image_refresh_rules_test.go`, `internal/cli/image_refresh_test.go`

**Interfaces:**
- `managedVersion(ref, source, kind string) (version string, ok bool)`: `source` replaces today's `host` parameter — a managed ref is now recognized by the **configured image source prefix** (`ghcr.io/dimipaun` or `--image-source`), not the installation's own registry host. `isManaged` follows the same signature change.
- `refreshBase(ctx context.Context, lc *localcfg.Config, kind, ver string, resolve basepinResolver) (string, error)`, where `basepinResolver` is a small func type (`func(ctx context.Context, name, version string) (string, error)`) the production code points at `basepin.Resolve` (with the real `Source`/`Verifier`) and tests point at a fake — keeping `internal/cli` free of a direct `sigstore-go` import, which stays inside `internal/basepin` alone.

- [ ] **Step 1: Write the failing tests**

`internal/cli/image_refresh_rules_test.go` (replacing the existing `TestRefreshBase`, which built `mirror.Dest` paths that no longer exist for `base`):

```go
func TestRefreshBaseResolvesAndVerifies(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"}
	resolved := "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)
	resolve := func(ctx context.Context, name, version string) (string, error) {
		if name != "fugaro-base" || version != "0.7.1" {
			t.Fatalf("resolve(%q, %q)", name, version)
		}
		return resolved, nil
	}
	got, err := refreshBase(context.Background(), lc, "base", "0.7.1", resolve)
	if err != nil || got != resolved {
		t.Errorf("got %q, %v; want %q", got, err, resolved)
	}
}

func TestRefreshBaseKeepsANewerManagedCopy(t *testing.T) {
	newer := "ghcr.io/dimipaun/fugaro-base:0.8.0@sha256:" + strings.Repeat("d", 64)
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5", BaseImages: map[string]string{"base": newer}}
	resolve := func(context.Context, string, string) (string, error) { t.Fatal("must not resolve: the existing copy is newer"); return "", nil }
	got, err := refreshBase(context.Background(), lc, "base", "0.7.1", resolve)
	if err != nil || got != newer {
		t.Errorf("got %q, %v; want the kept newer copy %q", got, err, newer)
	}
}

func TestRefreshBaseRefusesAnUnverifiedResolution(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"}
	resolve := func(context.Context, string, string) (string, error) { return "", errors.New("certificate identity mismatch") }
	if _, err := refreshBase(context.Background(), lc, "base", "0.7.1", resolve); err == nil {
		t.Error("want an error: resolve failed, nothing is recorded unverified")
	}
}

func TestIsManagedChecksTheImageSourceNotTheInstallationRegistry(t *testing.T) {
	ref := "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)
	if v, ok := managedVersion(ref, "ghcr.io/dimipaun", "base"); !ok || v != "0.7.1" {
		t.Errorf("managedVersion(%q) = %q, %v", ref, v, ok)
	}
	if _, ok := managedVersion(ref, "ghcr.io/someone-else", "base"); ok {
		t.Error("a ref under another owner must not be managed")
	}
	// The pre-migration, installation-registry-hosted shape stays
	// recognized too, so an old base_images.base entry is not treated as a
	// custom base (design retire-image-mirror.md §5).
	old := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.7.0"
	if v, ok := managedVersion(old, "ghcr.io/dimipaun", "base"); !ok || v != "0.7.0" {
		t.Errorf("an old copied ref must still be recognized: managedVersion(%q) = %q, %v", old, v, ok)
	}
}
```

`internal/cli/init_images_test.go` gains a case proving the images stage's `items()` no longer lists any `base`-kind item at all (only `history`, under `--firebase`), and that `--base` is refused or ignored for `base` with a message naming `fugaro image refresh` instead.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestRefreshBase|TestIsManaged' -count=1`
Expected: FAIL (old signatures, old `mirror.Dest`-based implementation).

- [ ] **Step 3: Implement**

`refreshBase` becomes:

```go
// refreshBase is the base image kind's ref once the base step has run:
// this release's verified pin, or a newer managed copy an earlier CLI
// already recorded (never downgraded). resolve is basepin.Resolve, wired
// by the caller with the real Source and Verifier; tests supply a fake.
func refreshBase(ctx context.Context, lc *localcfg.Config, kind, ver string, resolve func(ctx context.Context, name, version string) (string, error)) (string, error) {
	if !releaseRE.MatchString(ver) {
		return "", userErr("fugaro image refresh needs a release build of fugaro: %q is not a release version", ver)
	}
	source := imageSourcePrefix() // mirror.DefaultSources[0] or --image-source, unchanged
	cur := lc.BaseImage(kind)
	if cur != "" && !isManaged(cur, source, kind) {
		return "", userErr("base_images.%s is %s: not a release image fugaro verified, and fugaro image refresh never replaces one", kind, pluginwire.Printable(cur))
	}
	if cur != "" && newer(curVersion(cur, source, kind), ver) {
		return cur, nil // a newer managed copy is kept; nothing is re-resolved
	}
	return resolve(ctx, "fugaro-"+kind, ver)
}
```

`managedVersion`/`isManaged` change their second parameter from the installation's registry host to the configured source prefix, and their shared-prefix check (`strings.CutPrefix(ref, host+"/"+mirror.BaseRepository+"/fugaro-"+kind+":")`) becomes two alternatives tried in order: the new `source+"/fugaro-"+kind+":"` prefix (ghcr.io-shaped, digest-and-tag), and the old `<install registry>/`+`mirror.BaseRepository`+`/fugaro-`+kind+`:` prefix kept **only** for recognizing a pre-migration entry as still-managed (never newly written again) — exactly the backward-compatible case `TestIsManagedChecksTheImageSourceNotTheInstallationRegistry`'s last assertion pins.

`internal/image/local.go`'s `BaseRef(base, version string) (string, error)` — used today as an **offline, no-network** fallback wherever `base_images` has no entry yet (`refreshReload`, `image.go`, `init.go`, `image_layer.go`) — keeps that exact offline contract and its callers unchanged; it is not where verification happens (verification happens once, at `image refresh`/`init --base`/`upgrade` time, and is **recorded** into `base_images.base`). Those callers already read `base_images.base` first and fall back to `BaseRef` only when it is unset, so after one successful `image refresh` the verified, digest-pinned value is what every caller actually uses; `BaseRef`'s bare-tag fallback is reached only for a repository that has never run `image refresh` at all, in which case it already refuses to submit a build in some callers and prints "no published base image yet" in others — unchanged by this task, since fixing that corner is exactly what running `image refresh` once does.

Run: `go test ./internal/cli/... ./internal/image/... -count=1`
Expected: PASS.

## Task 4: `Cover.checkSpecs`'s narrow allowance for a pinned external base

**Flag for opus review:** `cover.go` is the one piece of code that decides what "nothing is applied unseen" silently covers; a loose match here (anything under `ghcr.io/dimipaun`, or anything with an `@sha256:`) would let a hand-edited `base_images.base` naming an attacker's own image sail through the single confirmation instead of asking its own.

**Files:**
- Modify: `internal/infra/tf/cover.go` (`checkSpecs`'s `inRegistry` gains a second check), `internal/infra/tf/cover_test.go`

**Interfaces:**
- `Cover` gains a field, `ImageSource string` (the configured `mirror.DefaultSources[0]`/`--image-source`, passed in by the caller exactly as `Registry`/`Projects` already are), used only to build the second allowed prefix.
- `inRegistry`'s replacement, `coveredBaseImage(ref string) bool`, used **only** for `s.BaseImages` entries (never for `s.Registry` or a build service account, which keep today's `inRegistry` exactly): `inRegistry(ref) || isPinnedSource(ref)`, where `isPinnedSource` requires an **exact** `<c.ImageSource>/fugaro-<kind>:<X.Y.Z>@sha256:<64 hex>` match (reusing the `baseRefReleaseRE`-shaped parse, kind one of `config.Bases`) — never a prefix match alone, never a bare tag, never missing the digest.

- [ ] **Step 1: Write the failing tests**

`internal/infra/tf/cover_test.go` (added alongside the existing table in `TestCheckSpecs`-style tests, using the same `spec(...)`/`jobWithSpec(...)` helpers already in the file):

```go
"pinned external base is covered": {
	jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:"+strings.Repeat("c", 64)), false), true,
},
"external base with no digest is NOT covered": {
	jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-base:0.7.1"), false), false,
},
"external base under another owner is NOT covered": {
	jobWithSpec(spec(reg+"r", "ghcr.io/someone-else/fugaro-base:0.7.1@sha256:"+strings.Repeat("c", 64)), false), false,
},
"external base naming another kind is NOT covered": {
	jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-other:0.7.1@sha256:"+strings.Repeat("c", 64)), false), false,
},
```

(`reg` and `spec`/`jobWithSpec` are the file's existing fixtures; the `Cover` value the table's test constructs gains `ImageSource: "ghcr.io/dimipaun"`.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/tf/ -run TestCheckSpecs -count=1`
Expected: FAIL (`inRegistry` alone refuses every one of the new "covered" cases, and `Cover` has no `ImageSource` field yet).

- [ ] **Step 3: Implement**

```go
// pinnedSourceRE is an exact pinned external base: the configured source,
// /fugaro-<kind>, :X.Y.Z, @sha256:<hex>, nothing else (design
// retire-image-mirror.md §2's cover.go allowance: digest mandatory, no
// prefix-only match).
var pinnedSourceRE = regexp.MustCompile(`:([0-9]+\.[0-9]+\.[0-9]+)@sha256:[0-9a-f]{64}$`)

func (c Cover) coveredBaseImage(ref string, inRegistry func(string) bool) bool {
	if inRegistry(ref) {
		return true
	}
	if c.ImageSource == "" {
		return false
	}
	for _, kind := range config.Bases {
		if prefix := c.ImageSource + "/fugaro-" + kind; strings.HasPrefix(ref, prefix) && pinnedSourceRE.MatchString(strings.TrimPrefix(ref, prefix)) {
			return true
		}
	}
	return false
}
```

`checkSpecs`'s `s.BaseImages` loop calls `c.coveredBaseImage(ref, inRegistry)` instead of `inRegistry(ref)` directly; `s.Registry` and the build-service-account check are untouched.

Run: `go test ./internal/infra/tf/... -count=1`
Expected: PASS.

## Task 5: Terraform — remove the build account's `fugaro-base` reader grant

**Files:**
- Modify: `deploy/terraform/gcp/modules/repo/build.tf` (delete `google_artifact_registry_repository_iam_member.build_base`)
- Regenerate: `internal/infra/tf/testdata/golden/repo*.plan.json` (`scripts/gen-golden-plans.sh`, per the base-image plan's own Task 19 precedent)
- Modify: `docs/base-image-migration.md` (a new, short section: the one-time `--allow-delete` an operator sees on the first `fugaro init --repo` after upgrading)

**Interfaces:** none new; this is a resource deletion plus the golden-plan regeneration its removal requires, and a doc paragraph.

- [ ] **Step 1: Write the failing test**

A golden-plan test already exists (`internal/infra/tf/testdata/golden/repo*.plan.json`-driven tests in this package); add one assertion that the regenerated golden plan's resource list no longer contains `google_artifact_registry_repository_iam_member.build_base`, so a future accidental re-add is caught:

```go
func TestRepoPlanNoLongerGrantsBuildBaseRead(t *testing.T) {
	for _, addr := range goldenRepoPlanAddresses(t) { // existing test helper reading the golden file(s)
		if strings.Contains(addr, "build_base") {
			t.Errorf("golden plan still has %s: the build account no longer reads fugaro-base (base images are pulled directly, design retire-image-mirror.md §4)", addr)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/infra/tf/ -run TestRepoPlanNoLongerGrantsBuildBaseRead -count=1`
Expected: FAIL (the current golden plan still has the grant).

- [ ] **Step 3: Implement**

Delete the `google_artifact_registry_repository_iam_member.build_base` resource block from `deploy/terraform/gcp/modules/repo/build.tf`; run `scripts/gen-golden-plans.sh` to regenerate the golden JSON fixtures against a fake provider (no live Terraform apply — the existing script already works this way for every other golden-plan change in this repository).

`docs/base-image-migration.md` gains, under "Everywhere":

> The first `fugaro init --repo` (or whole-installation `fugaro init`) you run after upgrading past this change shows a `delete` of `google_artifact_registry_repository_iam_member.build_base["<repo>"]` (the build account's now-unneeded read grant on `fugaro-base`) and refuses until you pass the exact flag it prints: `--allow-delete 'google_artifact_registry_repository_iam_member.build_base["<repo>"]'`. This is `tf.Guard` doing its job — confirm it once per repository, the same way any other intentional Terraform removal needs `--allow-delete`.

Run: `go test ./internal/infra/tf/... -count=1`
Expected: PASS.

## Task 6: CLI surface cleanup — flags, help text, dead confirmations

**Files:**
- Modify: `internal/cli/init.go` (`--base`'s help text and its base-kind-copying behavior removed; `--expect-digest`/`--replace-image` help text updated), `internal/cli/image_refresh.go` (`newImageRefreshCmd`'s `Long` text), `internal/cli/init_images.go` (`confirmReplace`, `sourceLine`, `reviewLine` lose their base-kind branches)
- Modify tests: `internal/cli/init_test.go`, `internal/cli/image_refresh_test.go` (any case asserting a base-kind copy's confirmation text or `--replace-image base` behavior is rewritten to assert the new verify-and-pin confirmation text instead)

**Interfaces:**
- `--base` (init): no longer copies anything for `base`; either removed outright (history's own copying is driven by `--firebase`, not `--base`) or kept as a no-op with a clear message pointing at `fugaro image refresh`. **Recommendation, for the owner:** remove it — a flag that does nothing for every value it used to accept is worse than no flag, and nothing in this plan's earlier tasks needs it to exist.
- `--expect-digest KIND=sha256:<hex>`: unchanged surface, new meaning for `base` — checked against `basepin.Resolve`'s own result (an extra equality check, refusing a mismatch) rather than against a copy's digest.
- `--replace-image KIND`: `base`'s meaning removed (help text drops it from the list); `history` unchanged.

- [ ] **Step 1: Write the failing tests**

`internal/cli/init_test.go`:

```go
func TestBaseFlagNoLongerCopiesAnything(t *testing.T) {
	// --base base must either be refused or be a documented no-op naming
	// fugaro image refresh; it must never attempt a copy (no mirror.Mirror
	// is constructed, so a test fake that fails on any mirror call proves
	// this even without a network).
	out, err := executeInitPlanOnly(t, "--base", "base") // existing test helper
	if err != nil || strings.Contains(out, "copies") {
		t.Errorf("out=%q err=%v; --base base must not describe a copy", out, err)
	}
}
```

`internal/cli/image_refresh_test.go` gains/updates a case asserting the base step's confirmation text (`r.confirmOrdinary`'s message in `refreshBase`/`image_refresh_steps.go`) names "verified" and a digest, never "copies" and a megabyte figure, for the `base` kind.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestBaseFlagNoLongerCopiesAnything|TestImageRefresh' -count=1`
Expected: FAIL (today's text still says "copies").

- [ ] **Step 3: Implement**

Remove `--base`'s base-kind-copying code path (`init.go`'s flag stays only if the owner's decision in Task's "Interfaces" note above is to keep it as a no-op; delete it otherwise, per the recommendation). Rewrite every confirmation and review-line string in `init_images.go`/`image_refresh_steps.go` that currently says "copies N image(s), M MB … into your registry" to instead say "verifies this release's base image against its signed digest list and records \<ref\>" (exact wording is the implementer's; the test only pins that "copies"/a byte count never appears for `base` and that "verif(y/ies/ied)" and the digest do).

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

## Task 7: Docs

**Files:**
- Modify: `docs/base-image.md` ("Building it locally", "Bring your own": the base pull is now direct and digest-pinned, not a copy; `--base base` no longer copies)
- Modify: `docs/base-image-migration.md` (Task 5's `--allow-delete` paragraph lands here too if not already, plus a short "what used to be a copy is now a verified pin" note)
- Modify: `docs/gcp-setup.md` §"The base image, in `fugaro-base`" (rewritten: no more megabytes-through-your-machine language; the confirmation now names a digest; `fugaro-base` the registry repository is called out as kept-but-dormant for `base`, still live for `history`)
- Modify: `docs/release.md` (the `images.yml` section documents the new `publish-base` signing step and the `image-digests.txt`/`.sigstore.json` release assets; the "How `fugaro init` uses the images (the mirror)" section is split: history keeps today's wording, base gets the new verify-and-pin wording; F1 marked done for the base image specifically, still open for anything else cosign might eventually cover)
- No change expected: `SECURITY.md` (its trust-model section already describes the container as the boundary and does not name the mirror specifically; confirm with a careful read rather than edit speculatively)

**Acceptance:** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` passes (every documented flag/command/key this task's wording references must already exist in code by this point in the task order, or be explicitly marked "proposed").

- [ ] Draft each doc change, citing the design doc's wording where it already states the user-facing behavior (§2, §3, §6), so the docs and the design never drift.
- [ ] Run the docs/lint suite and `gofmt -l .`; fix anything it flags.

## Task 8: Full sweep

- [ ] `go build ./...`, `go vet ./...`
- [ ] `go test ./... -count=1` (every package this plan touched, plus a full run to catch an unrelated regression)
- [ ] `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
- [ ] `gofmt -l .`
- [ ] Re-read `docs/design/retire-image-mirror.md` against the final code and update anything that drifted during implementation (a design doc that disagrees with the code it describes is worse than no design doc).

## Task 9 (owner-run; live checks, NOT RUN until the owner runs them)

Add to `docs/gcp-live-checklist.md`, in the existing numbered-table format, scoped to `fugaro`/`fugaro-dev`, sandbox repository only, never `belong`:

| # | Check | Command | Expected |
|---|---|---|---|
| 38a | The repository's own build service account (not the default compute SA) pulling the pinned base, both as a `docker pull` inside a step and as a step's own `name:` image (design retire-image-mirror.md §7, item a) | A one-off Cloud Build submitted as the sandbox repository's build account, one step `docker pull <pinned ref>`, one step `name: <pinned ref>` running a trivial command | Both steps succeed; record the elapsed time and the resolved digest as a `FACT`. |
| 38b | A full derived-image build with the digest-pinned `FROM`, end to end | `fugaro image refresh` in the sandbox repository's checkout, after the implementation lands | `credential`, `render`, `build`, `smoke`, `gate`, `promote`, `record` all succeed; the build record's `base_ref` is the ghcr.io-pinned form. |
| 38c | Cosign verification of a real signed `image-digests.txt`, on the operator's own machine | `fugaro upgrade` (or `fugaro init --base`/`image refresh`) against a real release that has run Task 1's signing step | The CLI reports the verified digest; a deliberately corrupted local copy of the bundle is refused (tamper-test, done by hand once). |
| 38d | Egress/rate-limit behavior under load | — | **Out of scope for this design (§3); not planned.** |

Number `38a`–`38d` follows the next free slot after the generic-tool plan's Check 38 (`docs/gcp-live-checklist.md`'s own out-of-order note already reserves numbers this way); the owner renumbers at integration time if another plan has since claimed 38.
