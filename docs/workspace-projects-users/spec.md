# Fugaro Workspace Spec — Addendum: Project Management and Users

This addendum extends the workspace spec with two areas: commands for managing the projects in a workspace, and a model for managing users and roles across a workspace.

## Background

- A workspace is made up of projects.
- Each Fugaro project has its own Firebase project, which is also a GCP project with the same name.
- Each workspace also has its own Firebase/GCP project.
- GCP has no idea what a workspace is. To GCP, every one of these is an independent project.
- Users today are authenticated directly as GCP identities (Google accounts) through IAM. The Firebase identity providers (IdPs) in workspace and project Firebase projects are not used.
- Projects already have three roles: admin, operator and viewer.

## Part 1: Project management commands

Workspaces need CRUD-style commands for their projects:

- `fugaro project add <project>`: attaches an existing Fugaro project to the workspace and sets up the access-control link (see Part 2).
- `fugaro project ls`: lists the projects in the workspace with a short health summary.
- `fugaro project rm <project>`: detaches a project from the workspace.
- `fugaro project status [<project>]`: runs a doctor on one project or all of them.

### `rm` detaches and never deletes

- `rm` only removes the project from the workspace. It never deletes GCP or Firebase resources.
- Before detaching, it offers to save the project's state (settings, spending summaries) to the project's associated Git repo.
- It warns that anything still running, such as the Cloud Scheduler → Cloud Build image rebuilds or the credentials security service, keeps costing money until the project is shut down.
- It prints a link to the GCP console page for shutting the project down, so that real deletion goes through Google's own flow and safeguards.
- It removes the workspace-managed IAM bindings, or the service-account delegation, from the project.

### `status`: the project doctor

For each project it reports pass, warn or fail, with a hint on how to fix anything that isn't passing:

- The Firebase/GCP project is reachable.
- The credentials security service responds.
- The model keys and named OAuth tokens it references are valid.
- The images are fresh, meaning the scheduled rebuilds are succeeding.
- Spending caps are set, along with current use against the caps.
- The project's state repo is in sync.
- The project's actual IAM bindings match the workspace's user and role list (drift detection, see Part 2).

## Part 2: User management

### Principles

- **Identity is a Google account.** Users are GCP/IAM principals. The Firebase IdPs in workspace and project Firebase projects stay unused.
- **The workspace is the single source of truth** for who has access to which project and with which role (admin, operator, viewer).
- **Fugaro applies that truth to GCP.** It translates workspace role assignments into IAM bindings on each project. Users are never managed by hand, one project at a time.

### Out of scope: the mobile app

The mobile app is a single global app backed by the fugaro-prod Firebase project. It cannot know about the Firebase projects that each customer creates for its workspaces and projects, so it never uses their IdPs. Any app sign-in goes through the fugaro-prod IdP. QR pairing links the app to a particular workspace or project, and pairings are cheap to redo. The app's identity model is therefore separate from this addendum. Signing in through fugaro-prod might later be used to remember pairings, but that is secondary.

### How the workspace gets authority over projects

Workspace projects and member projects are independent in GCP, so the workspace needs actual IAM authority on each member project. There are two mechanisms, and Fugaro picks between them automatically:

1. **GCP folder (preferred, for companies with a GCP organization).** A GCP organization is tied to a company domain through Google Workspace or Cloud Identity. Folders group projects inside it, and IAM roles granted on a folder are inherited by every project in it, including ones added later. The workspace maps to a folder, and `project add` places the project in it. Workspace-wide roles are granted on the folder, and project-specific roles on the project.
2. **Service-account delegation (fallback, when there's no organization).** On `project add`, the project's owner grants a workspace service account permission to manage IAM on that project. From then on, Fugaro manages that project's bindings through this account.

`fugaro workspace init` detects whether an organization is available and sets up the right mechanism. Workspaces are mainly a team/enterprise feature, so most of them are expected to use folders.

### Membership rules

- Once a project belongs to a workspace, only workspace members can hold roles on it.
- Only workspace admins can add new people to the workspace.
- Project admins can assign and change project roles, but only for existing workspace members.
- Workspace admin is the only workspace-level role. A workspace admin manages membership and is automatically an admin on every project in the workspace, through the folder grant or the workspace service account.
- Everyone else, project admins included, holds roles only per project. The same user can be an admin on one project and an operator on another.
- A workspace member with no project roles has no access to any project until someone grants a role.
- The number of workspace admins is not enforced; that is the company's call. The doctor only suggests adding another when a workspace has a single admin.

### Allowed domains instead of guests

- There is no separate guest user type for now. Every user is vetted at the workspace level anyway.
- A workspace has a list of allowed email domains. Workspace admins can add users from those domains freely.
- Adding a user from outside the allowed domains needs explicit confirmation, and the user is marked as external in the user list.

### Drift detection

People can change IAM directly in the GCP console, so the doctor compares each project's real bindings with the workspace list. It reports any extra or missing grants and offers to reconcile them.

### Offboarding

Removing a user from the workspace revokes all of that user's project roles, through folder and project IAM. It also removes any personal Claude OAuth tokens they shared with projects from each project's credentials security service, so that no job keeps running on their account. Jobs that were using those tokens fail over to other named tokens, or stop with a clear error.

### Machine identities

Service accounts used by CI, the coordinator and central orchestrators are recorded in the workspace too, as non-human members with per-project roles, so that drift detection and audit cover them. (The exact model is still to be detailed.)

### One workspace per project

A project belongs to at most one workspace. `project add` refuses a project that is already attached somewhere else. It has to be removed from the other workspace first.

### Audit trail

Every membership change, role grant or revoke, domain change and token removal is written to the workspace's Firestore with who did it and when. It is saved to the workspace's state repo alongside the other state.

## Proposed user commands

- `fugaro user add <email>` / `rm` / `ls`: manage workspace members (workspace admins only).
- `fugaro user grant <email> <project> <role>` / `revoke`: assign project roles.
- `fugaro workspace domains add|rm|ls`: manage the allowed email domains.

## Open questions

- How exactly do Fugaro's admin, operator and viewer roles map to IAM roles, whether predefined or custom?
- When projects join a folder, how are projects that already have hand-made IAM bindings migrated?
- What is the exact model for machine identities?
- Do fugaro-prod app sign-ins need to be linked to the workspace user list in a later phase?
