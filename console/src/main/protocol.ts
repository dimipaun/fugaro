// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The bundled document's origin: a registered, standard, secure `app://` scheme
// (desktop-console plan Task 1.1; design section 5.1 rules 2 and 3).
//
// The document is served over `app://console/index.html` and never over
// `file://`, and the reason is the CSP rather than taste: a `file://` document
// is not served over HTTP, so `session.webRequest.onHeadersReceived` never
// fires for it and a `Content-Security-Policy` *header* cannot be attached to
// it -- only the `<meta http-equiv>` form works there, and the meta form is
// parsed late and is the weaker of the two. `protocol.handle('app', ...)` gives
// the response real headers, so the CSP of design section 5.1 rule 3 is
// delivered the way it is meant to be, with `src/renderer/index.html`'s
// `<meta>` copy kept as belt-and-braces. The scheme is `standard: true` (so the
// document has an origin, `app://console`, rather than an opaque one) and
// `secure: true`, with **no `bypassCSP`**.
//
// Nothing here is Electron-specific at runtime: this module imports *types*
// only from Electron, and every decision it makes is a pure function of a URL
// string and a bundle root, so `test/unit/window.test.ts` drives the refusals,
// the CSP header and the served document under plain Node -- where
// `require('electron')` is a path string rather than a process and would throw.
// The two functions that do need the Electron runtime take the `protocol`
// module as an argument, and `src/main/index.ts` is the one place that imports
// it.
//
// The handler is deny-by-default in the same shape as the rest of the shell:
// it serves one host, one flat set of files under one root, and it refuses
// everything else with a reason -- a scheme that is not `app:`, a host that is
// not `console`, credentials or a port in the URL, a query string (the bundle
// is static), a dot segment anywhere in the raw request (the traversal case),
// a NUL or a backslash, a path that decodes past the root (the single-encoded
// `%2e%2e%2f` spelling, which the URL parser does not normalise away), and a
// file that is not there. Every refusal carries the CSP header too.

import { readFile } from 'node:fs/promises'
import { extname, resolve, sep } from 'node:path'

import type { Protocol } from 'electron'

/** APP_SCHEME is the one scheme the console's own documents are served over. */
export const APP_SCHEME = 'app'

/**
 * APP_HOST is the one host served over it. A custom scheme needs an authority
 * to be `standard`, and a single fixed host is what makes the origin of every
 * document the console renders one string: `app://console`.
 */
export const APP_HOST = 'console'

/** APP_ORIGIN is the origin the renderer runs on; an e2e assertion names it. */
export const APP_ORIGIN = `${APP_SCHEME}://${APP_HOST}`

/** APP_ENTRY_PATH is the document the window loads. */
export const APP_ENTRY_PATH = '/index.html'

/** APP_ENTRY_URL is that document's absolute URL, and the window's only load. */
export const APP_ENTRY_URL = `${APP_ORIGIN}${APP_ENTRY_PATH}`

/**
 * REFUSAL_ENTRY_PATH is the refusal document (desktop-console plan Task 3.3;
 * design section 7.1 and D7).
 *
 * It is a **second document** and not a mode of the first, because the claim D7
 * makes -- "a refused version never shows the dashboard" -- is a claim about
 * what the window loaded. A refused console that loaded `index.html` and merely
 * asked its renderer not to draw would be one renderer bug away from a spend
 * figure nothing vouched for; a window on `refusal.html` has no dashboard root
 * to draw into at all, whatever the renderer does.
 */
export const REFUSAL_ENTRY_PATH = '/refusal.html'

/** REFUSAL_ENTRY_URL is that document's absolute URL, and the refusal window's
 * only load. Both URLs are the same origin and the same handler (rule 3's CSP
 * is sent with every response), so the refusal window is exactly as hardened as
 * the dashboard's -- it is only its document that differs. */
export const REFUSAL_ENTRY_URL = `${APP_ORIGIN}${REFUSAL_ENTRY_PATH}`

/**
 * PICKER_ENTRY_PATH is the installation picker's document (Task 3.4b; owner
 * ruling C9): a separate window opened from the application menu, with its own
 * preload and its own channel prefix, and PICKER_ENTRY_URL is the URL that
 * window loads and the one its channels' sender gate compares against
 * (`isSenderOnDocument` in `src/main/ipc.ts`).
 */
export const PICKER_ENTRY_PATH = '/installations.html'

/** PICKER_ENTRY_URL is the full URL of the picker's document. */
export const PICKER_ENTRY_URL = `${APP_ORIGIN}${PICKER_ENTRY_PATH}`

/**
 * SERVED_FILES is the whole of what the served root holds (desktop-console plan
 * Task 3.4a, step 4; Task 3.4b's second entry; Task 6.2's stylesheet; GV2.2's
 * layout worker; V2.5.1a's confirmation window): the dashboard's, the refusal's,
 * the picker's and the confirmation's documents, the script the dashboard loads
 * and the ones the picker and the confirmation load, the one stylesheet every
 * document links, and elkjs's layout worker.
 *
 * The root itself is `dist/bundle/` (`rendererRoot` in `src/main/index.ts`),
 * and the nine files are there because `npm run build` puts them there: `tsc`
 * compiles `src/renderer/entry.ts`, `src/renderer/installations.ts` and
 * `src/renderer/confirm.ts` and everything they require, and
 * `tools/bundle-renderer/index.ts` writes
 * `console.js`, `installations.js`, `confirm.js` and `console.css`, copies the
 * four documents verbatim, and copies `elk-worker.min.js` verbatim out of the
 * installed `elkjs` package. `test/unit/bundle.test.ts` holds this list and that
 * directory equal in both directions, so a file that appears beside the bundle
 * -- or a name dropped from this list -- is a failing test rather than a
 * document the console serves and nothing vouches for.
 *
 * **`/elk-worker.min.js` is the one third-party file this origin serves, and
 * GV2.2 adds it as a file rather than as a policy.** It is elkjs 0.11.0's own
 * `lib/elk-worker.min.js`, copied byte for byte by the build -- the digest is
 * asserted in `test/unit/bundle.test.ts` -- so none of it is this project's code
 * and none of it is editable here; its EPL-2.0 licence and its source are
 * `console/THIRD_PARTY_NOTICES.md`, packed beside the app. A worker is governed
 * by `script-src 'self'` while no `worker-src` is given, and
 * `app://console/elk-worker.min.js` is this origin, so this entry is the whole
 * of the change: no directive is added and none is loosened, which is what makes
 * the absence of `'unsafe-eval'` (rule 3) hold *inside* the worker too.
 *
 * **Task 6.2's `/console.css` is a stylesheet this origin serves rather than a
 * second policy.** `style-src 'self'` is already the
 * third directive of `CONTENT_SECURITY_POLICY` below, so a `<link
 * rel="stylesheet">` to `app://console/console.css` is admitted by a directive
 * that was always there: no directive is added, none is loosened, and
 * `default-src 'none'` and `connect-src 'none'` are not consulted for it (a
 * `<link>` is not a fetch `connect-src` governs). `mediaTypeFor` already maps
 * `.css`, so this entry is the whole of the handler's change.
 *
 * It is a list of URL paths rather than of file names because that is how the
 * handler speaks, and it is deliberately **not** a second filter inside
 * `resolveBundleRequest`: the root's contents are already decided by the build,
 * the handler's refusals are pinned by `test/unit/window.test.ts` (a path inside
 * the root that names no file is a 404, and one that names a file the read fails
 * on is a 500), and a filter here would turn that 500 into a 404 for every name
 * it does not list.
 */
export const SERVED_FILES: readonly string[] = Object.freeze([
  '/confirm.html',
  '/confirm.js',
  '/console.css',
  '/console.js',
  '/elk-worker.min.js',
  '/index.html',
  '/installations.html',
  '/installations.js',
  '/refusal.html',
])

/** CSP_HEADER is the response header the CSP travels in (rule 3). */
export const CSP_HEADER = 'Content-Security-Policy'

/**
 * CONTENT_SECURITY_POLICY is design section 5.1 rule 3, directive by directive
 * and in its order. `connect-src 'none'` is the load-bearing one: the renderer
 * never talks to the network, because every byte it draws arrives over IPC from
 * the main process. `test/unit/window.test.ts` holds this string to the
 * design's own wording, so a directive cannot be dropped in a later edit, and
 * asserts the `<meta>` copy in `src/renderer/index.html` still matches it.
 */
export const CONTENT_SECURITY_POLICY = [
  "default-src 'none'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'none'",
  "form-action 'none'",
  "base-uri 'none'",
  "frame-ancestors 'none'",
  "object-src 'none'",
].join('; ')

/**
 * APP_SCHEME_PRIVILEGES is what `registerSchemesAsPrivileged` is shown: the
 * scheme is standard and secure, and nothing else. `bypassCSP` is deliberately
 * absent -- it would be an off switch for the CSP header this module exists to
 * deliver, and the unit test asserts it is absent rather than merely unset.
 */
export const APP_SCHEME_PRIVILEGES = { standard: true, secure: true } as const

/**
 * MEDIA_TYPES is the closed set of content types the bundle may be served as.
 * A type that is not here is `application/octet-stream`, never a guess from the
 * file's bytes: a document the console serves is one of its own files, and the
 * list is an allow-list in the same spirit as everything else in this file.
 */
const MEDIA_TYPES: Readonly<Record<string, string>> = {
  '.css': 'text/css; charset=utf-8',
  '.html': 'text/html; charset=utf-8',
  '.ico': 'image/x-icon',
  '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.txt': 'text/plain; charset=utf-8',
  '.woff2': 'font/woff2',
}

/** mediaTypeFor is that list's lookup, by lower-cased extension. */
export function mediaTypeFor(filePath: string): string {
  return MEDIA_TYPES[extname(filePath).toLowerCase()] ?? 'application/octet-stream'
}

/** BundleRequest is a request the handler will serve, and the file it names. */
export type BundleRequest = {
  ok: true
  filePath: string
  mediaType: string
}

/**
 * BundleRequestRefusal is a request the handler will not serve, with the status
 * it answers with and a reason that names the rule. The reasons are fixed
 * strings rather than formatted with the request's own text: a hostile request
 * never gets to write its URL into a response body.
 */
export type BundleRequestRefusal = {
  ok: false
  status: number
  reason: string
}

/** BundleRequestDecision is `resolveBundleRequest`'s answer. */
export type BundleRequestDecision = BundleRequest | BundleRequestRefusal

function refuse(status: number, reason: string): BundleRequestRefusal {
  return { ok: false, status, reason }
}

/**
 * isInsideBundleRoot reports whether a resolved absolute path is a file *below*
 * the bundle root: the root itself is not, and neither is a sibling directory
 * whose name merely starts with the root's (`/opt/bundle-other/x` is not inside
 * `/opt/bundle`). The separator is appended before the prefix test, which is
 * what makes the second half true on POSIX and on Windows alike.
 *
 * It is the invariant the whole handler rests on -- the only files the console
 * ever serves are files it ships -- and it is tested on its own, because the
 * reachable request spellings that break it are few and the ones the URL parser
 * has already normalised away are worth naming anyway.
 */
export function isInsideBundleRoot(bundleRoot: string, candidate: string): boolean {
  // Both sides are resolved before they are compared, so the predicate answers
  // the question it claims to: `renderer/../main/index.js` is not inside
  // `renderer`, and a caller cannot make it say otherwise by handing over a
  // path that has not been normalised yet.
  const root = resolve(bundleRoot)
  const target = resolve(candidate)
  if (target === root) return false
  const rootWithSeparator = root.endsWith(sep) ? root : root + sep
  return target.startsWith(rootWithSeparator)
}

/**
 * hasDotSegment reports whether a raw URL holds a "." or ".." path segment,
 * literal or percent-encoded.
 *
 * This is a check on the RAW string, and that is the point. The WHATWG URL
 * parser normalises dot segments while it parses -- `app://console/../secret`
 * comes out as the pathname `/secret`, and `%2e%2e` counts as `..` for that
 * purpose -- so by the time there is a parsed URL there is nothing left to see
 * and a request like `app://console/../../etc/passwd` would answer 404 for
 * `<root>/etc/passwd` instead of being refused for what it is: a traversal
 * attempt. The bundle has no parent, nothing in it is named "." or "..", and
 * the check runs over the whole raw URL rather than over the parsed pathname,
 * so a request that spells its way up is refused deterministically rather than
 * by whether a file happens to exist.
 */
export function hasDotSegment(rawUrl: string): boolean {
  for (const rawSegment of rawUrl.split('/')) {
    let segment = rawSegment
    try {
      segment = decodeURIComponent(rawSegment)
    } catch {
      // A segment that is not valid percent-encoding cannot be a dot segment
      // spelled with %2e, and the decoded path below is refused separately.
    }
    if (segment === '.' || segment === '..') return true
  }
  return false
}

/**
 * resolveBundleRequest decides what to serve for one request URL, and refuses
 * everything that is not this bundle's own file:
 *
 *   - a scheme that is not `app:`, a host that is not `console`, a port,
 *     credentials in the URL, or a query string -- the bundle is static, and
 *     the window loads exactly one URL, `APP_ENTRY_URL`;
 *   - a dot segment anywhere in the raw URL (see `hasDotSegment`);
 *   - a path that is not valid percent-encoding, or that holds a NUL byte or a
 *     backslash (the bundle is joined with `/`, and a backslash is a path
 *     separator on Windows);
 *   - a path that decodes to something outside the root. That last one is
 *     reachable and not theoretical: `%2e%2e%2f%2e%2e%2f` is one segment as far
 *     as the URL parser is concerned, so it survives normalisation, and it
 *     decodes to `../../` here -- which is why the decode happens once, and
 *     why the resolved path is then checked against the root.
 *
 * A path inside the root that names no file is not this function's business; it
 * resolves, and `serveAppRequest` answers 404.
 */
export function resolveBundleRequest(bundleRoot: string, rawUrl: string): BundleRequestDecision {
  const root = resolve(bundleRoot)

  let url: URL
  try {
    url = new URL(rawUrl)
  } catch {
    return refuse(400, 'the request URL does not parse')
  }

  if (url.protocol !== `${APP_SCHEME}:`) {
    return refuse(403, `refused a request over a scheme that is not ${APP_SCHEME}:`)
  }
  if (url.hostname !== APP_HOST) {
    return refuse(403, `refused a request for a host that is not ${APP_HOST}`)
  }
  if (url.port !== '') {
    return refuse(403, 'refused a request carrying a port')
  }
  if (url.username !== '' || url.password !== '') {
    return refuse(403, 'refused a request URL carrying credentials')
  }
  if (url.search !== '') {
    return refuse(403, 'refused a request with a query: the bundle is static')
  }
  if (hasDotSegment(rawUrl)) {
    return refuse(403, `refused a traversal attempt: ${APP_SCHEME}://${APP_HOST}/ has no parent`)
  }

  let decodedPath: string
  try {
    decodedPath = decodeURIComponent(url.pathname)
  } catch {
    return refuse(400, 'refused a path that is not valid percent-encoding')
  }
  if (decodedPath.includes('\0')) {
    return refuse(403, 'refused a path holding a NUL byte')
  }
  if (decodedPath.includes('\\')) {
    return refuse(403, 'refused a path holding a backslash: the bundle is joined with /')
  }

  const requested = decodedPath === '' || decodedPath === '/' ? APP_ENTRY_PATH : decodedPath
  const filePath = resolve(root, `.${requested}`)
  if (!isInsideBundleRoot(root, filePath)) {
    return refuse(403, 'refused a request whose path leaves the bundle root')
  }

  return { ok: true, filePath, mediaType: mediaTypeFor(filePath) }
}

/**
 * bundleHeaders is the header set every response from the handler carries: the
 * content type, `nosniff` (a document served with the wrong type is a script
 * the renderer would not otherwise run), and the CSP of rule 3.
 *
 * Refusals carry it too. A refused request is not a document, but the header
 * costs nothing and its absence on any response from this scheme would be one
 * more thing to reason about.
 */
function bundleHeaders(contentType: string): Record<string, string> {
  return {
    'content-type': contentType,
    'x-content-type-options': 'nosniff',
    [CSP_HEADER]: CONTENT_SECURITY_POLICY,
  }
}

/** textResponse is a refusal: a plain-text reason, never a document. */
function textResponse(status: number, reason: string): Response {
  return new Response(`${reason}\n`, {
    status,
    headers: bundleHeaders('text/plain; charset=utf-8'),
  })
}

/**
 * serveAppRequest is the protocol handler's whole body: decide, read, answer.
 *
 * A path that names no file is 404 -- and `EISDIR` counts as "no file", because
 * a directory is not a resource this bundle serves: the request is answered
 * with the same reason as a missing one rather than with a 500 that would say
 * the console is broken. Any *other* failure to read is 500 with a fixed
 * reason. Neither arm is ever a partially drawn console, and neither carries the
 * request's own text. The e2e spec asserts against the real browser what this
 * returns here; the unit test asserts it directly, on the `Response`.
 */
export async function serveAppRequest(bundleRoot: string, rawUrl: string): Promise<Response> {
  const decision = resolveBundleRequest(bundleRoot, rawUrl)
  if (!decision.ok) return textResponse(decision.status, decision.reason)

  try {
    // The bytes are copied into a fresh `Uint8Array` rather than handed over as
    // the `Buffer` `readFile` returns: the copy is what makes the body a plain
    // `ArrayBuffer`-backed view, which is what a `Response` takes. `tsc` is
    // strict about that distinction, and it is right to be.
    const bytes = new Uint8Array(await readFile(decision.filePath))
    return new Response(bytes, { status: 200, headers: bundleHeaders(decision.mediaType) })
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code
    if (code === 'ENOENT' || code === 'ENOTDIR' || code === 'EISDIR') {
      return textResponse(404, 'no such file in the bundle')
    }
    return textResponse(500, 'the bundle file could not be read')
  }
}

/**
 * registerAppScheme declares the scheme before `app.whenReady()`, as Electron
 * requires: `standard` so `app://console` is an origin rather than an opaque
 * one, `secure` so it is treated as a secure context, and no `bypassCSP`.
 */
export function registerAppScheme(protocolModule: Protocol): void {
  protocolModule.registerSchemesAsPrivileged([
    { scheme: APP_SCHEME, privileges: APP_SCHEME_PRIVILEGES },
  ])
}

/**
 * handleAppRequests is what actually serves the bundle: every `app://` request
 * in the app's default session goes through `serveAppRequest`, and the only
 * files it will read are the ones under `bundleRoot`.
 */
export function handleAppRequests(protocolModule: Protocol, bundleRoot: string): void {
  protocolModule.handle(APP_SCHEME, (request) => serveAppRequest(bundleRoot, request.url))
}
