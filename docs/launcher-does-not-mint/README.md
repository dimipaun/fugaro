# A launcher does not mint a run's identity

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `7987acb99`; main moved twice during the work, and
`c16781dc8` (during review), `588750891` (before the first push) and `c767abd43` (before the
last) were merged in, with the cited symbols re-read on the merged tree each time.

Today every launcher, every operator and the resident orchestrator can sign a token for any
identity in the Firebase project, so one launcher can spend as a run another launched. This spec
takes that grant away: the credentials service issues a run's identity, as a one-time ticket the
launcher asks for and the run itself claims, and `fugaro update` moves the grant once the service
answers. It is the spec batch-run-identity's F24 and O6 said was not written.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's ruling; its ruling section is a placeholder the owner has not filled (D3120 confirms the reading). No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A6, the census (who signs, who holds a signing grant, where a launch starts, who verifies), findings F1–F34, what each option does to the trust boundary, decisions D3120–D3137 with the owner's answers, resolutions R1–R16, open items O1–O19, UNVERIFIED U1–U11. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks C1, LM0 to LM24 without LM9, and I1 (26 runs); levels, critical path, shared files; Checks 840 and 841 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |
| [contract.md](contract.md) | The identity wire contract as implemented (C1): the five routes and their classes, the bodies, the refusal codes and the bridge's three triggers, the record and its place, the attempt reference and the probe |

**Other plans read, and where.** batch-run-identity was first read on its branch at `910a545a7`
while pull request #3323 was open; it merged as `b03f9147a` (branch tip `69f76967d`, which added
D3112 and BR22) and was read again on `main`. credentials-belong-to-models (#3315) merged as
`7abc5eb2f` and is read on `main` too. always-a-budget, keys-through-service, workspace-service,
roles-scopes and the others on `main`.

**Numbers reserved:** decisions **D3120–D3139** (D3120–D3137 used), checks **840–849** (840 and
841 used). First reading, 2026-11-20 19:33–19:36 UTC, against `main` at `7987acb99` and the diff
of each of the 61 open pull requests: the highest decision on `main` was D3099 (location-preference,
D3080–D3099); the highest anywhere was D3119, reserved by the open pull request #3323
(batch-run-identity, D3100–D3119, checks 830–839); #3315 holds D3060–D3079 and checks 810–819.
The highest check on `main` was 821. No open pull request took a number in D3120–D3139 or
840–849. Second reading, 2026-11-20 21:05 UTC, immediately before the push, against `main` at
`588750891` and the diff of each of the 38 pull requests then open: #3323 and #3315 had merged,
so the highest decision on `main` was D3119's block (D3112 used) and the highest check 831; no
number in D3120–D3139 or 840–849 was in use on `main` or in any open pull request. The block did
not move between the owner's answers and the push. Third reading, 23:05 UTC, before the push that
added D3132–D3137, against `main` at `c767abd43` and every pull request then open: still no
other use of D3120–D3139 or 840–849.

**Owner answers of 2026-11-20:** D3120 to D3137, **every one as recommended**; none is marked
"not the recommendation" and none was "your call". **Two are marked amended after review, D3129
and D3130.** Each was first asked on a wrong reading of other code or another plan, review found
it, the question was asked again the same day, and the owner's last answer is the one recorded:
D3129 was asked three times (rounds 1 and 2 each corrected what of batch-run-identity depends on
its C1), D3130 twice (round 1 found the roles-and-scopes launch check). D3131 was added by round
2 and answered. D3132 to D3137 were asked after the pull request opened, from its open items and
from review round 4: the orchestrator's launch path was traced and **D3136 amends D3125** (the
issuer serves it only a recorded, unstarted attempt; D3136 was itself **amended after review**,
its first question having described the wrong threat), a fourth review round was run on the
owner's word (D3133), D3126 stands with H1 narrowed (D3134), and a roster service account stays
unscoped (D3137). **D3135's fact was settled by LM14's first step the way its row said would
send the question back to the owner — an installation that can launch after always-a-budget can
have no published policy (U5 is yes) — and the owner answered on 2026-11-26 with the row's own
alternative: accept the gap.** An installation with no published policy keeps no lever for an
older CLI, which then meets IAM's 403; `fugaro project policy publish` fixes it by hand, and
`update` publishes no minimal policy. Two
answers reach outside this folder: **D3121 amends the rule** that the
credentials service account's only grant is its database role, and **D3129 holds** everything in
batch-run-identity that touches its key or its C1 until this plan's LM19, **which puts on hold
the go the owner gave that plan's C1 and BR6**.

**Review rounds (three, the cap), each by a fresh independent reviewer given the owner's actual
answers.** Round 1: a per-repository launch check already existed (D3130 asked again); BR6
depends on batch-run-identity's C1 (D3129 asked again); the policy stage runs before the
credentials stage; the update's deletes needed listing; the record could not sit under a run's
node; the bridge's trigger was not pinned. Round 2: that C1 also publishes the key fact (D3129
asked a third time); the bump halts queued executions (D3131 asked); the module would have
turned a test red before its reader existed; the issuer could not give the CLI gate's answer
without the implicit admins; a managed project's guard warns and does not refuse. Round 3: the
gate also judges context repositories; the Firebase root's guard lives in its own file; the
probe proved no signature; the version bump sat too early. Round 4, asked for by the
owner (D3133): the orchestrator's node is written by database admins only and names no committer
(D3136 asked again); nobody judged an orchestrator's context repositories; the probe had no
route and no caller class; doctor's task sat before the grant moved; a roster service account's
unscoped ticket was an unasked decision (D3137). **Round 4's fixes are not reviewed** (O18).
