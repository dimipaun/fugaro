# Always a budget

Owner: Dimi Paun. Date: 2026-11-20. Written by the coordinator from the owner's rulings; the
owner's words are quoted where they are his.

## The ruling

While planning [every model key through the credentials service](../keys-through-service/README.md)
the owner was asked what to do with a run in budget mode off, the one kind of run that must keep a
model key in the agent's own environment. His answer (D3009 there):

> I don't think we should have this no-budget case.

> Yes let's remove it. We should always have a budget. If anyone doesn't care, they can set a high
> number.

Two answers followed the same day: `observe` stays as a mode (D3010), and the removal is a spec of
its own, this one (D3011).

## What is asked

1. **Two modes.** The budget mode is `observe` or `enforce`. `off` is not a value any config,
   policy, flag or record accepts.
2. **Unset means `observe`**, everywhere, including in the run.
3. **Every run has a gateway and an identity.** No run puts a model key in the agent's own
   environment, and no code path exists for a run without the gateway.
4. **Every installation has the budget database.** An installation that has none gets one.
5. **`fugaro update` is the whole upgrade.** It creates the database where there is none, rewrites
   `off` to `observe` wherever it is written, and says what it did. No other command, deletion or
   manual step.
6. **Safe with runs in flight.** A run that started with the budget off finishes.
7. **Nobody starts being refused by a cap they did not choose.** The migration lands on `observe`,
   which refuses nothing.

## What the brief knows it does not know

- What a run does with a model that has no price, which mode off let through unchecked.
- What the Cloud Batch backend does, which refuses a run that has an identity.
- What happens to a repository's own `fugaro.yaml` that says `off`, a file in the user's checkout.
- Whether creating the database needs anything only a person can do.
- Which formats list `off` as a value and need a version bump.

## Out of scope

- Removing `observe`, or choosing a default cap (D3010).
- Moving model keys off job mounts: that is the keys plan, whose tasks KS14 and KS15 wait on this
  work.
- Local runs (`fugaro exec`, `fugaro troubleshoot`), unless they read the mode.
