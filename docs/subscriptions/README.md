# Subscriptions

**Status: plan only; nothing is implemented; no task approved.** D430–D448 and the Terms draft's
owner questions B1–B17 were answered by the owner on 2026-10-27 (D432, D443 and D445 partly, and
D433, D434, D436, D437, D438, D440, D444, B6, B7 and B13 not the recommendation). Open tensions
T10–T23 wait on the owner's rulings, and several tasks wait on counsel and on the new entity. Each
task in the plan still needs the owner's own go.

Spec owner: the repository owner (the byline in [spec.md](spec.md)). Spec received 2026-10-27;
reconciled against `origin/main` at `d9b675a31` the same day.

**Business material stays private (D403, D430).** The repository is public: prices, currency
amounts, discounts, fee rates, the Enterprise floor, refund and renewal figures and the entity's
tax plan are in the owner's private business repository, not here. `spec.md` is therefore an
excerpt, and the answer cells record each answer's structure with "price: private" where it
carries a figure.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's "Fugaro Subscriptions Spec", an excerpt: the commercial lines are left out (its opening note lists them) |
| [reconciliation.md](reconciliation.md) | Findings F1–F40 against the code, the licence files and the earlier rulings; decisions D430–D448 with the owner's answers; B1–B17; resolutions R1–R6; tensions T1–T23 (T10–T23 open, T19 the Terms amendment list); open items O1–O5; UNVERIFIED U1–U12. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices S0 (Terms and licence docs), S1 (the moves into `ee/`), S2 (the key and verifiers), S3 (the signing service), S4 (seat estimate), S5 (roles design), S6 (lapse state), S7 (website), S8 (owner, counsel, accountant); levels, critical path, shared files, Checks 170–178. Nothing in it is approved |

## Deviations from the owner's text

- `spec.md` is an excerpt (D430): the lines its opening note lists are left out, unedited
  otherwise.
- The name of the owner's existing company became "[the owner's existing company]" (the one place
  the excerpt keeps it), under the rule that docs name no real people or organisations other than
  the spec's byline. The other substitutions of the first commit fell inside the lines left out.
- No typo was fixed.

## The numbers this work holds

| Numbers | State |
| --- | --- |
| **D430–D459** | this work's block. **D430–D448** written and answered 2026-10-27; **D449–D459** reserved and unused. Taken above the go-to-market work's D400–D429 and the Bitbucket credentials work's D360–D389, on `main` and in every open PR |
| **Checks 170–179** | this work's block. **170–178** are named by the plan, owner-run and not run (S3.6 registers them); **179** is reserved and unused. Taken above the go-to-market work's Checks 160–169 |

## Headline

Counsel's business licence stays (D431). The owner's answers move future versions of two released
MIT features, the coordinator and the spend reports, into the business-licensed folder (D434,
D444), which the core's launch path, the coordinator's scheduled job, the periodic reviews and the
console all touch (T10, T11); add a Fugaro-hosted key signing service with a self-serve website
(D438, D440), a new trust boundary that needs a threat model and the new entity first (T13); and
drop the perpetual fallback for a read-only console and app after a 30-day grace (D436), which
needs an express grant in the Terms (T12). Counsel's sign-off on the free seat, the seat rule and
the lapse gates console 0.113.0 and the first mobile release (D448, T17).
