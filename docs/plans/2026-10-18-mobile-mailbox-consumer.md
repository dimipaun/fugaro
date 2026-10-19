# `fugaro mailbox` — the mobile consumer: implementation plan (mobile P4.1)

**Status: a plan. Nothing here is approved, nothing here is built, and no code exists.**
This document is the **CLI plan console D19 requires** before Task **P4.1** of
[mobile/implementation-plan.md](../mobile/implementation-plan.md) may be built, and it is the only
deliverable of this change: it writes down what `fugaro mailbox` will be, so the owner can approve
or refuse it. It implements nothing and it changes no command that exists.

Three things it deliberately does **not** claim:

- **Task P4.1 has no approval.** [mobile/implementation-plan.md](../mobile/implementation-plan.md)
  step 6 of its approval list is "Approve P4.1 separately" and that approval has not been given.
  Answering D113c (which fixed the *shape* — a new `fugaro` command — and the *name* —
  `fugaro mailbox`) approves no task.
- **Phase 6 is proposed and NOT APPROVED**, and this command's MAC is Phase 6's — **in both
  directions**. D169's per-pairing MAC is built by **P6.3** (the `mac` field, its canonical
  serialisation and the heartbeat's), required on every message by **P6.4**, and used here **twice**:
  verifying every inbox message on the way in (§3.5's "the **phone** signs every inbox message") and
  signing every outbox reply and the heartbeat on the way out (§3.5's "the **consumer** signs every
  outbox message and the heartbeat"). None of it exists in the merged schema. **Ruling M4** says what
  the command does until the owner approves Phase 6 — it refuses in both directions, and fails at
  startup rather than looping — and it is the first owner decision below.
- **`shared/`'s tests have no CI job.** P4.3 is owner-made (no agent edits
  `.github/workflows/`, Global Constraint 8), so until the owner adds it nothing runs the shared
  package's tests in CI. That is why every verification in this plan is a **Go** verification, and
  why the parity corpus (Ruling M9) is data rather than a Node subprocess.

Related: [mobile/implementation-plan.md](../mobile/implementation-plan.md) (P4.1 — this plan's
authority), [mobile/reconciliation.md](../mobile/reconciliation.md) §3 (D110–D129) and §4 (the
resolved tensions), [mobile/functional-spec-reconciliation.md](../mobile/functional-spec-reconciliation.md)
§3.1, §3.3, §3.5, §3.6 and §4 (the D128 amendment, D160–D173, the MAC and ledger spec, the
windows), [design/v1.md](../design/v1.md) §9.1 (the command table, the `--json` rule, the exit
codes) and [design/voice.md](../design/voice.md) (F38: no argv table fences what the consumer may
run).

---

## 1. What is built, and what it reuses

**Built:** one command, `fugaro mailbox`, a long-lived local process the operator runs beside the
orchestrator host. It holds the mailbox's **consumer** side: it takes a lease, reads the account's
inbox, verifies each message's pairing MAC, decides which deliveries are new, acts on them, and
**signs and writes** its answers to the outbox — and signs the heartbeat it renews. Both directions
of the wire are MAC-covered, and both are this command's (M4); D113c's answer of 2026-10-17 is option
**(ii)** — a new `fugaro` command, not a module inside the voice helper — and the owner fixed the
name on 2026-10-17.

**Reused, never forked.** Phase 2's rules and Phase 1's shared modules are the substrate. This
plan adds no rule of its own about what a message is, what a duplicate is, what a lease is, or what
a window is; where the Go side needs one of those answers it **binds** the module that owns it.

| What it needs | Where it already is (verified on `origin/main`) | How P4.1 uses it |
| --- | --- | --- |
| The message schema, and the ack field | `shared/mailbox-schema/index.js` — `FIELDS` (`['id','sender','text','at','acked']`), `REQUIRED_FIELDS`, `ID_MAX`, `SENDER_MAX`, `TEXT_MAX`, `validateMessage` | The parity corpus's schema vectors; the Go reader refuses a record `validateMessage` refuses |
| The mailbox state machine: new / duplicate / late / replay, the act step, the ledger cap | `shared/mailbox/index.js` — `initialState`, `step`, `STATE`, `EVENT`, `KIND`, `STATUS`, `OUTCOME`, `REFUSAL`, `INPUT_REFUSALS`, `TRANSITIONS`, `DEFAULT_WINDOW_MS` (6 h), `MIN_WINDOW_MS`, `MAX_WINDOW_MS` (7 d), `MAX_PROMPT_TTL_MS` (30 d), `DEFAULT_MAX_SEEN` (1024), `SENDER_SHARE`, `defaultMaxPerSender`, `DEFAULT_FUTURE_TOLERANCE_MS` | Ruling M9: the corpus is generated from this module and replayed by the Go tests |
| The consumer lease | `shared/lease/index.js` — `CLAIM_TTL` (10 min), `MAX_CLOCK_SKEW` (5 min), `MAX_TTL` (24 h), `MAX_HOLDER_LENGTH` (256), `LEASE_CODES`, `validateLease`, `emptyLeaseState`, `takeLease`, `readLease`, `releaseLease` | Ruling M1: the lease's **rule** is bound; the **store** is the heartbeat node |
| The compare-and-swap duty | `shared/lease/README.md` ("two consumers that both read the same state and both get `ok: true` from `takeLease` must not both be able to write"; CAS is the store's fence) | Ruling M1: an RTDB ETag conditional write is that fence |
| The conditional write itself | `internal/rtdb/client.go` — `GetETag` (it sends `X-Firebase-ETag` and returns the tag), `PutIfMatch`, `ErrPrecondition` (a 412 answer) | The fence M1 rests on already exists in this repository's client |
| Presence and the heartbeat's `boot` | `shared/presence/index.js` — `evaluatePresence`, `STATE`, `REASON`, `REFUSAL`, `DEFAULT_INTERVAL_MS` (60 s), `DEFAULT_MISSED_INTERVALS`, `DEFAULT_OFFLINE_INTERVALS`, `MAX_BOOT_ID_LENGTH` (64) | The heartbeat this command writes is the value `evaluatePresence` reads |
| The redactor (D118 requires it before anything is written) | `shared/digest/index.js` — `redact`, `REDACTED`, `redactFunc`, `redactForms`, `digest`, `SEVERITY`, `moreSevere`, `mostSevere` | Every field this command writes to its own state or prints passes it first |
| The simulator, the fault model and the canonical schedule line | `shared/mailbox-sim/index.js` — `createSimulator`, `scheduleFromSeed`, `faultFor`, `scheduleText`, `parseScheduleText`, `replyId`; its README §"The fault model, which is data", §"What the actors do" | The corpus's scenarios are its schedules; a failing Go case prints `scheduleText()` |
| The mailbox subtree, and who may write it | `internal/budget/rules/rules.json.tmpl` lines 193–230 (merged: `134db49`, "mobile P2.1 + P2.2 … (#532)"), and the macros `MAILBOX` / `CONSUMER` in `internal/budget/rules/rules.go` (lines 383 and 390) | §2 below. Its writes go through the `CONSUMER` credential (§2's C0), so the grants are a **store-level fence** on them — and no task mints that credential (decision 8) |
| The rules' emulator proof | `internal/budget/rules/emulator_mailbox_test.go` | The template's fence is already proven; this command does not re-prove it |
| A long-lived consumer's local state | `internal/watch/acks.go` — `AckPath`, `LoadAcks`, tmp+rename, `0o600` | The ledger and the pending-prompt file are written the same way (Ruling M7) |
| A lease-shaped claim, and its TTL | `internal/runstore/launch.go` — `Store.Claim`, `ClaimTTL` (10 min) | Not used directly: the launch claim is the runs bucket's. Cited as the shape this command's lease follows |
| A sweeper, its report and its user-key convention | `internal/budget/sweep.go` — `Sweeper`, `Sweep`, `SweepReport`, `RunUID` (`r~…`) | Ruling M8: the hidden `mailbox sweep` is written in this shape (`m~…`) |
| An owner-run hidden command | `internal/cli/history.go` — `newBudgetHistoryCmd` (`Hidden: true`) | The precedent for hiding the sweeper |
| Executing a cancel | `internal/cli/cancel.go` — `cancelRun` | The one existing destructive path the refusal fence (Ruling M6) must gate |
| A kill switch write, and a **conditional write on an ETag with a bounded retry** | `internal/budget/kill.go` — `SetKill` and its `SetAttempts` bound: "The write is conditional on the ETag read; a lost race is re-read up to `SetAttempts` times" | The other destructive path — and the in-repo precedent for Ruling M1's compare-and-swap |
| Spawning `gh` for a merge | `internal/watch/prstate.go` — `(*PRState).ask`: `procgroup.Run`, argv only, bounded buffers, nothing logged | The third, and the only precedent for "never through a shell" |
| The operator's credential (the **inbox read**), and the CLI's rule that it holds no credential of its own | `internal/cli/cloud.go` — `google.DefaultTokenSource`, `userErr`; `internal/rtdb`; `internal/gcpfake` | The database client; the read credential (§2's C0); and the fake (identitytoolkit) the tests mint the `CONSUMER` token against |
| The project and its config block | `internal/localcfg` — the `watch:` block precedent; `internal/cli/project.go`'s `selectProject`, `internal/cli/cloud.go`'s `budgetDBFor`, `internal/cli/budget.go`'s `checkDBProject` and `dbErr` | Where the keychain **item name** lives (Ruling M5) |
| The generated CLI reference | `internal/clidoc/cliref.go` — `writeCLIReference`, `defaultPath` (`website/content/cli-reference.md`); the drift tests `internal/clidoc/cliref_test.go` `TestCliReferenceIsCurrent` and `internal/cli/docs_website_cliref_test.go` | §4: the one `root.AddCommand` line requires regenerating it |
| The exit-code contract and the command table | `internal/cli/root.go` — the `ExitOK` / `ExitUserError` / `ExitRemoteError` constants, `ExitError` and `ExitCode` (which maps any other error to `ExitUserError`), and `NewRootCmd`; `docs/design/v1.md` §9.1 | §3, and the classification §4.4 rests on: `dbErr` and `checkDBProject` in `internal/cli/budget.go` |

**No existing behaviour changes.** The only Go file that changes is `internal/cli/root.go`, by
**one line**. Everything else is new files.

---

## 2. What the merged rules already decide, and the three things they force

Read from the template as it is on `main` (`internal/budget/rules/rules.json.tmpl` lines 193–230,
macro definitions at `internal/budget/rules/rules.go` 383 and 390). This is not a proposal; it is
what is merged, and P4.1 may not contradict it.

```jsonc
"mailbox": { "$mb": {
  "inbox": { "$msg": {
      ".write": "MAILBOX && newData.exists() && !data.exists()",   // the phone creates; create-only
      ".validate": "newData.hasChildren(['id','sender','text','at']) && $msg.length <= MaxKeyLength",
      "id"|"sender"|"text"|"at": …, "$other": { ".validate": false }
  }},
  "outbox": {
    ".read": "MAILBOX",                                            // the phone reads its own answers
    "$msg": {
      ".write": "CONSUMER && newData.exists() && !data.exists()",  // the consumer creates; create-only
      ".validate": "newData.hasChildren(['id','sender','text','at']) && …",
      "acked": { ".write": "MAILBOX && newData.exists() && !data.exists()",
                 ".validate": "newData.isBoolean() && newData.val() == true && data.parent().exists()" },
      "$other": { ".validate": false }
    }
  },
  "heartbeat": {
    ".read": "MAILBOX",
    ".write": "CONSUMER && newData.exists()",                      // the ONLY replaceable node the consumer holds
    ".validate": "newData.hasChildren(['at','boot'])",
    "at": { ".validate": "TIMESTAMP" }, "boot": { ".validate": "STR" }, "$other": { ".validate": false }
  }
}}
```

`MAILBOX` = `(auth != null && auth.token.mu != null && auth.token.mu == auth.uid) && $mb == auth.uid`
(the phone, carrying `mu`); `CONSUMER` = `(auth != null && auth.token.mc != null && auth.token.mc == $mb)`
(a token whose `mc` names the mailbox it serves). **`CONSUMER` may write the outbox and the
heartbeat and nothing else — in particular it may read no mailbox at all** (`CONSUMER` is a write
claim only: no `.read` in the subtree names it).

### C0 — Two credentials, named, because the whole argument below is an argument about grants

A grant analysis binds only the credential that the grant is evaluated for. So this plan names both
writers explicitly, and it names them **before** the three consequences because C1–C3 are statements
about what `CONSUMER` admits:

| The read or write | The credential | Why that one |
| --- | --- | --- |
| **Read** `mailbox/$mb/inbox` | the **operator's application-default credential** (`internal/cli/cloud.go`'s `google.DefaultTokenSource` feeding `internal/rtdb`) — an IAM read that **bypasses the rules** | The inbox has **no `.read` grant at all** (C1): neither `MAILBOX` nor `CONSUMER` may read it. The consumer's read is out of band, and it is the only credential the CLI already holds |
| **Write** `mailbox/$mb/outbox/$msg` and `mailbox/$mb/heartbeat` | a **`CONSUMER`-claimed credential** — a token carrying `mc == $mb` | It is the only credential the merged grants admit for those paths, and using it is what makes the grants a **store-level fence** on this command rather than a comment |

**The second row is a dependency this plan does not get to assume, and it is a finding:** no task in
the mobile plan mints a token carrying `mc`. P6.6 mints the **pairing** token (`p~<random>`) and the
**device** token (`m~<random>`, `mu = uid` — the phone's); P6.4 writes the device node and the
pairing nodes; the rules' own comment says `CONSUMER`'s `mc` "is minted the same way", by whoever
holds the launcher's token-minter role, and **names no task that does it**. So today the consumer has
**no write credential**, and the two honest options are owner decision 8: mint one (a new task, or an
addition to P6.6, which is written in its own CLI plan), or accept that the writes go through the
operator's ADC as well — in which case **the merged `CONSUMER` grant, and its create-only rule, do
not bind this command at all**, and M10's write-set test is the *only* fence. This plan does not
paper over the difference: C1, C2 and C3 below are true of a `CONSUMER` writer, and the third row's
grant is why M1 puts the lease on the heartbeat. Where the plan's tests need a writer they use the
**identitytoolkit fake with a minted `mc` token** (`internal/gcpfake`), which is the credential the
production path is designed around.

Three consequences follow, and they are the spine of this plan:

**C1 — The inbox is write-only, and has no read grant at all.** So the consumer's "ack of an inbox
message" **cannot be an inbox write**. There is no node to write and no grant that would admit it.

**C2 — The `acked` flag on the outbox is the *phone's* write**, one field on a message the consumer
already created, written when the phone has *spoken* the answer. It is the phone's receipt, not the
consumer's acknowledgement. **The consumer's record that it consumed an inbox delivery is the
ledger**, which is the consumer's own state (Ruling M7), and the observable signal to the phone is
the **outbox reply**.

**C3 — The heartbeat is the consumer's only mutable node, and its two fields are a lease.**
`{at, boot}` is precisely a holder identity (`boot`, a string, and `shared/presence` caps a boot id
at 64 characters) plus a timestamp, with `$other: false` forbidding a third child. The consumer
cannot store a lease anywhere else without a rules change.

**This correction matters, and it is why this plan exists.** The merged simulator models the ack as
**one write by the consumer into the inbox**, and says so in its own README (§"What the actors do":
"the ack being **one** write by the consumer … D113's phone-written ack is P3.6's, and this
simulator does not model two ack writers"). The merged rules make that write **impossible**. Both
are on `main`; they cannot both be the store's truth. Owner decision 2 below.

---

## 3. Rulings (settled here; each says what it costs if wrong)

### M1 — The lease **is** the heartbeat, taken by an ETag compare-and-swap. No fourth node.

The consumer takes the lease by reading `mailbox/$mb/heartbeat` with `internal/rtdb`'s `GetETag`
and writing `{at, boot}` back with `PutIfMatch`; a returned `ErrPrecondition` (the 412 answer) is a
lost race, and it is retried a **bounded** number of times, never forced —
the pattern `internal/budget/kill.go`'s `SetKill` already uses ("the write is conditional on the
ETag read; a lost race is re-read up to `SetAttempts` times"). `boot` is the holder identity; the lease's TTL is
`shared/lease`'s `CLAIM_TTL` (10 min) and its skew tolerance is `MAX_CLOCK_SKEW` (5 min), applied
by `readLease`/`takeLease` — the module decides, this command only supplies the value and the
store.

Why not a separate `mailbox/$mb/lease` node: (a) it needs a **rules change** — the merged subtree
has no `lease` path and no grant reaches one, so it would be a Phase 2 re-open, not a Phase 4 task;
(b) it is a **third write**, and D113b fences the consumer to two (the outbox and the heartbeat);
(c) the heartbeat must be written anyway to show presence, so a second node would let the two
disagree about who holds the mailbox. **Cost if wrong:** at-most-once acting rests on the ETag CAS
alone, so a store that does not honour the precondition lets two consumers act.
`internal/rtdb`'s client is written for it and `internal/budget/kill.go`'s `SetKill` relies on it in
production — that is the evidence the mechanism works; a **live** project's answer is check 83's
neighbourhood and the owner's, and no test here can prove it. The test in T3 is the fence in this
repository: two consumers read one empty heartbeat, both get `ok: true` from `takeLease`, and the
second commit is refused `ErrPrecondition` with **nothing written**.

### M2 — The consumer's "ack" of an inbox message is its outbox reply plus the ledger, not an inbox write.

Per C1 and C2 there is no inbox ack for the consumer to write. So the loop is: deliver → decide →
**commit the ledger** → act → **write the reply to the outbox**; and the phone's `acked` on that
outbox message is what retires it. A message the ledger holds as `acted` but which was **not
performed** (a stale prompt) is still answered with an outbox reply saying so — the ledger cannot
tell "performed" from "consumed", so the reply obligation lives with the consumer that performed,
which is what the simulator's README already states. **Cost if wrong:** if the owner rules that the
consumer must write an inbox ack, the rules must be changed first (an inbox `acked` child and a
`CONSUMER` grant on it), which is a Phase 2 task and not this one. Owner decision 2.

### M3 — The 72-hour prompt TTL is **this command's constant**, not the module's.

`shared/mailbox` requires `promptTtlMs` and validates it in `MIN_WINDOW_MS .. MAX_PROMPT_TTL_MS`
(1 s .. 30 d), and says in its own comment that it carries no number because D118 states a TTL and
no figure: "`promptTtlMs` is a **required** state field, handed in by the consumer (P4.1) from the
TTL its sweeper enforces". The owner's number is **72 hours** (O5 answered). So
`internal/cli/mailbox.go` declares `const mailboxPromptTTL = 72 * time.Hour`, hands it to the
ledger, enforces it in the sweeper, and a test pins that the ledger's refusal code changes from
neither-accepting to `expired` exactly at 72 h — not at 30 d (the module's ceiling) and not at 6 h
(the module's default window, which is a *command* window and a different rule). **Cost if wrong:**
prompts live 30 days or 6 hours and the owner's number is not the one enforced.

### M4 — The MAC covers **both directions of the wire**: every inbox message is verified **before** it reaches the ledger, every outbox message and the heartbeat are **signed before they are written**, and until P6.3 is approved the command **fails closed in both**.

The requirement is two-sided in the plan's own words, and this plan reads both halves of the
sentence. `mobile/implementation-plan.md`'s P4.1 verification says: **"every message it puts in the
outbox carries the pairing MAC over its id, text and timestamp, with the key read from the OS
keychain by the item name its settings hold and never logged"** (D113b, option (a), confirmed
2026-10-17) — that is the **outbound** half — and
[functional-spec-reconciliation.md](../mobile/functional-spec-reconciliation.md) §3.5 states it as a
rule of the protocol: **"Who signs: the **phone** signs every inbox message it writes (P3.6 …); the
**consumer** signs every outbox message and the heartbeat"** (line 192). P6.7 lists the same duty as
its own extension of this command ("signs every outbox message and the heartbeat"), and P6.3 makes
`mac` **required on every outbox message and on every inbox message of every kind**, with
`canonicalBytes(message)`, and gives the heartbeat an `agents` list and a `mac` (O7).

So P4.1 plans **two seams, one function**, and both are SECURITY-SENSITIVE:

- **M4a — inbound, before delivery.** Every inbox message is MAC-verified **before** it is handed to
  the ledger. A message that does not verify is refused, recorded by code, and never acted on, and
  it consumes no `(sender, id)` slot.
- **M4b — outbound, before the write.** Every outbox reply and every heartbeat write **carries a
  MAC** over the same canonicalisation, with the same key. A consumer that cannot sign **writes
  nothing**: it does not write an unsigned reply and it does not write an unsigned heartbeat, so it
  also does not take the lease (M1's write *is* the heartbeat). The write is refused with a code and
  reported, and the reply is owed again on the next pass — a reply the ledger has not retired is a
  reply still owed (M2), so the refusal is recoverable rather than a lost answer.

**What does not exist yet, and why both halves fail closed.** The merged schema has **no `mac`
field** (`shared/mailbox-schema`'s `FIELDS` is `['id','sender','text','at','acked']`), **no
`canonicalBytes`** and no `agents`, because all of them are P6.3's and P6.3 is NOT APPROVED; the
merged rules' `.validate` lists do not admit a `mac` child, because that is P6.4's (§2's quoted
subtree is verbatim). The rules' own comment says as much — "a per-pairing MAC on every inbox
message and action that `fugaro mailbox` verifies before acting (D169, D171, P6.3, P6.7)" — and P6.4
is where `.validate` starts requiring it. So both seams are built complete, wired at the two places
that matter (the delivery path and the write path), and **their behaviour until Phase 6 is a
refusal**, in both directions, symmetric and total.

**One function, so the two halves cannot drift.** The canonicalisation and the MAC are computed by
**one** function (`internal/mailbox/mac.go`), used by `verify` on the way in and `sign` on the way
out, held to `shared/mailbox-schema`'s `canonicalBytes` **and its vectors file** by the corpus
(Ruling M9). A mutation that changes what is covered, or that signs with a different key than it
verifies with, must fail a test in **each** direction — T2's Red step for the inbound half and T2a's
for the outbound.

**Cost if wrong:** the command is inert until Phase 6 lands, which is the point — and the cost is
now paid twice, because P6.7 will find both seams filled rather than one. The alternative for the
outbound half (write replies unsigned "until P6.7") is a phone reading an outbox message it cannot
authenticate, which is the same class of exposure as decision 1's; the alternative for the inbound
half (act on unverified messages) is a phone-shaped write making the orchestrator act with no
authentication at all, which is D128's whole subject. **Owner decision 1 covers both halves, and
should be answered for both.**

### M5 — The pairing key comes from the OS keychain **by item name**, and neither the key nor the item name is ever printed.

D113b's five narrow clauses, plus the run's own rule. Concretely:

- The **item name** is configuration, in the project's local config block (`internal/localcfg`,
  the `watch:` block precedent), e.g. `mailbox.keychain_item`. It has a **fixed shape**: a
  string of at most 64 characters from `[A-Za-z0-9._-]`, with the project name in it, so a config
  file cannot name an arbitrary keychain query.
- The **key itself** is read at run time from the OS keychain through a new, narrow package
  (`internal/mailbox/keychain`, or the platform-appropriate location if the owner prefers a
  different one — owner decision 5), and it is **read once per pairing**, held in memory for the
  life of the process, and never written to disk, never passed in argv, never put in the JSON, and
  never logged — including in the error paths, where the message names the **item name** and the
  failure, never the value.
- **The item name is not a secret and may be printed**; the key is not and never is. The tests
  assert both directions, and the CLI's existing redaction (`internal/safetext`,
  `shared/digest`'s `redact`) is applied to anything this command prints.
- **No API key, token, certificate or password is ever read or written by an agent working on this
  plan**, and the tests use a fake keychain. A test that needs a real keychain is not written.

**Cost if wrong:** a leaked key is a forged action on the operator's behalf. There is no larger
cost in this plan.

### M6 — D128 is enforced as a **table of refusals in code**, not as a rule in prose.

D128's recommendation, and its 2026-10-17 amendment, in one place: **a voice-originated message may
never cause a destructive command.** The three destructive things the consumer's orchestrator can
do are the ones F38 records as unfenced — `fugaro cancel RUN` (`internal/cli/cancel.go`'s
`cancelRun`), `fugaro budget kill` (`internal/budget/kill.go`'s `SetKill`) and `gh pr merge`
(`internal/watch/prstate.go`'s argv-only spawn) — and the consumer refuses **every** command it is
asked to run that is not in its own allow-list. The refusal is the default: an action whose kind
the command does not recognise is refused, not run.

The **2026-10-17 amendment** — cancel, approve/merge and confirming an action **allowed**, on the
phone, with a spoken read-back and an explicit "confirm" — is honoured as an allowance **only
through P6.2's confirmation record**: the destructive action runs only when the consumer holds a
confirmation record (P6.2's, D169's "typed action records executed by the consumer") that names
the same action id and was itself MAC-verified. **Absent that record, the refusal stands.** P6.2 is
NOT APPROVED and P6.2's record does not exist, so **today the refusal is unconditional**, and that
is what the tests pin — including a mutation that removes the allow-list and must turn a refusal
test red. Owner decision 3.

### M7 — Dedupe is by `(sender, id)`, in the ledger, across restarts, and it is bounded.

`shared/mailbox`'s ledger is the dedupe rule (P1.1: the dedupe and replay rule is itself
SECURITY-SENSITIVE). The command persists it at
`$XDG_STATE_HOME/fugaro/mailbox/<project>/ledger.json`, written **tmp+rename at `0o600`**, so a
partial write is never read and another user cannot read it. What it takes from
`internal/watch/acks.go` is exactly two things — the `$XDG_STATE_HOME/fugaro` **prefix** and the
**write discipline** (create a temp file in the destination directory, `0o600`, rename over) — and
no more: `internal/watch/acks.go`'s `AckPath` is a **single file**,
`filepath.Join(base, "fugaro", "watch-acks.json")`, with **no per-project directory component**.
The `<project>` level, the JSON document's shape and its contents are **new here**, and the plan says
so rather than borrowing the precedent for something it does not do. The cap is `DEFAULT_MAX_SEEN`
(1024) with
`defaultMaxPerSender`'s share rule; a full ledger is refused `dedupe-full` rather than grown, which
is the module's own rule.

**A message is deduped by its id even after a restart, and the ledger's ack is what stops the
replay.** The pending-prompt file (prompts the consumer is waiting to see confirmed) is a **separate
file in the same directory**, written the same way, because a prompt that is waiting is not a
delivery that was consumed. **Cost if wrong:** a restarted consumer re-performs an action the
operator's phone already asked for. The test in T4 kills the process mid-loop and restarts it.

### M8 — The command surface: one visible command, one hidden sweeper, `--json` everywhere it reports.

- `fugaro mailbox` — visible, long-lived, in the CLI reference. It selects the project and prints
  `project: <name> (GCP <id>)` first, like every cloud command (design §9.1).
- `fugaro mailbox sweep` — **hidden**, in the shape of `internal/budget/sweep.go` and the hidden
  `fugaro budget history` (`internal/cli/history.go`); it is the D118 TTL's enforcer and the
  presence verdict's writer, and its user keys are `m~…` (the runs bucket's are `r~…`,
  `internal/budget/sweep.go`'s `RunUID`). Hidden because it is internal — and because a hidden
  command is not user documentation, which is exactly what `internal/clidoc`'s skip rule is for.

### M9 — Parity is proven by a **corpus of vectors**, in data, replayable with no Node.

`shared/`'s semantics are JavaScript and this runner (and CI's `test` job) has **no Node**; the
`rules` job has Node but runs the rules, not this package, and there is no `shared/` job at all
(P4.3, owner-made). So the JS side and the Go side are held together by a corpus **file**, not by a
process:

1. A JS generator (a test in `shared/mailbox/`, run by the owner or by the future CI job) writes
   `internal/mailbox/testdata/parity.json`: for each scenario, the **input events**, the **state**
   the module produced, and the **outcomes** and **refusal codes** it gave. It is generated from
   `shared/mailbox` itself, so it cannot drift from the module by being hand-written.
2. A Go test replays the same inputs through the Go binding and compares **outcomes and refusal
   codes**, plus the corpus's own hash, so a hand-edited corpus is a failing test rather than a
   passing one.
3. The refusal codes are compared by their **own spelling** (`invalid-prompt-ttl`, `future-timestamp`,
   `replay`, `expired`, `dedupe-full`, `stale-pending`, `cas-conflict`, `bad-ttl`, …), never
   renamed: the modules' READMEs both insist a refusal travels with the module's code.
4. A simulator-produced failing case prints `scheduleText()` and the seed, so a failure is
   replayable by hand.

**The Go binding's rule, stated so it cannot drift:** every value the two sides must agree on (the
state machine's outcomes and codes; the ledger's cap and share rule; the windows; the TTL
validation) is compared against the corpus, and the Go code holds **no constant those modules
already own** — it holds only the constants that are the *command's* (72 h, the item name's shape,
the allow-list). **Cost if wrong:** the Go side and the phone's side disagree about what a duplicate
is, and the disagreement is found in production. Owner decision 4.

### M10 — What the consumer never writes.

One write to the outbox per answer (create-only, per the merged grant), one write to the heartbeat
per lease renewal, and its own state files. **It never writes the inbox** (C1), **never writes the
`acked` flag** (C2 — that is the phone's), never writes the runs bucket's `agents` subtree, and
never writes any path the `CONSUMER` claim does not admit. A test asserts the write set, and a
mutation that adds an inbox write must fail it.

### M11 — Nothing destructive is ever spawned through a shell, and the refusal happens before the spawn.

Every subprocess this command runs is `internal/procgroup.Run` with a fixed argv, exactly as
`internal/watch/prstate.go`'s `ask` does: no shell, no interpolation of any message text into an
argv element, bounded output buffers, and a process-group kill. A message's text is **data**: it
names an action id and nothing else. The refusal (M6) is evaluated **before** any spawn, so a
refused action never reaches the process table at all.

### M12 — `--once` exists so this is testable and scriptable, and the exit codes are the CLI's own.

`--once` performs at most one pass and exits; without it the command loops until interrupted, which
is how it is run beside the orchestrator. It supports `--json` (design §9.1's rule: a command that
reports supports `--json`), and the exit codes are `internal/cli/root.go`'s (`ExitOK`,
`ExitUserError`, `ExitRemoteError`).

**The contract, stated once and unambiguously, because a refusal is two different things.** The word
covers (a) a *per-message* refusal — this delivery is a duplicate, a replay, expired, late, or
badly signed — which is a **recorded outcome of a pass**, and (b) a *run-level* refusal — the
command cannot do its job at all — which is a **failure to start or to continue**. They are not the
same and they do not share an exit code:

| Exit | When | Not |
| --- | --- | --- |
| **0** | a pass completed, whatever it managed to read and whatever it refused: an **empty inbox**, a pass that **held no lease** (and so read nothing, §7), and a pass whose every message was refused as a duplicate, a replay, expired, `stale-pending` or badly signed. Refusals are outcomes and are reported in `refused[]` and in the `pass` counters | a per-message refusal is **never** an exit 1, and **neither is reading no message at all** — see the predicate below, which requires at least one |
| **1** | **a run-level refusal**, all of it before or outside the pass loop: a bad flag (`--interval` below 1 s, a negative duration, an unknown project selector); no project selectable; no mailbox configured; a keychain item name absent or not of M5's shape; **the M4/§8.3 state — MAC verification or signing unavailable — which fails at startup** rather than looping over a mailbox it can do nothing with; the database or keychain **refusing** — `dbErr`'s `rtdb.ErrPermission` branch (→ `adminDenied` for a write, a `userErr` for a read) and `checkDBProject`'s not-this-project's-database, which is a `userErr` too. **And** the one pass-level case: the predicate below | a remote failure — an outage, a transport error, anything `dbErr` sends to `remote(err)` — is 2, not 1 |
| **2** | what `dbErr` sends to `remote(err)`: the database could not be **reached or written** (an outage, a transport error, a 5xx), and nothing else; plus the new keychain package's own unopenable-backend case, classified the same way (that package is T2's, and this row is the mapping it must implement) | a permission refusal and a foreign database are **1**, not 2 — `dbErr` maps the first to `userErr` and `checkDBProject` raises the second as a `userErr` |

**The one place a D128 action refusal meets the exit code.** A refused action is first a **recorded
outcome**: the pass completes, `refused[]` carries its code, and the exit is **0** — a phone asking
for a cancel is not the operator's failure. It becomes **1** only when the operator asked for that
one thing and nothing else could happen: `fugaro mailbox --once` on a mailbox whose only pending
message is a destructive one that is refused, where the pass therefore did nothing at all.

**That rule is one predicate in the code**, so there is exactly one branch and no third reading.
Exit **1** iff **all four** hold:

1. the pass **read at least one message**;
2. it **performed no action** — nothing delivered, nothing replied, nothing swept;
3. **every message it read was refused**; and
4. **at least one of those refusals is an action refusal** — D128's fence — and not only per-message
   refusals.

Clause 1 exists because the other three are **vacuously true of a pass that read nothing**: a pass
that held no lease never reaches the inbox (§7), and an empty mailbox has nothing to read, so "every
message it read was refused" holds of both with **zero** messages read and zero refused. Neither is a
refusal the operator suffered, and both are **0** — which is what the 0 row above says, and what a
predicate without clause 1 would have contradicted. Clause 4 exists because the same is true of an
**ordinary** refusal: a `--once` pass that read three duplicates and refused all three read one or
more, refused everything it read and acted on nothing, and is still **0** — none of those refusals was
a refused action. Clauses 3 and 4 together are what "the refusals were the pass's whole purpose"
means, stated as code. T5 tests both sides of **every** row above and each clause's Red step; T6
asserts the code for a refused action in a pass that also delivered something (0), in a pass that
delivered nothing (1), and in a pass whose only readings were per-message refusals (0).

---

## 4. The command surface

### 4.1 `fugaro mailbox`

```
fugaro mailbox [--once] [--interval D] [--json] [--no-heartbeat] [--project NAME | --gcp-project ID]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--once` | bool | `false` | run at most one pass and exit (the scriptable and testable mode) |
| `--interval` | duration | `5s` | how long to wait between passes; never less than 1 s (exit 1 below that) |
| `--json` | bool | `false` | one JSON document per pass instead of lines |
| `--no-heartbeat` | bool | `false` | do not write the heartbeat (diagnosis: the lease is then not taken) |
| `--project`, `--gcp-project` | string | `""` | the existing project selectors, `addCloudFlags`'s, unchanged |

`Args: cobra.NoArgs`. It prints `project: <name> (GCP <id>)` first, as every cloud command does,
then one line per pass naming what it did — the lease (held / taken over / lost), the messages
delivered, the messages refused and their refusal **code** (never their text), the actions
performed, and the replies written. `--json` carries the same as one document per pass with a
`project` field (design §9.1).

**Long text** states, in this order: what the command is; that it acts on the operator's behalf and
that a voice-originated message may never cause a destructive command (D128), with the amendment's
allowance named and its P6.2 dependency named; that **both directions of the wire are MAC-covered** —
every message is verified before it is acted on and an unverifiable one is refused and never acted
on, and every reply and heartbeat is signed before it is written, so a command that cannot sign
writes nothing (M4); that the pairing key is read from the OS keychain by item name and is never
printed; that the command writes only the outbox and the heartbeat, and with which credential
(C0); and where its own state is.

### 4.2 `fugaro mailbox sweep` (hidden)

```
fugaro mailbox sweep [--json] [--dry-run]
```

The D118 TTL's enforcer, written in `internal/budget/sweep.go`'s shape: it walks the mailboxes it is
configured for, expires waiting prompts past 72 h (M3), and **reports** the presence verdict —
computing `shared/presence`'s `evaluatePresence` over the heartbeat and printing it, **writing
nothing**. That is not an omission: `evaluatePresence` is a **pure function over a heartbeat**, no
node is designated to hold its output, the merged template has **no grant reaching any presence
path**, M1 rules out a fourth node, and §6 therefore lists the verdict as computed and never written.
The verdict in the report is for the operator's eyes and for a later Phase 6 task to place, if the
owner ever wants it stored. `--dry-run` prints the plan and writes nothing. Hidden from the CLI
reference (M8).

### 4.3 The JSON document

One object per pass, always with `project`:

```jsonc
{
  "project": "aurora",
  "at": "2026-10-18T13:02:21Z",
  "lease": { "state": "held", "holder": "boot-…", "expires_at": "…" },
  "pass": { "read": 3, "delivered": 2, "duplicates": 1, "refused": 0 },
  "acted": [ { "id": "…", "kind": "…", "outcome": "performed" } ],
  "replied": [ { "id": "r-…", "for": "…" } ],
  "refused": [ { "id": "…", "code": "replay" } ],
  "warnings": []
}
```

**The rule for every field:** a message's `id` may appear (it is a key, and the ledger is keyed by
it); a message's **text never does**, and neither does the MAC, the key, or the item name's value.
`refused[].code` is the module's own code, never a paraphrase. `warnings` is present and empty,
like `ls`'s, so a reader can rely on it.

### 4.4 Exit codes

| Code | When | Decided by |
| --- | --- | --- |
| 0 | **a pass completed**, including one that read nothing at all — no lease held, or an empty inbox — and one whose every message was refused as a duplicate, a replay, expired, `stale-pending` or badly signed. Per-message refusals are outcomes, reported in `refused[]` and the pass counters | the pass's own counters: `read == 0` is a completed pass, and a per-message refusal is recorded, not returned |
| 1 | **a run-level refusal**, before or outside the pass loop: a bad flag or combination (`--interval` below 1 s); no project selectable; no mailbox configured; a keychain item name absent or not of M5's shape; **MAC verification or signing unavailable (M4/§8.3), refused at startup**; **and the database or keychain refusing** — `dbErr`'s `rtdb.ErrPermission` branch, `adminDenied` for a refused write, `checkDBProject`'s not-this-project's-database (**all three are `userErr`**) | `userErr` / `ExitUserError`, and the flag and config refusals the command raises itself |
| 2 | the database could not be **reached or written** — an outage or a transport error, which is all `dbErr` sends to `remote(err)`; and the new keychain package's unopenable-backend case, which T2 must classify the same way | `remote(err)` / `ExitRemoteError` |

**The second exit 1, and the only one that is a pass-level outcome**: a `--once` pass that read at
least one message, performed no action, refused every message it read, and refused at least one
**action** — M12's four clauses — is also **1**, decided by that predicate, which is the command's
single exit branch. This is the row M12 spends its detail on, and it is the same code as the
run-level refusal because it is the same answer to the operator: nothing happened.

**The rule in code is M12's one predicate**, four clauses: **at least one message read**, **no action
performed**, **every message read refused**, **at least one of those refusals an action refusal** →
1, else 0. It exists so that `--once` has a scriptable "nothing happened" answer without making an
ordinary refusal a failure. The first clause is what keeps the vacuous passes at 0 (nothing read,
nothing refused); the fourth is what keeps a duplicates-only pass at 0. Both sides of every row are
tested in T5, and T6 asserts the action case in a pass that also delivered (0), in one that delivered
nothing (1), and in one whose only readings were per-message refusals (0).

---

## 5. The one `root.AddCommand` line, and everything it drags with it

`NewRootCmd` (`internal/cli/root.go`) ends in one call to `root.AddCommand`:

```go
root.AddCommand(
    newVersionCmd(), …, newProjectCmd(),
)
```

P4.1 adds **`newMailboxCmd()`** to that list — **one line, no other edit**. The sweeper is a
subcommand of it, so it needs no line of its own. What that line drags with it, verified:

1. **`go run ./internal/clidoc` must be re-run.** `internal/clidoc/cliref.go`'s `defaultPath` is
   `website/content/cli-reference.md` and `internal/clidoc/cliref_test.go`'s
   `TestCliReferenceIsCurrent` compares the committed bytes with `Generate(cli.NewRootCmd())`, so a
   command in the tree and not in the file is a failing test — with the regenerating command in the
   failure message. `internal/cli/docs_website_cliref_test.go` holds the other direction.
2. **`docs/design/v1.md` §9.1 gains a `fugaro mailbox` row**, and its prose list of `--json`
   commands (the paragraph is "Every command that reports something supports `--json`", which today
   ends "Exit codes are stable: 0 for ok, 1 for a user error, 2 for a remote failure") then covers
   it. §9.1 is the command table, and a command that exists and is not in it is a design document
   that describes a CLI the reader does not have. **No test enforces this one** — the section says
   so rather than implying a guard — so it is a duty of the commit and a line in T5's review
   checklist.
3. **The hidden sweeper does not appear** in either, because `internal/clidoc`'s skip rule reads
   cobra's own `Hidden` field (`documentedCommands`), and `newBudgetHistoryCmd` is the precedent.
4. **No `docs_bucket_iam_test.go` pressure**: its guards scan for IAM claims in docs, and the globs
   it scans do not reach `docs/plans/*.md`. This plan therefore adds no IAM sentence anywhere near
   the bucket, and T7 runs the docs guards to prove it.

---

## 6. Where state lives

| State | Where | Who may write it | Written how |
| --- | --- | --- | --- |
| The lease, and presence | `mailbox/$mb/heartbeat` = `{at, boot}` (+ `mac`, and `agents`, once P6.4 lands) | the consumer, with the **`CONSUMER` credential** (C0) | ETag conditional write (M1); `boot` names the holder; **signed** (M4b) |
| The consumer's answers | `mailbox/$mb/outbox/$msg` = `{id, sender, text, at}` (+ `mac`, once P6.4 lands) | the consumer, with the `CONSUMER` credential | create-only, per the merged grant; **signed** (M4b) |
| The phone's receipt that an answer was spoken | `mailbox/$mb/outbox/$msg/acked` | **the phone** (`MAILBOX`) | not this command's (C2) |
| The dedupe ledger, the actions performed, the window state | `$XDG_STATE_HOME/fugaro/mailbox/<project>/ledger.json` | the consumer | tmp+rename, `0o600` — the *prefix* and the discipline are `internal/watch/acks.go`'s (`AckPath` is the single file `…/fugaro/watch-acks.json`); the `<project>` level is **new here** (M7) |
| Prompts waiting for a confirmation record | `$XDG_STATE_HOME/fugaro/mailbox/<project>/pending.json` | the consumer | same |
| The keychain **item name** | the project's local config block (`internal/localcfg`) | the operator | plain config, **not secret**, and it may be printed (M5) |
| The **pairing key** | the OS keychain, read by that item name (P6.6 generates it there) | the OS, then never written | read once per pairing, memory only, never printed (M5) |
| The **presence verdict** | **nowhere** — computed, never written | — | `shared/presence`'s `evaluatePresence` is a pure function and **no node and no grant** is designated for its output; the sweeper prints it and writes nothing (§4.2) |

**Nothing here is stored in the runs bucket**, and the sweeper's `keep` set holds no `m~` key
today — `internal/budget/sweep.go`'s `deleteUsers` and `RunUID` are the runs bucket's (`r~…`), and
extending the sweeper to `m~…` is **P6.6's** (proposed, NOT APPROVED), not this plan's. If the
sweeper deletes an `m~…` user before P6.6 lands, that is a **finding, not a silent fix** — T7's
docs guard and T8's review check it, and it goes in the PR body.

---

## 7. The loop

```
        ┌─ read heartbeat (ETag) ────────────────────────────────────────┐
        │  takeLease(readLease(hb, now))  → not ok: write nothing, act on nothing, sleep
        └─ ok → write heartbeat {at: now, boot: bootId} if-match ETag ───┘   (412 → lost race → sleep)
                                    │
        read inbox (operator credential) │ for each $msg, oldest first
                                    ▼
        validateMessage → MAC verify (M4) → step(state, {type:'deliver', now, message, kind})
                                    │
        duplicate / replay / expired / future-timestamp / bad-mac → recorded, NOT acted on
                                    │
        new → step(state, {type:'act', now}) → outcome
                                    │
        ┌───────────────────────────┴───────────────────────────────┐
        │ COMMIT THE LEDGER FIRST (tmp+rename). A lost race here     │
        │ performs nothing — that is the at-most-once fence.         │
        └───────────────────────────┬───────────────────────────────┘
                                    ▼
        performed            → run the action (M6's allow-list; refusal before any spawn)
                             → SIGN the reply, then write it (M4b/T2a) — cannot sign: write nothing
        stale-pending        → perform nothing, sign and write the reply saying so (M2, M4b)
        refused              → write nothing to the outbox; record the code; log the code, never the text

  the heartbeat, every renewal: SIGN {at, boot} … then write it (M4b/T2a) — cannot sign: no write,
  and since the heartbeat write IS the lease, no lease: the pass acts on nothing (§8.2)
```

**The three orderings that are load-bearing**, and each is tested with a mutation:

1. **The ledger is committed before the action is performed**, so a consumer that dies between the
   two re-performs nothing after the restart (T4's kill-and-restart test).
2. **The MAC is verified before the delivery reaches the ledger**, so a message that fails
   verification is not in the ledger, is not acted on, and does not consume its `(sender, id)`
   slot — a forged message cannot deny service to the real one (T2's mutation).
3. **The MAC is applied before the write**, so an unsigned reply or heartbeat never reaches the
   store at all — which is what makes "the phone saw it" imply "the consumer signed it" (T2a's
   three mutations, one of them on the heartbeat).

**A message the command cannot parse is refused once and not retried for ever.** The ledger records
it as refused with its code, the pass is counted, and the next pass does not re-read it as new;
`input-refusals` are the module's, per its `INPUT_REFUSALS`.

---

## 8. The MAC, precisely: **verifying in** (§8.1), **signing out** (§8.2), and the state until Phase 6 (§8.3)

### 8.1 Inbound — what is verified, in the order the command does it

1. **The message is what the schema says it is** — `validateMessage`'s rules, bound through the
   corpus (T1). A record the schema refuses is refused with the schema's own code.
2. **The MAC covers every field except `mac` and `acked`** (functional-spec-reconciliation §3.5),
   over a **canonical serialisation** — fixed key order, no whitespace, the numbers as the module
   writes them. The canonicalisation is **P6.3's** (`canonicalBytes(message)` plus its vectors
   file), so this command's function is written behind a seam whose *behaviour* the corpus pins and
   whose *input* is `FIELDS` minus `mac` minus `acked`.
3. **The key is the pairing's**, from the OS keychain by item name (M5), read once.
4. **The timestamp's future tolerance is 5 minutes** (`MAX_CLOCK_SKEW`, `shared/mailbox`'s
   `DEFAULT_FUTURE_TOLERANCE_MS`) — one millisecond beyond it and the delivery is refused
   `future-timestamp`.
5. **Failure is a refusal with a code, never an error that stops the loop**, never a retry that
   never ends, and never a fallback to acting.

### 8.2 Outbound — what is signed, and when the write is refused

The same function, the same key, the same covered fields, in the other direction (M4b):

1. **The value to be written is built first, then signed, then written** — `{id, sender, text, at}`
   plus `mac` once P6.4's `.validate` admits it, so what the MAC covers is exactly what lands.
2. **`sign` and `verify` are the same function**, held to `shared/mailbox-schema`'s
   `canonicalBytes` and its vectors by the corpus: a signature the command produces must verify
   under the command's own `verify`, and a test asserts that round trip **and** that the covered
   field set is the same one in both directions. A mutation that adds or drops a field from the
   covered set must fail in **both** directions (T2 and T2a are two Red steps, not one).
3. **Both outbound families are signed**: the outbox reply, and the heartbeat — the heartbeat's
   `{at, boot}` (and its `agents` once P6.4 lands) carry a MAC too, so a minted token cannot forge
   the presence a `MAILBOX` reader trusts (O7; §2's residual note, and `shared/presence` reads it).
4. **A write the command cannot sign does not happen.** No unsigned reply, no unsigned heartbeat —
   and since the heartbeat write is the lease (M1), a consumer that cannot sign also cannot hold the
   lease, so it acts on nothing. It reports the refusal with a code (never the message's text) and
   retries the reply on the next pass, because a reply the phone has not spoken is still owed (M2).
5. **The same is true of every other write**: the ledger and the pending-prompt file are local
   state, not wire messages, and they are **not** MAC-covered — but they are written with the
   redactor (D118) and at `0o600` (M7).

### 8.3 The honest state until P6.3 and P6.4 land

**Until P6.3 is approved**, neither half can be computed: the schema carries no `mac`, no
`canonicalBytes` and no vectors, and `FIELDS` has no member for it. So the command **refuses every
inbox message as unverifiable** (8.1) **and refuses every outbox and heartbeat write as
unsignable** (8.2); it says so with **one fixed sentence per direction**, and — per the exit
contract (M12, §4.4) — a command that can neither verify nor sign **fails at startup with exit 1**
rather than looping over a mailbox it can do nothing with. The tests prove all of it: the inbound
refusal, the outbound refusal, the two mutations that must fail one each, and the startup exit.
**This is the honest state of the world and it is the first owner decision, which must be answered
for both directions.**

---

## 9. D128: the refusal fence

| The message asks for | The consumer's answer today | Why |
| --- | --- | --- |
| `fugaro cancel RUN` | **refused** | D128; F38 records that no argv table fences it |
| `fugaro budget kill` | **refused** | same |
| `gh pr merge` / an approve | **refused** | same; and the 2026-10-17 amendment's allowance is P6.2's |
| Anything not in the allow-list | **refused** | the default is refusal, not permission |
| A launch (`fugaro run`) | allowed | **D162**: launches need no confirmation |
| A read-only report (`ls`, `diagnose`, `report`) | allowed | nothing is changed |
| A destructive action **with** P6.2's confirmation record naming the same action id | allowed **then** | the 2026-10-17 amendment, through P6.2 — which does not exist yet |

The allow-list is a **value in code** (a slice of action kinds, each with the argv shape it may
spawn), and the refusal is evaluated **before** any spawn (M11). The refusal is **recorded in the
ledger and logged by code** — the message's own text is never put in an argv element and never
printed; the ledger holds ids and codes — and its **exit code follows M12 and §4.4**: **0** when the
pass did other work **or read nothing at all**, **1** only under M12's predicate — at least one
message read, no action performed, every message read refused, and at least one of those refusals an
action refusal. Owner decision 3.

**The amendment is honoured as a seam and not as a behaviour**, because P6.2 is NOT APPROVED: the
code path exists, is covered by a test that builds a synthetic confirmation record, and **cannot be
reached from a real message today**. Owner decision 3.

---

## 10. Expiry and the windows

| Question | Answer | Source |
| --- | --- | --- |
| How long is a prompt valid? | **72 hours** from `at`, computed by the consumer (there is no `expires_at` field) | D118, O5 answered; D168 **as amended** 2026-10-17 (a prompt expires at D118's TTL) |
| What is a command's window? | **6 hours** (`shared/mailbox`'s `DEFAULT_WINDOW_MS`), at most **7 days** (`MAX_WINDOW_MS`) | §3.6, D173 (a command's 6-hour window) |
| Delivered after the TTL | refused `expired`, never delivered at all | `shared/mailbox` |
| Delivered in time, acted on too late | consumed **once**, answered `stale-pending`, **nothing performed**, the reply still written | `shared/mailbox`; simulator README |
| A stale `at` beyond the window | refused `replay` for a command, nothing acted on | §3.6 |
| A timestamp in the future by more than the tolerance | refused `future-timestamp` | §3.5 (5 minutes) |

**Two windows, named apart.** **D118's TTL is the `259 200 000` ms** the consumer passes as
`promptTtlMs` (functional-spec-reconciliation §3.6, O5: 72 × 3 600 000) — that is the **prompt**
window. **D173's 6-hour window is `shared/mailbox`'s `DEFAULT_WINDOW_MS`**, already in the module —
that is the **command** window. The command declares the TTL as the Go duration (`72 * time.Hour`,
M3) and a test pins that the duration and the number handed to the module are the same, so a future
edit to one is a failing test rather than a silent divergence.

---

## 11. How it is tested over the simulator

Everything runs as `go test ./internal/...`, on the fakes this repository already has
(`internal/gcpfake`'s Realtime Database and identitytoolkit fakes), with **no Node, no network and
no clock** — the pass takes `now` as a value, exactly as `shared/mailbox` and `shared/mailbox-sim`
do.

| Layer | What it proves | Where |
| --- | --- | --- |
| The corpus (M9) | the Go binding gives the module's outcomes and codes for the module's inputs | `internal/mailbox/testdata/parity.json` + a Go replay test; a JS generator in `shared/mailbox/` |
| The simulator scenarios | the seeded fault schedules (duplicate, reorder, drop, delay, clock skew) produce at-most-once acting, at-least-once delivery and one lease holder | the corpus's `scheduleText()` lines replayed in Go |
| The store | the ETag CAS refuses the loser with **nothing written**; the ledger survives a restart; the write set is exactly the outbox and the heartbeat | `internal/gcpfake` |
| The fence | every destructive action is refused, before any spawn; the mutation that removes the allow-list turns a test red; the exit code is 0 for a pass that also delivered, 0 for a pass that read only per-message refusals, and 1 for one whose only reading was a refused action | a fake action runner that records what was spawned |
| The MAC, **inbound** | an unverifiable message is refused and never acted on; a verified one is delivered; the mutation that skips verification turns a test red | a fake keychain |
| The MAC, **outbound** | every outbox reply and heartbeat carries a signature over the same covered fields; the command's own `verify` accepts what its `sign` produced; a write it cannot sign **does not happen** (no unsigned reply, no unsigned heartbeat, no lease taken); the mutation that writes unsigned turns a test red | a fake keychain, and a recording store |
| The **write credential** | the writes are made with a `CONSUMER`-claimed token (C0), and the merged create-only grant refuses a second write to one outbox id | `internal/gcpfake`'s identitytoolkit fake, and the emulator rules for the store's other half |
| The expiry | the 72-hour boundary, exactly; the 6-hour command window; the 5-minute future tolerance | injected `now` |
| The exit contract | 0 for a completed pass, refusals included **and nothing read included**; 1 for a bad flag, a missing mailbox, the M4/§8.3 startup refusal and a refused database (`dbErr`/`checkDBProject`); 2 only for `dbErr`'s `remote` errors; M12's four-clause predicate for the one pass-level case | `--once` runs through the command's own `RunE` |

**Every test is mutation-proven**, in `shared/*/mutation-proof.sh`'s convention: plant the bug, run
the test, watch it fail, remove it. Each task below names its **Red step** — the one-line mutation
its verification must catch — and a task whose Red step does not fail is not done. The Red steps
are the point: a test that cannot fail is not a fence.

**What is not tested here, and cannot be:** checks 80 and 83–88 (a real phone, a real orchestrator,
a real project, a real clock, real audio) are **owner-run**, written into the register by P4.2, and
none of them is this plan's. This plan claims **no live behaviour works**.

---

## 12. Tasks

Each is independently mergeable, in the order below, each with a runnable verification and the
mutation that must turn something red. `SECURITY-SENSITIVE` tasks get an independent opus review of
the diff by a reviewer that did not write it, per the mobile plan's Global Constraint 14.

### Task T1: The message binding and the schema vectors — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/message.go` (new) + `internal/mailbox/message_test.go` (new);
`shared/mailbox/parity.test.js` (new, the generator); `internal/mailbox/testdata/parity.json` (new,
committed).
**Depends on.** P0.2, P1.1 (merged), M9.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -count=1`; the corpus's own hash is
checked; `go run ./internal/clidoc` is untouched by this task.
**Red step.** Widen `FIELDS` on the Go side by one field (`acked` accepted as required) and watch
`TestParitySchema` fail on the module's own refusal code.
**Reviewer tier.** SECURITY-SENSITIVE — this is the protocol both ends of the mailbox agree on.

### Task T2: The MAC function, the keychain read, and the **inbound** gate — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/mac.go` (new) + `mac_test.go`; `internal/mailbox/keychain/` (new, with
a fake) + tests; `internal/localcfg`'s item-name field and its validation test.
**Depends on.** T1; **M4a**, M5.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox/... ./internal/localcfg -count=1`: the
covered field set is exactly `FIELDS` minus `mac` minus `acked`, pinned by the corpus; the inbound
gate refuses an unsigned, wrongly-signed, tampered or future-stamped message and never hands it to
the ledger; a test asserts the key never appears in any output, any error string, any log line or
the JSON; a test asserts the item name **does** appear in the diagnostic.
**Red step.** Make the gate return "verified" when the MAC field is absent and watch
`TestUnverifiableIsRefused` fail — and, separately, put the key into an error message and watch
`TestKeyIsNeverPrinted` fail.
**Reviewer tier.** SECURITY-SENSITIVE — a mistake here is a forged action, not a bug.

### Task T2a: The **outbound** signature, on the reply and on the heartbeat — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/mac.go` (the `sign` half of the same function T2 lands) +
`internal/mailbox/mac_sign_test.go`; the write paths in `internal/mailbox/` that put a value in the
outbox or the heartbeat; corpus entries for the round trip.
**Depends on.** **T2** (the same function and key), **T3** (the heartbeat write is the lease), T1;
**M4b**; §3.5's "who signs".
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Sign|Outbound|Heartbeat' -count=1`:
what `sign` produces, the command's own `verify` accepts; the covered set is the same one T2 pins, in
both directions; **both** families are signed (the outbox reply and the heartbeat, the heartbeat's
`{at, boot}` included); a consumer that cannot sign writes **nothing** — no reply, no heartbeat, and
therefore no lease taken (M1) — and reports the refusal by code; the reply it could not write is owed
again on the next pass rather than lost. Swept over the simulator's seeded schedules, not one call.
**Red step.** (a) Write the reply without a MAC and watch `TestOutboundIsSigned` fail; (b) swap the
key in `sign` (sign with one key, verify with the other) and watch `TestSignVerifyRoundTrip` fail;
(c) let the heartbeat be written unsigned when signing fails and watch
`TestUnsignedHeartbeatIsNotWritten` fail.
**Reviewer tier.** SECURITY-SENSITIVE — an unsigned reply is a phone reading an answer it cannot
attribute, which is the same class of exposure as the inbound half.

### Task T3: The lease on the heartbeat, by ETag CAS — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/lease.go` (new) + `lease_test.go` (new);
`shared/lease/parity.test.js` (new) + corpus entries.
**Depends on.** T1; P1.2 (merged); M1.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Lease' -count=1` over
`internal/gcpfake`: the read uses `GetETag` and the write `PutIfMatch`; two consumers read one empty
heartbeat, both get `ok: true` from `takeLease`, the second write is refused `ErrPrecondition` and
**nothing moves**; the retry is bounded (the loser does not spin); a lease 5 minutes ahead is held
and 5 minutes and 1 ms ahead is taken over; `--no-heartbeat` takes no lease and acts on nothing.
**Red step.** Drop the `if-match` from the second write and watch `TestTwoConsumersOneLease` fail
with both writes landing.
**Reviewer tier.** SECURITY-SENSITIVE — this is the fence that makes at-most-once true.

### Task T4: The ledger, the restart, and the dedupe — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/ledger.go` (new) + `ledger_test.go` (new).
**Depends on.** T1, T3; P1.1; M7.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Ledger|Restart' -count=1`: a
duplicate delivery is not acted on twice; a process killed between the ledger commit and the action
re-performs nothing after a restart; the file is `0o600` and written tmp+rename (asserted by
inspecting the directory during a write); a full ledger is refused `dedupe-full`.
**Red step.** Move the ledger commit **after** the action and watch `TestRestartDoesNotRePerform`
fail; and make the write truncate in place and watch `TestLedgerWriteIsAtomic` fail.
**Reviewer tier.** SECURITY-SENSITIVE — a dedupe hole re-performs an action the operator's phone
asked for once.

### Task T5: The command, the surface and the one `root.AddCommand` line

**Files.** `internal/cli/mailbox.go` (new) + `internal/cli/mailbox_test.go` (new);
`internal/cli/root.go` (**one line**); `website/content/cli-reference.md` (regenerated);
`docs/design/v1.md` §9.1 (one row and the `--json` list). **These four move together in one commit** —
and the guard covers **three of the four**: `internal/clidoc/cliref_test.go`'s
`TestCliReferenceIsCurrent` and `internal/cli/docs_website_cliref_test.go` fail on a command that is in
the tree and not in `website/content/cli-reference.md` (the file is generated, so the flags follow
from it). **`docs/design/v1.md` §9.1 is not test-enforced** — nothing in this repository reads that
table and compares it with `NewRootCmd` (the pins that exist are for
`website/content/cli-reference.md` and for `docs/project-layer.md` §7's flag table, which this change
does not touch), so keeping the row there is a duty this plan states and a reviewer checks, not a red
test. Claiming it was enforced would be the same kind of false guarantee as any other test that does
not exist.
**Depends on.** T1–T4, T2a; D113c; M8, M12, **C0** (the write credential the pass uses).
**Verification.** `go run ./internal/clidoc` then
`GOFLAGS=-p=2 go test ./internal/clidoc ./internal/cli -run 'CliRef|Docs|Lint' -count=1`, plus
`gofmt -l internal/cli` and `GOFLAGS=-p=2 go vet ./...`; a `--once --json` test asserts the document
shape, the `project` field, and that no message text and no key appears in it. **The exit contract is
tested here, on the command's own `RunE`, on both sides of every row of §4.4**: `--interval 500ms`
→ 1; no mailbox configured → 1; a malformed item name → 1; **M4/§8.3 (verification or signing
unavailable) → 1 at startup, with no pass run**; a completed pass whose every message was refused as
a duplicate → **0**; a pass that held no lease → **0**; a pass over an empty inbox → **0** (both read
nothing, so M12's first clause fails and the exit is 0); a `--once` pass that read only per-message
refusals and performed no action → **0** (clause 4); a `--once` pass that read only a refused action
and performed no action → **1**.
**Red step.** Add the command to the tree without regenerating the reference and watch
`TestCliReferenceIsCurrent` fail with the regenerating command in the message; **and**, separately,
make the pass return `ExitUserError` on any refused message and watch
`TestRefusedMessageStillExitsZero` fail.
**Reviewer tier.** standard.

### Task T6: The refusal fence and the action runner — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/action.go` (new) + `action_test.go` (new);
`internal/cli/mailbox.go`'s runner wiring.
**Depends on.** T5; D128 and its amendment; M6, M10, M11.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Refus|Action|Write' -count=1`: every
destructive kind is refused; the refusal precedes any spawn (the fake runner records that it was
never called); an unrecognised kind is refused; a launch is allowed; a synthetic P6.2 confirmation
record admits the destructive path; the write set is exactly the outbox and the heartbeat, **and each
of those writes carries a MAC** (M4b, T2a's test); **the action-refusal exit code is asserted all
three ways** — 0 in a pass that also delivered something, 1 in a pass whose only reading was a
refused action, and 0 in a pass whose only readings were per-message refusals (§4.4, M12's clauses
3 and 4).
**Red step.** Remove `cancel` from the refusal list and watch `TestDestructiveIsRefused` fail; and
let the runner write an unsigned reply and watch T2a's `TestOutboundIsSigned` fail (the two tasks
share that fence, so both must be green).
**Reviewer tier.** SECURITY-SENSITIVE — this is D128's fence.

### Task T7: The hidden sweeper, the TTL and the reported presence verdict

**Files.** `internal/cli/mailbox.go`'s sweeper command + `internal/cli/mailbox_sweep_test.go`;
`internal/mailbox/expiry.go` + `expiry_test.go`.
**Depends on.** T4; D118, D168 as amended, D173; P1.4 (`shared/presence`).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/mailbox -run 'Sweep|Expiry' -count=1`:
`fugaro mailbox sweep` is absent from `website/content/cli-reference.md` (asserted); the 72-hour
boundary is exact, and 72 h equals 259 200 000 ms; **the presence verdict is computed and reported
and nothing is written for it** — the test asserts the store is unchanged by a sweep whose only work
was a verdict, which is §4.2 and §6's row, and the reason is that no node and no grant is designated.
**Red step.** Change the TTL to 6 hours and watch `TestPromptExpiresAt72h` fail; unhide the sweeper
and watch the reference test fail; make the sweeper write a verdict node and watch
`TestSweepWritesNoVerdict` fail.
**Reviewer tier.** standard.

### Task T8: The parity corpus, the simulator scenarios and the review checklist

**Files.** `internal/mailbox/parity_test.go` (the replay, the hash check, **and the regenerate-and-diff
check**); `shared/mailbox-sim/parity.test.js` (the scenario exporter);
`docs/mobile/mailbox-review-checklist.md` (new, short: the things a reviewer of T1–T7 must confirm,
each with the test that proves it).
**Depends on.** T1–T7.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Parity' -count=1`; 120 seeded
schedules with three messages, two consumers and twelve passes each, replayed in Go and compared
outcome by outcome; a failing case prints the seed and `scheduleText()`.
**The drift check, and exactly how much it is worth.** `TestParityCorpusIsCurrent` runs the JS
generator and diffs its output against the committed corpus **when `node` is on `PATH`**, and
**skips with a recorded reason when it is not** — which is the case in this repository's `test` job
today (no Node) and on this runner image. So the check catches drift **where it can run at all** (the
owner's machine, and the P4.3 job once the owner adds it) and is otherwise **silent**: a `shared/`
module that changed under an unregenerated corpus is caught by nobody until then. That is the real
cost of the corpus design and it belongs in the decision, not in a footnote (decision 4). The
distinction the plan draws: the **hash** detects a **hand edit** of the artifact (it travels inside
it); only the **regeneration** detects a **changed module**.
**Red step.** Hand-edit one outcome in `parity.json` and watch `TestParityHash` fail, rather than the
replay silently agreeing with the edit; and, where `node` exists, change a `shared/mailbox` constant
without regenerating and watch `TestParityCorpusIsCurrent` fail.
**Reviewer tier.** standard.

---

## 13. File ownership, levels and PR groups

| File | Tasks | Note |
| --- | --- | --- |
| `internal/cli/root.go` | **T5** | **one line**; the mobile plan reserves a second line for P6.6 (level 4) |
| `internal/cli/mailbox.go` | T5, T6, T7 | one writer per task: T5 the surface, T6 the runner, T7 the sweeper |
| `internal/mailbox/` (new package) | T1–T4, **T2a**, T6, T7, T8 | one file per task, as listed |
| `internal/mailbox/mac.go` | **T2 and T2a** | the one deliberate two-writer file: T2 lands it with `verify`, T2a extends it with `sign` in a dependent task. There is **one** function, so the two halves cannot drift |
| `internal/mailbox/testdata/parity.json` | T1 (generated), T8 (replayed) | `shared/mailbox/parity.test.js` writes it; the Go side only reads it |
| `website/content/cli-reference.md` | T5 | **generated** — never hand-edited |
| `docs/design/v1.md` | T5 | one §9.1 row |
| `shared/` | T1, T3, T8 (test files only) | **no merged module changes.** If a task needs one, it is a finding, not an edit |
| `internal/localcfg` | T2 | one field, validated |
| `internal/cli/docs_mobile_mailbox_plan_test.go` | **this plan, not a task** | the pin this document ships with: it checks that every path and symbol cited above is still in the tree, and that M12, §4.4 and §9 state the same four-clause exit rule. It is the answer to the review that found the rule stated as one clause; it is not T1–T8's and it adds no product code |
| `.github/workflows/ci.yml` | **nobody** | P4.3 is owner-made (Global Constraint 8); see §16 |

**Levels** (the mobile plan's): T1 and T2 are level 3 (with P4.1); T2a, T3–T8 follow in one lane,
one writer each, because each builds on the one before. **Dependency edges that exist explicitly in
the mobile plan** — `P4.1 → P6.6` (the `root.go` line) and `P4.1 → P6.7` (this file) — are
respected: this plan does not touch what P6.6 or P6.7 will, and T2a is deliberately the seam P6.7
extends rather than an overlap with it.

**PR groups.** T1 and T2 together (the protocol, its one MAC function and the inbound gate), or
separately if the owner prefers one task per PR; **T2a with T2's PR** (same file, same function,
same corpus — reviewing them apart would review half a fence); T3 and T4 together (the lease and
what it fences); T5, T6 and T7 together (the command and its two halves); T8 either with T1's PR or
alone. **P4.1's own PR is SECURITY-SENSITIVE** (the mobile plan's group P-F) and is reviewed as one.

---

## 14. What this plan does not do

- **It changes no merged `shared/` module.** If a task appears to need one, that is a finding.
- **It changes no merged rule.** The mailbox subtree on `main` is what it is; a fourth node, an
  inbox `acked`, or a `CONSUMER` read grant would each be a Phase 2 re-open, and none is proposed
  here.
- **It builds no part of Phase 6** — no pairing, no device node, no pairing-key generation, no
  confirmation record, no typed action record, and **no canonical serialisation**: `canonicalBytes`,
  the `mac` field, its vectors and the heartbeat's `agents` are P6.3's and its rules are P6.4's, and
  this plan **consumes** them. What it *does* build is the **two seams** through which P6.3's
  serialisation is used — the inbound gate (M4a) and the outbound signature (M4b, T2a) — each held
  to the vectors by the corpus and each **failing closed** until the field exists (decision 1). The
  distinction matters: P4.1 does not implement Phase 6's MAC, it fills the two places Phase 6's MAC
  will be needed and refuses to work without it. It also **depends** on P6.2 for the amendment's
  allowance (M6), on **P6.6** for the OS keychain entry the pairing key lives in (M5 — generated
  there, read here), and, per decision 8, on a **`CONSUMER` write credential that no task mints
  today**.
- **It writes no check into the register.** Checks 80 and 83–88 are P4.2's; 81, 82 and 89 are
  P4.2a's; all ten are owner-run.
- **It touches nothing under `.github/workflows/`.** The shared package's CI job is P4.3 and is
  owner-made; §16 describes it rather than making it.
- **It performs no live cloud action, touches no `belong` installation, EdgeWeb or EdgeServer, and
  reads, writes or asks for no API key, token, secret, certificate or password.**
- **It does not make the app's side true.** P3.6 signs the inbox messages this command verifies;
  until P3.6 exists there is nothing real to verify, which is what the corpus stands in for.

---

## 15. Owner decisions

Each with the recommendation first. Decisions 1, 2, 3 and 8 hold the task back; 4–7 shape it.

1. **The MAC covers both directions of the wire (§3.5's "who signs"): what does `fugaro mailbox` do
   while P6.3 and P6.4 are not approved? (Answer for both halves.)**
   **Recommended: fail closed in both** — refuse every inbox message it cannot verify (M4a) **and**
   refuse every outbox reply and heartbeat it cannot sign (M4b), say so with one fixed sentence per
   direction, and — because a command that can neither verify nor sign has no work — **fail at
   startup with exit 1** rather than looping over a mailbox it can do nothing with (§8.3, M12). The
   command is built complete with both seams, P6.3's `canonicalBytes` and vectors fill the one
   function used by both, and neither an unverified action nor an unsigned reply ever reaches the
   wire. The alternatives: verify inbound but write replies unsigned "until P6.7" (a phone reading an
   answer it cannot attribute), or act on unverified messages (a phone-shaped write making the
   orchestrator act with no authentication — D128's and D169's whole subject). **Cost of the
   recommendation:** the command does nothing useful in production until Phase 6 lands, and P6.7's
   outbound duty arrives with its seam already filled. **Note for the owner:** this plan's ruling is
   the conservative reading of a sentence that appears in two places (P4.1's own verification, and
   §3.5's "who signs"); if the owner reads the outbound half as **P6.7's alone**, then M4b moves out
   of P4.1, T2a is dropped, and §14's deferred list gains the outbound signature — a smaller task
   with the same exposure in the meantime, which is why the recommendation is to build it.
2. **Is the consumer's ack of an inbox message its outbox reply plus the ledger (C1/C2, Ruling M2),
   or must the rules change to give the consumer an inbox `acked`?**
   **Recommended: the outbox reply plus the ledger.** The merged rules make an inbox ack
   impossible for the consumer (`CONSUMER` has no inbox grant, and the inbox `$msg` write is
   `MAILBOX && !data.exists()`), and the merged simulator's inbox ack is its own stated invention.
   **Cost:** the merged simulator models a write the store forbids — worth a one-line note in its
   README, which is a separate small change the owner may prefer to make at the same time.
3. **Is D128's amendment (cancel, approve/merge, confirming — allowed through P6.2's record) built
   as a **seam** now, or as behaviour?**
   **Recommended: a seam, unreachable from a real message, proven by a synthetic record in a test.**
   P6.2 is NOT APPROVED and its record does not exist, so the refusal today is unconditional.
   **Cost:** one code path with no caller, which a reviewer must be told about — hence T8's
   checklist.
4. **The parity corpus (Ruling M9): a committed `parity.json` generated by a JS test, replayed in
   Go — or a Node subprocess in the Go test?**
   **Recommended: the committed corpus.** Neither this runner nor CI's `test` job has Node, and
   `shared/` has no CI job at all until the owner makes P4.3, so a subprocess would be a
   verification that cannot run. **Cost, stated plainly, because it is the weak point of this
   design:** drift protection is **discipline plus one best-effort check**, not a mechanism. The
   hash inside the corpus detects a **hand edit** of the artifact and nothing else; what catches a
   **changed `shared/` module** is T8's `TestParityCorpusIsCurrent`, which regenerates and diffs
   **only where `node` is on `PATH`** and **skips with a recorded reason everywhere else** —
   including, today, in this repository's own `test` job. So a `shared/` change that nobody
   regenerates the corpus for is caught **by no test here** until the owner makes P4.3. **The owner
   is being asked to accept that gap knowingly:** the mitigation available today is the generator
   being a test in `shared/mailbox/` (so any `shared/` PR that runs `npm test` regenerates it), and
   the P4.3 job closing it properly. If the owner wants it closed now, the alternative is to make
   the JS regeneration part of the `rules` job (which already sets up Node 22) or to add the parity
   corpus check to it — a workflow change, and therefore owner-made.
5. **Where does the keychain reader live?** **Recommended: a new narrow `internal/mailbox/keychain`
   with a fake**, because nothing in this repository reads an OS keychain today (verified: the only
   matches are Docker's own credential mentions and docs), and a boundary this small should not be
   a dependency (`github.com/zalando/go-keyring` or similar) unless the owner prefers one.
   **Cost:** the platform code is ours to write and to test, on three platforms.
6. **Is `fugaro mailbox` visible in the CLI reference, and the sweeper hidden?**
   **Recommended: yes** — the consumer is a real operator command, and the sweeper is internal, in
   the shape of the hidden `fugaro budget history`. **Cost:** one more row in a generated file and
   in design §9.1, both of which T5 writes.
7. **Should the ledger and the pending-prompt file be **one** state file or **two**?**
   **Recommended: two**, because a waiting prompt is not a consumed delivery and one file would let
   a crash between the two meanings be ambiguous. **Cost:** two atomic writes per pass instead of
   one.
8. **Which credential authenticates the consumer's writes (§2's C0), since no task in the mobile
   plan mints one?** Today the command can read the inbox only as the operator (ADC, rules
   bypassed), and the merged `CONSUMER` grant — the one that makes the outbox create-only and the
   heartbeat the only replaceable node — binds a token **carrying `mc == $mb`**, which **no approved
   task mints**: P6.6 mints `p~` and the phone's `m~`, P6.4 writes the device and pairing nodes, and
   the rules' comment says `CONSUMER`'s `mc` "is minted the same way" without naming the task.
   **Recommended:** the writes carry a `CONSUMER`-claimed credential — a minting step added to P6.6
   (or a small task of its own, decided in P6.6's CLI plan, since P6.6 is where the device token is
   minted and where the launcher's `token_minter` role is already reasoned about), so the merged
   grants are a **store-level fence** on this command and the `mc` claim has a named holder. **Then
   P4.1 depends on that step for its production write path**, and the tests use
   `internal/gcpfake`'s identitytoolkit fake with a minted `mc` token meanwhile. **The alternative,
   and its cost:** make the writes with the operator's ADC too — simpler, needs no new minting, and
   it means the merged `CONSUMER` grant and its create-only rule **do not bind this command at
   all**, the Go write-set test in T6 becomes the *only* fence on what it writes, and the C1/C2/C3
   argument in §2 stops being a rules argument for the writes (it stays one for what the phone may
   do). **The plan's recommendation is the first:** a fence that the store enforces is worth one
   minting step, and it is the reading the merged rules were written for.

---

## 16. Unverified, and what cannot be verified here

- **`cd shared && npm test` was not run for this plan, and could not be:** this runner image has
  **no Node** (`node --version` → `node: command not found`). The plan is docs-only, so nothing
  depended on it; the parity design (M9) is built so that no Go verification does either. It is
  recorded in the pull request body.
- **The Go tests this plan describes do not exist yet** — every "Verification" line above is a
  **plan**, not a result. No task in this plan is done.
- **Checks 80 and 83–88** (a real phone, a real orchestrator, a real database, a real clock, real
  audio hardware) are owner-run and unrun. **No live behaviour is verified by this document.**
- **The RTDB conditional write against a real database** is not verified here. The client supports
  it (`internal/rtdb/client.go`'s `GetETag`, `PutIfMatch`, `ErrPrecondition`) and
  `internal/budget/kill.go`'s `SetKill` uses it in production, and T3 proves the fence against
  `internal/gcpfake`. What is unverified is a live project's answer, which is check 83's
  neighbourhood and is the owner's to run.
- **The OS keychain on macOS and Windows** is unverified by this plan: the tests use a fake, and no
  agent reads a real keychain. **Who generates the key is P6.6's** (it "generates the per-pairing MAC
  key into the OS keychain (the settings hold the item's name only, Voice D42(2))"), which is where
  this plan's M5 gets its item name from.
- **The `CONSUMER` write credential does not exist** — no task mints one (§2's C0, decision 8), so
  the production write path is a **dependency this plan states rather than a capability it has**.
  Everything is tested against `internal/gcpfake`'s identitytoolkit fake with a minted `mc` token.
- **The parity corpus's drift check cannot run wherever Node is absent** — including this runner and
  CI's `test` job today — so `TestParityCorpusIsCurrent` **skips** there and a `shared/` change under
  an unregenerated corpus is caught by no test in this repository until P4.3's job exists. The
  recommendation in decision 4 is offered with that gap named, not hidden.
- **The sweeper's treatment of `m~…` users** is P6.6's and is not approved. Until it lands, a
  `mailbox sweep` that deletes an `m~…` user is a **finding** and must be reported, not fixed here.
- **The shared package's CI job (P4.3)** does not exist. The proposed change — a job that installs
  the shared package's dependencies and runs its test script, alongside the Go job and the `rules`
  job, which is the only job that sets up Node today — belongs in `.github/workflows/ci.yml` and is
  **owner-made**. It is described here because an agent may not write it.

## 17. Risks

| Risk | What it costs | Where it is held |
| --- | --- | --- |
| The go/no-go on decision 1, **for both directions** | the command is inert until Phase 6 | the first owner decision; both seams are complete and tested either way |
| The corpus drifts from `shared/` | the Go side agrees with a stale module | **discipline only**, plus `TestParityCorpusIsCurrent` where Node exists (T8); the hash fails a **hand edit**, not drift — §16 and decision 4 |
| The ETag CAS is not honoured by a real RTDB | two consumers act | T3's fence, and check 83 by the owner |
| A refusal list that is right today and wrong later | a destructive action from a voice message | the list is a value with a test; an unrecognised kind is refused by default (T6) |
| The `CONSUMER` write credential is never minted | the writes fall back to the ADC and the store stops being a fence | decision 8; T6's write-set test is the fence that survives either answer |
| The keychain code is ours | a platform bug, or a leak | T2's two mutations; the reviewer tier |
| `internal/budget/sweep.go` touching `m~…` | a deleted mailbox's state | §6 and §16: a finding, not a silent fix |

## 18. Execution

1. The owner answers decisions **1, 2, 3 and 8** (the four that hold the task back) and 4–7 if
   convenient, and **approves P4.1 separately**, which the mobile plan's step 6 requires and which
   this document does not perform.
2. Then T1 and T2 land first, SECURITY-SENSITIVE, each with its independent opus review, and the
   corpus generator runs (the owner, or the P4.3 CI job once it exists). T2a follows immediately —
   it is the same function, the same key and the same corpus, and it is what P6.7 expects to find
   filled.
3. Then T3 and T4, then T5–T7, then T8.
4. Every task: commit, then `fugaro verify build` and `fugaro verify test`, with the working tree
   clean; the Docs guards, `gofmt -l` and `GOFLAGS=-p=2 go vet ./...` in the same run.
5. Nothing in this plan merges without a passing verification and a mutation-proven Red step, and
   nothing claims a live behaviour works before its check has been run by the owner.
