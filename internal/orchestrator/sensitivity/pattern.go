package sensitivity

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Category is what a pattern labels the paths it matches with. It is the
// closed set of words the record, the pull request body and fugaro watch print
// beside a rule's name, so the person reading the pull request can see why a
// run reviewed at the level it did (D1500).
type Category string

// The seven categories. Six are the ask's own words; the seventh is this
// design's, and it is needed: the classifier's sensitiveExceptions holds eight
// paths that fit none of the six — the website's two manifests and the three
// files a reader downloads, verifies or installs, and the docs tree's
// plan-graph artifacts and its two redesign scripts. A vocabulary without a
// value for them is a floor that cannot hold them, and a change to the
// published checksums would then never reach level 3 (D1500, reconciliation
// §6).
const (
	// Credentials is a secret, the code that holds one, or the client that
	// carries it.
	Credentials Category = "credentials"
	// Guards is a write path, a gate or the merge that decides what a run may
	// do.
	Guards Category = "guards"
	// Targets is what a run deploys to.
	Targets Category = "targets"
	// Infrastructure is what a run runs in.
	Infrastructure Category = "infrastructure"
	// Prompts is text an agent is told to obey.
	Prompts Category = "prompts"
	// CI is the pipeline and the release gate.
	CI Category = "CI"
	// Artifacts is what a reader installs, verifies or runs, and what a person
	// re-runs to reproduce a document: the published downloads, checksums and
	// install commands, the website's manifests, and the docs tree's plan
	// graphs and redesign scripts.
	Artifacts Category = "artifacts"
)

// Categories is the whole vocabulary, in the order reconciliation §6's table
// groups its rows. It is closed: a value outside it is refused where it is
// written, never matched as an unknown label at record time (D1500).
var Categories = []Category{Credentials, Guards, Targets, Infrastructure, Prompts, CI, Artifacts}

// KnownCategory reports whether c is one of Categories. The zero Category —
// what a row that forgot to say carries — is not.
func KnownCategory(c Category) bool { return slices.Contains(Categories, c) }

// CategoryList is the vocabulary as a refusal names it: "credentials, guards,
// targets, infrastructure, prompts, CI, artifacts". One spelling of the set,
// read by every refusal that has to say what a value should have been.
func CategoryList() string {
	names := make([]string, len(Categories))
	for i, c := range Categories {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// Pattern is one row of a security list: a name, a doublestar path glob
// relative to the repository root, and the category the paths it matches are
// labelled with.
//
// It is rigor.Area's twin — a name, a glob and the value the rule sets, where
// Area sets a rigor mode and a Pattern names a category — and its glob is
// validated by that rule's own four checks, in that order (F21, D1502). The
// paths it is matched against are repository-relative, which is what
// runstore.Record.ChangedFiles holds.
//
// The name is not decoration. It is what the run record, the pull request body
// and fugaro watch print when they say which rule decided (D24), and a rule
// that cannot be named cannot be explained to the person reading the pull
// request it governed.
type Pattern struct {
	Name     string   `yaml:"name" json:"name"`
	Match    string   `yaml:"match" json:"match"`
	Category Category `yaml:"category" json:"category"`
}

// Bounds on a row's two pieces of text and on how many rows a layer may write.
// All three are rigor.Area's own numbers — MaxAreaName, MaxAreaPattern and
// MaxAreas — because a pattern reaches the same three surfaces a rule name
// does: the run record, the pull request body and fugaro watch.
const (
	// MaxPatternName bounds a row's name.
	MaxPatternName = 40
	// MaxPatternMatch bounds a row's glob.
	MaxPatternMatch = 200
	// MaxPatterns bounds one layer's list of rows. The matched subset of it is
	// what the record, the pull request body and fugaro watch print, so the
	// list a run can be made to carry is bounded where it is written.
	MaxPatterns = 100
)

// patternNameRE is what a row's name may be: a short label of letters, digits,
// space, dash, underscore and dot, starting with a letter or a digit. The name
// reaches three surfaces a person reads, so the set is closed here rather than
// escaped at each of them — a name holding a newline, a terminal control
// character or a bidi override is refused where it is written.
var patternNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)

// Validate is a row's load-time check (D1502). It returns "" for a row the
// matcher can use, and otherwise the one reason it cannot, worded as the
// "must be ..." half of a configuration error (internal/config's Problem prints
// it after the row's path).
//
// The pattern rule is internal/rigor/area.go's, the same four checks in the
// same order: not empty, repository-relative, no ".." component, and a pattern
// doublestar.ValidatePattern accepts. One matcher and one pattern rule, per
// F21.
//
// The refusals after them are this list's own. A row that matches everything is
// the whole-repository answer, which is the project's always_level_3 setting
// and not a pattern (D1503): a `**` that hides that intent is a rule no reader
// of the record can see through. A row that matches nothing is *not* refused —
// the tree may not exist yet, and a project writes its list before its first
// commit to it; doctor is where that is said once (D1503, D1513).
//
// Alternation is the third of this list's own, and it is refused for cost
// rather than for meaning (see hasAlternation): it is the one shape of match
// the matcher cannot bound, and this function is the only place a published row
// is measured — SC7 and SC8 validate a loaded list with these rules and with no
// second check of their own — so the refusal has to be here or nowhere.
func (p Pattern) Validate() string {
	switch {
	case p.Name == "":
		return "must have a name: it is what the run record and the pull request say decided the level"
	case !patternNameRE.MatchString(p.Name):
		return fmt.Sprintf("a name must be 1 to %d characters of letters, digits, space, dash, underscore and dot, starting with a letter or a digit", MaxPatternName)
	case strings.TrimSpace(p.Match) == "":
		return "must have a match: a doublestar path glob relative to the repository root"
	case len(p.Match) > MaxPatternMatch:
		return fmt.Sprintf("a match must be at most %d characters", MaxPatternMatch)
	case strings.HasPrefix(p.Match, "/"):
		return "a match must be relative to the repository root, not start with /"
	case slices.Contains(strings.Split(p.Match, "/"), ".."):
		return "a match must not contain a .. component"
	case !doublestar.ValidatePattern(p.Match):
		return "is not a valid glob pattern"
	case p.Match == "*" || p.Match == "**":
		return "a match must name a tree, not the whole repository: * and ** are the project's always_level_3 setting, and a pattern that matches every path is not a pattern"
	case hasAlternation(p.Match):
		return "a match must not contain alternation: each {a,b} costs doublestar a re-entry, so the groups a bounded row allows are minutes of matching — write one row per tree"
	case !KnownCategory(p.Category):
		return "category must be one of " + CategoryList()
	}
	return ""
}

// hasAlternation reports whether match holds a `{` doublestar would read as the
// start of an alternation. It is the one pattern shape refused for what it
// costs rather than for what it means: doublestar matches `{a,b}` by re-entering
// its matcher once per alternative with the alternative spliced in, so the work
// multiplies with every group — measured on this tree, ten `{a,aa}` groups cost
// 224 µs, twenty cost 199 ms, and the 33 a MaxPatternMatch-long row allows is
// about half an hour, for one row and once per path of a change.
//
// A list a project or a repository publishes is exactly reconciliation §5's
// threat model, and a hang is not a refusal: the run spends its budget on a
// glob instead of failing where the row was written. So the refusal is here,
// where SC7 and SC8 measure a loaded row, because they have no second check of
// their own.
//
// An escaped `\{` is a literal brace to doublestar and is accepted, so the
// refusal is about the alternation and not about the character. A `{` inside a
// character class is refused too, though doublestar would read it literally:
// telling the two apart means re-implementing doublestar's scanner, and
// refusing a spelling nobody writes is the safe direction where hanging a run
// is not.
func hasAlternation(match string) bool {
	escaped := false
	for _, r := range match {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '{':
			return true
		}
	}
	return false
}

// Match reports which of list's rows a change's paths match: the rules that
// decided, in the list's own order, each row once however many of the paths it
// matched. It is what lets a caller name the rule rather than re-read a glob
// (D1500).
//
// It answers the level question and not the class one. A path no row matches
// produces no row here, and that is not a Routine answer: the caller composes
// it with Classify, whose answer for a path it cannot place is Sensitive (F22),
// and the two together are what keeps the fail-closed direction whole.
//
// A path this package cannot place — absolute, escaping, uncleaned, or spelled
// with a byte outside ASCII — is therefore skipped rather than matched by its
// bytes: `docs/./x`, `internal\creds\x.go` and `docs/pending-workflowſ/x.yml`
// are refused by the same cleanRelative the classifier asks, so a spelling that
// means more than one path is placed by no rule at all. That check is asked of
// the path as it was written, before anything else is done to it.
//
// Both sides are then matched in folded ASCII case, because that is the case the
// classifier's own exceptions are matched in (covers) and the two answers have
// to agree on the paths they name. A checkout whose filesystem does not
// distinguish `docs/pending-workflows` from `docs/Pending-Workflows` resolves
// the second spelling into the folder a person copies into .github/workflows, so
// a case-sensitive match here would leave a path Classify calls Sensitive
// matched by no row — a change that never reaches level 3 on the machine that
// can check it out, which is the direction this table exists to close. The
// globs are folded with the paths, not the paths alone, because a row written
// `internal/Creds/**` names the same tree on that machine as `internal/creds/**`
// does; folding both sides only ever adds matches, and a list whose additions
// are all toward level 3 is the list D1506 describes. This is the classifier's
// own rule with one difference: its routine table is still asked in the bytes it
// was written in, so that folding cannot admit a path to the cheaper class —
// this list has no cheaper class to admit anything to.
//
// A row whose glob doublestar cannot read matches nothing for the same reason.
// Every caller merges lists a layer validated at load, so a row that slipped
// past that is a rule that silently never fires rather than a match nobody can
// explain — the failure mode D1502 refuses at load, kept on the safe side here.
//
// The matching is doublestar's, so the cost of a row is doublestar's: linear in
// the path for the stars and `**`, and, for the one shape that is not linear,
// refused where the row is written — `{a,b}` alternation, which Validate declines
// for exactly this reason (hasAlternation) rather than leaving the matcher to be
// the bound. Every row a caller matches has passed that check, so the work here
// is proportional to the paths and the rows, not to what a row's text can make
// doublestar re-explore.
func Match(paths []string, list []Pattern) []Pattern {
	var matched []Pattern
	for _, row := range list {
		match := strings.ToLower(row.Match)
		for _, p := range paths {
			if !cleanRelative(p) {
				continue
			}
			if ok, err := doublestar.Match(match, strings.ToLower(p)); err == nil && ok {
				matched = append(matched, row)
				break
			}
		}
	}
	return matched
}

// Union merges the layers' lists into the one list in force, floor first, a
// project's rows next and a repository's last. A name is a row's identity and
// the first writer's row keeps it, so a later layer adds rows and never
// replaces one: a project that writes the floor's own name for a tree of its
// own cannot take the floor's row away from it, and a repository cannot remove
// a project's row. Nothing intersects and nothing subtracts.
//
// It is unionReviewFloor's direction, one kind of value up (F23, D1506): the
// list is a security control, and the one way it may move is the way that can
// only ever say more — a change may never make its own review cheaper.
func Union(lists ...[]Pattern) []Pattern {
	var out []Pattern
	seen := make(map[string]bool)
	for _, list := range lists {
		for _, row := range list {
			if seen[row.Name] {
				continue
			}
			seen[row.Name] = true
			out = append(out, row)
		}
	}
	return out
}
