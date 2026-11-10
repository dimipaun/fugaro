# Fugaro Image Setup Skill — Spec

## Goal
A Fugaro skill that helps users set up project-level Docker images for running jobs. It surveys a project's repos, proposes a small set of images, and generates and validates them.

## Scope: project-level images only
- Repo-level images are **disabled, not removed**. Keep the code; it will likely come back.
- Image resolution should go through one function that always returns the project image and ignores any repo-level config (log a warning if it finds some).
- No backwards-compatibility work needed.
- Why: project-level images are simpler to maintain. A `fugaro update` rebuilds a handful of images instead of going repo by repo.

## Repo survey and image proposal
- Survey every repo in the project (for example Rocket Core, 50+ repos).
- Signals: lockfiles, version files (.nvmrc, .tool-versions, etc.), existing Dockerfiles, CI configs.
- Group repos by ecosystem and major compatibility, **not exact versions**. Version differences are often just drift from repos that haven't been updated recently.
- Hard cap: **fewer than five images** per project, each one justified. Typical result: 3–4 images (for example frontend, backend).
- Choose the newest compatible version in each group, and flag repos being moved to a newer version as possibly needing a bump.
- Use multiple runtimes in one image (several JDKs or Node versions through a version manager) instead of adding images for legacy repos.
- Output: the proposed images, each mapped to the repos it covers. Each repo maps to exactly one image.
- Out of scope for now: one unified image for everything.

## Baked repos (optional)
- Asked during `fugaro setup`, **default yes**.
- Prompt wording, kept simple: "Bake your repos into the Docker images? This makes jobs start faster."
- Stored as a project setting. It can be changed later, followed by a rebuild.
- Job logic is the same either way: if the repo is in the image, fetch and check out the target commit; otherwise, clone.
- Baked images go stale, so rebuild them regularly (for example as part of `fugaro update`).

## Base image vs recipe
- Offer both. **The recipe is the contract**: exactly what Fugaro needs inside a container.
- The Fugaro base image is a convenience: the recipe applied to a sensible default. It is **never a requirement**.
- Bring-your-own-image supports teams with hardened company images or security rules that prevent inheriting from an outside base.
- For each proposed image the skill asks: start from the Fugaro base, or extend an existing image? For BYO it generates the extra Dockerfile layers.
- A validation step (a doctor-style check) confirms the image meets the recipe.

## Harnesses: Claude harness only
- Remove the other harnesses from the base image. They pull in extra runtimes and bloat it.
- The Claude harness is the standard and works well enough with other models.
- Harness choice matters on laptops, where people interact with it, not in cloud jobs.
- The recipe becomes: Claude harness plus Fugaro's own components.

## Corner cases
- **Build-time secrets:** tokens for private dependencies (npm, Maven, etc.) must never end up in image layers. This matters even more with baked repos. Use Docker build secret mounts, never COPY or build args.
- **Test services (Postgres, Redis, Firebase emulator):** Cloud Run jobs can't run Docker in Docker. Many repos start services with Docker locally but also support non-Docker instances through a flag or environment variable. The skill can't reliably detect that switch, so it makes a best guess and asks: it flags repos that appear to use Docker for services (docker-compose, Testcontainers), proposes the services to install in the image, and asks for the flag or variable that switches them over. Fugaro sets that flag in the job and starts the services before the tests. Repos that truly need Docker go to the Cloud Batch backend (VMs: about 60–70 s to start vs about 10 s, roughly double the per-minute cost).
- **Idempotent reruns:** rerunning the skill re-surveys everything, compares the result with the current setup, and proposes only the differences (a new repo and its suggested image, or a repo that has drifted). Existing decisions stay unless something actually changed, and a rerun never reshuffles a working setup.
- **CPU architecture:** images are always built in Cloud Build (the existing Cloud Scheduler → Cloud Build rebuild), never locally, so ARM Macs never produce images with the wrong architecture.

## Skill flow
1. Survey the project's repos.
2. Propose fewer than five images with repo mappings, and flag drift.
3. For each image: Fugaro base or the user's own image.
4. Apply the baked-repos setting.
5. Generate the Dockerfiles.
6. Validate against the recipe.
