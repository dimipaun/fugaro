// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"errors"
	"fmt"
	"slices"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// Limits is what one token is allowed to do: the rigor window and the Hotfix
// flag of spec.md §6, the spend limit D924 puts into the run's own ceiling,
// and the scopes D927 mints.
//
// Every field is a bound and never a grant. A token narrows what the project
// already allows and can never widen it (Narrow), and the zero value of every
// field is "this token asks for nothing here" rather than "this token is
// unbounded": an unset field passes the project's own value through, the same
// reading rigor's zero Mode and policy's unset caps already have.
type Limits struct {
	// MinRigor is the weakest mode a run started with this token may ask
	// for: the window's floor. MinRigor and MaxRigor are optional, and unset
	// means the project's policy decides alone (D926).
	MinRigor rigor.Mode
	// MaxRigor is the strongest mode a run started with this token may ask
	// for: the window's ceiling.
	//
	// The two bounds read the four on-axis modes only. Hotfix is not a value
	// they take, because it ranks 0 on purpose — below every on-axis mode —
	// so folding it into a window would make `min: pro` admit a Hotfix
	// request, which is the opposite of D925's "off by default". Hotfix is
	// the flag below and nothing else.
	MaxRigor rigor.Mode
	// Hotfix is whether a run started with this token may ask for Hotfix at
	// all (D925). It is off by default, it is not read against the window,
	// and it only ever narrows: ahead of a token that carries it, a project
	// that does not turns it off.
	Hotfix bool
	// SpendUSD is the most this token's runs may spend, in dollars. D924
	// puts it in the execution override as a variable of its own, and the
	// run's ceiling is the tighter of it and the job's own, so the limit is
	// enforced by the gateway where every other cap is and a wrong number at
	// the coordinator cannot widen a ceiling. Zero, or any non-positive
	// value, is unset: the project's cap decides alone, as policy's own caps
	// read an unset layer.
	SpendUSD float64
	// Scopes are the scopes the token holds: D927's two, and nothing else. A
	// value outside the vocabulary is refused where the token is written and
	// never dropped silently — a scope is a wire contract, it exists the
	// moment a token can hold it, and a server that ignored one would leave
	// its holder believing it holds a permission it does not.
	Scopes []string
}

// The v1 scope vocabulary, exactly the two names the owner lists (D927).
// roadmap:write is deliberately not here: it is a real name for a surface
// that does not exist yet, and the console's roadmap-write design is what
// should decide its scope.
const (
	// ScopeTasksRead is the status, logs, results and listing half of the
	// tools.
	ScopeTasksRead = "tasks:read"
	// ScopeTasksWrite is the submit and cancel half: the tools that spend
	// money or stop work.
	ScopeTasksWrite = "tasks:write"
)

// ScopeVocabulary is the closed set v1 mints, in the order a refusal names
// it.
var ScopeVocabulary = []string{ScopeTasksRead, ScopeTasksWrite}

// KnownScope reports whether s is a scope v1 mints. It is the only test a
// scope is put to, so an unknown one cannot be matched as a near miss.
func KnownScope(s string) bool { return slices.Contains(ScopeVocabulary, s) }

// The refusals of the limits vocabulary, in the shape every caller of it
// needs: errors.Is tells the front door which rule it hit, and the message
// names the field and the value, because a person reading a mint failure or
// an agent reading a tool refusal has to be able to act on it.
var (
	// ErrEmptyWindow refuses a rigor window that admits no mode: a floor
	// above a ceiling. It is refused where the limit is written rather than
	// on every call (D926), because a token that could never start a run
	// should fail at the operator's keyboard and not in an agent's retry
	// loop.
	ErrEmptyWindow = errors.New("mcpauth: the rigor window admits no mode: max_rigor is below min_rigor")

	// ErrOffAxisBound refuses Hotfix as a window bound. Rank gives Hotfix 0
	// so that it can never win a comparison against an on-axis mode, which
	// means reading it as a bound would admit it at every floor (D925).
	ErrOffAxisBound = errors.New("mcpauth: hotfix is not a rigor window bound: it is off the axis and gated by the Hotfix flag alone")

	// ErrUnknownMode refuses a mode outside rigor's vocabulary rather than
	// ranking it 0, which is what Rank does with a value that reached it
	// without having been parsed.
	ErrUnknownMode = errors.New("mcpauth: unknown rigor mode")

	// ErrModeRefused refuses a requested mode the token's limits do not
	// admit. The message names the mode that was asked for and the limit
	// that refused it (spec.md §6): an agent that cannot see which limit it
	// hit retries the same request.
	ErrModeRefused = errors.New("mcpauth: the requested rigor mode is outside the token's limits")

	// ErrUnknownScope refuses a scope outside ScopeVocabulary (D927).
	ErrUnknownScope = errors.New("mcpauth: unknown scope: v1 mints tasks:read and tasks:write only")

	// ErrNoScope refuses limits that reach no tool at all, whether the token
	// carries no scope or the project's own policy grants none of the ones
	// it asked for. It is the scope half of the empty window's rule.
	ErrNoScope = errors.New("mcpauth: scopes is empty: a token with no scope reaches no tool")
)

// onAxis reports whether m is a mode a window reads: one of the four, and not
// Hotfix.
func onAxis(m rigor.Mode) bool { return m != rigor.Hotfix && rigor.Known(m) }

// Validate is the mint-time check (D926, D927, D929): the limits a token is
// about to be issued with, or the one reason they cannot be.
//
// This is where a limit is refused rather than narrowed, because there is no
// wider side to take: no bound off the axis, no window that admits nothing,
// no scope outside D927's vocabulary and no set of limits that reaches no
// tool. The mint calls it once and refuses the token; nothing downstream has
// to ask the question again.
func (l Limits) Validate() error {
	if err := l.window(); err != nil {
		return err
	}
	if err := l.scopeVocabulary(); err != nil {
		return err
	}
	if len(l.Scopes) == 0 {
		return ErrNoScope
	}
	return nil
}

// window is the bounds' own check, shared by Validate and Narrow so the two
// cannot disagree about what a window is: both bounds lie on the axis, and
// the window they leave admits at least one mode.
func (l Limits) window() error {
	for _, b := range []struct {
		field string
		mode  rigor.Mode
	}{{"min_rigor", l.MinRigor}, {"max_rigor", l.MaxRigor}} {
		if b.mode == "" {
			continue
		}
		if b.mode == rigor.Hotfix {
			return fmt.Errorf("mcpauth: %s is %q: %w", b.field, string(b.mode), ErrOffAxisBound)
		}
		if !rigor.Known(b.mode) {
			return fmt.Errorf("mcpauth: %s is %q: %w", b.field, string(b.mode), ErrUnknownMode)
		}
	}
	if l.MinRigor != "" && l.MaxRigor != "" && l.MinRigor.Rank() > l.MaxRigor.Rank() {
		return fmt.Errorf("mcpauth: min_rigor %q and max_rigor %q admit no mode: %w",
			string(l.MinRigor), string(l.MaxRigor), ErrEmptyWindow)
	}
	return nil
}

// scopeVocabulary is the D927 check on its own, so a narrowing can refuse a
// token that was never mintable without also asking whether the result is
// empty.
func (l Limits) scopeVocabulary() error {
	for _, s := range l.Scopes {
		if !KnownScope(s) {
			return fmt.Errorf("mcpauth: scope %q: %w", s, ErrUnknownScope)
		}
	}
	return nil
}

// Admits reports whether a run may be asked for at mode under these limits,
// and returns the refusal naming the limit it hit when it may not.
//
// Hotfix is answered by the flag alone and never by the window (D925), which
// is the point of the split: Hotfix ranks 0, so a window comparison would
// admit it at every floor. Everything else is read against the floor and the
// ceiling, and a mode outside the vocabulary is refused rather than ranked.
//
// A window that is not a window is refused before it is read, for the same
// reason Validate refuses one: a bound off the axis ranks 0, so reading it
// here would answer as if the token had no bound at all.
func (l Limits) Admits(mode rigor.Mode) error {
	if !rigor.Known(mode) {
		return fmt.Errorf("mcpauth: %q: %w", string(mode), ErrUnknownMode)
	}
	if err := l.window(); err != nil {
		return err
	}
	if mode == rigor.Hotfix {
		if !l.Hotfix {
			return fmt.Errorf("mcpauth: %q was asked for and the token's hotfix flag is off: %w", string(mode), ErrModeRefused)
		}
		return nil
	}
	if l.MinRigor != "" && mode.Rank() < l.MinRigor.Rank() {
		return fmt.Errorf("mcpauth: %q was asked for and min_rigor is %q: %w", string(mode), string(l.MinRigor), ErrModeRefused)
	}
	if l.MaxRigor != "" && mode.Rank() > l.MaxRigor.Rank() {
		return fmt.Errorf("mcpauth: %q was asked for and max_rigor is %q: %w", string(mode), string(l.MaxRigor), ErrModeRefused)
	}
	return nil
}

// Narrow is the rule the whole front door rests on: the limits a request runs
// under are the tighter of the token's and the project's, field by field, so
// nothing a token carries can raise a cap, run a mode the project forbids or
// reach a tool the project does not grant (spec.md §6).
//
// It is the only place the tightening happens, and it is taken on every
// field rather than on the ones a caller remembers: the higher floor, the
// lower ceiling, the flag only if both sides carry it, the smaller positive
// spend, and the token's scopes read against the project's grant. An unset
// field on either side passes the other through — a project that says
// nothing restricts nothing, and a token that asks for nothing is asking for
// the project's answer.
//
// The one thing it returns instead of a narrower set is the refusal of a
// result that admits nothing: a window whose floor ends above its ceiling, or
// a scope set the project's grant leaves empty. Both name the field, because
// the caller is a launcher deciding whether to start a run and not a person
// reading a page.
func (l Limits) Narrow(project Limits) (Limits, error) {
	if err := l.window(); err != nil {
		return Limits{}, err
	}
	if err := project.window(); err != nil {
		return Limits{}, err
	}
	if err := l.scopeVocabulary(); err != nil {
		return Limits{}, err
	}

	out := Limits{
		MinRigor: rigor.Strictest(l.MinRigor, project.MinRigor),
		MaxRigor: lowerCeiling(l.MaxRigor, project.MaxRigor),
		Hotfix:   l.Hotfix && project.Hotfix,
		SpendUSD: tighterSpend(l.SpendUSD, project.SpendUSD),
		Scopes:   grantedScopes(l.Scopes, project.Scopes),
	}
	if err := out.window(); err != nil {
		return Limits{}, err
	}
	if len(out.Scopes) == 0 {
		return Limits{}, fmt.Errorf("mcpauth: the project grants none of the token's scopes: %w", ErrNoScope)
	}
	return out, nil
}

// lowerCeiling is the tighter of two ceilings: the lower mode admits fewer
// requests, and an unset one imposes nothing. It is the mirror of
// rigor.Strictest, which cannot be used here because a floor and a ceiling
// take opposite sides of the comparison.
func lowerCeiling(a, b rigor.Mode) rigor.Mode {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case a.Rank() <= b.Rank():
		return a
	}
	return b
}

// tighterSpend is the smaller of two spend limits, with a non-positive one
// unset — 0, a negative value and NaN all fail the comparison, which is how
// policy's own caps read a layer that writes none.
func tighterSpend(a, b float64) float64 {
	switch {
	case !(a > 0):
		return b
	case !(b > 0):
		return a
	case a < b:
		return a
	}
	return b
}

// grantedScopes is the token's scopes the project also grants, in the token's
// own order. A project that names none grants everything the token asks for:
// unset, like every other field, and never a widening, because the token's
// own set is already the ceiling.
//
// The order is the token's because it is the order the token was minted with,
// and a narrower that reordered the scopes would be a second place the
// vocabulary is decided.
func grantedScopes(token, project []string) []string {
	if len(project) == 0 {
		return slices.Clone(token)
	}
	out := make([]string, 0, len(token))
	for _, s := range token {
		if slices.Contains(project, s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
