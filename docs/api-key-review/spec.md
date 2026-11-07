# The spec: the api-key review path

**Status: the owner's ask, reproduced verbatim, with this line and the header below added; it states
no decision of its own.**

**Asked by:** the repository owner — the person who owns the installation and the account the
review runs on, the role the ask's own "no subscription account is needed" and "the key can be a
separate secret" describe — in a message of **2026-11-07** ("the api-key review path: let's start a
design"). No `@handle` and no name is recorded here, by this repository's rule that no real name or
handle of a person appears in `docs/`. The ask is **open**: it is a request for a design, and it
approves nothing.

It is reproduced below as this run received it. Nothing in this file is the design: the design is
[README.md](README.md), [reconciliation.md](reconciliation.md) and
[implementation-plan.md](implementation-plan.md). Where one of them and this file disagree about
what the ask *says*, this file wins, because this file is a copy of the ask and they are readings of
it.

---

The api-key review path: let's start a design.

Today the in-job senior/adversarial review can run on a Claude subscription account. That is the
mixed run — `stage_accounts`, or `stages.review.claude_account` — proven by **Check 290** on
2026-11-06, and written up in `docs/design/mixed-credentials.md` §11.

I want a review path that runs on an **Anthropic API key** instead of a subscription OAuth token, so
that:

1. **the review's dollars are real**, capped and attributed by the gateway like every other call —
   not "notional", the way the mixed run's account stages are today;
2. **no subscription account is needed**: a project with only an Anthropic API key, or only
   OpenRouter, still gets an Opus-class independent review;
3. **the account's rate limits and terms questions do not apply** — the ones
   `docs/design/accounts-add` raises about spreading one project across subscription accounts, and
   the subscription-account terms question recorded as D894;
4. **the key can be a separate secret** from the coder's provider key.

Decide how it fits the existing machinery rather than adding a parallel one.
