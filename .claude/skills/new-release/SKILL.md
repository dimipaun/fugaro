---
name: new-release
description: Cut a Fugaro release. Use as "/new-release X.Y.Z" (the version is the argument) when the user asks to release; writes the Highlights, runs scripts/release.sh, verifies the published release.
---

# /new-release X.Y.Z

Procedure for one Fugaro release. Detail lives in `docs/release.md` (read it first, whole); this file is the order of work and the stop rules.

## Rules

- Run only when the user asked for a release of a specific version, or you proposed one and they confirmed it. The tag push is outward-facing and permanent.
- Allowed before the user confirms the version: reading, preflight checks, proposing a version, drafting the Highlights text in the chat or in a local branch. Needs the user's go-ahead: merging the Highlights PR to main and running `scripts/release.sh` (step 4). Without it, stop before step 4.
- Never move, delete or re-create a tag; never force-push; never bypass the release gate. Rolling back a published release is the USER's decision and the user runs it ([docs/release.md#rolling-back](../../../docs/release.md#rolling-back), which deletes the tag); you never run it. Your remedy for a bad release is to supersede it with the next patch release.
- `.claude/` may be locally excluded in a clone (`.git/info/exclude`), so a new file under `.claude/skills` needs `git add -f`.
- No secrets in the Highlights, PR text or your report. Read every check and every script output; do not assume green.
- When any step fails: stop, report the failing command and its output, and wait. Do not improvise a workaround.

## 1. Preflight

- `git switch main && git fetch --prune --tags`; `git status --porcelain` must be empty and `main` must equal `origin/main`. Untracked files count and make `scripts/release.sh` refuse (an untracked `.claude/` did once): list them in `.git/info/exclude`, never commit someone else's files.
- Main's checks (`test`, `terraform`, `rules`) green: `gh run list --branch main --limit 5`. The script waits for running ones.
- The version is the next semver after `git tag --list 'v*' --sort=-v:refname | head -1`. New features mean a minor bump (0.y.0), fixes only a patch; ask the user if unclear. Strict `X.Y.Z`, no `v`.
- `gcpProjectFieldSince` in `internal/cli/init_fugaroyaml.go` must equal the first release containing the `gcp_project:` field (today `"0.4.0"`). If this release is that one, or the value is wrong, fix it in a PR and merge it BEFORE continuing.
- Other checklist items of `docs/release.md` ("Before you tag"): `internal/pricing` `checkedAt`, tag protection and immutable releases (owner setting, only mention if unset).

## 2. Highlights (required from 0.4.0)

- Collect the changes: `git log vPREV..main --oneline` and `gh pr list --state merged --search "merged:>=<date of vPREV>" --limit 100`. Read the PRs, not just titles.
- Write `docs/releases/vX.Y.Z.md` per `docs/releases/README.md`: 3 to 6 user-facing bullets, one sentence each, no commit-title echo, nothing internal-only (CI, tests, refactors). Add `### For operators` with ordered steps when a rollout order matters (shared config: upgrade CLIs and CI pins, rebuild images, then merge `gcp_project:`; `fugaro init --publish-config` is run by the user in their own terminal).
- Branch, commit, push, open a PR; after the user's go-ahead, merge it (squash) once its checks pass; then `git switch main && git pull --ff-only`.

## 3. Dry run

`scripts/release.sh X.Y.Z --dry-run`: it must pass every precondition (including "docs/releases/vX.Y.Z.md exists"). Fix what it reports.

## 4. Release (needs the user's go-ahead)

`scripts/release.sh X.Y.Z` (no prompt). It waits for checks, opens the bump PR with auto-merge, waits for the merge and for the gate on the merge commit, then tags and pushes. Run it in the background and watch its output; a timeout or failure is reported, not retried blindly (the script is safe to re-run, per `docs/release.md`, but read why it stopped first). Then `git ls-remote https://github.com/dimipaun/fugaro 'refs/tags/vX.Y.Z^{}' 'refs/tags/vX.Y.Z'` must print the released commit.

## 5. Verify

Watch both workflows: `gh run list --limit 6` and `gh run watch <id>` for `release` and `images`. Then check, with commands, not by assumption:

- `gh release view vX.Y.Z --json isDraft,isPrerelease,assets,body`: not a draft; `isPrerelease` is false for 0.4.0 and later; assets are 4 archives (darwin/linux, amd64/arm64), an SBOM per archive, `checksums.txt` and `checksums.txt.sigstore.json`.
- The body: install header, then `## Highlights` (your file), then `## Changelog` (no SHAs), then the base-images footer.
- The "Verifying a release" commands of `docs/release.md`: the exact `cosign verify-blob` of `checksums.txt` and the archive checksum for this machine.
- Homebrew tap: `gh api repos/dimipaun/homebrew-tap/commits --jq '.[0].commit.message'` is "Brew cask update for fugaro version vX.Y.Z".
- `images` workflow succeeded, including `verify-public`; note each image digest from its summary.
- Ask the user to run `brew upgrade dimipaun/tap/fugaro && fugaro version` (prints `X.Y.Z`).

## 6. Post-release

- From a fresh build of the tag in a separate worktree (`git worktree add /tmp/fugaro-wt-vX.Y.Z vX.Y.Z`, build with `go build -o /tmp/fugaro-vX.Y.Z ./cmd/fugaro` from there, a path outside the worktree), so the main checkout stays on main (`go install github.com/dimipaun/fugaro/cmd/fugaro@vX.Y.Z` also works but reports `dev`). Run `/tmp/fugaro-vX.Y.Z doctor` against the dogfood project (`dimipaun/fugaro`), report each line, then `git worktree remove /tmp/fugaro-wt-vX.Y.Z`.
- If the release changes the operator order, update the rollout section of `docs/release.md` and the memory notes in the same PR or a follow-up.
- Tell the user what only they can do: `brew upgrade dimipaun/tap/fugaro && fugaro upgrade` in each checkout, in their own terminal (it pins the plugin, updates it in Claude Code and refreshes the job images; `fugaro upgrade --yes` confirms every step, each billable build included), `fugaro init --publish-config` in their own terminal, upgrading CI pins and teammates' CLIs before any repository merges `gcp_project:`, the job images (`fugaro upgrade`'s cloud step, `fugaro image refresh`, stops on a local config `base_images.<kind>` that is a dev or hand-pushed image until that entry is removed), then `fugaro init --anchor` in each checkout to write that line (it refuses while an image was submitted by an older or dev CLI, was built FROM an older or dev base per its record's `base_ref`, or a configured base image is dev or older; plain `fugaro init` also writes it, after the full converge), the owner-only repository settings still unset.
- Final report: version, release URL, commit, what was verified with the command, what was not, and the user's to-do list.
