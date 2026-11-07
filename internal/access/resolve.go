package access

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// Input is what the resolver decides against: the stored grants, the implicit
// project admins, the published allow_repos and the licence state. It is given
// rather than read here, so the resolver is a pure function of its arguments
// and one answer is testable without a database, a file or a clock.
type Input struct {
	// Grants are the stored role and scope pairs.
	Grants []Grant
	// Implicit are the members who hold admin at project scope without a
	// stored grant: the GCP project's owners and editors and the budget
	// admins (F17.3, roles.md D689). They are an input — read from the
	// installation's own outputs — and never a stored pair (F27), and they
	// count toward the last-admin rule, so a revoke that would leave only an
	// implicit admin is not a revoke of the last one (F22).
	Implicit []string
	// AllowRepos is the published policy's allow_repos. A scope never admits a
	// repository it does not, because a repository outside it cannot run at
	// all (F27); an empty list is the deny-all case projectpolicy.AdmitRepo
	// documents, and the caller decides whether it is holding a published
	// deny-all policy or no policy at all.
	AllowRepos []string
	// Lapse is the licence-gate input (D1582): true while a subscription has
	// lapsed. No rule in this package reads it — a lapse limits the
	// Subscription surfaces (ee/, RS9), never the MIT core the resolver
	// answers for, so it must not drop anybody's role — and it is carried here
	// so the surface that does limit them has the one input asking for it.
	Lapse bool
}

// Effective returns the role caller holds in repo: the highest role of the
// grants that name them and whose scope covers the repository, or admin for an
// implicit project admin (F17.3).
//
// caller is a grant member ("user:<address>", the spelling Grant.User and
// Caller carry). repo is a canonical repository (projectpolicy.Canonical).
// Anything else — an empty caller, an empty repository, a repository the
// published allow_repos does not admit, a grant whose role or scope this
// package did not parse — resolves to RoleNone rather than to a role, and the
// highest role wins by Rank, never by the order the grants are stored in.
//
// The allow_repos gate comes first, implicit admins included: a repository the
// published policy does not admit cannot run at all, so nobody holds a role in
// it, and an answer of "admin" there would be a role in a repository the
// project does not have (F27).
func Effective(in Input, caller, repo string) Role {
	if caller == "" || repo == "" {
		return RoleNone
	}
	if !projectpolicy.AdmitRepo(in.AllowRepos, repo) {
		return RoleNone
	}
	if isImplicit(in, caller) {
		return RoleAdmin
	}
	best := RoleNone
	for _, grant := range in.Grants {
		if grant.User != caller || !grant.Role.Valid() || !grant.Scope.Valid() {
			continue
		}
		if !scopeCovers(grant.Scope, repo) {
			continue
		}
		if grant.Role.Rank() > best.Rank() {
			best = grant.Role
		}
	}
	return best
}

// Action is one thing a caller may do, named so a command asks about the thing
// it is doing rather than about a role (F19). The set is closed: an Action
// this package does not know is refused, so a command that asks about a typo
// is refused rather than allowed.
type Action string

const (
	// ActionViewRepo reads a repository's runs, its logs and its state.
	ActionViewRepo Action = "view-repo"
	// ActionLaunch starts a run of a repository.
	ActionLaunch Action = "launch"
	// ActionCancel cancels a run of a repository (D1587: an operator cancels
	// within scope, and an ordinary viewer does not cancel at all).
	ActionCancel Action = "cancel"
	// ActionManageRepo changes a repository's own settings, overrides and
	// derived image (within the project layer's guardrails, which the write's
	// own merge enforces — F9).
	ActionManageRepo Action = "manage-repo"
	// ActionViewProject reads the project-level surfaces: spend and members
	// (F19, D1586).
	ActionViewProject Action = "view-project"
	// ActionManageProject changes a project-level surface: the project layer
	// and its guardrails, allow_repos, the image catalog, accounts, the global
	// budget, setup/update/config publish, and the grants of project or
	// all-repos scope (F19; D1591 keeps these to a project-scope admin until
	// RS12).
	ActionManageProject Action = "manage-project"
)

// actionRule is what one action takes: the least role that may do it, and
// whether it is a project-level action — one that only a project-scope role
// holds, because an all-repos admin holds no project-level thing (F19).
type actionRule struct {
	min     Role
	project bool
}

// actionRules is the whole table. It is unexported and complete: Allowed
// refuses an action that is not one of these, and the list is what a reviewer
// reads to see that every action names the role F19 gives it.
var actionRules = map[Action]actionRule{
	ActionViewRepo:      {min: RoleViewer, project: false},
	ActionLaunch:        {min: RoleOperator, project: false},
	ActionCancel:        {min: RoleOperator, project: false},
	ActionManageRepo:    {min: RoleAdmin, project: false},
	ActionViewProject:   {min: RoleViewer, project: true},
	ActionManageProject: {min: RoleAdmin, project: true},
}

// Allowed reports whether caller may do action in repo. A project-level action
// never reads repo (the project is the scope), and a repository action needs a
// canonical repository: an empty one is refused by Effective.
//
// It is the one gate a command asks, so the refusal it prints and the role it
// names come from one place. What it does not do is decide what the caller
// holds in the cloud: a project-wide IAM grant reaches every repository with
// gcloud whatever this answers (D1580, D1591).
func Allowed(in Input, caller string, action Action, repo string) bool {
	rule, known := actionRules[action]
	if !known {
		return false
	}
	if rule.project {
		return effectiveAtProject(in, caller).AtLeast(rule.min)
	}
	return Effective(in, caller, repo).AtLeast(rule.min)
}

// effectiveAtProject is caller's role at project scope: admin for an implicit
// admin, and otherwise the highest role of the grants that name them with the
// project scope (an all-repos or repository grant holds no project-level
// thing, F19).
//
// It gates on Scope.Valid for the same reason Effective does, and it is the
// half where skipping it would matter most: a grant whose kind is ScopeProject
// but whose value this package did not parse (a project scope carrying
// repositories) would otherwise read as project admin, which is the role that
// hands out every other. The kind alone is not the scope.
func effectiveAtProject(in Input, caller string) Role {
	if caller == "" {
		return RoleNone
	}
	if isImplicit(in, caller) {
		return RoleAdmin
	}
	best := RoleNone
	for _, grant := range in.Grants {
		if grant.User != caller || !grant.Role.Valid() || !grant.Scope.Valid() || grant.Scope.Kind != ScopeProject {
			continue
		}
		if grant.Role.Rank() > best.Rank() {
			best = grant.Role
		}
	}
	return best
}

// isImplicit reports whether caller is one of the implicit project admins.
func isImplicit(in Input, caller string) bool {
	for _, member := range in.Implicit {
		if member == caller {
			return true
		}
	}
	return false
}

// Caller returns the member the credential the command will use belongs to, in
// the "user:<address>" form the grants are written with. It is the answer to
// "who is asking", and it is the only one: the caller is the credential, never
// the local configuration's `user:` key and never `git config user.email`,
// both of which are text a person or a coding agent can set (F21). A check
// built on either is a courtesy; this one is a check.
//
// A credential it cannot read — no Google credentials, a token whose userinfo
// refuses, an address a grant member could not carry, a service account's
// address, an unverified address — returns an error and no member, so a caller
// that ignores the error still holds nothing that could match a grant.
func Caller(ctx context.Context) (string, error) {
	address, err := credentialEmail(ctx)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(strings.ToLower(address), serviceAccountSuffix) {
		return "", fmt.Errorf("the credential is a service account's (%s is not a person the grants can name)", address)
	}
	member, err := Member(address)
	if err != nil {
		return "", fmt.Errorf("the credential's own address: %w", err)
	}
	return member, nil
}

// serviceAccountSuffix is how a service account's address ends. Such a
// credential is not a person: grants are members, and a member is an address a
// person has, so a machine's credentials hold nothing here whatever is stored.
const serviceAccountSuffix = ".gserviceaccount.com"

// credentialEmail returns the verified address of the credential the command
// will use. It is a package variable so a test never reaches the network, and
// so the one reader is replaceable at a seam rather than copied.
//
// The reader is Google's userinfo endpoint through the application-default
// credentials, which is the reader the tree already trusts for the same
// question (internal/cli's authenticatedUser asks it before writing the first
// project config). It is restated here rather than shared because this package
// cannot import internal/cli, and the CLI cannot yet take this one: nothing in
// the CLI calls this package while the work is inert (RS1), and RS5 points the
// commands at this reader and retires that one.
var credentialEmail = func(ctx context.Context) (string, error) {
	ts, err := google.DefaultTokenSource(ctx, oauth2UserinfoEmail)
	if err != nil {
		return "", fmt.Errorf("no Google credentials: %w", err)
	}
	httpClient := &http.Client{
		Timeout:   credentialTimeout,
		Transport: &oauth2.Transport{Source: oauth2.ReuseTokenSource(nil, ts)},
	}
	return credentialUserinfoEmail(ctx, httpClient, userinfoURL)
}

const (
	// oauth2UserinfoEmail is the scope the userinfo endpoint needs to answer
	// with an address.
	oauth2UserinfoEmail = "https://www.googleapis.com/auth/userinfo.email"
	// userinfoURL is Google's OpenID Connect userinfo endpoint.
	userinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	// credentialTimeout bounds the one call this package makes.
	credentialTimeout = 20 * time.Second
	// maxUserinfoBytes bounds what is read from that call: a userinfo document
	// is a few hundred bytes, and a body that never ends must not be read.
	maxUserinfoBytes = 64 << 10
)

// credentialUserinfoEmail reads the verified address of the user the client's
// token belongs to. An address the endpoint does not mark verified is refused:
// an unverified address is one a provider has not confirmed, and the grants
// name addresses.
func credentialUserinfoEmail(ctx context.Context, httpClient *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("asking the credential who it belongs to: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUserinfoBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("asking the credential who it belongs to: %s", resp.Status)
	}
	var who struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
	}
	if json.Unmarshal(body, &who) != nil || who.Email == "" {
		return "", fmt.Errorf("the credentials have no email (they were not made with the %s scope)", oauth2UserinfoEmail)
	}
	if !who.Verified {
		return "", fmt.Errorf("the credential's address is not verified")
	}
	return who.Email, nil
}
