# A vision-capable model for a run on a text-only coder

**Status: plan only; nothing is implemented; no task is approved.** Spec owner: Dimi Paun. Written
2026-11-10. **Independent review over these documents has not run.**

**Asked by:** the owner, 2026-11-07, in issue #1865: "we need to handle this case and provision for a
vision-capable model in case we're using a text-only model".
**Reconciled against** `origin/main` at `256106ecf1` and the 36 pull requests open on 2026-11-10.

## What it is

A run whose coder is text-only — `deepseek/deepseek-v4.1-flash` through OpenRouter, say — dies at
`implement` the moment its agent reads a picture. The agent's `Read` puts the file into the next
Messages request as a base64 image block, and the gateway refuses the whole call because the model's
price row carries no per-image token ceiling: `internal/gateway/forward.go:550`. There is no way out
of that refusal today — `model_prices`, the owner's only lever over a provider's price, sets token
rates only (`internal/pricing/overrides.go`), and the built-in provider price catalog refuses a model
that prices an image (`docs/design/provider-price-catalog.md` D8) — so a repository whose work
involves a screenshot, an icon or a golden PNG cannot be coded by a cheap model at all.

This work adds one model slot, `agent.models.vision`, resolved and checked like the three that exist
(`agent.models.coder`, `.reviewer`, `.background`), priced with its own `ImageTokens`, and a bootstrap
check that refuses early — with the fix named — when a text-only coder has no vision model to fall
back on and the task or the repository names image files.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received (verbatim): issue #1865's wanted behaviour |
| [reconciliation.md](reconciliation.md) | Each claim against the code on `main`: what exists, what is missing, what conflicts (findings F1–F18), the decisions D2000–D2016, and the UNVERIFIED items. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks V0–V8 (at most 12 files each), the levels, the critical path, the shared-files table and Checks 520–521. **Nothing in it is approved** |

## The disagreement a reader meets first

The spec says an image input is **routed** to the vision model. The gateway's own documented contract
is that it "never rewrites a body" (`internal/gateway/gateway.go:7`), and the model a call names is in
that body — so a per-request substitution is a change to the one thing the budget's whole design
rests on: the request the agent wrote is the request that is priced and sent. The reconciliation
recommends the narrower mechanism instead — the stage that can see images **runs** the vision model
(D2002) — and prices the literal reading in that row. That recommendation is not the owner's answer
yet; the plan is written for it and says which tasks change if the owner answers the other way.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D2000–D2019** | this work's block: **D2000–D2016** written, with a recommendation, the alternative's cost and an empty answer cell; **D2017–D2019** reserved and unused. Taken above every decision on `main` (the highest is **D1959**, the top of `docs/secrets-brokering/dynamic-delivery`'s reserved block D1930–D1959, used to D1946) and above every open pull request's docs on 2026-11-10: the highest used in any of them is **D1999** (the workspace-token reconciliation in #2259), whose block and #2259's own D1960–D1979 sit below this one |
| **Checks 520–529** | **520** and **521** are proposed by the plan, owner-run and **NOT RUN**; **522–529** are reserved and unused. Taken above every check on `main` (the highest is **Check 512**, inside `docs/secrets-brokering/dynamic-delivery`'s reserved 510–519) and above every open pull request's (the same 510–519; #2258's 230–249 and image-setup's 500–509 sit below) |

## Siblings

`docs/model-crafts` (crafts, credentials and pools: the work that generalises the model slots — its
MC1 is merged, `internal/config/craft.go` is in the tree) and `docs/models-commands` (the
`fugaro models` command family, done) both write the same keys this work adds a fourth of. Neither
is superseded here: this folder adds one slot to what they built and re-decides nothing in them.
