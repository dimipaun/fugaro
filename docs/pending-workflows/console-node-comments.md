# The four console workflows: the comment the Node range invalidated

**Status: staged, comment-only, not applied.** Four applied console workflows
carry a comment saying `console/package.json`'s `engines.node` is a field
`test/unit/pinning.test.ts` keeps **an exact pin**:

- `.github/workflows/console-tests.yml:98-101` (and its staged twin here,
  `docs/pending-workflows/console-tests.yml`, byte for byte);
- `.github/workflows/console-release.yml:531-534` and `:1082-1083` (and its
  staged twin `docs/pending-workflows/console-release.yml`);
- `.github/workflows/console-linux.yml:113-116`;
- `.github/workflows/console-windows.yml:204-207`.

The field is a **range** since **2026-11-22** (`#3566`): `>=22.23.3 <23`, a floor
and the next major as its ceiling — `console/package.json`'s own description and
`console/README.md`'s "The Node is the one field pinned to a range" paragraph.
`test/unit/pinning.test.ts` carves the field out of the exact-pin rule for it:
its `enginesNodeRE` accepts a floor and the next major and nothing else, an
exact version included. The four comments are therefore false, and this file
holds the corrected text. **No executable line changes**, and the check below
proves it.

It is a `.md` file rather than a `.yml` one: a `console-node-comments.yml` would
be enumerated as a pending workflow by `internal/cli`'s docs tests and by
`website/test/readme.mjs`, both of which read every `.ya?ml` in this directory,
and it would have to be named in this document's opening line and in
`website/README.md` — none of which is a true statement about a comment
correction. It is one file for four targets rather than four, because the stale
text is one comment repeated across them and one instruction is what the person
applying it wants.

## How to apply

Edit the **applied** file: find the `before` block below and put the `after`
block in its place, changing nothing else. Two of the four have a staged twin
here — `console-tests.yml` and `console-release.yml` — and those are the two
whose copies must stay byte-identical (`TestConsoleTestsWorkflowIsStagedAndPinned`,
`TestDocsConsoleReleaseWorkflowIsStagedAndPinned`), so after editing the applied
one, make the staged copy the same text:

```sh
cp .github/workflows/console-tests.yml docs/pending-workflows/console-tests.yml
cp .github/workflows/console-release.yml docs/pending-workflows/console-release.yml
```

Then prove no executable line moved, in every one of the four:

```sh
git diff -U0 -- .github/workflows/console-tests.yml \
  .github/workflows/console-release.yml .github/workflows/console-linux.yml \
  .github/workflows/console-windows.yml \
  | grep -E '^[+-]' | grep -v -E '^(\+\+\+|---)' | grep -v -E '^[+-][[:space:]]*(#|$)'
```

It must print nothing: every changed line is a comment or a blank one. Then
commit, deleting this file with the four edits:

```sh
git rm docs/pending-workflows/console-node-comments.md
```

**That commit also owes `docs/pending-workflows/README.md` two edits back.** The
`git rm` above takes this file's name out of that page's opening line, whose
second sentence lists the files here that are not workflows: *Six further files
here are not workflows at all* goes back to *Five*. And the section
`## The comment-only fragments` opens *Five files here are **fragments, not
workflows*** and goes back to *Four*, dropping the paragraph this fragment added
to it. Neither edit is one of that section's three reference edits, which belong
to the 2026-11-07 reviewer change and to nothing here. No test can see a dangling
reference either way, because the fragments are `.md` and neither `internal/cli`'s
docs tests nor `website/test/readme.mjs` enumerates them.

## `console-tests.yml:98-101`

**before**:

```
      # The Node version comes from console/package.json's `engines.node`, the
      # field test/unit/pinning.test.ts keeps an exact pin (design section 5.2):
      # a version written here a second time would be a pin that can disagree
      # with the one the console's own tests enforce.
```

**after**:

```
      # The Node version comes from console/package.json's `engines.node`, the
      # field test/unit/pinning.test.ts holds to a floor and the next major as
      # its ceiling (design section 5.2): a version written here a second time
      # would be a pin that can disagree with the one the console's own tests
      # enforce.
```

## `console-linux.yml:113-116` and `console-windows.yml:204-207`

Both carry the same four lines, six spaces in, and both take the same pair.

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

## `console-release.yml:531-534`

**before**:

```
      # The Node version comes from console/package.json's `engines.node`, the
      # field test/unit/pinning.test.ts keeps an exact pin. No step has held a
      # secret yet. No `cache:`: a restored cache is written by other runs, and
      # this job signs what it builds.
```

**after**:

```
      # The Node version comes from console/package.json's `engines.node`, the
      # field test/unit/pinning.test.ts holds to a floor and the next major as
      # its ceiling. No step has held a secret yet. No `cache:`: a restored
      # cache is written by other runs, and this job signs what it builds.
```

## `console-release.yml:1082-1083`

**before**:

```
      # The Node version comes from console/package.json's `engines.node`, the
      # field the console's own pinning test keeps an exact pin.
```

**after**:

```
      # The Node version comes from console/package.json's `engines.node`, the
      # field the console's own pinning test holds to a floor and the next major
      # as its ceiling.
```

## Why the sentence keeps its work and loses one claim

The comment says where the Node version comes from and why it is not written
into the workflow a second time, and both halves are still what a reader needs:
`actions/setup-node` reads the field through `node-version-file`, and the
console's own tests are what hold the field's shape. Only the shape changed — an
exact version to a floor and a ceiling — so the sentence keeps its work and
loses the claim that the shape is an exact pin.
