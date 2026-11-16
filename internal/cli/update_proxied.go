package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// update_proxied.go is the gate the owner's ruling of 2026-11-15 puts in front
// of the flip's drop (O8, docs/secrets-brokering/reconciliation.md §7): the
// apply that gives a proxied repository's jobs up their Bitbucket token — the
// mount and the job account's secretAccessor on it
// (config.ProxiedGitSecretNames, internal/infra's Readiness and Addresses,
// modules/workflow/sa.tf) — must not run while the credentials service's
// bundle holds no token to fetch instead.
//
// It is `fugaro update`'s own step and not a line of operator notes, because
// the HARD RULE says the upgrade is that one command and works perfectly: an
// installation at the previous version converges in one run, and a person who
// has to run `fugaro creds set bitbucket-token` before the re-apply has been
// handed exactly the second command the rule forbids. What replaces the step
// is a refusal that names the one command and nothing else: where the bundle
// already holds the token — every installation whose proxy is in use, since
// the service has nothing to forward with otherwise — the run says nothing and
// converges, and where it does not, the drop is refused and the repository
// keeps its mount until the person stores the credential at their own terminal.
//
// Nothing here is a confirmation and nothing waits: the refusal is a budget of
// its own (docs/no-prompt-policy), printed with the fix and no question.
//
// The bundle is read with the operator's own identity and refused under either
// marker (credsOwnerOnly, internal/cli/creds.go), so in a coding agent's
// session and in a Fugaro run's own process there is no reading to judge — and
// there the gate warns what the drop costs and lets the run go on, which is
// what credentialsBundleReady does with the same read. Refusing there instead
// would stop an ordinary `fugaro update` in a coding agent's session over a
// question that session may not answer at all, and the drop is one a person
// can put back by storing the token and applying the root again.

// refuseTheProxiedDrop is repoEngine's gate, called once the spec of the root
// this run is about to apply exists and before any cloud call of that apply:
// the credentials service's bundle is read (the run's one reading,
// credentialsBundleReady), and a proxied repository's drop is refused while it
// holds no token.
//
// It is judged on the SPEC and not on the local config's or the node's facts
// separately: infra.RepoSpec.DropsBitbucketToken is the plan's own condition —
// the installation opted in, the node admits this repository with proxy: true,
// and config.ProxiedGitSecretNames names the token — so the gate refuses
// exactly the applies that would take the mount away, and a repository the
// service does not serve (a held-off root, a node that does not admit it, a
// keyless installation) is left alone with its mount.
//
// --plan-only is exempt, like the other refusals that stop a CHANGE rather than
// report a contradiction (refuseClonesNotShipped, Readiness's D11 gate): a
// planned-only run applies nothing, so nothing is dropped and there is nothing
// to refuse — and the plan it prints is the plan the applying run refuses until
// the token is stored, which is the run that matters.
func (r *initRun) refuseTheProxiedDrop(ctx context.Context, lc *localcfg.Config, outs infra.InstallationOutputs, spec infra.RepoSpec) error {
	if r.o.planOnly || !spec.DropsBitbucketToken() {
		return nil
	}
	held, read := r.bundleBitbucketToken(ctx, credentialsServiceProbe(lc, outs))
	if !read {
		r.warn(fmt.Sprintf("the credentials service's bundle was not read, so whether it holds a %s credential is unknown, and this apply drops %s's mount of the %s secret and its job account's accessor on it (the published policy admits the repository with proxy: true and the credentials service serves this installation): if the bundle holds no token, a run of the repository is refused when it asks the service for its git credential, with no copy of its own left to read. A person's own %s --check-bitbucket-token reads what the bundle holds",
			config.BitbucketSecret, spec.Name, config.BitbucketSecret, selfCommand()))
		return nil
	}
	if held {
		fmt.Fprintf(r.w, "%s: the credentials service's bundle holds a %s credential, so this apply takes the job's mount of it and its job account's accessor on it away, and the repository's runs fetch their git credential from the service instead\n",
			spec.Name, config.BitbucketSecret)
		return nil
	}
	return userErr("this update would take %s's Bitbucket token away: the published policy admits the repository with proxy: true and the credentials service serves this installation, so the root applied here drops the job's mount of the %s secret and its job account's accessor on it — and the credentials service's bundle holds no %s credential, so nothing would be left to fetch instead and a run of the repository is refused when it asks the service for its git credential. Nothing has been applied. A person stores that credential at their own terminal:\n  %s\nand this command is the one to run after it (%s --check-bitbucket-token reads what the bundle holds today)",
		spec.Name, config.BitbucketSecret, config.BitbucketSecret, selfCommand()+" creds set "+config.BitbucketSecret, selfCommand())
}

// bundleBitbucketToken answers whether the credentials service's bundle holds
// the upstream Bitbucket token, and whether there was a reading to answer from
// at all. It is credentialsBundleReady's own reading — the same store, the same
// newest version, the same credsvc.ParseBundle — asked its other question, and
// the reading is the run's one: memoized there, so this opens no second one and
// the gate and the switch record cannot answer from two documents.
//
// read=false is every state in which the bundle was not opened or not read —
// a coding agent's session, a Fugaro run's own process, a container this
// machine's identity may not read, a document the loader refuses — and it is
// not an answer about the token: the caller warns and goes on rather than
// refusing a change on the word of a read that did not happen. held=false with
// read=true is the fact this gate exists for: an empty container, a newest
// version that is DISABLED, a document that parses without the entry, and a
// local config and installation outputs that name no service at all (which the
// spec's own condition answers before this is reached).
func (r *initRun) bundleBitbucketToken(ctx context.Context, lc *localcfg.Config) (held, read bool) {
	if _, read := r.credentialsBundleReady(ctx, lc); !read {
		return false, false
	}
	return r.credsBitbucket, true
}
