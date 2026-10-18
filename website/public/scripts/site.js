/* website/public/scripts/site.js — the site's one script (decision D183, redesign parts P3
 * and P6).
 *
 * The site ships exactly one first-party script file and this is it: one request, one line
 * in the byte budget, one file to read. `website/test/budget.mjs` counts these bytes as
 * every page's JavaScript, `website/test/copy.mjs` holds the controls it drives to the
 * markup they are written in, and `website/test/origins.mjs` reads the file for any origin
 * that is not this one — of which there are none: it fetches nothing, and it names no URL.
 *
 * What it does:
 *
 *   - **The copy control on every command block (part P6).** The markup renders each block
 *     with its control already in it and `hidden`, and this reveals the control and drives
 *     it. A browser that runs no script therefore shows the command and no button, rather
 *     than a button that copies nothing.
 *   - **Escape and a click outside close an open menu (part P3).** The menu's two levels are
 *     `<details>` and open and close by themselves; what a `<details>` does not do is close
 *     when the reader presses Escape or clicks somewhere else, which is what a menu owes a
 *     keyboard. Closing is all this adds: with no script the menu still opens, still closes
 *     and still reaches every link.
 *
 * Nothing here is load-bearing: the commands are in the markup in full, and every one of
 * them is readable, selectable and copyable by hand without this file ever arriving; the
 * menu is a `<details>` at both levels and needs none of this to work.
 *
 * It is loaded with `defer` from the base layout, so it runs once the document has been
 * parsed and every block it looks for is in the tree. It writes no storage and sets no
 * cookie: the clipboard it writes to is the reader's own.
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

  // Close every open menu. A menu closed under the reader's focus would leave that focus on
  // an element that is no longer rendered, so the control that opened it takes the focus
  // back — which is where a keyboard reader expects to be after dismissing a menu.
  function closeMenus() {
    var open = document.querySelectorAll(OPEN_MENUS)
    for (var i = 0; i < open.length; i++) {
      var details = open[i]
      var held = details.contains(document.activeElement)
      details.open = false
      if (held) details.querySelector('summary').focus()
    }
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

  function start() {
    menu()
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
