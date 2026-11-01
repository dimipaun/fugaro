# The review half's live check — the register row's shape, and the owner's eleven steps one line each

**Status: a stub, and NOT RUN.** The check itself is [spec.md](spec.md) §8, which is the design's own
statement of it; this file is the **register-shaped** entry it becomes and the steps as the register
records them, so the task that takes the number derives neither the shape nor the numbering again. **No
agent runs any step of it**, and no agent has: every step measures a live project, a real review run and
an owner's own terminal.

**The number is taken at that task's merge, not here.** `docs/gcp-live-checklist.md` is on the
do-not-touch list of the release window in flight (0.16.0's, D1072), so the section below is written under
the **name** §8 gives it and the number is the one free at the merge of the pull request that adds it —
C3.9g's, per [implementation-plan.md](implementation-plan.md) C3.9g. That is the register's own rule: a
claim above the highest written section is settled at its merge, and whichever of two open claims merges
second renumbers itself. The highest written section on `main` is **Check 190**
([README.md](README.md) §"Numbers this work holds").

## The row's shape, which is Check 190's

The register's sections are one shape, read off its highest written one (`docs/gcp-live-checklist.md`,
"Check 190" — written by task B0.4 of [docs/secrets-brokering/](../secrets-brokering/), with the owner's
steps in a file of that folder, [check190-steps.md](../secrets-brokering/check190-steps.md), the same
split this file makes):

1. a heading `## Check <N>: <the check in one line> (<scope>; run by you; NOT RUN)`, the scope naming
   the installation and the never-`belong` rule;
2. a first paragraph opening **NOT RUN**, naming the task that wrote the section and pointing at the file
   holding the steps — **the row carries no steps of its own**;
3. `**What the check settles.**` — the facts only a live project can show, and the reconciliation's
   findings they answer;
4. `**Prerequisites.**` — the installation, the sandbox repository and pull request, the tools, and the
   wall clock;
5. `**What you type**` — the commands, and the `CHECK … RESULT` block that is pasted back;
6. `**Restore.**` — what is deleted, and the counts asserted `0` before the run ends, from the exit trap
   too.

## The section as it will read

> ## Check <N>: the review class reads one repository, signs one verdict, and reaches nothing else (the
> `fugaro` installation only, sandbox repository only, never `belong`; run by you; NOT RUN)
>
> **NOT RUN.** Written by task **C3.9g** of
> [c39-review-identity/implementation-plan.md](implementation-plan.md), and **run by the owner**; the
> steps are [c39-review-identity/live-check.md](live-check.md) — this section carries no steps of its own.
> It is red until this slice and the review work's **CSR7** and **CSR2** are on `main` (§8's own sentence),
> and the steps that read another work stream's facts — 1 (CSR6's two `doctor` lines), 4 (CSR1's set) and
> 9 (**U7**'s observation) — are re-read against the tree being merged.
>
> **What the check settles.** …
>
> **Prerequisites.** Installation `fugaro`, a sandbox checkout with a sandbox pull request, and `gcloud`
> and `fugaro` on PATH — §8 names no tool beyond those two, and the row takes its wall clock from the
> steps it carries. Never `belong`.
>
> **What you type.** The eleven steps below, and the `CHECK <N> RESULT` block pasted back here.
>
> **Restore.** Nothing of the check's is created outside the sandbox; the throwaway signature, the
> tampered copy and the second-sign attempt leave the sandbox's own artifact and the service's own node
> where they were, and the run ends with the review's spend and the artifact kept reported.

## The eleven steps, in register shape

Numbered as §8 numbers them — that numbering is what the block the owner pastes back records, so it is
fixed here rather than worked out at merge.

1. **The two facts `fugaro doctor` reports.** The credentials service is enabled for this installation,
   and the review variant's account is admitted; a `warning` line instead is the finding, and the check
   stops there.
2. **The variant holds no credential at all** (ruling O1). `gcloud run jobs describe <the review job>`
   shows **no** secret mounted — not the account's Claude token, never the App key — and the variant's
   service account holds **no** `secretAccessor` on the App key, on the bundle or on anything else.
3. **A review launches and runs.** `fugaro review --pr <N>` on the sandbox completes, and its log names
   **no** token value, read by eye.
4. **The token is read-only and it is one repository's.** A minted token's push is refused by GitHub
   (403), and a hand-made call naming a different repository the account is admitted for is refused by
   the **service**.
5. **The model call goes to the service, for the account's own tier only.** The gateway's log names the
   service, the service's audit line for that call names the account's own **tier A** credential and no
   other, and a hand-made call naming a model only another tier serves is not served by another
   account's credential.
6. **The verdict is signed.** `runs/<slug>/<review-run>/artifacts/review.json.sig` exists and is one line
   of lowercase hex; the coordinator's path (or step 8's call) answers valid; the **sign line** names the
   account, the slug and the run and carries **no** pull requests.
7. **A tampered artifact does not verify.** One byte changed in a **copy** of `review.json` (never the
   stored object) → the signature no longer verifies; a valid signature pasted under a different review
   run → `{"valid":false}` from the service itself, before any coordinator check; an artifact with no
   sidecar → refused.
8. **The verify route's caller class, and what its line carries.** With the owner's identity in the
   verifier list, a hand-made `POST /v1/review/verify` with the owner's ID token answers `{"valid":true}`
   for the real pair and `{"valid":false}` for the tampered one, and the same call without a token is a
   **typed refusal**; the log shows the **verifier's email as its caller field** and the **signing key's
   entry name as its credential**, with no run and no job.
9. **A subprocess inside the container can reach nothing — U7, re-observed after the route exists**
   (D1074). From inside a mutant subprocess of the mutation check, both a `POST /v1/review/sign` and a
   request to the **instance metadata server** fail.
10. **The second sign is refused, and it is the security event.** With the run's own identity, a second
    `POST /v1/review/sign` for a run that already signed answers `request_refused`, **no** second
    signature exists, and the service's warning line is what an operator's alerting selects.
11. **The gate still holds where it was written.** A **coding** shared project job's account is refused
    on every route and the **review account is refused the verify route**, read from the refusal's own
    sentence; then the check's own cost: the review run's spend, the artifact kept, and nothing created,
    enabled or deleted outside the sandbox.

**§8 of [spec.md](spec.md) is what the check is, and this file is how it reads as a register section:
where the two disagree, §8 governs and this file is corrected.** Steps 1, 4 and 9 read other work
streams' facts — CSR6's two `doctor` lines, CSR1's read-only set, and U7's egress observation — while
step 2 rests on ruling O1's keyless variant, which is this slice's own; whoever writes the row re-reads
each of them against the tree and the shipped image being merged, and the "NOT RUN" line names which is
still owed.
