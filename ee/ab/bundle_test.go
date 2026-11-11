// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The anonymiser (A5.1): what the judge is handed, what it is never handed,
// and the two refusals that leave a pair unjudged rather than unblind.
//
// The fixtures below name entries of the built-in price table, because that
// table is the vocabulary the redaction reads (Claude IDs through
// pricing.Embedded, provider IDs through the embedded catalog) and invent run
// IDs of the shape task.ValidRunID accepts. No price appears anywhere: this
// package never reads one.

const (
	champRunID  = "20260101-120000-abcd"
	champBranch = "fugaro/" + champRunID
	challRunID  = "20260101-120000-beef"
	challBranch = "fugaro/" + challRunID

	// diffBody is a patch with nothing identifying in it: the two hunks a
	// per-model pair really carries.
	diffBody = "--- a/app.go\n+++ b/app.go\n@@ -1,2 +1,3 @@\n package app\n+\n+var version = \"1.0\"\n"
)

func champPatch(diff string) Patch {
	return Patch{RunID: champRunID, Branch: champBranch, Diff: diff}
}

func challPatch(diff string) Patch {
	return Patch{RunID: challRunID, Branch: challBranch, Diff: diff}
}

// TestBundleCarriesNoIdentifier pins the constraint the judge's blindness
// rests on: its input holds no run ID, no branch and no model ID. A diff that
// carries an identifier is refused — the pair is unjudged, counted by A5.4 —
// and one that carries a model's name is redacted, so a task that touches
// configuration or pricing is judged instead of refused (A5.1).
func TestBundleCarriesNoIdentifier(t *testing.T) {
	// The champion and the challenger both quote their own run ID here: a
	// changelog line, a branch in a URL, a fixture. Every one of them names
	// the run, and the run's record names the model.
	for _, tc := range []struct {
		name     string
		champion Patch
		chall    Patch
		pairID   string
	}{
		{"the champion's own run ID", champPatch("+// run " + champRunID + "\n" + diffBody), challPatch(diffBody), champRunID},
		{"the challenger's own run ID", champPatch(diffBody), challPatch("+// run " + challRunID + "\n" + diffBody), champRunID},
		{"the champion's branch", champPatch("+// " + champBranch + " is the branch\n" + diffBody), challPatch(diffBody), champRunID},
		{"the challenger's branch", champPatch(diffBody), challPatch("+// pushed to " + challBranch + "\n" + diffBody), champRunID},
		// Another run's ID is not this pair's, and it still tells the judge a
		// run wrote this: the shape alone is refused.
		{"another run's ID in a path", champPatch("+// see runs/acme-app/20260303-030303-cafe/report.md\n" + diffBody), challPatch(diffBody), champRunID},
		// The pair ID is the champion's run ID in practice (D557 samples on
		// the run ID, D559 joins the two records by the pair), and it is an
		// identifier whatever it spells.
		{"the pair ID", champPatch("+// pair bundle-pair-1\n" + diffBody), challPatch(diffBody), "bundle-pair-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _, err := Bundle(tc.champion, tc.chall, tc.pairID)
			if !errors.Is(err, ErrIdentified) {
				t.Fatalf("a diff carrying %s: Bundle err = %v, want %v, and no input (in = %+v)", tc.name, err, ErrIdentified, in)
			}
		})
	}

	t.Run("a clean pair is judged whole", func(t *testing.T) {
		challDiff := diffBody + "+var name = \"app\"\n"
		in, swap, err := Bundle(champPatch(diffBody), challPatch(challDiff), champRunID)
		if err != nil {
			t.Fatalf("a pair with nothing identifying in it: %v", err)
		}
		// The bundle removes nothing but model words, so each arm's diff is
		// exactly what the bucket holds: no commit metadata is invented, and
		// none is left in either (A2.1 diffs two trees, so there is none).
		wantA, wantB := diffBody, challDiff
		if swap {
			wantA, wantB = challDiff, diffBody
		}
		if in.A != wantA || in.B != wantB {
			t.Fatalf("judge input = (%q, %q), want (%q, %q)", in.A, in.B, wantA, wantB)
		}
		for what, text := range map[string]string{"A": in.A, "B": in.B} {
			for _, id := range []string{champRunID, champBranch, challRunID, challBranch} {
				if strings.Contains(text, id) {
					t.Errorf("the judge's %s carries %q: %q", what, id, text)
				}
			}
		}
	})
}

// TestBundleSwapDeterministic pins the seeded swap (D552): which arm is
// labelled A is a property of the pair, not of the caller and not of the
// diffs, so the same pair is labelled the same way on every judge run and the
// second pass is its exact reverse.
func TestBundleSwapDeterministic(t *testing.T) {
	champion, challenger := champPatch("+champion\n"), challPatch("+challenger\n")

	first, swap, err := Bundle(champion, challenger, champRunID)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	again, swapAgain, err := Bundle(champion, challenger, champRunID)
	if err != nil {
		t.Fatalf("Bundle again: %v", err)
	}
	if again != first || swapAgain != swap {
		t.Fatalf("the same pair was labelled twice: %+v/%v then %+v/%v", first, swap, again, swapAgain)
	}

	// The seed is the pair ID alone: another pair of diffs under the same ID
	// comes out with the same arm under A.
	other, otherSwap, err := Bundle(champPatch("+one\n"), challPatch("+two\n"), champRunID)
	if err != nil {
		t.Fatalf("Bundle another pair of diffs: %v", err)
	}
	if otherSwap != swap {
		t.Fatalf("the same pair ID gave swap %v and %v: the labels follow the diffs, not the pair", swap, otherSwap)
	}
	if (other.A == "+one\n") != (first.A == champion.Diff) {
		t.Fatalf("the same pair ID put a different arm under A: %+v and %+v", first, other)
	}

	// Both labellings do occur, so a report is not built on one order alone.
	seen := map[bool]bool{}
	for i := range 64 {
		_, sw, err := Bundle(champion, challenger, fmt.Sprintf("pair-%d", i))
		if err != nil {
			t.Fatalf("Bundle pair-%d: %v", i, err)
		}
		seen[sw] = true
	}
	if !seen[true] || !seen[false] {
		t.Fatalf("64 pair IDs gave swap values %v: the hash does not spread", seen)
	}

	// swap is what the caller decodes a verdict with, so it has to say which
	// arm A holds — in the first pass and, inverted, in the second, which is
	// the same input with the labels exchanged (D552). Both are checked here
	// because a caller that inverted one and not the other would read every
	// verdict backwards with no test failing.
	arm := func(swapped bool) string {
		if swapped {
			return challenger.Diff
		}
		return champion.Diff
	}
	if first.A != arm(swap) || first.B != arm(!swap) {
		t.Fatalf("pass 1 (%+v, swap %v) does not match the arms", first, swap)
	}
	second := JudgeInput{A: first.B, B: first.A}
	if second.A != arm(!swap) || second.B != arm(swap) {
		t.Fatalf("pass 2 (%+v, swap %v) is not pass 1 reversed", second, !swap)
	}
}

// TestBundleRefusesTruncated: half a change cannot be judged against a whole
// one, and the cap is a fact on the record rather than a silent cut (A2.1),
// so either arm's truncated patch leaves the pair unjudged instead of handing
// the judge a diff that stops mid-hunk.
func TestBundleRefusesTruncated(t *testing.T) {
	truncated := champPatch(diffBody)
	truncated.Truncated = true
	truncatedChall := challPatch(diffBody)
	truncatedChall.Truncated = true

	for _, tc := range []struct {
		name     string
		champion Patch
		chall    Patch
	}{
		{"the champion's patch", truncated, challPatch(diffBody)},
		{"the challenger's patch", champPatch(diffBody), truncatedChall},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Bundle(tc.champion, tc.chall, champRunID); !errors.Is(err, ErrTruncated) {
				t.Fatalf("a truncated %s: Bundle err = %v, want %v", tc.name, err, ErrTruncated)
			}
		})
	}
}

// TestBundleRedactsModelIDInBothArms: a model ID, a vendor word or a product
// name in a diff becomes one placeholder, the same one in both arms. It is
// redacted rather than refused because the diffs that name a model are the
// ones about configuration and pricing, and refusing them would leave exactly
// those tasks unjudged (A5.1).
func TestBundleRedactsModelIDInBothArms(t *testing.T) {
	// Each arm names a different model, and each names it the way a real
	// diff would: the coder pin, a route's provider model, a vendor word.
	champDiff := "--- a/fugaro.yaml\n+++ b/fugaro.yaml\n-agent.models.coder: claude-opus-5-5\n+agent.models.coder: claude-sonnet-5-5\n"
	challDiff := "--- a/fugaro.yaml\n+++ b/fugaro.yaml\n-routing.providers.deepseek.models: [deepseek/deepseek-v4-pro]\n+routing.providers.moonshotai.models: [moonshotai/kimi-k3]\n"

	in, _, err := Bundle(champPatch(champDiff), challPatch(challDiff), champRunID)
	if err != nil {
		t.Fatalf("a diff naming a model ID must be judged, not refused: %v", err)
	}
	for _, word := range []string{
		"claude-opus-5-5", "claude-sonnet-5-5", "opus", "sonnet",
		"deepseek/deepseek-v4-pro", "deepseek", "moonshotai/kimi-k3", "kimi", "moonshotai",
	} {
		if strings.Contains(in.A, word) || strings.Contains(in.B, word) {
			t.Errorf("the judge's input still carries %q:\nA: %s\nB: %s", word, in.A, in.B)
		}
	}
	// What is not a model survives: the diff the judge reads is still the
	// change, not a row of placeholders.
	for _, kept := range []string{"agent.models.coder", "routing.providers", "models:"} {
		if !strings.Contains(in.A, kept) && !strings.Contains(in.B, kept) {
			t.Errorf("the redaction dropped %q, which names no model:\nA: %s\nB: %s", kept, in.A, in.B)
		}
	}
	// One placeholder for every model, in both arms alike: a distinct
	// placeholder per model (or per arm) would tell the judge which arm named
	// which, which is the label the bundle exists to remove.
	if !strings.Contains(in.A, modelPlaceholder) || !strings.Contains(in.B, modelPlaceholder) {
		t.Fatalf("the arms carry no placeholder:\nA: %s\nB: %s", in.A, in.B)
	}
	if in.A == champDiff || in.B == challDiff {
		t.Fatalf("a diff was handed over unredacted:\nA: %s\nB: %s", in.A, in.B)
	}

	// Two diffs that differ only in which model they name become the same
	// text, so naming a model cannot distinguish the arms.
	one, _, err := Bundle(champPatch("+coder: claude-opus-5-5\n"), challPatch("+coder: deepseek/deepseek-v4-pro\n"), champRunID)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if one.A != one.B {
		t.Fatalf("the arms are still told apart by the model they name: %q and %q", one.A, one.B)
	}
}
