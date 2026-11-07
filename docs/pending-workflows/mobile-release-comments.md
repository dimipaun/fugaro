# `mobile-release.yml`: the comments the 2026-11-07 change invalidated

**Status: staged, comment-only, not applied.** `.github/workflows/mobile-release.yml`
is applied (2026-10-23), and its header and four inline comments still say that
the `mobile-release` environment holds each job for the owner's approval click —
and its first line still says **STAGED, NOT APPLIED** about a file that has been
running since 2026-10-23. The owner removed the required reviewer on
**2026-11-07**. This file holds the corrected comment text. **No executable line
changes**, and the check below proves it.

It is a `.md` file rather than a `.yml` one on purpose: it is not a workflow and
must not be enumerated as a pending workflow by `internal/cli`'s docs tests or by
`website/test/readme.mjs`, both of which read every `.ya?ml` in this directory as
a workflow awaiting the owner's copy-in. The count in this directory's opening
line and in `website/README.md` is therefore unchanged.

## How to apply

Edit `.github/workflows/mobile-release.yml` and, for each replacement below,
find the `before` block and put the `after` block in its place, changing nothing
else. Then prove no executable line moved:

```sh
git diff -U0 -- .github/workflows/mobile-release.yml \
  | grep -E '^[+-]' | grep -v -E '^(\+\+\+|---)' | grep -v -E '^[+-][[:space:]]*(#|$)'
```

It must print nothing: every changed line is a comment or a blank one. Then
commit, and delete this file (`git rm docs/pending-workflows/mobile-release-comments.md`)
in the same commit — a fragment whose change has been applied is a stale
instruction, and there is no byte-identity clause that needs it kept.

`docs/mobile/eas-signing-runbook.md` is the runbook for this environment, and its
§9 and §10 carry the same correction in prose.

## 1. The first line: this file is applied

**before**:

```
# STAGED, NOT APPLIED (2026-10-20; revised 2026-10-21 after the security review
# of pull request #777). This file is the **mobile app's release workflow**: on
```

**after**:

```
# APPLIED 2026-10-23; staged 2026-10-20 and revised 2026-10-21 after the
# security review of pull request #777. This file is the **mobile app's release
# workflow**: on
```

## 2. The copy-in instructions, which have been carried out

**before**:

```
# To apply it, from the repository root -- **but create the `mobile-release`
# environment and its secret first** (below), because a run against a missing
# environment makes GitHub create it with no required reviewer at all:
#
#     cp docs/pending-workflows/mobile-release.yml .github/workflows/mobile-release.yml
#     git add .github/workflows/mobile-release.yml
#     git rm docs/pending-workflows/mobile-release.yml
#     git commit -m "mobile: build and submit the app on a release tag"
#
# **The staged copy is deleted after the copy-in**, as images.yml's and
# console-linux.yml's are: this is a new workflow with nothing to be
# byte-identical to. Three follow-up edits belong in that same commit: the
# sentence in website/README.md and this README's own opening line, both of
# which count and name the files still pending here, and
# docs/pending-workflows/README.md's `## mobile-release.yml` section's own
# **Status line**, which its tests read against the tree in both directions and
# which therefore has to stop saying "staged and pending" in the very commit
# that applies the file. All three are in that section as exact text.
```

**after**:

```
# **This file was applied on 2026-10-23**: the staged
# `docs/pending-workflows/mobile-release.yml` was copied here and removed. The
# copy-in's record -- what it changed, and the three sentences that had to move
# with it -- is docs/pending-workflows/README.md's `## mobile-release.yml`
# section, which a reader of a pending file has no reason to look in and a
# reader of the applied one has.
```

## 3. Requirement 1: the environment's reviewer is gone

**before**:

```
#   1. the `mobile-release` GitHub environment, with the owner as its only
#      required reviewer, and its deployment branches and tags set to `main`
#      and to tags `mobile-v*`. This is the gate: the job holds an Expo access
#      token and publishes to two stores, so the run waits for a person's click
#      exactly as `console-release` and `website-live` do. **The tag policy is
```

**after**:

```
#   1. the `mobile-release` GitHub environment, with its deployment branches and
#      tags set to `main` and to tags `mobile-v*`. This is the fence: the job
#      holds an Expo access token and publishes to two stores, so what holds a
#      run to a reviewed commit is this deployment branch policy, the release
#      gate and the tag ruleset. **No reviewer since 2026-11-07** -- the owner
#      removed the click from this environment that day, as from
#      `console-release` and `website-live`, and docs/release.md, "What fences a
#      release now", gives the one `gh api` call that puts it back. **The tag policy is
```

## 4. Requirement 1's closing sentence, and the paragraph under it

**before**:

```
#      `v*` would leave every `mobile-vX.Y.Z` tag unable to reach the
#      environment at all.
```

**after**:

```
#      `v*` would leave every `mobile-vX.Y.Z` tag unable to reach the
#      environment at all, and with the reviewer gone the policy is the whole of
#      the environment's own lock.
```

**before**:

```
# **They have to be created before the copy-in, not after it.** GitHub creates a
# missing environment on first use, and the environment it creates that way has
# no required reviewer: the first run would go straight through, holding the
# Expo token, with nobody's click in between. docs/pending-workflows/README.md's
# `## mobile-release.yml` section has both as commands, each labelled with the
# account it is done under, and puts them before the copy-in for this reason.
```

**after**:

```
# **They have to exist before the first run, not after it.** GitHub creates a
# missing environment on first use, and the environment it creates that way has
# no deployment branch policy and no required reviewer -- nothing at all between
# a trigger and a runner. docs/pending-workflows/README.md's
# `## mobile-release.yml` section has both as commands, each labelled with the
# account it is done under, and puts them before the copy-in for this reason.
```

## 5. The header's "a manual dispatch passes a gate too" bullet

**before**:

```
#   - **A manual dispatch that would submit passes a gate too.** The environment
#     approval is a person deciding to *run* this workflow, not a review of the
#     commit it would submit, and a dispatch can name a tag as well as a branch
#     (`gh workflow run --ref v1.2.3`): without the second gate step a dispatch
#     on an off-main branch, or on a `mobile-vtest` tag, would build on EAS and submit
#     to a store behind nothing but that click. So a dispatch whose `dry_run` is
```

**after**:

```
#   - **A manual dispatch that would submit passes a gate too.** A dispatch is a
#     person deciding to *run* this workflow, not a review of the commit it
#     would submit, and it can name a tag as well as a branch
#     (`gh workflow run --ref v1.2.3`): without the second gate step a dispatch
#     on an off-main branch, or on a `mobile-vtest` tag, would build on EAS and
#     submit to a store behind nothing but the environment's branch policy --
#     which admits `main` and the app's tags. So a dispatch whose `dry_run` is
```

## 6. The `environment:` comment on the iOS job

**before**:

```
    # The gate. GitHub holds this job until the environment's only required
    # reviewer -- the owner -- approves it, however it was started. It has to
    # exist before this workflow runs once: a run against a missing environment
    # makes GitHub create one with no reviewer (the header above).
```

**after**:

```
    # The environment, and its fence. **No reviewer since 2026-11-07**: the click
    # that used to hold this job is gone, and what holds a run to a reviewed
    # commit is this environment's deployment branch policy (`main` and tags
    # `mobile-v*`), the release gate below and the tag ruleset. It has to exist
    # before this workflow runs once, or GitHub creates it on first use with no
    # policy and no reviewer at all (the header above). docs/release.md, "What
    # fences a release now", gives the one `gh api` call that puts the click back.
```

## 7. The iOS job's second-gate step

**before**:

```
      # The same gate for the path that has no `push` event, and the step that
      # keeps a dispatch from being the only thing between an off-main commit
      # and a store: the environment's approval is a person deciding to *run*
      # this workflow, not a review of the commit it would submit, and a
```

**after**:

```
      # The same gate for the path that has no `push` event, and the step that
      # keeps a dispatch from being the only thing between an off-main commit
      # and a store: a dispatch is a person deciding to *run* this workflow, not
      # a review of the commit it would submit, and a
```

## 8. The Android job's second-gate step

**before**:

```
      # The same second gate as the iOS job's, for the same reason: a manual
      # dispatch can name a tag or a branch, and the approval click is not a
      # review of the commit it would submit.
```

**after**:

```
      # The same second gate as the iOS job's, for the same reason: a manual
      # dispatch can name a tag or a branch, and the dispatch is not a review of
      # the commit it would submit.
```
