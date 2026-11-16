package settings

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

// This file is the writer registry: what writes a row's key, as a value the
// two consumers of item 4 and item 6 can act on (D2609, D2610).
//
// registry.go's rows say who OWNS a key in words — a family token, or the file
// a key no command edits is stated in. That was enough while nothing ran the
// owner: the read printed the token, and a refusal named the family. The
// owner's direction of 2026-11-15 asks for more. Item 4 wants a test that the
// owner really writes the key, and item 6 wants a refusal to print the command
// line that works; both need the same thing, which is the owner spelled as a
// command and an argv rather than as a token. This file is that spelling and
// docs/contracts/config-ownership.md is its contract.
//
// It stays read-only knowledge, as registry.go does: nothing here writes a
// file, reads a cloud object or imports internal/cli. The writers themselves
// live in internal/cli, one file per family, and each registers the patterns
// its rows answer for in that package's init.

// Store is where a writer's value lands — the file or the database the write
// itself touches, and not the place the value is served from (a pool is
// written in the local config and served from the published node; its store is
// local). It is the input of the stage choice the after-write hook makes
// (D2615): the hook reads the store and runs the stages that key needs, so a
// store nobody defined is a write no stage is ever run for.
type Store string

const (
	// StoreLocal is the machine's own config (internal/localcfg).
	StoreLocal Store = "local"
	// StoreProject is the project layer file (internal/config.ProjectLayer).
	StoreProject Store = "project"
	// StoreRepo is a checkout's `fugaro.yaml`.
	StoreRepo Store = "repo"
	// StoreNode is the published policy node.
	StoreNode Store = "node"
	// StoreBudget is the Realtime Database the budget lives in.
	StoreBudget Store = "budget"
	// StoreNone is a row no command writes: the labelled rows of item 8, and
	// the rows whose store is a command's own bookkeeping. It is the store
	// spelled out rather than left empty, so a Writer always says where it
	// wrote.
	StoreNone Store = "none"
)

// Label is why a row no command writes is not a settings row (D2611). The
// empty label is the ordinary row: a writer with an argv that runs.
type Label string

const (
	// LabelPlumbing is a key `fugaro setup` and `fugaro update` write as they
	// install and converge: endpoints, generations, the installation's own
	// bookkeeping.
	LabelPlumbing Label = "plumbing"
	// LabelFakeEndpoint is a key only a test writes.
	LabelFakeEndpoint Label = "fake-endpoint"
	// LabelFile is a key in a named file nobody edits. It is the one label
	// whose meaning is not in the label, so the row states its one-line Reason
	// (T5).
	LabelFile Label = "file"
)

// KeyPlaceholder is the word an Argv template writes where the concrete
// settings key goes: `{"settings", "set", KeyPlaceholder, "<value>"}`. A
// wildcard row has no single key to name (`workflows.*.commands.test` is
// written at `workflows.web.commands.test`), so the template is what is
// registered and ArgvFor fills it.
const KeyPlaceholder = "<key>"

// Writer is what writes a settings row's key.
//
// Exactly one of its two shapes is stated. A row a command writes carries
// Command, Argv and the Store the value lands in, with no Label — that is the
// shape the conformance test runs and a refusal prints (D2610). A row no
// command of this plan writes carries a Label and says StoreNone — that is the
// shape item 8 allows in place of a writer, and the label with its reason is
// the pinned fact (T5).
type Writer struct {
	// Command is the command family that writes the key, as the refusal and
	// the settings read name it: "fugaro accounts pool add". Empty for a row
	// registered with a label.
	Command string
	// Argv is the command line the row is written by, in words, without the
	// leading `fugaro` and with KeyPlaceholder where the concrete key goes.
	// The conformance test runs it against a hermetic config and asserts the
	// key changed in Store (T1); a refusal prints it (W3).
	Argv []string
	// Store is where this Argv's write lands, and the store the conformance
	// test asserts the key changed in. A key with more than one layer is
	// registered under the writer whose argv names the layer it writes; a
	// command writing another layer hands the hook the store it wrote in
	// rather than this row's (docs/contracts/config-ownership.md §4).
	Store Store
	// Label is why the row has no runnable writer, or "" for a row that has
	// one.
	Label Label
	// Reason is the one line saying what the label means for this row. It is
	// required for LabelFile and unused by the other two.
	Reason string
}

// ArgvFor is the Argv of a Writer with the key filled in: the command line the
// conformance test runs and a refusal prints. It is a copy, so a caller cannot
// edit the registered template.
func (w Writer) ArgvFor(key string) []string {
	out := slices.Clone(w.Argv)
	for i, word := range out {
		if word == KeyPlaceholder {
			out[i] = key
		}
	}
	return out
}

// writerRow is one registration: the settings row pattern and its writer.
type writerRow struct {
	pattern string
	writer  Writer
}

// writerRows is the table, in lookup order (see comparePatterns). Registration
// is what a family's init and a test do while a lookup may run in the same
// binary, so the table is guarded rather than being built once before any
// reader exists as registry.go's rows are.
var (
	writerMu   sync.RWMutex
	writerRows []writerRow
)

// RegisterWriter registers w as the writer of every key the pattern matches.
// It answers an error for a pattern that is already registered, for a store or
// a label outside the two vocabularies above, for a `file` row that states no
// reason, and for a writer that does not state exactly one of the two shapes:
// a row a command writes carries a Command, a non-empty Argv and a store other
// than none, and a labelled row carries neither an Argv nor a store.
//
// Registration happens in the family's package init, where the error is a
// programming error rather than a user's mistake: the family panics on it, so
// that a pattern two families claim fails every command of the binary and the
// conformance test loudly, instead of one of the two silently winning. The
// first registration of a pattern stands whichever way it is called.
func RegisterWriter(pattern string, w Writer) error {
	if pattern == "" {
		return fmt.Errorf("settings: a writer is registered under a registry row pattern, and this one is empty")
	}
	if err := checkWriter(w); err != nil {
		return fmt.Errorf("settings: the writer of %s: %w", pattern, err)
	}
	writerMu.Lock()
	defer writerMu.Unlock()
	for _, r := range writerRows {
		if r.pattern == pattern {
			return fmt.Errorf("settings: %s is already registered by %s, and one pattern has one registrant: two families naming one row is a diff to settle, not a registration to win (docs/contracts/config-ownership.md)", pattern, describe(r.writer))
		}
	}
	writerRows = append(writerRows, writerRow{pattern: pattern, writer: w})
	slices.SortFunc(writerRows, func(a, b writerRow) int { return comparePatterns(a.pattern, b.pattern) })
	return nil
}

// WriterOf is the writer of key: the row registered under the key itself, or
// the most specific pattern that matches it. false: no row and no pattern
// answers for the key, which is a key whose refusal keeps its family sentence
// (W3).
func WriterOf(key string) (Writer, bool) {
	writerMu.RLock()
	defer writerMu.RUnlock()
	for _, r := range writerRows {
		if patternMatches(r.pattern, key) {
			return r.writer, true
		}
	}
	return Writer{}, false
}

// checkWriter is the vocabulary a registration is held to, and the two shapes
// above: exactly one of them is stated, and a writer that states neither or
// both is refused rather than registered as something no reader can act on.
func checkWriter(w Writer) error {
	switch w.Store {
	case StoreLocal, StoreProject, StoreRepo, StoreNode, StoreBudget, StoreNone:
	default:
		return fmt.Errorf("the store %q is not one of %s, %s, %s, %s, %s, %s: the store is where the value lands, and the after-write hook reads it to choose the stages the write owes (D2615)", w.Store, StoreLocal, StoreProject, StoreRepo, StoreNode, StoreBudget, StoreNone)
	}
	switch w.Label {
	case "", LabelPlumbing, LabelFakeEndpoint, LabelFile:
	default:
		return fmt.Errorf("the label %q is not one of %s, %s, %s (or none): the three are the owner's vocabulary (D2611), and a fourth is a reason nobody agreed to", w.Label, LabelPlumbing, LabelFakeEndpoint, LabelFile)
	}
	if w.Label == "" {
		if len(w.Argv) == 0 {
			return fmt.Errorf("a row a command writes states the command line it is written by, and this one has no Argv: the conformance test runs it and a refusal prints it, so an empty one is bare `fugaro` (T1, W3)")
		}
		if w.Command == "" {
			return fmt.Errorf("a row a command writes names the command that writes it, and this one names none: the settings read and the refusal say which command owns the key (D2609)")
		}
		if w.Store == StoreNone {
			return fmt.Errorf("a row a command writes lands in a store and this one says %s: the %s store is for a row no command writes, and a writer with an Argv that lands nowhere is a write the hook runs no stage for (D2615)", w.Store, StoreNone)
		}
		return nil
	}
	if len(w.Argv) > 0 {
		return fmt.Errorf("a row registered with the %s label carries no Argv: the label is what stands in for a runnable writer, and a labelled row that also has a command line is two answers to one question (T5)", w.Label)
	}
	if w.Store != StoreNone {
		return fmt.Errorf("a row registered with the %s label says the %s store, and a row no command writes lands nowhere (%s): setup converging a file is not this row being written by a command (T5)", w.Label, w.Store, StoreNone)
	}
	if w.Label == LabelFile && w.Reason == "" {
		return fmt.Errorf("the %s label needs its one-line reason: a key in a named file nobody edits states why it is not a settings row, or it becomes one (D2611, T5)", LabelFile)
	}
	return nil
}

// describe names a registered writer the way a refusal would: by its command,
// or by the label standing in for one.
func describe(w Writer) string {
	if w.Command != "" {
		return w.Command
	}
	if w.Label != "" {
		return "the label " + string(w.Label)
	}
	return "a writer with no command"
}

// comparePatterns orders the table so that the most specific row answers
// first: the fewest varying segments, then the pattern itself, so that the
// answer for a key never depends on the order two families happened to
// register in.
func comparePatterns(a, b string) int {
	if d := varyingSegments(a) - varyingSegments(b); d != 0 {
		return d
	}
	return strings.Compare(a, b)
}

// varyingSegments is how many segments of a pattern a key varies.
func varyingSegments(pattern string) int {
	n := 0
	for _, segment := range strings.Split(pattern, ".") {
		if segment == "*" {
			n++
		}
	}
	return n
}

// patternMatches is the registry's own wildcard rule, which a writer's pattern
// is addressed by exactly as a registry row is (settingsPatternMatch,
// internal/cli/settings_set.go): two spellings match when they have the same
// number of dot-separated segments and every segment is the key's own, a `*`
// standing for exactly one non-empty segment. The two rules are one behaviour
// written twice — this package cannot import internal/cli — and
// TestWriterOfMatchesWildcardRows holds this half to the cases that one is
// held to.
func patternMatches(pattern, key string) bool {
	p, k := strings.Split(pattern, "."), strings.Split(key, ".")
	if len(p) != len(k) {
		return false
	}
	for i := range p {
		switch {
		case p[i] == "*":
			if k[i] == "" {
				return false
			}
		case p[i] != k[i]:
			return false
		}
	}
	return true
}
