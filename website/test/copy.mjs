// website/test/copy.mjs — the copy control on every command the site prints (redesign part
// P6; reconciliation finding 13, decision D183).
//
// Finding 13 measured the site before P6: 23 `<pre>` blocks (three on the home page, ten on
// getting started, ten on the docs hub) and not one control among them, so every command a
// reader wanted was a select-and-drag. P6 answers it with one first-party script,
// `public/scripts/site.js`, and this file is the rule the markup has to satisfy for that
// script to be able to drive it:
//
//   - **Every command carries exactly one control.** A command is a `<pre>` (an install
//     block, a code block of the rendered CLI reference, the terminal's own line) or a
//     `code.usage` (the caps guide's usage lines, which are commands but not `<pre>`).
//   - **The control is in the markup, `hidden`, and the command is too.** This is D183's
//     "no broken control": the control is emitted hidden and the script reveals it, so a
//     reader whose browser runs no script — or whose browser has no clipboard to write to —
//     sees the command and no button, rather than a button that copies nothing.
//   - **The control has an accessible name, and the name says what it copies.** A page of
//     commands carries several of these buttons, so a name that is only the visible word
//     "Copy" leaves a screen-reader user counting buttons rather than hearing which command
//     each one copies; and a name that does not contain the visible label is a control a
//     screen-reader user cannot find by the word they can see (WCAG 2.5.3). The name is
//     resolved the way a browser resolves one — `aria-label`, then `aria-labelledby`, then the
//     button's text — which is also how `website/test/a11y.mjs` resolves the first two.
//   - **What the script would copy is the command it sits on.** The script copies the
//     block's `[data-copy-source]` when there is one and its first `pre`/`code` otherwise,
//     so that is what this file resolves — for every block, not only for the terminal that
//     marks its own source.
//
// Every refusal below is a fixture, run on every check: the rule is only worth stating if
// the check that enforces it refuses the defect it names. `FUGARO_GATES_DIST` points the
// built-site case at a copy of `dist/`, which is how each one was proved by hand as well.
//
// It reads `dist/`, which `npm run check` builds before it runs this file. Standard library
// only; no network, and no browser: what a real clipboard does with a real click is the
// owner's live check (70c), and nothing here claims to have run one.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { DIST, HTMLScanError, builtPages, parse, textOf, walk } from './lib/html.mjs'

// The script every page with a control has to load. The control is emitted `hidden` and this
// file is what reveals it, so a page carrying controls without it is a page whose buttons
// never appear.
const SCRIPT = '/scripts/site.js'

/** The `[data-copy]` block a command belongs to, or null when it is in none. */
function blockOf(el) {
  for (let parent = el.parent; parent; parent = parent.parent) {
    if (parent.attrs && parent.attrs.has('data-copy')) return parent
  }
  return null
}

/** The command a page prints: a code block, or the caps guide's usage line. */
function isCommand(el) {
  return el.name === 'pre' || (el.name === 'code' && (el.attrs.get('class') ?? '').split(/\s+/).includes('usage'))
}

/** Whether the node, or anything above it, is hidden from assistive technology. */
function ariaHidden(node) {
  for (let el = node; el; el = el.parent) {
    if (el.attrs && el.attrs.get('aria-hidden') === 'true') return true
  }
  return false
}

/** An element's accessible name, resolved the way `a11y.mjs` resolves one. */
function nameOf(el, byId) {
  const label = (el.attrs.get('aria-label') ?? '').trim()
  if (label) return label
  const by = (el.attrs.get('aria-labelledby') ?? '').trim()
  if (by) {
    return by
      .split(/\s+/)
      .map((id) => (byId.has(id) ? textOf(byId.get(id)).trim() : ''))
      .join(' ')
      .trim()
  }
  return ''
}

/** The element the script would copy out of a block, by the rule it uses. */
function sourceOf(block) {
  const inside = [...walk(block)]
  return inside.find((el) => el.attrs.has('data-copy-source')) ?? inside.find((el) => el.name === 'pre' || el.name === 'code') ?? null
}

/** A node's text with the layout taken out of it, which is how two renderings compare. */
function words(node) {
  return textOf(node).replace(/\s+/g, ' ').trim()
}

/**
 * Every copy-control failure of one built page. `rel` names the page in the lines it
 * returns; an empty array means the page's commands are all copyable.
 */
export function copyFailures(html, rel) {
  const out = []
  const fail = (why) => out.push(`${rel}: ${why}`)
  let doc
  try {
    doc = parse(html)
  } catch (err) {
    if (err instanceof HTMLScanError) return [`${rel}: ${err.message}`]
    throw err
  }
  const all = [...walk(doc)]
  const commands = all.filter(isCommand)
  const blocks = all.filter((el) => el.attrs.has('data-copy'))

  // The commands first: a command with no block, or with a block that is not its own, is the
  // defect this file exists for.
  for (const command of commands) {
    const block = blockOf(command)
    if (!block) {
      fail(`a <${command.name}${isUsage(command) ? ' class="usage"' : ''}> holding ${JSON.stringify(words(command).slice(0, 60))} is in no [data-copy] block, so it has no copy control: every command the site prints has one (P6, D183)`)
      continue
    }
    const mine = all.filter((el) => isCommand(el) && blockOf(el) === block)
    if (mine.length !== 1) {
      fail(`a [data-copy] block holds ${mine.length} commands, and a block is one command and one control: the control would copy one of them and the reader would not know which`)
      continue
    }

    const buttons = [...walk(block)].filter((el) => el.attrs.has('data-copy-button'))
    if (buttons.length !== 1) {
      fail(`a [data-copy] block holds ${buttons.length} copy controls; it has exactly one (P6, D183)`)
      continue
    }
    const button = buttons[0]
    if (button.name !== 'button') fail(`the copy control is a <${button.name}>, and a control a keyboard cannot press is not a control`)

    // Emitted hidden, and revealed by the script: a browser that runs no script shows the
    // command and no button rather than a button that copies nothing (D183).
    if (!button.attrs.has('hidden')) {
      fail(`the copy control is not emitted \`hidden\`: the site's script reveals it, and a control rendered visible by the markup is a button that does nothing for a reader whose browser runs no script (D183)`)
    }
    if ((button.attrs.get('type') ?? '').toLowerCase() !== 'button') {
      fail(`the copy control is <button type="${button.attrs.get('type') ?? ''}">: a copy control is not a submit button, and a bare <button> in a form submits it`)
    }

    // The accessible name, and the label a sighted reader sees inside it (WCAG 2.5.3). The
    // name is resolved the way a browser resolves one — `aria-label`, then a resolving
    // `aria-labelledby`, then the button's own text — and `website/test/a11y.mjs` resolves the
    // first two the same way, so the two gates agree about what a name is.
    const byId = new Map(all.filter((el) => el.attrs.has('id')).map((el) => [el.attrs.get('id'), el]))
    const visible = words(button)
    const name = nameOf(button, byId) || visible
    if (name === '') {
      fail('the copy control has no accessible name: a screen reader announces a button by its name, and "button" alone says nothing about what it copies')
    } else if (name.toLowerCase() === visible.toLowerCase()) {
      fail(`the copy control's accessible name is just ${JSON.stringify(name)}: a page of commands carries several of these controls, and a name that says only "Copy" leaves a screen-reader user counting buttons instead of hearing which command each one copies`)
    } else if (visible !== '' && !name.toLowerCase().includes(visible.toLowerCase())) {
      fail(`the copy control's accessible name ${JSON.stringify(name)} does not contain its visible label ${JSON.stringify(visible)}: the name has to begin with the words on the button (WCAG 2.5.3)`)
    }

    // The state the script announces into, politely: it is what a reader is told when the
    // copy happened, and what a screen reader is told without being interrupted.
    const statuses = [...walk(block)].filter((el) => el.attrs.has('data-copy-status'))
    if (statuses.length !== 1) {
      fail(`a [data-copy] block holds ${statuses.length} status lines; it has exactly one, which is where the script writes "Copied"`)
    } else if ((statuses[0].attrs.get('aria-live') ?? '').toLowerCase() !== 'polite') {
      fail(`the copy control's status line is aria-live="${statuses[0].attrs.get('aria-live') ?? ''}": the copied state is announced politely, and an assertive one interrupts whatever the reader is hearing`)
    }

    // What the script would copy, resolved by the rule the script uses.
    const source = sourceOf(block)
    const text = words(mine[0])
    if (text === '') {
      fail('a command block holds no text: the command is in the markup in full, and a block whose text is somewhere else is one the script copies empty')
    } else if (!source || !text.includes(words(source))) {
      fail(`the copy control would copy ${JSON.stringify(source ? words(source).slice(0, 60) : '')}, which is not part of the command it sits on (${JSON.stringify(text.slice(0, 60))}): the script copies the block's [data-copy-source] or its first pre/code, and the control has to be on the command it copies`)
    }
    if (source && ariaHidden(source)) {
      fail('the copy control copies text inside aria-hidden: the script puts that text on the clipboard, and the page tells a screen reader it is decoration — the terminal marks the `$` prompt this way, and the source is the command beside it rather than the prompt')
    }

    // Nothing here may be inside aria-hidden: the keyboard reaches what a screen reader
    // cannot see, which is what a control inside a decorative wrapper becomes.
    if (ariaHidden(button)) {
      fail('the copy control is inside aria-hidden: the keyboard reaches a control a screen reader cannot see')
    }
  }

  // And the other direction: a block with no command in it is a control copying nothing.
  for (const block of blocks) {
    if (!all.some((el) => isCommand(el) && blockOf(el) === block)) {
      fail('a [data-copy] block holds no command: the control is on a command the site prints, and a block with no command in it is a button that copies nothing')
    }
  }
  return out
}

/** Whether a page prints any command at all, and so owes a control. */
function printsCommands(html) {
  return [...walk(parse(html))].some(isCommand)
}

function isUsage(el) {
  return el.name === 'code' && (el.attrs.get('class') ?? '').split(/\s+/).includes('usage')
}

// ---- the gate ---------------------------------------------------------------------------

test('every command on every built page carries exactly one copy control', () => {
  const pages = builtPages(DIST)
  assert.ok(pages.length > 0, 'the build produced no page, so this case would check nothing')

  const failures = []
  let controls = 0
  let withCommands = 0
  for (const page of pages) {
    failures.push(...copyFailures(page.html, page.rel))
    controls += (page.html.match(/data-copy-button/g) ?? []).length
    if (printsCommands(page.html)) {
      withCommands++
      // The control is revealed by the site's script, so a page with controls has to load it:
      // a page carrying a hidden button and no script is a command a reader cannot copy and a
      // button that never appears.
      assert.ok(
        page.html.includes(`<script src="${SCRIPT}"`),
        `${page.rel} prints commands but does not load ${SCRIPT}: the controls are emitted hidden and the script is what reveals them (D183)`,
      )
    }
  }
  assert.deepEqual(failures, [], `the built site breaks the copy control's rule (P6, D183):\n  ${failures.join('\n  ')}`)

  // The check is not vacuous: finding 13 counted 23 `<pre>` blocks and no control, and the
  // pages that print commands are the pages that have to carry one.
  assert.ok(controls > 0, 'the built site carries no copy control at all: every command the site prints has one (P6, RF13)')
  assert.ok(withCommands >= 3, `only ${withCommands} built page(s) print a command: finding 13 measured three (the home page, getting started and the docs hub), so this case is checking less than it was written for`)
})

// ---- the refusals, kept ------------------------------------------------------------------

// A page in the shape the layout writes: a heading, and whatever the fixture adds.
function page(body) {
  return `<!DOCTYPE html><html lang="en"><head><title>t</title><script src="${SCRIPT}" defer></script></head><body><main id="main"><h1>t</h1>${body}</main></body></html>`
}

/** A command block in the shape `src/lib/copy-control.mjs` writes it. */
function block({ label = 'Copy the command', hidden = ' hidden', status = ' aria-live="polite"', type = ' type="button"', command = '<pre><code>fugaro run</code></pre>', extra = '' } = {}) {
  return `<div class="command-block" data-copy><div class="command-controls"><span class="copy-status" data-copy-status role="status"${status}></span><button${type} class="copy-button" data-copy-button aria-label="${label}"${hidden}>Copy</button></div>${command}${extra}</div>`
}

function failuresOf(html) {
  return copyFailures(html, 'fixture.html')
}

test('a command with a control passes, so each refusal below is the one thing it breaks', () => {
  assert.deepEqual(failuresOf(page(block())), [])
  assert.deepEqual(failuresOf(page(`<div class="command-block" data-copy><div class="command-controls"><span class="copy-status" data-copy-status role="status" aria-live="polite"></span><button type="button" class="copy-button" data-copy-button aria-label="Copy the usage line" hidden>Copy</button></div><code class="usage">fugaro budget set</code></div>`)), [])
  assert.deepEqual(failuresOf(page('<p>no commands here</p>')), [])
})

test('every copy-control refusal fires on the defect it names', () => {
  const cases = [
    ['a command with no control', page('<pre><code>fugaro run</code></pre>'), /is in no \[data-copy\] block/],
    ['a control left visible', page(block({ hidden: '' })), /is not emitted `hidden`/],
    ['a control with no accessible name', page(block({ label: '' }).replace('>Copy</button>', '></button>')), /has no accessible name/],
    ['a name that is only the visible word', page(block({ label: '' })), /accessible name is just "Copy"/],
    ['a name that is not the visible label', page(block({ label: 'Duplicate' })), /does not contain its visible label/],
    ['a control that is not a button', page(block().replace('<button type="button"', '<a href="#"').replace('>Copy</button>', '>Copy</a>')), /is a <a>/],
    ['a submit button', page(block({ type: '' })), /is <button type="">/],
    ['two controls on one command', page(block({ extra: '<button type="button" class="copy-button" data-copy-button aria-label="Copy the command" hidden>Copy</button>' })), /holds 2 copy controls/],
    ['two commands in one block', page(block({ command: '<pre><code>a</code></pre><pre><code>b</code></pre>' })), /holds 2 commands/],
    ['a block with no command', page('<div class="command-block" data-copy><button type="button" class="copy-button" data-copy-button aria-label="Copy the command" hidden>Copy</button></div>'), /holds no command/],
    ['a status that interrupts', page(block({ status: ' aria-live="assertive"' })), /announced politely/],
    ['no status line at all', page(block().replace('<span class="copy-status" data-copy-status role="status" aria-live="polite"></span>', '')), /holds 0 status lines/],
    ['a control inside aria-hidden', page(block().replace('<div class="command-block" data-copy>', '<div class="command-block" data-copy aria-hidden="true">')), /inside aria-hidden/],
    ['a control that copies decoration', page(block({ command: '<pre><code><span aria-hidden="true">$ </span><span aria-hidden="true" data-copy-source>fugaro run</span></code></pre>' })), /copies text inside aria-hidden/],
    ['a block whose control copies something else', page(block({ command: '<pre><code>fugaro run</code></pre><span data-copy-source>a different command</span>' })), /is not part of the command it sits on/],
  ]
  for (const [what, html, expected] of cases) {
    const failures = failuresOf(html)
    assert.ok(failures.some((f) => expected.test(f)), `${what} was not refused (${expected}); the check said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
})

test('the terminal copies its command and not the prompt drawn beside it', () => {
  // The one block that marks its own source. The `$` prompt and the caret are decoration the
  // page marks `aria-hidden`, so the script's fallback — the block's first `pre`/`code` —
  // would put the prompt on the clipboard and the marker is what keeps it off.
  const terminal = (source = ' data-copy-source') =>
    page(
      `<div class="terminal" data-copy><div class="terminal-chrome"><div class="terminal-dots" aria-hidden="true"><span class="terminal-dot"></span></div><div class="command-controls"><span class="copy-status" data-copy-status role="status" aria-live="polite"></span><button type="button" class="copy-button" data-copy-button aria-label="Copy the command" hidden>Copy</button></div></div><pre class="terminal-body"><code><span class="terminal-prompt" aria-hidden="true">$ </span><span class="terminal-command"${source}>fugaro run "add a flag"</span><span class="terminal-caret" aria-hidden="true"></span></code></pre></div>`,
    )

  // As the component writes it, the block passes — and what the script resolves is the command
  // span, not the `<pre>` that holds the prompt.
  assert.deepEqual(failuresOf(terminal()), [])
  const block_ = [...walk(parse(terminal()))].find((el) => el.attrs.has('data-copy'))
  assert.equal(sourceOf(block_).attrs.get('class'), 'terminal-command')

  // Without the marker the fallback is the `<pre>`, whose text includes the prompt: that is a
  // real difference in what a reader gets, so the check has to see it. It refuses the variant
  // below, where the marker sits on the decoration instead of beside it.
  assert.match(failuresOf(terminal(' data-copy-source aria-hidden="true"')).join('\n'), /copies text inside aria-hidden/)
})
