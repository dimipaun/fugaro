package craft

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// The cross-path pool's convergence (docs/model-crafts/activation-plan.md MC21,
// reconciliation D1668 and D1673): a pool whose members span the two halves of a
// run becomes two pools, one per half, each keeping its own members in the order
// it had and the pool's strategy and owner, and the crafts whose access named it
// are repointed to the half their own model routes to.
//
// It is ONE rule and not two, and that is why it lives in this package: after
// the split the installation's own config and the published node have to name
// the same half for the same craft, or the next publish flips it back. The
// config converges at write time (internal/cli's derivePathPools) and the node
// in a step of the policy chain (internal/projectpolicy's migrateV8ToV9), and a
// second copy of the rule on either side would be the drift. Both may import
// this package and neither may import the other, so this is where it sits.
//
// A step of the policy chain may read nothing but the node (migrator's own
// contract, internal/projectpolicy/migrate.go), so the answer has to be one both
// documents can derive from themselves: the halves from the document's own
// credential types (config.CredentialServingPath), the order from the pool's own
// member order, and the craft's half from the craft's model over the document's
// own routing switch and providers block. Where a document resolves nothing the
// craft keeps the half the pool's FIRST member is on — the member the listed
// order would have served first — which changes nothing about what that craft
// was served by, and is a fact about the pool rather than a guess at a spending
// or egress path.

// PoolSplit is one crossed pool's answer: the pool the document wrote, and the
// pools that replace it, by the path that serves each.
type PoolSplit struct {
	// Pool is the pool that crossed, as the document wrote it.
	Pool config.Pool
	// Halves are the pools that replace it, keyed by the path they are served
	// by: config.CredentialPathCredsvc and config.CredentialPathRunner. Both are
	// there — a pool crosses only by naming a member of each — and the sorted
	// keys are the plan's own order (credsvc first).
	Halves map[string]config.Pool
}

// Paths are the paths the split produced, in config.CredentialPathCredsvc,
// config.CredentialPathRunner order.
func (s PoolSplit) Paths() []string { return slices.Sorted(maps.Keys(s.Halves)) }

// SplitCrossPathPools returns the pools block with every pool whose members span
// the two halves replaced by one pool per half, and the access each craft that
// named such a pool has to be given, by craft name.
//
// routing and providers are the document's own: the switch is nil for a document
// that states none, which is direct (D1b). crafts is the document's crafts block,
// whose model half the rule reads — and reads with config.CraftParent's
// inheritance, because a craft that sets no model follows another craft's.
//
// A pool that names a member of a type neither path serves is left as it is: the
// member would be dropped by the split, and a document carrying one is one the
// reader refuses for that member's own sake (config.ValidatePools).
func SplitCrossPathPools(pools []config.Pool, creds []config.Credential, crafts config.Crafts, routing *config.Routing, providers map[string]config.ModelProvider) (out []config.Pool, access map[string]string, splits []PoolSplit) {
	pathsOf := func(p config.Pool) (paths, unplaced []string) { return config.PoolServingPaths(p, creds) }
	if !slices.ContainsFunc(pools, func(p config.Pool) bool {
		paths, unplaced := pathsOf(p)
		return len(paths) > 1 && len(unplaced) == 0
	}) {
		return pools, nil, nil
	}

	taken := takenNames(pools, creds)
	halfOf := map[string]map[string]config.Pool{}
	out = make([]config.Pool, 0, len(pools))
	for _, p := range pools {
		paths, unplaced := pathsOf(p)
		if len(paths) < 2 || len(unplaced) > 0 || p.Name == "" {
			out = append(out, p)
			continue
		}
		halves := make(map[string]config.Pool, len(paths))
		for _, path := range paths {
			half := p
			half.Name = derivedPoolName(p.Name, path, taken)
			half.Members = membersOnPath(p, creds, path)
			// Sliced rather than shared with the pool that crossed: the config
			// side writes into the block it is handed, and two entries of one
			// backing array are one entry written twice.
			half.AllowedUsers = slices.Clone(p.AllowedUsers)
			taken[half.Name] = true
			halves[path] = half
			out = append(out, half)
		}
		halfOf[p.Name] = halves
		splits = append(splits, PoolSplit{Pool: p, Halves: halves})
	}

	access = map[string]string{}
	for _, name := range config.CraftNames {
		halves, crossed := halfOf[crafts.Of(name).Access]
		if !crossed {
			continue
		}
		half := craftHalf(crafts, routing, providers, name)
		if half == "" {
			half = firstMemberPath(splits, crafts.Of(name).Access)
		}
		access[name] = halves[half].Name
	}
	return out, access, splits
}

// membersOnPath is one half's member list: the pool's own members served by this
// path, in the order the pool listed them (D1632 — the order is what failover
// walks).
func membersOnPath(p config.Pool, creds []config.Credential, path string) []string {
	var out []string
	for _, m := range config.PoolMembers(p, creds) {
		if m.Path == path {
			out = append(out, m.Name)
		}
	}
	return out
}

// firstMemberPath is the path of the pool's first member that a path serves: the
// answer for a craft whose model resolves nothing, which is the member the
// listed order would have served first.
func firstMemberPath(splits []PoolSplit, pool string) string {
	for _, s := range splits {
		if s.Pool.Name != pool {
			continue
		}
		paths := s.Paths()
		for _, m := range s.Pool.Members {
			for _, path := range paths {
				if slices.Contains(s.Halves[path].Members, m) {
					return path
				}
			}
		}
		if len(paths) > 0 {
			return paths[0]
		}
	}
	return ""
}

// takenNames are the names a derived pool may not take: every pool's, every
// credential's and the built-in vertex's, because a pool and a credential share
// one namespace (config.ValidatePools) and a craft's access names one of them
// without saying which.
func takenNames(pools []config.Pool, creds []config.Credential) map[string]bool {
	taken := map[string]bool{config.CredentialVertex: true}
	for _, p := range pools {
		if p.Name != "" {
			taken[p.Name] = true
		}
	}
	for _, c := range creds {
		if c.Name != "" {
			taken[c.Name] = true
		}
	}
	return taken
}

// derivedPoolName is the name one half takes: <pool>-claude for the service's
// half and <pool>-provider for the run's own gateway, with a number appended
// while that name is taken or is one no credential may carry.
//
// The name has to be free and well formed or the document this migration writes
// refuses itself, which is the broken installation CLAUDE.md's HARD RULE
// forbids: a name a credential may carry is at most
// config.MaxClaudeAccountNameLen characters (credentialNameProblem), so a long
// pool name is cut rather than left to produce a name the writer refuses.
func derivedPoolName(base, path string, taken map[string]bool) string {
	suffix := "-provider"
	if path == config.CredentialPathCredsvc {
		suffix = "-claude"
	}
	for n := 1; ; n++ {
		s := suffix
		if n > 1 {
			s = fmt.Sprintf("%s-%d", suffix, n)
		}
		head := base
		if cut := config.MaxClaudeAccountNameLen - len(s); len(head) > cut {
			if cut <= 0 {
				continue
			}
			head = head[:cut]
		}
		cand := strings.TrimRight(head, "-") + s
		if !taken[cand] && config.ValidCredentialRef(cand) == "" {
			return cand
		}
	}
}

// craftHalf is the half a craft's own model is served on, "" where the document
// resolves nothing.
//
// The model is the craft's own rung and nothing else: the rungs above it (a
// task's grade, a review level's model) and the ones below it (agent.models.role,
// agent.model) belong to a run and not to either document, and a step of the
// policy chain cannot read them at all.
func craftHalf(crafts config.Crafts, routing *config.Routing, providers map[string]config.ModelProvider, name string) string {
	model := craftModelOf(crafts, name)
	if model == "" {
		return ""
	}
	var sw *config.RoutingClaude
	if routing != nil {
		sw = routing.Claude
	}
	route, _, ok := config.RoutingClaudeFor(sw, model, providers)
	if ok && (route != config.RouteDirect || isClaudeModel(model)) {
		if route == config.RouteDirect {
			return config.CredentialPathCredsvc
		}
		return config.CredentialPathRunner
	}
	// A model of another kind is served by the provider entry that claims it,
	// and a provider route is always the run's own gateway: the job holds that
	// member's key, and the credentials service never sees it (F7, D1634).
	if _, _, ok := config.ProviderFor(providers, model); ok {
		return config.CredentialPathRunner
	}
	return ""
}

// isClaudeModel reports whether a model is one the routing switch speaks about:
// the canonical Claude ID a pin carries, which is the only spelling
// routing.claude.models may hold (config.CheckRoutingModel's own rule, which is
// why the switch can name nothing else).
//
// The switch's answer is read for such a model and for no other.
// RoutingClaudeFor answers "direct" for every model the switch does not name,
// and that is right for a Claude ID — a Claude call the switch routes nowhere
// goes to Anthropic itself, which is the service's half — and wrong for a model
// of another kind: a coder running DeepSeek through OpenRouter (the spec's own
// example) is served by the provider entry that claims it, and reading the
// switch's "direct" for it would repoint that craft onto the subscription half,
// which cannot serve it at all (F23, D1637).
func isClaudeModel(model string) bool { return config.CheckRoutingModel(model) == "" }

// craftModelOf is the model a craft resolves to within one document: the model
// the craft sets, or the one the craft it follows sets (config.CraftParent).
// "" is a craft no rung of the document gives a model.
func craftModelOf(crafts config.Crafts, name string) string {
	for n := name; n != ""; n = config.CraftParent(n) {
		if m := crafts.Of(n).Model; m != "" {
			return m
		}
	}
	return ""
}
