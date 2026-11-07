# The api-key review path — the independent review served by an API key instead of a subscription account

**Status: plan only; nothing is implemented; the decisions D1410–D1429 are answered 2026-11-07 and
all tasks are approved 2026-11-07 (owner: "All tasks approved. Jobmax").** Every answer is the
recommendation, and the ones that decide the shape are worth a line: **D1410 both slices** — the
in-job half and the cloud half; **D1411 and D1412 Path A, and no second Anthropic key**; **D1413 the
key wins when the owner asks for it explicitly**; **D1414 no** — an `oauth` run's review does not
move onto an API key; and **D1415 a new additive field** on `StageTiming`, never a new value on
`credential`. This is a design for the owner to read. It adds no code, no schema, no config key and
no Terraform; it edits no file outside `docs/`. **Nothing in it waits on a further decision from the
owner**: the answers of 2026-11-07 and the approval of the same day are the last word the plan
needed, and items 1–5 of [implementation-plan.md](implementation-plan.md) §6 are satisfied. **The
two gates that remain are not decisions and are named there**: the mandatory security review owed
before **AK4–AK7** land, and **E7** — not met — so the cloud half stays unlaunchable and those four
tasks may not start.

**Asked by:** the repository owner, in a message of **2026-11-07** — "the api-key review path: let's
start a design". The role is the one the ask's own clauses describe: the person who owns the
installation and the account the review runs on. No `@handle` and no name is recorded anywhere in
this folder, by this repository's rule. The ask is reproduced verbatim in [spec.md](spec.md).

**What it is.** The tree has **two** independent reviews, and both run on a Claude **subscription**
account today. The **in-job** one is the mixed run's stage-scoped account
(`docs/design/mixed-credentials.md` §11, `internal/runner/agentenv.go` `resolveMixedStages`, whose
live check is **Check 290** — written, and **not run**: the register records it as "USER-RUN, NOT
RUN" and `docs/multi-model.md` and `docs/releases/v0.20.0.md` agree, so the configuration is
designed and built, never measured): an `api-key` run whose coder is on a provider route, whose
`review`/`adversarial` stages are handed a Claude OAuth account and go **straight to Anthropic**,
bypassing the gateway — so their dollars are **notional** (`runstore.StageTiming.ModelNotionalUSD`,
`Credential: "oauth"`) and nothing caps them but `agent.max_budget_usd` and the stage timeout. The
**cloud** one is the review-only job (`fugaro review --pr N`, or `fugaro run --pr N --recipe
review-only`; `internal/credsvc/review.go`): its model calls are served by the credentials service's
**review class** from the installation's Claude OAuth account, and the class **refuses** a repository
whose own `agent.auth` is an API key (`internal/runner/routing.go` `checkReviewClassModelPath`). It
is **built and not admitted**: `internal/cli/review.go`'s `reviewJobAdmitted` is `false`, so
`checkReviewJobAdmitted` refuses every launch until the service admits the review caller —
`docs/cloud-senior-review` §1's **E7**, then **CSR11**'s flip after the owner's live checks
(`docs/releases/v0.20.2.md`'s known limits: "the cloud review is built and not admitted"). The owner
wants the same review served
by an **Anthropic API key**, with the dollars real and capped, no subscription needed, and the key
able to be a secret of its own.

**What this folder found, in one paragraph.** The ask is **one name for two paths, and the two are
not equally far away** ([reconciliation.md](reconciliation.md) **F1**, **F9**–**F11**). The in-job
half **already works and needs no new machinery at all**: `agent.auth: api-key` with a Claude
`models.reviewer` and the gateway on already sends the review stage's calls through the gateway to
Anthropic's own API on the run's `anthropic-api-key` (`internal/runner/gateway.go` `gatewayUpstream`
— the `"api-key"` arm builds `gateway.Upstream{Kind: "anthropic"}`), priced from the table and
charged as `ModelUSD` — this is `docs/design/mixed-credentials.md` **§3 Path A**, whose own words are
"Works today with the code as it is". What is missing there is a **name and a pin**, not a
capability: no config key, no doc section and no test calls it the api-key review, and nothing
asserts the review stage's real-dollar accounting in such a run (**F5**). The cloud half is the
slice `docs/cloud-senior-review/implementation-plan.md` §6.7 already cut from 0.20.0 as **CSR17** and
**CSR18**, and the finding that matters is that **§6.7 prices the whole slice at the OpenRouter
cost** (**F12**, **F13**): the service already holds the Anthropic key at **tier B**
(`internal/credsvc/bundle.go` `CredentialKey`, `config.AnthropicSecret`) and
`internal/credsvc/forward.go` `send` already builds `gateway.Upstream{Kind: upstreamKindAnthropic}`
for a tier-B entry and sends it to Anthropic with `gateway.UpstreamHeaders` — so the **Anthropic-key**
half needs no base URL, no auth shape and no new upstream, only a pairing that may name the project's
own key instead of a Claude account. The **OpenRouter** half is the provider-key apparatus §6.7
describes, and it is a different slice. **Both cloud slices are cheaper than §6.7 prices the whole
of it, and neither of them is what stands between the owner and a review: the launch is refused
outright by `reviewJobAdmitted` until E7** (**F10**, **U6**) — so this design prices the cloud code
and says plainly that no installation can reach it yet.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's ask of **2026-11-07**, verbatim, with a header naming the owner by role and the status line. Where this file and the others disagree about what the ask *says*, this file wins |
| [reconciliation.md](reconciliation.md) | Every clause of the ask against the code on `main`, read by path and symbol: the findings **F1–F24** (Wrong / Exists / Gap / Rule / Open), what the **in-job** path does today end to end (§1), what the **cloud** path does today end to end (§2), the two readings of "the api-key review" (§4), **the security analysis as an attacker would see it** (§5), an **UNVERIFIED** section (§6), the decisions **D1410–D1429** each with a recommendation, an alternative and **the owner's answer of 2026-11-07** (§7), the number block and how it was read (§8), and the owner's **Open items**, each with the answer it now carries (§9). **Where it disagrees with [spec.md](spec.md) about the code, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **AK1–AK8**, each with its files (at most six), its interfaces, its named tests and command, its **red step**, its **mutation**, its **reviewer tier**, its dependencies and its commit; the levels, the critical path and the shared-files table proving no level has two writers of one file (§1–§2), what is deliberately not in the plan (§3), the release slot — **after 0.20.0**, from D1421 (§4) — the owner-run live check **Check 330** (§5) and the gates a task may not start without — the five the answers of 2026-11-07 satisfy, and the two that stand (§6) |
| `README.md` | This file: the status, the shape in one paragraph, the numbers, and what a reader should not take from here |

## The two things to read first

**`docs/cloud-senior-review/implementation-plan.md` §6.7 already names this feature.** It holds
**CSR17** (the service can hold and serve a provider key for a review caller, SEC) and **CSR18** (the
review run's model path reaches a provider's endpoint, SEC), ordered `CSR17 → CSR18`, and the owner
**cut both from 0.20.0 on 2026-11-06** — so 0.20.0 ships the OAuth review alone and refuses a
repository whose own `agent.auth` is an API key, in a named sentence. §6.7 is a sketch, not a
design: it carries no reconciliation, no decisions of its own and no levels. **This folder does not
silently become a second plan for one feature.** What it adds is the reading that §6.7's cost
sentence — "the service has nowhere to hold the key, no pairing that can name one, no candidate list
that may offer one and no upstream to send it to" — is **true of an OpenRouter key and not of an
Anthropic one** (**F12**), which splits the slice in two.

**`docs/design/mixed-credentials.md` §3 already designs the in-job half.** It is merged on `main`, it
is headed "Path A: Claude through an Anthropic API key", and §1's own words for it are **"Works
today with the code as it is"**. Its "Missing" list — the user docs, the cost guidance, a
gateway test with both upstreams in one run, and one sandbox live check — is **partly stale**:
`internal/runner/routing_e2e_test.go`'s `TestRoutedSwitchKeepsTheFirstLineLoop` already runs both
upstreams in one run and asserts the reviewer's call reached Anthropic (**F4**).

## What this folder is not

- **It is answered, and it is a decision.** D1410–D1429 are questions with a recommended answer
  each, and the owner took **every recommendation on 2026-11-07**
  ([reconciliation.md](reconciliation.md) §7, the dated answer in each row), approving every task in
  [implementation-plan.md](implementation-plan.md) the same day (owner: "All tasks approved.
  Jobmax").
- **It is not a second answer to the owner's cut of 2026-11-06.** The owner removed the api-key
  review from 0.20.0 deliberately, and nothing here reopens the release slot: **D1421** asks whether
  to, and the answer of 2026-11-07 is **after 0.20.0**, so the cut stands.
- **It is not a plan to add a second credential path.** The ask's own instruction — "decide how it
  fits the existing machinery rather than adding a parallel one" — is the constraint every task in
  [implementation-plan.md](implementation-plan.md) is written to, and **§3** of that file says what
  the instruction rules out.
- **It is not a substitute for the security review.** AK4–AK7 touch a model credential the service
  attaches to a caller's request, and are written as **SEC** tasks — opus, with the independent
  review mandatory. That review is owed before those tasks land, not after.
- **It claims no number of another work.** The decision block is read above every allocation on
  `main` (§8) and the live check is well above the register's highest written section; the
  register's own file is not this task's to write (implementation-plan §5).

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1410–D1429** | this work's block, read on 2026-11-07 over `origin/main` (`aa26f322f`) and every open pull request's head. The highest decision written anywhere on `main` is **D1409** (`docs/catalog-build-context`, merged as #1805), and a `git grep` for the decision numbers in the `D141x` and `D142x` ranges over `main` and over all open heads returns **no path at all**, with positive controls run so that "nothing" is not a broken command. **D1410–D1429 are all written** in [reconciliation.md](reconciliation.md) §7 and **every one of them is answered, as recommended, on 2026-11-07**; none is reserved unused. The reading, with its commands, is §8 |
| **Check 330** | this work's live check (implementation-plan §5), far above the register's highest **written** `## Check` section: **290** at this folder's own reading ([reconciliation.md](reconciliation.md) §8, `origin/main` at `aa26f322f`, 2026-11-07) and **291** since `docs/image-env`'s check was written (`docs/gcp-live-checklist.md`, the image-`ENV` work's check: what a catalog image's `ENV` does in a run, itself **NOT RUN**). **292 is claimed** by `docs/catalog-build-context` (merged on `main` as #1805); **293–299** are claimed by nobody; `docs/stateless-orchestrator` claims 300–309 and `docs/cli-output-console-graph` claims 310–314, both below this block. Like every claim above the highest written section, 330 is **claimed, not reserved**: the register's rule is that a claim is settled at its merge, and whichever of two claims merges second renumbers itself. The register's own row and section are written by the task that implements the check, under the number free **at that merge** |
| **Findings F1–F24** | this folder's own numbering, in [reconciliation.md](reconciliation.md) §1 and §2, taking no number any other work reads |
| **AK1–AK8** | this folder's task series, in [implementation-plan.md](implementation-plan.md). Distinct from `docs/cloud-senior-review`'s **CSR1–CSR18** and from `docs/in-job-review`'s **Q1–Q17** on purpose: two series in one tree that share letters would be read as one |

## Open items

These were the owner's, and **all six are closed: he answered every one on 2026-11-07, each as
recommended.** The full text, with the reason each one existed and the answer it carries, is
[reconciliation.md](reconciliation.md) §9; this is the index.

1. **D1410 — answered: both slices.** The ask names **one** thing, "the api-key review path", and the
   tree has **two** independent reviews: the in-job stage and the cloud review run. The gate that
   carried — **no task in [implementation-plan.md](implementation-plan.md) may start before this is
   answered** — is lifted, and the plan covers both halves. The in-job half needs a name and a test,
   while the cloud half needs the credentials service changed **and is refused outright today**: every
   review launch is gated by `reviewJobAdmitted`, which is `false` until E7 (**F10**, **U6**).
2. **D1421 — answered: after 0.20.0.** The owner cut the api-key review from 0.20.0 on 2026-11-06,
   and the answer keeps that cut, so this design reopens nothing.
3. **D1412 / D1413 — answered: no secret of its own, and the key wins when the owner asks for it
   explicitly.** The run has exactly one Anthropic key (**F6**), and a project that publishes a
   stage account for `review` takes the stage **off** the gateway and onto the account (**F7**) — so
   the answer fixes which of the two wins; the spelling of that explicit request is the plan's to
   design.
4. **D1416 / D1417 — answered: the Anthropic key first, paired to the project's own key.** The
   Anthropic-key half needs the pairing to name the project's own key (**F12**, **F13**); the
   OpenRouter half needs the provider-key apparatus of §6.7 (**F15**), and the answer is that one
   design does not carry both.
5. **D1414 — answered: no.** Under `agent.auth: oauth` the gateway is refused
   (`internal/runner/routing.go` `GatewayOn`), so a subscription-coder run can never have an api-key
   review (**F8**), and it stays that way.
6. **D1415 — answered: a new additive field.** `runstore.StageTiming.Credential`'s stated meaning is
   "the credential that served this stage **when it was not the run's own**", so recording one for a
   stage the run's own key served is a meaning change under an existing name — which CLAUDE.md
   forbids — so the answer is the new field and not a new value on `credential` (**F18**).
