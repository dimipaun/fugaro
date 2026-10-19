// The scrub's command line (see ./scrub.ts for what it replaces and why).
//
//   node .record-fixture/tools/scrub-fixture/index.js --in <raw.json> --out test/fixtures/normal.ndjson \
//        [--cli 0.8.6]
//
// `--in` is one `fugaro watch --json --once` document taken OUTSIDE this repository
// (never commit it). The invented document goes through the same redactor and
// writer a recording goes through, so the loader accepts it, then back through
// `loadFixture` before a byte is written. It prints the COUNT of values replaced and
// nothing else: no hash, no list, no derivative of a removed value is written anywhere.
//
// The clock shift is drawn for the run (`scrubDocument` does it) and this command
// line has no way to set it: an offset an operator can choose is an offset an
// operator can write down. So two runs of this command on the same input produce
// two different files, differing only in the clock and the run IDs that follow it.

import { readFileSync, writeFileSync } from 'node:fs'

import { loadFixture, writeFixture } from '../../src/source/fixture'
import type { FixtureRecord } from '../../src/source/fixture'
import type { JSONDoc } from '../../src/source/source'
import { PROJECT_MARKER, createRedactor } from '../../src/source/redact'
import { SCRUB_PROJECT, removedValues, scrubDocument } from './scrub'

/** NORMAL_PSEUDONYM_KEY keys the pseudonyms: public, and only for invented names. */
export const NORMAL_PSEUDONYM_KEY = 'normal-fixture-scrubbed-not-a-secret'

export const NORMAL_NOTE =
  'normal: the shape of one real fugaro watch --json document; every value is invented (see the scrub-fixture tool)'

/** normalFixture builds the fixture text from the raw document. */
export function normalFixture(
  raw: unknown, cli: string, onRemoved?: (removed: readonly string[]) => void,
): { text: string; scrubbed: JSONDoc } {
  const scrubbed = scrubDocument(raw)
  onRemoved?.(removedValues(raw, scrubbed))
  const redactor = createRedactor([SCRUB_PROJECT], { pseudonymKey: NORMAL_PSEUDONYM_KEY })
  const document = redactor.redactDocument(scrubbed) as JSONDoc
  const at = String(scrubbed['now'])
  const records: FixtureRecord[] = [{ at, document }]
  const text = writeFixture(records, { case: 'normal', project: PROJECT_MARKER, synthetic: true, cli, recorded: at, note: NORMAL_NOTE })
  loadFixture(text, 'normal.ndjson')
  return { text, scrubbed: document }
}

function arg(name: string): string | undefined {
  const i = process.argv.indexOf(name)
  return i < 0 ? undefined : process.argv[i + 1]
}

function main(): void {
  const inPath = arg('--in')
  const outPath = arg('--out')
  if (inPath === undefined || outPath === undefined) {
    process.stderr.write('usage: --in <raw.json> --out <fixture.ndjson> [--cli <version>]\n')
    process.exit(2)
  }
  const raw: unknown = JSON.parse(readFileSync(inPath, 'utf8'))
  let count = 0
  const { text } = normalFixture(raw, arg('--cli') ?? 'unknown', (removed) => {
    count = removed.length
  })
  writeFileSync(outPath, text)
  process.stdout.write(`${count} values replaced\n`)
}

if (require.main === module) main()
