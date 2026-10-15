<!-- The owner's draft spec (Dimi Paun, October 2026), kept verbatim as the source of docs/design/project-centric.md. -->

# Fugaro — Project-Centric Redesign Spec

**Status:** Draft · October 2026
**Author:** Dimi Paun

## 1. Summary

Fugaro started repo-centric. Project-level and layered configuration were added later, but much repo-centric behavior remains, and it makes Fugaro hard to use beyond small setups. One of Fugaro's core benefits, beyond scalability, is that policy can be set and enforced at the project level. This spec makes the **project** the primary unit for configuration, policy, credentials, and setup. Repos become lightweight members of a project that inherit everything from it.

## 2. Motivating cases

| Case | Why repo-centric fails |
|---|---|
| **Monorepo** (one repo holds everything) | Setting caps "per repo" under a project is redundant and confusing. |
| **Microservices** (50–60 repos per project) | Per-repo caps, keys, and init are unmanageable; nobody will run a heavy setup 50 times. |

## 3. Goals

1. Project-level policy and configuration as the default and primary model.
2. No heavy per-repo initialization.
3. Credentials configured once, at the project or individual level.
4. Durable, versioned history of project state.

**Non-goal:** preserving repo-level overrides. They are negotiable and may be dropped entirely if that is what it takes to remove per-repo provisioning.

## 4. Requirements

### 4.1 Spending caps at the project level

- Overall spending cap for the project.
- Default per-repo cap, **defined at the project level**, applying to every repo in the project.
- Optional per-repo override (nice-to-have; may be dropped).
- No requirement to touch individual repos to set or change caps.

### 4.2 Remove heavy per-repo init

- `fugaro init repo` currently runs Terraform and asks for change confirmations. This does not scale and kills the many-repo use case.
- All heavy provisioning happens **once**, at project init.
- Adding a repo to a project must be lightweight: no Terraform, no confirmations. Ideally repos tied to the project simply work.
- **Hard requirement:** if keeping repo-level overrides means keeping per-repo Terraform, drop the overrides.

### 4.3 API keys (Anthropic, OpenRouter)

- Keys are configured at the project level and inherited by all repos.
- **Neither key is mandatory.** Today both must be set, even with dummy data for Anthropic; that requirement goes away.
- **Decouple model choice from key routing.** The user picks the model and, separately, the provider/key it runs through. Example: senior review on Sonnet or Opus routed through the OpenRouter key.
- Sensible default: Anthropic models go direct when an Anthropic key exists; otherwise they route via OpenRouter. Users can choose a single OpenRouter key for everything, accepting the small markup for the convenience of one key.

### 4.4 Claude OAuth tokens

- Tokens identify an individual, so ownership is **personal**.
- Named tokens support several accounts per person, with failover when one account runs out of quota.
- Tokens may be registered at the **individual** or **project** level, **never at the repo level**.
- A project-level token is a **reference to an individual's token** that the owner has chosen to make available to that project. Ownership stays personal while usage is shared.

### 4.5 Project backing Git repo

Project state currently lives only in the Firebase project, with no history.

- Allow associating a Git repo with a project, as **backing store**, not as a unit of execution.
- Add Fugaro commands to export project state from Firebase into the repo and commit it:
  - project settings and policies (caps, routing, configuration)
  - periodic spending summaries
- Benefits:
  - **History and audit:** see how policy and spend changed over time.
  - **Survival:** the repo outlives the Firebase project and holds enough to reconstruct or account for it.
- **Secrets** (API keys, OAuth tokens) must be excluded or encrypted, never committed in plaintext.

## 5. Inheritance model (target)

```
Individual  ──(shares OAuth tokens by reference)──┐
                                                  ▼
Project   ← caps, default per-repo caps, API keys, routing, OAuth pool, backing repo
   │
   └── Repos ← inherit everything; no per-repo init; (optional) minimal overrides
```

## 6. Forward-looking: Fugaro desktop console

- Cross-platform desktop app built with **Electron**.
- Essentially a richer, graphical **Fugaro Watch**:
  - live task monitoring
  - spending monitoring with graphs
  - project-level views, consistent with the project-centric model
- **Visual inspiration:** the task manager at [tmog.org](https://tmog.org): dense, information-rich, attractive live graphics.

## 7. Open questions

1. Are repo-level overrides kept at all, and if so, for which settings (caps only)?
2. How is a repo "tied" to a project with no init step: explicit registration, discovery from the Git org, or first-use enrollment?
3. Migration path for existing projects that already ran per-repo init.
4. Export cadence for the backing repo: manual command, scheduled, or both?
5. Encryption approach for any secrets material that must live in the backing repo.
