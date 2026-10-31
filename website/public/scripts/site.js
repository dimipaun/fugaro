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
 *   - **The hero's live card (the owner's ruling of 2026-10-23, the redesign's Step 3).** The
 *     card in the homepage's hero is the static picture of one request with no script; this
 *     reveals a text field in it and redraws the jobs and pull requests for whatever task the
 *     reader types. It sends nothing and stores nothing. The homepage's three-panel slider it
 *     replaced is retired with the slider.
 *   - **The entrances (the same ruling, the motion layer).** The parts of each home section that
 *     are below the first screen rise into place once as they enter; with no script, or for a
 *     reader who asks for less motion, nothing is held back.
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
 * menu is a `<details>` at both levels and needs none of this to work; the slider's three
 * slides are in the markup and the dots that open them are links, so the block works with no
 * script and only its clock is lost; and with no script the banner is not shown at all, so a
 * reader who runs none is measured by nothing.
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

  // How long the copy button shows its checkmark before it is a clipboard again.
  var COPIED_MS = 1600

  // A short line that changes its words (the copy confirmation, the hero card's stages)
  // crossfades instead of snapping (owner-approved motion opportunities 2026-10-23): the old
  // words fade out and the new ones in, 120 ms in all (`--duration-fast`), on the site's
  // `--ease-out`. Opacity only, through the element's own `animate()`, so a browser without it
  // and a reader who asked for less motion get the plain swap.
  var SWAP_MS = 120
  var SWAP_EASE = 'cubic-bezier(0.22, 1, 0.36, 1)'

  function swaps(el) {
    if (typeof el.animate !== 'function' || !window.matchMedia) return false
    return !window.matchMedia('(prefers-reduced-motion: reduce)').matches
  }

  // `then` runs at the moment the words change. A newer swap on the same element wins: an older
  // fade that finishes after it does not write its words over the newer ones.
  function swapText(el, text, then) {
    var turn = (el.fugaroSwap || 0) + 1
    el.fugaroSwap = turn
    function apply() {
      el.textContent = text
      if (then) then()
    }
    if (!swaps(el) || el.textContent === text) {
      apply()
      return
    }
    // From nothing, the new words only fade in; otherwise half the time out, half in.
    if (!el.textContent) {
      apply()
      el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: SWAP_MS, easing: SWAP_EASE })
      return
    }
    var out = el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: SWAP_MS / 2, easing: SWAP_EASE, fill: 'forwards' })
    out.onfinish = function () {
      if (el.fugaroSwap === turn) {
        apply()
        if (text) el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: SWAP_MS / 2, easing: SWAP_EASE })
      }
      out.cancel()
    }
  }

  // The menu, and everything that is open in it (P3).
  var OPEN_MENUS = '.site-nav details[open]'

  // The menu's submenus: one `<details>` per group, whose `<summary>` is the control that
  // announces the group's state. There are two of them and no other menu control at any
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
  function say(block, message, tone) {
    var status = block.querySelector('[data-copy-status]')
    if (!status) return
    // `ok` is said to a screen reader only (the checkmark is the sighted reader's); anything
    // else, a refused copy, is shown as well.
    status.setAttribute('data-tone', tone || 'error')
    swapText(status, message)
    window.setTimeout(function () {
      if (status.textContent === message) swapText(status, '')
    }, CLEARS_AFTER_MS)
  }

  function copyFrom(block) {
    var source = sourceOf(block)
    if (!source) return
    navigator.clipboard.writeText(source.textContent || '').then(
      function () {
        say(block, 'Copied', 'ok')
        // The clipboard icon becomes a checkmark for a moment; the status line is what a screen
        // reader is told. A newer copy restarts the moment rather than being cut short by an
        // older timer.
        var button = block.querySelector('[data-copy-button]')
        if (!button) return
        var turn = (button.fugaroCopied || 0) + 1
        button.fugaroCopied = turn
        button.setAttribute('data-copied', 'true')
        window.setTimeout(function () {
          if (button.fugaroCopied === turn) button.setAttribute('data-copied', 'false')
        }, COPIED_MS)
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
  // drawn takes it; the groups are siblings and no level sits inside another, so there
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

  // The four names **both** of the site's sliders are found and driven by, declared once here
  // because the two blocks are the same two blocks to whoever reads the markup: `[data-slider]`
  // is the one attribute a page marks a slider with, the two step-control attributes are the two
  // controls a slider's markup emits `hidden`, and the six seconds and the motion query are what
  // the owner asked of both. What the two do with them is not shared, and the two sections say
  // why. (The homepage's three-panel slider that shared them is retired with the redesign's
  // Step 3, 2026-10-23; the console's screenshot slider is now the site's one slider.)
  var SLIDER = '[data-slider]'
  var SLIDER_PREV = '[data-slider-prev]'
  var SLIDER_NEXT = '[data-slider-next]'

  // How long a slide stands before the next one. Six seconds: long enough to look at a
  // screenshot the size of a page, short enough that a reader who is watching can see that the
  // slider is a slider (the owner's "~6s").
  var AUTO_MS = 6000

  // The query a visitor's system answers when it has asked for less motion. `base.css` is where
  // the site honours that for the stylesheet's own motion (D105); a carousel is motion no
  // stylesheet can switch off, so it is honoured here — and watched after load, the way the
  // menu watches its own query, because a visitor can turn the setting on without reloading.
  var LESS_MOTION = '(prefers-reduced-motion: reduce)'

  // The console slider's own three, and the two the bar is drawn from: the track it scrolls,
  // the slides it counts, the dots it marks, the bar itself and the count in it.
  //
  // **The interior is `data-slider-*` too, and that is the correction of 2026-10-21** (the
  // homepage's "See it" panel became a slider that renders the console page's own rows, D246).
  // These three were `.screens-track`, `.screen` and `.screen-dot` — the console page's class
  // names — and a second page can only reuse this driver by borrowing the classes of a page it
  // has nothing to do with. The classes stay, because they are what the stylesheet draws; what
  // the script reads is the attribute, which is the same shape as the door above it.
  var SLIDER_BAR = '[data-slider-bar]'
  var SLIDER_TRACK = '[data-slider-track]'
  var SLIDER_SLIDE = '[data-slider-slide]'
  var SLIDER_DOT = '[data-slider-dot]'
  var SLIDER_COUNT = '[data-slider-counter]'

  /**
   * The elements of `block`'s OWN slider: everything `selector` matches inside it that belongs
   * to no nearer slider. The homepage's "See it" panel holds one of these blocks inside the
   * homepage's own, so a plain `block.querySelectorAll('[data-slider-slide]')` would hand the
   * outer block the inner one's slides — and the two drivers would count, mark and move each
   * other's. `closest('[data-slider]')` is what tells them apart, and it answers the block
   * itself for everything of the block's own: the same expression a browser answers, so the
   * rule needs no bookkeeping and cannot go stale.
   */
  function own(block, selector) {
    var found = block.querySelectorAll(selector)
    var kept = []
    for (var i = 0; i < found.length; i++) {
      if (found[i].closest(SLIDER) === block) kept.push(found[i])
    }
    return kept
  }

  function slider(section) {
    var track = own(section, SLIDER_TRACK)[0]
    if (!track) return
    var slides = track.querySelectorAll(SLIDER_SLIDE)
    // One slide is nowhere to go, and the markup renders no bar for it either: this is that
    // same rule read from the other end, so a slider with one screenshot cannot end up with a
    // Next button that goes to the slide it is already on.
    if (slides.length < 2) return

    // Every hook this driver reads is read through `own()`, the single elements as much as the
    // lists: the homepage's "See it" panel holds a slider of its own inside this page's block,
    // and its bar, its count and its two arrows carry the same four names this one's do — a
    // `section.querySelector` would answer with whichever came first in the document, which is
    // this block's own today and the inner block's the day the two are written the other way
    // round. The rule is the same one the slides and the dots are read with, and it needs no
    // document order to be right.
    var bar = own(section, SLIDER_BAR)[0]
    var count = own(section, SLIDER_COUNT)[0]
    var dots = own(section, SLIDER_DOT)
    var prev = own(section, SLIDER_PREV)[0]
    var next = own(section, SLIDER_NEXT)[0]
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
    // moved on, and a jump across several slides would narrate every one it passed. **It is
    // `'instant'` and not `'auto'`**: CSSOM-View resolves `'auto'` to the box's computed
    // `scroll-behavior`, which the track declares `smooth` — so `'auto'` is the animation this
    // is written to avoid, and the count would narrate the slides a move passed through while a
    // reader pressing Next twice in a row advanced one slide, the second press having read a
    // position a `scroll` event had already put back. A browser too old to know the word reads
    // it as a value the dictionary does not have and takes the default, which is what the
    // reader's own motion gets anyway. That motion stays the stylesheet's — `scroll-behavior:
    // smooth` on the track, which the base layout's reduced-motion rule neutralises — and so is
    // a dot's anchor with no script.
    // A track the page is not laying out cannot be moved, and a move it cannot make is not one
    // to count: `clientWidth` is zero for a box inside a `display: none` subtree, and the case
    // on this site is the homepage, where this slider renders inside the "See it" panel and that
    // panel is drawn only while the reader is on it (D246). The clock keeps its beat there — six
    // seconds of a panel nobody has opened is no work — and every beat is refused here, so the
    // count and the mark on the dots stay on the slide the track is showing: the first one, which
    // is what the reader who opens the panel sees, and where their next move starts from.
    function show(to) {
      if (!(track.clientWidth || 0)) return
      index = (to + slides.length) % slides.length
      var left = index * (track.clientWidth || 0)
      if (typeof track.scrollTo === 'function') track.scrollTo({ left: left, behavior: 'instant' })
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
    // reading drawn before the first frame, so the count is written from the start rather than
    // empty until the first move.
    //
    // The first reading is the track's, like every other one: a page that arrives already
    // scrolled — a shared `#console-screen-3` deep link, or a scroll position the browser
    // restored — opens on that slide, and a count hardcoded to the first one would tell that
    // reader `1 / N` and mark the first dot until the first `scroll` event corrected it.
    // `shown()` answers `index` when the track reports no width, so a browser that lays nothing
    // out yet leaves the starting index as it was.
    if (bar) bar.hidden = false
    index = shown()
    paint()
    settle()
  }

  // ---- the consent banner, and the analytics gate behind it (W1.12, W3.2) ----------------

  // The banner's own element id, the status line inside it, and the page's content region.
  var BANNER_ID = 'consent-banner'
  var STATUS_ID = 'consent-banner-status'
  var MAIN_ID = 'main'

  // How long the bar's collapse may take before it is hidden anyway: the stylesheet's 200 ms
  // (`--duration-collapse`) with room to spare, so the timer only ever catches a missed event.
  var COLLAPSE_FLOOR_MS = 320

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
  var SITE_KEY_ATTR = 'data-consent-key'

  // The campaign's own measurement (G2.3, D401): its decision's key, the page the
  // configuration was built for, the two vendor ids and the three origins their scripts come
  // from, and the booking link the embed is built from. Every one of them is on the four
  // campaign pages' banner and on no other page, so the loaders at the end of this section are
  // inert everywhere else without a list of the campaign paths repeated here.
  var MARKETING_KEY_ATTR = 'data-marketing-consent-key'
  var MARKETING_PATH_ATTR = 'data-marketing-path'
  var META_ID_ATTR = 'data-meta-pixel-id'
  var META_ORIGIN_ATTR = 'data-meta-origin'
  var LINKEDIN_ID_ATTR = 'data-linkedin-partner-id'
  var LINKEDIN_ORIGIN_ATTR = 'data-linkedin-origin'
  var CALENDLY_URL_ATTR = 'data-calendly-url'
  var CALENDLY_ORIGIN_ATTR = 'data-calendly-origin'
  var EMBED_ID = 'calendly-embed'
  // The two calls to action a key event is about (G2.4): the ids the calls-to-action section
  // gives the repository link and the chat link. They are the page's own, so the script knows
  // the two links by the name the markup gives them rather than by where they sit.
  var INSTALL_ID = 'campaign-install'
  var CHAT_ID = 'campaign-whatsapp'

  // The decision, as the browser stored it, or null when there is none to read. Storage the
  // browser refuses is not a decision: an unreadable answer is no answer rather than a guess,
  // and the gate needs a readable `granted` before it fetches anything.
  function read(banner, attribute) {
    try {
      return window.localStorage.getItem(banner.getAttribute(attribute))
    } catch (err) {
      return null
    }
  }

  function stored(banner) {
    return read(banner, SITE_KEY_ATTR)
  }

  // Keep a decision, and say whether it was really kept. A browser in private mode may accept
  // the write and store nothing, and the visitor has to be told that rather than asked again
  // in silence.
  function keep(banner, attribute, decision) {
    var key = banner.getAttribute(attribute)
    try {
      window.localStorage.setItem(key, decision)
      return window.localStorage.getItem(key) === decision
    } catch (err) {
      return false
    }
  }

  // A choice, and the questions it answered: `null` is a question this press was not asked, and
  // the two gates below load or withdraw on nothing else — a bar that asks one of the two
  // questions must not answer the other one on the visitor's behalf.
  function announce(analytics, marketing) {
    window.dispatchEvent(new CustomEvent(CONSENT_EVENT, { detail: { analytics: analytics, marketing: marketing } }))
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

  // The same path, whichever way it was written: the banner carries the variant's path as
  // `website/data/landing.yaml` writes it (`/save`) and the browser reports what it was served
  // (`/save/`, because the site's pages are directories), so the two are the same page and the
  // comparison has to say so. It is the comparison's spelling only — the address reported to a
  // vendor keeps the browser's own, so it is the address the visitor is looking at.
  function route(path) {
    var written = bare(path)
    return written.length > 1 ? written.replace(/\/+$/, '') : written
  }

  // ---- the cookies a consented third party leaves, and the withdrawal that removes them ----
  //
  // Stopping the sends is only half of a withdrawal (the owner's ruling of 2026-10-18): the
  // identifiers a tag wrote while the visitor had accepted are still in their browser, and a
  // withdrawal that left them there would keep identifying a visitor who has said no. So a
  // withdrawal deletes the first-party cookies of the tags it loaded, from the one place D183
  // puts the site's script in — Google's for the analytics tag, and the pixel's for the
  // campaign one (G2.3).
  //
  // It runs only where something was loaded, and that is the whole of the "no work before
  // consent" rule: a visitor who never accepted has nothing this site put there, so a refusal
  // on a first visit reads no cookie and writes none either.
  //
  // An expiry in the past is what removes a cookie; an empty value alone would only overwrite
  // one with nothing.
  var EXPIRED = 'expires=Thu, 01 Jan 1970 00:00:00 GMT'

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

  // The parent domain a tag may have written a cookie to instead of the host: the host
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

  // The cookies the two tags set on this site, and nothing else: Meta's browser id and the
  // click id it writes when one of the campaign's own links was followed, and the six LinkedIn
  // documents for its insight tag — the browser id, the record of the sync, the matched
  // audience, the two it keeps its own session in, and the consent record. Both tags run as
  // scripts in this page's own origin, so every cookie either one writes is first-party here and
  // is this site's to remove; what they wrote on their own domains is theirs and is not
  // reachable from here, which the privacy page says. A name that is not in the jar costs
  // nothing: nothing is written for a cookie the browser is not holding.
  var MARKETING_COOKIES = [
    '_fbp',
    '_fbc',
    'li_sugr',
    'AnalyticsSyncHistory',
    'UserMatchHistory',
    'bcookie',
    'lidc',
    'li_gc',
  ]

  // Whether GA4 is loaded, which is the one thing the key events of the campaign pages wait
  // for (G2.4): `load()` sets it, a withdrawal clears it, and nothing else reads or writes it.
  // It is the gate's own state rather than a second reading of storage, so a visitor who has
  // just answered the banner is measured from the same press that loaded the tag.
  var measuring = false

  // And whether the pixel is, which is the same thing for the other consent (G2.3, the owner's
  // ruling of 2026-10-31): the campaign loader's `load()` sets it from the ids the banner
  // carries — an empty id leaves it false, and the pixel's queue is never created — a
  // withdrawal clears it, and only the key events read it. Two questions, two answers: the key
  // events of the campaign pages go to whichever of the two tags the visitor granted, and to
  // neither when they granted none.
  var tracking = false

  // One call into the tag's queue. The queue is created by `load()` below, and the two callers
  // are both behind a check that it exists: this is the gate's own voice and not a second one.
  function gtag() {
    window.dataLayer.push(arguments)
  }

  function analytics(banner) {
    var id = banner.getAttribute(ANALYTICS_ID_ATTR)
    var origin = banner.getAttribute(ANALYTICS_ORIGIN_ATTR)
    if (!id || !origin) return

    var loaded = false
    var OPT_OUT = 'ga-disable-' + id

    function load() {
      window[OPT_OUT] = false
      measuring = true
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
      measuring = false
      if (!loaded) return
      gtag('consent', 'update', { analytics_storage: DENIED })
      forget()
    }

    // The names the tag writes for this property: the client id and its per-container variant,
    // the session id, and the throttling cookie. The container name is DERIVED from the one
    // measurement id the banner carries — `_ga_` plus the id without its `G-` prefix — rather
    // than written here, so a property change moves the name with it and the two cannot drift.
    function cookieNames() {
      return ['_ga', '_ga_' + String(id).replace(/^G-/, ''), '_gid', '_gat']
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
      if (decision !== GRANTED && decision !== DENIED) return
      if (decision === GRANTED) load()
      else withdraw()
    })
  }

  // ---- the campaign's own measurement, behind its own consent (G2.3, D401) ----------------
  //
  // Three vendors the ads bring with them: Meta's pixel, LinkedIn's insight tag, and Calendly's
  // widget — the last one the inline booking embed, which is why the campaign's primary call to
  // action stays a plain link a visitor who declines still has. None of them is fetched until
  // the visitor grants THIS decision, which is stored under its own key so that answering the
  // site's analytics question does not answer this one, and so that a visitor who never opens a
  // campaign page is asked nothing new (F17).
  //
  // The configuration is the banner's: the two ids site.yaml carries (empty until the owner
  // supplies them, and an empty id is a loader that does not load), the three origins, the
  // booking link, and the page the configuration was built for. That last one is the path
  // check: the loader runs on the page it was built for and on no other, so a bar copied
  // anywhere else loads nothing there (F18's "those pages only", O2). (This file names no host,
  // not even in a comment: the origins reach it through the markup, and a build of a
  // non-campaign page carries none of them.)
  // The campaign parameters the page may pass on, by name (G2.4, spec §6): the five the
  // agenda's links carry. A key outside this list is dropped rather than forwarded — the page
  // adds a query to one address, and that address is the owner's, so what goes on it is a list
  // this file states rather than whatever the visitor's link happened to carry.
  var UTM_KEYS = ['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content']

  // And how long one value may be. A longer one is dropped rather than truncated: half a
  // campaign name is a campaign this page cannot name, and a link that carried one would
  // attribute a booking to a name nobody chose.
  var UTM_MAX = 100

  // The booking address, with the campaign parameters the visitor arrived through and only
  // those (F22): each value is decoded and encoded again, so a value that carries an address's
  // own characters stays one parameter, and a link with no campaign parameter is returned
  // exactly as the owner wrote it. The parameters go here and nowhere else — not into the chat
  // message, which the visitor would read and a vendor the privacy text does not name would
  // receive, not into GA4, and not onto the plain booking link a visitor who declines the
  // marketing consent keeps.
  function bookingURL(url) {
    var search = (window.location && window.location.search) || ''
    var pairs = String(search).replace(/^\?/, '').split('&')
    var query = []
    for (var i = 0; i < pairs.length; i++) {
      var eq = pairs[i].indexOf('=')
      if (eq < 1) continue
      var key = pairs[i].slice(0, eq)
      if (UTM_KEYS.indexOf(key) === -1) continue
      var value
      try {
        // A `+` in a query is a space, which is how a browser's own parser reads one and how
        // the `%20` this page writes is read back: a value that meant a literal plus arrives as
        // `%2B` and is left alone. Reading it the other way would put a campaign name nobody
        // chose — `newsletter+test` — on the booking link.
        value = decodeURIComponent(pairs[i].slice(eq + 1).replace(/\+/g, ' '))
      } catch (err) {
        // A value the browser cannot decode is not a campaign name this page can pass on.
        continue
      }
      if (!value || value.length > UTM_MAX) continue
      query.push(key + '=' + encodeURIComponent(value))
    }
    if (!query.length) return url
    return url + (url.indexOf('?') === -1 ? '?' : '&') + query.join('&')
  }

  function campaign(banner) {
    var key = banner.getAttribute(MARKETING_KEY_ATTR)
    if (!key) return
    // What asks the question is the configuration; a bar that carries the key without one is
    // the page a visitor withdraws on (W1.12), which loads none of the three and is here for
    // the withdrawal at the end of this function.
    var built = banner.getAttribute(MARKETING_PATH_ATTR)
    var here = bare(window.location && window.location.pathname)
    // And the loader runs on the page the configuration was built for and no other, so a
    // configuration copied to another page loads nothing there (F18's "those pages only", O2).
    var mine = built !== null && route(here) === route(built)

    // The page as the site reports it: its origin and its path, with no query string and no
    // fragment — the campaign parameters a visitor arrived through are the referrer's, not
    // this page's, and the tags are handed this address rather than left to read the one in
    // the window (F22).
    var page = bare(window.location && window.location.origin) + here
    var loaded = false

    function fetchScript(origin, path) {
      var tag = document.createElement('script')
      tag.async = true
      tag.src = origin + path
      document.head.appendChild(tag)
    }

    // The pixel: its own stub first, so nothing is lost to the fetch, and then its script. The
    // stub is Meta's own shape, and the half of it that matters is `callMethod`: `fbevents.js`
    // attaches that method to the stub it finds and drains the queue through it once, and from
    // then on a call pushed onto the queue is never read again. So a call is handed to
    // `callMethod` whenever one is there — a stub that only ever pushed would strand every event
    // a visitor's press sends after the load, and the withdrawal's revoke with them, which is
    // the privacy page's own claim about the pixel.
    //
    // It says whether it ran, which is what the key events of this page gate their own sends on
    // — an id nobody supplied is a loader that did not load, and not a queue that is sent through
    // namelessly.
    function pixel(origin, id) {
      if (!origin || !id) return false
      if (!window.fbq) {
        var stub = function () {
          return stub.callMethod ? stub.callMethod.apply(stub, arguments) : stub.queue.push(arguments)
        }
        if (!window._fbq) window._fbq = stub
        stub.push = stub
        stub.loaded = true
        stub.version = '2.0'
        stub.queue = []
        window.fbq = stub
      }
      // Before `init`, because that is the call the pixel is configured by: Meta's own automatic
      // collection — the button clicks it gathers and the page metadata it reads — is not this
      // page's, and the campaign's events are the three the handlers send and nothing else. The
      // id is the pixel's own, which is why the configuration names it.
      window.fbq('set', 'autoConfig', false, id)
      window.fbq('init', id)
      window.fbq('track', 'PageView', { page_location: page })
      fetchScript(origin, '/en_US/fbevents.js')
      return true
    }

    // The insight tag: the partner id on the window where its own snippet reads it, then its
    // script — and the same reported address, under the same key.
    function insight(origin, id) {
      if (!origin || !id) return
      window._linkedin_data_partner_ids = window._linkedin_data_partner_ids || []
      window._linkedin_data_partner_ids.push(id)
      window.lintrk = window.lintrk || function () {
        window.lintrk.q.push(Array.prototype.slice.call(arguments))
      }
      window.lintrk.q = window.lintrk.q || []
      window.lintrk('track', { page_location: page })
      fetchScript(origin, '/li.lms-analytics/insight.min.js')
    }

    // The embed: the container the calls-to-action section emits hidden, made a Calendly
    // inline widget, and Calendly's own script to build it. Nothing about the embed is
    // fetched before this runs, so a visitor who declines has the page's plain booking link
    // and no iframe. The address it is built from is the owner's link with the campaign
    // parameters the visitor arrived through on it (G2.4) — and it is the same address the
    // booking frame's report is accepted from, so the two cannot disagree about which host
    // this page books through.
    function embed(origin, url) {
      if (!origin || !url) return
      var host = document.getElementById(EMBED_ID)
      if (host) {
        host.setAttribute('data-url', bookingURL(url))
        host.hidden = false
      }
      fetchScript(origin, '/assets/external/widget.js')
    }

    function load() {
      if (!mine || loaded) return
      loaded = true
      tracking = pixel(banner.getAttribute(META_ORIGIN_ATTR), banner.getAttribute(META_ID_ATTR))
      insight(banner.getAttribute(LINKEDIN_ORIGIN_ATTR), banner.getAttribute(LINKEDIN_ID_ATTR))
      embed(banner.getAttribute(CALENDLY_ORIGIN_ATTR), banner.getAttribute(CALENDLY_URL_ATTR))
    }

    // A withdrawal stops what it can and says nothing it cannot do. The pixel takes a consent
    // command and is told; the insight tag has none, and no script already fetched can be
    // unfetched — so the senders stop on the next page, and the privacy page says so. What is
    // this site's to remove is removed: every first-party cookie the two tags set, which is the
    // list above, read out of the jar before anything is written so that nothing else is touched.
    //
    // It is not gated on this page having loaded anything, because the withdrawal is not always
    // made on the page that loaded: a visitor who accepted the campaign measurement on a
    // campaign page withdraws on the privacy page, where none of the three ever runs. That list
    // is the whole of what is read, so a visitor who never accepted has nothing deleted — and
    // nothing is written but an expiry on a cookie this host already held.
    function withdraw() {
      tracking = false
      if (window.fbq) window.fbq('consent', 'revoke')
      var host = (window.location && window.location.hostname) || ''
      var held = heldNames()
      for (var i = 0; i < MARKETING_COOKIES.length; i++) {
        if (held.indexOf(MARKETING_COOKIES[i]) !== -1) drop(MARKETING_COOKIES[i], host)
      }
    }

    if (read(banner, MARKETING_KEY_ATTR) === GRANTED) load()
    window.addEventListener(CONSENT_EVENT, function (event) {
      var decision = event && event.detail && event.detail.marketing
      if (decision !== GRANTED && decision !== DENIED) return
      if (decision === GRANTED) load()
      else withdraw()
    })
  }

  // ---- the campaign's key events (G2.4, spec §13) -----------------------------------------
  //
  // The funnel's three events, and the two things that make them worth reading. Two
  // measurements are told each of them, and each answers to its own gate: GA4, which waits for
  // the analytics grant — `keyEvent` sends nothing to it while `measuring` is false, which is
  // the state a visitor who has not answered the site's question is in and the state a
  // withdrawal puts them back in — and the pixel, which waits for the campaign's own answer and
  // reads it off `tracking`, under the names Meta gives the same three actions (the owner's
  // ruling of 2026-10-31). A visitor who has answered one question and not the other is
  // measured by one tag and not the other, which is what two questions mean. None of the events
  // carries a parameter — the count is the funnel, the page's own address reaches GA4 through
  // the tag's `config` call, and a campaign parameter is the booking link's business and not a
  // vendor's (F22) — and nothing a vendor said is forwarded to either.
  //
  // Two of the three are the page's own calls to action, found by the ids the calls-to-action
  // section gives them. The third is the embed's report, and it is taken from exactly one
  // sender: the frame Calendly's script built inside this page's booking container, reporting
  // from the origin the booking link names. Anything else that says `demo_booked` — another
  // origin, another window of this page, a message this page never asked for — is dropped
  // unread, and no field of the message that is accepted goes anywhere.
  function keyEvents(banner) {
    // The pixel's names for the three, keyed by GA4's: one handler per action tells both tags,
    // so a change to what an action means cannot reach one measurement and miss the other.
    var META_EVENTS = {
      install_click: 'ViewContent',
      whatsapp_click: 'Contact',
      demo_booked: 'Lead',
    }

    function keyEvent(name) {
      if (measuring) gtag('event', name)
      // The pixel's queue exists only where `load()` ran with an id, which is what `tracking`
      // says: an id nobody supplied is a tag nothing is sent through.
      if (tracking) window.fbq('track', META_EVENTS[name])
    }

    function press(id, name) {
      var link = document.getElementById(id)
      if (!link || !link.addEventListener) return
      link.addEventListener('click', function () {
        keyEvent(name)
      })
    }

    press(INSTALL_ID, 'install_click')
    press(CHAT_ID, 'whatsapp_click')

    // Which of the widget's messages is a booking. The frame reports several things over the
    // same channel — its own height, a viewed event type, a chosen date — and every one of them
    // arrives from the same origin and from the same window as the booking does, so the origin
    // and the sender cannot tell them apart: a listener that took any of them would count a
    // booking the moment the widget was drawn, and again on every date the visitor hovered.
    // The name is read to recognise the message and for nothing else — no other field of it is
    // looked at, and nothing in it goes anywhere (F22).
    var BOOKED = 'calendly.event_scheduled'

    // The origin the frame reports from, read out of the booking link rather than typed here:
    // this file names no host, and the host it takes a booking report from is the one the
    // owner's link names — the same one the deployed policy's `frame-src` admits (G2.3). A
    // booking link that is not an address, and a browser too old to parse one, are the same
    // answer: no report is taken, and the two events above are unaffected.
    var origin = ''
    try {
      origin = new URL(banner.getAttribute(CALENDLY_URL_ATTR)).origin
    } catch (err) {
      return
    }

    window.addEventListener('message', function (event) {
      if (!event || event.origin !== origin) return
      if (!event.data || event.data.event !== BOOKED) return
      var host = document.getElementById(EMBED_ID)
      var frame = host && host.querySelector ? host.querySelector('iframe') : null
      if (!frame || !frame.contentWindow || event.source !== frame.contentWindow) return
      keyEvent('demo_booked')
    })
  }

  function consent() {
    var banner = document.getElementById(BANNER_ID)
    if (!banner) return

    var status = document.getElementById(STATUS_ID)
    var closing = false

    function gone() {
      banner.hidden = true
      if (closing) banner.removeAttribute('data-consent-closing')
      // The early decision's mark (`consent-early.js`) is what draws the banner in the flow from
      // the first frame; the decision is made, so the mark goes with the banner.
      if (document.documentElement && document.documentElement.removeAttribute) {
        document.documentElement.removeAttribute('data-consent-ask')
      }
    }

    function dismiss() {
      // The bar collapses before it is hidden (owner-approved motion opportunities 2026-10-23):
      // the stylesheet runs the collapse from `data-consent-closing`, and `hidden` follows when
      // it ends. The decision is already stored and announced by then; only the drawing waits.
      // A browser with no `matchMedia` is too old for the collapse and is answered at once, and
      // the timer is the floor for a `transitionend` that never comes (a bar not being drawn).
      if (window.matchMedia && banner.removeAttribute) {
        closing = true
        var done = false
        var finish = function () {
          if (done) return
          done = true
          gone()
        }
        banner.addEventListener('transitionend', function (event) {
          if (event && event.target === banner && event.propertyName === 'opacity') finish()
        })
        window.setTimeout(finish, COLLAPSE_FLOOR_MS)
        banner.setAttribute('data-consent-closing', '')
      } else {
        gone()
      }
      // Focus goes to the content, not back to the top of the page and not to a control
      // that has just disappeared. `tabindex="-1"` makes <main> a focus target without
      // putting it in the tab order.
      var main = document.getElementById(MAIN_ID)
      if (main) {
        main.setAttribute('tabindex', '-1')
        main.focus()
      }
    }

    // The campaign question, where this bar asks it (G2.3): the attribute is on the four
    // campaign pages' banner and on no other page's, so this is null everywhere else and the
    // press below is the one the site has always made. The privacy page's bar carries the key
    // without the question — it is where a visitor withdraws one — and `asksCampaign` is what
    // tells the two apart: the key is what a decision is written under, the question is what
    // may be answered with a yes.
    var marketing = banner.getAttribute(MARKETING_KEY_ATTR) ? MARKETING_KEY_ATTR : null
    var asksCampaign = marketing !== null && banner.getAttribute(MARKETING_PATH_ATTR) !== null

    function choose(decision) {
      // One decision per banner: a second press or an Escape while the bar collapses is not a
      // new choice, exactly as it could not be once the bar was gone.
      if (closing) return
      // Which questions this press answers. The campaign's, wherever the bar asks it — the
      // visitor was asked it here and has answered it here. The site's, when they have not
      // answered it yet, and always on a bar that asks nothing else: that second half is what
      // makes a withdrawal on the privacy page overwrite the grant it is withdrawing. On a
      // campaign page, a press about the campaign never rewrites an answer the visitor gave
      // the site — a visitor who accepted analytics on another page keeps that answer, and one
      // who declined it is not quietly granted it here.
      var siteDecision = !asksCampaign || stored(banner) === null ? decision : null
      // And a bar that carries the campaign key without asking the question answers it in the
      // refusal direction only (the privacy page, `marketingKey`): this page never put the
      // three vendors to the visitor, so the bar may take that answer away and may not give it.
      var campaignDecision = asksCampaign || (decision === DENIED && marketing !== null) ? decision : null
      var keptSite = siteDecision === null ? true : keep(banner, SITE_KEY_ATTR, siteDecision)
      var keptCampaign = campaignDecision === null ? true : keep(banner, marketing, campaignDecision)
      // A refusal is honoured whether or not it could be remembered: the browser refused the
      // WRITE (a full quota, an old private mode), which is not the same as refusing the answer
      // — the tag this press stops is running on a page whose storage it can read, and a visitor
      // who has just said no is not measured because the note could not be filed. A grant is the
      // other way round: it is only honoured when it is kept, so nothing is loaded on a promise
      // the next page would not remember. A question this press did not answer is null, which
      // both gates ignore.
      if (!keptSite || !keptCampaign) {
        // The answer cannot be kept, so it is not honoured: nothing is loaded, the banner
        // stays, and the status line says why.
        if (status) status.hidden = false
        announce(
          keptSite ? siteDecision : siteDecision === DENIED ? DENIED : null,
          keptCampaign ? campaignDecision : campaignDecision === DENIED ? DENIED : null,
        )
        return
      }
      dismiss()
      announce(siteDecision, campaignDecision)
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

    // The choice is offered when there is no readable decision about a question this bar asks —
    // the site's, the campaign's on a campaign page, or both — and always on the privacy page,
    // which is where a visitor withdraws one.
    if (
      banner.getAttribute('data-consent-always') === 'true' ||
      stored(banner) === null ||
      (asksCampaign && read(banner, marketing) === null)
    ) {
      banner.hidden = false
    }

    analytics(banner)
    campaign(banner)
    keyEvents(banner)
  }

  // ---- the hero's live card (the owner's ruling of 2026-10-23, the redesign's lever 3) -------
  //
  // The card is the request `src/lib/agent-request.mjs` holds, the jobs it launches and the
  // pull requests they end in. With no script it is the static picture of that one request.
  // Here it becomes the one live thing on the page: the field is revealed, and whatever task the
  // reader types is redrawn as the jobs Fugaro would launch for it, in the words `fugaro watch`
  // prints (`implement`, `review`, `ready for review`). It is an illustration and says so in its
  // caption; it sends nothing anywhere and stores nothing.
  //
  // How many jobs: one per line when the reader writes several lines, else the number the task
  // names (a figure or a number word, `three bug fixes`), else one. The ceiling is README.md's
  // `max_parallel` default, which the hero hands over as `data-live-max`.
  var LIVE = '[data-live-run]'
  var NUMBER_WORDS = ['one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight', 'nine', 'ten',
    'eleven', 'twelve', 'thirteen', 'fourteen', 'fifteen', 'sixteen', 'seventeen', 'eighteen',
    'nineteen', 'twenty']
  var LIVE_STAGES = ['implement', 'review', 'ready for review']
  var LIVE_ROWS = 6

  function jobsIn(text) {
    var lines = String(text || '').split(/\n/)
    var named = []
    for (var i = 0; i < lines.length; i++) if (/\S/.test(lines[i])) named.push(lines[i])
    if (named.length === 0) return 0
    if (named.length > 1) return named.length
    var figure = /(^|[^\w.])(\d{1,4})(?![\w.])/.exec(named[0])
    if (figure) return parseInt(figure[2], 10)
    var lower = named[0].toLowerCase()
    for (var w = NUMBER_WORDS.length - 1; w >= 0; w--) {
      if (new RegExp('\\b' + NUMBER_WORDS[w] + '\\b').test(lower)) return w + 1
    }
    return 1
  }

  function liveRun() {
    if (typeof document.querySelector !== 'function') return
    var card = document.querySelector(LIVE)
    if (!card) return
    var ask = card.querySelector('[data-live-ask]')
    var field = card.querySelector('[data-live-field]')
    var prompt = card.querySelector('[data-live-prompt]')
    var status = card.querySelector('[data-live-status-text]')
    var list = card.querySelector('[data-live-jobs]')
    if (!ask || !field || !status || !list) return
    var max = parseInt(card.getAttribute('data-live-max'), 10) || 20
    // The rows are copies of the first row the page rendered, so they carry every attribute the
    // build gave it (the scoped style's own among them) and this file writes no markup.
    var model = list.firstElementChild ? list.firstElementChild.cloneNode(true) : null
    if (!model || !model.querySelector('.hero-shot-job-name') || !model.querySelector('.hero-shot-stage')) return
    var quiet = window.matchMedia ? window.matchMedia('(prefers-reduced-motion: reduce)') : null
    // The rows on the card, in order, each with its own timers, and the "and N more" line when
    // there is one. A pause in typing updates these in place rather than drawing the list again
    // (the owner-approved motion review of 2026-10-23): a row already on the card keeps its stage
    // and its clock, only a row that is new arrives, and a row no longer asked for leaves.
    var rows = []
    var rest = null
    var drawn = false
    var waiting = 0

    function stop(entry) {
      for (var t = 0; t < entry.timers.length; t++) window.clearTimeout(entry.timers[t])
      entry.timers = []
    }

    function row() {
      var item = model.cloneNode(true)
      item.removeAttribute('data-arrive')
      var stage = item.querySelector('.hero-shot-stage')
      return { item: item, name: item.querySelector('.hero-shot-job-name'), stage: stage, timers: [] }
    }

    // A step every 400 to 700 ms: quick enough to read as work moving, with a little spread so
    // the rows do not tick in lockstep.
    function advance(entry, step, delay) {
      entry.timers.push(window.setTimeout(function () {
        var last = step === LIVE_STAGES.length - 1
        // The stage's words crossfade, and the row's finished look arrives with its last word:
        // `data-settled` is what settles the row's icon into place, once, since a row reaches
        // its last stage once (the owner-approved motion opportunities of 2026-10-23).
        swapText(entry.stage, LIVE_STAGES[step], function () {
          if (!last) return
          entry.item.removeAttribute('data-stage')
          entry.item.setAttribute('data-settled', '')
        })
        if (!last) {
          entry.item.setAttribute('data-stage', 'working')
          advance(entry, step + 1, 400 + Math.floor(Math.random() * 300))
        }
      }, delay))
    }

    function draw() {
      var asked = jobsIn(field.value)
      var count = Math.min(asked, max)
      if (count === 0) status.textContent = 'Type a task to launch a job'
      else
        status.textContent =
          'Fugaro: launching ' + count + (count === 1 ? ' job' : ' jobs') +
          (asked > max ? ' (max_parallel is ' + max + ' by default)' : '')
      // A long fan-out draws its first rows and one line for the rest, so the card stays the size
      // of a card.
      var shown = count > LIVE_ROWS ? LIVE_ROWS - 1 : count
      // The page's own first row is the static card's (the no-script picture); the first draw
      // replaces it, and every draw after that reuses what it drew.
      if (!drawn) while (list.firstChild) list.removeChild(list.firstChild)
      drawn = true
      while (rows.length > shown) {
        var gone = rows.pop()
        stop(gone)
        list.removeChild(gone.item)
      }
      var kept = rows.length
      for (var n = 1; n <= shown; n++) {
        var entry = rows[n - 1]
        if (!entry) {
          entry = row()
          entry.item.setAttribute('data-arrive', '')
          if (quiet && quiet.matches) {
            entry.item.removeAttribute('data-stage')
            entry.stage.textContent = LIVE_STAGES[LIVE_STAGES.length - 1]
          } else {
            entry.item.setAttribute('data-stage', 'working')
            entry.stage.textContent = 'queued'
            advance(entry, 0, 300 + (n - kept) * 180)
          }
          rows.push(entry)
          list.insertBefore(entry.item, rest ? rest.item : null)
        }
        entry.name.textContent = 'Pull request ' + n + ' of ' + count
      }
      if (shown < count) {
        if (!rest) {
          rest = row()
          rest.item.removeAttribute('data-stage')
          rest.stage.textContent = ''
          rest.item.setAttribute('data-arrive', '')
          list.appendChild(rest.item)
        }
        rest.name.textContent = 'and ' + (count - shown) + ' more pull requests'
      } else if (rest) {
        list.removeChild(rest.item)
        rest = null
      }
    }

    ask.hidden = false
    if (prompt) prompt.hidden = true
    field.addEventListener('input', function () {
      window.clearTimeout(waiting)
      waiting = window.setTimeout(draw, 250)
    })
    draw()
  }

  // ---- the four levels on How it works, as a slider (owner's preview feedback, 2026-10-24) --
  //
  // The section is four stills in the markup; this makes it a slider and nothing without it
  // does. It advances by itself every LEVELS_ADVANCE_MS, and only while the reader is not
  // reading: not under the pointer, not with the focus inside, not with the tab hidden, not off
  // screen, and never for a reader who asked for less motion (it starts paused for them). Any
  // press of Previous, Next or a dot pauses it, and Pause/Play is always on the bar. While it
  // advances by itself the slides are not announced (`aria-live="off"`); paused, a change is.
  //
  // Four seconds is the time to read a slide's headline: each board's name and the line under
  // it (at most 12 words, under three seconds at an ordinary 250 words a minute), plus about a
  // second to find one's place in the drawing. Two seconds would move on mid-sentence. The
  // board's longer note under the drawing (up to about 45 words) is for a reader who stops it,
  // which the pointer, the focus or Pause each do.
  var LEVELS = '[data-levels]'
  var LEVELS_ADVANCE_MS = 4000
  var LEVELS_SWIPE_PX = 40

  function levels() {
    if (typeof document.querySelector !== 'function') return
    var root = document.querySelector(LEVELS)
    if (!root) return
    var slides = root.querySelectorAll('[data-levels-slide]')
    var track = root.querySelector('[data-levels-track]')
    var bar = root.querySelector('[data-levels-controls]')
    var toggle = root.querySelector('[data-levels-toggle]')
    var prev = root.querySelector('[data-levels-prev]')
    var next = root.querySelector('[data-levels-next]')
    var dots = root.querySelectorAll('[data-levels-dot]')
    var count = slides.length
    if (count < 2 || !track || !bar || !toggle || !prev || !next) return
    var quiet = window.matchMedia ? window.matchMedia('(prefers-reduced-motion: reduce)') : null
    var current = 0
    var playing = !(quiet && quiet.matches)
    var hovered = false
    var focused = false
    var onScreen = !('IntersectionObserver' in window)
    var timer = null

    function show(index) {
      current = (index + count) % count
      for (var i = 0; i < count; i++) {
        slides[i].setAttribute('data-current', i === current ? 'true' : 'false')
        slides[i].setAttribute('aria-hidden', i === current ? 'false' : 'true')
      }
      for (var d = 0; d < dots.length; d++) {
        dots[d].setAttribute('aria-current', d === current ? 'true' : 'false')
      }
    }

    function advancing() {
      return playing && !hovered && !focused && onScreen && !document.hidden
    }

    // The interval starts again from zero whenever the slider resumes, so a reader who looks
    // away from a slide is given its full time when they look back.
    function schedule() {
      if (timer) window.clearInterval(timer)
      timer = null
      track.setAttribute('aria-live', advancing() ? 'off' : 'polite')
      if (advancing()) {
        timer = window.setInterval(function () {
          show(current + 1)
        }, LEVELS_ADVANCE_MS)
      }
    }

    function setPlaying(value) {
      playing = value
      toggle.textContent = value ? 'Pause' : 'Play'
      toggle.setAttribute('aria-label', value ? 'Pause the levels' : 'Play the levels')
      schedule()
    }

    function go(index) {
      show(index)
      setPlaying(false)
    }

    toggle.addEventListener('click', function () {
      setPlaying(!playing)
    })
    prev.addEventListener('click', function () {
      go(current - 1)
    })
    next.addEventListener('click', function () {
      go(current + 1)
    })
    for (var d = 0; d < dots.length; d++) {
      ;(function (dot) {
        dot.addEventListener('click', function () {
          go(parseInt(dot.getAttribute('data-levels-dot'), 10) || 0)
        })
      })(dots[d])
    }
    root.addEventListener('keydown', function (event) {
      if (event.key === 'ArrowLeft') go(current - 1)
      else if (event.key === 'ArrowRight') go(current + 1)
    })
    root.addEventListener('mouseenter', function () {
      hovered = true
      schedule()
    })
    root.addEventListener('mouseleave', function () {
      hovered = false
      schedule()
    })
    root.addEventListener('focusin', function () {
      focused = true
      schedule()
    })
    root.addEventListener('focusout', function (event) {
      if (event && event.relatedTarget && root.contains(event.relatedTarget)) return
      focused = false
      schedule()
    })
    document.addEventListener('visibilitychange', schedule)
    if (quiet && quiet.addEventListener) {
      quiet.addEventListener('change', function (event) {
        if (event.matches) setPlaying(false)
      })
    }

    // A swipe on a touch screen is a press of Previous or Next.
    var startX = null
    track.addEventListener('pointerdown', function (event) {
      startX = event.pointerType === 'touch' ? event.clientX : null
    })
    track.addEventListener('pointerup', function (event) {
      if (startX === null) return
      var dx = event.clientX - startX
      startX = null
      if (Math.abs(dx) >= LEVELS_SWIPE_PX) go(dx < 0 ? current + 1 : current - 1)
    })

    if (!onScreen) {
      new window.IntersectionObserver(function (entries) {
        onScreen = entries[entries.length - 1].isIntersecting
        schedule()
      }, { threshold: 0.3 }).observe(root)
    }

    // A link to one level (`#level-3`) opens on it, paused.
    var start = 0
    var hash = window.location && window.location.hash ? window.location.hash.slice(1) : ''
    for (var h = 0; h < count; h++) {
      if (hash && slides[h].getAttribute('id') === hash) {
        start = h
        playing = false
      }
    }
    window.addEventListener('hashchange', function () {
      var id = window.location.hash.slice(1)
      for (var k = 0; k < count; k++) if (slides[k].getAttribute('id') === id) go(k)
    })

    root.setAttribute('data-levels-ready', '')
    bar.hidden = false
    show(start)
    setPlaying(playing)
  }

  // ---- the run picture on How it works: its four steps arrive once, in view -----------------
  //
  // The stylesheet holds the steps at their first frame from the first paint (paused, filling
  // backwards) wherever scripts run and motion is welcome; this only starts them, by marking the
  // picture `play` once it is in view. A picture already in view starts at once (the observer
  // reports it on its first callback), and a browser with no IntersectionObserver starts at once
  // too. A print shows the picture whole.
  function runReveal() {
    if (typeof document.querySelector !== 'function') return
    var figure = document.querySelector('.run-diagram')
    if (!figure) return
    function play() {
      figure.setAttribute('data-run-reveal', 'play')
    }
    window.addEventListener('beforeprint', play)
    if (!('IntersectionObserver' in window)) {
      play()
      return
    }
    var seen = new IntersectionObserver(function (entries) {
      for (var e = 0; e < entries.length; e++) {
        if (!entries[e].isIntersecting) continue
        play()
        seen.disconnect()
      }
    }, { threshold: 0.35 })
    seen.observe(figure)
  }

  // ---- entrances (the redesign's motion layer, step 9) ------------------------------------
  //
  // The parts of each home section rise into place once, in reading order, as the section
  // enters the window. Only what is below the first screen when the script runs is held back,
  // so nothing a reader already sees blinks; with no script, or a reader who asks for less
  // motion, everything is simply there.
  function entrances() {
    if (!('IntersectionObserver' in window) || typeof document.querySelectorAll !== 'function') return
    if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    var blocks = document.querySelectorAll('[data-reveal-block]')
    if (blocks.length === 0) return
    var seen = new IntersectionObserver(function (entries) {
      for (var e = 0; e < entries.length; e++) {
        if (!entries[e].isIntersecting) continue
        entries[e].target.setAttribute('data-revealed', '')
        seen.unobserve(entries[e].target)
      }
    }, { rootMargin: '0px 0px -12% 0px' })
    // The trigger line is the observer's: the window's bottom less its 12% margin. Anything
    // already above it when the script runs is in view and is never held back.
    var fold = (window.innerHeight || 0) * 0.88
    var held = []
    for (var b = 0; b < blocks.length; b++) {
      var parts = blocks[b].querySelectorAll('h2, h3, p, li, figure, pre')
      for (var p = 0; p < parts.length; p++) {
        if (parts[p].getBoundingClientRect().top < fold) continue
        parts[p].setAttribute('data-reveal', '')
        // The stagger stops at the fourth part, so the last part of a block starts 210 ms after
        // the first (the owner-approved motion review of 2026-10-23).
        parts[p].style.transitionDelay = Math.min(p, 3) * 70 + 'ms'
        seen.observe(parts[p])
        held.push(parts[p])
      }
    }
    // The failsafe: an observer that never fires (a fast scroll past, a stalled observer, a
    // print, a screenshot tool) must not leave a part invisible. About two seconds after the
    // page loads, anything still held back that is above the window's bottom is shown, and so is
    // everything at once when the page is printed.
    function release(all) {
      var bottom = window.innerHeight || 0
      for (var h = 0; h < held.length; h++) {
        if (held[h].hasAttribute('data-revealed')) continue
        if (!all && held[h].getBoundingClientRect().top >= bottom) continue
        held[h].setAttribute('data-revealed', '')
        seen.unobserve(held[h])
      }
    }
    function later() {
      window.setTimeout(function () { release(false) }, 2000)
    }
    if (document.readyState === 'complete') later()
    else window.addEventListener('load', later)
    window.addEventListener('beforeprint', function () { release(true) })
  }


  function start() {
    menu()
    consent()
    // The sliders first, and unconditionally: a slider's controls are not a convenience the
    // page can do without (the arrows, the count and the dots are the owner's feedback of
    // 2026-10-21), so none is behind the clipboard gate the copy controls stand behind below.
    var sliders = document.querySelectorAll(SLIDER)
    for (var s = 0; s < sliders.length; s++) {
      slider(sliders[s])
    }
    // The hero's live card and the entrances, before the clipboard gate for the same reason.
    liveRun()
    levels()
    runReveal()
    entrances()
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
