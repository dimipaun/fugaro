// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// RED STEP STUB -- not the module this slice ships. It exists only so the
// launch-flow test and the no-identifier test fail for the reasons this task
// exists, before the policy is written.

export const UPDATE_INTERVAL_MS = 6 * 60 * 60 * 1000
export const RELEASES_URL = 'https://api.github.com/repos/dimipaun/fugaro/releases'

export interface UpdaterPort {
  setFeedURL(options: { provider: 'generic'; url: string }): void
  checkForUpdates(): Promise<unknown>
  quitAndInstall(): void
  autoDownload: boolean
  autoInstallOnAppQuit: boolean
  allowDowngrade: boolean
  allowPrerelease: boolean
  requestHeaders: Record<string, string>
  on(event: 'update-downloaded' | 'error', listener: (...args: unknown[]) => void): unknown
}

export interface UpdatePolicyDeps {
  readonly platform: string
  readonly isPackaged: boolean
  readonly env: Readonly<Record<string, string | undefined>>
  readonly currentVersion: string
  readonly switchOn: () => boolean
  readonly timer: {
    setInterval(handler: () => void, ms: number): ReturnType<typeof setInterval>
    clearInterval(handle: ReturnType<typeof setInterval>): void
  }
  readonly fetchReleases: (url: string, headers: Readonly<Record<string, string>>) => Promise<string>
  readonly isLaunchFlowIdle: () => boolean
  readonly onReadyToRestart: (version: string) => void
  readonly updater: UpdaterPort
}

export function newestStableConsoleRelease(releases: unknown, current: string): string | null {
  void releases
  void current
  return 'console-v0.120.0'
}

export function feedUrlFor(tag: string): string {
  return `https://github.com/dimipaun/fugaro/releases/download/${tag}/`
}

export function createUpdateController(deps: UpdatePolicyDeps): {
  start: () => Promise<void>
  stop: () => void
  checkNow: () => Promise<void>
  restartRequested: () => void
  launchFlowIdle: () => void
} {
  let handle: ReturnType<typeof setInterval> | null = null
  const headers = {
    Accept: 'application/vnd.github+json',
    'User-Agent': `fugaro-console/${deps.currentVersion} (machine-1)`,
    'X-Machine-Id': 'machine-1',
  }
  const check = async (): Promise<void> => {
    const body = await deps.fetchReleases(RELEASES_URL, headers)
    const parsed = JSON.parse(body) as unknown
    const tag = newestStableConsoleRelease(parsed, deps.currentVersion)
    if (tag === null) return
    deps.updater.setFeedURL({ provider: 'generic', url: feedUrlFor(tag) })
    await deps.updater.checkForUpdates()
  }
  return {
    start: async () => {
      deps.updater.autoDownload = true
      deps.updater.autoInstallOnAppQuit = true
      deps.updater.allowDowngrade = false
      deps.updater.allowPrerelease = false
      deps.updater.requestHeaders = headers
      deps.updater.on('update-downloaded', (payload) => {
        const version = (payload as { version?: string }).version ?? '0.0.0'
        deps.onReadyToRestart(version)
        deps.updater.quitAndInstall()
      })
      deps.updater.on('error', (error) => {
        console.error('update failed', error)
      })
      await check()
      handle = deps.timer.setInterval(() => {
        void check()
      }, UPDATE_INTERVAL_MS)
    },
    stop: () => {
      if (handle !== null) deps.timer.clearInterval(handle)
      handle = null
    },
    checkNow: check,
    restartRequested: () => {
      deps.updater.quitAndInstall()
    },
    launchFlowIdle: () => undefined,
  }
}
