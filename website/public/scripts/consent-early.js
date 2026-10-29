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
// drawn, which is right: with scripts off nothing can be loaded from Google either. The key and
// the "always" flag are its own tag's data attributes, written by the layout from
// `src/lib/consent-key.mjs`.
;(function () {
  var tag = document.currentScript
  if (!tag) return
  var key = tag.getAttribute('data-consent-key')
  if (!key) return
  var ask = tag.getAttribute('data-consent-always') === 'true'
  if (!ask) {
    try {
      ask = window.localStorage.getItem(key) === null
    } catch (err) {
      ask = true
    }
  }
  if (ask) document.documentElement.setAttribute('data-consent-ask', '')
})()
