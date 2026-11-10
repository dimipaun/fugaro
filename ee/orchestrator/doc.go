// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package orchestrator is the orchestrator's shared half: the operations that
// only mean something once a project has more than one developer. It hands an
// agent from one developer to another, it is where an agent the project owns
// rather than a person comes from — the spec's "Multiple developers" section —
// and it says how many work items one approval covers, which is the spec's batch
// approval as the project's setting rather than a person's. All of them live in
// ee/ because they need the project's identity and governance (D1222, F14).
//
// The core carries the seams these fill: internal/orchestrator/hooks.go holds
// the Agent, Handoff and Batch vocabulary, the HandoffAgent and ApprovalBatch
// that run one each, and defaults that refuse the move and the raise, and it
// knows nothing about this package. ee/register installs Transfer and BatchSize
// once, the way it installs the coordinator's CLI. Nothing here is imported from
// the core, so the core builds and tests with ee/ deleted.
package orchestrator
