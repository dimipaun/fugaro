# The api-key review path — the independent review served by an API key instead of a subscription account

**Status: AK1–AK5 and AK6a are implemented on `main` (AK6a merged as #2147, `03c2a22c`); AK6b is
written and in review; no further task is started; the decisions
D1410–D1429 are answered 2026-11-07 and all tasks are approved 2026-11-07 (owner: "All tasks
approved. Jobmax") — except AK9 and AK10, which the security review added and which are written and
not approved.** Every answer is the recommendation, and the ones that decide the shape are worth a
line: **D1410 both slices** — the in-job half and the cloud half; **D1411 and D1412 Path A, and no
second Anthropic key**; **D1413 the key wins when the owner asks for it explicitly**; **D1414 no** —
an `oauth` run's review does not move onto an API key; **D1415 a new additive field** on
`StageTiming`, never a new value on `credential`; and **D1418, answered 2026-11-09: a provider's base
URL and auth shape go in the bundle**, as additive optional fields. This folder is a design for the
owner to read, and the six tasks that have landed (AK1–AK5 and AK6a) are its first two levels and the
first half of the third. **Items 1–5 of
[implementation-plan.md](implementation-plan.md) §6 are satisfied; item 6 is open and item 7's gate is
lifted.** What the folder has for item 6 is **the author's own pass** over the merged AK4 and AK5
([security-review.md](security-review.md)) — **not the independent review the SEC tier and
`.claude/rules/credentials-service.md` require, as that file says of itself** — so item 6 is open for
AK4 and AK5 (which landed under it, not past it) and stands for **AK6a**, **AK6b** and **AK7**. Three
things are still owed, and each is named where it stands: **the independent security review**, for the
merged AK4 and AK5 and then for the three tasks that have not landed — **D1413's spelling**, which §6
records as design work this plan still owes because no task names it yet — and **the owner's approval
of AK10, without which AK6b's release may not ship** (§6 item 11: the credential write that can outrun
the deployed service is guarded in the same release). **E7 is met as of 2026-11-08** (the flip below),
so a `fugaro review --pr N` is
admitted where it was refused; what no live installation has measured is either cloud path, which is
**Check 330**. **Check 330 is written and NOT RUN**: task **AK8** wrote its summary row and its
`## Check 330` section into [docs/gcp-live-checklist.md](../gcp-live-checklist.md), and
`bash scripts/live-checks/check330.sh` — with the fake-tool suite `check330_test.sh` beside it — is
the script the owner runs, so the only thing left between this folder and that reading is the owner's
own run of it, on a sandbox installation and a sandbox repository, never `belong`.

**Reconciled with `docs/model-crafts` on 2026-11-10** (that folder's **D1638** and **F26**). The owner
ruled that **api-key-review is NOT superseded by model-crafts; model-crafts builds on it** — so
**AK1–AK7 stay exactly as planned here**, and model-crafts' **MC5** (credential commands and pool
selection in the runner) and **MC6** (the credentials service serves Anthropic-key and OAuth pool
members) **extend what AK6a, AK6b and AK7 build and never replace it**: they are held until AK6b and
AK7 have merged and they re-decide no axis value, no bundle field, no forwarding rule and no model
path. **AK9 and AK10 are folded into MC6**: written and not approved here, they are carried there,
where MC6 is already the task that writes the credentials service's own files. AK9's files (the
refusals and the note that still say a review is served by a Claude account) and AK10's (the guard on
the bundle write at `fugaro creds set`) are booked in that folder's plan; **the fold approves nothing
and unapproves nothing**, and AK10's release precondition travels with it (§6 item 11): the release
that first ships AK6b's provider entry carries the guard, which MC6 now carries — so that release is
one that also carries MC6, which lands after AK6b's commit. That is the reading the fold leaves, and
it is recorded here rather than left to be discovered.

**Asked by:** the repository owner, in a message of **2026-11-07** — "the api-key review path: let's
start a design". The role is the one the ask's own clauses describe: the person who owns the
installation and the account the review runs on. No `@handle` and no name is recorded anywhere in
this folder, by this repository's rule. The ask is reproduced verbatim in [spec.md](spec.md).

**What it is.** The tree has **two** independent reviews, and both were designed around a Claude
**subscription** account — and **since AK4 and AK5 the cloud one may instead be served by the
project's own Anthropic API key**: the policy's pairing names the sentinel
`config.AnthropicKeyAccount`, the job for it mounts nothing, and the class's own door admits it. The **in-job** one is the mixed run's stage-scoped account
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
whose own `agent.auth` is an API key (`internal/runner/routing.go` `checkReviewClassModelPath`). It **launches since 2026-11-08**: `internal/cli/review.go`'s `reviewJobAdmitted` is `true`, flipped by
`docs/cloud-senior-review` §6's **CSR11** once the service's admission of the review caller reached the
tree (that section's §1 **E7**, its second half **CSRW1** and **CSRW2**), and the live-check switch the
flip replaced is deleted with it — before that it was built and not admitted, which is the state
`docs/releases/v0.20.2.md`'s known limits record of their release. **The launch is refused while the
installation itself still predates that release**: the launcher judges the installation's own release
(`internal/cli/recipes_skew.go`'s `checkReviewAdmission`, `reviewAdmissionSince`), so an installation
between the binary's upgrade and its `fugaro update` is refused rather than started on a service that
would answer 401. The owner
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
of it, and neither of them is what stands between the owner and a review: the launch was refused
outright by `reviewJobAdmitted` until E7, and **the flip of 2026-11-08 admits it** (**F10**,
**U6**) — so this design prices the cloud code and says plainly that what is still owed is the live
measurement (Check 330) rather than the code path.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's ask of **2026-11-07**, verbatim, with a header naming the owner by role and the status line. Where this file and the others disagree about what the ask *says*, this file wins |
| [reconciliation.md](reconciliation.md) | Every clause of the ask against the code on `main`, read by path and symbol: the findings **F1–F27** (Wrong / Exists / Gap / Rule / Open), what the **in-job** path does today end to end (§1), what the **cloud** path does today end to end (§2), the two readings of "the api-key review" (§4), **the security analysis as an attacker would see it** (§5), an **UNVERIFIED** section (§6), the decisions **D1410–D1429** each with a recommendation, an alternative and **the owner's answer** — 2026-11-07, except **D1418**, answered 2026-11-09 — (§7), the number block and how it was read (§8), and the owner's **Open items**, each with the answer it now carries (§9). **Where it disagrees with [spec.md](spec.md) about the code, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **AK1–AK10** — including **AK6a** and **AK6b**, the two the reconciliation of 2026-11-09 split the old AK6 into — each with its files, its interfaces, its named tests and command, its **red step**, its **mutation**, its **reviewer tier**, its dependencies and its commit; the levels, the critical path and the shared-files table proving no level has two writers of one file (§1–§2), what is deliberately not in the plan (§3), the release slot — **after 0.20.0**, from D1421 (§4) — the owner-run live check **Check 330** (§5), the gates a task may not start without — the five the answers of 2026-11-07 satisfy, the independent security review's own state (item 6, **open**), the two the reconciliation added (items 9 and 10) and AK6b's release precondition (§6 item 11) — and the author's own security pass over the merged AK4 and AK5 in summary (§7) |
| [security-review.md](security-review.md) | **The author's own security pass** over the merged **AK4** and **AK5**, written **2026-11-09** and **awaiting the independent review item 6 asks for** — it says so itself: what was read, the properties it verified, the three attack questions (the bundle, the forwarded key, the base URL), its findings **F25–F27** and the tasks they became (**AK9**, **AK10**, both unapproved), the conditions it places on **AK6a**, **AK6b** and **AK7**, and that AK10 is a release precondition of AK6b. **It is not a second design**: it reviews code that is already on `main` |
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
  attaches to a caller's request, and are written as **SEC** tasks — opus, with the **independent**
  review mandatory. That review is owed before those tasks land, not after, and **it is not written
  yet**: what exists is **the author's own pass** over the merged AK4 and AK5
  ([security-review.md](security-review.md)), which says of itself that it is not the independent
  review the tier asks for. So **§6 item 6 stays open** for AK4 and AK5 and for **AK6a**, **AK6b** and
  **AK7**, and no release note may present any of them as reviewed.
- **It claims no number of another work.** The decision block is read above every allocation on
  `main` (§8), and **Check 330 collides with no other work's claim** — the register's written
  sections have moved above it since it was taken, and no document claims 330 — so the number stays
  this work's; the register's own file is not this task's to write (implementation-plan §5).

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1410–D1429** | this work's block, read on 2026-11-07 over `origin/main` (`aa26f322f`) and every open pull request's head. The highest decision written anywhere on `main` is **D1409** (`docs/catalog-build-context`, merged as #1805), and a `git grep` for the decision numbers in the `D141x` and `D142x` ranges over `main` and over all open heads returns **no path at all**, with positive controls run so that "nothing" is not a broken command. **D1410–D1429 are all written** in [reconciliation.md](reconciliation.md) §7 and **every one of them is answered, as recommended: all on 2026-11-07 except D1418, which the owner answered on 2026-11-09** (the base URL and the auth shape go in the bundle); none is reserved unused. The reading, with its commands, is §8 |
| **Check 330** | this work's live check (implementation-plan §5), **claimed and not written**: the register carries no `## Check 330` section. The register's highest **written** `## Check` section is **360** (`docs/console-managed-screens`'s, written by its CM12), with **340** (`docs/periodic-reviews`, under its §P8), **291** (`docs/image-env`) and **290** (the mixed-credentials run) written beside it — 330 was taken when the highest written section was **290** and the register has moved above it since, so the number now sits in the gap between `docs/cli-output-console-graph`'s **310–314** and the periodic-reviews block **340–349**, where **315–329** are written by nobody. The claims around it are **292** (`docs/catalog-build-context`, merged as #1805), **306–309** (reserved by `docs/stateless-orchestrator`, whose 300–305 are written), **310–314** (`docs/cli-output-console-graph`), **341–349** (`docs/periodic-reviews`: 341 and 342 taken, 343–349 reserved), **350–359** (`docs/failure-classes`), **361–369** (`docs/console-managed-screens`'s available numbers) and **370** (`docs/security-classification`, with 371–379 available) — and **none of them is 330**, nor does any other document claim it. Like every claim it is **claimed, not reserved**: the register's rule is that a claim is settled at its merge and the later of two **colliding** claims renumbers itself, which 330 does not do. The register's own row and section are written by the task that implements the check (**AK8**), under the number free **at that merge** |
| **Findings F1–F27** | this folder's own numbering, in [reconciliation.md](reconciliation.md) §1, §2 and §5 — **F25–F27** are the security review's ([security-review.md](security-review.md), 2026-11-09) — taking no number any other work reads |
| **AK1–AK10** | this folder's task series, in [implementation-plan.md](implementation-plan.md) — the two halves of the old AK6 take the letters **AK6a** and **AK6b** rather than renumbering AK7 and AK8. **AK6a is merged** (`03c2a22c`), **AK6b is in review**, **AK7 is not started**, and **AK1–AK7 stay as planned** on the reconciliation of 2026-11-10. **AK9 and AK10 are unapproved**, and **AK10 gates AK6b's release** (§6 item 11) — and **both are folded into `docs/model-crafts`'s MC6** as of 2026-11-10 (§6 item 12, that folder's D1638 and F26): written and not approved here, they are that task's to write. Distinct from `docs/cloud-senior-review`'s **CSR1–CSR18** and from `docs/in-job-review`'s **Q1–Q17** on purpose: two series in one tree that share letters would be read as one |

## Open items

These were the owner's, and **all six are closed: he answered every one on 2026-11-07, each as
recommended.** The full text, with the reason each one existed and the answer it carries, is
[reconciliation.md](reconciliation.md) §9; this is the index.

1. **D1410 — answered: both slices.** The ask names **one** thing, "the api-key review path", and the
   tree has **two** independent reviews: the in-job stage and the cloud review run. The gate that
   carried — **no task in [implementation-plan.md](implementation-plan.md) may start before this is
   answered** — is lifted, and the plan covers both halves. The in-job half needs a name and a test,
   while the cloud half needs the credentials service changed **and was refused outright when this was
   written**: every review launch was gated by `reviewJobAdmitted`, `false` until E7 — the flip of
   2026-11-08 made it `true` (**F10**, **U6**).
2. **D1421 — answered: after 0.20.0.** The owner cut the api-key review from 0.20.0 on 2026-11-06,
   and the answer keeps that cut, so this design reopens nothing.
3. **D1412 / D1413 — answered: no secret of its own, and the key wins when the owner asks for it
   explicitly.** The run has exactly one Anthropic key (**F6**), and a project that publishes a
   stage account for `review` takes the stage **off** the gateway and onto the account (**F7**) — so
   the answer fixes which of the two wins; the spelling of that explicit request is the plan's to
   design.
4. **D1416 / D1417 — answered: the Anthropic key first, paired to the project's own key**, and
   **D1418, answered 2026-11-09: that half's provider endpoint is the bundle's** — a base URL and an
   auth shape as additive optional fields. The Anthropic-key half needs the pairing to name the
   project's own key (**F12**, **F13**); the OpenRouter half needs the provider-key apparatus of §6.7
   (**F15**), and the answer is that one design does not carry both.
5. **D1414 — answered: no.** Under `agent.auth: oauth` the gateway is refused
   (`internal/runner/routing.go` `GatewayOn`), so a subscription-coder run can never have an api-key
   review (**F8**), and it stays that way.
6. **D1415 — answered: a new additive field.** `runstore.StageTiming.Credential`'s stated meaning is
   "the credential that served this stage **when it was not the run's own**", so recording one for a
   stage the run's own key served is a meaning change under an existing name — which CLAUDE.md
   forbids — so the answer is the new field and not a new value on `credential` (**F18**).
