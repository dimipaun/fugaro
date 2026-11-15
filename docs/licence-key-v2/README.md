# Licence key v2

**Status as of 2026-11-11: plan only; nothing is implemented; no task approved.**

The owner's licence key spec, reconciled against the code. It replaces the key design of
[docs/licensing/licence-key.md](../licensing/licence-key.md) (licensing L3.1, subscriptions S2.1) and
extends the built verifier, mint tool and key service in `ee/licence` and `ee/keyservice`.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim (draft v0.1, November 2026) |
| [reconciliation.md](reconciliation.md) | Findings F1–F20 against `origin/main`, decisions D2130–D2148 with the owner's answers, open tensions TN1–TN6 (TN1–TN5 answered 2026-11-11; TN6 is the owner's). It wins over the spec |
| [implementation-plan.md](implementation-plan.md) | Slices K0–K7, parallel levels, critical path, shared-files table, Checks 560–564 |

Spec owner: the repository owner. Numbers taken: **D2130–D2179** (D2130–D2148 used; D2145 is a stated default, not an answer), **Checks 560–569**
(560–564 used). No spec typo was fixed.
