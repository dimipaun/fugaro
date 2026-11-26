package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator/service"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/routing"
)

// doctor_routing.go is RT7 of docs/model-credentials-routing: doctor reads the
// project's routing table and says what a run of this project would meet. Five
// reports, each one fact a person fixes before a run fails on it:
//
//   - a table problem RT3's own checks report (a chain that reaches nothing, a
//     source naming a pool or provider the project does not declare, a model no
//     entry matches), one line per problem;
//   - a subscription token in no pool (X2), which release 1 reports and release
//     2 refuses;
//   - a source whose credential the installation's bundle does not hold, so the
//     call fails where the chain reaches it;
//   - a chain naming the launcher's pool on a project where no pool is
//     designated to anyone, so that source serves nothing;
//   - a project whose chains can pause a run while this machine has no resident
//     orchestrator, so nothing would resume what the service recorded (D3186).
//
// What is read is the local config and this machine's own file, and nothing
// else: doctor measures, it never writes and it reaches no cloud here. The
// table is judged for the repositories the machine's own config records
// (`repos:`, the same list doctorCanonicalRepos reads) through RT6's own gate
// (routingTableGoverns): a project that publishes no table, or one whose every
// source is admitted to another repository, reaches its models the release-1
// way (D3170, O7, R25) and is not a table with no entries.
//
// The check is silent for a project with no table in force, which is where it
// deliberately earns its keep: nothing here prints on an installation that has
// not migrated, so the release-1 bridge is not reported as a set of problems.

// doctorRoutingChecks are all five reports, in the order a reader acts on them:
// the table's own refusals first, then the credential a chain would fail on,
// then the two states that are a project's shape rather than a table's mistake.
func doctorModelRoutingChecks(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config) []doctorCheck {
	if lc == nil {
		return nil
	}
	repos := slices.Sorted(maps.Keys(lc.Repos))
	if !routingTableGoverns(lc.ModelRouting, repos) {
		return nil
	}
	var out []doctorCheck
	out = append(out, doctorRoutingTableChecks(cmd, lc, repos)...)
	out = append(out, doctorRoutingSourceSecretChecks(ctx, lc, repos)...)
	out = append(out, doctorRoutingLauncherPoolChecks(lc)...)
	out = append(out, doctorPauseWithoutOrchestrator(lc)...)
	return out
}

// doctorTableChecks are RT3's own two answers: the problems — which refuse a
// table at every door that writes one — and the one warning, X2's lone
// subscription token.
//
// The problems are ordinary checks (no severity) because a table problem is a
// run of this project that cannot start, and doctor fails on it as it fails on
// every other state a run meets; the lone token is a warning, because release 1
// reports it and goes on (X2) and release 2 is what refuses it.
func doctorRoutingTableChecks(cmd *cobra.Command, lc *localcfg.Config, repos []string) []doctorCheck {
	models, err := routingModelNames(cmd, lc)
	if err != nil {
		// The models a run can call are the judgment's input, and a checkout
		// whose file will not read is one doctor has nothing to judge against:
		// an unknown, never an "ok" (a table that routes every model and one
		// nobody could check are not the same answer).
		return []doctorCheck{{ID: "routing-table", Severity: "info", unread: true,
			Problem: "the routing table was not judged: the models this project can call could not be read (" + oneLineCLI(err.Error()) + "), so whether the table routes them all is unknown",
			Fix:     "fix the checkout's fugaro.yaml, or run " + selfCommand() + " routing ls to read the table as it stands"}}
	}
	problems, warnings := modelRoutingProblems(lc, repos, models)
	var out []doctorCheck
	for i, p := range problems {
		out = append(out, doctorCheck{
			ID:      fmt.Sprintf("routing-table-%d", i+1),
			Problem: p.Message,
			Fix:     routingTableFix(),
		})
	}
	for i, w := range warnings {
		if w.Code != routing.CodeLoneSubscription {
			continue
		}
		out = append(out, doctorCheck{
			ID:       fmt.Sprintf("routing-lone-subscription-%d", i+1),
			Severity: "warning",
			Problem:  w.Message,
			// The migration is what writes a lone token's pool of one (D3176),
			// so the fix is the command that runs it rather than an edit.
			Fix: selfCommand() + " update",
		})
	}
	return out
}

// routingTableFix is the one command every table problem is fixed with: the
// table is written by `fugaro routing set` and by nothing else, and the
// problem's own sentence names the entry and the source to write.
func routingTableFix() string {
	return selfCommand() + " routing set <pattern> <source>..."
}

// doctorRoutingSourceSecretChecks reports a source whose credential the
// credentials service's bundle does not hold, so that the service's own walk
// skips it: a pool source whose member the document holds no entry for, or a
// source of kind anthropic or provider on a repository whose own entries the
// document holds none of. A chain reached by one of them is a call the service
// cannot serve — so a chain of one is a run that cannot start, and a chain of
// several is a fallback the person who wrote it does not have.
//
// The bundle is the ONE document the credentials service reads
// (credsvc.BundleSecretID, written by `fugaro creds set`), and it is read here
// through the same store doctorBundleChecks reads it through. Only NAMES are
// ever printed: whether the document holds a credential is what this check
// answers, and a value never enters a message.
//
// Every ask is the lookup the service's own walk makes for that source
// (internal/credsvc's chain planSource), so that doctor cannot report a
// credential the service would have found, and cannot miss one it looks up: a
// pool's members through the bundle's name index, with the tier the walk
// selects on, and the three single-credential kinds through the repository's
// own entries of C1's family (credsvc.ReadProviderKey). The launcher's pool
// names nothing here — which pool it is is the designation record's, and its
// absence is doctorRoutingLauncherPoolChecks' — and Vertex is reached through
// the installation's service account, so there is no entry of the document to
// hold.
//
// A bundle that could not be read is an unknown and never a pass, and an
// installation that has not opted into the credentials service is told so in
// one line: it is the state an installation with no service is in, and a
// warning about every credential of its table would be doctor inventing a
// finding out of a shape the installation never had.
func doctorRoutingSourceSecretChecks(ctx context.Context, lc *localcfg.Config, repos []string) []doctorCheck {
	const id = "routing-source-secret"
	asks := routingChainCredentialAsks(lc, repos)
	if len(asks) == 0 {
		return nil
	}
	unread := func(what string) []doctorCheck {
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: "the routing table's credentials could not be checked against the bundle: " + what,
			Fix:     "rerun doctor with your credentials available, or read the bundle's names yourself: " + selfCommand() + " creds ls"}}
	}
	if lc.CredentialsService == nil {
		return unread("this installation has not opted into the credentials service, which is where a chain's credentials are held")
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return unread(oneLineCLI(err.Error()))
	}
	if err := store.check(ctx); err != nil {
		return unread(oneLineCLI(err.Error()))
	}
	meta, err := store.newest(ctx)
	switch {
	case errors.Is(err, errNoBundleVersion):
		return unread("the service's bundle " + pluginwire.Printable(store.id) + " holds no version yet")
	case err != nil:
		return unread(oneLineCLI(err.Error()))
	}
	doc, err := store.access(ctx, meta.number)
	if err != nil {
		return unread(oneLineCLI(err.Error()))
	}
	b, err := credsvc.ParseBundle(doc.raw)
	if err != nil {
		return unread("the document is not one the service accepts: " + oneLineCLI(err.Error()))
	}
	var out []doctorCheck
	for _, a := range asks {
		if a.held(b) {
			continue
		}
		out = append(out, doctorCheck{
			ID:       fmt.Sprintf("%s-%d", id, len(out)+1),
			Severity: "warning",
			Problem:  fmt.Sprintf(a.problem, pluginwire.Printable(store.id)),
			Fix:      selfCommand() + " creds set " + a.key,
		})
	}
	return out
}

// routingCredentialAsk is one entry a chain's source needs the bundle to hold,
// and the lookup that answers whether it does: the key a person reads, the
// document key the write enters it under, and the service's own question about
// the document.
type routingCredentialAsk struct {
	// repo is the repository the entry is that repository's own for, and empty
	// for a credential the project holds once. It is what tells two asks of one
	// credential for two repositories apart, and it is printed in the message
	// of the two repo-scoped kinds.
	repo string
	// key is the document key `fugaro creds set` enters the credential under,
	// which is the fix's own argument.
	key string
	// problem is the finding, with one %s where the bundle's container goes: the
	// container's name is only known once the store has been read.
	problem string
	// held is the lookup the service's walk makes for this source
	// (planSource), so that a document the service would serve this source from
	// is never reported as one it could not.
	held func(*credsvc.Bundle) bool
}

// routingChainCredentialAsks is every credential the table's sources need the
// bundle to hold, in the table's own order, one entry per (source, credential,
// repository) state that is a hole when the document holds it not. A source of
// a kind that names no bundle entry — the launcher's pool, Vertex — asks
// nothing, and neither does a pool source naming a pool the project does not
// declare: RT3's own undeclared problem is that finding, and a member list
// doctor invented from a pool that is not there would be a second, wrong one.
//
// A member the service's pool walk skips is skipped here too, and for the same
// reasons: a runner-path key and the built-in Vertex credential are served
// outside the document, and a subscription member is not selectable at all
// while the spread switch is off (D1622) — a missing entry is not what holds
// either of them back, and reporting one would be a finding nobody can act on.
func routingChainCredentialAsks(lc *localcfg.Config, repos []string) []routingCredentialAsk {
	var out []routingCredentialAsk
	seen := make(map[string]bool)
	add := func(a routingCredentialAsk) {
		k := a.repo + "\x00" + a.key
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, a)
	}
	for i, e := range lc.ModelRouting {
		for j, s := range e.Chain {
			at := fmt.Sprintf("model_routing[%d].chain[%d]", i, j)
			switch s.Kind {
			case routing.KindPool:
				pool, ok := routingPoolNamed(lc.Pools, s.Pool)
				if !ok {
					continue
				}
				for _, member := range pool.Members {
					typ, declared := routingMemberType(lc, member)
					if !declared || config.CredentialServingPath(typ) != config.CredentialPathCredsvc {
						continue
					}
					if typ == config.CredentialTypeClaudeOAuth && !routingSpreadOn(lc) {
						continue
					}
					name, poolName := member, s.Pool
					add(routingCredentialAsk{
						key: accountsAddSecretName(name, typ),
						problem: fmt.Sprintf("the source %s reaches the pool %s, and the service's bundle %%s holds no entry for its member %s: that member serves nothing, and a pool with no other member serves none of this entry's calls",
							at, pluginwire.Printable(poolName), pluginwire.Printable(name)),
						held: func(b *credsvc.Bundle) bool {
							m, ok := b.Lookup(name)
							return ok && m.Kind == credsvc.KindModel && (m.Tier == creds.TierA || m.Tier == creds.TierB)
						},
					})
				}
			case routing.KindAnthropic, routing.KindProvider:
				// Both are the repository's own entries of C1's family — the
				// repository's Anthropic key and the provider's key for that
				// repository — so the ask is per repository, and only for the
				// repositories this source admits (an empty admit list admits
				// nobody, which is inert rather than a hole).
				for _, repo := range repos {
					if !slices.Contains(s.Admit, repo) {
						continue
					}
					slug, err := providerKeyRepoSlug(lc, repo)
					if err != nil {
						// A repository whose slug cannot be derived is one
						// whose entries cannot be named at all: no ask, rather
						// than a finding about an entry no one could write.
						continue
					}
					repo := repo
					if s.Kind == routing.KindAnthropic {
						add(routingCredentialAsk{
							repo: repo,
							key:  config.AnthropicSecret,
							problem: fmt.Sprintf("the source %s is the Anthropic key, and the service's bundle %%s holds neither the project's %s nor %s's own entry: the source serves nothing",
								at, pluginwire.Printable(config.AnthropicSecret), pluginwire.Printable(repo)),
							held: func(b *credsvc.Bundle) bool {
								if _, ok := b.Lookup(config.AnthropicSecret); ok {
									return true
								}
								_, ok := credsvc.ReadProviderKey(b, slug, config.AnthropicKeyAccount)
								return ok
							},
						})
						continue
					}
					provider := s.Provider
					add(routingCredentialAsk{
						repo: repo,
						key:  config.CredentialSecret(provider),
						problem: fmt.Sprintf("the source %s is the provider key %s, and the service's bundle %%s holds no entry of it for %s: the source serves nothing",
							at, pluginwire.Printable(provider), pluginwire.Printable(repo)),
						held: func(b *credsvc.Bundle) bool {
							if _, ok := credsvc.ReadProviderKey(b, slug, provider); ok {
								return true
							}
							_, ok := b.Lookup(config.ProviderKeySecret)
							return ok
						},
					})
				}
			}
		}
	}
	return out
}

// routingMemberType is a pool member's declared credential type: the block the
// loader already held the member against (config.ValidatePools), and false for
// a name no block defines, which is the built-in Vertex credential no
// installation writes down (D1624).
func routingMemberType(lc *localcfg.Config, member string) (string, bool) {
	c, ok := config.CredentialNamed(lc.Credentials, member)
	return c.Type, ok
}

// routingSpreadOn is whether a run may spread across two or more subscriptions
// (D1622), the switch that decides whether a Claude OAuth member is selectable
// at all: a pool it holds back is not a pool with a credential missing.
func routingSpreadOn(lc *localcfg.Config) bool {
	return lc.SubscriptionSpread != nil && *lc.SubscriptionSpread
}

// routingPoolNamed is the project's pool by the name a source of kind pool
// names: RT3's own lookup (its unexported poolNamed), restated here because the
// checks' copy is the checks' and this reader must not reach into them.
func routingPoolNamed(pools []config.Pool, name string) (config.Pool, bool) {
	if name == "" {
		return config.Pool{}, false
	}
	for _, p := range pools {
		if p.Name == name {
			return p, true
		}
	}
	return config.Pool{}, false
}

// doctorRoutingLauncherPoolChecks reports a chain that names the launcher's
// pool on a project where no one has designated one (D3180, D3176).
//
// The designation is what says which pool serves a launcher's principal
// (D3179 as amended), and a project that has designated none is one where the
// source is skipped at every run: the chain falls through to the source after
// it, which is not a failure but is very rarely what the person who wrote the
// entry meant. It is a warning rather than a problem for exactly that reason —
// D3180 makes it a skip and not a refusal.
//
// A designation is read as the project's own record of it: a pool naming a
// principal (config.PoolPrincipal). A project where one pool names one is a
// project where someone has designated one, whoever they are — doctor says
// nothing about which principal a given launcher carries, which no local file
// answers.
func doctorRoutingLauncherPoolChecks(lc *localcfg.Config) []doctorCheck {
	if slices.ContainsFunc(lc.Pools, func(p config.Pool) bool { return p.Principal != nil }) {
		return nil
	}
	var out []doctorCheck
	for _, e := range lc.ModelRouting {
		if !slices.ContainsFunc(e.Chain, func(s routing.Source) bool { return s.Kind == routing.KindLauncherPool }) {
			continue
		}
		out = append(out, doctorCheck{
			ID:       fmt.Sprintf("routing-launcher-pool-%d", len(out)+1),
			Severity: "warning",
			Problem: fmt.Sprintf("the entry %s goes out on the launcher's pool, and no pool of this project is designated to anyone: the source serves nothing and every run falls through to the source after it",
				pluginwire.Printable(e.Match)),
			Fix: selfCommand() + " accounts pool add <pool> --service-account <account>",
		})
	}
	return out
}

// doctorPauseWithoutOrchestrator reports a project whose chains can pause a run
// while this machine has no resident orchestrator (C3, PR4, D3186).
//
// A run pauses when its chain is exhausted and the reset is near (D3184), and
// only a pool has a reset to wait for: the chains that can pause are the ones
// naming the launcher's pool or a project pool. The credentials service is what
// records the pause — it is the side that answers the call — and the resident
// orchestrator is what resumes it, so a project with the service and a
// pause-capable chain is exactly the project that needs one.
//
// The orchestrator is read on this machine alone, through the same unit
// doctorOrchestratorUnit reads (O9): whether a daemon runs somewhere else is
// not a fact this machine holds. A machine with no unit is the finding, because
// a pause nothing resumes waits for a person (D3186); a platform with no login
// unit at all, and a unit that cannot be rendered or read, are not — the one is
// a machine that cannot carry a daemon, and the other is a read that did not
// answer.
func doctorPauseWithoutOrchestrator(lc *localcfg.Config) []doctorCheck {
	const id = "pause-no-orchestrator"
	if lc.CredentialsService == nil || !routingChainsCanPause(lc.ModelRouting) {
		return nil
	}
	u, err := orchestratorUnit()
	if err != nil {
		var un *service.UnsupportedError
		if errors.As(err, &un) {
			return []doctorCheck{{ID: id, OK: true}}
		}
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: "whether this machine has a resident orchestrator could not be checked: the login unit this release would write could not be rendered (" + oneLineCLI(err.Error()) + ")",
			Fix:     "run doctor in a shell with the variables the unit needs set"}}
	}
	_, ok, err := unitTextOnDisk(u)
	switch {
	case err != nil:
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: "whether this machine has a resident orchestrator could not be checked: " + pluginwire.Printable(u.Path) + " could not be read (" + oneLineCLI(err.Error()) + ")",
			Fix:     "make " + pluginwire.Printable(u.Path) + " readable, or run " + selfCommand() + " orchestrator install"}}
	case ok:
		return []doctorCheck{{ID: id, OK: true}}
	}
	return []doctorCheck{{ID: id, Severity: "warning",
		Problem: "this project routes calls through a pool, which can pause a run when the pool is exhausted, and this machine has no resident orchestrator: nothing resumes a pause, so a run that pauses waits for a launcher to continue it by hand",
		Fix:     selfCommand() + " orchestrator install"}}
}

// routingChainsCanPause reports whether any chain of the table names a source
// that waits for a reset: the launcher's pool, or one of the project's pools.
func routingChainsCanPause(table routing.Table) bool {
	for _, e := range table {
		for _, s := range e.Chain {
			if s.Kind == routing.KindLauncherPool || s.Kind == routing.KindPool {
				return true
			}
		}
	}
	return false
}

// routingModelNames is RT5's own model list (routingModels) as RT3's checks
// take it: every model a run of this project can be served on, as this
// machine's documents name them.
func routingModelNames(cmd *cobra.Command, lc *localcfg.Config) ([]string, error) {
	models, err := routingModels(cmd, lc)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.model)
	}
	return out, nil
}
