// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The documents the console serves, and the CSP that governs them
// (desktop-console plan Task 3.4a, steps 4 and 5; design section 5.1 rules 1, 2,
// 3 and 7).
//
// The dashboard document loads a script in this task, and that is the whole
// reason this file exists: `test/unit/window.test.ts` asserts the CSP the
// handler *sends* and that the `<meta>` copy in each document matches it, and
// this one asserts what the documents do with what they are allowed. The two
// halves are different questions -- a policy says what a document may do, and a
// document says what it does -- and the second is the one a review of "the
// renderer bundle the document loads" has to be able to read:
//
//   - **the CSP is the design's, byte for byte**, with no directive added and
//     none dropped (a literal copied by hand, so the expectation cannot be
//     assembled from the implementation);
//   - **`script-src` is exactly `'self'`**: one origin, no `'unsafe-inline'`, no
//     `'unsafe-eval'`, no `*`;
//   - **every served document carries that policy as a `<meta>` copy**, byte for
//     byte, and the copies are the same string as each other -- a refused
//     console's document is exactly as constrained as the dashboard's;
//   - **the dashboard loads exactly one script**, `<script src="./console.js"
//     defer>`, and it is a file the served root actually holds; and **the
//     refusal document loads none at all**;
//   - **every document links exactly one stylesheet** (Task 6.2),
//     `<link rel="stylesheet" href="./console.css">` -- one element, from this
//     origin, with no `media`, `onload` or `integrity` -- and the served root
//     actually holds the file it names, served as `text/css` under this same
//     policy;
//   - **no document holds inline script text, an `on*` attribute, an inline
//     style, or a `javascript:` URL.**
//
// The last three are the ones a mutation would break, and each is written over
// the documents' *markup* -- HTML comments stripped -- because a comment is not
// an element and this tree's comments talk about scripts on purpose. Stripping
// comments is not a weakening: a script inside a comment does not run, and the
// check that a script is not there is about what the browser parses.
//
// Everything here reads the **served** documents (`dist/bundle/`, built by
// `test/support/built.ts` if it is stale) rather than `src/renderer/`: since
// Task 3.4a the handler's root is the built tree, and a test over the sources
// would be a test of a file the console does not serve. That the build copies
// them verbatim is `test/unit/bundle.test.ts`'s assertion.

import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  CONTENT_SECURITY_POLICY,
  CSP_HEADER,
  SERVED_FILES,
  mediaTypeFor,
  resolveBundleRequest,
  serveAppRequest,
} from '../../src/main/protocol'
import { CONFIRM_ENTRY_PATH } from '../../src/main/confirm'
import { BUNDLE_DIR, builtFile, builtListing } from '../support/built'

// The design's own CSP, section 5.1 rule 3, copied by hand rather than built
// from the constant it is compared against: a test that assembled its
// expectation from the implementation would agree with any implementation.
const DESIGN_CSP =
  "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'"

/** DOCUMENTS are the served documents, in the order `SERVED_FILES` names them:
 * derived from the list rather than written out, so a third document is covered
 * the day it is served. */
const DOCUMENTS: readonly string[] = SERVED_FILES.filter((path) => path.endsWith('.html')).map((path) =>
  path.replace(/^\//, ''),
)

/** served is one served document's bytes. */
function served(name: string): string {
  return builtFile(`bundle/${name}`)
}

/** markup is a document with its HTML comments removed. Every structural claim
 * below is made over this: a comment is not an element, and this tree's comments
 * quote the very markup the claims are about. */
function markup(document: string): string {
  return document.replace(/<!--[\s\S]*?-->/g, '')
}

/** directives is the CSP parsed into its directive map, so a claim can be about
 * one directive rather than about the string's shape. */
function directives(policy: string): Record<string, string> {
  const map: Record<string, string> = {}
  for (const directive of policy.split('; ')) {
    const [name, ...sources] = directive.split(' ')
    if (name !== undefined) map[name] = sources.join(' ')
  }
  return map
}

/** metaPolicy is the policy a document carries in its `<meta http-equiv>` copy,
 * or `null` when it carries none. */
function metaPolicy(document: string): string | null {
  const match = /<meta\s+http-equiv="Content-Security-Policy"\s+content="([^"]*)"/.exec(document)
  return match?.[1] ?? null
}

/** bodyMarkup is a document's `<body>` contents -- comments removed, like every
 * other structural claim here -- or `null` when it has no body element. */
function bodyMarkup(document: string): string | null {
  const match = /<body[^>]*>([\s\S]*)<\/body>/i.exec(markup(document))
  return match === null ? null : (match[1] ?? '').trim()
}

/** scriptElements is every `<script ...>` opening tag of a document's markup. */
function scriptElements(document: string): readonly string[] {
  return [...markup(document).matchAll(/<script\b[^>]*>/gi)].map((match) => match[0])
}

/** linkElements is every `<link ...>` opening tag of a document's markup. */
function linkElements(document: string): readonly string[] {
  return [...markup(document).matchAll(/<link\b[^>]*>/gi)].map((match) => match[0])
}

describe('the console’s CSP', () => {
  it('is the design’s, byte for byte', () => {
    expect(CONTENT_SECURITY_POLICY).toBe(DESIGN_CSP)
  })

  it('has the design’s ten directives and no others', () => {
    // The set as well as the string: a directive added to the end would be a
    // change to the string too, but a test that says *which* directives are
    // there is the one a reviewer reads to see that nothing was dropped.
    expect(Object.keys(directives(CONTENT_SECURITY_POLICY)).sort()).toEqual([
      'base-uri',
      'connect-src',
      'default-src',
      'font-src',
      'form-action',
      'frame-ancestors',
      'img-src',
      'object-src',
      'script-src',
      'style-src',
    ])
  })

  it('allows scripts from this origin and from nowhere else', () => {
    expect(directives(CONTENT_SECURITY_POLICY)['script-src']).toBe("'self'")
    expect(directives(CONTENT_SECURITY_POLICY)['default-src']).toBe("'none'")
    expect(directives(CONTENT_SECURITY_POLICY)['connect-src']).toBe("'none'")
    for (const loosening of ['unsafe-inline', 'unsafe-eval', 'unsafe-hashes', 'strict-dynamic', '*', 'http:']) {
      expect(CONTENT_SECURITY_POLICY, `the CSP allows ${loosening}`).not.toContain(loosening)
    }
  })

  it('allows styles from this origin and from nowhere else (Task 6.2)', () => {
    // The directive the served stylesheet is admitted by, and the whole of the
    // change Task 6.2 makes to the policy: **none**. `style-src 'self'` was the
    // third directive before the `<link>` existed, so a stylesheet from
    // `app://console` is a stylesheet this policy always allowed -- and the
    // mutation this case refuses is the one a reviewer is told to look for, a
    // single token added to it. `window.test.ts` pins the same string against
    // the design's own wording; this is the directive read on its own.
    expect(directives(CONTENT_SECURITY_POLICY)['style-src']).toBe("'self'")
    for (const loosening of ['unsafe-inline', 'unsafe-eval', 'unsafe-hashes', '*', 'http:', 'https:']) {
      expect(directives(CONTENT_SECURITY_POLICY)['style-src'], `style-src allows ${loosening}`).not.toContain(loosening)
    }
  })
})

describe('the served documents', () => {
  it('are the four documents of SERVED_FILES, and each is non-empty', () => {
    // The non-vacuity guard for every claim below: an empty list, or a document
    // that was not there, would make them all true by looking at nothing.
    expect(DOCUMENTS).toEqual(['confirm.html', 'index.html', 'installations.html', 'refusal.html'])
    for (const name of DOCUMENTS) expect(served(name).length, `${name} is empty`).toBeGreaterThan(0)
  })

  it('carry the CSP as a meta copy of the header, byte for byte', () => {
    const copies = DOCUMENTS.map((name) => metaPolicy(served(name)))
    for (const [index, copy] of copies.entries()) {
      const name = DOCUMENTS[index] ?? ''
      expect(copy, `${name} carries no CSP meta copy`).not.toBeNull()
      // Byte for byte, and the same string in every document: a copy that had
      // drifted by one byte would be a document with a *different* policy from
      // the one the handler sends.
      expect(copy, `${name}'s meta copy is not the header's policy`).toBe(CONTENT_SECURITY_POLICY)
    }
    for (const copy of copies) expect(copy).toBe(copies[0])
  })

  it('give the dashboard exactly one script, from this origin', () => {
    const scripts = scriptElements(served('index.html'))
    expect(scripts.length, 'the dashboard document loads no script at all: the entry would never run').toBe(1)
    const script = scripts[0] ?? ''
    // Exactly the one element, and exactly its two attributes: a second script
    // element, an `async` one, one with a `type` that changed how it runs, or
    // one from another origin all fail here.
    expect(script).toMatch(/^<script\s+src="\.\/console\.js"\s+defer\s*>$/)
    expect(script).not.toContain('type=')
    expect(script).not.toContain('async')
    // And the file it names is one the served root holds, so the document's one
    // script cannot be a 404.
    expect(builtListing('bundle')).toContain('console.js')
  })

  it('give the picker exactly one script, its own bundle, from this origin (Task 3.4b)', () => {
    const scripts = scriptElements(served('installations.html'))
    expect(scripts.length, 'the picker document loads no script at all').toBe(1)
    expect(scripts[0] ?? '').toMatch(/^<script\s+src="\.\/installations\.js"\s+defer\s*>$/)
    // Never the dashboard's bundle: the picker's document has its own preload
    // and its own surface, and the dashboard's screens are not on it.
    expect(markup(served('installations.html'))).not.toContain('console.js')
    expect(builtListing('bundle')).toContain('installations.js')
  })

  it('give the confirmation document exactly one script, its own bundle, from this origin (V2.5.1a)', () => {
    // The confirmation window's document (design section 5.1 rule 11) loads its
    // own bundle and nothing else: the dashboard's entry and the picker's are
    // other windows' surfaces, and this window's preload exposes two methods.
    const scripts = scriptElements(served('confirm.html'))
    expect(scripts.length, 'the confirmation document loads no script at all').toBe(1)
    expect(scripts[0] ?? '').toMatch(/^<script\s+src="\.\/confirm\.js"\s+defer\s*>$/)
    expect(markup(served('confirm.html'))).not.toContain('console.js')
    expect(markup(served('confirm.html'))).not.toContain('installations.js')
    expect(builtListing('bundle')).toContain('confirm.js')
    // And the entry path the window is opened on is the document this list
    // serves, with the script beside it: a path the handler does not list is a
    // 404 in a window that then cancels (see `did-navigate` in
    // `src/main/confirm.ts`).
    expect(SERVED_FILES).toContain(CONFIRM_ENTRY_PATH)
    expect(SERVED_FILES).toContain('/confirm.js')
  })

  it('leave the dashboard document nothing of its own to draw', () => {
    // The entry draws the whole window (Task 3.4c): the installation header, the
    // navigation and the panel are appended to the document's body by the one
    // script it loads. Task 1.1's placeholder -- "The console's window is up. It
    // has no data yet." -- was true of the empty frame and became false the day
    // the entry mounted Live, so a document that kept it would print a second
    // `<h1>` and a false sentence above the figures on every run.
    expect(bodyMarkup(served('index.html'))).toBe('')
    // The non-vacuity guard, and the boundary of the claim: the refusal document
    // *is* a drawing -- it is the whole of what a refused console shows -- so a
    // helper that read no body at all would have to fail here.
    expect(bodyMarkup(served('refusal.html'))).not.toBe('')
  })

  it('give the refusal document no script at all', () => {
    // The point of the refusal document (design D7): a refused console runs no
    // renderer entry, in a window that has no dashboard root for one to draw
    // into. `window.test.ts` pins the window; this pins the document.
    expect(scriptElements(served('refusal.html'))).toEqual([])
    expect(markup(served('refusal.html'))).not.toContain('console.js')
  })

  it('give every document exactly one stylesheet, from this origin (Task 6.2)', () => {
    // One `<link>` per document, and the same one in all three: `./console.css`,
    // a relative path on this origin, resolved by the bundler as a literal and
    // by the handler as a path under the root. The refusal document is included
    // on purpose -- the one page a console below the floor shows must not be the
    // unstyled one -- and it is asserted here rather than trusted, because a
    // `<link>` that had been left out of one document is exactly the shape of
    // this task's own mutation.
    for (const name of DOCUMENTS) {
      const links = linkElements(served(name))
      expect(links.length, `${name} links ${String(links.length)} stylesheets, and exactly one is allowed`).toBe(1)
      const link = links[0] ?? ''
      // Exactly the one element and exactly its two attributes: a second
      // stylesheet, a remote one, a `media` that hid the file on the console's
      // only rendering mode, an `onload` (an inline handler, which
      // `style-src`'s `'self'` does not back) or an `integrity` nobody
      // maintains all fail here.
      expect(link, `${name}'s stylesheet is not the one this origin serves`).toMatch(
        /^<link\s+rel="stylesheet"\s+href="\.\/console\.css"\s*\/?>$/,
      )
      expect(link, `${name}'s stylesheet carries an attribute this file must not have`).not.toMatch(
        /\s(?:media|onload|integrity|crossorigin|type|disabled|nonce)\s*=/i,
      )
    }
    // And the file it names is one the served root holds, so the document's one
    // stylesheet cannot be a 404.
    expect(builtListing('bundle')).toContain('console.css')
  })

  it('are served the stylesheet under the same policy, as text/css, from the same root (Task 6.2)', async () => {
    // The other half of the claim, and the one a reviewer of a
    // SECURITY-SENSITIVE task should require: the stylesheet is served by the
    // same `serveAppRequest` as the documents, out of the same root, with the
    // allow-list's own media type and the CSP header on the response. Nothing
    // about the response is special-cased for CSS, which is what "the policy is
    // unchanged" has to mean to be worth anything.
    expect(mediaTypeFor('/console.css')).toBe('text/css; charset=utf-8')
    expect(resolveBundleRequest(BUNDLE_DIR, 'app://console/console.css')).toEqual({
      ok: true,
      filePath: join(BUNDLE_DIR, 'console.css'),
      mediaType: 'text/css; charset=utf-8',
    })
    const response = await serveAppRequest(BUNDLE_DIR, 'app://console/console.css')
    expect(response.status).toBe(200)
    expect(response.headers.get('content-type')).toBe('text/css; charset=utf-8')
    expect(response.headers.get(CSP_HEADER)).toBe(CONTENT_SECURITY_POLICY)
    expect(response.headers.get('x-content-type-options')).toBe('nosniff')
    await expect(response.text()).resolves.toBe(builtFile('bundle/console.css'))
  })

  it('hold no inline script text, no handler attribute and no javascript: URL', () => {
    for (const name of DOCUMENTS) {
      const text = markup(served(name))
      // Every script element has a `src` (the test above pins the count), so no
      // script element holds a body: this is the belt-and-braces half, and it
      // catches a script element written with a body and a src.
      for (const body of [...text.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)]) {
        expect((body[1] ?? '').trim(), `${name} holds inline script text`).toBe('')
      }
      expect(text, `${name} holds an on* handler attribute`).not.toMatch(/\son[a-z]+\s*=/i)
      expect(text, `${name} holds a javascript: URL`).not.toMatch(/javascript:/i)
      // `style-src 'self'` allows no inline style, so a `style` attribute is a
      // claim the policy does not back: the theme travels as `data-theme`.
      expect(text, `${name} holds an inline style attribute`).not.toMatch(/\sstyle\s*=/i)
      expect(text, `${name} holds a <style> element`).not.toContain('<style')
    }
  })
})
