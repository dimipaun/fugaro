// website/public/scripts/consent-early.js: decides, before the page is drawn, whether the
// analytics banner is shown (the owner's ruling of 2026-10-23, the redesign's Step 3).
//
// Since that ruling the banner is a compact bar in the page's own flow, at the very top, above
// the header, instead of a bar fixed over the foot of the window: it never covers the first
// screen's content. A bar in the flow that appeared only when `site.js` ran (deferred, after the
// parse) would push the whole page down under the reader's eyes, so this file runs first, in
// the head, and only sets one attribute on `<html>`: `data-consent-ask` when the visitor has not
// decided yet (or the page always offers the choice, as the privacy page does). The stylesheet
// draws the banner from that attribute, so the banner is in its place from the first frame,
// and `site.js` removes the attribute when the visitor decides.
//
// It reads storage and nothing else: it loads nothing, sets no cookie and writes nothing to
// storage. With scripts off it never runs, the attribute is never set and the banner is never
// drawn, which is right: with scripts off nothing can be loaded from Google or from a marketing
// vendor either, because a decision is stored and read by script — which is also why the site
// ships no `<noscript>` pixel, or a `<noscript>` anything: a tag in one would load for a visitor
// who has decided nothing, before any consent could be asked (the owner's question of
// 2026-10-31). The keys and the "always" flag are its own tag's data attributes, written by the
// layout from `src/lib/consent-key.mjs`.
//
// **Every page carries a second key** (G2.3, D401 as the owner amended it on 2026-10-31): the
// marketing measurement — the pixel on every page, and the two campaign vendors on the four
// campaign pages — is asked by the same bar under its own decision, so a visitor who answered
// the site's question and has not answered the marketing one is still undecided about the bar
// they are looking at, and the bar has to be in its place from the first frame for that visitor
// too. A tag that carries neither key asks nothing, which is the state a build with
// `site.yaml`'s analytics flag off is in.
;(function () {
  var tag = document.currentScript
  if (!tag) return
  function undecided(attribute) {
    var key = tag.getAttribute(attribute)
    if (!key) return false
    try {
      return window.localStorage.getItem(key) === null
    } catch (err) {
      return true
    }
  }
  var keys = ['data-consent-key', 'data-marketing-consent-key']
  var ask = tag.getAttribute('data-consent-always') === 'true'
  for (var i = 0; !ask && i < keys.length; i++) ask = undecided(keys[i])
  if (ask) document.documentElement.setAttribute('data-consent-ask', '')
})()
