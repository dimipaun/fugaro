package cli

// The daemon's issue-sync seams (B4, D2718): what `fugaro orchestrator run`
// hands its tick so that the standing backlog follows the open issues of the
// repositories `issue_sync` names.
//
// None of the sync's own rules is restated here — what is ingested, what a
// mirror marker means, which item adopts which issue are B4's
// (internal/issuesync). What this file answers is the three things only a
// running daemon has: the client each repository's issues are read and opened
// through, the published `issue_sync` block that names those repositories, and
// the standing backlog's node the patch lands on. The host behind the client is
// the installation's credentials service and not GitHub, because the service is
// the one holder of the App key (D2439): the daemon asks it for every listing
// and every creation, and no token is minted here at all.
//
// All three or none, as IssueSyncSeams.Set asks. None is the answer where the
// installation has nothing to reach the route with — no credentials service URL,
// or no daemon account to mint a token as because the installation's tfvars
// carry no orchestrator block (orchestratorIdentityToken's nil) — and where the
// project publishes no `issue_sync` block that decides anything. A tick with no
// seams ingests nothing and says nothing about it, which is the state every
// installation that has not opted in is in. A daemon that HAS both and cannot
// reach one repository is a different thing and never this: the refusal travels
// back through the client and the sync writes it as one line per cadence,
// because a daemon silently ingesting nothing with `issue_sync` published would
// be the failure nobody reads.

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/issuesync"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// orchestratorIssueSeams is the issue-sync seams this daemon's tick carries: the
// sync over one client per repository `issue_sync` names, the standing backlog's
// node the patch lands on, and the log the sync's failures go to.
//
// The seams are built once, when the daemon starts, and not per cycle: they are
// the same shape as every other seam of this daemon (the labels, the triage, the
// decision agent), and a repository's client holds nothing that changes. What
// that costs is stated rather than hidden: a project that publishes `issue_sync`
// — or removes a repository from it — after the daemon started is one the next
// daemon start picks up, so a project that turns the ingest off keeps reading the
// repositories it named until then. The guardrails are the one reading this
// daemon repeats every cycle (refreshCaps), because a person's stop has to bite
// at once; a repository set is not a stop.
func (d *orchestratorDaemon) orchestratorIssueSeams(ctx context.Context) (orchestrator.IssueSyncSeams, error) {
	id := orchestratorIdentityOf(d.lc)
	identity := orchestratorIdentityToken(id)
	if identity == nil {
		return orchestrator.IssueSyncSeams{}, nil
	}
	published, _, err := projectPolicyRead(ctx, d.lc)
	if err != nil {
		return orchestrator.IssueSyncSeams{}, err
	}
	if published == nil || published.IssueSync.SetsNothing() {
		return orchestrator.IssueSyncSeams{}, nil
	}
	blk := published.IssueSync.GitHub
	adapters := make(map[string]issuesync.Adapter, len(blk.Repos))
	appIDs := make(map[string]string, len(blk.Repos))
	for _, repo := range blk.Repos {
		appID, err := d.issueAppID(repo)
		if err != nil {
			// One repository whose client cannot be built does not take the
			// others down with it: the sync walks the repositories it was
			// handed, and this one's adapter answers both of its acts with the
			// refusal, so the line is written per cadence — which is what tells
			// a person a repository they named is not being read — rather than
			// once at start-up or not at all.
			adapters[repo] = serviceIssueAdapter{err: err}
			continue
		}
		adapters[repo] = serviceIssueAdapter{issues: &creds.ServiceIssues{
			URL: id.url, Repository: repo, AppID: appID, IDToken: identity,
		}}
		appIDs[repo] = appID
	}
	backlog, err := orchestrator.NewNode(orchestrator.NodeOptions{
		DB: d.db, AgentID: d.id, Role: orchestrator.RoleOrchestrator,
		Shard: orchestrator.ShardBacklog, Lease: d.lease,
	})
	if err != nil {
		return orchestrator.IssueSyncSeams{}, userErr("%v", err)
	}
	log := orchestratorDaemonLog{id: d.id, w: d.warn}
	return orchestrator.IssueSyncSeams{
		Sync: &issuesync.Runner{
			Adapters: adapters, AppIDs: appIDs, Mirror: blk.MirrorOn(), Log: log,
		},
		Backlog: backlog,
		Log:     log,
	}, nil
}

// issueAppID is the GitHub App this installation recorded for one repository:
// the id the credentials service signs the call as, and the one value the sync
// holds an issue's own App to before a mirror marker counts (D2728).
//
// A repository this installation records as something other than a GitHub
// repository, or one it records no App for, is refused here rather than reached
// for: the route serves GitHub and mints as an App, so a call that named none is
// one the service could not make, and the honest answer is a line per cadence
// rather than a call the service refuses every time.
func (d *orchestratorDaemon) issueAppID(repo string) (string, error) {
	if d.env.repoProvider(repo, nil) != gitprov.KindGitHub {
		return "", fmt.Errorf("this installation records %s as no GitHub repository, and the issue route reads and opens GitHub issues alone", safetext.Field(repo))
	}
	r, ok := d.env.localRepo(repo)
	if !ok || r.GitHubAppID == "" {
		return "", fmt.Errorf("this installation records no GitHub App ID for %s, and the credentials service signs the token it reads and opens an issue with as that App", safetext.Field(repo))
	}
	return r.GitHubAppID, nil
}

// serviceIssueAdapter is one repository's credentials-service client as B4's
// seam (issuesync.Adapter): the route's shape adapted to the sync's.
//
// The two shapes are one reading of an issue and the App ID passes through
// unchanged, because the sync is what decides whose App opened an issue (D2728)
// and it can only decide that with the id the host reported.
//
// err is set where no client could be built at all — a repository this
// installation records no GitHub App for — and both acts then answer it: the
// sync writes one line per cadence and ingests nothing for that repository,
// which is the direction a repository whose reads cannot be signed fails in.
type serviceIssueAdapter struct {
	issues creds.Issues
	err    error
}

var _ issuesync.Adapter = serviceIssueAdapter{}

// ListOpen implements issuesync.Adapter.
func (a serviceIssueAdapter) ListOpen(ctx context.Context) ([]issuesync.Issue, error) {
	if a.err != nil {
		return nil, a.err
	}
	list, err := a.issues.ListOpen(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]issuesync.Issue, 0, len(list))
	for _, i := range list {
		out = append(out, issueOf(i))
	}
	return out, nil
}

// Open implements issuesync.Adapter.
func (a serviceIssueAdapter) Open(ctx context.Context, title, body string) (issuesync.Issue, error) {
	if a.err != nil {
		return issuesync.Issue{}, a.err
	}
	i, err := a.issues.Open(ctx, title, body)
	if err != nil {
		return issuesync.Issue{}, err
	}
	return issueOf(i), nil
}

// issueOf is one issue as the route answered it, in the sync's own shape. This
// is the only translation between the two, so they are kept one reading: the
// route's body is the host's text and the sync's is the same text, hashed on the
// way into an item and stored nowhere (D2720).
func issueOf(i creds.Issue) issuesync.Issue {
	return issuesync.Issue{
		Number: i.Number, Title: i.Title, Body: i.Body, State: i.State,
		AuthorLogin: i.AuthorLogin, AppSlug: i.AppSlug, AppID: i.AppID,
	}
}
