# The config-ownership writer contract

The contract every writer family and the conformance test are built against
(`docs/config-ownership/implementation-plan.md` §3, task **C1**; modules
`cm-conformance` first). It is written by C1 and changed only by CR1 or CR2 of
that plan's §4 — a task that needs it to say more raises a contract change
rather than editing it inside its own pull request. **CR1** revised it after
C1's independent review: the segment placeholders, one writer per field, the
copies in and out, and the hook's stage choice (§2, §3, §6).

**What is landed.** The registry and the hook exist; the hook is a no-op stub
(`internal/cli/after_write_stub.go`) until **N3** gives it the in-process
`policy` stage, and **I1** deletes the stub and turns the conformance ratchet
strict. No command writes through either half yet.

## 1. The two halves

| Half | File | What a caller uses |
| --- | --- | --- |
| The writer registry | `internal/settings/writer.go` | `RegisterWriter(pattern, Writer) error`, `WriterOf(key) (Writer, bool)`, `Writer{Command, Argv, Store, Node, Label, Reason}`, `KeyPlaceholder`, `ValuePlaceholder`, `Writer.ArgvFor(key)` |
| The after-write hook | `internal/cli/after_write.go` | `runAfterWrite`, of type `afterWriteFunc`; its arguments are `context.Context`, `*cobra.Command` and `afterWriteChange{Key, Store}` |

`Writer` is what writes a settings row's key, as a value rather than a token
(D2609): the one registry's `Owner` column is a word — a family token, or the
file a key nobody edits is stated in — and item 4 of the owner's direction
(2026-11-15) asks a test that the owner really writes the key (D2610), while
item 6 asks a refusal to print the command line that works. Both read this
type.

## 2. What a registrant may rely on

- **The key is the pattern.** One `Writer` answers for every key a pattern
  matches. `WriterOf` answers the row registered under the key itself when
  there is one, and otherwise the matching pattern with the fewest varying
  segments; two patterns of equal specificity are ordered by their text, which
  now orders the table rather than an answer, since two of them that could match
  one key cannot both be registered (below). The answer never depends on the
  order two families registered in.
- **The matching rule is the registry's own.** Two spellings match when they
  have the same number of dot-separated segments and every segment is the
  key's own, a `*` standing for exactly one **non-empty** segment. It is
  `settingsPatternMatch`'s rule in `internal/cli/settings_set.go`: a row like
  `stages.*.claude_account` is answerable at `stages.review.claude_account`
  because the two rules agree. `TestWriterOfMatchesWildcardRows` holds the two
  to the same cases.
- **One pattern has one registrant, and no two patterns of equal specificity
  share a key.** `RegisterWriter` refuses a pattern that is already registered,
  and it refuses a pattern that **overlaps** an existing one with the same
  number of `*` segments — two patterns that can match one key, like `a.*.b`
  and `a.x.*` — naming both. Which of the two answered would otherwise be the
  table's text order rather than the specificity this document promises, and a
  reader cannot tell from either pattern that the other exists. Patterns of
  different star counts stay allowed even where they overlap — `a.x.*` and
  `*.*.b` both match `a.x.b` and neither contains the other — and the answer
  there is the pattern with the fewest varying segments, fixed by the star
  count and never by the order the two registered in. One writer per field is
  therefore enforced exactly among equal-star patterns, by this refusal, and
  across different counts by the star-count rule the table is ordered by. The
  first registration stands, and two families naming one row is a diff to
  settle, not a registration to win.
- **Registration is a package `init`, and its error is a programming error.**
  The family panics on it, so a duplicate or an out-of-vocabulary writer fails
  every command of the binary and the conformance test loudly rather than one
  of the two silently winning.
- **The vocabularies are closed, and a writer states one of the two shapes.**
  `Store` is `local`, `project`, `repo`, `node`, `budget` or `none`; `Label` is
  `plumbing`, `fake-endpoint`, `file` or the empty string; every other value is
  refused. A row a command writes carries a command, a non-empty `Argv` and a
  store other than `none`; a labelled row carries neither an `Argv` nor a
  store. A writer stating neither shape or both is refused at registration, so
  no reader is handed an empty command line to run or a label beside one.
  `TestLabelIsOneOfTheThree` and `TestWriterStoreIsOneOfTheSix` pin them.
- **`Store` is where the registered `Argv` lands** — the file or database the
  write touches, not the place the value is served from. A pool is written in
  the local config and served from the published node: its store is `local`.
  It is what the conformance test asserts the key changed in (D2610). A key
  with more than one layer registers the writer whose argv names the layer it
  wrote; a command writing another layer hands the hook the store it wrote in
  rather than this row's (see §4). It is **one of the two facts** the hook reads
  for the stages a write owes and not the whole of them (§3): a store of
  `project` is a write that touched the project layer and owes the `layer`
  stage, a store of `node` owes the `policy` stage, and the other fact is
  `Writer.Node`, which is how a key the node serves is stated — a pool's store
  is `local` and no one store says that the node serves it too. One pattern
  therefore answers for every key it matches, whatever place the write landed
  in, and no second registration is needed for a key the node also serves.
- **`Node` says the published node serves the key besides its store.** It is
  `true` for a key the node carries to every run although the argv writes
  somewhere else — `pools.*.*` is the case: the write lands in the local config
  and the node serves the pool. It is the fact `Store` cannot state, because
  `afterWriteChange` carries one key and one store and a pool needs two places.
  It is `false` for the ordinary key one place serves, `RegisterWriter` refuses
  it on a labelled row (no write, no stage) and refuses it beside a store of
  `node` (one place cannot also be the second place).
- **The registered template is the table's.** `RegisterWriter` stores a copy of
  the `Argv` it is handed and `WriterOf` answers a copy of the stored one, so
  neither a family's own template slice nor a caller's copy of the answer is a
  way to edit the words another reader gets. `ArgvFor` copies too. A pattern
  whose template a caller could rewrite in place would let one command change
  what a refusal prints and what the conformance test runs for every key of that
  pattern.
- **Nothing here writes anything.** As `registry.go` is, this file is
  read-only knowledge: no file, no cloud object, no `internal/cli` import. The
  writers live in `internal/cli`, one file per family.
- **`Argv` is words, and three placeholders stand in it.** It is the command
  line without the leading `fugaro`, and `ArgvFor(key)` fills it and returns a
  copy. `KeyPlaceholder` (`<key>`) is the whole key; `<1>`, `<2>`, … are the
  key's segments at the pattern's first, second, … `*`, counting the stars left
  to right — a `pools.*.owner` template writes `<1>` where the pool's name goes,
  so `pools.team.owner` fills it with `team`, and `accounts pool add
  pools.team.owner` is never the line a reader is handed; `ValuePlaceholder`
  (`<value>`) is the value, which `ArgvFor` leaves as written for the caller to
  fill — T1 chooses one per row. A wildcard row is written at one concrete name
  (`workflows.*.commands.test` at `workflows.web.commands.test`), so the
  template is what is registered.

  A placeholder is a **whole word**: `<1>` inside a longer word (`--pool=<1>`) is
  the caller's own text and is left alone. A `<n>` the registered pattern has no
  nth `*` for is **left as written** rather than dropped or refused: a key the
  pattern matches has exactly one segment per pattern segment, so there is
  nothing to fill it with, and `ArgvFor` answers a `[]string` with no error to
  refuse with — it is the line a refusal prints and the line T1 runs, and a word
  silently dropped would run a different command than the template states. A
  `Writer` built by hand rather than answered by `WriterOf` carries no pattern,
  so it fills `<key>` and leaves every `<n>`.

## 3. What a reader may rely on

- **The conformance test (T1).** A row whose writer carries no label is run:
  `ArgvFor(key)` against a hermetic config, asserting the key changed in
  `Store`. A row registered with a label is not run — the label and its reason
  are the fact that stands in for one (T5). `TestWriterConformanceTheOwnerWritesTheKey`
  is the pin, and `TestWriterConformanceEveryRowHasAWriterOrALabel` the
  containment rule: every registry row has a runnable writer or a label.
- **A refusal (W3).** For a key `WriterOf` answers with a runnable writer, the
  refusal prints `fugaro` followed by `ArgvFor(key)`: the exact line that
  works. A key no writer and no pattern answers for keeps today's family
  sentence. A refusal never sends the reader to `fugaro update` as a step.
- **The hook (N3, every family).** A semantic command calls `runAfterWrite`
  once, after its write has landed and before it returns, with the key as
  `fugaro settings` spells it and the store it wrote in. **The stages a write
  owes are the ones the layers it touched need**, and that is two facts, not
  one, because one store cannot say a key the node serves (D2615):
  - **The store the write landed in** — `afterWriteChange.Store` — says which
    layer the write touched. `project`, the project layer file, owes the `layer`
    stage, the stage `fugaro update` runs for that file. `node` owes the `policy`
    stage. `repo`, `local` and `budget` name no stage of their own: a checkout's
    `fugaro.yaml` and the local config are files the next run reads, so a write
    that touched only one of them owes nothing for touching it — a write to a
    `[project repo]` key with `--layer repo` is a repo write, and the `layer`
    stage's own refusal in an agent session must not be run for a write that
    never touched the project layer.
  - **`Writer.Node`** says the published node serves the key besides its store,
    and that is what a pool needs: its write lands in the local config and the
    node carries it to every run, so a pool write owes the `policy` stage with a
    store of `local`, and a hook reading the store alone would run no stage and
    a pool the owner added would never reach one.

  So the `layer` stage runs exactly when the store the write landed in is
  `project`, and the `policy` stage exactly when that store is `node` or the
  key's writer says `Node`; a write neither fact reaches owes no stage at all,
  and a write both reach — one that landed in the project layer file for a key
  the node also serves, which is the "both" D2615 names — owes both, in
  `stagesNamed` order. The key is answered by **the same wildcard rule the
  writer registry uses** (`WriterOf`, §2): `settings.Lookup("pools.team.owner")`
  finds no row, because the registry's row is the pattern `pools.*.owner` and
  `Lookup` matches a key spelling and not the patterns a key falls under, so
  only `WriterOf` can say a pool write is one the node serves. A write of a key
  no writer answers for (`WriterOf` false) owes what its store owes and nothing
  more. The caller encodes no stage name, so a stage renamed in `initflow` moves
  here with no caller edited. On a stage failure the local change stays, the
  command exits non-zero, and one sentence says the change is saved and not yet
  live (composite D1563).
- **The hook is inert until N3.** `runAfterWrite` is the stub's no-op, which
  returns `nil` and runs nothing, so a family built against this contract
  changes no behaviour a user sees until the stage lands.

## 4. Who registers which pattern

**T1** registers the rows that are no family's own:

- every row the generic path writes — one pattern each, walked over
  `settingsGenericKey` rather than restated, so a row the allowlist gains or
  loses moves T1's table with it (`writers_table.go`);
- the commands no task of this plan replaces: **`budget`**, **`workspace`**,
  **`secrets`** and **`project`** (clone URLs, the image map and catalog, the
  policy publish, triage).

**The families register their own**, and no pattern has two registrants:

| Registrant | Task | Patterns |
| --- | --- | --- |
| The secrets work's **S1** | not this plan | `secret_expires`, `secret_rotation_days`, `bitbucket_api_roots` — reserved to it here; T1 leaves the three unregistered, so a write of one keeps its family sentence until that writer lands (D2616 keeps the question open) |
| pools | **A1** | `pools.*.*` |
| crafts, grades and a repository's accounts | **A2** | `crafts.*.access`, `model_grades.*.access`, `repos.*.claude_accounts` |
| the credentials cascade | **K1** | the cascade carrier's leaves |
| binaries | **K2** | `binaries.*`, over binary-allow-rule's carrier |
| `models use` | **M1** | `agent.model`, `agent.recipe`, `agent.models.*`, `agent.levels.*.*`, and the `layer.defaults.agent.*` and `layer.claude_models` spellings |
| providers and prices | **M2** | `providers.*.*`, `model_prices.*.*` |
| `rigor set` | **M3**, **RG3** | the layer's `layer.defaults.rigor.default` and `layer.defaults.rigor.areas.*.{name,match,level}`; `--repo` writes the layer's `defaults.rigor.repos.<owner/name>` and a rule's members through the rows those layer entries own. A repository's own `rigor.default` and `rigor.areas` are labelled, not registered: R15 refuses `--layer repo` for both |
| security paths | **M4** | repo `security.patterns`, `layer.security.*` |
| the labelled rows | **L1** | every row no command writes, with a label and its reason |

A family that finds one of its rows registered by T1 removes it there in the
same change — the registration is the row's owner, so the two cannot both
stand, and the registry refuses the second.

## 5. The label vocabulary is three (D2611, T5)

A row no command writes is registered with one of three labels and no argv:

- **`plumbing`** — `fugaro setup` and `fugaro update` write it as they install
  and converge: an endpoint, a generation counter, the installation's own
  bookkeeping.
- **`fake-endpoint`** — only a test writes it.
- **`file`** — a key in a named file nobody edits. This is the one label whose
  meaning is not in the label, so the row states its one-line **`Reason`**, or
  it is promoted to a settings row instead. `RegisterWriter` refuses a `file`
  row with no reason, and the label with its reason is the pinned fact
  (`TestEveryUnwrittenRowCarriesALabelAndAReason`, L1).

The empty label is the ordinary row: a writer with an argv that runs. A label
that is not one of the three is refused; one label `manual` was declined
because it fails item 8's stated reason.

## 6. The layer flag is on every writer (T3)

Every writer takes `--layer repo|project|local`, and a layer the key does not
have **refuses naming the layers it does have** — the rule D2605 uses and
tension T3's recommendation (A3). A key with one real layer (a pool lives in
the local config and the node) refuses a wrong layer with a sentence naming
the right one rather than growing a second layer.

The flag changes the store a write lands in, so the command passes the store it
actually wrote in rather than assuming the row's: the store is where the write
landed and what the conformance test asserts (D2610). It is also what says which
layer the write touched, and only that layer's stage is owed for it (§3,
D2615): `--layer project` owes the `layer` stage, and `--layer repo` on a key
that has a project layer too owes it nothing, so a repo write never meets the
`layer` stage's refusal in a session that would refuse a project-layer write.
The one stage the store does not decide is `policy`, which `Writer.Node` adds
for a key the node serves wherever the write landed.

## 7. The not-yet-live sentence (D2614)

On a stage failure the command exits non-zero and prints one sentence saying
the change is saved and not yet live. Until composite **AP3** builds `fugaro
apply`, that sentence **names `fugaro update`**, and it comes from **one
helper** — the sentence a caller prints is never written at the call site, so
the day `apply` lands the wording is repointed in one place and no other file
carries it (N3; `TestAfterWriteSentenceIsOneHelper`).

## 8. Open point: a widening write and the agent session (D2617)

D2617 is answered and binds this work: **every widening write by a semantic
command is `Typed` and refused in a coding-agent's session whatever the gate
says** — a craft's or a grade's access, `fallback auto`, an allow pattern,
`layer.claude_models`, a provider's `base_url`/`allow_data_to`/`require_zdr`/
`route_fee_pct`, a security path. `Writer` carries no field for it yet, and
this document does not invent one: the write path's own fence (`Typed` in
`applySettingsEdit`) is what refuses it today, and `accounts pool` keeps its
unconditional agent refusal besides. A contract change of this kind (CR1 or
CR2 in the plan's §4) adds the field the day a family finds the fence cannot be
stated on its own path; CR1 did not need one.

## 9. What this contract forbids

- **Nothing here may let an agent session publish** (composite D1576, D2607):
  the hook runs stages whose own refusals and audit apply, and the contract
  adds no path around them.
- **No command waits on stdin, and no command asks a question.** Fugaro asks
  for nothing; `--ask` is the whole exemption and it is never an agent
  session's. A writer registered here is run with stdin closed by the
  conformance test.
- **A value never travels through the contract.** `afterWriteChange` carries a
  key and a store; the value is in the file the command wrote, and a provider's
  secret never appears in argv, a diff, a log or a record (M2).

## 10. The tests that hold this document

| Claim | Test |
| --- | --- |
| The wildcard rule, and the specific row deciding | `TestWriterOfMatchesWildcardRows` |
| One pattern, one registrant | `TestRegisterWriterRefusesADuplicatePattern` |
| One writer per field: an overlapping pattern of the same specificity refused | `TestRegisterWriterRefusesAnOverlappingPattern` |
| `<key>`, `<1>`, `<2>` … and `<value>`, and a `<n>` with no `*` to name | `TestArgvForFillsTheKeyAndItsSegments`, `TestArgvForLeavesAPlaceholderItDoesNotOwn` |
| The registered template is the table's: copied in, copied out | `TestWriterOfClonesTheArgv` |
| The three labels, a `file` row's reason, the store vocabulary | `TestLabelIsOneOfTheThree`, `TestWriterStoreIsOneOfTheSix` |
| A pool's write: `local` store, and the node serves the key so the `policy` stage is owed | `TestWriterStoreSaysTheNodeServesAPool` |
| Every row has a runnable writer or a label, and the owner writes the key | T1's `TestWriterConformanceEveryRowHasAWriterOrALabel`, `TestWriterConformanceTheOwnerWritesTheKey` |
| Every unwritten row carries a label and a reason | L1's `TestEveryUnwrittenRowCarriesALabelAndAReason` |
| The hook runs the stage, and the sentence is one helper's | N3's `TestAfterWritePublishesThePolicyStage`, `TestAfterWriteSentenceIsOneHelper` |
