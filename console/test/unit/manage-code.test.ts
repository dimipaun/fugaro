// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Code group's three subjects -- repositories, image catalog entries and
// recipes -- and the two Copy-commands a repository row offers (the
// console-improvements plan's task MO-C; D2413, D2903, D2909).
//
// **What this file holds.** Four claims, and each is a claim about a *pair* of
// things rather than about the console alone:
//
//   - the kinds sit in the Code group and name reads the console really has. The
//     two repository documents are on `src/main/ipc.ts`'s operation table, and
//     their writes are on its `NEVER_REACHABLE` table -- which is what makes a
//     Copy-command the only route to them (D2903). The two new reads are C-OPS's
//     operations, and a build without them answers the gap whose sentence the
//     section draws beside RB1's Retry.
//   - the cells are the CLI's own words. Every folded cell of the repositories
//     table ("none", the four image sentences) is a sentence
//     `internal/cli/project_repos_ls.go` prints, and the recipes table's `steps`
//     cell is `stepsSummary` (`internal/cli/recipes.go`). Each is held to that
//     file's text here, so a rewording in the CLI fails this test rather than
//     drifting. What is *not* drawn as a word is what the document holds as a
//     value: the two halves of a repository's enrolment are the allow-list entry
//     and the config key as the document spells them, each under the class of
//     the shape it has, and the CLI's `not admitted` / `not in this machine's
//     config` for an absent half is an empty cell here (a column has one class,
//     and neither `repoPattern` nor `repository` reads a word).
//   - the two consequences are the CLI's account of the two writes. Every clause
//     the console draws comes from `internal/cli/project_repos_rm.go` or
//     `project_enroll_converge.go`, and the constants are held to that text
//     verbatim: the point of the sentence beside a Copy is that it says what the
//     command will do, and a paraphrase decided here would be the console's
//     belief about a removal rather than the CLI's. The one part of a sentence a
//     document supplies -- the repository, and the entry that still admits it --
//     is read through `repoPattern` first, and the two are the document's own
//     answers: which entries a removal leaves is the CLI's matcher
//     (`projectpolicy.MatchRepo`), read here through the same `*`.
//   - **a Copy-command only copies.** The remove action is the one D2909 forbids
//     a console to run, so the last describe copies every row's commands to a
//     clipboard with a bridge full of spies and asserts that none of them was
//     called -- and then runs the mutant a spawn would be, so the assertion is
//     proved to be able to fail.

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it, vi } from 'vitest'

import { copyCommandText } from '../../src/renderer/manage/command'
import { imagesKind } from '../../src/renderer/manage/kinds/images'
import { recipesKind } from '../../src/renderer/manage/kinds/recipes'
import {
  reposEnrolmentBuild,
  reposEnrolmentConsequence,
  reposEnrolmentPublish,
  reposEnrolmentWrites,
  reposKeeps,
  reposKind,
  reposLeaves,
  reposRemovalConsequence,
  reposRemovalTakes,
  reposStatusKind,
} from '../../src/renderer/manage/kinds/repos'
import { MANAGE_GROUPS } from '../../src/renderer/manage/types'
import { newOpsFrom } from '../../src/renderer/new-bridge'
import {
  NEW_OPERATION_METHODS,
  NEW_OPERATION_NAMES,
  NEW_OPERATION_TEXT_CLASSES,
  NEW_OPS_NOT_AVAILABLE,
} from '../../src/shared/new-ops'
import { TEXT_CLASSES, sanitizeText } from '../../src/shared/sanitize'
import type { NewOperationName, NewOpsReply } from '../../src/shared/new-ops'
import type { ManageKind, ManageRow } from '../../src/renderer/manage/types'

const CONSOLE = join(dirname(fileURLToPath(import.meta.url)), '..', '..')

/** cliSource is one Go file the console's words are held to, read as text.
 * Reading the CLI's own source is what `schema.test.ts` and
 * `live-watch-fields.test.ts` do for the same reason: the pin is against what
 * the CLI says, not against a transcription of it. */
function cliSource(path: string): string {
  return readFileSync(join(CONSOLE, '..', 'internal', 'cli', path), 'utf8')
}

const REPOS_LS_GO = cliSource('project_repos_ls.go')
const REPOS_RM_GO = cliSource('project_repos_rm.go')
const ENROL_GO = cliSource('project_enroll_converge.go')
const REMOVAL_WARNING_GO = cliSource('project_enroll.go')
const RECIPES_GO = cliSource('recipes.go')
const IPC_TS = readFileSync(join(CONSOLE, 'src', 'main', 'ipc.ts'), 'utf8')

/** THE_KINDS is the Code group in the order `repos.ts`, `images.ts` and
 * `recipes.ts` declare it: the spec's three subjects, with repositories read
 * from the group's two documents (contract sections 6.26 and 6.27). */
const THE_KINDS: readonly ManageKind[] = [reposKind, reposStatusKind, imagesKind, recipesKind]

/** VALUE_COLUMNS are the cells that are a **value the document wrote** rather
 * than a sentence somebody wrote: the repository itself and the two halves of
 * its enrolment, one column each (`REPOS_COLUMNS`). The guard below holds each of
 * them to the cell itself and not merely to `ok`, because a redaction is an `ok`
 * answer -- naming them is what makes that assertion bite when a column's class
 * is changed to one that redraws its value. Every other cell of these four kinds
 * is prose: a CLI sentence (a row's `problem` and `fix`), a word the console
 * folded it into (`none`, the image sentences), or -- in the image and recipe
 * kinds -- a field whose class is `NEW_OPERATION_TEXT_CLASSES`' answer rather
 * than this task's, for which the free-text rules rewriting a path or an address
 * is `sanitize.ts`'s own behaviour. */
const VALUE_COLUMNS: readonly string[] = Object.freeze([
  'repos.repo',
  'repos.published',
  'repos.local',
  'repos-status.repo',
])

/** REPOS_DOC is `repos.ls --json` (contract section 6.27): a repository this
 * machine's config holds and the allow-list admits by a pattern, one the
 * allow-list names and this machine has never configured -- the two states the
 * document keeps in two halves, and the two the removal sentence reads the
 * allow-list for -- and one the allow-list admits by its own exact entry, which
 * is the contract's ordinary case and the one whose two names are long enough
 * for a free-text class to redact (see the cells case below). */
const REPOS_DOC = {
  version: 1,
  project: 'fugaro-dev',
  repos: [
    {
      canonical: 'github/acme/app',
      provider: 'github',
      base_branch: 'main',
      workflows: [
        { name: 'implement', entry: 'node22-playwright', source: 'workflow' },
        { name: 'review', entry: '', source: 'unresolved', state: 'the pin "v9" is not in the catalog' },
      ],
      image_entry: 'node22-playwright',
      image_source: 'workflow',
      claude_accounts: ['ana@example.invalid'],
      published: { admitted: true, pattern: 'github/acme/*' },
      local: { present: true, key: 'acme/app' },
    },
    {
      canonical: 'github/other/tool',
      provider: 'github',
      base_branch: 'main',
      workflows: [],
      image_entry: '',
      image_source: 'repo',
      claude_accounts: [],
      published: { admitted: false, pattern: '' },
      local: { present: false, key: '' },
    },
    {
      canonical: 'github/dimipaun/fugaro',
      provider: 'github',
      base_branch: 'main',
      workflows: [{ name: 'implement', entry: 'base', source: 'default' }],
      image_entry: 'base',
      image_source: 'default',
      claude_accounts: [],
      published: { admitted: true, pattern: 'github/dimipaun/fugaro' },
      local: { present: true, key: 'dimipaun/fugaro' },
    },
  ],
  allow: { entries: ['github/acme/*', 'github/other/tool', 'github/dimipaun/fugaro'], generation: '42', state: '' },
  seen: { entries: [], notes: [], state: '' },
  local: { repos: ['acme/app', 'dimipaun/fugaro'], state: '' },
}

/** STATUS_DOC is `repos.status --json` (contract section 6.26): a repository
 * whose nine checks were read, and one nothing enrolls, whose single row is the
 * `enrolment` row and whose `state` is the CLI's own sentence that the enrolment
 * could not be decided. */
const STATUS_DOC = {
  version: 1,
  project: 'fugaro-dev',
  repos: [
    {
      canonical: 'github/acme/app',
      enrolled: true,
      state: '',
      rows: [
        { id: 'app-installation', ok: true, problem: 'the App is installed on the repository' },
        {
          id: 'image-map',
          ok: false,
          problem: 'no image map names this repository, and no catalog entry is its default',
          fix: 'fugaro image build <entry>',
          severity: 'warning',
        },
        {
          id: 'job-and-grant',
          ok: false,
          problem: 'the repository has no job and no invoker grant',
          fix: 'fugaro update --repo <checkout>',
        },
      ],
    },
    {
      canonical: 'github/acme/ghost',
      enrolled: false,
      state: 'this repository is in neither half of the enrolment that was read',
      rows: [
        {
          id: 'enrolment',
          ok: false,
          problem: 'nothing enrolls this repository',
          fix: 'fugaro project repos add <provider/owner/name>',
        },
      ],
    },
  ],
}

/** IMAGE_DOC is `image ls --json` (C-OPS's `ImageCatalogView`): the entry a job
 * naming nothing runs, and one nothing has built yet. */
const IMAGE_DOC = {
  project: 'fugaro-dev',
  entries: [
    {
      name: 'base',
      tags: ['v1', 'latest'],
      default: true,
      source: 'images/base',
      description: 'the pinned base image every repository image derives from',
      digest: 'sha256:0f1e2d',
    },
    {
      name: 'node22-playwright',
      tags: ['v3'],
      default: false,
      source: 'images/node22-playwright',
      description: 'Node 22 with the browser the console’s e2e specs need',
      digest: '',
    },
  ],
}

/** RECIPES_DOC is `recipes ls --json` (C-OPS's `RecipesView`): one recipe a
 * routing skill may pick, one a higher layer shadows, and one that failed to
 * parse -- the three states the document's own members carry. */
const RECIPES_DOC = {
  recipes: [
    {
      name: 'quick',
      source: 'repo',
      description: 'one implement round and a review, for a small change',
      use_when: 'the task is one file and the tests already cover it',
      steps: [
        { kind: 'first_line', max_rounds: null, bounce: false, autofix: false },
        { kind: 'review', max_rounds: 2, bounce: true, autofix: false },
        { kind: 'check', command: 'npm test', max_rounds: null, bounce: false, autofix: true },
      ],
      launchable: true,
      shadowed_by: '',
      problem: '',
    },
    {
      name: 'quick',
      source: 'catalog',
      description: 'the catalog’s own quick recipe',
      use_when: 'a small change',
      steps: [{ kind: 'first_line', max_rounds: null, bounce: false, autofix: false }],
      launchable: true,
      shadowed_by: 'repo',
      problem: '',
    },
    {
      name: 'renovate',
      source: 'project',
      description: '',
      use_when: '',
      steps: [],
      launchable: false,
      shadowed_by: '',
      problem: 'steps[0]: unknown step kind "bump"',
    },
  ],
  notes: ['project recipe renovate has no use_when'],
}

/** fakeBridge is a bridge of this build's own making, from `new-ops.ts`'s names
 * rather than from a fixture (`contract-ops.md` says a consumer's test builds
 * its own, which is what let I-OPS delete the stub bridge whole). Each named
 * operation gets the method the contract names for it and answers the document
 * under `document`; every other operation has no method at all, which is the
 * older build this file's last case reads. */
function fakeBridge(answers: Partial<Record<NewOperationName, unknown>>): unknown {
  const bridge: Record<string, unknown> = {}
  for (const name of NEW_OPERATION_NAMES) {
    const document = answers[name]
    if (document === undefined) continue
    bridge[NEW_OPERATION_METHODS[name]] = () => Promise.resolve({ ok: true, data: { document } })
  }
  return bridge
}

/** valueOf is a reply's value, or a failure naming the gap -- so a case that
 * expected a document and got a refusal says which refusal it was. */
function valueOf<Answer>(reply: NewOpsReply<Answer>): Answer {
  if (!reply.ok) throw new Error(`${reply.gap.gap}: ${reply.gap.sentence}`)
  return reply.value
}

/** column is one column of a kind, or a failure naming it. */
function column(kind: ManageKind, key: string) {
  const found = kind.columns.find((candidate) => candidate.key === key)
  if (found === undefined) throw new Error(`${kind.id} has no column ${key}`)
  return found
}

describe('the Code group’s kinds', () => {
  it('is the spec’s three subjects across the group’s four documents', () => {
    // The spec groups the subject as `repos, images, recipes`; this project
    // answers the first with two reads, so the group is four kinds and the
    // subject names are `repos`/`repos-status`, `images`, `recipes`.
    expect(THE_KINDS.map((kind) => [kind.id, kind.group, kind.title, kind.source])).toEqual([
      ['repos', 'Code', 'Repositories', 'repos.ls'],
      ['repos-status', 'Code', 'Repository status', 'repos.status'],
      ['images', 'Code', 'Image catalog', 'image.catalog.ls'],
      ['recipes', 'Code', 'Recipes', 'recipes.ls'],
    ])
    // Non-vacuity: the group every one of them names is one the shell draws.
    for (const kind of THE_KINDS) expect(MANAGE_GROUPS).toContain(kind.group)
  })

  it('gives every column a class from the closed set, and no two columns one key', () => {
    for (const kind of THE_KINDS) {
      const keys = kind.columns.map((entry) => entry.key)
      expect(new Set(keys).size, `${kind.id} repeats a column key`).toBe(keys.length)
      for (const entry of kind.columns) {
        expect(TEXT_CLASSES, `${kind.id}.${entry.key}`).toContain(entry.class)
        expect(entry.title, `${kind.id}.${entry.key} has no heading`).not.toBe('')
      }
    }
  })

  it('names reads this console really has', () => {
    for (const kind of THE_KINDS) {
      const known =
        (NEW_OPERATION_NAMES as readonly string[]).includes(kind.source) ||
        IPC_TS.includes(`name: '${kind.source}',`)
      expect(known, `${kind.id} reads ${kind.source}, which no table lists`).toBe(true)
    }
    // The two repository reads are the console's own operations, and both were
    // listed before any screen consumed them (RE5, D2413).
    expect(IPC_TS).toContain("name: 'repos.ls',")
    expect(IPC_TS).toContain("name: 'repos.status',")
  })

  it('leaves the group’s two writes out of the console’s reach, so a Copy is the only route', () => {
    // D2903's whole claim for this subject: `repos add` and `repos rm` are on
    // `NEVER_REACHABLE`, and a kind's Copy-command is the operator's way to them.
    for (const command of ['project repos add', 'project repos rm']) {
      expect(IPC_TS).toContain(`value: '${command}',`)
    }
  })

  it('refuses rather than draws an empty table where the build has no such read', async () => {
    // The acceptance's older-CLI case, read from the kind's side of it: an
    // installation whose bridge has neither method answers the gap C-OPS wrote
    // for it, and that sentence is what the section draws beside RB1's Retry
    // (the shell is MO-SH's and the control is RB1's; what this kind owns is
    // that the read it names answers nothing rather than a document).
    const older = newOpsFrom(fakeBridge({}))
    for (const call of [older.listImageCatalog({}), older.listRecipes({})]) {
      const reply = await call
      expect(reply.ok).toBe(false)
      if (reply.ok) continue
      expect(reply.gap.gap).toBe('not-available')
      expect(reply.gap.sentence).toBe(NEW_OPS_NOT_AVAILABLE)
    }
    // And the refused reply is a gap, never an empty document: a section handed
    // `{entries: []}` would draw a catalog nobody listed.
    expect(NEW_OPS_NOT_AVAILABLE).not.toBe('')
  })
})

describe('the repositories kind', () => {
  it('draws one row per repository, with the CLI’s own words for the folded cells', () => {
    const rows = reposKind.decode(REPOS_DOC)
    expect(rows.map((row) => row.key)).toEqual([
      'github/acme/app',
      'github/other/tool',
      'github/dimipaun/fugaro',
    ])
    expect(rows[0]?.cells).toEqual({
      repo: 'github/acme/app',
      provider: 'github',
      branch: 'main',
      image: 'image node22-playwright (workflow)',
      workflows: 'implement, review',
      accounts: 'ana@example.invalid',
      published: 'github/acme/*',
      local: 'acme/app',
    })
    // The second repository's two halves are the document's own emptiness: the
    // allow-list admits it by nothing and this machine holds it under no key, so
    // both cells are empty and the shell draws nothing for them. A word there
    // would be a cell of a column whose class reads a value.
    expect(rows[1]?.cells).toEqual({
      repo: 'github/other/tool',
      provider: 'github',
      branch: 'main',
      image: 'image this project builds for the repository (repo)',
      workflows: 'none',
      accounts: 'none',
      published: '',
      local: '',
    })
    // The third repository's two halves are long names a free-text class must
    // not touch -- the rules that redacted a repository-shaped run (a pattern as
    // `github<path>`, a key as `[redacted]`) went with D3213 (2026-11-21) -- and
    // each cell is the document's own value, drawn as it came.
    expect(rows[2]?.cells['published']).toBe('github/dimipaun/fugaro')
    expect(rows[2]?.cells['local']).toBe('dimipaun/fugaro')
  })

  it('spells those folded cells the way the CLI spells them', () => {
    // Each phrase is the kind's and the CLI's at once: the console draws it and
    // `internal/cli/project_repos_ls.go` prints it. `none` is not in the list
    // because the exact-cells case above already pins it as a cell value, and
    // neither are the CLI's two words for an absent half, which the console does
    // not draw: `not admitted` and `not in this machine's config` say what an
    // empty cell of each column says.
    const phrases = [
      'image this project builds for the repository (repo)',
      'image the installation ships as its base (base)',
      'image the shared dependency layer (shared)',
      'image unresolved (no answer chose one)',
    ]
    for (const phrase of phrases) {
      expect(REPOS_LS_GO, `the CLI no longer prints ${phrase}`).toContain(phrase)
    }
    // Every phrase is really drawn: the fixture carries the two admitted/state
    // cells and the ordinary image, and the rest of the image vocabulary is one
    // row per answer the closed set of seven names -- including the three whose
    // image is not a catalog entry's, which is what the source, and not the
    // entry, decides.
    const answers = ['workflow', 'map', 'default', 'repo', 'base', 'shared', 'unresolved']
    const drawn = [REPOS_DOC, ...answers.map((source) => ({
      repos: [{ canonical: 'github/acme/app', image_source: source, image_entry: 'base' }],
    }))]
      .map((doc) => JSON.stringify(reposKind.decode(doc)))
      .join('\n')
    for (const phrase of phrases) {
      expect(drawn, `no decoded cell carries ${phrase}`).toContain(phrase)
    }
  })

  it('fills every cell with text its own column admits, and draws a value as the document wrote it', () => {
    // `ok` alone is not the assertion, and that is this case's point: a
    // **redaction is an `ok` answer**, so a column whose class does not match the
    // value it holds passes an `ok`-only guard while drawing a redaction the
    // document does not contain. `label` draws `github/dimipaun/fugaro` as
    // `[redacted]` and `github/acme/app` as `github<path>` -- the failure
    // `sanitize.ts`'s `repoPattern`/`imageRef` note says those two classes exist
    // to prevent, and what the repositories kind's published and local columns
    // drew until they carried the value's own class.
    //
    // So a cell that is a **value the document wrote** (VALUE_COLUMNS) is held to
    // the cell itself, and not merely to `ok`: a class that reads a value accepts
    // it whole or refuses it, and one that draws it as a redaction is the failure
    // above. The other cells are **prose** -- a sentence the CLI wrote (a row's
    // `problem` and `fix`, the console's folding words) -- and the free-text rules
    // rewriting a path or an address inside a sentence are the console's own rule
    // (`sanitize.ts`), not a mismatch: `repos-status.fix`'s own doctor command, or
    // `repos.accounts`' address, is drawn that way on purpose.
    for (const [kind, doc] of [
      [reposKind, REPOS_DOC],
      [reposStatusKind, STATUS_DOC],
      [imagesKind, IMAGE_DOC],
      [recipesKind, RECIPES_DOC],
    ] as readonly (readonly [ManageKind, unknown])[]) {
      const decoded = kind.decode(doc)
      expect(decoded.length, `${kind.id} decoded nothing`).toBeGreaterThan(0)
      for (const row of decoded) {
        for (const entry of kind.columns) {
          const cell = row.cells[entry.key] ?? ''
          const field = `${kind.id}.${entry.key}`
          if (cell === '') continue
          const sanitized = sanitizeText(cell, entry.class, field)
          expect(sanitized.ok, `${field}: ${cell}`).toBe(true)
          if (!sanitized.ok) continue
          if (!VALUE_COLUMNS.includes(field)) continue
          expect(sanitized.text, `${field}: ${cell}`).toBe(cell)
        }
      }
    }
    // Non-vacuity, in this file's own idiom (`spawnsToo` below): the assertion
    // above is one that can fail, because a class this module does not use for
    // these columns rewrites what it is given -- `repository` refuses a
    // three-part path rather than drawing it, which is why the columns declare a
    // free-text class and not that one.
    const asRepository = sanitizeText('github/dimipaun/fugaro', 'repository', 'repos.published')
    expect(asRepository.ok && asRepository.text === 'github/dimipaun/fugaro').toBe(false)
  })

  it('gives each row its own two Copy-commands, and none to the kind', () => {
    // A kind-level action could only be one that names no repository, so the
    // two live on the row (`ManageRow.actions`).
    expect(reposKind.actions).toEqual([])
    const rows = reposKind.decode(REPOS_DOC)
    for (const row of rows) {
      const actions = row.actions ?? []
      expect(actions.map((action) => action.kind)).toEqual(['copy-command', 'copy-command'])
      const texts = actions.map((action) =>
        action.kind === 'copy-command' ? copyCommandText(action) : '',
      )
      expect(texts).toEqual([
        `fugaro project repos add ${row.key}`,
        `fugaro project repos rm ${row.key}`,
      ])
    }
  })

  it('carries the two members a Copy-command has and no way to run one', () => {
    // The action vocabulary is the proof that "the console never runs a removal"
    // is a shape and not a promise: there is no argv member a drawer could hand
    // to a child, and no method member it could call.
    for (const row of reposKind.decode(REPOS_DOC)) {
      for (const action of row.actions ?? []) {
        expect(Object.keys(action).sort()).toEqual(['argv', 'consequence', 'kind'])
      }
    }
  })

  it('draws a row without its Copy when the value is one the console will not copy', () => {
    // A canonical name with a `..` segment and one opening with `-` are the two
    // refusals `command.ts` makes; a row that carries one draws no command
    // rather than a repaired one.
    for (const canonical of ['github/acme/../etc', '-github/acme/app']) {
      const doc = { ...REPOS_DOC, repos: [{ ...REPOS_DOC.repos[0], canonical }] }
      const rows = reposKind.decode(doc)
      expect(rows[0]?.cells['repo']).toBe(canonical)
      expect(rows[0]?.actions).toEqual([])
    }
  })

  it('decodes nothing from a document it cannot read, rather than throwing', () => {
    for (const doc of [null, 42, 'repos', [], { repos: 'none' }, { repos: [null, 'x'] }]) {
      expect(reposKind.decode(doc)).toEqual([])
      expect(reposStatusKind.decode(doc)).toEqual([])
      expect(imagesKind.decode(doc)).toEqual([])
      expect(recipesKind.decode(doc)).toEqual([])
    }
  })
})

describe('the repository status kind', () => {
  it('draws one row per check, and the CLI’s own sentence for an enrolment it could not decide', () => {
    const rows = reposStatusKind.decode(STATUS_DOC)
    expect(rows.map((row) => row.key)).toEqual([
      'github/acme/app/app-installation',
      'github/acme/app/image-map',
      'github/acme/app/job-and-grant',
      'github/acme/ghost/enrolment',
    ])
    expect(rows.map((row) => row.cells['result'])).toEqual(['ok', 'failed', 'failed', 'failed'])
    // A passing row has no fix, and the CLI omits the member (`omitempty`).
    expect(rows[0]?.cells['fix']).toBe('')
    expect(rows[1]?.cells['fix']).toBe('fugaro image build <entry>')
    // The repository could not be decided, so the enrolment cell is the CLI's
    // own sentence rather than a verdict this console made up.
    expect(rows[3]?.cells['enrolled']).toBe(
      'this repository is in neither half of the enrolment that was read',
    )
    expect(rows[0]?.cells['enrolled']).toBe('enrolled')
  })

  it('offers no action: every write it names is the CLI’s own fix', () => {
    expect(reposStatusKind.actions).toEqual([])
    for (const row of reposStatusKind.decode(STATUS_DOC)) expect(row.actions).toBeUndefined()
  })
})

describe('the image catalog and recipes kinds', () => {
  it('decodes the catalog a bridge answers, with C-OPS’s own classes', async () => {
    const reply = await newOpsFrom(fakeBridge({ 'image.catalog.ls': IMAGE_DOC })).listImageCatalog({})
    const rows = imagesKind.decode(valueOf(reply))
    expect(rows.map((row) => row.key)).toEqual(['base', 'node22-playwright'])
    expect(rows[0]?.cells).toEqual({
      entry: 'base',
      tags: 'v1, latest',
      default: 'yes',
      source: 'images/base',
      description: 'the pinned base image every repository image derives from',
      digest: 'sha256:0f1e2d',
    })
    expect(rows[1]?.cells['default']).toBe('no')
    expect(rows[1]?.cells['digest']).toBe('')
    // The class of each column is the contract's answer for the wire field it
    // draws, so the console cannot come to a second opinion about one value.
    const pins: readonly (readonly [string, string])[] = [
      ['entry', 'name'],
      ['tags', 'tags'],
      ['source', 'source'],
      ['description', 'description'],
      ['digest', 'digest'],
    ]
    for (const [key, wire] of pins) {
      expect(column(imagesKind, key).class, `image.${key}`).toBe(NEW_OPERATION_TEXT_CLASSES[wire])
    }
    expect(NEW_OPERATION_TEXT_CLASSES['name']).toBe('label')
  })

  it('decodes the recipes a bridge answers, one row per layer’s recipe', async () => {
    const reply = await newOpsFrom(fakeBridge({ 'recipes.ls': RECIPES_DOC })).listRecipes({})
    const rows = recipesKind.decode(valueOf(reply))
    expect(rows.map((row) => row.key)).toEqual(['repo/quick', 'catalog/quick', 'project/renovate'])
    expect(rows[0]?.cells).toEqual({
      name: 'quick',
      layer: 'repo',
      usable: 'yes',
      steps: 'first_line, review (bounce: first_line) (max_rounds: 2), check: npm test, autofix',
      useWhen: 'the task is one file and the tests already cover it',
      description: 'one implement round and a review, for a small change',
      shadowedBy: '',
      problem: '',
    })
    // A name two layers carry is two rows: the one that wins and the one it
    // shadows, which is what the CLI lists.
    expect(rows[1]?.cells['shadowedBy']).toBe('repo')
    // A recipe that failed to parse is not usable, and says why.
    expect(rows[2]?.cells['usable']).toBe('no')
    expect(rows[2]?.cells['problem']).toBe('steps[0]: unknown step kind "bump"')
    expect(rows[2]?.cells['steps']).toBe('none')
  })

  it('summarises a recipe’s steps the way the CLI summarises them', async () => {
    const reply = await newOpsFrom(fakeBridge({ 'recipes.ls': RECIPES_DOC })).listRecipes({})
    const steps = recipesKind.decode(valueOf(reply))[0]?.cells['steps'] ?? ''
    for (const phrase of ['check: ', ', autofix', '(bounce: first_line)', '(max_rounds: ']) {
      expect(RECIPES_GO, `the CLI no longer spells ${phrase}`).toContain(phrase)
      expect(steps, `no decoded cell carries ${phrase}`).toContain(phrase)
    }
    const pins: readonly (readonly [string, string])[] = [
      ['name', 'name'],
      ['layer', 'source'],
      ['useWhen', 'use_when'],
      ['description', 'description'],
      ['shadowedBy', 'shadowed_by'],
      ['problem', 'problem'],
    ]
    for (const [key, wire] of pins) {
      expect(column(recipesKind, key).class, `recipe.${key}`).toBe(NEW_OPERATION_TEXT_CLASSES[wire])
    }
  })
})

describe('the consequences the two Copy-commands state', () => {
  it('draws the CLI’s own account of a removal', () => {
    // Every phrase is the CLI's own text, copied into the kind and held back to
    // the file it came from: a paraphrase chosen in `repos.ts` fails here, and so
    // does a rewording of the command.
    for (const phrase of [reposRemovalTakes, ...reposLeaves, ...reposKeeps]) {
      expect(REPOS_RM_GO, `the CLI no longer says: ${phrase}`).toContain(phrase)
    }
    const consequence = reposRemovalConsequence(REPOS_DOC.allow.entries, 'github/other/tool')
    // The two halves of the warning are `removalWarning`'s own sentences
    // (`project_enroll.go`) and the six left behind are the CLI's list: the
    // sentence states both, which is D2909's requirement and D2413's.
    expect(REMOVAL_WARNING_GO).toContain('its runs are refused from now on')
    expect(REMOVAL_WARNING_GO).toContain('a run in flight keeps the policy until it finishes')
    expect(consequence).toContain('its runs are refused from now on')
    expect(consequence).toContain('a run in flight keeps the policy until it finishes')
    for (const phrase of [reposRemovalTakes, ...reposLeaves, ...reposKeeps]) {
      expect(consequence, `the sentence omits: ${phrase}`).toContain(phrase)
    }
    expect(reposLeaves).toHaveLength(6)
  })

  it('says a removal refuses nothing while another entry still admits the repository', () => {
    // `github/acme/*` admits `github/acme/app`, so removing the concrete entry
    // does not stop its runs -- the case `removalWarning` exists for, and the
    // reason the sentence is computed from the document's allow-list rather than
    // written out once.
    const consequence = reposRemovalConsequence(REPOS_DOC.allow.entries, 'github/acme/app')
    expect(consequence).toContain('but github/acme/* still admits it: its runs are not refused')
    expect(consequence).not.toContain('refused from now on')
  })

  it('reads a `*` the way the CLI reads it: inside a segment, not only as a whole one', () => {
    // `internal/config`'s `MatchSegment` lets a `*` stand for any run of
    // characters **within** one segment, so `github/acme/ap*` admits
    // `github/acme/app`. A matcher that read a `*` as a whole segment only would
    // answer "its runs are refused from now on" over a repository the list still
    // admits -- the misfire `matchseg.go` warns about -- so both directions are
    // pinned here.
    const inside = reposRemovalConsequence(['github/acme/app', 'github/acme/ap*'], 'github/acme/app')
    expect(inside).toContain('but github/acme/ap* still admits it: its runs are not refused')
    expect(inside).not.toContain('refused from now on')
    // A `*` never crosses a `/`: `github/*` has two segments where the
    // repository has three, so it admits nothing -- a matcher whose star could
    // span a separator would read it as every repository of the provider.
    expect(reposRemovalConsequence(['github/acme/app', 'github/*'], 'github/acme/app')).toContain(
      'its runs are refused from now on',
    )
    // The second argument may be a pattern of its own, and its `*` characters are
    // ordinary characters there -- which is why the pattern's star is tried
    // *before* literal equality: `github/acme/*` removed while `github/acme/*app`
    // remains is not a revocation, and a matcher that spent the star on the one
    // character would say it was.
    expect(reposRemovalConsequence(['github/acme/*app', 'github/acme/*'], 'github/acme/*app')).toContain(
      'but github/acme/* still admits it',
    )
  })

  it('draws no sentence, and so no Copy, for a value it will not name', () => {
    // The two values a consequence interpolates are the document's, so they are
    // read through the repository columns' own class (`repoPattern`) before they
    // are drawn. A name the copy alphabets admit but that class refuses -- here a
    // tag, which no repository entry carries -- is a value the console will not
    // draw, and a sentence that had to leave the name out would be about another
    // repository: the action is dropped instead.
    expect(reposRemovalConsequence(['github/acme/app'], 'github/acme/app:latest')).toBeNull()
    expect(reposEnrolmentConsequence('github/acme/app:latest')).toBeNull()
    const doc = {
      ...REPOS_DOC,
      repos: [{ ...REPOS_DOC.repos[0], canonical: 'github/acme/app:latest' }],
    }
    expect(reposKind.decode(doc)[0]?.actions).toEqual([])
  })

  it('draws the CLI’s own account of an enrolment', () => {
    for (const phrase of [reposEnrolmentWrites, reposEnrolmentPublish, reposEnrolmentBuild]) {
      expect(ENROL_GO, `the CLI no longer says: ${phrase}`).toContain(phrase)
    }
    const consequence = reposEnrolmentConsequence('github/acme/app')
    expect(consequence).toContain('github/acme/app')
    for (const phrase of [reposEnrolmentWrites, reposEnrolmentPublish, reposEnrolmentBuild]) {
      expect(consequence, `the sentence omits: ${phrase}`).toContain(phrase)
    }
  })

  it('holds each clause to the CLI’s file, not to a transcription of it', () => {
    // The constants are the CLI's text; this is the check that they still are.
    // It is what makes a copy in `repos.ts` safe: the kind and the pin both fail
    // the day the CLI rewords the sentence, and neither can move alone.
    expect(REPOS_RM_GO).toContain('var reposRemovalLeaves = []string{')
    expect(REPOS_RM_GO).toContain('func reposRemovalOffboarding(')
    expect(ENROL_GO).toContain('func reposEnrolmentWhat(')
    expect(ENROL_GO).toContain('func reposEnrolmentPlan(')
    expect(REMOVAL_WARNING_GO).toContain('func removalWarning(')
  })
})

describe('a Copy-command only copies', () => {
  /** Draw is what a drawer may do with an action: put its text on the
   * clipboard, and (in the mutant) spawn it. */
  interface Draw {
    readonly clipboard: string[]
    readonly spawns: readonly string[]
  }

  /** copiesOnly is the console's behaviour, in miniature: every `copy-command`
   * of every row goes to the clipboard and nothing else happens. The shell that
   * draws the Copy control is MO-SH's; what is pinned here is that the action
   * gives a drawer nothing else to do. */
  function copiesOnly(rows: readonly ManageRow[], into: { clipboard: string[] }): void {
    for (const row of rows) {
      for (const action of row.actions ?? []) {
        if (action.kind !== 'copy-command') continue
        into.clipboard.push(copyCommandText(action))
      }
    }
  }

  /** spawnsToo is the mutant: the same walk plus the one thing D2903 keeps out
   * of the console -- a child built out of the command the operator was supposed
   * to run in their own terminal. */
  function spawnsToo(
    rows: readonly ManageRow[],
    into: { clipboard: string[]; spawns: string[] },
    spawn: (argv: readonly string[]) => void,
  ): void {
    for (const row of rows) {
      for (const action of row.actions ?? []) {
        if (action.kind !== 'copy-command') continue
        into.clipboard.push(copyCommandText(action))
        into.spawns.push(action.argv.join(' '))
        spawn(action.argv)
      }
    }
  }

  it('puts both commands on the clipboard and calls nothing on the bridge', () => {
    // A spy for every method this build's bridge could be asked for, so a drawer
    // that reached for one -- the nine new operations and the spawn a write
    // would need -- is caught by the count rather than by an argument.
    const bridge: Record<string, ReturnType<typeof vi.fn>> = {}
    for (const method of [...Object.values(NEW_OPERATION_METHODS), 'spawn', 'run', 'exec']) {
      bridge[method] = vi.fn()
    }
    const draw = { clipboard: [] as string[] }
    copiesOnly(reposKind.decode(REPOS_DOC), draw)
    expect(draw.clipboard).toEqual([
      'fugaro project repos add github/acme/app',
      'fugaro project repos rm github/acme/app',
      'fugaro project repos add github/other/tool',
      'fugaro project repos rm github/other/tool',
      'fugaro project repos add github/dimipaun/fugaro',
      'fugaro project repos rm github/dimipaun/fugaro',
    ])
    for (const [method, spy] of Object.entries(bridge)) {
      expect(spy.mock.calls.length, `${method} was called`).toBe(0)
    }
  })

  it('fails if it spawns (MUTATION)', () => {
    // The check above is worth exactly as much as its ability to fail: this is
    // the same walk with the one line D2903 forbids, and the spy the case above
    // asserts on is the spy that catches it.
    const spawn = vi.fn()
    const bridge = { spawn }
    const mutant = { clipboard: [] as string[], spawns: [] as string[] }
    spawnsToo(reposKind.decode(REPOS_DOC), mutant, (argv) => bridge.spawn(argv))
    expect(mutant.clipboard).toEqual([
      'fugaro project repos add github/acme/app',
      'fugaro project repos rm github/acme/app',
      'fugaro project repos add github/other/tool',
      'fugaro project repos rm github/other/tool',
      'fugaro project repos add github/dimipaun/fugaro',
      'fugaro project repos rm github/dimipaun/fugaro',
    ])
    expect(mutant.spawns).toHaveLength(6)
    expect(bridge.spawn.mock.calls).toEqual([
      [['fugaro', 'project', 'repos', 'add', 'github/acme/app']],
      [['fugaro', 'project', 'repos', 'rm', 'github/acme/app']],
      [['fugaro', 'project', 'repos', 'add', 'github/other/tool']],
      [['fugaro', 'project', 'repos', 'rm', 'github/other/tool']],
      [['fugaro', 'project', 'repos', 'add', 'github/dimipaun/fugaro']],
      [['fugaro', 'project', 'repos', 'rm', 'github/dimipaun/fugaro']],
    ])
    // And a row with no Copy at all is one the mutant cannot spawn: the refusal
    // is what keeps a `..` segment out of a child as well as off the clipboard.
    const refused = { ...REPOS_DOC, repos: [{ ...REPOS_DOC.repos[0], canonical: 'github/acme/../etc' }] }
    const empty = { clipboard: [] as string[], spawns: [] as string[] }
    spawnsToo(reposKind.decode(refused), empty, () => bridge.spawn)
    expect(empty.clipboard).toEqual([])
    expect(empty.spawns).toEqual([])
  })
})
