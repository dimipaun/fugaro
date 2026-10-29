package cli

import (
	"github.com/spf13/cobra"
)

// The test-only seams for extend.go. Each one puts the package into a state a
// released binary must never reach — a command no build ships, a declarer
// switched off — so none of them lives in production code: a core caller that
// could switch the declarer off would silently stop every launch of a tagged
// binary declaring itself (design ee-boundary-moves.md §5.5).

// resetExtend clears the command registry, its build latch and the launch
// declarer, so one test's registration or hook cannot reach the next test, or
// the tests that launch. It is called before and after each of them.
func resetExtend() {
	registrations = map[string]func() *cobra.Command{}
	registeredNames = map[string]bool{}
	registrationsSealed = false
	launchDeclarer, launchDeclarerSet = coreLaunchDeclarer, false
}

// SetNoLaunchDeclarer is the state the untagged build is in once E2.1 moves
// the declarer out: no declarer at all, so a launch declares nothing. It is
// here rather than in the API because it takes a declarer away rather than
// installing one.
func SetNoLaunchDeclarer() {
	launchDeclarer, launchDeclarerSet = nil, false
}

// setPaidNames replaces the paid-command list and returns the restore. A test
// needs it because both names this build carries code for are still core
// commands, so the real list installs no stub at all.
func setPaidNames(names []string) func() {
	prev := paidCommandNames
	paidCommandNames = names
	return func() { paidCommandNames = prev }
}

// coreRootPaths is the released tree's command paths with nothing registered
// applied: what a test compares a built tree against to hold that a build
// which registered nothing is the released one.
func coreRootPaths() []string { return commandPaths(newCoreRoot()) }
