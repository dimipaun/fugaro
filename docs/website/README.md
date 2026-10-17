# The Fugaro website

**Status: wave 1 started, 2026-10-17.** Owner: **@Dimi**. Date: **2026-10-16**.

This directory is the documentation of a **second artifact**: a public site for Fugaro. It is a directory rather than a pair of files under `docs/design/` and `docs/plans/` because the site is not a feature of the Go program — it is its own build, in its own language, deployed on its own schedule, which is what `console/` is (reconciliation F2). **Wave 1 of the plan has started**: `website/data/install.yaml` — the one install source, carrying the reader-facing platform list — and the Go test that pins it to `README.md`, `docs/release.md` and `.goreleaser.yaml` (`internal/cli/docs_website_install_test.go`, W0.1) are in the tree today. There is still **no site code, no hosting configuration, no domain and no web asset** of any kind in this repository (F1).

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

- **Firebase Hosting is out** as the spec first put it, not because Firebase is bad but because **the repository's Firebase project is an installation's** — *"one Firebase project serves one installation"*, stated in three documents, and in project-centric mode every installation including a customer's has one. A public site there would be deployed into somebody's installation, with a credential against it (F13). **D91's answer keeps that rule and settles the finding around it**: the site is hosted on Firebase Hosting, in a **Firebase project created for the site alone** — never an installation's — with its own credential held as a GitHub Actions repository secret (D91, F14).
- **The caps-and-costs guide's numbers are data about the owner's own installations**, one of which holds repositories no agent may touch. They come from an owner-run, read-only aggregation over one named installation that emits percentiles and **no repository name, run ID, branch, task text or person**, from a sample of at least N runs, with a "not a promise" line (D96–D97, W0.3).
- **Everything the site would say that the product cannot back today is held back**: no console download link (the console is designed and unshipped), no "~2 h" sentence (nothing measures task duration — the tree records deadlines, not durations), no Windows support claim (no Windows build exists), and no competitor comparison without a source and a date.

## What the owner has answered

Twenty rows, **D90–D109**, in the reconciliation's §4, each with this document's recommendation, the cost of the alternative, and the **Owner's answer** — and **the owner answered all twenty on 2026-10-17**: six of the seven OWNER-ONLY rows in his own words — **D90** where the site lives, **D91** hosting, **D93** analytics, **D96** the aggregation's data policy, **D102** the tagline, **D104** the palette — and **D92** (domain and redirects) with the other thirteen as recommended. **In the same session the owner gave the go for wave 1**, which is the plan's §7.1 rule satisfied for that wave: **a wave starts as soon as the owner has answered the decisions that wave depends on and given a go for it; the other waves wait on their own; nothing else blocks.** Wave 1 is W0.1–W0.6 — **W0.1 has landed** and W0.3's row (**D96**) is answered, so it may start — Part 2 still needs the register's 69 beside D91 and D92, and Part 3 waits on the owner's own events (the console's first release and the video; D93 is answered). §15 is §7.1's rule as the gate that turned the owner's *"start as soon as we can"* into a go, one wave at a time.

The plan takes **check 69 and up** (written as 69a–69c) for the site's live checks; the highest written section in [docs/gcp-live-checklist.md](../gcp-live-checklist.md) on `main` is 58, and 59–68 are reserved by the console, budget-token-channel, project-centric, console-v2, voice and managed-projects lines. **A reserved number is never reused or renumbered**; if 69 is taken before the plan's Part 2 lands, the owner reallocates and the plan follows.

## Where the work happens

Wave 1 has started (W0.1 landed, 2026-10-17); each part below is the plan's own grouping, and each task's status is in [implementation-plan.md](implementation-plan.md).

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
