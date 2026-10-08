# `fugaro image refresh`

Status: design approved by the user (2026-10-07); details settled from the code, each one a veto-able decision in the plan ([plans/2026-10-07-image-refresh.md](../plans/2026-10-07-image-refresh.md)). Delivery: release **0.5.1**.

## Purpose

Moving a repository onto a new release's base image takes four commands today, in an order that is easy to get wrong: `fugaro init --base <kind>` from outside the checkout, `fugaro init --repo` in the checkout (only so the daily image check job follows the new base), `fugaro image build --repo <owner/name> --workflow <name>` for each workflow, then `fugaro init --anchor`. A build started before the copy bakes in the old `fugaro` binary. A check job left on the old base refuses `fugaro.yaml` once `gcp_project:` is merged. The `init --anchor` refusal, the recipe image check, `docs/release.md` and the `/new-release` operator list all repeat this sequence. `fugaro image refresh` replaces it with one command that resumes where it stopped.

## Behaviour

```
fugaro image refresh [--repo <owner/name>] [--workflow <name>]... [--project <name>] [--image-source <registry/owner>] [--expect-digest KIND=sha256:<hex>]...
```

You run it in a checkout of one repository, in your own terminal. Every confirmation that exists today stays, and no step runs Terraform. It runs these steps in order:

1. **Preflight** (read only). The command needs a real terminal, refuses inside a coding agent's session, and refuses a development build. It then checks the checkout's `fugaro.yaml` and origin (with `--repo`, the origin must be that repository), the local config, and that the project lists the repository. It works out which workflows are selected and their base kinds. Any `base_images.<kind>` that is not a release image `fugaro init` copied (a `dev-<sha>` or hand-pushed image) **stops the command**. The error names the entry and the one fix: remove it from the local config, keeping a backup, then rerun. It never offers to replace the image. It reads each workflow's build record and prints the whole plan before anything changes: the kinds and the base each one moves to, the check-job update, and the workflows to rebuild (N of M) with the reason for each.
2. **Base.** This is `init --base <kind>`'s image stage alone, run once per kind of the selected workflows (and only those kinds). It copies this release's base image into `fugaro-base` if the registry lacks it, showing the digest and asking for its own confirmation. It records `base_images.<kind>` and republishes the shared config, as `init --base` does. If the image is already there, it reports "No changes".
3. **The daily image check job.** No Terraform runs here. This step reads the repository's check job (`fugarochk-…`) through the Cloud Run Admin API and plans two changes from the updated local config:
   - the container image;
   - `base_images` inside the `FUGARO_CHECK_SPEC` env var. This spec is what a rebuild started by the check builds FROM. A real `init --repo` plan for a base change touches exactly these two attributes.

   The step shows the old and new image and spec, asks for its own confirmation (the project's name, as for a Terraform apply), and writes both in one update. The update uses the job as it was read, so every other field and env entry is unchanged. The job's etag goes with the update, so a change made to the job since it was read is refused. The new spec is the same Go struct, marshalled by the same code that `init --repo` uses to render the variable. A later `init --repo` from this local config therefore plans no change for the job. If the job is already current, the step reports "No changes". If the repository has no check job, the step says so and moves on.
4. **Builds.** For each selected workflow, the step reads the build record's `base_ref`:
   - **skip** if the record's `base_ref` is the base the next build would start from;
   - **skip** if it is a newer release copy of the same kind (no downgrade);
   - otherwise **rebuild** with today's Cloud Build. Each rebuild asks for the project's name and waits for the build to finish.
5. **End.** If the checkout's `fugaro.yaml` lacks `gcp_project:` (default runs bucket, a listed repository), the command prints `fugaro init --anchor` as the next step. It never writes that line itself.

**Stopping partway.** If a step fails or is declined, the error names the step that stopped, the steps that finished, and the exact command to rerun. The exit code is the one the step gave: 1 for a refusal or a declined confirmation, 2 for a cloud failure. Running the command again is safe: finished steps report "No changes" and the run continues.

**Scope.** One repository per invocation. A future version could add `--all`.

## Safety

- It applies cloud changes (a registry copy, a Cloud Run job update, billable builds), so it refuses inside a coding agent's session with `initflow.AgentRefusal`. This check comes before any credential, file or network use. The command also needs a real terminal: each billable build is a typed confirmation, and `--yes`, `--json` and `--non-interactive` are not offered.
- It never replaces a custom base image, never writes `fugaro.yaml`, never onboards a repository and never runs Terraform.
- A hand-pushed image in the project's own registry under a release-looking tag (for example `fugaro-go:9.9.9`, or a leading-zero tag such as `0.06.0`) counts as a release copy. This is inherited from `init`'s rule; writing there already needs registry write access, so it is not a new exposure.
- The check-job update writes only the image and the spec's base images, from the local config the operator just updated, with the operator's own credentials. An operator who applies `init --repo` already holds the `run.jobs.update` and `actAs` permissions on the build account that this needs.
- Tests use fakes only: `gcpfake.Run` (with jobs patch), fake registries, `file://` and `mem://` buckets, and a fake Cloud Build. No test applies anything live, and nothing touches EdgeWeb or EdgeServer. The command handles no secrets.

## The check job and Terraform

The direction given was to add the check job's `image` to its `lifecycle.ignore_changes`, so that Terraform and a direct update never fight. The code shows three problems with that:

1. **The base is in two places.** It is in the image and in `FUGARO_CHECK_SPEC`. Ignoring only the image would let the next `init --repo` reset the spec to whatever its local config says.
2. **Terraform cannot ignore one entry of the env list.** The env is a list of nested blocks with no key. The only way to address one entry is by its position, and the position moves when an env var is added (for example `FUGARO_GITHUB_APP_ID`).
3. **Ignoring the whole env would cost Terraform the rest of it.** Terraform would lose control of every env var of the check job: `FUGARO_PROJECT` (which its preconditions check), the spec's `workflows` (which change when a workflow is added) and the secret env. `init --repo` would then have to write the whole env directly as well.

**Decision (veto-able): no lifecycle change.** Instead, the direct update writes exactly the bytes Terraform would render from the same local config. A unit test pins this: the rewritten spec equals `infra.Repo`'s rendering with the new base images.

Consequences:

- An existing installation's state needs nothing: no one-time `init --repo` and no Terraform migration.
- After a refresh, the next `init --repo` from the same local config refreshes the state from the live job, sees it equal to the configuration, and plans no change.
- Nothing reports the update as drift. Discovery records only workflow jobs' images, `doctor` does not read the check job, `ls` reads only the spec's `workflows`, and the plan-cover rules see no diff.
- The generated roots and the tfvars golden do not change.
- An operator whose local config names an older base reverts the job on their next `init --repo`. That operator reverts both the image and the spec together, so the job stays consistent. This is what happens today; a future `init --repo` warning about it is a candidate.

**Unverified until the live check:** that Cloud Run v2 `jobs.patch` accepts the read-back output-only fields, and that the provider's refresh shows no diff after a direct update. Both are covered by the post-release sandbox step in the plan.

## Non-goals

- Replacing a custom or hand-pushed base image.
- Firebase rules or any other `init` stage.
- Several repositories in one run.
- Writing `fugaro.yaml`, including the `gcp_project:` line.
- `--plan-only`, `--json` and `--yes`. Any of these is a candidate later.

## Decisions

The plan lists them as D1 to D14:

- `--repo` checks the origin; it is not a way to run without a checkout.
- A real terminal is required.
- A development build is refused.
- What counts as a custom base.
- Only the selected kinds are copied.
- The base step is the images stage alone.
- The check job is updated directly, with no lifecycle change.
- The "current" rule for builds.
- A declined build stops the run.
- The end note.
- Exit codes.
- Where the docs point.
- Skills lint treatment.
- The release shape.

## Delivery: release 0.5.1

This is a patch release: a new command, with no format change and no version gate. It is one PR group:

1. the fake's jobs patch;
2. the check-job update in `internal/infra`;
3. the small refactors (the images stage's `only` kinds, and the Cloud Build seam with `submitAndWait`);
4. the command;
5. the pointers in the `init --anchor` refusal, the recipe image check and `init --help`;
6. the docs (`docs/gcp-setup.md`, `docs/recipes.md`, `docs/release.md`, the `/new-release` operator list and the working skill's follow-up table).

The setup skill has no line naming the sequence, so it is unchanged. Then `/new-release 0.5.1` with `docs/releases/v0.5.1.md`.
