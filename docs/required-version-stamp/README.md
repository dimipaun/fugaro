# Required-version stamp

**Status: plan only; nothing is implemented; no task approved.** Every decision is answered, and the owner confirmed D3267 as amended after review.

**Spec owner:** Dimi Paun. The spec was approved on 2026-11-23. It was reconciled and planned on
2026-11-23, against `origin/main` at `0dec0bb36`.

This is lift L1 of the [repo-coupling audit](../repo-coupling-audit/README.md) (report §6 and §7,
D3233, D3238). `fugaro update` records the oldest CLI release that may act on the project, in
`config/policy/required_cli` beside `config/policy/shared`. A CLI below it refuses project commands
with one sentence that names `brew upgrade` and `fugaro update`. L8 waits on it.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as approved. No typo was fixed |
| [reconciliation.md](reconciliation.md) | Assumptions A1–A6, findings F1–F25 (F20–F25 from the review rounds), decisions D3260–D3271 (D3260–D3266 are the owner's answers to Q1–Q7; D3267–D3270 were added by this reconciliation and its reviews and answered the same day, and D3267 was amended after review and confirmed; D3271 closes O7), resolutions R1–R10, open items O1–O8 (O1, O2, O4 and O7 closed), UNVERIFIED U1–U4. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Twelve tasks in four levels: T1 (the stamp package) and T2c (the published-release lookup); T2a, T3, T4, T5, T6 and K1; T2b and K2; T8 (end to end) and T7. It has the critical path, the shared files, and Check 900. No contract: the pass breaks no edge (R10). Nothing in it is approved |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it |

**The owner's answers (2026-11-23):**
- D3260: `update` writes its own release.
- D3261, **not the recommendation:** CLIs released before L1 stay unfenced. Nothing here bumps
  `projectpolicy.Version`, and L8's own spec decides its release timing.
- D3262: the stamp is never lowered.
- D3263: a cached stamp is trusted for 15 minutes.
- D3264: read failures fail open, except a cached value above the binary and a malformed stamp.
- D3265: console read paths are exempt and print one stderr line.
- D3266: an older `update` refuses before its first write.
- D3267: in a coding agent's session on a managed project, `update` raises the stamp under the
  managed gate, like the `rules` stage (as recommended). **Amended after review (F20, F21) and
  confirmed by the owner, "keep it, raise when converged":**
  - The stage raises only through its own `gateStep` and audit record.
  - It holds while `credentialsMoveHeld` holds the policy node, or while `servicesImageStale` says
    the credentials service is behind.
  - In a session, `credentialsMoveHeld` holds on every installation with a credentials block. So in
    practice the owner's own `update` raises the stamp, and an agent session raises it only when
    nothing is held.
- D3268: "Bound it". `update` writes only a published release, and a cached value above the binary
  refuses on its own for only 15 minutes. Nothing lowers the stamp (D3262); the last-resort recovery
  is a hand edit of the node in the Firebase console.
- D3269: the release bound is a live check that the tag `v<Version>` is a published release (not a
  draft or pre-release), whatever the update-check switches say. If the lookup fails, the stamp
  stays and a line says so.
- D3270: `fugaro budget kill`, in every form, `--workspace` included, is exempt from the fence.
- D3271: `update`'s door read keeps the 2-second timeout every command has; Check 900 measures a
  cold read (U1), and a follow-up raises the timeout only if the read does not fit (closes O7).

**Findings that changed the design:**
- The stage cannot sit right after `policy`, because that slot is taken by contract. It goes after
  `credentials-copy` (F3, R1).
- `needs` does not gate a stage; gating is linear (F2).
- The root hook ignores the command's own `--project`, and a member on the shared config would go
  unfenced (F5, F6, R3, R4).
- The console-read exemption would have exempted bare `fugaro apply` (F7, R5).
- A root refusal would crash-loop the login unit's daemon (F9, R7).
- The managed gate already lets an agent's session write some stages, so whether the stamp is one of
  them was a decision: D3267, answered gated (F12).
- An earlier stage can skip with its work undone and the run goes on, so the stamp holds until the
  installation has really converged (F20). A gated write needs its own `gateStep` (F21) and the
  adoption check (F22).
- A stamp that only rises makes a bad value permanent, so D3268 and D3269 bound the ways one gets in
  (F23).
- `update`'s `--forget` and `--config-only` bypass the converge, so `update` refuses at the top of its
  `RunE` (F24, R8).
- Stopping spend must never be fenced (F25, D3270).

**Numbers reserved:**
- Decisions **D3260–D3289**: D3260–D3271 used, D3272–D3289 reserved and unused.
- Checks **900–909**: Check 900 used, 901–909 reserved and unused.

Both blocks were checked free on `main` at `0dec0bb36`. The highest decision there was the
repo-coupling audit's block (D3230–D3259), and the highest check was 892.

**What waits on something external:**
- the owner's go for each task;
- Check 900, which the owner runs after the release that carries L1;
- the workspace work's own spec, for which stamp a workspace write checks (O5);
- the console's owner, for U3 (O6).

L8's migration placed after this stage (O1) was confirmed by the owner on 2026-11-23.
