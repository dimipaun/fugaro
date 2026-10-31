# Console release notes

`vX.Y.Z.md` in this directory is the **release body of the desktop console's own release**,
`console-vX.Y.Z`. It is not an entry of the CLI's changelog: the two are separate release
streams with separate tags, separate version lines and separate notes (the owner's addendum
of 2026-10-22 — CLI = animals on `vX.Y.Z`, **Console = fruits on `console-vX.Y.Z`**, Mobile =
birds), and `docs/releases/` one level up holds the CLI's, whose format
[`../README.md`](../README.md) describes. The site's changelog reads that directory and skips
this one; it never shows a console release.

The console workflow reads **this** file from the tagged commit and passes it as the whole
release body, after the standing sentence about what is and is not signed. It refuses to
build when the file is missing or empty, so it is a required release artifact: the console's
release cannot be cut without it.

## Format

Plain Markdown, read by a user rather than by a parser. There is no `## Highlights` wrapper
and no GoReleaser: the file's first line is the release's **heading**, and the rest is the
body, so:

```markdown
# Fugaro Console 0.110.0 "Cherry"

One sentence, for a user who has not read anything else: what this console now does for
them, in the words they would use.

- What changed for them, one bullet each: what they can do now, what they had to do before,
  what works on which platform.
- What to do to get it: download the `.dmg` (macOS, signed and notarised) or the AppImage or
  the `.deb` (Linux, **unsigned** — the SHA-256 in `console-sha256sums.txt` is what proves the
  bytes, and that file is signed with the Sigstore bundle beside it).

### Known limits

- What this release does not do, does not stop, or leaves to a later one.
```

The **release name** is the fruit in that heading (`"Cherry"`). The names are used once each,
in this order:

| Release | Name |
| --- | --- |
| `0.110.0` | Cherry |
| `0.111.0` | Mango |
| `0.112.0` | Melon |
| `0.113.0` | Nectarine |
| `0.114.0` | Orange |
| `0.115.0` | Papaya |
| `0.116.0` | Peach |
| `0.117.0` | Pear |

**Granny Smith, Golden Delicious and Crab are not on the list**, and neither is Lemon: a
name that spells "Apple" puts the word in a console release's title, and the desktop app is
not an Apple product; Lemon is avoided because it reads as a disclaimer on a release that has
not gone sour. The list grows by one when the next console release is cut, in the commit that
writes its notes file.

**Never below `0.10.0`.** The console's updater refuses a downgrade (Task 4.4 of
`docs/plans/2026-10-15-desktop-console.md`), so a console released with a version lower than
the one an installation already runs would refuse every update it was offered from then on.
The console's first release on its own stream is **`0.110.0` "Cherry"**: the version line and
the tag may not go below `0.10.0`, which is the console that shipped inside CLI `v0.10.0`, and
once Cherry is out, not below `0.110.0`. The stream starts at `0.110.0` rather than `0.11.0` so
that a console version is never confused with the CLI's own `0.11.0` (the owner's ruling of
2026-10-24; `docs/release.md`, "Console releases").

## Before the tag

`docs/release.md` ("Console releases") is the procedure. Both halves of it happen on `main`,
in the same commit, **before** the tag exists: `console/package.json`'s `version` is set to
`X.Y.Z`, and this release's notes file is written as `vX.Y.Z.md`. The workflow refuses a tag
whose `X.Y.Z` is not the version line's, and refuses to build without the notes file.

## The licence changes at 0.113.0

**From `0.113.0` the console is under its own business licence, [`console/LICENSE.md`](../../../console/LICENSE.md)**
(the owner's ruling D311 of 2026-10-26, [`LICENSING.md`](../../../LICENSING.md)); every release
up to and including `0.112.0` stays under the MIT licence it was released under. So the notes of
`0.113.0` say it in their summary line or first bullet, in a user's words: the release is the first
under the new licence, production use needs a subscription, and the earlier releases stay MIT.
The licence text is counsel's, and it names Subscription Terms that are not published yet: the release waits until the owner has published those terms or replaced that
sentence, because until then a production user has no way to comply.
