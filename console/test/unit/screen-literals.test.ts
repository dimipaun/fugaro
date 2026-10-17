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
// The mutation the test is proven against is the defect itself: put
// `literal(..., label.key)` back into `projects.ts` and the first case fails
// naming it (`MUTATION` below runs the same scan over a doctored copy).

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

const SCREENS = join(__dirname, '..', '..', 'src', 'renderer', 'screens')

/** SINKS are the functions whose argument is written to the screen unsanitized,
 * with the index of that argument. `note` is `projects.ts`'s wrapper around
 * `literal`, so its call sites are sinks of their own. */
const SINKS: Readonly<Record<string, number>> = Object.freeze({ literal: 1, note: 2 })

/**
 * CLOSED_VOCABULARY is every non-constant argument a screen hands a sink, with
 * why it is the console's own word. Keyed by file, then by the argument's exact
 * source text.
 */
const CLOSED_VOCABULARY: Readonly<Record<string, Readonly<Record<string, string>>>> = Object.freeze({
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
    text: 'the parameter of the `note` helper itself; its call sites are checked as a sink (SINKS)',
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
 * string is not a comment. */
function blankComments(source: string): string {
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
      out += source.slice(start, index)
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
      out += source.slice(start, index)
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

const STRING_LITERAL = /^(?:'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*")$/
const PLAIN_TEMPLATE = /^`(?:[^`\\$]|\\.|\$(?!\{))*`$/
const CONSTANT = /^[A-Z][A-Z0-9_]*$/
const STRING_TERNARY =
  /^[\w.!]+\s*\?\s*(?:'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*")\s*:\s*(?:'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*")$/

/** SinkCall is one argument a screen hands a sink. */
interface SinkCall {
  readonly sink: string
  readonly line: number
  readonly argument: string
}

/** sinkCalls finds every call to a sink in one file's source, skipping the
 * sinks' own declarations. */
function sinkCalls(source: string): SinkCall[] {
  const code = blankComments(source)
  const found: SinkCall[] = []
  for (const [sink, position] of Object.entries(SINKS)) {
    const pattern = new RegExp(`(?<![\\w.$])${sink}\\(`, 'g')
    for (const match of code.matchAll(pattern)) {
      const at = match.index
      if (/function\s+$/.test(code.slice(Math.max(0, at - 20), at))) continue
      const args = argumentsAt(code, at + sink.length)
      const argument = args[position]
      if (argument === undefined) throw new Error(`${sink}( with no argument ${String(position)}`)
      found.push({ sink, line: code.slice(0, at).split('\n').length, argument })
    }
  }
  return found
}

/** isConstantArgument is the shapes that are the console's words by syntax. */
function isConstantArgument(argument: string, source: string): boolean {
  if (STRING_LITERAL.test(argument) || PLAIN_TEMPLATE.test(argument) || STRING_TERNARY.test(argument)) return true
  if (CONSTANT.test(argument)) {
    return new RegExp(`^(?:export )?const ${argument}\\b`, 'm').test(source)
  }
  return false
}

/** offences is every sink argument in a file that is neither a constant shape
 * nor on that file's closed-vocabulary list. */
function offences(file: string, source: string): string[] {
  const allowed = CLOSED_VOCABULARY[file] ?? {}
  return sinkCalls(source)
    .filter((call) => !isConstantArgument(call.argument, source) && !(call.argument in allowed))
    .map((call) => `${file}:${String(call.line)} ${call.sink}(..., ${call.argument})`)
}

const FILES = readdirSync(SCREENS).filter((name) => name.endsWith('.ts'))

describe('a screen writes only its own vocabulary through literal', () => {
  it('reads the four screens', () => {
    expect(FILES.sort()).toEqual(['live.ts', 'projects.ts', 'run.ts', 'spend.ts'])
  })

  it('hands literal no document-controlled argument', () => {
    const found = FILES.flatMap((file) => offences(file, readFileSync(join(SCREENS, file), 'utf8')))
    expect(found, 'route these through putText (textCell), or add a reasoned CLOSED_VOCABULARY entry').toEqual([])
  })

  it('keeps no closed-vocabulary entry that nothing uses', () => {
    const stale: string[] = []
    for (const [file, entries] of Object.entries(CLOSED_VOCABULARY)) {
      const used = new Set(sinkCalls(readFileSync(join(SCREENS, file), 'utf8')).map((call) => call.argument))
      for (const argument of Object.keys(entries)) if (!used.has(argument)) stale.push(`${file}: ${argument}`)
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
})
