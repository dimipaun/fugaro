# The project image catalog: named images a project curates by hand

**Status: design only; nothing here is approved and no code follows from this folder.** This document
set reconciles one brief — the owner's project image catalog spec, received 2026-11-02 and kept
verbatim in [spec.md](spec.md) — against the code on `origin/main`, decides every ambiguity it opens,
and plans the build. The spec's own summary: "Job images move from repo-centric to project-centric. A
project keeps a small, hand-curated catalog of named Docker images. Repos inherit the project default
or reference a catalog image. Repo-level images stay as an optional override."

**Design release: 0.16.0** (the owner's ruling, [spec.md](spec.md) §Owner rulings) — this folder, and
nothing else. **Build release: the owner's decision, recommended 0.17.0**
([reconciliation.md](reconciliation.md) D1158), the release after this design lands.

**Answered by the owner, 2026-11-02:** the three open questions of the spec are answered, each as the
recommendation — **D1140** (catalog definitions live in the published project policy node, a new
top-level key `image_catalog`, copied by `fugaro update` and operator-owned), **D1148** (scheduled
security rebuilds: yes, and the daily image check's `base`/`max-age` triggers already are one) and
**D1149** (the `docker` tag selects the Batch/VM backend, and a Cloud Run job carrying it is refused
before it starts). The other decisions of D1140–D1159 are **open**: they are this plan's to write and
the owner's to answer. **Answering a decision is not approving a task**, and **no task here is
approved to build until the owner approves the plan** (the owner's ruling, [spec.md](spec.md)).

**Reconciled against** `origin/main` at `1fc82c43` (2026-11-02) and the open pull requests of
2026-11-02.

## What it is

One project, a handful of named Docker images, chosen by hand. An entry has a **name** (unique in the
project), free-form **tags** (`node`, `jvm`, `postgres`, `redis`, `firebase-emulator`, `docker`), a
**source** (a Dockerfile at a repo-and-path, an inline definition, or a prebuilt image), an optional
**description**, and the project marks one entry as the **default**. A repository does one of four
things: nothing (the default), `image: <name>`, `needs: [tags]` (Fugaro picks a covering entry), or a
repo-level override for the rare repository that does not fit. For a job, the needed tags are the
union over its repositories, the covering entry with the fewest tags wins, and no covering entry is a
refusal **before the job starts**.

That is the whole of it. The spec's non-goals are load-bearing: **no** automatic dependency
detection, **no** generating, composing or layering images automatically, **no** per-repository
Terraform and no heavy per-repository init. The earlier repo image spec — detect dependencies,
generate an image — is out of scope.

## How this relates to `docs/forge-authorship-images/`

That folder's **shared-images** design ([shared-images.md](../forge-authorship-images/shared-images.md),
**D617–D622**) shares one image between repositories that resolve to the **same dependency set**: a key
over the resolved dependencies, a lock, and a superset lookup. **C8a is built** — `internal/imagecheck`'s
`Key`/`KeyVersion`, `Superset` and the lock are in the tree — and **C8b and C8c are not**.

**The owner's ruling of 2026-11-02 is CATALOG FIRST.** The catalog is the default way a repository's
image is defined; dependency-set sharing stays **documented as a later option and is NOT cancelled**;
nothing is deleted — not the C8a code, not the old design — and the relationship is **marked** in
[shared-images.md](../forge-authorship-images/shared-images.md) by one note pointing here
([reconciliation.md](reconciliation.md) D1141). The two are composable rather than rivals: a catalog
entry's `source: dockerfile` is just a definition, so an entry may later be keyed by a resolved
dependency set without either design depending on the other. **Nothing in this folder touches C8a's
code**, and the two names that collide — the shared design's `image ls`/prune (C8c's, unbuilt) and the
spec's `image ls`/`add`/`build`/`rm` — are mapped in [reconciliation.md](reconciliation.md)
D1143–D1145.

## Why a folder and not one file

The documents have different roles and are read by different people at different times — the owner's
spec, the reconciliation against the code, the ordered tasks — and every design in this tree that
carries decisions *and* a plan keeps them apart in a folder of its own
([docs/c39-review-identity/](../c39-review-identity/), [docs/forge-authorship-images/](../forge-authorship-images/),
[docs/cloud-senior-review/](../cloud-senior-review/)). The **spec is kept verbatim** as its own file
because it is the owner's text, not this design's: the only change to it is the substitution of the
two example project names for **Project A** and **Project B**, noted in its status line.

## The files

| File | Role |
| --- | --- |
| [spec.md](spec.md) | **The owner's spec, verbatim**, received 2026-11-02, with one substitution: the two example project names become **Project A** and **Project B** (no customer or organisation name appears in this repository). Nothing else is changed — no typo fixed, no wording edited — and the owner's rulings sit beneath it under **Owner rulings** |
| [reconciliation.md](reconciliation.md) | Every ambiguity or conflict against the code (**F1–F16**, each Exists, Missing or Conflicts) and the decisions **D1140–D1159**, each with its options and the recommended answer first, the three answered ones marked, plus the tag-cover algorithm (§3), promote and the nightly triggers (§4) and what a merger re-checks (§5). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks **PIC1–PIC7** (at most 10 files each) with named tests, red steps, SEC flags and gates, the shared-files table, the owner-run live checks and the release slot |

**How a `:line` in this folder reads.** A citation names a file and a symbol, and the number is the
symbol's **declaration** line as it stood at `1fc82c43`; a line drifts the first time anyone edits the
file above it, so the **symbol**, not the number, is what a reader should search for, and every claim
also states the fact in words.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D1140–D1159** | this work's block. **D1140** (where the catalog definitions live — **open question 1**), **D1148** (scheduled security rebuilds — **open question 2**) and **D1149** (the `docker` tag's backend — **open question 3**) are **answered by the owner on 2026-11-02**, each as the recommendation; the rest are **open**. Taken above every number either the merged tree or an open pull request holds: the highest **written** on `main` is **D1139** ([docs/console-settings-edit/reconciliation.md](../console-settings-edit/reconciliation.md), that work's block D1100–D1139), and the highest any open pull request writes is **D1066** ([PR #1602](https://github.com/dimipaun/fugaro/pull/1602)) and **D1040** ([PR #1496](https://github.com/dimipaun/fugaro/pull/1496)). No `D114x`/`D115x` appears anywhere in `docs/` on this tree or in an open pull request's. That is the launcher's **2026-11-02 reading**, taken by grepping every open pull request's head and `docs/` for the numbers they write; **a merger re-reads it** |
| **Findings** | **F1–F16** are this folder's own and start at F1, the way [docs/forge-authorship-images/](../forge-authorship-images/)'s and [docs/c39-review-identity/](../c39-review-identity/)'s do; those folders' F1–Fn are different sets over different trees |
| **Checks** | **none taken, and the live checks are named rather than numbered.** The register's highest **written** section on `main` is **Check 190**, and open pull requests claim above it; the register's own rule is that a claim above the highest written section is settled at its merge and that whichever of two open claims merges second renumbers itself ([docs/gcp-live-checklist.md](../gcp-live-checklist.md)). So the task that writes a check takes the number free at **its** merge, and [implementation-plan.md](implementation-plan.md) writes each under a name |

## At merge, and before implementation

**At merge**, two things:

1. **Re-check the D-block.** The answers to D1140/D1148/D1149 stand; what a merger re-reads is the
   **inventory they sit in** — the open pull requests and the merged tree — and renumbers to the first
   free block if D1140 has moved, in every file here that names a number.
2. **Re-read the `:line` citations** against the tree being merged, under the convention above.

**Before implementation, one thing blocks, and it is not this folder's to clear by writing:** the
build tasks assume the **published project policy node** is where a catalog definition travels
(**D1140**), which is the mechanism **D1075**'s `verifier_accounts` establishes
([docs/c39-review-identity/](../c39-review-identity/)). If that key's publication path changes before
the build tasks start, D1140 and D1156 are re-read.

## What this folder is not

- **Not code.** It is a design: no Go, no Terraform, no schema, no rules change, no `.github/workflows`
  change, and no live action. Every command in it is a proposal.
- **Not an approval.** The owner's ruling is explicit: the build tasks are planned here but **nothing
  is built until the owner approves the plan** ([spec.md](spec.md) §Owner rulings).
- **Not a replacement for the shared-images design.** C8a's code stays, the old design stays, and
  C8b/C8c stay unscheduled — not cancelled ([reconciliation.md](reconciliation.md) D1141).
- **Not a multi-repo job.** One run, one repository is the deepest invariant in the code today
  ([reconciliation.md](reconciliation.md) F12, D1154). The tag-cover rule is stated for the union over
  a job's repositories so it is correct the day a multi-repo job exists; the single-repo case is what
  ships.
- **Not a lift of the `checkout: shared` refusal.** `checkout: shared` is refused outright today
  (`SharedCheckoutRefusal`, [reconciliation.md](reconciliation.md) F6, D1142) and this work does not
  lift it.
