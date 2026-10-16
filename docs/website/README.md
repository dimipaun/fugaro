# The Fugaro website

**Status: plan only; nothing is implemented.** Owner: **@Dimi**. Date: **2026-10-16**.

This directory is the documentation of a **second artifact**: a public site for Fugaro. It is a directory rather than a pair of files under `docs/design/` and `docs/plans/` because the site is not a feature of the Go program — it is its own build, in its own language, deployed on its own schedule, which is what `console/` is (reconciliation F2). **No site code exists**: there is no `website/` directory, no hosting configuration, no domain and no web asset of any kind in this repository today (F1).

## The four documents, in reading order

| Document | What it is |
|---|---|
| [spec.md](spec.md) | **The owner's text, verbatim** — committed byte for byte and alone, before anything that answers it. It is the source document for the other three, and it is not edited when they disagree with it. |
| [reconciliation.md](reconciliation.md) | What the spec asks for, checked against `origin/main` at `76f6c59`. Sixty-three numbered findings classified **Wrong / Exists / Gap / Rule / Open**, each citing a path and a symbol; a concept-to-repo table; twenty decision rows **D90–D109**; the **four** things the reconciliation refuses to accept from the spec and the **one right** the spec gives away; and six **UNVERIFIED** items. |
| [implementation-plan.md](implementation-plan.md) | The plan that executes the reconciliation: four parts, twenty-four tasks, ten PR groups, and a **Parallel execution** section of twelve levels (L0–L11) with the critical path, plus §7.1, which states once what can start under which of the owner's answers. Every task carries the files it touches, its tests, its command, its dependencies, its commit and its **red step**; every part carries its reviewer tier. |
| this file | The index, and the status line above. |

## The short version

The spec is a good brief for a marketing site and the repository can back most of it — but **everything the site is asked to *show* already exists here, and everything it is asked to *generate* does not**. The tagline is the line under `README.md`'s `# Fugaro` title, verbatim; the install paths are signed and tested; the security model is `SECURITY.md`; the release notes are `docs/releases/vX.Y.Z.md`; the caps and the run-record cost fields are real. What does not exist is any machinery that turns those facts into pages, or any test that would notice when they drift. That gap is the whole of this work, and it is why the plan puts the **generated facts and their drift tests before any site code**.

Three corrections the reconciliation makes, in one line each:

- **Firebase Hosting is out**, not because Firebase is bad but because **the repository's Firebase project is an installation's** — *"one Firebase project serves one installation"*, stated in three documents, and in project-centric mode every installation including a customer's has one. A public site there would be deployed into somebody's installation, with a credential against it (F13).
- **The caps-and-costs guide's numbers are data about the owner's own installations**, one of which holds repositories no agent may touch. They come from an owner-run, read-only aggregation over one named installation that emits percentiles and **no repository name, run ID, branch, task text or person**, from a sample of at least N runs, with a "not a promise" line (D96–D97, W0.3).
- **Everything the site would say that the product cannot back today is held back**: no console download link (the console is designed and unshipped), no "~2 h" sentence (nothing measures task duration — the tree records deadlines, not durations), no Windows support claim (no Windows build exists), and no competitor comparison without a source and a date.

## What the owner has to answer

Twenty rows, **D90–D109**, in the reconciliation's §4, each with this document's recommendation, the cost of the alternative, and an **empty Owner's answer column**. Seven are the owner's alone — **D90** where the site lives, **D91** hosting, **D92** domain and redirects, **D93** analytics, **D96** the aggregation's data policy, **D102** the tagline, **D104** the palette — and **D90 decides every path in the plan** — the plan's §7.1 says what can start under which answer (the first wave needs D90 and nothing else; W0.3 needs only D96; Part 2 needs D91 and D92), and §15 is the gate that turns the owner's *"start as soon as we can"* into a go, one wave at a time.

The plan takes **check 69 and up** (written as 69a–69c) for the site's live checks; the highest written section in [docs/gcp-live-checklist.md](../gcp-live-checklist.md) on `main` is 58, and 59–68 are reserved by the console, budget-token-channel, project-centric, console-v2, voice and managed-projects lines. **A reserved number is never reused or renumbered**; if 69 is taken before the plan's Part 2 lands, the owner reallocates and the plan follows.

## Where the work happens, once it is approved

| Part | What it ships | Paths |
|---|---|---|
| **Part 0 — the facts** | The generated content layer and its drift tests, plus the aggregation command. No site code. Mergeable on its own. | `website/data/`, `website/content/`, `internal/clidoc/`, `internal/stats/`, `internal/cli/docs_website_*.go`, `internal/cli/stats.go` |
| **Part 1 — the site** | The Astro app: toolchain, tokens and three palettes, layout, and the ten sections the repository can back — plus the hermetic gates (build, links, anchors, origins, accessibility, byte budget). | `website/` |
| **Part 2 — hosting and the live checks** | The deploy workflow **staged** for the owner to copy (no run may touch `.github/workflows/`), the canonical URL and redirects, and check 69a–69c. | `docs/pending-workflows/website.yml`, `website/data/site.yaml`, `docs/gcp-live-checklist.md` |
| **Part 3 — after the console ships** | Real console downloads, the explainer video, and analytics if D93 says yes. **Unscheduled.** | `website/content/downloads.json`, `website/src/components/` |

## See also

- [docs/release.md](../release.md) — what a tag publishes, and how a user verifies it (the site's install text is pinned to this file, W0.1)
- [docs/design/desktop-console.md](../design/desktop-console.md) — D2, D4, D10 and D11, which the console section reads and does not reopen
- [docs/gcp-setup.md](../gcp-setup.md) — `## Adoption`, where the one-Firebase-per-installation rule is stated
- [docs/dogfooding.md](../dogfooding.md) and [docs/pending-workflows/README.md](../pending-workflows/README.md) — why the deploy workflow is staged rather than written
- [docs/plans/2026-10-16-managed-projects.md](../plans/2026-10-16-managed-projects.md) — the sibling plan this one takes its shape from, and the holder of D55–D78 and checks 67–68
- [docs/gcp-live-checklist.md](../gcp-live-checklist.md) — the check register this plan takes 69 from
