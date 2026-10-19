// website/test/accent.mjs — the accent against the logo it is supposed to be.
//
// The brief's "Done when" line asks that "the accent colour matches the logo", and
// `docs/website/redesign/reconciliation.md` RF18 shows why that is a measurement rather
// than a matter of taste: the palette in force had `--accent: #8B5CF6` at 258.3°, about 60°
// from the logo's own hue. The owner ruled on 2026-10-18 (D180) that the accent is the
// logo's hue, and a gate is what stops the next palette edit from being made from memory.
//
// So this file does not read a number somebody wrote down. It **decodes
// `brand/fugaro-logo.png` itself** — the standard library only, `node:zlib`'s `inflateSync`
// and the five PNG row filters, no dependency — and runs reconciliation §5.1's method over
// the pixels: drop alpha < 250 and near-black (HSV value < 0.05), then take the **circular**
// mean hue of the pixels with saturation ≥ 0.5 (sin/cos sums, which is the only correct
// average for an angle). The result is the logo's hue, and three checks hold the palette in
// force to it:
//
//   1. the logo decodes to the shape §5.1 sampled and to the hue it records (198.4°), so a
//      logo that changed is noticed rather than silently re-baselined;
//   2. the palette in force's `--accent` is within 5° of that hue (D180);
//   3. its `--warn` is at least 90° from it (D181), so the warning tone can never be read
//      as the accent.
//
// The last two are one exported function, `accentFailures`, and two fixture palettes below
// prove that each of them can fail — the accent fixture is the palette in force before D180
// (`#8B5CF6`, 258.3°), which is the exact mistake this gate exists to refuse.
//
// It reads the sources, not the build: the palette in force is the one `tokens.css` imports,
// resolved the way `website/test/brand.mjs` resolves `--ground`, so there is no build to
// run and no network. Standard library only (`node:test`, `node:assert`, `node:zlib`).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { inflateSync } from 'node:zlib'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)
const LOGO = path.join(REPO, 'brand', 'fugaro-logo.png')
const STYLES_DIR = path.join(SITE, 'src', 'styles')
const TOKENS_FILE = 'tokens.css'

// ---- the numbers, and where each one comes from -----------------------------------------

/** The logo's circular mean hue at saturation ≥ 0.5, as §5.1 records it. */
export const LOGO_HUE = 198.4
/** §5.1's figure is a mean over 107,437 pixels; a different count means a different method. */
export const LOGO_PIXELS = 107437
/** …and 83,028 of them carry saturation ≥ 0.5, which is the set the mean is taken over. */
export const LOGO_PIXELS_AT_SATURATION = 83028
/** How far the accent may drift from the logo's own hue (D180, §5.2). */
export const ACCENT_TOLERANCE = 5
/** How far the warning tone must stay from it (D181, §5.2). */
export const WARN_MIN_DISTANCE = 90

/** The pixel filter §5.1 applies, as the three numbers it states. */
export const MIN_ALPHA = 250
export const MIN_VALUE = 0.05
export const MIN_SATURATION = 0.5

// ---- the logo, decoded ------------------------------------------------------------------

/**
 * A PNG, decoded to 8-bit samples per channel: `{ width, height, colourType, bitDepth,
 * interlace, bpp, pixels }`. `pixels` is the unfiltered image, row by row, `bpp` bytes per
 * pixel and no row padding.
 *
 * The five filters of the PNG specification (§9.2) are the whole of it, and a filter this
 * function did not implement would corrupt every pixel after it rather than fail — which is
 * why an unknown one throws here instead of being skipped.
 */
export function decodePng(file) {
  const bytes = readFileSync(file)
  assert.equal(
    bytes.subarray(0, 8).toString('hex'),
    '89504e470d0a1a0a',
    `${file} is not a PNG`,
  )

  let offset = 8
  let header = null
  const idat = []
  while (offset + 8 <= bytes.length) {
    const length = bytes.readUInt32BE(offset)
    const type = bytes.subarray(offset + 4, offset + 8).toString('latin1')
    const data = bytes.subarray(offset + 8, offset + 8 + length)
    if (type === 'IHDR') {
      header = {
        width: data.readUInt32BE(0),
        height: data.readUInt32BE(4),
        bitDepth: data[8],
        colourType: data[9],
        compression: data[10],
        filter: data[11],
        interlace: data[12],
      }
    } else if (type === 'IDAT') {
      idat.push(data)
    } else if (type === 'IEND') {
      break
    }
    offset += 12 + length
  }
  assert.ok(header, `${file} has no IHDR chunk`)
  assert.equal(header.compression, 0, `${file}: only the PNG's deflate compression is defined`)
  assert.equal(header.filter, 0, `${file}: only the PNG's adaptive filtering is defined`)
  assert.equal(header.bitDepth, 8, `${file}: this decoder reads 8 bits per channel, not ${header.bitDepth}`)

  const CHANNELS = { 0: 1, 2: 3, 4: 2, 6: 4 }
  const channels = CHANNELS[header.colourType]
  assert.ok(channels, `${file}: colour type ${header.colourType} is not one of the PNG's five`)
  assert.equal(header.interlace, 0, `${file} is interlaced; §5.1 sampled a non-interlaced image`)

  const raw = inflateSync(Buffer.concat(idat))
  const bpp = channels
  const stride = header.width * bpp
  const pixels = Buffer.alloc(header.height * stride)
  let at = 0
  for (let y = 0; y < header.height; y++) {
    const filter = raw[at]
    at += 1
    const row = raw.subarray(at, at + stride)
    at += stride
    const line = pixels.subarray(y * stride, (y + 1) * stride)
    const up = y === 0 ? null : pixels.subarray((y - 1) * stride, y * stride)
    for (let x = 0; x < stride; x++) {
      const left = x >= bpp ? line[x - bpp] : 0
      const above = up ? up[x] : 0
      const corner = up && x >= bpp ? up[x - bpp] : 0
      let value = row[x]
      if (filter === 1) value += left
      else if (filter === 2) value += above
      else if (filter === 3) value += (left + above) >> 1
      else if (filter === 4) {
        const p = left + above - corner
        const [pa, pb, pc] = [Math.abs(p - left), Math.abs(p - above), Math.abs(p - corner)]
        value += pa <= pb && pa <= pc ? left : pb <= pc ? above : corner
      } else assert.equal(filter, 0, `${file}: row ${y} carries filter ${filter}, which the PNG specification does not define`)
      line[x] = value & 0xff
    }
  }

  return { ...header, bpp, pixels }
}

/** Hue in degrees, saturation and value, each 0–1 for the last two (the HSV of a colour). */
export function hsv(r, g, b) {
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const span = max - min
  let hue = 0
  if (span !== 0) {
    if (max === r) hue = ((g - b) / span) % 6
    else if (max === g) hue = (b - r) / span + 2
    else hue = (r - g) / span + 4
    hue = ((hue * 60) % 360 + 360) % 360
  }
  return [hue, max === 0 ? 0 : span / max, max]
}

/**
 * §5.1's sample of a logo: `sampled` is the pixels that survive the alpha and value filter,
 * `kept` those of them that also clear the saturation threshold, and `hue` their circular
 * mean. The mean is taken as `atan2(Σ sin h, Σ cos h)` rather than as the arithmetic mean,
 * because a handful of pixels just below 0° would otherwise drag an average of angles near
 * 359° to the middle of the circle.
 */
export function sampleLogo(file, { saturation = MIN_SATURATION } = {}) {
  const image = decodePng(file)
  assert.equal(image.bpp, 4, `${file}: §5.1 samples an RGBA image, and this one has ${image.bpp} bytes per pixel`)
  let x = 0
  let y = 0
  let sampled = 0
  let kept = 0
  for (let at = 0; at < image.pixels.length; at += image.bpp) {
    if (image.pixels[at + 3] < MIN_ALPHA) continue
    const [hue, sat, value] = hsv(
      image.pixels[at] / 255,
      image.pixels[at + 1] / 255,
      image.pixels[at + 2] / 255,
    )
    if (value < MIN_VALUE) continue
    sampled += 1
    if (sat < saturation) continue
    x += Math.cos((hue * Math.PI) / 180)
    y += Math.sin((hue * Math.PI) / 180)
    kept += 1
  }
  assert.ok(kept > 0, `${file}: no pixel passes §5.1's filter at saturation ≥ ${saturation}, so there is no hue to take`)
  return { hue: ((Math.atan2(y, x) * 180) / Math.PI + 360) % 360, sampled, kept }
}

// ---- the palette, and the two rules over it ---------------------------------------------

/** A six-digit hex colour's hue in degrees. A palette value is an opaque literal. */
export function hueOf(value) {
  const hex = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.exec(String(value).trim())
  assert.ok(hex, `${JSON.stringify(value)} is not an opaque hex colour, so its hue cannot be read`)
  const digits = hex[1].length === 3 ? [...hex[1]].map((c) => c + c).join('') : hex[1]
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(digits.slice(i, i + 2), 16) / 255)
  return hsv(r, g, b)[0]
}

/** How far apart two hues are on the circle, in degrees: never more than 180. */
export function circularDistance(a, b) {
  const d = Math.abs(a - b) % 360
  return Math.min(d, 360 - d)
}

/** The palette in force: the one file `tokens.css` imports, as `brand.mjs` reads it. */
export function paletteInForce() {
  const tokens = readFileSync(path.join(STYLES_DIR, TOKENS_FILE), 'utf8')
  const imported = /@import\s+"\.\/(palette-[a-z-]+\.css)"/.exec(tokens)
  assert.ok(imported, `${TOKENS_FILE} imports no palette, so there is no accent in force to check`)
  const css = readFileSync(path.join(STYLES_DIR, imported[1]), 'utf8')
  const values = new Map()
  for (const [, name, value] of css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/--([a-zA-Z0-9-]+)\s*:\s*([^;{}]+);/g)) {
    values.set(name, value.trim())
  }
  return { file: imported[1], tokens: values }
}

/**
 * The lines a reader sees when the palette in force has drifted from the logo. `tokens` is a
 * Map of token name (without the leading dashes) to value, which is what `paletteInForce`
 * returns; `logo` is the hue in degrees that `sampleLogo` measured.
 */
export function accentFailures(tokens, logo) {
  const failures = []

  const accent = tokens.get('accent')
  if (!accent) {
    failures.push("--accent is not declared: the palette in force has to declare the token D180 makes the logo's hue")
  } else {
    const hue = hueOf(accent)
    const distance = circularDistance(hue, logo)
    if (distance > ACCENT_TOLERANCE) {
      failures.push(
        `--accent is ${hue.toFixed(1)}°, ${distance.toFixed(1)}° from the logo's ${logo.toFixed(1)}°: D180 makes the accent the logo's hue, and this check allows ${ACCENT_TOLERANCE}° of drift`,
      )
    }
  }

  const warn = tokens.get('warn')
  if (!warn) {
    failures.push('--warn is not declared: the palette in force has to declare the warning tone D181 keeps apart from the accent')
  } else {
    const hue = hueOf(warn)
    const distance = circularDistance(hue, logo)
    if (distance < WARN_MIN_DISTANCE) {
      failures.push(
        `--warn is ${hue.toFixed(1)}°, ${distance.toFixed(1)}° from the logo's ${logo.toFixed(1)}°: D181 keeps the warning tone at least ${WARN_MIN_DISTANCE}° from the accent, so the two can never be read as one colour`,
      )
    }
  }

  return failures
}

// ---- the cases --------------------------------------------------------------------------

test('the logo is the plain 8-bit, non-interlaced RGBA PNG §5.1 sampled', () => {
  const image = decodePng(LOGO)
  assert.equal(image.width, 1024, 'brand/fugaro-logo.png width')
  assert.equal(image.height, 1024, 'brand/fugaro-logo.png height')
  assert.equal(image.colourType, 6, 'brand/fugaro-logo.png is RGBA, so its ground is not its colour')
  assert.equal(image.bitDepth, 8, 'brand/fugaro-logo.png is 8 bits per channel')
  assert.equal(image.interlace, 0, 'brand/fugaro-logo.png is not interlaced')
})

test("the logo's hue is the one §5.1 records, over the pixels §5.1 counted", () => {
  const { hue, sampled, kept } = sampleLogo(LOGO)
  assert.equal(
    sampled,
    LOGO_PIXELS,
    `brand/fugaro-logo.png has ${sampled} pixels with alpha ≥ ${MIN_ALPHA} and value ≥ ${MIN_VALUE}; §5.1 counted ${LOGO_PIXELS}. A different count means a different logo, or a different method than the one this gate claims to run`,
  )
  assert.equal(
    kept,
    LOGO_PIXELS_AT_SATURATION,
    `${kept} of those pixels carry saturation ≥ ${MIN_SATURATION} and §5.1 counted ${LOGO_PIXELS_AT_SATURATION}: the mean below is taken over this set, so its size is half of what makes the figure reproducible`,
  )
  assert.ok(
    Math.abs(hue - LOGO_HUE) <= 0.5,
    `the logo's circular mean hue is ${hue.toFixed(1)}°, and §5.1 records ${LOGO_HUE}°: if the logo changed, the accent, this figure and docs/website/redesign/reconciliation.md §5 move together`,
  )
})

test('the palette in force takes its accent from the logo, and keeps the warning tone away from it', () => {
  const { file, tokens } = paletteInForce()
  const logo = sampleLogo(LOGO).hue
  for (const line of accentFailures(tokens, logo)) {
    assert.fail(`${file}: ${line}`)
  }
})

// The red step, kept. The palette in force before D180 was `--accent: #8B5CF6` at 258.3°,
// 59.9° from the logo's hue — the exact mistake this gate exists to refuse — so the fixture
// is that accent, in an otherwise passing palette, and the line it produces is required
// word for word from the same function the case above calls.
test('the accent check refuses the palette in force before D180, with the line it produces', () => {
  const before = new Map([
    ['accent', '#8B5CF6'],
    ['warn', '#F5A524'],
  ])
  assert.deepEqual(accentFailures(before, LOGO_HUE), [
    "--accent is 258.3°, 59.9° from the logo's 198.4°: D180 makes the accent the logo's hue, and this check allows 5° of drift",
  ])
})

test('the warning check refuses a warning tone that is the accent, with the line it produces', () => {
  const tooClose = new Map([
    ['accent', '#17A8E6'],
    ['warn', '#17A8E6'],
  ])
  assert.deepEqual(accentFailures(tooClose, LOGO_HUE), [
    "--warn is 198.0°, 0.4° from the logo's 198.4°: D181 keeps the warning tone at least 90° from the accent, so the two can never be read as one colour",
  ])
})
