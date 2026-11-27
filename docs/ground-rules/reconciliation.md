# Ground rules: reconciliation

The spec ([spec.md](spec.md)) checked against `origin/main` at `a067bd0fa`, on 2026-11-26. **Where this
document and the spec disagree, this document wins.** The spec itself says its rules win over any design
choice they conflict with; this document records which earlier designs that reaches (§5).

## Modules

The owner approved this map on 2026-11-26. Every finding, decision and task names a module.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `update-only` | §1: `update` is the only update operation; `--repo` and the per-repository Terraform root go | `deletion-gating` (interface: the classes) |
| `config-versions` | §1: `fugaro.yaml` read by version, upgraded in memory; the deprecation window | — |
| `deletion-gating` | §3: gate on what is deleted; the escape flags go; secrets follow membership | — |
| `command-output` | §4: one plain line per step, liveness, no raw output, structured detail for agents | — |
| `agent-access` | §2: the agent-block audit and its lifts; agents run `update` | `deletion-gating` (interface) |
| `checkout-fallback` | §5: the `checkout` key goes; the runner fetches a usable bake or clones | — |
| `version-stamp` | §6: each task stamped with the launcher's version; the runner upgrades in place; rebuilds leave `update` | — |

`config-versions`' retirement of the image-age gates waits on `version-stamp` (D3446).

## Assumptions

- **A1.** "Job file" is the task file a launch writes (`runs/<slug>/<run>/task.json`, `task.Spec`). The
  owner chose it over the Cloud Run job (D3441).
- **A2.** "Project" is the Fugaro project of `docs/project-centric/`: one GCP project, many repositories.
- **A3.** "Agent" is a coding-agent session as `internal/agentenv` `Session` detects it. It is not a
  Fugaro run's own agent, which runs no `fugaro` commands against the project.
- **A4.** "Fugaro owns the GCP project" means the managed mark (`docs/managed-projects/`), which the
  owner already made the only supported kind (D2217, `docs/managed-flag-ergonomics/`).
- **A5.** "Settings" among the irreplaceable data means the published policy, the project layer and the
  RTDB nodes, not the local config files on a person's machine.
- **A6.** The "existing spec" for parallel rebuilds is not in `docs/`. No document asks for them (F27);
  D3444 and D3446 set them here.

## Findings

What the repository already has, against the spec's concepts:

| Spec concept | What the repository has |
| --- | --- |
| Only `update` updates | `update` plus `setup --provision-batch`, `project repos add/sync/rm`, `image refresh`, `image build`, `project policy publish`, `apply`, `managed apply`, `restore`, the after-write hook (F2) |
| No per-repo update | `update --repo` and stage 9, the repository stage over the checkout's repository (F1) |
| Infra at project level | Shared-job profiles (`infra.SharedRepo`), but per-repository roots remain for repositories no profile admits (F5) |
| `fugaro.yaml` schema version | `version: 1` only; key-level in-memory upgrades (`dropLiftedKeys`, `RetiredKeyNotes`) (F7) |
| Agents run commands | The managed gate (`docs/managed-agent-converge/`) admits some `update` stages and a few commands (F12) |
| Gate on what is deleted | Gate rule 1: every delete is refused unless `fugaro/allow-delete.json` names it (F15) |
| Teardown | None (F18) |
| Secrets follow membership | `fugaro user offboard` sweeps a person's containers at once (F19) |
| Log file, step lines | `internal/report`: log under the user state dir, `==>` headers, icons, spinner (F20) |
| Bake setting at build time | `bake_repos`, project-level, default true (`imagebake.Decide`) (F25) |
| Nightly rebuild | `catalog_check` scheduler, paused by default, one build a night (F28) |
| Version stamp | `config/policy/required_cli`, a project-wide minimum-CLI fence, not a per-job stamp (F30) |

### update-only

- **F1. Wrong.** `update --repo` still exists (`internal/cli/init.go` `newInitCmdWith`, `initRun.repoEngine`).
  Three flags work only with it: `--no-build`, `--allow-job-delete` and `--retire`. Plain `update` runs the
  same engine as stage 9, but only over the working directory's checkout (`init_repo_stage.go`
  `repositoryStage`, `init_repo_target.go` `resolveRepoTarget`). Its removal is U12 of
  `docs/update-from-anywhere/`, the owner's D3353 of 2026-11-26, and U12 waits on U11.
- **F2. Exists.** Other commands change the cloud. Each is classified in D3424:
  - `setup --provision-batch` provisions the compute root the first time; `update` already converges an
    existing one by the installation's own opt-in (`init_compute.go`, R2/D3110);
  - `project repos add`, `sync` and `rm` (`project_enroll*.go`, `project_repos_rm.go`);
  - `image refresh` and `image build`;
  - `project policy publish`;
  - `apply` and `managed apply`;
  - `restore`;
  - the after-write hook (`write_publish.go` `publishAfterWrite`, `chain.go` `chainApply`).
- **F3. Open.** The approach the spec drops is lift L5 (`docs/update-from-anywhere/`). L5 reads repositories
  through the forge, not a checkout (`internal/repofiles` `Read`). These parts are merged: C1, U1, U2, U3a,
  U3b, U4, U5, U6a, U6b, U7b, U8, U9a, U10 and U13. U11, the step that would apply every repository's
  root, is not merged, and neither are U7a, U9b–U9d, U12, I1 or K1. D3420 settles which parts stay.
- **F4. Exists.** The repo-coupling audit (`docs/repo-coupling-audit/report.md`) already lists every
  coupling, B1–B19 and RC1–RC107, with the owner's rulings D3235–D3239. The audit this spec asks for is
  that report, plus F5 and F6.
- **F5. Gap.** `deploy/terraform/gcp/roots/repo` still creates, for each repository no shared-job profile
  admits:
  - service accounts, an Artifact Registry repository and Secret Manager containers;
  - a Cloud Run job per workflow;
  - a check job with its scheduler;
  - IAM bindings.

  These come from `fugaro.yaml` workflows (`var.repo.workflows`). The owner's D2312
  (`docs/shared-jobs-default/`) already ruled out repository-level Terraform; the code has not caught up.
- **F6. Gap.** Per-repository state that `update` writes but that is not Terraform:
  - `creds/workflowsecrets/<slug>` (`update_workflowsecrets.go`, RC70/RC99);
  - `creds/repoauth/<slug>` (RC48);
  - the RC39 facts in the local config's `repos.<r>`;
  - catalog build leases and records keyed by repository (RC44, RC55);
  - slug-scoped IAM conditions (RC57).

  All of these are written from the project side already, and none needs a checkout. Since the base,
  #4102 (IS6) took workflow secrets out of every job spec; `creds/workflowsecrets/<slug>` is still written. The plan keeps them
  and states that they are project data.
- **F7. Exists, partly.** `config.Config.Version` is refused unless it is 1 (`internal/config/validate.go`),
  and `schemas/fugaro.schema.json` holds `"const": 1`. There is no reader keyed on the version. The in-memory
  upgrades work key by key: `config.Resolve` and `dropLiftedKeys`, `RetiredKeyNotes` (`retired.go`), and
  `config.RepoSecretsOff`. `update` no longer rewrites `fugaro.yaml` (U6a).
- **F8. Gap.** `decodeRepo` decodes strictly (`KnownFields(true)`, as CLAUDE.md requires), so an older
  runner refuses a newer file. The code bridges the gap with per-key image-age gates at launch:
  `recipes_skew.go` (`cloneSince`, `rigorSince`, `governanceSince` and others) and
  `init_fugaroyaml.go` `gcpProjectFieldSince`. With D3441, the runner upgrades to the launcher's version
  before it reads anything, so the gates have nothing left to guard (D3446).
- **F9. Rule.** CLAUDE.md's HARD RULE on `fugaro update` says the update "brings the installation and the
  repository" to the new version, offers a pin removal as a diff, rebuilds images "behind its own
  confirmation", and "never works around the refusal of cloud writes in a coding agent's session". Other
  text names `update --repo`:
  - `.claude/rules/credentials-service.md` (twice);
  - `plugin/skills/update`, `setup` and `images`;
  - `docs/how-to/budgets.md`, `registry-cleanup.md` and `offboarding.md`.

  The spec reverses four of these points. D3447 rewrites them.
- **F10. Exists, under another name.** No deprecation policy exists for schema versions. The precedents:
  - L8's lifted keys get a one-release window (D3298, D3305);
  - `projectpolicy.Version` is migrated in place by `update`;
  - `required_cli` fences older CLIs.

  D3423 sets the window for `fugaro.yaml` versions.
- **F11. Open.** "The only update operation" leaves unclear whether enrolment, publishing and the
  after-write hook count. D3424 answers it.

### agent-access

- **F12. Exists.** An agent's `update` applies only the stages the managed gate admits
  (`update_managed.go` `gatedStages`; `initflow/loop.go` `agentStage`). These stay refused even in a
  managed project:
  - the secrets stage, which needs a hidden prompt;
  - Compute;
  - retirement (`retireManaged` requires a person at the terminal);
  - the budget self-flag;
  - any run with an installation-shaping flag (`shapesTheInstallation`).

  §A lists every command an agent is refused, with its reason and the decision on it.
- **F13. Wrong.** The handoff table of `docs/managed-agent-converge/` says rigor and `accounts rm` are
  "now" open to agents. `accounts rm` is still refused. `rigor set` already admits an agent's *raise* and
  refuses only a lowering, "the sensitive direction" (`rigor.go`); `rigor floor` and `rigor security rm`
  refuse the same way. (Corrected after review: the first text said rigor was wholly refused.)
- **F14. Exists, since the base.** T3 of `docs/managed-flag-ergonomics/` (#4045) merged after this
  document's base, as `e1c86a3e4`: `update` now stops on an unmanaged installation, naming
  `fugaro managed flag`. The "nothing is implemented" status lines of `docs/managed-flag-ergonomics/`,
  `docs/update-from-anywhere/` and `docs/required-version-stamp/` are stale.

### deletion-gating

- **F15. Exists, partly.** The gate already classifies by type: `tf.DataBearingType` and
  `RefusesRemovalType` (`internal/infra/tf/cover.go`, D1551/D1545). In the gate (`managed_cover.go`), a
  removal the owner's delete list (`internal/infra/managed/deletelist.go`, `fugaro allow-delete`) does not
  name is refused when its type is data-bearing, and passes with a warning otherwise. D1551 classes
  registries, service accounts, bucket objects and API keys as data-bearing. Outside the gate, `tf.Guard`
  (`internal/infra/tf/guard.go`) refuses every delete or replace unless `--allow-delete ADDR` names it.
  The delete list is also read by `managed apply`, `restore` and `internal/managed/inventory.go`.
  (Corrected after review: the first text missed the existing table.)
- **F16. Gap.** These copy-paste escape flags are in normal use:
  - `--retire`: the refusal prints `fugaro update --repo . --retire` (`init_refusal.go` `retireNextCommand`);
  - `--allow-delete ADDR`;
  - `--allow-job-delete`, which lowers `deletion_protection`;
  - `--yes` on `accounts alias` and `accounts switch`, which the no-prompt policy's T11 missed;
  - `--executable-changes` on `config publish` and `workflow` (`config_publish.go`, `workflow.go`), the
    opt-in to publishing command or image-step changes for every repository. D3425's second amendment
    keeps it a person's.

  `--forget` edits only Terraform state. It is a repair tool, not a normal operation, and stays.
- **F17. Exists.** Nothing irreplaceable can be destroyed by `update`:
  - the runs bucket has `force_destroy = false` and `prevent_destroy`;
  - the RTDB instance, the registry, the secret containers and the credentials, licence, orchestrator
    and mailbox stores all carry `prevent_destroy`;
  - no code deletes a project, a bucket or a database.

  Retiring a root keeps its secret containers (`keptSecrets`, `neverDestroyedTypes`).
- **F18. Gap.** There is no teardown command. `docs/how-to/offboarding.md` is a manual procedure. D3432
  gives teardown its own spec.
- **F19. Exists.** `fugaro user offboard` and `fugaro workspace user rm` delete a person's containers at
  once, by their owner annotation (`intake/handlers.go`). There is no grace period, and agents are refused.
  `update` deletes no member's secret. The only secret it deletes is a retired project-wide
  workflow-secret copy (`update_retire_copies.go`), which is not a person's.

### command-output

- **F20. Exists.** Most of the spec is already built, in `internal/report`:
  - the log file (`docs/cli-output-console-graph/`, D1351/D1352; `report/logsink.go` `OpenLog`);
  - the census of commands that keep one (`report_exempt.go`, `report_census_test.go`);
  - `==>` headers, icons and colour (`report/style.go`);
  - a spinner (`report/spinner.go`);
  - `--json`, plus `--events` NDJSON on setup and update (`setup_events.go`).
- **F21. Gap.** Step lines show internal stage IDs (`preflight`, `installation-2`, `firebase`;
  `initflow/stage.go`), not plain words.
- **F22. Gap.** The spinner shows no elapsed time. Without a TTY, or with `NO_COLOR`, `--json` or
  `--verbose`, a long step prints nothing until it ends.
- **F23. Wrong.** Raw Terraform output still reaches the terminal in several places:
  - under `--verbose` (the stream);
  - under `--plan-only`;
  - with no reporter (`--forget`, `--config-only`, `--print-vars`);
  - when the log fails to open.

  `image build --local` streams docker and git output (`image.go`), and the images stage prints mirror
  lines directly (`init_images.go`).
- **F24. Gap.** Output never changes for an agent; `agentMarker` is used only to refuse. The 140-character
  limit is tested only for about 30 update warnings (`update_laconic_test.go`), and several refusals run
  far longer (`selfFlagUnmanaged`, `retireRefusal`).

### checkout-fallback

- **F25. Exists.** The bake is decided at build time by `bake_repos` (`projectpolicy.Policy.BakeRepos`,
  `imagebake.Decide`, default true), which is project-level already. §5's last rule needs no change.
- **F26. Wrong.** The runner fails on a mismatch:
  - `checkBakedCheckoutOrigin` and `checkProjectCheckoutOrigin` refuse a foreign origin, and
    `refuseForeignWorkDirCheckout` refuses a foreign checkout;
  - a failed fetch or reset ends bootstrap;
  - `openCheckout` errors when `FUGARO_CHECKOUT=clone` has no remote;
  - at deploy, `infra/spec.go` `unbuiltOnBaked` (D1191) refuses a prebuilt entry on a `baked` workflow;
  - at launch, `recipes_skew.go` gates `clone` on `cloneSince`.

  The runner falls back to a clone only when the baked directory does not open at all. The mode is the
  `checkout` key: `config.Workflow.Checkout`, plus `Profile.Checkout` (`layer.go`). It reaches the job as
  `FUGARO_CHECKOUT` (`runner.CheckoutEnv`), and `run.cloneJob` switches on it: a blobless clone plus
  `mise install`, or a full open. `docs/checkout.md` is stale; it still describes per-repository images
  and `update --repo`.

### version-stamp

- **F27. Wrong.** `update` rebuilds images on every release and waits for them.
  `init_images_catalog.go` `catalogBaseStale` marks an entry stale when its release base is older than
  the CLI, so every release makes every entry stale. `joinCatalogBuilds` then blocks the run's end. The
  builds run one at a time (`catalogBuilds`, a single worker), and no document asks for parallel builds.
- **F28. Gap.** The nightly rebuild exists, but it is paused by default:
  `google_cloud_scheduler_job.catalog_check` (`modules/installation/catalogcheck.tf`) takes its pause from
  `catalog_check.paused`, which defaults to true (`paused = optional(bool, true)` in
  `roots/installation/variables.tf` and `modules/installation/variables.tf`, and `jobPaused` in Go, F36), and each run makes at most one build (`catalogRunBuildCap = 1`). Its
  own comment gives this as the reason `update` rebuilds in line.
- **F29. Rule.** The runner is the image's `fugaro` binary. `images/base/Dockerfile` builds it from
  source, and `images.yml` publishes one base image per release tag. Nothing downloads a `fugaro` binary,
  and the runner never upgrades itself. Releases are archives on GitHub with `checksums.txt`
  (`.goreleaser.yaml`). The job has no egress restriction (`infra/spec.go`; only the compute VMs have one).
  The runner runs as a non-root user, so `/usr/local/bin` is not writable.
- **F30. Exists, something else.** `config/policy/required_cli` (`internal/versionstamp`) is the
  project-wide minimum CLI. `update` writes it and CLI commands enforce it; the runner never reads it.
  `task.Spec` carries only its schema `Version`, and `task.Parse` refuses unknown fields and any
  `Version` other than 1. A new field therefore breaks every runner that predates it (D3445, T1).
- **F31. Gap.** A launcher newer than the installation stamps a newer version. The runner then upgrades
  past the installation's own release. That is the same skew that launching from a newer CLI has today;
  `required_cli` remains the floor.

Found by the first review round, 2026-11-26:

- **F32. Exists.** `checksums.txt` is already signed keyless with cosign (`.goreleaser.yaml`,
  `checksums.txt.sigstore.json`), and `internal/basepin/cosign.go` verifies cosign signatures by running
  the binary, which the job image does not carry. With D3442's sha256 alone, a compromised release runs in
  every job; the owner kept sha256 with this in view.
- **F33. Gap.** `fugaro exec` puts its own binary's directory first on the agent's `PATH` (`exec.go`,
  `PathPrepend`), and the image runs as one non-root user. An upgraded binary therefore lands in a
  directory the agent's user could write to. VS1 makes the directory and the file read-only; the same user
  could undo that and plant other tools there, which would shadow them for the run's later review and
  finalize stages. VS5 therefore checks, before each later stage, that the directory holds exactly the
  binary with its checksum.
- **F34. Gap.** A CLI older than release A reading a stamped `task.json` gets `runstore.ErrCorrupt`:
  - `runs ls` shows the title as `-`;
  - `cancel`, `followup` and the chain raise that rewrites `task.json` fail;
  - the coordinator's readers fail too.

  `update` writes `required_cli` as its own published release (`update_version_stamp.go`), so once the
  project is updated to A, older CLIs are refused before they read. The exposure is the T1 window only
  (D3448).
- **F35. Gap.** `checkout` has a third value, `shared` (`config.CheckoutShared`), which is refused while
  `sharedCheckoutShipped` is false. The mode is read in about twenty files beyond `internal/config` (the
  catalog selection, image refresh and promote, `run.go`, `init.go`, `infra/readiness.go`,
  `infra/repo.go`). Retiring the key is a sweep, not a config change.
- **F36. Gap.** The catalog check's pause default lives in Go, not in Terraform: `config/layer.go`
  `jobPaused` (shared with the reports job) and `infra/tfvars.go` `jobPaused`. `init.go` always writes
  the value it computes.
- **F37. Gap.** `update --repo` is named in 64 non-test Go files, and in these other places:
  - the setup skill's own test (`plugin/setup_skill_test.go`), the Docs-test pins and `README.md`;
  - `SECURITY.md` and `website/content/security.md`;
  - `docs/how-to/retiring-a-workflow.md` and `teammates.md`;
  - `plugin/skills/setup/reference/`.
- **F38. Exists.** Whole-root retirement already waits on `infra.InFlightJobs`
  (`docs/design/update-in-flight.md`). UO2 keeps it when retirement becomes automatic.

Found by the second review round, 2026-11-26:

- **F39. Rule.** The runner's mismatch checks are also security guards. `checkBakedCheckoutOrigin`
  (`runner.go`) holds the push binding against `pushurl`, `insteadOf` and URL-named remotes, so a push
  cannot carry the checkpoint and its credential to another repository. `refuseForeignWorkDirCheckout`
  (`projectjob.go`) keeps another repository's tree from the agent. CF1 keeps both properties while it
  falls back to a clone.
- **F40. Gap.** The registries carry `prevent_destroy` in Terraform (`modules/repo/registry.tf`,
  `modules/installation/registry.tf`), and `TestPreventDestroy` (`deploy/terraform/terraform_test.go`)
  requires it. Calling them recreatable in the gate changes nothing until the Terraform follows (DG3).
- **F41. Gap.** `task.Parse` and `runstore.ReadTask` decode strictly, so a runner refuses a task with any
  field it does not know before it could read `cli_version`. VS5 reads the stamp with a lenient decode
  first.
- **F42. Rule.** A Cloud Run job delete takes its executions with it. Whole-root retirement waits on
  `infra.InFlightJobs` (F38), but the single orphaned-job retirement does not
  (`docs/design/update-in-flight.md`, "Needs care, named in the line"). Once the gate admits recreatable
  deletes, any plan could take a running execution with it (D3450).
- **F43. Wrong (in the first draft of VS3a).** A catalog entry's key (`imagecheck.Key`) hashes packages,
  tools and the base reference only. `images/derived/Dockerfile.tmpl`, which is embedded in the CLI, and
  `images/common/finalize-checkout.sh` are not in it, so the epoch covers them too.

## Decisions

Answered by the owner on 2026-11-26 through `AskUserQuestion`, unless marked **resolution**. A resolution
is a point with one defensible answer, stated rather than asked; the owner sees each one in the pull
request.

| D | Module | Question | Answer (2026-11-26) |
| --- | --- | --- | --- |
| D3420 | update-only | What happens to L5's U11 and U12? | As recommended. U11 is cancelled. U12 removes `--repo` without waiting on U11. The merged U-tasks stay, because they read through the forge and need no checkout. **Amends D3353's "after U11".** |
| D3421 | update-only | How do the per-repository roots go? | As recommended. Every repository runs on the project's shared jobs. `update` retires each remaining root itself: jobs are deleted, and secret containers are kept (removed from state, not destroyed). Sizing comes from the profile (`Profile.Resources`, which exists), and a repository's `workflows.*.resources` is read and ignored with a note. |
| D3422 | config-versions | How do `fugaro.yaml` versions load? | As recommended. A registry of in-memory upgraders, v1→v2→…. Unknown keys within a known version stay errors. A file newer than the reader is refused in one line naming `brew upgrade`. The per-key image-age gates retire (D3446). |
| D3423 | config-versions | How long is the deprecation window? | As recommended: two minor releases. Through those releases `doctor` and `update` name the old version, and a later release may refuse it only by its own ruling. |
| D3424 | update-only | Which commands count as "update operations"? | **Resolution, amended after review (2026-11-26).** A command that edits project data on purpose (`project repos add/rm/sync`, `project policy publish`, `settings`, the after-write hook) is an edit, not an update. It publishes itself, as the owner ruled on 2026-11-26 ("management commands publish themselves"). It needs no checkout and writes no `fugaro.yaml`. `update` already converges an existing compute root (F2). Provisioning it the first time is a creating act, like setup's project-creating run, so `setup --provision-batch` stays. *First text: the compute root became an `update` stage. It was dropped because it would let an agent turn billable infrastructure on.* |
| D3425 | agent-access | Which agent refusals stay? | As recommended: the minimal keep list in §A. **Amended after review, confirmed by the owner the same day:** the installation-shaping flags of `update` that are on the keep list stay refused to agents (budget, billing, operator and admin lists, `--managed`, `--create-project`, `--state-bucket`, `--allow-delete`), and so do the image-source flags (`--base-image`, `--replace-image`, `--expect-digest`, `--image-source`), because they decide what code every job runs. The `settings` keys that raise a budget or widen `allow_data_to` stay a person's too. Lowering rigor lifts, as answered (F13). **Amended again after the second review, awaiting confirmation (O3):** on the same reasoning, `shapesTheInstallation` keeps refusing every flag it covers except `--registry-cleanup` and `--scheduler-region`, including `--base` and `--github-app-id`, and `config publish --executable-changes` stays a person's. These stay refused to agents: a budget raise or resume (D1542); entering any secret or token value; irreplaceable deletes (`secrets rm`, `accounts rm`, `user offboard`, teardown); `managed flag`/`unflag`; membership (`user`/`workspace` add, rm, grant); licence; `restore`; and setup's project-creating run. Every other refusal lifts. |
| D3426 | agent-access | Unmanaged projects | **Resolution.** This is consistent with D2217, and already done: #4045 (T3) merged after the base (F14). |
| D3427 | deletion-gating | What becomes of the managed gate? | As recommended. The gate stays as the plan check on agent runs. Rule 1 becomes: a recreatable delete passes and an irreplaceable one is refused (D3430). `fugaro/allow-delete.json` and the `fugaro allow-delete` command retire. |
| D3428 | deletion-gating | `--ask` | **Resolution.** Unchanged. It is the owner-terminal opt-in of D1028, not a safety flag. |
| D3429 | deletion-gating | `--yes` on `accounts alias` and `accounts switch` | **Resolution.** It goes, finishing the no-prompt policy's T11. |
| D3430 | deletion-gating | What is recreatable and what is irreplaceable? | As recommended. **Amended after review, confirmed by the owner the same day:** IAM grants to people (the operator and admin lists) are membership and irreplaceable. Registries and Fugaro-made service accounts stay recreatable, confirmed knowing that D1551 classes them data-bearing and that a recreated service account gets a new ID; `update` recreates the bindings in the same apply. DG0 extends the existing table (`tf.DataBearingType`), and this answer supersedes D1551 for those types. **Recreatable:** Cloud Run jobs and services, Scheduler jobs, images and registry repositories, build artifacts, and the service accounts and IAM bindings Fugaro made. **Irreplaceable:** Secret Manager secrets, buckets, the RTDB, Firestore, KMS keys, Terraform state, the policy and settings, the spend ledger and run history. |
| D3431 | deletion-gating | How is an irreplaceable delete approved? | As recommended: never in `update`. Its plan refuses one in one or two sentences. `--retire`, `--allow-delete`, `--allow-job-delete` and the delete list go. Such deletes exist only in their own commands, and those are refused to agents. |
| D3432 | deletion-gating | Teardown | As recommended: it gets its own spec later. This plan pins only that no normal command destroys the project, its buckets or the RTDB. |
| D3433 | deletion-gating | Grace period after offboarding | As recommended: none. Deletion stays immediate. |
| D3434 | deletion-gating | Members' secrets | **Resolution.** `update` never deletes the secret of a current member. This is already true (F19), and a test pins it. |
| D3435 | command-output | Step names | **Resolution.** Each stage gets a plain display name ("Applying infrastructure changes"). The internal ID stays in the log and in `--json`. |
| D3436 | command-output | Liveness without a TTY | As recommended. Without a TTY, a long step prints one "still …" line a minute with the elapsed time, and `--events` carries a heartbeat event. The TTY spinner shows elapsed time. |
| D3437 | command-output | Structured detail for agents | As recommended. In an agent session, a command that has `--json` writes its result document to stdout and the short lines to stderr. Explicit `--json` and `--events` are unchanged. |
| D3438 | command-output | Raw output | As recommended: never on screen. `--plan-only` prints the plain-words plan and the log path. `--verbose` shows full step reasons but no Terraform or docker stream. If the log cannot open, only the brief plan prints. |
| D3439 | command-output | The 140-character rule | **Resolution.** A tree-wide test over the human messages, with a shrinking allowlist, replaces the update-only list. |
| D3440 | checkout-fallback | An unusable baked copy | As recommended. Log one line and clone into a fresh directory, never using the baked tree. The `checkout` key is read and ignored with a note, and `FUGARO_CHECKOUT` and the D1191 refusal go. Whether to bake stays `bake_repos`. |
| D3441 | version-stamp | Which artifact carries the stamp? | **Not the recommendation:** `task.json`, stamped per launch by the launching CLI. The recommendation was the Cloud Run job's environment. |
| D3442 | version-stamp | Where the binary comes from | As recommended. The GitHub release archive and `checksums.txt` over TLS, checked by sha256, installed to a writable path, then the runner re-execs. No new dependency. Asked again the same day after a premise was corrected: cosign would not add a Go dependency (`internal/basepin/cosign.go` shells out to the binary), but it would add `cosign` to the base image and an outside call per run. The answer stood. |
| D3443 | version-stamp | A failed upgrade | As recommended. A short retry, then the run ends with one line naming the version it could not fetch. |
| D3444 | version-stamp | The nightly rebuild | As recommended. Unpaused by default, rebuilding every stale entry four at a time. |
| D3445 | version-stamp | How the stamp rolls out | **Not the recommendation:** one eager rebuild. Release A's runner reads the field, and its `update` rebuilds every image once and waits. From release A on, the launcher always writes the stamp. The recommendation was a write gated on the image's runner. |
| D3446 | version-stamp | What makes a rebuild eager | **Resolution**, following D3422, D3444 and D3445. A base-image epoch, a constant bumped when `images/base` changes for anything but the Fugaro version, is recorded on each catalog build. An entry is stale when its epoch differs or its age passes `DefaultMaxAge`. `update` waits only on an epoch change (release A bumps it, which is D3445's eager rebuild). Otherwise it submits the stale builds in the background and waits for none. The per-key image-age gates retire one release after A. |
| D3447 | all | The standing rules | As recommended. CLAUDE.md's HARD RULE, `.claude/rules/credentials-service.md`, the plugin skills and the how-tos are rewritten to these rules, with a short CLAUDE.md pointer to this folder. |
| D3448 | version-stamp | Tension T1 | **Not the recommendation:** accept the window. A launch from CLI A before the project's update to A finishes meets an old image that refuses the stamped task. Runs already in flight are untouched, because they parsed their task before the stamp. **Amended after review:** the old runner fails with its own parse error, not a line naming `fugaro update`, because old code cannot be taught a new message. Inside the same window, a teammate's CLI older than A also fails to read a stamped task (F34). Once the update finishes, `required_cli` refuses those CLIs before they read. |
| D3449 | version-stamp | Development builds | **Resolution.** A runner or a stamp that is a development build (`dev`, or no release version) never upgrades. A runner newer than the stamp never downgrades. |
| D3450 | deletion-gating | A recreatable delete with a run in flight | **Resolution (second review).** Under the HARD RULE, the gate and the guard defer the delete of a job, or of a service account a job runs as, while an execution of that job is in flight. They name it in one line and leave it for a later update, as whole-root retirement already does (F42). |
| D3451 | update-only | The per-repository Terraform | **Resolution (second review).** `roots/repo` and `modules/repo` are deleted one release after the release that carries UO2, so an installation one release behind still retires its roots (UO6). |
| D3452 | deletion-gating | The audit record's `delete_list_generation` | **Resolution (second review).** The field stays and is written as 0, which already means "no list consulted"; removing it would bump the audit format's version for nothing. |

D3453–D3459 are reserved and unused.

## Open tensions

- **T1. Settled by D3448.** The owner chose an eager rebuild with an always-written stamp, which leaves a
  window in which new launches fail. The alternative that kept the window closed was declined.

## Open items

- **O1.** Confirm D3424 as amended after review: `setup --provision-batch` stays the first provisioning
  of the compute root.
- **O2.** Confirm D3448 as amended after review: the window's failure is the old runner's own parse
  error, and older CLIs fail to read stamped tasks inside the same window.
- **O3.** Confirm D3425's second amendment: every `shapesTheInstallation` flag except `--registry-cleanup`
  and `--scheduler-region` stays refused to agents, `--base` and `--github-app-id` included, and
  `config publish --executable-changes` stays a person's.

## UNVERIFIED

- **U1.** Release archives on GitHub can be downloaded without credentials from inside a job. The
  repository's releases feed the public brew tap, which suggests they are public. Check 951 settles it.
- **U2. Settled after review.** Fugaro already sets a run's task timeout as an execution override
  (`backend/gcp/run.go`, `Overrides.Timeout`), so a per-run timeout needs nothing new.
- **U3.** Whether a Fugaro binary newer than an image's pinned Claude Code, `gh` and `mise` stays
  compatible with them. The epoch rule (D3446) assumes that any such incompatibility comes with a base
  image change. Check 951 runs one upgrade across releases.

## §A: agent refusals today, and D3425's decision on each

| Command or flag | Reason in the code | D3425 |
| --- | --- | --- |
| `update` stages outside the gate (`initflow/loop.go` `agentStage`) | `AgentRefusal` | **Lift.** Every stage runs through the reclassified gate (D3427); the secrets stage skips with a named line |
| `update --onboard-repo`, `--publish-config`, `--coordinator` | "writes to the cloud" | **Lift** |
| `update` with an installation-shaping flag (`shapesTheInstallation`) | the flags change what the installation is | **Keep** for budget, billing, operator and admin lists, `--managed`, `--create-project`, `--state-bucket`, `--allow-delete` and the image-source flags (D3425 as amended); **lift** for the rest |
| `--retire`, automatic retirement (`init_retire.go`) | needs a person's terminal | **Goes** (D3421, D3431): retirement is automatic for everyone |
| Budget self-flag (`update_selfflag.go`) | the marks are the owner's | **Goes** with #4045 (D3426) |
| `config publish`, `project policy publish` | "changes what every repository runs" | **Lift**; `--delete` stays a person's (it is irreplaceable) |
| `image refresh`, `image add`, `image map` | cloud changes | **Lift** (AG4b) |
| `config publish --executable-changes` | publishes command or image-step changes for every repository | **Keep** (D3425 as amended again; O3) |
| `image check`'s job submission | a billable build | **Lift** (AG5d) |
| `update`'s orchestrator and checkout-upgrade stages | their own `AgentRefusal` | **Lift** (AG2b) |
| `workspace state` save | a workspace admin's act | **Keep** (membership) |
| `settings set`/`unset` beyond `settingsManagedKeys` | the registry | **Lift**, except keys that raise a budget or widen `allow_data_to` (D3425 as amended) |
| `rigor set` lowering, `rigor floor`, `rigor security rm` | the sensitive direction (a raise is already admitted) | **Lift** |
| `accounts add` (token) | a token is a person's | **Keep** (secret value) |
| `accounts rm` | deletes a credential | **Keep** (irreplaceable) |
| `accounts switch`, `alias`, `pool` | "which credential spends" | **Lift** |
| `secrets set`/`rm`, `creds`, owner probe, secrets intake | secret values, deletes | **Keep** |
| `managed flag`/`unflag` | the mark is the owner's | **Keep** |
| `allow-delete` | approval of deletes | **Goes** (D3427) |
| `restore` | creates a project on billing | **Keep** |
| `user add/rm/grant/offboard`, `workspace` admin commands | membership and IAM | **Keep** |
| `project repos add/rm`, `project sync` | the allow-list, jobs | **Lift** |
| `budget set` raise, `budget resume` | D1542 | **Keep** |
| `recipes publish/rm`, `runs reindex`, `issue sync/dismiss`, `orchestrator patch/install`, `stats runs` | "a person's" | **Lift** |
| `issue submit --ask` | `--ask` | **Keep** (`--ask` is never an agent's, D3428) |
| `apply`, `managed apply` | `AgentRefusal` | **Lift**, through the reclassified gate |
| `orchestrator install` | writes a login unit on the person's machine | **Lift**, as answered; the session already runs code on that machine |
| `image build`, the cloud half of `upgrade` | cloud changes | **Lift** |
| `issue list/show/mine`, `runs digest`, `followups` | a person's reads | **Lift** |
| `budget kill --workspace` | a workspace act | **Lift** (a stop); `budget resume` stays kept |
| `--check-github-app` | reads the App key | **Keep** (secret value) |
| `licence set/login/refresh/renew` | a key is a person's | **Keep** |
| `setup` creating a project | billing | **Keep** |
