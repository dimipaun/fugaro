package credsvc

// WC5's tests: the personal candidate (docs/workspace-credentials, R7, D2974,
// D3144, D2965, R9).
//
// personal_workspace.go is the reader and cascade.go's candidates puts its
// answer in front of the list; what these tests state is the four answers R7
// names — whose token, whether, what tier and where in the list — the two
// refusals a person's own token is answered with when it cannot be served, the
// one move a spent token makes, and the one path that must not change: an
// installation's.
//
// The fixture is the slips fixture with the personal half wired on it
// (personalSvc), and it is built ON the routes rather than around them: a slip
// is granted and claimed through the service's own two routes, and a call is
// the model call a gateway inside a run sends. What a test reads is therefore
// the candidate list candidates really built, the value the upstream was
// really handed, and the answer the run really got.
//
// Nothing here names a person's token: the name is built through the grammar
// (mustUserName, personalName) wherever a test needs it, so a test cannot state
// a spelling the intake could never write and no test name carries one.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// personalScope is a workspace's scope as this file's calls meet it: an
// installation's scope with C2's personal half over it, and — where the test
// asked for one — the workspace's meter (WS8's).
//
// The real workspace scope is WS2's; the three methods beside Personal are that
// half's other routes, and no call in this file reaches them. They answer
// nothing rather than refusing, because a fixture that refused a route no test
// of this file calls would be asserting something about a task that is not
// WC5's: what the personal half needs of a scope is the reader, and what the
// reader needs of a call is the slip.
type personalScope struct {
	installationScope
	personal PersonalReader
	meter    *WorkspaceMeter
}

func (sc personalScope) Personal() PersonalReader { return sc.personal }

func (sc personalScope) Launchers() LauncherVerifier { return nil }

func (sc personalScope) Slips() creds.SlipStore { return nil }

func (sc personalScope) OwnsSlip(context.Context, string) (bool, error) { return false, nil }

func (sc personalScope) Meter() *WorkspaceMeter { return sc.meter }

// personalResolver answers every call of the fixture with a scope that has the
// personal half on it, the way wsMeterResolver puts the meter on one: the
// installation's own resolution, and the one thing the workspace's deployment
// adds over it. It is the seam WC14 wires for real (PersonScopeConfigForServe),
// stood in for here because a hermetic test has no roster and no workspace.
type personalResolver struct {
	srv      func() *Server
	personal PersonalReader
	meter    *WorkspaceMeter
}

func (r *personalResolver) Resolve(ctx context.Context, req *http.Request) (Scope, error) {
	sc, err := installationResolver{s: r.srv()}.Resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	base, ok := sc.(installationScope)
	if !ok {
		return sc, nil
	}
	return personalScope{installationScope: base, personal: r.personal, meter: r.meter}, nil
}

// personalReaderOf is the reader C2's constructor answers, built the way a
// workspace's deployment builds it. It goes through the constructor VARIABLE on
// purpose: that variable is the whole of how the two sides meet, and a test
// that named personalReader itself would pass on a tree where WC5's own init
// (personal_workspace.go) had stopped standing.
func personalReaderOf(t *testing.T) PersonalReader {
	t.Helper()
	r, err := NewPersonalReader(PersonScopeConfig{})
	if err != nil {
		t.Fatalf("the personal reader's constructor refused the zero configuration: %v", err)
	}
	if r == nil {
		t.Fatal("the personal reader's constructor answered no reader")
	}
	return r
}

// personalCall is the context one of this file's direct reader calls is made
// under: the call's own identity and the workspace's scope over it, which is
// the same pair the front door puts on a context (withScope). It is where the
// reader reads the member whose switch it asks about, so a test that wants the
// switch read for a project makes the identity carry that project.
func personalCall(t *testing.T, s *personalSvc) context.Context {
	t.Helper()
	return withScope(context.Background(), personalScope{
		installationScope: installationScope{s: s.server, id: Identity{
			Slug: s.slug, Run: s.run, Project: testProject,
			Job: s.job, JobAccount: testJobSA, JobClass: ClassRepository,
		}},
		personal: personalReaderOf(t),
	})
}

// scriptedSwitch is R9's switch under the test's control: the answer, an error
// for a policy that could not be read, and the projects it was asked about —
// which is what makes "the switch is the MEMBER's, read from the call's own
// identity" a fact a test can check rather than an inference.
type scriptedSwitch struct {
	on  bool
	err error

	mu    sync.Mutex
	asked []string
}

func (s *scriptedSwitch) PersonSwitchOn(_ context.Context, project string) (bool, error) {
	s.mu.Lock()
	s.asked = append(s.asked, project)
	s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	return s.on, nil
}

// projects is every project the switch was asked about, in order.
func (s *scriptedSwitch) projects() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

// personalSwitch installs the switch for one test and puts the process's own
// back when the test ends. It has to: the variable is package-level
// (personal_workspace.go says why), so one left set would decide the calls of
// every test that ran after it — including an installation's, which is the one
// path that must be served by the stub's OFF.
func personalSwitch(t *testing.T, on bool, err error) *scriptedSwitch {
	t.Helper()
	sw := &scriptedSwitch{on: on, err: err}
	prev := PersonSwitchForServe
	PersonSwitchForServe = sw
	t.Cleanup(func() { PersonSwitchForServe = prev })
	return sw
}

// personalSvc is the slips fixture with the personal half wired: the reader
// C2's constructor answers, the switch, a bundle source that reads containers
// as well as the document, and — where the test asked for one — the workspace's
// meter and the transport that watched the month at every upstream call.
type personalSvc struct {
	*slips
	dyn     *dynReader
	meter   *WorkspaceMeter
	meterDB *gcpfake.RTDB
	fenced  *wsFencedDB
	watch   *wsMeterWatch
}

// newPersonalSvc is the fixture. metered seeds the workspace's record and puts
// the workspace's meter on the scope; mutate is the test's own last word, run
// the way newSvcWith runs one.
//
// The launcher class is newSlips' wiring, respelled here because a fixture can
// carry only one mutate and this one is already spoken for: the class holds the
// fixture's one launcher address, and the store is the real creds.RTDBStore
// over a fake database, so a slip a test claims is one the service really wrote
// and really read back.
func newPersonalSvc(t *testing.T, metered bool, mutate func(*svc, *Config)) *personalSvc {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	store := slipStore(t, db)
	dyn := &dynReader{doc: testBundleDoc(t), values: map[string]string{}}
	loader := &BundleLoader{Source: dyn}
	f := &personalSvc{slips: &slips{db: db, store: store}, dyn: dyn}
	var watch *wsMeterWatch
	if metered {
		meterDB, spendDB, fenced := wsFenced(t)
		wsSeedRecord(t, meterDB, wsCapUSD)
		f.meterDB, f.fenced = meterDB, fenced
		f.meter = wsMeter(t, spendDB)
		watch = &wsMeterWatch{meter: f.meter}
		f.watch = watch
	}
	f.svc = newSvcWith(t, func(sv *svc, cfg *Config) {
		cfg.Slips = store
		cfg.Bundles = loader
		auth, err := NewAuthenticator(AuthConfig{
			Project:          testProject,
			FirebaseProject:  testFirebase,
			Audience:         testAudience,
			JobAccounts:      []string{testJobSA, testSiblingSA},
			Keys:             sv.keys,
			JobKeys:          sv.jobKeys,
			Outcomes:         RTDBOutcomes{DB: sv.db},
			LauncherAccounts: []string{testLauncherAccount},
			Now:              func() time.Time { return sv.now },
		})
		if err != nil {
			t.Fatalf("the fixture's launcher class cannot be configured: %v", err)
		}
		cfg.Auth, sv.auth = auth, auth
		cfg.Resolver = &personalResolver{srv: func() *Server { return sv.server }, personal: personalReaderOf(t), meter: f.meter}
		if watch != nil {
			watch.next = cfg.Client.Transport
			cfg.Client = &http.Client{Transport: watch}
		}
		if mutate != nil {
			mutate(sv, cfg)
		}
	})
	return f
}

// claimedSlipAs is claimedSlip for a run whose LAUNCHER is the person given:
// the fixture's own run, granted by a launcher token whose verified subject is
// that person's, so the slip's owner — and with it the personal name the call
// is entitled to — is theirs (F11, D2962).
func (f *personalSvc) claimedSlipAs(t *testing.T, sub string, runtimeSeconds int64, allowed ...string) aSlip {
	t.Helper()
	bearer := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.launcherToken(func(c map[string]any) { c["sub"] = sub }))
	}
	resp, body := f.grantCall(t, grantBody(t, f.slug, f.run, testJobSA, runtimeSeconds, allowed...), bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the grant by another launcher is answered %d, want 200: %s", resp.StatusCode, body)
	}
	var ans SlipGrantResponse
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the grant's answer is not one this contract reads: %v", err)
	}
	if ans.Slip == "" {
		t.Fatal("the grant answered no slip id")
	}
	claimed := f.claimOK(t, ans.Slip, claimBody(t, ""))
	if claimed.ClaimSecret == "" {
		t.Fatal("the claim answered no claim secret")
	}
	return aSlip{id: ans.Slip, secret: claimed.ClaimSecret}
}

// personalName is the one personal name this plan serves, for the fixture's own
// launcher. It is built through the grammar (mustUserName) and never spelled,
// so no test can state a spelling the intake could never write.
func personalName(t *testing.T) string {
	t.Helper()
	return mustUserName(t, testLauncherSub, creds.PersonTokenName)
}

// servedBy is the value the upstream was handed on request i, read off the
// request itself: the one fact that says which store served the attempt.
func (s *svc) servedBy(t *testing.T, i int) string {
	t.Helper()
	seen := s.up.Seen()
	if i >= len(seen) {
		t.Fatalf("the upstream saw %d requests and the test asks for request %d", len(seen), i)
	}
	return strings.TrimPrefix(seen[i].Header.Get("Authorization"), "Bearer ")
}

// servedLast is servedBy for the request the upstream saw last, for a fixture
// that makes more than one call: an index would be a count the test has to keep
// in step with itself, and the count is the thing under test.
func (s *svc) servedLast(t *testing.T) string {
	t.Helper()
	n := s.up.Count()
	if n == 0 {
		t.Fatal("the upstream saw no request")
	}
	return s.servedBy(t, n-1)
}

// wsRoom is the workspace's figure as the fixture's meter reads it now.
func (f *personalSvc) wsRoom(t *testing.T) budget.WorkspaceRoom {
	t.Helper()
	if f.meter == nil {
		t.Fatal("the fixture holds no meter: this test asked for an installation's service")
	}
	room, err := f.meter.Room(context.Background())
	if err != nil {
		t.Fatalf("reading the workspace's room: %v", err)
	}
	return room
}

// bundleValue is the fixture's bundle entry for a credential NAME, which is
// what the bundle half of the resolution answers with. It is read back through
// the loader rather than spelled: fakeCred's text carries the name of the t it
// was called with, and a test that spelled it would be pinning which t that
// was.
func bundleValue(t *testing.T, s *svc, name string) string {
	t.Helper()
	b, err := s.server.cfg.Bundles.Load(context.Background())
	if err != nil {
		t.Fatalf("the fixture's bundle was not read: %v", err)
	}
	return entryValue(t, b, name)
}

// aRefusalLine is the one audit line a refused call wrote, held to the shape
// every line of this service is and to the fields the act carries. credential
// and tier are what the line records about the credential the refusal was over:
// empty where the call never got as far as a candidate (the switch refused it
// before the list was built), and the candidate's own name and tier where the
// call was refused in the forward path over one it had selected — the field a
// refusal line carries is what was KNOWN, and the name of a credential is not
// its value (audit.go's checked).
func aRefusalLine(t *testing.T, s *personalSvc, sl aSlip, typ, credential, tier string) map[string]any {
	t.Helper()
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	wantSlipLine(t, lines[0], PurposeForward, typ, map[string]any{
		"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
		"slip": sl.id, "owner": testLauncherSub, "credential": credential, "tier": tier,
	})
	return lines[0]
}

// ---------------------------------------------------------------------------
// Whose token
// ---------------------------------------------------------------------------

// TestAnotherPersonsRunIsNeverServedTheToken: D2962. The personal name is the
// SLIP's owner's and never the call's or the list's — the list says whether the
// person's own token may be served, not whose it is — so a slip naming somebody
// else's token is answered no candidate at all, and the container that token
// lives in is never read.
//
// Three readings of one rule: the reader's answer for a slip that names another
// person's token, the reader's answer for that person's own slip, and a whole
// call of a run they launched — which is served THEIR token, and whose answer
// names them as the token's owner (F16).
func TestAnotherPersonsRunIsNeverServedTheToken(t *testing.T) {
	personalSwitch(t, true, nil)
	s := newPersonalSvc(t, false, nil)
	theirs := mustUserName(t, testOtherSub, creds.PersonTokenName)
	// The container IS there and holds a value: what keeps the token from being
	// served is the owner rule and not an absence, so a reader that built the
	// name from anything but the slip's owner would find one here.
	s.dyn.values[theirs] = fakeCred(t, "theirs")

	t.Run("a slip naming somebody else's token answers no candidate", func(t *testing.T) {
		got, ok, err := personalReaderOf(t).Personal(personalCall(t, s), creds.Slip{
			ID: anIdNobodyWrote, Owner: testLauncherSub, Allowed: []string{theirs},
		})
		if err != nil {
			t.Fatalf("the reader refused a slip that names somebody else's token: %v", err)
		}
		if ok {
			t.Errorf("the reader answered the candidate %q, want none: the name is the slip owner's own", got.Name)
		}
	})

	t.Run("the other person's own slip is answered their token", func(t *testing.T) {
		got, ok, err := personalReaderOf(t).Personal(personalCall(t, s), creds.Slip{
			ID: anIdNobodyWrote, Owner: testOtherSub, Allowed: []string{theirs},
		})
		if err != nil {
			t.Fatalf("the reader refused the other person's own slip: %v", err)
		}
		if !ok {
			t.Fatal("the reader answered no candidate for a slip naming the owner's own token")
		}
		if got.Name != theirs || got.Tier != creds.TierA {
			t.Errorf("the candidate is %+v, want %q at tier A: a person's own token is one more subscription candidate", got, theirs)
		}
	})

	t.Run("a run they launched is served their token and the answer names them", func(t *testing.T) {
		sl := s.claimedSlipAs(t, testOtherSub, 3600, theirs)
		resp, body := s.slipCall(t, sl, "another-persons-run")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get(HeaderCredential); got != theirs {
			t.Fatalf("%s = %q, want %q: the run is served the token its own launcher stored", HeaderCredential, got, theirs)
		}
		if got, want := s.servedBy(t, 0), s.dyn.values[theirs]; got != want {
			t.Error("the upstream was handed a value that is not the other person's container")
		}
		owner, present, err := CredentialOwnerHeader(resp.Header)
		if err != nil {
			t.Fatalf("the answer's owner header: %v", err)
		}
		if !present || owner != testOtherSub {
			t.Errorf("%s = %q (present %v), want the slip owner's own sub %q", HeaderCredentialOwner, owner, present, testOtherSub)
		}
	})
}

// ---------------------------------------------------------------------------
// Whether
// ---------------------------------------------------------------------------

// TestTheSwitchOffServesNoPersonalToken: R9 and R11. The switch is the MEMBER's
// — one key of their published policy, read by this service per call from the
// call's own identity — and a call under a slip that names a personal token is
// REFUSED when it is off. The shared entries are not served in the token's
// place: a slip exists because the switch was on when it was granted, so a call
// meeting it off is a run the member turned the half off under, and moving that
// run to the shared entries would spend what the member just refused to spend.
func TestTheSwitchOffServesNoPersonalToken(t *testing.T) {
	sw := personalSwitch(t, false, nil)
	s := newPersonalSvc(t, false, nil)
	own := personalName(t)
	s.dyn.values[own] = fakeCred(t, "own")
	sl := s.claimedSlip(t, 3600, own)

	resp, body := s.slipCall(t, sl, "switch-off")
	if e := wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable); e.Message != personalSwitchOff {
		t.Errorf("the refusal is %q, want the switch-off sentence", e.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the shared entries are not served in the token's place", n)
	}
	if got := sw.projects(); len(got) != 1 || got[0] != testProject {
		t.Errorf("the switch was asked about %v, want exactly [%s]: it is the member's, read from the call's own identity", got, testProject)
	}
	aRefusalLine(t, s, sl, ErrorStateUnavailable, "", "")
}

// TestASwitchThatCannotBeReadRefusesTheSameWay: a policy that could not be read
// is never a switch that is off (C1). The two are different incidents for an
// operator and the same answer for the run — a refusal, with its own sentence —
// and neither is the shared entries.
func TestASwitchThatCannotBeReadRefusesTheSameWay(t *testing.T) {
	personalSwitch(t, true, errors.New("the member's policy could not be read"))
	s := newPersonalSvc(t, false, nil)
	own := personalName(t)
	s.dyn.values[own] = fakeCred(t, "own")
	sl := s.claimedSlip(t, 3600, own)

	resp, body := s.slipCall(t, sl, "switch-unreadable")
	if e := wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable); e.Message != personalSwitchUnreadable {
		t.Errorf("the refusal is %q, want the unreadable-policy sentence", e.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
	aRefusalLine(t, s, sl, ErrorStateUnavailable, "", "")
}

// ---------------------------------------------------------------------------
// A token that is not there, and one that cannot be read
// ---------------------------------------------------------------------------

// TestAMissingPersonalTokenRefuses: D2974, and WC5's red step. A person who has
// stored no token is REFUSED and told what to run; the call never falls to a
// bundle entry, which for this name is the shared subscription token — a
// credential whose own allow-list nothing asked and which the meter would not
// have counted, because the tier is the candidate's.
//
// **What the red step actually measured** (this file's two refusals taken out,
// this test run, the refusals put back): the sentence a person meets is "the
// selected credential is not in the bundle" — the resolution's own refusal and
// NOT the bundle's entry, because the bundle's index holds the RESOLVED account
// name (config.DefaultClaudeAccount) while the fall names the credential the
// grammar holds (config.ClaudeOAuthToken). The entry the plan's red step names
// is reached by that same fall where an account is literally named after the
// credential, which TestAMissingPersonalTokenRefusesEvenWhereTheBundleHoldsThatName
// pins — and there the fall would serve that account's token. Either way the
// refusal is the change, and the class is a rule rather than the index's
// happening to miss.
func TestAMissingPersonalTokenRefuses(t *testing.T) {
	personalSwitch(t, true, nil)
	s := newPersonalSvc(t, false, nil)
	own := personalName(t)
	sl := s.claimedSlip(t, 3600, own)

	resp, body := s.slipCall(t, sl, "no-token")
	e := wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)
	if e.Message != ErrPersonalTokenAbsent.Error() {
		t.Errorf("the refusal is %q, want the sentence naming the command that sets a token", e.Message)
	}
	if !strings.Contains(e.Message, "fugaro secrets user set") {
		t.Errorf("the refusal %q names no command a person could run", e.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: a call with no token to serve is never moved to the shared entries", n)
	}
	if reads := s.dyn.reads; len(reads) != 1 || reads[0] != own {
		t.Errorf("the service read %v, want exactly [%s]: the read is by the person's own name, once", reads, own)
	}
	// The line carries the candidate the call was refused over: the refusal is
	// the forward path's, and what it records is what was known — the name of a
	// credential, which is not its value, and the tier it was selected at.
	aRefusalLine(t, s, sl, ErrorStateUnavailable, own, wireTier(creds.TierA))
}

// TestAMissingPersonalTokenRefusesEvenWhereTheBundleHoldsThatName is D2974's
// harm made concrete, and the reason the absent token is a class of its own
// rather than a sentence put on the fall-through.
//
// The personal candidate's held credential name is config.ClaudeOAuthToken, the
// DEFAULT account's secret, and an account may legally be NAMED after it — the
// bundle reserves that spelling for one credential only where it is the git
// entry's. A bundle holding such an account indexes that name, so R6's fall
// FINDS it: a call whose slip named a person's token would be served that
// account's token, through no allow-list of its own and counted as unbilled,
// because the tier is the candidate's. That is the whole of what D2974 forbids,
// and this is the state it is about.
func TestAMissingPersonalTokenRefusesEvenWhereTheBundleHoldsThatName(t *testing.T) {
	personalSwitch(t, true, nil)
	s := newPersonalSvc(t, false, nil)
	own := personalName(t)
	// The account named exactly as the credential the personal name holds: the
	// one document the fall-through would be answered by.
	shadow := config.ClaudeAccountSecret(creds.PersonTokenName)
	s.dyn.doc = append(testBundleDoc(t), []byte(shadow+": "+fakeCred(t, "shadow")+"\n")...)
	sl := s.claimedSlip(t, 3600, own)

	resp, body := s.slipCall(t, sl, "no-token-shadowed")
	if e := wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable); e.Message != ErrPersonalTokenAbsent.Error() {
		t.Errorf("the refusal is %q, want the absent-token sentence: the fall would serve the account named after the credential", e.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: no shared entry is served in the person's token's place", n)
	}
	aRefusalLine(t, s, sl, ErrorStateUnavailable, own, wireTier(creds.TierA))
}

// TestAFailedReadOfAPersonsTokenRefuses: R7, and the same rule D143 states for
// every other per-secret read. A container that could not be read is neither an
// absence nor a fallback, and the sentence is the read's own class rather than
// the absent one: a person whose token could not be read has stored one, and
// telling them to store one would send them to fix the wrong thing.
func TestAFailedReadOfAPersonsTokenRefuses(t *testing.T) {
	personalSwitch(t, true, nil)
	s := newPersonalSvc(t, false, nil)
	own := personalName(t)
	s.dyn.err = errors.New("the container could not be read")
	sl := s.claimedSlip(t, 3600, own)

	resp, body := s.slipCall(t, sl, "unreadable-token")
	e := wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)
	if e.Message != perSecretRefusal {
		t.Errorf("the refusal is %q, want the read's own class %q", e.Message, perSecretRefusal)
	}
	if e.Message == ErrPersonalTokenAbsent.Error() {
		t.Error("a read that failed is answered as an absence: the two are different acts for the person who reads it")
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the call is refused rather than served the bundle's value", n)
	}
	if reads := s.dyn.reads; len(reads) != 1 || reads[0] != own {
		t.Errorf("the service read %v, want exactly [%s]", reads, own)
	}
	aRefusalLine(t, s, sl, ErrorStateUnavailable, own, wireTier(creds.TierA))
}

// ---------------------------------------------------------------------------
// What tier, and where in the list
// ---------------------------------------------------------------------------

// TestAnExhaustedTokenMovesToTheSharedEntries: R7's order and D3144. The
// person's token is the FIRST candidate of the call — the assertion on request
// 0 is the acceptance's "the offering puts that token first" — and a 429 on it
// is the cascade's existing rule: the attempt is marked exhausted and the call
// moves on to the next admitted candidate — one of the bundle's own
// subscription entries — with the move named on the answer (C1.9's Switch).
func TestAnExhaustedTokenMovesToTheSharedEntries(t *testing.T) {
	personalSwitch(t, true, nil)
	s := newPersonalSvc(t, false, nil)
	own := personalName(t)
	rotated := fakeCred(t, "own")
	s.dyn.values[own] = rotated
	sl := s.claimedSlip(t, 3600, own)
	s.up.Script = []anthropicfake.Reply{
		{
			Status: http.StatusTooManyRequests,
			Header: http.Header{"Retry-After": []string{"3600"}},
			Body:   `{"type":"error","error":{"type":"` + rateLimitEventType + `","message":"usage limit reached"}}`,
		},
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.slipCall(t, sl, "exhausted-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the second candidate's 200: %s", resp.StatusCode, body)
	}
	if n := s.up.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want 2: a 429 before any byte is retried once", n)
	}
	if got := s.servedBy(t, 0); got != rotated {
		t.Error("the first attempt did not carry the person's own token: the candidate is not first in the list")
	}
	sw, ok, err := SwitchHeader(resp.Header)
	if err != nil {
		t.Fatalf("the switch header is not one this contract can read: %v", err)
	}
	if !ok {
		t.Fatal("the answer carries no switch header: this call moved")
	}
	if sw.From != own || sw.Reason != ReasonExhausted {
		t.Errorf("switch = %+v, want the person's token -> another candidate because it was exhausted", sw)
	}
	// WHICH shared entry the move lands on is the cascade's tie-break among the
	// entries this call is admitted to and not this test's to spell: the point
	// is that it is one of the BUNDLE's and never the person's own container
	// again. The name the answer carries is what the retry went out on, so the
	// assertion is made against it rather than against a name written here.
	if sw.To == own || sw.To == "" {
		t.Fatalf("the call moved to %q, want a shared entry of the bundle: an exhausted token is skipped, never retried", sw.To)
	}
	if got := s.servedBy(t, 1); got != bundleValue(t, s.svc, sw.To) {
		t.Error("the retry did not carry the bundle's own entry for the credential the answer names")
	}
	if got := resp.Header.Get(HeaderCredential); got != sw.To {
		t.Errorf("%s = %q, want %q: the answer names the credential that served the call", HeaderCredential, got, sw.To)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q: the shared entry the call moved to is a subscription one", HeaderTier, got, TierA)
	}
	if m, ok := s.state.mark(own); !ok || m.State != creds.StateExhausted {
		t.Errorf("the person's token is marked %q (found %v), want an exhaustion until the reset the answer named", m.State, ok)
	} else if want := s.now.Add(time.Hour); !m.Until.Equal(want) {
		t.Errorf("the mark runs out at %v, want %v: the reset the answer named", m.Until, want)
	}
}

// TestThePaidCallAfterTheMoveIsReserved: D2965 and D3144, and the one pair of
// facts only the meter can state. A call served the person's own token is a
// subscription call: it reserves nothing of the workspace's money, because the
// cap follows the credential actually served. The attempt that follows it — the
// paid key the move lands on — is reserved before it goes out, exactly as every
// other paid call of a member's run is (WS8).
//
// The two readings are the month as the meter saw it at each upstream request,
// which is the only vantage point from which "before the call" is a fact.
func TestThePaidCallAfterTheMoveIsReserved(t *testing.T) {
	personalSwitch(t, true, nil)
	s := newPersonalSvc(t, true, func(sv *svc, _ *Config) {
		// The repository is admitted no shared subscription account, so the
		// only candidate the move can land on is the paid key.
		delete(sv.policy.admissions, sv.slug)
	})
	own := personalName(t)
	rotated := fakeCred(t, "own")
	s.dyn.values[own] = rotated
	sl := s.claimedSlip(t, 3600, own)
	paid := s.tierBName(t)
	s.up.Script = []anthropicfake.Reply{
		{
			Status: http.StatusTooManyRequests,
			Header: http.Header{"Retry-After": []string{"3600"}},
			Body:   `{"type":"error","error":{"type":"` + rateLimitEventType + `","message":"usage limit reached"}}`,
		},
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.slipCall(t, sl, "paid-after-the-move")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the paid key's 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Fatalf("%s = %q, want %q: this test is about the paid call the move lands on", HeaderTier, got, TierB)
	}
	if got := resp.Header.Get(HeaderCredential); got != paid {
		t.Fatalf("%s = %q, want %q", HeaderCredential, got, paid)
	}
	if n := s.up.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want the person's token and the paid key", n)
	}
	if got := s.servedBy(t, 0); got != rotated {
		t.Error("the first attempt did not carry the person's own token")
	}
	if got := s.watch.reservedAt(t, 0).Reserved; got != 0 {
		t.Errorf("the person's own token had %v reserved before its call, want nothing: a subscription token is not a cost the workspace reserves", got.USD())
	}
	callWorst := wsWorst(t, testModel, []byte(callBody("paid-after-the-move")), 16)
	if got := s.watch.reservedAt(t, 1).Reserved; got != callWorst {
		t.Errorf("the paid call had %v reserved, want its own worst case %v: the cap follows the credential actually served",
			got.USD(), callWorst.USD())
	}
	room := s.wsRoom(t)
	if want := wsRow(t, testModel).Rates.Cost(testUsage); room.Settled != want {
		t.Errorf("the month settled at %v, want %v: the exhausted attempt was not billed and the paid call was", room.Settled.USD(), want.USD())
	}
	if room.Reserved != 0 {
		t.Errorf("the month still holds %v of room after both attempts, want none", room.Reserved.USD())
	}
}

// turnTheCounter puts the project's tie-breaking counter at n, which is where a
// workspace's is as soon as its runs have called anything: it advances by one on
// every selection that had to choose within a tier (creds/roundrobin, F6), so a
// fixture that leaves it at zero is a workspace whose first call is the call
// under test — and the two rules that pick a member by position agree with the
// offered order at zero by luck alone. Seeded through the store's own lock
// because the service is served over a listener (the handler runs on its own
// goroutine) and the counter is the store's.
func (s *svc) turnTheCounter(n int64) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	s.state.rr = n
}

// TestThePersonalTokenWinsTheTieBreak: R7 and D3144, and the state the
// candidate's own mark exists for. A person's token is SERVED to a call their
// slip entitles, and where the offered order puts it is not what says so: within
// a tier an exact tie is broken by the project's counter (creds'
// leastRecentlyExhausted), so a token offered as one more tier-A member beside
// the bundle's entries reaches a call about one time in N. At this counter's
// first turn the answer before the rule was the bundle's default entry, which is
// what this test is written against.
func TestThePersonalTokenWinsTheTieBreak(t *testing.T) {
	personalSwitch(t, true, nil)
	s := newPersonalSvc(t, false, nil)
	own := personalName(t)
	rotated := fakeCred(t, "own")
	s.dyn.values[own] = rotated
	sl := s.claimedSlip(t, 3600, own)
	s.turnTheCounter(1)

	resp, body := s.slipCall(t, sl, "tie-break")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != own {
		t.Fatalf("%s = %q, want %q: a token the slip entitles the call to is served while it can serve, and the counter's turn is not the question", HeaderCredential, got, own)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q", HeaderTier, got, TierA)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: the call went out on the person's own token and did not move", n)
	}
	if got := s.servedLast(t); got != rotated {
		t.Errorf("the upstream was handed %q, want the person's own container", got)
	}
}

// TestThePersonalTokenWinsARoundRobinRotation: the same rule under the other
// within-tier rule (MC7). A project whose crafts go out on a pool asking for the
// rotation serves that pool's members in turn, and a call under a slip naming the
// person's own token is still served THAT token: the person's own container is
// not a member of any pool, so it is neither offered as one nor given a turn, and
// a rotation that reached it by position would hand the runs a person launched to
// a shared subscription in (N-1) calls out of N (R7, D3144).
//
// The counter is seeded off zero for the reason the test above seeds it: at the
// rotation's first turn the member the rotation reached would be the pool's
// second, which is what the assertion would otherwise be reading.
func TestThePersonalTokenWinsARoundRobinRotation(t *testing.T) {
	personalSwitch(t, true, nil)
	s := newPersonalSvc(t, false, func(sv *svc, _ *Config) {
		sv.policy.shared = decodeTestPolicy(t, poolNode(rrBlock(
			`"fallback": "auto", "stop_after_exhaustion_per_window": false, "concurrency_per_token": 0`,
			testAccount, testPoolAccount)))
		sv.policy.admissions[sv.slug] = decodeTestChild(t, testChildDoc(testAccount, testPoolAccount))
	})
	// The bundle holds the pool's two subscriptions beside the default entry
	// the fixture's own document ends at, so the rotation really has two shared
	// members to reach. It is the document and not the values that changes: the
	// pool's members are read from it, which is where a pool's names come from
	// (poolBundleDoc).
	s.dyn.doc = poolBundleDoc(t)
	own := personalName(t)
	rotated := fakeCred(t, "own")
	s.dyn.values[own] = rotated
	sl := s.claimedSlip(t, 3600, own)
	s.turnTheCounter(1)

	resp, body := s.slipCall(t, sl, "rotation")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != own {
		t.Fatalf("%s = %q, want %q: a token the slip entitles the call to is served while it can serve, and a pool's rotation is not the question", HeaderCredential, got, own)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q", HeaderTier, got, TierA)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: the call went out on the person's own token and did not move", n)
	}
	if got := s.servedLast(t); got != rotated {
		t.Errorf("the upstream was handed %q, want the person's own container", got)
	}
}

// ---------------------------------------------------------------------------
// The path that must not change
// ---------------------------------------------------------------------------

// TestAnInstallationStillFallsToTheBundle: A3, D2966. An installation has no
// personal half — its scope does not implement C2's PersonScope, so no personal
// candidate is ever built for a call of one — and every path it does have is
// what it was: a bundle credential is served as before, a slip naming a
// person's token fences nothing (the name is not in an installation's offering
// at all, so the fence has nothing to drop and nothing to refuse), and the
// resolution's fall-through for a name of the grammar is the bundle's entry for
// the credential it holds, which is D2974's own answer (b) read where it
// applies.
//
// The switch is left ON for this test on purpose: an installation's answers are
// the same whether the switch of some member somewhere is on or off, and the
// last assertion is that no call of this test asked it anything.
func TestAnInstallationStillFallsToTheBundle(t *testing.T) {
	sw := personalSwitch(t, true, nil)
	s := newSlips(t, testLauncherAccount)
	s.serveCalls(3)

	t.Run("a bundle credential is served exactly as before", func(t *testing.T) {
		sl := s.claimedSlip(t, 3600, testSlipCredential)
		resp, body := s.slipCall(t, sl, "installation-bundle")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get(HeaderCredential); got != config.DefaultClaudeAccount {
			t.Errorf("%s = %q, want %q", HeaderCredential, got, config.DefaultClaudeAccount)
		}
		if got := resp.Header.Values(HeaderCredentialOwner); len(got) != 0 {
			t.Errorf("a call served a bundle entry carries %s = %q, want none", HeaderCredentialOwner, got)
		}
	})

	t.Run("a slip naming a person's token changes nothing", func(t *testing.T) {
		// The name is granted — the owner rule admits the launcher's own — and
		// the call is then served the bundle exactly as a slip naming nothing
		// would be: an installation's offering is its bundle's own entries
		// (installationOffering), so no candidate of the grammar is there for
		// the slip's fence to keep, and none is built for one either.
		sl := s.claimedSlip(t, 3600, personalName(t))
		resp, body := s.slipCall(t, sl, "installation-personal-name")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get(HeaderCredential); got != config.DefaultClaudeAccount {
			t.Errorf("%s = %q, want %q: an installation serves its bundle", HeaderCredential, got, config.DefaultClaudeAccount)
		}
		if got := resp.Header.Values(HeaderCredentialOwner); len(got) != 0 {
			t.Errorf("the answer carries %s = %q, want none", HeaderCredentialOwner, got)
		}
	})

	t.Run("a name of the grammar still falls to the bundle's entry", func(t *testing.T) {
		// The fall-through itself, on the one path that can reach it for a name
		// of the grammar: a candidate stated by hand. No route of an
		// installation builds one — F4's finding, and the reason the assertion
		// above is about a fence with nothing to keep — so a test that means to
		// ask the resolution has to state the candidate the way
		// TestBundleEntryStillServes does.
		//
		// The name is a rotation of an ACCOUNT's token and not the personal one:
		// the personal name is the one name of the grammar that never falls
		// (D2974), and what this subtest pins is that the rule is that narrow.
		rotated := mustUserName(t, testLauncherSub, testAccount)
		ctx := context.Background()
		b, err := s.server.cfg.Bundles.Load(ctx)
		if err != nil {
			t.Fatalf("the fixture's bundle was not read: %v", err)
		}
		if got := s.server.perSecrets(); got != nil {
			t.Fatalf("perSecrets answered %#v for a source that reads no containers, want none", got)
		}
		rec := httptest.NewRecorder()
		sel := &creds.Selector{
			Store:    s.state,
			Settings: settingsOf(s.policy.shared),
			Now:      func() time.Time { return s.now },
		}
		s.server.forward(ctx, rec, providerCall(t, MessagesPath), sel,
			Identity{Slug: s.slug, Run: s.run, Job: s.job, JobAccount: testJobSA, JobClass: ClassRepository},
			b, []creds.Credential{{Name: rotated, Tier: creds.TierA}}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("the call is answered %d, want the bundle's 200: %s", rec.Code, rec.Body)
		}
		if got := rec.Header().Get(HeaderCredential); got != rotated {
			t.Errorf("%s = %q, want %q: the answer still names what the run asked for", HeaderCredential, got, rotated)
		}
		if got, want := s.servedLast(t), bundleValue(t, s.svc, testAccount); got != want {
			t.Error("the call was not served the bundle's entry for the credential the rotation was of")
		}
	})

	if got := sw.projects(); len(got) != 0 {
		t.Errorf("an installation's calls asked the personal switch %v, want nothing: the reader is reached only through a scope that has the personal half on it", got)
	}
}
