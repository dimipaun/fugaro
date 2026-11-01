The owner, 2026-11-01: "In the console, under the Project tab, for each of those values, a way for administrators to edit them from within the console: a little pencil icon to trigger an edit."

Owner rulings given with the ask: yes to the feature; **all** edits, including widenings, from day one (not narrowing-first).

Owner rulings, 2026-11-01, on the design that came back from the ask:

1. **Every recommendation the design makes, D1102–D1121, is accepted as recommended.** Each of those
   decisions is answered, and the answer is the recommendation [reconciliation.md](reconciliation.md)
   carries with its reason — **except D1107**, which is answered in his own words in ruling 3 below
   rather than as the design recommended.
2. **A sensitive row is never editable and never shown.** It is **hidden**: no key, no value, no
   pencil, and it reaches no confirmation window and no diff.
3. **D1107: `--yes` is allowed for a widening.** "I don't see why `--yes` doesn't work, we should
   allow it." `fugaro settings set|unset` **accepts `--yes`** for a widening **when the session is
   not a coding agent's**, and **the agent-session refusal comes first: `--yes` never lifts it**. The
   console runs `settings set … --yes` after its own confirmation dialog, and there is **no
   console-only hook, no marker variable and no typed name in the console**. This **amends D846 for
   `settings set|unset` only**; `fugaro update` still asks for the typed project name and still
   refuses `--yes` alone where it would publish a looser node.

The ruling on D1107 is a decision about the CLI's behaviour, not only about this design: it is the
one answer here that a later task changes in `internal/cli`, and it is written into
[reconciliation.md](reconciliation.md)'s D1107 entry and into the plan's **P4**.

An answer is not an approval: the tasks in [implementation-plan.md](implementation-plan.md) start only
when the owner approves them.
