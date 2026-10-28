# Licensing (open core)

**Status: plan only; nothing is implemented; no task approved.** Every decision was answered by the
owner on 2026-10-24, and the same day the owner ruled on T1 (`ee/` ships reserved, no licence
file) and O2 (`LICENSE` stays pure MIT; the scope statement is a new `LICENSING.md`; this amends
D301, not the recommendation), and amended D303 (not the recommendation): one released binary,
key-gated, with ee code compiled in by an `ee` build tag the release sets; none at 0.11.0. Each task in the plan still needs the owner's own go.

**Amended 2026-10-26 by the owner's ruling D311** ([reconciliation.md](reconciliation.md#amendment-2026-10-26-d311)):
`console/` (from console 0.113.0), `mobile/` and `ee/` are under one business licence, drafted in
the structure of the enterprise terms the spec names, with no client-side MIT carve-out, and
applied **before counsel has read it**. The console is the first component licensed outside the
MIT core after the `ee/` skeleton. This reverses D302 for the console and the mobile app, and
brings plan task L4.1 forward with a draft rather than an approved text.

**Amended 2026-10-27: counsel's text replaces the draft.** `ee/LICENSE`, `console/LICENSE.md` and
`mobile/LICENSE.md` now carry counsel's revised text, one wording in all three, with no draft banner. It
still names two documents that are not published: the Subscription Terms and the contributor licence
agreement, shown as bracketed placeholders that a later change fills. Counsel's other items are
handled so: the root `LICENSE` stays pure MIT and `README.md` and `LICENSING.md` state the three
directories (the owner's ruling of 2026-10-27, keeping O2); `README.md`, `LICENSE` and `LICENSING.md`
state no client-side MIT exception (counsel's 2.1, checked: none found); SPDX notices on every file in
`console/` and `mobile/`, a `LICENSES/` directory and the copyleft and app-store audit
([dependency-audit.md](dependency-audit.md)) are one
follow-up run; the CLA text is counsel's and final, approved as-is on 2026-10-28, while its URL and the CONTRIBUTING gate wait on the owner's Gist and CLA Assistant install, and the owner's
ruling of 2026-10-27 is that they arrive together for `ee/`, `console/` and `mobile/`, not one directory
at a time. Questions
for counsel: the covered coordinator and orchestrator code lives outside the three directories and stays
MIT until it moves; the note that the licence "applies only to code added or changed after it is adopted"
sits beside a clause covering "every file" under the directories; and the Licensor is defined only
through Subscription Terms that do not exist yet, while the copyright line names the owner personally.

**Amended 2026-10-27: the Subscription Terms draft.** [subscription-terms.md](subscription-terms.md)
drafts the Fugaro Subscription Terms the licence names, for counsel to review; it is **not in
force**, and the licence files keep their placeholder URL until counsel approves the text and the
owner publishes it (plan tasks L4.3 and L4.4,
[reconciliation.md](reconciliation.md#amendment-2026-10-27-the-subscription-terms-draft)).

**Amended 2026-10-27: the Subscription Terms follow the owner's answers.** The draft now carries the
owner's answers of 2026-10-27 (kept, with the prices, in the private business repository): a new
entity as Licensor, a merchant of record for self-serve sales, one free seat per organisation and
other free plans, a seat as anyone who launches jobs (a pricing rule, the command-line tool staying
MIT), a read-only console and mobile app after a lapse with no perpetual fallback, and a signed
offline key for every user issued by a hosted service. Prices stay bracketed; it is still **not in
force** and still awaits counsel (plan task L4.5,
[reconciliation.md](reconciliation.md#amendment-2026-10-27-the-subscription-terms-follow-the-owners-answers)).

Spec owner: the repository owner. Spec received 2026-10-24; reconciled against `origin/main` at
`e924481cd` the same day.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim (no typo fixes) |
| [reconciliation.md](reconciliation.md) | Findings F1–F27 against the code and the published licence texts, resolutions R1–R8, decisions D300–D310 with the owner's answers (D301 and D303 amended), tensions T1 (ruled), T2 (accepted) and T3 (D303's mixed-licence release), items O1–O3, UNVERIFIED U1–U8. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices L0–L5 (L4.2, L4.3 and L5.1 owner-run), tasks with their tests and dependencies, levels, the critical path, the shared-files table. Nothing in it is approved |
| [subscription-terms.md](subscription-terms.md) | The Fugaro Subscription Terms, a draft for counsel (2026-10-27, amended the same day to the owner's answers): not reviewed, not in force; prices and other Order Form items in brackets, with the two open owner items and counsel's questions |

## The numbers this work holds

| Numbers | State |
| --- | --- |
| **D300–D319** | this work's block. **D300–D310** written and answered 2026-10-24 (D305 not the recommendation); **D311** the owner's ruling of 2026-10-26 (the business licence for `console/`, `mobile/` and `ee/`); **D312–D319** reserved and unused. Taken above the highest decision written under `docs/` on `main` (D271) and in open PRs (D260); **D272–D299** are left unused so that reconciliations started the same day cannot collide with this block |
| **Checks 130–134** | this work's block. **130** written by plan task L1.10 and run by the owner (L1.11): GitHub still reports MIT and `LICENSING.md` renders — the section and its register row are in `docs/gcp-live-checklist.md`, and both are **NOT RUN** until L1.11; **131–134** reserved and unused. The highest check section written on `main` was 113 when the block was taken; 119 and 123 are claimed by the console-windows and console dependency graph plans, and open PR #1022 takes 124; 130 was free either way |

## Headline

The core stays MIT, and `LICENSE` is not touched. The repository reserves `ee/` for net-new
organisation features under their own licence. What ships with CLI 0.11.0 is the skeleton only: a
reserved `ee/README.md`, a root `LICENSING.md` stating the split, a README Licensing section, the
website card, the vendored skills' licence, the Go boundary tests, the contribution terms, and a
staged workflow. `ee/LICENSE` waits on counsel (plan task L4.1), after 0.11.0 if need be.
