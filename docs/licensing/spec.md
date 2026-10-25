# Fugaro Licensing Spec (Open Core)

## Goal
Keep the Fugaro core fully open source, with no exceptions, to build trust and adoption. Make money from organisation-level features that live in the same repo under a separate commercial license.

## Principles
- The core stays MIT forever. Nothing gets moved out of the core to be sold.
- Dividing line: everything a single developer needs is free; features that matter to organisations and teams are paid.
- One repo, no separate repos or packages.
- The boundary between free and paid code must be obvious from the folder structure.

## Repo layout
- Everything outside `/ee` is under the MIT license (the current license).
- `/ee` holds commercial features under the Fugaro Enterprise License, with its own `ee/LICENSE` file.
- Code in `/ee` can depend on core code. Core code must never import or require anything from `/ee`.
- Core exposes extension points (hooks, interfaces, plugin registration) that `/ee` features plug into. If `/ee` is removed, the core must still build, pass its tests, and work fully.

## What goes in /ee (initial list)
- Workspaces (grouping an organisation's projects, shared identity, shared credentials, workspace-wide spending caps)
- Later candidates: the desktop console, the mobile app, shared central orchestrators, the per-project coordinator, and a hosted control plane. Decide on each one before release.

## What stays in the core (MIT)
- CLI, job execution, recipes, rigor modes, the review loop, project-level caps, Fugaro Watch dashboard, harness and backend abstractions, setup skills.

## License files and notices
- Root `LICENSE`: the MIT text, plus a short preamble saying that the contents of `/ee` are excluded and covered by `ee/LICENSE`.
- `ee/LICENSE`: commercial license. Source is visible and can be changed for evaluation and development, but production use needs a paid subscription. Model it on the GitLab EE or PostHog EE license.
- Every source file in `/ee` gets a short header pointing to `ee/LICENSE`.
- README: a short "Licensing" section explaining the split in two or three sentences.

## Enforcement
- Keep it light: a simple license-key check that enables `/ee` features.
- No telemetry-based enforcement and no crippling of the core.
- Without a key, `/ee` features are off and the core runs normally.

## Contributions
- Add a lightweight contributor license agreement (CLA) or a DCO plus a CLA bot before accepting outside contributions, so the copyright stays relicensable.
- No outside contributions exist yet, so Dimi owns all the copyright today.

## Optional
- Consider moving the core from MIT to Apache 2.0 for its explicit patent grant. It's equally permissive. Decide before the next release.

## Tasks
1. Create the `/ee` folder with `ee/LICENSE` and a README.
2. Update the root `LICENSE` preamble and add the README licensing section.
3. Define the core extension points that workspaces need.
4. Implement workspaces inside `/ee` against those extension points.
5. Add the license-key check.
6. Add a CI check that fails if core code imports from `/ee`.
7. Add the CLA bot to the repo.
