# Where the code comes from: `checkout: baked` or `checkout: clone`

Every workflow runs in a Cloud Run job. That job needs the repository in it before the agent starts, and `workflows.<name>.checkout` picks how it gets there. The key is per workflow, never per task: a task cannot choose its job's image.

```yaml
workflows:
  api:
    checkout: baked   # the default; this line may be left out
    commands: { build: make, test: make test }
```

## `baked` (the default)

Fugaro builds a per-repository image (`fugaro image build`, and the daily image check rebuilds it when it goes stale) with a full clone of your repository at the branch it was built from baked into it. At start the run opens that checkout and moves it to the task's ref, which costs one incremental fetch. The checkout is never wrong, only older, so a stale image costs time, not correctness.

What a rebuild buys is warm caches: the runtimes from your `mise.toml`, the `image.setup` command's work, and whatever your build leaves behind. Most real repositories need something only a build can give them — a system package, a service, a Dockerfile — and every one of those needs `baked`.

## `clone`

**`checkout: clone` is shipped** (fugaro 0.9.0). `fugaro update --repo` deploys a clone workflow's job onto the installation's base image (`base_images.base`) and `fugaro image refresh` moves it, the same as for any other workflow: the gate that refused both until the option was released (the `cloneShipped` flag in `internal/cli/recipes_skew.go`) is open. Two things the launch still checks: the job image's own release has to be **fugaro 0.7.5 or later** (`cloneSince` — the release whose runner reads `FUGARO_CHECKOUT` and makes the blobless clone), and a job image with no release tag at all (a dev build, a hand-pushed image) is only warned about. Live Check 38 of [gcp-live-checklist.md](gcp-live-checklist.md) is what proved the option end to end on the installation's own release base image; that run is the reason the gate is open. Its own record lags, though — the checklist's Check 38 row still reads **USER-RUN, NOT RUN** and its section still describes the hand-flipped build the run needed before the flip, because writing the run's FACT lines there is the owner's step, not this doc's. The gate is open; the checklist file has not been updated to say so.

`checkout: clone` means **no per-repository image at all**. The workflow's job runs the installation's own base image (`base_images.base`) directly, so there is no build and no daily image check for that workflow. What the run pays instead is at start-up:

- a **blobless clone** of your repository (`git clone --filter=blob:none --no-checkout --single-branch`): the commits and trees, not the file contents, which are fetched as the checkout of the task's ref needs them. It is never a shallow clone: checkpoints, follow-ups and `merge-base` need the whole history;
- **`mise install`**, when the checkout has a root mise config (`mise.toml`, `.mise.toml`, `mise/config.toml`, `.mise/config.toml`, `.config/mise.toml`, `.config/mise/config.toml`, `.tool-versions`, or a `conf.d` fragment in `mise/conf.d`, `.mise/conf.d` or `.config/mise/conf.d`). It runs as the same user as the image build, from the checkout, and then checks `mise ls --current --missing` exactly as the build does, failing the run if a declared tool is still missing. It does *not* inherit the run's whole environment: the model credentials, the gateway's provider keys, and every variable Fugaro's own — including the git-provider credentials it clones and pushes with (`FUGARO_GITHUB_APP_PRIVATE_KEY`/`_FILE`, `FUGARO_GITHUB_APP_ID`, `FUGARO_BITBUCKET_TOKEN`) — are removed, because a tool's install script is code your repository chooses; and `GITHUB_TOKEN` is set only for a first run or a review run of a GitHub repository, never for a follow-up — which runs the pull request branch's own mise config. That is not a hard boundary, and does not pretend to be: mise runs as the runner's own user, so a config that means to can still read the runner process's environment (`/proc/<pid>/environ`) or the metadata server, the same accepted residual the agent has (design `m9-budget-and-dashboard.md`, D2). What it does is keep the credentials out of the tool's environment and out of what the tool forgets to handle carefully. Its time is recorded as its own stage, `provision`, in `result.json` and in the dashboard, and it is bounded at ten minutes.

Choose `clone` when the repository needs no system package and no service, and has no Dockerfile: a plain Node, Go or Python service whose only requirement is what `mise.toml` declares. Then nothing is built per repository, and a weekly dependency change costs no rebuild. Otherwise stay on `baked`.

### What `clone` refuses

A clone job has no build at all, and a run has no root, so Fugaro cannot install a system package at start-up. `fugaro validate` (and the same checks when a run reads your `fugaro.yaml`) refuses these keys under `checkout: clone`, each message naming `checkout: baked` as the way out:

- `image.apt`, `image.tools`, `image.setup`, `image.skip_build_scripts`, `image.key_files`
- `dockerfile:`

Move the workflow back to `baked` if it needs any of them.

### Switching a workflow to `clone`

1. Set `checkout: clone` on the workflow in `fugaro.yaml`, commit it, and merge it to your base branch.
2. In your own terminal, run `fugaro update --repo .` in the checkout. That redeploys the workflow's job onto the installation's base image and sets `FUGARO_CHECKOUT=clone` in it, which is what tells the run to make its own clone.
3. Then run the workflow as usual.

Order matters: `fugaro image refresh` only moves a job that is already a clone job (it carries the `FUGARO_CHECKOUT` it must keep); a job deployed from a `baked` `fugaro.yaml` is refused, with `fugaro update --repo` as the fix. `fugaro run` decides `baked` or `clone` for a launch from the `fugaro.yaml` of the checkout you run it in, so run it in the repository's checkout.

### What is and is not in the checkout

The blobless clone holds the file contents of the revisions it has needed and no others: the one the run checked out (the task's ref for a first run, the pull request's branch for a follow-up, the base branch and then each reviewed head for a review run) plus what the runner fetched before the agent started. Everything else is fetched on demand, through `origin`.

An on-demand fetch goes to `origin` with whatever credentials the caller's git has, and the agent has none of its own in the two modes that read someone else's work — a **follow-up** and a **review run** (the agent environment is an allowlist, and the git credentials are not on it). On a **private** repository, then, a read of file contents nobody fetched fails with `could not fetch <oid> from promisor remote`. (A first run's agent is given the run's git credentials, so it fetches what it needs for itself.)

So the runner fetches, before any stage starts and with the credentials it holds, the file contents the agent is pointed at:

- **the branch's diff against the base**: `git diff origin/<base>...HEAD`, the command the review prompt names — its base side, at the merge base, is what no checkout of the branch brings in. `<base>` is the run's own base of work, not the repository's default branch: the ref a first run was cut from (`--ref`), the base branch the pull request targets *now* for a follow-up (read from the provider, so a retargeted pull request prefetches its new base), and each reviewed pull request's own base for a review run (`internal/runner/diffbase.go`, `diffBase`). A run started from another branch on a repository whose `git.base_branch` is still `main` therefore fetches — and reviews — the branch it was cut from, not the whole of `main`'s divergence from it;
- **the branch's own commits**: `git log -p origin/<base>..HEAD`, `git show <a commit of the branch>`, `git diff HEAD~1`. Reading a pull request commit by commit is the other thing a reviewer does, and an earlier revision of a file the branch changed again is not in the branch's tip;
- **the diff against the base tip**: `git diff origin/<base>`, for when the base branch has moved on since the merge base.

The base ref itself is fetched as well, before those three, whenever it is not one bootstrap already brought in: a clone job's clone carries the remote's default branch alone (`--single-branch`), so a run started from another branch, a follow-up whose pull request was retargeted, and a review run reviewing a pull request that targets any other branch all name an `origin/<base>` that only that fetch creates — without it the prompt's range, the review input and the reviewer's own `git diff origin/<base>...HEAD` resolve to nothing (`internal/runner/diffbase.go`, `fetchDiffBases`).

What is still not there: `git blame`, and `git show <old-revision>:<path>` for a revision nobody fetched — the file contents of revisions outside those three sets (an earlier revision of a file the branch's own commits did not touch, another branch, the rest of the base branch's history). On a private repository those fail under `clone` where they work under `baked`. Treat `clone` as a checkout for the branch you are working on, not as a full local mirror; `baked` is the option that has every object locally.

The fetching costs a few more calls per run, and the two that read the pull request's own commits and its diff against the base grow with the pull request — the files it changes — rather than with the repository. The third, the diff against the base tip, also brings in the base's side of everything the base branch has changed since the merge base, so on a busy base branch it is the largest of the three.

The dependency warm-up is the agent's job (or your `commands.build`).

## What the live check measured

Check 38 of [gcp-live-checklist.md](gcp-live-checklist.md) is the option's live check (sandbox only, run by the maintainer), and it has run and passed. It answers the two questions nothing in this repository could:

- whether a Cloud Run job's service agent may pull the installation's base image for a workflow job — a `checkout: clone` job pulls `base_images.base` under the run job's own service account, where before only image builds and the daily check job pulled it;
- what `mise install` costs at every start-up, which is the price of `clone` over `baked`.

Its FACT lines are the record of both, in the checklist section. `clone` is still the newer of the two options: `baked` is what every workflow existing before it, the setup skill and `fugaro image refresh`'s rebuild path assume.

## `checkout: shared` arrives in 0.15.0

A third kind, `checkout: shared`, is in `fugaro.yaml`'s schema and in the config format: the workflow's job runs a shared dependency-layer image — one image per resolved dependency set, holding no repository's own source — and the run clones your repository at launch, exactly as `clone` does. **No release builds or refreshes such an image before fugaro 0.15.0**, so `fugaro validate` refuses the key until then (`shared images arrive in 0.15.0`, at `workflows.<name>.checkout`) and a run of such a workflow does not start. If you have already written it, use `checkout: baked` or `checkout: clone` for now: a 0.14.0 CLI stops your runs, and 0.15.0 is the release that lifts the refusal and runs the kind.

## Known gaps

Three smaller gaps are carried from this feature's review and are **not** closed yet. None of them changes what a `baked` workflow does today:

- **`fugaro run --repo <owner/name>` from outside the checkout is refused** once the project has published profiles. `fugaro run` reads `checkout` from the `fugaro.yaml` of the checkout it runs in, not from the job it is about to run, so from elsewhere it cannot tell a clone workflow from a baked one and will not guess which job the launch belongs on: it stops and names both causes (the working directory is not the repository's checkout, or its `fugaro.yaml` could not be read). An installation that has published no profiles is untouched — there the per-repository job is the only job there is. Closing the gap properly means falling back to the deployed job's own `FUGARO_CHECKOUT` (or to its image being `base_images.base`), which is what would let a launch from elsewhere decide; until then, run `fugaro run` in the repository's checkout. **This gap is reachable now that `checkout: clone` has shipped**: until the release that carried the flag kept clone jobs from existing at all, and it no longer does.
- **The base-image freshness check reads your local config.** `fugaro run` compares `base_images.base` as *your* `fugaro.yaml` names it, not the image the deployed job actually runs, so a job whose image drifted (a refresh that has not been run, a hand-edited job) is not caught.
- **A clone-only repository still gets its own artifact registry**, plus the build account's writer grant, even though nothing is ever built into it. It is inert, and dropping it is a Terraform change (`deploy/terraform/gcp/modules/repo`) that has not been made.
