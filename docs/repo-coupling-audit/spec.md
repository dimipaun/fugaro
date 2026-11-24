# Fugaro Repo-Coupling Audit Spec

## Purpose

Fugaro started repo-centric and has been moving to a project-centric model. Shared images and shared jobs have already been lifted to the project level, but repo-level coupling keeps turning up one setting at a time. This audit produces a complete, verified inventory of everything that still ties Fugaro to an individual repo, so the remaining work can be planned in one go instead of discovered piecemeal.

This is an audit only. It changes no behaviour and ships no migrations. Its output is a report and a recommended lift plan.

## Guiding principles

- Anything that needs infrastructure changes (Terraform, IAM, Secret Manager, Cloud Build, Cloud Scheduler, images, service accounts) lives at the project level and is owned by project admins.
- `fugaro update` is run once per project by an admin. After that, no other member should need to run anything or have elevated permissions.
- The repo level may hold only benign, plain-data settings that never require a Terraform run. Every such setting must be listed for Dimi's explicit review before it is kept.
- Old repo-level configuration is migrated once, then rejected, consistent with the credentials routing spec.

## Scope

The audit must cover every surface where a repo identity (repo name, repo path, remote URL, repo ID, repo config file) is used as a key, owner, scope or input:

1. CLI commands and flags, including `fugaro init`, `fugaro setup`, `fugaro update`, job launch, `fugaro runs ls`, recipe commands and any command that reads the current working directory's repo.
2. Repo-local config files committed or generated in repos, and every field in them.
3. Firestore and RTDB data model: collections, document paths, indices and fields keyed by repo.
4. GCS paths for logs, diffs, archives, recipes and state.
5. Terraform modules and resources: anything created per repo or parameterised by repo.
6. Secrets and credentials: Secret Manager entries, OAuth tokens, API keys, GitHub/Bitbucket tokens, and any repo-level choice of credential type.
7. Images and Cloud Build: image definitions, Dockerfile locations, build triggers, Cloud Scheduler rebuilds.
8. Jobs: job definitions, Cloud Run job resources, shared versus per-repo jobs.
9. Spending caps, rigor levels, recipes and model routing: any repo-level layer or override.
10. Console, Watch, mobile app, coordinator and MCP server: any view, query or permission scoped by repo.
11. `fugaro update` and migration code: every step that only works when run inside a specific repo.
12. Skills, docs and templates that instruct users to act per repo.

## Method

1. Static code search across all Fugaro repos for repo identifiers, cwd and git-remote lookups, and repo-scoped paths. Record every hit, then deduplicate into distinct coupling points.
2. Data model inventory: export the schema of a real project (Firestore, RTDB, GCS layout) and mark every repo-keyed path.
3. Terraform inventory: list every resource and variable; flag any that are created per repo or take a repo argument.
4. Runtime trace: run `fugaro update` and a typical job launch on a multi-repo test project, and log every place repo context is read or required.
5. Permission review: for each coupling point, record what IAM role a member needs today to exercise it.

## Classification

Each coupling point is classified as one of:

- **Infra-bound**: requires Terraform or other infrastructure change. Must be lifted to project level and made admin-only.
- **Benign override candidate**: plain data, no infrastructure, plausible reason to differ per repo. Listed for Dimi's review; kept only if approved.
- **Identity only**: the repo is used legitimately as a label or target (for example, which repo a PR goes to). No action needed beyond confirming it is not used as a config owner.
- **Dead or vestigial**: disabled or unused code paths (for example, repo-level images that are disabled but still in the codebase). Recommend removal.

## Report format

One row per coupling point with these fields: ID, surface, location (file and line, or resource path), what is tied to the repo, classification, permissions needed today, effect on `fugaro update`, proposed project-level home, migration notes, effort estimate, and risk.

The report also includes a summary count by surface and classification, a list of everything that blocks a single admin-run `fugaro update`, and a proposed order for lifting work.

## Known items to confirm

These are already known or partly addressed; the audit should confirm their current state rather than assume it:

- Shared images: lifted, but `fugaro update` can only rebuild shared images whose Dockerfile lives in the repo where it is run.
- Shared jobs: lifted from per-repo jobs; confirm no per-repo job resources remain.
- Per-repo `fugaro init repo` with Terraform and confirmations.
- Repo-level marking of API key versus OAuth token, superseded by model credential routing.
- Repo-level spending caps and overrides.
- Repo-level recipe layer.

## Open question: images spread across repos

When shared image Dockerfiles live in several repos, `fugaro update` cannot rebuild them all from one checkout. The audit should evaluate and recommend between:

- A documented procedure the admin follows when images are spread across repos.
- `fugaro update` fetching each image's build context directly from its source repo via git, so it works from anywhere.
- Keeping image definitions in the project state repo, so there is a single source.

## Corner cases to cover

- **Stale local config**: repo-level config files left in existing checkouts could silently override project settings after an update. The audit must find every place such files are still read.
- **Mixed CLI versions**: a member on an old CLI could keep writing repo-level config after an admin has upgraded the project.
- **Monorepos and shared repos**: a repo used by two projects, or a monorepo serving many services, makes any repo-owned setting ambiguous.
- **Hidden admin dependencies**: any action a normal member takes today that silently triggers Terraform or needs elevated IAM rights.

## Proposal: required Fugaro version stamp

The project stores a required minimum Fugaro version in its Firebase settings (RTDB or Firestore). `fugaro update` sets it when an admin upgrades. The CLI checks it on startup and refuses project operations until the user upgrades, with a clear message and the upgrade command.

The stamp covers the CLI only. The console already detects when the CLI is out of sync, but the console is optional, so the CLI must enforce the check itself. The console's own version is not enforced: it is optional and generally works fine with older CLI versions. The mobile app is out of scope. The audit should identify every CLI entry point that must perform the check.

## Deliverables

1. The coupling inventory report in the format above.
2. The list of benign override candidates, for Dimi's review.
3. A recommendation on the images question.
4. A proposed lift plan, ordered by what unblocks admin-only `fugaro update` first.

## Non-goals

- No code, schema or infrastructure changes.
- No redesign of credentials routing, recipes or rigor levels beyond noting their repo coupling.
