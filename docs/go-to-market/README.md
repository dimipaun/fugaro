# Go-to-market (engineering sections)

**Status: plan only; nothing is implemented; no task approved.** Every decision (D400-D405) was answered
by the owner on 2026-10-26; D400 and D405 are not the recommendation. Each task in the plan still needs
the owner's own go.

Spec owner: the repository owner. Spec received 2026-10-26; reconciled against `origin/main` at
`e9dacf54b` the same day.

The owner's full go-to-market agenda holds business, marketing and sales material that is not kept in
this repository: the owner is creating a private repository, `fugaro-business`, for it (D403).
`spec.md` is therefore an excerpt: only the sections that ask for work here, copied verbatim, with
nothing edited and the named design partners left out.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The engineering sections of the agenda, verbatim |
| [reconciliation.md](reconciliation.md) | Findings F1-F16 against the code and the earlier rulings, decisions D400-D405 with the owner's answers, resolutions R1-R5, tensions T1-T2, item O1, UNVERIFIED U1-U5. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices G1 (`fugaro estimate`), G2 (four landing pages), G3 (SECURITY check), G5 (licensing note); levels, critical path, shared-files table. Nothing in it is approved |

## The numbers this work holds

| Numbers | State |
| --- | --- |
| **D400-D429** | this work's block. **D400-D405** written and answered 2026-10-26; **D406-D429** reserved and unused. Taken above the highest decision written under `docs/` on `main` and in open PRs (D368) |
| **Checks 160-169** | this work's block. **160-162** are named by the plan and owner-run; **163-169** are reserved and unused. The highest check on `main` and in open PRs was 150 |

## Headline

Most of the agenda's engineering exists: the site and its consent gate, the licensing skeleton, the
SECURITY text and the price table. What is missing is `fugaro estimate` (and the per-model token counts it
needs on the run record) and the four campaign pages with the three third-party loads the owner allowed
behind consent. SSO and audit logs are gates tracked here and built from a separate plan.
