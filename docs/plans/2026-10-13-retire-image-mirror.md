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

**Flag for opus review:** this task is the root of the new trust chain (design §2) — a mistake in *which* identity the signature is checked against, or in forgetting to remove the staged file once it is copied in, silently reintroduces "whoever can write the ghcr.io tag" as the trust anchor while the design doc claims otherwise.

**Starting state, checked 2026-10-13 (see the design doc's note on current repository state):** `.github/workflows/images.yml` already carries the real, merged `fugaro-base`/`publish-base` jobs (commit `356f7b1`, already an ancestor of this branch); `docs/pending-workflows/` holds only `README.md`, and that file's own `## images.yml` section still describes the now-merged base-image diff rather than "nothing pending" — it is itself stale and gets replaced wholesale by this task, not amended. **This task's first step is therefore not "modify the pending file" (there isn't one) but "stage a fresh one": create `docs/pending-workflows/images.yml` as the current real `.github/workflows/images.yml`'s content plus this task's own addition**, exactly as `docs/pending-workflows/README.md`'s own instructions describe for any workflow change Fugaro needs to make (Fugaro cannot push a commit touching `.github/workflows/**`, so the new step can only ever reach the real file by a human copying this staged one in, the same `cp`/`git rm` sequence the README already documents).

**The real data flow, checked against the real `.github/workflows/images.yml` (not the earlier draft's sketch, which invented files — `amd64-digest.txt`, `arm64-digest.txt`, `history-digest.txt` — that nothing writes today):**

- `publish-base` resolves each architecture's digest into a **local shell variable** (`digests="$digests $(docker image inspect …)"`), used only to build the `docker buildx imagetools create` command line, and never written anywhere; it holds **only** `contents: read` and `packages: write` (`.github/workflows/images.yml` ~L266-268) — the job that needs `packages: write` to push images.
- `fugaro-history` is published separately, by the **`publish`** job's `history` leg of its `[web-node, go, java-services, history]` matrix (~L324, also `packages: write`), which tags and pushes by version tag alone and resolves no digest at all today.
- Neither job may gain `id-token: write` (keyless cosign's requirement) **in addition to** `packages: write`: a job that can both push images and sign would be a strictly more powerful credential than either capability needs alone, and the design's own "do not widen the job that holds `packages: write`" constraint rules it out.

**The fix: a new, separate job, `sign-base-digests`, downstream of both, holding neither `packages: write`.** `publish-base` and `publish`'s `history` leg each gain one extra step that writes their own already-computed (or newly resolved) digest to a file and uploads it as a small artifact; `sign-base-digests` downloads both artifacts, builds `image-digests.txt` from their contents, installs cosign, signs, and uploads to the release. Its own permissions are **exactly** `id-token: write` (keyless signing) and `contents: write` (`gh release upload`, the same pair `docs/release.md` already documents for `release.yml`'s own cosign step) — no `packages: write` at all, since it never touches the registry.

**Files:**
- Add: `docs/pending-workflows/images.yml` (a fresh staged copy: today's real `.github/workflows/images.yml`, plus the digest-writing steps in `publish-base` and `publish`'s `history` leg and the new `sign-base-digests` job described below — not a diff against an existing pending file, since none exists)
- Add: `images/sign-digests.sh` (the shell logic, kept out of the YAML so it is testable and reviewable on its own)
- Modify: `images/base_workflow_test.go` (new pinning tests — these already read whichever of the pending or real file exists via `readImagesWorkflow`, so they exercise the newly staged file immediately, with no change to that fallback mechanism), `docs/pending-workflows/README.md` (its `## images.yml` section is rewritten to describe *this* task's diff against the real file, replacing the stale description of the already-merged base-image diff; the general "Copying a file in" instructions above it are unaffected)

**Interfaces:**
- `image-digests.txt`: one line per published reference, `<name> sha256:<hex>\n`, where `<name>` is `fugaro-base-amd64`, `fugaro-base-arm64`, `fugaro-base` (the index) and `fugaro-history`. No other content (no comments, no blank lines): the Go side (Task 2) parses it byte-exactly. A tree that published no `history` (an older kind list) omits that line, the same optional-presence handling `images/verify-public.sh`'s own `case "$KINDS" in *",$img,"*` already uses — the implementer follows that existing pattern rather than assuming `history` is always present.
- `images/sign-digests.sh <manifest-path>`: writes `<manifest-path>.sigstore.json` next to it with `cosign sign-blob --yes --bundle "<manifest-path>.sigstore.json" "<manifest-path>"` (keyless; `cosign` itself must already be on `PATH`, installed by the calling job — the script does not install it). Exits non-zero on any cosign failure; never swallows one.
- Both files are uploaded as release assets by the new `sign-base-digests` job: `gh release upload "vX.Y.Z" image-digests.txt image-digests.txt.sigstore.json --clobber` (`--clobber` so a re-run of the job after a transient failure doesn't fail on an asset it already half-uploaded).
- **The release-existence race.** `release.yml` and `images.yml` are two independent workflows triggered by the same tag push (`.github/workflows/release.yml`'s own header comment already says so); nothing orders them, and `gh release upload` fails outright if the GitHub Release doesn't exist yet. `sign-base-digests` **waits and retries** `gh release view "vX.Y.Z"` (the same check `release.yml` itself already uses at ~L128 to decide whether a tag's release exists) before ever attempting the upload, bounded (for example 30 tries, 20s apart, under the job's own `timeout-minutes`) rather than assuming an order; it does **not** create the release itself, since `release.yml` alone owns that (the correct Highlights-based body, the changelog) and a second creator would risk a conflicting or empty-bodied release.

- [ ] **Step 1: Write the failing tests**

`images/base_workflow_test.go`:

```go
// TestSignBaseDigestsJobIsSeparateFromPackagesWrite pins the permission
// split this task depends on (design retire-image-mirror.md §2, the "do not
// widen packages: write" constraint): publish-base and publish keep their
// today-only permissions (no id-token: write added to either), and the new
// sign-base-digests job holds id-token: write and contents: write and
// NEVER packages: write. A job that could both push images and sign would
// be strictly more powerful than either capability needs alone.
func TestSignBaseDigestsJobIsSeparateFromPackagesWrite(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Needs       []string          `yaml:"needs"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"publish-base", "publish"} {
		job, ok := wf.Jobs[name]
		if !ok {
			t.Fatalf("no %s job", name)
		}
		if job.Permissions["id-token"] == "write" {
			t.Errorf("%s must not gain id-token: write (it already holds packages: write)", name)
		}
	}
	sign, ok := wf.Jobs["sign-base-digests"]
	if !ok {
		t.Fatal("no sign-base-digests job")
	}
	if sign.Permissions["id-token"] != "write" || sign.Permissions["contents"] != "write" {
		t.Errorf("sign-base-digests permissions = %+v, want id-token: write, contents: write", sign.Permissions)
	}
	if _, ok := sign.Permissions["packages"]; ok {
		t.Error("sign-base-digests must not hold packages permission at all")
	}
	if !slices.Contains(sign.Needs, "publish-base") || !slices.Contains(sign.Needs, "publish") {
		t.Errorf("sign-base-digests.needs = %v, want publish-base and publish (it downloads their digest artifacts)", sign.Needs)
	}
}

// TestOnlyPublishJobsHoldPackagesWrite (existing test, extended): the new
// sign-base-digests job must not appear in the packages:write allowlist.
func TestOnlyPublishJobsHoldPackagesWrite(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for name, job := range wf.Jobs {
		write := job.Permissions["packages"] == "write"
		wantWrite := name == "publish" || name == "publish-base"
		if write != wantWrite {
			t.Errorf("job %q packages permission = %q, want write only for publish/publish-base", name, job.Permissions["packages"])
		}
	}
}

// TestSignBaseDigestsWaitsForTheRelease pins that the upload step polls for
// the release rather than assuming release.yml already created it (the two
// workflows are independent, triggered by the same tag push).
func TestSignBaseDigestsWaitsForTheRelease(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Steps []struct{ Run string `yaml:"run"` } `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["sign-base-digests"]
	if !ok {
		t.Fatal("no sign-base-digests job")
	}
	var waits, uploads bool
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "gh release view") {
			waits = true
		}
		if strings.Contains(s.Run, "gh release upload") && strings.Contains(s.Run, "image-digests.txt") {
			uploads = true
		}
	}
	if !waits || !uploads {
		t.Errorf("sign-base-digests: waits for the release = %v, uploads image-digests.txt = %v", waits, uploads)
	}
}

// TestSignDigestsScriptNeverIgnoresFailure: images/sign-digests.sh must
// never suppress a cosign failure (a swallowed error would silently publish
// an unsigned, and so unverifiable, manifest).
func TestSignDigestsScriptNeverIgnoresFailure(t *testing.T) {
	data, err := os.ReadFile("sign-digests.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "set -eu") {
		t.Error("sign-digests.sh does not set -eu")
	}
	for _, bad := range []string{"|| true", "|| :", "2>/dev/null"} {
		if strings.Contains(s, bad) {
			t.Errorf("sign-digests.sh ignores a failure (contains %q)", bad)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run 'TestSignBaseDigestsJobIsSeparateFromPackagesWrite|TestOnlyPublishJobsHoldPackagesWrite|TestSignBaseDigestsWaitsForTheRelease|TestSignDigestsScriptNeverIgnoresFailure' -count=1`
Expected: FAIL (no `sign-base-digests` job, no `images/sign-digests.sh`).

- [ ] **Step 3: Implement**

`images/sign-digests.sh`:

```sh
#!/bin/sh
# Signs image-digests.txt keyless, the same mechanism release.yml already
# uses for checksums.txt (docs/release.md "Verifying a release"). Run from
# sign-base-digests, which already installed cosign and holds GitHub OIDC
# (id-token: write); this script does not install cosign itself.
set -eu
manifest="$1"
cosign sign-blob --yes --bundle "$manifest.sigstore.json" "$manifest"
```

Create `docs/pending-workflows/images.yml` by copying today's real `.github/workflows/images.yml` verbatim, then making these additions (no job's existing permissions change):

In `publish-base`'s "Push per architecture and publish the index" step (the one that already builds `$digests` in its per-architecture loop), write each architecture's digest to a file as it's computed, and the index's digest once `docker buildx imagetools create` has run, then upload them as an artifact:

```yaml
            digests="$digests $(docker image inspect --format '{{index .RepoDigests 0}}' "$repo:$VERSION-$arch")"
            docker image inspect --format '{{index .RepoDigests 0}}' "$repo:$VERSION-$arch" | sed 's/.*@//' > "digest-$arch.txt"
            docker image rm fugaro-base:ci "$repo:$VERSION-$arch" >/dev/null
          done
          # … (existing tags/imagetools create block, unchanged) …
          docker buildx imagetools inspect "$repo:$VERSION" --format '{{.Manifest.Digest}}' > digest-index.txt
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: fugaro-base-digests
          path: digest-*.txt
          retention-days: 1
```

In `publish`'s "Load and publish" step, only for the `history` leg (`if: matrix.base == 'history' && contains(needs.version.outputs.kinds, ',history,')`), resolve and upload its digest the same way:

```yaml
      - name: Resolve the history digest
        if: matrix.base == 'history' && contains(needs.version.outputs.kinds, ',history,')
        run: docker buildx imagetools inspect "ghcr.io/dimipaun/fugaro-history:${{ needs.version.outputs.version }}" --format '{{.Manifest.Digest}}' > digest-history.txt
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        if: matrix.base == 'history' && contains(needs.version.outputs.kinds, ',history,')
        with:
          name: fugaro-history-digest
          path: digest-history.txt
          retention-days: 1
```

New job, after `publish-base`:

```yaml
  # Signs the base image's digests for fugaro upgrade/init/image refresh to
  # verify (design retire-image-mirror.md §2, F1 for the base image). Holds
  # id-token: write (keyless cosign) and contents: write (gh release
  # upload), and deliberately NEVER packages: write — it never touches the
  # registry, so it must not be folded into publish-base or publish, which
  # already hold packages: write for the image pushes above.
  sign-base-digests:
    needs: [version, publish-base, publish]
    if: needs.version.outputs.publish == 'true' && contains(needs.version.outputs.kinds, ',base,')
    permissions:
      id-token: write
      contents: write
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with: { name: fugaro-base-digests }
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        if: contains(needs.version.outputs.kinds, ',history,')
        with: { name: fugaro-history-digest }
      - name: Build the digest manifest
        run: |
          set -eu
          # digest-*.txt already hold "sha256:<hex>" (sed 's/.*@//' on
          # RepoDigests, and imagetools inspect's own --format, both keep
          # the algorithm prefix) — %s alone, not "sha256:%s", or the
          # manifest would read "fugaro-base-amd64 sha256:sha256:<hex>" and
          # every consumer (the Go parser, the signature itself) would see
          # a line that does not match "<name> sha256:<hex>" at all.
          : > image-digests.txt
          printf 'fugaro-base-amd64 %s\n' "$(cat digest-amd64.txt)" >> image-digests.txt
          printf 'fugaro-base-arm64 %s\n' "$(cat digest-arm64.txt)" >> image-digests.txt
          printf 'fugaro-base %s\n' "$(cat digest-index.txt)" >> image-digests.txt
          if [ -f digest-history.txt ]; then
            printf 'fugaro-history %s\n' "$(cat digest-history.txt)" >> image-digests.txt
          fi
      # PIN: resolve sigstore/cosign-installer's current release SHA before
      # copying this file in (docs/pending-workflows/README.md "Before you
      # copy: check the action pins") — not fabricated here.
      - uses: sigstore/cosign-installer@<PIN>
      - name: Sign the digest manifest
        run: sh images/sign-digests.sh image-digests.txt
      - name: Wait for the release, then upload
        env:
          GH_TOKEN: ${{ github.token }}
          TAG: v${{ needs.version.outputs.version }}
        run: |
          set -eu
          tries=0
          until gh release view "$TAG" --repo "$GITHUB_REPOSITORY" >/dev/null 2>&1; do
            tries=$((tries + 1))
            if [ "$tries" -ge 30 ]; then
              echo "release $TAG did not appear after $tries tries (release.yml and images.yml are independent workflows on the same tag push)" >&2
              exit 1
            fi
            sleep 20
          done
          gh release upload "$TAG" image-digests.txt image-digests.txt.sigstore.json --repo "$GITHUB_REPOSITORY" --clobber
```

`docs/pending-workflows/README.md`'s `## images.yml` section is replaced (not appended to): it now describes *this* diff — the digest-writing additions to `publish-base` and `publish`, and the new `sign-base-digests` job — against the real, already-merged `.github/workflows/images.yml`, instead of the base-image consolidation diff that commit `356f7b1` already copied in. The "Copying a file in" instructions and the `TestReadWorkflowFallsBackOnlyWhilePending`-style tests above that section are unaffected, since they test the fallback mechanism itself, not any one pending file's content.

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
- Modify: `internal/cli/init_images.go` (delete `items()`'s base-kind branch, `confirmReplace`'s base-kind path and `record()`'s base-kind write; `managedVersion`/`isManaged` and `curVersion` — defined here, used by `image_refresh_rules.go` too — stay, with the signature change below, not deleted), `internal/cli/image_refresh_rules.go` (`refreshBase` resolves+verifies instead of building a `mirror.Dest`), `internal/cli/image_refresh_steps.go` (`refreshBase` step's confirmation text), `internal/image/local.go` (`BaseRef` either gains a verified form or is replaced by a thin call into `basepin` — the plan's implementer picks one, see note below)
- Modify tests: `internal/cli/init_images_test.go`, `internal/cli/image_refresh_rules_test.go`, `internal/cli/image_refresh_test.go`

**Interfaces:**
- `managedVersion(ref, host, source, kind string) (version string, ok bool)`: gains a **second** prefix parameter, `source`; it does not replace `host`. Both are needed simultaneously, because a managed ref can legitimately be in **either** shape across the migration (§5 of the design doc): `host` (today's parameter, the installation's own registry — real accessor `infra.RegistryHost(lc)`, already in the codebase) recognizes a pre-migration, Artifact-Registry-copied entry; `source` (new — the configured `--image-source` or `mirror.DefaultSources[0]`) recognizes the new ghcr.io-pinned shape. (An earlier draft of this document wrongly had `source` *replace* `host` in the signature, which cannot recognize an old entry at all — contradicting its own Step 3 prose, which already described checking both prefixes.) `isManaged` takes the same four parameters.
- `imageSource(flag string) string` (**new**; nothing with this name exists in the codebase today): mirrors the existing `(*imagesStage).allow()` method's logic (`internal/cli/init_images.go`, deleted along with the rest of that base-kind branch) as a small free function, independent of `imagesStage` — `if flag != "" { return flag }; return mirror.DefaultSources[0]`. Callers pass `r.o.imageSource`/`o.imageSource` (the existing `refreshOptions`/`initOptions` field), unchanged.
- `refreshBase(ctx context.Context, lc *localcfg.Config, host, source, kind, ver string, resolve func(ctx context.Context, name, version string) (string, error)) (string, error)` — the production code points `resolve` at `basepin.Resolve` (with the real `Source`/`Verifier`) and tests point it at a fake — keeping `internal/cli` free of a direct `sigstore-go` import, which stays inside `internal/basepin` alone.

- [ ] **Step 1: Write the failing tests**

`internal/cli/image_refresh_rules_test.go` (replacing the existing `TestRefreshBase`, which built `mirror.Dest` paths that no longer exist for `base`):

```go
const testHost = "us-east5-docker.pkg.dev/proj-1234" // infra.RegistryHost(lc), fixed for these tests
const testSource = "ghcr.io/dimipaun"                 // imageSource(""), mirror.DefaultSources[0]

func TestRefreshBaseResolvesAndVerifies(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"}
	resolved := "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)
	resolve := func(ctx context.Context, name, version string) (string, error) {
		if name != "fugaro-base" || version != "0.7.1" {
			t.Fatalf("resolve(%q, %q)", name, version)
		}
		return resolved, nil
	}
	got, err := refreshBase(context.Background(), lc, testHost, testSource, "base", "0.7.1", resolve)
	if err != nil || got != resolved {
		t.Errorf("got %q, %v; want %q", got, err, resolved)
	}
}

func TestRefreshBaseKeepsANewerManagedCopy(t *testing.T) {
	newer := "ghcr.io/dimipaun/fugaro-base:0.8.0@sha256:" + strings.Repeat("d", 64)
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5", BaseImages: map[string]string{"base": newer}}
	resolve := func(context.Context, string, string) (string, error) { t.Fatal("must not resolve: the existing copy is newer"); return "", nil }
	got, err := refreshBase(context.Background(), lc, testHost, testSource, "base", "0.7.1", resolve)
	if err != nil || got != newer {
		t.Errorf("got %q, %v; want the kept newer copy %q", got, err, newer)
	}
}

func TestRefreshBaseKeepsANewerPreMigrationManagedCopy(t *testing.T) {
	// An old, Artifact-Registry-hosted copy, newer than this release: still
	// recognized and kept (host, not source, is what matches it).
	oldNewer := testHost + "/fugaro-base/fugaro-base:0.8.0"
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5", BaseImages: map[string]string{"base": oldNewer}}
	resolve := func(context.Context, string, string) (string, error) { t.Fatal("must not resolve: the existing copy is newer"); return "", nil }
	got, err := refreshBase(context.Background(), lc, testHost, testSource, "base", "0.7.1", resolve)
	if err != nil || got != oldNewer {
		t.Errorf("got %q, %v; want the kept newer copy %q", got, err, oldNewer)
	}
}

func TestRefreshBaseRefusesAnUnverifiedResolution(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"}
	resolve := func(context.Context, string, string) (string, error) { return "", errors.New("certificate identity mismatch") }
	if _, err := refreshBase(context.Background(), lc, testHost, testSource, "base", "0.7.1", resolve); err == nil {
		t.Error("want an error: resolve failed, nothing is recorded unverified")
	}
}

// TestManagedVersionRecognizesBothShapes: a managed ref can be in EITHER
// shape across the migration (design retire-image-mirror.md §5), so
// managedVersion/isManaged need host AND source, not source alone.
func TestManagedVersionRecognizesBothShapes(t *testing.T) {
	pinned := "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)
	if v, ok := managedVersion(pinned, testHost, testSource, "base"); !ok || v != "0.7.1" {
		t.Errorf("managedVersion(%q) = %q, %v", pinned, v, ok)
	}
	if _, ok := managedVersion(pinned, testHost, "ghcr.io/someone-else", "base"); ok {
		t.Error("a ref under another owner must not be managed")
	}
	// The pre-migration, installation-registry-hosted shape stays
	// recognized too, so an old base_images.base entry is not treated as a
	// custom base.
	old := testHost + "/fugaro-base/fugaro-base:0.7.0"
	if v, ok := managedVersion(old, testHost, testSource, "base"); !ok || v != "0.7.0" {
		t.Errorf("an old copied ref must still be recognized: managedVersion(%q) = %q, %v", old, v, ok)
	}
	// An old-shaped ref under ANOTHER project's registry host is not
	// managed by this installation's config either.
	if _, ok := managedVersion(old, "us-east5-docker.pkg.dev/other-project", testSource, "base"); ok {
		t.Error("an old ref under another project's registry must not be managed")
	}
}
```

`internal/cli/init_images_test.go` gains a case proving the images stage's `items()` no longer lists any `base`-kind item at all (only `history`, under `--firebase`), and that `--base` is refused or ignored for `base` with a message naming `fugaro image refresh` instead.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestRefreshBase|TestManagedVersion' -count=1`
Expected: FAIL (old signatures, old `mirror.Dest`-based implementation).

- [ ] **Step 3: Implement**

`refreshBase` becomes:

```go
// refreshBase is the base image kind's ref once the base step has run:
// this release's verified pin, or a newer managed copy an earlier CLI
// already recorded (never downgraded, in either shape — host recognizes a
// pre-migration copy, source the new pinned form). resolve is
// basepin.Resolve, wired by the caller with the real Source and Verifier;
// tests supply a fake. Callers pass host from infra.RegistryHost(lc)
// (unchanged, existing) and source from imageSource(o.imageSource) (new).
func refreshBase(ctx context.Context, lc *localcfg.Config, host, source, kind, ver string, resolve func(ctx context.Context, name, version string) (string, error)) (string, error) {
	if !releaseRE.MatchString(ver) {
		return "", userErr("fugaro image refresh needs a release build of fugaro: %q is not a release version", ver)
	}
	cur := lc.BaseImage(kind)
	if cur != "" && !isManaged(cur, host, source, kind) {
		return "", userErr("base_images.%s is %s: not a release image fugaro verified, and fugaro image refresh never replaces one", kind, pluginwire.Printable(cur))
	}
	if cur != "" && newer(curVersion(cur, host, source, kind), ver) {
		return cur, nil // a newer managed copy is kept; nothing is re-resolved
	}
	return resolve(ctx, "fugaro-"+kind, ver)
}

// imageSource is the configured release source prefix: --image-source when
// set, else the default (mirror.DefaultSources[0], "ghcr.io/dimipaun").
// New: replaces (*imagesStage).allow()'s now-deleted base-kind use of this
// same logic, as a free function so refreshBase needs no imagesStage.
func imageSource(flag string) string {
	if flag != "" {
		return flag
	}
	return mirror.DefaultSources[0]
}
```

`managedVersion`/`isManaged` gain a `source` parameter alongside the existing `host`, and their shared-prefix check (today, `strings.CutPrefix(ref, host+"/"+mirror.BaseRepository+"/fugaro-"+kind+":")`) becomes two alternatives tried in order: the new `source+"/fugaro-"+kind+":"` prefix (ghcr.io-shaped, digest-and-tag), and the existing `host+"/"+mirror.BaseRepository+"/fugaro-"+kind+":"` prefix, unchanged, kept for recognizing a pre-migration entry as still-managed (never newly written again) — exactly the backward-compatible case `TestManagedVersionRecognizesBothShapes` pins.

`internal/image/local.go`'s `BaseRef(base, version string) (string, error)` — used today as an **offline, no-network** fallback wherever `base_images` has no entry yet (`refreshReload`, `image.go`, `init.go`, `image_layer.go`) — keeps that exact offline contract and its callers unchanged; it is not where verification happens (verification happens once, at `image refresh`/`init --base`/`upgrade` time, and is **recorded** into `base_images.base`). Those callers already read `base_images.base` first and fall back to `BaseRef` only when it is unset, so after one successful `image refresh` the verified, digest-pinned value is what every caller actually uses; `BaseRef`'s bare-tag fallback is reached only for a repository that has never run `image refresh` at all, in which case it already refuses to submit a build in some callers and prints "no published base image yet" in others — unchanged by this task, since fixing that corner is exactly what running `image refresh` once does.

Run: `go test ./internal/cli/... ./internal/image/... -count=1`
Expected: PASS.

## Task 4: `Cover.checkSpecs`'s narrow allowance for a pinned external base

**Flag for opus review:** `cover.go` is the one piece of code that decides what "nothing is applied unseen" silently covers; a loose match here (anything under `ghcr.io/dimipaun`, or anything with an `@sha256:`) would let a hand-edited `base_images.base` naming an attacker's own image sail through the single confirmation instead of asking its own.

**Files:**
- Modify: `internal/infra/tf/cover.go` (`checkSpecs`'s `inRegistry` gains a second check; new import `internal/config`, for `config.Bases` — checked for an import cycle: `internal/config` does not import `internal/infra/tf` or anything under `internal/infra`, so none exists), `internal/infra/tf/cover_test.go` (also `internal/cli/init_review.go`, which constructs the real `tf.Cover{}` value — see below)

**Interfaces:**
- `Cover` gains a field, `ImageSource string` (the configured `mirror.DefaultSources[0]`/`--image-source`, passed in by the caller exactly as `Registry`/`Projects` already are), used only to build the second allowed prefix. Production wiring: `internal/cli/init_review.go`'s `(*initEngine).cover()` — the one place that actually constructs a `tf.Cover{}` today (`c := tf.Cover{Projects: …, Region: …, Buckets: …}`, then sets `c.Registry` a few lines down) — gains `c.ImageSource = imageSource(e.r.o.imageSource)` (Task 3's new `imageSource` function) alongside its existing `c.Registry` assignment.
- `inRegistry`'s replacement, `coveredBaseImage(ref string) bool`, used **only** for `s.BaseImages` entries (never for `s.Registry` or a build service account, which keep today's `inRegistry` exactly): `inRegistry(ref) || isPinnedSource(ref)`, where `isPinnedSource` requires an **exact, fully-anchored** `<c.ImageSource>/fugaro-<kind>:<X.Y.Z>@sha256:<64 hex>` match (one of `config.Bases`) — never a prefix match alone, never a bare tag, never missing the digest, and never anything the existing `refChars`/`".."` checks would themselves refuse.

- [ ] **Step 1: Write the failing tests**

`internal/infra/tf/cover_test.go`: add these cases to `TestCoverCheckSpec`'s existing map literal (not a new test — `jobWithSpec`/`spec(registry, bases...)` are that test's own existing fixtures, and `reg` there is `"us-east5-docker.pkg.dev/proj-1/"`, trailing slash included), and add `ImageSource: "ghcr.io/dimipaun"` to the file's shared `testCover` fixture (`cover_test.go:21`) so every case in the table — old and new — runs against it:

```go
"pinned external base is covered":                                 {jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:"+strings.Repeat("c", 64)), false), true},
"external base with no digest is NOT covered":                     {jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-base:0.7.1"), false), false},
"external base under another owner is NOT covered":                {jobWithSpec(spec(reg+"r", "ghcr.io/someone-else/fugaro-base:0.7.1@sha256:"+strings.Repeat("c", 64)), false), false},
"external base naming another kind is NOT covered":                {jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-other:0.7.1@sha256:"+strings.Repeat("c", 64)), false), false},
// Regression case for the un-anchored match this task's own first draft
// shipped: "ghcr.io/dimipaun/fugaro-base" is a literal string prefix of
// "ghcr.io/dimipaun/fugaro-base-evil/x:...", so a HasPrefix-plus-unanchored-
// regex check would wrongly cover this. The fix anchors the match to
// start exactly where the prefix ends.
"external base with an evil suffix on the prefix is NOT covered":  {jobWithSpec(spec(reg+"r", "ghcr.io/dimipaun/fugaro-base-evil/x:0.7.1@sha256:"+strings.Repeat("c", 64)), false), false},
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/tf/ -run TestCoverCheckSpec -count=1`
Expected: FAIL (`inRegistry` alone refuses every one of the new "covered" cases, and `Cover` has no `ImageSource` field yet).

- [ ] **Step 3: Implement**

```go
// pinnedSourceRE matches the remainder immediately after an allowed
// source+kind prefix has been trimmed off: it must START there (the ^
// anchor against the trimmed remainder, not the whole ref) with exactly
// ":X.Y.Z@sha256:<64 hex>" and nothing else. Without the ^ anchor here,
// strings.HasPrefix(ref, prefix) plus an unanchored regexp.MatchString
// would wrongly accept "ghcr.io/dimipaun/fugaro-base-evil/x:0.7.1@sha256:…"
// (HasPrefix only checks that "ghcr.io/dimipaun/fugaro-base" is a leading
// SUBSTRING, not that a path boundary follows it, and MatchString finds a
// match anywhere in the remainder, not only at its start) — pinned here by
// "external base with an evil suffix on the prefix is NOT covered".
var pinnedSourceRE = regexp.MustCompile(`^:([0-9]+\.[0-9]+\.[0-9]+)@sha256:[0-9a-f]{64}$`)

func (c Cover) coveredBaseImage(ref string, inRegistry func(string) bool) bool {
	if inRegistry(ref) {
		return true
	}
	// The same sanity checks inRegistry itself applies to every ref it
	// reads (refChars's character allowlist, and no ".." path segment):
	// a fully-anchored pinnedSourceRE already constrains everything after
	// the literal prefix, but the prefix itself (c.ImageSource, kind) is
	// reused here with no fresh validation, so this stays, defense in depth.
	if c.ImageSource == "" || !refChars.MatchString(ref) || slices.Contains(strings.Split(ref, "/"), "..") {
		return false
	}
	for _, kind := range config.Bases {
		prefix := c.ImageSource + "/fugaro-" + kind
		if rest, ok := strings.CutPrefix(ref, prefix); ok && pinnedSourceRE.MatchString(rest) {
			return true
		}
	}
	return false
}
```

(`strings.CutPrefix` replaces the earlier `HasPrefix` + `TrimPrefix` pair with one call that also makes the "did it actually have this prefix" check explicit, rather than two calls that could drift out of sync.)

`checkSpecs`'s `s.BaseImages` loop calls `c.coveredBaseImage(ref, inRegistry)` instead of `inRegistry(ref)` directly; `s.Registry` and the build-service-account check are untouched.

Run: `go test ./internal/infra/tf/... -count=1`
Expected: PASS.

## Task 5: Terraform — remove the build account's `fugaro-base` reader grant, safely ordered

**Flag for opus review:** the ordering hazard below (a repository's build account losing read access to `fugaro-base` before the installation's `base_images.base` is actually pinned) is a real availability regression, not just friction, if the precondition is missed or implemented loosely enough to be bypassed.

**The address, corrected.** `deploy/terraform/gcp/roots/repo/main.tf` invokes `module "repo" { … }` once per apply, with no `for_each`/`count` on the module block, and `build.tf`'s `google_artifact_registry_repository_iam_member.build_base` resource itself has no `for_each`/`count` either — so its Terraform address is the plain `module.repo.google_artifact_registry_repository_iam_member.build_base`, **not** indexed by the repository's name (an earlier draft of this document wrote `["<repo>"]`, which assumed a `for_each` that does not exist).

**The ordering hazard (design §4) and its fix.** `base_images.base` is one value shared by the whole installation, while the grant this task removes is applied per repository, by that repository's own `fugaro init --repo`. If an operator runs `fugaro init --repo` for one repository before *any* repository of that installation has ever run `fugaro image refresh`/`fugaro upgrade` (the step that moves `base_images.base` to the new pin, Task 3), that repository's builds and its own daily check job (which submits Cloud Build as, and runs as, the same build account) would try to pull the still-old, Artifact-Registry-hosted base using a grant Terraform just removed, and fail. This is closed with a **Go-side precondition**, not a Terraform `count` (a `count`-conditioned version of an already-unindexed resource would need a `moved` block to avoid Terraform treating the indexing change itself as a replace, and this repository has no existing `moved`-block precedent to build on — enforcing the order in Go, once, before Terraform is ever invoked, is simpler and needs no Terraform-state surgery).

**The precondition is NOT `isManaged` — it cannot be.** An earlier draft of this document gated the precondition on `!isManaged(cur, host, source, kind)`, but `isManaged` (Task 3) is deliberately built to recognize **both** shapes as fine — the old Artifact-Registry-hosted copy and the new ghcr.io pin — exactly so `image refresh` doesn't treat a pre-migration entry as a hostile custom base. That is the right behavior for `image refresh`'s own "is this safe to touch" question, and the wrong one for this task's "is the build account's read grant on `fugaro-base` still needed" question: `isManaged` would return `true` for a repository that has *never* been migrated, so the precondition would never fire for exactly the installation it exists to protect — reproducing the hazard, not closing it. The actual, narrower question this task needs answered is simpler and does not need `isManaged`/`managedVersion` at all: **is `base_images.base` still hosted inside this installation's own `fugaro-base` registry, in any shape** — a pre-migration managed copy, a hand-pushed `dev-<sha>` tag, or any other custom image someone pushed there? If so, the grant is still load-bearing and must not be removed; only once the value points somewhere else entirely (the new ghcr.io pin, or a custom image hosted in a different registry altogether, `--base-image`) is the grant safe to drop:

```go
// baseStillInInstallationRegistry reports whether ref is hosted inside
// this installation's own fugaro-base registry (host+"/"+
// mirror.BaseRepository+"/"), in ANY shape — a release copy isManaged
// recognizes, a hand-pushed dev tag, or anything else. Unlike isManaged
// (Task 3), this does not distinguish "a release image fugaro verified"
// from "a custom one": it only asks whether the build account's read
// grant on fugaro-base is still load-bearing for this repository, which
// is true whenever the base is read from there at all, managed or not.
func baseStillInInstallationRegistry(ref, host string) bool {
	return strings.HasPrefix(ref, host+"/"+mirror.BaseRepository+"/")
}
```

**Files:**
- Modify: `internal/cli/init_repo_stage.go` (`(*repositoryStage).Plan`, confirmed to exist at `init_repo_stage.go:134` — the implementer confirms the exact point to add the check when this task starts, since it is new code with no existing analogue to cite a line number for yet): gains a precondition, checked before any Terraform plan is generated, that refuses with a clear message naming `fugaro image refresh`/`fugaro upgrade` when `baseStillInInstallationRegistry` is true for the local config's `base_images.base` — mirroring the shape of the existing `customBaseRefusal`/`gcpProjectProblems` preflight checks elsewhere in this package, not a new pattern, but a **new, separate** predicate from Task 3's `isManaged`, not a reuse of it.
- Modify: `deploy/terraform/gcp/modules/repo/build.tf` (delete `google_artifact_registry_repository_iam_member.build_base`)
- Regenerate: `internal/infra/tf/testdata/golden/repo*.plan.json` (`scripts/gen-golden-plans.sh`, per the base-image plan's own Task 19 precedent)
- Modify: `docs/base-image-migration.md` (a new, short section: the required order — pin first, `init --repo` second — and the one-time `--allow-delete` an operator sees)

**Interfaces:** no new exported type; `baseStillInInstallationRegistry(ref, host string) bool` (above) plus one new unexported precondition function in `init_repo_stage.go`, shaped like `func (s *repositoryStage) baseGrantStillNeeded(lc *localcfg.Config, host string) error` (name is illustrative — the implementer may name it to fit the file's existing conventions), returning a `userErr` naming the fix when `baseStillInInstallationRegistry(lc.BaseImage("base"), host)` is true. An empty `base_images.base` is not refused here (nothing has been pinned into `fugaro-base` to strand, and a fresh installation's own `fugaro init` sets it via its own images stage, Task 3, before this stage's Terraform ever runs in the same invocation — see "no new deadlock" below).

**No new deadlock for a brand-new installation.** A plain `fugaro init` from within a checkout runs the images stage (which verifies and pins `base_images.base`, Task 3) before the repository stage's Terraform (this stage), in the same invocation — so a first-ever setup already has the new pin in hand by the time this precondition runs, and never refuses itself. The precondition only ever fires for an **existing**, previously-migrated-under-the-old-mirror installation whose operator runs `fugaro init --repo` for an onboarded (or newly onboarded) repository without first running `fugaro image refresh`/`fugaro upgrade` anywhere in it — exactly the case this task exists to catch.

- [ ] **Step 1: Write the failing tests**

The new predicate, `internal/cli/init_repo_stage_test.go` (or wherever it is defined — the implementer's call):

```go
func TestBaseStillInInstallationRegistry(t *testing.T) {
	const host = "us-east5-docker.pkg.dev/proj-1234"
	for ref, want := range map[string]bool{
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.6.0":    true,  // old managed copy: still in fugaro-base
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc": true,  // hand-pushed dev tag: isManaged would say false, this must still say true
		"ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64): false, // new pin: outside fugaro-base
		"registry.example.com/acme/custom-base:1":                           false, // a --base-image pointed entirely elsewhere
		"":                                                                   false, // never pinned: nothing to strand
	} {
		if got := baseStillInInstallationRegistry(ref, host); got != want {
			t.Errorf("baseStillInInstallationRegistry(%q) = %v, want %v", ref, got, want)
		}
	}
}
```

The ordering precondition, `internal/cli/init_repo_stage_test.go` (shape only — the implementer wires it to this file's actual fixtures, which this plan has not inspected in enough depth to sketch byte-exactly):

```go
func TestRepoStageRefusesBeforeTheBaseIsPinned(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5",
		BaseImages: map[string]string{"base": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.6.0"}} // old, unpinned shape, still in fugaro-base
	// ... build the stage fixture this test file's existing helpers use ...
	if _, err := stage.Plan(context.Background(), initflow.Env{}); err == nil || !strings.Contains(err.Error(), "fugaro image refresh") {
		t.Errorf("Plan() = %v, want a refusal naming fugaro image refresh", err)
	}
}

// TestRepoStageRefusesACustomBaseStillInTheInstallationRegistry: a
// hand-pushed dev tag still inside fugaro-base must refuse too, even
// though isManaged (a different question) would call it unmanaged — this
// is the case an isManaged-based precondition would have gotten backwards.
func TestRepoStageRefusesACustomBaseStillInTheInstallationRegistry(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5",
		BaseImages: map[string]string{"base": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc123"}}
	// ... same fixture shape as above ...
	if _, err := stage.Plan(context.Background(), initflow.Env{}); err == nil || !strings.Contains(err.Error(), "fugaro image refresh") {
		t.Errorf("Plan() = %v, want a refusal naming fugaro image refresh", err)
	}
}

func TestRepoStagePlansOnceTheBaseIsPinned(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5",
		BaseImages: map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("c", 64)}}
	// ... same fixture, now expected to proceed to its normal Plan() path ...
}
```

The golden-plan regression test, `internal/infra/tf/cover_test.go` (reusing the real `goldenPlan(t, name)` helper already in this package, `cover_golden_test.go:21` — there is no `goldenRepoPlanAddresses` helper, invented in an earlier draft of this document; none is needed):

```go
// TestGoldenRepoPlanNoLongerGrantsBuildBaseRead pins that regenerating the
// repo golden plan (after this task's Terraform change) no longer lists the
// build_base grant, so a future accidental re-add is caught.
func TestGoldenRepoPlanNoLongerGrantsBuildBaseRead(t *testing.T) {
	for _, rc := range goldenPlan(t, "repo").ResourceChanges {
		if strings.Contains(rc.Address, "build_base") {
			t.Errorf("golden repo plan still has %s: the build account no longer reads fugaro-base (base images are pulled directly, design retire-image-mirror.md §4)", rc.Address)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestBaseStillInInstallationRegistry|TestRepoStage' -count=1` and `go test ./internal/infra/tf/ -run TestGoldenRepoPlanNoLongerGrantsBuildBaseRead -count=1`
Expected: FAIL (no predicate, no precondition yet; the current golden plan still has the grant).

- [ ] **Step 3: Implement**

Add `baseStillInInstallationRegistry` (above) and the precondition that calls it to `(*repositoryStage).Plan`, before it builds or shows any Terraform plan. Delete the `google_artifact_registry_repository_iam_member.build_base` resource block from `deploy/terraform/gcp/modules/repo/build.tf`; run `scripts/gen-golden-plans.sh` to regenerate the golden JSON fixtures against a fake provider (no live Terraform apply — the existing script already works this way for every other golden-plan change in this repository).

`docs/base-image-migration.md` gains, under "Everywhere":

> **Order matters for this step.** Run `fugaro image refresh` (or `fugaro upgrade`) once, against any one onboarded repository of your installation, **before** running `fugaro init --repo` (or whole-installation `fugaro init`) anywhere in it: `base_images.base` is shared by the whole installation, but each repository's build account's read grant on `fugaro-base` is removed per repository, by that repository's own `init --repo`, and a repository whose grant is removed before the base is pinned would fail its next build and its daily check until it is. `fugaro init --repo` refuses outright, naming this fix, if it isn't pinned yet — it does not depend on you remembering the order. Once it is, that run's Terraform plan shows a `delete` of `module.repo.google_artifact_registry_repository_iam_member.build_base` (the build account's now-unneeded read grant); `tf.Guard` already prints the exact address to pass with `--allow-delete` in its refusal, so there is nothing to construct by hand.

Run: `go test ./internal/cli/... ./internal/infra/tf/... -count=1`
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
	// this even without a network). executeStdin is the file's own existing
	// helper (internal/cli/init_test.go already calls it this way, e.g.
	// `executeStdin(t, "", "init", "--plan-only")`); there is no
	// executeInitPlanOnly in this codebase.
	out, _, err := executeStdin(t, "", "init", "--base", "base", "--plan-only")
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
