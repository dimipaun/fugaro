// The console's application icon (the owner's brief of 2026-10-19): the
// website's own mark, at the size the packager wants, with nothing to keep in
// step by hand.
//
// The claim this file exists for is **the two icons are one file**. The
// console's icon is `build/icon.png` and the website's mark is
// `brand/app-icon-1024.png` -- the app-icon master `scripts/gen-brand-icons.py`
// writes from `brand/fugaro-logo.png`, the source of truth `brand/README.md`
// and `website/README.md` both name for every icon the site serves. Three
// things are asserted, and each one is a different way for them to come apart:
//
//   1. **the bytes are equal** -- copy something else over the console's icon,
//      or regenerate the brand master and forget the console, and the first test
//      fails with both digests in the message. This is the "one source of
//      truth" half: no second drawing, no second palette, no hand-edited copy.
//   2. **the mark is the site's** -- decoded and compared, block by block,
//      against the icon the website actually ships
//      (`website/public/android-chrome-512x512.png`). Byte equality alone would
//      be satisfied by a console icon that matched some *other* file in
//      `brand/`; this says the picture is the one a visitor's browser tab and
//      home screen get. The tolerance is a measured figure rather than a guess
//      (see the test).
//   3. **the packager is told about it** -- `build.mac.icon` names the file,
//      the file it names is this one and is a PNG the packager can convert, and
//      the icon is not smuggled into the asar through `build.files`.
//
// The rest of the file is the *dev-time* half of the same decision: an
// unpackaged console has no bundle to carry the icon, so `src/main/index.ts`
// gives it to the dock through `installDevDockIcon`, which is driven here with
// a recording dock. That function is where "macOS, unpackaged, and a dock to
// set" is decided; the entry point is the call, and the last test reads
// `src/main/index.ts` as text to hold it there -- the way `startup.test.ts`
// holds the handshake, because the entry point imports Electron's runtime and
// cannot be imported under vitest.

import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { inflateSync } from 'node:zlib'

import { describe, expect, it } from 'vitest'

import { CONSOLE_ICON_RELATIVE_PATH, consoleIconPath, installDevDockIcon } from '../../src/main/icon'
import type { DockLike } from '../../src/main/icon'

const CONSOLE_DIR = fileURLToPath(new URL('../..', import.meta.url))
const REPO_DIR = fileURLToPath(new URL('../../..', import.meta.url))

/** THE_MASTER is the file the console's icon is a copy of: the app-icon master
 * the site's icon set is generated from, opaque on the palette's `--ground`. */
const THE_MASTER = join(REPO_DIR, 'brand', 'app-icon-1024.png')

/** THE_SITE_ICON is the icon the website itself serves (its manifest's 512 px
 * `any` icon), which is the same composition at half the size. */
const THE_SITE_ICON = join(REPO_DIR, 'website', 'public', 'android-chrome-512x512.png')

const CONSOLE_ICON = join(CONSOLE_DIR, CONSOLE_ICON_RELATIVE_PATH)

/**
 * MEAN_CHANNEL_TOLERANCE is how far one block's average colour may sit from the
 * website's own icon's, out of 255.
 *
 * It is a *measured* figure, not a guess. The console's icon is the master at
 * 1024 and the site's is the same artwork at 512, so the two are the same
 * picture sampled at two sizes: the site's file was written by Pillow's LANCZOS
 * resampler, and this test averages 32 x 32 pixel blocks, so what is left
 * between them is the resampler's ringing along the mark's edges. Over the
 * 16 x 16 grid below that measured **3.96** at its worst block (mean 0.24), so
 * the tolerance is set at 8: twice the measured figure, and still far below
 * what any different mark, palette or crop would produce -- a block that held
 * the mark where the site's held the ground moves by tens, not by eight.
 */
const MEAN_CHANNEL_TOLERANCE = 8

/** GRID is the block grid the two icons are compared on: 16 x 16 blocks, which
 * is 32 x 32 pixels of the site's 512 and 64 x 64 of the console's 1024 -- big
 * enough that resampling differences average out, small enough that a mark
 * which moved a few pixels would still show. */
const GRID = 16

/** png reads a PNG's header and finds its end: the signature, the IHDR chunk
 * first, and a final IEND -- so a truncated file or one that is not a PNG at
 * all is a failure here rather than something the packer discovers on the
 * release runner. */
function png(file: string): { width: number; height: number; colourType: number; bytes: number } {
  const b = readFileSync(file)
  expect(b.subarray(0, 8).toString('hex'), `${file} is not a PNG`).toBe('89504e470d0a1a0a')
  expect(b.subarray(12, 16).toString('latin1'), `${file} has no IHDR chunk first`).toBe('IHDR')
  expect(b.readUInt32BE(8), `${file}'s first chunk is not 13 bytes`).toBe(13)
  const chunks: string[] = []
  let offset = 8
  while (offset + 8 <= b.length) {
    const length = b.readUInt32BE(offset)
    chunks.push(b.subarray(offset + 4, offset + 8).toString('latin1'))
    offset += 12 + length
  }
  expect(offset, `${file} ends mid-chunk`).toBe(b.length)
  expect(chunks.at(-1), `${file} has no IEND: it is truncated`).toBe('IEND')
  return {
    width: b.readUInt32BE(16),
    height: b.readUInt32BE(20),
    colourType: b.readUInt8(25),
    bytes: b.length,
  }
}

/** decodePng is an 8-bit, non-interlaced PNG decoder -- IHDR to pixels, the
 * five filter types included -- so the comparison below can look at the
 * pictures rather than only at the files. It is here rather than in a
 * dependency for the reason every other tool in this tree is: every entry of
 * `console/package.json` is an exact version -- seven devDependencies, no
 * runtime dependencies -- and all four dependency sections are pinned by
 * `test/unit/pinning.test.ts`, so an image library would be a pin to argue for
 * and a large tree for the one thing this file needs. It throws on anything it
 * does not implement
 * (another bit depth, a palette, interlacing) rather than guessing. */
function decodePng(file: string): { width: number; height: number; channels: number; pixels: Buffer } {
  const b = readFileSync(file)
  const width = b.readUInt32BE(16)
  const height = b.readUInt32BE(20)
  const bitDepth = b.readUInt8(24)
  const colourType = b.readUInt8(25)
  const interlace = b.readUInt8(28)
  expect([bitDepth, interlace], `${file} is not 8-bit, non-interlaced`).toEqual([8, 0])
  const channels = colourType === 2 ? 3 : colourType === 6 ? 4 : 0
  expect(channels, `${file} is neither RGB nor RGBA (colour type ${colourType})`).toBeGreaterThan(0)
  const idat: Buffer[] = []
  let offset = 8
  while (offset + 8 <= b.length) {
    const length = b.readUInt32BE(offset)
    if (b.subarray(offset + 4, offset + 8).toString('latin1') === 'IDAT') {
      idat.push(b.subarray(offset + 8, offset + 8 + length))
    }
    offset += 12 + length
  }
  const raw = inflateSync(Buffer.concat(idat))
  const stride = width * channels
  const pixels = Buffer.alloc(height * stride)
  let at = 0
  for (let y = 0; y < height; y += 1) {
    const filter = raw[at] as number
    at += 1
    const line = raw.subarray(at, at + stride)
    at += stride
    const above = y === 0 ? Buffer.alloc(stride) : pixels.subarray((y - 1) * stride, y * stride)
    const row = pixels.subarray(y * stride, (y + 1) * stride)
    for (let x = 0; x < stride; x += 1) {
      const left = x >= channels ? (row[x - channels] as number) : 0
      const up = above[x] as number
      const upLeft = x >= channels ? (above[x - channels] as number) : 0
      let value = line[x] as number
      if (filter === 1) value += left
      else if (filter === 2) value += up
      else if (filter === 3) value += (left + up) >> 1
      else if (filter === 4) {
        const p = left + up - upLeft
        const pa = Math.abs(p - left)
        const pb = Math.abs(p - up)
        const pc = Math.abs(p - upLeft)
        value += pa <= pb && pa <= pc ? left : pb <= pc ? up : upLeft
      } else expect(filter, `${file} uses filter ${filter}, which is not a PNG filter`).toBe(0)
      row[x] = value & 0xff
    }
  }
  return { width, height, channels, pixels }
}

/** blockMeans averages an image into a GRID x GRID grid of channels, so two
 * images of different sizes can be compared as the same picture. */
function blockMeans(image: { width: number; height: number; channels: number; pixels: Buffer }): number[] {
  const { width, height, channels, pixels } = image
  const means: number[] = []
  for (let gy = 0; gy < GRID; gy += 1) {
    for (let gx = 0; gx < GRID; gx += 1) {
      const x0 = Math.floor((gx * width) / GRID)
      const x1 = Math.floor(((gx + 1) * width) / GRID)
      const y0 = Math.floor((gy * height) / GRID)
      const y1 = Math.floor(((gy + 1) * height) / GRID)
      const sums = new Array<number>(channels).fill(0)
      let count = 0
      for (let y = y0; y < y1; y += 1) {
        for (let x = x0; x < x1; x += 1) {
          for (let c = 0; c < channels; c += 1) sums[c] = (sums[c] as number) + (pixels[(y * width + x) * channels + c] as number)
          count += 1
        }
      }
      for (let c = 0; c < channels; c += 1) means.push((sums[c] as number) / count)
    }
  }
  return means
}

interface BuildConfig {
  readonly mac: { readonly icon?: string }
  readonly files: readonly string[]
}

const build = (JSON.parse(readFileSync(join(CONSOLE_DIR, 'package.json'), 'utf8')) as { build: BuildConfig }).build

describe('the console’s icon is the website’s mark', () => {
  it('is the app-icon master, byte for byte', () => {
    // The one source of truth: the file the console ships is the file the
    // brand's generator wrote, and there is no second drawing of the mark in
    // this tree to keep in step. A regenerated master (the generator paints the
    // ground colour whatever palette is in force, so re-running it after a
    // palette change rewrites every icon) fails here until the console's copy is
    // refreshed from it, which is the point -- the README says how.
    expect(existsSync(THE_MASTER), `${THE_MASTER} is missing: the site's icon set has no master`).toBe(true)
    const master = readFileSync(THE_MASTER)
    const shipped = readFileSync(CONSOLE_ICON)
    expect(shipped.equals(master), `${CONSOLE_ICON} is not ${THE_MASTER}`).toBe(true)
  })

  it('is a valid opaque 1024 x 1024 PNG', () => {
    // 1024 rather than the 512 the site serves, and **not because the artwork is
    // natively that big**. It is not: the source logo's visible artwork (the
    // alpha bbox of `brand/fugaro-logo.png` at the generator's own threshold,
    // alpha > 8) is 548 x 371 pixels, so the generator's `fit(art, 1024, 0.80,
    // ground)` scales it up by 1.4949 to about 819 x 555, and the site's 512 is
    // the same artwork the other way at 0.7474. The 1024 master is therefore an
    // ~1.5x interpolation of the mark, and so is this file: what it buys is the
    // size the `.icns` needs (electron-builder refuses anything under 512 for
    // icns, and every slot from 512 down to 16 is a downscale of this one), not
    // detail the source does not have. The README says the same, with the
    // measured figures and what a genuinely crisp 1024 would need.
    //
    // It remains the right file: it is the largest rendering the generator
    // produces, and upscaling the site's 512 here with a second resampler would
    // be a worse interpolation *and* a second derivation of the mark.
    const icon = png(CONSOLE_ICON)
    expect(icon.width).toBe(1024)
    expect(icon.height).toBe(1024)
    // Colour type 2 is RGB: opaque. The site's icons are opaque for the same
    // reason (the maskable one too), and a macOS icon with holes in it shows the
    // desktop through the mark.
    expect(icon.colourType).toBe(2)
    expect(icon.bytes).toBeGreaterThan(0)
  })

  it('is the picture the website serves, not merely a file of the right size', () => {
    // The mark itself, at two sizes. The console's is the 1024 master, the
    // site's is the 512 it ships to a browser tab and a home screen; both are
    // the logo cropped to its artwork and centred on the palette's ground, so
    // block averages of the two must agree closely (the tolerance above records
    // how closely, and why).
    const mine = blockMeans(decodePng(CONSOLE_ICON))
    const site = blockMeans(decodePng(THE_SITE_ICON))
    expect(site.length).toBe(mine.length)
    let worst = 0
    let worstBlock = -1
    for (const [index, value] of mine.entries()) {
      const difference = Math.abs(value - (site[index] as number))
      if (difference > worst) {
        worst = difference
        worstBlock = index
      }
    }
    expect(
      worst,
      `block ${worstBlock} of the console's icon differs from the site's by ${worst.toFixed(2)} (tolerance ${MEAN_CHANNEL_TOLERANCE}): the console is not shipping the website's mark`,
    ).toBeLessThanOrEqual(MEAN_CHANNEL_TOLERANCE)
  })

  it('is the icon the packaging config names, and is what the packager can convert', () => {
    // The packager is told explicitly rather than left to electron-builder's
    // default (`build/icon.icns`, or a `build/icon.png` it would find by
    // convention): the file the config names and the file the dev-time dock gets
    // are one string, `CONSOLE_ICON_RELATIVE_PATH`, so they cannot be two
    // different files.
    expect(build.mac.icon).toBe(CONSOLE_ICON_RELATIVE_PATH)
    expect(existsSync(join(CONSOLE_DIR, build.mac.icon as string))).toBe(true)
    const icon = png(join(CONSOLE_DIR, build.mac.icon as string))
    // electron-builder converts a PNG to the bundle's `.icns` and refuses one
    // smaller than 512 x 512; this is the assertion that the file it is handed
    // is one it will accept.
    expect(Math.min(icon.width, icon.height)).toBeGreaterThanOrEqual(512)
  })

  it('is not smuggled into the asar as an asset', () => {
    // The icon's road into the bundle is `build.mac.icon`: it becomes the app's
    // `icon.icns`, which is the application's icon and not a file the app reads.
    // A pattern in `build.files` that picked up `build/icon.png` would put a
    // second copy of the artwork inside the asar, where nothing loads it -- and
    // `fuses.test.ts`'s exact-list test is the other half of this: every pattern
    // there ends in `.js`, `.html` or `.css`.
    for (const pattern of build.files) expect(pattern, `build.files names the icon: ${pattern}`).not.toContain('build/')
  })
})

describe('the dev-time icon: an unpackaged console has no bundle to carry it', () => {
  /** recordingDock is a DockLike that remembers what it was asked to show. */
  function recordingDock(): { dock: DockLike; shown: string[] } {
    const shown: string[] = []
    return { dock: { setIcon: (image: string) => void shown.push(image) }, shown }
  }

  const APP_PATH = '/Applications/Fugaro Console.app/Contents/Resources'

  it('sets the dock icon on macOS, from the file under the app’s own path', () => {
    const { dock, shown } = recordingDock()
    const iconPath = consoleIconPath(APP_PATH)
    expect(installDevDockIcon({ platform: 'darwin', packaged: false, dock, iconPath })).toBe(true)
    expect(shown).toEqual([iconPath])
    expect(iconPath).toBe(join(APP_PATH, CONSOLE_ICON_RELATIVE_PATH))
  })

  it('does nothing once packaged: the bundle already carries the icon', () => {
    // The packager put `build/icon.png` into the bundle as `icon.icns` when it
    // built it, so a packaged console that set the dock icon again would be
    // answering a question the bundle has already answered.
    const { dock, shown } = recordingDock()
    expect(installDevDockIcon({ platform: 'darwin', packaged: true, dock, iconPath: consoleIconPath(APP_PATH) })).toBe(false)
    expect(shown).toEqual([])
  })

  it('does nothing off macOS, where there is no dock at all', () => {
    // `app.dock` is `undefined` on every platform that is not macOS, which is
    // why the deps carry it as optional: the Linux e2e job must not throw here.
    for (const platform of ['linux', 'win32'] as const) {
      const { dock, shown } = recordingDock()
      expect(installDevDockIcon({ platform, packaged: false, dock, iconPath: consoleIconPath(APP_PATH) })).toBe(false)
      expect(shown).toEqual([])
    }
    const { shown } = recordingDock()
    expect(installDevDockIcon({ platform: 'darwin', packaged: false, dock: undefined, iconPath: consoleIconPath(APP_PATH) })).toBe(false)
    expect(shown).toEqual([])
  })

  it('is the icon the entry point actually installs', () => {
    // `src/main/index.ts` imports Electron's *runtime*, so it cannot be imported
    // here; reading it as text is what makes "the entry point is wired" a
    // checked claim (`startup.test.ts`'s source scans are the same shape). The
    // call's own arguments are the interesting half: the two facts it reads off
    // Electron rather than deciding -- `app.isPackaged` and `app.dock` -- and
    // the path built from `app.getAppPath()`, so the packaged bundle and the
    // checkout are one expression.
    const index = readFileSync(join(CONSOLE_DIR, 'src', 'main', 'index.ts'), 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/^\s*\/\/.*$/gm, '')
    expect(index).toContain('installDevDockIcon({')
    expect(index).toContain('packaged: app.isPackaged')
    expect(index).toContain('dock: app.dock')
    expect(index).toContain('iconPath: consoleIconPath(app.getAppPath())')
  })
})
