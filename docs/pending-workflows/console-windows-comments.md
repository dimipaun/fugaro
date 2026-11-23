# `console-windows.yml`: the comment the Node range invalidated

**Status: staged, comment-only, not applied.** `.github/workflows/console-windows.yml`
carries a comment saying `console/package.json`'s `engines.node` is a field
`test/unit/pinning.test.ts` keeps **an exact pin**. The field is a **range**
since **2026-11-22** (`#3566`): `>=22.23.3 <23`, a floor and the next major as
its ceiling — `console/package.json`'s own description and `console/README.md`'s
"The Node is the one field pinned to a range" paragraph. `test/unit/pinning.test.ts`
carves the field out of the exact-pin rule for it: its `enginesNodeRE` accepts a
floor and the next major and nothing else, an exact version included. The comment
is therefore false, and this file holds the corrected text. **No executable line
changes**, and the check below proves it.

It is a `.md` file rather than a `.yml` one: a `console-windows-comments.yml`
would be enumerated as a pending workflow by `internal/cli`'s docs tests and by
`website/test/readme.mjs`, both of which read every `.ya?ml` in this directory,
and it would have to be named in this document's opening line and in
`website/README.md` — none of which is a true statement about a comment
correction.

Like `console-linux-comments.md`, this fragment is applied to **one** file:
`.github/workflows/console-windows.yml` has no staged twin, so there is no `cp`
step and no byte-identity clause to keep.

## How to apply

Edit `.github/workflows/console-windows.yml`: find the `before` block below and
put the `after` block in its place, changing nothing else. Then prove no
executable line moved:

```sh
git diff -U0 -- .github/workflows/console-windows.yml \
  | grep -E '^[+-]' | grep -v -E '^(\+\+\+|---)' | grep -v -E '^[+-][[:space:]]*(#|$)'
```

It must print nothing: every changed line is a comment or a blank one. Then
commit, deleting this file in the same commit:

```sh
git rm docs/pending-workflows/console-windows-comments.md
```

**That commit also owes `docs/pending-workflows/README.md` its references back.**
This fragment is named in that page's opening line and in its section
`## The comment-only fragments`, whose three reference edits are written out
there as exact text. **One** of them is this file's, and it goes in the same
commit: the opening line's second sentence begins *Seven further files here are
not workflows at all* and goes back to *Six*, its list losing this file's name
and keeping `ci-hygiene.yml.snippet`, which is not a comment-only fragment and
which no `git rm` here retires. No test can see a dangling reference here,
because the fragments are `.md` and neither `internal/cli`'s docs tests nor
`website/test/readme.mjs` enumerates them.

## The pin's shape

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

## Why the sentence keeps its work and loses one claim

The comment says where the Node version comes from and why it is not written
into the workflow a second time, and both halves are still what a reader needs:
`actions/setup-node` reads the field through `node-version-file`, and the
console's own tests are what hold the field's shape. Only the shape changed — an
exact version to a floor and a ceiling — so the sentence keeps its work and
loses the claim that the shape is an exact pin.
