# A run on Cloud Batch gets its identity

Owner: Dimi Paun. Date: 2026-11-20. Written by the coordinator from the
owner's rulings.

### Why

The owner ruled that every run has a budget and an identity ("We should
always have a budget", D3009), and that until Cloud Batch can carry a run's
token, every Batch launch is refused with one stated reason, with "a carrier
for the token" left as a design of its own (D3021 of docs/always-a-budget).
This is that design. Without it the Cloud Batch backend can launch nothing.

### The problem, as known today

A launch hands the run a sealed token, and the key that opens it travels on
the execution, not on the job. On Cloud Run that is a per-execution override.
Cloud Batch has no per-execution place for it: the key would sit as a
variable on the job, where other launchers and the reaper can read it. So
the Batch backend refuses any sealed launch.

### What is asked

1. A run launched on Cloud Batch receives its token so that only that run
   can open it: no other launcher, no other run, and nothing that can list
   or read the job can.
2. The run's identity on Batch is the same identity a Cloud Run run has: the
   same token, the same budget session, the same credentials service
   admission. No second identity scheme.
3. The refusal D3021 adds goes away for an installation where the carrier is
   in place, and stays, with its fix, where it is not.
4. `fugaro update` is the whole upgrade for an installation on Batch: no
   other command, deletion or manual step, safe with runs in flight.
5. Nothing is left behind per run that someone must clean up by hand.
6. A numbered live check proves it on a real Batch installation: a run
   launches, opens its session, is metered, and a second principal with the
   launcher's rights cannot obtain the first run's token.

### What the brief knows it does not know

- Which carrier. The earlier design rejected a per-run Secret Manager
  secret; why, and whether that still holds, is for the reconciliation.
- What Cloud Batch offers today for per-task secrets, environment, or
  identity, which may have changed since the earlier design.
- Whether anyone uses the Batch backend. Its live check has never been run.
  If the honest answer is that nobody does, removing the backend is a
  legitimate alternative to put to the owner.
- How the reaper, the run record and `doctor` treat a Batch run's token.

### Out of scope

- Any change to how a Cloud Run run gets its token.
- Removing budget mode off (docs/always-a-budget) and moving keys off job
  mounts (docs/keys-through-service), except where this work touches them.
