// The console's application icon (the owner's brief of 2026-10-19): the same
// mark the website serves, and the two places macOS takes an icon from.
//
// **The file.** `build/icon.png` is the console's icon, and it is the
// *website's* mark rather than a second drawing of it: it is byte-for-byte
// `brand/app-icon-1024.png`, the app-icon master `scripts/gen-brand-icons.py`
// writes from `brand/fugaro-logo.png` -- the file `brand/README.md` and
// `website/README.md` both name as the source of truth every icon the site
// serves is derived from, and the same composition (the artwork on the
// palette's `--ground` at 80% extent) the site's own home-screen icon
// `website/public/android-chrome-512x512.png` is. `test/unit/icon.test.ts`
// holds it to that in two ways: the bytes must equal the master's, and the mark
// must still be the site's (a coarse block-average comparison against the 512
// the site ships), so the two cannot drift apart unnoticed.
//
// **Where the packager takes it.** `build.mac.icon` in `console/package.json`
// names that file, and electron-builder converts the 1024 PNG to the bundle's
// `.icns` on the macOS release runner (`docs/pending-workflows/console-release.yml`
// runs `npm run package` there). Nothing is committed as a `.icns`: it is a
// derived binary that only the packing step needs, and generating one here
// would be a second copy of the artwork to keep in step.
//
// **Where a running console takes it.** A packaged bundle carries its icon, so
// the dock and the app switcher show it with nothing to do. While the console
// runs *unpackaged* -- `npm run build && npx electron .`, which is what a
// developer does and what `test/e2e/` launches -- there is no bundle and
// therefore no icon, and the dock shows Electron's own until the process sets
// one. That is `installDevDockIcon` below, and macOS's door for it is
// `app.dock.setIcon`.
//
// **Why not `BrowserWindow`'s `icon` option.** Because on macOS Electron
// ignores it: a window's chrome and the dock take the *application's* icon, and
// on macOS that is the bundle's. A key that does nothing on the one platform
// this console runs on would be worse than absent -- and the console's own
// rule (the one `console/README.md` applies to `<link>` tags) is that a pointer
// to a file the packaged app does not carry is a broken pointer, and the icon
// is deliberately not in `build.files`: it goes into the bundle as the bundle's
// icon, not into the asar as an asset. So the window options are left alone and
// this module is the dev-time door.
//
// As everywhere else in this tree (`window.ts`, `protocol.ts`), this module
// imports Electron *types* only, so `test/unit/icon.test.ts` can drive every
// answer it gives under plain Node, with a fake for the one object that would
// otherwise need a running Electron: the dock.

import { join } from 'node:path'

/** CONSOLE_ICON_RELATIVE_PATH is where the packager finds the icon, relative to
 * the console's own directory (`console/`), and the one place the path is
 * written: `build.mac.icon` in `console/package.json` names the same string,
 * and `test/unit/icon.test.ts` asserts the two agree and that the file is
 * there. */
export const CONSOLE_ICON_RELATIVE_PATH = 'build/icon.png'

/** consoleIconPath is that file under the app's own path -- the directory
 * `app.getAppPath()` answers, which is the console's directory in development
 * and the bundle's `Resources` directory when packaged. */
export function consoleIconPath(appPath: string): string {
  return join(appPath, CONSOLE_ICON_RELATIVE_PATH)
}

/**
 * DockLike is Electron's `app.dock` narrowed to the one method this module
 * uses. The narrowing is not tidiness: `app.dock` is `Dock | undefined`, and
 * it is `undefined` on every platform that is not macOS, so the function below
 * has to answer for its absence rather than assume it.
 */
export interface DockLike {
  setIcon(image: string): void
}

/** DevDockIconDeps is everything `installDevDockIcon` decides from, as
 * arguments rather than as reads of the process: the platform, whether this
 * process is a packaged bundle, the dock (absent off macOS), and the icon's
 * path. `src/main/index.ts` is the one caller and passes Electron's own
 * answers. */
export interface DevDockIconDeps {
  /** `process.platform`: only `darwin` has a dock to set. */
  readonly platform: NodeJS.Platform
  /** `app.isPackaged`: a packaged bundle already carries its icon. */
  readonly packaged: boolean
  /** `app.dock`, undefined on every platform that is not macOS. */
  readonly dock: DockLike | undefined
  /** The icon's absolute path (`consoleIconPath(app.getAppPath())`). */
  readonly iconPath: string
}

/**
 * installDevDockIcon gives the running process the console's icon, where macOS
 * has somewhere to put it, and answers whether it did.
 *
 * The three refusals are each a real case rather than a defensive check:
 *
 *   - **not macOS** -- `app.dock` does not exist, and there is nothing to set;
 *   - **packaged** -- the bundle's icon is already the file above (the packager
 *     put it there from `build.mac.icon`), so setting it again would be a
 *     second, later answer to a question that is already answered;
 *   - **no dock** -- `app.dock` is `undefined` off macOS, and a `Dock` that a
 *     test supplies as absent is the same case.
 *
 * What it deliberately does *not* do is check that the file exists. In
 * development it does (it is a committed file in the checkout), and if it ever
 * did not, Electron's `setIcon` is the thing that should say so -- a second
 * `existsSync` here would be a check that can only disagree with the one the
 * call itself makes.
 *
 * The return value is what lets the test say *which* path ran rather than only
 * that nothing was thrown: `true` means the dock was given the icon.
 */
export function installDevDockIcon(deps: DevDockIconDeps): boolean {
  if (deps.platform !== 'darwin' || deps.packaged || deps.dock === undefined) return false
  deps.dock.setIcon(deps.iconPath)
  return true
}
