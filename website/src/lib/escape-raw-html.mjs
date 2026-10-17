// website/src/lib/escape-raw-html.mjs — raw HTML in a compiled markdown document becomes text.
//
// The generated CLI reference documents placeholders such as `<name>` and
// `<repo-slug>/<run-id>`. Astro's markdown pipeline passes raw HTML through, so a browser
// parses each placeholder as an unknown element and its text vanishes; and a `<script>` or
// `<img onerror=...>` in the reference would reach the page as a tag. The pipeline offers no
// switch for this without installing a markdown processor package (the lockfile is pinned
// and reviewed, so none is added), so this runs over the *compiled* HTML instead.
//
// It can, because of one property of the compiled output: text and code are already
// entity-escaped by the renderer, so every `<` that is left in it opens a tag. A tag is
// kept only if it is one the renderer itself emits, with only the attributes the renderer
// gives it; any other tag-shaped token is escaped (`<` and `>` become entities) and so is
// shown to the reader as the text it was written as.

/** The tags the markdown renderer emits, and the attributes each may carry. */
const ALLOWED = {
  h1: ['id'], h2: ['id'], h3: ['id'], h4: ['id'], h5: ['id'], h6: ['id'],
  p: [], ul: [], ol: [], li: [], blockquote: [], hr: [], br: [],
  table: [], thead: [], tbody: [], tr: [], th: [], td: [],
  em: [], strong: [], code: [],
  a: ['href'],
  pre: ['class', 'style', 'tabindex', 'data-language'],
  span: ['class', 'style'],
}

const TAG = /<[^>]*>/g
const NAME = /^<(\/?)([a-zA-Z][a-zA-Z0-9]*)/
const ATTRIBUTE = /\s+([a-zA-Z][\w-]*)="([^"<>]*)"/y

/** Whether a tag-shaped token is exactly an element the renderer emits. */
function isRendered(token) {
  const name = NAME.exec(token)
  if (name === null) return false
  const [head, closing, tag] = name
  const attributes = ALLOWED[tag.toLowerCase()]
  if (attributes === undefined) return false
  let rest = token.slice(head.length)
  if (closing === '/') return /^\s*>$/.test(rest)
  ATTRIBUTE.lastIndex = 0
  let at = 0
  for (;;) {
    ATTRIBUTE.lastIndex = at
    const attribute = ATTRIBUTE.exec(rest)
    if (attribute === null) break
    if (!attributes.includes(attribute[1])) return false
    if (attribute[1] === 'href' && !/^(https?:|#|\/|\.)/i.test(attribute[2])) return false
    at = ATTRIBUTE.lastIndex
  }
  return /^\s*\/?>$/.test(rest.slice(at))
}

/** The HTML with every tag the renderer did not emit shown as text. */
export function escapeRawHtml(html) {
  return html.replace(TAG, (token) => (isRendered(token) ? token : token.replace(/</g, '&lt;').replace(/>/g, '&gt;')))
}
