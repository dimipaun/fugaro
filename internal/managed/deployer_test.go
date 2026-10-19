package managed

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// fakeToken is a token for the fakes below. It is built at run time rather
// than written as a literal so that nothing in this repository looks like a
// credential to a scanner — the same rule the rest of the suite follows.
func fakeToken() string { return strings.Join([]string{"test", "only", "token"}, "-") }

// fakeAccess is the project's answers about the deployer account: one
// account, and one member that may assume it.
type fakeAccess struct {
	exists, granted     bool
	existsErr, grantErr error
	// asked is every question this fake was asked, in order: the account it
	// looked for and the member the grant was checked for.
	asked  []string
	member string
}

func (a *fakeAccess) Exists(_ context.Context, account string) (bool, error) {
	a.asked = append(a.asked, "exists "+account)
	return a.exists, a.existsErr
}

func (a *fakeAccess) TokenCreator(_ context.Context, account, member string) (bool, error) {
	a.asked = append(a.asked, "tokenCreator "+account+" "+member)
	a.member = member
	return a.granted, a.grantErr
}

// fakeCred is a credential for one account. Every credential below is one of
// these, and Account is what tells the deployer's own from the ambient one.
type fakeCred struct{ account string }

func (c fakeCred) Account() string { return c.account }
func (c fakeCred) Source() oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fakeToken()})
}

// fakeImp is an impersonator with the failure mode D75 is about: when it
// cannot assume the account it answers with the identity the run already
// holds rather than with an error, which is what every impersonation
// library's fallback looks like from here.
type fakeImp struct {
	self string
	// fallback, when set, is returned as the credential of accounts that are
	// not in ok. With it, Impersonate never fails.
	fallback string
	// accounts is every account this fake was asked to assume.
	accounts []string
	err      error
}

func (i *fakeImp) Self() string { return i.self }

func (i *fakeImp) Impersonate(_ context.Context, account string) (Credential, error) {
	i.accounts = append(i.accounts, account)
	if i.err != nil {
		return nil, i.err
	}
	if i.fallback != "" {
		if account == i.self {
			return fakeCred{account: account}, nil
		}
		return fakeCred{account: i.fallback}, nil
	}
	return fakeCred{account: account}, nil
}

// work is the apply a managed step would run: it records that it was called,
// which is how every refusal below proves it applied nothing.
type work struct {
	calls int
	cred  Credential
	err   error
}

func (w *work) run(_ context.Context, cred Credential) error {
	w.calls++
	w.cred = cred
	return w.err
}

// rig is the whole of a managed apply's resolution: the operator at the
// terminal, the project the marker names, and the apply.
func deployRig() (string, *fakeAccess, *fakeImp, *work) {
	const self = "operator@example.com"
	return self, &fakeAccess{exists: true, granted: true}, &fakeImp{self: self}, &work{}
}

// The red step's case, and the reason this file exists: the account is there
// and the caller holds no impersonation grant, and the resolver — a library
// that falls back to the run's own identity when it cannot assume — would
// hand back the operator's credential. The apply must not run, and this is
// the test that fails against a Deployer that takes whatever credential came
// back.
func TestDeployerRefusesWhenTheImpersonationGrantIsMissingAndAppliesNothing(t *testing.T) {
	self, access, imp, w := deployRig()
	access.granted = false
	imp.fallback = self
	err := Deployer(context.Background(), gcpProject, access, imp, w.run)
	if !errors.Is(err, ErrNoImpersonationGrant) {
		t.Fatalf("a caller with no impersonation grant got %v, want the missing grant's refusal", err)
	}
	if !strings.Contains(err.Error(), self) || !strings.Contains(err.Error(), DeployerAccount(gcpProject)) {
		t.Errorf("the refusal %q names neither the caller nor the account", err)
	}
	if w.calls != 0 {
		t.Errorf("the apply ran %d time(s) with no impersonation grant, as %v", w.calls, w.cred)
	}
	if len(imp.accounts) != 0 {
		t.Errorf("the account was assumed anyway: %v", imp.accounts)
	}
	assertOneLine(t, err)
}

// And the account itself: a project whose creating apply never ran has no
// deployer, and a managed apply there is refused rather than run as the
// operator.
func TestDeployerRefusesWhenTheAccountDoesNotExist(t *testing.T) {
	self, access, imp, w := deployRig()
	access.exists = false
	imp.fallback = self
	err := Deployer(context.Background(), gcpProject, access, imp, w.run)
	if !errors.Is(err, ErrNoDeployerAccount) {
		t.Fatalf("a missing account got %v, want the missing account's refusal", err)
	}
	if !strings.Contains(err.Error(), DeployerAccount(gcpProject)) {
		t.Errorf("the refusal %q does not name the account it looked for", err)
	}
	if w.calls != 0 {
		t.Errorf("the apply ran %d time(s) with no deployer account", w.calls)
	}
	if len(imp.accounts) != 0 {
		t.Errorf("an account that does not exist was assumed: %v", imp.accounts)
	}
	assertOneLine(t, err)
}

// The third failure: the account is there and the grant is there, and the
// assumption fails anyway.
func TestDeployerRefusesWhenTheAssumptionFails(t *testing.T) {
	_, access, imp, w := deployRig()
	imp.err = errors.New("the token endpoint said no")
	err := Deployer(context.Background(), gcpProject, access, imp, w.run)
	if !errors.Is(err, ErrImpersonationFailed) {
		t.Fatalf("a failed assumption got %v, want the failed assumption's refusal", err)
	}
	if !errors.Is(err, imp.err) {
		t.Errorf("the refusal %q drops what the assumption said (%v)", err, imp.err)
	}
	if w.calls != 0 {
		t.Errorf("the apply ran %d time(s) after a failed assumption", w.calls)
	}
	assertOneLine(t, err)
}

// The failure a green suite hides: impersonation "succeeds" and answers with
// the ambient identity. The credential is checked against the account before
// anything runs, so the operator's own authority never reaches the apply.
func TestDeployerRefusesACredentialThatIsNotTheDeployers(t *testing.T) {
	self, access, imp, w := deployRig()
	imp.fallback = self
	err := Deployer(context.Background(), gcpProject, access, imp, w.run)
	if !errors.Is(err, ErrImpersonationFailed) {
		t.Fatalf("a credential for %s got %v, want the impersonation refused", self, err)
	}
	if !strings.Contains(err.Error(), self) {
		t.Errorf("the refusal %q does not say whose credential it was", err)
	}
	if w.calls != 0 {
		t.Error("the apply ran with the operator's own credential")
	}
	assertOneLine(t, err)
}

// And the admitted case: the apply runs, as the deployer account, with the
// credential the assumption returned — not the ambient one.
func TestDeployerRunsWorkAsTheAssumedCredential(t *testing.T) {
	self, access, imp, w := deployRig()
	w.err = errors.New("the apply's own failure")
	err := Deployer(context.Background(), gcpProject, access, imp, w.run)
	if !errors.Is(err, w.err) {
		t.Fatalf("Deployer: %v, want the work's own error through", err)
	}
	if w.calls != 1 {
		t.Fatalf("the apply ran %d time(s), want once", w.calls)
	}
	if got := w.cred.Account(); got != DeployerAccount(gcpProject) {
		t.Errorf("the apply ran as %s, want %s", got, DeployerAccount(gcpProject))
	}
	if w.cred.Account() == self {
		t.Error("the apply ran as the operator")
	}
	if w.cred.Source() == nil {
		t.Error("the credential carries no token source, so no client could be built from it")
	}
	// The deployer's own account was the one looked up, assumed, and checked,
	// and the grant was checked for the identity the run holds.
	if len(imp.accounts) != 1 || imp.accounts[0] != DeployerAccount(gcpProject) {
		t.Errorf("assumed %v, want the deployer account alone", imp.accounts)
	}
	if access.member != self {
		t.Errorf("the grant was checked for %q, want the run's own identity %q", access.member, self)
	}
}

// The questions are asked in order and about one account: nothing is assumed
// before the project has answered that the account is there and that this
// caller may assume it.
func TestDeployerAsksBeforeItAssumes(t *testing.T) {
	_, access, imp, w := deployRig()
	if err := Deployer(context.Background(), gcpProject, access, imp, w.run); err != nil {
		t.Fatalf("Deployer: %v", err)
	}
	account := DeployerAccount(gcpProject)
	want := []string{"exists " + account, "tokenCreator " + account + " " + imp.Self()}
	if len(access.asked) != len(want) {
		t.Fatalf("asked %v, want %v", access.asked, want)
	}
	for i, q := range want {
		if access.asked[i] != q {
			t.Errorf("question %d was %q, want %q", i, access.asked[i], q)
		}
	}
}

// Every way of arriving with nothing to resolve with is a refusal: no
// project, no access, no impersonator, and no work. None of them is a pass,
// and none of them runs anything.
func TestDeployerRefusesWhenItHasNothingToResolveWith(t *testing.T) {
	_, access, imp, w := deployRig()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"no project", func() error { return Deployer(context.Background(), "", access, imp, w.run) }},
		{"no access", func() error { return Deployer(context.Background(), gcpProject, nil, imp, w.run) }},
		{"no impersonator", func() error { return Deployer(context.Background(), gcpProject, access, nil, w.run) }},
		{"no work", func() error { return Deployer(context.Background(), gcpProject, access, imp, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := w.calls
			err := tc.run()
			if err == nil {
				t.Fatal("nothing to resolve with was not refused")
			}
			if w.calls != before {
				t.Error("the apply ran")
			}
			if len(imp.accounts) != 0 {
				t.Errorf("an account was assumed: %v", imp.accounts)
			}
			assertOneLine(t, err)
		})
	}
}

// The account is the module's fixed one: the ID the installation module
// creates, in the project the marker names, and never another.
func TestDeployerAccountIsTheModulesFixedID(t *testing.T) {
	got := DeployerAccount(gcpProject)
	if want := tf.DeployerAccountID + "@" + gcpProject + ".iam.gserviceaccount.com"; got != want {
		t.Errorf("DeployerAccount(%s) = %q, want %q", gcpProject, got, want)
	}
	if got := DeployerAccount(""); !strings.HasPrefix(got, tf.DeployerAccountID+"@") {
		t.Errorf("DeployerAccount(\"\") = %q, want the module's account ID and an empty project", got)
	}
}

// assertOneLine is the refusal's shape rule: one line, so a terminal prints
// it whole and a caller can put it in a verdict without a header.
func assertOneLine(t *testing.T, err error) {
	t.Helper()
	if s := err.Error(); strings.Contains(s, "\n") {
		t.Errorf("the refusal %q is not one line", s)
	}
}
