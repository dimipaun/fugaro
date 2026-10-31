#!/usr/bin/env python3
"""Regenerate every derived brand file from brand/fugaro-logo.png.

Usage (from anywhere; paths are resolved from this file's location):

    python3 -I scripts/gen-brand-icons.py

Needs Pillow. The output is deterministic for a given Pillow version: no
timestamps, no text chunks, fixed resampling, fixed compression. Commit what it
writes; nothing reads this script at build time.

What it writes, all from the one source logo:

  website/public/favicon.ico               16, 32, 48 px, transparent
  website/public/favicon-16x16.png         transparent
  website/public/favicon-32x32.png         transparent
  website/public/apple-touch-icon.png      180 px, opaque (iOS ignores alpha)
  website/public/android-chrome-192x192.png  opaque, purpose "any"
  website/public/android-chrome-512x512.png  opaque, purpose "any"
  website/public/android-chrome-maskable-512x512.png  opaque, art inside
                                           the maskable safe zone
  website/public/logo.png                  the header logo, 2x its display size
  website/public/site.webmanifest          the web app manifest
  brand/app-icon-1024.png                  opaque master for the iOS app icon
  brand/app-icon-macos-1024.png            transparent master for the macOS app
                                           icon (the console's): Apple's squircle
                                           body inside a margin, with its shadow
  brand/app-icon-android-foreground-1024.png  transparent adaptive-icon
                                           foreground, art inside its safe zone

The opaque background is the `--ground` colour of the palette the site
imports (website/src/styles/tokens.css names the palette file), so the icons
and the manifest follow a palette change when this script is re-run.
"""

import json
import math
import re
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "brand" / "fugaro-logo.png"
PUBLIC = ROOT / "website" / "public"
BRAND = ROOT / "brand"
STYLES = ROOT / "website" / "src" / "styles"

# Pixels fainter than this alpha are treated as empty when finding the artwork's
# bounding box, so a stray near-transparent fringe does not widen the crop.
ALPHA_THRESHOLD = 8

# The header logo's display height in CSS pixels; the file is 2x that.
LOGO_DISPLAY_HEIGHT = 32


def ground_colour():
    """The `--ground` hex literal of the palette tokens.css imports."""
    tokens = (STYLES / "tokens.css").read_text(encoding="utf-8")
    m = re.search(r'@import\s+"\./(palette-[a-z-]+\.css)"', tokens)
    if not m:
        sys.exit("tokens.css imports no palette file")
    palette = (STYLES / m.group(1)).read_text(encoding="utf-8")
    m = re.search(r"--ground:\s*(#[0-9A-Fa-f]{6})\s*;", palette)
    if not m:
        sys.exit("the palette in force has no --ground hex literal")
    return m.group(1).upper()


def artwork(src):
    """The logo cropped to its visible artwork."""
    alpha = src.getchannel("A").point(lambda a: 255 if a > ALPHA_THRESHOLD else 0)
    box = alpha.getbbox()
    if box is None:
        sys.exit(f"{SOURCE} is fully transparent")
    return src.crop(box)


def fit(art, size, extent, background=None):
    """A size x size canvas with the artwork centred.

    extent is the fraction of the canvas the artwork's longer side spans.
    """
    scale = extent * size / max(art.size)
    return _place(art, size, scale, background)


def fit_diagonal(art, size, diameter):
    """Scale so the artwork's diagonal fits a centred circle of `diameter`
    (a fraction of the canvas): the safe zone of a maskable or adaptive icon
    is a circle, and a mask may cut anything outside it."""
    w, h = art.size
    scale = diameter * size / (w * w + h * h) ** 0.5
    return scale


def _place(art, size, scale, background):
    w = max(1, round(art.size[0] * scale))
    h = max(1, round(art.size[1] * scale))
    scaled = art.resize((w, h), Image.Resampling.LANCZOS)
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    canvas.alpha_composite(scaled, ((size - w) // 2, (size - h) // 2))
    if background is None:
        return canvas
    rgb = tuple(int(background[i : i + 2], 16) for i in (1, 3, 5))
    opaque = Image.new("RGBA", (size, size), rgb + (255,))
    opaque.alpha_composite(canvas)
    return opaque.convert("RGB")


# The macOS app-icon grid (Apple's template since Big Sur): a 1024 canvas whose
# body is an 824 px squircle centred in a 100 px transparent margin, which holds
# the drop shadow. macOS does not mask an app's icon, so an opaque full-bleed
# square shows as a square in the Dock beside every rounded system icon.
MACOS_CANVAS = 1024
MACOS_BODY = 824
# The body's outline is the superellipse |x|^n + |y|^n = 1. Apple's continuous
# corner is not published as a formula; n = 5.1 is the least-squares fit to the
# outline of the system apps' icons (Notes, Calculator) on macOS 26, within
# 2.4 px RMS and indistinguishable at Dock sizes.
MACOS_SQUIRCLE_EXPONENT = 5.1
# The shadow, measured off the same system icons: black, offset down, Gaussian.
MACOS_SHADOW_OFFSET = 10
MACOS_SHADOW_SIGMA = 16
MACOS_SHADOW_OPACITY = 0.20
# The body outline is drawn at this multiple and reduced, for an anti-aliased edge.
SUPERSAMPLE = 4


def squircle_mask(size, exponent):
    """An anti-aliased L mask of a centred superellipse filling size x size."""
    big = size * SUPERSAMPLE
    half = big / 2
    steps = 4096
    points = []
    for i in range(steps):
        t = 2 * math.pi * i / steps
        c, s = math.cos(t), math.sin(t)
        x = math.copysign(abs(c) ** (2 / exponent), c)
        y = math.copysign(abs(s) ** (2 / exponent), s)
        points.append((half + x * half, half + y * half))
    mask = Image.new("L", (big, big), 0)
    ImageDraw.Draw(mask).polygon(points, fill=255)
    return mask.resize((size, size), Image.Resampling.BOX)


def macos_icon(art, ground):
    """The macOS app icon: the opaque composition of fit(art, ..., 0.80, ground)
    drawn on the squircle body, over a soft shadow, on a transparent canvas."""
    margin = (MACOS_CANVAS - MACOS_BODY) // 2
    body = fit(art, MACOS_BODY, 0.80, ground).convert("RGBA")
    mask = squircle_mask(MACOS_BODY, MACOS_SQUIRCLE_EXPONENT)
    body.putalpha(mask)
    shadow_alpha = Image.new("L", (MACOS_CANVAS, MACOS_CANVAS), 0)
    shadow_alpha.paste(mask, (margin, margin + MACOS_SHADOW_OFFSET))
    shadow_alpha = shadow_alpha.filter(ImageFilter.GaussianBlur(MACOS_SHADOW_SIGMA))
    shadow_alpha = shadow_alpha.point(lambda a: round(a * MACOS_SHADOW_OPACITY))
    canvas = Image.new("RGBA", (MACOS_CANVAS, MACOS_CANVAS), (0, 0, 0, 0))
    canvas.putalpha(shadow_alpha)
    canvas.alpha_composite(body, (margin, margin))
    return canvas


def save_png(img, path):
    path.parent.mkdir(parents=True, exist_ok=True)
    # No pnginfo, no timestamps: the bytes depend only on the pixels.
    img.save(path, format="PNG", optimize=True)
    print(f"wrote {path.relative_to(ROOT)} ({path.stat().st_size} bytes)")


def main():
    src = Image.open(SOURCE)
    src.load()
    if src.mode != "RGBA":
        src = src.convert("RGBA")
    art = artwork(src)
    ground = ground_colour()

    # Favicons: transparent, the artwork spanning almost the whole square so it
    # stays legible at 16 px.
    for px in (16, 32):
        save_png(fit(art, px, 0.96), PUBLIC / f"favicon-{px}x{px}.png")
    ico = fit(art, 48, 0.96)
    ico_path = PUBLIC / "favicon.ico"
    ico.save(ico_path, format="ICO", sizes=[(16, 16), (32, 32), (48, 48)])
    print(f"wrote {ico_path.relative_to(ROOT)} ({ico_path.stat().st_size} bytes)")

    # Home-screen icons: opaque on the site's ground colour, with a margin.
    save_png(fit(art, 180, 0.80, ground), PUBLIC / "apple-touch-icon.png")
    for px in (192, 512):
        save_png(fit(art, px, 0.80, ground), PUBLIC / f"android-chrome-{px}x{px}.png")
    # Maskable: the safe zone is the centred circle of 80% diameter; keep the
    # artwork's diagonal inside 76% so no mask shape clips it.
    save_png(
        _place(art, 512, fit_diagonal(art, 512, 0.76), ground),
        PUBLIC / "android-chrome-maskable-512x512.png",
    )

    # The header logo: cropped to the artwork, 2x its display height.
    # The width is rounded to an even number so the display width is a whole pixel.
    h = LOGO_DISPLAY_HEIGHT * 2
    w = 2 * round(art.size[0] * LOGO_DISPLAY_HEIGHT / art.size[1])
    save_png(art.resize((w, h), Image.Resampling.LANCZOS), PUBLIC / "logo.png")

    # App-icon masters for the future mobile app (docs/mobile).
    save_png(fit(art, 1024, 0.80, ground), BRAND / "app-icon-1024.png")
    # The console's icon (console/build/icon.png is a byte copy of it).
    save_png(macos_icon(art, ground), BRAND / "app-icon-macos-1024.png")
    # Android adaptive icon foreground: 108dp canvas, 66dp safe circle.
    save_png(
        _place(art, 1024, fit_diagonal(art, 1024, 0.58), None),
        BRAND / "app-icon-android-foreground-1024.png",
    )

    manifest = {
        "name": "Fugaro",
        "short_name": "Fugaro",
        "start_url": "/",
        "display": "standalone",
        "theme_color": ground,
        "background_color": ground,
        "icons": [
            {"src": "android-chrome-192x192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
            {"src": "android-chrome-512x512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
            {"src": "android-chrome-maskable-512x512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
        ],
    }
    out = PUBLIC / "site.webmanifest"
    out.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(f"wrote {out.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
