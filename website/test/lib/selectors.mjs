// website/test/lib/selectors.mjs — whether a built rule can reach a built element.
//
// **A rule is in force only where its selector can match an element the page rendered**, and
// that is a fact about the built artifact rather than about the source. Astro appends a
// component's `data-astro-cid-…` to **every** compound of a scoped selector, so a source's
// `.release-notes code` is built as `.release-notes[cid] code[cid]` and a source's `.run-card`
// as `.run-card[cid]` — and an element that arrives through `set:html` carries no scope
// attribute for either to match. The rule is then dead, and it reads in the stylesheet as a
// rule that is there. That is the shape two defects of this site shipped as: the changelog's
// code chips (`website/test/changelog-page.mjs`, whose case asked whether a rule with those
// fragments existed and never whether it could match anything) and the run diagram's shapes
// (`website/test/how-it-works.mjs`, where every paint rule was dead and the drawing painted
// SVG's own default black). `website/test/base-styles.mjs` reads the same shape for the
// homepage's blocks, one class and one scope at a time.
//
// This is the reader both gates use, so "can this rule reach this element" cannot be two
// answers to one question. It is a small, strict matcher rather than a CSS engine: element
// names, classes, attribute selectors, and the two combinators Astro writes (a descendant and
// a child). A pseudo-class is not evaluated and is read as matching, which is the safe
// direction for a reading whose subject is the scope attribute; `+` and `~` are read as part
// of a compound and so as matching nothing, since neither gate's page writes one and a reader
// that guessed would be worse than one that refuses.
//
// Standard library only (Global Constraint 4).

/** The classes an element carries, as the markup writes them. */
export function classesOf(el) {
  return (el.attrs?.get('class') ?? '').split(/\s+/).filter(Boolean)
}

/** An element's ancestors, nearest first, up to the document. */
export function ancestorsOf(el) {
  const out = []
  for (let node = el.parent; node !== null && node !== undefined; node = node.parent) out.push(node)
  return out
}

/** One compound selector — the part between two combinators — read into what it requires. */
export function compoundOf(part) {
  return {
    name: /^[a-zA-Z][\w-]*/.exec(part)?.[0] ?? null,
    classes: [...part.matchAll(/\.([A-Za-z_][-\w]*)/g)].map((match) => match[1]),
    attrs: [...part.matchAll(/\[([^\]]*)\]/g)].map((match) => match[1]),
  }
}

/**
 * A complex selector as its compounds, each with the combinator before it (`null` for the
 * first). The two combinators a scoped Astro selector writes are the ones read — a descendant
 * (whitespace) and a child (`>`) — and a combinator inside `()` or `[]` is not the selector's
 * own. The other two (`+`, `~`) are read as part of a compound and so as matching nothing:
 * the pages these gates read write none, and a reader that guessed would be worse than one
 * that refuses.
 */
export function selectorChain(selector) {
  const parts = []
  let buffer = ''
  let depth = 0
  let combinator = null
  const flush = () => {
    if (buffer.trim() !== '') parts.push({ combinator, part: buffer.trim() })
    buffer = ''
  }
  for (const ch of selector) {
    if (ch === '(' || ch === '[') depth += 1
    else if (ch === ')' || ch === ']') depth -= 1
    if (depth === 0 && (ch === '>' || /\s/.test(ch))) {
      if (buffer.trim() !== '') {
        flush()
        combinator = ' '
      }
      if (ch === '>') combinator = '>'
      continue
    }
    buffer += ch
  }
  flush()
  return parts
}

/**
 * True when one compound selector requires nothing of `el` that `el` is not: the element name
 * it names, the classes it names, and each attribute selector it carries (presence, or `=` a
 * value). A pseudo-class is not evaluated and is read as matching — the safe direction for a
 * reading that asks whether a selector can match at all, and the defect it is here for is the
 * scope attribute, which it reads exactly.
 */
export function compoundMatches(part, el) {
  const { name, classes, attrs } = compoundOf(part)
  if (name !== null && name !== el.name) return false
  const own = classesOf(el)
  if (!classes.every((klass) => own.includes(klass))) return false
  return attrs.every((spec) => {
    const eq = spec.indexOf('=')
    if (eq === -1) return el.attrs.has(spec.trim())
    const value = spec.slice(eq + 1).trim().replace(/^["']|["']$/g, '')
    return el.attrs.get(spec.slice(0, eq).trim()) === value
  })
}

/** True when one complex selector — not a list of them — can match `el`. */
export function matchesSelector(selector, el) {
  const chain = selectorChain(selector)
  if (chain.length === 0) return false
  const at = (index, node) => {
    if (node === null || node === undefined || node.name === undefined) return false
    if (!compoundMatches(chain[index].part, node)) return false
    if (index === 0) return true
    if (chain[index].combinator === '>') return at(index - 1, node.parent)
    for (let parent = node.parent; parent !== null && parent !== undefined; parent = parent.parent) {
      if (at(index - 1, parent)) return true
    }
    return false
  }
  return at(chain.length - 1, el)
}

/** The subject of a complex selector: the compound that decides which element the rule styles. */
export function subjectOf(selector) {
  return compoundOf(selectorChain(selector).at(-1)?.part ?? '')
}

/** True when one of a rule's selectors — its list, read one at a time — can match `el`. */
export function ruleReaches(selector, el) {
  return selector
    .split(',')
    .some((part) => matchesSelector(part.trim(), el))
}
