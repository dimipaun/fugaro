# Post-review triage: what a review's findings have to do with the merge

**Status: built and running — the judge, its floor, the cap rule and the follow-ups command are on `main`; the design, its reconciliation and its plan live in [post-review-triage/](post-review-triage/README.md), and this page is not a plan.**

A run reviews its own work, and its reviewer's findings decide whether the pull
request it opened is published ready or stays a draft. A reviewer reports
everything it finds, and most of what it finds is not worth holding a merge for:
a doc sentence that drifted by a word, a name, a small edge case on a path
nobody walks. **Triage** is the step between the review and the fix that weighs
those findings against the task the run was given, and answers one question per
finding:

> Would a [persona] hold this merge for this?

Two answers, and only two:

- **Block** — fixed in this patch before the merge. The finding goes to the
  fixer, and while it stands the pull request stays a draft.
- **Defer** — filed as a follow-up task, and the merge goes ahead without it. A
  deferred finding never reaches the fixer, so the run spends no round on it.

The judge decides nothing about readiness itself. Each mode's **threshold table**
still decides which findings block, and the judge is let inside it: it weighs
only the findings the table already blocks, and only adds deferrals among those
(the [rigor modes](rigor-modes.md) are the table). A round whose findings were
all deferred ends the review loop — there is nothing left to fix — and a mixed
round hands the fixer the blocking findings and nothing else, with the
instruction to touch nothing more than those.

## Where it runs, and how to switch it off

In **`startup`** and **`pro`** the stage runs after every review round, at every
review level, and reads the mode reached at that round — a run that rose to
`pro` is judged by `pro`'s persona. The persona is one of two embedded files,
one per mode: Startup's is the 10x cowboy CTO with a deadline, who blocks
correctness, a broken build or test, security and data loss and writes the rest
down as debt; Pro's is the staff engineer at an established company, who blocks
a real bug and a meaningful edge case and defers the cosmetic. **No other mode
makes the call**: `poc` and `critical` judge findings by their table alone, and
`hotfix` blocks too few for the judge to matter — its table blocks only security
and unscored findings, and both are floor.

A repository turns the stage off with one key in its own `fugaro.yaml`:

```yaml
agent:
  triage: false
```

The key is read from the **base branch**, the way the mode is, and never from
the pull request's own checkout — a branch under review cannot switch off the
judge that is reviewing it. It is also refused to every other layer: a
launcher-written project layer, and the layer a launcher embeds in the task,
cannot set it. The owner of the project has the second switch, one for every
repository in it, in the installation's own config; `fugaro update` publishes it
into the project's published policy, and the two answers merge tighter-only, so
**off wins wherever it is written and a repository can never turn the stage back
on under a policy that turned it off.**

Triage is a model call, and it can fail: a call that times out or comes back
unparseable is recorded as a failed round, and **every finding the table blocks
blocks** — the judge fails safe, and a finding it never reached is not a finding
it deferred. A verdict the judge returns for a finding that is not in the round
is dropped rather than acted on, and a finding the judge did not answer blocks,
exactly as a missing verdict does. That is a call like any other in the run's
ledger: the stage runs on the senior reviewer's model and account (a
`stages.triage.claude_account` may name another), it is recorded per round, and
it runs with no tools and no settings sources of its own.

## The floor

Some findings are never the judge's to move, whatever the persona would do with
them. They are the **floor**, and a run's mode reaches them through the same
table:

- a finding of class `security`;
- a finding of class `data_loss`, or an impact of 5 — in every mode but
  `hotfix`, where the table itself tolerates them;
- a finding the reviewer left unscored — the table cannot read it, so it blocks;
- a finding only the owner can settle (`needs_owner`), which stops the loop for a
  person's decision rather than a code change.

A floor finding blocks, is never deferred, is never a follow-up task and never
reaches the follow-up command. Everything the judge defers is therefore outside
the floor, and a mode the table does not know is floor on everything — the
fail-closed reading, the same one an unreadable finding gets.

## Ready is never merged

A pull request whose findings were all deferred becomes **ready**: it leaves its
draft state, its status section is updated and reviewers are requested. Nothing
merges it. Fugaro has no merge call at all: a person, or the coordinator, merges
the pull request, and a ready pull request is one fewer step for them rather than
a change that shipped. Readiness is the same rule it is everywhere else — a
verified passing test on the final commit, and a deciding review that either
shipped or left findings the run was allowed to leave — with the deferred set as
the third way to leave them ([recipes.md](recipes.md) states it for recipes).

A merge that has not happened is what the follow-up command waits for, and it is
what "ready, not merged" is load-bearing for: the follow-up tasks are filed
against a base branch that has the parent's change on it, and until the merge
happens the change is not there.

## What was deferred, and where to read it

The run records every verdict, and the record has two blocks in `result.json`:

- **`triage`** — one entry per round: the mode, the review level, the account and
  model the call ran as, whether the call **failed**, and one entry per finding
  with its index, its key, the verdict, the one-line reason, the group key and
  the **source**. The source says who placed it: `agent` for the judge's own
  verdict, `table` for the ones the mode's table placed with no judge involved
  (tolerated, floor, a `needs_owner` finding), `fail_safe` for a round whose call
  failed, and `cap` for a deferral a review cap made (below).
- **`deferred`** — the findings a deferral **took effect** for, each with the key,
  the summary, the reason, the group it belongs to and its **source**: the judge's
  own deferrals (`agent`), the findings the mode's **table tolerates** (`table` —
  the judge's reach is only what the table blocks, so a finding the table already
  lets through is a follow-up with no call behind it, and it is filed as one),
  and a review cap's (`cap`). A finding the floor or the owner flag holds is in
  `triage` and never here: it blocked, whatever any verdict said, and a verdict
  the floor overrode is not a deferral that took effect. This is the block the
  pull request, the report and the follow-up command read.

The pull request's status section and the run's `report.md` carry a **Deferred
(follow-ups)** list — one line per finding the run left open, with the one-line
reason it was left, which is the judge's for a deferral, the table's for a
tolerance and the cap's for a leftover — and the sentence that names the command
that files them. A run that left nothing open gets no such section at all. A run
whose stage is off lists no judge's call — no round is weighed, and the finding
its own table tolerates is recorded where it always was — with the one exception
below: a run that reaches its review cap still records the cap's leftovers, and
those are listed here whether the stage is on or off (D1858).

## Filing the follow-ups: `fugaro followups RUN`

A job cannot create a task, and it should not: it holds no credential that
writes the task store, and the findings it would turn into tasks come from a
reviewer whose text is untrusted data. So the deferrals stay a record until a
person runs the command, after the parent is merged:

```
fugaro followups <run>            # prints the plan, writes nothing
fugaro followups <run> --launch   # files them, at most --max of them
```

The command finds the pull request on the forge by the run's own branch — never
by a number the run's record carries, which the job writes — and **refuses while
that pull request is open** (and refuses a closed one): its findings are still
being worked on, and the base the follow-ups would be cut from does not have the
parent yet. It groups the deferrals by the group key the judge gave them, one
task per group and one per finding where a finding stands alone, and each task
carries:

- `follow_up_of` — the parent run and the keys of the findings it settles;
- `debt: true` — the mark that says the work was left behind by a run's review
  rather than asked for;
- the parent's recorded mode, re-resolved through the floors at launch, so a
  follow-up **never lowers a floor** — and a recorded `hotfix` is not carried at
  all, because a hotfix is a statement about one emergency and not a mode a
  follow-up inherits;
- the finding's text and the judge's reason, **fenced as data** in the brief.

`--max` caps the tasks one call files (10 by default), `--json` prints the same
plan as a versioned document, and a group that already has a run is skipped — a
second call after a partial launch therefore files nothing twice. Nothing is
written without `--launch`; the plan is what the command is for by default.

## Listing the debt: `fugaro runs ls --debt`

`fugaro runs ls --debt` lists the runs whose task is marked `debt` — precisely
the work the follow-ups above filed — and `fugaro runs ls --json` carries
`follow_up_of` on every row that has one, so a script can walk from a run back
to the deferrals it settles. Neither changes any existing field of the listing.

## When a review cap is hit

The rigor mode's review rounds are still capped, and a run that reaches the cap
with findings left over no longer drops them:

- **Startup and POC** finalize **ready** with every non-floor leftover recorded
  as deferred, source `cap` — the same "merge as-is, follow up the rest" the stop
  rule already states;
- **Pro** stays a **draft** at the cap and records the same deferrals, so the
  leftovers are still filed as tasks once a person merges the parent;
- **Critical** is unchanged (it tolerates nothing), and **hotfix** is not triaged
  at all;
- a **floor** finding always holds the draft, at the cap as everywhere else.

The cap's deferrals are the cap's and not the judge's, so turning `agent.triage`
off does not suppress them. They are recorded only where the repository opted
into the modes at all: a repository that configures nothing resolves Pro and
defers nothing, and the cap keeps its older behaviour there.

## What is not settled

- **The floor rests on the reviewer's own labels.** The floor is the class and
  the impact the reviewer wrote, so a coder that can steer a reviewer's wording
  can get a real correctness finding deferred where the table blocks it today.
  The owner accepted this for this release, and the bound (for example, never
  defer a finding of impact 4 or 5) is a change to the judge's reach alone, with
  no wire change.
- **Nothing wires the follow-ups to a scheduler.** `fugaro followups` is a
  command a person (or a later orchestrator) runs; the deferred finding does not
  queue work by itself, and a parent's dependents are not unblocked by anything
  here.
- **Fugaro does not merge.** A ready pull request is the end of a run's part.
  Making Fugaro merge is a separate decision and is not part of this.
