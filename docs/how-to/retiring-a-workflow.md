# Retiring a workflow or an account

Retiring is removing something from `fugaro.yaml` — a workflow, a named Claude account, a secret no job mounts any more — and letting the plan delete what it declared.

Two things make an ordinary `fugaro update --repo` fail at this:

- A secret container carries `prevent_destroy` in the configuration, so a plan that would destroy one fails before the delete guard sees it.
- A job carries `deletion_protection = true` in the state, and once its variant has left the configuration no apply can lower it: `--allow-job-delete` reaches only the jobs the configuration still declares.

An ordinary run prints what the change retires before it plans — the addresses going away and why — and names the command that finishes it. That summary is a warning, not a refusal: the run goes on to plan, and exits 1 only in the two cases above.

## The one-command form (**per repository**)

In the checkout, after the `fugaro.yaml` change is merged:

```bash
fugaro update --repo --retire
```

It is the whole transition, in the order the design fixes:

1. **Remove the protected addresses from Terraform's state**, with `terraform state rm` — a secret container, and a job the state still holds with its protection on. This destroys nothing in GCP, and it runs before the plan because no plan can be made until it has.
2. **Plan, print the plan's own counts and its full delete list, and confirm again.** The project's name is typed at a real terminal, twice over the command; `--yes`, `--json`, `--non-interactive`, a pipe and a coding agent's session take neither confirmation.
3. **Apply, then delete the orphaned Cloud Run jobs through the Cloud Run API** — only jobs carrying this repository's own labels, of a variant the configuration no longer builds. A job that is not ours, or another variant's, is refused, with the `gcloud run jobs delete` command to run by hand.
4. **Say what is left** if it stops in the middle: the addresses that left the state (alive in GCP, with nothing managing them until the delete runs) and the `gcloud` command that finishes it. Rerunning is safe — an address the state no longer holds is not removed again, and the orphaned jobs are found by their labels.

`--retire` excludes `--plan-only`, `--print-vars` and `--forget`. `--allow-delete <address>` may be passed with it, and can only narrow what the guard allows.

## The manual order

If you prefer it, or the binary is older than 0.7.5:

1. **Before changing `fugaro.yaml`,** run `fugaro update --repo --allow-job-delete` (**per repository**): its only change is `deletion_protection = false` on the repository's jobs.
2. **A secret only the removed workflow used:** Terraform must forget it rather than delete it. Take it out of the state with `terraform state rm '<address>'`, run by hand in the repository's working directory, outside Fugaro's guard. Do **not** hand-write a `removed { from = … lifecycle { destroy = false } }` block into the working directory: `PrepareWorkdir` wipes and rewrites that directory, so the file does not survive to the plan it was meant for. Skip this when no secret was used by that workflow alone.
3. **Change `fugaro.yaml`, merge it, and run `fugaro update --repo --allow-delete <address>`** once per address the guard listed (the refusal prints them). Read the plan: it must delete only what you removed. The remaining jobs get their protection back in the same apply.

**Fugaro never deletes a secret or a secret version.** A container a retirement releases is taken out of Terraform's state and stays in Secret Manager with its value: nothing in the flow calls `secrets delete`, `versions delete` or `versions destroy`, and `gcloud secrets delete <name>` is what removes one if you want it gone. A live, correctly-labelled container is adopted back into the plan and printed as a `note:` line rather than retired.

Turning every workflow's `check: off` instead of removing it is the same transition, smaller: the plan deletes that repository's check job, its Scheduler job and the scheduler's invoker grant.

A managed project has no `fugaro update --forget` ([design/managed-projects.md](../design/managed-projects.md) §10.2). Design of record: [design/config-transitions.md](../design/config-transitions.md).
