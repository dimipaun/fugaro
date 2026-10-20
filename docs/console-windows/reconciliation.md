# The console on Windows — the reconciliation

**Status: reconciliation only. No code, no config, no workflow and no live action follows from this
document, and no decision in §4 is answered.** It reads the tree at `ccc406b2` and every path and
symbol in it was read at that commit. Where it disagrees with the brief in §0, **this document
wins** and says so.

**Owner:** Dimi Paun. **Date:** 2026-10-20. **Base:** `main` at `ccc406b2`.

The reconciliation's citations into `docs/design/` name the document, the section and the phrase the
citation turns on rather than a line number, for the reason
[docs/coordinator/README.md](../coordinator/README.md) gives: those trees gain sections and rows
while this work sits in review, and a line number into one of them moves under the sentence it was
written for without anything looking wrong. Citations into `console/src/` **do** carry a line number,
because that tree is this work's subject and a reader checking one is checking the code.

---

## 0. The brief, verbatim, as the source

> **Task: plan: Windows build of the Console that drives the Fugaro CLI inside WSL (0.10.0)**
>
> Owner ruling (2026-10-20): 0.10.0 gets a Windows build of the Console in which the Fugaro CLI is
> the LINUX binary running inside WSL (the owner's Windows-native CLI is a separate track,
> docs/windows-cli/). Docs only under docs/console-windows/ (README, reconciliation.md against the
> console code and docs/design/console-styling.md and docs/windows-cli/, implementation-plan.md).
> Cover: (1) the CLI resolver running `wsl.exe -d <distro> -- fugaro ...` instead of a host path,
> with a distro picker/default, WSL absent/no distro/no fugaro-in-distro first-run pages (the
> existing no-binary page pattern); (2) path translation (wslpath) for remembered checkouts and
> anything the console shows or opens, and where the console's own store lives; (3) credentials and
> the OS keychain boundary: nothing secret crosses wsl.exe argv or env (stdin only), the console
> never reads a token; (4) process spawning, streaming (watch/logs), cancellation and exit codes
> through wsl.exe; (5) the unsigned Windows installer job (NSIS or the existing builder target),
> staged under docs/pending-workflows/ for the owner (agents never write .github/workflows),
> SmartScreen note in the docs; (6) the test strategy without a Windows machine (resolver unit tests
> with a fake wsl.exe, a windows-latest advisory CI job) and an owner manual checklist; (7) parts
> ordered to merge alone. Decisions the owner must make: numbered, recommended option first. Keep
> the spec of this task as the reconciliation's source; do not touch code.

The brief is the owner's own text and is not paraphrased here. What follows reconciles it against
the tree, and **six of its own sentences are corrections to what the tree says today** rather than
questions about it.

### Corrections to the brief (C1–C6)

| # | The brief's sentence | What the tree says | Consequence |
|---|---|---|---|
| **C1** | *"the Fugaro CLI is the LINUX binary running inside WSL"* | There is **no Linux binary *of this repository* inside a distribution** unless the operator put one there. `docs/windows-cli/wsl.md` is the install procedure (download the linux archive, `cosign verify-blob`, `sha256sum -c`, `tar -xzf`, `~/.local/bin`) and nothing in this repository installs it for them | The console must **detect** `fugaro` inside a distro, never assume it, and the "no `fugaro` in the distro" page is a first-class state (F31, F32) rather than an error |
| **C2** | *"a distro picker/default"* | There is a **binary** picker (`console/src/main/picker.ts`, `PICKER_FILE_FILTERS`, a native `openFile` dialog) and no distro concept anywhere: `grep -rni wsl console/src` returns **0** | A distro picker is new surface, and it is a *list* rather than a file dialog — the only source is `wsl.exe --list` (F10, F11, F27) |
| **C3** | *"the unsigned Windows installer job (NSIS or the existing builder target)"* | **There is no existing builder target.** `console/package.json`'s `build` block has `mac` and `linux` and **no `win`**, `scripts` has no `package:win`, `build.electronDownload.checksums` pins three zips and no `win32-x64` one, and `fuseTarget` (`console/src/main/fuses.ts:379`) **throws** for `win32` because `SUPPORTED_PLATFORMS` (`fuses.ts:298`) is `['darwin','linux']` | The installer job cannot be *run* until three edits land in `console/` (F35, F36, F37). It is staged anyway, as the brief asks, and its header says in the first paragraph what must land before the copy-in is safe |
| **C4** | *"the CLI resolver runs `wsl.exe -d <distro> -- fugaro …`"* | That argv cannot be built today: `assertArgvFlags` (`console/src/main/spawn.ts:683`) refuses any `-`-prefixed element outside `SPAWN_FLAGS` (`spawn.ts:272`), and `--` is one of them; `planFor` (`spawn.ts:1106`) has no field for a prefix, and `runToCompletion` reads the binary out of `plan.argv[0]` (`spawn.ts:1183`) | The WSL leg needs a **named wrapper** in the plan rather than a longer argv (F8, F9) |
| **C5** | *"path translation (wslpath) for remembered checkouts and anything the console shows or opens"* | Nothing refuses a Windows path as a spawn argument: `--dir`'s and `--config`'s values are checked by `isSafeInstallationValue` (`spawn.ts:556`), which admits a backslash and a space | The hazard is not that translation is impossible but that it is **skippable**: an untranslated `C:\…` reaches the Linux `fugaro` as a `--dir` and produces a *wrong answer* rather than a refusal (F23) |
| **C6** | *"the CLI resolver running `wsl.exe -d <distro> -- fugaro …` **instead of a host path**"* | *Instead of a host path* is the load-bearing half, and it changes what the tree's POSIX assumptions are for. On the WSL leg the CLI is a file **inside a distribution**, which the Windows side cannot `stat` at all — so `binDirectories`, `isExecutableFile`, `locationsFor` and `childPath`'s join are **not on the WSL route**: the question "is there a `fugaro` here, and does it run?" is answered by the distro, through the existing `--version` probe. They are still needed, for two other reasons, and the plan must not confuse the three: (i) the `windows-latest` pack job runs the **whole unit suite** before it packs, and the suite's stub-based tests create a host executable and read its mode bits (F33), so a job that can never be green is a job whose release-time result nobody would believe; (ii) `console/src/main/installations.ts:619` asks `isExecutableFile` about a **stored** installation's `binaryPath`, and on the WSL leg that is a Linux path — the one place the host-path machinery meets a path it cannot see, and it must be taught to skip the question rather than answer it wrongly. The plan's Part C separates these: the **route** tasks (which the WSL leg needs) from the **suite** tasks (which the runner needs) |

---

## 1. The headline

**The console is already the right shape for this: it spawns a CLI it did not write, through an
injectable seam, with a fixed environment and a closed stdin — so the WSL route adds a wrapper and a
translation layer, not a redesign. Three things stand between the tree and a Windows artifact, and
only the first is hard.**

1. **The packer refuses Windows outright.** `fuseTarget` throws for any platform outside
   `SUPPORTED_PLATFORMS`, and `afterPack` is the pack's only flipper, so `electron-builder --win`
   produces no artifact at all (F35). There is no "pack it and fix it later": the `win32` row and the
   `win` builder target land together or nothing ships.
2. **Three POSIX assumptions in one module, each one line — and they are the *runner's* blockers
   rather than the route's.** `isExecutableFile`'s `stat.mode & 0o111` (`spawn.ts:850`), the `':'`
   separator in `binDirectories` (`spawn.ts:825`) and `childPath` (`spawn.ts:920`), and
   `privateWorkingDirectory`'s uid/chmod/lstat chain (`spawn.ts:955`). The WSL route itself does not
   walk the host's directories at all (C6) — but the `windows-latest` job runs the whole unit suite,
   whose stubs are host executables, and `installations.ts:619` asks the mode question about a
   *stored* installation's `binaryPath`, which on this route is a Linux path Windows cannot see. The
   module is a pure function of what it is given, which is why these are edits and not a rewrite
   (F4, F5, F12).
3. **The credential boundary is already correct and must not be touched.** `childEnvironment`
   (`spawn.ts:894`) builds the child's *whole* environment from `CHILD_ENV_KEYS` (`spawn.ts:247`)
   with no merge with `process.env` anywhere in the file; `nodeSpawner` (`spawn.ts:1082`) and
   `runToCompletion` (`spawn.ts:1194`) pass `stdio: ['ignore','pipe','pipe']`, so stdin is closed;
   `SPAWN_FLAGS` has no secret-bearing flag. The Windows environment therefore cannot reach the
   distribution, and no secret can ride on `wsl.exe`'s argv — **by construction, and the plan's job
   is to keep it that way rather than to build it** (F18, F19, F22).

What is genuinely new is small in code and large in consequence: a **wrapper** in the plan (F8, F9),
**translation in both directions** (F13, F15, F16), a **distro picker and three first-run pages**
(F10, F30, F31, F32), a **cancellation story that reaches inside the distribution** (F24), and an
**installer job** that cannot run yet (F35–F37, F40).

---

## 2. Findings

Each finding names a path, a symbol, one of **Wrong / Exists / Gap / Rule / Open**, and its
consequence. `Wrong` is something the tree says that the ruling makes false; `Exists` is something
the tree already has and the plan may lean on; `Gap` is something missing; `Rule` is a standing
constraint this work must not break; `Open` is a question the tree cannot answer.

### 2.1 The resolver (brief item 1)

| # | Class | Path and symbol | What it is, and its consequence |
|---|---|---|---|
| **F1** | Exists | `console/src/main/spawn.ts:1335` `resolveBinary`; `spawn.ts:1073` `ChildSpawner`; `spawn.ts:1082` `nodeSpawner` | Resolution is a walk over candidates produced by `binDirectories`, each probed by `probeVersion`, and the spawner is an injected interface. **A WSL resolution is a new candidate producer and a new wrapper, not a new resolver** — and the seam is why the tests need no Windows machine (F33 in §6). |
| **F2** | Exists | `spawn.ts:213` `PLATFORM_LOCATIONS`; `spawn.ts:234` `locationsFor` | The per-platform table exists because the design's §3.4 rule 4 says so in those words — *"Both platforms' lists are one table in the app … so a third platform is a row rather than an edit in each place that needs one"*. **This is the row the ruling adds.** |
| **F3** | Wrong | `spawn.ts:235` `return PLATFORM_LOCATIONS[platform] ?? LINUX_LOCATIONS` | An unknown platform silently gets **Linux's** directories. On `win32` that is a Windows machine told to look in `/usr/local/bin`. Consequence: the `win32` row must exist, and the fallback should refuse rather than guess — a fallback that answers "Linux" for a platform nobody decided is the silent substitution the design's rule 3 refuses elsewhere. |
| **F4** | Wrong | `spawn.ts:845`–`spawn.ts:853` `isExecutableFile` | `return (stat.mode & 0o111) !== 0`. Windows reports two modes, so the bit is never set. Two consequences, and they are different. On the **host** candidates a `win32` row would produce, every one is "not executable", `resolveBinary` walks the whole list and returns `no fugaro binary was found` (`spawn.ts:1380`) — a *runner* problem, because the unit suite's stubs are host executables (F33), and a *native-track* problem, because 0.10.0 has no host `fugaro.exe` to find. On the **WSL route** the function must not be asked at all: a Linux path inside a distribution cannot be `stat`ed by Windows, so the answer would be "no" about a `fugaro` that is there and works — which is exactly what `installations.ts:619` does with a stored installation's `binaryPath`. Consequence: the fix is two-part — a platform-aware mode question for the host, and a **skip** for a path the host cannot see, with the distro's own probe as the answer instead (C6). |
| **F5** | Wrong | `spawn.ts:825` `path.split(':')` in `binDirectories`; `spawn.ts:920` `join(':')` in `childPath` | The app's `PATH` is split on `':'` and the child's is joined with `':'`. On a Windows host the separator is `';'` and entries are `C:\…`, so the whole `PATH` is read as one entry. **The two halves follow different platforms, and on the WSL leg they disagree**: `binDirectories`'s split is about the **host** (the app's own `PATH`), so it becomes `';'` on `win32`; `childPath`'s join is about the **child**, and the child on the WSL leg is a Linux process, so it stays `':'` — which is what `childPath`'s own comment already says (*"the platform the child will run on"*) and what `spawn.test.ts:753`–`:758` already asserts. Consequence: only the host half changes for the WSL route, and it changes for the runner and the native track rather than for the route (C6). |
| **F6** | Exists | `spawn.ts:1351` the non-absolute configured path refusal; `spawn.ts:1380` the candidate list in the refusal | A configured path that is not absolute is refused *before* the walk, and an empty walk names every candidate. Consequence: the first-run pages inherit a value (`searched`) and a rule (a path the operator typed is checked against the app's cwd and run against the child's) — and the WSL case must express the same two things in distro terms (F31). |
| **F7** | Exists | `spawn.ts:1400` `resolveInstalledBinary` | It reads **exactly one** binary — the one an installation names — and never walks the list, so a stored installation whose binary has gone cannot have its window decided by some *other* `fugaro`. Consequence: a remembered WSL installation must resolve through this path (or a sibling with the same property), so a **renamed or removed distro** produces a stated failure rather than a silent switch to another distro's CLI. |
| **F8** | Gap | `spawn.ts:683` `assertArgvFlags`; `spawn.ts:272` `SPAWN_FLAGS` | Every emitted flag is asserted against a 13-entry list, and `--` is not on it. Consequence: `wsl.exe -d <distro> --cd <dir> --` cannot be emitted by `argvFor` or by any caller. The wrapper has to be a field the flag assertion does not read as a CLI flag (F9), and `assertSpawnSurface` (`spawn.ts:719`) — the test that iterates the whole table — is where the new shape is pinned. |
| **F9** | Gap | `spawn.ts:1106` `planFor`; `spawn.ts:1183` `const binary = plan.argv[0]` | The plan's argv **is** the child's argv with the binary first, and `runToCompletion` reads the binary back out of `argv[0]`. Consequence: the WSL leg needs a named wrapper on `SpawnPlan` (`command` + `prefix`), and every reader of `argv[0]` — `runToCompletion`, the spawn log's `argv` line (`console/src/main/spawnlog.ts:81` `SpawnLogEntry`), and the fixtures recorder — must read the new field. This is the one change that touches more than one module, and the plan makes it one task with one red step. |
| **F10** | Exists | `console/src/main/picker.ts:90` `PICKER_FILE_FILTERS`; `picker.ts:97` `DialogLike` | The binary picker is a native `openFile` dialog behind an injected `DialogLike`. Consequence: the **binary** picker needs no change on the WSL leg — the operator picks a *Windows-side* thing only if they have one — while the **distro** picker is a list and reuses none of it (F11). |
| **F11** | Gap | — nothing in `console/src/` | `grep -rni wsl console/src` returns **0**. Consequence: the enumeration of distributions (`wsl.exe --list --quiet`), its decode (F27) and its failure modes are new, and they are the only way to offer a picker at all. |

### 2.2 Paths (brief item 2)

| # | Class | Path and symbol | What it is, and its consequence |
|---|---|---|---|
| **F12** | Wrong | `spawn.ts:955` `privateWorkingDirectory`; `spawn.ts:1004` `assertSafeParent` | The private spawn directory is created `0700`, `lstat`ed against a symbolic link, checked against `stat.uid`, and re-`chmod`ed to `0700`; the base carries the uid so two users cannot collide. **None of that exists on Windows**: `process.getuid` is undefined (the code already handles `uid === null`), `chmodSync` is a no-op, and NTFS reports no POSIX mode. Consequence: on `win32` the function degrades to "make a directory under `tmpdir()` and return it", and the *claim* it enforces on POSIX must not be repeated as a Windows claim (F45, and `SECURITY.md`'s mode caveat that `docs/windows-cli/reconciliation.md`'s F8 already records). |
| **F13** | Wrong | `console/src/main/installations.ts:242` `isInside` | It compares resolved paths with a `'/'` appended: `inner.startsWith(outer.endsWith('/') ? outer : `${outer}/`)`. On a Windows host `resolve` is win32 (`C:\…`), so the separator appended is the wrong one and the prefix test can **fail to fire** — which is the test that stops the console keeping its store under the CLI's config directory. Consequence: the check must use `path.sep` or `path.relative`. On the WSL route the check is **vacuous and must stay vacuous rather than become wrong**: the console's store is under `%APPDATA%` (F14) and the CLI's config directory is inside the distribution, so no host path can be inside the other — and the temptation the plan must refuse is "put the store in the distribution so both sides spell one path", which would put the console's own file where the operator's `fugaro` reads (F14, F17). |
| **F14** | Exists | `installations.ts:280` `installationStorePath`; `installations.ts:294` | The store is `join(userData, 'installations.json')` — Electron's `userData`, which on Windows is `%APPDATA%\Fugaro Console`. Consequence: **the console's own store stays on the host**, never inside the distro, and that is the answer to the brief's "where the console's own store lives": it does not move. What moves is nothing. The finding is worth stating because the tempting design — put the store in the distro so both sides spell one path — would put the console's file where the operator's CLI reads. |
| **F15** | Gap | `installations.ts` `INSTALLATION_KEYS` | An installation is `id, project, binaryPath, configPath, checkout` — five strings, each a path in **one** spelling, with no field saying which side of the WSL boundary it is on. Consequence: a Windows host's remembered installation needs the distro and the Linux spelling, and the schema must grow by *name* (`distro`, and the meaning of `binaryPath`/`checkout` under it) with the bounds it already has — 16 entries, 64 KiB, mode `0600` (F46). |
| **F16** | Gap | `console/src/renderer/screens/degraded.ts:575` `noBinary`; the header's resolved-path line | What the console *shows* is the resolved absolute path, and what it *opens* is that path. On the WSL leg there are three spellings of one thing: the Linux path for argv (`--dir`, `--config`), the `\\wsl.localhost\<distro>\…` UNC path for anything Windows opens or the operator copies, and the distro-relative `~/…` an operator would type. Consequence: the plan must say which surface gets which spelling, and the *screen* must show the Linux one, because that is the one the operator can paste back into a terminal inside the distro. |
| **F17** | Wrong | `installations.ts:261` `cliConfigDirectories` | The three "CLI configuration" directories are `$XDG_CONFIG_HOME/fugaro`, `dirname($FUGARO_CONFIG)` and `~/.config/fugaro` — all computed from **the console's own** environment and home. On Windows the console's home is a Windows home, so the check protects a directory the CLI inside the distro does not use, and the directory the CLI *does* use (the distro's `~/.config/fugaro`) is not in the list at all. Consequence: the check must be expressed against the distro's home, which the console can only learn by asking the distro (F11's `wsl.exe`, or a documented default) — and the design's rule 7 test (*"never reads `~/.config/fugaro`"*, `docs/design/desktop-console.md` §3.4 rule 7) has to keep passing in the Windows form. |

### 2.3 Credentials and the keychain boundary (brief item 3)

| # | Class | Path and symbol | What it is, and its consequence |
|---|---|---|---|
| **F18** | Rule | `spawn.ts:894` `childEnvironment`; `spawn.ts:247` `CHILD_ENV_KEYS`; the comment at `spawn.ts:243` | The child's environment is built from a fixed eleven-key list, and *"there is no merge with `process.env` anywhere in this file"*. Consequence: **the Windows environment cannot leak into the distribution through the child's environment**, and this is a property the WSL leg inherits for free. The plan's job is to prove it still holds with `wsl.exe` in the middle — the fake in §6 reports the child's own environment, which is the only evidence that matters. |
| **F19** | Rule | `spawn.ts:1087` and `spawn.ts:1194`: `stdio: ['ignore','pipe','pipe']` | stdin is closed on every spawn. Consequence: the brief's *"nothing secret crosses wsl.exe argv or env (stdin only)"* is **already satisfied in the strongest form** — nothing crosses at all. If a secret ever had to cross, stdin is the one channel that is currently unused and admissible, and opening it is a decision rather than an edit (F22). |
| **F20** | Gap | — nothing in the repository | `git grep -n WSLENV` returns **0**. `wsl.exe` shares a Windows variable with the distribution only when its name is listed in `WSLENV`; with the variable unset the list is empty. Consequence: the boundary holds by default, and the plan sets `WSLENV` to the empty string **in the child's environment** so that it holds by statement too. The exact default when the variable is absent is **UNVERIFIED** (U4). |
| **F21** | Rule | `docs/design/desktop-console.md` §3.4 rule 7; `installations.ts:261`; `SECURITY.md` | The console never reads the CLI's configuration and never holds a credential. On the WSL leg the CLI's identity is the **distribution's own** application-default-credentials file, which `docs/windows-cli/wsl.md` records as a *different file* from the Windows one (its second trap). Consequence: nothing about the credential story changes and the operator's work does — they must have logged in **inside the distro**, and the environment view (`doctor`) must say so in the console's own words rather than reporting "not authenticated". |
| **F22** | Rule | `spawn.ts:272` `SPAWN_FLAGS`; `spawn.ts:534` `ARGUMENT_RE` | The flag allow-list is 13 read-only flags and the argument alphabet refuses whitespace, quotes, `$`, `;`, `|`, `&`, `<`, `>`, `(`, `)`, backticks, **backslashes**, `*`, `?` and every control character. Consequence: no secret can ride on argv by accident, and neither can a Windows path — the second half is F23. |

### 2.4 Spawning, streaming, cancellation and exit codes (brief item 4)

| # | Class | Path and symbol | What it is, and its consequence |
|---|---|---|---|
| **F23** | Wrong | `spawn.ts:556` `isSafeInstallationValue`; `spawn.ts:671`–`spawn.ts:674` the `--dir` emission | `--dir`'s and `--config`'s values are checked by the **loose** rule, which admits a backslash and a space — deliberately, because a POSIX path may contain a space. Consequence: a Windows path **is admitted** as `--dir`'s value and reaches the Linux `fugaro`, which answers from nowhere. The hazard is a wrong answer rather than a refusal, which is exactly the failure class the console's own rules refuse elsewhere. The plan must make the translation structural (the plan carries the Linux spelling, F9) rather than a check that can be forgotten. |
| **F24** | Gap | `spawn.ts:1224`–`spawn.ts:1235` `abandon`; `startSidecar`'s stop | Cancellation is `SIGTERM`, then `SIGKILL` after `SHUTDOWN_GRACE_MS = 2000` (`spawn.ts:298`), on the child the console spawned. On the WSL leg that child is `wsl.exe`, and **killing `wsl.exe` is not documented to kill the Linux process it started**. Consequence: a cancelled read or a stopped `watch` can leave a `fugaro` running inside the distribution — holding a lease, a lock or a run's worth of spend — and the operator cannot see it. The plan needs a Linux-side stop (the child's pid, captured inside the distro, killed through a second `wsl.exe`) and must state the residual window plainly. The exact behaviour is **UNVERIFIED** (U5, U6). |
| **F25** | Rule | `spawn.ts:1274` the `close` handler; the comment at `spawn.ts:1259`; `spawn.ts:1237` the deadline | A read settles on `close`, never on `exit`, because stdio may still be open when the child is reaped; and the deadline (`READ_TIMEOUT_MS = 90_000`, `spawn.ts:310`) is the backstop for a stream a stray grandchild holds. Consequence: with `wsl.exe` in the middle there are two more ways for the pipe to stay open (the WSL relay and the distro's session), so the existing backstop is what saves the screen — the failure becomes more likely without the rule changing. The plan tests the deadline through the wrapper rather than assuming it. |
| **F26** | Gap | `spawn.ts:1518` `probeVersion`'s `result.code` check; `spawn.ts:1133` `RunResult` | The version handshake reads the exit status and the one human line. Whether `wsl.exe` propagates the Linux child's status, and what it returns when the distribution cannot be started, are **UNVERIFIED** (U7, U8). Consequence: the resolver must not read a `wsl.exe` failure as a `fugaro` exit code, and the first-run pages must distinguish *no WSL*, *no such distribution*, *the distribution did not start* and *`fugaro` said no* — four states with four remedies, which is why F31 needs four state kinds and not one. |
| **F27** | Gap | `spawn.ts:1043` `readText` | Output is decoded as UTF-8, with a `StringDecoder` so a character split across chunks survives. `wsl.exe`'s **own** messages (as opposed to the Linux child's) are UTF-16LE in some builds and UTF-8 in others, and `WSL_UTF8=1` changes it. Consequence: the distro list may arrive NUL-interleaved, and the picker needs an explicit decode with a test vector rather than a hope (U9). |
| **F28** | Exists | `spawn.ts:1544` `decodeFeedLine` | The NDJSON reader strips a trailing `\r`. Consequence: the one place a Windows-side newline could have corrupted the feed does not — `watch --json` through `wsl.exe` is read by the same reader, and the plan's fixture work (F33) needs no new decoding. |
| **F29** | Exists | `spawn.ts:1669` `restartDecision`; `spawn.ts:292` `DEFAULT_BACKOFF`; `startSidecar`'s `exit` backstop | The streaming path restarts on its own backoff and destroys its streams after the grace once the child has exited. Consequence: `watch` survives a *CLI* crash inside the distro the same way it survives one on macOS — but "the distribution went away" and "the CLI died" are different failures with different remedies, and the screen must be able to say which (F26). |

### 2.5 The first-run pages (brief items 1 and 6)

| # | Class | Path and symbol | What it is, and its consequence |
|---|---|---|---|
| **F30** | Exists | `console/src/renderer/screens/degraded.ts:490` `NoBinaryState`; `degraded.ts:575` `noBinary`; `degraded.ts:661` `renderUnavailable` | The no-binary page draws a headline, the reason, the install command as `<code>`, a binary picker, and **the count of what was searched** — not the paths, which is the path-redaction rule. The state is *stated* in the renderer rather than imported from `src/source/cli.ts` (`degraded.ts:37`) because the renderer may not import a module that spawns. Consequence: the three (four, per F26) new pages are drawn the same way, in the same file, with the same stated-state rule — and [docs/design/console-styling.md](../design/console-styling.md) §2.7–§2.9 is where the variants and their one table live. |
| **F31** | Exists | `console/src/source/cli.ts:181` `NoInstallationState`; `cli.ts:217` `noInstallationState`; `cli.ts:203` `class NoInstallation` | The state carries `kind: 'no-binary'`, a `reason`, the `installCommand`, the `searched` list (re-derived from the same `binDirectories` the failed resolution walked) and the `configuredPath`. `createBoundSource` refuses to spawn at all when there is no installation. Consequence: the WSL states are **siblings of this one** — same shape, same refusal path, no sidecar, no retry — and the plan's naming is `no-wsl`, `no-distro`, `no-fugaro-in-distro`, each with its own reason and remedy. |
| **F32** | Wrong | `console/src/source/cli.ts:173` `FUGARO_INSTALL_COMMAND` = `brew install dimipaun/tap/fugaro` | One install line, shown on every platform. On the Windows leg it is wrong twice: it is not a Windows install, and Homebrew is not in the distribution. Consequence: the install line becomes per-route, and the WSL route's line is `docs/windows-cli/wsl.md`'s own procedure (download the linux archive, verify it, `tar -xzf`, put it on `~/.local/bin`) — pointed at, not retyped, so the two cannot drift. |
| **F33** | Exists | `console/test/unit/stub.ts:113` `installStub`; `stub.ts:68` `StubReport` | The test double is a real executable `fugaro` that `exec`s the test's own Node and appends a JSON line to a report file — **the child's own view** of its argv, cwd, environment keys, `PATH` and `FUGARO_PROJECT`, out of band. Consequence: the fake `wsl.exe` the brief asks for is this mechanism with one more hop, and it is why the resolver tests need no Windows machine. One caveat: `installStub` sets the execute bit, which `isExecutableFile` reads — so the existing stub-based tests **cannot pass on `windows-latest` today** (F4), which is why the release-time Windows pack job cannot be green until Part C lands — and why D7(c)'s by-hand dry run is the only way to find that out before a tag. |
| **F34** | Exists | `spawn.ts:786` `binDirectories`'s `platform` option; `spawn.ts:919` `childPath`'s; `spawn.ts:234` `locationsFor`'s; `spawn.ts:955` `privateWorkingDirectory`'s `base`/`uid` options; `spawn.test.ts:753`–`:758`, `:963`, `:896`–`:926` | **Every platform-dependent function in the resolver already takes the platform as an argument**, and the tests already drive both platforms from one machine — `childPath(binary, 'darwin')` beside `childPath(binary, 'linux')`, `locationsFor(platform)` walked in a loop, `privateWorkingDirectory({base, uid})` with a fabricated uid. The comments say why: *"`platform` replaces `process.platform` … It is how a test on one machine drives the other platform's order"*. Consequence: this work's test strategy rests on a seam that **exists** rather than on one it must build, and the two Windows-shaped cases it adds (T-C7's `';'` split, T-D4's no-mode-claim) are rows in tests that already have the shape. It is also why the WSL route's *third* platform does not have to be the running one to be tested. |

### 2.6 The packer, the installer and the release (brief item 5)

| # | Class | Path and symbol | What it is, and its consequence |
|---|---|---|---|
| **F35** | Wrong | `console/src/main/fuses.ts:298` `SUPPORTED_PLATFORMS`; `fuses.ts:379` `fuseTarget`; `console/package.json`'s `build.afterPack` | The fuse hook flips six fuses and **throws** for a platform outside the list, deliberately: *"a skipped flip is a binary that ships with the fuses unset"*. `afterPack` is the only flipper. Consequence: **`electron-builder --win` fails the pack.** The `win32` row — `<appOutDir>/<productFilename>.exe`, the extension electron-builder itself uses (`fuses.ts:346`) — and the `win` builder target are one change or there is no artifact. |
| **F36** | Gap | `console/package.json`'s `build` block; its `scripts` | `mac` and `linux` exist; `win` does not. `package:linux` exists; `package:win` does not. Consequence: the staged workflow's `npm run package:win` cannot resolve, and the plan's Part B writes the script **in `package.json` rather than in the workflow**, which is this repository's own rule (the `--publish never` precedent: `console-linux.yml` says *"the packer is the lockfile's exact electron-builder and nothing else, and `--publish never` is in the script rather than in this line so no invocation can add one"*). |
| **F37** | Gap | `console/package.json`'s `build.electronDownload.checksums` | Three hashes are pinned: `darwin-arm64`, `darwin-x64`, `linux-x64`. Consequence: a Windows leg downloads Electron against **no pinned hash**. Adding one is a supply-chain pin and therefore an owner-reviewable edit, not a convenience. |
| **F38** | Exists | `fuses.ts:331` `macExecutable`; `fuses.ts:358` `linuxExecutable`; `fuses.ts:358`–`fuses.ts:369` | The per-platform executable resolution is already one table with one refusal for a context that names nothing. Consequence: the `win32` row's shape is decided, not invented — and the refusal it inherits (a context that names no binary is refused rather than guessed) is the property that keeps a Windows pack from silently flipping nothing. |
| **F39** | Rule | `docs/design/desktop-console.md` §7.6 rule 2; `docs/release.md` "Verifying the console's release" | The Linux artifacts are unsigned and *"the release must not describe the Linux artifacts as signed"*. The Windows ruling keeps the same shape and adds a user-visible consequence: **SmartScreen will warn** on an unsigned installer, and the warning names the publisher as unknown. Consequence: the docs and the release notes say "unsigned" in those words and say what SmartScreen will show; nothing in the installer job may claim otherwise, and there is no signing key to handle. |
| **F40** | Exists | `docs/pending-workflows/console-linux.yml`; `docs/pending-workflows/README.md` `## console-linux.yml` | The Linux precedent, staged whole: the header's STAGED/NOT APPLIED block, the copy-in commands, `permissions: contents: read`, pinned actions by 40-hex SHA, `node-version-file: console/package.json`, `npm ci --ignore-scripts`, the exactly-one-artifact assertion, `upload-artifact` with `if-no-files-found: error`. Consequence: the Windows file is the same file with a different runner, a different packer target, a different artifact assertion **and different triggers** — the Linux file runs on every pull request, and D7(c) puts the Windows one on the release tag and a manual dispatch, and nowhere else. The reason it is **its own workflow** is still the one that file states: the release gate's required-check list is derived from `console-tests.yml`'s **single** job, so a second job in that file would be a gate change by the back door (D8(a)). |
| **F41** | Rule | `scripts/release-gate.sh`'s `names=(...)`; `internal/cli/docs_desktop_console_test.go:4315` `deriveConsoleCheckNames`; `docs_desktop_console_test.go:4429` `TestReleaseGateRequiresTheConsoleCheck` | The gate's list is the two console matrix legs plus `test`, `terraform` and `rules`, and the derivation **refuses a workflow with more than one job**. Consequence: the Windows job is not in the gate, a red Windows pack is information rather than a blocked release, and folding it into `console-tests.yml` would silently change which checks gate every tag — the owner's decision, not this work's. |
| **F42** | Gap | — `.github/workflows/` | `grep -rn windows-latest .github/workflows/` returns **0**. Consequence: the first Windows CI in this repository is this one, and there is no precedent to copy for the runner's quirks (shell, `unset`, path separators in a `run:` step). |
| **F44** | Rule | `docs/pending-workflows/README.md`'s opening rule | *"Fugaro refuses to push a commit that touches `.github/workflows/**`"*, so every workflow change is staged for the owner. Consequence: the installer job is staged under its own name, with its copy-in in the staging README, and no agent writes the workflow. |
| **F45** | Exists | `SECURITY.md`; `docs/windows-cli/reconciliation.md` F8 and D12; `console/src/main/spawnlog.ts:71` `SPAWN_LOG_MODE = 0o600`; `installations.ts`'s `0600`/`0700` | The repository's mode guarantee is **POSIX-only**: Windows reports two file modes and the console sets no ACL, so `installations.json`'s `0600` and the spawn log's `0600` are not enforceable there. Consequence: this work must not restate them as Windows claims, and the Windows host's protection for those two files is the user profile's own ACL — which is what `SECURITY.md` already says about the CLI's files on Windows. |

### 2.7 The documents the ruling makes stale (brief item 7, and C4 in the README's O7)

**One finding, four documents, and it is numbered once on purpose**: the sentence *"Windows stays
out"* is one claim this repository makes in four places, and it is one finding because it moves in
one edit or in none. The rows below are the four places, and each says which half of the sentence
moved.

| # | Class | Path | What it says today | What the ruling makes it |
|---|---|---|---|---|
| **F43** | Wrong | `docs/windows-cli/wsl.md` (its "What native Windows does not do yet" table, the row *"The desktop console \| Out of scope: it needs a Windows CLI and an Authenticode-grade decision first (**F40**)"*, and its F40 line above the table) | The console cannot reach a WSL-only installation. | **Partly reversed for the console**: this work is exactly that reach, and the ruling is the owner's own. The row needs a dated note; the CLI track's F40 stays true of the *CLI*, not of the console. |
| **F43** | Wrong | `docs/design/desktop-console.md` §4.2 *"**Windows as a *packaged* deliverable** — … signing and distributing Windows is still a later phase (**D10** …)"*, §7.6 rule 3 *"Windows is untouched"*, the owner-run table's *"Windows signing \| owner only, later (D10)"* and D10's row | Windows is out of 0.10.0 in full. | **Half of it moves**: the *build* is in 0.10.0 by the ruling, the *signing* answer is untouched (there is still no Authenticode identity and none is planned). Each sentence needs a dated note that says which half moved — a rewrite would claim more than the ruling does. |
| **F43** | Wrong | `docs/release.md`'s console paragraphs (*"Windows stays out (D10's amendment takes Linux and leaves Windows where the 2026-10-15 answer put it)"*, and "Verifying the console's release") | The console release carries macOS and Linux and nothing else. | **Half of it moves, and the Windows half moves to the end of the line**: under D7(c) the pack runs at release time only, so `docs/release.md` gains the by-hand dry run before the tag and the sentence that the installer is **unsigned** and SmartScreen-warned. **The release's own asset list is unchanged** — the installer is not an asset of `console-vX.Y.Z` (D7), and whether it becomes one is undecided. |
| **F43** | Wrong | `console/README.md`'s "The Linux build, unsigned (the owner's ruling of 2026-10-20)" section, which opens *"Windows stays out"* | Same. | Same dated note. |
| **F46** | Exists | `installations.ts`'s bounds (16 entries, 64 KiB) and modes (`0600`/`0700`); `INSTALLATION_KEYS` | The store's shape is fixed and validated with `hasExactlyKeys`, so an unknown key is refused. | A schema that grows must grow **by name** and in one version step, or a console that wrote the new key refuses to read its own file on the next start. The plan makes the schema change and its reader one task. |
| **F47** | Rule | `website/data/install.yaml`'s Windows entry (*"Windows is untested: this repository builds no Windows binary."*), pinned by `internal/cli/docs_website_install_test.go` to `.goreleaser.yaml` and to README.md's "## Requirements" | Windows is untested and no Windows binary is built. | **Unchanged, and the plan must not touch it.** The console's Windows installer is not a `fugaro` binary: this repository still builds no Windows *CLI*, and the site's sentence is about the CLI. A future edit that read the console's installer as satisfying it would be false, and the Go test that pins the file to `.goreleaser.yaml` is what would catch it. |

---

## 3. The census

**The commands are the durable part and the figures are not** — that rule is `docs/windows-cli/reconciliation.md`'s F37 and it holds here for the same reason: this tree moves under every one of these numbers. Each was run at `ccc406b2` from the repository root.

| What the brief names | Command | Count |
|---|---|---|
| WSL, anywhere in the console | `grep -rni wsl console/src \| wc -l` | **0** |
| `WSLENV`, anywhere in the repository | `git grep -n WSLENV ccc406b2 -- . \| wc -l` — **pinned to the base commit**, because this work's own documents and staged workflow now spell the name and a bare `git grep` would count those | **0** at `ccc406b2` |
| `win32`, in the console | `grep -rn win32 console/src` | **2** — and both are comments in `console/src/main/fuses.ts` (`:290` the refusal's own reason, `:346` the extension table it cites). No code path handles it |
| `windows-latest`, in CI | `grep -rn windows-latest .github/workflows/ \| wc -l` | **0** |
| `process.platform`, in the console | `grep -rn process.platform console/src \| wc -l` | **8** |
| The platform literals | `grep -rn "'darwin'\|'linux'" console/src/main/*.ts \| wc -l` | **5** |
| Mode bits (`0o111`, `0o600`, `0o700`, `0o777`) | `grep -rn "0o111\|0o600\|0o700\|0o777" console/src \| wc -l` | **7** |
| uid, `chmodSync`, `lstatSync` | `grep -rn "getuid\|chmodSync\|lstatSync" console/src \| wc -l` | **8** |
| The `':'` separator | `grep -rn "split(':')\|join(':')" console/src` | **2** — `spawn.ts:825` and `spawn.ts:920` |
| TypeScript sources / test files | `find console/src -name '*.ts' \| wc -l` / `find console/test -name '*.ts' \| wc -l` | **30** / **75** |
| Files staged under `docs/pending-workflows/` | `ls docs/pending-workflows/*.yml \| wc -l` | **5** at `ccc406b2`; **6** with this work's file, of which **4** are pending and **2** stay staged by design (`console-tests.yml`, `console-release.yml`) |

Two of these are worth reading as findings rather than as counts. **`win32` appears twice and both
are prose**: the refusal at `fuses.ts:290` explains why it is absent, which means the tree has
already decided where the decision lives and the decision is "not yet" — not "somewhere else".
And **`wsl` appears zero times**, so every WSL fact this work needs is new surface rather than a
path that exists and is unused.

---

## 4. Decisions for the owner

Each is a real fork with a recommendation first, the alternative and what it costs. **The owner
answered all twelve on 2026-10-20**, and the table below is the record: the option taken and the
reason in one line. **Answering a decision is not approving the build** — the plan's OWNER APPROVAL
GATE is where approval happens, and it is still not passed. The namespaces are local: **D1–D12 are
this folder's**, they are unrelated to
[docs/design/desktop-console.md](../design/desktop-console.md)'s D1–D20 and to
[docs/windows-cli/](../windows-cli/README.md)'s D1–D16, and O6 records the collision.

| # | The owner's answer (2026-10-20) | Why, in one line |
|---|---|---|
| **D1** | (a) — the named wrapper on `SpawnPlan` | The spawn log is the operator's record of what ran, and a longer argv would name `wsl.exe` as the binary in every refusal sentence they read |
| **D2** | (a) — a named distribution, defaulted from `wsl.exe --status` | A typo has to be distinguishable from a missing distribution before the spawn fails, and only the enumeration does that |
| **D3** | (a) — the Linux spelling where the console states or passes, the UNC spelling only where Windows opens | The path on the screen has to be the one the operator can paste into a terminal inside the distribution |
| **D4** | (a) — one `wslpath -a -u` call per translation, through the same spawner, cached | A mapping the console computed and the distribution disagrees with is the silent wrong answer this repository refuses everywhere else |
| **D5** | (a) — the existing allow-list, plus `WSLENV` empty and the child-report test | The boundary already holds by construction; the work is to prove it holds with `wsl.exe` in the middle, not to build it |
| **D6** | (a) — capture the Linux pid and kill inside the distribution | A cancelled read must not leave a `fugaro` holding a lease inside the distribution with nothing on screen to say so |
| **D7** | (c) — the pack runs at release time only, against the recommendation | The owner's words: *"a waste of resources; if there are problems we will fix them then"* — and the cost, a packaging break showing at release time, is accepted with a by-hand dry run before the tag |
| **D8** | (a) — not in the release gate | A red Windows pack is information, and adding a leg to the gate would change which checks gate every tag on this repository |
| **D9** | (a) — `docs/windows-cli/wsl.md` stays the authority | One procedure in one place; the console's first-run page names the document rather than retyping four commands that would then drift |
| **D10** | (a) — the SmartScreen sentence in the release notes, the release doc and the download page | One sentence, three places a reader arrives from, and no claim anywhere that it is signed |
| **D11** | (a) — the fake `wsl.exe` on the existing `installStub` | The only claim that matters is what the Linux side received, and only the stub's own out-of-band report can say that |
| **D12** | (a) — propose Check 119 and claim nothing | The register's rule is that the phase which ships the check writes it, and no phase here is approved |

### D1 — Where the WSL wrapper lives in the plan

**Answered, 2026-10-20: (a).** The spawn log's `argv` line is the operator's record of what ran, and
option (b) would put `wsl.exe` where the binary belongs in every refusal sentence they read.


**(a) Recommended: a named wrapper on `SpawnPlan`.** `planFor` gains a `command` (the executable,
`wsl.exe`) and a `prefix` (the argv before the CLI's own words: `-d <distro>`, `--cd <linux-dir>`,
`--`), `argv` stays what the CLI receives, and `runToCompletion`/`startSidecar`/the spawn log read
the new fields. `assertArgvFlags` keeps asserting the *CLI* half only, which is what it was written
for, and `assertSpawnSurface` gains the row. **Cost:** three readers of `argv[0]` change, and the
spawn log's line gains two fields.

**(b) A longer argv: `argv[0] = 'wsl.exe'`, then the WSL flags, then the binary.** Cheapest edit and
the worst one: `assertArgvFlags` would have to learn four `wsl.exe` flags, which widens the
allow-list that exists to be narrow, and `runToCompletion`'s "what did we run" line would name
`wsl.exe` as the binary in every refusal sentence the operator reads.

**(c) A second spawner implementation (`wslSpawner`) behind `ChildSpawner`.** Superficially
attractive — the seam exists — but it puts the translation *below* the plan, where the argv table
and the allow-list cannot see it, and it makes the argv the operator sees in the spawn log a lie.
**Refused**, for the reason the design refuses a second fuse flipper: one place decides, or the two
drift.

### D2 — What a "distro" is in the store, and its default

**Answered, 2026-10-20: (a).** A typo has to be distinguishable from a missing distribution *before*
the spawn fails, and only the enumeration does that.

**(a) Recommended: a named distribution, picked from `wsl.exe --list --quiet`, with the default being
the distribution `wsl.exe --status` names as default, and no default at all if that command says
nothing.** The store gains one field (`distro`). A stored installation with no `distro` is a Windows
installation that is not on the WSL route — which is a state, not an error.

**(b) A free-text distro name the operator types.** No enumeration, no decode problem (F27), and no
way to tell a typo from a missing distribution until the spawn fails. Cheaper by one call and worse
by one whole failure class.

**(c) No field: always the default distribution.** Fewest moving parts, and it breaks the moment the
operator has two — which is the case the brief's "distro picker/default" exists for.

### D3 — Which spelling the console shows and which it passes

**Answered, 2026-10-20: (a).** The path on the screen has to be the one the operator can paste into a
terminal inside the distribution.

**(a) Recommended: the Linux spelling everywhere the console *states* or *passes*, and the
`\\wsl.localhost\<distro>\…` UNC spelling only where Windows itself opens the path.** The resolved
path, `--dir`, `--config` and the header's line are Linux (`/home/dimi/src/fugaro`); a "reveal in
Explorer" affordance and the file dialog's initial directory use the UNC form. **Cost:** two
spellings in the UI code and a rule about which is which — which is the point, because the Linux
one is the one the operator can paste into a terminal inside the distribution.

**(b) The UNC spelling everywhere.** Windows-native and useless to the CLI: `--dir \\wsl.localhost\…`
is not a path the Linux `fugaro` can resolve, and the console would have to translate at the last
moment — the skippable translation F23 describes.

**(c) Translate only when the value would be refused.** There is nothing to refuse (F23), so this is
option (b) with a check that never fires.

### D4 — How `wslpath` is invoked, and whether it is a separate process

**Answered, 2026-10-20: (a).** A mapping the console computed and the distribution disagrees with is
the silent wrong answer this repository refuses everywhere else.

**(a) Recommended: one `wsl.exe -d <distro> -- wslpath -a -u <windows-path>` call per translation,
through the same spawner and the same deadline, cached for the life of the window.** It is the
documented tool, it is inside the distribution by definition, and it costs one process per distinct
path.

**(b) Compute the mapping in the console.** It is a pure string function for the common case
(`C:\Users\X` → `/mnt/c/Users/X`) and a **wrong** one for every mount the operator has configured,
every `--cd` into a path that is not under `/mnt`, and every distribution with a different
`/etc/wsl.conf`. **Refused**: a path the console computed and the distribution disagrees with is
the silent wrong answer this repository refuses everywhere else.

**(c) Ask once at installation time and store both spellings.** Stale the moment the operator moves
a directory, and the store would then hold a path that no longer resolves — with no way to tell that
from a path that never did.

### D5 — What "nothing secret crosses" is enforced by

**Answered, 2026-10-20: (a).** The boundary already holds by construction; the work is to prove it
holds with `wsl.exe` in the middle, not to build it.

**(a) Recommended: the existing construction, plus one explicit statement.** `childEnvironment`'s
allow-list is the enforcement and it already holds; the plan adds `WSLENV` with an empty value to the
Windows leg's child environment, so the "share these Windows variables" mechanism is *stated* to be
empty rather than merely unset, and it adds a unit test that diffs the child's own reported
environment against `CHILD_ENV_KEYS` **through the wrapper** (the fake reports it, F33).

**(b) Also screen the argv for secret-shaped strings.** A heuristic over argv in a process whose
argv the operator can read anyway; it would fire on a project named `token-budget` and teach the
reader to ignore it. **Refused.**

**(c) Open stdin and pass secrets that way.** Not needed by anything in 0.10.0 — the CLI reads its
own ADC inside the distribution — and opening a channel the console has no use for is a capability
added for a future that may never arrive. The plan records it as the *only* admissible channel
should a secret ever have to cross (F19).

### D6 — Cancellation that reaches the Linux child

**Answered, 2026-10-20: (a) — the kill inside the distribution.** A cancelled read must not leave a
`fugaro` holding a lease, a lock or a run's worth of spend inside the distribution with nothing on
screen to say so. **The residual window, stated rather than implied**: between the `SIGTERM` to
`wsl.exe` and the `kill` inside the distribution there is a window in which the Windows-side process
is gone and the Linux-side one is not yet — and if the `wsl.exe` that runs the pid-writing shell
exits before that file is written, the window is the whole of the cancellation. Part F's cancellation
task measures what a runner can measure of it and **U6 asks a Windows machine to settle the race**;
until then the screen says the read was cancelled and the operator's remedy is `ps aux` inside the
distribution, which is what the manual checklist's step 9 looks at.

**(a) Recommended: capture the Linux-side pid and kill it inside the distribution.** The wrapper
runs the CLI under a shell that writes its own pid to a file the console can read through the same
`wsl.exe`, and cancellation is `SIGTERM` to `wsl.exe` **and** a `kill` inside the distribution after
the existing `SHUTDOWN_GRACE_MS`. **Cost:** one more `wsl.exe` per cancellation, a pid file whose
lifecycle the plan must state, and a residual window that must be written down rather than implied.

**(b) `wsl.exe --terminate <distro>`.** Kills everything in the distribution, including the
operator's own shells and any other `fugaro` — a console that can terminate a distribution is a
console that can destroy work it does not own. **Refused.**

**(c) Kill `wsl.exe` only and document the orphan.** Zero work, and it leaves a `fugaro` holding a
lease inside the distribution with nothing on screen to say so. Acceptable **only** as an interim if
the pid path proves unreliable — and then the residual must be stated on the screen, not in a
comment.

### D7 — Where the Windows artifact is built and what it is

**Answered (c), 2026-10-20 — the owner's ruling, against the recommendation below.** The Windows pack
runs **at release time only**: on the release tag, or on a manual dispatch before it, and **never on a
pull request**, not even path-filtered. The owner's words: *"a waste of resources; if there are
problems we will fix them then."*

**What that means, concretely.**

- **The staged file stays its own workflow** — `docs/pending-workflows/console-windows.yml`, one job on
  `windows-latest`, triggered by `on: push: tags: ["v[0-9]+.[0-9]+.[0-9]+"]` **plus
  `workflow_dispatch`** and by nothing else. There is no `pull_request` trigger, no advisory pack check
  on a pull request, and **nothing is added to `console-tests.yml`**: the Windows job is not a third
  matrix leg and it is not a second job in the tests workflow, which is what keeps the release gate's
  derived check list exactly as it is (F41, D8(a)).
- **The artifact is the unsigned NSIS installer** the recommendation describes, and nothing about it
  changes: `npm run package:win` → `electron-builder --win --publish never`, the NSIS target named
  explicitly rather than inherited, `build.win.artifactName` following the Linux pattern, uploaded as
  the job's own artifact with `if-no-files-found: error`. Nothing signs it, so the SmartScreen sentence
  (D10) is unchanged and the release notes must still say **unsigned** in those words.
- **The accepted risk: a packaging break shows at release time**, and it is fixed then. Nothing between
  releases exercises the Windows pack, so a break in `console/package.json`'s `win` block, in the fuse
  table's `win32` row or in a lockfile bump is found by a release rather than by a pull request. **The
  mitigation is the manual dispatch**: [docs/release.md](../release.md)'s "Before you tag" checklist
  tells the owner to run the job by hand before the tag, which is the only dry run this design has, and
  the checklist says so in those terms.
- **What the ruling does not change.** The `win32` fuse row and the `win` builder target still land
  **together, in one part** (F35): there is no "pack it and fix it later", and neither half ships
  alone. And the ruling answers *where and when the pack runs*, not *what the release does with its
  artifact*: **the Windows installer is not an asset of `console-vX.Y.Z`**, and whether it becomes one
  — which would mean the release's `publish` job fetching this workflow's artifact, a mechanism the
  console release does not have today — is a separate decision that nobody has taken.

The three options as they were written, so the answer can be read against the fork it was made from:

**(a) Recommended, and *not* taken: `windows-latest`, `npm run package:win` → `electron-builder --win
--publish never`, NSIS, unsigned, uploaded as a build check on every pull request.** The default
Windows target is NSIS, so it is named explicitly rather than inherited; `build.win.artifactName`
follows the Linux pattern (`Fugaro.Console-${version}-win-${arch}.${ext}`); the version packs as
`0.0.0`, exactly as the Linux check does. Its cost is a job on every pull request, which the owner
weighed and refused.

**(b) A portable `.zip` instead of an installer.** No installer means no SmartScreen decision to
explain and no `nsis` step — and it means the operator has an unpacked directory, no Start-menu
entry, no uninstaller, and no answer to "where did it go". **Not taken.**

**(c) Build it at release time only (a tag, or a manual dispatch before one) — the owner's answer.**
Cheaper CI, and exactly the failure the Linux precedent refuses: a packaging path that is only
exercised on a tag breaks silently between releases. That objection is not answered by the ruling; it
is **accepted**, with the by-hand dry run as the mitigation and a red pack at release time as the
outcome to fix then.

### D8 — Whether the Windows check joins the release gate

**Answered, 2026-10-20: (a).** A red Windows pack is information, and adding a leg to the gate would
change which checks gate **every** tag on this repository — the owner's decision, taken deliberately
rather than by default.

**(a) Recommended: no, exactly as the Linux check does not.** The gate's `names` are derived from
`console-tests.yml`'s single job and `deriveConsoleCheckNames` refuses a multi-job workflow, so
adding a leg to the gate is three edits in one commit and it changes which checks gate **every** tag
on this repository — the owner's decision and not a packaging change. A red Windows pack is
information.

**(b) Yes, add it.** Defensible once Windows is a supported deliverable, and it must then land as one
commit: the job, `scripts/release-gate.sh`'s `names` array, and a widened `deriveConsoleCheckNames`
— or `TestReleaseGateRequiresTheConsoleCheck` fails and every tag is refused in the window between.

**(c) Fold it into `console-tests.yml` as a third matrix leg.** The matrix is the *same job* on two
runners; a third leg would be a third runner running the *tests*, not the packer, and the packer's
job is a different job with a different artifact. It would also make the derivation read a
multi-platform job whose name GitHub spells `console (windows-latest)` — which is a gate change by
the back door. **Refused.**

### D9 — Where the WSL facts are documented for the operator

**Answered, 2026-10-20: (a).** One procedure in one place; the console's first-run page names the
document rather than retyping four commands that would then drift.

**(a) Recommended: `docs/windows-cli/wsl.md` stays the install authority and the console links to
it.** One procedure, one place, and the console's first-run page names the document rather than
repeating four commands that would then drift (F32).

**(b) A second copy of the procedure inside the console's own screen.** The screen is the only place
the operator is looking when they need it, and a second copy of a four-command procedure in a
`<code>` block is a copy that goes stale silently — the repository's own rule about duplicated
facts.

**(c) A new `docs/console-windows/wsl.md`.** A third place. Refused: this directory's documents are
about the *console*, and the install procedure is about the *CLI*.

### D10 — The SmartScreen sentence, and where it goes

**Answered, 2026-10-20: (a).** One sentence, three places a reader arrives from, and no claim anywhere
that it is signed.

**(a) Recommended: say it in the release notes, in `docs/release.md`'s console verification section,
and on the download page's console entry — *"the Windows installer is unsigned: Windows SmartScreen
will warn that the publisher is unknown, and More info → Run anyway is the way past it"*.** One
sentence, three places a reader arrives from, and no claim anywhere that it is signed.

**(b) Only in the release notes.** Fewer edits and the reader who clicks Download is the one who
meets the dialog.

**(c) Buy an Authenticode certificate.** The owner's decision and not this work's, it is the D10
answer [docs/windows-cli/](../windows-cli/README.md) leaves standing, and nothing here should be
read as proposing it.

### D11 — The test seam for the resolver without a Windows machine

**Answered, 2026-10-20: (a).** The only claim that matters is what the Linux side received, and only
the stub's own out-of-band report can say that.

**(a) Recommended: a fake `wsl.exe` built on the existing `installStub` mechanism.** A real
executable named `wsl.exe` on a temporary directory that parses `-d`/`--cd`/`--`, records its own
argv and environment to the same out-of-band report file (`stub.ts:68` `StubReport`), and re-execs
the stub `fugaro` with the remaining argv and the translated cwd. The tests then assert **what the
Linux side received**, which is the only claim that matters. **Cost:** one more test helper, and a
decoder for the `--list` fixture (F27).

**(b) A pure-function test of the argv builder.** Cheap, and it proves the console is
self-consistent rather than that the child got the right thing — which is the exact failure
`stub.ts`'s own comment says it exists to avoid.

**(c) A recorded transcript of a real `wsl.exe` session.** Better evidence and it needs a Windows
machine to record, which is the constraint the brief names.

### D12 — Whether this work claims a check number

**Answered, 2026-10-20: (a).** The register's rule is that the phase which ships the check writes it,
and no phase here is approved.

**(a) Recommended: propose Check 119 for the owner's manual checklist and claim nothing.** The
register's written sections run to 89; 90–99, 100–109, 110, 111, 112, 113–118 are taken or named, so
119 is the next free number after reading the register name by name — and the register's own rule is
that *the phase that ships the check writes it*, and no phase here is approved. **The runner checks
are not register material**: they run on a GitHub-hosted runner, need no secret and no owner.

**(b) Write Check 119 into `docs/gcp-live-checklist.md` now.** It would be a check whose text exists
before the feature does, and the register's convention is the opposite (the plan writes the check's
text; the phase that ships it writes the row).

**(c) Claim no number at all.** The checklist would then have nothing to point at when the owner sits
down to run it, and the one document that says "this has not been verified on real Windows" would be
this folder alone.

---

## 5. Items found while planning (O1–O7)

- **O1 — `docs/windows-cli/wsl.md`'s F40 is now half wrong.** Its console row says the console is out
  of scope because it "needs a Windows CLI and an Authenticode-grade decision first". The ruling
  removes the first need for the *console* (the console runs the Linux CLI) and leaves the second
  where it was. The note belongs in that file and **this directory does not write it** (F43).
- **O2 — the fuse hook's comment is the decision record.** `fuses.ts:290` explains why `win32` is
  absent and names exactly what a Windows leg would need. When Part B lands, that comment is the
  paragraph to amend, and it is already the right paragraph.
- **O3 — `console/README.md` is the console's own reference and carries 3,700+ lines of claims.**
  Several of its platform claims (`PLATFORM_LOCATIONS`'s two rows, the fuses' platform list, the
  Linux section's "Windows stays out") become dated notes rather than rewrites. Part H owns them and
  the plan lists them by section rather than by line.
- **O4 — the spawn log's `argv` line.** `spawnlog.ts`'s entry carries `argv`, `cwd` and `project`,
  and Check 59a's `sort -u` over `.project` reads them. A wrapper adds two fields, and the check's
  expectations have to be re-read rather than assumed. The plan's Part F carries it.
- **O5 — the fixture recorder.** `console/tools/record-fixture/` spawns the CLI to record a feed.
  On a Windows host it would spawn through the wrapper too, and a fixture recorded that way is the
  same bytes — worth stating so nobody records a second set.
- **O6 — the D-number namespace** (repeated in the README): four conventions now exist in the tree
  and this work takes the folder-local one.
- **O7 — three documents still say Windows is out** (repeated in the README, F43): they need dated
  notes, and Part H is where they land.

---

## 6. UNVERIFIED

Every row is a fact this repository cannot establish from a Linux machine, with what would settle it.
**None of them is a claim**: the plan is written so that each is either measured by a runner check or
run by the owner, and so that no sentence in the docs depends on the answer before it is known.

| # | What is unverified | What would settle it |
|---|---|---|
| **U1** | That `electron-builder --win` produces an NSIS installer for this app at all, with the six fuses flipped by the `win32` row | A manual dispatch of `console-windows.yml` on a `windows-latest` runner, once Part B lands — or the release-time run |
| **U2** | That the fuse bytes are where the `win32` row says (`<appOutDir>/<productFilename>.exe`) for this packer version | The same run's artifact, opened; the Linux leg's `--appimage-extract` read-back is the precedent |
| **U3** | Whether `npm run test:unit` passes on `windows-latest` once Part C lands — the suite has 75 test files and only some are platform-neutral | The same run's `npm run test:unit` step |
| **U4** | The exact behaviour of `wsl.exe` when `WSLENV` is **absent** rather than empty | A Windows machine, or the WSL documentation read at the version the runner has |
| **U5** | Whether killing the Windows-side `wsl.exe` kills the Linux process it started | A Windows machine: start `sleep 300` through `wsl.exe`, kill the Windows side, look inside the distribution |
| **U6** | Whether the pid-capture design of D6(a) survives a `wsl.exe` that exits before the pid file is written | The same machine, with the race run deliberately |
| **U7** | Whether `wsl.exe` propagates the Linux child's exit status unchanged | A Windows machine: `wsl.exe -d <d> -- sh -c 'exit 7'`; read the status |
| **U8** | What `wsl.exe` returns when the distribution does not exist, and when it exists but will not start | The same machine, both cases |
| **U9** | The encoding of `wsl.exe --list --quiet` on the runner's Windows build (UTF-16LE or UTF-8), and whether `WSL_UTF8=1` is honoured | The same run, or a Windows machine |
| **U10** | Whether `wslpath -a -u` round-trips a `\\wsl.localhost\…` UNC path and a path under a custom mount | A Windows machine with a non-default mount |
| **U11** | Whether `privateWorkingDirectory`'s Windows degradation (F12) is safe enough: `tmpdir()` on Windows is per-user, but nothing checks the ACL | A Windows machine, and an owner decision on whether an ACL check is owed |
| **U12** | Whether the console's own store's `0600` matters on NTFS at all (F45) | `SECURITY.md`'s existing answer, and the owner's view of whether a Windows-specific note is owed |
| **U13** | Whether the Windows console can *launch* — the window, the CSP, the picker — on a Windows host | The owner's manual checklist (§4 of [implementation-plan.md](implementation-plan.md)) |
| **U14** | Whether `watch --json`'s feed arrives intact through the wrapper (no NUL interleaving, no lost final line) | The fake in D11(a) for the shape, and a real run for the bytes |
| **U15** | Whether the 90-second read deadline is the right budget when a distribution is cold-starting (a first `wsl.exe` invocation can take seconds) | A Windows machine, measured |
| **U16** | Whether `wsl.exe`'s argv is visible in the Windows process table to *other* users, and whether that matters for the paths it carries | The owner's judgement; the argv carries paths and a project name, never a secret (F18, F22) |
| **U17** | Whether a distribution can be listed while it is stopped, and what `--list --quiet` says about a stopped one | A Windows machine with a stopped distribution |
| **U18** | Whether the SmartScreen warning's exact wording matches what D10(a) says | A Windows machine, downloading the first installer |
| **U19** | Whether `electron-builder`'s NSIS target needs a Windows-only dependency the lockfile does not carry | The first manual dispatch of `console-windows.yml` |

**What this repository *can* establish, and does**: the argv the console builds (unit tests, with the
fake), the environment the child receives (the stub's own report), the translation calls and their
arguments (the fake records them), the refusal paths and their sentences, the workflow's shape and
its pins, and the docs' consistency with the directory. Those are the plan's Tier-A tasks — the
whole of Parts A, C, D, E and F, and the parts of G and H that do not need a runner — and they are the reason the Windows leg is worth building before a Windows machine is in the room.
