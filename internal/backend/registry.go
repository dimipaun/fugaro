package backend

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Opener constructs a Backend, already configured for one project: a
// caller resolves its own backend-specific options (GCP project, region,
// endpoints, ...) into a closure before building the map Open picks from.
// This package names no backend's package, which would cycle back to it.
type Opener func(ctx context.Context) (Backend, error)

// Open calls openers[name], the local config's backend key (CloudRun is
// its default). An unknown name is a user error naming every backend
// openers does know, sorted, so the message does not depend on reading the
// registry's source (design m11-setup-and-skills.md §7).
func Open(ctx context.Context, name string, openers map[string]Opener) (Backend, error) {
	o, ok := openers[name]
	if !ok {
		known := slices.Sorted(maps.Keys(openers))
		return nil, fmt.Errorf("unknown backend %q; known backends: %s", name, strings.Join(known, ", "))
	}
	return o(ctx)
}
