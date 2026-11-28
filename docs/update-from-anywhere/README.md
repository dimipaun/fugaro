# Update from anywhere (lift L5)

**Status as of 2026-11-27: most of the plan is merged; U9c, U9d, U12, I1 and K1 are left, and U11 is
cancelled — U12 no longer waits on it (D3420, [docs/ground-rules](../ground-rules/reconciliation.md)).
See [docs/status.md](../status.md) for the current table.** Every decision is answered.

**Spec owner:** Dimi Paun. The owner approved the spec on 2026-11-23. It was reconciled and planned
on 2026-11-24, against `origin/main` at `486823a00`.

This is lift L5 of the [repo-coupling audit](../repo-coupling-audit/README.md) (report §7, D3238), as
the owner's D3239 rescopes it. `fugaro update` does the same work from any directory:
- every stage reads every enrolled repository from its default branch;
- the working directory is only a hint for the project and the order of human output;
- a repository it cannot read, or a value only the owner can supply, gets one line naming it.

It covers audit rows RC12, RC13, RC38, RC39, RC75, RC98, RC104 and RC105.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as approved. No typo was fixed |
| [reconciliation.md](reconciliation.md) | Assumptions A1–A5, findings F1–F50 (F15–F30 from review round 1, F31–F41 from round 2, F42–F50 from round 3), decisions D3340–D3352 with the owner's answers (D3343, D3347 and D3349 amended after review and confirmed), resolutions R1–R22, open items O1–O3 (O3 closed), UNVERIFIED V1–V3. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, the tasks (U1, U2, U3a, U3b, U4, U5, U6a, U6b, U7a, U7b, U8, U9a–U9d, U10, U13, with U11 cancelled and U12 added by D3353), integration I1, the owner's Check 930 (K1) and one contract change that runs only if raised, in eight levels. Also the critical path, the shared files and the edges broken and kept. **U11 is cancelled and U12 no longer waits on it** (D3420 amends D3353) |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it |

**The owner's answers, all as recommended:**
- **D3340:** a leftover plugin pin and a `budget.mode: off` line become notices in `update` and
  `doctor`. The HARD RULE's pin sentence and the `update` skill change in the same pull request.
- **D3341:** everything but the repository stage ships first. That stage, with the RC39 facts
  (D3346), waits on L2.
- **D3342:** every stale derived image is refreshed from its default branch, behind today's
  confirmation.
- **D3343:** the shell's token reads the default branches. **Amended twice after review**, the
  second confirmed 2026-11-24: GitHub has no token path (survey reads the App key), so every read
  goes through the credentials service's person read mint (`serviceReadProvider`). No key or token
  reaches the admin's machine. The first amendment's `gh`/credential-helper fallback is withdrawn.
- **D3344:** the backlog fold runs from every default branch for one release, then goes.
- **D3345:** `update --anchor` moves into `fugaro project repos add`.
- **D3346:** the RC39 facts are written for every repository. Moving them to installation state is
  a follow-up.
- **D3347:** a value is asked for only under `--ask`. Otherwise a line names the repository and the
  fix. **Amended after review**, confirmed 2026-11-24: `--ask` also turns on value entry, on every
  project, in `fugaro update` only. `setup` keeps its terminal prompt.
- **D3348:** the hint never narrows `--plan-only`.
- **D3349 (2026-11-24):** `update --check` drops PATH and reads the local config's repositories.
  **Amended after review**, confirmed 2026-11-24: it reads through the read mint and writes nothing,
  instead of "no cloud call".
- **D3350 (2026-11-24):** a published admit counts as enrolled for secrets, builds and the fold.
  The hostile-checkout gate stays for `setup --repo` and
  `--onboard-repo`.
- **D3351 (2026-11-24):** `setup --anchor` goes too.
- **D3352 (2026-11-24):** in a coding agent's session no identity is minted, as D902: every
  repository reads as unread, with a line that says to run `fugaro update` from your own terminal.

**Findings that changed the design:**
- GitHub has no token path for a person's read; the credentials service's read mint is the way in
  (F31). An admit-only GitHub repository borrows the project's App ID (F43), and `repos add` keeps
  its checkout, which it deploys from (F42).
- L8's task M1 (`repofiles.Read`, merged as #3816) is already the reader of every listed
  repository, so L5 extends it and writes no reader of its own (F1).
- `--check`'s no-cloud promise cannot cover the published admits (F2, D3349).
- Image-secrets makes workflow secrets project-wide, so a name several repositories want is one line
  (F3).
- Until L2, Check 891 passes with only the repository stage differing (F4, R3).
- Doctor already has `plugin-pin` and `budget-mode` checks, which now read the default branch
  (F21). `--anchor` is shared with `setup` and pointed at from many places (F20). Stages report
  through `initflow`, so the output members need their own task (F15).
- A derived build clones its source in Cloud Build, so refreshing from a default branch needs no
  checkout (F7).

**Numbers reserved:**
- Decisions **D3340–D3369**: D3340–D3352 used, D3353–D3369 reserved and unused.
- Checks **930–939**: Check 930 used, 931–939 reserved and unused.

Both blocks were checked free on `main` at `486823a00` and in every open pull request's documents.
The highest decision was image-secrets' reserved block (D3320–D3339), and the highest check was its
929.

**What waits on something external:**
- the owner's go for each task;
- Check 930, which the owner runs after the release that carries I1.
