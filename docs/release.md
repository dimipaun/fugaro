# Releasing Fugaro

One SemVer tag `vX.Y.Z` releases everything (design section 12): the binary, the base images, the Terraform modules at the tag, the schemas at the tag, and the plugin. Before 1.0, minor versions may break.

## What a tag does

A push of a strict `vX.Y.Z` tag triggers two independent workflows:

- `release.yml` (this document): checks the tag, then runs GoReleaser, which builds `fugaro` for darwin and linux on amd64 and arm64, writes `checksums.txt`, an SBOM per archive (syft), a keyless cosign signature of `checksums.txt`, and the GitHub Release with a grouped changelog; it also pushes the Homebrew cask. A `v0.x.y` release is then marked as a pre-release (GoReleaser's `prerelease: auto` only recognises `-rc1`-style suffixes, which the strict tag rule never produces).
- `images.yml`: builds, smoke tests, scans and publishes `ghcr.io/dimipaun/fugaro-web-node:X.Y.Z` and `:X`, and the same two tags of `ghcr.io/dimipaun/fugaro-go`, `ghcr.io/dimipaun/fugaro-java-services` and `ghcr.io/dimipaun/fugaro-history` (`:X` moves only to the highest release of that major). After publishing, the `verify-public` job pulls every reference with no login (`images/verify-public.sh`) and fails the release when one cannot be read or has no `linux/amd64` build; it lists each image's digest in the run summary. Pull requests build, smoke test and publish nothing; the nightly run scans.

Neither workflow runs the test suite; CI already did on main. Both workflows run `scripts/release-gate.sh` first, so a tag on an off-main or red commit publishes nothing (no binaries, no images). It refuses unless the tagged commit is reachable from `origin/main` and the GitHub Actions checks `test`, `terraform` and `rules` all succeeded on it (a check of that name from another app does not count); if CI is still running, re-run the failed job once it is green. `release.yml` also requires the plugin version to equal the tag.

## Making the image packages public (one time per package)

A package that a workflow creates on ghcr.io is private. The mirror (below) reads the source with no credentials, and a plain `docker pull` has none either, so each of the four packages must be public. GitHub has no API call for this with the workflow's token; do it once by hand per package, after the first publish of that image:

1. Open `https://github.com/users/dimipaun/packages/container/fugaro-<name>/settings` for `web-node`, `go`, `java-services` and `history`.
2. Under "Danger Zone", "Change package visibility", choose Public and confirm.
3. Re-run the `verify-public` job of that release's `images` run (or `images/verify-public.sh ghcr.io/dimipaun/fugaro-<name>:X.Y.Z` on any machine, which uses no login).

Until it is done, the first release's `verify-public` job fails by design with "cannot be read without a login (is the package public?)": the images are published and the failure only says the step above is outstanding. Later releases of a public package stay public.

## How `fugaro init` uses the images (the mirror)

Cloud Build and Cloud Run in a user's project cannot read ghcr.io, and the project's base registry `fugaro-base` is writable only by people, never by a build account. So the `images` stage of `fugaro init` copies the images, as the user, from `ghcr.io/dimipaun/fugaro-<kind>:X.Y.Z` (the running binary's version; `--image-source ghcr.io/<owner>` for a fork) into `<region>-docker.pkg.dev/<gcp-project>/fugaro-base/`: the history image with `--firebase` (it lands as `history:latest`, which the history job runs) and a base kind when `--base <kind>` names it or the checkout's `fugaro.yaml` does (recorded in `base_images`). It is plain HTTP against the registry API: no Docker, only the `linux/amd64` image, each digest verified, nothing sent when the destination already has the same digest. A development build copies nothing and says how to build from a checkout.

What a release must therefore guarantee is what `verify-public` checks: every published reference is anonymously readable and has a `linux/amd64` build. Until a package is made public (above) a user's `init` is refused with the unreadable-source message. The trust anchor is the tag as ghcr.io resolves it: users who want more pin the digest from the `verify-public` run summary with `--expect-digest KIND=sha256:<hex>` (KIND is `go`, `java-services`, `web-node` or `history`). The release notes do not list the digests (GoReleaser runs in another workflow and cannot know them); signing the images and verifying before the copy is a planned follow-up (the plan's F1). **Which images exist when:** v0.1.0 published only `fugaro-web-node`; the next tag is the first to publish `fugaro-go`, `fugaro-java-services` and `fugaro-history` (and so the first whose `verify-public` can pass for them, after the one-time public step).

## Secrets

- `HOMEBREW_TAP_GITHUB_TOKEN`: a fine-grained personal access token with `Contents: Read and write` on the single repository `dimipaun/homebrew-tap`. Add it as a repository secret. If it is absent the release still succeeds and only the tap update is skipped, with a `::warning::` and a line in the run summary (add the secret and re-run the job, or copy the cask from a `dry-run` artifact's `homebrew/Casks/` by hand).
- Nothing else: the release uses the workflow's `GITHUB_TOKEN` (`contents: write`) and GitHub OIDC (`id-token: write`) for cosign.

## The cask's quarantine hook

The Homebrew cask runs a post-install step on macOS: `xattr -dr com.apple.quarantine` on the installed `fugaro`. The binaries are not signed or notarized by Apple, so without it Gatekeeper blocks the first run of a downloaded binary. The cost is that Gatekeeper's check is skipped for this binary; integrity rests on the sha256 in the cask, which comes from the release archives, and on the cosign-signed `checksums.txt`. Linux cask support was not tested; Linux users can use the archive or `go install`.

## Cutting a release

**Before you tag (the checklist; each item has been missed or is easy to miss):**

- **Run `scripts/bump-plugin-version.sh X.Y.Z` and commit it** (through a PR to main). The release gate refuses a tag whose plugin version or any skill header differs from it, and a repository pinned to the tag would otherwise load a plugin that disagrees with the binary.
- **Make each new ghcr.io package public, once** (see "Making the image packages public"): the first release that publishes `fugaro-go`, `fugaro-java-services` and `fugaro-history` leaves them private, and `verify-public` fails until you do.
- **Turn on tag protection and immutable releases (the repository owner's setting; Fugaro does not change repository settings).** A git tag is mutable, and the plugin pin and the image tags are anchored to it, so: add a GitHub ruleset that restricts updates and deletions of `v*` tags (Settings, Rules, Rulesets, target tags `v*`, restrict updates and deletions), and enable immutable releases. Without them, anyone who can push tags can move `vX.Y.Z` after users pin to it.
- **Refresh `internal/pricing`'s `checkedAt` date** after checking the vendor's price page (`fugaro budget prices` warns that the table is older than 90 days: the table was last checked 2026-09-30, so it warns from 2026-12-29).
- The binary prints the commit it was built from (`fugaro doctor --plugin`, `fugaro update-skills`) with the one line that checks the tag: `git ls-remote https://github.com/dimipaun/fugaro 'refs/tags/vX.Y.Z^{}' 'refs/tags/vX.Y.Z'` must print that commit: on the `^{}` line for an annotated tag, on the only line for a lightweight tag (the `^{}` pattern alone prints nothing for one, which is why both are given). Run it once after tagging.

1. Main is green: `test`, `terraform` and `rules` passed on the commit to be released (`gh run list --branch main`).
2. Bump the plugin and commit it through a PR to main:

   ```sh
   scripts/bump-plugin-version.sh X.Y.Z   # plugin/.claude-plugin/plugin.json and the header of every file under plugin/skills; the marketplace entry carries no version and the plugin test rejects one
   ```

   The header is the two lines at the top of each skill file, the comment `<!-- fugaro-skill name=<dir> fugaro-version=X.Y.Z -->` and the sentence `(Fugaro X.Y.Z)` in the quoted notice; the script rewrites both everywhere and then runs its own check. `go test ./plugin/` only requires a non-empty `plugin.json` version and a header in every skill file equal to it; the release workflow enforces `version == tag` for both (`scripts/bump-plugin-version.sh --check X.Y.Z`), so a tag on a commit whose plugin version or any skill header differs publishes nothing.

   **The plugin is pinned to this tag (M11, [design §4.3](design/m11-setup-and-skills.md)).** `fugaro init` and `fugaro update-skills` write the release tag `vX.Y.Z` of the running binary as the `ref` of the Fugaro marketplace in a repository's `.claude/settings.json`, so the binary, the plugin and the pin agree only if the plugin at that tag has version `X.Y.Z`: that is what the gate above enforces, and why a published tag is never moved or re-cut (a pin to it would silently change). `bump-plugin-version.sh` rewrites, and `--check` verifies, the version in `plugin.json` and in each skill's do-not-edit header. A development build (`fugaro version` prints `dev`) writes no pin. A private fork hosts its own marketplace and plugin at its own tags (`--allow-fork` moves the ref of its entry to the running binary's tag), and its users pin to those. After a release a repository moves its pin with `fugaro update-skills` (or `fugaro init`), reviewed as one line in a PR; `fugaro doctor --plugin --strict` in its CI fails a pin that is older or newer than the binary, unpinned, foreign or not wired.
3. Rehearse if the pipeline changed: Actions, `release`, Run workflow (`workflow_dispatch`). It runs `goreleaser release --snapshot --skip=publish,sign` and uploads `dist/` as the `goreleaser-dist` artifact. Locally: `goreleaser release --snapshot --clean --skip=publish,sign` with `HOMEBREW_TAP_GITHUB_TOKEN=` set (empty), and syft on the PATH (or add `sbom` to `--skip`).
4. Tag the merged commit and push:

   ```sh
   git switch main && git pull --ff-only
   git tag -a vX.Y.Z -m "Fugaro vX.Y.Z"
   git push origin vX.Y.Z
   ```

5. Watch both workflows (`gh run watch`), then verify.

Pull requests that touch `.goreleaser.yaml`, `release.yml` or the bump script run `goreleaser check` as the `check` job of `release.yml`. It is not a required check.

## Verifying a release

```sh
V=X.Y.Z
gh release download v$V -R dimipaun/fugaro -p checksums.txt -p checksums.txt.sigstore.json -p "fugaro_${V}_$(uname -s | tr A-Z a-z)_*"

# The checksums are signed by the release workflow on that tag
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/dimipaun/fugaro/.github/workflows/release.yml@refs/tags/v$V" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# The archives match the checksums
grep " fugaro_${V}_" checksums.txt | shasum -a 256 -c -      # macOS
grep " fugaro_${V}_" checksums.txt | sha256sum -c -         # Linux

# Each channel
brew install dimipaun/tap/fugaro && fugaro version
go install github.com/dimipaun/fugaro/cmd/fugaro@v$V
docker pull ghcr.io/dimipaun/fugaro-web-node:$V
docker pull ghcr.io/dimipaun/fugaro-go:$V
docker pull ghcr.io/dimipaun/fugaro-java-services:$V
docker pull ghcr.io/dimipaun/fugaro-history:$V
```

`fugaro version` prints `X.Y.Z` (no `v`); a `go install` build prints `dev` because the version is injected only by GoReleaser's `-ldflags`. SBOMs are the `*.sbom.json` assets (SPDX/syft JSON, one per archive).

## Rolling back

Delete the GitHub Release and the tag (`gh release delete vX.Y.Z --cleanup-tag -R dimipaun/fugaro`), delete the images' `X.Y.Z` tag from the package page (and re-point `:X` at the previous release if it moved), and revert the cask commit in `dimipaun/homebrew-tap`. The Go module proxy caches `vX.Y.Z` forever, so `go install ...@vX.Y.Z` keeps working with the old content; never reuse a version. Ship the fix as `vX.Y.(Z+1)` and, if the bad version is dangerous, add a `retract` directive to `go.mod`.
