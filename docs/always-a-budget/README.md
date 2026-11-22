# Always a budget

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `fb0aeb9aa`.

Budget mode off is removed. Every run has an identity and, except an `oauth` run the credentials
service does not serve (D3026), a gateway; every installation has the
budget database, and `fugaro update` carries an installation there alone. It carries out D3009 to
D3011 of [every model key through the credentials service](../keys-through-service/README.md),
whose tasks KS14 and KS15 wait on this plan's I1.

**Amended 2026-11-22 by [model credentials routing](../model-credentials-routing/README.md)**
(D3174): the Vertex refusal under `enforce` — `VertexBudgetRefusal`, which AB1b rewords and keeps —
is **lifted for a chain the credentials service serves**. There the service, not the job, calls
Vertex on its own identity, as it serves a key, so Vertex is capped as far as a key is and no
further (that plan's F56, R23) and a job's own Vertex permission is not what serves the call; the
job's account loses that permission in release 1 in an `enforce` project, in release 2 elsewhere.
Unchanged: an unmigrated `auth: vertex` repository — one whose runs are still `agent.auth: vertex`
rather than a chain — is refused as before, and a model with no price is refused in every mode
(D3020).

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's rulings, quoting his words; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A4, findings F1–F34, decisions D3020–D3029 with the owner's answers, resolutions R1–R12, open items O1–O12 (O9 withdrawn), UNVERIFIED U1–U7. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks AB0 to AB15 (AB6 dropped, its number not reused), integration I1 (19 runs in one release, and one contract change that runs only if raised); levels, critical path, shared files; Checks 790 and 791 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3020–D3039** (D3020–D3029 used), checks **790–799** (790 and
791 used). Read on 2026-11-20: the highest decision on `main` was D2999 (reserved), with
D3000–D3019 held by the keys plan's open pull request; the highest check was 773, with 774–779
reserved and 780–789 held by the same pull request.

**Owner answers of 2026-11-20:** D3021 as recommended (Cloud Batch refuses every launch). **D3020
is not the recommendation**: a model with no price is refused in every mode, where running it
under observe was recommended. **D3022 is not the recommendation**: a repository's `fugaro.yaml`
that says off is rejected at once, where reading it as unset for one release was recommended.
D3009 to D3011 were answered on 2026-11-19 in the keys plan.

**Review round 1** added D3023 to D3025, answered the same day. **D3023 is not the
recommendation**: one release, and an installation with no budget database launches nothing
after the upgrade until a project owner runs `fugaro update`. D3024 is as recommended: a
database's own mode and caps stand. **D3025 is not the recommendation**: the policy node keeps its
version, so an older CLI can still publish off.

**Review round 2** added D3026. **It is the owner's own answer**: "The entire notion of "key
repository" or "oauth repository" is stupid and should die." This plan writes no rule in terms
of a repository's auth; retiring that setting is a spec of its own, and until it lands an `oauth`
run the credentials service does not serve has no gateway. AB6 (removing the Terraform switch)
was dropped.

**Review round 3** changed no answer. It added AB0 (the test harnesses get a budget database
before any task refuses a run without one), corrected the shared project job and the stage on an
installation that already has its database, and stated that D3020, D3022 and D3023 set the hard
rule about `fugaro update` aside by the owner's choice. Its fixes were not reviewed again.

**After round 3** the owner answered its two open items the same day. D3027, as recommended: the
budget database is written before the Firestore location's note, so a location left for a person
holds back only the run history. **D3028 is not the recommendation**: a managed project creates
its budget database under the managed gate, with no confirmation and from a coding agent's
session too, where adopting under the gate and creating outside it was recommended. **D3029**: the budget
database's location follows a location preference set at setup, under a spec of its own that is
not written yet, and AB7 waits on it.

**What a reader should not take from here:** no task is approved; each needs the owner's go by name.
No release, a hotfix included, is cut from `main` between the merge of AB7 and I1. Checks 790 and 791 are owner-run after the release.
