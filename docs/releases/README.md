# Release highlights

`vX.Y.Z.md` in this directory is the hand-written **Highlights** of release `vX.Y.Z`. GoReleaser puts it into the GitHub release body between the install header and the generated `## Changelog` (`.goreleaser.yaml`, `release.header`), which is followed by the base-images footer.

It is a required release artifact from 0.4.0 on: `scripts/release.sh X.Y.Z` refuses to start, and the release workflow refuses to publish, when `docs/releases/vX.Y.Z.md` is missing or empty. Earlier releases (v0.1.0 to v0.3.1) have none, and none is written for them.

Commit it to `main` through a PR **before** running `scripts/release.sh`: GoReleaser reads it from the tag's tree, and the script runs from a clean `main`. The procedure is in [../release.md](../release.md) ("Cutting a release"); the `/new-release` skill (`.claude/skills/new-release/SKILL.md`) follows it.

## Format

Plain Markdown, no title (the release page adds `## Highlights`):

- 3 to 6 bullets for users, each one sentence: what they can do now or what changed for them, not the commit titles (the changelog has those).
- When a rollout order matters, a `### For operators` heading with the ordered steps (for example: upgrade CLIs and CI pins first, then rebuild images, then merge the config change), and anything that breaks or needs a manual step. Omit it otherwise.

Example:

```markdown
- Teammates need no setup: the installation's settings are shared through the repository.
- `fugaro init --name` creates a second project.

### For operators

1. Upgrade the CLI and run `fugaro init` once per installation.
2. Rebuild each repository's image before merging its `gcp_project:` line.
```
