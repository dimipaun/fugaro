// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
import { it } from 'vitest'

/**
 * itHostRoute is `it`, except on Windows, for a case that drives the console's
 * host-directory route with a stub `fugaro` (a script run through its `#!` line)
 * or with POSIX file kinds (a FIFO, a symbolic link).
 *
 * Windows has no `#!` line, so `spawn` cannot start such a stub there, and
 * Windows is the WSL route (`wsl.exe`, `src/main/wsl.ts`), whose own cases do
 * not read the host's directories at all. A case that asserts what that route
 * walks, or what a stub answered, describes the POSIX routes and nothing a
 * Windows run could reach; it runs on every other platform.
 */
export const itHostRoute = it.skipIf(process.platform === 'win32')
