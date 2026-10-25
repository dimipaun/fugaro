// website/src/lib/copy-control.mjs — the copy control's markup, written once (redesign part
// P6; reconciliation finding 13, decision D183).
//
// Every command the site prints carries one of these, and there are two ways a page renders
// one: an `.astro` file writes the wrapper and drops this markup inside it with
// `<Fragment set:html={copyControlHTML(…)} />`, and the docs hub — which renders the
// generated CLI reference as a string and so has no component to put it in — calls
// `commandBlockHTML` to wrap each block the generated document carries. Two callers, one
// piece of markup: the control's shape is a fact about the site, and a second copy of it in
// the hub would be the copy that drifts.
//
// The markup is deliberately dull and entirely static. The button is emitted `hidden`, and
// `website/public/scripts/site.js` is what reveals it: a reader whose browser runs no script
// sees the command and no button, rather than a button that copies nothing (D183's "no
// broken control"). `website/test/copy.mjs` holds every built page to that shape.
//
// The status line is the control's whole feedback. `role="status"` with `aria-live="polite"`
// is a region a screen reader announces when its text changes and never interrupts for, and
// the script writes "Copied" into it and clears it again. The button itself is an icon: a
// clipboard that becomes a checkmark for a moment once the copy happened, while the status line
// says "Copied" to a screen reader (and, only when a copy fails, says why to everyone).
//
// Standard library only: this module is imported by the build, and it computes nothing.

/** The three characters that would end an attribute or open a tag inside one. */
function escapeAttribute(value) {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

// The button's two glyphs (the owner's feedback of 2026-10-24: an icon, not the word, and a
// checkmark once copied). Inline, `currentColor`, hidden from assistive technology: the button's
// name is its `aria-label`, and the status line announces "Copied".
const ICON_ATTRS = 'viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"'
const CLIPBOARD_ICON = `<svg class="copy-icon copy-icon-clipboard" ${ICON_ATTRS}><rect x="8" y="3" width="8" height="4" rx="1"></rect><path d="M16 5h2a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h2"></path></svg>`
const CHECK_ICON = `<svg class="copy-icon copy-icon-check" ${ICON_ATTRS}><path d="M5 12.5l4.5 4.5L19 7.5"></path></svg>`

/**
 * The control itself: the button the script reveals, and the line it announces into.
 * `label` is the button's accessible name, which says which command it copies ("Copy the
 * install command"); the button shows an icon, and its `title` is the hover and focus tooltip.
 */
export function copyControlHTML(label) {
  return (
    '<div class="command-controls">' +
    '<span class="copy-status" data-copy-status role="status" aria-live="polite"></span>' +
    `<button type="button" class="copy-button" data-copy-button aria-label="${escapeAttribute(label)}" title="Copy command" data-copied="false" hidden>` +
    CLIPBOARD_ICON +
    CHECK_ICON +
    '</button>' +
    '</div>'
  )
}

/**
 * One command block: the markup a page already has, and the control that goes with it. The
 * docs hub is the caller — it holds the rendered reference as a string — and the wrapper it
 * gets is the same one the `.astro` files write by hand.
 */
export function commandBlockHTML(innerHTML, label) {
  return `<div class="command-block" data-copy>${copyControlHTML(label)}${innerHTML}</div>`
}

/**
 * A command cut after each `/`, so a page can put a `<wbr>` between the pieces: a long path
 * such as `github.com/dimipaun/fugaro/cmd/fugaro@latest` then wraps at a slash on a phone rather
 * than mid-word. `<wbr>` adds no text, so the command a reader copies is unchanged.
 */
export function commandPieces(command) {
  return String(command).split(/(?<=\/)/)
}
