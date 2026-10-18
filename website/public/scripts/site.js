/* website/public/scripts/site.js — the site's one script (decision D183, redesign parts P3
 * and P6, website plan Task W1.12's gate and W3.2's analytics).
 *
 * The site ships exactly one first-party script file and this is it: one request, one line
 * in the byte budget, one file to read. `website/test/budget.mjs` counts these bytes as
 * every page's JavaScript, `website/test/copy.mjs` holds the controls it drives to the
 * markup they are written in, and `website/test/consent.mjs` runs it as the consent gate.
 *
 * What it does:
 *
 *   - **The copy control on every command block (part P6).** The markup renders each block
 *     with its control already in it and `hidden`, and this reveals the control and drives
 *     it. A browser that runs no script therefore shows the command and no button, rather
 *     than a button that copies nothing.
 *   - **Escape and a click outside close an open menu (part P3).** The menu's two levels are
 *     `<details>` and open and close by themselves (and the groups share a `name`, so only
 *     one is open at a time); what a `<details>` does not do is close when the reader
 *     presses Escape or clicks somewhere else, which is what a menu owes a keyboard. Closing
 *     is all this adds: with no script the menu still opens, still closes and still reaches
 *     every link.
 *   - **The consent banner, and the analytics gate behind it (W1.12, W3.2; D93, D106).** The
 *     banner's two buttons write one decision to `localStorage`; this file reveals the banner
 *     when no decision is stored, and — only when that decision is `granted` — loads Google's
 *     analytics script. Nothing is requested from Google before that, and a refusal or a
 *     dismissal loads nothing at all.
 *
 * **The analytics gate is here rather than in an inline block because D183 admits exactly one
 * script, and because an inline block would need a hash in the deployed Content-Security-
 * Policy while a first-party file needs only `script-src 'self'`.** Everything the gate needs
 * to know — the property id, the one origin it may load from, and the key the decision is
 * stored under — it reads from the banner's markup (`data-analytics-id`,
 * `data-analytics-origin`, `data-consent-key`), which the layout emits only when `site.yaml`
 * says analytics are on. So this file names no host and no URL at all: with the flag off it
 * finds no banner and does nothing, and a test that scans the artifact for the analytics
 * origin finds it in the markup and in the deployed policy, never here.
 *
 * It writes text with `textContent` only and builds no markup of its own (the one element it
 * creates is the analytics script tag, and only after consent), and `website/test/copy.mjs`
 * and `website/test/consent.mjs` run it against the built pages and read it for anything that
 * would evaluate or fetch.
 *
 * Nothing here is load-bearing: the commands are in the markup in full, and every one of
 * them is readable, selectable and copyable by hand without this file ever arriving; the
 * menu is a `<details>` at both levels and needs none of this to work; and with no script the
 * banner is not shown at all, so a reader who runs none is measured by nothing.
 *
 * It is loaded with `defer` from the base layout, so it runs once the document has been
 * parsed and every block and banner it looks for is in the tree. The only thing it stores is
 * the consent decision, under the key the banner's markup names, and it sets no cookie.
 */
;(function () {
  'use strict'

  // How long the confirmation stays before it clears itself. A state that never clears is
  // one a reader cannot tell from a fresh one.
  var CLEARS_AFTER_MS = 4000

  // The menu, and everything that is open in it (P3).
  var OPEN_MENUS = '.site-nav details[open]'

  // Whether this browser has a clipboard to write to at all. A control that cannot do the
  // one thing it offers is worse than no control, so a browser without one keeps the
  // markup's `hidden` exactly as it is.
  function canCopy() {
    return !!(navigator.clipboard && typeof navigator.clipboard.writeText === 'function')
  }

  // The text a block offers: the element the block marks as its own source, or the code
  // element inside it. The terminal marks its command, because what a reader wants out of
  // that window is the invocation and not the prompt and the caret drawn around it.
  function sourceOf(block) {
    return block.querySelector('[data-copy-source]') || block.querySelector('pre, code')
  }

  // The one thing the control says, in the live region the markup carries for it. The
  // previous message's timer is not cancelled but guarded instead: a second copy replaces
  // the text, and the older timer no longer matches it, so the newer message keeps its own
  // full time on screen.
  function say(block, message) {
    var status = block.querySelector('[data-copy-status]')
    if (!status) return
    status.textContent = message
    window.setTimeout(function () {
      if (status.textContent === message) status.textContent = ''
    }, CLEARS_AFTER_MS)
  }

  function copyFrom(block) {
    var source = sourceOf(block)
    if (!source) return
    navigator.clipboard.writeText(source.textContent || '').then(
      function () {
        say(block, 'Copied')
      },
      function () {
        // A browser that refuses the write — an unfocused document, a permission the reader
        // has declined — says so rather than reporting a copy that did not happen.
        say(block, 'Copying failed: select the command and copy it yourself')
      },
    )
  }

  // Whether a menu holds the element: the drawer's panel is its toggle's next sibling rather
  // than its child (see the layout's note), so "inside the drawer" is the toggle or that panel.
  function holds(details, element) {
    if (!element) return false
    if (details.contains(element)) return true
    var panel = details.nextElementSibling
    return !!(panel && details.classList.contains('site-nav-drawer') && panel.contains(element))
  }

  // Close every open menu. A menu closed under the reader's focus would leave that focus on
  // an element that is no longer rendered, so the control that opened it takes the focus
  // back — which is where a keyboard reader expects to be after dismissing a menu. Menus come
  // in document order, outermost first, so the toggle that takes the focus is the outermost
  // one that holds it and is on the page: the drawer's on a narrow viewport (closing it hides
  // the groups' own toggles), the group's on a wide one (where the drawer is not drawn).
  function closeMenus() {
    var active = document.activeElement
    var open = document.querySelectorAll(OPEN_MENUS)
    var back = null
    for (var i = 0; i < open.length; i++) {
      var summary = open[i].querySelector('summary')
      if (!back && summary && holds(open[i], active) && summary.getClientRects().length > 0) back = summary
    }
    for (var j = 0; j < open.length; j++) open[j].open = false
    if (back) back.focus()
  }

  function menu() {
    document.addEventListener('keydown', function (event) {
      if (event.key === 'Escape') closeMenus()
    })
    document.addEventListener('click', function (event) {
      var target = event.target
      if (target && target.closest && target.closest('.site-nav')) return
      closeMenus()
    })
  }

  // ---- the consent banner, and the analytics gate behind it (W1.12, W3.2) ----------------

  // The banner's own element id, the status line inside it, and the page's content region.
  var BANNER_ID = 'consent-banner'
  var STATUS_ID = 'consent-banner-status'
  var MAIN_ID = 'main'

  // The two choices, as the markup names them, and the one answer that loads anything.
  var GRANTED = 'granted'
  var DENIED = 'denied'

  // The event the banner announces a choice with, so the gate below can act on the page the
  // visitor is already on rather than only on the next page load.
  var CONSENT_EVENT = 'fugaro:consent'

  // The gate's own state, and the two config attributes the banner carries: the property to
  // report to and the one origin to load the script from. Both are absent from the markup
  // while `site.yaml`'s flag is off, which is what makes this whole section inert then.
  var ANALYTICS_ID_ATTR = 'data-analytics-id'
  var ANALYTICS_ORIGIN_ATTR = 'data-analytics-origin'

  // The decision, as the browser stored it, or null when there is none to read. Storage the
  // browser refuses is not a decision: an unreadable answer is no answer rather than a guess,
  // and the gate needs a readable `granted` before it fetches anything.
  function stored(banner) {
    try {
      return window.localStorage.getItem(banner.getAttribute('data-consent-key'))
    } catch (err) {
      return null
    }
  }

  // Keep a decision, and say whether it was really kept. A browser in private mode may accept
  // the write and store nothing, and the visitor has to be told that rather than asked again
  // in silence.
  function keep(banner, decision) {
    var key = banner.getAttribute('data-consent-key')
    try {
      window.localStorage.setItem(key, decision)
      return window.localStorage.getItem(key) === decision
    } catch (err) {
      return false
    }
  }

  function announce(decision) {
    window.dispatchEvent(new CustomEvent(CONSENT_EVENT, { detail: { analytics: decision } }))
  }

  // The analytics script, fetched once and only after a grant (D93, D106). The property and
  // the origin come from the markup, and the origin is the single third-party exception the
  // owner decided on: the deployed policy's `script-src` admits it and `website/test/origins.mjs`
  // refuses every other. (This file names no host itself, not even in a comment: a gate that
  // reads the whole file for a second analytics vendor reads its prose too, and a comment is
  // cheaper to reword than a gate is to weaken.)
  //
  // What it asks Google for, and what it does not (the owner's ruling of 2026-10-18): one
  // `config` call, which sends one `page_view` per page and nothing else — no `event` calls,
  // no user id — with Google Signals and ads personalisation turned off in the same call, so
  // the property is not asked for the advertising features the site does not use. GA4
  // anonymises the visitor's IP by default; there is no setting to add for it.
  //
  // Three review findings on W3.2: consent mode's default (everything denied) is queued first
  // and the grant lifts `analytics_storage` alone, so no advertising storage is written; the
  // page is reported without its query string or fragment; and a refusal after a grant sets
  // Google's documented opt-out flag (`ga-disable-<id>`), so the page already measuring stops.
  function bare(address) {
    return String(address || '').split(/[?#]/)[0]
  }

  function analytics(banner) {
    var id = banner.getAttribute(ANALYTICS_ID_ATTR)
    var origin = banner.getAttribute(ANALYTICS_ORIGIN_ATTR)
    if (!id || !origin) return

    var loaded = false
    var OPT_OUT = 'ga-disable-' + id

    function gtag() {
      window.dataLayer.push(arguments)
    }

    function load() {
      window[OPT_OUT] = false
      if (loaded) {
        gtag('consent', 'update', { analytics_storage: GRANTED })
        return
      }
      loaded = true
      window.dataLayer = window.dataLayer || []
      gtag('consent', 'default', {
        ad_storage: DENIED,
        ad_user_data: DENIED,
        ad_personalization: DENIED,
        analytics_storage: DENIED,
      })
      gtag('consent', 'update', { analytics_storage: GRANTED })
      gtag('js', new Date())
      gtag('config', id, {
        allow_google_signals: false,
        allow_ad_personalization_signals: false,
        page_location: bare(window.location && window.location.origin) + bare(window.location && window.location.pathname),
        page_referrer: bare(document.referrer),
      })
      var script = document.createElement('script')
      script.async = true
      script.src = origin + '/gtag/js?id=' + encodeURIComponent(id)
      document.head.appendChild(script)
    }

    function withdraw() {
      window[OPT_OUT] = true
      if (loaded) gtag('consent', 'update', { analytics_storage: DENIED })
    }

    if (stored(banner) === GRANTED) load()
    window.addEventListener(CONSENT_EVENT, function (event) {
      var decision = event && event.detail && event.detail.analytics
      if (decision === GRANTED) load()
      else withdraw()
    })
  }

  function consent() {
    var banner = document.getElementById(BANNER_ID)
    if (!banner) return

    var status = document.getElementById(STATUS_ID)

    function dismiss() {
      banner.hidden = true
      // Focus goes to the content, not back to the top of the page and not to a control
      // that has just disappeared. `tabindex="-1"` makes <main> a focus target without
      // putting it in the tab order.
      var main = document.getElementById(MAIN_ID)
      if (main) {
        main.setAttribute('tabindex', '-1')
        main.focus()
      }
    }

    function choose(decision) {
      if (!keep(banner, decision)) {
        // The answer cannot be kept, so it is not honoured: nothing is loaded, the banner
        // stays, and the status line says why.
        if (status) status.hidden = false
        announce(DENIED)
        return
      }
      dismiss()
      announce(decision)
    }

    var controls = banner.querySelectorAll('[data-consent]')
    for (var i = 0; i < controls.length; i++) {
      ;(function (control) {
        control.addEventListener('click', function () {
          choose(control.getAttribute('data-consent'))
        })
      })(controls[i])
    }

    // Escape dismisses, and a dismissal is a refusal: the gate fetches nothing until
    // something stores `granted`, so ignoring the banner measures nothing either.
    window.addEventListener('keydown', function (event) {
      if (banner.hidden) return
      if (event.key === 'Escape' || event.key === 'Esc') choose(DENIED)
    })

    // The choice is offered when there is no readable decision — and always on the privacy
    // page, which is where a visitor withdraws one.
    if (banner.getAttribute('data-consent-always') === 'true' || stored(banner) === null) banner.hidden = false

    analytics(banner)
  }

  function start() {
    menu()
    consent()
    var blocks = document.querySelectorAll('[data-copy]')
    if (blocks.length === 0) return
    if (!canCopy()) return
    for (var i = 0; i < blocks.length; i++) {
      var block = blocks[i]
      var button = block.querySelector('[data-copy-button]')
      if (!button) continue
      button.hidden = false
      bind(block, button)
    }
  }

  function bind(block, button) {
    button.addEventListener('click', function () {
      copyFrom(block)
    })
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start)
  else start()
})()
