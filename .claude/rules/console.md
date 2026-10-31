---
paths:
  - "console/**"
---

# Desktop console (Electron)

- Use exactly the Node version in `package.json`'s `engines.node` (22.x). `.npmrc` sets `engine-strict=true`.
- Install with `npm ci --ignore-scripts --no-audit --no-fund --legacy-peer-deps=false`. A global `~/.npmrc` can otherwise skip the vite peer.
- Pin a new dependency to an exact version (`save-exact`). `test/unit/pinning.test.ts` enforces this.
- Run `npm run typecheck` and `npm run test:unit -- --maxWorkers=2 --testTimeout=60000`. On a loaded machine, the default timeouts flake.
- The spawn allow-list (`src/main/spawn.ts`) and the IPC allow-list (`CONSOLE_OPERATIONS` in `src/main/ipc.ts`) list exact argv, not command names. A new command, flag or argument is security-sensitive. It needs:
  - an independent review,
  - its pinning tests in `test/unit/`,
  - a matching update to the §5.1 table in `docs/design/desktop-console.md`.
- Treat everything that comes from the CLI or a run as untrusted text. Put it through a class in `src/shared/sanitize.ts` (`TEXT_CLASSES`) before it reaches the renderer.
- A view that shows spend, caps or the kill controls must ask `hidesView` (`src/shared/version.ts`, `REDUCED_VIEWS`). In the reduced mode it then withholds its figures instead of misreading a CLI shape it does not know.
- A child gets exactly `CHILD_ENV_KEYS` (11 keys) and nothing merged from `process.env`. macOS adds `__CF_USER_TEXT_ENCODING` to every process, and the tests exempt that key and nothing else.
- WSL route (`docs/console-windows/`):
  - refuse any word with shell metacharacters (`distroShellUnsafe`); never escape it,
  - a distro name must match `DISTRO_NAME_PATTERN`,
  - the env is the same 11 keys plus the console's own empty `WSLENV` and `WSL_UTF8=1` (`WSL_UTF8=1`
    goes on every `wsl.exe` call, the CLI's plan as well as the queries),
  - the wrapper's stderr marker carries a fresh random nonce per spawn (`WslWrapper.marker`): match
    that marker, never the fixed prefix, and remove U+0000 and a leading U+FEFF before reading —
    `wsl.exe` prints its own warnings in UTF-16LE on the builds that ignore `WSL_UTF8`.
- **A screen renders each part when its data arrives; independent reads run in parallel; no screen waits for all of them** (the owner's rule of 2026-10-31, and what a new tab is reviewed against):
  1. Every tab is built from independent **sections**, each with its own read, its own busy state and its own refusal — and a refusal names its own command.
  2. Independent reads start **in parallel**, within the spawn and IPC allow-lists above and whatever limit `src/main` puts on children.
  3. A section paints **the moment its data arrives**: never an all-or-nothing redraw of the whole tab, and never one spinner held until the last read lands.
  4. Leaving a tab, or changing a control, never leaves another view's content: the **sequence token covers each section**, and the latest change's view is the one that stays, whatever order the answers arrive in.
  5. Jobs are **never part of the first paint**: `runs ls --json` is a section of its own, fetched after the primary content and only when it is visible or expanded, always with `--since 24h`, drawing at most the newest N rows client-side with a "Load more" (this tree has no `runs ls --limit`, so that cut is the console's own).
- The renderer half of 1–4 is `src/renderer/section.ts` (`sectionRead`, `renderBusy`, `renderStamp`, `resultCache`), and a tab that grows a second read adopts it rather than writing a busy mark of its own. A view served from the session cache is stamped `as of HH:MM` with a refresh control, and one older than `CACHE_FRESH_MS` (five minutes) is drawn **and** re-read at once — never served silently.
- A control change is answered **in the frame it happened in**: a busy mark and `aria-busy` on the section, the figures already drawn kept under it (dimmed, never blanked), and a label naming what is being read.
- CI skips the screenshot goldens (`FUGARO_CONSOLE_GOLDENS: skip`). Never re-record them without the owner. On the owner's Mac that is `npx playwright test <spec> --update-snapshots`.
- Go docs tests pin `docs/design/desktop-console.md` and `console/docs/json-contract.md` (`internal/cli/docs_desktop_console_test.go`, `docs_console_boundary_test.go`). After changing either, run `go test ./internal/cli/ -run Docs`.
- No Go file outside the boundary test's allow-list may import or name `console/` paths (`docs_console_boundary_test.go`).
- The console releases on its own tag, `console-vX.Y.Z`. Its notes go in `docs/releases/console/`.
- From 0.113.0 `console/` is under the business licence in `console/LICENSE.md` (counsel's text of 2026-10-27) (`LICENSING.md`, D311), the renderer included; releases up to 0.112.0 stay MIT. `package.json`'s `license` points at it and `build.files` packs it beside `THIRD_PARTY_NOTICES.md`. Never edit the licence text itself.
