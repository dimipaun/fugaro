# Credentials, secrets and security service — the documents

**Status: plan only; nothing is implemented.** No implementation plan exists yet: it is written only after the owner has answered the decisions in the reconciliation (D130–D154). Nothing here is approved, and no code, Terraform, rules or live action follows from these documents.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-17.

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, "Fugaro — Credentials, Secrets and Security Service" (Oct 17, 2026), **verbatim**, committed alone in its own first commit. No typo needed fixing, so nothing in it was changed. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief, findings **F1–F37** against `origin/main` (`aa3adc51`), each classified **Wrong / Exists / Gap / Rule / Open** with a path and symbol and its consequence; what the parked mixed-credentials design still contributes and what this spec supersedes; **decisions D130–D154** for the owner, each with a recommendation, the alternative and its cost, and an empty "Owner's answer" cell; the **UNVERIFIED** table. Where it disagrees with `spec.md`, it wins. |

## The numbers this work holds

| Block | State |
|---|---|
| **D130–D159** | allocated to this work; D130–D154 are written, unanswered; D155–D159 unused and reserved |
| **Checks 90–99** | reserved for this work's owner-run checks; 90 and 91 are proposed in the reconciliation (D151), and no check section is written by this pull request |

A reserved number is never reused and never renumbered.

## Related

- [docs/design/mixed-credentials.md](../design/mixed-credentials.md) and the held [docs/plans/2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md): the parked design this spec answers (D130).
- [docs/design/project-centric.md](../design/project-centric.md) §7 (phase 5, the OAuth pool; D131).
- [docs/design/m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §11 (D2, the external gateway this service is) and [docs/design/budget-token-channel.md](../design/budget-token-channel.md) (the seal, D134).
- [SECURITY.md](../../SECURITY.md) (the claims this service would change, D148).
