# The mobile app's release highlights

`mobile/vX.Y.Z.md` in this directory is the hand-written **Highlights** of the
mobile app's release `mobile-vX.Y.Z` — the app's own stream, its own tag and its
own notes since the owner's ruling of 2026-10-22 ([../../release.md](../../release.md),
"Mobile releases"). The format is the CLI's, one directory down
([../README.md](../README.md) is the authority for it; this file is the app's
copy of the parts that differ):

- 3 to 6 bullets for users of the app, then `### For operators` when a rollout
  order matters, then `### Known limits` — a release ships with its known limits
  in its notes, so that section goes in unless the release genuinely has none.
- No title: the release page adds its own heading.

**The app's notes are in this directory because they are not the CLI's.** The
website's changelog generator reads the release files one level down
(`docs/releases/*.md`) and stops, naming the file, on any name that is not
`vX.Y.Z.md` (`website/src/lib/changelog.mjs`), and this directory is skipped by
name for the same reason `../README.md` is. So an app note at the top level would
either enter the CLI's changelog or break the site's build, and one written here
does neither.

**The two other things a mobile release ships with**, both in the commit that
lands the notes, on `main` before the tag:

- **`expo.version` in `mobile/app.json`, set to `X.Y.Z`.** That is the version
  the app reports, and the staged release workflow refuses a tag whose version
  disagrees with it ([../../pending-workflows/mobile-release.yml](../../pending-workflows/mobile-release.yml)).
  The notes' version and the tag's version are the same number as the config's.
- **One line in `website/data/mobile-release-summaries.yaml`**, keyed by the tag
  (`mobile-vX.Y.Z: what this release gives a user.`): the app's own end-user
  summary, beside the CLI's [`website/data/release-summaries.yaml`](../../../website/data/release-summaries.yaml)
  rather than inside it, because that file is keyed `vX.Y.Z` and validated
  against the CLI's own release files. What a sentence says, and what it must
  not, is [../README.md](../README.md)'s rule and holds here as it does there:
  what the release gives a user, not what changed inside the repository. Nothing
  reads this file automatically yet — the app's stream has no release body
  generated for it — so it is written for the app's own release page and for the
  store listing's "what's new" text, and it is the app's record of the release's
  one sentence.

**The name comes first.** The app's releases are named after birds, spent in
order: the **first is Falcon** (`mobile-v0.1.0`), then **Pelican**, then
**Robin**. The name belongs in the first bullet of the notes, in the summary
line's own phrasing where it fits, and in the store listing's "what's new" text:
this stream creates no GitHub release to title.

**The first release is `mobile-v0.1.0`, "Falcon".** `mobile/app.json` carries
`expo.version` `0.0.0` today, which is a placeholder rather than a version
anybody released: the release that cuts `mobile-v0.1.0` sets it to `0.1.0` on
`main` first. If the config carries another version when you read it, **that**
line is the release being cut and the tag is `mobile-v<that line>`: the config is
the authority and the tag follows it.

`../README.md`'s procedure (commit the file to `main` through a PR before
running the release) is the CLI's and does not apply here: the app's release is
started by the `mobile-vX.Y.Z` tag, and nothing in this repository writes a
GitHub release for it.
