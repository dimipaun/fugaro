// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.

/**
 * reportedMode is the permission bits `stat` reports for a file or directory the
 * console created with `posixMode`. Windows keeps no owner/group/other bits: Node
 * reports 0o666 for every writable entry whatever mode it was created with, so a
 * mode the console asked for can only be read back on a POSIX host.
 */
export function reportedMode(posixMode: number): number {
  return process.platform === 'win32' ? 0o666 : posixMode
}
