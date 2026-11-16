package cli

import (
	"github.com/spf13/cobra"
)

// The test-only seams for extend.go. Each one puts the package into a state a
// released binary must never reach — a command no build ships, a declarer
// installed where none should be — so none of them lives in production code
// (design ee-boundary-moves.md §5.5).

// resetExtend clears the command registry, its build latch and the launch
// declarer, so one test's registration or hook cannot reach the next test, or
// the tests that launch. It is called before and after each of them.
func resetExtend() {
	registrations = map[string]func() *cobra.Command{}
	registeredNames = map[string]bool{}
	registrationsSealed = false
	launchDeclarer, launchDeclarerSet = nil, false
	launchPlanner, launchPlannerSet = nil, false
	recipeManifests = map[string]RecipeManifest{}
}

// setPaidNames replaces the paid-command list and returns the restore. A test
// needs it to install a stub for a name no core command holds. Every name it
// installs gets a stub: the list is the test's own, and a name a test names is
// one it means the build to answer for (noStub is the released list's fact
// about `licence`, which a test's own names are not).
func setPaidNames(names []string) func() {
	prev := paidCommands
	paidCommands = nil
	for _, name := range names {
		paidCommands = append(paidCommands, paidCommand{name: name})
	}
	return func() { paidCommands = prev }
}

// coreRootPaths is the released tree's command paths with nothing registered
// applied: what a test compares a built tree against to hold that a build
// which registered nothing is the released one.
func coreRootPaths() []string { return commandPaths(newCoreRoot()) }
