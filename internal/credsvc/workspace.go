package credsvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The workspace's credentials service: one deployment in the workspace's own
// project, holding the workspace's one bundle secret and paying for every
// member's shared-credential call (docs/workspaces/spec.md §4, D1297).
//
// **It is the installation's service with three things moved, and this file is
// the three.** The front door, the bundle loader, the cascade, the wire
// contract and the refusals are the same code (server.go, bundle.go,
// cascade.go): a member's run reaches it exactly as a run reaches its own
// installation's service, with the job's own service account presenting a
// Google ID token. What differs is that one deployment serves N installations,
// so the three checks that were pinned to ONE installation have to become
// per-member, and the plan names them (spec.md §4):
//
//   - **The caller's member is the verified service account and nothing else.**
//     The account is looked up in the workspace's own table (W6's node,
//     projectpolicy.WorkspaceCredentials) and the member it names is the member,
//     whatever a request body, header or query parameter says. The configured
//     spelling of the address that matched is what the answer carries, exactly
//     as verifyJob returns the installation's.
//   - **The token's pins have to admit that member.** verifyRun refuses a token
//     whose `aud` is not this service's Firebase project, whose `iss` is not
//     that project's securetoken issuer, or whose `fp` is not this service's
//     Fugaro project. A workspace deployment accepts EACH MEMBER'S Firebase
//     project as the issuer and EACH MEMBER'S Fugaro project as `fp`, so this
//     file builds every member its own Authenticator over that member's row:
//     the account's member, the issuer's member and the `fp`'s member are one
//     member because they are one verifier, and a token whose pins name another
//     member is refused by the verifier of the member the account resolved to.
//     That is the half of this task a reader is most likely to miss, and it is
//     why the run identity is verified by the MEMBER's verifier and never by a
//     workspace-wide one.
//   - **The member's database is derived from the verified `iss`, never
//     declared** (finding 6 of the post-merge security review). The service
//     reads each member's policy, ledger and outcomes, and one configured
//     database would be one member's state answering for all of them: an owner
//     who declared member B's job accounts beside member A's database URL would
//     pair B's callers with A's — possibly looser — policy. So a member's row
//     carries no database URL at all (projectpolicy's node refuses one), the URL
//     is derived from the Firebase project the member's tokens are issued for
//     (workspaceDatabase), and the `iss` check above is what makes the two the
//     same fact.
//
// **The two caller classes, and which routes each reaches.** A workspace
// service admits a member's job service accounts and the workspace owner's own
// people, and it admits no member's person: the list that names a member's
// people is that member's own launcher/operator list, which this deployment
// holds no grant to read, and an admission it cannot check is an admission it
// does not make (spec.md §4, F24/F31). Every route therefore names the class it
// admits rather than inheriting one from a single person list: the
// workspace-wide routes — the member list, the cap, the rollup, which is every
// member's spend at once — admit the workspace owner's people and no member's
// job, and the routes that answer about ONE member admit that member's job
// service account and no person at all. WorkspaceWide and MemberRoute are those
// two decisions, written once so no route can invent a third.

// ErrWorkspaceConfig is the refusal of a configuration the workspace's service
// will not start on. It is not a caller's refusal: it is raised while the
// process is being built, and a service that started without one of these
// checks would be a service admitting what it cannot check.
var ErrWorkspaceConfig = errors.New("credsvc: the workspace credentials service is not configured to check what it admits")

// WorkspaceMember is one row of the workspace's per-member table: how the
// service reaches one member. It is the owner's declaration
// (projectpolicy.WorkspaceCredentials's member rows) read into the shape the
// verifiers are built from, and it carries no database URL and no bucket: the
// database is derived (workspaceDatabase) and no bucket is bound to a member's
// job.
type WorkspaceMember struct {
	// Project is the member's Fugaro project name: the namespace the shared
	// pool's allow-lists speak and the key this table is read by.
	Project string
	// FirebaseProject is the member's Firebase project id: the `aud` and the
	// `iss` of that member's run identities, and therefore the fact the
	// member's database is derived from.
	FirebaseProject string
	// FP is the member's Fugaro project name as the `fp` claim carries it,
	// which the member's verifier holds the run identity to. It is the same
	// value as Project, and the two are compared rather than one read twice
	// because the pins have to name the member the account names.
	FP string
	// JobAccounts are the member's job service accounts, by email: the
	// identities whose calls resolve to this member. One account belongs to one
	// member, and the table's own reader refuses a node where two rows name
	// one.
	JobAccounts []string
	// ExchangeURL is the member's exchange endpoint (spec.md §3). It is the
	// owner's declaration of where the member is reached, and this file reads
	// it for nothing: the assertion the workspace mints for a member is signed
	// at the workspace's signer, and the member's exchange verifies it against
	// the key its own pin carries.
	ExchangeURL string
}

// WorkspaceMembersOf reads the workspace's credentials node into the table the
// service is built from. It is the one conversion, so the node's reader and the
// service cannot come to different answers about what a row says.
func WorkspaceMembersOf(w *projectpolicy.WorkspaceCredentials) []WorkspaceMember {
	if w == nil {
		return nil
	}
	out := make([]WorkspaceMember, 0, len(w.Members))
	for _, m := range w.Members {
		accounts := make([]string, 0, len(m.JobAccounts))
		accounts = append(accounts, m.JobAccounts...)
		out = append(out, WorkspaceMember{
			Project: m.Project, FirebaseProject: m.FirebaseProject, FP: m.FP,
			JobAccounts: accounts, ExchangeURL: m.ExchangeURL,
		})
	}
	return out
}

// WorkspaceConfig is the workspace service's identity and reach.
type WorkspaceConfig struct {
	// Project is the workspace's own Fugaro project name: the name a member's
	// pin carries and the name the workspace's own record is published under.
	Project string
	// Audience is the audience every caller's Google ID token must carry: this
	// service's own URL, which is what Cloud Run IAM checked it against. It is
	// one value for the whole deployment — the members' tokens are minted for
	// the service, and no member's own URL appears here.
	Audience string
	// Members is the per-member table (WorkspaceMembersOf over the published
	// node). A service with no member admits nobody, and that is a
	// configuration error rather than a service that answers nothing: a
	// workspace that published no member has nothing to run.
	Members []WorkspaceMember
	// OwnerPeople are the workspace owner's own people: the identities the
	// workspace-wide routes admit, and the only person class this deployment
	// has (spec.md §4). A member's person is never one of them, because the
	// list that names a member's people is that member's own file.
	OwnerPeople []string
	// Keys is Google's published key set for Firebase ID tokens: the members'
	// run identities. One set for every member, because the keys are Google's
	// and one global set (GoogleKeysURL) — what tells one member's tokens from
	// another's is the pair aud/iss, which is why each member gets a verifier
	// of its own rather than a key set of its own.
	Keys KeySource
	// JobKeys is Google's published key set for Google ID tokens: the callers.
	JobKeys KeySource
	// Open opens the database at a URL: a member's, derived from the member's
	// Firebase project (workspaceDatabase), or the workspace's own for the
	// routes that answer about the workspace. It is a function rather than one
	// client because a deployment has one client per member, and the URL is the
	// only thing that says which member's state a call may read.
	Open func(databaseURL string) (Getter, error)
	// Now is the clock, for tests; nil means time.Now.
	Now func() time.Time
}

// WorkspaceCallerClass is which of the workspace's two caller classes a
// verified caller is. The classes are the deployment's whole admission rule and
// they are deliberately disjoint: an address two classes admit would be a
// caller whose reach depends on which route it reached.
type WorkspaceCallerClass int

const (
	// ClassWorkspaceMember is a member's job service account: the caller of
	// everything that answers about one member or spends on its behalf.
	ClassWorkspaceMember WorkspaceCallerClass = iota
	// ClassWorkspaceOwner is one of the workspace owner's own people: the only
	// class the workspace-wide routes admit. It is a person and holds no run
	// identity, so it reaches the routes that answer about the workspace and
	// none of the routes that act for a member.
	ClassWorkspaceOwner
)

// WorkspaceCaller is a verified caller of the workspace's service: which class
// it is, the configured spelling of its address and — for a member's job — the
// member it resolved to, the run identity beside it and the database that
// member's state is read from. It carries names and no credential value: no
// field of it is a secret, and a bundle's values are reached only by the code
// that attaches one to a call.
type WorkspaceCaller struct {
	// Class is which of the two classes the caller is.
	Class WorkspaceCallerClass
	// Account is the configured spelling of the address that matched, which is
	// what the audit line writes and never the claim's spelling.
	Account string
	// Member is the member the account resolved to. It is the zero value for
	// the owner's person, who is a member of nothing.
	Member WorkspaceMember
	// Identity is the verified run identity, for a member's job caller that
	// presented one. It is the zero value for the owner's person, and for a
	// member's job on a route that carries no run identity.
	Identity Identity
	// Database is the member's database, derived from the member's Firebase
	// project (finding 6). It is empty for the owner's person.
	Database string
}

// Workspace is the workspace's credentials service: the front door, the
// per-member verifiers and the route classes.
type Workspace struct {
	project     string
	audience    string
	now         func() time.Time
	ownerPeople map[string]string
	byAccount   map[string]WorkspaceMember
	byProject   map[string]WorkspaceMember
	auths       map[string]*Authenticator
	databases   map[string]Getter
	front       *Authenticator
}

// NewWorkspace validates the configuration and returns the service. Everything
// a check depends on is required, for NewAuthenticator's reason: a service that
// cannot check one of these does not start, because the alternative is a
// service that admits what it cannot check.
func NewWorkspace(cfg WorkspaceConfig) (*Workspace, error) {
	switch {
	case cfg.Project == "":
		return nil, fmt.Errorf("%w: no Fugaro project", ErrWorkspaceConfig)
	case cfg.Audience == "":
		return nil, fmt.Errorf("%w: no audience, so a caller's ID token could not be checked", ErrWorkspaceConfig)
	case cfg.Keys == nil:
		return nil, fmt.Errorf("%w: no key source for the members' run identities", ErrWorkspaceConfig)
	case cfg.JobKeys == nil:
		return nil, fmt.Errorf("%w: no key source for the callers", ErrWorkspaceConfig)
	case cfg.Open == nil:
		return nil, fmt.Errorf("%w: no database opener, so no member's state could be read", ErrWorkspaceConfig)
	case len(cfg.Members) == 0:
		return nil, fmt.Errorf("%w: the table names no member, so the deployment could admit nobody", ErrWorkspaceConfig)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	// The front door is an Authenticator whose CALLER half is the whole of what
	// is used: the run identity beside a caller is the member's own, checked by
	// that member's own verifier below, because the aud, the iss and the fp a
	// workspace accepts are per member. It is built rather than
	// NewAuthenticator'd for that reason — the constructor refuses a
	// configuration with no outcome reader, and this verifier reads no outcome:
	// it resolves which member a caller is, and the member's verifier is what
	// reads that member's database.
	front := &Authenticator{
		cfg:         AuthConfig{Audience: cfg.Audience, JobKeys: cfg.JobKeys},
		jobAccounts: make(map[string]string, len(cfg.Members)),
		now:         now,
	}
	w := &Workspace{
		project:     cfg.Project,
		audience:    cfg.Audience,
		now:         now,
		ownerPeople: make(map[string]string, len(cfg.OwnerPeople)),
		byAccount:   make(map[string]WorkspaceMember, len(cfg.Members)),
		byProject:   make(map[string]WorkspaceMember, len(cfg.Members)),
		auths:       make(map[string]*Authenticator, len(cfg.Members)),
		databases:   make(map[string]Getter, len(cfg.Members)),
		front:       front,
	}
	if err := w.readMembers(cfg); err != nil {
		return nil, err
	}
	if err := w.readPeople(cfg); err != nil {
		return nil, err
	}
	return w, nil
}

// readMembers folds the table and builds each member's verifier. The fold is
// the same ASCII rule a presented claim is folded by (foldEmail), so an account
// that could never be matched is a configuration error here rather than a gate
// that silently admits nobody.
func (w *Workspace) readMembers(cfg WorkspaceConfig) error {
	for _, m := range cfg.Members {
		if !config.ProjectNameRE.MatchString(m.Project) {
			return fmt.Errorf("%w: a member's project is not a project name", ErrWorkspaceConfig)
		}
		if m.Project != m.FP {
			return fmt.Errorf("%w: the member %q's row names the fp %q, and the account's member and the fp's member have to be one member", ErrWorkspaceConfig, m.Project, m.FP)
		}
		if !config.ProjectNameRE.MatchString(m.FirebaseProject) {
			return fmt.Errorf("%w: the member %q's row names a Firebase project that is not a project name", ErrWorkspaceConfig, m.Project)
		}
		if _, dup := w.byProject[m.Project]; dup {
			return fmt.Errorf("%w: the member %q is in the table twice", ErrWorkspaceConfig, m.Project)
		}
		if len(m.JobAccounts) == 0 {
			return fmt.Errorf("%w: the member %q names no job service account, so no caller could resolve to it", ErrWorkspaceConfig, m.Project)
		}
		url, err := workspaceDatabase(m.FirebaseProject)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrWorkspaceConfig, err)
		}
		db, err := cfg.Open(url)
		if err != nil {
			return fmt.Errorf("%w: the member %q's database could not be opened: %w", ErrWorkspaceConfig, m.Project, err)
		}
		auth, err := NewAuthenticator(AuthConfig{
			Project:         m.FP,
			FirebaseProject: m.FirebaseProject,
			Audience:        w.audience,
			JobAccounts:     m.JobAccounts,
			Keys:            cfg.Keys,
			JobKeys:         cfg.JobKeys,
			Outcomes:        RTDBOutcomes{DB: db},
			Now:             cfg.Now,
		})
		if err != nil {
			return fmt.Errorf("%w: the member %q's verifier: %w", ErrWorkspaceConfig, m.Project, err)
		}
		for _, account := range m.JobAccounts {
			folded, ok := foldEmail(account)
			if !ok {
				return fmt.Errorf("%w: a job service account of the member %q is not a printable ASCII email address", ErrWorkspaceConfig, m.Project)
			}
			if prev, dup := w.byAccount[folded]; dup {
				return fmt.Errorf("%w: one job service account is in the table for the members %q and %q, and one account belongs to one member", ErrWorkspaceConfig, prev.Project, m.Project)
			}
			w.byAccount[folded] = m
			// The front door's own table answers the configured spelling, for
			// verifyJob's reason: the "who" a refusal's audit line carries is
			// the one an operator configured and can search for.
			w.front.jobAccounts[folded] = account
		}
		w.byProject[m.Project] = m
		w.auths[m.Project] = auth
		w.databases[m.Project] = db
	}
	return nil
}

// readPeople folds the workspace owner's own people, which are the workspace's
// whole person class. An address that is also a member's job service account is
// refused rather than ordered: a caller two classes admit is a caller whose
// reach depends on which route it reached, which is the rule NewAuthenticator
// makes for a person that is also a job, read here for this deployment's two
// lists.
func (w *Workspace) readPeople(cfg WorkspaceConfig) error {
	for _, person := range cfg.OwnerPeople {
		folded, ok := foldEmail(person)
		if !ok {
			return fmt.Errorf("%w: one of the workspace owner's people is not a printable ASCII address", ErrWorkspaceConfig)
		}
		if _, isJob := w.byAccount[folded]; isJob {
			return fmt.Errorf("%w: an address is both one of the workspace owner's people and a member's job service account, and a caller two classes admit would have the reach of whichever route it reached", ErrWorkspaceConfig)
		}
		if _, dup := w.ownerPeople[folded]; dup {
			continue
		}
		w.ownerPeople[folded] = person
	}
	return nil
}

// Caller verifies a caller's Google ID token and answers which class it is and,
// for a member's job, which member it resolved to. Nothing in a request body, a
// header or a query parameter names the member: the verified account does, and
// the configured spelling of it is what the answer carries.
//
// The token check is the shared one every class of caller gets — the signature
// against Google's certificates, Google as the issuer, this service's audience
// and the token's own window (callerClaims, checkCallerAudience,
// checkCallerToken) — and the two lists are the deployment's classes. A caller
// on neither is refused with one message for both, which is what stops the
// refusal from enumerating the workspace's members.
func (w *Workspace) Caller(ctx context.Context, jobToken string) (WorkspaceCaller, error) {
	now := w.now()
	claims, folded, err := w.front.callerClaims(ctx, jobToken)
	if err != nil {
		return WorkspaceCaller{}, err
	}
	if err := checkCallerAudience(claims, w.audience); err != nil {
		return WorkspaceCaller{}, err
	}
	if err := checkCallerToken(claims, now); err != nil {
		return WorkspaceCaller{}, err
	}
	if person, ok := w.ownerPeople[folded]; ok {
		return WorkspaceCaller{Class: ClassWorkspaceOwner, Account: person}, nil
	}
	member, ok := w.byAccount[folded]
	if !ok {
		return WorkspaceCaller{}, fmt.Errorf("%w: the caller is not one of the workspace's member job service accounts, and it is not one of the workspace owner's people", ErrUnverifiedJob)
	}
	url, err := workspaceDatabase(member.FirebaseProject)
	if err != nil {
		return WorkspaceCaller{}, err
	}
	return WorkspaceCaller{
		Class:    ClassWorkspaceMember,
		Account:  w.front.jobAccounts[folded],
		Member:   member,
		Database: url,
	}, nil
}

// Verify is Caller plus the run identity beside it, and it is the front door of
// every route that acts for a member: the member's OWN verifier checks the run
// token, so the account's member, the issuer's member and the fp's member are
// one member — a token whose pins name another member is refused here, by the
// verifier of the member the account resolved to.
//
// The owner's person holds no run identity, so it is refused: the routes that
// act for a member admit that member's job service account and no person
// (spec.md §4).
func (w *Workspace) Verify(ctx context.Context, req Request) (WorkspaceCaller, error) {
	caller, err := w.Caller(ctx, req.JobIDToken)
	if err != nil {
		return WorkspaceCaller{}, err
	}
	if caller.Class != ClassWorkspaceMember {
		return WorkspaceCaller{}, fmt.Errorf("%w: the caller is one of the workspace owner's people and holds no run identity, so it may not reach the routes that act for a member", ErrUnverifiedJob)
	}
	auth, ok := w.auths[caller.Member.Project]
	if !ok {
		// Unreachable: Caller resolved the member out of this same table.
		return WorkspaceCaller{}, fmt.Errorf("%w: the caller resolved to a member this deployment has no verifier for", ErrWorkspaceConfig)
	}
	id, err := auth.Verify(ctx, req)
	if err != nil {
		return WorkspaceCaller{}, err
	}
	caller.Identity = id
	return caller, nil
}

// WorkspaceWide admits a caller to the routes that answer about the workspace
// as a whole: the member list, the cap and the rollup, which is every member's
// spend at once. It admits the workspace owner's people and no member's job,
// which is the whole of what stops one member's jobs reading every other
// member's figures (spec.md §4).
func (w *Workspace) WorkspaceWide(c WorkspaceCaller) error {
	if c.Class != ClassWorkspaceOwner {
		return fmt.Errorf("%w: the route answers about the whole workspace, and only the workspace owner's own people may read it", ErrUnverifiedJob)
	}
	return nil
}

// MemberRoute admits a caller to a route that answers about ONE member, and it
// admits that member's job service account and no person at all: a member's
// person is not a class this deployment has (spec.md §4), and the workspace
// owner's person reaches a member's own figures through the workspace-wide
// routes rather than through a member's route.
func (w *Workspace) MemberRoute(c WorkspaceCaller, project string) error {
	if c.Class != ClassWorkspaceMember {
		return fmt.Errorf("%w: the route answers about one member, and only a member's job service account may reach it", ErrUnverifiedJob)
	}
	if c.Member.Project != project {
		return fmt.Errorf("%w: the caller is another member's job service account, and a member's route answers about that member alone", ErrUnverifiedJob)
	}
	return nil
}

// Policy reads the member's own published policy from the database its verified
// `iss` names: the member the call resolved to decides which database is read,
// so two members' accounts can never be pointed at one database by the owner's
// declaration (finding 6).
func (w *Workspace) Policy(ctx context.Context, c WorkspaceCaller) (*projectpolicy.Policy, bool, error) {
	db, err := w.database(c)
	if err != nil {
		return nil, false, err
	}
	return RTDBPolicy{DB: db}.Shared(ctx)
}

// Shared is the effective set of the workspace's pool for this caller: the
// credentials the workspace offers this member AND the member's own policy
// admits (D1298). offered is the workspace's declaration — the node's shared
// entries with the tiers the bundle carries — and the member side is read from
// the member's own policy in the member's own database, for the repository the
// run is on.
//
// The refusal an operator reads for one named credential is creds.AdmitShared's,
// and this answers the SET, which is what the cascade is offered: the two legs
// are the same two either way. A policy that could not be read is a refusal and
// never an empty set, because a policy that could not be read is not one that
// admits nothing.
func (w *Workspace) Shared(ctx context.Context, c WorkspaceCaller, offered []creds.Shared) ([]creds.Credential, error) {
	if c.Class != ClassWorkspaceMember {
		return nil, fmt.Errorf("%w: the shared pool is served to a member's job, and the caller is not one", ErrUnverifiedJob)
	}
	policy, found, err := w.Policy(ctx, c)
	if err != nil {
		return nil, err
	}
	if !found {
		policy = nil
	}
	repo := c.Identity.Slug
	member := creds.MemberPolicy(func(name string) bool {
		return projectpolicy.MemberAdmitsShared(policy, name, repo)
	})
	return creds.SharedFor(c.Member.Project, offered, member), nil
}

// database is the member's database as this deployment opened it, and it is
// keyed by the member the CALL resolved to: there is no path from a caller to
// another member's database, because the only way to name a member here is to
// have resolved to it.
func (w *Workspace) database(c WorkspaceCaller) (Getter, error) {
	if c.Class != ClassWorkspaceMember {
		return nil, fmt.Errorf("%w: the caller is not a member's job, so it names no member's database", ErrUnverifiedJob)
	}
	db, ok := w.databases[c.Member.Project]
	if !ok {
		return nil, fmt.Errorf("%w: the member this call resolved to has no database in this deployment", ErrWorkspaceConfig)
	}
	return db, nil
}

// workspaceDatabase is a member's database URL, derived from the Firebase
// project the member's tokens are issued for: a Firebase project's own database
// is <project>-default-rtdb, the shape the installation's own service holds its
// one database to (internal/cli's checkDatabaseHost).
//
// It is derived from the issuer and not declared, and the two are the same fact
// because the member's verifier holds the run identity's `iss` to exactly
// workspaceIssuer(FirebaseProject) before anything is read: the database a call
// reads is therefore the one the caller's own verified `iss` names, and an
// owner's declaration can never pair one member's accounts with another
// member's — possibly looser — policy (finding 6).
func workspaceDatabase(firebaseProject string) (string, error) {
	if !config.ProjectNameRE.MatchString(firebaseProject) {
		return "", errors.New("the member's row names a firebase_project that is not a Firebase project id, so its database could not be derived from it")
	}
	return "https://" + firebaseProject + "-default-rtdb.firebaseio.com", nil
}

// workspaceIssuer is the issuer a member's Firebase project mints its tokens
// under, and the value the member's verifier holds the run identity's `iss` to
// (verifyRun, over the member's own FirebaseProject). It is the same derivation
// localcfg.WorkspaceIssuer makes on the member's own machine, and a test holds
// the two spellings to each other: the pair aud/iss is the whole of what tells
// one project's tokens from another's, because Google's Firebase signing keys
// are one global set (GoogleKeysURL), so a service whose half of the pair
// differed from the launcher's would refuse every honest call.
func workspaceIssuer(firebaseProject string) string {
	return "https://securetoken.google.com/" + firebaseProject
}
