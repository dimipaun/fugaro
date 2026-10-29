// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// A filesystem path is not a token, and a scratch directory's random name is
// not a credential (owner's report, 2026-10-21, from the screenshot run).
//
// What the owner saw: the console's header drew the CLI's binary path as
// `[redacted-token]` (`acme-platform [redacted-token] 0.8.1 · ok`), and the e2e
// entry spec had failed the same way in PR #751, where the drawn header held
// `[redacted-token].local/bin/fugaro`. The path is the one the run installed its
// fake CLI in -- `mkdtemp(join(tmpdir(), 'fugaro-cli-stub-'))`, then
// `home/.local/bin/fugaro` (`test/support/cliStub.ts`) -- and it was redacted
// **because of what `mkdtemp` writes**: six characters mixing case and digits
// make a root-plus-space run answer `isHighEntropy`'s three questions (20+
// characters, upper case, lower case, a digit, 3.5 bits per character), so the
// entropy net read a path as a key. `.local` is where the redaction stopped:
// `ENTROPY_RE`'s lookarounds list the dot, so the run it took was the part
// before the first dot directory.
//
// Which rule did it, and where. The path rule (`ABS_PATH_RE`) runs before the
// net in the free-text classes, so `sanitizeText` drew `<path>` and the recorder
// drew `<path>`: neither of them has the defect. The defect is in the **refusal
// door** (`sanitizeRefusal`, which `src/renderer/entry.ts`'s `textOf` and
// `src/renderer/installations.ts`'s `drawn` put the header, the picker's rows and
// the CLI's own sentences through), and it is there by design: that door runs no
// path rule, because the operator's own paths are the remedy a refusal names, so
// nothing had replaced the path before the net saw it.
//
// The fix is one guard, on the net itself: a run that is a **path** -- the path
// rule's own pattern matches it whole (`PATH_RUN_RE`: `ABS_PATH_SOURCE`, the one
// definition, asked of the run rather than of the text, with a single trailing
// separator allowed because a run stops at the first dot directory) **and no word
// of it a candidate on its own** (`isPathRun`, `src/shared/sanitize.ts`) -- is not
// a run the net reads. The second half is what keeps the guard from being a hole:
// a name is words run together by separators while a token is one unbroken
// stretch, and a *token written inside a path* has a word that is a candidate by
// itself, so it is still redacted, whole run and all -- which is the invariant
// `split-secrets.test.ts` pins for this door, and the case below that holds this
// guard to it. The first half is the path rule's question and not a weaker one:
// the guard read "rooted, with a second slash behind it" at first, and a run with
// an *empty* segment between its slashes is not a path the path rule can match, so
// that reading swallowed a run the net had redacted before -- the reviewer's
// finding, and the case below that holds the guard to the pattern.
//
// The cases are then the two halves the owner asked for: the paths that MUST
// come out as written (the header's own, built at run time the way the run built
// them, mixed case and all), and the secrets that MUST still be replaced --
// every family, alone and beside a path and glued into one and standing as a
// word of one.
//
// **No line of this file holds a secret-shaped or random-looking run**: the
// scratch suffix and every credential come from `test/support/fakeSecrets.ts`
// or are assembled from fragments here, for the reason that module gives.

import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { PATH_MARKER, TOKEN_MARKER, createRedactor } from '../../src/source/redact'
import {
  PATH_PLACEHOLDER,
  TOKEN_PLACEHOLDER,
  sanitizeRefusal,
  sanitizeText,
} from '../../src/shared/sanitize'
import * as fake from '../support/fakeSecrets'

/**
 * scratch is the run a template appends to a temporary directory's name, in the
 * shape `mkdtemp` writes it: six characters mixing case and digits
 * (`test/support/cliStub.ts` calls it with `'fugaro-cli-stub-'`). It is
 * assembled from fragments rather than written down, because a random-looking
 * run is exactly the string this repository keeps out of its lines
 * (`test/support/fakeSecrets.ts` makes the same argument for credentials).
 */
function scratch(): string {
  return ['aB3', 'xY9'].join('')
}

/**
 * PATHS are the paths the header, the picker and the CLI's refusals carry: the
 * scratch tree `test/support/cliStub.ts` installs a fake CLI in
 * (`mkdtemp(join(tmpdir(), 'fugaro-cli-stub-'))`, which is the one the owner's
 * header drew, and which PR #751's e2e spec had failed on), macOS's `TMPDIR`
 * shape (`/var/folders/<2>/<mixed>/T/...`), and the scratch config file the
 * console reads.
 */
const PATHS: readonly (readonly [string, string])[] = [
  ['a scratch bin directory', join('/tmp', `fugaro-cli-stub-${scratch()}`, 'bin', 'fugaro')],
  [
    'the scratch home’s well-known prefix',
    join('/tmp', `fugaro-cli-stub-${scratch()}`, 'home', '.local', 'bin', 'fugaro'),
  ],
  [
    'macOS TMPDIR',
    join(
      '/var/folders',
      'w1',
      '5v5rrgy13h1g3crrq_yx_mcm0000gn',
      'T',
      `fugaro-cli-stub-${scratch()}`,
      'bin',
      'fugaro',
    ),
  ],
  [
    'macOS TMPDIR, through the well-known prefix',
    join(
      '/var/folders',
      'w1',
      '5v5rrgy13h1g3crrq_yx_mcm0000gn',
      'T',
      `fugaro-cli-stub-${scratch()}`,
      'home',
      '.local',
      'bin',
      'fugaro',
    ),
  ],
  [
    'a scratch config file',
    join(
      '/var/folders',
      'w1',
      '5v5rrgy13h1g3crrq_yx_mcm0000gn',
      'T',
      `fugaro-cli-stub-${scratch()}`,
      'home',
      '.config',
      'fugaro',
      'fugaro.yaml',
    ),
  ],
]

/**
 * word is one segment of a name, and it is deliberately **under the net's `{20,}`
 * floor**: a run built out of words this long is a candidate only as a whole, so
 * what happens to it is the guard's decision and nothing else. Assembled from
 * fragments for the reason `scratch` is.
 */
function word(): string {
  return ['aB3', 'xY9mQ2', 'pL7vN'].join('')
}

/**
 * spelled is a blob spelled with separators -- the shape a base64url body has
 * with a `-` or `_` in it -- assembled from fragments for the reason `scratch`
 * is. It is the one run the guard reads as a name, and the case at the end of
 * this file says so out loud.
 */
function spelled(): string {
  return ['xY3k', '9pQz', 'Lm', '8vBn', '2wEr', 'Q7'].join('-')
}

/** refusal is the refusal door's answer as the screens draw it. */
function refusal(value: string): string {
  const result = sanitizeRefusal(value, 'the installation’s binary')
  return result.ok ? result.text : `refused: ${result.reason}`
}

/** sentence is the `sentence` class's answer, as a screen draws it. */
function sentence(value: string): string {
  const result = sanitizeText(value, 'sentence', 'the value')
  return result.ok ? result.text : `refused: ${result.reason}`
}

/** leaks answers whether `out` holds a window of `secret` that the marker does
 * not account for -- "no fragment of this token survived". */
function leaks(out: string, secret: string): string | null {
  for (let at = 0; at + 8 <= secret.length; at += 1) {
    const window = secret.slice(at, at + 8)
    if (out.includes(window)) return window
  }
  return null
}

describe('the header’s path is not a token', () => {
  it('draws a path as written through the refusal door, whatever its segments hold', () => {
    for (const [what, path] of PATHS) {
      expect(refusal(path), what).toBe(path)
      // And in the sentence a refusal arrives in: the path in place, unchanged.
      expect(refusal(`the binary ${path} could not be read`), what).toBe(
        `the binary ${path} could not be read`,
      )
    }
  })

  it('carries no flag for a path, because nothing was changed', () => {
    // The flag is the reason a screen may mark a value as changed, so a path
    // that comes back written must come back unflagged -- a `neutralised-
    // credential` here would be the console claiming a credential it did not
    // find (the false statement `sanitize.test.ts` argues against).
    for (const [, path] of PATHS) {
      const result = sanitizeRefusal(path, 'the installation’s binary')
      if (!result.ok) throw new Error(`refused: ${result.reason}`)
      expect(result.flags).toEqual([])
    }
  })

  it('still replaces a path in the free-text classes and in the recorder', () => {
    // The other two doors run the path rule first, so they already drew
    // `<path>`; the guard must not have loosened them, because a path in a
    // sentence is where the operator's user name and checkout live.
    //
    // The two markers are the same string, spelled by the two modules that own
    // them (`sanitize.test.ts` holds the whole marker set equal).
    expect(PATH_PLACEHOLDER).toBe(PATH_MARKER)
    const recorder = createRedactor()
    for (const [what, path] of PATHS) {
      expect(sentence(path), what).toBe(PATH_PLACEHOLDER)
      expect(sentence(`the binary ${path} could not be read`), what).toBe(
        `the binary ${PATH_PLACEHOLDER} could not be read`,
      )
      const structured = sanitizeText(path, 'path', 'the value')
      expect(structured.ok ? structured.text : 'refused', what).toBe(PATH_PLACEHOLDER)
      expect(recorder.redactText(path), what).toBe(PATH_MARKER)
    }
  })
})

describe('a real credential is still replaced', () => {
  it('neutralises every family the recorder knows, alone and beside a path', () => {
    for (const [name, secret] of fake.realisticSecrets()) {
      for (const [where, line] of [
        ['alone', secret],
        ['beside a path', `the binary ${PATHS[0]?.[1] ?? ''} printed ${secret} here`],
      ] as const) {
        const out = refusal(line)
        expect(out, `${name} / ${where}`).toContain(TOKEN_PLACEHOLDER)
        expect(leaks(out, secret), `${name} / ${where}: ${JSON.stringify(out)}`).toBeNull()
      }
    }
  })

  it('neutralises a token family written into a path, where the shape rules read it', () => {
    // The net's guard is structural and comes last; every shape rule runs before
    // it, so a token written *into* a path is still replaced -- the run the net
    // would have judged is broken by the replacement before the net reads it.
    const token = fake.githubToken()
    expect(refusal(`/tmp/${scratch()}/no/${token} denied`)).toBe(
      `/tmp/${scratch()}/no/${TOKEN_PLACEHOLDER} denied`,
    )
  })

  it('does not let the guard widen: a token-shaped part of a path is still redacted', () => {
    // **This is the case that fixes the guard's shape.** The first cut of it
    // read "rooted with a second slash is a path" and struck down
    // `read /home/x/<key>/key failed` whole -- a leak `split-secrets.test.ts`
    // had already pinned against, at every split position and every splitter.
    // What tells the two apart is not the segment but the **word**: a key is one
    // unbroken stretch of the net's alphabet, a path's segment is words run
    // together, and the separators (`-`, `_`, `/`) are run characters to
    // `ENTROPY_RE`, never boundaries. So a run holding a word that is a
    // candidate by itself is not a path, and goes whole, as it always did.
    // `fake.highEntropyRun()` is the family `split-secrets.test.ts` splits at
    // every position and drops into a path: mixed case and digits, and no
    // separators, so it is one word, and the run stays a token.
    const key = fake.highEntropyRun()
    expect(refusal(`read /home/x/${key}/key failed`)).toBe(`read ${TOKEN_PLACEHOLDER} failed`)
    expect(sentence(`read /home/x/${key}/key failed`)).toBe(`read ${PATH_PLACEHOLDER} failed`)
  })

  it('redacts a run the path rule would never take: the guard asks the path rule’s pattern', () => {
    // **The case the review round added, and the reason the guard's first half is
    // the path rule's own pattern and not a weaker question.** A root and a second
    // slash are one run to the net even when the segment between them is *empty*,
    // so a guard reading "rooted, with a second slash behind it" took
    // `//<word>/<word>` -- a run `ABS_PATH_RE` cannot match at all -- for a path.
    // The net had redacted it before the guard existed and the sentence door drew
    // it written afterwards, while the recorder, which runs no guard, still
    // redacted it: a redaction lost, and the two layers disagreeing over a string
    // `sanitize-agreement.test.ts` says they cannot disagree about. Each `word` is
    // under the net's floor, so no word of the run is a candidate and the guard's
    // second half cannot save the first.
    const doubled = `//${word()}//${word()}`
    expect(sentence(`wrote ${doubled} here`)).toBe(`wrote ${TOKEN_PLACEHOLDER} here`)
    expect(refusal(`wrote ${doubled} here`)).toBe(`wrote ${TOKEN_PLACEHOLDER} here`)
    expect(createRedactor().redactText(`wrote ${doubled} here`)).toBe(
      `wrote ${TOKEN_PLACEHOLDER} here`,
    )
  })

  it('keeps redacting a high-entropy run that is not a path', () => {
    // The boundary the guard must not cross: a run of the net's own alphabet
    // that is *not* rooted with a second segment is a token candidate exactly as
    // before -- one slash, or none at all.
    for (const run of [
      fake.highEntropyRun(),
      fake.hexDigest(),
      `/${fake.highEntropyRun()}`,
      `stub/${fake.highEntropyRun()}`,
    ]) {
      expect(refusal(`wrote ${run} here`), run).toBe(`wrote ${TOKEN_PLACEHOLDER} here`)
    }
  })

  it('draws a blob spelled with separators inside a path, and this is the trade', () => {
    // **Said out loud, because it is the price of the fix.** The guard reads a
    // name as words run together, so a random body *spelled* as words -- a
    // base64url run with a `-` or `_` in it, written into a path -- reads as a
    // name, and the refusal door draws it as written. What bounds it: the
    // free-text classes and the recorder still redact such a run whole
    // (`<path>`), every shape rule (token families, bearer lines, PEM blocks,
    // assignments, percent-encoded and base64-wrapped candidates) still runs
    // before the net in the refusal door too, and the alternative is where this
    // started -- the operator's own binary path drawn as `[redacted-token]`, a
    // mark that says a credential was found where none was.
    const run = `/tmp/${scratch()}/${spelled()}`
    expect(refusal(`wrote ${run} here`)).toBe(`wrote ${run} here`)
    expect(sentence(`wrote ${run} here`)).toBe(`wrote ${PATH_PLACEHOLDER} here`)
    expect(createRedactor().redactText(`wrote ${run} here`)).toBe(`wrote ${PATH_PLACEHOLDER} here`)
  })
})
