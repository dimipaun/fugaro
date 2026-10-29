// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's icons, and the brand masters they are copies of (the owner's task
// of 2026-10-21; `brand/README.md`; D125).
//
// **Why this file exists.** The owner installed the app on both platforms and
// the launcher drew the platform's own default icon: `app.json` named no icon
// at all, so every build so far was a correct app wearing the wrong face.
// Nothing in the tree could have caught it. An icon path is a string nothing
// imports, no screen renders and no other test reads, and a build resolves it
// only at prebuild time on the owner's machine -- so a typo in it is either a
// build that stops there or, worse, an app that ships without an icon and
// looks like this task was never done. This file is the reading.
//
// **The brand assets are the source, and the app copies them.** `brand/README.md`
// is the rule: `brand/app-icon-1024.png` is the **opaque** master (the iOS
// icon, and the console's copy), `brand/app-icon-android-foreground-1024.png`
// the **transparent** Android adaptive-icon foreground, and both are written
// by `scripts/gen-brand-icons.py` from `brand/fugaro-logo.png` alone. The app
// does not re-derive, re-crop, re-export or touch them: `mobile/assets/` holds
// byte copies, and the test below compares the bytes -- so an icon "fixed" in
// this directory is red, and the way to change an icon is to re-run the script
// and re-copy. (The copy is also what keeps the palette honest: the masters'
// opaque background is the palette's `--ground`, so the icons follow a palette
// change only through the script, and this directory never holds a second
// opinion about the colour.)
//
// **What is checked, and what cannot be.** The dimensions, the colour type and
// the paths are read from the two files' own PNG headers and from `app.json`;
// **no pixel is decoded** -- this file has no image decoder, and it is written
// for a reader who cannot look at the artwork either. So "the icon is the
// logo" rests on the byte comparison with the master `scripts/gen-brand-icons.py`
// wrote, and not on anything here. Whether a launcher draws it -- and whether
// the splash looks right on a device -- is owner-run with the rest of Check 89
// (`README.md`, "What is not verified here").

import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { secretShaped } from './accounts/index.js';

const HERE = import.meta.dirname;
const APP = join(HERE, '..');
const REPO = join(APP, '..');

const CONFIG = JSON.parse(readFileSync(join(APP, 'app.json'), 'utf8'));
const MANIFEST = JSON.parse(readFileSync(join(APP, 'package.json'), 'utf8'));

/** The two paths `app.json` names, written once so each assertion names one. */
const ICON = './assets/icon.png';
const ADAPTIVE = './assets/adaptive-icon.png';

/** The masters under `brand/`, which `scripts/gen-brand-icons.py` writes. */
const BRAND_ICON = join(REPO, 'brand', 'app-icon-1024.png');
const BRAND_ADAPTIVE = join(REPO, 'brand', 'app-icon-android-foreground-1024.png');

/** A PNG's first eight bytes: the signature every PNG starts with. */
const SIGNATURE = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

/**
 * The header of a PNG buffer: its dimensions, its bit depth and its colour
 * type, read out of the `IHDR` chunk.
 *
 * A PNG is signature (8 bytes), then a chunk: length (4), type (4), data,
 * CRC (4). `IHDR` is always the first chunk, so its data starts at byte 16:
 * width (4), height (4), bit depth (1), colour type (1).
 *
 * This is deliberately a header reader and not an image decoder: the four
 * facts this file can check are all in it, and "the icon is square, 1024 and
 * opaque" is a statement about those four, not about the artwork.
 */
function pngHeader(buffer) {
  assert.equal(buffer.subarray(0, 8).equals(SIGNATURE), true, 'not a PNG: signature');
  assert.equal(buffer.subarray(12, 16).toString('latin1'), 'IHDR', 'the first chunk is IHDR');
  return {
    width: buffer.readUInt32BE(16),
    height: buffer.readUInt32BE(20),
    bitDepth: buffer.readUInt8(24),
    colorType: buffer.readUInt8(25),
  };
}

/** A PNG header, as `pngHeader` reads one, for the planted cases below. */
function header({ width, height, bitDepth = 8, colorType = 2 }) {
  const bytes = Buffer.alloc(26);
  SIGNATURE.copy(bytes, 0);
  bytes.writeUInt32BE(13, 8);
  bytes.write('IHDR', 12, 'latin1');
  bytes.writeUInt32BE(width, 16);
  bytes.writeUInt32BE(height, 20);
  bytes.writeUInt8(bitDepth, 24);
  bytes.writeUInt8(colorType, 25);
  return bytes;
}

/** The plugin entry for `name` in `app.json`, or `undefined`. */
function plugin(name) {
  const entry = (CONFIG.expo.plugins ?? []).find(
    (item) => item === name || (Array.isArray(item) && item[0] === name),
  );
  return Array.isArray(entry) ? entry[1] : entry === undefined ? undefined : {};
}

test('the header reader reads a PNG header, so the rest of this file can fail', () => {
  // The positive control. Without it, "1024x1024" would be a claim a reader
  // that returns `NaN` twice also satisfies: `NaN === 1024` is `false`, but so
  // is every other comparison, and the failure would be read as "the icon is
  // wrong" rather than as "the reader is wrong". Mutation: take the dimensions
  // from the wrong offsets in `pngHeader` and every case below goes red.
  assert.deepEqual(pngHeader(header({ width: 1024, height: 1024 })), {
    width: 1024,
    height: 1024,
    bitDepth: 8,
    colorType: 2,
  });
  // ... and a **non-square** planted case, so a reader that took one dimension
  // from the other's offset cannot pass: both masters are 1024x1024, and a
  // control made of one of them would be blind to exactly that mistake.
  assert.deepEqual(pngHeader(header({ width: 512, height: 180, colorType: 6 })), {
    width: 512,
    height: 180,
    bitDepth: 8,
    colorType: 6,
  });
  // ... and the signature is required, so a JPEG renamed `.png` -- the shape
  // this check is for -- is refused rather than read as a header.
  assert.throws(() => pngHeader(Buffer.alloc(26)), /not a PNG/);
});

test('both icon files exist, are PNGs, and are square 1024x1024', () => {
  for (const [what, path] of [
    ['the app icon', ICON],
    ['the adaptive-icon foreground', ADAPTIVE],
  ]) {
    const file = join(APP, path);
    assert.equal(existsSync(file), true, `${what} is at ${path}`);
    const { width, height, bitDepth } = pngHeader(readFileSync(file));
    assert.equal(width, 1024, `${what}: width`);
    assert.equal(height, 1024, `${what}: height`);
    // Square is a separate statement from 1024x1024: a 1024x1024 that lost its
    // squareness in a copy would still pass the two lines above if one of them
    // were ever relaxed, and the launcher's mask assumes it.
    assert.equal(width, height, `${what} is square`);
    assert.equal(bitDepth, 8, `${what}: 8 bits per channel`);
  }
});

test('the two files are byte copies of the brand masters, not re-exports', () => {
  // `brand/README.md`: the masters are the source of truth, every other icon
  // is derived from `brand/fugaro-logo.png` by `scripts/gen-brand-icons.py`,
  // and none of them is edited by hand. So the app's copy is the same bytes.
  // Mutation: re-encode `mobile/assets/icon.png` (or an icon "fixed" here by
  // hand) and this is red -- and the fix is the script, then the copy.
  assert.equal(readFileSync(join(APP, ICON)).equals(readFileSync(BRAND_ICON)), true,
    `mobile/assets/icon.png is a byte copy of brand/app-icon-1024.png ` +
      `(cp brand/app-icon-1024.png mobile/assets/icon.png)`);
  assert.equal(
    readFileSync(join(APP, ADAPTIVE)).equals(readFileSync(BRAND_ADAPTIVE)),
    true,
    `mobile/assets/adaptive-icon.png is a byte copy of ` +
      `brand/app-icon-android-foreground-1024.png`,
  );
});

test('the opaque master is the icon and the transparent one the adaptive foreground', () => {
  // The two masters are not interchangeable, and the difference is in the
  // header: colour type 2 is truecolour with **no** alpha channel, colour type
  // 6 is truecolour **with** one. iOS refuses an icon with transparency, so
  // the app's `icon` is the opaque master; Android's adaptive icon wants a
  // transparent foreground over a background colour, so that is the other.
  // Mutation: point the two keys at each other's file and one of these lines
  // goes red.
  const icon = pngHeader(readFileSync(join(APP, ICON)));
  const adaptive = pngHeader(readFileSync(join(APP, ADAPTIVE)));
  assert.equal(icon.colorType, 2, 'the app icon has no alpha channel (iOS refuses transparency)');
  assert.equal(adaptive.colorType, 6, 'the adaptive foreground is transparent');
});

test('app.json points at both files, and the paths resolve', () => {
  // What the launcher reads. Mutations: delete `icon` (the state the owner
  // found, and the platform default comes back), misspell a path, or point one
  // key at the other file -- each is red here and nowhere else.
  assert.equal(CONFIG.expo.icon, ICON, 'expo.icon is the iOS and Android icon');
  assert.equal(CONFIG.expo.android.adaptiveIcon.foregroundImage, ADAPTIVE);
  // ... and `ios.icon` is deliberately not set: it **overrides** the top-level
  // key, so a second path here would be a second file to keep in step with the
  // brand masters, and SDK 58 does not need one (the top-level `icon` is what
  // prebuild reads for iOS).
  assert.equal('icon' in (CONFIG.expo.ios ?? {}), false, 'one icon path, not two');
  // Every path named above is a file, and it is inside the app: an icon that
  // ships is a file that is committed next to the config that names it, and a
  // path that climbed out of `mobile/` would be a file this artifact does not
  // carry (`eas build` uploads the project directory).
  for (const path of [CONFIG.expo.icon, CONFIG.expo.android.adaptiveIcon.foregroundImage]) {
    assert.equal(path.startsWith('./'), true, `${path} is relative to mobile/`);
    assert.equal(existsSync(join(APP, path)), true, `${path} exists`);
  }
});

test('the adaptive icon and the splash agree on the brand ground colour', () => {
  // Android's adaptive icon draws the foreground over `backgroundColor`, and
  // the splash draws the app icon over the same ground -- the masters' opaque
  // background **is** the palette's `--ground` (`brand/README.md`), so this is
  // the colour the icons were baked against and not a second choice made here.
  // Mutation: change either literal and this is red; change it in only one
  // place and the square around the splash logo stops blending into it.
  const backgroundColor = CONFIG.expo.android.adaptiveIcon.backgroundColor;
  assert.match(backgroundColor, /^#[0-9A-F]{6}$/, 'a six-digit hex literal');
  // The splash is configured through the `expo-splash-screen` plugin -- SDK 58
  // has no top-level `splash` for native (it is `web.splash` there), and the
  // plugin is what prebuild reads. Mutation: drop the plugin entry and the
  // splash comes back as the platform's blank screen, which is the other half
  // of "the app shows the brand while it starts".
  const splash = plugin('expo-splash-screen');
  assert.notEqual(splash, undefined, 'expo-splash-screen is declared in expo.plugins');
  assert.equal(
    Object.hasOwn(MANIFEST.dependencies ?? {}, 'expo-splash-screen'),
    true,
    'a config plugin that is not installed is a prebuild that stops',
  );
  assert.equal(splash.image, ICON, 'the splash shows the same logo the launcher does');
  assert.equal(splash.backgroundColor, backgroundColor, 'one ground colour for both');
  assert.equal(splash.resizeMode, 'contain', 'the whole logo, never cropped');
});

test('the icon paths carry no identity and no credential (P3.1s fence, over the new strings)', () => {
  // The three tests in `app-config.test.js` walk the whole file, so this is
  // not the only place the rule is held -- it is the place that says the icon
  // keys are inside the fence rather than beside it, because they are the keys
  // a build machine resolves and the ones a hurried edit is most likely to
  // fill with a URL, a bucket or a project name. Mutations: write
  // `https://.../icon.png` or `gs://...` in either key and this is red.
  for (const path of [CONFIG.expo.icon, CONFIG.expo.android.adaptiveIcon.foregroundImage]) {
    assert.equal(secretShaped(path), null, `${path} is ${secretShaped(path)}`);
    assert.equal(path.includes('com.'), false, 'no reverse-DNS identity is committed here');
    assert.equal(/^\.\/assets\/[A-Za-z0-9._-]+\.png$/.test(path), true, `${path} is a local asset`);
  }
});
