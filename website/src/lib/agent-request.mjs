// website/src/lib/agent-request.mjs — the one line the site shows a reader typing at their
// agent (website changes spec of 2026-10-18, item 3; the owner's feedback of 2026-10-19).
//
// One component renders it — the hero's prompt line (`Hero.astro`), which is the owner's
// feedback of 2026-10-19: the home page has to say, in as few words as possible, that a reader
// drives Fugaro through the coding agent they already have, and the fewest words that say it are
// the line they type, with nothing around it. It was two places until the draft of 2026-10-19
// folded the chat out of the page: the line was also the chat's first turn (`AgentChat.astro`),
// where it was what the reader said before the agent answered with the runs it launched
// (changes spec item 3).
//
// **The line lives here rather than in the component that renders it** so that the claim and the
// document behind it are one file: the line is a quotation, and the guard at the foot of this
// module is what holds it to the spec. `website/test/home.mjs` reads the rendered line and its
// `data-source` back off the built page, so the artifact is held to the same document from
// outside the build.
//
// **It is a line of the changes spec, held to it here.** Item 3 writes the request as its own
// example of what a reader says to their agent, so the line is a quotation rather than copy
// written for the page (Global Constraint 6, D102: a page renders a fact and never restates
// one). A string that stopped being a line of that document — a spec that reworded its example,
// or a component that typed a sentence of its own — stops the build naming the file.
//
// Standard library only, plus the repository root the content pipeline already works out: this
// module is read by the build, and it computes one comparison. The root is imported rather than
// derived from this file's own URL because the bundler rewrites that URL to the build's output
// directory, where walking up three levels lands outside the repository — `content.ts` finds the
// site by looking for `astro.config.mjs`, which is right in the build and out of it.

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO_ROOT } from '../content.ts'

/** The document the request is a line of, as every message about it names it. */
export const REQUEST_SOURCE = 'docs/website/redesign/changes-spec-2026-10-18.md'

/**
 * What the reader types at their agent. The spec writes it inside a sentence ("the user asks
 * their agent something like \"Fan out these three bug fixes.\""), and what a page shows is the
 * quoted sentence itself — the words a reader would put in the box.
 */
export const REQUEST = 'Fan out these three bug fixes.'

/**
 * A string with its markdown markers and its whitespace removed: how the request and the
 * document it claims to come from are compared, the same reading the components use. Code spans
 * and emphasis are markers rather than text.
 */
function comparable(document) {
  return document.replace(/[`*]/g, '').replace(/\s+/g, '')
}

{
  const document = readFileSync(path.join(REPO_ROOT, REQUEST_SOURCE), 'utf8')
  if (!comparable(document).includes(comparable(REQUEST))) {
    throw new Error(
      `website/src/lib/agent-request.mjs: the request ${JSON.stringify(REQUEST)} is not a line of ${REQUEST_SOURCE}, and the site shows the reader a line that document writes rather than one written for the page (changes spec 2026-10-18, item 3; Global Constraint 6). Either the spec reworded its example or the line here was edited.`,
    )
  }
}
