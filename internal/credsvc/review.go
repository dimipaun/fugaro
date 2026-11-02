package credsvc

// The review class (C3.9a): the third kind of caller this service admits, the
// repository table that pairs it with a run, and the claim rule that makes a
// run identity a review one (docs/c39-review-identity/spec.md §2, §3).
//
// Two classes were enough while every job of an installation was either a
// per-repository job (its service account is derived from the repository's
// slug and the workflow, so the account names the repository its runs are of)
// or a shared project job (refused whole, Global Constraint 6). The
// review-only variant (docs/cloud-senior-review/ CSR7) is a third: one job per
// (profile, account) of the published policy, whose account names no
// repository at all — so the comparison every other caller's call is bound by
// (ja.Slug == id.Slug) cannot be made, and the pairing is the account's
// admission to the repository the run identity names (spec §3.2).
//
// This file is SECURITY-SENSITIVE and is reviewed as such. It holds the class
// the rest of the package branches on, the table the pairing resolves a slug
// through, and the one admission check the pairing makes — which is the
// cascade's own (admittedAccounts), not a second spelling of it.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// JobClass is the kind of job a verified caller's service account belongs to:
// the account's class, decided from the verified email claim and never from
// the caller's own X-Fugaro-Run-Job header (spec §2.1). One service account
// is dedicated to one Cloud Run job, so the account is the job.
//
// It is internal to this package. It is not a field of the wire contract and
// not a field of the audit line: the act a line records is the route the
// caller asked for, which already says what the caller was doing, and no
// caller is told a class it can act on.
type JobClass string

const (
	// ClassRepository is a workflow job of one repository
	// (gcp.ServiceAccountID over the repository's slug and the workflow). It
	// is admitted to everything this service does: the model path, the mint
	// with the run's own permission set, and the operator's stop flag.
	ClassRepository JobClass = "repository"
	// ClassShared is a *coding* shared project job (gcp.ProjectServiceAccountID
	// over the published policy's profiles). Global Constraint 6 refuses a
	// caller of this class whole, and verifyJob answers the class beside that
	// refusal — the class is a fact about the account even where the decision
	// about it is a refusal, and the phase-3 shared-job rules are what lift
	// it.
	ClassShared JobClass = "shared"
	// ClassReview is the review-only variant of a project job, per
	// (profile, account): admitted to the mint, the sign and the model path
	// for its own account only, and refused everywhere else (spec §2.1).
	ClassReview JobClass = "review"
)

// ReviewVariantSuffix marks the review-only variant of a project job on the
// (profile, provider, account) axis: the same axis, the same job-name and
// service-account shapes (gcp.ProjectJobName, gcp.ProjectServiceAccountID),
// with this on the account segment.
//
// It is here, and the two functions below with it, because the service and the
// deployer must spell the variant's names the same way: the accounts this
// service admits are the accounts internal/infra/projectjobs.go creates
// (CSR7), and a second spelling would be a service that admits an account
// nobody deployed or, worse, refuses the one that exists.
const ReviewVariantSuffix = "-review"

// ReviewJobName is the review-only variant's Cloud Run job name for one
// (profile, provider, account) of the published policy's profiles. It is
// gcp.ProjectJobName's own derivation over the variant's axis, so the variant
// is one job per (profile, account) exactly as the coding job is, and its name
// cannot collide with the coding job's.
func ReviewJobName(profile, provider, account string) string {
	return gcp.ProjectJobName(profile, provider, account+ReviewVariantSuffix)
}

// ReviewServiceAccountID is the review-only variant's service account ID, the
// name ReviewJobName's job runs as.
func ReviewServiceAccountID(profile, provider, account string) string {
	return gcp.ProjectServiceAccountID(profile, provider, account+ReviewVariantSuffix)
}

// ReviewJobAccount is one review job: the service account it runs as, and the
// Claude account a call it makes is served by.
//
// The two are different names of the same thing. Email is what the caller is
// verified as — the account Cloud Run minted its ID token for, derived from
// the (profile, provider, account) triple. Account is what the *published
// admissions* speak: the name the shared node's accounts block is keyed by,
// which is the name the pairing looks the caller up under and the name of the
// tier-A credential a review call is offered. A caller cannot write either.
type ReviewJobAccount struct {
	// Email is the review job's service account, in the spelling the
	// deployment configured (AuthConfig.ReviewJobAccounts folds it for
	// matching and keeps this spelling for the identity and the audit line).
	Email string
	// Account is the Claude account's name as the policy's accounts block
	// spells it. It is never empty: the axis spells the default account as the
	// empty string (config.ProjectJobAccounts) and the admissions spell it
	// config.DefaultClaudeAccount.
	Account string
}

// ReviewJobAccountsFor computes the service accounts of the installation's
// review-only jobs: one per (profile, provider, account) the published policy
// publishes, derived exactly as CSR7 deploys them
// (ReviewServiceAccountID over config.ProjectJobAccounts' axis, the one
// function the deployer, the launcher and the runner share for a profile's job
// set).
//
// It mirrors SharedJobAccountsFor deliberately, down to its fail-closed shape:
// the set is computed from the *published policy* rather than listed, so an
// installation that publishes no profile for an account admits no review job
// for it, and an account the service admits is an account the deployer
// created. TestReviewAccountsAreTheNamesProjectJobsDeploys builds one
// installation both ways and requires the two answers to agree, and
// TestSharedAndReviewSetsAreDisjoint requires the two classes not to overlap —
// an account in both would be refused everywhere (verifyJob) and the review
// job could never mint.
//
// The account name is the one the admissions speak, and the axis spells the
// default account as the empty string: that mapping is
// config.ProjectJobTokenAccount's, spelled here because the review job holds
// no token of its own to join (the variant mounts nothing under O1 — its model
// calls come here).
func ReviewJobAccountsFor(p *projectpolicy.Policy, gcpProject string) []ReviewJobAccount {
	if p == nil {
		return nil
	}
	var out []ReviewJobAccount
	for _, name := range p.ProfileNames() {
		pr, err := p.ProfileFor(name)
		if err != nil {
			// A profile the policy itself cannot read is not a review job.
			// Decode refuses one, so this is unreachable through a published
			// node, and a reader that cannot be given a bad profile must not
			// invent an account name from it.
			continue
		}
		for _, account := range config.ProjectJobAccounts(pr.JobProfile) {
			if account == "" {
				account = config.DefaultClaudeAccount
			}
			out = append(out, ReviewJobAccount{
				Email:   serviceAccountEmail(ReviewServiceAccountID(name, pr.Provider, account), gcpProject),
				Account: account,
			})
		}
	}
	slices.SortFunc(out, func(a, b ReviewJobAccount) int { return cmp.Compare(a.Email, b.Email) })
	return slices.Compact(out)
}

// RepositoryTableFor builds the installation's repository table: the slug a
// run identity carries (task.Slug) to the canonical repository name the
// published policy speaks (projectpolicy.Canonical), over the same input
// JobAccountsFor takes.
//
// It is that function's own rows read the other way round, and deliberately:
// the slug a review caller's run token carries is resolved by the table the
// per-repository callers are bound by, so a service cannot bind an account one
// way and resolve a slug another. The keys are the two derivations the run
// identity and the mint already use, and nothing here guesses: a slug the
// table does not hold is a refusal at the pairing (D1066), never a canonical
// name invented from the slug's own text.
//
// The table holds the installation's own jobs and nothing else. A repository
// the project's policy admits but no job serves has no row, and a review call
// about it is refused — which is the same statement the per-repository class
// makes by having no account to call as.
func RepositoryTableFor(fugaroProject, gcpProject, provider string, jobs []RepoJob) (map[string]string, error) {
	accounts, err := JobAccountsFor(fugaroProject, gcpProject, provider, jobs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(accounts))
	for _, ja := range accounts {
		out[ja.Slug] = ja.Repo
	}
	return out, nil
}

// ReviewOf holds a verified run identity to the review claim rule (spec §3.3):
// it pushes nothing and it reviews at least one pull request.
//
//   - fb absent or empty. A review run pushes nothing, and an artifact run
//     satisfies this too — which is why the second half exists. A coding run
//     does not, and a coding run launched into the review job is REFUSED here
//     rather than downgraded to a review: the identity it carries is a
//     different act, and answering it as a review would hand the read-only set
//     to a run that meant to push.
//   - fpr present and non-empty. This is what tells a review identity from an
//     artifact identity, which pushes nothing and writes no pull request
//     either. branchClaims admits an fpr that is present and empty (it reads
//     the claim's shape, not its meaning), so the emptiness is caught here.
//
// The refusal is an ErrUnverified, which is what makes it the wire's own
// unverified_run_identity and the status the two-identity check already
// answers with: no runner needs a new switch arm. It names the claim that
// failed and never its value — an fb and an fpr are the caller's own claim
// set, and the package's no-echo rule holds for them as it does for the kid.
func ReviewOf(id Identity) error {
	if id.Branch != "" {
		return fmt.Errorf("%w: the run identity carries a branch, so it is a run that pushes and not a review", ErrUnverified)
	}
	if len(id.PRs) == 0 {
		return fmt.Errorf("%w: the run identity names no pull request, so it is not a review of one", ErrUnverified)
	}
	return nil
}

// reviewAccount is the review row of the calling account: the table NewServer
// built, looked up by the FOLDED account.
//
// The two sides are spelled by two lists. The table is keyed by the folded
// email (NewServer), and the account Verify answers with is the VERIFIER's own
// spelling — so the lookup folds on the way in, exactly as the verifier folds a
// presented claim, and a deployment that spelled the two lists in different
// cases pairs its caller rather than refusing it for a reason no operator could
// read. Folding either side alone leaves the other direction missing.
//
// An email that cannot be folded is a miss and not an error: verifyJob could
// not have answered a class for one (it folds the claim the same way), so the
// refusal it produces is fail-closed and unreachable through ServeHTTP.
func (s *Server) reviewAccount(email string) (ReviewJobAccount, bool) {
	folded, ok := foldEmail(email)
	if !ok {
		return ReviewJobAccount{}, false
	}
	ra, ok := s.review[folded]
	return ra, ok
}

// pairReview is the review class's pairing (spec §3.2 steps 1-3), and the
// answer is the canonical repository every check after it is made against: the
// repository is the run identity's own slug, the slug is resolved through the
// installation's table, and the caller's account must be admitted to that
// repository by the model path's own admission check.
//
// The admission is admittedAccounts and not a read of the repository's child
// node: the child names the accounts an administrator admitted, and the
// intersection also requires the shared node to define the account and to
// offer it to this repository. Reading the child alone is the narrower
// spelling that keeps admitting a repository the owner withdrew with `fugaro
// project repos rm` — that command takes the repository out of the project's
// allow-list and does not touch its child — so the pairing takes the whole
// intersection and no second implementation of O4 exists.
//
// An absent child is an empty admission and never an error (a repository
// admitted to nothing is admitted to nothing); an unreadable child or node is
// a refusal, because fail closed is a refusal and not a silent nothing.
func (s *Server) pairReview(ctx context.Context, id Identity) (string, error) {
	// Step 4 comes first: the claim rule is a fact about the run identity
	// alone, so it is asked before anything is read — before the table, before
	// the admissions and, because the pairing runs in ServeHTTP before the stop
	// flag and before the bundle, before both of those too. Its refusal is an
	// ErrUnverified through refusePairing and so a refusal this service records
	// once: asking it inside Verify, which returns an empty Identity on the way
	// out, was a refusal with no line at all.
	if err := ReviewOf(id); err != nil {
		return "", err
	}
	ra, ok := s.reviewAccount(id.JobAccount)
	if !ok {
		// The verifier answered a class this service's own table does not
		// hold: the two lists are configured together, so this is a
		// deployment that disagrees with itself, and the safe answer is the
		// refusal rather than an account name guessed from the email.
		return "", fmt.Errorf("%w: the calling account is not one of this service's review job accounts", ErrUnverified)
	}
	canonical, ok := s.cfg.Repos[id.Slug]
	if !ok || canonical == "" {
		return "", fmt.Errorf("%w: the run identity's repository is not one this installation's jobs serve", ErrUnverified)
	}
	admitted, err := s.admittedAccounts(ctx, id.Slug, canonical)
	if err != nil {
		return "", fmt.Errorf("%w: the repository's admissions could not be read", ErrUnavailable)
	}
	if !admitted[ra.Account] {
		return "", fmt.Errorf("%w: the calling account is not admitted to the run identity's repository", ErrUnverified)
	}
	return canonical, nil
}

// refusePairing answers a pairing refusal with the contract's own vocabulary:
// a pairing that could not be made is an unverified run identity (the same
// status and type the per-repository class's binding refusal carries, because
// it is the same question about the same run), and state the service could not
// read is the state being unavailable — a refusal the run halts on, never a
// fallback (D143).
func (s *Server) refusePairing(ctx context.Context, w http.ResponseWriter, id Identity, purpose, credential string, err error) {
	status, typ := StatusUnverified, ErrorUnverifiedRunIdentity
	if errors.Is(err, ErrUnavailable) {
		status, typ = StatusUnavailable, ErrorStateUnavailable
	}
	s.refuseCall(ctx, w, id, purpose, credential, "", status, typ, err.Error())
}
