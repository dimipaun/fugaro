# Fugaro Compatibility Matrix

Draft for the public site. Legend: ✅ Supported · 🔜 Coming soon · 🗺️ Roadmap · ❌ Not planned. **CE** = open-source core, **EE** = Enterprise Edition.

## Local environment (CLI)

| Platform | Status | Edition | Notes |
|---|---|---|---|
| macOS | ✅ | CE | |
| Linux | ✅ | CE | |
| Windows (WSL) | ✅ | CE | Via Windows Subsystem for Linux |
| Windows (native) | 🔜 | CE | Native port needed; WSL alone isn't enough |

## Console (Electron)

| Platform | Status | Edition | Notes |
|---|---|---|---|
| macOS | ✅ | CE | |
| Linux | ✅ | CE | |
| Windows | 🔜 | CE | No Windows build yet — to confirm |

The console runs on top of the CLI and inherits its identity; there is no separate console login.

## Mobile app (Expo)

| Platform | Status | Edition | Notes |
|---|---|---|---|
| iOS | 🔜 | CE | One global app published by Fugaro |
| Android | 🔜 | CE | |
| Web | 🔜 | CE | Comes from the same Expo codebase |

The app pairs with a project by scanning a QR code. Each pairing inherits the role of the user who generated it. One app can hold pairings for several projects.

## Agent harness

| Harness | Status | Notes |
|---|---|---|
| Claude Code | ✅ | Sole focus today |
| OpenAI Codex | 🔜 | |
| Others | ❌ | No current value in adding more |

## Model access

| Method | Status | Notes |
|---|---|---|
| OpenRouter API key | ✅ | Any model; used for the inner loop (coder and first reviewer) |
| Anthropic API key | ✅ | Senior and security reviewers, e.g. Opus |
| Claude OAuth token (bring your own credentials) | ✅ | One or more tokens per job; users must follow their provider's terms |
| Direct keys for OpenAI, DeepSeek and others | 🔜 | Avoids the OpenRouter markup |
| ChatGPT / Codex OAuth | 🔜 | Alongside Codex harness support |
| Vertex AI | 🗺️ | Natural fit for GCP-only; billed to the customer's GCP account |

Models are wired per role: a cheap model for the inner loop, frontier models for senior and security review.

## Cloud / sandbox

| Cloud | Status | Notes |
|---|---|---|
| Google Cloud (GCP) | ✅ | Deliberately GCP-only |
| AWS, Azure | ❌ | Not planned for now; would require replacing Firebase (RTDB etc.) |

**Positioning:** Fugaro runs in its own isolated GCP project, so it doesn't matter where your code or production lives.

## Execution backends

| Backend | Status | Notes |
|---|---|---|
| Cloud Run (gen 1) | ✅ | |
| Cloud Run (gen 2) | ✅ | |
| VM | ✅ | For heavier Docker workloads |

## Forges

| Forge | Status | Edition |
|---|---|---|
| GitHub Cloud | ✅ | CE |
| Bitbucket Cloud | ✅ | CE |
| GitLab Cloud | 🔜 | CE |
| Self-hosted (GitHub Enterprise Server, Bitbucket Data Center, GitLab self-managed) | 🗺️ | EE |

## Issue trackers

| Capability | Status | Notes |
|---|---|---|
| Read and update issues via MCP (GitHub Issues, Jira, Linear, etc.) | ✅ | Works with any tracker that has an MCP server |
| Native triggers (label or assign to a Fugaro bot to start a task) | 🔜 | Small webhook layer |

## Notifications

| Channel | Status | Notes |
|---|---|---|
| Mobile push | 🔜 | Primary channel for individuals |
| Slack, channel notifications | 🔜 | Incoming webhook |
| Microsoft Teams, channel notifications | 🔜 | |
| Slack / Teams interactive threads | 🗺️ | One thread per task; replies and buttons go back to the agent |
| Email | ❌ | |

## Identity and roles

| Capability | Status | Edition | Notes |
|---|---|---|---|
| Google identity via gcloud | ✅ | CE | CLI and console; no separate Fugaro login |
| QR pairing for mobile | ✅ | CE | Pairing carries the user's role |
| Roles: Admin, Developer, Viewer | 🔜 | CE | Custom IAM roles in the Fugaro project |
| Device management (list and revoke paired devices) | 🗺️ | EE | |
| Enterprise SSO | ✅ | EE | Through Google Workspace or workforce identity federation on GCP |
| Workspaces spanning several projects | 🗺️ | EE | Has its own Firebase project; separate discussion |

## Open questions

- Confirm whether Windows console builds exist.
- Current status of the mobile app, so the right status goes on the public page.
- Final name for the middle role: Developer or Operator.
