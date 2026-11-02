// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Every screen's `literal` writes the console's own vocabulary and never a
// document's string (design section 5.1 rule 7; desktop-console plan Task 2.3).
//
// `literal(element, text)` sets `textContent` with no sanitizer between the
// argument and the screen. That is right for the console's own words and wrong
// for anything a document, the CLI's stderr or a person chose: those go through
// `putText` (`src/renderer/app.ts`), which redacts a token-shaped run and strips
// control and invisible characters. The review of Task 2.3 found the same defect
// four times -- `logs[].severity`, `row.status`, `job.status` and a secret
// label's *key* -- each a wire string handed to `literal` because it looked like
// vocabulary. A test per call site catches the instance it names and not the
// fifth one, so this one reads the screens themselves:
//
//   - every argument a screen hands to a `literal` sink must be a string
//     literal, a template with no interpolation, a module constant
//     (`SCREAMING_CASE`, declared `const` at the top of the same file), or a
//     ternary between two string literals;
//   - a drawer that hands its text on instead of writing it -- `shared/version.ts`
//     composes the version-skew notice and `shared/notice.ts` writes it -- is
//     held to the same rule one step back, at the properties that carry the
//     text (`specTextValues` below), because a parameter is all the scan over
//     sinks can see;
//   - anything else must be on `CLOSED_VOCABULARY` below, by file and by its
//     exact source text, with the reason it is the console's own word (a table
//     the console wrote, or a word a document sent that the model already
//     filtered against a closed list). An entry nothing matches any more fails
//     the test too, so the list cannot rot into a blanket permission.
//
// A scan of `literal(` alone is fooled by every way of writing to the screen
// that does not spell `literal(`, so the scan also holds:
//
//   - a text slot (`textContent`, `innerText`) is assigned only inside the
//     `literal` helper's own body; markup slots (`innerHTML`, `outerHTML`,
//     `insertAdjacentHTML`) and `setAttributeNS` are not used at all;
//   - a sink is only ever *called* by its own name: an alias, a destructuring
//     or a sink handed on as a value is an offence, because the call through
//     the other name is a call this scan cannot see;
//   - an attribute write (`setAttribute`, `dataset`, `title`, `className`, `id`)
//     carries a constant shape or an entry on `ATTRIBUTE_VOCABULARY`;
//   - an entry can be scoped to one function's body (`within`), so the `note`
//     helper's own parameter is allowed there and nowhere else;
//   - **no module under `src/renderer` writes an inline style** (Task 6.2): the
//     `.style` property in any spelling, computed keys included, and
//     `setAttribute('style', ...)` are offences with no vocabulary behind them,
//     because `style-src 'self'` backs no inline style at all. That block reads
//     the whole `src/renderer` tree rather than the screens, and says so.
//
// **The spellings the second scan is closed against**, named here because they
// are the review of Task 2.3's finding and each one was a way past the first
// version of this file: a text slot or attribute written through a **computed
// key** (`el['textContent'] = x`, `el['title'] = x`), read or called through one
// (`el['setAttribute']('data-repo', x)`), the library spellings of a property
// write (`Object.assign(el, {textContent: x})`, `Object.defineProperty`,
// `Reflect.set`), the **optional call** `el.setAttribute?.(...)`, and the writer
// taken off the element and applied (`el.setAttribute.call(el, ...)`). Every one
// of them is an offence with no vocabulary list behind it: the screens write a
// slot by name, through `literal` or through `putText`, and a screen that needs
// one of these must come here and say why.
//
// The mutation the test is proven against is the defect itself: put
// `literal(..., label.key)` back into `projects.ts` and the first case fails
// naming it (`MUTATION` below runs the same scan over a doctored copy), and one
// doctored copy per way around the `literal(` scan.

import { mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, relative, sep } from 'node:path'

import { describe, expect, it } from 'vitest'

const SCREENS = join(__dirname, '..', '..', 'src', 'renderer', 'screens')

/** SHARED is the other directory that may draw: `src/shared` holds the notice
 * card (`notice.ts`), which writes text slots, and the module that composes the
 * version-skew notice's text for it (`version.ts`). A module added there with a
 * `literal` of its own would be a place this scan exists for. */
const SHARED = join(__dirname, '..', '..', 'src', 'shared')

/** RENDERER is the whole renderer tree. The `literal` scan above reads the
 * screens and the shared modules, because those are the files that draw text;
 * the inline-style scan below reads **every** module under `src/renderer`,
 * because a `.style` write is an offence wherever it is written -- a helper
 * outside `screens/` is a module the screens call, and a scan that read only the
 * screens would let one arrive there (Task 6.2; design section 5.5). */
const RENDERER = join(__dirname, '..', '..', 'src', 'renderer')

/** SINKS are the functions whose argument is written to the screen unsanitized,
 * with the index of that argument. `note` is `projects.ts`'s wrapper around
 * `literal`, so its call sites are sinks of their own. */
const SINKS: Readonly<Record<string, number>> = Object.freeze({ literal: 1, note: 2 })

/** Entry is why an argument is the console's own word: a reason, or a reason
 * that holds only inside one function's body (`within`). */
type Entry = string | { readonly within: string; readonly reason: string }

/**
 * CLOSED_VOCABULARY is every non-constant argument a screen hands a sink, with
 * why it is the console's own word. Keyed by file, then by the argument's exact
 * source text.
 */
const CLOSED_VOCABULARY: Readonly<Record<string, Readonly<Record<string, Entry>>>> = Object.freeze({
  'degraded.ts': {
    'state.installCommand': '`FUGARO_INSTALL_COMMAND` in `src/source/cli.ts`, which is the README\'s own `brew install dimipaun/tap/fugaro` rather than a string a document chose',
    'drawnOrNothing(state.operation)': '`OperationName`, the closed IPC allow-list union: the read the console asked for, in the console\'s own words, and passed through `sanitizeRefusal` again in `refused()` because `RefusedState` can be built without `refusedState`',
    'sentence.ok ? sentence.text : REFUSAL_UNREADABLE':
      'the CLI\'s refusal sentence on the `SourceRefusal` channel (`src/source/source.ts`), not a document string, **through `sanitizeRefusal`** in `refusedState` and again in `refused()` (a state built without `refusedState` is still sanitized; idempotent): design section 6.3 requires it verbatim, the free-text rules would draw `roles/datastore.viewer` as `[redacted]` and the project as `<project>`, and the credential rules still redact a token a child echoed -- `degraded.test.ts` asserts both halves of that',
  },
  'live.ts': {
    'flag.name': 'a RunFlag is built only from live.ts\'s own flag table (`runFlags`); an unknown health or deadline is the console\'s "unknown ..." flag, never the document\'s word',
    UNAVAILABLE_MARK:
      'the console\'s own `n/a` (`src/shared/availability.ts`), the mark a value with no number is drawn as. An actor the document left out of a run is drawn as the mark and never as an empty cell, and the mark is written here rather than through `putReading` because an actor is a string and not a reading: the `unit` `putReading` takes is a claim about how a number is formatted, and §2.10 scopes Live\'s one `count` reading to the review inbox (`docs_console_styling_test.go` holds that sentence)',
    caption: {
      within: 'fleetStatCell',
      reason:
        'the parameter of the `fleetStatCell` helper itself; every call site passes one of this file\'s own caption constants (`FLEET_BURN_WORD`, `FLEET_ACTIVE_WORD`, `FLEET_QUEUED_WORD`)',
    },
    'account.word': {
      within: 'fleetLegendRow',
      reason:
        'a `FleetSegment`\'s `word`, which this file assigns from its own table: `fleetSegments` gives a segment the money word of the account it draws (`FLEET_SPENT_WORD`, `FLEET_LEASED_WORD`, `FLEET_HEADROOM_WORD`) and `fleetLegend` builds its rows the same way -- the model carries dollars and states, never a word',
    },
    '`#${String(row.prNumber)}`':
      'a ready run\'s pull request number, filtered by `prNumberAt` to a positive integer (`schema.ts`\'s `optionalInt` guarantees a finite number and not a whole one) -- the same rule and the same entry `run.ts` keeps for `#${String(model.number)}`, and the review inbox draws the fact the same way: as text, this screen\'s own mark in front of a number the document carried',
    '`${LIVE_REVIEW_TITLE} (${formatReading(measured(model.reviewCount), \'count\')})`':
      'the review inbox\'s heading, and the interpolation is the count and nothing else: `LIVE_REVIEW_TITLE` is a console constant (the CLI\'s own words, `readyForReview` in `internal/watch/render.go`) and `reviewCount` is a number this console counted off the frame (`buildLiveModel`: `review.length`), drawn through `formatReading`, the only formatter, as `measured` because a frame that carried no `ready` list at all is refused by `schema.ts` before a screen sees it -- so no document *string* reaches the sink, which is why the heading is one text run rather than four slots (spend.ts\'s `note` is the same shape: a console sentence with a count the console computed)',
    word: {
      within: 'modeChip',
      reason:
        '`rigorModeWord(rigor.mode)`, which answers one of the console\'s five mode words or `null`; `null` is the branch that hands the document\'s own value to `putText` (the `label` class, the `runs[].rigor.mode` field), so only the console\'s word reaches the sink',
    },
  },
  'spend.ts': {
    'entry.dimension': 'the switch is `SPEND_DIMENSIONS.map(...)`: the eight words are the console\'s table, and an unknown document dimension is drawn as "an unknown breakdown"',
    'people.viewName': '`peopleViewOf` maps the document\'s `view` through `PEOPLE_VIEW_NAMES` and draws `UNKNOWN_PEOPLE_VIEW` for a word it does not know, so the document\'s own word is never drawn',
    'people.reason ?? NO_PEOPLE_READ': 'the console\'s own sentence for a by-person panel with no document: `peopleOf`\'s or `readSpend`\'s reason, each a console constant or the window sentence, or the CLI\'s refusal **through `sanitizeRefusal`** (`refusalSentence`), the treatment `degraded.ts` gives a refusal',
    sentence: {
      within: 'peoplePanel',
      reason: '`people.sentences` holds only sentences `peopleOf` composed from this file\'s own constants plus counts it read; the document\'s own `notes` take the `putText` branch instead',
    },
    'series.title': '`ACCOUNT_TITLES[account]`, the console\'s headings',
    'scope.name': '`scopeOf` maps the wire word through `SCOPES` and draws `UNKNOWN_SCOPE_NAME` for a word it does not know',
    word: {
      within: 'capFigure',
      reason:
        'the caps panel\'s own word for what a figure is (`CAPS_BILLED_WORD` and its neighbours, this file\'s constants: the panel draws no column headers, so every figure carries one), and the caller passes those constants and nothing else',
    },
    note: 'model.notes holds only the console\'s own sentences (`buildSpendModel`), the one interpolation being a count the console computed',
    'series.empty': "the console's own sentence for an account with no point to draw, written by `buildSeries`/`withheldSeries` -- or `null`, the branch that draws no cell at all",
  },
  'projects.ts': {
    text: { within: 'note', reason: 'the parameter of the `note` helper itself; its call sites are checked as a sink (SINKS)' },
    'model.secrets.note': '`SECRETS_ARRAY_NOTE`, a console constant',
    'panel.title': 'every panel constructor is called with a string literal title',
    'panel.source': '`PROJECTS_SOURCE_LINES[operation]`, a named console source line, or `fugaro ${operation}` over the closed `OperationName` union',
    'row.scope': '`ProjectCapScope`, a closed union the model assigns (`global`, `defaults`, `repo`)',
    'row.dailyCapSource': '`capSourceAt` keeps a word only when it is on `CAP_SOURCES`, else `unknown`',
    'row.perRunCapSource': '`capSourceAt` keeps a word only when it is on `CAP_SOURCES`, else `unknown`',
    'panel.because':
      'every panel constructor draws its reason through `sanitizeRefusal` (`drawnRefusal`) before it is stored, or it is one of the two console constants (`POLICY_BECAUSE`, `ENROLLMENT_BECAUSE`); a document\'s own `note` takes the `putText` branch instead (`becauseKind`)',
  },
  // The drawers outside `src/renderer/screens`: `src/shared/notice.ts` writes
  // the notice card's text slots, and `src/shared/version.ts` composes the
  // version-skew notice's text and hands it to that card (Task 3.2). Both live
  // in `shared/` because the main process runs the handshake with them and a
  // screen is not the only thing that writes to the console's document. They are
  // keyed by the path `join(SCREENS, ...)` resolves, which is how every read in
  // this file finds them.
  '../../shared/notice.ts': {
    'spec.title':
      'the card\'s title, which is the caller\'s own text: the skew notice passes `SKEW_HEADLINES[mode]` (this console\'s heading table) and every later caller passes a constant of its own',
    'spec.reason':
      'the card\'s reason. The version-skew notice passes `noticeReason(compatibility)`, which answers one of this console\'s own `NOTICE_REASONS` sentences -- no version, range or command reaches it -- and the reasons are the caller\'s own words for the same reason the title is',
    'item.text':
      'a list value\'s row, and the caller builds the list: the skew notice passes `HIDDEN_VIEW_WORDS[view]`, this console\'s word for a withheld view, keyed by the closed `ReducedView` union',
    'detail.label':
      'a fact\'s `dt`, the console\'s own word for what the row is ("what happened", "why", "withheld"), written by the caller as a string literal',
    'detail.text':
      'a fact\'s `dd`, which is the caller\'s own string: the skew notice passes `skewSentence(compatibility)` (composed from this console\'s constants), `versionCell(...)`, which is `versionText` -- the version line **through `sanitizeText`** (the `label` class), answering this console\'s `UNREADABLE_VERSION` when the sanitizer refuses it -- or `unreadableReason(...)`, the reader\'s reason **through `sanitizeText`** (the `sentence` class), or `null`, which is the branch that creates no row at all. The raw line a binary printed reaches none of them, and the spec-text case below is what says so',
  },
  'graph.ts': {
    'value.text': {
      within: 'drawInto',
      reason:
        'the sanitizer\'s own answer, which is the one document-shaped string this screen writes: a `Sanitized` value is built in `graph/model.ts` by `sanitizeText` with each field\'s own class and wire path, and `sanitizeText` refuses anything that is not a string -- so this is `version.ts`\'s `versionText` case ("a drawer may write a sanitizer\'s own answer, and it may write nothing else"), and `drawInto` is the only writer of one',
    },
    UNAVAILABLE_MARK: 'the console\'s own unavailable mark (`app.ts`), the other half of `drawInto`\'s pair of outcomes -- the same constant `version.ts` and `projects.ts` draw a refused value as',
    word: {
      within: 'markWord',
      reason:
        '`GraphNode.words`, the model\'s own `StateWord` list: `stateWords` is one exhaustive function over the closed `PlanState` union and answers a word from `STATE_WORDS` (plus `critical`), so no document string is ever a word -- the document\'s own stage and title reach the screen through `drawInto`, never through this sink',
    },
    reason: {
      within: 'tooltip',
      reason:
        'the sentence a gesture that does nothing is owed: `doorFor`\'s `because` (this console\'s own reason for a suffix no run ends with, or two runs ending with one) or `GRAPH_PR_NO_LINK_REASON`, both written in this file',
    },
    'GRAPH_VIEW_LABELS[picture.state.view]':
      '`GRAPH_VIEW_LABELS`, this file\'s own table of what each drawing is called, keyed by the closed `GraphViewName` union (`GRAPH_VIEWS`)',
    'notShownNote(picture.model.notShown)':
      '`notShownNote` composes the sentence from `GRAPH_NOT_SHOWN_SINGULAR`/`GRAPH_NOT_SHOWN_PLURAL` and a count the model computed (`buildGraphModel`\'s `notShown`), so a document string is in neither half of it',
    'barText(release)':
      '`barText` composes "n of m done" from two `formatReading` readings -- the console\'s only formatter, whose unavailable mark is what a release the document gave no counters reads -- and the words around them are this file\'s',
    'barText(box.release)': '`barText`, the same "n of m done" run the chip and the list heading draw, so one rule reads all three',
    line: {
      within: 'nodeCard',
      reason:
        'one line of `titleLines(node.title)`, the model\'s own `Sanitized` title wrapped to the lines a card has and cut only past them: the value has been through `sanitizeText` in `graph/model.ts`, a refusal is the console\'s `UNAVAILABLE_MARK`, and every cut is `[...text]` so a surrogate pair is never split',
    },
    '`#${String(node.prNumber)}`':
      'a pull request number the model read as a positive integer (`prNumberAt`), drawn as **text**: design section 5.1 rule 2 and its gap 7 leave the console no URL to build, so the number is the whole of it and its `title` carries `GRAPH_PR_NO_LINK_REASON` -- the treatment `live.ts` and `run.ts` give the same field',
    '`run …${node.run.text}`':
      'the run suffix as the model\'s `Sanitized` `run` value, which `suffixRefusal` refuses unless it is four characters, under this file\'s own "run …" label -- so the interpolation is a sanitized four-character suffix and never a run id or a path',
    'door.because': {
      within: 'inspector',
      reason:
        '`doorFor`\'s own sentence for a task whose run nothing opens -- the same sentence the card carries in a tooltip, drawn in the panel as its run line because the panel is text and a tooltip is not',
    },
    label: {
      within: 'fact',
      reason:
        'the `fact` helper\'s own label parameter; every call site passes one of this file\'s four fact words (`GRAPH_STREAM_FACT`, `GRAPH_RUN_FACT`, `GRAPH_DEPENDS_ON`, `GRAPH_UNBLOCKS`)',
    },
    spelling: {
      within: 'legendItem',
      reason:
        'one line of the legend: a word of `GRAPH_LEGEND_WORDS` (`graph/model.ts`\'s own `STATE_WORDS`, which is one exhaustive function over the closed `PlanState` union, less the unreachable `stuck`), or `GRAPH_DEPENDS_ON`/`GRAPH_CRITICAL_PATH` for the two kinds of line -- this file\'s own vocabulary and never a document string',
    },
  },
  'run.ts': {
    UNAVAILABLE_MARK:
      'the console\'s own `n/a` (`src/shared/availability.ts`): an absent repository, workflow, recipe, branch, account, outcome or stage on the run is drawn as the mark and never as an empty cell, and the mark is written here rather than through `putReading` because each of those is a string and not a reading -- the `unit` `putReading` takes is a claim about how a number is formatted (the same reason `live.ts` names)',
    'stage.deadline': '`deadlineState` keeps a word only when it is on `DEADLINE_STATES`, else null',
    '`#${String(model.number)}`': 'a pull request number the model read as a finite integer (`prNumberOf`)',
    'model.because': 'one of two console sentences (`PR_NO_LINK_REASON` or the no-pull-request one)',
    'model.linkBecause': '`LOG_NO_LINK`, a console constant',
    known: {
      within: 'rigorWord',
      reason:
        'the answer of the vocabulary the caller passed in (`rigorModeWord` or `rigorLayerWord`, both `shared/rigor.ts`), which is one of the console\'s own words or `null`; `null` is the branch that hands the document\'s own mode or layer to `putText` as text',
    },
  },
  'coordinator.ts': {
    word: {
      within: 'numberCell',
      reason:
        'the parameter of the `numberCell` helper itself; every call site passes one of this file\'s own count words (`COORDINATOR_RECORDS_WORD`, `COORDINATOR_IN_FLIGHT_COUNT_WORD`, `COORDINATOR_COLLISIONS_WORD`), so no document string reaches the sink',
    },
  },
  // The two drawings console 0.116.1 adds: the panel's reading placeholder and
  // the Graph's no-checkout card. Neither is handed a document string that
  // reaches a sink -- the card's one is the CLI's refusal sentence, which
  // `sanitizeRefusal` has already drawn -- and the entries below say so.
  'reading.ts': {
    '`${READING_LEAD} ${READING_WORDS[tab]}…`':
      'the placeholder\'s one sentence: `READING_LEAD` is this file\'s own word and `READING_WORDS` is this file\'s own table of the navigation\'s five labels, keyed by the closed `ReadingTab` union the entry passes -- the tab is the console\'s own view id and never a value a document or a child process chose (`entry-wiring.test.ts` asserts the table against the labels the navigation itself drew)',
  },
  'nocheckout.ts': {
    'drawn.ok ? drawn.text : REFUSAL_UNREADABLE':
      'the CLI\'s refusal sentence on the `SourceRefusal` channel (`src/source/source.ts`), not a document string, **through `sanitizeRefusal`** -- the same text and the same treatment `degraded.ts` gives it (design section 6.3 requires the remedy verbatim, a token a child echoed is still redacted), with `REFUSAL_UNREADABLE` for a sentence the sanitizer refuses, so the disclosure is never empty',
    '`${NO_CHECKOUT_AUTO_SENTENCE} ${checkoutLabel(only)}`':
      'this file\'s own sentence and **`checkoutLabel` of one entry of `row.checkoutChoices`**: that function\'s answer is a sanitizer\'s own result for the CLI\'s repository identity and directory (or this console\'s marker for a string it refused), which is the one document-shaped string a drawer may write -- the `launch.ts` `refusalText(recorded)` entry is the same case. The label names the directory the Graph is reading, which a bare "one checkout" would leave the operator to look up',
  },
  // The launch control (V2.5.1b) is the one screen that writes a string it was
  // also handed by a document -- the repository identities main recorded -- and
  // the entries below say where each of its arguments came from.
  'launch.ts': {
    'refusalText(recorded)': {
      within: 'options',
      reason:
        'the **drawn label** of one `<option>`, which is `refusalText` of an entry of `view.repositories`: a sanitizer\'s own answer (or this file\'s marker for one it refused), which is the one document-shaped string a drawer may write -- the `graph.ts` `drawInto` entry is the same case. The entry of `view.repositories` itself is **raw** (`entry.ts`\'s `railRows` keeps the identities main matches against its own records as they came), so the raw string is never drawn: it is written into the option\'s `value`, where the attribute vocabulary below accounts for it',
    },
    text: {
      within: 'help',
      reason:
        'the parameter of the `help` helper itself; every call site passes one of this file\'s own constants, or a line this file composed from them (`launchCounter(0)`, the two optional-field lines)',
    },
    label: {
      within: 'field',
      reason: 'the parameter of the `field` helper itself; every call site passes a string literal naming the control',
    },
    name: {
      within: 'fact',
      reason:
        'the parameter of the `fact` helper itself; every call site passes a string literal naming a `launchResult` field -- the value beside it goes through `putText` with that field\'s own class',
    },
    sentence: {
      within: 'line',
      reason:
        'the parameter of the `line` helper itself; every call site passes one of this file\'s own constants, or `refusalText(...)`, which is `sanitizeRefusal`\'s own answer for a sentence main or the CLI wrote',
    },
  },
})

/** blankComments replaces every comment with spaces, keeping strings and line
 * breaks, so that a `literal(` in a comment is not a call and a `//` inside a
 * string is not a comment. With `blankStrings`, string and template text is
 * blanked too (an interpolation's code is kept), so a sink's name inside a
 * string is not a reference to it. Offsets are preserved either way. */
function blankComments(source: string, blankStrings = false): string {
  const kept = (text: string): string => (blankStrings ? text.replace(/[^\n]/g, ' ') : text)
  let out = ''
  let index = 0
  const templateDepth: number[] = []
  let braces = 0
  while (index < source.length) {
    const char = source[index] as string
    const next = source[index + 1]
    if (char === '/' && next === '/') {
      while (index < source.length && source[index] !== '\n') {
        out += ' '
        index += 1
      }
      continue
    }
    if (char === '/' && next === '*') {
      const end = source.indexOf('*/', index + 2)
      const stop = end === -1 ? source.length : end + 2
      for (; index < stop; index += 1) out += source[index] === '\n' ? '\n' : ' '
      continue
    }
    if (char === '\'' || char === '"') {
      const start = index
      index += 1
      while (index < source.length && source[index] !== char && source[index] !== '\n') {
        index += source[index] === '\\' ? 2 : 1
      }
      index += 1
      out += kept(source.slice(start, index))
      continue
    }
    if (char === '`' || (char === '}' && templateDepth.length > 0 && templateDepth.at(-1) === braces)) {
      // The start of a template, or the `}` that closes an interpolation and
      // resumes one: copy the template text up to its end or its next `${`.
      if (char === '}') templateDepth.pop()
      const start = index
      index += 1
      while (index < source.length) {
        if (source[index] === '\\') {
          index += 2
          continue
        }
        if (source[index] === '`') {
          index += 1
          break
        }
        if (source[index] === '$' && source[index + 1] === '{') {
          index += 2
          templateDepth.push(braces)
          break
        }
        index += 1
      }
      out += kept(source.slice(start, index))
      continue
    }
    if (char === '{') braces += 1
    if (char === '}') braces -= 1
    out += char
    index += 1
  }
  return out
}

/** argumentsAt splits the call whose `(` is at `open` into its top-level
 * arguments, as source text. */
function argumentsAt(source: string, open: number): string[] {
  const args: string[] = []
  let depth = 0
  let start = open + 1
  let index = open
  while (index < source.length) {
    const char = source[index] as string
    if (char === '\'' || char === '"' || char === '`') {
      // Skip a string or a whole template, interpolations included.
      index = skipString(source, index)
      continue
    }
    if (char === '(' || char === '[' || char === '{') depth += 1
    if (char === ')' || char === ']' || char === '}') {
      depth -= 1
      if (depth === 0) {
        args.push(source.slice(start, index).trim())
        return args
      }
    }
    if (char === ',' && depth === 1) {
      args.push(source.slice(start, index).trim())
      start = index + 1
    }
    index += 1
  }
  throw new Error(`unterminated call at ${String(open)}`)
}

function skipString(source: string, at: number): number {
  const quote = source[at]
  let index = at + 1
  while (index < source.length) {
    const char = source[index]
    if (char === '\\') {
      index += 2
      continue
    }
    if (char === quote) return index + 1
    if (quote === '`' && char === '$' && source[index + 1] === '{') {
      // An interpolation: walk to its matching brace.
      let depth = 1
      index += 2
      while (index < source.length && depth > 0) {
        const inner = source[index]
        if (inner === '\'' || inner === '"' || inner === '`') {
          index = skipString(source, index)
          continue
        }
        if (inner === '{') depth += 1
        if (inner === '}') depth -= 1
        index += 1
      }
      continue
    }
    index += 1
  }
  return index
}

/**
 * ATTRIBUTE_VOCABULARY is every non-constant value a screen writes into an
 * attribute, with why it is not a document's string. Same shape as
 * CLOSED_VOCABULARY.
 */
const ATTRIBUTE_VOCABULARY: Readonly<Record<string, Readonly<Record<string, Entry>>>> = Object.freeze({
  'live.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    "formatReading(reading, 'percent')": '`formatReading` of a Reading: the console\'s own number formatting or its unavailable mark',
    'reading.state': 'a Reading\'s `state`, the closed `ReadingState` union',
    'String(line.segments)': 'a count the model computed',
    'none.state': 'a Reading\'s `state`, the closed `ReadingState` union',
    "formatReading(point.height, 'percent')": '`formatReading` of a Reading: the console\'s own number formatting or its unavailable mark',
    'point.height.state': 'a Reading\'s `state`, the closed `ReadingState` union',
    "formatReading(point.rate, 'usd')": '`formatReading` of a Reading: the console\'s own number formatting or its unavailable mark',
    'row.tone': '`RunTone`, a closed union the model assigns',
    'flag.name': 'a RunFlag name from live.ts\'s own flag table (`runFlags`)',
    'flag.tone': '`RunTone`, from the same flag table',
    'flag.because': 'the flag table\'s own sentences, interpolating only console constants',
    '`the document carries no ${field}`':
      'the console\'s own sentence for a field the document left out of this run, interpolating the wire path the caller passes (`runs[].models`, `runs[].auth`) and nothing the document chose -- the same sentence `putReading` writes into the `title` of a reading with no number, written here because an actor is text rather than a reading',
    'sanitizedKey(repo.slug)': 'the slug as `sanitizeText` drew it (slug class)',
    "repo.kill === null ? 'false' : 'true'": 'a boolean drawn as one of two literals',
    "connection.textContent ?? ''": 'read back from a cell `putText` wrote, so it is the sanitized text',
    "formatReading(feedReading(feed), 'seconds')": '`formatReading` of a Reading: the console\'s own number formatting or its unavailable mark',
    "model.feed === null ? '' : (model.feed.since ?? '')":
      'the arrival stamp `src/source/cli.ts` wrote on the frame (or the empty string when the caller keeps no feed reading), used as a key rather than as text',
    'segment.name': {
      within: 'fleetBar',
      reason:
        'the money account of the segment `fleetSegments` built -- one of this file\'s own four words (`counted`, `spent`, `leased`, `headroom`) -- written as the `data-account` hook the stylesheet paints a shape by, never drawn as text',
    },
    'account.name': {
      within: 'fleetLegendRow',
      reason:
        'the same money account for the legend row\'s swatch: `fleetLegend` builds its rows from this file\'s own table, so the attribute carries a word of the console\'s and not one from the document',
    },
    'axis(drawn)': {
      within: 'fleetBar',
      reason:
        '`axis` of a share this file\'s own arithmetic clamped to the cap: the x of a segment on a 100-unit axis, which is geometry rather than text, and the same kind of value `graph.ts` writes through `coord`',
    },
    'axis(share)': {
      within: 'fleetBar',
      reason:
        '`axis` of the same share as the segment\'s own width: the axis is this file\'s constant rather than a number from the document, so the value is the shape\'s size and nothing a document can spell',
    },
    theme: { within: 'renderLive', reason: 'a `LiveTheme`, the closed theme tuple' },
  },
  'projects.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    'panel.key': '`ProjectsPanelKey`, the closed `PROJECT_PANEL_KEYS` tuple',
    'panel.state': '`ProjectPanelState`, a closed union the model assigns',
    'panel.operation': '`OperationName`, the closed IPC allow-list union',
    'row.scope': '`ProjectCapScope`, a closed union the model assigns (`global`, `defaults`, `repo`)',
    slug: { within: 'capRowLine', reason: '`sanitizedKey(row.slug)`: the slug as `sanitizeText` drew it, null when refused' },
    theme: {
      within: 'renderProjectsFrame',
      reason:
        'a `ProjectsTheme`, the closed theme tuple. The write is the frame\'s: it is the shell the entry mounts in the frame the tab is opened in, and `renderProjects` draws the whole screen by calling it (console 0.117.0)',
    },
  },
  'degraded.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    "model.caps.state": 'a Reading\'s `state`, the closed `ReadingState` union',
    "sanitizedKey(row.run)": 'the run id as `sanitizeText` drew it (runId class), empty when refused',
    kind: { within: 'root', reason: 'the closed discriminant of this file\'s states (`no-firebase`, `no-binary`, `refused`), written as a key' },
    "receivedAt === null ? '' : receivedAt": 'the arrival stamp `src/source/cli.ts` wrote on the frame, or the empty string when the caller keeps no feed reading; a key rather than text',
    theme: { within: 'root', reason: 'a `LiveTheme`, the closed theme tuple' },
  },
  'graph.ts': {
    "classes.join(' ')": { within: 'elementIn', reason: 'the class list of the `elementIn` helper; every call site passes string-literal class names' },
    'value.reason': {
      within: 'drawInto',
      reason: 'the sanitizer\'s own reason for a refused value, written by `sanitizeText` in the model and naming the field it refused rather than the value it refused',
    },
    word: {
      within: 'markWord',
      reason: 'a `StateWord` from `GraphNode.words`, the model\'s own vocabulary, written as the `data-word` hook a rule colours -- the same word the element draws as text',
    },
    'coord(value)': {
      within: 'geometry',
      reason:
        '`coord` of a number the layout client let through as finite (`layout-client.ts` reads every coordinate with `Number.isFinite`), which is what makes each of `x`, `y`, `width`, `height`, `rx` and `d` a number and not a string -- and why the theme is never a length',
    },
    '`translate(${coord(x)} ${coord(y)})`': { within: 'shift', reason: 'the same two coordinates as an SVG transform: the engine\'s answer for where a card sits' },
    'path': {
      within: 'drawPlan',
      reason: '`pathOf(route.sections)`: an `M`/`L` run over the bend points the engine answered with, every coordinate of it through `coord`',
    },
    'picture.state.view': { within: 'header', reason: '`GraphViewName`, the closed `GRAPH_VIEWS` tuple' },
    'GRAPH_VIEW_NAMES[picture.state.view]': { within: 'header', reason: 'this file\'s own table of what each drawing is called, keyed by the same closed union' },
    'tabKey(option.tab)': {
      within: 'filter',
      reason:
        '`tabKey` spells a `GraphTab` as one string -- `all` for the All tab, `stream:<name>` otherwise -- so the attribute tells a stream called `all` from the All tab, which `tabOf` reads back',
    },
    'nodeKey(plan.id, task.id)': {
      within: 'related',
      reason:
        'the layout key of one of the two lists the inspector draws, built the same way a card\'s own key is -- ids the model holds as **keys** and never spells (`graph/model.ts`\'s header) -- written into `data-key` and never drawn; the text beside it is the task\'s `Sanitized` title',
    },
    'key.state': {
      within: 'legendItem',
      reason:
        'one legend line\'s `data-state`: a word of `GRAPH_LEGEND_WORDS` (the model\'s own `STATE_WORDS`, less `stuck`), so the swatch is drawn for a state the model can actually name',
    },
    'key.edge': {
      within: 'legendItem',
      reason:
        'one legend line\'s `data-edge`: one of the two literal kinds this file\'s `legend` passes (a dependency and the critical path), the console\'s own vocabulary and never a document string',
    },
    'group.stream': {
      within: 'chips',
      reason: 'a stream name, which the model holds as a **key**: `graph/model.ts` never spells one, the heading beside it is the `Sanitized` label, and a plan\'s `stream` is the same kind of value',
    },
    'one.stream': {
      within: 'band',
      reason: 'the same stream key the chip group carries, on the band drawn behind that stream\'s row: `graph/model.ts` never spells a stream name, and the label drawn at the band\'s head is the `Sanitized` one',
    },
    'box.key': { within: 'releaseBox', reason: '`releaseKey(plan, version)`, this file\'s own join of two ids the model holds as keys -- written as `data-release` and never drawn' },
    key: 'the layout key of a card, a row or a chip -- `nodeKey(plan.id, node.id)` or `releaseKey(plan.id, version)`, ids the model holds as **keys** and never spells (`graph/model.ts`\'s header) -- written into `data-key`/`data-chip` and never drawn; the drawn text is the `Sanitized` title beside it',
    "node.words[0] ?? ''": 'the node\'s first `StateWord`, the model\'s own vocabulary, as the `data-state` hook a rule colours; the empty string is a node the model gave no word, which is a state no colour applies to',
    'door.run': 'the run id this console\'s own run list resolved the node\'s suffix to (`doorFor`), which is what `data-run-id` carries and what the entry opens -- a node whose suffix no run ends with, or one two runs end with, carries no attribute at all',
    'door.because': { within: 'listRow', reason: '`doorFor`\'s own sentence for a gesture that opens nothing, in the `title` an HTML element keeps a tooltip in' },
    "door.run !== null ? 'button' : 'group'":
      'a card\'s role as one of two literals: where a run resolved the card is the `button` Enter and Space open, and where none did it is the `group` no key touches -- the same shape as `live.ts`\'s `repo.kill === null ? \'false\' : \'true\'`',
    'cardLabel(node)': {
      within: 'nodeCard',
      reason:
        '`cardLabel`: a card\'s accessible name -- the same `Sanitized` title the card draws, **whole** rather than cut at three lines, and the `StateWord` list the model answered beside it. It is a name and not a drawing: it is never painted, and what a reader is told is therefore the title the card could not hold',
    },
    'viewBoxOf(bounds, picture.state.zoom)': {
      within: 'canvas',
      reason: '`viewBoxOf`: four numbers off the picture\'s own bounds and the state\'s zoom, so what the canvas looks at is geometry rather than a style',
    },
    theme: { within: 'renderGraph', reason: 'a `GraphTheme`, the closed `GRAPH_THEMES` tuple' },
  },
  'run.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    theme: { within: 'renderRunFrame', reason: 'a `RunTheme`, the closed theme tuple' },
    '`the document carries no ${field}`':
      'the console\'s own sentence for a field the record left out of this run, interpolating the wire path the caller passes (`row.repo`, `row.oauth_account`, `row.branch`) and nothing the document chose -- the same sentence `putReading` writes into the `title` of a reading with no number, written here because a column of the header is text rather than a reading',
    'stageShare(value, longest)': {
      within: 'stageBar',
      reason:
        '`stageShare`: a measured duration as its share of the longest one drawn beside it, on the bar\'s own hundred-unit axis -- geometry rather than text or a style, and the same kind of value `graph.ts` writes through `coord`, with both numbers the model\'s (`measured` readings the panel read) and never a string a document can spell',
    },
    'shareOfPercent(billed, total)': {
      within: 'ledgerBar',
      reason:
        '`shareOfPercent`: the billed model figure as its share of the ledger\'s own measured total, on the bar\'s hundred-unit axis -- the bar\'s width, geometry rather than text or a style, of two numbers the panel read and never a string a document can spell',
    },
    'shareOfPercent(estimate, total)': {
      within: 'ledgerBar',
      reason:
        'the same share for the compute figure, which the outline segment starts at and is as wide as; geometry off the same two readings',
    },
    'trackCoord(playheadAt)': {
      within: 'replayPanel',
      reason:
        '`trackCoord` of the position `positionOf` returned for the scrubbed record -- the playhead\'s `x1`/`x2` and the width of the rail\'s filled part. Geometry on the track\'s hundred-unit axis, off a stamp the console clamps and never a string a document can spell',
    },
    'trackCoord(position)': {
      within: 'replayPanel',
      reason:
        'the same coordinate for one tick: `positionOf` of a record\'s own stamp, clamped to the window, written as the tick line\'s `x1`/`x2`. Geometry, and the tick carries no text of its own',
    },
  },
  '../../shared/notice.ts': {
    "classes.join(' ')": { within: 'cell', reason: 'the class list of the `cell` helper; every call site passes string-literal class names' },
    'spec.severity': { within: 'renderNotice', reason: 'a `NoticeSeverity`, the closed two-word tone union (`warning`, `info`)' },
    '[NOTICE_ROOT_CLASS, ...spec.classes].join(\' \')': {
      within: 'renderNotice',
      reason:
        'the card root\'s class list: this file\'s own constant plus the caller\'s extra class, and the caller passes a string-literal list (the skew notice passes `console-skew`). The write before it stamps `NOTICE_ROOT_CLASS` alone, because a list the theme\'s selector scan cannot evaluate would leave `section.notice` unlicensed',
    },
    value: { within: 'attribute', reason: 'the parameter of the `attribute` helper itself: it is handed one value per entry of a caller\'s own closed table (`data-hidden`\'s `ReducedView`), never a document string' },
    'ids.title': {
      within: 'renderNotice',
      reason:
        'the card\'s own title id (`noticeIds`): the prefix is this file\'s constant and the number is a counter this module keeps, so nothing a document or a caller passes is in it. It is an id and not text -- `aria-labelledby` resolves it to an element, it is never read as words on screen',
    },
    'ids.details': {
      within: 'renderNotice',
      reason:
        'the card\'s own details-region id, built by the same `noticeIds` call as the title\'s, so `aria-controls` names the region of **this** card and not of the first one drawn',
    },
  },
  '../../shared/version.ts': {
    theme: { within: 'renderVersionSkew', reason: 'a `SkewTheme`, the closed two-word theme union' },
    'compatibility.mode': { within: 'renderVersionSkew', reason: '`CliCompatibilityMode`, the closed union of the three things the console can do about a skew' },
    'compatibility.skew': { within: 'renderVersionSkew', reason: '`CliSkewName`, the closed list of the six ways a version line can be placed' },
  },
  'spend.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    'entry.dimension': '`SPEND_DIMENSIONS`, the console\'s table',
    'entry.note': '`PERSON_OFF_NOTE`, a console constant, or null',
    'drawnKey(keyed)': 'read back from a cell `putText` wrote, so it is the sanitized text',
    'series.account': '`SpendAccount`, a closed union',
    'drawnKey(name)': 'read back from a cell `putText` wrote, so it is the sanitized text',
    "scope.known ? scope.source : 'unknown'": "a known scope's word is on the console's `SCOPES` table; an unknown one is drawn as `unknown`",
    'people.view': '`PeopleView`, the closed three words `peopleViewOf` resolves (`all`, `own`, and `unknown` for a word this build does not know)',
    'drawnKey(currency)': 'read back from a cell `putText` wrote, so it is the sanitized text',
    theme: { within: 'renderSpendFrame', reason: 'a `SpendTheme`, the closed theme tuple' },
    'chartUnits(SPEND_CHART_SIZE - (share / 100) * SPEND_CHART_SIZE)': {
      within: 'gridline',
      reason:
        "a gridline's own y: `share` is one of `SPEND_GRIDLINE_SHARES`, a table in this file, and `chartUnits` rounds the arithmetic into the chart's drawing units -- so the value is a number this console computed and no document string is in it",
    },
    'shape.x': { within: 'barRect', reason: "a bar's own box, in the chart's drawing units: `barShape` computes it from the bar's share of the shared axis and `chartUnits` rounds it" },
    'shape.y': { within: 'barRect', reason: "a bar's own box, in the chart's drawing units: `barShape` computes it from the bar's share of the shared axis and `chartUnits` rounds it" },
    'shape.width': { within: 'barRect', reason: "a bar's own box, in the chart's drawing units: `barShape` computes it from the bar's share of the shared axis and `chartUnits` rounds it" },
    'shape.height': { within: 'barRect', reason: "a bar's own box, in the chart's drawing units: `barShape` computes it from the bar's share of the shared axis and `chartUnits` rounds it" },
    'capsBarUnits(counted / cap)': {
      within: 'capsBar',
      reason:
        "the caps panel's daily-cap bar: `counted_usd` over `daily_cap_usd`, as a length on the bar's own hundred-unit box, clamped to the box -- geometry this console computed from two figures the document carries, and nothing a document can spell",
    },
    'column.x': { within: 'gapColumn', reason: "the gap column's own box, in the chart's drawing units: the full height of the plot, computed the same way a bar's is" },
    'column.y': { within: 'gapColumn', reason: "the gap column's own box, in the chart's drawing units: the full height of the plot, computed the same way a bar's is" },
    'column.width': { within: 'gapColumn', reason: "the gap column's own box, in the chart's drawing units: the full height of the plot, computed the same way a bar's is" },
    'column.height': { within: 'gapColumn', reason: "the gap column's own box, in the chart's drawing units: the full height of the plot, computed the same way a bar's is" },
    key: "the **drawn** key a bar and a gap's own column are tagged with, read back off the label `chartKey` wrote with `putText` -- so it is the sanitized text and never the document's raw key",
    'bar.account': '`SpendAccount`, a closed union',
    'bar.reading.state': "a Reading's `state`, the closed `ReadingState` union",
    "formatReading(bar.share, 'percent')": "`formatReading` of a Reading: the console's own number formatting or its unavailable mark",
    account: { within: 'seriesBars', reason: '`SpendAccount`, the closed union the loop walks (`SPEND_ACCOUNTS`)' },
    'missing.account': 'the account whose figure the document does not carry, off the closed `SPEND_ACCOUNTS` union',
  },
  'coordinator.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    theme: {
      within: 'renderCoordinatorFrame',
      reason:
        'a `CoordinatorTheme`, the closed theme tuple. The write is the frame\'s: it is the shell the entry mounts in the frame the tab is opened in, and `renderCoordinator` draws the whole screen by calling it (console 0.117.0)',
    },
    'pullName(label, link.repo)': {
      within: 'pullButton',
      reason:
        'a pull request number\'s accessible name (K2b), and the same kind of value as `graph.ts`\'s `cardLabel`: the console\'s own verb, the repository that passed `pullLink` (main\'s own `PULL_REQUEST_REPO_RE` **and** the `repository` text class the cell beside it draws), and the label the row draws. The label is not a second conversion of a document string: `pullButton` is only reached with a link, and `pullLink` builds one only from a record that carries a whole positive `pr.number` -- which is exactly the case `recordLabel` answers `#<n>` for, so no run id, title or branch ever reaches the name. It is a name and not a drawing: it is never painted, and what a reader who cannot see the row hears is the number and where it lives',
    },
  },
  'reading.ts': {
    theme: { within: 'renderReading', reason: 'a `SkewTheme`, the closed two-word theme union' },
  },
  'nocheckout.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    theme: { within: 'renderNoCheckout', reason: 'a `SkewTheme`, the closed two-word theme union' },
  },
  'launch.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    recorded: {
      within: 'options',
      reason:
        'an entry of `view.repositories`, written into the `<option>`\'s `value`: the **recorded repository identity itself**, which is the string main matches against its own records (`launchRepoArgument`, `src/main/launch.ts`) and which `entry.ts` keeps raw for exactly that reason. It is the one attribute of this file that carries a document\'s string, and it is inert -- a `value` is never parsed as markup, never navigated to and never read as a style (unlike `href`, `style` and the rest of `sanitize.test.ts`\'s forbidden set). It cannot be replaced by the drawn text: the label goes through `refusalText`, so a repository named like a credential is drawn as the marker and the launch would then be refused for a repository the operator did pick',
    },
    id: { within: 'help', reason: 'the parameter of the `help` helper itself; every call site passes a `LAUNCH_IDS` member or this file\'s own `launch-form` id' },
    controlId: { within: 'field', reason: 'the parameter of the `field` helper itself; every call site passes a `LAUNCH_IDS` member' },
    "described.join(' ')": { within: 'field', reason: 'the `LAUNCH_IDS` members a control is described by; every call site passes an array of them' },
    'LAUNCH_IDS.form': {
      within: 'renderLaunch',
      reason:
        '`LAUNCH_IDS.form`, the id this file gives the form: the button\'s `aria-controls` and the form\'s own `id` are the one string, so the control and the thing it opens are related in the DOM rather than merely adjacent',
    },
  },
})

const STRING_LITERAL = /^(?:'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*")$/
const PLAIN_TEMPLATE = /^`(?:[^`\\$]|\\.|\$(?!\{))*`$/
const CONSTANT = /^[A-Z][A-Z0-9_]*$/
const STRING_TERNARY =
  /^[\w.!]+\s*\?\s*(?:'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*")\s*:\s*(?:'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*")$/

/** SinkCall is one argument a screen hands a sink. */
interface SinkCall {
  readonly sink: string
  readonly line: number
  readonly at: number
  readonly argument: string
}

/** callsOf finds every call to `name(` in comment-blanked code, skipping the
 * function's own declaration, with its top-level arguments. */
function callsOf(code: string, name: string): { at: number; args: string[] }[] {
  const found: { at: number; args: string[] }[] = []
  const pattern = new RegExp(`(?<![\\w.$])${name}\\(`, 'g')
  for (const match of code.matchAll(pattern)) {
    const at = match.index
    if (/function\s+$/.test(code.slice(Math.max(0, at - 20), at))) continue
    found.push({ at, args: argumentsAt(code, at + name.length) })
  }
  return found
}

/** sinkCalls finds every call to a sink in one file's source, skipping the
 * sinks' own declarations. */
function sinkCalls(source: string): SinkCall[] {
  const code = blankComments(source)
  const found: SinkCall[] = []
  for (const [sink, position] of Object.entries(SINKS)) {
    for (const { at, args } of callsOf(code, sink)) {
      const argument = args[position]
      if (argument === undefined) throw new Error(`${sink}( with no argument ${String(position)}`)
      found.push({ sink, line: lineOf(code, at), at, argument })
    }
  }
  return found
}

function lineOf(code: string, at: number): number {
  return code.slice(0, at).split('\n').length
}

/** bodyOf is the [start, end) offsets of a named function's body in
 * comment-blanked code, or null when the file declares no such function. */
function bodyOf(code: string, name: string): readonly [number, number] | null {
  const declared = new RegExp(`function\\s+${name}\\s*\\(`).exec(code)
  if (declared === null) return null
  // Past the parameter list, then to the first brace: the body.
  let index = declared.index + declared[0].length - 1
  let depth = 0
  for (; index < code.length; index += 1) {
    const char = code[index]
    if (char === '\'' || char === '"' || char === '`') {
      index = skipString(code, index) - 1
      continue
    }
    if (char === '(') depth += 1
    if (char === ')') {
      depth -= 1
      if (depth === 0) break
    }
  }
  const open = code.indexOf('{', index)
  depth = 0
  for (index = open; index < code.length; index += 1) {
    const char = code[index]
    if (char === '\'' || char === '"' || char === '`') {
      index = skipString(code, index) - 1
      continue
    }
    if (char === '{') depth += 1
    if (char === '}') {
      depth -= 1
      if (depth === 0) return [open, index + 1]
    }
  }
  throw new Error(`unterminated body of ${name}`)
}

/** within answers whether an offset is inside a named function's body. */
function within(code: string, name: string, at: number): boolean {
  const body = bodyOf(code, name)
  return body !== null && at >= body[0] && at < body[1]
}

/** allows is whether an entry admits an argument found at an offset. */
function allows(entry: Entry | undefined, code: string, at: number): boolean {
  if (entry === undefined) return false
  return typeof entry === 'string' || within(code, entry.within, at)
}

/** isConstantArgument is the shapes that are the console's words by syntax. */
function isConstantArgument(argument: string, source: string): boolean {
  if (STRING_LITERAL.test(argument) || PLAIN_TEMPLATE.test(argument) || STRING_TERNARY.test(argument)) return true
  if (CONSTANT.test(argument)) {
    return new RegExp(`^(?:export )?const ${argument}\\b`, 'm').test(source)
  }
  return false
}

/** MARKUP is what no screen may use at all, whatever the value: a markup slot
 * parses its value, and a namespaced attribute write is a door around the
 * attribute scan. */
const MARKUP = /\.(?:innerHTML|outerHTML)\s*\+?=(?!=)|(?<![\w$])(?:insertAdjacentHTML|setAttributeNS|createContextualFragment)\s*\(|(?<![\w$])document\s*\.\s*write/g

/** TEXT_SLOT is an assignment to a text slot, which only `literal` makes. */
const TEXT_SLOT = /\.(?:textContent|innerText)\s*\+?=(?!=)/g

/** ATTRIBUTE_PROPERTY is an attribute written as a property: its value is the
 * rest of the statement. */
const ATTRIBUTE_PROPERTY = /\.(?:title|className|id|dataset(?:\.\w+|\[[^\]]*\]))\s*\+?=(?!=)/g

/** QUOTED_KEY is a property name written as a quoted string, in any of the
 * three quote styles: the key half of a computed access. */
const QUOTED_KEY = "(?:'[^'\\n]*'|\"[^\"\\n]*\"|`[^`\\n]*`)"

/** COMPUTED_WRITE is a property written through a computed key --
 * `el['textContent'] = x`, `el["title"] = x`, ``el[`innerHTML`]``. It is an
 * offence whatever the key is: this scan reads `.textContent =` and
 * `.title =` by name, and a write it cannot read the name of is a write it
 * cannot hold to any vocabulary. */
const COMPUTED_WRITE = new RegExp(`\\[\\s*${QUOTED_KEY}\\s*\\]\\s*\\+?=(?!=)`, 'g')

/** COMPUTED_CALL is a call through a computed key -- `el['setAttribute'](...)`,
 * the attribute writer reached by a name this scan does not spell. */
const COMPUTED_CALL = new RegExp(`\\[\\s*${QUOTED_KEY}\\s*\\]\\s*\\??\\.?\\s*\\(`, 'g')

/** OBJECT_WRITE is the library spelling of "write this property": three ways to
 * write a text slot or an attribute without ever assigning one. */
const OBJECT_WRITE = /(?<![\w$.])(?:Object\s*\.\s*(?:assign|defineProperty|defineProperties)|Reflect\s*\.\s*set)\s*\(/g

/** ATTRIBUTE_NAME is the attribute writer by its member name, so that every
 * spelling of reaching it -- the call, the optional call, and the ones that are
 * not a call at all -- is found from one place. A bare `setAttribute(` in a
 * local of the same name is not this. */
const ATTRIBUTE_NAME = /\.setAttribute\b/g

/**
 * STYLE_PROPERTY is the element's `style` property, read or written, in any of
 * the spellings a module can reach it by: `.style.color = x`, `.style = {...}`,
 * `.style.setProperty(...)`, `el.style` handed on, and the **computed key**
 * (`el['style']`, ``el[`style`]``), which is the spelling the `literal` scan
 * above already treats as a way past a scan that reads names. **Every use of it
 * is an offence**, which is the one scan in this file with no vocabulary behind
 * it:
 * `style-src 'self'` is the CSP's third directive and it allows no inline style
 * at all, so there is no value a `style` write could hold that the policy backs.
 * The theme travels as classes and as `data-*` attributes the stylesheet keys
 * on, which is why `class` and `dataset` have vocabularies here and `style` has
 * none.
 */
const STYLE_PROPERTY = /\.style\b/g

/** STYLE_KEY is the same property reached by a **computed key** --
 * `el['style']`, `el["style"]`, ``el[`style`]``. It is a second constant and not
 * a second alternative in `STYLE_PROPERTY`, because the key IS a string: the
 * scan below blanks strings before it reads, so the computed spelling is only
 * visible in the code with its strings intact -- exactly as `COMPUTED_WRITE`
 * reads it. */
const STYLE_KEY = new RegExp(`\\[\\s*(?:'style'|"style"|\`style\`)\\s*\\]`, 'g')

/** STYLE_ATTRIBUTE_NAME is the `style` attribute written by name, in the three
 * quote styles: what `setAttribute('style', ...)` passes first, and the one
 * attribute name this scan refuses whatever the value is. */
const STYLE_ATTRIBUTE_NAME = /^(?:'style'|"style"|`style`)$/

/** styleOffences lists every inline style a module writes, one line each: the
 * `style` property in any spelling, and the `style` attribute by name. It is
 * deliberately not a vocabulary: see `STYLE_PROPERTY`. */
function styleOffences(file: string, source: string): string[] {
  const code = blankComments(source)
  const bare = blankComments(source, true)
  const out: string[] = []
  for (const match of bare.matchAll(STYLE_PROPERTY)) {
    out.push(
      `${file}:${String(lineOf(bare, match.index))} an inline style, which style-src 'self' does not back: ${match[0].trim()}`,
    )
  }
  // Read out of `code` rather than `bare`: the key is a string, and blanking
  // strings is what would hide it.
  for (const match of code.matchAll(STYLE_KEY)) {
    out.push(
      `${file}:${String(lineOf(code, match.index))} an inline style written through a computed key, which style-src 'self' does not back: ${match[0].trim()}`,
    )
  }
  for (const { at, args } of attributeCalls(bare, code)) {
    if (!STYLE_ATTRIBUTE_NAME.test(args[0] ?? '')) continue
    out.push(
      `${file}:${String(lineOf(bare, at))} setAttribute(${args[0] ?? ''}, ...): an inline style, which style-src 'self' does not back`,
    )
  }
  return out
}

/** referencesOf is every use of a sink's name that is not a call to it or its
 * declaration -- an alias, a destructuring, the sink passed as a value. Only
 * a sink the file declares is checked: `note` is a plain variable elsewhere. */
function referencesOf(code: string): { sink: string; at: number }[] {
  const found: { sink: string; at: number }[] = []
  for (const sink of Object.keys(SINKS)) {
    if (bodyOf(code, sink) === null) continue
    for (const match of code.matchAll(new RegExp(`(?<![\\w$.])${sink}(?![\\w$])`, 'g'))) {
      const at = match.index
      const after = code.slice(at + sink.length)
      if (/^\s*\(/.test(after)) continue
      if (/function\s+$/.test(code.slice(Math.max(0, at - 20), at))) continue
      // A property key or a type member of the same name (`note: string`) is
      // not the function; `{ note }` shorthand is, and is caught.
      if (/^\??\s*:(?!:)/.test(after) && !/\?\s*$/.test(code.slice(Math.max(0, at - 20), at))) continue
      found.push({ sink, at })
    }
  }
  return found
}

/** offences is every way a file writes to the screen that is neither a
 * constant shape nor on that file's closed-vocabulary lists. */
function offences(file: string, source: string): string[] {
  const allowed = CLOSED_VOCABULARY[file] ?? {}
  const attributes = ATTRIBUTE_VOCABULARY[file] ?? {}
  const code = blankComments(source)
  const bare = blankComments(source, true)
  const out: string[] = []
  for (const call of sinkCalls(source)) {
    if (isConstantArgument(call.argument, source) || allows(allowed[call.argument], code, call.at)) continue
    out.push(`${file}:${String(call.line)} ${call.sink}(..., ${call.argument})`)
  }
  for (const ref of referencesOf(bare)) {
    out.push(`${file}:${String(lineOf(bare, ref.at))} ${ref.sink} used other than by a call to it`)
  }
  for (const match of bare.matchAll(MARKUP)) {
    out.push(`${file}:${String(lineOf(bare, match.index))} markup: ${match[0].trim()}`)
  }
  for (const match of bare.matchAll(TEXT_SLOT)) {
    if (within(code, 'literal', match.index)) continue
    out.push(`${file}:${String(lineOf(bare, match.index))} a text slot written outside literal`)
  }
  for (const match of bare.matchAll(ATTRIBUTE_PROPERTY)) {
    const end = code.indexOf('\n', match.index)
    const value = code.slice(match.index + match[0].length, end === -1 ? undefined : end).trim()
    if (isConstantArgument(value, source) || allows(attributes[value], code, match.index)) continue
    out.push(`${file}:${String(lineOf(bare, match.index))} ${match[0].trim()} ${value}`)
  }
  // These two read the code with its strings intact, because the quoted key IS
  // a string: blanking them would blank the very thing the rule looks for. A
  // string literal containing the text `['title'] =` would be reported, which
  // is the direction this file errs in everywhere.
  for (const match of code.matchAll(COMPUTED_WRITE)) {
    out.push(`${file}:${String(lineOf(code, match.index))} a property written through a computed key: ${match[0].trim()}`)
  }
  for (const match of code.matchAll(COMPUTED_CALL)) {
    out.push(`${file}:${String(lineOf(code, match.index))} a call through a computed key: ${match[0].trim()}`)
  }
  for (const match of bare.matchAll(OBJECT_WRITE)) {
    out.push(`${file}:${String(lineOf(bare, match.index))} a property written through the library: ${match[0].trim()}`)
  }
  for (const at of attributeOtherUses(bare)) {
    out.push(`${file}:${String(lineOf(bare, at))} setAttribute used other than by a call to it`)
  }
  for (const { at, args } of attributeCalls(bare, code)) {
    const value = args[1] ?? ''
    if (isConstantArgument(value, source) || allows(attributes[value], code, at)) continue
    out.push(`${file}:${String(lineOf(bare, at))} setAttribute(..., ${value})`)
  }
  return out
}

/** isAttributeCall is whether what follows the attribute writer at `at` is a
 * call: `(` for the call, `?.(` for the optional one, and nothing else. */
function isAttributeCall(code: string, at: number): boolean {
  return /^\s*(?:\?\.\s*)?\(/.test(code.slice(at + '.setAttribute'.length))
}

/**
 * attributeCalls is every call of the attribute writer, **the optional-call
 * spelling included**, with its arguments. `matchesIn` is where the calls are
 * found and `argumentsIn` is where their arguments are read, and the two are
 * separate on purpose: the scan finds a call in string-blanked code (so a
 * `.setAttribute(` inside a string is not a call) and reads the argument out of
 * the real code (so a string argument is the string it is, and not the run of
 * spaces the blanking left).
 */
function attributeCalls(matchesIn: string, argumentsIn: string): { at: number; args: string[] }[] {
  const found: { at: number; args: string[] }[] = []
  for (const match of matchesIn.matchAll(ATTRIBUTE_NAME)) {
    if (!isAttributeCall(matchesIn, match.index)) continue
    found.push({ at: match.index, args: argumentsAt(argumentsIn, argumentsOpenAt(matchesIn, match.index)) })
  }
  return found
}

/** argumentsOpenAt is the offset of an attribute call's `(`, which is what
 * `argumentsAt` needs; the caller has already established that there is one. */
function argumentsOpenAt(code: string, at: number): number {
  const open = /^\s*(?:\?\.\s*)?\(/.exec(code.slice(at + '.setAttribute'.length))
  if (open === null) throw new Error('no attribute call at this offset')
  return at + '.setAttribute'.length + open[0].length - 1
}

/** attributeOtherUses is every use of the attribute writer that is not a call
 * with the `(` right there: the writer taken off the element and applied
 * (`.call`, `.apply`), read into a variable, or handed on as a value. */
function attributeOtherUses(code: string): number[] {
  const out: number[] = []
  for (const match of code.matchAll(ATTRIBUTE_NAME)) {
    if (!isAttributeCall(code, match.index)) out.push(match.index)
  }
  return out
}

/** SOURCE_EXTENSIONS is every extension a module that could draw may have. A
 * single `.ts` test was the review's finding: a drawer added as `x.tsx` or
 * `x.mts` was neither scanned nor noticed, so the defect this file exists for
 * could be reintroduced in a new file with the suite green. */
const SOURCE_EXTENSIONS: readonly string[] = Object.freeze(['.ts', '.tsx', '.mts', '.cts'])

/**
 * walkFiles lists every module under `directory`, **recursively**, as a path
 * relative to `from`. A walk rather than a one-level `readdirSync`: the review
 * found that `screens/<sub>/x.ts` -- and any extension this file did not name --
 * was invisible to the scan, and a drawer in a directory nobody listed is the
 * Task 2.3 defect back again in a new file. Paths are returned with `/` on every
 * platform, because they are keys into the vocabulary tables below.
 */
function walkFiles(directory: string, from: string = directory): string[] {
  const out: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) out.push(...walkFiles(path, from))
    else if (SOURCE_EXTENSIONS.some((extension) => entry.name.endsWith(extension))) {
      out.push(relative(from, path).split(sep).join('/'))
    }
  }
  return out
}

/** SCREEN_FILES are the screens, and they are the files that must each have a
 * sink call: an empty or renamed scan must fail rather than pass quietly. */
const SCREEN_FILES: readonly string[] = Object.freeze(
  walkFiles(SCREENS).sort((a, b) => a.localeCompare(b)),
)

/** SINK_FLOOR is the fewest sink calls a screen may have, for the one screen
 * whose drawing is smaller than that: `reading.ts` draws the panel's two
 * sentences (the placeholder and the line a slow read adds) and calls the sink
 * twice. Every other screen draws more, so the floor stays where it was -- the
 * case below is a non-vacuity guard, and lowering it everywhere to fit one
 * screen would be the weakened guard rather than the honest exception. */
const SINK_FLOOR: Readonly<Record<string, number>> = Object.freeze({ 'reading.ts': 1 })

/** SHARED_FILES are the modules outside `src/renderer/screens` that may draw --
 * `src/shared/notice.ts` writes the notice card's text slots, and
 * `src/shared/version.ts` composes the version-skew notice for it (Task 3.2).
 * They are scanned exactly as a screen is, but they are not required to have a
 * sink: most of them draw nothing at all. Their paths are relative to `SCREENS`
 * (`../../shared/notice.ts`), so every read and every vocabulary lookup in this
 * file finds them the way it finds a screen. */
const SHARED_FILES: readonly string[] = Object.freeze(
  walkFiles(SHARED, SCREENS).sort((a, b) => a.localeCompare(b)),
)

/** FILES is everything the scan reads: the screens, and the shared modules. */
const FILES: readonly string[] = Object.freeze([...SCREEN_FILES, ...SHARED_FILES])

/** RENDERER_FILES is every module under `src/renderer`, recursively, as a path
 * relative to it. It is the reach of the inline-style scan, and it is a walk
 * rather than a list of the two directories the screens live in for the reason
 * `walkFiles` exists: a module in a directory nobody listed is a module nobody
 * scanned. */
const RENDERER_FILES: readonly string[] = Object.freeze(
  walkFiles(RENDERER).sort((a, b) => a.localeCompare(b)),
)

/** SHARED_DRAWER is the shared module that draws today, kept as its own name
 * because the block below is about it: the notice card (`shared/notice.ts`),
 * which is the only place in `shared/` that writes a text slot any more. The
 * skew state is drawn **through** it rather than by its own writer, so the
 * module that composes the skew notice's text is `SKEW_MODULE` below and its
 * half of the rule is there. */
const SHARED_DRAWER = '../../shared/notice.ts'

/** SKEW_MODULE is `shared/version.ts`, which draws the version-skew state by
 * handing a spec to `SHARED_DRAWER`: it composes every string the card writes
 * and writes no text slot itself. */
const SKEW_MODULE = '../../shared/version.ts'

describe('a screen writes only its own vocabulary through literal', () => {
  it('reads the ten screens', () => {
    expect([...SCREEN_FILES]).toEqual([
      'coordinator.ts',
      'degraded.ts',
      'graph.ts',
      'launch.ts',
      'live.ts',
      'nocheckout.ts',
      'projects.ts',
      'reading.ts',
      'run.ts',
      'spend.ts',
    ])
  })

  it('reads the shared modules as well, so a drawer outside the screens is scanned', () => {
    expect([...SHARED_FILES]).toEqual([
      '../../shared/availability.ts',
      '../../shared/launcher.ts',
      '../../shared/notice.ts',
      '../../shared/replay.ts',
      '../../shared/rigor.ts',
      '../../shared/sanitize.ts',
      '../../shared/schema.ts',
      '../../shared/stringify.ts',
      '../../shared/version.ts',
    ])
    expect(FILES).toContain(SHARED_DRAWER)
    expect(FILES).toContain(SKEW_MODULE)
  })

  it('walks into subdirectories and reads every module extension (MUTATION)', () => {
    // The review's finding, as a test: with the one-level `.ts` listing this
    // walk replaces, none of these files was found -- a nested `range.ts`, a
    // `panel.tsx`, a `sink.mts`. The tree is a temporary directory rather than
    // the repository's own, so the test proves the walk without adding a file
    // to `src/` for the walk to find.
    const root = mkdtempSync(join(tmpdir(), 'fugaro-console-scan-'))
    mkdirSync(join(root, 'spend'), { recursive: true })
    mkdirSync(join(root, 'panels', 'deep'), { recursive: true })
    writeFileSync(join(root, 'live.ts'), '')
    writeFileSync(join(root, 'spend', 'range.ts'), '')
    writeFileSync(join(root, 'panels', 'deep', 'bar.mts'), '')
    writeFileSync(join(root, 'panel.tsx'), '')
    writeFileSync(join(root, 'notes.md'), '') // not a module: not scanned
    writeFileSync(join(root, 'spend', 'data.json'), '')
    expect(walkFiles(root).sort()).toEqual(['live.ts', 'panel.tsx', 'panels/deep/bar.mts', 'spend/range.ts'])
  })

  it('hands literal no document-controlled argument', () => {
    const found = FILES.flatMap((file) => offences(file, readFileSync(join(SCREENS, file), 'utf8')))
    expect(found, 'route these through putText (textCell), or add a reasoned CLOSED_VOCABULARY entry').toEqual([])
  })

  it('keeps no closed-vocabulary entry that nothing uses', () => {
    const stale: string[] = []
    for (const [file, entries] of Object.entries(CLOSED_VOCABULARY)) {
      const source = readFileSync(join(SCREENS, file), 'utf8')
      const code = blankComments(source)
      const calls = sinkCalls(source)
      for (const [argument, entry] of Object.entries(entries)) {
        if (!calls.some((call) => call.argument === argument && allows(entry, code, call.at))) stale.push(`${file}: ${argument}`)
      }
    }
    for (const [file, entries] of Object.entries(ATTRIBUTE_VOCABULARY)) {
      const code = blankComments(readFileSync(join(SCREENS, file), 'utf8'))
      const values = attributeCalls(code, code).map(({ at, args }) => ({ at, value: args[1] ?? '' }))
      for (const match of code.matchAll(ATTRIBUTE_PROPERTY)) {
        const end = code.indexOf('\n', match.index)
        values.push({ at: match.index, value: code.slice(match.index + match[0].length, end === -1 ? undefined : end).trim() })
      }
      for (const [value, entry] of Object.entries(entries)) {
        if (!values.some((one) => one.value === value && allows(entry, code, one.at))) stale.push(`${file}: attribute ${value}`)
      }
    }
    expect(stale).toEqual([])
  })

  it('finds a sink call in every screen, so an empty scan cannot pass', () => {
    for (const file of SCREEN_FILES) {
      expect(sinkCalls(readFileSync(join(SCREENS, file), 'utf8')).length, file).toBeGreaterThan(
        SINK_FLOOR[file] ?? 2,
      )
    }
  })

  it('fails on the defect it exists for (MUTATION)', () => {
    // The Task 2.3 defect, put back into a copy of the screen: a secret label's
    // key drawn through `literal`. And two variants the scan must also see: a
    // wire field split over lines, and one hidden behind a comment-like string.
    const source = readFileSync(join(SCREENS, 'projects.ts'), 'utf8')
    const mutants = [
      `${source}\nfunction m1(p: ScreenElement, f: ScreenFactory, label: { key: string }) {\n  literal(cell(p, f, 'span', 'x'), label.key)\n}\n`,
      `${source}\nfunction m2(p: ScreenElement, row: { repo: string }) {\n  literal(\n    p,\n    row.repo,\n  )\n}\n`,
      `${source}\nfunction m3(p: ScreenElement, f: ScreenFactory, row: { repo: string }) {\n  note(p, f, row.repo, 'a // b')\n}\n`,
      `${source}\nfunction m4(p: ScreenElement, row: { repo: string }) {\n  literal(p, \`seen \${row.repo}\`)\n}\n`,
    ]
    const before = offences('projects.ts', source).length
    for (const mutant of mutants) expect(offences('projects.ts', mutant)).toHaveLength(before + 1)
    // And a comment that mentions the call is not a call.
    const commented = `${source}\n// literal(p, row.repo)\n/* literal(p, row.repo) */\n`
    expect(offences('projects.ts', commented)).toHaveLength(before)
  })

  it('fails on the sinks a scan of literal( alone does not see (MUTATION)', () => {
    const source = readFileSync(join(SCREENS, 'projects.ts'), 'utf8')
    const before = offences('projects.ts', source).length
    const mutants: Record<string, string> = {
      // a document string assigned straight to a text slot, no helper at all
      textContent: `${source}\nfunction m5(p: ScreenElement, f: ScreenFactory, halt: { detail: string }) {\n  cell(p, f, 'span', 'x').textContent = halt.detail\n}\n`,
      innerHTML: `${source}\nfunction m6(p: { innerHTML: string }, row: { repo: string }) {\n  p.innerHTML = row.repo\n}\n`,
      // the sink under another name, so no literal( call carries the argument
      alias: `${source}\nconst say = literal\nfunction m7(p: ScreenElement, row: { repo: string }) {\n  say(p, row.repo)\n}\n`,
      destructure: `${source}\nconst { put } = { put: literal }\nfunction m8(p: ScreenElement, row: { repo: string }) {\n  put(p, row.repo)\n}\n`,
      // the identifier the note helper's own body is allowed, used elsewhere
      text: `${source}\nfunction m9(p: ScreenElement, text: string) {\n  literal(p, text)\n}\n`,
      // a document string into an attribute
      setAttribute: `${source}\nfunction m10(p: ScreenElement, row: { repo: string }) {\n  p.setAttribute('data-repo', row.repo)\n}\n`,
      dataset: `${source}\nfunction m11(p: { dataset: Record<string, string> }, row: { repo: string }) {\n  p.dataset.repo = row.repo\n}\n`,
      title: `${source}\nfunction m12(p: { title: string }, row: { repo: string }) {\n  p.title = row.repo\n}\n`,
      // The spellings the review of Task 2.3 named: each one writes a slot or
      // an attribute without ever spelling an assignment this scan reads by
      // name, and each must be an offence of its own.
      computedText: `${source}\nfunction m13(p: ScreenElement, row: { repo: string }) {\n  p['textContent'] = row.repo\n}\n`,
      computedTitle: `${source}\nfunction m14(p: ScreenElement, row: { repo: string }) {\n  p[\`title\`] = row.repo\n}\n`,
      computedAttribute: `${source}\nfunction m15(p: ScreenElement, row: { repo: string }) {\n  p['setAttribute']('data-repo', row.repo)\n}\n`,
      optionalAttribute: `${source}\nfunction m16(p: ScreenElement, row: { repo: string }) {\n  p.setAttribute?.('data-repo', row.repo)\n}\n`,
      assignedAttribute: `${source}\nfunction m17(p: ScreenElement, row: { repo: string }) {\n  const write = p.setAttribute\n  write.call(p, 'data-repo', row.repo)\n}\n`,
      objectAssign: `${source}\nfunction m18(p: ScreenElement, row: { repo: string }) {\n  Object.assign(p, { textContent: row.repo })\n}\n`,
      reflectSet: `${source}\nfunction m19(p: ScreenElement, row: { repo: string }) {\n  Reflect.set(p, 'textContent', row.repo)\n}\n`,
      defineProperty: `${source}\nfunction m20(p: ScreenElement, row: { repo: string }) {\n  Object.defineProperty(p, 'textContent', { value: row.repo })\n}\n`,
    }
    const missed = Object.entries(mutants)
      .filter(([, mutant]) => offences('projects.ts', mutant).length <= before)
      .map(([name]) => name)
    expect(missed, 'mutants the scan let through').toEqual([])
  })
})

/** SPEC_TEXT_PROPERTY is the properties that carry a notice's **text**: the
 * spec's title and reason, a fact's label and value, and a list row. It is read
 * where the text is composed rather than where it is written, because the card
 * writes it through a parameter (`detail.text`) and a parameter is all the scan
 * above can see. */
const SPEC_TEXT_PROPERTY = /(?:^|[{,;\s])(text|title|reason|label)\s*:\s*/gm

/** SPEC_TEXT_CALLS are the functions in the skew module that may compose a
 * notice's text: the console's own sentence for the handshake (`skewSentence`),
 * its own reason for a state (`noticeReason`), a version line through the
 * sanitizer (`versionCell` -> `versionText`) and a reader's own failure reason
 * through the sanitizer (`unreadableReason`). A fifth name is a decision, not a
 * rename: it is a new door a string from outside the console could take. */
const SPEC_TEXT_CALLS: readonly string[] = Object.freeze([
  'noticeReason',
  'skewSentence',
  'unreadableReason',
  'versionCell',
])

/** SPEC_TEXT_TABLES are the tables a spec's text may be read out of, **by
 * name**, and the list carries the same weight as `SPEC_TEXT_CALLS`: each entry
 * is a table of this console's own words keyed by a closed union
 * (`HIDDEN_VIEW_WORDS[view]`, `SKEW_HEADLINES[mode]`), so whatever key selects a
 * row the value is the console's. A name is not a form: `WORDS_BY_RUN[row.key]`
 * has the same shape and holds whatever a document put there, which is why the
 * rule reads the table's name and a fifth name is a decision rather than a
 * rename. */
const SPEC_TEXT_TABLES: readonly string[] = Object.freeze(['HIDDEN_VIEW_WORDS', 'SKEW_HEADLINES'])

/** valueAt is the source text of a property value at `at`: everything up to the
 * next comma, newline or closing brace that is not inside a call, a list or an
 * object -- which is where a spec's values end. */
function valueAt(code: string, at: number): string {
  let depth = 0
  let index = at
  for (; index < code.length; index += 1) {
    const char = code[index]
    if (char === '\'' || char === '"' || char === '`') {
      index = skipString(code, index) - 1
      continue
    }
    if (char === '(' || char === '[' || char === '{') depth += 1
    if (char === ')' || char === ']' || char === '}') {
      if (depth === 0) break
      depth -= 1
    }
    if (depth === 0 && (char === ',' || char === '\n')) break
  }
  return code.slice(at, index).trim()
}

/** specTextValues is every text-carrying property of `skewNoticeSpec`, as source
 * text, in file order. The function's body is the whole of the reach: a
 * `reason:` in a refusal type elsewhere in the file is not a string handed to
 * the card. */
function specTextValues(source: string): readonly string[] {
  const code = blankComments(source)
  const out: string[] = []
  for (const match of code.matchAll(SPEC_TEXT_PROPERTY)) {
    if (!within(code, 'skewNoticeSpec', match.index)) continue
    out.push(valueAt(code, match.index + match[0].length))
  }
  return out
}

/** allowedSpecText is whether a spec's text is the console's own by syntax: a
 * string literal, a constant, a row of one of `SPEC_TEXT_TABLES` (named, because
 * the name is the whole claim -- the table is the console's and its keys are a
 * closed union), one of `SPEC_TEXT_CALLS`, or an identifier this file binds to
 * one of them. Anything else -- a version line, a raw argv, a document field, a
 * table a document filled -- is a string that came from outside. */
function allowedSpecText(value: string, source: string): boolean {
  if (STRING_LITERAL.test(value) || CONSTANT.test(value)) return true
  const table = /^([A-Z][A-Z0-9_]*)\[[^\]]*\]$/.exec(value)
  if (table !== null && SPEC_TEXT_TABLES.includes(table[1] ?? '')) return true
  if (new RegExp(`^(?:${SPEC_TEXT_CALLS.join('|')})\\([^)]*\\)$`).test(value)) return true
  const code = blankComments(source)
  return new RegExp(`^[ \\t]*(?:const|let)\\s+${value}\\s*=\\s*(?:${SPEC_TEXT_CALLS.join('|')})\\(`, 'm').test(code)
}

describe('the shared drawers are held to the same rules', () => {
  const source = readFileSync(join(SCREENS, SHARED_DRAWER), 'utf8')
  const skew = readFileSync(join(SCREENS, SKEW_MODULE), 'utf8')

  it('hands literal no document-controlled argument and writes no text slot outside it', () => {
    // The same scan, the same vocabulary rules, the same refusal of every
    // bypass spelling -- over `src/shared/notice.ts`, the card both the reduced
    // banner and the refusal screen are drawn through (Task 3.2, design section
    // 2.14).
    expect(offences(SHARED_DRAWER, source)).toEqual([])
  })

  it('finds the shared drawer\'s sink calls, so the scan above cannot pass by finding nothing', () => {
    expect(sinkCalls(source).length).toBeGreaterThan(2)
  })

  it('fails on the same defects in that file (MUTATION)', () => {
    // One mutant per way in: a document string handed to the sink under a
    // parameter name, a text slot and an attribute written by hand, the sink
    // under another name, and the five spellings the review of Task 2.3 named.
    const before = offences(SHARED_DRAWER, source).length
    const mutants: Record<string, string> = {
      argument: `${source}\nfunction m1(p: NoticeElement, line: string) {\n  literal(p, line)\n}\n`,
      textContent: `${source}\nfunction m2(p: NoticeElement, line: string) {\n  cell(p, factory, 'span', ['x']).textContent = line\n}\n`,
      innerHTML: `${source}\nfunction m3(p: { innerHTML: string }, line: string) {\n  p.innerHTML = line\n}\n`,
      alias: `${source}\nconst say = literal\nfunction m4(p: NoticeElement, line: string) {\n  say(p, line)\n}\n`,
      setAttribute: `${source}\nfunction m5(p: NoticeElement, line: string) {\n  p.setAttribute('data-skew', line)\n}\n`,
      title: `${source}\nfunction m6(p: { title: string }, line: string) {\n  p.title = line\n}\n`,
      computedText: `${source}\nfunction m7(p: NoticeElement, line: string) {\n  p['textContent'] = line\n}\n`,
      computedAttribute: `${source}\nfunction m8(p: NoticeElement, line: string) {\n  p['setAttribute']('data-skew', line)\n}\n`,
      optionalAttribute: `${source}\nfunction m9(p: NoticeElement, line: string) {\n  p.setAttribute?.('data-skew', line)\n}\n`,
      assignedAttribute: `${source}\nfunction m10(p: NoticeElement, line: string) {\n  const write = p.setAttribute\n  write.call(p, 'data-skew', line)\n}\n`,
      objectAssign: `${source}\nfunction m11(p: NoticeElement, line: string) {\n  Object.assign(p, { textContent: line })\n}\n`,
      reflectSet: `${source}\nfunction m12(p: NoticeElement, line: string) {\n  Reflect.set(p, 'textContent', line)\n}\n`,
      defineProperty: `${source}\nfunction m13(p: NoticeElement, line: string) {\n  Object.defineProperty(p, 'textContent', { value: line })\n}\n`,
    }
    const missed = Object.entries(mutants)
      .filter(([, mutant]) => offences(SHARED_DRAWER, mutant).length <= before)
      .map(([name]) => name)
    expect(missed, 'mutants the scan let through').toEqual([])
  })

  it('composes the skew notice\'s text and writes no text slot of its own', () => {
    // `src/shared/version.ts` no longer writes a text slot: it hands the card a
    // spec, and the card writes it. That moves the whole question one step back
    // -- a raw version line passed as a `text` would reach `literal` through a
    // parameter the scan over sinks cannot see -- so the composition is held to
    // the rule here, where the text is chosen.
    expect(offences(SKEW_MODULE, skew)).toEqual([])
    expect(sinkCalls(skew), 'the skew module writes a text slot itself').toHaveLength(0)
    const values = specTextValues(skew)
    expect(values.length, 'the spec-text scan read no text at all').toBeGreaterThan(5)
    expect(values.filter((value) => !allowedSpecText(value, skew))).toEqual([])
  })

  it('fails on a string from outside handed to the card (MUTATION)', () => {
    // The four shapes the composition rule exists for: the version line the
    // binary printed, a line the reader echoed, a document field, and a table a
    // document filled -- which has the shape of the console's own tables
    // (`HIDDEN_VIEW_WORDS[view]`) and is refused because its name is not one of
    // `SPEC_TEXT_TABLES`. Each is injected **inside** `skewNoticeSpec`, because
    // outside it there is no spec.
    const anchor = '  const details: NoticeDetail[] = ['
    expect(skew, 'the mutant would land outside skewNoticeSpec').toContain(anchor)
    const mutants: Record<string, string> = {
      version: skew.replace(anchor, `  const leaked: NoticeDetail = { label: rawLabel, text: rawLine }\n${anchor}`),
      title: skew.replace(anchor, `  const leakedTitle = { label: rawLabel, title: rawTitle }\n${anchor}`),
      reason: skew.replace(anchor, `  const leakedReason = { label: rawLabel, reason: rawReason }\n${anchor}`),
      // The label is a literal so that the only thing this mutant can be caught
      // by is the table's name: `[A-Z][A-Z0-9_]*[...]` alone would have let it
      // through, and the row it selects is whatever the document wrote.
      table: skew.replace(anchor, `  const leakedRow = { label: 'x', text: WORDS_BY_RUN[row.key] }\n${anchor}`),
    }
    const missed = Object.entries(mutants)
      .filter(([, mutant]) => specTextValues(mutant).every((value) => allowedSpecText(value, mutant)))
      .map(([name]) => name)
    expect(missed, 'mutants the spec-text scan let through').toEqual([])
    // And the mutant is read as *more* text than the file has, so a scan that
    // stopped reading when it hit the injection cannot pass this case.
    for (const mutant of Object.values(mutants)) {
      expect(specTextValues(mutant).length).toBeGreaterThan(specTextValues(skew).length)
    }
  })
})

// ---------------------------------------------------------------------------
// No module under `src/renderer` writes an inline style (Task 6.2; design
// section 5.5).
// ---------------------------------------------------------------------------
//
// The `<link rel="stylesheet" href="./console.css">` Task 6.2 adds is admitted
// by `style-src 'self'`, and that is the whole of what the directive allows: a
// stylesheet **from this origin**. An inline style -- a `style` attribute, a
// `<style>` element, a `.style` property write, a `setAttribute('style', ...)`
// -- is not, and `csp-document.test.ts` refuses the first two in the served
// documents while this block refuses the last two in the modules that draw them.
//
// The scan reads every module under `src/renderer`, not the screens alone: the
// rule is about the renderer's output rather than about one directory, and the
// module that could grow a `.style` write is as likely to be `entry.ts` or a
// helper beside it as it is to be a screen.

describe('no module under src/renderer writes an inline style', () => {
  it('reads the whole renderer tree, so a module outside the screens is scanned', () => {
    // Non-vacuity, and the boundary of the claim: the files a narrowed scan
    // would miss are named, so a walk that had gone back to one directory fails
    // here rather than passing over fewer files.
    // The list is written out rather than counted, so a module added to the
    // renderer is a deliberate edit here -- which is the moment to ask whether
    // the new module draws. `src/renderer/theme/` holds `.css` and no module, so
    // the walk has nothing to find there.
    expect([...RENDERER_FILES]).toEqual([
      'app.ts',
      'checkout.ts',
      'confirm.ts',
      'entry.ts',
      'graph/layout-client.ts',
      'graph/layout.ts',
      'graph/model.ts',
      'installations.ts',
      'screens/coordinator.ts',
      'screens/degraded.ts',
      'screens/graph.ts',
      'screens/launch.ts',
      'screens/live.ts',
      'screens/nocheckout.ts',
      'screens/projects.ts',
      'screens/reading.ts',
      'screens/run.ts',
      'screens/spend.ts',
      'section.ts',
      'skeleton.ts',
      'writes.ts',
    ])
  })

  it('writes none', () => {
    const found = RENDERER_FILES.flatMap((file) =>
      styleOffences(file, readFileSync(join(RENDERER, file), 'utf8')),
    )
    expect(found, 'the theme travels as classes and data-* attributes, never as an inline style').toEqual([])
  })

  it('refuses a style entry in the attribute vocabulary, so one cannot be admitted', () => {
    // The other vocabularies in this file are lists of reasons a non-constant
    // value is the console's own word. There is no such reason for `style`: the
    // policy backs no style attribute at all, so an entry here would be a
    // permission this file has no business granting. The scan refuses the write
    // before it ever looks a vocabulary up (`styleOffences`), and this is the
    // belt to that brace.
    for (const [file, entries] of Object.entries(ATTRIBUTE_VOCABULARY)) {
      expect(Object.keys(entries), `${file} admits a style attribute`).not.toContain('style')
    }
    // And the control: the vocabulary is not empty, so the loop above is not
    // vacuous.
    expect(Object.keys(ATTRIBUTE_VOCABULARY).length).toBeGreaterThan(0)
  })

  it('fails on each way an inline style could arrive (MUTATION)', () => {
    // One mutant per spelling, run over a real module so the scan is proved on
    // the text it actually reads. The `withVocabulary` mutant is the one the
    // case above is about: even a reasoned entry on ATTRIBUTE_VOCABULARY cannot
    // admit it.
    const source = readFileSync(join(RENDERER, 'entry.ts'), 'utf8')
    const before = styleOffences('entry.ts', source).length
    const mutants: Record<string, string> = {
      propertyWrite: `${source}\nfunction m1(node: { style: { color: string } }) {\n  node.style.color = 'red'\n}\n`,
      wholeStyleObject: `${source}\nfunction m2(node: { style: string }) {\n  node.style = 'color: red'\n}\n`,
      styleMethod: `${source}\nfunction m3(node: { style: { setProperty(a: string, b: string): void } }) {\n  node.style.setProperty('color', 'red')\n}\n`,
      styleAttribute: `${source}\nfunction m4(node: ScreenElement) {\n  node.setAttribute('style', 'color: red')\n}\n`,
      styleAttributeDoubleQuoted: `${source}\nfunction m5(node: ScreenElement) {\n  node.setAttribute("style", 'color: red')\n}\n`,
      computedStyle: `${source}\nfunction m6(node: ScreenElement) {\n  node['style'].color = 'red'\n}\n`,
    }
    const missed = Object.entries(mutants)
      .filter(([, mutant]) => styleOffences('entry.ts', mutant).length <= before)
      .map(([name]) => name)
    expect(missed, 'mutants the scan let through').toEqual([])
    // A comment that mentions one is not one: the scan blanks comments first,
    // like every other scan in this file.
    const commented = `${source}\n// node.style.color = 'red'\n/* node.setAttribute('style', 'color: red') */\n`
    expect(styleOffences('entry.ts', commented)).toHaveLength(before)
    // And a string that spells one is not one either.
    const quoted = `${source}\nconst mention = "node.style.color = 'red'"\n`
    expect(styleOffences('entry.ts', quoted)).toHaveLength(before)
  })
})
