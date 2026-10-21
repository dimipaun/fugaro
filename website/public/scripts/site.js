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
 *   - **The menu: Escape and a click outside close an open submenu (part P3), and on a device
 *     whose pointer hovers a submenu opens when the pointer arrives at its group (the owner's
 *     brief of 2026-10-18, item 1).** The menu's two levels are `<details>` and open and close
 *     by themselves (and the groups share a `name`, so only one is open at a time). What a
 *     `<details>` does not do is close when the reader presses Escape or clicks somewhere
 *     else, or open when a pointer merely arrives at it, and those two are what this file
 *     adds: with no script the menu still opens, still closes and still reaches every link.
 *     The hover behaviour is bound only where `matchMedia` says the device's pointer hovers,
 *     and only for a pointer that says it is a mouse or a pen — a tap sends a pointer of its
 *     own, and a finger never hovers — so a touch screen keeps the tap it has always had. The
 *     answer is watched rather than sampled once, so a pointer plugged in or taken away after
 *     the page loaded is answered too. A group also opens when the keyboard's focus arrives at
 *     its trigger, which is what lets Tab reach the links inside a closed group — and never when
 *     a click or a tap brings that focus, because the click that brought it toggles the group
 *     itself and would close what the focus had opened. A close this file does not ask for is
 *     answered too: opening a group closes its siblings in the `name` set, and the focus the
 *     browser drops with them is handed back to the trigger. A close the *reader* asks for from
 *     somewhere else is not answered that way: a press outside the menu forgets where the focus
 *     was, because a click on the page's own text moves it to the body without a `focusin` to
 *     hear, and the focus a reader has just dismissed the menu from stays where they put it.
 *   - **The console's screenshot slider: the count, the current dot and the autoplay (the
 *     owner's feedback of 2026-10-21).** The slider itself is the browser's — a snapping
 *     scroll container whose dots are anchors, all of it in the markup and none of it here.
 *     What this file adds is what a stylesheet cannot know: the count (`n / N`) and the mark on
 *     the dot the reader is on, both read from the track's own scroll position, the two arrows
 *     the markup emits `hidden`, and an advance every six seconds that stops while somebody is
 *     reading. With no script the bar is never drawn, the four screenshots are the page, and
 *     the four dots are four links.
 *   - **The consent banner, and the analytics gate behind it (W1.12, W3.2; D93, D106).** The
 *     banner's two buttons write one decision to `localStorage`; this file reveals the banner
 *     when no decision is stored, and — only when that decision is `granted` — loads Google's
 *     analytics script. Nothing is requested from Google before that, and a refusal or a
 *     dismissal loads nothing at all. A withdrawal does more than stop the sends (the owner's
 *     ruling of 2026-10-18): it deletes the cookies the tag set while the visitor had
 *     accepted, from this host and from the parent domain the tag may have used, and touches
 *     no other cookie. Nothing is deleted, and no cookie is even read, before a grant: a
 *     visitor who never consented has nothing this site put in their browser.
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
 * the consent decision, under the key the banner's markup names, and it sets no cookie: the
 * one thing it does with cookies is delete the ones the accepted tag left, which it does only
 * after a withdrawal.
 */
;(function () {
  'use strict'

  // How long the confirmation stays before it clears itself. A state that never clears is
  // one a reader cannot tell from a fresh one.
  var CLEARS_AFTER_MS = 4000

  // The menu, and everything that is open in it (P3).
  var OPEN_MENUS = '.site-nav details[open]'

  // The menu's submenus: one `<details>` per group, whose `<summary>` is the control that
  // announces the group's state. There are three of them and no other menu control at any
  // width — the labels are the menu on a phone as much as on a wide viewport (the owner's
  // brief of 2026-10-19, item 1) — and each one opens and closes by its own click.
  var GROUP_DETAILS = '.site-nav-group details'
  var SUMMARY = 'summary'

  // A device whose pointer hovers opens a group's submenu when the pointer arrives at the
  // group and closes it a moment after the pointer leaves (the owner's brief of 2026-10-18,
  // item 1). The test is the pointer's capability and never the viewport's width: a tablet
  // 1280 px wide has no hover to give, and a phone with a mouse attached has one. It is the
  // query `base.css` scopes the menu's hover states to, and `test/nav-hover.mjs` holds the two
  // strings to each other.
  var HOVER_QUERY = '(hover: hover) and (pointer: fine)'

  // How long a submenu stays open after the pointer leaves its group. A menu that closes the
  // instant the pointer leaves the trigger is a menu that cannot be entered: the cursor
  // crosses the seam between the summary and the list, and it clips the corner of a
  // neighbouring group on the way. Long enough to cross, short enough that a menu the reader
  // has walked away from does not hang over the page.
  var CLOSE_AFTER_MS = 200

  // Whether this browser has a clipboard to write to at all. A control that cannot do the
  // one thing it offers is worse than no control, so a browser without one keeps the
  // markup's `hidden` exactly as it is.
  function canCopy() {
    return !!(navigator.clipboard && typeof navigator.clipboard.writeText === 'function')
  }

  // The text a block offers: the command inside it, which every block the site prints holds in
  // a `<pre><code>` or in a `code.usage` (website/test/copy.mjs holds each page to that shape).
  function sourceOf(block) {
    return block.querySelector('pre, code')
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

  // Whether a menu holds the element. A group's list is the `<details>`'s own child (see the
  // layout's note), so "inside the group" is what the element's own ancestry says.
  function holds(details, element) {
    return !!element && details.contains(element)
  }

  // The focus this file has just given back, and the reason the group it lands on does not
  // reopen. Both closes below hand the focus to the trigger they closed, and a trigger that
  // reopened what was just closed under it would be a menu no keyboard could dismiss. It is
  // cleared by the next focus that arrives anywhere else: the group's own listener reads it on
  // the way up, for the handback it was written for, and the document's own clears it for every
  // other arrival — so the guard lasts exactly the focus it was written for, and a reader who
  // takes the focus out of the menu and comes back to the same trigger is arriving at it rather
  // than being handed it back.
  var restored = null

  // Where the focus is, remembered as the `focusin` that brought it says. A group can be closed
  // by the browser rather than by this file — its sibling in the same `name` set opened, which on
  // a pointer's arrival happens with no key pressed and no focus moved by the reader — and the
  // browser stops drawing the list the focus is standing in and drops that focus to `<body>`
  // before the `toggle` this file hears about the close. So the element is remembered as it
  // arrives: by the time the group is closed, `document.activeElement` is no longer it.
  var focused = null

  // Whether the focus arriving next is the pointer's rather than the keyboard's. A click or a tap
  // moves the focus to the `<summary>` on the way to the click, and a group opened by that focus
  // would be closed again by the very click that brought it: the first tap on a group would do
  // nothing, and a click on a closed group whose trigger did not already hold the focus would do
  // nothing either. So the pointer's focus opens nothing — the click does, which is the control's
  // own behaviour — and the keyboard's focus opens the group, which is what lets Tab reach the
  // links inside a closed one. A press and a key press are the two things that move the focus,
  // and each answers the question the other way.
  var pointerFocus = false

  // Close every open menu. A menu closed under the reader's focus would leave that focus on
  // an element that is no longer rendered, so the control that opened it takes the focus
  // back — which is where a keyboard reader expects to be after dismissing a menu. The open
  // menus are read in document order and the first one that holds the focus and is still
  // drawn takes it; the three groups are siblings and no level sits inside another, so there
  // is only ever one open at a time and the search is a formality rather than a choice.
  function closeMenus() {
    var active = document.activeElement
    var open = document.querySelectorAll(OPEN_MENUS)
    var back = null
    for (var i = 0; i < open.length; i++) {
      var summary = open[i].querySelector('summary')
      if (!back && summary && holds(open[i], active) && summary.getClientRects().length > 0) back = summary
    }
    for (var j = 0; j < open.length; j++) open[j].open = false
    if (back && back !== active) {
      restored = back
      back.focus()
    }
  }

  // What a `<summary>` announces, kept in step with the `open` attribute by the one event a
  // browser fires when that attribute changes: a click, Enter or Space, a sibling in the
  // group's exclusive set, or this file's own hover below. A `<summary>` already exposes its
  // parent's state by itself — that native mapping is what makes the menu work with no script
  // at all — and the attribute written here is the same state said in the place a reader of
  // the DOM looks for it. The event does not bubble, so it is caught on the way down instead:
  // one listener, for every group, in every mode.
  //
  // A close this file did not ask for arrives here too, and only here: opening a group closes
  // its siblings in the `name` set, which on a pointer's arrival is a close under a keyboard
  // reader's focus with no key pressed and no focus moved by anyone. The browser has already
  // stopped drawing the list that focus was in and dropped it, so `focused` above is the only
  // record of where it was, and the trigger takes it back exactly as the two closes below do.
  //
  // Nothing writes the attribute before a toggle happens, which is the point: with no script
  // there is no attribute to override the mapping with, and no state to go stale.
  function expanded() {
    document.addEventListener(
      'toggle',
      function (event) {
        var group = event.target
        var parent = group && group.parentElement
        if (!group || !parent || !parent.classList || !parent.classList.contains('site-nav-group')) return
        var summary = group.querySelector(SUMMARY)
        if (summary) summary.setAttribute('aria-expanded', group.open ? 'true' : 'false')
        if (!group.open && summary && focused && focused !== summary && holds(group, focused)) {
          restored = summary
          summary.focus()
        }
      },
      true,
    )
  }

  // The keyboard's way into a group (the owner's brief of 2026-10-18, item 1), bound whatever
  // the device answers — beside `expanded` above rather than inside the hover behaviour below,
  // which is the pointer's own. A reader who tabs to a trigger has to be able to reach the links
  // inside a closed `<details>` — Tab from the trigger then goes into the list rather than past
  // it — and that reader is the device's keyboard rather than its pointer: a phone or a tablet
  // with a keyboard attached answers the hover query "no", and a menu whose second level only a
  // hovering pointer can open is a menu that keyboard cannot enter at all.
  //
  // What opens the group is the keyboard's focus and never the pointer's (see `pointerFocus`
  // above), and never the focus a close has just handed back to the trigger, which is where the
  // reader who dismissed a menu is left rather than somewhere they asked to be.
  function keyboardGroups() {
    var groups = document.querySelectorAll(GROUP_DETAILS)
    for (var i = 0; i < groups.length; i++) {
      ;(function (group) {
        var summary = group.querySelector(SUMMARY)
        group.addEventListener('focusin', function (event) {
          var fromClosing = restored
          restored = null
          if (!summary || event.target !== summary || fromClosing === summary) return
          // The pointer's focus opens nothing: the click that brought it toggles the group
          // itself, so a group opened here would be closed again by that click — a first tap
          // that does nothing, and a click on a closed group that does nothing either. The
          // keyboard's focus opens it, which is what lets Tab reach the links inside.
          if (pointerFocus) return
          group.open = true
        })
      })(groups[i])
    }
  }

  // Whether an event came from a pointer that hovers. The device's answer to `HOVER_QUERY` is
  // about its *primary* pointer, and a device can answer yes and still have a finger on it: a
  // touch laptop has a trackpad and a screen, and a tablet with one has both too. What the
  // event itself says is finer than that — a mouse and a pen hover, and a finger never does —
  // and it is the event that decides here, because a tap's own pointer arrives at the group
  // before the tap's click does: taken for a hover, it would open the submenu and the click
  // that follows would close it again, and a finger could not open a submenu at all. A pointer
  // that names no type is not a yes either, on the same principle as a browser with no
  // `matchMedia` to ask: no answer is no.
  function hoveringPointer(event) {
    var type = event && event.pointerType
    return type === 'mouse' || type === 'pen'
  }

  // The hover behaviour as it is bound right now, and the way to take it off again. The device's
  // answer is not fixed for the life of the page — see `decideHover` — so what is bound has to
  // be remembered to be unbound.
  var unbindHover = null

  // Bind the hover behaviour where the device answers that its pointer hovers, and take it off
  // where the answer changes (the owner's brief of 2026-10-18, item 1). A tablet docked to a
  // keyboard gains a trackpad, a mouse is plugged into a phone, a laptop's pointer is taken
  // away: none of them reloads the page, and a query asked once at load would leave the reader
  // who arrived after it with the wrong menu either way. So this runs on the answer as it is
  // now and on every `change` to it, and the click is untouched in both directions.
  function decideHover(query) {
    if (query && query.matches) {
      if (!unbindHover) unbindHover = hoverGroups()
    } else if (unbindHover) {
      unbindHover()
      unbindHover = null
    }
  }

  // The pointer's way in and out of a group (the owner's brief of 2026-10-18, item 1), bound
  // on the group's `<details>` because that element holds both the trigger and the list: a
  // pointer travelling from the summary into the list never leaves it, so the submenu cannot
  // close under a reader who is on their way to a link.
  //
  // The events are the pointer's own rather than the mouse's, because a finger's tap is
  // delivered as a mouse's too — the compatibility events every browser sends after a tap —
  // and `hoveringPointer` above is what refuses it.
  //
  // A group opens when the pointer arrives and closes `CLOSE_AFTER_MS` after the pointer leaves.
  // The keyboard's way into a group is `keyboardGroups` above, which is bound whether or not the
  // device has a pointer to hover with; what is bound here is the pointer's half alone. What this
  // file does not touch is the click: a `<summary>` toggles its own group, so a click on a group
  // the pointer has already opened closes it, which is what the control did before any of this
  // and what a reader pressing it is asking for.
  //
  // A group has two readers, and the pointer is only one of them. The keyboard's focus is the
  // other, and it outranks the pointer: a pointer that leaves a group somebody is standing in
  // has asked for a close that is not owed yet, and it lands when the focus goes rather than
  // under the reader. That is the same reader the delay is for — the one who comes back — and
  // coming back by keyboard is coming back.
  //
  // It returns the way back out: every listener it bound, taken off again. A close that is
  // already scheduled is left to run, because it was scheduled by a pointer that had left the
  // group and closing it is what the reader asked for by leaving; taking the listeners off is
  // about the pointers that have not moved yet.
  function hoverGroups() {
    var groups = document.querySelectorAll(GROUP_DETAILS)
    var bound = []
    // One re-reading of each group, for the listener below that hears the reader's focus move
    // anywhere on the page. What a group answers is whether a reader is inside it, and that
    // changes when the focus arrives in the group *and* when it leaves — and the leaving fires
    // no event on the group itself. So the listener is the document's and every group re-reads
    // its own state from wherever the focus is now.
    var reread = []

    function on(element, type, handler) {
      element.addEventListener(type, handler)
      bound.push([element, type, handler])
    }

    // Where the reader's focus has gone: a group it has arrived in is one the pointer's close
    // cannot take away, and one it has left owes the close the pointer asked for.
    function focusMoved() {
      for (var i = 0; i < reread.length; i++) reread[i]()
    }
    on(document, 'focusin', focusMoved)

    for (var i = 0; i < groups.length; i++) {
      ;(function (group) {
        var summary = group.querySelector(SUMMARY)
        var closesAt = null
        // The pointer has left and asked for a close. That alone does not say the close is owed:
        // it is owed when the pointer has gone and nobody is left reading.
        var pointerLeft = false

        function stopClosing() {
          if (closesAt !== null) window.clearTimeout(closesAt)
          closesAt = null
        }

        // Whether a reader is standing in the group, read when it is asked rather than
        // remembered: a focus can leave by way of something that cannot take it — a paragraph of
        // the page's own text — and that fires no `focusin` anywhere, so a memory of the reader
        // would outlive the reader and hold the group open for good.
        //
        // The one focus that is not a reader is the pointer's own on the trigger: a tap or a
        // click leaves the focus there, and the group that press opened is the pointer's to close
        // when the pointer goes. The keyboard's focus on the trigger is a reader — that is where
        // a Tab to a group leaves it — and so is a focus anywhere inside the list.
        function readerIsInside() {
          var active = document.activeElement
          if (!holds(group, active)) return false
          return active !== summary || !pointerFocus
        }

        // A pointer that leaves a group the keyboard is standing in has asked for a close that is
        // not owed yet: it is owed when the focus goes, and the focus going is what lands it. A
        // pointer that leaves an empty group is owed it at once.
        function closeWhenNobodyIsReading() {
          if (pointerLeft && !readerIsInside()) closesAt = window.setTimeout(close, CLOSE_AFTER_MS)
        }

        reread.push(function () {
          // A reader arriving inside the delay the pointer's close waits is the reader the delay
          // is for: the close is cancelled, and the focus holds the group open in its place.
          stopClosing()
          closeWhenNobodyIsReading()
        })

        // This close never lands under a focus inside the group — the focus holds the group open
        // until it goes — so there is no focus here to hand back to the trigger. The closes that
        // can strand one are the reader's own and the browser's, and those pay it above.
        function close() {
          closesAt = null
          pointerLeft = false
          group.open = false
        }

        on(group, 'pointerenter', function (event) {
          if (!hoveringPointer(event)) return
          pointerLeft = false
          stopClosing()
          group.open = true
        })

        on(group, 'pointerleave', function (event) {
          if (!hoveringPointer(event)) return
          pointerLeft = true
          stopClosing()
          closeWhenNobodyIsReading()
        })
      })(groups[i])
    }

    return function release() {
      for (var j = 0; j < bound.length; j++) bound[j][0].removeEventListener(bound[j][1], bound[j][2])
    }
  }

  function menu() {
    document.addEventListener('keydown', function (event) {
      // A key press makes the focus that follows the keyboard's own: this is what a Tab to a
      // group's trigger is, and the `focusin` above opens the group for it.
      pointerFocus = false
      if (event.key === 'Escape') closeMenus()
    })
    document.addEventListener('pointerdown', function (event) {
      // A pointer's press moves the focus to what it pressed, on the way to the click that
      // press is making. That focus is not the keyboard's, and the `focusin` above refuses it.
      pointerFocus = true
      // And a press outside the group the remembered focus is in is the reader leaving it. The
      // focus goes to what was pressed, or — where that can take no focus at all, a paragraph of
      // the page's own text — to the body, and that last one fires no `focusin` anywhere: the
      // element remembered above would stay the link the reader has walked away from, and a close
      // their press asks for would hand the focus back to a trigger they were leaving. So it is
      // forgotten here, and a close that follows has nothing to give back.
      var pressed = event.target && event.target.closest ? event.target.closest(GROUP_DETAILS) : null
      if (focused && !(pressed && holds(pressed, focused))) focused = null
    })
    document.addEventListener(
      'focusin',
      function (event) {
        // Where the focus is, for the `toggle` in `expanded` above: that listener hears a close
        // which has already dropped the focus, so the element has to be remembered as it arrives.
        focused = event.target
      },
      true,
    )
    document.addEventListener('focusin', function (event) {
      // The guard against reopening is written for the one focus a close hands back to the
      // trigger, and it lasts exactly that long: a focus arriving anywhere else — the page's own
      // text, a link in the footer — is the reader having moved on, and the trigger they come
      // back to afterwards is a trigger they have arrived at, which opens as it did the first
      // time. Read on the way up, after the group's own listener has had the focus the guard was
      // written for, and never for that focus itself.
      if (restored && event.target !== restored) restored = null
    })
    document.addEventListener('click', function (event) {
      var target = event.target
      if (target && target.closest && target.closest('.site-nav')) return
      closeMenus()
    })
    expanded()
    keyboardGroups()

    // The device's answer to the pointer's question, asked once and then watched. A browser
    // with no `matchMedia` to ask cannot answer, and a menu whose hover behaviour is guessed at
    // is worse than one that keeps the click: no answer means no hover behaviour. `addListener`
    // is the same subscription under the name browsers had for it before `addEventListener`
    // reached a `MediaQueryList`, and the object a `change` reports carries the new answer in
    // its own `matches` — which is the one `decideHover` reads, at load and after every change.
    var query = window.matchMedia ? window.matchMedia(HOVER_QUERY) : null
    if (query) {
      if (query.addEventListener) query.addEventListener('change', decideHover)
      else if (query.addListener) query.addListener(decideHover)
    }
    decideHover(query)
  }

  // ---- the console's screenshot slider (the owner's feedback of 2026-10-21) --------------
  //
  // The console page's slider is a scroll container with snap points, and it works with none
  // of this: the track scrolls, the dots are anchors into their slides and the first picture is
  // in the page, so a reader who runs no script still reaches every screenshot (D183, and
  // `website/test/console-screens.mjs` is the gate for it). Three things are this file's, and
  // they are the three the owner asked for on 2026-10-21:
  //
  //   - the control bar the markup emits `hidden` — a Previous button, the count and a Next
  //     button — which is revealed here;
  //   - the count (`n / N`) and the mark on the dot the reader is on, both read from where the
  //     track actually is, so a reader who swipes with a trackpad sees the same reading as one
  //     who presses Next;
  //   - and an advance every `AUTO_MS` that stops while somebody is reading: the pointer over
  //     the slider, the focus inside it, the tab in the background, or a system that asks for
  //     less motion at all.
  //
  // Nothing here is load-bearing, and the last line of this comment is the reason it is safe to
  // write any of it: with no script the pictures are all in the document, in order, with their
  // captions, and the four dots are four links.

  var SLIDER = '[data-slider]'
  var SLIDER_BAR = '[data-slider-bar]'
  var SLIDER_TRACK = '.screens-track'
  var SLIDER_SLIDE = '.screen'
  var SLIDER_DOT = '.screen-dot'
  var SLIDER_PREV = '[data-slider-prev]'
  var SLIDER_NEXT = '[data-slider-next]'
  var SLIDER_COUNT = '[data-slider-counter]'

  // How long a slide stands before the next one. Six seconds: long enough to look at a
  // screenshot the size of a page, short enough that a reader who is watching can see that the
  // slider is a slider (the owner's "~6s").
  var AUTO_MS = 6000

  // The query a visitor's system answers when it has asked for less motion. `base.css` is where
  // the site honours that for the stylesheet's own motion (D105); a carousel is motion no
  // stylesheet can switch off, so it is honoured here — and watched after load, the way the
  // menu watches its own query, because a visitor can turn the setting on without reloading.
  var LESS_MOTION = '(prefers-reduced-motion: reduce)'

  function slider(section) {
    var track = section.querySelector(SLIDER_TRACK)
    if (!track) return
    var slides = track.querySelectorAll(SLIDER_SLIDE)
    // One slide is nowhere to go, and the markup renders no bar for it either: this is that
    // same rule read from the other end, so a slider with one screenshot cannot end up with a
    // Next button that goes to the slide it is already on.
    if (slides.length < 2) return

    var bar = section.querySelector(SLIDER_BAR)
    var count = section.querySelector(SLIDER_COUNT)
    var dots = section.querySelectorAll(SLIDER_DOT)
    var prev = section.querySelector(SLIDER_PREV)
    var next = section.querySelector(SLIDER_NEXT)
    var index = 0
    var timer = null
    var over = false
    var inside = false

    // Where the track is, as the slide it is showing. The slides are one track-width each
    // (`grid-auto-columns: 100%`), so the slide at the left edge is the scroll offset over that
    // width, rounded to the nearest — which is what a swipe that stopped between two slides
    // (there is no such stop: the track snaps) comes out as.
    function shown() {
      var width = track.clientWidth || 0
      if (!width) return index
      var at = Math.round((track.scrollLeft || 0) / width)
      return at < 0 ? 0 : at > slides.length - 1 ? slides.length - 1 : at
    }

    // Draw the reading: the count, and the mark on the dot the reader is on. `aria-current` is
    // written on every dot rather than taken off the others — "false" is a value the attribute
    // has — so there is no second state (an attribute that is there or not) to keep in step,
    // and a whitespace-only difference never reaches the DOM.
    function paint() {
      if (count) count.textContent = index + 1 + ' / ' + slides.length
      for (var i = 0; i < dots.length; i++) {
        var mark = i === index ? 'true' : 'false'
        if (dots[i].getAttribute('aria-current') !== mark) dots[i].setAttribute('aria-current', mark)
      }
    }

    // Go to a slide, wrapping at either end: Next on the last slide is the first one rather
    // than a control that does nothing, which is what "auto-slide" asks for and what makes the
    // two arrows the whole of the navigation.
    //
    // The move lands where it was asked to land — no animation — and that is deliberate: the
    // count and the dots are the track's own position, so a move the browser animates would be
    // a move during which the position is between two slides while the reading had already
    // moved on, and a jump across several slides would narrate every one it passed. The
    // reader's own motion is the stylesheet's (`scroll-behavior: smooth` on the track, which
    // the base layout's reduced-motion rule neutralises) and so is a dot's anchor with no
    // script.
    function show(to) {
      index = (to + slides.length) % slides.length
      var left = index * (track.clientWidth || 0)
      if (typeof track.scrollTo === 'function') track.scrollTo({ left: left, behavior: 'auto' })
      else track.scrollLeft = left
      paint()
    }

    function forward() {
      show(index + 1)
    }

    // Whether the slider may move on its own right now: nobody is reading it (no pointer over
    // it, no focus in it), the tab is not in the background, and the system has not asked for
    // less motion.
    var query = window.matchMedia ? window.matchMedia(LESS_MOTION) : null
    function mayAdvance() {
      return !over && !inside && !document.hidden && !(query && query.matches)
    }

    function play() {
      if (timer !== null || !window.setInterval || !mayAdvance()) return
      timer = window.setInterval(forward, AUTO_MS)
    }

    function pause() {
      if (timer === null) return
      window.clearInterval(timer)
      timer = null
    }

    /** What the two states above come to after something changed: running, or stopped. */
    function settle() {
      if (mayAdvance()) play()
      else pause()
    }

    if (prev) prev.addEventListener('click', function () { show(index - 1) })
    if (next) next.addEventListener('click', function () { show(index + 1) })

    for (var d = 0; d < dots.length; d++) {
      ;(function (dot, at) {
        dot.addEventListener('click', function (event) {
          // A dot is an anchor into its own slide, which is what a reader with no script gets.
          // With a script the press is the same move a Next press is, so that the count and the
          // mark follow it exactly rather than following a jump that is still travelling.
          if (event && event.preventDefault) event.preventDefault()
          show(at)
        })
      })(dots[d], d)
    }

    // The track is the source of truth, so a reader's own swipe moves the reading too.
    track.addEventListener('scroll', function () {
      var at = shown()
      if (at !== index) {
        index = at
        paint()
      }
    })

    // The pointer's arrival and the focus's hold the slider still, and the two are answered
    // separately because they are different readers: a keyboard reader standing on a dot has no
    // pointer over anything, and a pointer resting on a screenshot has moved no focus. The
    // focus is read from the document rather than from this section's own `focusout`, which
    // fires with the focus already on its way out — and the arriving `focusin` is what says
    // where it went.
    section.addEventListener('pointerenter', function () { over = true; settle() })
    section.addEventListener('pointerleave', function () { over = false; settle() })
    document.addEventListener('focusin', function (event) {
      inside = section.contains(event.target)
      settle()
    })
    document.addEventListener('visibilitychange', settle)

    if (query) {
      if (query.addEventListener) query.addEventListener('change', settle)
      else if (query.addListener) query.addListener(settle)
    }

    // The bar the markup emitted `hidden`, revealed now that something drives it — and the
    // reading drawn before the first frame, so the count is `1 / N` from the start rather than
    // empty until the first move.
    if (bar) bar.hidden = false
    paint()
    settle()
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
      if (!loaded) return
      gtag('consent', 'update', { analytics_storage: DENIED })
      forget()
    }

    // ---- the cookies an accepted visitor leaves, and the withdrawal that removes them ----
    //
    // Stopping the sends is only half of a withdrawal (the owner's ruling of 2026-10-18): the
    // identifiers the tag wrote while the visitor had accepted are still in their browser, and
    // a withdrawal that left them there would keep identifying a visitor who has said no. So a
    // withdrawal deletes every cookie the tag sets for this site, from the one place D183 puts
    // the site's script in.
    //
    // It runs only where `loaded` is true, and that is the whole of the "no work before
    // consent" rule: a visitor who never accepted has nothing this site put there, so a
    // refusal on a first visit reads no cookie and writes none either.

    // An expiry in the past is what removes a cookie; an empty value alone would only
    // overwrite one with nothing.
    var EXPIRED = 'expires=Thu, 01 Jan 1970 00:00:00 GMT'

    // The names the tag writes for this property: the client id and its per-container variant,
    // the session id, and the throttling cookie. The container name is DERIVED from the one
    // measurement id the banner carries — `_ga_` plus the id without its `G-` prefix — rather
    // than written here, so a property change moves the name with it and the two cannot drift.
    function cookieNames() {
      return ['_ga', '_ga_' + String(id).replace(/^G-/, ''), '_gid', '_gat']
    }

    // The names this browser is holding, read out of `document.cookie`. Reading it is the only
    // way to find the throttling cookies, whose exact name (`_gat_gtag_` and the id) is not
    // derivable, and it is why the deletion can leave a browser that holds nothing untouched.
    function heldNames() {
      var names = []
      try {
        var parts = String(document.cookie || '').split(';')
        for (var i = 0; i < parts.length; i++) {
          var name = parts[i].split('=')[0].replace(/^\s+|\s+$/g, '')
          if (name) names.push(name)
        }
      } catch (err) {
        // A browser that refuses to read them holds none this can see, which is the same
        // answer as an empty jar rather than a reason to stop.
      }
      return names
    }

    // The parent domain the tag may have written a cookie to instead of the host: the host
    // without its leftmost label — the registrable domain of a `www.` host, which is the case
    // the tag's own `auto` setting picks — and only where there is a label to spare. An apex
    // host has none, and nothing below the parent is tried: no wildcard, no suffix guessed
    // from a list this file does not carry.
    //
    // One label means one label, and the result is not always a domain a cookie may be set
    // on: a preview host like `foo--bar.web.app` has `web.app` for a parent, and that is a
    // public suffix. The write is then refused by the browser (this file consults no
    // public-suffix list, and a list is not worth the bytes here), so the attempt costs a
    // rejected assignment and nothing else — and the host this site ships on is an apex, with
    // no parent at all, so the attempt is never made there.
    function parentDomain(host) {
      var labels = String(host || '').split('.')
      return labels.length > 2 ? labels.slice(1).join('.') : ''
    }

    // Delete one cookie: on this host first, and then — only while it is still there — on the
    // parent domain the tag may have written it to. Both attempts carry `Path=/`, the path the
    // tag's cookies are written at, which is what makes the expiry match the cookie rather
    // than leave it in place.
    function drop(name, host) {
      var parent = parentDomain(host)
      var scopes = ['']
      if (parent) scopes.push('; domain=' + parent)
      for (var i = 0; i < scopes.length; i++) {
        document.cookie = name + '=; path=/' + scopes[i] + '; ' + EXPIRED
        if (heldNames().indexOf(name) === -1) return
      }
    }

    // Forget what the tag stored, and nothing else: the cookies this property wrote, plus the
    // throttling ones the browser is actually holding — `_gat` and the names under it, which
    // is as far as the match goes: `_gate` or `_gather` are somebody else's cookie and are
    // left alone. Anything the page did not find in the jar is not written to at all.
    function forget() {
      var held = heldNames()
      var names = cookieNames()
      for (var i = 0; i < held.length; i++) {
        var throttle = held[i] === '_gat' || held[i].indexOf('_gat_') === 0
        if (throttle && names.indexOf(held[i]) === -1) names.push(held[i])
      }
      var host = (window.location && window.location.hostname) || ''
      for (var j = 0; j < names.length; j++) {
        if (heldNames().indexOf(names[j]) !== -1) drop(names[j], host)
      }
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
    // The sliders first, and unconditionally: the slider's control is not a convenience the
    // page can do without — the arrows and the count are the owner's feedback of 2026-10-21 —
    // so it is not behind the clipboard gate the copy controls stand behind below.
    var sliders = document.querySelectorAll(SLIDER)
    for (var s = 0; s < sliders.length; s++) slider(sliders[s])
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
