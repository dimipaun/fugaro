//go:build firebase

package rules

// P5.4.3: the front agent's brief at the database (mobile plan task P5.4.3;
// design front-agent.md §3.3 and §3.4).
//
// P5.4.2 landed the brief's nodes and held them to the shared schema field for
// field (TestMailboxRulesAgreeWithTheSharedSchema), and the golden pins the
// document that carries them. What neither proves is that the fence *holds*:
// that a brief outside its bound, a brief with no agent, a brief beside a verb
// that is not the launch, or a brief on a kind that cannot carry one is refused
// by the database and not merely by the app that wrote it. That is this
// generation's directed case set, in emulator_mailbox_test.go's shape — one
// positive control per shape the rules accept, and an explicit refusal for each
// way a writer could try to slip a brief past it.
//
// The generation the brief rides is 12 (mobile plan tasks P5.4.2 and P5.4.3):
// v0.29.0 ships generation 11 with no `brief`, so the field is a write a client
// holds and it takes the first free generation at the merge — the number
// projectpolicy/version.go's generation-12 block states. What P5.4.3 ships is
// that number and the denials below.

import (
	"strconv"
	"strings"
	"testing"
)

// aLaunchRecord is the typed launch a "go" carries (O10): a repository alone as
// the target, a rigor mode, and no confirmation — a launch needs none (D162).
func aLaunchRecord() map[string]any {
	return map[string]any{
		"id":     "act-go-1",
		"verb":   "launch",
		"mode":   "quick",
		"target": map[string]any{"repo": "dimipaun/fugaro"},
	}
}

// aBrief is the shaped task the brief carries: its first line is the
// `# Task: <title>` line a run's stored record is made of, so `fugaro watch`
// and the pull request title come from it.
const aBrief = "# Task: docs refactor"

// aGo is a launch message carrying the brief beside the action record, for the
// agent the brief is for (D161). The brief is a field of the *message* and
// never of the action record or its target, which is why it is set here.
func aGo(id, mb string, brief any) map[string]any {
	m := actionMessage(id, mb, aLaunchRecord())
	m["agent"] = "agent-1"
	m["brief"] = brief
	return m
}

// TestFrontAgentBriefIsAdmittedAndBounded: the positive control and the length
// bound. A "go" the front agent wrote — a launch, its brief and the agent the
// brief is for — is stored whole; the bound is the schema's BRIEF_MAX, inclusive
// on both sides, exactly as MaxBriefLength and the shared schema's BRIEF_MAX
// state it.
func TestFrontAgentBriefIsAdmittedAndBounded(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)

	h.mustAllow(alice, "a launch carrying a brief", put(inbox(mbAlice, "g1"), aGo("g1", mbAlice, aBrief)))
	h.wantValue(inbox(mbAlice, "g1")+"/brief", strconv.Quote(aBrief))
	// The agent travels with the brief: it is what the consumer routes the turn
	// by (D161), and a brief from nobody is a turn it could never route.
	h.wantValue(inbox(mbAlice, "g1")+"/agent", `"agent-1"`)

	// The owner's ruling of 2026-11-27: a launch may carry no brief, because the
	// app's typed start keeps its task in the message's own `text`.
	noBrief := actionMessage("g2", mbAlice, aLaunchRecord())
	h.mustAllow(alice, "a launch with no brief", put(inbox(mbAlice, "g2"), noBrief))

	// The bound, from both sides: exactly MaxBriefLength is admitted, one
	// character more is refused. A brief is long on purpose — it is the document
	// a run is started from — so this is the fence the app's own bound mirrors.
	atBound := "# Task: " + strings.Repeat("a", MaxBriefLength-len("# Task: "))
	h.mustAllow(alice, "a brief of exactly MaxBriefLength", put(inbox(mbAlice, "g3"), aGo("g3", mbAlice, atBound)))
	h.mustDeny(alice, "a brief one character over MaxBriefLength",
		put(inbox(mbAlice, "g4"), aGo("g4", mbAlice, atBound+"a")))

	// An empty brief is not a shaped task: a go whose brief is empty starts
	// nothing, so the database refuses it rather than storing a launch the
	// consumer must then refuse.
	h.mustDeny(alice, "an empty brief", put(inbox(mbAlice, "g5"), aGo("g5", mbAlice, "")))
	// A brief that is not a string is a value no reader can use — the schema's
	// type refusal, restated at the fence.
	h.mustDeny(alice, "a brief that is not a string", put(inbox(mbAlice, "g6"), aGo("g6", mbAlice, 42)))
}

// TestFrontAgentBriefCarriesItsAgent: the coupling the shared schema states and
// the rules hold to it (BRIEFOK). A brief with no agent is refused at the
// fence, not merely at the reader: the inbox is write-once, so a launch the
// database stored and the consumer could not route is one nobody could retire.
func TestFrontAgentBriefCarriesItsAgent(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)

	m := aGo("g1", mbAlice, aBrief)
	delete(m, "agent")
	h.mustDeny(alice, "a brief with no agent on the message", put(inbox(mbAlice, "g1"), m))

	// The agent is held to the id the consumer advertises (D161, D163): a value
	// the advertised list could never carry is not an agent.
	bad := aGo("g2", mbAlice, aBrief)
	bad["agent"] = "not an agent id"
	h.mustDeny(alice, "a brief whose agent is not an agent id", put(inbox(mbAlice, "g2"), bad))
}

// TestFrontAgentBriefIsALaunchFieldAlone: `brief` sits on a `launch` and
// nowhere else. Beside any other verb it is a field the record has no place
// for, and on a kind that cannot carry an action it is a field the message
// does not name — the (kind, field) table P6.3 removes the ambiguity with.
func TestFrontAgentBriefIsALaunchFieldAlone(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)

	// The other three verbs, each with the target shape its own arm resolves to.
	cancel := map[string]any{
		"id": "act-2", "verb": "cancel", "target": map[string]any{"run": "r1"},
		"confirmation": map[string]any{"id": "cfm-2", "at": msgAt},
	}
	merge := map[string]any{
		"id": "act-3", "verb": "merge", "target": map[string]any{"repo": "dimipaun/fugaro", "pr": 7},
		"head_sha":     strings.Repeat("a", MinShaLength),
		"confirmation": map[string]any{"id": "cfm-3", "at": msgAt},
	}
	confirm := map[string]any{
		"id": "act-4", "verb": "confirm", "target": map[string]any{"run": "r1"},
		"confirmation": map[string]any{"id": "cfm-4", "at": msgAt},
	}
	for i, rec := range []map[string]any{cancel, merge, confirm} {
		m := actionMessage("v"+strconv.Itoa(i), mbAlice, rec)
		m["agent"] = "agent-1"
		m["brief"] = aBrief
		h.mustDeny(alice, "a brief beside the verb "+rec["verb"].(string),
			put(inbox(mbAlice, "v"+strconv.Itoa(i)), m))
	}

	// A brief on a kind that cannot carry one: a plain message, and a prompt.
	plain := aMessage("k1", mbAlice)
	plain["brief"] = aBrief
	h.mustDeny(alice, "a brief on an ordinary message", put(inbox(mbAlice, "k1"), plain))
	prompt := aMessage("k2", mbAlice)
	prompt["kind"] = "prompt"
	prompt["options"] = map[string]any{"0": "yes", "1": "no"}
	prompt["brief"] = aBrief
	h.mustDeny(alice, "a brief on a prompt", put(inbox(mbAlice, "k2"), prompt))

	// A brief written inside the action record, or inside its target, is a
	// field neither node names — the record's own `$other` and the target's.
	inRecord := aGo("w1", mbAlice, nil)
	delete(inRecord, "brief")
	inRecord["action"].(map[string]any)["brief"] = aBrief
	h.mustDeny(alice, "a brief inside the action record", put(inbox(mbAlice, "w1"), inRecord))
	inTarget := aGo("w2", mbAlice, nil)
	delete(inTarget, "brief")
	inTarget["action"].(map[string]any)["target"].(map[string]any)["brief"] = aBrief
	h.mustDeny(alice, "a brief inside the launch's target", put(inbox(mbAlice, "w2"), inTarget))
}

// TestFrontAgentGoIsTagged: a go is a mailbox message, so it carries exactly one
// of `mac` and `sig` (J5r, O6) — the same fence every other message of the
// mailbox carries, restated here because the go is the one message that
// authorises a run and an untagged one is an impostor's.
func TestFrontAgentGoIsTagged(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)

	untagged := aGo("g1", mbAlice, aBrief)
	delete(untagged, "mac")
	h.mustDeny(alice, "a launch with a brief and no tag", put(inbox(mbAlice, "g1"), untagged))

	// The other scheme is admitted: a device that holds a key signs rather than
	// tags (O6), and which one it must use is its own device node's (J5b).
	signed := aGo("g2", mbAlice, aBrief)
	delete(signed, "mac")
	signed["sig"] = aSig
	h.mustAllow(alice, "a launch carrying a brief and signed", put(inbox(mbAlice, "g2"), signed))
}
