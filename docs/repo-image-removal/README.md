# Removing the per-repository derived images: the project image catalog is the only image path

**Status: a design, and nothing is built.** The owner gave the ruling this folder answers on
**2026-11-04** — *"Completely disable repo-level images for now to make things simpler."* — and this
folder reconciles it against the code on `main`, decides every ambiguity it opens, and plans the build
for **0.18.0**. This is a **design-only** pull request: it writes four documents, no code, no schema,
no Terraform, no `go.mod`. **No decision here is answered yet** — each carries a recommendation and
the cost of every alternative, and the plan's tasks start only when the owner approves them.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The design in full: what is removed, what stays, how a repository migrates and in what order so the dogfood pipeline never loses its image, the retirement of the surviving cloud objects, what each removal does to `fugaro.yaml` validation, the schemas and the goldens, the managed gate's rows, and the owner-run live check that gates the 0.18.0 tag. **Where it disagrees with [reconciliation.md](reconciliation.md), the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | Every claim of the spec against the code on `main`: findings **F1–F25**, what exists and what is missing, the decisions **D1190–D1208** (§4) with the recommendation and the cost of each alternative, the number block and how it was read (§3), and the residual this design refuses to hide (§5) |
| [implementation-plan.md](implementation-plan.md) | Eleven tasks **R0–R10**, each with its files (at most twelve **per commit** — R3's 50-fixture sweep is the one subject that needs four), its interface, its **red step**, its named tests and its **SEC** flag; the shared-files table (§4) and the order (§2), which migrates the dogfood repositories **before** anything is removed and every removal task **after** D1148's two; and the recommended slot (§7), **0.18.0**, whose tag waits for the owner's live check |
| `README.md` | This file: the status, the reading of the number block, what is being asked, and what a reader should not take from here |

## The owner's ruling (2026-11-04)

> **Owner, 2026-11-04:** *"Completely disable repo-level images for now to make things simpler."*

Read against the tree, the ruling has three parts, and they are the design's requirements rather than
its recommendations:

1. **A per-repository derived image is removed in 0.18.0.** Not deprecated, not defaulted off: the
   workflow `image:` block, `dockerfile:`, the `rebuild:` block, the commands that build, refresh and
   check a repository's own image, the per-repository check job and its Scheduler, and the
   per-repository Artifact Registry repository it all writes into, all go.
2. **The project image catalog is the only image path.** A repository's custom toolchain becomes a
   **catalog entry** whose source is one of the three the catalog already has
   (`internal/config/catalog.go:59`): a **Dockerfile in a repository**, an **inline definition**, or a
   **prebuilt image**. Nothing else decides a job's image.
3. **An owner-run live check gates the 0.18.0 tag.** It builds a catalog entry on the sandbox, launches
   a run on it, and confirms the run. The tag is not cut on a green unit-test suite alone.

No production users exist and this is pre-1.0, so the removal **reshapes directly**: no compatibility
shim, no dual path, no key that means one thing for an old installation and another for a new one.

## What is being asked

Today a workflow's job runs an image that Fugaro builds **for that repository and that workflow**:
`gcp.ImageName(c.registryPath(), slug, name) + ":latest"`
(`internal/infra/spec.go:1011`), built by Cloud Build into the repository's own Artifact Registry
repository (`deploy/terraform/gcp/modules/repo/registry.tf:10`) as the repository's own build account
(`deploy/terraform/gcp/modules/repo/build.tf:5`), from a Dockerfile Fugaro renders out of the
workflow's `image:` settings or that the repository commits and names with `dockerfile:`
(`internal/config/config.go:603`, `:604`). One Cloud Run job per repository
(`fugarochk-<slug>`, `internal/backend/gcp/names.go:280`) with one Cloud Scheduler entry
(`deploy/terraform/gcp/modules/repo/check.tf:116`) looks at every workflow once a day and rebuilds what
moved.

The catalog already exists and is already the *other* path: `catalogJobImage`
(`internal/infra/spec.go:799`) chooses an entry through the tag-cover rule and the job runs it — but
only for a workflow that declares none of the derived-image keys, because `catalog.Applies`
(`internal/catalog/select.go:318`) returns false the moment `image:` or `dockerfile:` is set. So the
tree carries **two image paths**, and the ruling says one: the catalog's.

**Two repositories carry a per-repository image today and neither may lose it:** this repository, whose
`go` workflow sets `image.setup` (`fugaro.yaml:43`), and the sandbox repository. §3 of
[spec.md](spec.md) is the order that gets both onto a catalog entry **before** the removal lands, so
the dogfood pipeline never runs a job whose image does not exist.

**The gateway enforces the cost cap on this run.** Every figure Claude Code prints about this session's
own spend is ignored: it misprices non-Anthropic models, here by roughly a factor of twenty-five, and
the gateway is the only authority. The run stops for one reason only — the gateway refuses a call.

## The number block

The decision block is taken **above every `D<number>` on `main` and in every open work stream**, read
on **2026-11-04** by fetching each **branch** and reading it, not by trusting a neighbour's table.

- **On `main`**, the highest *written* decision is **D1183** and the highest number *named* is
  **D1189**, the reserved tail of `docs/config-management/` (that work's block D1160–D1183, with
  D1184–D1189 reserved and unused).
- **In the open work streams**, re-checked in this run by fetching each branch and reading it — the
  branch is the evidence. The highest number any of them names is again **D1189**, from the same
  configuration-management folder `main` already carries; the open docs designs among them
  (`docs/workspaces` #1492, `docs/binary-allow-rule` #1491, `docs/periodic-reviews` #1490,
  `docs/mcp-server` #1487, `docs/plugin-user-scope` #1488, `docs/run-without-checkout` #1481) all sit
  below D1027, and the managed-mode and settings **code** branches (#1663, #1665, #1666–#1673) write no
  new decision. **D1190 is therefore free.**

**This work takes D1190–D1219: D1190–D1208 are written here, and D1209–D1219 are reserved and
unused.** Every decision in the block is **OPEN, with a recommendation** — none is answered, and the
owner answers them or the design is not approved. A later task that needs a number re-checks this
section's facts rather than its conclusion.

## What this design does not do

- **It does not build anything.** No task in [implementation-plan.md](implementation-plan.md) is
  approved, and no file outside `docs/` and the one Go test that pins this folder is touched.
- **It does not remove the project image catalog**, its tag-cover rule, its sources or its commands.
  The catalog is what survives, and this design changes it in one place only: what a `checkout: baked`
  workflow may point at (§2.2, D1191).
- **It does not delete a cloud object by itself.** The check job, its Scheduler and the repository's
  registry leave the Terraform configuration; the objects themselves are destroyed by the owner's own
  `--retire` run and by nothing else (§4, D1194).
- **It does not make retirement agent-runnable.** `--retire` keeps its two typed confirmations, is
  never admitted by the managed gate, and no task of this plan adds a path to it (D1194, D1206).
- **It does not lift the `checkout: shared` refusal** and does not touch C8a's code, exactly as
  [docs/project-image-catalog/](../project-image-catalog/README.md) left them.
- **It does not add a compatibility shim.** A removed key is refused and the refusal names the catalog
  command that replaces it (D1195); there is no release in which both paths work.
- **It does not rename or re-mean a wire field.** `checkout` keeps both its values and both their
  meanings, `image_name` and `needs` keep theirs, and the run record and the settings document are not
  touched (D1191, D1203).

## Related

- [docs/project-image-catalog/](../project-image-catalog/README.md) — the catalog this design makes the
  only image path: its three sources, its tag-cover rule, its commands, and the D1148 schedule whose
  two tasks the removal is sequenced after.
- [docs/managed-agent-converge/](../managed-agent-converge/README.md) — the gate whose command set this
  design owes rows to, and the D1026 rule every row inherits.
- [docs/config-management/](../config-management/README.md) — the command set that writes a
  repository's configuration, including the image keys this design removes, and the block D1190 is
  taken above.
- [docs/forge-authorship-images/](../forge-authorship-images/) — the shared-images design (C8a built,
  C8b/C8c not), whose relationship note
  ([shared-images.md](../forge-authorship-images/shared-images.md)) points at the catalog.
