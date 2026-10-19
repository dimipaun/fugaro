# The cloud STT/TTS allow-list (mobile P5.3)

**Status: design only; nothing is implemented.** No line of an Expo app, no rules change, no shared module and no provider client exists for this document's subject, and none is added by the change that writes it. **Check 85 — the on-device STT/TTS bake-off — has not been run**: no `## Check 85` section and no summary row exists anywhere in [docs/gcp-live-checklist.md](../../gcp-live-checklist.md), no device was measured, and **no latency number exists**. Every sentence in this document that depends on latency is therefore conditional on Check 85's measured results, and the recommendation in §8 is written as a conditional rather than as a preference.

**The owner's ruling of 2026-10-16 stands and this document does not touch it.** Voice **D44** ([voice.md](../../design/voice.md)) answered **local-only for v1**: cloud STT/TTS happens only by a **second, explicit owner decision**, and that decision has not been taken. This document is written under the owner's approval of 2026-10-18 that Phase 5's design documents be **written, not built** ([implementation-plan.md](../implementation-plan.md), Phase 5: "Later, unscheduled … each is recorded so that a later reader finds the reasoning rather than rediscovering the question"). It adds no task to any approved phase, approves nothing, writes no check and answers no decision.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-19. **Numbering:** this document allocates **no decision number** — its open questions are **Q1–Q6** in "Open for the owner" at the end. It **names** one live-check number (**110**) and writes no check, following the register's convention that "the phase that ships the check writes them". It amends no ruling in any other document.

---

## 1. The problem

The app exists so that the owner can talk to Fugaro from a phone. Voice D44 ruled that the speech and the replies stay on the device, and mobile D123 inherited Voice D46 unchanged: a **measurement-first bake-off**, on the **oldest supported device**, over **local on-device engines only** (plus at most one bundled engine), with the cloud half of the field closed for v1.

Two things follow from that ruling, and they are the whole of this document's subject.

1. **The cloud half was closed on a measurement that has not been taken.** D46's answer is that the phase-1 measurement decides *whether a local engine meets the latency bar*, and D44's answer is that a local engine failing the bar is "an input to the second decision and not a licence to make a cloud call". So the second decision is owed a **number**: what latency did the local engines actually produce, on the oldest supported device, for the interaction the app exists for. **Check 85 is that measurement, and it has not run.** Until it does, "the local engine is not good enough" is a hypothesis and a cloud design is a design against a hypothesis.
2. **A cloud path is the first thing in this work that sends a person's speech to a party the owner has no IAM over.** That is a different kind of egress from every other one the app makes, and it is the reason this document is a design and not a task.

The task entry this document elaborates is [implementation-plan.md](../implementation-plan.md) **Task P5.3**, whose text as it stood before this change was: "**Depends on.** D123 answered, and Voice D44's **second explicit decision**. **Files.** None yet. **Verification.** Check 85 records the bake-off; a cloud decision would need its own check. **Reviewer tier.** SECURITY-SENSITIVE, when it is scheduled."

## 2. What exists today (verified against `main`)

Nothing in this document is a claim about a file that was not read. Each sentence below names the path and the symbol it rests on.

**The app's voice path is local-only, and the ruling is written as a global constraint, not an implication.** [implementation-plan.md](../implementation-plan.md) Global Constraint 12: "**The app never stores or uploads audio** (D118, answered 2026-10-17 as recommended). STT is on-device; the mailbox carries text." D118's own cell says the same and gives the reason: on-device STT "is what makes that possible, and it must be stated as a rule rather than left as an implication".

**The console's voice work already carries the cloud half of D44 as a phase, and it is out of v1.** [voice.md](../../design/voice.md) §5.3's register holds checks **65 and 66**, "held for the signed-build microphone check and for the cloud-provider check that can exist only if the owner takes the second egress decision (D44, D46)", and its V7 phase — "cloud providers, only if the owner takes the second egress decision" — lists exactly what V7 would build: "the provider clients, the policy list (**deny by default**, D44), the disclosure that this spend is outside the caps (as D45's is), and the privacy write-up". **Nothing in V7 is built.**

**D44's allow-list is specified, and it is specified for the console.** Its cell: "a **list in the console's local settings whose syntax is `config.ModelProvider.AllowDataTo`'s and whose default is the opposite of it**. The syntax is copied deliberately — a list of project names, `["*"]` for everything, `[]` refused as ambiguous … For voice, **absent means nothing leaves the machine** … **The list's unit is *the project*, not the repository**". The same cell states plainly that "**`allow_data_to` does not cover this today**", and D42's cell says why a key and a policy of this kind are not the same object: "a project's `fugaro.yaml` and its published shared config reach every job of the project and every teammate".

**`allow_data_to`'s syntax and its default are both real, and they are the opposite of what voice needs.** `internal/config/providers.go`'s `ModelProvider.AllowDataTo` (`allow_data_to`) carries the doc comment "Absent (the zero value) or `[\"*\"]` mean every repository"; `ModelProvider.Allows(repo)` reads that way, `OpenToEveryRepo` and `OpenToEveryRepoReason` name it in prose, and `ValidateModelProviders` refuses the ambiguous empty list (the `len(p.AllowDataTo) == 0` branch, whose message is "an absent key and `[\"*\"]` both mean 'every repository', but a …"). So the syntax D44 copies is a list of names plus the wildcard, and the default it refuses to copy is "absent means everything".

**The project policy node exists, and it is read by a run's token, not by a user.** `internal/projectpolicy/paths.go`: `PathShared()` is `config/policy/shared`, `PathRepo(slug)` is `config/policy/repos/<escaped slug>`, `PathRulesMarker()` is `config/limits/policyRules`, `PathAdmin()` is `config/admin`; the file's comment records that "a read of `config/policy` itself is denied by" the rules because an RTDB read grant cascades to children. `internal/budget/rules/rules.json.tmpl` is where that is true: `config/policy/shared` is `{ ".read": "TOKEN", ".write": false }` and `config/policy/repos/$slug` is `{ ".read": "TOKEN && auth.token.fs == $slug", ".write": false }`. **There is no `USER` in either grant** — the phone's user claim cannot read the policy tree at all.

**The phone's read grant is exactly four families, and the policy tree is not one of them.** [reconciliation.md](../reconciliation.md) §4, tension (b1), resolved 2026-10-17: "The phone's user claim may **read** only **`config/caps/**`** … **`spend/$day/**`** … **`config/kill/**`** … **and its own mailbox (the outbox and the heartbeat)**, all in `internal/budget/model.go`. **Not `agents/$slug/$run`** … and **no new `.read` on `outcomes/**`** … until a separate decision of the owner says otherwise. **P2.2's emulator cases must assert the read denial for every family not named here**: `agents/**`, `outcomes/**`, **the rest of `config/**`**, `/fugaro/**`, another account's mailbox, and another installation's claim." The same resolution says the job list therefore "shows numbers and states only and has no run titles and no repositories".

**The phone's write grant is two paths, and the heartbeat is not one of them.** Global Constraint 6: "(a) its own **inbox messages** … and (b) the **ack on a message it has already spoken** … The **heartbeat is not the phone's write**". The template's merged mailbox subtree is `internal/budget/rules/rules.json.tmpl`'s `mailbox` node, which today validates only `id`, `sender`, `text` and `at` with `"$other"` denied; the `mac` and `kind` fields are **P6.4's**, not shipped.

**The app holds exactly two credential items per account, and a third is a new exception.** Global Constraint 4: "The app holds exactly two items per account, both in platform secure storage, both never logged and never in a settings file: a Firebase Auth ID token, and — only if D113b's pairing-key question is answered (a) — the per-pairing MAC key … Never a Fugaro or cloud admin credential, never an LLM key, never a service-account JSON, never a `fugaro` config file." Voice D42's cell is the same rule on the console's side and states its cost: "**the STT/TTS keys are new, and the recommendation is the macOS keychain, read by the child**", with `SECURITY.md`'s "**Cloud keys stay in the cloud.**" named as "the *reason* this is a decision and not an obvious one, since voice is the first feature that genuinely needs a credential on the operator's machine".

**The redactor exists, and it works on text.** `internal/agent/redact.go`'s `NewRedactor`, `Redact(s, secrets)` and `RedactFunc(secrets)`; D118's answer routes the mailbox through it ("text redacted before it is written, by the digest's redactor"). The titles the feed carries are sanitised and clipped the same way (`internal/watch`'s `CleanLine(s, 80)`, pinned by `internal/watch/build_test.go`'s `TestHostileTitlesSanitized` and `internal/watch/finished_test.go`'s `TestFinishedRowSanitisesHostileTitle`).

**The routing and confirmation logic is shipped, pure, and already distrusts its input.** `shared/command/index.js` exports `routeUtterance({utterance, aliases, projects, agents})` and `ROUTE` (`APP_COMMAND`, `AGENT_TEXT`, `NOT_UNDERSTOOD`), and its own comment says it treats text as routed input: "text this module ROUTES ('Fugaro​, confirm' looks like an app command and …)". `shared/confirm/index.js` exports `step(state, event)`, `readBack(verb, target)`, `isConfirmPhrase(text)`, `pending(state, now)`, `PHASE` (`idle`/`pending`/`crowded`), `DESTRUCTIVE_VERBS` (`cancel`, `confirm`, `merge`), `PASS_THROUGH_VERBS` (`launch`), `REFUSED_VERBS`, `DEFAULT_CONFIRM_TIMEOUT_MS` (60 s) and `REFUSAL`. Two of its stated rules are load-bearing for §6 below, in its own words: "the **read-back text is built from the resolved target and from nothing else** — never from the transcript … It is recomputed, never stored, so a state handed back with an edited read-back cannot be spoken", and "a confirmation is a **separate utterance** … recognised as the **fixed phrase**".

**The mailbox schema requires a MAC on every message and every heartbeat.** `shared/mailbox-schema/index.js`: `REQUIRED_FIELDS` is `['id', 'sender', 'text', 'at', 'mac']`, `KINDS` is `['message', 'prompt', 'answer', 'action']`, `ACTION_REQUIRED` is `['id', 'verb', 'target', 'confirmation']`, `HEARTBEAT_REQUIRED` is `['at', 'boot', 'mac']`, and `canonicalBytes(record, direction)` covers a `direction` the reader supplies rather than the record stores. [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) §3.5 states the rule the field serves: "**An answer to a destructive prompt is accepted only as a MAC-verified action record carrying a confirmation** — never as free text and never as a plain `answer`", and "**the rules (P6.4) only require the field to be present; they cannot verify an HMAC**" — the verification is the **consumer's**, in `fugaro mailbox`.

**A launcher can already read a mailbox, over IAM rather than over the rules.** [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) G13: "The same IAM file gives those people `roles/firebasedatabase.viewer`, an IAM read that bypasses the rules — so mailboxes are readable by every launcher". G13 also records that "**any launcher can mint a token for any `mu` or `mc`**" and that "the per-pairing MAC key (tension (b2)) remains the only thing that tells the phone its own consumer from another launcher".

**The provider-terms problem is already stated honestly for model providers, and the same sentence will be true here.** [docs/multi-model.md](../../multi-model.md): "**`require_zdr` is a price, not an enforcement — Fugaro cannot read your account's data policy** … and `fugaro doctor` says so (OpenRouter's key endpoint, `GET /api/v1/key`, has no data-policy field to read)". [voice.md](../../design/voice.md) draws the conclusion for STT/TTS in as many words: "The same will be true of an STT/TTS provider: the console cannot verify what the provider retains."

**Costs outside Fugaro's caps are already a disclosed category.** Voice **D45**: the summarizer's spend "is not accounted by Fugaro's caps, and it is disclosed". `SECURITY.md`: "**The backstop is the provider account's own limit**: set a credit limit on every key you give Fugaro", and "**Budget mode defaults to off.** … the real model key sits in the agent's environment like any other secret".

**The credentials service is not the phone's key holder, by its own boundary.** [docs/credentials-service/spec.md](../../credentials-service/spec.md): "**Boundary:** the service stays secrets-only. Other always-on features (**e.g. the phone client backend**) go in sibling services that call it, keeping its attack surface minimal." Its own scope is jobs: "The service is the single holder of every secret a **job** needs."

## 3. The variants, and what actually leaves the device in each

The honest framing is not "text leaves the device for the first time". **The transcript is already off the device**: D118's answer writes the redacted text into the mailbox, and G13 records that every launcher can read that mailbox over IAM. What a cloud provider adds is a party **outside the installation**, with no grant, no TTL and no redaction that Fugaro controls — and, in the STT variants, a payload that is **not in the mailbox at all and never has been**.

| variant | what leaves the device | what the owner can do about it | is it new? |
|---|---|---|---|
| **A — cloud STT** (speech recognition off-device) | **raw audio**, unredacted and unredactable, plus whatever metadata the transport carries | nothing after the fact; the provider's own terms are the only control, and `multi-model.md`'s sentence says Fugaro cannot read them | **yes, and categorically**: audio has never left the device (Global Constraint 12), and audio cannot be redacted by a rule that reads text |
| **B — cloud TTS** (spoken replies synthesised off-device) | the **reply text**, which the redactor and the reader rules have already been applied to | the same redactor the digest uses, applied before the call; a redaction hit can be masked or refused (§5) | **no in kind** (text is already in the mailbox), **yes in destination** |
| **C — cloud wake-word / always-on routing** | a **continuous stream** of audio, including everything said while no command was intended | nothing: the wake word is what decides what is "sent", and a cloud wake word has to hear everything to decide | **yes, and it is the worst of the three** |
| **D — local only** (today's answer) | nothing | — | — |

Three consequences, and they are the spine of this design.

1. **A and C are the variants D118 cannot survive.** D118's rule is that spoken text "can contain secrets" and is therefore redacted "before it is written". Redaction is a function of a string; audio is not a string. If the audio leaves, the secret leaves, and the transcript that comes back is a *rendering* of it — redacting the rendering does not recall the audio. **The only way to keep D118's promise under cloud STT is to not send the audio.** That is a property of the variant, not of the provider's terms.
2. **B is the variant where D118's promise can be kept**, because the payload is text that the redactor has already seen, and because a TTS call is initiated by the app at a moment it chooses, with the reply in hand — not by a wake word that has to listen first.
3. **C is not a design; it is a different product.** An always-on cloud listener is D5's no-telemetry posture reversed, and §5's never-send list could not be written at all, because "nothing while no command is intended" is not expressible as a filter on a stream that must be whole to work.

**This document therefore recommends the decision be split by direction**, which is the one structural idea it adds: **STT-out (audio) and TTS-out (text) are two decisions with two different answers available, and D44 as written treats them as one.** Nothing in D44's words requires them to be one — it says "cloud STT/TTS", and the direction that is defensible is the direction whose payload is text.

## 4. The proposal

### 4.1 The allow-list: a policy node, absent means local only

**A new node beside the policy tree, read by `TOKEN` and by nothing else:**

```
config/policy/voice/shared            { ".read": "TOKEN", ".write": false }
config/policy/voice/repos/$slug       { ".read": "TOKEN && auth.token.fs == $slug", ".write": false }
```

This copies `config/policy/shared` and `config/policy/repos/$slug` exactly (`internal/budget/rules/rules.json.tmpl`, `internal/projectpolicy/paths.go`'s `PathShared`/`PathRepo`), including the reason the parent gets no grant of its own: an RTDB read grant cascades to children, so a `.read` on `config/policy` would open `config/admin`'s sibling branches to every run. The unit is **the project** (Voice D44's own unit, restated by mobile D123's inheritance) and the slug is the same slug `PathRepo` escapes.

**Default deny, and the default is the opposite of `allow_data_to`'s.** Absent, empty, malformed, or unreadable all mean **local only** — the same four answers, because they are indistinguishable to a caller that must not guess. The syntax is copied and the default is inverted, which is what D44 asked for and what its cell says an earlier revision got wrong.

**The shape of an entry is a named provider, and the terms are recorded with it.** A provider is admitted by **name** — not by URL — and the name resolves to an endpoint the app already holds as a constant. A policy node that carried a URL would be a node that can redirect a person's audio by being edited, which is a worse failure than a node that can only widen a list of names the app knows.

```
config/policy/voice/repos/<slug> = {
  providers: ["<name>", ...],        // absent or [] => local only
  direction: { stt: false, tts: true },   // absent => both false
  read_at: "<date the owner read the terms>"
}
```

`direction` is where §3's split lives, and its default is false on both halves. `read_at` is the date the owner read the provider's terms; it is a record of a reading, not a claim about the provider, and it exists so that a stale reading is visible rather than assumed current.

### 4.2 How the phone learns the decision

The phone **cannot read `config/policy/voice/**`**: tension (b1) resolved the user-claim read grant to "exactly four families, and nothing else", and `config/policy/voice` is not one of them; P2.2's emulator cases are required to assert the denial of "the rest of `config/**`". Three ways to give the phone the decision, and only one of them keeps (b1) as written:

- **(i) The consumer resolves it and delivers it, signed, into the phone's own subtree.** The consumer already holds `TOKEN` and already reads the policy tree. It resolves `config/policy/voice/...` and writes the **resolved** decision into the phone's own mailbox subtree — a record in the family the phone may already read — signed with the per-pairing MAC key, so a launcher who mints a token for the consumer's uid still cannot forge it (§3.5's `direction`-covered MAC, and G13's note that the MAC key "remains the only thing that tells the phone its own consumer from another launcher"). **Fail-closed: no record, an unsigned record, a record older than the pairing, or a record that does not verify all mean local only.** Cost, stated: the phone learns a *widening* only when the consumer is running, so a cloud path is unavailable while the laptop is away — which is most of the time the app is useful. That cost is real and is Q2's alternative.
- **(ii) A fifth read family** — add `config/policy/voice/**` to the user-claim read grant. Cost: it reverses tension (b1)'s "exactly four families, and nothing else" and the P2.2 assertions that go with it, for one feature; it is a rules change of the SECURITY-SENSITIVE class (P2.1/P2.2's shape); and it makes the policy node's *contents* readable by any signed-in phone, which is more than the decision needs.
- **(iii) The console's local settings document**, which is what D44 literally answered for the console. Cost: it is not a policy node, so a teammate or a second device sees nothing, and the brief's requirement — "a policy node like the other allow lists … per-project opt-in" — is not met. It is the right answer for the *device's* half (§4.4) and the wrong one for the installation's.

**Recommendation: (i), with the device's own setting as a second gate.** Two gates, and the stricter wins: the installation's policy node (the operator's gate, §4.1) and the phone's own local opt-in (the user's gate, §4.4). A device that has opted in cannot send anything while the installation's node says otherwise, and an installation that has opened the node cannot make a device that has not opted in send anything.

### 4.3 The provider register

The brief asks for "named providers with their retention and training terms **recorded**". The register is a section of the document that admits a provider — a table, one row per name the policy node may carry, with: the name; what the provider says it retains and for how long; whether it trains on the data; the date the owner read that; and a link to the page read. **It records a reading, and it does not verify it**, because `multi-model.md`'s sentence is true here too and voice D44's cell already says so: "the console cannot verify what the provider retains". The register's honest form therefore has a column that says so, and `fugaro doctor`'s precedent (`multi-model.md`: "`fugaro doctor` says so") is what a later task would extend.

The register is **a document, not a node**, and the recommendation says why: a node holding prose no test can check invites a reader to treat it as verified. A name in the policy node that has no register row is **refused by the app**, not admitted by default — the same fail-closed direction as everything else here.

### 4.4 Consent, and the always-visible indicator

- **First-use consent, per provider, naming what leaves.** Before the first cloud call on a device, the app shows: the provider's name, **which direction** (audio or text), what that direction sends (§3's table, in those words), the register's terms for that provider **and the date they were read**, and the disclosure that **this spend is outside Fugaro's caps** — D45's disclosure shape, which voice §5.3's V7 lists for exactly this ("the disclosure that this spend is outside the caps (as D45's is)"). Consent is stored **on the device, never written to the database** — D127's precedent for a per-device preference, and Global Constraint 6's reason (the phone writes two paths and no others).
- **An always-visible indicator, and it must not be dismissible.** While the voice screen is open the indicator shows one of exactly three states — **local**, **cloud**, **cloud requested but refused** — and while a cloud call is in flight it shows that. The state is a **pure function of the resolved decision, the device's consent and the last call's route**, so it is testable in CI with no phone and no network (Global Constraint 10, and `shared/`'s precedent: every module in `shared/` is a pure function with vectors).
- **The indicator is the honest replacement for a promise this feature breaks.** [voice.md](../../design/voice.md) records that the app's promise is that "your machine stays your business" and that a transcript panel that persists, an audio spool or a session store are each new stored material against it. A cloud provider makes that sentence false for the duration of a call; an indicator that cannot be hidden is the minimum that keeps the app honest while it is false.

### 4.5 The key

A cloud provider needs a credential, and **there is no place in the current design for it to live.**

- **On the phone** it would be a **third** item in platform secure storage, against Global Constraint 4's "exactly two items per account" — a new named exception of the D54b/D111 kind, which only the owner grants. It would also be the first provider credential on a device Fugaro does not manage, which is `SECURITY.md`'s "Cloud keys stay in the cloud" read backwards, and voice D42's cell names the shape it refuses: "a plaintext provider key in the settings JSON, or in an environment variable the app sets".
- **In a Fugaro-hosted proxy** the key stays in the cloud and the phone holds only a short-lived per-device token — the shape the credentials service already uses for jobs ("Claude Code is pointed at the proxy via its base URL and given a short-lived, per-job token that the proxy validates"), and the shape `SECURITY.md` prefers. The cost is a new always-on service: the credentials service's own boundary says the phone's backend "go[es] in sibling services that call it", and an always-on service is **P5.1**'s subject and D115's territory — unscheduled, and the reason P5.3 depends on more than D44. **And the proxy does not make the audio stay on the device**; it moves who receives it from a third party to Fugaro's own service, which is a smaller move than it sounds and must be described as such rather than as a privacy win.
- **No key at all** (an unauthenticated provider endpoint) is refused: it would put the owner's speech on a shared quota with strangers.

**Recommendation: the proxy, if the cloud half is taken at all** — and the recommendation is deliberately *not* "a key on the phone", because that exception is the one part of this design that is hard to take back.

### 4.6 Failure modes

Every one of these is **fail-closed to local**, and none of them blocks the interaction:

| what fails | what happens |
|---|---|
| the provider is down, slow or returns an error | the call is abandoned and the utterance is handled by the local engine; the indicator shows **local**; nothing is retried against a second provider unless the owner named that provider for that project, because retrying against another provider is sending the same speech to a second third party |
| the policy node cannot be read | local only (§4.1's four answers) |
| the signed decision record is absent, unsigned or stale | local only (§4.2 (i)) |
| the device has no consent recorded | local only, and the consent sheet is offered |
| the local engine fails *and* the cloud path is unavailable | the app says so and offers **text entry**; it does not block, and it does not fall back to a provider that was not named |
| the redactor matches | the utterance is not sent (§5) |
| a confirmation is pending | no egress at all (§5, §6) |
| the network is down | local only; nothing is queued for later, because a queued utterance is a recording (D118: no audio is stored) |

**The last row is a rule, not a fallback**, and it is the one most likely to be got wrong by a well-meaning implementation: a retry queue for cloud STT is an audio spool, and D118's answer forbids one.

### 4.7 Cost

- **The spend is outside Fugaro's caps and is disclosed as such** (D45's shape, voice §5.3's V7, §4.4's consent sheet). Fugaro cannot meter it against `spend/$day/**` because the phone is not a run and holds no budget token — the token channel is per-run and consumed once (`internal/budget/token/object.go`'s `TakeObject`), and nothing in this design proposes to widen it.
- **The backstop is the provider account's own limit** (`SECURITY.md`: "set a credit limit on every key you give Fugaro"), which is the same backstop every other provider key in this project has.
- **The number that matters does not exist yet.** D46's bake-off records "cost per hour of conversation" per candidate; Check 85 produces it. Until then, "cloud is cheaper than local" and "cloud is more expensive" are both unmeasured, and a design that priced the choice now would be guessing at the number the owner's second decision is supposed to be taken against.

## 5. What the app must never send

A list, and each entry is a rule a test can hold:

1. **A spoken confirmation's audio.** The confirmation is the security boundary (§6). Sending it to a provider hands the one phrase that authorises a destructive action to a party outside the installation, and it hands the provider the ability to *return* it.
2. **Anything at all while a confirmation is pending.** Not "the confirmation", not "the audio of the confirmation" — **nothing**. This is the strongest form of rule 1 and the one that closes §7.3's attack, and it is why it is a rule about a *state* rather than about a *phrase*: the app cannot know which utterance will turn out to be the confirmation until it has been transcribed, and by then the audio is gone.
3. **Anything matched by a redaction rule.** The digest's redactor (`internal/agent/redact.go`) runs **before** egress. On the TTS path a hit is masked in the text sent; on the STT path a hit **cannot** be masked, so a hit means the utterance is not sent — and a design that could not express that difference would be a design that had not understood §3.
4. **Anything while no command is intended.** There is no continuous streaming, no cloud wake word, and no ambient buffer (variant C, refused).
5. **Speech about a project that is not on the list**, including a project named in the same utterance as one that is.
6. **Another installation's speech.** `belong` is never a target (Global Constraint 11), and the policy node is per-installation by construction.
7. **The console's traffic.** The console's voice module is a separate surface with its own V7 phase; the phone must not become its egress, and the app must not send on the console's behalf (D125's "the app's build reaches into the console's directory" is the coupling that would make it possible).
8. **Any credential**: the pairing MAC key, the Auth ID token, the per-device proxy token, or a provider key if one ever exists on the device (Global Constraint 4).
9. **Any audio at all, ever, that the user has not just spoken as a command.** A "keep the last 30 seconds in case" buffer is an audio spool by another name.

## 6. Why a destructive command stays impossible by voice, whatever the provider returns

**The provider changes where the text came from. It changes nothing about what authority the text has**, and that is a property of the shipped modules rather than a promise:

1. **The read-back cannot be poisoned.** `shared/confirm`'s own rule: "the read-back text is built from the resolved target and from nothing else — never from the transcript … It is recomputed, never stored". A provider that returns a transcript naming a *different* target cannot change what is read back, because the read-back is not derived from the transcript.
2. **The confirmation is a fixed phrase as a separate utterance**, recognised by `isConfirmPhrase(text)`, and an utterance carrying both a command and a confirm token is refused (`command-and-confirm`). A provider that injects "confirm" into the transcript of the command utterance is refused by that rule.
3. **A confirmation is bounded in time** (60 s default, `DEFAULT_CONFIRM_TIMEOUT_MS`) and **authorises exactly one action** through the ledger, so a replayed or duplicated transcript authorises nothing.
4. **Only a MAC-verified action record runs a destructive verb.** [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) §3.5: "An answer to a destructive prompt is accepted only as a MAC-verified action record carrying a confirmation — never as free text and never as a plain `answer`. The consumer refuses a plain `answer` or `message` to a prompt whose effect is destructive." The transcript is an *input to the phone's router*; it is never the record the consumer acts on.
5. **A transcript from a provider is untrusted input of the same class as agent-written text**, which `shared/command`'s own comment already treats as routed rather than trusted, and which `SECURITY.md` and the credentials spec treat the same way for agents ("Prompt-injection exposure: review agents read untrusted PR content").
6. **And the one path that would remain is closed by §5 rule 2**: if nothing leaves the device while a confirmation is pending, no provider can ever be the source of the utterance that confirms, so a forged confirmation is not a thing that can be forged — it is a thing that cannot arrive.

**Rules 1–6 mean the second decision cannot, by itself, make a destructive command possible by voice.** That is the answer to the brief's question, and it is why this design can be written at all before the owner answers Q1.

## 7. The threat model, with each attacker's capabilities stated

### 7.1 A malicious or compromised provider

**Capabilities.** It receives everything the app sends (audio in variant A/C, text in variant B); it can retain it, train on it, resell it, or leak it; it can return a **crafted transcript** for any audio; it can return a **crafted reply text** to be spoken; it can be honest for a year and stop being honest later; and it can do all of this while its published terms say otherwise, which Fugaro **cannot check** (`multi-model.md`: "Fugaro cannot read your account's data policy"; voice D44: "the console cannot verify what the provider retains").

**What it can achieve.** It learns the owner's speech, the project and repository names in it, and any secret spoken aloud (variant A/C), or the reply texts and therefore the project's state and job names (variant B). It can make the app speak a false reply (variant B).

**What it cannot achieve.** It cannot merge, cancel or confirm anything (§6). It cannot change the target of a pending confirmation (the read-back is recomputed). It cannot make the phone send anything while a confirmation is pending (§5 rule 2). It cannot reach a project that is not on the list (§5 rule 5). It cannot make the app send to *another* provider (§4.6). It cannot reach the mailbox, the database or the installation — it is a party the app calls, not a party with a grant.

**The residual risk, stated.** A provider that retains audio has the owner's voice, and no revocation recalls it. That is the risk the owner accepts or does not accept when answering Q1, and it is the reason §8's recommendation is conditional rather than enthusiastic.

### 7.2 A network attacker

**Capabilities.** Read and modify traffic; block it; delay it; observe metadata (that audio is being sent, to which provider, when, and how much); replay captured requests; present a certificate for a name it does not own if it can get one trusted.

**What the design requires.** TLS to a **named constant endpoint per provider name** — never a URL from the policy node (§4.1), because a policy-carried URL is a policy-carried redirection. A TLS failure is a **failed call**, not a downgrade: the app falls back to local and reports it, and there is no "continue anyway". No custom pinning is proposed in v1 — pinning has its own operational cost and this design does not claim it — and the honest statement of what that leaves is that a mis-issued certificate is a compromise of the confidentiality of that call, in the same way it would be for any other call the operator's machine makes.

**What it cannot achieve.** It cannot forge a transcript that confirms anything (§6). It cannot widen the allow-list (it cannot write the policy node). It cannot make the app send to a provider that is not named (§4.6). Its strongest real effect is **denial**: it can make every call fail, which costs the user nothing but the local path.

**Metadata is the part that is not protected**, and it is worth naming because it is the part a privacy-minded owner will care about: a network attacker learns *that* speech is being sent and *when*, even if it cannot read it. That is a property of the variant, not of the implementation.

### 7.3 A transcript injection that forges a command

This is the attacker the brief names, and it is worth separating from §7.1 because its capability is narrower and its target is precise.

**Capabilities.** Return a transcript containing a command the speaker did not say — "Fugaro, cancel the docs refactor", or "Fugaro, confirm". Return one that changes the *target* of a command the speaker did say. Return the fixed confirm phrase in response to an innocuous utterance, at a moment when a confirmation happens to be pending. Return a transcript with a wake word that was never spoken, which is the same attack one layer earlier.

**Why each fails.** §6's rules 1–5 cover the command half: the read-back is recomputed from the resolved target, the confirmation is a separate fixed phrase, an utterance carrying both halves is refused, the confirmation is time-bounded and single-use, and the consumer acts only on a MAC-verified action record. The **"confirm at a moment when a prompt is pending"** half is the sharpest version and is covered only by §5 rule 2 — and that is exactly why rule 2 is written as a freeze on the *state* rather than as a filter on the *phrase*. Without rule 2, a provider that returns the confirm phrase while a read-back is outstanding would be indistinguishable, at the router, from the owner saying it.

**What this design adds to the shipped modules: nothing.** The freeze is a rule about what the app sends, not a change to `shared/command` or `shared/confirm`. That is deliberate: §6's defences are shipped and reviewed, and a design that needed to change them to make a cloud path safe would be a design arguing against itself.

### 7.4 A malicious or compromised policy writer

**Capabilities.** An admin or budget admin writes `config/policy/voice/...` over IAM (`roles/firebasedatabase.admin`; G13: "Budget admins (D6) write /config over IAM"); a launcher mints a token for the consumer's uid (`token_minter` holds `iam.serviceAccounts.signJwt` and is bound to launchers and operators, G13). Both can therefore try to widen the allow-list or to impersonate the consumer.

**What they achieve and what they do not.** A widened node does cause the consumer to deliver a widened decision to the phone — so a launcher *can* widen the allow-list, and this design does not claim otherwise. What they cannot do is **forge the delivery**: the decision record is MAC-signed with the per-pairing key, which G13 records as "the only thing that tells the phone its own consumer from another launcher", and §3.5's `direction` coverage means a record reflected from the inbox into the outbox does not verify either. So the attack is "the installation's own operator widened a policy the owner wrote", which is the **accepted F38 risk** — "the agent already holds the operator's identity" — and it is not a new boundary. Stated rather than hidden.

**The one genuinely new thing a launcher gains** is the ability to *cause audio to be sent* where the owner's node said text only, by editing `direction`. That is why `direction` is a **separate field with its own default** rather than one flag per provider: an edit that widens text cannot silently widen audio, and the register row for each direction is separate.

### 7.5 A thief with the unlocked phone

Unchanged from the accepted risk. [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) §3.5: "Before revocation, whoever holds the unlocked phone can do what its owner can, **including saying 'confirm'** — the read-back does not stop a thief … **The real bound is the time to revoke.**" A cloud provider does not widen this: the thief already has the microphone and the screen. It does add one thing — the thief can spend the owner's provider credit, and revoking the device node does not stop a direct call to a provider from a key that is on the phone. That is a further reason §4.5 recommends the proxy rather than a key on the device.

### 7.6 What this design does not defend against

- **A provider that is honest today and not tomorrow.** The register records a reading with a date; nothing enforces the reading.
- **A compromised OS or a rooted device.** Out of scope for every part of this work.
- **The owner's own voice being recorded by someone else in the room.** Out of scope, and named only so that "local only" is not oversold.
- **A mis-issued TLS certificate** (§7.2).
- **Metadata** (§7.2).
- **The provider's own infrastructure being breached.** The register's retention column is the only thing that speaks to it, and it is the provider's claim.

## 8. Alternatives, and the recommendation

### A1 — local only, forever

**What it is.** D44's answer, made permanent: the cloud half is closed, the policy node is never built, and Check 66 (the console's cloud-provider check) and this document's Check 110 are never written.

**Cost.** If Check 85 shows the local engines miss D46's latency bar on the oldest supported device, then the app's main interaction is permanently below the bar for the users on that device, and the accessibility argument for cloud — a user whose local engine cannot understand them at all — has no answer. That is a real cost and it falls on exactly the users the app's hands-free mode exists for.

**When it is the right answer.** If Check 85 passes, this is not a cost at all: it is the answer, and this document's remaining value is the record of why.

### A2 — local with a cloud fallback for accessibility (recommended, conditionally)

**What it is.** Local first, always; cloud only when (a) the owner has taken the second explicit decision, (b) the project is on the policy node, (c) the provider is named and has a register row, (d) the device has recorded consent, and (e) the local engine is unavailable, below its bar on this utterance, or the user has explicitly asked. Split by direction (§3): **TTS-out (text) may be taken; STT-out (audio) should not be**, and variant C is refused outright.

**Why it is the recommendation, and the condition.** The accessibility case for cloud is the strongest case there is: a user whose device cannot hear them has no fallback at all, and no amount of latency tuning fixes a local engine that does not exist for their accent, their language or their device. **But the recommendation is conditional on Check 85**, in both directions: **if Check 85 passes the bar on the oldest supported device, the recommendation is A1** — do not take the second decision at all, because the cloud half then buys nothing that justifies §7.1's residual risk; **if Check 85 fails the bar, the recommendation is A2 with TTS-out only**, because that is the half where D118's promise survives (§3 consequence 2) and where the accessibility gain is real. **No sentence in this paragraph may be read as a recommendation to take the second decision now**, because the measurement it depends on has not been taken.

### A3 — cloud by default

**What it is.** The provider is used whenever it is available, and the local engine is the fallback.

**Cost.** Every cost in this document, paid by default rather than by choice: D118's "never stores or uploads audio" is reversed for every user of the feature; D5's no-telemetry posture and the app's "your machine stays your business" are false out of the box; and the failure mode is the one D44's own alternative column names — "would ship a feature that starts sending a colleague's speech off the machine the moment it is installed, with nothing in any settings file to show for it; that is the accident D5 exists to prevent". **Refused, and not merely not-recommended.**

## 9. The checks this would need

**Check 85 — the on-device STT/TTS bake-off — has not been run, and its text is not in the register.** The mobile work's table ([implementation-plan.md](../implementation-plan.md)) reserves it ("**85** | The on-device STT/TTS bake-off, with its latency numbers, on the oldest supported device | P5.3, P4.2 | Needs real audio hardware"), and **no `## Check 85` section and no summary row exists in [docs/gcp-live-checklist.md](../../gcp-live-checklist.md)**. It is written by **P4.2** (which writes 80 and 83–88), owner-run, on real hardware. **Until it runs, this document's recommendation is a conditional and the second decision has no number to be taken against.**

**The cloud half would need its own check, and the next free live-check number, read from the register, is 110.** The reading, so that a later reader can repeat it:

- **80–89 are fully allocated** — all ten rows are written in the mobile plan's table (80 mailbox end to end, 81 the write grant against the deployed rules, 82 the rules deploy, 83 replay, 84 presence, 85 the bake-off, 86 hands-free audio, 87 multi-account switching, 88 push, 89 a signed build). **There is no free number inside the block.**
- **90–99 belong to the credentials service** ("reserved for this work's owner-run checks; the plan assigns each number … 93, the declined in-runner cascade's check, reserved and unused").
- **100–109 belong to the functional spec v0.1** — 100–104 proposed, **105–109 "reserved and unused"**.
- **69a–69c and 70a–70c belong to the website work**, and 70c is the highest `## Check` section written in the checklist.
- **Nothing above 109 is claimed by any document or any open pull request**, so **110 is the next free number.**

**This document names 110 and writes nothing.** The convention is the register's own and the mobile plan's: "**the phase that ships the check writes them**", and "A check is never run before its text is in the register." The cloud-provider check would have to establish, on a real device against a real provider in a sandbox: that no audio leaves the device while a confirmation is pending; that a project absent from the policy node produces no call; that a provider outage produces a local answer and no queue; and that the indicator's state matches the route taken. **None of that is written here**, and the entry this change edits says so.

**One honest caveat, offered rather than resolved.** The voice work already holds **check 66** for "the cloud-provider check that can exist only if the owner takes the second egress decision (D44, D46)" ([voice.md](../../design/voice.md) §5.3). Whether the app's cloud path needs a check of its own (110) or is the *same* decision and therefore the *same* check (66, covering both surfaces) is **Q4**. This document names 110 because it may not take 66 — a reserved number is never reused — and because the app's surface (a phone, a device node, a policy node the console does not read) is not the console's.

## 10. What this adds to the plan (Phase 5, UNSCHEDULED)

Nothing in this document is scheduled, and nothing in it is approved. The task it elaborates is **[implementation-plan.md](../implementation-plan.md) Task P5.3**, whose entry this change edits to name the design, the two checks, and the reviewer tier. **The edit adds no task and moves no task out of UNSCHEDULED**; it replaces "**Files.** None yet" with the design's path, and it replaces "a cloud decision would need its own check" with the number this document names and does not write.

The work P5.3 would need, if it were ever scheduled, is: **a rules change** for `config/policy/voice/**` and its golden, parity test and `RulesVersion` bump (P2.1's shape, and its own SECURITY-SENSITIVE review); **a pure resolver in `shared/`** — `resolveVoiceEgress(policy, project, provider, direction) -> {allow, reason}` — with vectors and a mutation proof, in the shape of `shared/command` and `shared/confirm`, so that the decision is testable in CI with no phone, no microphone and no network (Global Constraint 10); **the signed delivery** of §4.2 (i) in the consumer, which is P4.1/P6.7's file and its own review; **the provider clients**, which voice §5.3's V7 already names as its own ("the provider clients, the policy list … the disclosure that this spend is outside the caps … and the privacy write-up") and which this design recommends be **reused from `shared/`** rather than written twice (D119: the shared package is what both surfaces consume); and **the register of §4.3**.

**Reviewer tier: SECURITY-SENSITIVE, mandatory independent review** — the same tier the entry carried before this change. The reasons are the policy node (a rules change), the delivery path (a MAC-signed record the phone trusts), the credential question (§4.5), and the fact that the failure mode of a mistake here is speech leaving a device that the owner's ruling said it would not.

## 11. What this document does not decide

- **It does not take the second explicit decision.** Voice D44's answer stands; nothing here is built; Q1 is the owner's.
- **It allocates no decision number.** Its questions are Q1–Q6.
- **It writes no check.** It names 110.
- **It amends no ruling.** It records that taking the cloud half would **amend Global Constraint 12** ("the app never stores or uploads audio") and **Global Constraint 4** ("exactly two items per account"), and that both amendments are the owner's to make in the form D129 requires — a separate, owner-approved, docs-only change that keeps the original text visible.
- **It does not reverse console D1, D2 or D14.** The app's direct-read grant is tension (b1)'s, and §4.2 (ii) is the only path here that would touch it, and it is not recommended.
- **It does not touch `belong`,** and it names no target project, repository or provider.

---

## Open for the owner

**Each of these is an open question, not a decision row, and none carries a D-number** — this document may not allocate one. Each states its options, the recommendation, and why. **Q1 is the gate: nothing in this design is built, and no other question is worth answering, until it is answered.**

### Q1 — The second explicit egress decision itself. Take it, or not?

Voice D44 answered local-only for v1 and reserved the cloud half for "a **second, explicit owner decision**". This is that decision, and it is asked in the conditional form the evidence supports.

- **(1) Do not take it. Local only stands; the cloud half stays closed; Check 110 is never written.** *Recommended until Check 85 reports.*
- **(2) Take it for TTS-out only** — the reply text may be synthesised off-device, the speech never is. *The recommended form if Check 85 fails the bar* (§3, §8 A2).
- **(3) Take it for both directions.** *Not recommended*: it is variant A, where D118's redaction cannot reach the payload.

**Recommendation and why:** **the recommendation is conditional on Check 85, and that is the point of this document.** If the bake-off shows the local engines meet D46's bar on the oldest supported device, the answer is **(1)** — the cloud half then buys an accessibility case that no measurement supports, at the price of §7.1's residual risk. If the bake-off shows they do not, the answer is **(2)** — the half where the payload is text, where the redactor has already run, and where the accessibility gain is real. **Answering this before Check 85 runs would be deciding against a hypothesis**, and D46's own answer says the numbers "are the ones the owner's second egress decision, if it is ever taken, is taken against".

### Q2 — Where the allow-list lives, and how the phone learns it

- **(1) The policy node of §4.1 (`config/policy/voice/**`, `TOKEN`-read, absent = local only), resolved by the consumer and delivered to the phone as a MAC-signed record in its own mailbox subtree, fail-closed to local.** *Recommended.*
- **(2) A fifth read family** — add `config/policy/voice/**` to the phone's user claim. *Cost: reverses tension (b1)'s "exactly four families, and nothing else" and the P2.2 assertions that go with it.*
- **(3) The console's local settings document**, which is what D44 literally answered for the console. *Cost: no per-project opt-in any teammate or second device can see; it is the right answer for the device's half and the wrong one for the installation's.*

**Recommendation: (1).** It is the brief's own shape ("a policy node like the other allow lists"), it copies `PathShared`/`PathRepo` and their grants exactly, it inverts `allow_data_to`'s default as D44 requires, and it needs no change to the read grant that tension (b1) settled one day before this document. **Its cost is stated and real**: the phone learns a widening only when the consumer is running, so cloud is unavailable while the laptop is away — which is when the app is most useful. If the owner would rather pay in the grant than in that availability, **(2)** is the alternative and it is a decision, not a detail.

### Q3 — Where the provider credential lives

- **(1) A Fugaro-hosted proxy holds the key; the phone holds a short-lived per-device token.** *Recommended.*
- **(2) The key in the phone's platform secure storage**, as a third named exception beside D54b and D111, amending Global Constraint 4.
- **(3) No key** (an unauthenticated endpoint). *Refused.*

**Recommendation: (1).** `SECURITY.md`'s "Cloud keys stay in the cloud" and the credentials service's own boundary ("the phone client backend … go[es] in sibling services") both point there, and it is the only option that survives a stolen phone (a revoked device node stops a token; it does not stop a key in secure storage). **The honest cost:** the proxy is a new always-on service, which is P5.1's subject and D115's territory — so taking the cloud half at all depends on more than D44, and this document says so rather than leaving P5.3's "Depends on" line as it was. **And the proxy is not a privacy win**: it moves the recipient from a third party to Fugaro's own service, and the audio still leaves the device.

### Q4 — Which check number the cloud half takes

- **(1) Check 110**, as this document names it, leaving the voice work's **66** to the console. *Recommended here.*
- **(2) Read D44's second decision as one decision with one check — 66 — covering both the console's V7 and the app.**

**Recommendation: (1), with the reasoning stated so the owner can overrule it cheaply.** 110 is the next free number after reading the register (§9), and the app's surface is not the console's: a different device, a policy node the console does not read, and a device-node revocation the console does not have. **But the counter-argument is real**: D44 is *one* decision, and if the owner takes it once, one check could prove both — in which case 66 is the number, and this document's 110 is released rather than reused. **This document may not take 66** (a reserved number is never reused), which is why it names 110 and asks.

### Q5 — Where the provider register lives, and who keeps it current

- **(1) A table in the design document that admits a provider**, owner-written, dated, with a column saying Fugaro cannot verify it (`multi-model.md`'s sentence), and a **refusal** for a policy node naming a provider with no row. *Recommended.*
- **(2) Fields in the policy node** (`retention`, `trains_on_data`, `read_at`). *Cost: prose in a node that no test can check, which invites a reader to treat it as verified.*
- **(3) Nothing recorded.** *Refused* — it is the brief's explicit requirement and it is the only thing that lets a later reader tell a reading from an assumption.

**Recommendation: (1), plus `read_at` in the node** (§4.1) so that a stale reading is visible where the decision is made. **Why the register cannot be a claim:** voice D44's cell and `multi-model.md` both say Fugaro cannot read a provider's data policy, and `fugaro doctor`'s precedent is to say so rather than to imply otherwise.

### Q6 — A cloud transcript, and the confirmation

- **(1) The §5 rule-2 freeze** — nothing leaves the device while a confirmation is pending — and the transcript is untrusted input throughout. *Recommended.*
- **(2) Require a typed confirmation on the phone whenever the utterance that produced it came from a cloud provider.**
- **(3) Accept a cloud transcript as a confirmation.** *Refused* (§7.3).

**Recommendation: (1).** It closes §7.3's sharpest attack (a provider returning the confirm phrase while a read-back is outstanding), it costs one rule rather than a new interaction, and it needs **no change to the shipped `shared/confirm` or `shared/command`** — which matters, because a design that had to weaken those to make a cloud path safe would be arguing against itself. **(2) is the defensible alternative** if the owner would rather pay in friction than in a state rule, and it is strictly stronger; it is not recommended only because the freeze already removes the path (2) guards, and a typed confirmation on every cloud-originated destructive action would make the hands-free mode useless in exactly the case the cloud half exists for.
