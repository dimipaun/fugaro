# Launching a run for a repository without its checkout

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved.**

Every decision is answered but one, and each answer is recorded with its date and its reason beside the
decision it answers in [reconciliation.md §3](reconciliation.md). **One answer is not the
recommendation this design made: D902, the credential the read is made with.** The owner's answer is
the installation's **credentials service, person class** — the launcher reads the file as the operator
themselves, and **the CLI holds no App key and reads no environment variable**.

**And that credential cannot read a file today.** The route mints with
`github.CoordinatorTokenPermissions`, which is `pull requests` and `metadata`, read — no `contents` —
so `GET /repos/{owner}/{repo}/contents/fugaro.yaml` is refused with the token the service hands out, and
an earlier round of this design said otherwise. That is why the plan's first task (**A0**) is a change
to the service's own mint, why it is the one question left **open** (**D915**: a permission set of its
own, or a route of its own), and why the design is no longer "no credentials-service change and no
operator step beyond `fugaro update`": the credentials service must be redeployed to serve the new set.
Reconciliation F6 holds the reading; the plan's B2 holds the note the release owes the operator.

**Reconciled against** `origin/main` as merged into this branch at `1625859c` (2026-11-04), with
**#1477 merged**: the refusal this design answers is on `main` now, not only in a pull request.
**Where `reconciliation.md` disagrees with `spec.md`, `reconciliation.md` wins.**

| File | What it holds |
| --- | --- |
| [spec.md](spec.md) | The behaviour: which launch reaches this decision, where the document comes from, at which ref, what it is trusted for, the failure modes and their messages, the console's contract, `--retry` and `--pr`, and the threat model |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists, what is missing, what conflicts (findings F1–F14), then the decisions D901–D919 with the owner's answers |
| [implementation-plan.md](implementation-plan.md) | Tasks A0–A4, B1–B2, C1 and D1, the files each owns, its tests, its red step and its commit, the shared-files table, and the slot this work takes |

## What it is

A run is one execution of one Cloud Run **job**. Which job is decided by the launcher, before the
job is started: a workflow whose `checkout` kind is `clone` runs on a shared **project job**
(`gcp.ProjectJobName(profile, provider, account)`) that the project's published policy defines, and
every other workflow runs on the repository's own job
(`gcp.JobName(slug, gcp.WorkflowVariant(workflow, token))`). The project's image catalog and #1676's
removal of per-repository **images** move the image axis and not the job axis: a repository's own
workflows still run on its own job (reconciliation F14). The kind and the profile come from the
repository's `fugaro.yaml` — from the file at the ref the run checks out.

So a launch that has no checkout of the repository cannot decide the job, and the tree refuses it
(`internal/cli/project_job.go`, #1477, merged):

> `fugaro run --repo X cannot tell which Cloud Run job it targets: the working directory is not X's
> checkout, or its fugaro.yaml could not be read (see fugaro validate). A workflow's checkout: kind
> and profile are read from that checkout's fugaro.yaml, and a clone workflow runs on a shared
> project job where a baked one runs on the repository's own. Run it from X's checkout`

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
read through the installation's credentials service as the operator themselves (D902's answer), on the
single path where the first two are silent and the launch is otherwise refused. When all three are
silent the launch is refused, in the shape the tree refuses today, naming the cause and the way out. A
launch that already works is not touched: none of this runs unless the code is about to refuse.

## The owner's answers, 2026-11-04

Each answer below is the owner's, dated, with the reason the owner gave for it. The full text — the
recommendation it answers and the cost of the alternative — is beside each decision in
[reconciliation.md §3](reconciliation.md).

| # | Answer, 2026-11-04 | Why |
| --- | --- | --- |
| D901 | Automatic, on the one refusing path | As recommended: no new flag, so the console needs no release before it can launch anything |
| D902 | **Read through the credentials service's person class — not the recommendation** | The person class shipped in 0.18.0, so a launcher can read as a person instead of holding the installation-wide App key; **the CLI holds no App key and reads no environment variable**. The class is the answer to *which caller*; the permission it mints with is not one that reaches a file, which is A0's (F6, D915) |
| D903 | `spec.Ref`, the ref the run checks out | As recommended: any other ref turns the launcher's own decision into a paid halt at run start |
| D904 | No cache | As recommended: a document the ref no longer carries must not be able to place a run |
| D905 | Refuse on a read that fails; never fall back to the per-repository job | As recommended: the fallback would silently drop the profile the project's policy chose |
| D906 | The console's contract unchanged | As recommended: every console launch is outside a checkout, so a control would be permanently on |
| D907 | `--retry` and `--pr` reuse the job the run recorded | As recommended: a continuation must stay on the job it continues |
| D908 | A provider no shared job serves proceeds with no read | As recommended: the per-repository job is the same answer the checkout path gives |
| D909 | A checkout whose tree lacks the file reads the ref's objects | As recommended: the local `git cat-file` answers where the working tree does not |
| D910 | A CLI seam over a `FileAt` in `internal/gitprov/github`, built on the `Contents` reader the package already has | As recommended: `gitprov.Provider` must not grow a capability one provider has — but `gitprov.ContentsReader` already is that capability beside it |
| D911 | The minted token's permissions | As recommended, and the recommendation is a task after all: the set the route mints with today (`CoordinatorTokenPermissions`: `pull requests` and `metadata`, read) reaches no file, so the permission is A0's, and it never widens that constant (F6, D915) |
| D912 | Two distinct refusals, one per cause | As recommended: the two causes have two different remedies |
| D913 | `docs/checkout.md` and `docs/git-providers.md` state the new rule | As recommended: both pages describe the launcher's behaviour, and one describes what the CLI holds |
| D914 | An independent, security-sensitive review | As recommended, and wider: with A0 the change is the service's own — its mint's permission set — so the review is the one `.claude/rules/credentials-service.md` requires |
| D915 | **Open** — which permission the person read mint carries: a set of its own beside the coordinator's, or a route of its own | Raised by re-reading the code on 2026-11-04: the mint D902's answer uses reaches no file (F6). The design states both options and picks neither, so A0 waits for the owner. **Cost of option 1:** the token reads the whole repository, and every listed person can mint it for every onboarded repository through the existing person route; "one file" is the launcher's use of it, not a limit on it |

## Numbers this work holds

**D901–D919**, and the block is free. On this tree `main`'s highest block is **`docs/config-management`'s
D1160–D1189** (its highest *written* decision is D1183 and its reserved tail is what names D1189), and
everything above this block is held elsewhere — **D1190–D1219** by the repository-image removal (#1676),
**D1220–D1259** by the stateless-orchestrator work, **D1260–D1289** by #1678, and **D1290–D1309** by
#1492 in the periodic reviews' reading (whose neighbour `docs/run-limit-carry-over` reads that same
pull request as D980–D1010: neither branch is in this checkout, so this is a reading of a moving tree
and a merger re-reads it). Designs written after this one take blocks between D920 and D999 — the
periodic reviews' **D960–D979** and the binary-allow rule's **D981–D997** among them — and none of them
reaches down to D901, so the numbers this design took on 2026-11-01, when `main`'s highest was D900,
stand. **D901–D915 are used** — D915 the open one — and **D916–D919 are held** for the decisions
implementing this design will need; `docs/run-limit-carry-over`, which first drafted D901–D920, has
moved to D1041 and records D901–D914 as this design's. Nothing beyond D919 is reserved here: a later
task that needs one takes D920 and re-checks this line's facts rather than its conclusion.

## What this design does not do

- **No code, and no task approved.** The plan's tasks carry red steps so that the first one to be
  approved can start red, but nothing here is scheduled.
- **No live step of any kind.** Nothing in this design asks the owner to apply Terraform by hand, to
  touch a real installation or to make a cloud call, and nothing asks for a command beyond the one every
  release already asks for, `fugaro update` — which is what redeploys the credentials service A0 changes
  (B2). If a task needs a manual step, that task says so and stops; none does.
- **No new secret, no new stored credential, no new IAM binding and no new principal** — but **not no
  credentials-service change, and not no operator step.** The read is made with the installation's
  **existing credentials service** — its people list, its bundle and its audit line all exist — and the
  operator's own Google identity (D902, D903). What does not exist is a permission the read can be made
  with: the route mints with the coordinator's own set, which reaches no file (F6). A0 adds one (D915),
  so the service's image changes and **an installation has to redeploy it — `fugaro update` does — for
  the read to be served at all.** Until it has, a launch outside a checkout refuses exactly as the tree
  refuses it today (D905), with the checkout named as the way out.
- **It does not touch the runner's placement.** The runner keeps every check it has
  (`checkJobProfile`, `checkProjectJobAccount`) and gains none: a run placed on a job its workflow
  does not select still halts rather than widening.
- **It does not change what a `fugaro.yaml` may select.** A branch may select any profile the
  published policy already admits that repository to — which it may do today, from a checkout at
  that branch. No new authority is granted to a branch author (spec.md §4).
