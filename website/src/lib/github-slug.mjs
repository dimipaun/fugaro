// website/src/lib/github-slug.mjs — the anchor a heading text becomes, as GitHub makes it.
//
// Two places need this rule and both of them need the *same* one: the docs hub
// (`src/pages/docs.astro`) resolves the anchors it links against the headings of the files
// it links — a link to a section somebody renamed is a 404 for a reader — and the hub's own
// check (`test/docs-hub.mjs`) resolves the same anchors to do it. While each file carried
// its own approximation the two could disagree, and both could disagree with the `id`s a
// reader's browser actually finds: `docs.astro` dropped underscores, GitHub keeps them, and
// the check unwrapped `[text](url)` markdown links while the page did not. The first guide
// heading with an underscore in it would have linked a fragment GitHub does not have, and
// the check would have agreed with the bug.
//
// The rule is GitHub's own, as `github-slugger` implements it and as Astro's markdown
// pipeline applies it to a rendered heading when it writes the `id`s a page is read at:
//
//   lower-case the text, drop the punctuation, turn each space into a hyphen.
//
// Three details are the ones an approximation gets wrong, and all three are pinned by the
// cases in `test/docs-hub.mjs`:
//
//   - **`_` and `-` survive.** They sit inside the punctuation ranges `github-slugger`
//     removes and are excluded from them by hand, which is why `fugaro_run_id` keeps its
//     underscores and `fugaro-run-id` keeps its hyphens.
//   - **Only U+0020 becomes a hyphen.** A tab is dropped rather than turned into one, and a
//     run of two spaces becomes two hyphens; nothing is trimmed or collapsed.
//   - **Markdown that does not survive rendering is not part of the text.** Backticks and
//     `*` are punctuation and fall out of the class already; a link's `[text](url)` does
//     not, so `headingText` reduces it to its text before the rule is applied — a heading
//     written `## [Getting started](#getting-started)` anchors at `getting-started` in
//     GitHub's own rendering, and the page has to link what GitHub anchors.
//
// What it removes is Unicode's punctuation, symbols, control, format and separator
// characters, except the three that survive — the same set `github-slugger`'s generated
// character class names, one code point at a time. Written as property escapes rather than
// as that class's 8 KB of ranges, and checked against it: every heading of every markdown
// file in this repository (7553 of them at W1.7) slugs identically under the two, and
// `test/docs-hub.mjs` resolves every command heading of the generated CLI reference through
// this function against the `id`s the built page carries, so a disagreement with the
// renderer fails the site's own check rather than a reader's click.
//
// Nothing here depends on `github-slugger` itself: the site ships no dependency for its own
// benefit (Global Constraint 5), and this is a four-line rule, not a package.

/** The characters GitHub drops, but for the three a slug keeps. */
const DROPPED = /(?![ _-])[\p{P}\p{S}\p{C}\p{Z}]/gu

/**
 * The text a markdown heading line renders as: its inline links reduced to their own text,
 * because the anchor is made from what a reader sees and not from the markdown —
 * `[Text](url)` anchors at `text`, and an image at its alt text. Code spans need no
 * unwrapping: the backticks are punctuation and `DROPPED` takes them.
 */
export function headingText(line) {
  return line.replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
}

/**
 * The anchor GitHub gives a heading whose rendered text is `text`.
 *
 * The text is a *rendered* heading's text, so the markdown that would not survive rendering
 * is not part of it: `headingText` does that reduction for a markdown heading line, and a
 * caller holding a line rather than a rendered string wants `headingAnchor`.
 */
export function githubSlug(text) {
  return text.toLowerCase().replace(DROPPED, '').replace(/ /g, '-')
}

/** The anchor of a markdown heading line: the anchor of the text it renders as. */
export function headingAnchor(line) {
  return githubSlug(headingText(line.trim()))
}
