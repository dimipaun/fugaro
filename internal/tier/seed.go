package tier

// The seed minutes: what a task of each tier is estimated at before any run
// history exists (docs/orchestrator-graph-estimates reconciliation D2712,
// D2724). They are the "default time estimates from our own Fugaro experience"
// the spec asks for, and they are minutes only: a cost seed is deliberately
// absent, because a run's cost depends too much on a repository's own model
// choices to guess at, and a guessed figure would be read as a promise. A
// task's cost reads "no history yet" until runs have paid for it.
//
// The figures match the minutes governance expects per tier, and that is a
// coincidence rather than a link: core cannot import ee/governance
// (.claude/rules/ee.md), so these are core's own numbers and a change to one
// side does not move the other. D2724 chose the duplication over the import.

// seedMinutes is the seed of each tier, keyed by the vocabulary's own names.
// It is not read directly: SeedMinutes is the one way in, so that a tier
// outside the vocabulary is a refusal rather than a zero-minute estimate.
var seedMinutes = map[Tier]int{
	Trivial: 10,
	Small:   30,
	Medium:  90,
	Large:   240,
}

// SeedMinutes is the minutes a run of tier t is seeded with, and ok is false
// for a value outside the vocabulary. An unknown tier is not one Fugaro
// dispatched, so it has no seed to be estimated at: the caller that invented
// one is the caller that has to say what it meant.
func SeedMinutes(t Tier) (int, bool) {
	minutes, ok := seedMinutes[t]
	return minutes, ok
}
