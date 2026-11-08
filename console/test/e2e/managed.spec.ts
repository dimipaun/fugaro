// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The managed-project screens in a real window: the Project tab's five later
// sections, each drawn from its own read (the managed-screens plan's CM12).
//
// **What this file adds that the unit suite cannot.** `managed.test.ts`,
// `retirements.test.ts`, `accountkeys.test.ts`, `credentialsservice.test.ts`,
// `policynotes.test.ts` and the tab's own wiring tests assert the five models
// and the wiring between them and the tab, from documents written in those
// files. What a window adds is the three things none of them can reach: the
// five reads are made by the **real main process**, through the real spawn
// allow-list, so the argv asserted below are the console's own rows and nothing
// a renderer chose; the five sections are drawn into **one document**, in the
// order their answers land; and the stub `fugaro` refuses every argv it was not
// given, so a console that asked for something else fails here with the argv it
// was handed rather than passing on an answer it should never have been able to
// obtain.
//
// **The five sections, and the read behind each.** The managed state and the
// retirements are one read (`fugaro managed status --json`, CM8 and CM9), the
// review account keys are the settings section's (`fugaro settings --json`,
// D1488), the credentials service is `fugaro doctor --json`, and the policy
// panel is `fugaro project policy show --json` -- which CM10 flipped out of its
// not-built state (D1481). Each is a sibling of the tab's first read, with its
// own host, its own busy state and its own refusal, and the owner's rule of
// 2026-10-31 is the whole of what the three cases below are about.
//
// **Three cases.** The first opens the tab and reads the frame the click
// happened in -- four marks up, nothing of any document drawn -- and then the
// five sections, each from the document its read answered with. The second
// makes the managed read **slow on purpose** (the stub sleeps the seconds in
// `managed-status-delay.txt` before it prints) and asserts the sections whose
// reads have already answered are drawn while that one is still out: a tab that
// held every section until its slowest read landed would fail it. The third
// takes the policy document away, so that read is refused with exit 2, and
// asserts the refusal is drawn **in that panel** while the four sections beside
// it keep what their own reads answered.
//
// **The corpus is three documents and a stub, and no network.** The two the
// fixture's new arms answer with are seeded beside the stub the way the specs
// seed every other one (`seedManaged`), the account keys are the committed
// `test/fixtures/settings.json`, and the tab's first read is the budget below.
// The credentials service's read is the stub's own fixed `doctor --json`
// document, which carries no `credentials-service-deployed` check: the section
// says so (`no-check`) rather than reporting a state nobody measured, and that
// is the state this spec asserts. Nothing here reaches the network or a real
// CLI.
//
// **No goldens.** This file takes none: the frames CM11's styles are held to are
// `projects.spec.ts`'s, and the class hooks are pinned by the unit suite.
//
// **The stub's version stays where `inRangeCliStub` leaves it**, below
// `MODELS_LIST_CLI_FLOOR`, so the tab's models panel draws the console's own
// floor sentence and makes **no** `models ls` read: the argv list below is the
// five reads these sections are, and a models read in it would be a read this
// spec's console should not have made.

import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'
import type { SpawnLogEntry } from '../../src/main/spawnlog'
import {
  ACCOUNT_KEYS_BUSY,
  ACCOUNT_KEYS_CLASS,
  ACCOUNT_KEYS_ROW_CLASS,
  ACCOUNT_KEYS_SOURCE,
  ACCOUNT_KEYS_SOURCE_CLASS,
  ACCOUNT_KEYS_TITLE,
  ACCOUNT_KEYS_TITLE_CLASS,
} from '../../src/renderer/screens/accountkeys'
import {
  CREDENTIALS_SERVICE_BUSY,
  CREDENTIALS_SERVICE_CLASS,
  CREDENTIALS_SERVICE_NO_CHECK_CLASS,
  CREDENTIALS_SERVICE_SOURCE,
  CREDENTIALS_SERVICE_SOURCE_CLASS,
  CREDENTIALS_SERVICE_TITLE,
  CREDENTIALS_SERVICE_TITLE_CLASS,
} from '../../src/renderer/screens/credentialsservice'
import {
  MANAGED_BUSY,
  MANAGED_SOURCE_LINE,
  MANAGED_TITLE,
  MANAGED_WORD_CLASS,
} from '../../src/renderer/screens/managed'
import { POLICY_NODE_SOURCE } from '../../src/renderer/screens/policynotes'
import {
  PROJECTS_ACCOUNT_KEYS_CLASS,
  PROJECTS_BODY_CLASS,
  PROJECTS_CREDENTIALS_SERVICE_CLASS,
  PROJECTS_MANAGED_CLASS,
  PROJECTS_RETIREMENTS_CLASS,
} from '../../src/renderer/screens/projects'
import {
  RETIREMENTS_BUSY,
  RETIREMENTS_ROW_CLASS,
  RETIREMENTS_SOURCE_LINE,
  RETIREMENTS_TITLE,
} from '../../src/renderer/screens/retirements'
import type { JSONDoc } from '../../src/source/source'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { hexDigest } from '../support/fakeSecrets'
import { launchConsole } from '../support/launch'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')

/** THE_PANEL is the dashboard's own panel, and THE_PROJECTS the view drawn in
 * it -- `projects.spec.ts`'s selectors, which are the shell's and not this
 * file's. */
const THE_PANEL = 'div.console-panel'
const THE_PROJECTS = `${THE_PANEL} section.projects`
const THE_BODY = `${THE_PROJECTS} div.${PROJECTS_BODY_CLASS}`

/** The five sections. The four CM10 added are hosts the tab owns -- each a
 * sibling of the body, with a class of the tab's (`projects.ts`), holding one
 * panel of the module that draws it -- and the policy panel is a member of the
 * tab's first read's body, so it is found inside the body rather than beside
 * it. */
const THE_MANAGED = `${THE_PROJECTS} div.${PROJECTS_MANAGED_CLASS}`
const THE_ACCOUNT_KEYS = `${THE_PROJECTS} div.${PROJECTS_ACCOUNT_KEYS_CLASS}`
const THE_RETIREMENTS = `${THE_PROJECTS} div.${PROJECTS_RETIREMENTS_CLASS}`
const THE_CREDENTIALS_SERVICE = `${THE_PROJECTS} div.${PROJECTS_CREDENTIALS_SERVICE_CLASS}`
const THE_POLICY = `${THE_BODY} section.projects-panel[data-panel="policy"]`

/** THE_MANAGED_PANEL and THE_RETIREMENTS_PANEL are the one panel each of the two
 * hosts holds, and THE_FIVE is all five sections for the case that is about
 * every one of them at once. */
const THE_MANAGED_PANEL = `${THE_MANAGED} section.projects-panel[data-panel="managed"]`
const THE_RETIREMENTS_PANEL = `${THE_RETIREMENTS} section.projects-panel[data-panel="retirements"]`
const THE_FIVE = [THE_MANAGED, THE_ACCOUNT_KEYS, THE_RETIREMENTS, THE_CREDENTIALS_SERVICE, THE_POLICY]

/** tab is one button of the dashboard's navigation. */
function tab(view: string): string {
  return `nav.console-views button.console-view[data-view="${view}"]`
}

/** readsOf is a spawn log as records: one line per child the console ran,
 * written by `src/main/spawn.ts` before the child prints anything. It is the
 * only place a read that should not have happened is visible from outside. */
function readsOf(where: string): readonly SpawnLogEntry[] {
  let text: string
  try {
    text = readFileSync(where, 'utf8')
  } catch {
    return []
  }
  return text
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => JSON.parse(line) as SpawnLogEntry)
}

/** BUDGET is `fugaro budget show --json` for the installation this spec seeds:
 * the tab's **first** read, and the document the body -- the policy panel
 * included -- is drawn from. Its figures are not this spec's subject and are not
 * asserted; what it has to be is a document the body can be built from, so that
 * the panel this spec is about exists at all. */
const BUDGET: JSONDoc = {
  project: 'example-project',
  gcp_project: 'example-cloud',
  mode: 'enforce',
  day: '2026-11-06',
  max_reserve_usd: 5,
  global: {
    daily_cap_usd: 40,
    per_run_cap_usd: 8,
    counted_usd: 12.5,
    spent_usd: 4,
    notional_usd: 3.25,
    headroom_usd: 27.5,
    kill: null,
  },
  defaults: { repo_daily_cap_usd: 10, repo_per_run_cap_usd: 2 },
  repos: [
    {
      slug: 'r-1a2b3c4d/r-5e6f7a8b',
      repo: 'example-owner/example-repo',
      daily_cap_usd: 20,
      daily_cap_source: 'repo',
      per_run_cap_usd: 8,
      per_run_cap_source: 'global',
      counted_usd: 12.5,
      spent_usd: 4,
      notional_usd: 3.25,
      headroom_usd: 7.5,
      kill: null,
      running: [],
    },
  ],
  running: [],
  warnings: [],
}

/**
 * MANAGED_STATUS is `fugaro managed status --json` on a managed project with a
 * record and a retirement set (contract section 6.21, in this file's own
 * words). Two things about it are this spec's subject and not decoration: the
 * marks say **managed**, which is the word the managed section draws, and
 * `retire` carries **two** rows, which is the count the retirements section
 * draws -- one read, two sections, and the cases below read both.
 *
 * `plan_digest` is `fakeSecrets.hexDigest()` and not a literal run: the digest
 * is a 40-character hexadecimal value, which is a shape a scanner has an opinion
 * about, and this tree assembles one where it needs one.
 */
const MANAGED_STATUS: JSONDoc = {
  project: 'example-project',
  gcp_project: 'example-cloud',
  managed: true,
  reason: '',
  version: 3,
  generation: '17',
  audit: {
    at: '2026-11-06T09:12:00Z',
    command: 'config publish',
    verdict: 'allowed',
    identity: 'operator@example.test',
    plan_digest: hexDigest(),
  },
  audit_day: '2026-11-06',
  audit_count: 4,
  audit_state: '',
  retirements: {
    version: 1,
    at: '2026-11-06T09:12:00Z',
    retire: [
      {
        address: 'module.repo.google_secret_manager_secret.github_token',
        kind: 'secret container',
        cause: 'the repository no longer mounts a GitHub token',
        logical: 'github-token',
        protected: true,
      },
      {
        address: 'module.repo.google_cloud_run_v2_job.check',
        kind: 'check job',
        cause: 'the repository configures no check job',
        protected: false,
      },
    ],
    runs_in_flight: 0,
  },
  retirements_state: '',
}

/**
 * POLICY is `fugaro project policy show --json`: the `{project, policy}` object
 * CM4's row answers with, whose members are the ones CM7's section draws
 * (D1481). The panel's state is `read` exactly when this document carries a
 * `policy` object, so this is what makes the fifth section a drawn one.
 */
const POLICY: JSONDoc = {
  project: 'example-project',
  policy: {
    version: 3,
    base_branch: 'trunk',
    allow_repos: ['github/example-owner/example-repo'],
    accounts: { 'claude-ops': { allow_repos: ['github/example-owner/example-repo'] } },
  },
}

/** RETIREMENT_ROWS is how many rows the retirements section draws from the
 * document above, and the count the cases assert: a section that drew the
 * record's head and no rows would pass a weaker assertion. */
const RETIREMENT_ROWS = 2

/** ACCOUNT_KEY_ROWS is how many rows the review-account-keys section draws from
 * the committed `settings.json`. The fixture carries no `default_claude_account`
 * and no stage key, and `buildAccountKeys` draws its own `unset` row for each of
 * the two keys the section is about -- so the count is a fact about the console
 * and not about the document, and asserting it is what says the section drew its
 * table rather than its refusal. */
const ACCOUNT_KEY_ROWS = 2

/** MANAGED_DELAY_SECONDS is how long the managed read is made to take in the
 * second case: long enough that the sections whose reads have already answered
 * are unambiguously drawn while it is still out, and awaited with a timeout
 * above it (`LANDING_TIMEOUT_MS`). */
const MANAGED_DELAY_SECONDS = 5

/** LANDING_TIMEOUT_MS is what the assertions that wait for the delayed read to
 * land are given: Playwright's default is five seconds, which is exactly the
 * delay above, and a correct console would fail on a loaded machine. */
const LANDING_TIMEOUT_MS = 30_000

/**
 * seedManaged writes this spec's two documents beside **every** copy of the
 * stub, the way `cliStub`'s own seeders do: either copy can be the one the
 * console's resolution runs, and the stub answers a read from the files beside
 * itself. The account keys' document is the committed `settings.json`, which the
 * fixture answers `settings --json` with -- it is the tab's third read and the
 * sixth section's, and this spec does not write a settings document of its own
 * for the reason the fixture is committed at all: the section draws a document
 * the CLI could have printed.
 *
 * Hermetic: three files in a scratch directory, and no CLI but the stub.
 */
function seedManaged(cli: CliStub): void {
  const settings = readFileSync(join(consoleDir, 'test', 'fixtures', 'settings.json'), 'utf8')
  for (const where of [join(cli.home, '.local', 'bin'), cli.dir]) {
    mkdirSync(where, { recursive: true })
    writeFileSync(join(where, 'settings.json'), settings)
    writeFileSync(join(where, 'managed-status.json'), `${JSON.stringify(MANAGED_STATUS)}\n`)
    writeFileSync(join(where, 'policy-show.json'), `${JSON.stringify(POLICY)}\n`)
  }
}

/** delayManaged makes the managed read slow, by the seconds the stub sleeps
 * before it prints (`managed-status-delay.txt`). Both copies are written, for
 * `seedManaged`'s reason. */
function delayManaged(cli: CliStub, seconds: number): void {
  for (const where of [join(cli.home, '.local', 'bin'), cli.dir]) {
    writeFileSync(join(where, 'managed-status-delay.txt'), `${seconds}\n`)
  }
}

/** undelayManaged takes the sleep away again, so the case that follows reads the
 * same document without waiting for it. */
function undelayManaged(cli: CliStub): void {
  for (const where of [join(cli.home, '.local', 'bin'), cli.dir]) {
    rmSync(join(where, 'managed-status-delay.txt'), { force: true })
  }
}

/** unseedPolicy takes the policy document away, which is how the stub's
 * `project policy show --json` becomes a refusal (exit 2) rather than an answer:
 * the third case is about the panel that read has to draw. */
function unseedPolicy(cli: CliStub): void {
  for (const where of [join(cli.home, '.local', 'bin'), cli.dir]) {
    rmSync(join(where, 'policy-show.json'), { force: true })
  }
}

test.describe('the managed sections in the dashboard', () => {
  // One window, one page, and the cases in order: each visits the tab once, and
  // the state the case before it left -- the delay, the missing document -- is
  // part of what this file asserts about.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub
  let scratch: string
  let spawnLog: string

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    // The tab's first read, and the five sections' documents: the two the
    // fixture's new arms answer with, and the committed settings document the
    // account keys are drawn from.
    cli.seedSpend({ budget: { document: BUDGET } })
    seedManaged(cli)
    // The spawn log is the operator's own opt-in (`SPAWN_LOG_ENV`) and the only
    // place a read that should not have happened is visible from outside.
    scratch = mkdtempSync(join(tmpdir(), 'fugaro-console-managed-'))
    spawnLog = join(scratch, 'spawn.jsonl')
    app = await launchConsole({
      args: cli.launchArgs(consoleDir),
      env: { ...cli.env, [SPAWN_LOG_ENV]: spawnLog },
    })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
    rmSync(scratch, { recursive: true, force: true })
  })

  test('answers the tab in the frame it was opened in, and draws the five sections', async () => {
    // Live, the view this console opens on, makes one `runs ls --json` of its
    // own -- the rigor join -- and that read is not this case's subject. Waiting
    // for it to land is what makes the count below a count of *this tab's*
    // reads.
    await expect
      .poll(() => readsOf(spawnLog).filter((record) => record.argv[1] === 'runs').length, {
        message: 'the Live view never made its rigor join',
      })
      .toBeGreaterThan(0)
    const before = readsOf(spawnLog).length

    // **The click's own frame.** The tab is pressed and its four later sections
    // are marked in the same turn of the event loop, with nothing awaited in
    // between: the mark of each of the four reads that are in flight, and the
    // label naming the read it belongs to. The body is the tab's **first**
    // read's and is not drawn yet, so the policy panel -- a member of it -- does
    // not exist in this frame, and neither does the header, which is that
    // document's project line.
    const inTheFrame = await page.evaluate(
      (where) => {
        document.querySelector<HTMLElement>(where.button)?.click()
        const marked = (selector: string): { busy: string | null; label: string | null } => {
          const host = document.querySelector(selector)
          return {
            busy: host?.getAttribute('data-busy') ?? null,
            label: host?.querySelector('.console-busy-label')?.textContent ?? null,
          }
        }
        return {
          managed: marked(where.managed),
          accountKeys: marked(where.accountKeys),
          retirements: marked(where.retirements),
          credentialsService: marked(where.credentialsService),
          header: document.querySelector('h1.projects-project') !== null,
          policy: document.querySelector(where.policy) !== null,
        }
      },
      {
        button: tab('projects'),
        managed: THE_MANAGED,
        accountKeys: THE_ACCOUNT_KEYS,
        retirements: THE_RETIREMENTS,
        credentialsService: THE_CREDENTIALS_SERVICE,
        policy: THE_POLICY,
      },
    )
    expect(inTheFrame).toEqual({
      managed: { busy: 'true', label: MANAGED_BUSY },
      accountKeys: { busy: 'true', label: ACCOUNT_KEYS_BUSY },
      retirements: { busy: 'true', label: RETIREMENTS_BUSY },
      credentialsService: { busy: 'true', label: CREDENTIALS_SERVICE_BUSY },
      header: false,
      policy: false,
    })

    // **The four CM10 sections, each from its own read.** The managed state and
    // the retirements are one document and two sections, the account keys are
    // the settings read's, and the credentials service is doctor's.
    const managed = page.locator(THE_MANAGED_PANEL)
    await expect(managed).toHaveAttribute('data-state', 'read')
    await expect(managed.locator('h2.projects-title')).toHaveText(MANAGED_TITLE)
    await expect(managed.locator('span.projects-source')).toHaveText(MANAGED_SOURCE_LINE)
    await expect(managed.locator(`.${MANAGED_WORD_CLASS}`)).toHaveText('managed')

    const retirements = page.locator(THE_RETIREMENTS_PANEL)
    await expect(retirements).toHaveAttribute('data-state', 'read')
    await expect(retirements.locator('h2.projects-title')).toHaveText(RETIREMENTS_TITLE)
    await expect(retirements.locator('span.projects-source')).toHaveText(RETIREMENTS_SOURCE_LINE)
    await expect(retirements.locator(`li.${RETIREMENTS_ROW_CLASS}`)).toHaveCount(RETIREMENT_ROWS)

    const accountKeys = page.locator(`${THE_ACCOUNT_KEYS} section.${ACCOUNT_KEYS_CLASS}`)
    await expect(accountKeys).toHaveAttribute('data-state', 'read')
    await expect(accountKeys.locator(`h2.${ACCOUNT_KEYS_TITLE_CLASS}`)).toHaveText(ACCOUNT_KEYS_TITLE)
    await expect(accountKeys.locator(`span.${ACCOUNT_KEYS_SOURCE_CLASS}`)).toHaveText(ACCOUNT_KEYS_SOURCE)
    await expect(accountKeys.locator(`li.${ACCOUNT_KEYS_ROW_CLASS}`)).toHaveCount(ACCOUNT_KEY_ROWS)

    const credentialsService = page.locator(`${THE_CREDENTIALS_SERVICE} section.${CREDENTIALS_SERVICE_CLASS}`)
    // The stub's own `doctor --json` document carries no
    // `credentials-service-deployed` check, so the section says so: `no-check` is
    // a document that answered, and the state word beside it is `read`.
    await expect(credentialsService).toHaveAttribute('data-state', 'no-check')
    await expect(credentialsService.locator(`h2.${CREDENTIALS_SERVICE_TITLE_CLASS}`)).toHaveText(
      CREDENTIALS_SERVICE_TITLE,
    )
    await expect(credentialsService.locator(`span.${CREDENTIALS_SERVICE_SOURCE_CLASS}`)).toHaveText(
      CREDENTIALS_SERVICE_SOURCE,
    )
    await expect(credentialsService.locator(`p.${CREDENTIALS_SERVICE_NO_CHECK_CLASS}`)).toBeVisible()

    // **The fifth section is the policy panel**, and it is a read one now: CM10
    // flipped it out of its not-built state, so it names the argv CM4's row
    // allows rather than a command no CLI answers.
    const policy = page.locator(THE_POLICY)
    await expect(policy).toHaveAttribute('data-state', 'read')
    await expect(policy.locator('span.projects-state')).toHaveText('read')
    await expect(policy.locator('span.projects-source')).toHaveText(POLICY_NODE_SOURCE)

    // **The reads behind that drawing.** The two this task's fixture arms are
    // about, word for word -- the rows CM4 put on the surface, and nothing a
    // renderer chose -- and the two the tab already made for the sections beside
    // them.
    const asked = readsOf(spawnLog)
      .slice(before)
      .map((record) => record.argv.slice(1))
    expect(asked).toContainEqual(['managed', 'status', '--json', '--project', 'e2e'])
    expect(asked).toContainEqual(['project', 'policy', 'show', '--json', '--project', 'e2e'])
    expect(asked).toContainEqual(['settings', '--json', '--project', 'e2e'])
    expect(asked.filter((argv) => argv[0] === 'doctor' && argv.includes('--json'))).toHaveLength(1)
    // And no models read: the stub's line is below the models read's floor, so
    // the panel beside these five draws the console's own sentence and asks
    // nothing.
    expect(asked.filter((argv) => argv[0] === 'models')).toEqual([])

    // **No section offers a control that changes anything.** The five are
    // readings of documents a person runs other commands to change, and a
    // control here would be this console deciding what those commands do.
    for (const where of THE_FIVE) {
      await expect(page.locator(`${where} button, ${where} input, ${where} a`)).toHaveCount(0)
    }
  })

  test('paints each section the moment its own answer lands, and waits for no sibling', async () => {
    // The managed read is made slow on purpose: one document, two sections, and
    // five seconds in which the other three reads answer. What the tab must not
    // do is hold them until this one lands.
    delayManaged(cli, MANAGED_DELAY_SECONDS)
    await page.click(tab('projects'))

    // The managed section wears its own mark -- the read is out -- ...
    await expect(page.locator(THE_MANAGED)).toHaveAttribute('data-busy', 'true')
    await expect(page.locator(`${THE_MANAGED} .console-busy-label`)).toHaveText(MANAGED_BUSY)
    // ... while the sections whose reads have already answered are **drawn** and
    // their marks are down: the account keys settled their own read, the
    // credentials service settled doctor's, and the policy panel -- a member of
    // the body, repainted when its own read lands -- reads.
    await expect(page.locator(THE_ACCOUNT_KEYS)).not.toHaveAttribute('data-busy', 'true')
    await expect(page.locator(`${THE_ACCOUNT_KEYS} section.${ACCOUNT_KEYS_CLASS}`)).toHaveAttribute(
      'data-state',
      'read',
    )
    await expect(page.locator(THE_CREDENTIALS_SERVICE)).not.toHaveAttribute('data-busy', 'true')
    await expect(page.locator(THE_POLICY)).toHaveAttribute('data-state', 'read')

    // And the managed section's own answer is drawn when it lands, with the
    // retirements beside it -- the same document, the other section. Both are
    // awaited past the delay above, which is longer than Playwright's default.
    await expect(page.locator(THE_MANAGED)).not.toHaveAttribute('data-busy', 'true', {
      timeout: LANDING_TIMEOUT_MS,
    })
    await expect(page.locator(THE_MANAGED_PANEL)).toHaveAttribute('data-state', 'read', {
      timeout: LANDING_TIMEOUT_MS,
    })
    await expect(page.locator(THE_RETIREMENTS_PANEL)).toHaveAttribute('data-state', 'read', {
      timeout: LANDING_TIMEOUT_MS,
    })

    // The next case is about a refusal and not about this sleep: the delay goes
    // back the way it was found.
    undelayManaged(cli)
  })

  test('draws one section’s refusal while its four siblings stay drawn', async () => {
    // The policy document is taken away, so `project policy show --json` is
    // refused with exit 2 and the stub's own sentence. The panel has to draw
    // that refusal, and the four sections beside it have to keep what their own
    // reads answered -- a tab that took a sibling down with the refusal, or that
    // showed one refusal for the whole group, is what this case is for.
    unseedPolicy(cli)
    await page.click(tab('projects'))

    const policy = page.locator(THE_POLICY)
    await expect(policy).toHaveAttribute('data-state', 'unavailable')
    await expect(policy.locator('span.projects-state')).toHaveText('not read')
    await expect(policy.locator('span.projects-source')).toHaveText(POLICY_NODE_SOURCE)
    // The CLI's own sentence, not the console's "no document arrived": the stub
    // refused the read and said why.
    await expect(policy.locator('p.projects-because')).toContainText('no policy document is seeded')

    await expect(page.locator(THE_MANAGED_PANEL)).toHaveAttribute('data-state', 'read')
    await expect(page.locator(`${THE_ACCOUNT_KEYS} section.${ACCOUNT_KEYS_CLASS}`)).toHaveAttribute(
      'data-state',
      'read',
    )
    await expect(page.locator(THE_RETIREMENTS_PANEL)).toHaveAttribute('data-state', 'read')
    await expect(page.locator(`${THE_CREDENTIALS_SERVICE} section.${CREDENTIALS_SERVICE_CLASS}`)).toHaveAttribute(
      'data-state',
      'no-check',
    )
    // The four are not merely present: their documents are the ones the stub
    // answered with, so the refusal beside them took nothing down.
    await expect(page.locator(`${THE_MANAGED_PANEL} .${MANAGED_WORD_CLASS}`)).toHaveText('managed')
    await expect(page.locator(`${THE_RETIREMENTS_PANEL} li.${RETIREMENTS_ROW_CLASS}`)).toHaveCount(RETIREMENT_ROWS)
  })
})
