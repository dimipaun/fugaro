# The coordinator agent

**Status (2026-10-24): approved to build on the branch `coordinator`, target 0.11.0; partly implemented — P0, P1, P2, P3, P4 and P5 are on `coordinator`, and P6–P8 are not started.** P1 is `fugaro coordinator status` over the view in `internal/coordinator/`: the one-repository read, and the first part of this work that produces something a person can look at; P2 is that command's `--all` and the credential behind it — one read-only installation token per repository, minted by the coordinator and cached for the token's life, never the run's own two-write one. P3 is the state that view will be written into: the `coordinator/` subtree, an account of its own that writes it, and a read grant for the signed-in person. P4 is the report path: `fugaro coordinator report`, one intent record per declaration under `coordinator/repos/<slug>/intents/<id>` — the branch, the target release, the files and the services a caller is about to touch — and the same writer `launchRun` calls, so a launch declares itself without a second command. P5 is the aggregation job: a scheduled Cloud Run job in the installation root, on the history job's pattern, that re-reads each repository through P2's reader, recomputes P1's view and writes it into P3's nodes — recording what it could not reconcile as a `stale` marker rather than half a view. It is **off by default**: nothing of it exists until the owner opts in (the plan's P5, **For operators**). It is what makes P4's records readable rather than merely writable, and it is the identity that holds the Firebase project's database admin, so the fence around it is `internal/coordinator`'s own subtree check and not IAM. This directory holds the owner's spec for a **coordinator agent**, the
reconciliation of that spec against the code at `origin/main` `c6009b5f` (`release:
v0.8.6 (#637)`), the questions the reconciliation could not answer on its own, the
owner's answers to them, and the plan that turns those answers into ordered parts. What
follows from these documents is the Go above, the Terraform grant that gives the
coordinator's writer its one role (the account P5's aggregation job creates once the owner opts in with `fugaro update --firebase-project <id> --coordinator`, so the grant has a member; off by default), and the database rules change that admits the
signed-in reader — and no TypeScript and no live action: what is here lands on the
long-lived branch and not on `main`, and the owner deploys the grant and the rules. **Owner: Dimi Paun. Date: 2026-10-19.** The plan's own date is **2026-10-21**,
written against `main` at `1e0953b7` (`docs(central-orchestrator): spec, reconciliation and plan
for central orchestrators (#775)`). The reconciliation was run against
`c6009b5f`, and every path and symbol in it was read at that commit; the citation check that
backs that claim, and the shape of citation it does not cover, are stated in
`internal/cli/docs_coordinator_test.go`'s own comment rather than left implied.
**The reconciliation's** citations into `docs/design/` and `docs/mobile/` name the document
and the phrase the citation turns on rather than a line number — those two trees gain
sections, rows and paragraphs while this work sits in review, and a line number into one of
them moves under the sentence it was written for without anything looking wrong.
`TestDocsCoordinatorFrequentCitationsAreAnchoredByContent` holds every one of those citations that names a file in this repository to the single line of that document which carries the phrase — and it reads the reconciliation and nothing else. The ones it does not hold name something other than a file here: the two designs that are only in open pull requests (PR #632's always-on orchestrator and PR #634's console styling) and the glob §0.3 quotes from the brief — each excused by name, with its reason, in `internal/cli/docs_coordinator_test.go`.
**This index's own citations into those two trees outside its numbers section name no phrase and are held by no anchor**: the Check 110 row below and the reading list at the end name a whole document each, and no sentence in one.

**All sixteen questions are answered, and none is approved.** The owner answered Q1–Q16 on
**2026-10-19**, each by taking **option (A)**, the recommended one; each answer is recorded
under its question in the reconciliation's §3, with what it means for the spec.
**An answer is not an approval**: an answer picks an option and approves no work. The five decisions the planning afterwards found — **D193–D197** ([reconciliation.md](reconciliation.md) §6) — are **answered 2026-10-21**: four took the recommendation and **D196 ruled the other way**, and the plan's owner gate is **passed for the parts that need only those decisions (P0–P6)**, on the long-lived branch **`coordinator`**, target **0.11.0**. **P7 and P8 do not start yet**: they wait on the front-door design review, and on nothing else — the two owner checks below stay open beside them.
The options that were not taken are left in place, unedited, so the answer can be
read against what it was choosing between. Two things the answers do not settle: **U2 stays
an owner check** (whether the GitHub App already has `pull_requests: read`, which Q3's answer
asks it for — P2's credential turns on it), and **U3 stays an owner check** (whether an
installation token may omit `repositories`, which D193's answer leaves the project-wide token
waiting on). **U6/U7 are still drafts** in open pull requests.

A coordinator is a **per-project service** that aggregates what every local orchestrator is
doing, owns project-level state, detects conflicts early and suggests sequencing — advisory,
never authoritative. The spec is the owner's own text; the reconciliation is what the
repository actually has against it.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | **The owner's spec, verbatim** — *"Fugaro Coordinator Agent — Design Spec"*, sections 1–11, exactly as received. Committed alone, in its own commit, before anything else in this directory. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief this work was written from (§0); the spec's concepts against what the repository has (§1); findings **#1–#30**, each classified **Wrong / Exists / Gap / Rule / Open**, each citing a path and a symbol and stating its consequence (§2); **Q1–Q16**, each with two or three options, the recommendation first, what it changes in the spec, and **the owner's answer of 2026-10-19** (§3); the **UNVERIFIED** table (§4); the glossary the spec's words need (§5); and the **five new decisions the planning found — D193–D197** (§6), each recommendation first, with the alternative, its cost and the part it blocks. |
| [implementation-plan.md](implementation-plan.md) | The plan: what it is written to and what changed since the reconciliation (§1); the order and the five dependencies that force it (§2); what is **deliberately not** in it (§3); **"What the owner must answer before any part starts"** (§4), a table naming every decision, what it blocks, the recommendation the plan is written to and **the owner's answer of 2026-10-21**; the parts **P0–P8** (§5), each mergeable on its own on the long-lived `coordinator` branch, each with its scope, the paths and symbols it touches, its **red step** and the mutation that proves it, its verification, what it depends on, its reviewer tier, whether it is security-sensitive, and its commit message; the critical path and the effort (§6); the **security-sensitive parts and what an independent review of each must cover** (§7); the test rules (§8); the owner-run live checks **proposed by name and not numbered** (§9); the numbers held (§10); and the **owner approval gate** (§11), **passed 2026-10-21 for P0–P6**. **P0–P6 may start; P7 and P8 wait on the front-door design review, and on nothing else** — the owner's U2/U3 checks stay open beside them. Target release **0.11.0**. |

## Typos fixed in `spec.md`

**None.** The spec is copied byte for byte from the text the owner supplied, and it needed
no correction. Nothing was reworded, reordered, re-headed or re-numbered, and no sentence
was added. If a later reader finds a difference between `spec.md` and the owner's copy, the
owner's copy is right and `spec.md` is the file to fix.

## The numbers this work holds

| Numbers | Whose | State |
| --- | --- | --- |
| **D1–D192** | the owner's allocation, block by block as the one table in this repository that lists every work's block reads it. **Every block is cited by content and not by line**: it is read from the one line of the document named after it that carries its label, written as that document writes it — the voice block's own label is **D39–D54, plus D54b** there. A line number here went stale the moment another work inserted a row above the table it pointed into, which is why these are content anchors now: **D1–D20** the console (`docs/mobile/README.md`), **D21–D38** console v2 (`docs/mobile/README.md`), **D39–D54b** voice, written there as **D39–D54, plus D54b** (`docs/mobile/README.md`), **D55–D78** managed projects (`docs/mobile/README.md`), **D90–D109** the website (`docs/mobile/README.md`), **D110–D129** mobile (`docs/mobile/README.md`), **D130–D159** the credentials service (`docs/mobile/README.md`), **D160–D179** the mobile functional spec (`docs/mobile/README.md`) and **D180–D192** the website redesign, proposed (`docs/website/redesign/README.md`) | **taken**, with two exceptions the allocation's own documents record: **D79–D89** is **deliberately unused** — *"an unused number is not a free number"* (`docs/website/redesign/implementation-plan.md`) — and **D180–D192** is **proposed, not yet confirmed by the owner**. The highest number written anywhere in `docs/` at `c6009b5f` is **D192**, and the row below is what this work wrote into the free block after it |
| **D193–D197** | **this work's**, taken from the **D193 onward** block the owner's register records as free, and written in [reconciliation.md](reconciliation.md) §6 | **answered 2026-10-21**, four by taking the recommendation and **D196 against it**. The owner's earlier answers took **no** decision number — its items were **Q1–Q16**, and an answer picks an option rather than allocating one. The planning found five decisions the answers do not settle, and they are numbered: **D193** the GitHub read reach — **one read-only installation token per repository**, cached per repository, the project-wide token waiting on U3; **D194** the writer identity — **the aggregation job's own account holds `roles/firebasedatabase.admin`** beside the history account's, and the rules change is the read grant only; **D195** the node layout — **a new top-level `coordinator/` subtree**, `coordinator/project/` and `coordinator/repos/<slug>/`, deterministic IDs, `GetETag` + `PutIfMatch`; **D196** which service is the front door's home — **the coordinator's own resident service, built by this work** (the recommendation was the central orchestrator plan's service, and the owner ruled the other way); **D197** the override log's retention — **the last 200 per project**, newest first, no expiry in v1. Each was written **recommendation first**, and the plan is written to the answers. **D198 onward is free.** |
| **Checks 1–109** | the live checklist (`docs/gcp-live-checklist.md`), block by block as its own register reads it, cited by content the same way — the register writes its own sections (`## Check 70a`), and the two blocks below are written **Checks 90–99** and **Checks 100–109** on the lines named beside them: **48–56** reserved by the project-centric plan (`docs/gcp-live-checklist.md`), **59a/59b** the desktop console's (`docs/gcp-live-checklist.md`), **60** the budget-token channel's (`docs/gcp-live-checklist.md`), **61** project-centric phase 6's (`docs/gcp-live-checklist.md`), **62–63** console v2, **64–66** voice and **67–68** managed projects, each reserved (`docs/gcp-live-checklist.md`), **69a–69c** the website's (`docs/gcp-live-checklist.md`), **70a–70c** the website redesign's, proposed (`docs/gcp-live-checklist.md`), **71–79** inside the website work's held range, with no check written in them (`docs/gcp-live-checklist.md`), **80–89** mobile's, carved out of that range (`docs/gcp-live-checklist.md`), **90–99** the credentials service, written **Checks 90–99** (`docs/credentials-service/README.md`) — of which the register writes **90**, **91**, **92**, **94**, **95**, **96** and **99** itself, so **93**, **97** and **98** are what it does not reach — and **100–109** the mobile functional spec, written **Checks 100–109** (`docs/mobile/README.md`), whose first five the register now writes (Task P6.11, below) | **taken or reserved**, each range's own state being the register's: **71–79** are **held but unused** — the website work holds *"69 and up"* (`docs/gcp-live-checklist.md`), and no document writes a check in them; **105–109** are **reserved and unused** (`docs/mobile/README.md`). The highest check *section written* is **149** — **111** the setup wizard's billing picker, **112** and **113** the website's zoom levels and its phone page, **124a–124d** the Cloud Batch backend's own live check, **130** the licensing work's (the licence detectors: GitHub's MIT reading once the skeleton has put its licence statement beside an untouched licence file, and that statement file rendering), **135**, **136** and **139** the task-governance work's (a per-repository job account's read of the runs bucket by impersonation, a report run's own pull request, and what an agent ID distinguishes in a gateway run's log — the block runs from **135** to **144**, and beside these three **137** and **138** are task T7.5's and unwritten while **140–144** stay reserved), and **145**, **146** and **149** the Bitbucket-credentials work's (the read consumer's facts, the coordinator sweep reading Bitbucket through the service, and the console showing the stored view on the owner's own machine), each written by its own work and **NOT RUN**, above the block this row reads; inside it the highest is **99** — the credentials service's cascade end to end, written by that plan's Task C1.23 and **NOT RUN**, beside its **94** and **95** (the service deployed, and a run through it), also written by Task C1.23 and **NOT RUN**; **96**, GitHub tokens through the service, is written by that plan's Task C2.4 and **NOT RUN** too (and not runnable on this tree: its own section says why); **92**, the rules deploy and the live helper a run's own identity is put through, is written by that plan's Task C1.5 and **NOT RUN**; **91**, the Cloud Run streaming check, is written by Task C0.3 and **NOT RUN** too, and **90**, the header swap, is the one of them that has run: **2026-10-21, PASSED — TIER A VIABLE**. **93**, **97** and **98** are unused: no document writes a check in them, and the register carries no section there |
| **Check 110** | **claimed by the always-on orchestrator design** (`docs/design/always-on-orchestrator.md`, PR #632, its §7 and Q5) | **claimed, not merged.** This work takes **no check number**, and it must not take 110 while that claim is open: the repository's own rule is that whichever of two open claims merges *second* renumbers itself (`docs/gcp-live-checklist.md`) |

A reserved number is never reused and never renumbered, and that is the register's own rule and
not this work's — the numbering ruling of 2026-10-13, on the register's own line
(`docs/gcp-live-checklist.md`). Neither block above is derived by grep alone: the D-block is read
from the one table in this repository that lists every work's block, and the redesign's proposed
block and the unused gap beside it from that work's own documents; the check numbers from the
paragraphs that allocate them — `docs/gcp-live-checklist.md` for every range up to **70a–70c**,
for mobile's **80–89**, and now for **90**, **91**, **92**, **94**, **95**, **96** and **99** themselves,
whose rows and sections the register writes and whose block's own allocation paragraph it
carries — and `docs/mobile/README.md`, `docs/mobile/implementation-plan.md` and
`docs/credentials-service/README.md` for **90–99** and **100–109**: of **90–99**, the register
writes **90**, **91**, **92**, **94**, **95**, **96** and **99**, and **97** and **98** are not written while **93**
is reserved and unused. Every block
either row states names the document it is read from, and
both rows are held to the line that document carries it on by
`internal/cli/docs_coordinator_test.go` rather than trusted. One range is read differently by two
documents: **70–79**. `docs/mobile/README.md` records them as unused and not taken — the line
reading *"Checks 70–79 are unused and deliberately not taken"* — which is what that work did: it
took **80–89** instead. The register reads **70** as the website redesign's and **71–79** as
inside the website work's held range, and the row above follows the register, which is the
document that allocates. **100–104** are **approved 2026-10-18**
(`docs/mobile/implementation-plan.md`) and **written** (`docs/gcp-live-checklist.md`), and the
two documents around them still disagree in a smaller place: the plan approved all of Phase 6 —
those five checks with it — on 2026-10-18, `docs/mobile/README.md` still writes them *proposed*,
and Task P6.11 has written all five into the register since (`## Check 100`–`## Check 104`,
between check 99's section and 111's). The row above follows the register for their written state
and the plan for whose block they are, and its state cell leaves their numbers to that sentence
rather than repeating the range, because the pin over the row
(`internal/cli/docs_coordinator_test.go`) reads every range in a cell that calls anything in it
unused against the sections the register has written, and these five are no longer that.

## What this work collides with, in one paragraph

The spec is a good idea whose deployment section describes a surface Fugaro has never had.
**There is no Cloud Run service anywhere in this repository** — every workload is a Cloud
Run *job*, and the type is not even in `fugaro init`'s plan allowlist, so adding one is a
decision the code enforces rather than an accident. The spec's *"whoever can reach the
project's coordinator is in the project; no extra permission model in v1"* is the opposite
of the standing design, which refuses `allUsers`, `allAuthenticatedUsers` and `domain:`
members everywhere it can and puts the trust boundary at the repository and the task source.
Its *"reuses the project's existing key management and security service"* names a service
that does not exist — the credentials service is a document, not code — and its *"Firestore
transactions"* and *"Firestore's real-time listeners drive live updates in the console"* are
both unavailable: the Firestore client is a hand-written REST client with no transactions
and no listeners, and the console renderer's CSP is `connect-src 'none'`, so it never talks
to the network at all. Its *"daily token-spend cap that counts toward the project's existing
spend caps"* cannot be satisfied by configuration, because every existing cap is keyed to a
**run's** Firebase identity and a long-lived service has none. And its LLM half would decide
the mobile design's deferred phase-2 question (D115) implicitly — which is exactly what
that deferral was written to prevent. What the spec gets right is the part that needs no new
surface: the project-wide view, the PR records, the roadmap and the override log are all
greenfield, all deterministic, and all buildable from the two stores and the one GitHub
credential the repository already has.

## What still reads this register the old way

This table is the register, and three other works read it at **their own base commits** and
concluded, correctly for those commits, that **D193 onward was free**. Writing **D193–D197** into
that block on 2026-10-21 makes those readings stale, and their documents were **not** edited by
this change — they are dated readings of a register, and rewriting another work's base-commit
reading from here would be this folder quietly editing someone else's record. Recorded instead,
site by site, so a reader who finds one of them knows what changed:

- **`docs/central-orchestrator/README.md`** and its reconciliation: **D193 onward "is free"**, and
  the recommended alternative is *"move this work to D193–D218"*. That alternative now overlaps
  this work's first five numbers: **a move into the block must start at D198** unless the owner
  rules otherwise, and its folder-local D1–D26 recommendation is unaffected.
- **`docs/windows-cli/README.md`** and its reconciliation: the same free-block reading and the
  same *"move this work to D193 onward"* alternative (its O6). Same correction: **D198 onward**.
- **`docs/mobile/voice-session/README.md`** and its reconciliation: it reads the same label and
  **leaves D193–D200 alone deliberately**, taking **D201**. It does **not** collide with anything
  here, and nothing in it needs to change.
- **`docs/central-orchestrator/reconciliation.md`** also quotes **this index's previous status
  sentence** verbatim, twice (its C4 and its finding #51), as the state of this folder at
  `c6009b5f`. Both quotes were true of that commit; this folder's status line changed on
  2026-10-21, and the quotes are readings of it at an earlier date rather than claims about it
  now.

**The rule this records rather than repairs**: a document that reads another folder's register is
right about the commit it read it at, and a register that moves is what makes the reader stale.
When a later work moves into D198 onward, this note is the thing to correct.

## How these documents are held

Thirteen tests pin everything above, and they are one command:
`go test ./internal/cli/ -run TestDocsCoordinator`. They are the reason a claim in this folder
can be trusted without re-reading the repository — the spec cannot stop being verbatim, a
citation cannot drift to a path that has moved, the numbers table cannot stop agreeing with the
documents it is read from, the plan cannot stop being an order rather than a list, and the five
decisions a reader has to weigh cannot be renumbered, nor their answers of 2026-10-21 drift —
the one answered against the recommendation least of all.
`TestDocsCoordinatorMutationRecordIsCounted` and `TestDocsCoordinatorSelfMapIsCounted` are the odd
ones out: they read `internal/cli/docs_coordinator_test.go` itself, holding that file's record of
its own planted mutations to the list under it, and its own count of the tests it declares to the
map it heads.

## Related

- [docs/design/v1.md](../design/v1.md) §1 — what a **Fugaro project** and an
  **installation** are (the spec's unit of deployment).
- [docs/design/project-centric.md](../design/project-centric.md) — the project as the unit
  of configuration, policy and credentials; the `project` Terraform module.
- `docs/design/always-on-orchestrator.md` — the close sibling: one resident service per
  installation, no model credential in it. **It is in open PR #632 and not on `main`**, so
  there is no link to it here. Reconciliation §2 #29 and **Q10**.
- [docs/central-orchestrator/](../central-orchestrator/README.md) — the sibling that landed as
  #775: the resident service per installation, to which every orchestrator reports, and whose
  **P9** records the coordinator's half as blocked on this folder's plan existing. **D196 was
  answered (B) on 2026-10-21**: the coordinator is **not** tied to that service, it gets **its
  own** (the plan's P7), and orchestrators reach it as clients — so P7 and P8 no longer wait on
  that plan's P4/P5. The two plans now share one thing: the allow-list admission of
  `google_cloud_run_v2_service`, and **whichever part lands first admits it and the other cites
  it** (§6 of the reconciliation, and §11 of the plan).
- [docs/credentials-service/](../credentials-service/README.md) — the "security service"
  the spec's §8 names. Plan only; D130–D154 answered 2026-10-17; nothing implemented.
- [docs/mobile/](../mobile/README.md) — the orchestrator, the phone, and D115 (phase 2
  deferred).
- [docs/design/desktop-console-v2.md](../design/desktop-console-v2.md) — the console's
  project-wide "Overview" (unbuilt) and D22, the one write (launch).
- [SECURITY.md](../../SECURITY.md) — the trust assumption the spec's §4 contradicts.
