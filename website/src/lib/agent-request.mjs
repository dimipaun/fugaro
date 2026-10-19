// website/src/lib/agent-request.mjs — the one line the site shows a reader typing at their
// agent (website changes spec of 2026-10-18, item 3; the owner's brief of 2026-10-19, item 1).
//
// Two components render it, and they are the two places the site makes the same claim:
//
//   - the chat's first turn (`AgentChat.astro`), where it is what the reader says before the
//     agent answers with the runs it launched (changes spec item 3), and
//   - the hero's prompt line (`Hero.astro`), which is the owner's brief of 2026-10-19, item 1:
//     the home page has to say, in as few words as possible, that a reader drives Fugaro
//     through the coding agent they already have — and the fewest words that say it are the
//     line they type, with nothing around it.
//
// **One string rather than two copies.** The hero's line and the chat's first turn are the same
// sentence: a reader meets it under the promise and then again as the first thing they say. Two
// literals would be two lines to keep in step, and the day they drifted the page would show the
// reader asking for one thing and then being answered about another. So both read this module,
// and `website/test/home.mjs` holds the two rendered lines to each other and to the document
// below from outside the build.
//
// **It is a line of the changes spec, held to it here.** Item 3 writes the request as its own
// example of what a reader says to their agent, so the line is a quotation rather than copy
// written for the page (Global Constraint 6, D102: a page renders a fact and never restates
// one). A string that stopped being a line of that document — a spec that reworded its example,
// or a component that typed a sentence of its own — stops the build naming the file, which is
// the same guard `AgentChat.astro` keeps over the rest of the conversation.
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
