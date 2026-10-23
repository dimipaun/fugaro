// website/src/lib/consent-key.mjs — the key the analytics decision is stored under.
//
// It is written once, here. The banner carries it in its markup for `public/scripts/site.js`,
// which writes and reads the decision; the layout carries it on the tag of
// `public/scripts/consent-early.js`, which reads it before the page is drawn so that the banner
// takes its place in the page from the first frame (the owner's ruling of 2026-10-23: the
// banner must not cover the first screen's content). Two readers, one literal.
export const CONSENT_KEY = 'fugaro:analytics-consent'
