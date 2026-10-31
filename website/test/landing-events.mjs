// website/test/landing-events.mjs — the campaign pages' key events, and the campaign
// parameters the booking link carries (go-to-market plan, Task G2.4; spec §6 and §13;
// reconciliation F22, D401).
//
// **What this file holds.** The four landing pages are where the ads send people, so the
// agenda measures them: `demo_booked`, `install_click` and `whatsapp_click` are the funnel's
// key events in GA4 (spec §13), the same three actions are sent to Meta's pixel under its own
// names (the owner's ruling of 2026-10-31), and the campaign parameters a visitor arrived
// through are passed on to the booking link so that a booking can be attributed to the campaign
// that paid for it. Five promises come out of that, and each is a case below.
//
//   1. **The events are GA4's, so they wait for the analytics grant.** A visitor who has not
//      granted it is measured by nothing — not by the tag, and not by an event the tag would
//      have carried. The queue they go into (`dataLayer`) does not exist before a grant, and
//      the cases below watch it rather than the source: the two links are pressed and the
//      booking frame reports on a page that has granted nothing, and the queue is still absent.
//   2. **A booking is the widget's own booking report, from one sender only.** `demo_booked` is
//      a message Calendly's inline widget posts when a booking is made, so it is accepted only
//      when the message *is* that report — the widget posts several others over the same
//      channel, its frame height and a viewed event type among them — AND it comes from the
//      frame that widget built inside this page's booking container AND from the origin the
//      booking link names. Anything else is dropped: another origin, another window of this
//      page, a message the page never asked for, or one of the widget's own non-booking
//      messages. No field of the message is forwarded either — the event is the funnel's count,
//      and the vendor's payload is the visitor's (F22).
//   3. **The campaign parameters go into the booking link and nowhere else.** They are
//      allow-listed by key, capped in length and encoded (F22): not into the chat message,
//      which the visitor would read and a vendor the privacy text does not name would receive;
//      not into GA4; and not onto the plain link a visitor who declined the marketing consent
//      keeps. A value the page cannot vouch for is dropped rather than passed on, and a visit
//      with no campaign parameters leaves the link exactly as `website/data/landing.yaml`
//      writes it.
//   4. **The pixel's three are the same handlers under Meta's names, and wait for the campaign
//      answer.** `ViewContent`, `Contact` and `Lead` are sent from the same press and the same
//      widget report as the GA4 events — with the same once-per-action binding and the same
//      origin and sender checks, which is why no case below re-tests those checks for the pixel
//      beyond the booking — and the gate is the other consent: a visitor who has answered the
//      campaign question and not the site's is sent the pixel's three and none of GA4's, and a
//      page whose pixel id is empty sends Meta nothing at all (R3).
//   5. **The stub the page builds is Meta's own shape, so a call made after its script has
//      loaded still arrives.** `fbevents.js` attaches `callMethod` to the stub the page already
//      has and drains the queue through it once; from then on a call pushed onto the queue is
//      never read, so the page's stub hands a call to `callMethod` whenever one is there. A stub
//      that only ever pushed would strand every event a press sends after the load — and the
//      withdrawal's revoke with it, which is the privacy page's own claim about the pixel.
//
// **The red step, and what it was.** The plan's red step reads: "the tests fail because the
// events are not emitted." That is what they did: with G2.3 merged and G2.4 unwritten, the
// booking address on the embed was the file's link with no query (no parameter ever reached
// it) and no `event` call was queued for any of the three actions, so every case below failed
// on the thing it is about rather than on a missing fixture. The `wa.me` link the chat event
// belongs to did not exist on the page either — G2.2 left the calls to action as the booking
// and the repository, and the plan gives the chat link to this task (spec §6) — so the chat
// case failed on a page with no such link until G2.4 rendered it.
//
// **The fix round's red step.** The stub the page built was the page's own: it pushed every call
// onto `queue` and never looked for `callMethod`, so once Meta's script had drained the queue
// nothing the page sent afterwards was read — the events a press and a booking send, and the
// withdrawal's revoke. The case below that loads a fake script and reads what it is handed fails
// on that stub and passes on Meta's own shape.
//
// **It runs the page the way a browser does**, in the same stub the other gates that run
// `site.js` use (`test/lib/dom.mjs`), against the artifact `npm run check` builds: the built
// campaign page, the site's one script file read out of `dist/`, the address a browser serves
// the page at, and the storage a visitor who has already answered brings with them. The
// booking frame is planted into the page's own booking container, because the build ships that
// container empty — Calendly's script fills it after a grant — and a booking report can only
// come from a frame that exists. Standard library only; no network, no browser: what a real
// Calendly widget posts, and what a real GA4 property records, is the owner's live check
// (Check 160, and Check 161 for the attribution a test booking carries).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { fakeDOM } from './lib/dom.mjs'
import { DIST, builtPages, findAll, parse, siteFacts, textOf } from './lib/html.mjs'
import { loadLanding } from '../src/lib/landing.mjs'
import { CONSENT_KEY, MARKETING_CONSENT_KEY } from '../src/lib/consent-key.mjs'

// ---- the facts the cases read --------------------------------------------------------------

// The booking link, and the four variants: both are `website/data/landing.yaml`'s, read through
// the same loader the pages read them through, so a case that compares the booking address with
// a literal of its own would be the second copy this repository refuses.
const LANDING = loadLanding()

// The message Calendly's inline widget posts when a booking is made, and the other messages the
// same widget posts over the same channel: its frame's height, a viewed event type, a chosen
// date. All of them come from the same origin and the same window, so a listener that read only
// those two would count a booking when the widget was drawn.
const BOOKING_REPORT = 'calendly.event_scheduled'
const WIDGET_MESSAGES = ['calendly.page_height', 'calendly.event_type_viewed', 'calendly.date_and_time_selected', 'calendly.profile_page_viewed']

// The campaign page the cases below run, and the path the browser serves it at: the pages are
// directories, so a visitor is at `/save/`, and that is the spelling the loader's path check
// reads (`site.js`'s `route`).
const CAMPAIGN_REL = 'save/index.html'
const CAMPAIGN_PATH = '/save/'
const PAGE_ORIGIN = 'https://site.example'

// The ids the calls-to-action section gives the two links a key event is about, and the
// container the booking frame is built inside. `site.js` reads them out of the markup rather
// than out of a list of its own.
const INSTALL_ID = 'campaign-install'
const WHATSAPP_ID = 'campaign-whatsapp'
const EMBED_ID = 'calendly-embed'

// The two links as the page words them, which is how a case finds them in the built markup.
const DEMO_LABEL = 'Book a demo'
const CHAT_LABEL = 'Chat with the founder'

// The origin the booking frame reports from: the booking link's own, which is what `site.js`
// derives and what the deployed policy's `frame-src` admits (G2.3). It is not written here as a
// literal — it is the file's link, read the way the script reads it.
const BOOKING_ORIGIN = new URL(LANDING.calendlyURL).origin

// The pixel's id, read from `site.yaml` rather than written here: the owner supplied it on
// 2026-10-31, and the built campaign page hands it to the pixel — so a case that watches the
// pixel load compares the two rather than restating the value (the file's own rule).
const PIXEL_ID = siteFacts().marketing.metaPixelID
assert.ok(PIXEL_ID.length > 0, 'site.yaml carries no pixel id, so the built campaign page has no pixel to watch and the cases below could not tell a switched-off loader from a missing one')

/** The built campaign page, as the artifact serves it. */
function builtPage(rel = CAMPAIGN_REL) {
  const found = builtPages(DIST).find((one) => one.rel === rel)
  assert.ok(found, `${rel} is not in the built artifact: the four campaign pages are G2.2's, and this case reads one of them`)
  return found.html
}

/**
 * The address a browser serves the page at, with the query a visitor arrived through. The
 * `search` is the campaign parameters the ads carry — and, in the cases below, the ones the
 * page must not pass on.
 */
function address(search = '') {
  return {
    href: `${PAGE_ORIGIN}${CAMPAIGN_PATH}${search}`,
    origin: PAGE_ORIGIN,
    hostname: 'site.example',
    pathname: CAMPAIGN_PATH,
    search,
    hash: '',
  }
}

/**
 * The page with the booking frame in it, as Calendly's own script leaves the page: the widget
 * builds an `iframe` inside the container the calls-to-action section emits, and the script
 * takes a booking report from that frame. The build ships the container empty — it is hidden
 * until the visitor grants the marketing consent, and no frame exists before the vendor's
 * script has run — so the cases that are about a booking plant it, the way `consent.mjs` plants
 * the two vendor ids.
 */
function withFrame(html) {
  const planted = html.replace(/(<div[^>]*id="calendly-embed"[^>]*>)/, '$1<iframe src="about:blank"></iframe>')
  assert.notEqual(planted, html, `the campaign page carries no #${EMBED_ID} to plant the booking frame in, so no booking case below could run`)
  return planted
}

/**
 * The same page with the pixel's id as a case wants it. The owner supplied the id on
 * 2026-10-31 and the built page carries it, and an id cleared in `site.yaml` is the loader
 * switched off (R3) — a state the artifact no longer ships, so the one case that is about it
 * writes the empty value into the markup.
 */
function withPixelId(id, html = withFrame(builtPage())) {
  const written = /data-meta-pixel-id="[^"]*"/
  assert.match(html, written, 'the campaign page carries no pixel id attribute for a case to write a value into, so the case below could not run')
  return html.replace(written, `data-meta-pixel-id="${id}"`)
}

/**
 * A campaign page, run the way a browser runs it. `storage` is what the visitor's browser
 * already holds — the answers they have given on other pages — and `granted` presses the
 * banner's own yes, which on a campaign page answers both questions at once.
 */
function run({ search = '', storage = {}, granted = true, html = withFrame(builtPage()) } = {}) {
  const dom = fakeDOM(html, { location: address(search), storage })
  if (granted) dom.choose('granted')
  return dom
}

/** Every call the page queued for GA4, as plain arrays: what `gtag` was asked for, in order. */
function queued(dom) {
  // `Array.from` rather than the queue's own `map`: the queue is built inside the page's own
  // realm, and an array of that realm is not `deepStrictEqual` to one of this file's.
  return Array.from(dom.window.dataLayer ?? [], (call) => Array.from(call))
}

/** The key events the page sent, in order, and nothing else it queued. */
function sent(dom) {
  return queued(dom)
    .filter((call) => call[0] === 'event')
    .map((call) => call[1])
}

/**
 * Every call the page queued for Meta's pixel, as plain arrays — `init`, the page view and the
 * events — read out of the queue the pixel's own snippet reads. A page that loaded no pixel has
 * no queue at all, which is the state every case about consent reads.
 */
function pixelCalls(dom) {
  return Array.from(dom.window.fbq?.queue ?? [], (call) => Array.from(call))
}

/**
 * The pixel's own events, in order: what it was asked to track, without `init` and without the
 * page view the loader sends — those are `site.js`'s load rather than an action a visitor took,
 * and the cases below are about the three actions.
 */
function tracked(dom) {
  return pixelCalls(dom)
    .filter((call) => call[0] === 'track' && call[1] !== 'PageView')
    .map((call) => call[1])
}

/**
 * Meta's own script arriving, as `fbevents.js` does it: it attaches `callMethod` to the stub the
 * page already has — the stub `site.js` builds is that stub — and drains the queue through it
 * once. From then on a call that is still pushed onto `queue` is never read again, which is the
 * half a case has to model to tell a live stub from a stranded one: a loader that only recorded
 * the queue would read the same either way. What it returns is every call the script was handed,
 * the drained ones first, so a case can read what reached Meta rather than what the page meant.
 * The script is never fetched — no network — and what Meta does with a call afterwards is Meta's
 * business and not this site's.
 */
function loadPixelScript(dom) {
  const fbq = dom.window.fbq
  assert.ok(fbq, 'the page built no pixel stub, so no case could watch Meta\'s own script arrive')
  const reached = []
  fbq.callMethod = function () {
    reached.push(Array.from(arguments))
  }
  for (const queued of fbq.queue.splice(0)) fbq.callMethod.apply(fbq, queued)
  return reached
}

/** The booking address the page handed the embed, which is what Calendly's script builds from. */
function bookingAddress(dom) {
  const embed = dom.document.getElementById(EMBED_ID)
  assert.ok(embed, `the campaign page carries no #${EMBED_ID} for the booking address to be written on`)
  return embed.getAttribute('data-url')
}

/** The window inside the page's booking frame, which is the one sender a booking is taken from. */
function frameWindow(dom) {
  const frame = dom.document.getElementById(EMBED_ID).querySelector('iframe')
  assert.ok(frame, 'the planted booking frame is not in the page, so no booking case below could run')
  return frame.contentWindow
}

/** Press one of the calls to action, as a pointer or a keyboard does. */
function press(dom, id) {
  const link = dom.document.getElementById(id)
  assert.ok(link, `the campaign page carries no #${id} link to press, and a key event is about one of its calls to action`)
  link.dispatch('click', { type: 'click' })
}

/**
 * The booking frame's own report, as Calendly's widget posts one: the frame the page embedded,
 * the origin the booking link names, and the message that says a booking was made. The payload
 * beside it is the vendor's own, and the default is the message the widget sends for a booking
 * — a case that wants one of the widget's other messages hands it in.
 */
function report(dom, data = { event: BOOKING_REPORT }) {
  dom.window.dispatchEvent({ type: 'message', origin: BOOKING_ORIGIN, source: frameWindow(dom), data })
}

/**
 * A message at the page's window that is NOT the booking frame's own booking report: another
 * origin, a sender that is not the embedded frame, a message that names no sender at all (which
 * is what a `null` source is in a browser), or one of the widget's own non-booking messages.
 */
function foreignReport(dom, { origin = BOOKING_ORIGIN, source = {}, data = { event: BOOKING_REPORT } } = {}) {
  dom.window.dispatchEvent({ type: 'message', origin, source, data })
}

// ---- 1. nothing before the grant ----------------------------------------------------------

test('no key event is sent before the visitor grants the analytics consent', () => {
  // A first visit to a campaign page: the banner is offered and nothing is granted. Every
  // action a key event is about is taken — both links are pressed and the booking frame
  // reports — and the queue GA4's events go into does not exist at all, which is the same
  // promise `consent.mjs` holds the tag to from the other end.
  const dom = run({ granted: false })
  press(dom, INSTALL_ID)
  press(dom, WHATSAPP_ID)
  report(dom)
  assert.equal(
    dom.window.dataLayer,
    undefined,
    `the page queued ${JSON.stringify(queued(dom))} before the visitor granted the analytics consent: the three key events are GA4's, and a visitor who has not granted it is measured by nothing (D93, W3.2)`,
  )
  // And nothing reached the other measurement either: the pixel waits for the campaign answer,
  // which this visitor has not given, so its queue does not exist and no event could be sent.
  assert.equal(
    dom.window.fbq,
    undefined,
    `the page queued ${JSON.stringify(pixelCalls(dom))} before the visitor answered either question: the pixel waits for the campaign consent, and a visitor who has granted nothing is measured by nothing`,
  )

  // And the answer they wait for is the analytics one. A visitor who granted the campaign
  // measurement on this page and has not answered the site's question sends nothing: the two
  // are two questions with two answers, and the second does not stand for the first (F17).
  const campaignOnly = run({ granted: false, storage: { [MARKETING_CONSENT_KEY]: 'granted' } })
  press(campaignOnly, INSTALL_ID)
  press(campaignOnly, WHATSAPP_ID)
  report(campaignOnly)
  assert.equal(
    campaignOnly.window.dataLayer,
    undefined,
    `the page queued ${JSON.stringify(queued(campaignOnly))} for a visitor who answered the campaign question and not the site's: the key events are GA4's, and GA4 loads on the analytics answer alone`,
  )
})

// ---- 2. each event, once, on its own action ------------------------------------------------

test('each key event is sent once, on the action it is about, and only after the grant', () => {
  const dom = run()
  assert.deepEqual(sent(dom), [], 'the page sent a key event on its own load, and every one of the three is about something a visitor did')

  // One press is one event: a listener bound twice would send the same event twice, which is
  // the defect a case that only ever pressed once could not see.
  press(dom, INSTALL_ID)
  assert.deepEqual(sent(dom), ['install_click'], `pressing Install free sent ${JSON.stringify(sent(dom))}`)
  press(dom, INSTALL_ID)
  assert.deepEqual(sent(dom), ['install_click', 'install_click'], `the second press of Install free sent ${JSON.stringify(sent(dom))}: one press is one event`)

  press(dom, WHATSAPP_ID)
  assert.deepEqual(sent(dom), ['install_click', 'install_click', 'whatsapp_click'], `pressing the chat link sent ${JSON.stringify(sent(dom))}`)

  report(dom)
  assert.deepEqual(
    sent(dom),
    ['install_click', 'install_click', 'whatsapp_click', 'demo_booked'],
    `the booking frame's own report sent ${JSON.stringify(sent(dom))}`,
  )

  // And the events carry nothing: the funnel counts them, the page's own address reaches GA4
  // through the tag's `config` call, and no campaign parameter is put beside an event (F22).
  assert.deepEqual(
    queued(dom).filter((call) => call[0] === 'event'),
    [['event', 'install_click'], ['event', 'install_click'], ['event', 'whatsapp_click'], ['event', 'demo_booked']],
    `a key event was sent with more than its own name: ${JSON.stringify(queued(dom).filter((call) => call[0] === 'event'))}`,
  )

  // The state most visitors arrive in: the analytics answer was given on another page, so the
  // gate loads at the page's own start and the events are sent from the first action.
  const returning = run({ granted: false, storage: { [CONSENT_KEY]: 'granted' } })
  press(returning, INSTALL_ID)
  assert.deepEqual(
    sent(returning),
    ['install_click'],
    `a visitor who granted analytics on another page sent ${JSON.stringify(sent(returning))}: a stored answer is an answer, and the campaign question left open does not take it back`,
  )
})

// ---- 2b. the same three actions, as the pixel's own events ---------------------------------
//
// Meta's pixel is told the same three actions under its own names (the owner's ruling of
// 2026-10-31): `ViewContent` for a press of Install free, `Contact` for the chat link and
// `Lead` for a booking. They are the same handlers — the same press, the same once-per-action
// binding, the same origin and sender checks on the widget's message — so a change to one
// measurement's events is a change to the other's, and these cases hold the second half of
// that. The pixel's gate is its own: it waits for the campaign consent, which a visitor may
// give without answering the site's question, and an empty id means it never runs at all.

test('each pixel event is sent once, on the action it is about, and only after the campaign grant', () => {
  const dom = run()
  // The load itself: the id and one page view, which is what the grant is allowed to send
  // before anything has happened on the page.
  assert.deepEqual(tracked(dom), [], `the page sent a pixel event on its own load, and each of the three is about something a visitor did: ${JSON.stringify(pixelCalls(dom))}`)

  press(dom, INSTALL_ID)
  assert.deepEqual(tracked(dom), ['ViewContent'], `pressing Install free sent ${JSON.stringify(tracked(dom))}`)
  press(dom, INSTALL_ID)
  assert.deepEqual(tracked(dom), ['ViewContent', 'ViewContent'], `the second press of Install free sent ${JSON.stringify(tracked(dom))}: one press is one event, and a listener bound twice would send the same event twice`)

  press(dom, WHATSAPP_ID)
  assert.deepEqual(tracked(dom), ['ViewContent', 'ViewContent', 'Contact'], `pressing the chat link sent ${JSON.stringify(tracked(dom))}`)

  report(dom)
  assert.deepEqual(tracked(dom), ['ViewContent', 'ViewContent', 'Contact', 'Lead'], `the booking frame's own report sent ${JSON.stringify(tracked(dom))}`)

  // And the events carry nothing: the same promise the GA4 events are held to, on the other
  // sender — no field of a vendor's message, and no campaign parameter (F22).
  assert.deepEqual(
    pixelCalls(dom).filter((call) => call[0] === 'track' && call[1] !== 'PageView'),
    [['track', 'ViewContent'], ['track', 'ViewContent'], ['track', 'Contact'], ['track', 'Lead']],
    `a pixel event was sent with more than its own name: ${JSON.stringify(pixelCalls(dom).filter((call) => call[0] === 'track'))}`,
  )
})

test('once Meta\'s own script has loaded, a later event and the withdrawal\'s revoke reach it rather than the queue', () => {
  // The stub the page builds is Meta's own shape, and the half that matters is what happens
  // after `fbevents.js` arrives: it attaches `callMethod` to the stub the page already has and
  // drains the queue through it once, and from then on a call that is still pushed onto `queue`
  // is never read. A stub that only ever pushed would strand every event a visitor's press sends
  // after the load — and the withdrawal's revoke with them, which is the privacy page's own
  // claim that the pixel is told to revoke.
  const dom = run()
  const reached = loadPixelScript(dom)
  assert.deepEqual(
    reached.map((call) => call.slice(0, 2).map(String)),
    [['set', 'autoConfig'], ['init', PIXEL_ID], ['track', 'PageView']],
    `the script read ${JSON.stringify(reached)} out of the queue: the queue the page left is what Meta's own script drains when it loads, in the order the page made the calls`,
  )

  press(dom, WHATSAPP_ID)
  report(dom)
  assert.deepEqual(
    reached.map((call) => String(call[1])),
    ['autoConfig', PIXEL_ID, 'PageView', 'Contact', 'Lead'],
    `after the script had loaded, a press and a booking left ${JSON.stringify(reached)} with the tag: a call made after the load goes to \`callMethod\`, because the queue it would otherwise be pushed onto is one Meta's script has already drained`,
  )
  assert.deepEqual(
    pixelCalls(dom),
    [],
    `the page left ${JSON.stringify(pixelCalls(dom))} in the queue after Meta's script had drained it, where nothing reads them again`,
  )

  dom.choose('denied')
  assert.deepEqual(
    reached.map((call) => call.slice(0, 2).map(String)).slice(-1),
    [['consent', 'revoke']],
    `the withdrawal left ${JSON.stringify(reached)} with the tag: the privacy page says the pixel is told to revoke, and a revoke pushed onto a queue Meta has already drained is a revoke nobody hears`,
  )
  assert.deepEqual(pixelCalls(dom), [], `the withdrawal left ${JSON.stringify(pixelCalls(dom))} in the queue after Meta's script had drained it`)
})

test('the pixel waits for the campaign answer alone, and the site\'s own answer is not it', () => {
  // The two questions are two answers (F17). A visitor who granted the campaign measurement on
  // another page arrives with the pixel running and GA4 absent, and the events follow the same
  // split: the pixel's three are sent, GA4's are not.
  const dom = run({ granted: false, storage: { [MARKETING_CONSENT_KEY]: 'granted' } })
  assert.deepEqual(tracked(dom), [], 'the stored campaign grant did not load the pixel, so the events below would prove nothing')
  assert.deepEqual(
    pixelCalls(dom).map((call) => call.slice(0, 2).map(String)),
    [['set', 'autoConfig'], ['init', PIXEL_ID], ['track', 'PageView']],
    `the pixel was handed ${JSON.stringify(pixelCalls(dom))}: the automatic collection off, then the id site.yaml writes, once, and one page view on the page it was built for`,
  )

  press(dom, WHATSAPP_ID)
  press(dom, INSTALL_ID)
  report(dom)
  assert.deepEqual(
    tracked(dom),
    ['Contact', 'ViewContent', 'Lead'],
    `a visitor whose campaign answer is a grant and whose analytics answer is open sent ${JSON.stringify(tracked(dom))} to the pixel`,
  )
  assert.equal(
    dom.window.dataLayer,
    undefined,
    `the same actions queued ${JSON.stringify(queued(dom))} for GA4: the key events are GA4's and wait for the analytics grant, which this visitor has not given`,
  )
})

test('a booking is a Lead only when the embedded frame reports it from the booking link\'s origin', () => {
  // The same one sender the GA4 booking is taken from (the listener is one, so the checks are
  // one), read out of the other end: another origin, a sender that is not the embedded frame, a
  // message that names no sender, and the widget's own non-booking messages are all no event —
  // and no field of the report that IS a booking reaches the pixel either.
  const dom = run()
  foreignReport(dom, { origin: 'https://not-the-booking-host.example' })
  foreignReport(dom, { source: {} })
  foreignReport(dom, { source: null })
  for (const message of WIDGET_MESSAGES) report(dom, { event: message, payload: { height: 700 } })
  assert.deepEqual(
    tracked(dom),
    [],
    `a message that is not the embedded frame's own booking report sent ${JSON.stringify(tracked(dom))} to the pixel: only the report that says a booking was made is the funnel's event, and the widget's others arrive from the same frame, at the same origin, over the same channel`,
  )

  const marker = 'a-field-of-the-vendors-message'
  report(dom, { event: BOOKING_REPORT, payload: { invitee: { email: marker }, event: { uri: marker } } })
  assert.deepEqual(tracked(dom), ['Lead'], `the booking frame's own report sent ${JSON.stringify(tracked(dom))}`)
  assert.ok(
    !JSON.stringify(pixelCalls(dom)).includes(marker),
    `a field of the message that reported the booking reached the pixel: ${JSON.stringify(pixelCalls(dom))}`,
  )
})

test('with the pixel id empty nothing reaches Meta, and GA4\'s own events are unaffected', () => {
  // `site.yaml` carries the owner's id, so the loader is on: this is the state the file's own
  // rule describes rather than the one the site ships in, and it is what an id cleared there
  // would do (R3). The empty id is the pixel's alone — the analytics gate is a different
  // question with a different answer, and its events are not switched off by it.
  const dom = run({ html: withPixelId('') })
  press(dom, INSTALL_ID)
  press(dom, WHATSAPP_ID)
  report(dom)
  assert.equal(
    dom.window.fbq,
    undefined,
    `the page queued ${JSON.stringify(pixelCalls(dom))} for a pixel whose id is empty: an empty id is a loader that does not load, and no event may be sent through one`,
  )
  assert.deepEqual(
    sent(dom),
    ['install_click', 'whatsapp_click', 'demo_booked'],
    `with the pixel id empty the page sent ${JSON.stringify(sent(dom))} to GA4: the pixel's configuration is the pixel's, and an id cleared for it does not touch the analytics gate`,
  )
})

// ---- 3. a booking comes from one sender, and says one thing --------------------------------

test('a booking is taken only from the embedded frame, reporting from the booking link\'s origin', () => {
  const dom = run()

  // Another origin, from the frame the page embedded: a frame that navigated away, or a
  // document served by somebody else, is not Calendly's report.
  foreignReport(dom, { origin: 'https://not-the-booking-host.example' })
  assert.deepEqual(sent(dom), [], 'a message from another origin was taken as a booking')

  // The right origin, from a window that is not the frame: the page's own window, a popup, or
  // a second frame the vendor's script did not build here.
  foreignReport(dom, { source: {} })
  assert.deepEqual(sent(dom), [], 'a message from a window that is not the embedded frame was taken as a booking')

  // A message that names no sender at all is the same answer as the wrong one.
  foreignReport(dom, { source: null })
  assert.deepEqual(sent(dom), [], 'a message with no sender was taken as a booking')

  // And the frame's own report, which is the booking.
  report(dom)
  assert.deepEqual(sent(dom), ['demo_booked'], `the booking frame's own report sent ${JSON.stringify(sent(dom))}`)
})

test('a booking forwards no field of the message that reported it', () => {
  // What Calendly posts is its own payload — the invitee, the event, the times. None of it is
  // GA4's: the event is the funnel's count, and a field the page forwarded would be the
  // visitor's own details sent to a third party the privacy text does not name for them (F22).
  const marker = 'a-field-of-the-vendors-message'
  const dom = run()
  report(dom, { event: 'calendly.event_scheduled', payload: { invitee: { email: marker }, event: { uri: marker } } })
  assert.deepEqual(sent(dom), ['demo_booked'], `the booking frame's own report sent ${JSON.stringify(sent(dom))}`)
  assert.ok(
    !JSON.stringify(queued(dom)).includes(marker),
    `a field of the message that reported the booking reached GA4: ${JSON.stringify(queued(dom))}`,
  )
})

test('a booking is the widget\'s own booking report, and none of its other messages', () => {
  const dom = run()

  // The framing the listener reads is right — the embedded frame, at the booking link's origin
  // — and the message is the widget's own too, but it is not the one that says a booking was
  // made. The widget posts its frame's height before a visitor has done anything at all, and a
  // viewed event type and a chosen date while they are still choosing, so a listener that took
  // any message from the right frame would count a page render as the funnel's conversion.
  for (const message of WIDGET_MESSAGES) {
    report(dom, { event: message, payload: { height: 700 } })
    assert.deepEqual(
      sent(dom),
      [],
      `the widget's own ${message} was taken as a booking: ${JSON.stringify(sent(dom))}. Only the report that says a booking was made is the funnel's event, and the others arrive from the same frame, at the same origin, over the same channel`,
    )
  }

  // A message with no payload, and one whose payload is not an object: neither is the report,
  // and neither may throw the listener out of the page. `{}` is the message that names nothing.
  for (const data of [{}, { event: '' }, null, 'calendly.event_scheduled', 42]) {
    report(dom, data)
    assert.deepEqual(sent(dom), [], `a message whose data is ${JSON.stringify(data)} was taken as a booking: ${JSON.stringify(sent(dom))}`)
  }

  // And a message that carries no `data` at all, dispatched by hand because the helper's
  // default argument stands for the booking report itself.
  dom.window.dispatchEvent({ type: 'message', origin: BOOKING_ORIGIN, source: frameWindow(dom), data: undefined })
  assert.deepEqual(sent(dom), [], `a message with no data at all was taken as a booking: ${JSON.stringify(sent(dom))}`)

  // And the booking itself, from the same frame, which is what the case above is the contrast
  // to: the widget does report one, and it is counted.
  report(dom)
  assert.deepEqual(sent(dom), ['demo_booked'], `the widget's booking report sent ${JSON.stringify(sent(dom))}`)
})

// ---- 4. the campaign parameters, into the booking link and nowhere else --------------------

test('the campaign parameters a visitor arrived through reach the booking address', () => {
  const dom = run({ search: '?utm_source=newsletter&utm_medium=email&utm_campaign=save-launch' })
  assert.equal(
    bookingAddress(dom),
    `${LANDING.calendlyURL}?utm_source=newsletter&utm_medium=email&utm_campaign=save-launch`,
    `the booking address is ${JSON.stringify(bookingAddress(dom))}, and the parameters the visitor arrived through are passed on to the link landing.yaml writes (spec §6)`,
  )
})

test('a visit with no campaign parameters leaves the booking address as the file writes it', () => {
  // The plain link's own address, and a query that carries something other than a campaign
  // parameter: the page adds nothing it cannot vouch for, and the link is left exactly as
  // `website/data/landing.yaml` writes it.
  for (const search of ['', '?token=leaked-query', '?q=leaked&page=2#leaked-fragment']) {
    const dom = run({ search })
    assert.equal(
      bookingAddress(dom),
      LANDING.calendlyURL,
      `a visit to ${JSON.stringify(search || 'the page with no query')} produced the booking address ${JSON.stringify(bookingAddress(dom))}, and only an allow-listed campaign parameter may change it`,
    )
  }
})

test('a campaign parameter is allow-listed, capped and encoded before it reaches the booking address', () => {
  // Markup: the value is a string a visitor's link carried, so it is encoded rather than
  // pasted into an address the vendor's script builds a frame from.
  const markup = run({ search: '?utm_source=%3Cscript%3Ealert(1)%3C%2Fscript%3E' })
  assert.ok(
    !/[<>]/.test(bookingAddress(markup)),
    `a campaign parameter reached the booking address as markup: ${JSON.stringify(bookingAddress(markup))}`,
  )
  assert.equal(
    bookingAddress(markup),
    `${LANDING.calendlyURL}?utm_source=%3Cscript%3Ealert(1)%3C%2Fscript%3E`,
    `the encoded value is ${JSON.stringify(bookingAddress(markup))}`,
  )

  // A value with the characters an address is made of: encoded, so the campaign name is one
  // parameter and not three.
  const breaking = run({ search: '?utm_campaign=a%26b%3Dc%23d' })
  assert.equal(bookingAddress(breaking), `${LANDING.calendlyURL}?utm_campaign=a%26b%3Dc%23d`, `a value that carries an address's own characters produced ${JSON.stringify(bookingAddress(breaking))}`)

  // Over-long: dropped rather than truncated, because half a campaign name is a campaign the
  // page cannot name — and the other parameter of the same visit is untouched.
  const overLong = run({ search: `?utm_source=${'a'.repeat(101)}&utm_medium=email` })
  assert.equal(
    bookingAddress(overLong),
    `${LANDING.calendlyURL}?utm_medium=email`,
    `a 101-character value produced ${JSON.stringify(bookingAddress(overLong))}: a value longer than the cap is dropped, and a shorter one beside it is not`,
  )

  // A key that is not one of the agenda's five: dropped, whether it merely resembles one, is
  // spelled differently, or carries nothing.
  // A key that is not one of the agenda's five: dropped, whether it merely resembles one, is
  // spelled differently, or carries nothing.
  for (const search of ['?utm_evil=x', '?utm=x', '?UTM_SOURCE=y', '?utm_source=', '?utm_source']) {
    const dom = run({ search })
    assert.equal(
      bookingAddress(dom),
      LANDING.calendlyURL,
      `${JSON.stringify(search)} produced the booking address ${JSON.stringify(bookingAddress(dom))}: the page passes on the parameters it allow-lists by name and nothing else`,
    )
  }

  // A value spelled the way a link spells a space: a browser's own query parser reads `+` as a
  // space, so the page does too, and the campaign the visitor is attributed to is the one the
  // link named. Passing the `+` through would put a campaign name nobody chose on the link.
  const plus = run({ search: '?utm_source=newsletter+test' })
  assert.equal(
    bookingAddress(plus),
    `${LANDING.calendlyURL}?utm_source=newsletter%20test`,
    `?utm_source=newsletter+test produced the booking address ${JSON.stringify(bookingAddress(plus))}: a plus in a query is a space, and the page that re-encodes the value must read it the way the browser that served the link did`,
  )
})

// ---- 5. after a withdrawal ------------------------------------------------------------------

/**
 * The privacy page with the two calls to action in it. The bar that withdraws the site's answer
 * is the privacy page's — on a campaign page, a press about the campaign never rewrites the
 * answer the visitor gave the site (G2.3) — and the links the key events are about are the
 * campaign page's, so the case needs both under the one script that would really handle them.
 * The links are the built campaign page's own markup, copied rather than typed.
 */
function withdrawingPage() {
  const built = builtPage()
  const list = /<ul[^>]*class="campaign-calls-list"[^>]*>[\s\S]*?<\/ul>/.exec(built)
  assert.ok(list, 'the built campaign page carries no calls-to-action list to plant, so the links the key events are about could not be pressed after a withdrawal')
  const embed = /<div[^>]*id="calendly-embed"[^>]*>\s*<\/div>/.exec(built)
  assert.ok(embed, 'the built campaign page carries no booking container to plant, so the frame a booking is reported from would not exist')
  const privacy = builtPage('privacy/index.html')
  const planted = privacy.replace('</body>', `${list[0]}${embed[0]}</body>`)
  assert.notEqual(planted, privacy, 'the built privacy page carries no </body> for the calls to action to be planted before, so the fixture would be the page unchanged')
  return withFrame(planted)
}

test('a withdrawal stops the key events, and a stored refusal never starts them', () => {
  // The visitor granted analytics on another page and takes the answer back from the banner
  // that always offers the choice (W1.12's withdrawal). Every action a key event is about is
  // taken after that press, and none of them may be sent: the grant is what the events wait
  // for, and a withdrawal is the end of it. This is the half of the README's sentence that a
  // change leaving the gate's flag set would break in silence — every other case here runs on
  // a page whose answer is a grant.
  const dom = run({ granted: false, storage: { [CONSENT_KEY]: 'granted' }, html: withdrawingPage() })
  press(dom, INSTALL_ID)
  assert.deepEqual(
    sent(dom),
    ['install_click'],
    `a visitor whose stored answer is a grant sent ${JSON.stringify(sent(dom))} on the first press: an answer given on another page is an answer, and the events are sent from the first action`,
  )

  dom.choose('denied')
  press(dom, INSTALL_ID)
  press(dom, WHATSAPP_ID)
  report(dom)
  assert.deepEqual(
    sent(dom),
    ['install_click'],
    `after withdrawing, the page sent ${JSON.stringify(sent(dom))}: a withdrawal stops the key events as well as the tag, and a visitor who has taken the answer back is not measured by the next press`,
  )

  // And the page a withdrawn answer leads to: the next campaign page the visitor reads asks
  // Google for nothing, so the events have no queue to be sent into at all.
  const next = run({ granted: false, storage: { [CONSENT_KEY]: 'denied' } })
  press(next, INSTALL_ID)
  press(next, WHATSAPP_ID)
  report(next)
  assert.equal(
    next.window.dataLayer,
    undefined,
    `a campaign page served to a visitor whose stored answer is a refusal queued ${JSON.stringify(queued(next))}: a refusal is honoured on every page, because the key events are the analytics gate's own`,
  )
})

test('the campaign parameters reach the booking link and no other address on the page', () => {
  // The three other places a parameter could leak (F22): the chat message, which the visitor
  // reads and WhatsApp receives; the plain booking link, which is the one a visitor who
  // declined the marketing consent keeps; and GA4, which the reconciliation records as
  // receiving the page's origin and path only.
  const search = '?utm_source=newsletter&utm_campaign=save-launch'
  const dom = run({ search })
  press(dom, INSTALL_ID)
  press(dom, WHATSAPP_ID)
  report(dom)
  assert.ok(
    !JSON.stringify(queued(dom)).includes('newsletter'),
    `a campaign parameter reached GA4: ${JSON.stringify(queued(dom))}`,
  )

  const doc = parse(withFrame(builtPage()))
  const links = (label) => findAll(doc, (el) => el.name === 'a' && textOf(el).trim() === label)
  // The booking link is on the page twice: the hero's, which is the first screen's action, and
  // the closing band's (the owner's feedback of 2026-10-30). Both are the file's one link and
  // neither may carry a campaign parameter — the hero's is the one a visitor meets first, so a
  // parameter that leaked into either would reach the booking from the top of the page.
  const demo = links(DEMO_LABEL)
  assert.equal(demo.length, 2, `the campaign page carries ${demo.length} "${DEMO_LABEL}" links`)
  for (const link of demo) {
    assert.equal(
      link.attrs.get('href'),
      LANDING.calendlyURL,
      `a plain booking link is ${JSON.stringify(link.attrs.get('href'))}: it is the link landing.yaml writes, with no query of its own, and it is what a visitor who declined the marketing consent keeps (F22)`,
    )
  }
  const chat = links(CHAT_LABEL)
  assert.equal(chat.length, 1, `the campaign page carries ${chat.length} "${CHAT_LABEL}" links: the spec's third call to action is the WhatsApp chat, and the pre-fill message names the page the visitor came from`)
  assert.equal(
    chat[0].attrs.get('href'),
    LANDING.variants.find((variant) => variant.path === '/save').whatsappURL,
    `the chat link is ${JSON.stringify(chat[0].attrs.get('href'))}: it is the wa.me link the loader derives from the number and the message landing.yaml writes, and no campaign parameter goes into it (F22)`,
  )
})
