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

**`checkout: clone` is not shipped yet.** `fugaro init --repo` and `fugaro image refresh` refuse, before any cloud call, to deploy or move a `checkout: clone` workflow's job (the `cloneShipped` gate in `internal/cli/recipes_skew.go`), so no deployed job is a clone job today and the switch recipe below cannot be completed. Everything in this section is what the option does when it ships; the live check in "Not verified yet" is what has to pass first.

`checkout: clone` means **no per-repository image at all**. The workflow's job runs the installation's own base image (`base_images.base`) directly, so there is no build and no daily image check for that workflow. What the run pays instead is at start-up:

- a **blobless clone** of your repository (`git clone --filter=blob:none --no-checkout --single-branch`): the commits and trees, not the file contents, which are fetched as the checkout of the task's ref needs them. It is never a shallow clone: checkpoints, follow-ups and `merge-base` need the whole history;
- **`mise install`**, when the checkout has a root mise config (`mise.toml`, `.mise.toml`, `mise/config.toml`, `.mise/config.toml`, `.config/mise.toml`, `.config/mise/config.toml`, `.tool-versions`, or a `conf.d` fragment in `mise/conf.d`, `.mise/conf.d` or `.config/mise/conf.d`). It runs as the same user as the image build, from the checkout, and then checks `mise ls --current --missing` exactly as the build does, failing the run if a declared tool is still missing. It does *not* inherit the run's whole environment: the model credentials, the gateway's provider keys, and every variable Fugaro's own — including the git-provider credentials it clones and pushes with (`FUGARO_GITHUB_APP_PRIVATE_KEY`/`_FILE`, `FUGARO_GITHUB_APP_ID`, `FUGARO_BITBUCKET_TOKEN`) — are removed, because a tool's install script is code your repository chooses; and `GITHUB_TOKEN` is set only for a first run or a review run of a GitHub repository, never for a follow-up — which runs the pull request branch's own mise config. That is not a hard boundary, and does not pretend to be: mise runs as the runner's own user, so a config that means to can still read the runner process's environment (`/proc/<pid>/environ`) or the metadata server, the same accepted residual the agent has (design `m9-budget-and-dashboard.md`, D2). What it does is keep the credentials out of the tool's environment and out of what the tool forgets to handle carefully. Its time is recorded as its own stage, `provision`, in `result.json` and in the dashboard, and it is bounded at ten minutes.

Choose `clone` when the repository needs no system package and no service, and has no Dockerfile: a plain Node, Go or Python service whose only requirement is what `mise.toml` declares. Then nothing is built per repository, and a weekly dependency change costs no rebuild. Otherwise stay on `baked`.

### What `clone` refuses

A clone job has no build at all, and a run has no root, so Fugaro cannot install a system package at start-up. `fugaro validate` (and the same checks when a run reads your `fugaro.yaml`) refuses these keys under `checkout: clone`, each message naming `checkout: baked` as the way out:

- `image.apt`, `image.tools`, `image.setup`, `image.skip_build_scripts`
- `dockerfile:`

Move the workflow back to `baked` if it needs any of them.

### Switching a workflow to `clone`

Once the option ships:

1. Set `checkout: clone` on the workflow in `fugaro.yaml`, commit it, and merge it to your base branch.
2. In your own terminal, run `fugaro init --repo .` in the checkout. That redeploys the workflow's job onto the installation's base image and sets `FUGARO_CHECKOUT=clone` in it, which is what tells the run to make its own clone. (Today this step is refused: see the note at the top of this section.)
3. Then run the workflow as usual.

Order matters: `fugaro image refresh` only moves a job that is already a clone job (it carries the `FUGARO_CHECKOUT` it must keep); a job deployed from a `baked` `fugaro.yaml` is refused, with `fugaro init --repo` as the fix. `fugaro run` decides `baked` or `clone` for a launch from the `fugaro.yaml` of the checkout you run it in, so run it in the repository's checkout.

### What is and is not in the checkout

The blobless clone holds the file contents of the revisions it has needed and no others: the one the run checked out (the task's ref for a first run, the pull request's branch for a follow-up, the base branch and then each reviewed head for a review run) plus what the runner fetched for the branch's diff. Everything else is fetched on demand, through `origin`.

That is a problem for the agent, which deliberately has no git credentials of its own: on a **private** repository its git cannot fetch anything, so an on-demand fetch fails (`could not fetch <oid> from promisor remote`). The runner therefore brings in what the prompts point the agent at, before any stage starts: it runs the review's own `git diff origin/<base>...HEAD` itself, with the run's credentials, so the base's side of every file the branch changed is already there. The branch's diff, the files as they stand at `HEAD`, and everything the run's own git does (checkpoints, pushes, the diff stat in a follow-up's prompt) are covered, in every mode.

**Deep history is not.** `git log -p`, `git blame`, and `git show <old-revision>:<path>` need the file contents of revisions nobody checked out and nobody prefetched, so on a private repository they fail under `clone` where they work under `baked`. The same is true of any file content outside the branch's own diff. Treat `clone` as a checkout for the branch you are working on, not as a full local mirror; `baked` is the option that has every object locally.

The dependency warm-up is the agent's job (or your `commands.build`).

## Not verified yet

Two things are still unmeasured, and both are in the live checklist's clone check (sandbox only, run by the maintainer):

- that a Cloud Run job's service agent may pull the installation's base image for a workflow job (today only builds and the check job do);
- what `mise install` actually costs at every start-up, which is the price of `clone` over `baked`.

Until that check passes, treat `clone` as the newer of the two options. `baked` is what every existing workflow, the setup skill and `fugaro image refresh` assume.

## Known gaps

Three smaller gaps are carried from this feature's review and are **not** closed yet. None of them changes what a `baked` workflow does today:

- **`fugaro run --repo <owner/name>` from outside the checkout.** `fugaro run` reads `checkout` from the `fugaro.yaml` of the checkout it runs in, not from the job it is about to run, so a clone workflow launched from elsewhere is judged as a baked one. Closing it means falling back to the deployed job's own `FUGARO_CHECKOUT` (or to its image being `base_images.base`); until then, run `fugaro run` in the repository's checkout. (A clone job cannot be deployed at all yet — see the top of the `clone` section — so this gap is not reachable today.)
- **The base-image freshness check reads your local config.** `fugaro run` compares `base_images.base` as *your* `fugaro.yaml` names it, not the image the deployed job actually runs, so a job whose image drifted (a refresh that has not been run, a hand-edited job) is not caught.
- **A clone-only repository still gets its own artifact registry**, plus the build account's writer grant, even though nothing is ever built into it. It is inert, and dropping it is a Terraform change (`deploy/terraform/gcp/modules/repo`) that has not been made.
