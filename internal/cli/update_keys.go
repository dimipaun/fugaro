package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"

	"google.golang.org/api/googleapi"
	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The copy of this installation's model keys into the credentials service's
// bundle: the stage KS12 adds to `fugaro update` (D3001, R4 of
// docs/keys-through-service).
//
// **What it is for.** Every model key an installation holds today lives in a
// container of its own — a provider's secret, the project's own Anthropic key,
// a repository's — and a run reaches it through a mount. The credentials
// service serves the same keys out of ONE bundle (C1), so a key that is not in
// that bundle is a key a released run is no longer served: the copy is what
// moves the installation onto the service without asking anybody to re-enter a
// value nobody can read back. It is the one exception to the rule that no value
// is ever copied between the two stores, and internal/credsvc/dynamicsecrets.go
// is where that rule is written down.
//
// **The rules it keeps, in the order it keeps them.** Only a project owner
// copies (D3006): KS2's own probe is asked before any value is read, and an
// operator's update is skipped with the sentence that names who can make the
// write. The value is read into memory and never printed, logged or carried in
// an error: every line here names an entry, a container or a version, and every
// value the stage holds is registered with the run's redaction while it is
// held. An entry the copy writes carries the container and the version it came
// from (the marker, D3014), which is what makes the copy idempotent and what
// tells a later run that the entry is the copy's own; an entry it did not write
// is never overwritten without saying so, one a container has moved past is
// copied again and named in one line, and one that already says what this run
// would write adds no version at all. It asks nothing: an operator who is not
// the project owner is told who to ask, and nobody is prompted for a value.
//
// **The container is the source and C1's name is the entry.** A pair of those
// two is what this file calls a copy: the container is read with this
// installation's own Secret Manager client, and the entry's name is built by
// C1's rule and never spelled here. One value several repositories share is one
// pair per repository (D3008): the slug is what makes them different entries,
// and no member of the family names two repositories.
//
// **What it enumerates is the two kinds of key the bundle's family holds**
// (copySources): a provider's key for each repository that may send code to that
// provider, and the key a repository holds as its own — its Anthropic key, under
// an entry carrying the repository. The project's own Anthropic key and every
// account's OAuth token are NOT enumerated and cannot be: their entries are
// plain scalars under their own names, written by the person who holds the
// value, so there is no container version to follow and nowhere to record a
// marker — and an entry the copy cannot mark is one it would never write, since
// it may not overwrite what it did not write (D3015, copyDecide).
//
// **The write goes through the guard every writer of the family goes through**
// (refuseAnEntryTheServiceCannotRead, F22): a document holding an entry the
// deployed credentials service cannot read is a document that service refuses
// WHOLE, halting every caller of it, coding runs included — so the copy is
// refused rather than landed where the service is too old, and this stage sits
// immediately after the one that deploys that service, which is what leaves the
// guard's answer a state the same run has already fixed.
//
// **A read that failed is not an absence** (R7): a container that is not there,
// or one holding no version this run can read, is a pair with nothing to copy
// and a line saying so; a read that came back an error is this stage's failure,
// because a copy that quietly skipped a key would leave a repository with
// neither a mount (release B drops them) nor an entry the service serves.
//
// **Nothing here is cached across the check, the plan and the apply, and that
// is deliberate.** The stages whose read is remembered (the route, the
// workspace) hold an answer about documents this run does not itself move; this
// one decides on the release of a service the stage immediately before it
// redeploys, and on a container the installation stage creates — so a read
// taken in the preview pass, before either has happened, is a read of another
// installation, and believing it would leave the copy owed on the very run that
// made it possible. The read is made again for each of the three, which is a
// document and a handful of containers against an update that takes minutes.

// keyCopy is one pair the copy works on: a container that holds a model key
// outside the bundle, and the bundle entry that value belongs under.
type keyCopy struct {
	// container is the container's LOGICAL name as this installation spells it
	// (config.AnthropicSecret, a provider's own name, a repository's): a name
	// and never a value, and the name the operator's own commands and `fugaro
	// secrets ls` speak. For a provider's key it is the name the repository's
	// own job mounts that key under, which is the provider's own `secret` — or
	// the derived name of the runner-path key credential that supersedes it
	// (infra.RunnerKeyCredential, D1670, MC23).
	container string
	// repoID and projectID are the two containers that logical name may be held
	// in, as Secret Manager names them: the repository's own, gcp.SecretID of
	// that repository's slug, and the project-wide copy gcp.ProjectSecretID
	// gives every declared name. A key has one home per scope, and the run's own
	// job mounts the narrower one: the repository's container while it holds an
	// enabled version, the project-wide copy of the same name otherwise
	// (infra.RepoSpec.MountSecretID, deploy/terraform/gcp/modules/repo/
	// workflows.tf's local.secrets, which resolves the project copy for exactly
	// the names whose repository copy has no version). A pair read from the
	// wider container while a narrower one holds a value would enter a key the
	// repository's own runs are not served — and release B drops the mount, so
	// nothing else would serve them it afterwards.
	//
	// The read is made of the ids and the marker carries the id of the container
	// that ANSWERED (credsvc.ProviderKeySource): a version number is a version of
	// one container, and the same logical name's two scopes number their
	// versions independently, so a marker naming the name alone is a version the
	// next run compares against another container's (copyDecide).
	repoID, projectID string
	// entry is the bundle key the value belongs under, as C1 builds it
	// (credsvc.RepoCredentialKey).
	entry string
	// baseURL and auth are the entry's two routing facts: where a routed call
	// goes and how the key is sent. They are the credential's own declaration
	// in the owner's config and never a caller's (F45, D1418), and the loader
	// holds an entry to the same two rules before it serves one.
	baseURL, auth string
	// repo and credential are the two halves of the entry's name as a person
	// reads them — the repository and the credential the value is entered for —
	// for the lines this stage prints. Neither is a value.
	repo, credential string
	// own marks the pair of a key the CONFIG does not declare: a repository's
	// own Anthropic key, which a repository has only where somebody stored one
	// of its own (copyRepositoryKeys). A container holding nothing is therefore
	// the ordinary state of this pair rather than a key this installation lost,
	// and the run does not say so (copyRow.reports): the line naming every
	// repository of an installation that never stored one would name the whole
	// installation, and a person reading it would go looking for a key nobody
	// ever had. The provider half is the other way round — its pair exists
	// because the owner declared that the repository sends code to that
	// provider, so a container holding nothing is a key a run of it is served
	// none of.
	own bool
}

// name is the pair as one of this stage's lines names it.
func (c keyCopy) name() string {
	if c.repo == "" {
		return pluginwire.Printable(c.credential)
	}
	return fmt.Sprintf("%s for %s", pluginwire.Printable(c.credential), pluginwire.Printable(c.repo))
}

// homes is the containers the pair's key may be held in, in the order the read
// takes them: the repository's own first, the wider one after it.
func (c keyCopy) homes() []string {
	if c.projectID == "" || c.projectID == c.repoID {
		return []string{c.repoID}
	}
	return []string{c.repoID, c.projectID}
}

// homeScope names one of those containers as this stage's lines say it. The two
// hold the same logical name, so the scope is what tells them apart — and the
// scope is the fact a person acts on, because it is the copy the repository's
// own job mounts. An id that is neither is named by the id itself, which is the
// marker of a container this pair no longer reads: a scope the other one has
// taken over since, or a key a runner-path key credential now holds.
func (c keyCopy) homeScope(id string) string {
	switch {
	case id == c.repoID && id == c.projectID:
		return safetext.Field(c.container)
	case id == c.repoID:
		return "the repository's own container " + safetext.Field(c.container)
	case id == c.projectID:
		return "the project-wide container " + safetext.Field(c.container)
	}
	return safetext.Field(id)
}

// copySources answers the pairs this run has to copy: for each model key the
// installation holds outside the credentials service's bundle, the container
// that holds it and the entry it belongs under — one pair per repository a
// shared key is entered for (D3008).
//
// It is the half of the copy that reads the installation's own config, and it
// is a variable so that the machinery the rest of this file is — the read, the
// marker, the decision, the guard and the write — can be put in front of pairs
// a test builds.
//
// It answers the two families of key this copy moves, and they are the whole of
// it: the provider keys a repository may send code to (copyProviderKeys) and the
// key a repository holds as its own (copyRepositoryKeys). What is deliberately
// NOT enumerated is anything whose bundle entry is a plain scalar: the project's
// own Anthropic key and every Claude account's OAuth token are entered under
// their own names by the person who holds them (`fugaro creds set`), so there is
// no marker to carry and no container version to follow — the copy could not
// tell its own entry from a person's, and an entry it may not overwrite is one
// it never writes (copyDecide, D3015).
var copySources = func(ctx context.Context, lc *localcfg.Config) ([]keyCopy, error) {
	providers, err := copyProviderKeys(lc)
	if err != nil {
		return nil, err
	}
	own, err := copyRepositoryKeys(lc)
	if err != nil {
		return nil, err
	}
	return append(providers, own...), nil
}

// copyProviderKeys is the provider-key half of the enumeration (KS12b): every
// provider credential a repository this installation serves may send code to,
// entered once for each such repository (D3008).
//
// Both halves of a pair are the owner's own config and nothing here is a
// literal. The container is the provider's declaration's own `secret` — the
// logical name this installation holds that key under, and the name `fugaro
// secrets ls` speaks — except where the installation declares a runner-path key
// credential of the provider's own name that admits this repository: that
// credential's derived container is what the repository's job mounts for this
// provider, and the provider's own `.secret` is superseded and mounted nowhere
// beside it (infra.SecretMounts, D1670, MC23). Either way the name has one
// container per scope this installation keeps (F8, F27): the repository's own,
// `gcp.SecretID` of that repository's slug, and the project-wide one
// `gcp.ProjectSecretID` gives every declared name the day the installation is
// applied. The pair names both, the repository's first,
// and the read takes them in that order (keyCopy.homes) because that is the
// precedence the run's own job mounts them with — so the entry holds the key
// the repository's runs are served, whichever home the owner stored it in. The
// entry is C1's name for that repository and that provider, built by the one
// function that builds it (providerKeyEntry, which derives the slug from the
// repository's git provider as every other writer of the family does), and its
// two routing facts are the provider's base_url and auth, which the loader
// holds an entry to and which are therefore already refused by the config's own
// validation before a pair is built.
//
// One pair per repository and not one per key: a key two repositories share is
// two pairs, written twice under each repository's entry (D3008) — and each
// container is read once whatever the number of pairs that name it
// (copyPlan.read, keyed by Secret Manager ID), so a shared key is read once and
// entered as many times as it is used.
//
// A repository the installation serves is an entry of the config's `repos`
// map: that map is where the slug C1's name carries comes from and it is the
// whole of the repositories a run of this installation is launched on. A
// provider the repository may not send code to (config.ModelProvider.AllowsData)
// is no pair at all: the key may not reach it, so no run of it is served one
// and no entry is written for it.
func copyProviderKeys(lc *localcfg.Config) ([]keyCopy, error) {
	var out []keyCopy
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		for _, name := range slices.Sorted(maps.Keys(lc.Providers)) {
			p := lc.Providers[name]
			if !p.AllowsData(repo) {
				continue
			}
			slug, err := providerKeyRepoSlug(lc, repo)
			if err != nil {
				return nil, err
			}
			entry, err := providerKeyEntry(lc, name, repo)
			if err != nil {
				return nil, err
			}
			// The container the repository's own job mounts this key under: the
			// provider's own `secret`, or — where the installation declares a
			// runner-path key credential of the provider's own name that admits
			// this repository — that credential's derived container, which
			// SUPERSEDES it (D1670, MC23). Asked through the rule the mount is
			// built from and not spelled again here, so the entry this copy
			// writes and the key a released run is served cannot disagree.
			logical := p.Secret
			if s, ok := infra.RunnerKeyCredential(lc, name, repo); ok {
				logical = s
			}
			out = append(out, keyCopy{
				container:  logical,
				repoID:     gcp.SecretID(slug, logical),
				projectID:  gcp.ProjectSecretID(lc.Name, logical),
				entry:      entry,
				baseURL:    p.BaseURL,
				auth:       p.Auth,
				repo:       repo,
				credential: name,
			})
		}
	}
	return out, nil
}

// anthropicAPIURL is where an Anthropic key's calls go, for the one pair whose
// routing facts are the platform's rather than a declaration's: a repository's
// own Anthropic key is the credential of no provider the owner declares, so
// there is no providers.<name>.base_url to take and no auth to read beside it
// (F45, D1418 is about a caller never stating either — this is the entry
// stating both, from the platform's own knowledge of the API the key is for).
// It is the address the gateway's own route falls back to when a call names
// none (gateway's anthropicBase), written out here as internal/cli's other
// table-derived addresses are (githubAPIURL, bitbucketAPIURL): the gateway is
// the reader that sends a call with this key, and an entry that named another
// address would be a call the gateway's own rule for Anthropic refuses.
var anthropicAPIURL = "https://api.anthropic.com"

// copyRepositoryKeys is the other half of the enumeration (KS12c): the model key
// a repository holds as its OWN, entered under an entry that carries the
// repository (config.AnthropicKeyAccount, D3008).
//
// One pair per repository, and its one home is the repository's OWN container
// (gcp.SecretID of that repository's slug). The project's own Anthropic key is
// deliberately not read here and its project-wide container is deliberately not
// a second home: the project's key is already an entry of its own under the
// project's name (config.AnthropicSecret, tier B), served to every repository
// the copy wrote none for — and where a repository has a key of its own, that
// key is what its runs are served INSTEAD of the project's (D3008; the offering
// is cascade.go's ownKeyBeforeTheProjects). A pair that read the project-wide
// copy of the same logical name would enter the project's key under every
// repository's name, which is neither "a repository's own" nor a copy that
// leaves the project's entry serving anybody.
//
// The entry therefore exists for a repository that really holds a key of its
// own, and a container holding nothing is the ordinary answer for one that does
// not (keyCopy.own). The marker, the idempotence and the newer-version line are
// the provider half's machinery unchanged: what makes this pair different is
// only which container it reads and what it says when there is nothing there.
//
// An account's OAuth token is not here and cannot be: its entry is the
// account's, a plain scalar under the account's name, which is the one shape
// this copy's marker cannot be written into (D3015, and the family's own arm in
// bundle.go says the same). It stays entered once, by the person who holds it.
func copyRepositoryKeys(lc *localcfg.Config) ([]keyCopy, error) {
	var out []keyCopy
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		slug, err := providerKeyRepoSlug(lc, repo)
		if err != nil {
			return nil, err
		}
		entry, err := credsvc.RepoCredentialKey(slug, config.AnthropicKeyAccount)
		if err != nil {
			return nil, userErr("%s: %v; nothing was copied", pluginwire.Printable(repo), err)
		}
		out = append(out, keyCopy{
			container:  config.AnthropicSecret,
			repoID:     gcp.SecretID(slug, config.AnthropicSecret),
			entry:      entry,
			baseURL:    anthropicAPIURL,
			auth:       providerAuthXAPIKey,
			repo:       repo,
			credential: config.AnthropicKeyAccount,
			own:        true,
		})
	}
	return out, nil
}

// copyNoServiceReason is the stage's skip on an installation whose config
// carries no credentials service at all. The block's presence is the opt-in
// (localcfg.CredentialsService), so an installation without it has no bundle
// for the copy to write into — and nothing beyond that to say: this is the
// state of every installation that has not turned the service on, and it is not
// work the operator owes anybody.
const copyNoServiceReason = "the project's config carries no credentials_service block, and its presence is what turns the credentials service on: this installation has no credentials service, so there is no bundle for the copy to write a model key into"

// copyNothingReason is the stage's skip when the copy was handed no pair to
// work on. It is not the same answer as the one above: the service is here, and
// the copy looked.
//
// The enumeration is now the whole of what this copy moves (copySources), so
// the sentence can say what the config really is: every pair a copy writes is a
// key entered for a repository, and an installation with none onboarded has
// nothing for it to move. That is a state a person can act on — the repository
// that is not in the config is the repository whose runs the service is not
// being prepared for — which the enumeration's own incompleteness, said here
// while the chunks of KS12 landed one at a time, could not be.
const copyNothingReason = "nothing was copied: this installation's config lists no repository, and every model key the copy moves is one entered for a repository, so there is nothing for it to move into the credentials service's bundle"

// copyNotOwnerReason is the skip of a run that is not a project owner (D3006).
//
// The probe's own refusal is a COMMAND's sentence and about twice a stage's
// line: it says what the document is, who may write into it and what to ask
// for, and a stage's Detail is one bounded line (pluginwire.MaxPrintable) —
// printed whole there it would be cut at the byte where the permission is
// named, which is the half an operator acts on. So the stage says the part a
// person has to do first (who runs what, and which project they own) and leaves
// the rest to the sentence every writer of an entry by hand prints.
func copyNotOwnerReason(lc *localcfg.Config) string {
	return fmt.Sprintf("a project owner of %s must run %s: only a project owner copies this installation's model keys into the credentials service's bundle, and this run is not one (D3006)",
		safetext.Field(lc.GCPProject), selfCommand()+" update")
}

// copyServiceTooOldReason is the skip for a deployed credentials service whose
// release cannot read the family the copy writes. It is the guard's answer said
// the way a stage says one, for the reason above and one more: the guard's
// refusal names the entry, the release and the image, and its fix is the
// command this run already is — the deployment of that service and this copy
// are the two stages of one `fugaro update`, so the line says the state, that
// nothing was copied, and what settles it.
func copyServiceTooOldReason() string {
	return fmt.Sprintf("this installation's credentials service is older than the entries the copy writes, so nothing was copied: the next %s moves that service onto this release and settles it", selfCommand()+" update")
}

// copySessionReason is KS2's question 1 said the way a stage says one (F36,
// D3006): nothing a coding agent's session, this run's own process or a
// GODEBUG http2debug leak carries reaches this bundle, and the line names the
// person's own command because that is the whole of what the reader does about
// it. Which of the three it was is the probe's business and not the reader's:
// all three mean this write is not made from here.
func copySessionReason() string {
	return fmt.Sprintf("nothing was copied: the copy into the credentials service's bundle is a person's own write at their own terminal, never a coding agent's session's, a run's or a GODEBUG http2debug leak's: run %s yourself", selfCommand()+" update")
}

// copySkip is a state the copy does not act on, with the sentence that names
// why. undone marks the skips that leave work owed — a coding agent's session,
// a bundle this run could not read, a person who is not a project owner, a
// service too old to read the document — as against the two that mean there is
// nothing to do at all.
type copySkip struct {
	reason string
	undone bool
}

func (e *copySkip) Error() string { return e.reason }

// copySkipOf is the skip Check, Plan and Apply all answer with.
func copySkipOf(err error) (copySkip, bool) {
	var skip *copySkip
	if errors.As(err, &skip) {
		return *skip, true
	}
	return copySkip{}, false
}

// copyRowState is what the copy does with one pair, read from the entry the
// bundle holds for it and from the version the container's newest one is.
type copyRowState int

const (
	// copyNew is a pair the bundle holds no entry for: it is written.
	copyNew copyRowState = iota
	// copyFollow is an entry the copy wrote, from an older version of the same
	// container than the one the container holds now (D3014), or with the two
	// routing facts the credential's own declaration has changed since: it is
	// written again.
	copyFollow
	// copyUnchanged is an entry that already says exactly what this run would
	// write: nothing is written and no version of the bundle is added, which is
	// what makes a second `fugaro update` change nothing.
	copyUnchanged
	// copyLeft is an entry the copy did not write, and which does not say what
	// it would: it is left as it is and named in one line.
	copyLeft
	// copyAbsent is a container with no version this run can read: there is
	// nothing to copy out of it.
	copyAbsent
)

// copyRow is one pair and what this run does with it.
type copyRow struct {
	pair keyCopy
	// from is the Secret Manager ID of the pair's container the value was read
	// out of — which of keyCopy.homes answered — and therefore what the entry's
	// marker is written with and read back against (D3014).
	from string
	// held is the container's newest version as it was read: the version
	// number, the bytes (in memory and nowhere else) and the slot the run's
	// redaction keeps them out of any output with.
	held  *copyHold
	state copyRowState
	// why is the clause the state is reported with: what the entry holds
	// against what this run would write. It carries names and versions only.
	why string
}

// copyHold is one container read once, and everything the run holds of it.
//
// The value is held rather than passed around so that a line printed while it
// is in memory cannot carry it: initEngine.hold registers the whole value and
// each of its lines with the run's redaction, and release gives the slots back.
// The read is made once per CONTAINER and not once per pair, because one key
// several repositories share is many pairs over one container (D3008) — and the
// slots a run has are bounded (maxHeldSecrets).
type copyHold struct {
	value   []byte
	version string
	release func()
}

// copyPlan is the whole of one run's copy: the document it was decided against,
// the pairs, and what each of them answers.
type copyPlan struct {
	lc     *localcfg.Config
	store  *bundleStore
	before bundleDoc
	// held is every container this read looked at, by Secret Manager ID.
	held map[string]*copyHold
	rows []copyRow
}

// release clear()s every value the plan holds and gives its slot back. It is
// called after the write — the values are the document's by then — and wherever
// a plan is dropped unfinished: a value in memory is a value with a lifetime,
// and the run's redaction is a fixed set of slots.
func (p *copyPlan) release() {
	for id, h := range p.held {
		clear(h.value)
		h.release()
		delete(p.held, id)
	}
}

// copyPlanRead is the whole read the copy is decided on: the two config-side
// answers, the bundle, who is asking, the guard for the service that will read
// the document, and then one read per container.
//
// The order is the one every writer of this document keeps
// (providerKeyBundleStore), and each step is where it is for its own reason:
// KS2's question 1 first of all, because the probe stands ahead of every read
// this stage makes (owner_probe.go); the config's two answers next, because an
// installation with no service and one with nothing outside the bundle make no
// cloud call at all; the container before the probe, so a container nobody has
// created yet is answered as the missing container it is rather than as a
// refusal of the person; the person before any value is read (D3006); the guard
// before the document is built; and the values last.
func copyPlanRead(ctx context.Context, e *initEngine) (*copyPlan, error) {
	lc := e.lc
	// KS2's question 1 is asked here and not only in Apply, because the stage
	// loop checks and plans every stage inside the session this refuses
	// (initflow/loop.go): a read that ran first would have looked at every
	// model credential the installation holds before anyone said no.
	if err := providerKeyOwnerOnly(os.Getenv, "copied"); err != nil {
		return nil, &copySkip{reason: copySessionReason(), undone: true}
	}
	if lc.CredentialsService == nil {
		return nil, &copySkip{reason: copyNoServiceReason}
	}
	pairs, err := copySources(ctx, lc)
	if err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, &copySkip{reason: copyNothingReason}
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return nil, copyBundleUnreadable(err)
	}
	if err := store.check(ctx); err != nil {
		return nil, copyBundleUnreadable(err)
	}
	if err := bundleOwnerProbe(ctx, store, lc, "copied"); err != nil {
		// D3006: an operator's update does not copy, and the probe's own
		// sentence names the project owner who can.
		return nil, &copySkip{reason: copyNotOwnerReason(lc), undone: true}
	}
	if err := refuseAnEntryTheServiceCannotRead(ctx, lc, repoCredentialFamily, repoCredentialSince); err != nil {
		// The deployed service cannot read the family, so a document holding
		// one of its entries is a document that service refuses WHOLE (F22):
		// every caller of it, a coding run included, would be halted by this
		// write. The work is owed, and the run that settles it is this one's
		// own service stage, or the next `fugaro update`.
		return nil, &copySkip{reason: copyServiceTooOldReason(), undone: true}
	}
	before, err := providerKeyBundleBefore(ctx, store)
	if err != nil {
		return nil, copyBundleUnreadable(err)
	}
	// The document is judged through the service's own loader, because what the
	// decision reads is what the SERVICE will read and not a second reading of
	// the same YAML. providerKeyBundleBefore has already refused a document
	// this cannot read, so the error here is unreachable and is the failure it
	// would be.
	var held *credsvc.Bundle
	if len(before.raw) > 0 {
		if held, err = credsvc.ParseBundle(before.raw); err != nil {
			return nil, credsDocRefusal(store.id, err)
		}
	}
	p := &copyPlan{lc: lc, store: store, before: before, held: map[string]*copyHold{}}
	for _, pair := range pairs {
		row, err := p.read(ctx, e, pair, held)
		if err != nil {
			// Nothing stays held over a read that did not finish: these values
			// are not going to be written by this run.
			p.release()
			return nil, err
		}
		p.rows = append(p.rows, row)
	}
	return p, nil
}

// copyBundleUnreadable is the skip for a bundle this read could not get at. It
// is a skip and not a failure, for routeFactsOf's reason: the copy is the last
// stage of an update whose other stages have already done their work, and a run
// that failed here — over a container the installation stage has just created,
// say — would be an update the person has to make twice. What the copy owes is
// said as owed instead.
func copyBundleUnreadable(err error) error {
	return &copySkip{
		reason: fmt.Sprintf("%s, and the copy of this installation's model keys is written into that bundle: nothing was copied", oneLineCLI(remoteErr(err, nil).Error())),
		undone: true,
	}
}

// read is one pair's whole answer: the container's newest version, read once
// per container, and the clause the bundle's own entry decides.
//
// The pair's homes are tried in the mount's own order (keyCopy.homes): the
// repository's container while it holds a version this run can read, the
// project-wide copy otherwise. Only the container that answered is held, so a
// key two repositories share out of the wider container is still read once
// (p.held is keyed by Secret Manager ID), and a repository that overrides it
// with a container of its own is read once for each of the two. The id of the
// one that answered is what the row keeps (copyRow.from) and what the entry's
// marker is written with: it is the container the value really came out of, and
// a version is a version of that one.
func (p *copyPlan) read(ctx context.Context, e *initEngine, pair keyCopy, held *credsvc.Bundle) (copyRow, error) {
	row := copyRow{pair: pair}
	var h *copyHold
	for _, id := range pair.homes() {
		if got, ok := p.held[id]; ok {
			h, row.from = got, id
			break
		}
		value, version, found, err := copyContainerNewest(ctx, p.store, pair, id)
		if err != nil {
			return row, err
		}
		if !found {
			continue
		}
		// The slots are built once per engine, and a stage driven on its own —
		// a test, or an engine whose options were never read — has not built
		// them yet.
		e.slots()
		release, err := e.hold(value)
		if err != nil {
			clear(value)
			return row, err
		}
		h = &copyHold{value: value, version: version, release: release}
		p.held[id] = h
		row.from = id
		break
	}
	if h == nil {
		// A container holding nothing is the state a provider's key is in
		// before anybody has stored one, and it is answered as the skip it
		// is. It names the provider and the repository and not only the
		// container, because that is the state a person acts on: which
		// repository of the installation is served no key.
		//
		// A repository's own key says the other thing, because it is the other
		// state: a repository with none of its own is not a repository served
		// no key — the project's key is what its runs are served, exactly as
		// they were before this copy existed (D3008, copyRepositoryKeys).
		row.state = copyAbsent
		if pair.own {
			row.why = fmt.Sprintf("%s holds no version this run can read, so %s holds no key of its own and the project's key is what its runs are served", safetext.Field(pair.container), pair.name())
		} else {
			row.why = fmt.Sprintf("%s holds no version this run can read, so %s is served no key", safetext.Field(pair.container), pair.name())
		}
		return row, nil
	}
	row.held = h
	row.state, row.why = copyDecide(held, pair, row.from, h.value, h.version)
	return row, nil
}

// copyContainerNewest reads the newest version of one of a pair's containers
// (id, one of keyCopy.homes), in memory, with the version number the read
// resolved to (the server's, as bundleStore.access reads the bundle's). The
// pair is here for the logical name its failures name and not for the ID: the
// caller is the one that keeps which home answered (copyPlan.read).
//
// found=false is "there is nothing here to copy out of": a container that is
// not there, one whose newest version carries no payload, and one Secret
// Manager will not serve. The last is a DISABLED version — the bytes are there
// and retired, and Secret Manager refuses them with a 400 — and it is answered
// as an absence because a value somebody disabled deliberately is not one the
// copy may resurrect or report as this run's fault. A version reference this
// tree builds is not a malformed request, which is the other thing a 400 can
// mean.
//
// A read that FAILED is neither: it is this stage's failure, for the reason
// R7's own rule gives for the service — a copy that skipped a key it could not
// read would leave a repository with neither a mount nor an entry the service
// serves. What the server said is never repeated as it came: the failure is
// bounded and redacted like every other remote error of this document.
func copyContainerNewest(ctx context.Context, store *bundleStore, pair keyCopy, id string) (value []byte, version string, found bool, err error) {
	r, err := store.svc.Projects.Secrets.Versions.
		Access("projects/" + store.project + "/secrets/" + id + "/versions/latest").
		Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusNotFound || ge.Code == http.StatusBadRequest) {
			return nil, "", false, nil
		}
		return nil, "", false, remoteErr(fmt.Errorf("reading the newest version of %s: %w", safetext.Field(pair.container), err), nil)
	}
	if r.Payload == nil || r.Payload.Data == "" {
		return nil, "", false, nil
	}
	value, err = base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		// The payload is not in the error: it is a value, and the failure says
		// only that it was not base64.
		return nil, "", false, remote(fmt.Errorf("reading the newest version of %s: the payload is not base64", safetext.Field(pair.container)))
	}
	number, err := versionNumber(r.Name)
	if err != nil {
		return nil, "", false, remote(err)
	}
	return value, number, true, nil
}

// copyDecide is the whole of the marker's rule (R4, D3014), read from the entry
// the bundle holds for the pair through C1's own accessor — the loader the
// service reads the document with, so the two cannot disagree about what an
// entry says.
//
// A pair the bundle holds no entry for is written; an entry that already says
// what this run would write is left alone and no version is added; an entry the
// COPY wrote is followed when the container it was copied out of is no longer
// the one the repository is served the key from, when the container has moved
// past the version its marker names, or when the credential's own declaration
// has changed under it; and an entry the copy did not write is never
// overwritten. Those are the four answers, and the last one is the one a person
// is told about.
//
// from is the container the value was read out of this run (copyRow.from), and
// the marker's container is the one it was read out of before: the two are
// Secret Manager IDs of the SAME logical name's two scopes as often as not, and
// the marker naming the scope that answered is what lets this tell a rotation
// of one container from the key having moved to the other. A move is followed
// rather than left: the mount's own order is which container the repository's
// runs are served the key from, so an entry naming the other one holds a key
// those runs are no longer served (F27, R6).
func copyDecide(held *credsvc.Bundle, pair keyCopy, from string, value []byte, version string) (copyRowState, string) {
	slug, credential, ok := credsvc.RepoCredentialByKey(pair.entry)
	if !ok {
		// A name C1 cannot read back is a name this tree built wrongly, and
		// the loader would refuse the whole document over it: it is left
		// alone, named, and never written.
		return copyLeft, fmt.Sprintf("the entry name %s is not one C1's rule builds, so this run writes nothing for it", safetext.Field(pair.entry))
	}
	entry, found := credsvc.ReadProviderKey(held, slug, credential)
	switch {
	case !found:
		return copyNew, ""
	case entry.Source.IsZero():
		// An entry the copy did not write: one written by hand, or one
		// `fugaro creds set` set at the owner's terminal. It is not the copy's
		// to replace (R4) — but an entry that already holds exactly what this
		// run would write is not replaced either, because there is nothing to
		// write, and adding a version for it would be an update that changed
		// the bundle on every run.
		if copyEntryMatches(entry, pair, value) {
			return copyUnchanged, fmt.Sprintf("the entry holds what %s and the credential's own declaration say, and carries no marker of this copy", safetext.Field(pair.container))
		}
		return copyLeft, fmt.Sprintf("the entry was not written by this copy and does not hold what %s does, and this run does not overwrite an entry it did not write", safetext.Field(pair.container))
	case entry.Source.Container != from:
		// The key is not held where the entry was copied from any more: the
		// repository is served it from another container of the same logical
		// name now, or from one a runner-path key credential put in place of
		// the provider's own secret. No version is read across the two — a
		// version number is a version of one container, and the marker names
		// which — and the mount's own order decides the answer, so the entry
		// follows the container the runs are served.
		return copyFollow, fmt.Sprintf("the entry was copied from %s, and this repository is served the key from %s now", pair.homeScope(entry.Source.Container), pair.homeScope(from))
	}
	switch {
	case entry.Source.Version == version && copyEntryFactsMatch(entry, pair):
		return copyUnchanged, fmt.Sprintf("the entry was copied from %s version %s and still says what this run would write", pair.homeScope(from), safetext.Field(version))
	case newerVersion(entry.Source.Version, version):
		// A marker naming a version the container no longer holds: both are
		// the copy's own, and the entry's is the newer of them, so a rotation
		// since undone is not followed backwards over it.
		return copyLeft, fmt.Sprintf("the entry was copied from version %s of %s, newer than the %s this container holds now", safetext.Field(entry.Source.Version), pair.homeScope(from), safetext.Field(version))
	case entry.Source.Version != version:
		return copyFollow, fmt.Sprintf("%s holds version %s, newer than the %s the entry was copied from", pair.homeScope(from), safetext.Field(version), safetext.Field(entry.Source.Version))
	}
	// The same version, and the credential's own declaration has moved since
	// the copy wrote the entry: the entry is the copy's own, so it carries what
	// the config says now.
	return copyFollow, fmt.Sprintf("%s holds the version the entry was copied from (%s), and the credential's own declaration in the config has changed since", pair.homeScope(from), safetext.Field(version))
}

// copyEntryMatches is whether an entry the copy did not write already says what
// this run would write: the value the container holds, and the two routing
// facts the credential's own declaration states.
func copyEntryMatches(entry credsvc.ProviderKeyEntry, pair keyCopy, value []byte) bool {
	return entry.Value() == string(value) && copyEntryFactsMatch(entry, pair)
}

// copyEntryFactsMatch is whether the entry's two routing facts are the ones the
// credential's own declaration states.
func copyEntryFactsMatch(entry credsvc.ProviderKeyEntry, pair keyCopy) bool {
	return entry.BaseURL == pair.baseURL && entry.Auth == pair.auth
}

// copyEntryNode is the value node one entry is written with: the mapping of
// exactly the fields the service's loader reads — key, base_url and auth, the
// three providerKeyValueNode builds — with the copy's marker beside them
// (source: container, version), which is what tells a later run that this entry
// is one the copy wrote and which version of which container it came from
// (D3014). The container named is the one the value was read out of (from): a
// Secret Manager id and never a value, and the container whose version below it
// is (credsvc.ProviderKeySource).
//
// The two routing facts are held to the same two rules the loader holds an
// entry to before they are written — the platform's provider-URL check and the
// closed set of two auth shapes — so a pair this document could not carry is
// refused here, in a sentence about the declaration it came from, rather than
// by the service as a document it refuses whole.
//
// Nothing here renders the value: it goes into the node, the node into the
// document, and the document to Secret Manager. Every line this stage prints
// names an entry, a container or a version.
func copyEntryNode(pair keyCopy, from string, value []byte, version string) (*yaml.Node, error) {
	if err := config.CheckProviderBaseURL(pair.baseURL); err != nil {
		return nil, userErr("%s is not an address one of the credentials service's entries may hold (%v): the entry's base_url is the destination of every routed call spent on this key, and the loader and the gateway's routes hold it to the same rule. Fix the credential's own declaration in the project config and run %s again; nothing was copied",
			pluginwire.Printable(pair.name()), err, selfCommand()+" update")
	}
	if pair.auth != providerAuthBearer && pair.auth != providerAuthXAPIKey {
		return nil, userErr("%s's declaration carries the auth shape %s, and one of C1's entries may hold only the two the gateway can send: %s or %s. Fix the credential's own declaration in the project config and run %s again; nothing was copied",
			pluginwire.Printable(pair.name()), pluginwire.Printable(pair.auth), providerAuthBearer, providerAuthXAPIKey, selfCommand()+" update")
	}
	str := func(v string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}
	}
	source := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	source.Content = append(source.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "container"}, str(from),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"}, str(version))
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "key"}, str(string(value)),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "base_url"}, str(pair.baseURL),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "auth"}, str(pair.auth),
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"}, source)
	return root, nil
}

// writes are the rows this run would write, in the order the pairs came in.
func (p *copyPlan) writes() []copyRow {
	var out []copyRow
	for _, row := range p.rows {
		if row.state == copyNew || row.state == copyFollow {
			out = append(out, row)
		}
	}
	return out
}

// count is how many pairs the plan answers with one state.
func (p *copyPlan) count(state copyRowState) int {
	n := 0
	for _, row := range p.rows {
		if row.state == state {
			n++
		}
	}
	return n
}

// reports is whether a row this run found nothing to copy out of is a state the
// run says out loud. A pair the CONFIG declares is: the owner said this
// repository sends code to that provider, so a container holding nothing is a
// key a run of it is served none of, and the run names the provider and the
// repository (absentClause). A pair the config does not declare — a repository's
// own Anthropic key (keyCopy.own) — is not: the container holds nothing for
// every repository that never stored a key of its own, which is most of them,
// and naming each would be a list of repositories a person has nothing to do
// about standing in the line that says what the run wrote.
func (r copyRow) reports() bool { return !(r.state == copyAbsent && r.pair.own) }

// rest is the parenthetical the counts after the first one are said in.
func (p *copyPlan) rest() string {
	var parts []string
	if n := p.count(copyUnchanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d already there", n))
	}
	if n := p.count(copyLeft); n > 0 {
		parts = append(parts, fmt.Sprintf("%d left as they are", n))
	}
	absences := 0
	for _, row := range p.rows {
		if row.reports() && row.state == copyAbsent {
			absences++
		}
	}
	if absences > 0 {
		parts = append(parts, fmt.Sprintf("%d with no version to copy", absences))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// idle is whether this run has nothing to write, and the line that says so. A
// pair this run left alone is NAMED here and not only in the diff, because a
// run with nothing to write prints no diff at all — the diff is what a
// confirmation is made against — and "one with no version to copy" with no
// name is a state nobody could act on.
//
// The lead is the strongest thing this run can say about the whole of what it
// found, and it is read off the states the rows carry rather than assumed: a
// bundle this run did not move because every entry it holds is already right is
// not the same state as one where the containers it looked at held nothing to
// copy, and the sentence the first state earns would tell a person their keys
// are served when no container holds them.
func (p *copyPlan) idle() (string, bool) {
	if len(p.writes()) > 0 {
		return "", false
	}
	lead := "every model key of this installation is already in the credentials service's bundle"
	switch {
	case p.count(copyUnchanged) == 0 && p.count(copyLeft) > 0:
		// Every entry the bundle holds is someone else's, so the sentence
		// above would claim a copy that has not happened. An owner reading
		// this line is deciding whether their service is served the key the
		// entry holds, and "already in the bundle" answers that wrongly.
		lead = "nothing here is a model key this copy wrote into the credentials service's bundle"
	case p.count(copyUnchanged) == 0 && p.count(copyAbsent) > 0:
		lead = "no model key this installation holds outside the credentials service's bundle has a value to copy"
	case p.count(copyAbsent) > 0:
		lead = "every model key this installation holds outside the credentials service's bundle is either already in it or a container that holds nothing to copy"
	}
	return lead + p.rest() + p.leftClause() + p.absentClause(), true
}

// leftClause names the entries this run leaves alone because it did not write
// them (R4) — the entries the bundle held before this stage, with whatever
// value their own writer put there. rest() says how many there are and this
// says which, because a person has to be able to find the entry the copy
// declined to touch and the value nobody may silently replace.
func (p *copyPlan) leftClause() string {
	var names []string
	for _, row := range p.rows {
		if row.state == copyLeft {
			names = append(names, safetext.Field(row.pair.entry))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return ": this run does not overwrite an entry it did not write, and the copy wrote no marker on " + strings.Join(names, ", ")
}

// absentClause names the pairs this run found no version to copy out of, by the
// provider and the repository they belong to. It is leftClause's counterpart
// and exists for the same reason: a run with nothing to write prints no diff,
// so without it the provider whose container nobody has filled yet is a key
// nobody is told about. The name a person acts on is the pair's and not the
// container's: the container is an implementation detail of where this
// installation keeps the key, while "this repository is served no key" is the
// state a run of it is in.
func (p *copyPlan) absentClause() string {
	var names []string
	for _, row := range p.rows {
		if row.reports() && row.state == copyAbsent {
			names = append(names, row.pair.name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	return ": nothing is copied for " + strings.Join(names, ", ")
}

// planDetail is what the stage says it is about to do.
func (p *copyPlan) planDetail() string {
	return fmt.Sprintf("the credentials service's bundle %s at %s: this run copies %d model key(s) into it%s",
		safetext.Field(p.store.id), versionWord(p.before.version), len(p.writes()), p.rest())
}

// versionWord is the bundle's version as a sentence says it.
func versionWord(version string) string {
	if version == "" {
		return "the first version it will hold"
	}
	return "version " + safetext.Field(version)
}

// document is the bundle this run leaves in place: the document as it is, with
// one entry set for each pair that is written — all of them in ONE version.
//
// One version and not one write per pair, for providerKeyBundleDoc's reason: an
// entry written for some of the repositories a key serves and not for the
// others is a state in which the key serves a run on some repositories and not
// on others, which is a half-applied move. The read-modify-write edits YAML
// nodes rather than rebuilding the document from a map, so every entry this
// write was not asked about keeps its bytes.
func (p *copyPlan) document() ([]byte, error) {
	doc, root, err := bundleNodes(p.before.raw)
	if err != nil {
		return nil, err
	}
	for _, row := range p.writes() {
		node, err := copyEntryNode(row.pair, row.from, row.held.value, row.held.version)
		if err != nil {
			return nil, err
		}
		setBundleNodeValue(root, row.pair.entry, node)
	}
	return marshalBundle(doc)
}

// write lands the whole document as one version of the bundle, through the tail
// every writer of this document shares (bundleAddVerified): the add, the check
// of the version it landed as, the re-read it is verified with, and the
// retirement of the version it replaced.
//
// The value the tail is given is nil, and that is not an omission: the request
// it sends IS the document, and the forms a failure is redacted with are the
// document's own (writeForms registers the document, every secret the loader
// finds in it, and each of them base64 as sent) — so every value this write
// sets is one of them.
//
// The step it is given ends nowhere: a reporter with no writer discards what
// the tail writes into it, and the stage's own account is the loop's line.
func (p *copyPlan) write(ctx context.Context, cmd *cobra.Command) (bundleAddResult, error) {
	doc, err := p.document()
	if err != nil {
		return bundleAddResult{}, err
	}
	if _, err := credsvc.ParseBundle(doc); err != nil {
		return bundleAddResult{}, credsDocRefusal(p.store.id, err)
	}
	st := report.New(report.Options{}).Step(initflow.CredentialsCopy)
	return bundleAddVerified(ctx, cmd, st, p.store, p.before, doc, nil)
}

// confirm is the confirmation this write is made behind: the ordinary step,
// confirmed by confirmStageWrite — nothing is asked at the owner's terminal
// (D1540), --yes stands in where it reaches the stage, and --ask puts the
// question back. The diff is printed either way.
//
// It is the ordinary confirmation and not the workspace record's widened one,
// and the difference is what the two writes are: this one copies values that
// are already this installation's into the document its own service reads, and
// it decides nothing about who is admitted, what may be spent or what may be
// read.
func (p *copyPlan) confirm(cmd *cobra.Command, yes bool, w io.Writer, managed bool, c initflow.Conditions) error {
	p.print(w)
	return confirmStageWrite(cmd, p.lc, c, managed, yes, p.what())
}

// what names this write as the confirmation reads it.
func (p *copyPlan) what() string {
	return fmt.Sprintf("this copies %d model key(s) of this installation into the credentials service's bundle %s, in one version: each entry carries the container and the version it was copied from, and no value is printed, logged or carried anywhere else",
		len(p.writes()), safetext.Field(p.store.id))
}

// print is the one confirmation diff: each pair and what this run does with it.
// It carries entry names, container names and versions, and never a value.
//
// A row the run does not report is not in it (copyRow.reports): the diff is what
// the confirmation is made against, and "this repository holds no Anthropic key
// of its own" is not a state anybody decides anything about — it is what most
// repositories of an installation are in, and a diff that listed them would bury
// the rows the person is reading it for.
func (p *copyPlan) print(w io.Writer) {
	fmt.Fprintf(w, "%s: this run copies this installation's model keys into the credentials service's bundle at %s\n",
		safetext.Field(p.store.id), versionWord(p.before.version))
	for _, row := range p.rows {
		if !row.reports() {
			continue
		}
		fmt.Fprintf(w, "  %s: %s\n", safetext.Field(row.pair.entry), row.line())
	}
}

// line is one pair as the diff says it: what this run does, and why.
func (r copyRow) line() string {
	switch r.state {
	case copyNew:
		return fmt.Sprintf("written, from %s version %s", safetext.Field(r.pair.container), safetext.Field(r.held.version))
	case copyFollow:
		return "written again: " + r.why
	case copyUnchanged:
		return "unchanged: " + r.why
	case copyAbsent:
		return "nothing to copy: " + r.why
	}
	return "left as it is: " + r.why
}

// note is the one line this stage prints for an entry it wrote, after the
// version has landed — which is D3014's line, naming the container the value
// was copied from and the entry it was written under. Nothing else about the
// write is printed per pair: the stage's own line carries the count.
func (r copyRow) note() string {
	if r.state == copyNew {
		return fmt.Sprintf("note: the entry %s is copied from version %s of %s, for %s\n",
			safetext.Field(r.pair.entry), safetext.Field(r.held.version), r.pair.homeScope(r.from), r.pair.name())
	}
	return fmt.Sprintf("note: the entry %s is copied again from version %s of %s, for %s: %s\n",
		safetext.Field(r.pair.entry), safetext.Field(r.held.version), r.pair.homeScope(r.from), r.pair.name(), r.why)
}

// copyStage is the stage itself. It is SelfConfirming, like the policy, the
// workspace, the route and the layer stages: it shows its own diff, takes its
// own confirmation, and refuses a coding agent's session.
//
// It is NOT a stage of the managed gate (gatedStages, update_managed.go), and
// that is this stage's own answer rather than an omission: what it moves is
// every model credential the installation holds into the document its own
// service reads, which is not a write anybody's coding agent makes on their
// behalf — and KS2's probe answers an operator's run with the owner's name
// already.
type copyStage struct {
	e *initEngine
	// p is what Apply read and wrote, kept for Verify. Nothing is carried
	// across the check, the plan and the apply (see the file comment).
	p *copyPlan
}

func newCredentialsCopyStage(e *initEngine) *copyStage { return &copyStage{e: e} }

func (copyStage) Name() string    { return initflow.CredentialsCopy }
func (copyStage) SelfConfirming() {}

func (copyStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.CredentialsCopy, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// Check reports what the copy would write. A refusal here is the run's: a
// container this read cannot get at is not something the operator finishes by
// hand. The skips are the vocabulary of the config, the bundle and the two
// guards, and only the ones that leave work owed carry Undone.
func (s *copyStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := copyPlanRead(ctx, s.e)
	if skip, ok := copySkipOf(err); ok {
		return initflow.Status{State: initflow.Skipped, Detail: skip.reason, Undone: skip.undone}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	defer p.release()
	if detail, ok := p.idle(); ok {
		return initflow.Status{State: initflow.Done, Detail: detail}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: p.planDetail()}, nil
}

// Plan is what the run says it is about to do. The diff is printed where a
// person can read it — a terminal, or --yes, which is a person's word that they
// have read it — because the confirmation is made against it. Apply prints the
// same diff again at its own turn, which is where a converge confirms.
func (s *copyStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	p, err := copyPlanRead(ctx, s.e)
	if skip, ok := copySkipOf(err); ok {
		return initflow.Plan{NothingToDo: true, Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Plan{}, err
	}
	defer p.release()
	if detail, ok := p.idle(); ok {
		return initflow.Plan{NothingToDo: true, Detail: detail}, nil
	}
	if env.Interactive || env.Yes {
		p.print(s.e.r.cmd.OutOrStdout())
	}
	return initflow.Plan{Detail: p.planDetail()}, nil
}

// Apply is the write. The session that may not write to the cloud refuses
// first, exactly as the route and layer stages do: the bundle holds every model
// credential the service issues, and no flag a coding agent's session carries
// reaches it. The read below answers the same session with a skip, so this
// refusal is the fence for a caller that reached the write anyway rather than
// the only one — the two are one answer (providerKeyOwnerOnly) said twice.
func (s *copyStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
	}
	p, err := copyPlanRead(ctx, s.e)
	if skip, ok := copySkipOf(err); ok {
		return initflow.Outcome{Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Outcome{}, err
	}
	if detail, ok := p.idle(); ok {
		p.release()
		return initflow.Outcome{Detail: "No changes: " + detail}, nil
	}
	cmd := s.e.r.cmd
	if err := s.e.r.asking(func() error {
		return p.confirm(cmd, env.Yes, s.e.r.w, s.e.r.gateStandsIn(), s.e.r.conditions())
	}); err != nil {
		p.release()
		return initflow.Outcome{}, err
	}
	res, err := p.write(ctx, cmd)
	if err != nil {
		p.release()
		return initflow.Outcome{}, err
	}
	// The values are the document's by now — every form of each of them is
	// written, and the redaction carried them while the run held them — so what
	// the run holds is given back before anything else is printed.
	writes := p.writes()
	p.release()
	for _, row := range writes {
		fmt.Fprint(s.e.r.w, row.note())
	}
	s.p = p
	return initflow.Outcome{
		Changed: true,
		Detail: fmt.Sprintf("copied %d model key(s) into the credentials service's bundle %s, as version %s",
			len(writes), safetext.Field(p.store.id), safetext.Field(res.version)),
	}, nil
}

// Verify re-reads every entry this run wrote and checks that the bundle really
// carries it, with the marker naming the container and the version it came
// from. The value is not compared: the write's own re-read is the whole
// document byte for byte (bundleAddVerified), and what is left for this to
// check is the half a whole-document comparison cannot speak about — that the
// entry the service will be served is under the name the copy meant, carrying
// the copy's own marker and the facts the declaration states.
func (s *copyStage) Verify(ctx context.Context) error {
	if s.p == nil || len(s.p.writes()) == 0 {
		// The run wrote nothing, or was skipped: there is nothing of this
		// run's to read back.
		return nil
	}
	p := s.p
	after, err := p.store.access(ctx, "latest")
	if err != nil {
		return remoteErr(err, nil)
	}
	held, err := credsvc.ParseBundle(after.raw)
	if err != nil {
		return credsDocRefusal(p.store.id, err)
	}
	for _, row := range p.writes() {
		slug, credential, ok := credsvc.RepoCredentialByKey(row.pair.entry)
		if !ok {
			return fmt.Errorf("the entry name %s is not one C1's rule builds", safetext.Field(row.pair.entry))
		}
		entry, found := credsvc.ReadProviderKey(held, slug, credential)
		if !found {
			return fmt.Errorf("the credentials service's bundle at version %s holds no entry %s, though this run copied one into it", safetext.Field(after.version), safetext.Field(row.pair.entry))
		}
		if entry.Source.Container != row.from || entry.Source.Version != row.held.version {
			return fmt.Errorf("the entry %s was written by this run but does not carry the container and the version it was copied from", safetext.Field(row.pair.entry))
		}
		if !copyEntryFactsMatch(entry, row.pair) {
			return fmt.Errorf("the entry %s was written by this run but does not carry the address and the auth shape the credential's own declaration states", safetext.Field(row.pair.entry))
		}
	}
	return nil
}
