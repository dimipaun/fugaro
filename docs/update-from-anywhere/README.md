# Update from anywhere (lift L5)

**Status: plan only; nothing is implemented; no task approved.** Every decision is answered.

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
| [reconciliation.md](reconciliation.md) | Assumptions A1–A5, findings F1–F30 (F15–F30 from review round 1), decisions D3340–D3351 with the owner's answers (D3343 and D3347 amended after review and confirmed), resolutions R1–R17, open items O1–O2, UNVERIFIED V1–V3. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, sixteen tasks (U1–U5, U6a, U6b, U7a, U7b, U8, U9a, U9b, U9c, U10, U11, U13), integration I1, the owner's Check 930 (K1) and one contract change that runs only if raised, in five levels. Also the critical path, the shared files and the edges broken and kept. Nothing in it is approved |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it |

**The owner's answers, all as recommended:**
- **D3340:** a leftover plugin pin and a `budget.mode: off` line become notices in `update` and
  `doctor`. The HARD RULE's pin sentence and the `update` skill change in the same pull request.
- **D3341:** everything but the repository stage ships first. That stage, with the RC39 facts
  (D3346), waits on L2.
- **D3342:** every stale derived image is refreshed from its default branch, behind today's
  confirmation.
- **D3343:** the shell's token reads the default branches. **Amended after review**, confirmed
  2026-11-24: with no token variable set, it falls back to `gh auth token` and the git credential
  helper.
- **D3344:** the backlog fold runs from every default branch for one release, then goes.
- **D3345:** `update --anchor` moves into `fugaro project repos add`.
- **D3346:** the RC39 facts are written for every repository. Moving them to installation state is
  a follow-up.
- **D3347:** a value is asked for only under `--ask`. Otherwise a line names the repository and the
  fix. **Amended after review**, confirmed 2026-11-24: `--ask` also turns on value entry, on every
  project (today it restores only a managed project's confirmations).
- **D3348:** the hint never narrows `--plan-only`.
- **D3349 (2026-11-24):** `update --check` drops PATH and reads the local config's repositories from
  the code host, with no GCP call.
- **D3350 (2026-11-24):** a published admit counts as enrolled for secrets, builds and the fold.
  The hostile-checkout gate stays for `setup --repo`.
- **D3351 (2026-11-24):** `setup --anchor` goes too.

**Findings that changed the design:**
- L8's task M1 (`repofiles.Read`, in flight as #3816) is already the reader of every listed
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
- Decisions **D3340–D3369**: D3340–D3351 used, D3352–D3369 reserved and unused.
- Checks **930–939**: Check 930 used, 931–939 reserved and unused.

Both blocks were checked free on `main` at `486823a00` and in every open pull request's documents.
The highest decision was image-secrets' reserved block (D3320–D3339), and the highest check was its
929.

**What waits on something external:**
- the owner's go for each task;
- L8 M1 (#3816) for U1 and U9a;
- L2 (shared-jobs) for U11;
- Check 930, which the owner runs after the release that carries I1.
