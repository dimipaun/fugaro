// Package craft resolves the crafts: which model a run's four crafts use and
// which credential or pool each one's calls go out on, and for each half the
// layer it came from (docs/model-crafts/spec.md §3, reconciliation MC2).
//
// It is the one place that answers "which model and which credential or pool,
// and from where", so `fugaro craft ls`, a launcher and a runner cannot
// disagree about what the same configuration means. The keys it resolves are
// internal/config's (craft.go): config.Craft is one craft's two halves as one
// layer writes them, and this package turns the layers into one answer per
// craft.
package craft

import "github.com/dimipaun/fugaro/internal/config"

// The names a layer's values are resolved under. The repository and the project
// are config's own names for the layers every fugaro.yaml key resolves in
// (config.Resolution.Sources): one vocabulary for "which layer set this", the
// same choice config.EffectiveBaseBranch makes when it resolves across the
// file, the installation config and the project. The installation's config
// (internal/localcfg) is no fugaro.yaml layer, so its name is this package's,
// and it is the one docs/model-crafts/reconciliation.md F16 prints.
const (
	SourceDefault = config.SourceDefault
	SourceProject = config.SourceProject
	SourceRepo    = config.SourceRepo
	SourceLocal   = "local"
)

// InheritedFrom is the source of a half a craft did not set and took from the
// craft it follows: "inherited from coder". It names the craft rather than the
// layer the value was finally written in, because what a reader has to know is
// which craft to change (F16).
func InheritedFrom(craft string) string { return "inherited from " + craft }

// Layer is one configuration layer's own answer about the crafts: the block it
// writes, the credentials and pools it holds, and the name it is resolved
// under.
//
// Credentials and Pools are here although a layer may hold neither — a
// repository's fugaro.yaml names credentials and pools it cannot see — because
// an access is resolved against every layer at once: whether a name exists is a
// fact of all of them, and no single layer can answer it (D1621).
type Layer struct {
	// Name is what Effective.ModelFrom and Effective.AccessFrom say for a half
	// this layer set: config.SourceRepo, SourceLocal or config.SourceProject.
	Name string
	// Crafts is the layer's own crafts block: agent.crafts in a repository's
	// file, defaults.agent.crafts in the project layer, crafts in the
	// installation's config and in the project policy node.
	Crafts config.Crafts
	// Credentials and Pools are the layer's named credentials and pools
	// (spec §1–2), the built-in vertex excepted: it needs no layer (D1624).
	Credentials []config.Credential
	Pools       []config.Pool
}

// Effective is one craft's answer: the model it runs and the credential or pool
// its calls go out on, each with where it came from, and whether what it goes
// out on serves anything yet.
type Effective struct {
	// Model is the model ID this craft runs, "" when no layer sets one and the
	// craft it follows sets none either. A craft with no model is a
	// configuration no run can serve, and naming one here that no layer wrote
	// would hide that.
	Model string
	// ModelFrom is where Model came from: the Name of the layer that set it,
	// InheritedFrom(craft), or config.SourceDefault when nothing set it.
	ModelFrom string
	// Access names the credential or pool this craft's calls go out on, and is
	// never empty: an access no layer sets is the project's default credential
	// (config.DefaultCredential, vertex today), which is what makes a project
	// that writes no craft at all resolvable (D1624).
	Access string
	// AccessFrom is where Access came from, as ModelFrom is where Model did.
	AccessFrom string
	// Inert is whether Access names a pool that serves nothing yet: a pool
	// holding two or more Claude OAuth subscription credentials while the
	// project's subscription-spread switch is off (D1622). The switch is the
	// successor of the ordered failover's own (D1636) and only the owner turns
	// it on.
	Inert bool
}

// Resolve answers every craft (config.CraftNames) from the layers, read in the
// order they are given: narrowest first, which is the order this repository
// resolves a key in — the checkout's file, the installation's config, the
// project's answer (config.EffectiveBaseBranch). The first layer that sets a
// half of a craft wins that half, and the two halves are answered apart from
// each other: a craft may change only its model and keep the access it follows
// (spec §3).
//
// A half no layer sets is the craft it follows (config.CraftParent): what a
// craft that follows nobody leaves unset is the default credential for an
// access and nothing at all for a model.
//
// subscriptionSpread is the project's subscription-spread switch (D1622,
// D1636): while it is off, a pool of two or more Claude OAuth credentials is
// inert. Whether such a pool's subscription members are individually skipped
// inside a pool that also holds another kind is the run's own rule (D1626,
// MC5) and not part of this answer.
//
// The problems are the accesses that name nothing. An access one craft inherits
// is reported on the craft that wrote it and not again on each craft that
// inherits it.
func Resolve(layers []Layer, subscriptionSpread bool) (map[string]Effective, []config.Problem) {
	// written is a half a craft sets itself. The layers are read narrowest
	// first and the first one that writes a half wins it, so the winner is the
	// narrowest layer that sets it.
	type written struct{ value, from string }
	models := map[string]written{}
	accesses := map[string]written{}
	for _, name := range config.CraftNames {
		for _, l := range layers {
			c := l.Crafts.Of(name)
			if _, ok := models[name]; !ok && c.Model != "" {
				models[name] = written{c.Model, l.Name}
			}
			if _, ok := accesses[name]; !ok && c.Access != "" {
				accesses[name] = written{c.Access, l.Name}
			}
		}
	}

	// eff answers one craft, resolving the craft it follows first: a half the
	// craft sets nowhere is that craft's answer, already resolved itself. The
	// chain is config's own and short — the review follows the coder, the
	// security review the senior review — and a craft that follows nobody ends
	// it.
	var eff func(string) Effective
	eff = func(name string) Effective {
		e := Effective{ModelFrom: SourceDefault, AccessFrom: SourceDefault}
		if w, ok := models[name]; ok {
			e.Model, e.ModelFrom = w.value, w.from
		}
		if w, ok := accesses[name]; ok {
			e.Access, e.AccessFrom = w.value, w.from
		}
		if parent := config.CraftParent(name); parent != "" {
			p := eff(parent)
			if e.Model == "" && p.Model != "" {
				e.Model, e.ModelFrom = p.Model, InheritedFrom(parent)
			}
			if e.Access == "" && p.Access != "" {
				e.Access, e.AccessFrom = p.Access, InheritedFrom(parent)
			}
		}
		if e.Access == "" {
			e.Access, e.AccessFrom = config.DefaultCredential, SourceDefault
		}
		return e
	}

	out := make(map[string]Effective, len(config.CraftNames))
	for _, name := range config.CraftNames {
		e := eff(name)
		e.Inert = inert(e.Access, layers, subscriptionSpread)
		out[name] = e
	}

	var ps []config.Problem
	for _, name := range config.CraftNames {
		w, ok := accesses[name]
		if !ok || defined(w.value, layers) {
			continue
		}
		ps = append(ps, config.Problem{
			Path: "agent.crafts." + name + ".access",
			Message: "names no credential or pool any layer holds: name one this project defines, " +
				"or the built-in " + config.CredentialVertex + ", or drop the key to inherit the craft it follows",
		})
	}
	return out, ps
}

// defined is whether name is a credential or a pool some layer holds, or the
// built-in vertex, which needs no layer.
func defined(name string, layers []Layer) bool {
	if credentialType(name, layers) != "" {
		return true
	}
	_, ok := pool(name, layers)
	return ok
}

// credentialType is the type of the credential name, "" when no layer defines
// it and it is not the built-in vertex.
func credentialType(name string, layers []Layer) string {
	for _, c := range config.BuiltinCredentials() {
		if c.Name == name {
			return c.Type
		}
	}
	for _, l := range layers {
		for _, c := range l.Credentials {
			if c.Name == name {
				return c.Type
			}
		}
	}
	return ""
}

// pool is the first pool of that name the layers hold: the same narrowest-first
// rule a half is resolved by, so two layers that hold one name cannot give two
// answers.
func pool(name string, layers []Layer) (config.Pool, bool) {
	for _, l := range layers {
		for _, p := range l.Pools {
			if p.Name == name {
				return p, true
			}
		}
	}
	return config.Pool{}, false
}

// inert is whether name is a pool that serves nothing yet: a pool of two or
// more Claude OAuth subscription credentials while the project's
// subscription-spread switch is off (D1622). A credential, an undefined name, a
// pool with fewer than two OAuth members and a pool of another kind are none of
// them inert.
func inert(name string, layers []Layer, subscriptionSpread bool) bool {
	if subscriptionSpread {
		return false
	}
	p, ok := pool(name, layers)
	if !ok {
		return false
	}
	oauth := 0
	for _, m := range p.Members {
		if credentialType(m, layers) == config.CredentialTypeClaudeOAuth {
			oauth++
		}
	}
	return oauth >= 2
}
