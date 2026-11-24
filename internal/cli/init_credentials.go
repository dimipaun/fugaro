package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
	"gopkg.in/yaml.v3"
)

// The credentials service's image, and the two names the release publishes it
// under. The service itself is deployed by `fugaro update` on every
// installation (docs/credentials-service/default-on.md, the owner's ruling of
// 2026-11-05): what is left in this file is the resolve the deploy pins
// through -- init_stages.go's ensureCredentialsService, which writes the
// installation root's block, and its moveCredentialsImage, which moves an
// existing one onto this release's image after the policy stage has migrated
// the node -- and the two constants the gates that name the service read.

// resolveCredentialsImage is the service's image, resolved and verified
// exactly as a base kind's pin is: the release's "-pinned" tag, its cosign
// signature, and the digest read back through the installation's remote
// repository with the operator's own reader grant on it. What comes out is the
// reference the tfvars carry, digest-pinned; a tag is never returned.
//
// It takes the run's engine rather than a stage because two paths pin through
// it and both must pin the same way: the deploy that gives an installation its
// first block, and the move that takes an installation's recorded image forward
// to this release's (init_stages.go's moveCredentialsImage). Neither
// writes a digest this did not return: the release's own signed "-pinned"
// image, read back through the remote repository the job pulls by.
func resolveCredentialsImage(ctx context.Context, e *initEngine) (string, error) {
	ver := releaseVersion()
	if ver == "" {
		return "", userErr("this is a development build, so there is no released credentials service image to pin: the service runs the released image, resolved at its signed %q tag, and a build from a checkout has none; run this stage with a released fugaro", credentialsKind+"-pinned")
	}
	allow := e.images.allow()
	srcHost, srcOwner, _ := strings.Cut(allow[0], "/")
	if srcHost != baseSourceHost {
		return "", userErr("the credentials service's image is pinned through the installation's remote repository, whose upstream is https://%s, so its source must be %s/<owner>, and this run's is %s: pass --image-source %s/<owner>", baseSourceHost, baseSourceHost, allow[0], baseSourceHost)
	}
	digest, err := resolvePinnedBase(ctx, allow[0], credentialsKind, ver)
	if err != nil {
		return "", remote(fmt.Errorf("the credentials service's image: %w", err))
	}
	host, err := infra.RegistryHost(e.lc)
	if err != nil {
		return "", userErr("%v", err)
	}
	m, err := newMirror(ctx, e.lc, allow)
	if err != nil {
		return "", err
	}
	regHost, project := registryParts(host)
	client, err := newARClient(ctx, m)
	if err != nil {
		return "", err
	}
	upstream := srcOwner + "/fugaro-" + credentialsKind
	if err := mirror.VerifyThroughRemote(ctx, client, regHost, project, infra.GhcrRegistry, upstream, digest); err != nil {
		return "", remote(fmt.Errorf("the credentials service's image: %w", err))
	}
	return mirror.RemoteRef(regHost, project, infra.GhcrRegistry, upstream, ver, digest), nil
}

// credentialsKind is the release image's kind: the name the release publishes
// it under (ghcr.io/<owner>/fugaro-credentials) and the "-pinned" tag's own
// word.
const credentialsKind = "credentials"

// credsServiceSince is the release that carries the credentials service, for
// the gates that name what to upgrade to.
const credsServiceSince = "0.10.0"

// providerEntriesSince is the first fugaro release whose credentials service
// reads C1's named-provider entries (RepoCredentialPrefix): the release KS1
// lands in, and the second of R12's first fact's three conditions — a bundle
// holding the entry is served only by a service that can load the document
// that holds it, because the loader refuses a document carrying a key it does
// not know (ErrBundleUnknownKey) and a service older than this one answers
// every call of that installation with that refusal.
//
// It is read here rather than in internal/credsvc because the question is
// about the service an installation has DEPLOYED — the image its own
// `fugaro update` pinned, `installationServiceRelease` — and never about the
// release this CLI is: an operator running a newer CLI against an installation
// whose service was never re-applied is exactly the state this condition
// exists for.
//
// It names 0.27.0, the release this work ships in, which is not tagged yet
// (docs/release-streams.yaml has 0.26.1 current and Xerus 0.27.0 next), and
// the name is a claim this tree can be held to rather than one to trust: the
// service a release ships is built from that release's own loader, so a tree
// whose loader has no arm for the family cannot build the service this
// constant names — providerEntriesLoaderShipped below is that loader's own
// answer, readCredentialsBundle refuses the whole reading while it is false,
// and TestProviderEntriesSinceIsTheLoaderThisTreeBuilds ties the two together.
// It is the release that ships the entry, so it must not RISE once that
// release is tagged: a value above it withholds C1's first fact from
// installations whose deployed service already reads the entry.
const providerEntriesSince = "0.27.0"

// providerEntriesLoaderShipped is whether the loader this tree's service is
// built with knows C1's named-provider family — the arm KS1 added to
// credsvc.CredentialKey, the key rule internal/credsvc/bundle.go reads every
// document key with. It is TRUE in this tree because KS1 has landed: a
// document carrying a key of the family is one this loader reads. Before the
// arm it was false, because the loader refused such a document WHOLE
// (credsvc.ErrBundleUnknownKey answers the document and not the entry alone).
//
// It is what readCredentialsBundle gates the whole reading on. It is a
// variable so that a test can put the tree in the state the release that lands
// KS1 leaves it in — config.ServiceHeldSecretNames' own lever, set and
// restored by a helper — while TestProviderEntriesSinceIsTheLoaderThisTreeBuilds
// holds it to the loader's own key rule, so that flipping it without the arm,
// or landing the arm without flipping it, fails there instead of in a release.
var providerEntriesLoaderShipped = true

// errProviderEntriesUnreadable is what a bundle holding a key of that family
// is answered with while providerEntriesLoaderShipped is false: the reading is
// refused whole, the way every read that could not be made is (unreadable),
// and not taken around the family. What the refusal states is a fact about the
// document — the service the same run deploys cannot read it — which is the
// kind of fact the switch is read from, and not a read that failed at a store.
var errProviderEntriesUnreadable = fmt.Errorf("the document holds an entry of %s (C1's named-provider family) and the loader this release's service is built with knows no key of that family, so that service answers the whole document with credsvc.ErrBundleUnknownKey rather than serving the entries around it", credsvc.RepoCredentialPrefix)

// stageAccountServedSince is the first fugaro release whose credentials
// service serves a mixed stage account — the release KS9 lands in — and it is
// the third condition of R12's second fact: a record naming a stage account is
// a fact about the repository, and whether the deployed service selects that
// account "like any named account" (R9) is a fact about the service, so a
// service older than this one must not be told the stage account is served —
// the runner would hold no token for an account whose calls the service
// refuses.
//
// It is a constant of its own and not providerEntriesSince, although the two
// releases are the same one today: they are two capabilities of the service
// (reading a named-provider entry; admitting a stage account for its stage),
// and a release that slips or moves takes one of them with it. It names
// 0.27.0, this work's release, not tagged yet, on providerEntriesSince's
// reasoning.
const stageAccountServedSince = "0.27.0"

// readCredentialsBundle is the installation's credentials service's bundle as
// this run read it: the NAME of every model credential it holds, or record
// false where there was no reading at all. It is the readiness half of the
// switch (infra.CredentialsServiceServes, docs/credentials-service/default-on.md
// §1.5), read once for the run and judged per repository by
// recordCredentialsServiceReady.
//
// It answers names rather than one yes/no because the question is each
// repository's own (credentialsBundleServes, credentialsBundleReady): the
// bundle holds every model credential of the installation, and which of them a
// repository's runs may be served is its agent.auth's to say. One reading
// answers all of them, and that is what the memo is for.
//
// It is the service's OWN loader and never a second reading of the secret: the
// same store `fugaro creds set` writes through (openBundleStore), the same
// newest-version read, and the same credsvc.ParseBundle the running service
// parses with, so "the bundle holds a model credential" means here exactly
// what it means to the process that has to hand one back. The document's own
// entries answer it and not the container's existence: a container with no
// version at all, one whose newest version is DISABLED, and one that parses
// with no KindModel entry are three ways of holding nothing.
//
// A read that FAILS is not a reading, which is why it is not an answer either
// (the review of 2026-11-07's third round). The three states that answer are
// facts about the bundle itself, true for every reader: no version at all, a
// newest version that is DISABLED (the state `creds set`'s rotation leaves the
// version it replaced, and the service reads `latest`), and a document that
// parses without a KindModel entry. An error is none of those — a container
// this machine's identity may not read, a version it may not access, a listing
// that failed, a document that does not parse — and the identity that reads
// here is the operator's own, never the service's: the answer a failed read
// would write is a claim about every other machine of the installation, since
// the record travels in the local config that `fugaro update` publishes
// (internal/localcfg/shared.go). Written false there, one refused read would
// switch the service off for every teammate's `fugaro run` — including the ones
// whose jobs had their model mounts dropped by the last apply and have nothing
// else to run on — and every one of them halts at the service's door (D143) on
// the word of a machine that read nothing. So a failed read leaves the record
// as the last reading wrote it, warns what failed, and fails no run.
//
// Nothing here reads the BUDGET backend or the node: the switch's other half,
// the repository's agent.auth, is read where the spec is built, from the
// checkout's own fugaro.yaml (infra.CredentialsServiceServes).
//
// The second answer, record, is whether there is a reading to write down at
// all, and it is false wherever the bundle was not read: a coding agent's
// session and a Fugaro run's own process, where it is not opened — its
// document holds every model credential the service issues, and `fugaro creds
// ls/set` make the same refusal before their first call (credsOwnerOnly,
// internal/cli/creds.go) — and every failed read above. The callers leave each
// entry's own record as the person's last run wrote it then. Answering false
// in either of those states instead would be a write no reading produced: it
// would switch a repository off, or keep it off, on a session's word alone,
// and an agent running `fugaro update` — or an operator whose identity lost
// the bundle's accessor role — would silently rewrite the switch of every
// repository this machine serves.
//
// Both answers are memoized, and that is part of the rule above rather than an
// optimization: one run asks twice — the installation door's backfill, then the
// repository stage's record (init.go's install and repoEngine) — and the second
// answer is a property of the READING, not of the question. Memoizing the
// reading alone would leave the second door answering "the bundle holds no
// model credential" out of a memo the first door wrote as "there was no
// reading": a false no read produced, written into the entry and published for
// the installation, which is the one write this function exists to refuse.
//
// The reading has a further answer, credsBitbucket: whether the document holds
// the upstream Bitbucket token, which `fugaro update`'s gate on the flip's drop
// asks before it applies a proxied repository's root (bundleBitbucketToken,
// update_proxied.go; O8). It is settled from the same parsed document for the
// same reason — the gate and the switch record are two questions about one
// apply, so a second read would let them answer from two documents — and it is
// false wherever there was no reading and wherever the bundle holds no
// document, which is the fact said twice rather than a fourth state.
//
// credsBitbucketTyped is the same fact about the NAMED token family (D2350):
// the proxy serves a typed record as readily as the legacy entry (R5, T15 —
// "any mix within one workspace"), so the gate S6's flip is held by reads both
// and answers "the bundle holds a Bitbucket token" for either. It is false
// wherever credsBitbucket is and for the same three reasons, and neither is a
// question about which token the proxy would pick for a request: that selection
// is the service's own (internal/credsvc/bitbucketproxy.go).
//
// The marker of a session that may not open the bundle is remembered beside
// them (credsUnreadMarker), because a caller has to tell that no-reading from a
// read that FAILED: the first is a question the session may not answer at all,
// and the second is a state a person can be sent to fix — so the gate above
// goes on under the first and refuses on the second, and a branch added here
// that answers without a read is refused on by default.
func (r *initRun) readCredentialsBundle(ctx context.Context, lc *localcfg.Config) (names map[string]bool, record bool) {
	if m := agentMarker(os.Getenv, r.stdinTerminal()); m != "" {
		r.credsUnreadMarker = m
		r.warn(fmt.Sprintf("the credentials service's bundle was not read (%s is set, so this is a coding agent's session) and the switch's record is left as this machine last wrote it: the bundle holds every model credential the service issues, and a person's own %s reads it", m, selfCommand()))
		return nil, false
	}
	if m := managedFlagRunMarker(os.Getenv); m != "" {
		r.credsUnreadMarker = m
		r.warn(fmt.Sprintf("the credentials service's bundle was not read (%s is set, so this process is a Fugaro run's) and the switch's record is left as this machine last wrote it: the bundle holds every model credential the service issues, and a person's own %s reads it", m, selfCommand()))
		return nil, false
	}
	if r.credsRead {
		return r.credsNames, r.credsRecord
	}
	r.credsRead = true
	// answer is the three states that ARE readings, and it memoizes the switch
	// with them: the reading is spent, so every later door of this run reads it
	// here rather than deciding again from a memo that answered nothing.
	answer := func(names map[string]bool) (map[string]bool, bool) {
		r.credsNames, r.credsRecord = names, true
		return names, true
	}
	if lc == nil || lc.CredentialsService == nil {
		// No service, nothing to serve a run with — and no read to spend:
		// the block's absence is the answer (the installation could not
		// deploy the service, or its owner removed it).
		return answer(nil)
	}
	unreadable := func(err error) (map[string]bool, bool) {
		r.warn(fmt.Sprintf("the credentials service's bundle was not read (%s) and the switch's record is left as the last reading wrote it: nothing was read, so there is no answer to write, and a false published here would switch the service off for every machine of this installation", oneLineCLI(err.Error())))
		return nil, false
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return unreadable(err)
	}
	if err := store.check(ctx); err != nil {
		return unreadable(err)
	}
	meta, err := store.newest(ctx)
	if errors.Is(err, errNoBundleVersion) {
		// An empty container is a state and not a failure: the service was
		// deployed and nothing has been stored in it yet, which is exactly
		// the installation a fresh deploy leaves behind.
		return answer(nil)
	}
	if err != nil {
		return unreadable(err)
	}
	if meta.state == "DISABLED" {
		return answer(nil)
	}
	doc, err := store.access(ctx, meta.number)
	if err != nil {
		return unreadable(err)
	}
	// C1's named-provider entries are read off the document's OWN keys — they
	// are what R12's first fact is over — and taken out of the bytes the loader
	// parses (withoutRepoCredentials), so that every other rule of the loader
	// is still the loader's own and this reading cannot disagree with the
	// service about any other key.
	//
	// The family is also why this reading can be refused whole, and it is
	// refused: the loader answers a document carrying a key it does not know
	// with ErrBundleUnknownKey, and the loader that matters is the one the
	// service is built with — the service of every release, by the constant
	// above. A tree whose own loader has no arm for the family (KS1, unmerged)
	// is a tree whose service would answer every call of this installation with
	// that refusal, so a bundle holding one of the family's keys is not read
	// around: it is not read at all — the state it was in before this reading
	// learned to take the family out. Reading it anyway would record the switch
	// and both facts true — the plan drops the job's model mounts on the switch
	// — while the service the same run deploys refuses every call of it.
	held, stripped := withoutRepoCredentials(doc.raw)
	if len(held) > 0 && !providerEntriesLoaderShipped {
		return unreadable(errProviderEntriesUnreadable)
	}
	b, err := credsvc.ParseBundle(stripped)
	if err != nil {
		return unreadable(err)
	}
	r.credsRepoCredentials = held
	// Two facts and not one, because two questions are asked of them
	// (credentialsBundleServes): the model credentials, by the name each is
	// served under — the account's for tier A, config.AnthropicSecret for the
	// paid key — and the GitHub App's private key, which a repository on
	// GitHub needs the service to mint with and which is no model credential
	// at all. The provider key is read beside them and kept apart (credsProviderKey):
	// it IS a model credential, but it is reached under the account name
	// config.ProviderKeyAccount rather than its own, so it is not one of
	// `names` and a caller that needs it reads the flag.
	// The same reading answers the gate on the flip's drop: whether the bundle
	// holds the upstream Bitbucket token, which is a question about the document
	// just parsed and not a second read of the secret (bundleBitbucketToken,
	// update_proxied.go). The three states above answered with no document at
	// all — and a document read is one that may hold the token, so they leave it
	// false, which is what they say: an empty container, a DISABLED newest
	// version and a config with no service hold nothing to fetch.
	_, r.credsBitbucket = b.BitbucketToken()
	r.credsBitbucketTyped = len(b.BitbucketTokens()) > 0
	creds := b.Credentials()
	names = make(map[string]bool, len(creds))
	for _, c := range creds {
		names[c.Name] = true
	}
	_, r.credsAppKey = b.AppKey()
	_, r.credsProviderKey = b.ProviderKey()
	return answer(names)
}

// withoutRepoCredentials is a bundle document with C1's named-provider entries
// taken out, and the document keys of that family it held: the reading R12's
// first fact is made over (credentialsProviderEntries), taken from the
// document's own top-level keys through C1's own naming rule.
//
// It is the same node walk bundleHoldsKey takes (creds.go), for the same
// reason — the question is about keys of C1's own naming rule — and it differs
// in one thing: the keys are REMOVED, so that what the loader is handed is a
// document it reads, with the family judged by the fact over the keys this
// function answers (credentialsProviderEntries). The loader's other rules are
// untouched by the removal — an unknown key, a duplicate, a value that is not
// a scalar still refuse this reading exactly as they refuse the service's —
// and a document holding a member of the family is refused WHOLE before this
// point wherever the loader this tree builds cannot read one
// (readCredentialsBundle, providerEntriesLoaderShipped).
//
// A document holding no member of the family — every bundle a release before
// this one wrote — comes back unchanged, byte for byte: the loader's own
// answer, its error messages included, is what answers about it, and nothing
// here reads or rewrites a document the fact is not about.
func withoutRepoCredentials(raw []byte) (held map[string]bool, stripped []byte) {
	_, root, err := bundleNodes(raw)
	if err != nil || root.Kind != yaml.MappingNode {
		// A document yaml cannot read, or one whose root is not a mapping:
		// credsvc.ParseBundle answers both, with the refusal it answers the
		// service's own reader with, and this reading adds nothing to it.
		return nil, raw
	}
	kept := make([]*yaml.Node, 0, len(root.Content))
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if _, _, ok := credsvc.RepoCredentialByKey(k.Value); !ok {
			kept = append(kept, k, v)
			continue
		}
		if held == nil {
			held = map[string]bool{}
		}
		held[k.Value] = true
	}
	if held == nil {
		return nil, raw
	}
	root.Content = kept
	out, err := yaml.Marshal(root)
	if err != nil {
		// Unreachable: a node tree that decoded from yaml encodes. Answering
		// the document unchanged hands the loader the family key and it
		// refuses the whole document, which is the fail-closed direction.
		return nil, raw
	}
	return held, out
}

// credentialsProviderEntries is R12's first fact for this repository: whether
// the bundle holds an entry for every provider credential the repository's
// config names, whether the deployed service can read that entry kind, and
// whether the project publishes an allow-list (F32). It is what the owner's
// own `fugaro update` records, and a run's gateway then sends a routed provider
// call through the service instead of using the key its job mounts (KS5).
//
// The three conditions, and each of them is somebody else's answer:
//
//   - the credentials. "The provider credentials the repository's config
//     names" is runner.ProvidersFor's own set — the providers a run of this
//     repository has, allow_data_to applied — and not a second reading of the
//     providers block: a provider the repository may not send its code to is
//     not one its runs route a call through, so it is not one the service
//     needs an entry for. Each name is turned into the document key by C1's
//     own rule (credsvc.RepoCredentialKey, over the slug task.Slug derives for
//     this repository and this provider) and looked up in what the reading
//     held, so the key this asks about is the key KS12's copy writes and KS1's
//     loader reads, byte for byte.
//   - the deployed service's release (providerEntriesSince): the entry kind is
//     one only a service at or after that release loads, and one older answers
//     every call of the installation with the loader's unknown-key refusal.
//     A release that cannot be read — no workdir on this machine, no tfvars,
//     no image (installationServiceRelease's "not known") — is not "at or
//     after" it, and the fact stays false: the direction this fails in is the
//     one that changes nothing, the run keeping the key its job mounts.
//   - the allow-list (F32): the service pins a routed call to the project's
//     published budget.allowed_models and refuses a call under none
//     (credsvc's requestNoAllowListRefusal, fail closed), so a project that
//     publishes no list is served no routed call at all and the fact would
//     send the run to a refusal. An unread policy (pol nil) is the same answer
//     for the same reason.
//
// A repository whose config names no provider is not a hole: the set is empty,
// every credential of it is held by construction, and the fact then says the
// service serves this repository's provider keys while there is no provider
// call to send — which changes nothing a run does and is the honest reading of
// "every provider credential the repository's config names".
func credentialsProviderEntries(lc *localcfg.Config, pol *projectpolicy.Policy, held map[string]bool, repo, provider string) bool {
	if pol == nil || len(pol.Budget.AllowedModels) == 0 {
		return false
	}
	rel, _, ok := installationServiceRelease(lc)
	if !ok || imagePredates(rel, providerEntriesSince) {
		return false
	}
	slug, err := task.Slug(provider, repo)
	if err != nil {
		return false
	}
	for name := range runner.ProvidersFor(lc.Providers, repo) {
		key, err := credsvc.RepoCredentialKey(slug, name)
		if err != nil || !held[key] {
			return false
		}
	}
	return true
}

// credentialsStageAccount is R12's second fact for this repository: the mixed
// variant's second account is selected by the service like any named account,
// so the runner holds no token for it (R9, F44; KS10 reads this one where the
// runner picks the account its stages go out on).
//
// Its three conditions are the switch's own — the caller writes the fact only
// where the record says the service serves this repository — plus:
//
//   - the record names a stage account at all: infra.MixedStageAccounts is the
//     set the repository's jobs are built from, and it is empty for every
//     repository that mixes nothing (a cascade repository, an oauth one, an
//     api-key one whose stages name no account), which is the whole of "there
//     is nothing here for the service to serve".
//   - the deployed service is at or after the release that serves one
//     (stageAccountServedSince): a service older than that refuses the stage
//     account's calls, and the runner would then hold no token for an account
//     it must use. "Not known" is the same answer as "older", on
//     credentialsProviderEntries' reasoning, and the direction is again the one
//     that changes nothing: the run keeps the token the job mounts.
func credentialsStageAccount(lc *localcfg.Config, cfg *config.Config, repo string) bool {
	if len(infra.MixedStageAccounts(cfg, lc, repo)) == 0 {
		return false
	}
	rel, _, ok := installationServiceRelease(lc)
	return ok && !imagePredates(rel, stageAccountServedSince)
}

// credentialsBundleReady is whether the bundle holds at least one MODEL
// credential: the readiness half of the switch as the installation door's
// backfill reads it (backfillCredentialsServiceReady), for the entries whose
// auth that door cannot know.
//
// It is the question every reader asked before B2.3 and it is still the right
// one there: the backfill repairs records for roots an EARLIER release applied
// with the switch on, and those applies are all cascade ones — the switch
// admitted nothing else — so "holds a model credential" is exactly what their
// grants were made behind. The repository door, which has the repository's own
// fugaro.yaml, asks the sharper question (credentialsBundleServes).
func (r *initRun) credentialsBundleReady(ctx context.Context, lc *localcfg.Config) (ready, record bool) {
	names, record := r.readCredentialsBundle(ctx, lc)
	if !record {
		return false, false
	}
	return len(names) > 0, true
}

// credentialsBundleServes is whether the bundle holds what a run of THIS
// repository needs the service to hand it: the credential its agent.auth calls
// for, and the git credential its provider needs to open at all. It is the
// readiness half of the switch (infra.CredentialsServiceServes) for the
// repository door, which knows both.
//
// The auth's own credential, and not "some model credential" (B2.3):
//
//   - the cascade may settle on any model credential the bundle holds, so one
//     of them is enough — the run has nothing of its own to fall back on
//     (D143).
//   - `api-key` goes out on the project's Anthropic key, which the bundle
//     serves under config.AnthropicSecret: a bundle holding only an account's
//     token would serve an `api-key` repository nothing, and its job would
//     still mount the key it was told to stop using.
//   - `oauth` goes out on EVERY account its runs may be launched on, which is
//     infra.WorkflowAccounts' answer for this repository — the accounts its
//     allow-list admits, which is exactly the list the record this same run
//     writes names (repoAuthRecord). All of them and not the one its
//     fugaro.yaml happens to name: a launch may name another allow-listed
//     account with `--token` (D2070 (a), the account being per workflow and
//     per run), and the service refuses a run that declares an account the
//     bundle holds no credential under — with no fallback to the token the
//     job still mounts (D143), so a run that went out on its own mount before
//     this release would halt instead. In the bundle an account IS its
//     credential's name, resolved through this machine's aliases first: the
//     bundle is keyed by the account a name stands for, and a name that is an
//     alias would otherwise be held off for an account the bundle does hold.
//
// The git credential is the second half, and it is why the URL is not inert
// (F29): a job handed the service's address opens its git provider KEYLESS
// (internal/gitprov/providers/providers.go), so a GitHub repository whose
// bundle holds no App key would lose the mint it needs to push and comment at
// all — its job keeps the App key's mount, and reading the key itself is what
// the address stops it doing. A Bitbucket repository needs nothing here: its
// job reads its own token from its own mount, whatever the address says, and
// the proxied path (B1.8, the node's own fact) is the one that takes the token
// away.
//
// auth is read from the repository's own document and never from the record,
// exactly as the switch reads it (CredentialsServiceServes), and accounts is
// infra.WorkflowAccounts' answer for this repository — the accounts its jobs
// exist for and its record names, so a launch on any of them is a run this
// switch has to be able to serve. An auth the switch does not serve answers
// false, and so does `vertex`: no release hands those a model credential.
func (r *initRun) credentialsBundleServes(ctx context.Context, lc *localcfg.Config, auth string, accounts []string, provider string) (ready, record bool) {
	names, record := r.readCredentialsBundle(ctx, lc)
	if !record {
		return false, false
	}
	switch auth {
	case config.AuthCascade:
		ready = len(names) > 0
	case config.AuthAPIKey:
		ready = names[config.AnthropicSecret]
	case config.AuthOAuth:
		// Every account and not any one of them: the record this run writes
		// names them all, so the service would accept a run on the ones the
		// bundle cannot serve — and hand it nothing where it holds its own
		// mounted token (D143). An empty set answers no, which is the one
		// answer a repository whose jobs name no account deserves.
		ready = len(accounts) > 0
		for _, account := range accounts {
			if resolved, _ := lc.ResolveAccount(account); resolved != "" {
				account = resolved
			}
			if !names[account] {
				ready = false
				break
			}
		}
	}
	if ready && provider == gitprov.KindGitHub {
		ready = r.credsAppKey
	}
	return ready, true
}

// makeOnboardingRepoEntry makes the entry of the repository this run is about
// to onboard, before the first stage plans.
//
// The credentials service's job table is composed from the local config by both
// of its writers — the deploy (infra.Credentials) and the refresh every
// installation apply makes (infra.RefreshCredentialsServiceJobAccounts) — and
// the stage that applies the installation root is the third, while a repository
// joins the config in the repository stage, which is the ninth. So without this
// the run that onboards a `cascade` repository applies its root with the switch
// on — mounts dropped, roles/run.invoker granted — into a service whose table
// names no job of it, and every run of that repository then halts at the
// service's door: internal/credsvc admits the callers its table holds and
// refuses the rest, and a `cascade` run has no credential of its own to fall
// back to (D143). The table carries the row in the same run the grant is made
// exactly when the entry is here before that run's installation apply plans.
//
// It carries the provider and the workflows, and no more: those are the two
// fields a row is derived from (CredentialsJobAccounts reads one row per
// workflow of the entry, so an entry with none is a repository whose jobs the
// service refuses just the same), and both are what the checkout's default
// branch states. The rest of the entry — the base branch, the app id, the
// record itself — is the repository door's, which is the one that knows the
// plan it was built with; it writes over this shell in the same run.
//
// It is made only where the gate has authorised the onboarding: a repository
// the user declined, and one this run cannot ask about at all (a coding agent's
// session, a pipe, --json), is left alone. A name the config already lists at
// another host is left alone too: that entry belongs to the other provider's
// repository, and infra.Repo refuses the disagreement rather than switching it
// (spec.go's provider check).
//
// A run that authorises the onboarding and then stops before the repository
// stage leaves the row behind, and that is the direction this table fails in:
// the refresh UNIONS the derived rows with the document's, so an extra row
// binds an account no token is ever minted for, while a missing one is a job
// that holds the invoker grant and is refused.
func (e *initEngine) makeOnboardingRepoEntry() {
	if e.lc == nil || e.repo == nil {
		return
	}
	tg := e.repo.tg
	if tg == nil || tg.cfg == nil || repoNameListed(e.lc, tg.origin) {
		return
	}
	if a, err := e.authState(tg.origin); err != nil || a != authOK {
		return
	}
	if e.lc.Repos == nil {
		e.lc.Repos = map[string]localcfg.Repo{}
	}
	e.lc.Repos[repoConfigKey(e.lc, tg.repo)] = localcfg.Repo{
		Provider:  tg.cfg.Git.Provider,
		Workflows: slices.Sorted(maps.Keys(tg.cfg.Workflows)),
	}
}

// recordCredentialsServiceReady records, on the ONE repository entry whose root
// this run applies, the switch that root's plan was built with
// (docs/credentials-service/default-on.md, DON8's record, amended by the review
// of 2026-11-07).
//
// The value is the SWITCH and not the bundle reading alone: the auth that
// repository's runs are judged by is folded in, through the one predicate
// (infra.CredentialsServiceServes), so a repository that is served and a
// repository whose root was applied with the switch OFF are two different
// records, which bare bundle readiness could not tell apart. The one state it
// closes is the natural adoption order — an owner fills the bundle, runs
// `fugaro update --repo` while the checkout still says `agent.auth: oauth` (the
// root is planned held off: mounts kept, no roles/run.invoker), and only then
// commits the cascade. Under bare readiness that run recorded true, and the
// next launch, reading the cascade live and the record as "ready", handed the
// job the service's address while its account held no grant to knock with: the
// provider then opens keyless for a mint Cloud Run refuses, and a `cascade` run
// has no credential of its own to fall back to (D143). Folded in, the record
// says what happened — this root's last apply was held off — the launch
// withholds the address, the job keeps the mounts that apply kept, and the run
// after the cascade is committed is the one that applies the root with the
// switch on and records true.
//
// It is the repository door's (repoEngine, init.go), and it runs before the
// repository root's spec is built, so the plan that root makes and the record
// the launch reads are the same answer. Without this the record lags the plan
// by one run — the plan of a held-off root keeps the job's model mounts and
// grants its account no roles/run.invoker, while the record written afterwards
// tells the next launch the service is ready, which is the same broken state.
//
// One entry and not every entry the local config holds, which is what this
// helper did first: the record is read as "this repository's root has been
// applied with the service on", and the ONLY thing that makes the service
// usable to a repository is the roles/run.invoker grant its own root's apply
// makes (modules/workflow/sa.tf's google_cloud_run_v2_service_iam_member.
// credentials; the installation root grants run.invoker to operators, the
// coordinator and the verifier and to no job account). Recording readiness for
// a repository whose root this run does not apply is therefore a record of
// something that did not happen: its launch would be handed the service's
// address while its job accounts hold no grant, and a `cascade` run has no
// credential of its own to fall back to, so it halts at the service's door
// (D143). The installation door (install, init.go) applies no repository root
// at all and so writes no record — which is also why a run outside a checkout,
// whose repository stage is skipped, leaves every entry as it was.
//
// A repository the local config does not hold yet gets its entry from this
// function, provider and nothing else, and that is the onboarding's one-run
// rule (the review of 2026-11-07's fourth round): the spec below reads the
// entry, so an onboarding that created it only in the write after the apply
// deployed a held-off root on its first `fugaro update --repo` — mounts kept,
// no invoker grant — and the next launch, reading the record that write left
// (false), withheld the address too, leaving a `cascade` run with no model
// credential and nothing to swap it for until a second, unannounced update
// in the same checkout. The entry is created here, before infra.Repo reads
// it, so the plan and the record are the same answer on the run that onboards
// — which is what they are on every later run.
//
// The value is written false as well as true: a bundle that is later emptied —
// or a checkout edited back to an auth the service does not serve — switches
// the repository off again on the next run, and that is the fail-safe direction
// (see writeRepoConfig). Recorded and not merely "on": since the review's
// fourth round the field tells "no record" from a recorded false
// (localcfg.Repo.CredentialsServiceReady is a pointer, and
// localcfg.MergeShared carries a published record over a missing one), so this
// write is what lets a switch-off travel to the other machines — and the
// nothing-written case below is what keeps a machine that read nothing from
// erasing theirs.
//
// auth is the repository's agent.auth as the RUN will read it, which is the
// document the spec below is built from and not this working tree's
// (deploymentConfig, repoEngine): the record has to be the answer the plan was
// made with, and a record judged on a working tree the runs never see is the
// same disagreement one edit away. accounts is infra.WorkflowAccounts' answer
// for this repository — the accounts its jobs go out on, which is the same
// list the record this run goes on to write names (repoAuthRecord), so the
// bundle has to hold every one of them and not the one the document happens to
// name: a launch may declare any of them (`--token`, D2070 (a)). mode is the
// node's own budget mode as B1.8b reads it: the bundle reading of an `api-key`
// or `oauth` repository is the credential its auth calls for, and the switch
// itself turns on the mode (D2071).
//
// Nothing is written where the bundle was not read (credentialsBundleServes's
// second answer): in a coding agent's session or in a run's own process, whose
// bundle is a secret they may not open, and on a read that FAILED, whose error
// is an answer about this machine and not about the bundle — the record travels
// in the published config, so a false written from one would switch the service
// off for every machine of the installation. This repository keeps the record
// the last reading left on it either way.
//
// This door writes R12's two facts as well, on the same entry and in the same
// write (KS19, F31): "this repository's provider keys are served" and "this
// repository's stage account is served", each judged from the reading this
// function already made and recorded beside the switch, so the repository
// root's plan hands the fact to its jobs from one value rather than judging it
// again (infra's repoCtx reads the record; a second judgement there could not
// be made at all — internal/infra reads no cloud — and a second judgement here
// would be a second reading of the bundle for a question about the one already
// read). cfg is the repository's own document (the one the spec beside this is
// built from, repoEngine's cfg), which is what decides whether the repository
// mixes a stage account at all, and pol is the published node this door already
// read for the budget mode — R12's third condition is that the project
// publishes an allow-list, and the node is the only place that answer is.
//
// outs is the installation root's own outputs, and it is TWO things here.
// It is the first condition of the switch, read from the same source the spec
// beside this reads it from (infra.Inputs.Installation, which withDefaults
// never fills from the local config), so a record cannot say on for a root the
// spec held off. The two part company where the local config is the stale one:
// an owner who takes the service out leaves a config still carrying the block
// and its URL (docs/credentials-service/runbook.md §9), and a `fugaro update
// --repo` in a checkout then reads outputs with no service at all while
// planning that repository's root held off. The bundle's reading alone would
// record true there, and the next launch — which reads the config's block —
// would hand the job the address of a service that no longer exists: mounts
// kept, no roles/run.invoker, and a provider opened keyless for a mint nothing
// answers (D143).
//
// And it is the fallback for the bundle read: on the run that deploys the
// service the config is written by the very step this precedes, so the outputs
// are the only place the service's existence is known. Only the block's
// PRESENCE is read for that — the bundle's container is derived from the
// project and the installation's name (openBundleStore) — so a URL the first
// apply could not know yet is not a reason to answer false.
func (r *initRun) recordCredentialsServiceReady(ctx context.Context, lc *localcfg.Config, outs infra.InstallationOutputs, cfg *config.Config, pol *projectpolicy.Policy, repo, provider, auth string, accounts []string, mode string) {
	if lc == nil {
		return
	}
	key := repoConfigKey(lc, repo)
	entry, ok := lc.Repos[key]
	if !ok {
		// A repository the local config does not hold gets its entry HERE,
		// before the spec below reads it, and not only in the write after the
		// apply (writeRepoConfig, init.go): the spec is built from this
		// value, so a first onboarding whose entry is created afterwards is
		// deployed with the switch off — mounts kept, no invoker grant — and
		// the cascade the owner just committed keeps no address until a
		// SECOND `fugaro update --repo` in the same checkout, which the run
		// neither asks for nor announces. `fugaro update --repo` is the
		// documented onboarding, and CLAUDE.md's HARD RULE is that it is the
		// whole upgrade: one run, the same one main ran.
		//
		// The entry is the shell the write after the apply fills in (provider
		// and nothing else), and it is this map's only use of it that matters
		// before then: infra.Repo reads the KEY (via RepoFor) and, from the
		// rest, the provider — which the checkout's own fugaro.yaml has just
		// been held to (repoEngine resolves the config first) — and the base
		// branch the repository's own file sets. Everything else the spec
		// reads comes from that document and not from repos:.
		entry = localcfg.Repo{Provider: provider}
	}
	installed := outs.CredentialsService != nil
	ready, record := false, true
	if installed {
		// The bundle is read only where the installation HAS the service: the
		// container is the installation's, so a read on an installation
		// without one would be a cloud call about a resource that does not
		// exist — and its answer could not move the record anyway, the plan
		// this run applies being held off by the same outputs.
		//
		// What it is read FOR is this repository's own run: the credential its
		// auth calls for and the git credential its provider needs
		// (credentialsBundleServes). A bundle that holds the whole cascade and
		// no App key serves a GitHub repository nothing it can push with, and
		// one that holds only an account's token serves an `api-key`
		// repository nothing it was told to stop mounting.
		ready, record = r.credentialsBundleServes(ctx, credentialsServiceProbe(lc, outs), auth, accounts, provider)
	}
	if !record {
		// Nowhere the bundle was not read — a coding agent's session, a run's
		// own process, or a read that failed — is there a reading to write, so
		// the entry keeps the record the person's last run left on it. A failed
		// read is the one that reaches an operator's own run, and the write it
		// would make is the installation-wide one the doc above refuses.
		return
	}
	// The one predicate, not a second copy of its conjunction: installed is
	// read here exactly as the spec reads it, ready is this run's reading of
	// the bundle — which the probe answers false for, without a read, when no
	// block is known — and mode is the node's own, the one the spec reads out
	// of Inputs.Policy for the `api-key` and `oauth` arm (D2071). So the record
	// cannot come out differently from the plan it is the record of.
	serves := infra.CredentialsServiceServes(installed, ready, auth, mode)
	entry.CredentialsServiceReady = localcfg.RecordSwitch(serves)
	// R12's two facts, recorded beside the switch and read from the same
	// reading (KS19, F31, F44): the repository's provider keys are served, and
	// its stage account is served. Each is written as the reading's answer and
	// never as "the switch is on" alone — the switch says a run may reach the
	// service, while a fact says the service has what that run would ask it for
	// — and each is hung off the switch because a fact is planned into a job
	// that can only be reached where the switch is on (ProviderKeysServed's own
	// comment, infra's repoCtx).
	//
	// They are written false as well as true, exactly as the switch is: the
	// conditions move in both directions — a provider is added to the config
	// and its key has not been copied into the bundle yet, the bundle is
	// emptied, the project stops publishing an allow-list, a stage account is
	// removed — and the next `fugaro update` has to be able to take the fact
	// away again, or a run would keep sending calls to a service that refuses
	// them while its job still mounts the key that works.
	entry.ProviderKeysServed = localcfg.RecordSwitch(serves && credentialsProviderEntries(lc, pol, r.credsRepoCredentials, repo, provider))
	entry.StageAccountServed = localcfg.RecordSwitch(serves && credentialsStageAccount(lc, cfg, repo))
	if lc.Repos == nil {
		lc.Repos = map[string]localcfg.Repo{}
	}
	lc.Repos[key] = entry
}

// credentialsServiceProbe is the config the bundle reading is made over:
// lc itself, with the installation's block taken from its own outputs when the
// local config does not carry one yet. That is the run which deploys the
// service — the config is written by a step after the one that reads the
// bundle — and the outputs are the only place the service's existence is known
// there. A nil block leaves the probe as it is: with no service anywhere the
// answer is false without a read, which is what the reading says of a config
// that has no block.
func credentialsServiceProbe(lc *localcfg.Config, outs infra.InstallationOutputs) *localcfg.Config {
	if lc == nil || lc.CredentialsService != nil {
		return lc
	}
	block := credentialsServiceBlock(outs.CredentialsService)
	if block == nil {
		return lc
	}
	withBlock := *lc
	withBlock.CredentialsService = block
	return &withBlock
}

// backfillCredentialsServiceReady records the switch on every repository entry
// the installation's own state answers for — by any machine, from any checkout
// — whether the entry carries no record yet, one left by an earlier apply, or
// one the root's last apply has outlived.
//
// It is the upgrade path of docs/credentials-service/default-on.md's DON1, and
// it exists because the record is new in this release: an entry written before
// it carries none, so an installation that opted in under an earlier release is
// indistinguishable from one that never did. Such an installation's roots were
// applied with the service on — their jobs mount no model credential and their
// accounts hold roles/run.invoker — and this release's launcher, which reads
// the record as the switch's second condition, would withhold the service's
// address from a `cascade` run of them: a job with no model mount and no
// address halts at the service's door (D143).
//
// The installation door is where it belongs, and not the repository door,
// because the record has to reach the roots this door does NOT apply: the
// repository stage applies the checkout's own root and writes that entry from
// the plan it builds, while the siblings of the installation are roots the
// next `fugaro update` in THIS checkout never re-applies. Without the backfill
// the upgrade converges one repository per checkout it is run in, which is an
// operator step beside `fugaro update` — and CLAUDE.md's HARD RULE is that
// `fugaro update` is the whole upgrade.
//
// The evidence is the SHARED root's own state, never a guess and never this
// machine's copy of it (appliedRepoCredentialsService, over
// infra.RepoCredentialsServiceApplied): the objects under the repository's
// prefix in the installation's state bucket carry the invoker grant exactly
// when that root's apply made it. It reads the state and not the tfvars a
// workdir holds — which is what the review of 2026-11-07's fourth round found
// — because those are per-machine (<state>/fugaro/terraform/<project>/repos/
// <slug>) and the question is the installation's: a repository whose root was
// applied from another machine, or from another checkout of this one, has no
// document here, so a workdir read would report "never applied" for a root
// that is applied — and the upgrade would converge one checkout at a time, the
// operator step this function exists to remove. The state is also the honest
// half of the pair: the tfvars are what a run HANDED Terraform, so an apply
// that failed leaves a document ahead of the state. That read is made for
// EVERY entry the config holds, whatever record it carries (the loop below says
// why), and it is the only thing that repairs a record the root's last apply
// has outlived: the record is published whole and a local one wins the union
// (localcfg's mergeRepoRecords), so a machine whose own apply is older keeps
// writing its answer over the one the machine that applied last published, in
// both directions — a stale false withholds the address from a repository whose
// jobs have no model mounts left, and a stale true hands an address to a job
// whose account holds no roles/run.invoker grant. Where the state shows no
// grant a record that says on is written `false`; an entry with no record and
// no grant keeps its absence, since nothing has judged that root and a written
// false would claim an apply that ran with the switch off. Where the state
// shows the grant the record is written as this run's bundle reading — the same
// value the repository door writes, and the bundle reading alone: this door
// fills the roots no checkout here applies, so it has neither the
// installation's outputs nor their files to fold in, and does not need them,
// the grant that brought a key here being one an apply made with the switch on,
// while both readers judge those two conditions again, live, on the run that
// reads the record (localcfg.Repo.CredentialsServiceReady).
//
// The reading is the SHARP one wherever this installation records what the
// repository's runs go out on (recordedRepoAuth, creds.ReadRepoAuth): the weak
// question — "the bundle holds a model credential" — is right only for the
// cascade, which settles on any of them, and the grant in the state does not
// say which auth the apply that made it was for. The switch admits `api-key`
// and `oauth` as well (infra.CredentialsServiceServes), and config.
// ServiceServedAuths has served those two since 0.26.0, so an installation
// whose repository is on one of them and whose bundle holds no credential for
// it is a repository this door would otherwise record ON: its jobs' model
// mounts are dropped, its launches are handed the service's address, and every
// model call of it is refused while the bundle cannot serve the one credential
// the repository's own record names (the owner's installation of 2026-11-24).
// Recorded off, the fail-safe direction holds: the next `fugaro update` in that
// repository's own checkout puts its jobs' mounts back and stops handing them
// the address, which is the one command CLAUDE.md's HARD RULE promises. The
// weak question stays for the entries this door cannot judge — no database, no
// record, a read that failed — exactly as it was before the record was read
// here at all.
//
// Nothing is written where the bundle was not read (credentialsBundleReady's
// second answer): in a coding agent's session or in a run's own process, and on
// a read that failed. Every entry keeps the record the person's last run left
// on it, and the convergence happens on that person's next `fugaro update` —
// which is still the one command CLAUDE.md's HARD RULE promises. A failed read
// costs this door a convergence rather than a wrong write: an entry it cannot
// judge keeps the record it had, so an installation whose opt-in predates this
// release converges on the next run that CAN read the bundle — which is the
// same door, the next time it works, and never a second command. A state it
// cannot read is the same shape, one repository at a time.
//
// Both doors that apply the installation root and write the local config call
// it: install (init.go), and initFirebase (init_firebase.go), because
// `fugaro update --firebase-project <id>` runs no installation stage and
// applies that root itself — a door that skipped it would leave this upgrade's
// record to the next plain update, which is the second command the rule above
// forbids.
func (r *initRun) backfillCredentialsServiceReady(ctx context.Context, c *infra.Clients, stateBucket string, lc *localcfg.Config, outs infra.InstallationOutputs) {
	if lc == nil || len(lc.Repos) == 0 {
		return
	}
	var switched, heldOff []string
	for key := range lc.Repos {
		// Every entry is read against the installation's own state, whatever
		// record it holds, because the record is this machine's and the state is
		// the installation's: a record carries no stamp, so one left by an
		// EARLIER apply of that root — on another machine, or from another
		// checkout of this one — outranks the answer that root's last apply
		// published for every reader of the published file (localcfg's
		// mergeRepoRecords is where that happens), and no other door would ever
		// correct it. A recorded false is that case in one direction: the apply
		// that made the grant can be NEWER than the record, made on another
		// machine from a bundle filled since, and the false then withholds the
		// address from a repository whose jobs have no model mounts left
		// (D143). A recorded true is the same case the other way: a root
		// re-applied held off — the owner switching the repository back, or an
		// auth the service does not serve — puts the jobs' mounts back and takes
		// the roles/run.invoker grant away, and the stale true hands those jobs
		// an address their accounts may not knock with, which fails every run of
		// them at bootstrap. It costs one state read per entry per update, which
		// is the price of a repair `fugaro update` alone can make: the
		// alternative is a record no door can correct.
		on, judged, err := appliedRepoCredentialsService(ctx, c, stateBucket, lc, key)
		if err != nil {
			r.warn(fmt.Sprintf("whether %s's repository root carries the credentials service could not be read (%s): its record is left as it is, and a run of it may not be handed the service's address until the next update reads the state", key, oneLineCLI(err.Error())))
			continue
		}
		if !judged {
			// Nowhere to read the answer from — no state bucket, or a
			// repository this config cannot name a root for — is not the answer
			// "no grant": the entry keeps the record it has, as it does for a
			// read that failed.
			continue
		}
		if on {
			switched = append(switched, key)
			continue
		}
		if lc.Repos[key].CredentialsServiceOn() {
			heldOff = append(heldOff, key)
		}
	}
	if len(switched) == 0 && len(heldOff) == 0 {
		return
	}
	probe := credentialsServiceProbe(lc, outs)
	weak, record := r.credentialsBundleReady(ctx, probe)
	if !record {
		// Nowhere the bundle was not read — a coding agent's session, a run's
		// own process, or a read that failed — is there a reading to write, so
		// every entry keeps the record the person's last run left on it and the
		// upgrade converges on that person's next `fugaro update`. The entries
		// this door visits are the ones the installation's state disagrees with,
		// and the values it would write are this run's own reading and a plain
		// off, so a failed read costs it a convergence and cannot clear a record
		// someone else wrote.
		return
	}
	// This door writes the switch alone and leaves R12's two facts exactly as
	// the person's last `fugaro update --repo` recorded them (KS19). It fills
	// the roots no checkout here applies, so it has neither the bundle
	// document's entries, nor the published policy's allow-list, nor a
	// repository's own document to judge a fact from: a value written from a
	// readiness answer would be a guess the repository door then contradicts,
	// and one that took a recorded true away would clear a fact for a
	// repository this run never looked at. A stale fact beside a switch this
	// loop has just written `false` is inert rather than wrong — the spec plans
	// each fact only where the same record's switch is on (infra's repoCtx) —
	// and the next `fugaro update --repo` in that repository writes both again
	// from its own reading.
	// The database the sharp question is read from, opened once for the entries
	// below: an installation with no budget backend has none, and every entry
	// then keeps the weak answer, which is what this door wrote before the
	// record was read here.
	var db *rtdb.Client
	if len(switched) > 0 {
		db = repoAuthDB(ctx, lc)
	}
	for _, key := range switched {
		entry := lc.Repos[key]
		ready := weak
		if auth, accounts, ok := recordedRepoAuth(ctx, db, entry, key); ok {
			ready, _ = r.credentialsBundleServes(ctx, probe, auth, accounts, entry.Provider)
		}
		entry.CredentialsServiceReady = localcfg.RecordSwitch(ready)
		lc.Repos[key] = entry
	}
	for _, key := range heldOff {
		// Off whatever this run's bundle says: the state carries no grant, so
		// the service refuses this repository's accounts whatever the bundle
		// holds, and its jobs kept (or got back) the mounts that make the
		// service unnecessary to them.
		entry := lc.Repos[key]
		entry.CredentialsServiceReady = localcfg.RecordSwitch(false)
		lc.Repos[key] = entry
	}
}

// repoAuthDB is this installation's budget database, or nil where its config
// names none or the client cannot be opened: the record the sharp question is
// judged from lives there (recordedRepoAuth), and a door that cannot read it
// keeps the weak answer rather than failing the upgrade over a diagnosis.
func repoAuthDB(ctx context.Context, lc *localcfg.Config) *rtdb.Client {
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil
	}
	return db
}

// recordedRepoAuth is what this installation records as one repository's own
// auth and the accounts its runs may go out on (creds.ReadRepoAuth): the record
// the repository door writes from the checkout's fugaro.yaml and the credentials
// service narrows every call of that repository by, so it is the one answer to
// "which auth" a door that has no checkout can reach.
//
// The second answer is false wherever there is nothing to read — no database, a
// repository this config cannot name a slug for, a node the reader refuses — and
// the caller then asks the weak question, which is the answer it had before this
// reading existed.
func recordedRepoAuth(ctx context.Context, db *rtdb.Client, entry localcfg.Repo, repo string) (string, []string, bool) {
	if db == nil {
		return "", nil, false
	}
	slug, err := task.Slug(entry.Provider, repo)
	if err != nil {
		return "", nil, false
	}
	rec, found, err := creds.ReadRepoAuth(ctx, db, slug)
	if err != nil || !found {
		return "", nil, false
	}
	return rec.Auth, rec.Accounts, true
}

// appliedRepoCredentialsService reports whether the repository root has been
// applied with the installation's credentials service on, read from the
// installation's own copy of that root's state
// (infra.RepoCredentialsServiceApplied: the objects under the repository's
// prefix in the state bucket, whichever machine applied them).
//
// It is the upgrade backfill's reader
// (backfillCredentialsServiceReady), and it is deliberately not
// deployedRepoCredentialsService beside it: that one reads the tfvars a
// workdir holds, which are THIS machine's copy of what a run handed Terraform.
// The question here is the installation's — was that root applied with the
// service on, by anyone — so the answer has to come from the installation's
// own state (the review of 2026-11-07's fourth round).
//
// A repository the local config does not hold, one it cannot name a slug for,
// and a run with no state bucket to read all answer false, and none of them
// JUDGES the root: the second answer is what keeps the caller from writing "no
// grant" where it could not ask, which is the difference between a repository
// whose root was applied with the service off and one whose state this run
// never read. A state that holds no object at all for that root IS judged, and
// answers false: the bucket is the installation's own, so nothing of this
// installation applied that root with the service on.
func appliedRepoCredentialsService(ctx context.Context, c *infra.Clients, stateBucket string, lc *localcfg.Config, repo string) (on, judged bool, err error) {
	if lc == nil || c == nil || stateBucket == "" {
		return false, false, nil
	}
	entry, ok := lc.RepoFor(repo)
	if !ok {
		return false, false, nil
	}
	slug, err := task.Slug(entry.Provider, repo)
	if err != nil {
		// A repository whose provider this config cannot name has no state
		// prefix to have written the root's state under: the slug is what
		// names it (repoEngine's own derivation).
		return false, false, nil
	}
	on, err = infra.RepoCredentialsServiceApplied(ctx, c, stateBucket, slug)
	if err != nil {
		return false, true, err
	}
	return on, true, nil
}

// deployedRepoCredentialsService reports whether the repository root this
// machine last applied carries the installation's credentials service: the
// tfvars the repository door wrote into the repository's own workdir
// (infra.RepoWorkdir, ReadWorkdirVars, infra.RepoCredentialsServiceDeployed).
//
// It answers the one question neither the local config nor the repository's
// fugaro.yaml can, and it has ONE reader: a launch that cannot read the run's
// own document at all — `fugaro run --repo X` from a directory that holds no
// checkout of X, so there is no file to read and no ref to read one out of
// (run.go's launchFileConfig) — which has no agent.auth to judge the switch's
// third condition on. The root's own apply already made that decision: it
// dropped the job's model mounts and granted its account roles/run.invoker
// exactly when this key is there, and a job is handed the service's address to
// match the containers it runs (run.go's credentialsServiceLaunchEnv call
// site).
//
// The upgrade backfill used to read this too, and does not any more: it asks
// about roots another machine or another checkout applied, and those wrote no
// document into THIS machine's workdir (appliedRepoCredentialsService, over
// the installation's own state). A launch keeps the workdir read because it is
// the one this machine can afford: the state bucket is the operator's — it is
// created so that project Viewers cannot read it — and a developer launching a
// run holds no grant on it.
//
// A repository the local config does not hold, one it cannot name a slug for,
// and one whose root was applied with the service off (the key null, which is
// what repoCredentialsService writes for every other answer) all answer false,
// and so does a repository this machine holds no tfvars for: "no document" and
// "a document that says off" are deliberately the same answer, because neither
// is evidence that a job's account may call the service. A document that is
// there and cannot be read is the error, which no caller may read as a yes.
//
// The residual it leaves: the tfvars are what the run handed Terraform, not
// the state, so an apply that failed or was refused leaves a document ahead of
// the state — a root whose document says "on" while the job kept its mounts
// would be handed an address it cannot use. The authoritative read is the
// Cloud Run service's IAM policy (doctor_brokering.go's
// doctorCredentialsServiceDeployed), which costs a permission the operator
// holds by role and a developer launching a run need not; the backfill reads
// the state, which is the same fact one step earlier (the apply's own
// addresses) at a permission this machine already spends on every repository
// root it plans.
func deployedRepoCredentialsService(lc *localcfg.Config, repo string) (bool, error) {
	if lc == nil || lc.GCPProject == "" {
		return false, nil
	}
	entry, ok := lc.RepoFor(repo)
	if !ok {
		return false, nil
	}
	slug, err := task.Slug(entry.Provider, repo)
	if err != nil {
		// A repository whose provider this config cannot name has no
		// repository root's workdir to have written a document into: the
		// slug is what names it (repoEngine's own derivation).
		return false, nil
	}
	dir, err := infra.RepoWorkdir(os.Getenv, lc.GCPProject, slug)
	if err != nil {
		return false, nil
	}
	vars, err := infra.ReadWorkdirVars(dir, "repo")
	if err != nil {
		return false, err
	}
	return infra.RepoCredentialsServiceDeployed(vars)
}

// deploymentConfig is the checkout's resolved config as this run DEPLOYS the
// repository root from: the same document with agent.auth taken from the
// fugaro.yaml at the default branch — the file the runs of this repository
// read — where the working tree would answer differently.
//
// The switch (infra.CredentialsServiceServes) is judged on agent.auth, so a
// plan that reads it from another document than the launch does is a job
// deployed for one auth and launched for another. `fugaro update --repo` reads
// the working tree, which an uncommitted edit or a checkout parked on another
// branch can leave saying oauth over a default branch that says cascade; the
// launcher reads the file at the ref the run checks out (internal/cli/run.go's
// launchFileConfig). Worktree oauth over a cascade default branch plans a root
// that keeps the job's model mounts and grants its account no
// roles/run.invoker, and the launch hands that job the service's address
// anyway, which is the halt with no fallback D143 is about. The other
// direction drops the mount and withholds the address, which leaves the job
// with no model credential at all. The two answers have to come from one
// document, and the document is the default branch's: the repository stage
// already holds the working tree to it, byte for byte (init_repo_target.go's
// readFugaroYAML comparison, "the engine reads the working tree: it must be
// the default branch's file"), and it is the ref a run is cut from unless the
// checkout is parked on another branch origin has (run_ref.go's newRunRef). A
// run cut with --ref from a third branch whose own fugaro.yaml says something
// else again is the case this cannot reach: no update can know which branch a
// future run is cut from, and the default branch is the ref a plan can be held
// to.
//
// It is agent.auth alone that moves, and it moves for the whole deployment and
// not only for the switch: agent.auth is also what decides which model
// credential a job mounts and whether it is a Vertex one (infra's SecretMounts
// and WorkflowAccounts), so a deployment planned for one auth and launched for
// another is incoherent on those axes too. Nothing else is taken from the
// default branch — the workflows, the secrets and the base branch this engine
// deploys are the working tree's, as they have always been.
//
// A default branch this checkout cannot answer for — no remote-tracking ref,
// no file at it, a file that does not resolve, or no project layer to resolve
// it over — leaves the config exactly as it was, which is the fallback the
// launcher's own read takes (launchFileConfig). In the converge door the two
// documents are the same file anyway: the repository stage refuses a checkout
// whose fugaro.yaml is not the default branch's before this engine runs.
func deploymentConfig(ctx context.Context, root string, cfg *config.Config) *config.Config {
	if cfg == nil || root == "" {
		return cfg
	}
	db := defaultBranchParse(ctx, root)
	if db.cfg == nil || db.cfg.Agent.Auth == "" || db.cfg.Agent.Auth == cfg.Agent.Auth {
		return cfg
	}
	out := *cfg
	out.Agent.Auth = db.cfg.Agent.Auth
	return &out
}
