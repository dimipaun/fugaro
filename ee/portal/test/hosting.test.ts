// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The portal's Hosting configuration (portal plan P9.2; F21, F40, D2194).
//
// Three readings here are the ones nobody notices breaking until the site is
// live. The file is **one** hosting object naming the portal's own site: a
// second site, a `target` or the website's `public` would put one deploy's
// bytes on the other's domain, which is F40's risk exactly. The CSP is read
// directive by directive rather than matched as a string, because it is what
// stands between a signed-in page holding licence keys and any script a supply
// chain reaches: `script-src` is `'self'` and three named origins, `object-src`
// and `frame-ancestors` are off, and the API is reached same-origin -- which is
// the rewrite below, not a host in `connect-src`. And the rewrite's service and
// region are held to the two files that decide them (P1.7's Terraform and
// P9.2's deploy script): a rewrite to a service that does not exist, or one in
// another region, is a portal whose every request fails.
//
// Nothing here deploys anything or reaches the network: the configuration is
// read as text, and the two facts it has to agree with are read from the
// repository.

import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

interface RunRewrite {
  source: string
  run: { serviceId: string; region: string }
}

interface FileRewrite {
  source: string
  destination: string
}

interface HeaderRule {
  source: string
  headers: { key: string; value: string }[]
}

interface HostingConfig {
  site?: string
  target?: string
  public?: string
  ignore?: string[]
  rewrites?: (RunRewrite | FileRewrite)[]
  headers?: HeaderRule[]
}

interface FirebaseConfig {
  hosting?: HostingConfig
}

function read(path: string): string {
  return readFileSync(new URL(path, import.meta.url), 'utf8')
}

const parsed = JSON.parse(read('../firebase.json')) as FirebaseConfig
const hosting: HostingConfig = parsed.hosting ?? {}
const rewrites = hosting.rewrites ?? []
const headers = hosting.headers ?? []

/** The header a rule sets, or undefined when the rule does not set it. */
function header(rule: HeaderRule, key: string): string | undefined {
  return rule.headers.find((entry) => entry.key === key)?.value
}

/**
 * The CSP, as directives. A browser ignores a directive it does not know, so
 * the policy is read as a map rather than searched as a string: a misspelling
 * is then a missing directive here rather than a line that looks right.
 */
function policy(): Map<string, string[]> {
  const rule = headers.find((entry) => header(entry, 'Content-Security-Policy') !== undefined)
  const value = rule === undefined ? undefined : header(rule, 'Content-Security-Policy')
  if (value === undefined) {
    throw new Error('ee/portal/firebase.json sets no Content-Security-Policy header')
  }
  const directives = new Map<string, string[]>()
  for (const part of value.split(';')) {
    const tokens = part.trim().split(/\s+/).filter((token) => token !== '')
    const name = tokens[0]
    if (name !== undefined) {
      directives.set(name, tokens.slice(1))
    }
  }
  return directives
}

/** A directive's tokens, or a failure naming the directive that is missing. */
function directive(name: string): string[] {
  const tokens = policy().get(name)
  if (tokens === undefined) {
    throw new Error(`the portal's CSP has no ${name} directive`)
  }
  return tokens
}

describe('the portal hosting configuration', () => {
  it('is one hosting object for the portal site alone', () => {
    expect(Array.isArray(parsed.hosting)).toBe(false)
    expect(hosting.site).toBe('fugaro-portal')
    // A target is a deploy-time name a CI job can redirect: the portal's site is
    // written here, so nothing but this file chooses where a deploy lands.
    expect(hosting.target).toBeUndefined()
    expect(hosting.public).toBe('dist')
    expect(hosting.ignore).toContain('**/.*')
  })

  it('publishes the SPA bundle and leaves the website where it is', () => {
    const website = JSON.parse(read('../../../firebase.json')) as FirebaseConfig
    expect(website.hosting?.public).toBe('website/dist')
    expect(hosting.public).not.toBe(website.hosting?.public)
    expect(JSON.stringify(parsed)).not.toContain('website/dist')
  })

  it('rewrites the API before the SPA fallback, which is last', () => {
    expect(rewrites).toHaveLength(2)
    const api = rewrites[0] as RunRewrite
    expect(api.source).toBe('/api/**')
    expect(api.run.serviceId).toBe('fugaro-portal-api')
    // The fallback has to be last: Firebase serves the first rewrite that
    // matches, so a page route in front of it would shadow the API.
    const fallback = rewrites[1] as FileRewrite
    expect(fallback.source).toBe('**')
    expect(fallback.destination).toBe('/index.html')
  })

  it('names the API service P1.7 deploys, in the region P9.2 deploys it to', () => {
    const terraform = read('../../keyservice/terraform/services/variables.tf')
    const service = /variable "api_service"[\s\S]*?default\s*=\s*"([^"]+)"/.exec(terraform)?.[1]
    expect(service).toBe('fugaro-portal-api')
    expect((rewrites[0] as RunRewrite).run.serviceId).toBe(service)

    const script = read('../../../scripts/deploy-portal.sh')
    const region = /^DEFAULT_REGION=(.*)$/m.exec(script)?.[1]
    expect(region).not.toBeUndefined()
    expect((rewrites[0] as RunRewrite).run.region).toBe(region)
    // The deploy is told which configuration to read, and the default is this
    // file: a deploy that fell back to the repository's own firebase.json would
    // publish the SPA to the website's site.
    expect(/^DEFAULT_CONFIG=(.*)$/m.exec(script)?.[1]).toBe('ee/portal/firebase.json')
  })

  it('serves one policy on every path', () => {
    const every = headers.filter((rule) => rule.source === '**')
    expect(every).toHaveLength(1)
    expect(every.map((rule) => header(rule, 'Content-Security-Policy'))).not.toContain(undefined)
    // Only the cache rule may touch a narrower path: a second rule setting the
    // CSP would be a page served under a policy nobody read.
    for (const rule of headers) {
      if (rule.source !== '**') {
        expect(header(rule, 'Content-Security-Policy')).toBeUndefined()
      }
    }
  })

  it('allows scripts from the bundle and three named origins, and no inline script', () => {
    const scripts = directive('script-src')
    expect(scripts).toContain("'self'")
    expect(scripts).toContain('https://apis.google.com')
    expect(scripts).toContain('https://www.googletagmanager.com')
    expect(scripts).toContain('https://cdn.paddle.com')
    expect(scripts).not.toContain("'unsafe-inline'")
    expect(scripts).not.toContain("'unsafe-eval'")
    expect(scripts).not.toContain('*')
    expect(scripts).toHaveLength(4)
  })

  it('is same-origin by default and reaches only the origins the app calls', () => {
    expect(directive('default-src')).toEqual(["'self'"])
    const connect = directive('connect-src')
    // The API is this origin: `connect-src 'self'` is the rewrite working, and
    // a host here instead would be a portal reaching an API it did not deploy.
    expect(connect).toContain("'self'")
    for (const host of [
      'https://identitytoolkit.googleapis.com',
      'https://securetoken.googleapis.com',
      'https://firebaseinstallations.googleapis.com',
      'https://www.googleapis.com',
      'https://*.google-analytics.com',
      'https://*.ingest.sentry.io',
      'https://*.ingest.us.sentry.io',
      'https://*.paddle.com',
    ]) {
      expect(connect).toContain(host)
    }
    const frames = directive('frame-src')
    // Firebase Auth's sign-in flow is an iframe of the project's auth domain and
    // Paddle's checkout is an overlay of its own: both are frames, and neither
    // is a script.
    expect(frames).toContain('https://*.firebaseapp.com')
    expect(frames).toContain('https://accounts.google.com')
    expect(frames).toContain('https://*.paddle.com')
    expect(directive('img-src')).toEqual([
      "'self'",
      'data:',
      'https://www.googletagmanager.com',
      'https://*.google-analytics.com',
    ])
    expect(directive('style-src')).toContain("'self'")
    expect(directive('font-src')).toEqual(["'self'"])
  })

  it('refuses framing, plugins, a moved base and off-origin forms', () => {
    expect(directive('object-src')).toEqual(["'none'"])
    expect(directive('base-uri')).toEqual(["'self'"])
    expect(directive('frame-ancestors')).toEqual(["'none'"])
    // A form here posts only to itself: the key service's intake is the
    // website's form action, and the portal's checkout is a frame.
    expect(directive('form-action')).toEqual(["'self'"])
    expect(directive('upgrade-insecure-requests')).toEqual([])
  })

  it('names only directives a browser knows and only https origins', () => {
    const known = new Set([
      'default-src',
      'script-src',
      'style-src',
      'img-src',
      'font-src',
      'connect-src',
      'frame-src',
      'object-src',
      'base-uri',
      'form-action',
      'frame-ancestors',
      'upgrade-insecure-requests',
    ])
    for (const [name, tokens] of policy()) {
      expect(known).toContain(name)
      for (const token of tokens) {
        expect(token.startsWith('http://')).toBe(false)
      }
    }
  })

  it('sets the browser protections that are not the policy', () => {
    const every = headers.find((rule) => rule.source === '**') as HeaderRule
    expect(header(every, 'X-Content-Type-Options')).toBe('nosniff')
    expect(header(every, 'X-Frame-Options')).toBe('DENY')
    expect(header(every, 'Referrer-Policy')).toBe('strict-origin-when-cross-origin')
    expect(header(every, 'Strict-Transport-Security')).toContain('max-age=')
    expect(header(every, 'Permissions-Policy')).toContain('camera=()')
  })

  it('revalidates the page and caches the bundle by its hash', () => {
    const every = headers.find((rule) => rule.source === '**') as HeaderRule
    expect(header(every, 'Cache-Control')).toBe('no-cache')
    const assets = headers.find((rule) => rule.source === '/assets/**') as HeaderRule
    // Vite names every emitted file with its content hash, so a year is safe
    // here and a deploy is picked up by the page's own revalidation above.
    expect(header(assets, 'Cache-Control')).toBe('public, max-age=31536000, immutable')
  })

  it('carries the business licence notice beside it', () => {
    expect(read('../firebase.json.license')).toBe(
      'SPDX-License-Identifier: LicenseRef-Fugaro-Subscription\nSee ee/LICENSE for the terms.\n',
    )
  })
})
