# Releasing Fugaro

One SemVer tag `vX.Y.Z` releases everything (design section 12): the binary, the base images, the Terraform modules at the tag, the schemas at the tag, and the plugin. Before 1.0, minor versions may break.

## What a tag does

A push of a strict `vX.Y.Z` tag triggers two independent workflows:

- `release.yml` (this document): checks the tag, then runs GoReleaser, which builds `fugaro` for darwin and linux on amd64 and arm64, writes `checksums.txt`, an SBOM per archive (syft), a keyless cosign signature of `checksums.txt`, and the GitHub Release with a grouped changelog; it also pushes the Homebrew cask. A `v0.x.y` release is then marked as a pre-release (GoReleaser's `prerelease: auto` only recognises `-rc1`-style suffixes, which the strict tag rule never produces).
- `images.yml`: builds, smoke tests, scans and publishes `ghcr.io/dimipaun/fugaro-web-node:X.Y.Z` and `:X` (`:X` moves only to the highest release of that major).

Neither workflow runs the test suite; CI already did on main. `release.yml` refuses to publish unless the tagged commit is reachable from `origin/main`, the checks `test`, `terraform` and `rules` all succeeded on it, and the plugin version equals the tag.

## Secrets

- `HOMEBREW_TAP_GITHUB_TOKEN`: a fine-grained personal access token with `Contents: Read and write` on the single repository `dimipaun/homebrew-tap`. Add it as a repository secret. If it is absent the release still succeeds and only the tap update is skipped (push the cask by hand from `dist/homebrew/Casks/` of a run, or re-run once the secret exists).
- Nothing else: the release uses the workflow's `GITHUB_TOKEN` (`contents: write`) and GitHub OIDC (`id-token: write`) for cosign.

## Cutting a release

1. Main is green: `test`, `terraform` and `rules` passed on the commit to be released (`gh run list --branch main`).
2. Bump the plugin and commit it through a PR to main:

   ```sh
   scripts/bump-plugin-version.sh X.Y.Z   # plugin/.claude-plugin/plugin.json and .claude-plugin/marketplace.json
   ```

   `go test ./plugin/` only requires a non-empty version; the release workflow enforces `version == tag` (`scripts/bump-plugin-version.sh --check X.Y.Z`).
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
shasum -a 256 -c checksums.txt --ignore-missing

# Each channel
brew install dimipaun/tap/fugaro && fugaro version
go install github.com/dimipaun/fugaro/cmd/fugaro@v$V
docker pull ghcr.io/dimipaun/fugaro-web-node:$V
```

`fugaro version` prints `X.Y.Z` (no `v`); a `go install` build prints `dev` because the version is injected only by GoReleaser's `-ldflags`. SBOMs are the `*.sbom.json` assets (SPDX/syft JSON, one per archive).

## Rolling back

Delete the GitHub Release and the tag (`gh release delete vX.Y.Z --cleanup-tag -R dimipaun/fugaro`), delete the images' `X.Y.Z` tag from the package page (and re-point `:X` at the previous release if it moved), and revert the cask commit in `dimipaun/homebrew-tap`. The Go module proxy caches `vX.Y.Z` forever, so `go install ...@vX.Y.Z` keeps working with the old content; never reuse a version. Ship the fix as `vX.Y.(Z+1)` and, if the bad version is dangerous, add a `retract` directive to `go.mod`.
