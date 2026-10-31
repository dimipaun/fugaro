// website/src/lib/consent-key.mjs: the keys a consent decision is stored under.
//
// Each is written once, here. The banner carries them in its markup for `public/scripts/site.js`,
// which writes and reads the decisions; the layout carries the analytics one on the tag of
// `public/scripts/consent-early.js`, which reads it before the page is drawn so that the banner
// takes its place in the page from the first frame (the owner's ruling of 2026-10-23: the
// banner must not cover the first screen's content). Two readers, one literal.
//
// **Two keys, because there are two questions.** Analytics is asked on every page and answered
// once for the site; the marketing measurement — the pixel, which is on every page too, and the
// insight tag and Calendly's embed, which are the four campaign pages' — is asked by the same
// bar under its own key, answered separately (F17, D401 as the owner amended it on 2026-10-31).
// A single key would let one answer stand for the other.
//
// **The marketing key's version moves with the question, and it has moved once.** Until the
// owner's ruling of 2026-10-31 that question was about the four campaign pages alone, and a
// visitor who answered it answered a narrower one than the site now asks: the pixel loads on
// every page of the site. Nothing may read an old answer as consent to the new question, so
// the key's version is part of the key — the answer a visitor gave the old question stays in
// their browser, unread, and the bar asks them once more on their next page. The analytics
// question's scope did not change, so its key does not move with this one.
export const CONSENT_KEY = 'fugaro:analytics-consent'
export const MARKETING_CONSENT_KEY = 'fugaro:marketing-consent:v2'
