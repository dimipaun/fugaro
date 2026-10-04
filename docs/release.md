# Releasing Fugaro

One SemVer tag `vX.Y.Z` releases everything (design section 12): the binary, the base images, the Terraform modules at the tag, the schemas at the tag, and the plugin. Before 1.0, minor versions may break.

## What a tag does

A push of a strict `vX.Y.Z` tag triggers two independent workflows:

- `release.yml` (this document): checks the tag, then runs GoReleaser, which builds `fugaro` for darwin and linux on amd64 and arm64, writes `checksums.txt`, an SBOM per archive (syft), a keyless cosign signature of `checksums.txt`, and the GitHub Release with a grouped changelog; it also pushes the Homebrew cask. A `v0.x.y` release is then marked as a pre-release (GoReleaser's `prerelease: auto` only recognises `-rc1`-style suffixes, which the strict tag rule never produces).
- `images.yml`: builds, smoke tests, scans and publishes `ghcr.io/dimipaun/fugaro-web-node:X.Y.Z` and `:X`, and the same two tags of `ghcr.io/dimipaun/fugaro-go`, `ghcr.io/dimipaun/fugaro-java-services` and `ghcr.io/dimipaun/fugaro-history` (`:X` moves only to the highest release of that major). After publishing, the `verify-public` job pulls every reference with no login (`images/verify-public.sh`) and fails the release when one cannot be read or has no `linux/amd64` build; it lists each image's digest in the run summary. Pull requests build, smoke test and publish nothing; the nightly run scans.

Neither workflow runs the test suite; CI already did on main. Both workflows run `scripts/release-gate.sh` first, so a tag on an off-main or red commit publishes nothing (no binaries, no images). It refuses unless the tagged commit is reachable from `origin/main` and the GitHub Actions checks `test`, `terraform` and `rules` all succeeded on it (a check of that name from another app does not count); if CI is still running, re-run the failed job once it is green. `release.yml` also requires the plugin version to equal the tag.

## Making the image packages public (one time per package)

A package that a workflow creates on ghcr.io is private. `fugaro init` mirrors the images with the user's own credentials and a plain `docker pull` has none, so each of the four packages must be public. GitHub has no API call for this with the workflow's token; do it once by hand per package, after the first publish of that image:

1. Open `https://github.com/users/dimipaun/packages/container/fugaro-<name>/settings` for `web-node`, `go`, `java-services` and `history`.
2. Under "Danger Zone", "Change package visibility", choose Public and confirm.
3. Re-run the `verify-public` job of that release's `images` run (or `images/verify-public.sh ghcr.io/dimipaun/fugaro-<name>:X.Y.Z` on any machine, which uses no login).

Until it is done, the first release's `verify-public` job fails by design with "cannot be read without a login (is the package public?)": the images are published and the failure only says the step above is outstanding. Later releases of a public package stay public.

## Secrets

- `HOMEBREW_TAP_GITHUB_TOKEN`: a fine-grained personal access token with `Contents: Read and write` on the single repository `dimipaun/homebrew-tap`. Add it as a repository secret. If it is absent the release still succeeds and only the tap update is skipped, with a `::warning::` and a line in the run summary (add the secret and re-run the job, or copy the cask from a `dry-run` artifact's `homebrew/Casks/` by hand).
- Nothing else: the release uses the workflow's `GITHUB_TOKEN` (`contents: write`) and GitHub OIDC (`id-token: write`) for cosign.

## The cask's quarantine hook

The Homebrew cask runs a post-install step on macOS: `xattr -dr com.apple.quarantine` on the installed `fugaro`. The binaries are not signed or notarized by Apple, so without it Gatekeeper blocks the first run of a downloaded binary. The cost is that Gatekeeper's check is skipped for this binary; integrity rests on the sha256 in the cask, which comes from the release archives, and on the cosign-signed `checksums.txt`. Linux cask support was not tested; Linux users can use the archive or `go install`.

## Cutting a release

1. Main is green: `test`, `terraform` and `rules` passed on the commit to be released (`gh run list --branch main`).
2. Bump the plugin and commit it through a PR to main:

   ```sh
   scripts/bump-plugin-version.sh X.Y.Z   # plugin/.claude-plugin/plugin.json; the marketplace entry carries no version and the plugin test rejects one
   ```

   `go test ./plugin/` only requires a non-empty version; the release workflow enforces `version == tag` (`scripts/bump-plugin-version.sh --check X.Y.Z`).

   **The plugin is pinned to this tag (M11, [design §4.3](design/m11-setup-and-skills.md)).** `fugaro init` and `fugaro update-skills` write the release tag `vX.Y.Z` of the running binary as the `ref` of the Fugaro marketplace in a repository's `.claude/settings.json`, so the binary, the plugin and the pin agree only if the plugin at that tag has version `X.Y.Z`: that is what the gate above enforces, and why a published tag is never moved or re-cut (a pin to it would silently change). When M11 lands, `bump-plugin-version.sh` also rewrites, and `--check` also verifies, the version in each skill's do-not-edit header. A private fork hosts its own marketplace and plugin at its own tags and its users pin to those.
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
```

`fugaro version` prints `X.Y.Z` (no `v`); a `go install` build prints `dev` because the version is injected only by GoReleaser's `-ldflags`. SBOMs are the `*.sbom.json` assets (SPDX/syft JSON, one per archive).

## Rolling back

Delete the GitHub Release and the tag (`gh release delete vX.Y.Z --cleanup-tag -R dimipaun/fugaro`), delete the images' `X.Y.Z` tag from the package page (and re-point `:X` at the previous release if it moved), and revert the cask commit in `dimipaun/homebrew-tap`. The Go module proxy caches `vX.Y.Z` forever, so `go install ...@vX.Y.Z` keeps working with the old content; never reuse a version. Ship the fix as `vX.Y.(Z+1)` and, if the bad version is dangerous, add a `retract` directive to `go.mod`.
