package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	"github.com/dimipaun/fugaro/internal/rigor"
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
// It answers nil for anything but a project this program can PROVE is managed:
// a project that is not managed, a client that could not be built, a mark that
// did not answer. The caller then has no gate to consult and refuses the
// session in the command's own words — the refusal this command gave it before
// this row landed — and nothing is recorded.
//
// A read that failed is a refusal and not a diagnostic, which is the answer the
// sibling rows give it for the same reason (accounts_managed.go,
// image_refresh_managed.go): a session that is refused anyway must not learn
// which read broke, which endpoint carries it or what the Storage or Resource
// Manager error said. That is the installation's business, and a coding agent's
// session is not the surface that reads it — the owner's own terminal is, where
// this command's refusal is not the gate's.
//
// The clients are built from the installation's own endpoints when the
// installation is a fake (a test's), and from Google's otherwise. A
// no_auth installation that names no storage or Resource Manager endpoint has
// no cloud to read the marks from at all — there is nothing to be managed
// there, and no call is attempted.
func configPublishGate(ctx context.Context, env *cloudEnv, marker string) *managedGate {
	lc := env.lc
	if lc.Endpoints.NoAuth && (lc.Endpoints.Storage == "" || lc.Endpoints.ResourceManager == "") {
		return nil
	}
	s, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return nil
	}
	c, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return nil
	}
	g, err := readManagedGate(ctx, managedGateEnv{lc: lc, s: s, crm: c}, managedGateFacts{
		Subcommand:  "config publish",
		Argv:        os.Args[1:],
		Declined:    "nothing was published",
		AgentMarker: marker,
	})
	if err != nil || !g.managed {
		return nil
	}
	return &g
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
//
// local says the bytes being published ARE this machine's own layer file, which
// is the whole of D854's subject: the rule is about the file this machine reads
// back and the generation it was written from. `fugaro config publish FILE`
// publishes a layer kept somewhere else — the command's own record of that
// difference is rec.lc — and the state of the unrelated local file says nothing
// about the document under judgement: checking it there would refuse a publish
// whose subject is a different file, and would put a rule in the record that
// never ran. Every other rule is decided against the published object and runs
// either way.
//
// marker is the session's own environment marker, exactly the value the gate's
// own facts carry (managedGateFacts.AgentMarker, read once by the command), so
// the rules that bind an agent's session and the record that names it cannot
// disagree about which session this is. The direction rules — the rounds and
// the review levels, the same ones `fugaro settings set` holds an agent's
// session to (D1027, owner's ruling of 2026-11-06) — are decided under it and
// are the only rules here that are: every other rule is about the document and
// binds whoever publishes it.
func configPublishConverge(ctx context.Context, env *cloudEnv, data []byte, local bool, marker string) (managedmode.Converge, *layerAt) {
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
		// An object that is not a layer this build can read is a predecessor
		// no rule can be decided against, and that is a refusal rather than a
		// predecessor of nothing. Measured against nil every cap, claim and
		// floor of the new document counts as new — which for the executable
		// keys is decision L7's fail-closed default, but for the widening and
		// rigor rules is an admission nobody made: the document could be
		// lowering a floor the unreadable object held, and the record would
		// still say the rigor rule ran and passed. The count is named and the
		// object is not echoed, for publishLayer's own reason: bytes nobody
		// validated may hold a credential a hand edit put there.
		if op, invalid := config.ParseProjectLayer(old, config.LayerAnchor{}); len(invalid) == 0 {
			prev = op
		} else {
			rules = append(rules, fmt.Sprintf("the published project layer is not a layer this program can read (%d problem(s)), so the caps, claims and rigor floors this write replaces cannot be judged against it, and the project's owner publishes from their own terminal", len(invalid)))
			return managedmode.ConvergeRefused(data, initflow.YesOrTyped, rules), at
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
	rules = append(rules, layerRigorRules(prev, l)...)
	rules = append(rules, layerRoundsRules(prev, l, at.old, marker)...)
	rules = append(rules, layerAllowlistRules(at.old, data, marker)...)
	if local {
		if r := layerStaleRule(env.lc, prev, gen); r != "" {
			rules = append(rules, r)
		}
	}
	if len(rules) > 0 {
		return managedmode.ConvergeRefused(data, initflow.YesOrTyped, rules), at
	}
	return managedmode.ConvergeAdmitted(data, initflow.YesOrTyped, layerAdmittedRules(local, marker)), at
}

// layerAdmittedRules is what a record of an admission says was applied and
// passed: the rules above, in the order the converge applies them. They are
// written out rather than derived from the functions, because a record has to
// be checkable by a person months later without reading this build's code —
// which is also why D854's sentence is in the list only when D854 ran: a record
// of a `config publish FILE` that claimed the local file's generation was
// checked would name a rule about a file that was never the document under
// judgement. The rounds rule's sentence is written in only when a marker was
// there for it to bind, by the same rule: a person's own publish is not held to
// it, and a record that named it would claim a rule that never ran.
func layerAdmittedRules(local bool, marker string) []string {
	rules := []string{
		"every key of the document is one the settings registry knows",
		"the document names no reserved key",
		"the document is one this program can publish",
		"no key that runs as shell changes, whatever the command line says",
		"the document widens no model claim, no A/B route and no cap",
		"the document lowers no rigor floor and removes no rigor rule",
	}
	if marker != "" {
		rules = append(rules,
			"the document lowers no review round and turns no review level off, level 1's own first-line switch and count included (the floor keys D1027 holds a coding agent's session to)",
			"the document changes no key outside the allowlist D1027 gives a coding agent's session (the settings door's own list, which admits the two round counts above and nothing else)")
	}
	if local {
		rules = append(rules, "this machine's local layer file was written from the published layer's generation")
	}
	return rules
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
// (`defaults.rigor.areas.*.mode`). See layerWalkKeys for the walk itself.
func layerKeyPaths(data []byte) ([][]string, bool) {
	var paths [][]string
	if !layerWalkKeys(data, func(path []string, _ []string) {
		paths = append(paths, slices.Clone(path))
	}) {
		return nil, false
	}
	return paths, true
}

// layerKeyValues is one document as the comparison layerAllowlistRules makes
// reads it: every path the same walk yields, under the key the registry spells
// it with (layerPathKey), and the value(s) the document holds there. A path
// written more than once — the `*` of a list of blocks stands for each of its
// elements — carries one entry per element, in document order.
func layerKeyValues(data []byte) (map[string][]string, bool) {
	out := map[string][]string{}
	if !layerWalkKeys(data, func(path []string, value []string) {
		key := layerPathKey(path)
		out[key] = append(out[key], value...)
	}) {
		return nil, false
	}
	return out, true
}

// layerWalkKeys is the walk both readings above are made of: it visits every
// leaf of a layer document and every key the registry knows, and answers false
// for a document it cannot follow.
//
// The walk stops at a path the registry knows, and that is the whole reason it
// has to be a walk: `layer.claude_models` is one registry row holding a block
// of its own, so descending into it would judge provider and model names
// against keys the registry deliberately does not have.
//
// A document it cannot follow is a refusal rather than an empty reading: YAML
// it cannot parse, a key that is not a string, and an anchor or the alias that
// copies it (a merge key included, which is an alias under another name) are
// each answered false, and both callers refuse the document on that answer.
func layerWalkKeys(data []byte, visit func(path []string, value []string)) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return false
	}
	var walk func(path []string, n *yaml.Node) bool
	walk = func(path []string, n *yaml.Node) bool {
		if n.Anchor != "" || n.Alias != nil {
			return false
		}
		if len(path) > 0 && layerRegistryExact(path) {
			visit(path, []string{layerNodeValue(n)})
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
				visit(path, []string{layerNodeValue(n)})
				return true
			}
			for _, e := range n.Content {
				if !walk(append(slices.Clip(path), "*"), e) {
					return false
				}
			}
		default:
			visit(path, []string{layerNodeValue(n)})
		}
		return true
	}
	return walk(nil, doc.Content[0])
}

// layerNodeValue is the node at a key as the comparison reads it: a scalar's
// own tag and text, and a block or a list rendered member by member. Two
// documents that say the same thing therefore compare equal however they were
// written — a re-indent, a flow style or a re-ordered mapping is not a change —
// and two that say different things never do. The tag is part of the value
// because a re-typed scalar (`5` written as `"5"`) is a different document,
// which the fail-closed direction of every read of this gate answers with a
// refusal rather than with a comparison nobody can make.
func layerNodeValue(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		members := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			members = append(members, n.Content[i].Value+"="+layerNodeValue(n.Content[i+1]))
		}
		slices.Sort(members)
		return "{" + strings.Join(members, ", ") + "}"
	case yaml.SequenceNode:
		members := make([]string, 0, len(n.Content))
		for _, e := range n.Content {
			members = append(members, layerNodeValue(e))
		}
		return "[" + strings.Join(members, ", ") + "]"
	}
	return n.ShortTag() + " " + n.Value
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

// layerRigorRules names every rigor floor the layer removes: a
// defaults.rigor.default lowered or dropped, and a defaults.rigor.areas rule
// lowered or dropped, both compared by rigor.Mode.Rank().
//
// This is one of the two directions the gate refuses for admitting LESS rather
// than more — the review rounds a run spends are the other (layerRoundsRules,
// below) — and it is here because a floor is what the run's own rigor resolution
// starts from (D20c): removing one does not make the project's runs cheaper or
// smaller, it makes them weaker, and a mode a run is held to is exactly what a
// session has no business deciding about itself. Owner-ratified by D1026: a
// floor is removed at a person's own terminal, which is why a refusal here
// reads like the widenings and not like the caps.
//
// Raising a floor, adding one, and adding a rigor rule are all admitted: they
// take nothing away, and a run resolves the strictest of the floors it finds.
func layerRigorRules(prev, next *config.ProjectLayer) []string {
	if prev == nil || next == nil {
		return nil
	}
	var rules []string
	add := func(what string) {
		rules = append(rules, "the layer "+what+", which is a floor no coding agent's session removes: the project's owner publishes it at their own terminal")
	}
	if was := prev.Defaults.Rigor.Default; was != "" {
		now := next.Defaults.Rigor.Default
		switch {
		case now == "":
			add("removes defaults.rigor.default, which was " + string(was))
		case now.Rank() < was.Rank():
			add("lowers defaults.rigor.default from " + string(was) + " to " + string(now))
		}
	}
	was := make(map[string]rigor.Mode, len(prev.Defaults.Rigor.Areas))
	for _, a := range prev.Defaults.Rigor.Areas {
		was[a.Name] = a.Mode
	}
	for _, a := range next.Defaults.Rigor.Areas {
		held, ok := was[a.Name]
		if !ok {
			continue
		}
		delete(was, a.Name)
		switch {
		case a.Mode == "":
			add("removes the rigor rule " + safetext.Field(a.Name) + "'s mode, which was " + string(held))
		case a.Mode.Rank() < held.Rank():
			add("lowers the rigor rule " + safetext.Field(a.Name) + " from " + string(held) + " to " + string(a.Mode))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(was)) {
		add("removes the rigor rule " + safetext.Field(name) + ", which was " + string(was[name]))
	}
	return rules
}

// layerRoundsKeys are the round counts a coding agent's session may only
// raise, as the document spells them (the registry's own rows for them are
// these with a `layer.` prefix, and settingsValueIn reads the document's
// spelling). The first two are D1027's allowlist itself; the third is the
// first-line loop's count, which the settings door refuses outright — it is
// not a key that allowlist admits — so it is here for its direction only: a
// raise takes nothing away and this door judges directions, where a lowering or
// a removal is the level-1 review a session would be deciding for the project.
//
// `row` is the mode's own row for the loop the key counts, the base
// settingsFloorBaseRow falls back to where the layer sets no number of its own:
// the two counts the senior review spends, and the level-1 count's own loop.
var layerRoundsKeys = []struct {
	key  string
	path []string
	row  int
}{
	{"defaults.agent.review_rounds", []string{"defaults", "agent", "review_rounds"}, rigor.DefaultRounds(rigor.Critical).Senior},
	{"defaults.agent.adversarial_rounds", []string{"defaults", "agent", "adversarial_rounds"}, rigor.DefaultRounds(rigor.Critical).Senior},
	{"defaults.agent.first_line_rounds", []string{"defaults", "agent", "first_line_rounds"}, rigor.DefaultRounds(rigor.Critical).Inner},
}

// layerFirstLineReviewPath is the second key that turns review level 1 off:
// the levels block's `l1: { none: true }` is one spelling of a level that does
// not run, and `first_line_review: off` (config.FirstLineOff) is the one the
// first-line step itself reads.
var layerFirstLineReviewPath = []string{"defaults", "agent", "first_line_review"}

// layerRoundsRules names every direction this layer moves the review a run
// spends, for a coding agent's session and no one else: a round count lowered
// or removed, a review level turned off (`defaults.agent.levels.*.none`), and
// review level 1 turned off by the first-line step's own switch
// (`defaults.agent.first_line_review: off`, whose key is not in the levels
// block and is refused at the settings door all the same).
//
// Why this door needs the rule and not only the settings door. The two commands
// write the same published layer — `fugaro settings set` writes one key of it,
// `fugaro config publish` writes the whole document — and D1027's allowlist and
// direction rule are the owner's decision about what a session may move, not
// about which command it names to move it. A session with the whole document in
// hand can publish a lower `review_rounds`, or turn `levels.l2` off, around a
// rule that only judges single-key writes; the rounds are the review a run
// spends and a level that does not run spends none, so the direction is the same
// one layerRigorRules refuses on the floor beside it (D20c, D1026).
//
// Why it binds a coding agent's session and not a person. The owner's ruling of
// 2026-11-06: the allowlist and the floors are what a session may move on its
// own, and a person at their own terminal decides the project's review for it
// (#1793, #1795, settingsFloorRule's own reading). A person's publish does not
// even reach this converge — the command reads no mark and builds no gate
// without a marker (config_publish.go) — and the rule is written to say so,
// because a rule that named a person's write among the ones it judged would be
// a record of something that did not happen (D1014).
//
// The base is settingsFloorBaseRow's, over the bytes of the layer this document
// replaces (at.old, the object the rules already judge against): one function
// decides what "the rounds a run spends today" is, so the two doors cannot
// disagree about it — a layer that sets no number of its own is held to the
// strictest mode's row for that key's loop at both. A key neither document sets
// is not a change and is admitted, which is what keeps an unchanged layer
// publishable.
//
// The first-line switch is judged in two directions: `off` turns the level
// off outright, and a layer that said `on` and now says `auto` (or says
// nothing, which reads as auto) hands the level to the run's models, where
// config.Agent.FirstLineOn is false for an Anthropic coder, so an explicit
// `on` is a level a session may not give up. Where the layer did not say `on`,
// `auto` leaves the level to the run's models, which the two documents in hand
// do not settle, and a rule that guessed would move a level that may not have
// moved: a layer that says off already, and one whose switch is set back to
// `on` or `auto`, take nothing away and pass.
func layerRoundsRules(prev, next *config.ProjectLayer, published []byte, marker string) []string {
	if marker == "" || next == nil {
		return nil
	}
	var rules []string
	add := func(what, verb string) {
		rules = append(rules, "the layer "+what+", which is a floor no coding agent's session "+verb+": the project's owner publishes it at their own terminal")
	}
	for _, k := range layerRoundsKeys {
		wasText := settingsValueIn(published, k.path)
		now := settingsValueIn(next.Raw, k.path)
		if now == "" {
			// The document leaves no number: taking one away is refused where
			// the layer it replaces set one, and the zero that layer's own
			// reader treats as unset is no number to take away
			// (settingsFloorBase's own reading).
			if was, ok := settingsRounds(wasText); ok && was > 0 {
				add("removes "+k.key+", which was "+safetext.Field(wasText), "removes")
			}
			continue
		}
		value, ok := settingsRounds(now)
		if !ok {
			// Not a number: the layer parse refuses the document before this
			// rule runs, so this is unreachable and no rule is claimed for it.
			continue
		}
		if base, baseText := settingsFloorBaseRow(published, k.path, k.row); value < base {
			add("lowers "+k.key+" from "+baseText+" to "+safetext.Field(now), "lowers")
		}
	}
	for _, l := range layerLevelOff(prev, next) {
		add("turns the review level defaults.agent.levels."+l+" off (none: true), so a run of the project spends less review", "turns off")
	}
	was, now := settingsValueIn(published, layerFirstLineReviewPath), settingsValueIn(next.Raw, layerFirstLineReviewPath)
	if (now == config.FirstLineOff && was != config.FirstLineOff) || (was == config.FirstLineOn && now != config.FirstLineOn) {
		add("turns review level 1 off (defaults.agent.first_line_review: off), so a run of the project spends no first-line review", "turns off")
	}
	return rules
}

// layerAllowlistRules names every key this document changes, against the layer
// it replaces, that D1027's allowlist does not admit for a coding agent's
// session — and it is empty for a person, whose own terminal the owner's ruling
// of 2026-11-06 leaves to every key of the layer.
//
// Why the whole document and not the keys the rules above read. `fugaro config
// publish` writes the whole published layer and `fugaro settings set` writes
// one key of it, and D1027 is the owner's decision about what a session may
// move, not about which command it names to move it. The rules above judge the
// DIRECTIONS the layer writes could weaken a run with — a cap, a claim, a
// floor, a round — and every one of them admits a change that takes nothing
// away: a recipe the project did not run, another reviewer model, a level's
// model, effort or focus, a key no rule here reads at all. Each of those is one
// the settings door refuses a session outright, so a session with the whole
// document in hand could otherwise reach through this door every key the
// settings door holds it back from, which is the hole the owner's ruling names.
// The list itself is the settings door's (settingsManagedKeys) and not a second
// copy of it: one writer of the list is the whole point, because two would be
// two answers to what a session may move and the drift between them is the hole
// again — and the direction rule over the two keys it does admit stays where it
// is, in layerRoundsRules, which judges a raise of them and nothing else.
//
// The comparison is of the two documents key by key (layerKeyValues), and it is
// a comparison of documents rather than of bytes: an added key, a removed key
// and a changed key are each a change, and a block re-indented, a mapping
// re-ordered or a value written in flow style is not. What the walk cannot read
// is already refused: a document whose keys it cannot follow is refused by
// layerDocumentRules, and a published object the parse cannot read is refused
// by configPublishConverge before any rule is decided against it — so this rule
// claims no comparison there rather than naming one that never ran.
//
// A published layer that is NOT THERE yet is the fail-closed case, and the one
// this rule cannot delegate: with no predecessor there is no document the
// new one's keys can be shown unchanged in, so every key of it counts as added
// and a session that has never published a layer may publish only the
// allowlist's keys. The alternative — reading a missing predecessor as "nothing
// changed" — would admit the first publish of any document at all, which is the
// hole with a fresh installation in front of it. A predecessor the bucket could
// not be read from at all is refused earlier still, by the rule that says so.
func layerAllowlistRules(published, next []byte, marker string) []string {
	if marker == "" {
		return nil
	}
	was := map[string][]string{}
	if len(published) > 0 {
		var ok bool
		if was, ok = layerKeyValues(published); !ok {
			// Unreachable today: the object is read as a layer before this rule
			// runs, and every shape this walk refuses is one that parse refuses
			// too. Kept because the answer is the fail-closed one, and the other
			// answer — comparing against nothing — admits the whole document.
			return []string{"the published project layer's keys cannot be read as a document this gate compares, so no key of this write was judged against it, and the project's owner publishes from their own terminal"}
		}
	}
	now, ok := layerKeyValues(next)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(was)+len(now))
	for k := range was {
		keys = append(keys, k)
	}
	for k := range now {
		if _, held := was[k]; !held {
			keys = append(keys, k)
		}
	}
	var changed []string
	for _, key := range sortedUnique(keys) {
		if settingsManagedAdmits(key) {
			continue
		}
		if !slices.Equal(was[key], now[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return []string{"the document changes the key(s) " + safetext.FieldListLine(changed) +
		", which are no keys the gate's allowlist admits for a coding agent's session: it admits " +
		strings.Join(settingsManagedKeys, " and ") + " and nothing else, each only raised, so every other key of the layer is the project's owner's to publish at their own terminal"}
}

// layerLevelOff is the name of every review level next turns off that the layer
// it replaces had on: l1, l2 and l3, in that order. A level already off stays
// off without a rule (nothing moved), and a level turned back on is a raise the
// rule admits.
func layerLevelOff(prev, next *config.ProjectLayer) []string {
	var was config.ReviewLevels
	if prev != nil {
		was = prev.Defaults.Agent.Levels
	}
	now := next.Defaults.Agent.Levels
	var out []string
	for _, l := range []struct {
		name     string
		was, now *config.LevelSetting
	}{
		{"l1", was.L1, now.L1},
		{"l2", was.L2, now.L2},
		{"l3", was.L3, now.L3},
	} {
		if l.now != nil && l.now.Off && (l.was == nil || !l.was.Off) {
			out = append(out, l.name)
		}
	}
	return out
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
