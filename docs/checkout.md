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

**What is in the image is a cache.** It is a head start — the repository's objects, and whatever warmed the runtimes — and it is never a permission: what a run may reach is decided by the credential layer, and which job it lands in by the profile its workflow selects, not by `checkout` (D2304). What still ties a baked workflow to its own job on this tree is not access but tools: a shared project job runs the image its profile was deployed with, and the keys a baked workflow declares — `image.apt`, `image.tools`, `image.key_files`, `image.setup`, `dockerfile` — live in the repository's own image and nowhere else, so a baked workflow keeps the job it has always had until that image is the project's catalog entry (D2305).

What a rebuild buys is warm caches: the runtimes from your `mise.toml`, the `image.setup` command's work, and whatever your build leaves behind. Most real repositories need something only a build can give them — a system package, a service, a Dockerfile — and every one of those needs `baked`.

### When the baked copy is unusable

What the run works on is the checkout the job's image holds, and the run decides it from that tree itself, never from a value in the job's environment. The tree is this run's when its `origin` is this repository's — a `pushurl`, a URL rewrite or a remote named after another URL is refused too, since a push is what would carry the branch away — and it works there after the one fetch above.

With no tree at all, with another repository's, with one it cannot read, or with one whose fetch or checkout fails, the run does not stop: it logs one line, removes the tree before the agent starts, and clones this repository into a fresh directory — the blobless clone and the `mise install` [below](#clone), whatever the workflow's `checkout` says. A tree that is not this repository's is never readable by the agent.

`FUGARO_CHECKOUT`, which a job deployed by an older fugaro carries, is **ignored**: a launcher-writable value must not decide where the run's code comes from. Whether an image bakes a repository is the image build's business (`bake_repos`) and not the run's.

**None of this is a list of steps to run by hand.** Enrolling a repository in the project — the checkout's `fugaro.yaml` read, this machine's `repos:` entry and its repo-to-image mapping, its entry in the published allow-list, the policy publish that carries it, and the repository's own job set deployed from the checkout with its `run.invoker` grant — is one command, `fugaro project repos add <owner/name>`, behind one typed confirmation. It is resumable: over a repository already enrolled it writes nothing and says so per step, and over an enrolment a failure left half done it does the missing steps and leaves the finished ones alone. `fugaro project repos status <owner/name>` is the doctor that says whether every fact deciding a launch is in place and names the one command each failing row wants, and `fugaro project repos rm <owner/name>` is the inverse — it takes the enrolment away and keeps the repository's Terraform state and its root's own IAM members, naming both what it removes and what it leaves.

## `clone`

**`checkout: clone` is shipped** (fugaro 0.9.0). `fugaro update --repo` deploys a clone workflow's job onto the installation's base image (`base_images.base`), and it is the only command that moves one: every command that would build or refresh a repository's own image — `fugaro image build --local` and `--workflow`, and `fugaro image refresh` — answers `repo-level images are disabled; the project's catalog image is used` and moves nothing, the same as for any other workflow (D1900, IS2). Building a catalog entry is the part of `fugaro image build` that still works. The gate that refused a clone workflow until the option was released (the `cloneShipped` flag in `internal/cli/recipes_skew.go`) is open. Two things the launch still checks: the job image's own release has to be **fugaro 0.7.5 or later** (`cloneSince` — the release whose runner began making the blobless clone), and a job image with no release tag at all (a dev build, a hand-pushed image) is only warned about. Live Check 38 of [gcp-live-checklist.md](gcp-live-checklist.md) is what proved the option end to end on the installation's own release base image; that run is the reason the gate is open. Its own record lags, though — the checklist's Check 38 row still reads **USER-RUN, NOT RUN** and its section still describes the hand-flipped build the run needed before the flip, because writing the run's FACT lines there is the owner's step, not this doc's. The gate is open; the checklist file has not been updated to say so.

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
2. In your own terminal, run `fugaro update --repo .` in the checkout. That redeploys the workflow's job onto the installation's base image — which holds no checkout, so the run makes its own clone — and sets `FUGARO_CHECKOUT=clone` in it, a mark no fugaro reads any more (D3440; `fugaro update` drops it from the job in a later release).
3. Then run the workflow as usual.

Order no longer matters: `fugaro update --repo` is the only command that deploys a workflow's job, and it deploys whatever kind the checkout's `fugaro.yaml` names when it runs, so a workflow switched back to `baked` is moved by the next update in that checkout exactly as one switched to `clone` is. `fugaro image refresh` used to be the order-sensitive mover — it moved only a job that already carried the `FUGARO_CHECKOUT` it had to keep, and refused a job deployed from a `baked` file — and it now answers `repo-level images are disabled; the project's catalog image is used` and moves nothing, as do the arms of `fugaro image build` that would build a repository's own image. `fugaro run` decides `baked` or `clone` for a launch from the repository's `fugaro.yaml` at the ref the run checks out: run it in the repository's checkout and that file is read out of the checkout with no credential at all, while a launch from outside any checkout of the repository gets it another way ([below](#a-launch-with-no-checkout-of-the-repository)).

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

## `checkout: shared` has no released image yet

A third kind, `checkout: shared`, is in `fugaro.yaml`'s schema and in the config format: the workflow's job runs a shared dependency-layer image — one image per resolved dependency set, holding no repository's own source — and the run clones your repository at launch, exactly as `clone` does. **No released fugaro builds or refreshes such an image yet** — 0.15.0 was the release it was to arrive in, and it shipped without it — so `fugaro validate` refuses the key (`no released fugaro builds or refreshes a shared dependency-layer image yet`, at `workflows.<name>.checkout`) and a run of such a workflow does not start: use `checkout: baked` or `checkout: clone` meanwhile.

## A launch with no checkout of the repository

A launch's job is decided from the repository's own `fugaro.yaml` at the ref the run checks out: `workflows.<name>.checkout` says whether the workflow is a `clone` one, and the profile it selects says which shared project job it belongs on. The profile, `job_profile`, is in the repository's file and in no job's environment; the job's own `FUGARO_CHECKOUT` mark says nothing, since no run reads it any more ([above](#when-the-baked-copy-is-unusable)). Run `fugaro run` in the repository's checkout and that file is read out of the checkout, with no credential at all. A launch from a machine that holds no checkout of the repository gets the same file another way — the console's launches always run in the app's own empty directory, and so does `fugaro run --repo …` typed anywhere else — and the order below is the whole of it:

- **A run being continued reuses the job that run recorded.** `fugaro run --retry` and `--pr N` re-launch the workflow of the run they continue, so the job they target is the one recorded in that run's `launch.json`, when the project's published policy computes that name as a shared job. The record decides the *job* and nothing else: the launch's own gates still read the ref's `fugaro.yaml` — the workflow's kind and its image are judged from it — so a continuation outside a checkout reads that document and meets the same no-service refusal a first launch does.
- **Otherwise the ref's `fugaro.yaml` is read through the installation's credentials service**, as you, the operator: on that one path the service mints a read-only token for that repository and the launcher reads that one file at the ref the run checks out. The CLI holds no provider credential of its own for it, no App key and no environment variable ([git-providers.md](git-providers.md)), and the read is refused in a coding agent's session, as every read made with your own credential is. An installation with no credentials service has nothing to make the read with, so its launches outside a checkout refuse and name the checkout — the limit stated plainly, with the checkout as the way out.
- **A policy that defines no profile for the repository's provider, or a profile that does not admit the repository, proceeds on the per-repository job**, which is the same job the checkout path gives for the same repository. A provider no shared job serves is no longer part of that — the shared job takes Bitbucket beside GitHub — so what leaves a launch here is the published policy rather than the provider, and there is no shared job for the document to decide anything about.
- **This is not a fallback a Bitbucket launch gets**, once the project has published a profile. The read above is GitHub's — its mint is signed as the repository's App, and the launcher takes that ID from `repos.<owner/name>.github_app_id` — so a Bitbucket repository, whose local entry records none, is refused before anything is read, naming the missing value and the repository's checkout, as an installation with no credentials service is. That refusal is such a launch's outcome today; the per-repository job it ran on before docs/shared-jobs-default's I1 admitted Bitbucket to the list a shared job serves is a behaviour change, not a fallback that is there now.

Falling back to the deployed job's own `FUGARO_CHECKOUT` — or to its image being `base_images.base` — cannot close this: the mark says nothing at all, and the image says only whether a checkout is baked into it, while the workflow's `job_profile` is in the repository's file and nowhere in the job's environment.

Everything else is **refused**, naming the cause and the checkout as the way out. A read that fails is not a reason to guess: a run placed on a job the project's policy did not choose carries no profile, and nothing downstream catches it. An installation that has published no profiles is untouched — there the per-repository job is the only job there is — and so is a ref whose tree carries no `fugaro.yaml`: such a repository selects no clone workflow and no profile.

## Known gaps

Two smaller gaps are carried from this feature's review and are **not** closed yet. Neither of them changes what a `baked` workflow does today:

- **The base-image freshness check reads your local config.** `fugaro run` compares `base_images.base` as *your* `fugaro.yaml` names it, not the image the deployed job actually runs, so a job whose image drifted (an update that has not been run, a hand-edited job) is not caught.
- **A clone-only repository still gets its own artifact registry**, plus the build account's writer grant, even though nothing is ever built into it. It is inert, and dropping it is a Terraform change (`deploy/terraform/gcp/modules/repo`) that has not been made.
