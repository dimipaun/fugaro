// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth_test

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// check compares a narrowed Limits with the one the case expects, field by
// field, so a failure names which field moved rather than printing two
// structs. Scopes are compared by value and in order: the order is the
// token's own, and a narrower that reordered them would be a second place
// the vocabulary is decided.
func check(t *testing.T, got, want mcpauth.Limits) {
	t.Helper()
	if got.MinRigor != want.MinRigor {
		t.Errorf("MinRigor = %q; want %q", got.MinRigor, want.MinRigor)
	}
	if got.MaxRigor != want.MaxRigor {
		t.Errorf("MaxRigor = %q; want %q", got.MaxRigor, want.MaxRigor)
	}
	if got.Hotfix != want.Hotfix {
		t.Errorf("Hotfix = %v; want %v", got.Hotfix, want.Hotfix)
	}
	if got.SpendUSD != want.SpendUSD {
		t.Errorf("SpendUSD = %v; want %v", got.SpendUSD, want.SpendUSD)
	}
	if !slices.Equal(got.Scopes, want.Scopes) {
		t.Errorf("Scopes = %v; want %v", got.Scopes, want.Scopes)
	}
}

// TestLimitsOnlyNarrow is the rule the whole front door rests on: a request
// runs under the tighter of the token's limits and the project's, and nothing
// a token carries can raise a cap, run a mode the project forbids or reach a
// tool the project does not grant (spec.md §6).
//
// The first two cases are the plan's own M1 red step: a token asking for
// critical under a project policy of pro resolves to pro, and a token whose
// spend exceeds the run's ceiling resolves to the ceiling. The rest are the
// other three fields, because "only narrows" is a claim about every one of
// them or about none.
//
// The Hotfix flag is carried on both sides here, because it is the one field
// whose type cannot say "unset": a bool that is false is false, so a project
// whose policy does not turn Hotfix on has turned it off, and only a project
// that says so lets the token's own flag stand.
func TestLimitsOnlyNarrow(t *testing.T) {
	token := mcpauth.Limits{
		MinRigor: rigor.POC,
		MaxRigor: rigor.Critical,
		Hotfix:   true,
		SpendUSD: 25,
		Scopes:   []string{mcpauth.ScopeTasksRead, mcpauth.ScopeTasksWrite},
	}

	for _, tc := range []struct {
		name    string
		project mcpauth.Limits
		want    mcpauth.Limits
	}{
		{
			// A token may ask for the top of the axis; the project's own
			// ceiling is the answer.
			name:    "a token asking for critical under a project policy of pro resolves to pro",
			project: mcpauth.Limits{MaxRigor: rigor.Pro, Hotfix: true},
			want: mcpauth.Limits{MinRigor: rigor.POC, MaxRigor: rigor.Pro, Hotfix: true,
				SpendUSD: 25, Scopes: []string{mcpauth.ScopeTasksRead, mcpauth.ScopeTasksWrite}},
		},
		{
			// D924: the run's ceiling is the tighter of the two, so the
			// gateway enforces the token's limit and not the token's wish.
			name:    "a token whose spend exceeds the run's ceiling resolves to the ceiling",
			project: mcpauth.Limits{SpendUSD: 5, Hotfix: true},
			want: mcpauth.Limits{MinRigor: rigor.POC, MaxRigor: rigor.Critical, Hotfix: true,
				SpendUSD: 5, Scopes: []string{mcpauth.ScopeTasksRead, mcpauth.ScopeTasksWrite}},
		},
		{
			name:    "a token whose spend is under the ceiling keeps its own",
			project: mcpauth.Limits{SpendUSD: 100, Hotfix: true},
			want: mcpauth.Limits{MinRigor: rigor.POC, MaxRigor: rigor.Critical, Hotfix: true,
				SpendUSD: 25, Scopes: []string{mcpauth.ScopeTasksRead, mcpauth.ScopeTasksWrite}},
		},
		{
			// The floor is the stricter of the two, never the lower: a
			// project that runs nothing below pro cannot be talked down.
			name:    "a project floor raises the token's floor",
			project: mcpauth.Limits{MinRigor: rigor.Startup, Hotfix: true},
			want: mcpauth.Limits{MinRigor: rigor.Startup, MaxRigor: rigor.Critical, Hotfix: true,
				SpendUSD: 25, Scopes: []string{mcpauth.ScopeTasksRead, mcpauth.ScopeTasksWrite}},
		},
		{
			// D925: the flag is off by default and a token cannot turn it on
			// — a project that does not carry it has refused Hotfix, which
			// is the fail-closed reading of a flag that has no unset.
			name:    "a project that does not carry the flag turns it off",
			project: mcpauth.Limits{},
			want: mcpauth.Limits{MinRigor: rigor.POC, MaxRigor: rigor.Critical, Hotfix: false,
				SpendUSD: 25, Scopes: []string{mcpauth.ScopeTasksRead, mcpauth.ScopeTasksWrite}},
		},
		{
			// D927: a scope the project does not grant is not held, and the
			// ones it does are held in the token's own order.
			name:    "the scopes are the project's grant read against the token's ask",
			project: mcpauth.Limits{Scopes: []string{mcpauth.ScopeTasksRead}, Hotfix: true},
			want: mcpauth.Limits{MinRigor: rigor.POC, MaxRigor: rigor.Critical, Hotfix: true,
				SpendUSD: 25, Scopes: []string{mcpauth.ScopeTasksRead}},
		},
		{
			// A project that names no scope grants the token's own: unset,
			// like the window's bounds, and never a widening, because the
			// token's set is already the ceiling.
			name:    "a project that grants no scope leaves the token's own alone",
			project: mcpauth.Limits{Hotfix: true},
			want: mcpauth.Limits{MinRigor: rigor.POC, MaxRigor: rigor.Critical, Hotfix: true,
				SpendUSD: 25, Scopes: []string{mcpauth.ScopeTasksRead, mcpauth.ScopeTasksWrite}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := token.Narrow(tc.project)
			if err != nil {
				t.Fatalf("Narrow: %v", err)
			}
			check(t, got, tc.want)
		})
	}

	// The flag narrows from the token's side too: a project that allows
	// Hotfix does not put it on a token whose limits never asked for it.
	off := token
	off.Hotfix = false
	got, err := off.Narrow(mcpauth.Limits{Hotfix: true})
	if err != nil {
		t.Fatalf("Narrow: %v", err)
	}
	if got.Hotfix {
		t.Error("Hotfix = true; a project that allows it does not grant it to a token that does not ask")
	}
}

// TestNarrowTakesTheTighterCeilingWhicheverSideIsLower covers both arms of
// the ceiling: the table above always has the token's ceiling at the top of
// the axis, so it only ever exercises the project's ceiling winning. The
// other two readings — the token's own ceiling is the lower one, and the two
// are equal — are where a comparison written the wrong way round returns the
// project's value anyway and looks right.
func TestNarrowTakesTheTighterCeilingWhicheverSideIsLower(t *testing.T) {
	scopes := []string{mcpauth.ScopeTasksRead}

	// The ceiling: the lower bound stands, whichever side carries it.
	for _, tc := range []struct {
		name    string
		token   rigor.Mode
		project rigor.Mode
		want    rigor.Mode
	}{
		{"the project's ceiling is lower", rigor.Critical, rigor.Pro, rigor.Pro},
		{"the token's ceiling is lower", rigor.Startup, rigor.Critical, rigor.Startup},
		{"the two ceilings are equal", rigor.Pro, rigor.Pro, rigor.Pro},
		{"the project sets no ceiling", rigor.Startup, "", rigor.Startup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (mcpauth.Limits{MaxRigor: tc.token, Scopes: scopes}).
				Narrow(mcpauth.Limits{MaxRigor: tc.project, Scopes: scopes})
			if err != nil {
				t.Fatalf("Narrow: %v", err)
			}
			if got.MaxRigor != tc.want {
				t.Errorf("MaxRigor = %q; want %q", got.MaxRigor, tc.want)
			}
		})
	}

	// The floor is the same comparison from the other end: the higher bound
	// stands, and a token may not lower the project's.
	for _, tc := range []struct {
		name    string
		token   rigor.Mode
		project rigor.Mode
		want    rigor.Mode
	}{
		{"the project's floor is higher", rigor.POC, rigor.Pro, rigor.Pro},
		{"the token's floor is higher", rigor.Critical, rigor.POC, rigor.Critical},
		{"the two floors are equal", rigor.Pro, rigor.Pro, rigor.Pro},
		{"the project sets no floor", rigor.Startup, "", rigor.Startup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (mcpauth.Limits{MinRigor: tc.token, Scopes: scopes}).
				Narrow(mcpauth.Limits{MinRigor: tc.project, Scopes: scopes})
			if err != nil {
				t.Fatalf("Narrow: %v", err)
			}
			if got.MinRigor != tc.want {
				t.Errorf("MinRigor = %q; want %q", got.MinRigor, tc.want)
			}
		})
	}
}

// TestNarrowTakesTheTighterSpendWhicheverSideSetsOne covers the spend limit
// the same both ways, because D924's ceiling is the field a wrong comparison
// hides in: the table above only ever has the token setting one, so the arm
// where the project's is the lower — and the arm where the token writes none
// at all — are the two a comparison written the other way round still passes.
//
// The unset arm is the last three cases, and it is the one that says a token
// which writes no spend limit takes the project's: zero, a negative and NaN
// all fail the comparison, which is how policy reads a layer that writes no
// cap.
func TestNarrowTakesTheTighterSpendWhicheverSideSetsOne(t *testing.T) {
	scopes := []string{mcpauth.ScopeTasksRead}

	for _, tc := range []struct {
		name    string
		token   float64
		project float64
		want    float64
	}{
		{"the project's cap is lower", 25, 5, 5},
		{"the token's cap is lower", 2, 25, 2},
		{"the two caps are equal", 5, 5, 5},
		{"the project sets no cap", 2, 0, 2},
		{"the token sets no cap and takes the project's", 0, 5, 5},
		{"a negative cap is unset", -1, 5, 5},
		{"NaN is unset", math.NaN(), 5, 5},
		{"neither side sets one", 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (mcpauth.Limits{SpendUSD: tc.token, Scopes: scopes}).
				Narrow(mcpauth.Limits{SpendUSD: tc.project, Scopes: scopes})
			if err != nil {
				t.Fatalf("Narrow: %v", err)
			}
			if got.SpendUSD != tc.want {
				t.Errorf("SpendUSD = %v; want %v", got.SpendUSD, tc.want)
			}
		})
	}
}

// TestEmptyWindowIsRefusedAtMint pins D926's answer: a window that admits no
// mode is refused where the token is written, so a token that could never
// start a run cannot be issued, and the refusal names the two fields rather
// than leaving the operator to guess which bound is wrong.
//
// The second half is the same rule one step out: the token's own window is
// mintable, and narrowing it against a project whose ceiling ends below the
// token's floor admits nothing, so Narrow refuses it instead of handing back
// a token that reaches no mode.
func TestEmptyWindowIsRefusedAtMint(t *testing.T) {
	empty := mcpauth.Limits{
		MinRigor: rigor.Critical,
		MaxRigor: rigor.Pro,
		Scopes:   []string{mcpauth.ScopeTasksRead},
	}
	err := empty.Validate()
	if !errors.Is(err, mcpauth.ErrEmptyWindow) {
		t.Fatalf("Validate() = %v; want ErrEmptyWindow", err)
	}
	for _, field := range []string{"min_rigor", "max_rigor"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("Validate() = %q; want it to name %s", err, field)
		}
	}

	// A window of one mode is not empty: min == max admits exactly that
	// mode, and refusing it would refuse the tightest token the design has.
	if err := (mcpauth.Limits{
		MinRigor: rigor.Pro, MaxRigor: rigor.Pro, Scopes: []string{mcpauth.ScopeTasksRead},
	}).Validate(); err != nil {
		t.Errorf("a window of one mode: Validate() = %v; want ok", err)
	}

	token := mcpauth.Limits{MinRigor: rigor.Critical, Scopes: []string{mcpauth.ScopeTasksRead}}
	if err := token.Validate(); err != nil {
		t.Fatalf("the token's own window: Validate() = %v; want ok", err)
	}
	_, err = token.Narrow(mcpauth.Limits{MaxRigor: rigor.Pro})
	if !errors.Is(err, mcpauth.ErrEmptyWindow) {
		t.Fatalf("Narrow() = %v; want ErrEmptyWindow", err)
	}
	if !strings.Contains(err.Error(), "min_rigor") || !strings.Contains(err.Error(), "max_rigor") {
		t.Errorf("Narrow() = %q; want it to name min_rigor and max_rigor", err)
	}
}

// TestNarrowRefusesAProjectWindowItCannotRead is the project's half of the
// check above: the project's limits are read by the same rule as the token's,
// so a policy whose own window admits nothing, or whose bound is not on the
// axis, is refused and named rather than read as if the project had said
// nothing at all.
//
// That last reading is the failure the floor cases pin, and it comes from the
// comparison rather than from a missing check: Strictest skips a mode it
// cannot place, and both Hotfix and a value outside the vocabulary rank 0, so
// a project floor naming either would lose to the token's own floor and hand
// the token back unchanged. The ceiling cases are refused by the narrowed
// window's own check as well, which is why they are here: the refusal is
// taken before the two sides are compared, so it names the policy the operator
// has to correct rather than the result of comparing it.
func TestNarrowRefusesAProjectWindowItCannotRead(t *testing.T) {
	token := mcpauth.Limits{MinRigor: rigor.POC, Scopes: []string{mcpauth.ScopeTasksRead}}

	for _, tc := range []struct {
		name    string
		project mcpauth.Limits
		want    error
	}{
		{"the project's floor is off the axis", mcpauth.Limits{MinRigor: rigor.Hotfix}, mcpauth.ErrOffAxisBound},
		{"the project's ceiling is off the axis", mcpauth.Limits{MaxRigor: rigor.Hotfix}, mcpauth.ErrOffAxisBound},
		{"the project's floor is outside the vocabulary", mcpauth.Limits{MinRigor: "fast"}, mcpauth.ErrUnknownMode},
		{"the project's window admits nothing", mcpauth.Limits{MinRigor: rigor.Critical, MaxRigor: rigor.Pro}, mcpauth.ErrEmptyWindow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := token.Narrow(tc.project)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Narrow(%+v) = %v; want %v", tc.project, err, tc.want)
			}
			if !strings.Contains(err.Error(), "max_rigor") && !strings.Contains(err.Error(), "min_rigor") {
				t.Errorf("Narrow() = %q; want it to name the project's bound", err)
			}
		})
	}
}

// TestUnknownScopeIsRefusedNotIgnored pins D927: the v1 vocabulary is
// tasks:read and tasks:write, and a value outside it is refused where the
// token is written rather than dropped, because a scope a server silently
// ignored would be a token whose holder believes it holds a permission it
// does not — and because a scope, once minted, is a wire contract that cannot
// be withdrawn without breaking tokens.
func TestUnknownScopeIsRefusedNotIgnored(t *testing.T) {
	// roadmap:write is the scope the console's blocked write would want
	// (D927): it is a real name the design deliberately leaves out of v1, so
	// it is refused and not quietly read as one of the two that exist.
	for _, s := range []string{"roadmap:write", "tasks:Read", "TASKS:READ", "tasks", "read", " tasks:read", "tasks:read "} {
		lim := mcpauth.Limits{MinRigor: rigor.POC, Scopes: []string{mcpauth.ScopeTasksRead, s}}
		err := lim.Validate()
		if !errors.Is(err, mcpauth.ErrUnknownScope) {
			t.Errorf("Validate(scopes %q) = %v; want ErrUnknownScope", s, err)
			continue
		}
		if !strings.Contains(err.Error(), s) {
			t.Errorf("Validate(scopes %q) = %q; want it to name the scope", s, err)
		}
	}

	// A token that reaches no tool is refused at mint for the same reason an
	// empty window is: it is a limit that admits nothing.
	err := (mcpauth.Limits{MinRigor: rigor.POC}).Validate()
	if !errors.Is(err, mcpauth.ErrNoScope) {
		t.Errorf("Validate() with no scopes = %v; want ErrNoScope", err)
	}

	// Narrow refuses a token carrying a scope outside the vocabulary rather
	// than intersecting it away: the token was never mintable, and a silent
	// intersection would hide that behind a call that looks like a narrowing.
	//
	// The project named here grants a scope the token does hold, and nothing
	// else, so the refusal can only come from the token's own list: a project
	// that named the unknown scope too would satisfy the assertion through
	// the project's check with the token's own removed.
	_, err = (mcpauth.Limits{
		MinRigor: rigor.POC,
		Scopes:   []string{mcpauth.ScopeTasksRead, "roadmap:write"},
	}).Narrow(mcpauth.Limits{Scopes: []string{mcpauth.ScopeTasksRead}})
	if !errors.Is(err, mcpauth.ErrUnknownScope) {
		t.Errorf("Narrow() with an unknown token scope = %v; want ErrUnknownScope", err)
	}
	if !strings.Contains(err.Error(), "roadmap:write") {
		t.Errorf("Narrow() = %q; want it to name the token's scope", err)
	}

	// And it reads the project's side as strictly as the token's: a project
	// policy that names a scope outside the vocabulary is refused and named,
	// rather than quietly intersecting the scope away — which would be the
	// same silent narrowing, one layer out.
	project := mcpauth.Limits{Scopes: []string{mcpauth.ScopeTasksRead, "roadmap:write"}}
	_, err = (mcpauth.Limits{
		MinRigor: rigor.POC,
		Scopes:   []string{mcpauth.ScopeTasksRead},
	}).Narrow(project)
	if !errors.Is(err, mcpauth.ErrUnknownScope) {
		t.Fatalf("Narrow() against a project naming an unknown scope = %v; want ErrUnknownScope", err)
	}
	if !strings.Contains(err.Error(), "roadmap:write") {
		t.Errorf("Narrow() = %q; want it to name the project's scope", err)
	}

	// A token and a project that share no scope admit nothing between them,
	// and the refusal says so rather than reporting the token as empty: the
	// two are different failures and an operator acts on them differently.
	_, err = (mcpauth.Limits{
		MinRigor: rigor.POC,
		Scopes:   []string{mcpauth.ScopeTasksRead},
	}).Narrow(mcpauth.Limits{Scopes: []string{mcpauth.ScopeTasksWrite}})
	if !errors.Is(err, mcpauth.ErrNoScope) {
		t.Fatalf("Narrow() with no shared scope = %v; want ErrNoScope", err)
	}
	if !strings.Contains(err.Error(), "the project grants none of the token's scopes") {
		t.Errorf("Narrow() = %q; want it to name the project's grant as the cause", err)
	}

	// The vocabulary itself is exactly the two names D927 mints, so a later
	// change that widened it is a decision and not an edit.
	if !slices.Equal(mcpauth.ScopeVocabulary, []string{"tasks:read", "tasks:write"}) {
		t.Errorf("ScopeVocabulary = %v; want tasks:read and tasks:write", mcpauth.ScopeVocabulary)
	}
	for _, s := range mcpauth.ScopeVocabulary {
		if !mcpauth.KnownScope(s) {
			t.Errorf("KnownScope(%q) is false", s)
		}
	}
}
