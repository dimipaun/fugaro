// website/test/lib/control-target.mjs — the site's one reading of the 44 px rule (the owner's
// brief of 2026-10-19: every control the site asks a reader to tap is at least 44 px tall).
//
// It lives under `test/lib/` rather than beside a page's cases because more than one page has
// controls and the rule is one rule: the home page's case reads its own controls with it
// (`test/home.mjs`) and the console page's case reads its own with it (`test/console.mjs`), so
// a control cannot pass on one page and fail on another for the same declaration. That is what
// `test/lib/` is for — the readers the gates share.
//
// It reads the **declaration**, not a measurement, and that is the point: the hero's filled
// link is its own line height plus its padding, which comes to a fraction of a pixel short of
// the target with nothing in the markup to say so — a defect no reading of the markup can see
// and no rendered measurement in this repository can take, which is why the rule declares the
// minimum and this reading looks for the declaration. A rule inside a media query does not
// count: the minimum has to hold at every width, so the rule it is declared in is one with no
// condition over it. A `var()` step of the spacing scale is resolved, because a minimum written
// as a token is the same minimum.
//
// A page passes its own controls — the classes it gives them and what each one is, as a
// failure names it — because the controls belong to the page and the rule does not. What comes
// back is the list of failures, as the lines a reader of a failure gets; the caller asserts it
// is empty, and the case beside it plants the ways the minimum stops holding and asserts the
// refusal fires, so the reading cannot rot into one that refuses nothing.
//
// Standard library only, and no network (Global Constraint 4).

/**
 * Every way a control misses the target size, as the lines a reader of a failure gets:
 * `controls` is a list of `[class, what it is]`, and each entry that declares no `min-height`
 * of at least 44 px outside a media query contributes one line.
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
