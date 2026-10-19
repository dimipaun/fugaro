# The local/remote voice switch (mobile P5.3)

**Status: design only; nothing is implemented.** No line of an Expo app, no rules change, no shared module, no provider client and no server-side hop exists for this document's subject, and none is added by the change that writes it. **Check 85 — the on-device STT/TTS bake-off — has not been run**: no `## Check 85` section and no summary row exists anywhere in [docs/gcp-live-checklist.md](../../gcp-live-checklist.md), no device was measured, and **no latency number exists**. Every sentence here that depends on latency is conditional on Check 85's measured results.

**This revision is written to the owner's ruling of 2026-10-19**, which is quoted in §1 and which **relaxes Voice D44 for evaluation only**. It is **not** D44's second explicit decision: adopting remote voice stays a later decision, taken after the owner has used both. This document allocates no decision number — its open questions are **Q1–Q4** at the end — writes no check (it names one, **110**), and amends no ruling in any other document. It was `docs/mobile/design/cloud-stt-tts.md` in its first revision; the earlier content is kept where it still holds, and §11 records what changed.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-19. **Amended:** 2026-10-19 (the evaluation switch).

---

## 1. The owner's ruling of 2026-10-19, and the problem it answers

The owner wants **an in-app switch between local and remote speech-to-text and text-to-speech, so that both can be tried and the choice made by using them.** The earlier Voice D44 answer — "local-only for v1", cloud only by a second explicit decision — stands for **adopting** remote voice; what the owner has approved is a way to **evaluate** it first. The seven approved points, in the owner's own numbering:

1. **Default local**; remote is an opt-in setting.
2. A **persistent in-app indicator** of where voice goes.
3. A **named provider allow-list**, each provider's **retention and training terms recorded**.
4. **No remote audio or text while a confirmation is pending, and never on any destructive-command path** — the Phase 6 rules stay as they are.
5. **The remote credential never ships inside the app binary** — a binary can be unpacked and the key read out — so there is a **server-side hop** (the existing budget/gateway pattern, or a small proxy), and **the app holds only its mailbox identity**.
6. **Local fallback when remote fails**; the interaction never blocks.
7. **Live Check 85's bake-off runs the same prompts through both** engines, so the owner's impression has numbers beside it.

Three questions the owner left open are **Q1–Q3** at the end: which providers to try; whether the hop reuses the budget gateway or is a new minimal proxy; and how the switch is persisted, including whether a project may force local-only.

**Why a switch and not a decision now.** D46's own answer is that the bake-off's numbers "are the ones the owner's second egress decision, if it is ever taken, is taken against". A switch turns that from a document into an experience: the owner can speak a real command, hear the reply, and *then* weigh the latency and the privacy rather than reading a table. The cost of a switch over a decision is that the code for both paths has to exist before either is chosen — which is why this document spends most of its length on what must be true **while remote is on**, not on whether remote is good.

## 2. What exists today (verified against `main`)

**Nothing in this document is a claim about a file that was not read.** Each sentence names the path and the symbol it rests on.

**Voice D44's answer stands, and it is the one this ruling relaxes.** [voice.md](../../design/voice.md) D44: "**Local-only for v1** … cloud only by a **second, explicit owner decision**, and then governed by a **list in the console's local settings whose syntax is `config.ModelProvider.AllowDataTo`'s and whose default is the opposite of it**"; "**absent means nothing leaves the machine**"; "**The list's unit is *the project*, not the repository**". The same cell states that "**`allow_data_to` does not cover this today**". Its V7 phase — "cloud providers, only if the owner takes the second egress decision … the provider clients, the policy list (**deny by default**, D44), the disclosure that this spend is outside the caps (as D45's is), and the privacy write-up" — is **out of v1 and unbuilt**, and its register holds checks **65 and 66**, "held for the signed-build microphone check and for the cloud-provider check that can exist only if the owner takes the second egress decision (D44, D46)". **A switch that reaches a provider for evaluation goes beyond what V7's phase gate permits, which is exactly why it needs the 2026-10-19 ruling in writing and why it is not the second decision.**

**The local-only rule is written as a global constraint, not an implication.** [implementation-plan.md](../implementation-plan.md) Global Constraint 12: "**The app never stores or uploads audio** (D118, answered 2026-10-17 as recommended). STT is on-device; the mailbox carries text."

**The app holds exactly two credential items per account.** Global Constraint 4: "The app holds exactly two items per account, both in platform secure storage, both never logged and never in a settings file: a Firebase Auth ID token, and — only if D113b's pairing-key question is answered (a) — the per-pairing MAC key … Never a Fugaro or cloud admin credential, never an LLM key, never a service-account JSON, never a `fugaro` config file." Voice D42's cell is the same rule on the console's side and names why a provider key on a machine is a decision and not a detail: `SECURITY.md`'s "**Cloud keys stay in the cloud.**"

**Point 5 keeps that constraint intact, and that is its whole value.** A provider key in the app would be a **third** item; a server-side hop means the app authenticates with the **Firebase Auth ID token it already holds** (item (1)), and no new item is added at all. §4.4 states the mechanism.

**The project policy node exists, and under the rules it is read by a run's token, not by a user.** `internal/projectpolicy/paths.go`: `PathShared()` is `config/policy/shared`, `PathRepo(slug)` is `config/policy/repos/<escaped slug>`, `PathRepos()` is the parent, `PathRulesMarker()` is `config/limits/policyRules`, `PathAdmin()` is `config/admin`; the file's comment records that a read of `config/policy` itself is denied by the rules because "an RTDB read grant cascades to children, so a read on the parent would make every child readable by every run token". `internal/budget/rules/rules.json.tmpl` is where that is true: `config/policy/shared` is `{ ".read": "TOKEN", ".write": false }` and `config/policy/repos/$slug` is `{ ".read": "TOKEN && auth.token.fs == $slug", ".write": false }`, and `config/admin` is `{ ".read": false, ".write": false }`. **There is no `USER` in any of them** — the phone's user claim cannot read the policy tree, and it cannot read its own device node either.

**The rules are not the whole story, and the difference decides §4.4.** The operator's own CLI reads and writes this tree under its **application-default credential** (`internal/cli/cloud.go`'s `google.DefaultTokenSource` feeding `rtdb.New`), which is an **admin** credential that **bypasses the rules** — D113b's honesty note, and `internal/infra/firebasedb.go`'s `dbErr` says so in as many words. A server-side hop can hold the same kind of access; a phone cannot.

**The phone's read grant is exactly four families.** [reconciliation.md](../reconciliation.md) §4, tension (b1), resolved 2026-10-17: only `config/caps/**`, `spend/$day/**`, `config/kill/**` and the phone's own mailbox, "all in `internal/budget/model.go`. **Not `agents/$slug/$run`** … and **no new `.read` on `outcomes/**`** … until a separate decision of the owner says otherwise", with "P2.2's emulator cases must assert the read denial for every family not named here: `agents/**`, `outcomes/**`, **the rest of `config/**`** …". **The phone's write grant is two paths** (Global Constraint 6: its own inbox messages, and the ack on a message it has already spoken), and "the **heartbeat is not the phone's write**".

**The mailbox subtree is merged and validates four fields.** `internal/budget/rules/rules.json.tmpl`'s `mailbox/$mb/{inbox,outbox,heartbeat}`: inbox and outbox carry `newData.hasChildren(['id', 'sender', 'text', 'at'])` with `"$other"` denied, and the heartbeat admits only `at` and `boot`. The `mac` and `kind` fields are **P6.4's**, not shipped. `shared/mailbox-schema/index.js` is where they are specified: `REQUIRED_FIELDS` is `['id', 'sender', 'text', 'at', 'mac']`, `KINDS` is `['message', 'prompt', 'answer', 'action']`, `ACTION_REQUIRED` is `['id', 'verb', 'target', 'confirmation']`, `HEARTBEAT_REQUIRED` is `['at', 'boot', 'mac']`, and `canonicalBytes(record, direction)` covers a `direction` the reader supplies rather than the record stores.

**The routing and confirmation logic is shipped, pure, and already distrusts its input.** `shared/command/index.js` exports `routeUtterance({utterance, aliases, projects, agents})` and `ROUTE` (`APP_COMMAND`, `AGENT_TEXT`, `NOT_UNDERSTOOD`). `shared/confirm/index.js` exports `step(state, event)`, `readBack(verb, target)`, `isConfirmPhrase(text)`, `pending(state, now)`, `PHASE` (`idle`/`pending`/`crowded`), `DESTRUCTIVE_VERBS` (`cancel`, `confirm`, `merge`), `PASS_THROUGH_VERBS` (`launch`), `REFUSED_VERBS`, `DEFAULT_CONFIRM_TIMEOUT_MS` (60 s) and `REFUSAL`. Two of its stated rules are load-bearing in §5, in its own words: "the **read-back text is built from the resolved target and from nothing else** — never from the transcript … It is recomputed, never stored, so a state handed back with an edited read-back cannot be spoken", and "a confirmation is a **separate utterance** … recognised as the **fixed phrase**".

**The mailbox schema's other half is the consumer's.** [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) §3.5: "**An answer to a destructive prompt is accepted only as a MAC-verified action record carrying a confirmation** — never as free text and never as a plain `answer`", and "**the rules (P6.4) only require the field to be present; they cannot verify an HMAC**" — verification is the consumer's, in `fugaro mailbox`. G13 records that "**any launcher can mint a token for any `mu` or `mc`**", that a launcher holds `roles/firebasedatabase.viewer` "an IAM read that bypasses the rules — so mailboxes are readable by every launcher", and that "the per-pairing MAC key … remains the only thing that tells the phone its own consumer from another launcher".

**The gateway pattern point 5 names is shipped, and the external service it would reuse is not.** `internal/gateway` holds the **in-runner loopback** gateway: `gateway.go`, `route.go`, `forward.go`, `sse.go`, `vertex.go`, `lease.go`, `ledger.go`, `log.go`. [m9-budget-and-dashboard.md](../../design/m9-budget-and-dashboard.md) describes it: "The gateway runs inside the runner … and listens on `127.0.0.1`" and "The gateway holds the real credential; the agent's environment no longer does", with §11's later hardening being an **external** gateway. That external service — the credentials service — is **plan only**: [docs/credentials-service/README.md](../../credentials-service/README.md) says "**Status: plan only; nothing is implemented**", and its plan lists `internal/credsvc/auth.go` (identity verification), `forward.go`, `server.go` and `internal/gateway/export.go` as **new** files. **There is no `internal/credsvc` directory today.** Its spec fixes the boundary this document must respect: "**The service stays secrets-only. Other always-on features (e.g. the phone client backend) go in sibling services that call it**, keeping its attack surface minimal", and "The service is the single holder of every secret a **job** needs" — a phone is not a job.

**The provider-terms problem is already stated honestly for model providers.** [docs/multi-model.md](../../multi-model.md): "**`require_zdr` is a price, not an enforcement — Fugaro cannot read your account's data policy** … and `fugaro doctor` says so (OpenRouter's key endpoint, `GET /api/v1/key`, has no data-policy field to read)". [voice.md](../../design/voice.md) draws the conclusion for STT/TTS: "The same will be true of an STT/TTS provider: the console cannot verify what the provider retains."

**The redactor works on text, and the titles the feed carries are sanitised.** `internal/agent/redact.go`'s `NewRedactor`, `Redact(s, secrets)` and `RedactFunc(secrets)`; D118's answer routes the mailbox through it. `internal/watch`'s `CleanLine(s, 80)` clips and sanitises titles, pinned by `internal/watch/build_test.go`'s `TestHostileTitlesSanitized` and `internal/watch/finished_test.go`'s `TestFinishedRowSanitisesHostileTitle`.

**Costs outside Fugaro's caps are an existing, disclosed category.** Voice D45: the summarizer's spend "is not accounted by Fugaro's caps, and it is disclosed". `SECURITY.md`: "**The backstop is the provider account's own limit**: set a credit limit on every key you give Fugaro", and "**Budget mode defaults to off.**"

**`belong` is never touched** (Global Constraint 11) and **no test may need a live cloud, a real device or a real microphone** (Global Constraint 10).

## 3. What the switch switches, and the one direction that matters

Three variants were named in this document's first revision and the split still holds:

| variant | what leaves the device while it is on | can a redaction rule reach the payload? |
|---|---|---|
| **remote STT** | **raw audio**, plus the returned transcript's text | **no** — redaction is a function of a string, and audio is not a string |
| **remote TTS** | the **reply text**, which the redactor and the reader rules have already been applied to | **yes** |
| **a remote wake word / always-listening** | a **continuous stream**, including everything said when no command was intended | **no**, and §5's never-send list could not be written at all |

**The transcript is already off the device; the audio has never been.** D118's answer writes the redacted text into the mailbox, and G13 records that every launcher can read a mailbox over IAM. What the switch adds is a party **outside the installation**, with no grant, no TTL and no redaction Fugaro controls — and, in the remote-STT variant, a payload that is **not in the mailbox and never has been**, and that no rule can redact.

**So the switch is offered per direction, and the default on both is local.** The owner's point 1 is "default local"; this document reads the two directions as **two switches** rather than one, because they are two different privacy propositions and the owner's evaluation may reach different answers for them. A remote wake word is **refused** and is not on the switch at all.

## 4. The design

### 4.1 The switch

- **Two settings, per account, on the device: `stt` and `tts`, each `local` (default) or `remote`.** Never `auto`-by-default: an unset switch is local, and a remote call happens only after the user has turned it on and recorded consent for a named provider (§4.3).
- **The switch never changes what a command *means*.** It changes which engine produced the text (STT) or the speech (TTS). §5's rules and §6's confirmation chain are unchanged, and §5 rule 3 is what keeps a destructive path off the remote engine even when the switch is on.
- **A per-project policy can force local**, and it wins over the device (Q3): a project that forbids remote is served locally on every device, whatever that device's setting says. **The enforcement is at the hop (§4.4), not on the device** — the app holds no grant on `config/policy/**` (§2) and cannot decide this for itself, so a stale cache, a modified client or a device whose owner wants remote cannot widen what the hop admits. **The limit of that, stated here exactly as §4.4 states it:** the hop takes the project *name* from the request, so this is a guard against accident and stale state rather than a boundary against the device's owner. The two sections say the same thing, and neither softens the other.
- **Persistence is device-local, not the database** (Q3's recommendation, and §4.6 gives the reason the device node cannot be the place).

### 4.2 The indicator

- **Persistent, in the voice screen, and visible before anything is spoken.** It shows exactly one of **local**, **remote (<provider name>)**, **blocked by project policy**, and while a call is in flight it shows that. It is not dismissible and it is not a toast. **"Blocked by project policy" is reachable because the hop answers a project-status query (§4.4)**; until that first answer arrives the app shows **local — checking**, never a guess.
- **Every utterance in the transcript is tagged with the engine that produced it**, so the evaluation the owner wants has a per-utterance record and not only a global badge.
- **Its state is a pure function of four inputs the app actually holds**: the two switch settings, the recorded consent, **the hop's last project-status answer** (§4.4), and the last call's route. It is deliberately **not** a function of the policy tree — the app cannot read `config/policy/**` (§2, §4.3), so it learns the project's effective setting the same way it learns everything else about the project, from the hop, and caches the answer. That keeps the function pure and testable in CI with no phone, no microphone and no network (Global Constraint 10, and `shared/`'s precedent). This is the one new piece of pure logic this design requires, and it belongs in `shared/` beside `shared/command` and `shared/confirm` (D119).
- **It is the honest replacement for a promise the switch makes false while it is on.** [voice.md](../../design/voice.md) records the app's promise as "your machine stays your business"; with remote on, that sentence is false for the duration of a call, and a badge that cannot be hidden is the minimum that keeps the app honest while it is false.

### 4.3 The provider allow-list and the register

**A new node beside the policy tree, read by `TOKEN` under the rules and by no user claim:**

```
config/policy/voice/shared            { ".read": "TOKEN", ".write": false }
config/policy/voice/repos/$slug       { ".read": "TOKEN && auth.token.fs == $slug", ".write": false }
```

This copies `config/policy/shared` and `config/policy/repos/$slug` exactly (`internal/budget/rules/rules.json.tmpl`, `internal/projectpolicy/paths.go`'s `PathShared`/`PathRepo`), including the reason the parent gets no grant of its own: `PathShared`'s comment says a read of `config/policy` itself is denied by the per-child rules because "an RTDB read grant cascades to children, so a read on the parent would make every child readable by every run token" — and the children in question are `shared` and every `repos/$slug`, so a parent grant would make **every repository's** policy readable by **every** run. (`config/admin` is a sibling of `config/policy`, not a child, and a grant here does not reach it either way.) The unit is **the project** (D44's own unit) and the slug is the same slug `PathRepo` escapes.

**Default deny, and the default is the opposite of `allow_data_to`'s.** Absent, empty, malformed or unreadable all mean **local only** — the same four answers, because they are indistinguishable to a caller that must not guess. The syntax is `allow_data_to`'s (`internal/config/providers.go`'s `ModelProvider.AllowDataTo`, read by `AllowsData(repo)`, named in prose by `OpenToEveryRepo` and `OpenToEveryRepoReason`, with `ValidateModelProviders` refusing the ambiguous empty list) and the default is inverted, which is what D44 asked for.

**An entry is a provider name, never a URL** — a policy-carried URL is a policy-carried redirection — and `direction` is its own field so that an edit widening text cannot silently widen audio:

```
config/policy/voice/repos/<slug> = {
  providers: ["<name>", ...],             // absent or [] => local only
  direction: { stt: false, tts: false },  // absent => both false
  force_local: false,                     // true => every device serves this project locally
  read_at: "<date the owner read the terms>"
}
```

**The register records what the owner read, and it records that it is a reading.** One row per admitted name, with the provider's stated retention and training terms, the price, the languages, the streaming latency and the date it was read. **The register lives with the hop** — installation configuration beside the provider key — and **not** in the policy node, which carries only the names and the two switches, and **not** in the app, which has no grant to read anything of the sort and does not need the terms to do its job. **So the refusal is the hop's**: a name with no register row is refused there, and a name the project does not admit is refused there as well; the app refuses only what it can know by itself — a provider for which it holds no recorded consent. Fail-closed at both ends, in the same direction. **The register cannot be a claim about the provider**: `multi-model.md`'s sentence ("Fugaro cannot read your account's data policy") and D44's ("the console cannot verify what the provider retains") are both true here, and `fugaro doctor`'s precedent is to say so.

§7 is the register's first draft: it fills in what could be read from the providers' public documentation on 2026-10-19, and marks everything else **UNVERIFIED** rather than guessing.

### 4.4 The server-side hop

Point 5: **the remote credential never ships inside the app binary.** The hop is the whole answer to that, and it is also what keeps Global Constraint 4 intact: **the app adds no credential.** It authenticates the way it already authenticates to everything else — with the **Firebase Auth ID token** that is item (1) of D111's two items.

**The shape, stated as the design's proposal:**

1. **The app calls a Fugaro-hosted endpoint** with `Authorization: Bearer <Firebase ID token>` and the audio (STT) or the text (TTS) as the body. It sends **no credential of its own**, and it names the project it is speaking about.
2. **The hop verifies the ID token itself** — signature against Google's published keys, issuer and audience, and the uid's shape `m~` (D165/D166's device uids; never `r~`) — which is the verification `internal/credsvc/auth.go` is planned to do for a run's identity (C1.10), applied to a device instead of a job.
3. **The hop checks the device node exists** at `config/admin/devices/$uid` (D166) **over IAM**, because the rules cannot be consulted by a Cloud Run service and because that node is what revocation deletes: **revoking the device denies the next call immediately**, which is the property D166's answer was taken for.
4. **The hop holds the provider key in the installation's Secret Manager**, in the shape `deploy/terraform/gcp/modules/installation/secrets.tf` already provides, and the app never sees it.
5. **The hop enforces the project policy** (§4.3) — `force_local`, the admitted provider names and the direction — and **refuses** a call the policy does not admit. **This is where a force-local project is actually enforced**, not in the app.
6. **The hop forwards and streams**, in the shape `internal/gateway`'s `forward.go`, `sse.go` and `route.go` already implement for model calls, and **logs no audio and no transcript**: the precedent is `internal/gateway`'s `callLog` (`log.go`), whose own comment says it "holds no header but the two Claude Code IDs and **no byte of any body**: only the error type of an upstream error". A voice log line is the same shape, and a transcript is emphatically a body.
7. **The hop answers a project-status query** — the same authenticated call, carrying no audio and no text — returning what is effective for that device and that project: whether remote is admitted at all, the provider names admitted, the direction, and the reason for a refusal (`force_local`, no register row, no admission). **This is the app's only source for "blocked by project policy", and what the indicator displays (§4.2).** The app asks it when the switch changes, when it comes to the foreground and after pairing, and caches the answer. **It is a cache of a server answer, not a copy of the policy**: the app still never reads `config/policy/**`, and a stale answer is harmless because **the hop refuses on the call itself**, not on what the app believed.

**Two honest limits, stated rather than hidden.**

- **The hop takes the project from the request, and a modified client could misname it.** The policy check is therefore a **guard against accident, not a boundary against the device's owner** — the same posture as D113's "the mailbox is a conversation channel", and the same sentence §4.1 uses. What the hop *can* bind to an authenticated fact is the **installation** (from the uid) and the **device's liveness** (from D166's node); the project is a claim.
- **The hop is a new receiver of the audio.** It moves the recipient from the provider to Fugaro's own service — a smaller move than it sounds, and **not a privacy win**: the audio still leaves the device, and it now reaches two parties instead of one. The hop's value is the credential (§4.4's point 4) and the policy enforcement (point 5), not confidentiality.

**Who the hop is — reuse or new — is Q2**, with the costs priced there.

### 4.5 Failure modes: local fallback, never blocking

Every row is **fail-closed to local**, and none blocks the interaction:

| what fails | what happens |
|---|---|
| the provider is down, slow, or returns an error | the hop reports it; the app answers from the local engine; the indicator shows **local**; **nothing is retried against a second provider** unless the owner named that provider for that project, because a retry against another provider is the same speech to a second third party |
| the hop is unreachable or the network is down | local only, immediately; **nothing is queued for later**, because a queued utterance is a recording, and D118's answer forbids one |
| the ID token is expired and cannot be refreshed | local only; the app says so and offers the pairing/overview screen |
| the device node is gone (revoked) | local only, and the app says the device is revoked |
| the policy refuses the project, or the policy node cannot be read | local only (§4.3's four answers) |
| the project-status query fails, or has not answered yet | the app shows **local — checking** and serves locally (§4.2); the hop would still refuse on the call itself |
| the device has no consent recorded for the provider | local only, and the consent sheet is offered |
| the local engine also fails | the app says so and offers **text entry** |
| the redactor matches (§5 rule 4) | the utterance is not sent |
| a confirmation is pending (§5 rule 2) | no egress at all |

**The last two rows are rules, not fallbacks**, and the no-queue row is the one most likely to be got wrong by a well-meaning implementation.

### 4.6 Where the switch is persisted, and why not the database

- **Recommended: device-local storage**, per account, in the app's own settings — **never written to the database**. D127's answer is the precedent and gives the reason: a per-device preference "never reaches the database", and writing it would cost "another phone write and another grant (D113)".
- **The device node cannot hold it.** `config/admin` is `{ ".read": false, ".write": false }` in the rules, so **the phone cannot read its own device node**; that node exists for the rules' `USER` predicate (D166) and for `fugaro pair ls`, not as a place the app reads. A setting stored there would be write-only from the app's point of view.
- **The project's half is the policy node** (§4.3), which the hop reads and enforces. So the two halves are stored where each is authoritative: the user's choice on the device, the project's prohibition in the policy node.
- **Cost of device-local, stated:** a second device does not inherit the setting, and re-installing loses it. That is the price of not adding a phone write, and it is the price D127 already accepted.

### 4.7 The bake-off: Check 85 runs both

Point 7. Check 85 is written by **P4.2**, whose row on `main` reads "**85** | The on-device STT/TTS bake-off, with its latency numbers, on the oldest supported device | P5.3, P4.2 | Needs real audio hardware" (**as first written**; this change amends that row in place with the 2026-10-19 scope, keeping the original text visible), and **has not been run**. Under this ruling it changes shape: **the same fixed set of prompts goes through the local engine and through each admitted remote provider**, and the table carries **D46's own three numbers per candidate** — **end-of-turn latency** (measured here as first partial transcript *and* as end-of-speech to spoken reply), **cost per hour of conversation**, and **error rate on the identifiers this tree actually uses** (project, repository and recipe names, the strings `internal/watch`'s `CleanLine(s, 80)` clips and sanitises) — plus **custom vocabulary, which D46's cell scores in the same table**, and **the language result**, which the same cell calls "**a hard filter, not a score**": if Romanian is needed, a candidate without it is out.

**The prompts are fixtures, prepared off-device, and the owner speaks them.** No fixture is ever recorded from `belong` (Global Constraint 11), and no test needs a device (Global Constraint 10). **The result feeds Q1**: the owner's impression with numbers beside it, which is the whole point of running the switch rather than writing a decision.

## 5. What the app must never send, and the destructive-command rule

A list, and each entry is a rule a test can hold:

1. **A spoken confirmation's audio**, and anything while a confirmation is pending (rules 2 and 3 below make this structural rather than a filter).
2. **Nothing at all while a confirmation is pending** — not "the confirmation", not "the audio of it": **nothing**. This is written as a rule about a *state*, not about a *phrase*, because the app cannot know which utterance will turn out to be the confirmation until it has been transcribed, and by then the audio is gone.
3. **Nothing on a destructive-command path, and the read-back and the confirmation are always local.** The mechanism: **the local engine always runs when it is available**, and its route decides whether the utterance is on a destructive path (`shared/command`'s `routeUtterance`, then `shared/confirm`'s verbs); a destructive route is served **entirely locally** — the read-back is rebuilt from the resolved target by `readBack(verb, target)`, the read-back is spoken with local TTS, and the confirming utterance is transcribed with local STT. **The residual, stated honestly:** if the local engine cannot hear the user at all, the *first* utterance ("Fugaro, cancel the docs refactor") was transcribed remotely before anything knew it was destructive — the app cannot classify an utterance before transcribing it. Rule 2 still holds from the moment a read-back exists, so **a remote engine can never be the source of the utterance that confirms**.
4. **Anything matched by a redaction rule.** The digest's redactor (`internal/agent/redact.go`) runs **before** egress. On the TTS path a hit is masked in the text sent; on the STT path a hit **cannot** be masked — audio is not a string — so **a hit means the utterance is not sent**, and the local engine serves it. A design that could not express that difference would be a design that had not understood §3.
5. **Anything while no command is intended.** No continuous streaming, no remote wake word, no ambient buffer (variant C, refused).
6. **Speech about a project that is not on the list**, including a project named in the same utterance as one that is.
7. **Another installation's speech.** `belong` is never a target (Global Constraint 11).
8. **The console's traffic.** The console's voice module is a separate surface with its own V7 phase; the phone must not become its egress.
9. **Any credential** — the pairing MAC key, the ID token, and above all any provider key, which never exists on the device at all (point 5).
10. **Any audio the user has not just spoken as a command.** A "keep the last 30 seconds in case" buffer is an audio spool by another name, and D118 forbids one.

## 6. Why a destructive command stays impossible by voice, whatever the provider returns

**The switch changes where the text came from. It changes nothing about what authority the text has**, and that is a property of the shipped modules:

1. **The read-back cannot be poisoned.** `shared/confirm`'s rule: the read-back "is built from the resolved target and from nothing else — never from the transcript … It is recomputed, never stored". A provider that returns a transcript naming a different target cannot change what is read back.
2. **The confirmation is a fixed phrase as a separate utterance** (`isConfirmPhrase`), and an utterance carrying both a command and a confirm token is refused (`command-and-confirm`).
3. **A confirmation is time-bounded** (60 s default) and **authorises exactly one action** through the ledger.
4. **Only a MAC-verified action record runs a destructive verb** (§3.5; the consumer verifies, not the rules).
5. **A provider's transcript is untrusted input of the same class as agent-written text**, which `shared/command` already treats as routed rather than trusted.
6. **A remote engine can never be the source of a confirm**: §5's rule 2 freezes all egress while a confirmation is pending, and rule 3 keeps the read-back and the confirmation local.

**Rules 1–6 mean the switch cannot, by itself, make a destructive command possible by voice** — which is why the owner could approve the switch while leaving the adoption decision open.

## 7. The register: providers, as read on 2026-10-19

**What this section is.** The owners' point 3 asks for a named allow-list with each provider's retention and training terms recorded. This is that register's first draft. Every entry below was read from the provider's own public documentation on **2026-10-19**, quoted, with the URL. **Anything not read is marked UNVERIFIED**, and no number here is inferred, averaged or guessed. **A provider's own page is a claim by that provider** — `multi-model.md`'s sentence applies — so the register records the claim and not a verification of it.

**Three candidates, chosen for what the switch needs** (a streaming STT path, a TTS path, and a provider the installation can already reach):

**1. Google Cloud Speech-to-Text and Text-to-Speech.** Read: [Data logging](https://cloud.google.com/speech-to-text/docs/data-logging) — "**By default, Cloud Speech-to-Text does not log customer audio data or transcripts.** However … you can opt into the data logging program. This program allows Google to use logged customer data to refine and improve the service quality of Cloud STT — and in exchange, you gain access to discounted pricing", and "**Your logged data is not deleted when you delete your project.** Fill out the deletion request form to request to have your data deleted." [Supported languages](https://cloud.google.com/speech-to-text/v2/docs/speech-to-text-supported-languages) — **`ro-RO` (Romanian, Romania) is listed, with `chirp` and `chirp_2`**. [Text-to-Speech pricing](https://cloud.google.com/text-to-speech/pricing) — a per-character rate was read ("**US$30 per 1 million characters**", Instant custom voice); **the rest of the TTS rate card and the whole STT rate card did not render extractably on 2026-10-19 and are UNVERIFIED** (both pages are client-side). **Retention:** the default is no logging and thus no Google-side copy, which is the strongest of the three entries; opt-in logging changes that materially and is **not** to be enabled for this feature. **Training:** not on by default; the opt-in programme trains and Google "retains ownership of the models trained from that data" — another reason the option stays off. **Streaming:** `chirp`/`chirp_2` are Google's streaming-capable models; **latency UNVERIFIED**. **Cost note:** an installation's GCP project already holds Secret Manager and a data-processing relationship, which is the practical argument for this one.
**2. Deepgram.** Read: [pricing](https://deepgram.com/pricing) — streaming, pay-as-you-go, per minute: **Flux English $0.0065/min** (regular $0.0077), **Flux Multilingual $0.0078/min**, **Nova-3 Monolingual $0.0048/min** (regular $0.0077), **Nova-3 Multilingual $0.0058/min** (regular $0.0092). The page describes Flux as "**Conversational speech recognition for real-time voice agents** … with built-in turn detection, natural interruption handling, and ultra-low latency", which is the property D46's end-of-turn criterion is about. **Retention and training: UNVERIFIED** — its privacy policy was not retrievable on 2026-10-19 (the URL fetched returned a 404) and this document will not guess. **Languages:** Nova-3 Multilingual advertises "automatic language detection"; **Romanian specifically UNVERIFIED**. **Latency UNVERIFIED**: "ultra-low latency" is the vendor's phrase, not a number, and Check 85 is where a number comes from.
**3. ElevenLabs (TTS only).** Read: [privacy policy](https://elevenlabs.io/privacy-policy) — the retention section reads "**We generally keep your personal information for as long as necessary to provide you with our Services or as otherwise required or permitted by law** … After this we will either securely delete or de-identify it", which is an **open-ended retention statement with no window**, and the training section describes training on "**publicly available or third-party information**" rather than on customer audio. **Price, languages, streaming latency: UNVERIFIED** (its pricing page fetched but rendered client-side, so no number was read). **This entry's retention is the weakest of the three and its row should say so**; it is proposed for the TTS direction because voice quality is the whole reason to use it, and the register exists so that trade-off is visible.

**What is deliberately not proposed.** **OpenAI's audio APIs** — the data-usage policy page returned **403** on 2026-10-19, so its retention and training terms could not be read, and this document will not state them from memory. If the owner wants it in the set, it is added when its terms are read. **A remote wake word** is refused (§3), and no provider is proposed for it.

**The register's columns are the owner's to fill.** This draft exists so that Q1 has a starting point with sources rather than a list of brand names.

## 8. Threat model, with each attacker's capabilities

**A malicious or compromised provider** — *capabilities:* receives everything sent (audio or text), can retain, train, resell or leak it, can return a **crafted transcript** or a **crafted reply text**, can be honest for a year and stop later, and can do all of this while its published terms say otherwise, which Fugaro **cannot check**. *Achieves:* it learns the owner's speech, project and repository names, and any secret spoken aloud (remote STT), or the reply texts and therefore the project's state (remote TTS); it can make the app speak a false reply (remote TTS). *Cannot achieve:* merge, cancel or confirm anything (§6); change the target of a pending read-back (recomputed); be the source of a confirm (§5 rule 2); reach a project the policy does not admit (§4.4); make the app call another provider (§4.5). *Residual, stated:* a provider that retains audio has the owner's voice, and no revocation recalls it. That is the risk the switch lets the owner **price by use**, and the adoption decision is where it is accepted or refused.

**A network attacker** — *capabilities:* read and modify traffic, block, delay, replay, observe metadata (that audio is being sent, to which provider, when, how much). *Design requirement:* TLS to a **named constant endpoint per provider name**, never a URL from the policy node; a TLS failure is a **failed call**, never a downgrade, and there is no "continue anyway". No certificate pinning is proposed in v1 and this design does not claim it: a mis-issued certificate is a compromise of that call's confidentiality, as it would be for any other call the machine makes. *Cannot achieve:* forge a transcript that confirms anything (§6); widen the allow-list; make the app call a provider that is not named. *Strongest real effect:* denial — every call fails, which costs the user nothing but the local path. *Not protected:* **metadata**, which is a property of the variant, not of the implementation.

**A transcript injection that forges a command** — *capabilities:* return a transcript containing a command the speaker did not say, or one that changes the target of a command they did say, or the fixed confirm phrase in response to an innocuous utterance while a confirmation happens to be pending, or a wake word that was never spoken. *Why each fails:* §6's rules 1–5 cover the command half; the "confirm while a read-back is outstanding" half is closed **only** by §5 rule 2, which is exactly why that rule is written about a *state*. *What this design adds to the shipped modules:* **nothing** — the freeze is a rule about what the app sends, and a design that needed to change `shared/command` or `shared/confirm` to make the switch safe would be arguing against itself.

**The hop's own attacker surface, stated** — the hop is a new internet-facing endpoint. Its capabilities are limited by construction: it verifies an ID token, checks one database node over IAM, reads one policy node, and holds one provider key. It logs **no audio and no transcript** (§4.4 point 6), for which `internal/gateway`'s `callLog` (`log.go`) is the precedent — its own comment says it holds "no byte of any body". It is a **secret holder**, and the credentials service's own boundary says the phone's backend "go[es] in sibling services" rather than into the secrets-only service — Q2 prices which of the two it becomes.

**A launcher, admin or budget admin** — *capabilities:* writes `config/policy/voice/...` over IAM (`roles/firebasedatabase.admin`; G13: "Budget admins (D6) write /config over IAM"); mints a token for any uid (`token_minter` holds `iam.serviceAccounts.signJwt`, bound to launchers and operators). *Achieves:* widening the allow-list, including the `direction` field — so a launcher **can** cause audio to be sent where the owner's node said text only. That is why `direction` and `force_local` are **separate fields with their own defaults**, so an edit that widens text cannot silently widen audio. *Cannot achieve:* anything the owner cannot — this is the accepted F38 risk ("the agent already holds the operator's identity"), and mailboxes are readable by every launcher over IAM today (G13).

**A thief with the unlocked phone** — unchanged from the accepted risk: [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) §3.5, "whoever holds the unlocked phone can do what its owner can, **including saying 'confirm'** … **The real bound is the time to revoke.**" The switch does not widen it: the thief already has the microphone and the screen. It does not widen it with a **credential** either, which is point 5's payoff — there is no provider key on the device to steal, and revoking the device node (§4.4 point 3) denies the next call.

**What this design does not defend against:** a provider honest today and not tomorrow; a compromised or rooted OS; someone else in the room recording the owner; a mis-issued TLS certificate; metadata; a breach of the provider's own infrastructure (the register's retention column is the provider's claim and nothing more); and a modified client that misnames the project (§4.4).

## 9. Cost

- **The spend is outside Fugaro's caps and is disclosed as such** (D45's shape; voice §5.3's V7 lists exactly this for the console). Fugaro cannot meter it against `spend/$day/**`: the phone is not a run and holds no budget token — the token channel is per-run and consumed once (`internal/budget/token/object.go`'s `TakeObject` over `ObjectKey(slug, run)`), and nothing in this design proposes to widen it. **A provider spend cap belongs on the provider account, which is the backstop `SECURITY.md` already names.**
- **The hop is a cost of its own**: a Cloud Run service with min instances 0 has the shape the credentials service prices ("min instances 0 with request-based billing, so it costs nothing between reviews; expect about a second of cold start"), and a cold start is exactly what a voice interaction cannot afford — a real, measured cost that Check 85 sees and a document cannot.
- **The number that matters does not exist yet.** D46's bake-off records "cost per hour of conversation"; §7 records the providers' *list* prices as read; Check 85 produces the actual number, and until it runs, "remote is cheaper" and "remote is dearer" are both unmeasured.

## 10. Alternatives, and the recommendation

The owner has decided **that** there is a switch. What remains is **how**, and that is where the alternatives are:

**A1 — the switch as designed: two device-local settings, a persistent indicator, a policy node with a per-project `force_local`, and a Fugaro-hosted hop.** *Recommended.* It is the seven approved points with each enforcement point placed where it is authoritative: the user's choice on the device (D127's precedent), the project's prohibition in the policy node, the credential and the provider call at the hop, and the destructive path entirely local.

**A2 — the switch with the credential on the device** (an exception of the D54b/D111 kind, amending Global Constraint 4). *Refused by point 5 as the owner wrote it*, and priced here so the reason is on the record: a binary can be unpacked, so the key is not secret; it is a **third** item where D111's answer says two; and it is the one part of this design that is hard to take back. It would also be the first provider credential on a device Fugaro does not manage — `SECURITY.md`'s "Cloud keys stay in the cloud" read backwards.

**A3 — remote by default, local as fallback.** *Refused.* Every cost in §8 paid by default rather than by choice, and D44's own alternative column names the failure: "would ship a feature that starts sending a colleague's speech off the machine the moment it is installed, with nothing in any settings file to show for it; that is the accident D5 exists to prevent."

**A4 — a remote wake word.** *Refused, and not on the switch.* §3's variant C, and §5's never-send list could not be written for it.

**The recommendation's condition, which is the point of the whole exercise:** the switch should ship **so that Check 85 can run against both engines and the owner can spend a week with each**. It should **not** be the occasion for adopting remote. D44's second explicit decision stays owed, and it is answered — if it is answered — by what the owner saw in Check 85's table and felt using the switch.

## 11. What this revision changed, and what it kept

**Kept, because it still holds:** the direction split (§3), the never-send list (§5), the destructive-command argument (§6), the threat model (§8), the cost posture (§9), and the credential argument (§4.4). The first revision's claim that "the transcript is already off the device; the audio has never been" is where the whole design still starts.

**Changed by the ruling of 2026-10-19:**

- **The recommendation is no longer conditional on Check 85 in the same way.** The first revision recommended "local only forever" if the bake-off passed. The switch supersedes that: the owner wants to **use** both, and Check 85's numbers now sit beside an experience rather than deciding a document.
- **The consumer-delivered decision is gone.** The first revision had the laptop consumer resolve the policy node and deliver a MAC-signed decision into the phone's mailbox subtree, because the phone cannot read `config/policy` (tension (b1)). **Point 5 removes that need entirely**: the hop reads the policy and enforces it server-side, so **no read-grant change is proposed, the (b1) tension is untouched, and no new signed record is needed**. This is a real simplification and it came from the owner's constraint, not from a preference.
- **The per-project policy can now *force* local** (`force_local`), which the first revision did not have (§4.1, Q3).
- **The file is renamed** from `docs/mobile/design/cloud-stt-tts.md`, because it is no longer a design about the cloud half alone.
- **The open questions are consolidated from six to four.** The first revision's Q1 (D44's second decision) is no longer written as a question to answer but as a decision still owed, stated in §1, §10 and the Open section, because the 2026-10-19 ruling changed its shape while leaving it untaken; its "which holder of the key" question becomes Q2; its persistence question stands as Q3; its check-number question is carried as Q4. The questions the hop dissolved — how the phone learns the policy, and the fifth read family — are gone with §4.2's consumer delivery.

**Still owed, and not decided here:** Voice D44's second explicit decision (adoption), Check 85 (the measurement), and Q1–Q3 below.

**Corrected in review round 2 (2026-10-19), and recorded rather than quietly applied:**

- **The indicator's inputs were wrong and are now named.** §4.2 had made its state a function of "the effective project policy", which the app cannot read — the phone holds no grant on `config/policy/**` (§2), and §4.3's `config/policy/voice/**` grant is `TOKEN`-only. The state is now a function of four inputs the app actually holds, the policy half arriving as **the hop's project-status answer** (§4.4 point 7), which is also what makes "blocked by project policy" reachable on the device at all.
- **§4.1 and §4.4 contradicted each other about `force_local`.** §4.1 had said the hop's enforcement "cannot be bypassed by a stale cache or a modified client"; §4.4 had said the hop takes the project from the request and a modified client could misname it. §4.1 now carries §4.4's sentence, and both sections say the same thing: it is a guard against accident and stale state, not a boundary against the device's owner. The stronger claim did not survive, and the design does not need it — §4.4 point 7's cache is harmless precisely because the hop decides on the call.
- **The register's data source is now stated.** §4.3 had said "a name with no register row is refused by the app" while the register was this document's §7 and the policy node carries no terms. The register now lives **with the hop**, and the refusal is the hop's; the app refuses only what it can know locally — a name with no recorded consent.
- **`redactWriter` was mis-cited and is no longer cited here.** It is defined in `internal/gateway/route.go` (not `log.go`) and, in its own comment's words, "replaces every occurrence of key in what is written through it" — it is a credential scrubber, not a body-logging rule. The precedent for "no body logged" is `internal/gateway`'s `callLog` (`log.go`), and §4.4 point 6 and §8 now cite that instead. Same class as the earlier `Allows` → `AllowsData` citation.

## 12. What this adds to the plan (Phase 5, UNSCHEDULED)

Nothing is scheduled and nothing is approved. The task this document elaborates is **[implementation-plan.md](../implementation-plan.md) Task P5.3**, whose entry this change edits to describe the evaluation switch. **No task is added and none leaves UNSCHEDULED.**

If it were ever scheduled, the work is: **the pure switch-and-indicator function in `shared/`** (the one new pure logic, §4.2, which takes the hop's project-status answer as an input) with vectors and a mutation proof, in the shape of `shared/command` and `shared/confirm`; **the two device-local settings and the consent sheet** in the app; **the policy node** `config/policy/voice/**` with its golden, parity test and `RulesVersion` bump (P2.1's shape); **the hop**, which is the new component and the SECURITY-SENSITIVE heart of the task; **the provider clients**, which V7 already names for the console and which this design recommends be **reused from `shared/`** rather than written twice (D119); and **the register of §7**, kept current by the owner.

**Reviewer tier: SECURITY-SENSITIVE, mandatory independent review** — for the policy node (a rules change), the hop (a new internet-facing secret holder and the only place the policy is enforced), the device-node check that is the revocation bound, and because the failure mode of a mistake here is speech leaving a device the owner's earlier ruling said it would not.

## 13. The checks this would need

**Check 85 — the bake-off — has not been run**, and its text is not in the register: no `## Check 85` section and no summary row exists in [docs/gcp-live-checklist.md](../../gcp-live-checklist.md). Check 85 is written by P4.2 and owner-run; under this ruling it runs **the same prompts through the local engine and each admitted remote provider** (§4.7).

**The remote half would need its own check, and the next free live-check number, read from the register, is 110.** The reading, so a later reader can repeat it: **80–89 are fully allocated** (all ten rows are written in the mobile plan's table); **90–99** belong to the credentials service ("reserved for this work's owner-run checks"); **100–109** belong to the functional spec v0.1 (**105–109 "reserved and unused"**); **69a–69c and 70a–70c** belong to the website work, and 70c is the highest `## Check` section written; **nothing above 109 is claimed by any document or any open pull request**, so **110 is the next free number**.

**This document names 110 and writes nothing.** The convention is the register's and the mobile plan's: "**the phase that ships the check writes them**", and "a check is never run before its text is in the register". Check 110 would have to establish, on a real device against a real provider in a sandbox: that **no audio leaves the device while a confirmation is pending**; that a project with `force_local` produces no remote call; that a remote read-back or confirmation never occurs (§5 rule 3); that a provider outage produces a local answer and **no queue**; and that the indicator's state matches the route taken. **None of that is written here.**

**One caveat, offered rather than resolved.** The voice work already holds **check 66** for "the cloud-provider check that can exist only if the owner takes the second egress decision (D44, D46)" ([voice.md](../../design/voice.md) §5.3), and the app's evaluation switch is arguably the *first* run of exactly that check. Whether it is 66 or 110 is **Q4**; this document names 110 because it may not take 66 (a reserved number is never reused) and because the app's surface — a phone, a device node, a hop the console does not have — is not the console's.

---

## Open for the owner

**And one item that is not a design question at all: Voice D44's second explicit egress decision — *adopting* remote voice — is still owed and still untaken.** The owner's ruling of 2026-10-19 permits the app to carry the switch **so that the decision can be taken by use**; it is not that decision, and this document does not treat it as taken (§1, §10).

**Q1–Q3 are the three the owner left open.** **Q4** is carried from this document's first revision because it is still unresolved. Each states its options, the recommendation and why. **Nothing here is built, and none of these blocks the switch's design — they block its schedule.**

### Q1 — Which remote providers to try

**Options.** **(a) The three in §7** — Google Cloud Speech (STT and TTS), Deepgram (STT), ElevenLabs (TTS) — **plus OpenAI's audio APIs once their terms can be read** (the page returned 403 on 2026-10-19, so nothing is claimed about them). **(b) Google only**, on the argument that the installation already has the project, Secret Manager and the data-processing relationship, and one provider is enough to answer "is remote better than local". **(c) A set of the owner's own choosing**, with the register filled in from the owner's reading.

**Recommendation: (a) with (b) as the fallback.** Three providers is the smallest set that covers both directions without putting the whole evaluation on one vendor's terms, and it is what D46's spirit asks for ("the spec's §13 asks for '2–3 candidates'"). **Why the register matters more than the list:** the entries in §7 differ most in exactly the column the owner's ruling is about — Google's default is *no logging at all* and its opt-in logging trains on the data, Deepgram's terms could not be read, and ElevenLabs' retention is open-ended. A single-provider evaluation would decide "is remote better" while leaving "on whose terms" unanswered, and the second question is the one that governs adoption.

**What is UNVERIFIED in §7, listed so it is not mistaken for an omission:** OpenAI's terms (403); Deepgram's retention, training, Romanian support and latency; ElevenLabs' price, languages and latency; and **every latency number for every provider**, which is Check 85's to produce.

### Q2 — Does the hop reuse the budget gateway, or is it a new minimal proxy?

**Options.** **(a) A new minimal service** holding the voice provider key, callable by a device rather than a job, reusing `internal/gateway`'s forwarding shape and the credentials service's planned identity-verification shape. **(b) Extend the credentials service** to serve devices as well as jobs. **(c) Route the device's request through a run** (a job that makes the call) — *refused*, since it would give a phone a path into `fugaro run` authority.

**Recommendation: (a).** Three reasons, and each is checkable. The credentials service's own boundary says so: "**The service stays secrets-only. Other always-on features (e.g. the phone client backend) go in sibling services that call it**, keeping its attack surface minimal" — and its scope is jobs ("the single holder of every secret a **job** needs"), while a phone is not a job and its identity is a Firebase device uid (`m~`), not a run's `fs`/`fr`. Second, **(b) would put an internet-facing device path on the service whose compromise costs every model credential in the project** — the credential the whole 0.9.0 cascade design exists to protect — for a feature that needs one key. Third, the **cost** of (a) is bounded and already priced by the pattern: a Cloud Run service with min instances 0, one Secret Manager entry in the installation's existing shape (`deploy/terraform/gcp/modules/installation/secrets.tf`), and a service account with exactly three abilities — verify an ID token, read one device node and one policy node over IAM, and call one provider. **Who holds the provider key: the installation's Secret Manager and nothing else** (point 5), and **no run identity, no launcher and no agent ever holds it**.

**UNVERIFIED, and it belongs to the task that builds it:** whether reading `config/admin/devices/$uid` and `config/policy/voice/**` from a Cloud Run service over IAM needs a custom role or can be scoped to the two paths; the cold-start cost (§9); and whether the ID-token verification can reuse `internal/credsvc/auth.go`'s planned code as-is or needs a device variant.

### Q3 — How the switch is persisted, and can a project force local?

**Options.** **(a) Device-local, per account, never written to the database, plus a per-project `force_local` in the policy node enforced at the hop.** **(b) In the device node** `config/admin/devices/$uid`. **(c) A per-project policy only**, with no device setting.

**Recommendation: (a), and yes — a project should be able to force local.** **(b) is not available**: `config/admin` is `{ ".read": false, ".write": false }` in the rules, so the phone cannot read its own device node, and a setting the owner cannot read back is not a setting. **(c) removes the owner's own choice**, which is the whole point of the switch. **Why a project may force local:** the project is the unit D44 chose for the allow-list, the audio is about the project, and the project is the only party that can know its own rules — a repository under a contract that forbids third-party processing must be able to say so where the enforcement is, at the hop (§4.4 point 5), not only on a device it cannot see. **Why `force_local` and not a per-project default:** a default can be overridden by a device and a prohibition cannot, and the difference matters exactly once — when a project needs the prohibition.
**Cost, stated:** a device-local setting is not inherited by a second device and is lost on reinstall; that is the price of not adding a phone write and a grant (D127's precedent), and the alternative — the device node — is not readable by the app at all.

### Q4 — Which check number the remote half takes

**Options.** **(a) Check 110**, as this document names it, leaving the voice work's **66** to the console. **(b) Read the evaluation as the first run of D44's cloud-provider check and use 66**, covering the console's V7 and the app together.

**Recommendation: (a), with the reasoning stated so the owner can overrule it cheaply.** 110 is the next free number (§13), and the app's surface is not the console's: a different device, a hop the console does not have, and a device-node revocation the console does not have. **But the counter-argument is real**: D44 is *one* decision, and if the owner's 2026-10-19 ruling is the evaluation D44's check was held for, then 66 is arguably its number and 110 should be released rather than used. **This document may not take 66** (a reserved number is never reused), which is why it names 110 and asks.
