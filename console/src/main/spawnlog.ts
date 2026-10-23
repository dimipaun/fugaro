// The opt-in spawn log (desktop-console plan Task 1.3, SECURITY-SENSITIVE;
// design section 5.1 rule 10, decision D20).
//
// The console writes nothing by default: no history, no cache of a document, no
// crash dump. This module is the one exception, and it is the whole of it --
// **when, and only when, `FUGARO_CONSOLE_SPAWN_LOG` names a path**, the main
// process appends one JSON object per line to that path, one line per child
// process it spawns. Unset, the writer is a no-op and no file descriptor is
// ever opened, which is the property `test/unit/spawnlog.test.ts` asserts first.
//
// The record holds **exactly four keys**, and the schema is the point rather
// than the encoding:
//
//   {"ts":"2026-10-15T14:22:31Z","argv":["/opt/homebrew/bin/fugaro","watch","--json","--project","fugaro"],"cwd":"/Users/owner/src/fugaro","project":"fugaro"}
//
// (Both paths in that line are the example's, not the format's: the first is
// wherever rule 4 resolved the binary -- `/usr/local/bin/fugaro` on a Linux
// machine -- and the second is the installation's own directory.)
//
//   - `ts` -- RFC 3339, UTC, second precision (the shape Check 59a's FACT lines
//     compare, and the reason the milliseconds `Date#toISOString` writes are
//     trimmed here rather than left in);
//   - `argv` -- the array as spawned, the binary's absolute path first. It is
//     `spawn.ts`'s `spawnedArgv(plan)` and not `plan.argv`: the two are the same
//     array on the host route, and on the WSL route the line is the *whole*
//     command line -- `wsl.exe -d <distro> --cd <dir> -- fugaro …` -- because
//     what ran is `wsl.exe`, and a line that showed only the CLI's own argv
//     would name a process the operator never sees in Task Manager. The wrapper
//     is a field of the plan (D1(a)) precisely so that this line can stay what it
//     has always been: the record of what ran, first word first;
//   - `cwd` -- absolute, the directory the child was given;
//   - `project` -- the chosen installation's project name, which is the value
//     passed as `--project` -- so **the bare `version` handshake records which
//     installation it was for**, even though it carries no flag at all
//     (design section 3.4 rule 3; Check 59a's `sort -u` depends on it).
//
// It holds no command output, no environment, no credential, no person and
// nothing a job wrote: the only installation-specific strings it can contain are
// the argv the app itself built. That is what makes it an audit of the app's own
// spawn contract rather than application state -- and it is why the two things
// that read it (Task 1.3's spawn-contract test and Check 59a's negative
// assertion) read it as evidence about the app, never as data for a screen.
//
// **Bounded at 1000 records.** The writer counts the records *it* writes in this
// process -- it never reads the file back, so it cannot and must not count
// anything else -- and at the 1001st spawn it appends one
// `{"truncated":true,"limit":1000}` line and writes nothing further. No
// rotation, no truncation in place, no deletion: a long session cannot grow an
// unbounded file, and the operator's earlier lines are never touched. (Per
// process, not per file, is also what Check 59a's mutation control relies on: it
// appends a line to a *copy* of the log and expects the console's own writes to
// be unaffected.)
//
// **The app never reads it back, never rotates it, never deletes it and never
// transmits it.** `test/unit/spawnlog.test.ts` holds the first of those as a
// grep over `console/src/`: this file is the only one that names the variable,
// and there is no read of a path anywhere in it. A spawn log the app read would
// be application state, which is exactly what D20 says it must not be.

import { closeSync, openSync, writeSync } from 'node:fs'

/**
 * SPAWN_LOG_ENV is the one variable that turns this module on. Nothing else
 * enables it, and there is no default path: an app that picked one would be an
 * app that writes to disk by default, which design section 5.1 rule 10 forbids.
 */
export const SPAWN_LOG_ENV = 'FUGARO_CONSOLE_SPAWN_LOG'

/** SPAWN_LOG_LIMIT is the record bound, in lines, before the truncation line. */
export const SPAWN_LOG_LIMIT = 1000

/**
 * SPAWN_LOG_MODE is the mode the file is *created* with -- owner read and write,
 * nobody else. `open(2)` applies a mode only when it creates the file, so a file
 * the operator already made keeps the mode it has: the log is the operator's
 * file at the operator's path, and the console does not chmod it.
 */
export const SPAWN_LOG_MODE = 0o600

/**
 * TRUNCATION_LINE is the one line written after the bound, verbatim, with no
 * `ts`, no `argv` and no `cwd`: it is a statement about the file, not a record
 * of a spawn, and Check 59a's key-name assertions must never see it as one.
 */
export const TRUNCATION_LINE = `{"truncated":true,"limit":${SPAWN_LOG_LIMIT}}`

/** SpawnLogEntry is one line's content, and there are exactly four fields. */
export interface SpawnLogEntry {
  readonly ts: string
  readonly argv: readonly string[]
  readonly cwd: string
  readonly project: string
}

/** RecordedSpawn is what a caller passes: everything but the timestamp, which
 * the writer stamps itself so no caller can put another clock in the log. */
export interface RecordedSpawn {
  readonly argv: readonly string[]
  readonly cwd: string
  readonly project: string
}

/**
 * SpawnLogWriter is the whole surface. `record` never throws: a log the console
 * cannot write must not stop the console from spawning the CLI, because the read
 * on screen matters more than the audit line -- so a failure is remembered in
 * `error` for whoever can surface it, and the writer stops trying.
 */
export interface SpawnLogWriter {
  /** `enabled` is false when the variable is unset or empty. */
  readonly enabled: boolean
  /** `path` is the operator's path, or null when nothing is written. */
  readonly path: string | null
  /** `records` is how many record lines this writer has written. */
  readonly records: number
  /** `error` is the first failure, or null. */
  readonly error: string | null
  /** record appends one line, or does nothing -- see the interface comment. */
  record(spawn: RecordedSpawn): void
  /** close releases the descriptor, if one was opened, and ends the writer: a
   * `record` after it is a no-op, never a reopen. */
  close(): void
}

/**
 * SpawnLogFs is the slice of `node:fs` this module uses, as an argument rather
 * than an import so the test can pass a recording fake and assert that the
 * unset case never opens anything. It is the same shape `src/main/window.ts`
 * uses for Electron's runtime: the decisions are reachable under plain Node.
 */
export interface SpawnLogFs {
  open(path: string, flags: string, mode: number): number
  write(fd: number, data: string): void
  close(fd: number): void
}

const NODE_FS: SpawnLogFs = {
  open: (path, flags, mode) => openSync(path, flags, mode),
  write: (fd, data) => {
    writeSync(fd, data)
  },
  close: (fd) => {
    closeSync(fd)
  },
}

/** rfc3339Seconds renders an instant the way the schema spells `ts`. */
export function rfc3339Seconds(instant: Date): string {
  return instant.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

/**
 * disabledSpawnLog is the writer for an unset variable: no path, no descriptor,
 * no work. It is a named export because the test asserts on it directly -- the
 * "unset means nothing on disk" half is about *this* object being what an unset
 * environment produces, not about a string comparison in the test.
 */
export function disabledSpawnLog(): SpawnLogWriter {
  return {
    enabled: false,
    path: null,
    records: 0,
    error: null,
    record: () => undefined,
    close: () => undefined,
  }
}

/**
 * createSpawnLog returns the writer for an environment. `appEnv` is the app's
 * own environment and nothing else is consulted: the variable is the operator's
 * opt-in, so reading it from anywhere but the environment the operator launched
 * the app with would be the app enabling its own log.
 */
export function createSpawnLog(
  appEnv: Readonly<Record<string, string | undefined>>,
  fs: SpawnLogFs = NODE_FS,
  now: () => Date = () => new Date(),
): SpawnLogWriter {
  const raw = appEnv[SPAWN_LOG_ENV]
  if (raw === undefined || raw === '') return disabledSpawnLog()

  const path = raw
  let fd: number | null = null
  let records = 0
  let stopped = false
  let failure: string | null = null

  const ensureOpen = (): number | null => {
    if (fd !== null) return fd
    try {
      fd = fs.open(path, 'a', SPAWN_LOG_MODE)
      return fd
    } catch (error) {
      failure = `the spawn log at ${path} could not be opened: ${describe(error)}`
      stopped = true
      return null
    }
  }

  const writeLine = (line: string): boolean => {
    const open = ensureOpen()
    if (open === null) return false
    try {
      fs.write(open, line + '\n')
      return true
    } catch (error) {
      failure = `the spawn log at ${path} could not be written: ${describe(error)}`
      stopped = true
      return false
    }
  }

  return {
    enabled: true,
    path,
    get records(): number {
      return records
    },
    get error(): string | null {
      return failure
    },
    record(spawn: RecordedSpawn): void {
      if (stopped) return
      // The bound, and the one line that states it. Written once, then nothing:
      // a later spawn appends neither a record nor a second truncation line.
      if (records >= SPAWN_LOG_LIMIT) {
        writeLine(TRUNCATION_LINE)
        stopped = true
        return
      }
      // `records` counts the lines that were *written*: a session whose log
      // cannot be opened must not believe it has recorded a thousand spawns and
      // then stop trying. A count that ran ahead of the file is the one way the
      // bound could hide a spawn the audit needed to see.
      // Exactly four keys, in the schema's order: JSON.stringify keeps the
      // insertion order of string keys, so the line a `jq` or a `grep` reads is
      // the line this object is. `argv` is copied so a caller cannot mutate an
      // array the log already recorded.
      const wrote = writeLine(
        JSON.stringify({
          ts: rfc3339Seconds(now()),
          argv: [...spawn.argv],
          cwd: spawn.cwd,
          project: spawn.project,
        } satisfies SpawnLogEntry),
      )
      if (wrote) records += 1
    },
    close(): void {
      // Closed is final: a later `record` must not reopen the path the caller
      // just released and append to it (review round 2 of PR 478).
      stopped = true
      if (fd === null) return
      try {
        fs.close(fd)
      } catch (error) {
        failure = `the spawn log at ${path} could not be closed: ${describe(error)}`
      }
      fd = null
    },
  }
}

/** describe renders an unknown thrown value as a bounded one-line string. */
function describe(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}
