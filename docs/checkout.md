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

`checkout: clone` means **no per-repository image at all**. The workflow's job runs the installation's own base image (`base_images.base`) directly, so there is no build, no registry per repository and no daily image check for that workflow. What the run pays instead is at start-up:

- a **blobless clone** of your repository (`git clone --filter=blob:none --single-branch`): the commits and trees, not the file contents, which are fetched as the checkout of the task's ref needs them. It is never a shallow clone: checkpoints, follow-ups and `merge-base` need the whole history;
- **`mise install`**, when the checkout has a root mise config (`mise.toml`, `.mise.toml`, `mise/config.toml`, `.config/mise/config.toml`, `.tool-versions`, or a `conf.d` fragment), run as the same user and with the same settings as the image build. Its time is recorded as its own stage, `provision`, in `result.json` and in the dashboard.

Choose `clone` when the repository needs no system package and no service, and has no Dockerfile: a plain Node, Go or Python service whose only requirement is what `mise.toml` declares. Then nothing is built per repository, and a weekly dependency change costs no rebuild. Otherwise stay on `baked`.

### What `clone` refuses

A clone job has no build at all, and a run has no root, so Fugaro cannot install a system package at start-up. `fugaro validate` (and the same checks when a run reads your `fugaro.yaml`) refuses these keys under `checkout: clone`, each message naming `checkout: baked` as the way out:

- `image.apt`, `image.tools`, `image.setup`, `image.skip_build_scripts`
- `dockerfile:`

Move the workflow back to `baked` if it needs any of them.

### Switching a workflow to `clone`

1. Set `checkout: clone` on the workflow in `fugaro.yaml`, commit it, and merge it to your base branch.
2. In your own terminal, run `fugaro init --repo .` in the checkout. That redeploys the workflow's job onto the installation's base image and sets `FUGARO_CHECKOUT=clone` in it, which is what tells the run to make its own clone.
3. Then run the workflow as usual.

Order matters: `fugaro image refresh` only moves a job that is already a clone job (it carries the `FUGARO_CHECKOUT` it must keep); a job deployed from a `baked` `fugaro.yaml` is refused, with `fugaro init --repo` as the fix. `fugaro run --repo <owner/name>` decides from the job it is about to run, so launching from outside the checkout behaves the same as launching inside it.

Each task's diff against the base branch is the same; review mode, follow-ups and checkpoints work the same way. The dependency warm-up is the agent's job (or your `commands.build`).

## Not verified yet

Two things are still unmeasured, and both are in the live checklist's clone check (sandbox only, run by the maintainer):

- that a Cloud Run job's service agent may pull the installation's base image for a workflow job (today only builds and the check job do);
- what `mise install` actually costs at every start-up, which is the price of `clone` over `baked`.

Until that check passes, treat `clone` as the newer of the two options. `baked` is what every existing workflow, the setup skill and `fugaro image refresh` assume.
