# Review plan: one read, not three in parallel

The review prompt asks the reviewer to score every finding on three axes plus a
class, and then to decide "ship" or "changes." Today the pieces that feed it —
the scorecard instructions, the PR body, and the diff — are assembled in
parallel as if they were independent, but a real review is one read. The
reviewer compares the diff against the author's intent, and the PR body carries
that intent. When the two are disentangled, the reviewer must hold the author's
claim in mind while reading the diff; a scorecard dropped on top of that reading
is noise.

## What the reviewer gets today

Three things are handed to the reviewer, assembled independently:

1. **The diff**, built by `buildReviewInput` (`reviewinput.go`): `git diff`
   text appended to the prompt, binary files reduced to markers. The diff is
   rebuilt every round (fix rounds move the branch).

2. **The PR body**, assembled by the author's implementing stage and written to
   `pr.md`. The runner reads it to post the PR, but the reviewer never sees it.
   The reviewer is told "You are reviewing a pull request written by another
   engineer" — it reads the diff and the tree and must infer intent from those
   alone.

3. **The scorecard** (`reviewScoring`: likelihood, impact, fix_cost, class,
   severity label, plus the "ship"/"changes" ask). This is appended to the
   prompt after the scope line. The scoring anchors (D1) are the same words
   every time.

These three compose into one prompt through `reviewBrief` → `reviewPromptWithInput`,
but they are stitched as three independent strings. The reviewer reads the scope
line, then the scoring contract, then the diff; the PR body is nowhere in that
chain.

## The gap

A real reviewer reads the PR body _first_, to understand what the author thinks
they did. Then they read the diff and check whether the code does that. Then
they weigh findings against the bar the project sets. Today's prompt skips the
first step entirely and asks for scoring the moment the diff is read, which
produces findings that score the diff as text rather than scoring the change as
the author's intent.

The three pieces are not three independent materials — they have an order, and
each tells the reviewer something the next needs. When they are run in parallel
(the scorecard asks for scoring, the diff asks for reading, the PR body is
absent), the reviewer answers three questions at once, and the composite answer
is noisier than any one of them alone.

## The plan

One read, in this order:

### 1. Read the PR body

The reviewer reads the PR body — the author's description of what this change
does and why. This is the intent. It is presented first, quoted as data
(safetext, as FocusPrompt does for untrusted text), not as instruction. The
reviewer forms a mental model of the change from the author's words.

Concrete pieces:
- The title (one line, from pr.md's first line or the task's title).
- The description (the rest of pr.md, scrubbed of status markers as
  `earlyPRText` already scrubs them).
- "This is the author's description of what the change does. Read it to
  understand the intent."

### 2. Read the diff against the intent

The reviewer reads the diff — the same `buildReviewInput` text today's prompt
carries — but now they hold the author's claim. They check: does the code do
what the PR body says? Are there gaps? Does it do things the description does
not mention?

Concrete pieces:
- The same diff header and text `buildReviewInput` builds.
- A line that connects it to step 1: "The change under review follows. Check
  whether it does what the author described, and whether it does anything the
  description does not mention."

### 3. Score under the contract

The reviewer scores the findings they already made against the project's
scoring contract. The contract is the same anchored axes (likelihood, impact,
fix_cost, class, severity label), but it comes after the reading, not before
it. The ask is the same ("ship" or "changes") but the findings that go into it
were formed from intent-versus-code, not from diff-as-text.

Concrete pieces:
- The same scoring axes and severity labels.
- A line that says: "Score each finding you made against this contract."
- The same verdict ask.

### 4. Conclude

One verdict. If the code does what the author said and nothing the description
does not mention, and no finding blocks, "ship." Otherwise "changes" with one
finding per issue.

## What changes in the prompt

The prompt becomes one block, not three. `reviewBrief` (prompts.go) builds it
from these materials in this order, and `reviewPromptWithInput` (reviewinput.go)
inserts the diff at step 2 rather than appending it after the scoring contract.

The PR body is read from `pr.md` at review start (the file is already written
by the implement stage) and passed through `earlyPRText`'s scrubbing — the same
scrubbing that prepares it for the provider. The review prompt builder gets the
scrubbed title and body as parameters.

## What does not change

- The scoring contract (the anchored axes, the severity labels, the class set)
  is the same. It changes only where it appears in the prompt.
- The diff builder (`buildReviewInput`) is the same. It changes only where its
  text is stitched into the prompt.
- The verdict reader (`verdict.go`, `VerdictSchema`) is the same. The reviewer
  answers to the same shape.
- The review loop (`seniorLoop`, `adversarialLoop`, `firstLine`) is the same.
  Each round rebuilds the diff-in-context prompt exactly as today.
- The adversarial preamble (`adversarialReview`) is the same. It still asks the
  reviewer to find what the previous level missed.
- The fix prompt (`FixPrompt`) is the same. It still lists the findings and
  asks the coder to fix them.

## What the reviewer is told (the new prompt shape)

```
You are reviewing a pull request written by another engineer. Look for
correctness bugs, missing or weak tests, security problems, and deviations from
this repository's conventions (see CLAUDE.md if present). Ignore pure style
preferences.

## Author's description

The author described the change as:

> [title]
>
> [description]

Read this first: it tells you what the author thinks the change does. Then read
the diff and check whether the code does that.

## Changes

[buildReviewInput text — the diff header and the diff itself]

Check whether the code under review does what the author described above, and
whether it does anything the description does not mention.

## Scoring

Score every finding you made against this contract:

[the same scoringAxes text, with the same severity labels and the same ask]

When you are done, give your verdict: "ship" if nothing must change before this
is merged, otherwise "changes" with one finding per issue that must be fixed.
```

When the PR body is absent (the task has no author description, which is the
case for a task that names only the change and no why), the "Author's
description" section says so rather than skipping: "The author gave no
description for this change" — a missing body is itself a finding.

### Adversarial level

The adversarial level gets the same shape but the description is the same
author's intent the earlier levels already read. The preamble still asks it to
find what the earlier level missed, and the description is still there to check
against — a gap between intent and code is what an adversarial read is for.

### Review-mode runs

A review-mode run (`fugaro run --pr N --recipe review-only`) reads the PR body
from the provider (the PR's own title and description) rather than from pr.md,
since the implementing stage did not run in this job. The rest of the shape is
the same.

## Implementation notes

- `reviewBrief` gains title and body parameters. When both are empty, the
  "Author's description" section says the author gave none.
- `reviewPromptWithInput` inserts the diff between the description block and
  the scoring block, not after both.
- `reviewRev` and the diff input are unchanged: the diff is still rebuilt per
  round and still cut at `maxReviewDiffBytes`.
- The PR body is read once at review stage start, never per round: the author's
  description is part of the task, not of the branch, and a fix round does not
  rewrite the task.
- The body is scrubbed the same way the provider gets it (`earlyPRText`'s
  scrubDescription) so no forged markers reach the reviewer.
- The adversarial prompt (`AdversarialPrompt`) gets the same shape: the
  adversarial preamble still leads, and the description-and-diff-and-scoring
  block follows it in the same order.
- Pick runs (`PickPrompt`) do not get this shape: they compare several
  checkouts and have no single author description to check against.
- Tests: the docs tests in `internal/cli` and the runner's own prompt tests
  (`prompts_test.go`) pin the shape, so a new shape needs new pinned lines; the
  scoring contract's own words do not change, so the scoring anchors are still
  the same pinned strings.