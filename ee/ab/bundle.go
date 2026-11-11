// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package ab is the policy half of model A/B testing (D550): what may be
// sampled, what a pair means once its two runs are done, and what a judge is
// allowed to see. The wire — the task's and the record's `ab` block, the patch
// objects both arms upload — and the shadow arm that writes them stay in the
// core.
//
// This file is the anonymiser (F10, D552): the two patch objects of a pair
// become the input a blind judge is shown, labelled A and B by a swap seeded
// from the pair ID, with every identifier of the pair's runs refused and every
// model word redacted. Nothing here reads the bucket or a record, so a caller
// holding the two patches and the pair ID gets the judge's input — which is
// what makes blindness checkable in a unit test (A5.1, Check 211).
package ab

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/task"
)

// Patch is one arm's material for the judge: the patch object A2.1 stored
// under the run's own prefix (runs/<repo-slug>/<run-id>/ab/patch.diff) with
// the identifiers of the run that wrote it. It is a plain value, so the reader
// that lists a pair's two records builds it without this package reaching the
// bucket.
type Patch struct {
	// Diff is the stored tree diff, as the bucket holds it: the change from
	// the run's start commit to its final head. A2.1 diffs two trees, so
	// there is no commit metadata in it to strip — and nothing here adds
	// any.
	Diff string
	// Truncated says Diff is only the first part of the run's real diff
	// (runstore.Record.PatchTruncated). Half a change cannot be judged
	// against a whole one, so a truncated patch leaves the pair unjudged
	// rather than handing the judge a diff that stops mid-hunk.
	Truncated bool
	// RunID is the run's ID (runstore.Record.RunID), and Branch the branch it
	// worked on (Record.Branch, fugaro/<run-id>). A diff carrying either
	// names the run, and the record it names carries the model: both are
	// refused.
	RunID  string
	Branch string
	// Model is the model that wrote the diff: the one the arms differ in
	// (D555), which is the challenger's model for the challenger's run and the
	// champion's for the champion's. It joins the redaction vocabulary below,
	// because the arms differ in this and nothing else, so a diff naming its
	// own arm's model tells the judge which arm it is reading.
	//
	// It is not optional decoration and "" is not the same pair: a challenger
	// may be a model this build's price table does not carry (an owner prices
	// it under model_prices, D556), and that is the challenger whose own diff
	// would otherwise name it. A reader that has the record has this field on
	// it; one that does not leaves it "" and gets the table's words alone.
	Model string
}

// JudgeInput is what one pass of the judge's call sees: the pair's two diffs
// under the labels A and B, and nothing else. No run ID, no branch, no model
// ID, no commit metadata and no cost — each of those is either redacted (a
// model word becomes modelPlaceholder) or a reason the pair is not judged at
// all.
type JudgeInput struct {
	// A and B are the two diffs, in the order the judge is shown them.
	A, B string
}

var (
	// ErrTruncated is the pair unjudged because a patch object was cut at the
	// cap A2.1 stores it under: the judge would compare half a change with a
	// whole one. A5.4 counts these per group.
	ErrTruncated = errors.New("the patch object is truncated")
	// ErrIdentified is the pair unjudged because a diff carries something
	// that names a run: it is a line the judge could follow to the record,
	// and the record names the model that wrote the arm.
	ErrIdentified = errors.New("the patch carries a run identifier")
)

// modelPlaceholder is what every model word in a diff becomes. One
// placeholder for every model, in both arms alike: a distinct placeholder per
// model — or per arm — would hand the judge the very label the bundle exists
// to remove.
const modelPlaceholder = "[model]"

// minModelWord is the shortest word the redaction replaces, and it is what
// keeps the vocabulary from eating the diff: the match is a substring, so a
// two- or three-letter word lands inside ordinary text — "pro" in provider and
// process, "k3" in task3 — and a judge handed "ap[model]ve" is reading a diff
// the pair is judged on. internal/redact's redactor draws the same line at 4
// bytes for the same reason. What it costs is the shortest spelling of a name
// alone: the ID and the vendor word that carry it are longer and stay in the
// vocabulary, so a diff that spells the model out is still redacted whole.
const minModelWord = 4

// Bundle builds the judge's input for one pair from its two patch objects.
// The champion's patch is the first argument.
//
// The labels are seeded by the pair ID alone, so the same pair is labelled the
// same way on every judge run and a verdict over it is reproducible (D563).
// swap reports which arm A holds: the challenger's diff when it is true, the
// champion's when it is false. The second pass (D552: the same two diffs
// judged with the order swapped) is this input with A and B exchanged and swap
// inverted — nothing else about the two passes differs, which is what makes a
// disagreement between them a tie rather than a second opinion.
//
// A patch that is truncated, or a diff that carries an identifier of a run, is
// refused instead of judged: an unblind verdict is worse than none. A diff
// naming a model is not refused but redacted, in both arms alike, so the tasks
// that touch configuration or pricing — exactly the ones whose diffs spell a
// model ID — are not systematically unjudged.
func Bundle(champion, challenger Patch, pairID string) (JudgeInput, bool, error) {
	if strings.TrimSpace(pairID) == "" {
		return JudgeInput{}, false, errors.New("a bundle needs the pair ID its labels are seeded by: with none, every pair would be labelled the same way")
	}
	for _, side := range []struct {
		name  string
		patch Patch
	}{{"the champion", champion}, {"the challenger", challenger}} {
		if side.patch.Truncated {
			return JudgeInput{}, false, fmt.Errorf("%w: %s's patch is only the first part of its diff, and half a change cannot be judged against a whole one (A2.1)", ErrTruncated, side.name)
		}
		if what := identifierIn(side.patch.Diff, pairID, champion, challenger); what != "" {
			return JudgeInput{}, false, fmt.Errorf("%w: %s's diff carries %s, and the record it leads to names the model that wrote the arm", ErrIdentified, side.name, what)
		}
	}
	words, err := modelWords(champion.Model, challenger.Model)
	if err != nil {
		return JudgeInput{}, false, err
	}
	redact := modelRedactor(words)
	champ, chall := redact(champion.Diff), redact(challenger.Diff)
	if swapFor(pairID) {
		return JudgeInput{A: chall, B: champ}, true, nil
	}
	return JudgeInput{A: champ, B: chall}, false, nil
}

// swapFor is the seeded swap (D552, F10): the pair ID's hash folded to one bit
// decides which arm is labelled A. It is a property of the pair and of nothing
// else — not of the caller, not of the diffs — so a judge run cannot choose the
// labelling, and a run ID cannot steer it (D558 excludes a chosen --run-id from
// sampling for the same reason).
//
// The seed is SHA-256 rather than a multiplicative hash, whose low bit is an
// exclusive-or of the input's low bits: a pair ID chosen to set that bit would
// fix which arm the judge sees first, and the exclusion D558 rests on is a
// property of sampling rather than of this call. A digest exposes no such
// lever, so a pair ID steers nothing here whatever chose it.
func swapFor(pairID string) bool {
	sum := sha256.Sum256([]byte(pairID))
	return sum[0]&1 == 1
}

// identifierIn names what in the diff gives a run away, or "" when the diff is
// clean. The pair's own runs are checked by their exact spelling — their IDs,
// their branches and the pair ID, which is the champion's run ID in practice
// and names the pair either way — and then by shape: a run ID spelled any
// other way, another run's or one quoted inside a path, still tells the judge
// that a harness wrote this and which runs it belongs to.
//
// Both checks fold case, because folding is what a diff does to a quoted ID as
// easily as a person does: "FUGARO/20260101-120000-ABCD" is the same run, and
// task.ValidRunID is lowercase-only, so an unfolded check would hand the judge
// exactly what it refuses to hand it. For the same reason the shape check runs
// over text rather than over tokens: a diff glues an ID to what surrounds it
// (a hash, a word, "…-cafeclaude"), and a token boundary misses the ID inside.
//
// It reads the diff as stored, before the redaction. That is the complete
// check, not a cheaper one: a replacement is modelPlaceholder, whose characters
// are not in a run ID's alphabet, so it can neither hide an ID (all twenty of a
// redacted ID's characters come from the stored text, contiguously) nor join
// two fragments into one that was not there.
func identifierIn(diff, pairID string, arms ...Patch) string {
	if diff == "" {
		return ""
	}
	folded := strings.ToLower(diff)
	for _, arm := range arms {
		switch {
		case arm.RunID != "" && strings.Contains(folded, strings.ToLower(arm.RunID)):
			return "the run ID " + arm.RunID
		case arm.Branch != "" && strings.Contains(folded, strings.ToLower(arm.Branch)):
			return "the branch " + arm.Branch
		}
	}
	if pairID != "" && strings.Contains(folded, strings.ToLower(pairID)) {
		return "the pair ID " + pairID
	}
	if id := runIDLike(diff); id != "" {
		return "the run ID " + id
	}
	return ""
}

// runIDGuess is a run ID's shape as an unanchored, case-insensitive search.
// Every match is confirmed by task.ValidRunID — lowercased first, since the
// launcher mints lowercase but a diff may spell an ID any way — so task stays
// the authority on what a run ID is, and this is only what finds the candidates
// to ask about.
//
// The guess is deliberately looser than that authority (it may match text that
// is not a run ID, which costs one call and decides nothing) and it must never
// be tighter: a shape ValidRunID accepts and this misses is another run's ID
// reaching the judge, while the pair's own runs are caught by their exact
// spelling whatever shape a run ID grows into. task's own pattern is unexported,
// so it is spelled out here; this finds the candidates and ValidRunID decides.
var runIDGuess = regexp.MustCompile(`(?i)[0-9]{8}-[0-9]{6}-[0-9a-f]{4}`)

// runIDLike is a run ID the text carries anywhere, or "" — another run's ID,
// or one glued to its neighbours.
func runIDLike(text string) string {
	for _, m := range runIDGuess.FindAllString(text, -1) {
		if task.ValidRunID(strings.ToLower(m)) {
			return m
		}
	}
	return ""
}

// modelRedactor is the pass Bundle applies to one diff: every word that names
// a model becomes modelPlaceholder. The alternatives are longest first,
// because the match is leftmost-first: a full ID is replaced whole rather than
// leaving its version ("[model]-5-5") behind.
func modelRedactor(words []string) func(string) string {
	if len(words) == 0 {
		return func(diff string) string { return diff }
	}
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		quoted = append(quoted, regexp.QuoteMeta(w))
	}
	re := regexp.MustCompile(`(?i)` + strings.Join(quoted, "|"))
	return func(diff string) string { return re.ReplaceAllString(diff, modelPlaceholder) }
}

// modelWords is every word a diff must not carry, longest first: for each
// model, its ID and its aliases, the name it is served under, the vendor family
// it belongs to (config.ModelFamily: a Claude ID is "anthropic", a provider ID
// its vendor prefix, D553) and the words its name is built from before its
// version. An arm's own model comes first — that is the one whose naming
// unblinds the pair — and then the build's price table: the embedded Claude rows
// and the embedded provider catalog.
//
// The table is the build's own because the judge run is Fugaro's, whatever
// repository the pair came from: it is what a judge could look a name up in.
// The two arm models are what it does not carry — an owner prices a challenger
// under model_prices (D556) that this build's table has no row for — so they
// are handed in rather than looked up, and the pair's own arms are covered
// whatever prices them.
//
// A third model an owner prices and the build's table does not name stays in
// the diff that names it: this function sees the pair's two arms and the
// build's own table, and the repository's effective table is a record rather
// than a patch. The arms are the ones whose naming unblinds the pair, and the
// rest is the gap named above rather than one this function can close.
func modelWords(armModels ...string) ([]string, error) {
	words := map[string]bool{}
	put := func(word string) {
		if len(word) >= minModelWord {
			words[strings.ToLower(word)] = true
		}
	}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		put(id)
		name := id
		if i := strings.LastIndexByte(id, '/'); i >= 0 {
			name = id[i+1:]
		}
		put(name)
		put(config.ModelFamily(id))
		for _, w := range strings.FieldsFunc(name, isNameSep) {
			if versionWord(w) {
				break
			}
			put(w)
		}
	}
	for _, model := range armModels {
		add(model)
	}
	for _, m := range pricing.Embedded().Models {
		add(m.ID)
		for _, alias := range m.Aliases {
			add(alias)
		}
	}
	file, err := catalog.Load()
	if err != nil {
		return nil, fmt.Errorf("reading the provider price catalog, which names the provider models a diff may spell: %w", err)
	}
	for id := range file.Models {
		add(id)
	}
	out := slices.Collect(maps.Keys(words))
	slices.SortFunc(out, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return out, nil
}

// isNameSep splits a model's name into the words it is built from: the two in
// claude-opus-5-5, the route spelling claude-sonnet-5.5 and the provider's
// moonshotai/kimi-k3.
func isNameSep(r rune) bool {
	return r == '-' || r == '.' || r == '_' || r == '@'
}

// versionWord reports whether a word of a model's name is its version rather
// than part of what the model is called: "5" in claude-opus-5-5, "v4" in
// deepseek-v4-pro, "20251001" in an alias. Everything from the version on is a
// number or a variant ("flash", "pro"), and a bare variant word names no model
// on its own, so it stays in the diff while the ID carrying it is redacted
// whole.
func versionWord(w string) bool {
	if w == "" {
		return true
	}
	if w[0] >= '0' && w[0] <= '9' {
		return true
	}
	return len(w) > 1 && (w[0] == 'v' || w[0] == 'V') && w[1] >= '0' && w[1] <= '9'
}
