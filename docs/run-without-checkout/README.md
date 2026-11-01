# Launching a run for a repository without its checkout

**Status: design only; nothing is implemented; no task approved.** Every decision below is a
recommendation for the owner, numbered **D901–D919**, and none of them is answered yet.

**Reconciled against** `origin/main` at `432623d9c` (2026-10-31) and the 14 pull requests open on
2026-11-01, re-checked by hand for the numbers this design takes. **Where `reconciliation.md`
disagrees with `spec.md`, `reconciliation.md` wins.**

| File | What it holds |
| --- | --- |
| [spec.md](spec.md) | The behaviour: which launch reaches this decision, where the document comes from, at which ref, what it is trusted for, the failure modes and their messages, the console's contract, `--retry` and `--pr`, and the threat model |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists, what is missing, what conflicts (findings F1–F12), then the decisions D901–D919 |
| [implementation-plan.md](implementation-plan.md) | Tasks A1–A7 and D1–D2, the files each owns, its tests, its red step and its commit, the shared-files table, and the slot this work takes |

## What it is

A run is one execution of one Cloud Run **job**. Which job is decided by the launcher, before the
job is started: a workflow whose `checkout` kind is `clone` runs on a shared **project job**
(`gcp.ProjectJobName(profile, provider, account)`) that the project's published policy defines, and
every other workflow runs on the repository's own job
(`gcp.JobName(slug, gcp.WorkflowVariant(workflow, token))`). The kind and the profile come from the
repository's `fugaro.yaml` — from the file at the ref the run checks out.

So a launch that has no checkout of the repository cannot decide the job, and #1477 makes it refuse:

> `fugaro run --repo X is launched from outside X's checkout, so which Cloud Run job it targets
> cannot be decided: a workflow's checkout: kind and profile are read from the checkout's own
> fugaro.yaml, and a clone workflow runs on a shared project job where a baked one runs on the
> repository's own. Run it from X's checkout`

That refusal breaks the case it was written around. **The console's launch child always runs in the
app's own private, empty directory** (`console/src/main/launch.ts`), never in a checkout — so on any
installation whose policy defines a profile, *every* console launch of *any* repository is refused,
the one the console is looking at included. `fugaro run --retry` and `fugaro run --pr` from outside
the repository's checkout are refused the same way.

This design keeps the refusal for the launches that genuinely cannot be decided and lifts it for the
rest, by letting the launcher read the very document the runner will read — the same file, at the
same ref, from a source that is not the working tree.

**The shape, in one paragraph.** Three sources, tried in this order, and no fourth: the checkout's
**object database** at the ref (`git cat-file`, no network, no credential — today only the auth axis
is read this way); **the job the run being continued recorded** in its `launch.json` (a retry and a
follow-up re-launch the same workflow at the same ref); and **the forge**, one file, at `spec.Ref`,
with the installation's own App credential, on the single path where the first two are silent and
the launch is otherwise refused. When all three are silent the launch is refused, in the shape
#1477 refuses today, naming the cause and the way out. A launch that already works is not touched:
none of this runs unless the code is about to refuse.

## Numbers this work holds

**D901–D919.** The block was re-checked by hand on 2026-11-01, not assumed from a neighbour's line:
the highest number on `origin/main` is **D900** (`docs/rigor-command`, whose own record says the next
task takes D901), and the open pull requests reach **D899** at most (`docs/cloud-senior-review`, PR
#1472, holds D880–D899; `docs/accounts-add` holds D860–D879, merged; `docs/settings-commands` holds
D840–D854 with D855–D859 reserved). `git grep -E '\bD90[0-9]\b|\bD91[0-9]\b'` over `docs/` returns
the rigor design's pointer at D901 and nothing else, and each open PR's own diff carries no number
at or above D900. Nothing beyond D919 is reserved here: a later task that needs one takes D920 and
re-checks this line's facts rather than its conclusion.

## What this design does not do

- **No code, and no task approved.** The plan's tasks carry red steps so that the first one to be
  approved can start red, but nothing here is scheduled.
- **No live step of any kind.** Nothing in this design asks the owner to run `fugaro update`, to
  apply Terraform, to touch a real installation or to make a cloud call. If a task needs one, that
  task says so and stops; none does.
- **No new secret, no new stored credential, no new IAM binding, no new principal.** The read uses
  the installation's existing App and the operator's existing Google identity (D902, D903).
- **It does not touch the runner's placement.** The runner keeps every check it has
  (`checkJobProfile`, `checkProjectJobAccount`) and gains none: a run placed on a job its workflow
  does not select still halts rather than widening.
- **It does not change what a `fugaro.yaml` may select.** A branch may select any profile the
  published policy already admits that repository to — which it may do today, from a checkout at
  that branch. No new authority is granted to a branch author (spec.md §4).
