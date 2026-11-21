package credsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is R16: the launch-scope check the ticket route asks before it mints
// anything, and the whole of the answer it must give.
//
// **It gives the CLI gate's answer, arm by arm, and that is the property the
// implementation is written for, not a resemblance.** internal/cli's
// launchPolicyGates is what a launcher refuses a run with today, and it is a
// courtesy: a launcher can call jobs.run directly, and the launcher is the side
// a modified binary is on. The issuer is the first server-side place that can
// enforce the same thing (D3130 as amended), so it must reach the same verdict
// as the gate on every arm of R16 — the implicit admins, the service account,
// the installation with no roster, the context repositories, a node that does
// not decode — with exactly one deliberate difference: when the records cannot
// be reached the gate does not judge and the launch goes on, while the issuer
// refuses, because it cannot write its own record there either (R16's sixth
// arm).
//
// **The two arms are one answer because they are one resolver.** The judgement
// is `internal/access`'s - the same Input, the same Effective, the same
// Allowed - and what this file adds is the reading of that input on the
// service's side of the wire: the records over IAM (U10: the credentials
// account carries the database's write permission, which is what reads this
// node as well as writing the issuance record), the implicit admins the
// deployment renders (LM12, LM22) and the published policy.
// internal/cli/access_issuer_agree_test.go drives both implementations over one
// table, a row per arm, so a divergence fails there rather than in production.

// ErrNoLaunchScope is the refusal of the launch scope: the caller was admitted
// (LM3 verified its class) and the repository the ask names is not one it may
// launch.
//
// It is a sentinel because the ticket route reads it as a CODE and not as a
// sentence: a refusal that carries it is answered with
// identitywire.RefusalNoLaunchScope and the one audit line every refusal of a
// verified caller writes, and the sentence wrapped around it is what the
// operator (and the launcher that printed it) reads. Everything else this file
// returns is either that refusal or a *ScopeFault, which is the service's own
// failure and is answered as one - a run's gateway reads a 403 as a missing
// invoke grant and halts the run on it (R16), so a node nobody could read must
// never be refused as a caller who is not admitted.
var ErrNoLaunchScope = errors.New("credsvc: the caller holds no launch scope on the repository")

// AccessRecords is the roles-and-scopes records as the issuer reads them: the
// grants node and its migration marker, which `internal/access` owns and reads.
// It is an interface for the reason ScopeCheck is one: the check is judged by
// tests that hand it a roster without a database, and the real reader is
// RTDBAccess below.
type AccessRecords interface {
	// Read is the grants node (access.Read): the stored pairs, and whether the
	// node exists at all.
	Read(ctx context.Context) (accesslib.Node, error)
	// Migrated is the node's own marker (access.Migrated): what tells an
	// installation whose last grant was revoked (a roster of nobody, judged)
	// from one whose lists have never been migrated (no roster, not judged).
	// A marker that cannot be read is an error, and it fails closed.
	Migrated(ctx context.Context) (bool, error)
}

// RTDBAccess is the records as the service reads them from the installation's
// database, over IAM with its own account.
//
// The account is the point: the node sits under config/admin, which the rules
// cascade closed to every client token, and what reaches it is the database's
// one write permission - firebasedatabase.instances.update, which this
// service's account carries through the two-permission role `fugaro update`
// creates on every installation (internal/access/store.go's PathNode comment,
// D6, SECURITY.md). The same principals that may write the issuance record may
// write this node, which is why the claim re-checks the record and why R3
// states the residual in the open.
type RTDBAccess struct {
	// DB is the database; *rtdb.Client is one. A nil DB is a deployment with no
	// database to read, which is read as an installation with no roster (the
	// grant-free state LaunchScope's third arm allows) rather than as a
	// panic - a service built before its database address is configured must
	// answer calls, not crash on them.
	DB *rtdb.Client
}

func (a RTDBAccess) Read(ctx context.Context) (accesslib.Node, error) {
	if a.DB == nil {
		return accesslib.Node{}, nil
	}
	return accesslib.Read(ctx, a.DB)
}

func (a RTDBAccess) Migrated(ctx context.Context) (bool, error) {
	if a.DB == nil {
		return false, nil
	}
	return accesslib.Migrated(ctx, a.DB)
}

// LaunchScopeConfig is what the check is built from, and every field is the
// service's side of one of the gate's inputs.
type LaunchScopeConfig struct {
	// Repos is the installation's repository table: the storage slug of every
	// repository this installation has a job for, and its canonical name
	// ("github/acme/app"). The gate derives the canonical from the repository
	// the launch's task names; the issuer cannot, because a slug is a hash and
	// names its repository back only against this table (D1066). A slug the
	// table does not hold is refused rather than guessed at, so a caller cannot
	// reach a scope judgement about a repository nothing here can name.
	Repos map[string]string
	// Records is the roles-and-scopes records (internal/access). A nil reader
	// and a nil database are one state: an installation with no roster, which
	// the gate reads as nothing to judge by and allows.
	Records AccessRecords
	// Policy is the published policy, read exactly as the gate's
	// accessAllowRepos reads it. nil is an installation that has published
	// none.
	Policy PolicyReader
	// ImplicitAdmins are the machine's implicit admins as members
	// ("user:<address>"), the gate's first arm: the CLI reads them from its own
	// configuration (localcfg's terraform.budget_admins, `user:` entries only)
	// and `fugaro update` renders them into this service's environment, so an
	// owner is not refused a launch that works today.
	ImplicitAdmins []string
}

// NewLaunchScope is the real launch-scope check (LM23), built from the
// deployment's configuration. I1 hands it to C1's IdentityConfig.Scope; the
// orchestrator's class is not its business and is composed beside it (LM24).
func NewLaunchScope(cfg LaunchScopeConfig) ScopeCheck { return launchScope{cfg: cfg} }

type launchScope struct{ cfg LaunchScopeConfig }

// Check answers whether caller may be issued a ticket for the run its ask
// names.
//
// The orchestrator's class is left to its own check, and that is a decision and
// not an omission: what that class may be issued is bound to the orchestrator's
// own record and to the attempt the ask names (D3136 as amended, LM24), it is
// judged with no committer and no scope at all, and a launch scope that refused
// it here would refuse every ask LM24 admits. The two are composed at the mount
// (I1), so this check sees the class only to hand it on.
func (sc launchScope) Check(ctx context.Context, caller IdentityCaller, ask identitywire.TicketRequest) error {
	switch caller.Class {
	case identitywire.ClassOrchestrator:
		return nil
	case identitywire.ClassPerson, identitywire.ClassServiceAccount:
	default:
		// The ticket route admits three classes and refuses a job account
		// itself (identitywire.Routes, LM3), so no call of this check carries
		// another one. It is refused rather than admitted anyway: a class this
		// check does not judge is one whose scope nothing computed, and the
		// ticket route is not a place to be wrong about that.
		return fmt.Errorf("%w: the caller's class %q is not one the launch scope judges", ErrNoLaunchScope, string(caller.Class))
	}

	canonical, kind, ok := sc.canonical(ask.Slug)
	if !ok {
		return fmt.Errorf("%w: this installation has no repository %q, so no scope on one can be judged", ErrNoLaunchScope, safetext.Field(ask.Slug))
	}

	allow, published, err := sc.policyAllow(ctx)
	if err != nil {
		return err
	}

	in, placed, err := sc.accessInput(ctx, allow, published)
	if err != nil {
		return err
	}

	// A person is judged by the resolver, the way the gate judges them
	// (requireAccess): the roles the records hold, the implicit admins the
	// configuration names and the allow-list, in one answer. A service account
	// is admitted with no scope judgement at all (D3137: parity with the gate,
	// which places no such credential - its address is not a member a grant can
	// carry), and so is a person the installation does not place, which is an
	// installation with no roster rather than one that placed nobody (F29:
	// accessInput, and the gate's own arm for it).
	//
	// The records are read for BOTH classes, and that is the gate's own order
	// (accessRunJudge reads the node before it reads the credential): a node
	// that cannot be held whole refuses everybody, and a service account that
	// skipped the read would be served through a roster the installation cannot
	// read.
	member, judged := "", false
	if caller.Class == identitywire.ClassPerson {
		member, err = accesslib.Member(caller.Address)
		if err != nil {
			return fmt.Errorf("%w: the caller's own address cannot be a grant's member, so no record can place it: %v", ErrNoLaunchScope, err)
		}
		// judged is the gate's own `placed` for a person, and false for a
		// service account however full the roster is: the resolver is asked
		// about a member, a service account's address is not one a grant can
		// carry, and a service account judged with the empty member would be
		// refused every repository a context list names - while the gate
		// (accessRunJudge) answers the same credential placed=false and lets
		// the launch through (D3137). The allow-list below still judges it,
		// which is the gate's own arm for it (accessRunJudge returns before
		// requireAccess, and allowListRefusal is asked after).
		judged = placed
		if judged && !accesslib.Allowed(in, member, accesslib.ActionLaunch, canonical) {
			// Which sentence a refusal carries is the gate's own choice
			// (internal/cli's accessRunRefusal): a repository the published
			// policy does not admit is answered with the policy's sentence,
			// because no grant can change that answer, and every other refusal
			// with what the caller holds.
			if published && !projectpolicy.AdmitRepo(allow, canonical) {
				return notAdmitted(allow, canonical)
			}
			return scopeRefusal(caller.Address, "launching", canonical, accesslib.RoleOperator, accesslib.Effective(in, member, canonical))
		}
	}

	// The published allow-list, asked of every class, exactly as the gate's
	// allowListRefusal asks it after its role check: a repository the project's
	// own list does not admit cannot run at all (F27), and for a service
	// account and an unplaced caller this is the only arm that judges the
	// repository. The resolver above has already refused a repository outside
	// the list for a caller it placed (access.Effective gates on it first), so
	// the two implementations agree by construction and not by coincidence.
	if published && !projectpolicy.AdmitRepo(allow, canonical) {
		return notAdmitted(allow, canonical)
	}

	if len(ask.ContextRepos) > 0 && !published {
		// A context repository is admitted by the project's published
		// allow_repos and by nothing else (F31), and an installation that has
		// published none has no list that could admit one. It is the gate's own
		// arm, word for word in outcome: it refuses the launch that names one
		// and lets the launch that names none through.
		return fmt.Errorf("%w: the ask names the context repositories %s and this project has published no policy, so no allow_repos admits them", ErrNoLaunchScope, safetext.Field(strings.Join(ask.ContextRepos, ", ")))
	}

	// Every context repository is judged the way the run's own one is, and for
	// the same reason the gate gives: this service signs the list into the
	// run's identity, and a reader of that identity (the run's gateway) holds
	// the signature and not a role, so a repository the caller may not read
	// must be refused here or the launch hands out a read the person was never
	// given (F31). ActionViewRepo and not ActionLaunch: nothing of a context
	// repository is launched, written or pushed, only read.
	for _, repo := range ask.ContextRepos {
		context, err := projectpolicy.Canonical(kind, repo)
		if err != nil {
			return fmt.Errorf("%w: the context repository %q cannot be named in the project's allow-list: %v", ErrNoLaunchScope, safetext.Field(repo), err)
		}
		if judged && !accesslib.Allowed(in, member, accesslib.ActionViewRepo, context) {
			if !projectpolicy.AdmitRepo(allow, context) {
				return notAdmitted(allow, context)
			}
			return scopeRefusal(caller.Address, "reading", context, accesslib.RoleViewer, accesslib.Effective(in, member, context))
		}
		if !projectpolicy.AdmitRepo(allow, context) {
			return notAdmitted(allow, context)
		}
	}
	return nil
}

// canonical is the canonical name and the provider kind of the repository one
// ask is for, from the ask's slug: the installation's table is the only thing
// that names a repository back from its storage slug (task.Slug is a hash of
// the pair), and an entry this check cannot read as a repository is refused
// rather than judged - the kind is what a context repository is canonically
// named with, and a table that does not carry one would canonicalize them in a
// spelling no allow-list matches.
func (sc launchScope) canonical(slug string) (canonical, kind string, ok bool) {
	canonical, ok = sc.cfg.Repos[slug]
	if !ok {
		return "", "", false
	}
	kind, repo, cut := strings.Cut(canonical, "/")
	if !cut || kind == "" || repo == "" {
		return "", "", false
	}
	return canonical, kind, true
}

// policyAllow is the published policy's allow_repos, and whether a policy is
// published at all.
//
// No policy and an unreadable policy are deliberately NOT one answer here, and
// that is the one place this reader differs from the gate's accessAllowRepos,
// which reads both as an allow-list that admits everything: a policy that
// cannot be read is the launcher's own refusal one step earlier (the gate reads
// the node before it reaches any scope question), so refusing it keeps the two
// sides agreeing rather than admitting at the issuer what the gate refuses -
// and the resolution here is not the one that has to tolerate an outage, since
// the launch it decides has an installation at the other end that is running
// its own gate first. The "*" answer accessAllowRepos gives belongs to the
// commands that read the roles for a run that already exists, where a bad
// minute must not lock a person out of their own history.
func (sc launchScope) policyAllow(ctx context.Context) (allow []string, published bool, err error) {
	if sc.cfg.Policy == nil {
		return nil, false, nil
	}
	p, found, err := sc.cfg.Policy.Shared(ctx)
	switch {
	case err != nil:
		// The scope could not be built for a reason that is not the caller's,
		// and it is answered as one. Caller is left nil deliberately: the only
		// identity this check was given is the verified caller, the ROUTE holds
		// it already, and the one audit line of the refusal is written where the
		// refusal is answered - an Identity built here would name a run
		// identity nothing verified (ScopeFault's own comment).
		return nil, false, &ScopeFault{What: "the project's published policy (" + projectpolicy.PathShared() + ")", Err: err}
	case !found:
		return nil, false, nil
	}
	return p.AllowRepos, true, nil
}

// accessInput is the resolver's input and whether the installation has a roster
// to judge against at all: internal/cli's accessInput and accessStoredGrants on
// the service's side, arm for arm.
//
// placed false is the state the gate allows rather than refuses - a database
// whose lists have never been migrated, a service that holds no database,
// records that cannot be reached - and it is NOT the same as a roster that
// places nobody: an installation whose last grant was revoked holds a roster of
// nobody (the migration marker is under the node), is placed=true, and refuses
// everyone it does not name. Reading the two as one is what would let the last
// grant's revocation hand every launch to whoever asks next.
//
// The one arm where the two sides part is here, and it is R16's sixth: records
// that cannot be REACHED leave the gate judging nothing and the launch going
// on, and the issuer refuses. What it refuses with is a *ScopeFault and never a
// refusal of the caller, because a run's gateway halts a 403 as a missing
// invoke grant (R16) and this answer is the service's own - it cannot write the
// issuance record there either.
func (sc launchScope) accessInput(ctx context.Context, allow []string, published bool) (accesslib.Input, bool, error) {
	in := accesslib.Input{Implicit: sc.cfg.ImplicitAdmins, AllowRepos: []string{"*"}}
	if published {
		in.AllowRepos = allow
	}
	if sc.cfg.Records == nil {
		return in, false, nil
	}
	node, err := sc.cfg.Records.Read(ctx)
	if err != nil {
		return accesslib.Input{}, false, sc.recordsFault(err)
	}
	if !node.Found {
		migrated, err := sc.cfg.Records.Migrated(ctx)
		if err != nil {
			return accesslib.Input{}, false, sc.recordsFault(err)
		}
		if !migrated {
			return in, false, nil
		}
	}
	in.Grants = node.Grants
	return in, true, nil
}

// recordsFault is the service's own failure to read the installation's roles,
// whatever the reader answered.
//
// A node that cannot be HELD WHOLE (a document past its bounds, an unknown
// role, a scope that does not parse: access.Decode) is a fault and not a
// refusal of the caller, and that is the gate's own reading of the same fact
// (internal/cli's dbErr answers a decode error as a remote failure, not as a
// user error) - it is an installation this binary and this service cannot agree
// about, not a caller who is not admitted. A REFUSED read is a fault too, and
// deliberately: at the gate the reader is the caller's own credential, and the
// refusal is worded as the credential holding no viewer role on the project
// (F29b), while here the reader is the service's own account (U10) - the same
// answer would be about this service's IAM and not about the caller, and a
// deployment whose account cannot read the node it must judge is a deployment
// failure. Both sides refuse it either way; only the sentence and the status
// differ, and neither is the caller's.
func (sc launchScope) recordsFault(err error) error {
	return &ScopeFault{What: "the installation's roles and scopes records (" + accesslib.PathNode + ")", Err: err}
}

// notAdmitted is the refusal of a repository the published policy does not
// admit, worded by projectpolicy's own NotAdmitted - the one sentence the run's
// admission check, the launcher's gate and this check all refuse with, so an
// operator who reads it twice reads about one policy and not three (F27).
func notAdmitted(allow []string, canonical string) error {
	return fmt.Errorf("%w: %s", ErrNoLaunchScope, projectpolicy.NotAdmitted(allow, canonical))
}

// scopeRefusal is a caller the records do not place: what the caller holds
// there, what the action needs, and the one command that grants it. It is the
// service's own sentence for the same fact the gate words (accessRunRefusal),
// and it names no value the caller did not send itself.
func scopeRefusal(address, verb, repo string, needs accesslib.Role, held accesslib.Role) error {
	holds := "no role there"
	if held != accesslib.RoleNone {
		holds = "the role " + string(held) + " there"
	}
	return fmt.Errorf("%w: %s holds %s, and %s %s needs the role %s in it. The roles are the installation's records (%s), read from the verified identity that asked and never from anything the ask carries, and an admin of the project grants one with 'fugaro user grant %s --role %s --scope %s'",
		ErrNoLaunchScope, safetext.Field(address), holds, verb, repo, needs, accesslib.PathNode, safetext.Field(address), needs, repo)
}

// The values and the interface are one contract: a check whose refusal the
// route cannot read as a code, or a fault it answers as a caller refusal, is
// the bug this assertion catches at build time.
var (
	_ ScopeCheck = launchScope{}
	_ error      = (*ScopeFault)(nil)
)
