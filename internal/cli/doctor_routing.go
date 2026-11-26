package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
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
	out = append(out, doctorRoutingSourceSecretChecks(ctx, lc)...)
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
// credentials service's bundle does not hold: a pool source whose pool lists a
// member the document has no entry for, or a source naming the project's
// Anthropic key or a provider key the document does not carry. A chain that
// reaches such a source is a call the service cannot serve, and the walk skips
// it — so a chain of one is a run that cannot start, and a chain of several is
// a fallback the person who wrote it does not have.
//
// The bundle is the ONE document the credentials service reads
// (credsvc.BundleSecretID, written by `fugaro creds set`), and it is read here
// through the same store doctorBundleChecks reads it through. Only NAMES are
// ever printed: whether the document holds a credential is what this check
// answers, and a value never enters a message.
//
// The names are the ones the service's own walk looks up for a source
// (internal/credsvc's chain planSource): a pool's members, the project's
// Anthropic key for a source of kind anthropic, and the provider's own name for
// a source of kind provider. The launcher's pool names nothing here — which
// pool it is is the designation record's, and its absence is
// doctorRoutingLauncherPoolChecks' — and Vertex is reached through the
// installation's service account, so there is no entry of the document to hold.
//
// A bundle that could not be read is an unknown and never a pass, and an
// installation that has not opted into the credentials service is told so in
// one line: it is the state an installation with no service is in, and a
// warning about every credential of its table would be doctor inventing a
// finding out of a shape the installation never had.
func doctorRoutingSourceSecretChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	const id = "routing-source-secret"
	asks := routingChainCredentialAsks(lc)
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
		if _, ok := b.Lookup(a.name); ok {
			continue
		}
		out = append(out, doctorCheck{
			ID:       fmt.Sprintf("%s-%d", id, len(out)+1),
			Severity: "warning",
			Problem: fmt.Sprintf("the source %s names the credential %s, and the service's bundle %s holds no entry for it: a call the chain reaches there is skipped, and a chain that names nothing else serves no run",
				a.at, pluginwire.Printable(a.name), pluginwire.Printable(store.id)),
			Fix: selfCommand() + " creds set " + config.CredentialSecret(a.name),
		})
	}
	return out
}

// routingCredentialAsk is one credential a chain's source needs from the
// bundle: the name the service looks up, and where in the table it is named.
type routingCredentialAsk struct {
	at   string
	name string
}

// routingChainCredentialAsks is every credential the table's sources need from
// the bundle, in the table's own order, one entry per (source, credential)
// pair. A source of a kind that names no bundle entry — the launcher's pool,
// Vertex — asks nothing, and neither does a pool source naming a pool the
// project does not declare: RT3's own undeclared problem is that finding, and a
// member list doctor invented from a pool that is not there would be a second,
// wrong one.
func routingChainCredentialAsks(lc *localcfg.Config) []routingCredentialAsk {
	var out []routingCredentialAsk
	add := func(at, name string) {
		if name == "" {
			return
		}
		if slices.ContainsFunc(out, func(a routingCredentialAsk) bool { return a.at == at && a.name == name }) {
			return
		}
		out = append(out, routingCredentialAsk{at: at, name: name})
	}
	for i, e := range lc.ModelRouting {
		for j, s := range e.Chain {
			at := fmt.Sprintf("model_routing[%d].chain[%d]", i, j)
			switch s.Kind {
			case routing.KindPool:
				if pool, ok := routingPoolNamed(lc.Pools, s.Pool); ok {
					for _, member := range pool.Members {
						add(at, member)
					}
				}
			case routing.KindAnthropic:
				add(at, config.AnthropicSecret)
			case routing.KindProvider:
				add(at, s.Provider)
			}
		}
	}
	return out
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
