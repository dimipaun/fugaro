package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/settings"
)

// `fugaro config publish` under the managed gate (M2, D1012, spec §4.2).
//
// Why this command has rules of its own and does not simply let the parse
// decide. The layer is a document the project's owner keeps, and the parse
// already refuses what it cannot read: a key no scope carries, a value that is
// not a number, a credential-shaped string. What the parse cannot say is which
// of the keys it understands an agent's session may WRITE — `governance.*` is a
// key this program reads and a lever over every run of the project, and a
// document that parses is still a document no session may publish. So the rules
// below are the ones a person would apply by reading the diff, applied to the
// bytes the command is about to write: the registry, the reserved keys, the
// executable keys, a widening of a cap or a model claim, and D854's generation
// of the local file the bytes came from.
//
// Why every refusal carries the owner's own command. The session reads WHY the
// step stopped and that no person is being asked (D1023's first sentence), and
// the owner reads the one command they run themselves (its second) — which is
// this same command, on a layer the owner has reviewed, in their own terminal.
// Neither sentence may name a flag, a variable or a mark a reader could change
// to get past the gate, and `--executable-changes` is exactly such a flag: the
// command line of the session the gate judges is the session's own, so a flag
// it can pass is not the owner's consent (D1015(e), D1021(b)). Every
// executable change is therefore refused here, whatever the flag says, and the
// flag is never named — the banner publishLayer prints for a person's own run
// is where it belongs.
//
// Why the gate reads the published object itself. Two of the rules are about
// what the write REPLACES rather than about the new document: an executable key
// that changes, and a cap or a model claim that widens. Both are decided
// against the object as it is now, so the read is made here, once, with the
// command's own credentials — and a read that fails is not an admission: a
// publish whose predecessor cannot be read is a publish nobody can judge.

// configPublishGate reads the managed gate for this command: the two marks,
// per command and never cached (D1015), through the helper M6 wrote
// (`readManagedGate`), so the accept path, the refusal and the audit record are
// the same four calls every command in the set makes.
//
// It answers nil, nil for a project that is not managed, which is the answer
// that keeps today's refusal exactly: the caller has no gate to consult and
// refuses the session in the command's own words. A read that failed is an
// error and stops the command (fail closed): a project this program cannot
// prove is managed is a project whose confirmations it must not skip.
//
// The clients are built from the installation's own endpoints when the
// installation is a fake (a test's), and from Google's otherwise. A
// no_auth installation that names no storage or Resource Manager endpoint has
// no cloud to read the marks from at all — there is nothing to be managed
// there, and no call is attempted.
func configPublishGate(ctx context.Context, env *cloudEnv, marker string) (*managedGate, error) {
	lc := env.lc
	if lc.Endpoints.NoAuth && (lc.Endpoints.Storage == "" || lc.Endpoints.ResourceManager == "") {
		return nil, nil
	}
	s, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return nil, remote(fmt.Errorf("nothing was published: the managed gate cannot be read: no storage client could be built to read the project's creation fact: %w", err))
	}
	c, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return nil, remote(fmt.Errorf("nothing was published: the managed gate cannot be read: no Resource Manager client could be built to read the project's own label: %w", err))
	}
	g, err := readManagedGate(ctx, managedGateEnv{lc: lc, s: s, crm: c}, managedGateFacts{
		Subcommand:  "config publish",
		Argv:        os.Args[1:],
		Declined:    "nothing was published",
		AgentMarker: marker,
	})
	if err != nil {
		return nil, err
	}
	if !g.managed {
		return nil, nil
	}
	return &g, nil
}

// configPublishConverge judges the layer document an agent's session is about
// to publish, with this command's rules, and answers the verdict the gate
// decides the session's confirmation under (M0's Converge: the bytes judged,
// the class, the rules that fired).
//
// The class is YesOrTyped: publishing a layer is an ordinary confirmed step and
// never a Typed one, so the gate may judge it at all (D1026 — and a Typed step
// could not be admitted by these rules however clean the document looked).
//
// It also answers the object as IT read it, for the write to be conditioned on
// that read and on no other (D1024's rule, one step further out): the rules
// above judge the document against the published layer, and a write that took a
// second read would be conditioned on a layer the rules never saw — the
// widening they admitted could be measured against a document that landed in
// between, and the audit record would name a plan nobody judged.
func configPublishConverge(ctx context.Context, env *cloudEnv, data []byte) (managedmode.Converge, *layerAt) {
	rules := layerDocumentRules(data)
	l, ps := projectLayerProblems(data, env.lc)
	if len(ps) > 0 {
		rules = append(rules, "the layer is not a document this program can publish ("+
			pluginwire.Printable(layerProblemsText(ps))+
			"), so the project's owner publishes one it can: fugaro config publish, at their own terminal")
		return managedmode.ConvergeRefused(data, initflow.YesOrTyped, rules), nil
	}
	old, gen, rerr := env.bucket.ReadMaxStrict(ctx, config.LayerKey, config.LayerMaxBytes)
	var prev *config.ProjectLayer
	var at *layerAt
	switch {
	case rerr == nil:
		at = &layerAt{old: old, gen: gen, exists: true}
		// nil when the published object is not a layer this build can read:
		// every rule over the difference then counts its whole content as new,
		// which is the fail-closed direction decision L7 already takes for the
		// executable keys in publishLayer.
		if op, invalid := config.ParseProjectLayer(old, config.LayerAnchor{}); len(invalid) == 0 {
			prev = op
		}
	case errors.Is(rerr, blobx.ErrNotExist):
		// nothing is published yet: the diff is the whole document
		at = &layerAt{}
	default:
		rules = append(rules, "the published project layer could not be read ("+oneLineCLI(rerr.Error())+
			"), so what this write replaces cannot be judged, and the project's owner publishes from their own terminal")
		return managedmode.ConvergeRefused(data, initflow.YesOrTyped, rules), nil
	}
	if changes, cerr := executableChanges(prev, l); cerr != nil {
		rules = append(rules, "this fugaro cannot compare the layer's executable keys ("+cerr.Error()+
			"), so the change cannot be judged")
	} else if len(changes) > 0 {
		// Whatever --executable-changes says: in a coding agent's session that
		// flag is the session's own command line, and a change to what runs as
		// shell is the owner's to make in their own terminal (D1015(e)).
		rules = append(rules, "the layer changes what runs as shell in every repository that takes a profile ("+
			safetext.FieldListLine(changes)+"), which is a change the project's owner publishes from their own terminal")
	}
	rules = append(rules, layerWideningRules(prev, l)...)
	if r := layerStaleRule(env.lc, prev, gen); r != "" {
		rules = append(rules, r)
	}
	if len(rules) > 0 {
		return managedmode.ConvergeRefused(data, initflow.YesOrTyped, rules), at
	}
	return managedmode.ConvergeAdmitted(data, initflow.YesOrTyped, layerAdmittedRules), at
}

// layerAdmittedRules is what a record of an admission says was applied and
// passed: the rules above, in the order the converge applies them. They are
// written out rather than derived from the functions, because a record has to
// be checkable by a person months later without reading this build's code.
var layerAdmittedRules = []string{
	"every key of the document is one the settings registry knows",
	"the document names no reserved key",
	"the document is one this program can publish",
	"no key that runs as shell changes, whatever the command line says",
	"the document widens no model claim, no A/B route and no cap",
	"this machine's local layer file was written from the published layer's generation",
}

// layerReservedRuns are the keys no coding agent's session writes, as the
// segments the document spells them with (spec §4.2): a run matches the path
// wherever it appears, and `*` matches one segment. They are the levers that
// are not the layer's to move — a credential, a clone URL, a budget key, the
// agent's own token and the governance block — and they are named here rather
// than derived from the registry because most of them are registry keys: the
// point is that the registry knows them and the gate still refuses them.
var layerReservedRuns = [][]string{
	{"credentials", "*"},
	{"clone_urls"},
	{"budget", "*"},
	{"agent", "oauth_token"},
	{"governance", "*"},
}

// layerRegistryKeys is the settings registry's project-layer keys, read once:
// the keys a layer may write are exactly the keys `fugaro settings` documents
// for it, so a document and a `settings set` cannot disagree about what is
// settable.
var layerRegistryKeys = func() []string {
	var out []string
	for _, r := range settings.Rows() {
		if strings.HasPrefix(r.Key, "layer.") {
			out = append(out, r.Key)
		}
	}
	return out
}()

// layerDocumentRules judges the document's own keys: the reserved runs first
// (a reserved key is refused whatever the registry says about it), then every
// key the registry does not know.
//
// A document this walk cannot read — YAML it cannot parse, a key that is not a
// string, an anchor or an alias — gets a rule of its own rather than no rule.
// The parse refuses all four as well, so the verdict is a refusal either way;
// what the rule adds is that the refusal names the keys as unjudged instead of
// reading as a document every key rule passed, which is the reading an attempt
// to reach past this gate through a shape the walk does not follow would
// otherwise leave in the record.
func layerDocumentRules(data []byte) []string {
	paths, ok := layerKeyPaths(data)
	if !ok {
		return []string{"the layer's keys cannot be read as a document this gate judges (YAML it cannot parse, a key that is not a string, or an anchor and the alias that copies it), so no key of it was judged and no session publishes it"}
	}
	var reserved, unknown []string
	for _, p := range paths {
		if run := layerReservedRun(p); len(run) > 0 {
			reserved = append(reserved, strings.Join(run, "."))
			continue
		}
		if !layerRegistryExact(p) && !layerRegistryExtends(p) {
			unknown = append(unknown, layerPathKey(p))
		}
	}
	var rules []string
	if len(reserved) > 0 {
		rules = append(rules, "the layer names the reserved key(s) "+safetext.FieldListLine(sortedUnique(reserved))+
			", which no coding agent's session writes: the project's owner publishes that at their own terminal")
	}
	if len(unknown) > 0 {
		rules = append(rules, "the layer names the key(s) "+safetext.FieldListLine(sortedUnique(unknown))+
			", which the settings registry does not know, so no run reads them and no session publishes them")
	}
	return rules
}

// layerKeyPaths is the document's key paths, one per leaf and one per key the
// registry knows: the paths the two rules above are decided over. `*` stands in
// for an element of a list of blocks, which is how the registry spells one
// (`defaults.rigor.areas.*.mode`).
//
// The walk stops at a path the registry knows, and that is the whole reason it
// has to be a walk: `layer.claude_models` is one registry row holding a block
// of its own, so descending into it would judge provider and model names
// against keys the registry deliberately does not have.
func layerKeyPaths(data []byte) ([][]string, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false
	}
	var paths [][]string
	var walk func(path []string, n *yaml.Node) bool
	walk = func(path []string, n *yaml.Node) bool {
		if n.Anchor != "" || n.Alias != nil {
			return false
		}
		if len(path) > 0 && layerRegistryExact(path) {
			paths = append(paths, path)
			return true
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
					return false
				}
				if !walk(append(slices.Clip(path), k.Value), v) {
					return false
				}
			}
		case yaml.SequenceNode:
			if !layerListOfBlocks(n) {
				paths = append(paths, path)
				return true
			}
			for _, e := range n.Content {
				if !walk(append(slices.Clip(path), "*"), e) {
					return false
				}
			}
		default:
			paths = append(paths, path)
		}
		return true
	}
	if !walk(nil, doc.Content[0]) {
		return nil, false
	}
	return paths, true
}

// layerListOfBlocks reports whether a list holds blocks, so its elements are
// addressed by index: the registry's own spelling of a list of scalars is the
// key itself (`ab.challengers`), and of a list of blocks is the key with `*`
// under it (`defaults.rigor.areas.*.name`).
func layerListOfBlocks(n *yaml.Node) bool {
	for _, e := range n.Content {
		if e.Kind == yaml.MappingNode || e.Kind == yaml.SequenceNode {
			return true
		}
	}
	return false
}

// layerPathKey is a path as the registry spells it. The prefix is the one
// internal/settings's layer rows carry (SD14): the project layer's
// `claude_models` and the local config's are different keys.
func layerPathKey(path []string) string { return "layer." + strings.Join(path, ".") }

// layerRegistryExact reports whether the registry knows this exact path: the
// row a `settings set` would write for the same key in the same file. A row
// whose key varies — `layer.profiles.*.commands.test` is the test command of any
// profile — is matched by its pattern rather than by its spelling, through
// internal/settings's own rule for that question.
func layerRegistryExact(path []string) bool {
	key := layerPathKey(path)
	if _, ok := settings.Lookup(key); ok {
		return true
	}
	for _, row := range layerRegistryKeys {
		if settingsPatternMatch(row, key) {
			return true
		}
	}
	return false
}

// layerRegistryExtends reports whether some registry key lies under this path,
// which is what tells a block of settable keys from a key nothing reads.
func layerRegistryExtends(path []string) bool {
	segs := strings.Split(layerPathKey(path), ".")
	for _, key := range layerRegistryKeys {
		row := strings.Split(key, ".")
		if len(row) <= len(segs) || !segmentsMatch(row[:len(segs)], segs) {
			continue
		}
		return true
	}
	return false
}

// segmentsMatch reports whether the first pattern segments name the value
// segments: equal where the pattern names one, and any non-empty segment (the
// `*` of a list index as much as the `*` of a varying name) where it writes
// `*`. It answers the spelling and nothing else, and it is
// internal/settings's own rule for the same question (settingsPatternMatch),
// written over a prefix so a row can be asked "is this key under me".
func segmentsMatch(pattern, value []string) bool {
	if len(pattern) > len(value) {
		return false
	}
	for i := range pattern {
		if pattern[i] == "*" {
			if value[i] == "" {
				return false
			}
			continue
		}
		if pattern[i] != value[i] {
			return false
		}
	}
	return true
}

// layerReservedRun is the reserved run a path hits, as the path spells it, and
// its shortest spelling: the run matched anywhere in the path, so
// `defaults.agent.oauth_token` is refused for the `agent.oauth_token` run as
// much as a top-level `governance` block is for its own.
func layerReservedRun(path []string) []string {
	for _, run := range layerReservedRuns {
		for i := 0; i+len(run) <= len(path); i++ {
			if segmentsMatch(run, path[i:i+len(run)]) {
				return path[i : i+len(run)]
			}
		}
	}
	return nil
}

// layerWideningRules names every change that admits more than the layer it
// replaces did (spec §4.1.5, §4.1.6): a model claim the project did not serve,
// an A/B challenger it did not run, an A/B rate or daily pair count raised, and
// the agent's own spend cap raised. A layer that is narrower, or different in a
// way that admits nothing, passes.
//
// What is deliberately not judged here: `ab.judge_model` (which judge a pair
// uses, not how many runs are sampled), `ab.exchange_rate` (a cost tolerance,
// and the modes it names have shipped figures of their own) and
// `profiles.*.inputs.*` (caps whose unset state is a built-in figure this
// command does not hold — the settings design's floor-and-ceiling rule is where
// those are judged, and a comparison against a guessed default would refuse a
// layer for a raise that never happened).
//
// A published object that is absent or unreadable makes prev nil, and then
// every claim in the document is new: publishing models into a project that
// served none is exactly the widening this refuses.
func layerWideningRules(prev, next *config.ProjectLayer) []string {
	if next == nil {
		return nil
	}
	var rules []string
	add := func(what string) {
		rules = append(rules, "the layer "+what+", which is a widening no coding agent's session publishes: the project's owner publishes it at their own terminal")
	}
	var prevModels map[string][]string
	var prevAB config.AB
	if prev != nil {
		prevModels, prevAB = prev.ClaudeModels, ab(prev)
	}
	if added := addedClaims(prevModels, next.ClaudeModels); len(added) > 0 {
		add("adds the model claim(s) " + safetext.FieldListLine(added) + " to claude_models")
	}
	if added := addedStrings(prevAB.Challengers, ab(next).Challengers); len(added) > 0 {
		add("adds the A/B challenger(s) " + safetext.FieldListLine(added))
	}
	if a, b := layerBudget(prev), layerBudget(next); b > a {
		add(fmt.Sprintf("raises defaults.agent.max_budget_usd from %v to %v", a, b))
	}
	if a, b := prevAB.Rate, ab(next).Rate; b > a {
		add(fmt.Sprintf("raises ab.rate from %v to %v, so more of the project's runs become challengers", a, b))
	}
	nb := ab(next)
	if a, b := prevAB.MaxPairs(), nb.MaxPairs(); b > a {
		add(fmt.Sprintf("raises ab.max_pairs_per_day from %d to %d", a, b))
	}
	return rules
}

// ab is a layer's A/B block, with a nil layer read as the block a project that
// published none has: the zero value, whose MaxPairs takes the shipped default
// exactly as a run's own resolution does.
func ab(l *config.ProjectLayer) config.AB {
	if l == nil || l.AB == nil {
		return config.AB{}
	}
	return *l.AB
}

// layerBudget is a layer's agent spend cap as a run reads it: a layer that
// writes none starts under config.DefaultMaxBudgetUSD, so the comparison is of
// the numbers a run would really be bounded by and not of 0 against a number.
func layerBudget(l *config.ProjectLayer) float64 {
	if l == nil || l.Defaults.Agent.MaxBudgetUSD == 0 {
		return config.DefaultMaxBudgetUSD
	}
	return l.Defaults.Agent.MaxBudgetUSD
}

// addedClaims is the provider/model pairs next names that prev did not, sorted
// and written as the layer spells them (`anthropic/claude-x`).
func addedClaims(prev, next map[string][]string) []string {
	var out []string
	for _, provider := range modelProviders(prev, next) {
		for _, id := range next[provider] {
			if !slices.Contains(prev[provider], id) {
				out = append(out, provider+"/"+id)
			}
		}
	}
	return sortedUnique(out)
}

// modelProviders is the union of two claim maps' providers, which is what a
// comparison of them has to walk.
func modelProviders(a, b map[string][]string) []string {
	out := make([]string, 0, len(a)+len(b))
	for k := range a {
		out = append(out, k)
	}
	for k := range b {
		out = append(out, k)
	}
	return slices.Sorted(slices.Values(out))
}

// addedStrings is the entries of next that prev did not hold, sorted and
// deduplicated, so a rule that named an entry twice does not read as two.
func addedStrings(prev, next []string) []string {
	var out []string
	for _, s := range next {
		if !slices.Contains(prev, s) {
			out = append(out, s)
		}
	}
	return sortedUnique(out)
}

// sortedUnique is a listing of keys or names, sorted and deduplicated: a rule
// that named the same key twice would read as two findings.
func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// layerStaleRule is D854's check as a gate rule: the local layer file this
// command would publish was written from a generation that is not the published
// object's, so the write would replace a layer this machine cannot account for
// (another machine published since). The refusal carries the owner's own
// command — `fugaro update`, which re-seeds the file from the published layer —
// through doctor's own sentence for the state, so the same two surfaces cannot
// describe one file differently.
//
// Only the stale cause refuses. A local file with no recorded generation is a
// file this machine has never published (the ordinary first publish, and the
// one way an owner seeds a layer of their own), and a local edit on top of the
// published generation is exactly what publishing is for; doctor reports both
// and the gate admits them.
func layerStaleRule(lc *localcfg.Config, prev *config.ProjectLayer, gen int64) string {
	st := localLayerState(lc, lc.Name, prev, gen)
	if !st.Unpublished || st.Cause != layerLocalStale {
		return ""
	}
	return "the layer this machine would publish is not the one it was written from: " + st.unpublishedText()
}
