# A launcher does not mint a run's identity

Owner: Dimi Paun. Date: 2026-11-20. Written by the coordinator from the
owner's ruling; the owner's words are quoted where they are his.

### The ruling

<OWNER: your words here. Until then the coordinator's reading stands, and the
first question the agent asks is whether it is right.>

The batch-run-identity plan found (its F24) that every launcher and operator
can sign a token for any run's identity, so a second launcher can spend as a
run it did not launch. That plan recorded "minting stays with launchers" and no
spec (its O6). This spec is that one.

### What is asked

1. A launcher's own credentials cannot produce a token that is accepted as a
   run's identity. The grant that lets a person sign as the runs' signer is
   taken off launchers and operators.
2. A run still gets its identity at launch, from something that is not the
   launcher's machine, and that checks who is launching, for which repository
   and which run, before it issues anything.
3. One person cannot obtain, replay or re-issue the identity of a run another
   person launched.
4. Every way a run is launched today keeps working: `fugaro run`, a follow-up,
   `fugaro review`, a shared project job, the orchestrator, the coordinator,
   and a launch from the console or the mobile app.
5. `fugaro update` is the whole upgrade: it moves the grant and deploys
   whatever now issues the identity, with no other command, deletion or manual
   step, and it is safe with runs in flight.
6. Nothing that works today stops without the update saying so first, with
   the fix.

### What the brief knows it does not know

- What issues the identity instead: the credentials service, a service of its
  own, the workspace service, or the job's platform.
- How that issuer knows the caller may launch that repository, and what it
  trusts: the launcher roster, IAM, the published policy.
- Whether the same signer also signs things that are not a run's identity
  (pairing tokens, device tokens, the mobile account's) and what happens to
  each.
- What a launcher with an older CLI does after the upgrade, and for how many
  releases the old grant stays.
- What happens when the issuer is unreachable: every launch fails, or
  something degrades.
- Which launches are made by a service account and not a person, and whether
  they keep the grant.
- Whether Cloud Batch's design (batch-run-identity) gets simpler or changes
  once this lands.
- Whether this ships before, with or after the plans it touches.

### Out of scope

- The claims a run's identity carries and what the budget database's rules do
  with them, except where the issuer must check a claim.
- Model credentials (the keys plan and credentials-belong-to-models).
- Per-person tokens at the workspace (waits on the threat model).
