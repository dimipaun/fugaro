package cli

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The repository-to-image map's key carries a provider ("github/acme/app"), and
// the map `update` finds on a node written before L11 does not. This file is
// where the provider behind a bare repository name is resolved (R1), once for
// every writer of a key and once for the migration that rewrites an old map.
//
// R1 has two modes, and they are two functions here. repoImageKey writes ONE key
// — `image map set`, `project repos add`, `project repos rm` — so it takes the
// first source that answers, and refuses when two of them disagree or none
// does. migrateImageMap rewrites a whole map, so it takes the UNION of the local
// entry and the admits: a local GitHub acme/app beside a Bitbucket admit is what
// gives one name two keys (D3373), which is the image each repository runs
// today.
//
// The sources, in R1's order: this machine's config entry
// (repos.<name>.provider), the node's own allow_repos entries that admit the
// name, and the git.provider of a checkout of THAT SAME repository. The last is
// not the checkout the command happens to run in: checkoutConfig answers only
// for a checkout whose origin is the repository being resolved, so a command run
// from another repository reads nothing there (F33).

// repoImageKey turns a repository into the key a writer stores: R1's first-hit
// mode, then projectpolicy.Canonical.
//
// repo is a bare owner/name, the form the old map is keyed by: an argument a
// person wrote as provider/owner/name has its provider already, and its caller
// passes it in. provider is the kind the caller knows that way — or a job's own
// input — and "" to take it from R1's sources.
//
// It refuses rather than guess, in the three cases R1 names: a provider Fugaro
// does not serve (R10 — the key would name an image no job of this release can
// run), two sources that disagree, and no source that answers at all.
func repoImageKey(ctx context.Context, lc *localcfg.Config, node *projectpolicy.Policy, repo, provider string) (string, error) {
	if provider != "" {
		return servedImageKey(provider, repo)
	}
	local, _, admits := imageKeySources(lc, node, repo)
	switch {
	case local != "" && len(admits) > 0 && !slices.Contains(admits, local):
		// This machine's config says one provider and the allow-list admits the
		// name at another: the two are one document's word against the other's,
		// and the writer has nothing to break the tie with. An allow-list that
		// admits the name at the local provider TOO is not this case: the two
		// agree, and the local entry is the more specific of them.
		return "", imageKeyDisagreement(repo, local, admits)
	case local != "":
		return servedImageKey(local, repo)
	case len(admits) == 1:
		return servedImageKey(admits[0], repo)
	case len(admits) > 1:
		// The allow-list admits the name at more than one provider and nothing
		// else answers: one key cannot be written without picking one, and
		// picking is the person's (a node that admits both is a project that
		// really has both repositories).
		return "", imageKeyDisagreement(repo, "", admits)
	}
	if p := checkoutGitProvider(ctx, repo); p != "" {
		return servedImageKey(p, repo)
	}
	return "", userErr("cannot tell the git provider of %s: name the repository as provider/owner/name, as in github/acme/app, or run from its checkout", pluginwire.Printable(repo))
}

// migrateImageMap is R1's union mode: the map the project serves today, with
// each bare key turned into the key of every provider the repository is enrolled
// at.
//
// The map it rewrites is the EFFECTIVE one (localcfg.RepoImagesEffective), so a
// project whose map is already repo_images is left byte for byte — which is what
// makes a second `update` write nothing — and one that still holds a legacy map,
// on this machine or on the node, is migrated from whichever of the two the
// readers are serving today (R2).
//
// A key's providers are the union of R1's first two sources. Only when neither
// answers is the checkout of that same repository read, and only when that
// answers nothing either does the key become every provider Fugaro serves —
// D3375, which is today's behaviour exactly: a bare key matches every provider's
// repository, so every job that reaches it keeps the image it has now.
//
// A name neither this machine's config nor an allow_repos entry names is dropped
// and answered with one line: no key of it reached a job before the migration
// either, so there is no image to carry over. The migration never refuses and
// never asks — `update` must not stop on a mapping question — so what it cannot
// resolve it either keeps (every provider) or drops with its line.
//
// dropped holds those lines, ready to print: the wording is this function's,
// because it is the only place that knows a name was dropped rather than moved.
func migrateImageMap(ctx context.Context, lc *localcfg.Config, node *projectpolicy.Policy) (images map[string]string, dropped []string) {
	served, fromRepoImages := lc.RepoImagesEffective(node)
	if fromRepoImages {
		return maps.Clone(served), nil
	}
	if served == nil {
		return nil, nil
	}
	images = make(map[string]string, len(served))
	for _, repo := range slices.Sorted(maps.Keys(served)) {
		local, listed, admits := imageKeySources(lc, node, repo)
		providers := imageKeyUnion(local, admits)
		if len(providers) == 0 {
			if !listed {
				dropped = append(dropped, imageMapDropLine(repo))
				continue
			}
			if p := checkoutGitProvider(ctx, repo); servedProvider(p) {
				providers = []string{p}
			} else {
				providers = slices.Clone(config.Providers)
			}
		}
		var keys []string
		for _, p := range providers {
			key, err := projectpolicy.Canonical(p, repo)
			if err != nil {
				// A key of the map being migrated is a canonical repository
				// already (ValidImageMap, on the node and in the config), so
				// this is a name a hand edit left: it is dropped with its line
				// rather than migrated to a key no lookup reaches.
				keys = nil
				break
			}
			keys = append(keys, key)
		}
		if keys == nil {
			dropped = append(dropped, imageMapDropLine(repo))
			continue
		}
		for _, key := range keys {
			images[key] = served[repo]
		}
	}
	return images, dropped
}

// imageKeySources answers R1's first two sources for repo, a bare owner/name:
// the provider this machine's config names for it ("" when it names none, and
// when the repository has no entry at all), whether the config lists the
// repository, and the providers the node's own allow_repos entries admit the
// name at, in config.Providers' order.
//
// The provider is answered as the entry spells it; both callers hold it to
// config.Providers (R10, the config is checked on read, so an entry naming
// another kind is one no key may be written for). Such an entry still counts as
// listed, which is what keeps its name from being dropped: the repository is one
// of the project's, and the migration falls through to the checkout and then to
// every provider (D3375) rather than writing the key the entry names.
func imageKeySources(lc *localcfg.Config, node *projectpolicy.Policy, repo string) (local string, listed bool, admits []string) {
	if r, ok := lc.RepoFor(repo); ok {
		local, listed = r.Provider, true
	}
	var allow []string
	if node != nil {
		allow = node.AllowRepos
	}
	for _, p := range config.Providers {
		canonical, err := projectpolicy.Canonical(p, repo)
		if err != nil {
			continue
		}
		if projectpolicy.AdmitRepo(allow, canonical) {
			admits = append(admits, p)
		}
	}
	return local, listed, admits
}

// imageKeyUnion is R1's union of the two sources, sorted and without repeats:
// the key's providers when at least one of them answers, and nil when neither
// does (the caller's signal to read the checkout, then to fall back to every
// provider). A provider this release does not serve is left out for
// servedImageKey's reason: the value read here is not always one the config was
// checked for.
func imageKeyUnion(local string, admits []string) []string {
	seen := make(map[string]bool, len(admits)+1)
	var out []string
	for _, p := range append([]string{local}, admits...) {
		if !servedProvider(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// servedImageKey is projectpolicy.Canonical for a provider this release serves,
// with both of a writer's refusals in one place: the kind, and the repository
// path (a Config field or an allow-list entry can carry a name CanonicalRepo
// refuses).
func servedImageKey(provider, repo string) (string, error) {
	if !servedProvider(provider) {
		return "", userErr("the git provider %q is not one this installation serves (%s): a mapping names a repository at one of those", provider, strings.Join(config.Providers, ", "))
	}
	key, err := projectpolicy.Canonical(provider, repo)
	if err != nil {
		return "", userErr("the repository %s: %v", pluginwire.Printable(repo), err)
	}
	return key, nil
}

// servedProvider reports whether a provider kind is one this installation
// serves (config.Providers).
func servedProvider(provider string) bool { return slices.Contains(config.Providers, provider) }

// checkoutGitProvider is R1's third source: the git.provider of a checkout of
// repo in the working directory, "" when there is no checkout there at all or
// when the one there is another repository's. The second half is the whole point
// (F33): a checkout of another repository says nothing about this one, and
// reading its git.provider would write a key for a repository that is not there.
//
// A provider the checkout names but this release does not serve is answered as
// it is written; the callers hold it to config.Providers, which is the write
// rule everywhere (R10).
func checkoutGitProvider(ctx context.Context, repo string) string {
	if c := checkoutConfig(ctx, repo); c != nil {
		return c.Git.Provider
	}
	return ""
}

// imageKeyDisagreement is the refusal of a repository whose provider two sources
// answer differently, or that more than one source admits at all: one key cannot
// be written without picking a provider, and picking is the person's. The fix is
// the same either way, and it is the form a key takes.
func imageKeyDisagreement(repo, local string, admits []string) error {
	what := "allow_repos admits " + strings.Join(admits, " and ")
	if local != "" {
		what = "this machine's config says " + local + ", and " + what
	}
	return userErr("cannot tell which provider %s is on (%s): name the repository as provider/owner/name, as in github/acme/app", pluginwire.Printable(repo), what)
}

// imageMapDropLine is the one line a name the migration dropped is answered
// with: the line names the name as the old map spelled it and says why nothing
// was written for it. It is a line about the migration of a map, whose command
// is `image map set`, and not a claim about a member: what is written from here
// on is repo_images (R11).
func imageMapDropLine(repo string) string {
	return "image map: " + pluginwire.Printable(repo) + " dropped: no enrolled repository has that name"
}
