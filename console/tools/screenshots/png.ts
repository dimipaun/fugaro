// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The PNG half of the screenshot run: what a captured file has to be for the
// run to call it a picture (owner ruling 2026-10-20, step 4: "exits non-zero if
// any capture is blank (a pixel-variance check: a mostly uniform image fails)
// or smaller than expected").
//
// **Why this reads PNG bytes itself.** The run has to say whether a file it
// just wrote is a picture of the console or a blank rectangle, and the two ways
// to get that answer from outside are both wrong here: asking a browser or an
// image library is a dependency the console does not have (every specifier in
// `console/package.json` is an exact, pinned version, held by
// `test/unit/pinning.test.ts`), and asking the *renderer* for its own pixels is
// asking the thing under test to grade itself. A PNG is a small, closed format
// -- a signature, a header, deflate-compressed scanlines -- and reading one is
// forty lines of `node:zlib`. So the check is made on the bytes a person would
// open, which is also the artefact the website will serve.
//
// What it reads is deliberately narrow, and it says so rather than guessing:
// 8 bits per channel, colour types 6 (RGBA) and 2 (RGB), not interlaced -- which
// is what Chromium's `page.screenshot` writes and the only shape a screenshot
// of this console can arrive in. Anything else is a refusal naming the property
// it did not understand, so a future Chromium that writes something else fails
// this run loudly instead of being graded as blank.

import { inflateSync } from 'node:zlib'

/** PNG_SIGNATURE is the eight bytes every PNG opens with. */
const PNG_SIGNATURE: readonly number[] = Object.freeze([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])

/**
 * BLANK_STDDEV_FLOOR is the standard deviation of pixel luminance, in 0..1,
 * below which a capture is called blank. 0.01 is 2.55 grey levels: a uniform
 * rectangle is 0, a rectangle with a single hairline of text on it is well
 * under, and any real drawing of the console -- a dark ground, rows of text,
 * one accent colour -- measures an order of magnitude above it. The number is
 * a floor against "the window painted nothing", not a judgement about the
 * picture, which is what the alt text and the owner's own eyes are for.
 */
export const BLANK_STDDEV_FLOOR = 0.01

/** SAMPLED_PIXELS is how many pixels the variance is measured over: a
 * 2560x1600 capture is four million pixels, and sampling an evenly spaced
 * subset of them is both faster and steadier than reading all of them. */
export const SAMPLED_PIXELS = 200_000

/** PngImage is a decoded capture: its size, and its pixels as 8-bit RGBA. */
export interface PngImage {
  readonly width: number
  readonly height: number
  /** pixels is `width * height` RGBA quadruples, row by row. */
  readonly pixels: Uint8Array
}

function fail(what: string): never {
  throw new Error(`the screenshot run cannot read this PNG: ${what}`)
}

/** readUint32 is one big-endian 32-bit integer at an offset. */
function readUint32(bytes: Uint8Array, at: number): number {
  const a = bytes[at]
  const b = bytes[at + 1]
  const c = bytes[at + 2]
  const d = bytes[at + 3]
  if (a === undefined || b === undefined || c === undefined || d === undefined) fail('the file ends inside a length')
  return ((a << 24) | (b << 16) | (c << 8) | d) >>> 0
}

/** CHANNELS is the channel count of each colour type this module reads. */
const CHANNELS: ReadonlyMap<number, number> = new Map([
  [2, 3],
  [6, 4],
])

/**
 * decodePng reads one PNG into its pixels, or throws with the property it did
 * not understand. Every failure is a refusal rather than a default: a decoder
 * that assumed a bit depth would read a 16-bit capture as half-length rows and
 * then measure the noise of misalignment.
 */
export function decodePng(bytes: Uint8Array): PngImage {
  if (bytes.length < PNG_SIGNATURE.length) fail('the file is shorter than a PNG signature')
  for (let i = 0; i < PNG_SIGNATURE.length; i++) {
    if (bytes[i] !== PNG_SIGNATURE[i]) fail('the file does not start with the PNG signature')
  }
  let at = PNG_SIGNATURE.length
  let width = 0
  let height = 0
  let channels = 0
  let sawHeader = false
  const idat: Uint8Array[] = []
  while (at + 8 <= bytes.length) {
    const length = readUint32(bytes, at)
    const type = String.fromCharCode(
      bytes[at + 4] ?? 0,
      bytes[at + 5] ?? 0,
      bytes[at + 6] ?? 0,
      bytes[at + 7] ?? 0,
    )
    const dataAt = at + 8
    const end = dataAt + length
    if (end + 4 > bytes.length) fail(`the ${type} chunk is longer than the file`)
    if (type === 'IHDR') {
      width = readUint32(bytes, dataAt)
      height = readUint32(bytes, dataAt + 4)
      const depth = bytes[dataAt + 8]
      const colourType = bytes[dataAt + 9]
      const compression = bytes[dataAt + 10]
      const filter = bytes[dataAt + 11]
      const interlace = bytes[dataAt + 12]
      if (depth !== 8) fail(`its bit depth is ${String(depth)}, and only 8 is read`)
      const found = colourType === undefined ? undefined : CHANNELS.get(colourType)
      if (found === undefined) fail(`its colour type is ${String(colourType)}, and only RGB and RGBA are read`)
      if (compression !== 0 || filter !== 0) fail('it uses a compression or filter method this module does not read')
      if (interlace !== 0) fail('it is interlaced, which this module does not read')
      if (width === 0 || height === 0) fail('it declares a zero width or height')
      channels = found
      sawHeader = true
    } else if (type === 'IDAT') {
      idat.push(bytes.subarray(dataAt, end))
    } else if (type === 'IEND') {
      break
    }
    at = end + 4
  }
  if (!sawHeader) fail('it carries no IHDR chunk')
  if (idat.length === 0) fail('it carries no IDAT chunk')

  const raw = inflateSync(concat(idat))
  const stride = width * channels
  const expected = (stride + 1) * height
  if (raw.length < expected) {
    fail(`its pixel data is ${raw.length} bytes, and ${expected} are needed for ${String(width)}x${String(height)}`)
  }
  const pixels = new Uint8Array(width * height * 4)
  const previous = new Uint8Array(stride)
  const line = new Uint8Array(stride)
  for (let row = 0; row < height; row++) {
    const from = row * (stride + 1)
    const filter = raw[from]
    for (let i = 0; i < stride; i++) line[i] = raw[from + 1 + i] ?? 0
    unfilter(filter ?? 0, line, previous, channels)
    for (let column = 0; column < width; column++) {
      const source = column * channels
      const target = (row * width + column) * 4
      // Both colour types carry three colour channels; the fourth is the alpha
      // the RGB one does not have, which is opaque. Reading an RGB file's green
      // and blue out of its *red* byte -- one `source` for all three -- would
      // draw a grey picture of a coloured one, and the variance this module
      // measures would be of the wrong image.
      pixels[target] = line[source] ?? 0
      pixels[target + 1] = line[source + 1] ?? 0
      pixels[target + 2] = line[source + 2] ?? 0
      pixels[target + 3] = channels === 4 ? (line[source + 3] ?? 0) : 255
    }
    previous.set(line)
  }
  return { width, height, pixels }
}

function concat(parts: readonly Uint8Array[]): Uint8Array {
  let total = 0
  for (const part of parts) total += part.length
  const out = new Uint8Array(total)
  let at = 0
  for (const part of parts) {
    out.set(part, at)
    at += part.length
  }
  return out
}

/**
 * unfilter reverses one scanline's filter, in place, against the previous
 * scanline. The five filters are the format's whole set (`None`, `Sub`, `Up`,
 * `Average`, `Paeth`), and an unknown one is a refusal: a row left unfiltered
 * would be measured as noise rather than as an error.
 */
function unfilter(filter: number, line: Uint8Array, previous: Uint8Array, bpp: number): void {
  for (let i = 0; i < line.length; i++) {
    const raw = line[i] ?? 0
    const left = i >= bpp ? (line[i - bpp] ?? 0) : 0
    const up = previous[i] ?? 0
    const upLeft = i >= bpp ? (previous[i - bpp] ?? 0) : 0
    let value: number
    switch (filter) {
      case 0:
        value = raw
        break
      case 1:
        value = raw + left
        break
      case 2:
        value = raw + up
        break
      case 3:
        value = raw + ((left + up) >> 1)
        break
      case 4:
        value = raw + paeth(left, up, upLeft)
        break
      default:
        fail(`a scanline carries filter ${String(filter)}, and the format has five`)
    }
    line[i] = value & 0xff
  }
}

/** paeth is the format's predictor. */
function paeth(left: number, up: number, upLeft: number): number {
  const estimate = left + up - upLeft
  const dLeft = Math.abs(estimate - left)
  const dUp = Math.abs(estimate - up)
  const dUpLeft = Math.abs(estimate - upLeft)
  if (dLeft <= dUp && dLeft <= dUpLeft) return left
  if (dUp <= dUpLeft) return up
  return upLeft
}

/**
 * pixelStdDev is the standard deviation of the capture's pixel luminance, in
 * 0..1 -- the one number `assertNotBlank` reads.
 *
 * Luminance rather than a channel: a picture of a screen is mostly a ground and
 * ink in one hue, and a red channel that is flat while the blue channel carries
 * the text would be a picture. The sample is evenly spaced (`SAMPLED_PIXELS`),
 * so a capture is measured in the same way whatever its size, and a run over a
 * small image reads every pixel.
 */
export function pixelStdDev(image: PngImage): number {
  const count = image.width * image.height
  if (count === 0) fail('it has no pixels')
  const step = Math.max(1, Math.floor(count / SAMPLED_PIXELS))
  let n = 0
  let sum = 0
  let sumSquares = 0
  for (let pixel = 0; pixel < count; pixel += step) {
    const at = pixel * 4
    const r = image.pixels[at] ?? 0
    const g = image.pixels[at + 1] ?? 0
    const b = image.pixels[at + 2] ?? 0
    const luminance = (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255
    n += 1
    sum += luminance
    sumSquares += luminance * luminance
  }
  const mean = sum / n
  const variance = Math.max(0, sumSquares / n - mean * mean)
  return Math.sqrt(variance)
}

/** PIXEL_TOLERANCE is how far a capture's size may differ from the size this
 * run asked for: nothing, which is the point -- a window that came up at the
 * wrong size is a picture of the wrong thing. */
export const PIXEL_TOLERANCE = 0

/**
 * assertUsableCapture is the one gate every captured file passes before it is
 * written into the manifest: it is the size the run asked for, and it is not a
 * blank rectangle. It answers the image, so a caller can hash it afterwards
 * without decoding twice.
 */
export function assertUsableCapture(
  bytes: Uint8Array,
  expected: { readonly width: number; readonly height: number },
  file: string,
): PngImage {
  const image = decodePng(bytes)
  if (
    Math.abs(image.width - expected.width) > PIXEL_TOLERANCE ||
    Math.abs(image.height - expected.height) > PIXEL_TOLERANCE
  ) {
    throw new Error(
      `${file} is ${String(image.width)}x${String(image.height)} pixels, and this run asked for ${String(expected.width)}x${String(expected.height)}`,
    )
  }
  const spread = pixelStdDev(image)
  if (spread < BLANK_STDDEV_FLOOR) {
    throw new Error(
      `${file} is blank: the standard deviation of its pixels is ${spread.toFixed(4)}, under the floor of ${String(BLANK_STDDEV_FLOOR)}`,
    )
  }
  return image
}
