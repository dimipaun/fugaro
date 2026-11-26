package cli

// accounts_pool_personal_test.go is PL9's own file: the pools a person keeps for
// themselves in a project, and the token that goes into one.
//
// Neither command takes a principal from the caller. A personal pool is recorded
// at the installation's credentials service through a route keyed on the subject
// the person's own Google ID token carries (credsvc.PersonalPoolPath), and the
// token is written into the project's per-user container for that subject through
// the intake (intake.ScopeUser) — so no field of either call can name whose pool
// it is, and a member is a container of the caller's own or it is refused there
// (R9, R26).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/routing"
)

// poolSeen is one personal-pool call the commands made, as the fake below
// recorded it.
type poolSeen struct {
	URL string
	Req credsvc.PersonalPoolRequest
}

// fakePersonalPool replaces the one seam the declare call goes through. No test
// mints an identity token or reaches a service.
func fakePersonalPool(t *testing.T, answer credsvc.PersonalPoolRecord) *[]poolSeen {
	t.Helper()
	seen := &[]poolSeen{}
	prev := accountsPoolPersonalDo
	accountsPoolPersonalDo = func(_ context.Context, url string, req credsvc.PersonalPoolRequest) (credsvc.PersonalPoolRecord, error) {
		*seen = append(*seen, poolSeen{URL: url, Req: req})
		if answer.Pool == "" {
			answer.Pool = req.Pool
		}
		if answer.Rule == "" {
			answer.Rule = routing.Rule(req.Rule).RuleOrDefault("")
		}
		answer.Members = req.Members
		return answer, nil
	}
	t.Cleanup(func() { accountsPoolPersonalDo = prev })
	return seen
}

// fakePersonalPoolRead replaces the read seam with one pool the caller has
// declared, holding members.
func fakePersonalPoolRead(t *testing.T, pool string, members []string) *int {
	t.Helper()
	calls := new(int)
	prev := accountsPoolPersonalReadDo
	accountsPoolPersonalReadDo = func(_ context.Context, _, name string) (credsvc.PersonalPoolRecord, bool, error) {
		*calls++
		if name != pool {
			return credsvc.PersonalPoolRecord{}, false, nil
		}
		return credsvc.PersonalPoolRecord{
			Principal: routing.Principal{Kind: routing.PrincipalPerson, ID: "102938475610293847561"},
			Project:   "aurora",
			Pool:      pool,
			Rule:      routing.RuleFailover,
			Members:   members,
		}, true, nil
	}
	t.Cleanup(func() { accountsPoolPersonalReadDo = prev })
	return calls
}

// TestAPersonCreatesTheirOwnPool is PL9's red step: `fugaro accounts pool add
// --personal <name>` records a pool of the caller's OWN at the installation's
// credentials service, with its rule and its members — and the flags a config
// pool has, and the config itself, are none of its business.
func TestAPersonCreatesTheirOwnPool(t *testing.T) {
	intakeFixture(t)
	want := credsvc.PersonalPoolRecord{
		Principal: routing.Principal{Kind: routing.PrincipalPerson, ID: "102938475610293847561"},
		Project:   "aurora",
	}
	seen := fakePersonalPool(t, want)

	out, _, err := execute(t, "accounts", "pool", "add", "mine", "--personal", "--rule", "round-robin", "one", "two")
	if err != nil {
		t.Fatalf("accounts pool add --personal: %v\n%s", err, out)
	}
	if len(*seen) != 1 {
		t.Fatalf("the command made %d calls, want one", len(*seen))
	}
	got := (*seen)[0]
	if got.URL != intakeTestCredentialsURL {
		t.Errorf("the pool was recorded at %s, want the installation's own credentials service (%s): the address is read from the project's config and never from a flag", got.URL, intakeTestCredentialsURL)
	}
	if got.Req.Pool != "mine" || got.Req.Rule != "round-robin" || strings.Join(got.Req.Members, ",") != "one,two" {
		t.Errorf("the call sent %#v, want the pool, its rule and its members in order", got.Req)
	}
	if !strings.Contains(out, "mine") || !strings.Contains(out, "one, two") {
		t.Errorf("the answer does not name the pool and the members it walks:\n%s", out)
	}
	// The wire carries names and no value (F19), and no field a principal could
	// be written in: whose pool it is, is the subject the service verified.
	body, err := json.Marshal(got.Req)
	if err != nil {
		t.Fatalf("marshalling the request: %v", err)
	}
	if strings.Contains(string(body), "principal") {
		t.Errorf("the request carries a principal: %s", body)
	}

	// The rule is set by a second declare, and the member list given is the
	// WHOLE list: a member left out is one the pool no longer walks, which is
	// how a member is removed.
	*seen = (*seen)[:0]
	if _, _, err := execute(t, "accounts", "pool", "add", "mine", "--personal", "--rule", "ordered", "two"); err != nil {
		t.Fatalf("re-declaring the pool: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("the second declare made %d calls, want one", len(*seen))
	}
	if got := (*seen)[0].Req; got.Rule != "ordered" || strings.Join(got.Members, ",") != "two" {
		t.Errorf("the second declare sent %#v, want the whole list with `one` gone", got)
	}

	// The flags a personal pool has no field for are refused rather than ignored,
	// and each refusal comes before anything is sent.
	*seen = (*seen)[:0]
	for _, args := range [][]string{
		{"accounts", "pool", "add", "mine", "--personal", "--owner", "person@example.invalid"},
		{"accounts", "pool", "add", "mine", "--personal", "--allow-user", "person@example.invalid"},
		{"accounts", "pool", "add", "mine", "--personal", "--service-account", "sa@proj.iam.gserviceaccount.com"},
		{"accounts", "pool", "add", "mine", "--personal", "--strategy", "failover"},
		{"accounts", "pool", "add", "mine", "--personal", "--layer", "local"},
		{"accounts", "pool", "add", "mine", "--personal", "--rule", "sideways"},
		{"accounts", "pool", "add", "mine", "--personal", "Not_A_Member"},
		{"accounts", "pool", "add", "Not_A_Pool", "--personal"},
	} {
		if _, _, err := execute(t, args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("a refused call reached the service: %#v", *seen)
	}

	// And the person's own session is the whole of who may run it: an agent's
	// session and a run's own process are refused before a byte is sent.
	for _, env := range []string{"CLAUDECODE", "FUGARO_RUN"} {
		t.Run(env, func(t *testing.T) {
			intakeFixture(t)
			t.Setenv(env, "1")
			seen := fakePersonalPool(t, want)
			if _, _, err := execute(t, "accounts", "pool", "add", "mine", "--personal", "one"); err == nil {
				t.Fatalf("a personal pool was recorded with %s set", env)
			} else if !strings.Contains(err.Error(), env) {
				t.Errorf("the refusal does not name the marker that refused it: %v", err)
			}
			if len(*seen) != 0 {
				t.Errorf("%s reached the service: the refusal is the client's own and it comes first", env)
			}
		})
	}
}

// TestNobodyElseFillsAPersonalPool: the token a person puts in their own pool is
// stored in THEIR container, through the intake that derives the owner from the
// verified identity and never from anything the request says (R3, R9), and the
// pool is only ever the caller's own — the declare call has no field a principal
// could be written in, and a pool the caller has not declared is one nothing is
// stored for.
func TestNobodyElseFillsAPersonalPool(t *testing.T) {
	intakeFixture(t)
	seen := fakePersonalPool(t, credsvc.PersonalPoolRecord{})
	read := fakePersonalPoolRead(t, "mine", []string{"one"})
	calls := fakeIntakeCalls(t, func(c secretsIntakeCall) {
		if a, ok := c.Out.(*intake.SecretAnswer); ok {
			*a = intake.SecretAnswer{ID: "fugaro_u_102938475610293847561_two", Version: "3"}
		}
	})

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "two", "--pool", "mine", "--personal")
	if err != nil {
		t.Fatalf("accounts add --pool mine --personal: %v\n%s", err, out)
	}
	if *read != 1 {
		t.Fatalf("the pool was read %d times, want once: a member is added to the list the pool already walks", *read)
	}
	if len(*calls) != 1 {
		t.Fatalf("the command made %d intake calls, want one", len(*calls))
	}
	call := (*calls)[0]
	if !strings.Contains(call.URL, "fugaro-intake-") {
		t.Errorf("the token was stored at %s, want the installation's own intake service (the credentials service's own address with its label swapped)", call.URL)
	}
	req, ok := call.Call.Body.(intake.CreateRequest)
	if !ok {
		t.Fatalf("the intake call carries %T, want a create request", call.Call.Body)
	}
	if req.Scope != intake.ScopeUser {
		t.Errorf("the token was stored with scope %q, want %q: a pool of your own holds containers of your own (R9)", req.Scope, intake.ScopeUser)
	}
	if req.Pool != "" {
		t.Errorf("the token was stored with pool %q: a per-user container is not a pool member's container", req.Pool)
	}
	if req.Name != "two" {
		t.Errorf("the token was stored as %q, want the name given", req.Name)
	}
	if strings.Contains(out, addToken()) || strings.Contains(call.Call.What, addToken()) {
		t.Errorf("a value reached a line of this command's output:\n%s", out)
	}

	if len(*seen) != 1 {
		t.Fatalf("the declare made %d calls, want one", len(*seen))
	}
	declared := (*seen)[0].Req
	if declared.Pool != "mine" || strings.Join(declared.Members, ",") != "one,two" {
		t.Errorf("the pool was re-declared as %#v, want the members it had plus the new one", declared)
	}
	if declared.Rule != string(routing.RuleFailover) {
		t.Errorf("the re-declare sent rule %q, want the rule the pool already had", declared.Rule)
	}
	if !strings.Contains(out, "two") || !strings.Contains(out, "mine") {
		t.Errorf("the answer does not name the member and the pool:\n%s", out)
	}

	// A pool the caller has not declared is one nothing is stored for: the token
	// is not read and the intake is not called.
	*seen = (*seen)[:0]
	*calls = (*calls)[:0]
	if _, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "two", "--pool", "theirs", "--personal"); err == nil {
		t.Fatal("a token was stored for a pool the caller has not declared")
	} else if !strings.Contains(err.Error(), "accounts pool add") {
		t.Errorf("the refusal does not name the command that declares the pool: %v", err)
	}
	if len(*calls) != 0 || len(*seen) != 0 {
		t.Errorf("a call was made for a pool the caller does not have: %#v %#v", *calls, *seen)
	}

	// The flags of the config half are refused rather than ignored: this path
	// writes no config at all.
	for _, args := range [][]string{
		{"accounts", "add", "two", "--personal"},
		{"accounts", "add", "two", "--pool", "mine", "--personal", "--repos", "acme/app"},
		{"accounts", "add", "two", "--pool", "mine", "--personal", "--profile", "standard"},
		{"accounts", "add", "two", "--pool", "mine", "--personal", "--type", "claude-oauth"},
		{"accounts", "add", "two", "--pool", "mine", "--personal", "--yes"},
	} {
		if _, _, err := executeStdin(t, addToken()+"\n", args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	if len(*calls) != 0 || len(*seen) != 0 {
		t.Errorf("a refused call reached a service: %#v %#v", *calls, *seen)
	}
}
