package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// The credential route's publish, as a stage of `fugaro update`: the MEMBER's
// own installation works out, for each of its repositories and each of the
// three kinds of credential, whether its own project serves that kind or the
// workspace's service does, and publishes one record per repository at
// creds.RoutePath(slug) in its OWN database (WS9 of
// docs/workspace-service/implementation-plan.md; R3, R4 and C2 of the same
// plan's reconciliation, and internal/creds/route.go for the record itself).
//
// **Why the member publishes it and not the run.** What a member's policy
// admits and what its bundle holds are two lists only the member's own machine
// reads, and the run may not guess between them: the record is computed once,
// here, and the run reads it once at bootstrap and keeps the answer (D2982).
// The publish is the other half of creds.RouteRecord, and it is the only
// writer of the leaves the reader reads.
//
// **The project's own copy wins (D2973).** A kind is moved to the workspace
// only where this project's bundle does NOT hold a credential its own policy
// admits for that repository: the member pays for its own calls wherever it
// can, and the workspace's money is used for what the member cannot serve
// itself. The direction is deliberate and it is C2's own reading of the
// record: a kind the record does not name is the project's, so a record
// truncated, mistyped or half-understood cannot send a call anywhere but the
// project.
//
// **A member in no workspace publishes nothing.** It removes every record it
// published before, which is the reading that makes leaving a workspace
// converge with no step of its own: a repository with no record is served by
// the project's own service, exactly as it was before this record existed
// (C2's own words). The same holds for a member whose pin names no service
// address (R3): such a pin is valid and means "this member takes no workspace
// credential".
//
// **The route is per repository because admission is (F34).** Two repositories
// of one project may be admitted to different accounts, and to different
// forge credentials, so one "this project is on the workspace" statement would
// send every repository of a project to the workspace the moment one of them
// is uncovered.
//
// **The member's policy is asked for the PROJECT's credential and never for
// the workspace's (U1).** What settles the account-name question the
// reconciliation left open is where each leg is asked: a kind is `project`
// here only where the member's own policy admits a credential ITS OWN bundle
// holds for that repository, and the workspace leg is not asked at publish
// time at all. It cannot be: a member cannot read the workspace's entries
// (F38), so a route gated on the member's policy naming one of them could
// never move a kind to the workspace. What the member's policy must name for
// the workspace to serve a call is asked where the call is made, by
// projectpolicy.MemberAdmitsShared and the repository's own admission child,
// which is internal/credsvc's Workspace.Shared and not this stage's business.
//
// **The record is a policy-side leaf and the rules are already there.** Its
// path is creds.RoutePath, which escapes the slug the way the rules compare a
// run token's `fs` claim, so a run reads its own repository's record and no
// other. Nothing here writes a grant: the leaf lives under the subtree the
// deployed rules already admit a run to.

// routeConfig is the half of the publish the local config alone decides: the
// address this project's pin carries, and whether it publishes a route at all.
//
// It is asked EVERY time and never cached, for the reason
// workspaceConfigState is: the run writes this file under its own stages'
// feet, and the run that first names a budget backend is the run that first
// has a database to publish in.
type routeConfig struct {
	// address is the workspace's credentials service, from this project's own
	// pin (R3). Empty means this project takes no workspace credential.
	address string
	// why is the config-side sentence a run publishes nothing, and it is empty
	// exactly when this project publishes a route.
	why string
}

// publishes is whether this project publishes a route: it is a member of a
// workspace (workspace: and workspace_pin:) AND its pin names a service
// address.
func (c routeConfig) publishes() bool { return c.why == "" }

// routeConfigOf reads the member's own half of the membership. Both halves of
// the absence are the same state and not two: a project in no workspace has
// nothing to route to the workspace's service, and a member whose pin names no
// address is one that takes no workspace credential — R3's own words, which is
// what makes a pin an earlier release wrote converge with no edit.
func routeConfigOf(lc *localcfg.Config) routeConfig {
	if !workspaceMember(lc) {
		return routeConfig{why: fmt.Sprintf("the project %s is in no workspace: it declares no `workspace:` and no `workspace_pin:`", routeProject(lc))}
	}
	if lc.WorkspacePin.ServiceAddress == "" {
		return routeConfig{why: fmt.Sprintf("the project %s's pin names no workspace service address, so it takes no workspace credential", routeProject(lc))}
	}
	return routeConfig{address: lc.WorkspacePin.ServiceAddress}
}

// routeProject is the project as the sentences below name it.
func routeProject(lc *localcfg.Config) string {
	if lc == nil || lc.Name == "" {
		return "this project"
	}
	return safetext.Field(lc.Name)
}

// routeFacts is everything the derivation reads: what this project's own
// bundle holds, and what its published policy admits.
type routeFacts struct {
	// names are the bundle's model credentials, by the name each is served
	// under (credsvc.Bundle.Credentials): an account's own name for a tier-A
	// token, config.AnthropicSecret for the paid key.
	names map[string]bool
	// providerKey is whether the bundle holds the project's provider key
	// (config.ProviderKeySecret), which no run's model call is listed under in
	// `names` and which a cascade may still settle on.
	providerKey bool
	// bitbucket is whether the bundle holds a Bitbucket token at all — the
	// legacy entry or one of the typed family (D2350), which the proxy's own
	// gate reads the same two ways (update_proxied.go).
	bitbucket bool
	// appKey is whether the bundle holds the GitHub App's private key, which
	// is what a GitHub repository's git traffic is minted with (F29).
	appKey bool
	// policy is the published node, or nil where the project has published
	// none.
	policy *projectpolicy.Policy
	// admissions is the accounts block inverted (repoAdmissions): the slug of
	// each repository an account admits, to the sorted names of the accounts
	// admitting it.
	admissions map[string][]string
}

// routeFactsOf reads what the derivation needs. It answers a routeSkip for
// every state that is not a reading — the bundle this session may not open, a
// node that could not be read, a project that has published no policy — and
// never an error: this stage publishes a record the rest of the run does not
// depend on, so a state it cannot read is work left undone rather than a
// `fugaro update` that fails after `brew upgrade` (the HARD RULE, and the
// reason readCredentialsBundle answers a skipped read with a warning).
func routeFactsOf(ctx context.Context, r *initRun, lc *localcfg.Config) (routeFacts, error) {
	names, record := r.readCredentialsBundle(ctx, lc)
	if !record {
		return routeFacts{}, &routeSkip{
			reason: "the credentials service's bundle was not read, and the route says where each kind of credential is served, which is a decision made from it: nothing is published and the records this project published before are left as they are",
			undone: true,
		}
	}
	policy, found, err := projectPolicyRead(ctx, lc)
	if err != nil {
		// A read that FAILED is not a project that admits nothing: publishing
		// an all-workspace route over a node nobody could read would send every
		// call of every repository to the workspace, and removing the published
		// records over it would take the routes away from runs that work. The
		// stage is skipped with the error named and nothing is written.
		return routeFacts{}, &routeSkip{
			reason: fmt.Sprintf("the published policy at %s could not be read (%s), and the route is derived from it: nothing is published and the records this project published before are left as they are",
				projectpolicy.PathShared(), oneLineCLI(err.Error())),
			undone: true,
		}
	}
	if !found {
		return routeFacts{}, &routeSkip{
			reason: fmt.Sprintf("the project has published no policy at %s, and the route is derived from it: nothing is published; run %s to publish it, then run %s",
				projectpolicy.PathShared(), selfCommand()+" project policy publish", selfCommand()+" update"),
			undone: true,
		}
	}
	admissions, err := repoAdmissions(policy)
	if err != nil {
		return routeFacts{}, err
	}
	return routeFacts{
		names:       names,
		providerKey: r.credsProviderKey,
		bitbucket:   r.credsBitbucket || r.credsBitbucketTyped,
		appKey:      r.credsAppKey,
		policy:      policy,
		admissions:  admissions,
	}, nil
}

// holder is where one kind of one repository's credentials is served from:
// creds.HolderProject where this project's own copy serves it, and
// creds.HolderWorkspace otherwise (D2973's direction, and R4's).
//
// canonical is the repository's canonical name ("github/acme/app") and slug is
// task.Slug's key for it; provider is the repository's git provider kind, which
// is also the name of the forge kind it uses (creds.KindBitbucket and
// creds.KindGitHub are gitprov's own spellings, pinned by
// creds.TestRouteNamesArePinned).
func (f routeFacts) holder(kind creds.Kind, provider, canonical, slug string) creds.Holder {
	if v, ok := kindHolder(kind, provider, canonical, slug, f); ok && v {
		return creds.HolderProject
	}
	return creds.HolderWorkspace
}

// kindHolder answers whether this project's own copy serves one kind for one
// repository, and false for a kind this code does not know — which is the
// project's, like every kind no record moves.
func kindHolder(kind creds.Kind, provider, canonical, slug string, f routeFacts) (bool, bool) {
	switch kind {
	case creds.KindModel:
		return f.modelServes(slug), true
	case creds.KindBitbucket, creds.KindGitHub:
		// The two forge kinds are the forge's own name, so a repository's
		// provider kind IS the kind of credential its git traffic uses: the
		// other forge's kind reaches no repository of this installation at all
		// — a run of a GitHub repository never takes a Bitbucket credential —
		// so nothing may move it to the workspace, and the project's own copy
		// (which is to say, no credential at all) is the honest answer.
		if string(kind) != provider {
			return true, true
		}
		if kind == creds.KindBitbucket {
			return f.bitbucket && f.bitbucketReaches(canonical), true
		}
		// The GitHub App key is the installation's one App and the policy
		// carries no per-repository admission for it: the credential reaches
		// every GitHub repository this project runs. The repository's own
		// admission is the run's check (`allow_repos`), asked where the run
		// starts and not here — a route is not a permission.
		return f.appKey, true
	}
	return false, false
}

// modelServes is whether this project's own service serves the model kind for
// one repository: some model credential the bundle holds is one the member's
// policy admits for it.
//
// The admission is the ACCOUNT's own allow_repos list and not the project's:
// the accounts block is what bounds the credential a run of that account
// holds, and it is the list republished as the repository's admission child
// (repoAdmissions, so the two are the same set). The project-wide allow_repos
// is a different question — whether the repository may run at all — and asking
// it here would move a repository that cannot run to the workspace's money.
func (f routeFacts) modelServes(slug string) bool {
	for _, name := range f.admissions[slug] {
		if f.names[routeBundleName(name)] {
			return true
		}
	}
	// The provider key is the one model credential no caller lists under an
	// account: the project's own route to a model provider, keyed by
	// config.ProviderKeyAccount in the accounts block exactly as a Claude
	// account is. It is asked by the same list, so a project whose calls ride
	// it is not mis-reported as one the workspace must serve.
	if f.providerKey && slices.Contains(f.admissions[slug], config.ProviderKeyAccount) {
		return true
	}
	return false
}

// bitbucketReaches is whether the project's Bitbucket credential reaches one
// repository: the legacy block's own list, or the coverage of one of the typed
// records (R5). Both are in the node's provider-qualified spelling and are
// matched with MatchRepo, which is the one implementation of "does this
// pattern admit this repository" (the selector uses it too, bbtokens).
func (f routeFacts) bitbucketReaches(canonical string) bool {
	if f.policy.BitbucketToken != nil && projectpolicy.AdmitRepo(f.policy.BitbucketToken.AllowRepos, canonical) {
		return true
	}
	if ts := f.policy.BitbucketTokens; ts != nil {
		for _, e := range ts.Entries {
			if projectpolicy.AdmitRepo(e.Coverage, canonical) {
				return true
			}
		}
	}
	return false
}

// routeBundleName is the name a policy key is held under in the bundle: the
// third direction of the rule creds.policyKey and creds.reviewOfferings
// implement, and the reason it is a function rather than one map at the call
// site.
//
// The two sentinel keys are the project's own credentials and are named for
// their axis rather than for the container they live in (D3): the paid key is
// policyKey`d from config.AnthropicSecret to config.AnthropicKeyAccount, and
// the provider key from config.ProviderKeySecret to config.ProviderKeyAccount,
// so the two are translated back here. Every other key is an account's name,
// which the bundle holds under the account the name stands for: a config that
// names an alias has its credential stored under the account itself
// (credentialsBundleServes resolves the same way, and for the same reason).
func routeBundleName(key string) string {
	switch key {
	case config.AnthropicKeyAccount:
		return config.AnthropicSecret
	case config.ProviderKeyAccount:
		return config.ProviderKeySecret
	}
	return key
}

// routeRow is one repository's record: the node it is published at, the route
// it carries, and what the read found there.
type routeRow struct {
	repo string // the config's own owner/name, as the operator reads it
	path string
	slug string
	// route is what this run publishes for the repository.
	route creds.Route
	// body is the record's bytes, and etag the ETag of the node they replace:
	// the write is conditional on it, so a publish races nothing.
	body  []byte
	etag  string
	found bool
	// done is a node that already carries this route: there is nothing to do,
	// and nothing is written.
	done bool
}

// routeStale is a published record this run does not publish: a repository
// this project no longer serves a route for, or every record of a project that
// left its workspace.
type routeStale struct {
	path string
	etag string
	// route is the record as it was published, and read is whether this Fugaro
	// could read it at all. The diff names what the operator is giving up, and
	// a record a newer Fugaro wrote is one this binary cannot read.
	route creds.Route
	read  bool
}

// routeUnknown is a repository this run could not derive a route for: the
// record's key is the repository's SLUG, so a repository whose provider the
// local config does not name has no key to publish at. It is reported and not
// guessed at, and what becomes of the record it published before is the run's
// rule about the leaves it cannot attribute (routePublication.keepsUnknowns).
type routeUnknown struct {
	repo string
	why  string
}

// routePublication is one run's publish: the rows it writes, the records it
// removes, and the repositories it could not derive.
type routePublication struct {
	lc *localcfg.Config
	db *rtdb.Client
	// address is the workspace's service, from the member's own pin (R3).
	address string
	// removes is whether this project publishes nothing at all: it is in no
	// workspace, or its pin names no address. Nothing is written and every
	// record it published before is removed.
	removes bool
	// why is the config-side sentence a run publishes nothing.
	why   string
	rows  []routeRow
	stale []routeStale
	// unknown are the repositories this run could not derive a route for.
	unknown []routeUnknown
	// keepsUnknowns is whether a repository this run could not derive a route
	// for keeps whatever it published — and, with it, every other record this
	// run does not publish. The two cannot be told apart: a record's key is its
	// repository's slug, and the provider this config does not name is what the
	// slug's hash is taken from, so the leaf of such a repository is a name
	// this run cannot attribute to anything. The diff says which records are
	// kept, and then this run removes none of them.
	keepsUnknowns bool
}

// routeSkip is the state this stage does not act on, in the sentence that
// names why. undone marks the skips that leave work owed — the database it
// cannot reach, the bundle this session may not read, the policy it could not
// read — as against the one skip that means there is nothing to do at all (a
// project in no workspace that has published no record), which is the
// distinction initflow.Status.Undone carries.
type routeSkip struct {
	reason string
	undone bool
}

func (e *routeSkip) Error() string { return e.reason }

// routeNoDBReason is the skip for a member whose config names no budget
// backend: the record lives in a Realtime Database, so there is nowhere to
// publish it. It is a skip and not a refusal, like the policy stage's own
// (policyNodeUnreachable): a config written before the budget backend is a
// state `fugaro update` must not stop on.
const routeNoDBReason = "the project's config names no Firebase budget backend, so there is no database to publish the credential route in"

// routePublicationRead is the whole read past the config's own half: the
// database, the ownership gate, the bundle, the policy and every node the
// publish would touch.
//
// The gate (budgetDBUnadopted) is asked BEFORE the first read and only when
// this run would write — a project that publishes nothing and has published no
// record has nothing to do in that database at all, so it is skipped without
// opening it.
func routePublicationRead(ctx context.Context, r *initRun, lc *localcfg.Config, rc routeConfig) (*routePublication, error) {
	if policyNodeUnreachable(lc) {
		if !rc.publishes() {
			// No database and no route to publish: there is no record to
			// remove either, because a record can only live in one.
			return nil, &routeSkip{reason: routeNoDBReason}
		}
		return nil, &routeSkip{reason: routeNoDBReason, undone: true}
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "this stage publishes no credential route there", selfCommand()+" update"); why != "" {
		return nil, &routeSkip{reason: why, undone: true}
	}
	p := &routePublication{lc: lc, db: db, address: rc.address, removes: !rc.publishes(), why: rc.why}
	bySlug := map[string]bool{}
	if rc.publishes() {
		facts, err := routeFactsOf(ctx, r, lc)
		if err != nil {
			return nil, err
		}
		if err := p.plan(ctx, facts); err != nil {
			return nil, err
		}
		for _, row := range p.rows {
			bySlug[row.slug] = true
		}
	}
	// A repository this run could not derive a route for may own a leaf this
	// run would otherwise call stale — and which leaf that is cannot be worked
	// out, because the provider this run does not have is what the slug's hash
	// is taken from. So nothing is removed at all, and the diff says which
	// records are kept. A project that publishes no route (removes) is the
	// other case: every record under the subtree is one nobody publishes, and
	// an underived repository's own goes with the rest.
	p.keepsUnknowns = !p.removes && len(p.unknown) > 0
	if !p.keepsUnknowns {
		if err := p.readStale(ctx, lc, bySlug); err != nil {
			return nil, err
		}
	}
	if p.removes && len(p.stale) == 0 {
		// A project in no workspace that published no record: the stage found
		// nothing to do, which is not the same answer as one whose work is
		// owed.
		return nil, &routeSkip{reason: p.why}
	}
	return p, nil
}

// plan derives one route per repository of this project and reads the node
// each is published at.
func (p *routePublication) plan(ctx context.Context, facts routeFacts) error {
	for _, repo := range slices.Sorted(maps.Keys(p.lc.Repos)) {
		row, unknown, err := p.rowFor(repo, p.lc.Repos[repo], facts)
		if err != nil {
			return err
		}
		if unknown != nil {
			p.unknown = append(p.unknown, *unknown)
			continue
		}
		var raw json.RawMessage
		etag, found, err := p.db.GetETag(ctx, row.path, &raw)
		if err != nil {
			return dbErr(p.lc, err, false)
		}
		row.etag, row.found = etag, found
		if found {
			published, _, err := creds.DecodeRoute(raw)
			if err == nil && published == row.route {
				row.done = true
				row.body = nil
			}
		}
		p.rows = append(p.rows, row)
	}
	return nil
}

// rowFor derives one repository's route. It answers an unknown — and no row —
// for a repository whose provider the local config does not name, or whose
// name is not one task.Slug or projectpolicy.Canonical accepts: the record's
// key is the slug, and a key this run cannot compute is a record it must not
// guess at.
func (p *routePublication) rowFor(repo string, entry localcfg.Repo, facts routeFacts) (routeRow, *routeUnknown, error) {
	unknown := func(why string) (routeRow, *routeUnknown, error) {
		return routeRow{}, &routeUnknown{repo: repo, why: why}, nil
	}
	if entry.Provider == "" {
		// Repo.Provider's own doc: empty means the checkout's fugaro.yaml
		// decides, which is a file this stage does not read — and the same
		// ruling usedProviders makes in this very flow, where an entry that
		// records no provider is left out rather than guessed at (localcfg
		// accepts such an entry). Without the provider there is no slug, and
		// without the slug no key: the repository is reported and its published
		// record is left to the run's own rule about the leaves it cannot
		// attribute (routePublication.keepsUnknowns).
		return unknown(fmt.Sprintf("its entry in this project's local config names no provider, and a record's key is the repository's slug: set `repos.%s.provider`", safetext.Field(repo)))
	}
	canonical, err := projectpolicy.Canonical(entry.Provider, repo)
	if err != nil {
		return unknown(fmt.Sprintf("this project's local config names it as %s, which is not a canonical repository name: %v", safetext.Field(entry.Provider+"/"+repo), err))
	}
	slug, err := task.Slug(entry.Provider, repo)
	if err != nil {
		return unknown(fmt.Sprintf("its repository name is not one a record's key can be made from: %v", err))
	}
	route := creds.Route{Version: creds.RouteVersion, Address: p.address}
	for _, kind := range creds.Kinds {
		route = routeWithKind(route, kind, facts.holder(kind, entry.Provider, canonical, slug))
	}
	body, err := creds.RouteRecord(route)
	if err != nil {
		return routeRow{}, nil, userErr("the route for %s could not be built: %v; nothing was published", safetext.Field(repo), err)
	}
	return routeRow{repo: repo, path: creds.RoutePath(slug), slug: slug, route: route, body: body}, nil, nil
}

// routeWithKind is one kind moved, by name: the record's fields are three and
// there is no table of them anywhere else, so a kind added to the vocabulary
// without a field is a kind this function does not move — which is the
// fail-safe direction and not a silent one (the kind is written out by
// RouteRecord's own list, and a kind left unnamed is the project's).
//
// A kind the project serves writes NOTHING: the field is left as the zero value
// and RouteRecord's encode omits it, which is the canonical form C2 publishes
// (movedHolder). The distinction matters beyond tidiness — the plan's "nothing
// to do" and Verify are one value comparison of a decoded record against this
// one, and a struct that spelled HolderProject where the encode drops it would
// differ from every record it had just written.
func routeWithKind(r creds.Route, kind creds.Kind, h creds.Holder) creds.Route {
	if h != creds.HolderWorkspace {
		return r
	}
	switch kind {
	case creds.KindModel:
		r.Model = h
	case creds.KindBitbucket:
		r.Bitbucket = h
	case creds.KindGitHub:
		r.GitHub = h
	}
	return r
}

// readStale reads the records this run does not publish, one ETag each: the
// child names of the subtree the routes live in, less the slugs this run
// publishes.
//
// The key is the database's own child name and not a slug re-derived from it,
// because the two are not the same string for a node nobody wrote through this
// program: budget.Key escapes a slug as itself (task.Slug's character set is
// [a-z0-9-] and the 16 hex digits of a hash), so a leaf of this contract is its
// own escaped spelling — and a leaf that is not is one this run removes
// without inventing a path for it.
func (p *routePublication) readStale(ctx context.Context, lc *localcfg.Config, bySlug map[string]bool) error {
	leaves, err := p.db.Keys(ctx, creds.RoutePathParent())
	if err != nil {
		return dbErr(lc, err, false)
	}
	for _, leaf := range leaves {
		if bySlug[leaf] {
			continue
		}
		path := creds.RoutePathParent() + "/" + leaf
		var raw json.RawMessage
		etag, found, err := p.db.GetETag(ctx, path, &raw)
		if err != nil {
			return dbErr(lc, err, false)
		}
		if !found {
			continue
		}
		s := routeStale{path: path, etag: etag}
		if s.route, s.read, _ = creds.DecodeRoute(raw); !s.read {
			// A record this binary cannot read is removed like any other: this
			// run publishes no route for it, and a record nothing publishes is
			// a decision nobody made. The removal is the fail-safe direction
			// (C2: a repository with no record is served by the project), and
			// the diff says the record could not be read rather than naming a
			// route it does not hold.
			s.route = creds.Route{}
		}
		p.stale = append(p.stale, s)
	}
	return nil
}

// routeSkipOf is the skip both Check and Plan answer with.
func routeSkipOf(err error) (routeSkip, bool) {
	var skip *routeSkip
	if errors.As(err, &skip) {
		return *skip, true
	}
	return routeSkip{}, false
}

// routeStage is the stage itself. It is SelfConfirming, like the policy, the
// workspace and the layer stages: it shows its own diff, takes its own
// confirmation and refuses a coding agent's session.
//
// It is NOT a stage of the managed gate (gatedStages, update_managed.go), and
// that is this stage's own answer rather than an omission: what it writes is a
// leaf derived from two lists the member's own machine holds — its bundle and
// its published policy — and it admits nothing, removes no access and spends
// nothing. The gate's admission is for the writes a managed project's coding
// agent may converge on its owner's behalf, and this one is derived rather
// than decided: the next `fugaro update` at a terminal republishes it.
// Read the other way, a leaf published under a mark would be a claim about
// where a run's credential comes from, made by a session nobody watched.
type routeStage struct {
	e *initEngine
	// planned is the cloud half, cached, and db is the database it was read
	// in: the run's own local-config write may name a backend that was not
	// there when the plan view was drawn, and a read taken before it is a read
	// in another database (routePublicationRead).
	planned bool
	db      string
	p       *routePublication
	err     error
}

func newCredentialsRouteStage(e *initEngine) *routeStage { return &routeStage{e: e} }

func (routeStage) Name() string    { return initflow.CredentialsRoute }
func (routeStage) SelfConfirming() {}

func (routeStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.CredentialsRoute, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// routeDBKey is the database a cached read belongs to: the URL and the
// Firebase project it names, which is the pair every reader of this backend
// opens by.
func routeDBKey(lc *localcfg.Config) string {
	if lc == nil || lc.Budget == nil {
		return ""
	}
	return lc.Budget.RTDBURL + "\x00" + lc.Budget.FirebaseProject
}

// plan is what Check, Plan and Apply all answer with. The config's own half is
// asked every time (routeConfigOf) and only the half that reaches the cloud is
// remembered, keyed by the database it was taken in: the run writes its own
// local config, so the Firebase stage's write is what gives this stage a
// database on the very run that first names one — and a cached read outliving
// that write would publish nothing on it (the workspace stage's forget, kept
// here without a hook on the run).
func (s *routeStage) plan(ctx context.Context) (*routePublication, error) {
	rc := routeConfigOf(s.e.lc)
	if key := routeDBKey(s.e.lc); !s.planned || s.db != key {
		s.planned, s.db = true, key
		s.p, s.err = routePublicationRead(ctx, s.e.r, s.e.lc, rc)
	}
	return s.p, s.err
}

// Check reports what is published. A refusal here is the run's: a node this
// Fugaro cannot read is not something the operator finishes by hand. The skips
// are the vocabulary of the config and the database, and only the ones that
// leave work owed carry Undone.
func (s *routeStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := s.plan(ctx)
	if skip, ok := routeSkipOf(err); ok {
		return initflow.Status{State: initflow.Skipped, Detail: skip.reason, Undone: skip.undone}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	if detail, ok := p.idle(); ok {
		return initflow.Status{State: initflow.Done, Detail: detail}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: p.planDetail()}, nil
}

// idle is whether this run has nothing to do, and the line that says so.
func (p *routePublication) idle() (string, bool) {
	var writes, removes int
	for _, row := range p.rows {
		if !row.done {
			writes++
		}
	}
	removes = len(p.stale)
	switch {
	case writes == 0 && removes == 0 && p.removes:
		return p.why + ": this project publishes no route and has published none", true
	case writes == 0 && removes == 0:
		return fmt.Sprintf("every route of this project is published and carries what this installation's bundle and policy say (%d record(s) at %s)", len(p.rows), creds.RoutePathParent()), true
	}
	return "", false
}

// planDetail is what the stage says it is about to do, in the wording that
// fits this run's plan.
func (p *routePublication) planDetail() string {
	var writes int
	for _, row := range p.rows {
		if !row.done {
			writes++
		}
	}
	switch {
	case p.removes:
		return fmt.Sprintf("%s: this run removes the %d credential-route record(s) this project published before, after your confirmation", creds.RoutePathParent(), len(p.stale))
	case writes > 0 && len(p.stale) > 0:
		return fmt.Sprintf("%s: this run publishes %d credential-route record(s) and removes %d, after your confirmation", creds.RoutePathParent(), writes, len(p.stale))
	case writes > 0:
		return fmt.Sprintf("%s: this run publishes %d credential-route record(s), after your confirmation", creds.RoutePathParent(), writes)
	}
	return fmt.Sprintf("%s: this run removes the %d credential-route record(s) it no longer publishes, after your confirmation", creds.RoutePathParent(), len(p.stale))
}

// RouteKindLine is the per-kind report R4 asks for (F38): where each kind of
// one repository's credentials is served from, said so that a kind served from
// the project's own copy says out loud that a workspace credential of that
// kind is not used.
//
// It is exported so the report reads the same wherever it is made: the run
// that publishes the record is the only place a member may learn what the
// workspace holds (F38 — a member cannot read the workspace's entries), and
// the line names this project's own copy or the address, never an entry of the
// workspace's pool.
func RouteKindLine(kind creds.Kind, h creds.Holder, address string) string {
	if h == creds.HolderWorkspace {
		return fmt.Sprintf("%s: the workspace's service at %s (a workspace credential of this kind is used)", kind, safetext.Field(address))
	}
	return fmt.Sprintf("%s: this project's own copy (a workspace credential of this kind is not used)", kind)
}

// Plan is what the run says it is about to do. The diff is printed where a
// person can read it — a terminal, or --yes, which is a person's word that
// they have read it — because the confirmation is made against it.
func (s *routeStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	p, err := s.plan(ctx)
	if skip, ok := routeSkipOf(err); ok {
		return initflow.Plan{NothingToDo: true, Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Plan{}, err
	}
	if detail, ok := p.idle(); ok {
		return initflow.Plan{NothingToDo: true, Detail: detail}, nil
	}
	if env.Interactive || env.Yes {
		p.print(s.e.r.cmd.OutOrStdout())
	}
	return initflow.Plan{Detail: p.planDetail()}, nil
}

// Apply is the write. The session that may not write to the cloud refuses
// first, exactly as the layer stage does: this stage is not a step the managed
// gate admits, so the refusal is the whole of what a coding agent's session
// gets from it.
func (s *routeStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
	}
	p, err := s.plan(ctx)
	if skip, ok := routeSkipOf(err); ok {
		return initflow.Outcome{Detail: skip.reason}, nil
	}
	if err != nil {
		return initflow.Outcome{}, err
	}
	if detail, ok := p.idle(); ok {
		return initflow.Outcome{Detail: "No changes: " + detail}, nil
	}
	cmd := s.e.r.cmd
	if err := s.e.r.asking(func() error {
		return p.confirm(cmd, env.Yes, s.e.r.w, s.e.r.gateStandsIn(), s.e.r.conditions())
	}); err != nil {
		return initflow.Outcome{}, err
	}
	if err := p.write(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	return initflow.Outcome{Changed: true, Detail: p.doneDetail()}, nil
}

// confirm is the confirmation this write is made behind: the ordinary step,
// confirmed by confirmStageWrite — nothing is asked at the owner's terminal
// (D1540), --yes stands in where it reaches the stage, and --ask puts the
// question back. The diff is printed either way.
//
// It is the ordinary confirmation and not the workspace record's widening
// banner, and the difference is what the two documents are: the record this
// stage writes decides where a run's calls are SERVED FROM and never who is
// admitted, what may be spent or what may be read. The credentials it moves a
// call towards are the member's own, under the member's own policy and the
// repository's own admission (R5), and the workspace's money is spent under
// the entry's allow-list the workspace owner published. There is nothing here
// to reserve for the owner's own typed word.
func (p *routePublication) confirm(cmd *cobra.Command, yes bool, w io.Writer, managed bool, c initflow.Conditions) error {
	p.print(w)
	return confirmStageWrite(cmd, p.lc, c, managed, yes, p.what())
}

// what names this write as the confirmation reads it.
func (p *routePublication) what() string {
	if p.removes {
		return fmt.Sprintf("this removes the %d credential-route record(s) this project published at %s: %s, so its runs take every credential from its own service again", len(p.stale), creds.RoutePathParent(), p.why)
	}
	return fmt.Sprintf("this publishes the credential route of this project's repositories at %s: which of them take a credential from the workspace's service at %s and which from this project's own copy", creds.RoutePathParent(), safetext.Field(p.address))
}

// doneDetail is the line the run reports the write with.
func (p *routePublication) doneDetail() string {
	var writes, removes int
	for _, row := range p.rows {
		if !row.done {
			writes++
		}
	}
	removes = len(p.stale)
	switch {
	case writes > 0 && removes > 0:
		return fmt.Sprintf("published %d credential-route record(s) and removed %d at %s", writes, removes, creds.RoutePathParent())
	case writes > 0:
		return fmt.Sprintf("published %d credential-route record(s) at %s", writes, creds.RoutePathParent())
	}
	return fmt.Sprintf("removed %d credential-route record(s) at %s", removes, creds.RoutePathParent())
}

// write lands every row and every removal, each conditional on the ETag of the
// node it replaces. A concurrent writer makes a precondition fail, and that is
// a refusal and not a retry: the operator confirmed a diff of the documents
// this write replaces.
func (p *routePublication) write(ctx context.Context) error {
	for _, row := range p.rows {
		if row.done {
			continue
		}
		if err := p.put(ctx, row.path, row.etag, json.RawMessage(row.body)); err != nil {
			return err
		}
	}
	for _, s := range p.stale {
		if err := p.put(ctx, s.path, s.etag, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *routePublication) put(ctx context.Context, path, etag string, body any) error {
	if err := p.db.PutIfMatch(ctx, path, etag, body); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the credential route at %s changed while this publish was being confirmed (another machine wrote it meanwhile), so nothing more was written: run %s again to read what it now holds",
				path, selfCommand()+" update")
		}
		return dbErr(p.lc, err, true)
	}
	return nil
}

// Verify re-reads every node this run wrote and every one it removed. A write
// that claims a record it did not land is a node this Fugaro would republish
// on every run, and a removal that did not land is a record a run would still
// be served by.
func (s *routeStage) Verify(ctx context.Context) error {
	if s.p == nil {
		// The run published nothing, or was skipped: there is nothing of this
		// run's to read back.
		return nil
	}
	for _, row := range s.p.rows {
		if row.done {
			continue
		}
		var raw json.RawMessage
		_, found, err := s.p.db.GetETag(ctx, row.path, &raw)
		if err != nil {
			return dbErr(s.p.lc, err, false)
		}
		if !found {
			return fmt.Errorf("the credential route published at %s is gone", row.path)
		}
		got, _, err := creds.DecodeRoute(raw)
		if err != nil {
			return fmt.Errorf("the credential route at %s was written but cannot be read back: %w", row.path, err)
		}
		if got != row.route {
			return fmt.Errorf("the credential route at %s was written but does not carry what this run published", row.path)
		}
	}
	for _, stale := range s.p.stale {
		var raw json.RawMessage
		_, found, err := s.p.db.GetETag(ctx, stale.path, &raw)
		if err != nil {
			return dbErr(s.p.lc, err, false)
		}
		if found {
			return fmt.Errorf("the credential route at %s was removed but is still published", stale.path)
		}
	}
	return nil
}

// print is the one confirmation diff: what each repository's record says, kind
// by kind, what is removed, and the repositories this run could not derive.
func (p *routePublication) print(w io.Writer) {
	head := creds.RoutePathParent()
	if p.removes {
		fmt.Fprintf(w, "%s: this run publishes no credential route (%s), and removes the %d record(s) this project published there\n", head, p.why, len(p.stale))
	} else {
		fmt.Fprintf(w, "%s: this run publishes one record per repository of this project, for the workspace's service at %s\n", head, safetext.Field(p.address))
	}
	for _, row := range p.rows {
		state := "published"
		if row.done {
			state = "unchanged"
		}
		fmt.Fprintf(w, "  %s (%s): %s\n", row.path, safetext.Field(row.repo), state)
		for _, kind := range creds.Kinds {
			fmt.Fprintf(w, "    %s\n", RouteKindLine(kind, row.route.Holder(kind), p.address))
		}
	}
	for _, s := range p.stale {
		held := "an unreadable record"
		if s.read {
			held = fmt.Sprintf("the record it holds (address %s)", safetext.Field(s.route.Address))
		}
		fmt.Fprintf(w, "  %s: removed, and %s goes with it\n", s.path, held)
	}
	for _, u := range p.unknown {
		// What the line says about the repository's own record follows what
		// this run does to the leaves it does not publish: kept, where it
		// removes none of them (keepsUnknowns), and removed with the rest,
		// where it removes every record it finds — a project that left its
		// workspace.
		held := "so any record it published goes with the rest"
		if p.keepsUnknowns {
			held = "so its published record (if any) is left as it is"
		}
		fmt.Fprintf(w, "  %s: no route is derived, %s (%s)\n", safetext.Field(u.repo), held, u.why)
	}
}
