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
- **Phase 6 is approved, and most of its modules are merged — this plan is written against what
  landed.** The owner approved all of Phase 6 on 2026-10-18. **Merged on `main` today — five of its
  tasks, each with the module that proves it:** **P6.1** (`shared/command/index.js` — the command
  grammar),
  **P6.2** (`shared/confirm/index.js`, with the confirmation machine's rules, its `vectors.json` and
  its mutation proofs), **P6.3** (the schema extension — `mac` required on every message of every
  kind and on the heartbeat, `agent`, `kind`, `options`, `reply_to`, the action record,
  `canonicalBytes`, and `shared/mailbox-schema/vectors.json`), **P6.4** (rules generation 3 — the
  same fields in the template, the `kind` / parent-kind / MAC-shape macros, and
  `config/admin/devices/$uid`, which `USER` now requires) and **P6.5** (`shared/prompts` — the
  queue, `PROMPT_TTL_MS`, the interrupt table). **Approved and not built:** **P6.6** (`fugaro pair`
  — the tokens, the device node, and the OS keychain entry the pairing key lives in), **P6.7** (the
  consumer's enforcement of the D128 amendment — this command's inbound gate, the action record,
  the ledger and the expiry), **P6.12** (the phone's spoken confirmation, which consumes the merged
  confirmation machine) and **P6.13** (the sweeper's rule for stored prompts). So the MAC **can** be
  computed today and the **key cannot**: no pairing exists until P6.6 runs. What that means for
  this command's startup is §8.3's first row, and §8.4 says **which half of the MAC work is
  P4.1's and which is P6.7's**. A module existing and a task being *built* are not the same claim,
  and this bullet is held to the tree by the pin (`TestMailboxPlanPhaseSixStatusIsTrue`).
- **`shared/`'s tests have no CI job *running* — the job itself is written and staged.** P4.3 is
  owner-made (no agent edits `.github/workflows/`, Global Constraint 8), so the job is committed as
  **`docs/pending-workflows/shared-tests.yml`** for the owner to copy in by hand, with the README
  beside it, and `internal/cli/docs_shared_tests_workflow_test.go` pins the staged file's triggers,
  permissions, action commits and the state the README describes. **Until the owner copies it in,
  nothing runs the shared package's tests in CI** — the file is a proposal in the tree, not a job.
  That is why every verification in this plan is a **Go** verification, and why the MAC and
  action-record contracts are replayed from **committed vector files** rather than from a Node
  subprocess (Ruling M9); §16 and Ruling M9's decision 4 say what the copy-in does and does not
  change for this plan.

Related: [mobile/implementation-plan.md](../mobile/implementation-plan.md) (P4.1 — this plan's
authority — and P6.6, P6.7 and P6.13, whose halves §8.4 and M8 draw), [mobile/reconciliation.md](../mobile/reconciliation.md)
§3 (D110–D129) and §4 (the resolved tensions), [mobile/functional-spec-reconciliation.md](../mobile/functional-spec-reconciliation.md)
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

**Reused, never forked.** Rules generation 3 and the merged shared modules are the substrate. This
plan adds no rule of its own about what a message is, what a duplicate is, what a lease is, what a
tag covers or what a window is; where the Go side needs one of those answers it **binds** the module
that owns it.

| What it needs | Where it already is (verified on this branch, which contains `main`) | How P4.1 uses it |
| --- | --- | --- |
| The message schema, its kinds and its action record | `shared/mailbox-schema/index.js` — `FIELDS`, `REQUIRED_FIELDS` (`['id','sender','text','at','mac']`), `KINDS`, `ACTIONS`, `DIRECTIONS`, `validateMessage`, `validateHeartbeat`, `isMessage`, `canonicalBytes`, the bounds (`ID_MAX`, `SENDER_MAX`, `TEXT_MAX`, `AGENT_MAX`, `AGENTS_MAX`, `OPTIONS_MAX`, `SHA_MIN`, `MAC_MAX`, `BOOT_MAX`) | The Go reader refuses a record `validateMessage` refuses, with a code of its own; §8.1 |
| The tag, and the vectors that are its contract | `shared/mailbox-schema/mac.js` — `macOf`, `verifyMac`; `shared/mailbox-schema/vectors.json` (24 cases, three `direction_pairs`, the cross-direction reflections, and 31 `refusals`) | **Committed, so the Go side replays it directly** (M9): the bytes, the tag and whether it verifies, case by case, with no Node |
| The mailbox state machine: new / duplicate / late / replay, the act step, the ledger cap | `shared/mailbox/index.js` — `initialState`, `step`, `STATE`, `EVENT`, `KIND`, `STATUS`, `OUTCOME`, `REFUSAL`, `INPUT_REFUSALS`, `TRANSITIONS`, `DEFAULT_WINDOW_MS` (6 h), `MIN_WINDOW_MS`, `MAX_WINDOW_MS` (7 d), `MAX_PROMPT_TTL_MS` (30 d), `DEFAULT_MAX_SEEN` (1024), `SENDER_SHARE`, `defaultMaxPerSender`, `DEFAULT_FUTURE_TOLERANCE_MS` | Ruling M9: the corpus is generated from this module and replayed by the Go tests — it is the one merged module **without** a committed vector file |
| The consumer lease | `shared/lease/index.js` — `CLAIM_TTL` (10 min), `MAX_CLOCK_SKEW` (5 min), `MAX_TTL` (24 h), `MAX_HOLDER_LENGTH` (256), `LEASE_CODES`, `validateLease`, `emptyLeaseState`, `takeLease`, `readLease`, `releaseLease` | Ruling M1: the lease's **rule** is bound; the **store** is the heartbeat node |
| The compare-and-swap duty | `shared/lease/README.md` ("two consumers that both read the same state and both get `ok: true` from `takeLease` must not both be able to write"; CAS is the store's fence) | Ruling M1: an RTDB ETag conditional write is that fence |
| The conditional write itself | `internal/rtdb/client.go` — `GetETag` (it sends `X-Firebase-ETag` and returns the tag), `PutIfMatch`, `ErrPrecondition` (a 412 answer) | The fence M1 rests on already exists in this repository's client |
| Presence, and the heartbeat's `boot` | `shared/presence/index.js` — `evaluatePresence`, `STATE`, `REASON`, `REFUSAL`, `DEFAULT_INTERVAL_MS` (60 s), `DEFAULT_MISSED_INTERVALS`, `DEFAULT_OFFLINE_INTERVALS`, `MAX_BOOT_ID_LENGTH` (64) | The heartbeat this command writes is the value `evaluatePresence` reads (§4.2) |
| The prompt queue, the TTL and the safe answer | `shared/prompts/index.js` — `PROMPT_TTL_MS` (72 h), `EXPIRED_REPLY`, `QUEUE_FULL_REPLY`, `initialQueue`, `enqueue`, `expire`, `answer`, `pending`, `announcement`, `evaluateInterrupt`, `SWITCH` | Ruling M3: the **TTL is the module's number, not this command's**; the pending file's shape is the module's queue |
| The redactor (D118 requires it before anything is written) | `shared/digest/index.js` — `redact`, `REDACTED`, `redactFunc`, `redactForms`, `digest`, `SEVERITY`, `moreSevere`, `mostSevere` | Every field this command writes to its own state or prints passes it first |
| The simulator, the fault model and the canonical schedule line | `shared/mailbox-sim/index.js` — `createSimulator`, `scheduleFromSeed`, `faultFor`, `scheduleText`, `parseScheduleText`, `replyId`; its README §"The fault model, which is data", §"What the actors do" | The corpus's scenarios are its schedules; a failing Go case prints `scheduleText()` |
| The mailbox subtree, and who may write it | `internal/budget/rules/rules.json.tmpl`'s `mailbox` block, `config/admin/devices` and `pairing` — the **merged generation 3** — and the macros `MAILBOX` / `CONSUMER` / `userClaim` / `PAIRING` / `MACOK` / `KINDREQ` / `BOOTOK` in `internal/budget/rules/rules.go` | §2 below, whose quoted block `TestMailboxPlanQuotesTheRulesFile` derives from the file line by line, and whose grant claims `TestMailboxPlanMatchesTheMergedGrants` holds to it |
| The rules' emulator proof | `internal/budget/rules/emulator_mailbox_test.go` | The template's fence is already proven; this command does not re-prove it |
| A long-lived consumer's local state | `internal/watch/acks.go` — `AckPath`, `LoadAcks`, tmp+rename, `0o600` | The ledger and the pending-prompt file are written the same way (Ruling M7) |
| A lease-shaped claim, and its TTL | `internal/runstore/launch.go` — `Store.Claim`, `ClaimTTL` (10 min) | Not used directly: the launch claim is the runs bucket's. Cited as the shape this command's lease follows |
| A sweeper, its report and its delete-set discipline | `internal/budget/sweep.go` — `Sweeper`, `Sweep`, `SweepReport`, its keep set | Ruling M8: the hidden `mailbox sweep` is written in that shape, and it deletes the consumer's own state — **no `m~…` user** (that rule is P6.6's, in this same file, and is not built) and no `r~…` one |
| An owner-run hidden command | `internal/cli/history.go` — `newBudgetHistoryCmd` (`Hidden: true`) | The precedent for hiding the sweeper |
| Executing a cancel | `internal/cli/cancel.go` — `cancelRun` | The one existing destructive path the refusal fence (Ruling M6) must gate |
| A kill switch write, and a **conditional write on an ETag with a bounded retry** | `internal/budget/kill.go` — `SetKill` and its `SetAttempts` bound: "The write is conditional on the ETag read; a lost race is re-read up to `SetAttempts` times" | The other destructive path — and the in-repo precedent for Ruling M1's compare-and-swap |
| Spawning `gh` for a merge | `internal/watch/prstate.go` — `(*PRState).ask`: `procgroup.Run`, argv only, bounded buffers, nothing logged | The third, and the only precedent for "never through a shell" |
| The operator's credential (both the inbox read and the writes — C0) | `internal/cli/cloud.go` — `google.DefaultTokenSource`, `userErr`; `internal/rtdb`; `internal/gcpfake` | The database client; the one credential this command holds; and the fake the tests use |
| The project and its config block | `internal/localcfg` — the `watch:` block precedent; `internal/cli/project.go`'s `selectProject`, `internal/cli/cloud.go`'s `budgetDBFor`, `internal/cli/budget.go`'s `checkDBProject` and `dbErr` | Where the keychain **item name** lives (Ruling M5) |
| The generated CLI reference | `internal/clidoc/cliref.go` — `writeCLIReference`, `defaultPath` (`website/content/cli-reference.md`); the drift tests `internal/clidoc/cliref_test.go` `TestCliReferenceIsCurrent` and `internal/cli/docs_website_cliref_test.go` | §5: the one `root.AddCommand` line requires regenerating it |
| The exit-code contract and the command table | `internal/cli/root.go` — the `ExitOK` / `ExitUserError` / `ExitRemoteError` constants, `ExitError` and `ExitCode` (which maps any other error to `ExitUserError`), and `NewRootCmd`; `docs/design/v1.md` §9.1 | §3, and the classification §4.4 rests on: `dbErr` and `checkDBProject` in `internal/cli/budget.go` |

**No existing behaviour changes — but the change is not only new files.** Two existing Go files are
edited, and each edit is small and named: `internal/cli/root.go`, by **one line** (T5's
`newMailboxCmd()` in the `root.AddCommand` list, §5); and **`internal/localcfg`**, where T2 adds the
**keychain item name** — a field on the existing config struct (`localcfg.go`'s `Config`, beside
`Watch`, `localcfg.go:143`) with its validation, **and its layered merge** in `localcfg/shared.go`
(the `if out.Watch == nil { out.Watch = published.Watch }` precedent at `shared.go:115`, which a new
block must join or a shared config's item name would be silently dropped). Two generated and
hand-written documents follow the command: `website/content/cli-reference.md` (regenerated) and
`docs/design/v1.md` §9.1 (one row). Everything else T1–T8 writes is new files. The owner sizing this
change should count three Go files, not one.

---

## 2. What the merged schema and rules already decide

Read from the template as it is on this branch (`internal/budget/rules/rules.json.tmpl` — the
`mailbox` subtree, `config/admin/devices` and the new `pairing` subtree), and from the macros at
`internal/budget/rules/rules.go`. **This is not a proposal; it is what is merged, and P4.1 may not
contradict it.** Two tests hold this section: `TestMailboxPlanQuotesTheRulesFile` **derives every
line of the quote below from the file** (and proves the comparison can fail, by mutating a copy of
the template in a temp directory), and `TestMailboxPlanMatchesTheMergedGrants` parses the file and
holds the grant claims to it. So a later rules change is a failing test rather than a stale
paragraph.

```jsonc
"$mb": {
  "inbox": {
    "$msg": {
      ".write": "MAILBOX && newData.exists() && !data.exists()",
      ".validate": "newData.hasChildren(['id', 'sender', 'text', 'at', 'mac']) && $msg.length <= {{.MaxKeyLength}} && KINDREQ",
      "id": { ".validate": "newData.isString() && newData.val().length <= {{.MaxKeyLength}} && newData.val() == $msg" },
      "sender": { ".validate": "STR && newData.val() == $mb" },
      "text": { ".validate": "newData.isString() && newData.val().length > 0 && newData.val().length <= {{.MaxTextLength}}" },
      "at": { ".validate": "TIMESTAMP" },
      "mac": { ".validate": "MACOK" },
      "agent": { ".validate": "AGENTID" },
      "kind": { ".validate": "newData.isString() && (newData.val() == 'message' || newData.val() == 'prompt' || newData.val() == 'answer' || newData.val() == 'action')" },
      "options": {
        ".validate": "PARENTKIND == 'prompt' && newData.hasChildren()",
        "$i": { ".validate": "OPTIONIDX && newData.isString() && newData.val().length > 0 && newData.val().length <= {{.MaxOptionLength}}" }
      },
      "reply_to": { ".validate": "newData.isString() && newData.val().length > 0 && newData.val().length <= {{.MaxKeyLength}} && (PARENTKIND == 'answer' || PARENTKIND == 'action')" },
      "action": {
        ".validate": "PARENTKIND == 'action' && newData.hasChildren(['id', 'verb', 'target', 'confirmation']) && (newData.child('verb').val() == 'merge' ? newData.child('head_sha').exists() : true)",
        "id": { ".validate": "newData.isString() && newData.val().length > 0 && newData.val().length <= {{.MaxKeyLength}}" },
        "verb": { ".validate": "newData.isString() && (newData.val() == 'cancel' || newData.val() == 'merge' || newData.val() == 'confirm')" },
        "target": {
          ".validate": "TARGETOK",
          "run": { ".validate": "RUNTARGET" },
          "repo": { ".validate": "REPOTARGET" },
          "pr": { ".validate": "newData.isNumber() && newData.val() >= 1 && newData.val() % 1 == 0 && newData.val() <= {{.MaxTimestamp}}" },
          "$other": { ".validate": false }
        },
        "head_sha": { ".validate": "(newData.isString() && newData.val().length > 0) && SHAOK" },
        "confirmation": {
          ".validate": "newData.hasChildren(['id', 'at'])",
          "id": { ".validate": "newData.isString() && newData.val().length > 0 && newData.val().length <= {{.MaxKeyLength}}" },
          "at": { ".validate": "TIMESTAMP" },
          "$other": { ".validate": false }
        },
        "$other": { ".validate": false }
      },
      "$other": { ".validate": false }
    }
  },
  "outbox": {
    ".read": "MAILBOX",
    "$msg": {
      ".write": "CONSUMER && newData.exists() && !data.exists()",
      ".validate": "newData.hasChildren(['id', 'sender', 'text', 'at', 'mac']) && $msg.length <= {{.MaxKeyLength}} && KINDREQ",
      "acked": {
        ".write": "MAILBOX && newData.exists() && !data.exists()",
        ".validate": "newData.isBoolean() && newData.val() == true && data.parent().exists()"
      },
      "$other": { ".validate": false }
    }
  },
  "heartbeat": {
    ".read": "MAILBOX",
    ".write": "CONSUMER && newData.exists()",
    ".validate": "newData.hasChildren(['at', 'boot', 'mac'])",
    "at": { ".validate": "TIMESTAMP" },
    "boot": { ".validate": "BOOTOK" },
    "mac": { ".validate": "MACOK" },
    "agents": {
      ".validate": "newData.hasChildren()",
      "$i": {
        ".validate": "AGENTIDX && newData.hasChildren(['id', 'name'])",
        "id": { ".validate": "AGENTID" },
        "name": { ".validate": "newData.isString() && newData.val().length > 0 && newData.val().length <= {{.MaxAgentNameLength}}" },
        "$other": { ".validate": false }
      }
    },
    "$other": { ".validate": false }
  }
"admin": {
  ".read": false,
  ".write": false,
  "devices": {
    "$uid": { ".read": false, ".write": false }
  }
"pairing": {
  "$uid": {
    "request": {
      ".write": "PAIRING && newData.exists() && !data.exists()",
      ".validate": "newData.hasChildren(['key', 'at'])",
      "key": { ".validate": "PUBKEY" },
      "at": { ".validate": "TIMESTAMP" },
      "$other": { ".validate": false }
    },
    "delivery": {
      ".read": "PAIRING",
      ".write": false
    }
  }
```

**What each group of lines decides — and it is more than "the fields".**

| The lines | What the merged rules decide | Where this plan stands on it |
| --- | --- | --- |
| the inbox `$msg` `.write` / `.validate` | the **phone** creates an inbox message (`MAILBOX`), create-only, with `mac` **required** and the kind/field table enforced (`KINDREQ`); a field the table does not name for that kind is refused | C1: there is **no inbox write for the consumer**, and no inbox `.read` anywhere |
| the outbox `.read` | `MAILBOX` — **the phone reads its own answers**, and no one else's | C2: the consumer's observable answer is the outbox message the phone reads |
| the outbox `$msg` `.write` | `CONSUMER`, create-only — a second write to one outbox id is refused | C3, and M10: the consumer's answers are create-only |
| the outbox `acked` | `.write` is `MAILBOX` — **the phone's** receipt, one boolean, `true` only, on an existing parent | C2: the consumer never writes it |
| the heartbeat `.write` | `CONSUMER && newData.exists()` — **the only replaceable node the consumer holds**, and the only one with no create-only clause | C3/M1: the heartbeat is where the lease lives |
| the heartbeat `.validate` | `['at','boot','mac']` — the holder identity, a timestamp and the tag | M1: `boot` is the holder; M4b: the heartbeat is signed |
| `config/admin` and `config/admin/devices/$uid` | `.read` and `.write` are **false** for every client — only an IAM credential (the operator's) reaches them, and `rules.go`'s `userClaim` now requires `config/admin/devices/<uid>` to exist before a user claim passes at all | C0: this is why the phone's claim is device-gated, and why the consumer's credential cannot be a phone token |
| the `pairing` subtree | the `p~` uid may write its own request once and read its own delivery; nothing else | P6.6's; this command reads and writes **no** pairing node |

`MAILBOX` = `userClaim && $mb == auth.uid`, where `userClaim` = `auth.token.mu == auth.uid` **and**
`config/admin/devices/<auth.uid>` exists (generation 3 added the second conjunct); `CONSUMER` =
`(auth != null && auth.token.mc != null && auth.token.mc == $mb)`. **`CONSUMER` may write the outbox
and the heartbeat and nothing else — in particular it may read no mailbox at all** (`CONSUMER` is a
write claim only: no `.read` in the subtree names it), and **nowhere in the tree does a `.read` grant
admit the consumer to the inbox**.

### C0 — One credential, named, because the whole argument below is an argument about grants

A grant analysis binds only the credential the grant is evaluated for. So this plan names the
writer explicitly, and it names it **before** the consequences:

| The access | The credential | What the rules say about it |
| --- | --- | --- |
| **Read** `mailbox/$mb/inbox/**`, `mailbox/$mb/heartbeat` (M1's ETag read) | the **operator's application-default credential** | The inbox has **no `.read` grant at all**, and the heartbeat's only `.read` is `MAILBOX` — the phone's. `CONSUMER` may read no mailbox at all. Both reads are therefore out of band, and the ADC is the only credential the CLI already holds |
| **Write** `mailbox/$mb/outbox/$msg` and `mailbox/$mb/heartbeat` | **the same operator's ADC** (§15 decision 1, and the ruling below) | The merged grants name `CONSUMER` for these paths, and **no task mints a token carrying `mc`** (the finding below). An IAM write bypasses `.validate` **and** the create-only clause |

**The lease's arbitration does not depend on a grant at all.** The CAS is the store's HTTP
`if-match` precondition (`internal/rtdb`'s `GetETag` + `PutIfMatch`, and `ErrPrecondition` on the
412), not a rules predicate. It is evaluated for whoever sends the request — an ADC write is still
refused when the ETag has moved — which is why M1's fence survives the credential question
entirely. T3's test is the evidence in this repository; a live project's is check 83's.

**§15 decision 1 — the consumer runs on the operator's ADC, and this plan decides it from the rules rather than leaving it unsaid.**
The rules name `CONSUMER` for the two writes, and the honest state of the tree is that **nothing
mints an `mc` token**: `internal/budget/token`'s minting is the launcher's and P6.6's files are
`internal/cli/pair.go`, `internal/budget/token/device.go`, `internal/budget/sweep_device_test.go`,
`internal/budget/sweep.go` and one `root.go` line, and P6.6's own text mints the **pairing** token
(`p~<random>`) and the **device** token (`m~<random>`, `mu = uid` — the phone's) — neither carries
`mc`. So the two honest options were to mint one or to use the ADC, and this plan takes the second,
with its meaning stated in full rather than implied:

- **What it means.** For this writer, the mailbox subtree's `.write` claims, its **create-only**
  rule and its `.validate` lists are **not enforced by the store**. "The outbox and the heartbeat
  and nothing else" is a rule in the consumer's own code — which is what D113b's own honesty note
  says in so many words ("an **admin** credential bypasses RTDB rules … for the **laptop** consumer
  'the outbox and the heartbeat and nothing else' is a rule in the consumer's own code, not a fence
  the database enforces").
- **What is therefore the fence, and it is tested.** M10's write set is a value in code with a test
  that a mutation must turn red; every value the consumer writes is validated against
  `shared/mailbox-schema` **before** it is written (a message the schema refuses is never written);
  and the create-only rule is enforced by the consumer's own ledger and by its reply-once logic.
- **What is not lost.** The grants still bind every **token-bearing** writer — the phone, and the
  phase-2 service identity D113b names — and `internal/budget/rules/emulator_mailbox_test.go`
  proves them. The tests in this plan mint an `mc` token through `internal/gcpfake`'s
  identitytoolkit fake and assert that the merged grants **would** bind it, so minting the
  credential later is a wire-up rather than a redesign.
- **What stays a finding for the owner** (not a plan's choice to make quietly): a task that mints a
  token carrying `mc == $mb` — a third minting step beside P6.6's two — belongs in **P6.6's CLI
  plan**, because that is where the token minter's role (`iam.tf`'s `minter`) is already reasoned
  about. Until it exists, the production write path is ADC (this decision) and the tests use the
  fake.

Three consequences follow, and they are the spine of this plan:

**C1 — The inbox is write-only, and has no read grant at all.** So the consumer's "ack of an inbox
message" **cannot be an inbox write**. There is no node to write and no grant that would admit it.

**C2 — The `acked` flag on the outbox is the *phone's* write**, one field on a message the consumer
already created, written when the phone has *spoken* the answer. It is the phone's receipt, not the
consumer's acknowledgement. **The consumer's record that it consumed an inbox delivery is the
ledger**, which is the consumer's own state (Ruling M7), and the observable signal to the phone is
the **outbox reply**.

**C3 — The heartbeat is the consumer's only mutable node, and its three fields are a lease plus a
tag.** `{at, boot}` is precisely a holder identity (`boot`, a string, and `shared/presence` caps a
boot id at 64 characters) plus a timestamp — with `mac` beside them and `$other: false` forbidding a
fourth child — so the consumer cannot store a lease anywhere else without a rules change.

**A correction the merged rules make, and it is why this plan exists.** The merged simulator models
the ack as **one write by the consumer into the inbox**, and says so in its own README (§"What the
actors do": "the ack being **one** write by the consumer … D113's phone-written ack is P3.6's, and
this simulator does not model two ack writers"). The merged rules make that write **impossible**.
Both are on `main`; they cannot both be the store's truth. Owner decision 2.

---

## 3. Rulings (settled here; each says what it costs if wrong)

### M1 — The lease **is** the heartbeat, taken by an ETag compare-and-swap. No fourth node.

The consumer takes the lease by reading `mailbox/$mb/heartbeat` with `internal/rtdb`'s `GetETag`
and writing `{at, boot, mac}` back with `PutIfMatch`; a returned `ErrPrecondition` (the 412 answer)
is a lost race, and it is retried a **bounded** number of times, never forced — the pattern
`internal/budget/kill.go`'s `SetKill` already uses ("the write is conditional on the ETag read; a
lost race is re-read up to `SetAttempts` times"). **The read runs under the operator's ADC, and it
has to** (C0's read row): the heartbeat's only `.read` grant is `MAILBOX` — the phone's — and
`CONSUMER` is a write claim that appears in no `.read` in the mailbox subtree, so its token would be
refused the `GET` the ETag comes from. The CAS's **arbitration is the store's `if-match`
precondition**, not a rules predicate, so it is evaluated for whichever credential sends the write
and is unaffected by that choice; and because the consumer writes under the ADC too (C0's second
row), the rules bind nothing on this path — which is why T3's test proves the fence itself (two
writers, one ETag, one winner) rather than only the grant.

`boot` is the holder identity; the lease's TTL is `shared/lease`'s `CLAIM_TTL` (10 min) and its skew
tolerance is `MAX_CLOCK_SKEW` (5 min), applied by `readLease`/`takeLease` — the module decides, this
command only supplies the value and the store. The module's own caller obligations are this plan's
too, and they are named where they bite: **`at` comes from one authoritative clock** (the server's
timestamp, D117 — never each consumer's own), **the state is replaced only by compare-and-swap** (the
ETag write is that fence), and **a holder stops acting by `claim.at + ttl − MAX_CLOCK_SKEW`** or
re-reads the lease before it acts (T3 tests the re-read).

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

### M3 — The 72-hour TTL is **`shared/prompts`' number**, bound here, not declared here.

P6.5 is merged, and `shared/prompts/index.js` exports `PROMPT_TTL_MS = 72 * 60 * 60 * 1_000` — the
owner's O5 answer, inside `shared/mailbox`'s 30-day ceiling — with `expired(prompt, now)`,
`EXPIRED_REPLY` ("no answer, expired") and `QUEUE_FULL_REPLY` beside it. So this command **binds**
the module rather than restating its number: the Go side compares `time.Duration(PROMPT_TTL_MS) *
time.Millisecond` with whatever Go constant it uses, and a test pins that the two are the same
number, so a future edit to either is a failing test rather than a silent divergence. The
module also fixes **what** expiring a prompt yields (the non-destructive answer, or a stop) and
**who** delivers it (P6.7, exactly once, ledger-protected); this command's part is the local
expiry sweep and the pending file (§6, §7), and the *store's* prompt deletion is P6.13's (M8).
**Cost if wrong:** prompts live 30 days or 6 hours and the owner's number is not the one enforced.

### M4 — The MAC covers **both directions of the wire**: every inbox message is verified **before** it reaches the ledger, every outbox message and the heartbeat are **signed before they are written**, and a consumer that cannot verify or cannot sign **writes nothing and acts on nothing**.

The requirement is two-sided in the plan's own words, and this plan reads both halves of the
sentence. `mobile/implementation-plan.md`'s P4.1 verification says: **"every message it puts in the
outbox carries the pairing MAC over its id, text and timestamp, with the key read from the OS
keychain by the item name its settings hold and never logged"** (D113b, option (a), confirmed
2026-10-17) — that is the **outbound** half — and
[functional-spec-reconciliation.md](../mobile/functional-spec-reconciliation.md) §3.5 states it as a
rule of the protocol: **"Who signs: the **phone** signs every inbox message it writes (P3.6 …); the
**consumer** signs every outbox message and the heartbeat"**. **One correction, and it is the merged
module's to make:** P4.1's sentence says the tag covers "its id, text and timestamp", which is
§3.5's older phrasing; what is merged is `canonicalBytes(record, direction)` — **every field except
`mac` and `acked`, plus the direction** (the owner's ruling of 2026-10-18 on PR #579, one field
beyond §3.5's list) — and the direction is one of `DIRECTIONS` (`inbox`, `outbox`, `heartbeat`). So
this plan follows the module and not the older sentence, and it says so here rather than quietly
covering three fields.

**P6.7 is the enforcement task, and it lists the same duties as its own extension of this command**
("first verifies the MAC of every inbox message, of every kind … signs every outbox message and the
heartbeat"). §8.4 draws the line between the two tasks from their own texts; the short version is
that **P4.1 lands the one function and both wire-ups** (because P4.1's own verification — "never
runs a destructive command from a voice-originated message" — is unenforceable without the inbound
gate), and **P6.7 owns the enforcement**: the per-kind clauses, the action record and its
confirmation, the ledger of ids, the expiry and the safe answer, the revoke. Neither half is
optional here, and both are SECURITY-SENSITIVE.

- **M4a — inbound, before delivery.** Every inbox message is MAC-verified **before** it is handed to
  the ledger, with the direction `inbox` supplied by the reader (never from the record — a stored
  `direction` is refused by the schema and by `canonicalBytes`). A message that does not verify is
  refused, recorded by code, and never acted on, and it consumes no `(sender, id)` slot — **and it
  therefore cannot be retired**: nothing records it, and the merged inbox grant refuses a delete as
  well as an overwrite, so every later pass re-reads and re-refuses it. That is a stated cost with a
  named remedy, in §7's ordering 2 and §16.
- **M4b — outbound, before the write.** Every outbox reply and every heartbeat write **carries a
  MAC** over the same canonicalisation and the same key, with the direction `outbox` for the reply
  and `heartbeat` for the heartbeat. A consumer that cannot sign **writes nothing**: it does not
  write an unsigned reply and it does not write an unsigned heartbeat, so it also does not take the
  lease (M1's write *is* the heartbeat). The write is refused with a code and reported, and the
  reply is owed again on the next pass — a reply the ledger has not retired is a reply still owed
  (M2), so the refusal is recoverable rather than a lost answer.

**"Cannot verify or sign" names three states, and the plan gives each its own answer** — an earlier
revision described the first and the third with the same words and named no trigger between them,
which left an implementer unable to satisfy §8.3's startup exit and T2a's per-write refusal at once:

| The state | What triggers it | What the command does | Where |
| --- | --- | --- | --- |
| **Unavailable** — the MAC layer cannot be computed **at all** | decided **once, before the loop**: the **pairing key** is not at the keychain item M5 names (absent, unreadable, or not of M5's shape). The *schema* half of this row is gone: P6.3 and P6.4 are merged, so the field, the canonical serialisation and the vectors all exist | **refuses to start**: one fixed sentence naming the item name and the failure, **exit 1, no pass run and nothing written** | M5, §8.3, §4.4's exit-1 row |
| **A message that does not verify** | the layer is available and this message's signature is wrong, absent or tampered | a **per-message refusal**: the code is recorded, the message is not handed to the ledger, the pass continues | M4a, §8.1, T2 |
| **A write that cannot be signed** | the layer is available and the key is loaded, and it is **this value** the signer errors on | a **per-write refusal**: nothing is written for that value, the code is recorded, the pass continues, and the reply is owed again next pass | M4b, §8.2, T2a |

The second and third are **outcomes of a running pass** and are exit **0** (§4.4); only the first is
exit 1. **Why the third is not a startup condition:** M5 reads the key **once**, so a *key read*
that fails is always the availability check above — what is left at run time is an error from a
signer that **is** present, which is a fault path (a key that no longer fits the algorithm, an OS
signing service that fails, or, in tests, an injected failing signer). T2a is the task that reaches
it, with the **injected failing signer** the review asks for, and no shipped build reaches it by
itself — which is exactly why the tests for the three rows are three tests and not one.

**One function, so the two halves cannot drift.** The canonical serialisation and the tag are
computed by **one** function (`internal/mailbox/mac.go`), used by `verify` on the way in and `sign`
on the way out, held to `shared/mailbox-schema`'s `canonicalBytes` and to `mac.js` by its
**`vectors.json`** (Ruling M9). A mutation that changes what is covered, or that signs with a
different key than it verifies with, must fail a test in **each** direction — T2's Red step for the
inbound half and T2a's for the outbound.

**Cost if wrong:** an unverified message acted on is a phone-shaped write making the orchestrator act
with no authentication at all — D128's and D169's whole subject — and an unsigned reply is a phone
reading an answer it cannot attribute. The two seams are the whole reason this task is
SECURITY-SENSITIVE.

### M5 — The pairing key comes from the OS keychain **by item name**, and neither the key nor the item name's value is ever printed.

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
- **Who puts the key there is P6.6** (approved, not built): it "generates the per-pairing MAC key
  into the OS keychain (the settings hold the item's name only, Voice D42(2))". Until it runs, the
  item is absent — which is §8.3's first row, not a bug.

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
confirmation record (D169's "typed action records executed by the consumer") that names the same
action id, is itself MAC-verified, and is re-validated against `shared/confirm`'s rules by a Go
implementation held to `shared/confirm/vectors.json` (P6.7's parity test — §8.4). **Absent that
record, the refusal stands.** P6.2's module `shared/confirm` (its rules and its `vectors.json`) is in
the tree, but nothing on `main` writes, mints or consumes a confirmation record — the phone's half is
P6.12's and the consumer's is P6.7's, and neither is built — so **the refusal is unconditional until
P6.7**, and that is
what P4.1's tests pin — including a mutation that removes the allow-list and must turn a refusal
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
(1024) with `defaultMaxPerSender`'s share rule; a full ledger is refused `dedupe-full` rather than
grown, which is the module's own rule.

**A message is deduped by its id even after a restart, and the ledger's ack is what stops the
replay.** The pending-prompt file (the queue the consumer is waiting to see answered) is a
**separate file in the same directory**, written the same way, because a waiting prompt is not a
delivery that was consumed; **its shape is `shared/prompts`'s queue** (P6.5, merged), so the entry's
`destructive`/`safe` classification and the escalation state are the module's. **Cost if wrong:** a
restarted consumer re-performs an action the operator's phone already asked for. The test in T4
kills the process mid-loop and restarts it.

### M8 — The command surface: one visible command, one hidden sweeper, `--json` everywhere it reports. And the sweeper's remit is **P4.1's own state, and nothing in the store**.

- `fugaro mailbox` — visible, long-lived, in the CLI reference. It selects the project and prints
  `project: <name> (GCP <id>)` first, like every cloud command (design §9.1).
- `fugaro mailbox sweep` — **hidden**, in the shape of `internal/budget/sweep.go` and the hidden
  `fugaro budget history` (`internal/cli/history.go`); it reports the presence verdict and expires
  the consumer's own waiting prompts.
- **What it may delete, exactly.** With P6.13's rule as the standard — the real sweeper "deletes
  stored prompts past D118's TTL — and nothing more", and **"the sweeper never answers the
  conversation"** — the P4.1 sweeper's delete set is **the consumer's own local state and nothing
  else**: the waiting prompts in its `pending.json` past 72 h (§6, §7) and its stale ledger
  entries. It **deletes no Firebase user at all** and **no store node at all**: the `m~`-keyed user
  sweep is **P6.6's** rule, in `internal/budget/sweep.go` (the history sweeper's `deleteUsers` walk,
  whose `RunUID` keys are `r~…` — runs bucket keys, not this command's), and the **stored-prompt
  deletion is P6.13's**, in this same `internal/cli/mailbox.go`, which is where P6.13 says its file
  is. So a mailbox sweep that found itself about to delete an `m~…` user, an `r~…` user or a node in
  the mailbox subtree must **refuse and report** it as a finding, never fix it here — that is T7's
  deletion-set test, and it is the rule this ruling exists to state rather than leave implied.
  **And the sweeper never answers**: expiring a prompt and delivering its safe answer is the
  consumer's, on its own clock, because a sweep can run while the consumer is down (P6.13's reason,
  P6.7's work — M3).
  Hidden because it is internal — and because a hidden command is not user documentation, which is
  exactly what `internal/clidoc`'s skip rule is for.

### M9 — Parity is proven by **committed vectors** where they exist, and by a data corpus where they do not.

Two of the three contracts this command must agree with already ship **vector files in the tree**,
and a Go test can replay them **with no Node, no network and no subprocess** — which is the strongest
form available here, and the reason this plan needs no generated artifact for them:

1. **The MAC and the canonical bytes** — `shared/mailbox-schema/vectors.json`: every accepted case
   carries the record, its **exact bytes**, its tag and whether it verifies; three `direction_pairs`
   hold one record under two directions; three cases are the cross-direction reflections; and 22
   `refusals` carry the value, the record it is judged as, and the code and field the validator
   answers. The Go side must reproduce the bytes **byte for byte**, compute the same tag, refuse
   every refused value, and verify exactly the cases the file says verify. The schema README names
   the Go traps explicitly and T1/T2 test each: **decode case-sensitively and refuse unknown
   fields** (`encoding/json` matches keys case-insensitively otherwise), **write HTML characters raw**
   (`SetEscapeHTML(false)`), **sort keys by code point** (byte order is the same order), and **take
   the direction from the node read, never from the record**.
2. **The action record** — `shared/confirm/vectors.json`, replayed by the Go implementation of
   `shared/confirm`'s rules that P6.7's own verification names (`TestConfirmParityWithShared`). This
   command's P4.1 half does not validate action records (M6: until P6.7 the refusal is
   unconditional), so this vector file is **P6.7's**, and it is named here because it is how the two
   halves of §8.4 line up.
3. **The state machine**, which is the one merged module with **no** vector file
   (`shared/mailbox` carries `index.js`, `index.test.js` and `mutation-proof.sh`). So it keeps a
   corpus: a JS test in `shared/mailbox/` writes `internal/mailbox/testdata/parity.json` — for each
   scenario the **input events**, the **state** the module produced, and the **outcomes** and
   **refusal codes** it gave — and a Go test replays it and compares outcomes and codes, plus the
   corpus's own hash, so a hand-edited corpus is a failing test rather than a passing one. The
   refusal codes are compared by their **own spelling** (`invalid-prompt-ttl`, `future-timestamp`,
   `replay`, `expired`, `dedupe-full`, `stale-pending`, `cas-conflict`, `bad-ttl`, …). A
   simulator-produced failing case prints `scheduleText()` and the seed.

**The Go binding's rule, stated so it cannot drift:** every value the two sides must agree on (the
tag's bytes; the state machine's outcomes and codes; the ledger's cap and share rule; the windows;
the TTL) is compared against the vector file or the corpus, and the Go code holds **no constant
those modules already own** — it holds only the constants that are the *command's* (the item name's
shape, the allow-list). **Cost if wrong:** the Go side and the phone's side disagree about what a
duplicate is, or about what a tag covers, and the disagreement is found in production. Owner
decision 4.

### M10 — What the consumer never writes.

One write to the outbox per answer (create-only, per the merged grant — and enforced in code, since
the writer is an ADC writer: C0), one write to the heartbeat per lease renewal, and its own state
files. **It never writes the inbox** (C1), **never writes the `acked` flag** (C2 — that is the
phone's), never writes `config/admin/devices` (the phone's pairing node is P6.6's, and the rules
deny it to every client), never writes the `pairing` subtree, never writes the runs bucket's
`agents` subtree, and never writes any path the merged mailbox subtree does not name. A test asserts
the write set, and a mutation that adds an inbox write must fail it.

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
| **0** | a pass completed, whatever it managed to read and whatever it refused: an **empty inbox**, a pass that **held no lease** (and so read nothing, §7), a pass whose every message was refused as a duplicate, a replay, expired, `stale-pending` or badly signed, and a pass whose **write** was refused as unsignable (M4b's per-write state — an outcome, not a failure to run). Refusals are outcomes and are reported in `refused[]` and in the `pass` counters | a per-message refusal is **never** an exit 1, **neither is reading no message at all** (the predicate below requires at least one), and **neither is a per-write signing refusal** — clause 4 wants an *action* refusal |
| **1** | **a run-level refusal**, all of it before or outside the pass loop: a bad flag (`--interval` below 1 s, a negative duration, an unknown project selector); no project selectable; no mailbox configured; a keychain item name absent or not of M5's shape; **the MAC layer's availability check (M4's table, §8.3) — there is no pairing key, so the command fails at startup** rather than looping over a mailbox it can do nothing with; **and the database or keychain refusing** — `dbErr`'s `rtdb.ErrPermission` branch (→ `adminDenied` for a write, a `userErr` for a read) and `checkDBProject`'s not-this-project's-database, which is a `userErr` too. **And** the one pass-level case: the predicate below | a remote failure — an outage, a transport error, anything `dbErr` sends to `remote(err)` — is 2, not 1 |
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
allowance named and its P6.7 dependency named; that **both directions of the wire are MAC-covered** —
every message is verified before it is acted on and an unverifiable one is refused and never acted
on, and every reply and heartbeat is signed before it is written, so a value it cannot sign is not
written (M4); that the command **stops at startup when there is no pairing key at the named keychain
item** (M4's first row, §8.3) rather than looping; that the pairing key is read from the OS keychain
by item name and is never printed; that the command writes only the outbox and the heartbeat, **with
the operator's own credential**, and that this makes the write set a rule in this command's code
rather than a fence the database enforces (C0); and where its own state is.

### 4.2 `fugaro mailbox sweep` (hidden)

```
fugaro mailbox sweep [--json] [--dry-run]
```

The sweeper P6.13 names — "the D118 mailbox sweeper P4.1 builds in `internal/budget/sweep.go`'s
shape" — with P4.1's half of its remit and nothing more (M8). At P4.1 it walks the mailboxes it is
configured for, expires **its own** waiting prompts past the TTL (M3, §6's `pending.json`) — it
holds no Firebase user keys and deletes none, and it writes nothing in the store — and **reports**
the presence verdict: computing `shared/presence`'s `evaluatePresence` over the heartbeat and
printing it, **writing nothing**. That is not an omission: `evaluatePresence` is a **pure function
over a heartbeat**, no node is designated to hold its output, the merged template has **no grant
reaching any presence path**, M1 rules out a fourth node, and §6 therefore lists the verdict as
computed and never written. The verdict in the report is for the operator's eyes and for a later
task to place, if the owner ever wants it stored. **It never answers the conversation** (P6.13) and
**it deletes no stored prompt at P4.1** — there are none, because the consumer writes no prompt
until P6.7 — so the deletion P6.13 adds is "**stored prompts past D118's TTL and nothing more**", by
that task's own text, in this same file. `--dry-run` prints the plan and writes nothing. Hidden
from the CLI reference (M8).

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
| 1 | **a run-level refusal**, before or outside the pass loop: a bad flag or combination (`--interval` below 1 s); no project selectable; no mailbox configured; a keychain item name absent or not of M5's shape; **the MAC layer's availability check (M4's table, §8.3) — no pairing key at the item, refused at startup with no pass run**; **and the database or keychain refusing** — `dbErr`'s `rtdb.ErrPermission` branch, `adminDenied` for a refused write, `checkDBProject`'s not-this-project's-database (**all three are `userErr`**) | `userErr` / `ExitUserError`, and the flag and config refusals the command raises itself |
| 2 | the database could not be **reached or written** — an outage or a transport error, which is all `dbErr` sends to `remote(err)`; and the new keychain package's unopenable-backend case, which T2 must classify the same way | `remote(err)` / `ExitRemoteError` |

**Both rows are the merged functions' own behaviour, line for line** (`internal/cli/budget.go`):
`dbErr(lc, err, write)` sends `rtdb.ErrPermission` **with** `write` to `adminDenied` and **without**
it to a `userErr` — both exit 1 — and sends **everything else** to `remote(err)`, exit 2.
`checkDBProject` raises its own `userErr` for a malformed, absent or foreign `/fugaro/project` and
**delegates a read failure to `dbErr(lc, err, false)`**, so a permission refusal there is exit 1 and
an outage is exit 2. The command opens the database the same way (`openBudget`), which is why both
functions are on its startup path.

**The second exit 1, and the only one that is a pass-level outcome**: a `--once` pass that read at
least one message, performed no action, refused every message it read, and refused at least one
**action** — M12's four clauses — is also **1**, decided by that predicate, which is the command's
single exit branch. This is the row M12 spends its detail on, and it is the same code as the
run-level refusal because it is the same answer to the operator: nothing happened.

**The two per-value MAC failures are not exit 1, and they are not M12's clause 4 either.** A message
that fails verification (M4a) and a write that cannot be signed (M4b) are each **recorded outcomes
of a completed pass**: the pass keeps running, the code lands in `refused[]`, and the exit is 0 —
even when the pass acted on nothing, because clause 4 asks for an **action** refusal and neither of
these is one. Reading them as run-level refusals is the error M4's table exists to prevent: what is
run-level is the **availability** check, which happens once and before any pass (above), while these
two need a pass to have started at all.

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
4. **No `docs_bucket_iam_test.go` pressure — and no guard either way.** That test scans for IAM
   claims in `../../docs/*.md`, `../../docs/design/*.md`, `README.md`, `SECURITY.md` and the plugin
   skills; its globs stop at `docs/`, so they **never reach `docs/plans/*.md`**, which is this file's
   directory. This plan therefore adds no IAM sentence anywhere near the bucket as a **duty**, and
   nothing would go red if it did. The precise gap: **the pin in §13 is the only test that reads
   this document** (via `readDoc`, and §13 describes it), and it checks citations, the derived rules
   quote, the four-clause exit rule and the three MAC states — **not the IAM prose**, and no other
   test looks for an IAM claim inside `docs/plans/*.md` either, for its presence or for its absence.
   So the check that is missing is exactly that one, and the duty rests on the pin's citations and on
   T8's checklist line rather than on a guard.
   (An earlier draft made T7 the proof, which was wrong twice over: T7's verification filters on
   `-run 'Sweep|Expiry'`, so it does not run those guards at all, and a guard whose globs stop at
   `docs/` could not prove anything about a file in `docs/plans/` in any case.)

---

## 6. Where state lives

| State | Where | Who may write it | Written how |
| --- | --- | --- | --- |
| The lease, and presence | `mailbox/$mb/heartbeat` = `{at, boot, mac}` (and `agents`, which is P6.7's to advertise — D161) | the consumer, under the **operator's ADC** (C0) — the phone **reads** it, and the consumer's own read of the ETag is an **ADC** read for the same reason: no grant admits it | ETag conditional write (M1); `boot` names the holder; **signed** with direction `heartbeat` (M4b) |
| The consumer's answers | `mailbox/$mb/outbox/$msg` = `{id, sender, text, at, mac}` (+ `kind`, `reply_to`, `agent` as the message needs) | the consumer, under the same ADC | create-only **in code** (C0: an ADC write is not fenced by the grant); **signed** with direction `outbox` (M4b) |
| The phone's receipt that an answer was spoken | `mailbox/$mb/outbox/$msg/acked` | **the phone** (`MAILBOX`) | not this command's (C2) |
| The dedupe ledger, the actions performed, the window state | `$XDG_STATE_HOME/fugaro/mailbox/<project>/ledger.json` | the consumer | tmp+rename, `0o600` — the *prefix* and the discipline are `internal/watch/acks.go`'s (`AckPath` is the single file `…/fugaro/watch-acks.json`); the `<project>` level is **new here** (M7) |
| Prompts waiting for an answer | `$XDG_STATE_HOME/fugaro/mailbox/<project>/pending.json`, holding `shared/prompts`'s queue | the consumer | same; the entry's shape is P6.5's (M3, M7) |
| The keychain **item name** | the project's local config block (`internal/localcfg`) | the operator | plain config, **not secret**, and it may be printed (M5) |
| The **pairing key** | the OS keychain, read by that item name (P6.6 generates it there) | the OS, then never written | read once per pairing, memory only, never printed (M5) |
| The **presence verdict** | **nowhere** — computed, never written | — | `shared/presence`'s `evaluatePresence` is a pure function and **no node and no grant** is designated for its output; the sweeper prints it and writes nothing (§4.2) |
| A stored prompt past the TTL | `mailbox/$mb/**`, deleted by the **owner-run sweeper** | **P6.13's** rule, in this command's file, "and nothing more" | out of P4.1's remit (M8): at P4.1 there is nothing stored to delete |

**Nothing here is stored in the runs bucket**, and the sweeper's `keep` set holds no `m~` key
today — `internal/budget/sweep.go`'s `deleteUsers` and `RunUID` are the runs bucket's (`r~…`), and
extending the sweeper to `m~…` is **P6.6's** (approved, not built), not this plan's. If the
sweeper deletes an `m~…` user before P6.6 lands, that is a **finding, not a silent fix** — §16's
rule makes it one, T8's review checklist carries it as a line (a `go test -run 'Sweep'` cannot see it;
the store's own state is the only evidence), and it goes in the PR body.

---

## 7. The loop

```
        ┌─ read heartbeat (ETag) ────────────────────────────────────────┐
        │  takeLease(readLease(hb, now))  → not ok: write nothing, act on nothing, sleep
        └─ ok → write heartbeat {at: now, boot: bootId, mac} if-match ETag ─┘   (412 → lost race → sleep)
                                    │
        read inbox (operator credential) │ for each $msg, oldest first
                                    ▼
        validateMessage → MAC verify (M4a, direction 'inbox') → step(state, {type:'deliver', now, message, kind})
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
                             → SIGN the reply (direction 'outbox'), then write it (M4b/T2a)
                               — cannot sign: write nothing
        stale-pending        → perform nothing, sign and write the reply saying so (M2, M4b)
        refused              → write nothing to the outbox; record the code; log the code, never the text

  the heartbeat, every renewal: SIGN {at, boot, mac} as direction 'heartbeat' … then write it
  (M4b/T2a) — cannot sign: no write, and since the heartbeat write IS the lease, no lease: the
  pass acts on nothing (§8.2)

  the loop above starts only after M4's first row: a command with no pairing key at the item never
  gets here (it refuses to start, §8.3). Both "cannot sign" notes above are M4's third row — the
  key is loaded and the signer errored on this value — and both are per-value refusals inside a
  running pass, not the startup exit.
```

**The three orderings that are load-bearing**, and each is tested with a mutation:

1. **The ledger is committed before the action is performed**, so a consumer that dies between the
   two re-performs nothing after the restart (T4's kill-and-restart test).
2. **The MAC is verified before the delivery reaches the ledger**, so a message that fails
   verification is not in the ledger, is not acted on, and does not consume its `(sender, id)`
   slot — a badly signed message cannot deny service to the genuine one that shares its id, which is
   the property that costs the re-read below (T2's mutation).
   **And that is a cost, stated:** nothing retires an unverifiable message. The ledger holds no slot
   for it (this ordering, by design), and the merged inbox grant is **create-only** — a delete is
   refused exactly as an overwrite is (§2's `MAILBOX && newData.exists() && !data.exists()`) — so
   neither the consumer nor the phone can remove it. **Every pass therefore re-reads it and
   re-refuses it for as long as the node exists**, and it counts in `pass.read` and in `refused[]`
   each time. The only thing that removes it today is the operator's ADC, out of band and by hand —
   the credential the inbox read already uses (§2's C0) and the one the rules cannot fence. The
   alternative (record the `(sender, id)` of a bad-MAC message as a tombstone) is refused because it
   would consume the slot the genuine message needs, which is the denial of service this ordering
   exists to prevent; a store-side delete landlord would be a rules change and a Phase 2 re-open.
   **This is a known cost of the merged design, not a bug this plan introduces** — §16 and T8's
   checklist carry it, with one runbook line: an operator removes a parked bad message by deleting
   `mailbox/<mb>/inbox/<id>` with their own credential, out of band.
3. **The MAC is applied before the write**, so an unsigned reply or heartbeat never reaches the
   store at all — which is what makes "the phone saw it" imply "the consumer signed it" (T2a's
   three mutations, one of them on the heartbeat).

**A message the command cannot parse is refused once and not retried for ever.** The ledger records
it as refused with its code, the pass is counted, and the next pass does not re-read it as new;
`input-refusals` are the module's, per its `INPUT_REFUSALS`. **This is the parse class only, and the
difference matters:** a message the schema accepts and the MAC rejects takes **no** ledger slot
(ordering 2 above), so it is the opposite — it is re-read and re-refused on every pass until someone
removes the node. The two classes are not the same and the plan does not pretend they are.

---

## 8. The MAC, precisely: **verifying in** (§8.1), **signing out** (§8.2), the three states (§8.3), and **the split with P6.7** (§8.4)

### 8.1 Inbound — what is verified, in the order the command does it

1. **The message is what the schema says it is** — `validateMessage`'s rules, replayed from
   `shared/mailbox-schema/vectors.json` (T1): the field set, the kind, the `(kind, field)` table, the
   bounds, the action record's shape. The Go decoder is **case-sensitive** and refuses an unknown
   field (the schema README names this trap; `encoding/json` would otherwise fold `Text` into
   `text`), and it computes the canonical bytes **from the decoded map**, never from a re-marshalled
   struct. A record the schema refuses is refused with a code of its own.
2. **The MAC covers every field except `mac` and `acked`, plus the direction** — `canonicalBytes`'s
   own set (the owner's ruling of 2026-10-18; §3.5's older "id, text and timestamp" is superseded by
   the merged module), over JSON with keys sorted by code point, absent fields omitted, numbers as
   integers, and `HTML` characters written **raw** (`SetEscapeHTML(false)`). The direction is
   **supplied by the reader** — `inbox` here — and never taken from the record: a stored `direction`
   is refused by the schema and throws in `canonicalBytes`.
3. **The key is the pairing's**, from the OS keychain by item name (M5), read once.
4. **The timestamp's future tolerance is 5 minutes** (`MAX_CLOCK_SKEW`, `shared/mailbox`'s
   `DEFAULT_FUTURE_TOLERANCE_MS`) — one millisecond beyond it and the delivery is refused
   `future-timestamp`.
5. **Failure is a refusal with a code, never an error that stops the loop**, never a retry that
   never ends, and never a fallback to acting. This is the **per-message** failure of a layer that is
   available (M4's table): a message that does not verify never reaches the unavailability case,
   which is decided once at startup (§8.3) and leaves no pass to refuse anything in. **"Never a
   retry that never ends" is true of the *pass*, not of the message**: the pass ends, and the next
   pass reads that same message again and refuses it again for as long as the node exists, because
   nothing can retire it (§7's ordering 2, M4a). The retry that does end is the deliberate one — the
   module's window and the ledger's slot — and an unverifiable message has no slot; the cost of that
   choice is the re-read, stated there and in §16.

### 8.2 Outbound — what is signed, and when the write is refused

The same function, the same key, the same covered fields, in the other direction (M4b):

1. **The value to be written is built first, then signed, then written** — `{id, sender, text, at}`
   plus whatever the message's kind carries (`kind`, `reply_to`, `agent`) and `mac` last, so what the
   MAC covers is exactly what lands.
2. **`sign` and `verify` are the same function**, held to `shared/mailbox-schema`'s `canonicalBytes`
   and `mac.js` by `vectors.json`: a signature the command produces must verify under the command's
   own `verify`, and a test asserts that round trip **and** that the covered field set is the same
   one in both directions — the difference being the direction argument alone. A mutation that adds
   or drops a field from the covered set must fail in **both** directions (T2 and T2a are two Red
   steps, not one).
3. **Both outbound families are signed**: the outbox reply (direction `outbox`), and the heartbeat
   (direction `heartbeat`) — the heartbeat's `{at, boot, mac}` (and its `agents` list, once P6.7
   advertises one) carries a MAC too, so a minted token cannot forge the presence a `MAILBOX` reader
   trusts (§2's residual note, and `shared/presence` reads it).
4. **A write the command cannot sign does not happen.** No unsigned reply, no unsigned heartbeat —
   and since the heartbeat write is the lease (M1), a consumer that cannot sign also cannot hold the
   lease, so it acts on nothing. It reports the refusal with a code (never the message's text) and
   retries the reply on the next pass, because a reply the phone has not spoken is still owed (M2).
   **This is the per-write failure of a layer that is available** — the key is loaded (M5 reads it
   once, at startup) and it is this value the signer errors on. It is not §8.3's unavailability,
   which is decided before the loop and never reaches a write; T2a reaches this path with an
   **injected failing signer**, because no shipped build reaches a signer error by itself.
5. **The same is true of every other write**: the ledger and the pending-prompt file are local
   state, not wire messages, and they are **not** MAC-covered — but they are written with the
   redactor (D118) and at `0o600` (M7).

### 8.3 The three states, and the one that stops the command at startup

**The schema and the rules are merged; the key is not.** P6.3 and P6.4 landed, so `mac`, the
canonical serialisation, the vectors and the rules that require the field all exist — but
**P6.6 does not**, and P6.6 is what "generates the per-pairing MAC key into the OS keychain". So
the state in which this command cannot verify or sign at all is now exactly one thing: **there is
no pairing key at the item M5 names** — absent, unreadable, or not of M5's shape. In that state it
**refuses to start with exit 1, runs no pass and writes nothing**, in one fixed sentence per reason
naming the item name and never its value. That is the first row of M4's table, and it is what an
operator sees from the day P4.1 lands until P6.6 runs.

**With the availability check firing, there is no pass**, so §8.1's per-message refusal and §8.2's
per-write refusal cannot occur — they are what the same code does once the check passes. T2 and T2a
test them with the key loaded from a **fake keychain**, and T2a reaches the per-write state with an
**injected failing signer**, the only way to reach a signer error when the layer is available
(otherwise the whole third row would collapse into the first). The tests prove all of it: the
inbound refusal, the outbound refusal, the two mutations that must fail one each, and **the startup
exit** — with the startup case and the two per-value cases asserted as three separate tests, because
they are three different states of the world (M4's table).

### 8.4 The split with P6.7 — what P4.1 lands, and what the enforcement task owns

Both tasks describe MAC work, and this is where their own texts divide it. Nothing here is a
negotiation: each row quotes the task that names it.

| The work | Whose | The task text it comes from |
| --- | --- | --- |
| **Signs every outbox message and the heartbeat**, with the key read from the OS keychain by item name and never logged | **P4.1** | P4.1's verification: "every message it puts in the outbox carries the pairing MAC over its id, text and timestamp, with the key read from the OS keychain by the item name its settings hold and never logged" |
| **The one function** — canonical bytes in Go, HMAC-SHA-256, `sign`/`verify` over it — held to `shared/mailbox-schema`'s `vectors.json` | **P4.1**, extended by P6.7 | P6.7's files are "`internal/cli/mailbox.go` and its tests (extending P4.1's consumer)" — the function is in `internal/mailbox/`, and P6.7 adds no second implementation |
| **The inbound gate: a message that does not verify is never acted on** | **P4.1 wires it; P6.7 owns it as enforcement** | P4.1's verification: "the consumer **never** runs a destructive command from a voice-originated message", which is unenforceable without refusing what it cannot attribute. P6.7's: "**first verifies the MAC of every inbox message, of every kind**, with the per-pairing key … an unsigned or wrongly signed message is refused before anything else" |
| **The per-kind clauses and the action record**: kind vocabulary, `options`/`reply_to` per kind, the action record's verb–target agreement, the confirmation record, the merge head SHA read-back, the destruction fence's allowance | **P6.7** | P6.7's: "Re-validates every action record with a **Go implementation** of `shared/confirm`'s rules … held to them by a parity test over `shared/confirm/vectors.json`" |
| **The ledger of used message, action and confirmation ids, and of prompts expired or answered, surviving a restart** | **P4.1 lands the delivery ledger** (M7); **P6.7 extends it** to action and confirmation ids and to expiry | P6.7's: "keeps a **ledger of used message, action and confirmation ids, and of prompts expired or answered, that survives a restart**" |
| **The pending prompts, the TTL, the safe answer delivered exactly once, the queue cap, the revoke** | **P6.7** (the module is P6.5's) | P6.7's: "persists its pending prompts locally (id, `at`, safe answer) … produces prompts that **expire at D118's TTL** … hands the orchestrator the prompt's non-destructive answer, or tells it to stop if there is none, **exactly once**" |
| **The heartbeat's `agents` list** | **P6.7** | P6.7's: "advertises its agents in the heartbeat's `agents` list (D161)"; P4.1 writes the heartbeat without it |
| **The outbox `acked` read, presence, the launch pass-through** | P4.1 reads presence (the sweeper reports it); the launch is a plain `message` and needs no confirmation (D162) | P6.7's: "a launch ('kick off …') is passed through without a confirmation record (D162)" |

**The residual, stated rather than implied:** between P4.1 and P6.7 the consumer verifies inbound with
the same function, the same key and the same direction, and refuses what does not verify — so no
build of this product ever acts on an unattributed message — but it does **not** yet know what a
verified message *means* (a kind, a confirmation, an action). That is exactly the interval P6.7
closes, and it is why P6.7 is written as an extension of this command rather than as a second one.

---

## 9. D128: the refusal fence

| The message asks for | The consumer's answer today | Why |
| --- | --- | --- |
| `fugaro cancel RUN` | **refused** | D128; F38 records that no argv table fences it |
| `fugaro budget kill` | **refused** | same |
| `gh pr merge` / an approve | **refused** | same; and the 2026-10-17 amendment's allowance is P6.2's and P6.7's |
| Anything not in the allow-list | **refused** | the default is refusal, not permission |
| A launch (`fugaro run`) | allowed | **D162**: launches need no confirmation |
| A read-only report (`ls`, `diagnose`, `report`) | allowed | nothing is changed |
| A destructive action **with** P6.7's confirmation record naming the same action id | allowed **then** | the 2026-10-17 amendment, through P6.2 (whose module and vectors are merged) and P6.7 (which is not built — it is the half that reads the record) |

The allow-list is a **value in code** (a slice of action kinds, each with the argv shape it may
spawn), and the refusal is evaluated **before** any spawn (M11). The refusal is **recorded in the
ledger and logged by code** — the message's own text is never put in an argv element and never
printed; the ledger holds ids and codes — and its **exit code follows M12 and §4.4**: **0** when
the pass did other work **or read nothing at all**, **1** only under M12's predicate:
**at least one message** read, **performed no action**, **every message** read refused, and
**at least one of those refusals an action refusal** — and not only per-message refusals. Owner
decision 3. The pin checks all four clauses here as well
as in M12 and §4.4, so a section that drifts on one of them is a failing test rather than a
paragraph nobody re-reads.

**The amendment is honoured as a seam and not as a behaviour**, because the record's *consumer* is
P6.7's and P6.7 is not built: P6.2's module (`shared/confirm`, with `vectors.json`) **is** in the
tree, so the re-validation table this command will use has a merged contract to be held to, but
nothing in the merged tree writes a confirmation record, produces one from a real message, or keeps
the ledger of used action and confirmation ids. So the code path here exists, is covered by a test
that builds a **synthetic** confirmation record, and **cannot be reached from a real message
today** — the refusal is unconditional until P6.7. Owner decision 3.

---

## 10. Expiry and the windows

| Question | Answer | Source |
| --- | --- | --- |
| How long is a prompt valid? | **72 hours** from `at`, computed by the consumer on its own clock (there is no `expires_at` field — the schema refuses every name for one) | D118, O5 answered; D168 **as amended** 2026-10-17 (a prompt expires at D118's TTL); `shared/prompts`' `PROMPT_TTL_MS` |
| What is a command's window? | **6 hours** (`shared/mailbox`'s `DEFAULT_WINDOW_MS`), at most **7 days** (`MAX_WINDOW_MS`) | §3.6, D173 (a command's 6-hour window) |
| What does expiring a prompt yield? | its **non-destructive answer**, or a **stop** when it has none (`EXPIRED_REPLY`) — delivered **exactly once**, by the consumer, never by the sweeper | D168 as amended; `shared/prompts`; P6.7 |
| What happens at the queue cap? | the prompt past the cap is **refused, never evicted**, with `QUEUE_FULL_REPLY` ("queue full") | `shared/prompts` (P6.5) |
| Delivered after the TTL | refused `expired`, never delivered at all | `shared/mailbox` |
| Delivered in time, acted on too late | consumed **once**, answered `stale-pending`, **nothing performed**, the reply still written | `shared/mailbox`; simulator README |
| A stale `at` beyond the window | refused `replay` for a command, nothing acted on | §3.6 |
| A timestamp in the future by more than the tolerance | refused `future-timestamp` | §3.5 (5 minutes) |

**Two windows, named apart.** **D118's TTL is 259 200 000 ms** — the number `shared/prompts` exports
as `PROMPT_TTL_MS` (72 × 3 600 000; M3) — and that is the **prompt** window. **D173's 6-hour window
is `shared/mailbox`'s `DEFAULT_WINDOW_MS`**, already in the module — and that is the **command**
window. If the Go side declares a duration for the TTL, a test pins that it equals the module's
number, so a future edit to one is a failing test rather than a silent divergence.

---

## 11. How it is tested over the simulator

Everything runs as `go test ./internal/...`, on the fakes this repository already has
(`internal/gcpfake`'s Realtime Database and identitytoolkit fakes), with **no Node, no network and
no clock** — the pass takes `now` as a value, exactly as `shared/mailbox` and `shared/mailbox-sim`
do.

| Layer | What it proves | Where |
| --- | --- | --- |
| The schema vectors (M9) | the Go side reproduces `canonicalBytes`'s bytes, `mac.js`'s tag and `validateMessage`'s refusals, case by case, from the **committed** `shared/mailbox-schema/vectors.json` | a Go replay test, no Node |
| The corpus (M9) | the Go binding gives `shared/mailbox`'s outcomes and codes for the module's inputs | `internal/mailbox/testdata/parity.json` + a Go replay test; a JS generator in `shared/mailbox/` |
| The simulator scenarios | the seeded fault schedules (duplicate, reorder, drop, delay, clock skew) produce at-most-once acting, at-least-once delivery and one lease holder | the corpus's `scheduleText()` lines replayed in Go |
| The store | the ETag CAS refuses the loser with **nothing written**; the ledger survives a restart; the write set is exactly the outbox and the heartbeat | `internal/gcpfake` |
| The fence | every destructive action is refused, before any spawn; the mutation that removes the allow-list turns a test red; the exit code is 0 for a pass that also delivered, 0 for a pass that read only per-message refusals, and 1 for one whose only reading was a refused action | a fake action runner that records what was spawned |
| The MAC, **inbound** | an unverifiable message is refused and never acted on; a verified one is delivered; a message verified in the **wrong direction** is refused; the mutation that skips verification turns a test red | a fake keychain, and the vectors' three reflections |
| The MAC, **outbound** | every outbox reply and heartbeat carries a signature over the same covered fields, in its own direction; the command's own `verify` accepts what its `sign` produced; a write it cannot sign **does not happen** (no unsigned reply, no unsigned heartbeat, no lease taken); the mutation that writes unsigned turns a test red | a fake keychain, **an injected failing signer** (M4's third row), and a recording store |
| M4's three states are three states | the availability check is a startup refusal with **no pass run** (T5); a message that fails verification is a per-message refusal inside a running pass (T2); a value the signer rejects is a per-write refusal inside a running pass (T2a) — and the third is unreachable without the injected signer, which is why the tests are three and not one | `internal/cli`'s `RunE` tests, `internal/mailbox`'s gate tests, and a signer that fails on demand |
| An unverifiable message is never retired | the node is still read on the next pass and refused again with its code; nothing the command can do removes it (M4a, §7's ordering 2) | `internal/gcpfake`'s Realtime Database, two passes |
| The **credential** | the reads and the writes are the operator's ADC (C0), so the command's own write-set check is the fence; and, separately, a token minted with `mc == $mb` through the identitytoolkit fake is **accepted** by the merged grants where the ADC is not proof of anything — so the day that credential is minted, the store fences what today the code does | `internal/gcpfake`'s identitytoolkit fake, and the emulator rules for the grant half (`internal/budget/rules/emulator_mailbox_test.go`) |
| The **sweeper's delete set** | it deletes the consumer's own expired state and **no store node and no Firebase user**; a sweep that would delete an `m~…` (or `r~…`) user refuses and reports it instead (M8); and it **never answers** a conversation (P6.13's rule, pinned as a test even though the answering is P6.7's) | a fake Auth that records every deletion; a recording store; T7 |
| The expiry | the 72-hour boundary, exactly, against the module's number; the 6-hour command window; the 5-minute future tolerance | injected `now` |
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

**Files.** `internal/mailbox/message.go` (new) + `internal/mailbox/message_test.go` (new); the
replay of `shared/mailbox-schema/vectors.json` (read-only: the file is merged and is not edited).
**Depends on.** P6.3 and P6.4 (merged), M9.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -count=1`: every accepted vector's record
validates, its **exact bytes** are reproduced byte for byte, its tag is reproduced and it verifies
exactly where the file says it does; the three `direction_pairs` produce the two different byte
strings and the two different tags; the three reflections (an inbox tag read as outbox, an outbox
tag read as inbox, a heartbeat tag read as outbox) **fail** to verify; and each of the 22
`refusals` is refused. The decoder is case-sensitive and refuses unknown fields, `SetEscapeHTML` is
off, and keys sort by byte — each asserted, not assumed. `go run ./internal/clidoc` is untouched by
this task.
**Red step.** (a) Fold `Text` into `text` (a case-insensitive decode) and watch the unknown-field
refusal fail; (b) turn HTML escaping back on and watch the bytes of the vector that carries `<`, `>`
or `&` differ; (c) drop the `direction` key from the covered map and watch the `direction_pairs`
case fail — the tag would then verify in both directions.
**Reviewer tier.** SECURITY-SENSITIVE — this is the protocol both ends of the mailbox agree on.

### Task T2: The MAC function, the keychain read, and the **inbound** gate — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/mac.go` (new) + `mac_test.go`; `internal/mailbox/keychain/` (new, with
a fake) + tests; `internal/localcfg`'s item-name field and its validation test.
**Depends on.** T1; **M4a**, M5; **§8.4** (this is the gate P6.7 owns as enforcement and P4.1 must
close).
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox/... ./internal/localcfg -count=1`: the
covered field set is exactly `canonicalBytes`'s (`mac` and `acked` out, the direction in), pinned by
the vectors; the inbound gate refuses an unsigned, wrongly-signed, tampered, wrong-direction or
future-stamped message and never hands it to the ledger; **a test asserts the cost M4a and §7 state —
the refused message is still in the inbox on the next pass and is refused again, with its code, and
nothing this command can do retires it**; a test asserts the key never appears in any output, any
error string, any log line or the JSON; a test asserts the item name **does** appear in the
diagnostic.
**Red step.** Make the gate return "verified" when the MAC field is absent and watch
`TestUnverifiableIsRefused` fail; and, separately, put the key into an error message and watch
`TestKeyIsNeverPrinted` fail.
**Reviewer tier.** SECURITY-SENSITIVE — a mistake here is a forged action, not a bug.

### Task T2a: The **outbound** signature, on the reply and on the heartbeat — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/mac.go` (the `sign` half of the same function T2 lands) +
`internal/mailbox/mac_sign_test.go`; the write paths in `internal/mailbox/` that put a value in the
outbox or the heartbeat; vector round-trip entries.
**Depends on.** **T2** (the same function and key), **T3** (the heartbeat write is the lease), T1;
**M4b**; §3.5's "who signs" and the owner's direction ruling.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Sign|Outbound|Heartbeat' -count=1`:
what `sign` produces, the command's own `verify` accepts; the covered set is the same one T2 pins, in
both directions, differing only in the direction argument; **both** families are signed (the outbox
reply with `outbox`, the heartbeat with `heartbeat`); a consumer whose **signer fails for this value**
writes **nothing** — no reply, no heartbeat, and therefore no lease taken (M1) — and reports the
refusal by code; the reply it could not write is owed again on the next pass rather than lost. **That
case is reached with an injected failing signer**, in a build where the key is loaded: it is M4's
third row, and the line between §8.3's startup exit and this per-write refusal is the whole reason
T2a cannot be tested by the shipped default. Swept over the simulator's seeded schedules, not one
call.
**Red step.** (a) Write the reply without a MAC and watch `TestOutboundIsSigned` fail; (b) swap the
key in `sign` (sign with one key, verify with the other) and watch `TestSignVerifyRoundTrip` fail;
(c) let the heartbeat be written unsigned when signing fails and watch
`TestUnsignedHeartbeatIsNotWritten` fail; (d) sign the heartbeat with the `outbox` direction and watch
`TestDirectionsAreNotInterchangeable` fail; (e) make the failing signer's error a startup refusal
instead of a per-write one and watch `TestSigningFailureIsAPerWriteRefusal` fail — the injected
signer is only reachable if the availability check has already passed, and this step is what keeps
M4's second row and third row from collapsing into the first.
**Reviewer tier.** SECURITY-SENSITIVE — an unsigned reply is a phone reading an answer it cannot
attribute, which is the same class of exposure as the inbound half.

### Task T3: The lease on the heartbeat, by ETag CAS — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/lease.go` (new) + `lease_test.go` (new);
`shared/lease/parity.test.js` (new) + corpus entries.
**Depends on.** T1; P1.2 (merged); M1; C0.
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Lease' -count=1` over
`internal/gcpfake`: the read uses `GetETag` **under the operator's ADC** (C0) and the write
`PutIfMatch`; two consumers read one empty heartbeat, both get `ok: true` from `takeLease`, the
second write is refused `ErrPrecondition` and **nothing moves**; the retry is bounded (the loser does
not spin); a lease 5 minutes ahead is held and 5 minutes and 1 ms ahead is taken over; a holder
re-reads the lease before it acts past `claim.at + ttl − MAX_CLOCK_SKEW`; `--no-heartbeat` takes no
lease and acts on nothing.
**Red step.** Drop the `if-match` from the second write and watch `TestTwoConsumersOneLease` fail
with both writes landing. **The read half's mutation is not a template edit any more** — with the
consumer on the ADC the rules are not on this path at all (C0) — so it is asserted as the grant
claim it is: a token minted with `mc == $mb` is **refused** the heartbeat `GET` by the merged rules
(the emulator test's half), while the ADC read succeeds, which is why the lease's credential is the
one C0 names.
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
**Depends on.** T1–T4, T2a; D113c; M8, M12, **C0** (the credential the pass uses).
**Verification.** `go run ./internal/clidoc` then
`GOFLAGS=-p=2 go test ./internal/clidoc ./internal/cli -run 'CliRef|Docs|Lint' -count=1`, plus
`gofmt -l internal/cli` and `GOFLAGS=-p=2 go vet ./...`; a `--once --json` test asserts the document
shape, the `project` field, and that no message text and no key appears in it. **The exit contract is
tested here, on the command's own `RunE`, on both sides of every row of §4.4**: `--interval 500ms`
→ 1; no mailbox configured → 1; a malformed item name → 1; **§8.3 (no pairing key at the item) → 1 at
startup, with no pass run** — this is **M4's first row** (the availability check), and it is the only
MAC case that is a startup exit; a completed pass whose every message was refused as a duplicate →
**0**; a pass that held no lease → **0**; a pass over an empty inbox → **0** (both read nothing, so
M12's first clause fails and the exit is 0); a `--once` pass that read only per-message refusals and
performed no action → **0** (clause 4); a `--once` pass that read only a refused action and performed
no action → **1**; **and, with the key loaded from a fake keychain, a pass whose write the signer
refuses → 0** (M4's third row is an outcome, not a startup refusal — the assertion T2a's injected
signer makes possible and this task's own `RunE` test repeats).
**Red step.** Add the command to the tree without regenerating the reference and watch
`TestCliReferenceIsCurrent` fail with the regenerating command in the message; **and**, separately,
make the pass return `ExitUserError` on any refused message and watch
`TestRefusedMessageStillExitsZero` fail.
**Reviewer tier.** standard.

### Task T6: The refusal fence and the action runner — SECURITY-SENSITIVE, independent opus review

**Files.** `internal/mailbox/action.go` (new) + `action_test.go` (new);
`internal/cli/mailbox.go`'s runner wiring.
**Depends on.** T5; D128 and its amendment; M6, M10, M11; §8.4 (the part of the fence P6.7 extends).
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Refus|Action|Write' -count=1`: every
destructive kind is refused; the refusal precedes any spawn (the fake runner records that it was
never called); an unrecognised kind is refused; a launch is allowed; a synthetic confirmation record
admits the destructive path; the write set is exactly the outbox and the heartbeat, **and each of
those writes carries a MAC** (M4b, T2a's test); **the action-refusal exit code is asserted all three
ways** — 0 in a pass that also delivered something, 1 in a pass whose only reading was a refused
action, and 0 in a pass whose only readings were per-message refusals (§4.4, M12's clauses 3 and 4).
**Red step.** Remove `cancel` from the refusal list and watch `TestDestructiveIsRefused` fail;
**and** let the runner spawn before the fence is evaluated and watch
`TestRefusalPrecedesTheSpawn` fail.
**Reviewer tier.** SECURITY-SENSITIVE — D128 is the reason this command exists, and a refusal
table that can be walked around is not a fence.

### Task T7: The hidden sweeper, D118's TTL and the reported presence verdict

**Files.** `internal/cli/mailbox.go`'s hidden `sweep` command + `internal/cli/mailbox_sweep_test.go`;
`internal/mailbox/expiry.go` (new) + `expiry_test.go` (new).
**Depends on.** T4; T5 (the surface and the hidden-command rule); **M3** (`shared/prompts`' number),
**M8** and **§6** (the remit and the delete set); D118, D168 as amended, D173; `shared/presence`;
`shared/prompts` (**merged**). The store-side half of the sweeper is **P6.13's** and is not built
here — §6's last row and §8.4's table say which row is whose.
**What it does, and what it does not.** At P4.1 the sweeper's delete set is **the consumer's own
state**: `pending.json` entries and ledger rows past `PROMPT_TTL_MS` **plus one sweep interval**,
which is P6.7's bound for the same ledger. It **deletes no store node** — nothing under
`mailbox/$mb/` is in its set, so a stored prompt is not deleted here, because at P4.1 there is
nothing stored for this command to delete and the deletion of a stored prompt is P6.13's remit;
and it **never answers a conversation** (P6.13's own rule): the consumer expires a prompt and
delivers its safe answer, the sweeper reads and deletes. It computes and **reports** the presence
verdict and writes nothing for it, because no node and no grant is designated for one (§4.2).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/mailbox -run 'Sweep|Expiry' -count=1`:
`fugaro mailbox sweep` is **absent** from `website/content/cli-reference.md` (asserted, its own
test, the `newBudgetHistoryCmd` precedent); the expiry boundary is exact and the Go constant
**equals `shared/prompts`' exported `PROMPT_TTL_MS`**, with `259200000` written out as the
assertion — 72 h in ms, D118's number as amended by D168 on 2026-10-17 — so a change to either
side is a failing test; a prompt one millisecond inside the bound is live and one at it is expired;
the consumer, not the sweeper, is asserted to be what returns `EXPIRED_REPLY` on expiry (§10); the
**delete set is asserted to hold no path under `mailbox/`** against a recording store, and the
store is asserted **unchanged** by a sweep whose only work was a verdict
(`TestSweepWritesNoVerdict`); **the sweep deletes no Firebase user** — it runs against a fake Auth
that records every deletion, and the record is empty — and the set it would delete is asserted to
name **no `m~…` and no `r~…` key**, so the `m~` rule P6.6 owns (in `internal/budget/sweep.go`,
approved and not built) is **refused and reported** if it ever appears, never performed (M8);
**`TestSweeperNeverAnswers`** asserts no outbox write and no "answered" ledger row from a sweep
whose set held an expired prompt.
**Red step.** (a) Change the TTL to 6 hours and watch `TestPromptExpiresAt72h` fail (the assertion
is against `PROMPT_TTL_MS`, not against a literal — this is M3's mutation, and it is what proves
the plan did not declare the number of its own); (b) unhide the sweeper and watch the reference
test fail; (c) make the sweeper write a verdict node and watch `TestSweepWritesNoVerdict` fail;
(d) put one `m~…` user in the delete set and watch `TestSweepDeletesNoUser` fail; (e) make the
sweeper deliver a safe answer and watch `TestSweeperNeverAnswers` fail.
**Reviewer tier.** standard here — but **P6.13, which adds the store-side deletion to this same
file, is SECURITY-SENSITIVE** (the mobile plan's Global Constraint 14 list) and is reviewed as one.

### Task T8: The parity corpus and the review checklist

**Files.** `internal/mailbox/parity_test.go` (the replay, the hash check, **and the
regenerate-and-diff check**); `shared/mailbox/parity.test.js` (the generator, a **test file only** —
`shared/mailbox/index.js` is not edited by this plan); `internal/mailbox/testdata/parity.json`
(generated); `docs/mobile/mailbox-review-checklist.md` (new, short: the things a reviewer of T1–T7
must confirm, each with the test that proves it **or marked as a line no test in this repository
can prove** — the `docs/design/v1.md` §9.1 row, the absence of an IAM sentence in this plan (no
guard's globs reach `docs/plans/*.md`), the `m~…` sweeper question, the store-side deletion P6.13
owns, and the parked-unverifiable-message cost, which is behaviour a test asserts but an operator's
decision to accept).
**Depends on.** T1–T7; M9; `shared/mailbox` and `shared/mailbox-sim` (**merged**).
**Verification.** `GOFLAGS=-p=2 go test ./internal/mailbox -run 'Parity' -count=1`; 120 seeded
schedules with three messages, two consumers and twelve passes each, replayed in Go and compared
outcome by outcome; a failing case prints the seed and `scheduleText()`. The **committed vectors**
M9 leans on are `shared/mailbox-schema/vectors.json` (T1) and `shared/confirm/vectors.json`
(P6.7's, both merged), so what this corpus adds is `shared/mailbox`'s **outcomes and codes** — the
part with no vector file.
**The drift check, and exactly how much it is worth.** `TestParityCorpusIsCurrent` runs the JS
generator and diffs its output against the committed corpus **when `node` is on `PATH`**, and
**skips with a recorded reason when it is not** — which is the case in this repository's `test`
job today (no Node) and on this runner image (verified: `node --version` → `node: command not
found`). So the check catches drift **where it can run at all** (the owner's machine, and the P4.3
job once the owner adds it) and is otherwise **silent**: a `shared/mailbox` change under an
unregenerated corpus is caught by nobody until then. That is the real cost of the corpus design and
it belongs in the decision, not in a footnote (decision 4). The distinction the plan draws: the
**hash** detects a **hand edit** of the artifact (it travels inside it); only the **regeneration**
detects a **changed module**.
**Red step.** Hand-edit one outcome in `parity.json` and watch `TestParityHash` fail, rather than
the replay silently agreeing with the edit; and, where `node` exists, change a `shared/mailbox`
constant without regenerating and watch `TestParityCorpusIsCurrent` fail.
**Reviewer tier.** standard.

---

## 13. File ownership, levels and PR groups

| File | Tasks | Note |
| --- | --- | --- |
| `internal/cli/root.go` | **T5** | **one line**; the mobile plan reserves a second line for P6.6 (level 4) |
| `internal/cli/mailbox.go` | T5, T6, T7 | one writer per task: T5 the surface, T6 the runner, T7 the sweeper. **P6.7 extends this file** (the enforcement half of the MAC and the action record) and **P6.13 adds the store-side deletion** — §8.4's table says which row is whose, and a task here that takes a row the table assigns away is a finding, not an edit |
| `internal/mailbox/` (new package) | T1–T4, **T2a**, T6, T7, T8 | one file per task, as listed |
| `internal/mailbox/mac.go` | **T2 and T2a** | the one deliberate two-writer file: T2 lands it with `verify`, T2a extends it with `sign` in a dependent task. There is **one** function, so the two halves cannot drift |
| `internal/mailbox/testdata/parity.json` | T8 (generated, replayed) | `shared/mailbox/parity.test.js` writes it; the Go side only reads it |
| `shared/mailbox/parity.test.js` | T8 | **a test file only.** No merged module is edited by this plan; the merged contracts it consumes — `shared/mailbox-schema/vectors.json`, `shared/confirm/vectors.json`, `shared/prompts`, `shared/lease`, `shared/presence` — are **read**. A task that needs to edit one is a finding |
| `website/content/cli-reference.md` | T5 | **generated** — never hand-edited |
| `docs/design/v1.md` | T5 | one §9.1 row; **not test-enforced** (T5 says so) |
| `docs/mobile/mailbox-review-checklist.md` | T8 | new, short; the lines a reviewer must confirm, each marked proved or unprovable here |
| `internal/localcfg` | T2 | one field (the keychain item name), validated, **not secret** |
| `internal/cli/docs_mobile_mailbox_plan_test.go` | **this plan, not a task** | the pin this document ships with. It **derives its path list from this document at test time** — every backticked `internal/`, `shared/`, `mobile/`, `docs/`, `website/`, `deploy/` and `scripts/` path in the plan, plus its markdown links, must be in the tree, except the handful T1–T8 create, each of which must still be absent — that each symbol the plan names is still in the file it names it in; that M12, §4.4 and §9 state the same four-clause exit rule, **all four clauses in each of the three places**; that M4's three MAC states stay told apart and that the parked-message cost stays stated; **that the Phase 6 status the premise bullet states is true of this branch** — every task the bullet calls merged must still have its module in the tree (and be named there), and every task it calls approved-and-unbuilt must still have none of the files the plan names for it, so the list cannot rot back into "P6.2 is not built" while `shared/confirm` sits in the tree (`TestMailboxPlanPhaseSixStatusIsTrue`); **and, for every quote of `internal/budget/rules/rules.json.tmpl` this document makes, that the quoted lines are still in the template** — the quote is *derived by the pin*: it extracts §2's quoted block line by line and compares each with the file, and a negative control mutates a **temp copy** of the template and asserts the comparison fails, so the guard is proven able to fail rather than assumed. **That list is what the pin checks; it is not a claim that the pin checks everything above.** It is the answer to reviews that found a "verbatim" quote the file did not contain, the rule stated as one clause, and the exit row attributed to the wrong function; it is not T1–T8's and it adds no product code |
| `.github/workflows/ci.yml` | **nobody** | P4.3 is owner-made (Global Constraint 8), and its job is already **written and staged** as `docs/pending-workflows/shared-tests.yml` for the owner to copy in — pinned by `internal/cli/docs_shared_tests_workflow_test.go`; see §16 |
| `docs/pending-workflows/shared-tests.yml` | **nobody** | P4.3's job, staged for the owner's copy-in (merged in #582, with its README and its pin); this plan names it and changes nothing in it |

**Levels** (the mobile plan's): T1 and T2 are level 3 (with P4.1); T2a, T3–T8 follow in one lane,
one writer each, because each builds on the one before. **Dependency edges that exist explicitly in
the mobile plan** — `P4.1 → P6.6` (the `root.go` line and the keychain entry) and `P4.1 → P6.7`
(this file) — are respected: this plan does not touch what P6.6, P6.7 or P6.13 will, and T2a is
deliberately the seam P6.7 extends rather than an overlap with it.

**PR groups.** T1 and T2 together (the protocol, its one MAC function and the inbound gate), or
separately if the owner prefers one task per PR; **T2a with T2's PR** (same file, same function,
same vectors — reviewing them apart would review half a fence); T3 and T4 together (the lease and
what it fences); T5, T6 and T7 together (the command and its two halves); T8 either with T1's PR or
alone. **P4.1's own PR is SECURITY-SENSITIVE** (the mobile plan's group P-F) and is reviewed as one.

---

## 14. What this plan does not do

- **It changes no merged `shared/` module.** They are contracts now, and this plan is a consumer of
  them: `shared/mailbox-schema`'s `canonicalBytes`, `mac`, the field set, the kinds, the action
  record and `vectors.json` (P6.3, merged); `shared/prompts`' `PROMPT_TTL_MS`, queue and interrupt
  table (P6.5, merged); `shared/confirm`'s rules and vectors (P6.2's module, in the tree);
  `shared/lease` (P1.2, merged); `shared/presence` (P1.4, merged). If a task appears to need one
  changed, that is a finding.
- **It changes no merged rule.** Rules generation 3 is in the tree (P6.4, merged) and is what §2
  quotes. A fourth node, an inbox `acked`, a `CONSUMER` **read** grant or a lease node would each be
  a re-open of the rules task, and none is proposed here.
- **It builds no part of Phase 6.** No pairing, no device node, no pairing-key generation, no
  confirmation machine on the phone, no `fugaro pair`. What it *does* build are the **two seams**
  through which the merged serialisation is used — the inbound gate (M4a) and the outbound signature
  (M4b, T2a) — each held to the merged vectors. It **reads** a key P6.6 will put in the OS keychain
  (M5), **depends** on P6.2's record for the amendment's allowance (M6, through P6.7), leaves the
  **enforcement** of the kinds, the action record and the expiry to **P6.7**, leaves the deletion of
  a stored prompt to **P6.13**, and leaves the `m~…` sweep rule to **P6.6**.
- **It writes no check into the register.** Checks 80 and 83–88 are P4.2's; 81, 82 and 89 are
  P4.2a's; all ten are owner-run.
- **It touches nothing under `.github/workflows/`, and it authors no CI job.** P4.3's shared-tests
  job is owner-made and already staged (`docs/pending-workflows/shared-tests.yml`); this plan neither
  writes it nor copies it in — §16 says what the copy-in would mean for T8's drift check.
- **It performs no live cloud action, touches no `belong` installation, EdgeWeb or EdgeServer, and
  reads, writes or asks for no API key, token, secret, certificate or password.**
- **It does not make the app's side true.** P3.6 signs the inbox messages this command verifies, and
  P3.5/P6.12 speak the confirmations; until those exist there is nothing real to verify, which is
  what the merged vectors and the corpus stand in for.

---

## 15. Owner decisions

Each with the recommendation first. Decisions **1, 2 and 3** hold the task back; 4–8 shape it. The
list is shorter and more settled than an earlier draft's, because the merged state answers what that
draft had to ask: P6.3's serialisation and vectors exist, so the MAC is not a thing this plan must
wait for, and the questions that remain are the credential C0 names, the two store-side rows P6.6
and P6.13 own, and the corpus's known gap.

1. **Which credential authenticates the consumer's reads and writes (§2's C0), since no task in the
   mobile plan mints a `CONSUMER` one?**
   **The plan's answer, and the owner is asked to confirm it: the operator's ADC, for both halves** —
   and the reason is read off the merged rules rather than chosen for convenience. In the mailbox
   subtree **`CONSUMER` appears in no `.read`**, and the inbox's absence is stronger than that: the
   merged template gives `mailbox/$mb/inbox/$msg` **no `.read` at all** — not `MAILBOX`, not
   `userClaim`, nothing — so **no credential is admitted to the inbox**, while the heartbeat's only
   read grant is `MAILBOX` (the phone's, through `userClaim`). The consumer's own ETag read and its
   inbox read therefore **cannot** be made with any token it could hold — the inbox read is
   out of band for *every* client, which is what makes the ADC the only answer — and the
   `CONSUMER` grant that shapes the writes
   binds a token **carrying `mc == $mb`, which no approved task mints** — P6.6 mints `p~` and the
   phone's `m~`, P6.4 writes the device and pairing nodes, and the template's own comment says the
   claim "is minted the same way" without naming the task. **What that means, said plainly** (and
   D113b says it of the laptop consumer already): an admin credential **bypasses the rules**, so for
   this writer **the merged `CONSUMER` grant is not a fence at all** — the create-only outbox rule
   and the "the heartbeat and nothing else" write set are **rules in this command's own code**,
   tested by T6 and by §6's table, and the grants remain a store-level fence on **token-bearing**
   writers (the phone, and the consumer of some later day). The tests therefore do two things, not
   one: the write-set test in T6 asserts what the code writes, and an **emulator** test asserts that
   a token minted with `mc == $mb` is **refused** the heartbeat `GET` and the inbox read and
   **accepted** for the outbox and heartbeat writes — which is what makes C0 a claim about the
   merged rules rather than a shrug. **The cost:** the store does not enforce this command's write
   set; a fence in code is a fence only while the code is right. **The finding this leaves with the
   owner:** a `CONSUMER`-token minter belongs in **P6.6's CLI plan** (that is where the device token
   is minted and where the launcher's `token_minter` role is already reasoned about); if the owner
   wants the store to fence the consumer, that step is what buys it, and this plan's write-set test
   is what holds until then. **The alternative:** a new minting task before P4.1 — more work, a
   stronger fence, and it changes none of this plan's tests, only their tier.
2. **Is the consumer's ack of an inbox message its outbox reply plus the ledger (Ruling M2), or
   must the rules change to give the consumer an inbox `acked`?**
   **Recommended: the outbox reply plus the ledger.** The merged rules make an inbox ack
   impossible for the consumer (the inbox `$msg` write is `MAILBOX && newData.exists() &&
   !data.exists()` on the merged template, and a change to it is a re-open of P6.4), and the
   merged simulator's inbox ack is its own stated invention. **Cost:** the merged simulator and the
   app's P3.6 model a write the store does not grant the consumer — worth a one-line note where the
   simulator states it, which is a separate small change the owner may prefer to make at the same
   time.
3. **Is D128's amendment (cancel, approve/merge, confirming — allowed through P6.2's record) built
   as a **seam** now, or as behaviour?**
   **Recommended: a seam, unreachable from a real message, proven by a synthetic record in a test.**
   P6.2's **module** (`shared/confirm`, with `vectors.json`) is in the tree, but the **consumer's
   half of the record — the re-validation, the ledger of used action and confirmation ids, the
   confirmations themselves — is P6.7's**, so a real message still cannot reach the allowance and
   the refusal is unconditional. **Cost:** one code path with no caller, which a reviewer must be
   told about — hence T8's checklist and §9's "unconditional until P6.7". **What the merged module
   changes for the better:** P6.7's parity table has real vectors to be held to, so the seam is
   written against a contract that exists rather than one that is promised.
4. **The parity corpus (Ruling M9): a committed corpus generated by a JS test, replayed in Go — or
   a Node subprocess in the Go test?**
   **Recommended: the committed corpus.** Neither this runner nor CI's `test` job has Node (verified
   here: `node --version` → `node: command not found`), and `shared/`'s CI job is **staged, not
   running** — `docs/pending-workflows/shared-tests.yml` waits for the owner's copy-in — so a
   subprocess would be a verification that cannot run today. **What the merged
   state buys:** the two contracts that matter most — `shared/mailbox-schema/vectors.json` and
   `shared/confirm/vectors.json` — are **committed files**, so the Go side replays them with no Node
   at all; the corpus only covers `shared/mailbox`'s outcomes and codes. **Cost, stated plainly,
   because it is the weak point of this design:** drift protection for that last part is
   **discipline plus one best-effort check**, not a mechanism. The hash inside the corpus detects a
   **hand edit** of the artifact and nothing else; what catches a **changed `shared/mailbox`** is
   T8's `TestParityCorpusIsCurrent`, which regenerates and diffs **only where `node` is on `PATH`**
   and **skips with a recorded reason everywhere else** — including, today, in this repository's own
   `test` job. The mitigation available today is the generator being a test in `shared/mailbox/` (so
   any `shared/` PR that runs `npm test` regenerates it), plus the **one step left** for the
   staged job: `docs/pending-workflows/shared-tests.yml` is written and pinned, and copying it into
   `.github/workflows/` is what closes the gap — which also runs the package's mutation proofs, so
   the corpus generator runs there too. The copy-in is the owner's hand, not a task of this plan's
   (Global Constraint 8).
5. **Where does the keychain reader live?** **Recommended: a new narrow `internal/mailbox/keychain`
   with a fake**, because nothing in this repository reads an OS keychain today, and a boundary this
   small should not be a dependency (`github.com/zalando/go-keyring` or similar) unless the owner
   prefers one. **Cost:** the platform code is ours to write and to test, on three platforms. **Who
   generates the key is P6.6's** ("it generates the per-pairing MAC key into the OS keychain", Voice
   D42(2)), and **P6.6 is approved and not built**, so at P4.1 the item name is configurable and the
   key is absent — §8.3's first row, the startup exit.
6. **Is `fugaro mailbox` visible in the CLI reference, and the sweeper hidden?**
   **Recommended: yes** — the consumer is a real operator command, and the sweeper is internal, in
   the shape of the hidden `fugaro budget history`. **Cost:** one more row in a generated file and
   in design §9.1, both of which T5 writes.
7. **Should the ledger and the pending-prompt file be **one** state file or **two**?**
   **Recommended: two**, because a waiting prompt is not a consumed delivery and one file would let
   a crash between the two meanings be ambiguous. **Cost:** two atomic writes per pass instead of
   one.
8. **The two store-side rows §8.4 assigns away — does P4.1 stage them, or do their own tasks add
   them wholesale?**
   **Recommended: wholesale, by their own tasks.** **P6.13** adds the deletion of a stored prompt
   past D118's TTL to this command's file, "and nothing more" — the consumer, not the sweeper,
   expires a prompt and delivers its safe answer, and the sweeper never answers a conversation (T7
   pins the rule it must not break, and builds none of the deletion). **P6.6** adds the `m~…` rule
   to `internal/budget/sweep.go`, the one file the mobile plan gives it. **Cost of staging nothing:**
   between P4.1 and P6.13 a stored prompt is not deleted by `fugaro mailbox sweep`, which is correct
   — at P4.1 nothing stored exists — and is what §6's last row and §14 state. **The alternative** —
   P4.1 building the deletion ahead of its rule — would put a store write in a task whose plan says
   it writes none, and would be the same overlap this section's table exists to prevent.

---

## 16. Unverified, and what cannot be verified here

- **`cd shared && npm test` was not run for this plan, and could not be:** this runner image has
  **no Node** (verified again at the time of writing: `node --version` → `node: command not
  found`). The plan is docs-only, so nothing depended on it; the parity design (M9) is built so
  that no Go verification does either, and the two vector files the MAC depends on are committed
  rather than generated. It is recorded in the pull request body.
- **The Go tests this plan describes do not exist yet** — every "Verification" line above is a
  **plan**, not a result. `internal/mailbox/` does not exist in the tree. No task in this plan is
  done.
- **The pairing key does not exist until P6.6 runs**, and P6.6 is approved and not built. So the
  shipped build can compute a MAC (P6.3 is merged) and has **no key to compute one with**: the run
  ends at §8.3's first row, the startup exit with no pass run, and that is a **dependency this plan
  states rather than a capability it has**. The per-write signing refusal (M4's third row) likewise
  has no producer in the merged tree — T2a's **injected failing signer** is what reaches it, in a
  build where a fake keychain has supplied a key.
- **An unverifiable inbox message is never retired, and this plan does not fix it** — it cannot: the
  ledger holds no slot for it (M4a's anti-denial-of-service rule) and the merged inbox `$msg` write
  is create-only, so it refuses a delete as well as an overwrite (§2), and every pass re-reads it
  and re-refuses it until an operator removes the node with their own credential, out of band. The
  cost is one inbox read and one `refused[]` entry per pass, forever, from one bad message; the
  alternatives are a tombstone that would consume the genuine message's `(sender, id)` (refused) or
  a rules change (a re-open of P6.4). §7's ordering 2 states it where the design decision is made;
  T2 asserts the behaviour.
- **Checks 80 and 83–88** (a real phone, a real orchestrator, a real database, a real clock, real
  audio hardware) are owner-run and unrun. **No live behaviour is verified by this document.**
- **The RTDB conditional write against a real database** is not verified here. The client supports
  it (`internal/rtdb/client.go`'s `GetETag`, `PutIfMatch`, `ErrPrecondition`) and
  `internal/budget/kill.go`'s `SetKill` uses it in production, and T3 proves the fence against
  `internal/gcpfake`. What is unverified is a live project's answer, which is check 83's
  neighbourhood and is the owner's to run.
- **The rules on the consumer's own path are not exercised by the consumer's tests**, because the
  consumer writes with the ADC (C0, decision 1). The grants are proven where they *are* load —
  the emulator tests over the identitytoolkit fake — and the consumer's write set is proven by its
  own test. Neither proves the other, and neither is claimed to.
- **The OS keychain on macOS and Windows** is unverified by this plan: the tests use a fake, and no
  agent reads a real keychain.
- **The corpus's drift check cannot run wherever Node is absent** — including this runner and CI's
  `test` job today — so `TestParityCorpusIsCurrent` **skips** there and a `shared/mailbox` change
  under an unregenerated corpus is caught by no test in this repository until the owner copies the
  staged P4.3 job in. Decision 4 offers the design with that gap named, not hidden.
- **The sweeper's treatment of `m~…` users** is P6.6's and is not built. So at P4.1 the sweeper's
  delete set cannot contain one: it deletes the consumer's own state and **refuses and reports** if
  it is ever about to delete an `m~…` — or an `r~…` — user (M8, §6, §17). The `m~`-keyed deletion
  is a **finding for the owner**, never a silent fix here.
- **The stored-prompt deletion is P6.13's** and is not built: at P4.1 `fugaro mailbox sweep` deletes
  the consumer's own expired state and **no store node** (§6's last row, T7).
- **The shared package's CI job (P4.3) is written and staged, not running.** The job that installs
  the shared package's dependencies, runs its test script and runs its mutation proofs — alongside
  the Go job and the `rules` job, which is the only job that sets up Node today — is committed at
  **`docs/pending-workflows/shared-tests.yml`**, with the README beside it, and
  **`internal/cli/docs_shared_tests_workflow_test.go`** pins the staged file (its `main`-push and
  `shared/**`-pull-request triggers, `contents: read`, every `uses:` as a full commit with its tag,
  the two Node lines, and that the README says the file is still pending — a sentence the copy-in
  must update). It is **owner-made** (Global Constraint 8: no agent writes or edits
  `.github/workflows/**`), so it is **not in `.github/workflows/` yet and no workflow runs it**.
  **What that means for T8:** `TestParityCorpusIsCurrent` regenerates and diffs only where `node`
  is on `PATH` and **skips with a recorded reason everywhere else**, which is still the case in this
  repository's `test` job today — the copy-in is the single remaining step, and until it happens a
  `shared/mailbox` change under an unregenerated corpus is caught by no test here. Nothing in this
  plan writes that job or performs the copy-in.

---

## 17. Risks

| Risk | What it costs | Where it is held |
| --- | --- | --- |
| The credential C0 names (decision 1) | the store's grants do not bind this writer, so the write set is a rule in code | C0 and T6's write-set test; the emulator test proves the grants bind a token-bearing writer; the minter is the finding left for P6.6 |
| The key never arrives (P6.6 not built) | the command is inert until a pairing exists | §8.3's first row, stated as the startup exit and as a dependency (§16) |
| The corpus drifts from `shared/mailbox` | the Go side agrees with a stale module | **discipline only**, plus `TestParityCorpusIsCurrent` where Node exists (T8); the hash fails a **hand edit**, not drift — §16 and decision 4 |
| The ETag CAS is not honoured by a real RTDB | two consumers act | T3's fence, and check 83 by the owner |
| A refusal list that is right today and wrong later | a destructive action from a voice message | the list is a value with a test; an unrecognised kind is refused by default (T6) |
| The keychain code is ours | a platform bug, or a leak | T2's two mutations; the reviewer tier |
| `internal/budget/sweep.go` touching `m~…` | a deleted mailbox's state | **M8 refuses it** — the P4.1 sweeper's delete set is its own state, and an `m~…` user in it is refused and reported (§6, §16); the rule itself is P6.6's |
| The sweeper deleting a stored prompt ahead of its task | the design's separation — the sweeper reads and deletes, the consumer answers | T7 asserts the delete set holds no `mailbox/` path and that the sweeper never answers; the deletion is P6.13's (decision 8) |
| An unverifiable message parks in the inbox for ever | one inbox read and one `refused[]` entry **per pass**, forever, from one bad message | nothing in the merged design retires it (§7's ordering 2, §16): the ledger holds no slot for it by design, and the create-only inbox write refuses a delete. The operator's own credential is the only remedy, and it is out of band |

## 18. Execution

1. The owner answers decisions **1, 2 and 3** (the three that hold the task back) and 4–8 if
   convenient, and **approves P4.1 separately**, which the mobile plan's step 6 requires and which
   this document does not perform. The dependency on **P6.6** for the pairing key is a real one and
   is named rather than assumed: P4.1 can be built and tested without it (the fake keychain), and
   cannot do anything in production until it runs.
2. Then T1 and T2 land first, SECURITY-SENSITIVE, each with its independent opus review, and the
   corpus generator runs (the owner, by hand or after copying the staged P4.3 job in). T2a follows
  immediately —
   it is the same function, the same key and the same vectors, and it is what P6.7 expects to find
   filled.
3. Then T3 and T4, then T5–T7, then T8.
4. Every task: commit, then `fugaro verify build` and `fugaro verify test`, with the working tree
   clean; the Docs guards, `gofmt -l` and `GOFLAGS=-p=2 go vet ./...` in the same run.
5. Nothing in this plan merges without a passing verification and a mutation-proven Red step, and
   nothing claims a live behaviour works before its check has been run by the owner.