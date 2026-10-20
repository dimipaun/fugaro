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
# Playwright/Electron path the e2e suite uses, resizes that window to 1280x800
# and captures the Live screen, a run's detail and the Spend screen -- then
# opens the installation picker from the console's own `Installations...` menu
# item and captures that too. One launch, four pictures. There is no headless
# mode and none is wanted: the
# picture is of the application as a person sees it, and the window has to fit
# on the display (1280x800 of content, so a screen at least that tall).
#
# **What it takes pictures of is a fake.** Every read the console makes is
# answered by a fake `fugaro` the run installs in its own scratch tree, from the
# invented documents in `console/test/fixtures/screenshots/`. The run seeds the
# console's own store with an installation that names that fake as its chosen
# binary, and -- before the window exists -- reads that store back with the
# console's own reader and resolves the chosen path with the console's own
# one-path resolution, refusing the launch if it is not the fake. So the
# handshake reads one path: it never walks `/opt/homebrew/bin`, `/usr/local/bin`,
# `~/.local/bin` and `/usr/bin`, where *your* `fugaro` lives, and the only
# `fugaro` this run ever executes is the fake.
#
# `HOME`, `XDG_CONFIG_HOME`, `TMPDIR` and the window's `--user-data-dir` are
# fresh directories under one temporary tree (removed when the run finishes),
# and the app is passed an allow-list environment -- so it cannot read your
# `~/.config`, your console profile, or any credential, and it writes nothing
# outside that tree. It makes no network request of its own.
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

if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
  echo "console-screenshots: node and npm are needed (Node as console/package.json's engines pins it), and one of them is not on PATH" >&2
  exit 1
fi

if [ ! -d "${console_dir}" ]; then
  echo "console-screenshots: ${console_dir} is not there -- run this from a checkout of this repository" >&2
  exit 1
fi

# The console's toolchain is pinned **exactly** (`console/package.json`'s
# `engines.node` is one version, and `console/.npmrc` sets
# `engine-strict=true`), and the Electron build it runs is what the e2e suite
# runs. The version is read from that file rather than written here, and it is
# compared exactly rather than by major: the guard exists to turn the `npm ci`
# EBADENGINE failure into one line, and a major-only check would let Node 22.11
# through to exactly that failure -- with a message that said the version was
# fine.
wanted=$(node -p -e 'require(process.argv[1]).engines.node' "${console_dir}/package.json" 2>/dev/null || true)
if [ -z "${wanted}" ]; then
  echo "console-screenshots: ${console_dir}/package.json does not name an engines.node, so this script cannot check the version it needs" >&2
  exit 1
fi

current=$(node -p 'process.versions.node')
if [ "${current}" != "${wanted}" ]; then
  echo "console-screenshots: console/package.json pins Node ${wanted} (engines.node, with engine-strict in console/.npmrc), and this shell has $(node -v); install Node ${wanted} (nvm, mise, asdf or homebrew) and run this again" >&2
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
