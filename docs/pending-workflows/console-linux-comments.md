# `console-linux.yml`: the comments two changes invalidated

**Status: staged, comment-only, not applied.** `.github/workflows/console-linux.yml`
carries a comment saying `electron-v<version>-linux-x64.zip` is one of **three**
hashes `build.electronDownload.checksums` pins. That block gained a fourth hash,
`electron-v<version>-win32-x64.zip`, on **2026-11-09**, when the console's
Windows work pinned the archive its Windows leg will download before that leg
exists (docs/console-windows/implementation-plan.md T-B3, finding F37;
`console/test/unit/fuses.test.ts` holds the block to the lockfile-pinned
package's own `checksums.json`). The count is now stale. This file holds the
corrected comment text. **No executable line changes**, and the check below
proves it.

**A second comment of the same file is stale too, for a change of 2026-11-22**
(`#3566`): it says `console/package.json`'s `engines.node` is a field
`test/unit/pinning.test.ts` keeps an exact pin, and the field is a **range** —
`>=22.23.3 <23`, a floor and the next major as its ceiling — which that test's
`enginesNodeRE` carves out of the exact-pin rule. It is the same correction
`console-tests-comments.md` and `console-windows-comments.md` stage for the
comments their own files carry, with the same pair below.

It is a `.md` file rather than a `.yml` one: a `console-linux-comments.yml` would
be enumerated as a pending workflow by `internal/cli`'s docs tests and by
`website/test/readme.mjs`, both of which read every `.ya?ml` in this directory,
and it would have to be named in this document's opening line and in
`website/README.md` — none of which is a true statement about a comment
correction.

Unlike `console-release-comments.md` and its siblings, this fragment is applied
to **one** file: `.github/workflows/console-linux.yml` has no staged twin, so
there is no `cp` step and no byte-identity clause to keep.

## How to apply

Edit `.github/workflows/console-linux.yml` and, for each replacement below, find
the `before` block and put the `after` block in its place, changing nothing
else. Then prove no executable line moved:

```sh
git diff -U0 -- .github/workflows/console-linux.yml \
  | grep -E '^[+-]' | grep -v -E '^(\+\+\+|---)' | grep -v -E '^[+-][[:space:]]*(#|$)'
```

It must print nothing: every changed line is a comment or a blank one. Then
commit, deleting this file in the same commit:

```sh
git rm docs/pending-workflows/console-linux-comments.md
```

**That commit also owes `docs/pending-workflows/README.md` its references back.**
This fragment is named in that page's opening line and in its section
`## The comment-only fragments`, whose three reference edits are written out
there as exact text. **One** of them is this file's, and it goes in the same
commit: the opening line's second sentence goes back from *Seven further files
here are not workflows at all* to *Six*, its list losing this file's name and
keeping `ci-hygiene.yml.snippet`, which is not a comment-only fragment and which
no `git rm` here retires. The exception paragraph under that
section's numbered list is **not** one of the three: it names this file,
`console-tests-comments.md` and `console-windows-comments.md` together, so it
goes with the last of the three to be applied and not with this one's `git rm`
alone. No test can see
a dangling reference here, because the fragments are `.md` and neither
`internal/cli`'s docs tests nor `website/test/readme.mjs` enumerates them.

## The count of pinned hashes

**before**:

```
      # Electron's own download of its distribution, verified against the pinned
      # package's `checksums.json`: `electron-v<version>-linux-x64.zip` is one of
      # the three hashes `build.electronDownload.checksums` pins. Then the build
      # the packer packs (`npm run build` is what writes the file
      # package.json's `main` points at).
```

**after**:

```
      # Electron's own download of its distribution, verified against the pinned
      # package's `checksums.json`: `electron-v<version>-linux-x64.zip` is one of
      # the hashes `build.electronDownload.checksums` pins. Then the build the
      # packer packs (`npm run build` is what writes the file package.json's
      # `main` points at).
```

## Why the count is dropped rather than raised to four

The block pins one archive per platform this build packs, and that set moves:
`win32-x64` joined it on 2026-11-09, in the same batch of Windows work that
added the leg's fuse row and its packer target (implementation-plan.md T-B2,
T-B4). A comment that names the number is a comment that goes stale on the next
one, which is what happened here; the sentence says what the file is checked
against instead, and `console/test/unit/fuses.test.ts` is where the exact set is
pinned.

## The Node pin's shape

**before**:

```
      # The Node version comes from console/package.json's `engines.node`, the
      # field test/unit/pinning.test.ts keeps an exact pin: a version written
      # here a second time would be a pin that can disagree with the one the
      # console's own tests enforce.
```

**after**:

```
      # The Node version comes from console/package.json's `engines.node`, the
      # field test/unit/pinning.test.ts holds to a floor and the next major as
      # its ceiling: a version written here a second time would be a pin that
      # can disagree with the one the console's own tests enforce.
```

The comment says where the Node version comes from and why it is not written
into the workflow a second time, and both halves are still what a reader needs:
`actions/setup-node` reads the field through `node-version-file`, and the
console's own tests are what hold the field's shape. Only the shape changed — an
exact version to a floor and a ceiling — so the sentence keeps its work and
loses the claim that the shape is an exact pin.
