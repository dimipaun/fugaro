# The mobile app's sign-in, pairing and allowlist

**Status: plan only.** No code is implemented, no rules document is changed, no Firebase
configuration is touched, and **nothing here is approved** — the four documents below are the
owner's spec, what the repository actually holds, the decisions planning found, and the plan that
would follow from them. **Owner: Dimi Paun. Date: 2026-10-21.** Read against `main` at `d9dda8f3`
(`cli: fugaro --version and fugaro -v print the version, like fugaro version (#794)`); every
citation is by **path and symbol, never by line number**.

The spec is one page of prose and a flow: sign in with an account already on the phone, scan a
project QR, let the project's allowlist check the verified email. What the repository holds today is
the **other** half of that — an allowlist that is a **device node keyed by Firebase Auth uid**, an
identity layer that deliberately refuses **every** public sign-up path, and a pairing command that is
approved and unbuilt. [Reconciliation.md](reconciliation.md) works through the difference;
[implementation-plan.md](implementation-plan.md) turns it into parts.

## The documents

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | **The owner's spec, verbatim** — the text as it was written, alone, with nothing of ours in it. It is not edited, not annotated and not reconciled in place; the document that disagrees with it says so. |
| [reconciliation.md](reconciliation.md) | What the repository has, what the spec asks for, and where they differ: findings **F1–F29** classified *Wrong / Exists / Gap / Rule / Open*, the provider path written out in full, the decisions **D221–D236** (recommendation first, alternative and cost after), what could not be verified, a glossary — and an "Open for the owner" section of things that are **not** decisions. **Where it disagrees with spec.md, it wins.** |
| [implementation-plan.md](implementation-plan.md) | The parts **P0–P6**: each independently mergeable, each with its scope, files, **red step**, verification, dependencies, reviewer tier and commit. It carries what an **independent opus review** must cover for each security-sensitive part, and the owner-run live checks **proposed by name and not numbered**. |
| [README.md](README.md) | This index. |

## Typos fixed in spec.md

**None.** The spec is reproduced byte for byte from the owner's text, including its
`Oct 20, 2026 · @Dimi` line, its three tables and its four already-ticked open questions. Two things
that read like errors were checked and are **not** changed, because they are the owner's words
rather than slips: *"Sign in with Apple is also supported on iOS"* is a statement of intent for
this work to resolve, not a description of the app (which has no Apple sign-in at all,
`mobile/package.json` lists no such dependency), and the `fugaro allow add` line in the allowlist
section is marked *"for example"* and is a command this repository does not have (reconciliation
§F24 records that the name `allow` is taken twice already).

## The numbers this work takes

| Numbers | What | Why these |
| --- | --- | --- |
| **D221–D236** | this work's decisions | Taken from **above every written number**: the highest written decision under `docs/` at the base commit is **D220** (the voice-session work's, `docs/mobile/voice-session/`), the coordinator holds D193–D197, and D198–D200 are free but **D200 is recorded as possibly held by the owner's notes**. Each decision is written in [reconciliation.md](reconciliation.md) §4, **recommendation first**. |
| **none** | check numbers | The register's rule is that **the phase that ships a check writes it**. The parts here ship none, and the numbers above 111 are proposals and unmerged claims (§9 of the plan): this work proposes its owner-run checks **by name** and claims no number. |

## What this work collides with, in one paragraph

The spec's step 4 has the app exchange a Google, Microsoft or Apple ID token for a Firebase Auth
session in each paired project, with a passwordless email link as a third provider. That is a
**reversal** of a standing rule of this repository: `internal/cli/init_firebase.go`'s
`refusePublicSignUp` refuses **any** public sign-up path, and the owner's own answer to the central
orchestrator's D6 (recorded in `docs/mobile/functional-spec-reconciliation.md` §3.7) says the same —
a device node plus an administered email field, an unverified email refused **at pairing**, and
**no change to custom-tokens-only**. The **domain rule** is **not** part of that answer: §3.7
records that the answer does not restate the domain half and leaves it to D6, whose recommendation
it is (D6's sub-question (b)) — this work takes it as its own **D223**, and §3.7's wording, not this
sentence, is the record of what the owner ruled. The reconciliation
settles that as **D221** (no provider session reaches the project's database) and writes the
alternative out in full, in §3 and in P6, so that a decision against the recommendation is taken
with the configuration delta, the attack surface and the per-provider verified-email question in
front of the owner rather than in a reviewer's head.

## How these documents are held

**This folder has no guard yet, and that is deliberate rather than overlooked.** Its nearest
neighbour, `docs/central-orchestrator/`, has no test of its own either; the folders with pins —
`docs/mobile/` (four pins named for it: `internal/cli/docs_mobile_plan_test.go`,
`docs_mobile_checks_review_test.go`, `docs_mobile_push_test.go` and `docs_mobile_mailbox_plan_test.go`,
beside four sibling pins that quote it), the coordinator, the voice-session work — are held by
`internal/cli`'s `docs_*_test.go` family, which is where this folder's own pin belongs. **P0 of the
plan is the part that writes it**: `internal/cli/docs_mobile_auth_test.go`, holding the spec
verbatim against the owner's text, the four documents, the decisions **D221–D236** contiguous and
recommendation-first, the parts **P0–P6** in order, the cited paths resolving on disk under a rule
that says what counts as one (a backticked token whose first segment is a directory of this
repository — so **Realtime Database node paths** like `config/admin/devices` and the **IAM role**
`roles/firebasedatabase.admin` are not mistaken for files), and a scan that no line of these
documents carries a credential-shaped literal — because a folder about Firebase configuration is
exactly the folder where somebody eventually pastes a real key. **The
guard will state what it does not hold**, as the neighbouring ones do: a symbol named beside a
citation without the possessive form, and any claim about the folders this one reconciles, which
their own pins hold. It also carries the **excuse list** — the three paths this work will create and
the four the mobile Phase 6 tasks own, each excused **only while it does not exist**, with the guard
failing on an entry that has become stale, so the list cleans itself as the work lands. Until P0 lands, the honest statement is the one above: **these four documents
are unpinned**, and a reader who wants a pin before the plan is approved should say so.

## Related

- [../mobile/README.md](../mobile/README.md) — the app, its phases, and the pairing task this work
  extends (`docs/mobile/implementation-plan.md`'s **P6.6** and **P6.8**, approved 2026-10-18 and
  **unbuilt**).
- [../mobile/functional-spec-reconciliation.md](../mobile/functional-spec-reconciliation.md) §3.7 and
  §4.2 — **the owner's D6 answer of 2026-10-20**, which is the ruling this work is written to.
- [../central-orchestrator/reconciliation.md](../central-orchestrator/reconciliation.md) — the D6
  question itself, and the identity-layer question beside it.
- [../credentials-service/reconciliation.md](../credentials-service/reconciliation.md) — where
  custom tokens would be minted if the spec's last deferral is ever taken.
- [../gcp-live-checklist.md](../gcp-live-checklist.md) — the register, and the rule about numbers
  this work follows.
- [../releases/](../releases/) — the release train: 0.9.5 shipped, 0.9.6 and 0.10.0 are ahead of it,
  and 0.11.0 is the central orchestrator's and the rigor modes work's (reconciliation §F26, D236).

## What is not verified here

Everything in [reconciliation.md](reconciliation.md) §5, and one thing about the container itself:
**no Node is installed here and `mobile/node_modules` does not exist**, so **not one of the app's
tests was run** while writing this — the app's boundary, its dependency list and its demo promises
were read, not executed, and the plan says so in the parts that would run them (P4). The Expo SDK 58
provider guidance the spec leans on (Credential Manager, Google Sign-In, browser OAuth for Entra ID)
could not be read at all, there is no network here, and the App Store rule the spec cites is cited
rather than checked. None of that changes the plan's shape, and all of it is listed by number in §5
so that a reader can see exactly where a claim rests on a document rather than on a run.
