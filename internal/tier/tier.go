// Package tier is the expectation tier's vocabulary (docs/task-governance
// spec.md §3, implementation plan T8.1): how much a run is expected to cost
// and take, chosen by the launcher or the orchestrator when it dispatches.
//
// It is the only place that spells the four tiers, so the task spec, the
// governance configuration and the CLI share one closed list rather than three
// that can drift apart. It holds the vocabulary alone: the expectations each
// tier starts with are governance defaults and live with them
// (ee/governance's Defaults), because a tier's value is a guardrail the project
// layer may set and never a property of the name.
//
// A tier is an expectation and never a cap: the deployed per-run cap stays the
// ceiling whatever tier a run carries (constraint G6), so an expected cost
// above the cap is an expectation the run still stops at the cap for. Nothing
// reads a tier at run time yet — the expected-cost checkpoint is T8.2 — so a
// task that carries one says what it is expected to be and nothing more.
package tier

import (
	"slices"
	"strings"
)

// Tier is one point of the expectation axis: how much a run is expected to
// cost and take.
type Tier string

// The four tiers, as fugaro.yaml, task.json and the CLI spell them (spec §3),
// ordered from the smallest change to the largest.
const (
	Trivial Tier = "trivial"
	Small   Tier = "small"
	Medium  Tier = "medium"
	Large   Tier = "large"
)

// Tiers is the whole vocabulary, smallest first. It is closed: a value outside
// it is refused where it is written, never read as an unknown tier later.
var Tiers = []Tier{Trivial, Small, Medium, Large}

// Known reports whether t is one of Tiers. The zero Tier — a task that carries
// no tier — is not one.
func Known(t Tier) bool { return slices.Contains(Tiers, t) }

// List is the vocabulary as a refusal spells it.
func List() string {
	names := make([]string, len(Tiers))
	for i, t := range Tiers {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}
