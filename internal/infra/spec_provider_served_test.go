package infra

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// R12's two facts at the READER (KS19,
// docs/keys-through-service/reconciliation.md F31 and F32).
//
// The owner's `fugaro update --repo` is the only place either fact is judged —
// the bundle's entries, the release the deployed service is on, the published
// allow-list and the stage accounts a repository mixes are all things a plan
// build cannot see: internal/infra reads no cloud — so `update` records each
// fact on the repository's entry in the local config, beside the switch that
// was already there, and the plan hands it to a job from that record
// (internal/cli's TestTheFactIsRecordedWhenEveryProviderEntryIsThere). This is
// that last step, and it is the one a run meets: its job carries the fact as
// an environment entry (C1's names, internal/config/provider_served.go), and
// the run reads it out of its own job rather than judging it again.
//
// Both facts are hung off the SWITCH here and not merely written beside it: a
// fact is planned only where the same record's switch is on, which is the same
// value the job's model mounts and its invoker grant are planned from
// (CredentialsServiceServes). A record that says a fact is on for a repository
// this plan holds off is therefore inert rather than wrong — the job has no
// address to a service holding the key and no accessor to reach it — which is
// what lets the installation door's backfill write the switch alone
// (internal/cli's backfillCredentialsServiceReady) and the whole upgrade stay
// one command.

// servedFactsInputs is the fixture this test reads the fact off: the api-key
// repository the credentials fixture already has, with one provider its config
// allows and the switch ON — the installation's block in the outputs, the
// record the update read out of the bundle, and a published mode that serves
// an api-key repository (D2071, F30).
//
// The mode carries an allow-list as well, because that is what the update's own
// reading requires for the fact (R12's third condition, F32) — this plan never
// reads it, and the record is where the two agree.
func servedFactsInputs(t *testing.T, facts func(*localcfg.Repo)) Inputs {
	t.Helper()
	in := repoRootInputs(t, `agent: { auth: `+config.AuthAPIKey+` }`)
	in.LC = parseLC(t, repoRootAPIKeyLC)
	serviceReady(t, in.LC, in.Repo)
	entry := in.LC.Repos[in.Repo]
	if facts != nil {
		facts(&entry)
	}
	in.LC.Repos[in.Repo] = entry
	in.Policy = &projectpolicy.Policy{Budget: projectpolicy.Budget{
		Mode:          config.BudgetObserve,
		AllowedModels: []string{"deepseek/*"},
	}}
	return in
}

// servedFactsRig plans that fixture with the switch ON: the credentials service
// in the installation's outputs, which is what the plan's own first condition
// of the switch reads (and, for this repository, the rest of them: an api-key
// auth on a project publishing observe is served, CredentialsServiceServes).
func servedFactsRig(t *testing.T, facts func(*localcfg.Repo)) RepoSpec {
	t.Helper()
	in := servedFactsInputs(t, facts)
	in.Installation.CredentialsService = credentialsService()
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// withFacts is the two facts recorded as the update leaves them: written, and
// each on or off as the test states — never absent, which is the state of an
// entry no update has judged yet and the one the test's own arm pins
// separately.
func withFacts(providerKeys, stageAccount bool) func(*localcfg.Repo) {
	return func(r *localcfg.Repo) {
		r.ProviderKeysServed = localcfg.RecordSwitch(providerKeys)
		r.StageAccountServed = localcfg.RecordSwitch(stageAccount)
	}
}

// TestTheJobCarriesTheFactFromTheRecord is the red step of KS19 at the reader,
// and the whole of what the record buys: a repository whose bundle holds every
// provider credential its config names — the state the update records as
// ProviderKeysServed — has a job carrying FUGARO_PROVIDER_KEYS_SERVED, so a run
// of it asks the service for the key instead of forwarding the call on the one
// its job still mounts (KS5/KS6, R12, F31). Before the change that entry was
// nowhere in the job, which is the red step: a repository whose bundle holds
// every provider entry had a job with no such entry.
//
// The switch is asserted on every arm, and the provider mount on one: an
// api-key repository keeps the provider key it mounts even when the fact is on
// (R6, D1634). The fact is what the run's gateway reads to route the call to
// the service; the mount is what the call falls back on, and neither replaces
// the other in this release.
func TestTheJobCarriesTheFactFromTheRecord(t *testing.T) {
	on := servedFactsRig(t, withFacts(true, true))
	if on.Installation.CredentialsService == nil {
		t.Fatalf("the fixture's switch is off: an api-key repository on an observe project whose record the update wrote is what F30 says is served, and every arm below would say nothing about the facts")
	}
	ws := on.Workflows["web"]
	if got := ws.Env[config.ProviderKeysServedEnv]; got != config.ServedValue {
		t.Errorf("the record says this repository's provider keys are served and the job carries %s=%q, want %q: the run then forwards a provider call on the key its own job mounts while the service holds one for it, which is the whole of what this record is for (KS5, R12, F31)", config.ProviderKeysServedEnv, got, config.ServedValue)
	}
	if got := ws.Env[config.StageAccountServedEnv]; got != config.ServedValue {
		t.Errorf("the record says this repository's stage account is served and the job carries %s=%q, want %q: the runner then keeps a token for an account the service selects, the second credential it has to stop holding (KS10, R12, F44)", config.StageAccountServedEnv, got, config.ServedValue)
	}
	// The premise the fact is read on: a provider key is deliberately NOT one
	// of the mounts the switch takes away (B2.7, R6, D1634), so an api-key run
	// whose routed call the service refuses is one that still works on its own
	// key.
	if env := config.ProviderKeyEnv("openrouter-api-key"); ws.SecretEnv[env] == "" {
		t.Errorf("the job mounts no provider key (%v) while the fact is on: the service forwards a routed call on the key it holds, and the mount is what the call falls back on (R6, D1634)", ws.SecretEnv)
	}

	// Recorded off — the same repository after an edit that took the key out of
	// the bundle — and the job says so: the record is written false as well as
	// true, so the next launch stops routing without a second command.
	off := servedFactsRig(t, withFacts(false, false))
	offWs := off.Workflows["web"]
	if got, ok := offWs.Env[config.ProviderKeysServedEnv]; ok {
		t.Errorf("the record says this repository's provider keys are NOT served and the job carries %s=%q: a written false is the record's own answer and not an absence, and a job built from it would route a call to a service that refuses the entry it does not hold (F31, F32)", config.ProviderKeysServedEnv, got)
	}
	if got, ok := offWs.Env[config.StageAccountServedEnv]; ok {
		t.Errorf("the record says this repository's stage account is NOT served and the job carries %s=%q (F44)", config.StageAccountServedEnv, got)
	}
	// The switch is untouched by either fact: F30's own half at this reader. A
	// fact is a value the same record carries, and the release that adds it
	// turns no repository off.
	if off.Installation.CredentialsService == nil {
		t.Errorf("the switch came out off over a record whose facts are written false: the switch is the address and the accessor, and the facts are what a call travels on — a written false is not a repository switched back (F30)")
	}

	// An entry no update has judged carries NEITHER field, and the job then says
	// nothing about either: absent is not the same answer as a recorded false
	// (localcfg.Repo's pointers, MergeShared's record-over-missing), and the
	// plan may not invent a fact for a repository the update has never read.
	unjudged := servedFactsRig(t, nil)
	unjudgedWs := unjudged.Workflows["web"]
	if got, ok := unjudgedWs.Env[config.ProviderKeysServedEnv]; ok {
		t.Errorf("a record no update has judged produced %s=%q: absence is no reading at all, and a job built from an invented one routes a call the update never found an entry for (F31)", config.ProviderKeysServedEnv, got)
	}
	if got, ok := unjudgedWs.Env[config.StageAccountServedEnv]; ok {
		t.Errorf("a record no update has judged produced %s=%q (F44)", config.StageAccountServedEnv, got)
	}

	// And the AND with the switch: a record carrying both facts true for a
	// repository this plan holds OFF is inert. The job is built with no address
	// to the service and no accessor for it, so a fact planned into it would
	// name a service the run cannot reach, and the mount it still holds is the
	// only thing that would answer.
	held := servedFactsRigOff(t)
	if held.Installation.CredentialsService != nil {
		t.Fatalf("the fixture carries the credentials service with it out of the installation's outputs: this arm is the switch's own first condition, and the facts are read under it (F30)")
	}
	heldWs := held.Workflows["web"]
	if got, ok := heldWs.Env[config.ProviderKeysServedEnv]; ok {
		t.Errorf("the job carries %s=%q for a repository the switch holds off: a fact is planned only where the same record's switch is on, and a job that took one without the address would route a call to a service it has no grant for (D143)", config.ProviderKeysServedEnv, got)
	}
	if got, ok := heldWs.Env[config.StageAccountServedEnv]; ok {
		t.Errorf("the job carries %s=%q for a repository the switch holds off (D143)", config.StageAccountServedEnv, got)
	}
}

// servedFactsRigOff is servedFactsRig with the installation's block taken out
// of its outputs and the record left exactly as it was: the switch OFF, and
// both facts still recorded true. It is the state a machine whose local config
// is ahead of cloud reads — the service taken out, an installation re-applied
// without it — and the one the AND with the switch exists for.
func servedFactsRigOff(t *testing.T) RepoSpec {
	t.Helper()
	in := servedFactsInputs(t, withFacts(true, true))
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}
