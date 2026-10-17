// website/test/base-styles.mjs — the page chrome reaches every built page.
//
// The defect this file exists for: `tokens.css` was in every built page and nothing applied
// it, so a browser showed a white page under the palette's pale text, an unstyled bullet
// list for a nav, serif body text and default blue links. `src/styles/base.css` is the layer
// that applies the tokens; these cases hold the *built* pages to carrying it, after the
// tokens, with the chrome it styles (a labelled nav, keyboard-reachable links, the current
// page marked). The colour side — no literal in base.css, every pair it paints checked for
// all three palettes — is in `a11y.mjs`, beside the contrast arithmetic it reuses.
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file.
// No browser, no network: the checks are over the HTML and CSS a browser would receive.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const DIST = path.join(SITE, 'dist')

function builtPages() {
  assert.ok(existsSync(path.join(DIST, 'index.html')), 'dist/index.html is missing: run `npm run build` (npm run check does it first)')
  const pages = []
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name)
      if (entry.isDirectory()) walk(full)
      else if (entry.name === 'index.html') pages.push(full)
    }
  }
  walk(DIST)
  return pages.map((file) => ({ rel: path.relative(DIST, file), html: readFileSync(file, 'utf8') }))
}

// The CSS a page carries: its inline `<style>` blocks, and any first-party stylesheet it
// links (Astro inlines today — `build.inlineStylesheets: 'always'` — but the check does not
// depend on which of the two it emits).
function pageCss(html) {
  const inline = [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1])
  const linked = [...html.matchAll(/<link[^>]*rel="stylesheet"[^>]*href="(\/[^"]+)"/g)].map((m) =>
    readFileSync(path.join(DIST, m[1]), 'utf8'),
  )
  return [...inline, ...linked].join('\n')
}

test('every built page carries the base stylesheet, after the tokens it applies', () => {
  for (const { rel, html } of builtPages()) {
    const css = pageCss(html)
    const tokensAt = css.search(/--ground:\s*#/)
    const body = /body\{background:var\(--ground\);color:var\(--text\);font-family:var\(--font-sans\)/.exec(css)
    assert.ok(tokensAt >= 0, `${rel} carries no palette tokens`)
    assert.ok(body, `${rel} carries no body{background:var(--ground);color:var(--text);...} rule: the tokens are declared and never applied`)
    assert.ok(tokensAt < body.index, `${rel} applies the tokens before it declares them`)
    for (const rule of [/\.site-nav ul\{[^}]*display:flex/, /\.site-nav a\[aria-current=page\]\{/, /:focus-visible\{outline:var\(--focus-ring-width\) solid var\(--accent\)/, /\.site-footer\{/]) {
      assert.match(css, rule, `${rel} is missing the base rule ${rule}`)
    }
  }
})

test('the layout imports the stylesheets statically, tokens first', () => {
  const layout = readFileSync(path.join(SITE, 'src', 'layouts', 'Base.astro'), 'utf8')
  assert.ok(!/import\.meta\.glob\(/.test(layout), 'Base.astro still loads the styles through import.meta.glob: both files exist, so a missing one must fail the build')
  const tokens = layout.indexOf("import '../styles/tokens.css'")
  const base = layout.indexOf("import '../styles/base.css'")
  assert.ok(tokens >= 0 && base >= 0, 'Base.astro does not import tokens.css and base.css')
  assert.ok(tokens < base, 'Base.astro imports base.css before tokens.css')
})

test('the section nav is a labelled landmark of keyboard-reachable links, with the current page marked', () => {
  for (const { rel, html } of builtPages()) {
    const nav = /<nav class="site-nav" aria-label="([^"]+)">([\s\S]*?)<\/nav>/.exec(html)
    assert.ok(nav, `${rel} has no labelled section nav`)
    assert.ok(nav[1].trim().length > 0, `${rel}'s section nav has an empty label`)

    const links = [...nav[2].matchAll(/<a\b([^>]*)>/g)].map((m) => m[1])
    assert.ok(links.length >= 2, `${rel}'s nav has ${links.length} links`)
    for (const attrs of links) {
      // A link is reachable by Tab when it has an href and no negative tabindex.
      assert.match(attrs, /\bhref="[^"]+"/, `${rel}: a nav link without an href is not keyboard-reachable`)
      assert.doesNotMatch(attrs, /tabindex="-/, `${rel}: a nav link is taken out of the tab order`)
    }

    const route = `/${path.dirname(rel) === '.' ? '' : `${path.dirname(rel)}/`}`
    const current = links.filter((attrs) => /aria-current="page"/.test(attrs))
    if (links.some((attrs) => attrs.includes(`href="${route}"`))) {
      assert.equal(current.length, 1, `${rel}: the nav marks ${current.length} links as the current page`)
      assert.match(current[0], new RegExp(`href="${route}"`), `${rel}: the nav marks the wrong link as the current page`)
    }
  }
})

test('the skip link is the first thing in the body and points at main', () => {
  for (const { rel, html } of builtPages()) {
    const body = /<body[^>]*>\s*(<a[^>]*>)/.exec(html)
    assert.ok(body, `${rel}: the body does not open with a link`)
    assert.match(body[1], /class="skip-link"/, `${rel}: the first link is not the skip link`)
    assert.match(body[1], /href="#main"/, `${rel}: the skip link does not point at #main`)
    assert.match(html, /<main id="main"/, `${rel}: there is no main#main for the skip link to reach`)
  }
})
