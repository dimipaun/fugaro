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
  - the env is the same 11 keys plus an empty `WSLENV`.
- CI skips the screenshot goldens (`FUGARO_CONSOLE_GOLDENS: skip`). Never re-record them without the owner. On the owner's Mac that is `npx playwright test <spec> --update-snapshots`.
- Go docs tests pin `docs/design/desktop-console.md` and `console/docs/json-contract.md` (`internal/cli/docs_desktop_console_test.go`, `docs_console_boundary_test.go`). After changing either, run `go test ./internal/cli/ -run Docs`.
- No Go file outside the boundary test's allow-list may import or name `console/` paths (`docs_console_boundary_test.go`).
- The console releases on its own tag, `console-vX.Y.Z`. Its notes go in `docs/releases/console/`.
- From 0.113.0 `console/` is under the business licence in `console/LICENSE.md` (counsel's text of 2026-10-27) (`LICENSING.md`, D311), the renderer included; releases up to 0.112.0 stay MIT. `package.json`'s `license` points at it and `build.files` packs it beside `THIRD_PARTY_NOTICES.md`. Never edit the licence text itself.
