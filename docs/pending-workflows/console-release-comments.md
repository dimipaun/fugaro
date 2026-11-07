# `console-release.yml`: the comments the 2026-11-07 change invalidated

**Status: staged, comment-only, not applied.** `.github/workflows/console-release.yml`
and its byte-identical staged twin `docs/pending-workflows/console-release.yml`
both carry comments saying the `console-release` environment holds the signing
job for the owner's approval click, and the applied file's first line still says
**STAGED, NOT APPLIED** about a file that has been running since 2026-10-23. The
owner removed the required reviewer on **2026-11-07**. This file holds the
corrected comment text. **No executable line changes**, and the check below
proves it.

It is a `.md` file rather than a `.yml` one (a `console-release-comments.yml`
would be enumerated as a pending workflow and would break the byte-identity
clause between the two files above), and it is applied to **both** copies,
because a change to one of them alone reddens
`TestDocsConsoleReleaseWorkflowIsStagedAndPinned` and with it every release tag.

## How to apply

Edit `.github/workflows/console-release.yml` and, for each replacement below,
find the `before` block and put the `after` block in its place, changing nothing
else. Then prove no executable line moved:

```sh
git diff -U0 -- .github/workflows/console-release.yml \
  | grep -E '^[+-]' | grep -v -E '^(\+\+\+|---)' | grep -v -E '^[+-][[:space:]]*(#|$)'
```

It must print nothing: every changed line is a comment or a blank one. Then make
the staged twin the same text — the three commands the file's own header
documents, in this order — and commit, deleting this file in the same commit:

```sh
cp .github/workflows/console-release.yml docs/pending-workflows/console-release.yml
git add docs/pending-workflows/console-release.yml
git rm docs/pending-workflows/console-release-comments.md
```

`docs/design/desktop-console.md` §9.4 and `docs/release.md` ("What fences a
release now") carry the same correction in prose, and the latter gives the one
`gh api` call that restores the click.

## 1. The first line: this file is applied, and its twin is kept

**before**:

```
# STAGED, NOT APPLIED. This file is the console's signing, notarisation and
# stapling pipeline **on the console's own release tag**, written by the run that
```

**after**:

```
# APPLIED 2026-10-23, and also staged at
# docs/pending-workflows/console-release.yml byte-identical to this file on
# purpose (the docs tests assert it). This is the console's signing,
# notarisation and stapling pipeline **on the console's own release tag**,
# written by the run that
```

## 2. The copy-in paragraph's list of controls

**before**:

```
# Nothing else about the copy-in changes: the protected environment, the
# reviewer and the tag ruleset are the same controls, and this file adds no
# secret to them.
```

**after**:

```
# Nothing else about the copy-in changes: the protected environment and the tag
# ruleset are the same controls -- the reviewer that used to be counted among
# them was removed on 2026-11-07 (docs/release.md, "What fences a release now")
# -- and this file adds no secret to them.
```

## 3. The environment the owner creates before the copy-in

**before**:

```
# **Before the copy-in, the owner creates the protected environment this file
# names** (Settings, Environments, `console-release`): required reviewers = the
# owner, deployment branches restricted to tags. A staging file cannot create an
# environment, and this workflow is only as safe as the environment its secrets
# live in. **The staged copy is NOT deleted after the copy-in**: while both files
```

**after**:

```
# **Before the copy-in, the owner creates the protected environment this file
# names** (Settings, Environments, `console-release`): deployment branches
# restricted to tags, and **no reviewer since 2026-11-07** -- it was the owner
# until then (docs/release.md, "What fences a release now"). A staging file
# cannot create an environment, and this workflow is only as safe as the
# environment its secrets live in. **The staged copy is NOT deleted after the
# copy-in**: while both files
```

## 4. "The real control is the environment's reviewer"

**before**:

```
# THE REAL CONTROL IS THE ENVIRONMENT'S REVIEWER AND A TAG RULESET, NOT THIS
# FILE'S GATE. A tag push runs the workflow file *at the tagged commit*, so a
# tag on a branch whose copy of this file (or of scripts/release-gate.sh) was
# edited runs the edited copy -- with the gate weakened or removed. What stops
# that is outside the file: the `console-release` environment's required
# reviewer (the owner) and a tag ruleset restricting who may create, update or
# delete `v*` tags (docs/release.md, "Before you tag"). **The approver confirms
# the tag's commit is on main before approving the deployment.** The first run
# step below checks the same thing from inside the job (the tagged commit is an
# ancestor of origin/main), which catches a mistake, not an attacker who
# controls the tagged tree.
```

**after**:

```
# THE REAL CONTROL IS A TAG RULESET AND THE ENVIRONMENT'S BRANCH POLICY, NOT
# THIS FILE'S GATE. A tag push runs the workflow file *at the tagged commit*, so
# a tag on a branch whose copy of this file (or of scripts/release-gate.sh) was
# edited runs the edited copy -- with the gate weakened or removed. What stops
# that is outside the file: a tag ruleset restricting who may create, update or
# delete `v*` tags (docs/release.md, "Before you tag"), and the
# `console-release` environment's deployment branch policy, which admits
# `console-v*` tags and nothing else. **Until 2026-11-07 that environment also
# required a reviewer (the owner), whose approval is where the tag's commit was
# confirmed to be on main; the owner removed that click, so the ruleset, the
# branch policy and the release procedure are what hold a run to a reviewed
# commit.** The first run step below checks the same thing from inside the job
# (the tagged commit is an ancestor of origin/main), which catches a mistake,
# not an attacker who controls the tagged tree.
```

## 5. The `environment:` comment on the `release` job

**before**:

```
    # The protected environment the owner creates (plan Task 4.5): required
    # reviewers on the owner, deployment branches restricted to tags. Every
    # secret below is read through it. The approver confirms the console tag's
    # commit is on main before approving (see the header).
```

**after**:

```
    # The protected environment the owner creates (plan Task 4.5): deployment
    # branches restricted to tags, and **no reviewer since 2026-11-07** -- it was
    # the owner, and the approval used to be where the console tag's commit was
    # confirmed to be on main (see the header, and docs/release.md, "What fences
    # a release now"). Every secret below is read through it.
```

## 6. The first step's comment

**before**:

```
      # First: the tagged commit is on main. A tag push runs this file as it is
      # at the tagged commit, so this is a check against a mistake (a tag on
      # the wrong commit), not against someone who edited this file on a
      # branch -- that is the environment reviewer's and the tag ruleset's job
      # (the header). The full history is why the checkout asks for depth 0.
```

**after**:

```
      # First: the tagged commit is on main. A tag push runs this file as it is
      # at the tagged commit, so this is a check against a mistake (a tag on
      # the wrong commit), not against someone who edited this file on a
      # branch -- that is the tag ruleset's job and the environment's branch
      # policy's (the header). No click sits behind this step since the reviewer
      # was removed on 2026-11-07. The full history is why the checkout asks for
      # depth 0.
```
