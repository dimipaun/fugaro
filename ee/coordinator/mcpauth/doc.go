// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package mcpauth is the hosted MCP front door's own records and rules: the
// limits a token carries, the token's shape, and the one property every limit
// obeys — that a token may only narrow what the project already allows.
//
// The design is docs/mcp-server/spec.md and the decisions are
// docs/mcp-server/reconciliation.md D920–D936; this package is the front
// door's half of the implementation plan's M1 onward. It is a business
// feature by the owner's ruling of 2026-10-27 (A6, docs/ee-reorg/spec.md):
// the hosted MCP server, like the coordinator it is served by, starts under
// the Subscription Edition licence.
//
// Two rules are what the folder rests on, and each has a task of its own:
//
//   - **Only narrows** (this package). The rigor window, the Hotfix flag, the
//     spend limit and the scopes merge by taking the tighter side against the
//     project's policy, so a wrong number or a wishful client cannot widen a
//     cap (Limits.Narrow).
//   - **Hashes only** (the mint and the store). No token value, no refresh
//     token and no client secret is ever written: a record holds a hash and
//     nothing a reader could act with (D922), and the records live in the store
//     of their own that the front door's account alone writes (D935) rather
//     than in a node of the project's database, which a rule closes to every
//     client and to nobody else (H3). **The hash is not keyed yet**: it is a
//     plain sha256 (store.go's TokenHash) and no record carries a MAC, which is
//     the row M2's own entry names and which is reported rather than closed
//     here.
//
// What the limits mean is spec.md §6, and each field points at the decision
// that shaped it: the window and its mint-time refusal (D925, D926), the
// spend limit that becomes the run's own ceiling (D924), and the two scopes
// of v1 (D927).
package mcpauth
