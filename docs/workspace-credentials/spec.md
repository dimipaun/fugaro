# Workspace credentials: a person's tokens and the shared keys, held once

**Status: a draft for the owner's ruling. Nothing here is decided and no task exists.** Written on
2026-11-18 at the owner's request, against `origin/main` at `e2ec97c1a`. It is an addendum to
[the workspaces design](../workspaces/README.md) and to
[workspace projects and users](../workspace-projects-users/README.md). Once the decisions in §4 are
answered, the reconciliation and the plan follow in this folder.

**Numbers reserved:** decisions **D2930–D2939** (D2930–D2937 used). Read on 2026-11-18: no file on
`main` and no open pull request uses a number of this block; D2900–D2929 and D2940–D2959 are taken.

## 1. The ask

The owner, 2026-11-18:

> Wouldn't be easier for ppl to store their oauth tokens at the workspace level so they can use them
> in every project? Ditto for the openrouter and anthropic key...

## 2. What exists

- **A workspace already holds shared model credentials.** One bundle and one credentials service,
  in the workspace's own project (D1297). `fugaro creds set`, run with the workspace owner's config
  selected, writes that bundle (`openBundleStore`, `internal/cli/creds.go`). It takes
  `claude-oauth-token`, `claude-oauth-token-<account>`, `anthropic-api-key` and `provider-key`.
- **Each shared credential names the members it serves.** The owner's shared list carries a
  per-project allow-list, and the service intersects it with the member's own policy and the run's
  repository (D1298, `WorkspaceShared` in `internal/credsvc/bundle_workspace.go`). There is no
  "every member" value.
- **Only the workspace owner can write the bundle.** A member's person has no way to put a token
  there: the write needs a credential for the workspace's project.
- **A person's own token is per project.** It lives in each project's credentials service under the
  person's prefix (`fugaro_u_<id>_<name>`, the dynamic-delivery intake), and offboarding sweeps it
  project by project (workspace-projects-users, "Offboarding"; task WU9).
- **An OpenRouter key is not in any bundle.** `fugaro creds set` refuses a declared credential of
  type `openrouter`: the runner path serves it, and its key is mounted in the job that spends it.
  So today it cannot be a workspace credential at all.
- **A project in no workspace** has only its own store.

## 3. The proposal

When a project is in a workspace, the workspace is where a credential is stored by default, once.
The project's own store stays for a project in no workspace, and as a tighter override.

1. **A person's Claude token is stored once, at the workspace's service**, under the person's own
   prefix, through the same intake a project has today. It serves the member projects that person
   holds a role in, and by default only the runs that person launched.
2. **A shared key can be offered to every member** with one explicit value in its allow-list. Each
   member's own policy still decides whether it takes it.
3. **An OpenRouter key becomes shareable** by being served through the workspace's service rather
   than mounted in each job.
4. **Offboarding removes a person's tokens in one place**, not once per member.

The two rules of the workspaces design hold as written. A member's own policy still narrows
everything the workspace offers. No run, CLI or person is ever handed a credential's value.

## 4. Decisions for the owner

The first option of each is the recommendation.

| # | Question | Options | Why the first |
| --- | --- | --- | --- |
| **D2930** | Where does a person's Claude token live when their project is in a workspace? | **(a) Once, at the workspace's service**, under the person's prefix; the per-project copy stays for a project in no workspace. (b) Per project as today, with a command that writes it to several projects at once | One place to add, rotate and revoke; (b) leaves N copies and N sweeps |
| **D2931** | Which members does a personal token serve? | **(a) Every member the person holds a role in**, read from the workspace's roster, intersected with each member's policy. (b) A list the person writes per token | The roster already says where the person works; a hand-written list drifts from it. This amends D1298 for personal tokens only: the allow-list is derived, not written |
| **D2932** | Whose runs may draw on a personal token? | **(a) Only runs its owner launched**; sharing with named people or with a whole member is an opt-in per token. (b) Any run in the members it serves | A subscription is one person's; (b) lets a colleague's runs spend it in a project its owner never opened |
| **D2933** | May a shared key be offered to every member at once? | **(a) Yes, with an explicit `*` in its allow-list**, covering members that join later, still intersected with each member's policy. (b) No, each member is named, as today | The member's policy is the fence either way; naming each member is the step people forget. This amends D1298's "available to every member would be a policy bypass" by keeping the member's refusal in force |
| **D2934** | Does an OpenRouter key move to the workspace's service? | **(a) Yes: served through the service's provider route** when the project is in a workspace, so it is shared and counted by the workspace cap; the job-mounted key stays for a project in no workspace. (b) It stays per project | Without it the ask's "ditto" is not met for OpenRouter. It is the largest piece here, and U2 has to be settled first |
| **D2935** | Does a personal token's use count against the workspace cap? | **(a) No**: it is unbilled, and it is counted in the notional figures as an OAuth run is today. (b) Yes | The cap is the ceiling on what the workspace's own credentials pay for |
| **D2936** | Is a project in no workspace in scope? | **(a) No**: nothing changes for it. (b) Yes: `fugaro accounts add` learns to write several projects at once | Keeps this addendum to one mechanism; (b) can be its own small spec |
| **D2937** | Where does the work go? | **(a) A slice of workspace-projects-users, after WU0**, with the threat model extended by one case: the workspace's service holding people's own tokens. (b) Its own plan, independent of WU0 | It changes WU9's offboarding and reads the same roster; WU0's review (#3213) is open now |

## 5. What it costs

- **The workspace's service holds more.** A compromise of it reaches every person's token, not one
  project's. D1297 accepted that for the shared keys; this extends it to personal tokens, which is
  why D2937 sends it through the threat model.
- **A second intake.** The workspace's deployment needs the person-prefix intake a project has, and
  a way to admit a member's person to it. Today the workspace's service admits only the workspace
  owner's people (workspaces spec §4).
- **One subscription, more runs.** A token that serves several projects reaches its rate window
  sooner. The run then waits or fails over exactly as it does today; the pool it fails over within
  becomes the workspace's.

## 6. Unverified

- **U1.** Whether the workspace's service has the person-prefix intake at all. Nothing in
  `internal/credsvc/dynamicsecrets.go` names a workspace; not read further.
- **U2.** Whether the service's provider route (`provider-key`, tier C) can serve OpenRouter as it
  stands, or only the provider it was built for.
- **U3.** Whether the launcher of a run is available to the workspace's service at selection time in
  a form it can trust, which D2932 (a) needs.
- **U4.** How the owner's shared list is written today, and so what D2933's `*` changes in the CLI.
