// website/test/keys.mjs — the key request page (portal plan, Task P9.3; it was S3.3's).
//
// `/keys/` is where a reader is sent for a licence key, and the page no longer ASKS for anything
// itself: a key — the free evaluation seat, the open-source year, a paid seat — is asked for in
// the portal, Fugaro's own signed-in service, so this page points a reader there and carries no
// form and no field. The privacy statement says the site has no account, no comment box and no
// form, and that is now true of every page of the site, this one included; `website/test/consent.mjs`
// holds the statement to the built pages, and `website/test/privacy.mjs` rule 9 holds the
// statement's key sign-up card to this page. So this file holds the page to the things the task
// names, read off the BUILT artifact, because what a browser gets is what the page is:
//
//   1. **No form, and no field.** The page carries no `<form>` and no `input`, `textarea` or
//      `select` at all (`formFreeFailures`): the site collects nothing, and a control here would
//      be a thing nothing submits on the one page a reader was told asks for nothing.
//   2. **The portal link.** The page links to the portal, `app.` plus the site's canonical host
//      (`src/lib/portal.mjs`) — the same derived origin the deployed policy and the tests hold,
//      so the page and the checks cannot point at two different hosts (`portalLinkFailures`).
//   3. **The privacy link.** The page keeps its pointer at `/privacy/`
//      (`privacyLinkFailures`), which is where what the service keeps is stated.
//   4. **The deployed policy admits no form host.** The page carries no form, and `firebase.json`
//      says the site may post nowhere but itself: `form-action 'self'` alone, the S3.3 widening to
//      the key service's intake having come off with the forms (`website/test/origins.mjs` reads
//      the whole policy; this case reads the one directive this page's history touched).
//
// It reads `dist/`, which `npm run check` builds before it runs this file. Standard library
// only, no network (Global Constraint 4). Red step: this file fails against a build with no
// `/keys/` page, which is the state it was written in.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { DIST, builtPages, deployedCSP, find, parse, siteFacts, walk } from './lib/html.mjs'
import { portalOrigin } from '../src/lib/portal.mjs'

/** The page this file is about. */
export const KEYS_URL = '/keys/'

/** The built key request page, or a failure that says the build is missing it. */
function keysPage(dist = DIST) {
  const page = builtPages(dist).find((p) => p.url === KEYS_URL)
  assert.ok(
    page,
    `the build carries no ${KEYS_URL} page: this file holds the key request page to the portal contract, and \`npm run check\` builds the artifact it reads first`,
  )
  return page
}

/** A built page's `main`, or a failure that says there is none to read. */
function mainOf(html) {
  const main = find(parse(html), 'main')
  assert.ok(main, 'the built key request page has no `main`, so this reader is looking at nothing (a11y.mjs is the gate that refuses the landmark)')
  return main
}

const FIELDS = Object.freeze(['input', 'textarea', 'select'])

// ---- 1. no form, and no field -------------------------------------------------------------

/**
 * Every way the page asks a reader for something, or [].
 *
 * The page carries no `<form>` and no field: the site collects nothing, and a form here would be
 * a control the statement's "this site has no form" would be made false by — and a form that
 * posts off-site would be a host the deployed policy's `form-action 'self'` blocks anyway. A
 * field outside a form is the same defect: a reader asked for something nothing submits.
 */
export function formFreeFailures(main) {
  const out = []
  for (const el of walk(main)) {
    if (el.name === 'form') {
      out.push(
        `the key request page carries a <form> (action ${JSON.stringify(el.attrs.get('action') ?? '(none)')}): the site collects nothing, and a licence key is asked for in the portal, which the page links to rather than posting to — the policy's form-action is 'self' alone`,
      )
      continue
    }
    if (FIELDS.includes(el.name)) {
      out.push(
        `the key request page carries a <${el.name}>: the site asks a reader for nothing on this page, and a control here is one the built pages are held against (website/test/consent.mjs)`,
      )
    }
  }
  return out
}

// ---- 2. the portal link -------------------------------------------------------------------

/**
 * Every way the page's pointer at the portal departs from the task, or [].
 *
 * The portal is Fugaro's own service on `app.` plus the site's canonical host, derived by
 * `src/lib/portal.mjs` — the same function the deployed policy and the other checks derive it
 * from, so the page, the `allowed_hosts` entry the link check resolves it against and the policy
 * are one host rather than three copies of it. The link is absolute (the portal is another
 * origin), so a relative href is not it.
 */
export function portalLinkFailures(main, portal) {
  const hrefs = [...walk(main)].filter((el) => el.name === 'a' && el.attrs.has('href')).map((el) => el.attrs.get('href'))
  const isPortal = (href) => {
    try {
      return new URL(href).origin === portal
    } catch {
      return false
    }
  }
  if (!hrefs.some(isPortal)) {
    return [
      `the key request page carries no link to the portal (${portal}): a licence key is asked for in Fugaro's own signed-in service now, so the page's whole job is to send a reader there (the portal plan's P9.3; src/lib/portal.mjs)`,
    ]
  }
  return []
}

// ---- 3. the privacy link ------------------------------------------------------------------

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
      "the key request page carries no link to /privacy/: what the service keeps, and for how long, is stated there, so a reader sent to the portal is owed the route back to it",
    ]
  }
  return []
}

// ---- the gate -----------------------------------------------------------------------------

const FACTS = siteFacts()
const PORTAL = portalOrigin(FACTS.canonicalURL)

test('the key request page carries no form, and points a reader at the portal', () => {
  const main = mainOf(keysPage().html)
  for (const failures of [formFreeFailures(main), portalLinkFailures(main, PORTAL), privacyLinkFailures(main, FACTS)]) {
    assert.deepEqual(failures, [], failures.join('\n'))
  }

  // The deployed policy's half, read where the page's history touched it: the site may post
  // nowhere but itself, because it carries no form at all.
  const formAction = new Map(
    deployedCSP()
      .split(';')
      .map((part) => part.trim().split(/\s+/).filter(Boolean))
      .filter((t) => t.length)
      .map(([name, ...values]) => [name, values]),
  ).get('form-action')
  assert.deepEqual(formAction, ["'self'"], `firebase.json's form-action is ${JSON.stringify(formAction)}: the site carries no form, so the key request page's forms' intake widening came off with them (S3.3)`)
})

// ---- the refusals, kept -------------------------------------------------------------------

const SHELL = (body) =>
  `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>Get a key</h1>${body}</main></body></html>`

/** The shipped shape of the page: the portal link, the privacy link, and no control. */
const shipped = (body = `<a href="${PORTAL}" rel="noopener">Go to the portal</a><p><a href="/privacy/">privacy statement</a></p>`) => SHELL(body)

test('a form or a field on the key request page is refused', () => {
  assert.deepEqual(formFreeFailures(mainOf(shipped())), [], 'the shipped shape of the page is refused, so this case proves nothing')

  for (const [what, body, expected] of [
    ['a form', `<form method="post" action="/free-seat"><button>go</button></form>`, /carries a <form>/],
    ['a form posting to another host', `<form method="post" action="https://keys.example.net/free-seat"><button>go</button></form>`, /carries a <form>/],
    ['an input', `<input name="email" type="email">`, /carries a <input>/],
    ['a textarea', `<textarea name="request"></textarea>`, /carries a <textarea>/],
    ['a select', `<select name="plan"><option>x</option></select>`, /carries a <select>/],
  ]) {
    assert.match(formFreeFailures(mainOf(SHELL(body))).join('\n'), expected, `${what} was not refused as expected (${expected})`)
  }
})

test('a page with no portal link, or one pointing elsewhere, is refused', () => {
  assert.deepEqual(portalLinkFailures(mainOf(shipped()), PORTAL), [], 'the shipped page carries the link this case refuses, so it proves nothing')
  assert.match(
    portalLinkFailures(mainOf(SHELL('<h1>t</h1>')), PORTAL).join('\n'),
    /carries no link to the portal/,
    'a page with no portal link was not refused',
  )
  // A host that merely starts with the portal's, and a relative link, are not the portal: the
  // comparison is the origin, and the portal is another origin, so its link is absolute.
  for (const href of [`${PORTAL}.attacker.example/`, '/portal/', 'https://elsewhere.example/']) {
    assert.match(
      portalLinkFailures(mainOf(SHELL(`<a href="${href}">portal-ish</a>`)), PORTAL).join('\n'),
      /carries no link to the portal/,
      `${href} was taken for the portal`,
    )
  }
})

test('a page with no pointer at the privacy statement is refused', () => {
  assert.deepEqual(privacyLinkFailures(mainOf(shipped()), FACTS), [], 'the shipped page carries the link this case refuses, so it proves nothing')
  // The other way the site writes a link to one of its own pages: absolute on the canonical
  // origin, which is what W1.3 builds. Either form is a link to the statement.
  assert.deepEqual(
    privacyLinkFailures(mainOf(SHELL(`<a href="https://${FACTS.canonicalHost}/privacy/">privacy statement</a>`)), FACTS),
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
