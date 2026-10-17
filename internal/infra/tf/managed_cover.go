package tf

import (
	"maps"
	"slices"
	"strings"
)

// The managed project's instantiation of Cover (Task 1.1 of
// docs/plans/2026-10-16-managed-projects.md).
//
// init's own cover (cover.go) answers "what does Fugaro's own installation
// need": its resource types are the ones the embedded modules declare, and its
// grant and role tables are the ones those modules write. A managed project's
// plan is written by someone else — the agent in the managed project — so the
// question is different: what may an agent-authored plan contain in a project
// Fugaro created, given that the identity applying it (D75's deployer service
// account, Task 1.5) can do exactly what the managed allowlist admits and
// nothing wider. The list below is therefore derived from allowedTypes and is
// deliberately NOT identical to it, and deriving it is a decision rather than
// a copy (design finding 22, D61).
//
// RED STEP PLACEHOLDER: this first revision inherits allowedTypes wholesale,
// and the tests below are written to fail against it.
var managedTypes = func() map[string]bool {
	m := map[string]bool{}
	for t := range allowedTypes {
		m[t] = true
	}
	return m
}()

// managedTypeReasons is the one-line justification of every entry the managed
// list keeps.
var managedTypeReasons = map[string]string{}

// managedDropped is the reason every entry of allowedTypes the managed list
// leaves out is not on it. Leaving a type out is the default and the safe
// direction: a refused plan falls back to a confirmation, which is a working
// program, while a permitted plan that should not have been is the failure this
// gate exists to avoid.
var managedDropped = map[string]string{}

// ManagedPrefix is the name prefix every resource a managed plan creates must
// carry: the deployer's scoped role is bound under an IAM condition that
// admits this prefix and nothing else (D75 (iii), Task 1.5), so a plan that
// touches a resource outside it is a plan the deployer cannot apply.
//
// It deliberately matches none of Fugaro's own names: the runs bucket
// (fugaro-runs-<gcp-project>), the dedicated state bucket of D77
// (fugaro-managed-tfstate-<gcp-project>, which is why "fugaro-managed-" is not
// a usable prefix), the state bucket of the installation
// (fugaro-tfstate-<gcp-project>), the fixed accounts (fugaro-scheduler,
// fugaro-history, fugaro-token-signer), the per-job accounts
// (fugaro-<name>-<8 hex>), the installation's repositories (fugaro-base,
// fugaro-ghcr) and the Firebase web key (fugaro-web).
// TestManagedPrefixDoesNotMatchFugarosOwnNames is what holds that.
const ManagedPrefix = "fmgd-"

// ManagedAllowedTypes are the resource types a plan in a managed project may
// contain, the managed allowlist itself: Task 1.1's decision, one line of
// justification per entry in managedTypeReasons.
func ManagedAllowedTypes() []string {
	return slices.Sorted(maps.Keys(managedTypes))
}

// managedDroppedTypes are the entries of allowedTypes the managed list leaves
// out, sorted.
func managedDroppedTypes() []string {
	return slices.Sorted(maps.Keys(managedDropped))
}

// ManagedCover is the managed project's instantiation of tf.Cover: the
// scope-and-attribute gate the managed apply hands managed.Spec.Cover
// (internal/managed/plan.go), with Cover's own inputs — the projects, the
// members the review listed, the region, the buckets, the registry and the
// billing account — taken unchanged from it.
//
// RED STEP PLACEHOLDER: the first revision carries the inherited answer.
type ManagedCover struct {
	Cover
}

func (c ManagedCover) NotCovered(p *Plan, imports []ImportKey) []string {
	return c.Cover.NotCovered(p, imports)
}

var _ = strings.TrimSpace
