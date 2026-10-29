# Subscriptions

**Status: plan only; nothing is implemented; no task approved.** D430–D448 and the Terms draft's
owner questions B1–B17 were answered by the owner on 2026-10-27 (D432, D443 and D445 partly, and
D433, D434, D436, D437, D438, D440, D444, B6, B7 and B13 not the recommendation). The owner
ruled every open tension, T10–T26, O4 and the key binding, the same day (T10, T11, T21 and T25
not the recommendation), and the plan was re-sliced to follow. One consequence of the rulings,
T27 (honour-based consoles against key-gated CLI commands), was **ruled (b) on 2026-10-29**: the
paid commands warn, not refuse, until enforcement ships, and that warning is itself **deferred with
the key work** (`docs/ee-reorg/implementation-plan.md`, D523), so for now everything is available to
all. That plan **supersedes** S1.1–S1.5, S1.7 and S1.9; S1.6 is carried there as E3.2, S1.8
stands and S1.10 (Check 178) is deferred until enforcement. Several tasks wait on counsel and on
the new entity. Each task in the plan still needs the owner's own go.

Spec owner: the repository owner (the byline in [spec.md](spec.md)). Spec received 2026-10-27;
reconciled against `origin/main` at `252b5c46a` the same day, which carries the Subscription
Terms already amended to the owner's answers (#1146).

**Business material stays private (D403, D430).** The repository is public: prices, currency
amounts, discounts, fee rates, the Enterprise floor, refund and renewal figures and the entity's
tax plan are in the owner's private business repository, not here. `spec.md` is therefore an
excerpt, and the answer cells record each answer's structure with "price: private" where it
carries a figure.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's "Fugaro Subscriptions Spec", an excerpt: the commercial lines are left out (its opening note lists them) |
| [reconciliation.md](reconciliation.md) | Findings F1–F47 against the code, the licence files and the earlier rulings; decisions D430–D448 with the owner's answers; B1–B17; resolutions R1–R6; tensions T1–T26, all ruled 2026-10-27, and T27, a consequence the rulings create (ruled 2026-10-29) (a "Rulings" section; the tensions as raised are kept); open items O1–O5; UNVERIFIED U1–U12. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices S0 (Terms and licence docs), S1 (the moves into `ee/`), S2 (the per-organisation key and its verifiers), S3 (the owner-operated key service), S4 (seat estimate), S5 (roles design), S6 (lapse state), S7 (website), S8 (owner, counsel, accountant); levels, critical path, shared files, Checks 170–179. Nothing in it is approved |

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
| **Checks 170–179** | this work's block. **170–179** are named by the plan, owner-run and not run (S3.6 registers them). Taken above the go-to-market work's Checks 160–169 |

## Headline

Counsel's business licence stays (D431). After the rulings of 2026-10-27:

- **The moves.** The coordinator's command and library move to `ee/` (T10). `fugaro report` moves
  while `fugaro runs digest` stays MIT (T11). One relabelled, mixed-licence binary ships (T21). The
  released MIT versions stay the fork point.
- **The key.** One offline key per organisation, expiry only, renewed 30 days early (O4, T26).
- **The key service.** It is owner-operated in a dedicated GCP project and built after the entity
  exists; until then, keys are minted by hand (T13). **Pointer, 2026-10-29:** the owner ruled that
  the key-minting service belongs under `ee/` (for example `ee/keysvc/`), its deploy owner-run and
  deferred with the key work; see `docs/ee-reorg/implementation-plan.md` (E4.1).
- **Lapse.** After the 30-day grace the console and the app keep reading and cancelling (T12).
- **The first licensed releases.** Console 0.113.0 and the first mobile release ship honour-based
  once counsel signs off (T17).
