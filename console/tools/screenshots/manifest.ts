// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The manifest the screenshot run leaves beside its pictures (owner ruling
// 2026-10-20, step 3): one entry per capture, with its pixel size, its sha256
// and a one-sentence alt text drafted from what the fixture shows.
//
// It exists because the pictures are git-ignored and the *facts about them* are
// what a later reader -- the owner adding a row to
// `website/data/console-screens.yaml`, or a reviewer asking whether the file the
// website serves is the file this run made -- needs: the website's slider takes
// `width` and `height` (so the browser reserves the box before the picture
// lands) and an `alt` (what a reader gets when it does not), and a sha256 is how
// a copy that arrived somewhere else is shown to be the same bytes.
//
// It is written even when a capture is *not* taken, because the omission is
// itself a fact: `screens` lists what was captured and `omitted` says what was
// not and why (the first-run page the owner's ruling names is one of these,
// until the onboarding page it belongs to is on `main`).

import { createHash } from 'node:crypto'

/** MANIFEST_FILE_NAME is the manifest's own name, beside the captures. */
export const MANIFEST_FILE_NAME = 'MANIFEST.json'

/** MANIFEST_VERSION is the document's version, so a reader can tell a shape it
 * knows from one it does not. */
export const MANIFEST_VERSION = 1

/**
 * CAPTURE_WINDOW is the window every capture is taken at, and the reason each
 * number is what it is:
 *
 *   - **1280x800** is the console's own window at the size the website's
 *     Console page draws a screenshot at, and a size every Mac laptop has;
 *   - **deviceScaleFactor 2** is a Retina capture: the PNG is 2560x1600, which
 *     is the size a reader on a Retina screen actually sees. The switch is
 *     `--force-device-scale-factor=2` on Electron's own command line, so the
 *     renderer lays out at 1280x800 CSS pixels and the raster is twice that;
 *   - **dark** is the one theme the console ships (the owner's Q1 ruling of
 *     2026-10-19: one value set, dark; the light set does not ship);
 *   - **titleBar: 'excluded'** is a statement about `page.screenshot`, which
 *     reads the window's **web contents**: the platform's own title bar and
 *     traffic lights are native chrome outside the page and are not in any
 *     capture. Every picture is therefore framed the same way, and the
 *     alternative -- capturing the whole window through a platform screenshot
 *     tool -- is the one thing this run deliberately does not do, because it
 *     would make the frame depend on the machine.
 */
export const CAPTURE_WINDOW = Object.freeze({
  width: 1280,
  height: 800,
  deviceScaleFactor: 2,
  theme: 'dark',
  titleBar: 'excluded',
} as const)

/** ScreenshotEntry is one captured picture. */
export interface ScreenshotEntry {
  /** file is the capture's name, relative to the output folder. */
  readonly file: string
  /** width and height are the PNG's own pixels -- 2560x1600 for a capture at
   * `CAPTURE_WINDOW`, and read from the file rather than assumed. */
  readonly width: number
  readonly height: number
  /** sha256 is the digest of the file's bytes, lowercase hex. */
  readonly sha256: string
  /** alt is what a reader gets when the picture does not arrive. One sentence,
   * drafted from the fixture the screen was drawn from. */
  readonly alt: string
}

/** Omission is a screen this run did not capture, and why. */
export interface Omission {
  readonly file: string
  readonly because: string
}

/** ScreenshotManifest is `console/screenshots/MANIFEST.json`. */
export interface ScreenshotManifest {
  readonly version: number
  readonly window: typeof CAPTURE_WINDOW
  readonly screens: readonly ScreenshotEntry[]
  readonly omitted: readonly Omission[]
}

/** SHA256_RE is a digest as this file writes one. */
const SHA256_RE = /^[0-9a-f]{64}$/

/** FILE_RE is a capture's name: a `.png` file name with no path in it, which is
 * what keeps the manifest a document about one folder. */
const FILE_RE = /^[a-z0-9][a-z0-9.-]*\.png$/

/**
 * buildManifest is the manifest for a run's captures, and the one place their
 * shape is checked.
 *
 * Every rule here is a way a manifest can be *readable but wrong*, which is
 * worse than one that is absent: two entries for one file (which picture does
 * the website show?), a name with a path in it (a manifest about a folder that
 * names a file outside it), a size that is not a whole positive number, a
 * digest that is not a digest, and an alt text that is empty -- a picture a
 * reader cannot identify is the one thing an alt exists to prevent.
 */
export function buildManifest(options: {
  readonly screens: readonly ScreenshotEntry[]
  readonly omitted?: readonly Omission[]
}): ScreenshotManifest {
  const seen = new Set<string>()
  for (const screen of options.screens) {
    if (!FILE_RE.test(screen.file)) {
      throw new Error(`the manifest cannot list ${JSON.stringify(screen.file)}: a capture is a .png file name with no path in it`)
    }
    if (seen.has(screen.file)) {
      throw new Error(`the manifest lists ${screen.file} twice: one file, one entry`)
    }
    seen.add(screen.file)
    if (!Number.isInteger(screen.width) || !Number.isInteger(screen.height) || screen.width <= 0 || screen.height <= 0) {
      throw new Error(`the manifest entry for ${screen.file} carries a pixel size that is not a positive whole number`)
    }
    if (!SHA256_RE.test(screen.sha256)) {
      throw new Error(`the manifest entry for ${screen.file} carries no sha256`)
    }
    if (screen.alt.trim() === '') {
      throw new Error(`the manifest entry for ${screen.file} carries no alt text`)
    }
  }
  return {
    version: MANIFEST_VERSION,
    window: CAPTURE_WINDOW,
    screens: [...options.screens],
    omitted: [...(options.omitted ?? [])],
  }
}

/**
 * serializeManifest writes the manifest's canonical form: two-space indent, the
 * keys in the order `ScreenshotManifest` declares them, and a trailing newline.
 *
 * It is a function rather than a `JSON.stringify` at the call site so the shape
 * is one thing: the file is read by a person and by `website/`'s loader, and a
 * diff between two runs should be the captures that changed and nothing else.
 */
export function serializeManifest(manifest: ScreenshotManifest): string {
  const screens = manifest.screens.map((screen) =>
    [
      '    {',
      `      "file": ${JSON.stringify(screen.file)},`,
      `      "width": ${String(screen.width)},`,
      `      "height": ${String(screen.height)},`,
      `      "sha256": ${JSON.stringify(screen.sha256)},`,
      `      "alt": ${JSON.stringify(screen.alt)}`,
      '    }',
    ].join('\n'),
  )
  const omitted = manifest.omitted.map((omission) =>
    ['    {', `      "file": ${JSON.stringify(omission.file)},`, `      "because": ${JSON.stringify(omission.because)}`, '    }'].join(
      '\n',
    ),
  )
  const list = (entries: readonly string[]): string => (entries.length === 0 ? '[]' : `[\n${entries.join(',\n')}\n  ]`)
  const window = [
    '  "window": {',
    `    "width": ${String(manifest.window.width)},`,
    `    "height": ${String(manifest.window.height)},`,
    `    "deviceScaleFactor": ${String(manifest.window.deviceScaleFactor)},`,
    `    "theme": ${JSON.stringify(manifest.window.theme)},`,
    `    "titleBar": ${JSON.stringify(manifest.window.titleBar)}`,
    '  },',
  ].join('\n')
  return `{\n  "version": ${String(manifest.version)},\n${window}\n  "screens": ${list(screens)},\n  "omitted": ${list(omitted)}\n}\n`
}

/** sha256Of is the digest the manifest carries, of the bytes as they are on
 * disk -- the same bytes the website would serve. */
export function sha256Of(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex')
}
