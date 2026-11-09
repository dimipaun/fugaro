// website/test/keys.mjs — the key request page (subscriptions plan, Task S3.3).
//
// `/keys/` is the page a person asks for a licence key on, and it is the one page of this site
// that ASKS A READER FOR SOMETHING: the privacy statement says the site has no account and no
// form, and the statement's own copy for the key sign-up is S7.3's and the owner's (the
// design's §7.3) — which is why the page is in the repository and not published before S7.4.
// So this file holds the page to the things the task names, read off the BUILT artifact,
// because what a browser gets is what the page is:
//
//   1. **The three forms, and where they post.** `free-seat`, `open-source` and `request` (the
//      design's §4.1, §4.4 and §4.5): one form each, POST, and each action is the key service's
//      intake origin plus that request's own route. The origin is not typed here or on the page
//      — it is `src/lib/keys-intake.mjs`'s `keysIntakeOrigin`, derived from site.yaml's
//      canonical URL, and that is the same function the page builds its actions from, so the
//      check and the page cannot drift (`formFailures`).
//   2. **The fields belong to the forms.** Every field sits inside one of the three forms,
//      carries a name, and has a label of its own: a field outside them is the site asking a
//      reader for something nothing submits, and an unnamed or unlabelled field is one the
//      applicant cannot fill (`fieldFailures`).
//   3. **No third-party load, and the one off-site address the site posts to.** The page
//      fetches nothing from another origin, and the only address it carries outside this site
//      is the intake's, as a form's action (`fetchFailures`). `origins.mjs` is the gate that
//      holds every page to the full origin contract and the deployed policy with it (D106,
//      W3.2); this case reads the page's own fetch set, so the keys page's one off-site address
//      is named where the page is rather than inferred from the policy.
//   4. **The privacy link.** The page points at `/privacy/` (`privacyLinkFailures`), which is
//      where S7.3 writes what the intake collects and how long it keeps it.
//
// It reads `dist/`, which `npm run check` builds before it runs this file. Standard library
// only, no network (Global Constraint 4). Red step: this file fails against a build with no
// `/keys/` page, which is the state it was written in.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { DIST, builtPages, find, parse, siteFacts, textOf, walk } from './lib/html.mjs'
import { keysIntakeOrigin } from '../src/lib/keys-intake.mjs'

/** The page this file is about. */
export const KEYS_URL = '/keys/'

/**
 * The three requests the page carries, in the order it lays them out: the route each form posts
 * to and the id of the section and form it sits in. The routes are the site's half of the
 * intake's contract — the intake "serves the forms' endpoints" (the design's §3.1) — and the
 * service that answers them is S3.2's, deployed by the owner in S3.7.
 */
export const FORMS = Object.freeze([
  { id: 'free-seat', route: '/free-seat' },
  { id: 'open-source', route: '/open-source' },
  { id: 'request', route: '/request' },
])

/** The built key request page, or a failure that says the build is missing it. */
function keysPage(dist = DIST) {
  const page = builtPages(dist).find((p) => p.url === KEYS_URL)
  assert.ok(
    page,
    `the build carries no ${KEYS_URL} page: this file holds the key request page to the intake's contract, and \`npm run check\` builds the artifact it reads first`,
  )
  return page
}

/** A built page's `main`, or a failure that says there is none to read. */
function mainOf(html) {
  const main = find(parse(html), 'main')
  assert.ok(main, 'the built key request page has no `main`, so this reader is looking at nothing (a11y.mjs is the gate that refuses the landmark)')
  return main
}

/** Whether `el` is `ancestor` or sits inside it. */
function within(el, ancestor) {
  for (let n = el; n; n = n.parent) if (n === ancestor) return true
  return false
}

const FIELDS = Object.freeze(['input', 'textarea', 'select'])

/** Every form of the page, in document order. */
const formsOf = (main) => [...walk(main)].filter((el) => el.name === 'form')

/** Every field of the page, in document order. */
const fieldsOf = (main) => [...walk(main)].filter((el) => FIELDS.includes(el.name))

/** The element's text as one line, for a failure line about it. */
const textOn = (el) => JSON.stringify(textOf(el).replace(/\s+/g, ' ').trim())

// ---- 1. the three forms, and where they post ----------------------------------------------

/**
 * Every way the page's forms depart from the intake's contract, or [].
 *
 * `intake` is the origin the page posts to, derived from site.yaml's canonical URL by
 * `src/lib/keys-intake.mjs` — the same function the page builds its actions from. What is held
 * is the exact set: one form per request, POST, and an action that is the intake's origin plus
 * that request's route and nothing else. A form posting to this site would collect nothing (the
 * site is static), and a fourth form would be a request the service has no route for.
 */
export function formFailures(main, intake) {
  const out = []
  const forms = formsOf(main)
  if (forms.length !== FORMS.length) {
    out.push(
      `the key request page carries ${forms.length} form(s) and the page is the ${FORMS.length} ways to ask for a key — the free seat, the open-source programme and the requests a person reads (the design's §4.1, §4.4, §4.5)`,
    )
  }
  const seen = new Set()
  forms.forEach((form, at) => {
    const where = `the key request page's ${at + 1}. form`
    const method = (form.attrs.get('method') ?? '').toLowerCase()
    if (method !== 'post') {
      out.push(
        `${where} submits with method=${JSON.stringify(method || '(none)')}: an application carries an address the reader typed, and a GET would put it in the URL, in the host's logs and in the browser's history — the intake takes the three requests as POSTs`,
      )
    }
    const action = form.attrs.get('action')
    if (action === undefined) {
      out.push(`${where} carries no action: it would submit to this page, which is a static file and collects nothing (the design's §3.1)`)
      return
    }
    let url
    try {
      url = new URL(action)
    } catch {
      out.push(
        `${where}'s action ${JSON.stringify(action)} is not an absolute URL: the intake is a service of its own, so a relative action posts to this site and nowhere near the service (the design's §3.1)`,
      )
      return
    }
    if (url.origin !== intake) {
      out.push(
        `${where}'s action ${JSON.stringify(action)} is not on the intake (${intake}): the key request page posts to the key service's intake and to nothing else, and the origin is derived from site.yaml's canonical URL rather than written anywhere (src/lib/keys-intake.mjs)`,
      )
      return
    }
    if (url.search || url.hash) {
      out.push(
        `${where}'s action ${JSON.stringify(action)} carries a query or a fragment: the request is the route and nothing else, and a parameter the service does not read is one nobody sees (the intakes' routes are the site's half of its contract)`,
      )
      return
    }
    const match = FORMS.find((one) => one.route === url.pathname)
    if (!match) {
      out.push(
        `${where}'s action reaches ${JSON.stringify(url.pathname)} on the intake, which is not one of the requests this page carries (${FORMS.map((one) => one.route).join(', ')}): the route set is the site's half of the intake's contract, and a route nothing answers is a form that collects an application and drops it`,
      )
      return
    }
    if (seen.has(match.route)) out.push(`${where} posts to ${match.route} and the page carries that request twice: one route per request`)
    seen.add(match.route)
    // Each form sits in the section that names its request, so `/keys/#open-source` is the one
    // anchor a reader can be sent — and the id is on the section rather than on the form, because
    // one id in a document names one thing (the anchor and the form sharing it would be a
    // duplicate, and duplicate ids are what `linkcheck.mjs` resolves fragments against).
    let anchored = false
    for (let node = form.parent; node; node = node.parent) if (node.attrs?.get('id') === match.id) anchored = true
    if (!anchored) {
      out.push(
        `${where} posting to ${match.route} sits in no section carrying id=${JSON.stringify(match.id)}: each request is a section named after it, so a reader, this case and the service name the three the same way and a link to one of them is an anchor the page has`,
      )
    }
  })
  for (const one of FORMS) {
    if (!seen.has(one.route)) {
      out.push(
        `the key request page carries no form posting to ${one.route}: ${JSON.stringify(one.id)} is one of the three ways to ask for a key (the design's §4.1, §4.4, §4.5), and a page that lost one of them sends that applicant to the other two, where the checks do not fit their case`,
      )
    }
  }
  return out
}

// ---- 2. the fields belong to the forms ----------------------------------------------------

/**
 * Every way the page's fields depart from "the site asks for what it collects", or [].
 *
 * Each field sits inside one of the three forms, carries a `name` (so the control reaches the
 * intake as a field and not as nothing — which names the intake reads is S3.2's contract and
 * not this page's), and has a
 * label of its own — resolved through `for`/`id` or by being wrapped in one — because a field a
 * screen-reader user cannot name is a field they cannot fill (WCAG 3.3.2; `a11y.mjs` holds the
 * same pair on every page, and this case reads the three forms together, so a field the three
 * forms do not own is refused rather than counted as the page's own).
 */
export function fieldFailures(main) {
  const out = []
  const forms = formsOf(main)
  const fields = fieldsOf(main)
  const labelled = new Set()
  for (const label of [...walk(main)].filter((el) => el.name === 'label')) {
    if (label.attrs.has('for')) labelled.add(label.attrs.get('for'))
    for (const field of fields) if (field.attrs?.has('id') && within(field, label)) labelled.add(field.attrs.get('id'))
  }
  for (const field of fields) {
    const where = `the key request page's <${field.name} ${field.attrs.get('name') ?? '(no name)'}>`
    if (!forms.some((form) => within(field, form))) {
      out.push(
        `${where} sits outside the three forms: a field on this page is a field of one of the three requests, and one anywhere else is the site asking a reader for something no form submits`,
      )
    }
    if (!field.attrs.has('name')) {
      out.push(`${where} carries no name: a control with no name reaches the intake as nothing, so the application would arrive empty`)
    }
    if (!labelled.has(field.attrs.get('id') ?? '')) {
      out.push(
        `${where} has no label pointing at it: every field of the three forms is named by a <label for> (or sits inside one), because a reader told ${textOn(field)} beside the field and a reader whose screen reader announces only the control are owed the same field`,
      )
    }
  }
  return out
}

// ---- 3. no third-party load, and the one off-site address the site posts to ---------------

// The attributes a browser fetches from on load, by element: the site loads nothing from another
// origin (D106), and this is what the keys page's fetch set is read off.
const FETCHING = Object.freeze({
  script: ['src'],
  link: ['href'],
  iframe: ['src'],
  frame: ['src'],
  embed: ['src'],
  object: ['data'],
  img: ['src', 'srcset'],
  source: ['src', 'srcset'],
  video: ['src', 'poster'],
  audio: ['src'],
  track: ['src'],
  input: ['src'],
})

/** The URLs of a `srcset` value. */
const srcsetURLs = (value) => value.split(',').map((part) => part.trim().split(/\s+/)[0]).filter(Boolean)

/**
 * Every way the page loads something from another origin, or posts anywhere but the intake, or [].
 *
 * A URL with no scheme is this site's. An absolute one is this site's when it is http(s) on the
 * canonical host — over https, so a plain-http address is refused rather than counted as this
 * site's — and the intake's when the element is a submit target (`form action`, `formaction`) —
 * the page's one off-site address, and the reason the deployed policy's `form-action` carries
 * the intake's token (`origins.mjs` holds the policy to it; the widening is D451, option (a),
 * with the owner's approval). Anything else is a third party, which is what this case refuses.
 */
export function fetchFailures(main, facts, intake) {
  const out = []
  const check = (el, attr) => {
    const value = el.attrs.get(attr)
    if (value === undefined || value === '') return
    for (const raw of attr === 'srcset' ? srcsetURLs(value) : [value]) {
      const url = raw.trim().replace(/[\t\n\r]/g, '')
      if (url.startsWith('#')) continue // a fragment names this page
      // A URL that names no scheme is this site's own file. The test is "carries a scheme",
      // not "carries `//`": `//evil.example.net/x` is another host, and so is
      // `http:evil.example.net/x` — a scheme-only URL in a scheme other than this page's is
      // still an authority, where the same scheme (`https:evil.example.net/x` on an https
      // page) is a path on this site, which is what the browser does with it too.
      if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(url) && !url.startsWith('//')) continue
      let parsed
      try {
        parsed = new URL(url, `https://${facts.canonicalHost}/`)
      } catch {
        out.push(`the key request page's <${el.name} ${attr}=${JSON.stringify(raw)}> is not a URL`)
        continue
      }
      // This site's own file: https on the canonical host. A plain-http one is not, and
      // `origins.mjs` refuses it too.
      if (parsed.origin === `https://${facts.canonicalHost}`) continue
      const submits = (el.name === 'form' && attr === 'action') || (['button', 'input'].includes(el.name) && attr === 'formaction')
      if (submits && parsed.origin === intake) continue
      out.push(
        submits
          ? `the key request page's <${el.name} ${attr}=${JSON.stringify(raw)}> posts to ${parsed.host}, and the page posts to the intake (${intake}) and to nothing else: the one service this site sends a request to is the key service, and a form elsewhere is an application nobody receives`
          : `the key request page's <${el.name} ${attr}=${JSON.stringify(raw)}> loads from ${parsed.host}: the page loads nothing from another origin (D106), and the intake is reached by posting a form to it and never by fetching from it`,
      )
    }
  }
  for (const el of walk(main)) {
    for (const attr of FETCHING[el.name] ?? []) check(el, attr)
    if (el.name === 'form') check(el, 'action')
    if (['button', 'input'].includes(el.name)) check(el, 'formaction')
  }
  return out
}

// ---- 4. the privacy link ------------------------------------------------------------------

/**
 * Every way the page's pointer at the privacy statement departs from the task, or [].
 *
 * The statement is `/privacy/` on this site, so the link may be written the way the rest of the
 * site writes its own links — relative, or absolute on the canonical origin (W1.3 builds both)
 * — and a fragment or a query that is not the statement's path is not it.
 */
export function privacyLinkFailures(main, facts) {
  const base = `https://${facts.canonicalHost}/`
  const hrefs = [...walk(main)].filter((el) => el.name === 'a' && el.attrs.has('href')).map((el) => el.attrs.get('href'))
  const isStatement = (href) => {
    try {
      const url = new URL(href, base)
      return url.origin === base.slice(0, -1) && /^\/privacy\/?$/.test(url.pathname)
    } catch {
      return false
    }
  }
  if (!hrefs.some(isStatement)) {
    return [
      "the key request page carries no link to /privacy/: the intake collects an applicant's address and the repositories they name, and the statement that says what is collected, how long it is kept and who receives it is the one a reader filling a form has to be able to read first (the design's §7.3, S7.3)",
    ]
  }
  return []
}

// ---- the gate -----------------------------------------------------------------------------

const FACTS = siteFacts()
const INTAKE = keysIntakeOrigin(FACTS.canonicalURL)

test('the key request page posts its three requests to the intake, and to nothing else', () => {
  const main = mainOf(keysPage().html)
  for (const failures of [formFailures(main, INTAKE), fieldFailures(main), fetchFailures(main, FACTS, INTAKE), privacyLinkFailures(main, FACTS)]) {
    assert.deepEqual(failures, [], failures.join('\n'))
  }
})

// ---- the refusals, kept -------------------------------------------------------------------

const SHELL = (body) => `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>Get a key</h1>${body}</main></body></html>`

/** The page's three forms, as the page writes them: a labelled field each. */
const THREE_FORMS = FORMS.map(
  (one) =>
    `<section id="${one.id}"><h2>${one.id}</h2><form method="post" action="${INTAKE}${one.route}">` +
    `<label for="${one.id}-email">Email address</label><input id="${one.id}-email" name="email" type="email"></form></section>`,
).join('')

/** The shipped shape of the page, with `body` standing in for the three forms. */
const shipped = (body = THREE_FORMS) => SHELL(`${body}<p><a href="/privacy/">privacy statement</a></p>`)

test('a form posting anywhere but the intake, a wrong method, a wrong route and a missing form are refused', () => {
  assert.deepEqual(formFailures(mainOf(shipped()), INTAKE), [], 'the shipped shape of the three forms is refused, so this case proves nothing')

  const cases = [
    ['a form posting elsewhere', THREE_FORMS.replace(`${INTAKE}/request`, 'https://collect.example.net/request'), /is not on the intake/],
    ['a GET', THREE_FORMS.replace('method="post"', 'method="get"'), /submits with method="get"/],
    ['a relative action', THREE_FORMS.replace(`${INTAKE}/free-seat`, '/free-seat'), /is not an absolute URL/],
    ['a route the service does not answer', THREE_FORMS.replace(`${INTAKE}/open-source`, `${INTAKE}/purchase`), /not one of the requests this page carries/],
    ['a route with a query on it', THREE_FORMS.replace(`${INTAKE}/request`, `${INTAKE}/request?utm=1`), /carries a query or a fragment/],
    ['a form outside the section that names its request', THREE_FORMS.replace('<section id="request">', '<section id="other">'), /sits in no section carrying id="request"/],
    ['a missing request', THREE_FORMS.replace(/<section id="request">.*?<\/section>/, ''), /carries no form posting to \/request/],
  ]
  for (const [what, body, expected] of cases) {
    assert.match(formFailures(mainOf(shipped(body)), INTAKE).join('\n'), expected, `${what} was not refused as expected (${expected})`)
  }
  assert.match(formFailures(mainOf(shipped(THREE_FORMS.replace(/<section id="request">.*?<\/section>/, ''))), INTAKE).join('\n'), /carries 2 form/)
})

test('a field outside the forms, a field with no name and an unlabelled field are refused', () => {
  assert.deepEqual(fieldFailures(mainOf(shipped())), [], 'the shipped shape of the three forms is refused, so this case proves nothing')

  const cases = [
    ['a field outside the forms', `${THREE_FORMS}<label for="extra">Age</label><input id="extra" name="age">`, /sits outside the three forms/],
    ['a field with no name', THREE_FORMS.replace(' name="email"', ''), /carries no name/],
    ['an unlabelled field', THREE_FORMS.replace('<label for="free-seat-email">Email address</label>', ''), /has no label pointing at it/],
  ]
  for (const [what, body, expected] of cases) {
    assert.match(fieldFailures(mainOf(shipped(body))).join('\n'), expected, `${what} was not refused as expected (${expected})`)
  }
})

test('a third-party fetch, a post off the intake and a protocol-relative fetch are refused', () => {
  assert.deepEqual(fetchFailures(mainOf(shipped()), FACTS, INTAKE), [], 'the shipped page fetches something this case refuses, so it proves nothing')

  const cases = [
    ['an image from a third party', '<img src="https://pixel.example.net/p.gif" alt="">', /loads from pixel\.example\.net/],
    ['a scheme-only image URL in another scheme, which a browser reads as another host', '<img src="http:pixel.example.net/p.gif" alt="">', /loads from pixel\.example\.net/],
    ['an image this site serves over plain http', '<img src="http://site.example/a.png" alt="">', /loads from site\.example/],
    ['an image in srcset', '<img src="/a.png" srcset="/a.png 1x, https://img.example.net/a@2x.png 2x" alt="">', /loads from img\.example\.net/],
    ['a protocol-relative image', '<img src="//pixel.example.net/p.gif" alt="">', /loads from pixel\.example\.net/],
    ['a stylesheet from another host', '<link rel="stylesheet" href="https://cdn.example.net/x.css">', /loads from cdn\.example\.net/],
    ['a frame', '<iframe src="https://embed.example.net/x" title="e"></iframe>', /loads from embed\.example\.net/],
  ]
  for (const [what, body, expected] of cases) {
    assert.match(fetchFailures(mainOf(shipped(body)), FACTS, INTAKE).join('\n'), expected, `${what} was not refused as expected (${expected})`)
  }
  const elsewhere = THREE_FORMS.replace(`${INTAKE}/request`, 'https://collect.example.net/')
  assert.match(fetchFailures(mainOf(shipped(elsewhere)), FACTS, INTAKE).join('\n'), /posts to collect\.example\.net/)
})

test('a page with no pointer at the privacy statement is refused', () => {
  assert.deepEqual(privacyLinkFailures(mainOf(shipped()), FACTS), [], 'the shipped page carries the link this case refuses, so it proves nothing')
  // The other way the site writes a link to one of its own pages: absolute on the canonical
  // origin, which is what W1.3 builds. Either form is a link to the statement.
  assert.deepEqual(
    privacyLinkFailures(mainOf(SHELL('<a href="https://fugaro.dev/privacy/">privacy statement</a>')), FACTS),
    [],
    'an absolute link to the statement is refused, so this case demands one spelling of it rather than the page',
  )
  assert.match(privacyLinkFailures(mainOf(SHELL('<h1>t</h1>')), FACTS).join('\n'), /carries no link to \/privacy\//)
  // A link to a page that is not the statement — the FAQ's own answer about privacy, say — is
  // not it, and neither is another host's `/privacy/`.
  for (const href of ['/faq/', 'https://elsewhere.example/privacy/']) {
    assert.match(
      privacyLinkFailures(mainOf(SHELL(`<a href="${href}">privacy-ish</a>`)), FACTS).join('\n'),
      /carries no link to \/privacy\//,
      `${href} was taken for the statement`,
    )
  }
})
