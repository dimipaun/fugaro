# Task 3.0 (the project label): the eight open points, settled

**Addendum to [2026-10-16-managed-projects.md](2026-10-16-managed-projects.md), written 2026-10-18, beside [2026-10-18-managed-projects-task-3.0-addendum.md](2026-10-18-managed-projects-task-3.0-addendum.md). Docs only: this file adds no code of its own and changes no other document.**

The run of 2026-10-18 13:06Z (`fugaro/20261018-130637-1702`, PR #587) stopped and wrote the addendum above, because the brief it held and the plan's record disagreed about whether the label is wanted at all. Point 1 of that addendum was the reversal, recorded; the owner gave it on 2026-10-18, and it is now written in the two places that carry the record — `docs/plans/2026-10-16-managed-projects.md` (Task 3.0's heading and paragraph, the C3 row, the parallel-execution table row, and the approval-gate paragraph) and `docs/design/managed-projects.md` (§5.1 item 3) — superseding the ruling of 2026-10-17 that skipped the task. **Nothing else about the task changed.**

This file settles points 2 to 8 of the addendum, which is what a build needs before it starts. Where the plan or the design already answers a point, the answer is quoted and followed; where nothing does, the most conservative option is taken and **marked VETOABLE** — a decision this run made that the owner can reverse in one word. No point on this list is a security boundary decided silently: the boundary itself is set by the brief ("the label must be written only by an owner-run command, never by an agent session, never by `managed apply`; it must be verifiable and its absence or removal must fail closed"), and every point below either follows from it or narrows it further.

## 2. The label's name — `fugaro_managed=true`

**Settled: `fugaro_managed`, value `"true"`. The spec's `fugaro-managed` is recorded as the name not taken.**

The plan's own Steps say `fugaro_managed=true` (`:551`) and the owner's spec says `fugaro-managed=true` (`docs/design/managed-projects-spec.md:30`). They are two different keys, and a GCP project label key lives for the life of the project. The plan is the newer of the two documents and the one the task is written in, so the plan's spelling wins; the spec is left as it is, with this file as the record that its spelling was considered and not taken.

`internal/backend/gcp/names.go:56-57` already owns `LabelManaged = "fugaro"` / `ManagedValue = "managed"`, for *buckets* — the installation's own label on the runs bucket, which is what proves a bucket is a project's own. It is a different key on a different kind of object and is not reused: the project label gets its own constants, `ProjectLabelKey` and `ProjectLabelValue`, in `internal/infra/managed` beside the marker's, so that a reader of either cannot mistake one for the other.

## 3. Where it is written — `internal/cli/init.go`'s `markCreated`, and nowhere else

**Settled: the marker object's writer, and nothing else. Two marks, one operator-run step.**

The plan's Files list names `internal/cli/init_project.go` (`:162`); the plan's Steps name "the same operator-run path that writes the marker object". The Steps are right and the Files line is wrong, because the project stage runs before the installation stage and the runs bucket does not exist until the installation has applied — which is why `markCreated` sits where it does (`internal/cli/init.go:670-811`).

There is exactly one such place, and it is not a choice this run made: `markCreated` already holds the two things a label write needs and is the only writer of the creation fact. So the write goes **after** `managed.WriteMarker` succeeds, in the same function, on the same values (`r.res.Created`, `r.gcpProject`). `Marked = true` is set only when **both** marks are written: a run whose object landed and whose label did not has not marked the project in the sense the read will use, so it must not report that it did.

A creation-time write was considered and refused: `internal/infra/projectcreate.go`'s `CreateProject` builds only `ProjectId`, `DisplayName` and `Parent` (design finding 14), and adding the label there would put the mark on a project that is not yet known to be Fugaro's — a project that fails between creation and installation would carry a label with no object behind it. The label is a mark on the *installation*, not on the project's birth.

The owner-run gate is structural rather than newly written down: `projectStage.Apply` requires `env.Interactive && s.canType()`, and a coding agent's session confirms nothing (`initflow.CanConfirmManaged`), so no agent session can reach the creating path at all — and `markCreated` is on that path and no other. `fugaro restore` goes through the same creating path and checks `made.Marked`, so it is covered by the same gate. **No new refusal text is added to enforce this**, because there is no second way in to refuse.

Failures are warnings, exactly as `WriteMarker`'s already are (`:782-793`): the project exists either way, an unmarked project is the fail-closed direction, and the owner-run flagging of an existing project is the way back (D58).

## 4. What "agree" means — the label can only narrow, never widen

**Settled: managed requires the object's read to answer managed AND the project's label to be present and exactly `"true"`. Absent, other value, and unreadable project are all not-managed, each with its own one-line reason.**

The plan gives the two disagreement cases — either mark alone refuses — and not the rest. The rule is stated once, as a narrowing rule, because that is the property that makes the change safe to land on a shipped path:

| Object says | Label | Answer |
|---|---|---|
| managed | `fugaro_managed="true"` | **managed** — the amount, if any, is read from the object as it is today |
| managed | absent | **not managed** — "the project has no `fugaro_managed` label" |
| managed | present, another value | **not managed** — "the project's `fugaro_managed` label is … , not \"true\"" |
| managed | the project cannot be fetched | **not managed** — the reason the fetch gave |
| not managed | anything | **not managed** — the label cannot make a project managed |

**A test asserts the last row directly**: a project with the label and no object is not managed. Without it the rule would be an assertion about the first four rows only, and the fifth is the one that would let a label widen.

**This costs no new call and no new permission.** `readFact` already fetches the project object for its number check — `gcpProjectNumber` (`internal/infra/managed/read.go:392-398`) is `c.Projects.Get(gcpProject)` — and throws away the `Labels` the answer carries. The change returns them instead of discarding them. This is written down because it is the first thing a reviewer checks: the read's privilege is unchanged, and what is genuinely new is only that a second condition can now turn managed mode off.

Every answer stays in the shape the read already uses: an answer with a one-line reason (`safetext.OneLine`), never a failure. A project whose label cannot be read is not an error, it is **not managed**.

## 5. Projects already marked — they fail closed, with a one-line owner-run backfill. VETOABLE

**Settled: every already-marked project becomes not-managed until its label is written, the refusal reason says so, and `docs/gcp-setup.md` carries a one-line owner-run backfill. The refusal text does NOT name a command.**

Every managed project in existence today has the object and no label, because nothing has ever written one. Adding the agreement condition turns all of them not managed: `fugaro managed apply` and `fugaro restore` refuse, and the coding-agent refusal comes back. That is fail-closed, so it is safe, but it is a real regression for a real installation, and the plan does not mention it.

The task says so, and `docs/gcp-setup.md` gains the backfill beside the managed creation: one `gcloud` line the owner runs against a project that already carries the object. It is owner-run because the label is owner-run, for the reason in the task's own premise: `resourcemanager.projects.update` (design finding 14) is a write to the privilege boundary the feature rests on.

**VETOABLE, and this is the one point where this run took the more conservative of two defensible options.** The addendum recommends that "the refusal may name it" — a refusal that tells the person which command tightens the mark back, on the argument that it is the fail-closed direction and so is not "a way around the marker" (Task 3.2's line, `:601`). This run did not do that: **the refusal states the condition and not a command**, and the command lives in the setup document the owner reads. Two reasons. A refusal string is the one piece of this feature that reaches a *coding agent's* session, and a sentence that names `gcloud projects update` — run by whoever holds the session, with whatever credential is ambient — is a sentence that invites the boundary write from the side the brief says must never do it. And the brief's rule is that the label is written "only by an owner-run command"; the shortest reading of "only" is that no other path, including a printed one, points at it. The cost is small: a person who hits the refusal reads the same line in `docs/gcp-setup.md`, and `Status.Detail` already ends in a sentence a person can act on. **If the owner prefers the addendum's version, the change is one sentence in one test's expected reason.**

## 6. The job-account fixture — extended, and Open item 2 answered

**Settled: `internal/infra/managed/marker_test.go`'s module scan is extended with `resourcemanager.projects.update` and `resourcemanager.projects.setIamPolicy` as permissions no run account may hold, and the plan's Open item 2 conditional is answered rather than left standing.**

The plan's Open item 2 (`:927`) says that if the fixture cannot be written, **the label is refused instead**. The addendum's point 6 shows the fixture can be written, and this run confirms it from the tree: `marker_test.go` already scans every `.tf` under `deploy/terraform` for grants to non-people members, pins each one whole in `runAccountGrants`, classifies the role by the permissions it resolves to (`classifyRole`, `storageWriteRoles`), and proves the scan is not vacuous with `TestTheModuleScanCatchesTheMutations`. The label check is an extension of that fixture, not a new one: a deny-list of the two label-writing permissions, checked against every grant to a non-people member using the same resolved-role map, with new rows in the mutation test to prove the check bites.

**Open item 2's conditional is therefore answered by meeting it, not by falling back to it.** The scan passes on the modules as they are, and the pre-existing `knownComputeRefusals` exception (Cloud Batch, a documented 0.8.5 limit) does not cover any grant that carries a label permission, so the check is not vacuous through that door.

The two permissions are both on the list because either one suffices: `resourcemanager.projects.update` sets the label, and `resourcemanager.projects.setIamPolicy` is how a holder grants itself the first — Project IAM Admin does not carry `update`, and Owner and Editor carry both, which is why predefined roles are judged by a small pinned list rather than by a permission check this test cannot do offline. Global Constraint 12 stands: the scan reads the tree, never a live IAM API.

## 7. Where the label lands — a narrowing change to a shipped path, stated plainly

**Settled: stated in the task, and its review reads it that way.**

Task 3.1 has merged (`6aa3ecc`, #527), so the plan's edge "3.1 ← 3.0 if a label is wanted" (`:208`) is already violated by merge order. The label arrives as a new condition on a path that is already shipped, not before it. The label can only remove managed mode, so the behaviour change is fail-closed in every direction — but a new condition on a shipped read is still a new way for an apply to refuse and a new sentence a person reads, and §5's regression is where that is felt. The task says so rather than leaving it to be discovered.

## 8. The documents that follow

**Settled: the two the brief names are updated by this run; the plan file is left to its single writer except for the four places the reversal is recorded.**

| Document | What happens |
|---|---|
| `docs/plans/2026-10-16-managed-projects.md` | **Four places only**, all of them the reversal of point 1: Task 3.0's heading and its paragraph, the C3 row, the parallel-execution table row, and the approval-gate paragraph. The file is Task 5.4's alone (`:179`) and this run is not that pass, so the lines that only Task 5.4 may tidy — `:208`'s edge, `:254`, and the paragraph at `:557` — are **left as they are** and listed in the pull request body instead. |
| `docs/design/managed-projects.md` | §5.1 item 3, rewritten to the 2026-10-18 ruling. The section's own intro sentence ("Three rulings amend, and do not replace") is left as it is for the same single-writer reason and listed in the pull request body. |
| `docs/gcp-setup.md` | The label, and the owner-run backfill of point 5, beside the managed creation. |
| `plugin/skills/setup/SKILL.md` | `:45` says a project is managed when its runs bucket holds the creation fact, and it gains the label as the second half of what "managed" means. **The skill is at its byte budget** — 24567 of 24576 when this run reached it (`maxSkillBytes`, the lint's own rule) — so the managed rules **move to a new `reference/managed.md`** and `:45` becomes a pointer that keeps the boundary sentence in the skill ("none of those writes is yours") and names the reference. This is the skill's existing structure, not a new one: three of the five references it carries already hold exactly this kind of detail. The alternative — shaving the same ~350 bytes out of unrelated instructions — spends other people's sentences on this task's change. |

The addendum's recommendation was a separate docs task for the last two, "so no code task has to reach into documents the plan gives to someone else". That reasoning is about the *plan* file, and it is followed where it applies. `docs/gcp-setup.md` and the setup skill are not plan files and have no single-writer rule; leaving them to a later task would ship a label whose behaviour the setup document does not describe, which is the failure mode Task 5.4 exists to prevent rather than one to reproduce. Both are updated here.
