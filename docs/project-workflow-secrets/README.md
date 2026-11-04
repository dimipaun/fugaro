# The project workflow secret: a workflow's own secret stored once for the project, behind an operator-local allow-list

**Status: a design, and nothing is built and nothing is approved.** This folder answers **issue
#1678**, which the owner opened on 2026-11-04 and which is reproduced **verbatim** in
[spec.md](spec.md). No decision below is answered: [reconciliation.md](reconciliation.md) §3 asks
**eighteen questions (D1260–D1277)**, each with a recommendation and the cost of every alternative,
and the build [implementation-plan.md](implementation-plan.md) prices is written to the
recommendations and may not start until the owner rules.

**This is a design pull request; it writes four documents and one test.** No code, no Terraform, no
schema, no `go.mod`.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The issue, **byte for byte** — its headings, its checklist box and its em dashes — with a status line and nothing else |
| [reconciliation.md](reconciliation.md) | Each claim read against the code on `main` at `b41709ea`: findings **F1–F20**, what already exists and what is missing, the decisions **D1260–D1277** (§3) with the recommendation and the cost of each alternative, the number block and how it was read (§4), and the residual this design refuses to hide (§5). **Where it disagrees with this file, it wins** |
| [implementation-plan.md](implementation-plan.md) | Eight tasks **W0–W7**, each with its files (at most twelve), its interface, its **red step**, its named tests and its **SEC** flag; the shared-files table (§4); the order and the critical path (§2); the recommended slot (D1277 of the reconciliation) |
| `README.md` | This file: the status, the reading of the issue, the number block, and what a reader should not take from here |

## What is being asked

A repository's workflow secret — the names a `fugaro.yaml` lists under `workflows.<w>.secrets`, which
its build and tests read — can only ever be stored **per repository**. `fugaro secrets set`'s help
says so, and the code says it twice more: `internal/cli/secrets.go:759`'s `projectWideAccepted` ends
in a branch whose whole content is `false` for such a name, and
`internal/config/project_secrets.go:168`'s key rule is that a name in the operator's `project_secrets`
block must be one of the four the platform itself mounts. Everything else about the project-wide
mechanism is already name-agnostic — the container, its labels, the per-repository grant, the mount,
the live resolution that prefers the repository's own copy, the listing and the doctor checks — which
is why [reconciliation.md](reconciliation.md) F2, F3 and F7 are the findings this design rests on.

The issue's case is a project of about **65 Maven services** that all resolve dependencies from one
private Nexus through one read-only account: 130 hidden prompts, and 130 copies to rotate.

So the design's question is not "how do we build a project secret" — that is built — but **where the
allow-list lives, what a name in it may be, and what must stay refused**. It takes the issue at its
word on the two properties that are not negotiable: the list is **operator-local**, and a
**repository's own copy still wins**, as it does for the reserved names.

**The four things a reader of this folder should hold onto:**

1. **The gate is the operator's own entry, and nothing else.** No sweep, no default, no
   "declared by more than one repository" heuristic (D1262). A repository the entry does not name
   mounts nothing.
2. **The block is new, and the reserved names' block is not weakened** (D1260, D1261). That is the
   design's most arguable recommendation and the one whose alternatives are costed in full: relaxing
   `project_secrets` is the smaller diff and it costs the typo check for every name in it.
3. **A shared project job may not mount one** (D1266), because a profile is shared by every
   repository admitted to it and a per-repository list is not one it can honour — the refusal
   `internal/projectpolicy/profiles.go:322` already gives a Claude OAuth token.
4. **No Terraform, no new grant shape, no new reader, no published document.** The whole build is a
   config block, one predicate, one store path, one refusal and five sentences. If a task in the plan
   finds itself editing a `.tf` file, the design is wrong and the task stops.

## The number block

The block is taken **above every `D<number>` on `main` and in every open work stream**, read on
2026-11-04 by grepping the trees rather than inherited from a neighbour's line.

- **On `main`**, at `b41709ea`, the highest written decision is **D1189**.
- **In the open work streams**: **#1676** writes **D1190–D1209** and names **D1219** as its block's
  top; the stateless-orchestrator work (**#1682**) writes no decision yet and is to take
  **D1220–D1259**; **#1683** and **#1672** write nothing above D1189.
- **The highest number claimed anywhere is `D1259`**, and the next free block above it is
  **D1260–D1289**, the thirty-wide width `docs/ee-reorg` took for D520–D549.
- **This work takes D1260–D1289: D1260–D1277 are written here and D1278–D1289 are reserved and
  unused.** A later task re-checks §4's facts rather than its conclusion; that section carries the
  four commands.

## What this design does not do

- **It does not build anything, and it does not approve anything.** Five of the eight tasks carry a
  **SEC** flag; the build is security-sensitive and its recommended slot is **0.19.0** (D1277),
  deliberately not the 0.18.0 release that carries the managed-agent-converge theme.
- **It does not weaken the reserved names' block.** `project_secrets`' key rule, D2's sweep,
  `PerRepositorySecret`, the provider convention, the two harness OAuth namespaces and the
  `creds-bundle` refusal are read by this design and amended by none of it.
- **It does not publish the allow-list.** Nothing reaches the project policy, the shared config or
  any document a run token reads (D1268). The list is the operator's own, on their own machine.
- **It does not touch the credentials service.** The bundle is a closed set of kinds and a workflow
  secret is not one of them; S3's vault fetch is unscheduled and gets a handoff sentence, not a task
  (D1267, [reconciliation.md](reconciliation.md) F10).
- **It does not move a value.** `fugaro secrets set`'s stdin-only contract, its hidden prompt, its
  bounds and its refusals are untouched; only *which container* a name resolves to changes.
- **It does not make a shared value narrow.** The allow-list decides which jobs mount a value, never
  what a run does with it once mounted — `SECURITY.md` says as much, and §5 of
  [reconciliation.md](reconciliation.md) repeats it rather than softening it.
- **It is not a release plan.** No release notes, no `### For operators` step (there is none: the
  build adds no operator command and no migration) and no live check. Every test the plan names is
  hermetic.
