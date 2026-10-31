# Brand assets

`fugaro-logo.png` is the Fugaro logo: a blue wing, gear and cloud around a terminal
prompt, 2048x2048 RGBA on a transparent background. It is the **source of truth**, kept
as the original bytes; every other icon is derived from it and is never edited by hand.

The owner replaced the source with this 2048 px one on 2026-10-20 (it was 1024x1024
before, and the artwork on it was 548x371 px, so the 1024 px app icon was a 1.5x upscale
of it). The new canvas carries the same picture at twice the real pixels — the artwork's
alpha bounding box is now 1089x738 — so the app-icon master renders it *down*, and
`console/README.md` has the measured figures and what they mean for the packager.

## Regenerating the derived files

```sh
python3 -I scripts/gen-brand-icons.py   # needs Pillow
```

The script crops the transparent margin to the artwork, centres it with padding, and
writes (deterministically, for a given Pillow version):

| File | What it is |
|---|---|
| `website/public/favicon.ico`, `favicon-16x16.png`, `favicon-32x32.png` | browser favicons, transparent |
| `website/public/apple-touch-icon.png` | 180x180, opaque (iOS does not allow transparency) |
| `website/public/android-chrome-192x192.png`, `-512x512.png` | opaque, manifest purpose `any` |
| `website/public/android-chrome-maskable-512x512.png` | opaque, artwork inside the maskable safe zone |
| `website/public/logo.png` | the site header logo, 2x its 32 px display height |
| `website/public/site.webmanifest` | the web app manifest |
| `brand/app-icon-1024.png` | opaque master for the iOS app icon (the mobile app, docs/mobile) |
| `brand/app-icon-macos-1024.png` | transparent master for the macOS app icon, the **console's**: the same composition as `app-icon-1024.png` on Apple's rounded body (an 824 px squircle in a 100 px margin, with a soft shadow), because macOS does not round an app's icon itself. `console/build/icon.png` is a byte copy of this file, converted to the bundle's `.icns` by `build.mac.icon` at pack time |
| `brand/app-icon-android-foreground-1024.png` | transparent Android adaptive-icon foreground, artwork inside its safe zone |

The opaque background, and the manifest's theme and background colours, are the `--ground`
colour of the palette the website imports (`website/src/styles/tokens.css`), so re-run the
script after a palette change. Commit the outputs; `website/test/brand.mjs` checks them.

**The console's copy is not written by the script** (that runner has no Pillow, and the
packaged app's icon is a build input rather than a build output), so a regeneration has to
refresh it in the same commit — `cp brand/app-icon-macos-1024.png console/build/icon.png`, which
is the whole sync. `console/test/unit/icon.test.ts` fails on the bytes if it is forgotten,
and `console/README.md` has the paragraph.
