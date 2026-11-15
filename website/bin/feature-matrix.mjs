// website/bin/feature-matrix.mjs — the generator: FEATURES.md, from status.md and the mapping
// (docs/feature-matrix/implementation-plan.md, task T3; D2446).
//
// `npm run features -- --as-of vX.Y.Z`, run from `website/`, reads the three inputs a snapshot is
// built from — `docs/status.md`, `docs/feature-matrix/mapping.yaml`, and the block of the
// FEATURES.md already in the tree, which is the previous snapshot the regression check reads
// (R7) — and writes the file the renderer produces at the repository root.
//
// **A refusal writes nothing.** The builder is the one thing that reads status.md, and it throws
// rather than returning a snapshot it could not build (an unmapped name, a regression against the
// previous file). This command catches that, prints it and exits non-zero with FEATURES.md
// exactly as it found it: the file is opened for writing only once the snapshot has been built
// and rendered, which is what makes "leaves the file untouched" structural rather than a
// promise to be careful.
//
// **`main` takes the builder as a parameter.** The real one is T1's `buildSnapshot`; until it
// lands the default is the contract's stub (`src/lib/feature-matrix-stub.mjs`), which answers
// with the fixture and reads none of its arguments. I1 swaps the import and deletes the stub,
// and the tests pass a builder of their own — which is how a refusal is exercised without a
// status.md that has one in it.
//
// `repo` is the tree to read and write, the repository root by default: the command is run from
// `website/` and the file it writes is the repository's, so neither is taken from the working
// directory, and a test can point the whole command at a scratch tree.

import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { parseYAML } from '../src/content.ts'
import { readSnapshotBlock } from '../src/lib/feature-matrix-model.mjs'
import { renderMarkdown } from '../src/lib/feature-matrix-markdown.mjs'
import { buildSnapshot } from '../src/lib/feature-matrix-stub.mjs'

/** The tree this file lives in: `website/bin/` up two directories. */
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')

const STATUS_FILE = path.join('docs', 'status.md')
const MAPPING_FILE = path.join('docs', 'feature-matrix', 'mapping.yaml')
const FEATURES_FILE = 'FEATURES.md'

/** The release a stamp names, and the only shape `--as-of` takes (`vX.Y.Z`, the contract's `as_of`). */
const AS_OF = /^v\d+\.\d+\.\d+$/

/**
 * Write `FEATURES.md` for `asOf` into `repo`, and answer with the exit code: 0 when the file was
 * written, 1 when the snapshot could not be built, in which case the refusal is printed and the
 * file is left as it was.
 */
export function main({ asOf, builder = buildSnapshot, repo = REPO_ROOT } = {}) {
  const file = path.join(repo, FEATURES_FILE)
  try {
    if (typeof asOf !== 'string' || !AS_OF.test(asOf)) {
      throw new Error(
        `--as-of takes the release the file is stamped with, written vX.Y.Z, and was given ${asOf === undefined ? 'nothing' : JSON.stringify(asOf)}`,
      )
    }

    const mappingFile = path.join(repo, MAPPING_FILE)
    const statusMarkdown = readFileSync(path.join(repo, STATUS_FILE), 'utf8')
    const mapping = parseYAML(readFileSync(mappingFile, 'utf8'), mappingFile)
    const previous = existsSync(file) ? readSnapshotBlock(readFileSync(file, 'utf8')) : null

    const markdown = renderMarkdown(builder({ statusMarkdown, mapping, previous, asOf }))
    writeFileSync(file, markdown)
    process.stdout.write(`${FEATURES_FILE} written, as of ${asOf}\n`)
    return 0
  } catch (failure) {
    process.stderr.write(`feature-matrix: ${failure.message}\n`)
    return 1
  }
}

/** The value of `--name value` or `--name=value`, or undefined when neither is there. */
function flag(args, name) {
  const at = args.indexOf(name)
  if (at !== -1) return args[at + 1]
  const inline = args.find((arg) => arg.startsWith(`${name}=`))
  return inline === undefined ? undefined : inline.slice(name.length + 1)
}

if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exitCode = main({ asOf: flag(process.argv.slice(2), '--as-of') })
}
