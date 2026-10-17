// The picker's view (desktop-console plan Task 3.4b; design section 3.4 rules 5
// and 7, D13, D14; owner ruling C9).
//
// The renderer half of the picker is deliberately the thinnest thing in this
// task, and that is the design's rule 7 rather than a shortcut: **the document
// holds no path, no id it can act on by itself, and no rule about what may be
// stored.** It draws what the main process answered, sends a project name and
// the tokens the main process issued, and nothing else. Every decision that
// matters -- is this binary executable, is this CLI in range, does this checkout
// belong to this project, is this token outstanding -- is made in
// `src/main/installations.ts`, where a test can hold it under plain Node and a
// renderer cannot reach it at all.
//
// `renderInstallations` is a pure function over the answer, so the view's whole
// behaviour is a unit test's: `test/unit/installations-view.test.ts` drives it
// with the two shapes the picker can be in -- an installation chosen and none --
// and with the refusal the store's file produces.

import type { InstallationReply } from '../preload/installations'

/** InstallationListRow is one row as `fugaro:installations/list` answers it. */
export interface InstallationListRow {
  readonly id: string
  readonly project: string
  readonly binary: string
  readonly config?: string
  readonly checkout?: string
  readonly active: boolean
  readonly mode: string
}

/** InstallationListAnswer is the whole of what `list` answers. `refusal` is the
 * sentence the *file* was refused with, and it is drawn with `path` so the
 * operator can fix the file rather than guess which one it is. */
export interface InstallationListAnswer {
  readonly rows: readonly InstallationListRow[]
  readonly refusal?: string
  readonly path: string
}

/** InstallationView is what the document shows: the status line, the rows, and
 * whether the add form is usable. */
export interface InstallationView {
  readonly status: string
  readonly rows: readonly InstallationListRow[]
  readonly canAdd: boolean
}

/** viewOf turns one reply into the view. A refusal from the main process is the
 * status line verbatim -- it is already a sentence written for a person -- and
 * an unreadable file additionally disables the form, because an add is refused
 * while the file cannot be read. */
export function viewOf(reply: InstallationReply): InstallationView {
  if (!reply.ok) return { status: reply.reason, rows: [], canAdd: false }
  const answer = reply.data as InstallationListAnswer
  if (answer.refusal !== undefined) {
    return { status: `${answer.path} could not be read: ${answer.refusal}`, rows: answer.rows, canAdd: false }
  }
  if (answer.rows.length === 0) {
    return { status: 'No installations yet. Add one below.', rows: [], canAdd: true }
  }
  const active = answer.rows.find((row) => row.active)
  const status = active === undefined
    ? 'No installation is chosen. Choose one below.'
    : `${active.project} is the installation this console is showing (${active.mode}).`
  return { status, rows: answer.rows, canAdd: true }
}

/** rowText is one row's line: the project, the binary and the state. It carries
 * no id and no `--config`: the id is the main process's handle for a row, and
 * the config path is a path, which the document draws only because the operator
 * chose it and must be able to recognise it. */
export function rowText(row: InstallationListRow): string {
  const parts = [`${row.project} — ${row.binary}`]
  if (row.config !== undefined) parts.push(`config ${row.config}`)
  if (row.checkout !== undefined) parts.push(`checkout ${row.checkout}`)
  if (row.mode !== 'ok') parts.push(`(${row.mode})`)
  if (row.active) parts.push('active')
  return parts.join(' · ')
}

/**
 * The bootstrap is guarded on the bridge being there, for the reason
 * `src/preload/index.ts` gives about its own guard: this file is imported by a
 * unit test under plain Node, where there is no `document` and no bridge, and
 * importing it must do nothing at all.
 */
const scope = globalThis as {
  readonly document?: {
    getElementById(id: string): { textContent: string | null; replaceChildren(...nodes: unknown[]): void } | null
  }
  readonly fugaroInstallations?: {
    list(): Promise<InstallationReply>
  }
}

if (scope.document !== undefined && scope.fugaroInstallations !== undefined) {
  const bridge = scope.fugaroInstallations
  const status = scope.document.getElementById('picker-status')
  const list = scope.document.getElementById('picker-list')
  void bridge.list().then((reply) => {
    const view = viewOf(reply)
    if (status !== null) status.textContent = view.status
    if (list !== null) list.replaceChildren()
  })
}
