// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// stringifyDeep: `JSON.stringify`'s output, without its recursion.
//
// Shared by the fixture writer and the replay buffer's export, whose bodies are
// one format and must be one serializer (design section 9.2: the replay
// buffer's export doubles as the fixture format).

/**
 * primitive is the JSON text of a value that is not an object, or `undefined`
 * for a value `JSON.stringify` would leave out (an `undefined`, a function, a
 * symbol) -- which is how a property drops out of an object and how a hole
 * becomes `null` in an array.
 */
function primitive(value: unknown): string | undefined {
  if (value === null) return 'null'
  if (typeof value === 'boolean') return value ? 'true' : 'false'
  if (typeof value === 'number') return Number.isFinite(value) ? JSON.stringify(value) : 'null'
  if (typeof value === 'string') return JSON.stringify(value)
  return undefined
}

/**
 * resolve applies `toJSON` the way `JSON.stringify` does: once, to the value as
 * written, on any value that carries one. A `toJSON` that returns `undefined`
 * therefore leaves the property out, exactly as `JSON.stringify` does.
 */
function resolve(value: unknown): unknown {
  if (value === null || typeof value !== 'object') return value
  const holder = value as { toJSON?: unknown }
  if (typeof holder.toJSON !== 'function') return value
  return (holder.toJSON as () => unknown)()
}

/**
 * stringifyDeep serialises a document with an **explicit stack** instead of by
 * recursion, and its output is byte for byte what `JSON.stringify` produces for
 * the same value.
 *
 * It exists because the input is not always sane: a record's document is a
 * hostile field like every other, and a line nested ten thousand deep is a
 * thousand times past the depth at which V8's recursive serialiser throws
 * `RangeError: Maximum call stack size exceeded`. The redactor refuses such a
 * document (`RedactionRefusal`, "nested deeper than"), and so does the loader
 * (`FixtureRefusal`) -- but a writer that *crashes* on the way between them is a
 * writer that turns a refused document into a lost process, and "hostile shapes
 * are refusals, not crashes" is the property `redact-persons.test.ts` holds the
 * fixture module to.
 *
 * It lives here, in `src/shared/`, because it has two callers that must write
 * the same bytes: `writeFixture` (`src/source/fixture.ts`, main process) and
 * `ReplayBuffer.export()` (`src/shared/replay.ts`, which the renderer loads and
 * which therefore cannot import a module that pulls in `node:crypto`).
 *
 * `toJSON` is honoured, because a value that carries one would otherwise
 * serialise as `{}` and silently change a fixture's bytes.
 */
export function stringifyDeep(root: unknown): string {
  // A frame is either a value already through `toJSON` (serialise it as it
  // stands) or a run of text to copy out. The stack is the whole state, so
  // depth costs heap rather than call frames.
  type Frame = { readonly value: unknown } | { readonly text: string }
  const out: string[] = []
  const stack: Frame[] = [{ value: resolve(root) }]
  while (stack.length > 0) {
    const frame = stack.pop() as Frame
    if ('text' in frame) {
      out.push(frame.text)
      continue
    }
    const value = frame.value
    const size = primitive(value)
    if (size !== undefined) {
      out.push(size)
      continue
    }
    if (Array.isArray(value)) {
      if (value.length === 0) {
        out.push('[]')
        continue
      }
      out.push('[')
      stack.push({ text: ']' })
      for (let index = value.length - 1; index >= 0; index -= 1) {
        // An element `JSON.stringify` would leave out becomes `null` here.
        const child = resolve(value[index])
        stack.push(typeof child === 'object' && child !== null ? { value: child } : { text: primitive(child) ?? 'null' })
        if (index > 0) stack.push({ text: ',' })
      }
      continue
    }
    const entries = Object.entries(value as Record<string, unknown>)
      .map(([key, child]) => [key, resolve(child)] as const)
      // A property `JSON.stringify` would leave out is left out here too.
      .filter(([, child]) => primitive(child) !== undefined || (typeof child === 'object' && child !== null))
    if (entries.length === 0) {
      out.push('{}')
      continue
    }
    out.push('{')
    stack.push({ text: '}' })
    for (let index = entries.length - 1; index >= 0; index -= 1) {
      const entry = entries[index] as readonly [string, unknown]
      stack.push({ value: entry[1] })
      stack.push({ text: `${JSON.stringify(entry[0])}:` })
      if (index > 0) stack.push({ text: ',' })
    }
  }
  return out.join('')
}
