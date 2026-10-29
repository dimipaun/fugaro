// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package governance is the policy half of governance and the stage tripwires
// that read it: the guardrails Fugaro ships, the rule that a layer may only
// tighten them, and the wall-clock bound a stage is held to with the halt it
// leaves when it fires (plan task E2.3, design ee-boundary-moves.md §4).
//
// The vocabulary, the wire and the mechanism stay in the core: strict decoding
// means an untagged runner that did not know `governance:` would refuse the
// file and stop every repository of the project (F9, R7), and the halt itself
// — the reason vocabulary, the checkpoint report, the run-log line — is core
// plumbing an ee/ hook cannot invent (D521, D529). What is here is only what
// the core default no longer enforces: an untagged build decodes the block,
// warns once and applies none of it (D522).
//
// ee/register is the one place these functions are handed to the core's seams;
// nothing else sets them (design §5.5).
package governance
