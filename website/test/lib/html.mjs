// website/test/lib/html.mjs — the reader the site's gates stand on (website plan, W1.11).
//
// The gates (`linkcheck.mjs`, `a11y.mjs`, `origins.mjs`, `budget.mjs` and the redesign's
// `meta.mjs`) all read the built artifact, and they read it with this one module so that what
// counts as "a link", "a script" or "a heading" cannot differ between them. It lives under `test/lib/` rather than
// beside them because `npm run check` runs `node --test test/*.mjs`: a helper at the top level
// would be run as a test file of its own, and a test file imported by another registers its
// cases twice. Its own cases are in `origins.mjs` (the scanner section), which is the gate
// whose failure mode is a security one.
//
// It is a small, STRICT reader of the HTML Astro writes, not a browser's error-recovering
// parser. It refuses what it does not understand — an unterminated tag, comment or quoted
// value, a duplicated attribute, a stray `<`, a mismatched end tag, an unknown named entity
// inside an attribute — because a gate that skipped a construct it could not read is
// exactly the gate a third-party origin would arrive through. If Astro ever writes a
// construct this reader refuses, the fix is to teach it here, with a case, rather than to
// make it lenient.
//
// Standard library only, and no network (Global Constraint 4).

import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

export const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
export const REPO = path.dirname(SITE)
// The built artifact the gates read. `FUGARO_GATES_DIST` points them at another directory,
// which is how a gate is mutation-proved by hand: copy `dist/`, plant a defect in the copy,
// run the gate file against it and watch it fail. `npm run check` never sets it.
export const DIST = process.env.FUGARO_GATES_DIST ? path.resolve(process.env.FUGARO_GATES_DIST) : path.join(SITE, 'dist')

// Elements that never have content or an end tag.
export const VOID = new Set([
  'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link', 'meta', 'source', 'track', 'wbr',
])

// Elements whose content is raw text: nothing inside is markup until the matching end tag.
const RAW_TEXT = new Set(['script', 'style'])
// Elements whose content is text with entities but no markup.
const RCDATA = new Set(['title', 'textarea'])

// The named entities a built page may carry. Astro escapes with the first five; the rest are
// the ones markdown and hand-written copy actually reach for. An unknown one in an attribute
// is refused (an attribute is where a URL lives); in text it is kept as written.
const NAMED = new Map([
  ['amp', '&'], ['lt', '<'], ['gt', '>'], ['quot', '"'], ['apos', "'"], ['nbsp', ' '],
  ['mdash', '—'], ['ndash', '–'], ['hellip', '…'], ['rsquo', '’'],
  ['lsquo', '‘'], ['rdquo', '”'], ['ldquo', '“'], ['copy', '©'],
  ['middot', '·'], ['rarr', '→'], ['larr', '←'], ['times', '×'],
])

export class HTMLScanError extends Error {}

function lineOf(src, at) {
  let line = 1
  for (let i = 0; i < at && i < src.length; i++) if (src.charCodeAt(i) === 10) line++
  return line
}

function fail(src, at, message) {
  throw new HTMLScanError(`line ${lineOf(src, at)}: ${message}`)
}

/** Decode character references. `strict` refuses an unknown named one. */
export function decodeEntities(text, strict = false) {
  return text.replace(/&(#[xX][0-9a-fA-F]+|#[0-9]+|[a-zA-Z][a-zA-Z0-9]*);/g, (whole, ref) => {
    if (ref[0] === '#') {
      const code = ref[1] === 'x' || ref[1] === 'X' ? parseInt(ref.slice(2), 16) : parseInt(ref.slice(1), 10)
      if (!Number.isFinite(code) || code > 0x10ffff) throw new HTMLScanError(`bad character reference ${whole}`)
      return String.fromCodePoint(code)
    }
    if (NAMED.has(ref)) return NAMED.get(ref)
    if (strict) throw new HTMLScanError(`unknown named entity ${whole} in an attribute`)
    return whole
  })
}

const NAME = /[a-zA-Z][a-zA-Z0-9:-]*/y
const ATTR_NAME = /[^\s"'>\/=<`]+/y

/**
 * Tokenize a page. Returns a flat list of tokens:
 *   { kind: 'doctype' }, { kind: 'comment', text },
 *   { kind: 'start', name, attrs: Map, selfClosing, raw?, at },
 *   { kind: 'end', name, at }, { kind: 'text', text }
 * `raw` is the verbatim content of a script or style element (its end token follows).
 */
export function tokenize(src) {
  const tokens = []
  let i = 0
  const n = src.length
  while (i < n) {
    const lt = src.indexOf('<', i)
    if (lt === -1) {
      tokens.push({ kind: 'text', text: decodeEntities(src.slice(i)) })
      break
    }
    if (lt > i) tokens.push({ kind: 'text', text: decodeEntities(src.slice(i, lt)) })
    i = lt
    if (src.startsWith('<!--', i)) {
      const end = src.indexOf('-->', i + 4)
      if (end === -1) fail(src, i, 'unterminated comment')
      tokens.push({ kind: 'comment', text: src.slice(i + 4, end) })
      i = end + 3
      continue
    }
    if (/^<!doctype/i.test(src.slice(i, i + 9))) {
      const end = src.indexOf('>', i)
      if (end === -1) fail(src, i, 'unterminated doctype')
      tokens.push({ kind: 'doctype' })
      i = end + 1
      continue
    }
    if (src[i + 1] === '/') {
      NAME.lastIndex = i + 2
      const m = NAME.exec(src)
      if (!m) fail(src, i, 'an end tag with no name')
      let j = NAME.lastIndex
      while (j < n && /\s/.test(src[j])) j++
      if (src[j] !== '>') fail(src, i, `malformed end tag </${m[0]}`)
      tokens.push({ kind: 'end', name: m[0].toLowerCase(), at: i })
      i = j + 1
      continue
    }
    NAME.lastIndex = i + 1
    const m = NAME.exec(src)
    if (!m) fail(src, i, `a stray "<" that does not open a tag (escape it as &lt;)`)
    const name = m[0].toLowerCase()
    const attrs = new Map()
    let j = NAME.lastIndex
    let selfClosing = false
    for (;;) {
      const before = j
      while (j < n && /\s/.test(src[j])) j++
      if (j >= n) fail(src, i, `unterminated <${name}> tag`)
      if (src[j] === '>') { j++; break }
      if (src[j] === '/' && src[j + 1] === '>') { selfClosing = true; j += 2; break }
      if (j === before) fail(src, j, `<${name}>: attributes must be separated by whitespace`)
      ATTR_NAME.lastIndex = j
      const a = ATTR_NAME.exec(src)
      if (!a) fail(src, j, `<${name}>: unreadable attribute`)
      const attr = a[0].toLowerCase()
      j = ATTR_NAME.lastIndex
      let value = ''
      let k = j
      while (k < n && /\s/.test(src[k])) k++
      if (src[k] === '=') {
        k++
        while (k < n && /\s/.test(src[k])) k++
        const q = src[k]
        if (q === '"' || q === "'") {
          const close = src.indexOf(q, k + 1)
          if (close === -1) fail(src, k, `<${name} ${attr}>: unterminated quoted value`)
          value = src.slice(k + 1, close)
          j = close + 1
        } else {
          const um = /[^\s"'=<>`]+/y
          um.lastIndex = k
          const v = um.exec(src)
          if (!v) fail(src, k, `<${name} ${attr}>: missing value after "="`)
          value = v[0]
          j = um.lastIndex
        }
        try {
          value = decodeEntities(value, true)
        } catch (err) {
          fail(src, k, `<${name} ${attr}>: ${err.message}`)
        }
      }
      if (attrs.has(attr)) fail(src, i, `<${name}>: duplicated attribute "${attr}"`)
      attrs.set(attr, value)
    }
    const token = { kind: 'start', name, attrs, selfClosing, at: i }
    tokens.push(token)
    i = j
    if ((RAW_TEXT.has(name) || RCDATA.has(name)) && !selfClosing) {
      const re = new RegExp(`</${name}\\s*>`, 'ig')
      re.lastIndex = i
      const close = re.exec(src)
      if (!close) fail(src, token.at, `<${name}> is never closed`)
      const body = src.slice(i, close.index)
      if (RAW_TEXT.has(name)) token.raw = body
      else if (body) tokens.push({ kind: 'text', text: decodeEntities(body) })
      tokens.push({ kind: 'end', name, at: close.index })
      i = close.index + close[0].length
    }
  }
  return tokens
}

/**
 * Parse a page into a tree of { name, attrs, children, parent, raw } nodes, with text nodes
 * as { text }. A mismatched or unexpected end tag is refused.
 */
export function parse(src) {
  const root = { name: '#document', attrs: new Map(), children: [], parent: null }
  let cur = root
  for (const t of tokenize(src)) {
    if (t.kind === 'text') {
      cur.children.push({ text: t.text, parent: cur })
    } else if (t.kind === 'start') {
      const node = { name: t.name, attrs: t.attrs, children: [], parent: cur }
      if (t.raw !== undefined) node.raw = t.raw
      cur.children.push(node)
      if (!VOID.has(t.name) && !t.selfClosing) cur = node
    } else if (t.kind === 'end') {
      if (VOID.has(t.name)) fail(src, t.at, `an end tag for the void element <${t.name}>`)
      if (cur.name !== t.name) {
        fail(src, t.at, `</${t.name}> closes <${cur.name}>: the reader refuses a mismatched end tag rather than guessing`)
      }
      cur = cur.parent
    }
  }
  if (cur !== root) throw new HTMLScanError(`<${cur.name}> is never closed`)
  return root
}

/** Every element node under `node`, in document order. */
export function* walk(node) {
  for (const child of node.children ?? []) {
    if (child.name === undefined) continue
    yield child
    yield* walk(child)
  }
}

export function find(node, name) {
  for (const el of walk(node)) if (el.name === name) return el
  return null
}

export function findAll(node, pred) {
  const out = []
  for (const el of walk(node)) if (typeof pred === 'string' ? el.name === pred : pred(el)) out.push(el)
  return out
}

/** The text a node renders, with an image's alt standing in for the image. */
export function textOf(node) {
  if (node.text !== undefined) return node.text
  if (node.name === 'img') return node.attrs.get('alt') ?? ''
  if (node.raw !== undefined) return ''
  return (node.children ?? []).map(textOf).join('')
}

// The elements a browser lays out as blocks: the text on either side of one is not the same
// run of words, so a reader's word boundary is there even when the source wrote none.
// Everything else is inline, and inline markup joins its neighbours verbatim. The document's
// own containers are here too: a page's `<title>` is not laid out beside its body, so its text
// is not part of the same run of words as the first thing a reader reads.
export const BLOCK = new Set([
  'address', 'article', 'aside', 'blockquote', 'body', 'br', 'dd', 'div', 'dl', 'dt',
  'fieldset', 'figcaption', 'figure', 'footer', 'form', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'head', 'header', 'hr', 'html', 'li', 'main', 'nav', 'noscript', 'ol', 'p', 'pre',
  'section', 'table', 'tbody', 'td', 'template', 'tfoot', 'th', 'thead', 'title', 'tr', 'ul',
])

/**
 * The runs of text a reader sees inside a node, in document order, as a flat list of strings:
 * each direct text child, and the text a leaf element carries, with **no space invented
 * between them**. Two runs that end up next to each other in this list are adjacent on the
 * page, so a run that ends in a letter directly followed by one that starts in a letter is one
 * glued word — `back and<code>fugaro secrets</code>` reads as "andfugaro", in either case.
 * That defect is invisible in the built file's source and invisible to a reader that joins a
 * node's children with a space as well, because the space it invents is the space that went
 * missing (see `spacing.mjs`, whose rule this is).
 *
 * An inline element is not a word boundary: `<em>foo</em>bar` is the run `foo` and then the
 * run `bar`. A block element is laid out apart from its neighbours, so its text is fenced with
 * a single space on each side and that space is the list's marker for "a boundary a reader
 * sees". `raw` content (a script or style body) and a `hidden` element contribute nothing,
 * and an image contributes its alt text, as `textOf` above does.
 *
 * `isBlock` is the caller's answer to what is laid out apart, and it defaults to the list
 * above by tag name. A page whose own stylesheet lays an element out as a block passes its own
 * answer (`spacing.mjs` reads the page's stylesheet for exactly that), because a class that
 * the site displays as a block is a boundary a reader sees just as a `<p>` is.
 */
export function textRuns(node, isBlock = (el) => BLOCK.has(el.name)) {
  if (node.raw !== undefined || node.attrs?.has('hidden')) return []
  if (node.text !== undefined) return [node.text]
  const children = node.children ?? []
  if (children.length === 0) return node.name === 'img' ? [node.attrs.get('alt') ?? ''] : []
  const runs = []
  for (const child of children) {
    const inner = textRuns(child, isBlock)
    if (inner.length === 0) {
      if (isBlock(child)) runs.push(' ')
      continue
    }
    if (isBlock(child)) runs.push(' ', ...inner, ' ')
    else runs.push(...inner)
  }
  return runs
}

/** The text a reader sees inside a node, block boundaries included, with nothing invented
 *  between two neighbours a browser runs together. */
export function verbatimText(node, isBlock) {
  return textRuns(node, isBlock).join('')
}

/**
 * The places two runs of text are one word to a reader, because the page wrote no space
 * between them: a run ending in a letter directly against one starting with a letter, in
 * **either case**. Each finding is `{ before, after, glued }`, where `glued` is the word a
 * reader reads — the letters on either side of the seam, since a hyphen, a slash or a digit
 * ends a word.
 *
 * **The seam is about letters, not about lower case.** An earlier version of this rule read
 * only `a`–`z` on both sides, which misses the same defect wherever the element the space was
 * lost in front of starts a word with a capital — `see the<a>Privacy</a>` reads "thePrivacy"
 * and passed. That narrowing was not a safety property: it was hiding a hole in what the
 * caller hands in as `isBlock`, because the site's own flex rows put two capitalised links
 * side by side (`spacing.mjs` reads the page's stylesheet and treats a flex or grid parent's
 * children as the boundaries they are, which is what the wide rule needs to stay quiet).
 */
export function runTogether(node, isBlock) {
  const runs = textRuns(node, isBlock)
  const out = []
  for (let i = 1; i < runs.length; i++) {
    const before = runs[i - 1]
    const after = runs[i]
    if (before === ' ' || after === ' ') continue
    const left = before.slice(-1)
    const right = after.slice(0, 1)
    if (!/[A-Za-z]/.test(left) || !/[A-Za-z]/.test(right)) continue
    const tail = /[A-Za-z]+$/.exec(before)?.[0] ?? left
    const head = /^[A-Za-z]+/.exec(after)?.[0] ?? right
    out.push({ before, after, glued: tail + head })
  }
  return out
}

export function hasAncestor(node, name) {
  for (let p = node.parent; p; p = p.parent) if (p.name === name) return true
  return false
}

// ---- CSS, read just far enough --------------------------------------------------------

export function stripCSSComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

/**
 * Split a stylesheet into rules: [{ conditions, selector, body }], where `conditions` is the
 * list of enclosing `@media`/`@supports`/`@layer`/`@container` preludes, outermost first.
 * `@keyframes` and `@font-face` are returned with their at-rule as the selector, and
 * `@import` as { conditions, importURL }. A rule with no block is refused.
 */
export function cssRules(source) {
  const css = stripCSSComments(source)
  const rules = []
  function block(from) {
    // returns index of matching close brace for the "{" at `from`
    let depth = 0
    for (let k = from; k < css.length; k++) {
      const c = css[k]
      if (c === '"' || c === "'") {
        const close = css.indexOf(c, k + 1)
        if (close === -1) throw new HTMLScanError('unterminated string in a stylesheet')
        k = close
      } else if (c === '{') depth++
      else if (c === '}') {
        depth--
        if (depth === 0) return k
      }
    }
    throw new HTMLScanError('unbalanced braces in a stylesheet')
  }
  function scan(from, to, conditions) {
    let k = from
    while (k < to) {
      while (k < to && /[\s;]/.test(css[k])) k++
      if (k >= to) break
      const open = css.indexOf('{', k)
      const semi = css.indexOf(';', k)
      if (css[k] === '@' && semi !== -1 && (open === -1 || semi < open)) {
        const stmt = css.slice(k, semi).trim()
        const imp = /^@import\s+(?:url\(\s*)?["']?([^"')\s]+)/i.exec(stmt)
        if (imp) rules.push({ conditions, importURL: imp[1] })
        k = semi + 1
        continue
      }
      if (open === -1 || open >= to) throw new HTMLScanError(`a stylesheet rule with no block: ${css.slice(k, k + 40)}`)
      const selector = css.slice(k, open).trim()
      const close = block(open)
      if (/^@(media|supports|layer|container)\b/i.test(selector)) {
        scan(open + 1, close, [...conditions, selector])
      } else {
        rules.push({ conditions, selector, body: css.slice(open + 1, close) })
      }
      k = close + 1
    }
  }
  scan(0, css.length, [])
  return rules
}

/** The declarations of a rule body, as [property, value] pairs (lowercased property). */
export function cssDeclarations(body) {
  const out = []
  for (const part of body.split(';')) {
    const colon = part.indexOf(':')
    if (colon === -1) continue
    out.push([part.slice(0, colon).trim().toLowerCase(), part.slice(colon + 1).trim()])
  }
  return out
}

/** Every url(...) a piece of CSS names. */
export function cssURLs(css) {
  return [...stripCSSComments(css).matchAll(/url\(\s*(["']?)([^"')]*)\1\s*\)/gi)].map((m) => m[2].trim())
}

// ---- the built artifact ------------------------------------------------------------------

/** Every file under a directory, as paths relative to it, with forward slashes. */
export function filesUnder(dir) {
  const out = []
  function rec(d) {
    for (const name of readdirSync(d).sort()) {
      const full = path.join(d, name)
      if (statSync(full).isDirectory()) rec(full)
      else out.push(path.relative(dir, full).split(path.sep).join('/'))
    }
  }
  rec(dir)
  return out
}

/** The built pages of a dist directory, as [{ rel, url, html }]. */
export function builtPages(dist = DIST) {
  if (!existsSync(dist)) {
    throw new Error(`${dist} is missing: the gates read the built artifact, so run \`npm run build\` (or \`npm run check\`, which builds first)`)
  }
  return filesUnder(dist)
    .filter((rel) => rel.endsWith('.html'))
    .map((rel) => ({
      rel,
      url: '/' + rel.replace(/(^|\/)index\.html$/, '$1').replace(/\.html$/, ''),
      html: readFileSync(path.join(dist, rel), 'utf8'),
    }))
}

/**
 * Resolve a site path (`/docs/`, `/_astro/x.css`) to the file in `dist` that serves it, or
 * null. A directory path is served by its index.html, a path with no extension by
 * `<path>/index.html` or `<path>.html`, which is what a static host does.
 */
export function servedFile(dist, pathname) {
  let p
  try {
    p = decodeURIComponent(pathname)
  } catch {
    return null
  }
  if (!p.startsWith('/') || p.split('/').includes('..')) return null
  const rel = p.slice(1)
  const candidates = p.endsWith('/') ? [rel + 'index.html'] : [rel, rel + '/index.html', rel + '.html']
  for (const c of candidates) {
    const full = path.join(dist, c)
    if (existsSync(full) && statSync(full).isFile()) return c
  }
  return null
}

// ---- site.yaml, the part the gates need --------------------------------------------------

/** The Content-Security-Policy `firebase.json` serves, as one string (W3.2). */
export function deployedCSP(repo = REPO) {
  const config = JSON.parse(readFileSync(path.join(repo, 'firebase.json'), 'utf8'))
  const headers = config.hosting.headers.flatMap((entry) => entry.headers)
  const csp = headers.find((header) => header.key.toLowerCase() === 'content-security-policy')
  if (!csp) throw new Error('firebase.json carries no Content-Security-Policy header')
  return csp.value
}

/**
 * The four facts the gates need from site.yaml: `canonical_url` (and the host it names),
 * `allowed_hosts`, `analytics.enabled` and `analytics.measurement_id`. A strict reader of
 * exactly those keys — the Go boundary test pins their shape (bare lowercase hosts, a
 * quoted-or-bare URL, a boolean, a quoted id) and anything else in their place is refused
 * here rather than read as something it is not.
 *
 * `analyticsMeasurementID` is the property the site reports to, and it is **empty while the
 * flag is off**: the two move together (the boundary test holds them to it), and it requires
 * a **quoted** `measurement_id` — the id is a string a browser would stringify anyway, and an
 * unquoted one is refused rather than turned into a number or a boolean by a YAML reader.
 * A test that reads the built page's own configuration compares it with this rather than with
 * a literal of its own, so the value lives in site.yaml alone (W3.2).
 */
export function siteFacts(siteYAML = readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8')) {
  const lines = siteYAML.split('\n')
  const canonical = lines.map((l) => /^canonical_url:\s*"?([^"\s#]+)"?\s*(#.*)?$/.exec(l)).find(Boolean)
  if (!canonical) throw new Error('site.yaml has no canonical_url line this reader understands')
  const at = lines.findIndex((l) => /^allowed_hosts:\s*$/.test(l))
  if (at === -1) throw new Error('site.yaml has no allowed_hosts block')
  const hosts = []
  for (const l of lines.slice(at + 1)) {
    if (/^\s*(#.*)?$/.test(l)) continue
    const m = /^  - ([a-z0-9.-]+)\s*$/.exec(l)
    if (!m) {
      if (/^\S/.test(l)) break
      throw new Error(`site.yaml allowed_hosts: unreadable entry ${JSON.stringify(l)}`)
    }
    hosts.push(m[1])
  }
  const a = lines.findIndex((l) => /^analytics:\s*$/.test(l))
  const block = a === -1 ? [] : lines.slice(a + 1, a + 4)
  const enabled = /^  enabled:\s*(true|false)\s*(#.*)?$/.exec(block.find((l) => /^  enabled:/.test(l)) ?? '')
  if (!enabled) throw new Error('site.yaml analytics.enabled is not a plain true/false')
  const id = /^  measurement_id:\s*"([^"]*)"\s*(#.*)?$/.exec(block.find((l) => /^  measurement_id:/.test(l)) ?? '')
  if (!id) throw new Error('site.yaml analytics.measurement_id is not a quoted value')
  const canonicalURL = new URL(canonical[1])
  return {
    canonicalURL,
    canonicalHost: canonicalURL.host,
    allowedHosts: new Set(hosts),
    analyticsEnabled: enabled[1] === 'true',
    analyticsMeasurementID: id[1],
  }
}
