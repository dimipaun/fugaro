# The current site, captured (2026-10-18)

This directory holds the "before" pictures of the redesign brief (`../spec.md`, process
step: "Playwright full-page screenshots of every page (desktop and mobile)") **once they are
regenerated: the images are not committed.** `docs/website/redesign/current/*.jpg` is in the
root `.gitignore`; the set taken on 2026-10-18 (22 JPEGs, 7.8 MiB) was removed from the
branch before merge. They are the input to the owner's graphic-design step and the baseline
the finished site is compared against.

**No agent has looked at any of these images.** The model that took them cannot view
an image, so nothing below is a description of what is in them: the file names, the
byte sizes and the measurements in `../reconciliation.md` §6 are what was read, and
visual comparison is left to the owner's design step.

## What is here

Every route of the site — `/`, `/how-it-works/`, `/getting-started/`,
`/caps-and-costs/`, `/console/`, `/docs/`, `/security/`, `/changelog/`, `/faq/`,
`/community/`, `/privacy/` — at two viewports:

| viewport | size | device scale factor |
| --- | --- | --- |
| `desktop` | 1280 × 900 | 1 |
| `mobile` | 390 × 844 | 1 |

Both are captured with `prefers-reduced-motion: reduce`, so what is in the picture is
the page as the site's own reduced-motion rules leave it. A still of an animation is
a lie about the page, and this set is not a recording.

## The three pages that do not fit in one image

| page | height at 1280 | height at 390 |
| --- | --- | --- |
| `/docs/` | 79,707 px | 134,171 px |
| `/changelog/` | 17,076 px | 32,122 px |
| `/security/` | 15,314 px | 30,500 px |

Chromium cannot capture a surface that tall: a `fullPage` screenshot of `/docs/`
hangs until it times out, at both viewports, and that hang is itself a finding about
those pages. They are captured as their **top 8000 px only**, and each file name
carries the fraction — `docs-desktop-part01of10.jpg` is the first of ten bands, not
the page. `/docs/` at 390 px would need seventeen.

## Format

`*-fullpage.jpg` is a whole page, JPEG quality 78. `*-part01ofNN.jpg` is a band,
JPEG quality 62. The set is about 8 MB; a lossless set of the same pages was 36 MB, most
of it in those three tall pages.

## Reproducing it

From the repository root, on the commit whose site you want to capture (Playwright is
installed out of tree, so `website/package.json` never changes):

```sh
(cd website && npm ci && npm run build)
npm install --prefix /tmp/fugaro-pw playwright@1.49.1
/tmp/fugaro-pw/node_modules/.bin/playwright install chromium
PLAYWRIGHT_PREFIX=/tmp/fugaro-pw node docs/website/redesign/capture-screenshots.mjs docs/website/redesign/current
```

The script serves `website/dist` on 127.0.0.1:8099, writes the 22 images here and the
measurements to `/tmp/website-screenshots.json` (its second argument changes that path).
The redesign's **"after"** set is the same command with `docs/website/redesign/after` as
the argument — the directory is an argument precisely so that the second run cannot
overwrite this baseline, and `after/*.jpg` is ignored too. The script's header records how
the 2026-10-18 set was taken in a container with no Node and no root.
