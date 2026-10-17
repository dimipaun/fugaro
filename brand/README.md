# Brand assets

`fugaro-logo.png` is the Fugaro logo: a blue wing, gear and cloud around a terminal
prompt, 1024x1024 RGBA on a transparent background. It is the **source of truth**, kept
as the original bytes; every other icon is derived from it and is never edited by hand.

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
| `brand/app-icon-1024.png` | opaque master for the iOS app icon (future mobile app, docs/mobile) |
| `brand/app-icon-android-foreground-1024.png` | transparent Android adaptive-icon foreground, artwork inside its safe zone |

The opaque background, and the manifest's theme and background colours, are the `--ground`
colour of the palette the website imports (`website/src/styles/tokens.css`), so re-run the
script after a palette change. Commit the outputs; `website/test/brand.mjs` checks them.
