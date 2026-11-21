package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// R12's two facts, at the door that decides them (KS19,
// docs/keys-through-service/reconciliation.md F31 and F32).
//
// The owner's own `fugaro update --repo` is the one command that can read what
// each fact is about — the bundle's entries, the release the deployed service
// is on, the project's published allow-list, and the stage accounts this
// repository mixes — so it decides both and records them on that repository's
// entry in the local config, beside the switch that was already there. The
// repository root's plan then hands each fact to the job from that record
// (internal/infra's TestTheJobCarriesTheFactFromTheRecord), and the run reads
// it out of its own job (C1's names, internal/config/provider_served.go).
//
// Every test here drives the door's own call, as the switch's tests do
// (update_switch_test.go): the repository door is `fugaro update --repo`, and
// what it leaves on the entry is what the plan of that same run reads.

// providerServedRepo is the repository these tests drive the record for: the
// one the rigs' local config holds.
const providerServedRepo = "acme/sandbox"

// providerServedProvider is the one model provider the fixtures below allow
// this repository to send its code to, and so the one credential R12's first
// fact owes an entry for.
const providerServedProvider = "openrouter"

// providerServedDoc is the checkout document the door reads: the api-key
// repository whose provider key the service may take over (R6, D1634 — a
// switched api-key run goes on mounting its provider key until a fact says
// otherwise, which is the whole of what this record is for).
const providerServedDoc = "bitbucket"

// providerServedProviders is the local config's providers block: one provider
// allowed to this repository, which is the set runner.ProvidersFor answers and
// therefore the set of credentials the fact is read over.
const providerServedProviders = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: [acme/sandbox]
`

// providerServedStageAccount is the local config's one mix (B0,
// docs/mixed-credentials/spec.md §11): an account this repository's review
// stage goes out on instead of its provider key, admitted to this repository by
// its own allow_data_to — the account infra.MixedStageAccounts answers with,
// which is the whole of R12's "the record names a stage account".
const providerServedStageAccount = `claude_accounts:
  bmb: { allow_data_to: [acme/sandbox] }
stages:
  review: { claude_account: bmb }
`

// providerServedPolicy is the published node R12's third condition reads: a
// project that publishes an allow-list. The models are the ones the fixture's
// provider serves, and the mode is one the service serves an api-key
// repository under — the switch's own fourth condition, which the door is
// handed the same way the repository stage hands it (policyBudgetMode of the
// node it read).
func providerServedPolicy() *projectpolicy.Policy {
	return &projectpolicy.Policy{Budget: projectpolicy.Budget{
		Mode:          config.BudgetObserve,
		AllowedModels: []string{"deepseek/*"},
	}}
}

// providerServedBundle is the bundle document a run reads: the project's own
// Anthropic key — what the switch's readiness is read from for an api-key
// repository — and one entry per name, which is how a test says what the
// service holds.
func providerServedBundle(t *testing.T, entries ...string) string {
	t.Helper()
	doc := config.AnthropicSecret + ": " + credsValue(t, "model") + "\n"
	for _, name := range entries {
		doc += name + ": " + credsValue(t, "entry") + "\n"
	}
	return doc
}

// providerServedRig is the installation the door reads either fact on: the
// credentials service deployed under image, the extra local config the fact's
// own conditions need, and the bundle the fake hands back.
//
// image is where the second condition of BOTH facts is stated: the release the
// installation's own record names, which is the release of the service a run
// of it would call (installationServiceRelease, recipes_skew.go).
// An image of "" is the rig's OWN release — what an installation an update of
// THIS release applied holds (credentialsRef) — so a test that is not about the
// release condition says nothing about it.
func providerServedRig(t *testing.T, image, extra, bundle string) *imagesRig {
	t.Helper()
	r := credentialsRig(t)
	if image == "" {
		image = r.credentialsRef()
	}
	if extra != "" {
		r.appendConfig(t, extra)
	}
	seedCredentialsService(t, r, image)
	putSwitchBundle(t, r.initRig, []byte(bundle))
	return r
}

// providerServedRecord runs the repository door's own call over the rig and
// answers the entry it left on the local config, which is the value the plan of
// that same run is built from (infra.Repo reads it out of in.LC).
func providerServedRecord(t *testing.T, r *initRig, pol *projectpolicy.Policy, mode string) localcfg.Repo {
	t.Helper()
	_, entry := providerServedRun(t, r, pol, mode)
	return entry
}

// providerServedRun is providerServedRecord with the run that made the record,
// for the test that reads what the run said about it (e.r.res.Warnings).
//
// pol and mode are what the door read before it: the published node, and the
// budget mode out of it (policyBudgetMode) — R12's third condition and the
// switch's fourth, both of which the repository stage has in hand by the time
// it calls this.
func providerServedRun(t *testing.T, r *initRig, pol *projectpolicy.Policy, mode string) (*initEngine, localcfg.Repo) {
	t.Helper()
	e := rigEngine(t, r, &initOptions{yes: true})
	cfg, problems := config.Parse([]byte(checkoutYAML(providerServedDoc, config.AuthAPIKey, "aurora", "")))
	if cfg == nil {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems)
	}
	e.r.recordCredentialsServiceReady(t.Context(), e.lc, switchOutputs(), cfg, pol, providerServedRepo,
		cfg.Git.Provider, cfg.Agent.Auth, infra.WorkflowAccounts(cfg, e.lc, providerServedRepo), mode)
	entry, ok := e.lc.Repos[providerServedRepo]
	if !ok {
		t.Fatalf("the door left no entry for %s: %v", providerServedRepo, e.lc.Repos)
	}
	return e, entry
}

// theLoaderReadsProviderEntries puts this tree in the state the release that
// lands KS1 leaves it in: the bundle's loader knows C1's named-provider family
// (providerEntriesLoaderShipped), which is what the release providerEntriesSince
// names ships.
//
// Every fact below is recorded in that world, because a bundle holding a member
// of the family is not readable at all before it — the loader refuses the WHOLE
// document at such a key (credsvc.ErrBundleUnknownKey), so the door takes no
// reading from one while this tree's loader has no arm for the family, and the
// fact and the switch alike are left as the last reading wrote them
// (TestProviderEntriesSinceIsTheLoaderThisTreeBuilds is where this tree's own
// state, and that refusal, are read). The lever is
// config.ServiceHeldSecretNames' own: the release is one variable, and it is
// back where it was when the test ends.
func theLoaderReadsProviderEntries(t *testing.T) {
	t.Helper()
	shipped := providerEntriesLoaderShipped
	providerEntriesLoaderShipped = true
	t.Cleanup(func() { providerEntriesLoaderShipped = shipped })
}

// theLoaderDoesNotReadProviderEntries puts the tree in the state before the
// arm landed, so the gate's refusal stays pinned now that the tree ships it.
func theLoaderDoesNotReadProviderEntries(t *testing.T) {
	t.Helper()
	shipped := providerEntriesLoaderShipped
	providerEntriesLoaderShipped = false
	t.Cleanup(func() { providerEntriesLoaderShipped = shipped })
}

// providerEntry is C1's own name for this repository's key at that provider
// (credsvc.RepoCredentialKey), built through the rule rather than spelled, so a
// test cannot pass on a key the copy and the loader would disagree about.
func providerEntry(t *testing.T, provider string) string {
	t.Helper()
	slug, err := task.Slug(providerServedDoc, providerServedRepo)
	if err != nil {
		t.Fatal(err)
	}
	key, err := credsvc.RepoCredentialKey(slug, provider)
	if err != nil {
		t.Fatalf("credsvc.RepoCredentialKey(%s, %s): %v", slug, provider, err)
	}
	return key
}

// TestTheFactIsRecordedWhenEveryProviderEntryIsThere is R12's first fact at the
// writer, and the RED STEP of KS19: a repository whose bundle holds an entry
// for every provider credential its config names is recorded as served — the
// value the repository root's plan hands to its jobs — while the same run over
// a bundle that holds none records that it is not.
//
// The switch is asserted on both arms, and that is F30's own half: it answers
// the same `true` over either bundle, because what it reads is the model
// credential the run's auth calls for and not a provider entry (the field above
// it, localcfg.Repo.CredentialsServiceReady). The fact is a SECOND thing the
// same run records, and the release that adds it turns no repository off.
func TestTheFactIsRecordedWhenEveryProviderEntryIsThere(t *testing.T) {
	entry := providerEntry(t, providerServedProvider)
	r := providerServedRig(t, "", providerServedProviders, providerServedBundle(t, entry))

	// The same bundle in THIS tree's own state first, before the release that
	// reads the family lands: the loader has no arm for the family
	// (providerEntriesLoaderShipped, tied to the loader by
	// TestProviderEntriesSinceIsTheLoaderThisTreeBuilds), so the document is
	// refused WHOLE and the entry keeps the records the person's last run left
	// on it — an entry no update has judged, which is what it reads as
	// (localcfg.Repo's pointers). A switch recorded from this bundle would be a
	// plan that drops the job's model mounts while the service the same run
	// deploys answers every call of the installation with the loader's refusal.
	theLoaderDoesNotReadProviderEntries(t)
	e, refused := providerServedRun(t, r.initRig, providerServedPolicy(), config.BudgetObserve)
	if refused.CredentialsServiceReady != nil || refused.ProviderKeysServed != nil || refused.StageAccountServed != nil {
		t.Errorf("a bundle this tree's loader refuses whole (%+v) wrote a record: nothing was read from it, and the switch it would carry decides whether every job of this repository keeps the model credential it mounts", refused)
	}
	if w := strings.Join(e.r.res.Warnings, "\n"); !strings.Contains(w, credsvc.RepoCredentialPrefix) {
		t.Errorf("the run wrote nothing from a bundle holding a key of C1's family and said nothing about it: a person reading its output cannot tell a bundle the release cannot read from one no update has judged, and the warning is where the refusal speaks (it said:\n%s)", w)
	}

	// And the world the release that ships the fact leaves: the loader knows the
	// family, the same bundle is a reading, and the fact is recorded from it —
	// the value the repository root's plan hands to its jobs.
	theLoaderReadsProviderEntries(t)
	with := providerServedRecord(t, r.initRig, providerServedPolicy(), config.BudgetObserve)
	if !with.CredentialsServiceOn() {
		t.Fatalf("the fixture's switch is off (%+v): the bundle holds the Anthropic key an api-key repository's readiness is read from and the mode is observe, so this test would say nothing about the fact", with)
	}
	if !with.ProviderKeysServedOn() {
		t.Errorf("the bundle holds %s, the entry for the one provider %s names, and the record says the repository's provider keys are not served: the run then keeps a provider call on the key its job mounts while the service holds one for it, and the fact KS5 reads at the gateway is never on anywhere (R12, F31)", entry, providerServedRepo)
	}

	without := providerServedRecord(t, providerServedRig(t, "", providerServedProviders,
		providerServedBundle(t)).initRig, providerServedPolicy(), config.BudgetObserve)
	if without.ProviderKeysServedOn() {
		t.Errorf("the bundle holds no entry for %s, and the record says this repository's provider keys are served: the run's gateway would send a routed call to a service that answers the loader's refusal for the entry it does not hold (R12, F32)", providerServedProvider)
	}
	if !without.CredentialsServiceOn() {
		t.Errorf("the switch is off for a repository whose bundle holds the Anthropic key, and the missing provider entry is not a condition of it: the switch is the address and the accessor, and the fact is what the call travels on (F30, F31)")
	}

	// And the universal's empty case, which is a reading of R12 and not an
	// accident: the fact is "the bundle holds an entry for every provider
	// credential the repository's config names", a repository naming none owes
	// no entry, and the contract states three conditions and no fourth
	// (docs/keys-through-service/contract-provider.md, F32). It changes nothing
	// a run does — a repository with no provider named has no routed call to
	// send — and it is recorded rather than left absent, so the run that first
	// names a provider finds the value of the last reading here instead of
	// nothing.
	noProvider := providerServedRecord(t, providerServedRig(t, "", "",
		providerServedBundle(t)).initRig, providerServedPolicy(), config.BudgetObserve)
	if !noProvider.ProviderKeysServedOn() {
		t.Errorf("a repository whose config names no provider credential is recorded as not served: R12's first condition is a universal over the credentials the config names, and its empty case holds — the fact is what a provider call would travel on, and there is none (F32)")
	}
}

// TestTheFactNeedsAnAllowList is R12's third condition at the writer: the
// service pins every accepted call to the project's published
// budget.allowed_models and refuses under none (credsvc's
// requestNoAllowListRefusal, F32, fail closed), so a project that publishes no
// list is served no routed call at all — and the fact would send a run to a
// refusal with no key of its own to fall back on, which is exactly what the
// record exists to prevent.
//
// A node this machine never read is the same answer and not an absent one: the
// door passes the nil repoPolicyInput gives it where the project has no budget
// backend, and "nothing published" is a project that publishes no list.
func TestTheFactNeedsAnAllowList(t *testing.T) {
	entry := providerEntry(t, providerServedProvider)
	theLoaderReadsProviderEntries(t)

	bundle := providerServedBundle(t, entry)
	empty := &projectpolicy.Policy{Budget: projectpolicy.Budget{Mode: config.BudgetObserve}}
	for _, tc := range []struct {
		name string
		pol  *projectpolicy.Policy
	}{
		{"no node at all", nil},
		{"a node publishing no list", empty},
	} {
		got := providerServedRecord(t, providerServedRig(t, "", providerServedProviders, bundle).initRig,
			tc.pol, config.BudgetObserve)
		if got.ProviderKeysServedOn() {
			t.Errorf("%s: the record says the repository's provider keys are served: the service refuses a call under a policy that names no model (requestNoAllowListRefusal), so the fact would send the run to a refusal, and the key its job mounts is the one answer that works", tc.name)
		}
	}

	published := providerServedRecord(t, providerServedRig(t, "", providerServedProviders, bundle).initRig,
		providerServedPolicy(), config.BudgetObserve)
	if !published.ProviderKeysServedOn() {
		t.Errorf("the fixture is the same bundle and the same provider as the arms above, and the project publishes the list this time: the fact has to be on, or the two arms above pass for the wrong reason")
	}
}

// TestAnOperatorsRunLeavesTheFactAsItWas is F31's other half, on both doors: a
// run that could not read the bundle, and the run that fills the roots no
// checkout here applies, leave the entry's records exactly as the person's own
// `fugaro update --repo` left them.
//
// The first half is the reading itself: nowhere the bundle was not read — a
// coding agent's session, a run's own process, a read that failed — is there an
// answer to record, so the door returns before it writes anything, and the
// facts are left by the same gate that leaves the switch (the record bool,
// init_credentials.go). The second is the installation door
// (backfillCredentialsServiceReady): it has neither the bundle document's
// entries, nor the published allow-list, nor a repository's own stages to judge
// a fact from, so it writes the switch alone and never a fact it would have to
// guess.
func TestAnOperatorsRunLeavesTheFactAsItWas(t *testing.T) {
	entry := providerEntry(t, providerServedProvider)
	theLoaderReadsProviderEntries(t)
	slug, err := task.Slug(providerServedDoc, providerServedRepo)
	if err != nil {
		t.Fatal(err)
	}

	// The record a person's own run left: the switch on, and the two facts as
	// THAT reading found them — the provider entry was not in the bundle yet,
	// and the repository mixes a stage account.
	r := providerServedRig(t, "", providerServedProviders+providerServedStageAccount,
		providerServedBundle(t, entry))
	markServedRecord(t, r.initRig, providerServedRepo, true, false, true)

	// One reading of the bundle is spent first, and it is the fake's own
	// accounting that needs it: FailVersionsAfter is "the first n version lists
	// succeed", so arming it at zero is not armed at all. It is a reading of its
	// own engine and writes nothing (recordForRepo's callers are the ones that
	// persist, and this is not one of them), so the file the person's run left is
	// exactly what the failing run below starts from.
	providerServedRecord(t, r.initRig, providerServedPolicy(), config.BudgetObserve)

	// The read fails after those calls: the fake stops answering the version
	// list, which is the first thing the bundle's read does.
	r.sm.FailVersionsAfter = r.sm.VersionsListCalls()

	e := rigEngine(t, r.initRig, &initOptions{yes: true})
	cfg, problems := config.Parse([]byte(checkoutYAML(providerServedDoc, config.AuthAPIKey, "aurora", "")))
	if cfg == nil {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems)
	}
	e.r.recordCredentialsServiceReady(t.Context(), e.lc, switchOutputs(), cfg, providerServedPolicy(), providerServedRepo,
		cfg.Git.Provider, cfg.Agent.Auth, infra.WorkflowAccounts(cfg, e.lc, providerServedRepo), config.BudgetObserve)
	got := e.lc.Repos[providerServedRepo]
	if !got.CredentialsServiceOn() || got.ProviderKeysServedOn() || !got.StageAccountServedOn() {
		t.Fatalf("a run whose bundle read failed left %+v, want the record the person's run wrote: the switch on, the provider fact off, the stage fact on — a false written from a read that never happened switches the service off for every machine of this installation, and a fact cleared there is a call the run no longer sends through a service that holds it", got)
	}
	// And the persisted write, which is where the file the installation
	// publishes is made: it writes the value the plan read, so the same record
	// has to be in it.
	spec := infra.RepoSpec{Name: providerServedRepo, Provider: providerServedDoc, BaseBranch: "master"}
	if err := e.r.writeRepoConfig(t.Context(), e.lc, spec, cfg, r.cfg, e.old); err != nil {
		t.Fatalf("the repository stage's config write failed: %v", err)
	}
	written, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the project config does not parse: %v", err)
	}
	persisted := written.Repos[providerServedRepo]
	if !persisted.CredentialsServiceOn() || persisted.ProviderKeysServedOn() || !persisted.StageAccountServedOn() {
		t.Errorf("the file `fugaro update` publishes for the installation no longer carries the record a run that read nothing must leave alone: %+v", persisted)
	}
	if raw := readFileString(t, r.cfg); !strings.Contains(raw, "provider_keys_served: false") ||
		!strings.Contains(raw, "stage_account_served: true") {
		t.Errorf("the local config names neither fact as the person's run left it, and a machine reading this file cannot tell the record from an entry no update has judged (localcfg.Repo's pointers):\n%s", raw)
	}

	// The installation door, over a bundle it CAN read: it writes the switch
	// and nothing beside it.
	other := providerServedRig(t, "", providerServedProviders+providerServedStageAccount,
		providerServedBundle(t, entry))
	markServedRecord(t, other.initRig, providerServedRepo, true, false, true)
	seedRepoStateGrant(t, other.initRig, slug)
	back := rigEngine(t, other.initRig, &initOptions{yes: true})
	c, err := newInitClients(t.Context(), back.lc)
	if err != nil {
		t.Fatalf("the rig's clients do not build: %v", err)
	}
	back.r.backfillCredentialsServiceReady(t.Context(), c, initStateBucket, back.lc, switchOutputs())
	after := back.lc.Repos[providerServedRepo]
	if !after.CredentialsServiceOn() {
		t.Fatalf("the installation door left the switch off for a root whose state carries the grant and whose bundle holds a model credential: this test's second half would say nothing about the facts")
	}
	if after.ProviderKeysServedOn() || !after.StageAccountServedOn() {
		t.Errorf("the installation door wrote R12's facts (%+v): it fills the roots no checkout here applies, so it has neither the bundle's entries, nor the published allow-list, nor the repository's own stages — a fact written from a readiness answer is a guess the repository door then contradicts (F31)", after)
	}
}

// markServedRecord writes on the repository's entry in the rig's local config
// the records a person's own `fugaro update --repo` leaves: the switch and
// R12's two facts, each as the test states it. It is markSwitchRepoRecord's
// shape with the two facts the tests above must see left alone.
func markServedRecord(t *testing.T, r *initRig, name string, switchOn, providerKeys, stageAccount bool) {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	at := "  " + name + ": {"
	if !strings.Contains(string(b), at) {
		t.Fatalf("the rig's local config holds no entry for %s:\n%s", name, b)
	}
	written := strings.Replace(string(b), at, at+" credentials_service_ready: "+boolWord(switchOn)+
		", provider_keys_served: "+boolWord(providerKeys)+", stage_account_served: "+boolWord(stageAccount)+",", 1)
	if err := os.WriteFile(r.cfg, []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the record the helper wrote does not parse: %v", err)
	}
	got := lc.Repos[name]
	if got.CredentialsServiceOn() != switchOn || got.ProviderKeysServedOn() != providerKeys || got.StageAccountServedOn() != stageAccount {
		t.Fatalf("the record the helper wrote reads back as %+v: the fixture is not the state this test starts from", got)
	}
}

// boolWord is the spelling a bool takes in the config a test writes by hand.
func boolWord(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// TestTheSwitchOfASwitchedRepositoryIsUnchanged is F30's own test: the release
// that records R12's facts changes no repository's switch and no `ready`.
//
// The two runs differ in one thing — the bundle holds the provider's entry or
// it does not — and everything the entry carries must come out the same but for
// the fact itself: the switch's value, and every other field of the repository
// record, which is what makes this a change to the record's content rather than
// to what the record decides. The provider fact IS asserted to move, so the
// comparison cannot pass by the two entries being equal because neither run
// looked at the bundle.
func TestTheSwitchOfASwitchedRepositoryIsUnchanged(t *testing.T) {
	entry := providerEntry(t, providerServedProvider)
	theLoaderReadsProviderEntries(t)

	on := providerServedRecord(t, providerServedRig(t, "", providerServedProviders,
		providerServedBundle(t, entry)).initRig, providerServedPolicy(), config.BudgetObserve)
	off := providerServedRecord(t, providerServedRig(t, "", providerServedProviders,
		providerServedBundle(t)).initRig, providerServedPolicy(), config.BudgetObserve)

	if !on.ProviderKeysServedOn() || off.ProviderKeysServedOn() {
		t.Fatalf("the two runs' provider facts are %v and %v: this test needs the two bundles to come apart on the fact and nowhere else", on.ProviderKeysServedOn(), off.ProviderKeysServedOn())
	}
	if !on.CredentialsServiceOn() || !off.CredentialsServiceOn() {
		t.Fatalf("the switch came out %v and %v over two bundles that differ in one provider entry: a switched repository is not turned off by a fact, and no fact is a condition of the switch (F30)", on.CredentialsServiceOn(), off.CredentialsServiceOn())
	}
	a, b := on, off
	a.ProviderKeysServed, b.ProviderKeysServed = nil, nil
	a.StageAccountServed, b.StageAccountServed = nil, nil
	if !reflect.DeepEqual(a, b) {
		t.Errorf("the entry differs by more than the fact:\n%+v\n%+v\nThe record's other members are the switch's own — the grant, the workflows, the accounts, whatever the write after the apply fills in — and the fact is a value beside them (F31).", on, off)
	}
}

// TestTheStageFactNeedsAServiceThatServesOne is R12's second fact at the
// writer: the mixed variant's second account is served only where the record
// names a stage account at all AND the deployed service is at or after the
// release that selects one — a service older than that refuses the account's
// calls, and KS10's runner would then hold no token for an account it has to
// use (F44, R9).
//
// The three arms are the three states, and the middle one is the release
// condition on the same installation otherwise: the image an update of that
// release left is what the fact is read against, and an installation that has
// not been re-applied is exactly the state the condition exists for.
func TestTheStageFactNeedsAServiceThatServesOne(t *testing.T) {
	bundle := providerServedBundle(t)
	serving := providerServedRig(t, "", providerServedStageAccount, bundle)
	got := providerServedRecord(t, serving.initRig, providerServedPolicy(), config.BudgetObserve)
	if !got.CredentialsServiceOn() || !got.StageAccountServedOn() {
		t.Errorf("an api-key repository mixing one stage account (%+v), on a service at this release's image with a bundle the switch is on for, is recorded as not served: KS10's runner then holds a token for an account the service selects, which is the second credential the run has to stop keeping", got)
	}

	// The same installation with a service the release before this one left: the
	// records have not moved, and the fact may not say the account is served
	// until the service that selects one is deployed.
	older := providerServedRig(t, oldCredentialsRef("0.26.1"), providerServedStageAccount, bundle)
	if got := providerServedRecord(t, older.initRig, providerServedPolicy(), config.BudgetObserve); got.StageAccountServedOn() {
		t.Errorf("the fact is on for an installation whose credentials service is the release before the one that serves a stage account: that service refuses the account's calls, and the runner would stop holding the token it has to use (F44)")
	}

	// And a repository that mixes nothing: there is no stage account for the
	// service to serve, whatever the service is.
	plain := providerServedRig(t, "", providerServedProviders, bundle)
	if got := providerServedRecord(t, plain.initRig, providerServedPolicy(), config.BudgetObserve); got.StageAccountServedOn() {
		t.Errorf("a repository with no stage account of its own is recorded as served one: infra.MixedStageAccounts is the set the repository's jobs are built from, and it is empty here — the runner holds nothing for an account that has no job (F44)")
	}
}

// TestProviderEntriesSinceIsTheLoaderThisTreeBuilds is the tie the release
// constant and this tree's loader have to keep, and the reason the reading is
// gated at all: the bundle's loader refuses a document carrying a key it does
// not know, and it refuses the WHOLE document (credsvc.ErrBundleUnknownKey
// answers the document and not the entry), so a bundle holding one of C1's
// entries is served only by a service whose loader has the family's arm — and
// the service a release ships is built from that release's own tree.
//
// The gate is not a value to remember: it is read here against the loader's own
// key rule (credsvc.CredentialKey, the rule internal/credsvc/bundle.go reads
// every document key with), so the tree that lands KS1 and forgets the gate
// fails here, and so does one that flips the gate without the arm.
//
// What the gate itself does with such a bundle is pinned where the fact is
// recorded, on the rig that already holds one
// (TestTheFactIsRecordedWhenEveryProviderEntryIsThere's first arm): nothing is
// written from it while the loader cannot read the family, which is what
// origin/main answered this same document with.
func TestProviderEntriesSinceIsTheLoaderThisTreeBuilds(t *testing.T) {
	key := providerEntry(t, providerServedProvider)
	if !strings.HasPrefix(key, credsvc.RepoCredentialPrefix) {
		t.Fatalf("%s is not a key of C1's family %s: this test is about what this tree's loader knows of the keys that family spells", key, credsvc.RepoCredentialPrefix)
	}
	_, _, _, known := credsvc.CredentialKey(key)
	if known != providerEntriesLoaderShipped {
		t.Errorf("credsvc.CredentialKey answers %v for %s, a key of C1's family, and providerEntriesLoaderShipped — what readCredentialsBundle gates the whole reading on, and what providerEntriesSince's claim that a service reads the family rests on — is %v: a tree whose loader has no arm for the family can build no service that reads a document holding one, and a switch or a fact recorded as if it could names a service that refuses every call of the installation", known, key, providerEntriesLoaderShipped)
	}
}
