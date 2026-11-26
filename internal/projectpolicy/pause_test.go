package projectpolicy

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// pauseNode is the node a project that has published its pause settings holds:
// the credentials block the settings act on, and the block itself.
func pauseNode(block string) string {
	return `{"version": ` + itoa(Version) + `,` +
		`"budget": {"mode": "enforce", "per_run_usd": 5},` +
		`"credentials": {"fallback": "auto", "wait_threshold": "15m"},` +
		`"pause": ` + block + `}`
}

// TestPauseSettingsDefaults: R11's three defaults (D3184, D3187, D3190), read
// through the accessors a consumer uses — the credentials service is the one
// that decides between waiting, pausing and halting by them (PR3).
//
// An absent block, a written empty one and a block that writes one member are
// asserted together, because "absent" and "empty" must not be two answers and a
// member left out is its default rather than a zero: a zero wait_in_job
// suspends a run the moment a source is exhausted, and a zero max_pause is a
// chain that may not pause at all.
func TestPauseSettingsDefaults(t *testing.T) {
	for _, tc := range []struct{ name, block string }{
		{"no block at all", ""},
		{"a written empty block", `{}`},
	} {
		node := `{"version": ` + itoa(Version) + `}`
		if tc.block != "" {
			node = pauseNode(tc.block)
		}
		p, found, err := Decode([]byte(node))
		if err != nil || !found || p == nil {
			t.Fatalf("%s: Decode = (%v, %v, %v), want a policy", tc.name, p, found, err)
		}
		z := p.Pause
		if got := z.WaitInJobDuration(); got != 5*time.Minute {
			t.Errorf("%s: wait_in_job = %s, want 5m (R11, D3184)", tc.name, got)
		}
		if got := z.MinPauseDuration(); got != 30*time.Minute {
			t.Errorf("%s: min_pause = %s, want 30m (R11, D3190 as amended)", tc.name, got)
		}
		if got := z.MaxPauseDuration(); got != 6*time.Hour {
			t.Errorf("%s: max_pause = %s, want 6h (R11, D3187)", tc.name, got)
		}
	}
	// A block that writes a member keeps it and defaults the rest, which is the
	// pair above read the other way round: the accessor answers the written value
	// and not the default beside it, and a member left out is still its default.
	p, _, err := Decode([]byte(pauseNode(`{"wait_in_job": "10m", "max_pause": "2h"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Pause.WaitInJobDuration(); got != 10*time.Minute {
		t.Errorf("wait_in_job = %s, want the written 10m", got)
	}
	if got := p.Pause.MaxPauseDuration(); got != 2*time.Hour {
		t.Errorf("max_pause = %s, want the written 2h", got)
	}
	if got := p.Pause.MinPauseDuration(); got != 30*time.Minute {
		t.Errorf("min_pause = %s, want the default 30m beside the two written members", got)
	}
	q, _, err := Decode([]byte(pauseNode(`{"min_pause": "45m"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Pause.MinPauseDuration(); got != 45*time.Minute {
		t.Errorf("min_pause = %s, want the written 45m", got)
	}
	if got := q.Pause.WaitInJobDuration(); got != 5*time.Minute {
		t.Errorf("wait_in_job = %s, want the default 5m under a minimum written alone", got)
	}
	if got := q.Pause.MaxPauseDuration(); got != 6*time.Hour {
		t.Errorf("max_pause = %s, want the default 6h under a minimum written alone", got)
	}
}

// TestPauseSettingsAreOrdered: R11's one rule about the three together — the
// in-job floor under the minimum pause under the maximum — is the node's own,
// so a project cannot publish settings the service would have to choose between.
//
// The refusals are the rule's four edges: each pair the wrong way round, and the
// document that writes one member of a pair and is held to the other's default
// (the backoff pair's reading, D153). The two rows that are accepted are the
// other half: a written order, and a boundary stated once — min_pause ==
// max_pause is a chain allowed one pause of exactly the minimum, and
// wait_in_job == min_pause is a wait that is also the shortest pause, and
// neither is a contradiction.
func TestPauseSettingsAreOrdered(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
		want  string
		ok    bool
	}{
		{"the written order", `{"wait_in_job": "2m", "min_pause": "30m", "max_pause": "6h"}`, "", true},
		{"the whole block at its defaults", `{"wait_in_job": "5m", "min_pause": "30m", "max_pause": "6h"}`, "", true},
		{"a boundary stated once", `{"wait_in_job": "30m", "min_pause": "30m", "max_pause": "30m"}`, "", true},
		{"the in-job floor above the minimum", `{"wait_in_job": "40m", "min_pause": "30m"}`, "pause.wait_in_job", false},
		{"the minimum above the maximum", `{"min_pause": "8h", "max_pause": "6h"}`, "pause.min_pause", false},
		// An absent member is its default, so these two are the same two
		// refusals with one member written: a 5m floor is above a 4m maximum's
		// minimum, and a 30m minimum is below a 20m maximum.
		{"a maximum below the default minimum", `{"max_pause": "20m"}`, "pause.max_pause", false},
		{"an in-job floor above the default minimum", `{"wait_in_job": "45m"}`, "pause.wait_in_job", false},
		{"a value that is not a duration", `{"min_pause": "half an hour"}`, "pause.min_pause", false},
		{"a duration of zero", `{"max_pause": "0m"}`, "pause.max_pause", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode([]byte(pauseNode(tc.block)))
			if tc.ok {
				if err != nil {
					t.Fatalf("the node was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the node carrying %s was read, and the rule refuses it", tc.block)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name %s: %v", tc.want, err)
			}
		})
	}
	// The block's member names are the documented ones: a misspelt member is
	// refused by the strict read rather than left at its default under a node
	// that appears to state it (checkMemberNames).
	if _, _, err := Decode([]byte(pauseNode(`{"wait_injob": "10m"}`))); err == nil {
		t.Error("a node spelling the member wait_injob was read: the name is not one the block is spelled with")
	}
}

// TestAReleaseBehindReaderToleratesPause is the compatibility story the key
// exists for (R21, F49, contract-pause.md): the release behind this one knows no
// pause, so it lists the key in UnknownKeys and reads every block it does know —
// `credentials` above all, which is the block the settings act on and the one a
// member added inside it would have made that reader refuse whole.
//
// The node is the case the acceptance names: this release writes `pause` beside
// `credentials`, and a reader that predates the key refuses nothing.
func TestAReleaseBehindReaderToleratesPause(t *testing.T) {
	raw := pauseNode(`{"wait_in_job": "2m", "min_pause": "30m", "max_pause": "6h"}`)
	p, found, err := Decode([]byte(raw))
	if err != nil || !found || p.Pause == nil {
		t.Fatalf("this Fugaro does not read its own node = (%v, %v, %v)", p, found, err)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: pause is a key this Fugaro reads", p.UnknownKeys)
	}

	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, "pause")
	q, found, err := decodeWithKeys([]byte(raw), preKey)
	if err != nil || !found {
		t.Fatalf("the reader that predates the key refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{"pause"}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("the reader that predates the key lists UnknownKeys = %v, want %v", q.UnknownKeys, want)
	}
	// The credentials block is what that reader serves the project from, and it
	// is byte for byte what this release publishes: the settings travel beside it
	// and never inside it.
	if q.Credentials == nil || q.Credentials.Fallback != FallbackAuto || q.Credentials.WaitThreshold != "15m" {
		t.Errorf("the reader that predates the key did not read credentials whole: %+v", q.Credentials)
	}
	if q.Budget.Mode != "enforce" || q.Budget.PerRunUSD != 5 {
		t.Errorf("the rest of the policy is not in force beside the key: %+v", q.Budget)
	}

	// The writer is the same read with the strict half on: the block is one this
	// release models member by member, so a command lands it (DecodeStrict) and
	// its members are held to their spelling and their order.
	if _, _, err := DecodeStrict([]byte(raw)); err != nil {
		t.Errorf("the writer refuses a node it is about to publish: %v", err)
	}
	if _, _, err := DecodeStrict([]byte(pauseNode(`{"min_pause": "30m", "wait_in_job": "45m"}`))); err == nil {
		t.Error("the writer would land a node whose settings are out of order")
	}
}
