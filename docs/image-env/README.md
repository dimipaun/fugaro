# An image's `ENV` reaches a run, for operator-owned catalog images — and the runs say what is dropped

**Status: the design is approved and IE1 is implemented (one branch, not merged); IE2, IE3 and IE4
are not.** The design adds no config key, no wire field, no command and no Terraform; the one schema
edit it does make is IE2's and it comes with the Go change, never alone (`BATCH_`, O4's answer, joins
the refused names in `schemas/fugaro.schema.json`'s `env` pattern as well as in
`config.ReservedEnvPrefixes`, and a test holds the two together). IE1 adds code, and it is the only
implemented task. The owner ruled on **2026-11-05** on the two questions this design could not open
with — the scope (below) and the release (**0.20.0**, a number `main` has since tagged and this work
did not ship in: the slot is the owner's to re-take, and no task waits on it) — and **on 2026-11-06**
on the three that were still open: **O3** (names only, no redaction of what the passthrough carries),
**O4** (`BATCH_` joins `config.ReservedEnvPrefixes`) and **O5** (the report is `fugaro doctor --all`),
each the answer its decision recommended, recorded in [reconciliation.md](reconciliation.md) §8 and in
[implementation-plan.md](implementation-plan.md)'s head. The owner's approval of the plan is what let
IE1 start.

**Asked by:** issue **#1755** ("runs: an image's ENV variables never reach a run (allowlist); pass
them through the secrets denylist, or add a plain env map", opened by the issue's reporter, 2026-11-05,
labelled `enhancement`, still open), reproduced verbatim in [spec.md](spec.md). The issue is a
failed run: a project catalog image is operator-owned, it is where a team configures its toolchain,
and its `ENV NEXUS_BASEURL=…` was silently empty in the run, so Maven resolved
`${env.NEXUS_BASEURL}/repository/maven-public/` to nothing and `fugaro verify` failed before it
reached the repository. The agent's environment is an **allow-list** (`internal/agent/env.go`'s
`baseEnv`) and every other name the image sets is dropped without a word.

**What it is:** for a job whose image the **operator's catalog** chose, the run passes the image's
own environment variables to the agent, **minus the denylist Fugaro already has** — the names a
workflow secret's `env` may not take (`config.ReservedEnv`), which is the same list
`schemas/fugaro.schema.json:473` mirrors — with every variable **Fugaro writes applied last**, so
nothing an image sets can override the routing, the auth or a `FUGARO_*` name. Nothing the image
sets is printed: the runs report **names, never values**. Per-repository images and a plain `env:`
map on a workflow stay **out** (§"Considered and declined"). The load-bearing fact the reconciliation
had to settle first is that **nothing in the job tells the runner where its image came from**
(reconciliation F5) — so the operator's catalog entry is stamped into the job's own environment at
deploy, where the entry is already known (F6), and a run whose job carries no stamp (empty or absent) **passes
nothing** (D1371).

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | Issue **#1755**, verbatim: the problem, the three asks in the issue's own order of preference, and the checklist line. Where this file and the others disagree about what the issue *says*, this file wins |
| [reconciliation.md](reconciliation.md) | Every claim of the issue and of this design against the code on `main` and against the open pull requests: findings **F1–F18**, the **attacker probe** (§4: can a repository steer which names pass; `PATH`/`HOME`; proxies and CA bundles; `JAVA_TOOL_OPTIONS`/`MAVEN_OPTS`-style hooks; a variable that looks like a secret; the gateway's credential; the logging), the decisions **D1370–D1383** with a recommendation each (§5), what is considered and declined (§6), the number block and how it was read (§7), and the **Open items** that are the owner's (§8). **Where it disagrees with [spec.md](spec.md) about the code, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **IE1–IE4**, each with its files (IE2's is the longest at eight, and one of them is the schema mirror of the list `BATCH_` joins), its interfaces, its named tests, its **red step**, its **mutation**, its **reviewer tier** and its commit; the order and the shared-files table (§2), what is deliberately not in the plan (§4), the **release slot 0.20.0** (§5), the owner-run live check **Check 291** (§6) and the gates a task may not start without (§7) |
| `README.md` | This file: the status, the ruling, the shape in one paragraph, the numbers, and what a reader should not take from here |

## The owner's ruling (2026-11-05, `AskUserQuestion`, both answers the recommendation)

> **1. Scope: option 3 ("say so") now, PLUS option 1 for OPERATOR-OWNED CATALOG IMAGES ONLY.** The
> catalog image's `ENV` variables are passed to the agent's environment minus the denylist the
> workflow-secret `env` field already uses, with every variable **Fugaro sets itself** (routing,
> auth, `FUGARO_*`) applied **last**, so nothing an image sets can override it. Per-repository
> images and a plain `env:` map (option 2) stay **out**, said so in a "considered and declined, and
> why" note. **2. Release: 0.20.0** — the design note and option 3 land in 0.20.0; the code change
> only if the design review is clean in time.

Three consequences follow, and they are requirements of this design rather than recommendations:

1. **The scope is the catalog, and the code has to prove it.** An image the operator's
   `image_catalog` chose passes its variables; a **per-repository** image does not, and neither does
   `checkout: clone`'s base image. Today nothing in the container distinguishes them (F5), so the
   distinction has to be **created at deploy** (D1371) — and a job with an empty or absent stamp passes nothing,
   which is the fail-closed direction.
2. **The denylist is reused, not invented.** `config.ReservedEnv` (`internal/config/validate.go:85-88`)
   is Fugaro's Go implementation of the names a workflow secret's variable may not take; it is
   case-insensitive over both prefix families and single names (F3). Option 1 is not "trust the image
   minus a new list": it is "the image's names, put through the list that already guards the other
   way a name reaches the agent".
3. **Option 3 is in the same release, whatever happens to option 1.** The reason the issue exists is
   that nothing *said so* — not that the value was missing (the workarounds in the issue are real:
   hard-code the value in a file, or store a public URL as a workflow secret once per repository).
   So one sentence in `docs/base-image.md` next to "Bring your own" and one command that lists the
   dropped **names** ship even if the passthrough slips.

## What this design does not do

- **It does not pass the runner's environment.** The image's `ENV` is what a job's environment
  holds, and so is everything else the deployment and the launcher write into it — the five platform
  names, the budget variables, `FUGARO_*`, and **every secret name of the job's workflow**
  (`FUGARO_SECRET_ENVS`, F8). A passthrough that copied the runner's environment would hand a run
  the secrets of a workflow it did not declare, which is a narrowing SECURITY.md states plainly
  (`SECURITY.md:66`). The design subtracts those (D1373) and is written as an **allow**, not a copy.
- **It does not read the image.** The design never inspects the built image's config: no registry
  call at run time, no `docker inspect` in the job. What it passes is the names the job's own
  environment already holds, which is the only half a run can see (F5) — and that is also why the
  report can only be honest about **names** (D1375).
- **It does not make a value a secret.** A variable an image declares is not registered for
  redaction by default: the recommendation (D1376) is that the **names** are reported and the values
  are not, and the trust statement that carries it is in reconciliation §4. The alternative —
  registering every passed value with the redactor — is stated with its cost, and **what it does to
  transcripts was a product decision, so it was put to the owner** (Open item **O3**): the answer of
  2026-11-06 is **names only**, the recommendation, so no value this work passes is registered.
- **It does not touch the per-repository image path**, which another design removes
  ([docs/repo-image-removal/](../repo-image-removal/README.md), a design whose slot is **0.20.0** and
  which is **not built** on `main`; the re-slot that moved it there is **#1709, which merged**): the
  scope here is a statement about today's tree, and the reconciliation says what happens to this design
  when that one lands (F17). That design's own tasks R0–R2 are still in open pull requests.
- **It claims no decision of another work.** The number block is read above every allocation on
  `main` and in the open pull requests (§7) and the live check is the next free number; the
  register's own file is not this task's to write (implementation-plan §7).

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1370–D1389** | **this work's block, moved here by the fix round of 2026-11-06, and read above every claim rather than inside one.** `main`'s highest written decision is `docs/workspaces`' **D1290–D1305** (its block is D1290–D1309), and **D1310** — the number `docs/mcp-server/README.md:111`, `docs/workspaces/reconciliation.md:665` and `docs/periodic-reviews/README.md:125` name as the next free one — is still free; but `docs/stateless-orchestrator/reconciliation.md:24` names **D1320–D1349** as a block it considered and did not take, so a block written there would sit inside another document's claim. Open PR **#1780** reads it the same way and takes **D1350–D1369**, saying of this pull request that *"that overlap is not this work's to fix; it is why this block starts above D1349."* Nothing claims **D1370** or above: a `git grep` for `D13[7-9][0-9]` and `D14[0-9]{2}` over `origin/main` and over every fetched open pull request head returns nothing. This work writes **D1370–D1383** and reserves **D1384–D1389** unused. The reading is taken in [reconciliation.md](reconciliation.md) §7 with the commands |
| **Check 291** | this work's live check (implementation-plan §6), **291**: the first number free above the register's highest written `## Check` section at the claim's merge, **290** the mixed-credentials run (`docs/gcp-live-checklist.md`'s `## Check 290`, named by content and not by line) — and the register's highest written section since this work's own `## Check 291` was written, by the task that implemented it. The checks other work claims start at **300** (`docs/stateless-orchestrator`'s 300–309 and #1780's 311–314), so 291 is **claimed, not reserved**: the register's rule is that a claim above the highest written section is settled at its merge, and whichever of two claims merges second renumbers itself. The register's own row and section are written by the task that implements the check, under the number free **at that merge** |
| **Findings F1–F18** | this folder's own numbering, in [reconciliation.md](reconciliation.md) §3, and taking no number any other work reads |
| **O1–O6** | the **Open items** of [reconciliation.md](reconciliation.md) §8: O1 and O2 are the two the owner answered on 2026-11-05 (the scope and the release), and **O3**, **O4** and **O5** are answered on 2026-11-06 — names only, no redaction of what the passthrough carries; `BATCH_` joins `config.ReservedEnvPrefixes`; the report is `fugaro doctor --all` — each the answer its decision recommended. **O6** is not a question — it is the owner-run live check, which is theirs to run. An answer takes no decision number of its own — it picks an option — which is the convention `docs/cloud-senior-review/README.md:67` records for the same situation |
