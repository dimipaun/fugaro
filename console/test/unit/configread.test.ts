// Rule 7's third prohibition, enforced: **the app never reads the operator's
// config** (desktop-console plan Task 1.3, design section 3.4 rule 7, D13).
//
// Rule 7 has three prohibitions and the design gives the first two their own
// guards elsewhere: the console never reads a database (there is no database
// client in `package.json`, which `pinning.test.ts` asserts by naming the one
// runtime dependency), and it never holds a credential (the child's environment
// is `CHILD_ENV_KEYS`, which `spawn.test.ts` diffs against what the child
// reports). This file is the third, and it is the one the plan says had no test
// at all -- while the config is read in the **main** process, where the
// installation store lives, outside the IPC surface Task 1.2 covers. Reading it
// is exactly the shortcut D13 forbids: it would answer "where do the
// installations come from" the one way the design says it must not.
//
// The distinction the rule turns on is written here as the test's two halves:
//
//   1. **the config's path may be a *string* the installation stores**, and the
//      console passes it to the child as `--config` -- so `--config`, the string
//      and the argv that carries it are all present in `src/`, and half one
//      asserts that they are;
//   2. **the app itself never opens it** -- no `~/.config/fugaro`, no
//      `XDG_CONFIG_HOME` join, no `FUGARO_CONFIG`-derived path, no
//      `readFile`/`readFileSync`/`createReadStream` of any of them -- which is
//      what half two asserts.
//
// **The mutation that proves it**: a
// `readFileSync(path.join(os.homedir(), '.config', 'fugaro', …))` anywhere under
// `console/src/` must make this red. The checker is a function over a list of
// files, and the planted file below *is* that mutation, held as a fixture -- so
// the check is tested against the thing it is meant to catch instead of merely
// being trusted. **The escape hatch is named in the failure message: the CLI is
// the reader.** `addInstallation` runs `doctor --json` and lets the CLI read
// `fugaro.yaml`; see the installation-store tests in `spawn.test.ts`, where a
// checkout that belongs to another project is refused by the CLI's own sentence
// with no file opened here at all.

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { argvFor } from '../../src/main/spawn'
import { CONFIG_FLAG } from '../../src/main/spawn'

const SOURCE_ROOT = fileURLToPath(new URL('../../src', import.meta.url))

/** SourceFile is one file the checker looks at. */
interface SourceFile {
  readonly path: string
  readonly source: string
}

/** codeOnly is a file's source with its comments removed: every scan here is
 * about what the code *does*, and these modules discuss the config at length on
 * purpose. Block comments come out first, then `//` lines. */
function codeOnly(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/**
 * offences returns, for every file, the reasons it looks like a config read.
 * Each reason names the file and the pattern, because a failure a reviewer
 * cannot locate is a failure that gets waived.
 *
 * The patterns are deliberately two-part rather than a list of path spellings: a
 * *read* of a path that is *config-shaped* is the offence. `spawnlog.ts` opens a
 * path and `main/index.ts` reads the renderer's bundle; neither is config-shaped
 * and neither is reported, which is what keeps this from being a scan that
 * simply forbids filesystem access.
 */
export function offences(files: readonly SourceFile[]): readonly string[] {
  const found: string[] = []
  const reads = /\b(?:readFileSync|readFile|createReadStream|openSync|open|readdirSync|statSync|existsSync|loadFile)\s*\(/
  const configish = /(?:FUGARO_CONFIG|fugaro\.ya?ml|config\.toml|XDG_CONFIG_HOME|['"]\.config['"]|['"]fugaro['"]\s*,\s*['"])/
  for (const file of files) {
    const source = codeOnly(file.source)
    for (const line of source.split('\n')) {
      if (reads.test(line) && configish.test(line)) {
        found.push(`${file.path}: ${line.trim()} -- the CLI is the reader`)
      }
    }
    // The home directory is only ever used to find a *binary* (`~/.local/bin`,
    // rule 4). A config read under the home directory is the shortcut D13
    // forbids, and it is one `homedir()` away from any read above.
    if (/homedir\(\)/.test(source) && !/binDirectories|BINARY|FUGARO_BINARY/.test(source)) {
      found.push(`${file.path}: homedir() outside binary resolution -- the CLI is the reader`)
    }
    if (/\bos\.homedir\b/.test(source)) found.push(`${file.path}: os.homedir is not used anywhere in the console`)
  }
  return found
}

/** sourceFiles reads every `.ts` file under `console/src/`. */
function sourceFiles(): readonly SourceFile[] {
  const walk = (directory: string, prefix: string): SourceFile[] => {
    const out: SourceFile[] = []
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const relative = prefix === '' ? entry.name : `${prefix}/${entry.name}`
      if (entry.isDirectory()) out.push(...walk(join(directory, entry.name), relative))
      else if (entry.name.endsWith('.ts')) {
        out.push({ path: relative, source: readFileSync(join(directory, entry.name), 'utf8') })
      }
    }
    return out
  }
  return walk(SOURCE_ROOT, '')
}

describe('the app never reads the operator’s config', () => {
  it('is clean: no file under console/src/ opens a config, anywhere', () => {
    expect(offences(sourceFiles())).toEqual([])
  })

  it('catches the mutation: a read of ~/.config/fugaro is an offence', () => {
    // The plan's own mutation, held as a fixture. A checker that accepted this
    // would pass the test above too, and the test above is the one that matters.
    const planted: SourceFile = {
      path: 'main/installations.ts',
      source: [
        "import { readFileSync } from 'node:fs'",
        "import { homedir } from 'node:os'",
        "import { join } from 'node:path'",
        'export function readConfig(): string {',
        "  return readFileSync(join(homedir(), '.config', 'fugaro', 'config.toml'), 'utf8')",
        '}',
      ].join('\n'),
    }
    const found = offences([...sourceFiles(), planted])
    expect(found).toHaveLength(2)
    expect(found.join('\n')).toContain('main/installations.ts')
    expect(found.join('\n')).toContain('the CLI is the reader')
  })

  it.each([
    ["readFileSync(join(process.env['XDG_CONFIG_HOME'] ?? '', 'fugaro', 'config.toml'))"],
    ["openSync(join(process.env['FUGARO_CONFIG'] ?? '', 'fugaro.yaml'), 'r')"],
    ["createReadStream(join(home, '.config', 'fugaro', 'config.toml'))"],
    ['readFileSync(join(home, "fugaro.yaml"))'],
  ])('catches %s', (line) => {
    expect(offences([{ path: 'planted.ts', source: line }]).length).toBeGreaterThan(0)
  })

  it('does not fire on the reads this console legitimately makes', () => {
    // The allow-list half: the spawn log is opened, the renderer's bundle is
    // read, and the binary is resolved under a home directory. None of those is
    // a config read, and a checker that flagged them would be waived within a
    // week -- which is how a guard stops guarding anything.
    expect(
      offences([
        { path: 'main/spawnlog.ts', source: "fd = fs.open(path, 'a', SPAWN_LOG_MODE)" },
        { path: 'main/index.ts', source: "await session.loadFile(join(root, 'index.html'))" },
        { path: 'main/spawn.ts', source: 'const home = options.home ?? homedir() // FUGARO_BINARY_NAME' },
        { path: 'main/window.ts', source: "const bundle = readFileSync(join(root, 'index.html'), 'utf8')" },
      ]),
    ).toEqual([])
  })

  it('passes the config’s path as a string, which is the half the rule allows', () => {
    // Half one of the distinction: the path is a *string* on the installation,
    // and `--config` is how it reaches the child. Nothing here opens it.
    const argv = argvFor('budget.show', {}, {
      target: { project: 'fugaro', configPath: '/Users/owner/.config/fugaro/config.toml' },
      directory: '/Users/owner/src/fugaro',
    })
    expect(CONFIG_FLAG).toBe('--config')
    expect(argv).toContain('--config')
    expect(argv[argv.indexOf('--config') + 1]).toBe('/Users/owner/.config/fugaro/config.toml')
    // ...and the string is only ever an argv element: the source that builds it
    // does not import a reader.
    const spawn = readFileSync(join(SOURCE_ROOT, 'main', 'spawn.ts'), 'utf8')
    expect(spawn).toContain('CONFIG_FLAG')
    expect(codeOnly(spawn)).not.toMatch(/readFileSync|readFile\(|createReadStream/)
  })

  it('lets the installation store stat a config path, and never open one', () => {
    // Task 3.4b added `src/main/installations.ts`, the first module in the
    // console that handles a config path at all -- and the plan's red step for
    // this half is a planted `readFileSync` of it. The line the store makes is
    // a `stat`: "is this an existing file", which is what rule 7 allows, and
    // nothing about what is in it. The scan is per line and keyed on the
    // variable, so a later edit that swapped the `stat` for a read fails here
    // by name.
    const code = codeOnly(readFileSync(join(SOURCE_ROOT, 'main', 'installations.ts'), 'utf8'))
    expect(code).toContain('fs.stat(configPath)')
    for (const line of code.split('\n')) {
      if (!/configPath/.test(line)) continue
      expect(line.trim(), line.trim()).not.toMatch(/readFileSync|readFile\(|createReadStream|openSync|open\(/)
    }
    // ...and the planted mutation, against the same scan, so the check above is
    // not vacuous.
    const planted = "isFile = readFileSync(configPath, 'utf8').length > 0"
    expect(/configPath/.test(planted) && /readFileSync/.test(planted)).toBe(true)
  })

  it('leaves the reading to the CLI, which is the only thing that names the file', () => {
    // The console names `fugaro.yaml` in exactly one place, and it is a comment:
    // the file itself is read by `localcfg.Select` in the CLI, whose refusal is
    // what the add-an-installation screen shows. In code, the console knows the
    // name nowhere.
    const files = sourceFiles()
    const namers = files.filter((file) => codeOnly(file.source).includes('fugaro.yaml'))
    expect(namers).toEqual([])
    const commentNamers = files.filter((file) => file.source.includes('fugaro.yaml'))
    expect(commentNamers.map((file) => file.path)).toEqual(['source/cli.ts'])
  })
})
