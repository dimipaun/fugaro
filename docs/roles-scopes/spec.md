# Fugaro User Roles and Scopes Spec

## Summary

Fugaro has three roles: **viewer**, **operator** and **admin**. Each role is always granted together with a **scope**: the whole project, all repos, or specific repos. A user can hold several role and scope pairs. In any place, the highest role that applies there counts.

## Roles

### Viewer

- Read-only. Can see running and past jobs, logs, and the current state.
- Can't launch jobs or change anything.
- **Used for lapsed subscriptions:** when a subscription lapses, users drop to viewer. They keep access to their history, and nothing new costs money.

### Operator

- The everyday developer role. Can launch and manage jobs and do normal work.
- Can't change settings, overrides, or images.
- **The default role** for new users added to a project.

### Admin

- Can manage everything within its scope: settings, overrides, images, and the other resources.
- With project scope, admin also sets up the project and manages the project-level settings and guardrails.

## Scopes

Every role is granted with one of these scopes:

| Scope | Covers | Project-level settings |
|---|---|---|
| **Project** | The project and every repo in it | Yes |
| **All repos** | Every repo, including repos added later | No |
| **Specific repos** | Only the listed repos | No |

The difference between project scope and all-repos scope mostly matters for admin. An all-repos admin can manage every repo's overrides and images but can't change the project guardrails.

## How it fits together

- A user can have more than one role and scope pair, for example operator on the project and admin on one repo.
- **The highest role wins:** in any repo, the user's permission is the highest role whose scope covers that repo. Project-level actions need a role with project scope.
- Repo admins can change repo settings only within the guardrails set at project level.
- **Resources follow their scope:** an image tied to a repo is managed by an admin of that repo. A project-wide image is managed by a project admin.

## Examples

- **Team lead:** operator on the project, admin on their team's repos. Manages those repos' overrides and images without touching the project guardrails.
- **Contractor:** operator on one specific repo only.
- **Auditor:** viewer on the project.
- **Platform lead:** admin on all repos, without project-level control.

## Managing roles and scopes

Roles and scopes are managed only through Fugaro commands, never by editing files by hand, just like other settings.

- A user role command grants, changes or removes a role for a user, with the scope given as an option, for example project, all repos, or a list of repos.
- A list command shows each user's role and scope pairs, and the role they effectively have in a given repo.
- Commands check their input: the user exists, the repos exist, and the caller is allowed to grant that role in that scope.
- Only a project admin can grant roles with project or all-repos scope. A repo admin can grant roles up to admin, but only for their own repos.
- Changes take effect right away, with no prompts and no follow-up steps (see the Composite Management Commands Spec).
- The console uses the same commands.

**Scope values:** `project`, `all-repos`, or a comma-separated list of repo names, for example `frontend,api`. The names `project` and `all-repos` are reserved, and Fugaro refuses to create or register a repo with either name.

**Wildcards:** repo names in a scope can use simple glob-style `*` patterns, for example `dimipaun/*` or `acme/api-*`. Only plain stars are supported, not regular expressions. A wildcard scope automatically covers new repos that match it, just like `all-repos`. The list command shows which repos each pattern matches right now.

## Fits with other specs

- Management commands (see the Composite Management Commands Spec) check the caller's role for the scope they're changing before applying the change.
- No confirmation prompts. Permission checks either allow the action or fail clearly (see the No-Prompt Policy Spec).
- A permission failure explains which role and scope the action needs.

## Acceptance criteria

- Role assignments are stored and checked as role and scope pairs.
- An operator with project scope who is admin on repo A can change repo A's settings and images, but not repo B's or the project's.
- An all-repos admin is automatically admin on newly created repos.
- When a subscription lapses, all users are limited to viewer, and their assignments come back when the subscription resumes.
