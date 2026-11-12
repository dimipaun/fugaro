# Rigor modes: how much review a run gets

**Status: the modes are built and decide runs today; this is their product document — the design, its reconciliation and its plan live in [rigor-modes/](rigor-modes/README.md), and this page is not a plan.**

A run reviews its own work, and its reviewer's findings decide whether the pull
request it opened is published ready or stays a draft. The **rigor mode** is the
dial on that decision: how many review rounds a run spends, which findings it
blocks on, what a run that still has findings left when it stops does with them,
and how much of the per-run cap it may spend getting there. Five modes, one word
each: `poc`, `startup`, `pro`, `critical` and `hotfix`.

The default is `pro`, for every repository that configures nothing. It is a
default and not a floor: the narrowest layer that sets a mode wins, and a
repository that wants the behavior of the releases before the modes existed pins
itself back with one line — `rigor: { default: critical }` in its `fugaro.yaml`.

This page is the promise; [rigor-modes/](rigor-modes/README.md) is the design
record, and the two are separate on purpose. The ladder a mode sits on is the one
project-layer rule `docs/project-layer.md` §2 states, one rank longer for this
key; nothing here restates it.

## The modes

Each mode is one of the sentences the owner already wrote, read as numbers. The
four on-axis modes are ordered by how much they block; `hotfix` is off the axis,
because the run it names is the one with the least room to spend.

| Mode | What it is for | Rounds (first-line / senior) | What blocks | Share of the per-run cap |
| --- | --- | --- | --- | --- |
| `poc` | "Happy path only. Damn the torpedoes; just make it work." | 1 / 1 | Nothing but a finding the spec calls catastrophic — a `security` or `data_loss` class, or an impact of 5 — and a finding whose score cannot be read at all. | 0.55 |
| `startup` | "Good foundation. Happy path plus common error paths. Rare, expensive corner cases go to the backlog." | 2 / 2 | A finding whose likelihood × impact reaches 8, plus the catastrophic override above. | 0.70 |
| `pro` | "Established-company quality. Aim for bug-free patches, but balance cost against likelihood; extremely rare, high-effort issues may stay unfixed." | 3 / 3 | A finding whose likelihood × impact reaches 4, plus the override — except the case the sentence names: a likelihood of 1 with a fix cost of 4 or more is tolerated. | 0.85 |
| `critical` | "Maximum care about everything." | 3 / 5 | Every finding. It is the strictest mode and the one no repository may fall below for security-sensitive code. | 1.00 |
| `hotfix` | "Production is on fire. Time is the dominant constraint." It is reached per run and by no layer. | 1 / 1 | A finding of class `security` alone, plus an unscored one. A `data_loss` finding and an impact of 5 are tolerated. | 0.40 |

The rounds column is the mode's **default**, not a cap (§[Review
rounds](#review-rounds)), and the last column is the mode's share of the
per-run cap the installation was deployed with (§[Cost](#cost)). Both are read
out of the code rather than remembered: the tests beside this page hold this
table to the vocabulary in `internal/rigor` and to the two tables' figures, so a
mode added there without a row here fails.

## What a mode changes in a run

### Review rounds

A run's review is a loop of rounds, and each round is one of the review levels —
the first-line review, the senior review that decides readiness, and the
adversarial pass ([recipes.md](recipes.md) §2). The mode supplies the numbers the
loop starts from: the first-line rounds and the senior rounds, one pair per mode,
in the table's third column.

They are defaults and not ceilings. The number a run actually runs comes from the
precedence first — the task's own override, then the recipe's `max_rounds`, then
the repository's `agent.first_line_rounds` and `agent.review_rounds`, then the
project layer's — and the mode's row supplies the number only where none of those
four set one, up or down. A repository that asks for eight senior rounds gets
eight at `poc`; the recipe's own bounds are the only ceiling either number is
held under.

The levels themselves are a second set the mode supplies: `poc` runs the
first-line level and turns the senior one off, `startup` and `pro` run the senior
level, `critical` adds the adversarial pass after it, and `hotfix` runs `pro`'s
set. In every mode but `poc` the first-line level is the repository's own switch
(`agent.first_line_review`: `auto`, `on` or `off`). The owner's floor can require
a level the configuration turned off (§[The floor](#the-floor)).

### What blocks

Findings are the reviewer's own output: every finding carries three axes of 1–5 —
likelihood, impact and fix cost — and a class, and the mode's table reads the
axes and the class and never the free-form severity word beside them. A `security`
or `data_loss` class finding, or one whose impact is 5, blocks in every mode but
`hotfix`: that is the spec's severity override, "rare but catastrophic findings
(data loss, security) block even in Startup mode". A finding the table cannot read
at all — no score, or a value outside the three axes — blocks in every mode,
`hotfix` included: an unreadable finding is never the lenient answer.

In `startup` and `pro` a **triage** judge runs after each review round: it weighs
the round's blocking findings against what the task asked for and may defer the
ones the mode's table blocks, except the ones the override reads and the ones with
no readable score, which the judge may not move. A round whose findings are all
deferred buys no fix round and the loop ends. The judge is the repository's to
switch off (`agent.triage: false`), the policy node's to switch off, and it runs
in no other mode — `poc` and `critical` judge findings by their own table, and
`hotfix` blocks too few for the judge to matter.

**At the stop.** A run stops reviewing when its rounds are spent or when it has
spent the configured share of its time budget (`agent.review_stop_fraction`,
four fifths by default), whichever comes first, and what is still open then is the
mode's second answer:

- `poc` and `startup` accept it, and the pull request goes ready with the accepted
  findings named in its status section. This is Startup's own sentence, "merge
  as-is, follow up the rest" — a finding it would have spent a round on blocks
  nothing once the rounds are gone, and only the catastrophic override still ends
  a `startup` run a draft.
- `hotfix` accepts what its security gate tolerates, exactly as its rounds do.
- `pro` and `critical` keep the pull request a draft, on the terms they reviewed
  under. `pro`'s sentence is "aim for bug-free patches", so `pro` does not accept
  at the stop what its rounds did not.

### The pull request

A run's pull request is ready if and only if the runner's own readiness rule says
so: the final commit has a clean verified test run, and the last review either
shipped or left nothing the mode blocks on. No person is in that path, in any
mode. A pull request that stays a draft carries the reason in its body — the mode,
the round and what it left — and the same reason is in the run's record.

`hotfix` is not a second readiness rule today. What a run of it does with its
findings is the table's row and nothing else, and the gate that is meant to
authorize reaching it does not exist yet
(§[What is not settled](#what-is-not-settled-the-hotfix-gate)).

### Cost

The per-run cap a run gets is the installation's deployed cap, scaled by the
mode's own share: 0.55 at `poc`, 0.70 at `startup`, 0.85 at `pro`, 0.40 at
`hotfix`, and the whole cap at `critical`. A mode **lowers the ceiling and never
raises it**, and the scaling applies only where no other layer wrote a cap: a
`budget.per_run_usd` the owner, the team or the branch set is that layer's own
number and is taken as it is. A run at a mode's fraction always has enough left
to finish a verification stage, so a mode can never starve a run into dying on the
cap.

`fugaro budget preview --recipe NAME --runs N [--repo R] [--mode MODE]` prints
this arithmetic before you launch: the ceiling a batch of that recipe at that mode
may reach, against today's caps. It is a ceiling and not a forecast, and `--mode`
here is the rigor mode — not the accounting mode `fugaro budget set --mode` takes
(`observe` or `enforce`).

## Choosing a mode

**Per run, from the command line:**

```
fugaro run --mode critical "add a unique index on payments.idempotency_key"
```

`--mode` asks for a mode for this run, and it **may raise the mode the run
resolves from its configuration and never lower it**: a request below what the
configuration already says has no effect, and the launcher says so. `--path`
declares a path the run will touch, which is what lets `rigor.areas` rules be
matched from the first moment rather than from the run's diff. Repeat it for each
path.

**Per repository**, in its `fugaro.yaml`:

```yaml
rigor:
  default: pro
  areas:
    - name: payments
      match: "internal/payments/**"
      mode: critical
```

`rigor.default` is the mode a run of this repository starts from; `rigor.areas` is
a list of named rules, each a `doublestar` glob and a mode. A rule applies to a
run whose touched paths match it; the paths are the ones `--path` declared at
launch, the run's own diff after each review round, and the paths it changed
before the pull request is marked ready — so a rule that matches only at the end
still decides the run's readiness.

**Per project**, in the project layer that `fugaro update` publishes:
`defaults.rigor.default` and `defaults.rigor.areas`, the same two keys for every
repository in the project. `fugaro rigor set MODE` writes the project layer's
default, and `fugaro rigor set MODE --area GLOB --name NAME` writes one rule;
both write this machine's own project layer file, end with `next: fugaro update`,
and refuse `hotfix` in either form — no layer sets it, and
`fugaro run --mode hotfix` is the one door that reaches it. A mode below what the
project resolves to today is refused in a coding agent's session, with the command
to run at the owner's own terminal.

**The order** is the one rule `docs/project-layer.md` §2 states: an area rule the
run's paths match, then the repository's `rigor.default`, then the project layer's
`defaults.rigor.default`, then Fugaro's own default, which is `pro`. Unlike every
other key, a narrower layer may set a **lower** mode than a wider one — this is a
default and not a floor. Two differences worth knowing:

- Both `areas` lists are in effect: a repository's rules and the project's are
  unioned rather than the narrower list replacing the wider one, and the
  **strictest matched rule** decides. Every rule that matched is recorded,
  strictest first.
- A mode a run has reached never falls within that run. A run that rose to
  `critical` for one path stays there for the rest of its rounds; a later
  resolution that comes back weaker leaves the reached mode standing.

**A follow-up** (`fugaro run --pr N`) keeps the mode the pull request's runs
recorded, and may only raise it — with `--mode` or without. A follow-up is a
continuation of the same work, so it may not review less than the run it follows.

## The floor

The mode's precedence says what a run starts from; the **review floor** says what
it may not fall below. It is the owner's key, per mode: the review levels a run of
that mode must run whatever a lower layer asks for. It lives in this machine's
project config under `review_floor`, `fugaro project policy publish` copies it
into the policy node the runner fails closed on, and a change there reaches runs
at the next `fugaro update`.

```
fugaro rigor floor show                 # what this machine's config sets
fugaro rigor floor set critical l1,l2,l3
fugaro rigor floor unset poc
```

`set` takes a comma-separated list of `l1`, `l2` and `l3`. Adding a mode the file
does not carry, or a level a mode's floor does not carry, is the ordinary write —
it can only require more review. Dropping a level, or removing a mode's entry, is
a whole project reviewing less: it asks nothing, and a coding agent's session is
refused it outright.

**The floor is outside every layer and only tightens.** `poc`, `startup`, `pro`,
`critical` and `hotfix` are all under it: a floor of `l2` on `poc` runs the senior
review in a mode whose own level set is the first-line review alone.

## Reading a run's mode back

- **`fugaro diagnose RUN`** prints the run's `Rigor:` line — the mode, the layer
  that decided it, every area rule that matched with the moment it applied at, and
  the revision the configuration was read from — and, under the last review,
  the findings the mode **tolerated**: each with its label and its score, in the
  reviewer's own order. `--json` prints the same answer as a document.
- **`fugaro runs ls`** groups by the mode each run resolved, in its `MODE` column.
- **The run's record** carries the same three parts the line does, as the `rigor`
  block of `result.json`.
- **The pull request** carries them the runner wrote: a status section naming the
  mode, the layer that decided it and the area rule that matched, and a line for
  every finding the mode left open.
- **`fugaro rigor floor show`** prints the floor, one line per mode, and tells
  apart a file that says nothing about the key from one that has taken the
  published floor away.

## What is not settled: the hotfix gate

`hotfix` is a mode value today, and that is all it is. It resolves like the
others, its levels are `pro`'s set, and it is the one mode no layer may set — so
`fugaro run --mode hotfix` reaches it, and a hand-written `rigor.default: hotfix`
does too. What does **not** exist is the gate the spec names: an authorization
read from the base branch before a run may be a hotfix at all.

That gate is an open question for the owner and is designed and reviewed on its
own, as task R7 of `docs/rigor-command/implementation-plan.md` — whose first step
is the question rather than code. Until it is answered, this page describes what
the code does and promises nothing about who may reach `hotfix`. Treat a hotfix
run as a mode with almost no gates and no gate of its own: `hotfix` tolerates
everything but an exploitable weakness, spends one round, takes the smallest share
of the cap of the five, and reaches a ready pull request on the same readiness
rule as every other mode.
