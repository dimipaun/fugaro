#!/usr/bin/env bash
#
# console-screenshots.sh -- the owner's one command for the website's console
# pictures (owner ruling 2026-10-20).
#
#   ./scripts/console-screenshots.sh
#
# **It needs a display, and it needs to be run on your Mac.** The console is an
# Electron application: this script builds it (`npm ci`, `npm run build`) and
# then launches the *built* app in a real window through the same
# Playwright/Electron path the e2e suite uses, resizes that window to
# 1280x800 and captures the Live screen, a run's detail, the Spend screen and
# the installation picker. There is no headless mode and none is wanted: the
# picture is of the application as a person sees it, and the window has to fit
# on the display (1280x800 of content, so a screen at least that tall).
#
# **What it takes pictures of is a fake.** Every read the console makes is
# answered by a fake `fugaro` the run installs in its own scratch tree, from the
# invented documents in `console/test/fixtures/screenshots/`. The run refuses to
# start unless the console's own resolver finds that fake (never a real
# installation), sets `HOME`, `XDG_CONFIG_HOME` and the window's
# `--user-data-dir` to fresh directories under one temporary tree, and passes
# the app an allow-list environment -- so it cannot read your `~/.config`, your
# console profile, or any credential. It never runs a real `fugaro`, and it
# makes no network request of its own.
#
# Where the pictures land: `console/screenshots/` -- git-ignored, never
# committed. The `MANIFEST.json` beside them lists each capture with its pixel
# size, its sha256 and a sentence of alt text; that manifest is what a later
# reader (or the website) needs, and it is safe to copy into `website/`.
#
# Nothing here writes to the repository except `console/screenshots/`, and
# nothing here talks to GitHub, the cloud, or the network.

set -euo pipefail

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
console_dir="${here}/../console"

# The console's toolchain is pinned to Node 22 (`console/package.json`'s
# `engines`), and the Electron build it runs is what the e2e suite runs. One
# line, naming both, rather than a stack trace from `npm ci` or a TypeScript
# error about a syntax node the older Node cannot parse.
if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
  echo "console-screenshots: node and npm are needed (Node 22, the version console/package.json pins), and one of them is not on PATH" >&2
  exit 1
fi

node_major=$(node -p 'process.versions.node.split(".")[0]')
if [ "${node_major}" != "22" ]; then
  echo "console-screenshots: console/package.json pins Node 22, and this shell has $(node -v); install Node 22 (nvm, mise, asdf or homebrew) and run this again" >&2
  exit 1
fi

if [ ! -d "${console_dir}" ]; then
  echo "console-screenshots: ${console_dir} is not there -- run this from a checkout of this repository" >&2
  exit 1
fi

cd "${console_dir}"

# The console's documented install (`console/README.md`), flags and all:
# `--ignore-scripts` is the policy this repository installs under, and Electron's
# runtime is fetched on the first `require('electron')` rather than by a
# lifecycle script, which the launch below does.
echo "console-screenshots: installing the console's pinned toolchain (npm ci)"
npm ci --ignore-scripts --no-audit --no-fund

echo "console-screenshots: building the console (npm run build)"
npm run build

# The run itself: compiles the tool and launches the built app
# (`tsconfig.screenshots.json` -> `.screenshots/`, an ignored build artifact).
# Its exit code is this script's: a blank capture, a window at the wrong size,
# or a read the fake did not prepare ends the run non-zero, and the pictures it
# did take are left where they are as evidence.
echo "console-screenshots: capturing (this opens a window)"
npm run screenshots
