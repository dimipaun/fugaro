# The conversational front agent (mobile P5.4)

**Status: design only; nothing is implemented.** There was no copy of this document in the tree before this change — the plan says so in its own status paragraphs and hands the writing to **P5.4.1** — and **no schema field, no rules node, no service and no app code is added by it**. The **owner approved P5.4 on 2026-11-26** in the order recorded as *"jobmax P5.4"*, and the plan's **Task P5.4** heading carries the approval in place of `UNSCHEDULED`; [implementation-plan.md](../implementation-plan.md) splits it into **nine tasks, P5.4.1–P5.4.9**, each with its Files, Depends on, Verification, Red step, Reviewer tier and Commit. **This document is P5.4.1 — the design of record** — and every task after it reads it rather than a draft. **The approval is permission to start and nothing else**: it supersedes **D172**'s *timing* and only its timing (D172's answer of 2026-10-17 was *"deferred with D115; not v1"*), and **the placement, the credential, the brief's carriage and the conversation's memory are the owner's still**.

**What this document does and does not decide.** It is written so that the owner **ratifies or amends a written design rather than answering into a blank**: the plan's **four open questions (1–4)** are answered **in** this document, each as a recommendation **with the alternative, priced** — they are **the owner's to ratify or amend**, not decisions taken here. It **allocates no decision number, writes no check and changes no approval**. Where a question is answered the answer names the task it moves (P5.4.2–P5.4.7), so that a later reader can see which files an amendment would touch. **D115 is still unanswered** and is the phase-2 hosting decision the placement (Q1) rides on.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-11-26 (P5.4 approved and split). **Stands on:** [design/always-on-orchestrator.md](always-on-orchestrator.md) §4.4 (the three model-credential candidates) and §6 (the never-list); the functional spec's [§3.1](../functional-spec-v0.1.md) future option; [design/push-notifications.md](push-notifications.md) and [design/voice-evaluation-switch.md](voice-evaluation-switch.md) for the shape a mobile design of record takes.

---

## 0. What this document reads, and the two findings it records

**It designs the functional spec's own future option, and nothing wider.** [functional-spec-v0.1.md](../functional-spec-v0.1.md) §3.1 ("Direct commands to agents") ends with the sentence this document is written from: *"Future option (not v1): a dedicated conversational agent in front of the orchestrator that shapes a spec with the user in a longer back-and-forth and hands a clean task to the orchestrator only on an explicit 'go'."* Everything below is that sentence made specific enough to build.

**Two findings the split records, and this document does not smooth over.**

1. **D172 was answered on 2026-10-17, and the approval supersedes its timing and nothing else.** D172's answer was *"deferred with D115; not v1"*, and it named **D115** (the phase-2 hosting decision) as the thing it waited on. What the approval of 2026-11-26 changes is **when**, not **where**: the placement (Q1), the credential (Q2), the brief's carriage (Q3) and the memory (Q4) stay open, which is why they are the questions this document asks.

2. **The plan's rules-generation chain is stale against the tree.** The plan reads generations 2–6 (P6.4's 3, the coordinator's 4, P5.3.1's 5, P5.2.2's 6) while `internal/projectpolicy/version.go`'s **`RulesVersion` is 11 on 2026-11-26**, because other works took 7–11 and this plan names none of them. So **P5.4.3 takes the first free generation at merge and states the number it took**; nothing is renumbered here, and this document claims no generation.

## 1. The problem

The orchestrator's job is to run work and report on it, and its output is **noisy** — the functional spec says so itself (§3.1: *"the orchestrator is busy and its output is noisy"*). A person who wants a *good* run out of it has to think the task through first: what the change is, what it must not touch, what "done" means. That thinking is exactly what a chat window is good at and what a one-line command is bad at. The front agent is the missing step: **a conversational agent in front of the orchestrator that shapes a spec with the person and, only on an explicit "go", hands a clean task to the orchestrator.** It is not a second orchestrator and not a second way to run work; it is the part that *writes the brief* and then gets out of the way.

**Why now, and why not a plain chat window.** The plan's own reason (P5.4) is that a **brief** — the shaped task — is the whole point: a conversation that produces nothing a run can use has produced nothing. The agent's value is the **handoff**, not the chat.

## 2. What exists today (verified against the tree)

- **The mailbox.** `shared/mailbox-schema/` carries the message kinds (`message`, `prompt`, `answer`, `action`), the per-direction MAC and the canonical serialisation the MAC covers, and a heartbeat with an **`agents` list** (id and spoken name) — **D161** says an agent is a conversational endpoint the installation's consumer advertises, and **D163** says the default is the last used in that project, else the consumer's first advertised agent. Turns and prompts travel over this and nothing else; the app has one write path (D113).
- **The consumer.** **P4.1** (`internal/cli/mailbox.go`, the registered `fugaro mailbox` command) is where an inbox message lands on the laptop, and **P6.7** is where it verifies the MAC before it acts. The front agent is a **new endpoint behind, or beside, that consumer**.
- **The launch path.** `fugaro run` writes the task to the runs bucket and starts the job; the launch is a **claim** that the caps and the budget token bound (F17). **D162 as amended 2026-11-18 (D2786)** decides the confirmation: a plain start needs none, while a start in a costly rigor mode the project policy names needs the read-back and, per a project switch, biometric re-authentication.
- **The session host.** `internal/agent`'s `--session-id`/`--resume` (`internal/agent/session.go`) already runs a **multi-turn `claude -p` session**. The conversation is that mechanism, not a second one.
- **The rules.** `internal/budget/rules/rules.json.tmpl` and its golden are where a new field or kind is admitted, and `RulesVersion` is **11** (see §0).

**What does not exist.** A front agent: no session of its own, no turn, no brief field, no host, no handoff, no screen. It is the one node the tree does not already have.

## 3. The proposal

### 3.1 The front agent's job

**One job: turn a conversation into a brief, and a "go" into a run.** The front agent (a) **reads** an utterance meant for an agent (P6.1's grammar decides what is an app command and what is agent text — the two must not both claim one), (b) **holds a multi-turn conversation** with the person in which the task is shaped, (c) **writes the brief** — the shaped task, in the person's words and the agent's, ready to become a run — and (d) **waits for the "go"** and, on it, **hands the brief to the launch path**. It does nothing else: it runs no work, reads no run's logs as its own, and reaches no node the consumer's enforcement does not name.

### 3.2 Its conversation with the user

A turn arrives as an inbox message of the existing `prompt`/`answer` kinds, **carrying the `agent` field** (D161) so that a turn says which agent it is for. The front agent replies over the same mailbox the app already reads, so the phone needs **no second write path** (D113) and **no second schema for the conversation**. The screen that renders a turn is P5.4.7; the turn's shape is P5.4.2; the pure function that decides what an utterance *is* — a command, a turn, or a "go" — is P5.4.4.

**A turn is not an app command.** P6.1's wake-word grammar owns "Fugaro …"; an utterance that is not one is agent text and is routed to the front agent. **A "go" is not a plain turn** (§3.4).

### 3.3 The brief it shapes

**The brief is the shaped task: the text a run's stored task record is made of.** Its first line is the `# Task: <title>` line the hard rule requires, so `fugaro watch` and the pull-request title come from it; the rest is the task itself in the person's own words and the agent's, ready to be the run's brief. It is **bounded** (an over-long brief is refused where it is written and again where it is handed off — P5.4.2, P5.4.6) and it is **the person's words**, so **no brief text reaches a log line** (P5.4.6).

### 3.4 The "go" it waits for

**The "go" is the confirmation the conversation collects, and it starts exactly one run.** It is **confirmation-shaped** and **reuses P6.2's spoken-confirmation rules rather than writing a second set** (P5.4.4): **D128's amendment of 2026-10-17** gives a spoken confirmation its shape — its **own utterance**, recognised as the fixed phrase, and **one "go" authorises exactly one run** — and P6.2's other rules come with it: a replayed "go" authorises nothing the second time; a "go" whose turn still holds an unresolved target is refused, because only a resolved target is read back; and a "go" after the conversation's timeout is refused. **It is carried as the typed `launch` action** (question 5's recommendation), so a "go" is the one way to start a run rather than a second one — the place D162 (a launch needs no confirmation) and D128's amendment meet.

### 3.5 The handoff it makes

On the "go", the front agent hands the **brief** and the **`launch` action** to the launch path, which is the **consumer** (`internal/cli/mailbox.go`) if the front agent runs on the laptop, or the **resident orchestrator's own launcher** (P5.1d's claim and mint) if the placement is a sibling service. The brief becomes the **run's stored task record**, not a copy of the transcript; a "go" that arrives twice starts one run, not two; and a "go" whose brief is empty or over its bound starts nothing. **The launch keeps every gate it already has**: the caps, the budget token, the runner's admission check, and — for a costly rigor mode — P5.1g's read-back and biometric re-authentication.

## 4. Question 1 — the placement

**Q1 — where does the front agent run?** A sibling service per installation, or D172's own alternative, a second agent behind the phase-1 consumer on the laptop?

**Recommended: a sibling service per installation**. D172 names exactly this if the front agent is always-on — a **sibling of the credentials service** (the always-on design's G4 shape, one Cloud Run service per installation with `min-instances = 0`) — because **a laptop-only agent dies with the laptop**, and a conversation that has to survive the phone being put down cannot live on a machine that sleeps. The service's files are **`internal/frontagent/`** (the service, its session host and its turn loop), **`deploy/terraform/gcp/modules/installation/front_agent.tf`** (its account, its one grant and the Cloud Run service) and **`internal/infra/tfvars.go`** — the files [implementation-plan.md](../implementation-plan.md) gives P5.4.5 under this answer.

**The alternative, priced: a second agent behind the phase-1 consumer on the laptop.** This is D172's own alternative and it is real: the consumer (`internal/cli/mailbox.go`) already exists, so the front agent would add **no service, no new account, no Terraform and no new home for the credential** — it needs only the session host the laptop already runs. **Its cost is the laptop**: the conversation stops when the laptop sleeps or the consumer is not running, the phone's conversation has no host while the person is away from the machine, and the always-on conversation the spec's "put the phone down" survives is the thing that is lost. Under this answer **P5.4.5 writes `internal/cli/mailbox.go`** (level 7, after P5.2.4) and **P5.4.6 sits at level 8**, the levels [implementation-plan.md](../implementation-plan.md) states.

**How a turn reaches it, and who signs the reply.** The sibling service **holds no mailbox key** (§8 rule 2) and **never reads or writes the mailbox itself**. A turn reaches it the way any agent's does: the phone writes it to the inbox, and **the installation's consumer** — the one holder of the pairing key, and the only process that verifies a tag (`internal/mailbox/mac.go`) or signs one (`internal/mailbox/outbox.go`'s `WriteReply`) — verifies it and hands it on through the seam the consumer already has (`mailboxHandOff` and `mailboxPromptSource` in `internal/cli/mailbox.go`, the one channel whose far end "is not in this tree"). The reply travels the same way back, and **the consumer is what signs and writes it**: a reply the phone cannot verify is one it must drop, and the front agent cannot produce one. **The front agent's mailbox identity is therefore the consumer's, not its own** — D161's "an agent is an endpoint the installation's consumer advertises" is load-bearing rather than descriptive, and `mailboxAgents` is where the front agent's spoken name appears.

**What that costs, and which half of Q1 it leaves open.** A consumer-routed agent is reachable only while a consumer runs, and today the consumer is run **beside the orchestrator as one local process** — fine under the laptop answer below, and **not a machine that is always on** under this one. So this answer buys an always-on **session**, and its always-on **reachability** needs a consumer pass running where the service does; **nothing in the tree runs one**, and that half of **D115 is unanswered** (§12). **The hop's spelling** — how the consumer's far end authenticates (the installation's own service identity, never the pairing key), retries and bounds — is **P5.4.5's to choose and to state in its pull request**; this document fixes only that the front agent holds no mailbox key and that the consumer stays the one signer.

**This is the placement the whole split turns on**, and **D115 is still unanswered** — it is the phase-2 hosting decision this rides on. **It decides P5.4.5's files and therefore P5.4.6's**, and it is why the plan asks it before either is started rather than during.

## 5. Question 2 — the credential

**Q2 — where does the front agent's model credential live?**

**Recommended: the credentials service (candidate (A) of the always-on design's §4.4), with candidate (B) — a Secret Manager secret mounted into this one service — as the interim where (A) is not yet reachable.** The always-on design's §4.4 prices the three candidates; (A) is the target because the credential never enters the process that holds the conversation, and (B) is the acceptable interim because it already ships. **This is the third place a model credential would live** — the runner's and the orchestrator's are the other two — and it is **the first task whose mistake spends the owner's money without the owner watching** (D116's spend, and the gateway's caps do not cover a session that is **not** a run). **Where the answer is (A), no secret is mounted** and the credential comes from the credentials service; **where it is (B), a Secret Manager secret mounted into this one service and no other** carries it. **P5.4.5 is the mandatory independent review** for exactly this reason.

**Why the question had to be asked rather than answered by the never-list.** The always-on design's **§6 rule 4** is *"**No model credential of its own, ever**"* — and **the rule is about the resident process**: it bounds the *resident orchestrator*, which launches runs and lets *them* hold the credential in the container that dies with them. **A front agent is a second process** — it holds a model session of its own and calls a model directly, not through a run — so rule 4 does not by itself say where **its** credential lives. **This document is the first place that has to say what it means for a second one**, and the answer is the one above: the same never-list discipline, applied to a process that is not the resident one.

**The spend is disclosed separately.** D116 puts a front agent's conversation spend on **the operator's own model account, outside Fugaro's caps**, and a session that is not a run is exactly that spend — so the phone's screen carries the disclosure line and P5.4.8 writes it, and the front agent's own cap (question 9) is enforced by the host, not the gateway.

## 6. Question 3 — the brief's carriage

**Q3 — does the brief travel with the "go"?**

**Recommended: yes.** The brief — the shaped task, in the user's own words and the agent's, ready to become a run — **is the whole point of a front agent**, and without it the conversation produces nothing a run can use. **It sits in its own bounded field beside the `launch` action and never inside the action's `target`.** The `launch` action's `target` **is "a repo alone"**, and widening it to carry the brief would change what a launch *means* — the plan's `ACTION_FIELDS` is read by the consumer's enforcement, so a brief on the target is a launch whose meaning has changed (P5.4.2). The brief's spelling is **P5.4.2's to choose and to state in its pull request**, because this document fixes the requirement and not the name.

**The alternative, priced: no.** If the brief does **not** travel with the "go", **P5.4.2 and P5.4.3 are deleted** — the turn and its rules generation exist to carry the brief — and what is left is a chat window and **no handoff**: the "go" would start a run from a brief assembled elsewhere or not at all, which is the outcome the front agent exists to prevent.

## 7. Question 4 — the memory and the retention

**Q4 — where does the conversation's memory live, and what is kept?**

**Recommended: the phone holds the draft and the host holds the session**. The **turns** live in the mailbox and **expire with it (72 hours** — D118/O5, the mailbox and prompt TTL); the **shaped brief** lives on the **phone** until the "go", so a conversation in progress survives a host that is briefly away; and the **host's `--session-id` session** is the agent's own memory, **scoped to that installation and that conversation**. This is the question that decides what a person loses when they put the phone down for three days: the turns, yes; the draft and the run, no.

**The retention, at the end (question 7).** The **brief becomes the run's task record and is kept with it**; the **turns expire with the mailbox's 72-hour TTL**; and the **host's session is deleted when the conversation ends**. P5.4.5 owns the session's teardown and P5.4.8 owns the retention sentences.

**The alternative, priced: the host holds everything.** A host that kept the whole conversation would have to hold a person's words past the mailbox's TTL, in a second store with its own retention and its own answer to "what does the operator's own model account see" — a new retention decision, not a consequence of this one. Keeping the turns in the mailbox is what lets the conversation inherit the TTL the mailbox already has.

## 8. What it must never do, read for a second process

The never-list is [design/always-on-orchestrator.md](always-on-orchestrator.md) **§6**, and the front agent inherits every rule that is about *reaching* rather than about being resident. The three that cost the most to get wrong here:

1. **Never a credential it is not entitled to (§6 rule 4).** *"No model credential of its own, ever"* — and **the rule is about the resident process**; applied to **a second process**, it means the front agent holds **only** the one credential Q2 names and never a second — where (A), none mounted; where (B), the one secret and no other.
2. **Never read a secret value it does not hold (§6 rule 5).** No `secretAccessor` on model, git or App secrets; the pairing key is the tag's exception, not this process's — which is why §4 routes a turn and its reply **through the consumer** rather than giving this process a key of its own.
3. **Never act on an unverified message, and never answer its own prompt (§6 rules 7–8).** A turn is acted on only after the MAC verifies (P6.7's rule), and the front agent never synthesizes a "go" for itself.

**What rule 4 does *not* buy, restated so this document does not oversell it.** It bounds the **lifetime and blast radius of a credential on a compromise**; it does not bound what a compromised front agent can *reach* — that is the caps, the kill switch, the runner's admission check and the mode gate, exactly as the always-on design's §4.4 says of the resident process.

## 9. The threat model, stated once

- **A stolen, unlocked phone.** It can talk to the front agent and **say "go"** — which is a launch, and a launch the phone can already make. Bound by the same things a launch is bound by (the caps, the admission check, and the costly-mode read-back and biometric), and by the time to revoke.
- **A stolen phone account.** It cannot sign (the MAC key is not in an Auth account), so it cannot forge a turn or a "go"; it can read the four families until its user is deleted.
- **A poisoned mailbox message.** A message that is unsigned, mis-signed, reflected, replayed or re-targeted fails the MAC and the replay windows before the front agent reads it (P6.7). A message with the key is not this attacker but the next.
- **A compromised front agent.** It holds the credential Q2 names and the session; it can be made to say anything to the phone and to shape any brief. What bounds it is **not** the MAC — it is the caps, the kill switch, the mode gate, and the fact that the brief becomes a run whose admission the runner re-checks. **The session's host is why Q2's answer matters**, and why P5.4.5's review is mandatory.

## 10. Failure modes

| Failure | What happens |
|---|---|
| The host is not running | A front agent on the laptop is offline when the laptop sleeps (Q1's cost); a sibling service wakes on its tick. The phone shows the agent as not reachable; nothing is lost inside the mailbox TTL. |
| The model is unreachable | The turn fails **with a message that names the fix**, and the conversation **hangs on nothing** (the no-prompt policy). |
| The "go" arrives twice | One run starts; the second authorises nothing (P5.4.6's guard, P6.2's one-confirmation-one-action). |
| A costly rigor mode | The handoff reads the mode **before** it applies the action, and takes P5.1g's read-back and biometric re-authentication (D2786). |
| The brief is too long | Refused where it is written and again where it is handed off; the run starts nothing. |
| A turn's text looks like a secret | It is rendered as text and never into an error path — the app has no logger, and P5.4.7 is where that matters most. |

## 11. The cost

The conversation is **the operator's own model spend, outside Fugaro's caps** (D116), and a session that is not a run is **not** bounded by the gateway — so the front agent's **own cap lives in the installation's config and is enforced by the host** (question 9's recommendation). The host itself is one service per installation with `min-instances = 0`, the same shape and the same near-zero idle cost as the orchestrator and the credentials service.

## 12. UNVERIFIED

- **Every claim about a file that does not exist yet.** `internal/frontagent/`, `front_agent.tf`, the brief field and the turn are this design's proposal, not the tree's state; none is verified against a build.
- **The session's real behaviour under resume.** `internal/agent/session.go` runs a multi-turn session; the front agent's use of it across a phone-side pause is unmeasured.
- **The consumer-routed hop, for a service that is not on the laptop.** §4 fixes who verifies a turn (the consumer, P6.7) and who signs the reply (the consumer, `WriteReply`) and leaves the wire to P5.4.5 — but **nothing in the tree forwards a verified turn to a second process, and nothing runs a consumer pass always-on**. Until one does, a sibling front agent is reachable only while a consumer runs somewhere, which is the half of Q1 that D115 still holds.
- **The credential service's reach for a non-run caller.** Where Q2 answers (A), the front agent is the **first non-run caller** of the credentials service; that it can serve a session that is not a job is unverified here.
- **The cost of a long conversation.** No number exists; question 9's cap is the bound, and its value is the owner's.

## 13. The plan's four questions, answered

| # | Question | Recommended | Alternative, priced | Moves |
|---|---|---|---|---|
| **Q1 — ** | Where does the front agent run? | A sibling service per installation | A second agent behind the phase-1 consumer on the laptop — dies with the laptop | P5.4.5's files, and P5.4.6's level |
| **Q2 — ** | Where does its model credential live? | The credentials service (A), the mounted secret (B) as interim | — (the never-list's rule 4 is read here) | P5.4.5's shape and its grant |
| **Q3 — ** | Does the brief travel with the "go"? | Yes, in its own bounded field | No — P5.4.2 and P5.4.3 are deleted | P5.4.2, P5.4.3 |
| **Q4 — ** | Where is the memory, and what is kept? | The phone holds the draft, the host holds the session | The host holds everything — a new retention store | P5.4.5's session, P5.4.7's screen |

**None of the four is a decision number, and none is taken here: each is the owner's to ratify or amend.** The plan lists six further questions beside the tasks (the "go"'s carriage, the check's number, the retention, the spoken name, the spend cap, and the plan's missing graph file); this document's §3.4, §7, §5 and §11 carry the recommendations for the ones a design can state, and the rest stay with the plan's own list.

## 14. The hygiene list: what goes stale on merge, and what collides

- **A generation number, once P5.4.3 merges.** `RulesVersion` is **11** today; this document claims none, and the task states the number it takes.
- **The placement, once Q1 is answered.** §4's file list is P5.4.5's under one answer and the laptop's `internal/cli/mailbox.go` under the other; a change of answer changes a level, not just a file.
- **The front agent's mailbox identity, once P5.4.5 merges.** §4 fixes that it has **none of its own**: the consumer verifies the turn and signs the reply, and the front agent attaches at the consumer's seam. A task that gives the front agent a pairing key — or a second writer to `mailbox/<uid>/outbox/` — is amending this document, not implementing it.
- **The brief's spelling, once P5.4.2 merges.** §3.3 and §6 fix the requirement and not the name; the name lives in the schema and its pull request.
- **A check number, once P5.4.9 merges.** This document writes no check and names none; the register's own rule decides the number.
- **The reader table of §5, if the credential is ever granted to anyone else.** The third copy of a credential is a widening wherever it lands, and the note that records it is owed where the grant is made.

**Related:** [implementation-plan.md](../implementation-plan.md) (Tasks P5.4.1–P5.4.9) · [design/always-on-orchestrator.md](always-on-orchestrator.md) (§4.4, §6) · [design/push-notifications.md](push-notifications.md) · [design/voice-evaluation-switch.md](voice-evaluation-switch.md) · [functional-spec-v0.1.md](../functional-spec-v0.1.md) §3.1 · [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) · [README.md](../README.md).
