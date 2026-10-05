# Releasing Fugaro

One SemVer tag `vX.Y.Z` releases everything (design section 12): the binary, the base images, the Terraform modules at the tag, the schemas at the tag, and the plugin. Before 1.0, minor versions may break.

## What a tag does

A push of a strict `vX.Y.Z` tag triggers two independent workflows:

- `release.yml` (this document): checks the tag, then runs GoReleaser, which builds `fugaro` for darwin and linux on amd64 and arm64, writes `checksums.txt`, an SBOM per archive (syft), a keyless cosign signature of `checksums.txt`, and the GitHub Release with a grouped changelog; it also pushes the Homebrew cask. A `v0.x.y` release is then marked as a pre-release (GoReleaser's `prerelease: auto` only recognises `-rc1`-style suffixes, which the strict tag rule never produces).
- `images.yml`: builds, smoke tests, scans and publishes `ghcr.io/dimipaun/fugaro-web-node:X.Y.Z` and `:X`, and the same two tags of `ghcr.io/dimipaun/fugaro-go`, `ghcr.io/dimipaun/fugaro-java-services` and `ghcr.io/dimipaun/fugaro-history` (`:X` moves only to the highest release of that major). After publishing, the `verify-public` job pulls every reference with no login (`images/verify-public.sh`) and fails the release when one cannot be read or has no `linux/amd64` build; it lists each image's digest in the run summary. Pull requests build, smoke test and publish nothing; the nightly run scans.

Neither workflow runs the test suite; CI already did on main. Both workflows run `scripts/release-gate.sh` first, so a tag on an off-main or red commit publishes nothing (no binaries, no images). It refuses unless the tagged commit is reachable from `origin/main` and the GitHub Actions checks `test`, `terraform` and `rules` all succeeded on it (a check of that name from another app does not count); if CI is still running, re-run the failed job once it is green. `release.yml` also requires the plugin version to equal the tag.

## Making the image packages public (one time per package)

The mirror (below) reads the source with no credentials, and a plain `docker pull` has none either, so each of the four packages must be public. **In practice a package a workflow publishes from this public repository is public from the start** (it inherits the repository's visibility): the v0.2.0 release published `fugaro-go`, `fugaro-java-services` and `fugaro-history` for the first time, `verify-public` passed with no manual step, and all four manifests answered 200 anonymously. So after a release that publishes a new package, only check that it is public: the `verify-public` job passing is that check.

If a package ever turns out private (`verify-public` fails with "cannot be read without a login (is the package public?)"): GitHub has no API call for this with the workflow's token, so do it by hand:

1. Open `https://github.com/users/dimipaun/packages/container/fugaro-<name>/settings`.
2. Under "Danger Zone", "Change package visibility", choose Public and confirm.
3. Re-run the `verify-public` job of that release's `images` run (or `images/verify-public.sh ghcr.io/dimipaun/fugaro-<name>:X.Y.Z` on any machine, which uses no login).

Later releases of a public package stay public.

## How `fugaro init` uses the images (the mirror)

Cloud Build and Cloud Run in a user's project cannot read ghcr.io, and the project's base registry `fugaro-base` is writable only by people, never by a build account. So the `images` stage of `fugaro init` copies the images, as the user, from `ghcr.io/dimipaun/fugaro-<kind>:X.Y.Z` (the running binary's version; `--image-source ghcr.io/<owner>` for a fork) into `<region>-docker.pkg.dev/<gcp-project>/fugaro-base/`: the history image with `--firebase` (it lands as `history:latest`, which the history job runs) and a base kind when `--base <kind>` names it or the checkout's `fugaro.yaml` does (recorded in `base_images`). It is plain HTTP against the registry API: no Docker, only the `linux/amd64` image, each digest verified, nothing sent when the destination already has the same digest. A development build copies nothing and says how to build from a checkout.

What a release must therefore guarantee is what `verify-public` checks: every published reference is anonymously readable and has a `linux/amd64` build. Until a package is made public (above) a user's `init` is refused with the unreadable-source message. The trust anchor is the tag as ghcr.io resolves it: users who want more pin the digest from the `verify-public` run summary with `--expect-digest KIND=sha256:<hex>` (KIND is `go`, `java-services`, `web-node` or `history`). The release notes do not list the digests (GoReleaser runs in another workflow and cannot know them); signing the images and verifying before the copy is a planned follow-up (the plan's F1). **Which images exist when:** v0.1.0 published only `fugaro-web-node`; the next tag is the first to publish `fugaro-go`, `fugaro-java-services` and `fugaro-history` (v0.2.0, whose `verify-public` passed).

## Secrets

- `HOMEBREW_TAP_GITHUB_TOKEN`: a fine-grained personal access token with `Contents: Read and write` on the single repository `dimipaun/homebrew-tap`. Add it as a repository secret. If it is absent the release still succeeds and only the tap update is skipped, with a `::warning::` and a line in the run summary (add the secret and re-run the job, or copy the cask from a `dry-run` artifact's `homebrew/Casks/` by hand).
- Nothing else: the release uses the workflow's `GITHUB_TOKEN` (`contents: write`) and GitHub OIDC (`id-token: write`) for cosign.

## The cask's quarantine hook

The Homebrew cask runs a post-install step on macOS: `xattr -dr com.apple.quarantine` on the installed `fugaro`. The binaries are not signed or notarized by Apple, so without it Gatekeeper blocks the first run of a downloaded binary. The cost is that Gatekeeper's check is skipped for this binary; integrity rests on the sha256 in the cask, which comes from the release archives, and on the cosign-signed `checksums.txt`. Linux cask support was not tested; Linux users can use the archive or `go install`.

### The `postflight` deprecation warning

Homebrew warns that a cask using a `postflight` quarantine removal is deprecated (it is moving to refuse unsigned, un-notarized casks). That warning comes from Homebrew, not from GoReleaser: the `postflight` block is the one this repo's `hooks.post.install` asks for, GoReleaser v2.18.2 (the pinned version) emits `hooks.post.install` as `postflight` with no alternative stanza, and `.goreleaser.yaml` uses none of the config fields v2.18.2 flags as deprecated (`binary`, `manpage`, `url.verified`, `conflicts.formula`). So there is no supported config change to make offline, and none was made. What would remove it: sign and notarize the macOS binaries (GoReleaser's `notarize` support, which needs an Apple Developer ID certificate and API key as secrets), then delete `hooks` from the cask. Until then the hook stays; if Homebrew starts disabling such casks, `brew install` of the tap cask will fail and users fall back to the archive or `go install`.

## Cutting a release

**Before you tag (the checklist; each item has been missed or is easy to miss):**

- **Run `scripts/bump-plugin-version.sh X.Y.Z` and commit it** (through a PR to main). The release gate refuses a tag whose plugin version or any skill header differs from it, and a repository pinned to the tag would otherwise load a plugin that disagrees with the binary.
- **Check that each new ghcr.io package is public** (see "Making the image packages public"): a package published from this public repository inherits its visibility (it did for v0.2.0), so this is only a check; `verify-public` passing is the proof.
- **Turn on tag protection and immutable releases (the repository owner's setting; Fugaro does not change repository settings).** A git tag is mutable, and the plugin pin and the image tags are anchored to it, so: add a GitHub ruleset that restricts updates and deletions of `v*` tags (Settings, Rules, Rulesets, target tags `v*`, restrict updates and deletions), and enable immutable releases. Without them, anyone who can push tags can move `vX.Y.Z` after users pin to it.
- **Refresh `internal/pricing`'s `checkedAt` date** after checking the vendor's price page (`fugaro budget prices` warns that the table is older than 90 days: the table was last checked 2026-09-30, so it warns from 2026-12-29).
- The binary prints the commit it was built from (`fugaro doctor --plugin`, `fugaro update-skills`) with the one line that checks the tag: `git ls-remote https://github.com/dimipaun/fugaro 'refs/tags/vX.Y.Z^{}' 'refs/tags/vX.Y.Z'` must print that commit: on the `^{}` line for an annotated tag, on the only line for a lightweight tag (the `^{}` pattern alone prints nothing for one, which is why both are given). Run it once after tagging.

`scripts/release.sh X.Y.Z` does the whole thing from a clean `main`: it checks every precondition below, bumps the plugin through a PR, waits for it to merge, verifies the merge commit, then asks before tagging. It needs `gh` installed and authenticated (`gh auth login`) and nothing else; it never force-pushes, never pushes to `main` directly, and never deletes or re-tags a release.

1. Preconditions, checked in order and stopping at the first failure: `X.Y.Z` is strict SemVer (no `v`, no pre-release suffix, no leading zeros); the working tree is clean (**untracked files count: a stray file blocks the release**, so commit, remove or ignore it first); the current branch is `main` and equals `origin/main` after a fetch; `gh` is installed and authenticated and targets the repository `origin` points at; the tag `vX.Y.Z` doesn't already exist on origin (a release is never re-tagged, see "Rolling back" below; a tag that exists only locally is handled at step 5); `X.Y.Z` is greater than the latest released tag (only strict `vX.Y.Z` tags count, so `-rc` and four-part tags are ignored); and `test`, `terraform` and `rules` all succeeded on the tip of `main` (reusing `scripts/release-gate.sh`). Checks that are still running on `main` (for example a just-merged PR) are waited for, polling and naming the pending check, up to `--timeout-minutes`; a failed check fails at once, and one still pending at the timeout fails with a clear message.
2. It creates `release/vX.Y.Z` from `main`, runs `scripts/bump-plugin-version.sh X.Y.Z` (below), commits and pushes it, opens a PR to `main` and turns on squash auto-merge. If auto-merge is unavailable (it is off for the repository), it says so and stops: merge the PR yourself, then re-run.
3. It polls (every 30s, 45 minutes by default; `--timeout-minutes N`, a whole number of at least 1, to change it) until that PR merges, stopping immediately if it's closed unmerged or one of its checks fails. A cancelled check gets one more poll before counting as a failure.
4. Once merged, it pulls `main`, requires `HEAD` to be the PR's merge commit (if anything else landed on `main` first it stops, inspect before tagging), re-checks the plugin version, and then **waits for the release gate on the merge commit**: CI starts on that commit only now, so its `test`, `terraform` and `rules` are first missing, then running. It polls (same interval, up to `--timeout-minutes` again), printing which check is still pending, and fails at once on a conclusive failure; a check that is still pending or cancelled when the wait ends fails the release. **Identical-tree shortcut.** A squash merge creates a new commit, so CI starts from nothing on it even though a version bump changes no code and the PR head already passed. While `test`, `terraform` or `rules` is missing, running or cancelled on the merge commit (and none failed on it), the gate accepts the checks of the pull request's head instead, and prints `using the checks of <sha> (identical tree)`; the wait ends at once. The candidate counts only if all of these hold, each read from the GitHub API rather than assumed: it is the head of a pull request that is merged and whose `merge_commit_sha` is exactly the commit being gated (so an unrelated commit with the same tree on another branch, or an open or closed-unmerged PR, cannot vouch); its tree id equals the commit's tree id (identical content to the commit being gated; the PR's checks ran on its head merged with the base of that moment, since `pull_request` jobs check out the merge ref, and that is accepted because the tree equals the merged result that became the commit); and `test`, `terraform` and `rules` are all `success` on it from the GitHub Actions app, as strict as on the commit itself (failed, cancelled, running or missing never counts). A conclusive failure on the commit's own runs is never overridden, and a commit with its own complete green checks never triggers the lookup. The lookup needs no token on a public repository, so the tag workflows (`release.yml`, `images.yml`) may use the shortcut too, under the same rules; if the repository ever goes private it fails with a one-line `identical-tree lookup unavailable (...)` note on stderr and the gate falls back to waiting for the commit's own checks. Only the first 100 pull requests associated with the commit are read. It then prints a summary (tag, commit, checks) and the standard reminder that a pushed tag is permanent, and asks `Create and push tag vX.Y.Z? [y/N]` (default no; `--yes` answers it, nothing else).
5. On yes, it creates the annotated tag and pushes it (`git push origin refs/tags/vX.Y.Z`) and prints the two workflow run URLs to watch, plus this page's post-release checklist (verify-public, making a new image package public once, verifying the release). If the push fails the tag stays local, the script prints the exact command to run by hand, and a re-run offers to push it: a local-only tag is accepted only if the release PR is merged and the tag is on the verified merge commit; any other local tag is refused. If origin turns out to already have the tag at that commit, it says so instead of reporting a push.

`--dry-run` stops after printing the plan for step 2, changing nothing. However it ends (an error, Ctrl-C, SIGTERM, SIGHUP) the script switches the checkout back to `main`, and re-running is safe: it finds the state from git and `gh` rather than keeping any of its own. An existing `release/vX.Y.Z` branch with no PR is reused (rebuilt from `main` if `main` has moved on), an open PR is picked up at step 3, and a merged PR goes straight to step 4.

**Recovering from a closed release PR.** If the PR for `release/vX.Y.Z` was closed without merging, the script stops before attempting a merge and prints the recovery: delete the branch (`git push origin --delete release/vX.Y.Z`, plus `git branch -D release/vX.Y.Z` locally) and re-run, which opens a fresh PR; or reopen the PR on GitHub and re-run.

`scripts/release_test.go` covers this against a local bare repository and a fake `gh`: every precondition, argument errors, `--dry-run`, the full happy path (exactly one branch, one commit, one PR, the tag only once the prompt is answered `y`), `--yes`, the post-merge wait (checks missing, pending, cancelled, red), `HEAD` not being the merge commit, a failed bump check, resuming a merged PR, an open PR, a pushed branch without a PR and a stale local branch, a closed PR, a failed push of the branch, the PR creation or the tag (each leaving the checkout on `main`), signals, and a local-only tag.

### Manual steps (fallback)

Only needed if `scripts/release.sh` can't run (no `gh`, or something it doesn't handle):

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
