#!/usr/bin/env python3
"""docs/website/redesign/sample-logo-hue.py -- the logo's hue, as numbers.

Reproduces reconciliation.md section 5.1. Run from the repository root:

    python3 -I docs/website/redesign/sample-logo-hue.py brand/fugaro-logo.png

Needs Pillow only, like scripts/gen-brand-icons.py. Prints numbers and nothing else:
pixels with alpha >= 250 and HSV value >= 0.05 are kept; for each saturation
threshold it prints the pixel count, the circular mean hue (unweighted, and weighted
by saturation) and the mean saturation; then the six largest 5-degree hue bins over
the pixels with saturation >= 0.7.
"""
import colorsys
import math
import sys

from PIL import Image

THRESHOLDS = (0.5, 0.6, 0.7, 0.8)


def main(path):
    image = Image.open(path).convert("RGBA")
    print(f"{path}: {image.size[0]} x {image.size[1]} RGBA")
    kept = []
    raw = image.tobytes()
    for i in range(0, len(raw), 4):
        r, g, b, a = raw[i], raw[i + 1], raw[i + 2], raw[i + 3]
        if a < 250:
            continue
        h, s, v = colorsys.rgb_to_hsv(r / 255, g / 255, b / 255)
        if v < 0.05:
            continue
        kept.append((h * 360.0, s))
    print(f"alpha >= 250 and value >= 0.05: {len(kept)} pixels")

    def circular_mean(rows, weighted):
        x = sum((s if weighted else 1.0) * math.cos(math.radians(h)) for h, s in rows)
        y = sum((s if weighted else 1.0) * math.sin(math.radians(h)) for h, s in rows)
        return math.degrees(math.atan2(y, x)) % 360.0

    print("saturation>=  pixels  mean-hue  weighted-hue  mean-saturation")
    for t in THRESHOLDS:
        rows = [(h, s) for h, s in kept if s >= t]
        mean_s = sum(s for _, s in rows) / len(rows)
        print(f"{t:<13} {len(rows):<7} {circular_mean(rows, False):<9.1f} "
              f"{circular_mean(rows, True):<13.1f} {mean_s:.3f}")

    rows = [h for h, s in kept if s >= 0.7]
    bins = [0] * 72
    for h in rows:
        bins[int(h // 5) % 72] += 1
    print("5-degree bins, saturation >= 0.7:")
    for i in sorted(range(72), key=lambda i: -bins[i])[:6]:
        print(f"  {i * 5}-{i * 5 + 4}: {100 * bins[i] / len(rows):.2f} %")


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "brand/fugaro-logo.png")
