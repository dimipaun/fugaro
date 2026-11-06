# An image's `ENV` reaches a run, for operator-owned catalog images — and the runs say what is dropped

**Status: design only; nothing is implemented; no task approved.** This is a design for the owner to
read. It adds no code, no schema, no command and no Terraform; it edits no file outside `docs/`. The
owner ruled on **2026-11-05** on the two questions this design could not open with — the scope
(below) and the release (**0.20.0**) — and everything else in it is a **recommendation** that the
owner has not answered: an answer is not an approval, and **no task in
[implementation-plan.md](implementation-plan.md) starts before the owner approves it**.

**Asked by:** issue **#1755** ("runs: an image's ENV variables never reach a run (allowlist); pass
them through the secrets denylist, or add a plain env map", opened by **@paulkore**, 2026-11-05,
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
deploy, where the entry is already known (F6), and a run whose job carries no stamp **passes
nothing** (D1311).

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | Issue **#1755**, verbatim: the problem, the three asks in the issue's own order of preference, and the checklist line. Where this file and the others disagree about what the issue *says*, this file wins |
| [reconciliation.md](reconciliation.md) | Every claim of the issue and of this design against the code on `main` and against the open pull requests: findings **F1–F18**, the **attacker probe** (§4: can a repository steer which names pass; `PATH`/`HOME`; proxies and CA bundles; `JAVA_TOOL_OPTIONS`/`MAVEN_OPTS`-style hooks; a variable that looks like a secret; the gateway's credential; the logging), the decisions **D1310–D1323** with a recommendation each (§5), what is considered and declined (§6), the number block and how it was read (§7), and the **Open items** that are the owner's (§8). **Where it disagrees with [spec.md](spec.md) about the code, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **IE1–IE4**, each with its files (at most six, per commit), its interfaces, its named tests, its **red step**, its **mutation**, its **reviewer tier** and its commit; the order and the shared-files table (§2), what is deliberately not in the plan (§4), the **release slot 0.20.0** (§5), the owner-run live check **Check 291** (§6) and the gates a task may not start without (§7) |
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
   distinction has to be **created at deploy** (D1311) — and a job with no stamp passes nothing,
   which is the fail-closed direction.
2. **The denylist is reused, not invented.** `config.ReservedEnv` (`internal/config/validate.go:83`)
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
  (`SECURITY.md:66`). The design subtracts those (D1313) and is written as an **allow**, not a copy.
- **It does not read the image.** The design never inspects the built image's config: no registry
  call at run time, no `docker inspect` in the job. What it passes is the names the job's own
  environment already holds, which is the only half a run can see (F5) — and that is also why the
  report can only be honest about **names** (D1315).
- **It does not make a value a secret.** A variable an image declares is not registered for
  redaction by default: the recommendation (D1316) is that the **names** are reported and the values
  are not, and the trust statement that carries it is in reconciliation §4. The alternative —
  registering every passed value with the redactor — is stated with its cost, and **what it does to
  transcripts is a product decision, so it is put to the owner** (Open item **O3**).
- **It does not touch the per-repository image path**, which another design is removing in the same
  release ([docs/repo-image-removal/](../repo-image-removal/README.md), re-slotted to 0.20.0 by open
  PR **#1709**): the scope here is a statement about today's tree, and the reconciliation says what
  happens to this design when that one lands (F17).
- **It claims no decision of another work.** The number block is read above every allocation on
  `main` and in the open pull requests (§7) and the live check is the next free number; the
  register's own file is not this task's to write (implementation-plan §7).

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1310–D1329** | **this work's block, and it is the first free one.** `git grep -n 'D13[1-9][0-9]' origin/main` finds no decision, and the highest block `main` writes is `docs/workspaces`' **D1290–D1305** with **D1306–D1309** reserved and unused; `docs/mcp-server/README.md:111` and `docs/workspaces/reconciliation.md:665` both say the next task that needs a number takes **D1310**, and none of the thirteen open pull requests writes a decision there. This work writes **D1310–D1323** and reserves **D1324–D1329** unused. The reading is taken in [reconciliation.md](reconciliation.md) §7 with the commands |
| **Check 291** | this work's live check (implementation-plan §7), the first number free above the register's highest written `## Check` section, **290**. No document and no open pull request writes 291. The register's own row and section are written by the task that implements the check, under the number free **at that merge** |
| **Findings F1–F18** | this folder's own numbering, in [reconciliation.md](reconciliation.md) §3, and taking no number any other work reads |
| **O1–O6** | the **Open items** of [reconciliation.md](reconciliation.md) §8: O1 and O2 are the two the owner answered on 2026-11-05 (the scope and the release); **O3**, **O4** and **O5** are still open, and O3's answer changes what the code does; **O6** is not a question — it is the owner-run live check, which is theirs to run. An answer takes no decision number of its own — it picks an option — which is the convention `docs/cloud-senior-review/README.md:67` records for the same situation |
