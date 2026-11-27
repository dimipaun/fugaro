# Offboarding a repository

Offboarding is taking one repository's cloud resources and its records out of an installation. **There is no offboarding command.** Step 1 is an ordinary apply; steps 2 and 3 are an outline run by hand, outside Fugaro's guard — and `PrepareWorkdir` overwrites hand edits the next time `fugaro update --repo` runs.

Every resource that could lose data is protected on purpose: jobs by `deletion_protection`, and secrets, the runs bucket and every registry by `prevent_destroy`, so a destroy plan fails at plan time until you edit the module. Steps 2 and 3 have not been exercised live.

## 1. Lower the jobs' protection (**per repository**)

In the checkout, before you change anything else:

```bash
fugaro update --repo --allow-job-delete
```

Its only change is `deletion_protection = false` on the repository's jobs — the workflow jobs and the check job. The flag is a variable of that apply, so the next run without it puts the protection back.

**After a `fugaro project repos rm` this step is not a no-op.** That command keeps the repository's state, and the root still declares the repository's workflow jobs, its check job and its daily check schedule, so this apply **recreates all of them** — and it writes the repository's `repos:` entry back into this machine's config and republishes the shared config. Step 3 destroys the recreated jobs and schedule again, and step 4 removes the state and, as its last line says, the `repos:` entry.

## 2. Keep the secrets (**per repository**)

They hold values Terraform never saw, and `prevent_destroy` refuses to destroy them. Take them out of the repository's state, so Terraform forgets them and leaves them and their values in Secret Manager. `fugaro update --repo --retire` removes the addresses the configuration no longer declares ([retiring-a-workflow.md](retiring-a-workflow.md)); a whole repository's `fugaro.yaml` still declares its secrets, so there the removal is `terraform state rm '<address>'` run by hand in the repository's working directory, outside Fugaro's guard. **Fugaro never deletes a secret or a secret version:** `gcloud secrets delete <name>` removes one if you want it gone.

## 3. Destroy the rest, by hand

The repository's registry has `prevent_destroy`, so this needs the module edited first, and nothing checks the plan for you: read it line by line. The registry, its images, the workflow jobs, the job accounts, the build account and the check job and its schedule go with it — including the jobs step 1 recreated. The runs bucket keeps the repository's `runs/`, `cache/` and `builds/` objects until their lifecycle rules delete them (`builds/` never expires: delete it by hand).

## 4. Forget what is left (**per repository**)

`fugaro update --repo --forget` removes the repository from Terraform's state; it destroys nothing. Then remove the repository from the local config's `repos` — including the entry step 1 wrote back.

## The two lists the repository is still on

**Per installation.** The repository stays in the published shared config until you edit `fugaro/config.yaml` in the runs bucket by hand — a later `fugaro update --publish-config` merges, so it does not remove it. If the project publishes a policy, `fugaro project repos rm <provider>/owner/name` un-enrols it there too: it takes away the allow-list entry, this machine's `repos:` and `repo_images:` entries, the published per-repository admission and the repository's Cloud Run and Cloud Scheduler jobs. It keeps the repository's Terraform state and the repository root's own IAM members — what steps 1 to 3 above plan from, and the grants only their apply deletes — and says so.

## A managed project

Removing the `fugaro_managed` label takes the project out of managed mode, but it does not take the managed deployer's fence out of the installation, and `fugaro update --forget` is refused for a fenced installation. [managed-projects/spec.md](../managed-projects/spec.md) §10.1 and §10.2 say what removing the label and rolling back do and do not undo.
