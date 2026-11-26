# Fugaro Update-From-Anywhere Spec

Owner: Dimi Paun
Status: draft for the owner's review, 2026-11-23

Lift L5 of the repo-coupling audit (`docs/repo-coupling-audit/report.md` §7). Rows RC12, RC13, RC38,
RC39, RC75, RC98, RC104, RC105; reconciliation F6, F20, F21. Wave 1, with L1 (version stamp) and
L8 (benign rulings). Code claims checked against `origin/main` at `b8fdb53e2`.

## Purpose

One admin runs `fugaro update` once and the project is upgraded. Today the result depends on the
directory it runs in. Check 891 on project `fugaro` showed it: from an empty directory the
`secrets`, `repository` and `checkout` stages skip; inside each checkout they act on that checkout's
repository only. Two steps after the stage loop (the `budget.mode` removal and the backlog fold)
do the same, and Check 891 cannot see them. This spec makes every stage and step do the same work
from any directory.

## Guiding principles (D3239, 2026-11-23)

- **The directory is a hint, never a scope.** Inside a checkout, `update` may take the project from
  that checkout's `fugaro.yaml`, so `--project` is not needed there. It may also put that
  repository first in the human output. Nothing else.
- **The work is the project's.** From any directory, the secrets, repository and checkout stages,
  the `budget.mode` removal and the backlog fold cover every enrolled repository of the project.
- **The default branch, not the working tree.** Each repository is read from its default branch on
  the code host. No working tree is read, and no local edit changes what `update` does.
- **Never a silent skip.** A repository `update` cannot read, or a value only a person can supply
  for it, gets one line naming the repository and the fix.
- **Unchanged rules.** `fugaro update` stays the whole upgrade (CLAUDE.md HARD RULE). It stays safe
  with runs in flight. It asks nothing without `--ask` (docs/no-prompt-policy/).

## Scope

Plain `fugaro update`, `update --check`, and the `gcp_project` anchor edit. Not in scope:
`update --repo` and the per-repository roots (L2, which L5 waits on), catalog entries built by
another repository's account (L3), credential records (L4), workflow-secret records and the
GitHub App (L6).

## The enrolled list

The enrolled repositories are the union of the local config's `repos:` and the concrete names in
the published policy's `allow_repos`. Patterns are not names. This is the list `fugaro project
repos ls` already draws (`repos_view.go` `reposViewRowNames`), so `update` and `repos ls` agree on
what "the project's repositories" means. A repository of the published list that this machine's
config lacks is still covered, so two admins' machines do the same work.

## Reading a default branch

**Choice: the code host's file API, no clone.** `update` opens each repository's provider the way
`fugaro image survey` does (`OpenProvider` in `api.go`, then `gitprov.ContentsReader`; directory
listings through `gitprov.TreeReader`). It reads at the default branch: `fugaro.yaml`, and
`.claude/settings.json` for the plugin pin. Until the fold retires, it also lists `docs/backlog/`.

- **Cost.** About three API calls per repository per run, one repository at a time. That is 150
  calls for 50 repositories, far below GitHub's hourly limit, and the plan declines a bound of
  concurrency for the read (§Considered and declined): it buys seconds and adds a second thing per
  repository that can fail. Reads are memoized per run, as `defaultBranchParse` is today
  (`checkoutParseCache`).
- **Why not a clone.** A clone of each repository costs disk, time and network for three small
  files. A remembered local checkout (`recordCheckout`) is not used either: it is as fresh as its
  last fetch and exists on one machine only, which brings the machine dependence back.

**A repository that cannot be read.** Causes are offline, no token for its provider, a deleted or
renamed repository (404), no access (403), no `fugaro.yaml` on the default branch, or a file that
does not validate. Each gives one line, `<owner/name>: unread: <why>; <fix>`. Examples of the fix:
the token variable to set (`FUGARO_GIT_TOKEN`, `GH_TOKEN`, `FUGARO_BITBUCKET_TOKEN`), or
`fugaro project repos rm <repo>` for a deleted repository. The rules for an unread repository:

- Its per-repository work is skipped, never guessed.
- Its stage reports needs-you.
- The update goes on for every other repository. One unread repository never fails the upgrade.
- A project-wide decision that needs every repository's file is held, never made on part of the
  list. This covers the default catalog entry (below).

## Stage by stage

| Stage or step | Reads the cwd today | Reads instead | The hint may |
| --- | --- | --- | --- |
| `secrets` (RC98) | `initsecrets.go` `secretsStage.resolve`: `gitRead` ".", `readOrigin`, `defaultBranchConfig` of that checkout; skips outside one | every enrolled repository's default-branch `fugaro.yaml`; the wanted names come from `infra.SecretMounts` per repository, as today | list that repository first |
| `repository` | `init_repo_target.go` `resolveRepoTarget`; refuses when the working tree differs from the default branch; `repoEngine(ctx, ".", …)` | after L2, the per-repository records for every enrolled repository, from its default branch; the working-tree comparison goes, since nothing reads the tree; a repository not enrolled is never onboarded by `update` (that is `project repos add`) | nothing |
| `checkout` (RC73, RC74) | `init_checkout_upgrade.go` `checkoutStage.checkout` (`pluginwire.Locate` "."), `cloudStep` over the working tree (`upgrade_cloud.go` `loadCheckoutResolved`); an origin it cannot read refuses the run | the derived-image refresh for every enrolled repository whose image is stale (open question 1); a repository whose view it cannot read, or whose origin is not `owner/name`, is one line for that repository and never fails the upgrade (U4, R10, R13) | nothing |
| `images` (RC75) | `init_images.go` `imagesStage.kinds`: `loadCheckoutConfigAt(ctx, ".")`; `init_images_catalog.go` `staleCatalog` splits stale entries "here" and "elsewhere" by the cwd's repository | the union of base kinds over every enrolled repository's workflows; stale entries listed with their building repository, with no "here" split (which builds `update` may submit is L3's) | nothing |
| `policy` (RC75) | `init_policy_migrate.go` `checkoutWorkflows` (the repository stage's target); `defaultCatalogEntryRefusal` holds the entry while `unvouchedRepos` names repositories it cannot read | every enrolled repository's workflows; the entry is written once all are read and none refuses; held, naming the unread ones, otherwise | nothing |
| `plugin` (RC38) | `init_plugin_stage.go` `locate` and `pin`: the cwd checkout's `.claude/settings.json` (`pluginwire.PlanRemoval`); the install half runs `claude` in that directory | install half: unchanged, run in a fixed directory; the pin: read from each enrolled repository's default branch, reported as a notice (below) | nothing |
| `budget.mode` removal (RC104) | `update_budget_off.go` `offerBudgetModeRemoval`: `gitRead` "." and the working-tree `fugaro.yaml`, offered as a diff | each enrolled repository's default-branch `fugaro.yaml`; a notice (below) | nothing |
| backlog fold (RC105) | `update_backlog.go` `foldBacklog`: `pluginwire.Locate` ".", then `docs/backlog/` | each enrolled repository's default-branch `docs/backlog/`, folded under that repository's name (open question 3) | nothing |
| anchor (RC13) | `init_fugaroyaml.go` `repositoryStage.anchor` writes `gcp_project:` into the cwd checkout; `init_anchor.go` `noteAnchor` hints at `--anchor` | moves to `fugaro project repos add` (below) | nothing |
| `update --check` (RC12) | `update_check.go` `runUpdateCheck`: `pluginwire.Locate(PATH)` | no PATH; the same per-repository read as the converge, with no writes | nothing |

**Secrets and the no-prompt policy.** Without `--ask`, a missing secret is never asked for. The
stage prints one line per repository, naming the secret and the command that stores it. That
command is the one the stage already builds (`initsecrets.go`): `fugaro secrets set <name> --repo
<owner/name>` for a repository-scoped name, or `fugaro secrets set <name>` for a project-wide one.
For the default Claude account, `claude setup-token` comes first. With `--ask`, at the owner's own
terminal, `update` asks for each value by repository name with a hidden prompt. It never asks in a
coding agent's session or under `--json`, and with no terminal it fails at once (D1540, D1544).
Each value is stored where `projectScope` decides today. A repository-level name that D2313
disabled is still dropped, with the ruling's sentence. The per-repository provider-key line (F21)
stays as it is, and its lift is L4's.

**The pin and `budget.mode: off` become notices, not diffs or pull requests.** `update` prints one
line per affected repository, naming the file and the exact edit. `doctor` reports the same
finding, with the same text, as a check with a fix. Why a notice:

- Today's diff is a write to whatever working tree the run stands in. That is exactly the
  directory dependence this lift removes.
- A pull request per repository needs a write credential to every repository, a branch and a PR
  flow that `update` does not have. It would also turn one upgrade into N reviews.
- Both values are dead. The pin has no writer since D949. `budget.mode: off` is already refused at
  launch with its own message (D3022), so a notice loses no protection.

CLAUDE.md's HARD RULE sentence about the pin ("offered its removal as a diff") and the update skill
change in the same pull request.

**The anchor moves to `fugaro project repos add`.** Enrolling a repository reads its default
branch. If the branch has no `gcp_project:` line, `repos add` prints the line to add and the image
checks `--anchor` runs today (`gcpProjectProblems`). Run inside that repository's checkout, it
offers the edit as a diff, as `writeAnchor` does now. `update` no longer writes or hints at the
anchor (`noteAnchor` goes). `update --anchor` goes too (open question 4).

## Output

The output follows the brew style. Each stage prints its `==>` header. Under it come one line per
repository that has something to say, and one summary line when none does:

```
==> Secrets
acme/app: done: stored 1 (git credential)
acme/web: needs you: <secret> is missing: run `fugaro secrets set <secret> --repo acme/web`
acme/old: unread: the repository is gone (404): `fugaro project repos rm github/acme/old`
```

The hint only moves the hinted repository's line to the top of each stage, in human output.
`--json` and `--events` never move with it: repositories are sorted by canonical name. A stage
event's `detail` is a summary that is the same from every directory (for example "3 repositories:
1 done, 1 needs you, 1 unread"). The per-repository lines go in a new member, `repos`, a list of
`{repo, state, detail}` (`setup_events.go`). This is an addition, so no version bump.

## Interactions

- **`--plan-only`.** All reads happen and no write does. Secrets name what is missing. The notices
  print. The fold prints how many items it would fold (today it returns early, so it says nothing).
- **`--json` / `--events`.** Same document from every directory, which is what Check 891 compares.
  No prompt under `--json`, `--ask` included.
- **Runs in flight.** L5 adds no new kind of write. Each per-repository write is one the
  per-checkout run already makes today, now made for every repository. The step-by-step analysis
  in `docs/design/update-in-flight.md` therefore holds per repository. Reading a default branch
  changes nothing a run holds.
- **Coding agent's session.** Each stage keeps its refusal of cloud writes. A secret is never
  asked for there, even with `--ask`.

## Acceptance criteria

1. Check 891 (`scripts/live-checks/check-891-update-trace.sh`) on `fugaro`, from an empty
   directory and from each of the three checkouts, prints PASS with no stage marked DIFFERS.
2. A test runs `update --plan-only --json --events` on a two-repository project (`gcpfake` plus the
   fake provider) from an empty directory, from each checkout, and from a checkout with a dirty
   `fugaro.yaml`. The event stream is byte-identical across all four.
3. From an empty directory, the secrets stage names every missing secret of both repositories, each
   with its `fugaro secrets set … --repo <owner/name>` line. With `--ask` and a fake terminal it
   asks for each by repository name. Under an agent marker or `--json` with `--ask`, it asks
   nothing.
4. A repository whose read fails (404, no token, invalid file) gets an `unread:` line naming it.
   Its stage is needs-you, every other repository is converged, and the default catalog entry is
   held while any repository is unread.
5. A default branch holding the plugin pin or `budget.mode: off` gives the same line from `update`
   and `doctor`. No working tree is written.
6. Backlog items on one repository's default branch are folded once under that repository, from
   any directory.
7. `TestUpdateLeavesARunInFlightRunning` passes on a two-repository project.
8. No code path of `fugaro update` calls `gitRead(ctx, ".", …)`, `pluginwire.Locate(".")` or
   `loadCheckoutConfigAt(ctx, ".")`, except to select the project when `--project` is absent. A
   source-scan test pins this.

## Open questions

1. **Derived images (RC73, RC74; D1900 stands).** Should `update` refresh every enrolled
   repository's stale derived image, or still only the checkout's? *Recommendation: every stale
   one.* Each build keeps today's confirmation rule, and the line names the repository. Refreshing
   only the checkout's breaks D3239. Refreshing none leaves an operator step. L9 makes this moot
   once a no-catalog project gets a default entry.
2. **Which credential reads the default branches.** *Recommendation: the shell's token, as
   `image survey` does.* An unread line names the variable to set. Reading the project's stored App
   key onto an admin's machine widens who holds it. A read through the credentials service is
   possible later.
3. **The backlog fold (RC105, Dead).** Fold from every default branch, or retire the step?
   *Recommendation: fold for one release through `TreeReader`, then delete it.* D2716 superseded
   the folder. A repository never updated from a checkout still has unfolded items.
4. **`update --anchor`.** Keep it as an explicit checkout command, or fold it into
   `project repos add`? *Recommendation: fold it in and remove the flag.* A per-checkout mode of
   `update` is the pattern this lift removes, and `repos add` is the per-repository door.
5. **RC39, the per-repository facts in the owner's local config** (`vertex`,
   `credentials_service_ready`, `provider_keys_served`, `stage_account_served`, `github_app_id`).
   Today the cwd repository's engine writes them. *Recommendation: L5 has the repository stage
   write them for every enrolled repository, and moving them to installation state is a follow-up
   task.* `workflows` is M2's, and `github_app_id` overlaps L6.
6. **`--ask` narrows today's value entry.** Today the secrets stage asks for a value at any real
   terminal, without `--ask` (value entry is not a confirmation, D1540). This spec asks only under
   `--ask`. *Recommendation: accept.* `update` then asks nothing by default, and the line it prints
   instead is a complete fix.
7. **Hint scope.** Should the hint also limit `--plan-only` output to the hinted repository?
   *Recommendation: no.* Output that changes with the directory is what Check 891 forbids.
