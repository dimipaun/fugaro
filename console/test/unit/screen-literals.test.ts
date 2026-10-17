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
//     helper's own parameter is allowed there and nowhere else.
//
// The mutation the test is proven against is the defect itself: put
// `literal(..., label.key)` back into `projects.ts` and the first case fails
// naming it (`MUTATION` below runs the same scan over a doctored copy), and one
// doctored copy per way around the `literal(` scan.

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

const SCREENS = join(__dirname, '..', '..', 'src', 'renderer', 'screens')

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
    'state.operation': '`OperationName`, the closed IPC allow-list union: the read the console asked for, in the console\'s own words',
    'state.sentence':
      'the CLI\'s own refusal sentence on the `SourceRefusal` channel (`src/source/source.ts`), not a document string: design section 6.3 requires it verbatim, and the free-text rules would draw `roles/datastore.viewer` as `[redacted]` and the project as `<project>` -- `degraded.test.ts` asserts both halves of that',
  },
  'live.ts': {
    'flag.name': 'a RunFlag is built only from live.ts\'s own flag table (`runFlags`); an unknown health or deadline is the console\'s "unknown ..." flag, never the document\'s word',
  },
  'spend.ts': {
    'entry.dimension': 'the switch is `SPEND_DIMENSIONS.map(...)`: the seven words are the console\'s table, and an unknown document dimension is drawn as "an unknown breakdown"',
    'series.title': '`ACCOUNT_TITLES[account]`, the console\'s headings',
    'scope.name': '`scopeOf` maps the wire word through `SCOPES` and draws `UNKNOWN_SCOPE_NAME` for a word it does not know',
    note: 'model.notes holds only the console\'s own sentences (`buildSpendModel`), the one interpolation being a count the console computed',
  },
  'projects.ts': {
    text: { within: 'note', reason: 'the parameter of the `note` helper itself; its call sites are checked as a sink (SINKS)' },
    'model.secrets.note': '`SECRETS_ARRAY_NOTE`, a console constant',
    'panel.title': 'every panel constructor is called with a string literal title',
    'panel.source': '`PROJECTS_SOURCE_LINES[operation]`, a named console source line, or `fugaro ${operation}` over the closed `OperationName` union',
    'row.scope': '`ProjectCapScope`, a closed union the model assigns (`global`, `defaults`, `repo`)',
    'row.dailyCapSource': '`capSourceAt` keeps a word only when it is on `CAP_SOURCES`, else `unknown`',
    'row.perRunCapSource': '`capSourceAt` keeps a word only when it is on `CAP_SOURCES`, else `unknown`',
  },
  'run.ts': {
    'stage.deadline': '`deadlineState` keeps a word only when it is on `DEADLINE_STATES`, else null',
    '`#${String(model.number)}`': 'a pull request number the model read as a finite integer (`prNumberOf`)',
    'model.because': 'one of two console sentences (`PR_NO_LINK_REASON` or the no-pull-request one)',
    'model.linkBecause': '`LOG_NO_LINK`, a console constant',
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
    'sanitizedKey(repo.slug)': 'the slug as `sanitizeText` drew it (slug class)',
    "repo.kill === null ? 'false' : 'true'": 'a boolean drawn as one of two literals',
    "connection.textContent ?? ''": 'read back from a cell `putText` wrote, so it is the sanitized text',
    "formatReading(feedReading(feed), 'seconds')": '`formatReading` of a Reading: the console\'s own number formatting or its unavailable mark',
    "model.feed === null ? '' : (model.feed.since ?? '')":
      'the arrival stamp `src/source/cli.ts` wrote on the frame (or the empty string when the caller keeps no feed reading), used as a key rather than as text',
    theme: { within: 'renderLive', reason: 'a `LiveTheme`, the closed theme tuple' },
  },
  'projects.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    'panel.key': '`ProjectsPanelKey`, the closed `PROJECT_PANEL_KEYS` tuple',
    'panel.state': '`ProjectPanelState`, a closed union the model assigns',
    'panel.operation': '`OperationName`, the closed IPC allow-list union',
    'row.scope': '`ProjectCapScope`, a closed union the model assigns (`global`, `defaults`, `repo`)',
    slug: { within: 'capRowLine', reason: '`sanitizedKey(row.slug)`: the slug as `sanitizeText` drew it, null when refused' },
    theme: { within: 'renderProjects', reason: 'a `ProjectsTheme`, the closed theme tuple' },
  },
  'degraded.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    "model.caps.state": 'a Reading\'s `state`, the closed `ReadingState` union',
    "sanitizedKey(row.run)": 'the run id as `sanitizeText` drew it (runId class), empty when refused',
    kind: { within: 'root', reason: 'the closed discriminant of this file\'s states (`no-firebase`, `no-binary`, `refused`), written as a key' },
    "receivedAt === null ? '' : receivedAt": 'the arrival stamp `src/source/cli.ts` wrote on the frame, or the empty string when the caller keeps no feed reading; a key rather than text',
    theme: { within: 'root', reason: 'a `LiveTheme`, the closed theme tuple' },
  },
  'run.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    theme: { within: 'renderRun', reason: 'a `RunTheme`, the closed theme tuple' },
  },
  'spend.ts': {
    "classes.join(' ')": { within: 'make', reason: 'the class list of the `make` helper; every call site passes string-literal class names' },
    'entry.dimension': '`SPEND_DIMENSIONS`, the console\'s table',
    'entry.note': '`PERSON_OFF_NOTE`, a console constant, or null',
    'drawn.reading.state': 'a Reading\'s `state`, the closed `ReadingState` union',
    "formatReading(drawn.height, 'percent')": '`formatReading` of a Reading: the console\'s own number formatting or its unavailable mark',
    'drawnKey(keyed)': 'read back from a cell `putText` wrote, so it is the sanitized text',
    'series.account': '`SpendAccount`, a closed union',
    "formatReading(series.peak, 'usd')": '`formatReading` of a Reading: the console\'s own number formatting or its unavailable mark',
    'String(series.segments)': 'a count the model computed',
    'none.state': 'a Reading\'s `state`, the closed `ReadingState` union',
    "formatReading(none, 'percent')": '`formatReading` of a Reading: the console\'s own number formatting or its unavailable mark',
    'drawnKey(name)': 'read back from a cell `putText` wrote, so it is the sanitized text',
    "scope.known ? scope.source : 'unknown'": 'a known scope\'s word is on the console\'s `SCOPES` table; an unknown one is drawn as `unknown`',
    theme: { within: 'renderSpend', reason: 'a `SpendTheme`, the closed theme tuple' },
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
  for (const { at, args } of attributeCalls(code)) {
    const value = args[1] ?? ''
    if (isConstantArgument(value, source) || allows(attributes[value], code, at)) continue
    out.push(`${file}:${String(lineOf(code, at))} setAttribute(..., ${value})`)
  }
  return out
}

/** attributeCalls is every `.setAttribute(` call, with its arguments. */
function attributeCalls(code: string): { at: number; args: string[] }[] {
  const found: { at: number; args: string[] }[] = []
  for (const match of code.matchAll(/\.setAttribute\(/g)) {
    found.push({ at: match.index, args: argumentsAt(code, match.index + '.setAttribute'.length) })
  }
  return found
}

const FILES = readdirSync(SCREENS).filter((name) => name.endsWith('.ts'))

describe('a screen writes only its own vocabulary through literal', () => {
  it('reads the five screens', () => {
    expect(FILES.sort()).toEqual(['degraded.ts', 'live.ts', 'projects.ts', 'run.ts', 'spend.ts'])
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
      const values = attributeCalls(code).map(({ at, args }) => ({ at, value: args[1] ?? '' }))
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
    for (const file of FILES) {
      expect(sinkCalls(readFileSync(join(SCREENS, file), 'utf8')).length, file).toBeGreaterThan(2)
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
    }
    const missed = Object.entries(mutants)
      .filter(([, mutant]) => offences('projects.ts', mutant).length <= before)
      .map(([name]) => name)
    expect(missed, 'mutants the scan let through').toEqual([])
  })
})
