// website/test/lib/controls.mjs — the target size every control the site asks a reader to tap
// is measured against (the owner's brief of 2026-10-19), as one reading two gates share.
//
// **Every control a page asks a reader to tap is at least 44 px tall.** A minimum rather than a
// measurement, and that is the point: a filled link is its own line height plus its padding,
// which comes to a fraction of a pixel short of the target with nothing in the markup to say
// so — a defect no reading of the markup can see and no rendered measurement in this
// repository can take, which is why the rule declares the minimum and this reading looks for
// the declaration. A rule inside a media query does not count: the minimum has to hold at every
// width, so the rule it is declared in is one with no condition over it.
//
// It lives here rather than in one gate because two pages carry controls of their own — the
// homepage's hero, run band and closing call to action (`test/home.mjs`, which owns that list)
// and the caps-and-costs page's two hero actions (`test/caps-figures.mjs`) — and the site's
// slider tabs are `test/slider.mjs`'s, read at the widths the page is read at. The list is the
// caller's: this reading knows what a control is (a class, and what a failure calls it) and
// nothing about which page asks a reader to tap one.

/**
 * Every way a control misses the target size the owner's brief of 2026-10-19 measures it
 * against, as the lines a reader of a failure gets. `controls` is the caller's list of
 * `[class, what]` pairs, where `what` is how a failure names the control.
 */
export function controlTargetFailures(css, controls) {
  const tokens = new Map([...css.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;}]+)/g)].map((m) => [m[1], m[2].trim()]))
  const px = (value) => {
    const trimmed = (value ?? '').trim()
    const named = /^var\((--[a-z0-9-]+)\)$/.exec(trimmed)
    const resolved = named ? (tokens.get(named[1]) ?? '') : trimmed
    const m = /^([\d.]+)px$/.exec(resolved.trim())
    return m ? Number(m[1]) : null
  }
  // Whether a position in the stylesheet is inside a block: at the top level every rule is in
  // force at every width, and a rule inside a media query is not.
  const depthAt = (index) => {
    let depth = 0
    for (let i = 0; i < index; i++) {
      if (css[i] === '{') depth++
      else if (css[i] === '}') depth--
    }
    return depth
  }

  const out = []
  for (const [klass, what] of controls) {
    const heights = []
    for (const match of css.matchAll(new RegExp(`\\.${klass}(?![\\w-])[^{}]*\\{([^}]*)\\}`, 'g'))) {
      if (depthAt(match.index) !== 0) continue
      const declared = /min-height\s*:\s*([^;}]+)/.exec(match[1])
      const height = px(declared?.[1])
      if (height !== null) heights.push(height)
    }
    if (!heights.some((height) => height >= 44)) {
      out.push(
        `${what} is ${heights.length ? `a ${Math.max(...heights)} px target` : 'no declared target'}, and the owner's brief of 2026-10-19 measures every control the page asks a reader to tap against 44 px`,
      )
    }
  }
  return out
}
