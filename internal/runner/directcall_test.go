package runner_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestDirectCallHasNoTools: the request a registered step's call makes carries
// a system prompt and one user message and no tool list at all, so the model
// answering it can neither read a file nor run a command nor reach the network.
// That is the whole security property of the judge step (A5.2): a step hands
// the model untrusted text — two diffs another model wrote — and everything the
// model can do with it is answer.
//
// The assertion is on the bytes the upstream received rather than on the
// request struct, because the struct is this build's and the wire is what a
// model sees: the fields are read off the body the gateway forwarded.
func TestDirectCallHasNoTools(t *testing.T) {
	g := newGWFiles(t, gwConfig(t, ""), "observe", "", nil,
		anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 30000, Output: 40}))
	ctx := context.Background()
	if err := g.store.WriteTask(ctx, evaluateSpec(stepProbeName, stepProbeYAML)); err != nil {
		t.Fatal(err)
	}
	rec, err := g.run(t)
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if g.fake.Count() != 1 {
		t.Fatalf("the upstream saw %d calls, want one", g.fake.Count())
	}
	// The call went through the gateway, and this is the gateway's own count of
	// it: the fake's one request is the call the stage's report carries.
	if calls := rec.Stages[0].Calls; calls == nil || *calls != 1 || g.fake.Count() != *calls {
		t.Fatalf("the stage counted %v call(s) against %d upstream call(s)", calls, g.fake.Count())
	}
	var body map[string]any
	if err := json.Unmarshal(g.fake.Body(0), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["tools"]; ok {
		t.Fatalf("the request carries a tool list: %s", g.fake.Body(0))
	}
	var keys []string
	for k := range body {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"max_tokens", "messages", "model", "system"}; !slices.Equal(keys, want) {
		t.Fatalf("the request carries %v, want exactly %v", keys, want)
	}
	// One user message, and what the step asked for: the ceiling DirectCall
	// clamps to is the run's own stage limit, and a call under it gets what it
	// asked for.
	if msgs, ok := body["messages"].([]any); !ok || len(msgs) != 1 {
		t.Fatalf("messages = %v", body["messages"])
	} else if m, ok := msgs[0].(map[string]any); !ok || m["role"] != "user" {
		t.Fatalf("the request's one message = %v", msgs[0])
	}
	if body["model"] != sonnet || body["max_tokens"] != float64(64) {
		t.Fatalf("model = %v, max_tokens = %v", body["model"], body["max_tokens"])
	}
}
