# The screenshot fixtures — every value invented

These are the documents the owner's one-command screenshot run
(`scripts/console-screenshots.sh`) feeds the **built** console through a **fake**
`fugaro`, so the website's Console page can carry pictures of the real
application showing plausible figures rather than a real installation's.

**Nothing here names a real repository, person, GCP project, account or
figure.** The project names (`acme-platform`, `acme-web`, `acme-data`), the
repositories (`acme/platform-core`, `acme/platform-cli`), every run id, title,
model pair, dollar figure and URL is invented for the pictures. The `.invalid`
and `.example` hosts are the reserved ones, so no link here can resolve to
anything. This directory is data, not a recording: it is written by hand, it was
never read from an installation, and `scripts/console-screenshots.sh` refuses to
run against anything but this CLI.

| File | What it is | Where its shape comes from |
|---|---|---|
| `live.json` | the `fugaro watch --json` feed: **six frames** under `frames`, one compact document each, 30 seconds apart — three runs in flight and two just finished across two repositories, with a burn rate that moves so the sparkline is a curve and not one bar. The Live screen draws `runs`, `repos` and `total`; `finished[]` is decoded and not drawn, so the picture's alt text does not count it | `internal/watch/json.go`, read back by `console/src/shared/schema.ts` |
| `budget.json` | one `fugaro budget show --json` document: the caps panel and the rail's spend | `internal/cli/budget.go`'s `show` |
| `report.json` | one `fugaro report --json` document: 30 days by day, with a believable curve | `internal/cli/report.go`, `console/docs/json-contract.md` §4 |
| `diagnose.json` | one `fugaro diagnose RUN --json` document: the row, its stages, its findings, its splits | `internal/runview/runview.go` plus the diagnosis document |
| `logs.ndjson` | the `fugaro logs RUN --json` window: one compact `logLine` per line | `console/docs/json-contract.md` §6.2 |
| `log-url.txt` | the one line `fugaro logs RUN --url` prints | the same |
| `installations.seed.json` | the three installations the picker draws — **id and project only**, because the binary path is the fake the run itself installs in its own scratch tree, which is a different path on every run | `console/src/main/installations.ts` |

The feed's shape is the **e2e suite's** own (`../synthetic.ndjson`,
`../normal.ndjson` are the same seven keys), and the tool reuses that suite's
code rather than a copy of it: `console/test/support/launch.ts` is how the
window is opened, and the store the console reads is the console's own
(`console/src/main/installations.ts`). What this directory adds is only the
*pictures*: the
recordings beside it are redacted, and a redacted document draws `<project>`
where the picture wants a name.

`console/tools/screenshots/fixture.ts` is what turns these files into a CLI: it
installs the unit suite's fake child (`console/test/unit/stub.ts`) with these
documents as its answers — `watch` from `live.json`'s frames, `budget show`,
`report`, `diagnose`, `logs` and `logs --url`, a version line inside the range
the console was built against, and a **refusal** (exit 2) for anything else — and
`installScreenshotFixture` writes the ids and projects above into the console's
own installation store, so the console under the camera talks to nothing but
this directory. `console/test/unit/screenshots.test.ts` reads these documents
through the console's own readers; see `console/README.md` for the one command
that takes the pictures.
