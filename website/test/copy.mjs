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
//     block, a code block of the rendered CLI reference, the slider's `fugaro watch` snippet)
//     or a `code.usage` (the caps guide's usage lines, which are commands but not `<pre>`).
//     A line that is not a command a reader runs — the hero's prompt line is the one on the
//     homepage — is not a command block at all and carries none (changes spec of
//     2026-10-18, item 3).
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
//   - **What the script would copy is the command it sits on.** The script copies the block's
//     first `pre`/`code` — the element the command is written in — so that is what this file
//     resolves, for every block and for every page. There is no marker for a block to name a
//     source of its own: every command the site prints is the whole of what its block copies,
//     and decoration drawn beside a command (a prompt) is what this file refuses below.
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
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fakeDOM, SCRIPT_SOURCE } from './lib/dom.mjs'
import { DIST, HTMLScanError, REPO, builtPages, parse, textOf, walk } from './lib/html.mjs'

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
  return [...walk(block)].find((el) => el.name === 'pre' || el.name === 'code') ?? null
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
      fail(`the copy control would copy ${JSON.stringify(source ? words(source).slice(0, 60) : '')}, which is not part of the command it sits on (${JSON.stringify(text.slice(0, 60))}): the script copies the block's first pre/code, and the control has to be on the command it copies`)
    }
    // And nothing inside what it copies may be decoration either: the copied element is the
    // command, so a prompt drawn beside one would go on the clipboard with it — and every line
    // after the command too, if the prompt shares the block's `<pre>`.
    if (source && [...walk(source)].some((el) => el.attrs.get('aria-hidden') === 'true' && textOf(el).trim() !== '')) {
      fail(`the copy control copies ${JSON.stringify(words(source).slice(0, 60))}, which holds decoration the page marks aria-hidden (a prompt): what the block copies is the command, so the decoration belongs outside the block the control sits on`)
    }
    if (source && ariaHidden(source)) {
      fail('the copy control copies text inside aria-hidden: the script puts that text on the clipboard, and the page tells a screen reader it is decoration — a prompt beside a command is marked this way, and the source is the command beside it rather than the prompt')
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
    ['a control that copies decoration', page(block({ command: '<pre><code><span aria-hidden="true">$ </span>fugaro run</code></pre>' })), /holds decoration the page marks aria-hidden/],
    ['a command the page tells a screen reader is not there', page(block().replace('<pre>', '<pre aria-hidden="true">')), /copies text inside aria-hidden/],
    ['a block whose first code element is not its command', page(block({ command: '<code>a stray span</code><pre><code>fugaro run</code></pre>' })), /is not part of the command it sits on/],
  ]
  for (const [what, html, expected] of cases) {
    const failures = failuresOf(html)
    assert.ok(failures.some((f) => expected.test(f)), `${what} was not refused (${expected}); the check said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
})

test("the homepage offers `fugaro watch` in one block with one control", () => {
  // Item 3's shape, held on the built page rather than on a fixture: the homepage's one command
  // block besides the install block is the watch block, and it holds the command and nothing
  // else — the note under it is outside the block, so what the control copies is the command a
  // reader runs rather than the sentence describing it. The block is the slider's third panel
  // since the owner's approved draft of 2026-10-19; the chat's status line, which this case also
  // held out of any `[data-copy]` block, left the site with `AgentChat.astro`.
  const html = readFileSync(path.join(DIST, 'index.html'), 'utf8')
  const page = parse(html)
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/)
  const watch = [...walk(page)].find((el) => classes(el).includes('watch-block'))
  assert.ok(watch, 'the homepage carries no watch block, and the panel offers `fugaro watch` as its one command besides the install block (changes spec 2026-10-18, item 3)')
  assert.ok(watch.attrs.has('data-copy'), 'the watch block is in no [data-copy] block')
  assert.equal(words(sourceOf(watch)), 'fugaro watch', `the watch block's control would copy ${JSON.stringify(words(sourceOf(watch)))}, and the block holds the command and nothing else`)
  assert.deepEqual(failuresOf(html), [], 'the built homepage breaks the copy control\'s rule')
})

// ---- the script itself (P3, P6, D183) -----------------------------------------------------
//
// The cases above hold the markup to the rule the script uses; the cases below run the script.
// There is no browser here, so the document is the built page parsed by `lib/html.mjs` and
// wrapped in the handful of DOM calls `site.js` makes — enough to say what it copies, what it
// reveals and where it leaves the focus, and nothing that pretends to be a layout engine. What
// a real clipboard does with a real click is still the owner's 70c.
//
// **The stub is `lib/dom.mjs`'s**, because this file is no longer the only gate that runs the
// script: `console-screens.mjs` runs the same file for the slider's arrows, count and autoplay.
// One harness for both is one answer to "what does the script do with this markup".

const flush = () => new Promise((resolve) => setImmediate(resolve))

test('site.js names nothing that evaluates, writes markup or fetches', () => {
  const code = SCRIPT_SOURCE.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '')
  const banned = [
    [/\beval\s*\(/, 'eval'],
    [/\bFunction\s*\(/, 'the Function constructor'],
    [/innerHTML|outerHTML|insertAdjacentHTML|createContextualFragment/, 'a markup sink'],
    [/document\.write/, 'document.write'],
    [/\bfetch\s*\(|XMLHttpRequest|sendBeacon|WebSocket|EventSource|\bimport\s*\(/, 'a request'],
    [/setTimeout\s*\(\s*['"`]/, 'a string timer'],
    [/:\/\/|sessionStorage|indexedDB|\bcaches\b/, 'a URL or storage other than the consent decision'],
  ]
  for (const [pattern, what] of banned) assert.ok(!pattern.test(code), `public/scripts/site.js uses ${what}: the site's one script copies text, closes menus and runs the consent gate, and nothing else (D183, W3.2)`)

  // `document.cookie` used to be refused outright, because the site set no cookie of its own.
  // The owner's ruling of 2026-10-18 gives the withdrawal a second duty — deleting the cookies
  // GA4 set while the visitor had accepted — so the file touches cookies again, and only to
  // delete them: every write carries the expiry that removes a cookie, and a write that left
  // one behind is the defect this rule refuses. `consent.mjs` runs the file and reads the jar
  // it leaves; this is the same promise said in the source a reader reads.
  const cookieWrites = [...code.matchAll(/document\.cookie\s*=[^\n]*/g)].map((m) => m[0])
  assert.ok(cookieWrites.length > 0, 'site.js no longer touches document.cookie, so the withdrawal cannot delete the cookies GA4 set (owner\'s ruling, 2026-10-18)')
  for (const write of cookieWrites) {
    assert.ok(
      /EXPIRED/.test(write),
      `site.js writes document.cookie without the expiry that removes it (${write.trim()}): a withdrawal deletes the cookies GA4 set and sets none of its own`,
    )
    // A write with no `Path` lands on the page's own directory rather than on `/`, so it would
    // not match a cookie the tag set at `/` and the deletion would silently remove nothing.
    assert.ok(
      /path=\//i.test(write),
      `site.js writes document.cookie without Path=/ (${write.trim()}): the tag's cookies are set at /, and an expiry on another path removes none of them`,
    )
  }

  // The storage this file touches beyond that is the consent decision, and it names no key of
  // its own: the key is an attribute of the banner's markup, so the script cannot store under
  // a key the page does not carry, and a flag-off artifact has nothing to store under.
  const keys = [...code.matchAll(/localStorage\.(?:get|set|remove)Item\s*\(\s*([^,)]*)/g)].map((m) => m[1].trim())
  assert.ok(keys.length > 0, 'site.js no longer reads or writes the consent decision at all')
  for (const key of keys) {
    assert.ok(
      /^banner\.getAttribute\(|^key$/.test(key),
      `site.js stores under the literal ${key}: the consent key is the banner's own attribute, so the script names no key of its own`,
    )
  }
})

test('the same file that reveals the controls loads nothing from Google until the visitor chooses', () => {
  // The gate is the third duty of this one file, so the harness that runs it for the copy
  // control runs it for the gate too — a second stub, built a different way, reaching the
  // same conclusion as `consent.mjs`. Both matter: this one is what a reader would get if the
  // banner's own controller had stayed in the markup, and it is the reading that says the
  // three duties do not interfere.
  const html = builtPages(DIST).find((page) => page.rel === 'index.html').html

  const undecided = fakeDOM(html)
  assert.deepEqual(undecided.appended, [], 'site.js appended the analytics tag before the visitor chose')

  // The id and the origin are read from the artifact's own configuration rather than written
  // here: the property lives in site.yaml and the origin in the banner's markup, and a
  // literal in this file would be a second copy of each (W3.2).
  const banner = parse(html)
  const config = [...walk(banner)].find((el) => el.attrs.has('data-analytics-id'))
  assert.ok(config, 'the shipped page carries no analytics configuration for the gate to read')
  const id = config.attrs.get('data-analytics-id')
  const origin = config.attrs.get('data-analytics-origin')

  const accepted = fakeDOM(html)
  accepted.choose('granted')
  assert.deepEqual(
    accepted.appended.map((el) => el.src),
    [`${origin}/gtag/js?id=${id}`],
    'accepting did not append exactly the one analytics tag, from the one admitted origin',
  )

  const refused = fakeDOM(html)
  refused.choose('denied')
  assert.deepEqual(refused.appended, [], 'site.js appended the analytics tag after the visitor declined')

  const remembered = fakeDOM(html, { storage: { 'fugaro:analytics-consent': 'denied' } })
  assert.deepEqual(remembered.appended, [], 'a stored refusal still loaded the analytics tag')
})

test('site.js reveals every control and copies exactly the command each one sits on', async () => {
  const readme = readFileSync(path.join(REPO, 'README.md'), 'utf8')
  let pages = 0
  for (const { rel, html } of builtPages(DIST)) {
    const page = fakeDOM(html)
    const blocks = page.find('[data-copy]')
    if (blocks.length === 0) continue
    pages++
    for (const block of blocks) {
      const button = block.querySelector('[data-copy-button]')
      assert.equal(button.hidden, false, `${rel}: site.js left a copy control hidden on a browser with a clipboard`)
      const command = block.querySelector('pre, code')
      const before = page.written.length
      button.dispatch('click')
      await flush()
      assert.equal(page.written.length, before + 1, `${rel}: clicking a copy control wrote nothing to the clipboard`)
      const copied = page.written[before]
      assert.equal(copied, command.textContent, `${rel}: the control copied ${JSON.stringify(copied)} rather than the command it sits on`)
      // One command: a single line, or lines joined by the shell's own `\` continuation.
      const lines = copied.trim().split(/\r?\n/)
      assert.ok(copied.trim() !== '' && lines.slice(0, -1).every((line) => line.endsWith('\\')), `${rel}: the control copied ${JSON.stringify(copied)}, which is not one command`)
      assert.ok(!/^\s*[$>#]\s/.test(copied), `${rel}: the control copied ${JSON.stringify(copied)}, prompt included — a pasted prompt is a command that fails`)
      assert.equal(block.querySelector('[data-copy-status]').textContent, 'Copied', `${rel}: a copy that happened was not announced`)
    }
    if (rel === 'index.html') {
      // The panel's one command besides the install block, clicked the way a reader clicks it
      // (changes spec 2026-10-18, item 3): what lands on the clipboard is the command, and it
      // is a command README.md prints.
      const watch = page.find('.watch-block')[0]
      watch.querySelector('[data-copy-button]').dispatch('click')
      await flush()
      const copied = page.written.at(-1)
      assert.equal(copied, 'fugaro watch', `the watch snippet's control copied ${JSON.stringify(copied)}: it copies the command the reader is asked to run, not the note beside it`)
      assert.ok(readme.includes(copied), `the watch snippet's control copied ${JSON.stringify(copied)}, which is not the command README.md prints`)
    }
  }
  assert.ok(pages >= 3, `site.js ran on ${pages} pages with commands; finding 13 measured three`)
})

test('site.js leaves the controls hidden with no clipboard, and says so when a copy is refused', async () => {
  const html = builtPages(DIST).find((p) => p.rel === 'index.html').html
  const none = fakeDOM(html, { clipboard: 'absent' })
  for (const button of none.find('[data-copy-button]')) assert.equal(button.hidden, true, 'site.js revealed a copy control on a browser with no clipboard to write to (D183: no broken control)')

  const refused = fakeDOM(html, { clipboard: 'refuse' })
  const block = refused.find('[data-copy]')[0]
  block.querySelector('[data-copy-button]').dispatch('click')
  await flush()
  assert.match(block.querySelector('[data-copy-status]').textContent, /failed/i, 'a refused copy was reported as nothing, or as a copy')
  // And the message clears itself, so a later reader is not told about an old copy.
  const timer = refused.timers.at(-1)
  assert.ok(timer && timer.ms > 0, 'the copy status sets no timer to clear itself')
  timer.fn()
  assert.equal(block.querySelector('[data-copy-status]').textContent, '', 'the copy status did not clear itself')
})

test('Escape and a click outside close the menu, and the focus goes back to a toggle that is drawn', () => {
  const html = builtPages(DIST).find((p) => p.rel === 'docs/index.html').html

  // A group open, the focus on a link inside it — the one level the menu has, at every width,
  // since the owner's brief of 2026-10-19 took the drawer in front of the groups away.
  const page = fakeDOM(html)
  const group = page.find('.site-nav-group details')[0]
  group.open = true
  group.querySelector('a').focus()
  page.document.dispatch('keydown', { key: 'Escape' })
  assert.equal(group.open, false, 'Escape left a group open')
  // Compared by identity and asserted as a boolean: the elements wrap whole parsed pages,
  // and a failing deep comparison would print them.
  assert.ok(
    page.document.activeElement === group.querySelector('summary'),
    "Escape inside a group did not give the focus back to its own trigger: the list is the <details>'s child, and a focus left in it is a focus on something no longer drawn",
  )

  // A key that is not Escape does nothing; a click inside the menu does nothing; outside closes.
  group.open = true
  page.document.dispatch('keydown', { key: 'Tab' })
  assert.equal(group.open, true, 'a key other than Escape closed the menu')
  page.document.dispatch('click', { target: group.querySelector('summary') })
  assert.equal(group.open, true, 'a click inside the menu closed it')
  page.document.dispatch('click', { target: page.find('main')[0] })
  assert.equal(group.open, false, 'a click outside the menu left it open')

  // And the trigger it hands the focus to is one that is drawn: a reader whose group closed
  // while the label was not rendered is left where they were rather than moved to a control
  // they cannot see. The case says which elements are not drawn, which is how the script's own
  // question — `getClientRects().length` — is answered here.
  const hidden = fakeDOM(html, { undrawn: (node) => node.name === 'summary' })
  const hiddenGroup = hidden.find('.site-nav-group details')[0]
  hiddenGroup.open = true
  hiddenGroup.querySelector('a').focus()
  hidden.document.dispatch('keydown', { key: 'Escape' })
  assert.equal(hiddenGroup.open, false, 'Escape left a group open')
  assert.ok(
    hidden.document.activeElement !== hiddenGroup.querySelector('summary'),
    'the focus was handed to a trigger that is not drawn, so the reader has been moved to a control they cannot see',
  )
})

// The button is an icon that becomes a checkmark for a moment (the owner's feedback of
// 2026-10-24); "Copied" is still announced, to a screen reader only.
test('a copy turns the clipboard into a checkmark for a moment and announces Copied', async () => {
  const html = builtPages(DIST).find((p) => p.rel === 'index.html').html
  const page = fakeDOM(html)
  const block = page.find('[data-copy]')[0]
  const button = block.querySelector('[data-copy-button]')
  assert.equal(button.textContent.trim(), '', 'the copy button shows a word, and it is an icon')
  assert.equal(button.querySelectorAll('svg').length, 2, 'the copy button does not carry its clipboard and its checkmark')
  assert.ok((button.getAttribute('title') || '').length > 0, 'the icon button has no tooltip')
  assert.equal(button.getAttribute('data-copied'), 'false', 'the button starts as a checkmark')
  button.dispatch('click')
  await flush()
  assert.equal(button.getAttribute('data-copied'), 'true', 'a copy did not turn the button into a checkmark')
  const status = block.querySelector('[data-copy-status]')
  assert.equal(status.textContent, 'Copied', 'a copy was not announced')
  assert.equal(status.getAttribute('data-tone'), 'ok', 'the announcement of a copy is drawn as well as the checkmark')
  const back = page.timers.find((timer) => timer.ms === 1600)
  assert.ok(back, 'the checkmark sets no timer to turn back')
  back.fn()
  assert.equal(button.getAttribute('data-copied'), 'false', 'the checkmark did not turn back into a clipboard')
})
