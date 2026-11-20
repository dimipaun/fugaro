package config

// R12's two facts, as the entries a run reads them from: "this repository's
// provider keys are served" and "this repository's stage account is served".
// They are C1's names — U6 asks for exactly this, the job entries and the road
// they travel — and the whole of the arrangement is
// docs/keys-through-service/contract-provider.md.
//
// # The road, which these names are one stop on
//
// The owner's `fugaro update` is what reads a fact and decides it, because it
// is the one command that can see everything the fact is about: whether the
// bundle holds an entry for every provider credential the repository's config
// names, whether the deployed credentials service can read that entry kind,
// and whether the project publishes an allow-list (R12, F41). It records the
// answer on that repository's own entry in the LOCAL CONFIG, beside the switch
// already there (localcfg.Repo, the field CredentialsServiceReady is on), and
// the repository's root is then applied with the fact planned into the job's
// environment from that record — which is why the names are here and not in
// the runner: internal/infra plans a job with them, internal/runner reads them
// out of one, and config is the package both may import.
//
// A run reads one of these out of its own job's environment at bootstrap. Its
// TRUE value is ServedValue and nothing else, and an absent entry is not
// served — a run whose job does not carry the fact uses its mounted key, which
// is release A's whole point (R12: release A's `ready` does not change, so no
// switched repository is turned off).
//
// # What a false fact cannot do
//
// A fact set where it should not be gains the setter nothing. The fact is what
// decides whether a run SENDS a provider call through the service; it is not
// what decides whether the service serves one. The service derives the entry
// from the run's verified repository and refuses a credential it does not hold
// for it whatever the job claimed (R2), so the worst a forged fact reaches is
// that run's own call — refused, with the fix named — and never another
// repository's key (R12, HG1).
//
// The names are FUGARO_-prefixed, which is what keeps them Fugaro's own:
// config.ReservedEnv refuses a workflow secret's variable with that prefix and
// the image contract refuses it too, so nothing a repository's own workflow
// declares can write one of these into a job — the same road the switch and
// the budget variables travel.

const (
	// ProviderKeysServedEnv is R12's first fact: this repository's runs take
	// their provider keys from the credentials service rather than from the
	// keys mounted in their job. The run's gateway reads it (KS5) and sends a
	// routed provider call through the service when it is set.
	ProviderKeysServedEnv = "FUGARO_PROVIDER_KEYS_SERVED"
	// StageAccountServedEnv is R12's second fact: the mixed variant's second
	// account (the stage account) is selected by the service like any named
	// account, so the runner holds no token for it (R9, R12; KS10 reads this
	// one where the runner picks the account its stages go out on).
	//
	// It is a fact of its own rather than a second meaning for the first: the
	// two answer different questions about different credentials, one is true
	// of a repository whose project serves provider keys alone, and a run that
	// folded them would go out on a token it was never given.
	StageAccountServedEnv = "FUGARO_STAGE_ACCOUNT_SERVED"
)

// ServedValue is what either fact's entry holds when the fact is true. It is a
// value and not the entry's presence alone, so that a job planned by an older
// installation — which sets neither name — and a job whose entry was written
// empty are read the same way, and so that the fact can be read out of an
// environment dump without the reader having to know which release planned it.
const ServedValue = "1"
