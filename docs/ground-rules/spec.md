# Fugaro Ground Rules Spec

These rules exist to stop spec sprawl and the corner cases it breeds. When a design choice conflicts with a rule here, the rule wins. Prefer doing the right thing automatically over adding flags, modes or operations that can fail.

## 1. Project-centric: no per-repo update

- `fugaro update` runs once per project after a CLI upgrade. It may run Terraform. It is the only update operation.
- `fugaro update --repo` is removed. Drop the in-progress approach that checks out every repo to run it automatically.
- Audit the code for every reason a repo needs updating today. Anything that touches infrastructure (secrets, triggers, IAM, images, jobs, caps) moves to project level.
- Repo-level config (fugaro.yaml) is plain declarative data only. It never requires Terraform or an update operation.
- fugaro.yaml carries a schema version. The CLI reads all supported older versions and upgrades them in memory on load. Files are never rewritten as a required step; an agent PR may bump them opportunistically.
- Support for an old schema version is dropped only deliberately, with a deprecation window, never by accident.

## 2. Agents can run Fugaro commands

- Audit every command currently blocked for agents and record why. Keep a block only if it is genuinely critical.
- Fugaro supports only managed projects: Fugaro owns the GCP project and is responsible for it. Do not push black-box operations onto the user.
- Agents may run `fugaro update`. This matters: most failures happen during update, the raw output means little to users, and the agent can interpret it and guide them.

## 3. Gating and deletion

- Gate on what is being deleted, not on the fact that it is a delete.
- Resources Fugaro created and can recreate (services, jobs, images, temporary artifacts) may be deleted freely by Fugaro or the agent.
- Irreplaceable user data (secrets, project history, spending records, settings) is gated.
- Remove copy-paste escape flags such as `--retire` from normal operations. A flag the user just copies protects nothing.
- Destroying the project, its buckets or its RTDB never happens in normal operation. `fugaro update` never does it, with or without a flag. Teardown is a separate, deliberate command.
- Secrets follow membership. Removing a user from the project is the admin decision; their secrets are then deleted, optionally after a short grace period. Secrets of current members are never deleted by update.
- When an irreversible deletion genuinely needs approval, say so in one or two plain sentences.

## 4. Output and verbosity

- Full output always goes to the log file (existing spec).
- Human output: one short line per meaningful step, in plain semantic terms (for example "Applying infrastructure changes"). Brew-style colour and icons are welcome.
- Long steps show liveness, such as a spinner with elapsed time, so the user never faces minutes of silence.
- Never print raw Terraform or build output to the terminal.
- Errors, warnings and notices: what happened, and what to do. Nothing more.
- When an agent runs a command, give it structured detail to work from; the human sees the short summary.

## 5. Baked vs cloned repos

- Remove the clone/baked mode flag at job level.
- At job start: if the repo is baked into the image, fetch to update it; otherwise clone it. Never fail over a mismatch.
- If a baked copy is broken or unusable, fall back to a fresh clone.
- A setting remains only at image build time, deciding whether to bake a repo, defaulting to yes.

## 6. Image rebuilds

- `fugaro update` no longer rebuilds images synchronously just to ship a new Fugaro version.
- Every job file is stamped with the Fugaro version that created it.
- At job start, if the installed Fugaro is older than the stamp, upgrade in place to exactly the stamped version before running.
- Images catch up on the regular nightly rebuild. Update may also trigger a background rebuild that nobody waits on.
- An eager rebuild is required only when the image itself changes (base image, system packages), not for a Fugaro version change. Where rebuilds happen, run them in parallel (existing spec).
