// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/agent"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is the settle command (D3 of
// docs/forge-authorship-images/implementation-plan.md): the entry point the
// coordinator's own step runs through — by hand today, from any job wired to run
// it later. The decision it makes lives in ee/coordinator/changeset.go; what is
// here is the environment — the runs bucket the members' records come out of,
// and the settle-only token (D641) each member's pull request is written with.

// coordinatorSettleTimeout bounds one settle. A set is at most five members
// (task.MaxChangeSetMembers), each costing a handful of calls to one host, so
// this is generous; it exists so a host that answers none of them cannot hold
// the command.
const coordinatorSettleTimeout = 2 * time.Minute

// coordinatorSettleTest points the command at a step of the test's own. Tests
// only: the mint and the opener below are the real ones, and a test that wants
// to drive the command without a host replaces this.
var coordinatorSettleTest *coordinator.Settler

// coordinatorSettleProvider opens the GitHub adapter the settle writes through:
// the App credential this process holds, on the API root this code pins. It is
// a seam for the same reason coordinatorGitHubReadProvider is — a test holds
// the root without a network — and it is separate from that one because the two
// are two credentials: the read is keyless and minted at the service, and this
// one mints its own token from the App's key.
var coordinatorSettleProvider = func(o github.Options) (gitprov.Provider, error) {
	return github.New(o)
}

func newCoordinatorSettleCmd() *cobra.Command {
	var o coordinatorSettleOptions
	cmd := &cobra.Command{
		Use:   "settle",
		Short: "Make a change set's pull requests ready together",
		Long: "Settle one change set: read every member's records and every member's pull\n" +
			"request as the host has it now, and — only when every member has shipped, with\n" +
			"passing tests on the commit its pull request is at — make every pull request\n" +
			"ready, through the settle-only token.\n\n" +
			"A change set is one piece of work spanning several repositories: a run per\n" +
			"repository, launched together, each opening its own pull request. A member's\n" +
			"run never makes its own pull request ready, because a set's pull requests are\n" +
			"read together; this is the step that lifts that hold, and it is the only thing\n" +
			"that does.\n\n" +
			"--change-set names the set, which is the id of the run of its FIRST member, and\n" +
			"--repo names that member's repository (the default is the local config's, when\n" +
			"it lists one): the two together are what the member list is read from. Every\n" +
			"other member, its repository and its run are read from that list, and a member\n" +
			"whose task lists another set holds the whole set back (membership is fixed when\n" +
			"a set is launched).\n\n" +
			"Nothing is written until every member has been read and found shippable, so a\n" +
			"set with one member still working is left exactly as it was, and the report says\n" +
			"which member holds it. It is idempotent: a member whose pull request is already\n" +
			"ready counts as ready, so a settle that flipped some of a set and then met a\n" +
			"failure is finished by the next one rather than undone.\n\n" +
			"The credential is the settle-only one: an installation token scoped to the\n" +
			"members' repositories, carrying pull requests write and nothing else, minted\n" +
			"where the App's key is and taken back when the command ends. A repository whose\n" +
			"provider is not GitHub is reported rather than written to.\n\n" +
			"Exit codes: 0 whether or not the set settled — the report is the answer, and a\n" +
			"set that is merely waiting is not a failure — 1 when the set named cannot be\n" +
			"read (no such run, a run in no change set), and 2 for a failure of the host.\n\n" +
			"Nothing runs this on a schedule yet: the coordinator job's tick is `fugaro\n" +
			"coordinator sweep`, which writes no pull request, so a change set's pull\n" +
			"requests stay drafts until this command is run — by hand, in an environment\n" +
			"holding the App's key (the coordinator job's own holds it where the sweep\n" +
			"reads GitHub), or from a job wired to run it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), coordinatorSettleTimeout)
			defer cancel()
			return runCoordinatorSettle(ctx, cmd, &o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.set, "change-set", "", "the change set to settle (the id of its first member's run)")
	f.StringVar(&o.repo, "repo", "", "the repository of the set's first member (owner/name; default: the local config's, when it lists one)")
	f.BoolVar(&o.asJSON, "json", false, "print the settle's report as JSON")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

// coordinatorSettleOptions are the settle command's own flags.
type coordinatorSettleOptions struct {
	cloud  fcli.CloudFlags
	set    string
	repo   string
	asJSON bool
}

func runCoordinatorSettle(ctx context.Context, cmd *cobra.Command, o *coordinatorSettleOptions) error {
	if o.set == "" {
		return fcli.UserErr("settle needs --change-set: the change set to settle is the id of the run of its first member")
	}
	if !task.ValidRunID(o.set) {
		return fcli.UserErr("--change-set %q must look like 20260926-221530-a1b2", o.set)
	}
	env, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	repo, err := coordinatorSettleRepo(env, o)
	if err != nil {
		return err
	}
	set, err := coordinatorSettleSet(ctx, env, repo, o.set)
	if err != nil {
		return err
	}
	warn := cmd.ErrOrStderr()
	settler, tokens, err := coordinatorSettlerFor(ctx, env, set, warn)
	if err != nil {
		return err
	}
	// Every token this settle minted is taken back when the command is done, on
	// every return and whatever happened on the way: the credential lives for
	// the write and no longer (github.RevokeInstallationToken).
	defer tokens.revoke(ctx)

	rep, err := settler.Settle(ctx, set)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	out := cmd.OutOrStdout()
	if o.asJSON {
		return writeCoordinatorJSON(out, rep)
	}
	return writeCoordinatorSettle(out, rep)
}

// coordinatorSettleRepo is the repository whose run names the set: --repo's,
// else the local config's single one. It is the same selection the status
// command makes, and it is one repository and not the whole project because a
// change set is read from the run that names it: the set's id is that run's id,
// and the run lives under its own repository's prefix.
func coordinatorSettleRepo(env *fcli.Cloud, o *coordinatorSettleOptions) (string, error) {
	if o.repo != "" {
		return o.repo, nil
	}
	repos := env.Config().Repos
	if len(repos) == 1 {
		for name := range repos {
			return name, nil
		}
	}
	return "", fcli.UserErr("the local config lists %d repositories; name the repository of the set's first member with --repo", len(repos))
}

// coordinatorSettleSet reads the set out of the run that names it: the task at
// runs/<slug>/<set id>/task.json, whose change set is the whole set — every
// member, its repository and its run (D642).
func coordinatorSettleSet(ctx context.Context, env *fcli.Cloud, repo, set string) (*task.ChangeSet, error) {
	slug, err := env.RepoSlug(ctx, repo)
	if err != nil {
		return nil, err
	}
	spec, err := runstore.Open(env.RunsBucket().Bucket, slug, set).ReadTask(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil, fcli.UserErr("no run %s of %s is in the runs bucket: the change set's id is the id of the run of its FIRST member, so --repo names that member's repository", task.ShortRun(set), safetext.OneLine(repo))
	}
	if err != nil {
		return nil, fcli.RemoteErr(err)
	}
	if spec.ChangeSet == nil || spec.ChangeSet.ID != set {
		return nil, fcli.UserErr("run %s of %s is in no change set, so there is nothing to settle (the set's id is the id of the run of its first member)", task.ShortRun(set), safetext.OneLine(repo))
	}
	return spec.ChangeSet, nil
}

// coordinatorSettlerFor builds the step the command runs: the member reader over
// the runs bucket, and the opener that mints the settle-only token per owner and
// installs it on the member's provider.
//
// The App credential is asked for here, before anything is read, the way the
// reader's own opener asks for it: a settle that cannot mint is the
// environment's failure and never a member's, so it is the caller's own refusal
// (exit 1) and it comes before the report rather than as a row of it. A settle
// of a set that turns out not to need the credential pays one environment read
// for that, and no credential is minted until a member reaches the host
// (coordinator.Settler.Open).
func coordinatorSettlerFor(ctx context.Context, env *fcli.Cloud, set *task.ChangeSet, warn io.Writer) (*coordinator.Settler, *coordinatorSettleTokens, error) {
	if coordinatorSettleTest != nil {
		return coordinatorSettleTest, &coordinatorSettleTokens{}, nil
	}
	members, err := coordinatorSettleMembers(set)
	if err != nil {
		return nil, nil, err
	}
	appID, key, pemData, err := coordinatorAppCredential()
	if err != nil {
		return nil, nil, err
	}
	tokens := &coordinatorSettleTokens{appID: appID, key: key, pem: pemData, members: members, byOwner: map[string]string{}}
	return &coordinator.Settler{
		Member: func(ctx context.Context, repo, runID string) (coordinator.MemberReads, error) {
			return coordinatorSettleMember(ctx, env, repo, runID)
		},
		Open: func(ctx context.Context, repo string) (coordinator.SettleProvider, error) {
			return tokens.open(ctx, env, repo, warn)
		},
	}, tokens, nil
}

// coordinatorSettleMember reads one member run's records out of the runs
// bucket: the task it was launched with (which carries the set, D642), the
// changeset.json it recorded and its result.json (D2).
//
// A run that recorded no changeset.json is not a failure: the step reports the
// member rather than the read (changeset.json is written once the run has a
// pull request, and a run that ended before that has none).
func coordinatorSettleMember(ctx context.Context, env *fcli.Cloud, repo, runID string) (coordinator.MemberReads, error) {
	slug, err := env.RepoSlug(ctx, repo)
	if err != nil {
		return coordinator.MemberReads{}, err
	}
	st := runstore.Open(env.RunsBucket().Bucket, slug, runID)
	spec, err := st.ReadTask(ctx)
	if err != nil {
		return coordinator.MemberReads{}, err
	}
	rec, err := st.ReadRecord(ctx)
	if err != nil {
		return coordinator.MemberReads{}, err
	}
	member, err := st.ReadChangeSetMember(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		member = nil
	} else if err != nil {
		return coordinator.MemberReads{}, err
	}
	return coordinator.MemberReads{Task: spec, Member: member, Run: rec}, nil
}

// coordinatorSettleMembers is the set's members grouped by the owner that holds
// them, each owner's as the repository NAMES its installation token is scoped
// to (github.MintSettleToken). A set is launched with canonical owner/name
// repositories and may span owners; a member that is not an owner/name pair is
// refused here rather than sent to a mint that would answer a token covering
// less than the set.
func coordinatorSettleMembers(set *task.ChangeSet) (map[string][]string, error) {
	members := make(map[string][]string, 1)
	for _, m := range set.Members {
		owner, name, ok := gitprov.SplitRepo(m.Repo)
		if !ok {
			return nil, fcli.UserErr("change set %s names %q, which is not an owner/name repository", task.ShortRun(set.ID), safetext.OneLine(m.Repo))
		}
		members[owner] = append(members[owner], name)
	}
	return members, nil
}

// coordinatorSettleTokens is the settle's own credential: one settle-only token
// per OWNER of the set's members (D641), minted from the App key this process
// holds, scoped to the members that owner holds and to nothing else.
//
// Per owner and not per member, because that is the granularity GitHub's
// endpoint has: a token is minted at one installation and covers the
// repositories the request names. A set whose members are all in one owner — the
// shape a launch produces — mints once for the whole set, and a member of
// another owner is written with the token minted for THAT owner's members, so no
// member is ever written with a credential covering a repository the set does
// not name.
//
// It is deliberately not the coordinator's read mint (coordinatorMint): that one
// asks for the read-only set, one repository at a time, and a settle needs the
// write D641 grants it and nothing else.
type coordinatorSettleTokens struct {
	appID   string
	key     *rsa.PrivateKey
	pem     string
	members map[string][]string // owner -> the set's member names within it
	mu      sync.Mutex
	byOwner map[string]string
	// minted is every token this command minted, in mint order, for the revoke
	// at the end (coordinatorTokens' own rule).
	minted []string
}

// source is the TokenSource one member's provider is opened with: the token
// minted for that member's owner, minted once and kept for the command's life.
func (t *coordinatorSettleTokens) source(ctx context.Context, owner string) (string, error) {
	t.mu.Lock()
	tok, ok := t.byOwner[owner]
	t.mu.Unlock()
	if ok {
		return tok, nil
	}
	token, _, err := github.MintSettleToken(ctx, github.Options{
		Owner:      owner,
		AppID:      t.appID,
		PrivateKey: t.key,
		BaseURL:    coordinatorGitHubAPIURL,
	}, t.members[owner])
	if err != nil {
		return "", fcli.RemoteErr(errors.New(agent.Redact(err.Error(), []string{t.pem})))
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if tok, ok := t.byOwner[owner]; ok {
		// Another member of the same owner minted while this one was in flight:
		// the token that got there first is the one kept, and this one is taken
		// back rather than left to expire.
		go t.revokeOne(context.WithoutCancel(ctx), token)
		return tok, nil
	}
	t.byOwner[owner], t.minted = token, append(t.minted, token)
	return token, nil
}

// open opens one member repository's provider with its owner's settle token
// installed on it, and refuses a repository the settle does not write to.
//
// The two refusals come first, before anything is minted: a member whose
// provider is not GitHub (the settle token is a GitHub installation token and
// there is nothing to widen it to), and a member the set does not name (a token
// is scoped to the members' repositories, and a repository outside the set is
// not one of them).
func (t *coordinatorSettleTokens) open(ctx context.Context, env *fcli.Cloud, repo string, warn io.Writer) (coordinator.SettleProvider, error) {
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok {
		return nil, fcli.UserErr("repository %q must look like owner/name", repo)
	}
	if !slices.Contains(t.members[owner], name) {
		return nil, fcli.UserErr("%s is not a member of the change set being settled, so the settle holds no token for it", safetext.OneLine(repo))
	}
	if kind := env.RepoProvider(repo, fcli.CheckoutOf(ctx, repo)()); kind != gitprov.KindGitHub {
		return nil, fcli.UserErr("the settle makes pull requests ready on github only: %s is %s", safetext.OneLine(repo), safetext.OneLine(kind))
	}
	token, err := t.source(ctx, owner)
	if err != nil {
		return nil, err
	}
	p, err := coordinatorSettleProvider(github.Options{
		Owner: owner, Repo: name, AppID: t.appID, PrivateKey: t.key,
		BaseURL: coordinatorGitHubAPIURL, Warn: func(s string) { fmt.Fprintln(warn, safetext.MultiLine(s)) },
	})
	if err != nil {
		return nil, err
	}
	if err := installCoordinatorSource(p, fixedToken(token)); err != nil {
		return nil, err
	}
	return p, nil
}

// revoke takes back every token this settle minted, best-effort and silently,
// the way the read's own revoke does: a revoke that failed leaves a token to
// expire within the hour, and the settle that used it has already answered.
func (t *coordinatorSettleTokens) revoke(ctx context.Context) {
	if t == nil {
		return
	}
	t.mu.Lock()
	tokens := slices.Clone(t.minted)
	t.mu.Unlock()
	for _, tok := range tokens {
		t.revokeOne(ctx, tok)
	}
}

func (t *coordinatorSettleTokens) revokeOne(ctx context.Context, token string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinatorRevokeTimeout)
	defer cancel()
	_ = github.RevokeInstallationToken(rctx, coordinatorGitHubAPIURL, token, nil)
}

// fixedToken is a TokenSource holding one already-minted token: the settle mints
// per owner itself (source), and the provider it opens must not mint a second
// one of its own.
type fixedToken string

func (f fixedToken) Token(context.Context, time.Duration) (string, time.Time, error) {
	return string(f), time.Time{}, nil
}

// writeCoordinatorSettle prints one settle's report: the set, whether it is
// settled, the set's advisory merge order and one line per member saying what
// happened to it. It is a report and never advice: a member that is not ready is
// named with the reason the step found, and what to do about it is the reader's.
//
// The merge order is the set's own and is stated for the reader who sees the
// whole set here and none of its pull requests: each member's body carries the
// same order (gitprov.ChangeSetLine), and this is the coordinator's view of the
// set stating it (D4). Nothing enforces it (D615).
func writeCoordinatorSettle(w io.Writer, rep coordinator.SettleReport) error {
	head := "not settled"
	if rep.Settled {
		head = "settled"
	}
	if _, err := fmt.Fprintf(w, "change set %s: %s (%d of %d members flipped ready)\n",
		task.ShortRun(rep.SetID), head, rep.Flipped, len(rep.Members)); err != nil {
		return err
	}
	if len(rep.MergeOrder) > 0 {
		repos := make([]string, 0, len(rep.MergeOrder))
		for _, repo := range rep.MergeOrder {
			repos = append(repos, safetext.OneLine(repo))
		}
		if _, err := fmt.Fprintf(w, "merge order (advisory): %s — merge in this order; nothing blocks a merge\n",
			strings.Join(repos, ", then ")); err != nil {
			return err
		}
	}
	for _, m := range rep.Members {
		var what string
		switch {
		case m.Why != "":
			what = "not ready: " + safetext.OneLine(m.Why)
		case m.Flipped:
			what = "ready (flipped)"
		default:
			what = "ready (already ready)"
		}
		if _, err := fmt.Fprintf(w, "  %s %s  %s\n", safetext.OneLine(m.Repo), task.ShortRun(m.RunID), what); err != nil {
			return err
		}
		if m.Note != "" {
			if _, err := fmt.Fprintf(w, "    %s\n", safetext.OneLine(m.Note)); err != nil {
				return err
			}
		}
	}
	return nil
}
