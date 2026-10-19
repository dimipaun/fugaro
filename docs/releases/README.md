# Release highlights

`vX.Y.Z.md` in this directory is the hand-written **Highlights** of release `vX.Y.Z`. GoReleaser puts it into the GitHub release body between the install header and the generated `## Changelog` (`.goreleaser.yaml`, `release.header`), which is followed by the base-images footer.

It is a required release artifact from 0.4.0 on: `scripts/release.sh X.Y.Z` refuses to start, and the release workflow refuses to publish, when `docs/releases/vX.Y.Z.md` is missing or empty. Earlier releases (v0.1.0 to v0.3.1) have none, and none is written for them.

Commit it to `main` through a PR **before** running `scripts/release.sh`: GoReleaser reads it from the tag's tree, and the script runs from a clean `main`. The procedure is in [../release.md](../release.md) ("Cutting a release"); the `/new-release` skill (`.claude/skills/new-release/SKILL.md`) follows it.

## The end-user summary

The same PR also writes the release's **end-user summary**, one line in [`website/data/release-summaries.yaml`](../../website/data/release-summaries.yaml):

```yaml
summaries:
  v0.8.7: A sentence saying what this release gives a user.
```

The website's changelog page prints that sentence above the entry's Highlights, and the site's build reads one entry per release file and stops, naming the file, when a release file has none (`website/src/content.ts`). So it is a required artifact wherever this file is, from 0.4.0 on, and `scripts/release.sh X.Y.Z` refuses to start when `main` carries no entry for the version being released. What a sentence says, and what it must not: this is the page a user reads before a release note, so it is what the release gives them, not what changed inside the repository — the entry's Highlights, one click below it, are where the details are.

## Format

Plain Markdown, no title (the release page adds `## Highlights`):

- 3 to 6 bullets for users, each one sentence: what they can do now or what changed for them, not the commit titles (the changelog has those).
- When a rollout order matters, a `### For operators` heading with the ordered steps (for example: upgrade CLIs and CI pins first, then rebuild images, then merge the config change), and anything that breaks or needs a manual step. Omit it otherwise.
- A `### Known limits` heading with the release's known limits as bullets: what this release does not do, does not stop, or leaves to a later one, and anything a reader who relies on it has to know. The rule is that a release ships with its known limits in its notes, so write them; the heading is omitted only for a release that genuinely has none.

The two section headings are optional, independent (a file may carry either without the other, or none) and written after the bullets, in the order a reader needs them — and they are `###`, not `##`: GoReleaser puts the file into the release body under its own `## Highlights` (`.goreleaser.yaml`), so a `##` inside the file would be a second top-level heading beside it. The website's changelog page renders each one as a heading with its bullets under it, in the order the file writes them (`website/src/lib/changelog.mjs`); a heading line it does not recognise is rendered as the text it is, and the site's check refuses it.

Example:

```markdown
- Teammates need no setup: the installation's settings are shared through the repository.
- `fugaro init --name` creates a second project.

### For operators

1. Upgrade the CLI and run `fugaro init` once per installation.
2. Rebuild each repository's image before merging its `gcp_project:` line.

### Known limits

- `fugaro init --name` does not rename an installation that already exists.
```
