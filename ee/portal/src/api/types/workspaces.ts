// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Workspaces and the binding that attaches an entitlement to one (portal plan
// P1.4; F20). The shapes are `ee/portal/schemas/workspaces.schema.json` and
// their samples are `ee/portal/schemas/fixtures/workspaces.*.json`, held to
// each other by `test/types.test.ts`.

/** One workspace: a Firebase project an entitlement may be bound to. */
export interface Workspace {
  id: string
  organisation_id: string
  name: string
  /** The Firebase project id once bound, or null while the workspace is unbound. */
  project_id: string | null
  /** The active entitlement, or null while none is bound. */
  entitlement_id: string | null
  created_at: string
  bound_at: string | null
}

/**
 * A binding: the entitlement, and the Firebase project id it is bound to. Both
 * are names the caller chose; nothing proves the project id (D2205).
 */
export interface WorkspaceBind {
  entitlement_id: string
  project_id: string
}

/** Everything `workspaces.schema.json` describes. */
export type Workspaces = Workspace | WorkspaceBind
