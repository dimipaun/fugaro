# A repository build context for catalog `dockerfile` entries — the Dockerfiles teams already have

**Status: plan only; nothing is implemented on `main`; the decisions D1390–D1409 are all answered
2026-11-07 (each the recommendation) and all five tasks CB1–CB5 are approved 2026-11-07.** This is a
plan for the owner to read.

**2026-11-07: D1390-D1409 are all answered, each the recommendation** (asked one at a time; reconciliation section 7 carries the dated answer in each row). D1408 is answered (fold into `docs/project-image-catalog/context-design.md`), so the first gate is lifted, and **all five tasks are approved**: the go this folder left to the owner, task by task, was given in that one answer. D1409's answer puts the work **after the removal and after D1208's migrated entries — 0.22.0**, the slot `docs/failure-classes` shares with it.

**2026-11-08: the owner amended D1409** — **this work does not wait for `docs/repo-image-removal`'s chain**, so the ordering that answer named is superseded. The amendment changes no task — CB1 and CB5's gate half were unaffected either way, and CB2–CB4's order against R0–R2 becomes a release-planning question rather than a code one — and it names no release: which one carries the work stays the owner's and is claimed nowhere here (reconciliation section 7 records the amendment in its own row, beside the answer it amends).

It adds no code, no schema, no config key and no Terraform of its own; the documents here edit no file
outside `docs/`, and the work they plan is `internal/config`'s, `internal/infra`'s and
`internal/settings`' — named here, done by the tasks. **Open items** below is what the owner answered,
and each answer is the reason a task reads the way it does.

**Asked by:** issue **#1738** ("catalog: a repository build context for dockerfile entries"), opened
by **the issue's reporter** — a Fugaro operator running a project image catalog on their own
installation, the role the issue's own "The problem" section describes — and **still open**. No
`@handle` and no name is recorded anywhere in this folder, by this repository's rule. The issue is
reproduced verbatim in [spec.md](spec.md).

**What it is.** A project image catalog entry whose source is a repository Dockerfile
(`source: { kind: dockerfile, repo, path, branch }`) is built today with a context holding **one
file**: `internal/backend/gcp/build.go`'s `CatalogDockerfileStep` copies the Dockerfile into the
project's bucket, and the build runs `docker build … /workspace/context` on a directory holding
nothing else — `internal/backend/gcp/catalog.go`'s `catalogBuildSteps` says so in words ("a catalog
build never clones a repository into the build context"). So every `COPY` of a repository file fails,
and the Dockerfile shape teams actually have — the one a CI step runs as `docker build -f
ci/Dockerfile-java .` — cannot be a catalog entry at all. The issue asks for an optional
`context:` on a `dockerfile` source: the repository the Dockerfile is in, or a directory of it,
checked out at the exact commit the Dockerfile was read at, as the build context. An entry without
`context:` keeps today's build.

**What this folder found, in one paragraph.** Twelve of the issue's fifteen claims are accurate about
today's code and the ask is well founded. Three are wrong or partly wrong, and the two that matter are the same error
seen twice: the issue believes the per-repository build clones the repository **outside** the Docker
build and that a context checkout therefore "reuses that path rather than adding a new one", and it
does not — `images/derived/cloudbuild.yaml` passes `--secret id=git-credentials` **into** `docker
build`, and `images/derived/Dockerfile.tmpl` is where the clone happens
([reconciliation.md](reconciliation.md) **F1**, **F3**). The third is that "the exact commit the
Dockerfile was read at" names something the tree never produces: the copy reads at a branch
(`internal/catalog/fetch.go`'s `fetchDockerfile`) and the record's commit is the operator's own
`rev-parse HEAD` (**F2**). None of that weakens the ask; it changes what a design has to build and how
its security section reads.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | Issue **#1738**, verbatim: the problem, the Dockerfile example, the four things the ask would keep, and the checklist line. Where this file and the others disagree about what the issue *says*, this file wins |
| [reconciliation.md](reconciliation.md) | Every claim of the issue against the code on `main`, read by path and symbol: findings **F1–F33** (Wrong / Exists / Gap / Rule / Open), how a catalog `dockerfile` entry is built today end to end (§2), what a context would mean for `COPY` (§3), **the security analysis as an attacker would see it** (§4: repository-authored context text, the build account's grants, the git credential, secrets in the context, size, `.dockerignore`, the nightly check's credential-less shape, and how `docs/repo-image-removal`'s D1208 points the other way), an **UNVERIFIED** section (§6), the decisions **D1390–D1409** each with a recommendation, an alternative and the owner's dated answer (§7, where D1409 carries a second row for the 2026-11-08 amendment), the number block and how it was read (§8), and the owner's **Open items**, every one of them answered (§9). **Where it disagrees with [spec.md](spec.md) about the code, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **CB1–CB5**, each with its files (at most six, and CB1 is exactly at the cap), its interfaces, its named tests and command, its **red step**, its **mutation**, its **reviewer tier**, its dependencies and its commit; the levels, the critical path and the shared-files table proving no level has two writers of one file (§1–§2), what is deliberately not in the plan (§4), the release slot left to the owner with the `### For operators` text quoted (§5), the owner-run live check **Check 292** (§6) and the gates a task may not start without (§7) |
| `README.md` | This file: the status, the shape in one paragraph, the numbers, and what a reader should not take from here |

## The one thing to read first

**`docs/project-image-catalog/context-design.md` already designs this feature.** It is merged on
`main`, in the folder whose spec this issue's feature belongs to, and it is titled — in those words —
"A repository build context for catalog `dockerfile` entries". It carries its own gaps **G1–G4**,
decisions **A1–A8**, open items **O1–O6** and build tasks **C1–C5**, it takes no `D` number and no
`Check` number, and it is unbuilt. Its **G2** is this folder's **F4**, its **G3** is this folder's **F2**, and
**D1408** — answered 2026-11-07: fold into it — makes its series this folder's: **CB1–CB5** are the
tasks written in it, with **CB5** carrying the fold-in.

**This folder does not silently become a second plan for one feature.** The owner's answer to
**D1408** — fold into that amendment, supersede it, or stand beside it — is the gate on every task
here, and it is the first item under **Open items**. What this folder adds that the amendment does not
is a reconciliation written against the tree as it is on 2026-11-06, after `docs/repo-image-removal`
ruled on D1208, and the finding that the issue's own premise about where the credential is used does
not match the code (**F1**). The amendment states that premise correctly and moves the fetch out of
the Docker build on purpose (`docs/project-image-catalog/context-design.md` §A3, §C3); what this
folder adds is the reading that the **issue** does not.

## What this folder is not

- **It is not an approval by itself.** D1390–D1409 are questions with a recommended answer each, and
  the recommendation is not the answer: every one of them was answered by the owner on 2026-11-07,
  each as recommended, and the five tasks were approved the same day. What this folder writes is the
  reading those answers were given against.
- **It is not a second answer to `docs/repo-image-removal`'s question.** That design's **D1208**
  (owner ruling 2026-11-04) answered how a migrated entry bakes the repository's own dependency cache:
  "the entry's own Dockerfile does the clone … and the engine gains NO bake mode", on the stated
  ground that an engine-side mechanism keyed by repository "re-creates the per-repository build input
  inside the catalog, which is the thing the ruling removes". A `context:` key is an engine-side
  mechanism that re-creates part of that input, and the difference — a checkout the Dockerfile decides
  what to do with, versus cache state the engine maintains — is stated in
  [reconciliation.md](reconciliation.md) §4.7 and left for the owner to weigh (**F30**, **D1409**).
- **It claims no number of another work.** The decision block is read above every allocation on `main`
  and in the open pull requests (§8) and the live check is the next free number; the register's own
  file is not this task's to write (implementation-plan §6).
- **It is not a substitute for the security review.** CB3 touches what a Cloud Build executes and is
  written as a **SEC** task — opus, with the independent security review mandatory. That review is
  owed before the task lands, not after.

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1390–D1409** | this work's block, read on 2026-11-06 over `origin/main` and **every** open pull request's head (1783 refs). The highest decision written anywhere is **D1389** (`docs/image-env`, on `main`), and a `git grep` for the decision numbers in the `D139x` and `D140x` ranges over all of them returns **no path at all**, with positive controls run so that "nothing" is not a broken command. **D1390–D1409 are all written** in [reconciliation.md](reconciliation.md) §7 and **every one of them is answered** (2026-11-07, each the recommendation; D1409 amended 2026-11-08); none is reserved unused. The reading, with its commands, is §8 |
| **Check 292** | this work's live check (implementation-plan §6), the first number free above the register's highest **written** `## Check` section, **290** at this work's own reading (`docs/gcp-live-checklist.md`) and **291** since `docs/image-env`'s check was written. **291 was claimed** by `docs/image-env` at that reading; **292–299 are claimed by nobody**; `docs/stateless-orchestrator` claims 300–309 and `pr/1780`'s `docs/cli-output-console-graph` claims 310–314, both above this block. Like every claim above the highest written section, 292 is **claimed, not reserved**: the register's rule is that a claim is settled at its merge, and whichever of two claims merges second renumbers itself. The register's own row and section are written by the task that implements the check, under the number free **at that merge** |
| **Findings F1–F33** | this folder's own numbering, in [reconciliation.md](reconciliation.md) §5, taking no number any other work reads. **F33 keeps the next free number rather than renumbering the findings the decisions above it cite** — it was established while slicing the plan, it is a Gap like F14–F22, and the reason is written where it sits |
| **CB1–CB5** | this folder's task series, in [implementation-plan.md](implementation-plan.md). Distinct from `docs/project-image-catalog/context-design.md`'s **C1–C5** on purpose: two series in one tree that share letters would be read as one |

## Open items

These were the owner's, and every one is answered: the items are the questions the answers were given
to, and each answer is the reason a task reads the way it does. The full text, with the reason each one
exists, is [reconciliation.md](reconciliation.md) §9; this is the index.

1. **D1408 — answered 2026-11-07: fold into `docs/project-image-catalog/context-design.md`.**
   `docs/project-image-catalog/context-design.md` already designs this feature (**F29**), so its
   G/A/O/C series is this work's series and no task here starts before that fold-in (**CB5** carries
   it).
2. **D1409 — answered 2026-11-07 (after the removal lands and D1208's migrated entries are real),
   amended 2026-11-08: this work does not wait for the removal chain.** `docs/repo-image-removal`'s
   D1208 (owner ruling 2026-11-04) refused an engine-side mechanism keyed by repository, and this
   feature re-creates part of that input inside the catalog (**F30**). The amendment says the work
   does not wait for that chain; it changes no task, because CB1 and CB5's gate half were unaffected
   either way, and it leaves CB2–CB4's order against R0–R2 a release-planning problem rather than a
   code one. Which release carries the work is still the owner's and is claimed nowhere
   ([implementation-plan.md](implementation-plan.md) §5).
3. **D1401 — the security fork, and the one that changes a task's tier.** A context-change trigger
   needs either a git credential in the check job or a hash staged beside the Dockerfile in
   `builds/catalog/`. The second is recommended, because it keeps the check job's credential-less
   shape and the `objectViewer`-only grant that stops a build account writing a definition
   ([reconciliation.md](reconciliation.md) §4.6, **F24**). **If the owner takes the first, CB4 grows a
   Terraform task and is promoted to SEC.**
4. **D1397 — the credential the build hands the Dockerfile.** `images/derived/cloudbuild.yaml` passes
   `--secret id=git-credentials` into `docker build` unconditionally, so every Dockerfile a build runs
   can read the git credential (**F1**, **F31**). The issue's claim that it cannot is wrong; whether a
   context entry keeps that mount is a decision with a security argument on both sides.
5. **D1394 / D1395 — the commit.** "The exact commit the Dockerfile was read at" does not exist today
   (**F2**, **F16**). The owner decides what replaces it, and whether the Dockerfile is read at that
   same commit.
6. **D1398 — the bound.** A context has no size limit, because there is no context; the only nearby
   ceiling is `internal/catalog/fetch.go`'s `MaxDockerfileBytes` for one file (**F19**, §4.4).
7. **D1402's second half — the template salt.** The workflow check fires on
   `internal/backend/gcp/build.go`'s `TemplateSalt`; the catalog check has no such input, so a task
   that adds a build step to `images/derived/cloudbuild.yaml` rebuilds every workflow image and **no**
   catalog entry (**F33**). Recommended: salt, in the same change.
