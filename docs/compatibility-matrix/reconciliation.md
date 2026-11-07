# Reconciliation: the compatibility matrix against the code

Checked against `origin/main` on **2026-11-07** (the CLI's latest tag is `v0.20.3`, the console's
`console-v0.120.0`; there is no mobile tag). Where this file disagrees with [spec.md](spec.md),
this file wins. Spec owner: Dimi Paun. Decisions **D1520–D1539** (a fresh block: D1500–D1519 are taken by the open `docs/security-classification` pull request, D1490–D1499 are reserved by `docs/console-managed-screens`; no D15xx exists on main). No
live check is taken: the page makes claims, it does not run anything.

## 1. What the spec is

A draft of a public page: a matrix of platforms, harnesses, model access, clouds, backends, forges,
trackers, notifications and identity, each row marked Supported, Coming soon, Roadmap or Not planned
with an edition. It was written without the repository, and its three own open questions (does a
Windows console build exist; the mobile app's status; the middle role's name) are answered below by
the code.

## 2. Findings

Each finding cites a path and symbol, not a line. **Wrong** = the spec contradicts the code;
**Gap** = the code forces a case the spec leaves out; **Rule** = a recorded ruling or licence
conflicts; **Exists** = the spec claims what is only partly there.

| # | Spec row | Class | What the repository says | Consequence for the page |
| --- | --- | --- | --- | --- |
| F1 | CLI macOS and Linux | Exists, true | `.goreleaser.yaml` builds `goos: [darwin, linux]`; releases from 0.13.0 are built with the `ee` tag (`LICENSING.md`, `ee/README.md`) | Supported; the binary carries `ee/` code beside the MIT core |
| F2 | CLI Windows (WSL) | Exists, true | `docs/windows-cli/wsl.md`: the Linux binary inside WSL 2 is the Linux path; caveats: no checkout under `/mnt/c`, gcloud credentials are a different file inside the distro, Claude Code must be installed there | Supported, with those three caveats in the notes |
| F3 | CLI Windows native "Coming soon" | Gap | `docs/windows-cli/README.md`: D1 both (WSL now, native behind it), only slices 0 and 1 approved (D16); `.goreleaser.yaml` has no windows build | Coming soon, no date; say "approved through the first slice" only if the owner wants the detail |
| F4 | Console macOS and Linux "CE" | Rule | `console/package.json` electron-builder: `mac` (universal) and `linux` (AppImage, deb). `console/LICENSE.md` is the **Subscription Edition** licence from console 0.113.0, and `mobile/LICENSE.md` likewise | Supported for both; edition is **Subscription**, not open-source core |
| F5 | Console Windows "to confirm" | Wrong as worded | no `win` target in electron-builder; `docs/console-windows` approves a Windows build that runs the Linux CLI through `wsl.exe`; the staged `docs/pending-workflows/console-windows.yml` is not in `.github/workflows` | Answers the spec's open question: **no Windows console build exists**; one is approved. Coming soon, no date |
| F6 | Console inherits the CLI identity | Exists, true | `docs/design/desktop-console.md`: the console spawns allow-listed `fugaro ... --json` commands under the operator's own Application Default Credentials; `docs/design/roles.md` D691: the console has no sign-in | Keep as written |
| F7 | Mobile iOS, Android, **Web** | Wrong | `mobile/app.json` has only `ios` and `android`; `mobile/package.json` has no web script and no `react-native-web` | **Remove the Web row.** It is not a status question: there is no web build |
| F8 | Mobile "one global app" | Gap | `mobile/eas.json` has a store-distribution profile and `mobile-release.yml` submits to internal testing, but no `mobile-v*` tag exists, `mobile/package.json` is `0.0.0`, and `docs/subscriptions/status-2026-11-07.md` says the app is unreleased. Answers the spec's open question about the app's status | Coming soon for iOS and Android; the "one app published by Fugaro" sentence is intent, so it says "planned" |
| F9 | QR pairing "inherits the role" | Wrong | `internal/cli/pair.go` writes a pairing with `--role` defaulting to the issuer's role and only lowering it; the QR carries the project config and a one-shot claim, not a role (`internal/mobileauth`, `pairingClaim` in the rules). The role is recorded on the device node and **not enforced** by the security rules (`docs/design/roles.md` §1.1, D225). The app-side scanner is not in `mobile/src` and the app has no camera dependency. Several projects in one app: ruled (D114), not built | Pairing: CLI side Supported; app scanner Coming soon; worded "records a role, not yet enforced by the rules"; multi-project is not claimed |
| F10 | Agent harness: Claude Code | Exists, true | `internal/agent` runs `claude -p` only | Supported |
| F11 | Codex "Coming soon" | Gap | no Codex code; only a non-goal in `docs/design/m10-multi-model.md` and a reserved `codex-oauth-token` naming in `docs/design/project-named-oauth-tokens.md` | Roadmap |
| F12 | OpenRouter "any model" | Wrong as worded | `internal/config/providers.go` entries are `kind: anthropic-compat` with `models:` patterns, a price and a route fee; `routing.claude.via` keeps Claude IDs direct unless the operator opts in; pins, prices and a budget cap are required in enforce mode (`docs/multi-model.md`) | Supported, worded "OpenRouter or any Anthropic-compatible endpoint, for the coder and first-line reviewer"; no "any model" |
| F13 | Anthropic API key for the senior reviewer | Exists, partly | in-job path A in `docs/design/mixed-credentials.md` (section 1 says it "works today"; section 3 is the Path A heading); its live check (Check 330) has not run; the cloud review job is refused (`reviewJobAdmitted` is a package variable set to `false` in `internal/cli/review.go`); `docs/api-key-review` AK4–AK7 are best effort | **Built, not enabled** (the new status, D1520) |
| F14 | Claude OAuth tokens | Exists, partly | `internal/config/claude_accounts.go`, `fugaro accounts add`, stage accounts in `internal/infra/spec.go`; Check 290 passed on 0.20.3 (the OAuth in-job review is the proven path); automatic failover `claude_account_auto` is dormant until the owner confirms the provider's terms (`docs/accounts-add`, D875) | Supported, worded "several named accounts per project, chosen per profile or stage"; failover is not claimed; there is **no Terms-page sentence about Claude accounts** (the subscription terms mention neither Claude nor Anthropic; the only wording is the internal D875 text in `docs/accounts-add/spec.md`), so the row carries no terms caveat and the question goes to the owner and counsel (D1539, amended after review) |
| F15 | Direct keys for OpenAI, DeepSeek "Coming soon" | Wrong as worded | providers are user-named `anthropic-compat` entries: a DeepSeek entry works today (`docs/multi-model.md`); there is no native OpenAI, Gemini or Bedrock kind | Split: Anthropic-compatible endpoints (DeepSeek and others) **Supported**; native OpenAI **Roadmap** |
| F16 | ChatGPT / Codex OAuth | Gap | only the `codex-oauth-token` naming reservation | Roadmap |
| F17 | Vertex AI "Roadmap" | **Wrong** (found by the review round) | **Vertex is the default**: `DefaultAgentAuth = "vertex"` and `AgentAuths` in `internal/config/defaults.go`; `internal/agent/env.go` passes the Vertex environment for `auth: vertex`; `docs/design/v1.md` says "Vertex AI (default) via the job's service account". The same page states that budget `enforce` with Vertex is refused until Check 20's Vertex FACTs are recorded (`docs/design/v1.md` section on the gateway) | **Supported** for Claude on Vertex, billed to the project's own GCP account; the note says that a dollar cap (budget `enforce`) on Vertex waits on Check 20, so the dollar-cap part is **Built, not enabled**, and the note says **observe-only** (the code's own term in `docs/design/v1.md`). Amends D1527 (below) |
| F18 | Cloud: GCP only; AWS/Azure "Not planned" | Gap | `internal/backend` holds Cloud Run and Cloud Batch only; no ruling says AWS or Azure never happen | GCP Supported; AWS and Azure say "no plan", not a ruling |
| F19 | Backends: Cloud Run gen 1 and gen 2, VM | Wrong | Terraform sets `EXECUTION_ENVIRONMENT_GEN2` on the jobs and no gen 1 exists; `internal/backend/batch` exists but `backend: cloud-batch` is still refused by `internal/localcfg` validation (`docs/design/compute-backend.md`) | Remove gen 1; Cloud Run jobs (gen2) Supported; VM / Batch **Built, not enabled** |
| F20 | Forges: GitHub and Bitbucket Cloud | Exists, true | `internal/gitprov/{github,bitbucket}` | Supported, core |
| F21 | GitLab Cloud "Coming soon" | Gap | GitLab appears only in test fixtures | Roadmap |
| F22 | Self-hosted forges (EE, Roadmap) | Rule | `docs/bitbucket-workspace-token`: Bitbucket Data Center is ruled out (Cloud-compatible API roots only); no GitHub Enterprise Server trace | Bitbucket Data Center **Not planned**; GitHub Enterprise Server and GitLab self-managed Roadmap; the edition cell follows the Subscription rename, not "EE" |
| F23 | Trackers "via MCP" Supported | Wrong as worded | Fugaro gives a run's agent no tracker MCP: no `mcp` key in `fugaro.yaml`, review stages strip any MCP configuration (`internal/runner/reviewmode.go`); Fugaro's own hosted MCP server (`docs/mcp-server`) is a different thing and dormant | Reworded (D1530): the user's own agent reads the tracker through its own MCP servers and hands the task to Fugaro (Supported); tracker access inside a run Roadmap |
| F24 | Native tracker triggers "Coming soon" | Gap | no trace | Roadmap |
| F25 | Notifications | Wrong | `docs/mobile/design/push-notifications.md` is design only (no sender, no token store, no `expo-notifications`); no Slack, Teams or email code | Mobile push, Slack, Teams and threads Roadmap; email Not planned |
| F26 | Google identity via gcloud | Exists, true | `docs/design/desktop-console.md`; the phone signs in with an account on the phone (`docs/mobile-auth/spec.md`), not gcloud | Supported for the CLI and console; the phone row says so |
| F27 | Roles "Admin, Developer, Viewer" | Rule | the rulings and code say Viewer / Operator / Admin (`internal/mobileauth` role constants, `docs/design/roles.md`); "Developer" appears nowhere; enforcement is design (`docs/design/roles.md` "design, no code"; D446) | Answers the spec's third open question: **Operator**. Row: Coming soon |
| F28 | Device management Roadmap, EE | Exists | `fugaro pair ls` and `fugaro pair revoke` exist in the core CLI | Supported (CLI); app and console management Roadmap |
| F29 | Enterprise SSO Supported | Wrong | no SSO or workforce-federation code or doc; identity is Google plus email or domain allowlists (`docs/mobile-auth/spec.md`) | Replace with "Google identity with email or domain allowlists" Supported; SSO and federation Roadmap |
| F30 | Workspaces Roadmap, EE | Exists | `docs/workspaces`: design answered; W0 (#1824) and W1 (#1840, the local layer and `fugaro workspace` reads) are merged, W2 is a draft pull request, no workspace service code exists under `ee/`; services land under `ee/` | Roadmap, Subscription |
| F31 | Editions CE and EE | Rule | counsel renamed Enterprise to Subscription Edition on 2026-10-29 (memory: `subscription-edition-licence-rename`; `LICENSING.md`); the console and mobile are under the Subscription licence from console 0.113.0; `ee/` holds `clidoc`, `coordinator`, `governance`, `graph`, `plugincheck`, `register`, `report` | Editions are **Core (MIT)** and **Subscription**; the console and mobile are Subscription |

**F32 (Missing, found by the review round).** Spec sentences with no row: "Others: Not planned" (harnesses) is kept as Not planned, "Models are wired per role" is true (`docs/multi-model.md`) and stays as one prose line, and the cloud "Positioning" paragraph is kept as written (the page says nothing a run's isolation does not back: `docs/design/v1.md`). No decision needed.

### Concept to what exists

| Spec concept | What the repository has |
| --- | --- |
| A public page of claims | `website/data/*.yaml` rendered by `website/src/pages/*.astro`; `website/data/mobile-claims.yaml` is the precedent for a claims file |
| "Supported" | no status vocabulary in the repo; the website has none for platforms |
| Stamp of "as of" | `website/data/release-summaries.yaml` and `docs/releases/vX.Y.Z.md` per release |
| Keeping it true | `website/test/*.mjs` pins and `scripts/release.sh`'s preconditions |

## 3. Decisions

All answered by the owner on **2026-11-07** through AskUserQuestion, **as recommended** (no answer
deviates, so there is no open tension).

| # | Question | Recommendation | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| D1520 | A status for rows built but not enabled or not yet verified? | **Yes: "Built, not enabled"** as a fourth status beside Supported, Coming soon, Roadmap and Not planned (F13, F19) | no new status: those rows go to Coming soon, which understates the Anthropic-key review and the Batch backend | **Recommended** — 2026-11-07, keeps the page honest about what exists |
| D1521 | The mobile Web row | **Remove it** (F7); there is no web build | keep it as Roadmap, claiming a plan nobody has made | **Recommended** — 2026-11-07 |
| D1522 | Edition vocabulary | **"Core (MIT)" and "Subscription"**, console and mobile Subscription (F4, F31) | keep CE/EE: stale against counsel's rename and wrong for the console | **Recommended** — 2026-11-07 |
| D1523 | Windows CLI and console | **Both Coming soon, no date**; the WSL row stays Supported with its caveats (F2, F3, F5) | Roadmap for the console: understates an approved build | **Recommended** — 2026-11-07 |
| D1524 | Codex | **Roadmap** (F11): there is no code | Coming soon: promises work nobody scheduled | **Recommended** — 2026-11-07 |
| D1525 | Model-access wording | **Rewrite to the code** (F12–F14): no "any model"; Anthropic key Built, not enabled; OAuth Supported without failover | keep the spec's wording | **Recommended** — 2026-11-07 |
| D1526 | Direct provider keys | **Split** (F15): Anthropic-compatible endpoints Supported, native OpenAI Roadmap | one row Coming soon | **Recommended** — 2026-11-07 |
| D1527 | ChatGPT/Codex OAuth and Vertex | **ChatGPT/Codex OAuth Roadmap (F16). Vertex Supported** with the dollar-cap caveat (F17) | Vertex as Roadmap: understates the default route | **Recommended for ChatGPT/Codex OAuth — 2026-11-07. Vertex: AMENDED AFTER REVIEW.** The owner answered "Roadmap" on the premise that no Vertex run path exists; the code says Vertex is the default auth (F17). Intent kept (claim only what the code proves); the status follows the code. **The owner confirmed the amended text on 2026-11-07** ("Vertex confirmed as amended") |
| D1528 | Backends | **Remove gen 1**; Cloud Run (gen2) Supported; VM / Batch Built, not enabled (F19) | keep the spec's rows | **Recommended** — 2026-11-07 |
| D1529 | Forges | **GitLab Roadmap; Bitbucket Data Center Not planned; GHES and GitLab self-managed Roadmap** (F21, F22) | keep Coming soon for GitLab | **Recommended** — 2026-11-07 |
| D1530 | Tracker row | **Reworded** to the user's own agent reading the tracker and handing the task to Fugaro (F23); in-run tracker access and triggers Roadmap (F24) | keep "via MCP" Supported: false for runs | **Recommended** — 2026-11-07 |
| D1531 | Notifications | **All Roadmap except email Not planned** (F25) | keep Coming soon for push and webhooks | **Recommended** — 2026-11-07 |
| D1532 | The SSO row | **Replace with Google identity plus email or domain allowlists** (Supported) and SSO or federation Roadmap (F29) | keep "Enterprise SSO Supported": no such feature | **Recommended** — 2026-11-07 |
| D1533 | QR pairing wording | **CLI Supported, app scanner Coming soon, "records a role, not yet enforced by the rules"** (F9) | keep "inherits the role": wrong on both counts | **Recommended** — 2026-11-07 |
| D1534 | The middle role's name | **Operator** (F27): what the CLI, console and docs say | Developer: a permanent mapping gap | **Recommended** — 2026-11-07, answers the spec's open question |
| D1535 | Device management | **Supported (CLI)**, app and console management Roadmap (F28) | keep Roadmap, EE | **Recommended** — 2026-11-07 |
| D1536 | Workspaces | **Roadmap, Subscription** (F30) | keep as drafted with "EE" | **Recommended** — 2026-11-07 |
| D1537 | Where it lives and how it is kept true | **`website/data/compatibility.yaml` rendered into one page, with a website test that every Supported or Built row cites an evidence path that exists, and that the "as of" stamp matches the latest release** | a static page: drifts silently | **Recommended** — 2026-11-07 |
| D1538 | The "as of" stamp | **A CLI and console version stamp refreshed by the release procedure** | no stamp | **Recommended** — 2026-11-07 |
| D1539 | The Claude subscription-terms caveat | **None on the page.** The owner's answer assumed a sentence already on the Terms page; none exists (`website/content/subscription-terms.md` and `docs/licensing/subscription-terms.md` name neither Claude nor Anthropic). The Claude OAuth row says only what the code does, and the caveat becomes an owner and counsel item | write a new caveat on the page: counsel's, not this plan's | **AMENDED AFTER REVIEW.** The owner answered "only the sentence already on the Terms page" (2026-11-07, recommended) on a premise the repository does not support. Intent kept (no new legal sentence from this plan); the consequence is that the row carries no caveat. **Needs the owner's confirmation (open item 4)** |

## 4. Open tensions and open items for the owner

None between answers. Open items (D1527's amended Vertex answer was confirmed by the owner on 2026-11-07; **item 4: D1539's amended answer, which the owner has not seen**): (a) the page's final copy of each row's note is the build task's
to draft and the owner's to read before the website promote, which the release procedure performs
(the required reviewer was removed from the GitHub environments on 2026-11-07; `docs/release.md` still says otherwise until the stale-docs task DOC1 lands); (b) **(2) the navigation change needs the owner's explicit approval**, because `.claude/rules/website.md` keeps nav labels and URLs unless the owner approves a change: the build task proposes the entry (the "Project" group in `website/src/layouts/Base.astro` is the candidate) and does not add it before the owner says yes.

## 5. UNVERIFIED

- Whether a repository's own `.mcp.json` loads in an implement stage was **not tested**, so the page
  makes no claim about it.
- Vertex's real-world operation (a run on Vertex) was not exercised here: F17 rests on the code defaults and `docs/design/v1.md`, and Check 20's FACTs are not recorded.
- The mobile app's store state beyond "no tag and no release note" is taken from
  `docs/subscriptions/status-2026-11-07.md`; no store was inspected.
- Findings F1–F32 were produced by a read-only reader of `origin/main` and are re-verified by the
  review round before this PR leaves draft.
