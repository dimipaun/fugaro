package localcfg

import (
	"reflect"
	"slices"

	"gopkg.in/yaml.v3"
)

// SharedMaxBytes caps the published file when read back.
const SharedMaxBytes = 64 << 10

// Shared is the installation-wide, non-secret part of the config, the part
// that is published to the runs bucket. It drops what is owner-only
// (terraform:), personal (user:, commit_name:, commit_email:, endpoints:), derived (bucket_url, the
// legacy registry), deprecated (build.service_account) or local-only:
// providers:, whose base_url is where a key and a repository's code are
// sent, routing:, which is that same decision from the other side — the Claude
// IDs opted in to a provider route, and therefore the models whose prompts may
// leave for a third party; it resolves through the providers this file does not
// carry, so a published switch would name routes nothing in the file defines
// (the node, where a run is actually routed, is written by `project policy
// publish`), and claude_accounts:, whose allow-list is decision T1's operator-only
// trust anchor (design docs/design/named-oauth-tokens.md §3: never
// published, never settable from fugaro.yaml or the project layer, so it
// must not round-trip through the runs bucket either, which any launcher
// with objectAdmin on it can write), secret_rotation_days and secret_expires
// (the same reasoning again: they are the operator's own record of when the
// credentials they hold are rotated and when they expire, which no launcher
// should be able to write for everyone), update_check (same reasoning: a
// machine's choice to silence the nag is its own operator's, not something
// a launcher writing the shared file should be able to flip for everyone),
// and each repo's base_branch and the project's top-level base_branch, which
// the checkout's reviewed fugaro.yaml names (the project's is published to the
// policy node instead, by `fugaro project policy publish`: the node is written
// only by the owner and the runner fails closed on it, while the shared file
// is writable by any launcher). **credentials_service: is published too**, for the reason batch: and
// mailbox: are: the installation's one Cloud Run service, and therefore the
// URL `fugaro run` puts on a job's environment, is the installation's fact and
// not one machine's — the machine that launches is often not the machine that
// opted in, and a local-only copy of it is exactly what goes missing (the
// ledger item of PR #903). **batch: is published**, for the opposite reason: it is the
// installation's own facts (design docs/design/compute-backend.md §4.4, plan
// 2026-10-19-compute-backend.md Task 8 step 2) — the image a run's job runs,
// the machine type, the network and subnetwork its VMs are placed in, the
// account they run as, the socket gid — and a machine that onboarded before
// the compute root was applied must not have to write them to run somewhere
// else's jobs. Every one of its values that names a cloud resource is held to
// the anchor's GCP project when the file is read back
// (internal/cli's ParseShared), like registry_host, base_images, log_view and
// budget.firebase_project. **mailbox: is published as well**, and for the same
// reason batch: is: a keychain item's name is a fact of the installation's own
// config and not of the machine that reads the key out (mobile P4.1, M5), and a
// machine whose own config has no mailbox: block must not erase the names
// another machine recorded — one per pairing since decision 9, which is why
// MergeShared unions this block's entries rather than taking it whole. What it
// holds is the name and never the key — the key lives in the operator's OS
// keychain and is never written to disk, so nothing published here is a secret.
// The receiver is not modified. The copy is shallow except for repos: its other
// maps, slices and pointers alias the receiver's, which is safe because the
// result is only marshalled, and Marshal only reads.
func (c *Config) Shared() *Config {
	s := *c
	s.Terraform = Terraform{}
	s.User = ""
	// commit_name and commit_email are personal for the reason user: is
	// (D604, D606): they are the identity one machine's launches sign commits
	// with, and a launcher who could write them into the runs bucket would be
	// choosing whose name every teammate's commits carry — and, for
	// commit_email, whose forge account the history links to (D649).
	s.CommitName = ""
	s.CommitEmail = ""
	s.Endpoints = Endpoints{}
	s.Bucket = ""
	s.Registry = ""
	s.Providers = nil
	s.Routing = nil
	s.ClaudeAccounts = nil
	s.ProjectSecrets = nil
	// BitbucketAPIRoots is the owner's policy as well, and it travels the same
	// way ProjectSecrets does: `fugaro project policy publish` copies it into
	// the policy node, which the runner reads and fails closed on. A launcher
	// who could write this key into the runs bucket's shared file would be
	// choosing where a run sends the Bitbucket token.
	s.BitbucketAPIRoots = nil
	// Profiles are the owner's policy, published through `fugaro project
	// policy publish` into the policy node — not through the shared config a
	// launcher can write (the node is what the runner reads and fails closed
	// on). Dropping them here keeps the shared file to what it was before
	// phase 3 added profiles.
	s.Profiles = nil
	s.UpdateCheck = nil
	// The operator's rotation record is local-only for update_check's reason,
	// one credential at a time: secret_rotation_days is how often the person
	// who holds the provider accounts means to rotate them, and secret_expires
	// is what their own page for a token says — neither is a fact of the
	// installation, and a launcher who could write the shared file would be
	// choosing when every other machine is told its credentials are overdue.
	s.SecretRotationDays = 0
	s.SecretExpires = nil
	s.Build.ServiceAccount = ""
	s.BaseBranch = ""
	// ReviewFloor is the owner's policy and travels the way BaseBranch does,
	// through `fugaro project policy publish` into the policy node. It is
	// dropped here for a sharper reason than the keys above it: the floor is
	// what stops a launcher turning review off, so a floor a launcher could
	// write into the runs bucket's shared file would be that launcher setting
	// its own floor — and ParseShared refuses the key besides, because a
	// shared config carrying it was not written by the converge.
	s.ReviewFloor = nil
	s.Repos = withoutBaseBranches(c.Repos)
	return &s
}

// withoutBaseBranches is a copy of repos with every base_branch cleared.
func withoutBaseBranches(repos map[string]Repo) map[string]Repo {
	if repos == nil {
		return nil
	}
	out := make(map[string]Repo, len(repos))
	for k, r := range repos {
		r.BaseBranch = ""
		out[k] = r
	}
	return out
}

// SameInstallation reports whether a published object names the same
// installation as local: the same project name and GCP project. A foreign
// one must never be merged from.
func SameInstallation(published, local *Config) bool {
	return published != nil && published.Name == local.Name && published.GCPProject == local.GCPProject
}

// MergeShared is what to publish when published is what the runs bucket
// already holds (nil when absent) and local is this machine's config: the
// shared subset of local (Shared), filled in from published so that a
// machine that lacks what another onboarded does not erase it. Map fields
// (repos, base_images, model_prices, compute_prices, mailbox.keychain_item)
// are unioned, the local entry winning a key clash; scalar and pointer fields
// take the local value when it is non-zero, else the published one; name,
// gcp_project, runs_bucket and version always come from local. What Shared
// drops is
// never taken from published either. A published object of another
// installation (SameInstallation) is ignored. Neither argument is modified.
func MergeShared(published, local *Config) *Config {
	out := local.Shared()
	if !SameInstallation(published, local) {
		return out
	}
	out.Repos = withoutBaseBranches(unionMaps(published.Repos, out.Repos))
	out.BaseImages = unionMaps(published.BaseImages, local.BaseImages)
	out.ModelPrices = unionMaps(published.ModelPrices, local.ModelPrices)
	out.ComputePrices = unionMaps(published.ComputePrices, local.ComputePrices)
	keep := func(dst *string, old string) {
		if *dst == "" {
			*dst = old
		}
	}
	keep(&out.Region, published.Region)
	keep(&out.RegistryHost, published.RegistryHost)
	keep(&out.BackendName, published.BackendName)
	keep(&out.LogView, published.LogView)
	keep(&out.ImageSource, published.ImageSource)
	keep(&out.SchedulerRegion, published.SchedulerRegion)
	if out.Build == (Build{}) {
		out.Build = published.Build
		out.Build.ServiceAccount = ""
	}
	if out.Batch == nil {
		// The batch block is the installation's facts, not one machine's (the
		// image a run's job runs, the machine type, the network and subnetwork
		// its VMs are placed in, the account they run as, the socket gid), so
		// it is published and it is a pointer like budget: and watch:. A
		// publisher whose own config has no batch: block is a machine that did
		// not apply the compute root; taking its nil over the published one
		// would erase what another machine onboarded, silently, in the only
		// place the installation's copy lives.
		out.Batch = published.Batch
	}
	if out.Budget == nil {
		out.Budget = published.Budget
	}
	if out.Watch == nil {
		out.Watch = published.Watch
	}
	if published.Mailbox != nil || out.Mailbox != nil {
		// The mailbox block is published for the reason batch: and watch: are
		// (T2's plan, §13): the item names are the installation's record and not
		// one machine's, and a publisher whose own config has no mailbox: block
		// — a machine that onboarded before the pairing existed — would
		// otherwise erase the names another machine recorded. What it is NOT is
		// the key: the key never leaves the OS keychain it was generated into
		// (M5), so nothing published here is a secret.
		//
		// Its entries are unioned the way repos and base_images are, and not
		// taken wholesale: decision 9 made the block one item per pairing, so
		// the names in it are several machines' — a laptop that paired its own
		// phone would otherwise publish a file naming its phone and no other
		// machine's, which is exactly the erasure the paragraph above is about,
		// one entry at a time. Local wins a clash (one uid is one phone). A
		// fresh block rather than either argument's, so neither is modified.
		items := func(m *Mailbox) map[string]string {
			if m == nil {
				return nil
			}
			return m.KeychainItem
		}
		out.Mailbox = &Mailbox{KeychainItem: unionMaps(items(published.Mailbox), items(out.Mailbox))}
	}
	if out.CredentialsService == nil {
		// The credentials service block is published for the same reason, and
		// it is the one the launcher reads: the URL is the installation's own
		// Cloud Run service, and the machine that runs a launch is often not
		// the machine that opted in. A publisher whose own config has no block
		// is a machine that never ran the opt-in — a teammate who onboarded a
		// repository with `fugaro update --repo`, a CI box whose config came
		// from the published file — and taking its nil over the published one
		// would erase the installation's one record of its service, silently,
		// so that every later launch of the project went out without the
		// variable while the installation has the service on (the ledger item
		// of PR #903).
		//
		// The cost of that rule is the opt-out's: an installation whose service
		// is removed keeps the published block until an operator clears it, and
		// a launch that reads it puts a dead URL on the job. That is the
		// direction this file takes everywhere (a stale fact is recoverable; an
		// erased one is not), and the runbook's opt-out section says what to
		// remove.
		out.CredentialsService = published.CredentialsService
	}
	if out.MaxParallel == 0 {
		out.MaxParallel = published.MaxParallel
	}
	return out
}

// unionMaps is a new map with every entry of a, and every entry of b over
// it; nil when both are empty.
func unionMaps[K comparable, V any](a, b map[K]V) map[K]V {
	if len(a)+len(b) == 0 {
		return b
	}
	m := make(map[K]V, len(a)+len(b))
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

// SharedDiff names the top-level fields on which the published shared
// config differs from what local would publish (local.Shared()), sorted;
// none when they agree. It is for `fugaro doctor`: a local config wins over
// the published one, and a difference is worth saying.
func SharedDiff(published, local *Config) []string {
	toMap := func(c *Config) map[string]any {
		data, err := c.Marshal()
		m := map[string]any{}
		if err == nil {
			_ = yaml.Unmarshal(data, &m)
		}
		return m
	}
	p, l := toMap(published.Shared()), toMap(local.Shared())
	var out []string
	// These maps are a union across machines when published, while a local
	// config holds only its own entries: only the keys present locally are
	// compared, so an entry only the published object has is no difference.
	// mailbox is the same rule one level down — MergeShared unions
	// keychain_item's entries — so a pairing another machine recorded is no
	// difference either, while a local entry the published object lacks is one
	// (decision 9; without this the pairing on the next desk is reported as a
	// difference on every machine, for a file that publishing again cannot
	// change).
	union := map[string]bool{"repos": true, "base_images": true, "model_prices": true, "compute_prices": true, "mailbox": true}
	differs := func(k string, lv, pv any) bool {
		lm, lok := lv.(map[string]any)
		if !union[k] || !lok {
			return !reflect.DeepEqual(lv, pv)
		}
		pm, _ := pv.(map[string]any)
		if k == "mailbox" {
			li, _ := lm["keychain_item"].(map[string]any)
			pi, _ := pm["keychain_item"].(map[string]any)
			for uid, v := range li {
				if !reflect.DeepEqual(v, pi[uid]) {
					return true
				}
			}
			return false
		}
		for name, v := range lm {
			if !reflect.DeepEqual(v, pm[name]) {
				return true
			}
		}
		return false
	}
	for k, v := range l {
		if differs(k, v, p[k]) {
			out = append(out, k)
		}
	}
	for k := range p {
		if _, ok := l[k]; !ok && !union[k] {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
