// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package orchestrator is the orchestrator's shared half: the operations that
// only mean something once a project has more than one developer. It hands an
// agent from one developer to another, and it is where an agent the project
// owns rather than a person comes from — the spec's "Multiple developers"
// section — and both live in ee/ because they need the project's identity and
// governance (D1222, F14).
//
// The core carries the seam these fill: internal/orchestrator/hooks.go holds
// the Agent and Handoff vocabulary, the HandoffAgent that runs one and a
// default that refuses every handoff, and it knows nothing about this package.
// ee/register installs Transfer once, the way it installs the coordinator's
// CLI. Nothing here is imported from the core, so the core builds and tests
// with ee/ deleted.
package orchestrator
