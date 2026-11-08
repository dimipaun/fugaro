# Failure classes: a closed vocabulary on the run record, and a failures report

**Status: a design, and its FC1–FC4 have shipped; the rest of §10 is not built.** This document
recommends, and names the alternative and what it costs, for every question it takes up; the owner
rules, and the design's own decisions — §8 D1–D9 and §9 O1–O6 — stay his. The plan's decisions,
D1450–D1469, are answered 2026-11-07, each as recommended. **FC1** — the vocabulary, the record's
field and the schema's enum — **FC2** — the one writer of a terminal status (§4.1) — **FC3** —
the classes the runner knows from its own state, passed at the site that already knows why the run
is stopping (§4.2) — and **FC4** — the classifier over the agent's own message, which reads the
text the stage already folded and copies no byte of it (§4.3) — are on `main`: `internal/failure`,
the `failure.Classes()` slice §3 names, `Record.FailureClass`, `recordEnd`, which is what every
statement §1 counted writes its ending through now, `haltClass` over the closed halt reasons, the
class `Decide`, `finalize`, `shadowFinalize` and the configuration refusals return beside the
reason they already did, and `stageFailureClass`, which names the cause inside a stage's own error
result.
**FC5–FC8 are not started.** What is written here was read against `main` on 2026-11-05, and the
line citations are taken from that tree.

Related: [v1.md](v1.md) §4.5 (failure paths), §4.6 (the run record) and §4.8 (`runs ls`),
[artifact-runs.md](artifact-runs.md) (the report a run writes with the digest), the digest's own
design ([task-governance/reconciliation.md](../task-governance/reconciliation.md), D320–D328, and
[periodic-reviews/README.md](../periodic-reviews/README.md)), the no-verdict label
([binary-allow-rule/spec.md](../binary-allow-rule/spec.md) §8, D994–D996), the 90-day retention
([rigor-modes/reconciliation.md](../rigor-modes/reconciliation.md), F24) and the backlog's one folder
([backlog/FORMAT.md](../backlog/FORMAT.md)).

## 1. What is true today

**The record carries the cause as free text and nothing else.** `runstore.Record`
(`internal/runstore/runstore.go:651`) has `Status` (`type Status string`, `runstore.go:26`), `Outcome`
(`type Outcome string`, `runstore.go:116`), `Reason` (`runstore.go:684`, `json:"reason,omitempty"`) and, for a halt,
`Halt` (`runstore.go:744`) whose own closed `HaltReason` (`runstore.go:44`) is written by the runner.
`Reason` is a sentence the runner wrote, sometimes quoting what the agent or a provider said
(`stageErrorText`, `internal/runner/runner.go:3887`), and it is the only place the *why* of a
non-success run is kept. `runview.Row` copies it for the listing (`internal/runview/runview.go:106`),
so `fugaro runs ls --json` carries one `reason` string per row and no grouping anywhere.

**Nothing groups failures, and nothing has ever produced a group.** `fugaro diagnose RUN`
(`internal/cli/diagnose.go`) explains one run; `fugaro runs ls` lists rows; `fugaro runs stats`
(`internal/userstats/userstats.go`) counts runs by *person* and buckets every status into
`succeeded/failed/halted/infra_error/other` (`userstats.go:124`–`136`); `fugaro runs digest` writes
one closed period's report of one repository into that repository's tree, and its per-run
"oddities" are six closed flags — `review_calls`, `subagents`, `halt`, `fix_rounds`,
`repeated_command`, `review_scope_drift` (`internal/digest/digest.go:79`–`97`) — each a property of a
run, none of them a cause. So the owner's own 7-day report had to be built by normalizing this free
text with a script: **the vocabulary below exists because the arithmetic that answers "where does the
money go" was done by hand in a shell.**

**A run's ending is written in one place: `recordEnd`, and no statement of `internal/runner`
outside `internal/runner/recordend.go` writes a record's `Status`, `Outcome`, `Reason` or
`FailureClass`.** FC2 is the task that made it so. Before it the census was **36 statements, in 24
functions across 8 files** — every statement of `internal/runner` that assigned a `runstore.Status`
to a record's `Status`, over the non-test files:

- `artifactMode` (`internal/runner/artifact.go`), `endArtifactCancelled`,
  `endArtifactHalted`, `failArtifactInfra` and `endArtifactFailed`;
- `storeUnposted` (`internal/runner/followup.go` — a copy of the record, for the
  stored report, rendered by `recordend.go`'s `reportCopy`) and `endUnchanged`;
- `endWithoutPR` (`internal/runner/gate.go`), `failInfra`, `endCancelled` and `endHalted`, for a run
  whose check gate decided it;
- `endRefused` (`internal/runner/refused.go`), recording a push the host will never accept;
- `writeReviewArtifact` (`internal/runner/reviewartifact.go`);
- `refuseReview` (`internal/runner/reviewmode.go`), `reviewMode`,
  `endReviewCancelled`, `endReviewHalted`, `failReviewInfra`,
  `endReviewFailed` and `endReviewInvalidVerdict`;
- `refuseArm` (`internal/runner/shadow.go`) and `shadowFinalize`, for a
  challenger arm;
- `Run` — the deferred fallback (`internal/runner/runner.go`) and the two bootstrap terminals;
- `finalize`'s switch (`internal/runner/runner.go`), which decides
  succeeded/halted/cancelled/failed and whose four cases were four of the 36.

Twelve of the functions were the `end*` helpers, three were the `fail*Infra` helpers and two were the
`refuse*` writers; the rest wrote inline, which is why the count was a count of statements and not of
functions, and each wrote `Status`, `Outcome` and usually `Reason` in a statement of its own. Every
one of those statements is now one `recordEnd(…)` call, so the site that already knows why the run is
stopping is the site that says what class it stopped for, and no reader derives a class, caches one or
writes one back (§4.1). The one ending that is not written to the record itself — `storeUnposted`'s
copy of it — goes through the same writer, from the file that owns it. The sentence in bold is what
the pin in
`internal/cli/docs_pr_state_test.go` applies to the parsed files of `internal/runner`, and what
`TestRecordEndIsTheOnlyWriterOfATerminalStatus`
(`internal/runner/failure_class_internal_test.go`) holds inside the package.

**The run's first failure reason has one writer.** `r.fail` (`internal/runner/runner.go:1547`) records the first
reason the run cannot produce a ready PR and `failLocked` (`internal/runner/runner.go:1554`) keeps it; every stage
failure, panic and refusal reaches it (`internal/runner/runner.go:3419`–`3837`), and `recordHaltLocked`
(`internal/runner/halt.go:57`) calls it too, with the halt's own text. `r.failure()` (`halt.go:128`)
reads it back for the pull request's status section and for the reasons `finalize` decides with. That
is the *report's* reason and the *record's* reason in most runs — but not in all of them:
`endHalted` (`gate.go:422`), `endRefused` (`refused.go:327`) and `endUnchanged` (`internal/runner/followup.go:561`)
end the record with a reason of their own, so the record's reason and `r.failure()` can disagree today.

**The agent's own words are already classified, once, narrowly.** `subscriptionUsageLimit`
(`internal/runner/runner.go:3913`) reads a stage's error text (`res.Text`, set when the result event carries
`is_error`, `internal/runner/runner.go:3836`) against a three-phrase table (`oauthLimitPhrases`, `internal/runner/runner.go:3905`) to
decide whether the run stopped on the OAuth account's spent allowance, and
`stageErrorReason` (`internal/runner/runner.go:3862`) uses that to pick one named reason instead of quoting the text.
The subtype is no help — Claude Code reports a refused API call as subtype `success`, which is why
that path reads the text at all.

**The digest's pattern rules already read a committed block over time.** `internal/digest/patterns.go`
holds four fixed rules (`patterns.go:19`–`42`) read over the `summary` block a report carries
(`internal/digest/render.go:91`) and the blocks of the reports committed before it, each with its own
key so two periods are comparable (`Patterns`, `patterns.go:91`). Nothing in that block says why a
period's runs failed.

## 2. What the data says: the seven days to 2026-11-05

**Source.** `fugaro runs ls --since 7d --json`, read as one row per run: the row's `reason`, its
`halt` (for a halted run) and its `outcome`. The figures below are that listing's, and every row's
cause is a sentence in `reason` — which is the whole problem: two readers of the same window can only
agree on these figures by agreeing on a normalization of prose. (No run id and no transcript text is
quoted anywhere in this document: a row's reason quotes what a provider or an agent said, and none of
it belongs here.)

| Window | Runs | Not succeeded | Share | Spend | Share of spend |
| --- | --- | --- | --- | --- | --- |
| 7 days to 2026-11-05 | 585 | 191 | 33% | $354.68 | 42% of $844.27 |

191 runs, $354.68 — the table groups them by the cause `reason` names, in the order the owner's own
report put them:

| Cause (the words in `reason`, and the row's other fields) | Runs | Spend |
| --- | --- | --- |
| The review loop used its rounds and findings were left: `review round N still has N findings`, `outcome: draft`, a branch pushed | 97 | $151.60 |
| Run cap reached: `halted: run_cap` (`halt.reason`) | 28 | $136.51 |
| Tests failing on the final commit: `tests failing on the final commit` | 21 | — |
| `the agent reported an error` with subtype `success` (three different things, §3: a provider or gateway auth refusal, mostly 2026-11-01 21:00–22:30 at $0 model cost; `API Error: stream closed before completion`; and review- and fix-stage runs on 2026-11-04 that spent $0.50–$3.20 and then reported `is_error` after a normal final message) | 20 | — |
| No verified test run on the final commit, or work committed at finalize after the last verified run (`no verified test run on the final commit`, `uncommitted changes were committed at finalize, after the last verified test run`) | 5 | — |
| Configured maximum above the model's own: `max_tokens N for model deepseek/deepseek-v4-pro is above the model's maximum` (all 2026-10-31, since fixed) | 7 | — |
| GitHub GraphQL error marking the pull request ready (the work was complete) | 3 | — |
| Cancelled by a person | 4 | — |
| One-offs, the remainder that closes the table to 191 (the named rows hold 185): a pinning refusal, a pull request merged during the run, a push race, an agent exit with no result event, no commits | 6 | — |

**Three facts this table cannot state, and they are the point of the design.** First, 97 runs whose
label is `failed` carry a usable draft pull request: `outcome: draft` and a pushed branch. Second,
the 20-run "the agent reported an error" row is three causes with three different responses, and only
the text tells them apart. Third, a run whose *work landed* (the GraphQL error marking the pull
request ready; a pull request merged during the run) is indistinguishable, from `status` alone, from
one that did nothing.

## 3. The class vocabulary

**A new field on the record: `failure_class`, a closed enum in snake_case, one of the names below or
absent.** It is written for every run that did not end `succeeded`; a succeeded run, a `mode: review`
run that reviewed (`outcome: reviewed`), an artifact run that delivered (`outcome: artifacts`) and a
row that never started carry none. `reason` keeps its meaning and its text exactly as it is: a field
never changes meaning, and the class is an addition beside it, never a replacement.

**Each class carries four properties, written once in code and printed by the report**, so a reader
knows what to do without reading a sentence:

- `kind` — what family the cause is in: `quality` (the work is not what the rule wants),
  `budget` (a limit stopped it), `infra` (the run's own machinery or the model route),
  `external` (the forge or the platform), `config` (the resolved configuration was refused), `other`.
- `retryable` — whether running the same task again, without changing anything else, could end
  differently. It is a claim about the cause, not a promise.
- `needs_owner` — whether a person has to do something before the class can be avoided at all.
- a definition, which is what the runner tests its own state against.

| **`failure_class`** | The run ended because | kind | retryable | needs owner |
| --- | --- | --- | --- | --- |
| `review_not_converged` | the deciding review round's verdict was `changes` and the mode did not tolerate its findings, or a level that had shipped was held back by one that did not (`review round N still has N findings`, `heldBackReason`, `internal/runner/outcome.go:73`, `:81`). The work is on the branch and the pull request is a draft. | quality | no — a follow-up on the pull request is the retry | yes |
| `review_unread` | no round the rule can read: no deciding round at all, or a verdict of `none` after the retry (`no review verdict`, `review round N returned no parseable verdict after a retry`, `outcome.go:54`, `:65`) | quality | no | yes |
| `tests_failed` | the final commit's test record failed (`tests failing on the final commit`, `outcome.go:52`) | quality | no | yes |
| `unverified_final_commit` | the pushed head carries no passing test: no test record on that commit, or work committed at finalize after the last verified run (`ReasonNoVerifiedTest`, `outcome.go:26`; `internal/runner/runner.go:4243`) | quality | no | yes |
| `no_commits` | the agent made no commits (`internal/runner/runner.go:4196`) | quality | no | yes |
| `halted_budget` | a dollar or token cap stopped the run: `run_cap`, `repo_daily_cap`, `global_daily_cap`, `token_cap`, `no_cap` (`runstore.go:49`–`53`) | budget | yes, once the cap is raised | yes |
| `halted_governance` | a governance tripwire stopped the run: `stage_budget`, `review_ratio`, `fanout`, `review_scope`, `expected_exceeded` (`runstore.go:81`–`95`, `GovernanceHaltReasons`, `:103`). The checkpoint says what it would have done next. | budget | no, until the checkpoint is read | yes |
| `halted_credential` | no credential became usable, or the budget database refused: `no_credential`, `creds_rules_denied`, `budget_unavailable`, `budget_token_expired` (`runstore.go:54`–`71`) | infra | yes, once the account is usable | yes |
| `halted_kill_switch` | the project's or the repository's kill switch was on (`HaltKillSwitch`, `runstore.go:48`) | infra | yes, once the switch is off | yes |
| `cancelled` | a person cancelled the run (`markCancelled`, `halt.go:101`, and the reason `cancelled`) | other | yes | no |
| `provider_auth_refused` | the model route or the credential refused the call: `403` from the provider, or a failed authentication (§4.3) | infra | no | yes |
| `transport_failed` | the call's stream or connection died before completion: `API Error: stream closed before completion` (§4.3) | infra | yes | no |
| `stage_error_unexplained` | an `is_error` result the closed classifier does not name, with or without text (§4.3): in the window, the runs where the agent finished its fix and wrote a normal final message and the stage was recorded as an error anyway | infra | no (D4) | yes |
| `stage_no_result` | the stage's process ended without writing a result event | infra | yes | yes |
| `config_invalid` | the resolved configuration was refused before the work could run: a `max_tokens` above the model's maximum, a recipe the runner cannot read | config | no | yes |
| `git_provider_error` | the forge refused or failed a call the run needed — the GraphQL error marking the pull request ready, a comment that would not post | external | yes | no |
| `branch_moved` | the branch or the pull request changed under the run: a push race, a pull request merged during the run, a push the host will never accept (`endRefused`, `refused.go:327`) | external | yes | yes |
| `infra_error` | the run never got far enough to name anything else: bootstrap failed, the process died, the record was never settled (`internal/runner/runner.go:585`, `runview.ReasonNoFinalRecord`) | infra | yes | yes |
| `unclassified` | the terminal the run reached has no class of its own (`stage_no_result`'s neighbour: a class the vocabulary does not yet hold). The report prints its count, and that count is the signal to name one. | other | yes | yes |

**Nineteen values, and the set is closed in Go, in the schema and in the digest**, the way
`runstore.HaltReason` and the six digest flags are: one slice the tests read (`failure.Classes()`,
`internal/failure/failure.go`, built by FC1), the schema's enum, and the report's table all built from
it (the shape `TestResultSchemaStatusesAgreeWithGo` already uses for `status`).

### 3.1 "Delivered despite the label": not a class

**The class vocabulary stays causal, and the delivered fact gets its own column.** The 97 runs above
are not `failed` because they delivered; they are `failed` because the review did not converge. A
class named `delivered_draft` would not be a cause — it would be a *reading of the outcome* — and it
would have to be true of a run at the same time as its own class, which no first-wins rule can
produce (§4.2). The fact belongs where it already lives: `outcome: draft` (or `ready`) together with
`pushed_head`/`pr` say whether a person can pick the work up, and the report's table gains a
`delivered` column computed from those two fields — never a third field, because two writers of one
fact is the thing this tree's planning rulings forbid (the sibling plan's shared-files rule is that
argument's home).

**Recommended, and it is the design's answer to "include a class for delivered despite the label":
the name is `delivered` and it is a column, not a class.** *Alternative:* a `delivered_draft` value
in the enum, assigned by the same writer. *Cost of the alternative:* the value is not a cause, so it
breaks the one rule that makes the enum usable ("the class says why, the status says how far"), it
cannot be the first class of a run that also has one, and every reader then has to know which of the
nineteen values are causes and which is a consequence. *Alternative:* a second field `delivery`.
*Cost:* a second vocabulary that can disagree with `outcome` and `pushed_head`, i.e. a fact with two
writers — and §8 D5 shows the *status* question it is really about.

## 4. Where the class is assigned

### 4.1 One writer, and it is the one that writes the status

**Recommended: a single function on the run, `recordEnd(status, outcome, class, reason)`, is the only
writer of a terminal `status`, `outcome`, `reason` and `failure_class`.** The class is decided by
*the same decision that decides the status* — the site that already knows why the run is stopping
passes the class beside the reason — and no reader anywhere derives it, caches it or writes it back.

Today no such function exists: 36 statements in 24 functions write the fields, each in a statement of
its own (§1). The change is mechanical and it *removes* writers rather than adding one: every one of
those statements becomes one `recordEnd(…)` call, and the deferred function in `Run`
(`internal/runner/runner.go:586`), the two bootstrap terminals beside it (`:652`, `:657`),
`finalize`'s switch (`internal/runner/runner.go:4371`) and the twelve `end*` helpers with the three
`fail*Infra` and two `refuse*` writers all go through it. Its comment states the rule once: *the last
call wins, and the four fields are written together, so a class can never describe a status the
record does not carry.* The one statement it must account for rather than absorb is `storeUnposted`
(`internal/runner/followup.go:522`), which renders a copy of the record into the stored report and
not the record itself, through `reportCopy` — the copy's own write, in `recordend.go`, so that no
call to `writeEnd` sits in another file either.

**Why this and not `fail`/`failLocked`.** `failLocked` is the one writer of the run's **first**
failure reason, which is what the pull request's status section and `finalize`'s decision read — and
it is deliberately *not* the record's terminal reason in three cases (`endHalted`, `endRefused`,
`endUnchanged` overwrite it, §1). Hanging the class on `failLocked` would therefore give a halted
run the class of the failure that came before its halt, and the class would describe a sentence the
record no longer holds. *Alternative:* the class beside `failReason` in `failLocked`.
*Cost:* that disagreement — 36 statements that write a status, a reason and a halt already exist, and the
class would have to be reconciled at read time, which is the reader deciding. *Alternative:* the
class in `finalize`'s switch alone. *Cost:* a run that never reaches `finalize` — a bootstrap
failure, a push refused, a cancelled review run, a challenger arm — carries none, and those runs
are the six the one-offs row of §2 counts, that row being the remainder that closes its table to
191 (the named rows hold 185); every `infra_error` run is unnamed there too and so is one of them.

**What is not in this change.** `fail` keeps its signature and its job (the pull request's report
reads it); `r.rec.Reason` is not reworded anywhere; the pull request's section, `diagnose`'s output
and the six digest flags do not move. The one new thing is that the 36 statements become one call,
at the site that already knows why the run is stopping.

### 4.2 How a class is chosen where several apply

**The class of the call that wrote the terminal status wins, and the four fields are written
together — so the class always describes the record's own `status`.** The precedence a reader sees is
therefore the one `status` already has: a run that failed and was then halted is `halted` /
`halted_budget` (the halt's `endHalted` call is the last one); a run that was cancelled after a tests
failure is `cancelled` / `cancelled`; a run whose last word is `finalize`'s switch takes the class
that switch's decision names. This is stated once, in `recordEnd`'s comment, and the report never
re-derives it: the class it groups by is the field.

**It does not always describe the record's `reason`, and one case says so plainly.** `finalize`
writes `r.rec.Reason = reason` (`internal/runner/runner.go:4370`) *before* its switch, so a run
cancelled at that point carries `status: cancelled`, the class `cancelled`, and the reason
(`r.failure()`'s or `Decide`'s) it had already failed for — the class describes how the run ended,
and the reason describes what it was about to report. A reader pairing the two is told the truth by
each field; a reader taking the reason as the class's cause would be wrong, which is why §6's tables
print the class and never read a cause out of the sentence.

**A run that reaches `finalize` with more than one refusal gets the class of the refusal the record
carries.** `finalize` computes one `reason` (`internal/runner/runner.go:4246`: `r.failure()` outranks `Decide`, and a
halt outranks both) and the switch writes it — so the class is chosen by the same three-way choice,
in the same place, by the same lines. The class the `Decide` refusal would have had is then lost,
which is correct: the record cannot hold two reasons, and the pull request's section already prints
the review's own count.

### 4.3 The classifier over the agent's own message

**The three causes inside `the agent reported an error` are told apart by a closed phrase table, and
nothing is copied into the record.** One function beside `subscriptionUsageLimit`, in the same file,
for the same reasons:

```go
// stageFailureClass names the cause inside an is_error result. The text is
// model output: it selects one enum value and is never stored, quoted or
// returned. A phrase table, matched as shapes rather than as English, is the
// rule subscriptionUsageLimit already follows (oauthLimitPhrases).
func stageFailureClass(auth string, res agent.Result) failure.Class
```

- `provider_auth_refused` — the text carries a `403` from the provider's route or the gateway's
  refusal (`the provider answered with status 403`, a failed authentication), or the stage settled
  at $0 model cost with no call answered. Mostly one hour of 2026-11-01 in the window above.
- `transport_failed` — the text carries a stream or connection that ended before completion
  (`stream closed before completion`, a dropped stream, a reset connection).
- `stage_error_unexplained` — everything else, including a result with no text at all. This is the
  class the three observed 2026-11-04 review- and fix-stage runs take: the agent wrote a normal
  final message and the stage was recorded as an error anyway, and no phrase table can name what
  happened after the fact. The class exists so those runs are *counted* rather than dropped, and so
  the next occurrence is visible in the report instead of in a person's memory.

**Why the message may be read at all, and why it is safe.** The agent's message is untrusted input,
so the only thing that may cross from it into the record is a choice among fixed values:

- **No text is copied.** The class is an enum whose values are the runner's own words; the matched
  phrase, the message and the provider's error string are never stored on the class or printed by
  the report. This is the point the owner's brief made and it is not negotiable: `stageErrorText`
  already redacts what the *reason* quotes (`internal/runner/runner.go:3887`, two redactions and a one-line fold),
  and the class adds no second copy for a secret to travel in.
- **No injection path.** A message can at most move a run from one of these three values to another.
  Nothing branches on the class except the report's grouping: no launch, no retry, no admission, no
  spend, no pull-request write reads it, so the worst a hostile or confusing message can do is make
  the report name a cause wrongly — a mislabel with no privilege (the property
  `verdict.go`'s schema and `managed_gate.go:295` state for their own vocabularies: *"no verdict"
  must never read as "admitted"*).
- **The table is a table.** The phrases are matched inside their envelope, not as English:
  `oauthLimitPhrases` records exactly why (a type merely *named* in a result text — an echoed
  snippet, a `previous_error_type` field — is not taken for the refusal itself), and the same rule
  applies here.

*Alternative:* the agent states its own class in the result protocol (a field in the harness's
output). *Cost:* the class would then be chosen by the model, i.e. by the thing under measurement,
and a model that misreports its failure would write the vocabulary the report counts. *Alternative:*
the runner classifies only from structured facts (exit status, cost, duration) and never from text.
*Cost:* the three observed causes are indistinguishable that way — the auth refusal, the dropped
stream and the post-work error all end the same way — and the 20 runs above would be one bucket,
which is the state the owner's report had to fix by hand.

### 4.4 The classes the runner already knows structurally

Two of the three halt families need no table at all: `haltClass(reason runstore.HaltReason)`
translates the halt's own closed enum into a class value with a `switch` beside `haltReasonText`
(`halt.go:159`), which is how a halt reason keeps its own meaning (`status` says *halted*, `halt`
says which cap, and the class says which family). The class is not a copy of the halt reason and
never replaces it — a report that wants `run_cap` reads `halt.reason`, which is already on the
record and in `runs ls --json`'s row.

Everything else is a structured fact the site holds: `Decide`'s refusal (four classes, chosen where
`Decide` is called, `internal/runner/runner.go:4241`), `verify.Record.Passed` for the final commit,
`r.runCommits` for an empty run, the push rejection's own `Kind`
(`gitops.PushRejected`, `refused.go`), the provider call's error type for `git_provider_error`, and
`config`'s own refusal for `config_invalid`.

## 5. Legacy records: read from the reason, never written back

**Records are kept 90 days** (F24), so on the day the field ships, most rows in any window have no
`failure_class` — and the report the owner wants is about the last seven days. **Recommended: the
reader classifies an old record from `reason` with one fixed pattern table, and never writes the
answer back.**

- **One place.** `internal/runview` gains `legacyClass(rec *runstore.Record) failure.Class` (or,
  where the class type lands, the one function the row builder and the digest both call), a table of
  ordered patterns in exactly the shape `internal/digest`'s flags and `oauthLimitPhrases` have. It
  is consulted **only when the field is absent**, and its answer is a *reading*: the row and the
  report say which it is — the position the console already holds for
  `runview.Derived`/`DerivedPreempted` (`internal/runview/runview.go:38`–`57`), *a derivation, never written back,
  derived anew on every read*.
- **The patterns are the reasons the runner already writes**, not English: `no verified test run on
  the final commit` → `unverified_final_commit`; `tests failing on the final commit` →
  `tests_failed`; `review round ` + a number → `review_not_converged` (and
  `returned no parseable verdict after a retry` → `review_unread`); a `halt` block → `haltClass`
  (the same table the writer uses, so a legacy halted row and a new one cannot disagree); the
  agent's error prefix + the §4.3 phrases → the three classes; `the agent made no commits` →
  `no_commits`; a refusal whose text names `max_tokens` and the model's maximum → `config_invalid`.
- **The table is ordered, and every pattern is anchored on a prefix the runner itself writes.** A
  reason is the runner's sentence with, at one point, the provider's or the agent's words inside it
  (`stageErrorText`, `internal/runner/runner.go:3887`), so the agent-error prefix is matched **first**
  and the phrases of §4.3 are looked for only inside a reason that carries it. The worst case of that
  test is a mislabel into one of the three classes — a mislabel with no privilege, since nothing acts
  on a class (§4.3) — and anchoring on the runner's prefixes is what keeps model text from choosing a
  class at all.
- **An unmatched reason is `unclassified`**, never a guess and never a class derived from the
  status alone. A record with no `reason` and no class reads as `unclassified` too.
- **The digest reads the same function.** The digest's own rule is that every cell of its tables is
  "a figure or a closed vocabulary: nothing here can hold a word a record chose"
  (`internal/digest/render.go:70`), so a class read from a record is *validated against the closed
  set* on the way in: a value this build does not know prints as `unclassified` and is never
  echoed — the shape `runview.StatusError` and the digest's `statuses` set already use.

*Alternative:* report the last 90 days as `unclassified` until they age out. *Cost:* the owner's
next 7-day report is empty of causes — the one report the field exists to make possible — and it
would be three months before anything was visible. *Alternative:* a one-off backfill that writes
classes into old records. *Cost:* a write to records nobody's run is writing — a second writer of a
field whose whole value is that one function wrote it — and the retention makes the work evaporate
within 90 days anyway.

## 6. The failures report

### 6.1 `fugaro runs ls --group-by failure_class`

**Recommended: a flag on `runs ls`, not a new command.** The listing already reads the window
(`api.LoadRows`, `internal/cli/runs.go`), so the group table costs one read of the same rows and
**adds no network path at all** — the owner's brief's constraint. `--group-by` takes exactly one
value, `failure_class`, refused by the flag's own closed list the way `--state` refuses
(`lsStates`, `runs.go:65`), so the flag cannot be asked for a grouping that does not exist.

- The text listing prints one row per class — `class`, `kind`, `runs`, `spend`, `share` (of the
  window's runs), `delivered` (runs whose `outcome` is `draft` or `ready` with a branch pushed) and
  an example run id — newest-first classes first? No: by runs descending, ties by name, so two
  readers of one window print the same table. The totals line keeps its meaning and now reads
  `191 of 585 runs failed (33%), $354.68 of $844.27` — the same figures, with the share the owner
  computed by hand.
- `--json` gains one block, `groups`, beside the `runs` array it already carries (`lsDoc`,
  `runs.go:214`): per class, the six figures and the properties (`kind`, `retryable`,
  `needs_owner`). The block is additive: a reader that does not know it loses the grouping, not the
  rows.
- **`--group-by` and `--limit`/`--after` are refused together** (a page of a grouped table is a
  total of a page, and `--limit`'s own help says a page is not a total); `--group-by` with `--watch`
  is refused for the same reason `--limit` is.

*Alternative:* `fugaro runs failures`, a command of its own. *Cost:* a second listing path for the
same data (a second reader of the bucket, a second place `--since`/`--repo`/`--pr` must be taught),
and a command a person has to know exists; the flag rides the command they already type. *Alternative:*
`fugaro runs stats --group-by` (the command that already aggregates). *Cost:* `runs stats` reads a
person-census document, not the rows, and the class lives on the rows — the grouping would have to
read every record the census does not.

### 6.2 The "Failures" table in `fugaro runs digest`

The digest is where a period's figures are *committed to the repository*, so the class belongs there
as well, in the same shapes the rest of that report uses:

- `internal/digest.Digest` gains `Failures []failure.ClassCount` (class, runs, billed micros, an
  example run id) built from the period's records beside `Flags` and `Recipes`.
- `render.go` gains `FailuresTable`, a Markdown table with the same discipline as `RunsTable`
  (`internal/digest/render.go:70`): every cell a figure or a closed vocabulary, the run id the report's own.
- the `summary` block (`internal/digest/render.go:91`) gains `failures: map[string]int` — class → runs — and nothing
  else: the two rules below read counts, and money per class stays out of the block so the block
  keeps its discipline of exact decimal strings and flat maps.
- **Two fixed rules join the four** in `patterns.go`, with the same shape (the keys they read, one
  figure per period, and a name in `RuleNames`): `failure_class_high` — one class above
  `FailureClassShareNum/Den` (recommended **1/4**) of the period's runs, `FailureClassWeeks`
  (recommended **2**) weeks running, the shape `halts_high` already has; and `failure_class_new` —
  a class this week that no weekly report of the last `FailureClassNewWindow` (recommended **4**)
  that carries the block names. **A rule reads only summaries that carry the block**, so the first
  week after the release raises neither: *cost of that rule, stated rather than discovered:* the
  report is silent about a brand-new class for one week, and the alternative — treating "not in the
  block" as "new" — would fire on every class in the first report, which is noise a reader stops
  reading.

### 6.3 Which commands, docs and skills change

- **Commands:** `runs ls` gains `--group-by` (§6.1). `runs digest` gains the table and the two rules
  (§6.2). Nothing else moves: `diagnose` prints one run and keeps doing so — the class is one more
  line in it, if the owner wants it there (D9).
- **`website/content/cli-reference.md` is generated** (`go run ./ee/clidoc`), so the flag's row and
  the command's prose appear when the generator runs, and `ee/clidoc/cliref_test.go`'s
  `TestCliReferenceIsCurrent` requires the committed file to be regenerated in the same commit.
- **`console/docs/json-contract.md`** gains two rows — the row's `failure_class` field and the
  `groups` block — because `TestDocsDesktopConsoleContractNamesTheOtherShapesFields`
  (`internal/cli/docs_desktop_console_test.go:353`) reflects over `runview.Row` and pins every json
  key against that contract. The console then needs its own task (a chip or a column) — out of this
  design's scope, named so it is not forgotten.
- **`docs/design/v1.md` §4.6** carries the record's field list and gains the field's sentence; the
  digest's own documents (`task-governance/reconciliation.md`'s D326 and the periodic-reviews
  folder) gain the two rules and the table, in the task that adds them.
- **Plugin skills:** no skill text changes *because of the field* — nothing in `plugin/` enumerates
  `runview.Row`'s keys except `plugin/routing_skill_test.go:86`, which pins `launched_by` alone. If
  the implementing work wants a person to *use* the grouping, one sentence in
  `plugin/skills/working/reference/diagnose.md` (the file that tells a reader what to do with a
  failed run) is its home; `plugin/skills_lint_test.go` resolves every `fugaro …` a skill names
  against the tree, so a skill naming `--group-by` must name a flag that exists — the flag and the
  skill sentence land together or not at all.

## 7. Wire and version effects

**What this adds, and what does not move.** `failure_class` is a **new field** on a format a reader
outside the same binary consumes, which the field rule allows: additive, no version bump, no meaning
borrowed.

| Format | Change | Version |
| --- | --- | --- |
| `result.json` (`schemas/result.schema.json`) | the `failure_class` property, an enum of the nineteen values, **not** in `required` (a succeeded record carries none) — the convention `artifacts` already follows (`schemas/schemas_test.go:1320`) | `"version"` stays `const 1` (`result.schema.json:10`); `additionalProperties: false` means the schema and `runstore.Record` move in the same commit or `TestResultSchemaAcceptsRunnerRecords` (`schemas/schemas_test.go:694`) fails |
| `runs ls --json` (`lsDoc`) | the row's `failure_class`, and the `groups` block under `--group-by` | no version key exists on this document; additive |
| the digest's committed report and its `summary` block | the Failures table and `failures` in the block | no version key: the block is read key by key, and the two new rules read only the newer blocks (§6.2) |
| `console/docs/json-contract.md` | two rows | the contract's own version constant is the console's; adding a row is additive |

**Who reads what, and why nothing breaks.** A reader older than the field sees the record and the row
it always saw (`omitempty`, additive), and the console's redactor already admits `status`, `stage`,
`outcome` and every other contract key by enumeration (`console/src/source/redact.ts:1160`) — a new
key arrives as a new enumeration entry, not as a surprise, and an unknown *value* is read as
`unclassified`/unknown rather than refused (§5). `mobile/` and `shared/` do not read `runs ls` at
all; `internal/userstats` counts statuses and needs no change (the class is not a status);
`internal/watch`'s `isFailure` (`internal/watch/finished.go:43`) switches on three status values and
is untouched.

**A class value added later is additive too** — the enum grows inside an existing field, so no
version moves; a reader that does not know the new value reads it as `unclassified` and the report
prints it by name only for a build that knows it. That is the property that keeps this vocabulary
from needing a migration, and it is why the *set* is written in one Go slice the schema test reads.

## 8. Decisions this design needs (recommended; none is answered)

**D1. Is `failure_class` a new field on the record, or a second reader-side derivation?**
**Recommended: a new field**, written by the runner, because a derivation is a normalization two
readers can disagree about and the owner's own report needed a script for exactly that reason.
*Alternative:* the digest and `runs ls` normalize `reason` with the §5 table and write nothing.
*Cost:* the cause stays a sentence; two builds with two tables print two figures for one week, and a
person cannot tell a record's own class from a reader's guess. **This is the design's foundation —
if the owner declines it, §§3–5 collapse to §5 and the report is a shell over prose.**

**D2. The vocabulary above: nineteen values, or fewer?** **Recommended: the nineteen in §3**, on the
rule that a class exists when a *different act* follows from it — raise a cap, read the checkpoint,
fix a credential, re-verify, read the findings, re-run. *Alternative:* one class per exact `reason`
(about forty strings). *Cost:* the report's table is long enough that nobody reads it, and the
strings are the runner's prose, which is exactly what must not be counted. *Alternative:* four
classes (quality / budget / infra / external). *Cost:* the `kind` column already says that, and the
biggest row of the window — 97 runs of non-converged review — would vanish into "quality" beside
`config_invalid` and `no_commits`, which are 7 and a handful.

**D3. One writer, `recordEnd`, replacing 36 statements?** **Recommended: yes** (§4.1).
*Alternative:* the class beside `failReason` in `failLocked`. *Cost:* the class then describes a
reason the record may no longer carry (`endHalted`, `endRefused`, `endUnchanged`), and every reader
would have to reconcile it.

**D4. The three-value classifier over the agent's message (§4.3)?** **Recommended: yes, with the
phrase table and no text copied.** *Alternative:* two values (a provider refusal and everything
else). *Cost:* the transport failures the owner named are retryable and the post-work errors are
not, so one bucket tells a reader to re-run runs that will fail again. **The table's `retryable`
column follows this reasoning**: `transport_failed` is the retryable one, and
`stage_error_unexplained` — the bucket the post-work errors fall into — is marked `no` for exactly
that reason, not because an unnamed error can never be transient.

**D5. Should `review_not_converged` end as `failed` at all?** **Recommended: not in this work.**
The class plus the `delivered` column answers the report's question, and a *status* change is a wire
change read by five clients — the console (`console/src/renderer/screens/run.ts`'s
`RUN_FAILURE_OUTCOMES` vocabulary), `mobile/`, `internal/userstats`'s census (`userstats.go:198`),
`internal/cli/runs_summary.go`'s `summaryStates` (`internal/cli/runs_summary.go:62`) and `internal/watch`'s `isFailure`
(`finished.go:43`) — and the sibling design already introduces one new terminal value for the
no-verdict case (`binary-allow-rule` NV0–NV2, `StatusUnreviewed`, D994–D996, *not on `main`*), so
the owner has one decision about the vocabulary's shape, not two. **This is Open item O1**: whether
"a usable draft exists" deserves its own status (`draft_delivered`?), whether it is the *same* new
value the no-verdict work adds, or whether the label stays and the report carries the fact.
*Alternative:* flip these runs to the NV value now. *Cost:* two designs writing one wire value in two
pull requests, and 97 runs relabelled before anyone has ruled on what the label should mean.

**D6. `--group-by` on `runs ls`, or a command of its own?** **Recommended: the flag** (§6.1).
*Alternative:* `fugaro runs failures`. *Cost:* a second listing path and a command to learn.

**D7. The two digest rules and their thresholds (1/4 share, 2 weeks; 4-week new-class window)?**
**Recommended: as written**, with the thresholds as named constants beside the existing ones
(`patterns.go:26`–`42`), where a later release moves them the way `HaltShareNum` moves today.
*Alternative:* no rules — the table alone. *Cost:* the table is per period and a person has to
remember last week's; the digest's whole point (D326) is that a committed report is read over time.

**D8. Legacy classification from `reason` (§5)?** **Recommended: yes, read-only, derived on every
read, never written back.** *Alternative:* nothing until the retention window ages out. *Cost:* the
owner's next report is empty of causes.

**D9. Does `diagnose` print the class?** **Recommended: yes, one line** — it is the command a person
runs on one run, and the class is the answer's first sentence. *Cost:* one more line on a screen
that is already long; no reader of it is a contract.

## 9. Open items (the owner's)

- **O1 — the label (§8 D5).** Whether a run that delivered a usable draft because its review did not
  converge should be reported as `failed` at all, or carry a status of its own; and if it should,
  whether that value is the one the no-verdict work already plans (`StatusUnreviewed`) or a second
  value beside it. The readers that change either way: the console, `mobile/`, the history census,
  `runs summary`, `watch` and the plugin's status vocabulary.
- **O2 — the vocabulary's size (§8 D2).** Nineteen values is the design's recommendation on the
  "different act" rule; the owner may rule for fewer, and the report's table follows.
- **O3 — whether the class may carry a *reason* beyond the enum.** This design says no (a closed
  enum, no text, §4.3). If the owner wants the provider's own error class preserved, that is a
  second field and a second decision about what may be stored from model output.
- **O4 — the report's audience.** `runs ls --group-by` reads the installation's own bucket and
  prints nothing to a repository; the digest commits into the repository. Whether the committed
  Failures table may name run ids at all is the owner's call — the table above recommends the
  report's existing practice (it already prints run ids, `internal/digest/render.go:70`), and a run id
  is not task text.
- **O5 — the thresholds (§8 D7)** if the owner has a view on what "high" means.
- **O6 — no class names the spent subscription.** A run stopped by the OAuth account's usage limit
  is written by `stageErrorReason` (`internal/runner/runner.go:3862`) today, and none of the nineteen
  values names it: it is not the provider's `403`, not a stream that ended, so §4.3's classifier
  leaves it in `stage_error_unexplained`. This design adds no class for it — the cause is a limit on
  the *account*, shared with the owner's own use and resetting on its own schedule, rather than
  anything about the task — so whether it earns a value of its own is a ruling on the vocabulary's
  size (O2) and not an omission this design settles.

## 10. Tasks

**Status: a plan, approved to build on 2026-11-07; FC1–FC4 are built and FC5–FC8 are not
started.** Each task is written to fit one run's $5 cap: it names at most twelve files, one command,
one commit message and one red step. The convention is the one the active plans use
(`docs/bitbucket-credentials/implementation-plan.md` is the most recent): **Files**,
**Interfaces**, **Tests**, **Command**, **Depends on**, **Red step**, **Reviewer**, **Commit**, and a
**SEC** flag where the change moves a security boundary. **This work is SECURITY-SENSITIVE: the
classifier reads model output, so FC2 carries an SEC flag and needs an independent review.**

### FC1 — the vocabulary, the field and its schema (D1, D2)

- **Files (6):** the new `failure` package (or `internal/runstore/failure.go` if the owner's rule is
  that a wire vocabulary lives with the record — **the design recommends a package of its own**:
  the runner, the reader and the digest all need it, and `runstore` importing nothing new is the
  property the package graph is built on), `internal/runstore/runstore.go` (`Record.FailureClass`),
  `schemas/result.schema.json`, `internal/cli/runs_test.go`, `schemas/schemas_test.go`,
  `internal/cli/docs_pr_state_test.go` (the pin this design ships, extended in FC1's red step).
- **Interfaces:** `failure.Class` (a string type), `failure.Classes()` (the one slice), and the
  per-class properties (`Kind`, `Retryable`, `NeedsOwner`) read from one table.
- **Tests:** `TestResultSchemaFailureClassesAgreeWithGo` (the schema's enum is `failure.Classes()`,
  the shape `TestResultSchemaStatusesAgreeWithGo` uses), `TestResultSchemaAcceptsRunnerRecords`
  (the fully-populated record carries the field).
- **Command:** `GOFLAGS=-p=2 go test ./internal/runstore ./schemas -run 'FailureClass|Accepts' -count=1`.
- **Depends on:** none.
- **Red step:** write the schema's enum from the Go slice before the slice exists: the test fails
  because the two lists disagree (an empty slice against a written enum).
- **Reviewer:** standard. **Owner-run:** no. **SEC:** no (a field and a vocabulary).
- **Commit:** `feat(runstore): the failure_class vocabulary and the record's field`.

### FC2 — one writer for the terminal status (D3)

- **Files (12, at the plan's twelve-file cap — `recordend.go` is the twelfth, and it is the file the
  owner put `recordEnd` in, D1452):** `internal/runner/recordend.go` (new — the writer),
  `internal/runner/runner.go`
  (`finalize`'s switch, the deferred function and the two bootstrap terminals),
  `internal/runner/gate.go`, `internal/runner/refused.go`, `internal/runner/followup.go`,
  `internal/runner/reviewmode.go`, `internal/runner/artifact.go`, `internal/runner/reviewartifact.go`,
  `internal/runner/shadow.go` (§1's statements — `reviewartifact.go` is the file the first
  draft of this list missed), `internal/runner/failure_class_internal_test.go` — **the internal
  test package, because this is an unexported writer**, and the design recommends that new file over
  extending `record_save_internal_test.go` (an `internal` test file is not one of the
  `docs_*_test.go` files the census counts) — plus the two files this task's own change falsifies:
  `docs/design/failure-classes.md` (§1's "36 statements, in 24 functions across 8 files" is
  rewritten, because after `recordEnd` takes the status no statement assigns one itself) and
  `internal/cli/docs_pr_state_test.go`, **whose census pin this task rewrites**: the pattern it
  counts (`\brec\.Status[^=!]*= runstore\.Status`) matches nothing the tree writes once every writer
  calls `recordEnd`, so `TestDocsFailureClassCensusMatchesTheRunner` reaches its `statements == 0`
  fatal on this task's first run. The pin is rewritten to the shape that survives — no statement
  assigns a `Status` outside `recordEnd` — and its file stays where it is, in an existing
  `docs_pr_state_test.go` rather than a new one, so the docs-pin census it is chosen not to move
  does not move (§11).
- **Interfaces:** `func (r *run) recordEnd(status runstore.Status, outcome runstore.Outcome, class failure.Class, reason string)`.
- **Tests:** `TestRecordEndIsTheOnlyWriterOfATerminalStatus` — the guard has to see **any** statement
  that writes a record's `Status`, `Outcome`, `Reason` or `FailureClass`, not only `r.rec.Status`:
  `storeUnposted` (`internal/runner/followup.go:522`) renders a *copy* of the record through
  `recordend.go`'s `reportCopy`, and a guard written for `r.rec.Status` alone would leave exactly a
  statement like that one — and a statement that writes the class alone, which is the field a report
  counts runs by and which FC4 fills — free to carry an ending past `recordEnd`. So the guard matches
  a selector by any of those four names assigned anywhere in `internal/runner`, and any of the four
  as a key of a `runstore.Record` literal (bar the record's construction, whose status is
  `running`), and the task accounts for the copy explicitly rather than excepting it silently.
  It reads the **parsed** Go code, not the text: `runstore.Record{Status: …}` in a struct
  literal, `r.rec.Status = st` through a variable, or a status written in a helper and assigned
  later all sit past any pattern matched against lines, and only the parsed form can say whether a
  statement *calls* the writer or writes the field itself — the AST is what the rewritten census
  pin counts too. The same reading covers the second way in, which is not a field at all: a
  reference to `writeEnd` anywhere but `recordend.go`, or to `recordEnd` anywhere but the call
  itself, since `w := writeEnd` and `f := r.recordEnd` reach the writer one hop further from its
  name and a guard over the call alone would pass over them. Also
  `TestNoClassIsWrittenWhereTheRunDidNotFail` — the absence pin, named here
  `TestUnclassifiedIsWrittenWhereNoClassIsChosen`: a class on a `succeeded` run, a `mode: review` run
  that reviewed or an artifact run that delivered would be a classification of a success, and the
  half of that name that is true — an unmatched reason reading `unclassified` — is the reader's,
  FC5's (F35). Also `TestNoFailureEndingCallsRecordEndWithoutAClass` (every ending that is not
  `succeeded` passes a class, and one that passes the empty literal is reported by the file and line
  that writes it — the shape `finalize`'s failed case and `endHalted` are held in),
  `TestRecordEndSitesKeepTheClassTheyChose` (the endings whose class their status does not name,
  `endRefused`'s `branch_moved` among them: a mutation there to `infra_error` or `unclassified`
  leaves a class in place, so only the site's own name says which one is right),
  `TestRecordEndHaltAndCancelClasses` (the two endings whose class the site does not choose: a halt
  reads its family from the halt's own reason and a cancellation is `cancelled` and no other, held as
  a shape over every halt and cancel site, so a site that passes a family of its own reddens) and
  `TestHaltedRunCarriesItsHaltsClass` (a failure then a halt ends `halted` with the halt's own class,
  walked over the whole closed `HaltReason` set rather than sampled).
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'RecordEnd|NoClassIsWritten|HaltedRun' -count=1`.
- **Depends on:** FC1.
- **Red step:** `TestRecordEndIsTheOnlyWriterOfATerminalStatus` fails the moment the field is added
  beside the 36 statements — which is the task.
- **Reviewer:** **independent (SEC).** The reviewer asks one question above all: *can a class or a
  status be written anywhere else, and is the last-writer rule the only rule?*
- **Commit:** `feat(runner): one writer for a run's terminal status and class`.

### FC3 — the classes the runner knows structurally (D2)

- **Files (7 — the plan's five and their test files, plus `recordend.go`, which the move takes the
  declaration out of):** `internal/runner/outcome.go` (the four `Decide` refusals),
  `internal/runner/halt.go` (**`haltClass` moves here** — FC2 shipped it in
  `internal/runner/recordend.go`, where its own file cap left it, together with the walk over the
  closed `HaltReason` set, which FC2's `TestHaltedRunCarriesItsHaltsClass` already runs; this task
  moves the one declaration beside `haltReasonText` and declares no second),
  `internal/runner/runner.go`
  (`verify`, `runCommits`, the `config_invalid` refusals), `internal/runner/refused.go`
  (`branch_moved`), `internal/runner/prflow.go` (`git_provider_error`), and their test files.
- **Interfaces:** `failure.ClassForVerify(rec verify.Record, sha string)`, `haltClass(HaltReason)`.
- **Tests:** `TestDecideRefusalsCarryTheirClass`, `TestHaltReasonsMapToTheirFamily`,
  `TestConfigRefusalIsConfigInvalid`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'DecideRefusals|HaltReasons|ConfigRefusal' -count=1`.
- **Depends on:** FC2.
- **Red step:** every class assertion fails against a tree where the class is `unclassified`.
- **Reviewer:** standard. **SEC:** no. **Commit:** `feat(runner): the classes the runner knows from its own state`.

### FC4 — the classifier over the agent's own message (D4, SEC)

- **Files (4):** `internal/runner/runner.go` (`stageFailureClass`, the phrase tables, the
  `res.IsError` case), `internal/runner/stageerror_internal_test.go` (or the existing
  `stageerror_test.go`), and — for the security property — `internal/runner/redaction_test.go`.
- **Interfaces:** `func stageFailureClass(auth string, res agent.Result) failure.Class`.
- **Tests:** `TestStageFailureClassNamesTheAuthRefusal`, `TestStageFailureClassNamesTheStreamFailure`,
  `TestStageFailureClassFallsBackWithoutText`, and `TestStageFailureClassCopiesNoText` (the class,
  printed, contains no byte of the message — the redaction suite's own shape).
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'StageFailureClass' -count=1`.
- **Depends on:** FC2.
- **Red step:** the three tests fail against a classifier that reads no text (one class for all).
- **Reviewer:** **independent (SEC).** The reviewer asks: *is every byte of the message outside the
  record, and does anything act on the class?*
- **Commit:** `feat(runner): classify a stage's own error message, without copying it`.

### FC5 — the legacy reader (D8)

- **Files (4):** `internal/runview/runview.go` (`FailureClass` on the row, `legacyClass`), its test
  file, `internal/runview/launched_by_test.go`'s neighbour for the guard (the row's own
  `TestRowCarriesTheClassTheRecordHolds`), and `internal/cli/runs_test.go`.
- **Interfaces:** `func ClassOf(rec *runstore.Record) failure.Class` — the one function the row and
  the digest both call: the field when it is there, the fixed table when it is not.
- **Tests:** `TestLegacyClassFromReasonTable`, `TestLegacyUnmatchedReasonIsUnclassified`,
  `TestLegacyClassIsNeverWrittenBack` (the record's bytes after a read are unchanged).
- **Command:** `GOFLAGS=-p=2 go test ./internal/runview -run Legacy -count=1`.
- **Depends on:** FC1, FC3.
- **Red step:** the table's own tests fail against a function that returns the class only when the
  field is present.
- **Reviewer:** standard. **SEC:** no. **Commit:** `feat(runview): read a legacy record's class from its reason`.

### FC6 — `runs ls --group-by failure_class` (D6)

- **Files (6):** `internal/cli/runs.go` (the flag, `lsOptions`, `lsDoc.Groups`, the refusals),
  `internal/cli/runview`-facing helpers, `internal/cli/runs_test.go`,
  `internal/cli/runid_test.go` (the default-window guard reads the same constants), a golden under
  `internal/cli/testdata`, and `website/content/cli-reference.md` (**regenerated, not edited**).
- **Interfaces:** the `groups` block of `lsDoc`; `--group-by`'s closed value list.
- **Tests:** `TestRunsGroupByFailureClassNamesEveryClass`, `TestRunsGroupByRefusesAPage`,
  `TestRunsGroupByJSONCarriesTheServlet…` — the shape, not the name: one test per property, named in
  the task's own commit.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'RunsGroupBy' -count=1`.
- **Depends on:** FC5.
- **Red step:** the grouping test fails against a listing with no block.
- **Reviewer:** standard. **SEC:** no. **Commit:** `feat(cli): group runs ls by failure class`.

### FC7 — the Failures table and the two rules (D7)

- **Files (6):** `internal/digest/digest.go` (`Failures`), `internal/digest/render.go`
  (`FailuresTable`, the summary block), `internal/digest/patterns.go` (the two rules and their
  constants), `internal/digest/digest_test.go`, `internal/digest/patterns_test.go`, and
  `internal/digest/testdata` (the golden report).
- **Tests:** `TestDigestFailuresTableIsClosed`, `TestFailureClassHighNeedsItsWeeks`,
  `TestFailureClassNewIgnoresBlocksWithoutTheField`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/digest -count=1`.
- **Depends on:** FC5.
- **Red step:** the two rule tests fail with the four rules alone.
- **Reviewer:** standard. **SEC:** no. **Commit:** `feat(digest): a period's failures by class, and two rules`.

### FC8 — the documents, the contract and the skills (D9)

- **Files (6):** `docs/design/v1.md` (§4.6's field), `console/docs/json-contract.md` (two rows),
  `docs/task-governance/reconciliation.md` (D326's rules) or the periodic-reviews folder's own
  document — the one that owns the rule list, whichever the tree says by then —
  `plugin/skills/working/reference/diagnose.md` (one sentence, only if O1's answer wants a person to
  use the grouping), `internal/cli/docs_desktop_console_test.go` (the contract's pins, if a row
  moves), and `docs/releases/vX.Y.Z.md`'s `### For operators`.
- **Tests:** the pins that already read those documents (`TestDocsDesktopConsoleContractNames…`,
  `TestDocs` over the changed design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1`.
- **Depends on:** FC6, FC7.
- **Red step:** none of its own — the task writes no behaviour; its gate is the Docs run.
- **Reviewer:** standard. **SEC:** no. **Commit:** `docs: the failure class on the record, the contract and the report`.

### 10.1 Edges, levels and the critical path

**Edges** (from each "Depends on"):

- FC1 → FC2, FC1 → FC5
- FC2 → FC3, FC2 → FC4
- FC3 → FC5
- FC5 → FC6, FC5 → FC7
- FC6, FC7 → FC8

**Levels** (a level's tasks can run concurrently: every task sits below all the tasks it depends on,
and no level holds two writers of one file — the three tasks that write
`internal/runner/runner.go` are three levels apart, which is what §10.2's rule costs):

| Level | Tasks |
| --- | --- |
| L0 | FC1 (script) |
| L1 | FC2 |
| L2 | FC3 |
| L3 | FC4, FC5 |
| L4 | FC6, FC7 |
| L5 | FC8 |

**Critical path:** FC1 → FC2 → FC3 → FC5 → FC6 → FC8. FC4 and FC7 hang off it. **The long pole is
FC2**: the 36 statements across eight files become one call, in a package whose suite costs ~25 minutes, which is why
FC2's own test is a guard over the tree and not a fixture per site.

### 10.2 Shared files (no level holds two writers of a file)

| File | Writers (level) |
| --- | --- |
| `internal/runstore/runstore.go`, `schemas/result.schema.json` | FC1 (L0) |
| `internal/runner/runner.go` | FC2 (L1), FC3 (L2), FC4 (L3) — three tasks in three levels, each a different function; the implementing runs land them in order and rebase once |
| `internal/runner/refused.go` | FC2 (L1), FC3 (L2) |
| `internal/runner/recordend.go` | FC2 (L1), FC3 (L2) |
| `internal/runner/gate.go`, `followup.go`, `reviewmode.go`, `artifact.go`, `reviewartifact.go`, `shadow.go`, `failure_class_internal_test.go` | FC2 (L1) |
| `internal/runner/outcome.go`, `halt.go`, `prflow.go` | FC3 (L2) |
| `internal/runner/stageerror_test.go`, `redaction_test.go` | FC4 (L3) |
| `internal/runview/runview.go`, its own test file | FC5 (L3) |
| `internal/cli/runs_test.go` | FC1 (L0), FC5 (L3), FC6 (L4) |
| `internal/cli/docs_pr_state_test.go` | FC1 (L0), FC2 (L1) |
| `docs/design/failure-classes.md` | FC2 (L1) |
| `internal/cli/runs.go`, `runid_test.go`, the `internal/cli/testdata` golden, `website/content/cli-reference.md` | FC6 (L4) |
| `internal/digest/*.go`, `internal/digest/testdata` | FC7 (L4) |
| `docs/design/v1.md`, `console/docs/json-contract.md`, `docs/task-governance/reconciliation.md`, `plugin/skills/working/reference/diagnose.md`, `internal/cli/docs_desktop_console_test.go`, `docs/releases/vX.Y.Z.md` | FC8 (L5) |

Two files have three writers and three have two, and the table's rule is that a level holds at most
one of them: `runner.go` (FC2, FC3, FC4) is spread across L1–L3 and `internal/cli/runs_test.go`
(FC1, FC5, FC6) across L0 and L3–L4, while `refused.go` (FC2, FC3), `recordend.go` (FC2, FC3 —
FC2's declaration and FC3's move of it out) and `internal/cli/docs_pr_state_test.go` (FC1, FC2) are
each spread across two levels, so no two runs write one file at once. An implementing run that lands
two of them at once rebases once, in the order of their levels.

### 10.3 The HARD RULE, and the release

**A field addition needs no operator step.** `failure_class` is additive on `result.json` and on
`runs ls --json`, it publishes no policy node, moves no schema version, changes no image contract and
changes no plugin pin, so **the whole upgrade is `fugaro update`**, the one documented command, and
that command has nothing new to do: an installation at the previous version converges with no
migration, and no release ships an operator step this design does not name. The classes for the runs
between the release and the day the field is written are read from `reason` (§5), so no backfill and
no second command is owed. **The release's `### For operators` is "run `fugaro update`" and nothing
else** ([releases/README.md](../releases/README.md)); the release note's Highlights line says the run
record now says why a run failed, in a closed vocabulary, and the Failures table is what the digest
commits.

## 11. What this design does not do

- **It builds nothing.** No code, no schema, no command, no test-file count: the pin this work ships
  is written into `internal/cli/docs_pr_state_test.go`, an existing file, because the docs-pin census
  counts `internal/cli/docs_*_test.go` files and moving it is a decision of its own
  ([rigor-modes/reconciliation.md](../rigor-modes/reconciliation.md) §4's row, held by
  `TestDocsRigorModesReconciliationCountsTheConfigManagementPin`).
- **It files no backlog item.** `docs/backlog/README.md` reads **2 items: 2 open, 0 closed, 0
  dropped**, and this design leaves it exactly as it is: an open question here is an Open item (§9)
  until the owner rules, and a filed item is what the owner's ruling becomes
  ([backlog/FORMAT.md](../backlog/FORMAT.md)).
- **It changes no status.** §8 D5 is an open item, not a proposal: `failed`, `halted`, `cancelled`,
  `infra_error` keep their meanings and their readers.
- **It does not touch `reason`.** The free-text sentence stays byte for byte what it is, and the
  90-day retention keeps meaning what it means.
- **It does not retry, resume or spend by class.** Nothing in the runner branches on the class; the
  report is the only reader, and no class may ever become an admission or a launch.
- **It does not score runs by class, or cost, in a dashboard.** The console work this implies (a
  column, a chip) is named in §6.3 and left to its own task.
