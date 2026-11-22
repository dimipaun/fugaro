# The pause contract

This is the document of **C3**: what a suspended run leaves behind, what the credentials service
records about it, and who may read or change either. Its shapes are declared once, in
`internal/runstore/pause.go`, because four sides already import that package and none of them
imports the others: the job that suspends itself, the credentials service that records the pause,
the resident orchestrator that resumes it, and the views that show it. It is written against
[reconciliation.md](reconciliation.md) R6, R8, R10 to R13, R18, R19, R24 and R26 (which wins over
[spec.md](spec.md) where the two disagree) and against D3184 to D3191, D3195 and D3197.

The pause is the answer to an exhausted chain that is not worth waiting out inside the job
(D3184). A job that meets it stops: it saves what a later run needs, says so, ends `paused` and
releases its lease, and a fresh run continues it later. Nothing is held open — no proxy call, no
container — which is the property the whole mechanism exists for (D138's rule, F28, R12).

## The shapes, and who writes each

| Shape | Where | Written by | Read by |
| --- | --- | --- | --- |
| `pause.json` (`runstore.PauseRecord`) | the run's own bucket prefix | **the suspended run, inside the job** | the run that resumes it, and nobody else |
| the paused node (`runstore.PauseNode`) | `paused/<slug>/<run>` in the Realtime Database (`runstore.PauseNodePath`) | **the credentials service alone** (R19, D3189) | the service, the orchestrator, and every launcher's view |
| `/pause` (`runstore.PausePath`) | a request to the service | the suspended run, once it has saved | — |
| `action`, `resume_at` (`runstore.PauseMemberAction`, `PauseMemberResumeAt`) | the service's exhausted answer | the credentials service | the gateway and the runner (PR2) |
| `resumes` (`runstore.PauseMemberResumes`) | the resuming run's task | the resident orchestrator (PR4) | the resuming run (PR5) |
| `resume_at`, `resumed_by` (`PauseMemberResumeAt`, `PauseMemberResumedBy`) | a run's row in `fugaro watch --json` and `runs ls --json` | the views (PR7) | people, the console and the mobile feed |

The record and the node are two halves of one pause and neither is derivable from the other. The
record is the run's own account of where it stopped, written inside the job and trusted exactly as
far as the run record a follow-up already trusts (R23, R26). The node is the service's account of
the grant — who launched the run, on what pool, until when, in what chain — and a job may not write
a word of it.

### `pause.json`

`runstore.PauseVersion` is the format version, `version: 1` today, and the record carries four
members:

| Member | What it is |
| --- | --- |
| `version` | the format version this record was written by |
| `stage` | the stage that was running: the resumed run enters **that** stage, not the first one |
| `session` | the name of the saved Claude Code session, which the resumed run restores (F34) |
| `checkpoint` | the commit the suspended run pushed before it exited, which the resumed run checks out: a full commit id and nothing else (`gitops.ValidCommitSHA`) |

The session is saved and the work in progress is pushed as a checkpoint **before** the record is
written (R18, F34): uncommitted work is lost unless it is pushed first
([checkpoint pushes](../checkpoint-pushes/)), so a resume that finds its checkout at another commit
refuses the record rather than starting from what it finds.

A reader that does not know the version it finds **ends with a named outcome and never guesses**
(R18). The name is `runstore.ErrInvalidPause`, and the outcome is the resumed run's own: it ends,
saying which version it could not read, and starts no stage afresh. The same direction covers a
record whose shape this release does not know at all — both the record and the node decode
strictly, so a member a release cannot place is refused rather than dropped (R25's rule for the
policy, applied to the pause) — and a record whose members a reader acts on are not what the reader
needs: no stage, no session, a session name that is not one, or a checkpoint that is not a full
commit id (`runstore.PauseRecord`'s rule, asked by the writer and the reader alike). The
checkpoint is the one member of the record the job's own agent chooses that reaches a git call:
the resumed run checks that commit out, so a ref name, `HEAD` or an option-shaped value never
passes the record, however it got there.

`pause.json` is written for the run that resumes it **and for nobody else**: it is not a document
of the installation's state, no view reads it, and the console and the mobile feed draw a pause
from the node instead.

### The paused node

`paused/<slug>/<run>` — the slug and the run id escaped for the database exactly as
`internal/budget` escapes every other path segment (its `Key`, restated in `runstore` because
`internal/budget` imports that package and it cannot call back).

| Member | What it is |
| --- | --- |
| `launcher` | the verified principal the run's **slip** named (R7, R8): a person's Google subject, or a verified service account |
| `pool` | the pool the run's launch named (`fugaro run --pool`), empty when the launch named none |
| `resume_at` | the instant the run will be resumed at, in epoch milliseconds UTC |
| `chain` | the id of the chain the run belongs to: `runstore.Chain`'s root (D1045) |
| `paused_seconds` | the chain's paused time so far, this pause included, in seconds |
| `cancelled` | the mark that stops the pause ever being resumed |

**The launcher and the resume time are the service's own record and never the job's** (R8, D3189).
The service resolves the launcher from the verified owner of the run's slip and the pool from its
own record of the grant, and it computes the resume time from its own knowledge of the reset — not
from anything the run sent, not from `pause.json`, and not from the run's own launch record, which
is display text. This is the whole of the reason the node exists: a run that resumes a paused one
is launched by the orchestrator's service account but must draw on **the original launcher's
pool** (D3191), and a suspended job's own word about who launched it would let a hostile agent
name another person and spend their tokens (F37, D2976 as amended by D3191).

`resume_at` is the one instant the pause has, and it is named the same thing everywhere it appears
— in the answer, on the node and in a run's row — in each format's own unit: epoch milliseconds UTC
in the answer (as `reset_at` is) and on the node (as the database's instants are), and RFC 3339 UTC
in a run's row, which spells every instant that way (`waiting_until`). The job may read it and may
log it; it never chooses it, and `pause.json` does not carry it.

The node is **readable by every launcher** of the project and is **never an authorization input to
anything but the service and the orchestrator** (R26, F61): to a view it is display data, and the
fact that a launcher can read another run's node is not a permission to resume it, to cancel it or
to draw on its pool. It is not a secret, and nothing but a pause lives in it.

The node carries no "resumed" mark. Whether a pause has already been resumed is the service's
record — a resume is granted once (below) — and a reader that wants the run that resumed a pause
finds it in the chain: the run whose task names `resumes` with the paused run's id.

### The action the answer carries

When every source a chain can reach is exhausted, the service's refusal body gains two members,
and it decides which action the run is to take (R19 — the job follows the action, and the one rule
the runner owns is R10, a hotfix run treating `wait` and `pause` as `halt`):

| `action` | What the run does |
| --- | --- |
| `wait` | the in-job wait, as the runner has always done it: the reset is nearer than `pause.wait_in_job` (D3184) |
| `pause` | suspends itself: saves, pushes, writes the record, posts `/pause`, ends `paused`, releases its lease |
| `halt` | ends at once with `HaltNoCredential`, the reset time in the reason |

Which of the three it is, is the service's decision and is taken from its own knowledge (PR3): the
reset against `pause.wait_in_job`, `pause.min_pause` and `pause.max_pause` (R11, D3184, D3187), and
the chain's own history — a chain whose total paused time would pass `pause.max_pause` halts
instead of pausing again (D3190), and an installation with no resident orchestrator halts, because
nothing would ever resume what it recorded (D3186). The pause it names is never shorter than
`pause.min_pause`.

`resume_at` rides with `action: pause`; a `wait` carries the reset it waits for (`reset_at`) and a
`halt` the reset it refused to wait for, and neither carries a resume time, because neither is a
pause.

**These members are sent only to a caller that sent the stage header** (R24, F57). The member is
added to the answer for a caller that declared its stage and is absent from the answer to one that
did not, so a gateway of a release behind — which decodes the refusal strictly — meets exactly
today's answer and never a member it would refuse. The header itself belongs to the chain's
contract (`internal/credsvc`'s stage header, C1) and not to this file.

### `/pause`

Once a run has saved, it tells the service so with `runstore.PausePath` (`/pause`). The request
carries no member of its own: the run is the identity the service already verified, and every
question of who launched it, on what pool, and until when is answered from the service's own
record, never from the request.

**The service honours it only for a run it last answered `pause`, and once per answer.** A run
whose last answer was `wait` or `halt`, or whose pause it has already recorded, is refused. A
refusal means no node is written and nothing will resume that run — so a run must never end
`paused` on a refused `/pause`: it ends the way a run whose credential ran out ends, naming the
refusal (PR2, PR3). A pause nothing would resume is the one failure this part of the contract
exists to prevent.

The order is fixed: save the session, push the checkpoint, write `pause.json`, ask, and only then
end `paused` and release the lease. A run that dies between the record and the ask leaves a
`pause.json` nothing will read, which costs a resumed run nothing: the record is read by a run that
is launched, and one that is never launched reads nothing.

## Resuming

A pause is resumed by the resident orchestrator (D3186, R13), which on each tick reads the nodes
under `paused/` whose resume time has passed, is not cancelled, and whose run really ended with the
status `paused`, and reissues each one **once** as a run that continues the paused chain (D3185):
the same chain, with the task member `resumes` naming the paused run.

* **Who may ask for the resume.** A slip that names `resumes` is granted only to the
  orchestrator's class, only for a run of the **same repository and the same chain** as the node it
  names, and only for a node the credentials service itself wrote (D3189 — the one case this plan
  adds to launcher-does-not-mint's D3136, which otherwise serves the orchestrator an attempt on its
  own record alone).
* **Once.** The grant is single-use: a pause is resumed at most once. A second ask for the same
  node is refused, which is what makes two ticks of the same instant resume nothing twice.
* **Not before the resume time.** An ask before `resume_at` is refused. The service computed that
  instant; a caller cannot move it.
* **Never a cancelled one.** An ask for a node carrying `cancelled` is refused.
* **Never a run that did not end `paused`.** A `/pause` posted by something that is not a
  suspended run's own job writes no node, and a node whose run has not ended `paused` is not
  reissued.

The resumed run draws on the **original launcher's pool**, from the node (D3191); it is launched by
the orchestrator's service account and is still, for the purpose of whose tokens it spends, the
person's run.

## Cancelling

Only the pause's **launcher** cancels it (D3197): the service has no admin surface (D143), so it
cannot tell a project admin from anyone else, and an admin does not cancel another person's pause
in this plan. The cancellation is a request to a route of the service that verifies the caller's
principal against the node's own `launcher` and marks the node `cancelled`. It is the launcher's
own act and nobody else's, and a cancelled pause is never resumed.

## The status word

A run that suspends itself ends with `runstore.StatusPaused` — the word `paused`, a fifth status
and not a reuse of one (R15, D3185): `running` and `halted` are not reused, because a paused run is
in flight and will be resumed, where a halted one has stopped. It is terminal for the execution —
the container stops and the lease is released (R12) — and it is a word every reader of a status
must learn at once, which is PR1's work: the result schema, the budget rules' outcome words and the
console's set gain it together, so no reader draws a paused run as a failure or as a success.

A pull request whose chain is paused is in a fifth state of its own, `paused` (D3195): the chain is
still in flight to every guard that asks — a duplicate launch and a follow-up are refused while the
pause stands — and the pull request needs nothing from a person, is not landed and takes no
follow-up while it stands. That state is the views' and the guards' work (PR12, PR13) and is not a
status of a run.

## The stubs (C3)

C3 ships the contract and no behaviour. Two seams stand where the implementation will be, and each
decides nothing:

* `internal/runner/suspend_stub.go` — the seam a run suspends through, whose body is today's
  answer to an exhausted credential: it **waits in the job**. That is exactly the behaviour the
  pause replaces, and PR2 replaces the body (its own file sorts before the stub's, and the stub's
  guarded assignment stands only while nothing else has made one).
* `internal/orchestrator/resume_stub.go` — the seam the tick resumes through, which answers that
  nothing is due and **resumes nothing**. PR4 fills it, and I2 deletes both stubs.
