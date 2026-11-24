#!/usr/bin/env bash
#
# console-screenshots.sh -- the owner's one command for the website's console
# pictures (owner ruling 2026-10-20).
#
#   ./scripts/console-screenshots.sh
#
# **It needs a display, and it needs to be run on your Mac.** The console is an
# Electron application: this script installs its pinned toolchain (`npm ci`) and
# then runs the console's own `screenshots` script, which builds the console and
# launches the *built* app in a real window through the same
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
# outside that tree. The console it launches makes no network request of its own.
#
# Where the pictures land: `console/screenshots/` -- git-ignored, never
# committed. The `MANIFEST.json` beside them lists each capture with its pixel
# size, its sha256 and a sentence of alt text; that manifest is what a later
# reader (or the website) needs, and it is safe to copy into `website/`.
#
# Where it writes, and what it reaches. Neither is nothing: `npm ci` writes
# `console/node_modules/` and reads the npm registry, the console's build writes
# `console/dist/`, `console/.screenshots/` and `console/.bundle-renderer/`, and
# the launch fetches Electron's runtime on the first `require('electron')`.
# Every one of those paths is git-ignored. What it does *not* do is talk to
# GitHub or to any cloud API, read a credential, or ask the network for anything
# on the run's own behalf.

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

# The console's toolchain is pinned to a **bounded range** (`console/package.json`'s
# `engines.node` is `>=22.23.3 <23`, and `console/.npmrc` sets
# `engine-strict=true`), and the Electron build it runs is what the e2e suite
# runs. The range is read from that file rather than written here, and the
# question asked of this shell's Node is the one `npm ci` asks -- is it inside
# that range -- rather than "is it this string": the guard exists to turn the
# EBADENGINE failure into one line, and a check that only compared majors would
# let Node 22.11 through to exactly that failure, with a message that said the
# version was fine.
wanted=$(node -p -e 'require(process.argv[1]).engines.node' "${console_dir}/package.json" 2>/dev/null || true)
if [ -z "${wanted}" ]; then
  echo "console-screenshots: ${console_dir}/package.json does not name an engines.node, so this script cannot check the version it needs" >&2
  exit 1
fi

# The floor-and-ceiling question, in node so that it reads `engines.node` the way
# npm reads it: `yes` inside the range, `no` outside it, and nothing at all when
# the field is not the shape this guard knows (an open range, a caret, a tag) --
# which is the third answer below and not a silent pass.
holds=$(node -e '
const m = /^>=(\d+)\.(\d+)\.(\d+) <(\d+)$/.exec(process.argv[1])
if (m === null) process.exit(0)
const floor = m.slice(1, 4).map(Number)
const ceiling = Number(m[4])
const [major, minor, patch] = process.versions.node.split(".").map(Number)
const above = (major - floor[0] || minor - floor[1] || patch - floor[2]) >= 0
process.stdout.write(above && major < ceiling ? "yes" : "no")
' "${wanted}" 2>/dev/null || true)

case "${holds}" in
yes) ;;
no)
  echo "console-screenshots: console/package.json requires Node ${wanted} (engines.node, with engine-strict in console/.npmrc), and this shell has $(node -v); install a Node in that range (nvm, mise, asdf or homebrew) and run this again" >&2
  exit 1
  ;;
*)
  echo "console-screenshots: console/package.json's engines.node is ${wanted}, which this script cannot read as a floor and a ceiling, so it cannot check the version it needs" >&2
  exit 1
  ;;
esac

cd "${console_dir}"

# The console's documented install (`console/README.md`), flags and all:
# `--ignore-scripts` is the policy this repository installs under, and Electron's
# runtime is fetched on the first `require('electron')` rather than by a
# lifecycle script, which the launch below does.
echo "console-screenshots: installing the console's pinned toolchain (npm ci)"
npm ci --ignore-scripts --no-audit --no-fund

# The run itself: builds the console, compiles the tool and launches the built
# app (`tsconfig.screenshots.json` -> `.screenshots/`, an ignored build
# artifact). The build is the script's own rather than this one's, so that
# `npm run screenshots` from `console/` -- the second command `console/README.md`
# documents -- does the same work on a checkout where `dist/` has never been
# built. Its exit code is this script's: a blank capture, a window at the wrong
# size, or a read the fake did not prepare ends the run non-zero, and the
# pictures it did take are left where they are as evidence.
echo "console-screenshots: capturing (this opens a window)"
npm run screenshots
