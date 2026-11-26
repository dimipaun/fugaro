package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/spf13/cobra"
)

// doctor's reading of the project's routing table (RT7). Five reports, one
// check ID each, and nothing here decides a rule of its own: the table is judged
// by RT3's checks through the helper RT6 added to validate.go
// (modelRoutingProblems), which is the same answer `validate` refuses a file
// with and `fugaro routing set` refuses a write with — a second assembly of the
// checks here would be free to hand them another table, another budget or
// another model list, which is the drift the checks exist to catch one level
// down.
//
// What doctor adds around that answer is the three states a person meets before
// a launch does, each read from where it is really written: the installation's
// bundle (a source whose secret is not in it), this project's own pools (a chain
// that goes out on the launcher's pool where there is none to designate), and
// the installation's records (a table that can pause where nothing would resume
// what it recorded, D3186).
//
// These checks are the project's and not a repository's, so they read the local
// config wherever doctor runs — the table is the project's (contract-table.md)
// — and every repository they name is the config's own spelling of it (owner/
// name), which is what `admit` is written in.
const (
	// doctorRoutingTableID is RT3's answer for the table itself: a chain that
	// reaches nothing, a source naming a pool or a provider the project does not
	// declare, a model no entry matches, one pattern written twice. It is a
	// failure and not a warning, because a launch of this project is refused on
	// it (R22).
	doctorRoutingTableID = "routing-table"
	// doctorRoutingLoneSubscriptionID is RT3's one report that is not a refusal:
	// a subscription token in no pool (X2). Release 1 reports it, so it fails
	// --strict and never doctor.
	doctorRoutingLoneSubscriptionID = "routing-lone-subscription"
	// doctorRoutingBundleID is a source whose secret this installation's bundle
	// does not hold, read from the document itself.
	doctorRoutingBundleID = "routing-bundle-source"
	// doctorRoutingLauncherPoolID is a chain that goes out on the launcher's
	// pool where this project declares no pool for a designation to name.
	doctorRoutingLauncherPoolID = "routing-launcher-pool"
	// doctorRoutingPauseID is a table that goes out on a pool where the
	// installation records no resident orchestrator, so a run whose pools are
	// exhausted ends with no credential rather than pausing (D3186).
	doctorRoutingPauseID = "routing-pause-orchestrator"
)

// doctorRoutingTableChecks is the five reports, in the order a reader asks the
// questions: what is wrong with the table, then the three states around it, then
// what a pause of it would need.
//
// Nothing is reported for a project whose table governs none of its
// repositories: such a project's runs reach their models the release-1 way
// (routingTableGoverns, O7, R25, D3196), and judging it would report every model
// unrouted from a table that never answered for it. An installation that
// publishes no table at all is the same answer and stays silent, which is what
// doctor has always done for it.
//
// checkBitbucketToken is `--check-bitbucket-token`, the flag doctor already
// reads the bundle behind: the one report that opens the document takes it, and
// every other report here is answered from this machine's own files.
func doctorRoutingTableChecks(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, checkBitbucketToken bool) []doctorCheck {
	if lc == nil {
		return nil
	}
	repos := slices.Sorted(maps.Keys(lc.Repos))
	if !routingTableGoverns(lc.ModelRouting, repos) {
		return nil
	}
	// The project's own model list (RT5's routingModels), which is what makes
	// this read the project's whole answer rather than one file's: a layer, a
	// pin or a review level names a model every repository of the project runs
	// on, and the table has to carry it. A resolution that failed is no model
	// list rather than a wrong one — the pins and levels this machine cannot
	// read are the file checks' subject, not this one's.
	var models []string
	if ms, err := routingModels(cmd, lc); err == nil {
		for _, m := range ms {
			models = append(models, m.model)
		}
	}
	problems, warnings := modelRoutingProblems(lc, repos, models)
	out := make([]doctorCheck, 0, len(problems)+len(warnings)+2)
	for _, p := range problems {
		out = append(out, doctorCheck{ID: doctorRoutingTableID,
			Problem: oneLine(p.Message),
			Fix:     doctorRoutingFix(p)})
	}
	for _, w := range warnings {
		out = append(out, doctorCheck{ID: doctorRoutingLoneSubscriptionID, Severity: "warning",
			Problem: oneLine(w.Message),
			Fix:     doctorRoutingFix(w)})
	}
	out = append(out, doctorRoutingBundleChecks(ctx, lc, repos, checkBitbucketToken)...)
	out = append(out, doctorRoutingLauncherPoolChecks(lc)...)
	out = append(out, doctorRoutingPauseChecks(lc)...)
	return out
}

// doctorRoutingFix is the command that writes what one of RT3's problems asks
// for, so the line a person reads does not send them to a second reading of the
// sentence above it. The table is the project's own file and one command writes
// an entry of it; a subscription token in no pool is the one problem that is not
// the table's, and the pool is what has to hold it.
func doctorRoutingFix(p routing.Problem) string {
	if p.Code == routing.CodeLoneSubscription {
		return selfCommand() + " accounts pool add NAME [MEMBER...] (a pool may be a pool of one)"
	}
	return selfCommand() + " routing set (writes the entry this line is about)"
}

// doctorRoutingBundleChecks is whether this installation's bundle holds the
// secret each source of the table is served by, for the repositories that source
// admits.
//
// **Only the kinds whose secret is one entry of the bundle are judged here** —
// the project's Anthropic key and a provider key — and each is asked through the
// service's own reader (credsvc.ReadProviderKey), which is the accessor the
// servicing path itself reads (credsvc's planSource, ownNamedEntry): the entry
// one repository's credential is held under is the family's own rule
// (RepoCredentialKey) and this check derives no spelling of it. A pool source's
// secret is one of its members', and which of them the bundle holds is the pool
// reader's answer (poolOfferingOf), which takes the published policy and is
// unexported: re-deriving it here would be a second reading of what a bundle key
// means, which is the one thing this check may not add.
//
// The bundle document is read, and that read is a person's, opt-in and named:
// it is the flag doctor already reads it behind (`--check-bitbucket-token`,
// refused in a coding agent's session before anything is opened), and a run
// without it gets the unread line that names the flag rather than an answer
// nobody asked for. That container is the service's every credential, so
// opening it on an ordinary run would be a read the owner never opted into;
// what the check compares is names, and what its lines carry is names — no
// value is printed, put in an error or written anywhere.
//
// A source whose entry is missing is not an error of the config: the source is
// inert for that repository — it serves nothing and the call falls to the
// chain's next source (D3180) — which is exactly what an owner cannot see from
// either side alone, and why the report is a warning rather than a measurement.
func doctorRoutingBundleChecks(ctx context.Context, lc *localcfg.Config, repos []string, optedIn bool) []doctorCheck {
	// need is one source of the table, over one repository it admits, with the
	// entry the bundle has to hold for it.
	type need struct {
		repo       string
		slug       string
		credential string
		// secret is the name `fugaro secrets set` takes for this credential —
		// a provider's own `.secret`, the project's key secret
		// (config.AnthropicSecret) for the Anthropic kind — which is not the
		// axis the bundle holds it under: a provider's entry is keyed by the
		// provider's name (credsvc.RepoCredentialKey, the rule the writer and
		// the loader share) and the command resolves the key by its own
		// secret's name (config.ProviderBySecret, its .secret). A source whose
		// secret name this config does not state names no usable command.
		secret string
		// named is the credential as a person knows it (the project's Anthropic
		// key, a provider's key by its name), never the account axis's own value.
		named  string
		claims routing.Source
	}
	var needs []need
	for _, repo := range repos {
		slug, err := task.Slug(lc.Repos[repo].Provider, repo)
		if err != nil {
			// No git provider recorded for this repository, so there is no slug
			// and no entry name to ask the bundle about: its sources are not
			// judged rather than asked with a name invented here (`fugaro
			// update` writes the provider into this config).
			continue
		}
		for _, e := range lc.ModelRouting {
			for _, s := range e.Chain {
				var credential, secret string
				switch s.Kind {
				case routing.KindAnthropic:
					credential, secret = config.AnthropicKeyAccount, config.AnthropicSecret
				case routing.KindProvider:
					p, ok := lc.Providers[s.Provider]
					if !ok {
						// The table names a provider this config does not
						// declare, so there is no key to enter and no name to
						// enter it under: the table's own check reports that
						// entry, and a line here would name a command that
						// cannot be run.
						continue
					}
					credential, secret = s.Provider, p.Secret
				default:
					continue
				}
				if !infra.ChainSourceServes(s, repo) {
					continue
				}
				needs = append(needs, need{repo: repo, slug: slug, credential: credential,
					secret: secret, named: doctorRoutingCredentialName(s), claims: s})
			}
		}
	}
	if len(needs) == 0 {
		return nil
	}
	what := fmt.Sprintf("the table goes out on %d credential(s) of one repository each", len(needs))
	unreadable := func(why, fix string) []doctorCheck {
		return []doctorCheck{{ID: doctorRoutingBundleID, Severity: "info", unread: true,
			Problem: fmt.Sprintf("%s, and %s: whether this installation's bundle holds them could not be checked", what, why),
			Fix:     fix}}
	}
	readWhat := "or read the document's names yourself: " + selfCommand() + " creds ls"
	// The document is opened only where a person asked for it: an ordinary run
	// reads no bundle at all, and says which flag answers the question.
	if !optedIn {
		return unreadable("the bundle is read only where a person asks for it, and this run did not",
			"rerun doctor at your own terminal with "+checkBitbucketTokenFlag+" to read it, "+readWhat)
	}
	if err := credsOwnerOnly(os.Getenv, "read"); err != nil {
		return unreadable("the bundle was not read ("+oneLineCLI(err.Error())+")",
			"rerun doctor at your own terminal, with the bundle's accessor available, "+readWhat)
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return unreadable("the bundle ("+oneLineCLI(err.Error())+")",
			"rerun doctor at your own terminal, with the bundle's accessor available, "+readWhat)
	}
	if err := store.check(ctx); err != nil {
		return []doctorCheck{{ID: doctorRoutingBundleID, Severity: "warning",
			Problem: fmt.Sprintf("%s, and this installation has no bundle container (%s): every one of those sources is served nothing, and the call falls to the chain's next source", what, pluginwire.Printable(store.id)),
			Fix:     selfCommand() + " creds set NAME (the bundle's only writer: no update writes a credential)"}}
	}
	meta, err := store.newest(ctx)
	switch {
	case errors.Is(err, errNoBundleVersion):
		return []doctorCheck{{ID: doctorRoutingBundleID, Severity: "warning",
			Problem: fmt.Sprintf("%s, and this installation's bundle (%s) holds no version: every one of those sources is served nothing, and the call falls to the chain's next source", what, pluginwire.Printable(store.id)),
			Fix:     selfCommand() + " creds set NAME (the bundle's only writer: no update writes a credential)"}}
	case err != nil:
		return unreadable("the bundle's newest version ("+oneLineCLI(err.Error())+")",
			"rerun doctor at your own terminal, with the bundle's accessor available, "+readWhat)
	}
	if meta.state != "ENABLED" {
		return []doctorCheck{{ID: doctorRoutingBundleID, Severity: "warning",
			Problem: fmt.Sprintf("%s, and the newest version of the bundle (%s, version %s) is %s: Secret Manager will not serve it, so every one of those sources is served nothing", what, pluginwire.Printable(store.id), pluginwire.Printable(meta.number), pluginwire.Printable(meta.state)),
			Fix:     selfCommand() + " creds set NAME writes a new version (no update writes a credential)"}}
	}
	doc, err := store.access(ctx, meta.number)
	if err != nil {
		return unreadable("the bundle's document ("+oneLineCLI(err.Error())+")",
			"rerun doctor at your own terminal, with the bundle's accessor available, "+readWhat)
	}
	b, err := credsvc.ParseBundle(doc.raw)
	if err != nil {
		return unreadable("the document is not one the service accepts: "+oneLineCLI(err.Error()),
			"rerun doctor at your own terminal, with the bundle's accessor available, "+readWhat)
	}
	var out []doctorCheck
	for _, n := range needs {
		if _, ok := credsvc.ReadProviderKey(b, n.slug, n.credential); ok {
			continue
		}
		out = append(out, doctorCheck{ID: doctorRoutingBundleID, Severity: "warning",
			Problem: fmt.Sprintf("this project's table is served on %s for %s, and this installation's bundle holds no such credential: a run served by that source is served nothing, and the call falls to the chain's next source", n.named, pluginwire.Printable(n.repo)),
			Fix:     doctorRoutingBundleFix(n.claims, n.repo, n.secret)})
	}
	if len(out) > 0 {
		return out
	}
	return []doctorCheck{{ID: doctorRoutingBundleID, OK: true, Severity: "info",
		Problem: fmt.Sprintf("%s, and this installation's bundle holds every one of them", what),
		Fix:     "nothing to run for this: a source is served by the entry the bundle holds for its repository, and every source of this table has one"}}
}

// doctorRoutingCredentialName spells one source's credential as the owner knows
// it, and never as the account axis's own value (config.AnthropicKeyAccount):
// the two kinds here are the project's Anthropic key and a provider's, and the
// provider's own name is the one its entry in this config is written under.
func doctorRoutingCredentialName(s routing.Source) string {
	if s.Kind == routing.KindProvider {
		return fmt.Sprintf("the %s key", pluginwire.Printable(s.Provider))
	}
	return "the project's Anthropic key"
}

// doctorRoutingBundleFix is the command that puts one source's credential into
// the bundle, which is the fix for the state the line above it reports. The two
// kinds are written by two different writers, and each line names the one that
// writes it: a provider's key is entered per repository by `fugaro secrets set`,
// and a repository's own Anthropic key is copied into the bundle by `fugaro
// update` — out of the repository's OWN container and no other
// (copyRepositoryKeys), so an update copies nothing for a repository that holds
// no key of its own and the line has to name storing one first. The name
// `secrets set` takes is the provider's own secret's name and not the
// provider's (config.ProviderBySecret, which is the lookup its own name check
// makes), so the line names the secret the config stores that key under rather
// than a name the command would refuse.
func doctorRoutingBundleFix(s routing.Source, repo, secret string) string {
	set := selfCommand() + " secrets set " + quoteWord(secret) + " --repo " + quoteWord(repo)
	if s.Kind == routing.KindProvider {
		return set
	}
	return set + " stores this repository's own key, and then " + selfCommand() + " update copies it into the bundle"
}

// doctorRoutingLauncherPoolChecks is a chain that goes out on the launcher's
// pool where this project declares no pool at all.
//
// A launcher-pool source names no pool in the table: which pool serves is
// decided per run, from the run's launcher and the pool its launch named, out of
// the designations (D3176, D3180, and the store PL4 ships). doctor reads no
// designation — the shape is C2's, the store is not written yet — so what it can
// answer is the state that makes one impossible: a designation names a pool this
// project must declare, and a project with no pool block has none for anyone to
// designate, so that source is skipped on every run of every launcher and the
// call falls to the chain's next source. The state where the project declares
// pools and nobody has designated one is the one this check cannot see, and it
// says so by staying silent rather than by guessing.
func doctorRoutingLauncherPoolChecks(lc *localcfg.Config) []doctorCheck {
	names := 0
	for _, e := range lc.ModelRouting {
		for _, s := range e.Chain {
			if s.Kind == routing.KindLauncherPool {
				names++
			}
		}
	}
	if names == 0 || len(lc.Pools) > 0 {
		return nil
	}
	return []doctorCheck{{ID: doctorRoutingLauncherPoolID, Severity: "warning",
		Problem: fmt.Sprintf("the table goes out on the launcher's pool in %d place(s), and this project declares no pool at all: no launcher has one to designate, so that source is skipped on every run and the call falls to the chain's next source", names),
		Fix:     selfCommand() + " accounts pool add NAME [MEMBER...] declares the project's pools"}}
}

// doctorRoutingPauseChecks is a table that can pause where nothing would resume
// it.
//
// A pool is a subscription's home (RT3's own reading of a chain, R17): it is the
// kind that runs out at a reset rather than at a balance, so it is the kind a
// run pauses on — and a pause is resumed by the resident orchestrator, which an
// installation records once `fugaro update` has given it one (D3184, D3185).
// Without that record a run whose pools are exhausted halts with the reset in
// its reason instead of pausing (D3186), which is a run that stops where it
// could have waited: the line names the state and the one command that changes
// it.
//
// An installation that cannot carry an orchestrator at all — no credentials
// service recorded, no Firebase backend for one to read — is the same fact with
// no command behind it, so it is information and never a warning: an update
// would only repeat what it says.
func doctorRoutingPauseChecks(lc *localcfg.Config) []doctorCheck {
	subs := 0
	for _, e := range lc.ModelRouting {
		for _, s := range e.Chain {
			if s.Kind == routing.KindPool || s.Kind == routing.KindLauncherPool {
				subs++
			}
		}
	}
	if subs == 0 || orchestratorBlockHeld(lc.GCPProject) {
		return nil
	}
	problem := fmt.Sprintf("the table goes out on a pool in %d place(s), and this installation records no resident orchestrator: a run whose pools are exhausted ends with the reset in its reason instead of pausing until it, because nothing would resume what it recorded", subs)
	if !orchestratorCarryable(lc) {
		return []doctorCheck{{ID: doctorRoutingPauseID, Severity: "info",
			Problem: problem + ", and this installation has no " + orchestratorMissing(lc) + " for one to use",
			Fix:     "no action: fugaro update gives the installation a resident orchestrator once it has both, and says on every run what is still missing"}}
	}
	return []doctorCheck{{ID: doctorRoutingPauseID, Severity: "warning",
		Problem: problem,
		Fix:     "run fugaro update: it writes the resident orchestrator's block and applies it, and nothing else is needed"}}
}

// orchestratorCarryable reports whether this installation records both halves a
// resident orchestrator is made of — the credentials service it is reached
// through and the Firebase backend its process reads — which is the pair
// doctor's own identity check reads (doctorOrchestratorIdentity) to tell a block
// an update has yet to write from an installation that cannot carry one at all.
func orchestratorCarryable(lc *localcfg.Config) bool {
	return lc.CredentialsService != nil && lc.Budget != nil &&
		lc.Budget.FirebaseProject != "" && lc.Budget.RTDBURL != ""
}
