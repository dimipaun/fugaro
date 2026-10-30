// website/src/lib/consent-key.mjs: the keys a consent decision is stored under.
//
// Each is written once, here. The banner carries them in its markup for `public/scripts/site.js`,
// which writes and reads the decisions; the layout carries the analytics one on the tag of
// `public/scripts/consent-early.js`, which reads it before the page is drawn so that the banner
// takes its place in the page from the first frame (the owner's ruling of 2026-10-23: the
// banner must not cover the first screen's content). Two readers, one literal.
//
// **Two keys, because there are two questions.** Analytics is asked on every page and answered
// once for the site; the campaign measurement — the pixel, the insight tag and Calendly's
// embed — is asked only on the four campaign pages and answered separately (F17, D401). A
// single key would re-ask a visitor who had answered the site's question, and would let an
// answer about a campaign page's ads stand for the whole site.
export const CONSENT_KEY = 'fugaro:analytics-consent'
export const MARKETING_CONSENT_KEY = 'fugaro:marketing-consent'
