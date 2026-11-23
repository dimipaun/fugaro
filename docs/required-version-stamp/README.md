# Required-version stamp

**Status: plan only; nothing is implemented; no task approved.** One decision is pending: D3267.

**Spec owner:** Dimi Paun. The spec was approved on 2026-11-23. It was reconciled and planned on
2026-11-23, against `origin/main` at `0dec0bb36`.

This is lift L1 of the [repo-coupling audit](../repo-coupling-audit/README.md) (report §6 and §7,
D3233, D3238). `fugaro update` records the oldest CLI release that may act on the project, in
`config/policy/required_cli` beside `config/policy/shared`. A CLI below it refuses project commands
with one sentence that names `brew upgrade` and `fugaro update`. L8 waits on it.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as approved. No typo was fixed |
| [reconciliation.md](reconciliation.md) | Assumptions A1–A6, findings F1–F19, decisions D3260–D3267 (D3260–D3266 are the owner's answers to Q1–Q7; D3267 is pending), resolutions R1–R10, open items O1–O3, UNVERIFIED U1–U4. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Twelve tasks in four levels: contract C1; T1, T2a, T3, T4, T5, T6 and K1; T2b, K2 and I1; then T7. It has the critical path, the shared files, and Check 900. Nothing in it is approved |
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

**Pending:** D3267. In a coding agent's session on a managed project, does `update` raise the stamp
under the managed gate? The recommendation is yes, like the `rules` stage. T2a and T2b depend on it.

**Findings that changed the design:**
- The stage cannot sit right after `policy`, because that slot is taken by contract. It goes after
  `credentials-copy` (F3, R1).
- `needs` does not gate a stage; gating is linear (F2).
- The root hook ignores the command's own `--project`, and a member on the shared config would go
  unfenced (F5, F6, R3, R4).
- The console-read exemption would have exempted bare `fugaro apply` (F7, R5).
- A root refusal would crash-loop the login unit's daemon (F9, R7).
- The managed gate already lets an agent's session write some stages, so whether the stamp is one of
  them is a decision: D3267 (F12).

**Numbers reserved:**
- Decisions **D3260–D3289**: D3260–D3267 used, D3268–D3289 reserved and unused.
- Checks **900–909**: Check 900 used, 901–909 reserved and unused.

Both blocks were checked free on `main` at `0dec0bb36`. The highest decision there was the
repo-coupling audit's block (D3230–D3259), and the highest check was 892.

**What waits on something external:**
- the owner's answer to D3267;
- the owner's go for each task;
- Check 900, which the owner runs after the release that carries L1;
- L8's spec, which places its migration after this stage (O1).
