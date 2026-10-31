// website/test/lib/build.mjs — build a mutated copy of the site (website plan, W1.11).
//
// A gate that is only ever run against the artifact `npm run build` produced proves what
// that artifact is, not what the gate would refuse. The cases that need a *different* build
// — the origins check with the analytics flag switched on — build a copy of the repository
// in a temp directory, the same shape `layout.mjs` and `faq-community.mjs` use, with one
// difference that matters when the test files run in parallel: the copy gets its OWN
// `node_modules` directory whose entries are symlinks into the installed tree, rather than a
// symlink to the whole tree. Vite keeps its dependency cache in `node_modules/.vite`, so a
// shared `node_modules` is a shared cache, and two builds rewriting one cache at once is the
// ENOTEMPTY flake. Every build is network-free: `npm ci` is not run, and the only executable
// started is the site's own installed Astro.

import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { REPO, SITE } from './html.mjs'

// What a site build cannot read: the Go module's tree and test data, the console, and the
// installed and built directories of the site.
const SKIP = new Set(['.git', 'internal', 'cmd', 'console', 'schemas', 'deploy', 'testdata'])
const SKIP_UNDER_WEBSITE = new Set(['node_modules', 'dist', '.astro'])

// The parts of `plugin/` a site build does read, and the reason `plugin` is no longer a skip
// of its own: the slider's "See it" panel quotes the skills the reader's agent reads
// (`website/src/components/HomeSlider.astro` reads `plugin/skills/working/SKILL.md`, changes
// spec of 2026-10-18 item 3), so a copy of the repository has to carry `plugin/skills`; and
// the changelog decides which releases are
// published from the plugin's own version (`src/lib/changelog.mjs`, the owner's brief of
// 2026-10-19), so it carries `plugin/.claude-plugin` too — a copy without it would fail the
// build rather than build a site. The plugin's Go sources and its fixtures are what a build
// does not read, and every copy of the repository — this one and the five test files that
// stage their own — leaves those out through this constant.
export const READ_UNDER_PLUGIN = new Set(['.claude-plugin', 'skills'])
// The caches a build writes inside node_modules, which each copy must own.
const CACHES = new Set(['.vite', '.astro'])

/** A private node_modules for a copy: every installed package linked, every cache its own. */
export function linkNodeModules(into) {
  const from = path.join(SITE, 'node_modules')
  mkdirSync(into, { recursive: true })
  for (const name of readdirSync(from)) {
    if (CACHES.has(name)) continue
    symlinkSync(path.join(from, name), path.join(into, name))
  }
}

/**
 * Copy what a site build reads of the repository into `root` — everything but SKIP and the
 * site's installed and built directories — with a private node_modules. Returns the copy's
 * website directory.
 */
export function copyRepository(root) {
  cpSync(REPO, root, {
    recursive: true,
    filter: (src) => {
      const name = path.basename(src)
      if (path.dirname(src) === REPO && SKIP.has(name)) return false
      if (path.basename(path.dirname(src)) === 'plugin' && !READ_UNDER_PLUGIN.has(name)) return false
      if (path.dirname(src) === SITE && SKIP_UNDER_WEBSITE.has(name)) return false
      return true
    },
  })
  const site = path.join(root, 'website')
  linkNodeModules(path.join(site, 'node_modules'))
  return site
}

/**
 * Take the campaigns' screenshots out of a copy's `website/data/landing.yaml`, and return whether
 * the file carried any. A fixture that replaces `website/data/console-screens.yaml` with rows of
 * its own leaves the four campaign heroes naming screens that file no longer carries, and the
 * campaign loader refuses a name no row carries at build time — a rule of its own
 * (`website/test/landing-data.mjs` reads it) rather than the subject of the fixture. On such a
 * copy each campaign hero renders the campaign's own drawing instead, and the console's page,
 * which is what those fixtures read, is built as the fixture meant it.
 */
export function dropCampaignScreenshots(site) {
  const file = path.join(site, 'data', 'landing.yaml')
  const before = readFileSync(file, 'utf8')
  const after = before.replace(/^ {4}screenshot: .+$/gm, '')
  if (after !== before) writeFileSync(file, after)
  return after !== before
}

/** Copy the repository, let `mutate` change the copy, build it, and clean up after `t`. */
export function buildCopy(t, mutate, prefix = 'fugaro-gates-') {
  const dir = mkdtempSync(path.join(tmpdir(), prefix))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const root = path.join(dir, 'repo')
  const site = copyRepository(root)
  mutate({ root, site })
  const result = spawnSync(process.execPath, [path.join(site, 'node_modules', 'astro', 'bin', 'astro.mjs'), 'build'], {
    cwd: site,
    encoding: 'utf8',
    env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' },
  })
  return {
    status: result.status,
    output: `${result.stdout ?? ''}${result.stderr ?? ''}`,
    dist: path.join(site, 'dist'),
    site,
  }
}
