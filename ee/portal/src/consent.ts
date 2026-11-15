// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Analytics consent (portal plan P1.4; D93, spec §Architecture).
//
// **Nothing is measured before a grant, and a withdrawal undoes what was.** The
// decision is one value in the browser's own storage, this file is the gate
// that reads it, and the analytics tag is loaded from behind that gate and
// nowhere else: a visitor who has not decided is not measured, and one who
// refused is not measured either. A withdrawal does more than stop the sends
// (the owner's ruling of 2026-10-18, which the website follows too): it deletes
// the cookies the tag wrote while the decision was a grant, because an
// identifier left in the browser keeps identifying somebody who has said no.
//
// **A visit with no decision reads no cookie and writes none.** `applyConsent`
// does nothing at all without a stored decision -- it neither starts nor stops
// -- so "no work before consent" is a property of the code rather than a
// promise in a comment, and the deletion below runs only on a decision that
// exists.
//
// **What is deleted is Google's cookies and nothing else**, on this host and on
// the parent domain the tag may have written them to (the website's script
// does the same, and for the same reason: the tag's own `auto` setting chooses
// between the two). A name that is not in the browser's jar costs nothing --
// nothing is written for a cookie that is not held.

import type { Analytics } from 'firebase/analytics'

/** The key the decision is kept under. It is a preference, not a credential. */
export const CONSENT_KEY = 'fugaro.portal.analytics'

/** A visitor's decision. `null` -- no decision -- is not one of these. */
export type ConsentDecision = 'granted' | 'refused'

export interface ConsentStore {
  /** The stored decision, or null when there is none (or it is not one of ours). */
  read(): ConsentDecision | null
  write(decision: ConsentDecision): void
}

/** The browser's own storage. Reading it is the only thing that touches it. */
export function browserConsentStore(storage: Storage = window.localStorage): ConsentStore {
  return {
    read() {
      const raw = storage.getItem(CONSENT_KEY)
      // A value that is not a decision is not a decision: a change to the
      // vocabulary, a hand-edited entry or another app's key reaches the gate
      // as "not decided", which measures nothing.
      return raw === 'granted' || raw === 'refused' ? raw : null
    },
    write(decision) {
      storage.setItem(CONSENT_KEY, decision)
    },
  }
}

/** What the gate drives. It is a seam so a test can watch the gate itself. */
export interface AnalyticsClient {
  start(): void
  stop(): void
}

/**
 * Applies the stored decision. It answers what it did, and answers `null` for
 * a visit with no decision -- in which case it has started nothing, stopped
 * nothing and read nothing.
 */
export function applyConsent(store: ConsentStore, client: AnalyticsClient): ConsentDecision | null {
  const decision = store.read()
  if (decision === 'granted') {
    client.start()
  } else if (decision === 'refused') {
    client.stop()
  }
  return decision
}

export function grantConsent(store: ConsentStore, client: AnalyticsClient): void {
  store.write('granted')
  client.start()
}

export function refuseConsent(store: ConsentStore, client: AnalyticsClient): void {
  store.write('refused')
  client.stop()
}

/** The prefix of every cookie the analytics tag writes: `_ga` and its children. */
const ANALYTICS_COOKIE_PREFIX = '_ga'

/** An expiry in the past is what removes a cookie; an empty value alone would not. */
const EXPIRED = 'expires=Thu, 01 Jan 1970 00:00:00 GMT'

/** The names the browser is holding. Reading them is the only way to find them all. */
function heldNames(): string[] {
  try {
    return String(document.cookie || '')
      .split(';')
      .map((part) => part.split('=')[0]?.trim() ?? '')
      .filter((name) => name !== '')
  } catch {
    // A browser that refuses to read them holds none this can see, which is the
    // same answer as an empty jar rather than a reason to stop.
    return []
  }
}

/** The host without its leftmost label, or '' when there is no label to spare. */
function parentDomain(host: string): string {
  const labels = host.split('.')
  return labels.length > 2 ? labels.slice(1).join('.') : ''
}

/** Deletes one cookie here, and then on the parent domain while it is still held. */
function drop(name: string, host: string): void {
  const scopes = ['']
  const parent = parentDomain(host)
  if (parent !== '') {
    scopes.push('; domain=' + parent)
  }
  for (const scope of scopes) {
    document.cookie = `${name}=; path=/${scope}; ${EXPIRED}`
    if (!heldNames().includes(name)) {
      return
    }
  }
}

/** Forgets every analytics cookie this browser is holding, and nothing else. */
export function forgetAnalyticsCookies(host: string = window.location.hostname): void {
  for (const name of heldNames()) {
    if (name.startsWith(ANALYTICS_COOKIE_PREFIX)) {
      drop(name, host)
    }
  }
}

/**
 * Firebase Analytics behind the gate (spec §Architecture: Google Analytics via
 * Firebase). The SDK is imported when the tag is started and never before: a
 * visitor who has not consented has not even downloaded the client that would
 * measure them, which is a stronger promise than not sending.
 *
 * With no measurement id -- a build or an environment where analytics are off
 * -- there is nothing to start against, so `start` does nothing. `stop` still
 * forgets, because a person who has withdrawn is entitled to the deletion
 * whoever configured the build.
 */
export function firebaseAnalyticsClient(measurementId: string | null): AnalyticsClient {
  type Loaded = { module: typeof import('firebase/analytics'); analytics: Analytics }
  let loaded: Promise<Loaded | null> | null = null

  function load(): Promise<Loaded | null> {
    loaded ??= Promise.all([import('firebase/app'), import('firebase/analytics')])
      .then(([app, module]) =>
        // No app means this build has no Firebase configuration at all, and an
        // analytics instance cannot exist without one.
        app.getApps().length === 0
          ? null
          : { module, analytics: module.getAnalytics(app.getApp()) },
      )
      // A failed load is not a reason to break the page: the decision is
      // recorded either way, and the tag is simply not there.
      .catch(() => null)
    return loaded
  }

  return {
    start() {
      if (measurementId === null || measurementId === '') {
        return
      }
      void load().then((ready) => {
        if (ready !== null) {
          ready.module.setAnalyticsCollectionEnabled(ready.analytics, true)
        }
      })
    },
    stop() {
      forgetAnalyticsCookies()
      // A withdrawal loads nothing: only a tag that was already there is told to
      // stop. Creating the client in order to switch it off would download the
      // very script the person has just refused.
      const ready = loaded
      if (ready === null) {
        return
      }
      void ready.then((tag) => {
        if (tag !== null) {
          tag.module.setAnalyticsCollectionEnabled(tag.analytics, false)
        }
      })
    },
  }
}

/** The build's measurement id, or null when this build measures nothing. */
export function measurementId(env: Record<string, string | undefined>): string | null {
  const id = env.VITE_FIREBASE_MEASUREMENT_ID ?? ''
  return id === '' ? null : id
}
