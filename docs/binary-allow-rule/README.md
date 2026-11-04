# The project's binary allow rule, and a status label for a run no review answered

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** The two changes below are settled down to the shape of a node member, the moment a run
is refused, the stage that publishes it and the vocabulary of one wire enum, but this folder writes
no code: it is the input the tasks are written from, and the answers of 2026-11-04 are recorded in
[reconciliation.md](reconciliation.md) §2. The slot is in
[implementation-plan.md](implementation-plan.md) §"The recommended slot" — **0.20.0**, because
0.15.0, 0.16.0 and 0.17.0 have shipped (the newest notes in `docs/releases/`), 0.18.0 and 0.19.0 are
spoken for, and this design is two works, one of them a policy-node version bump —
and the one-line answer is that **the status label lands first** (it is independent of everything
else and closes a lie today) and **the allow rule's runner half follows it**, with its publish
riding the **managed** policy-publish gate
([docs/managed-agent-converge](../managed-agent-converge/spec.md), D1012/M3) that main now carries.

**Asked by:** the owner's rulings of 2026-10-31–2026-11-01 on the 0.14.2 runner group (PR #1476):
the allow rule is **project-level only, tighten-only, in the policy node** — never a repository key
the repository's own agent could write — and the run that no review answered gets **a new status
label, not a re-aimed old one** (a name never changes meaning). **Answered by:** the owner's
ruling of **2026-11-04** on this folder's own decisions — D983–D989, D991 and D997, recorded with
the date and the one-line why in [reconciliation.md](reconciliation.md) §2.

**The two items this design answers, both deferred from #1476:**

1. **finalize refuses every untracked binary, and that includes the run's own legitimate image.**
   `internal/runner/stray.go:48`'s `strayArtifacts` reads what a commit would carry and no commit
   has (`gitops.NewPaths`), refuses any file whose first 8000 bytes hold a NUL, holds it out of
   finalize's leftover commit (`gitops.CommitAllExcept`, `runner.go:3738`) and ends the run as a
   failed draft. **It is `main`'s code** (the 0.14.2 runner group, #1476, is merged). That is right
   about a compiler's output and wrong about a new asset: this repository **tracks 44 images and
   fonts** (32 `.png`, 7 `.woff2`, 4 `.webp`, 1 `.ico`, the largest `brand/fugaro-logo.png` at
   855 285 bytes), so adding one is ordinary work here and a run that adds one ends a draft for
   doing its job.
2. **a run whose review returned no parseable verdict ends `failed`, although nothing failed.** The
   tests passed, the agent worked, the branch was pushed and the pull request exists: what is
   missing is a *verdict*. `internal/runner/outcome.go:46`'s `Decide` refuses readiness for it —
   correctly, a run nobody read is not ready — but finalize's status switch
   (`runner.go:3936`–`3942`) then lands it in `default` and the record's `status` says `failed`, so
   a person reading `fugaro runs ls`, the console or the history is told a thing that is not true.

**The one question that was left open is answered: D997 is a *yes* (2026-11-04).** A binary the
coder puts in a commit of its own is refused, anywhere in the branch diff — the check that reads the
working tree at finalize does not see it, and closing that gap is the behaviour change the owner
took. The options and the cost of each are in [reconciliation.md](reconciliation.md) §2, and the
implementation plan's **AR4 is written** because of it.

**The gateway enforces the cost cap** on the runs that build this design's tasks. Nothing else in
this folder is about cost.

## What this design adds

1. **A binary allow rule in the project's policy node.** A project states which paths a run may add
   a binary at, and how large one may be: `binaries.allow` (doublestar path globs, the repository's
   one pattern language) and `binaries.max_bytes` (one cap, the largest a run may add at an admitted
   path). A path no entry admits is refused exactly as it is today; a path one admits is committed
   as ordinary work and named in the pull request's status section. It is the owner's word alone:
   the key exists in the policy node and in no repository file, because `fugaro.yaml` is inside the
   checkout the run's own agent writes to, and a key there would let a prompt-injected agent admit
   its own payload.
2. **A status label for the run that got no verdict.** `runstore.StatusUnreviewed` — the run reached
   finalize, pushed, opened its pull request and is a draft, and no review verdict exists. Nothing
   failed; nobody answered.

Both are small. The reason this folder is four documents is that the first one has a **residual** —
a binary the agent commits itself, or one swept into a commit by `gate.go`'s autofix, by
`digest.go`'s step or by the challenger arm's own `shadowFinalize` commit, never passes through the
check finalize runs — and the second changes a word on a wire the console, the phone,
`fugaro watch`, the digest and the user census all read. Two limits are named rather than papered
over: a committed payload is refused *after* it is on the branch (history is not rewritten), and a
path the agent adds and then deletes in a later commit is not refused at all, because the check
reads the two ends of the run's diff. Both are in spec.md §4.2 and §9's residual table.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The rule's shape (paths, size, and the three things it deliberately is not), who may write it, where finalize checks it, what the agent is told, how `fugaro update` publishes it, the residual and what to do about it, the status label and its wire, and the threat model. The design in full; the tasks in [implementation-plan.md](implementation-plan.md) are written to it |
| [reconciliation.md](reconciliation.md) | Findings F1–F14 against the code on `main`, the owner decisions **D981–D997** — **D983–D989, D991 and D997 answered by the owner 2026-11-04**, the rest recommendations — each with the recommendation and the cost of the alternative, the open items O1–O3, and what this design does not decide. **D997 was the one left open on purpose** (whether a binary the coder commits itself is refused) and the answer is yes. **Where it disagrees with [spec.md](spec.md), it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks NV0–NV2 (the label) and AR0–AR7 (the rule), the external dependencies E1–E3, the files the sibling designs share, the order, and the recommended slot |
| [README.md](README.md) | This file: what is being asked, what it adds, and where it sits in the release |

## Where the numbers come from

The decision block was taken on 2026-11-01 **above every `D<number>` on `main` and in every open
pull request** at that time: `main` at `3d6ed04d` carried up to **D901**, and the highest of the
then-open pull requests was **D980**. This work took the next thirty-wide block, **D981–D1010**.

**Re-read on 2026-11-04, and the numbers are fixed here:** `main` now carries **D1000–D1189**, and
open work holds **D1190–D1219** (#1676), **D1220–D1259** (the stateless-orchestrator design),
**D1260–D1289** (#1678) and **D1290–D1309** (#1492). **This design keeps only D981–D997** — taken
first, above the then-highest number, and untouched by every block that came after — and **gives up
the reserved half, D998–D1010**, which would have collided with main's managed block (D1012 and its
neighbours). A reserved block that is unused is worth nothing; an overlapping one is worth less.
Nothing else in the tree uses D981–D997.

Every `D` this folder writes is a *proposal with a recommendation*. **Nine of them were answered by
the owner on 2026-11-04** (D983–D989, D991, D997) and each carries the date and the one-line why in
[reconciliation.md](reconciliation.md) §2; the rest stand as recommendations, and the plan is
written as recommended so that an answer of "as recommended" needs no rewrite.

## What this design is not

- **Not a content rule.** It admits a *path*, up to a size. It does not sniff a magic number, it does
  not read a MIME type, and it does not try to decide whether the bytes at an admitted path are
  benign — the reviewer's marker line (`internal/runner/reviewinput.go`'s `binary file %s changed,
  %d bytes`) is the whole of what a person sees, and [spec.md](spec.md) §9 says why that is the
  honest boundary rather than an oversight.
- **Not a change to *when* a run is ready.** A run that adds an admitted binary still has to pass
  its tests and be read by its review; the rule governs what may be *committed*, never what may be
  *shipped*.
- **Not a licence, key or payment change**, and it writes no notice of any kind.
