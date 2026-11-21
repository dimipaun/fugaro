// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The readers every Manage kind decodes a document with (the console-improvements
// plan's task MO-C; the descriptor is `../types.ts` and the prose is
// `docs/console-improvements/manage-kinds.md`).
//
// **Why this file exists.** A kind's `decode` is handed `unknown` and is the
// only place its document's shape is known (MG0, `ManageKind.decode`), so each
// kind reads members by name -- and there are three kinds in the Code group
// alone, reading three documents with the same handful of questions between
// them. Written into the kind files, the answers to "what is the string at this
// key" and "which objects is this array holding" would be three near-copies, and
// the second one would be the one that got a case wrong.
//
// **The one rule every reader follows: a member the kind did not read is
// absent, never inferred.** A value of the wrong JSON type, a key the document
// does not hold, an array holding something that is not an object -- each reads
// as the neutral value for its kind (the empty string, false, an empty list) and
// never as a coercion of whatever was there. That is MG0's rule for the rows a
// kind decodes ("a member the kind did not read is not drawn rather than drawn
// as whatever was in the document"), one level down: a number drawn as its
// string, or an object drawn as `[object Object]`, would be a cell nobody wrote.
// Nothing here refuses the document -- whether a row is complete enough to draw
// is the kind's question and the shell's, and a reader that threw would take a
// whole section down over one odd member.

/** JsonObject is a document, or a member of one, as these readers take it: the
 * plain object a CLI document decodes to, with members nobody has checked. */
export type JsonObject = Readonly<Record<string, unknown>>

/** objectAt is the object at `key`, or `null` for a member that is missing or
 * is not an object. The null is what lets a kind tell a document that does not
 * hold a part from one that holds an empty one: `local: {}` and no `local` are
 * different documents, and only the first is a half of the enrolment. */
export function objectAt(document: JsonObject, key: string): JsonObject | null {
  const value = document[key]
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null
  return value as JsonObject
}

/** textAt is the string at `key`, or the empty string. A number and a boolean
 * read as absent rather than as their spelling: a cell is text a document sent,
 * and `String(1)` would be text this console wrote. */
export function textAt(document: JsonObject, key: string): string {
  const value = document[key]
  return typeof value === 'string' ? value : ''
}

/** boolAt is the boolean at `key`, false for anything else -- including for a
 * missing member, which is why every caller that needs to tell the two apart
 * reads the member's own presence (a status row's `severity`) rather than this. */
export function boolAt(document: JsonObject, key: string): boolean {
  return document[key] === true
}

/** hasAt reports whether the document holds `key` at all, whatever it holds.
 * It is the reader for the members a document writes only when it means
 * something (`fix`, `severity`, `problem`), where absent is the answer. */
export function hasAt(document: JsonObject, key: string): boolean {
  return Object.hasOwn(document, key)
}

/** valuesAt is the non-null values of the array at `key`, or an empty list. The
 * list is of `unknown` because the caller knows what its own array holds: a
 * repository's `workflows` is an array of objects and its `claude_accounts` is
 * an array of strings, and a reader that decided here would be deciding for
 * both. */
export function valuesAt(document: JsonObject, key: string): readonly unknown[] {
  const value = document[key]
  return Array.isArray(value) ? value : []
}

/** objectsAt is the objects of the array at `key`, in the document's own order,
 * with any member that is not an object dropped rather than drawn as an empty
 * one: a row a kind cannot read a single member of is a row that would draw as
 * a table line of blanks. */
export function objectsAt(document: JsonObject, key: string): readonly JsonObject[] {
  const out: JsonObject[] = []
  for (const value of valuesAt(document, key)) {
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      out.push(value as JsonObject)
    }
  }
  return out
}

/** stringsAt is the strings of the array at `key`, in the document's own order,
 * with anything else dropped. A list a kind draws as one cell (a repository's
 * account names, an entry's tags) goes through the class its column declares
 * afterwards, so what is dropped here is a member of the wrong type and never a
 * value a class refused. */
export function stringsAt(document: JsonObject, key: string): readonly string[] {
  const out: string[] = []
  for (const value of valuesAt(document, key)) {
    if (typeof value === 'string') out.push(value)
  }
  return out
}
