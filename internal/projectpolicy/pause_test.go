package projectpolicy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// pause_test.go is PR9 of docs/model-credentials-routing: the pause block a
// project's policy carries (R11, D3184, D3187, D3190) — the three settings the
// credentials service decides an exhausted chain by. It holds the defaults an
// absent member means, the one rule the writer and the reader both ask of a
// written block, and the compatibility story the key travels as its own key
// for: a release-behind reader lists `pause` and reads every block it knows.

// pauseNode is a node carrying the pause block beside the two blocks a release
// behind this one reads strictly — the budget and the cascade — so the
// compatibility test can show both of them read whole next to the new key.
func pauseNode(members string) string {
	return `{"version": ` + itoa(Version) + `,` +
		`"budget": {"mode": "enforce", "per_run_usd": 5},` +
		`"credentials": {"fallback": "oauth-only", "wait_threshold": "15m"},` +
		`"pause": ` + members + `}`
}

// TestPauseSettingsDefaults: the three settings read the node's own member when
// one is written and the plan's figure when none is (R11) — 5 minutes under
// which a reset is waited out in the job, 30 minutes as the shortest pause, and
// 6 hours as the longest pause and the bound on a chain's whole paused time. An
// absent block is one policy with an empty one, and a block that sets nothing
// is no member at all: the database keeps no empty object, so a publish that
// wrote one would report a difference against the node it has just written.
func TestPauseSettingsDefaults(t *testing.T) {
	p, found, err := Decode([]byte(pauseNode(`{}`)))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
	}
	if p.Pause != nil {
		t.Errorf("Pause = %+v, want no block: a block that sets no member is no statement", p.Pause)
	}
	// A policy with no block at all reads the defaults, which is what a project
	// that never wrote the key pauses by.
	var none *Pause
	for _, tc := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"wait_in_job", none.WaitInJobDuration(), DefaultPauseWaitInJob},
		{"min_pause", none.MinPauseDuration(), DefaultPauseMinPause},
		{"max_pause", none.MaxPauseDuration(), DefaultPauseMaxPause},
	} {
		if tc.got != tc.want {
			t.Errorf("%s on an absent block = %s, want %s", tc.name, tc.got, tc.want)
		}
	}
	if DefaultPauseWaitInJob != 5*time.Minute || DefaultPauseMinPause != 30*time.Minute || DefaultPauseMaxPause != 6*time.Hour {
		t.Errorf("the defaults are %s, %s and %s, and R11 states 5m, 30m and 6h",
			DefaultPauseWaitInJob, DefaultPauseMinPause, DefaultPauseMaxPause)
	}

	// A written block is the operator's, member by member, and a member it
	// leaves out is the default rather than a zero.
	p, _, err = Decode([]byte(pauseNode(`{"wait_in_job": "10m", "min_pause": "45m", "max_pause": "2h"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Pause == nil || p.Pause.WaitInJob != "10m" || p.Pause.MinPause != "45m" || p.Pause.MaxPause != "2h" {
		t.Fatalf("Pause = %+v, want the three members the node wrote", p.Pause)
	}
	for _, tc := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"wait_in_job", p.Pause.WaitInJobDuration(), 10 * time.Minute},
		{"min_pause", p.Pause.MinPauseDuration(), 45 * time.Minute},
		{"max_pause", p.Pause.MaxPauseDuration(), 2 * time.Hour},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %s, want the written %s", tc.name, tc.got, tc.want)
		}
	}

	p, _, err = Decode([]byte(pauseNode(`{"min_pause": "1h"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Pause.WaitInJobDuration(); got != DefaultPauseWaitInJob {
		t.Errorf("wait_in_job = %s, want the default %s: a member the block leaves out is absent", got, DefaultPauseWaitInJob)
	}
	if got := p.Pause.MinPauseDuration(); got != time.Hour {
		t.Errorf("min_pause = %s, want the written 1h", got)
	}
	if got := p.Pause.MaxPauseDuration(); got != DefaultPauseMaxPause {
		t.Errorf("max_pause = %s, want the default %s", got, DefaultPauseMaxPause)
	}

	// The block is one this package writes back: a member it read and did not
	// write would drop the project's settings on the next publish of a config
	// that states none.
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Decode(b)
	if err != nil || back.Pause == nil {
		t.Fatalf("the node this package wrote does not decode back = (%+v, %v)", back.Pause, err)
	}
	if back.Pause.MinPause != "1h" {
		t.Errorf("the round trip lost the block: %+v", back.Pause)
	}
}

// TestPauseSettingsAreOrdered: the block's one rule, `wait_in_job` under
// `min_pause` under `max_pause` (R11). Each pair is asked of the block as the
// service would use it, an absent member being its default — so a node that
// writes one member is held to the other two's defaults, and `max_pause: 1m`
// alone is a 30-minute minimum under a one-minute maximum. It is asked by the
// writer and by every reader: a block out of order is not a policy the service
// could decide by, and half of one is not a decision.
func TestPauseSettingsAreOrdered(t *testing.T) {
	for _, tc := range []struct {
		name, members, want string
	}{
		{
			name:    "a floor at the minimum",
			members: `{"wait_in_job": "30m"}`,
			want:    "pause.wait_in_job",
		},
		{
			name:    "a floor over the minimum",
			members: `{"wait_in_job": "1h"}`,
			want:    "pause.wait_in_job",
		},
		{
			name:    "a floor at the minimum written beside it",
			members: `{"wait_in_job": "2h", "min_pause": "2h"}`,
			want:    "pause.wait_in_job",
		},
		{
			name:    "a minimum at the maximum",
			members: `{"min_pause": "6h"}`,
			want:    "pause.min_pause",
		},
		{
			name:    "a minimum over the maximum",
			members: `{"min_pause": "8h", "max_pause": "2h"}`,
			want:    "pause.min_pause",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(pauseNode(tc.members))
			// The reader a run makes refuses it too, which is the pair this
			// package holds every block's own rule to (ValidCredentials): the
			// block is not a value only the writer judges, it is one the
			// service decides by, and a service deciding by half of it would
			// wait in the job for a reset it should have paused on.
			for _, read := range []struct {
				name string
				fn   func([]byte) (*Policy, bool, error)
			}{
				{"Decode", Decode},
				{"DecodeStrict", DecodeStrict},
			} {
				_, _, err := read.fn(raw)
				if err == nil {
					t.Fatalf("%s read the block %s", read.name, tc.members)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("%s refusal does not name the member to move (%q): %v", read.name, tc.want, err)
				}
				if !strings.Contains(err.Error(), "pause") {
					t.Errorf("%s refusal does not say which key the problem is in: %v", read.name, err)
				}
			}
		})
	}

	// The order the rule admits, including the pairs one member over from a
	// default: the rule is a strict one and each of these is a block the service
	// can decide by.
	for _, tc := range []struct{ name, members string }{
		{"no member at all", `{}`},
		{"a floor under the default minimum", `{"wait_in_job": "4m"}`},
		{"a minimum over the default floor", `{"min_pause": "1h"}`},
		{"a maximum over the default minimum", `{"max_pause": "1h"}`},
		{"a floor and a minimum raised together", `{"wait_in_job": "1h", "min_pause": "2h"}`},
		{"the three written", `{"wait_in_job": "1m", "min_pause": "5m", "max_pause": "12h"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := Decode([]byte(pauseNode(tc.members)))
			if err != nil || p == nil {
				t.Fatalf("the block %s was refused: %v", tc.members, err)
			}
		})
	}

	// A member that is not a duration at all, and one that is not positive, are
	// refused where the cascade's own durations are.
	for _, tc := range []struct{ name, members string }{
		{"a floor that is not a duration", `{"wait_in_job": "soon"}`},
		{"a minimum of zero", `{"min_pause": "0s"}`},
		{"a negative maximum", `{"max_pause": "-1h"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Decode([]byte(pauseNode(tc.members))); err == nil {
				t.Fatalf("the block %s was read", tc.members)
			}
		})
	}

	// The rule is asked of the block itself, so a caller holding one — the local
	// config's carrier, and the merge of that carrier with the published node —
	// asks it without a node in hand.
	if err := ValidPause(&Pause{WaitInJob: "1h", MinPause: "2h", MaxPause: "3h"}, "pause"); err != nil {
		t.Errorf("a block in order was refused: %v", err)
	}
	if err := ValidPause(&Pause{MinPause: "8h"}, "pause"); err == nil {
		t.Error("a block whose minimum is over the default maximum was accepted")
	}
	if err := ValidPause(nil, "pause"); err != nil {
		t.Errorf("an absent block was refused: %v", err)
	}
}

// TestAReleaseBehindReaderToleratesPause: the compatibility story the key
// travels as a key of its own for (R11, F49, R21). The release behind this one
// knows no `pause`, so it lists the key in UnknownKeys and reads every block it
// does know — the budget, the cascade with `credentials.wait_threshold` still
// decoded (R4: retired, and still read) — which is what lets an update publish
// this block while the runs of that release keep going. A member added inside
// `credentials` would make that reader refuse the WHOLE node, and the node is
// re-read while runs are in flight.
func TestAReleaseBehindReaderToleratesPause(t *testing.T) {
	raw := pauseNode(`{"wait_in_job": "10m", "min_pause": "45m", "max_pause": "2h"}`)
	p, found, err := Decode([]byte(raw))
	if err != nil || !found || p == nil {
		t.Fatalf("this Fugaro does not read its own node = (%v, %v, %v)", p, found, err)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: pause is a key this Fugaro reads", p.UnknownKeys)
	}
	if p.Pause == nil || p.Pause.WaitInJobDuration() != 10*time.Minute {
		t.Fatalf("this Fugaro read no pause block: %+v", p.Pause)
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
	if q.Budget.Mode != "enforce" || q.Budget.PerRunUSD != 5 {
		t.Errorf("the rest of the policy is not in force beside the key: %+v", q.Budget)
	}
	if q.Credentials == nil {
		t.Fatal("the reader that predates the key did not read the cascade at all")
	}
	if q.Credentials.Fallback != FallbackOAuthOnly || q.Credentials.WaitThreshold != "15m" {
		t.Errorf("the reader that predates the key did not read the cascade whole: %+v", q.Credentials)
	}
	// The seam models the older reader's *key list* and not its body, so the
	// block it does not know is still decoded here (decodeWithKeys' own note);
	// the row that matters is that it refuses nothing and reads every block it
	// knows, which the assertions above say.
	if q.Pause == nil {
		t.Error("the seam read no pause block at all, and the note above says the block is decoded whatever the key list")
	}

	// The node's cascade is the one the runs of the release behind read to
	// decide how they fall over, and this key does not touch it: a node that
	// carries the pause block and no cascade still decodes, and the members
	// that release wrote there — `fallback`, and D153's `wait_threshold`, which
	// R4 retires from the chain and keeps decodable — are read here as they
	// were.
	cred, _, err := Decode([]byte(`{"version": ` + itoa(Version) + `, "credentials": {"fallback": "auto", "wait_threshold": "20m"}, "budget": {"mode": "enforce", "per_run_usd": 5}, "pause": {"min_pause": "1h"}}`))
	if err != nil {
		t.Fatalf("a node carrying both blocks was refused: %v", err)
	}
	if cred.Credentials == nil || cred.Credentials.WaitThreshold != "20m" {
		t.Fatalf("the cascade is not read beside the pause block: %+v", cred.Credentials)
	}
	if cred.Pause == nil || cred.Pause.MinPauseDuration() != time.Hour {
		t.Fatalf("the pause block is not read beside the cascade: %+v", cred.Pause)
	}
}
