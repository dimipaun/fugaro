# C3.9's review half: a project-scoped review identity, a read-only token set and a signed verdict

**Status: design only; nothing here is approved and no code follows from this folder.** This document
set answers one brief: the cloud senior review ([docs/cloud-senior-review/](../cloud-senior-review/), PR
#1472) cannot run its first slice — **E1 + R1 + R7 + R2** — because it needs a half of the credentials
service that does not exist and that
[docs/credentials-service/implementation-plan.md](../credentials-service/implementation-plan.md) lists as
**C3.9, "Later, unscheduled"**. That half is three things and no more: the service **admitting a
project-scoped review identity**, a mint that serves the **read-only token set** for a review, and the
**sign and verify endpoints** the verdict's provenance rests on (**D892 (a)**). This folder scopes exactly
those, decides them, and says in as many words what of the general C3.9 it does **not** do.

**Asked by:** the owner, 2026-11-01. **Answered by:** the owner, 2026-11-01 — **every decision below is
answered**: D1062–D1071 as recommended, **D1072 as 0.16.0**, **D1063 amended** by the owner's ruling **O1**,
and **D1073** and **D1074** added in the same review of this folder. Answering a decision is not approving a
task: nothing here is approved to build.
**Reconciled against** `origin/main` at `26bcf0a0` and the open pull requests of 2026-11-01.

## What it is

`docs/cloud-senior-review/`'s **E7** is a precondition rather than a dependency to schedule around: the
review job fetches its read-only installation token from the credentials service by name (**D893 (c)**),
and the service signs the verdict (**D892 (a)**). Two halves of E7 must hold, and only one of them is a
deployment step:

1. the service **enabled for the installation** — `fugaro update --provision-credentials`, the owner's own
   typed confirmation, the App key in the bundle: expressible in `fugaro update` terms, and the credentials
   service's own documented steps;
2. the service **admitting the review job's own account**, which is a **project-scoped** job — the one
   class of caller **Global Constraint 6** refuses whole until the phase-3 shared-job rules are on `main`
   — and holding a per-account **review-signing key** with the endpoints that use it.

This folder is the second half. It is a **slice of C3.9, not C3.9**: shared jobs for *coding* runs stay
refused, a coding caller's model path keeps its rule while a **review** caller is admitted to the model path
for its own account only (**D1063**, amended by the owner's ruling O1), and the phase-3 shared-job rules stay
unwritten (**D1070**).

## Why a folder and not one file in `docs/credentials-service/`

The brief allowed either, and the folder is the recommendation, for three reasons that are about this
repository rather than about taste:

- **The documents have four different roles** — the design, the reconciliation against the code, the
  decisions, and the ordered tasks — and every design written in this tree that carries decisions *and* a
  plan keeps them apart in a folder of its own
  ([docs/run-limit-carry-over/](../run-limit-carry-over/), [docs/accounts-add/](../accounts-add/),
  [docs/cloud-senior-review/](../cloud-senior-review/)). One file would either be four documents in one
  or would drop the reconciliation, which is the part a reviewer reads first.
- **`docs/credentials-service/`'s own documents are pinned and parsed**: its `implementation-plan.md` is
  read by `TestPlanTestListsNamesOnlyTestsThatExist` (`internal/cli/docs_credentials_plan_tests_test.go`)
  and by the `docs_credentials_*` guards, and its `spec.md` and `reconciliation.md` hold **D130–D154** and
  **O1–O4**. A fifth file in that folder would sit beside documents this design does **not** amend, and a
  reader would have to work out which of the two governs.
- **The plan is not amended here.** C3.9 keeps its meaning for coding runs; what changes is the *scope* of
  Global Constraint 6's refusal (**D1063**), and the task that writes the code carries that amendment into
  `implementation-plan.md` in the same pull request (`implementation-plan.md` §"The amendment the
  implementing pull request carries"). Writing it here would either be an amendment nobody applies or a
  second copy of a plan.

## The files

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The design: the account classes, the two identities and their pairing, the read-only mint, the sign and verify endpoints, the key and its rotation, the threat model, the owner-run live check, the `fugaro update` story, and what the slice does not do |
| [reconciliation.md](reconciliation.md) | Every claim of the spec against the code on `origin/main` (**F1–F14**, each Exists, Missing or Conflicts) and the owner decisions **D1062–D1074**, each written to the recommended answer first, with the cost of its alternative, and each answered by the owner on 2026-11-01 (D1063 as amended by ruling O1). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks **C3.9a–C3.9g** (at most 10 files each) with named tests, red steps, SEC flags and gates, the shared-files table, the named live check, and the release slot |

**How a `:line` in this folder reads.** A citation names a file and a symbol, and the number is the
symbol's **declaration** line as it stood at `26bcf0a0`; a line drifts the first time anyone edits the file
above it, so the symbol, not the number, is what a reader should search for, and every claim also states
the fact in words.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D1062–D1074** | this work's block: **13 decisions, every one answered by the owner on 2026-11-01** — D1062–D1071 as recommended, **D1072** as 0.16.0, **D1063** as amended by the owner's ruling **O1**, and **D1073**/**D1074** added in the same review of this folder. The block was reserved as **D1062–D1075** so that a decision added in review did not collide with another work; **D1075 is the one number still unused**. Taken above every number on `main` — whose highest written is **D1061** ([docs/run-limit-carry-over/reconciliation.md](../run-limit-carry-over/reconciliation.md) §3, merged) — and above every open pull request's: #1481 holds D901–D914, #1487 D920–D934, #1488 D940–D959, #1490 D960–D979, #1491/#1492 D980–D1010, #1496 D1011–D1040 and #1472 holds **D880–D899** with **D894 written and answered (a)** and **D895–D899 reserved and unused**. That is the launcher's **2026-11-01 reading**, taken by fetching every open pull request's head and grepping `docs/` for the numbers it writes — the inventory above is the result, and **a merger re-reads it** |
| **Findings** | **F1–F14** are this folder's own and start at F1, the way [docs/cloud-senior-review/](../cloud-senior-review/)'s F1–F27 do; the credentials-service plan's F1–F37 and that folder's are different sets over different trees |
| **Checks** | **none taken, and the check is named rather than numbered** — the shape [docs/run-limit-carry-over/README.md](../run-limit-carry-over/README.md) chose on 2026-11-01. The register's highest **written** section on `main` is **Check 190**, and open pull requests claim above it (PR #1420 holds **230–235**, 233 withdrawn); the register's own rule is that a claim above the highest written section is settled at its merge and that whichever of two open claims merges second renumbers itself (`docs/gcp-live-checklist.md`). So the task that writes the check takes the number free at **its** merge, and [spec.md](spec.md) §8 writes the steps under a name |

## At merge, and before implementation

**At merge**, three things, and a merger who checks only the first has not finished:

1. **Re-check the D-block.** The owner answered D1062–D1074 on 2026-11-01, so the answers themselves stand;
   what a merger re-reads is the **inventory they sit in** — the open pull requests and the merged tree —
   and renumbers to the first free block if it has moved, in every file here that names a number.
2. **Take the live check's number** ([spec.md](spec.md) §8): the task that writes it takes the number free
   at **its** merge.
3. **Re-read the `:line` citations** against the tree being merged, under the convention above.

**Before implementation, two things block, and neither is this folder's to clear by writing**: **R1** of
[docs/cloud-senior-review/](../cloud-senior-review/) names the read-only permission set in
`internal/gitprov/github/apptoken.go`, and the mint this design specifies **serves that set** rather than
declaring a second spelling of it. R1 is that work stream's task; this plan depends on it and adds no task
that writes the file (see [implementation-plan.md](implementation-plan.md) §"Dependencies"). Everything
else the slice needs is in the tree: the keyless fetch path, the two-identity verifier, the mint, the audit
line and the one bundle container. The second is **U7** of that same work stream, and it is a **hard
precondition of the sign route** (**D1074**, [spec.md](spec.md) §7): an owner-run live observation that a
subprocess inside the review container cannot reach the network at all — the instance metadata server
included — because the container's own untrusted test code is what would otherwise be able to ask this
service to sign. The sign route does not start before that observation has returned.

Nothing here is approved by being written down. **Answering a decision is not approving a task.**

## What this folder is not

- **Not code.** It is a design: no Go, no Terraform, no schema, no rules change, no `.github/workflows`
  change, and no live action. Every command below is a proposal.
- **Not a lift of Global Constraint 6.** The constraint's *reason* is the model-credential path and the App
  key, and a coding caller's exposure to both stays exactly where it is: a coding run's shared project job
  is refused today and refused after this lands. The review class is admitted on **three routes** — the
  mint, the sign and the model path — and on the model path only **for its own account**: the pairing of
  [spec.md](spec.md) §3 resolves the one account the call is served by and the cascade's other tiers are
  never offered to it, which is why the review variant can hold no model token at all and a review job's
  Claude calls reach the service whether or not the published `routing.claude` switch says so (**D1063**,
  amended by the owner's ruling O1).
- **Not a second signing key holder.** The key lives in the service and nowhere else — not on the review
  variant, not on the shared project job, not in the coordinator, not in the runner (D892's ruling of
  2026-11-01, [spec.md](spec.md) §6).
- **Not a new operator step.** The key's *container* is the bundle the installation already has, so no
  Terraform and no apply changes; the key's *bytes* are written by the credentials service's own
  documented `fugaro creds` family, which is the family `github-app-key` already travels by
  ([spec.md](spec.md) §9).
- **Not the coordinator.** The verify endpoint's caller class is configured and empty until the
  coordinator (or the owner) is put in it; no coordinator task is in this plan
  ([spec.md](spec.md) §5.4, [reconciliation.md](reconciliation.md) §4 X1).
