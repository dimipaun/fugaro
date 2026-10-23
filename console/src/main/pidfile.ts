// The wrapper's pid file (docs/console-windows/implementation-plan.md T-F4,
// decision D6(a)).
//
// **What it is for.** On the WSL route the console does not start the CLI: it
// starts `wsl.exe`, which starts a shell inside a distribution, which `exec`s
// the CLI (see `WslWrapper` in `spawn.ts`). Killing the Windows-side `wsl.exe`
// is not documented to kill the Linux process it started (F24, U5), so the
// shell records the pid it is about to become -- `echo $$ > <file>` before
// `exec` -- and the console reads that file back and sends one `kill` inside
// the distribution. Without the file there is no pid to name, and a pid guessed
// at is a process killed that is not this console's child.
//
// **It is the console's own file.** The name is this module's, the directory is
// the private working directory the console created for the spawn (`--cd` is
// its distribution's spelling, T-D4), and the value is one integer. Nothing
// here is the operator's config, and nothing here reads anything config-shaped
// -- which is why `spawn.ts` can hand the reading to this module and stay free
// of file readers, the invariant `test/unit/configread.test.ts` holds it to.

import { readFileSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

/** WSL_PID_FILE_PREFIX is the base of the pid file's name. The name is made
 * unique per spawn rather than fixed (`<prefix><pid>.<n>`) because two reads of
 * one installation run in one working directory at the same time: a fixed name
 * would let the second spawn's shell overwrite the first's pid, and a
 * cancellation would then name a pid that is not its own child's. */
export const WSL_PID_FILE_PREFIX = 'fugaro.pid.'

/** wslPidFileSequence numbers the pid file names this process hands out. It is
 * a counter and not a random number: two spawns of one process must differ, and
 * a name a test can predict is a name a test can assert. */
let wslPidFileSequence = 0

/** pidFileFor names the file one spawn's wrapper writes. It lives in the
 * console's own working directory, under a name no other spawn of this process
 * uses -- see `WSL_PID_FILE_PREFIX`. The shell writes it by its **base name**,
 * relative to the directory the shell starts in, so the console reads it at the
 * host spelling of that same directory. */
export function pidFileFor(directory: string): string {
  wslPidFileSequence += 1
  return join(directory, `${WSL_PID_FILE_PREFIX}${process.pid}.${wslPidFileSequence}`)
}

/** readWrapperPid reads the pid the wrapper's shell wrote. `null` for a file
 * that is not there, is not readable, or does not hold a plain positive integer
 * -- the three ways the answer is "the console cannot name the pid", which is a
 * residual stated to the operator rather than a pid guessed at.
 *
 * The digits-only test is also what makes the value safe to hand to a shell:
 * `wslStopArgs` puts it in a `sh -c` command line, and a file that said
 * anything else would be shell syntax. Refusing here is the one guard, in the
 * one place, rather than a quoting rule every caller must remember. */
export function readWrapperPid(path: string): number | null {
  let text: string
  try {
    text = readFileSync(path, 'utf8')
  } catch {
    return null
  }
  const digits = text.trim()
  if (!/^[0-9]{1,10}$/.test(digits)) return null
  const pid = Number(digits)
  return pid > 0 ? pid : null
}

/** removeFile is `rm -f`'s promise: the file is gone afterwards, and it being
 * gone already is not an error. */
export function removeFile(path: string): void {
  try {
    unlinkSync(path)
  } catch {
    /* not there, or not ours to remove: nothing to do */
  }
}
