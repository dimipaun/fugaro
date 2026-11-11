package config

import "github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"

// The one resolution of what a run treats as security-sensitive (docs/
// security-classification/implementation-plan.md SC9, F10, F23, D1506,
// D1511): the engine's floor, the project layer's rows and the repository's
// own, merged into the one list in force, each row carrying the layer it came
// from.
//
// It is a union and never a replacement. A name is a row's identity and the
// first writer's row keeps it, so a repository with no key gets the project
// layer's rows and the floor's, a project with no key gets the floor, a
// repository's row can never take a project's row away from it, and a list
// nothing was written to is the floor (D1504, D1506). Nothing is recorded of a
// row the union dropped, which is unionReviewFloor's own rule for a list (F23):
// a union has no loser — the rows a layer asked for are all in force, and the
// rows it left out are the ones no layer asked for.
//
// It is one exported name rather than a promise, which is D1511's "one source
// of truth": every reader of the list — `fugaro security paths`, doctor's line
// and the runner's escalation — calls this function and none of them reads the
// floor directly any more, so a layer's rows reach all three or none. The
// classifier's own tables are deliberately not part of it: they answer which
// model a decision runs on, this answers which review level a change reaches,
// and the two are the same dialect with different questions (D1511).

// SecuritySource is the layer a row of the list in force came from. It is the
// `source` D1512's document prints per row and per answer, out of its three
// words.
type SecuritySource string

const (
	// SecuritySourceFloor is the engine's own list (sensitivity.Floor, D1505):
	// what Fugaro has always called security-sensitive, before any project and
	// any repository says anything. It is the union's first writer, so no
	// layer can take a row of it away — a layer cannot even write one of its
	// names (validateSecurity refuses the repeat at load).
	SecuritySourceFloor SecuritySource = "floor"
	// SecuritySourceProject is the project layer's security: block (D1501):
	// what every repository of the project treats as security-sensitive.
	SecuritySourceProject SecuritySource = "project"
	// SecuritySourceRepo is the repository's own security: block (D1500): what
	// this repository adds to its project's list.
	SecuritySourceRepo SecuritySource = "repo"
)

// SecurityRow is one row of the list in force with the layer it came from.
type SecurityRow struct {
	// Pattern is the row itself: a name, a doublestar path glob relative to
	// the repository root, and the category the paths it matches are labelled
	// with (sensitivity.Pattern).
	Pattern sensitivity.Pattern
	// Source is the layer the row is in force from. Where two layers wrote one
	// name it is the first of them, which is whose row it is (sensitivity.Union).
	Source SecuritySource
}

// SecurityList is the list in force, in the order a run matches it: the
// floor's rows, then the project layer's, then the repository's. Rows may be
// added to a layer's list but never removed from it, so the list a repository
// with no key reads is a prefix of the list its project's busiest repository
// reads.
type SecurityList []SecurityRow

// Patterns is the list in force's rows alone, in force order: what
// sensitivity.Match reads, and what a caller that wants to name the row that
// decided filters for. The source is the caller's to look up here — a name is
// a row's identity in this list, so the row Match answered with is the one row
// of this list that carries it.
func (l SecurityList) Patterns() []sensitivity.Pattern {
	out := make([]sensitivity.Pattern, len(l))
	for i, row := range l {
		out[i] = row.Pattern
	}
	return out
}

// SecurityPatterns is the list in force over the two layers a caller holds:
// repo is the repository's own security: block and layer the project layer
// that applies to it, either of which may be nil — a repository that writes no
// key, and a project no layer applies to, are both ordinary states and neither
// is a list that says less than the floor (D1504).
//
// They are two arguments rather than one resolved Config because the two
// callers hold them differently: the runner holds the config it resolved at
// bootstrap (cfg.Security and cfg.Layer), while `fugaro security paths` reads
// one file's own text at a ref and never resolves a config at all — the
// resolution reads the repository's block and the layer's and nothing else of
// either, so neither caller has to build the other half to ask.
func SecurityPatterns(repo *Security, layer *ProjectLayer) SecurityList {
	var project, own []sensitivity.Pattern
	if layer != nil && layer.Security != nil {
		project = layer.Security.Patterns
	}
	if repo != nil {
		own = repo.Patterns
	}

	floor := sensitivity.Floor()
	source := make(map[string]SecuritySource, len(floor)+len(project)+len(own))
	mark := func(rows []sensitivity.Pattern, s SecuritySource) {
		for _, row := range rows {
			// The first writer's row keeps the name (sensitivity.Union), and
			// the map keeps the same one: a row the union drops is not in the
			// list in force and so is not a source of anything.
			if _, ok := source[row.Name]; !ok {
				source[row.Name] = s
			}
		}
	}
	mark(floor, SecuritySourceFloor)
	mark(project, SecuritySourceProject)
	mark(own, SecuritySourceRepo)

	merged := sensitivity.Union(floor, project, own)
	out := make(SecurityList, 0, len(merged))
	for _, row := range merged {
		out = append(out, SecurityRow{Pattern: row, Source: source[row.Name]})
	}
	return out
}

// SecurityOf reads the security: block out of a fugaro.yaml's own bytes, and
// says whether the file could be read at all.
//
// It is AgentOf's shape and for AgentOf's reason: `fugaro security paths` and
// doctor read one file's own text — the base branch's, out of the checkout's
// object database — and neither resolves a whole config to ask what the file
// says about security. Resolve would answer a different question: it refuses a
// file that needs a project layer for something else, and a repository's own
// rows are still that file's own rows.
//
// ok is false for bytes this Fugaro cannot read as a fugaro.yaml at all — the
// strict decode's own refusal, an unknown field, a duplicate key — which is
// "the file is not this", never "the file writes no block": a file that writes
// no security: key answers with a nil block and true. The rows are as written
// and are not validated here, which is deliberate: a file a run would refuse at
// load is a file no run of this repository exists for, and the matcher skips a
// glob it cannot read (sensitivity.Match) rather than matching by its bytes.
func SecurityOf(data []byte) (*Security, bool) {
	c, ps := decodeRepo(data)
	if c == nil || len(ps) > 0 {
		return nil, false
	}
	return c.Security, true
}
