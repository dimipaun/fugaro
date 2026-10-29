# Fugaro Forge, Authorship & Image Spec

Draft, October 2026. Covers commit authorship, a pluggable forge layer (GitHub, GitLab, Bitbucket), multi-repo images, and project-level Dockerfiles.

## Commit authorship

- **Author** = the human who launched the job. That person is accountable for the change and shows up in blame.
- **Committer** = the Fugaro bot identity for the forge (GitHub App, GitLab project/group bot user, Bitbucket bot account or repository access token).
- Author and committer are standard Git commit fields, so this works the same on every forge.
- Trailers on every commit: `Fugaro-Job:`, `Fugaro-Rigor:`, `Fugaro-Model:`. These link each commit back to its job and spend record.
- Never add `Signed-off-by` automatically. A DCO sign-off is a legal assertion that only a human can make.
- Recording the launcher as author doubles as an audit trail for the subscription seat definition (a seat is anyone who launches jobs).

## Forge abstraction

Required at minimum: **GitHub, GitLab, Bitbucket**, all behind one small interface.

The interface:

1. Mint a short-lived token scoped to the repos the task may write to
2. Create commits / push branches
3. Open and update pull/merge requests (draft ↔ ready)
4. Post comments
5. Read check/pipeline status

Forge-neutral (written once, not per adapter): author/committer handling, trailers, job-ID linking across repos, PR lifecycle policy (draft until senior review passes).

Credentials stay in the per-project security service. Adapters ask it for tokens and never store long-lived secrets in jobs.

### Signing (per-forge differences, to verify)

- **GitHub:** commits created through the API by a GitHub App are expected to show as Verified automatically. Prefer API commits over in-container `git commit`.
- **GitLab:** GitLab may sign commits created through the API/web. Verify behaviour on GitLab.com and self-managed instances before relying on it.
- **Bitbucket:** probably no forge-side signing. The security service holds a per-project signing key (SSH or GPG) and signs in the job.
- Repos with "require signed commits" protection need this solved before launch on that forge.

## Multiple repos in one image

- Treat baked repos as a cache, not the source of truth. Bake every repo at a pinned SHA.
- At job start, `git fetch` and fast-forward only the repos the task touches. Stale images never produce stale code.
- The task declares which repos it may write to. All other repos are read-only context. The security service mints a token scoped to exactly the writable set.
- Cross-repo changes: one PR/MR per repo, linked by a shared job ID. Merge ordering (contract before consumers) is owned by the coordinator.
- Security note for SECURITY doc: baked source for every repo sits in the project's image registry, readable by anyone with pull rights.

## Project-level Dockerfiles

- Stored in the project Git repo under `images/`. Each named image covers toolchains and services (Postgres, Redis, emulators).
- Project config maps each repo to an image. No per-repo Dockerfiles and no per-repo init.
- The setup skill writes them by inspecting all of the project's repos together.
- Built with Cloud Build, tagged by content hash. Jobs pin the image digest.
- Layer order for cheap rebuilds: toolchains, then services, then dependency installs from lockfiles, then source snapshots last.

## Image build and project repo layout

The repo manifest lives in the project Git repo, never in a service repo, so service repos need no Fugaro awareness.

- **Manifest:** lists every repo pinned to a commit SHA, like a lockfile. Bumped on each rebuild.
- **Build:** Cloud Build reads the manifest and fetches each repo at its pinned SHA, using a short-lived token passed as a BuildKit secret (never baked into the image).
- **Layers:** toolchains and services, then dependency installs from each repo's lockfile, then one source layer per repo on top. A change to one service rebuilds only its layer.
- **Git metadata:** baked as a shallow clone so jobs fetch and fast-forward quickly at start.
- **Rebuild cadence:** nightly, plus on main moving for busy repos. Jobs catch up with a fetch anyway.

### Intent and lock

Repo selection is split like a package manager: a hand-edited intent file and a generated lock (the manifest).

**Intent (hand-edited, project repo):**

- The repo allow list: explicit names for small projects (3–5 repos), wildcards for large ones like [a large customer project] (50+ repos), so new repos are picked up without edits.
- Optional resolver command (e.g. the customer's own CLI) that prints the repo list. Without one, Fugaro expands wildcards through the forge API.
- Overrides block for every hand edit: pin a repo to a SHA, exclude a repo, assign a repo to a different image.

**Lock / manifest (generated, never hand-edited):** regenerated on each rebuild. One entry per resolved repo:

| Field | Meaning |
| --- | --- |
| Repo | Forge path (org + repo name) |
| Commit | Exact SHA baked into the image |
| Default branch | What jobs fetch and fast-forward to at start |
| Image | Which project image the repo goes into |
| Source | Explicit name, wildcard or resolver (for debugging) |

Header: generation time plus a hash of the intent file, so Fugaro detects a lock that is stale against its intent.

The lock file is named `fugaro.lock`, next to `fugaro.yaml` (the intent).

**Resolution order:**

1. Generator command configured → run it and use its repo list.
2. No generator, allow list fully explicit → use the names as listed, no expansion.
3. No generator, allow list has wildcards → iterate per forge and expand each wildcard through that forge's API.

The allow list is the security boundary: generator output is intersected with it, and any repo the allow list doesn't permit is dropped with a warning. A buggy generator can never widen access.

### Repo markers

The project allow list is the authority on membership; an in-repo marker is an optional pointer.

- **Marker:** a minimal `fugaro.yaml` in the repo holding only the Fugaro project name and GCP project. It tells anyone who checks out the repo that it belongs to Fugaro and which project; the CLI uses it to reach Firebase and fetch everything else.
- **Require-marker setting (per project):** off = unmarked repos work (the project knows the repo; the repo needn't know the project). On = wildcards only pick up repos carrying a marker. Adding markers across 50 repos is a scriptable one-PR-per-repo change.
- **Mismatch:** a repo whose marker points at another project, or that the setting excludes, is ignored with a clear warning naming the reason.

### Repo sync

`fugaro repo sync` re-resolves repos, diffs against `fugaro.lock` and updates it. Run manually (e.g. right after adding a repo) or by the nightly job; `--check` reports the diff only.

- Removed repos: dropped from the lock, image rebuilt automatically.
- Added repos: install dependencies and run tests in the existing image. Pass → added and rebuilt automatically. Fail → the setup skill runs as a job with the failure log and opens a Dockerfile PR for human review; the repo stays pending (out of the image) until merged.

### Monorepo layout

- No separate project repo. Config (Dockerfiles, project settings) lives in a `.fugaro/` folder in the main repo; the manifest is trivial with one repo.
- Project state (settings snapshots, spending summaries) goes on an orphan branch, e.g. `fugaro-state`, in the same repo, keeping main's history clean and avoiding writes to protected branches.

## Developer onboarding

Developers are end users in the project's Firebase Auth (the IdP), never GCP/Firebase IAM members; only owners and admins hold IAM roles.

- Role (viewer, operator, admin) comes from the project allow list, stored as a custom claim or Firestore record and enforced by security rules and the security service.
- Developers can launch jobs and use the dashboard but cannot reach the GCP console or Secret Manager. Jobs get credentials from the security service, never from laptops.
- Removing someone = disabling their Auth account. No Terraform or IAM change per developer.

**Flow (e.g. [a large customer project], 10–15 developers):**

1. Admin runs `fugaro user add` with emails (or `--domain`) and a role; a workspace-level invite can cover the customer's main project, [its sibling projects] at once.
2. The CLI prints a short invite (project name, install line, `fugaro join`) for the admin to paste into Slack or similar. The CLI sends no email by default.
3. Developer runs `fugaro join` in a repo: reads the `fugaro.yaml` pointer (or takes the project as a flag), signs in with Google, checks the allow list, fetches config.
4. The CLI issues the seat licence automatically and installs skills locally.
5. Optionally, the developer shares their Claude OAuth token with the project as a named reference for failover.

**Commands:**

| Command | Does |
| --- | --- |
| `fugaro user add` | Add emails or a domain with a role; prints the invite |
| `fugaro user ls` | Members, role, status, last active |
| `fugaro user role` | Change a member's role; enforced live by the security service |
| `fugaro user suspend` | Block access immediately but keep role, history and shared tokens; suspended users don't count as seats |
| `fugaro user resume` | Restore a suspended member as they were |
| `fugaro user rm` | Revoke access permanently |

Later, low cost: `--email` on `user add` using Firebase Auth's built-in sign-in link emails.

Starting role is set at invite time: `--role` is required for email invites; domain invites default to viewer, since they open the project to everyone in the domain.

**Console and mobile:**

- Console gets a per-project Members tab, admin-only: list with role, status and last active; Invite producing the same pasteable message with a copy button; inline role change; suspend, resume, remove.
- Console and CLI call the same backend API, so behaviour never drifts.
- Mobile app: read-only member list, with suspend as the one allowed action for emergency lock-out from a phone.

**Contributing a Claude OAuth token:**

1. Developer runs `fugaro token add`, which runs `claude setup-token` and has them approve in the browser.
2. The CLI sends the token straight to the project security service over an authenticated call. Never written to disk, Firestore, RTDB or the project repo.
3. Stored as a named secret owned by that developer (e.g. `dev-a-max`); they can list, rotate or withdraw it. Suspending or removing the user pauses their token.

**Storage and IAM:**

- Secrets live only in Secret Manager.
- The security service's account may create secrets and add versions only under the prefix `user-token-` (IAM condition on the secret name). Admin-managed secrets (model keys, GitHub credentials) stay read-only to it.
- Admins keep full Secret Manager access through IAM.
- Considered and rejected for now: KMS-encrypted tokens in Firestore (cleaner at scale, more moving parts).

**Token policy (per project):**

- **Own:** a job gets only the token contributed by its author (e.g. developer A's jobs use developer A's token).
- **Pool:** a job uses its author's own token first; once that hits its reserve, it borrows from the other contributed tokens (developer A, developer B, developer C), picking the one with the most headroom and skipping any that are rate-limited or at their reserve.
- The author comes from a signed job token minted at launch with the launcher's identity; the security service trusts only that, never a self-declared author.
- Own-mode exhaustion is a policy choice: wait, fail, or fall back to a project API key.
- Default: own, until the terms question below is settled.

**Token reserve:**

- Default policy when unspecified: own.
- Fugaro stops drawing on a contributed token once any usage window (five-hour, weekly) passes a threshold (e.g. 80%), leaving the owner headroom; it resumes after the window resets.
- The project sets the default threshold; each owner may tighten it for their own token.
- Usage must reflect the owner's total use, not just Fugaro's jobs. Claude Code already displays remaining quota, so the same data should be readable (likely API response headers on subscription tokens). To verify how Claude Code obtains it.

To check: whether pooling one person's Claude subscription for others' jobs fits Anthropic's terms. Safer default: a contributed token runs only its owner's jobs; shared project capacity uses API keys.

## Decisions

- Launch supports cloud-hosted forges only: GitHub.com, GitLab.com and Bitbucket Cloud. Self-managed GitLab and Bitbucket Data Center come later.
- Signing keys are SSH keys, one per project per forge, held by the security service and registered to that project's bot account on that forge. Projects rarely span forges, so in practice this is one key per project, or none on GitHub if API commits are verified.
- Cross-repo work is a core feature: roughly 10–30% of [a large customer project] tasks touch several microservice repos.

## Change sets

A change set is one job whose changes span several repos, delivered as linked PRs.

- One PR/MR per repo, all tagged with the shared job ID and linking to each other.
- All PRs stay draft until every one passes senior review; then all flip to ready together.
- The change set records merge order: contracts and shared libraries first, then consumers.
- The coordinator enforces that order and blocks a consumer merge until its dependencies have merged.
- Matches how [a large customer project] merges today.

## Open questions

- None at present.
