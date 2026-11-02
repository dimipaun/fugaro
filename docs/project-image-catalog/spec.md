# Fugaro Project Image Catalog Spec (v1, lean)  [owner text, 2026-11-02, verbatim]

**Status: the owner's spec, as received, with one substitution.** The owner's two example project
names were replaced by the placeholders **Project A** and **Project B** before the text reached this
repository, under the standing rule that no customer or organisation name appears in docs, PRs or
commits. Nothing else was changed: no typo fixed, no wording edited. The owner's rulings on this
folder sit under **Owner rulings** at the end of this file.

## Summary
Job images move from repo-centric to project-centric. A project keeps a small, hand-curated catalog of named Docker images. Repos inherit the project default or reference a catalog image. Repo-level images stay as an optional override. This spec replaces the earlier repo image spec, which tried to detect dependencies and generate images automatically. That work is out of scope.

## Goals
- Let a person define a few images by hand in minutes.
- Make project-level images the default; keep repo-level overrides for rare cases.
- Make sure a job runs in an image that covers every repo in it, using simple tags.

## Non-goals
- Automatic dependency detection or analysis of repos.
- Generating, composing or layering images automatically.
- Per-repo Terraform or a heavy per-repo init.

## Catalog entry
Each entry has:
- name: unique within the project, for example frontend, backend or universal.
- tags: a free-form list of what the image provides, for example node, jvm, postgres, redis, firebase-emulator, docker.
- source: one of
  - dockerfile: a path to a Dockerfile (in the project Git repo or a named repo), or
  - inline: Dockerfile contents written straight into the catalog, or
  - image: a reference to a prebuilt image (optional, if cheap to support).
- description: optional.
The project marks one entry as default.

## Repo configuration
A repo can do any one of these:
1. Nothing: it uses the project default.
2. image: <name>: it uses that catalog image.
3. needs: [tags]: Fugaro chooses an image that covers those tags.
4. A repo-level override image, for the rare repo that doesn't fit the catalog.

## Choosing an image for a job
- For a single-repo job, use the repo's image, its tag match, or the project default.
- For a multi-repo job, combine every tag the repos need. Choose the catalog image with the fewest tags that still covers them all. If two images tie, use the default when it qualifies; otherwise pick by name.
- If no image fits, fail before the job starts. Say which tags are missing and suggest adding an image or a universal image.
- A user can pin an image for a job by hand; Fugaro warns if the pinned image doesn't cover the needed tags.

## Where Dockerfiles live
Every Dockerfile is copied into the project's GCS bucket before it is built, and the build reads it from there. This gives one read path and a record of exactly what was built, and it avoids a full checkout just to get one file.
- Repo reference: repo name + path (+ optional branch), for example backend-api:docker/agent.Dockerfile. Leaving out the repo means the project repo, if there is one. Fugaro fetches the file with the project's existing GitHub credentials from the security service; no new access is needed.
- Inline: the definition is stored in project state and written to GCS as a Dockerfile.
- The project repo is optional, so neither source depends on it.
- Build context: a catalog entry can set it explicitly. By default, a repo-referenced Dockerfile uses its own repo as context, shallow-cloned at build time, so COPY works. An inline definition gets an empty context plus any baked repos. Builds run only nightly or on demand, so the clone cost doesn't matter.

## Build secrets
Build-time secrets, such as a private npm token, come from the project's security service. They are mounted only for the build (for example as BuildKit secret mounts) and are never written into image layers.

## Migrating repo images
Existing repo-level images are never converted automatically. `fugaro image promote <repo> [name]` opts in: it copies the repo's image definition into the catalog and points that repo at the new entry.

## Runtime contract
An image can be built from any base, as long as it meets the Fugaro runtime contract. The published Fugaro base image is only a convenience package that already meets it.
- Required: the Fugaro agent binary, Git, a shell, plus anything else on the contract list.
- Fugaro does not check the FROM line.
- After each build, Fugaro runs a quick check inside the image and fails the build if anything required is missing.

## Baked repos
A catalog entry can list repos to bake into the image, so jobs start fast.
- When a job starts, Fugaro fetches the latest commits on top of the baked copy; ordinary commits never need a rebuild.
- Repos not baked in are shallow-cloned when the job starts.

## Storage
Built images are stored in the project's own Google Artifact Registry, next to the Cloud Run jobs that pull them. A few recent versions are kept for rollback.

## Building
- Fugaro builds catalog images on demand or with an explicit command, and caches them per project.
- A nightly job checks each image and rebuilds it only if its definition changed, its base image changed, or a baked repo's dependency files changed (for example package.json or a lockfile).

## CLI (sketch)
- fugaro image ls lists catalog entries, their tags and the default.
- fugaro image add <name> --tags ... --dockerfile <path> | --inline <file> adds an entry.
- fugaro image build [name] builds one image or all of them.
- fugaro image rm <name> removes an entry.

## Examples
- Project A: backend image with Postgres, Redis and the Firebase emulator built in, frontend image for React.
- Project B: a Docker-capable image for the gen-2 Cloud Run backend.
- Monorepo: a single default image; project and repo scope are the same.

## Open questions
- Where should catalog definitions live: Firebase project state, the project Git repo, or both?
- Should images be rebuilt on a schedule for base-image security updates?
- How should the docker tag map to execution backends (gen-2 Cloud Run or VM)?

---

## Owner rulings

These are the owner's answers given alongside the spec (AskUserQuestion, 2026-11-02). They are not task
approvals: each task in [implementation-plan.md](implementation-plan.md) still needs the owner's own go.

- **Relationship to the shared-images design** ([docs/forge-authorship-images/shared-images.md](../forge-authorship-images/shared-images.md),
  **D617–D622**, **C8a** built in `internal/imagecheck`, **C8b**/**C8c** not built): **CATALOG FIRST**.
  Dependency-set sharing stays documented as a later option and is **NOT cancelled**. Do not delete the
  C8a code or the old design; mark their relationship.
- **Next step:** a **DESIGN-ONLY** pull request now, aimed at **0.16.0** for the design; the build tasks
  are planned in it but **nothing is built until the owner approves the plan**.
- **Names:** the spec's two example projects are the owner's own projects. Public docs must use the
  placeholders **Project A** and **Project B**, never those names (the substitution noted at the top of
  this file).
