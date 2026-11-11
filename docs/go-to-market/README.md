# Go-to-market (engineering sections)

**Status as of 2026-11-10: in progress, 4/12 merged; see [docs/status.md](../status.md) for the current table.** D400-D408 were answered by the owner on
2026-10-26 (D400, D405 and D406 are not the recommendation), and D409-D412 on 2026-10-30 — D409
not the recommendation either, D410 the design pass the owner asked for, D411 the owner's own
instruction that the hero's picture is a real screen of the console and D412 the owner's three
findings read on the same preview after both: a mark on each closing action, the page's two fields
of colour cut at the site's largest radius, and the console's every screen shown on all four pages
in the homepage's own slider. Each task in the plan still
needs the owner's own go.

Spec owner: the repository owner. Spec received 2026-10-26; reconciled against `origin/main` at
`e9dacf54b` the same day.

The owner's full go-to-market agenda holds business, marketing and sales material that is not kept in
this repository: the owner is creating a private repository, `fugaro-business`, for it (D403).
`spec.md` is therefore an excerpt: only the sections that ask for work here, copied verbatim, with
nothing edited and the named design partners left out.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The engineering sections of the agenda, verbatim |
| [reconciliation.md](reconciliation.md) | Findings F1-F22 against the code and the earlier rulings, decisions D400-D412 with the owner's answers, resolutions R1-R5, tensions T1-T2 (both closed), items O1-O2, UNVERIFIED U1, U3-U5 (U3 and U4 settled). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices G1 (`fugaro estimate`), G2 (four landing pages), G3 (SECURITY check), G5 (licensing note); levels, critical path, shared-files table. Nothing in it is approved |
| [security-check.md](security-check.md) | G3.1's verification note: every secrets-flow claim `SECURITY.md` and the site's security page make, checked against the code at `c964639f6` with `file:line` evidence, a claim/holds/gap table, and the four gaps with wording for the owner to approve. `SECURITY.md` is untouched by it |

## The numbers this work holds

| Numbers | State |
| --- | --- |
| **D400-D429** | this work's block. **D400-D408** written and answered 2026-10-26 and **D409-D412** on 2026-10-30; **D413-D429** reserved and unused. Taken above the highest decision written (D367) and reserved (D389, by the Bitbucket credentials plan) on `main` and in open PRs |
| **Checks 160-169** | this work's block. **160-162** are named by the plan and owner-run; **163-169** are reserved and unused. The highest check reserved on `main` and in open PRs was 154 |

## Headline

Most of the agenda's engineering exists: the site and its consent gate, the licensing skeleton, the
SECURITY text and the price table. What is missing is `fugaro estimate` (and the per-model token counts it
needs on the run record) and the four campaign pages with the three third-party loads the owner allowed
behind consent. SSO and audit logs are gates tracked here and built from a separate plan.
