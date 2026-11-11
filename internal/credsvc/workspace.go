package credsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
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
// two decisions, written once so no route can invent a third, and each takes the
// type only its own entry point produces — OwnerCall for the owner, MemberCall
// for a member — so the class a route admits is carried by a value and not by a
// field the route re-reads.

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
	// DatabaseRegion is the region of the member's Realtime Database, empty for
	// the project's default one: an adopted project may hold its database in a
	// region, and the tree names that database
	// `<firebase_project>-default-rtdb.<region>.firebasedatabase.app` rather
	// than the legacy default host (internal/infra's CheckFirebaseOutputsFor).
	// The Firebase project still decides WHOSE database it is — the `iss` check
	// is what makes the derivation secure — and this only says where in that
	// project the database lives.
	DatabaseRegion string
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
			Project: m.Project, FirebaseProject: m.FirebaseProject, DatabaseRegion: m.DatabaseRegion, FP: m.FP,
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
	// Identity is the verified run identity. It is the zero value for the
	// owner's person, who is admitted by Caller alone — and it is never the
	// zero value on a MemberCall, the type every route that acts for a member
	// takes, because only Verify produces one.
	Identity Identity
	// Database is the member's database, derived from the member's Firebase
	// project (finding 6). It is empty for the owner's person.
	Database string
}

// MemberCall is a verified member's caller: what Caller resolved — the class,
// the account, the member and the member's database — together with the run
// identity Verify checked beside it.
//
// **It is a type rather than a field a route re-reads, and that is the whole of
// its job.** Only Verify produces one, so the routes that ACT for a member take
// a MemberCall and cannot be reached without the run identity having been
// verified: the pool, the policy the pool is intersected with and the member's
// own figures are all behind it, and no route can serve them by checking a flag
// of its own. It is also what the audit line needs: the "who" a member's route
// records is the run Verify returned (Identity.Audit), which a caller that
// stopped at Caller would have no value for — the installation's serving path
// takes its Identity the same way (cascade.go).
//
// **"Only Verify produces one" is carried by a value and not by this
// paragraph.** The mark below is unexported, so no caller outside this package
// can build the type at all; inside it, every entry point refuses a MemberCall
// the mark is not set on (refuseUnverified), which closes the literal a route
// written next to this file could otherwise build out of Caller's answer — a
// literal whose fields are all well-formed and which stands for no verified run
// identity at all.
//
// **What a call names is held to that Verify too, because the fields it is
// named by are exported and writable.** A holder of a verified call can point
// it at another member — `c.Member.Project = other` — or at another repository
// — `c.Identity.Slug = other` — and the mark would still be set, so a route
// that read either after the mark alone would read the OTHER member's database,
// or the OTHER repository's admission node and allow-list entry, while the run
// identity beside it stayed the one Verify checked. Both values Verify resolved
// are therefore kept unexported here and compared, which is the same division
// the mark makes: the mark says a Verify ran, and these say what it resolved.
//
// A member's Caller is still a WorkspaceCaller: the workspace-wide routes take
// the owner's type, whose only producer is Owner, because the class they admit —
// the workspace owner's people — holds no run identity at all (Verify refuses
// it).
type MemberCall struct {
	WorkspaceCaller
	verified bool
	// member is the project of the member Verify resolved and slug is the run
	// identity's verified repository, and they are what the routes below read:
	// the embedded Member and Identity are exported, so the member a route acts
	// for and the repository it asks about have to come from fields a caller
	// cannot rewrite.
	member string
	slug   string
}

// refuseUnverified holds every route that acts for a member to the mark Verify
// sets AND to what it resolved, and those are the three unexported fields'
// whole job: a MemberCall whose fields are all plausible and which Verify never
// produced is a call with no run identity behind it, and a MemberCall whose
// Member or Identity was pointed elsewhere afterwards names a member or a
// repository no Verify resolved — and the routes behind this check read that
// member's own policy and serve the workspace's pool for that repository. The
// refusal names no value: the package's rule is that a refusal names the field
// and never its content.
func (c MemberCall) refuseUnverified() error {
	if !c.verified {
		return fmt.Errorf("%w: the call did not come from Verify, so no run identity stands behind it and the routes that act for a member are closed", ErrUnverifiedJob)
	}
	if c.Member.Project != c.member {
		return fmt.Errorf("%w: the member this call names is not the member Verify resolved, so the routes that act for a member are closed", ErrUnverifiedJob)
	}
	if c.Identity.Slug != c.slug {
		return fmt.Errorf("%w: the repository this call names is not the one Verify resolved, so the routes that act for a member are closed", ErrUnverifiedJob)
	}
	return nil
}

// OwnerCall is a verified workspace owner's caller: one of the workspace
// owner's own people, resolved by Caller through Owner — its only producer — and
// the type the workspace-wide routes take.
//
// **It exists for MemberCall's reason, and for the write the member guard cannot
// close.** WorkspaceCaller is exported field by field, and the owner class is
// decided by its Class alone, so a route that took a bare WorkspaceCaller would
// admit a MEMBER's verified call relabelled `Class = ClassWorkspaceOwner` — and
// a literal `WorkspaceCaller{Class: ClassWorkspaceOwner}` a route could write
// for itself. Those routes are every member's spend at once (the member list,
// the cap, the rollup), so the class a route admits has to be carried by a value
// and not by a field: only Owner sets the mark below, and no caller outside this
// package can build the type at all.
//
// The owner's person holds no member and no run identity, so there is nothing
// else to hold to it: the mark is the whole of the type, and the Class is still
// checked because it is what the class means.
type OwnerCall struct {
	WorkspaceCaller
	verified bool
}

// refuseUnverified holds the workspace-wide routes to the mark Owner sets: a
// call whose fields are all plausible and which Owner never produced stands for
// no verified owner person, and the routes behind this check answer with every
// member's figures.
func (c OwnerCall) refuseUnverified() error {
	if !c.verified {
		return fmt.Errorf("%w: the call did not come from the owner's own entry point, so no verified owner person stands behind it and the routes that answer about the workspace are closed", ErrUnverifiedJob)
	}
	if c.Class != ClassWorkspaceOwner {
		return fmt.Errorf("%w: the route answers about the whole workspace, and only the workspace owner's own people may read it", ErrUnverifiedJob)
	}
	return nil
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
//
// **The verifier is built from the OWNER's table alone, so two account classes
// the installing member decides from its own published policy are not asked.**
// The installation refuses a shared project job whole until the phase-3 rules
// land (Global Constraint 6, verifyJob) and admits a review job for its own
// account's pairing alone (pairReview, offeredToReview); an account this table
// names is a per-repository job here whatever the member's own service would
// call it, and it is served the pool on every repository the member's own lists
// name. That is stated beside the repository pairing in
// docs/workspaces/spec.md §8's residual risk 7 and pinned by
// TestWorkspaceAdmitsEveryAccountInTheOwnersTableAsAPerRepositoryJob. It is not
// an oversight of the constructor: the owner's table IS this deployment's whole
// caller rule by design (spec.md §4 — a member's own file cannot admit a caller
// into the workspace's pool), so reading the member's lists here would be a
// second admission rule and an owner decision.
func (w *Workspace) readMembers(cfg WorkspaceConfig) error {
	seenFirebase := make(map[string]string, len(cfg.Members))
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
		// The region is a piece of the database's URL and this constructor may be
		// handed a table that never went through the node, so the shape is held
		// here too — by the same rule (projectpolicy.ValidDatabaseRegion) and
		// naming the member, so an operator knows which row to fix.
		if !projectpolicy.ValidDatabaseRegion(m.DatabaseRegion) {
			return fmt.Errorf("%w: the member %q's row names a database_region that is not a region a Realtime Database host is written with", ErrWorkspaceConfig, m.Project)
		}
		if _, dup := w.byProject[m.Project]; dup {
			return fmt.Errorf("%w: the member %q is in the table twice", ErrWorkspaceConfig, m.Project)
		}
		if len(m.JobAccounts) == 0 {
			return fmt.Errorf("%w: the member %q names no job service account, so no caller could resolve to it", ErrWorkspaceConfig, m.Project)
		}
		// One Firebase project belongs to one member, and the check is here as
		// well as in the node's own Validate: this constructor may be handed a
		// table that never went through the node, and the database below is
		// DERIVED from this field — so two rows naming one Firebase project would
		// open one database for two members and read one member's policy for
		// both, which is the splice finding 6 closed.
		if prev, dup := seenFirebase[m.FirebaseProject]; dup {
			return fmt.Errorf("%w: the members %q and %q name one Firebase project, and the database a member's state is read from is derived from it, so the two would read one member's state", ErrWorkspaceConfig, prev, m.Project)
		}
		seenFirebase[m.FirebaseProject] = m.Project
		url, err := workspaceDatabase(m.FirebaseProject, m.DatabaseRegion)
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
//
// A service account address is refused for the sibling constructor's reason
// (NewAuthenticator): the owner's people are the humans who operate the
// workspace, and *.gserviceaccount.com is the shape every job, verifier and
// coordinator account has. Admitting one would hand the class that reads the
// workspace's own figures — every member's spend at once — to an automation,
// which is the caller the class does not exist for.
func (w *Workspace) readPeople(cfg WorkspaceConfig) error {
	for _, person := range cfg.OwnerPeople {
		folded, ok := foldEmail(person)
		if !ok {
			return fmt.Errorf("%w: one of the workspace owner's people is not a printable ASCII address", ErrWorkspaceConfig)
		}
		if _, isJob := w.byAccount[folded]; isJob {
			return fmt.Errorf("%w: an address is both one of the workspace owner's people and a member's job service account, and a caller two classes admit would have the reach of whichever route it reached", ErrWorkspaceConfig)
		}
		if strings.HasSuffix(folded, ".gserviceaccount.com") {
			return fmt.Errorf("%w: one of the workspace owner's people is a service account address, and the person class is the workspace owner's operators: the routes that answer about the whole workspace are what that class reads", ErrWorkspaceConfig)
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
// The class is resolved BEFORE the token's audience is checked, because the
// audiences are the class's and not the deployment's. A member's job mints an
// ID token for this service's URL; one of the workspace owner's people cannot,
// because `gcloud auth print-identity-token --audiences` is refused for a user
// account — so a person's token carries gcloud's own client id and nothing else
// (GcloudAudience, the owner's ruling of 2026-11-04, and the same asymmetry the
// installation's own person class has). Holding this class to this service's
// URL alone would leave it with no reachable caller at all, which is exactly the
// class the workspace-wide routes are written for. The other direction is held
// too: a member's job is not offered gcloud's audience, so a token a person
// could mint cannot be presented as a member's.
//
// The rest of the token check is the shared one every class of caller gets —
// the signature against Google's certificates, Google as the issuer and the
// token's own window (callerClaims, checkCallerToken) — and the two lists are
// the deployment's classes. A caller on neither is refused with one message for
// both, which is what stops the refusal from enumerating the workspace's
// members.
func (w *Workspace) Caller(ctx context.Context, jobToken string) (WorkspaceCaller, error) {
	now := w.now()
	claims, folded, err := w.front.callerClaims(ctx, jobToken)
	if err != nil {
		return WorkspaceCaller{}, err
	}
	person, isPerson := w.ownerPeople[folded]
	member, isMember := w.byAccount[folded]
	if !isPerson && !isMember {
		return WorkspaceCaller{}, fmt.Errorf("%w: the caller is not one of the workspace's member job service accounts, and it is not one of the workspace owner's people", ErrUnverifiedJob)
	}
	auds := []string{w.audience}
	if isPerson {
		auds = append(auds, GcloudAudience)
	}
	if err := checkCallerAudience(claims, auds...); err != nil {
		return WorkspaceCaller{}, err
	}
	if err := checkCallerToken(claims, now); err != nil {
		return WorkspaceCaller{}, err
	}
	if isPerson {
		return WorkspaceCaller{Class: ClassWorkspaceOwner, Account: person}, nil
	}
	url, err := workspaceDatabase(member.FirebaseProject, member.DatabaseRegion)
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
// (spec.md §4). What it answers is a MemberCall, the type those routes take, so
// the two facts — this caller is a member's job AND the run identity beside it
// verified — arrive together or not at all.
func (w *Workspace) Verify(ctx context.Context, req Request) (MemberCall, error) {
	caller, err := w.Caller(ctx, req.JobIDToken)
	if err != nil {
		return MemberCall{}, err
	}
	if caller.Class != ClassWorkspaceMember {
		return MemberCall{}, fmt.Errorf("%w: the caller is one of the workspace owner's people and holds no run identity, so it may not reach the routes that act for a member", ErrUnverifiedJob)
	}
	auth, ok := w.auths[caller.Member.Project]
	if !ok {
		// Unreachable: Caller resolved the member out of this same table.
		return MemberCall{}, fmt.Errorf("%w: the caller resolved to a member this deployment has no verifier for", ErrWorkspaceConfig)
	}
	id, err := auth.Verify(ctx, req)
	if err != nil {
		return MemberCall{}, err
	}
	caller.Identity = id
	return MemberCall{WorkspaceCaller: caller, verified: true, member: caller.Member.Project, slug: caller.Identity.Slug}, nil
}

// Owner is Caller for the workspace owner's class, and it is the only producer
// of the type the workspace-wide routes take: the token is verified by the same
// front door Verify uses, and a member's job — which Caller resolves as readily
// as it resolves a person — is refused here, because the routes those calls
// reach answer with every member's spend at once.
func (w *Workspace) Owner(ctx context.Context, jobToken string) (OwnerCall, error) {
	caller, err := w.Caller(ctx, jobToken)
	if err != nil {
		return OwnerCall{}, err
	}
	if caller.Class != ClassWorkspaceOwner {
		return OwnerCall{}, fmt.Errorf("%w: the caller is a member's job service account, and the routes that answer about the workspace admit the workspace owner's own people alone", ErrUnverifiedJob)
	}
	return OwnerCall{WorkspaceCaller: caller, verified: true}, nil
}

// WorkspaceWide admits a caller to the routes that answer about the workspace
// as a whole: the member list, the cap and the rollup, which is every member's
// spend at once. It admits the workspace owner's people and no member's job,
// which is the whole of what stops one member's jobs reading every other
// member's figures (spec.md §4).
//
// It takes an OwnerCall rather than a bare caller, so neither a member's
// verified call relabelled `Class = ClassWorkspaceOwner` nor a
// WorkspaceCaller literal can reach these routes: the class a caller belongs to
// is decided once, by Caller, and carried by the type.
func (w *Workspace) WorkspaceWide(c OwnerCall) error {
	return c.refuseUnverified()
}

// MemberRoute admits a caller to a route that answers about ONE member, and it
// admits that member's job service account and no person at all: a member's
// person is not a class this deployment has (spec.md §4), and the workspace
// owner's person reaches a member's own figures through the workspace-wide
// routes rather than through a member's route. It takes a MemberCall rather
// than a bare caller so that a route cannot ask this question of a caller whose
// run identity was never verified.
func (w *Workspace) MemberRoute(c MemberCall, project string) error {
	if err := c.refuseUnverified(); err != nil {
		return err
	}
	if c.Class != ClassWorkspaceMember {
		return fmt.Errorf("%w: the route answers about one member, and only a member's job service account may reach it", ErrUnverifiedJob)
	}
	if c.member != project {
		return fmt.Errorf("%w: the caller is another member's job service account, and a member's route answers about that member alone", ErrUnverifiedJob)
	}
	return nil
}

// Policy reads the member's own published policy from the database its verified
// `iss` names: the member the call resolved to decides which database is read,
// so two members' accounts can never be pointed at one database by the owner's
// declaration (finding 6).
func (w *Workspace) Policy(ctx context.Context, c MemberCall) (*projectpolicy.Policy, bool, error) {
	db, err := w.database(c)
	if err != nil {
		return nil, false, err
	}
	return RTDBPolicy{DB: db}.Shared(ctx)
}

// Shared is the effective set of the workspace's pool for this caller: the
// credentials the workspace offers this member (the node's shared entries with
// the tiers the bundle carries, D1298's allow-list) whose name the member's own
// policy admits AND whose name the repository this run is on was admitted to in
// the member's own database.
//
// **The member's side is two reads, and the second is the repository's own
// node.** One is the member's published policy, asked by the credential's NAME
// (creds.AdmitShared's member leg): the member's `accounts` block is the
// vocabulary both nodes speak, so the leg is one name lookup. The other is the
// repository child at the slug the run identity VERIFIED (projectpolicy.PathRepo
// over the verified slug this call carries unexported, MemberCall.slug) —
// `Admissions` in the installation's own cascade, read
// there for the cascade's tier-A admission and here for the pooled credential —
// whose `credentials.accounts` are the account names an administrator admitted
// that repository to. A credential the member's policy names but that
// repository's node does not is not served: the repository is the member's own
// narrowing.
//
// **The member's two `allow_repos` legs are asked too, and the name they need is
// recovered rather than inverted.** The member's own cascade scopes the
// credential by the canonical `owner/repo` name as WELL as by the child
// (admittedAccounts): the project's own `allow_repos`, which is the leg that
// closes the `repos rm` gap, and the account's. A slug cannot be turned back
// into a name (F18), but it does not have to be: the account's list is exact —
// projectpolicy.validAccounts refuses a wildcard — so the run's repository is
// the entry whose task.Slug is the verified slug, which is the direction the
// publish path keys the child with (repoAdmissions). The account's leg is that
// entry and the project's list is then asked with the recovered name
// (canonicalOfSlug), so a child left behind by a narrowed account list or by a
// narrowed project list is refused here exactly as the member's own cascade
// refuses it, and the pool cannot widen a member by serving a decision that
// member's database no longer holds.
//
// **The slug is the run's own repository, and it is NOT held to the calling job
// account.** The identity Verify returned carries the slug its own token proved
// and this route reads the copy Verify kept (MemberCall.slug), so the child read
// here is the run's repository and not one a caller named — but nothing compares
// it with the repository the calling job account belongs
// to. The installation's front door does exactly that
// (`!bound || ja.Slug != id.Slug`, internal/credsvc/server.go), because its
// table binds each job account to the repository its service account is derived
// from; this deployment's per-member table names a member's job accounts and no
// repository, so there is no slug to compare and an account admitted for one
// repository can present a run identity of another (review finding N1). What
// that reaches is bounded by the member's own lists, because the two legs above
// are asked over the identity's own slug: a second repository has to be one the
// member's own policy scopes this credential to, so the confusion crosses no
// member and leaves no list the member itself wrote. That pairing is all
// docs/workspaces/spec.md §8's residual risk 7 still holds: stated there, an
// open item for the owner, and the column this service does not carry.
//
// The refusal an operator reads for one named credential is creds.AdmitShared's,
// and this answers the SET, which is what the cascade is offered: the legs are
// the same either way. A policy or an admission node that could not be read is a
// refusal and never an empty set, because one that could not be read is not one
// that admits nothing.
func (w *Workspace) Shared(ctx context.Context, c MemberCall, offered []creds.Shared) ([]creds.Credential, error) {
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
	admitted, err := w.admittedOn(ctx, c)
	if err != nil {
		return nil, err
	}
	member := creds.MemberPolicy(func(name string) bool {
		if !admitted[name] || !projectpolicy.MemberAdmitsShared(policy, name) {
			return false
		}
		repo, ok := canonicalOfSlug(policy.Accounts[name].AllowRepos, c.slug)
		return ok && projectpolicy.AdmitRepo(policy.AllowRepos, repo)
	})
	return creds.SharedFor(c.member, offered, member), nil
}

// canonicalOfSlug is the canonical name of the run's own repository, recovered
// from the member's own policy rather than from the slug: the entry of an
// account's `allow_repos` whose task.Slug is the slug the run identity verified.
//
// **The direction is canonical -> slug, the direction the publish path keys the
// repository's child with** (repoAdmissions, internal/cli/project_cmd.go), and
// the member's account lists are exact — projectpolicy.validAccounts refuses a
// wildcard — which is what makes the search decide. A slug cannot be inverted
// into a name (F18), and nothing here tries: the candidates are in the policy
// this call already read, and hashing each of them and comparing is the whole of
// the arithmetic. So the two legs the member's own cascade asks by the canonical
// name can be asked here too, and the pool is bounded by the member's policy and
// not only by the child a publish derived from it.
//
// An entry task.Slug refuses is skipped rather than fatal: the publish path
// refuses to write such a list (repoAdmissions), and a hand-written one neither
// matches nor stops the other entries from being asked.
func canonicalOfSlug(allowRepos []string, slug string) (string, bool) {
	for _, canonical := range allowRepos {
		provider, repo, ok := strings.Cut(canonical, "/")
		if !ok || provider == "" || repo == "" {
			continue
		}
		if s, err := task.Slug(provider, repo); err == nil && s == slug {
			return canonical, true
		}
	}
	return "", false
}

// admittedOn is the account names the run's OWN repository is admitted to, read
// from the member's own database at the node the run identity's verified slug
// names (projectpolicy.PathRepo, `Admissions` in the installation's cascade).
//
// An absent node is an empty admission and never an error — a repository the
// member's administrator admitted to no account is admitted to none, which is
// the same reading the cascade makes — and an unreadable node is a refusal,
// because a reader that turned one into an empty set would answer a permission
// question with a database outage (fail closed is a refusal, not a silent
// nothing). The slug is the verified one and not a value a caller supplied: the
// run identity carries `fs` and `fr`, both of them held to the uid the token's
// own Firebase project minted, so the node read here is the calling run's own
// repository — and the slug read is MemberCall.slug, the field Verify set, not
// the exported Identity it was copied from, so a call whose Identity.Slug was
// rewritten after Verify is refused by refuseUnverified instead of naming the
// node to read. What is not asked is whether that repository is the calling
// ACCOUNT's: the table names a member's job accounts and no repository, so the
// pairing the installation's front door makes has no slug to be made on here
// (Shared's own doc, and docs/workspaces/spec.md §8's residual risk 7). The
// shipped behaviour is pinned either way — the node read is the run's own
// repository and nothing falls back to another — by
// TestWorkspaceSharedReadsTheRepositoryOfTheRunIdentityNotTheCallers, so a
// change that closes the gap and a change that widens it both have to say so.
func (w *Workspace) admittedOn(ctx context.Context, c MemberCall) (map[string]bool, error) {
	db, err := w.database(c)
	if err != nil {
		return nil, err
	}
	child, found, err := RTDBPolicy{DB: db}.Admissions(ctx, c.slug)
	if err != nil {
		return nil, fmt.Errorf("credsvc: the repository's own admission node could not be read, so the workspace's pool is not served to this run: %w", err)
	}
	if !found || child == nil || child.Credentials == nil {
		return nil, nil
	}
	admitted := make(map[string]bool, len(child.Credentials.Accounts))
	for _, name := range child.Credentials.Accounts {
		admitted[name] = true
	}
	return admitted, nil
}

// database is the member's database as this deployment opened it, and it is
// keyed by the member the CALL resolved to: there is no path from a caller to
// another member's database, because the only way to name a member here is to
// have resolved to it.
func (w *Workspace) database(c MemberCall) (Getter, error) {
	if err := c.refuseUnverified(); err != nil {
		return nil, err
	}
	if c.Class != ClassWorkspaceMember {
		return nil, fmt.Errorf("%w: the caller is not a member's job, so it names no member's database", ErrUnverifiedJob)
	}
	db, ok := w.databases[c.member]
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
// The region is the row's and the project is the issuer's, and the division is
// what the derivation has to keep. A Database in a region is named there by the
// region alone — the tree's own reader admits exactly the legacy
// `https://<project>-default-rtdb.firebaseio.com` and the regional
// `https://<project>-default-rtdb.<region>.firebasedatabase.app`
// (internal/infra's CheckFirebaseOutputsFor) — so a member whose project was
// adopted with a regional database derives the host it actually has rather than
// the legacy one, which is a URL that is not its database at all. The project id
// still decides WHOSE database it is, which is the half finding 6 rests on, and
// it is the issuer's: the member's verifier holds the run identity's `iss` to
// exactly firebaseIssuer(FirebaseProject) before anything is read, so an owner's
// declaration can never pair one member's accounts with another member's —
// possibly looser — policy.
//
// The shape of the region is held here as well as in the node (projectpolicy's
// ValidDatabaseRegion), because this constructor may be handed a table that
// never went through the node and a region is a piece of a URL whatever built
// the row.
func workspaceDatabase(firebaseProject, region string) (string, error) {
	if !config.ProjectNameRE.MatchString(firebaseProject) {
		return "", errors.New("the member's row names a firebase_project that is not a Firebase project id, so its database could not be derived from it")
	}
	if !projectpolicy.ValidDatabaseRegion(region) {
		return "", errors.New("the member's row names a database_region that is not a region a Realtime Database host is written with, so its database could not be derived from it")
	}
	if region == "" {
		return "https://" + firebaseProject + "-default-rtdb.firebaseio.com", nil
	}
	return "https://" + firebaseProject + "-default-rtdb." + region + ".firebasedatabase.app", nil
}
