# `website-promote.yml`: the comments the 2026-11-07 change invalidated

**Status: staged, comment-only, not applied.** `.github/workflows/website-promote.yml`
carries a header and three inline comments that say the `website-live`
environment holds the job for the owner's approval click. The owner removed that
click on **2026-11-07**, so those comments now describe a gate that is not there.
This file holds the corrected comment text. **No executable line changes**, and
the check below proves it.

It is a `.md` file rather than a `.yml` one on purpose: it is not a workflow and
must not be enumerated as a pending workflow by `internal/cli`'s docs tests or by
`website/test/readme.mjs`, both of which read every `.ya?ml` in this directory as
a workflow awaiting the owner's copy-in. The count in this directory's opening
line and in `website/README.md` is therefore unchanged.

## How to apply

Edit `.github/workflows/website-promote.yml` and, for each replacement below,
find the `before` block and put the `after` block in its place, changing nothing
else. Then prove no executable line moved:

```sh
git diff -U0 -- .github/workflows/website-promote.yml \
  | grep -E '^[+-]' | grep -v -E '^(\+\+\+|---)' | grep -v -E '^[+-][[:space:]]*(#|$)'
```

It must print nothing: every changed line is a comment or a blank one. Then
commit, and delete this file (`git rm docs/pending-workflows/website-promote-comments.md`)
in the same commit — a fragment whose change has been applied is a stale
instruction, and there is no byte-identity clause that needs it kept.

**That commit also owes `docs/pending-workflows/README.md` its references back.**
This fragment is named in that page's opening line, in its section `## The three
comment-only fragments (2026-11-07)`, and in the dated note at the top of the
page, and that section writes the three edits out as exact text — including that
the **last** fragment applied is the one that deletes the section. Do them in the
same commit: no test can see a dangling reference here, because the fragments are
`.md` and neither `internal/cli`'s docs tests nor `website/test/readme.mjs`
enumerates them.

`docs/website/preview-promote.md` is the owner-only one-time setup for the
environment, and it carries the same correction in prose.

## 1. The header's "who may run it" paragraph

The paragraph named the environment's required reviewer as one of two locks and
called the sign-off a person's click. Neither is true since 2026-11-07.

**before** (header, the paragraph that opens `# WHO MAY RUN IT`):

```
# WHO MAY RUN IT, and the two locks that make that true. The job carries
# `environment: website-live`, an environment whose ONLY required reviewer is
# the owner, so GitHub holds the job until that reviewer approves it -- and the
# job also refuses to start for any actor other than the repository's owner
# (the `if:` below), so a stray dispatch by anyone else never even queues an
# approval request. The sign-off is a person's click; the federation below
# authenticates the *workflow*, not the person, which is why the person's part
# is this gate rather than a credential.
```

**after**:

```
# WHO MAY RUN IT, and the fences that make that true. The job refuses to start
# for any actor other than the repository's owner (the `if:` below), so a stray
# dispatch by anyone else never runs at all, and it carries
# `environment: website-live`, which is where its deployment branch policy and
# its repository variables live. **Until 2026-11-07 that environment also
# required a reviewer, and that click is what held the job; the owner removed it
# that day.** What fences a promotion now is the actor guard, the environment's
# `main`-only branch policy, the Workload Identity Federation provider's own
# condition on the dispatch ref (`refs/heads/main` and `workflow_dispatch`) and
# the release procedure, which checks the preview before dispatching the
# promotion. The federation below authenticates the *workflow*, not the person,
# which is why the person's part is the guard rather than a credential.
# Restoring the click is one `gh api` call, written out in docs/release.md,
# "What fences a release now".
```

## 2. The header's claim about who the promotion belongs to

`... it is the owner's alone` reads as a claim about a gate. It is still true of
the *dispatch*, so say so.

**before**:

```
# what goes live, and the run itself builds nothing. It is the only path that
# moves the live site, and it is the owner's alone.
```

**after**:

```
# what goes live, and the run itself builds nothing. It is the only path that
# moves the live site, and it is the owner's alone to dispatch.
```

## 3. The `environment:` comment on the `promote` job

**before**:

```
    # The environment: the owner is its only required reviewer, so the job
    # waits for that approval however it was started. Its deployment branch
    # policy also restricts the job to main (docs/website/preview-promote.md),
    # as does the WIF provider's own condition on the dispatch ref.
```

**after**:

```
    # The environment: its deployment branch policy restricts the job to main
    # (docs/website/preview-promote.md), as does the WIF provider's own
    # condition on the dispatch ref. **No reviewer since 2026-11-07**: the
    # approval click that used to hold this job is gone, and docs/release.md
    # ("What fences a release now") says how to put it back.
```

## 4. The "only the owner may promote" step's comment

The step is unchanged and still worth having. Only its last clause, which named
a reviewer, is stale.

**before**:

```
      # The actor check again, as a step: the job's own `if:` is what keeps a
      # non-owner dispatch from ever requesting an approval, and this is the
      # belt to that pair of braces -- if the `if:` were ever weakened or lost,
      # the run stops here with a message naming what happened instead of
      # asking the reviewer to approve a promotion they did not start.
```

**after**:

```
      # The actor check again, as a step: the job's own `if:` is what keeps a
      # non-owner dispatch from running at all, and this is the belt to that
      # pair of braces -- if the `if:` were ever weakened or lost, the run stops
      # here with a message naming what happened, instead of promoting for an
      # actor who never dispatched it.
```
