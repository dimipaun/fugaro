# Fugaro: Project-Level Shared Jobs as Default

**Status:** Urgent. Bitbucket projects currently have no usable job type.

## Problem

Right now Fugaro has two job models:

- **Repo-bound jobs.** These depend on repo-level images that have one repo baked in, plus a repo-level Bitbucket access key. Terraform creates a separate cloud job definition for each repo.
- **Shared jobs.** These are project-level and not tied to any one repo. Today they are GitHub-only, because they rely on GitHub app tokens that can be narrowed per job. They are also disabled as "untested".

We recently disabled repo-level images for new projects, on purpose. Because repo-bound jobs need repo-level images, new Bitbucket projects can't use repo-bound jobs either. Shared jobs are off for Bitbucket as well. The result is that **a new Bitbucket project has no jobs at all**.

## Root cause: two separate ideas got tied together

Baking a repo into an image was only ever meant to make jobs start faster: a `git fetch` on an existing checkout is quicker than a fresh clone. Somewhere along the way, baking got tied to what a job is allowed to access. These two things should be fully separate:

- **What's in the image** is a cache.
- **What a job is allowed to touch** is access control, and only the credential layer decides it.

## Decisions

1. **Shared, project-level jobs become the default and only active job model**, for both GitHub and Bitbucket.
2. **Repo-bound jobs are being retired, and repo-level images stay disabled.** Keep the code for now, but no new project can use them.
3. **Images are project-level.** A project image may bake in several repos as a cache. A handful of images can serve 50 to 100 repos. Rebuilding images after a Fugaro update happens per project, not per repo.
4. **Access is decided per job by the credential service:**
   - GitHub: a GitHub app installation token, narrowed to the repos the job needs.
   - Bitbucket: the credential service's Bitbucket proxy holds a workspace-wide token. It mints Fugaro-issued tokens narrowed to the job's repos and swaps in the real token on each request. Jobs never see the Bitbucket token.
5. **All Bitbucket traffic goes through the proxy.** That includes git clone, fetch and push, not only REST API calls. Set up the git credential helper or remote URL so git talks to the proxy.

## Image builds use the same credential path

An image build is treated as just another job:

- It asks the proxy (or the GitHub app) for a narrowed, **read-only**, short-lived token covering the repos it will bake in.
- The token expires when the build ends. **No token or credential file may remain in any image layer.** Add a check that scans the built image for leftover credentials (git config, `.netrc`, credential helper store, environment variables).
- This gives one credential path, one set of checks and one audit trail for jobs and builds alike.

## Runtime behaviour

- The repo, or set of repos, is a **parameter passed when the job runs**, not something Terraform defines.
- When a job starts: if the target repo is baked into the image, run `git fetch` through the proxy and check out the requested ref. If it isn't baked in (for example, a newly added repo), do a full clone through the proxy. That's slower but correct, and the repo gets picked up at the next image rebuild.

## Infrastructure (Terraform)

- Remove the per-repo job definitions from the default Terraform path. Terraform defines a small, fixed set of jobs per project (per image or profile), not one per repo.
- Adding or removing a repo is a config change only. No Terraform re-run is needed.

## Default profile migration

Shared jobs need a profile. In the next Fugaro release, `fugaro update` should:

- Create a **default shared-job profile for any project that doesn't have one**, so shared jobs work immediately without the user doing anything.
- Be **idempotent**: never overwrite or change an existing profile, including ones users have edited.
- Use narrow defaults: the project's image, and per-job access limited to the repos named in the task.
- Log or print what it created.

## Submodules and LFS

- Git submodules and Git LFS traffic also go through the proxy. Rewrite submodule URLs to the proxy, and point the LFS endpoint at it, so nested fetches use the same narrowed token.
- A submodule in a repo the job's token doesn't cover should fail with a clear message naming the repo that isn't covered.
- Image builds that bake in repos follow the same rules.

## Retiring repo-bound jobs

- Repo-bound jobs are being retired, not just disabled.
- `fugaro update` migrates existing projects: it creates the default shared-job profile, switches the project's jobs over to shared jobs, and makes sure a project-level image exists, building it if needed.
- Once a project has been migrated, Terraform removes its per-repo job definitions. Do this only after the shared jobs have run successfully at least once.
- If anything still points at a repo-bound job, show a deprecation warning naming the shared-job replacement. Remove the code path in a later release.

## Release blockers

- **Bitbucket proxy verification check must pass.** The proxy is built, but the end-to-end check is still outstanding. Bitbucket projects will depend on it from day one.
- End-to-end tests of shared jobs on both GitHub and Bitbucket: clone, fetch, push, opening a PR, and image build with the credential-leak scan.
- Remove the "untested, so disabled" gate on shared jobs once these pass.

## Out of scope

- Deleting the repo-bound or repo-level image code (decide later).
- New execution backends.
